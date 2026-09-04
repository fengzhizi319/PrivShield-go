# ============================================================================
# Service Hub — Multi-stage Dockerfile
# 数据服务调度中枢多阶段轻量化 Docker 构建清单
#
# 构建说明：
#   构建上下文必须设为项目根目录（.），以确保能正确访问根目录的 go.work、pkg/ 共享包及跨模块依赖。
#   构建命令：docker build -f services/service-hub/Dockerfile -t service-hub:latest .
#
# 架构分层：
#   Stage 1 (builder): 基于 golang:1.25-alpine 静态编译 CGO_ENABLED=0 二进制程序；
#   Stage 2 (runtime): 基于 alpine:3.21 极简基础镜像，创建非 root 安全运行用户 privacy，
#                      挂载 /app/data 数据卷并配置容器健康检查指令。
# ============================================================================

# ── Stage 1: 编译构建阶段 ───────────────────────────────────────────────────
FROM golang:1.25-alpine3.21 AS builder

WORKDIR /app

# 配置 国内加速 Go 模块代理与自动工具链升级
ENV GOPROXY=https://goproxy.cn,https://goproxy.io,https://mirrors.aliyun.com/goproxy/,direct
ENV GOTOOLCHAIN=auto

# 配置 阿里云 Alpine 软件源镜像并安装必要编译工具
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories \
    && apk add --no-cache git ca-certificates

# 复制共享公共基础库与依赖模块源码
COPY pkg/ ./pkg/
COPY console/mock-datasource/ ./console/mock-datasource/
COPY services/service-hub/ ./services/service-hub/

WORKDIR /app/services/service-hub
RUN if [ -f "./bin/server" ]; then \
        cp ./bin/server /app/server; \
    else \
        GOWORK=off CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/server ./cmd/server; \
    fi

# ── Stage 2: 生产运行时阶段 ─────────────────────────────────────────────────
FROM alpine:3.21

WORKDIR /app

# 安装基础 CA 证书与健康检查 curl，创建无登录权限的非 root 用户 privacy（UID/GID 系统自动分配）
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories \
    && apk add --no-cache ca-certificates curl \
    && addgroup -S privacy \
    && adduser -S -G privacy -h /app -s /sbin/nologin privacy

# 从 builder 阶段复制编译好的可执行文件
COPY --from=builder /app/server /app/server

# 创建 SQLite 持久化存储目录并配置目录属主权限
RUN mkdir -p /app/data && chown -R privacy:privacy /app

# 暴露 HTTP + gRPC 双协议监听端口
EXPOSE 8082 50052

# 声明数据卷用于 SQLite 数据持久化（生产部署时需绑定挂载到宿主机目录）
VOLUME ["/app/data"]

# 容器原生健康检查探针：每 30 秒轮询探测 /health
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8082/health || exit 1

# 切换为非 root 安全用户执行
USER privacy

# 默认运行环境变量
ENV SERVICE_HUB_HOST=0.0.0.0
ENV SERVICE_HUB_PORT=8082
ENV SERVICE_HUB_GRPC_HOST=0.0.0.0
ENV SERVICE_HUB_GRPC_PORT=50052
ENV PRIVACY_AGENT_REST_HOST=privshield-agent
ENV PRIVACY_REST_PORT=8079

# 容器启动入口
CMD ["/app/server"]

