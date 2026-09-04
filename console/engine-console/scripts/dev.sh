#!/usr/bin/env bash
# dev.sh — 一键并发启动 engine-console (BFF :8081 + Vite 前端 :5173)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONSOLE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_ROOT="$(cd "$CONSOLE_DIR/../.." && pwd)"

export CONSOLE_HOST="${CONSOLE_HOST:-127.0.0.1}"
export CONSOLE_PORT="${CONSOLE_PORT:-8081}"
export BFF_AGENT_URL="${BFF_AGENT_URL:-http://127.0.0.1:8079}"

cleanup() {
    echo "Stopping engine-console components..."
    kill 0 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "Starting engine-console BFF on :$CONSOLE_PORT..."
cd "$REPO_ROOT"
go run ./console/engine-console/bff-go/cmd/server &
BFF_PID=$!

echo "Starting engine-console Web on :5173..."
cd "$CONSOLE_DIR/web"
if [ ! -d "node_modules" ]; then
    echo "Installing frontend dependencies..."
    npm install
fi
npm run dev &
WEB_PID=$!

wait $BFF_PID $WEB_PID
