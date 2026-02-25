VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X github.com/zelinewang/claudemem/cmd.Version=$(VERSION)
BINARY  := claudemem

.PHONY: build install test clean verify-no-network

# Default build: no network, no sync
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

# Verify default build has no net/http (security)
verify-no-network: build
	@if go tool nm $(BINARY) 2>/dev/null | grep -q 'net/http\.'; then \
		echo "FAIL: default binary contains net/http"; exit 1; \
	else \
		echo "✓ Default binary has no network imports"; \
	fi

clean:
	rm -f $(BINARY)
