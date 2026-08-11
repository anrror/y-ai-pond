# y-ai-pond cloud server (TIER 2) — multi-stage Docker build
#
# Build:
#   docker build -t y-ai-pond:0.1.0 .
#
# With local y-ai-agent-base dependency (requires BuildKit):
#   docker build --build-context yaiagentbase=../y-ai-agent-base -t y-ai-pond:0.1.0 .
#
# Runtime:
#   docker run -p 8080:8080 \
#     -v $(pwd)/config/config.docker.yaml:/app/config/config.docker.yaml:ro \
#     -e POND_CONFIG=/app/config/config.docker.yaml \
#     y-ai-pond:0.1.0
#
# ── Stage 1: Build ──────────────────────────────────────────────────────────

FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Copy local y-ai-agent-base dependency (requires --build-context or
# additional_contexts in docker-compose; see docker-compose.yml for setup)
COPY --from=yaiagentbase . /y-ai-agent-base

# Download Go module dependencies (layer cache)
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN CGO_ENABLED=0 go build \
  -ldflags="-s -w" \
  -trimpath \
  -o /app/server \
  ./cmd/server

# ── Stage 2: Runtime ───────────────────────────────────────────────────────

FROM alpine:3.21

RUN apk add --no-cache ca-certificates curl tzdata

# Non-root user
RUN adduser -D -H appuser

WORKDIR /app
COPY --from=builder /app/server .

# Create directories for config and data
RUN mkdir -p /app/config /app/data && \
  chown -R appuser:appuser /app

USER appuser

EXPOSE 8080

# Health check — curl exits non-zero on HTTP errors (4xx/5xx)
HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=5 \
  CMD ["curl", "-f", "http://localhost:8080/health"]

ENTRYPOINT ["/app/server"]
