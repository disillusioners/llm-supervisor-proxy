package stickyorder

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually advanced time source.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// newTestEngine builds an engine with a long sweep interval (the janitor
// never fires mid-test) and a fake clock, so TTL assertions are
// deterministic. The caller must Stop it.
func newTestEngine(t *testing.T, ttl time.Duration) (*Engine, *fakeClock) {
	t.Helper()
	e := NewEngine(ttl, time.Hour)
	clock := newFakeClock()
	e.clock = clock.Now
	return e, clock
}

// captureLog redirects the standard logger for the duration of fn.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}()
	fn()
	return buf.String()
}

// TestEngine_RecordGetRoundtrip pins the basic record -> get contract.
func TestEngine_RecordGetRoundtrip(t *testing.T) {
	e, _ := newTestEngine(t, time.Minute)
	defer e.Stop()

	if _, ok := e.Get("conv-1"); ok {
		t.Fatal("Get on an empty engine must miss")
	}

	e.RecordSuccess("conv-1", "model-a")
	got, ok := e.Get("conv-1")
	if !ok || got != "model-a" {
		t.Fatalf("Get after record = (%q, %v), want (model-a, true)", got, ok)
	}

	// Overwrite.
	e.RecordSuccess("conv-1", "model-b")
	got, ok = e.Get("conv-1")
	if !ok || got != "model-b" {
		t.Fatalf("Get after overwrite = (%q, %v), want (model-b, true)", got, ok)
	}

	// Keys are independent.
	if _, ok := e.Get("conv-2"); ok {
		t.Fatal("unrelated key must miss")
	}
}

// TestEngine_EmptyInputsNoOp pins the "" key / "" model gates.
func TestEngine_EmptyInputsNoOp(t *testing.T) {
	e, _ := newTestEngine(t, time.Minute)
	defer e.Stop()

	e.RecordSuccess("", "model-a")
	e.RecordSuccess("conv-1", "")
	if _, ok := e.Get(""); ok {
		t.Fatal("Get(\"\") must miss")
	}
	if _, ok := e.Get("conv-1"); ok {
		t.Fatal("RecordSuccess with empty model must not record")
	}
	e.InvalidateOnFailure("", "model-a")
}

// TestEngine_NilReceiverNoOps pins the nil-receiver seam contract for
// all four methods.
func TestEngine_NilReceiverNoOps(t *testing.T) {
	var e *Engine

	if got, ok := e.Get("conv"); ok || got != "" {
		t.Fatalf("nil Get = (%q, %v), want (\"\", false)", got, ok)
	}
	e.RecordSuccess("conv", "model-a")
	e.InvalidateOnFailure("conv", "model-a")
	e.Stop() // must not panic or block
}

// TestEngine_TTLExpiry pins sweep-based expiry and the read-path lazy
// expiry (an entry past TTL is a miss even before the janitor runs).
func TestEngine_TTLExpiry(t *testing.T) {
	const ttl = 10 * time.Minute
	e, clock := newTestEngine(t, ttl)
	defer e.Stop()

	e.RecordSuccess("conv-1", "model-a")

	// Just inside the window.
	clock.Advance(ttl - time.Second)
	if _, ok := e.Get("conv-1"); !ok {
		t.Fatal("entry must be live just inside the TTL")
	}

	// Past the TTL: the READ path already misses (janitor has not run).
	clock.Advance(2 * time.Second)
	if _, ok := e.Get("conv-1"); ok {
		t.Fatal("entry past TTL must miss on read")
	}

	// The sweep removes it for real.
	e.sweepOnce()
	e.mu.RLock()
	n := len(e.winners)
	e.mu.RUnlock()
	if n != 0 {
		t.Fatalf("janitor left %d entries, want 0", n)
	}
}

// TestEngine_SweepKeepsFreshEntries pins that a sweep only drops expired
// entries.
func TestEngine_SweepKeepsFreshEntries(t *testing.T) {
	const ttl = 10 * time.Minute
	e, clock := newTestEngine(t, ttl)
	defer e.Stop()

	e.RecordSuccess("old", "model-old")
	clock.Advance(20 * time.Minute)
	e.RecordSuccess("fresh", "model-fresh")

	e.sweepOnce()

	if _, ok := e.Get("old"); ok {
		t.Fatal("expired entry survived the sweep")
	}
	if got, ok := e.Get("fresh"); !ok || got != "model-fresh" {
		t.Fatalf("fresh entry evicted: (%q, %v)", got, ok)
	}
}

// TestEngine_SlidingRefreshOnRecordSuccess pins that RecordSuccess
// refreshes the timestamp (the slot lives as long as successes keep
// arriving).
func TestEngine_SlidingRefreshOnRecordSuccess(t *testing.T) {
	const ttl = 10 * time.Minute
	e, clock := newTestEngine(t, ttl)
	defer e.Stop()

	e.RecordSuccess("conv-1", "model-a")
	clock.Advance(9 * time.Minute)
	e.RecordSuccess("conv-1", "model-a") // refresh
	clock.Advance(9 * time.Minute)       // 18m since first record, 9m since refresh
	if _, ok := e.Get("conv-1"); !ok {
		t.Fatal("sliding refresh did not extend the slot")
	}

	e.sweepOnce()
	if _, ok := e.Get("conv-1"); !ok {
		t.Fatal("sweep evicted a refreshed entry")
	}
}

// TestEngine_GetDoesNotRefreshTTL pins the deliberate no-refresh-on-hit
// rule: a repeatedly-read but never-successful slot ages out.
func TestEngine_GetDoesNotRefreshTTL(t *testing.T) {
	const ttl = 10 * time.Minute
	e, clock := newTestEngine(t, ttl)
	defer e.Stop()

	e.RecordSuccess("conv-1", "model-a")
	for i := 0; i < 5; i++ {
		clock.Advance(2 * time.Minute)
		_, _ = e.Get("conv-1") // repeated hits must NOT extend the slot
	}
	// 10m elapsed; strictly greater than the TTL boundary is achieved
	// with one more tick.
	clock.Advance(time.Second)
	if _, ok := e.Get("conv-1"); ok {
		t.Fatal("Get refreshed the TTL; slot must have expired")
	}
}

// TestEngine_InvalidateOnFailure pins winner-matched deletion.
func TestEngine_InvalidateOnFailure(t *testing.T) {
	e, _ := newTestEngine(t, time.Minute)
	defer e.Stop()

	// No record: no-op.
	e.InvalidateOnFailure("conv-1", "model-a")

	e.RecordSuccess("conv-1", "model-a")

	// Non-winner failure: no-op.
	e.InvalidateOnFailure("conv-1", "model-b")
	if got, ok := e.Get("conv-1"); !ok || got != "model-a" {
		t.Fatalf("non-winner failure unpinned the slot: (%q, %v)", got, ok)
	}

	// Winner failure: deletes.
	e.InvalidateOnFailure("conv-1", "model-a")
	if _, ok := e.Get("conv-1"); ok {
		t.Fatal("winner failure did not invalidate the slot")
	}

	// Re-record after invalidation works.
	e.RecordSuccess("conv-1", "model-a")
	if got, ok := e.Get("conv-1"); !ok || got != "model-a" {
		t.Fatalf("re-record after invalidation failed: (%q, %v)", got, ok)
	}
}

// TestEngine_LogLines pins the observability contract: a record logs, a
// non-winner failure does not, and an actual invalidation logs its
// reason.
func TestEngine_LogLines(t *testing.T) {
	e, _ := newTestEngine(t, time.Minute)
	defer e.Stop()

	out := captureLog(t, func() {
		e.RecordSuccess("0123456789abcdef", "model-a")
	})
	if !strings.Contains(out, "[STICKY] success recorded key=01234567… model=model-a") {
		t.Fatalf("record log line missing/incorrect: %q", out)
	}

	out = captureLog(t, func() {
		e.InvalidateOnFailure("0123456789abcdef", "model-b")
	})
	if strings.Contains(out, "[STICKY] invalidated") {
		t.Fatalf("non-winner failure logged an invalidation: %q", out)
	}

	out = captureLog(t, func() {
		e.InvalidateOnFailure("0123456789abcdef", "model-a")
	})
	if !strings.Contains(out, "[STICKY] invalidated key=01234567… model=model-a reason=attempt_failure") {
		t.Fatalf("invalidation log line missing/incorrect: %q", out)
	}
}

// TestEngine_StopIdempotent pins double-Stop.
func TestEngine_StopIdempotent(t *testing.T) {
	e := NewEngine(time.Minute, time.Hour)
	e.Stop()
	e.Stop()
	e.Stop()

	// Methods still usable after Stop (lazy expiry only, no janitor).
	e.RecordSuccess("conv-1", "model-a")
	if got, ok := e.Get("conv-1"); !ok || got != "model-a" {
		t.Fatalf("engine unusable after Stop: (%q, %v)", got, ok)
	}
}

// TestEngine_SweepPanicRecovered pins the defer/recover around a sweep:
// a panicking sweep is logged, recovered, and the next sweep runs.
func TestEngine_SweepPanicRecovered(t *testing.T) {
	e, clock := newTestEngine(t, time.Minute)
	defer e.Stop()

	e.RecordSuccess("conv-1", "model-a")

	// Poison the clock so the sweep panics inside now().
	e.clock = func() time.Time { panic("boom") }
	out := captureLog(t, func() {
		e.sweepOnce() // must not propagate the panic
	})
	if !strings.Contains(out, "janitor sweep recovered from panic") {
		t.Fatalf("panic-recovery WARN line missing: %q", out)
	}

	// A healthy clock lets the next sweep work.
	e.clock = clock.Now
	clock.Advance(2 * time.Minute)
	e.sweepOnce()
	e.mu.RLock()
	n := len(e.winners)
	e.mu.RUnlock()
	if n != 0 {
		t.Fatalf("post-recovery sweep left %d entries, want 0", n)
	}
}

// TestEngine_ConcurrentHammer exercises every method concurrently with
// deterministic per-key assertions. Run with -race.
func TestEngine_ConcurrentHammer(t *testing.T) {
	e := NewEngine(time.Minute, time.Hour)
	defer e.Stop()

	const goroutines = 16
	const iterations = 200

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				key := fmt.Sprintf("conv-%d", g) // per-goroutine key: deterministic
				model := fmt.Sprintf("model-%d-%d", g, i)
				e.RecordSuccess(key, model)
				got, ok := e.Get(key)
				if !ok {
					t.Errorf("goroutine %d: key %s vanished after RecordSuccess", g, key)
					return
				}
				if !strings.HasPrefix(got, fmt.Sprintf("model-%d-", g)) {
					t.Errorf("goroutine %d: Get returned foreign value %q", g, got)
					return
				}
				// Non-winner failures are no-ops; winner failures unpin.
				e.InvalidateOnFailure(key, "some-other-model")
				if i%7 == 0 {
					e.InvalidateOnFailure(key, model)
				}
			}
		}(g)
	}

	// Concurrent readers on the whole keyspace.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				for k := 0; k < goroutines; k++ {
					_, _ = e.Get(fmt.Sprintf("conv-%d", k))
				}
			}
		}()
	}

	wg.Wait()

	// Final deterministic assertion: record then read on a fresh key.
	e.RecordSuccess("final", "final-model")
	if got, ok := e.Get("final"); !ok || got != "final-model" {
		t.Fatalf("post-hammer roundtrip = (%q, %v)", got, ok)
	}
}

// TestEngine_ConcurrentStopAndUse pins that Stop races safely with
// in-flight engine use (no panic, no deadlock).
func TestEngine_ConcurrentStopAndUse(t *testing.T) {
	e := NewEngine(time.Minute, time.Millisecond) // short sweep ⇒ real janitor ticks

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				key := fmt.Sprintf("k-%d-%d", g, i)
				e.RecordSuccess(key, "m")
				_, _ = e.Get(key)
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		e.Stop()
	}()
	wg.Wait()
	e.Stop() // idempotent
}

// TestKeyPrefix pins the single canonical key rendering (shared by the
// engine log lines and the proxy promotion line): ≤8 chars rendered
// whole, longer keys truncated to 8, always with the ellipsis marker.
func TestKeyPrefix(t *testing.T) {
	cases := []struct{ key, want string }{
		{"", "…"},
		{"abc", "abc…"},
		{"12345678", "12345678…"},
		{"123456789", "12345678…"},
		{"0123456789abcdef", "01234567…"},
	}
	for _, tc := range cases {
		if got := KeyPrefix(tc.key); got != tc.want {
			t.Errorf("KeyPrefix(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}

// TestEngine_ZeroValueStop pins the zero-value guard: an Engine whose
// channels were never initialized (not built by NewEngine) must not block
// on a nil sweepDone.
func TestEngine_ZeroValueStop(t *testing.T) {
	var e Engine // zero value: nil stopCh + nil sweepDone
	done := make(chan struct{})
	go func() {
		e.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop on a zero-value Engine blocked")
	}
}
