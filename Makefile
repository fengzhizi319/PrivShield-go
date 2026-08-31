# PrivShield 常用命令
#
# 这个 Makefile 的目标是把“开发、测试、打包、部署、文档”入口统一到一处，
# 全栈 100% 统一为 Go 1.25+ 云原生工程：
# - 运行全量测试：`make test`
# - 编译 Agent 与网关：`make build`
# - 构建全套镜像：`make docker-all`
# - 部署与打包：`make helm-lint` / `make helm-template`

.PHONY: help build test test-race test-unit test-console test-go test-services test-cov check lint format \
        helm-lint helm-template docker-agent docker-services docker-console docker-all clean docs-serve docs-build docs-clean

VERSION ?= 10.0.0
HELM_DIR = deploy/helm/PrivShield

help:
	@echo "Available targets:"
	@echo ""
	@echo "Building & Testing:"
	@echo "  build          - 编译 engine-go 二进制产物 (privshield-agent, privshield-gateway)"
	@echo "  test           - 运行全量 Go 测试套件 (SDK + Agent + 微服务群 + BFF)"
	@echo "  test-unit      - 运行快速单元测试"
	@echo "  test-console   - 运行 console/bff-go 单元测试"
	@echo "  test-go        - 运行 Go 全量测试"
	@echo "  test-services  - 运行三大中台微服务单元测试"
	@echo "  test-race      - 运行写入路径竞态门禁 (-race: 存储缓冲器 + 审计服务)"
	@echo ""
	@echo "Quality:"
	@echo "  lint           - go vet 静态代码检查"
	@echo "  format         - go fmt 代码格式化"
	@echo "  check          - 一键格式化与质量检查"
	@echo ""
	@echo "Deployment:"
	@echo "  helm-lint      - helm lint 检查 chart"
	@echo "  helm-template  - helm template 渲染 chart"
	@echo "  docker-agent   - 构建 Go Agent/Gateway Docker 镜像"
	@echo "  docker-services - 构建三大中台微服务 Docker 镜像"
	@echo "  docker-console - 构建控制台全套 Docker 镜像"
	@echo "  docker-all     - 构建全套 PrivShield Docker 镜像"
	@echo ""
	@echo "Docs:"
	@echo "  docs-serve     - 启动 MkDocs 开发服务器"
	@echo "  docs-build     - 构建文档站点"
	@echo "  docs-clean     - 清理文档构建产物"
	@echo ""
	@echo "Other:"
	@echo "  clean          - 清理构建产物"

# ── Build & Quality ──────────────────────────────────────────

build:
	@mkdir -p bin
	CGO_ENABLED=0 go build -ldflags="-s -w -X 'main.Version=$(VERSION)'" -o bin/privshield-agent ./engine-go/cmd/privshield-agent
	CGO_ENABLED=0 go build -ldflags="-s -w -X 'main.Version=$(VERSION)'" -o bin/privshield-gateway ./engine-go/cmd/privshield-gateway

lint:
	@for mod in pkg privacy-go-sdk engine-go services/service-hub services/datasource-mgr services/audit-log console/bff-go console/app-lz/bff-go; do \
		(cd $$mod && CGO_ENABLED=0 go vet ./...) || exit 1; \
	done

format:
	@for mod in pkg privacy-go-sdk engine-go services/service-hub services/datasource-mgr services/audit-log console/bff-go console/app-lz/bff-go; do \
		(cd $$mod && go fmt ./...) || exit 1; \
	done

check: format lint test

# ── Testing ──────────────────────────────────────────────────

test:
	CGO_ENABLED=0 go test ./pkg/... ./services/service-hub/... ./services/datasource-mgr/... ./services/audit-log/... ./console/bff-go/... ./console/app-lz/bff-go/... ./privacy-go-sdk/... ./engine-go/...

test-unit: test

test-console:
	CGO_ENABLED=0 go test -count=1 -v ./console/bff-go/... ./console/app-lz/bff-go/...

test-go: test

test-services:
	CGO_ENABLED=0 go test -count=1 ./services/service-hub/... ./services/datasource-mgr/... ./services/audit-log/...

# 微批写入缓冲器与审计服务是并发关键区，本地门禁与 CI 保持一致。
# -race 依赖 cgo，因此这里不能沿用其它目标的 CGO_ENABLED=0。
test-race:
	CGO_ENABLED=1 go test -race -count=1 -timeout 900s ./pkg/store/... ./services/audit-log/...

# ── Docker ───────────────────────────────────────────────────

docker-agent:
	docker build -t privshield-agent:$(VERSION) -f Dockerfile .

docker-services:
	docker build -t privshield-service-hub:$(VERSION) -f services/service-hub/Dockerfile .
	docker build -t privshield-datasource-mgr:$(VERSION) -f services/datasource-mgr/Dockerfile .
	docker build -t privshield-audit-log:$(VERSION) -f services/audit-log/Dockerfile .

docker-console:
	docker build -t privshield-bff-go:$(VERSION) -f console/bff-go/Dockerfile .
	docker build -t privshield-web:$(VERSION) -f console/web/Dockerfile .

docker-all: docker-agent docker-services docker-console

# ── Helm ─────────────────────────────────────────────────────

helm-lint:
	helm lint $(HELM_DIR)

helm-template:
	helm template privshield $(HELM_DIR)

# ── Docs ─────────────────────────────────────────────────────

docs-serve:
	mkdocs serve

docs-build:
	mkdocs build

docs-clean:
	rm -rf site/

# ── Clean ────────────────────────────────────────────────────

clean:
	rm -rf bin/ site/
