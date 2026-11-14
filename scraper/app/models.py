"""Request body and ScrapeResult objects of social-scraper-api."""
from pydantic import BaseModel, Field, StrictInt, field_validator
from urllib.parse import urlsplit


class ScrapeBody(BaseModel):
    """POST /scrape body. Unknown members are ignored (tolerant reader)."""

    url: str = Field(max_length=2048)
    max_posts: StrictInt = Field(default=20, ge=1, le=100)
    max_comments: StrictInt = Field(default=50, ge=0, le=200)

    @field_validator("url")
    @classmethod
    def _absolute_http(cls, v: str) -> str:
        parts = urlsplit(v.strip())
        if parts.scheme not in ("http", "https") or not parts.hostname:
            raise ValueError("must be an absolute http or https URL")
        return v.strip()


class Author(BaseModel):
    username: str
    display_name: str | None = None


class Post(BaseModel):
    id: str
    text: str = ""
    created_at: str | None = None
    url: str | None = None
    likes: int | None = None
    replies: int | None = None
    reposts: int | None = None


class Comment(Post):
    author: Author
    in_reply_to_id: str | None = None


class ScrapeWarning(BaseModel):
    code: str
    message: str


class ScrapeResult(BaseModel):
    platform: str
    url_kind: str
    username: str
    display_name: str | None = None
    bio: str | None = None
    profile_url: str | None = None
    followers: int | None = None
    following: int | None = None
    post_count: int | None = None
    verified: bool | None = None
    scraped_at: str = ""      # set by the core after the module returns
    latency_ms: int = 0       # set by the core after the module returns
    posts: list[Post] = []
    post: Post | None = None
    comments: list[Comment] = []
    warnings: list[ScrapeWarning] = []
