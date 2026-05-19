package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
)

// maskAuthToken masks an auth token for display.
// Shows first 6 chars + "***" + last 3 chars if token is long enough,
// otherwise just "***".
func maskAuthToken(token string) string {
	if token == "" || len(token) <= 9 {
		return "***"
	}
	return token[:6] + "***" + token[len(token)-3:]
}

// maskServer masks the auth_token field in an MCPServer for API responses.
func maskServer(server *MCPServer) *MCPServer {
	if server == nil {
		return nil
	}
	masked := *server
	masked.AuthToken = maskAuthToken(server.AuthToken)
	return &masked
}

// maskServers masks the auth_token field in a slice of MCPServers.
func maskServers(servers []*MCPServer) []*MCPServer {
	if servers == nil {
		return nil
	}
	result := make([]*MCPServer, len(servers))
	for i, s := range servers {
		result[i] = maskServer(s)
	}
	return result
}

// MCPStatusResponse represents the status response for MCP server.
type MCPStatusResponse struct {
	Enabled bool `json:"enabled"`
	Port    int  `json:"port"`
}

// TestConnectionResponse represents the response for testing MCP server connection.
type TestConnectionResponse struct {
	Success   bool   `json:"success"`
	LatencyMs int64  `json:"latency_ms"`
	Transport string `json:"transport"`
	Error     string `json:"error,omitempty"`
}

// handleMCPStatus returns the MCP server status (enabled/disabled and port).
func (s *Server) handleMCPStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(MCPStatusResponse{
		Enabled: s.port > 0,
		Port:    s.port,
	})
}

// handleListMCPServers returns all MCP servers.
func (s *Server) handleListMCPServers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	ctx := r.Context()
	servers, err := s.store.ListServers(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to list servers: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(maskServers(servers))
}

// handleGetMCPServer returns a single MCP server by ID.
func (s *Server) handleGetMCPServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Extract ID from path: /fe/api/mcp-servers/{id}
	id := strings.TrimPrefix(r.URL.Path, "/fe/api/mcp-servers/")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "Missing ID")
		return
	}

	ctx := r.Context()
	server, err := s.store.GetServer(ctx, id)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get server: %v", err))
		return
	}
	if server == nil {
		writeJSONError(w, http.StatusNotFound, "Server not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(maskServer(server))
}

// handleCreateMCPServer creates a new MCP server.
func (s *Server) handleCreateMCPServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Limit request body to 64KB to prevent memory exhaustion attacks
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	var req CreateMCPServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid JSON: %v", err))
		return
	}

	// Validate the request
	if err := ValidateMCPServerConfig(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Validation failed: %v", err))
		return
	}

	ctx := r.Context()
	server, err := s.store.CreateServer(ctx, req)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create server: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(maskServer(server))
}

// handleUpdateMCPServer updates an existing MCP server.
func (s *Server) handleUpdateMCPServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Extract ID from path: /fe/api/mcp-servers/{id}
	id := strings.TrimPrefix(r.URL.Path, "/fe/api/mcp-servers/")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "Missing ID")
		return
	}

	ctx := r.Context()

	// Fetch existing server first
	existing, err := s.store.GetServer(ctx, id)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get server: %v", err))
		return
	}
	if existing == nil {
		writeJSONError(w, http.StatusNotFound, "Server not found")
		return
	}

	// Limit request body to 64KB to prevent memory exhaustion attacks
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	var req UpdateMCPServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid JSON: %v", err))
		return
	}

	// Validate the update request
	if err := ValidateUpdateMCPServerConfig(&req, existing); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Validation failed: %v", err))
		return
	}

	server, err := s.store.UpdateServer(ctx, id, req)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to update server: %v", err))
		return
	}
	if server == nil {
		writeJSONError(w, http.StatusNotFound, "Server not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(maskServer(server))
}

// handleDeleteMCPServer deletes an MCP server.
func (s *Server) handleDeleteMCPServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Extract ID from path: /fe/api/mcp-servers/{id}
	id := strings.TrimPrefix(r.URL.Path, "/fe/api/mcp-servers/")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "Missing ID")
		return
	}

	// Close any active connections for this server
	closed := s.connMgr.GetRegistry().CloseConnections(id)
	if closed > 0 {
		log.Printf("MCP: Closed %d active connection(s) for server %s", closed, id)
	}

	ctx := r.Context()
	if err := s.store.DeleteServer(ctx, id); err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to delete server: %v", err))
		return
	}

	// Publish event for deletion
	if s.bus != nil {
		s.bus.Publish(events.Event{
			Type: "mcp_server_deleted",
			Data: map[string]interface{}{"server_id": id, "connections_closed": closed},
		})
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleTestMCPServer tests the connection to an MCP server.
func (s *Server) handleTestMCPServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Extract ID from path: /fe/api/mcp-servers/{id}/test
	path := strings.TrimPrefix(r.URL.Path, "/fe/api/mcp-servers/")
	id := strings.TrimSuffix(path, "/test")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "Missing ID")
		return
	}

	ctx := r.Context()

	// Fetch server by ID
	server, err := s.store.GetServer(ctx, id)
	if err != nil {
		writeTestResponse(w, false, 0, string(server.TransportType), fmt.Sprintf("Failed to get server: %v", err))
		return
	}
	if server == nil {
		writeTestResponse(w, false, 0, "", "Server not found")
		return
	}

	// Create HTTP client with short timeout
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	startTime := time.Now()

	switch server.TransportType {
	case TransportSSE:
		// SSE: GET {upstream_url}/sse with 5s timeout
		// Verify response status 200 AND Content-Type contains text/event-stream
		endpoint := strings.TrimSuffix(server.UpstreamURL, "/") + "/sse"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			writeTestResponse(w, false, 0, string(TransportSSE), fmt.Sprintf("Failed to create request: %v", err))
			return
		}
		req.Header.Set("Accept", "text/event-stream")
		s.injectTestAuth(req, server)

		resp, err := client.Do(req)
		if err != nil {
			writeTestResponse(w, false, 0, string(TransportSSE), fmt.Sprintf("Connection failed: %v", err))
			return
		}
		defer resp.Body.Close()

		latencyMs := time.Since(startTime).Milliseconds()
		contentType := resp.Header.Get("Content-Type")

		if resp.StatusCode == http.StatusOK && strings.Contains(contentType, "text/event-stream") {
			writeTestResponse(w, true, latencyMs, string(TransportSSE), "")
		} else {
			writeTestResponse(w, false, latencyMs, string(TransportSSE),
				fmt.Sprintf("Unexpected response: status=%d, content-type=%s", resp.StatusCode, contentType))
		}

	case TransportStreamableHTTP:
		// Streamable HTTP: POST to {upstream_url} with empty body and Mcp-Session-Id header
		// Verify response status 200 or 202
		endpoint := server.UpstreamURL
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
		if err != nil {
			writeTestResponse(w, false, 0, string(TransportStreamableHTTP), fmt.Sprintf("Failed to create request: %v", err))
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Mcp-Session-Id", "") // Empty session ID for initial request
		s.injectTestAuth(req, server)

		resp, err := client.Do(req)
		if err != nil {
			writeTestResponse(w, false, 0, string(TransportStreamableHTTP), fmt.Sprintf("Connection failed: %v", err))
			return
		}
		defer resp.Body.Close()
		// Drain and close body to allow connection reuse
		io.Copy(io.Discard, resp.Body)

		latencyMs := time.Since(startTime).Milliseconds()

		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
			writeTestResponse(w, true, latencyMs, string(TransportStreamableHTTP), "")
		} else {
			writeTestResponse(w, false, latencyMs, string(TransportStreamableHTTP),
				fmt.Sprintf("Unexpected status: %d", resp.StatusCode))
		}

	default:
		writeTestResponse(w, false, 0, string(server.TransportType), fmt.Sprintf("Unknown transport type: %s", server.TransportType))
	}
}

// injectTestAuth adds authentication headers for test requests.
func (s *Server) injectTestAuth(req *http.Request, server *MCPServer) {
	switch server.AuthType {
	case AuthBearer:
		req.Header.Set("Authorization", "Bearer "+server.AuthToken)
	case AuthBasic:
		req.Header.Set("Authorization", "Basic "+server.AuthToken)
	case AuthAPIKey:
		req.Header.Set("X-API-Key", server.AuthToken)
	case AuthNone:
		// No auth headers
	}

	// Inject custom headers
	if server.Headers != "" && server.Headers != "{}" {
		var headers map[string]string
		if err := json.Unmarshal([]byte(server.Headers), &headers); err == nil {
			for key, value := range headers {
				// Skip blocked headers
				lowerKey := strings.ToLower(key)
				if blockedHeaders[lowerKey] {
					continue
				}
				req.Header.Set(key, value)
			}
		}
	}
}

// writeTestResponse writes a test connection response.
func writeTestResponse(w http.ResponseWriter, success bool, latencyMs int64, transport string, errMsg string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(TestConnectionResponse{
		Success:   success,
		LatencyMs: latencyMs,
		Transport: transport,
		Error:     errMsg,
	})
}
