# syntax=docker/dockerfile:1
# Scraper service (cmd/scraper): static Go binary plus chromium for the browser-based platform modules.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/scraper ./cmd/scraper

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends chromium ca-certificates fonts-liberation \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --no-create-home --uid 10001 scraper
COPY --from=build /out/scraper /usr/local/bin/scraper
# HOME is writable for chromium's profile directory.
ENV HOME=/tmp
USER scraper
EXPOSE 8003
HEALTHCHECK --interval=30s --timeout=5s CMD ["/usr/local/bin/scraper", "-healthcheck"]
# SCRAPER_TOKEN must be supplied at runtime (docker run -e / env file); the image contains no secrets.
ENTRYPOINT ["/usr/local/bin/scraper"]
