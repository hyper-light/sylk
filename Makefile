.PHONY: all build build-gguf test test-gguf clean libbinding submodules

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

test-gguf: libbinding
	LIBRARY_PATH=$(LIBRARY_PATH) C_INCLUDE_PATH=$(C_INCLUDE_PATH) go test -tags gguf ./...

clean:
	$(MAKE) -C $(LLAMA_DIR) clean 2>/dev/null || true
	go clean ./...
