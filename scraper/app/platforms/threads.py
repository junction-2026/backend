"""Threads public profiles and posts: httpx with a browser User-Agent, data from the page's embedded SSR JSON.

The page embeds Relay query results in <script type="application/json" data-sjs> blocks. Their nesting changes
often, so nothing here follows a fixed path: the blocks are walked for objects that look like a user (has
`username` and profile members) and like a post (has `pk`, `code`, `user`). Every key access is defensive: a
missing member is None (and one `partial_parse` warning), never an exception. Only a page without any
recognizable profile data is an error (`upstream_error`).
"""
import json
import re
from datetime import datetime, timezone

import httpx

from ..errors import ScrapeError, rate_limited
from ..models import Author, Comment, Post, ScrapeResult, ScrapeWarning
from ..textutil import to_rfc3339
from .base import Recognized, ScrapeRequest

NAME = "threads"
_BASE = "https://www.threads.com"
_UA = ("Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36")
_SCRIPT = re.compile(r'<script[^>]*type="application/json"[^>]*>(.*?)</script>', re.DOTALL)
_USER_HINTS = ("follower_count", "biography", "full_name", "is_verified", "pk", "id")


def recognize(u) -> Recognized | None:
    s = u.segments
    if u.host not in ("threads.net", "threads.com") or not s or not s[0].startswith("@") or len(s[0]) < 2:
        return None
    if len(s) == 1:
        return Recognized(NAME, "profile", {"username": s[0][1:]})
    if len(s) == 3 and s[1] == "post":
        return Recognized(NAME, "post", {"username": s[0][1:], "code": s[2]})
    return None


def _retry_after(r: httpx.Response) -> float | None:
    v = r.headers.get("Retry-After")
    return float(v) if v and v.isdigit() else None


async def _page(client: httpx.AsyncClient, path: str, what: str) -> str:
    try:
        r = await client.get(path)
    except httpx.TimeoutException:
        raise ScrapeError("upstream_timeout", "Threads did not answer in time", platform=NAME) from None
    except httpx.HTTPError:
        raise ScrapeError("upstream_error", "could not reach Threads", platform=NAME) from None
    if r.status_code == 429:
        raise rate_limited(NAME, _retry_after(r))
    if r.status_code in (404, 410):
        raise ScrapeError(f"{what}_not_found", f"the Threads {what} does not exist or was deleted", platform=NAME)
    if r.status_code != 200:
        raise ScrapeError("upstream_error", f"Threads answered {r.status_code}", platform=NAME)
    return r.text


def _walk(o):
    """Every dict and list nested in a JSON value, iteratively (no recursion limit on hostile depth)."""
    stack = [o]
    while stack:
        cur = stack.pop()
        if isinstance(cur, dict):
            yield cur
            stack.extend(cur.values())
        elif isinstance(cur, list):
            stack.extend(cur)


def _blocks(html: str) -> list:
    out = []
    for m in _SCRIPT.finditer(html):
        try:
            out.append(json.loads(m.group(1)))
        except (ValueError, RecursionError):
            continue
    return out


def _int(v):
    return v if isinstance(v, int) and not isinstance(v, bool) else None


def _text_of(d: dict) -> str:
    cap = d.get("caption")
    t = cap.get("text") if isinstance(cap, dict) else cap
    return t.strip() if isinstance(t, str) else ""


def _post_dicts(blocks: list) -> tuple[dict[str, dict], int]:
    """(pk -> post dict, number of post-like objects without a usable id or code). First occurrence of a pk wins."""
    found: dict[str, dict] = {}
    skipped = 0
    for b in blocks:
        for d in _walk(b):
            pk = d.get("pk")
            if not isinstance(d.get("user"), dict) or not ("caption" in d or "taken_at" in d):
                continue
            if isinstance(d.get("code"), str) and d["code"] and isinstance(pk, (str, int)) and not isinstance(pk, bool):
                found.setdefault(str(pk), d)
            else:
                skipped += 1
    return found, skipped


def _author(d: dict) -> str:
    u = d.get("user")
    name = u.get("username") if isinstance(u, dict) else None
    return name if isinstance(name, str) else ""


def _to_post(d: dict, username: str) -> Post:
    info = d.get("text_post_app_info")
    info = info if isinstance(info, dict) else {}
    taken = d.get("taken_at")
    return Post(id=str(d["pk"]), text=_text_of(d),
                created_at=to_rfc3339(datetime.fromtimestamp(taken, timezone.utc))
                if isinstance(taken, (int, float)) and not isinstance(taken, bool) else None,
                url=f"{_BASE}/@{username}/post/{d['code']}" if username else None,
                likes=_int(d.get("like_count")), replies=_int(info.get("direct_reply_count")),
                reposts=_int(info.get("repost_count")))


def _user_dict(blocks: list, username: str) -> dict | None:
    """The richest object naming this user, completed from the other (partial) ones."""
    cands = [d for b in blocks for d in _walk(b)
             if isinstance(d.get("username"), str) and d["username"].lower() == username.lower()
             and any(k in d for k in _USER_HINTS) and "code" not in d]
    if not cands:
        return None
    merged: dict = {}
    for d in sorted(cands, key=len, reverse=True):
        for k, v in d.items():
            if v is not None:
                merged.setdefault(k, v)
    return merged


def _parse_posts(blocks: list, bad: list) -> list[tuple[dict, Post]]:
    """(post dict, Post) for every post found; `bad` collects what could not be parsed."""
    found, skipped = _post_dicts(blocks)
    bad.extend([None] * skipped)
    out = []
    for d in found.values():
        try:
            out.append((d, _to_post(d, _author(d))))
        except (KeyError, TypeError, ValueError, OverflowError, OSError):
            bad.append(d)
    return out


def _profile_result(user: dict, username: str, url_kind: str) -> ScrapeResult:
    if user.get("is_private") is True:
        raise ScrapeError("account_private", "the Threads account is private", platform=NAME)
    str_or_none = lambda v: v if isinstance(v, str) and v else None  # noqa: E731
    name = str_or_none(user.get("username")) or username
    return ScrapeResult(
        platform=NAME, url_kind=url_kind, username=name, display_name=str_or_none(user.get("full_name")),
        bio=user["biography"].strip() if isinstance(user.get("biography"), str) else None,
        profile_url=f"{_BASE}/@{name}", followers=_int(user.get("follower_count")), following=None, post_count=None,  # not exposed by Threads
        verified=user["is_verified"] if isinstance(user.get("is_verified"), bool) else None)


def _history(posts: list[tuple[dict, Post]], username: str, limit: int) -> list[Post]:
    mine = [p for d, p in posts if _author(d).lower() == username.lower()]
    mine.sort(key=lambda p: p.created_at or "", reverse=True)
    return mine[:limit]


async def scrape(req: ScrapeRequest) -> ScrapeResult:
    username = req.ids["username"]
    async with httpx.AsyncClient(base_url=_BASE, timeout=req.timeout_s, follow_redirects=False,
                                 headers={"User-Agent": _UA, "Accept": "text/html,application/xhtml+xml",
                                          "Accept-Language": "en-US,en;q=0.9"}) as client:
        warnings: list[ScrapeWarning] = []
        bad: list = []  # post-like objects of every page read, reported once
        if req.kind == "profile":
            result = _profile(await _page(client, f"/@{username}", "account"), req, username, bad)
        else:
            result = _post(await _page(client, f"/@{username}/post/{req.ids['code']}", "post"), req, username, bad)
            try:  # the author's history and counters live on the profile page, a second request
                prof = _profile(await _page(client, f"/@{result.username}", "account"), req, result.username, bad)
            except ScrapeError as e:
                warnings.append(ScrapeWarning(code="history_unavailable", message=e.message))
            else:
                result.posts = prof.posts
                for k in ("display_name", "bio", "followers", "verified"):
                    if getattr(result, k) is None:
                        setattr(result, k, getattr(prof, k))
        if bad:
            warnings.append(ScrapeWarning(code="partial_parse", message=f"{len(bad)} post(s) could not be parsed"))
        if result.followers is None:
            warnings.append(ScrapeWarning(code="partial_parse", message="Threads page did not contain the follower count"))
        result.warnings = warnings
        return result


def _profile(html: str, req: ScrapeRequest, username: str, bad: list) -> ScrapeResult:
    blocks = _blocks(html)
    user = _user_dict(blocks, username)
    if user is None:
        raise ScrapeError("upstream_error", "the Threads page contained no recognizable profile data", platform=NAME)
    result = _profile_result(user, username, "profile")
    result.posts = _history(_parse_posts(blocks, bad), result.username, req.max_posts)
    return result


def _post(html: str, req: ScrapeRequest, username: str, bad: list) -> ScrapeResult:
    blocks = _blocks(html)
    posts = _parse_posts(blocks, bad)
    code = req.ids["code"]
    target = next(((d, p) for d, p in posts if d.get("code") == code), None)
    if target is None:
        raise ScrapeError("upstream_error", "the Threads page contained no recognizable post data", platform=NAME)
    td, tp = target
    author = _author(td) or username
    user = _user_dict(blocks, author) or {"username": author}
    result = _profile_result(user, author, "post")
    result.post = tp
    if req.max_comments:
        replies = [(d, p) for d, p in posts if d is not td and p.created_at and tp.created_at and p.created_at >= tp.created_at]
        replies.sort(key=lambda x: x[1].created_at or "")
        for d, p in replies[: req.max_comments]:
            u = d.get("user") if isinstance(d.get("user"), dict) else {}
            result.comments.append(Comment(**p.model_dump(), author=Author(
                username=_author(d), display_name=u.get("full_name") if isinstance(u.get("full_name"), str) else None),
                in_reply_to_id=None))
    return result
