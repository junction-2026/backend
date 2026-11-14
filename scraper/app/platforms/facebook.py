"""Facebook public pages: headless chromium (Playwright), reading the GraphQL responses the page itself requests.

Public pages only, no login. Assumed GraphQL shapes (the walker looks for these node types anywhere in a response):
  profile  {"__typename": "Page"|"User", "id", "name", "url"|"vanity", "followers_count", "description", "is_verified"}
  post     {"__typename": "Story", "post_id"|"id", "owner": {"id"}, "message": {"text"}, "creation_time", "url",
            "feedback": {"reaction_count": {"count"}, "comment_count": {"total_count"}, "share_count": {"count"}}}
  comment  {"__typename": "Comment", "id", "body": {"text"}, "author": {"id", "name", "url"}, "created_time",
            "comment_parent": {"id"}, "feedback": {...}}
A "User" profile node means a personal profile, which is never scraped.
"""
import asyncio
import json
import logging
import re
from datetime import datetime, timezone

from playwright.async_api import Error as PlaywrightError
from playwright.async_api import TimeoutError as PlaywrightTimeout
from playwright.async_api import async_playwright

from ..errors import ScrapeError
from ..models import Author, Comment, Post, ScrapeResult, ScrapeWarning
from ..textutil import to_rfc3339
from .base import Recognized, ScrapeRequest

NAME = "facebook"
log = logging.getLogger("scraper.facebook")
_RESERVED = {"groups", "events", "watch", "marketplace", "share", "sharer.php", "photo", "photo.php",
             "permalink.php", "story.php", "login", "login.php", "people", "pages", "gaming", "reel",
             "reels", "stories", "hashtag", "help", "policies", "public", "search", "videos", "video.php"}
_BUDGET = 0.8        # share of the upstream timeout the page work may use; the rest is for cleanup and parsing
_MAX_ROUNDS = 12     # scroll rounds for history
_UNAVAILABLE = ("isn't available", "isn’t available", "may have been removed")


def recognize(u) -> Recognized | None:
    s = u.segments
    if u.host != "facebook.com" or not s:
        return None
    if s[0] == "profile.php":
        raise ScrapeError("unsupported_account_type", "Facebook personal profiles are not scraped",
                          platform=NAME, account_type="personal_profile")
    if s[0] in _RESERVED:
        return None
    if len(s) == 1:
        return Recognized(NAME, "profile", {"page": s[0]})
    if len(s) == 3 and s[1] == "posts":
        return Recognized(NAME, "post", {"page": s[0], "post_id": s[2]})
    return None


# ---- GraphQL walking -------------------------------------------------------------------------------------------

def _walk(o):
    if isinstance(o, dict):
        yield o
        for v in o.values():
            yield from _walk(v)
    elif isinstance(o, list):
        for v in o:
            yield from _walk(v)


def _first(d: dict, *paths: str):
    for p in paths:
        v = d
        for k in p.split("."):
            v = v.get(k) if isinstance(v, dict) else None
        if v is not None:
            return v
    return None


def _int(v) -> int | None:
    return v if isinstance(v, int) and not isinstance(v, bool) else None


def _ts(v) -> str | None:
    try:
        return to_rfc3339(datetime.fromtimestamp(float(v), timezone.utc))
    except (TypeError, ValueError, OverflowError, OSError):
        return None


def _slug(url) -> str:
    return str(url or "").split("?")[0].rstrip("/").rsplit("/", 1)[-1].lower()


def _profile_node(nodes: list[dict], page: str) -> dict | None:
    named = [n for n in nodes if n.get("__typename") in ("Page", "User") and n.get("name")]
    mine = [n for n in named if page.lower() in (_slug(n.get("url")), str(n.get("vanity") or "").lower())]
    return (mine or [n for n in named if n["__typename"] == "Page"] or [None])[0]


def _post(n: dict) -> Post:
    return Post(id=str(n.get("post_id") or n["id"]), text=str(_first(n, "message.text") or "").strip(),
                created_at=_ts(n.get("creation_time")), url=n.get("url") or n.get("wwwURL"),
                likes=_int(_first(n, "feedback.reaction_count.count")),
                replies=_int(_first(n, "feedback.comment_count.total_count")),
                reposts=_int(_first(n, "feedback.share_count.count")))


def _comment(n: dict, post_id: str) -> Comment:
    a = n.get("author") or {}
    return Comment(id=str(n["id"]), text=str(_first(n, "body.text") or "").strip(), created_at=_ts(n.get("created_time")),
                   url=None, likes=_int(_first(n, "feedback.reaction_count.count")),
                   replies=_int(_first(n, "feedback.reply_count.count")), reposts=None,
                   author=Author(username=_slug(a.get("url")) or str(a.get("id") or ""), display_name=a.get("name")),
                   in_reply_to_id=str(_first(n, "comment_parent.id") or post_id))


def _by_id(nodes: list[dict], typename: str) -> dict[str, dict]:
    out: dict[str, dict] = {}
    for n in nodes:
        if n.get("__typename") == typename and (n.get("post_id") or n.get("id")):
            out.setdefault(str(n.get("post_id") or n["id"]), n)
    return out


# ---- browser ---------------------------------------------------------------------------------------------------

async def _load(request: ScrapeRequest, path: str) -> tuple[list[dict], str]:
    """Open the page, collect GraphQL JSON documents (scrolling for history) -> (documents, visible body text)."""
    loop = asyncio.get_running_loop()
    deadline = loop.time() + request.timeout_s * _BUDGET
    docs: list[dict] = []
    tasks: list[asyncio.Task] = []

    async def read(resp):
        try:
            text = await resp.text()
        except Exception:
            return
        for line in text.splitlines():  # batched answers are one JSON document per line
            try:
                docs.append(json.loads(line))
            except ValueError:
                pass

    def on_response(resp):
        if "/api/graphql" in resp.url:
            tasks.append(asyncio.ensure_future(read(resp)))

    async def settle():
        await asyncio.gather(*tasks, return_exceptions=True)

    try:
        async with async_playwright() as pw:
            browser = await pw.chromium.launch(headless=True, args=["--no-sandbox", "--disable-dev-shm-usage"])
            try:
                page = await (await browser.new_context(locale="en-US")).new_page()
                page.on("response", on_response)
                resp = await page.goto(f"https://www.facebook.com/{path}", wait_until="domcontentloaded",
                                       timeout=max(1.0, (deadline - loop.time()) * 1000))
                if resp is not None and resp.status == 404:
                    return [], "isn't available"
                rounds = _MAX_ROUNDS if request.kind == "profile" else 2  # a post page loads its first comments itself
                seen = -1
                for _ in range(rounds):
                    await page.wait_for_timeout(1000)
                    await settle()
                    nodes = [n for d in docs for n in _walk(d)]
                    have = len(_by_id(nodes, "Story"))
                    if loop.time() >= deadline or (request.kind == "profile" and have >= request.max_posts) or have == seen:
                        break
                    seen = have
                    await page.mouse.wheel(0, 6000)
                text = ""
                if not docs:
                    try:
                        text = await page.inner_text("body", timeout=2000)
                    except PlaywrightError:
                        pass
                return docs, text
            finally:
                try:
                    await browser.close()  # every path: success, error, timeout, cancellation
                except Exception:
                    log.warning("facebook browser close failed")
    except PlaywrightTimeout:
        raise ScrapeError("upstream_timeout", "Facebook did not answer in time", platform=NAME) from None
    except PlaywrightError as e:
        log.warning("facebook browser failure %s", type(e).__name__)
        raise ScrapeError("upstream_error", "Facebook could not be read with the browser", platform=NAME) from None
    finally:
        for t in tasks:
            t.cancel()


async def scrape(request: ScrapeRequest) -> ScrapeResult:
    page, kind = request.ids["page"], request.kind
    path = page if kind == "profile" else f"{page}/posts/{request.ids['post_id']}"
    docs, text = await _load(request, path)
    nodes = [n for d in docs for n in _walk(d)]
    profile = _profile_node(nodes, page)
    if profile and profile["__typename"] == "User":
        raise ScrapeError("unsupported_account_type", "Facebook personal profiles are not scraped",
                          platform=NAME, account_type="personal_profile")
    stories = _by_id(nodes, "Story")
    target = None
    if kind == "post":
        pid = request.ids["post_id"]
        target = stories.get(pid) or next((s for s in stories.values() if str(s.get("url") or "").rstrip("/").endswith(pid)), None)
    if profile is None:
        if any(m in text.lower() for m in _UNAVAILABLE):
            raise ScrapeError(f"{'post' if kind == 'post' else 'account'}_not_found",
                              "the Facebook page or post does not exist or is not public", platform=NAME)
        raise ScrapeError("upstream_error", "Facebook returned no recognizable public page data", platform=NAME)
    if kind == "post" and target is None:  # the page loaded but does not carry that post
        raise ScrapeError("post_not_found", "the Facebook post does not exist or is not public", platform=NAME)

    warnings: list[ScrapeWarning] = []
    followers = _int(_first(profile, "followers_count", "follower_count"))
    if followers is None:
        warnings.append(ScrapeWarning(code="partial_parse", message="the follower count was not in the page data"))
    # a Story with an "owner" belongs to that profile only; related stories on a post page are dropped
    own = [s for s in stories.values() if _first(s, "owner.id") in (None, profile["id"])]
    posts = sorted((_post(s) for s in own), key=lambda p: p.created_at or "", reverse=True)[: request.max_posts]
    if kind == "profile" and len(posts) < request.max_posts:
        warnings.append(ScrapeWarning(code="history_truncated", message="fewer posts than requested were loaded"))
    post, comments = None, []
    if target is not None:
        post = _post(target)
        if request.max_comments:
            found = _by_id(nodes, "Comment")
            comments = sorted((_comment(c, post.id) for c in found.values()), key=lambda c: c.created_at or "")[: request.max_comments]
            if not comments:
                warnings.append(ScrapeWarning(code="comments_unavailable", message="no comments were loaded for the post"))
    return ScrapeResult(
        platform=NAME, url_kind=kind, username=page, display_name=profile.get("name"),
        bio=str(profile.get("description") or "").strip() or None, profile_url=f"https://www.facebook.com/{page}",
        followers=followers, verified=profile["is_verified"] if isinstance(profile.get("is_verified"), bool) else None,
        posts=posts, post=post, comments=comments, warnings=warnings)
