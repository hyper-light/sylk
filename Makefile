.PHONY: all build build-gguf test test-gguf clean libbinding submodules claims-infra-ci claims-infra-lint claims-infra-race mockery-check docs-traceability

LLAMA_DIR := third_party/go-llama.cpp
LIBRARY_PATH := $(shell pwd)/$(LLAMA_DIR)
C_INCLUDE_PATH := $(shell pwd)/$(LLAMA_DIR)

all: build

submodules:
	git submodule update --init --recursive

libbinding: submodules
	$(MAKE) -C $(LLAMA_DIR) libbinding.a

build:
	go build ./...

build-gguf: libbinding
	LIBRARY_PATH=$(LIBRARY_PATH) C_INCLUDE_PATH=$(C_INCLUDE_PATH) go build -tags gguf ./...

test:
	go test ./...

claims-infra-ci: test claims-infra-race mockery-check docs-traceability claims-infra-lint

claims-infra-lint:
	go run ./cmd/sylk-lint ./...

claims-infra-race:
	go test -race ./core/claims ./ui/bridge ./core/ci/analyzers/claimsops

mockery-check:
	mockery --config .mockery.yaml --dry-run

docs-traceability:
	test -s docs/CLAIMS_AND_INFRASTRUCTURE.md
	test -s docs/CLAIMS_OPERATIONS.md

test-gguf: libbinding
	LIBRARY_PATH=$(LIBRARY_PATH) C_INCLUDE_PATH=$(C_INCLUDE_PATH) go test -tags gguf ./...

clean:
	$(MAKE) -C $(LLAMA_DIR) clean 2>/dev/null || true
	go clean ./...
