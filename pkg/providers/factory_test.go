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
		{"opencode-go", "opencode-go", false},
		{"lightllm", "lightllm", false},
		{"openrouter", "openrouter", false},
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
	supported := []string{"openai", "anthropic", "gemini", "zhipu", "azure", "zai", "minimax", "grok", "deepseek", "opencode", "opencode-go", "lightllm", "openrouter"}
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
	// 14 types post-openrouter commission — OpenRouter becomes a
	// REGULAR OpenAI-compatible chat provider (multi-vendor model
	// router), default base URL https://openrouter.ai/api/v1; see
	// factory.go:ProviderOpenRouter. Prior lineage: 13 post-lightllm
	// commission (remove-external-upstream
	// phase 2 — LightLLM becomes a REGULAR OpenAI-compatible chat
	// provider, default base URL http://localhost:4001 inherited
	// from the retired external-upstream config default; see
	// factory.go:ProviderLightLLM), 12 post-opencode-go
	// commission (OpenCode Go is a DISTINCT provider from OpenCode
	// Zen — subscription realm, default base URL
	// https://opencode.ai/inference/go/openai/v1, provider-native
	// session/UA attribution; see factory.go:ProviderOpencodeGo and
	// opencodego.go), 11 post-opencode. Decision Models / §5.2
	// adds ProviderTypesafe for /v1/decisions — the chat layer
	// doesn't speak System One, so typesafe returns "not yet
	// implemented" from NewProvider, but the id is registered so
	// /fe/api/providers and the FE DecisionModelForm provider
	// dropdown can see it.
	if len(types) != 14 {
		t.Errorf("expected 14 provider types, got %d", len(types))
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
	// Spot-check the opencode-go addition (DISTINCT from opencode).
	foundOpencodeGo := false
	for _, t := range types {
		if t == ProviderOpencodeGo {
			foundOpencodeGo = true
			break
		}
	}
	if !foundOpencodeGo {
		t.Errorf("ProviderOpencodeGo missing from GetProviderTypes()")
	}
	// Spot-check the lightllm addition (regular OpenAI-compatible chat provider).
	foundLightLLM := false
	for _, t := range types {
		if t == ProviderLightLLM {
			foundLightLLM = true
			break
		}
	}
	if !foundLightLLM {
		t.Errorf("ProviderLightLLM missing from GetProviderTypes()")
	}
	// Spot-check the openrouter addition (regular OpenAI-compatible chat provider).
	foundOpenRouter := false
	for _, t := range types {
		if t == ProviderOpenRouter {
			foundOpenRouter = true
			break
		}
	}
	if !foundOpenRouter {
		t.Errorf("ProviderOpenRouter missing from GetProviderTypes()")
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
