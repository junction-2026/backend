"""The one place platform modules are registered. Add a module = add it to MODULES.

Order matters only for mastodon, which accepts any host that is not a listed platform, so it stays last.
"""
from .errors import ScrapeError
from .platforms import bluesky, facebook, instagram, mastodon, threads, x
from .platforms.base import Recognized
from .urls import normalize

MODULES = [bluesky, instagram, threads, facebook, x, mastodon]


def resolve(url: str):
    """-> (module, Recognized). Raises ScrapeError unsupported_url (or a module's own typed error)."""
    n = normalize(url)
    for module in MODULES:
        rec: Recognized | None = module.recognize(n)
        if rec is not None:
            return module, rec
    raise ScrapeError("unsupported_url", "not a recognized public post or profile URL of a supported platform")


def recognize(url: str) -> Recognized:
    return resolve(url)[1]
