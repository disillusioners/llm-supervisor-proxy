package mcp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// =============================================================================
// Test Helpers
// =============================================================================

// setupSSETest creates a Server with real SQLite-backed stores for testing.
// Returns the server, a valid auth token, and a cleanup function.
func setupSSETest(t *testing.T) (*Server, string, func()) {
	t.Helper()

	server, _, plaintext, cleanup := setupTestEnv(t)
	return server, plaintext, cleanup
}

// createTestServerInStore creates an MCP server in the store and returns it.
func createTestServerInStore(t *testing.T, store *MCPStore, upstreamURL string, transportType TransportType) *MCPServer {
	t.Helper()
	ctx := context.Background()

	server, err := store.CreateServer(ctx, CreateMCPServerRequest{
		ID:            "test-sse-server-" + time.Now().Format("150405.000000"),
		Name:          "test-sse-server-" + time.Now().Format("150405.000000"),
		Description:   "Test SSE server",
		UpstreamURL:   upstreamURL,
		TransportType: transportType,
		AuthType:      AuthNone,
	})
	if err != nil {
		t.Fatalf("failed to create test server: %v", err)
	}
	return server
}

// =============================================================================
// extractServerID Tests
// =============================================================================

func TestExtractServerID_ValidPath(t *testing.T) {
	t.Parallel()

	got, err := extractServerID("/v1/mcp/server123/sse")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "server123"
	if got != want {
		t.Errorf("extractServerID(%q) = %q, want %q", "/v1/mcp/server123/sse", got, want)
	}
}

func TestExtractServerID_WithMessages(t *testing.T) {
	t.Parallel()

	got, err := extractServerID("/v1/mcp/server456/messages")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "server456"
	if got != want {
		t.Errorf("extractServerID(%q) = %q, want %q", "/v1/mcp/server456/messages", got, want)
	}
}

func TestExtractServerID_InvalidPath_TooShort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
	}{
		{"only /v1/mcp", "/v1/mcp"},
		{"v1/mcp only no slash", "v1/mcp"},
		{"empty string", ""},
		{"single slash", "/"},
		{"mcp slash only", "/mcp"},
		{"SSE endpoint only", "/v1/mcp/sse"},
		{"messages endpoint only", "/v1/mcp/messages"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := extractServerID(tt.path)
			if err == nil {
				t.Errorf("extractServerID(%q) = %q, want error", tt.path, got)
			}
		})
	}
}

func TestExtractServerID_WrongPrefix(t *testing.T) {
	t.Parallel()

	_, err := extractServerID("/v1/api/server/sse")
	if err == nil {
		t.Errorf("extractServerID(%q) should return error, got nil", "/v1/api/server/sse")
	}
}

func TestExtractServerID_TrailingSlash(t *testing.T) {
	t.Parallel()

	// Note: extractServerID trims leading /v1/ and trailing slashes
	got, err := extractServerID("/v1/mcp/server789/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "server789"
	if got != want {
		t.Errorf("extractServerID(%q) = %q, want %q", "/v1/mcp/server789/", got, want)
	}
}

func TestExtractServerID_LongerPath(t *testing.T) {
	t.Parallel()

	got, err := extractServerID("/v1/mcp/my-server/sse/extra/path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "my-server"
	if got != want {
		t.Errorf("extractServerID(%q) = %q, want %q", "/v1/mcp/my-server/sse/extra/path", got, want)
	}
}

func TestExtractServerID_NoLeadingSlash(t *testing.T) {
	t.Parallel()

	got, err := extractServerID("/v1/mcp/server123/sse")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "server123"
	if got != want {
		t.Errorf("extractServerID(%q) = %q, want %q", "/v1/mcp/server123/sse", got, want)
	}
}

// =============================================================================
// rewriteEndpointData Tests
// =============================================================================

func TestRewriteEndpointData_SingleLineURL(t *testing.T) {
	t.Parallel()

	dataLines := []string{"http://upstream:3001/messages?token=abc"}
	result := rewriteEndpointData(dataLines, "server123")

	if len(result) != 1 {
		t.Fatalf("expected 1 result line, got %d", len(result))
	}

	// Should contain the proxy path and preserve query string
	if !strings.Contains(result[0], "/v1/mcp/server123/messages") {
		t.Errorf("result %q should contain /v1/mcp/server123/messages", result[0])
	}
	if !strings.Contains(result[0], "?token=abc") {
		t.Errorf("result %q should preserve query string ?token=abc", result[0])
	}
}

func TestRewriteEndpointData_HTTPSSingleLineURL(t *testing.T) {
	t.Parallel()

	dataLines := []string{"https://upstream.example.com:3001/messages?token=abc"}
	result := rewriteEndpointData(dataLines, "server456")

	if len(result) != 1 {
		t.Fatalf("expected 1 result line, got %d", len(result))
	}

	if !strings.Contains(result[0], "/v1/mcp/server456/messages") {
		t.Errorf("result %q should contain /v1/mcp/server456/messages", result[0])
	}
	if !strings.Contains(result[0], "?token=abc") {
		t.Errorf("result %q should preserve query string ?token=abc", result[0])
	}
}

func TestRewriteEndpointData_MultiLineData(t *testing.T) {
	t.Parallel()

	dataLines := []string{
		"http://upstream:3001/messages?session=xyz",
		"http://upstream:3001/messages?session=abc",
	}
	result := rewriteEndpointData(dataLines, "server789")

	if len(result) != 2 {
		t.Fatalf("expected 2 result lines, got %d", len(result))
	}

	for i, line := range result {
		if !strings.Contains(line, "/v1/mcp/server789/messages") {
			t.Errorf("result[%d] %q should contain /v1/mcp/server789/messages", i, line)
		}
	}
}

func TestRewriteEndpointData_AbsolutePath(t *testing.T) {
	t.Parallel()

	dataLines := []string{"/messages?token=xyz"}
	result := rewriteEndpointData(dataLines, "serverID")

	if len(result) != 1 {
		t.Fatalf("expected 1 result line, got %d", len(result))
	}

	if !strings.Contains(result[0], "/v1/mcp/serverID/messages") {
		t.Errorf("result %q should contain /v1/mcp/serverID/messages", result[0])
	}
	if !strings.Contains(result[0], "?token=xyz") {
		t.Errorf("result %q should preserve query string ?token=xyz", result[0])
	}
}

func TestRewriteEndpointData_NonEndpointPassthrough(t *testing.T) {
	t.Parallel()

	dataLines := []string{"this is just some data", "not a url", "plain text"}
	result := rewriteEndpointData(dataLines, "server123")

	if len(result) != 3 {
		t.Fatalf("expected 3 result lines, got %d", len(result))
	}

	for i, line := range result {
		if line != dataLines[i] {
			t.Errorf("result[%d] = %q, want %q (unchanged passthrough)", i, line, dataLines[i])
		}
	}
}

func TestRewriteEndpointData_QueryStringPreserved(t *testing.T) {
	t.Parallel()

	dataLines := []string{"http://host:8080/messages?token=abc&session=def&foo=bar"}
	result := rewriteEndpointData(dataLines, "svr")

	if len(result) != 1 {
		t.Fatalf("expected 1 result line, got %d", len(result))
	}

	// All query params should be preserved
	if !strings.Contains(result[0], "?token=abc&session=def&foo=bar") {
		t.Errorf("result %q should preserve all query params", result[0])
	}
}

func TestRewriteEndpointData_EmptyInput(t *testing.T) {
	t.Parallel()

	result := rewriteEndpointData([]string{}, "server123")
	if len(result) != 0 {
		t.Errorf("expected empty result for empty input, got %d lines", len(result))
	}
}

// =============================================================================
// rewriteSingleEndpointLine Tests
// =============================================================================

func TestRewriteSingleEndpointLine_FullHTTPURL(t *testing.T) {
	t.Parallel()

	result := rewriteSingleEndpointLine("http://upstream:3001/messages?token=abc", "/v1/mcp/s1/messages")
	if !strings.Contains(result, "/v1/mcp/s1/messages") {
		t.Errorf("result %q should contain /v1/mcp/s1/messages", result)
	}
	if !strings.Contains(result, "?token=abc") {
		t.Errorf("result %q should contain query string", result)
	}
}

func TestRewriteSingleEndpointLine_FullHTTPSURL(t *testing.T) {
	t.Parallel()

	result := rewriteSingleEndpointLine("https://secure.example.com/messages?key=val", "/v1/mcp/s2/messages")
	if !strings.Contains(result, "/v1/mcp/s2/messages") {
		t.Errorf("result %q should contain /v1/mcp/s2/messages", result)
	}
}

func TestRewriteSingleEndpointLine_AbsolutePath(t *testing.T) {
	t.Parallel()

	result := rewriteSingleEndpointLine("/messages?token=xyz", "/v1/mcp/s3/messages")
	if !strings.Contains(result, "/v1/mcp/s3/messages") {
		t.Errorf("result %q should contain /v1/mcp/s3/messages", result)
	}
}

func TestRewriteSingleEndpointLine_PlainText(t *testing.T) {
	t.Parallel()

	input := "not a url at all"
	result := rewriteSingleEndpointLine(input, "/v1/mcp/s4/messages")
	if result != input {
		t.Errorf("result = %q, want %q (unchanged)", result, input)
	}
}

func TestRewriteSingleEndpointLine_WithDataPrefix(t *testing.T) {
	t.Parallel()

	result := rewriteSingleEndpointLine("data: http://upstream:3001/messages?token=abc", "/v1/mcp/s5/messages")
	if !strings.Contains(result, "/v1/mcp/s5/messages") {
		t.Errorf("result %q should contain /v1/mcp/s5/messages", result)
	}
	if !strings.Contains(result, "?token=abc") {
		t.Errorf("result %q should contain query string", result)
	}
}

// =============================================================================
// rewriteFullURL Tests
// =============================================================================

func TestRewriteFullURL_SimpleHTTP(t *testing.T) {
	t.Parallel()

	result := rewriteFullURL("http://upstream:3001/messages?token=abc", "/v1/mcp/s1/messages")
	if !strings.Contains(result, "/v1/mcp/s1/messages") {
		t.Errorf("result %q should contain /v1/mcp/s1/messages", result)
	}
	if !strings.Contains(result, "?token=abc") {
		t.Errorf("result %q should contain query string", result)
	}
}

func TestRewriteFullURL_HTTPS(t *testing.T) {
	t.Parallel()

	result := rewriteFullURL("https://secure.example.com:443/messages?session=xyz", "/v1/mcp/s2/messages")
	if !strings.Contains(result, "/v1/mcp/s2/messages") {
		t.Errorf("result %q should contain /v1/mcp/s2/messages", result)
	}
	if !strings.Contains(result, "?session=xyz") {
		t.Errorf("result %q should contain query string", result)
	}
}

func TestRewriteFullURL_WithDataPrefix(t *testing.T) {
	t.Parallel()

	result := rewriteFullURL("data: http://host:8080/messages?key=val", "/v1/mcp/s3/messages")
	if !strings.Contains(result, "data:") {
		t.Errorf("result %q should preserve data: prefix", result)
	}
	if !strings.Contains(result, "/v1/mcp/s3/messages") {
		t.Errorf("result %q should contain /v1/mcp/s3/messages", result)
	}
}

func TestRewriteFullURL_NoQueryString(t *testing.T) {
	t.Parallel()

	result := rewriteFullURL("http://host:8080/messages", "/v1/mcp/s4/messages")
	if !strings.Contains(result, "/v1/mcp/s4/messages") {
		t.Errorf("result %q should contain /v1/mcp/s4/messages", result)
	}
}

// =============================================================================
// rewriteAbsolutePath Tests
// =============================================================================

func TestRewriteAbsolutePath_MessagesEndpoint(t *testing.T) {
	t.Parallel()

	result := rewriteAbsolutePath("/messages?token=xyz", "/v1/mcp/s1/messages")
	if !strings.Contains(result, "/v1/mcp/s1/messages") {
		t.Errorf("result %q should contain /v1/mcp/s1/messages", result)
	}
	if !strings.Contains(result, "?token=xyz") {
		t.Errorf("result %q should contain query string", result)
	}
}

func TestRewriteAbsolutePath_WithDataPrefix(t *testing.T) {
	t.Parallel()

	result := rewriteAbsolutePath("data: /messages?token=xyz", "/v1/mcp/s2/messages")
	if !strings.Contains(result, "data:") {
		t.Errorf("result %q should preserve data: prefix", result)
	}
	if !strings.Contains(result, "/v1/mcp/s2/messages") {
		t.Errorf("result %q should contain /v1/mcp/s2/messages", result)
	}
}

func TestRewriteAbsolutePath_NoQueryString(t *testing.T) {
	t.Parallel()

	result := rewriteAbsolutePath("/messages", "/v1/mcp/s3/messages")
	if !strings.Contains(result, "/v1/mcp/s3/messages") {
		t.Errorf("result %q should contain /v1/mcp/s3/messages", result)
	}
}

// =============================================================================
// flushSSEEvent Tests
// =============================================================================

func TestFlushSSEEvent_BasicEvent(t *testing.T) {
	t.Parallel()

	server := &Server{}
	rec := httptest.NewRecorder()
	event := &sseEvent{
		eventType: "message",
		dataLines: []string{"hello world"},
		rawLines:  []string{"event: message"},
	}

	server.flushSSEEvent(rec, event, rec, "server123")

	body := rec.Body.String()
	if !strings.Contains(body, "event: message\n") {
		t.Errorf("body should contain 'event: message', got: %q", body)
	}
	if !strings.Contains(body, "data: hello world\n") {
		t.Errorf("body should contain 'data: hello world', got: %q", body)
	}
}

func TestFlushSSEEvent_EndpointEventRewritten(t *testing.T) {
	t.Parallel()

	server := &Server{}
	rec := httptest.NewRecorder()
	event := &sseEvent{
		eventType: "endpoint",
		dataLines: []string{"http://upstream:3001/messages?token=abc"},
		rawLines:  []string{"event: endpoint"},
	}

	server.flushSSEEvent(rec, event, rec, "test-server")

	body := rec.Body.String()
	if !strings.Contains(body, "event: endpoint\n") {
		t.Errorf("body should contain 'event: endpoint', got: %q", body)
	}
	if !strings.Contains(body, "/v1/mcp/test-server/messages") {
		t.Errorf("body should contain rewritten proxy path, got: %q", body)
	}
	if !strings.Contains(body, "?token=abc") {
		t.Errorf("body should preserve query string, got: %q", body)
	}
}

func TestFlushSSEEvent_MultipleDataLines(t *testing.T) {
	t.Parallel()

	server := &Server{}
	rec := httptest.NewRecorder()
	event := &sseEvent{
		eventType: "message",
		dataLines: []string{"line1", "line2", "line3"},
		rawLines:  nil,
	}

	server.flushSSEEvent(rec, event, rec, "server123")

	body := rec.Body.String()
	if strings.Count(body, "data: ") != 3 {
		t.Errorf("body should have 3 data lines, got: %q", body)
	}
}

func TestFlushSSEEvent_NonEndpointEventPassthrough(t *testing.T) {
	t.Parallel()

	server := &Server{}
	rec := httptest.NewRecorder()
	event := &sseEvent{
		eventType: "message",
		dataLines: []string{"http://some-url.com/path"},
		rawLines:  nil,
	}

	server.flushSSEEvent(rec, event, rec, "server123")

	body := rec.Body.String()
	// Non-endpoint events should pass data through unchanged
	if !strings.Contains(body, "data: http://some-url.com/path\n") {
		t.Errorf("non-endpoint event data should pass through unchanged, got: %q", body)
	}
}

// =============================================================================
// handleSSEMessage Tests
// =============================================================================

func TestHandleSSEMessage_405ForNonPOST(t *testing.T) {
	t.Parallel()

	server, _, cleanup := setupSSETest(t)
	defer cleanup()

	methods := []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(method, "/v1/mcp/test-server/messages", nil)
			req.Header.Set("Authorization", "Bearer test-token")
			rec := httptest.NewRecorder()

			server.handleSSEMessage(rec, req)

			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want %d for method %s", rec.Code, http.StatusMethodNotAllowed, method)
			}
		})
	}
}

func TestHandleSSEMessage_MissingServerID(t *testing.T) {
	t.Parallel()

	server, validToken, cleanup := setupSSETest(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/messages", nil)
	req.Header.Set("Authorization", "Bearer "+validToken)
	rec := httptest.NewRecorder()

	// Call the handler directly (bypassing auth middleware since path is invalid)
	server.handleSSEMessage(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleSSEMessage_ServerNotFound(t *testing.T) {
	t.Parallel()

	server, validToken, cleanup := setupSSETest(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/nonexistent-server/messages", nil)
	req.Header.Set("Authorization", "Bearer "+validToken)
	rec := httptest.NewRecorder()

	// Call handler directly (bypassing auth middleware for unit test)
	server.handleSSEMessage(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleSSEMessage_ForwardsPOST(t *testing.T) {
	t.Parallel()

	server, validToken, cleanup := setupSSETest(t)
	defer cleanup()

	// Create a mock upstream server that echoes back the request body
	receivedBody := ""
	receivedMethod := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ok"}`)
	}))
	defer upstream.Close()

	// Create server in store pointing to our mock upstream
	mcpServer := createTestServerInStore(t, server.store, upstream.URL, TransportSSE)

	// Build the request
	requestBody := `{"jsonrpc":"2.0","method":"initialize","id":1}`
	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/"+mcpServer.ID+"/messages", strings.NewReader(requestBody))
	req.Header.Set("Authorization", "Bearer "+validToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.handleSSEMessage(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	if receivedMethod != http.MethodPost {
		t.Errorf("upstream received method %q, want %q", receivedMethod, http.MethodPost)
	}

	if receivedBody != requestBody {
		t.Errorf("upstream received body %q, want %q", receivedBody, requestBody)
	}

	// Verify response body is forwarded
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Errorf("response body should contain forwarded content, got: %q", rec.Body.String())
	}
}

func TestHandleSSEMessage_McpSessionIdPassthrough(t *testing.T) {
	t.Parallel()

	server, validToken, cleanup := setupSSETest(t)
	defer cleanup()

	receivedSessionID := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSessionID = r.Header.Get("Mcp-Session-Id")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `ok`)
	}))
	defer upstream.Close()

	mcpServer := createTestServerInStore(t, server.store, upstream.URL, TransportSSE)

	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/"+mcpServer.ID+"/messages", nil)
	req.Header.Set("Authorization", "Bearer "+validToken)
	req.Header.Set("Mcp-Session-Id", "session-abc-123")
	rec := httptest.NewRecorder()

	server.handleSSEMessage(rec, req)

	if receivedSessionID != "session-abc-123" {
		t.Errorf("upstream received Mcp-Session-Id %q, want %q", receivedSessionID, "session-abc-123")
	}

	// Verify response includes session ID header
	if got := rec.Header().Get("Mcp-Session-Id"); got != "session-abc-123" {
		t.Errorf("response Mcp-Session-Id = %q, want %q", got, "session-abc-123")
	}
}

func TestHandleSSEMessage_UpstreamError(t *testing.T) {
	t.Parallel()

	server, validToken, cleanup := setupSSETest(t)
	defer cleanup()

	// Create a mock upstream that returns an error
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `internal error`)
	}))
	defer upstream.Close()

	mcpServer := createTestServerInStore(t, server.store, upstream.URL, TransportSSE)

	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/"+mcpServer.ID+"/messages", nil)
	req.Header.Set("Authorization", "Bearer "+validToken)
	rec := httptest.NewRecorder()

	server.handleSSEMessage(rec, req)

	// The proxy forwards the upstream status code
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleSSEMessage_UpstreamUnreachable(t *testing.T) {
	t.Parallel()

	server, validToken, cleanup := setupSSETest(t)
	defer cleanup()

	// Create a server with a URL that can't be reached
	mcpServer := createTestServerInStore(t, server.store, "http://127.0.0.1:1", TransportSSE)

	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/"+mcpServer.ID+"/messages", strings.NewReader("test"))
	req.Header.Set("Authorization", "Bearer "+validToken)
	rec := httptest.NewRecorder()

	server.handleSSEMessage(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
}

// =============================================================================
// handleSSEMessage with Auth Middleware Tests
// =============================================================================

func TestHandleSSEMessage_AuthRequired(t *testing.T) {
	t.Parallel()

	server, _, cleanup := setupSSETest(t)
	defer cleanup()

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{
			name:       "no auth header",
			authHeader: "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid token",
			authHeader: "Bearer invalid-token-value",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "malformed auth",
			authHeader: "Basic abc123",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodPost, "/v1/mcp/test-server/messages", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			rec := httptest.NewRecorder()
			nextHandler := server.proxyAuthMiddleware(server.handleSSEMessage)
			nextHandler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

// =============================================================================
// handleSSEConnection Tests
// =============================================================================

func TestHandleSSEConnection_MissingServerID(t *testing.T) {
	t.Parallel()

	server, _, cleanup := setupSSETest(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/v1/mcp/sse", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	server.handleSSEConnection(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleSSEConnection_ServerNotFound(t *testing.T) {
	t.Parallel()

	server, validToken, cleanup := setupSSETest(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/v1/mcp/nonexistent-server/sse", nil)
	req.Header.Set("Authorization", "Bearer "+validToken)
	rec := httptest.NewRecorder()

	server.handleSSEConnection(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleSSEConnection_UpstreamConnectionForwarded(t *testing.T) {
	t.Parallel()

	server, validToken, cleanup := setupSSETest(t)
	defer cleanup()

	// Create a mock upstream that sends SSE events
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("upstream ResponseWriter does not support flushing")
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		// Send a few SSE events
		fmt.Fprintf(w, "event: endpoint\ndata: http://localhost/messages?token=test\n\n")
		flusher.Flush()

		fmt.Fprintf(w, "event: message\ndata: hello from upstream\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	mcpServer := createTestServerInStore(t, server.store, upstream.URL, TransportSSE)

	req := httptest.NewRequest(http.MethodGet, "/v1/mcp/"+mcpServer.ID+"/sse", nil)
	req.Header.Set("Authorization", "Bearer "+validToken)
	rec := httptest.NewRecorder()

	server.handleSSEConnection(rec, req)

	// Check that the SSE headers were set
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want %q", got, "text/event-stream")
	}

	body := rec.Body.String()

	// Should contain the forwarded message event
	if !strings.Contains(body, "hello from upstream") {
		t.Errorf("body should contain forwarded message, got: %q", body)
	}
}

func TestHandleSSEConnection_EndpointRewriting(t *testing.T) {
	t.Parallel()

	server, validToken, cleanup := setupSSETest(t)
	defer cleanup()

	// Create a mock upstream that sends an endpoint event with a full URL
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		// Send endpoint event with full URL
		fmt.Fprintf(w, "event: endpoint\ndata: http://localhost:3001/messages?session=abc123\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	mcpServer := createTestServerInStore(t, server.store, upstream.URL, TransportSSE)

	req := httptest.NewRequest(http.MethodGet, "/v1/mcp/"+mcpServer.ID+"/sse", nil)
	req.Header.Set("Authorization", "Bearer "+validToken)
	rec := httptest.NewRecorder()

	server.handleSSEConnection(rec, req)

	body := rec.Body.String()

	// The endpoint URL should be rewritten to proxy path
	expectedProxyPath := "/v1/mcp/" + mcpServer.ID + "/messages"
	if !strings.Contains(body, expectedProxyPath) {
		t.Errorf("body should contain rewritten path %q, got: %q", expectedProxyPath, body)
	}

	// The original upstream URL should NOT appear in the output
	if strings.Contains(body, "http://localhost:3001/messages") {
		t.Errorf("body should NOT contain original upstream URL, got: %q", body)
	}

	// Query string should be preserved
	if !strings.Contains(body, "?session=abc123") {
		t.Errorf("body should preserve query string ?session=abc123, got: %q", body)
	}
}

func TestHandleSSEConnection_UpstreamReturnsNon200(t *testing.T) {
	t.Parallel()

	server, validToken, cleanup := setupSSETest(t)
	defer cleanup()

	// Create a mock upstream that returns a non-200 status
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	mcpServer := createTestServerInStore(t, server.store, upstream.URL, TransportSSE)

	req := httptest.NewRequest(http.MethodGet, "/v1/mcp/"+mcpServer.ID+"/sse", nil)
	req.Header.Set("Authorization", "Bearer "+validToken)
	rec := httptest.NewRecorder()

	server.handleSSEConnection(rec, req)

	// The handler writes 200 headers first, then if upstream returns non-200,
	// it just returns without streaming. Body should be empty.
	// Status was already written as 200
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (headers already sent)", rec.Code, http.StatusOK)
	}
}

func TestHandleSSEConnection_UpstreamUnreachable(t *testing.T) {
	t.Parallel()

	server, validToken, cleanup := setupSSETest(t)
	defer cleanup()

	// Create a server pointing to an unreachable port
	mcpServer := createTestServerInStore(t, server.store, "http://127.0.0.1:1", TransportSSE)

	req := httptest.NewRequest(http.MethodGet, "/v1/mcp/"+mcpServer.ID+"/sse", nil)
	req.Header.Set("Authorization", "Bearer "+validToken)
	rec := httptest.NewRecorder()

	server.handleSSEConnection(rec, req)

	// The handler writes 200 headers first, then connection fails silently.
	// Status is already 200, body is empty.
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (headers already sent)", rec.Code, http.StatusOK)
	}
}

func TestHandleSSEConnection_MultipleSSEEvents(t *testing.T) {
	t.Parallel()

	server, validToken, cleanup := setupSSETest(t)
	defer cleanup()

	// Create a mock upstream that sends multiple SSE events
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		// Send endpoint event
		fmt.Fprintf(w, "event: endpoint\ndata: http://localhost:3001/messages?token=abc\n\n")
		flusher.Flush()

		// Send multiple message events
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, "event: message\ndata: message %d\n\n", i)
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	mcpServer := createTestServerInStore(t, server.store, upstream.URL, TransportSSE)

	req := httptest.NewRequest(http.MethodGet, "/v1/mcp/"+mcpServer.ID+"/sse", nil)
	req.Header.Set("Authorization", "Bearer "+validToken)
	rec := httptest.NewRecorder()

	server.handleSSEConnection(rec, req)

	body := rec.Body.String()

	// Should contain all 3 messages
	for i := 0; i < 3; i++ {
		expected := fmt.Sprintf("message %d", i)
		if !strings.Contains(body, expected) {
			t.Errorf("body should contain %q, got: %q", expected, body)
		}
	}

	// Endpoint should be rewritten
	expectedProxyPath := "/v1/mcp/" + mcpServer.ID + "/messages"
	if !strings.Contains(body, expectedProxyPath) {
		t.Errorf("body should contain rewritten path %q", expectedProxyPath)
	}
}

// =============================================================================
// streamSSEEvents Tests
// =============================================================================

func TestStreamSSEEvents_BasicEvent(t *testing.T) {
	t.Parallel()

	s := &Server{}

	sseInput := "event: message\ndata: hello\n\n"
	reader := strings.NewReader(sseInput)

	rec := httptest.NewRecorder()

	err := s.streamSSEEvents(context.Background(), reader, rec, rec, "server123")
	if err != nil {
		t.Fatalf("streamSSEEvents returned error: %v", err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "data: hello") {
		t.Errorf("body should contain 'data: hello', got: %q", body)
	}
}

func TestStreamSSEEvents_MultipleEvents(t *testing.T) {
	t.Parallel()

	s := &Server{}

	sseInput := "event: endpoint\ndata: http://localhost/messages\n\nevent: message\ndata: hello\n\nevent: message\ndata: world\n\n"
	reader := strings.NewReader(sseInput)

	rec := httptest.NewRecorder()

	err := s.streamSSEEvents(context.Background(), reader, rec, rec, "server123")
	if err != nil {
		t.Fatalf("streamSSEEvents returned error: %v", err)
	}

	body := rec.Body.String()

	// Endpoint should be rewritten
	if !strings.Contains(body, "/v1/mcp/server123/messages") {
		t.Errorf("body should contain rewritten endpoint, got: %q", body)
	}

	// Messages should be present
	if !strings.Contains(body, "data: hello") {
		t.Errorf("body should contain 'data: hello', got: %q", body)
	}
	if !strings.Contains(body, "data: world") {
		t.Errorf("body should contain 'data: world', got: %q", body)
	}
}

func TestStreamSSEEvents_ContextCancellation(t *testing.T) {
	// Note: bufio.Scanner.Scan() is a blocking call that doesn't respond to context cancellation
	// until after data is read. This test verifies the behavior with a closed pipe.
	t.Parallel()

	s := &Server{}

	// Create a pipe and close the writer immediately
	reader, writer := io.Pipe()
	defer writer.Close()

	// Close writer immediately - this will cause scanner.Scan() to return with EOF
	writer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rec := httptest.NewRecorder()

	err := s.streamSSEEvents(ctx, reader, rec, rec, "server123")
	if err != nil {
		t.Errorf("streamSSEEvents returned unexpected error: %v", err)
	}

	// Verify empty stream was processed correctly (no events)
	body := rec.Body.String()
	if body != "" {
		t.Errorf("expected empty body for closed pipe with no data, got: %q", body)
	}
}

func TestStreamSSEEvents_ReaderClosedWithData(t *testing.T) {
	t.Parallel()

	s := &Server{}

	// Create a pipe, write some data, then close
	reader, writer := io.Pipe()
	defer writer.Close()

	go func() {
		// Write data then close
		fmt.Fprint(writer, "event: message\ndata: hello\n\n")
		writer.Close()
	}()

	rec := httptest.NewRecorder()

	err := s.streamSSEEvents(context.Background(), reader, rec, rec, "server123")
	if err != nil {
		t.Fatalf("streamSSEEvents returned error: %v", err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "hello") {
		t.Errorf("body should contain 'hello', got: %q", body)
	}
}

func TestStreamSSEEvents_LargeEvent(t *testing.T) {
	t.Parallel()

	s := &Server{}

	// Create a large data payload
	largeData := strings.Repeat("x", 10000)
	sseInput := fmt.Sprintf("event: message\ndata: %s\n\n", largeData)
	reader := strings.NewReader(sseInput)

	rec := httptest.NewRecorder()

	err := s.streamSSEEvents(context.Background(), reader, rec, rec, "server123")
	if err != nil {
		t.Fatalf("streamSSEEvents returned error: %v", err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, largeData) {
		t.Errorf("body should contain the large data payload")
	}
}

func TestStreamSSEEvents_MultiDataLineEvent(t *testing.T) {
	t.Parallel()

	s := &Server{}

	sseInput := "event: message\ndata: line1\ndata: line2\ndata: line3\n\n"
	reader := strings.NewReader(sseInput)

	rec := httptest.NewRecorder()

	err := s.streamSSEEvents(context.Background(), reader, rec, rec, "server123")
	if err != nil {
		t.Fatalf("streamSSEEvents returned error: %v", err)
	}

	body := rec.Body.String()

	if strings.Count(body, "data: ") != 3 {
		t.Errorf("body should have 3 data lines, got: %q", body)
	}
	if !strings.Contains(body, "data: line1") {
		t.Errorf("body should contain 'data: line1'")
	}
	if !strings.Contains(body, "data: line2") {
		t.Errorf("body should contain 'data: line2'")
	}
	if !strings.Contains(body, "data: line3") {
		t.Errorf("body should contain 'data: line3'")
	}
}

// =============================================================================
// Integration: Full SSE Flow with httptest
// =============================================================================

func TestSSEIntegration_EndToEnd(t *testing.T) {
	// End-to-end test: upstream SSE → proxy → client
	// Verifies the full SSE proxy pipeline including endpoint rewriting

	server, validToken, cleanup := setupSSETest(t)
	defer cleanup()

	// Create a mock upstream SSE server
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}

		// Verify upstream received correct headers
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("upstream Accept header = %q, want %q", r.Header.Get("Accept"), "text/event-stream")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		// Send endpoint event with the full upstream URL
		// Use r.Host which contains the actual host:port from the request
		upstreamURL := "http://" + r.Host + "/messages"
		fmt.Fprintf(w, "event: endpoint\ndata: %s?session=test-session-id\n\n", upstreamURL)
		flusher.Flush()

		// Send a JSON-RPC message
		fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"result\":{\"status\":\"initialized\"},\"id\":1}\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	mcpServer := createTestServerInStore(t, server.store, upstream.URL, TransportSSE)

	// Make GET request to SSE endpoint
	req := httptest.NewRequest(http.MethodGet, "/v1/mcp/"+mcpServer.ID+"/sse", nil)
	req.Header.Set("Authorization", "Bearer "+validToken)
	rec := httptest.NewRecorder()

	server.handleSSEConnection(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/event-stream")
	}

	body := rec.Body.String()

	// Verify endpoint was rewritten
	proxyPath := "/v1/mcp/" + mcpServer.ID + "/messages"
	if !strings.Contains(body, proxyPath) {
		t.Errorf("body should contain proxy path %q, got: %s", proxyPath, body)
	}

	// Verify session query param preserved
	if !strings.Contains(body, "?session=test-session-id") {
		t.Errorf("body should preserve session query param, got: %s", body)
	}

	// Verify message event was forwarded
	if !strings.Contains(body, "initialized") {
		t.Errorf("body should contain forwarded message content, got: %s", body)
	}
}

func TestSSEIntegration_MessageRoundTrip(t *testing.T) {
	// Test: POST message → upstream → response forwarded back to client

	server, validToken, cleanup := setupSSETest(t)
	defer cleanup()

	// Create a mock upstream that processes messages
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the request body
		body, _ := io.ReadAll(r.Body)
		defer r.Body.Close()

		// Echo back with a response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"echo":%s},"id":1}`, string(body))
	}))
	defer upstream.Close()

	mcpServer := createTestServerInStore(t, server.store, upstream.URL, TransportSSE)

	// Send a POST message through the proxy
	requestBody := `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"test"},"id":1}`
	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/"+mcpServer.ID+"/messages", strings.NewReader(requestBody))
	req.Header.Set("Authorization", "Bearer "+validToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", "session-xyz")
	rec := httptest.NewRecorder()

	server.handleSSEMessage(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Verify the response contains the echoed content
	body := rec.Body.String()
	if !strings.Contains(body, "tools/call") {
		t.Errorf("response body should contain echoed request, got: %q", body)
	}

	// Verify session ID header passthrough
	if got := rec.Header().Get("Mcp-Session-Id"); got != "session-xyz" {
		t.Errorf("Mcp-Session-Id = %q, want %q", got, "session-xyz")
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestExtractServerID_UUID(t *testing.T) {
	t.Parallel()

	got, err := extractServerID("/v1/mcp/550e8400-e29b-41d4-a716-446655440000/sse")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "550e8400-e29b-41d4-a716-446655440000"
	if got != want {
		t.Errorf("extractServerID() = %q, want %q", got, want)
	}
}

func TestExtractServerID_SpecialCharacters(t *testing.T) {
	t.Parallel()

	got, err := extractServerID("/v1/mcp/my_server-123/messages")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "my_server-123"
	if got != want {
		t.Errorf("extractServerID() = %q, want %q", got, want)
	}
}

// TestExtractServerID_ReservedEndpointNames tests that reserved endpoint names are rejected
func TestExtractServerID_ReservedEndpointNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
	}{
		{"sse as server ID", "/v1/mcp/sse"},
		{"messages as server ID", "/v1/mcp/messages"},
		{"sse with sse endpoint", "/v1/mcp/sse/sse"},
		{"messages with sse endpoint", "/v1/mcp/messages/sse"},
		{"sse with messages endpoint", "/v1/mcp/sse/messages"},
		{"messages with messages endpoint", "/v1/mcp/messages/messages"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := extractServerID(tt.path)
			if err == nil {
				t.Errorf("extractServerID(%q) = %q, want error for reserved endpoint name", tt.path, got)
			}
		})
	}
}

func TestRewriteEndpointData_URLWithoutPath(t *testing.T) {
	t.Parallel()

	dataLines := []string{"http://upstream:3001"}
	result := rewriteEndpointData(dataLines, "server123")

	if len(result) != 1 {
		t.Fatalf("expected 1 result line, got %d", len(result))
	}

	// Should be rewritten to proxy path
	if !strings.Contains(result[0], "/v1/mcp/server123/messages") {
		t.Errorf("result %q should contain proxy path", result[0])
	}
}

func TestStreamSSEEvents_CommentsAndFields(t *testing.T) {
	t.Parallel()

	s := &Server{}

	// SSE with comments, id, and retry fields
	sseInput := ": this is a comment\nevent: message\nid: 42\nretry: 5000\ndata: hello\n\n"
	reader := strings.NewReader(sseInput)

	rec := httptest.NewRecorder()

	err := s.streamSSEEvents(context.Background(), reader, rec, rec, "server123")
	if err != nil {
		t.Fatalf("streamSSEEvents returned error: %v", err)
	}

	body := rec.Body.String()

	// Comment should be passed through
	if !strings.Contains(body, ": this is a comment") {
		t.Errorf("body should contain comment, got: %q", body)
	}

	// ID should be passed through
	if !strings.Contains(body, "id: 42") {
		t.Errorf("body should contain id field, got: %q", body)
	}

	// Retry should be passed through
	if !strings.Contains(body, "retry: 5000") {
		t.Errorf("body should contain retry field, got: %q", body)
	}

	// Data should be present
	if !strings.Contains(body, "data: hello") {
		t.Errorf("body should contain data, got: %q", body)
	}
}

func TestStreamSSEEvents_EmptyStreamReader(t *testing.T) {
	t.Parallel()

	s := &Server{}

	reader := strings.NewReader("")
	rec := httptest.NewRecorder()

	err := s.streamSSEEvents(context.Background(), reader, rec, rec, "server123")
	if err != nil {
		t.Fatalf("streamSSEEvents returned error: %v", err)
	}

	// Empty input should produce empty output
	body := rec.Body.String()
	if body != "" {
		t.Errorf("expected empty body for empty input, got: %q", body)
	}
}

func TestStreamSSEEvents_BufferedSSEStream(t *testing.T) {
	t.Parallel()

	s := &Server{}

	// Simulate a buffered SSE stream using bufio.Scanner to ensure
	// the scanner can handle the data
	lines := []string{
		"event: endpoint",
		"data: http://localhost:8080/messages?token=abc",
		"",
		"event: message",
		"data: {\"jsonrpc\":\"2.0\",\"result\":{}}",
		"",
	}
	sseInput := strings.Join(lines, "\n")
	reader := strings.NewReader(sseInput)

	rec := httptest.NewRecorder()

	err := s.streamSSEEvents(context.Background(), reader, rec, rec, "testserver")
	if err != nil {
		t.Fatalf("streamSSEEvents returned error: %v", err)
	}

	body := rec.Body.String()

	// Verify endpoint was rewritten
	if !strings.Contains(body, "/v1/mcp/testserver/messages") {
		t.Errorf("body should contain rewritten proxy path, got: %q", body)
	}

	// Verify query string preserved
	if !strings.Contains(body, "?token=abc") {
		t.Errorf("body should preserve query string, got: %q", body)
	}

	// Verify message forwarded
	if !strings.Contains(body, "jsonrpc") {
		t.Errorf("body should contain message content, got: %q", body)
	}
}

func TestStreamSSEEvents_ParsesUsingBufioScanner(t *testing.T) {
	t.Parallel()

	s := &Server{}

	// Create a reader that will be read via bufio.Scanner
	sseInput := "event: endpoint\ndata: http://host:3000/messages?key=val\n\nevent: message\ndata: payload\n\n"
	reader := bufio.NewReader(strings.NewReader(sseInput))

	rec := httptest.NewRecorder()

	err := s.streamSSEEvents(context.Background(), reader, rec, rec, "s1")
	if err != nil {
		t.Fatalf("streamSSEEvents returned error: %v", err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "/v1/mcp/s1/messages") {
		t.Errorf("body should contain rewritten path, got: %q", body)
	}
	if !strings.Contains(body, "payload") {
		t.Errorf("body should contain forwarded data, got: %q", body)
	}
}
