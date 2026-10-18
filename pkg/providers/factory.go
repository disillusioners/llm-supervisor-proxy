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
	// ProviderLightLLM is the identity for a self-hosted LightLLM
	// gateway (LiteLLM proxy) — a REGULAR OpenAI-compatible chat
	// provider (remove-external-upstream phase 2, 2026-10-18). No
	// migration / DB schema change required; the provider layer
	// routes OpenAIProvider at the inherited default base URL
	// http://localhost:4001 (the retired external-upstream default,
	// the retired global upstream default), overridable via credential.base_url.
	// SCOPE: chat only.
	ProviderLightLLM ProviderType = "lightllm"
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
		//
		// Zen carries provider-native UA attribution (UA-only; no
		// session header) — see opencodego.go and the Zen UA design
		// note. The flavour flag gates the kind-checked attribution
		// seam; the UA is stamped only after the proxy attaches it
		// (AttachOpenCodeGoAttribution), never at construction.
		if baseURL == "" {
			baseURL = "https://opencode.ai/inference/openai/v1"
		}
		p := NewOpenAIProvider(apiKey, baseURL)
		p.openCodeZen = true
		return p, nil
	case ProviderOpencodeGo:
		// OpenCode Go (subscription realm) — OpenAI-compatible API,
		// DISTINCT provider from ProviderOpencode (Zen): different
		// product, different entitlements, different default base
		// URL (the Go realm is selected by URL path alone). Beyond
		// the shared transport, the flavour flag enables the
		// provider-native x-opencode-session + User-Agent
		// attribution (see opencodego.go); the headers are stamped
		// only after the proxy attaches the per-request attribution
		// (AttachOpenCodeGoAttribution), never at construction.
		if baseURL == "" {
			baseURL = OpenCodeGoDefaultBaseURL
		}
		p := NewOpenAIProvider(apiKey, baseURL)
		p.openCodeGo = true
		return p, nil
	case ProviderLightLLM:
		// LightLLM gateway uses the OpenAI-compatible API. Default
		// base URL is inherited from the removed external-upstream
		// default (the retired global upstream default — localhost:4001,
		// LiteLLM's
		// canonical port); override via credential.base_url for
		// remote hosts.
		if baseURL == "" {
			baseURL = "http://localhost:4001"
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
	case ProviderOpenAI, ProviderAnthropic, ProviderGemini, ProviderZhipu, ProviderAzure, ProviderZAI, ProviderMiniMax, ProviderGrok, ProviderDeepSeek, ProviderOpencode, ProviderOpencodeGo, ProviderLightLLM, ProviderTypesafe:
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
	ProviderOpencodeGo: {
		// OpenCode Go — the subscription-billed SIBLING of Zen
		// (https://opencode.ai/go). DISTINCT provider (not a base_url
		// override of opencode): different product/entitlements and
		// its own realm path. Carries provider-native
		// x-opencode-session + User-Agent attribution on upstream
		// requests (pkg/providers/opencodego.go).
		name:        "OpenCode Go",
		baseURL:     OpenCodeGoDefaultBaseURL,
		color:       "teal",
		description: "OpenCode Go (OpenAI-compatible chat-completions; subscription realm; provider-native session + User-Agent attribution)",
	},
	ProviderLightLLM: {
		// LightLLM — self-hosted LiteLLM gateway, a regular
		// OpenAI-compatible chat provider. Default expects a local
		// LiteLLM proxy on :4001 (the retired external-upstream
		// default); override via credential.base_url for remote hosts.
		name:        "LightLLM",
		baseURL:     "http://localhost:4001",
		color:       "sky",
		description: "LightLLM (self-hosted LiteLLM gateway — OpenAI-compatible, chat-only; default expects a local LiteLLM proxy on :4001, override base_url for remote hosts)",
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
		// opencode-go commission — DISTINCT provider (subscription
		// realm); appears in the GET /fe/api/providers FE dropdown.
		ProviderOpencodeGo,
		// lightllm (remove-external-upstream phase 2) — a regular
		// OpenAI-compatible chat provider; appears in the
		// GET /fe/api/providers FE dropdown.
		ProviderLightLLM,
		// Decision Models / §5.2 — typesafe is auto-included via
		// providerMetadataMap; the inclusion here makes it
		// appear in the GET /fe/api/providers FE dropdown (used
		// by the DecisionModelForm provider filter — P3, not P2).
		ProviderTypesafe,
	}
}
