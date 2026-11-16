package database

// wi-4d2c8e17 / ruling 1+2 — the LAYER-SEPARATION contract for group
// member selection (supersedes the wi-7769a812 part-2/3 skip-cache
// ordering):
//
//	1. R1 — the group pick is POLICY. A skip-cache (upstream-layer)
//	   mark NEVER changes WHICH member becomes the head; it suppresses
//	   that member's ATTEMPT downstream and lets the walk proceed into
//	   its chain.
//	2. STRUCTURAL — the group pick cannot read the cache at all:
//	   store_group_rewrite.go imports neither pkg/skipcache nor
//	   pkg/modelcache. memberDemotionExpiry consults only the
//	   GROUP-owned grouplb rotation cooldown.
//	3. AVAILABILITY — marks never make a group empty-at-runtime: the
//	   pick is always a real servable member.
//
// The cache API is still exercised here (Mark/Marked) to prove the
// isolation is behavioural, not merely "the test didn't call it".

import (
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/skipcache"
)

// newSkipCacheGroupManager seeds the canonical two-member span-
// provider group (coding: openai/glm-5 + coding-alt: zhipu/kimi) and
// wires a fresh skip cache into the manager.
func newSkipCacheGroupManager(t *testing.T) (*ModelsManager, *skipcache.Cache, func()) {
	t.Helper()
	mgr, cleanup := newGroupManager(t)
	cache := skipcache.New()
	mgr.SetSkipCache(cache)
	if err := mgr.AddModel(validGroup()); err != nil {
		cleanup()
		t.Fatalf("AddModel(group): %v", err)
	}
	return mgr, cache, cleanup
}

// pickOf rewrites a direct-named group entry and returns the served
// member id ("" when the rewrite failed).
func pickOf(t *testing.T, mgr *ModelsManager, key string) string {
	t.Helper()
	res := mgr.RewriteGroupEntries([]string{"coding-group"}, key)
	if res.FailedGroupID != "" {
		return ""
	}
	return res.MemberID
}

// TestGroup_SkipCache_MarksDoNotChangeThePick (R1) — pin a sticky
// binding first, then storm the bound member's credentials with
// upstream marks: the pick must stay byte-stable.
func TestGroup_SkipCache_MarksDoNotChangeThePick(t *testing.T) {
	mgr, cache, cleanup := newSkipCacheGroupManager(t)
	defer cleanup()

	key := "conv-stable-pick"
	baseline := pickOf(t, mgr, key)
	if baseline == "" {
		t.Fatal("baseline pick failed")
	}

	// Mark BOTH members on every scope — a storm. If the group pick
	// consulted the cache, the bound member would be demoted/flipped.
	cache.Mark(skipcache.ScopeCredential, "cred-openai", "glm-5", time.Minute)
	cache.Mark(skipcache.ScopeProvider, "openai", "glm-5", time.Minute)
	cache.Mark(skipcache.ScopeCredential, "cred-zhipu", "kimi", time.Minute)
	cache.Mark(skipcache.ScopeProvider, "zhipu", "kimi", time.Minute)

	for i := 0; i < 10; i++ {
		if got := pickOf(t, mgr, key); got != baseline {
			t.Fatalf("pick changed to %q under upstream marks (baseline %q) — the cache decided the group pick (R1 violation)", got, baseline)
		}
	}
}

// TestGroup_SkipCache_ProviderWallDoesNotChangeThePick — a
// provider-scope wall on the bound member must not flip the pick.
func TestGroup_SkipCache_ProviderWallDoesNotChangeThePick(t *testing.T) {
	mgr, cache, cleanup := newSkipCacheGroupManager(t)
	defer cleanup()

	key := "conv-provider-wall"
	baseline := pickOf(t, mgr, key)
	if baseline == "" {
		t.Fatal("baseline pick failed")
	}
	cache.Mark(skipcache.ScopeProvider, "openai", "glm-5", time.Minute)

	for i := 0; i < 5; i++ {
		if got := pickOf(t, mgr, key); got != baseline {
			t.Fatalf("pick changed to %q under a provider wall (baseline %q) — R1 violation", got, baseline)
		}
	}
}

// TestGroup_SkipCache_AllMarked_StillServes — every member marked is
// NOT an availability gate: the rewrite still serves a real member
// (never FailedGroupID).
func TestGroup_SkipCache_AllMarked_StillServes(t *testing.T) {
	mgr, cache, cleanup := newSkipCacheGroupManager(t)
	defer cleanup()

	cache.Mark(skipcache.ScopeCredential, "cred-openai", "glm-5", time.Minute)
	cache.Mark(skipcache.ScopeProvider, "openai", "glm-5", time.Minute)
	cache.Mark(skipcache.ScopeCredential, "cred-zhipu", "kimi", time.Minute)
	cache.Mark(skipcache.ScopeProvider, "zhipu", "kimi", time.Minute)

	res := mgr.RewriteGroupEntries([]string{"coding-group"}, "conv-all-marked")
	if res.FailedGroupID != "" {
		t.Fatalf("all-marked group failed as empty-at-runtime (FailedGroupID=%q) — marks are an optimizer, never an availability gate", res.FailedGroupID)
	}
	if res.MemberID != "coding" && res.MemberID != "coding-alt" {
		t.Fatalf("all-marked pick = %q, want one of the group's members", res.MemberID)
	}
}

// TestGroup_SkipCache_MarkNeverBenchesOrderingSeam — the REAL ordering
// seam (memberDemotionExpiry) is blind to skip-cache marks: a live
// mark does NOT bench the member. The cache is not consulted here at
// all (structural isolation).
func TestGroup_SkipCache_MarkNeverBenchesOrderingSeam(t *testing.T) {
	mgr, cache, cleanup := newSkipCacheGroupManager(t)
	defer cleanup()

	cache.Mark(skipcache.ScopeCredential, "cred-openai", "glm-5", time.Minute)
	cache.Mark(skipcache.ScopeProvider, "openai", "glm-5", time.Minute)

	if _, benched := mgr.memberDemotionExpiry("coding-group", mgr.GetModelNative("coding")); benched {
		t.Fatal("a live skip mark benched the member via memberDemotionExpiry — the group pick must not read the upstream cache (R2/structural)")
	}
	// The mark IS live in the cache (proves the test exercised it).
	if !cache.Marked(skipcache.ScopeCredential, "cred-openai", "glm-5") {
		t.Fatal("sanity: the skip mark should be live")
	}
}

// TestGroup_SkipCache_NilCacheByteIdentical — a manager without
// SetSkipCache still serves a member (nil-cache degradation).
func TestGroup_SkipCache_NilCacheByteIdentical(t *testing.T) {
	mgr, _, cleanup := newSkipCacheGroupManager(t)
	defer cleanup()
	mgr.SetSkipCache(nil) // unwire — the legacy/test main shape

	got := pickOf(t, mgr, "conv-nil-cache")
	if got != "coding" && got != "coding-alt" {
		t.Fatalf("nil-cache pick = %q, want one of the group's members", got)
	}
}

// TestGroup_SkipCache_ProviderMarkDoesNotWallSibling — a provider
// mark never reaches the ordering seam (ok=false) and the member stays
// a servable CANDIDATE (shape predicate true).
func TestGroup_SkipCache_ProviderMarkDoesNotWallSibling(t *testing.T) {
	mgr, cleanup := newGroupManager(t)
	defer cleanup()
	cache := skipcache.New()
	mgr.SetSkipCache(cache)

	if err := mgr.AddCredential(models.CredentialConfig{
		ID: "cred-a", Provider: "openai", APIKey: "key-a", BaseURL: "http://upstream-a",
	}); err != nil {
		t.Fatalf("AddCredential cred-a: %v", err)
	}
	if err := mgr.AddCredential(models.CredentialConfig{
		ID: "cred-b", Provider: "openai", APIKey: "key-b", BaseURL: "http://upstream-b",
	}); err != nil {
		t.Fatalf("AddCredential cred-b: %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "dual", Name: "dual", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "dual-model",
		Credentials: models.TestRefs("cred-a", "cred-b"),
	}); err != nil {
		t.Fatalf("AddModel(dual): %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "dual-group", Name: "dual-group", Enabled: true,
		Kind: models.KindGroup, Internal: true,
		Members: []models.GroupMember{{ModelID: "dual", Weight: 1}},
	}); err != nil {
		t.Fatalf("AddModel(dual-group): %v", err)
	}

	cache.Mark(skipcache.ScopeProvider, "openai", "dual-model", time.Minute)

	row := mgr.GetModelNative("dual")
	if row == nil {
		t.Fatal("dual row missing")
	}
	if _, benched := mgr.memberDemotionExpiry("dual-group", row); benched {
		t.Fatal("provider mark reached memberDemotionExpiry — the group pick must not read the upstream cache")
	}
	if !mgr.isServableGroupMember(row) {
		t.Fatal("provider-marked member reported unservable — marks must never filter candidacy")
	}
}
