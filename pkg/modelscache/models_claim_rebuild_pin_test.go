package modelscache

// 2026-10-19 (shadowfollowup A2) — PIN the CONDITIONAL claim-index
// rebuild in cacheModel.
//
// models.go cacheModel installs a filled row into the decorator's maps
// and rebuilds the shadow-claim index ONLY when the installed row is
// a GROUP:
//
//	if cp.IsGroup() { c.rebuildClaimsLocked() }
//
// WHY the rebuild is load-bearing on the FILL path: the claim index
// (claimsByID) is DERIVED from the cached rows — a group row that
// ENTERS the cache activates its shadow claim, so the client-facing
// GetModel must redirect the shadowed member ID to the group row.
// The write-through mutators (AddModel/UpdateModel/RemoveModel) always
// rebuild, so a group installed by a MUTATOR can never leave a stale
// index behind. The FILL path (GetModelNative miss →
// fillModelByDirectID → cacheModel) has no such guarantee: without the
// conditional rebuild the newly installed group row would sit in the
// cache UNCLAIMED and the client-facing read of the shadowed member ID
// would resolve to the member row instead — a silent LB / sticky /
// credential-failover semantic divergence on exactly the shape the P0
// row-identity fix (8a7d071) made production-real (a group whose
// shadow_model_id names one of its OWN members).
//
// The test drives the REAL DB-backed manager: the group row is
// written to the STORE at runtime (a manager write the decorator has
// not observed yet — seed+boot alone is a FALSE GREEN for group-shadow
// behavior, per the project KB), so the ONLY way that row can enter
// the cache is the native fill path.
//
// MUTATION-SENSITIVE: deleting the `if cp.IsGroup() {
// c.rebuildClaimsLocked() }` block from cacheModel makes this test
// FAIL — the client-facing read returns the member row, not the group
// row.

import (
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestCache_CacheModel_GroupFill_RebuildsClaimIndex — A2: a group row
// that enters the cache through the NATIVE FILL PATH must activate its
// shadow claim, so the client-facing read of the shadowed member ID
// still resolves to the GROUP row.
func TestCache_CacheModel_GroupFill_RebuildsClaimIndex(t *testing.T) {
	store, cache, cleanup := newCountingCacheManager(t)
	defer cleanup()

	// The two member rows — written THROUGH the decorator (the
	// write-through path; the member rows are cached).
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

	// The GROUP row — written to the STORE only (a runtime manager
	// write the decorator has not observed). validGroupRow() is the
	// canonical day-1 self-shadow shape: shadow_model_id ("coding")
	// names one of the group's OWN members.
	group := validGroupRow()
	if err := store.AddModel(group); err != nil {
		t.Fatalf("seed the group row in the store: %v", err)
	}

	// SETUP INVARIANTS: the decorator knows NEITHER the group row nor
	// its claim — the group row can only enter the cache through the
	// native fill path.
	cache.mu.RLock()
	_, groupCached := cache.modelsByID[group.ID]
	claimOwner, claimed := cache.claimsByID[group.ShadowModelID]
	cache.mu.RUnlock()
	if groupCached {
		t.Fatalf("setup: the group row must NOT be cached before the fill (a cached group row would bypass the fill path)")
	}
	if claimed {
		t.Fatalf("setup: no claim may be live before the fill (claims[%s] = %q)", group.ShadowModelID, claimOwner)
	}

	// THE NATIVE FILL PATH: GetModelNative miss → fillModelByDirectID
	// → cacheModel (the fill installs the row).
	before := store.readCount()
	got := cache.GetModelNative(group.ID)
	if got == nil {
		t.Fatalf("GetModelNative(%s) = nil — the miss must fill from the store", group.ID)
	}
	if got.ID != group.ID || !got.IsGroup() {
		t.Fatalf("GetModelNative(%s) = %+v (ID=%q kind=%q), want the group row", group.ID, got, got.ID, got.Kind)
	}
	if after := store.readCount(); after != before+1 {
		t.Fatalf("the native fill must issue EXACTLY ONE store read: %d → %d", before, after)
	}
	cache.mu.RLock()
	_, groupCached = cache.modelsByID[group.ID]
	cache.mu.RUnlock()
	if !groupCached {
		t.Fatalf("the native fill must install the group row in the cache")
	}

	// THE PIN: the client-facing read of the shadowed member ID must
	// STILL resolve to the GROUP row — the claim the fill activated.
	clientFacing := cache.GetModel(group.ShadowModelID)
	if clientFacing == nil {
		t.Fatalf("GetModel(%s) = nil — the claim must redirect to the group row", group.ShadowModelID)
	}
	if clientFacing.ID != group.ID || !clientFacing.IsGroup() {
		t.Fatalf("the conditional claim-index rebuild in cacheModel is MANDATORY: GetModel(%s) = %+v (ID=%q kind=%q), want the group row %q (kind=group) — the group row was installed by the native fill WITHOUT activating its shadow claim",
			group.ShadowModelID, clientFacing, clientFacing.ID, clientFacing.Kind, group.ID)
	}

	// The claim index itself names the group as the owner.
	cache.mu.RLock()
	claimOwner, claimed = cache.claimsByID[group.ShadowModelID]
	cache.mu.RUnlock()
	if !claimed || claimOwner != group.ID {
		t.Fatalf("the claim index must own the shadowed ID after the group fill: claims[%s] = %q (claimed = %v), want %q",
			group.ShadowModelID, claimOwner, claimed, group.ID)
	}

	// The native read stays CLAIM-BLIND: the execution path still gets
	// the MEMBER row, not the group row — the two semantics are
	// independent, which is why the rebuild must not disturb the
	// native path.
	member := cache.GetModelNative(group.ShadowModelID)
	if member == nil {
		t.Fatalf("GetModelNative(%s) = nil — the member row must resolve", group.ShadowModelID)
	}
	if member.ID != group.ShadowModelID || member.IsGroup() {
		t.Fatalf("the native read must stay CLAIM-BLIND: GetModelNative(%s) = %+v (ID=%q), want the member row", group.ShadowModelID, member, member.ID)
	}
}
