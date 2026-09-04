# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM node:22-alpine AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/ollama-webui ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache bash ca-certificates curl git jq ripgrep su-exec \
    && addgroup -g 1000 -S webui \
    && adduser -u 1000 -S -G webui webui \
    && mkdir -p /data /workspace /app/web \
    && chown -R webui:webui /data /workspace /app
COPY --from=backend /out/ollama-webui /app/ollama-webui
COPY --from=frontend /src/web/dist/client/ /app/web/
COPY --chmod=755 docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
ENV LISTEN_ADDR=:8080 \
    OLLAMA_BASE_URL=http://ollama:11434 \
    DATABASE_PATH=/data/ollama-webui.db \
    WEB_DIST_DIR=/app/web \
    AGENT_WORKSPACE=/workspace \
    AGENT_CONTEXT_PATH=/data/agent-context \
    AGENT_MAX_TURNS=200 \
    AGENT_CONTEXT_BUDGET_TOKENS=32768 \
    AGENT_INLINE_INPUT_BYTES=16384 \
    AGENT_TOOL_FEEDBACK_BYTES=8192 \
    SHELL_TIMEOUT_SECONDS=600 \
    SHELL_MAX_OUTPUT_BYTES=65536
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/api/health || exit 1
ENTRYPOINT ["docker-entrypoint.sh"]
