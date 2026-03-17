package normalizers

import (
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

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
