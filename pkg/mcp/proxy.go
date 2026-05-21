package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ConnectionRegistry tracks active connections per server for clean shutdown
type ConnectionRegistry struct {
	mu       sync.Mutex
	registry map[string][]context.CancelFunc
}

// NewConnectionRegistry creates a new connection registry
func NewConnectionRegistry() *ConnectionRegistry {
	return &ConnectionRegistry{
		registry: make(map[string][]context.CancelFunc),
	}
}

// Register adds a cancel function for a server
func (r *ConnectionRegistry) Register(serverID string, cancel context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.registry[serverID] = append(r.registry[serverID], cancel)
}

// Unregister removes a specific cancel function by pointer comparison
func (r *ConnectionRegistry) Unregister(serverID string, cancel context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cancels, ok := r.registry[serverID]
	if !ok {
		return
	}

	// Find and remove by pointer comparison
	for i, fn := range cancels {
		// Compare function pointers
		if fmt.Sprintf("%p", fn) == fmt.Sprintf("%p", cancel) {
			// Remove from slice
			r.registry[serverID] = append(cancels[:i], cancels[i+1:]...)
			return
		}
	}
}

// CloseConnections cancels all contexts for a server and returns the count closed
func (r *ConnectionRegistry) CloseConnections(serverID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	cancels, ok := r.registry[serverID]
	if !ok {
		return 0
	}

	count := len(cancels)
	for _, cancel := range cancels {
		cancel()
	}

	delete(r.registry, serverID)
	return count
}

// CloseAll cancels all contexts for all servers
func (r *ConnectionRegistry) CloseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, cancels := range r.registry {
		for _, cancel := range cancels {
			cancel()
		}
	}
	r.registry = make(map[string][]context.CancelFunc)
}

// connectionManagerImpl is the internal implementation with actual fields
// Note: The ConnectionManager struct in mcp.go is a placeholder from Phase 1.
// For Phase 2, the placeholder must be removed or replaced with this implementation.
// The Server.connMgr field should be updated to use *connectionManagerImpl.
type connectionManagerImpl struct {
	registry   *ConnectionRegistry
	httpClient *http.Client
}

// NewConnectionManager creates a new connection manager with sensible defaults.
// Returns *connectionManagerImpl which provides:
// - ConnectUpstream: establishes SSE connections to upstream MCP servers
// - ForwardHTTPRequest: forwards HTTP requests with auth injection
// - GetRegistry: returns the connection registry for tracking active connections
func NewConnectionManager() *connectionManagerImpl {
	return &connectionManagerImpl{
		registry: NewConnectionRegistry(),
		httpClient: &http.Client{
			// No read timeout for SSE stream support
			// Context deadlines handle cancellation properly
			Transport: &http.Transport{
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   100,
				IdleConnTimeout:       5 * time.Minute,
				ResponseHeaderTimeout: 30 * time.Second, // Timeout waiting for response headers
			},
		},
	}
}

// GetRegistry returns the connection registry for tracking active connections
func (m *connectionManagerImpl) GetRegistry() *ConnectionRegistry {
	return m.registry
}

// getUpstreamURL returns the upstream URL for a server
func (m *connectionManagerImpl) getUpstreamURL(server *MCPServer) string {
	return server.UpstreamURL
}

// injectAuth adds authentication headers based on server.AuthType
func (m *connectionManagerImpl) injectAuth(req *http.Request, server *MCPServer) {
	switch server.AuthType {
	case AuthBearer:
		req.Header.Set("Authorization", "Bearer "+server.AuthToken)
	case AuthBasic:
		encoded := base64.StdEncoding.EncodeToString([]byte(server.AuthToken))
		req.Header.Set("Authorization", "Basic "+encoded)
	case AuthAPIKey:
		req.Header.Set("X-API-Key", server.AuthToken)
	case AuthNone:
		// No auth headers added
	}

	// Inject custom headers from server.Headers (JSON map)
	if server.Headers != "" && server.Headers != "{}" {
		var headers map[string]string
		if err := json.Unmarshal([]byte(server.Headers), &headers); err == nil {
			for key, value := range headers {
				// Filter through blockedHeaders from validation.go
				if blockedHeaders[strings.ToLower(key)] {
					continue
				}
				req.Header.Set(key, value)
			}
		}
	}
}

// ConnectUpstream establishes an SSE connection to the upstream MCP server
func (m *connectionManagerImpl) ConnectUpstream(ctx context.Context, server *MCPServer) (*http.Response, error) {
	upstreamURL := m.getUpstreamURL(server)

	// Build SSE endpoint URL based on transport type
	var endpoint string
	switch server.TransportType {
	case TransportSSE:
		// SSE endpoint typically uses /sse path
		endpoint = strings.TrimSuffix(upstreamURL, "/") + "/sse"
	case TransportStreamableHTTP:
		// Streamable HTTP uses the base URL
		endpoint = upstreamURL
	default:
		endpoint = upstreamURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create upstream request: %w", err)
	}

	// Inject auth headers
	m.injectAuth(req, server)

	// Set SSE-specific headers
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")

	return m.httpClient.Do(req)
}

// ForwardHTTPRequest forwards an HTTP request to the upstream MCP server
func (m *connectionManagerImpl) ForwardHTTPRequest(ctx context.Context, server *MCPServer, r *http.Request) (*http.Response, error) {
	upstreamURL := m.getUpstreamURL(server)

	// C1: Strip /mcp/{id} prefix from path before constructing upstream URL
	// Client POSTs to /mcp/{id}/messages, upstream expects /messages
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	var upstreamPath string
	if len(parts) >= 2 && parts[0] == "mcp" && len(parts[1]) > 0 {
		// /mcp/{id}/messages → /messages, /mcp/{id}/ → /, /mcp/{id} → /
		if len(parts) == 2 {
			// /mcp/{id} → /
			upstreamPath = "/"
		} else {
			// /mcp/{id}/... → /...
			upstreamPath = "/" + strings.Join(parts[2:], "/")
		}
	} else {
		upstreamPath = path
	}

	// Build the full URL
	url := strings.TrimSuffix(upstreamURL, "/") + upstreamPath
	if r.URL.RawQuery != "" {
		url += "?" + r.URL.RawQuery
	}

	// Create new request, preserving method and body
	req, err := http.NewRequestWithContext(ctx, r.Method, url, r.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to create upstream request: %w", err)
	}

	// Copy headers from original request
	for key, values := range r.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// C3: Strip dangerous headers that should not be forwarded to upstream
	// (e.g., client's Authorization, Host, Content-Length, etc.)
	for k := range req.Header {
		if blockedHeaders[strings.ToLower(k)] {
			req.Header.Del(k)
		}
	}

	// Inject auth headers (overrides any client-supplied auth)
	m.injectAuth(req, server)

	return m.httpClient.Do(req)
}
