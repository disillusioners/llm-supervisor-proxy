package modelscache

import (
	"context"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// strictSource is the package-private contract the models decorator
// consumes: the full public models.ModelsConfigInterface (so the
// decorator can delegate mutators and legacy reads to the inner
// store) PLUS the Phase 1A strict, error-propagating reads and the
// resolver variant. The concrete *database.ModelsManager satisfies
// it; the decorator is the consumer, never the implementer.
//
// Compile-time assertion lives in outage_test.go (alongside the
// strict-matrix outage assertions) to keep this package's import
// graph limited to what the alias in health.go already needs.
type strictSource interface {
	models.ModelsConfigInterface

	// Strict single-row reads — distinguish not-found from infra.
	GetModelStrict(ctx context.Context, modelID string) (*models.ModelConfig, error)
	GetModelByNameStrict(ctx context.Context, modelName string) (*models.ModelConfig, error)
	GetCredentialStrict(ctx context.Context, id string) (*models.CredentialConfig, error)

	// Strict list reads — error-propagating bulk scans.
	GetModelsStrict(ctx context.Context) ([]models.ModelConfig, error)
	GetEnabledModelsStrict(ctx context.Context) ([]models.ModelConfig, error)
	GetCredentialsStrict(ctx context.Context) ([]models.CredentialConfig, error)

	// Resolver variant — zero DB reads on a warm cache.
	ResolveInternalConfigWithAffinityCached(cached *models.ModelConfig, conversationKey string, credLookup func(credentialID string) (*models.CredentialConfig, bool)) (models.ResolvedCredential, bool)
}
