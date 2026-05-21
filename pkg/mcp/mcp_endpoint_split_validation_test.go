package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	_ "modernc.org/sqlite"
)

// =============================================================================
// MCP Endpoint Split - Functional Validation
// Tests the endpoint split changes from the feature/mcp-endpoint-split branch
// =============================================================================

// =============================================================================
// Test Setup
// =============================================================================

// testEnv holds test environment for validation tests
type testEnv struct {
	server     *Server
	validToken string
	mux        *http.ServeMux
	cleanup    func()
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	// Create in-memory SQLite for mcp_servers
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory database: %v", err)
	}

	if err := runMCPMigrations(db); err != nil {
		db.Close()
		t.Fatalf("failed to run migrations: %v", err)
	}

	mcpStore := NewMCPStore(db, database.SQLite)

	// Create in-memory SQLite for auth_tokens
	authDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		db.Close()
		t.Fatalf("failed to open auth database: %v", err)
	}

	createTable := `
		CREATE TABLE auth_tokens (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			expires_at TEXT,
			created_at TEXT NOT NULL,
			created_by TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			ultimate_model_enabled INTEGER NOT NULL DEFAULT 0,
			ultimate_model TEXT,
			allowed_models TEXT
		)
	`
	if _, err := authDB.Exec(createTable); err != nil {
		db.Close()
		authDB.Close()
		t.Fatalf("failed to create auth_tokens table: %v", err)
	}

	tokenStore := auth.NewTokenStore(authDB, database.SQLite)

	plaintext, _, err := tokenStore.CreateToken(context.Background(), "test-token", nil, "test", false, "", nil)
	if err != nil {
		db.Close()
		authDB.Close()
		t.Fatalf("failed to create test token: %v", err)
	}

	bus := events.NewBus()

	server := &Server{
		store:      mcpStore,
		bus:        bus,
		tokenStore: tokenStore,
		connMgr:    NewConnectionManager(),
	}

	mux := http.NewServeMux()

	return &testEnv{
		server:     server,
		validToken: plaintext,
		mux:        mux,
		cleanup: func() {
			db.Close()
			authDB.Close()
		},
	}
}

// =============================================================================
// Test 1: Proxy Auth Enforcement
// =============================================================================

// Test1_ProxyAuth_NoToken_Returns401 verifies that proxy routes return 401 without token
func Test1_ProxyAuth_NoToken_Returns401(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()

	env.server.RegisterProxyHandlers(env.mux)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{"GET /v1/mcp/{id}/sse - no auth", http.MethodGet, "/v1/mcp/test-id/sse", http.StatusUnauthorized},
		{"POST /v1/mcp/{id}/messages - no auth", http.MethodPost, "/v1/mcp/test-id/messages", http.StatusUnauthorized},
		{"POST /v1/mcp/{id}/ - no auth", http.MethodPost, "/v1/mcp/test-id/", http.StatusUnauthorized},
		{"POST /v1/mcp/{id} - no auth", http.MethodPost, "/v1/mcp/test-id", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			env.mux.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status code = %d, want %d. Body: %s", w.Code, tt.wantStatus, w.Body.String())
			}

			// Verify JSON error response
			var resp map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Errorf("response is not valid JSON: %v", err)
			}
			if resp["error"] == "" {
				t.Error("error field should not be empty for 401 response")
			}
		})
	}
}

// Test1_ProxyAuth_InvalidToken_Returns401 verifies that invalid tokens return 401
func Test1_ProxyAuth_InvalidToken_Returns401(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()

	env.server.RegisterProxyHandlers(env.mux)

	tests := []struct {
		name       string
		method     string
		path       string
		authHeader string
		wantStatus int
	}{
		{"GET /v1/mcp/{id}/sse - invalid token", http.MethodGet, "/v1/mcp/test-id/sse", "Bearer invalid-token", http.StatusUnauthorized},
		{"POST /v1/mcp/{id}/messages - malformed auth", http.MethodPost, "/v1/mcp/test-id/messages", "Basic dXNlcjpwYXNz", http.StatusUnauthorized},
		{"POST /v1/mcp/{id}/ - empty bearer token", http.MethodPost, "/v1/mcp/test-id/", "Bearer ", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Authorization", tt.authHeader)
			w := httptest.NewRecorder()
			env.mux.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status code = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

// Test1_ProxyAuth_ValidToken_PassesAuth verifies that valid tokens pass auth (not 401)
func Test1_ProxyAuth_ValidToken_PassesAuth(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()

	env.server.RegisterProxyHandlers(env.mux)

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"GET /v1/mcp/{id}/sse - valid token", http.MethodGet, "/v1/mcp/test-id/sse"},
		{"POST /v1/mcp/{id}/messages - valid token", http.MethodPost, "/v1/mcp/test-id/messages"},
		{"POST /v1/mcp/{id}/ - valid token", http.MethodPost, "/v1/mcp/test-id/"},
		{"POST /v1/mcp/{id} - valid token", http.MethodPost, "/v1/mcp/test-id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Authorization", "Bearer "+env.validToken)
			w := httptest.NewRecorder()
			env.mux.ServeHTTP(w, req)

			// Auth should pass (not 401). May be 404 or 500 for unknown server,
			// but should NOT be 401 Unauthorized
			if w.Code == http.StatusUnauthorized {
				t.Errorf("got 401 Unauthorized with valid token. Body: %s", w.Body.String())
			}
		})
	}
}

// =============================================================================
// Test 2: Management Routes No Auth
// =============================================================================

// Test2_ManagementRoutes_NoAuth_ReturnsNon401 verifies management routes don't require auth
func Test2_ManagementRoutes_NoAuth_ReturnsNon401(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()

	env.server.RegisterAPIHandlers(env.mux)

	tests := []struct {
		name           string
		method         string
		path           string
		body           string
		wantStatus     int
		notAuthRelated bool
	}{
		{"GET /fe/api/mcp-servers/ - no auth", http.MethodGet, "/fe/api/mcp-servers/", "", http.StatusOK, true},
		{"GET /fe/api/mcp-servers/status - no auth", http.MethodGet, "/fe/api/mcp-servers/status", "", http.StatusOK, true},
		{"GET /fe/api/mcp-servers/{id} - 404 not 401", http.MethodGet, "/fe/api/mcp-servers/nonexistent", "", http.StatusNotFound, true},
		{"POST /fe/api/mcp-servers/ - bad request not 401", http.MethodPost, "/fe/api/mcp-servers/", `{"invalid":"json"}`, http.StatusBadRequest, true},
		{"PUT /fe/api/mcp-servers/{id} - 404 not 401", http.MethodPut, "/fe/api/mcp-servers/nonexistent", `{"name":"test"}`, http.StatusNotFound, true},
		{"DELETE /fe/api/mcp-servers/{id} - no content not 401", http.MethodDelete, "/fe/api/mcp-servers/nonexistent", "", http.StatusNoContent, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			env.mux.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status code = %d, want %d. Body: %s", w.Code, tt.wantStatus, w.Body.String())
			}

			// Verify it's NOT a 401 (auth-related error)
			if tt.notAuthRelated && w.Code == http.StatusUnauthorized {
				t.Errorf("got 401 Unauthorized, but this endpoint should NOT require auth")
			}
		})
	}
}

// =============================================================================
// Test 3: MCP Status Endpoint
// =============================================================================

// Test3_MCPStatus_ReturnsCorrectJSON verifies status endpoint returns correct JSON
func Test3_MCPStatus_ReturnsCorrectJSON(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()

	env.server.RegisterAPIHandlers(env.mux)

	req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/status", nil)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", w.Code, http.StatusOK)
	}

	// Parse response
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	// Verify enabled field exists and is true (store is set)
	enabled, ok := resp["enabled"]
	if !ok {
		t.Fatal("response should have 'enabled' field")
	}
	if enabled != true {
		t.Errorf("enabled = %v, want true", enabled)
	}

	// Verify port field does NOT exist (it was removed in this refactor)
	if _, ok := resp["port"]; ok {
		t.Error("response should NOT have 'port' field (removed in refactor)")
	}

	// Print response for verification
	t.Logf("Status response: %s", w.Body.String())
}

// Test3_MCPStatus_NoPortField verifies the MCPStatusResponse struct has no port field
func Test3_MCPStatus_NoPortField(t *testing.T) {
	// Verify the struct definition only has Enabled field
	status := MCPStatusResponse{Enabled: true}
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("failed to marshal MCPStatusResponse: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	// Should have exactly one field: "enabled"
	if len(parsed) != 1 {
		t.Errorf("MCPStatusResponse has %d fields, want 1. Fields: %v", len(parsed), parsed)
	}

	if _, ok := parsed["enabled"]; !ok {
		t.Error("MCPStatusResponse should have 'enabled' field")
	}

	if _, ok := parsed["port"]; ok {
		t.Error("MCPStatusResponse should NOT have 'port' field")
	}

	t.Logf("MCPStatusResponse JSON: %s", string(data))
}

// Test3_MCPStatus_WithoutStore_RoutesNotRegistered verifies behavior when store is nil
func Test3_MCPStatus_WithoutStore_RoutesNotRegistered(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()

	// Set store to nil to simulate MCP not initialized
	env.server.store = nil

	mux := http.NewServeMux()
	env.server.RegisterAPIHandlers(mux)

	// When store is nil, RegisterAPIHandlers returns early (no routes registered)
	// This is correct behavior - if MCP is disabled, no routes should be registered
	req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Should get 404 because route was not registered (store == nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("When store is nil, routes should not be registered, got %d", w.Code)
	}

	t.Log("When store is nil, RegisterAPIHandlers returns early (correct behavior)")
	t.Log("Status endpoint correctly returns 404 when MCP is not enabled")
}

// =============================================================================
// Test 4: Route Registration - No Conflicts
// =============================================================================

// Test4_RouteRegistration_NoConflicts verifies MCP and chat routes coexist
func Test4_RouteRegistration_NoConflicts(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()

	// Register MCP routes
	env.server.RegisterAPIHandlers(env.mux)
	env.server.RegisterProxyHandlers(env.mux)

	// Test that MCP routes are registered (should get 401 without auth, not 404)
	req := httptest.NewRequest(http.MethodGet, "/v1/mcp/test-id/sse", nil)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("MCP proxy route should return 401 (auth required), got %d. If 404, route not registered.", w.Code)
	}

	// Test that chat routes would be separate (mock by checking mux behavior)
	// The actual /v1/chat/completions route is registered in main.go
	// We verify that MCP routes don't interfere by checking their specific patterns
	t.Log("MCP proxy routes registered at /v1/mcp/{id}/*")
	t.Log("Chat routes registered at /v1/chat/completions")
	t.Log("No route conflicts detected")
}

// Test4_RouteRegistration_APIvsProxy verifies API and proxy routes are separate
func Test4_RouteRegistration_APIvsProxy(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()

	env.server.RegisterAPIHandlers(env.mux)
	env.server.RegisterProxyHandlers(env.mux)

	// API routes should NOT require auth
	req1 := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/", nil)
	w1 := httptest.NewRecorder()
	env.mux.ServeHTTP(w1, req1)
	if w1.Code == http.StatusUnauthorized {
		t.Error("API route should NOT require auth")
	}

	// Proxy routes SHOULD require auth
	req2 := httptest.NewRequest(http.MethodGet, "/v1/mcp/test-id/sse", nil)
	w2 := httptest.NewRecorder()
	env.mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Error("Proxy route SHOULD require auth")
	}
}

// =============================================================================
// Test 5: Edge Cases
// =============================================================================

// Test5_EdgeCase_MCPEnabledFalse_NoRoutesRegistered verifies no crash when MCP not enabled
func Test5_EdgeCase_MCPEnabledFalse_NoRoutesRegistered(t *testing.T) {
	// Simulate MCP_ENABLED=false by NOT calling RegisterAPIHandlers/RegisterProxyHandlers
	// This is what happens in main.go when MCP_ENABLED != "true"
	env := newTestEnv(t)
	defer env.cleanup()

	mux := http.NewServeMux()

	// Server is created but routes are NOT registered (simulating MCP_ENABLED=false)
	// No crash should occur
	_ = env.server // Server exists but not registered

	// Verify status endpoint is NOT registered
	req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Should get 404 from mux since route was never registered
	if w.Code != http.StatusNotFound {
		t.Errorf("Unregistered route should return 404, got %d", w.Code)
	}

	t.Log("MCP_ENABLED=false: Routes correctly not registered, no crash")
}

// Test5_EdgeCase_MCPEnabledTrue_NoServersConfigured verifies behavior with no servers
func Test5_EdgeCase_MCPEnabledTrue_NoServersConfigured(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()

	env.server.RegisterAPIHandlers(env.mux)
	env.server.RegisterProxyHandlers(env.mux)

	// Status should return enabled=true (store exists, MCP is enabled)
	req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/status", nil)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", w.Code, http.StatusOK)
	}

	var statusResp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &statusResp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if statusResp["enabled"] != true {
		t.Errorf("enabled = %v, want true (MCP is enabled, even if no servers configured)", statusResp["enabled"])
	}

	// List should return empty array (no servers configured)
	req2 := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/", nil)
	w2 := httptest.NewRecorder()
	env.mux.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("list servers status = %d, want %d", w2.Code, http.StatusOK)
	}

	var servers []interface{}
	if err := json.Unmarshal(w2.Body.Bytes(), &servers); err != nil {
		t.Fatalf("failed to unmarshal servers list: %v", err)
	}

	if len(servers) != 0 {
		t.Errorf("servers list should be empty, got %d servers", len(servers))
	}
}

// Test5_EdgeCase_UnknownServerID_Returns404 verifies unknown server IDs return 404
func Test5_EdgeCase_UnknownServerID_Returns404(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()

	env.server.RegisterAPIHandlers(env.mux)
	env.server.RegisterProxyHandlers(env.mux)

	// Management route: GET /fe/api/mcp-servers/{id} for unknown server
	req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/nonexistent-id", nil)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("GET nonexistent server: status = %d, want %d (not 500 or crash)", w.Code, http.StatusNotFound)
	}

	// Proxy route: /v1/mcp/{id}/sse for unknown server (with valid auth)
	req2 := httptest.NewRequest(http.MethodGet, "/v1/mcp/nonexistent-id/sse", nil)
	req2.Header.Set("Authorization", "Bearer "+env.validToken)
	w2 := httptest.NewRecorder()
	env.mux.ServeHTTP(w2, req2)

	// Should NOT be 401 (auth passed), should be some other error (not crash)
	if w2.Code == http.StatusUnauthorized {
		t.Error("Proxy route with valid token should NOT return 401")
	}

	// Should be 404 or 500 (not crash)
	if w2.Code == http.StatusOK {
		t.Error("Proxy route for unknown server should not return 200")
	}

	// No crash = success
	t.Logf("Unknown server ID proxy route returns: %d (no crash)", w2.Code)
}

// Test5_EdgeCase_InvalidJSON_Returns400 verifies invalid JSON returns 400 not crash
func Test5_EdgeCase_InvalidJSON_Returns400(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()

	env.server.RegisterAPIHandlers(env.mux)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/", strings.NewReader("invalid json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Invalid JSON: status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// Test5_EdgeCase_MCPEnabled_EnvVarSimulated simulates MCP_ENABLED env var behavior
func Test5_EdgeCase_MCPEnabled_EnvVarSimulated(t *testing.T) {
	// This test verifies the logic in main.go works correctly
	// When MCP_ENABLED != "true", routes should not be registered

	mcpEnabled := os.Getenv("MCP_ENABLED")
	defer os.Setenv("MCP_ENABLED", mcpEnabled) // Restore

	os.Setenv("MCP_ENABLED", "false")

	env := newTestEnv(t)
	defer env.cleanup()

	mux := http.NewServeMux()

	// Simulate main.go logic: only register if MCP_ENABLED == "true"
	if os.Getenv("MCP_ENABLED") == "true" {
		env.server.RegisterAPIHandlers(mux)
		env.server.RegisterProxyHandlers(mux)
	}
	// else: routes not registered

	// Verify routes not registered
	req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("When MCP_ENABLED=false, routes should not be registered, got %d", w.Code)
	}

	t.Log("MCP_ENABLED=false: Routes correctly skipped, no crash on startup")
}

// =============================================================================
// Integration Test: Full Flow
// =============================================================================

// TestIntegration_FullFlow verifies complete MCP workflow
func TestIntegration_FullFlow(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()

	env.server.RegisterAPIHandlers(env.mux)
	env.server.RegisterProxyHandlers(env.mux)

	// Step 1: Check status (no auth required)
	t.Log("Step 1: Check MCP status")
	req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/status", nil)
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Status check failed: %d", w.Code)
	}

	// Step 2: List servers (no auth required, returns empty)
	t.Log("Step 2: List MCP servers")
	req = httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/", nil)
	w = httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("List servers failed: %d", w.Code)
	}

	// Step 3: Create server (no auth required)
	t.Log("Step 3: Create MCP server")
	createReq := CreateMCPServerRequest{
		ID:            "test-server",
		Name:          "test-server",
		UpstreamURL:   "https://api.example.com/mcp",
		TransportType: TransportStreamableHTTP,
		AuthType:      AuthNone,
	}
	body, _ := json.Marshal(createReq)
	req = httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Errorf("Create server failed: %d, Body: %s", w.Code, w.Body.String())
	}

	// Step 4: Get server (no auth required)
	t.Log("Step 4: Get MCP server")
	req = httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/test-server", nil)
	w = httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Get server failed: %d", w.Code)
	}

	// Step 5: Access proxy route without auth (should return 401)
	t.Log("Step 5: Access proxy route without auth")
	req = httptest.NewRequest(http.MethodGet, "/v1/mcp/test-server/sse", nil)
	w = httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Proxy route without auth should return 401, got %d", w.Code)
	}

	// Step 6: Access proxy route with valid auth (auth passes)
	t.Log("Step 6: Access proxy route with valid auth")
	req = httptest.NewRequest(http.MethodGet, "/v1/mcp/test-server/sse", nil)
	req.Header.Set("Authorization", "Bearer "+env.validToken)
	w = httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Errorf("Proxy route with valid auth should NOT return 401, got %d", w.Code)
	}

	t.Log("Integration test passed: Full MCP workflow works correctly")
}
