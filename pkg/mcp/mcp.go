package mcp

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"

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

// RegisterAPIHandlers registers management API routes for MCP servers.
func (s *Server) RegisterAPIHandlers(mux *http.ServeMux) {
	if s.store == nil || s.tokenStore == nil {
		return
	}

	// Status endpoint - unprotected (only returns {enabled, port})
	mux.HandleFunc("/fe/api/mcp-servers/status", s.handleMCPStatus)

	// Apply auth middleware to all /fe/api/mcp-servers/ routes (except status above)
	authMW := s.proxyAuthMiddleware

	// CRUD + test endpoints - handle all paths under /fe/api/mcp-servers/
	mux.HandleFunc("/fe/api/mcp-servers/", authMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Route based on method and path pattern
		switch r.Method {
		case http.MethodGet:
			// GET /fe/api/mcp-servers/ → list
			// GET /fe/api/mcp-servers/{id} → get
			if path == "/fe/api/mcp-servers/" {
				s.handleListMCPServers(w, r)
			} else if strings.HasSuffix(path, "/test") {
				s.handleTestMCPServer(w, r)
			} else {
				s.handleGetMCPServer(w, r)
			}

		case http.MethodPost:
			// POST /fe/api/mcp-servers/ → create
			// POST /fe/api/mcp-servers/{id}/test → test connection
			if strings.HasSuffix(path, "/test") {
				s.handleTestMCPServer(w, r)
			} else {
				s.handleCreateMCPServer(w, r)
			}

		case http.MethodPut:
			// PUT /fe/api/mcp-servers/{id} → update
			s.handleUpdateMCPServer(w, r)

		case http.MethodDelete:
			// DELETE /fe/api/mcp-servers/{id} → delete
			s.handleDeleteMCPServer(w, r)

		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	})))
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
