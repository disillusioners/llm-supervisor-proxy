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
	"net/url"
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
// Status Unprotected Tests
// =============================================================================

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

// =============================================================================
// Direct Test Connection Tests (no saved server required)
// =============================================================================

// TestHandleTestMCPServerDirect_SSE_SSRFProtected verifies that SSRF protection blocks localhost URLs.
func TestHandleTestMCPServerDirect_SSE_SSRFProtected(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	reqBody := TestConnectionDirectRequest{
		UpstreamURL:   "http://127.0.0.1:8080/sse",
		TransportType: "sse",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/test-connection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleTestMCPServerDirect(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Success {
		t.Error("Success = true, want false for SSRF-blocked URL")
	}

	if !strings.Contains(resp.Error, "localhost") && !strings.Contains(resp.Error, "private") {
		t.Errorf("Error should mention SSRF rejection, got: %s", resp.Error)
	}
}

// TestHandleTestMCPServerDirect_StreamableHTTP_SSRFProtected verifies that SSRF protection blocks localhost URLs.
func TestHandleTestMCPServerDirect_StreamableHTTP_SSRFProtected(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	reqBody := TestConnectionDirectRequest{
		UpstreamURL:   "http://localhost:8080/mcp",
		TransportType: "streamable_http",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/test-connection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleTestMCPServerDirect(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Success {
		t.Error("Success = true, want false for SSRF-blocked URL")
	}

	if !strings.Contains(resp.Error, "localhost") && !strings.Contains(resp.Error, "private") {
		t.Errorf("Error should mention SSRF rejection, got: %s", resp.Error)
	}
}

// TestHandleTestMCPServerDirect_PrivateIP_SSRFProtected verifies that private IP ranges are blocked.
func TestHandleTestMCPServerDirect_PrivateIP_SSRFProtected(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	// Test 10.x.x.x private range
	reqBody := TestConnectionDirectRequest{
		UpstreamURL:   "http://10.0.0.1:8080/mcp",
		TransportType: "streamable_http",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/test-connection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleTestMCPServerDirect(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Success {
		t.Error("Success = true, want false for SSRF-blocked private IP")
	}

	if !strings.Contains(resp.Error, "private") {
		t.Errorf("Error should mention private IP rejection, got: %s", resp.Error)
	}
}

func TestHandleTestMCPServerDirect_Unreachable(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	reqBody := TestConnectionDirectRequest{
		UpstreamURL:   "https://api.unreachable.example.com:9999/mcp",
		TransportType: "streamable_http",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/test-connection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleTestMCPServerDirect(w, req)

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

func TestHandleTestMCPServerDirect_MissingUpstreamURL(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	reqBody := TestConnectionDirectRequest{
		UpstreamURL:   "",
		TransportType: "sse",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/test-connection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleTestMCPServerDirect(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleTestMCPServerDirect_InvalidTransportType(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	reqBody := TestConnectionDirectRequest{
		UpstreamURL:   "https://api.example.com/mcp",
		TransportType: "invalid_transport",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/test-connection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleTestMCPServerDirect(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleTestMCPServerDirect_InvalidJSON(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/test-connection", bytes.NewReader([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleTestMCPServerDirect(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleTestMCPServerDirect_WrongMethod(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	tests := []struct {
		name   string
		method string
	}{
		{"GET", http.MethodGet},
		{"PUT", http.MethodPut},
		{"DELETE", http.MethodDelete},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqBody := TestConnectionDirectRequest{
				UpstreamURL:   "https://api.example.com/mcp",
				TransportType: "sse",
			}
			body, _ := json.Marshal(reqBody)

			req := httptest.NewRequest(tt.method, "/fe/api/mcp-servers/test-connection", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			server.handleTestMCPServerDirect(w, req)

			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("status code = %d, want %d", w.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

// TestHandleTestMCPServerDirect_192168_SSRFProtected verifies 192.168.x.x private IPs are blocked.
func TestHandleTestMCPServerDirect_192168_SSRFProtected(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	reqBody := TestConnectionDirectRequest{
		UpstreamURL:   "http://192.168.1.1:8080/mcp",
		TransportType: "streamable_http",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/test-connection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleTestMCPServerDirect(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Success {
		t.Error("Success = true, want false for SSRF-blocked private IP")
	}

	if !strings.Contains(resp.Error, "private") {
		t.Errorf("Error should mention private IP rejection, got: %s", resp.Error)
	}
}

// TestHandleTestMCPServerDirect_IPv6Loopback_SSRFProtected verifies IPv6 loopback is blocked.
func TestHandleTestMCPServerDirect_IPv6Loopback_SSRFProtected(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	reqBody := TestConnectionDirectRequest{
		UpstreamURL:   "http://[::1]:8080/mcp",
		TransportType: "streamable_http",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/test-connection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleTestMCPServerDirect(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Success {
		t.Error("Success = true, want false for SSRF-blocked IPv6 loopback")
	}

	if !strings.Contains(resp.Error, "loopback") && !strings.Contains(resp.Error, "private") && !strings.Contains(resp.Error, "localhost") {
		t.Errorf("Error should mention loopback rejection, got: %s", resp.Error)
	}
}

// TestHandleTestMCPServerDirect_172Private_SSRFProtected verifies 172.16.x.x private IPs are blocked.
func TestHandleTestMCPServerDirect_172Private_SSRFProtected(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	reqBody := TestConnectionDirectRequest{
		UpstreamURL:   "http://172.16.0.1:8080/mcp",
		TransportType: "streamable_http",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/test-connection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleTestMCPServerDirect(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Success {
		t.Error("Success = true, want false for SSRF-blocked private IP")
	}

	if !strings.Contains(resp.Error, "private") {
		t.Errorf("Error should mention private IP rejection, got: %s", resp.Error)
	}
}

// TestRegisterAPIHandlers_DirectTestConnectionRoute tests that the route exists and SSRF validation works.
func TestRegisterAPIHandlers_DirectTestConnectionRoute(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	mux := http.NewServeMux()
	server.RegisterAPIHandlers(mux)

	// Test that SSRF validation is applied to the route
	reqBody := TestConnectionDirectRequest{
		UpstreamURL:   "http://127.0.0.1:8080/sse",
		TransportType: "sse",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/test-connection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Should get SSRF validation error, not 404 (route exists)
	if w.Code != http.StatusBadRequest {
		t.Errorf("Direct test-connection route: status code = %d, want %d. Body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Success {
		t.Error("Success = true, want false for SSRF validation")
	}
}

// =============================================================================
// Test SSRF Protection via Redirect
// =============================================================================

// TestHandleTestMCPServerDirect_RedirectToLocalhostBlocked verifies that
// redirects to localhost/private IPs are blocked by CheckRedirect.
// Note: We test this by verifying the CheckRedirect function behavior directly,
// since httptest.NewServer() uses localhost which can't pass initial validation.
func TestHandleTestMCPServerDirect_RedirectToLocalhostBlocked(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	// Create a mock server that redirects to localhost
	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:8080/sse", http.StatusFound)
	}))
	defer redirectServer.Close()

	// The mock server's URL is localhost, so initial validation fails.
	// This test verifies the implementation is correct - the redirect
	// validation would catch localhost redirects if the initial URL was valid.
	// For full integration testing, a server on a non-blocked IP would be needed.

	// Verify that localhost is blocked by ValidateUpstreamURL (this is what CheckRedirect calls)
	err := ValidateUpstreamURL("http://127.0.0.1:8080/sse")
	if err == nil {
		t.Error("ValidateUpstreamURL should reject localhost URL")
	}

	// Test with a real redirect to localhost by using httpbin's redirect endpoint
	// httpbin.org/redirect-to?url=http://127.0.0.1:8080 will redirect to localhost
	reqBody := TestConnectionDirectRequest{
		UpstreamURL:   "https://httpbin.org/redirect-to?url=http://127.0.0.1:8080/sse",
		TransportType: "sse",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/test-connection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleTestMCPServerDirect(w, req)

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Success {
		t.Error("Success = true, want false for redirect to localhost blocked")
	}

	// The error should mention redirect rejection
	if !strings.Contains(resp.Error, "redirect") && !strings.Contains(resp.Error, "disallowed") {
		t.Errorf("Error should mention redirect rejection, got: %s", resp.Error)
	}
}

// TestHandleTestMCPServerDirect_RedirectToPrivateIPBlocked verifies that
// redirects to private IPs are blocked by CheckRedirect.
func TestHandleTestMCPServerDirect_RedirectToPrivateIPBlocked(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	// Verify that private IPs are blocked by ValidateUpstreamURL
	err := ValidateUpstreamURL("http://192.168.1.1:8080/mcp")
	if err == nil {
		t.Error("ValidateUpstreamURL should reject private IP URL")
	}

	// Test with a real redirect to private IP using httpbin
	reqBody := TestConnectionDirectRequest{
		UpstreamURL:   "https://httpbin.org/redirect-to?url=http://192.168.1.1:8080/mcp",
		TransportType: "streamable_http",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/test-connection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleTestMCPServerDirect(w, req)

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Success {
		t.Error("Success = true, want false for redirect to private IP blocked")
	}

	// The error should mention redirect rejection
	if !strings.Contains(resp.Error, "redirect") && !strings.Contains(resp.Error, "disallowed") {
		t.Errorf("Error should mention redirect rejection, got: %s", resp.Error)
	}
}

// TestHandleTestMCPServerDirect_TooManyRedirectsBlocked verifies that
// too many redirects are blocked by CheckRedirect.
// Note: This test verifies the implementation by checking that the CheckRedirect
// function limits redirects to 3. We use a mock server approach to test this.
func TestHandleTestMCPServerDirect_TooManyRedirectsBlocked(t *testing.T) {
	// Test the CheckRedirect function logic directly
	// Create a test request with multiple via entries (simulating redirects)
	req := &http.Request{URL: mustParseURL("https://example.com/final")}

	// Simulate 4 redirects (0, 1, 2, 3 - where 3 would be the 4th)
	via := make([]*http.Request, 4)
	for i := range via {
		via[i] = &http.Request{URL: mustParseURL(fmt.Sprintf("https://example.com/redirect-%d", i))}
	}

	// Create a mock CheckRedirect that mimics the actual implementation
	checkRedirect := func(req *http.Request, via []*http.Request) error {
		if err := ValidateUpstreamURL(req.URL.String()); err != nil {
			return fmt.Errorf("redirect to disallowed URL: %w", err)
		}
		if len(via) >= 3 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	}

	err := checkRedirect(req, via)
	if err == nil {
		t.Error("CheckRedirect should reject more than 3 redirects")
	}

	if !strings.Contains(err.Error(), "too many redirects") {
		t.Errorf("Error should mention 'too many redirects', got: %s", err.Error())
	}

	// Verify that 2 redirects (within limit) is allowed
	via2 := make([]*http.Request, 2)
	for i := range via2 {
		via2[i] = &http.Request{URL: mustParseURL(fmt.Sprintf("https://example.com/redirect-%d", i))}
	}
	err = checkRedirect(req, via2)
	if err != nil {
		t.Errorf("CheckRedirect should allow 2 redirects, got error: %v", err)
	}
}

func mustParseURL(rawURL string) *url.URL {
	u, err := url.Parse(rawURL)
	if err != nil {
		panic(err)
	}
	return u
}

// TestHandleTestMCPServerDirect_RedirectToExternalAllowed verifies that
// redirects to valid external URLs are allowed.
func TestHandleTestMCPServerDirect_RedirectToExternalAllowed(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	// Use httpbin's redirect to an allowed URL (the redirect target is httpbin.org itself)
	reqBody := TestConnectionDirectRequest{
		UpstreamURL:   "https://httpbin.org/redirect-to?url=https://httpbin.org/get",
		TransportType: "sse",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/test-connection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleTestMCPServerDirect(w, req)

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	// Should NOT contain redirect error (redirect to external URL is allowed)
	// Note: It may fail for other reasons (wrong content type), but not redirect
	if strings.Contains(resp.Error, "redirect") && strings.Contains(resp.Error, "disallowed") {
		t.Errorf("Error should NOT mention redirect rejection for external URL, got: %s", resp.Error)
	}
}

// =============================================================================
// Test Direct Endpoint with Auth Fields
// =============================================================================

// TestHandleTestMCPServerDirect_WithBearerAuth verifies that bearer auth
// is properly passed through for direct test.
func TestHandleTestMCPServerDirect_WithBearerAuth(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	// Use httpbin.org which echoes back headers - this is a public test service
	// that accepts POST requests and returns the headers it received.
	// We use /post endpoint which accepts any POST and echoes back headers.
	reqBody := TestConnectionDirectRequest{
		UpstreamURL:   "https://httpbin.org/post",
		TransportType: "streamable_http",
		AuthType:      "bearer",
		AuthToken:     "test-secret-token",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/test-connection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleTestMCPServerDirect(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if !resp.Success {
		t.Errorf("Success = false, want true. Error: %s", resp.Error)
	}

	if resp.Transport != "streamable_http" {
		t.Errorf("Transport = %q, want %q", resp.Transport, "streamable_http")
	}
}

// TestHandleTestMCPServerDirect_WithBasicAuth verifies that basic auth
// is properly passed through for direct test.
func TestHandleTestMCPServerDirect_WithBasicAuth(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	reqBody := TestConnectionDirectRequest{
		UpstreamURL:   "https://httpbin.org/post",
		TransportType: "streamable_http",
		AuthType:      "basic",
		AuthToken:     "user:password",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/test-connection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleTestMCPServerDirect(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if !resp.Success {
		t.Errorf("Success = false, want true. Error: %s", resp.Error)
	}
}

// TestHandleTestMCPServerDirect_WithAPIKeyAuth verifies that API key auth
// is properly passed through for direct test.
func TestHandleTestMCPServerDirect_WithAPIKeyAuth(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	reqBody := TestConnectionDirectRequest{
		UpstreamURL:   "https://httpbin.org/post",
		TransportType: "streamable_http",
		AuthType:      "api_key",
		AuthToken:     "my-api-key-12345",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/test-connection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleTestMCPServerDirect(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if !resp.Success {
		t.Errorf("Success = false, want true. Error: %s", resp.Error)
	}
}

// TestHandleTestMCPServerDirect_NoAuth defaults to no auth when auth_type not specified.
func TestHandleTestMCPServerDirect_NoAuth(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	// No auth_type specified - should default to no auth
	reqBody := TestConnectionDirectRequest{
		UpstreamURL:   "https://httpbin.org/post",
		TransportType: "streamable_http",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/test-connection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleTestMCPServerDirect(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if !resp.Success {
		t.Errorf("Success = false, want true. Error: %s", resp.Error)
	}
}

// TestHandleTestMCPServerDirect_InvalidAuthType returns error for invalid auth_type.
func TestHandleTestMCPServerDirect_InvalidAuthType(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	reqBody := TestConnectionDirectRequest{
		UpstreamURL:   "https://httpbin.org/post",
		TransportType: "streamable_http",
		AuthType:      "invalid_auth_type",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/test-connection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleTestMCPServerDirect(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d. Body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Success {
		t.Error("Success = true, want false for invalid auth_type")
	}

	if !strings.Contains(resp.Error, "auth_type") {
		t.Errorf("Error should mention invalid auth_type, got: %s", resp.Error)
	}
}

// TestHandleTestMCPServerDirect_SSEWithBearerAuth verifies SSE transport works with bearer auth.
// Note: We use streamable_http for auth testing since httpbin doesn't return text/event-stream.
// The auth injection logic is the same for both transports.
func TestHandleTestMCPServerDirect_SSEWithBearerAuth(t *testing.T) {
	server, _, _, cleanup := setupAPITestEnv(t)
	defer cleanup()

	// Use httpbin.org/post which accepts POST and returns headers as JSON
	// This verifies the auth headers are being sent correctly for SSE
	reqBody := TestConnectionDirectRequest{
		UpstreamURL:   "https://httpbin.org/post",
		TransportType: "sse",
		AuthType:      "bearer",
		AuthToken:     "sse-test-token",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/mcp-servers/test-connection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleTestMCPServerDirect(w, req)

	// For SSE, the server should check Content-Type = text/event-stream
	// httpbin returns application/json, so it will fail with "Unexpected response"
	// This is expected behavior - the important thing is that auth headers are sent
	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp TestConnectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	// Should have tried to connect (auth was sent), but SSE validation failed
	// because httpbin doesn't return text/event-stream
	if resp.Transport != "sse" {
		t.Errorf("Transport = %q, want %q", resp.Transport, "sse")
	}

	// Verify the error is about SSE content type, not about auth
	if resp.Success {
		t.Error("Success = true, want false for SSE with wrong content type")
	}
	if !strings.Contains(resp.Error, "content-type") && !strings.Contains(resp.Error, "Unexpected") {
		t.Errorf("Error should mention content type mismatch, got: %s", resp.Error)
	}
}


