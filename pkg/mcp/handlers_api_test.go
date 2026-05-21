package mcp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// =============================================================================
// Test Helpers
// =============================================================================

// setupAPITestEnv creates a test Server for API handler testing.
// Returns the server, MCPStore, valid auth token, and a cleanup function.
// Note: Uses newTestDB from store_test.go (same package = white-box testing).
func setupAPITestEnv(t *testing.T) (*Server, *MCPStore, string, func()) {
	t.Helper()

	// Create in-memory SQLite (using helper from store_test.go)
	db, cleanup := newTestDB(t)

	// Create MCPStore
	mcpStore := NewMCPStore(db.DB, database.SQLite)

	bus := events.NewBus()

	// Create auth token store
	authDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		cleanup()
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
		authDB.Close()
		cleanup()
		t.Fatalf("failed to create auth_tokens table: %v", err)
	}

	tokenStore := auth.NewTokenStore(authDB, database.SQLite)

	// Create a test token
	plaintext, _, err := tokenStore.CreateToken(context.Background(), "api-test-token", nil, "api-test", false, "", nil)
	if err != nil {
		authDB.Close()
		cleanup()
		t.Fatalf("failed to create test token: %v", err)
	}

	server := &Server{
		store:      mcpStore,
		bus:        bus,
		tokenStore: tokenStore,
		connMgr:    NewConnectionManager(),
	}

	fullCleanup := func() {
		cleanup()
		authDB.Close()
	}

	return server, mcpStore, plaintext, fullCleanup
}

// =============================================================================
// Status Endpoint Tests
// =============================================================================

func TestHandleMCPStatus_WithStore(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	// Server already has store set from setupAPITestEnv
	req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/status", nil)
	w := httptest.NewRecorder()

	server.handleMCPStatus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusOK)
	}

	var resp MCPStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if !resp.Enabled {
		t.Error("resp.Enabled should be true when store is set")
	}
}

func TestHandleMCPStatus_WithoutStore(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	server.store = nil

	req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/status", nil)
	w := httptest.NewRecorder()

	server.handleMCPStatus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusOK)
	}

	var resp MCPStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Enabled {
		t.Error("resp.Enabled should be false when store is nil")
	}
}

func TestHandleMCPStatus_WrongMethod(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	tests := []struct {
		name   string
		method string
	}{
		{"POST", http.MethodPost},
		{"PUT", http.MethodPut},
		{"DELETE", http.MethodDelete},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/fe/api/mcp-servers/status", nil)
			w := httptest.NewRecorder()

			server.handleMCPStatus(w, req)

			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("status code = %d, want %d", w.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

// =============================================================================
// List Servers Tests
// =============================================================================

func TestHandleListMCPServers_ReturnsServers(t *testing.T) {
	server, store, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	ctx := context.Background()

	// Create some servers
	for i := 0; i < 3; i++ {
		_, err := store.CreateServer(ctx, CreateMCPServerRequest{
			ID:          fmt.Sprintf("list-test-server-%d", i),
			Name:        fmt.Sprintf("server-%d", i),
			UpstreamURL: "https://api.example.com/mcp",
			AuthType:    AuthNone,
		})
		if err != nil {
			t.Fatalf("failed to create server: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/", nil)
	w := httptest.NewRecorder()

	server.handleListMCPServers(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusOK)
	}

	var servers []*MCPServer
	if err := json.Unmarshal(w.Body.Bytes(), &servers); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(servers) != 3 {
		t.Errorf("len(servers) = %d, want 3", len(servers))
	}
}

func TestHandleListMCPServers_Empty(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/", nil)
	w := httptest.NewRecorder()

	server.handleListMCPServers(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusOK)
	}

	var servers []*MCPServer
	if err := json.Unmarshal(w.Body.Bytes(), &servers); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(servers) != 0 {
		t.Errorf("len(servers) = %d, want 0", len(servers))
	}
}

func TestHandleListMCPServers_MasksTokens(t *testing.T) {
	server, store, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	ctx := context.Background()

	_, err := store.CreateServer(ctx, CreateMCPServerRequest{
		ID:          "server-with-token",
		Name:        "server-with-token",
		UpstreamURL: "https://api.example.com/mcp",
		AuthType:    AuthBearer,
		AuthToken:   "my-secret-token-abc",
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/", nil)
	w := httptest.NewRecorder()

	server.handleListMCPServers(w, req)

	var servers []*MCPServer
	if err := json.Unmarshal(w.Body.Bytes(), &servers); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	// Verify token is masked: first 6 + "***" + last 3
	// "my-secret-token-abc"[:6] = "my-sec", last 3 = "abc"
	expectedMask := "my-sec***abc"
	if servers[0].AuthToken != expectedMask {
		t.Errorf("AuthToken = %q, want %q (masked)", servers[0].AuthToken, expectedMask)
	}
}

func TestHandleListMCPServers_WrongMethod(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/", nil)
	w := httptest.NewRecorder()

	server.handleListMCPServers(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

// =============================================================================
// Get Server Tests
// =============================================================================

func TestHandleGetMCPServer_Existing(t *testing.T) {
	server, store, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	ctx := context.Background()

	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		ID:          "test-server",
		Name:        "test-server",
		UpstreamURL: "https://api.example.com/mcp",
		AuthType:    AuthBearer,
		AuthToken:   "secret-token-xyz",
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/"+created.ID, nil)
	w := httptest.NewRecorder()

	server.handleGetMCPServer(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusOK)
	}

	var serverResp MCPServer
	if err := json.Unmarshal(w.Body.Bytes(), &serverResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if serverResp.Name != "test-server" {
		t.Errorf("Name = %q, want %q", serverResp.Name, "test-server")
	}

	// Verify token is masked
	// "secret-token-xyz"[:6] = "secret", last 3 = "xyz"
	expectedMask := "secret***xyz"
	if serverResp.AuthToken != expectedMask {
		t.Errorf("AuthToken = %q, want %q (masked)", serverResp.AuthToken, expectedMask)
	}
}

func TestHandleGetMCPServer_NotFound(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/nonexistent-id", nil)
	w := httptest.NewRecorder()

	server.handleGetMCPServer(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleGetMCPServer_MissingID(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	// Path without ID: /fe/api/mcp-servers/ with trailing slash but no ID
	req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/", nil)
	w := httptest.NewRecorder()

	server.handleGetMCPServer(w, req)

	// Should return bad request for missing ID
	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleGetMCPServer_WrongMethod(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/some-id", nil)
	w := httptest.NewRecorder()

	server.handleGetMCPServer(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

// =============================================================================
// Create Server Tests
// =============================================================================

func TestHandleCreateMCPServer_Success(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	reqBody := CreateMCPServerRequest{
		ID:            "new-server",
		Name:          "new-server",
		Description:   "A new test server",
		UpstreamURL:   "https://api.example.com/mcp",
		TransportType: TransportStreamableHTTP,
		AuthType:      AuthBearer,
		AuthToken:     "secret-token-abc",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleCreateMCPServer(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusCreated)
	}

	var serverResp MCPServer
	if err := json.Unmarshal(w.Body.Bytes(), &serverResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if serverResp.Name != "new-server" {
		t.Errorf("Name = %q, want %q", serverResp.Name, "new-server")
	}

	if serverResp.Description != "A new test server" {
		t.Errorf("Description = %q, want %q", serverResp.Description, "A new test server")
	}

	if serverResp.UpstreamURL != "https://api.example.com/mcp" {
		t.Errorf("UpstreamURL = %q, want %q", serverResp.UpstreamURL, "https://api.example.com/mcp")
	}

	// Token should be masked
	// "secret-token-abc"[:6] = "secret", last 3 = "abc"
	expectedMask := "secret***abc"
	if serverResp.AuthToken != expectedMask {
		t.Errorf("AuthToken = %q, want %q", serverResp.AuthToken, expectedMask)
	}
}

func TestHandleCreateMCPServer_EmptyName(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	reqBody := CreateMCPServerRequest{
		ID:          "test-server",
		Name:        "",
		UpstreamURL: "https://api.example.com/mcp",
		AuthType:   AuthNone,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleCreateMCPServer(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleCreateMCPServer_InvalidURL(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	tests := []struct {
		name       string
		upstreamURL string
	}{
		{"localhost URL", "http://localhost:8080/mcp"},
		{"invalid URL", "not-a-url"},
		{"missing scheme", "api.example.com/mcp"},
		{"private IP", "http://192.168.1.1/mcp"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqBody := CreateMCPServerRequest{
				ID:          "test-server",
				Name:        "test-server",
				UpstreamURL: tt.upstreamURL,
				AuthType:   AuthNone,
			}
			body, _ := json.Marshal(reqBody)

			req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			server.handleCreateMCPServer(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status code = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestHandleCreateMCPServer_InvalidTransportType(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	reqBody := CreateMCPServerRequest{
		ID:            "test-server",
		Name:          "test-server",
		UpstreamURL:   "https://api.example.com/mcp",
		TransportType: "invalid_transport",
		AuthType:      AuthNone,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleCreateMCPServer(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleCreateMCPServer_MissingTokenForAuth(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	reqBody := CreateMCPServerRequest{
		ID:          "test-server",
		Name:        "test-server",
		UpstreamURL: "https://api.example.com/mcp",
		AuthType:   AuthBearer,
		// AuthToken is missing
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleCreateMCPServer(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleCreateMCPServer_InvalidJSON(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/", bytes.NewReader([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleCreateMCPServer(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleCreateMCPServer_DuplicateID(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	// First, create a server with ID "test-dup"
	reqBody := CreateMCPServerRequest{
		ID:            "test-dup",
		Name:          "test-dup",
		UpstreamURL:   "https://api.example.com/mcp",
		TransportType: TransportStreamableHTTP,
		AuthType:      AuthNone,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleCreateMCPServer(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("First create: status code = %d, want %d", w.Code, http.StatusCreated)
	}

	// Attempt to create another server with the same ID
	req2Body := CreateMCPServerRequest{
		ID:            "test-dup",
		Name:          "test-dup-2",
		UpstreamURL:   "https://api.example.com/mcp",
		TransportType: TransportStreamableHTTP,
		AuthType:      AuthNone,
	}
	body2, _ := json.Marshal(req2Body)

	req2 := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()

	server.handleCreateMCPServer(w2, req2)

	// Should return 409 Conflict
	if w2.Code != http.StatusConflict {
		t.Errorf("Second create with duplicate ID: status code = %d, want %d", w2.Code, http.StatusConflict)
	}
}

func TestHandleCreateMCPServer_WrongMethod(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/", nil)
	w := httptest.NewRecorder()

	server.handleCreateMCPServer(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

// =============================================================================
// Update Server Tests
// =============================================================================

func TestHandleUpdateMCPServer_Success(t *testing.T) {
	server, store, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	ctx := context.Background()

	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		ID:          "original-name",
		Name:        "original-name",
		Description: "Original description",
		UpstreamURL: "https://api.example.com/mcp",
		AuthType:    AuthNone,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	newName := "updated-name"
	newDesc := "Updated description"
	updateReq := UpdateMCPServerRequest{
		Name:        &newName,
		Description: &newDesc,
	}
	body, _ := json.Marshal(updateReq)

	req := httptest.NewRequest(http.MethodPut, "/fe/api/mcp-servers/"+created.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleUpdateMCPServer(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusOK)
	}

	var serverResp MCPServer
	if err := json.Unmarshal(w.Body.Bytes(), &serverResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if serverResp.Name != "updated-name" {
		t.Errorf("Name = %q, want %q", serverResp.Name, "updated-name")
	}

	if serverResp.Description != "Updated description" {
		t.Errorf("Description = %q, want %q", serverResp.Description, "Updated description")
	}
}

func TestHandleUpdateMCPServer_NotFound(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	newName := "updated-name"
	updateReq := UpdateMCPServerRequest{
		Name: &newName,
	}
	body, _ := json.Marshal(updateReq)

	req := httptest.NewRequest(http.MethodPut, "/fe/api/mcp-servers/nonexistent-id", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleUpdateMCPServer(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleUpdateMCPServer_InvalidURL(t *testing.T) {
	server, store, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	ctx := context.Background()

	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		ID:          "test-server",
		Name:        "test-server",
		UpstreamURL: "https://api.example.com/mcp",
		AuthType:    AuthNone,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	newURL := "http://localhost:8080/mcp"
	updateReq := UpdateMCPServerRequest{
		UpstreamURL: &newURL,
	}
	body, _ := json.Marshal(updateReq)

	req := httptest.NewRequest(http.MethodPut, "/fe/api/mcp-servers/"+created.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleUpdateMCPServer(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleUpdateMCPServer_EmptyName(t *testing.T) {
	server, store, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	ctx := context.Background()

	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		ID:          "test-server",
		Name:        "test-server",
		UpstreamURL: "https://api.example.com/mcp",
		AuthType:    AuthNone,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	emptyName := ""
	updateReq := UpdateMCPServerRequest{
		Name: &emptyName,
	}
	body, _ := json.Marshal(updateReq)

	req := httptest.NewRequest(http.MethodPut, "/fe/api/mcp-servers/"+created.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleUpdateMCPServer(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleUpdateMCPServer_InvalidJSON(t *testing.T) {
	server, store, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	ctx := context.Background()

	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		ID:          "test-server",
		Name:        "test-server",
		UpstreamURL: "https://api.example.com/mcp",
		AuthType:    AuthNone,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/fe/api/mcp-servers/"+created.ID, bytes.NewReader([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleUpdateMCPServer(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleUpdateMCPServer_WrongMethod(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/some-id", nil)
	w := httptest.NewRecorder()

	server.handleUpdateMCPServer(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

// =============================================================================
// Delete Server Tests
// =============================================================================

func TestHandleDeleteMCPServer_Success(t *testing.T) {
	server, store, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	ctx := context.Background()

	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		ID:          "delete-me",
		Name:        "delete-me",
		UpstreamURL: "https://api.example.com/mcp",
		AuthType:    AuthNone,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/fe/api/mcp-servers/"+created.ID, nil)
	w := httptest.NewRecorder()

	server.handleDeleteMCPServer(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusNoContent)
	}

	// Verify server is deleted
	retrieved, err := store.GetServer(ctx, created.ID)
	if retrieved != nil {
		t.Error("expected server to be deleted, but it still exists")
	}
}

func TestHandleDeleteMCPServer_NotFound(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodDelete, "/fe/api/mcp-servers/nonexistent-id", nil)
	w := httptest.NewRecorder()

	server.handleDeleteMCPServer(w, req)

	// Delete returns no content even for not found (consistent with store behavior)
	if w.Code != http.StatusNoContent {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestHandleDeleteMCPServer_WrongMethod(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/some-id", nil)
	w := httptest.NewRecorder()

	server.handleDeleteMCPServer(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

// =============================================================================
// Test Connection Tests
// =============================================================================

func TestHandleTestMCPServer_SSE_Success(t *testing.T) {
	server, store, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	ctx := context.Background()

	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		ID:            "sse-server",
		Name:          "sse-server",
		UpstreamURL:   "https://api.example.com/mcp",
		TransportType: TransportSSE,
		AuthType:      AuthNone,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	// Create mock upstream server that returns SSE content type
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Don't send any data - just close
	}))
	defer mockUpstream.Close()

	// Update server URL to mock server
	updatedURL := mockUpstream.URL + "/sse"
	newURL := updatedURL
	_, err = store.UpdateServer(ctx, created.ID, UpdateMCPServerRequest{
		UpstreamURL: &newURL,
	})
	if err != nil {
		t.Fatalf("failed to update server URL: %v", err)
	}

	// Need to reload the server from the test server instance
	server.store = store

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/"+created.ID+"/test", nil)
	w := httptest.NewRecorder()

	server.handleTestMCPServer(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusOK)
	}

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if !resp.Success {
		t.Errorf("Success = false, want true. Error: %s", resp.Error)
	}

	if resp.Transport != string(TransportSSE) {
		t.Errorf("Transport = %q, want %q", resp.Transport, TransportSSE)
	}

	if resp.LatencyMs < 0 {
		t.Errorf("LatencyMs = %d, want >= 0", resp.LatencyMs)
	}
}

func TestHandleTestMCPServer_StreamableHTTP_Success(t *testing.T) {
	server, store, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	ctx := context.Background()

	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		ID:            "http-server",
		Name:          "http-server",
		UpstreamURL:   "https://api.example.com/mcp",
		TransportType: TransportStreamableHTTP,
		AuthType:      AuthNone,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	// Create mock upstream server that returns 200
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mockUpstream.Close()

	// Update server URL to mock server
	newURL := mockUpstream.URL
	_, err = store.UpdateServer(ctx, created.ID, UpdateMCPServerRequest{
		UpstreamURL: &newURL,
	})
	if err != nil {
		t.Fatalf("failed to update server URL: %v", err)
	}

	server.store = store

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/"+created.ID+"/test", nil)
	w := httptest.NewRecorder()

	server.handleTestMCPServer(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusOK)
	}

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if !resp.Success {
		t.Errorf("Success = false, want true. Error: %s", resp.Error)
	}

	if resp.Transport != string(TransportStreamableHTTP) {
		t.Errorf("Transport = %q, want %q", resp.Transport, TransportStreamableHTTP)
	}
}

func TestHandleTestMCPServer_Unreachable(t *testing.T) {
	server, store, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	ctx := context.Background()

	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		ID:            "unreachable-server",
		Name:          "unreachable-server",
		UpstreamURL:   "https://api.unreachable.example.com:9999/mcp",
		TransportType: TransportStreamableHTTP,
		AuthType:      AuthNone,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/"+created.ID+"/test", nil)
	w := httptest.NewRecorder()

	server.handleTestMCPServer(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusOK)
	}

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Success {
		t.Error("Success = true, want false for unreachable server")
	}

	if resp.Error == "" {
		t.Error("Error should not be empty for unreachable server")
	}
}

func TestHandleTestMCPServer_NotFound(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/nonexistent-id/test", nil)
	w := httptest.NewRecorder()

	server.handleTestMCPServer(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusOK)
	}

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Success {
		t.Error("Success = true, want false for not found server")
	}

	if resp.Error != "Server not found" {
		t.Errorf("Error = %q, want %q", resp.Error, "Server not found")
	}
}

func TestHandleTestMCPServer_WrongMethod(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/some-id/test", nil)
	w := httptest.NewRecorder()

	server.handleTestMCPServer(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

// =============================================================================
// Token Masking Tests
// =============================================================================

func TestMaskAuthToken(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		expected string
	}{
		{"empty string", "", "***"},
		{"short token (5 chars)", "abcde", "***"},
		{"exactly 9 chars", "123456789", "***"},
		// "my-secret-token-abc"[:6] = "my-sec", last 3 = "abc"
		{"long token", "my-secret-token-abc", "my-sec***abc"},
		// "abcdefghijklmnop"[:6] = "abcdef", last 3 = "nop"
		{"very long token", "abcdefghijklmnop", "abcdef***nop"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := maskAuthToken(tt.token)
			if result != tt.expected {
				t.Errorf("maskAuthToken(%q) = %q, want %q", tt.token, result, tt.expected)
			}
		})
	}
}

func TestMaskServer(t *testing.T) {
	server := &MCPServer{
		ID:        "test-id",
		Name:      "test-server",
		AuthToken: "my-secret-token-xyz",
	}

	masked := maskServer(server)

	// Original should not be modified
	if server.AuthToken != "my-secret-token-xyz" {
		t.Errorf("Original server.AuthToken was modified: %q", server.AuthToken)
	}

	// Masked should have masked token
	// "my-secret-token-xyz"[:6] = "my-sec", last 3 = "xyz"
	expected := "my-sec***xyz"
	if masked.AuthToken != expected {
		t.Errorf("masked.AuthToken = %q, want %q", masked.AuthToken, expected)
	}
}

func TestMaskServer_Nil(t *testing.T) {
	result := maskServer(nil)
	if result != nil {
		t.Errorf("maskServer(nil) = %v, want nil", result)
	}
}

func TestMaskServers(t *testing.T) {
	servers := []*MCPServer{
		{ID: "1", Name: "server1", AuthToken: "token-one-abc"},
		{ID: "2", Name: "server2", AuthToken: "token-two-xyz"},
		nil, // Test nil handling
	}

	masked := maskServers(servers)

	if len(masked) != 3 {
		t.Errorf("len(masked) = %d, want 3", len(masked))
	}

	// "token-one-abc"[:6] = "token-", last 3 = "abc"
	expected1 := "token-***abc"
	if masked[0].AuthToken != expected1 {
		t.Errorf("masked[0].AuthToken = %q, want %q", masked[0].AuthToken, expected1)
	}

	// "token-two-xyz"[:6] = "token-", last 3 = "xyz"
	expected2 := "token-***xyz"
	if masked[1].AuthToken != expected2 {
		t.Errorf("masked[1].AuthToken = %q, want %q", masked[1].AuthToken, expected2)
	}

	// nil element should remain nil
	if masked[2] != nil {
		t.Errorf("masked[2] = %v, want nil", masked[2])
	}
}

func TestMaskServers_NilSlice(t *testing.T) {
	result := maskServers(nil)
	if result != nil {
		t.Errorf("maskServers(nil) = %v, want nil", result)
	}
}

// =============================================================================
// Integration Tests (via RegisterAPIHandlers)
// =============================================================================

func TestRegisterAPIHandlers_RoutesCorrectly(t *testing.T) {
	server, store, validToken, cleanup := setupAPITestEnv(t)
	defer cleanup()

	ctx := context.Background()

	// Create a server
	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		ID:          "api-test-server",
		Name:        "api-test-server",
		UpstreamURL: "https://api.example.com/mcp",
		AuthType:    AuthNone,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	mux := http.NewServeMux()
	server.RegisterAPIHandlers(mux)

	// Helper to create authenticated request
	authReq := func(method, path string) *http.Request {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+validToken)
		return req
	}

	// Test status route (unprotected)
	req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Status route: status code = %d, want %d", w.Code, http.StatusOK)
	}

	// Test list route (requires auth)
	req = authReq(http.MethodGet, "/fe/api/mcp-servers/")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("List route: status code = %d, want %d", w.Code, http.StatusOK)
	}

	// Test get route (requires auth)
	req = authReq(http.MethodGet, "/fe/api/mcp-servers/"+created.ID)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Get route: status code = %d, want %d", w.Code, http.StatusOK)
	}

	// Test create route (requires auth)
	createReq := CreateMCPServerRequest{
		ID:            "new-api-server",
		Name:          "new-api-server",
		UpstreamURL:   "https://httpbin.org/post",
		TransportType: TransportStreamableHTTP,
		AuthType:      AuthNone,
	}
	body, _ := json.Marshal(createReq)
	req = authReq(http.MethodPost, "/fe/api/mcp-servers/")
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Errorf("Create route: status code = %d, want %d. Body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	// Test update route (requires auth)
	newName := "updated-api-server"
	updateReq := UpdateMCPServerRequest{
		Name: &newName,
	}
	body, _ = json.Marshal(updateReq)
	req = authReq(http.MethodPut, "/fe/api/mcp-servers/"+created.ID)
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Update route: status code = %d, want %d", w.Code, http.StatusOK)
	}

	// Test delete route (requires auth)
	req = authReq(http.MethodDelete, "/fe/api/mcp-servers/"+created.ID)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("Delete route: status code = %d, want %d", w.Code, http.StatusNoContent)
	}
}

// =============================================================================
// Authentication Tests
// =============================================================================

func TestRegisterAPIHandlers_RequiresAuth(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	mux := http.NewServeMux()
	server.RegisterAPIHandlers(mux)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{"GET list - no auth", http.MethodGet, "/fe/api/mcp-servers/", http.StatusUnauthorized},
		{"GET single - no auth", http.MethodGet, "/fe/api/mcp-servers/test-id", http.StatusUnauthorized},
		{"POST create - no auth", http.MethodPost, "/fe/api/mcp-servers/", http.StatusUnauthorized},
		{"PUT update - no auth", http.MethodPut, "/fe/api/mcp-servers/test-id", http.StatusUnauthorized},
		{"DELETE - no auth", http.MethodDelete, "/fe/api/mcp-servers/test-id", http.StatusUnauthorized},
		{"POST test - no auth", http.MethodPost, "/fe/api/mcp-servers/test-id/test", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != tt.wantStatus {
				t.Errorf("status code = %d, want %d. Body: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

func TestRegisterAPIHandlers_InvalidToken(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	mux := http.NewServeMux()
	server.RegisterAPIHandlers(mux)

	tests := []struct {
		name       string
		method     string
		path       string
		authHeader string
		wantStatus int
	}{
		{"GET list - invalid token", http.MethodGet, "/fe/api/mcp-servers/", "Bearer invalid-token-xyz", http.StatusUnauthorized},
		{"GET list - malformed header", http.MethodGet, "/fe/api/mcp-servers/", "Basic abc123", http.StatusUnauthorized},
		{"GET list - empty bearer", http.MethodGet, "/fe/api/mcp-servers/", "Bearer ", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Authorization", tt.authHeader)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != tt.wantStatus {
				t.Errorf("status code = %d, want %d. Body: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

func TestRegisterAPIHandlers_ValidToken(t *testing.T) {
	server, _, validToken, cleanup := setupAPITestEnv(t)
	defer cleanup()

	mux := http.NewServeMux()
	server.RegisterAPIHandlers(mux)

	// All routes should work with valid token
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{"GET list", http.MethodGet, "/fe/api/mcp-servers/", http.StatusOK},
		{"GET single", http.MethodGet, "/fe/api/mcp-servers/test-id", http.StatusNotFound}, // 404 because server doesn't exist
		{"POST test", http.MethodPost, "/fe/api/mcp-servers/test-id/test", http.StatusOK},   // Returns OK with error
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Authorization", "Bearer "+validToken)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != tt.wantStatus {
				t.Errorf("status code = %d, want %d. Body: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

func TestRegisterAPIHandlers_StatusUnprotected(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	// Store is already set from setupAPITestEnv

	mux := http.NewServeMux()
	server.RegisterAPIHandlers(mux)

	// Status endpoint should NOT require auth
	req := httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status route without auth: status code = %d, want %d", w.Code, http.StatusOK)
	}

	// Should also work WITH auth (auth doesn't hurt)
	req = httptest.NewRequest(http.MethodGet, "/fe/api/mcp-servers/status", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status route with auth: status code = %d, want %d", w.Code, http.StatusOK)
	}
}

// =============================================================================
// Timeout and CloseConnections Tests
// =============================================================================

func TestHandleTestMCPServer_Timeout(t *testing.T) {
	server, store, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	ctx := context.Background()

	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		ID:            "timeout-server",
		Name:          "timeout-server",
		UpstreamURL:   "https://api.example.com/mcp",
		TransportType: TransportSSE,
		AuthType:      AuthNone,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	// Create mock upstream server that sleeps for 6 seconds (exceeds 5s timeout)
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(6 * time.Second)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	defer mockUpstream.Close()

	// Update server URL to mock server
	updatedURL := mockUpstream.URL + "/sse"
	newURL := updatedURL
	_, err = store.UpdateServer(ctx, created.ID, UpdateMCPServerRequest{
		UpstreamURL: &newURL,
	})
	if err != nil {
		t.Fatalf("failed to update server URL: %v", err)
	}

	server.store = store

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/"+created.ID+"/test", nil)
	w := httptest.NewRecorder()

	server.handleTestMCPServer(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusOK)
	}

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Success {
		t.Error("Success = true, want false for timeout")
	}

	// Error should indicate a timeout
	if resp.Error == "" {
		t.Error("Error should not be empty for timeout")
	}
	// The error should mention timeout or context deadline exceeded
	if resp.Error != "" && !containsTimeoutError(resp.Error) {
		t.Errorf("Error = %q, want timeout-related error", resp.Error)
	}
}

// containsTimeoutError checks if the error message indicates a timeout
func containsTimeoutError(errMsg string) bool {
	return strings.Contains(errMsg, "timeout") ||
		strings.Contains(errMsg, "deadline") ||
		strings.Contains(errMsg, "context deadline exceeded") ||
		strings.Contains(errMsg, "i/o timeout")
}


func TestHandleDeleteMCPServer_ClosesConnections(t *testing.T) {
	server, store, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	ctx := context.Background()

	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		ID:            "delete-with-connections",
		Name:          "delete-with-connections",
		UpstreamURL:   "https://api.example.com/mcp",
		TransportType: TransportSSE,
		AuthType:      AuthNone,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	// Register a mock cancel function in the connection registry
	registry := server.connMgr.GetRegistry()
	cancelCalled := false
	mockCancel := func() {
		cancelCalled = true
	}
	registry.Register(created.ID, mockCancel)

	// Verify the connection is registered
	connections := getConnectionsForServer(registry, created.ID)
	if len(connections) != 1 {
		t.Fatalf("Expected 1 connection registered, got %d", len(connections))
	}

	req := httptest.NewRequest(http.MethodDelete, "/fe/api/mcp-servers/"+created.ID, nil)
	w := httptest.NewRecorder()

	server.handleDeleteMCPServer(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusNoContent)
	}

	// Verify the connection was closed
	if !cancelCalled {
		t.Error("cancelCalled should be true after delete")
	}

	// Verify the connection is no longer in the registry
	connections = getConnectionsForServer(registry, created.ID)
	if len(connections) != 0 {
		t.Errorf("Expected 0 connections after delete, got %d", len(connections))
	}
}

// getConnectionsForServer returns the cancel functions registered for a server
// This is a test helper that accesses internal state
func getConnectionsForServer(registry *ConnectionRegistry, serverID string) []context.CancelFunc {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	cancels, ok := registry.registry[serverID]
	if !ok {
		return nil
	}
	result := make([]context.CancelFunc, len(cancels))
	copy(result, cancels)
	return result
}


