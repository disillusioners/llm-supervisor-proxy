package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
)

func TestGetMCPProxyPort(t *testing.T) {
	tests := []struct {
		name   string
		envVal string
		want   int
	}{
		{
			name:   "Valid port",
			envVal: "9090",
			want:   9090,
		},
		{
			name:   "Empty env",
			envVal: "",
			want:   0,
		},
		{
			name:   "Non-numeric",
			envVal: "abc",
			want:   0,
		},
		{
			name:   "Too high",
			envVal: "99999",
			want:   0,
		},
		{
			name:   "Zero",
			envVal: "0",
			want:   0,
		},
		{
			name:   "Negative",
			envVal: "-1",
			want:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVal != "" {
				t.Setenv("MCP_PROXY_PORT", tt.envVal)
			} else {
				t.Setenv("MCP_PROXY_PORT", "")
			}

			got := GetMCPProxyPort()
			if got != tt.want {
				t.Errorf("GetMCPProxyPort() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestNewServer(t *testing.T) {
	t.Parallel()

	port := 1234
	store := (*MCPStore)(nil)
	bus := (*events.Bus)(nil)
	var tokenStore auth.TokenStoreInterface

	server := NewServer(port, store, bus, tokenStore)

	if server == nil {
		t.Fatal("NewServer returned nil")
	}

	if server.port != port {
		t.Errorf("server.port = %d, want %d", server.port, port)
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
	if server.httpServer != nil {
		t.Error("server.httpServer should be nil before Start()")
	}
}

func TestShutdown_NilHTTPServer(t *testing.T) {
	t.Parallel()

	// Create server with nil httpServer
	server := NewServer(1234, nil, nil, nil)

	// Call Shutdown with a context that times out quickly
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := server.Shutdown(ctx)
	if err != nil {
		t.Errorf("Shutdown() with nil httpServer returned error: %v", err)
	}
}
