VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test lint

build:
	go build -trimpath -ldflags "-X main.version=$(VERSION)" -o bin/memcue ./cmd/memcue

test: build
	go test -race ./...
	MEMCUE_BIN=$(CURDIR)/bin/memcue python3 -I plugins/hermes/memcue/tests/test_provider.py

lint:
	go vet ./...
	golangci-lint run ./...
