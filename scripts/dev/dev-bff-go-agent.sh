#!/usr/bin/env bash
# ============================================================================
# 【开发模式】一键启动 Go 原生引擎 + Go BFF + Vite 控制台 (HMR 热更新)
# Launch Go Engine + Go BFF + Vite Dev UI in DEV mode
#
# 与 dev-bff-agent.sh 的区别：
#   - dev-bff-agent.sh 使用 Python 引擎（python -m engine.server）
#   - 本脚本使用 Go 原生引擎（go run ./cmd/privshield-agent）
#
# 用法 / Usage:
#   ./scripts/dev/dev-bff-go-agent.sh [--force] [--mtls]
#
# 参数说明 / Options:
#   --force: 非交互模式，端口被占用时自动终止占用进程（CI/脚本化场景）
#   --mtls:  启用 mTLS 双向认证（自动配置/生成证书与双向鉴权环境）
#   -h, --help: 查看帮助说明
# ============================================================================

set -euo pipefail

FORCE=false
MTLS_MODE=false

for arg in "$@"; do
    case "$arg" in
        --force) FORCE=true ;;
        --mtls)  MTLS_MODE=true ;;
        -h|--help)
            echo "用法: $0 [选项]"
            echo "  --force   端口被占用时自动终止占用进程（非交互模式）"
            echo "  --mtls    以 mTLS 双向认证模式启动 Agent 与 Go BFF"
            echo "  -h, --help 显示此帮助信息"
            exit 0
            ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
CONSOLE_DIR="$PROJECT_ROOT/console"
PIDS_DIR="$PROJECT_ROOT/.pids"
LOGS_DIR="$PROJECT_ROOT/.logs"
ENGINE_GO_DIR="$PROJECT_ROOT/engine-go"

mkdir -p "$PIDS_DIR" "$LOGS_DIR"

CERT_DIR="$CONSOLE_DIR/bff-go/certs"
GEN_CERTS="$CONSOLE_DIR/bff-go/scripts/gen-certs.sh"

CONSOLE_URL="http://127.0.0.1:8081"
AGENT_URL="http://127.0.0.1:8079"
if [[ "$MTLS_MODE" == "true" ]]; then
    AGENT_URL="https://127.0.0.1:8079"
fi
AGENT_GRPC_ADDR="127.0.0.1:50051"
VITE_URL="http://localhost:5173"

_is_port_in_use() {
    local port="$1"
    python3 -c "
import socket, sys
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.settimeout(0.5)
try:
    s.connect(('127.0.0.1', $port))
    s.close()
    sys.exit(0)
except (ConnectionRefusedError, socket.timeout, OSError):
    sys.exit(1)
" 2>/dev/null
}

check_port_available() {
    local port="$1"
    local name="$2"
    if ! _is_port_in_use "$port"; then return 0; fi
    echo ""
    echo "⚠️  端口 $port 已被占用（$name）"
    if [[ "$FORCE" == "true" ]]; then
        echo "（--force 非交互模式：自动终止占用进程）"
        if command -v lsof >/dev/null 2>&1; then
            lsof -t -i :"$port" 2>/dev/null | xargs kill -9 2>/dev/null || true
        fi
        sleep 1
        if ! _is_port_in_use "$port"; then
            echo "✅ 端口 $port 已释放"
            return 0
        fi
    fi
    echo "错误：端口 $port 被占用，请手动释放后重试或使用 --force"
    exit 1
}

# 1. Go 工具链检查
if ! command -v go >/dev/null 2>&1; then
    echo "错误：未找到 Go 工具链，请先安装 Go 1.25+。"
    exit 1
fi

# 2. mTLS 证书准备
if [[ "$MTLS_MODE" == "true" ]]; then
    if [[ ! -f "$CERT_DIR/ca.crt" || ! -f "$CERT_DIR/server.crt" || ! -f "$CERT_DIR/client.crt" ]]; then
        echo "未检测到完整证书，正在自动生成开发用自签名证书..."
        bash "$GEN_CERTS" "$CERT_DIR"
    fi
fi

# 3. 前端 node_modules 检查
if [[ ! -d "$CONSOLE_DIR/web/node_modules" ]]; then
    echo "未找到前端 node_modules，自动安装依赖..."
    (
        cd "$CONSOLE_DIR/web"
        if command -v corepack >/dev/null 2>&1; then
            corepack pnpm install
        elif command -v pnpm >/dev/null 2>&1; then
            pnpm install
        elif command -v npm >/dev/null 2>&1; then
            npm install
        fi
    )
fi

# 4. 编译 Go BFF
echo "编译 Go gRPC 代理后端..."
(cd "$CONSOLE_DIR/bff-go" && go build -o bin/backend-go ./cmd/server)

AGENT_PID_FILE="$PIDS_DIR/agent-go-all.pid"
GO_CONSOLE_PID_FILE="$PIDS_DIR/console-go-all.pid"
VITE_PID_FILE="$PIDS_DIR/vite-dev.pid"

write_pid() { echo "$2" > "$1"; }

PIDS=()
STOPPING=false
cleanup() {
    STOPPING=true
    echo ""
    echo "正在停止【开发模式 - Go Engine】控制台所有服务..."
    for pid in "${PIDS[@]}"; do
        kill "$pid" 2>/dev/null || true
    done
    wait 2>/dev/null || true
    rm -f "$AGENT_PID_FILE" "$GO_CONSOLE_PID_FILE" "$VITE_PID_FILE"
    echo "已停止。"
}
trap cleanup INT TERM EXIT

check_port_available 8079 "Go Engine REST"
check_port_available 50051 "Go Engine gRPC"
check_port_available 8081 "Go gRPC 代理后端"
check_port_available 5173 "Vite 前端开发服务器"

# 5. 启动 Go Agent 核心引擎
launch_go_agent() {
    local agent_log="$LOGS_DIR/agent_go_all.log"
    echo "启动 Go Engine (REST: $AGENT_URL, gRPC: $AGENT_GRPC_ADDR)，日志: $agent_log..."
    (
        cd "$ENGINE_GO_DIR"
        export PRIVACY_REST_HOST="127.0.0.1"
        export PRIVACY_REST_PORT="8079"
        export PRIVACY_GRPC_HOST="127.0.0.1"
        export PRIVACY_GRPC_PORT="50051"
        export PRIVACY_LOG_LEVEL="${PRIVACY_LOG_LEVEL:-DEBUG}"
        if [[ "$MTLS_MODE" == "true" ]]; then
            export AGENT_TLS_ENABLED=true
            export AGENT_TLS_CERT_FILE="$CERT_DIR/server.crt"
            export AGENT_TLS_KEY_FILE="$CERT_DIR/server.key"
            export AGENT_TLS_CA_FILE="$CERT_DIR/ca.crt"
            export AGENT_AUTH_INTERNAL_MTLS_ENABLED=true
            export AGENT_AUTH_MTLS_ALLOWED_CNS='["privshield-client"]'
        fi
        exec go run ./cmd/privshield-agent >> "$agent_log" 2>&1
    ) &
    AGENT_PID=$!
    PIDS[0]="$AGENT_PID"
    write_pid "$AGENT_PID_FILE" "$AGENT_PID"
}
launch_go_agent

wait_for_service() {
    local url="$1"
    local name="$2"
    local max_attempts=30
    local attempt=0
    local curl_opts=("--noproxy" "*" "--connect-timeout" "1" "--max-time" "3" "-s" "-o" "/dev/null" "-w" "%{http_code}")
    if [[ "$MTLS_MODE" == "true" ]]; then
        curl_opts+=("-k")
    fi
    echo -n "等待 $name 就绪"
    while [[ $attempt -lt $max_attempts ]]; do
        if [[ "$name" == "Go Engine" ]] && ! kill -0 "$AGENT_PID" 2>/dev/null; then
            echo " 失败（Go Agent 进程已退出）"
            echo "最近日志："
            tail -n 40 "$LOGS_DIR/agent_go_all.log" >&2 || true
            return 1
        fi
        if curl "${curl_opts[@]}" "$url" | grep -q '^200$'; then
            echo " OK"
            return 0
        fi
        echo -n "."
        sleep 1
        attempt=$((attempt + 1))
    done
    echo " 超时（$url）"
    return 1
}

wait_for_service "$AGENT_URL/health" "Go Engine"

echo -n "等待 Go Engine gRPC ($AGENT_GRPC_ADDR) 就绪"
for i in $(seq 1 30); do
    if _is_port_in_use 50051; then
        echo " OK"
        break
    fi
    echo -n "."
    sleep 1
    if [[ $i -eq 30 ]]; then
        echo " 超时"
        exit 1
    fi
done

# 6. 启动 Go BFF 代理网关
echo "启动 Go gRPC 代理后端 (API: $CONSOLE_URL)..."
(
    cd "$CONSOLE_DIR/bff-go"
    if [[ "$MTLS_MODE" == "true" ]]; then
        export PRIVACY_AGENT_TLS_ENABLED=true
        export PRIVACY_AGENT_TLS_CA_FILE="$CERT_DIR/ca.crt"
        export PRIVACY_AGENT_TLS_CERT_FILE="$CERT_DIR/client.crt"
        export PRIVACY_AGENT_TLS_KEY_FILE="$CERT_DIR/client.key"
        export PRIVACY_AGENT_TLS_SERVER_NAME="localhost"
    else
        export PRIVACY_AGENT_TLS_ENABLED=false
    fi
    export CONSOLE_RATE_LIMIT="${CONSOLE_RATE_LIMIT:-0}"
    exec ./bin/backend-go
) > "$LOGS_DIR/backend-go-all.log" 2>&1 &
GO_CONSOLE_PID=$!
PIDS+=("$GO_CONSOLE_PID")
write_pid "$GO_CONSOLE_PID_FILE" "$GO_CONSOLE_PID"

wait_for_service "$CONSOLE_URL/health" "Go gRPC 代理后端"

# 7. 启动 Vite 前端热开发服务器
echo "启动 Vite 前端开发服务器 (UI: $VITE_URL)..."
(
    cd "$CONSOLE_DIR/web"
    export VITE_PROXY_TARGET="$CONSOLE_URL"
    if command -v corepack >/dev/null 2>&1; then
        corepack pnpm dev
    elif command -v pnpm >/dev/null 2>&1; then
        pnpm dev
    else
        npm run dev
    fi
) > "$LOGS_DIR/vite-all.log" 2>&1 &
VITE_PID=$!
PIDS+=("$VITE_PID")
write_pid "$VITE_PID_FILE" "$VITE_PID"

wait_for_service "$VITE_URL" "Vite 前端"

echo ""
echo "================================================================="
if [[ "$MTLS_MODE" == "true" ]]; then
    echo "🎉 PrivShield Go Engine (Agent + Go BFF + Vite UI) [mTLS] 已就绪！"
else
    echo "🎉 PrivShield Go Engine (Agent + Go BFF + Vite UI) 已全部启动！"
fi
echo "  🌐 前端界面 (UI):    $VITE_URL"
echo "  🔌 代理后端 (Go):    $CONSOLE_URL"
echo "  🛡️  Go 隐私引擎:      $AGENT_URL (REST) / $AGENT_GRPC_ADDR (gRPC)"
if [[ "$MTLS_MODE" == "true" ]]; then
    echo "  🔒 安全模式 (mTLS):  已启用"
fi
echo "  🛑 停止所有服务:  按 Ctrl+C 或运行 ./scripts/dev/dev-stop.sh"
echo "================================================================="

wait
