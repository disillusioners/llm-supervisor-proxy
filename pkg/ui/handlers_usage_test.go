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

// =============================================================================
// Additional comprehensive tests for coverage gaps
// =============================================================================

// Test handleUsage_NonExistentTokenID tests that querying a non-existent token returns empty array
func TestHandleUsage_NonExistentTokenID(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Insert some tokens and usage data
	_, err := ts.db.Exec(`INSERT INTO auth_tokens (id, token_hash, name, created_at, created_by) VALUES
		('token1', 'hash1', 'Test Token 1', '2024-01-01T00:00:00Z', 'test')`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ts.db.Exec(`INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens) VALUES
		('token1', '2024-01-01T10:00', 5, 1000, 500, 1500)`)
	if err != nil {
		t.Fatal(err)
	}

	// Query non-existent token
	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage?token_id=nonexistent&from=2024-01-01T09&to=2024-01-01T12", nil)
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
		t.Errorf("expected 0 data rows for non-existent token, got %d", len(resp.Data))
	}

	if resp.Totals.RequestCount != 0 {
		t.Errorf("expected total requests = 0, got %d", resp.Totals.RequestCount)
	}
}

// Test handleUsage_CombinedFilters tests that combining token_id with date filters works
func TestHandleUsage_CombinedFilters(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Insert test token
	_, err := ts.db.Exec(`INSERT INTO auth_tokens (id, token_hash, name, created_at, created_by) VALUES
		('token1', 'hash1', 'Test Token 1', '2024-01-01T00:00:00Z', 'test')`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert usage data spanning multiple days and hours
	_, err = ts.db.Exec(`INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens) VALUES
		('token1', '2024-01-01T08:00', 5, 1000, 500, 1500),
		('token1', '2024-01-01T10:00', 10, 2000, 1000, 3000),
		('token1', '2024-01-01T12:00', 15, 3000, 1500, 4500),
		('token1', '2024-01-02T10:00', 20, 4000, 2000, 6000),
		('token1', '2024-01-03T10:00', 25, 5000, 2500, 7500)`)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name          string
		tokenID       string
		from          string
		to            string
		expectedCount int
		expectedTotal int
	}{
		{
			name:          "token1 with range 09-11 should match 10:00 only",
			tokenID:       "token1",
			from:          "2024-01-01T09",
			to:            "2024-01-01T11",
			expectedCount: 1,
			expectedTotal: 10,
		},
		{
			name:          "token1 with range 07-11 should match 08:00 and 10:00",
			tokenID:       "token1",
			from:          "2024-01-01T07",
			to:            "2024-01-01T11",
			expectedCount: 2,
			expectedTotal: 15,
		},
		{
			name:          "token1 with range spanning multiple days",
			tokenID:       "token1",
			from:          "2024-01-01T00",
			to:            "2024-01-02T23",
			expectedCount: 4,
			expectedTotal: 50,
		},
		{
			name:          "wide range covering all data",
			tokenID:       "token1",
			from:          "2024-01-01T00",
			to:            "2024-01-03T23",
			expectedCount: 5,
			expectedTotal: 75,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/fe/api/usage?token_id=" + tt.tokenID + "&from=" + tt.from + "&to=" + tt.to
			req := httptest.NewRequest(http.MethodGet, url, nil)
			req = req.WithContext(ctx)
			w := httptest.NewRecorder()

			ts.handleUsage(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
			}

			var resp UsageResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if len(resp.Data) != tt.expectedCount {
				t.Errorf("expected %d data rows, got %d", tt.expectedCount, len(resp.Data))
			}

			if resp.Totals.RequestCount != tt.expectedTotal {
				t.Errorf("expected total requests = %d, got %d", tt.expectedTotal, resp.Totals.RequestCount)
			}
		})
	}
}

// Test handleUsage_ViewParameter tests the view query parameter
func TestHandleUsage_ViewParameter(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Insert test token
	_, err := ts.db.Exec(`INSERT INTO auth_tokens (id, token_hash, name, created_at, created_by) VALUES
		('token1', 'hash1', 'Test Token 1', '2024-01-01T00:00:00Z', 'test')`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ts.db.Exec(`INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens) VALUES
		('token1', '2024-01-01T10:00', 5, 1000, 500, 1500)`)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage?from=2024-01-01T09&to=2024-01-01T12&view=daily", nil)
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

	// View should be set to the provided value
	if resp.View != "daily" {
		t.Errorf("expected view = 'daily', got %s", resp.View)
	}
}

// Test handleUsage_DefaultView tests that default view is "hourly"
func TestHandleUsage_DefaultView(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

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

	if resp.View != "hourly" {
		t.Errorf("expected default view = 'hourly', got %s", resp.View)
	}
}

// Test handleUsage_DatabaseNotConfigured tests 503 when dbStore is nil
func TestHandleUsage_DatabaseNotConfigured(t *testing.T) {
	s := &Server{dbStore: nil}
	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage", nil)
	w := httptest.NewRecorder()

	s.handleUsage(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", w.Code)
	}
}

// Test handleUsageTokens_DatabaseNotConfigured tests 503 when dbStore is nil
func TestHandleUsageTokens_DatabaseNotConfigured(t *testing.T) {
	s := &Server{dbStore: nil}
	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage/tokens", nil)
	w := httptest.NewRecorder()

	s.handleUsageTokens(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", w.Code)
	}
}

// Test handleUsageSummary_DatabaseNotConfigured tests 503 when dbStore is nil
func TestHandleUsageSummary_DatabaseNotConfigured(t *testing.T) {
	s := &Server{dbStore: nil}
	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage/summary", nil)
	w := httptest.NewRecorder()

	s.handleUsageSummary(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", w.Code)
	}
}

// Test handleUsageTokens_Ordering tests that tokens are returned in order by token_id
func TestHandleUsageTokens_Ordering(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Insert tokens out of order
	_, err := ts.db.Exec(`INSERT INTO auth_tokens (id, token_hash, name, created_at, created_by) VALUES
		('zzz-token', 'hash1', 'Z Token', '2024-01-01T00:00:00Z', 'test'),
		('aaa-token', 'hash2', 'A Token', '2024-01-01T00:00:00Z', 'test'),
		('mmm-token', 'hash3', 'M Token', '2024-01-01T00:00:00Z', 'test')`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ts.db.Exec(`INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens) VALUES
		('zzz-token', '2024-01-01T10:00', 1, 100, 50, 150),
		('aaa-token', '2024-01-01T10:00', 1, 100, 50, 150),
		('mmm-token', '2024-01-01T10:00', 1, 100, 50, 150)`)
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

	if len(resp.Tokens) != 3 {
		t.Fatalf("expected 3 tokens, got %d", len(resp.Tokens))
	}

	// Verify alphabetical ordering
	expectedOrder := []string{"aaa-token", "mmm-token", "zzz-token"}
	for i, expected := range expectedOrder {
		if resp.Tokens[i].TokenID != expected {
			t.Errorf("position %d: expected token_id = %s, got %s", i, expected, resp.Tokens[i].TokenID)
		}
	}
}

// Test handleUsageTokens_TokenWithNoUsage tests that tokens without usage are not returned
func TestHandleUsageTokens_TokenWithNoUsage(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Insert tokens - one with usage, one without
	_, err := ts.db.Exec(`INSERT INTO auth_tokens (id, token_hash, name, created_at, created_by) VALUES
		('token-with-usage', 'hash1', 'With Usage', '2024-01-01T00:00:00Z', 'test'),
		('token-no-usage', 'hash2', 'No Usage', '2024-01-01T00:00:00Z', 'test')`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ts.db.Exec(`INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens) VALUES
		('token-with-usage', '2024-01-01T10:00', 5, 1000, 500, 1500)`)
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

	// Only the token with usage should be returned
	if len(resp.Tokens) != 1 {
		t.Errorf("expected 1 token (only with usage), got %d", len(resp.Tokens))
	}

	if resp.Tokens[0].TokenID != "token-with-usage" {
		t.Errorf("expected token_id = 'token-with-usage', got %s", resp.Tokens[0].TokenID)
	}
}

// Test handleUsageSummary_LargeDateRange tests handling of very large date ranges
func TestHandleUsageSummary_LargeDateRange(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Insert test token
	_, err := ts.db.Exec(`INSERT INTO auth_tokens (id, token_hash, name, created_at, created_by) VALUES
		('token1', 'hash1', 'Test Token 1', '2024-01-01T00:00:00Z', 'test')`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert usage data across multiple months
	// Note: Using hour buckets without :mm suffix to ensure proper string comparison
	_, err = ts.db.Exec(`INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens) VALUES
		('token1', '2024-01-01T10:00', 100, 20000, 10000, 30000),
		('token1', '2024-06-15T10:00', 200, 40000, 20000, 60000),
		('token1', '2024-12-01T23', 50, 10000, 5000, 15000)`)
	if err != nil {
		t.Fatal(err)
	}

	// Query ~11 months (within 1 year limit)
	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage/summary?from=2024-01-01T00&to=2024-12-01T23", nil)
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

	if resp.GrandTotal.TotalRequests != 350 {
		t.Errorf("expected grand total requests = 350, got %d", resp.GrandTotal.TotalRequests)
	}

	if resp.GrandTotal.TotalTokens != 105000 {
		t.Errorf("expected grand total tokens = 105000, got %d", resp.GrandTotal.TotalTokens)
	}
}

// Test handleUsageSummary_PeakHourCalculation tests peak hour calculation across multiple hours
func TestHandleUsageSummary_PeakHourCalculation(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Insert test token
	_, err := ts.db.Exec(`INSERT INTO auth_tokens (id, token_hash, name, created_at, created_by) VALUES
		('token1', 'hash1', 'Test Token 1', '2024-01-01T00:00:00Z', 'test')`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert usage with clear peak hour (10:00 with 50 requests)
	_, err = ts.db.Exec(`INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens) VALUES
		('token1', '2024-01-01T08:00', 10, 2000, 1000, 3000),
		('token1', '2024-01-01T09:00', 20, 4000, 2000, 6000),
		('token1', '2024-01-01T10:00', 50, 10000, 5000, 15000),
		('token1', '2024-01-01T11:00', 30, 6000, 3000, 9000)`)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage/summary?from=2024-01-01T00&to=2024-01-01T23", nil)
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

	if resp.GrandTotal.PeakHour != "2024-01-01T10:00" {
		t.Errorf("expected peak hour = '2024-01-01T10:00', got %s", resp.GrandTotal.PeakHour)
	}

	if resp.GrandTotal.PeakHourRequests != 50 {
		t.Errorf("expected peak hour requests = 50, got %d", resp.GrandTotal.PeakHourRequests)
	}
}

// Test handleUsageSummary_TokenWithNoUsage tests summary with token that has no usage
func TestHandleUsageSummary_TokenWithNoUsage(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Insert token with no usage
	_, err := ts.db.Exec(`INSERT INTO auth_tokens (id, token_hash, name, created_at, created_by) VALUES
		('unused-token', 'hash1', 'Unused Token', '2024-01-01T00:00:00Z', 'test')`)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage/summary?from=2024-01-01T00&to=2024-01-01T23", nil)
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

	// Token with no usage should not appear in summary
	if len(resp.Tokens) != 0 {
		t.Errorf("expected 0 tokens in summary, got %d", len(resp.Tokens))
	}

	if resp.GrandTotal.TotalRequests != 0 {
		t.Errorf("expected 0 total requests, got %d", resp.GrandTotal.TotalRequests)
	}
}

// Test handleUsage_TokenNameIsEmpty tests handling when token has no name in auth_tokens
func TestHandleUsage_TokenNameIsEmpty(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Insert usage with token_id that doesn't exist in auth_tokens
	_, err := ts.db.Exec(`INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens) VALUES
		('orphan-token', '2024-01-01T10:00', 5, 1000, 500, 1500)`)
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

	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 data row, got %d", len(resp.Data))
	}

	// Token name should be empty string (not NULL or error)
	if resp.Data[0].TokenName != "" {
		t.Errorf("expected empty token name, got %s", resp.Data[0].TokenName)
	}
}

// Test handleUsageSummary_TokenNameIsEmpty tests handling when token has no name
func TestHandleUsageSummary_TokenNameIsEmpty(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Insert usage with token_id that doesn't exist in auth_tokens
	_, err := ts.db.Exec(`INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens) VALUES
		('orphan-token', '2024-01-01T10:00', 5, 1000, 500, 1500)`)
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

	if len(resp.Tokens) != 1 {
		t.Fatalf("expected 1 token in summary, got %d", len(resp.Tokens))
	}

	// Token name should be empty string (not NULL or error)
	if resp.Tokens[0].Name != "" {
		t.Errorf("expected empty token name, got %s", resp.Tokens[0].Name)
	}
}

// Test handleUsageTokens_TokenNameIsEmpty tests handling when token has no name
func TestHandleUsageTokens_TokenNameIsEmpty(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Insert usage with token_id that doesn't exist in auth_tokens
	_, err := ts.db.Exec(`INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens) VALUES
		('orphan-token', '2024-01-01T10:00', 5, 1000, 500, 1500)`)
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

	if len(resp.Tokens) != 1 {
		t.Fatalf("expected 1 token, got %d", len(resp.Tokens))
	}

	// Token name should be empty string (not NULL or error)
	if resp.Tokens[0].Name != "" {
		t.Errorf("expected empty token name, got %s", resp.Tokens[0].Name)
	}
}

// Test handleUsage_ContentType tests that response has correct content type
func TestHandleUsage_ContentType(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage?from=2024-01-01T09&to=2024-01-01T12", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ts.handleUsage(w, req)

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type = 'application/json', got %s", contentType)
	}
}

// Test handleUsageTokens_ContentType tests that response has correct content type
func TestHandleUsageTokens_ContentType(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage/tokens", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ts.handleUsageTokens(w, req)

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type = 'application/json', got %s", contentType)
	}
}

// Test handleUsageSummary_ContentType tests that response has correct content type
func TestHandleUsageSummary_ContentType(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage/summary?from=2024-01-01T09&to=2024-01-01T12", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ts.handleUsageSummary(w, req)

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type = 'application/json', got %s", contentType)
	}
}

// Test handleUsageSummary_AllTokensPeakHour tests peak hour calculation when filtering all tokens
func TestHandleUsageSummary_AllTokensPeakHour(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Insert multiple tokens with peak hours at different times
	_, err := ts.db.Exec(`INSERT INTO auth_tokens (id, token_hash, name, created_at, created_by) VALUES
		('token1', 'hash1', 'Token 1', '2024-01-01T00:00:00Z', 'test'),
		('token2', 'hash2', 'Token 2', '2024-01-01T00:00:00Z', 'test')`)
	if err != nil {
		t.Fatal(err)
	}

	// token1 peaks at 10:00 with 30 requests
	// token2 peaks at 11:00 with 20 requests
	// Combined peak should be 10:00 with 30 requests
	_, err = ts.db.Exec(`INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens) VALUES
		('token1', '2024-01-01T09:00', 10, 2000, 1000, 3000),
		('token1', '2024-01-01T10:00', 30, 6000, 3000, 9000),
		('token1', '2024-01-01T11:00', 15, 3000, 1500, 4500),
		('token2', '2024-01-01T09:00', 5, 1000, 500, 1500),
		('token2', '2024-01-01T10:00', 10, 2000, 1000, 3000),
		('token2', '2024-01-01T11:00', 20, 4000, 2000, 6000)`)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage/summary?from=2024-01-01T00&to=2024-01-01T23", nil)
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

	if resp.GrandTotal.PeakHour != "2024-01-01T10:00" {
		t.Errorf("expected peak hour = '2024-01-01T10:00', got %s", resp.GrandTotal.PeakHour)
	}

	if resp.GrandTotal.PeakHourRequests != 40 {
		// 30 from token1 + 10 from token2
		t.Errorf("expected peak hour requests = 40, got %d", resp.GrandTotal.PeakHourRequests)
	}
}

// Test handleUsage_ResponseFields tests that all expected response fields are present
func TestHandleUsage_ResponseFields(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	req := httptest.NewRequest(http.MethodGet, "/fe/api/usage?from=2024-01-01T09&to=2024-01-01T12&token_id=token1&view=daily", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ts.handleUsage(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Verify all expected fields are present
	expectedFields := []string{"token_id", "from", "to", "view", "data", "totals"}
	for _, field := range expectedFields {
		if _, ok := resp[field]; !ok {
			t.Errorf("expected field %q in response", field)
		}
	}

	// Verify totals fields
	totals, ok := resp["totals"].(map[string]interface{})
	if !ok {
		t.Fatal("totals should be an object")
	}
	totalsFields := []string{"request_count", "prompt_tokens", "completion_tokens", "total_tokens"}
	for _, field := range totalsFields {
		if _, ok := totals[field]; !ok {
			t.Errorf("expected totals field %q in response", field)
		}
	}
}

// Test handleUsageSummary_ResponseFields tests that all expected response fields are present
func TestHandleUsageSummary_ResponseFields(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Insert test token
	_, err := ts.db.Exec(`INSERT INTO auth_tokens (id, token_hash, name, created_at, created_by) VALUES
		('token1', 'hash1', 'Test Token 1', '2024-01-01T00:00:00Z', 'test')`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ts.db.Exec(`INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens) VALUES
		('token1', '2024-01-01T10:00', 5, 1000, 500, 1500)`)
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

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Verify top-level fields
	expectedFields := []string{"from", "to", "tokens", "grand_total"}
	for _, field := range expectedFields {
		if _, ok := resp[field]; !ok {
			t.Errorf("expected field %q in response", field)
		}
	}

	// Verify grand_total fields
	grandTotal, ok := resp["grand_total"].(map[string]interface{})
	if !ok {
		t.Fatal("grand_total should be an object")
	}
	grandTotalFields := []string{"total_requests", "total_prompt_tokens", "total_completion_tokens", "total_tokens", "peak_hour", "peak_hour_requests"}
	for _, field := range grandTotalFields {
		if _, ok := grandTotal[field]; !ok {
			t.Errorf("expected grand_total field %q in response", field)
		}
	}
}

// Test handleUsageTokens_ResponseFields tests that all expected response fields are present
func TestHandleUsageTokens_ResponseFields(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Insert test token
	_, err := ts.db.Exec(`INSERT INTO auth_tokens (id, token_hash, name, created_at, created_by) VALUES
		('token1', 'hash1', 'Test Token 1', '2024-01-01T00:00:00Z', 'test')`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ts.db.Exec(`INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens) VALUES
		('token1', '2024-01-01T10:00', 5, 1000, 500, 1500)`)
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

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Verify tokens field exists
	if _, ok := resp["tokens"]; !ok {
		t.Error("expected 'tokens' field in response")
	}

	// Verify token summary fields
	tokens, ok := resp["tokens"].([]interface{})
	if !ok {
		t.Fatal("tokens should be an array")
	}
	if len(tokens) > 0 {
		token, ok := tokens[0].(map[string]interface{})
		if !ok {
			t.Fatal("token should be an object")
		}
		tokenFields := []string{"token_id", "name", "total_requests", "total_tokens"}
		for _, field := range tokenFields {
			if _, ok := token[field]; !ok {
				t.Errorf("expected token field %q in response", field)
			}
		}
	}
}
