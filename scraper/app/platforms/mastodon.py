"""Mastodon via the unauthenticated REST API of the instance named in the URL."""
import re
from datetime import datetime, timezone

import httpx

from ..errors import ScrapeError, rate_limited
from ..models import Author, Comment, Post, ScrapeResult, ScrapeWarning
from ..textutil import html_to_text, to_rfc3339
from ..urls import LISTED_HOSTS
from .base import Recognized, ScrapeRequest

NAME = "mastodon"
_PAGE = 40  # Mastodon caps statuses pages at 40
_MAX_PAGES = 10
# A real TLD starts with a letter; this drops every IPv4 spelling (decimal, octal, hex) and IPv6 (":" is not a label char).
_HOSTNAME = re.compile(r"[a-z0-9-]+(\.[a-z0-9-]+)*\.[a-z][a-z0-9-]*")
_PARSE_ERRORS = (KeyError, TypeError, ValueError, AttributeError)  # ValueError covers pydantic ValidationError


def recognize(u) -> Recognized | None:
    h, s = u.host, u.segments
    # Any host that is not another listed platform; no IP literals (any spelling) or dotless names (the API call goes there).
    if h in LISTED_HOSTS or not _HOSTNAME.fullmatch(h) or not s:
        return None
    if s[0].startswith("@") and len(s[0]) > 1:
        user, rest = s[0][1:], s[1:]
        if len(rest) == 0:
            return Recognized(NAME, "profile", {"host": h, "user": user})
        if len(rest) == 1 and rest[0].isdigit():
            return Recognized(NAME, "post", {"host": h, "user": user, "status_id": rest[0]})
    if s[0] == "users" and len(s) >= 2:
        if len(s) == 2:
            return Recognized(NAME, "profile", {"host": h, "user": s[1]})
        if len(s) == 4 and s[2] == "statuses" and s[3].isdigit():
            return Recognized(NAME, "post", {"host": h, "user": s[1], "status_id": s[3]})
    return None


def _retry_after(resp: httpx.Response) -> float | None:
    if (v := resp.headers.get("Retry-After")) and v.isdigit():
        return float(v)
    if reset := resp.headers.get("X-RateLimit-Reset"):  # ISO-8601 time the window resets
        try:
            return (datetime.fromisoformat(reset.replace("Z", "+00:00")) - datetime.now(timezone.utc)).total_seconds()
        except ValueError:
            pass
    return None


async def _get(client: httpx.AsyncClient, url: str, what: str, **params) -> httpx.Response:
    """GET and map failures to ScrapeError. `what` is "account" or "post"."""
    try:
        r = await client.get(url, params=params or None)
    except httpx.TimeoutException:
        raise ScrapeError("upstream_timeout", "the Mastodon instance did not answer in time", platform=NAME) from None
    except httpx.HTTPError:
        raise ScrapeError("upstream_error", "could not reach the Mastodon instance", platform=NAME) from None
    code = r.status_code
    if code == 429:
        raise rate_limited(NAME, _retry_after(r))
    if 300 <= code < 400:
        raise ScrapeError("unsupported_url", "the host does not serve the Mastodon REST API", platform=NAME)
    if code in (401, 403):
        raise ScrapeError("account_private", f"the Mastodon {what} is not public", platform=NAME)
    if code in (404, 410):
        if _json(r) is None:  # a Mastodon 404 is JSON; HTML means this host is not Mastodon
            raise ScrapeError("unsupported_url", "the host does not serve the Mastodon REST API", platform=NAME)
        raise ScrapeError(f"{what}_not_found", f"the Mastodon {what} does not exist or was deleted", platform=NAME)
    if code >= 400:
        raise ScrapeError("upstream_error", f"the Mastodon instance answered {code}", platform=NAME)
    if _json(r) is None:
        raise ScrapeError("unsupported_url", "the host does not serve the Mastodon REST API", platform=NAME)
    return r


def _json(r: httpx.Response):
    try:
        data = r.json()
    except ValueError:
        return None
    return data if isinstance(data, (dict, list)) else None


def _acct(acct: str, host: str) -> str:
    return acct if "@" in acct else f"{acct}@{host}"


def _post(s: dict) -> Post:
    return Post(id=str(s["id"]), text=html_to_text(s.get("content")), created_at=to_rfc3339(s.get("created_at")),
                url=s.get("url") or s.get("uri"), likes=s.get("favourites_count"), replies=s.get("replies_count"),
                reposts=s.get("reblogs_count"))


def _comment(s: dict, host: str) -> Comment:
    a = s.get("account") or {}
    return Comment(**_post(s).model_dump(),
                   author=Author(username=_acct(a.get("acct") or a.get("username") or "", host),
                                 display_name=a.get("display_name") or None),
                   in_reply_to_id=None if s.get("in_reply_to_id") is None else str(s["in_reply_to_id"]))


def _parse_all(items, fn, warnings: list) -> list:
    """Parse each item; one that does not parse is skipped and reported once as partial_parse."""
    out, bad = [], 0
    for s in items:
        try:
            out.append(fn(s))
        except _PARSE_ERRORS:
            bad += 1
    if bad:
        warnings.append(ScrapeWarning(code="partial_parse", message=f"{bad} item(s) from the instance could not be parsed"))
    return out


def _same_origin(url: str, base: httpx.URL) -> bool:
    """Link rel=next must stay on the instance: same scheme (https), host and port."""
    try:
        u = httpx.URL(url)
    except httpx.InvalidURL:
        return False
    return (u.scheme, u.host, u.port) == (base.scheme, base.host, base.port)


async def _history(client, account_id: str, req: ScrapeRequest, warnings: list) -> list[Post]:
    posts: list[Post] = []
    url, params = f"/api/v1/accounts/{account_id}/statuses", {"limit": min(_PAGE, req.max_posts), "exclude_reblogs": "true"}
    try:
        for _ in range(_MAX_PAGES):
            r = await _get(client, url, "account", **params)
            page = _json(r)
            if not isinstance(page, list):
                raise ScrapeError("upstream_error", "unexpected statuses response", platform=NAME)
            posts += _parse_all(page, _post, warnings)
            nxt = r.links.get("next", {}).get("url")
            if len(posts) >= req.max_posts or not page or not nxt or not _same_origin(nxt, client.base_url):
                break
            url, params = nxt, {}
    except ScrapeError as e:
        warnings.append(ScrapeWarning(code="history_truncated" if posts else "history_unavailable", message=e.message))
    return posts[: req.max_posts]


async def scrape(req: ScrapeRequest) -> ScrapeResult:
    try:
        return await _scrape(req)
    except _PARSE_ERRORS:  # the requested account or post itself has an unexpected shape
        raise ScrapeError("upstream_error", "the Mastodon instance answered with data that could not be parsed",
                          platform=NAME) from None


async def _scrape(req: ScrapeRequest) -> ScrapeResult:
    host = req.ids["host"]
    async with httpx.AsyncClient(base_url=f"https://{host}", timeout=req.timeout_s, follow_redirects=False,
                                 headers={"Accept": "application/json", "User-Agent": "junction-scraper/1"}) as client:
        status = None
        if req.kind == "post":
            r = await _get(client, f"/api/v1/statuses/{req.ids['status_id']}", "post")
            status = _json(r)
            if not isinstance(status, dict) or not isinstance(status.get("account"), dict):
                raise ScrapeError("upstream_error", "unexpected status response", platform=NAME)
            acct = status["account"]
        else:
            r = await _get(client, "/api/v1/accounts/lookup", "account", acct=req.ids["user"])
            acct = _json(r)
            if not isinstance(acct, dict) or "id" not in acct:
                raise ScrapeError("upstream_error", "unexpected account response", platform=NAME)
        warnings: list[ScrapeWarning] = []
        result = ScrapeResult(
            platform=NAME, url_kind=req.kind, username=_acct(acct.get("acct") or acct.get("username") or "", host),
            display_name=acct.get("display_name") or None, bio=html_to_text(acct.get("note")),
            profile_url=acct.get("url"), followers=acct.get("followers_count"), following=acct.get("following_count"),
            post_count=acct.get("statuses_count"), verified=None,  # Mastodon has no account verification flag
        )
        result.posts = await _history(client, str(acct["id"]), req, warnings)
        if status is not None:
            result.post = _post(status)
            if req.max_comments:
                try:
                    ctx = _json(await _get(client, f"/api/v1/statuses/{status['id']}/context", "post"))
                    if not isinstance(ctx, dict):
                        raise ScrapeError("upstream_error", "unexpected context response", platform=NAME)
                    comments = _parse_all(ctx.get("descendants") or [], lambda s: _comment(s, host), warnings)
                    comments.sort(key=lambda c: c.created_at or "")
                    result.comments = comments[: req.max_comments]
                except ScrapeError as e:
                    warnings.append(ScrapeWarning(code="comments_unavailable", message=e.message))
        result.warnings = warnings
        return result
