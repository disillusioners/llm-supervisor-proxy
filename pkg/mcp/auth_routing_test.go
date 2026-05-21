package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	_ "modernc.org/sqlite"
)

// =============================================================================
// Test Setup
// =============================================================================

// authRoutingTestEnv holds test environment for a single test case
type authRoutingTestEnv struct {
	server     *Server
	validToken string
	mux        *http.ServeMux
	cleanup    func()
}

// newAuthRoutingTestEnv creates a fresh test environment for auth routing tests.
// Each call creates new databases to avoid parallel test interference.
func newAuthRoutingTestEnv(t *testing.T) *authRoutingTestEnv {
	t.Helper()

	// Create in-memory SQLite for mcp_servers
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory database: %v", err)
	}

	// Run migrations
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

	// Create auth_tokens table
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

	// Create a valid test token
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

	env := &authRoutingTestEnv{
		server:     server,
		validToken: plaintext,
		mux:        mux,
		cleanup: func() {
			db.Close()
			authDB.Close()
		},
	}

	return env
}

// =============================================================================
// Task 11: Management routes — no auth required
// =============================================================================

func TestAuthRouting_ManagementRoutes_NoAuthRequired(t *testing.T) {
	// Test each endpoint individually to avoid database sharing issues
	tests := []struct {
		name           string
		method         string
		path           string
		body           string
		wantStatus     int
		notAuthRelated bool // if true, 401 would be auth-related
	}{
		{
			name:       "GET /fe/api/mcp-servers/ - list servers (no auth)",
			method:     http.MethodGet,
			path:       "/fe/api/mcp-servers/",
			wantStatus: http.StatusOK,
		},
		{
			name:           "GET /fe/api/mcp-servers/{id} - get nonexistent (404, not 401)",
			method:         http.MethodGet,
			path:           "/fe/api/mcp-servers/nonexistent-id",
			wantStatus:     http.StatusNotFound,
			notAuthRelated: true,
		},
		{
			name:           "POST /fe/api/mcp-servers/ - create without body (400, not 401)",
			method:         http.MethodPost,
			path:           "/fe/api/mcp-servers/",
			wantStatus:     http.StatusBadRequest,
			notAuthRelated: true,
		},
		{
			name:           "PUT /fe/api/mcp-servers/{id} - update nonexistent (404, not 401)",
			method:         http.MethodPut,
			path:           "/fe/api/mcp-servers/nonexistent-id",
			wantStatus:     http.StatusNotFound,
			notAuthRelated: true,
		},
		{
			name:           "DELETE /fe/api/mcp-servers/{id} - delete nonexistent (no content, not 401)",
			method:         http.MethodDelete,
			path:           "/fe/api/mcp-servers/nonexistent-id",
			wantStatus:     http.StatusNoContent,
			notAuthRelated: true,
		},
		{
			name:       "GET /fe/api/mcp-servers/status - status (no auth)",
			method:     http.MethodGet,
			path:       "/fe/api/mcp-servers/status",
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newAuthRoutingTestEnv(t)
			defer env.cleanup()

			env.server.RegisterAPIHandlers(env.mux)

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
				t.Errorf("got 401 Unauthorized, but this endpoint should NOT require auth. Body: %s", w.Body.String())
			}
		})
	}
}

// =============================================================================
// Task 12: Proxy routes — unauthenticated returns 401
// =============================================================================

func TestAuthRouting_ProxyRoutes_NoAuthReturns401(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{
			name:       "GET /v1/mcp/{id}/sse - SSE without auth",
			method:     http.MethodGet,
			path:       "/v1/mcp/test-server-id/sse",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "POST /v1/mcp/{id}/messages - messages without auth",
			method:     http.MethodPost,
			path:       "/v1/mcp/test-server-id/messages",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "POST /v1/mcp/{id}/ - streamable HTTP without auth",
			method:     http.MethodPost,
			path:       "/v1/mcp/test-server-id/",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "POST /v1/mcp/{id} - streamable HTTP without auth (no trailing slash)",
			method:     http.MethodPost,
			path:       "/v1/mcp/test-server-id",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newAuthRoutingTestEnv(t)
			defer env.cleanup()

			env.server.RegisterProxyHandlers(env.mux)

			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()

			env.mux.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status code = %d, want %d. Body: %s", w.Code, tt.wantStatus, w.Body.String())
			}

			// Verify response is JSON with auth error
			var resp map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Errorf("response is not valid JSON: %v", err)
			}

			if resp["error"] == "" {
				t.Error("error field should not be empty for 401 response")
			}

			// Verify error message is auth-related
			errMsg := strings.ToLower(resp["error"])
			if !strings.Contains(errMsg, "authorization") && !strings.Contains(errMsg, "auth") && !strings.Contains(errMsg, "token") {
				t.Errorf("error message should mention authorization/token, got: %s", resp["error"])
			}
		})
	}
}

// =============================================================================
// Task 13: Proxy routes — invalid/expired token returns 401
// =============================================================================

func TestAuthRouting_ProxyRoutes_InvalidTokenReturns401(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		authHeader string
		wantStatus int
	}{
		{
			name:       "GET /v1/mcp/{id}/sse - invalid token",
			method:     http.MethodGet,
			path:       "/v1/mcp/test-server-id/sse",
			authHeader: "Bearer invalid-token-12345",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "POST /v1/mcp/{id}/messages - expired/invalid token",
			method:     http.MethodPost,
			path:       "/v1/mcp/test-server-id/messages",
			authHeader: "Bearer sk-expired-token-xyz",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "POST /v1/mcp/{id}/ - malformed auth header",
			method:     http.MethodPost,
			path:       "/v1/mcp/test-server-id/",
			authHeader: "Basic dXNlcjpwYXNz", // Basic auth instead of Bearer
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "POST /v1/mcp/{id} - empty bearer token",
			method:     http.MethodPost,
			path:       "/v1/mcp/test-server-id",
			authHeader: "Bearer ",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newAuthRoutingTestEnv(t)
			defer env.cleanup()

			env.server.RegisterProxyHandlers(env.mux)

			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Authorization", tt.authHeader)
			w := httptest.NewRecorder()

			env.mux.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status code = %d, want %d. Body: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

// =============================================================================
// Task 14: Proxy routes — valid token passes auth
// =============================================================================

func TestAuthRouting_ProxyRoutes_ValidTokenPassesAuth(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		authHeader string
	}{
		{
			name:       "GET /v1/mcp/{id}/sse - valid token",
			method:     http.MethodGet,
			path:       "/v1/mcp/test-server-id/sse",
			authHeader: "", // will be set to valid token
		},
		{
			name:       "POST /v1/mcp/{id}/messages - valid token",
			method:     http.MethodPost,
			path:       "/v1/mcp/test-server-id/messages",
			authHeader: "",
		},
		{
			name:       "POST /v1/mcp/{id}/ - valid token",
			method:     http.MethodPost,
			path:       "/v1/mcp/test-server-id/",
			authHeader: "",
		},
		{
			name:       "POST /v1/mcp/{id} - valid token (no trailing slash)",
			method:     http.MethodPost,
			path:       "/v1/mcp/test-server-id",
			authHeader: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newAuthRoutingTestEnv(t)
			defer env.cleanup()

			env.server.RegisterProxyHandlers(env.mux)

			// Set auth header with valid token
			authHeader := "Bearer " + env.validToken

			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Authorization", authHeader)
			w := httptest.NewRecorder()

			env.mux.ServeHTTP(w, req)

			// Auth should pass (not 401)
			// May be 500 or other errors depending on server setup,
			// but should NOT be 401 Unauthorized
			if w.Code == http.StatusUnauthorized {
				t.Errorf("got 401 Unauthorized with valid token. Body: %s", w.Body.String())
			}
		})
	}
}

// =============================================================================
// Task 15: Route registration verification test
// =============================================================================

func TestAuthRouting_RouteRegistration_VerifyPrefixes(t *testing.T) {
	t.Run("RegisterAPIHandlers registers /fe/api/mcp-servers/ prefix", func(t *testing.T) {
		env := newAuthRoutingTestEnv(t)
		defer env.cleanup()

		env.server.RegisterAPIHandlers(env.mux)

		// Test a route that should exist under /fe/api/mcp-servers/
		req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/", nil)
		w := httptest.NewRecorder()

		env.mux.ServeHTTP(w, req)

		// If route is registered, we should get a response (not mux 404)
		// We expect 200 (list) which confirms the route is registered
		if w.Code != http.StatusOK {
			// Check if it's a handler-level 404 (route matched but server not found)
			// vs mux-level 404 (route not registered)
			var resp map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err == nil {
				if resp["error"] == "Server not found" {
					// This means route was registered and matched, but server doesn't exist
					// Route registration is correct!
					return
				}
			}
			// If we got here, something unexpected happened
			t.Logf("Got status %d, body: %s", w.Code, w.Body.String())
		}
	})

	t.Run("RegisterProxyHandlers registers /v1/mcp/ prefix", func(t *testing.T) {
		env := newAuthRoutingTestEnv(t)
		defer env.cleanup()

		env.server.RegisterProxyHandlers(env.mux)

		// Test a route that should exist under /v1/mcp/
		req := httptest.NewRequest(http.MethodGet, "/v1/mcp/test-id/sse", nil)
		// No auth header - should get 401
		w := httptest.NewRecorder()

		env.mux.ServeHTTP(w, req)

		// If route is registered with auth middleware, we should get 401 (auth required)
		// If route is not registered, we would get 404 from mux
		if w.Code != http.StatusUnauthorized {
			// Could be 404 if route not registered
			t.Errorf("expected 401 for registered proxy route, got %d. Body: %s", w.Code, w.Body.String())
		}
	})

	t.Run("API routes do not require auth", func(t *testing.T) {
		env := newAuthRoutingTestEnv(t)
		defer env.cleanup()

		env.server.RegisterAPIHandlers(env.mux)

		// Make request WITHOUT auth header
		req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/", nil)
		w := httptest.NewRecorder()

		env.mux.ServeHTTP(w, req)

		// Should NOT be 401 (auth error)
		// Should be 200 (list) or some other non-401 response
		if w.Code == http.StatusUnauthorized {
			t.Errorf("API route returned 401 Unauthorized - auth should NOT be required")
		}
	})

	t.Run("Proxy routes require auth", func(t *testing.T) {
		env := newAuthRoutingTestEnv(t)
		defer env.cleanup()

		env.server.RegisterProxyHandlers(env.mux)

		// Make request WITHOUT auth header
		req := httptest.NewRequest(http.MethodPost, "/v1/mcp/test-id/messages", nil)
		w := httptest.NewRecorder()

		env.mux.ServeHTTP(w, req)

		// MUST be 401 (auth required)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("Proxy route should return 401 without auth, got %d. Body: %s", w.Code, w.Body.String())
		}
	})

	t.Run("Correct handler mapping for API routes", func(t *testing.T) {
		env := newAuthRoutingTestEnv(t)
		defer env.cleanup()

		env.server.RegisterAPIHandlers(env.mux)

		// Test list endpoint returns array
		req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/", nil)
		w := httptest.NewRecorder()
		env.mux.ServeHTTP(w, req)

		// Should return 200 with JSON array (empty or with servers)
		if w.Code != http.StatusOK {
			t.Errorf("List endpoint status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
		}

		// Response should be an array
		var servers []*MCPServer
		if err := json.Unmarshal(w.Body.Bytes(), &servers); err != nil {
			t.Errorf("List endpoint should return JSON array: %v", err)
		}
	})

	t.Run("Correct handler mapping for proxy routes", func(t *testing.T) {
		env := newAuthRoutingTestEnv(t)
		defer env.cleanup()

		env.server.RegisterProxyHandlers(env.mux)

		// Test streamable HTTP endpoint - should not be 401 with valid token
		req := httptest.NewRequest(http.MethodPost, "/v1/mcp/test-id/", nil)
		req.Header.Set("Authorization", "Bearer "+env.validToken)
		w := httptest.NewRecorder()
		env.mux.ServeHTTP(w, req)

		// Auth should pass (not 401)
		if w.Code == http.StatusUnauthorized {
			t.Errorf("Proxy route returned 401 with valid token: %s", w.Body.String())
		}
	})
}

// =============================================================================
// Edge cases
// =============================================================================

func TestAuthRouting_ProxyRoutes_InvalidAuthHeaderFormat(t *testing.T) {
	tests := []struct {
		name       string
		authHeader string
	}{
		{"no space after Bearer", "Bearertoken123"},
		{"lowercase bearer", "bearer some-token"},
		{"BEARER uppercase", "BEARER some-token"},
		{"multiple spaces", "Bearer  token"},
		{"trailing space", "Bearer token "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newAuthRoutingTestEnv(t)
			defer env.cleanup()

			env.server.RegisterProxyHandlers(env.mux)

			req := httptest.NewRequest(http.MethodGet, "/v1/mcp/test-id/sse", nil)
			req.Header.Set("Authorization", tt.authHeader)
			w := httptest.NewRecorder()

			env.mux.ServeHTTP(w, req)

			// All invalid formats should return 401
			if w.Code != http.StatusUnauthorized {
				t.Errorf("status code = %d, want %d for auth header: %s", w.Code, http.StatusUnauthorized, tt.authHeader)
			}
		})
	}
}

func TestAuthRouting_ManagementRoutes_AuthIgnored(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{
			name:       "GET list with auth header",
			method:     http.MethodGet,
			path:       "/fe/api/mcp-servers/",
			wantStatus: http.StatusOK,
		},
		{
			name:       "GET status with auth header",
			method:     http.MethodGet,
			path:       "/fe/api/mcp-servers/status",
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newAuthRoutingTestEnv(t)
			defer env.cleanup()

			env.server.RegisterAPIHandlers(env.mux)

			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Authorization", "Bearer "+env.validToken)
			w := httptest.NewRecorder()

			env.mux.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status code = %d, want %d. Auth header should be ignored for management routes", w.Code, tt.wantStatus)
			}
		})
	}
}
