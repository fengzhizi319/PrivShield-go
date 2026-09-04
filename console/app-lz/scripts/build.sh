#!/usr/bin/env bash
# build.sh — 独立构建 app-lz (BFF 二进制 + Web 静态资源)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONSOLE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_ROOT="$(cd "$CONSOLE_DIR/../.." && pwd)"

echo "Building app-lz BFF binary..."
cd "$REPO_ROOT"
mkdir -p "$CONSOLE_DIR/bff-go/bin"
CGO_ENABLED=0 go build -ldflags="-s -w" -o "$CONSOLE_DIR/bff-go/bin/server" ./console/app-lz/bff-go/cmd/server

echo "Building app-lz Web frontend..."
cd "$CONSOLE_DIR/web"
if [ ! -d "node_modules" ]; then
    npm install
fi
npm run build
echo "Build complete."
