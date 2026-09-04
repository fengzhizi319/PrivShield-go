#!/usr/bin/env bash
# ============================================================================
# 【生产模式】PrivShield 生产级 Docker Compose 一键部署脚本
# Launch PrivShield in Production Mode with Docker Compose Orchestration
#
# 执行步骤总览：
#   1. 解析命令行参数（compose 文件、是否启用 LLM/监控、是否重新构建/拉取镜像）
#   2. 前置检查：docker 与 docker compose 命令是否可用
#   3. 准备持久化数据目录（.data / .logs）
#   4. 构建 --profile 参数（根据 --with-llm / --with-monitoring 开关）
#   5. 检查 compose 文件存在性（带回退逻辑）
#   6. 执行 docker compose up -d 启动全部服务容器
#   7. 轮询等待核心 Agent 就绪探针（GET /readyz 返回 200）
#   8. 输出部署结果、服务访问地址与常用维护命令
#
# 用法 / Usage:
#   ./scripts/prod/deploy-docker-compose.sh [选项]
#
# 选项 / Options:
#   -f, --file FILE      指定 Compose 配置文件 (默认: docker-compose.prod.yml)
#   --with-llm           启用 vLLM 大模型推理容器 (需具备 NVIDIA GPU / CUDA 环境)
#   --with-monitoring    启用生产监控栈 (Prometheus + Grafana)
#   --with-postgres      启用 Phase B PostgreSQL 多副本 Hub 模式
#   --build              强制重新构建容器镜像 (默认使用已有镜像)
#   --pull               拉取最新基础镜像
#   -h, --help           显示帮助信息
# ============================================================================

# set -e: 任何命令返回非零状态码立即退出（防止错误级联）
# set -u: 引用未定义变量时报错（防止拼写错误导致静默失败）
# set -o pipefail: 管道中任一命令失败则整体返回非零（防止 | 后掩盖错误）
set -euo pipefail
export NO_PROXY="*"
export no_proxy="*"

# ── 步骤 0：定位路径 ──────────────────────────────────────────────────────
# 通过 $0（脚本自身路径）反推项目根目录，确保无论从哪里调用都能正确定位 compose 文件
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"          # 脚本所在目录：scripts/prod/
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"       # 上溯两级：项目根目录
COMPOSE_DIR="$PROJECT_ROOT/deploy/docker-compose"      # Docker Compose 编排文件目录

# ── 步骤 1：设置参数默认值 ────────────────────────────────────────────────
# 默认使用生产 compose 文件；LLM/监控默认关闭；构建/拉取标志默认空
COMPOSE_FILE="docker-compose.prod.yml"
GO_ENGINE=true                                         # 默认使用 Go 原生高性能引擎
WITH_LLM=false                                         # 是否启用 vLLM GPU 推理容器
WITH_MONITORING=false                                  # 是否启用 Prometheus + Grafana 监控
WITH_POSTGRES=false                                    # 是否启用 Phase B PostgreSQL
BUILD_FLAG=""                                          # 非空时 docker compose up 前重新构建镜像
PULL_FLAG=""                                           # 非空时强制拉取最新基础镜像
AGENT_ONLY=false                                       # 是否仅启动核心 Agent (不启动前端与代理)

# ── 步骤 2：解析命令行参数 ────────────────────────────────────────────────
# 遍历所有位置参数，按 --key value 配对消费（shift 跳过已处理的参数）
while [[ $# -gt 0 ]]; do
    case "$1" in
        -f|--file)
            # 指定自定义 compose 文件（如 docker-compose.dev.yml / docker-compose.test.yml）
            COMPOSE_FILE="$2"
            shift 2
            ;;
        --go-engine)
            # 启用 Go 原生引擎覆盖层
            GO_ENGINE=true
            shift 1
            ;;
        --agent-only)
            # 仅启动核心 Agent (PrivShield 与 redis)，不启动前端 Web 与后端代理
            AGENT_ONLY=true
            shift 1
            ;;
        --with-llm)
            # 启用 llm profile：启动带 profiles: ["llm"] 的 vLLM 服务（需 GPU 环境）
            WITH_LLM=true
            shift 1
            ;;
        --with-monitoring)
            # 启用 monitoring profile：启动 Prometheus + Grafana 监控套件
            WITH_MONITORING=true
            shift 1
            ;;
        --with-postgres)
            # 启用 phase-b profile：启动 PostgreSQL 容器（多副本 Hub 模式）
            WITH_POSTGRES=true
            shift 1
            ;;
        --build)
            # 设置 --build 标志：up 前强制重新构建镜像（适用于代码变更后）
            BUILD_FLAG="--build"
            shift 1
            ;;
        --pull)
            # 设置 --pull always 标志：强制拉取最新基础镜像（适用于基础镜像更新后）
            PULL_FLAG="--pull always"
            shift 1
            ;;
        -h|--help)
            echo "用法 / Usage: $0 [选项]"
            echo ""
            echo "选项 / Options:"
            echo "  -f, --file FILE      指定 Compose 配置文件 (默认: docker-compose.prod.yml)"
            echo "  --go-engine          使用 Go 原生引擎替代 Python 引擎 (极速轻量)"
            echo "  --agent-only         仅启动核心 Agent 服务 (不拉起 Web 前端与后端代理)"
            echo "  --with-llm           启用 vLLM 大模型 GPU 推理容器"
            echo "  --with-monitoring    启用 Prometheus + Grafana 生产监控套件"
            echo "  --with-postgres      启用 Phase B PostgreSQL 多副本 Hub 模式"
            echo "  --build              在启动前重新构建应用镜像"
            echo "  --pull               拉取最新的依赖基础镜像"
            echo "  -h, --help           显示帮助信息并退出"
            exit 0
            ;;
        *)
            echo "❌ [错误] 未知参数: $1" >&2
            echo "   请运行 $0 --help 查看帮助" >&2
            exit 1
            ;;
    esac
done

# ── 步骤 3：打印部署摘要 ──────────────────────────────────────────────────
echo "============================================================================"
echo "🛡️  【生产模式】PrivShield 生产级 Docker Compose 部署"
echo "============================================================================"

# ── 步骤 4：前置检查 — 工具链可用性 ────────────────────────────────────────
# command -v 检查命令是否存在于 PATH 中；缺失则提示安装方式并退出
# 检查 Docker Engine CLI
if ! command -v docker >/dev/null 2>&1; then
    echo "❌ [错误] 未检测到 docker 命令，请先安装 Docker Engine: https://docs.docker.com/engine/install/" >&2
    exit 1
fi

# 检查 Docker Compose v2 插件（docker compose 子命令）
# 注意：Compose v2 是 Go 重写的插件，与 v1 (docker-compose 独立二进制) 不同
if ! docker compose version >/dev/null 2>&1; then
    echo "❌ [错误] 未检测到 Docker Compose v2 插件，请安装 docker-compose-plugin。" >&2
    exit 1
fi

# ── 步骤 5：准备持久化数据目录 ────────────────────────────────────────────
# 创建项目根目录下的 .data 和 .logs 目录，供 compose 卷挂载使用
#   .data  → 隐私预算数据库（SQLite）、审计日志等持久化数据
#   .logs  → 容器运行日志归档
# mkdir -p: 目录已存在则不报错；chmod 755: 确保容器内非 root 用户可读
mkdir -p "$PROJECT_ROOT/.data" "$PROJECT_ROOT/.logs"
chmod 755 "$PROJECT_ROOT/.data" "$PROJECT_ROOT/.logs"

# ── 步骤 6：构建 --profile 参数 ──────────────────────────────────────────
# 根据命令行开关动态组装 profile 参数，控制哪些可选服务被启动
# compose 文件中：
#   - vllm 服务标记了 profiles: ["llm"]
#   - prometheus/grafana 标记了 profiles: ["monitoring"]
# 未指定 profile 时，仅启动无 profile 的核心服务（agent + console + web）
PROFILES=()
if [[ "$WITH_LLM" == "true" ]]; then
    PROFILES+=("--profile" "llm")
    echo "   • 大模型推理 : 已启用 vLLM (Profile: llm)"
else
    echo "   • 大模型推理 : 未启用 (轻量 Core 规则+NER 模式)"
fi

if [[ "$WITH_MONITORING" == "true" ]]; then
    PROFILES+=("--profile" "monitoring")
    echo "   • 生产监控栈 : 已启用 (Prometheus + Grafana)"
else
    echo "   • 生产监控栈 : 未启用"
fi

if [[ "$WITH_POSTGRES" == "true" ]]; then
    PROFILES+=("--profile" "phase-b")
    echo "   • Phase B PG   : 已启用 (多副本 Hub 模式)"
else
    echo "   • Phase B PG   : 未启用 (单副本 SQLite 模式)"
fi

# ── 步骤 7：切换到编排目录 + 检查 compose 文件存在性 ────────────────
# docker compose 默认在当前工作目录查找 compose 文件；必须 cd 到编排目录
cd "$COMPOSE_DIR"

# 若指定的文件不存在，回退到通用默认文件 docker-compose.yml
# 场景：用户指定 docker-compose.prod.yml 但该文件被删除/重命名时，不至于报错退出
if [[ ! -f "$COMPOSE_FILE" ]]; then
    echo "⚠️  指定的 Compose 文件不存在: $COMPOSE_FILE，回退为 docker-compose.yml"
    COMPOSE_FILE="docker-compose.yml"
fi

COMPOSE_ARGS=("-f" "$COMPOSE_FILE")
if [[ "$GO_ENGINE" == "true" ]]; then
    if [[ -f "docker-compose.prod-go-engine.yml" ]]; then
        COMPOSE_ARGS+=("-f" "docker-compose.prod-go-engine.yml")
    elif [[ -f "docker-compose.go-engine.yml" ]]; then
        COMPOSE_ARGS+=("-f" "docker-compose.go-engine.yml")
    fi
    echo "   • 引擎类型    : Go 原生高性能引擎 (privshield-go:1.0.0)"
else
    echo "   • 引擎类型    : Python 核心引擎"
fi

echo "   • 编排配置文件: ${COMPOSE_ARGS[*]}"

# ── 步骤 8：执行 docker compose up -d 启动服务 ─────────────────────────
echo ""
if [[ "$AGENT_ONLY" == "true" ]]; then
    echo "🚀 正在启动生产级核心 Agent 服务 (Agent-Only)..."
    docker compose "${COMPOSE_ARGS[@]}" "${PROFILES[@]}" up -d $BUILD_FLAG $PULL_FLAG PrivShield redis
else
    echo "🚀 正在启动生产服务容器群..."
    docker compose "${COMPOSE_ARGS[@]}" "${PROFILES[@]}" up -d $BUILD_FLAG $PULL_FLAG
fi

# ── 步骤 9：轮询等待核心 Agent 就绪探针 ────────────────────────────────
# 通过循环探测 /readyz 端点判断服务是否就绪：
#   - /readyz 校验配置解析器 + 隐私预算 DB 连通性，比 /health 更严格
#   - 先尝试 HTTPS（生产默认 TLS），失败则回退 HTTP（兼容未启用 TLS 场景）
#   - 最多等待 30 秒（MAX_ATTEMPTS=30，每次 sleep 1s）
#   - 超时不报错，仅输出警告，运维可后续手动检查
echo ""
echo -n "⏳ 等待 PrivShield 核心 Agent 服务就绪探针响应..."
MAX_ATTEMPTS=30
ATTEMPT=0
READY=false

# 循环探测，每次尝试 HTTPS 和 HTTP 两种方式
while [[ $ATTEMPT -lt $MAX_ATTEMPTS ]]; do
    # curl -s -k: 静默模式 + 跳过证书验证（自签名证书场景）
    # -w "%{http_code}": 仅输出 HTTP 状态码
    # grep -q '^200$': 静默匹配 200 状态码
    if curl -s -k -o /dev/null -w "%{http_code}" "https://127.0.0.1:8079/readyz" 2>/dev/null | grep -q '^200$' || \
       curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:8079/readyz" 2>/dev/null | grep -q '^200$'; then
        READY=true
        break
    fi
    echo -n "."    # 每秒输出一个点号，表示等待进度
    sleep 1
    ATTEMPT=$((ATTEMPT + 1))
done

# 根据是否就绪输出不同提示
if [[ "$READY" == "true" ]]; then
    echo " ✅ 已就绪"
else
    echo " ⚠️  就绪检查等待超时，请使用 'docker compose -f $COMPOSE_FILE logs' 查看容器运行状态。"
fi

# ── 步骤 10：输出部署结果与服务访问地址 ─────────────────────────────────
# 根据启用的 profile 动态输出对应的服务访问地址，未启用的不显示
echo ""
echo "============================================================================"
echo "🎉 PrivShield 生产级服务已启动完毕！"
echo "============================================================================"
# 核心服务地址（始终显示）
echo "  • 核心 Agent REST API : http://127.0.0.1:8079 (或 https://127.0.0.1:8079)"
echo "  • 核心 Agent gRPC RPC : 127.0.0.1:50051"
echo "  • Web 控制台 UI       : http://127.0.0.1:5173"
echo "  • Go BFF 代理网关 REST: http://127.0.0.1:8081"
echo "  • 调度中枢 Service Hub: http://127.0.0.1:8082"
echo "  • 数据源管理 Datasource: http://127.0.0.1:8083"
echo "  • 脱敏审计日志 AuditLog: http://127.0.0.1:8084"
# 可选服务地址（根据 profile 动态显示）
if [[ "$WITH_LLM" == "true" ]]; then
    echo "  • vLLM 大模型推理 API : http://127.0.0.1:8000/v1"
fi
if [[ "$WITH_MONITORING" == "true" ]]; then
    echo "  • Prometheus 监控指标 : http://127.0.0.1:9090"
    echo "  • Grafana 可视化大屏  : http://127.0.0.1:3000 (admin / privshield_admin)"
fi
if [[ "$WITH_POSTGRES" == "true" ]]; then
    echo "  • PostgreSQL (Phase B) : 127.0.0.1:5432 (privshield_hub)"
fi
# 常用维护命令提示
echo "────────────────────────────────────────────────────────────────────────────"
echo "  常用维护命令:"
echo "    - 查看容器运行状态 : cd $COMPOSE_DIR && docker compose -f $COMPOSE_FILE ps"
echo "    - 实时查看服务日志 : cd $COMPOSE_DIR && docker compose -f $COMPOSE_FILE logs -f"
echo "    - 停止生产服务集群 : ./scripts/prod/stop-docker-compose.sh"
echo "    - 生产健康全面巡检 : ./scripts/prod/prod-health-check.sh"
echo "============================================================================"
