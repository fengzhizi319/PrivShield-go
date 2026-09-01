# ============================================================================
# Go gRPC 代理后端构建自动化脚本
# Go gRPC proxy backend build automation script
#
# 用法 / Usage:
#   make proto    生成 gRPC 桩代码 / Generate gRPC stubs
#   make build    编译服务端二进制 / Compile server binary
#   make run      编译并运行（开发模式）/ Compile and run (dev mode)
#   make test     运行全部测试 / Run all tests
#   make lint     格式化与静态检查 / Format and static analysis
#   make clean    清理构建产物 / Clean build artifacts
# ============================================================================

# 声明伪目标，避免与同名文件冲突
# Declare phony targets to avoid conflicts with files of the same name
.PHONY: proto build run test lint clean

# Go 模块路径（与 go.mod 中的 module 一致）
# Go module path (consistent with module in go.mod)
MODULE := github.com/fengzhizi319/PrivShield-go/console/bff-go

# protoc 编译器路径（使用 /tmp 下的预装版本，避免污染系统环境）
# protoc compiler path (uses pre-installed version in /tmp to avoid polluting system env)
PROTOC := /tmp/protoc-install/bin/protoc

# proto - 从仓库根目录的共享 proto 文件生成 Go gRPC 桩代码
# Generate Go gRPC stubs from the shared proto file at repository root
#
# 逻辑 / Logic:
#   1. 创建本地 proto/ 输出目录 / Create local proto/ output directory
#   2. 以 ../../proto 为导入根，编译 privacy.proto
#      Use ../../proto as import root, compile privacy.proto
#   3. 生成 *.pb.go（消息类型）和 *_grpc.pb.go（服务桩）
#      Generate *.pb.go (message types) and *_grpc.pb.go (service stubs)
#   4. paths=source_relative 使输出路径与 proto 包路径一致
#      paths=source_relative keeps output path consistent with proto package path
proto:
	mkdir -p proto  # 确保输出目录存在 / ensure output directory exists
	$(PROTOC) -I ../../proto \
		--go_out=proto \
		--go_opt=paths=source_relative \
		--go-grpc_out=proto \
		--go-grpc_opt=paths=source_relative \
		../../proto/privacy.proto

# build - 下载依赖并编译服务端二进制到 bin/ 目录
# Download dependencies and build the server binary into bin/
#
# 逻辑 / Logic:
#   1. go mod tidy 同步 go.mod/go.sum（添加缺失、移除多余依赖）
#      go mod tidy syncs go.mod/go.sum (adds missing, removes unused deps)
#   2. go build 编译 cmd/server 入口为静态二进制
#      go build compiles cmd/server entry into a static binary
build:
	go mod tidy  # 同步依赖 / sync dependencies
	go build -o bin/backend-go ./cmd/server  # 编译二进制 / compile binary

# run - 开发模式：编译后立即运行服务端
# Run the server in development mode: compile then immediately start
#
# 逻辑 / Logic:
#   1. 同步依赖 / Sync deps
#   2. 编译二进制 / Compile binary
#   3. 前台运行（Ctrl+C 停止）/ Run in foreground (Ctrl+C to stop)
run:
	go mod tidy  # 同步依赖 / sync dependencies
	go build -o bin/backend-go ./cmd/server  # 编译 / compile
	./bin/backend-go  # 前台运行 / run in foreground

# dev - run 的别名（兼容旧习惯）
# Legacy alias for run (backward compatibility)
dev: run

# test - 运行全部测试（单元测试 + 集成测试）
# Run all tests (unit + integration)
#
# 注意：集成测试需要 PrivShield Agent (engine) 在 127.0.0.1:50051 运行
# Note: integration tests require PrivShield Agent (engine) running on 127.0.0.1:50051
test:
	go test ./...  # 递归测试所有包 / recursively test all packages

# test-unit - 仅运行单元测试（跳过带 -short 标记排除的集成测试）
# Run only unit tests (skip integration tests excluded by -short flag)
test-unit:
	go test -short ./...  # -short 标志跳过耗时测试 / -short flag skips long-running tests

# test-integration - 仅运行集成测试（匹配名称含 Integration 的用例）
# Run only integration tests (match test cases with Integration in name)
test-integration:
	go test -run Integration ./tests/...  # 按名称过滤 / filter by name

# lint - 代码格式化与静态分析
# Format code and run static analysis
#
# 逻辑 / Logic:
#   1. go fmt 统一代码格式（等价于 gofmt -w）
#      go fmt standardizes code formatting (equivalent to gofmt -w)
#   2. go vet 检测常见编码错误（如 printf 参数不匹配）
#      go vet detects common coding errors (e.g. printf arg mismatch)
lint:
	go fmt ./...  # 格式化 / format
	go vet ./...  # 静态检查 / static analysis

# clean - 清理构建产物和生成的 protobuf 代码
# Clean build artifacts and generated protobuf code
#
# 注意：清理后需重新 make proto && make build 才能运行
# Note: after clean, run make proto && make build before running again
clean:
	rm -rf bin  # 删除编译产物目录 / remove compiled binary directory
	rm -f proto/*.pb.go  # 删除生成的 protobuf Go 代码 / remove generated protobuf Go code
