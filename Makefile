.PHONY: all build build-gguf test test-gguf clean libbinding submodules claims-infra-ci claims-infra-lint claims-infra-race mockery-check docs-traceability

LLAMA_DIR := third_party/go-llama.cpp
LIBRARY_PATH := $(shell pwd)/$(LLAMA_DIR)
C_INCLUDE_PATH := $(shell pwd)/$(LLAMA_DIR)
GOCACHE ?= /tmp/sylk-gocache
MOCKERY ?= GOCACHE=$(GOCACHE) go tool mockery

all: build

submodules:
	git submodule update --init --recursive

libbinding: submodules
	$(MAKE) -C $(LLAMA_DIR) libbinding.a

build:
	GOCACHE=$(GOCACHE) go build ./...

build-gguf: libbinding
	GOCACHE=$(GOCACHE) LIBRARY_PATH=$(LIBRARY_PATH) C_INCLUDE_PATH=$(C_INCLUDE_PATH) go build -tags gguf ./...

test:
	GOCACHE=$(GOCACHE) go test ./...

claims-infra-ci: test claims-infra-race mockery-check docs-traceability claims-infra-lint

claims-infra-lint:
	GOCACHE=$(GOCACHE) go run ./cmd/sylk-lint ./...

claims-infra-race:
	GOCACHE=$(GOCACHE) go test -race ./core/claims ./core/boot ./agents/shared ./ui/bridge ./core/ci/analyzers/claimsops

mockery-check:
	$(MOCKERY) --config .mockery.yaml --dry-run

docs-traceability:
	scripts/ci/docs-traceability.sh

test-gguf: libbinding
	GOCACHE=$(GOCACHE) LIBRARY_PATH=$(LIBRARY_PATH) C_INCLUDE_PATH=$(C_INCLUDE_PATH) go test -tags gguf ./...

clean:
	$(MAKE) -C $(LLAMA_DIR) clean 2>/dev/null || true
	go clean ./...
