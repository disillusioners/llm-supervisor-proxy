package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
)

// mockAuthTokenStore implements auth.TokenStoreInterface for testing
type mockAuthTokenStore struct {
	validPlaintext string
	token          *auth.AuthToken
}

func (m *mockAuthTokenStore) ValidateToken(ctx context.Context, plaintext string) (*auth.AuthToken, error) {
	if plaintext == m.validPlaintext {
		return m.token, nil
	}
	return nil, auth.ErrTokenNotFound
}

func (m *mockAuthTokenStore) CreateToken(ctx context.Context, name string, expiresAt *time.Time, createdBy string, ultimateModelEnabled bool, ultimateModelID string, allowedModels []string) (string, *auth.AuthToken, error) {
	return "", nil, nil
}

func (m *mockAuthTokenStore) DeleteToken(ctx context.Context, id string) error {
	return nil
}

func (m *mockAuthTokenStore) ListTokens(ctx context.Context) ([]auth.AuthToken, error) {
	return nil, nil
}

func (m *mockAuthTokenStore) GetTokenByID(ctx context.Context, id string) (*auth.AuthToken, error) {
	return nil, nil
}

func (m *mockAuthTokenStore) UpdateTokenPermission(ctx context.Context, id string, ultimateModelEnabled bool, ultimateModelID string, allowedModels []string) error {
	return nil
}

// setupAuthTest creates a test Server with mock token store
func setupAuthTest(t *testing.T) (*Server, string) {
	t.Helper()

	// Generate a valid test token
	validToken := "sk-" + strings.Repeat("a", 64)
	tokenHash := auth.HashToken(validToken)

	mockStore := &mockAuthTokenStore{
		validPlaintext: validToken,
		token: &auth.AuthToken{
			ID:        "test-token-id",
			Name:      "test-token",
			TokenHash: tokenHash,
			CreatedBy: "test",
		},
	}

	bus := events.NewBus()
	server := NewServer(0, nil, bus, mockStore)

	return server, validToken
}

// setupAuthTestWithSQLite creates a test Server with SQLite-backed token store
func setupAuthTestWithSQLite(t *testing.T) (*Server, string) {
	t.Helper()

	server, _, plaintext, cleanup := setupTestEnv(t)
	t.Cleanup(cleanup)

	return server, plaintext
}

func TestProxyAuthMiddleware(t *testing.T) {
	t.Parallel()

	server, validToken := setupAuthTest(t)

	// Next handler that returns 200 OK
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name               string
		authHeader         string
		wantStatus         int
		wantBodyContains   string
	}{
		{
			name:       "Valid token",
			authHeader: "Bearer " + validToken,
			wantStatus: http.StatusOK,
		},
		{
			name:             "Invalid token",
			authHeader:       "Bearer invalid-token",
			wantStatus:       http.StatusUnauthorized,
			wantBodyContains: "invalid or expired token",
		},
		{
			name:             "Missing header",
			authHeader:       "",
			wantStatus:       http.StatusUnauthorized,
			wantBodyContains: "missing authorization header",
		},
		{
			name:             "Malformed header - Basic auth",
			authHeader:       "Basic abc123",
			wantStatus:       http.StatusUnauthorized,
			wantBodyContains: "invalid authorization header format",
		},
		{
			name:             "Empty bearer token",
			authHeader:       "Bearer ",
			wantStatus:       http.StatusUnauthorized,
			wantBodyContains: "missing bearer token",
		},
		{
			name:             "No scheme - just a string",
			authHeader:       "just-a-string",
			wantStatus:       http.StatusUnauthorized,
			wantBodyContains: "invalid authorization header format",
		},
		{
			name:             "Wrong auth type - Digest",
			authHeader:       "Digest username",
			wantStatus:       http.StatusUnauthorized,
			wantBodyContains: "invalid authorization header format",
		},
		{
			name:       "Case insensitive bearer",
			authHeader: "bearer " + validToken,
			wantStatus: http.StatusOK,
		},
		{
			name:       "BEARER uppercase",
			authHeader: "BEARER " + validToken,
			wantStatus: http.StatusOK,
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
			handler := server.proxyAuthMiddleware(nextHandler)
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

func TestProxyAuthMiddleware_TokenInContext(t *testing.T) {
	t.Parallel()

	server, validToken := setupAuthTest(t)

	// Next handler that checks context for token
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify token info is in context
		tokenInfo, ok := r.Context().Value(tokenContextKey).(*auth.AuthToken)
		if !ok {
			t.Error("token info not found in context")
			return
		}
		if tokenInfo.ID != "test-token-id" {
			t.Errorf("token ID = %q, want %q", tokenInfo.ID, "test-token-id")
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+validToken)

	rec := httptest.NewRecorder()
	handler := server.proxyAuthMiddleware(nextHandler)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestProxyAuthMiddleware_WithSQLiteStore(t *testing.T) {
	t.Parallel()

	server, validToken := setupAuthTestWithSQLite(t)

	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+validToken)

	rec := httptest.NewRecorder()
	handler := server.proxyAuthMiddleware(nextHandler)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d. body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestWriteJSONError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     int
		message    string
		wantStatus int
		wantBody   string
	}{
		{
			name:       "401 Unauthorized",
			status:     http.StatusUnauthorized,
			message:    "test error",
			wantStatus: http.StatusUnauthorized,
			wantBody:   `{"error":"test error"}`,
		},
		{
			name:       "500 Internal Server Error",
			status:     http.StatusInternalServerError,
			message:    "server error",
			wantStatus: http.StatusInternalServerError,
			wantBody:   `{"error":"server error"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()

			writeJSONError(rec, tt.status, tt.message)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			contentType := rec.Header().Get("Content-Type")
			if !strings.Contains(contentType, "application/json") {
				t.Errorf("Content-Type = %q, want to contain 'application/json'", contentType)
			}

			body := strings.TrimSpace(rec.Body.String())
			if body != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
		})
	}
}

func TestProxyAuthMiddleware_ContextPropagation(t *testing.T) {
	t.Parallel()

	server, validToken := setupAuthTest(t)

	// Next handler that modifies context and continues
	var ctxTokenInfo *auth.AuthToken
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxTokenInfo, _ = r.Context().Value(tokenContextKey).(*auth.AuthToken)
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+validToken)

	rec := httptest.NewRecorder()
	handler := server.proxyAuthMiddleware(nextHandler)
	handler.ServeHTTP(rec, req)

	if ctxTokenInfo == nil {
		t.Fatal("token info should be in context")
	}

	// Verify it's the correct token info
	if ctxTokenInfo.Name != "test-token" {
		t.Errorf("token Name = %q, want %q", ctxTokenInfo.Name, "test-token")
	}
}

// containsIgnoreCase checks if s contains substr, case-insensitive
func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// Verify error responses are valid JSON
func TestWriteJSONError_ValidJSON(t *testing.T) {
	t.Parallel()

	tests := []string{"simple", "with spaces", "special chars: !@#$", "unicode: 日本語"}

	for _, msg := range tests {
		t.Run(msg, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()

			writeJSONError(rec, http.StatusUnauthorized, msg)

			var resp map[string]string
			err := json.Unmarshal(rec.Body.Bytes(), &resp)
			if err != nil {
				t.Errorf("response is not valid JSON: %v", err)
			}

			if resp["error"] != msg {
				t.Errorf("error = %q, want %q", resp["error"], msg)
			}
		})
	}
}
