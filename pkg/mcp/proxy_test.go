package mcp

import (
	"context"
	"encoding/base64"
	"net/http"
	"testing"
)

// =============================================================================
// ConnectionRegistry Tests
// =============================================================================

func TestNewConnectionRegistry_CreatesEmptyRegistry(t *testing.T) {
	t.Parallel()

	registry := NewConnectionRegistry()

	if registry == nil {
		t.Fatal("NewConnectionRegistry returned nil")
	}

	if registry.registry == nil {
		t.Error("registry.registry is nil, should be initialized empty map")
	}

	// Verify accessing a non-existent server returns empty slice
	cancels := registry.registry["nonexistent"]
	if cancels != nil {
		t.Errorf("registry.registry for nonexistent key = %v, want nil", cancels)
	}
}

func TestRegister_AddsEntry(t *testing.T) {
	t.Parallel()

	registry := NewConnectionRegistry()
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	registry.Register("server1", cancel)

	// Verify entry was added
	cancels, ok := registry.registry["server1"]
	if !ok {
		t.Fatal("registry.registry does not contain 'server1' after Register")
	}

	if len(cancels) != 1 {
		t.Errorf("len(cancels) = %d, want 1", len(cancels))
	}
}

func TestRegister_MultipleEntries(t *testing.T) {
	t.Parallel()

	registry := NewConnectionRegistry()
	_, cancel1 := context.WithCancel(context.Background())
	_, cancel2 := context.WithCancel(context.Background())
	defer cancel1()
	defer cancel2()

	registry.Register("server1", cancel1)
	registry.Register("server1", cancel2)

	cancels := registry.registry["server1"]
	if len(cancels) != 2 {
		t.Errorf("len(cancels) = %d, want 2", len(cancels))
	}
}

func TestUnregister_RemovesSpecificEntry(t *testing.T) {
	t.Parallel()

	registry := NewConnectionRegistry()
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Register the same cancel function twice
	registry.Register("server1", cancel)
	registry.Register("server1", cancel)

	// Unregister once - should remove only one entry
	registry.Unregister("server1", cancel)

	cancels := registry.registry["server1"]
	if len(cancels) != 1 {
		t.Errorf("len(cancels) after one Unregister = %d, want 1", len(cancels))
	}
}

func TestUnregister_NonExistentServer(t *testing.T) {
	t.Parallel()

	registry := NewConnectionRegistry()
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	// This should not panic
	registry.Unregister("nonexistent", cancel)

	// Verify the registry is still in a valid state
	if registry.registry == nil {
		t.Error("registry.registry is nil after Unregister on non-existent key")
	}
}

func TestUnregister_NotLastEntry(t *testing.T) {
	t.Parallel()

	registry := NewConnectionRegistry()
	_, cancel1 := context.WithCancel(context.Background())
	_, cancel2 := context.WithCancel(context.Background())
	defer cancel1()
	defer cancel2()

	registry.Register("server1", cancel1)
	registry.Register("server2", cancel2)

	// Unregister from server1 only
	registry.Unregister("server1", cancel1)

	// server1 should be empty
	cancels1 := registry.registry["server1"]
	if len(cancels1) != 0 {
		t.Errorf("len(cancels1) = %d, want 0 after Unregister", len(cancels1))
	}

	// server2 should still have its entry
	cancels2 := registry.registry["server2"]
	if len(cancels2) != 1 {
		t.Errorf("len(cancels2) = %d, want 1 (unaffected by server1 Unregister)", len(cancels2))
	}
}

func TestCloseConnections_ClosesAllAndReturnsCount(t *testing.T) {
	t.Parallel()

	registry := NewConnectionRegistry()

	// Create multiple cancel functions for the same server
	_, cancel1 := context.WithCancel(context.Background())
	_, cancel2 := context.WithCancel(context.Background())
	defer cancel1()
	defer cancel2()

	registry.Register("server1", cancel1)
	registry.Register("server1", cancel2)

	count := registry.CloseConnections("server1")

	if count != 2 {
		t.Errorf("CloseConnections returned %d, want 2", count)
	}

	// Verify all entries were cleared
	cancels := registry.registry["server1"]
	if cancels != nil {
		t.Errorf("registry.registry['server1'] = %v, want nil after CloseConnections", cancels)
	}
}

func TestCloseConnections_NonExistentServer(t *testing.T) {
	t.Parallel()

	registry := NewConnectionRegistry()

	count := registry.CloseConnections("nonexistent")

	if count != 0 {
		t.Errorf("CloseConnections for non-existent server returned %d, want 0", count)
	}
}

func TestCloseConnections_EmptyServer(t *testing.T) {
	t.Parallel()

	registry := NewConnectionRegistry()

	// Register and immediately unregister all entries
	_, cancel := context.WithCancel(context.Background())
	registry.Register("server1", cancel)
	registry.Unregister("server1", cancel)

	count := registry.CloseConnections("server1")

	if count != 0 {
		t.Errorf("CloseConnections for server with no entries returned %d, want 0", count)
	}
}

// =============================================================================
// injectAuth Tests
// =============================================================================

func TestInjectAuth_BearerToken(t *testing.T) {
	t.Parallel()

	cm := NewConnectionManager()
	server := &MCPServer{
		ID:          "test-server",
		AuthType:    AuthBearer,
		AuthToken:   "my-secret-token",
		UpstreamURL: "https://api.example.com",
	}

	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com", nil)
	cm.injectAuth(req, server)

	got := req.Header.Get("Authorization")
	want := "Bearer my-secret-token"
	if got != want {
		t.Errorf("Authorization header = %q, want %q", got, want)
	}
}

func TestInjectAuth_BasicAuth(t *testing.T) {
	t.Parallel()

	cm := NewConnectionManager()
	server := &MCPServer{
		ID:          "test-server",
		AuthType:    AuthBasic,
		AuthToken:   "user:password",
		UpstreamURL: "https://api.example.com",
	}

	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com", nil)
	cm.injectAuth(req, server)

	got := req.Header.Get("Authorization")
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:password"))
	if got != want {
		t.Errorf("Authorization header = %q, want %q", got, want)
	}
}

func TestInjectAuth_APIKey(t *testing.T) {
	t.Parallel()

	cm := NewConnectionManager()
	server := &MCPServer{
		ID:          "test-server",
		AuthType:    AuthAPIKey,
		AuthToken:   "api-key-12345",
		UpstreamURL: "https://api.example.com",
	}

	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com", nil)
	cm.injectAuth(req, server)

	got := req.Header.Get("X-API-Key")
	want := "api-key-12345"
	if got != want {
		t.Errorf("X-API-Key header = %q, want %q", got, want)
	}
}

func TestInjectAuth_AuthNone(t *testing.T) {
	t.Parallel()

	cm := NewConnectionManager()
	server := &MCPServer{
		ID:          "test-server",
		AuthType:    AuthNone,
		AuthToken:   "",
		UpstreamURL: "https://api.example.com",
	}

	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com", nil)
	cm.injectAuth(req, server)

	// Authorization header should not be set
	got := req.Header.Get("Authorization")
	if got != "" {
		t.Errorf("Authorization header = %q, want empty (no auth)", got)
	}

	// X-API-Key header should not be set
	apiKey := req.Header.Get("X-API-Key")
	if apiKey != "" {
		t.Errorf("X-API-Key header = %q, want empty (no auth)", apiKey)
	}
}

func TestInjectAuth_CustomHeadersAdded(t *testing.T) {
	t.Parallel()

	cm := NewConnectionManager()
	server := &MCPServer{
		ID:          "test-server",
		AuthType:    AuthNone,
		Headers:     `{"X-Custom-Header": "custom-value", "X-Request-ID": "req-123"}`,
		UpstreamURL: "https://api.example.com",
	}

	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com", nil)
	cm.injectAuth(req, server)

	// Verify custom headers are added
	customHeader := req.Header.Get("X-Custom-Header")
	if customHeader != "custom-value" {
		t.Errorf("X-Custom-Header = %q, want %q", customHeader, "custom-value")
	}

	requestID := req.Header.Get("X-Request-ID")
	if requestID != "req-123" {
		t.Errorf("X-Request-ID = %q, want %q", requestID, "req-123")
	}
}

func TestInjectAuth_BlockedHeadersFiltered(t *testing.T) {
	t.Parallel()

	cm := NewConnectionManager()

	// Create server with headers that should be blocked
	// These headers are in the blockedHeaders map from validation.go
	server := &MCPServer{
		ID:          "test-server",
		AuthType:    AuthNone,
		Headers:     `{"Host": "evil.com", "Content-Length": "1000", "Transfer-Encoding": "chunked", "Connection": "close", "Authorization": "Bearer evil", "Proxy-Authorization": "Basic evil", "X-Allowed": "yes"}`,
		UpstreamURL: "https://api.example.com",
	}

	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com", nil)
	cm.injectAuth(req, server)

	// Blocked headers should NOT be set
	if req.Header.Get("Host") != "" {
		t.Errorf("Host header should not be set, got %q", req.Header.Get("Host"))
	}
	if req.Header.Get("Content-Length") != "" {
		t.Errorf("Content-Length header should not be set, got %q", req.Header.Get("Content-Length"))
	}
	if req.Header.Get("Transfer-Encoding") != "" {
		t.Errorf("Transfer-Encoding header should not be set, got %q", req.Header.Get("Transfer-Encoding"))
	}
	if req.Header.Get("Connection") != "" {
		t.Errorf("Connection header should not be set, got %q", req.Header.Get("Connection"))
	}
	if req.Header.Get("Authorization") != "" {
		t.Errorf("Authorization header should not be set, got %q", req.Header.Get("Authorization"))
	}
	if req.Header.Get("Proxy-Authorization") != "" {
		t.Errorf("Proxy-Authorization header should not be set, got %q", req.Header.Get("Proxy-Authorization"))
	}

	// Allowed header should be set
	allowed := req.Header.Get("X-Allowed")
	if allowed != "yes" {
		t.Errorf("X-Allowed = %q, want %q", allowed, "yes")
	}
}

func TestInjectAuth_EmptyHeaders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		headers string
	}{
		{"empty string", ""},
		{"empty JSON object", "{}"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cm := NewConnectionManager()
			server := &MCPServer{
				ID:          "test-server",
				AuthType:    AuthNone,
				Headers:     tt.headers,
				UpstreamURL: "https://api.example.com",
			}

			req, _ := http.NewRequest(http.MethodGet, "https://api.example.com", nil)

			// Should not panic
			cm.injectAuth(req, server)

			// No custom headers should be added
			// Just verify no panic and empty headers
		})
	}
}

func TestInjectAuth_InvalidHeadersJSON(t *testing.T) {
	t.Parallel()

	cm := NewConnectionManager()
	server := &MCPServer{
		ID:          "test-server",
		AuthType:    AuthNone,
		Headers:     `invalid json`,
		UpstreamURL: "https://api.example.com",
	}

	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com", nil)

	// Should not panic - invalid JSON is silently ignored
	cm.injectAuth(req, server)
}

func TestInjectAuth_BlockedHeadersCaseInsensitive(t *testing.T) {
	t.Parallel()

	cm := NewConnectionManager()

	tests := []struct {
		name   string
		header string
	}{
		{"lowercase host", "host"},
		{"uppercase HOST", "HOST"},
		{"mixed Host", "Host"},
		{"lowercase authorization", "authorization"},
		{"uppercase AUTHORIZATION", "AUTHORIZATION"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			headersJSON := `{"` + tt.header + `": "blocked-value"}`
			server := &MCPServer{
				ID:          "test-server",
				AuthType:    AuthNone,
				Headers:     headersJSON,
				UpstreamURL: "https://api.example.com",
			}

			req, _ := http.NewRequest(http.MethodGet, "https://api.example.com", nil)
			cm.injectAuth(req, server)

			// Blocked headers should NOT be set regardless of case
			got := req.Header.Get(tt.header)
			if got != "" {
				t.Errorf("%s header should not be set, got %q", tt.header, got)
			}
		})
	}
}

func TestInjectAuth_AuthTypeOverridesExistingHeaders(t *testing.T) {
	t.Parallel()

	cm := NewConnectionManager()
	server := &MCPServer{
		ID:          "test-server",
		AuthType:    AuthBearer,
		AuthToken:   "my-token",
		UpstreamURL: "https://api.example.com",
	}

	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com", nil)
	// Set a pre-existing authorization header
	req.Header.Set("Authorization", "Existing-Auth")

	cm.injectAuth(req, server)

	// The injectAuth should override with Bearer token
	got := req.Header.Get("Authorization")
	want := "Bearer my-token"
	if got != want {
		t.Errorf("Authorization header = %q, want %q (should be overridden)", got, want)
	}
}

func TestInjectAuth_CustomHeadersAndAuthCombined(t *testing.T) {
	t.Parallel()

	cm := NewConnectionManager()
	server := &MCPServer{
		ID:          "test-server",
		AuthType:    AuthBearer,
		AuthToken:   "my-secret-token",
		Headers:     `{"X-Request-ID": "req-123", "X-Custom": "value"}`,
		UpstreamURL: "https://api.example.com",
	}

	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com", nil)
	cm.injectAuth(req, server)

	// Auth header should be set
	auth := req.Header.Get("Authorization")
	if auth != "Bearer my-secret-token" {
		t.Errorf("Authorization = %q, want %q", auth, "Bearer my-secret-token")
	}

	// Custom headers should also be set
	requestID := req.Header.Get("X-Request-ID")
	if requestID != "req-123" {
		t.Errorf("X-Request-ID = %q, want %q", requestID, "req-123")
	}

	custom := req.Header.Get("X-Custom")
	if custom != "value" {
		t.Errorf("X-Custom = %q, want %q", custom, "value")
	}
}

// =============================================================================
// getUpstreamURL Tests
// =============================================================================

func TestGetUpstreamURL_ReturnsServerURL(t *testing.T) {
	t.Parallel()

	cm := NewConnectionManager()

	tests := []struct {
		name        string
		upstreamURL string
	}{
		{"HTTPS URL", "https://api.example.com/mcp"},
		{"HTTP URL", "http://internal.example.com:8080"},
		{"URL with path", "https://api.example.com/v1/mcp/sse"},
		{"URL without trailing slash", "https://api.example.com"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := &MCPServer{
				ID:          "test-server",
				UpstreamURL: tt.upstreamURL,
			}

			got := cm.getUpstreamURL(server)
			if got != tt.upstreamURL {
				t.Errorf("getUpstreamURL() = %q, want %q", got, tt.upstreamURL)
			}
		})
	}
}

// =============================================================================
// NewConnectionManager Tests
// =============================================================================

func TestNewConnectionManager_ReturnsValidManager(t *testing.T) {
	t.Parallel()

	cm := NewConnectionManager()

	if cm == nil {
		t.Fatal("NewConnectionManager returned nil")
	}

	if cm.registry == nil {
		t.Error("cm.registry is nil")
	}

	if cm.httpClient == nil {
		t.Error("cm.httpClient is nil")
	}
}

func TestNewConnectionManager_GetRegistry(t *testing.T) {
	t.Parallel()

	cm := NewConnectionManager()

	registry := cm.GetRegistry()

	if registry == nil {
		t.Fatal("GetRegistry returned nil")
	}

	// Verify it's the same registry instance
	if registry != cm.registry {
		t.Error("GetRegistry should return the same registry instance")
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestConnectionRegistry_ConcurrentAccess(t *testing.T) {
	registry := NewConnectionRegistry()
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Spawn multiple goroutines registering and unregistering
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				_, localCancel := context.WithCancel(context.Background())
				registry.Register("server", localCancel)
				registry.Unregister("server", localCancel)
				localCancel()
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Just verify no panic and registry is still accessible
	count := registry.CloseConnections("server")
	t.Logf("Closed %d connections after concurrent access", count)
}

func TestInjectAuth_WithExistingRequestHeaders(t *testing.T) {
	t.Parallel()

	cm := NewConnectionManager()
	server := &MCPServer{
		ID:          "test-server",
		AuthType:    AuthBearer,
		AuthToken:   "my-token",
		Headers:     `{"X-Request-ID": "req-123", "Accept": "application/json"}`,
		UpstreamURL: "https://api.example.com",
	}

	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com", nil)
	// Set some existing headers
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "TestClient")

	cm.injectAuth(req, server)

	// Custom headers should be set (may override existing)
	accept := req.Header.Get("Accept")
	if accept != "application/json" {
		t.Errorf("Accept = %q, want %q (should be overridden by custom headers)", accept, "application/json")
	}

	userAgent := req.Header.Get("User-Agent")
	if userAgent != "TestClient" {
		t.Errorf("User-Agent = %q, want %q (unaffected)", userAgent, "TestClient")
	}
}
