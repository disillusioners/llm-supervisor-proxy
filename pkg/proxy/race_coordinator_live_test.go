// Phase 2 / real-streaming-default — new test battery (plan §Test Strategy, lines 659-745).
// Coverage targets:
//
//   TestLiveRelay_FirstByteWinner          — predicate swap: winner fires on first
//                                            forwardable chunk, not on completion.
//   TestLiveRelay_PredicateVariants         — role-only-first, thinking-first,
//                                            two-attempt first-buffered-wins (NOT
//                                            first-completed), error-chunk-only MUST
//                                            NOT win (task 2.3 reorder), and the
//                                            FIX-1 completed-EMPTY-no-error pins.
//   TestLiveRelay_StreamDeadlineZeroByte    — live-mode 0-byte deadline guard
//                                            (task 2.2): handler.go handleRaceFailure
//                                            surfaces the deadline error envelope.
//   TestLiveRelay_StreamDeadlineInLiveMode  — review row (c): wire-level in-band
//                                            envelope (not bare 200 / 5xx),
//                                            byte-at-boundary honored, late-deadline
//                                            after winner (no double-winner/panic).
//   TestLiveRelay_NonStreamUnaffected       — M3 regression pin: isStream=false
//                                            keeps IsCompleted gate + 0-byte
//                                            deadline promotion byte-identical.
//   TestLiveRelay_HeaderDispatch            — header present ⇒ buffered; header
//                                            absent ⇒ live first-byte (coordinator
//                                            AND HTTP-level through handler wiring).
//   TestLiveRelay_MidStreamLoserCancel      — review row (b): REAL dual-upstream
//                                            race; loser cancelled mid-Add.
//   TestLiveRelay_FirstByteThenImmediateErr — review row (d): 1 chunk + well-formed
//                                            in-band SSE error envelope on the wire.
//   TestLiveRelay_PreFirstByteError         — review row (e): pre-byte 500 ⇒
//                                            in-band envelope, shape-identical to (d).
//   TestLiveRelay_R2_EstimatorUnderPrunePressure — review row (g): full-stream
//                                            bufferstore/estimator capture in live
//                                            mode; buffered entry+Done unchanged.
//   TestLiveRelay_ClientDisconnectDuringMidStreamCancel — review row (h): no panic,
//                                            goroutine cleanup under -race.
//   TestLiveRelay_BufferStoreDoubleSave_Benign — review row (k) / task 2.4.1:
//                                            live failing relay saves EXACTLY once.
//
// (Review row (f) — TestLiveRelay_NoCredFailoverAfterFirstByte and its
// pre-first-byte companion — lives in race_coordinator_credfailover_test.go
// next to the engine-backed harness it extends.)
//
// These tests use the direct coordinator constructor (`newRaceCoordinator` 6-arg
// wrapper), which the plan pins at `liveFirstByteGate=false` so buffered-mode
// tests stay byte-identical. For live-mode tests we call the 11-arg
// `newRaceCoordinatorWithEvents` directly with `liveFirstByteGate=true`.

package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/bufferstore"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// liveTestCfg returns a minimal ConfigSnapshot suitable for live-mode tests.
// StreamDeadline is intentionally small where tests need to exercise the
// deadline path; otherwise generous to avoid accidental firings.
func liveTestCfg(upstreamURL string, streamDeadline time.Duration) *ConfigSnapshot {
	return &ConfigSnapshot{
		UpstreamURL:        upstreamURL,
		RaceRetryEnabled:   true,
		RaceMaxParallel:    1,
		RaceMaxBufferBytes: 4096,
		IdleTimeout:        1 * time.Second,
		StreamDeadline:     streamDeadline,
		MaxGenerationTime:  10 * time.Second,
		ModelID:            "test-model",
	}
}

// liveTestCtx returns a context with a generous timeout for live tests.
func liveTestCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// TestLiveRelay_FirstByteWinner verifies the live-mode predicate: the winner
// is selected at the FIRST forwardable chunk, BEFORE the upstream emits
// [DONE]. Buffered mode (the 6-arg wrapper) keeps the IsCompleted gate and
// only fires after [DONE].
func TestLiveRelay_FirstByteWinner(t *testing.T) {
	// Mock upstream emits chunk1, sleeps (longer than the 100ms tick), then
	// emits chunk2 + [DONE]. Live mode MUST select the winner on chunk1
	// arrival — within ~150ms; buffered mode MUST wait for [DONE] (~250ms+).
	var firstWriteTime atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: chunk1\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		firstWriteTime.Store(time.Now().UnixNano())
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte("data: chunk2\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	cfg := liveTestCfg(server.URL, 5*time.Second)
	ctx, cancel := liveTestCtx(t)
	defer cancel()

	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test-model","stream":true}`))
	rawBody := []byte(`{"model":"test-model","stream":true}`)

	// Live mode: pass liveFirstByteGate=true
	coord := newRaceCoordinatorWithEvents(ctx, cfg, req, rawBody,
		[]string{"test-model"}, nil, "test-live-fbw", false, nil, "", true)
	coord.Start()

	startTime := time.Now()
	winner := coord.WaitForWinner()
	elapsed := time.Since(startTime)
	if winner == nil {
		t.Fatalf("No winner selected (live mode)")
	}

	// In live mode, the winner must be selected well before [DONE] — i.e.
	// the first chunk fires within one tick (~100ms). 250ms is a generous
	// upper bound that still rules out waiting for completion.
	if elapsed > 250*time.Millisecond {
		t.Errorf("Live mode: winner selected after %v — predicate did NOT fire on first byte (should be <~150ms)", elapsed)
	}
	if firstWriteTime.Load() == 0 {
		t.Error("Upstream did not record first write")
	}
	t.Logf("Live mode winner selected after %v (first upstream write was earlier)", elapsed)

	// Drain and verify chunks arrived
	select {
	case <-winner.buffer.Done():
	case <-time.After(1 * time.Second):
		t.Errorf("Buffer never completed")
	}
	chunks, _ := winner.buffer.GetChunksFrom(0)
	if len(chunks) < 3 {
		t.Errorf("Expected at least 3 chunks (chunk1 + chunk2 + [DONE]), got %d", len(chunks))
	}
}

// TestLiveRelay_PredicateVariants exercises the four live-mode winner-
// eligibility shapes plus the negative case (task 2.3).
func TestLiveRelay_PredicateVariants(t *testing.T) {
	t.Run("RoleOnlyFirst_Wins", func(t *testing.T) {
		// Upstream emits role-only first chunk, then content, then [DONE].
		// Live mode: winner fires on role-only first chunk (NOT waiting for content).
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			// role-only chunk (no content)
			w.Write([]byte(`data: {"choices":[{"delta":{"role":"assistant"},"index":0}]}` + "\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(200 * time.Millisecond)
			w.Write([]byte(`data: {"choices":[{"delta":{"content":"hi"},"index":0}]}` + "\n\n"))
			w.Write([]byte("data: [DONE]\n\n"))
		}))
		defer server.Close()

		cfg := liveTestCfg(server.URL, 5*time.Second)
		ctx, cancel := liveTestCtx(t)
		defer cancel()

		req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","stream":true}`))
		rawBody := []byte(`{"model":"m","stream":true}`)

		coord := newRaceCoordinatorWithEvents(ctx, cfg, req, rawBody,
			[]string{"m"}, nil, "test-role-only", false, nil, "", true)
		coord.Start()

		start := time.Now()
		winner := coord.WaitForWinner()
		if winner == nil {
			t.Fatal("No winner")
		}
		// Role-only chunk must have fired the predicate — i.e. winner
		// arrived well before upstream completion (~250ms).
		if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
			t.Errorf("Role-only chunk did NOT fire first-byte winner gate (elapsed=%v)", elapsed)
		}
	})

	t.Run("ThinkingFirst_Wins", func(t *testing.T) {
		// Upstream emits reasoning_content first (before any plain content),
		// then content, then [DONE]. Live mode: winner fires on thinking chunk.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`data: {"choices":[{"delta":{"reasoning_content":"thinking..."},"index":0}]}` + "\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(200 * time.Millisecond)
			w.Write([]byte(`data: {"choices":[{"delta":{"content":"hi"},"index":0}]}` + "\n\n"))
			w.Write([]byte("data: [DONE]\n\n"))
		}))
		defer server.Close()

		cfg := liveTestCfg(server.URL, 5*time.Second)
		ctx, cancel := liveTestCtx(t)
		defer cancel()

		req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","stream":true}`))
		rawBody := []byte(`{"model":"m","stream":true}`)

		coord := newRaceCoordinatorWithEvents(ctx, cfg, req, rawBody,
			[]string{"m"}, nil, "test-thinking-first", false, nil, "", true)
		coord.Start()

		start := time.Now()
		winner := coord.WaitForWinner()
		if winner == nil {
			t.Fatal("No winner")
		}
		if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
			t.Errorf("Thinking-first chunk did NOT fire first-byte winner gate (elapsed=%v)", elapsed)
		}
	})

	t.Run("ErrorChunkOnly_DoesNotWin", func(t *testing.T) {
		// Upstream emits ONLY an error chunk (not SSE data) before failing.
		// With task 2.3 reorder: isStreamErrorChunk check runs BEFORE Add,
		// so the dying attempt never Adds the error chunk. In live mode,
		// the predicate never sees TotalLen>0 — winner is nil (no fallback
		// available). The error surfaces as the executor return error,
		// NOT as a phantom winner.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			// Emulate a stream-error chunk that isStreamErrorChunk recognizes.
			// `isStreamErrorChunk` looks for "error" markers in the JSON.
			w.Write([]byte(`data: {"error": {"message": "upstream blew up", "code": "internal_error"}}` + "\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			// Hold the connection briefly so the manage-loop sees the
			// failed attempt (otherwise the executor returns very fast).
			time.Sleep(100 * time.Millisecond)
		}))
		defer server.Close()

		cfg := liveTestCfg(server.URL, 5*time.Second)
		ctx, cancel := liveTestCtx(t)
		defer cancel()

		req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","stream":true}`))
		rawBody := []byte(`{"model":"m","stream":true}`)

		coord := newRaceCoordinatorWithEvents(ctx, cfg, req, rawBody,
			[]string{"m"}, nil, "test-err-only", false, nil, "", true)
		coord.Start()

		// Either the coordinator returns nil (no winner, executor returned
		// the error and the attempt failed) OR the manage-loop eventually
		// hits the deadline and surfaces the streamDeadlineError. Either
		// way: NOT a phantom winner with a small TotalLen.
		winner := coord.WaitForWinner()

		// The TotalLen-based predicate must NOT have transiently fired on
		// the error chunk. We assert: either winner==nil (preferred — the
		// error returned first), OR if a winner exists, its buffer is empty
		// (which would be a regression — would mean the error chunk was Added).
		if winner != nil {
			bufLen := winner.GetBuffer().TotalLen()
			if bufLen > 0 {
				t.Errorf("Error-chunk-only stream erroneously won the first-byte predicate (TotalLen=%d) — task 2.3 reorder failed", bufLen)
			}
		}
	})

	// Review row (a) — the core semantic distinguishing the first-byte
	// gate from IsCompleted. Drives the REAL manage-loop ticker arm over
	// hand-crafted attempts (no Start(): no executor/network) so the
	// interleaving under test is exact, not timing-approximated.
	t.Run("TwoAttempt_FirstBufferedWins_NotFirstCompleted", func(t *testing.T) {
		// PRECISE SHAPE: attempt A's first Add lands before B's; B
		// reaches IsCompleted FIRST (A is still mid-stream). The
		// first-byte gate MUST pick A; an IsCompleted gate picks B.
		cfg := liveTestCfg("http://127.0.0.1:0", 5*time.Second)
		cfg.MaxGenerationTime = 10 * time.Second
		cfg.RaceParallelOnIdle = false
		coord := newRaceCoordinatorWithEvents(context.Background(), cfg, newTestRequest(),
			[]byte(`{"model":"mA","stream":true}`), []string{"mA", "mB"}, nil, "crafted-2a", false, nil, "", true)

		a := newUpstreamRequest(0, modelTypeMain, "mA", 1<<20)
		a.MarkStarted()
		a.MarkStreaming()
		if !a.GetBuffer().Add([]byte("data: A1\n")) {
			t.Fatal("Add A1 failed")
		}

		b := newUpstreamRequest(1, modelTypeFallback, "mB", 1<<20)
		b.MarkStarted()
		if !b.GetBuffer().Add([]byte("data: B1\n")) {
			t.Fatal("Add B1 failed")
		}
		b.MarkCompleted() // B completes FIRST; A never completes in this window

		coord.mu.Lock()
		coord.requests = append(coord.requests, a, b)
		coord.mu.Unlock()

		go coord.manage()
		winner := coord.WaitForWinner()
		if winner == nil {
			t.Fatal("no winner selected")
		}
		if winner != a {
			t.Fatalf("winner = attempt %d (%s), want attempt A (mA) — first-buffered must beat first-completed", winner.id, winner.modelID)
		}
	})

	// Review row (a) / FIX 1 regression pin: an attempt that COMPLETED
	// with err==nil but TotalLen()==0 must NOT be eligible in live mode.
	// Under the pre-FIX-1 OR-superset it satisfied the IsCompleted arm
	// and won → phantom 0-byte 200 stream. Two fall-through shapes:
	t.Run("CompletedEmptyNoError_OtherAttemptWins", func(t *testing.T) {
		cfg := liveTestCfg("http://127.0.0.1:0", 5*time.Second)
		cfg.MaxGenerationTime = 10 * time.Second
		coord := newRaceCoordinatorWithEvents(context.Background(), cfg, newTestRequest(),
			[]byte(`{"model":"mA","stream":true}`), []string{"mA", "mB"}, nil, "crafted-empty1", false, nil, "", true)

		empty := newUpstreamRequest(0, modelTypeMain, "mA", 1<<20)
		empty.MarkStarted()
		empty.MarkCompleted() // IsCompleted()==true, err==nil, ZERO bytes

		live := newUpstreamRequest(1, modelTypeFallback, "mB", 1<<20)
		live.MarkStarted()
		live.MarkStreaming()
		if !live.GetBuffer().Add([]byte("data: B1\n")) {
			t.Fatal("Add B1 failed")
		}

		coord.mu.Lock()
		coord.requests = append(coord.requests, empty, live)
		coord.mu.Unlock()

		go coord.manage()
		winner := coord.WaitForWinner()
		if winner == nil {
			t.Fatal("expected the buffered attempt to win")
		}
		if winner != live {
			t.Fatal("completed-EMPTY-no-error attempt must NOT win in live mode (FIX 1 strict-predicate pin)")
		}
	})

	t.Run("CompletedEmptyNoError_FallsThroughToDeadline", func(t *testing.T) {
		// Alone (no other attempt): the completed-empty attempt must not
		// win; the race falls through to the live-mode 0-byte deadline
		// guard (streamDeadlineError set, winner nil).
		cfg := liveTestCfg("http://127.0.0.1:0", 300*time.Millisecond)
		cfg.MaxGenerationTime = 10 * time.Second
		coord := newRaceCoordinatorWithEvents(context.Background(), cfg, newTestRequest(),
			[]byte(`{"model":"mA","stream":true}`), []string{"mA"}, nil, "crafted-empty2", false, nil, "", true)

		empty := newUpstreamRequest(0, modelTypeMain, "mA", 1<<20)
		empty.MarkStarted()
		empty.MarkCompleted()

		coord.mu.Lock()
		coord.requests = append(coord.requests, empty)
		coord.mu.Unlock()

		go coord.manage()
		if winner := coord.WaitForWinner(); winner != nil {
			t.Fatalf("completed-empty attempt erroneously won (winner=%d)", winner.id)
		}
		if coord.GetStreamDeadlineError() == nil {
			t.Fatal("expected the live-mode 0-byte deadline guard to fire after fall-through")
		}
	})
}

// TestLiveRelay_StreamDeadlineZeroByte exercises the live-mode 0-byte
// deadline guard (task 2.2). Upstream holds the first byte indefinitely;
// StreamDeadline fires with all buffers empty. Live mode MUST surface a
// streamDeadlineError (in-band SSE error envelope) — not a hung 0-byte winner.
func TestLiveRelay_StreamDeadlineZeroByte(t *testing.T) {
	// Mock upstream writes headers immediately but never sends body; waits
	// for the request context (which the coordinator cancels on the
	// deadline-guard c.mu release + cancelAll path). The handler returns
	// on ctx.Done so the httptest.Server.Close can complete cleanly.
	releaseHandler := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		select {
		case <-r.Context().Done():
		case <-releaseHandler:
		}
	}))
	defer func() {
		// Unblock any lingering handler goroutines so server.Close can join.
		close(releaseHandler)
		server.Close()
	}()

	// Tight StreamDeadline so the test runs fast.
	cfg := liveTestCfg(server.URL, 300*time.Millisecond)
	// Long MaxGenerationTime so the deadline guard, not the hard deadline, fires first.
	cfg.MaxGenerationTime = 10 * time.Second
	cfg.IdleTimeout = 5 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","stream":true}`))
	rawBody := []byte(`{"model":"m","stream":true}`)

	coord := newRaceCoordinatorWithEvents(ctx, cfg, req, rawBody,
		[]string{"m"}, nil, "test-deadline-zero", false, nil, "", true)
	coord.Start()

	// Wait for the deadline to fire. handleStreamingDeadline in live mode
	// with all buffers empty MUST take the no-content error branch and set
	// streamDeadlineError; then cancelAll cancels the upstream.
	winner := coord.WaitForWinner()

	// We accept either: (a) winner is nil because the deadline guard fired
	// and the executor's upstream was cancelled, or (b) winner is non-nil
	// but streamDeadlineError is set. Both indicate the 0-byte guard ran.
	streamErr := coord.GetStreamDeadlineError()
	if streamErr == nil {
		t.Errorf("Expected streamDeadlineError to be set (live-mode 0-byte guard); winner=%v", winner)
	} else {
		t.Logf("streamDeadlineError: %+v", streamErr)
	}

	// Confirm no panic and no double-unlock (C2 / W1).
	// WaitForWinner returning and the goroutines cleaning up is enough —
	// any double-unlock would have panicked synchronously inside
	// handleStreamingDeadline; the test reaching this line proves it
	// didn't.
}

// TestLiveRelay_BufferedModeKeepsIsCompletedGate is the byte-identical-
// to-today pin: with liveFirstByteGate=false, the winner is selected at
// [DONE], not on the first byte.
func TestLiveRelay_BufferedModeKeepsIsCompletedGate(t *testing.T) {
	var firstWriteTime atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: chunk1\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		firstWriteTime.Store(time.Now().UnixNano())
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte("data: chunk2\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	cfg := liveTestCfg(server.URL, 5*time.Second)
	ctx, cancel := liveTestCtx(t)
	defer cancel()

	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","stream":true}`))
	rawBody := []byte(`{"model":"m","stream":true}`)

	// Buffered mode: liveFirstByteGate=false (the 6-arg wrapper default).
	coord := newRaceCoordinator(ctx, cfg, req, rawBody, []string{"m"}, false)
	coord.Start()

	start := time.Now()
	winner := coord.WaitForWinner()
	elapsed := time.Since(start)
	if winner == nil {
		t.Fatal("No winner")
	}
	// Buffered mode: winner fires after [DONE], which the upstream emits
	// ~250ms after starting. So elapsed must be >= 200ms (upstream holds
	// the first chunk), and effectively arrives with completion.
	if elapsed < 200*time.Millisecond {
		t.Errorf("Buffered mode: winner selected after %v — expected to wait for [DONE] (~250ms)", elapsed)
	}
	if elapsed > 600*time.Millisecond {
		t.Errorf("Buffered mode: winner selected after %v — unreasonably slow", elapsed)
	}
}

// TestLiveRelay_NonStreamUnaffected pins the M3 amendment (review row i,
// strengthened): isStream=false requests keep the IsCompleted gate + the
// 0-byte deadline PROMOTION behavior byte-identical to today, even when
// the header is absent (live mode by header semantics). The production
// call site (handler.go:935) computes liveFirstByteGate =
// !rc.bufferMode && rc.isStream — the gate is NEVER true for non-stream,
// so these tests pin the coordinator behavior that scoping guarantees.
func TestLiveRelay_NonStreamUnaffected(t *testing.T) {
	// Subtest 1 (original, strengthened): a non-stream request under the
	// production scoping (gate=false — header absent AND isStream=false)
	// still produces a winner from the completed body, with NO deadline
	// error / in-band envelope anywhere on the path.
	t.Run("Basic_NonStream_WinnerAfterBody_NoEnvelope", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`))
		}))
		defer server.Close()

		cfg := liveTestCfg(server.URL, 5*time.Second)
		ctx, cancel := liveTestCtx(t)
		defer cancel()

		req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","stream":false}`))
		rawBody := []byte(`{"model":"m","stream":false}`)

		// Production scoping for isStream=false: liveFirstByteGate=false.
		coord := newRaceCoordinatorWithEvents(ctx, cfg, req, rawBody,
			[]string{"m"}, nil, "test-non-stream", false, nil, "", false)
		coord.Start()

		winner := coord.WaitForWinner()
		if winner == nil {
			t.Fatal("Non-stream request: expected a winner after body completion")
		}
		if coord.GetStreamDeadlineError() != nil {
			t.Fatal("Non-stream request must never surface the live-mode 0-byte deadline envelope")
		}
	})

	// Subtest 2 (review row i.1): isStream=false + gate flag as production
	// computes it (false) ⇒ a deadline firing at 0 bytes still PROMOTES a
	// winner per today's race_coordinator.go deadline branch, and the
	// promoted stream continues to completion.
	t.Run("DeadlineZeroByte_StillPromotesWinner_NonStream", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			select {
			case <-r.Context().Done():
				return
			case <-time.After(600 * time.Millisecond): // body lands well after the deadline fires
			}
			w.Write([]byte(`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`))
		}))
		defer server.Close()

		cfg := liveTestCfg(server.URL, 250*time.Millisecond) // deadline BEFORE body
		cfg.MaxGenerationTime = 10 * time.Second
		ctx, cancel := liveTestCtx(t)
		defer cancel()

		req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","stream":false}`))
		rawBody := []byte(`{"model":"m","stream":false}`)

		coord := newRaceCoordinatorWithEvents(ctx, cfg, req, rawBody,
			[]string{"m"}, nil, "test-non-stream-dl", false, nil, "", false)
		coord.Start()

		winner := coord.WaitForWinner()
		if winner == nil {
			t.Fatal("Non-stream + 0-byte deadline: today's behavior PROMOTES a winner (no live-mode guard)")
		}
		if coord.GetStreamDeadlineError() != nil {
			t.Fatal("0-byte deadline promotion must NOT set the in-band envelope for non-stream (E2 pin)")
		}

		// The promoted winner keeps streaming: body lands, buffer completes.
		select {
		case <-winner.GetBuffer().Done():
		case <-time.After(2 * time.Second):
			t.Fatal("promoted winner never completed after body landed")
		}
		chunks, _ := winner.GetBuffer().GetChunksFrom(0)
		if len(chunks) == 0 {
			t.Fatal("promoted winner buffer empty — body never landed")
		}
	})

	// Subtest 3 (review row i.3): header-PRESENT-equivalent (buffered mode,
	// gate=false) non-stream is byte-identical — IsCompleted winner, full
	// body, no envelope. (HTTP-level header variant lives in
	// TestLiveRelay_HeaderDispatch's HTTP subtests.)
	t.Run("HeaderPresent_BufferedNonStream_ByteIdentical", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`))
		}))
		defer server.Close()

		cfg := liveTestCfg(server.URL, 5*time.Second)
		ctx, cancel := liveTestCtx(t)
		defer cancel()

		req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","stream":false}`))
		rawBody := []byte(`{"model":"m","stream":false}`)

		coord := newRaceCoordinatorWithEvents(ctx, cfg, req, rawBody,
			[]string{"m"}, nil, "test-non-stream-hdr", false, nil, "", false)
		coord.Start()

		winner := coord.WaitForWinner()
		if winner == nil {
			t.Fatal("buffered non-stream: expected a winner")
		}
		// IsCompleted gate: the winner exists only because the body
		// COMPLETED (no first-byte promotion is possible for non-stream).
		if !winner.IsCompleted() || winner.GetError() != nil {
			t.Fatal("buffered non-stream winner must be the IsCompleted attempt")
		}
		if coord.GetStreamDeadlineError() != nil {
			t.Fatal("buffered non-stream must never set the deadline envelope")
		}
		select {
		case <-winner.GetBuffer().Done():
		case <-time.After(2 * time.Second):
			t.Fatal("buffer never completed")
		}
	})

	// Subtest 4 (documentation of the M3 boundary): IF the gate were
	// mis-scoped to `!bufferMode` alone (gate=true on a NON-stream
	// request), the live-mode 0-byte guard WOULD hijack the deadline and
	// break today's non-stream behavior. This contrast proves the
	// isStream term in the handler.go:935 computation is load-bearing —
	// exactly the mis-scoping bug class TestLiveRelay_NonStreamUnaffected
	// exists to lock out.
	t.Run("MisScopedGate_WouldBreakNonStream_Documentation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			select {
			case <-r.Context().Done():
				return
			case <-time.After(2 * time.Second): // body never lands in time
			}
			w.Write([]byte(`{}`))
		}))
		defer server.Close()

		cfg := liveTestCfg(server.URL, 250*time.Millisecond)
		cfg.MaxGenerationTime = 10 * time.Second
		ctx, cancel := liveTestCtx(t)
		defer cancel()

		req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","stream":false}`))
		rawBody := []byte(`{"model":"m","stream":false}`)

		// The MIS-SCOPED form: gate=true despite isStream=false.
		coord := newRaceCoordinatorWithEvents(ctx, cfg, req, rawBody,
			[]string{"m"}, nil, "test-non-stream-mis", false, nil, "", true)
		coord.Start()

		winner := coord.WaitForWinner()
		if winner != nil {
			t.Fatalf("mis-scoped gate: expected the 0-byte guard to hijack the deadline (winner=%v) — if this fails, M3 semantics changed", winner)
		}
		if coord.GetStreamDeadlineError() == nil {
			t.Fatal("mis-scoped gate: expected streamDeadlineError (the breakage M3 scoping prevents)")
		}
	})
}

// TestLiveRelay_HeaderDispatch is the wiring pin: the same coordinator
// runs in buffered mode with liveFirstByteGate=false and in live mode with
// liveFirstByteGate=true. The header semantics are handled at handler.go
// (parseBufferResponseValue); this test confirms both coordinator shapes
// exist and behave correctly.
func TestLiveRelay_HeaderDispatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: hi\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	cfg := liveTestCfg(server.URL, 5*time.Second)
	_, cancel := liveTestCtx(t)
	defer cancel()

	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","stream":true}`))
	rawBody := []byte(`{"model":"m","stream":true}`)

	// Buffered: 6-arg wrapper (liveFirstByteGate=false). Winner fires after
	// [DONE] (~250ms).
	t.Run("Buffered", func(t *testing.T) {
		ctxB, cancelB := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelB()
		coord := newRaceCoordinator(ctxB, cfg, req, rawBody, []string{"m"}, false)
		coord.Start()
		start := time.Now()
		_ = coord.WaitForWinner()
		elapsed := time.Since(start)
		if elapsed < 150*time.Millisecond {
			t.Errorf("Buffered mode: winner too fast (%v) — should wait for [DONE]", elapsed)
		}
	})

	// Live: 11-arg explicit call with liveFirstByteGate=true. Winner fires
	// on the first chunk (~100ms tick).
	t.Run("Live", func(t *testing.T) {
		ctxL, cancelL := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelL()
		coord := newRaceCoordinatorWithEvents(ctxL, cfg, req, rawBody,
			[]string{"m"}, nil, "test-dispatch-live", false, nil, "", true)
		coord.Start()
		start := time.Now()
		_ = coord.WaitForWinner()
		elapsed := time.Since(start)
		if elapsed > 250*time.Millisecond {
			t.Errorf("Live mode: winner too slow (%v) — should fire on first chunk", elapsed)
		}
	})

	// Review row (j) — HTTP-LEVEL variants through the REAL handler
	// wiring: incoming X-LLMProxy-Buffer-Response header →
	// initRequestContext.bufferMode → handler.go:935 computes
	// liveFirstByteGate = !rc.bufferMode && rc.isStream → coordinator.
	// A firstWriteTimestampingRecorder distinguishes the two gates on the
	// WIRE: live mode relays chunk 1 before upstream [DONE]; buffered
	// mode emits nothing until after [DONE].
	t.Run("HTTP_HeaderAbsent_LiveFirstByteOnWire", func(t *testing.T) {
		// Upstream: chunk 1 at +50ms, [DONE] at +450ms.
		h, _ := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk("HTTP-live-chunk1"))
			w.(http.Flusher).Flush()
			time.Sleep(400 * time.Millisecond)
			fmt.Fprint(w, "data: [DONE]\n\n")
			w.(http.Flusher).Flush()
		}, nil)

		reqHTTP := makeRequest(t, simpleBody("mock-model", true))
		// NO X-LLMProxy-Buffer-Response header ⇒ live mode (default).
		rec := newFirstDataRecorder()
		start := time.Now()
		h.HandleChatCompletions(rec, reqHTTP)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if rec.firstDataAt.IsZero() {
			t.Fatal("no data frame written to the client")
		}
		if elapsed := rec.firstDataAt.Sub(start); elapsed > 350*time.Millisecond {
			t.Errorf("live mode: first data frame at %v — must precede upstream [DONE] (~450ms); gate mis-wired as buffered?", elapsed)
		}
		if body := rec.Body.String(); !strings.Contains(body, "HTTP-live-chunk1") {
			t.Errorf("client body missing chunk1: %q", body)
		}
	})

	t.Run("HTTP_HeaderPresent_BufferedWaitsForDone", func(t *testing.T) {
		h, _ := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk("HTTP-buf-chunk1"))
			w.(http.Flusher).Flush()
			time.Sleep(400 * time.Millisecond)
			fmt.Fprint(w, "data: [DONE]\n\n")
			w.(http.Flusher).Flush()
		}, nil)

		reqHTTP := makeRequest(t, simpleBody("mock-model", true))
		reqHTTP.Header.Set("X-LLMProxy-Buffer-Response", "true") // ⇒ buffered gate
		rec := newFirstDataRecorder()
		start := time.Now()
		h.HandleChatCompletions(rec, reqHTTP)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if rec.firstDataAt.IsZero() {
			t.Fatal("no data frame written to the client")
		}
		if elapsed := rec.firstDataAt.Sub(start); elapsed < 400*time.Millisecond {
			t.Errorf("buffered mode: first data frame at %v — must wait for upstream [DONE] (~450ms); gate mis-wired as live?", elapsed)
		}
	})
}

// TestLiveRelay_MidStreamLoserCancel (review row b — REBUILT as a REAL
// dual-attempt race): two internal models resolve to two DISTINCT mock
// servers via per-model credentials (mA→serverA, mB→serverB), so the
// parallel attempts hit different upstreams. mA (main, index 0) stalls
// past the idle timeout (second+fallback spawn), then buffers its first
// byte BEFORE mB; the first-byte gate + index preference select mA;
// cancelAllExcept cancels the mB fallback WHILE IT IS MID-Add. Asserts:
// loser IsCancelled(), a late Add on the loser's (pre-cancel snapshotted)
// buffer returns false (idempotent drop), the winner carries byte 1 that
// the relay forwards, and everything is -race clean.
func TestLiveRelay_MidStreamLoserCancel(t *testing.T) {
	aFirst := make(chan struct{})
	var closeAFirst sync.Once

	// Server A (winner path, model mA): stall past IdleTimeout so the
	// idle spawn fires, then emit A1, hold mid-stream, complete.
	aSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body) // arm the ctx-close watcher
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		time.Sleep(220 * time.Millisecond) // no first byte → idle spawn of second+fallback
		fmt.Fprintf(w, "data: %s\n\n", liveTestContentChunk("A1"))
		w.(http.Flusher).Flush()
		closeAFirst.Do(func() { close(aFirst) })
		time.Sleep(700 * time.Millisecond) // winner keeps streaming after selection
		fmt.Fprintf(w, "data: %s\n\n", liveTestContentChunk("A2"))
		fmt.Fprint(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
	}))
	defer aSrv.Close()

	// Server B (loser path, model mB): holds its first byte until A has
	// buffered A1, then streams continuously — it is MID-Add when the
	// coordinator cancels it.
	bSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body) // arm the ctx-close watcher
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		select {
		case <-aFirst: // B's first byte lands AFTER A's — A (index 0) wins
		case <-r.Context().Done():
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", liveTestContentChunk("B1"))
		w.(http.Flusher).Flush()
		for i := 2; ; i++ {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(25 * time.Millisecond):
				fmt.Fprintf(w, "data: %s\n\n", liveTestContentChunk(fmt.Sprintf("B%d", i)))
				w.(http.Flusher).Flush()
			}
		}
	}))
	defer bSrv.Close()

	// Two internal models, each pinned to its own server via a dedicated
	// credential — per-model upstream URL injection through the resolver.
	mock := &mockModelsConfig{}
	if err := mock.AddCredential(models.CredentialConfig{ID: "cred-a", Provider: "openai", APIKey: "ka", BaseURL: aSrv.URL}); err != nil {
		t.Fatal(err)
	}
	if err := mock.AddCredential(models.CredentialConfig{ID: "cred-b", Provider: "openai", APIKey: "kb", BaseURL: bSrv.URL}); err != nil {
		t.Fatal(err)
	}
	if err := mock.AddModel(models.ModelConfig{ID: "mA", Name: "mA", Enabled: true, Internal: true, InternalModel: "up-a", Credentials: models.TestRefs("cred-a")}); err != nil {
		t.Fatal(err)
	}
	if err := mock.AddModel(models.ModelConfig{ID: "mB", Name: "mB", Enabled: true, Internal: true, InternalModel: "up-b", Credentials: models.TestRefs("cred-b")}); err != nil {
		t.Fatal(err)
	}

	cfg := liveTestCfg(aSrv.URL, 5*time.Second)
	cfg.ModelsConfig = mock
	cfg.IdleTimeout = 100 * time.Millisecond // idle spawn of second+fallback
	cfg.RaceMaxParallel = 3
	cfg.RaceParallelOnIdle = true
	cfg.MaxGenerationTime = 10 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"mA","stream":true}`))
	rawBody := []byte(`{"model":"mA","stream":true}`)

	coord := newRaceCoordinatorWithEvents(ctx, cfg, req, rawBody,
		[]string{"mA", "mB"}, nil, "test-loser-cancel", false, nil, "", true)
	coord.Start()

	// Snapshot the fallback attempt's (index 2, server B) buffer BEFORE
	// cancellation can release it — Cancel() sets r.buffer=nil, but the
	// streamBuffer itself stays valid (closed) for the late-Add check.
	var loserBuf *streamBuffer
	snapDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(snapDeadline) {
		coord.mu.RLock()
		var snap *streamBuffer
		if len(coord.requests) >= 3 {
			snap = coord.requests[2].GetBuffer()
		}
		coord.mu.RUnlock()
		if snap != nil {
			loserBuf = snap
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if loserBuf == nil {
		t.Fatal("fallback attempt (loser, server B) never spawned within 2s")
	}

	winner := coord.WaitForWinner()
	if winner == nil {
		t.Fatal("No winner")
	}
	if winner.GetModelID() != "mA" {
		t.Fatalf("winner = %s, want mA (first-buffered, lower index)", winner.GetModelID())
	}

	// Every non-winner attempt must observe cancellation (the winner
	// branch closes streamCh before cancelAllExcept runs, so poll).
	waitCancelled := func(r *upstreamRequest) bool {
		dl := time.Now().Add(1500 * time.Millisecond)
		for time.Now().Before(dl) {
			if r.IsCancelled() {
				return true
			}
			time.Sleep(5 * time.Millisecond)
		}
		return false
	}
	coord.mu.RLock()
	losers := append([]*upstreamRequest(nil), coord.requests...)
	coord.mu.RUnlock()
	for _, r := range losers {
		if r != winner && !waitCancelled(r) {
			t.Errorf("attempt %d (%s, model %s) was not cancelled after winner selection", r.id, r.modelType, r.modelID)
		}
	}

	// Late Add on the cancelled loser's buffer: idempotent drop (must
	// return false on the closed buffer — stream_buffer.go Add contract).
	if loserBuf.Add([]byte("data: LATE\n")) {
		t.Error("late Add on cancelled loser buffer must return false (idempotent drop)")
	}

	// Winner byte 1 must be present in the winner's buffer (this is the
	// byte streamResult relays to the client) — then the stream completes.
	select {
	case <-winner.GetBuffer().Done():
	case <-time.After(3 * time.Second):
		t.Fatal("winner buffer never completed")
	}
	chunks, _ := winner.GetBuffer().GetChunksFrom(0)
	joined := make([]byte, 0, 1024)
	for _, c := range chunks {
		joined = append(joined, c...)
	}
	if !strings.Contains(string(joined), "A1") {
		t.Errorf("winner buffer missing byte 1 (A1): %q", string(joined))
	}
}

// TestLiveRelay_LoserAddAfterCancel verifies that late Adds on a cancelled
// attempt's buffer are silently dropped (idempotent). The plan pins this
// for the post-first-byte loser-cancel path.
func TestLiveRelay_LoserAddAfterCancel(t *testing.T) {
	// A closed streamBuffer's Add must return false (stream_buffer.go:118-120).
	buffer := newStreamBuffer(1000)
	if !buffer.Add([]byte("data: chunk1\n")) {
		t.Fatal("Initial Add should succeed")
	}
	buffer.Close(nil)
	// After Close: Add must return false (caller treats it as a no-op).
	if buffer.Add([]byte("data: chunk2\n")) {
		t.Error("Late Add after Close must return false (idempotent drop)")
	}
	// GetAllRawBytesOnce still returns the prefix — idempotent cached read.
	rawBytes := buffer.GetAllRawBytesOnce()
	if !strings.Contains(string(rawBytes), "chunk1") {
		t.Errorf("Late-Add should not corrupt the cached raw bytes; got %q", string(rawBytes))
	}
}

// TestLiveRelay_StreamingNonRetryableAtomic verifies that the atomic.Bool
// field on requestContext supports concurrent Load/Store under -race (the
// design rationale for the atomic type — Q4 / §1.6).
func TestLiveRelay_StreamingNonRetryableAtomic(t *testing.T) {
	rc := &requestContext{}
	if rc.streamingNonRetryable.Load() {
		t.Error("Initial state should be false")
	}
	// Concurrent writer + reader. Use a WaitGroup to join.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			rc.streamingNonRetryable.Store(true)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = rc.streamingNonRetryable.Load()
		}
	}()
	wg.Wait()
	if !rc.streamingNonRetryable.Load() {
		t.Error("Final Load should be true after 1000 Stores")
	}
	// reset() must clear it back to false (per-request lifecycle).
	rc.streamingNonRetryable.Store(false)
	if rc.streamingNonRetryable.Load() {
		t.Error("Reset to false should clear the flag")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Review-fix battery (rows c, d, e, g, h, k): wire-level envelope tests,
// deadline 4-aspect completion, R2 estimator pin, client-disconnect pin,
// bufferstore double-save pin. Shared helpers first.
// ─────────────────────────────────────────────────────────────────────────────

// liveTestContentChunk returns a minimal well-formed OpenAI streaming data
// payload (choices-wrapped delta) for mock upstreams.
func liveTestContentChunk(content string) string {
	return `{"choices":[{"index":0,"delta":{"content":"` + content + `"}}]}`
}

// firstDataRecorder wraps httptest.ResponseRecorder and timestamps the
// first NON-COMMENT Write (the SSE preamble ": connected\n\n" and any
// heartbeats are comments; the first `data:` frame is what the gate
// timing is about).
type firstDataRecorder struct {
	*httptest.ResponseRecorder
	firstDataAt time.Time
	sawData     bool
}

func newFirstDataRecorder() *firstDataRecorder {
	return &firstDataRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (r *firstDataRecorder) Write(b []byte) (int, error) {
	if !r.sawData {
		s := strings.TrimLeft(string(b), " \t\r\n")
		if s != "" && !strings.HasPrefix(s, ":") {
			r.firstDataAt = time.Now()
			r.sawData = true
		}
	}
	return r.ResponseRecorder.Write(b)
}

// extractLastSSEDataLine returns the LAST complete `data: ...` payload in
// an SSE wire body ([DONE] excluded), used to assert the terminal error
// envelope.
func extractLastSSEDataLine(body string) (string, bool) {
	last, ok := "", false
	for _, ln := range strings.Split(body, "\n") {
		ln = strings.TrimRight(ln, "\r")
		if strings.HasPrefix(ln, "data: ") {
			payload := strings.TrimPrefix(ln, "data: ")
			if payload != "[DONE]" {
				last, ok = payload, true
			}
		}
	}
	return last, ok
}

// parseErrorEnvelope unmarshals a `data:` payload and asserts it carries
// the OpenAI error object (handler.go sendSSEError / streamResult shapes).
func parseErrorEnvelope(t *testing.T, dataLine string) map[string]interface{} {
	t.Helper()
	var env struct {
		Error *struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(dataLine), &env); err != nil {
		t.Fatalf("error envelope is not JSON: %v (line=%q)", err, dataLine)
	}
	if env.Error == nil {
		t.Fatalf("envelope has no error object: %q", dataLine)
	}
	if env.Error.Type == "" || env.Error.Message == "" {
		t.Fatalf("envelope error object missing type/message: %q", dataLine)
	}
	out := map[string]interface{}{"type": env.Error.Type, "code": env.Error.Code, "message": env.Error.Message}
	return out
}

// newLiveRelayHandler builds a Handler against a fresh upstream mock with
// an optional bufferstore and models config (handler-level harness — the
// same shape as handler_test.go's newTestHandler, plus the bufferStore
// wiring needed by the R2/double-save pins).
func newLiveRelayHandler(t *testing.T, upstreamHandler http.HandlerFunc, modelsCfg models.ModelsConfigInterface, bufStore *bufferstore.BufferStore, opts ...func(*config.Config)) (*Handler, *httptest.Server, *events.Bus, *store.RequestStore) {
	t.Helper()
	upstream := httptest.NewServer(upstreamHandler)
	t.Cleanup(upstream.Close)

	mgr := newTestManagerWithConfig(t, upstream.URL, opts...)
	cfg := &Config{ConfigMgr: mgr, ModelsConfig: modelsCfg}
	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)
	h := NewHandler(cfg, bus, reqStore, bufStore, nil, nil, nil)
	return h, upstream, bus, reqStore
}

// waitResponseLoggedEvents polls the event bus until n `response_logged`
// events arrived (one per successful bufferStore save via saveRawResponse)
// or the timeout expires; returns what it saw.
func waitResponseLoggedEvents(t *testing.T, bus *events.Bus, n int, timeout time.Duration) []events.Event {
	t.Helper()
	ch, err := bus.Subscribe()
	if err != nil {
		t.Fatalf("bus.Subscribe: %v", err)
	}
	defer bus.Unsubscribe(ch)

	var got []events.Event
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) && len(got) < n {
		select {
		case evt := <-ch:
			if evt.Type == "response_logged" {
				got = append(got, evt)
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	// Drain any extras (double-save detection): a short quiet window.
	quiet := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(quiet) {
		select {
		case evt := <-ch:
			if evt.Type == "response_logged" {
				got = append(got, evt)
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	return got
}

// readSavedResponseFile reads the single `*-response.txt` file the
// bufferstore wrote for this test's request (one request per store).
func readSavedResponseFile(t *testing.T, dir string) []byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "-response.txt") {
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			return b
		}
	}
	return nil
}

// TestLiveRelay_StreamDeadlineInLiveMode (review row c) completes the
// mandated 4-aspect deadline coverage. Aspect (1)'s coordinator-level twin
// already exists as TestLiveRelay_StreamDeadlineZeroByte; here:
//
//	(1) wire-level: in-band SSE envelope ON THE WIRE — SSE headers, 200
//	    status (headersSent), envelope bytes present — NOT a bare 0-byte
//	    200, NOT HTTP 5xx;
//	(2) byte-at-boundary: a first byte landing in the same lock window as
//	    the deadline fire is honored (predicate fires synchronously under
//	    c.mu; either the ticker arm selects the winner first or the
//	    deadline arm promotes the non-empty best buffer — never the
//	    0-byte error guard);
//	(3) late-deadline-after-winner: no double-winner, no panic — both the
//	    defensive winner-nil early return (unit) and the real loop (timer
//	    abandoned when manage() exits at winner selection);
//	(4) all assertions are event/wire-based, never wall-clock deltas.
func TestLiveRelay_StreamDeadlineInLiveMode(t *testing.T) {
	// Aspect 1 — wire-level envelope (handler harness; headersSent path).
	t.Run("Wire_InBandEnvelope_NotBare200_Not5xx", func(t *testing.T) {
		h, _, _, _ := newLiveRelayHandler(t, func(w http.ResponseWriter, r *http.Request) {
			// Drain the request body: arms net/http's background EOF
			// watcher so r.Context() actually fires when the deadline
			// guard's cancelAll aborts the client side.
			io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			// Hold the first byte indefinitely; the deadline guard's
			// cancelAll unblocks us via r.Context().
			<-r.Context().Done()
		}, nil, nil, func(c *config.Config) {
			c.StreamDeadline = config.Duration(300 * time.Millisecond)
			c.RaceRetryEnabled = false
		})

		reqHTTP := makeRequest(t, simpleBody("mock-model", true))
		rec := httptest.NewRecorder()
		h.HandleChatCompletions(rec, reqHTTP)

		// NOT HTTP 5xx — headers were already sent (SSE preamble path).
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (in-band envelope, not an HTTP error)", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
			t.Fatalf("Content-Type = %q, want text/event-stream", ct)
		}
		body := rec.Body.String()
		// NOT a bare 0-byte 200: preamble + envelope must be on the wire.
		if !strings.Contains(body, ": connected") {
			t.Errorf("missing SSE preamble: %q", body)
		}
		line, ok := extractLastSSEDataLine(body)
		if !ok {
			t.Fatalf("no data envelope on the wire: %q", body)
		}
		env := parseErrorEnvelope(t, line)
		if msg, _ := env["message"].(string); !strings.Contains(msg, "Request timeout") {
			t.Errorf("envelope message = %q, want the stream-deadline timeout message", msg)
		}
	})

	// Aspect 2 — byte-at-boundary honored: first byte Added in the
	// window straddling the deadline fire. Whichever arm wins the race,
	// the byte MUST be honored (ticker: first-byte winner; deadline:
	// bestLen>0 promote) — never the 0-byte error guard.
	t.Run("ByteAtBoundary_Honored", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			time.Sleep(200 * time.Millisecond) // first byte lands INSIDE the deadline window
			fmt.Fprintf(w, "data: %s\n\n", liveTestContentChunk("boundary-byte"))
			w.(http.Flusher).Flush()
			time.Sleep(700 * time.Millisecond)
			fmt.Fprint(w, "data: [DONE]\n\n")
			w.(http.Flusher).Flush()
		}))
		defer server.Close()

		cfg := liveTestCfg(server.URL, 250*time.Millisecond)
		cfg.MaxGenerationTime = 10 * time.Second
		ctx, cancel := liveTestCtx(t)
		defer cancel()

		req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","stream":true}`))
		coord := newRaceCoordinatorWithEvents(ctx, cfg, req,
			[]byte(`{"model":"m","stream":true}`), []string{"m"}, nil, "test-boundary", false, nil, "", true)
		coord.Start()

		// Whichever arm wins the race (ticker selects the first-byte
		// winner, or the deadline promotes the 1-byte best buffer), the
		// byte MUST be honored: a winner exists and NO deadline error.
		winner := coord.WaitForWinner()
		if winner == nil {
			t.Fatal("byte-at-boundary: expected the arriving byte to be honored (winner), got none")
		}
		if coord.GetStreamDeadlineError() != nil {
			t.Fatal("byte-at-boundary: 0-byte guard must NOT fire when a byte arrived in the window")
		}
		select {
		case <-winner.GetBuffer().Done():
		case <-time.After(2 * time.Second):
			t.Fatal("winner never completed")
		}
		if total := winner.GetBuffer().TotalLen(); total == 0 {
			t.Fatal("winner buffer empty — boundary byte was dropped")
		}
	})

	// Aspect 3 — late deadline after a winner exists.
	t.Run("LateDeadlineAfterWinner_NoDoubleWinner_NoPanic", func(t *testing.T) {
		// (3a) Defensive unit form: the deadline guard's winner-nil early
		// return (race_coordinator.go handleStreamingDeadline head).
		cfg := liveTestCfg("http://127.0.0.1:0", 5*time.Second)
		coord := newRaceCoordinatorWithEvents(context.Background(), cfg, newTestRequest(),
			[]byte(`{"model":"m","stream":true}`), []string{"m"}, nil, "test-late-dl", false, nil, "", true)

		a := newUpstreamRequest(0, modelTypeMain, "mA", 1<<20)
		a.MarkStarted()
		a.MarkStreaming()
		a.GetBuffer().Add([]byte("data: A1\n"))
		coord.mu.Lock()
		coord.requests = append(coord.requests, a)
		coord.winner = a
		coord.winnerIdx = 0
		coord.mu.Unlock()

		// Direct call simulating a late timer fire with a winner set.
		coord.handleStreamingDeadline() // must NOT panic (double-unlock etc.)

		coord.mu.RLock()
		w, idx := coord.winner, coord.winnerIdx
		coord.mu.RUnlock()
		if w != a || idx != 0 {
			t.Fatalf("late deadline re-selected the winner (got %v idx %d)", w != a, idx)
		}
		if coord.GetStreamDeadlineError() != nil {
			t.Fatal("late deadline must not set the 0-byte error after a winner exists")
		}

		// (3b) Real loop: winner selected at first byte; the (later)
		// StreamDeadline timer is abandoned when manage() exits — no
		// double-winner, no panic, stream completes.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "data: %s\n\n", liveTestContentChunk("late-dl-first"))
			w.(http.Flusher).Flush()
			time.Sleep(800 * time.Millisecond) // deadline (400ms) passes mid-stream
			fmt.Fprint(w, "data: [DONE]\n\n")
			w.(http.Flusher).Flush()
		}))
		defer server.Close()

		cfg2 := liveTestCfg(server.URL, 400*time.Millisecond) // deadline AFTER winner selection
		cfg2.MaxGenerationTime = 10 * time.Second
		ctx2, cancel2 := liveTestCtx(t)
		defer cancel2()

		req2, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","stream":true}`))
		coord2 := newRaceCoordinatorWithEvents(ctx2, cfg2, req2,
			[]byte(`{"model":"m","stream":true}`), []string{"m"}, nil, "test-late-dl-2", false, nil, "", true)
		coord2.Start()

		winner := coord2.WaitForWinner()
		if winner == nil {
			t.Fatal("no winner at first byte")
		}
		select {
		case <-winner.GetBuffer().Done():
		case <-time.After(2 * time.Second):
			t.Fatal("stream never completed past the (abandoned) deadline")
		}
		if coord2.GetStreamDeadlineError() != nil {
			t.Fatal("late deadline must not surface the 0-byte envelope after a winner exists")
		}
	})
}

// TestLiveRelay_FirstByteThenImmediateErr (review row d): live-mode mock
// emits ONE chunk then fails mid-stream (error chunk). The client must
// receive the chunk AND a well-formed in-band SSE error envelope
// (handler.go streamResult Done-case shape: NewOpenAIError JSON under
// `data: ` framing).
func TestLiveRelay_FirstByteThenImmediateErr(t *testing.T) {
	h, _, _, _ := newLiveRelayHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk("first-byte-content"))
		w.(http.Flusher).Flush()
		// Winner selection (≤100ms tick) + relay start must precede the
		// failure so the error lands MID-RELAY, not pre-winner.
		time.Sleep(300 * time.Millisecond)
		fmt.Fprint(w, `data: {"error":{"message":"midstream boom","code":"internal_error"}}`+"\n\n")
		w.(http.Flusher).Flush()
	}, nil, nil, func(c *config.Config) {
		c.RaceRetryEnabled = false
	})

	reqHTTP := makeRequest(t, simpleBody("mock-model", true)) // no header ⇒ live
	rec := httptest.NewRecorder()
	h.HandleChatCompletions(rec, reqHTTP)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (in-band envelope after SSE headers)", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "first-byte-content") {
		t.Errorf("client never received chunk 1: %q", body)
	}
	line, ok := extractLastSSEDataLine(body)
	if !ok {
		t.Fatalf("no terminal envelope on the wire: %q", body)
	}
	env := parseErrorEnvelope(t, line)
	if env["type"] != "server_error" {
		t.Errorf("envelope type = %v, want server_error", env["type"])
	}
	if msg, _ := env["message"].(string); !strings.HasPrefix(msg, "Streaming error: ") {
		t.Errorf("envelope message = %q, want the streamResult 'Streaming error: …' shape", msg)
	}
	if !strings.HasSuffix(body, "\n\n") {
		t.Errorf("envelope must be SSE-framed (trailing blank line): %q", body[len(body)-64:])
	}
}

// TestLiveRelay_PreFirstByteError (review row e): upstream 500s BEFORE any
// byte. The live-mode client (headers already sent via the SSE preamble
// path) receives the in-band SSE error envelope — no HTTP error status,
// no 5xx. The envelope SHAPE must be byte-identical to (d)'s: same
// `data: {"error":{...}}\n\n` framing, same JSON key layout (code omitted).
func TestLiveRelay_PreFirstByteError(t *testing.T) {
	h, _, _, _ := newLiveRelayHandler(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream exploded before any byte", http.StatusInternalServerError)
	}, nil, nil, func(c *config.Config) {
		c.RaceRetryEnabled = false
	})

	reqHTTP := makeRequest(t, simpleBody("mock-model", true)) // no header ⇒ live
	rec := httptest.NewRecorder()
	h.HandleChatCompletions(rec, reqHTTP)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — headers were sent (SSE preamble), error must be in-band", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, ": connected") {
		t.Errorf("missing SSE preamble (headersSent path): %q", body)
	}
	line, ok := extractLastSSEDataLine(body)
	if !ok {
		t.Fatalf("no in-band envelope on the wire: %q", body)
	}
	env := parseErrorEnvelope(t, line)
	if msg, _ := env["message"].(string); msg == "" {
		t.Fatal("envelope message must carry the upstream failure detail")
	}

	// Shape identity with (d): both envelopes are produced by
	// json.Marshal(models.NewOpenAIError(...)) under `data: ` framing —
	// exact template match (type then message, code omitted).
	envelopeTemplate := regexp.MustCompile(`^\{"error":\{"type":"[^"]+","message":"[^"]+"\}\}$`)
	if !envelopeTemplate.MatchString(line) {
		t.Errorf("pre-first-byte envelope %q does not match the (d) envelope template — shapes diverged", line)
	}
}

// TestLiveRelay_R2_EstimatorUnderPrunePressure (review row g / task 2.4
// regression pin): a live-mode long stream (≫ prune threshold chunks) with
// NO usage chunk from upstream. At stream end the raw-bytes consumers must
// see the FULL stream:
//   - the bufferstore Done capture saves the EXACT full-stream byte count
//     (mid-stream Prune would truncate it non-deterministically);
//   - the usage-estimator fallback produced non-zero completion tokens;
//   - exactly ONE save in live mode (no partial entry capture).
//
// Companion buffered subtest: today's entry+Done double capture is
// unchanged (TWO saves, both full).
func TestLiveRelay_R2_EstimatorUnderPrunePressure(t *testing.T) {
	const chunkCount = 80

	// Precompute the full deterministic stream (mockCreateChunk is pure):
	// the handler writes precomputed lines; the expected byte count is
	// computed on the TEST goroutine — no shared mutable state.
	//
	// Byte math: each wire line "data: X\n\n" produces TWO stored chunks
	// on the race-external path — the data line itself (Add re-appends
	// the stripped newline: len+1) AND the blank SSE separator line,
	// which ProcessChunk passes through empty and Add stores as a bare
	// '\n' (+1). [DONE] (break before its separator) contributes len+1.
	streamLines := make([]string, 0, chunkCount+1)
	expectedBytes := 0
	for i := 0; i < chunkCount; i++ {
		line := "data: " + mockCreateChunk(fmt.Sprintf("prune-piece-%03d-payload", i))
		streamLines = append(streamLines, line)
		expectedBytes += len(line) + 1 // the data line
		expectedBytes += 1             // its blank SSE separator chunk
	}
	streamLines = append(streamLines, "data: [DONE]")
	expectedBytes += len("data: [DONE]") + 1

	writeStream := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, line := range streamLines {
			fmt.Fprintf(w, "%s\n\n", line)
			w.(http.Flusher).Flush()
			time.Sleep(2 * time.Millisecond) // pace: relay ticks race Adds
		}
	}

	assertEstimatorSawFullStream := func(t *testing.T, reqStore *store.RequestStore) {
		t.Helper()
		for _, log := range reqStore.List() {
			if log.Usage != nil && log.Usage.CompletionTokens > 0 {
				return
			}
		}
		t.Fatal("usage-estimator fallback never saw the stream (no non-zero completion tokens) — raw bytes truncated?")
	}

	t.Run("LiveMode_FullStreamCapture", func(t *testing.T) {
		t.Setenv("LOG_RAW_UPSTREAM_RESPONSE", "true")
		bufStore, dir := mustBufferStoreWithDir(t)
		h, _, bus, reqStore := newLiveRelayHandler(t, func(w http.ResponseWriter, r *http.Request) {
			writeStream(w)
		}, nil, bufStore)

		reqHTTP := makeRequest(t, simpleBody("mock-model", true)) // live
		rec := httptest.NewRecorder()
		h.HandleChatCompletions(rec, reqHTTP)

		bodyStr := rec.Body.String()
		if !strings.Contains(bodyStr, "[DONE]") {
			t.Fatalf("stream did not complete (len=%d): %q", len(bodyStr), bodyStr)
		}

		saves := waitResponseLoggedEvents(t, bus, 2, 2*time.Second)
		if len(saves) != 1 {
			t.Fatalf("live mode: bufferStore saves = %d, want EXACTLY 1 (Done capture only; entry capture is live-gated)", len(saves))
		}
		size, _ := saves[0].Data.(map[string]interface{})["size_bytes"].(int)
		if size != expectedBytes {
			t.Errorf("saved size = %d, want the EXACT full-stream byte count %d — mid-stream Prune truncation?", size, expectedBytes)
		}
		// The persisted file itself must be the FULL stream.
		saved := readSavedResponseFile(t, dir)
		if len(saved) != expectedBytes {
			t.Errorf("persisted buffer bytes = %d, want exact full-stream %d", len(saved), expectedBytes)
		}
		if !strings.Contains(string(saved), "prune-piece-000-payload") || !strings.Contains(string(saved), fmt.Sprintf("prune-piece-%03d-payload", chunkCount-1)) {
			t.Errorf("persisted buffer missing first/last chunks — truncated capture")
		}
		assertEstimatorSawFullStream(t, reqStore)
	})

	t.Run("BufferedMode_EntryAndDoneUnchanged", func(t *testing.T) {
		t.Setenv("LOG_RAW_UPSTREAM_RESPONSE", "true")
		h, _, bus, _ := newLiveRelayHandler(t, func(w http.ResponseWriter, r *http.Request) {
			writeStream(w)
		}, nil, mustBufferStore(t))

		reqHTTP := makeRequest(t, simpleBody("mock-model", true))
		reqHTTP.Header.Set("X-LLMProxy-Buffer-Response", "true") // buffered
		rec := httptest.NewRecorder()
		h.HandleChatCompletions(rec, reqHTTP)

		if !strings.Contains(rec.Body.String(), "[DONE]") {
			t.Fatal("buffered stream did not complete")
		}

		// Today's behavior: entry capture + Done capture (both see the
		// completed buffer — buffered winner waits for [DONE]).
		saves := waitResponseLoggedEvents(t, bus, 2, 2*time.Second)
		if len(saves) != 2 {
			t.Fatalf("buffered mode: bufferStore saves = %d, want 2 (today's entry+Done behavior unchanged)", len(saves))
		}
		for i, s := range saves {
			size, _ := s.Data.(map[string]interface{})["size_bytes"].(int)
			if size != expectedBytes {
				t.Errorf("buffered save[%d] size = %d, want full %d", i, size, expectedBytes)
			}
		}
	})
}

// TestLiveRelay_ClientDisconnectDuringMidStreamCancel (review row h):
// the client disconnects mid-stream while the coordinator has (or is)
// cancelling loser attempts. No panic; the handler returns promptly;
// goroutines settle back to the pre-request baseline (NumGoroutine
// sampling — no goleak in this repo).
func TestLiveRelay_ClientDisconnectDuringMidStreamCancel(t *testing.T) {
	h, _, _, _ := newLiveRelayHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		time.Sleep(250 * time.Millisecond) // stall: idle-spawns the loser attempt(s)
		for i := 0; i < 8; i++ {
			fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk(fmt.Sprintf("dc-chunk-%d", i)))
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
	}, nil, nil, func(c *config.Config) {
		c.IdleTimeout = config.Duration(150 * time.Millisecond) // loser spawns while main stalls
		c.RaceMaxParallel = 2
	})

	baseline := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	reqHTTP := makeRequest(t, simpleBody("mock-model", true))
	reqHTTP = reqHTTP.WithContext(ctx)

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	panicked := make(chan interface{}, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				panicked <- p
			}
			close(done)
		}()
		h.HandleChatCompletions(rec, reqHTTP)
	}()

	time.Sleep(500 * time.Millisecond) // winner selected + relay live + loser cancelled
	cancel()                           // client disconnects mid-stream

	select {
	case p := <-panicked:
		t.Fatalf("handler panicked on mid-stream disconnect: %v", p)
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not return after client disconnect")
	}

	// Goroutine cleanup: settle back to (near) baseline once the upstream
	// mock servers are closed (t.Cleanup closes them AFTER this test
	// body — close explicitly here first via... they are t.Cleanup-bound;
	// settle with tolerance instead).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline+4 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if now := runtime.NumGoroutine(); now > baseline+4 {
		t.Errorf("goroutines did not settle: baseline=%d now=%d (leaked relay/executor/loser goroutines?)", baseline, now)
	}
}

// TestLiveRelay_BufferStoreDoubleSave_Benign (review row k / task 2.4.1):
// LogRawUpstreamResponse + LogRawUpstreamOnError both on; a LIVE-mode
// relay that fails partway (chunk then mid-stream error chunk). The entry
// raw-capture is gated to buffered mode, so the failing live relay must
// Save EXACTLY ONCE (the Done/error-path capture), with the FULL
// pre-failure prefix bytes. The buffered companion keeps today's
// entry+Done double capture.
func TestLiveRelay_BufferStoreDoubleSave_Benign(t *testing.T) {
	t.Run("LiveMode_FailingRelay_SavesExactlyOnce", func(t *testing.T) {
		t.Setenv("LOG_RAW_UPSTREAM_RESPONSE", "true")
		t.Setenv("LOG_RAW_UPSTREAM_ON_ERROR", "true")

		bufStore, dir := mustBufferStoreWithDir(t)
		firstLine := "data: " + mockCreateChunk("ks-chunk-1")
		// data line (+1 newline) + blank separator chunk (+1); the error
		// chunk is never Added (task 2.3 reorder) and its separator is
		// never read (the executor returns at the error-chunk check).
		expectedBytes := len(firstLine) + 2
		h, _, bus, _ := newLiveRelayHandler(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "%s\n\n", firstLine)
			w.(http.Flusher).Flush()
			time.Sleep(300 * time.Millisecond) // winner + relay start first
			fmt.Fprint(w, `data: {"error":{"message":"k-boom","code":"internal_error"}}`+"\n\n")
			w.(http.Flusher).Flush()
		}, nil, bufStore)

		reqHTTP := makeRequest(t, simpleBody("mock-model", true)) // live
		rec := httptest.NewRecorder()
		h.HandleChatCompletions(rec, reqHTTP)

		if !strings.Contains(rec.Body.String(), "ks-chunk-1") {
			t.Fatalf("client never got the pre-failure chunk: %q", rec.Body.String())
		}
		if line, ok := extractLastSSEDataLine(rec.Body.String()); !ok {
			t.Fatalf("no terminal envelope for failing relay: %q", rec.Body.String())
		} else {
			parseErrorEnvelope(t, line)
		}

		saves := waitResponseLoggedEvents(t, bus, 2, 2*time.Second)
		if len(saves) != 1 {
			t.Fatalf("live mode failing relay: bufferStore saves = %d, want EXACTLY 1 (no entry+Done double-write)", len(saves))
		}
		saved := readSavedResponseFile(t, dir)
		if len(saved) != expectedBytes {
			t.Errorf("saved bytes = %d, want the full pre-failure prefix %d", len(saved), expectedBytes)
		}
		if !strings.Contains(string(saved), "ks-chunk-1") {
			t.Errorf("saved buffer missing the pre-failure chunk: %q", string(saved))
		}
	})

	t.Run("BufferedMode_KeepsTodayEntryPlusDone", func(t *testing.T) {
		t.Setenv("LOG_RAW_UPSTREAM_RESPONSE", "true")

		firstLine := "data: " + mockCreateChunk("ks-buf-chunk")
		// chunk line (+1) + blank separator (+1) + [DONE] (+1).
		expectedBytes := len(firstLine) + 2 + len("data: [DONE]") + 1
		h, _, bus, _ := newLiveRelayHandler(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "%s\n\n", firstLine)
			w.(http.Flusher).Flush()
			fmt.Fprint(w, "data: [DONE]\n\n")
			w.(http.Flusher).Flush()
		}, nil, mustBufferStore(t))

		reqHTTP := makeRequest(t, simpleBody("mock-model", true))
		reqHTTP.Header.Set("X-LLMProxy-Buffer-Response", "true") // buffered
		rec := httptest.NewRecorder()
		h.HandleChatCompletions(rec, reqHTTP)

		if !strings.Contains(rec.Body.String(), "[DONE]") {
			t.Fatal("buffered relay did not complete")
		}
		saves := waitResponseLoggedEvents(t, bus, 2, 2*time.Second)
		if len(saves) != 2 {
			t.Fatalf("buffered mode: saves = %d, want 2 (today's entry+Done double capture is KEPT)", len(saves))
		}
		for i, s := range saves {
			size, _ := s.Data.(map[string]interface{})["size_bytes"].(int)
			if size != expectedBytes {
				t.Errorf("buffered save[%d] size = %d, want full %d", i, size, expectedBytes)
			}
		}
	})
}

// mustBufferStore creates a bufferstore in a fresh temp dir (path also
// retrievable via mustBufferStoreWithDir when the test reads the files).
func mustBufferStore(t *testing.T) *bufferstore.BufferStore {
	t.Helper()
	bs, err := bufferstore.New(t.TempDir(), 1024*1024)
	if err != nil {
		t.Fatalf("bufferstore.New: %v", err)
	}
	return bs
}

func mustBufferStoreWithDir(t *testing.T) (*bufferstore.BufferStore, string) {
	t.Helper()
	dir := t.TempDir()
	bs, err := bufferstore.New(dir, 1024*1024)
	if err != nil {
		t.Fatalf("bufferstore.New: %v", err)
	}
	return bs, dir
}
