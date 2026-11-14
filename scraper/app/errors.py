"""Error envelope of social-scraper-api: {"error": {"code", "message", "details"?}}."""

# code -> HTTP status (shared spec: request, account/post, rate-limit and upstream error codes)
STATUS = {
    "unauthorized": 401,
    "invalid_request": 400,
    "validation_failed": 422,
    "unsupported_url": 422,
    "unsupported_account_type": 422,
    "account_not_found": 404,
    "post_not_found": 404,
    "account_private": 403,
    "rate_limited": 429,
    "platform_not_configured": 503,
    "upstream_error": 502,
    "upstream_timeout": 504,
    "internal_error": 500,
}


class ScrapeError(Exception):
    """A typed, user-visible failure. `message` must never carry credentials or stack traces."""

    def __init__(self, code: str, message: str, *, platform: str | None = None,
                 headers: dict[str, str] | None = None, **details):
        super().__init__(message)
        self.code, self.message, self.headers = code, message, headers or {}
        self.status = STATUS[code]
        self.details = dict(details)
        if platform:
            self.details["platform"] = platform

    def envelope(self) -> dict:
        err = {"code": self.code, "message": self.message}
        if self.details:
            err["details"] = self.details
        return {"error": err}


def rate_limited(platform: str, retry_after_s: float | None = None) -> ScrapeError:
    if retry_after_s is None:
        return ScrapeError("rate_limited", "the platform throttled the scraper", platform=platform)
    secs = max(0, int(retry_after_s))
    return ScrapeError("rate_limited", "the platform throttled the scraper", platform=platform,
                       headers={"Retry-After": str(secs)}, retry_after_s=secs)
