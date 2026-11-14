"""URL normalization shared by every platform module."""
from dataclasses import dataclass
from urllib.parse import urlsplit

# Hosts owned by the non-Mastodon platforms (after prefix stripping). Mastodon is "any other host".
LISTED_HOSTS = frozenset({
    "bsky.app", "instagram.com", "threads.net", "threads.com", "facebook.com", "twitter.com", "x.com",
})
_PREFIXES = ("www.", "m.", "mobile.")


@dataclass(frozen=True)
class NormalizedUrl:
    host: str                  # lower case, one www./m./mobile. prefix removed
    segments: tuple[str, ...]  # non-empty path segments; query string and fragment are dropped


def normalize(url: str) -> NormalizedUrl:
    parts = urlsplit(url)
    host = (parts.hostname or "").lower().rstrip(".")
    for prefix in _PREFIXES:
        if host.startswith(prefix):
            host = host[len(prefix):]
            break
    return NormalizedUrl(host, tuple(s for s in parts.path.split("/") if s))
