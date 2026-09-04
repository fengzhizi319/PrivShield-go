# console/engine-console/Makefile
.PHONY: all build test dev clean

all: build test

build:
	@bash scripts/build.sh

test:
	CGO_ENABLED=0 go test -v ./bff-go/...

dev:
	@bash scripts/dev.sh

clean:
	rm -rf bff-go/bin/ web/dist/
