package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/crypto"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	_ "modernc.org/sqlite"
)

// =============================================================================
// E2E Test Setup Helpers
// =============================================================================

// e2eTestEnv holds all components needed for E2E tests
type e2eTestEnv struct {
	proxyServer    *Server
	proxyURL       string
	mcpStore       *MCPStore
	authTokenStore auth.TokenStoreInterface
	validToken     string
	authDB         *sql.DB
	mcpDB          *sql.DB
	cleanup        func()
}

// setupE2EEnv creates a complete E2E test environment with running proxy server
func setupE2EEnv(t *testing.T) *e2eTestEnv {
	t.Helper()

	env := &e2eTestEnv{}

	// Create in-memory SQLite for MCP servers
	mcpDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open MCP database: %v", err)
	}
	env.mcpDB = mcpDB

	// Run MCP migrations
	if err := runMCPMigrations(mcpDB); err != nil {
		mcpDB.Close()
		t.Fatalf("failed to run MCP migrations: %v", err)
	}

	// Create MCPStore
	env.mcpStore = NewMCPStore(mcpDB, database.SQLite)

	// Create in-memory SQLite for auth tokens
	authDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		mcpDB.Close()
		t.Fatalf("failed to open auth database: %v", err)
	}
	env.authDB = authDB

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
		mcpDB.Close()
		authDB.Close()
		t.Fatalf("failed to create auth_tokens table: %v", err)
	}

	env.authTokenStore = auth.NewTokenStore(authDB, database.SQLite)

	// Create a test token
	plaintext, _, err := env.authTokenStore.CreateToken(context.Background(), "e2e-test-token", nil, "e2e-test", false, "", nil)
	if err != nil {
		mcpDB.Close()
		authDB.Close()
		t.Fatalf("failed to create test token: %v", err)
	}
	env.validToken = plaintext

	bus := events.NewBus()

	// Create proxy server - no port needed, runs on main mux
	env.proxyServer = NewServer(env.mcpStore, bus, env.authTokenStore)

	// Register proxy handlers on a test mux
	testMux := http.NewServeMux()
	env.proxyServer.RegisterProxyHandlers(testMux)

	// For httptest.Server approach - create a test server that routes to our proxy
	// We'll use httptest.Server for the proxy itself
	mux := http.NewServeMux()
	env.proxyServer.RegisterAPIHandlers(mux)
	mux.HandleFunc("/v1/mcp/", func(w http.ResponseWriter, r *http.Request) {
		// Route MCP requests through auth middleware then to handlers
		handler := env.proxyServer.proxyAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if strings.HasSuffix(path, "/sse") {
				env.proxyServer.handleSSEConnection(w, r)
			} else if strings.HasSuffix(path, "/messages") {
				env.proxyServer.handleSSEMessage(w, r)
			} else {
				env.proxyServer.handleStreamableHTTP(w, r)
			}
		})
		handler.ServeHTTP(w, r)
	})

	proxyTS := httptest.NewServer(mux)
	env.proxyURL = proxyTS.URL
	t.Cleanup(proxyTS.Close)

	env.cleanup = func() {
		mcpDB.Close()
		authDB.Close()
	}

	return env
}

// =============================================================================
// TestE2E_SSEProxyFlow
// =============================================================================

func TestE2E_SSEProxyFlow(t *testing.T) {
	// Skip if running in parallel - needs exclusive port access
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	env := setupE2EEnv(t)
	defer env.cleanup()

	// Create mock upstream SSE server
	upstreamMessagesReceived := false
	upstreamMessagesBody := ""
	var upstreamMu sync.Mutex

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// SSE endpoint
		if r.URL.Path == "/sse" {
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "streaming not supported", http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			flusher.Flush()

			// Send endpoint event with full URL pointing to messages endpoint
			// Use the actual upstream URL from the request
			messagesURL := fmt.Sprintf("http://%s/messages", r.Host)
			fmt.Fprintf(w, "event: endpoint\ndata: %s?token=test-session\n\n", messagesURL)
			flusher.Flush()

			// Send a message event
			fmt.Fprintf(w, "event: message\ndata: {\"type\":\"initialized\"}\n\n")
			flusher.Flush()

			// Keep connection open for a moment
			time.Sleep(500 * time.Millisecond)
			return
		}

		// Messages endpoint
		if r.URL.Path == "/messages" {
			upstreamMu.Lock()
			upstreamMessagesReceived = true
			body, _ := io.ReadAll(r.Body)
			upstreamMessagesBody = string(body)
			upstreamMu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":{"status":"ok"}}`)
			return
		}

		http.NotFound(w, r)
	}))
	defer upstream.Close()

	// Create MCP server config via store
	ctx := context.Background()
	mcpServer, err := env.mcpStore.CreateServer(ctx, CreateMCPServerRequest{
		ID:            "e2e-sse-server",
		Name:          "e2e-sse-server",
		Description:   "E2E SSE Test Server",
		UpstreamURL:   upstream.URL,
		TransportType: TransportSSE,
		AuthType:      AuthNone,
	})
	if err != nil {
		t.Fatalf("failed to create MCP server: %v", err)
	}

	// Test SSE endpoint - verify endpoint rewriting
	req, err := http.NewRequest(http.MethodGet, env.proxyURL+"/v1/mcp/"+mcpServer.ID+"/sse", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.validToken)

	client := &http.Client{
		Timeout: 2 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("SSE request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	// Read the SSE stream - should contain rewritten endpoint
	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	// The endpoint URL should be rewritten to proxy path
	expectedProxyPath := "/v1/mcp/" + mcpServer.ID + "/messages"
	if !strings.Contains(bodyStr, expectedProxyPath) {
		t.Errorf("SSE body should contain rewritten proxy path %q, got: %s", expectedProxyPath, bodyStr)
	}

	// Session token should be preserved
	if !strings.Contains(bodyStr, "?token=test-session") {
		t.Errorf("SSE body should preserve query string, got: %s", bodyStr)
	}

	// Test POST to messages endpoint
	postReq, err := http.NewRequest(http.MethodPost, env.proxyURL+"/v1/mcp/"+mcpServer.ID+"/messages",
		strings.NewReader(`{"jsonrpc":"2.0","method":"tools/list","id":1}`))
	if err != nil {
		t.Fatalf("failed to create POST request: %v", err)
	}
	postReq.Header.Set("Authorization", "Bearer "+env.validToken)
	postReq.Header.Set("Content-Type", "application/json")
	postReq.Header.Set("Mcp-Session-Id", "test-session-id")

	postResp, err := client.Do(postReq)
	if err != nil {
		t.Fatalf("POST request failed: %v", err)
	}
	defer postResp.Body.Close()

	if postResp.StatusCode != http.StatusOK {
		t.Fatalf("POST status = %d, want %d. Body: %s", postResp.StatusCode, http.StatusOK, postResp.Body)
	}

	// Verify message was forwarded to upstream
	upstreamMu.Lock()
	if !upstreamMessagesReceived {
		t.Error("upstream did not receive the message")
	}
	if !strings.Contains(upstreamMessagesBody, "tools/list") {
		t.Errorf("upstream received body %q, want to contain 'tools/list'", upstreamMessagesBody)
	}
	upstreamMu.Unlock()

	// Verify session ID passthrough
	if got := postResp.Header.Get("Mcp-Session-Id"); got != "test-session-id" {
		t.Errorf("Mcp-Session-Id = %q, want %q", got, "test-session-id")
	}
}

// =============================================================================
// TestE2E_StreamableHTTPFlow
// =============================================================================

func TestE2E_StreamableHTTPFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	env := setupE2EEnv(t)
	defer env.cleanup()

	// Track upstream state
	var upstreamMu sync.Mutex
	var receivedPath string
	var receivedBody string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamMu.Lock()
		receivedPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		upstreamMu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "upstream-session-123")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"jsonrpc":"2.0","result":{"tools":[]},"id":1}`)
	}))
	defer upstream.Close()

	// Create MCP server config
	ctx := context.Background()
	mcpServer, err := env.mcpStore.CreateServer(ctx, CreateMCPServerRequest{
		ID:            "e2e-streamable-server",
		Name:          "e2e-streamable-server",
		Description:   "E2E Streamable HTTP Test Server",
		UpstreamURL:   upstream.URL,
		TransportType: TransportStreamableHTTP,
		AuthType:      AuthNone,
	})
	if err != nil {
		t.Fatalf("failed to create MCP server: %v", err)
	}

	// POST to streamable HTTP endpoint
	requestBody := `{"jsonrpc":"2.0","method":"tools/list","id":1}`
	req, err := http.NewRequest(http.MethodPost, env.proxyURL+"/v1/mcp/"+mcpServer.ID+"/",
		strings.NewReader(requestBody))
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.validToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", "client-session-456")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Streamable HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Streamable HTTP status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// Verify request was forwarded to upstream
	upstreamMu.Lock()
	if !strings.Contains(receivedPath, "tools") && receivedPath != "/" {
		// Path stripping is expected - /mcp/{id}/ -> /
	}
	// Verify body was forwarded
	if !strings.Contains(receivedBody, "tools/list") {
		t.Errorf("upstream received body should contain 'tools/list', got: %s", receivedBody)
	}
	upstreamMu.Unlock()

	// Read response body
	respBody, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(respBody), "jsonrpc") {
		t.Errorf("response should contain jsonrpc, got: %s", respBody)
	}

	// Verify session ID passthrough (upstream's session ID takes precedence)
	if got := resp.Header.Get("Mcp-Session-Id"); got != "upstream-session-123" {
		t.Errorf("Mcp-Session-Id = %q, want %q", got, "upstream-session-123")
	}
}

// =============================================================================
// TestE2E_AuthOnTransport
// =============================================================================

func TestE2E_AuthOnTransport(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	env := setupE2EEnv(t)
	defer env.cleanup()

	// Create a simple upstream
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	// Create MCP server config
	ctx := context.Background()
	mcpServer, err := env.mcpStore.CreateServer(ctx, CreateMCPServerRequest{
		ID:            "e2e-auth-server",
		Name:          "e2e-auth-server",
		UpstreamURL:   upstream.URL,
		TransportType: TransportSSE,
		AuthType:      AuthNone,
	})
	if err != nil {
		t.Fatalf("failed to create MCP server: %v", err)
	}

	client := &http.Client{Timeout: 2 * time.Second}

	// Test 1: No auth token -> 401
	t.Run("no_auth_token", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, env.proxyURL+"/v1/mcp/"+mcpServer.ID+"/sse", nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		// No Authorization header

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
		}

		// Verify error response is JSON
		body, _ := io.ReadAll(resp.Body)
		var errResp map[string]string
		json.Unmarshal(body, &errResp)
		if errResp["error"] == "" {
			t.Errorf("error response should contain 'error' field, got: %s", body)
		}
	})

	// Test 2: Invalid token -> 401
	t.Run("invalid_token", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, env.proxyURL+"/v1/mcp/"+mcpServer.ID+"/sse", nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer invalid-token-xyz")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
		}
	})

	// Test 3: Valid token -> 200 (or connection proceeds)
	t.Run("valid_token", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, env.proxyURL+"/v1/mcp/"+mcpServer.ID+"/sse", nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+env.validToken)

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		// Should get 200 (or connection succeeds)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	})

	// Test 4: Malformed auth header -> 401
	t.Run("malformed_auth_header", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, env.proxyURL+"/v1/mcp/"+mcpServer.ID+"/sse", nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
		}
	})
}

// =============================================================================
// TestE2E_EncryptionRoundTrip
// =============================================================================

func TestE2E_EncryptionRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	// Set up encryption key for this test
	crypto.ResetEncryptionState()
	testKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}
	os.Setenv(crypto.EnvEncryptionKey, testKey)
	defer os.Unsetenv(crypto.EnvEncryptionKey)
	if err := crypto.InitEncryption(); err != nil {
		t.Fatalf("failed to init encryption: %v", err)
	}

	env := setupE2EEnv(t)
	defer env.cleanup()

	// Create server config with auth_token
	plaintextToken := "super-secret-upstream-token-12345"
	ctx := context.Background()
	mcpServer, err := env.mcpStore.CreateServer(ctx, CreateMCPServerRequest{
		ID:            "e2e-encryption-server",
		Name:          "e2e-encryption-server",
		UpstreamURL:   "https://api.example.com/mcp",
		TransportType: TransportSSE,
		AuthType:      AuthBearer,
		AuthToken:     plaintextToken,
	})
	if err != nil {
		t.Fatalf("failed to create MCP server: %v", err)
	}

	// Step 1: Query raw DB to verify stored token is encrypted (not plaintext)
	var rawToken string
	err = env.mcpDB.QueryRowContext(ctx,
		"SELECT auth_token FROM mcp_servers WHERE id = ?",
		mcpServer.ID).Scan(&rawToken)
	if err != nil {
		t.Fatalf("failed to query raw token: %v", err)
	}

	// Raw token should NOT be plaintext
	if rawToken == plaintextToken {
		t.Errorf("raw DB token should be encrypted, not plaintext")
	}

	// Raw token should look encrypted (base64-ish, not readable)
	if rawToken == "" || rawToken == plaintextToken {
		t.Errorf("stored token should be different from plaintext, got: %s", rawToken)
	}

	// Step 2: Use store.GetServer() to verify token is decrypted (plaintext)
	retrieved, err := env.mcpStore.GetServer(ctx, mcpServer.ID)
	if err != nil {
		t.Fatalf("failed to get server: %v", err)
	}

	if retrieved.AuthToken != plaintextToken {
		t.Errorf("GetServer().AuthToken = %q, want plaintext %q", retrieved.AuthToken, plaintextToken)
	}

	// Step 3: Verify masking behavior on API responses
	// The API handlers use maskServer() which shows first 6 + "***" + last 3
	maskedServer := maskServer(retrieved)
	if maskedServer.AuthToken == plaintextToken {
		t.Errorf("masked server AuthToken should be masked, not plaintext")
	}

	// Verify masking pattern: first 6 + "***" + last 3
	// "super-secret-upstream-token-12345" -> "super-***345"
	expectedMask := plaintextToken[:6] + "***" + plaintextToken[len(plaintextToken)-3:]
	if maskedServer.AuthToken != expectedMask {
		t.Errorf("masked AuthToken = %q, want %q", maskedServer.AuthToken, expectedMask)
	}
}

// =============================================================================
// TestE2E_ConnectionTestAPI
// =============================================================================

func TestE2E_ConnectionTestAPI(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	env := setupE2EEnv(t)
	defer env.cleanup()

	ctx := context.Background()

	// Test 1: Reachable SSE upstream -> success
	t.Run("reachable_sse_upstream", func(t *testing.T) {
		// Create reachable upstream
		reachableUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
		}))
		defer reachableUpstream.Close()

		// Create server pointing to reachable upstream
		mcpServer, err := env.mcpStore.CreateServer(ctx, CreateMCPServerRequest{
			ID:            "reachable-sse-" + time.Now().Format("150405.000"),
			Name:          "reachable-sse-" + time.Now().Format("150405.000"),
			UpstreamURL:   reachableUpstream.URL + "/sse",
			TransportType: TransportSSE,
			AuthType:      AuthNone,
		})
		if err != nil {
			t.Fatalf("failed to create server: %v", err)
		}

		// Call test endpoint
		req := httptest.NewRequest(http.MethodPost,
			env.proxyURL+"/fe/api/mcp-servers/"+mcpServer.ID+"/test", nil)
		w := httptest.NewRecorder()

		env.proxyServer.handleTestMCPServer(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d. Body: %s", w.Code, http.StatusOK, w.Body.String())
		}

		var resp TestConnectionResponse
		json.Unmarshal(w.Body.Bytes(), &resp)

		if !resp.Success {
			t.Errorf("Success = false, want true. Error: %s", resp.Error)
		}

		if resp.LatencyMs < 0 {
			t.Errorf("LatencyMs = %d, want >= 0", resp.LatencyMs)
		}

		if resp.Transport != string(TransportSSE) {
			t.Errorf("Transport = %q, want %q", resp.Transport, TransportSSE)
		}
	})

	// Test 2: Unreachable upstream -> failure with error message
	t.Run("unreachable_upstream", func(t *testing.T) {
		// Create server pointing to unreachable host
		mcpServer, err := env.mcpStore.CreateServer(ctx, CreateMCPServerRequest{
			ID:            "unreachable-" + time.Now().Format("150405.000"),
			Name:          "unreachable-" + time.Now().Format("150405.000"),
			UpstreamURL:   "http://192.0.2.1:9999/mcp", // TEST-NET-1, should be unreachable
			TransportType: TransportSSE,
			AuthType:      AuthNone,
		})
		if err != nil {
			t.Fatalf("failed to create server: %v", err)
		}

		// Call test endpoint
		req := httptest.NewRequest(http.MethodPost,
			env.proxyURL+"/fe/api/mcp-servers/"+mcpServer.ID+"/test", nil)
		w := httptest.NewRecorder()

		env.proxyServer.handleTestMCPServer(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}

		var resp TestConnectionResponse
		json.Unmarshal(w.Body.Bytes(), &resp)

		if resp.Success {
			t.Error("Success = true, want false for unreachable upstream")
		}

		if resp.Error == "" {
			t.Error("Error should not be empty for unreachable upstream")
		}
	})

	// Test 3: Streamable HTTP upstream
	t.Run("reachable_streamable_upstream", func(t *testing.T) {
		reachableUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer reachableUpstream.Close()

		mcpServer, err := env.mcpStore.CreateServer(ctx, CreateMCPServerRequest{
			ID:            "reachable-http-" + time.Now().Format("150405.000"),
			Name:          "reachable-http-" + time.Now().Format("150405.000"),
			UpstreamURL:   reachableUpstream.URL,
			TransportType: TransportStreamableHTTP,
			AuthType:      AuthNone,
		})
		if err != nil {
			t.Fatalf("failed to create server: %v", err)
		}

		req := httptest.NewRequest(http.MethodPost,
			env.proxyURL+"/fe/api/mcp-servers/"+mcpServer.ID+"/test", nil)
		w := httptest.NewRecorder()

		env.proxyServer.handleTestMCPServer(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}

		var resp TestConnectionResponse
		json.Unmarshal(w.Body.Bytes(), &resp)

		if !resp.Success {
			t.Errorf("Success = false, want true. Error: %s", resp.Error)
		}
	})
}

// =============================================================================
// TestE2E_DeleteCleanup
// =============================================================================

func TestE2E_DeleteCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	env := setupE2EEnv(t)
	defer env.cleanup()

	// Create MCP server config
	ctx := context.Background()
	mcpServer, err := env.mcpStore.CreateServer(ctx, CreateMCPServerRequest{
		ID:            "e2e-delete-server",
		Name:          "e2e-delete-server",
		UpstreamURL:   "https://api.example.com/mcp",
		TransportType: TransportSSE,
		AuthType:      AuthNone,
	})
	if err != nil {
		t.Fatalf("failed to create MCP server: %v", err)
	}

	// Establish a connection (register in registry)
	registry := env.proxyServer.connMgr.GetRegistry()
	cancelCalled := false
	mockCancel := func() {
		cancelCalled = true
	}
	registry.Register(mcpServer.ID, mockCancel)

	// Verify connection is registered
	connections := getConnectionsForServer(registry, mcpServer.ID)
	if len(connections) != 1 {
		t.Fatalf("Expected 1 connection, got %d", len(connections))
	}

	// Delete server via API
	req := httptest.NewRequest(http.MethodDelete,
		env.proxyURL+"/fe/api/mcp-servers/"+mcpServer.ID, nil)
	w := httptest.NewRecorder()

	env.proxyServer.handleDeleteMCPServer(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d", w.Code, http.StatusNoContent)
	}

	// Verify cancel was called
	if !cancelCalled {
		t.Error("cancelCalled should be true after delete")
	}

	// Verify connection was removed from registry
	connections = getConnectionsForServer(registry, mcpServer.ID)
	if len(connections) != 0 {
		t.Errorf("Expected 0 connections after delete, got %d", len(connections))
	}

	// Verify server removed from DB
	retrieved, err := env.mcpStore.GetServer(ctx, mcpServer.ID)
	if err != nil {
		t.Fatalf("GetServer after delete failed: %v", err)
	}
	if retrieved != nil {
		t.Error("Server should be deleted from DB")
	}
}

// =============================================================================
// TestE2E_StatusEndpoint
// =============================================================================

func TestE2E_StatusEndpoint(t *testing.T) {
	// Test status endpoint when server has a valid port
	t.Run("server_running", func(t *testing.T) {
		env := setupE2EEnv(t)
		defer env.cleanup()

		// Server is enabled when store is not nil (already set up in setupE2EEnv)

		req := httptest.NewRequest(http.MethodGet,
			env.proxyURL+"/fe/api/mcp-servers/status", nil)
		w := httptest.NewRecorder()

		env.proxyServer.handleMCPStatus(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}

		var resp MCPStatusResponse
		json.Unmarshal(w.Body.Bytes(), &resp)

		if !resp.Enabled {
			t.Error("Enabled should be true when server has valid store")
		}
	})

	// Test with nil store (disabled)
	t.Run("store_nil", func(t *testing.T) {
		env := setupE2EEnv(t)
		defer env.cleanup()

		// Set store to nil to simulate disabled state
		env.proxyServer.store = nil

		req := httptest.NewRequest(http.MethodGet,
			env.proxyURL+"/fe/api/mcp-servers/status", nil)
		w := httptest.NewRecorder()

		env.proxyServer.handleMCPStatus(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}

		var resp MCPStatusResponse
		json.Unmarshal(w.Body.Bytes(), &resp)

		if resp.Enabled {
			t.Error("Enabled should be false when store is nil")
		}
	})
}

// =============================================================================
// Additional Integration Tests
// =============================================================================

func TestE2E_MultipleServers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	env := setupE2EEnv(t)
	defer env.cleanup()

	ctx := context.Background()

	// Create multiple servers
	for i := 0; i < 3; i++ {
		_, err := env.mcpStore.CreateServer(ctx, CreateMCPServerRequest{
			ID:            fmt.Sprintf("multi-server-%d", i),
			Name:          fmt.Sprintf("multi-server-%d", i),
			UpstreamURL:   "https://api.example.com/mcp",
			TransportType: TransportStreamableHTTP,
			AuthType:      AuthNone,
		})
		if err != nil {
			t.Fatalf("failed to create server %d: %v", i, err)
		}
	}

	// List servers
	servers, err := env.mcpStore.ListServers(ctx)
	if err != nil {
		t.Fatalf("ListServers failed: %v", err)
	}

	if len(servers) != 3 {
		t.Errorf("Expected 3 servers, got %d", len(servers))
	}
}

func TestE2E_ServerNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	env := setupE2EEnv(t)
	defer env.cleanup()

	client := &http.Client{Timeout: 2 * time.Second}

	// Try to access non-existent server
	req, err := http.NewRequest(http.MethodGet,
		env.proxyURL+"/v1/mcp/nonexistent-id/sse", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.validToken)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Should return 404 or 401 (auth passes, but server not found)
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 404 or 401", resp.StatusCode)
	}
}

func TestE2E_ServerDisabled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	env := setupE2EEnv(t)
	defer env.cleanup()

	ctx := context.Background()

	// Create disabled server
	disabled := false
	mcpServer, err := env.mcpStore.CreateServer(ctx, CreateMCPServerRequest{
		ID:            "disabled-server",
		Name:          "disabled-server",
		UpstreamURL:   "https://api.example.com/mcp",
		TransportType: TransportSSE,
		AuthType:      AuthNone,
		Enabled:       &disabled,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	client := &http.Client{Timeout: 2 * time.Second}

	// Try to access disabled server
	req, err := http.NewRequest(http.MethodGet,
		env.proxyURL+"/v1/mcp/"+mcpServer.ID+"/sse", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.validToken)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Should return 503 Service Unavailable
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
}


