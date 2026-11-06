package proxy

// Sticky last-success — SCENARIO closure (merge-gate stage 2).
//
// The wiring tests in sticky_order_test.go prove each choke point in
// isolation (single request, winner pre-seeded via a direct
// RecordSuccess). None of them closes the ORIGINAL live symptom the
// feature exists for: chain [A, B, C] where every request burns all
// three attempts before reaching the one working model (~9.3s live),
// and what sticky buys on the NEXT request with the same conversation
// prefix. This file adds the missing multi-request, attempts-counting,
// wall-clock-measured scenario:
//
//	S1  flag OFF — two same-prefix requests both burn [A B C] (RUN 1)
//	S2  flag ON, fresh handler — the cold request burns 3 and records;
//	    the next same-prefix request hits ONLY the winner at index 0
//	    (RUN 2), with the promoted list visible in race_started
//	S3  the recorded winner failing unpins (reason=attempt_failure);
//	    the next request races un-promoted and re-records; a fallback
//	    win (B over C) re-pins and promotes B
//	S4  TTL expiry — handler-wired short-TTL engine: a stale record
//	    stops promoting (the engine's TTL semantics are unit-covered
//	    in pkg/stickyorder; this pins the handler consulting THAT
//	    engine through the real request path)
//
// Fixture posture (recorded for the gate report):
//   - three single-credential internal chat models on a shared stub
//     upstream, behavior-switchable per model between requests;
//     per-model artificial latency (~300ms fail / ~250ms success)
//     stands in for the live incident's ~3s-per-burned-attempt
//     upstreams — the eliminated-waste math scales linearly.
//   - sequential burn: RaceMaxParallel=3 (Case-1 failure spawn
//     reachable: running is 0 after a failure), RaceParallelOnIdle=
//     false (no idle spawns) — each attempt is spawned by the
//     PREVIOUS attempt's failure, so the hit order is deterministic
//     A → B → C.
//   - 402 quota on A, 429 rate-limit on B, mirroring the live
//     incident's failure classes; single credential per model ⇒ no
//     cred-failover rotation (len(Credentials) <= 1 is ineligible) —
//     each failure advances the model chain directly.
//
// Wall-clock is EVIDENCE, not a gate: numbers are logged verbatim, the
// only timing assertion is the loose warm<cold drop inside S2.

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/stickyorder"
)

// ─── per-model switchable fake upstream ─────────────────────────────────────

const (
	scenarioA = "m-a" // always fails 402 quota — the incident's first failure class
	scenarioB = "m-b" // fails 429 by default; flipable to succeed (S3 fallback win)
	scenarioC = "m-c" // the healthy winner; flipable to fail (S3 unpin)
)

// scenarioHit is one observed upstream attempt.
type scenarioHit struct {
	model   string        // model ROW id (body "model" minus the "up-" prefix)
	latency time.Duration // observed wall time of this upstream call
}

// scenarioFake is a behavior-switchable fake upstream: each seeded row
// either fails with its own status/body or succeeds, flipable between
// requests via atomic mode words. Every attempt (fail or succeed) takes
// the model's artificial latency — the measurable stand-in for the live
// incident's ~3s-per-burned-attempt upstreams. Hits are appended at
// completion, so the log is strictly in arrival order.
type scenarioFake struct {
	mu       sync.Mutex
	hits     []scenarioHit
	latency  map[string]time.Duration
	succeed  map[string]*atomic.Bool
	failCode map[string]int
	failBody map[string]string
}

func newScenarioFake(
	latency map[string]time.Duration,
	failCode map[string]int,
	failBody map[string]string,
) *scenarioFake {
	f := &scenarioFake{
		latency:  latency,
		succeed:  map[string]*atomic.Bool{},
		failCode: failCode,
		failBody: failBody,
	}
	for id := range latency {
		var b atomic.Bool
		b.Store(false) // default: fail — each test flips its winners explicitly
		f.succeed[id] = &b
	}
	return f
}

func (f *scenarioFake) serve(w http.ResponseWriter, r *http.Request) {
	row := rowIDFromUpstream(upstreamBodyModel(r))
	f.mu.Lock()
	lat := f.latency[row]
	succeed := f.succeed[row].Load()
	f.mu.Unlock()

	start := time.Now()
	time.Sleep(lat)
	code, body := http.StatusOK, stubUpstreamBody("stub-model", "hi")
	if !succeed {
		f.mu.Lock()
		code, body = f.failCode[row], f.failBody[row]
		f.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(body))

	f.mu.Lock()
	f.hits = append(f.hits, scenarioHit{model: row, latency: time.Since(start)})
	f.mu.Unlock()
}

func (f *scenarioFake) setSucceed(row string, succeed bool) {
	f.mu.Lock()
	f.succeed[row].Store(succeed)
	f.mu.Unlock()
}

// total returns the number of upstream attempts observed so far.
func (f *scenarioFake) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.hits)
}

// hitsBetween returns the attempts recorded in the [n0, n1) window —
// the diff of snapshots taken around one client request.
func (f *scenarioFake) hitsBetween(n0, n1 int) []scenarioHit {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]scenarioHit(nil), f.hits[n0:n1]...)
}

// meanLatency returns the mean observed call duration and attempt count
// for one model across the whole test.
func (f *scenarioFake) meanLatency(row string) (time.Duration, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var total time.Duration
	n := 0
	for _, hit := range f.hits {
		if hit.model == row {
			total += hit.latency
			n++
		}
	}
	if n == 0 {
		return 0, 0
	}
	return total / time.Duration(n), n
}

func modelsOf(hits []scenarioHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.model)
	}
	return out
}

// logStickyLines echoes the [STICKY] lines from a captured log window
// verbatim into the test output — gate evidence (captureStickyLog
// redirects the global logger, so these lines never reach stderr).
func logStickyLines(t *testing.T, logged string) {
	t.Helper()
	for _, line := range strings.Split(logged, "\n") {
		if strings.Contains(line, "[STICKY]") {
			t.Logf("EVIDENCE %s", line)
		}
	}
}

// ─── harness + request helpers ──────────────────────────────────────────────

// scenarioFailShapes mirror the live incident's failure classes.
var (
	scenarioLatency = map[string]time.Duration{
		scenarioA: 300 * time.Millisecond,
		scenarioB: 300 * time.Millisecond,
		scenarioC: 250 * time.Millisecond,
	}
	scenarioFailCode = map[string]int{
		scenarioA: http.StatusPaymentRequired, // quota
		scenarioB: http.StatusTooManyRequests, // rate limit
		scenarioC: http.StatusBadGateway,      // only when flipped to fail (S3)
	}
	scenarioFailBody = map[string]string{
		scenarioA: `{"error":{"type":"insufficient_quota","message":"You exceeded your current quota, check your plan and billing details"}}`,
		scenarioB: `{"error":{"type":"rate_limit_error","message":"Rate limit reached for requests"}}`,
		scenarioC: `{"error":{"type":"server_error","message":"boom"}}`,
	}
)

// newScenarioHarness builds the sticky harness with the scenario fake
// upstream, the three-model execution list [m-a, m-b, m-c] (requested
// model m-a + fallback chain), and the sequential spawn posture. The
// gate flag sets the sticky config; the engine is always present (the
// nil seam is covered by the wiring tests).
func newScenarioHarness(t *testing.T, stickyEnabled bool) (*stickyHarness, *scenarioFake) {
	t.Helper()
	h := newStickyHarness(t, stickyEnabled, true)
	fake := newScenarioFake(scenarioLatency, scenarioFailCode, scenarioFailBody)
	// Baseline posture (the live incident's healthy tail): C succeeds;
	// A and B fail with their incident classes. S3 flips C/B explicitly.
	fake.setSucceed(scenarioC, true)
	h.swapUpstream(t, fake.serve)
	// Execution list = [requested] + FallbackChain ⇒ [m-a, m-b, m-c].
	h.seedChatModel(t, scenarioA, scenarioB, scenarioC)
	h.seedChatModel(t, scenarioB)
	h.seedChatModel(t, scenarioC)
	// Sequential burn posture (exact fields recorded in the gate report):
	h.cfgMutate = func(c *config.Config) {
		c.RaceMaxParallel = 3        // Case-1 failure spawn reachable (running=0 after a fail)
		c.RaceParallelOnIdle = false // no idle spawns — the burn is failure-driven ⇒ deterministic
		c.RaceRetryEnabled = false   // default; snapshot-only in this codebase — pinned for the record
	}
	h.rebuild(t)
	return h, fake
}

// fireScenarioRequest runs one chat request with the SAME conversation
// prefix every time (requested model m-a, identical leading user run,
// no auth header ⇒ token ""), timing the handler call and capturing log
// output. grace keeps the capture open briefly after the handler
// returns so coordinator-side [STICKY] lines (invalidation) have
// landed; the returned elapsed EXCLUDES the grace window.
func fireScenarioRequest(t *testing.T, h *stickyHarness, grace time.Duration) (code int, elapsed time.Duration, logged string) {
	t.Helper()
	req := makeChatRequest(t, map[string]interface{}{
		"model":    scenarioA,
		"messages": stickyMessages(),
	})
	rec := httptest.NewRecorder()
	logged = captureStickyLog(t, func() {
		s := time.Now()
		h.h.HandleChatCompletions(rec, req)
		elapsed = time.Since(s)
		if grace > 0 {
			time.Sleep(grace)
		}
	})
	return rec.Code, elapsed, logged
}

// waitForCond polls cond with a bounded deadline. Coordinator-side
// sticky mutations (invalidation) land in the race manage loop and may
// lag the handler's return by a scheduler tick, so engine-state
// assertions poll instead of checking once.
func waitForCond(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within 2s: %s", desc)
}

func stickyRecordedIs(eng *stickyorder.Engine, key, want string) func() bool {
	return func() bool {
		v, ok := eng.Get(key)
		return ok && v == want
	}
}

func stickyCleared(eng *stickyorder.Engine, key string) func() bool {
	return func() bool {
		_, ok := eng.Get(key)
		return !ok
	}
}

// ─── S1 / RUN 1: gate off — every same-prefix request burns the chain ───────

// TestStickyScenario_Run1_GateOffEveryRequestBurnsFullChain pins the
// ORIGINAL SYMPTOM with the gate off: two requests with the SAME
// conversation prefix each burn all three chain attempts before the
// healthy model serves, zero [STICKY] lines are emitted, and nothing
// is recorded.
func TestStickyScenario_Run1_GateOffEveryRequestBurnsFullChain(t *testing.T) {
	h, fake := newScenarioHarness(t, false)
	key := stickyKeyFor(scenarioA, stickyMessages())

	var logAll strings.Builder
	var burns []time.Duration
	for i := 1; i <= 2; i++ {
		before := fake.total()
		code, elapsed, logged := fireScenarioRequest(t, h, 0)
		logAll.WriteString(logged)
		if code != http.StatusOK {
			t.Fatalf("request %d: status = %d (want 200 — m-c must serve)", i, code)
		}
		got := modelsOf(fake.hitsBetween(before, fake.total()))
		if !slices.Equal(got, []string{scenarioA, scenarioB, scenarioC}) {
			t.Fatalf("request %d burned %v, want the full chain [%s %s %s]",
				i, got, scenarioA, scenarioB, scenarioC)
		}
		burns = append(burns, elapsed)
		t.Logf("RUN1 request %d: attempts-until-success=3 hit-order=%v elapsed=%s", i, got, elapsed)
	}
	if out := logAll.String(); strings.Contains(out, "[STICKY]") {
		t.Fatalf("gate-off run emitted [STICKY] lines: %q", out)
	}
	if v, ok := h.sticky.Get(key); ok {
		t.Fatalf("gate-off run recorded a winner: %q", v)
	}

	// Per-attempt latency means of the fakes (report input — the
	// eliminated waste per burn is mean(A)+mean(B)+mean(C)-mean(C)).
	for _, row := range []string{scenarioA, scenarioB, scenarioC} {
		mean, n := fake.meanLatency(row)
		t.Logf("fake %s: attempts=%d mean-latency=%s", row, n, mean)
	}
	t.Logf("RUN1 wall-clock per request: %v (n=2 requests)", burns)
}

// ─── S2 / RUN 2: gate on — the second same-prefix request is a 1-attempt hit ─

// TestStickyScenario_Run2_PromotesWinnerToIndex0OnSecondRequest closes
// the symptom with the gate ON (fresh handler/engine): request 1 burns
// the chain cold and records m-c; request 2 with the SAME prefix hits
// ONLY m-c at index 0 — the promoted list is observable both in the
// [STICKY] promoted line and in the race_started payload.
func TestStickyScenario_Run2_PromotesWinnerToIndex0OnSecondRequest(t *testing.T) {
	h, fake := newScenarioHarness(t, true)
	key := stickyKeyFor(scenarioA, stickyMessages())
	evCh := collectStickyEvents(t, h.bus)

	// Request 1 — cold: burns the full chain, delivers via m-c, records.
	before := fake.total()
	code1, elapsed1, logged1 := fireScenarioRequest(t, h, 50*time.Millisecond)
	if code1 != http.StatusOK {
		t.Fatalf("cold request: status = %d", code1)
	}
	got1 := modelsOf(fake.hitsBetween(before, fake.total()))
	if !slices.Equal(got1, []string{scenarioA, scenarioB, scenarioC}) {
		t.Fatalf("cold request burned %v, want the full chain", got1)
	}
	if !strings.Contains(logged1, "[STICKY] success recorded") ||
		!strings.Contains(logged1, "model="+scenarioC) {
		t.Fatalf("cold request did not log success recorded for m-c: %q", logged1)
	}
	waitForCond(t, "m-c recorded", stickyRecordedIs(h.sticky, key, scenarioC))
	logStickyLines(t, logged1)
	t.Logf("RUN2 request 1 (cold): attempts-until-success=3 hit-order=%v elapsed=%s", got1, elapsed1)

	// Request 1's own race ran the UN-promoted chain.
	firstRace := waitRaceStartedModels(t, evCh, 1, 2*time.Second)
	if !slices.Equal(firstRace, []string{scenarioA, scenarioB, scenarioC}) {
		t.Fatalf("cold race_started models = %v, want un-promoted chain", firstRace)
	}

	// Request 2 — SAME prefix, warm: the recorded winner must be hit FIRST.
	before = fake.total()
	code2, elapsed2, logged2 := fireScenarioRequest(t, h, 0)
	if code2 != http.StatusOK {
		t.Fatalf("warm request: status = %d", code2)
	}
	got2 := modelsOf(fake.hitsBetween(before, fake.total()))
	if len(got2) != 1 || got2[0] != scenarioC {
		t.Fatalf("warm request attempts-until-success = %d (hit-order %v), want exactly 1 hitting %s",
			len(got2), got2, scenarioC)
	}
	// Request 2's race_started: the first-race wait above already
	// consumed request 1's event, so request 2's is the NEXT
	// race_started on the channel (n=1 from this point).
	warmRace := waitRaceStartedModels(t, evCh, 1, 2*time.Second)
	if !slices.Equal(warmRace, []string{scenarioC, scenarioA, scenarioB}) {
		t.Fatalf("warm race_started models = %v, want promoted [%s %s %s] (index 0 = winner)",
			warmRace, scenarioC, scenarioA, scenarioB)
	}
	if !strings.Contains(logged2, "promoted last-success model="+scenarioC+" to index 0") ||
		!strings.Contains(logged2, "from=[m-a m-b m-c] to=[m-c m-a m-b]") {
		t.Fatalf("promotion line missing/malformed: %q", logged2)
	}
	logStickyLines(t, logged2)
	t.Logf("RUN2 request 2 (warm): attempts-until-success=1 hit-order=%v elapsed=%s", got2, elapsed2)

	// LOOSE wall-clock claim (evidence, not a flaky gate): the warm
	// request pays only m-c's latency; the cold one paid A+B+C.
	if elapsed2*2 >= elapsed1 {
		t.Fatalf("wall-clock did not drop as expected: warm=%s cold=%s", elapsed2, elapsed1)
	}
	// The winner survived its own promotion (re-record is a silent TTL refresh).
	waitForCond(t, "m-c still recorded", stickyRecordedIs(h.sticky, key, scenarioC))
}

// ─── S3: unpin on winner failure, revert, fallback win re-pins ──────────────

// TestStickyScenario_UnpinOnWinnerFailure_RevertAndFallbackWin covers
// the correction path end to end. Slot state is tracked explicitly at
// every step (each step's assertion depends on the slot the PREVIOUS
// step left behind):
//
//  1. cold burn ⇒ slot=C
//  2. C flipped to fail: the PROMOTED winner burns first and its
//     failure invalidates (reason=attempt_failure) ⇒ slot empty
//  3. B flipped to succeed (C still failing), slot empty: the chain
//     burns in original order and B delivers at attempt 2 ⇒ slot=B
//     (the fallback-win bonus)
//  4. slot=B: the next request promotes B to index 0 (single hit)
//  5. B flipped to fail (C still failing): promoted B burns first and
//     unpins ⇒ slot empty
//  6. all restored (B fail, C succeed), slot empty: the request races
//     UN-promoted in original order and C's delivered success
//     re-records (the revert assertion)
func TestStickyScenario_UnpinOnWinnerFailure_RevertAndFallbackWin(t *testing.T) {
	h, fake := newScenarioHarness(t, true)
	key := stickyKeyFor(scenarioA, stickyMessages())

	// 1. Setup (cold): burn 3, m-c delivers and records.
	before := fake.total()
	code, _, _ := fireScenarioRequest(t, h, 50*time.Millisecond)
	if code != http.StatusOK {
		t.Fatalf("setup request: status = %d", code)
	}
	_ = fake.hitsBetween(before, fake.total()) // window consumed
	waitForCond(t, "m-c recorded", stickyRecordedIs(h.sticky, key, scenarioC))

	// 2. Winner C flipped to fail: the promoted winner burns FIRST and
	// its failure unpins; A and B fail after; chain end ⇒ 502.
	fake.setSucceed(scenarioC, false)
	before = fake.total()
	code2, _, logged2 := fireScenarioRequest(t, h, 100*time.Millisecond)
	if code2 == http.StatusOK {
		t.Fatalf("all-fail request unexpectedly succeeded")
	}
	got2 := modelsOf(fake.hitsBetween(before, fake.total()))
	if !slices.Equal(got2, []string{scenarioC, scenarioA, scenarioB}) {
		t.Fatalf("all-fail request burned %v, want the promoted winner first [%s %s %s]",
			got2, scenarioC, scenarioA, scenarioB)
	}
	if n := strings.Count(logged2, "[STICKY] invalidated"); n != 1 {
		t.Fatalf("invalidated lines = %d, want exactly 1 (winner-matched): %q", n, logged2)
	}
	if !strings.Contains(logged2, "reason=attempt_failure") ||
		!strings.Contains(logged2, "model="+scenarioC) {
		t.Fatalf("invalidation line missing/malformed: %q", logged2)
	}
	waitForCond(t, "slot cleared after winner failure", stickyCleared(h.sticky, key))
	logStickyLines(t, logged2)
	t.Logf("S3 unpin: attempts=%v (promoted winner burned first), status=%d", got2, code2)

	// 3. BONUS — fallback win re-pins B: slot EMPTY (step 2), B flipped
	// to succeed while C still fails ⇒ original order, B delivers at
	// attempt 2 and is recorded.
	fake.setSucceed(scenarioB, true)
	before = fake.total()
	code3, _, logged3 := fireScenarioRequest(t, h, 50*time.Millisecond)
	if code3 != http.StatusOK {
		t.Fatalf("fallback-win request: status = %d", code3)
	}
	got3 := modelsOf(fake.hitsBetween(before, fake.total()))
	if !slices.Equal(got3, []string{scenarioA, scenarioB}) {
		t.Fatalf("fallback-win request burned %v, want [%s %s] (B serves at attempt 2)",
			got3, scenarioA, scenarioB)
	}
	if strings.Contains(logged3, "promoted last-success") {
		t.Fatalf("fallback-win request promoted from an empty slot: %q", logged3)
	}
	waitForCond(t, "m-b recorded after fallback win", stickyRecordedIs(h.sticky, key, scenarioB))
	t.Logf("S3 fallback win: attempts=%v (B delivered, slot was empty), status=%d", got3, code3)

	// 4. slot=B: the next request promotes B to index 0 — single hit.
	before = fake.total()
	code4, _, logged4 := fireScenarioRequest(t, h, 0)
	if code4 != http.StatusOK {
		t.Fatalf("B-promoted request: status = %d", code4)
	}
	got4 := modelsOf(fake.hitsBetween(before, fake.total()))
	if len(got4) != 1 || got4[0] != scenarioB {
		t.Fatalf("B-promoted request hit %v, want exactly [%s]", got4, scenarioB)
	}
	if !strings.Contains(logged4, "promoted last-success model="+scenarioB+" to index 0") ||
		!strings.Contains(logged4, "from=[m-a m-b m-c] to=[m-b m-a m-c]") {
		t.Fatalf("B promotion line missing/malformed: %q", logged4)
	}
	logStickyLines(t, logged4)
	t.Logf("S3 B-promotion: attempts=%v, status=%d", got4, code4)

	// 5. B flipped to fail: promoted B burns first and unpins; A and C
	// fail after ⇒ 502, slot empty again.
	fake.setSucceed(scenarioB, false)
	before = fake.total()
	code5, _, logged5 := fireScenarioRequest(t, h, 100*time.Millisecond)
	if code5 == http.StatusOK {
		t.Fatalf("all-fail request (step 5) unexpectedly succeeded")
	}
	got5 := modelsOf(fake.hitsBetween(before, fake.total()))
	if !slices.Equal(got5, []string{scenarioB, scenarioA, scenarioC}) {
		t.Fatalf("step-5 request burned %v, want the promoted B first [%s %s %s]",
			got5, scenarioB, scenarioA, scenarioC)
	}
	if n := strings.Count(logged5, "[STICKY] invalidated"); n != 1 {
		t.Fatalf("step-5 invalidated lines = %d, want exactly 1: %q", n, logged5)
	}
	if !strings.Contains(logged5, "model="+scenarioB) {
		t.Fatalf("step-5 invalidation did not name B: %q", logged5)
	}
	waitForCond(t, "slot cleared after B failure", stickyCleared(h.sticky, key))
	logStickyLines(t, logged5)
	t.Logf("S3 unpin B: attempts=%v, status=%d", got5, code5)

	// 6. All restored (B fails, C succeeds), slot EMPTY: the request
	// races UN-promoted in original order — the revert assertion — and
	// C's delivered success re-records.
	fake.setSucceed(scenarioC, true)
	before = fake.total()
	code6, _, logged6 := fireScenarioRequest(t, h, 50*time.Millisecond)
	if code6 != http.StatusOK {
		t.Fatalf("revert request: status = %d", code6)
	}
	got6 := modelsOf(fake.hitsBetween(before, fake.total()))
	if !slices.Equal(got6, []string{scenarioA, scenarioB, scenarioC}) {
		t.Fatalf("post-unpin revert burned %v, want the un-promoted chain", got6)
	}
	if strings.Contains(logged6, "promoted last-success") {
		t.Fatalf("post-unpin revert promoted without a record: %q", logged6)
	}
	waitForCond(t, "m-c re-recorded", stickyRecordedIs(h.sticky, key, scenarioC))
	t.Logf("S3 revert: attempts=%v (original order, no promotion), status=%d", got6, code6)
}

// ─── S4: TTL expiry — handler-wired short-TTL engine ────────────────────────

// TestStickyScenario_TTLExpiryStopsPromotion_HandlerWired swaps in a
// SHORT-TTL engine and pins that a stale record stops promoting through
// the real request path: after one delivered success and an idle gap
// past the TTL, the next same-prefix request races UN-promoted (and its
// own success re-arms the slot). The engine's TTL semantics themselves
// are unit-covered (TestEngine_TTLExpiry, TestEngine_GetDoesNotRefreshTTL);
// this is the handler-wired check.
func TestStickyScenario_TTLExpiryStopsPromotion_HandlerWired(t *testing.T) {
	h, fake := newScenarioHarness(t, true)
	// Short-TTL engine via the constructor param seam (production
	// default is 30m — constructible per-request here).
	h.sticky.Stop()
	h.sticky = stickyorder.NewEngine(150*time.Millisecond, 50*time.Millisecond)
	h.rebuild(t)

	key := stickyKeyFor(scenarioA, stickyMessages())
	evCh := collectStickyEvents(t, h.bus)

	// Cold request: burn 3, m-c records (the TTL clock starts now).
	before := fake.total()
	code, _, logged := fireScenarioRequest(t, h, 0)
	if code != http.StatusOK {
		t.Fatalf("cold request: status = %d", code)
	}
	if !strings.Contains(logged, "[STICKY] success recorded") {
		t.Fatalf("cold request did not record: %q", logged)
	}
	waitForCond(t, "m-c recorded", stickyRecordedIs(h.sticky, key, scenarioC))

	// Idle past the TTL (sub-second: 400ms > 150ms ttl + 50ms sweep cadence).
	time.Sleep(400 * time.Millisecond)
	if _, ok := h.sticky.Get(key); ok {
		t.Fatal("TTL-expired record still readable (engine-level check failed)")
	}

	// Next same-prefix request: NO promotion — original order burns.
	before = fake.total()
	code2, _, logged2 := fireScenarioRequest(t, h, 0)
	if code2 != http.StatusOK {
		t.Fatalf("post-TTL request: status = %d", code2)
	}
	got2 := modelsOf(fake.hitsBetween(before, fake.total()))
	if !slices.Equal(got2, []string{scenarioA, scenarioB, scenarioC}) {
		t.Fatalf("post-TTL request burned %v, want the un-promoted chain", got2)
	}
	if strings.Contains(logged2, "promoted last-success") {
		t.Fatalf("post-TTL request promoted an expired record: %q", logged2)
	}
	postTTLRace := waitRaceStartedModels(t, evCh, 2, 2*time.Second)
	if !slices.Equal(postTTLRace, []string{scenarioA, scenarioB, scenarioC}) {
		t.Fatalf("post-TTL race_started models = %v, want the un-promoted chain", postTTLRace)
	}
	// The delivered success re-arms the slot (sliding from now).
	waitForCond(t, "m-c re-recorded post-TTL", stickyRecordedIs(h.sticky, key, scenarioC))
	t.Logf("S4: post-TTL attempts=%v (no promotion), re-recorded m-c", got2)
}
