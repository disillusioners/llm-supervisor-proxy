package mcp

import (
	"context"
	"net/http"
	"strings"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
)

// Server is the MCP Proxy Server
type Server struct {
	store      *MCPStore
	bus        *events.Bus
	tokenStore auth.TokenStoreInterface
	connMgr    *connectionManagerImpl
}

// NewServer creates a new MCP proxy server
func NewServer(store *MCPStore, bus *events.Bus, tokenStore auth.TokenStoreInterface) *Server {
	return &Server{
		store:      store,
		bus:        bus,
		tokenStore: tokenStore,
		connMgr:    NewConnectionManager(),
	}
}

// RegisterProxyHandlers registers MCP proxy routes on the main server mux.
// All routes require token authentication via proxyAuthMiddleware.
func (s *Server) RegisterProxyHandlers(mux *http.ServeMux) {
	if s.tokenStore == nil {
		return
	}

	authMW := s.proxyAuthMiddleware

	// SSE transport routes
	mux.HandleFunc("/v1/mcp/{id}/sse", authMW(s.handleSSEConnection))
	mux.HandleFunc("/v1/mcp/{id}/messages", authMW(s.handleSSEMessage))

	// Streamable HTTP transport routes
	mux.HandleFunc("/v1/mcp/{id}/", authMW(s.handleStreamableHTTP))
	mux.HandleFunc("/v1/mcp/{id}", authMW(s.handleStreamableHTTP))
}

// RegisterAPIHandlers registers management API routes for MCP servers.
func (s *Server) RegisterAPIHandlers(mux *http.ServeMux) {
	if s.store == nil {
		return
	}

	// Status endpoint - unprotected (only returns {enabled})
	mux.HandleFunc("/fe/api/mcp-servers/status", s.handleMCPStatus)

	// CRUD + test endpoints - handle all paths under /fe/api/mcp-servers/
	mux.HandleFunc("/fe/api/mcp-servers/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	}))
}

// Shutdown cleans up SSE connections
func (s *Server) Shutdown(ctx context.Context) error {
	if s.connMgr != nil {
		s.connMgr.GetRegistry().CloseAll()
	}
	return nil
}
