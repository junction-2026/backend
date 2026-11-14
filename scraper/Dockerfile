FROM python:3.12-slim
# git: platform modules installed later pull some dependencies from git repositories
# libatomic1: the Playwright driver (node) needs it before it can install chromium
RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates libatomic1 \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /srv/scraper
COPY pyproject.toml ./
COPY app ./app
# Facebook module: chromium for Playwright (with its system libraries), in a path the unprivileged user can read
ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright
RUN pip install --no-cache-dir . \
    && playwright install --with-deps chromium \
    && chmod -R a+rX /ms-playwright \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --no-create-home scraper
# HOME is writable for chromium and the X account pool (a private temp dir under /tmp unless SCRAPER_X_POOL_DB is set)
ENV HOME=/tmp
USER scraper
EXPOSE 8003
HEALTHCHECK --interval=30s --timeout=5s CMD python -c "import urllib.request as u; u.urlopen('http://127.0.0.1:8003/health', timeout=4)"
# SCRAPER_TOKEN must be supplied at runtime (docker run -e / env file); the image contains no secrets.
CMD ["uvicorn", "app.main:create_app", "--factory", "--host", "0.0.0.0", "--port", "8003"]
