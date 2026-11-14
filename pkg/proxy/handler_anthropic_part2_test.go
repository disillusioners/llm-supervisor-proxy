package proxy

// wi-7769a812 part 2 — exhaustive pre-commit group fallback,
// Anthropic route (coverage PARITY with group_fallback_part2_test.go,
// MANDATE #3):
//
//	1. LATCH RETIREMENT (Anthropic leg): an N-member group makes up
//	   to N attempts in pool order — no re-serves — and the
//	   model_member_failover event fires per real re-entry.
//	2. WINDOW PARITY (lazy-preamble boundary): the route's OWN
//	   commit flag (arc.payloadCommitted) is set by forwarded
//	   PAYLOAD events only — SSE comments and ping keep-alives are
//	   NON-COMMITTING framing even though they set headersSent via
//	   the lazy preamble (writeLiveStreamPreamble). The fallback
//	   iteration gate reads payloadCommitted, NEVER headersSent.
//	3. P2 FAIL-FAST (Anthropic leg): a client-attributable 400 stops
//	   the walk after the FIRST member — exactly ONE upstream call.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// TestHandlerGroups_AnthropicReentry_ExhaustiveThreeMembers — the
// latch-retirement proof, Anthropic leg (MANDATE #6 parity): a
// 3-member group whose every attempt fails pre-commit makes EXACTLY 3
// upstream attempts, one per member (the tried-set refuses pool
// re-offers), with exactly 2 model_member_failover events (one per
// re-entry). The old one-re-entry latch stopped at 2 attempts.
func TestHandlerGroups_AnthropicReentry_ExhaustiveThreeMembers(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedExhaustGroup(t, h, "g-anth-exh")

	st := &exhaustStubState{status: http.StatusBadGateway}
	h.up.Close()
	h.up = httptest.NewServer(newExhaustStubUpstream(st))
	h.rebuildHandler(t)

	seen, stop := subscribeFailover(t, h)
	defer stop()

	rec := httptest.NewRecorder()
	req := makeAnthropicRequestNoAuth(t, map[string]interface{}{
		"model":      "g-anth-exh",
		"max_tokens": 16,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleAnthropicMessages(rec, req)

	calls, attempted := st.snapshot()
	if calls != 3 {
		t.Fatalf("upstream calls = %d, want exactly 3 (exhaustive member iteration — the latch is retired); attempted: %v", calls, attempted)
	}
	seenSet := map[string]bool{}
	for _, m := range attempted {
		seenSet[m] = true
	}
	for _, want := range []string{"x-one", "x-two", "x-three"} {
		if !seenSet[want] {
			t.Errorf("member %s was never attempted; attempted: %v", want, attempted)
		}
	}
	if len(attempted) != len(seenSet) {
		t.Errorf("a member was RE-SERVED: attempted %v (the tried-set must refuse pool re-offers)", attempted)
	}

	deadline := time.After(2 * time.Second)
	failovers := 0
collect:
	for {
		select {
		case <-seen:
			failovers++
			if failovers > 2 {
				break collect
			}
		case <-deadline:
			break collect
		}
	}
	if failovers != 2 {
		t.Errorf("model_member_failover events = %d, want exactly 2 (one per re-entry)", failovers)
	}
}

// TestHandlerGroups_Anthropic400_FailsFast — the P2 client-
// attributable fail-fast, Anthropic leg (MANDATE #5 parity): a 400
// stops the walk after the FIRST member — exactly ONE upstream call
// for a 3-member group, no member failover.
func TestHandlerGroups_Anthropic400_FailsFast(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedExhaustGroup(t, h, "g-anth-400")

	st := &exhaustStubState{status: http.StatusBadRequest}
	h.up.Close()
	h.up = httptest.NewServer(newExhaustStubUpstream(st))
	h.rebuildHandler(t)

	seen, stop := subscribeFailover(t, h)
	defer stop()

	rec := httptest.NewRecorder()
	req := makeAnthropicRequestNoAuth(t, map[string]interface{}{
		"model":      "g-anth-400",
		"max_tokens": 16,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleAnthropicMessages(rec, req)

	calls, attempted := st.snapshot()
	if calls != 1 {
		t.Fatalf("upstream calls = %d, want exactly 1 (client-attributable 400 must not thrash the pool); attempted: %v", calls, attempted)
	}
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case d := <-seen:
			t.Errorf("unexpected model_member_failover on a client-attributable 400: %v", d)
		case <-deadline:
			return
		}
	}
}

// newLiveStreamTestArc builds a minimal anthropicRequestContext for
// direct handleAnthropicLiveStreamResponse calls (the handler and
// store are real; the sticky/metering paths are nil-safe).
func newLiveStreamTestArc(t *testing.T, h *Handler) *anthropicRequestContext {
	t.Helper()
	return &anthropicRequestContext{
		conf:          *newTestConfigSnapshot("claude-sonnet-4-5"),
		reqID:         "live-commit-test",
		startTime:     time.Now(),
		reqLog:        &store.RequestLog{Model: "claude-sonnet-4-5"},
		baseCtx:       context.Background(),
		originalModel: "claude-sonnet-4-5",
	}
}

// runLiveStreamAgainst drives handleAnthropicLiveStreamResponse with
// an upstream serving the given raw SSE body.
func runLiveStreamAgainst(t *testing.T, h *Handler, arc *anthropicRequestContext, sseBody string) *httptest.ResponseRecorder {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseBody))
	}))
	t.Cleanup(up.Close)

	resp, err := http.Get(up.URL)
	if err != nil {
		t.Fatalf("upstream GET: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })

	rec := httptest.NewRecorder()
	h.handleAnthropicLiveStreamResponse(rec, resp, arc)
	return rec
}

// TestAnthropicLiveStream_PingOnlyDoesNotCommit — window parity,
// framing leg (MANDATE #1, Anthropic side): SSE comments and ping
// keep-alives forwarded through the lazy preamble set headersSent but
// NEVER payloadCommitted — framing-only progress leaves the fallback
// window OPEN (the old headersSent guard would have closed it here —
// the lazy-preamble boundary gets its OWN gate flag).
func TestAnthropicLiveStream_PingOnlyDoesNotCommit(t *testing.T) {
	h := &Handler{store: store.NewRequestStore(10)}
	arc := newLiveStreamTestArc(t, h)

	rec := runLiveStreamAgainst(t, h, arc, ": keepalive\n\n: keepalive\n\n")
	if !arc.headersSent {
		t.Fatal("precondition: the lazy preamble must have set headersSent on the comment forwarding (framing flowed)")
	}
	if !strings.Contains(rec.Body.String(), ": connected") {
		t.Fatalf("recorder body %q missing the preamble — framing precondition unmet", rec.Body.String())
	}
	if arc.payloadCommitted {
		t.Fatal("SSE comments/pings are NON-COMMITTING framing — payloadCommitted must stay false")
	}
}

// TestAnthropicLiveStream_PayloadCommits — window parity, payload leg
// (MANDATE #2, Anthropic side): the first forwarded non-ping
// translated event (message_start from a real upstream chunk) IS the
// commit — the window CLOSES.
func TestAnthropicLiveStream_PayloadCommits(t *testing.T) {
	h := &Handler{store: store.NewRequestStore(10)}
	arc := newLiveStreamTestArc(t, h)

	chunk := makeOpenAITextChunk("hi")
	runLiveStreamAgainst(t, h, arc, "data: "+chunk+"\n\ndata: [DONE]\n\n")
	if !arc.payloadCommitted {
		t.Fatal("the first forwarded non-ping payload event must SET payloadCommitted (window closes)")
	}
}

// TestAnthropicEventIsPing — the commit predicate's vocabulary:
// pure ping events are framing; content-bearing events are payload.
func TestAnthropicEventIsPing(t *testing.T) {
	if !anthropicEventIsPing("event: ping\ndata: {\"type\":\"ping\"}\n\n") {
		t.Fatal("a pure ping event must classify as framing")
	}
	if anthropicEventIsPing("event: content_block_delta\ndata: {\"type\":\"content_block_delta\"}\n\n") {
		t.Fatal("a content event must classify as payload")
	}
}
