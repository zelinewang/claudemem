VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X github.com/zelinewang/claudemem/cmd.Version=$(VERSION)
BINARY  := claudemem

.PHONY: build install deploy test feature-test e2e-test clean test-all

# Build a single static binary (pure Go, no CGO). Network calls are
# opt-in at runtime via `claudemem setup` — default is zero network.
build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

# Install to ~/.local/bin/
# After the atomic rename, re-run the user's wrapper hook if one exists:
# installing replaces ~/.local/bin/$(BINARY) wholesale, which clobbers any
# credential-injection shim the user keeps in front of the real binary.
# The hook is optional — absent on machines that don't use a wrapper.
install: build
	mkdir -p $(HOME)/.local/bin
	tmp="$(HOME)/.local/bin/$(BINARY).tmp.$$$$" && \
		cp $(BINARY) "$$tmp" && \
		chmod 755 "$$tmp" && \
		mv "$$tmp" $(HOME)/.local/bin/$(BINARY)
	@echo "Installed to $(HOME)/.local/bin/$(BINARY)"
	@if [ -x "$(HOME)/.claudemem/ensure-wrapper.sh" ]; then \
		sh "$(HOME)/.claudemem/ensure-wrapper.sh" && \
		echo "Post-install hook: user wrapper re-applied (~/.claudemem/ensure-wrapper.sh)"; \
	fi

# Cross-compile and deploy to a remote machine (staged scp + atomic rename).
# A raw `scp` straight onto ~/.local/bin/$(BINARY) would (a) expose a torn
# binary mid-transfer and (b) clobber any credential-injection wrapper the
# remote user keeps in front of the real binary. This target stages under a
# temp name, renames atomically, then re-runs the remote user's
# ~/.claudemem/ensure-wrapper.sh hook if present (optional, like install).
# Usage: make deploy HOST=<ssh-host> [GOOS=linux] [GOARCH=amd64]
GOOS   ?= linux
GOARCH ?= amd64
deploy:
	@test -n "$(HOST)" || { echo "usage: make deploy HOST=<ssh-host> [GOOS=...] [GOARCH=...]"; exit 1; }
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags "$(LDFLAGS)" -o $(BINARY)-$(GOOS)-$(GOARCH) .
	scp $(BINARY)-$(GOOS)-$(GOARCH) $(HOST):.local/bin/$(BINARY).staged
	ssh $(HOST) "sh -c 'chmod 755 \"\$$HOME/.local/bin/$(BINARY).staged\" && mv -f \"\$$HOME/.local/bin/$(BINARY).staged\" \"\$$HOME/.local/bin/$(BINARY)\" && if [ -x \"\$$HOME/.claudemem/ensure-wrapper.sh\" ]; then sh \"\$$HOME/.claudemem/ensure-wrapper.sh\"; fi'"
	@echo "Deployed $(BINARY) $(GOOS)/$(GOARCH) to $(HOST)"

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
	rm -f $(BINARY) $(BINARY)-*
