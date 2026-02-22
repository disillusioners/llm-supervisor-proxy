BINARY_NAME=llm-supervisor-proxy
PREFIX?=/usr/local
BINDIR=$(PREFIX)/bin
VERSION_FILE=VERSION

.PHONY: all build build-frontend clean install uninstall test run help

# Default target
all: build-frontend build

help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  all             Build both frontend and backend"
	@echo "  build           Build the Go backend (auto-increments version)"
	@echo "  build-frontend  Build the frontend UI"
	@echo "  install         Build and install to $(BINDIR) (auto-sudo for install step)"
	@echo "  uninstall       Remove the binary from $(BINDIR)"
	@echo "  run             Build and run the proxy locally"
	@echo "  test            Run Go tests"
	@echo "  clean           Remove build artifacts"
	@echo ""
	@echo "Current version: $$(cat $(VERSION_FILE))"

build-frontend:
	@echo "Building frontend..."
	@cd pkg/ui/frontend && npm install && npm run build

build:
	$(eval VERSION := $(shell cat $(VERSION_FILE)))
	$(eval NEXT_VERSION := $(shell echo $$(($(VERSION) + 1))))
	@echo $(NEXT_VERSION) > $(VERSION_FILE)
	@echo "Building backend (build $(NEXT_VERSION))..."
	@go build -ldflags "-X main.Version=$(NEXT_VERSION)" -o $(BINARY_NAME) ./cmd/main.go

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
	@go test ./...

clean:
	@echo "Cleaning..."
	@rm -f $(BINARY_NAME)
