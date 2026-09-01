# ============================================================================
# PrivShield App-LZ BFF — Multi-stage Dockerfile
# ============================================================================
# Build context: . (Project root)
#
# docker build -f console/app-lz/bff-go/Dockerfile -t privshield-app-lz-bff:1.8.0 .

FROM golang:1.25-alpine3.21 AS builder

WORKDIR /app

ENV GOPROXY=https://goproxy.cn,https://goproxy.io,https://mirrors.aliyun.com/goproxy/,direct
ENV GOTOOLCHAIN=auto

RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories \
    && apk add --no-cache git ca-certificates

COPY pkg/ ./pkg/
COPY console/app-lz/bff-go/ ./console/app-lz/bff-go/

WORKDIR /app/console/app-lz/bff-go
RUN if [ -f "./bin/server" ]; then \
        cp ./bin/server /app/server; \
    else \
        GOWORK=off CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/server ./cmd/server; \
    fi

FROM alpine:3.21

WORKDIR /app

RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories \
    && apk add --no-cache ca-certificates curl \
    && addgroup -S privacy \
    && adduser -S -G privacy -h /app -s /sbin/nologin privacy

COPY --from=builder /app/server /app/server

RUN chown -R privacy:privacy /app

# 仅 HTTP REST 面：本进程没有 gRPC 服务端（原 50055 属 P2-1 配置漂移，已删除）
EXPOSE 8085

HEALTHCHECK --interval=10s --timeout=5s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8085/api/health || exit 1

USER privacy

ENV APP_LZ_PORT=8085
ENV APP_LZ_HOST=0.0.0.0
ENV HUB_URL=http://service-hub:8082
ENV DATASOURCE_URL=http://datasource-mgr:8083
ENV AUDIT_URL=http://audit-log:8084
ENV AGENT_URL=http://PrivShield:8079

CMD ["/app/server"]
