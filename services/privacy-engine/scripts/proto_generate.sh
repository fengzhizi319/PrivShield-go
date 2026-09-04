#!/bin/bash
# 生成 gRPC Go 桩代码
# 前置条件: go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#           go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

PROTO_DIR="$PROJECT_ROOT/proto"
OUT_DIR="$PROJECT_ROOT/engine-go/internal/grpcserver/proto"

echo "=== Generating gRPC Go stubs ==="
echo "Proto dir: $PROTO_DIR"
echo "Output:    $OUT_DIR"

mkdir -p "$OUT_DIR"

# 检查 protoc
if ! command -v protoc &>/dev/null; then
    echo "ERROR: protoc not found. Install: brew install protobuf"
    exit 1
fi

# 检查 protoc-gen-go
if ! command -v protoc-gen-go &>/dev/null; then
    echo "Installing protoc-gen-go..."
    go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
fi

if ! command -v protoc-gen-go-grpc &>/dev/null; then
    echo "Installing protoc-gen-go-grpc..."
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
fi

# 生成
protoc \
    --proto_path="$PROTO_DIR" \
    --go_out="$OUT_DIR" \
    --go_opt=paths=source_relative \
    --go-grpc_out="$OUT_DIR" \
    --go-grpc_opt=paths=source_relative \
    "$PROTO_DIR/privacy.proto"

echo "=== gRPC stubs generated successfully ==="
ls -la "$OUT_DIR/"
