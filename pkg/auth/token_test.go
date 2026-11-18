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
// ScanGrant / ScanAllowedModels Tests
// ─────────────────────────────────────────────────────────────────────────────

// TestScanGrant_TriState pins the five tri-state inputs (plan §2.7 / ADR D10).
//
// It REPLACES and REPURPOSES the former TestScanAllowedModels table: that
// table's "non-string type" row asserted `123 => []string{}` (an empty grant
// list), which is exactly the fail-open conflation this commission removes —
// an unreadable row was indistinguishable from a wildcard grant. The `123` row
// now asserts INVALID.
func TestScanGrant_TriState(t *testing.T) {
	tests := []struct {
		name        string
		input       interface{}
		wantIDs     []string
		wantPresent bool
		wantInvalid bool
	}{
		{name: "nil is ABSENT", input: nil, wantIDs: nil, wantPresent: false, wantInvalid: false},
		{name: "empty string is INVALID", input: "", wantIDs: nil, wantPresent: false, wantInvalid: true},
		{name: "whitespace string is INVALID", input: "   ", wantIDs: nil, wantPresent: false, wantInvalid: true},
		{name: "tab/newline string is INVALID", input: "\t\n", wantIDs: nil, wantPresent: false, wantInvalid: true},
		{name: "empty array is PRESENT-empty", input: "[]", wantIDs: []string{}, wantPresent: true, wantInvalid: false},
		{name: "array with ids is PRESENT", input: `["a"]`, wantIDs: []string{"a"}, wantPresent: true, wantInvalid: false},
		{name: "byte slice with ids is PRESENT", input: []byte(`["byte-model"]`), wantIDs: []string{"byte-model"}, wantPresent: true, wantInvalid: false},
		{name: "byte slice empty array is PRESENT-empty", input: []byte(`[]`), wantIDs: []string{}, wantPresent: true, wantInvalid: false},
		{name: "multi-element array", input: `["gpt-4","claude-3"]`, wantIDs: []string{"gpt-4", "claude-3"}, wantPresent: true, wantInvalid: false},
		{name: "leading whitespace array", input: `  ["model-1", "model-2"]  `, wantIDs: []string{"model-1", "model-2"}, wantPresent: true, wantInvalid: false},
		// Both fail-open branches land in INVALID.
		{name: "unknown raw type 123 is INVALID", input: 123, wantIDs: nil, wantPresent: false, wantInvalid: true},
		{name: "unknown raw type float is INVALID", input: 1.5, wantIDs: nil, wantPresent: false, wantInvalid: true},
		{name: "malformed JSON string is INVALID", input: `"not-json"`, wantIDs: nil, wantPresent: false, wantInvalid: true},
		{name: "malformed JSON braces is INVALID", input: `{invalid}`, wantIDs: nil, wantPresent: false, wantInvalid: true},
		{name: "non-array JSON null is INVALID", input: `null`, wantIDs: nil, wantPresent: false, wantInvalid: true},
		{name: "non-array JSON number is INVALID", input: `123`, wantIDs: nil, wantPresent: false, wantInvalid: true},
		{name: "non-array JSON object is INVALID", input: `{"a":"b"}`, wantIDs: nil, wantPresent: false, wantInvalid: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ids, present, invalid := ScanGrant(tt.input)
			if present != tt.wantPresent {
				t.Errorf("ScanGrant(%v) present = %v, want %v", tt.input, present, tt.wantPresent)
			}
			if invalid != tt.wantInvalid {
				t.Errorf("ScanGrant(%v) invalid = %v, want %v", tt.input, invalid, tt.wantInvalid)
			}
			if len(ids) != len(tt.wantIDs) {
				t.Fatalf("ScanGrant(%v) ids = %v, want %v", tt.input, ids, tt.wantIDs)
			}
			for i, v := range ids {
				if v != tt.wantIDs[i] {
					t.Errorf("ScanGrant(%v)[%d] = %q, want %q", tt.input, i, v, tt.wantIDs[i])
				}
			}
			// An INVALID parse must never hand back ids.
			if invalid && len(ids) != 0 {
				t.Errorf("ScanGrant(%v) returned %v ids alongside invalid=true", tt.input, ids)
			}
		})
	}
}

// TestScanGrant_OnlySQLNullIsAbsent pins the Phase-1.1 item A rule at the
// exact boundary: ONLY a SQL NULL (raw == nil) means ABSENT. An empty or
// whitespace-only TEXT value is UNREADABLE (INVALID ⇒ DENY-ALL), because
// reading it as ABSENT made a legacy `""` column indistinguishable from a
// NULL column — and NULL on BOTH axes is the WILDCARD state (ADR D3/D11).
//
// The paired resolver-side proof lives in pkg/authz
// (TestAuthzResolve_EmptyTextGrantIsDenyAll_NotWildcard): this test pins
// the parser, that one pins the consequence.
func TestScanGrant_OnlySQLNullIsAbsent(t *testing.T) {
	// The ABSENT boundary: SQL NULL.
	ids, present, invalid := ScanGrant(nil)
	if ids != nil || present || invalid {
		t.Errorf("ScanGrant(nil) = (%v, %v, %v), want (nil, false, false) — SQL NULL is ABSENT", ids, present, invalid)
	}

	// Everything else that is "empty" is UNREADABLE.
	for _, raw := range []interface{}{"", " ", "   ", "\t", "\n", "\r\n\t ", []byte(""), []byte("  ")} {
		ids, present, invalid := ScanGrant(raw)
		if present {
			t.Errorf("ScanGrant(%q) present = true, want false", raw)
		}
		if !invalid {
			t.Errorf("ScanGrant(%q) invalid = false, want true (empty/whitespace TEXT is UNREADABLE, never ABSENT)", raw)
		}
		if len(ids) != 0 {
			t.Errorf("ScanGrant(%q) = %v, want no ids alongside invalid=true", raw, ids)
		}
	}
}

// TestScanAllowedModels_ShimPreservesIDs pins the deprecated []string shim:
// it must keep returning ids for readable rows, an empty slice for unreadable
// ones (its legacy contract), and nil for ABSENT — while remaining explicitly
// NOT the authorization decision.
func TestScanAllowedModels_ShimPreservesIDs(t *testing.T) {
	if got := ScanAllowedModels(nil); got != nil {
		t.Errorf("ScanAllowedModels(nil) = %v, want nil (ABSENT)", got)
	}
	// Phase-1.1 item A: an empty TEXT column is UNREADABLE (INVALID), not
	// ABSENT — the shim therefore returns its legacy empty slice for it
	// (nil is reserved for a real ABSENT/NULL column).
	if got := ScanAllowedModels(""); got == nil || len(got) != 0 {
		t.Errorf(`ScanAllowedModels("") = %v, want non-nil empty (INVALID, not ABSENT)`, got)
	}
	if got := ScanAllowedModels(`["a","b"]`); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf(`ScanAllowedModels(["a","b"]) = %v, want [a b]`, got)
	}
	if got := ScanAllowedModels(`[]`); got == nil || len(got) != 0 {
		t.Errorf(`ScanAllowedModels("[]") = %v, want non-nil empty`, got)
	}
	// Legacy []string contract for unreadable input (the shim collapses
	// INVALID to empty; callers that must distinguish use ScanGrant).
	if got := ScanAllowedModels(`not-json`); got == nil || len(got) != 0 {
		t.Errorf(`ScanAllowedModels("not-json") = %v, want empty slice`, got)
	}
	if got := ScanAllowedModels(123); got == nil || len(got) != 0 {
		t.Errorf("ScanAllowedModels(123) = %v, want empty slice", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// IsModelAllowed Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestAuthToken_IsModelAllowed(t *testing.T) {
	tests := []struct {
		name          string
		allowedModels []string
		modelName     string
		expected      bool
	}{
		{
			name:          "nil allowed models allows any model",
			allowedModels: nil,
			modelName:     "gpt-4",
			expected:      true,
		},
		{
			name:          "nil allowed models allows any model 2",
			allowedModels: nil,
			modelName:     "claude-3",
			expected:      true,
		},
		{
			name:          "empty allowed models allows any model",
			allowedModels: []string{},
			modelName:     "gpt-4",
			expected:      true,
		},
		{
			name:          "empty allowed models allows any model 2",
			allowedModels: []string{},
			modelName:     "claude-3",
			expected:      true,
		},
		{
			name:          "model in list returns true",
			allowedModels: []string{"gpt-4", "claude-3", "gemini-pro"},
			modelName:     "claude-3",
			expected:      true,
		},
		{
			name:          "model not in list returns false",
			allowedModels: []string{"gpt-4", "claude-3"},
			modelName:     "gemini-pro",
			expected:      false,
		},
		{
			name:          "exact match required case-sensitive - lowercase",
			allowedModels: []string{"gpt-4"},
			modelName:     "gpt-4",
			expected:      true,
		},
		{
			name:          "exact match required case-sensitive - uppercase mismatch",
			allowedModels: []string{"gpt-4"},
			modelName:     "GPT-4",
			expected:      false,
		},
		{
			name:          "exact match required case-sensitive - mixed case mismatch",
			allowedModels: []string{"Claude-3"},
			modelName:     "claude-3",
			expected:      false,
		},
		{
			name:          "single model in list exact match",
			allowedModels: []string{"o3-mini"},
			modelName:     "o3-mini",
			expected:      true,
		},
		{
			name:          "single model in list mismatch",
			allowedModels: []string{"o3-mini"},
			modelName:     "o3",
			expected:      false,
		},
		{
			name:          "first model in list",
			allowedModels: []string{"gpt-4", "claude-3"},
			modelName:     "gpt-4",
			expected:      true,
		},
		{
			name:          "last model in list",
			allowedModels: []string{"gpt-4", "claude-3"},
			modelName:     "claude-3",
			expected:      true,
		},
		{
			name:          "empty model name when models allowed",
			allowedModels: []string{"gpt-4"},
			modelName:     "",
			expected:      false,
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
