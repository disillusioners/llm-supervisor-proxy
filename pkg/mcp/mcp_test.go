package mcp

import (
	"context"
	"net/http"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
)

func TestNewServer(t *testing.T) {
	t.Parallel()

	store := (*MCPStore)(nil)
	bus := (*events.Bus)(nil)
	var tokenStore auth.TokenStoreInterface

	server := NewServer(store, bus, tokenStore)

	if server == nil {
		t.Fatal("NewServer returned nil")
	}

	if server.store != store {
		t.Error("server.store should be nil")
	}
	if server.bus != bus {
		t.Error("server.bus should be nil")
	}
	if server.tokenStore != tokenStore {
		t.Error("server.tokenStore should be nil")
	}
	if server.connMgr == nil {
		t.Error("server.connMgr should not be nil")
	}
}

func TestShutdown(t *testing.T) {
	t.Parallel()

	// Create server
	server := NewServer(nil, nil, nil)

	// Call Shutdown with a context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := server.Shutdown(ctx)
	if err != nil {
		t.Errorf("Shutdown() returned error: %v", err)
	}
}

func TestRegisterProxyHandlers_NilTokenStore(t *testing.T) {
	t.Parallel()

	// Create server with nil tokenStore
	server := NewServer(nil, nil, nil)

	mux := http.NewServeMux()
	server.RegisterProxyHandlers(mux)

	// Should not panic and mux should have no handlers registered (handlers are added on nil tokenStore check)
}

func TestRegisterAPIHandlers_NilStore(t *testing.T) {
	t.Parallel()

	// Create server with nil store
	server := NewServer(nil, nil, nil)

	mux := http.NewServeMux()
	server.RegisterAPIHandlers(mux)

	// Should not panic and mux should have no handlers registered (handlers are added on nil store check)
}
