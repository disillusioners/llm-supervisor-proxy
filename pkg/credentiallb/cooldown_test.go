package credentiallb

import (
	"bytes"
	"log"
	"sync"
	"sync/atomic"
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

// TestEngine_GetOrSelect_TOCTOU_CoolingReverify — Item 1 (pre-merge
// gate): the post-lock-upgrade re-verify predicate (engine.go
// happy-path lock-upgrade branch) must include a just-started cooldown
// check. A concurrent ExcludeAndReselect that marks the bound
// credential cooling in the RLock→Lock gap must cause the optimistic
// read to fall through to the full path; GetOrSelect never returns a
// cooling credential once the cooldown is observable. Stays -race
// clean.
//
// Interleaved shape: 100 readers hammer GetOrSelect(convKey) in a
// loop, all bound to A; ONE writer (running on a separate goroutine,
// on a DIFFERENT conversation key "other-conv") calls
// ExcludeAndReselect to mark A cooling mid-stream. Because
// "other-conv" does not overlap any reader key, the writer's
// exclusion marks A cooling WITHOUT rebinding the readers' keys —
// the same shape the production race takes when "a sibling
// conversation 429s A while ours is bound to A". The leader-ruled
// race window (RLock observes binding=A → cooldown seeded in the
// gap → Lock-upgrade re-verify must observe the cooldown) is
// exercised dynamically because the writer lands while readers are
// actively mid-flight.
//
// Per-iteration flag check: every reader loop checks writerDoneFlag
// atomically — returns BEFORE flag-set may be A (legitimate, no
// cooldown yet); returns AFTER flag-set must NOT be A. The aggregate
// post-flag read count also must be > 0, otherwise the writer
// finished before any reader observed the cooldown and the interleave
// was never exercised (a -race-only timing failure on a too-fast
// runner).
//
// Bounded runtime: ~100ms total window, then close(stop). No runaway
// loops.
func TestEngine_GetOrSelect_TOCTOU_CoolingReverify(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 11, 10*time.Second)
	seed3(e, "m") // A=1 B=1 C=2

	// Pre-bind 100 conversations to a deterministic credential (A) so
	// every reader has the same cached hit-credential under the RLock.
	const N = 100
	keys := make([]string, N)
	for i := 0; i < N; i++ {
		keys[i] = "conv-" + itoa(i)
		e.InjectPreconditionStateForTest("m", keys[i], "A")
	}
	// Sanity: the optimistic RLock reads see A as the bound cred.
	if id, _, err := e.GetOrSelect("m", "conv-0"); err != nil || id != "A" {
		t.Fatalf("baseline: id=%s err=%v want A", id, err)
	}

	stop := make(chan struct{})
	var writerDoneFlag int64 // atomic: 0 = not yet, 1 = writer finished seeding the cooldown
	var postFlagReads int64  // aggregate count of reader returns AFTER writerDoneFlag=1
	var postFlagSawA int64   // aggregate count of post-flag returns that violated the contract

	var wg sync.WaitGroup
	wg.Add(N + 1)

	for _, k := range keys {
		go func(k string) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				id, _, err := e.GetOrSelect("m", k)
				if err != nil {
					t.Errorf("GetOrSelect err for %s: %v", k, err)
					return
				}
				if id == "" {
					t.Errorf("empty credential returned for %s", k)
					return
				}
				if atomic.LoadInt64(&writerDoneFlag) == 0 {
					// Pre-flag — A is legitimate here (no cooldown
					// has been seeded yet by the writer).
					continue
				}
				// Post-flag: A is FORBIDDEN. The predicate fix must
				// have caused the optimistic read to fall through to
				// the full path and pick a healthy reselect (B or C).
				atomic.AddInt64(&postFlagReads, 1)
				if id == "A" {
					atomic.AddInt64(&postFlagSawA, 1)
					t.Errorf("TOCTOU cooling leak: post-flag served A for %s", k)
				}
			}
		}(k)
	}

	// Tiny head start so readers are actively mid-flight when the
	// writer lands — this is what creates the genuine
	//   RLock observes binding=A → cooldown seeded in the gap →
	//   Lock-upgrade re-verify must observe the cooldown
	// window the leader ruling pins. Without the head start the
	// writer could land during the readers' scheduling gap and the
	// race would never be exercised.
	time.Sleep(2 * time.Millisecond)

	// WRITER: ExcludeAndReselect on a DIFFERENT conversation key.
	// "other-conv" does not overlap any reader key, so this marks A
	// cooling WITHOUT touching the readers' bindings. Once the call
	// returns, the cooldown is observable to any subsequent reader
	// iteration — flip writerDoneFlag so readers switch from
	// pre-flag to post-flag accounting.
	go func() {
		defer wg.Done()
		e.ExcludeAndReselect("m", "other-conv", "A", 30*time.Second)
		atomic.StoreInt64(&writerDoneFlag, 1)
	}()

	// Bounded runtime: 100ms is enough on any runner to span many
	// reader iterations before the writer's flag flips and many more
	// post-flag reads.
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()

	// (a) Every post-flag return was non-empty and never A.
	if postFlagSawA != 0 {
		t.Fatalf("TOCTOU cooling leak: %d post-flag reads served A", postFlagSawA)
	}
	// (a.2) The interleave was actually exercised — the writer landed
	// while at least one reader iteration observed the cooldown.
	// Without this guard the test would pass on a too-fast runner
	// even with the predicate bug present (no reader iteration ever
	// hit the post-flag path).
	if postFlagReads == 0 {
		t.Fatalf("no post-flag reads — writer finished before any reader observed the cooldown; interleave not exercised")
	}

	// (c) Every binding is now healthy (B or C); A is gone from every
	// conversation (the final sequential GetOrSelect runs after
	// writerDoneFlag=1 so the same predicate applies).
	for _, k := range keys {
		id, _, err := e.GetOrSelect("m", k)
		if err != nil {
			t.Fatal(err)
		}
		if id == "A" {
			t.Fatalf("binding %s still on cooling A: %s", k, id)
		}
	}
	// Gauge: A is the only cooling credential; once janitor sweeps
	// the test-cooldown the gauge goes to 0 (we don't sleep here —
	// the contract is just "exactly one cooling row").
	if got := e.Stats()["m"].Cooldowns; got != 1 {
		t.Fatalf("cooldown gauge after race: %d want 1", got)
	}
}

// TestEngine_ExcludeAndReselect_B2_IndependentCoolingGuard — Item 2
// (leader-ruled SEMANTICS CHANGE): the B2 precondition no-op only
// holds when the current binding's credential is HEALTHY. When the
// bound credential is INDEPENDENTLY cooling (a separate
// ExcludeAndReselect marked it cooling on a different conversation),
// the no-op does NOT apply — we fall through to the healthy reselect
// path, returning a healthy credential with mode=ReselectHealthy AND
// rebinding the conversation to it (subsequent GetOrSelect on the
// same key returns the new credential as an in-TTL hit).
//
// Constraint: the EXISTING B2 test
// (TestEngine_ExcludeAndReselect_PreconditionNoOp_B2) still passes
// unchanged — the new guard is a STRICTLY TIGHTER no-op (it only
// refuses to no-op when the bound cred is independently cooling).
func TestEngine_ExcludeAndReselect_B2_IndependentCoolingGuard(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 17, 10*time.Second)
	seed3(e, "m") // A=1 B=1 C=2

	// Setup: conversation "conv" is bound to B (concurrent request
	// rebinded away from A). B is INDEPENDENTLY cooling (a separate
	// ExcludeAndReselect on "other" marked B cooling).
	e.InjectPreconditionStateForTest("m", "conv", "B")
	e.InjectPreconditionStateForTest("m", "other", "B")
	e.ExcludeAndReselect("m", "other", "B", 0) // B is now cooling
	if got := e.Stats()["m"].Cooldowns; got != 1 {
		t.Fatalf("preconditions: cooldown gauge %d want 1", got)
	}

	// Excluded = A (not equal to bound B). Pre-Item-2: returns B
	// (the no-op). Post-Item-2: B is independently cooling, so we
	// fall through to the healthy reselect — A is cooling (we just
	// marked it), C is healthy. The result MUST be C with
	// mode=ReselectHealthy, AND the conversation must be REBOUND to
	// C (subsequent GetOrSelect on "conv" returns C as an in-TTL
	// hit, not B).
	preFailovers := e.Stats()["m"].Failovers
	id, mode := e.ExcludeAndReselect("m", "conv", "A", 0)
	if mode != ReselectHealthy {
		t.Fatalf("mode: got %v want ReselectHealthy", mode)
	}
	if id != "C" {
		t.Fatalf("reselect: got %s want C (A is excluded, B is independently cooling)", id)
	}
	// Failovers ticked — the new guard forces a REAL rebind, not a
	// no-op.
	if got := e.Stats()["m"].Failovers; got != preFailovers+1 {
		t.Fatalf("Failovers: got %d want %d (real rebind should tick)", got, preFailovers+1)
	}
	// Conversation rebound to C: subsequent GetOrSelect returns C
	// as an in-TTL hit (newlyBound=false).
	if id2, newly, err := e.GetOrSelect("m", "conv"); err != nil || id2 != "C" || newly {
		t.Fatalf("post-guard rebind: id=%s newly=%v err=%v want C/false", id2, newly, err)
	}
	// Cooldown gauge: A (just excluded) + B (still cooling) = 2.
	if got := e.Stats()["m"].Cooldowns; got != 2 {
		t.Fatalf("cooldown gauge post-guard: %d want 2", got)
	}
	// The pre-existing B2 path (B2 no-op, bound NOT cooling) must
	// STILL pass unchanged. Cross-check by binding to A on a
	// different conv that is NOT excluded and NOT cooling — A's
	// bound conv is A, we exclude a different cred, the no-op
	// applies.
	e2 := newTestEngine(t, time.Hour, time.Hour, 17, time.Minute)
	e2.RebindFromStore("m2", models.TestRefs("X", "Y", "Z"))
	e2.InjectPreconditionStateForTest("m2", "k", "Y")
	preFailovers2 := e2.Stats()["m2"].Failovers
	id2, mode2 := e2.ExcludeAndReselect("m2", "k", "X", 0)
	if mode2 != ReselectHealthy || id2 != "Y" {
		t.Fatalf("legacy B2 no-op shape: got (%s,%v) want (Y,ReselectHealthy)", id2, mode2)
	}
	if got := e2.Stats()["m2"].Failovers; got != preFailovers2 {
		t.Fatalf("legacy B2 no-op ticked Failovers: %d want %d", got, preFailovers2)
	}

	// 2-cred degradation sub-case — the doc comment of this test
	// predicts that when the B2 guard falls through AND no healthy
	// alternative exists, the call MUST degrade to
	// ReselectSoonestExpiry (NOT ReselectHealthy). Fixture: 2 creds
	// (X, Y); X is bound + independently cooling with a LONGER
	// retryAfter; Y is excluded-and-cooling via THIS call with the
	// engine's shorter defaultCooldown. Soonest-expiry = Y (its
	// shorter remaining cooldown beats X's).
	e3 := newTestEngine(t, time.Hour, time.Hour, 19, 200*time.Millisecond)
	e3.RebindFromStore("m3", models.TestRefs("X", "Y"))
	e3.InjectPreconditionStateForTest("m3", "conv", "X")
	e3.InjectPreconditionStateForTest("m3", "other", "X")
	e3.ExcludeAndReselect("m3", "other", "X", 30*time.Second) // X cooling for 30s
	preFailovers3 := e3.Stats()["m3"].Failovers
	id3, mode3 := e3.ExcludeAndReselect("m3", "conv", "Y", 0)
	if mode3 != ReselectSoonestExpiry {
		t.Fatalf("2-cred degradation: got mode=%v want ReselectSoonestExpiry", mode3)
	}
	if id3 != "Y" {
		t.Fatalf("soonest-expiry pick: got %s want Y (200ms default < X's 30s remaining)", id3)
	}
	// SoonestExpiry is selection-only — Failovers does NOT tick.
	if got := e3.Stats()["m3"].Failovers; got != preFailovers3 {
		t.Fatalf("SoonestExpiry ticked Failovers: got %d want %d", got, preFailovers3)
	}
}

// TestEngine_SkipCooling_RenormalizedDistribution — Item 5a: the
// skip-cooling renormalized pick follows weights AMONG SURVIVORS.
// Fixture: A=1, B=1, C=2 with A cooling → survivors B:C with weights
// 1:2. Over N samples with a fixed seed, the empirical B/(B+C) ratio
// must land in the principled band.
//
// N=10000 with fixed seed=23. Expected: B ≈ 3333, C ≈ 6666. ±5% bands
// = [3167, 3500] for B and [6333, 7000] for C. The 5% band is the
// same generous band used by TestEngine_WeightedDistribution (the
// contract's canonical distribution fixture), keeping the test
// sensitivity aligned across the suite — anything tighter would
// start to flake on slower runners, anything looser would let real
// selector bugs slip through.
func TestEngine_SkipCooling_RenormalizedDistribution(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 23, time.Minute)
	seed3(e, "m") // A=1 B=1 C=2

	// Mark A cooling via a throwaway conversation's failover (the
	// realistic shape — A genuinely 429'd).
	e.InjectPreconditionStateForTest("m", "warmup", "A")
	e.ExcludeAndReselect("m", "warmup", "A", 0)
	if got := e.Stats()["m"].Cooldowns; got != 1 {
		t.Fatalf("A not cooling: gauge=%d", got)
	}

	const N = 10000
	counts := map[string]int{"B": 0, "C": 0}
	for i := 0; i < N; i++ {
		id, _, err := e.GetOrSelect("m", "f"+itoa(i))
		if err != nil {
			t.Fatal(err)
		}
		// A must NEVER be picked while cooling — that's the
		// hard correctness assertion; the distribution check is
		// secondary.
		if id == "A" {
			t.Fatalf("cooling A selected at iter %d", i)
		}
		counts[id]++
	}
	bands := map[string][2]int{"B": {3167, 3500}, "C": {6333, 7000}}
	for id, c := range counts {
		b := bands[id]
		if c < b[0] || c > b[1] {
			t.Fatalf("renormalized distribution drift for %s: got %d, want [%d,%d]", id, c, b[0], b[1])
		}
	}
}
