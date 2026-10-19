package proxy

// remove-external-upstream P5 — the canonical internal test fixture.
//
// Pre-P1 the proxy tests pointed at their mock upstream through the
// now-deleted UPSTREAM_URL config path (a global external forward).
// Post-P1 every request must resolve to an INTERNAL model whose
// credential base_url IS the mock upstream URL — otherwise the boundary
// 404 gate rejects it. This helper is the single place that knows the
// fixture IDs, so behavioral tests can assert against stable names.
//
// seedInternalFixtureNamed registers the same credential + an internal
// model under a bespoke ID (the Anthropic helper drives tests that
// request a specific model ID); the quiet variant backs ConfigSnapshot
// builders that don't thread a *testing.T.

import (
	"fmt"

	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

const (
	// p5FixtureCredID is the credential the fixture registers; its
	// base_url is the httptest mock upstream.
	p5FixtureCredID = "test-cred"
	// p5FixtureModelID is the canonical internal model ID the fixture
	// registers (chat tests request this ID).
	p5FixtureModelID = "test-model"
)

// seedInternalFixtureErr registers the fixture pair on mc and returns
// any seeding error. No-op (nil error) when mc is nil. The credential's
// base_url is UPSERTED to upstreamURL (a test may have declared the
// fixture credential earlier with a placeholder URL so its model rows
// pass write-time validation).
func seedInternalFixtureErr(mc models.ModelsConfigInterface, upstreamURL, modelID string) error {
	if mc == nil {
		return nil
	}
	cred := models.CredentialConfig{
		ID:       p5FixtureCredID,
		Provider: "openai",
		APIKey:   "test-key",
		BaseURL:  upstreamURL,
	}
	if existing := mc.GetCredential(p5FixtureCredID); existing == nil {
		if err := mc.AddCredential(cred); err != nil {
			return fmt.Errorf("seedInternalFixture: AddCredential: %w", err)
		}
	} else if existing.BaseURL != upstreamURL {
		if err := mc.UpdateCredential(p5FixtureCredID, cred); err != nil {
			return fmt.Errorf("seedInternalFixture: UpdateCredential: %w", err)
		}
	}
	if mc.GetModel(modelID) == nil {
		if err := mc.AddModel(models.ModelConfig{
			ID:            modelID,
			Name:          modelID,
			Enabled:       true,
			Internal:      true,
			InternalModel: "test-upstream-model",
			Credentials:   models.TestRefs(p5FixtureCredID),
		}); err != nil {
			return fmt.Errorf("seedInternalFixture: AddModel(%s): %w", modelID, err)
		}
	}
	return nil
}

// seedInternalTestModel adds an internal chat model row referencing the
// fixture credential — for testCase literals that declare their own
// model table (fallback chains). Post-P1 the write-stamp forces
// Internal=true, so a credential-less row fails AddModel validation;
// this helper declares the fixture credential (placeholder base_url —
// newTestHandler re-points it at the httptest upstream) then the row.
func seedInternalTestModel(mc models.ModelsConfigInterface, id, internalModel string, fallback ...string) {
	if mc == nil {
		return
	}
	if mc.GetCredential(p5FixtureCredID) == nil {
		_ = mc.AddCredential(models.CredentialConfig{
			ID: p5FixtureCredID, Provider: "openai", APIKey: "test-key",
		})
	}
	if mc.GetModel(id) != nil {
		return
	}
	_ = mc.AddModel(models.ModelConfig{
		ID:            id,
		Name:          id,
		Enabled:       true,
		Internal:      true,
		InternalModel: internalModel,
		FallbackChain: fallback,
		Credentials:   models.TestRefs(p5FixtureCredID),
	})
}

// seedInternalFixture registers the canonical internal fixture pair
// (credential test-cred + internal model test-model) on mc, routing
// requests through upstreamURL. No-op when mc is nil.
func seedInternalFixture(t *testing.T, mc models.ModelsConfigInterface, upstreamURL string) {
	t.Helper()
	seedInternalFixtureNamed(t, mc, upstreamURL, p5FixtureModelID)
}

// seedInternalFixtureNamed is seedInternalFixture with an explicit model
// ID — for helpers whose tests request a model name other than the
// canonical test-model.
func seedInternalFixtureNamed(t *testing.T, mc models.ModelsConfigInterface, upstreamURL, modelID string) {
	t.Helper()
	if err := seedInternalFixtureErr(mc, upstreamURL, modelID); err != nil {
		t.Fatalf("%v", err)
	}
}

// seedInternalModels registers an internal model row for each id (all
// sharing the fixture credential pointed at upstreamURL).
func seedInternalModels(mc models.ModelsConfigInterface, upstreamURL string, ids ...string) {
	for _, id := range ids {
		_ = seedInternalFixtureErr(mc, upstreamURL, id)
	}
}

// seededModelsConfig builds a ModelsConfig carrying the canonical fixture
// pointed at upstreamURL — for callers that build a ConfigSnapshot
// directly (race / tool-repair tests) instead of going through
// newTestHandler. The low-level race tests hand-build multi-model lists
// ("test-model" + a fallback), so the common fallback IDs are seeded too.
func seededModelsConfig(t *testing.T, upstreamURL string) models.ModelsConfigInterface {
	t.Helper()
	mc := models.NewModelsConfig()
	seedInternalModels(mc, upstreamURL, p5FixtureModelID, "fallback-model", "test-model-fallback")
	return mc
}
