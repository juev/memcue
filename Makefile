# Releases of the binary are the v* tags; the plugin tags (dsh-v*, pi-v*) share the namespace and must not count (#740).
RELEASE_TAGS := --tags --match 'v*'
VERSION ?= $(shell git describe $(RELEASE_TAGS) --always --dirty 2>/dev/null || echo dev)
GOLANGCI_VERSION ?= v2.14.0
GOLANGCI_LINT ?= golangci-lint
CLAUDE_BIN ?= claude
HERMES_PYTHON ?= python3

.PHONY: build test test-plugins test-opencode test-pi test-codex test-qwen test-claude-mods test-hermes-request lint bench previous-release

build:
	go build -p 2 -trimpath -ldflags "-X main.version=$(VERSION)" -o bin/memcue ./cmd/memcue

# The release before TAG, which the release workflow hands to goreleaser for the changelog: left to itself, goreleaser
# takes whatever tag is nearest, and a plugin tag one commit back cut the v0.37.0 notes to one line.
previous-release:
	@test -n "$(TAG)" || { echo "usage: make previous-release TAG=vX.Y.Z" >&2; exit 2; }
	@git describe $(RELEASE_TAGS) --abbrev=0 '$(TAG)^'

test: build test-codex test-qwen test-claude-mods
	go test -race -p 2 -parallel 2 -count=1 -json ./...
	MEMCUE_BIN=$(CURDIR)/bin/memcue $(HERMES_PYTHON) -I plugins/hermes/memcue/tests/test_provider.py
	MEMCUE_BIN=$(CURDIR)/bin/memcue $(HERMES_PYTHON) -I -m unittest discover -s plugins/hermes -p 'test_*.py'
	python3 -I plugins/test_cross_harness.py
	MEMCUE_BIN=$(CURDIR)/bin/memcue python3 -I docs/research/scripts/test_search_stand.py
	$(MAKE) test-plugins

# The bench drives a built binary through search -json (#451). make test already runs it under go test ./...; this
# target runs it alone, case by case. MEMCUE_BIN=/path/to/memcue judges another build instead of this tree.
bench:
	go test -race -p 2 -parallel 2 ./internal/bench -v

# The plugin tests need the harness packages as peers. A checkout without node_modules (a release build, the CI Go
# job) skips them with the command that would install them, instead of failing on an import the task does not need.
test-plugins: build
	@if [ -d node_modules/@deepseek-ai/dsh-llm ] && [ -d node_modules/@deepseek-ai/dsh-tools ]; then \
		MEMCUE_BIN="$(CURDIR)/bin/memcue" node --experimental-test-module-mocks --test --test-concurrency=2 plugins/deepseek-harness/test/*.test.mjs plugins/claude-code/test/*.test.mjs; \
	else \
		echo "skipping plugin tests: node_modules lacks @deepseek-ai/dsh-llm and @deepseek-ai/dsh-tools"; \
		echo "install them with: npm install --no-save --no-package-lock @deepseek-ai/dsh-llm@0.2.0-rc.2 @deepseek-ai/dsh-tools@0.2.0-rc.2"; \
	fi
	$(MAKE) test-pi
	$(MAKE) test-opencode

lint:
	@have=$$($(GOLANGCI_LINT) version 2>&1 | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | head -1); \
	if [ "$$have" != "$(GOLANGCI_VERSION)" ]; then \
		echo "warning: golangci-lint $${have:-unknown}, CI pins $(GOLANGCI_VERSION)"; \
	fi
	go vet -p 2 ./...
	$(GOLANGCI_LINT) run --concurrency 2 ./...

# Install the plugin's dependencies first: npm --prefix plugins/opencode ci
test-opencode: build
	npm --prefix plugins/opencode run typecheck
	MEMCUE_TEST_BINARY="$(CURDIR)/bin/memcue" npm --prefix plugins/opencode test

# Install the pinned Pi test dependencies first: npm --prefix plugins/pi ci
test-pi: build
	npm --prefix plugins/pi run typecheck
	MEMCUE_REQUIRE_LIVE=1 MEMCUE_TEST_BINARY="$(CURDIR)/bin/memcue" npm --prefix plugins/pi test

# Synthetic CLI/configuration coverage; requires the built binary, never a live memory base.
test-codex: build
	MEMCUE_BIN=$(CURDIR)/bin/memcue python3 -I -m unittest discover -s plugins/codex/tests -p 'test_*.py'

# Synthetic CLI/configuration coverage for the Qwen Code adapter; requires the built binary, never a live base.
test-qwen: build
	MEMCUE_BIN=$(CURDIR)/bin/memcue python3 -I -m unittest discover -s plugins/qwen-code/tests -p 'test_*.py'

# Model-free native clients; no authentication or inference is needed.
test-claude-mods:
	$(CLAUDE_BIN) plugin validate plugins/claude-code
	$(CLAUDE_BIN) plugin test plugins/claude-code

# Run with the released Hermes >=0.17.0 interpreter, also exercised by hermes-contract CI.
test-hermes-request: build
	MEMCUE_BIN=$(CURDIR)/bin/memcue $(HERMES_PYTHON) -I plugins/hermes/test_request_context.py -v
