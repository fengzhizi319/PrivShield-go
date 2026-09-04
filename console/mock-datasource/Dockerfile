# ============================================================================
# Datasource Manager — Multi-stage Dockerfile
# ============================================================================
# Build context: . (Project root)
# 构建上下文：项目根目录
#
# docker build -f console/mock-datasource/Dockerfile -t datasource-mgr:latest .
# ============================================================================

# Stage 1: Build
FROM golang:1.25-alpine3.21 AS builder

WORKDIR /app

ENV GOPROXY=https://goproxy.cn,https://goproxy.io,https://mirrors.aliyun.com/goproxy/,direct
ENV GOTOOLCHAIN=auto

RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories \
    && apk add --no-cache git ca-certificates

# Copy shared pkg/ and module source
COPY pkg/ ./pkg/
COPY console/mock-datasource/ ./console/mock-datasource/

WORKDIR /app/console/mock-datasource
RUN if [ -f "./bin/server" ]; then \
        cp ./bin/server /app/server; \
    else \
        GOWORK=off CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/server ./cmd/server; \
    fi

# Stage 2: Runtime
FROM alpine:3.21

WORKDIR /app

RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories \
    && apk add --no-cache ca-certificates curl \
    && addgroup -S privacy \
    && adduser -S -G privacy -h /app -s /sbin/nologin privacy

COPY --from=builder /app/server /app/server
COPY data/ /app/data/
COPY console/mock-datasource/samples/ /app/samples/

RUN mkdir -p /app/data && chown -R privacy:privacy /app

EXPOSE 8083 50053

# P27 fix: declare data volume for SQLite persistence
# 声明数据卷用于 SQLite 持久化
VOLUME ["/app/data"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8083/health || exit 1

USER privacy

ENV DATASOURCE_MGR_HOST=0.0.0.0
ENV DATASOURCE_MGR_PORT=8083
ENV DATASOURCE_MGR_GRPC_HOST=0.0.0.0
ENV DATASOURCE_MGR_GRPC_PORT=50053
ENV PRIVACY_AGENT_REST_HOST=privshield-agent
ENV PRIVACY_REST_PORT=8079

CMD ["/app/server"]
