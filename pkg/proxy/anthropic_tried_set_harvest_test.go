package proxy

// CRITICAL-FIX PASS (part 3 review, item 3) — RED-provable pin for the
// Anthropic-lane tried-set harvest.
//
// Shape: group har-group, three default-pool members. har-a is the only
// unbenched member at pick time (har-b and har-c carry seeded cooldown
// rows, armed in that order so har-b sorts soonest), and har-a's
// fallback chain contains har-b — ANOTHER MEMBER of the same group.
// The walk attempts har-a (503) then har-b AS A CHAIN ENTRY (503), so
// har-b was attempted THIS request. The walk exhausts → the re-entry
// gate.
//
//	PRE-FIX (84d41e1): the walk body records nothing, so the
//	tried-set is still {har-a}; the re-entry offer walk accepts
//	har-b as the next member head → har-b runs a SECOND time in
//	the same request (the opendesign-double-run class).
//	POST-FIX: the gate harvests the attempted set, the offer walk
//	refuses the already-tried har-b, advances the ordering scan,
//	and the fresh har-c serves. Each member is attempted exactly
//	once.
//
// Determinism: the seeded 60s rows fix the Phase-4 demoted-head order
// (har-b armed before har-c; the walk's re-armed rows land strictly
// later), so every offer/rebuild head is pinned without wall-clock
// sensitivity — same-window rows per the long-tail starvation lesson
// (a long row would starve behind the re-armed 60s heads).

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

const harTriedSetGroupID = "har-group"

func TestAnthropicReentry_ChainAttemptedMemberNotReOffered(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()

	// Members: har-a (head candidate, chain [har-b]), har-b (same-group
	// member living in har-a's chain), har-c (fresh re-entry target).
	for _, id := range []string{"har-a", "har-b", "har-c"} {
		if err := h.mgr.AddModel(models.ModelConfig{
			ID: id, Name: id, Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: id,
			Credentials: models.TestRefs(p5FixtureCredID),
		}); err != nil {
			t.Fatalf("AddModel(%s): %v", id, err)
		}
	}
	if err := h.mgr.UpdateModel("har-a", models.ModelConfig{
		ID: "har-a", Name: "har-a", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "har-a",
		Credentials:   models.TestRefs(p5FixtureCredID),
		FallbackChain: []string{"har-b"},
	}); err != nil {
		t.Fatalf("UpdateModel(har-a chain): %v", err)
	}
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: harTriedSetGroupID, Name: harTriedSetGroupID, Enabled: true,
		Kind: models.KindGroup, Internal: true,
		Members: []models.GroupMember{
			{ModelID: "har-a", Weight: 10},
			{ModelID: "har-b", Weight: 1},
			{ModelID: "har-c", Weight: 1},
		},
	}); err != nil {
		t.Fatalf("AddModel(%s): %v", harTriedSetGroupID, err)
	}

	// Bench har-b and har-c with same-window (engine-default) rows,
	// har-b armed FIRST so it sorts soonest (µs ordering, wall-clock
	// safe): the initial pick must be the only unbenched member har-a,
	// and the post-walk re-entry offer order is har-b → har-c.
	eng := h.mgr.GroupEngine()
	eng.ExcludeAndReselect(harTriedSetGroupID, "__seed-h3b__", "har-b", 0)
	eng.ExcludeAndReselect(harTriedSetGroupID, "__seed-h3c__", "har-c", 0)

	st := &t1StubState{statuses: map[string]int{
		"har-a": http.StatusServiceUnavailable, // head fails
		"har-b": http.StatusServiceUnavailable, // chain entry fails (attempted ONCE)
		"har-c": http.StatusOK,                 // fresh re-entry member serves
	}}
	h.up.Close()
	h.up = httptest.NewServer(newT1Stub(st))
	if err := h.mgr.UpdateCredential(p5FixtureCredID, models.CredentialConfig{
		ID: p5FixtureCredID, Provider: "openai", APIKey: "key-" + p5FixtureCredID, BaseURL: h.up.URL,
	}); err != nil {
		t.Fatalf("UpdateCredential(%s): %v", p5FixtureCredID, err)
	}
	h.rebuildHandler(t)

	req := makeAnthropicRequestNoAuth(t, map[string]interface{}{
		"model": harTriedSetGroupID,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
		"max_tokens": 16,
	})
	rec := httptest.NewRecorder()
	h.h.HandleAnthropicMessages(rec, req)

	calls, attempted := st.snapshot()
	t.Logf("ITEM3 attempted=%v status=%d", attempted, rec.Code)
	if calls == 0 {
		t.Fatal("VACUOUS: no upstream attempts recorded — intended path not exercised")
	}
	seenModels := map[string]bool{}
	for _, m := range []string{"har-a", "har-b", "har-c"} {
		seenModels[m] = false
	}
	for _, m := range attempted {
		if _, known := seenModels[m]; !known {
			continue // non-member noise (translator probes etc.) — not the seam
		}
		if seenModels[m] {
			t.Fatalf("ITEM 3 RED: member %s attempted TWICE in one request — the chain-attempted member was re-offered as a member head on re-entry (double-run); attempted=%v", m, attempted)
		}
		seenModels[m] = true
	}
	for m, seen := range seenModels {
		if !seen {
			t.Fatalf("shape not exercised: member %s never attempted; attempted=%v", m, attempted)
		}
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("request not served (status=%d); attempted=%v", rec.Code, attempted)
	}
	// The fresh member har-c must be the one that served (the re-entry
	// advanced past the chain-attempted har-b, not onto it).
	if attempted[len(attempted)-1] != "har-c" {
		t.Fatalf("ITEM 3: expected the fresh member har-c to serve last; attempted=%v", attempted)
	}
}
