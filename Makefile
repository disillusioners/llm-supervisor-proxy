BINARY_NAME=llm-supervisor-proxy
PREFIX?=/usr/local
BINDIR=$(PREFIX)/bin
# Version injected at build time (CI passes the run number); local
# builds default to "dev". There is NO committed VERSION file — the
# version lives only in the binary via the -ldflags below.
VERSION ?= dev

.PHONY: all build build-frontend clean install uninstall test run help census-gate

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
	@echo ">> import guard: pkg/oauth2 must not import pkg/proxy / pkg/ui / pkg/store (internal-oauth2)"
	@if grep -rnE 'github\.com/disillusioners/llm-supervisor-proxy/pkg/(proxy|ui|store)(/|")' pkg/oauth2/; then \
		echo "FAIL: pkg/oauth2 must not import pkg/proxy / pkg/ui / pkg/store (import guard, internal-oauth2)"; \
		exit 1; \
	fi
	@echo ">> import guard: pkg/ui must not import pkg/oauth2 (internal-oauth2 seam B1)"
	@if grep -rnE 'github\.com/disillusioners/llm-supervisor-proxy/pkg/oauth2(/|")' pkg/ui/; then \
		echo "FAIL: pkg/ui must not import pkg/oauth2 (import guard, internal-oauth2 seam B1)"; \
		exit 1; \
	fi
	@echo ">> import guard: pkg/identity must not import ANY repo package (internal-oauth2 leaf, plan §5.8)"
	@if grep -rnE 'github\.com/disillusioners/llm-supervisor-proxy/pkg/[a-z]+(/|")' pkg/identity/; then \
		echo "FAIL: pkg/identity must not import ANY repo package (import guard, internal-oauth2 leaf — plan §5.8: pkg/identity imports NOTHING from the repo)"; \
		exit 1; \
	fi
	@echo ">> census anti-drift gate: registered mux patterns vs pkg/oauth2/middleware_test.go (BLK-4)"
	@$(MAKE) census-gate
	@go test ./...

census-gate:
	@set -e; \
	reg=$$(mktemp); cens=$$(mktemp); trap 'rm -f $$reg $$cens' EXIT; \
	grep -hEo 'mux\.(Handle|HandleFunc)\("[^"]*"' pkg/ui/server.go pkg/mcp/mcp.go cmd/main.go pkg/oauth2/handler.go \
	  | sed -E 's/mux\.(Handle|HandleFunc)\("([^"]*)"/\2/' | sort -u > $$reg; \
	grep -oE 'route: "[^"]*"' pkg/oauth2/middleware_test.go \
	  | sed -E 's/route: "([^"]*)"/\1/' | sort -u > $$cens; \
	missing=$$(comm -23 $$reg $$cens); \
	if [ -n "$$missing" ]; then \
	  echo "FAIL: registered mux pattern(s) missing from the census (BLK-4 anti-drift):"; \
	  printf '%s\n' "$$missing" | sed 's/^/  /'; \
	  exit 1; \
	fi; \
	extra=$$(comm -13 $$reg $$cens); \
	bad=""; \
	for p in $$extra; do \
	  case "$$p" in \
	    /ui) ;; \
	    *) bad="$$bad $$p";; \
	  esac; \
	done; \
	if [ -n "$$bad" ]; then \
	  echo "FAIL: census route(s) with no registration site (not allow-listed):$$bad"; \
	  exit 1; \
	fi; \
	echo ">> census gate: registered=$$(wc -l < $$reg) census-paths=$$(wc -l < $$cens) — OK"; \
	echo "   allow-listed no-site: /ui (derived from /ui/ trailing-slash redirect)"; \
	echo "   allow-listed method-variant: /fe/api/version POST (method-agnostic registration at pkg/ui/server.go serves all methods)"

clean:
	@echo "Cleaning..."
	@rm -f $(BINARY_NAME)
