# services/privacy-engine/Makefile
.PHONY: all build test bench run-agent run-gateway clean

VERSION ?= 10.0.0

all: build test

build:
	@mkdir -p bin
	CGO_ENABLED=0 go build -ldflags="-s -w -X 'main.Version=$(VERSION)'" -o bin/privshield-agent ./cmd/privshield-agent
	CGO_ENABLED=0 go build -ldflags="-s -w -X 'main.Version=$(VERSION)'" -o bin/privshield-gateway ./cmd/privshield-gateway

test:
	CGO_ENABLED=0 go test -v ./... ./sdk/...

bench:
	@bash scripts/bench.sh

run-agent:
	@bash scripts/run-agent.sh

run-gateway:
	@bash scripts/run-gateway.sh

clean:
	rm -rf bin/
