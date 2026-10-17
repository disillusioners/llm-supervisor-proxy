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
	ProviderGrok      ProviderType = "grok"
	ProviderDeepSeek  ProviderType = "deepseek"
	// ProviderOpencode is the identity for OpenCode Zen
	// (https://opencode.ai/zen) — OpenAI-compatible chat-completions
	// provider. No migration / DB schema change required; the provider
	// layer routes OpenAIProvider at the locked default base URL
	// https://opencode.ai/inference/openai/v1. SCOPE v1: chat only.
	ProviderOpencode ProviderType = "opencode"
	// ProviderTypesafe is the identity for TypeSafe (Jev) decision
	// models served at /v1/decisions (Decision Models commission
	// / §5.1 — recommended identity B; the chat provider layer does
	// not speak System One; the decision handler builds its own
	// upstream HTTP client mirroring how imggen handles minimax).
	ProviderTypesafe ProviderType = "typesafe"
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
	case ProviderGrok:
		// Grok (X.ai) uses OpenAI-compatible API
		if baseURL == "" {
			baseURL = "https://api.x.ai/v1"
		}
		return NewOpenAIProvider(apiKey, baseURL), nil
	case ProviderDeepSeek:
		// DeepSeek uses OpenAI-compatible API
		if baseURL == "" {
			baseURL = "https://api.deepseek.com"
		}
		return NewOpenAIProvider(apiKey, baseURL), nil
	case ProviderOpencode:
		// OpenCode Zen uses OpenAI-compatible API. Default base URL
		// is the v2 Console canonical path consumed by the vendor's
		// own agent (https://opencode.ai/inference/openai/v1); the
		// stable-docs alternative https://opencode.ai/zen/v1 is
		// equally live with the same catalog — users can override
		// via credential.base_url.
		if baseURL == "" {
			baseURL = "https://opencode.ai/inference/openai/v1"
		}
		return NewOpenAIProvider(apiKey, baseURL), nil
	case ProviderTypesafe:
		// Decision Models / §5.5 — the chat provider layer does
		// NOT speak System One (TypeSafe / Jev's wire shape).
		// The decision handler builds its own upstream HTTP
		// client in pkg/decision/typesafe.go (mirrors imggen's
		// minimax posture). Adding a chat-shaped entry here would
		// imply System One is chat-compatible — it isn't.
		return nil, fmt.Errorf("typesafe provider not yet implemented")
	default:
		return nil, fmt.Errorf("unknown provider: %s", providerType)
	}
}

// IsProviderSupported checks if a provider type is supported
func IsProviderSupported(providerType string) bool {
	switch ProviderType(providerType) {
	case ProviderOpenAI, ProviderAnthropic, ProviderGemini, ProviderZhipu, ProviderAzure, ProviderZAI, ProviderMiniMax, ProviderGrok, ProviderDeepSeek, ProviderOpencode, ProviderTypesafe:
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
	ProviderGrok: {
		name:        "Grok (xAI)",
		baseURL:     "https://api.x.ai/v1",
		color:       "gray",
		description: "xAI (Grok models)",
	},
	ProviderDeepSeek: {
		name:        "DeepSeek",
		baseURL:     "https://api.deepseek.com",
		color:       "indigo",
		description: "DeepSeek AI",
	},
	ProviderOpencode: {
		// OpenCode Zen — OpenAI-compatible chat-completions provider
		// (https://opencode.ai/zen). Base URL is the v2 Console
		// canonical path; the stable-docs alternative
		// https://opencode.ai/zen/v1 is equally live with the same
		// catalog — override via credential.base_url if needed.
		name:        "OpenCode Zen",
		baseURL:     "https://opencode.ai/inference/openai/v1",
		color:       "emerald",
		description: "OpenCode Zen (OpenAI-compatible chat-completions; v2 Console path; override base_url to https://opencode.ai/zen/v1 for stable-docs route)",
	},
	ProviderTypesafe: {
		// Decision Models / §5.1 — the "TypeSafe (Jev)" identity
		// shows up verbatim in the FE /fe/api/providers dropdown.
		// The proxy serves it for /v1/decisions only (the chat
		// layer returns "not yet implemented" above).
		name:        "TypeSafe (Jev)",
		baseURL:     "https://api.typesafe.ai",
		color:       "violet",
		description: "TypeSafe (Jev) decision API",
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
		ProviderGrok,
		ProviderDeepSeek,
		ProviderOpencode,
		// Decision Models / §5.2 — typesafe is auto-included via
		// providerMetadataMap; the inclusion here makes it
		// appear in the GET /fe/api/providers FE dropdown (used
		// by the DecisionModelForm provider filter — P3, not P2).
		ProviderTypesafe,
	}
}
