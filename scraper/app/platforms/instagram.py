"""Instagram public profiles and posts through instaloader, anonymously.

An optional logged-in session (env INSTAGRAM_SESSION_FILE, an instaloader session file) is loaded when it is
set; neither its path nor its content is ever logged. instaloader is blocking, so the whole scrape runs in a
worker thread. The retry/sleep machinery of instaloader is switched off: a throttle answers rate_limited at once.
"""
import asyncio
import logging
import os
import re
import time
from pathlib import Path

import instaloader
import requests
from instaloader import exceptions as ex
from instaloader.instaloadercontext import RateController

from ..errors import ScrapeError, rate_limited
from ..models import Author, Comment, Post, ScrapeResult, ScrapeWarning
from ..textutil import to_rfc3339
from .base import Recognized, ScrapeRequest

NAME = "instagram"
log = logging.getLogger("scraper.instagram")
_USER = re.compile(r"^[A-Za-z0-9._]{1,30}$")
_RESERVED = {"explore", "accounts", "p", "reel", "reels", "stories", "direct", "tv", "about", "legal", "web", "api"}
_PARSE_ERRORS = (KeyError, TypeError, ValueError, AttributeError, IndexError)
_BUDGET = 0.8  # share of the scrape timeout the history loop may use; the core answers 504 at 100 %


def recognize(u) -> Recognized | None:
    s = u.segments
    if u.host != "instagram.com" or not s:
        return None
    if len(s) == 2 and s[0] in ("p", "reel"):
        return Recognized(NAME, "post", {"shortcode": s[1]})
    if len(s) == 1 and s[0] not in _RESERVED and _USER.match(s[0]):
        return Recognized(NAME, "profile", {"username": s[0]})
    return None


class _NoWait(RateController):
    """instaloader would sleep minutes to respect Instagram's windows; a request must answer, not wait."""

    def wait_before_query(self, query_type: str) -> None:
        return None


def _loader(timeout_s: float) -> instaloader.Instaloader:
    loader = instaloader.Instaloader(
        quiet=True, sleep=False, max_connection_attempts=1, request_timeout=timeout_s, rate_controller=_NoWait,
        download_pictures=False, download_videos=False, download_video_thumbnails=False,
        download_geotags=False, download_comments=False, save_metadata=False, compress_json=False)
    session_file = os.environ.get("INSTAGRAM_SESSION_FILE", "")
    if session_file:
        # instaloader names session files session-<username>; the username is not a secret, the file is.
        user = Path(session_file).name.removeprefix("session-")
        try:
            loader.load_session_from_file(user, session_file)
        except Exception:  # unreadable or corrupt session: scrape anonymously, say nothing about the file
            log.warning("instagram session file could not be loaded; continuing anonymously")
    return loader


def _chain(e: BaseException):
    for _ in range(10):
        if e is None:
            return
        yield e
        e = e.__cause__ or e.__context__


def _map(e: Exception, what: str) -> ScrapeError:
    """instaloader/requests failure -> typed error. `what` is "account" or "post"."""
    errs = list(_chain(e))
    if any(isinstance(x, requests.exceptions.Timeout) for x in errs):
        return ScrapeError("upstream_timeout", "Instagram did not answer in time", platform=NAME)
    # get_json wraps the final failure in a ConnectionException; the 429 is in its cause chain or its text
    if any(isinstance(x, (ex.TooManyRequestsException, ex.LoginRequiredException)) or " 429 " in f" {x} " for x in errs):
        return rate_limited(NAME)  # instaloader does not expose Retry-After, so the wait is unknown
    if any(isinstance(x, (ex.ProfileNotExistsException, ex.QueryReturnedNotFoundException)) for x in errs):
        return ScrapeError(f"{what}_not_found", f"the Instagram {what} does not exist or was deleted", platform=NAME)
    if any(isinstance(x, ex.PrivateProfileNotFollowedException) for x in errs):
        return ScrapeError("account_private", "the Instagram account is private", platform=NAME)
    return ScrapeError("upstream_error", "Instagram answered with an error or with data that could not be parsed",
                       platform=NAME)


def _opt(fn):
    """A member the platform omitted is None, never a guess."""
    try:
        return fn()
    except _PARSE_ERRORS:
        return None


def _count(node: dict, polaris: str, legacy: str):
    """Counter from a post node: Polaris media carry `<polaris>`, legacy nodes `<legacy>.count`.
    instaloader fills a missing Polaris counter with 0, so the raw key decides."""
    if node.get("like_and_view_counts_disabled") and polaris == "like_count":
        return None
    if "taken_at" in node:
        v = node.get(polaris)
    else:
        v = (node.get(legacy) or {}).get("count")
    return v if isinstance(v, int) and not isinstance(v, bool) else None


def _post(p: instaloader.Post) -> Post:
    n = p._node
    return Post(id=p.shortcode, text=(p.caption or "").strip(), created_at=to_rfc3339(p.date_utc),
                url=f"https://www.instagram.com/p/{p.shortcode}/",
                likes=_count(n, "like_count", "edge_media_preview_like"),
                replies=_count(n, "comment_count", "edge_media_to_comment"), reposts=None)


def _comments(post: instaloader.Post, limit: int, deadline: float, warnings: list) -> list[Comment]:
    """Comments (and their answers) oldest first, at most `limit`. What was read before a failure or the
    time budget running out is kept, with a comments_unavailable warning."""
    out: list[Comment] = []

    def add(c, parent: str | None):
        out.append(Comment(id=str(c.id), text=(c.text or "").strip(), created_at=to_rfc3339(c.created_at_utc), url=None,
                           likes=c.likes_count if isinstance(c.likes_count, int) else None, replies=None, reposts=None,
                           author=Author(username=c.owner.username, display_name=None), in_reply_to_id=parent))

    try:
        for c in post.get_comments():  # LoginRequiredException anonymously
            add(c, None)
            for a in c.answers:
                add(a, str(c.id))
            if len(out) >= limit * 2:
                break
            if time.monotonic() > deadline:
                warnings.append(ScrapeWarning(code="comments_unavailable", message="time budget used up before max_comments"))
                break
    except (ex.InstaloaderException, requests.exceptions.RequestException, *_PARSE_ERRORS) as e:
        log.info("instagram comments cut short: %s", type(e).__name__)
        warnings.append(ScrapeWarning(
            code="comments_unavailable",
            message="Instagram stopped serving the comments" if out else "Instagram does not serve the comments of this post"))
    out.sort(key=lambda c: c.created_at or "")
    return out[:limit]


def _history(profile: instaloader.Profile, limit: int, deadline: float, warnings: list) -> list[Post]:
    posts: list[Post] = []
    bad = 0
    try:
        for p in profile.get_posts():
            if time.monotonic() > deadline:
                warnings.append(ScrapeWarning(code="history_truncated", message="time budget used up before max_posts"))
                break
            try:
                posts.append(_post(p))
            except ex.InstaloaderException:
                raise
            except _PARSE_ERRORS:
                bad += 1
            if len(posts) >= limit:
                break
    except (ex.InstaloaderException, requests.exceptions.RequestException, *_PARSE_ERRORS) as e:
        err = _map(e, "account")
        if err.code == "account_private":
            raise err from None
        warnings.append(ScrapeWarning(code="history_truncated" if posts else "history_unavailable", message=err.message))
    if bad:
        warnings.append(ScrapeWarning(code="partial_parse", message=f"{bad} post(s) could not be parsed"))
    posts.sort(key=lambda p: p.created_at or "", reverse=True)
    return posts[:limit]


def _profile_members(profile: instaloader.Profile, warnings: list) -> dict:
    if profile.is_private:
        raise ScrapeError("account_private", "the Instagram account is private", platform=NAME)
    m = dict(username=profile.username, display_name=_opt(lambda: profile.full_name) or None,
             bio=_opt(lambda: profile.biography.strip()), profile_url=f"https://www.instagram.com/{profile.username}/",
             followers=_opt(lambda: profile.followers), following=_opt(lambda: profile.followees),
             post_count=_opt(lambda: profile.mediacount), verified=_opt(lambda: profile.is_verified))
    for key in ("followers", "following", "post_count", "verified"):
        if m[key] is not None and not isinstance(m[key], (int, bool)):
            m[key] = None
    missing = [k for k in ("followers", "following", "post_count") if m[k] is None]
    if missing:
        warnings.append(ScrapeWarning(code="partial_parse", message="Instagram omitted: " + ", ".join(missing)))
    return m


def _scrape_sync(req: ScrapeRequest) -> ScrapeResult:
    what = "post" if req.kind == "post" else "account"
    deadline = time.monotonic() + req.timeout_s * _BUDGET
    warnings: list[ScrapeWarning] = []
    loader = _loader(req.timeout_s)
    ctx = loader.context
    try:
        post = None
        if req.kind == "post":
            try:
                post = instaloader.Post.from_shortcode(ctx, req.ids["shortcode"])
            except ex.BadResponseException:  # the media query answered without an item: deleted or not viewable
                raise ScrapeError("post_not_found", "the Instagram post does not exist or was deleted",
                                  platform=NAME) from None
            username = post.owner_username
        else:
            username = req.ids["username"]
        what = "account"
        profile = instaloader.Profile.from_username(ctx, username)
        result = ScrapeResult(platform=NAME, url_kind=req.kind, **_profile_members(profile, warnings))
        if post is not None:
            result.post = _post(post)
            if req.max_comments:
                result.comments = _comments(post, req.max_comments, deadline, warnings)
        result.posts = _history(profile, req.max_posts, deadline, warnings)
        result.warnings = warnings
        return result
    except ScrapeError:
        raise
    except (ex.InstaloaderException, requests.exceptions.RequestException) as e:
        raise _map(e, what) from None
    except _PARSE_ERRORS:
        raise ScrapeError("upstream_error", "Instagram answered with data that could not be parsed",
                          platform=NAME) from None
    finally:
        loader.close()


async def scrape(req: ScrapeRequest) -> ScrapeResult:
    return await asyncio.to_thread(_scrape_sync, req)
