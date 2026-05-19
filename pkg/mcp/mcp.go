package mcp

import (
	"context"
	"net/http"
	"os"
	"strconv"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
)

// GetMCPProxyPort reads MCP_PROXY_PORT env var, returns 0 if unset/invalid
func GetMCPProxyPort() int {
	val := os.Getenv("MCP_PROXY_PORT")
	if val == "" {
		return 0
	}
	port, err := strconv.Atoi(val)
	if err != nil || port < 1 || port > 65535 {
		return 0
	}
	return port
}

// ConnectionManager placeholder for Phase 2
type ConnectionManager struct {
	// Will be implemented in Phase 2
}

// Server is the MCP Proxy Server
type Server struct {
	port       int
	store      *MCPStore
	bus        *events.Bus
	tokenStore auth.TokenStoreInterface
	connMgr    *ConnectionManager
	httpServer *http.Server
}

// NewServer creates a new MCP proxy server
func NewServer(port int, store *MCPStore, bus *events.Bus, tokenStore auth.TokenStoreInterface) *Server {
	return &Server{
		port:       port,
		store:      store,
		bus:        bus,
		tokenStore: tokenStore,
	}
}

// setupRoutes registers MCP proxy routes — stub for Phase 2 to fill
func (s *Server) setupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	// Phase 2 will add: /mcp/{id}/sse, /mcp/{id}/messages, /mcp/{id}/http
}

// RegisterAPIHandlers registers management API routes — stub for Phase 3 to fill
func (s *Server) RegisterAPIHandlers(mux *http.ServeMux) {
	// Phase 3 will add: /fe/api/mcp-servers, /fe/api/mcp-servers/, /fe/api/mcp-servers/status
}

// Start creates and starts the HTTP server
func (s *Server) Start() error {
	mux := http.NewServeMux()
	s.setupRoutes(mux)
	s.httpServer = &http.Server{
		Addr:    ":" + strconv.Itoa(s.port),
		Handler: mux,
	}
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}
