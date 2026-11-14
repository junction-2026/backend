"""Logging that never carries credentials: Authorization headers and secret env values are redacted."""
import logging
import os
import re
import traceback

_AUTH = re.compile(r"(?i)(authorization['\"]?\s*[:=]\s*['\"]?)(?:bearer|basic|token)?\s*[^\s,'\"}]+")
_SECRET_NAME = re.compile(r"(?i)token|secret|key|password|passwd|ct0|cookie|session")


def _secret_values() -> list[str]:
    # Read at log time so credentials set by tests or late config are covered too.
    return [v for k, v in os.environ.items() if _SECRET_NAME.search(k) and len(v) >= 6]


def redact(text: str) -> str:
    text = _AUTH.sub(r"\1[redacted]", text)
    for v in _secret_values():
        text = text.replace(v, "[redacted]")
    return text


class RedactFilter(logging.Filter):
    def filter(self, record: logging.LogRecord) -> bool:
        msg = record.getMessage()
        clean = redact(msg)
        if record.exc_info:  # format the traceback ourselves so it is redacted too
            clean += "\n" + redact("".join(traceback.format_exception(*record.exc_info)))
            record.exc_info = None
        if clean != msg:  # leave untouched records alone: uvicorn's access formatter needs record.args
            record.msg, record.args = clean, ()
        return True


def setup() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    f = RedactFilter()
    for name in ("", "uvicorn", "uvicorn.error", "uvicorn.access"):
        for h in logging.getLogger(name).handlers:
            if not any(isinstance(x, RedactFilter) for x in h.filters):
                h.addFilter(f)
    for noisy in ("httpx", "httpcore"):  # their INFO lines carry full request URLs
        logging.getLogger(noisy).setLevel(logging.WARNING)
