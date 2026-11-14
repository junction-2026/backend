"""Settings from the environment only. Nothing here is ever logged."""
import os
from dataclasses import dataclass


@dataclass(frozen=True)
class Settings:
    token: str
    upstream_timeout_s: float = 30.0


def load_settings(env=os.environ) -> Settings:
    """Raises SystemExit with a clear message so a misconfigured container stops at startup."""
    token = env.get("SCRAPER_TOKEN", "")
    if not token:
        raise SystemExit("SCRAPER_TOKEN is required but not set: refusing to start without an auth token")
    raw = env.get("SCRAPER_UPSTREAM_TIMEOUT_SECONDS", "30")
    try:
        timeout = float(raw)
        if timeout <= 0:
            raise ValueError
    except ValueError:
        raise SystemExit("SCRAPER_UPSTREAM_TIMEOUT_SECONDS must be a positive number") from None
    return Settings(token=token, upstream_timeout_s=timeout)
