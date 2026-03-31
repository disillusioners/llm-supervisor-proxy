package ui

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// testServer creates a Server with a test database
type testServer struct {
	*Server
	db *sql.DB
}

func setupTestServer(t *testing.T) *testServer {
	t.Helper()

	// Create in-memory SQLite database
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	// Create auth_tokens table
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS auth_tokens (
		id TEXT PRIMARY KEY,
		token_hash TEXT NOT NULL,
		name TEXT NOT NULL,
		expires_at TEXT,
		created_at TEXT NOT NULL,
		created_by TEXT NOT NULL
	)`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}

	// Create token_hourly_usage table
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS token_hourly_usage (
		token_id TEXT NOT NULL,
		hour_bucket TEXT NOT NULL,
		request_count INTEGER NOT NULL DEFAULT 0,
		prompt_tokens INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0,
		total_tokens INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (token_id, hour_bucket)
	)`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}

	// Create database store
	dbStore := &database.Store{
		DB:      db,
		Dialect: database.SQLite,
	}

	// Create a minimal server for testing
	s := &Server{
		dbStore: dbStore,
	}

	t.Cleanup(func() { db.Close() })

	return &testServer{
		Server: s,
		db:     db,
	}
}

func TestHandleUsage_BasicQuery(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Insert test tokens
	_, err := ts.db.Exec(`INSERT INTO auth_tokens (id, token_hash, name, created_at, created_by) VALUES
		('token1', 'hash1', 'Test Token 1', '2024-01-01T00:00:00Z', 'test'),
		('token2', 'hash2', 'Test Token 2', '2024-01-01T00:00:00Z', 'test')`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert test usage data
	_, err = ts.db.Exec(`INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens) VALUES
		('token1', '2024-01-01T10:00', 5, 1000, 500, 1500),
		('token1', '2024-01-01T11:00', 3, 600, 300, 900),
		('token2', '2024-01-01T10:00', 2, 400, 200, 600)`)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage?from=2024-01-01T09&to=2024-01-01T12", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ts.handleUsage(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp UsageResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Data) != 3 {
		t.Errorf("expected 3 data rows, got %d", len(resp.Data))
	}

	if resp.Totals.RequestCount != 10 {
		t.Errorf("expected total requests = 10, got %d", resp.Totals.RequestCount)
	}
	if resp.Totals.PromptTokens != 2000 {
		t.Errorf("expected total prompt tokens = 2000, got %d", resp.Totals.PromptTokens)
	}
}

func TestHandleUsage_WithTokenIDFilter(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Insert test token
	_, err := ts.db.Exec(`INSERT INTO auth_tokens (id, token_hash, name, created_at, created_by) VALUES
		('token1', 'hash1', 'Test Token 1', '2024-01-01T00:00:00Z', 'test')`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert test usage data
	_, err = ts.db.Exec(`INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens) VALUES
		('token1', '2024-01-01T10:00', 5, 1000, 500, 1500),
		('token1', '2024-01-01T11:00', 3, 600, 300, 900),
		('token2', '2024-01-01T10:00', 2, 400, 200, 600)`)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage?token_id=token1&from=2024-01-01T09&to=2024-01-01T12", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ts.handleUsage(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp UsageResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.TokenID != "token1" {
		t.Errorf("expected token_id = token1, got %s", resp.TokenID)
	}

	if len(resp.Data) != 2 {
		t.Errorf("expected 2 data rows for token1, got %d", len(resp.Data))
	}

	// Should not include token2 data
	for _, row := range resp.Data {
		if row.TokenName == "Test Token 2" {
			t.Error("should not include data from token2")
		}
	}
}

func TestHandleUsage_WithDateRangeFilter(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Insert test token
	_, err := ts.db.Exec(`INSERT INTO auth_tokens (id, token_hash, name, created_at, created_by) VALUES
		('token1', 'hash1', 'Test Token 1', '2024-01-01T00:00:00Z', 'test')`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert test usage data spanning multiple days
	_, err = ts.db.Exec(`INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens) VALUES
		('token1', '2024-01-01T10:00', 5, 1000, 500, 1500),
		('token1', '2024-01-02T10:00', 10, 2000, 1000, 3000),
		('token1', '2024-01-03T10:00', 15, 3000, 1500, 4500)`)
	if err != nil {
		t.Fatal(err)
	}

	// Query only Jan 2
	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage?token_id=token1&from=2024-01-02T00&to=2024-01-02T23", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ts.handleUsage(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp UsageResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Data) != 1 {
		t.Errorf("expected 1 data row for Jan 2, got %d", len(resp.Data))
	}

	if resp.Totals.RequestCount != 10 {
		t.Errorf("expected total requests = 10, got %d", resp.Totals.RequestCount)
	}
}

func TestHandleUsage_EmptyResult(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// No test data inserted - should return empty result

	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage?from=2024-01-01T09&to=2024-01-01T12", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ts.handleUsage(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp UsageResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Data) != 0 {
		t.Errorf("expected 0 data rows, got %d", len(resp.Data))
	}

	if resp.Totals.RequestCount != 0 {
		t.Errorf("expected total requests = 0, got %d", resp.Totals.RequestCount)
	}
}

func TestHandleUsageTokens(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Insert test tokens
	_, err := ts.db.Exec(`INSERT INTO auth_tokens (id, token_hash, name, created_at, created_by) VALUES
		('token1', 'hash1', 'Test Token 1', '2024-01-01T00:00:00Z', 'test'),
		('token2', 'hash2', 'Test Token 2', '2024-01-01T00:00:00Z', 'test')`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert test usage data
	_, err = ts.db.Exec(`INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens) VALUES
		('token1', '2024-01-01T10:00', 5, 1000, 500, 1500),
		('token1', '2024-01-01T11:00', 3, 600, 300, 900),
		('token2', '2024-01-01T10:00', 2, 400, 200, 600)`)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage/tokens", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ts.handleUsageTokens(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp UsageTokensResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Tokens) != 2 {
		t.Errorf("expected 2 tokens, got %d", len(resp.Tokens))
	}

	// Find token1 summary
	var token1Summary *UsageTokenSummary
	for i := range resp.Tokens {
		if resp.Tokens[i].TokenID == "token1" {
			token1Summary = &resp.Tokens[i]
			break
		}
	}

	if token1Summary == nil {
		t.Fatal("token1 summary not found")
	}

	if token1Summary.Name != "Test Token 1" {
		t.Errorf("expected token1 name = 'Test Token 1', got %s", token1Summary.Name)
	}

	if token1Summary.TotalRequests != 8 {
		t.Errorf("expected token1 total requests = 8, got %d", token1Summary.TotalRequests)
	}

	if token1Summary.TotalTokens != 2400 {
		t.Errorf("expected token1 total tokens = 2400, got %d", token1Summary.TotalTokens)
	}
}

func TestHandleUsageTokens_EmptyResult(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// No test data inserted - should return empty result

	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage/tokens", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ts.handleUsageTokens(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp UsageTokensResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Tokens) != 0 {
		t.Errorf("expected 0 tokens, got %d", len(resp.Tokens))
	}
}

func TestHandleUsageSummary(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Insert test tokens
	_, err := ts.db.Exec(`INSERT INTO auth_tokens (id, token_hash, name, created_at, created_by) VALUES
		('token1', 'hash1', 'Test Token 1', '2024-01-01T00:00:00Z', 'test'),
		('token2', 'hash2', 'Test Token 2', '2024-01-01T00:00:00Z', 'test')`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert test usage data
	_, err = ts.db.Exec(`INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens) VALUES
		('token1', '2024-01-01T10:00', 5, 1000, 500, 1500),
		('token1', '2024-01-01T11:00', 3, 600, 300, 900),
		('token2', '2024-01-01T10:00', 10, 2000, 1000, 3000),
		('token2', '2024-01-01T11:00', 2, 400, 200, 600)`)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage/summary?from=2024-01-01T09&to=2024-01-01T12", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ts.handleUsageSummary(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp UsageSummaryResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Tokens) != 2 {
		t.Errorf("expected 2 tokens, got %d", len(resp.Tokens))
	}

	if resp.GrandTotal.TotalRequests != 20 {
		t.Errorf("expected grand total requests = 20, got %d", resp.GrandTotal.TotalRequests)
	}

	if resp.GrandTotal.TotalPromptTokens != 4000 {
		t.Errorf("expected grand total prompt tokens = 4000, got %d", resp.GrandTotal.TotalPromptTokens)
	}

	// Peak hour should be 2024-01-01T10:00 with 15 requests (5 + 10)
	if resp.GrandTotal.PeakHour != "2024-01-01T10:00" {
		t.Errorf("expected peak hour = '2024-01-01T10:00', got %s", resp.GrandTotal.PeakHour)
	}
}

func TestHandleUsageSummary_WithTokenIDFilter(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Insert test token
	_, err := ts.db.Exec(`INSERT INTO auth_tokens (id, token_hash, name, created_at, created_by) VALUES
		('token1', 'hash1', 'Test Token 1', '2024-01-01T00:00:00Z', 'test')`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert test usage data
	_, err = ts.db.Exec(`INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens) VALUES
		('token1', '2024-01-01T10:00', 5, 1000, 500, 1500),
		('token1', '2024-01-01T11:00', 10, 2000, 1000, 3000)`)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage/summary?token_id=token1&from=2024-01-01T09&to=2024-01-01T12", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ts.handleUsageSummary(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp UsageSummaryResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Tokens) != 1 {
		t.Errorf("expected 1 token, got %d", len(resp.Tokens))
	}

	if resp.GrandTotal.TotalRequests != 15 {
		t.Errorf("expected grand total requests = 15, got %d", resp.GrandTotal.TotalRequests)
	}

	// Peak hour should be 2024-01-01T11:00 with 10 requests
	if resp.GrandTotal.PeakHour != "2024-01-01T11:00" {
		t.Errorf("expected peak hour = '2024-01-01T11:00', got %s", resp.GrandTotal.PeakHour)
	}
}

func TestHandleUsageSummary_EmptyResult(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// No test data inserted - should return empty result

	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage/summary?from=2024-01-01T09&to=2024-01-01T12", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ts.handleUsageSummary(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp UsageSummaryResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Tokens) != 0 {
		t.Errorf("expected 0 tokens, got %d", len(resp.Tokens))
	}

	if resp.GrandTotal.TotalRequests != 0 {
		t.Errorf("expected grand total requests = 0, got %d", resp.GrandTotal.TotalRequests)
	}
}

func TestHandleUsage_MethodNotAllowed(t *testing.T) {
	ts := setupTestServer(t)

	methods := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}
	for _, method := range methods {
		req := httptest.NewRequest(method, "/fe/api/usage", nil)
		w := httptest.NewRecorder()

		ts.handleUsage(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status 405 for method %s, got %d", method, w.Code)
		}
	}
}

func TestHandleUsageTokens_MethodNotAllowed(t *testing.T) {
	ts := setupTestServer(t)

	methods := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}
	for _, method := range methods {
		req := httptest.NewRequest(method, "/fe/api/usage/tokens", nil)
		w := httptest.NewRecorder()

		ts.handleUsageTokens(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status 405 for method %s, got %d", method, w.Code)
		}
	}
}

func TestHandleUsageSummary_MethodNotAllowed(t *testing.T) {
	ts := setupTestServer(t)

	methods := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}
	for _, method := range methods {
		req := httptest.NewRequest(method, "/fe/api/usage/summary", nil)
		w := httptest.NewRecorder()

		ts.handleUsageSummary(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status 405 for method %s, got %d", method, w.Code)
		}
	}
}
