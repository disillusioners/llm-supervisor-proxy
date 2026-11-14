package proxy

// CRITICAL-FIX PASS (part 3 review) — win-clear seams that the raw-
// manager repro suite cannot see:
//
//   - TestWinClear_ReachesEngineThroughCachedConfig (Critical 1): the
//     production conf.ModelsConfig is *modelscache.CachedModelsConfig
//     (CACHE_LAYER_ENABLED default ON — cmd/main.go). The win-clear's
//     member-cooldown leg rides an OPTIONAL structural assertion
//     (handler_helpers.go), so a decorator without the forwarding
//     method failed that assertion SILENTLY: winning members stayed
//     engine-cooled up to TTL in prod while every raw-manager test
//     (T6) stayed green — the modelscache-decorator-forward-trap,
//     exact shape. This test binds the CACHED decorator and proves
//     the clear reaches the grouplb engine.
//
//   - TestWinClear_ClearsWinnerMemberNotFailedHead (item 4): the
//     member-cooldown clear must target the WINNER's modelList
//     identity, not groupPick.MemberID — on a chain-entry win the
//     picked head FAILED this request and its cooldown is genuine
//     failure bookkeeping that must survive. Bound to the RAW manager
//     to isolate the identity guard from the decorator forward (the
//     composed cached+identity path is covered by the two tests
//     together: decorator (this file, test 1) × identity (test 2)).

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelscache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/skipcache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// winClearPairGroupID is the two-member default-pool group both tests
// run against.
const winClearPairGroupID = "wc-group"

// seedWinClearPair seeds memberA + memberB (both default pool, no
// backup members) with the given per-member upstream statuses wired
// through a fresh stub. memberA is the heavy head candidate;
// memberB's role (benched winner / chain entry) is decided by the
// caller's arming + memberA's chain.
func seedWinClearPair(t *testing.T, h *groupHarness, memberAChain []string) {
	t.Helper()
	for _, id := range []string{"wc-a", "wc-b"} {
		if err := h.mgr.AddModel(models.ModelConfig{
			ID: id, Name: id, Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: id,
			Credentials: models.TestRefs(p5FixtureCredID),
		}); err != nil {
			t.Fatalf("AddModel(%s): %v", id, err)
		}
	}
	if len(memberAChain) > 0 {
		if err := h.mgr.UpdateModel("wc-a", models.ModelConfig{
			ID: "wc-a", Name: "wc-a", Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: "wc-a",
			Credentials:   models.TestRefs(p5FixtureCredID),
			FallbackChain: memberAChain,
		}); err != nil {
			t.Fatalf("UpdateModel(wc-a chain): %v", err)
		}
	}
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: winClearPairGroupID, Name: winClearPairGroupID, Enabled: true,
		Kind: models.KindGroup, Internal: true,
		Members: []models.GroupMember{
			{ModelID: "wc-a", Weight: 10},
			{ModelID: "wc-b", Weight: 1},
		},
	}); err != nil {
		t.Fatalf("AddModel(%s): %v", winClearPairGroupID, err)
	}
}

// swapWinClearStub replaces the harness stub with a per-model status
// dispatcher (the T1 shape) and re-points the fixture credential.
func swapWinClearStub(t *testing.T, h *groupHarness, statuses map[string]int) *t1StubState {
	t.Helper()
	st := &t1StubState{statuses: statuses}
	h.up.Close()
	h.up = httptest.NewServer(newT1Stub(st))
	if err := h.mgr.UpdateCredential(p5FixtureCredID, models.CredentialConfig{
		ID: p5FixtureCredID, Provider: "openai", APIKey: "key-" + p5FixtureCredID, BaseURL: h.up.URL,
	}); err != nil {
		t.Fatalf("UpdateCredential(%s): %v", p5FixtureCredID, err)
	}
	return st
}

// rebuildHandlerWithCachedModels rebinds the harness handler with
// conf.ModelsConfig = the CACHED decorator (the production binding,
// cmd/main.go) while the manager-side skip-cache read stays wired on
// the raw manager (the decorator forwards the rewrite to the inner
// manager, which owns the read).
func rebuildHandlerWithCachedModels(t *testing.T, h *groupHarness, cached *modelscache.CachedModelsConfig, cache *skipcache.Cache) {
	t.Helper()
	mgrCfg := newTestManagerWithConfig(t, h.up.URL)
	reqStore := store.NewRequestStore(100, store.WithMaxBytes(256<<20))
	*h.h = *NewHandler(&Config{
		ConfigMgr:         mgrCfg,
		ModelsConfig:      cached,
		UpstreamSkipCache: cache,
	}, h.bus, reqStore, nil, nil, nil, h.mgr.Engine(), nil)
}

// TestWinClear_ReachesEngineThroughCachedConfig (CRITICAL 1
// decorator-through): the win-clear's grouplb member-cooldown leg
// must reach the engine THROUGH the CACHED config — on the pre-fix
// decorator (no ClearGroupMemberCooldown forward) the handler's
// structural assertion misses and the winner's cooldown row survives
// this request. Shape: heavy unmarked head wc-a fails 503; the
// re-entry reselects the benched wc-b, which WINS and must have its
// seed cooldown + skip marks cleared; the failed head's cooldown
// must survive.
func TestWinClear_ReachesEngineThroughCachedConfig(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedWinClearPair(t, h, nil)

	cache := skipcache.New()
	h.mgr.SetSkipCache(cache)
	eng := h.mgr.GroupEngine()

	// Arm the benched winner wc-b: a grouplb cooldown row (engine
	// default window — same-window shape per the incident lesson) plus
	// skip marks on both identities its win implicates.
	eng.ExcludeAndReselect(winClearPairGroupID, "__seed-wc1__", "wc-b", 0)
	cache.Mark(skipcache.ScopeCredential, p5FixtureCredID, "wc-b", time.Minute)
	cache.Mark(skipcache.ScopeProvider, "openai", "wc-b", time.Minute)
	if _, ok := eng.MemberCooldownUntil(winClearPairGroupID, "wc-b"); !ok {
		t.Fatal("seed: wc-b cooldown row not armed — the test cannot exercise the win-clear seam")
	}

	st := swapWinClearStub(t, h, map[string]int{
		"wc-a": http.StatusServiceUnavailable, // heavy head fails once
		"wc-b": http.StatusOK,                 // benched member wins
	})

	// THE PRODUCTION BINDING: conf.ModelsConfig = *modelscache
	// .CachedModelsConfig (CACHE_LAYER_ENABLED default ON).
	cached, err := modelscache.NewCachedModelsConfig(h.mgr, modelscache.Options{ReconcileInterval: time.Hour})
	if err != nil {
		t.Fatalf("NewCachedModelsConfig: %v", err)
	}
	defer cached.Stop()
	if _, ok := any(cached).(interface {
		ClearGroupMemberCooldown(groupID, memberID string)
	}); !ok {
		t.Fatal("PRE-FIX shape: *modelscache.CachedModelsConfig does not satisfy the win-clear assertion — the decorator forward is missing (Critical 1)")
	}
	rebuildHandlerWithCachedModels(t, h, cached, cache)

	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model": winClearPairGroupID,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleChatCompletions(rec, req)

	_, attempted := st.snapshot()
	t.Logf("cached win-clear attempted=%v status=%d", attempted, rec.Code)
	if len(attempted) == 0 {
		t.Fatal("VACUOUS: no upstream attempts recorded — intended path not exercised")
	}
	sawA, sawB := false, false
	for _, m := range attempted {
		if m == "wc-a" {
			sawA = true
		}
		if m == "wc-b" {
			sawB = true
		}
	}
	if !sawA || !sawB {
		t.Fatalf("intended shape not exercised: wc-a (failed head) and wc-b (winner) must both be attempted; attempted=%v", attempted)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("request not served (status=%d); attempted=%v", rec.Code, attempted)
	}

	// CRITICAL 1 — the clear must reach the engine THROUGH the cached
	// decorator: the winner's cooldown row must be gone.
	if _, ok := eng.MemberCooldownUntil(winClearPairGroupID, "wc-b"); ok {
		t.Fatal("CRITICAL 1: wc-b's cooldown row survived its win with the CACHED config bound — ClearGroupMemberCooldown is a no-op through the decorator")
	}
	// The win also clears its own skip marks (independent of the
	// decorator — they ride conf.UpstreamSkipCache directly).
	if cache.Marked(skipcache.ScopeCredential, p5FixtureCredID, "wc-b") {
		t.Fatal("wc-b's (credential, wire-model) skip mark survived its win")
	}
	if cache.Marked(skipcache.ScopeProvider, "openai", "wc-b") {
		t.Fatal("wc-b's (provider, wire-model) skip mark survived its win")
	}
	// The FAILED head's cooldown (written by the re-entry's honest
	// exclusion) must SURVIVE — the clear targets the winner, never
	// the failed head.
	if _, ok := eng.MemberCooldownUntil(winClearPairGroupID, "wc-a"); !ok {
		t.Fatal("failed head wc-a's cooldown was cleared — win-clear must target the winner only")
	}
}

// TestWinClear_ClearsWinnerMemberNotFailedHead (item 4): a CHAIN-ENTRY
// win must clear the WINNER's member cooldown and leave the failed
// head's cooldown alive. Shape: both members benched (equal expiries —
// ties break by configured order, so wc-a is the demoted head), wc-a's
// chain carries wc-b; wc-a fails 503 and the chain entry wc-b wins.
// The pre-fix code cleared groupPick.MemberID (wc-a — the member that
// just FAILED) while the actual winner wc-b stayed bench-marked.
// Bound to the RAW manager to isolate the identity guard from the
// decorator forward.
func TestWinClear_ClearsWinnerMemberNotFailedHead(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedWinClearPair(t, h, []string{"wc-b"})

	eng := h.mgr.GroupEngine()
	// Burst-arm BOTH members with equal expiries: the ordering sweep
	// serves the soonest-expiring head, ties by configured order →
	// wc-a is the served head; wc-b is its chain entry.
	eng.InjectAllCoolingForTest(winClearPairGroupID)
	if _, ok := eng.MemberCooldownUntil(winClearPairGroupID, "wc-a"); !ok {
		t.Fatal("seed: wc-a cooldown row not armed")
	}
	if _, ok := eng.MemberCooldownUntil(winClearPairGroupID, "wc-b"); !ok {
		t.Fatal("seed: wc-b cooldown row not armed")
	}

	st := swapWinClearStub(t, h, map[string]int{
		"wc-a": http.StatusServiceUnavailable, // head fails
		"wc-b": http.StatusOK,                 // chain entry wins
	})
	h.rebuildHandler(t) // RAW manager binding (isolation: identity guard only)

	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model": winClearPairGroupID,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleChatCompletions(rec, req)

	_, attempted := st.snapshot()
	t.Logf("chain-entry win attempted=%v status=%d", attempted, rec.Code)
	if len(attempted) == 0 {
		t.Fatal("VACUOUS: no upstream attempts recorded")
	}
	sawA, sawB := false, false
	for _, m := range attempted {
		if m == "wc-a" {
			sawA = true
		}
		if m == "wc-b" {
			sawB = true
		}
	}
	if !sawA || !sawB {
		t.Fatalf("chain-entry win shape not exercised: head wc-a and chain entry wc-b must both be attempted; attempted=%v", attempted)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("request not served (status=%d); attempted=%v", rec.Code, attempted)
	}

	// ITEM 4 — the WINNER's member cooldown must be cleared...
	if _, ok := eng.MemberCooldownUntil(winClearPairGroupID, "wc-b"); ok {
		t.Fatal("ITEM 4: chain-entry winner wc-b's cooldown survived its win — the clear targeted the wrong identity (groupPick.MemberID)")
	}
	// ...and the FAILED head's cooldown must survive (genuine failure
	// bookkeeping — the pre-fix code spuriously cleared THIS row).
	if _, ok := eng.MemberCooldownUntil(winClearPairGroupID, "wc-a"); !ok {
		t.Fatal("ITEM 4: failed head wc-a's cooldown was cleared — the win-clear must never resurrect a member that failed this request")
	}
}
