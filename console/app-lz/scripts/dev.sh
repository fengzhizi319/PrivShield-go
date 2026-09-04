#!/usr/bin/env bash
# dev.sh — 一键并发启动 app-lz 调度之眼 (BFF :8085 + Vite 前端 :5174)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONSOLE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_ROOT="$(cd "$CONSOLE_DIR/../.." && pwd)"

export APP_LZ_HOST="${APP_LZ_HOST:-127.0.0.1}"
export APP_LZ_PORT="${APP_LZ_PORT:-8085}"
export APP_LZ_SERVICE_HUB_URL="${APP_LZ_SERVICE_HUB_URL:-http://127.0.0.1:8082}"

cleanup() {
    echo "Stopping app-lz components..."
    kill 0 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "Starting app-lz BFF on :$APP_LZ_PORT..."
cd "$REPO_ROOT"
go run ./console/app-lz/bff-go/cmd/server &
BFF_PID=$!

echo "Starting app-lz Web on :5174..."
cd "$CONSOLE_DIR/web"
if [ ! -d "node_modules" ]; then
    echo "Installing frontend dependencies..."
    npm install
fi
npm run dev &
WEB_PID=$!

wait $BFF_PID $WEB_PID
