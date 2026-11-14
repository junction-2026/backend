"""Recognition for platforms whose scraping module is not installed yet.

Follow-up tasks replace the matching entry in app/registry.py with a real module that
implements recognize() and scrape(); until then these answer with the documented
`platform_not_configured` (503) instead of failing with a 500.
"""
import re

from ..errors import ScrapeError
from ..urls import NormalizedUrl
from .base import Recognized, ScrapeRequest

_IG_USER = re.compile(r"^[A-Za-z0-9._]{1,30}$")
_IG_RESERVED = {"explore", "accounts", "p", "reel", "reels", "stories", "direct", "tv", "about", "legal", "web", "api"}


def _instagram(u: NormalizedUrl) -> Recognized | None:
    s = u.segments
    if u.host != "instagram.com" or not s:
        return None
    if len(s) == 2 and s[0] in ("p", "reel"):
        return Recognized("instagram", "post", {"shortcode": s[1]})
    if len(s) == 1 and s[0] not in _IG_RESERVED and _IG_USER.match(s[0]):
        return Recognized("instagram", "profile", {"username": s[0]})
    return None


def _threads(u: NormalizedUrl) -> Recognized | None:
    s = u.segments
    if u.host not in ("threads.net", "threads.com") or not s or not s[0].startswith("@") or len(s[0]) < 2:
        return None
    if len(s) == 1:
        return Recognized("threads", "profile", {"username": s[0][1:]})
    if len(s) == 3 and s[1] == "post":
        return Recognized("threads", "post", {"username": s[0][1:], "code": s[2]})
    return None


class Pending:
    def __init__(self, name: str, recognize):
        self.NAME, self.recognize = name, recognize

    async def scrape(self, request: ScrapeRequest):
        raise ScrapeError("platform_not_configured",
                          f"the {self.NAME} module is not installed in this scraper build",
                          platform=self.NAME, missing_settings=[])


INSTAGRAM = Pending("instagram", _instagram)
THREADS = Pending("threads", _threads)
