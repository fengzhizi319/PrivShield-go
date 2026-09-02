#!/usr/bin/env bash
# ============================================================================
# 【开发模式】一键启动 PrivShield 调度之眼控制台 (全部 4 上游 + App-LZ BFF + Vite HMR)
# Launch PrivShield App-LZ Console in DEV mode
#   (Engine :8079 + Hub :8082 + Datasource :8083 + Audit :8084 + BFF :8085 + Web :5174)
#
# 用法 / Usage:
#   ./scripts/dev/dev-app-lz.sh [--force] [--skip-upstream]
#
# 选项:
#   --force           端口被占用时自动终止占用进程（非交互模式）
#   --skip-upstream   跳过上游服务启动（假设 4 个微服务已在运行）
# ============================================================================

set -euo pipefail
export CGO_ENABLED=0

FORCE=false
SKIP_UPSTREAM=false
MTLS_MODE=false

for arg in "$@"; do
    case "$arg" in
        --force) FORCE=true ;;
        --skip-upstream) SKIP_UPSTREAM=true ;;
        --mtls)  MTLS_MODE=true ;;
        -h|--help)
            echo "用法: $0 [选项]"
            echo "  --force           端口被占用时自动终止占用进程（非交互模式）"
            echo "  --mtls            启用 mTLS 双向认证（自动配置证书与双向鉴权环境）"
            echo "  --skip-upstream   跳过上游服务启动（假设 4 个微服务已在运行）"
            echo "  -h, --help        显示此帮助信息"
            exit 0
            ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
PROJECT_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel 2>/dev/null || (cd "$SCRIPT_DIR/../.." && pwd -P))"
APP_LZ_DIR="$PROJECT_ROOT/console/app-lz"
PIDS_DIR="$PROJECT_ROOT/.pids"
LOGS_DIR="$PROJECT_ROOT/.logs"
DATA_DIR="$PROJECT_ROOT/data"
CERT_DIR="$PROJECT_ROOT/console/bff-go/certs"
GEN_CERTS="$PROJECT_ROOT/console/bff-go/scripts/gen-certs.sh"
GO_BIN="${GO_BIN:-go}"

# mTLS 模式下自动确保测试证书存在
if [[ "$MTLS_MODE" == "true" ]]; then
    if [[ ! -f "$CERT_DIR/ca.crt" || ! -f "$CERT_DIR/server.crt" || ! -f "$CERT_DIR/client.crt" ]]; then
        echo "未检测到完整证书，正在自动生成开发用自签名证书..."
        bash "$GEN_CERTS" "$CERT_DIR"
    fi
fi

# Python 解释器自动探测
if [ -x "${PROJECT_ROOT}/.venv/bin/python" ]; then
    PYTHON="${PYTHON:-${PROJECT_ROOT}/.venv/bin/python}"
else
    PYTHON="${PYTHON:-python3}"
fi

mkdir -p "$PIDS_DIR" "$LOGS_DIR" "$DATA_DIR"

BFF_PORT=8085
VITE_PORT=5174
ENGINE_HEALTH_URL="http://127.0.0.1:8079/health"
if [[ "$MTLS_MODE" == "true" ]]; then
    ENGINE_HEALTH_URL="https://127.0.0.1:8079/health"
fi

_is_port_in_use() {
    local port="$1"
    python3 -c "
import socket, sys
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.settimeout(0.5)
res = s.connect_ex(('127.0.0.1', int('$port')))
s.close()
sys.exit(0 if res == 0 else 1)
" 2>/dev/null
}

_kill_port() {
    local port="$1"
    if _is_port_in_use "$port"; then
        echo "终止端口 $port 上的占用进程..."
        if command -v lsof >/dev/null 2>&1; then
            lsof -ti ":$port" | xargs kill -9 2>/dev/null || true
        elif command -v fuser >/dev/null 2>&1; then
            fuser -k -9 "${port}/tcp" 2>/dev/null || true
        fi
        sleep 0.5
    fi
}

check_and_free_port() {
    local port="$1"
    local desc="$2"
    if _is_port_in_use "$port"; then
        if [[ "$FORCE" == "true" ]]; then
            echo "⚠️  端口 $port ($desc) 被占用，--force 模式下自动清理..."
            _kill_port "$port"
        else
            echo "⚠️  端口 $port ($desc) 已被占用！"
            echo "使用 --force 参数自动清理，或手动释放端口后重试。"
            exit 1
        fi
    fi
}

check_and_free_port "$BFF_PORT" "App-LZ Go BFF"
check_and_free_port "$VITE_PORT" "App-LZ Vite Web"

# ── 上游服务自动启动 ─────────────────────────────────────────────────
# 检查服务是否可达，若不可达则自动启动
_wait_for_http() {
    local name="$1" url="$2" max_wait="${3:-15}"
    local curl_opts=("--noproxy" "*" "--connect-timeout" "1" "--max-time" "3" "-sf" "-o" "/dev/null")
    if [[ "$MTLS_MODE" == "true" ]]; then
        curl_opts+=("-k")
    fi
    local i=0
    while [ $i -lt "$max_wait" ]; do
        if curl "${curl_opts[@]}" "$url" 2>/dev/null; then return 0; fi
        sleep 1; i=$((i + 1))
    done
    echo "⚠️  $name 在 ${max_wait}s 内未就绪 ($url)"
    return 1
}

_start_upstream_if_needed() {
    local name="$1" port="$2" health_url="$3"
    # --force 模式下强制清理端口并重新启动最新版本
    if [[ "$FORCE" == "true" ]]; then
        if _is_port_in_use "$port"; then
            echo "⚠️  端口 $port ($name) 被占用，--force 模式下自动重启..."
            _kill_port "$port"
        fi
        return
    fi
    # 非 force 模式下：若已健康可达则跳过
    local curl_opts=("--noproxy" "*" "-sf" "-o" "/dev/null")
    if [[ "$MTLS_MODE" == "true" ]]; then
        curl_opts+=("-k")
    fi
    if curl "${curl_opts[@]}" "$health_url" 2>/dev/null; then
        echo "✅ $name 已在运行 (port $port)"
        return
    fi
    if _is_port_in_use "$port"; then
        echo "❌ 端口 $port ($name) 被占用，使用 --force 自动清理并重启"
        return
    fi
}

start_engine() {
    local port=8079 pid_file="$PIDS_DIR/agent.pid"
    _start_upstream_if_needed "Go Engine" "$port" "$ENGINE_HEALTH_URL"
    local curl_opts=("--noproxy" "*" "-sf" "-o" "/dev/null")
    [[ "$MTLS_MODE" == "true" ]] && curl_opts+=("-k")
    curl "${curl_opts[@]}" "$ENGINE_HEALTH_URL" 2>/dev/null && return

    echo "🔄 启动 PrivShield Go Engine (REST :$port / gRPC :50051)..."
    cd "$PROJECT_ROOT"
    if [[ "$MTLS_MODE" == "true" ]]; then
        PRIVACY_REST_HOST=127.0.0.1 PRIVACY_REST_PORT="$port" \
        PRIVACY_GRPC_HOST=127.0.0.1 PRIVACY_GRPC_PORT=50051 \
        PRIVACY_TLS_ENABLED=true \
        PRIVACY_TLS_CERT_FILE="$CERT_DIR/server.crt" \
        PRIVACY_TLS_KEY_FILE="$CERT_DIR/server.key" \
        PRIVACY_TLS_CA_FILE="$CERT_DIR/ca.crt" \
        PRIVACY_AUTH_INTERNAL_MTLS_ENABLED=true \
        PRIVACY_AUTH_MTLS_WHITELIST_FILE="$PROJECT_ROOT/config/mtls-whitelist.yaml" \
        "$GO_BIN" run ./engine-go/cmd/privshield-agent \
            > "${LOGS_DIR}/agent_app_lz.log" 2>&1 &
    else
        PRIVACY_REST_HOST=127.0.0.1 PRIVACY_REST_PORT="$port" \
        PRIVACY_GRPC_HOST=127.0.0.1 PRIVACY_GRPC_PORT=50051 \
        PRIVACY_TLS_ENABLED=false \
        PRIVACY_AUTH_INTERNAL_MTLS_ENABLED=false \
        "$GO_BIN" run ./engine-go/cmd/privshield-agent \
            > "${LOGS_DIR}/agent_app_lz.log" 2>&1 &
    fi
    echo $! > "$pid_file"
    _wait_for_http "Go Engine" "$ENGINE_HEALTH_URL" 15 && \
        echo "✅ Go Engine 已就绪 (PID $(cat "$pid_file"))" || \
        echo "⚠️  Go Engine 启动超时，请检查 ${LOGS_DIR}/agent_app_lz.log"
}

start_service_hub() {
    local port=8082 pid_file="$PIDS_DIR/service-hub.pid"

    echo "🔨 编译 Service Hub..."
    cd "${PROJECT_ROOT}/services/service-hub"
    "$GO_BIN" build -o bin/service-hub ./cmd/server

    # 总是重启以确保加载最新编译的二进制
    if _is_port_in_use "$port"; then
        echo "🔄 停止旧 Service Hub 进程..."
        _kill_port "$port"
    fi

    echo "🔄 启动 Service Hub (:$port)..."
    if [[ "$MTLS_MODE" == "true" ]]; then
        SERVICE_HUB_HOST=127.0.0.1 SERVICE_HUB_PORT="$port" \
        SERVICE_HUB_AGENT_REST_HOST=127.0.0.1 SERVICE_HUB_AGENT_REST_PORT=8079 \
        SERVICE_HUB_DB_PATH="${DATA_DIR}/service-hub.db" \
        PRIVACY_AGENT_TLS_ENABLED=true \
        PRIVACY_AGENT_TLS_CA_FILE="$CERT_DIR/ca.crt" \
        PRIVACY_AGENT_TLS_CERT_FILE="$CERT_DIR/client.crt" \
        PRIVACY_AGENT_TLS_KEY_FILE="$CERT_DIR/client.key" \
        PRIVACY_AGENT_TLS_SERVER_NAME="localhost" \
        ./bin/service-hub > "${LOGS_DIR}/service-hub_app_lz.log" 2>&1 &
    else
        SERVICE_HUB_HOST=127.0.0.1 SERVICE_HUB_PORT="$port" \
        SERVICE_HUB_AGENT_REST_HOST=127.0.0.1 SERVICE_HUB_AGENT_REST_PORT=8079 \
        SERVICE_HUB_DB_PATH="${DATA_DIR}/service-hub.db" \
        ./bin/service-hub > "${LOGS_DIR}/service-hub_app_lz.log" 2>&1 &
    fi
    echo $! > "$pid_file"
    _wait_for_http "Service Hub" "http://127.0.0.1:$port/health" 10 && \
        echo "✅ Service Hub 已就绪 (PID $(cat "$pid_file"))" || \
        echo "⚠️  Service Hub 启动超时"
}

start_datasource_mgr() {
    local port=8083 pid_file="$PIDS_DIR/datasource-mgr.pid"

    echo "🔨 编译 Datasource Mgr..."
    cd "${PROJECT_ROOT}/services/datasource-mgr"
    "$GO_BIN" build -o bin/datasource-mgr ./cmd/server

    # 总是重启以确保加载最新编译的二进制
    if _is_port_in_use "$port"; then
        echo "🔄 停止旧 Datasource Mgr 进程..."
        _kill_port "$port"
    fi

    echo "🔄 启动 Datasource Mgr (:$port)..."
    if [[ "$MTLS_MODE" == "true" ]]; then
        DATASOURCE_MGR_HOST=127.0.0.1 DATASOURCE_MGR_PORT="$port" \
        DATASOURCE_MGR_AGENT_REST_HOST=127.0.0.1 DATASOURCE_MGR_AGENT_REST_PORT=8079 \
        PRIVACY_AGENT_TLS_ENABLED=true \
        PRIVACY_AGENT_TLS_CA_FILE="$CERT_DIR/ca.crt" \
        PRIVACY_AGENT_TLS_CERT_FILE="$CERT_DIR/client.crt" \
        PRIVACY_AGENT_TLS_KEY_FILE="$CERT_DIR/client.key" \
        PRIVACY_AGENT_TLS_SERVER_NAME="localhost" \
        ./bin/datasource-mgr > "${LOGS_DIR}/datasource-mgr_app_lz.log" 2>&1 &
    else
        DATASOURCE_MGR_HOST=127.0.0.1 DATASOURCE_MGR_PORT="$port" \
        DATASOURCE_MGR_AGENT_REST_HOST=127.0.0.1 DATASOURCE_MGR_AGENT_REST_PORT=8079 \
        ./bin/datasource-mgr > "${LOGS_DIR}/datasource-mgr_app_lz.log" 2>&1 &
    fi
    echo $! > "$pid_file"
    _wait_for_http "Datasource Mgr" "http://127.0.0.1:$port/health" 10 && \
        echo "✅ Datasource Mgr 已就绪 (PID $(cat "$pid_file"))" || \
        echo "⚠️  Datasource Mgr 启动超时"
}

start_audit_log() {
    local port=8084 pid_file="$PIDS_DIR/audit-log.pid"

    echo "🔨 编译 Audit Log..."
    cd "${PROJECT_ROOT}/services/audit-log"
    "$GO_BIN" build -o bin/audit-log ./cmd/server

    # 总是重启以确保加载最新编译的二进制
    if _is_port_in_use "$port"; then
        echo "🔄 停止旧 Audit Log 进程..."
        _kill_port "$port"
    fi

    echo "🔄 启动 Audit Log (:$port)..."
    if [[ "$MTLS_MODE" == "true" ]]; then
        AUDIT_LOG_HOST=127.0.0.1 AUDIT_LOG_PORT="$port" \
        AUDIT_LOG_AGENT_REST_HOST=127.0.0.1 AUDIT_LOG_AGENT_REST_PORT=8079 \
        AUDIT_LOG_DB_PATH="${DATA_DIR}/audit-log.db" \
        PRIVACY_AGENT_TLS_ENABLED=true \
        PRIVACY_AGENT_TLS_CA_FILE="$CERT_DIR/ca.crt" \
        PRIVACY_AGENT_TLS_CERT_FILE="$CERT_DIR/client.crt" \
        PRIVACY_AGENT_TLS_KEY_FILE="$CERT_DIR/client.key" \
        PRIVACY_AGENT_TLS_SERVER_NAME="localhost" \
        ./bin/audit-log > "${LOGS_DIR}/audit-log_app_lz.log" 2>&1 &
    else
        AUDIT_LOG_HOST=127.0.0.1 AUDIT_LOG_PORT="$port" \
        AUDIT_LOG_AGENT_REST_HOST=127.0.0.1 AUDIT_LOG_AGENT_REST_PORT=8079 \
        AUDIT_LOG_DB_PATH="${DATA_DIR}/audit-log.db" \
        ./bin/audit-log > "${LOGS_DIR}/audit-log_app_lz.log" 2>&1 &
    fi
    echo $! > "$pid_file"
    _wait_for_http "Audit Log" "http://127.0.0.1:$port/health" 10 && \
        echo "✅ Audit Log 已就绪 (PID $(cat "$pid_file"))" || \
        echo "⚠️  Audit Log 启动超时"
}

# ── 0. 启动上游服务（Engine + 3 Go 微服务）──────────────────────────
if [[ "$SKIP_UPSTREAM" != "true" ]]; then
    echo ""
    echo "── 检查并启动上游微服务 ──"
    start_engine
    start_service_hub
    start_datasource_mgr
    start_audit_log
    echo ""
else
    echo "⏭️  跳过上游服务启动 (--skip-upstream)"
fi

echo "=================================================================="
echo " 🚀 启动 PrivShield App-LZ 调度全景控制台 [开发模式 (HMR)]"
echo "=================================================================="
echo "  Engine 引擎:    $ENGINE_HEALTH_URL (REST) / 127.0.0.1:50051 (gRPC)"
echo "  调度中枢 (Hub): http://127.0.0.1:8082"
echo "  数据源管理:     http://127.0.0.1:8083"
echo "  审计日志:       http://127.0.0.1:8084"
echo "  BFF 后端:       http://127.0.0.1:$BFF_PORT"
echo "  Web 前端:       http://localhost:$VITE_PORT"
if [[ "$MTLS_MODE" == "true" ]]; then
    echo "  mTLS 安全模式:  已开启 (CA: $CERT_DIR/ca.crt)"
fi
echo "=================================================================="

# 1. 编译并启动 Go BFF
echo "编译 App-LZ Go BFF..."
(cd "$APP_LZ_DIR/bff-go" && go build -o bin/server ./cmd/server)

echo "启动 App-LZ Go BFF..."
if [[ "$MTLS_MODE" == "true" ]]; then
    APP_LZ_HOST=127.0.0.1 APP_LZ_PORT="$BFF_PORT" \
    APP_LZ_AGENT_URL="https://127.0.0.1:8079" \
    APP_LZ_AGENT_GRPC="127.0.0.1:50051" \
    PRIVACY_AGENT_TLS_ENABLED=true \
    PRIVACY_AGENT_TLS_CA_FILE="$CERT_DIR/ca.crt" \
    PRIVACY_AGENT_TLS_CERT_FILE="$CERT_DIR/client.crt" \
    PRIVACY_AGENT_TLS_KEY_FILE="$CERT_DIR/client.key" \
    PRIVACY_AGENT_TLS_SERVER_NAME="localhost" \
    "$APP_LZ_DIR/bff-go/bin/server" > "$LOGS_DIR/app-lz-bff.log" 2>&1 &
else
    APP_LZ_HOST=127.0.0.1 APP_LZ_PORT="$BFF_PORT" "$APP_LZ_DIR/bff-go/bin/server" > "$LOGS_DIR/app-lz-bff.log" 2>&1 &
fi
BFF_PID=$!
echo "$BFF_PID" > "$PIDS_DIR/app-lz-bff.pid"

# 等待 BFF 就绪
for i in {1..30}; do
    if curl -s "http://127.0.0.1:$BFF_PORT/api/health" >/dev/null 2>&1; then
        echo "✅ App-LZ Go BFF 已就绪 (PID: $BFF_PID)"
        break
    fi
    sleep 0.2
done

# 2. 启动 Vite 前端开发服务器
echo "启动 App-LZ Vite 前端开发服务器 (HMR: :$VITE_PORT)..."
if [ ! -d "$APP_LZ_DIR/web/node_modules" ]; then
    echo "📦 正在安装 App-LZ 前端依赖..."
    (cd "$APP_LZ_DIR/web" && pnpm install)
fi
(cd "$APP_LZ_DIR/web" && npx vite --port "$VITE_PORT" --host 0.0.0.0) > "$LOGS_DIR/app-lz-web.log" 2>&1 &
WEB_PID=$!
echo "$WEB_PID" > "$PIDS_DIR/app-lz-web.pid"

# 等待 Web 前端就绪
for i in {1..30}; do
    if curl -s "http://127.0.0.1:$VITE_PORT" >/dev/null 2>&1; then
        echo "✅ App-LZ Web 前端已就绪 (PID: $WEB_PID)"
        break
    fi
    sleep 0.2
done

cleanup() {
    echo ""
    echo "正在停止 App-LZ 控制台服务..."
    kill "$BFF_PID" 2>/dev/null || true
    kill "$WEB_PID" 2>/dev/null || true
    rm -f "$PIDS_DIR/app-lz-bff.pid" "$PIDS_DIR/app-lz-web.pid"
    # 停止上游服务（仅停止本脚本启动的）
    for svc in agent service-hub datasource-mgr audit-log; do
        local pf="$PIDS_DIR/${svc}.pid"
        if [ -f "$pf" ]; then
            kill "$(cat "$pf")" 2>/dev/null || true
            rm -f "$pf"
        fi
    done
    echo "已停止。"
    exit 0
}

trap cleanup INT TERM

echo "=================================================================="
echo " ✨ App-LZ 控制台已启动完成！"
echo " 🌐 前端访问地址: http://localhost:$VITE_PORT"
echo " 🔌 BFF 接口地址: http://127.0.0.1:$BFF_PORT/api/lz/topology"
echo " 按 Ctrl+C 停止服务"
echo "=================================================================="

wait "$BFF_PID" "$WEB_PID"
