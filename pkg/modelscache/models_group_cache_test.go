package modelscache

// load-balance-groups / P2.2 (plan §3.4 modelscache + §9.3):
//
//   - Claims-aware GetModel: an ID claimed by an ENABLED group
//     resolves to the GROUP row through the decorator (caching and
//     strict-fill both respect the claim).
//   - Claim-eviction hook: a group write (Add/Update/Remove +
//     shadow change) drops the positive AND negative cache entries
//     for the OLD and NEW shadow IDs in the same locked section
//     (synchronous, before any subsequent resolve can read a stale
//     entry).
//   - Disabled group ⇒ inactive claim (the shadowed model reappears
//     and resolves normally).
//
// The decorator wraps the real *database.ModelsManager; the claim
// behavior is a faithful shadow of the manager's claims map, kept
// coherent by rebuilds on every snapshot/mutation (no per-row
// branching in the read paths).

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// newCacheManager builds a real DB-backed *ModelsManager + a
// decorator over it (no business background sync — the decorator
// is manually fed via AddModel/UpdateModel in the test).
func newCacheManager(t *testing.T) (*database.ModelsManager, *CachedModelsConfig, func()) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("DATABASE_URL", "sqlite:"+filepath.Join(tmp, "test.db"))
	ctx := context.Background()
	store, err := database.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	// nil event bus: see newCountingCacheManager
	// (models_native_cache_test.go) — the manager's bus-drain
	// goroutine (drainCredentialsChanged, store.go:770) is
	// production-owned and unjoinable from this package, yet it reads
	// the temp-dir SQLite db after mgr.Close() returns, racing
	// t.TempDir()'s RemoveAll (the ~1/16-batch unlinkat flake). Nil
	// bus = the goroutine is never created.
	_, mgr, err := database.InitializeManagers(store, nil)
	if err != nil {
		store.Close()
		t.Fatalf("InitializeManagers: %v", err)
	}
	// Seed credentials referenced by the test fixtures.
	for _, c := range []models.CredentialConfig{
		{ID: "cred-openai", Provider: "openai", APIKey: "k"},
		{ID: "cred-zhipu", Provider: "zhipu", APIKey: "k"},
	} {
		if err := mgr.AddCredential(c); err != nil {
			store.Close()
			t.Fatalf("seed credential %s: %v", c.ID, err)
		}
	}
	primed, err := NewCachedModelsConfig(mgr, Options{
		ModelsNegTTL: 60 * time.Second, // long neg-TTL so the eviction assertion is observable
	})
	if err != nil {
		store.Close()
		t.Fatalf("NewCachedModelsConfig: %v", err)
	}
	return mgr, primed, func() {
		primed.Stop()
		// Full join of the decorator's reconciler goroutine — Stop()'s
		// wait is bounded and may abandon it mid-scan (same rationale
		// as newCountingCacheManager).
		primed.reconcileWG.Wait()
		mgr.Close()
		store.Close()
		_ = os.RemoveAll(tmp)
	}
}

// validGroupRow matches the canonical day-1 shape used in
// pkg/store/database/store_group_test.go.
func validGroupRow() models.ModelConfig {
	return models.ModelConfig{
		ID: "coding-group", Name: "Coding Group", Enabled: true,
		Kind:                         models.KindGroup,
		Internal:                     true,
		ExcludeFromUltimateSwitching: true,
		Members: []models.GroupMember{
			{ModelID: "coding", Weight: 2},
			{ModelID: "coding-alt", Weight: 1},
		},
		ShadowModelID: "coding",
	}
}

func TestCache_ClaimAware_GetModel_RedirectsToGroup(t *testing.T) {
	_, cache, cleanup := newCacheManager(t)
	defer cleanup()
	for _, m := range []models.ModelConfig{
		{ID: "coding", Name: "Coding", Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: "glm-5", Credentials: models.TestRefs("cred-openai")},
		{ID: "coding-alt", Name: "Coding Alt", Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: "kimi", Credentials: models.TestRefs("cred-zhipu")},
	} {
		// Mutators go through the DECORATOR so the decorator's
		// claimsByID / eviction paths are exercised (the
		// cache-eviction contract is the decorator-side seam).
		if err := cache.AddModel(m); err != nil {
			t.Fatalf("seed %s: %v", m.ID, err)
		}
	}
	if err := cache.AddModel(validGroupRow()); err != nil {
		t.Fatalf("AddModel group: %v", err)
	}
	// Warm the cache via the decorator (a read populates the snapshot).
	_ = cache.GetModel("coding")

	// Claimed ID → group row through the decorator.
	got := cache.GetModel("coding")
	if got == nil {
		t.Fatal("GetModel(coding) = nil")
	}
	if got.ID != "coding-group" || !got.IsGroup() {
		t.Fatalf("decorator claim-aware GetModel: got ID=%q kind=%q, want coding-group/group", got.ID, got.Kind)
	}
}

func TestCache_ClaimEviction_OnShadowChange(t *testing.T) {
	_, cache, cleanup := newCacheManager(t)
	defer cleanup()
	for _, m := range []models.ModelConfig{
		{ID: "coding", Name: "Coding", Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: "glm-5", Credentials: models.TestRefs("cred-openai")},
		{ID: "coding-alt", Name: "Coding Alt", Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: "kimi", Credentials: models.TestRefs("cred-zhipu")},
	} {
		if err := cache.AddModel(m); err != nil {
			t.Fatalf("seed %s: %v", m.ID, err)
		}
	}

	// Prime the cache + create a negative entry for "nonexistent".
	_ = cache.GetModel("coding")
	_ = cache.GetModel("nonexistent")
	if got := cache.GetModel("nonexistent"); got != nil {
		t.Fatal("expected nil for nonexistent (negative cache primed)")
	}
	// Confirm modelsByID[coding] and negByID[nonexistent] are present.
	cache.mu.RLock()
	_, hasChat := cache.modelsByID["coding"]
	_, hasNeg := cache.negByID["nonexistent"]
	cache.mu.RUnlock()
	if !hasChat || !hasNeg {
		t.Fatalf("priming failed: hasChat=%v hasNeg=%v", hasChat, hasNeg)
	}

	// Add the group with shadow=coding-alt (NOT coding). The
	// eviction hook must drop the cached "coding" row and the
	// "nonexistent" negative IF shadowed_id matches — here it
	// doesn't, so the existing entries stay.
	if err := cache.AddModel(validGroupRow()); err != nil {
		t.Fatalf("AddModel group: %v", err)
	}

	// Now UPDATE the group's shadow to "coding": both the old
	// shadow ("coding-alt" — uncached, harmless) and the new shadow
	// ("coding" — currently cached as the chat row) must be evicted.
	updated := validGroupRow()
	updated.ShadowModelID = "coding"
	if err := cache.UpdateModel("coding-group", updated); err != nil {
		t.Fatalf("UpdateModel: %v", err)
	}

	// modelsByID[coding] must be GONE (so the next read goes through
	// claims-first and serves the group row).
	cache.mu.RLock()
	_, hasChat = cache.modelsByID["coding"]
	cache.mu.RUnlock()
	if hasChat {
		t.Error("shadow-change UpdateModel must evict the cached chat row for the NEW shadow")
	}
	// GetModel(coding) → claims-first redirect → group row.
	if got := cache.GetModel("coding"); got == nil || got.ID != "coding-group" {
		t.Errorf("post-claim GetModel(coding) = %+v, want the group row", got)
	}
}

func TestCache_ClaimEviction_OnGroupRemove(t *testing.T) {
	_, cache, cleanup := newCacheManager(t)
	defer cleanup()
	for _, m := range []models.ModelConfig{
		{ID: "coding", Name: "Coding", Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: "glm-5", Credentials: models.TestRefs("cred-openai")},
		{ID: "coding-alt", Name: "Coding Alt", Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: "kimi", Credentials: models.TestRefs("cred-zhipu")},
	} {
		if err := cache.AddModel(m); err != nil {
			t.Fatalf("seed %s: %v", m.ID, err)
		}
	}
	if err := cache.AddModel(validGroupRow()); err != nil {
		t.Fatalf("AddModel group: %v", err)
	}
	// Claimed: the cache would serve the group row for GetModel("coding").
	if got := cache.GetModel("coding"); got == nil || !got.IsGroup() {
		t.Fatalf("setup: GetModel(coding) = %+v, want the group row", got)
	}

	// Remove the group: shadow "coding" must be evicted (no stale
	// redirect); the chat row must reappear.
	if err := cache.RemoveModel("coding-group"); err != nil {
		t.Fatalf("RemoveModel(group): %v", err)
	}
	if got := cache.GetModel("coding"); got == nil || got.IsGroup() {
		t.Errorf("post-removal GetModel(coding) = %+v, want the chat row", got)
	}
}

func TestCache_DisabledGroupClaimInactive(t *testing.T) {
	// Disabling a group drops its claim: the shadowed model
	// reappears and resolves normally (rebuildClaimsLocked respects
	// the enabled flag).
	_, cache, cleanup := newCacheManager(t)
	defer cleanup()
	for _, m := range []models.ModelConfig{
		{ID: "coding", Name: "Coding", Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: "glm-5", Credentials: models.TestRefs("cred-openai")},
		{ID: "coding-alt", Name: "Coding Alt", Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: "kimi", Credentials: models.TestRefs("cred-zhipu")},
	} {
		if err := cache.AddModel(m); err != nil {
			t.Fatalf("seed %s: %v", m.ID, err)
		}
	}
	if err := cache.AddModel(validGroupRow()); err != nil {
		t.Fatalf("AddModel group: %v", err)
	}
	if got := cache.GetModel("coding"); got == nil || !got.IsGroup() {
		t.Fatalf("setup: claim active, got %+v", got)
	}
	disabled := validGroupRow()
	disabled.Enabled = false
	if err := cache.UpdateModel("coding-group", disabled); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got := cache.GetModel("coding"); got == nil || got.IsGroup() {
		t.Errorf("disabled group's claim must be inactive: got %+v, want the chat row", got)
	}
}
