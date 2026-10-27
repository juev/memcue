VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOLANGCI_VERSION ?= v2.13.0

.PHONY: build test test-plugins lint bench

build:
	go build -trimpath -ldflags "-X main.version=$(VERSION)" -o bin/memcue ./cmd/memcue

test: build
	go test -race ./...
	MEMCUE_BIN=$(CURDIR)/bin/memcue python3 -I plugins/hermes/memcue/tests/test_provider.py
	python3 -I plugins/test_cross_harness.py
	MEMCUE_BIN=$(CURDIR)/bin/memcue python3 -I docs/research/scripts/test_search_stand.py
	$(MAKE) test-plugins

# The bench drives a built binary through search -json (#451). make test already runs it under go test ./...; this
# target runs it alone, case by case. MEMCUE_BIN=/path/to/memcue judges another build instead of this tree.
bench:
	go test -race ./internal/bench -v

# The plugin tests need the harness packages as peers. A checkout without node_modules (a release build, the CI Go
# job) skips them with the command that would install them, instead of failing on an import the task does not need.
test-plugins:
	@if [ -d node_modules/@deepseek-ai/dsh-llm ] && [ -d node_modules/@deepseek-ai/dsh-tools ]; then \
		node --test plugins/deepseek-harness/test/*.test.mjs plugins/claude-code/test/*.test.mjs; \
	else \
		echo "skipping plugin tests: node_modules lacks @deepseek-ai/dsh-llm and @deepseek-ai/dsh-tools"; \
		echo "install them with: npm install --no-save --no-package-lock @deepseek-ai/dsh-llm@0.2.0-rc.2 @deepseek-ai/dsh-tools@0.2.0-rc.2"; \
	fi

lint:
	@have=$$(golangci-lint version 2>&1 | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | head -1); \
	if [ "$$have" != "$(GOLANGCI_VERSION)" ]; then \
		echo "warning: golangci-lint $${have:-unknown}, CI pins $(GOLANGCI_VERSION)"; \
	fi
	go vet ./...
	golangci-lint run ./...
