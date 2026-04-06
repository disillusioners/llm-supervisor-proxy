package models

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/crypto"
)

// =============================================================================
// Helper: reset encryption state for tests
// =============================================================================

// resetCryptoState resets the encryption state for testing
func resetCryptoState() {
	crypto.ResetEncryptionState()
}

// =============================================================================
// isValidProvider tests
// =============================================================================

func TestIsValidProvider(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		want     bool
	}{
		// Valid providers
		{"openai valid", "openai", true},
		{"anthropic valid", "anthropic", true},
		{"gemini valid", "gemini", true},
		{"zhipu valid", "zhipu", true},
		{"azure valid", "azure", true},
		{"zai valid", "zai", true},
		{"minimax valid", "minimax", true},
		{"grok valid", "grok", true},

		// Case sensitivity - should be lowercase
		{"OpenAI uppercase", "OpenAI", false},
		{"ANTHROPIC uppercase", "ANTHROPIC", false},
		{"Gemini mixed case", "Gemini", false},
		{"AZURE uppercase", "AZURE", false},

		// Invalid providers
		{"empty string", "", false},
		{"unknown provider", "unknown", false},
		{"cohere", "cohere", false},
		{"mistral", "mistral", false},
		{"google", "google", false},
		{"ollama", "ollama", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidProvider(tt.provider)
			if got != tt.want {
				t.Errorf("isValidProvider(%q) = %v, want %v", tt.provider, got, tt.want)
			}
		})
	}
}

// =============================================================================
// expandEnvVars tests
// =============================================================================

func TestExpandEnvVars(t *testing.T) {
	// Setup: set some test environment variables
	testVars := map[string]string{
		"TEST_API_KEY":    "secret-key-123",
		"TEST_EMPTY":      "",
		"TEST_MULTI":      "multi-value",
		"NONEXISTENT_VAR": "",
	}
	originalVals := make(map[string]string)
	for k, v := range testVars {
		originalVals[k] = os.Getenv(k)
		os.Setenv(k, v)
	}
	defer func() {
		for k, v := range originalVals {
			if v == "" {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, v)
			}
		}
	}()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		// Simple ${VAR} expansion
		{
			name:  "simple expansion",
			input: "${TEST_API_KEY}",
			want:  "secret-key-123",
		},
		{
			name:  "empty env var",
			input: "${TEST_EMPTY}",
			want:  "",
		},
		{
			name:  "nonexistent env var",
			input: "${NONEXISTENT_VAR}",
			want:  "",
		},
		{
			name:  "env var not defined",
			input: "${DEFINITELY_NOT_SET}",
			want:  "",
		},

		// ${VAR:-default} syntax
		{
			name:  "default with unset var",
			input: "${UNSET_VAR:-my-default}",
			want:  "my-default",
		},
		{
			name:  "default with empty var",
			input: "${TEST_EMPTY:-default-value}",
			want:  "default-value",
		},
		{
			name:  "default with set var",
			input: "${TEST_API_KEY:-other-value}",
			want:  "secret-key-123", // Should use env var, not default
		},
		{
			name:  "default with spaces",
			input: "${UNSET:-value with spaces}",
			want:  "value with spaces",
		},
		{
			name:  "default with hyphen",
			input: "${UNSET:-fallback-value}",
			want:  "fallback-value",
		},

		// Multiple variables
		{
			name:  "multiple variables",
			input: "${TEST_API_KEY}:${TEST_MULTI}",
			want:  "secret-key-123:multi-value",
		},
		{
			name:  "multiple with defaults",
			input: "${UNSET1:-first}:${UNSET2:-second}",
			want:  "first:second",
		},
		{
			name:  "mixed variables and defaults",
			input: "${TEST_API_KEY}:${UNSET:-fallback}",
			want:  "secret-key-123:fallback",
		},

		// Edge cases
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "no variables",
			input: "plain-text-no-vars",
			want:  "plain-text-no-vars",
		},
		{
			name:  "text around variable",
			input: "prefix-${TEST_API_KEY}-suffix",
			want:  "prefix-secret-key-123-suffix",
		},
		{
			name:  "empty default value",
			input: "${UNSET:-}",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandEnvVars(tt.input)
			if got != tt.want {
				t.Errorf("expandEnvVars(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// =============================================================================
// CredentialConfig.Validate tests
// =============================================================================

func TestCredentialConfigValidate(t *testing.T) {
	tests := []struct {
		name       string
		cred       CredentialConfig
		wantErr    bool
		errContain string
	}{
		// Valid credentials
		{
			name: "valid openai credential",
			cred: CredentialConfig{
				ID:       "openai-creds",
				Provider: "openai",
				APIKey:   "sk-test-key",
			},
			wantErr:    false,
			errContain: "",
		},
		{
			name: "valid anthropic credential",
			cred: CredentialConfig{
				ID:       "anthropic-creds",
				Provider: "anthropic",
				APIKey:   "sk-ant-test",
			},
			wantErr:    false,
			errContain: "",
		},
		{
			name: "valid credential with base URL",
			cred: CredentialConfig{
				ID:       "custom-provider",
				Provider: "openai",
				APIKey:   "sk-test",
				BaseURL:  "https://api.example.com",
			},
			wantErr:    false,
			errContain: "",
		},

		// ID validation
		{
			name: "missing ID",
			cred: CredentialConfig{
				ID:       "",
				Provider: "openai",
				APIKey:   "sk-test",
			},
			wantErr:    true,
			errContain: "invalid credential ID",
		},

		// Provider validation
		{
			name: "missing provider",
			cred: CredentialConfig{
				ID:       "test-id",
				Provider: "",
				APIKey:   "sk-test",
			},
			wantErr:    true,
			errContain: "provider is required",
		},
		{
			name: "invalid provider",
			cred: CredentialConfig{
				ID:       "test-id",
				Provider: "unknown",
				APIKey:   "sk-test",
			},
			wantErr:    true,
			errContain: "invalid provider",
		},

		// API key validation (after env var expansion)
		{
			name: "missing API key",
			cred: CredentialConfig{
				ID:       "test-id",
				Provider: "openai",
				APIKey:   "",
			},
			wantErr:    true,
			errContain: "api_key is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cred.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errContain != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContain) {
					t.Errorf("Validate() error = %v, should contain %q", err, tt.errContain)
				}
			}
		})
	}
}

// =============================================================================
// CredentialConfig.ResolveAPIKey tests
// =============================================================================

func TestCredentialConfigResolveAPIKey(t *testing.T) {
	// Setup environment variables for testing
	os.Setenv("RESOLVE_TEST_KEY", "resolved-api-key")
	os.Setenv("RESOLVE_EMPTY_KEY", "")
	defer func() {
		os.Unsetenv("RESOLVE_TEST_KEY")
		os.Unsetenv("RESOLVE_EMPTY_KEY")
	}()

	tests := []struct {
		name   string
		cred   CredentialConfig
		expect string
	}{
		{
			name: "plain API key",
			cred: CredentialConfig{
				APIKey: "sk-plain-key",
			},
			expect: "sk-plain-key",
		},
		{
			name: "env var expansion",
			cred: CredentialConfig{
				APIKey: "${RESOLVE_TEST_KEY}",
			},
			expect: "resolved-api-key",
		},
		{
			name: "env var with default - var not set",
			cred: CredentialConfig{
				APIKey: "${UNSET_VAR:-default-key}",
			},
			expect: "default-key",
		},
		{
			name: "env var with default - var set",
			cred: CredentialConfig{
				APIKey: "${RESOLVE_TEST_KEY:-other-key}",
			},
			expect: "resolved-api-key",
		},
		{
			name: "empty API key",
			cred: CredentialConfig{
				APIKey: "",
			},
			expect: "",
		},
		{
			name: "empty env var",
			cred: CredentialConfig{
				APIKey: "${RESOLVE_EMPTY_KEY}",
			},
			expect: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cred.ResolveAPIKey()
			if got != tt.expect {
				t.Errorf("ResolveAPIKey() = %q, want %q", got, tt.expect)
			}
		})
	}
}

// =============================================================================
// NewCredentialsConfig tests
// =============================================================================

func TestNewCredentialsConfig(t *testing.T) {
	cc := NewCredentialsConfig()
	if cc == nil {
		t.Fatal("NewCredentialsConfig() returned nil")
	}
	if cc.credentials == nil {
		t.Error("NewCredentialsConfig().credentials is nil")
	}
	if len(cc.credentials) != 0 {
		t.Errorf("NewCredentialsConfig().credentials has %d items, want 0", len(cc.credentials))
	}
}

// =============================================================================
// CredentialsConfig.GetCredential tests
// =============================================================================

func TestCredentialsConfigGetCredential(t *testing.T) {
	// Reset crypto state to avoid encryption issues
	resetCryptoState()
	os.Unsetenv(crypto.EnvEncryptionKey)

	cc := NewCredentialsConfig()

	// Test getting from empty config
	t.Run("get from empty config", func(t *testing.T) {
		got := cc.GetCredential("nonexistent")
		if got != nil {
			t.Errorf("GetCredential() from empty config = %v, want nil", got)
		}
	})

	// Add a credential directly (bypass encryption for setup)
	cc.mu.Lock()
	cc.credentials["test-id"] = CredentialConfig{
		ID:       "test-id",
		Provider: "openai",
		APIKey:   "test-api-key",
	}
	cc.mu.Unlock()

	// Test getting existing credential
	t.Run("get existing credential", func(t *testing.T) {
		got := cc.GetCredential("test-id")
		if got == nil {
			t.Fatal("GetCredential() returned nil for existing credential")
		}
		if got.ID != "test-id" {
			t.Errorf("GetCredential().ID = %q, want %q", got.ID, "test-id")
		}
		if got.Provider != "openai" {
			t.Errorf("GetCredential().Provider = %q, want %q", got.Provider, "openai")
		}
		if got.APIKey != "test-api-key" {
			t.Errorf("GetCredential().APIKey = %q, want %q", got.APIKey, "test-api-key")
		}
	})

	// Test getting nonexistent credential from non-empty config
	t.Run("get nonexistent from non-empty config", func(t *testing.T) {
		got := cc.GetCredential("other-id")
		if got != nil {
			t.Errorf("GetCredential() for nonexistent = %v, want nil", got)
		}
	})
}

// =============================================================================
// CredentialsConfig.GetCredentials tests
// =============================================================================

func TestCredentialsConfigGetCredentials(t *testing.T) {
	// Reset crypto state
	resetCryptoState()
	os.Unsetenv(crypto.EnvEncryptionKey)

	cc := NewCredentialsConfig()

	// Test empty config
	t.Run("empty config", func(t *testing.T) {
		got := cc.GetCredentials()
		if len(got) != 0 {
			t.Errorf("GetCredentials() from empty config has %d items, want 0", len(got))
		}
	})

	// Add credentials directly
	cc.mu.Lock()
	cc.credentials["cred1"] = CredentialConfig{
		ID:       "cred1",
		Provider: "openai",
		APIKey:   "key1",
	}
	cc.credentials["cred2"] = CredentialConfig{
		ID:       "cred2",
		Provider: "anthropic",
		APIKey:   "key2",
	}
	cc.mu.Unlock()

	// Test with credentials
	t.Run("with credentials", func(t *testing.T) {
		got := cc.GetCredentials()
		if len(got) != 2 {
			t.Errorf("GetCredentials() returned %d items, want 2", len(got))
		}
	})
}

// =============================================================================
// CredentialsConfig.AddCredential tests
// =============================================================================

func TestCredentialsConfigAddCredential(t *testing.T) {
	// Reset crypto state
	resetCryptoState()
	os.Unsetenv(crypto.EnvEncryptionKey)

	cc := NewCredentialsConfig()

	tests := []struct {
		name       string
		cred       CredentialConfig
		wantErr    bool
		errContain string
	}{
		// Valid additions
		{
			name: "add valid openai credential",
			cred: CredentialConfig{
				ID:       "openai-1",
				Provider: "openai",
				APIKey:   "sk-test-key",
			},
			wantErr:    false,
			errContain: "",
		},
		{
			name: "add valid anthropic credential",
			cred: CredentialConfig{
				ID:       "anthropic-1",
				Provider: "anthropic",
				APIKey:   "sk-ant-key",
			},
			wantErr:    false,
			errContain: "",
		},
		{
			name: "add credential with base URL",
			cred: CredentialConfig{
				ID:       "custom-1",
				Provider: "azure",
				APIKey:   "azure-key",
				BaseURL:  "https://azure.example.com",
			},
			wantErr:    false,
			errContain: "",
		},

		// Validation failures
		{
			name: "add credential missing ID",
			cred: CredentialConfig{
				ID:       "",
				Provider: "openai",
				APIKey:   "sk-test",
			},
			wantErr:    true,
			errContain: "invalid credential ID",
		},
		{
			name: "add credential missing provider",
			cred: CredentialConfig{
				ID:       "test-id",
				Provider: "",
				APIKey:   "sk-test",
			},
			wantErr:    true,
			errContain: "provider is required",
		},
		{
			name: "add credential missing API key",
			cred: CredentialConfig{
				ID:       "test-id",
				Provider: "openai",
				APIKey:   "",
			},
			wantErr:    true,
			errContain: "api_key is required",
		},
		{
			name: "add credential invalid provider",
			cred: CredentialConfig{
				ID:       "test-id",
				Provider: "unknown",
				APIKey:   "sk-test",
			},
			wantErr:    true,
			errContain: "invalid provider",
		},

		// Duplicate ID
		{
			name: "add duplicate ID",
			cred: CredentialConfig{
				ID:       "openai-1", // Already added above
				Provider: "openai",
				APIKey:   "sk-other-key",
			},
			wantErr:    true,
			errContain: "duplicate credential ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cc.AddCredential(tt.cred)
			if (err != nil) != tt.wantErr {
				t.Errorf("AddCredential() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errContain != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContain) {
					t.Errorf("AddCredential() error = %v, should contain %q", err, tt.errContain)
				}
			}
		})
	}
}

// =============================================================================
// CredentialsConfig.UpdateCredential tests
// =============================================================================

func TestCredentialsConfigUpdateCredential(t *testing.T) {
	// Reset crypto state
	resetCryptoState()
	os.Unsetenv(crypto.EnvEncryptionKey)

	cc := NewCredentialsConfig()

	// First add a credential to update
	err := cc.AddCredential(CredentialConfig{
		ID:       "update-test",
		Provider: "openai",
		APIKey:   "original-key",
	})
	if err != nil {
		t.Fatalf("Failed to add initial credential: %v", err)
	}

	tests := []struct {
		name       string
		id         string
		cred       CredentialConfig
		wantErr    bool
		errContain string
	}{
		// Valid updates
		{
			name: "update API key",
			id:   "update-test",
			cred: CredentialConfig{
				ID:       "update-test",
				Provider: "openai",
				APIKey:   "updated-key",
			},
			wantErr:    false,
			errContain: "",
		},
		{
			name: "update base URL",
			id:   "update-test",
			cred: CredentialConfig{
				ID:       "update-test",
				Provider: "openai",
				APIKey:   "updated-key",
				BaseURL:  "https://new-endpoint.com",
			},
			wantErr:    false,
			errContain: "",
		},

		// Credential not found
		{
			name: "update nonexistent credential",
			id:   "does-not-exist",
			cred: CredentialConfig{
				ID:       "does-not-exist",
				Provider: "openai",
				APIKey:   "sk-test",
			},
			wantErr:    true,
			errContain: "credential not found",
		},

		// Cannot change ID
		{
			name: "cannot change ID",
			id:   "update-test",
			cred: CredentialConfig{
				ID:       "new-id", // Different from the id param
				Provider: "openai",
				APIKey:   "sk-test",
			},
			wantErr:    true,
			errContain: "cannot change credential ID",
		},

		// Validation failures
		{
			name: "update with missing API key",
			id:   "update-test",
			cred: CredentialConfig{
				ID:       "update-test",
				Provider: "openai",
				APIKey:   "",
			},
			wantErr:    true,
			errContain: "api_key is required",
		},
		{
			name: "update with invalid provider",
			id:   "update-test",
			cred: CredentialConfig{
				ID:       "update-test",
				Provider: "invalid",
				APIKey:   "sk-test",
			},
			wantErr:    true,
			errContain: "invalid provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cc.UpdateCredential(tt.id, tt.cred)
			if (err != nil) != tt.wantErr {
				t.Errorf("UpdateCredential() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errContain != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContain) {
					t.Errorf("UpdateCredential() error = %v, should contain %q", err, tt.errContain)
				}
			}
		})
	}
}

// =============================================================================
// CredentialsConfig.RemoveCredential tests
// =============================================================================

func TestCredentialsConfigRemoveCredential(t *testing.T) {
	// Reset crypto state
	resetCryptoState()
	os.Unsetenv(crypto.EnvEncryptionKey)

	cc := NewCredentialsConfig()

	// Add a credential to remove
	err := cc.AddCredential(CredentialConfig{
		ID:       "to-remove",
		Provider: "openai",
		APIKey:   "sk-test",
	})
	if err != nil {
		t.Fatalf("Failed to add credential: %v", err)
	}

	tests := []struct {
		name       string
		id         string
		wantErr    bool
		errContain string
	}{
		// Valid removals
		{
			name:    "remove existing credential",
			id:      "to-remove",
			wantErr: false,
		},
		{
			name:    "remove non-existent credential",
			id:      "never-existed",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cc.RemoveCredential(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("RemoveCredential() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errContain != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContain) {
					t.Errorf("RemoveCredential() error = %v, should contain %q", err, tt.errContain)
				}
			}
		})
	}

	// Verify the credential was actually removed
	t.Run("verify removal", func(t *testing.T) {
		got := cc.GetCredential("to-remove")
		if got != nil {
			t.Errorf("GetCredential() after removal = %v, want nil", got)
		}
	})
}

// =============================================================================
// CredentialsConfig.SetCredentials tests
// =============================================================================

func TestCredentialsConfigSetCredentials(t *testing.T) {
	// Reset crypto state
	resetCryptoState()
	os.Unsetenv(crypto.EnvEncryptionKey)

	cc := NewCredentialsConfig()

	// Add some initial credentials
	_ = cc.AddCredential(CredentialConfig{
		ID:       "initial-1",
		Provider: "openai",
		APIKey:   "key1",
	})

	// Set new credentials
	newCreds := []CredentialConfig{
		{
			ID:       "new-1",
			Provider: "anthropic",
			APIKey:   "ant-key",
		},
		{
			ID:       "new-2",
			Provider: "gemini",
			APIKey:   "gemini-key",
		},
	}

	cc.SetCredentials(newCreds)

	// Verify old credential is gone
	t.Run("old credentials removed", func(t *testing.T) {
		got := cc.GetCredential("initial-1")
		if got != nil {
			t.Errorf("GetCredential() for removed credential = %v, want nil", got)
		}
	})

	// Verify new credentials exist
	t.Run("new credentials added", func(t *testing.T) {
		got := cc.GetCredentials()
		if len(got) != 2 {
			t.Errorf("GetCredentials() returned %d items, want 2", len(got))
		}

		// Check specific credentials
		cred1 := cc.GetCredential("new-1")
		if cred1 == nil || cred1.Provider != "anthropic" {
			t.Errorf("GetCredential(new-1) = %v, want provider=anthropic", cred1)
		}

		cred2 := cc.GetCredential("new-2")
		if cred2 == nil || cred2.Provider != "gemini" {
			t.Errorf("GetCredential(new-2) = %v, want provider=gemini", cred2)
		}
	})
}

// =============================================================================
// CredentialsConfig.ToSlice tests
// =============================================================================

func TestCredentialsConfigToSlice(t *testing.T) {
	// Reset crypto state
	resetCryptoState()
	os.Unsetenv(crypto.EnvEncryptionKey)

	cc := NewCredentialsConfig()

	// Test empty config
	t.Run("empty config", func(t *testing.T) {
		got := cc.ToSlice()
		if len(got) != 0 {
			t.Errorf("ToSlice() from empty config = %d items, want 0", len(got))
		}
	})

	// Add credentials directly (bypass encryption for deterministic testing)
	cc.mu.Lock()
	cc.credentials["cred-a"] = CredentialConfig{
		ID:       "cred-a",
		Provider: "openai",
		APIKey:   "key-a",
	}
	cc.credentials["cred-b"] = CredentialConfig{
		ID:       "cred-b",
		Provider: "anthropic",
		APIKey:   "key-b",
	}
	cc.mu.Unlock()

	// Test with credentials
	t.Run("with credentials", func(t *testing.T) {
		got := cc.ToSlice()
		if len(got) != 2 {
			t.Errorf("ToSlice() returned %d items, want 2", len(got))
		}

		// Verify IDs are present
		ids := make(map[string]bool)
		for _, cred := range got {
			ids[cred.ID] = true
		}

		if !ids["cred-a"] {
			t.Error("ToSlice() missing cred-a")
		}
		if !ids["cred-b"] {
			t.Error("ToSlice() missing cred-b")
		}
	})
}

// =============================================================================
// Thread safety tests
// =============================================================================

func TestCredentialsConfigConcurrentAccess(t *testing.T) {
	// Reset crypto state
	resetCryptoState()
	os.Unsetenv(crypto.EnvEncryptionKey)

	cc := NewCredentialsConfig()

	// Add initial credential
	_ = cc.AddCredential(CredentialConfig{
		ID:       "initial",
		Provider: "openai",
		APIKey:   "initial-key",
	})

	const numGoroutines = 10
	var wg sync.WaitGroup
	wg.Add(numGoroutines * 3) // 3 operation types: Get, Add, Update

	// Concurrent readers
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = cc.GetCredential("initial")
				_ = cc.GetCredentials()
			}
		}()
	}

	// Concurrent adders - each with unique ID
	for i := 0; i < numGoroutines; i++ {
		id := i // capture loop variable
		go func() {
			defer wg.Done()
			// Each goroutine adds with its own unique ID (after some initial work)
			for j := 0; j < 10; j++ {
				uniqueID := "concurrent-add-" + string(rune('a'+id)) + "-" + string(rune('0'+j))
				_ = cc.AddCredential(CredentialConfig{
					ID:       uniqueID,
					Provider: "openai",
					APIKey:   "key",
				})
			}
		}()
	}

	// Concurrent updaters - only one at a time for "initial"
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_ = cc.UpdateCredential("initial", CredentialConfig{
					ID:       "initial",
					Provider: "openai",
					APIKey:   "updated-key",
				})
			}
		}()
	}

	wg.Wait()
	// If we get here without deadlock or race detection failures, test passes
}

// =============================================================================
// Encryption integration tests
// =============================================================================

func TestCredentialsConfigEncryption(t *testing.T) {
	// Generate a test key
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	// Reset crypto state and set key
	resetCryptoState()
	os.Setenv(crypto.EnvEncryptionKey, key)
	defer os.Unsetenv(crypto.EnvEncryptionKey)

	cc := NewCredentialsConfig()

	// Add a credential - API key should be encrypted
	originalKey := "my-super-secret-api-key"
	err = cc.AddCredential(CredentialConfig{
		ID:       "encrypted-test",
		Provider: "openai",
		APIKey:   originalKey,
	})
	if err != nil {
		t.Fatalf("AddCredential() failed: %v", err)
	}

	// GetCredential should return decrypted key
	t.Run("API key is decrypted on get", func(t *testing.T) {
		got := cc.GetCredential("encrypted-test")
		if got == nil {
			t.Fatal("GetCredential() returned nil")
		}
		if got.APIKey != originalKey {
			t.Errorf("GetCredential().APIKey = %q, want %q (decrypted)", got.APIKey, originalKey)
		}
	})

	// GetCredentials should return decrypted keys
	t.Run("API keys are decrypted in GetCredentials", func(t *testing.T) {
		creds := cc.GetCredentials()
		if len(creds) != 1 {
			t.Fatalf("GetCredentials() returned %d items, want 1", len(creds))
		}
		if creds[0].APIKey != originalKey {
			t.Errorf("GetCredentials()[0].APIKey = %q, want %q", creds[0].APIKey, originalKey)
		}
	})

	// ToSlice returns stored (encrypted) keys
	t.Run("ToSlice returns encrypted keys", func(t *testing.T) {
		slice := cc.ToSlice()
		if len(slice) != 1 {
			t.Fatalf("ToSlice() returned %d items, want 1", len(slice))
		}
		// The stored API key should be encrypted (not equal to original)
		if slice[0].APIKey == originalKey {
			t.Error("ToSlice() should return encrypted API key, but got plaintext")
		}
	})

	// Update should also encrypt
	t.Run("Update encrypts new API key", func(t *testing.T) {
		newKey := "new-secret-key"
		err = cc.UpdateCredential("encrypted-test", CredentialConfig{
			ID:       "encrypted-test",
			Provider: "openai",
			APIKey:   newKey,
		})
		if err != nil {
			t.Fatalf("UpdateCredential() failed: %v", err)
		}

		// Get decrypted key
		got := cc.GetCredential("encrypted-test")
		if got.APIKey != newKey {
			t.Errorf("GetCredential().APIKey after update = %q, want %q", got.APIKey, newKey)
		}
	})
}

// =============================================================================
// Credential error type tests
// =============================================================================

func TestCredentialErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		msg  string
	}{
		{
			name: "ErrInvalidCredentialID",
			err:  ErrInvalidCredentialID,
			msg:  "invalid credential ID: cannot be empty",
		},
		{
			name: "ErrDuplicateCredentialID",
			err:  ErrDuplicateCredentialID,
			msg:  "duplicate credential ID",
		},
		{
			name: "ErrCredentialNotFound",
			err:  ErrCredentialNotFound,
			msg:  "credential not found",
		},
		{
			name: "ErrCredentialInUse",
			err:  ErrCredentialInUse,
			msg:  "credential is in use by one or more models",
		},
		{
			name: "ErrCannotChangeCredentialID",
			err:  ErrCannotChangeCredentialID,
			msg:  "cannot change credential ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Error() != tt.msg {
				t.Errorf("Error() = %q, want %q", tt.err.Error(), tt.msg)
			}
		})
	}
}

// =============================================================================
// Edge case tests
// =============================================================================

func TestCredentialsEdgeCases(t *testing.T) {
	// Reset crypto state
	resetCryptoState()
	os.Unsetenv(crypto.EnvEncryptionKey)

	t.Run("credential with special characters in API key", func(t *testing.T) {
		cc := NewCredentialsConfig()
		specialKey := "sk-test!@#$%^&*()_+-=[]{}|;':\",./<>?"
		err := cc.AddCredential(CredentialConfig{
			ID:       "special-chars",
			Provider: "openai",
			APIKey:   specialKey,
		})
		if err != nil {
			t.Fatalf("AddCredential() with special chars failed: %v", err)
		}

		got := cc.GetCredential("special-chars")
		if got == nil {
			t.Fatal("GetCredential() returned nil")
		}
		if got.APIKey != specialKey {
			t.Errorf("GetCredential().APIKey = %q, want %q", got.APIKey, specialKey)
		}
	})

	t.Run("credential with unicode in API key", func(t *testing.T) {
		cc := NewCredentialsConfig()
		unicodeKey := "密钥-中文-key-🔐"
		err := cc.AddCredential(CredentialConfig{
			ID:       "unicode-key",
			Provider: "openai",
			APIKey:   unicodeKey,
		})
		if err != nil {
			t.Fatalf("AddCredential() with unicode failed: %v", err)
		}

		got := cc.GetCredential("unicode-key")
		if got == nil {
			t.Fatal("GetCredential() returned nil")
		}
		if got.APIKey != unicodeKey {
			t.Errorf("GetCredential().APIKey = %q, want %q", got.APIKey, unicodeKey)
		}
	})

	t.Run("credential with very long API key", func(t *testing.T) {
		cc := NewCredentialsConfig()
		longKey := strings.Repeat("x", 10000)
		err := cc.AddCredential(CredentialConfig{
			ID:       "long-key",
			Provider: "openai",
			APIKey:   longKey,
		})
		if err != nil {
			t.Fatalf("AddCredential() with long key failed: %v", err)
		}

		got := cc.GetCredential("long-key")
		if got == nil {
			t.Fatal("GetCredential() returned nil")
		}
		if got.APIKey != longKey {
			t.Errorf("GetCredential().APIKey length = %d, want %d", len(got.APIKey), len(longKey))
		}
	})

	t.Run("empty base URL is allowed", func(t *testing.T) {
		cc := NewCredentialsConfig()
		err := cc.AddCredential(CredentialConfig{
			ID:       "empty-baseurl",
			Provider: "openai",
			APIKey:   "sk-test",
			BaseURL:  "",
		})
		if err != nil {
			t.Fatalf("AddCredential() with empty base URL failed: %v", err)
		}

		got := cc.GetCredential("empty-baseurl")
		if got == nil {
			t.Fatal("GetCredential() returned nil")
		}
		if got.BaseURL != "" {
			t.Errorf("GetCredential().BaseURL = %q, want empty", got.BaseURL)
		}
	})
}

// =============================================================================
// Benchmark tests
// =============================================================================

func BenchmarkGetCredential(b *testing.B) {
	resetCryptoState()
	os.Unsetenv(crypto.EnvEncryptionKey)

	cc := NewCredentialsConfig()
	_ = cc.AddCredential(CredentialConfig{
		ID:       "bench-cred",
		Provider: "openai",
		APIKey:   "bench-key",
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cc.GetCredential("bench-cred")
	}
}

func BenchmarkGetCredentials(b *testing.B) {
	resetCryptoState()
	os.Unsetenv(crypto.EnvEncryptionKey)

	cc := NewCredentialsConfig()
	for i := 0; i < 100; i++ {
		_ = cc.AddCredential(CredentialConfig{
			ID:       "bench-cred-" + string(rune(i)),
			Provider: "openai",
			APIKey:   "bench-key",
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cc.GetCredentials()
	}
}

func BenchmarkExpandEnvVars(b *testing.B) {
	os.Setenv("BENCH_VAR", "benchmark-value")
	defer os.Unsetenv("BENCH_VAR")

	input := "prefix-${BENCH_VAR}-suffix"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = expandEnvVars(input)
	}
}
