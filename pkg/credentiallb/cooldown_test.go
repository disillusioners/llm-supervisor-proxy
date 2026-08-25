package credentiallb

import (
	"bytes"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestEngine_Cooldown_ExpiryRestoresSelectability — a credential under
// cooldown becomes selectable again once its cooldownUntil passes
// (fast-forwarded via the test hook — no real sleeps).
func TestEngine_Cooldown_ExpiryRestoresSelectability(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 7, 10*time.Second)
	e.RebindFromStore("m", models.TestRefs("A", "B"))

	// conv1 → A; A 429s → rebinds conv1 to B, A cooling.
	e.InjectPreconditionStateForTest("m", "conv1", "A")
	id, mode := e.ExcludeAndReselect("m", "conv1", "A", 0)
	if mode != ReselectHealthy || id != "B" {
		t.Fatalf("exclude A: got (%s,%v) want (B,ReselectHealthy)", id, mode)
	}
	// While A cools, B is the only healthy pick for new conversations.
	for i := 0; i < 25; i++ {
		if id, _, _ := e.GetOrSelect("m", "fresh"+itoa(i)); id != "B" {
			t.Fatalf("cooling A was selected mid-cooldown: got %s", id)
		}
	}
	// Fast-forward A's cooldown; A is healthy again — excluding B must
	// now fail over BACK to A.
	e.forceCooldownExpiryForTest("m", "A")
	e.InjectPreconditionStateForTest("m", "conv2", "B")
	id, mode = e.ExcludeAndReselect("m", "conv2", "B", 0)
	if mode != ReselectHealthy || id != "A" {
		t.Fatalf("post-expiry reselect: got (%s,%v) want (A,ReselectHealthy)", id, mode)
	}
}

// TestEngine_Cooldown_RetryAfterSeeding — retryAfter > 0 seeds the
// cooldown deadline directly; retryAfter <= 0 falls back to the
// engine's defaultCooldown (the constructor's 4th arg, W8 — not the
// production 60s).
func TestEngine_Cooldown_RetryAfterSeeding(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 7, 750*time.Millisecond)
	e.RebindFromStore("m", models.TestRefs("A", "B"))
	e.InjectPreconditionStateForTest("m", "k1", "A")
	e.InjectPreconditionStateForTest("m", "k2", "B")

	before := time.Now()
	e.ExcludeAndReselect("m", "k1", "A", 5*time.Second)
	e.ExcludeAndReselect("m", "k2", "B", 0) // default path

	untilRA := e.cooldownUntil("m", "A")
	untilDef := e.cooldownUntil("m", "B")
	if untilRA.IsZero() || untilDef.IsZero() {
		t.Fatalf("cooldowns not seeded: A=%v B=%v", untilRA, untilDef)
	}
	if d := untilRA.Sub(before); d < 4900*time.Millisecond || d > 5100*time.Millisecond {
		t.Fatalf("Retry-After seeding off: A cooldown delta %v (want ~5s)", d)
	}
	if d := untilDef.Sub(before); d < 650*time.Millisecond || d > 850*time.Millisecond {
		t.Fatalf("default cooldown off: B cooldown delta %v (want ~750ms)", d)
	}
}

// TestEngine_Cooldown_NeverTouchesBoundAt — R3-4/#10 interplay: a
// cooldown does NOT drop or expire the binding for a conversation
// still pinned to the cooling credential (the binding map entry
// survives with its idle clock intact; only selection skips it).
func TestEngine_Cooldown_NeverTouchesBoundAt(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 7, time.Hour)
	e.RebindFromStore("m", models.TestRefs("A", "B"))
	e.InjectPreconditionStateForTest("m", "k", "A")

	// Mark A cooling via an unrelated conversation's failover.
	e.InjectPreconditionStateForTest("m", "other", "A")
	e.ExcludeAndReselect("m", "other", "A", 0)

	if got := e.Stats()["m"].Bindings; got != 2 {
		t.Fatalf("cooldown dropped/mutated bindings: %d (want 2)", got)
	}
	if !e.cooldownUntil("m", "A").After(time.Now()) {
		t.Fatal("A not cooling")
	}
	// The k→A binding still exists; the next lookup on k sees A
	// cooling and rebinds to healthy B (skip-cooling selection), but
	// the point here is that the cooldown itself never evicted k.
	id, _, _ := e.GetOrSelect("m", "k")
	if id != "B" {
		t.Fatalf("cooling bound credential served: %s", id)
	}
}

// TestEngine_Cooldown_JanitorSweeps — expired cooldown rows are
// removed by the EXISTING janitor pass (no second ticker): the
// Cooldowns gauge returns to 0 after ~2 sweep intervals.
func TestEngine_Cooldown_JanitorSweeps(t *testing.T) {
	e := newTestEngine(t, time.Hour, 25*time.Millisecond, 7, 40*time.Millisecond)
	e.RebindFromStore("m", models.TestRefs("A", "B"))
	e.InjectPreconditionStateForTest("m", "k", "A")
	e.ExcludeAndReselect("m", "k", "A", 0) // A cooling for defaultCooldown=40ms
	if got := e.Stats()["m"].Cooldowns; got != 1 {
		t.Fatalf("gauge before sweep: %d", got)
	}
	time.Sleep(120 * time.Millisecond) // expiry ~40ms; sweeps at 25/50/75/100ms
	if got := e.Stats()["m"].Cooldowns; got != 0 {
		t.Fatalf("janitor did not sweep expired cooldown: gauge=%d", got)
	}
	if !e.cooldownUntil("m", "A").IsZero() {
		t.Fatal("cooldown row survived sweeps")
	}
}

// TestEngine_ExcludeAndReselect_RebindsConversation — the rebind
// re-pins the SAME conversation key: subsequent GetOrSelect calls on
// that key return the new credential as an in-TTL hit.
func TestEngine_ExcludeAndReselect_RebindsConversation(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 7, time.Minute)
	e.RebindFromStore("m", models.TestRefs("A", "B", "C"))
	e.InjectPreconditionStateForTest("m", "conv", "A")

	newID, mode := e.ExcludeAndReselect("m", "conv", "A", 0)
	if mode != ReselectHealthy {
		t.Fatalf("mode: got %v want ReselectHealthy", mode)
	}
	if newID != "B" && newID != "C" {
		t.Fatalf("rebind picked the excluded credential: %s", newID)
	}
	for i := 0; i < 5; i++ {
		id, newly, err := e.GetOrSelect("m", "conv")
		if err != nil {
			t.Fatal(err)
		}
		if id != newID {
			t.Fatalf("conversation not re-pinned: call %d got %s want %s", i, id, newID)
		}
		if newly {
			t.Fatalf("post-rebind hit reported newlyBound (boundAt refresh missed): call %d", i)
		}
	}
	// The rebind counts as a binding write: exactly one binding.
	if got := e.Stats()["m"].Bindings; got != 1 {
		t.Fatalf("bindings after rebind: %d want 1", got)
	}
}

// TestEngine_ExcludeAndReselect_WeightedOrderSkipsCooling — selection
// after an exclusion is weighted random among NON-COOLING
// (renormalized): cooling credentials are never returned while their
// cooldown is active, and the healthy remainder follows weights.
func TestEngine_ExcludeAndReselect_WeightedOrderSkipsCooling(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 11, time.Minute)
	seed3(e, "m") // A=1 B=1 C=2

	// Mark A + B cooling via failovers from throwaway conversations.
	e.InjectPreconditionStateForTest("m", "t1", "A")
	e.ExcludeAndReselect("m", "t1", "A", 0)
	e.InjectPreconditionStateForTest("m", "t2", "B")
	e.ExcludeAndReselect("m", "t2", "B", 0)

	// C is the ONLY healthy credential: every fresh pick is C.
	for i := 0; i < 100; i++ {
		id, _, err := e.GetOrSelect("m", "w"+itoa(i))
		if err != nil {
			t.Fatal(err)
		}
		if id != "C" {
			t.Fatalf("cooling credential selected: pick %d -> %s (want C)", i, id)
		}
	}
	// Symmetric check: only B+C cooling ⇒ always A.
	e2 := newTestEngine(t, time.Hour, time.Hour, 11, time.Minute)
	seed3(e2, "m")
	e2.InjectAllCoolingForTest("m", "A") // cools B and C, leaves A healthy
	for i := 0; i < 50; i++ {
		if id, _, _ := e2.GetOrSelect("m", "x"+itoa(i)); id != "A" {
			t.Fatalf("skip-cooling renormalization broken: got %s want A", id)
		}
	}
}

// TestEngine_ExcludeAndReselect_PreconditionNoOp_B2 — rebind happens
// ONLY when the current binding's credential == excludedCredID; a
// binding already moved off the excluded credential is returned
// UNCHANGED as (currentCred, ReselectHealthy) with NO Failovers tick,
// while the excluded credential is still marked cooling.
func TestEngine_ExcludeAndReselect_PreconditionNoOp_B2(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 7, time.Minute)
	e.RebindFromStore("m", models.TestRefs("A", "B", "C"))
	e.InjectPreconditionStateForTest("m", "conv", "B") // concurrent request already rebinded A→B

	id, mode := e.ExcludeAndReselect("m", "conv", "A", 30*time.Second)
	if mode != ReselectHealthy || id != "B" {
		t.Fatalf("B2 no-op: got (%s,%v) want (B,ReselectHealthy)", id, mode)
	}
	// Binding unchanged: still B, still an in-TTL hit.
	if id2, newly, _ := e.GetOrSelect("m", "conv"); id2 != "B" || newly {
		t.Fatalf("no-op disturbed the binding: id=%s newly=%v", id2, newly)
	}
	// The genuinely-429'd A is still cooling.
	if until := e.cooldownUntil("m", "A"); !until.After(time.Now()) {
		t.Fatal("B2 no-op failed to mark the excluded credential cooling")
	}
	if got := e.Stats()["m"].Failovers; got != 0 {
		t.Fatalf("B2 no-op ticked Failovers: %d", got)
	}
}

// TestEngine_ExcludeAndReselect_ModeMatrix_B3 — all three ReselectMode
// outcomes, driven deterministically via the W8 test seams:
//
//	Healthy        — bind→A, exclude A (typical failover)
//	SoonestExpiry  — ALL credentials cooling with staggered deadlines
//	None           — single-credential model (InjectSingleCredExclusion)
func TestEngine_ExcludeAndReselect_ModeMatrix_B3(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 13, 10*time.Second)
	seed3(e, "m")

	// (a) ReselectHealthy.
	e.InjectPreconditionStateForTest("m", "h", "A")
	id, mode := e.ExcludeAndReselect("m", "h", "A", 0)
	if mode != ReselectHealthy || id == "A" || id == "" {
		t.Fatalf("healthy cell: got (%s,%v)", id, mode)
	}

	// (b) ReselectSoonestExpiry — staggered cooldowns: C=10s (default),
	// A=30s, B=5m. All cooling ⇒ soonest is C.
	e.InjectPreconditionStateForTest("m", "sa", "A")
	e.ExcludeAndReselect("m", "sa", "A", 30*time.Second)
	e.InjectPreconditionStateForTest("m", "sb", "B")
	e.ExcludeAndReselect("m", "sb", "B", 5*time.Minute)
	e.InjectAllCoolingForTest("m", "A", "B") // C cools with defaultCooldown=10s
	id, mode = e.ExcludeAndReselect("m", "sx", "C", 0)
	if mode != ReselectSoonestExpiry || id != "C" {
		t.Fatalf("soonest-expiry cell: got (%s,%v) want (C,ReselectSoonestExpiry)", id, mode)
	}

	// (c) ReselectNone — single-credential model.
	e2 := newTestEngine(t, time.Hour, time.Hour, 13, 10*time.Second)
	e2.RebindFromStore("solo", models.TestRefs("only"))
	e2.InjectSingleCredExclusionForTest("solo")
	id, mode = e2.ExcludeAndReselect("solo", "n", "only", 0)
	if mode != ReselectNone || id != "" {
		t.Fatalf("none cell: got (%s,%v) want (\"\",ReselectNone)", id, mode)
	}

	// Enum rendering (logs / failure messages).
	if ReselectHealthy.String() != "ReselectHealthy" ||
		ReselectSoonestExpiry.String() != "ReselectSoonestExpiry" ||
		ReselectNone.String() != "ReselectNone" {
		t.Fatalf("mode strings drifted: %s %s %s", ReselectHealthy, ReselectSoonestExpiry, ReselectNone)
	}
}

// TestEngine_ExcludeAndReselect_ReselectSoonestExpiry_WarnEmitted —
// the all-cooling path emits the [LB-FAILOVER] WARN naming the
// soonest-expiring credential, and returns it with
// mode=ReselectSoonestExpiry.
func TestEngine_ExcludeAndReselect_ReselectSoonestExpiry_WarnEmitted(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 13, 10*time.Second)
	seed3(e, "m")

	// Stagger: C=10s (soonest), A=30s, B=5m.
	e.InjectPreconditionStateForTest("m", "sa", "A")
	e.ExcludeAndReselect("m", "sa", "A", 30*time.Second)
	e.InjectPreconditionStateForTest("m", "sb", "B")
	e.ExcludeAndReselect("m", "sb", "B", 5*time.Minute)
	e.InjectAllCoolingForTest("m", "A", "B")

	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	id, mode := e.ExcludeAndReselect("m", "final", "C", 0)
	log.SetOutput(prev)

	if mode != ReselectSoonestExpiry || id != "C" {
		t.Fatalf("soonest pick: got (%s,%v) want (C,ReselectSoonestExpiry)", id, mode)
	}
	out := buf.String()
	if !bytes.Contains(buf.Bytes(), []byte("[LB-FAILOVER]")) ||
		!bytes.Contains(buf.Bytes(), []byte("all credentials cooling")) ||
		!bytes.Contains(buf.Bytes(), []byte("cred=C")) {
		t.Fatalf("WARN shape wrong; log was: %q", out)
	}
	// Selection-only: no binding write for the soonest pick.
	if got := e.Stats()["m"].Bindings; got != 2 { // sa, sb primed bindings only
		t.Fatalf("soonest-expiry path wrote a binding: %d", got)
	}
}

// TestEngine_ExcludeAndReselect_ReselectNone_NoFallThroughToEngine —
// single-credential models return ("", ReselectNone) WITHOUT any
// engine state mutation: no cooldown row is written and the existing
// binding stays put (the caller routes to model-fallback; the engine
// is not consulted further).
func TestEngine_ExcludeAndReselect_ReselectNone_NoFallThroughToEngine(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 7, time.Minute)
	e.RebindFromStore("solo", models.TestRefs("only"))
	e.InjectPreconditionStateForTest("solo", "conv", "only")

	id, mode := e.ExcludeAndReselect("solo", "conv", "only", 0)
	if mode != ReselectNone || id != "" {
		t.Fatalf("single-cred exclusion: got (%s,%v) want (\"\",ReselectNone)", id, mode)
	}
	if until := e.cooldownUntil("solo", "only"); !until.IsZero() {
		t.Fatalf("ReselectNone wrote a cooldown row: %v", until)
	}
	if got := e.Stats()["solo"].Bindings; got != 1 {
		t.Fatalf("ReselectNone disturbed the binding: %d", got)
	}
	if id2, _, _ := e.GetOrSelect("solo", "conv"); id2 != "only" {
		t.Fatalf("binding drifted after None: %s", id2)
	}
	// Zero-configured model is also None (genuinely no candidate).
	if id3, mode3 := e.ExcludeAndReselect("ghost", "k", "x", 0); id3 != "" || mode3 != ReselectNone {
		t.Fatalf("unknown model: got (%s,%v)", id3, mode3)
	}
}

// TestEngine_AllCooling_SoonestExpiry_SinglePick — when EVERY
// credential is cooling, GetOrSelect returns the SOONEST-expiring one
// (availability beats strict cooldown) with NO binding stored and the
// WARN emitted; once that credential's cooldown lapses, normal
// skip-cooling selection resumes on it.
func TestEngine_AllCooling_SoonestExpiry_SinglePick(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 13, 10*time.Second)
	seed3(e, "m")

	// C = soonest (10s default), A = 30s, B = 5m.
	e.InjectPreconditionStateForTest("m", "sa", "A")
	e.ExcludeAndReselect("m", "sa", "A", 30*time.Second)
	e.InjectPreconditionStateForTest("m", "sb", "B")
	e.ExcludeAndReselect("m", "sb", "B", 5*time.Minute)
	e.InjectAllCoolingForTest("m", "A", "B")

	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	id, newly, err := e.GetOrSelect("m", "fresh-conv")
	log.SetOutput(prev)

	if err != nil || id != "C" {
		t.Fatalf("all-cooling pick: id=%s err=%v want C", id, err)
	}
	if newly {
		t.Fatal("all-cooling soonest pick must not store a binding (newlyBound=false)")
	}
	if got := e.Stats()["m"].Bindings; got != 2 { // sa, sb only
		t.Fatalf("soonest pick stored a binding: %d", got)
	}
	if !bytes.Contains(buf.Bytes(), []byte("all credentials cooling")) {
		t.Fatalf("WARN missing; log: %q", buf.String())
	}

	// C's cooldown lapses → C is the only healthy credential: normal
	// skip-cooling selection resumes (and now binds, since the key is
	// non-empty and C is healthy).
	e.forceCooldownExpiryForTest("m", "C")
	id2, newly2, _ := e.GetOrSelect("m", "fresh-conv2")
	if id2 != "C" || !newly2 {
		t.Fatalf("post-expiry resume: id=%s newly=%v want C/true", id2, newly2)
	}
}

// TestEngine_Cooldown_ConcurrentWritesAndSelections — Task 11
// acceptance: concurrent cooldown writes + selections are -race clean.
func TestEngine_Cooldown_ConcurrentWritesAndSelections(t *testing.T) {
	e := newTestEngine(t, time.Hour, 5*time.Millisecond, 7, 50*time.Millisecond)
	seed3(e, "m")
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				k := "r" + itoa(g) + "-" + itoa(i%20)
				switch i % 3 {
				case 0:
					e.ExcludeAndReselect("m", k, []string{"A", "B", "C"}[g%3], time.Duration(i%5)*time.Second)
				case 1:
					e.GetOrSelect("m", k)
				default:
					e.Stats()
				}
			}
		}(g)
	}
	wg.Wait()
}
