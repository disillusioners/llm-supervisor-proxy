package proxy

// wi-7769a812 part 2 — exhaustive pre-commit group fallback, OpenAI
// route. Coverage:
//
//	1. LATCH RETIREMENT: an N-member group makes up to N attempts in
//	   pool order (one per member, no re-serves) and the terminal
//	   carries group_fallback_exhausted with to_member ABSENT — once
//	   per member coordinator, plus the model_member_failover event
//	   per real re-entry.
//	2. COMMIT SEMANTICS (the window): heartbeat/framing progress
//	   NEVER sets streamingNonRetryable (the window stays open);
//	   the first forwarded payload frame DOES (the window closes).
//	   This is the LEADER DECISION 3 OVERTURN proof pair.
//	3. P2 FAIL-FAST: a client-attributable 400 stops BOTH levels —
//	   the coordinator chain (no advance spawn) AND the handler
//	   member iteration (no re-entry) — exactly ONE upstream call.
//	4. P2 ADVANCE: a 5xx (rotation-INELIGIBLE, advance-ELIGIBLE)
//	   still advances the chain to the serving member.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// serverErrorScriptedProvider fails its first failN ChatCompletion
// calls with a PLAIN 503 ProviderError, then succeeds — the P2
// advance-eligible / P1 rotation-INELIGIBLE class. Mirror of
// quotaScriptedProvider with the class swapped.
type serverErrorScriptedProvider struct {
	mu    sync.Mutex
	failN int
	calls int
}

func (p *serverErrorScriptedProvider) Name() string { return "5xx-scripted" }

func (p *serverErrorScriptedProvider) ChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.mu.Unlock()
	if n <= p.failN {
		return nil, &providers.ProviderError{
			Provider:   "5xx-scripted",
			StatusCode: http.StatusServiceUnavailable,
			Message:    "upstream 503",
			Retryable:  true,
		}
	}
	return &providers.ChatCompletionResponse{
		ID:      "chatcmpl-5xx-scripted",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []providers.Choice{
			{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
		},
		Usage: providers.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
	}, nil
}

func (p *serverErrorScriptedProvider) StreamChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	return nil, &providers.ProviderError{StatusCode: http.StatusServiceUnavailable, Message: "upstream 503", Retryable: true}
}

func (p *serverErrorScriptedProvider) IsRetryable(err error) bool { return true }

func (p *serverErrorScriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// exhaustStubState records the wire models attempted against the
// stub, in order, plus the call count.
type exhaustStubState struct {
	mu     sync.Mutex
	calls  int
	models []string
	status int // the status EVERY call fails with
}

func (s *exhaustStubState) record(model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.models = append(s.models, model)
}

func (s *exhaustStubState) snapshot() (int, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.models))
	copy(out, s.models)
	return s.calls, out
}

// newExhaustStubUpstream fails EVERY call with the stub's status and
// records the attempted wire model per call.
func newExhaustStubUpstream(st *exhaustStubState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		model, _ := body["model"].(string)
		st.record(model)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st.status)
		_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"stub exhaust"}}`))
	}
}

// seedExhaustGroup seeds a 3-member group whose members carry
// DISTINCT InternalModel names (so the stub can attribute attempts
// per member).
func seedExhaustGroup(t *testing.T, h *groupHarness, groupID string) {
	t.Helper()
	for _, id := range []string{"x-one", "x-two", "x-three"} {
		if err := h.mgr.AddModel(models.ModelConfig{
			ID: id, Name: id, Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: id,
			Credentials: models.TestRefs(p5FixtureCredID),
		}); err != nil {
			t.Fatalf("AddModel(%s): %v", id, err)
		}
	}
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: groupID, Name: groupID, Enabled: true,
		Kind: models.KindGroup, Internal: true,
		Members: []models.GroupMember{
			{ModelID: "x-one", Weight: 1},
			{ModelID: "x-two", Weight: 1},
			{ModelID: "x-three", Weight: 1},
		},
	}); err != nil {
		t.Fatalf("AddModel(%s): %v", groupID, err)
	}
}

// TestHandlerGroups_ExhaustiveMemberAdvance_NAttemptsInOrder — the
// latch-retirement proof (MANDATE #6): a 3-member group whose every
// attempt fails pre-commit makes EXACTLY 3 upstream attempts, one per
// member (no re-serves — the pool never hands a member back twice),
// and the terminal publishes group_fallback_exhausted with the
// to_member key ABSENT. The old one-re-entry latch would have stopped
// at 2 attempts.
func TestHandlerGroups_ExhaustiveMemberAdvance_NAttemptsInOrder(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedExhaustGroup(t, h, "g-exh")

	st := &exhaustStubState{status: http.StatusBadGateway}
	h.up.Close()
	h.up = httptest.NewServer(newExhaustStubUpstream(st))
	h.rebuildHandler(t)

	seen, stop := subscribeFailover(t, h)
	defer stop()

	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model": "g-exh",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleChatCompletions(rec, req)

	calls, attempted := st.snapshot()
	if calls != 3 {
		t.Fatalf("upstream calls = %d, want exactly 3 (N members = up to N attempts — the one-re-entry latch is retired); attempted: %v", calls, attempted)
	}
	seenSet := map[string]bool{}
	for _, m := range attempted {
		seenSet[m] = true
	}
	for _, want := range []string{"x-one", "x-two", "x-three"} {
		if !seenSet[want] {
			t.Errorf("member %s was never attempted; attempted: %v (each member at most once, every member exactly once)", want, attempted)
		}
	}
	if len(attempted) != len(seenSet) {
		t.Errorf("a member was RE-SERVED: attempted %v (pool re-offers must be refused by the tried-set)", attempted)
	}

	// Exactly 2 real re-entries (3 attempts − 1 initial pick).
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

	// The exhausted event: one per member coordinator (each member's
	// own chain ended), ALL with to_member ABSENT.
	all := drainAllEvents(h.bus)
	exhausted := eventsOfType(all, "group_fallback_exhausted")
	if len(exhausted) != 3 {
		t.Fatalf("group_fallback_exhausted events = %d, want 3 (one per member's chain-end)", len(exhausted))
	}
	members := map[string]bool{}
	for _, evt := range exhausted {
		d, ok := evt.Data.(map[string]interface{})
		if !ok {
			t.Fatalf("exhausted payload not a map: %T", evt.Data)
		}
		if _, present := d["to_member"]; present {
			t.Errorf("exhausted payload for member %v carries to_member=%v — want the key ABSENT (payload contract)", d["member_id"], d["to_member"])
		}
		members[asString(d["member_id"])] = true
	}
	for _, want := range []string{"x-one", "x-two", "x-three"} {
		if !members[want] {
			t.Errorf("exhausted events name %v — missing %s", members, want)
		}
	}
	if got := asString(payloadMap(t, exhausted[0])["reason"]); !strings.HasPrefix(got, "reason=upstream_5xx") {
		t.Errorf("exhausted reason = %q, want the classified 502 line (reason=upstream_5xx …)", got)
	}
}

// TestHandlerGroups_ClientAttributable400_FailsFast — the P2
// client-attributable fail-fast at BOTH levels (MANDATE #5, OpenAI
// leg): a 400 stops the coordinator's chain AND the handler's member
// iteration — exactly ONE upstream call for a 3-member group, no
// member failover, one exhausted event whose reason carries the 400.
func TestHandlerGroups_ClientAttributable400_FailsFast(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedExhaustGroup(t, h, "g-400")

	st := &exhaustStubState{status: http.StatusBadRequest}
	h.up.Close()
	h.up = httptest.NewServer(newExhaustStubUpstream(st))
	h.rebuildHandler(t)

	seen, stop := subscribeFailover(t, h)
	defer stop()

	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model": "g-400",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleChatCompletions(rec, req)

	calls, attempted := st.snapshot()
	if calls != 1 {
		t.Fatalf("upstream calls = %d, want exactly 1 (client-attributable 400 must not thrash the pool — no chain advance, no member advance); attempted: %v", calls, attempted)
	}
	// No failover may fire within the assertion window.
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

// TestCoordinator_FiveXX_AdvancesTheChain — P2's advance side at the
// coordinator level (MANDATE #5): plain 503s are rotation-INELIGIBLE
// (P1 false — zero cred_failover rows for the single-credential
// members) yet advance-ELIGIBLE (P2 false) — the chain advances past
// two failing members to the serving one.
func TestCoordinator_FiveXX_AdvancesTheChain(t *testing.T) {
	mock := &mockModelsConfig{}
	engine := credentiallb.NewEngine(time.Hour, time.Hour, 42, 60*time.Second)
	t.Cleanup(engine.Stop)

	idsByModel := make(map[string][]string)
	modelIDs := make([]string, 0, 3)
	for _, m := range []string{"m1", "m2", "m3"} {
		id := "cred-" + m + "-A"
		idsByModel[m] = []string{id}
		mock.AddCredential(models.CredentialConfig{
			ID: id, Provider: "openai", APIKey: "key-" + id, BaseURL: "http://upstream-" + id,
		})
		modelIDs = append(modelIDs, m)
		mock.AddModel(models.ModelConfig{
			ID: m, Name: m, Enabled: true, Internal: true,
			InternalModel: "internal-" + m,
			Credentials:   models.TestRefs(id),
		})
		engine.RebindFromStore(m, models.TestRefs(idsByModel[m]...))
	}

	resolver := &engineBackedResolver{mockModelsConfig: mock, engine: engine}

	provider := &serverErrorScriptedProvider{failN: 2}
	origNew := newProviderClient
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		return provider, nil
	}
	t.Cleanup(func() { newProviderClient = origNew })

	bus := events.NewBus()
	cfg := newTestConfigSnapshot(modelIDs[0])
	cfg.ModelsConfig = resolver
	cfg.StreamDeadline = 5 * time.Second
	cfg.MaxGenerationTime = 10 * time.Second

	rawBody := []byte(`{"model":"m1","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	coord := newRaceCoordinatorWithEvents(context.Background(), cfg, newTestRequest(), rawBody, modelIDs, bus, "5xx-advance-req", false, engine, "conv-5xx-advance", false, nil)
	coord.groupPick = &groupPickInfo{GroupID: "g-5xx", MemberID: "m1", Pool: models.PoolDefault}

	coord.Start()
	winner := coord.WaitForWinner()
	if winner == nil {
		t.Fatal("expected a winner on m3 (5xx advances the chain)")
	}
	if winner.modelID != "m3" {
		t.Errorf("winner model = %q, want m3", winner.modelID)
	}
	if got := provider.callCount(); got != 3 {
		t.Errorf("upstream calls = %d, want 3 (two advances, no rotation rows)", got)
	}

	all := drainAllEvents(bus)
	if got := len(eventsOfType(all, "group_fallback_attempted")); got != 2 {
		t.Errorf("group_fallback_attempted = %d, want 2 (m1→m2, m2→m3 — the chain advanced on non-client-attributable failures)", got)
	}
	if got := len(eventsOfType(all, "group_fallback_exhausted")); got != 0 {
		t.Errorf("group_fallback_exhausted = %d, want 0 (the chain ended on a winner)", got)
	}
	spawns := recordSpawns(all)
	if n := countByType(spawns, "cred_failover"); n != 0 {
		t.Errorf("cred_failover rows = %d, want 0 (5xx is rotation-INELIGIBLE — P1 false even though P2 advanced)", n)
	}
}

// TestGroupFallbackWindow_HeartbeatDoesNotCommit — the window pair,
// heartbeat leg (MANDATE #1): headers + ": connected" + heartbeat
// comments are NON-COMMITTING framing — none of them sets
// streamingNonRetryable, so the member-advance gate
// (!rc.streamingNonRetryable.Load()) stays OPEN on framing-only
// progress. This is the LEADER DECISION 3 OVERTURN proof: the old
// headersSent proxy would have closed the window here (headersSent
// is set BEFORE ": connected").
func TestGroupFallbackWindow_HeartbeatDoesNotCommit(t *testing.T) {
	h := &Handler{} // sendHeartbeat touches no Handler state
	rc := &requestContext{}
	rc.baseCtx = context.Background()

	// The exact framing sequence HandleChatCompletions performs
	// before the coordinator starts: SSE headers + ": connected".
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", "text/event-stream")
	rec.WriteHeader(http.StatusOK)
	fmt.Fprintf(rec, ": connected\n\n")
	if rc.streamingNonRetryable.Load() {
		t.Fatal("framing (headers + ': connected') must NOT set the commit flag")
	}

	// A real heartbeat write through the production seam.
	done := make(chan bool, 1)
	go func() {
		done <- h.sendHeartbeat(rec, &rc.writeMu, rc.baseCtx, time.NewTimer(HeartbeatWriteTimeout))
	}()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("heartbeat write against an active recorder must succeed")
		}
	case <-time.After(HeartbeatWriteTimeout + 2*time.Second):
		t.Fatal("heartbeat write timed out")
	}
	if !strings.Contains(rec.Body.String(), ": heartbeat") {
		t.Fatalf("recorder body %q missing the heartbeat comment — the framing precondition is unmet", rec.Body.String())
	}
	if rc.streamingNonRetryable.Load() {
		t.Fatal("heartbeat framing must NOT set the commit flag — the fallback window stays open")
	}
	// The gate, evaluated exactly as the re-entry site does: framing
	// only ⇒ advance ALLOWED.
	if rc.streamingNonRetryable.Load() {
		t.Fatal("gate predicate: member advance must be ALLOWED after framing-only progress")
	}
}

// TestGroupFallbackWindow_PayloadCommits — the window pair, payload
// leg (MANDATE #2): the FIRST forwarded payload frame sets
// streamingNonRetryable through the REAL relay (streamResult's
// set-site) — after which the member-advance gate is CLOSED (no
// advance after commit).
func TestGroupFallbackWindow_PayloadCommits(t *testing.T) {
	t.Setenv("TOKEN_FALLBACK_ENABLED", "false") // keep the relay lean

	h := &Handler{store: store.NewRequestStore(10)}
	rc := &requestContext{}
	rc.baseCtx = context.Background()
	rc.isStream = true
	rc.reqLog = &store.RequestLog{Model: "m1"}

	winner := newUpstreamRequest(0, modelTypeMain, "m1", 1<<20)
	winner.buffer.Add([]byte(`data: {"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"}}]}` + "\n\n"))
	winner.buffer.Close(nil)

	rec := httptest.NewRecorder()
	if err := h.streamResult(rec, rc, winner); err != nil {
		t.Fatalf("streamResult: %v", err)
	}
	if !strings.Contains(rec.Body.String(), `"content":"hi"`) {
		t.Fatalf("recorder body %q missing the forwarded payload — the relay precondition is unmet", rec.Body.String())
	}
	// The gate, evaluated exactly as the re-entry site does: payload
	// forwarded ⇒ advance BLOCKED.
	if !rc.streamingNonRetryable.Load() {
		t.Fatal("the first forwarded payload frame must SET the commit flag and CLOSE the member-advance gate")
	}
}
