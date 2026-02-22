BINARY_NAME=llm-supervisor-proxy
PREFIX?=/usr/local
BINDIR=$(PREFIX)/bin

.PHONY: all build build-frontend clean install uninstall test help

# Default target
all: build-frontend build

help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  all             Build both frontend and backend"
	@echo "  build           Build the Go backend"
	@echo "  build-frontend  Build the frontend UI"
	@echo "  install         Install the binary to $(BINDIR) (may require sudo)"
	@echo "  uninstall       Remove the binary from $(BINDIR)"
	@echo "  test            Run Go tests"
	@echo "  clean           Remove build artifacts"

build-frontend:
	@echo "Building frontend..."
	@cd pkg/ui/frontend && npm install && npm run build

build:
	@echo "Building backend..."
	@go build -o $(BINARY_NAME) ./cmd/main.go

install: all
	@echo "Installing to $(BINDIR)..."
	@install -d $(BINDIR)
	@install -m 755 $(BINARY_NAME) $(BINDIR)/$(BINARY_NAME)
	@echo "Done! You can now run '$(BINARY_NAME)'"

uninstall:
	@echo "Uninstalling from $(BINDIR)..."
	@rm -f $(BINDIR)/$(BINARY_NAME)

test:
	@echo "Running tests..."
	@go test ./...

clean:
	@echo "Cleaning..."
	@rm -f $(BINARY_NAME)
