APP_NAME = wisp
BIN_DIR = ./bin
INSTALL_DIR ?= $(HOME)/.local/bin
UNAME_S := $(shell uname -s)

.PHONY: all build check fmt install smoke test clean help

all: build

build:
	@echo "🔨 Building Go $(APP_NAME)..."
	@mkdir -p "$(BIN_DIR)"
	go build -o "$(BIN_DIR)/$(APP_NAME)" ./cmd/wisp
	@ln -sf "$(APP_NAME)" "$(BIN_DIR)/wispd"
	@if [ "$(UNAME_S)" = "Darwin" ] && command -v swiftc >/dev/null 2>&1; then \
		echo "🔨 Building macOS hint floater..."; \
		swiftc -O -o "$(BIN_DIR)/wisp-hint-macos" ./cmd/wisp-hint-macos/main.swift; \
	fi

check:
	go test ./...

fmt:
	gofmt -w ./cmd/wisp/main.go

install: build
	@echo "📦 Installing to $(INSTALL_DIR)/$(APP_NAME)"
	@mkdir -p "$(INSTALL_DIR)"
	install -m 0755 "$(BIN_DIR)/$(APP_NAME)" "$(INSTALL_DIR)/$(APP_NAME)"
	ln -sf "$(APP_NAME)" "$(INSTALL_DIR)/wispd"
	@if [ -x "$(BIN_DIR)/wisp-hint-macos" ]; then \
		install -m 0755 "$(BIN_DIR)/wisp-hint-macos" "$(INSTALL_DIR)/wisp-hint-macos"; \
	fi
	@if command -v xattr >/dev/null 2>&1; then \
		xattr -d com.apple.quarantine "$(INSTALL_DIR)/$(APP_NAME)" 2>/dev/null || true; \
	fi
	@mkdir -p "$(HOME)/.local/share/zsh/site-functions" "$(HOME)/.local/share/bash-completion/completions"
	install -m 0644 "completions/zsh/_wisp" "$(HOME)/.local/share/zsh/site-functions/_wisp"
	install -m 0644 "completions/bash/wisp" "$(HOME)/.local/share/bash-completion/completions/wisp"
	@echo "✅ Installed. Run with: $(APP_NAME)"

smoke:
	scripts/smoke-test

test: check

clean:
	rm -rf "$(BIN_DIR)"

help:
	@echo "Available targets:"
	@echo "  all     - Build wisp (default)"
	@echo "  build   - Build bin/wisp, bin/wispd symlink, and macOS hint floater when available"
	@echo "  check   - Run Go tests"
	@echo "  fmt     - Format Go sources"
	@echo "  install - Build and install wisp, wispd symlink, optional hint floater, plus completions"
	@echo "  smoke   - Run smoke test script"
	@echo "  test    - Alias for check"
	@echo "  clean   - Remove build artifacts"
	@echo "  help    - Show this help"
