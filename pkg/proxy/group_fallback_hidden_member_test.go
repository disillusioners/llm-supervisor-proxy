package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/skipcache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// t1StubState records every upstream attempt's wire model (thread-safe)
// and dispatches a per-model HTTP status, so a single upstream server
// can simulate a mixed-failure group (503 / 402 / 200) by model name.
type t1StubState struct {
	mu       sync.Mutex
	calls    int
	models   []string
	statuses map[string]int
}

func (s *t1StubState) record(model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.models = append(s.models, model)
}

func (s *t1StubState) snapshot() (int, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.models))
	copy(out, s.models)
	return s.calls, out
}

func newT1Stub(st *t1StubState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		model, _ := body["model"].(string)
		st.record(model)
		status := st.statuses[model]
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status >= 400 {
			_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"stub failure"}}`))
		} else {
			_, _ = w.Write([]byte(stubUpstreamBody(model, "hi")))
		}
	}
}

// rebuildHandlerWithCache mirrors groupHarness.rebuildHandler but wires
// a shared skip cache into both the manager (read side, store.go:729)
// and the handler config (write side, handler.go:124) — one instance.
func rebuildHandlerWithCache(t *testing.T, h *groupHarness, cache *skipcache.Cache) {
	t.Helper()
	// Re-point EVERY credential the fixtures may have created to the
	// rebuilt stub URL — otherwise cred-od-flash / cred-od-khanguyen
	// keep dialing the CLOSED first stub (connection-refused noise
	// that mis-shapes the sweep). Fixtures that never created a given
	// id (the minimal T4 two-member group) are skipped.
	for _, cid := range []string{p5FixtureCredID, "cred-od-flash", "cred-od-khanguyen"} {
		if h.mgr.GetCredential(cid) == nil {
			continue
		}
		if err := h.mgr.UpdateCredential(cid, models.CredentialConfig{
			ID: cid, Provider: "openai", APIKey: "key-" + cid, BaseURL: h.up.URL,
		}); err != nil {
			t.Fatalf("rebuildHandlerWithCache UpdateCredential(%s): %v", cid, err)
		}
	}
	mgrCfg := newTestManagerWithConfig(t, h.up.URL)
	reqStore := store.NewRequestStore(100, store.WithMaxBytes(256<<20))
	*h.h = *NewHandler(&Config{
		ConfigMgr:         mgrCfg,
		ModelsConfig:      h.mgr,
		UpstreamSkipCache: cache,
	}, h.bus, reqStore, nil, nil, nil, h.mgr.Engine(), nil)
}

// seedT1Group seeds the incident topology (realism per mechanism update):
// agentic-Main (default pool, w10, healthy), 9router-sonnet (backup, w2, 503),
// opendesign-flash (backup, w2, 402 on 2 creds), agentic-go (backup, w1, 429),
// coding-free2 (backup, w1, DISABLED — negative control).
func seedT1Group(t *testing.T, h *groupHarness) {
	t.Helper()
	// extra creds for opendesign-flash (402 on both)
	for _, cid := range []string{"cred-od-flash", "cred-od-khanguyen"} {
		if err := h.mgr.AddCredential(models.CredentialConfig{
			ID: cid, Provider: "openai", APIKey: "key-" + cid, BaseURL: h.up.URL,
		}); err != nil {
			t.Fatalf("AddCredential(%s): %v", cid, err)
		}
	}

	type memberSpec struct {
		id       string
		weight   float64
		backup   bool
		disabled bool
		creds    []string
	}
	specs := []memberSpec{
		{id: "9router-sonnet", weight: 2.0, backup: true, creds: []string{p5FixtureCredID}},
		{id: "opendesign-flash", weight: 2.0, backup: true, creds: []string{"cred-od-flash", "cred-od-khanguyen"}},
		{id: "agentic-go", weight: 1.0, backup: true, creds: []string{p5FixtureCredID}},
		{id: "coding-free2", weight: 1.0, backup: true, disabled: true, creds: []string{p5FixtureCredID}},
		{id: "agentic-Main", weight: 10.0, backup: false, creds: []string{p5FixtureCredID}},
	}
	for _, sp := range specs {
		enabled := !sp.disabled
		fc := []string{}
		if sp.id == "9router-sonnet" {
			fc = []string{"opendesign-flash"}
		}
		if err := h.mgr.AddModel(models.ModelConfig{
			ID: sp.id, Name: sp.id, Enabled: enabled, Kind: models.KindChat,
			Internal: true, InternalModel: sp.id,
			Credentials:   models.TestRefs(sp.creds...),
			FallbackChain: fc,
		}); err != nil {
			t.Fatalf("AddModel(%s): %v", sp.id, err)
		}
	}
	members := make([]models.GroupMember, 0, len(specs))
	for _, sp := range specs {
		members = append(members, models.GroupMember{
			ModelID: sp.id, Weight: sp.weight, BackupOnly: sp.backup,
		})
	}
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "agentic-group", Name: "agentic-group", Enabled: true,
		Kind: models.KindGroup, Internal: true,
		Members: members,
	}); err != nil {
		t.Fatalf("AddModel(agentic-group): %v", err)
	}
}

// armT1Cooldowns arms grouplb member cooldowns with DISTINCT expiries so
// backups (short) are attempted before agentic-Main (long) — simulating
// the burst window that cooled everything, with backups cooling sooner.
// Route: (b) seeded via the sanctioned Engine.ExcludeAndReselect seam
// (natural arming was impractical: it requires N terminal-failure
// requests, one per member, each exhausting the whole group).
func armT1Cooldowns(t *testing.T, h *groupHarness) {
	t.Helper()
	// Route (b): sanctioned seam — arms EVERY member cooling (burst
	// simulation). Equal expiries -> soonest-first ties break by member
	// order in the slice (backups first, agentic-Main last).
	h.mgr.GroupEngine().InjectAllCoolingForTest("agentic-group")
}

// TestHandlerGroups_HiddenHealthyMember_LogRepro (T1): the incident
// topology must be served by a healthy member (agentic-Main or
// agentic-go); on current HEAD (v192) the request fails because the
// backup-pool re-entry never reaches the default pool.
func TestHandlerGroups_HiddenHealthyMember_LogRepro(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedT1Group(t, h)

	cache := skipcache.New()
	h.mgr.SetSkipCache(cache)

	st := &t1StubState{statuses: map[string]int{
		"9router-sonnet":   http.StatusServiceUnavailable,
		"opendesign-flash": http.StatusPaymentRequired,
		"agentic-go":       http.StatusTooManyRequests,
		"agentic-Main":     http.StatusOK,
	}}
	h.up.Close()
	h.up = httptest.NewServer(newT1Stub(st))
	rebuildHandlerWithCache(t, h, cache)

	armT1Cooldowns(t, h)
	t.Logf("after arm: DefaultPoolHealthy=%v HasBackupPool=%v", h.mgr.GroupEngine().DefaultPoolHealthy("agentic-group"), h.mgr.GroupEngine().HasBackupPool("agentic-group"))

	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model": "agentic-group",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleChatCompletions(rec, req)

	calls, attempted := st.snapshot()
	t.Logf("T1 attempts=%d attempted=%v status=%d", calls, attempted, rec.Code)

	// Negative-control: the stub must have been hit.
	if calls == 0 {
		t.Fatal("VACUOUS: no upstream attempts recorded — intended path not exercised")
	}
	// coding-free2 (disabled) must NEVER be attempted.
	for _, m := range attempted {
		if m == "coding-free2" {
			t.Fatalf("disabled member coding-free2 was attempted — exclusion-by-disable broken")
		}
	}
	// A healthy member must have been attempted.
	seenHealthy := false
	for _, m := range attempted {
		if m == "agentic-Main" || m == "agentic-go" {
			seenHealthy = true
		}
	}
	if !seenHealthy {
		t.Fatalf("no healthy member (agentic-Main/agentic-go) was attempted; attempted=%v", attempted)
	}
	// Desired: request served (200), not terminal failure.
	if rec.Code != http.StatusOK {
		t.Fatalf("T1 RED: request not served (status=%d); healthy member was not reached via re-entry. attempted=%v", rec.Code, attempted)
	}
}

// TestHandlerGroups_MarkedMembers_Ordering (T2): skip marks and
// grouplb cooldowns ORDER, never FILTER — the candidate set is always
// the full servable member×credential set. Unmarked first (pool/weight
// order), then marked soonest-expiring-first; REQUEST_FAILED only
// after every member×credential across both pools genuinely failed
// this request (the within-request tried-set is never re-picked).
func TestHandlerGroups_MarkedMembers_Ordering(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedT1Group(t, h)

	cache := skipcache.New()
	h.mgr.SetSkipCache(cache)

	// Mark only the backups (cooling); agentic-Main stays UNMARKED
	// (healthy) so it is an unmarked candidate.
	// All members marked (burst) — agentic-Main is a marked candidate
	// with the same expiry as backups; ordering is soonest-first, ties
	// break by member order (backups first, agentic-Main last).
	h.mgr.GroupEngine().InjectAllCoolingForTest("agentic-group")

	st := &t1StubState{statuses: map[string]int{
		"9router-sonnet":   http.StatusServiceUnavailable,
		"opendesign-flash": http.StatusPaymentRequired,
		"agentic-go":       http.StatusTooManyRequests,
		"agentic-Main":     http.StatusOK,
	}}
	h.up.Close()
	h.up = httptest.NewServer(newT1Stub(st))
	rebuildHandlerWithCache(t, h, cache)

	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model": "agentic-group",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleChatCompletions(rec, req)

	calls, attempted := st.snapshot()
	t.Logf("T2 attempts=%d attempted=%v status=%d", calls, attempted, rec.Code)
	if calls == 0 {
		t.Fatal("VACUOUS: no upstream attempts recorded")
	}
	// (i) every unmarked servable candidate attempted before any marked:
	// agentic-Main is unmarked -> must be among the FIRST attempts,
	// before the (all-failing) marked backups exhaust. Concretely: the
	// last attempt must be an unmarked survivor OR the request served.
	// (ii) marked never skipped: all three marked backups must appear.
	markedSeen := map[string]bool{}
	for _, m := range attempted {
		if m == "9router-sonnet" || m == "opendesign-flash" || m == "agentic-go" {
			markedSeen[m] = true
		}
	}
	if len(markedSeen) != 3 {
		t.Fatalf("T2: marked members skipped — only %d/3 attempted: %v", len(markedSeen), attempted)
	}
	// (iii) marked never skipped: all three marked backups attempted.
	if len(markedSeen) != 3 {
		t.Fatalf("T2 RED: marked members skipped — only %d/3 attempted: %v", len(markedSeen), attempted)
	}
	// Cross-pool reach: agentic-Main (default pool, marked) must be
	// attempted after backups fail.
	agenticMainAttempted := false
	for _, m := range attempted {
		if m == "agentic-Main" {
			agenticMainAttempted = true
		}
	}
	if !agenticMainAttempted {
		t.Fatalf("T2 RED: cross-pool marked member agentic-Main never attempted; attempted=%v", attempted)
	}
	// Desired: request served by the healthy marked member.
	if rec.Code != http.StatusOK {
		t.Fatalf("T2 RED: request not served (status=%d); attempted=%v", rec.Code, attempted)
	}
}

// TestHandlerGroups_WinClearsCooldown (T6): a member that WINS a race
// while carrying an older cooldown row must remain a candidate for
// subsequent requests (demotion to later order is OK; EXCLUSION is
// not), and the WIN must CLEAR its benching state (skip-cache marks +
// grouplb member-cooldown row) via the win-clear seam
// (handler_helpers.go clearWinnerUpstreamSkip -> store_group_rewrite.go
// ClearGroupMemberCooldown -> grouplb ClearCooldown). Shape: agentic-Main
// fails once (503) so agentic-go (pre-marked + cooled) serves and the
// seam is exercised end-to-end.
func TestHandlerGroups_WinClearsCooldown(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedT1Group(t, h)

	cache := skipcache.New()
	h.mgr.SetSkipCache(cache)

	// Arm agentic-go's benching state: a grouplb member-cooldown row
	// (old failure, engine-default window) PLUS live skip-cache marks
	// on BOTH identities it implicates ((credential, wire-model) and
	// (provider, wire-model)) so the win-clear seam has something to
	// clear on each surface. Window = engine default (60s), NOT a
	// multi-hour row: a deep-tail 1h row is structurally unreachable
	// by the ordering sweep while the tried members keep re-arming
	// 60s rows ahead of it (the offer walk advances by excluding
	// tried heads, each exclusion re-arming past the tail) — the
	// incident's real shape was a same-window row the WIN should
	// clear, not an unreachable tail.
	eng := h.mgr.GroupEngine()
	eng.ExcludeAndReselect("agentic-group", "__seed6__", "agentic-go", 0)
	cache.Mark(skipcache.ScopeCredential, p5FixtureCredID, "agentic-go", time.Minute)
	cache.Mark(skipcache.ScopeProvider, "openai", "agentic-go", time.Minute)
	if until, ok := eng.MemberCooldownUntil("agentic-group", "agentic-go"); !ok {
		t.Fatal("T6 seed: agentic-go cooldown row not armed — test cannot exercise the win-clear seam")
	} else {
		_ = until
	}

	st := &t1StubState{statuses: map[string]int{
		"9router-sonnet":   http.StatusServiceUnavailable,
		"opendesign-flash": http.StatusPaymentRequired,
		"agentic-go":       http.StatusOK,                 // agentic-go is the healthy winner
		"agentic-Main":     http.StatusServiceUnavailable, // FAIL once — forces a second member to serve
	}}
	h.up.Close()
	h.up = httptest.NewServer(newT1Stub(st))
	rebuildHandlerWithCache(t, h, cache)

	// Request #1: agentic-Main fails; the sweep reaches agentic-go,
	// which WINS — the win-clear seam fires.
	rec1 := httptest.NewRecorder()
	req1 := makeChatRequest(t, map[string]interface{}{
		"model": "agentic-group",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "first"},
		},
	})
	h.h.HandleChatCompletions(rec1, req1)
	_, attempted1 := st.snapshot()
	t.Logf("T6 req1 attempted=%v status=%d", attempted1, rec1.Code)
	if rec1.Code != http.StatusOK {
		t.Fatalf("T6: first request not served (status=%d); attempted=%v", rec1.Code, attempted1)
	}
	agenticGoWon := false
	for _, m := range attempted1 {
		if m == "agentic-go" {
			agenticGoWon = true
		}
	}
	if !agenticGoWon {
		t.Fatalf("T6: agentic-go did not win request #1 (not attempted); attempted=%v — win-clear seam not reached", attempted1)
	}

	// SUPPLEMENTARY (direct seam): the win must have cleared agentic-go's
	// benching state on ALL THREE surfaces.
	if until, ok := eng.MemberCooldownUntil("agentic-group", "agentic-go"); ok {
		t.Fatalf("T6: grouplb cooldown row survived agentic-go's win (until=%v) — win-clear seam did not fire", until)
	}
	if cache.Marked(skipcache.ScopeCredential, p5FixtureCredID, "agentic-go") {
		t.Fatalf("T6: (credential, wire-model) skip mark survived agentic-go's win — Cache.Clear did not fire")
	}
	if cache.Marked(skipcache.ScopeProvider, "openai", "agentic-go") {
		t.Fatalf("T6: (provider, wire-model) skip mark survived agentic-go's win — Cache.Clear did not fire")
	}

	// Request #2: subsequent request — agentic-go must still be a
	// candidate (attempted, demotion allowed, exclusion is not), and
	// with its benching state cleared it should be attempted EARLY
	// (first or second attempt, not at the tail of the sweep).
	st2CallsBefore := st.calls
	rec2 := httptest.NewRecorder()
	req2 := makeChatRequest(t, map[string]interface{}{
		"model": "agentic-group",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "second"},
		},
	})
	h.h.HandleChatCompletions(rec2, req2)
	_, attempted2 := st.snapshot()
	t.Logf("T6 req2 attempted=%v status=%d (new calls since req1: %d)", attempted2, rec2.Code, st.calls-st2CallsBefore)
	if st.calls == st2CallsBefore {
		t.Fatal("VACUOUS: second request recorded no upstream attempts")
	}
	agenticGoAttempted := false
	for _, m := range attempted2 {
		if m == "agentic-go" {
			agenticGoAttempted = true
		}
	}
	if !agenticGoAttempted {
		t.Fatalf("T6 RED: agentic-go excluded from second request despite being a prior winner; attempted=%v", attempted2)
	}
	// Head-of-order check on REQUEST #2's OWN attempts (the snapshot is
	// cumulative across both requests — slice off request #1's calls).
	req2Attempts := attempted2[len(attempted2)-(st.calls-st2CallsBefore):]
	if len(req2Attempts) > 0 && req2Attempts[0] != "agentic-go" {
		t.Logf("T6 note: agentic-go cleared but not the FIRST attempt of request #2 (demoted?): req2 attempts=%v", req2Attempts)
	}
	if rec2.Code != http.StatusOK {
		t.Fatalf("T6: second request not served (status=%d); attempted=%v", rec2.Code, attempted2)
	}
}

// TestHandlerGroups_CrossPoolExhaustion_BeforeTerminal (T3): REQUEST_FAILED
// may fire ONLY after every servable member×credential across BOTH pools
// (default + backup) genuinely failed THIS request. No terminal failure
// while any pool holds an unattempted servable candidate.
func TestHandlerGroups_CrossPoolExhaustion_BeforeTerminal(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedT1Group(t, h)

	cache := skipcache.New()
	h.mgr.SetSkipCache(cache)
	h.mgr.GroupEngine().InjectAllCoolingForTest("agentic-group")

	st := &t1StubState{statuses: map[string]int{
		"9router-sonnet":   http.StatusServiceUnavailable,
		"opendesign-flash": http.StatusPaymentRequired,
		"agentic-go":       http.StatusTooManyRequests,
		"agentic-Main":     http.StatusServiceUnavailable,
	}}
	h.up.Close()
	h.up = httptest.NewServer(newT1Stub(st))
	rebuildHandlerWithCache(t, h, cache)

	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model": "agentic-group",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleChatCompletions(rec, req)

	calls, attempted := st.snapshot()
	t.Logf("T3 attempts=%d attempted=%v status=%d", calls, attempted, rec.Code)
	if calls == 0 {
		t.Fatal("VACUOUS: no upstream attempts recorded")
	}
	// coding-free2 (disabled) must never be attempted.
	for _, m := range attempted {
		if m == "coding-free2" {
			t.Fatalf("disabled member coding-free2 was attempted")
		}
	}
	// Desired: every servable member attempted before terminal failure.
	// Servable members = 4 (all except coding-free2).
	seen := map[string]bool{}
	for _, m := range attempted {
		seen[m] = true
	}
	for _, want := range []string{"9router-sonnet", "opendesign-flash", "agentic-go", "agentic-Main"} {
		if !seen[want] {
			t.Fatalf("T3 RED: servable member %s never attempted before terminal; attempted=%v", want, attempted)
		}
	}
	// Terminal failure expected (all fail) — but only after full coverage.
	if rec.Code == http.StatusOK {
		t.Fatalf("T3: unexpected success (all members fail); attempted=%v", attempted)
	}
}

// TestHandlerGroups_Reentry_NoRePickSameMember (T4b): with only two
// servable members, re-entry must not re-pick the already-failed
// initial member. On current HEAD the tried-set omits the initial pick
// (handler.go:1034-1041) so the second race re-uses it -> duplicate.
func TestHandlerGroups_Reentry_NoRePickSameMember(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()

	// Two-member group, both genuinely failing.
	for _, id := range []string{"m1", "m2"} {
		if err := h.mgr.AddModel(models.ModelConfig{
			ID: id, Name: id, Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: id,
			Credentials:   models.TestRefs(p5FixtureCredID),
			FallbackChain: []string{},
		}); err != nil {
			t.Fatalf("AddModel(%s): %v", id, err)
		}
	}
	// m1's chain includes m2
	if err := h.mgr.UpdateModel("m1", models.ModelConfig{
		ID: "m1", Name: "m1", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "m1",
		Credentials:   models.TestRefs(p5FixtureCredID),
		FallbackChain: []string{"m2"},
	}); err != nil {
		t.Fatalf("UpdateModel(m1): %v", err)
	}
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "g2", Name: "g2", Enabled: true,
		Kind: models.KindGroup, Internal: true,
		Members: []models.GroupMember{
			{ModelID: "m1", Weight: 1},
			{ModelID: "m2", Weight: 1},
		},
	}); err != nil {
		t.Fatalf("AddModel(g2): %v", err)
	}

	h.mgr.GroupEngine().InjectAllCoolingForTest("g2")

	st := &t1StubState{statuses: map[string]int{
		"m1": http.StatusServiceUnavailable,
		"m2": http.StatusServiceUnavailable,
	}}
	h.up.Close()
	h.up = httptest.NewServer(newT1Stub(st))
	cache := skipcache.New()
	h.mgr.SetSkipCache(cache)
	rebuildHandlerWithCache(t, h, cache)

	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model": "g2",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleChatCompletions(rec, req)

	_, attempted := st.snapshot()
	t.Logf("T4b attempted=%v status=%d", attempted, rec.Code)
	if len(attempted) == 0 {
		t.Fatal("VACUOUS: no upstream attempts recorded")
	}
	seen := map[string]bool{}
	for _, m := range attempted {
		if seen[m] {
			t.Fatalf("T4 RED: member %s re-picked within the same request; attempted=%v", m, attempted)
		}
		seen[m] = true
	}
}
