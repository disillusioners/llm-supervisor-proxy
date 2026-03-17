package normalizers

import (
	"os"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// NormalizerConfig holds configuration for the stream normalizer module
type NormalizerConfig struct {
	// Enabled enables/disables the entire normalizer module
	Enabled bool
	// ProviderNormalizers stores per-provider normalizer overrides
	ProviderNormalizers map[string]ProviderNormalizerConfig
}

// ProviderNormalizerConfig holds normalizer configuration for a specific provider
type ProviderNormalizerConfig struct {
	Enabled     bool
	Normalizers []string // which normalizers to enable for this provider
}

// DefaultNormalizerConfig returns the default configuration
func DefaultNormalizerConfig() NormalizerConfig {
	return NormalizerConfig{
		Enabled: true,
		ProviderNormalizers: map[string]ProviderNormalizerConfig{
			// GLM-5 specific: both normalizers enabled by default
			"zhipu": {
				Enabled:     true,
				Normalizers: []string{"fix_empty_role", "fix_tool_call_index"},
			},
		},
	}
}

// LoadNormalizerConfig loads configuration from environment variables
func LoadNormalizerConfig() NormalizerConfig {
	cfg := DefaultNormalizerConfig()

	// Check STREAM_NORMALIZER_ENABLED env var
	if v := os.Getenv("STREAM_NORMALIZER_ENABLED"); v != "" {
		cfg.Enabled = v == "true" || v == "1"
	}

	// Check STREAM_NORMALIZER_GLM5_ENABLED env var
	if v := os.Getenv("STREAM_NORMALIZER_GLM5_ENABLED"); v != "" {
		enabled := v == "true" || v == "1"
		cfg.ProviderNormalizers["zhipu"] = ProviderNormalizerConfig{
			Enabled:     enabled,
			Normalizers: []string{"fix_empty_role", "fix_tool_call_index"},
		}
	}

	return cfg
}

// DetectProvider identifies the upstream provider from model configuration
func DetectProvider(cfg models.ModelsConfigInterface, modelID string) string {
	if cfg == nil {
		return "external"
	}

	model := cfg.GetModel(modelID)
	if model == nil {
		return "external"
	}

	// If the model uses internal upstream, get provider from credentials
	if model.Internal {
		provider, _, _, _, ok := cfg.ResolveInternalConfig(modelID)
		if ok && provider != "" {
			return provider
		}
	}

	// Otherwise it's an external upstream (LiteLLM or similar)
	return "external"
}
