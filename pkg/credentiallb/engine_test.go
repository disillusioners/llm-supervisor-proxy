package credentiallb

import (
	"bytes"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// newTestEngine builds an engine with caller-chosen timings and a
// FIXED seed so distribution assertions are deterministic, plus the
// standard janitor cleanup.
func newTestEngine(t *testing.T, ttl, sweep time.Duration, seed int64, cooldown time.Duration) *Engine {
	t.Helper()
	e := NewEngine(ttl, sweep, seed, cooldown)
	t.Cleanup(e.Stop)
	return e
}

// seed3 binds a 3-credential model [A,B,C] with weights [1,1,2] (the
// contract's canonical distribution fixture).
func seed3(e *Engine, modelID string) {
	e.RebindFromStore(modelID, models.TestRefsWeighted(
		models.CredentialRef{CredentialID: "A", Weight: 1},
		models.CredentialRef{CredentialID: "B", Weight: 1},
		models.CredentialRef{CredentialID: "C", Weight: 2},
	))
}

// TestEngine_WeightedDistribution — decisions.md §E #1: weights
// [1,1,2], 10k unique conversation keys, fixed seed; per-credential
// counts within ±5% of (2500, 2500, 5000) ⇒ bands [2375,2625] and
// [4750,5250].
func TestEngine_WeightedDistribution(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 42, time.Minute)
	seed3(e, "m")
	counts := map[string]int{"A": 0, "B": 0, "C": 0}
	for i := 0; i < 10000; i++ {
		id, newly, err := e.GetOrSelect("m", itoa(i))
		if err != nil || !newly {
			// Unique keys: every call must bind fresh (W-1).
			t.Fatalf("call %d: err=%v newly=%v (unique keys must all bind fresh)", i, err, newly)
		}
		counts[id]++
	}
	bands := map[string][2]int{"A": {2375, 2625}, "B": {2375, 2625}, "C": {4750, 5250}}
	for id, c := range counts {
		b := bands[id]
		if c < b[0] || c > b[1] {
			t.Fatalf("distribution drift for %s: got %d, want [%d,%d]", id, c, b[0], b[1])
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}

// TestEngine_Affinity — decisions.md §E #2: 100 calls on the same
// conversation key all return the same credential.
func TestEngine_Affinity(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 7, time.Minute)
	seed3(e, "m")
	first := ""
	for i := 0; i < 100; i++ {
		id, _, err := e.GetOrSelect("m", "conv-A")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if i == 0 {
			first = id
			continue
		}
		if id != first {
			t.Fatalf("affinity broken at call %d: got %s want %s", i, id, first)
		}
	}
	st := e.Stats()["m"]
	if st.Bindings != 1 {
		t.Fatalf("bindings after 100 affinity calls: got %d want 1", st.Bindings)
	}
}

// TestEngine_TTL_LazyExpiry — ttl elapses with no traffic ⇒ the next
// lookup treats the binding as expired and re-binds (newlyBound=true).
func TestEngine_TTL_LazyExpiry(t *testing.T) {
	e := newTestEngine(t, 100*time.Millisecond, time.Hour, 7, time.Minute)
	seed3(e, "m")
	if _, _, err := e.GetOrSelect("m", "conv-A"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	id, newly, err := e.GetOrSelect("m", "conv-A")
	if err != nil {
		t.Fatal(err)
	}
	if !newly {
		t.Fatalf("idle-expired binding reused: id=%s newlyBound=false", id)
	}
	st := e.Stats()["m"]
	if st.Misses != 2 {
		t.Fatalf("misses: got %d want 2", st.Misses)
	}
}

// TestEngine_TTL_SlidingRefresh — #10 (leader-ruled): boundAt is
// REFRESHED on every in-TTL hit; expiry requires ttl of CONSECUTIVE
// idle. Timeline: ttl=300ms; bind at t0; hit at ~150ms (refresh);
// third call at ~300ms — beyond t0+ttl but within 300ms of the
// refreshed boundAt ⇒ must still be a HIT.
func TestEngine_TTL_SlidingRefresh(t *testing.T) {
	e := newTestEngine(t, 300*time.Millisecond, time.Hour, 7, time.Minute)
	seed3(e, "m")
	if _, newly, _ := e.GetOrSelect("m", "conv-S"); !newly {
		t.Fatal("first call must bind")
	}
	time.Sleep(150 * time.Millisecond)
	id, newly, _ := e.GetOrSelect("m", "conv-S")
	if newly {
		t.Fatal("in-TTL hit must not re-bind")
	}
	time.Sleep(150 * time.Millisecond) // 300ms total — past the ORIGINAL boundAt
	id2, newly2, _ := e.GetOrSelect("m", "conv-S")
	if newly2 {
		t.Fatalf("sliding TTL violated: binding expired despite in-window refresh (id=%s)", id2)
	}
	if id2 != id {
		t.Fatalf("credential changed on refresh hit: %s vs %s", id2, id)
	}
	st := e.Stats()["m"]
	if st.Hits != 2 {
		t.Fatalf("hits: got %d want 2", st.Hits)
	}
	if st.Misses != 1 {
		t.Fatalf("misses: got %d want 1 (the initial bind only)", st.Misses)
	}
}

// TestEngine_Janitor_SweepsExpiredBindings — the janitor proactively
// evicts idle-expired bindings WITHOUT any lookup traffic.
func TestEngine_Janitor_SweepsExpiredBindings(t *testing.T) {
	e := newTestEngine(t, 50*time.Millisecond, 20*time.Millisecond, 7, time.Minute)
	seed3(e, "m")
	for i := 0; i < 3; i++ {
		if _, _, err := e.GetOrSelect("m", "conv-j"+itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	if got := e.Stats()["m"].Bindings; got != 3 {
		t.Fatalf("pre-sweep bindings: got %d want 3", got)
	}
	time.Sleep(150 * time.Millisecond) // expiry at 50ms; sweeps at 60/80/100/120/140ms
	if got := e.Stats()["m"].Bindings; got != 0 {
		t.Fatalf("janitor did not evict idle bindings: %d remain", got)
	}
}

// TestEngine_E1_SweepConcurrency — E-1: the janitor's outer RLock lets
// reads proceed concurrently with the sweep. A sweep-heavy engine
// (fast ticker, many models with many bindings) runs while 20 reader
// goroutines hammer GetOrSelect; under -race any lock-discipline slip
// trips, and a stalled-read design (outer write lock) would show up as
// starvation (reads still complete here) or deadlock (test timeout).
//
// Item 5c (leader-ruled): the starvation sampling must happen BEFORE
// wg.Wait() — otherwise the post-wg.Wait() assertion only sees the
// quiescent final state and cannot distinguish "reads kept up during
// the sweep" from "all readers blocked and then finished in a single
// burst after the sweep ended". The mid-flight read samples taken at
// the ~100ms and ~200ms marks prove progress is happening WHILE the
// sweep is running (lateReads > midReads; both > 0).
//
// Goroutines loop until the stop channel closes so the test wall-time
// (200ms) — not an iteration count — controls how long reads run;
// this keeps the mid-flight sample meaningful on fast machines where
// the original fixed-iteration design completed all 20k reads in
// <100ms.
func TestEngine_E1_SweepConcurrency(t *testing.T) {
	e := newTestEngine(t, 30*time.Millisecond, 5*time.Millisecond, 7, time.Minute)
	// 20 models × 100 bindings = sweeping work, constantly expiring +
	// reseeded by readers.
	for m := 0; m < 20; m++ {
		mid := "m" + itoa(m)
		seed3(e, mid)
		for i := 0; i < 100; i++ {
			if _, _, err := e.GetOrSelect(mid, "c"+itoa(i)); err != nil {
				t.Fatal(err)
			}
		}
	}
	var reads, bad int64
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			mid := "m" + itoa(g%20)
			i := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				id, _, err := e.GetOrSelect(mid, "c"+itoa(i%100))
				if err != nil || id == "" {
					atomic.AddInt64(&bad, 1)
				}
				atomic.AddInt64(&reads, 1)
				i++
			}
		}(g)
	}

	// Mid-flight samples (Item 5c): two snapshots at ~100ms and
	// ~200ms, BEFORE wg.Wait(), prove reads are progressing WHILE
	// the sweep is running. With proper outer-RLock + per-model
	// write-lock discipline, the second sample is comfortably
	// larger than the first (a stalled-read bug would show
	// lateReads ≈ midReads).
	time.Sleep(100 * time.Millisecond)
	midReads := atomic.LoadInt64(&reads)
	time.Sleep(100 * time.Millisecond)
	lateReads := atomic.LoadInt64(&reads)

	// Signal goroutines to exit, then collect.
	close(stop)
	wg.Wait()

	if bad != 0 {
		t.Fatalf("%d reads failed during concurrent sweeps", bad)
	}
	if reads < 10000 {
		t.Fatalf("reads starved during sweep: only %d completed (expected >=10000)", reads)
	}
	// Starvation check (Item 5c): mid-flight sample must be non-
	// zero (reads were running WHILE the sweep was running), AND
	// the late sample must show meaningful progress past the mid
	// sample (proving no stall between the two sample points).
	if midReads == 0 {
		t.Fatalf("mid-flight starvation: zero reads at 100ms")
	}
	if lateReads <= midReads {
		t.Fatalf("starvation: midReads=%d lateReads=%d (no progress between 100ms and 200ms)", midReads, lateReads)
	}
}

// TestEngine_E2_FilterSurvivors — E-2: OnModelChanged PRESERVES
// bindings whose credential survives a reweight (boundAt untouched)
// and drops only orphans.
func TestEngine_E2_FilterSurvivors(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 7, time.Minute)
	e.RebindFromStore("m", models.TestRefs("A", "B"))
	// Deterministically bind 10 conversations to credA.
	for i := 0; i < 10; i++ {
		e.InjectPreconditionStateForTest("m", "k"+itoa(i), "A")
	}
	// Reweight: A survives (weight 1→5), B stays, C added.
	e.OnModelChanged("m", models.TestRefsWeighted(
		models.CredentialRef{CredentialID: "A", Weight: 5},
		models.CredentialRef{CredentialID: "B", Weight: 1},
		models.CredentialRef{CredentialID: "C", Weight: 1},
	))
	if got := e.Stats()["m"].Bindings; got != 10 {
		t.Fatalf("reweight flushed survivors: %d/10 bindings remain", got)
	}
	for i := 0; i < 10; i++ {
		id, newly, err := e.GetOrSelect("m", "k"+itoa(i))
		if err != nil {
			t.Fatal(err)
		}
		if id != "A" || newly {
			t.Fatalf("survivor binding lost affinity: k%d -> id=%s newly=%v", i, id, newly)
		}
	}
	// Now drop A from the config: all 10 A-bindings are orphans.
	e.OnModelChanged("m", models.TestRefs("C"))
	if got := e.Stats()["m"].Bindings; got != 0 {
		t.Fatalf("orphan bindings not dropped: %d remain", got)
	}
	// Post-drop lookups can only resolve to C.
	for i := 0; i < 10; i++ {
		id, _, err := e.GetOrSelect("m", "k"+itoa(i))
		if err != nil || id != "C" {
			t.Fatalf("post-drop pick: k%d -> id=%s err=%v", i, id, err)
		}
	}
}

// TestEngine_E3_FastPath_NoMapWrites — E-3: the single-credential
// fast path does NO map writes and NO stats ticks; 1000 calls leave
// the binding map at size 0.
func TestEngine_E3_FastPath_NoMapWrites(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 7, time.Minute)
	e.RebindFromStore("solo", models.TestRefs("only"))
	for i := 0; i < 1000; i++ {
		id, newly, err := e.GetOrSelect("solo", "k"+itoa(i))
		if err != nil || id != "only" || newly {
			t.Fatalf("fast-path call %d: id=%s newly=%v err=%v", i, id, newly, err)
		}
	}
	st := e.Stats()["solo"]
	if st.Bindings != 0 {
		t.Fatalf("fast path wrote bindings: map size %d", st.Bindings)
	}
	if st.Hits != 0 || st.Misses != 0 {
		t.Fatalf("fast path ticked stats: hits=%d misses=%d", st.Hits, st.Misses)
	}
}

// TestEngine_NoCredentials_Sentinel — exact sentinel error (design
// note #5) on unknown model, empty-ref model, and nil receiver.
func TestEngine_NoCredentials_Sentinel(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 7, time.Minute)
	if _, _, err := e.GetOrSelect("ghost", "k"); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("unknown model: err=%v want ErrNoCredentials", err)
	}
	if err := ErrNoCredentials; err.Error() != "credentiallb: model has no credentials" {
		t.Fatalf("sentinel text drifted: %q", err.Error())
	}
	e.RebindFromStore("empty", models.TestRefs())
	if _, _, err := e.GetOrSelect("empty", "k"); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("empty refs: err=%v want ErrNoCredentials", err)
	}
	var nilEngine *Engine
	if _, _, err := nilEngine.GetOrSelect("m", "k"); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("nil receiver: err=%v want ErrNoCredentials", err)
	}
}

// TestEngine_W2_EmptyKey_NoBinding — W-2: ""-key calls perform a
// fresh weighted pick per call, store NO binding, tick Misses, and
// return newlyBound=false (C2: newlyBound ⇔ a binding was stored).
func TestEngine_W2_EmptyKey_NoBinding(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 99, time.Minute)
	e.RebindFromStore("m", models.TestRefs("A", "B", "C"))
	counts := map[string]int{}
	for i := 0; i < 3000; i++ {
		id, newly, err := e.GetOrSelect("m", "")
		if err != nil {
			t.Fatal(err)
		}
		if newly {
			t.Fatal("empty-key pick reported newlyBound=true (C2 violation)")
		}
		counts[id]++
	}
	for id, c := range counts {
		if c < 900 || c > 1100 { // ±10% inspection band around 1000 (loose)
			t.Fatalf("empty-key distribution skewed for %s: %d/3000", id, c)
		}
	}
	st := e.Stats()["m"]
	if st.Bindings != 0 {
		t.Fatalf("empty-key call stored a binding: map size %d", st.Bindings)
	}
	if st.Misses != 3000 {
		t.Fatalf("empty-key misses: got %d want 3000", st.Misses)
	}
	if st.Hits != 0 {
		t.Fatalf("empty-key hits: got %d want 0", st.Hits)
	}
}

// TestEngine_W1_NewlyBoundMatrix — W-1: true on first bind; false on
// in-TTL reuse; false on the single-credential fast path; false on
// empty-key fresh picks.
func TestEngine_W1_NewlyBoundMatrix(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 7, time.Minute)
	seed3(e, "m")
	e.RebindFromStore("solo", models.TestRefs("only"))

	if _, newly, _ := e.GetOrSelect("m", "conv-W"); !newly {
		t.Fatal("first bind: newlyBound must be true")
	}
	if _, newly, _ := e.GetOrSelect("m", "conv-W"); newly {
		t.Fatal("in-TTL reuse: newlyBound must be false")
	}
	if _, newly, _ := e.GetOrSelect("solo", "any"); newly {
		t.Fatal("fast path: newlyBound must be false")
	}
	if _, newly, _ := e.GetOrSelect("m", ""); newly {
		t.Fatal("empty-key fresh pick: newlyBound must be false")
	}
}

// TestEngine_Hammer_RaceClean — decisions.md §E #5: 100 goroutines ×
// 1000 calls on the same model; -race clean; each conversation key
// yields a consistent credential.
func TestEngine_Hammer_RaceClean(t *testing.T) {
	e := newTestEngine(t, time.Hour, 10*time.Millisecond, 7, time.Minute)
	seed3(e, "m")
	var wg sync.WaitGroup
	for g := 0; g < 100; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			local := map[string]string{}
			for i := 0; i < 1000; i++ {
				k := "conv-" + itoa(g) + "-" + itoa(i%10)
				id, _, err := e.GetOrSelect("m", k)
				if err != nil {
					t.Errorf("goroutine %d: %v", g, err)
					return
				}
				if prev, ok := local[k]; ok && prev != id {
					t.Errorf("goroutine %d: key %s flapped %s -> %s", g, k, prev, id)
					return
				}
				local[k] = id
			}
		}(g)
	}
	wg.Wait()
}

// TestEngine_Stats_ShapeAndSemantics — E-4: per-model Hits/Misses/
// Bindings plus the Round-3 Failovers (monotonic, non-no-op only) and
// Cooldowns (live gauge of the cooldown map).
func TestEngine_Stats_ShapeAndSemantics(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 7, time.Minute)
	seed3(e, "m")

	// Two binds + one hit.
	e.GetOrSelect("m", "s1")
	e.GetOrSelect("m", "s2")
	e.GetOrSelect("m", "s1")

	st := e.Stats()["m"]
	if st.Hits != 1 || st.Misses != 2 || st.Bindings != 2 {
		t.Fatalf("base stats: hits=%d misses=%d bindings=%d", st.Hits, st.Misses, st.Bindings)
	}
	if st.Failovers != 0 || st.Cooldowns != 0 {
		t.Fatalf("pre-failover stats: failovers=%d cooldowns=%d", st.Failovers, st.Cooldowns)
	}

	// One non-no-op failover (bind s1 → A, exclude A).
	e.InjectPreconditionStateForTest("m", "s1", "A")
	e.ExcludeAndReselect("m", "s1", "A", 0)
	if st = e.Stats()["m"]; st.Failovers != 1 {
		t.Fatalf("failovers after one rebind: got %d want 1", st.Failovers)
	}
	if st = e.Stats()["m"]; st.Cooldowns != 1 {
		t.Fatalf("cooldowns gauge: got %d want 1", st.Cooldowns)
	}
	// A B2 no-op must NOT tick Failovers.
	e.InjectPreconditionStateForTest("m", "s2", "B")
	e.ExcludeAndReselect("m", "s2", "A", 0) // binding is B ≠ excluded A → no-op
	if st = e.Stats()["m"]; st.Failovers != 1 {
		t.Fatalf("B2 no-op ticked failovers: got %d want 1", st.Failovers)
	}
	// The value-copy contract: mutating the returned map is safe.
	snap := e.Stats()
	snap["m"] = EngineStats{}
	if (e.Stats()["m"]) == (EngineStats{}) {
		t.Fatal("Stats() leaked internal state (not a copy)")
	}
}

// TestEngine_JanitorPanicRecovery — E-4 / Risk #8: an injected sweep
// panic is recovered with a WARN and the janitor keeps sweeping (the
// post-panic sweep still evicts expired bindings).
func TestEngine_JanitorPanicRecovery(t *testing.T) {
	e := newTestEngine(t, 30*time.Millisecond, 15*time.Millisecond, 7, time.Minute)
	seed3(e, "m")
	if _, _, err := e.GetOrSelect("m", "p1"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	e.InjectSweepPanicForTest(func() { panic("boom") })
	time.Sleep(60 * time.Millisecond) // ≥ 2 sweeps panic + recover
	e.InjectSweepPanicForTest(nil)    // clear the hook
	time.Sleep(60 * time.Millisecond) // binding expired at ~30ms; ≥ 2 clean sweeps must evict

	log.SetOutput(prev)
	if !bytes.Contains(buf.Bytes(), []byte("janitor sweep recovered from panic")) {
		t.Fatalf("panic WARN missing; log was: %q", buf.String())
	}
	if got := e.Stats()["m"].Bindings; got != 0 {
		t.Fatalf("janitor dead after panic: %d bindings remain post-expiry", got)
	}
}

// TestEngine_Stop_Idempotent_AndLazyPathAlive — Stop is idempotent and
// GetOrSelect keeps working after Stop (lazy expiry only).
func TestEngine_Stop_Idempotent_AndLazyPathAlive(t *testing.T) {
	e := NewEngine(time.Hour, time.Hour, 7, time.Minute)
	e.Stop()
	e.Stop() // must not panic / double-close
	seed3(e, "m")
	id, _, err := e.GetOrSelect("m", "post-stop")
	if err != nil || id == "" {
		t.Fatalf("GetOrSelect after Stop: id=%s err=%v", id, err)
	}
}

// boundCreds snapshots the (convKey → credentialID) binding map for
// precise same-package assertions.
func boundCreds(e *Engine, modelID string) map[string]string {
	e.mu.RLock()
	st := e.models[modelID]
	e.mu.RUnlock()
	if st == nil {
		return nil
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	out := make(map[string]string, len(st.bindings))
	for k, b := range st.bindings {
		out[k] = b.credentialID
	}
	return out
}

// TestEngine_OnCredentialDeleted_S6 — drops bindings AND clears
// cooldowns for the deleted credential across every model in one
// pass; unrelated credentials untouched.
func TestEngine_OnCredentialDeleted_S6(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 7, time.Hour)
	e.RebindFromStore("m1", models.TestRefs("X", "A", "B"))
	e.RebindFromStore("m2", models.TestRefs("Y", "A", "C"))

	// Deterministic bindings: m1 has k1→X, k2→A, k3→B; m2 has k4→A.
	e.InjectPreconditionStateForTest("m1", "k1", "X")
	e.InjectPreconditionStateForTest("m1", "k2", "A")
	e.InjectPreconditionStateForTest("m1", "k3", "B")
	e.InjectPreconditionStateForTest("m2", "k4", "A")

	// Seed cooldowns: X on m1, A on m2 (via failovers off throwaway
	// conversations; the rebind target is irrelevant here).
	e.InjectPreconditionStateForTest("m1", "cx", "X")
	e.ExcludeAndReselect("m1", "cx", "X", 0)
	e.InjectPreconditionStateForTest("m2", "cy", "A")
	e.ExcludeAndReselect("m2", "cy", "A", 0)

	if !e.cooldownUntil("m1", "X").After(time.Now()) || !e.cooldownUntil("m2", "A").After(time.Now()) {
		t.Fatal("cooldown seeding failed")
	}

	// Delete X: m1 loses k1 and the X cooldown; everything else stays.
	e.OnCredentialDeleted("X")
	if got := e.cooldownUntil("m1", "X"); !got.IsZero() {
		t.Fatalf("deleted credential cooldown survived on m1: %v", got)
	}
	m1 := boundCreds(e, "m1")
	if m1["k1"] != "" {
		t.Fatalf("k1 (X) survived the X deletion: %v", m1)
	}
	if m1["k2"] != "A" || m1["k3"] != "B" {
		t.Fatalf("unrelated m1 bindings disturbed: %v", m1)
	}
	// Unrelated cooldown on m2 untouched; m2 bindings intact.
	if !e.cooldownUntil("m2", "A").After(time.Now()) {
		t.Fatal("unrelated cooldown (m2/A) was cleared")
	}
	if m2 := boundCreds(e, "m2"); len(m2) != 2 { // k4→A + cy→(Y|C)
		t.Fatalf("m2 bindings disturbed by X deletion: %v", m2)
	}

	// Delete A: clears the A cooldown on m2 and drops every A binding
	// on BOTH models in the same pass.
	e.OnCredentialDeleted("A")
	if !e.cooldownUntil("m2", "A").IsZero() {
		t.Fatal("A cooldown survived on m2")
	}
	for key, cred := range boundCreds(e, "m1") {
		if cred == "A" {
			t.Fatalf("A binding survived on m1: %s→%s", key, cred)
		}
	}
	for key, cred := range boundCreds(e, "m2") {
		if cred == "A" {
			t.Fatalf("A binding survived on m2: %s→%s", key, cred)
		}
	}
}

// TestEngine_DefensiveStaleBindingDrop — design note #2: a binding
// whose credential is no longer in the configured list is dropped at
// the next lookup even with NO invalidation event.
func TestEngine_DefensiveStaleBindingDrop(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 7, time.Minute)
	e.RebindFromStore("m", models.TestRefs("A", "B"))
	// Hand-prime a binding to a credential that is NOT configured —
	// simulates a missed invalidation.
	e.InjectPreconditionStateForTest("m", "stale", "GONE")
	id, newly, err := e.GetOrSelect("m", "stale")
	if err != nil {
		t.Fatal(err)
	}
	if id == "GONE" || !newly {
		t.Fatalf("stale binding not defensively dropped: id=%s newly=%v", id, newly)
	}
	if id != "A" && id != "B" {
		t.Fatalf("re-pick outside configured set: %s", id)
	}
}

// TestEngine_NilReceiver_NoPanics — every public method is nil-safe.
func TestEngine_NilReceiver_NoPanics(t *testing.T) {
	var e *Engine
	e.OnModelChanged("m", models.TestRefs("A"))
	e.OnCredentialDeleted("A")
	e.RebindFromStore("m", models.TestRefs("A"))
	e.Stop()
	e.InjectAllCoolingForTest("m")
	e.InjectPreconditionStateForTest("m", "k", "A")
	e.InjectSingleCredExclusionForTest("m")
	e.InjectSweepPanicForTest(nil)
	if got := len(e.Stats()); got != 0 {
		t.Fatalf("nil Stats: got %d entries", got)
	}
	if id, mode := e.ExcludeAndReselect("m", "k", "A", 0); id != "" || mode != ReselectNone {
		t.Fatalf("nil ExcludeAndReselect: got (%s,%v)", id, mode)
	}
}

// TestEngine_RebindFromStore_LastCallWins — startup rebind is
// overwrite semantics and never drops bindings (a stale binding is
// resolved lazily by the next lookup's defensive check, not by the
// rebind itself).
func TestEngine_RebindFromStore_LastCallWins(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 7, time.Minute)
	e.RebindFromStore("m", models.TestRefs("A"))
	e.InjectPreconditionStateForTest("m", "k", "A")
	// Rebind to a 2-cred set (a 1-cred set would take the E-3 fast
	// path, which bypasses the binding map entirely).
	e.RebindFromStore("m", models.TestRefs("B", "C"))
	// Binding to A is orphaned by the new refs → next lookup drops it
	// and re-picks from {B,C}.
	id, newly, err := e.GetOrSelect("m", "k")
	if err != nil || newly != true || (id != "B" && id != "C") {
		t.Fatalf("last-call-wins: id=%s newly=%v err=%v", id, newly, err)
	}
}
