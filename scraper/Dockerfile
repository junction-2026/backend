FROM python:3.12-slim
# git: platform modules installed later pull some dependencies from git repositories
RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /srv/scraper
COPY pyproject.toml ./
COPY app ./app
RUN pip install --no-cache-dir . && useradd --system --no-create-home scraper
USER scraper
EXPOSE 8003
HEALTHCHECK --interval=30s --timeout=5s CMD python -c "import urllib.request as u; u.urlopen('http://127.0.0.1:8003/health', timeout=4)"
# SCRAPER_TOKEN must be supplied at runtime (docker run -e / env file); the image contains no secrets.
CMD ["uvicorn", "app.main:create_app", "--factory", "--host", "0.0.0.0", "--port", "8003"]
