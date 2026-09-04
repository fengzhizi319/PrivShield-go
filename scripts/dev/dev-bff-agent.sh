#!/usr/bin/env bash
# ============================================================================
# 【开发模式】一键启动 PrivShield 引擎 + Go BFF + Vite 控制台 (HMR 热更新)
# Launch PrivShield Agent + Go BFF + Vite Dev UI in DEV mode
#
# 用法 / Usage:
#   ./scripts/dev/dev-bff-agent.sh [--force] [--mtls]
#
# 参数说明 / Options:
#   --force: 非交互模式，端口被占用时自动终止占用进程（CI/脚本化场景）
#   --mtls:  启用 mTLS 双向认证（自动配置/生成证书与双向鉴权环境）
#   -h, --help: 查看帮助说明
# ============================================================================

set -euo pipefail
export CGO_ENABLED=0

# ── 解析命令行参数 ───────────────────────────────────────────────────
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

# ── 解析脚本目录，初始化全局变量 ──────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
PROJECT_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel 2>/dev/null || (cd "$SCRIPT_DIR/../.." && pwd -P))"
CONSOLE_DIR="$PROJECT_ROOT/console/engine-console"
PIDS_DIR="$PROJECT_ROOT/.pids"
LOGS_DIR="$PROJECT_ROOT/.logs"

mkdir -p "$PIDS_DIR" "$LOGS_DIR"

AGENT_VENV="$PROJECT_ROOT/.venv"
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
    local port="${1:-}"
    local name="${2:-服务}"

    if [[ -z "$port" ]] || ! _is_port_in_use "$port"; then
        return 0
    fi

    echo ""
    echo "⚠️  端口 $port 已被占用（$name）"
    echo "────────────────────────────────────────"

    # 1. 检查是否有 Docker 容器映射/占用了该端口
    if command -v docker >/dev/null 2>&1; then
        local docker_containers=""
        docker_containers=$(docker ps --filter "publish=$port" --format "{{.ID}} ({{.Names}})" 2>/dev/null || true)
        if [[ -n "$docker_containers" ]]; then
            echo "📦 检测到 Docker 容器正在占用此端口:"
            echo "$docker_containers"
            local docker_answer=""
            if [[ "$FORCE" == "true" ]]; then
                echo "（--force 非交互模式：自动停止占用端口 $port 的 Docker 容器）"
                docker_answer="y"
            elif [[ ! -t 0 ]]; then
                echo "错误：端口 $port 被 Docker 容器占用且当前为非交互环境。请使用 --force 自动停止容器，或先运行 ./scripts/dev/docker-stop.sh。"
                exit 1
            else
                read -rp "是否自动停止上述 Docker 容器以释放端口？[y/N] " docker_answer
            fi
            case "$docker_answer" in
                [yY]|[yY][eE][sS])
                    for cid in $(docker ps -q --filter "publish=$port" 2>/dev/null); do
                        echo "正在停止 Docker 容器 $cid..."
                        docker stop "$cid" >/dev/null 2>&1 || docker kill "$cid" >/dev/null 2>&1 || true
                    done
                    sleep 1
                    if ! _is_port_in_use "$port"; then
                        echo "✅ 端口 $port 已成功释放 (Docker 容器已停止)"
                        return 0
                    fi
                    ;;
                *)
                    echo "已取消。请手动停止 Docker 容器或释放端口 $port 后重试。"
                    exit 1
                    ;;
            esac
        fi
    fi

    # 2. 检查宿主机本地进程 PID
    local pids=""
    if command -v lsof >/dev/null 2>&1; then
        pids=$( (lsof -t -i :"$port" 2>/dev/null || true) | sort -u | tr '\n' ' ')
    elif command -v ss >/dev/null 2>&1; then
        pids=$( (ss -tlnp 2>/dev/null || true) | (grep -E "LISTEN.*:$port\\s" || true) | sed -n 's/.*pid=\([0-9]*\).*/\1/p' | sort -u | tr '\n' ' ')
    elif command -v fuser >/dev/null 2>&1; then
        pids=$(fuser "$port"/tcp 2>/dev/null | tr -s ' ' || true)
    fi

    if [[ -z "$pids" ]]; then
        if [[ "$FORCE" == "true" ]] && command -v fuser >/dev/null 2>&1; then
            echo "（--force 模式：尝试使用 fuser 释放端口 $port）"
            fuser -k -9 "$port/tcp" 2>/dev/null || true
            sleep 1
            if ! _is_port_in_use "$port"; then
                echo "✅ 端口 $port 已释放"
                return 0
            fi
        fi
        echo "错误：无法定位占用端口 $port 的进程，请手动排查。"
        exit 1
    fi

    local answer=""
    if [[ "$FORCE" == "true" ]]; then
        echo "（--force 非交互模式：自动终止占用端口 $port 的进程）"
        answer="y"
    elif [[ ! -t 0 ]]; then
        echo "错误：端口 $port 被占用且当前为非交互环境（无 TTY）。请手动释放端口，或使用 --force 自动处理。"
        exit 1
    else
        read -rp "是否自动终止上述进程以释放端口？[y/N] " answer
    fi
    case "$answer" in
        [yY]|[yY][eE][sS])
            for pid in $pids; do
                kill -9 "$pid" 2>/dev/null || true
            done
            sleep 1
            if ! _is_port_in_use "$port"; then
                echo "✅ 端口 $port 已释放"
            else
                echo "错误：端口 $port 仍被占用，请手动排查。"
                exit 1
            fi
            ;;
        *)
            echo "已取消。请手动释放端口 $port 后重试。"
            exit 1
            ;;
    esac
}

# 1. Agent 虚拟环境检查与初始化
if [[ ! -d "$AGENT_VENV" ]]; then
    echo "未找到 agent 虚拟环境，自动创建并安装依赖：$AGENT_VENV"
    python3 -m venv "$AGENT_VENV"
    (
        source "$AGENT_VENV/bin/activate"
        cd "$PROJECT_ROOT"
        pip install --upgrade pip >/dev/null
        pip install -e .
    )
fi

# 2. Go 工具链检查
if ! command -v go >/dev/null 2>&1; then
    echo "错误：未找到 Go 工具链，请先安装 Go。"
    exit 1
fi

# 3. mTLS 模式下自动确保测试证书存在
if [[ "$MTLS_MODE" == "true" ]]; then
    if [[ ! -f "$CERT_DIR/ca.crt" || ! -f "$CERT_DIR/server.crt" || ! -f "$CERT_DIR/client.crt" ]]; then
        echo "未检测到完整证书，正在自动生成开发用自签名证书..."
        bash "$GEN_CERTS" "$CERT_DIR"
    fi
fi

# 4. 确保前端 node_modules 存在
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

# 5. 编译 Go BFF
echo "编译 Go gRPC 代理后端..."
(cd "$CONSOLE_DIR/bff-go" && go build -o bin/backend-go ./cmd/server)

AGENT_PID_FILE="$PIDS_DIR/agent-all.pid"
GO_CONSOLE_PID_FILE="$PIDS_DIR/console-go-all.pid"
VITE_PID_FILE="$PIDS_DIR/vite-dev.pid"

write_pid() {
    echo "$2" > "$1"
}

PIDS=()
STOPPING=false
cleanup() {
    STOPPING=true
    echo ""
    echo "正在停止【开发模式】控制台所有服务..."
    for pid in "${PIDS[@]}"; do
        kill "$pid" 2>/dev/null || true
    done
    wait 2>/dev/null || true
    rm -f "$AGENT_PID_FILE" "$GO_CONSOLE_PID_FILE" "$VITE_PID_FILE"
    echo "已停止。"
}
trap cleanup INT TERM EXIT

check_port_available 8079 "PrivShield REST"
check_port_available 50051 "PrivShield gRPC"
check_port_available 8081 "Go gRPC 代理后端"
check_port_available 5173 "Vite 前端开发服务器"

# 6. 启动 Python Agent 核心引擎
launch_agent() {
    local agent_log="$LOGS_DIR/agent_all.log"
    (
        cd "$PROJECT_ROOT"
        if [[ "$MTLS_MODE" == "true" ]]; then
            export AGENT_TLS_ENABLED=true
            export AGENT_TLS_CERT_FILE="$CERT_DIR/server.crt"
            export AGENT_TLS_KEY_FILE="$CERT_DIR/server.key"
            export AGENT_TLS_CA_FILE="$CERT_DIR/ca.crt"
            export AGENT_AUTH_INTERNAL_MTLS_ENABLED=true
            export AGENT_AUTH_MTLS_ALLOWED_CNS='["privshield-client"]'
        else
            export AGENT_TLS_ENABLED=false
            export AGENT_AUTH_INTERNAL_MTLS_ENABLED=false
        fi
        if [[ -f "$PROJECT_ROOT/bin/privshield-agent" ]]; then
            exec "$PROJECT_ROOT/bin/privshield-agent" >> "$agent_log" 2>&1
        else
            exec go run ./services/privacy-engine/cmd/privshield-agent >> "$agent_log" 2>&1
        fi
    ) &
    AGENT_PID=$!
    PIDS[0]="$AGENT_PID"
    write_pid "$AGENT_PID_FILE" "$AGENT_PID"
}
launch_agent

wait_for_service() {
    local url="$1"
    local name="$2"
    local max_attempts=30
    local attempt=0
    # 本地服务不能经过 HTTP(S)_PROXY；否则代理可能把 127.0.0.1 请求吞掉。
    local curl_opts=("--noproxy" "*" "--connect-timeout" "1" "--max-time" "3" "-s" "-o" "/dev/null" "-w" "%{http_code}")
    if [[ "$MTLS_MODE" == "true" ]]; then
        curl_opts+=("-k")
    fi
    echo -n "等待 $name 就绪"
    while [[ $attempt -lt $max_attempts ]]; do
        if [[ "$name" == "PrivShield" ]] && ! kill -0 "$AGENT_PID" 2>/dev/null; then
            echo " 失败（Agent 进程已退出）"
            echo "最近日志："
            tail -n 40 "$LOGS_DIR/agent_all.log" >&2 || true
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
    if [[ "$name" == "PrivShield" ]]; then
        echo "最近日志："
        tail -n 40 "$LOGS_DIR/agent_all.log" >&2 || true
    fi
    return 1
}

wait_for_service "$AGENT_URL/health" "PrivShield"

echo -n "等待 agent gRPC ($AGENT_GRPC_ADDR) 就绪"
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

# 7. 启动 Go BFF 代理网关
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
        # 避免 BFF 继承全局 AGENT_TLS_ENABLED=true 后错误地按 TLS 连接 Agent。
        export PRIVACY_AGENT_TLS_ENABLED=false
    fi
    export CONSOLE_RATE_LIMIT="${CONSOLE_RATE_LIMIT:-0}"
    exec ./bin/backend-go
) > "$LOGS_DIR/backend-go-all.log" 2>&1 &
GO_CONSOLE_PID=$!
PIDS+=("$GO_CONSOLE_PID")
write_pid "$GO_CONSOLE_PID_FILE" "$GO_CONSOLE_PID"

wait_for_service "$CONSOLE_URL/health" "Go gRPC 代理后端"

# 8. 启动 Vite 前端热开发服务器
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
    echo "🎉 PrivShield 全量服务 (Agent + Go BFF + Vite UI) [mTLS 双向认证] 已就绪！"
else
    echo "🎉 PrivShield 全量服务 (Agent + Go BFF + Vite UI) 已全部启动！"
fi
echo "  🌐 前端界面 (UI):    $VITE_URL"
echo "  🔌 代理后端 (Go):    $CONSOLE_URL"
echo "  🛡️  隐私引擎 (Agent): $AGENT_URL (REST) / $AGENT_GRPC_ADDR (gRPC)"
if [[ "$MTLS_MODE" == "true" ]]; then
    echo "  🔒 安全模式 (mTLS):  已启用 (CA: $CERT_DIR/ca.crt)"
fi
echo "  🛑 停止所有服务: 按 Ctrl+C 或运行 ./scripts/dev/dev-stop.sh"
echo "================================================================="

wait
