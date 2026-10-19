package normalizers

import (
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// DetectProvider identifies the upstream provider from model configuration.
//
// remove-external-upstream P1 (A19): the historical "external" fallback
// returns are GONE — there is no external upstream anymore. Unknown /
// unresolvable models normalize under the "unknown" provider label
// (normalization context only; verified zero production consumers of
// the old "external" literal — its only callers were the internal arm,
// which survives, and the deleted external response loop).
func DetectProvider(cfg models.ModelsConfigInterface, modelID string) string {
	if cfg == nil {
		return "unknown"
	}

	model := cfg.GetModel(modelID)
	if model == nil {
		return "unknown"
	}

	// Every configured model is internal (remove-external-upstream P1):
	// resolve the provider from credentials.
	provider, _, _, _, ok := cfg.ResolveInternalConfig(modelID)
	if ok && provider != "" {
		return provider
	}

	// Unresolvable (no credentials / dangling refs) — normalization
	// context only, never a routing decision.
	return "unknown"
}
