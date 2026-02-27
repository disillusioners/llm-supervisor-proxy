package providers

import (
	"fmt"
)

// ProviderType represents a supported provider type
type ProviderType string

const (
	ProviderOpenAI    ProviderType = "openai"
	ProviderAnthropic ProviderType = "anthropic"
	ProviderGemini    ProviderType = "gemini"
	ProviderZhipu     ProviderType = "zhipu"
	ProviderAzure     ProviderType = "azure"
	ProviderZAI       ProviderType = "zai"
	ProviderMiniMax   ProviderType = "minimax"
)

// NewProvider creates a new provider based on the provider type
func NewProvider(providerType, apiKey, baseURL string) (Provider, error) {
	switch ProviderType(providerType) {
	case ProviderOpenAI:
		return NewOpenAIProvider(apiKey, baseURL), nil
	case ProviderAnthropic:
		// TODO: Implement Anthropic provider
		return nil, fmt.Errorf("anthropic provider not yet implemented")
	case ProviderGemini:
		// TODO: Implement Gemini provider
		return nil, fmt.Errorf("gemini provider not yet implemented")
	case ProviderZhipu:
		// Zhipu uses OpenAI-compatible API
		if baseURL == "" {
			baseURL = "https://open.bigmodel.cn/api/paas/v4"
		}
		return NewOpenAIProvider(apiKey, baseURL), nil
	case ProviderZAI:
		// ZAI coding plan uses OpenAI-compatible API
		if baseURL == "" {
			baseURL = "https://api.z.ai/api/coding/paas/v4"
		}
		return NewOpenAIProvider(apiKey, baseURL), nil
	case ProviderMiniMax:
		// MiniMax uses OpenAI-compatible API
		if baseURL == "" {
			baseURL = "https://api.minimax.io/v1"
		}
		return NewOpenAIProvider(apiKey, baseURL), nil
	case ProviderAzure:
		// Azure OpenAI uses OpenAI-compatible API with different auth
		// For now, treat as OpenAI with custom base URL
		return NewOpenAIProvider(apiKey, baseURL), nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", providerType)
	}
}

// IsProviderSupported checks if a provider type is supported
func IsProviderSupported(providerType string) bool {
	switch ProviderType(providerType) {
	case ProviderOpenAI, ProviderAnthropic, ProviderGemini, ProviderZhipu, ProviderAzure, ProviderZAI, ProviderMiniMax:
		return true
	default:
		return false
	}
}

// GetProviderTypes returns a list of supported provider types
func GetProviderTypes() []ProviderType {
	return []ProviderType{
		ProviderOpenAI,
		ProviderAnthropic,
		ProviderGemini,
		ProviderZhipu,
		ProviderAzure,
		ProviderZAI,
		ProviderMiniMax,
	}
}
