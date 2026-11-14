"""Bluesky via the atproto SDK against the public AppView (no authentication)."""
from atproto import AsyncClient
from atproto_client import exceptions as ax
from atproto_client.models.app.bsky.feed.defs import ThreadViewPost
from atproto_client.request import AsyncRequest
from atproto_core.exceptions import AtProtocolError
from pydantic import ValidationError

from ..errors import ScrapeError, rate_limited
from ..models import Author, Comment, Post, ScrapeResult, ScrapeWarning
from ..textutil import to_rfc3339
from .base import Recognized, ScrapeRequest

NAME = "bluesky"
APPVIEW = "https://public.api.bsky.app"
_PAGE = 100  # getAuthorFeed maximum
_MAX_PAGES = 10  # shortcut: at most 1000 feed items inspected per scrape, raise if reposts crowd out history


def recognize(u) -> Recognized | None:
    s = u.segments
    if u.host != "bsky.app" or len(s) < 2 or s[0] != "profile":
        return None
    if len(s) == 2:
        return Recognized(NAME, "profile", {"actor": s[1]})
    if len(s) == 4 and s[2] == "post":
        return Recognized(NAME, "post", {"actor": s[1], "rkey": s[3]})
    return None


def _map(e: Exception, what: str) -> ScrapeError:
    """SDK exception -> typed ScrapeError. `what` is "account" or "post" (the thing a 400 says is missing)."""
    if isinstance(e, ax.InvokeTimeoutError):
        return ScrapeError("upstream_timeout", "Bluesky did not answer in time", platform=NAME)
    if isinstance(e, ax.RateLimitExceededError):
        return rate_limited(NAME, e.retry_after)
    if isinstance(e, ax.BadRequestError):
        return ScrapeError(f"{what}_not_found", f"the Bluesky {what} does not exist or was deleted", platform=NAME)
    if isinstance(e, (ValidationError, ValueError)):
        return ScrapeError("upstream_error", "Bluesky answered with data that could not be parsed", platform=NAME)
    status = getattr(getattr(e, "response", None), "status_code", None)
    return ScrapeError("upstream_error", f"Bluesky answered with an error{f' ({status})' if status else ''}",
                       platform=NAME)


async def _call(coro, what: str):
    try:
        return await coro
    except (AtProtocolError, ValidationError) as e:
        raise _map(e, what) from None


def _post(pv) -> Post:
    rec = pv.record
    rkey = pv.uri.rsplit("/", 1)[-1]
    return Post(
        id=pv.uri,
        text=(getattr(rec, "text", None) or "").strip(),
        created_at=to_rfc3339(getattr(rec, "created_at", None) or pv.indexed_at),
        url=f"https://bsky.app/profile/{pv.author.handle}/post/{rkey}",
        likes=pv.like_count, replies=pv.reply_count, reposts=pv.repost_count,
    )


def _comments(node, parent_uri: str, out: list[Comment]) -> None:
    for r in node.replies or []:
        if not isinstance(r, ThreadViewPost):  # NotFoundPost / BlockedPost
            continue
        a = r.post.author
        out.append(Comment(**_post(r.post).model_dump(),
                           author=Author(username=a.handle, display_name=a.display_name or None),
                           in_reply_to_id=parent_uri))
        _comments(r, r.post.uri, out)


async def _history(client, did: str, req: ScrapeRequest, warnings: list[ScrapeWarning]) -> list[Post]:
    posts: list[Post] = []
    cursor = None
    try:
        for _ in range(_MAX_PAGES):
            page = await _call(client.get_author_feed(
                did, cursor=cursor, filter="posts_no_replies", limit=_PAGE), "account")
            for item in page.feed:
                if item.reason is None:  # skip reposts
                    posts.append(_post(item.post))
            cursor = page.cursor
            if len(posts) >= req.max_posts or not cursor or not page.feed:
                break
    except ScrapeError as e:
        warnings.append(ScrapeWarning(code="history_truncated" if posts else "history_unavailable", message=e.message))
    return posts[: req.max_posts]


async def scrape(req: ScrapeRequest) -> ScrapeResult:
    client = AsyncClient(APPVIEW, request=AsyncRequest(timeout=req.timeout_s))
    try:
        profile = await _call(client.get_profile(req.ids["actor"]), "account")
        if any(l.val == "!no-unauthenticated" for l in profile.labels or []):
            raise ScrapeError("account_private", "the Bluesky account is not visible without login", platform=NAME)
        v = profile.verification
        warnings: list[ScrapeWarning] = []
        result = ScrapeResult(
            platform=NAME, url_kind=req.kind, username=profile.handle,
            display_name=profile.display_name or None, bio=profile.description,
            profile_url=f"https://bsky.app/profile/{profile.handle}",
            followers=profile.followers_count, following=profile.follows_count, post_count=profile.posts_count,
            verified=None if v is None else v.verified_status == "valid",
        )
        result.posts = await _history(client, profile.did, req, warnings)
        if req.kind == "post":
            uri = f"at://{profile.did}/app.bsky.feed.post/{req.ids['rkey']}"
            thread = (await _call(client.get_post_thread(uri, depth=10, parent_height=0), "post")).thread
            if not isinstance(thread, ThreadViewPost):
                raise ScrapeError("post_not_found", "the Bluesky post does not exist or is not visible", platform=NAME)
            result.post = _post(thread.post)
            if req.max_comments:
                comments: list[Comment] = []
                _comments(thread, thread.post.uri, comments)
                if thread.replies is None and thread.post.reply_count:
                    warnings.append(ScrapeWarning(code="comments_unavailable", message="Bluesky returned no replies"))
                comments.sort(key=lambda c: c.created_at or "")
                result.comments = comments[: req.max_comments]
        result.warnings = warnings
        return result
    finally:
        await client.request.close()
