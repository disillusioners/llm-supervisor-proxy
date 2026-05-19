package mcp

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	_ "modernc.org/sqlite"
)

// setupAuthTest creates an in-memory SQLite database with the auth_tokens table
// and a TokenStore with a test token
func setupAuthTest(t *testing.T) (*auth.TokenStore, string) {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Create the auth_tokens table with all required columns
	// Schema based on migrations 004, 020, 022, 023
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
	);
	`
	_, err = db.Exec(createTable)
	if err != nil {
		t.Fatalf("failed to create auth_tokens table: %v", err)
	}

	// Create token store
	store := auth.NewTokenStore(db, database.SQLite)

	// Create a test token
	plaintext, _, err := store.CreateToken(context.Background(), "test-token", nil, "test", false, "", nil)
	if err != nil {
		t.Fatalf("failed to create test token: %v", err)
	}

	return store, plaintext
}

func TestProxyAuthMiddleware(t *testing.T) {
	t.Parallel()

	tokenStore, validToken := setupAuthTest(t)
	middleware := proxyAuthMiddleware(tokenStore)

	// Next handler that returns 200 OK
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name           string
		authHeader     string
		wantStatus     int
		wantBodyContains string
	}{
		{
			name:       "Valid token",
			authHeader: "Bearer " + validToken,
			wantStatus: http.StatusOK,
		},
		{
			name:           "Invalid token",
			authHeader:     "Bearer invalid-token",
			wantStatus:     http.StatusUnauthorized,
			wantBodyContains: "invalid or expired token",
		},
		{
			name:           "Missing header",
			authHeader:     "",
			wantStatus:     http.StatusUnauthorized,
			wantBodyContains: "missing authorization header",
		},
		{
			name:           "Malformed header - Basic auth",
			authHeader:     "Basic abc123",
			wantStatus:     http.StatusUnauthorized,
			wantBodyContains: "invalid authorization header format",
		},
		{
			name:           "Empty bearer token",
			authHeader:     "Bearer ",
			wantStatus:     http.StatusUnauthorized,
			wantBodyContains: "missing bearer token",
		},
		{
			name:           "No scheme - just a string",
			authHeader:     "just-a-string",
			wantStatus:     http.StatusUnauthorized,
			wantBodyContains: "invalid authorization header format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			rec := httptest.NewRecorder()
			handler := middleware(nextHandler)
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("got status %d, want %d", rec.Code, tt.wantStatus)
			}

			if tt.wantBodyContains != "" {
				body := rec.Body.String()
				if !containsIgnoreCase(body, tt.wantBodyContains) {
					t.Errorf("body %q does not contain %q", body, tt.wantBodyContains)
				}
			}
		})
	}
}

// containsIgnoreCase checks if s contains substr, case-insensitive
func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
