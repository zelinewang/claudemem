VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X github.com/zelinewang/claudemem/cmd.Version=$(VERSION)
BINARY  := claudemem

.PHONY: build install test feature-test e2e-test clean test-all

# Build a single static binary (pure Go, no CGO). Network calls are
# opt-in at runtime via `claudemem setup` — default is zero network.
build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

# Install to ~/.local/bin/
install: build
	mkdir -p $(HOME)/.local/bin
	cp $(BINARY) $(HOME)/.local/bin/$(BINARY)
	@echo "Installed to $(HOME)/.local/bin/$(BINARY)"

# Quick smoke test
test: build
	@echo "Running smoke test..."
	@STORE=$$(mktemp -d) && \
	./$(BINARY) --store $$STORE note add test --title "Smoke" --content "Test" --tags "test" && \
	./$(BINARY) --store $$STORE note search "Smoke" && \
	./$(BINARY) --store $$STORE session save --title "Smoke" --branch "test" --project "." --session-id "t1" --summary "Smoke test" && \
	./$(BINARY) --store $$STORE search "Smoke" && \
	./$(BINARY) --store $$STORE stats && \
	rm -rf $$STORE && \
	echo "✓ All smoke tests passed"

# End-to-end CLI tests
e2e-test: build
	@echo "Running E2E tests..."
	@bash ./e2e_test.sh

# Comprehensive black-box feature tests (74 cases across 7 levels)
feature-test: build
	@bash tests/feature_test.sh

# Run ALL tests: unit + smoke + e2e + feature
test-all: build
	@echo "=== Unit Tests ==="
	@go test ./... -count=1
	@echo ""
	@echo "=== Smoke Test ==="
	@$(MAKE) test
	@echo ""
	@echo "=== E2E Tests ==="
	@bash ./e2e_test.sh
	@echo ""
	@echo "=== Feature Tests ==="
	@bash tests/feature_test.sh

clean:
	rm -f $(BINARY)
