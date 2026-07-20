# syntax=docker/dockerfile:1

# ── Stage 1: build a static binary ───────────────────────────────────────
FROM golang:1.24-alpine AS build
WORKDIR /src

# Cache modules first.
COPY go.mod go.sum ./
RUN go mod download

# Then the source (openapi.yaml is embedded via go:embed, so it ships inside
# the binary — no need to copy it into the runtime image).
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/neo-server ./cmd/server

# ── Stage 2: minimal runtime ─────────────────────────────────────────────
FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 10001 neo

WORKDIR /app
COPY --from=build /out/neo-server /usr/local/bin/neo-server

# /data holds the corpus + all state and is mounted as a volume so it
# survives container restarts and image upgrades. Logs go under it too, so
# they persist as well (and are still echoed to stdout for `docker logs`).
ENV API_PORT=8001 \
    DATA_DIR=/data \
    LOG_FILE=/data/logs/go-suggest.log

RUN mkdir -p /data && chown -R neo:neo /data
USER neo
VOLUME ["/data"]
EXPOSE 8001

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8001/health || exit 1

ENTRYPOINT ["neo-server"]
