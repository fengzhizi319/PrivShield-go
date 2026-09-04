# =============================================================================
# Console Web Frontend — Multi-stage Dockerfile
# =============================================================================
#
# 本 Dockerfile 采用多阶段构建，将「前端编译」和「静态文件服务」分离：
#
#   - Stage 1 (Builder)：需要完整的 Node.js 工具链 + pnpm/npm 将 React/TypeScript
#     源码编译为静态 HTML/CSS/JS 产物（dist 目录）
#   - Stage 2 (Runtime)：只需要 Nginx 托管静态文件，无需 Node.js 运行时
#
# 最终产出的运行镜像不包含 Node.js、npm/pnpm、源码及 node_modules，仅保留：
#   1. Vite 构建的静态产物（/usr/share/nginx/html）
#   2. Nginx 配置文件（nginx.conf）
#   3. 非 root 用户 privacy（Nginx worker 进程降权运行）
#
# 两阶段对比：
#   ┌──────────────┬───────────────────────────┬──────────────────────────────┐
#   │              │ Stage 1: Builder          │ Stage 2: Runtime             │
#   ├──────────────┼───────────────────────────┼──────────────────────────────┤
#   │ 基础镜像       │ node:20.18.0-alpine3.20   │ nginx:1.26.2-alpine          │
#   │ 包含内容       │ Node.js+源码+node_modules  │ 仅静态 HTML/CSS/JS + Nginx    │
#   │ 镜像体积       │ ~350MB（仅构建用）           │ ~25MB（最终交付）              │
#   │ 是否进入最终    │ 否，构建完即丢弃             │ 是，作为生产运行镜像             │
#   │ 安全暴露面     │ 大（含 Node.js、npm 等）     │ 极小（仅 Nginx 静态服务）       │
#   └──────────────┴───────────────────────────┴──────────────────────────────┘
#
# 运行时依赖分析（Runtime dependency analysis）：
#   必须：
#     - 静态产物（dist/）— Vite 构建的 HTML/CSS/JS，由 Nginx 直接托管
#     - Nginx 配置（nginx.conf）— 定义路由规则、SPA 回退、API 代理等
#   不需要：
#     - Node.js 运行时 — 前端已编译为纯静态文件，无需 JS 执行环境
#     - node_modules — 构建时已打包，运行时零依赖
#     - 配置文件 — 前端配置在构建时内联到 JS 中，运行时通过环境变量覆盖（可选）
#
# 锁定 Node 和 Nginx 版本，确保构建可追溯性
# Pinned Node and Nginx versions for build reproducibility
# =============================================================================

# ==============================================================================
# Stage 1: Build（前端构建阶段）
# ==============================================================================
# 基础镜像：node:20.18.0-alpine3.20
#   - 包含完整的 Node.js 20 LTS 运行时、npm 包管理器
#   - 基于 Alpine 3.20，体积远小于 debian-based 的 Node 镜像
#   - 此阶段安装 pnpm（更快的包管理器）并通过国内镜像加速依赖下载
#   - 构建产出：Vite 将 React/TypeScript 源码编译为静态 HTML/CSS/JS（dist 目录）
#   - 该阶段的所有内容（源码、node_modules、Node.js 运行时）不会进入最终镜像
FROM node:20.18.0-alpine3.20 AS builder

WORKDIR /app

# 复制全部前端文件（若已在宿主机编译出 dist/ 则直接复用，秒级构建）
# Copy all frontend files (if dist/ is already built on host, reuse directly)
COPY . .

RUN if [ ! -d "dist" ] || [ ! -f "dist/index.html" ]; then \
        npm config set registry https://registry.npmmirror.com \
        && npm config set fetch-retries 5 \
        && npm install --no-audit \
        && npm run build; \
    fi

# ==============================================================================
# Stage 2: Runtime（Nginx 静态文件服务阶段 — 最小化攻击面）
# ==============================================================================
# 基础镜像：nginx:1.26.2-alpine（~10MB 的轻量 Nginx + Alpine）
#   - 仅包含 Nginx 静态文件服务器，不含 Node.js、编译器或任何开发工具
#   - 与 Stage 1 完全隔离：通过 COPY --from=builder 仅取走 dist/ 静态产物
#   - Nginx 是业界标准的高性能静态文件服务器，支持 gzip、缓存控制、SPA 回退等
#   - worker 进程降权为 privacy 用户运行，遵循最小权限原则
# ==============================================================================
FROM nginx:1.26.2-alpine

# 创建非 root 用户（Nginx 架构说明）
# Create non-root user
#
# Nginx 权限模型：
#   - master 进程：以 root 启动（需绑定 80/443 等特权端口）
#   - worker 进程：启动后立即降权为指定用户运行（处理客户端请求）
#   本配置将 worker 进程降权为 privacy 用户，即使 worker 被攻破也无法获取 root 权限
#
# ① 替换 Alpine 包源为阿里云镜像（2>/dev/null || true 容错：镜像源替换失败不阻断构建）
# ② 创建系统用户组 privacy（-S = 系统组）
# ③ 创建系统用户 privacy：
#    -h /var/cache/nginx  家目录设为 Nginx 缓存目录（worker 进程需要读写该目录）
#    -s /sbin/nologin     禁止登录 shell（安全加固）
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories 2>/dev/null || true \
    && addgroup -S privacy \
    && adduser -S -G privacy -h /var/cache/nginx -s /sbin/nologin privacy

# 复制构建产物与 Nginx 配置
# Copy build artifacts and Nginx config
#
# --from=builder  从 Stage 1（Node.js 构建阶段）复制文件
# /app/dist       Vite 构建产出的静态文件目录（index.html + assets/）
# /usr/share/nginx/html  Nginx 默认的静态文件根目录
#
# nginx.conf      自定义 Nginx 配置（从宿主机复制），定义：
#                 - SPA 回退路由（非 /api 路径一律返回 index.html）
#                 - /api 反向代理到后端服务
#                 - 静态资源缓存策略（带哈希的文件强缓存）
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf

# 设置 Nginx 各工作目录的所有权为 privacy 用户
# Set ownership of Nginx working directories to privacy user
# Nginx worker 进程以 privacy 用户运行，必须有权限读写以下目录：
#   /usr/share/nginx/html  静态文件目录（读取）
#   /var/cache/nginx       Nginx 缓存目录（读写）
#   /var/log/nginx         访问日志/错误日志目录（写入）
#   /var/run/nginx.pid     Nginx master 进程 PID 文件（master 以 root 写入，worker 需读取）
RUN chown -R privacy:privacy /usr/share/nginx/html \
    && chown -R privacy:privacy /var/cache/nginx \
    && chown -R privacy:privacy /var/log/nginx \
    && touch /var/run/nginx.pid \
    && chown -R privacy:privacy /var/run/nginx.pid

# 声明容器监听端口 5173（与 Vite 开发服务器端口一致，切换开发/生产无感）
# Document the container's listening port (matches Vite dev server port for seamless dev/prod switch)
EXPOSE 5173

# 容器健康检查：使用 wget 探测首页（Nginx Alpine 镜像无 curl，用 wget 替代）
# Container health check: probe homepage with wget (Alpine Nginx image has no curl)
# --no-verbose  安静模式，仅输出错误
# --tries=1     单次尝试，快速失败
# --spider      仅发 HEAD 请求，不下载页面内容（节省带宽）
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:5173/ || exit 1

# 启动命令：以非守护模式运行 Nginx（Docker 容器必须前台运行，否则容器会立即退出）
# Startup command: run Nginx in non-daemon mode
# (Docker containers must run in foreground; daemon off prevents immediate exit)
# -g "daemon off;"  全局指令：禁止 Nginx 后台化，确保 Docker 能正确管理进程生命周期
CMD ["nginx", "-g", "daemon off;"]
