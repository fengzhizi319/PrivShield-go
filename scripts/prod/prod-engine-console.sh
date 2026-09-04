#!/usr/bin/env bash
# ============================================================================
# 【生产预览/独立交付模式】一键启动 PrivShield Agent + Go BFF (静态页面托管)
# Launch PrivShield Agent + Go BFF with static frontend hosting in PROD mode
#
# 用法 / Usage:
#   ./scripts/prod/prod-engine-console.sh [--rebuild] [--force] [--mtls]
#
# 参数说明 / Options:
#   --rebuild: 强制重新编译打包前端与 Go BFF
#   --force:   非交互模式，端口被占用时自动终止占用进程
#   --mtls:    启用 mTLS 双向认证
#   -h, --help: 查看帮助说明
# ============================================================================

set -euo pipefail
export NO_PROXY="*"
export no_proxy="*"

REBUILD=false
FORCE=false
MTLS_MODE=false

for arg in "$@"; do
    case "$arg" in
        --rebuild) REBUILD=true ;;
        --force)   FORCE=true ;;
        --mtls)    MTLS_MODE=true ;;
        -h|--help)
            echo "用法: $0 [选项]"
            echo "  --rebuild 强制重新编译前端与 Go BFF"
            echo "  --force   端口被占用时自动终止占用进程（非交互模式）"
            echo "  --mtls    以 mTLS 双向认证模式启动"
            echo "  -h, --help 显示此帮助信息"
            exit 0
            ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
CONSOLE_DIR="$PROJECT_ROOT/console/engine-console"
PIDS_DIR="$PROJECT_ROOT/.pids"
LOGS_DIR="$PROJECT_ROOT/.logs"

mkdir -p "$PIDS_DIR" "$LOGS_DIR"

AGENT_URL="http://127.0.0.1:8079"
if [[ "$MTLS_MODE" == "true" ]]; then
    AGENT_URL="https://127.0.0.1:8079"
fi
AGENT_GRPC_ADDR="127.0.0.1:50051"
CONSOLE_URL="http://127.0.0.1:8081"
CERT_DIR="$CONSOLE_DIR/bff-go/certs"
GEN_CERTS="$CONSOLE_DIR/bff-go/scripts/gen-certs.sh"

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

    if ! _is_port_in_use "$port"; then
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
        echo "错误：端口 $port 被占用且当前为非交互环境（无 TTY）。请使用 --force 自动处理。"
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

# 1. Go 工具链检查
if ! command -v go >/dev/null 2>&1; then
    echo "错误：未找到 Go 工具链，请先安装 Go。"
    exit 1
fi

# 3. mTLS 模式下自动确保测试证书存在
if [[ "$MTLS_MODE" == "true" ]]; then
    if [[ ! -f "$CERT_DIR/ca.crt" || ! -f "$CERT_DIR/server.crt" || ! -f "$CERT_DIR/client.crt" ]]; then
        echo "未检测到完整证书，正在自动生成自签名证书..."
        bash "$GEN_CERTS" "$CERT_DIR"
    fi
fi

# 4. 前端打包
if [[ "$REBUILD" == "true" || ! -d "$CONSOLE_DIR/web/dist" ]]; then
    echo "构建前端生产包..."
    (
        cd "$CONSOLE_DIR/web"
        if [[ ! -d "node_modules" ]]; then
            if command -v corepack >/dev/null 2>&1; then
                corepack pnpm install
            elif command -v pnpm >/dev/null 2>&1; then
                pnpm install
            else
                npm install
            fi
        fi
        if command -v corepack >/dev/null 2>&1; then
            corepack pnpm build
        elif command -v pnpm >/dev/null 2>&1; then
            pnpm build
        else
            npm run build
        fi
    )
fi

# 5. 编译 Go BFF 二进制
echo "编译 Go BFF 服务端..."
(cd "$CONSOLE_DIR/bff-go" && go build -o bin/backend-go ./cmd/server)

AGENT_PID_FILE="$PIDS_DIR/agent.pid"
GO_CONSOLE_PID_FILE="$PIDS_DIR/console-go.pid"

write_pid() {
    echo "$2" > "$1"
}

PIDS=()
STOPPING=false
cleanup() {
    STOPPING=true
    echo ""
    echo "正在停止【生产模式】控制台所有服务..."
    for pid in "${PIDS[@]}"; do
        kill "$pid" 2>/dev/null || true
    done
    wait 2>/dev/null || true
    rm -f "$AGENT_PID_FILE" "$GO_CONSOLE_PID_FILE"
    echo "已停止。"
}
trap cleanup INT TERM EXIT

check_port_available 8079 "PrivShield REST"
check_port_available 50051 "PrivShield gRPC"
check_port_available 8081 "Go BFF 服务"

# 6. 启动 Agent 核心引擎
launch_agent() {
    local agent_log="$LOGS_DIR/agent.log"
    echo "启动 PrivShield (REST: $AGENT_URL, gRPC: $AGENT_GRPC_ADDR)，日志: $agent_log..."
    (
        cd "$PROJECT_ROOT"
        if [[ "$MTLS_MODE" == "true" ]]; then
            export AGENT_TLS_ENABLED=true
            export AGENT_TLS_CERT_FILE="$CERT_DIR/server.crt"
            export AGENT_TLS_KEY_FILE="$CERT_DIR/server.key"
            export AGENT_TLS_CA_FILE="$CERT_DIR/ca.crt"
            export AGENT_AUTH_INTERNAL_MTLS_ENABLED=true
            export AGENT_AUTH_MTLS_ALLOWED_CNS='["privshield-client"]'
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
    local curl_opts=("-s" "-o" "/dev/null" "-w" "%{http_code}")
    if [[ "$MTLS_MODE" == "true" ]]; then
        curl_opts+=("-k")
    fi
    echo -n "等待 $name 就绪"
    while [[ $attempt -lt $max_attempts ]]; do
        if curl "${curl_opts[@]}" "$url" | grep -q '^200$'; then
            echo " OK"
            return 0
        fi
        echo -n "."
        sleep 1
        attempt=$((attempt + 1))
    done
    echo " 超时"
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

# 7. 启动 Go BFF（托管静态资源）
echo "启动 Go BFF (Web UI & API: $CONSOLE_URL)..."
(
    cd "$CONSOLE_DIR/bff-go"
    export PRIVACY_CONSOLE_STATIC_DIR="../web/dist"
    if [[ "$MTLS_MODE" == "true" ]]; then
        export PRIVACY_AGENT_TLS_ENABLED=true
        export PRIVACY_AGENT_TLS_CA_FILE="$CERT_DIR/ca.crt"
        export PRIVACY_AGENT_TLS_CERT_FILE="$CERT_DIR/client.crt"
        export PRIVACY_AGENT_TLS_KEY_FILE="$CERT_DIR/client.key"
        export PRIVACY_AGENT_TLS_SERVER_NAME="localhost"
    fi
    exec ./bin/backend-go
) > "$LOGS_DIR/console-go.log" 2>&1 &
GO_CONSOLE_PID=$!
PIDS+=("$GO_CONSOLE_PID")
write_pid "$GO_CONSOLE_PID_FILE" "$GO_CONSOLE_PID"

wait_for_service "$CONSOLE_URL/health" "Go BFF"

echo ""
echo "================================================================="
if [[ "$MTLS_MODE" == "true" ]]; then
    echo "🎉 PrivShield 生产控制台服务 (Agent + Go BFF) [mTLS 双向认证] 已就绪！"
else
    echo "🎉 PrivShield 生产控制台服务 (Agent + Go BFF) 已全部启动！"
fi
echo "  🌐 Web 控制台地址:   $CONSOLE_URL"
echo "  🛡️  隐私引擎 (Agent): $AGENT_URL (REST) / $AGENT_GRPC_ADDR (gRPC)"
if [[ "$MTLS_MODE" == "true" ]]; then
    echo "  🔒 安全模式 (mTLS):  已启用 (CA: $CERT_DIR/ca.crt)"
fi
echo "  🛑 停止所有服务: 按 Ctrl+C 或运行 ./scripts/prod/prod-stop.sh"
echo "================================================================="

wait
