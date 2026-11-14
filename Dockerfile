# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/orchestrator ./cmd/orchestrator

# distroless static: CA certificates for https analyzers, no shell, non-root user 65532.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/orchestrator /orchestrator
COPY config/analyzers /etc/orchestrator/analyzers
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/orchestrator"]
