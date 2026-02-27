package auth

import (
	"testing"
	"time"
)

func TestGenerateToken(t *testing.T) {
	plaintext, hash, err := GenerateToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check prefix
	if len(plaintext) < 3 || plaintext[:3] != TokenPrefix {
		t.Errorf("token should have %s prefix, got: %s", TokenPrefix, plaintext[:3])
	}

	// Check length (sk- + 64 hex chars)
	if len(plaintext) != len(TokenPrefix)+64 {
		t.Errorf("token should have length %d, got %d", len(TokenPrefix)+64, len(plaintext))
	}

	// Check hash is not empty
	if hash == "" {
		t.Error("hash should not be empty")
	}

	// Hash should be different from plaintext
	if hash == plaintext {
		t.Error("hash should not equal plaintext")
	}
}

func TestHashToken(t *testing.T) {
	token := "sk-test123456789"
	hash1 := HashToken(token)
	hash2 := HashToken(token)

	// Same input should produce same hash
	if hash1 != hash2 {
		t.Error("same token should produce same hash")
	}

	// Hash should be 64 chars (SHA-256 in hex)
	if len(hash1) != 64 {
		t.Errorf("hash should be 64 chars, got %d", len(hash1))
	}
}

func TestValidateTokenFormat(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		expected bool
	}{
		{"valid token", "sk-" + "0123456789abcdef" + "0123456789abcdef" + "0123456789abcdef" + "0123456789abcdef", true},
		{"no prefix", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", false},
		{"wrong prefix", "pk-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", false},
		{"too short", "sk-short", false},
		{"empty", "", false},
		{"non-hex chars", "sk-0123456789abcdef0123456789abcdef0123456789abcdef0123456789gxyz", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateTokenFormat(tt.token)
			if result != tt.expected {
				t.Errorf("ValidateTokenFormat(%q) = %v, want %v", tt.token, result, tt.expected)
			}
		})
	}
}

func TestAuthToken_IsExpired(t *testing.T) {
	t.Run("no expiry", func(t *testing.T) {
		token := &AuthToken{ExpiresAt: nil}
		if token.IsExpired() {
			t.Error("token with no expiry should not be expired")
		}
	})

	t.Run("past expiry", func(t *testing.T) {
		past := time.Now().Add(-1 * time.Hour)
		token := &AuthToken{ExpiresAt: &past}
		if !token.IsExpired() {
			t.Error("token with past expiry should be expired")
		}
	})

	t.Run("future expiry", func(t *testing.T) {
		future := time.Now().Add(1 * time.Hour)
		token := &AuthToken{ExpiresAt: &future}
		if token.IsExpired() {
			t.Error("token with future expiry should not be expired")
		}
	})
}
