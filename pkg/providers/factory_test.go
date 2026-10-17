package providers

import (
	"testing"
)

func TestNewProvider(t *testing.T) {
	tests := []struct {
		name        string
		provider    string
		expectError bool
	}{
		{"openai", "openai", false},
		{"zhipu", "zhipu", false},
		{"azure", "azure", false},
		{"zai", "zai", false},
		{"minimax", "minimax", false},
		{"grok", "grok", false},
		{"deepseek", "deepseek", false},
		{"opencode", "opencode", false},
		{"unknown", "unknown", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewProvider(tt.provider, "test-key", "")
			if tt.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if provider == nil {
					t.Error("expected provider, got nil")
				}
			}
		})
	}
}

func TestIsProviderSupported(t *testing.T) {
	supported := []string{"openai", "anthropic", "gemini", "zhipu", "azure", "zai", "minimax", "grok", "deepseek", "opencode"}
	for _, p := range supported {
		if !IsProviderSupported(p) {
			t.Errorf("expected %s to be supported", p)
		}
	}

	if IsProviderSupported("unknown") {
		t.Error("expected unknown provider to not be supported")
	}
}

func TestGetProviderTypes(t *testing.T) {
	types := GetProviderTypes()
	// 11 types post-opencode commission (OpenCode Zen is an
	// OpenAI-compatible chat-completions provider, default base
	// URL https://opencode.ai/inference/openai/v1; see
	// factory.go:ProviderOpencode). Decision Models / §5.2 also
	// adds ProviderTypesafe for /v1/decisions — the chat layer
	// doesn't speak System One, so typesafe returns "not yet
	// implemented" from NewProvider, but the id is registered so
	// /fe/api/providers and the FE DecisionModelForm provider
	// dropdown can see it.
	if len(types) != 11 {
		t.Errorf("expected 11 provider types, got %d", len(types))
	}
	// Spot-check the typesafe addition.
	foundTypesafe := false
	for _, t := range types {
		if t == ProviderTypesafe {
			foundTypesafe = true
			break
		}
	}
	if !foundTypesafe {
		t.Errorf("ProviderTypesafe missing from GetProviderTypes()")
	}
}

func TestProviderError(t *testing.T) {
	err := &ProviderError{
		Provider:   "openai",
		StatusCode: 429,
		Message:    "rate limit exceeded",
		Retryable:  true,
	}

	expected := "openai: rate limit exceeded"
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}

	if !err.IsRetryable() {
		t.Error("expected error to be retryable")
	}
}
