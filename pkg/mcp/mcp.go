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

// Server is the MCP Proxy Server
type Server struct {
	port       int
	store      *MCPStore
	bus        *events.Bus
	tokenStore auth.TokenStoreInterface
	connMgr    *connectionManagerImpl
	httpServer *http.Server
}

// NewServer creates a new MCP proxy server
func NewServer(port int, store *MCPStore, bus *events.Bus, tokenStore auth.TokenStoreInterface) *Server {
	return &Server{
		port:       port,
		store:      store,
		bus:        bus,
		tokenStore: tokenStore,
		connMgr:    NewConnectionManager(),
	}
}

// setupRoutes registers MCP proxy routes
func (s *Server) setupRoutes() {
	mux := http.NewServeMux()

	// SSE transport routes
	mux.HandleFunc("/mcp/{id}/sse", s.proxyAuthMiddleware(s.handleSSEConnection))
	mux.HandleFunc("/mcp/{id}/messages", s.proxyAuthMiddleware(s.handleSSEMessage))

	// Streamable HTTP transport routes
	mux.HandleFunc("/mcp/{id}/", s.proxyAuthMiddleware(s.handleStreamableHTTP))
	mux.HandleFunc("/mcp/{id}", s.proxyAuthMiddleware(s.handleStreamableHTTP))

	s.httpServer.Handler = mux
}

// RegisterAPIHandlers registers management API routes — stub for Phase 3 to fill
func (s *Server) RegisterAPIHandlers(mux *http.ServeMux) {
	// Phase 3 will add: /fe/api/mcp-servers, /fe/api/mcp-servers/, /fe/api/mcp-servers/status
}

// Start creates and starts the HTTP server
func (s *Server) Start() error {
	s.httpServer = &http.Server{
		Addr: ":" + strconv.Itoa(s.port),
	}
	s.setupRoutes()
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}
