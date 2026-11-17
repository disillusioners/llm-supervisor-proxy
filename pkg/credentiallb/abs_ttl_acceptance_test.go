package credentiallb

import (
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestEngine_BindingExpiresDespiteContinuousHits_DeletesEntry proves the
// absolute lifetime in the credential layer: continuous hits do not
// extend createdAt; the expired row is deleted on read and replaced;
// the Bindings stat never reaches 2.
func TestEngine_BindingExpiresDespiteContinuousHits_DeletesEntry(t *testing.T) {
	const ttl = 120 * time.Millisecond
	e := newTestEngine(t, ttl, time.Hour, 7, time.Minute)
	e.RebindFromStore("m", models.TestRefs("A", "B", "C"))

	id0, newly0, err := e.GetOrSelect("m", "conv-key")
	if err != nil || !newly0 {
		t.Fatalf("first bind: id=%s newly=%v err=%v", id0, newly0, err)
	}
	orig, ok := e.bindingCreatedAtForTest("m", "conv-key")
	if !ok {
		t.Fatal("binding missing after first bind")
	}

	maxBindings := uint64(0)
	rebindSeen := false
	deadline := time.Now().Add(2 * ttl)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		id, newly, err := e.GetOrSelect("m", "conv-key")
		if err != nil {
			t.Fatalf("hit: %v", err)
		}
		if b := e.Stats()["m"].Bindings; b > maxBindings {
			maxBindings = b
		}
		if time.Since(orig) < ttl {
			if newly {
				t.Fatalf("rebind inside original window at %v", time.Since(orig))
			}
			if id != id0 {
				// credential may change only after rebind; inside window it must stay
				t.Fatalf("id changed inside window: %s vs %s", id, id0)
			}
		} else if newly {
			rebindSeen = true
		}
	}
	if !rebindSeen {
		t.Fatal("no rebind after expiry — TTL did not fire despite continuous hits")
	}
	if maxBindings > 1 {
		t.Fatalf("Bindings stat reached %d (delete did not happen before insert)", maxBindings)
	}
	if got := e.Stats()["m"].Bindings; got != 1 {
		t.Fatalf("Bindings=%d want 1", got)
	}
	ts, ok := e.bindingCreatedAtForTest("m", "conv-key")
	if !ok {
		t.Fatal("binding missing after rebind")
	}
	if !ts.After(orig) {
		t.Fatalf("createdAt not advanced after rebind: %v vs %v", ts, orig)
	}

	// ROW-LEVEL stale-row probe (the row accessor that ALREADY exists —
	// bindingCreatedAtForTest returns the row's createdAt and whether the
	// row exists at all): the expired row was REPLACED by a fresh one,
	// not left behind with its original stamp.
	row, present := e.bindingCreatedAtForTest("m", "conv-key")
	if !present {
		t.Fatal("binding row missing after rebind")
	}
	if !row.After(orig) {
		t.Fatalf("stale row survived the rebind: createdAt=%v (original %v)", row, orig)
	}

	// FALLBACK for the missing len(map) accessor (this layer exposes no
	// map-length hook): a SECOND read must be a HIT inside the fresh
	// window and the Bindings mirror must stay at 1 — the rebound row
	// REPLACES the expired one instead of accumulating a second row.
	id2, newly2, err := e.GetOrSelect("m", "conv-key")
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if newly2 {
		t.Fatal("second read must be a HIT inside the fresh window (not a re-bind)")
	}
	if got := e.Stats()["m"].Bindings; got != 1 {
		t.Fatalf("Bindings=%d want 1 after the second read (no accumulation)", got)
	}
	_ = id2

	// DISCRIMINATING stale-row proof — catches a mirror-only decrement
	// (decrement the count but keep the row): with EVERY credential
	// cooling no re-bind can happen, so the expired row must be GONE
	// from the map, not merely uncounted. The row accessor is the
	// direct probe. A dedicated engine with a wide cooldown keeps the
	// all-cooling window from lapsing between the injection and the read.
	stale := newTestEngine(t, ttl, time.Hour, 7, 5*time.Second)
	stale.RebindFromStore("m", models.TestRefs("A", "B", "C"))
	stale.InjectPreconditionStateForTest("m", "conv-stale", "A")
	// Sleep: this engine has no clock seam, so expiry needs real time;
	// 30ms of margin over the 120ms TTL. The assertion is "expired",
	// which tolerates being late, never early.
	time.Sleep(ttl + 30*time.Millisecond)
	stale.InjectAllCoolingForTest("m")
	if _, _, err := stale.GetOrSelect("m", "conv-stale"); err != nil {
		t.Fatalf("all-cooling pick: %v", err)
	}
	if _, present := stale.bindingCreatedAtForTest("m", "conv-stale"); present {
		t.Fatal("expired row survived the read — the binding was not deleted (only the counter moved)")
	}
	if got := stale.Stats()["m"].Bindings; got != 0 {
		t.Fatalf("Bindings=%d want 0 after the expiring read", got)
	}
}

// TestEngine_SetBindingTTL_HonouredAndNonPositiveIgnored proves the knob:
// short TTL honoured, non-positive ignored, out-of-range clamped,
// nil-receiver safe.
func TestEngine_SetBindingTTL_HonouredAndNonPositiveIgnored(t *testing.T) {
	e := newTestEngine(t, 30*time.Minute, time.Hour, 7, time.Minute)
	e.RebindFromStore("m", models.TestRefs("A", "B"))

	// The knob governs expiry: 120ms TTL → expired well before 30m.
	e.SetBindingTTL(120 * time.Millisecond)
	if e.BindingTTL() != 120*time.Millisecond {
		t.Fatalf("BindingTTL=%v want 120ms", e.BindingTTL())
	}
	if _, _, err := e.GetOrSelect("m", "conv-knob"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond) // just past the 120ms knob
	if _, newly, _ := e.GetOrSelect("m", "conv-knob"); !newly {
		t.Fatal("binding should have expired under the 120ms knob (30m default would still be alive)")
	}

	// Non-positive ignored (keeps the current TTL).
	prior := e.BindingTTL()
	e.SetBindingTTL(0)
	if e.BindingTTL() != prior {
		t.Fatalf("zero changed TTL: %v vs %v", e.BindingTTL(), prior)
	}
	e.SetBindingTTL(-time.Second)
	if e.BindingTTL() != prior {
		t.Fatalf("negative changed TTL: %v vs %v", e.BindingTTL(), prior)
	}

	// Clamp floor / ceiling.
	e.SetBindingTTL(time.Nanosecond)
	if e.BindingTTL() != time.Minute {
		t.Fatalf("floor clamp: got %v want 1m", e.BindingTTL())
	}
	e.SetBindingTTL(48 * time.Hour)
	if e.BindingTTL() != 24*time.Hour {
		t.Fatalf("ceiling clamp: got %v want 24h", e.BindingTTL())
	}

	// Nil receiver safe.
	var nilE *Engine
	nilE.SetBindingTTL(time.Minute)
	if nilE.BindingTTL() != DefaultBindingTTL {
		t.Fatalf("nil BindingTTL=%v", nilE.BindingTTL())
	}
}
