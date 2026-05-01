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

// ─────────────────────────────────────────────────────────────────────────────
// ScanAllowedModels Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestScanAllowedModels(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected []string
	}{
		{
			name:     "nil input",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "non-string type",
			input:    123,
			expected: []string{},
		},
		{
			name:     "valid JSON array multiple elements",
			input:    `["gpt-4","claude-3"]`,
			expected: []string{"gpt-4", "claude-3"},
		},
		{
			name:     "valid JSON empty array",
			input:    `[]`,
			expected: []string{},
		},
		{
			name:     "valid JSON single element",
			input:    `["gpt-4"]`,
			expected: []string{"gpt-4"},
		},
		{
			name:     "malformed JSON string",
			input:    `"not-json"`,
			expected: []string{},
		},
		{
			name:     "malformed JSON random",
			input:    `{invalid}`,
			expected: []string{},
		},
		{
			name:     "valid JSON with extra whitespace",
			input:    `  ["model-1", "model-2"]  `,
			expected: []string{"model-1", "model-2"},
		},
		{
			name:     "byte slice input (postgres driver)",
			input:    []byte(`["byte-model"]`),
			expected: []string{"byte-model"},
		},
		{
			name:     "byte slice empty array",
			input:    []byte(`[]`),
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ScanAllowedModels(tt.input)

			// Check length first
			if len(result) != len(tt.expected) {
				t.Errorf("ScanAllowedModels(%v) length = %d, want %d", tt.input, len(result), len(tt.expected))
				return
			}

			// Check contents
			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("ScanAllowedModels(%v)[%d] = %q, want %q", tt.input, i, v, tt.expected[i])
				}
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// IsModelAllowed Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestAuthToken_IsModelAllowed(t *testing.T) {
	tests := []struct {
		name         string
		allowedModels []string
		modelName    string
		expected     bool
	}{
		{
			name:         "nil allowed models allows any model",
			allowedModels: nil,
			modelName:    "gpt-4",
			expected:     true,
		},
		{
			name:         "nil allowed models allows any model 2",
			allowedModels: nil,
			modelName:    "claude-3",
			expected:     true,
		},
		{
			name:         "empty allowed models allows any model",
			allowedModels: []string{},
			modelName:    "gpt-4",
			expected:     true,
		},
		{
			name:         "empty allowed models allows any model 2",
			allowedModels: []string{},
			modelName:    "claude-3",
			expected:     true,
		},
		{
			name:         "model in list returns true",
			allowedModels: []string{"gpt-4", "claude-3", "gemini-pro"},
			modelName:    "claude-3",
			expected:     true,
		},
		{
			name:         "model not in list returns false",
			allowedModels: []string{"gpt-4", "claude-3"},
			modelName:    "gemini-pro",
			expected:     false,
		},
		{
			name:         "exact match required case-sensitive - lowercase",
			allowedModels: []string{"gpt-4"},
			modelName:    "gpt-4",
			expected:     true,
		},
		{
			name:         "exact match required case-sensitive - uppercase mismatch",
			allowedModels: []string{"gpt-4"},
			modelName:    "GPT-4",
			expected:     false,
		},
		{
			name:         "exact match required case-sensitive - mixed case mismatch",
			allowedModels: []string{"Claude-3"},
			modelName:    "claude-3",
			expected:     false,
		},
		{
			name:         "single model in list exact match",
			allowedModels: []string{"o3-mini"},
			modelName:    "o3-mini",
			expected:     true,
		},
		{
			name:         "single model in list mismatch",
			allowedModels: []string{"o3-mini"},
			modelName:    "o3",
			expected:     false,
		},
		{
			name:         "first model in list",
			allowedModels: []string{"gpt-4", "claude-3"},
			modelName:    "gpt-4",
			expected:     true,
		},
		{
			name:         "last model in list",
			allowedModels: []string{"gpt-4", "claude-3"},
			modelName:    "claude-3",
			expected:     true,
		},
		{
			name:         "empty model name when models allowed",
			allowedModels: []string{"gpt-4"},
			modelName:    "",
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := &AuthToken{AllowedModels: tt.allowedModels}
			result := token.IsModelAllowed(tt.modelName)

			if result != tt.expected {
				t.Errorf("IsModelAllowed(%q) with AllowedModels=%v = %v, want %v",
					tt.modelName, tt.allowedModels, result, tt.expected)
			}
		})
	}
}
