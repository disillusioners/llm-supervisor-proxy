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
	ProviderGork      ProviderType = "gork"
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
	case ProviderGork:
		// Gork (X.ai) uses OpenAI-compatible API
		if baseURL == "" {
			baseURL = "https://api.x.ai/v1"
		}
		return NewOpenAIProvider(apiKey, baseURL), nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", providerType)
	}
}

// IsProviderSupported checks if a provider type is supported
func IsProviderSupported(providerType string) bool {
	switch ProviderType(providerType) {
	case ProviderOpenAI, ProviderAnthropic, ProviderGemini, ProviderZhipu, ProviderAzure, ProviderZAI, ProviderMiniMax, ProviderGork:
		return true
	default:
		return false
	}
}

// ProviderInfo contains metadata about a provider for API responses
type ProviderInfo struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	BaseURL     string `json:"base_url"`
	Color       string `json:"color"`
	Description string `json:"description,omitempty"`
}

// providerMetadata contains display metadata for each provider
type providerMetadata struct {
	name        string
	baseURL     string
	color       string
	description string
}

var providerMetadataMap = map[ProviderType]providerMetadata{
	ProviderOpenAI: {
		name:        "OpenAI",
		baseURL:     "https://api.openai.com/v1",
		color:       "green",
		description: "OpenAI API (GPT models)",
	},
	ProviderAnthropic: {
		name:        "Anthropic",
		baseURL:     "https://api.anthropic.com",
		color:       "orange",
		description: "Anthropic API (Claude models)",
	},
	ProviderGemini: {
		name:        "Gemini",
		baseURL:     "https://generativelanguage.googleapis.com",
		color:       "blue",
		description: "Google Gemini API",
	},
	ProviderZhipu: {
		name:        "Zhipu (智谱)",
		baseURL:     "https://open.bigmodel.cn/api/paas/v4",
		color:       "purple",
		description: "Zhipu AI (GLM models)",
	},
	ProviderAzure: {
		name:        "Azure OpenAI",
		baseURL:     "",
		color:       "cyan",
		description: "Microsoft Azure OpenAI",
	},
	ProviderZAI: {
		name:        "ZAI",
		baseURL:     "https://api.z.ai/api/coding/paas/v4",
		color:       "red",
		description: "ZAI Coding Platform",
	},
	ProviderMiniMax: {
		name:        "MiniMax",
		baseURL:     "https://api.minimax.io/v1",
		color:       "yellow",
		description: "MiniMax API",
	},
	ProviderGork: {
		name:        "Grok (xAI)",
		baseURL:     "https://api.x.ai/v1",
		color:       "gray",
		description: "xAI (Grok models)",
	},
}

// GetProviders returns a list of all providers with their metadata
func GetProviders() []ProviderInfo {
	types := GetProviderTypes()
	providers := make([]ProviderInfo, len(types))
	for i, t := range types {
		meta := providerMetadataMap[t]
		providers[i] = ProviderInfo{
			Type:        string(t),
			Name:        meta.name,
			BaseURL:     meta.baseURL,
			Color:       meta.color,
			Description: meta.description,
		}
	}
	return providers
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
		ProviderGork,
	}
}
