"""X (Twitter) through twscrape with a one-account cookie pool built from TWITTER_AUTH_TOKEN and TWITTER_CT0."""
import html
import logging
import os
import re
import tempfile
from contextlib import aclosing

from twscrape import API, AccountsPool
from twscrape.accounts_pool import NoAccountError
from twscrape.logger import set_log_level

from ..errors import ScrapeError, rate_limited
from ..models import Author, Comment, Post, ScrapeResult, ScrapeWarning
from ..textutil import to_rfc3339
from .base import Recognized, ScrapeRequest

NAME = "twitter"
log = logging.getLogger("scraper.x")
set_log_level("ERROR")  # twscrape logs through loguru, outside the redacting stdlib filter
_REQUIRED = ("TWITTER_AUTH_TOKEN", "TWITTER_CT0")
_ACCOUNT = "junction"
_USER = re.compile(r"^[A-Za-z0-9_]{1,15}$")
_RESERVED = {"explore", "home", "i", "search", "settings", "notifications", "messages", "compose",
             "hashtag", "intent", "share", "login", "signup", "tos", "privacy", "about", "account"}
_api: API | None = None


def recognize(u) -> Recognized | None:
    s = u.segments
    if u.host not in ("twitter.com", "x.com") or not s or s[0] in _RESERVED or not _USER.match(s[0]):
        return None
    if len(s) == 1:
        return Recognized(NAME, "profile", {"username": s[0]})
    if len(s) == 3 and s[1] == "status" and s[2].isdigit():
        return Recognized(NAME, "post", {"username": s[0], "status_id": s[2]})
    return None


async def _get_api() -> API:
    """The pool is built once from the environment. Its sqlite file holds the cookies, so it lives in a private
    temp dir unless SCRAPER_X_POOL_DB names a path on a volume; it is never inside the repository."""
    global _api
    if _api is None:
        db = os.environ.get("SCRAPER_X_POOL_DB") or os.path.join(tempfile.mkdtemp(prefix="scraper-x-"), "pool.db")
        # wait_timeout: a pool account is locked while another scrape uses it; wait briefly instead of failing
        pool = AccountsPool(db, raise_when_no_account=True, wait_timeout=5.0, wait_interval=0.5)
        await pool.add_account_cookies(
            _ACCOUNT, f"auth_token={os.environ['TWITTER_AUTH_TOKEN']}; ct0={os.environ['TWITTER_CT0']}")
        _api = API(pool)
    return _api


async def _guard(api: API, coro):
    """Await a twscrape call and map its failures to typed errors (never echoing exception text)."""
    try:
        return await coro
    except ScrapeError:
        raise
    except TimeoutError:
        raise ScrapeError("upstream_timeout", "X did not answer in time", platform=NAME) from None
    except NoAccountError:
        if (await api.pool.stats()).get("active"):  # the account exists but is locked: X throttled it
            raise rate_limited(NAME) from None
        raise ScrapeError("upstream_error", "X rejected the configured session cookies", platform=NAME) from None
    except Exception as e:
        log.warning("x upstream failure %s", type(e).__name__)
        raise ScrapeError("upstream_error", "X answered with an error or unparseable data", platform=NAME) from None


async def _collect(gen, limit: int, keep=lambda t: True) -> list:
    out = []
    async with aclosing(gen) as g:
        async for t in g:
            if keep(t):
                out.append(t)
                if len(out) >= limit:
                    break
    return out


def _post(t) -> Post:
    return Post(id=t.id_str, text=html.unescape(t.rawContent or "").strip(), created_at=to_rfc3339(t.date),
                url=t.url, likes=t.likeCount, replies=t.replyCount, reposts=t.retweetCount)


def _comment(t) -> Comment:
    return Comment(**_post(t).model_dump(), author=Author(username=t.user.username, display_name=t.user.displayname),
                   in_reply_to_id=t.inReplyToTweetIdStr)


async def scrape(request: ScrapeRequest) -> ScrapeResult:
    if missing := [n for n in _REQUIRED if not os.environ.get(n)]:
        raise ScrapeError("platform_not_configured", "the X credentials are not configured",
                          platform=NAME, missing_settings=missing)
    api = await _get_api()
    warnings: list[ScrapeWarning] = []
    post = None
    if request.kind == "post":
        t = await _guard(api, api.tweet_details(int(request.ids["status_id"])))
        if t is None:
            raise ScrapeError("post_not_found", "the X post does not exist or was deleted", platform=NAME)
        user, post = t.user, _post(t)
    else:
        user = await _guard(api, api.user_by_login(request.ids["username"]))
        if user is None:
            raise ScrapeError("account_not_found", "the X account does not exist", platform=NAME)
    if user.protected:
        raise ScrapeError("account_private", "the X account is protected", platform=NAME)

    posts: list[Post] = []
    try:  # shortcut: scans at most 3x max_posts timeline entries (retweets are skipped); upgrade if retweet-heavy accounts matter
        tweets = await _guard(api, _collect(api.user_tweets(user.id, limit=request.max_posts * 3), request.max_posts,
                                            lambda t: t.retweetedTweet is None and t.user.id == user.id))
        posts = [_post(t) for t in sorted(tweets, key=lambda t: t.date, reverse=True)]
    except ScrapeError:
        warnings.append(ScrapeWarning(code="history_unavailable", message="the recent posts could not be read"))
    comments: list[Comment] = []
    if post and request.max_comments:
        try:
            replies = await _guard(api, _collect(api.tweet_replies(int(post.id), limit=request.max_comments),
                                                 request.max_comments))
            comments = [_comment(t) for t in sorted(replies, key=lambda t: t.date)]
        except ScrapeError:
            warnings.append(ScrapeWarning(code="comments_unavailable", message="the replies could not be read"))
    return ScrapeResult(
        platform=NAME, url_kind=request.kind, username=user.username, display_name=user.displayname,
        bio=html.unescape(user.rawDescription or "").strip(), profile_url=user.url, followers=user.followersCount,
        following=user.friendsCount, post_count=user.statusesCount,
        verified=None if user.verified is None and user.blue is None else bool(user.verified or user.blue),
        posts=posts, post=post, comments=comments, warnings=warnings)
