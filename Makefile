BINARY_NAME=llm-supervisor-proxy
PREFIX?=/usr/local
BINDIR=$(PREFIX)/bin
# Version injected at build time (CI passes the run number); local
# builds default to "dev". There is NO committed VERSION file — the
# version lives only in the binary via the -ldflags below.
VERSION ?= dev

.PHONY: all build build-frontend clean install uninstall test run help

# Default target
all: build-frontend build

help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  all             Build both frontend and backend"
	@echo "  build           Build the Go backend (VERSION=<v> to override, default dev)"
	@echo "  build-frontend  Build the frontend UI"
	@echo "  install         Build and install to $(BINDIR) (auto-sudo for install step)"
	@echo "  uninstall       Remove the binary from $(BINDIR)"
	@echo "  run             Build and run the proxy locally"
	@echo "  test            Run Go tests"
	@echo "  clean           Remove build artifacts"
	@echo ""

build-frontend:
	@echo "Building frontend..."
	@cd pkg/ui/frontend && npm install && npm run build

build:
	@echo "Building backend (version $(VERSION))..."
	@go build -ldflags "-X main.Version=$(VERSION)" -o $(BINARY_NAME) ./cmd/main.go

# Build as current user, then sudo only for the copy to system bin
install: all
	@echo "Installing to $(BINDIR) (requires sudo)..."
	@sudo install -d $(BINDIR)
	@sudo install -m 755 $(BINARY_NAME) $(BINDIR)/$(BINARY_NAME)
	@echo "Done! You can now run '$(BINARY_NAME)'"

uninstall:
	@echo "Uninstalling from $(BINDIR)..."
	@sudo rm -f $(BINDIR)/$(BINARY_NAME)

run: all
	@echo "Starting $(BINARY_NAME)..."
	@./$(BINARY_NAME)

test:
	@echo "Running tests..."
	@echo ">> import guard: pkg/imggen must not import pkg/proxy (Amendment 9)"
	@if grep -rnE 'github\.com/disillusioners/llm-supervisor-proxy/pkg/proxy(/|")' pkg/imggen/; then \
		echo "FAIL: pkg/imggen must not import pkg/proxy (import guard, Amendment 9 — see pkg/imggen/doc.go)"; \
		exit 1; \
	fi
	@echo ">> import guard: pkg/decision must not import pkg/proxy (Decision Models commission / §3.5)"
	@if grep -rnE 'github\.com/disillusioners/llm-supervisor-proxy/pkg/proxy(/|")' pkg/decision/; then \
		echo "FAIL: pkg/decision must not import pkg/proxy (import guard, Decision Models §3.5 — see pkg/decision/doc.go)"; \
		exit 1; \
	fi
	@echo ">> import guard: pkg/decision must not import pkg/imggen (Decision Models commission / §3.5, transitive-closure improvement over imggen)"
	@if grep -rnE 'github\.com/disillusioners/llm-supervisor-proxy/pkg/imggen(/|")' pkg/decision/; then \
		echo "FAIL: pkg/decision must not import pkg/imggen (import guard, Decision Models §3.5 transitive closure)"; \
		exit 1; \
	fi
	@go test ./...

clean:
	@echo "Cleaning..."
	@rm -f $(BINARY_NAME)
