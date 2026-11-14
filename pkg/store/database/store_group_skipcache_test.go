package database

// wi-7769a812 part 2 / USER RULING D3 — the skip-cache-aware group
// member selection, REWORKED by part 3 / ORDERING-NOT-FILTERING
// (CRITICAL-FIX 73e3bd0): health state reaches selection ONLY through
// memberDemotionExpiry (the ordering key — marks DEMOTE a candidate
// to the tail, soonest-expiring first, and never remove it):
//
//	1. ORDERING: a member whose (credential|provider, model) upstream
//	   is skip-marked is DEMOTED behind unmarked candidates (never
//	   excluded — the part-2 "excluded while unmarked alternatives
//	   exist" filtering predicate is RETIRED; it was the incident's
//	   root cause).
//	2. ALL-MARKED: every candidate benched ⇒ the sweep serves the
//	   SOONEST-expiring demoted head — the cache is an OPTIMIZER,
//	   never an availability gate; the group never fails as
//	   empty-at-runtime because of marks.
//	3. EXPIRY: a mark past its TTL stops benching — the member returns
//	   to the unbenched order, selection behavior byte-identical to
//	   pre-part-2 again.
//	4. UNWIRED: a manager without SetSkipCache keeps the exact
//	   pre-part-2 selection (nil-cache degradation).

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

// TestGroup_SkipCache_HealthAwareSelection (A2 fix) — the member
// whose credential is marked is NEVER picked while the alternative is
// unmarked; the pick lands on the healthy member across fresh
// conversation keys (weight-independent).
func TestGroup_SkipCache_HealthAwareSelection(t *testing.T) {
	mgr, cache, cleanup := newSkipCacheGroupManager(t)
	defer cleanup()

	// Mark the heavy member's (credential, wire-model) pair — the
	// limit-class failure shape the coordinator's marking seam mints.
	cache.Mark(skipcache.ScopeCredential, "cred-openai", "glm-5", time.Minute)
	cache.Mark(skipcache.ScopeProvider, "openai", "glm-5", time.Minute)

	for i := 0; i < 10; i++ {
		got := pickOf(t, mgr, "conv-skip-"+string(rune('a'+i)))
		if got != "coding-alt" {
			t.Fatalf("pick = %q, want coding-alt (the marked member's upstreams are walled; selection must be health-aware)", got)
		}
	}
}

// TestGroup_SkipCache_ProviderWallSkipsAllItsMembers — a
// provider-scope mark alone (every cred of the provider marked for
// the model) skips the member without any credential-scope mark.
func TestGroup_SkipCache_ProviderWallSkipsAllItsMembers(t *testing.T) {
	mgr, cache, cleanup := newSkipCacheGroupManager(t)
	defer cleanup()

	cache.Mark(skipcache.ScopeProvider, "openai", "glm-5", time.Minute)

	for i := 0; i < 5; i++ {
		got := pickOf(t, mgr, "conv-wall-"+string(rune('a'+i)))
		if got != "coding-alt" {
			t.Fatalf("pick = %q, want coding-alt (a provider-wide wall skips every member on that provider)", got)
		}
	}
}

// TestGroup_SkipCache_AllMarked_DegradesToSoonestExpiry — the
// anti-availability-gate guard (MANDATE: all-candidates-marked ⇒ ONE
// honest attempt against the SOONEST-EXPIRING mark): with both
// members walled, the rewrite still SERVES (no FailedGroupID), and it
// serves the member whose mark expires first.
func TestGroup_SkipCache_AllMarked_DegradesToSoonestExpiry(t *testing.T) {
	mgr, cache, cleanup := newSkipCacheGroupManager(t)
	defer cleanup()

	// coding's wall expires in 30ms; coding-alt's in 10 minutes —
	// the degradation anchor must pick coding.
	cache.Mark(skipcache.ScopeCredential, "cred-openai", "glm-5", 30*time.Millisecond)
	cache.Mark(skipcache.ScopeProvider, "openai", "glm-5", 30*time.Millisecond)
	cache.Mark(skipcache.ScopeCredential, "cred-zhipu", "kimi", 10*time.Minute)
	cache.Mark(skipcache.ScopeProvider, "zhipu", "kimi", 10*time.Minute)

	res := mgr.RewriteGroupEntries([]string{"coding-group"}, "conv-degrade")
	if res.FailedGroupID != "" {
		t.Fatalf("all-marked group must DEGRADE to one honest attempt, not fail as empty-at-runtime (FailedGroupID=%q)", res.FailedGroupID)
	}
	if res.MemberID != "coding" {
		t.Fatalf("degraded pick = %q, want coding (the SOONEST-expiring mark — 30ms vs 10m)", res.MemberID)
	}
	if res.MemberPool != models.PoolDefault {
		t.Fatalf("degraded pool = %q, want default (the anchor member is a default)", res.MemberPool)
	}
}

// TestGroup_SkipCache_TTLExpiryRestoresSelection — a mark past its
// TTL stops skipping: the member is servable again and the selection
// degrades to the pre-mark behavior.
func TestGroup_SkipCache_TTLExpiryRestoresSelection(t *testing.T) {
	mgr, cache, cleanup := newSkipCacheGroupManager(t)
	defer cleanup()

	cache.Mark(skipcache.ScopeCredential, "cred-openai", "glm-5", 20*time.Millisecond)
	cache.Mark(skipcache.ScopeProvider, "openai", "glm-5", 20*time.Millisecond)

	if got := pickOf(t, mgr, "conv-live"); got != "coding-alt" {
		t.Fatalf("live mark: pick = %q, want coding-alt", got)
	}
	// CRITICAL-FIX item 8 — assert the mark through the REAL ordering
	// seam, not the mark-blind shape predicate: while LIVE, the mark
	// must bench (demote) the member via memberDemotionExpiry.
	if _, benched := mgr.memberDemotionExpiry("coding-group", mgr.GetModelNative("coding")); !benched {
		t.Fatal("pre-expiry sanity: live mark must bench the member (memberDemotionExpiry ok=true) — the ordering seam must see the mark")
	}
	time.Sleep(50 * time.Millisecond)

	row := mgr.GetModelNative("coding")
	// CRITICAL-FIX item 8: isServableGroupMember is the mark-blind
	// Am28 SHAPE predicate under ordering-not-filtering — asserting
	// servability through it passed for the wrong reason (true even
	// with the mark live). The real seam is memberDemotionExpiry: the
	// expired mark must stop BENCHING the member.
	if _, benched := mgr.memberDemotionExpiry("coding-group", row); benched {
		t.Fatal("expired mark must stop benching the member (memberDemotionExpiry ok=false — the cache is an optimizer with a TTL, not a permanent gate)")
	}
	// With the wall gone, BOTH members are servable — the sticky pick
	// is weight-random again, so only assert it is a member.
	got := pickOf(t, mgr, "conv-after-expiry")
	if got != "coding" && got != "coding-alt" {
		t.Fatalf("post-expiry pick = %q, want one of the group's members", got)
	}
}

// TestGroup_SkipCache_NilCacheByteIdentical — a manager without
// SetSkipCache keeps the exact pre-part-2 selection: the marked-
// elsewhere cache is invisible, picks flow unchanged.
func TestGroup_SkipCache_NilCacheByteIdentical(t *testing.T) {
	mgr, _, cleanup := newSkipCacheGroupManager(t)
	defer cleanup()
	mgr.SetSkipCache(nil) // unwire — the legacy test/main shape

	got := pickOf(t, mgr, "conv-nil-cache")
	if got != "coding" && got != "coding-alt" {
		t.Fatalf("nil-cache pick = %q, want one of the group's members (nil cache must not skip anything)", got)
	}
}

// TestGroup_SkipCache_ProviderMarkDoesNotWallSibling (T5, optional
// guard — provider-family hypothesis REJECTED by live evidence; kept
// as cheap defense-in-depth). A provider-scope mark must NOT hide a
// sibling credential on the SAME member (or another member) that
// still has an unmarked upstream. Desired: ordering-only — the marked
// provider's creds/members are still attempted (after unmarked).
func TestGroup_SkipCache_ProviderMarkDoesNotWallSibling(t *testing.T) {
	mgr, cleanup := newGroupManager(t)
	defer cleanup()
	cache := skipcache.New()
	mgr.SetSkipCache(cache)

	// Member with TWO credentials on the same provider (openai), one
	// of which we will mark at credential scope only is not enough;
	// we mark at PROVIDER scope to test the wall.
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

	// Mark the WHOLE provider (openai) at provider scope. Under the
	// retired (filtering) semantics the member vanished from the
	// candidate set; under ordering-not-filtering the mark must reach
	// the REAL ordering seam — the member is DEMOTED (benched with a
	// live expiry), never removed.
	cache.Mark(skipcache.ScopeProvider, "openai", "dual-model", time.Minute)

	row := mgr.GetModelNative("dual")
	if row == nil {
		t.Fatal("dual row missing")
	}
	// CRITICAL-FIX item 8: assert the mark through memberDemotionExpiry
	// — the provider mark must BENCH the member (the ordering seam sees
	// it and sorts it to the tail). Asserting only isServableGroupMember
	// here passed for the wrong reason: that predicate is mark-blind by
	// design (shape gate), so it was true regardless of the mark.
	if until, benched := mgr.memberDemotionExpiry("dual-group", row); !benched {
		t.Fatal("T5 RED: provider mark never reached the ordering seam — the member must be DEMOTED (memberDemotionExpiry ok=true), not invisible to it")
	} else if !until.After(time.Now()) {
		t.Fatalf("T5 RED: benching expiry %v is not in the future — mark not honored as a live demotion", until)
	}
	// ...and the shape predicate stays TRUE: a marked member is a
	// demoted CANDIDATE, never a filtered one (ordering-never-filtering).
	if !mgr.isServableGroupMember(row) {
		t.Fatal("T5 RED: provider-marked member reported unservable — marks must order, never filter")
	}
}
