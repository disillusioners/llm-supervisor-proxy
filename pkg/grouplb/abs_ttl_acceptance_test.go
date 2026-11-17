package grouplb

import (
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
)

// TestEngine_BindingExpiresDespiteContinuousHits_DeletesEntry proves the
// absolute lifetime: even with continuous in-window hits the binding
// expires at createdAt+ttl, the expired row is DELETED on read
// (not merely ignored), and a fresh bind replaces it (count stays 1).
func TestEngine_BindingExpiresDespiteContinuousHits_DeletesEntry(t *testing.T) {
	const ttl = 120 * time.Millisecond
	e := newTestEngine(t, ttl, time.Hour)
	e.OnGroupChanged("g1", refs2())

	m0, newly0, err := e.GetOrSelect("g1", "conv-key")
	if err != nil || !newly0 {
		t.Fatalf("first bind: m=%s newly=%v err=%v", m0, newly0, err)
	}
	orig, ok := e.bindingCreatedAtForTest("g1", "conv-key")
	if !ok {
		t.Fatal("binding missing after first bind")
	}

	// Hammer across a window longer than the TTL. Sleeps are
	// unavoidable: this engine has no clock seam, so we rely on real
	// time. 20ms cadence keeps the test fast while still producing
	// multiple in-window observations.
	member := m0
	rebindSeen := false
	deadline := time.Now().Add(2 * ttl)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		m, newly, err := e.GetOrSelect("g1", "conv-key")
		if err != nil {
			t.Fatalf("hit: %v", err)
		}
		if time.Since(orig) < ttl {
			// Inside the original lifetime window: no rebind, same member.
			if newly {
				t.Fatalf("rebind inside original window at %v", time.Since(orig))
			}
			if m != member {
				t.Fatalf("member changed inside window: %s vs %s", m, member)
			}
		} else if newly {
			rebindSeen = true
			member = m
		}
	}
	if !rebindSeen {
		t.Fatal("no rebind after expiry — TTL did not fire despite continuous hits")
	}
	if got := e.bindingsCountForTest("g1"); got != 1 {
		t.Fatalf("bindingsCount=%d want 1 (delete-then-insert, no second row)", got)
	}
	ts, ok := e.bindingCreatedAtForTest("g1", "conv-key")
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
	row, present := e.bindingCreatedAtForTest("g1", "conv-key")
	if !present {
		t.Fatal("binding row missing after rebind")
	}
	if !row.After(orig) {
		t.Fatalf("stale row survived the rebind: createdAt=%v (original %v)", row, orig)
	}

	// FALLBACK for the missing len(map) accessor (this layer exposes no
	// map-length hook): a SECOND read must be a HIT inside the fresh
	// window and the count must stay 1 — the rebound row REPLACES the
	// expired one instead of accumulating a second row.
	m2, newly2, err := e.GetOrSelect("g1", "conv-key")
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if newly2 {
		t.Fatal("second read must be a HIT inside the fresh window (not a re-bind)")
	}
	if m2 != member {
		t.Fatalf("second read member = %q, want %q", m2, member)
	}
	if got := e.bindingsCountForTest("g1"); got != 1 {
		t.Fatalf("bindingsCount=%d want 1 after the second read (no accumulation)", got)
	}

	// DISCRIMINATING stale-row proof — catches a mirror-only decrement
	// (decrement the count but keep the row): with EVERY member cooling
	// no re-bind can happen, so the expired row must be GONE from the
	// map, not merely uncounted. The row accessor is the direct probe.
	// A dedicated engine with a wide cooldown (5s) keeps the all-cooling
	// window from lapsing between the injection and the read.
	stale := NewEngine(ttl, time.Hour, 42, 5*time.Second)
	t.Cleanup(stale.Stop)
	stale.OnGroupChanged("g1", refs2())
	stale.InjectPreconditionStateForTest("g1", "conv-stale", "member-a")
	// Sleep: this engine has no clock seam, so expiry needs real time;
	// 30ms of margin over the 120ms TTL. The assertion is "expired",
	// which tolerates being late, never early.
	time.Sleep(ttl + 30*time.Millisecond)
	stale.InjectAllCoolingForTest("g1")
	if _, _, err := stale.GetOrSelect("g1", "conv-stale"); err != nil {
		t.Fatalf("all-cooling pick: %v", err)
	}
	if _, present := stale.bindingCreatedAtForTest("g1", "conv-stale"); present {
		t.Fatal("expired row survived the read — the binding was not deleted (only the counter moved)")
	}
	if got := stale.bindingsCountForTest("g1"); got != 0 {
		t.Fatalf("bindingsCount=%d want 0 after the expiring read", got)
	}
}

// TestEngine_BindingExpiry_ReturnsToPreferredDefaultMember is the
// acceptance-(c) probe promoted to a permanent test: a conversation
// pinned to a BACKUP-pool member (the agentic-Main / coding4 shape)
// falls back to the DEFAULT member once the binding's absolute
// lifetime expires — and that fallback is itself a working affinity.
//
// Shape: ONE default member (member-a) + ONE backup member (backup-b).
// backup-b carries a POSITIVE weight on purpose: newWeightedSelector
// returns nil for weight <= 0, so a zero-weight backup member would
// have no selector at all.
func TestEngine_BindingExpiry_ReturnsToPreferredDefaultMember(t *testing.T) {
	const ttl = 120 * time.Millisecond
	e := newTestEngine(t, ttl, time.Hour)
	e.OnGroupChanged("g1", []MemberRef{
		{ModelID: "member-a", Weight: 1, Position: 0},
		{ModelID: "backup-b", Weight: 1, Position: 1, BackupOnly: true},
	})

	// Plant the affinity on the BACKUP member.
	e.InjectPreconditionStateForTest("g1", "conv-pin", "backup-b")

	// Sleep: this engine has no clock seam, so expiry needs real time.
	// 30ms of margin over the 120ms TTL; the assertion is "expired",
	// which tolerates being late, never early.
	time.Sleep(ttl + 30*time.Millisecond)

	// (i) The expired binding is dropped and the DEFAULT pool's own
	// selection runs: member-a, bound fresh.
	m, newly, err := e.GetOrSelect("g1", "conv-pin")
	if err != nil {
		t.Fatalf("post-expiry pick: %v", err)
	}
	if m != "member-a" {
		t.Fatalf("post-expiry pick = %q, want member-a (default logic resumed)", m)
	}
	if !newly {
		t.Fatal("post-expiry pick must be a NEW bind (the expired row was dropped)")
	}

	// (ii) The fresh window serves the affinity: same member, not newly
	// bound.
	m2, newly2, err := e.GetOrSelect("g1", "conv-pin")
	if err != nil {
		t.Fatalf("follow-up hit: %v", err)
	}
	if m2 != "member-a" {
		t.Fatalf("follow-up hit = %q, want member-a", m2)
	}
	if newly2 {
		t.Fatal("follow-up hit must not re-bind inside the fresh window")
	}

	// (iii) Exactly one row: the expired entry was deleted, not
	// accumulated.
	if got := e.bindingsCountForTest("g1"); got != 1 {
		t.Fatalf("bindingsCount=%d want 1 (old entry gone, not accumulating)", got)
	}
}

// TestEngine_BindingExpiry_BackupPool_DeletesEntry — backup-pool twin.
func TestEngine_BindingExpiry_BackupPool_DeletesEntry(t *testing.T) {
	const ttl = 120 * time.Millisecond
	e := newTestEngine(t, ttl, time.Hour)
	e.OnGroupChanged("g", []MemberRef{
		{ModelID: "default-a", Weight: 1, Position: 0},
		{ModelID: "backup-x", Weight: 1, Position: 1, BackupOnly: true},
	})

	m0, newly0, err := e.PickBackupMember("g", "conv-backup")
	if err != nil || !newly0 {
		t.Fatalf("first backup bind: m=%s newly=%v err=%v", m0, newly0, err)
	}

	rebindSeen := false
	deadline := time.Now().Add(2 * ttl)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		_, newly, err := e.PickBackupMember("g", "conv-backup")
		if err != nil {
			t.Fatalf("backup hit: %v", err)
		}
		if newly {
			rebindSeen = true
		}
	}
	if !rebindSeen {
		t.Fatal("no backup rebind after expiry — TTL did not fire despite continuous hits")
	}
	if got := e.backupBindingsCountForTest("g"); got != 1 {
		t.Fatalf("backupBindingsCount=%d want 1 (delete-then-insert)", got)
	}

	// Row-GONE proof (not the TTL discrimination itself — that is
	// carried by rebindSeen above): with every backup member COOLING no
	// re-bind can happen, so if the read still removes the expired row
	// the map must be EMPTY (count 0). An ignore-in-place implementation
	// would leave the row behind (count 1) — this assertion turns
	// delete-on-read into an observable contract even where it is
	// triggered by the pre-existing cooling path.
	e.InjectAllCoolingForTest("g")
	// A pick may still be returned by the soonest-expiry path; the
	// assertion below is what matters.
	_, _, _ = e.PickBackupMember("g", "conv-backup")
	if got := e.backupBindingsCountForTest("g"); got != 0 {
		t.Fatalf("backupBindingsCount=%d want 0 — the expired row was not deleted on read", got)
	}
}

// TestEngine_SetBindingTTL_HonouredAndNonPositiveIgnored proves the knob:
//   - a short TTL set through the setter really governs expiry (the
//     30m default would still be alive after 120ms — expiry at 120ms
//     proves the knob is live);
//   - non-positive input is IGNORED (the current TTL survives);
//   - out-of-range values clamp: sub-millisecond noise → 1m floor,
//     48h → 24h ceiling;
//   - nil-receiver calls do not panic.
func TestEngine_SetBindingTTL_HonouredAndNonPositiveIgnored(t *testing.T) {
	e := newTestEngine(t, 30*time.Minute, time.Hour)
	e.OnGroupChanged("g1", refs2())

	// The knob governs expiry: 120ms TTL → expired well before 30m.
	e.SetBindingTTL(120 * time.Millisecond)
	if e.BindingTTL() != 120*time.Millisecond {
		t.Fatalf("BindingTTL=%v want 120ms", e.BindingTTL())
	}
	if _, _, err := e.GetOrSelect("g1", "conv-knob"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond) // just past the 120ms knob
	if _, newly, _ := e.GetOrSelect("g1", "conv-knob"); !newly {
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

// TestEngine_BindingTTL_IndependenceFromCredentialLayer
func TestEngine_BindingTTL_IndependenceFromCredentialLayer(t *testing.T) {
	// grouplb engine
	ge := newTestEngine(t, 30*time.Minute, time.Hour)
	// credentiallb engine
	ce := credentiallb.NewEngine(30*time.Minute, time.Hour, 7, time.Minute)
	t.Cleanup(ce.Stop)
	ge.SetBindingTTL(5 * time.Minute)
	if ce.BindingTTL() != 30*time.Minute {
		t.Fatalf("credential engine TTL changed: %v", ce.BindingTTL())
	}
	ce.SetBindingTTL(7 * time.Minute)
	if ge.BindingTTL() != 5*time.Minute {
		t.Fatalf("group engine TTL changed: %v", ge.BindingTTL())
	}
}
