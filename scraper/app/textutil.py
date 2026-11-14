"""Plain-text and timestamp normalization shared by all platform modules."""
from datetime import datetime, timezone
from html.parser import HTMLParser


class _Text(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)  # decodes entities
        self.parts: list[str] = []

    def _newline(self):
        if self.parts and not self.parts[-1].endswith("\n"):
            self.parts.append("\n")

    def handle_starttag(self, tag, attrs):
        if tag == "br":
            self.parts.append("\n")
        elif tag == "p":
            self._newline()

    handle_startendtag = handle_starttag

    def handle_endtag(self, tag):
        if tag == "p":
            self._newline()

    def handle_data(self, data):
        self.parts.append(data)


def html_to_text(html: str | None) -> str:
    """HTML -> plain text: br and paragraph boundaries become \\n, entities decoded, link text kept."""
    if not html:
        return ""
    p = _Text()
    p.feed(html)
    p.close()
    return "".join(p.parts).strip()


def to_rfc3339(value: str | datetime | None) -> str | None:
    """Any ISO-8601 timestamp -> `YYYY-MM-DDTHH:MM:SSZ` in UTC; None when absent or unparseable."""
    if value is None or value == "":
        return None
    try:
        dt = value if isinstance(value, datetime) else datetime.fromisoformat(str(value).replace("Z", "+00:00"))
    except ValueError:
        return None
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def now_rfc3339() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
