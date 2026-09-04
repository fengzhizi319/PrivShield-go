# ============================================================================
# Datasource Manager (模拟数据源服务) Build Automation
# ============================================================================

.PHONY: build run dev prod gen-certs test test-unit lint clean

build:
	go mod tidy
	go build -o bin/datasource-mgr ./cmd/server

run: dev

dev:
	bash scripts/dev-run.sh

prod:
	bash scripts/prod-run.sh

gen-certs:
	bash scripts/gen-certs.sh

test:
	go test ./...

test-unit:
	go test -short ./...

lint:
	go fmt ./...
	go vet ./...

clean:
	rm -rf bin
