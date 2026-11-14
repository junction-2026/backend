"""Platform module interface.

A module is any object (usually a Python module) with:
    NAME: str
    recognize(url: NormalizedUrl) -> Recognized | None      # None = not mine; may raise ScrapeError
    async scrape(request: ScrapeRequest) -> ScrapeResult    # raises ScrapeError for typed failures
Modules are listed in app/registry.py and nowhere else.
"""
from dataclasses import dataclass, field
from typing import NamedTuple

from ..urls import NormalizedUrl  # noqa: F401  (re-exported for modules)


class Recognized(NamedTuple):
    platform: str
    url_kind: str            # "profile" | "post"
    ids: dict[str, str]      # platform-specific identifiers taken from the URL


@dataclass
class ScrapeRequest:
    url: str
    max_posts: int
    max_comments: int
    recognized: Recognized
    timeout_s: float                      # SCRAPER_UPSTREAM_TIMEOUT_SECONDS: per-request and whole-scrape bound
    warnings: list = field(default_factory=list)

    @property
    def platform(self) -> str:
        return self.recognized.platform

    @property
    def kind(self) -> str:
        return self.recognized.url_kind

    @property
    def ids(self) -> dict[str, str]:
        return self.recognized.ids
