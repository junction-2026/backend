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
COPY config/finalizer/prompt.md /etc/orchestrator/finalizer/prompt.md
COPY config/extractor/prompt.md /etc/orchestrator/extractor/prompt.md
COPY config/technique_spans/prompt.md /etc/orchestrator/technique_spans/prompt.md
COPY config/llm_techniques/prompt.md /etc/orchestrator/llm_techniques/prompt.md
COPY config/risk_score/risk_score.yaml /etc/orchestrator/risk_score/risk_score.yaml
# RISK_SCORE_CONFIG (unset: built-in defaults) points at a Manipulation Risk Score configuration, e.g. /etc/orchestrator/risk_score/risk_score.yaml.
COPY data/outlets.csv /etc/orchestrator/data/outlets.csv
# DATA_DIR (unset by default: the cache then lives in ./var/wikipedia relative to the working directory) holds the wikipedia analyzer's on-disk cache in
# $DATA_DIR/wikipedia. The container runs as uid 65532: mount a volume there that uid 65532 can write to
# (e.g. -e DATA_DIR=/data -v wiki-cache:/data) to keep the cache across container restarts.
# JOB_STORE=sqlite keeps job history in JOB_STORE_PATH; point it at a volume uid 65532 can write:
# e.g. -e JOB_STORE=sqlite -e JOB_STORE_PATH=/data/jobs.db -v jobs:/data (see README "Job history").
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/orchestrator"]
