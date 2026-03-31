package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
)

// ─────────────────────────────────────────────────────────────────────────────
// Mock TokenStore for testing
// ─────────────────────────────────────────────────────────────────────────────

// mockTokenStore is a mock implementation of auth.TokenStoreInterface for testing
type mockTokenStore struct {
	tokens map[string]*auth.AuthToken // map of plaintext token to AuthToken
}

func newMockTokenStore() *mockTokenStore {
	return &mockTokenStore{
		tokens: make(map[string]*auth.AuthToken),
	}
}

func (m *mockTokenStore) addToken(plaintext string, token *auth.AuthToken) {
	m.tokens[plaintext] = token
}

func (m *mockTokenStore) ValidateToken(ctx context.Context, plaintext string) (*auth.AuthToken, error) {
	// Check if token exists
	token, ok := m.tokens[plaintext]
	if !ok {
		return nil, auth.ErrTokenNotFound
	}

	// Check expiration
	if token.ExpiresAt != nil && time.Now().After(*token.ExpiresAt) {
		return nil, auth.ErrTokenExpired
	}

	return token, nil
}

func (m *mockTokenStore) CreateToken(ctx context.Context, name string, expiresAt *time.Time, createdBy string) (string, *auth.AuthToken, error) {
	// Not used in authenticate tests
	panic("not implemented")
}

func (m *mockTokenStore) DeleteToken(ctx context.Context, id string) error {
	// Not used in authenticate tests
	panic("not implemented")
}

func (m *mockTokenStore) ListTokens(ctx context.Context) ([]auth.AuthToken, error) {
	// Not used in authenticate tests
	panic("not implemented")
}

// ─────────────────────────────────────────────────────────────────────────────
// Test cases
// ─────────────────────────────────────────────────────────────────────────────

func TestAuthenticate(t *testing.T) {
	// Create a valid test token
	validToken := &auth.AuthToken{
		ID:        "test-token-id",
		Name:      "Test Token",
		TokenHash: auth.HashToken("sk-testvalidtoken123456789012345678901234567890123456"),
		CreatedBy: "test",
	}
	// Note: plaintext format must match what ValidateToken expects
	validPlaintext := "sk-testvalidtoken123456789012345678901234567890123456"

	// Create expired token
	expiredTime := time.Now().Add(-1 * time.Hour)
	expiredToken := &auth.AuthToken{
		ID:        "expired-token-id",
		Name:      "Expired Token",
		TokenHash: auth.HashToken("sk-expiredtoken1234567890123456789012345678901234567"),
		ExpiresAt: &expiredTime,
		CreatedBy: "test",
	}
	expiredPlaintext := "sk-expiredtoken1234567890123456789012345678901234567"

	// Create future-expired token (valid)
	futureTime := time.Now().Add(1 * time.Hour)
	validFutureToken := &auth.AuthToken{
		ID:        "future-token-id",
		Name:      "Valid Future Token",
		TokenHash: auth.HashToken("sk-validfuturetoken12345678901234567890123456789012345"),
		ExpiresAt: &futureTime,
		CreatedBy: "test",
	}
	validFuturePlaintext := "sk-validfuturetoken12345678901234567890123456789012345"

	tests := []struct {
		name       string
		tokenStore auth.TokenStoreInterface // nil means auth disabled
		apiKey     string                   // empty means no Authorization header
		wantToken  *auth.AuthToken
		wantOK     bool
	}{
		{
			name:       "auth disabled (nil tokenStore) - backward compatibility",
			tokenStore: nil,
			apiKey:     "",
			wantToken:  nil,
			wantOK:     true, // BACKWARD COMPATIBILITY: returns true when auth disabled
		},
		{
			name:       "auth disabled with api key present - still returns true",
			tokenStore: nil,
			apiKey:     "sk-sometoken",
			wantToken:  nil,
			wantOK:     true, // BACKWARD COMPATIBILITY: returns true when auth disabled
		},
		{
			name:       "empty api key - no Authorization header",
			tokenStore: newMockTokenStore(),
			apiKey:     "",
			wantToken:  nil,
			wantOK:     false,
		},
		{
			name:       "empty api key - empty Bearer token",
			tokenStore: newMockTokenStore(),
			apiKey:     "Bearer",
			wantToken:  nil,
			wantOK:     false,
		},
		{
			name:       "empty api key - whitespace only",
			tokenStore: newMockTokenStore(),
			apiKey:     "Bearer ",
			wantToken:  nil,
			wantOK:     false,
		},
		{
			name: "invalid token format - no sk- prefix",
			tokenStore: func() auth.TokenStoreInterface {
				m := newMockTokenStore()
				m.addToken("not-a-valid-token-format", validToken)
				return m
			}(),
			apiKey:    "not-a-valid-token-format",
			wantToken: nil,
			wantOK:    false,
		},
		{
			name:       "invalid token format - too short",
			tokenStore: newMockTokenStore(),
			apiKey:     "sk-short",
			wantToken:  nil,
			wantOK:     false,
		},
		{
			name:       "token not found in store",
			tokenStore: newMockTokenStore(), // Empty store
			apiKey:     "Bearer " + validPlaintext,
			wantToken:  nil,
			wantOK:     false,
		},
		{
			name: "valid token - returns token and true",
			tokenStore: func() auth.TokenStoreInterface {
				m := newMockTokenStore()
				m.addToken(validPlaintext, validToken)
				return m
			}(),
			apiKey:    "Bearer " + validPlaintext,
			wantToken: validToken,
			wantOK:    true,
		},
		{
			name: "valid token with future expiry - returns token and true",
			tokenStore: func() auth.TokenStoreInterface {
				m := newMockTokenStore()
				m.addToken(validFuturePlaintext, validFutureToken)
				return m
			}(),
			apiKey:    "Bearer " + validFuturePlaintext,
			wantToken: validFutureToken,
			wantOK:    true,
		},
		{
			name: "expired token - returns nil and false",
			tokenStore: func() auth.TokenStoreInterface {
				m := newMockTokenStore()
				m.addToken(expiredPlaintext, expiredToken)
				return m
			}(),
			apiKey:    "Bearer " + expiredPlaintext,
			wantToken: nil,
			wantOK:    false,
		},
		{
			name:       "Bearer prefix without token",
			tokenStore: newMockTokenStore(),
			apiKey:     "Bearer sk-", // Bearer with empty sk- prefix
			wantToken:  nil,
			wantOK:     false,
		},
		{
			name:       "malformed bearer - lowercase",
			tokenStore: newMockTokenStore(),
			apiKey:     "bearer " + validPlaintext,
			wantToken:  nil,
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create handler with the test tokenStore
			handler := &Handler{
				tokenStore: tt.tokenStore,
			}

			// Create test request
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.apiKey != "" {
				req.Header.Set("Authorization", tt.apiKey)
			}

			// Call authenticate
			token, ok := handler.authenticate(req)

			// Check ok result
			if ok != tt.wantOK {
				t.Errorf("authenticate() ok = %v, want %v", ok, tt.wantOK)
			}

			// Check token result
			if tt.wantOK {
				// When ok is true, token should match
				if token == nil && tt.wantToken != nil {
					t.Errorf("authenticate() token = nil, want %+v", tt.wantToken)
				}
				if token != nil && tt.wantToken != nil {
					if token.ID != tt.wantToken.ID {
						t.Errorf("authenticate() token.ID = %v, want %v", token.ID, tt.wantToken.ID)
					}
					if token.Name != tt.wantToken.Name {
						t.Errorf("authenticate() token.Name = %v, want %v", token.Name, tt.wantToken.Name)
					}
				}
			} else {
				// When ok is false, token should be nil
				if token != nil {
					t.Errorf("authenticate() token = %+v, want nil when ok=false", token)
				}
			}
		})
	}
}

// TestAuthenticate_ExtractAPIKey tests the extractAPIKey helper via authenticate
func TestAuthenticate_ExtractAPIKey(t *testing.T) {
	store := newMockTokenStore()
	handler := &Handler{tokenStore: store}

	tests := []struct {
		name        string
		authHeader  string
		expectEmpty bool
	}{
		{"no header", "", true},
		{"empty header", "", true},
		{"bearer only", "Bearer", true},
		{"bearer with whitespace", "Bearer ", true},
		{"bearer with valid token", "Bearer sk-abc1234567890abcdefghijklmnopqrstuvwxyz1234567890abcd", false},
		{"token without bearer prefix", "", true}, // extractAPIKey only extracts from Authorization header
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			// The extractAPIKey function is tested indirectly through authenticate
			// For empty results, authenticate should return (nil, false)
			token, ok := handler.authenticate(req)

			if ok {
				t.Error("expected ok=false for extractAPIKey tests, got ok=true")
			}
			if token != nil {
				t.Errorf("expected token=nil, got %+v", token)
			}
		})
	}
}

// TestAuthenticate_WithContextCancellation tests that authenticate handles context cancellation
func TestAuthenticate_WithContextCancellation(t *testing.T) {
	// Create a slow mock that blocks on ValidateToken
	slowStore := &slowMockTokenStore{
		delay: 100 * time.Millisecond,
	}
	handler := &Handler{tokenStore: slowStore}

	// Create request with already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	req := httptest.NewRequest(http.MethodGet, "/test", nil).WithContext(ctx)

	token, ok := handler.authenticate(req)

	// Should return false because context was cancelled
	if ok {
		t.Error("expected ok=false for cancelled context, got ok=true")
	}
	if token != nil {
		t.Errorf("expected token=nil for cancelled context, got %+v", token)
	}
}

// slowMockTokenStore simulates a slow database call
type slowMockTokenStore struct {
	delay time.Duration
}

func (s *slowMockTokenStore) ValidateToken(ctx context.Context, plaintext string) (*auth.AuthToken, error) {
	select {
	case <-time.After(s.delay):
		return &auth.AuthToken{ID: "test"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *slowMockTokenStore) CreateToken(ctx context.Context, name string, expiresAt *time.Time, createdBy string) (string, *auth.AuthToken, error) {
	panic("not implemented")
}

func (s *slowMockTokenStore) DeleteToken(ctx context.Context, id string) error {
	panic("not implemented")
}

func (s *slowMockTokenStore) ListTokens(ctx context.Context) ([]auth.AuthToken, error) {
	panic("not implemented")
}
