package modelscache

// 2026-10-19 (shadowfollowup Y1) — GetModelNative is CACHE-BACKED and
// CLAIM-BLIND.
//
// Before this change the decorator forwarded every GetModelNative call
// to the inner manager's DB-direct lookup (store.go GetModelNative),
// so the per-attempt EXECUTION reads (race_executor.go:48/114/208/
// 353, race_coordinator.go:1547/1668, handler_anthropic.go:521) fired
// 2-3 raw SQL queries per attempt whenever CACHE_LAYER_ENABLED
// (default ON).
//
// The native read must therefore:
//   - serve a cached row under RLock only (no write lock, no DB),
//   - fill a miss through the CLAIM-BLIND path (fillModelByDirectID
//     → inner GetModelStrict), never through the claims redirect
//     (GetModel is claim-first by design),
//   - leave the shadow claim intact (a native fill must not clobber
//     the claim),
//   - not thrash on a genuine not-found (the standard negative
//     verdict applies).
//
// The tests drive the REAL DB-backed manager through a counting
// decorator (runtime manager writes — seed+boot alone is a FALSE
// GREEN for group-shadow behavior, per the project KB).

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// countingModelsStore wraps the real DB-backed *ModelsManager and
// counts the strict single-row reads the decorator issues (the only
// read path the native fill uses). List reads and writes are NOT
// counted (the mutators go through the embedded manager).
type countingModelsStore struct {
	*database.ModelsManager

	mu    sync.Mutex
	reads int
}

// GetModelStrict counts the call before delegating.
func (s *countingModelsStore) GetModelStrict(ctx context.Context, modelID string) (*models.ModelConfig, error) {
	s.mu.Lock()
	s.reads++
	s.mu.Unlock()
	return s.ModelsManager.GetModelStrict(ctx, modelID)
}

// GetModelNative counts the call before delegating. This is the
// pre-Y1 UNCACHED forward path (the decorator used to forward every
// native read straight to the manager's DB-direct lookup) — a warm
// read must never reach it.
func (s *countingModelsStore) GetModelNative(modelID string) *models.ModelConfig {
	s.mu.Lock()
	s.reads++
	s.mu.Unlock()
	return s.ModelsManager.GetModelNative(modelID)
}

// readCount returns the number of strict single-row reads so far.
func (s *countingModelsStore) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

// newCountingCacheManager builds the real DB-backed *ModelsManager
// (wrapped in the counting decorator) + a CachedModelsConfig over it.
// Same fixture shape as newCacheManager (models_group_cache_test.go):
// two seeded credentials, no business background sync.
func newCountingCacheManager(t *testing.T) (*countingModelsStore, *CachedModelsConfig, func()) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("DATABASE_URL", "sqlite:"+filepath.Join(tmp, "test.db"))
	ctx := context.Background()
	store, err := database.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	bus := events.NewBus()
	_, mgr, err := database.InitializeManagers(store, bus)
	if err != nil {
		store.Close()
		t.Fatalf("InitializeManagers: %v", err)
	}
	for _, c := range []models.CredentialConfig{
		{ID: "cred-openai", Provider: "openai", APIKey: "k"},
		{ID: "cred-zhipu", Provider: "zhipu", APIKey: "k"},
	} {
		if err := mgr.AddCredential(c); err != nil {
			store.Close()
			t.Fatalf("seed credential %s: %v", c.ID, err)
		}
	}
	counting := &countingModelsStore{ModelsManager: mgr}
	primed, err := NewCachedModelsConfig(counting, Options{
		ModelsNegTTL: 60 * time.Second,
	})
	if err != nil {
		store.Close()
		t.Fatalf("NewCachedModelsConfig: %v", err)
	}
	return counting, primed, func() {
		primed.Stop()
		mgr.Close()
		store.Close()
	}
}

// evictCachedModel drops a row from the decorator's maps — the same
// effect the §3.4 claim-eviction hook has (evictClaimEntriesLocked).
func evictCachedModel(c *CachedModelsConfig, modelID string) {
	c.mu.Lock()
	delete(c.modelsByID, modelID)
	delete(c.negByID, modelID)
	c.mu.Unlock()
}

// TestCache_GetModelNative_ServedFromCache_NoStoreRead — T1: the
// cached-path hit proof. After a warm-up, GetModelNative must be
// served WITHOUT calling through to the wrapped store (store-call
// counting decorator).
func TestCache_GetModelNative_ServedFromCache_NoStoreRead(t *testing.T) {
	store, cache, cleanup := newCountingCacheManager(t)
	defer cleanup()

	// Runtime write THROUGH the decorator (the row must be live in
	// the store, not just seeded at boot).
	if err := cache.AddModel(models.ModelConfig{
		ID: "coding", Name: "Coding", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "glm-5", Credentials: models.TestRefs("cred-openai"),
	}); err != nil {
		t.Fatalf("seed coding: %v", err)
	}

	// Warm-up read: the row is cached → ZERO store reads.
	before := store.readCount()
	got := cache.GetModelNative("coding")
	if got == nil {
		t.Fatal("GetModelNative(coding) = nil — the cached row must be served")
	}
	if got.ID != "coding" {
		t.Fatalf("GetModelNative(coding) = %q, want %q", got.ID, "coding")
	}
	if after := store.readCount(); after != before {
		t.Fatalf("the warm-up native read must be served from the cache: store reads %d → %d", before, after)
	}

	// Steady state: repeated native reads stay OFF the store.
	for i := 0; i < 5; i++ {
		if got := cache.GetModelNative("coding"); got == nil || got.ID != "coding" {
			t.Fatalf("native read #%d = %+v, want the coding row", i, got)
		}
	}
	if after := store.readCount(); after != before {
		t.Fatalf("cached native reads must NOT touch the store: store reads %d → %d", before, after)
	}
}

// TestCache_GetModelNative_MissFillsThenHitsCache — T1 (fill leg):
// a cache miss fills through the claim-blind path with EXACTLY ONE
// store read, and every later read is a pure cache hit.
func TestCache_GetModelNative_MissFillsThenHitsCache(t *testing.T) {
	store, cache, cleanup := newCountingCacheManager(t)
	defer cleanup()

	if err := cache.AddModel(models.ModelConfig{
		ID: "coding", Name: "Coding", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "glm-5", Credentials: models.TestRefs("cred-openai"),
	}); err != nil {
		t.Fatalf("seed coding: %v", err)
	}
	// Drop the cached row → the next native read is a MISS.
	evictCachedModel(cache, "coding")

	before := store.readCount()
	got := cache.GetModelNative("coding")
	if got == nil || got.ID != "coding" {
		t.Fatalf("GetModelNative(coding) = %+v, want the coding row (the miss must fill)", got)
	}
	if after := store.readCount(); after != before+1 {
		t.Fatalf("the miss-fill must issue EXACTLY ONE store read: %d → %d", before, after)
	}
	// The fill INSTALLED the row → the next read is a pure hit.
	if after := store.readCount(); after != before+1 {
		t.Fatalf("the fill must install the row in the cache")
	}
	if got := cache.GetModelNative("coding"); got == nil || got.ID != "coding" {
		t.Fatalf("post-fill native read = %+v, want the coding row", got)
	}
	if after := store.readCount(); after != before+1 {
		t.Fatalf("the post-fill read must NOT touch the store: %d → %d", before+1, after)
	}
}

// TestCache_GetModelNative_ClaimBlindFillAfterClaimEviction — T2:
// the claim-blind miss-fill after the §3.4 claim eviction, and the
// claim's survival across the fill.
func TestCache_GetModelNative_ClaimBlindFillAfterClaimEviction(t *testing.T) {
	store, cache, cleanup := newCountingCacheManager(t)
	defer cleanup()

	// The member row (its ID is the group's shadow id) + the group's
	// second member.
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
	// The group claims "coding" — the claim-eviction hook drops the
	// member's cached row and the claim goes live.
	if err := cache.AddModel(validGroupRow()); err != nil {
		t.Fatalf("AddModel group: %v", err)
	}

	cache.mu.RLock()
	_, memberCached := cache.modelsByID["coding"]
	_, groupCached := cache.modelsByID["coding-group"]
	claimOwner, claimed := cache.claimsByID["coding"]
	cache.mu.RUnlock()
	if memberCached {
		t.Fatal("setup: the claim-eviction hook must have dropped the member's cached row")
	}
	if !groupCached {
		t.Fatal("setup: the group row must stay cached")
	}
	if !claimed || claimOwner != "coding-group" {
		t.Fatalf("setup: the shadow claim must be live (claims[coding] = %q, claimed = %v)", claimOwner, claimed)
	}

	// The CLAIM-BLIND native read must return the MEMBER row (not the
	// group row the claim would redirect to) and must fill it through
	// the claim-blind path.
	before := store.readCount()
	got := cache.GetModelNative("coding")
	if got == nil {
		t.Fatal("GetModelNative(coding) = nil — the member row must resolve")
	}
	if got.ID != "coding" {
		t.Fatalf("GetModelNative(coding) = %q — the native read must stay CLAIM-BLIND (no group redirect)", got.ID)
	}
	if got.IsGroup() {
		t.Fatal("GetModelNative(coding) returned the GROUP row — the native read must stay claim-blind")
	}
	if got.InternalModel != "glm-5" {
		t.Errorf("native row internal model = %q, want %q (the MEMBER row's internal model)", got.InternalModel, "glm-5")
	}
	if got.PrimaryCredentialID() != "cred-openai" {
		t.Errorf("native row credential = %q, want %q", got.PrimaryCredentialID(), "cred-openai")
	}
	if after := store.readCount(); after != before+1 {
		t.Fatalf("the claim-blind fill must issue EXACTLY ONE store read: %d → %d", before, after)
	}

	// The next native read is a pure cache hit (the fill installed
	// the row).
	if got := cache.GetModelNative("coding"); got == nil || got.ID != "coding" {
		t.Fatalf("post-fill native read = %+v, want the coding row", got)
	}
	if after := store.readCount(); after != before+1 {
		t.Fatalf("the post-fill native read must NOT touch the store: %d → %d", before+1, after)
	}

	// (a) The shadow claim SURVIVED the native fill: the client-facing
	// read still redirects to the group row.
	if g := cache.GetModel("coding"); g == nil || g.ID != "coding-group" {
		t.Fatalf("the shadow claim must survive the native fill: GetModel(coding) = %+v, want the group row", g)
	}
	// The group row was not clobbered by the member fill.
	cache.mu.RLock()
	group, groupPresent := cache.modelsByID["coding-group"]
	owner, stillClaimed := cache.claimsByID["coding"]
	cache.mu.RUnlock()
	if !groupPresent || group == nil || group.ID != "coding-group" {
		t.Fatalf("the native fill must not clobber the group row: %+v", group)
	}
	if !stillClaimed || owner != "coding-group" {
		t.Fatalf("the native fill must not clobber the claim index: claims[coding] = %q (claimed = %v)", owner, stillClaimed)
	}
}

// TestCache_GetModelNative_NotFound_NoThrash — the not-found
// convention: ONE store read, then the standard negative verdict
// short-circuits the fill path (no fill/miss loop).
func TestCache_GetModelNative_NotFound_NoThrash(t *testing.T) {
	store, cache, cleanup := newCountingCacheManager(t)
	defer cleanup()

	before := store.readCount()
	if got := cache.GetModelNative("ghost-model"); got != nil {
		t.Fatalf("GetModelNative(ghost-model) = %+v, want nil", got)
	}
	if after := store.readCount(); after != before+1 {
		t.Fatalf("the not-found fill must issue EXACTLY ONE store read: %d → %d", before, after)
	}
	// The negative verdict is cached → no further store reads.
	for i := 0; i < 5; i++ {
		if got := cache.GetModelNative("ghost-model"); got != nil {
			t.Fatalf("native read #%d = %+v, want nil", i, got)
		}
	}
	if after := store.readCount(); after != before+1 {
		t.Fatalf("a not-found ID must NOT re-read the store (negative verdict): %d → %d", before+1, after)
	}
	// The negative verdict is the cache's own convention.
	cache.mu.RLock()
	_, hasNeg := cache.negByID["ghost-model"]
	cache.mu.RUnlock()
	if !hasNeg {
		t.Error("the not-found native read must store the standard negative verdict")
	}
}

// TestCache_GetModelNative_ServesMemberRow_NotGroupRow — the
// claim-blind property in isolation: for a claimed ID the client-
// facing read returns the GROUP row while the native read returns
// the MEMBER row — from the same decorator, same claim.
func TestCache_GetModelNative_ServesMemberRow_NotGroupRow(t *testing.T) {
	_, cache, cleanup := newCountingCacheManager(t)
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

	clientFacing := cache.GetModel("coding")
	if clientFacing == nil || clientFacing.ID != "coding-group" {
		t.Fatalf("client-facing GetModel(coding) = %+v, want the group row", clientFacing)
	}
	native := cache.GetModelNative("coding")
	if native == nil || native.ID != "coding" {
		t.Fatalf("native GetModelNative(coding) = %+v, want the member row (claim-blind)", native)
	}
	if native.IsGroup() {
		t.Fatal("the native read must never return the claims-redirected group row")
	}
}
