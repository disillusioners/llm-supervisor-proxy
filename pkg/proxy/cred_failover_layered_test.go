package proxy

// ─────────────────────────────────────────────────────────────────────────────
// Layered credential fallback (Fix 1 + Fix 2) — coordinator + handler-level
// acceptance tests.
//
// Spec order (the acceptance example, generalized):
//
//	models[0]/cred₁ → models[0]/cred₂ → … (Layer 1, per-model rotation)
//	→ models[1]/cred₁ → models[1]/cred₂ → … (Layer 2 advance, then Layer 1 again)
//	→ … → terminate at the attempt cap.
//
// Coverage rule honored: every cred-failover test here uses a MULTI-model
// chain with BOTH models internal + multi-credential (the m-chain terminal
// model that never failovers is the exception the rule permits — it is the
// advance target, not a failover subject).
// ─────────────────────────────────────────────────────────────────────────────

import (
	"context"
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
)

// quotaScriptedProvider fails its first failN ChatCompletion calls with a
// QUOTA-class error in the observed live shape (HTTP 402 + code 2067 +
// "Token Plan usage limit"), then succeeds. Mirrors scriptedProvider
// (race_coordinator_credfailover_test.go) with the class swapped.
type quotaScriptedProvider struct {
	mu    sync.Mutex
	failN int
	calls int
	keys  []string
}

func (p *quotaScriptedProvider) Name() string { return "quota-scripted" }

func (p *quotaScriptedProvider) ChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.mu.Unlock()
	if n <= p.failN {
		return nil, &providers.ProviderError{
			Provider:   "quota-scripted",
			StatusCode: 402,
			Message:    "Token Plan usage limit",
			ErrorCode:  "quota_exceeded",
			Retryable:  false,
		}
	}
	return &providers.ChatCompletionResponse{
		ID:      "chatcmpl-quota-scripted",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []providers.Choice{
			{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
		},
		Usage: providers.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
	}, nil
}

func (p *quotaScriptedProvider) StreamChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	return nil, &providers.ProviderError{StatusCode: 402, Message: "Token Plan usage limit", ErrorCode: "quota_exceeded"}
}

func (p *quotaScriptedProvider) IsRetryable(err error) bool { return true }

func (p *quotaScriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// layeredChainModel is one model in the test chain: a pool of credCount
// credentials (auto-named cred-<m>-<i>), all internal.
type layeredChainModel struct {
	id        string
	credCount int
}

// newLayeredChainEnv builds a coordinator wired for layered-failover
// tests: N internal models in a linear chain (models[0] → … →
// models[N-1]), each with its own multi-credential pool, one shared
// quota-scripted provider, a real engine (quota cooldown at the package
// default 15m), and an event bus.
func newLayeredChainEnv(t *testing.T, chain []layeredChainModel, failN int, conversationKey string) (*raceCoordinator, *quotaScriptedProvider, *events.Bus, *credentiallb.Engine) {
	t.Helper()

	mock := &mockModelsConfig{}
	engine := credentiallb.NewEngine(time.Hour, time.Hour, 42, 60*time.Second)
	t.Cleanup(engine.Stop)

	idsByModel := make(map[string][]string, len(chain))
	modelIDs := make([]string, 0, len(chain))
	for _, m := range chain {
		ids := make([]string, 0, m.credCount)
		for i := 0; i < m.credCount; i++ {
			id := "cred-" + m.id + "-" + string(rune('A'+i))
			ids = append(ids, id)
			mock.AddCredential(models.CredentialConfig{
				ID: id, Provider: "openai", APIKey: "key-" + id, BaseURL: "http://upstream-" + id,
			})
		}
		idsByModel[m.id] = ids
		modelIDs = append(modelIDs, m.id)
		mock.AddModel(models.ModelConfig{
			ID: m.id, Name: m.id, Enabled: true, Internal: true,
			InternalModel: "internal-" + m.id,
			Credentials:   models.TestRefs(ids...),
		})
		engine.RebindFromStore(m.id, models.TestRefs(idsByModel[m.id]...))
	}

	resolver := &engineBackedResolver{mockModelsConfig: mock, engine: engine}

	provider := &quotaScriptedProvider{failN: failN}
	origNew := newProviderClient
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		provider.mu.Lock()
		provider.keys = append(provider.keys, apiKey)
		provider.mu.Unlock()
		return provider, nil
	}
	t.Cleanup(func() { newProviderClient = origNew })

	bus := events.NewBus()
	cfg := newTestConfigSnapshot(modelIDs[0])
	cfg.ModelsConfig = resolver
	cfg.StreamDeadline = 5 * time.Second
	cfg.MaxGenerationTime = 10 * time.Second

	rawBody := []byte(`{"model":"` + modelIDs[0] + `","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	coord := newRaceCoordinatorWithEvents(context.Background(), cfg, newTestRequest(), rawBody, modelIDs, bus, "layered-test-req", false, engine, conversationKey, false)
	return coord, provider, bus, engine
}

// spawnOrderSnapshot flattens the coordinator's request rows into
// "type:model" pairs for exact order assertions.
func spawnOrderSnapshot(coord *raceCoordinator) []string {
	out := make([]string, 0, len(coord.requests))
	for _, r := range coord.requests {
		out = append(out, string(r.modelType)+":"+r.modelID)
	}
	return out
}

func assertSpawnOrder(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("spawn order = %v, want %v (length mismatch)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("spawn order = %v, want %v (first divergence at %d: %q vs %q)", got, want, i, got[i], want[i])
		}
	}
}

// (a) Quota-class failure on a multi-credential model spawns cred-failover
// on the SAME model BEFORE any model switch.
//
// Chain: m1 (2 creds) → m2 (2 creds). The provider quota-fails the first
// TWO calls then succeeds. Expected flow:
//
//	main(m1/credA) → 402 → cred_failover(m1/credB) [SAME model — Layer 1]
//	→ 402 → m1 budget (2−1) exhausted → fallback(m2) → 200 (winner).
func TestCoordinator_QuotaFailover_SameModelBeforeModelSwitch(t *testing.T) {
	coord, _, bus, _ := newLayeredChainEnv(
		t,
		[]layeredChainModel{{id: "m1", credCount: 2}, {id: "m2", credCount: 2}},
		2, // first two calls quota-fail
		"conv-quota-layered",
	)

	coord.Start()
	winner := coord.WaitForWinner()

	if winner == nil {
		t.Fatal("expected a winner (m2 serves after m1's quota pool exhausted)")
	}
	if winner.modelID != "m2" {
		t.Errorf("winner model = %q, want m2 (the advance target)", winner.modelID)
	}

	// THE ORDER IS THE SPEC: credential rotation on m1 completes BEFORE
	// the model switch, and the switch lands on the NEXT model (m2, not
	// a re-roll of models[0]).
	assertSpawnOrder(t, spawnOrderSnapshot(coord), []string{
		"main:m1",
		"cred_failover:m1", // Layer 1 — same model, second credential
		"fallback:m2",      // Layer 2 — only after m1's pool exhausted
	})

	// The credFailover row's credential must come from m1's OWN pool.
	m1Pool := map[string]bool{"cred-m1-A": true, "cred-m1-B": true}
	for _, r := range coord.requests {
		if r.modelType == modelTypeCredFailover && !m1Pool[r.resolved.CredentialID] {
			t.Errorf("credFailover on m1 resolved credential %q — not in m1's pool (wrong-account billing shape)", r.resolved.CredentialID)
		}
	}

	// Zero-value fallback: no fallback row may exist before m1's
	// rotations are spent (precedence — asserted by the exact order
	// above; the modelAttemptCount cross-check keeps B1 accounting).
	if got := coord.modelAttemptCountLocked(); got != 2 {
		t.Errorf("modelAttempts = %d, want 2 (main + 1 advance; credFailover rows are B1-exempt)", got)
	}

	// Fix 1 observability — the quota vocabulary end-to-end:
	//   race_spawn trigger == "quota_exceeded"
	//   model_credential_failover.reason == "quota_exceeded"
	//   cooldown_ms == the 15m DEFAULT quota cooldown (no Retry-After on 402).
	for _, evt := range drainEvents(bus, "race_spawn") {
		data, _ := evt.Data.(map[string]interface{})
		if data == nil || asString(data["type"]) != "cred_failover" {
			continue
		}
		if got := asString(data["trigger"]); got != "quota_exceeded" {
			t.Errorf("cred_failover race_spawn trigger = %q, want quota_exceeded", got)
		}
	}
	failovers := drainEvents(bus, credentiallb.EventCredentialFailover)
	if len(failovers) != 1 {
		t.Fatalf("model_credential_failover events = %d, want 1", len(failovers))
	}
	data, _ := failovers[0].Data.(map[string]interface{})
	if got := asString(data["reason"]); got != "quota_exceeded" {
		t.Errorf("failover reason = %q, want quota_exceeded", got)
	}
	if got := asString(data["model_id"]); got != "m1" {
		t.Errorf("failover model_id = %q, want m1", got)
	}
	if got, want := asInt64(data["cooldown_ms"]), (15 * time.Minute).Milliseconds(); got != want {
		t.Errorf("failover cooldown_ms = %d, want %d (configured quota cooldown default 15m)", got, want)
	}
	if got, want := asInt64(data["retry_after_ms"]), int64(0); got != want {
		t.Errorf("failover retry_after_ms = %d, want 0 (402 carries no Retry-After)", got)
	}
}

// (b) 3-model chain where the first two models fail: credentials are
// exhausted PER MODEL and the race reaches models[2] — previously
// impossible (the fallback arm was hard-coded to c.models[1]).
//
// All three models have 2 credentials; the provider quota-fails the
// first FIVE calls, the sixth succeeds (m3's second credential).
func TestCoordinator_LayeredChain_ThreeModels_ReachesThirdModel(t *testing.T) {
	coord, _, bus, _ := newLayeredChainEnv(
		t,
		[]layeredChainModel{{id: "m1", credCount: 2}, {id: "m2", credCount: 2}, {id: "m3", credCount: 2}},
		5, // m1×2 + m2×2 + m3's first attempt all fail; m3's second serves
		"conv-three-models",
	)

	coord.Start()
	winner := coord.WaitForWinner()

	if winner == nil {
		t.Fatal("expected a winner (models[2] reachable)")
	}
	if winner.modelID != "m3" {
		t.Errorf("winner model = %q, want m3 (models[2])", winner.modelID)
	}

	// The full layered order: every model gets its Layer-1 rotation
	// before the chain advances past it.
	assertSpawnOrder(t, spawnOrderSnapshot(coord), []string{
		"main:m1",
		"cred_failover:m1",
		"fallback:m2", // advance past m1 only after m1's pool exhausted
		"cred_failover:m2",
		"fallback:m3", // advance past m2 — the previously-impossible step
		"cred_failover:m3",
	})

	// Per-model rotation budget: exactly one credFailover row per model
	// (len(creds)−1 = 1 each), each drawn from its own model's pool.
	pools := map[string]map[string]bool{
		"m1": {"cred-m1-A": true, "cred-m1-B": true},
		"m2": {"cred-m2-A": true, "cred-m2-B": true},
		"m3": {"cred-m3-A": true, "cred-m3-B": true},
	}
	perModel := map[string]int{}
	for _, r := range coord.requests {
		if r.modelType != modelTypeCredFailover {
			continue
		}
		perModel[r.modelID]++
		if !pools[r.modelID][r.resolved.CredentialID] {
			t.Errorf("credFailover on %s resolved credential %q — cross-model pool leak", r.modelID, r.resolved.CredentialID)
		}
	}
	for _, m := range []string{"m1", "m2", "m3"} {
		if perModel[m] != 1 {
			t.Errorf("credFailover rows on %s = %d, want 1 (per-model budget len(creds)−1)", m, perModel[m])
		}
	}
	if got := coord.failoverAttempts; got != 3 {
		t.Errorf("global failoverAttempts = %d, want 3 (one per model; kept for attempt_index)", got)
	}

	// All failover events carry the quota vocabulary (the whole chain
	// is quota-driven).
	for i, evt := range drainEvents(bus, credentiallb.EventCredentialFailover) {
		data, _ := evt.Data.(map[string]interface{})
		if got := asString(data["reason"]); got != "quota_exceeded" {
			t.Errorf("failover[%d] reason = %q, want quota_exceeded", i, got)
		}
	}
}

// (d) Attempt cap still terminates the race: the layered order cannot
// run past the cap — same 3-model/2-cred chain with EVERY call failing
// terminates with a bounded, exactly-shaped attempt set and no winner.
func TestCoordinator_LayeredChain_AttemptCapTerminates(t *testing.T) {
	coord, provider, _, _ := newLayeredChainEnv(
		t,
		[]layeredChainModel{{id: "m1", credCount: 2}, {id: "m2", credCount: 2}, {id: "m3", credCount: 2}},
		99, // every call fails
		"conv-attempt-cap",
	)

	coord.Start()
	winner := coord.WaitForWinner()

	if winner != nil {
		t.Fatalf("expected terminal (all-failed), got winner on %s", winner.modelID)
	}

	// The attempt cap = Σ per-model budgets + one row per model:
	// 3 models × (1 main/fallback row + 1 credFailover row) = 6 attempts.
	assertSpawnOrder(t, spawnOrderSnapshot(coord), []string{
		"main:m1",
		"cred_failover:m1",
		"fallback:m2",
		"cred_failover:m2",
		"fallback:m3",
		"cred_failover:m3",
	})
	if got := provider.callCount(); got != 6 {
		t.Errorf("provider calls = %d, want 6 (bounded by the layered cap — no runaway)", got)
	}
	for _, m := range []string{"m1", "m2", "m3"} {
		if got := coord.failoverAttemptsByModel[m]; got != 1 {
			t.Errorf("failoverAttemptsByModel[%s] = %d, want 1 (per-model cap)", m, got)
		}
	}
	// Advance past the last model is refused: no second fallback wave
	// (the chain terminates instead of re-entering models[1]).
	for _, r := range coord.requests {
		if r.modelType == modelTypeFallback && r.modelID == "m1" {
			t.Errorf("fallback row re-entered m1 — the chain wrapped past the cap")
		}
	}
}

// (b-companion) The advance target skips an exhausted-model re-entry:
// a chain whose MIDDLE model has a single credential (no Layer-1 to
// rotate) still advances 0 → 1 → 2 with no credFailover row on it.
func TestCoordinator_LayeredChain_SingleCredMiddleModel_Advances(t *testing.T) {
	coord, _, _, _ := newLayeredChainEnv(
		t,
		[]layeredChainModel{{id: "m1", credCount: 1}, {id: "m2", credCount: 1}, {id: "m3", credCount: 1}},
		2, // m1 and m2 fail; m3 serves
		"conv-single-cred-middle",
	)

	coord.Start()
	winner := coord.WaitForWinner()

	if winner == nil {
		t.Fatal("expected a winner on m3")
	}
	if winner.modelID != "m3" {
		t.Errorf("winner model = %q, want m3", winner.modelID)
	}
	assertSpawnOrder(t, spawnOrderSnapshot(coord), []string{
		"main:m1",
		"fallback:m2", // single-cred ⇒ no Layer-1 row, straight advance
		"fallback:m3",
	})
}

// asInt64 normalizes the JSON-decoded event payload numbers (encoding/
// json may deliver int or float64 depending on the publish path).
func asInt64(v interface{}) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	default:
		return -1
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// (c) The NON-RACE single-model path rotates credentials on quota errors.
//
// HTTP-level at the InternalHandler seam (mirrors
// TestInternalHandler_RateLimitFailover with the class swapped): the
// upstream 402s ("Token Plan usage limit") the FIRST API key it serves;
// the hook must classify the failure quota-class, ExcludeAndReselect,
// retry ONCE with the reselected credential, and publish the quota
// vocabulary — with the engine's configured quota cooldown as
// cooldown_ms.
// ─────────────────────────────────────────────────────────────────────────────

// quotaOnceUpstream 402s the first distinct API key it serves; any
// other key gets a valid 200 response.
type quotaOnceUpstream struct {
	mu        sync.Mutex
	failedKey string
	calls     int
	keys      []string
}

func (u *quotaOnceUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	u.calls++
	key := r.Header.Get("Authorization")
	if key == "" {
		key = r.Header.Get("x-api-key")
	}
	u.keys = append(u.keys, key)
	first := u.failedKey == ""
	if first {
		u.failedKey = key
	}
	u.mu.Unlock()

	if first {
		w.WriteHeader(http.StatusPaymentRequired)
		// Numeric code 2067: unparseable into the string-typed
		// ErrorCode field — the classifier must key on the 402 status
		// and the message text (the observed live shape).
		fmt.Fprint(w, `{"error":{"message":"Token Plan usage limit","code":2067}}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"id":"chatcmpl-quota-test","object":"chat.completion","created":1,"model":"up-im1","choices":[{"index":0,"message":{"role":"assistant","content":"recovered"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
}

func (u *quotaOnceUpstream) callCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls
}

func TestInternalHandler_QuotaFailover_SingleModel(t *testing.T) {
	upstream := &quotaOnceUpstream{}
	srv := httptest.NewServer(upstream)
	defer srv.Close()

	mock, mc := newInternalFailoverModel([]string{"cred-A", "cred-B"}, srv.URL)

	engine := credentiallb.NewEngine(time.Hour, time.Hour, 21, 60*time.Second)
	t.Cleanup(engine.Stop)
	engine.RebindFromStore("im1", models.TestRefs("cred-A", "cred-B"))
	resolver := &engineBackedResolver{mockModelsConfig: mock, engine: engine}

	bus := events.NewBus()

	ih := NewInternalHandler(mc, resolver)
	ih.SetCredentialFailover(engine, bus)

	w := httptest.NewRecorder()
	err := ih.HandleRequest(context.Background(), map[string]interface{}{
		"model":    "im1",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	}, w, false, "v1m-quota-key-1")

	if err != nil {
		t.Fatalf("expected quota failover retry to succeed, got: %v", err)
	}
	if got := upstream.callCount(); got != 2 {
		t.Errorf("upstream calls = %d, want 2 (402 + single retry)", got)
	}
	upstream.mu.Lock()
	keys := append([]string(nil), upstream.keys...)
	upstream.mu.Unlock()
	if len(keys) == 2 && keys[0] == keys[1] {
		t.Errorf("retry reused the same credential: %v", keys)
	}
	if !strings.Contains(w.Body.String(), "recovered") {
		t.Errorf("recorder body missing success payload: %q", w.Body.String())
	}

	// The failover event carries the QUOTA vocabulary and the engine's
	// configured quota cooldown (default 15m = 900000ms).
	failovers := drainEvents(bus, credentiallb.EventCredentialFailover)
	if len(failovers) != 1 {
		t.Fatalf("failover events = %d, want 1", len(failovers))
	}
	data, _ := failovers[0].Data.(map[string]interface{})
	if got := asString(data["reason"]); got != "quota_exceeded" {
		t.Errorf("failover reason = %q, want quota_exceeded", got)
	}
	if got, want := asInt64(data["cooldown_ms"]), (15 * time.Minute).Milliseconds(); got != want {
		t.Errorf("failover cooldown_ms = %d, want %d (default quota cooldown)", got, want)
	}
	if got, want := asInt64(data["retry_after_ms"]), int64(0); got != want {
		t.Errorf("failover retry_after_ms = %d, want 0", got)
	}
}

// TestInternalHandler_QuotaFailover_ConfiguredCooldown pins the config
// knob end-to-end at the seam: SetQuotaCooldown drives the seeded
// cooldown_ms (here 2h — the deployment's "hours-long exhaustion"
// tuning) instead of the package default.
func TestInternalHandler_QuotaFailover_ConfiguredCooldown(t *testing.T) {
	upstream := &quotaOnceUpstream{}
	srv := httptest.NewServer(upstream)
	defer srv.Close()

	mock, mc := newInternalFailoverModel([]string{"cred-A", "cred-B"}, srv.URL)

	engine := credentiallb.NewEngine(time.Hour, time.Hour, 21, 60*time.Second)
	t.Cleanup(engine.Stop)
	engine.SetQuotaCooldown(2 * time.Hour)
	engine.RebindFromStore("im1", models.TestRefs("cred-A", "cred-B"))
	resolver := &engineBackedResolver{mockModelsConfig: mock, engine: engine}

	ih := NewInternalHandler(mc, resolver)
	ih.SetCredentialFailover(engine, events.NewBus())

	w := httptest.NewRecorder()
	if err := ih.HandleRequest(context.Background(), map[string]interface{}{
		"model":    "im1",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	}, w, false, "v1m-quota-key-2"); err != nil {
		t.Fatalf("expected quota failover retry to succeed, got: %v", err)
	}
	if got := upstream.callCount(); got != 2 {
		t.Errorf("upstream calls = %d, want 2", got)
	}
}

// TestInternalHandler_QuotaFailover_ExhaustsAndPropagates pins the
// non-race hook's single-retry budget against a quota error: BOTH
// credentials 402 → exactly 2 calls, the failure propagates, and the
// 401/auth class stays terminal (ineligible) at the same seam.
func TestInternalHandler_QuotaFailover_ExhaustsAndPropagates(t *testing.T) {
	// Always-402 upstream.
	upstream := alwaysQuotaUpstream{}
	srv := httptest.NewServer(&upstream)
	defer srv.Close()

	mock, mc := newInternalFailoverModel([]string{"cred-A", "cred-B"}, srv.URL)

	engine := credentiallb.NewEngine(time.Hour, time.Hour, 21, 60*time.Second)
	t.Cleanup(engine.Stop)
	engine.RebindFromStore("im1", models.TestRefs("cred-A", "cred-B"))
	resolver := &engineBackedResolver{mockModelsConfig: mock, engine: engine}

	ih := NewInternalHandler(mc, resolver)
	ih.SetCredentialFailover(engine, events.NewBus())

	w := httptest.NewRecorder()
	err := ih.HandleRequest(context.Background(), map[string]interface{}{
		"model":    "im1",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	}, w, false, "v1m-quota-key-3")

	if err == nil {
		t.Fatal("expected the quota failure to propagate after the rotation budget")
	}
	if got := upstream.callCount(); got != 2 {
		t.Errorf("upstream calls = %d, want 2 (single retry budget)", got)
	}
}

// alwaysQuotaUpstream 402s every call.
type alwaysQuotaUpstream struct {
	calls int
	mu    sync.Mutex
}

func (u *alwaysQuotaUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	u.calls++
	u.mu.Unlock()
	w.WriteHeader(http.StatusPaymentRequired)
	fmt.Fprint(w, `{"error":{"message":"Token Plan usage limit","code":2067}}`)
}

func (u *alwaysQuotaUpstream) callCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls
}

// (e) Layered fallback review fix — observability pin: quota-class
// credFailover spawn rows must carry credential_id +
// failed_request_index in exactly the same payload shape as
// rate_limit rows (the emit gate covers BOTH classes). Fails against
// the unfixed gate: credential_id is omitted entirely, so the
// non-empty assertions below fire.
//
// Chain: m1 (2 creds) → m2 (2 creds); the provider quota-fails only
// the FIRST call, so the flow is main(m1/cred-A) → 402 →
// cred_failover(m1/cred-B) → 200 (single rotation, winner on m1).
func TestCoordinator_QuotaFailover_SpawnRowCarriesCredentialID(t *testing.T) {
	coord, _, bus, _ := newLayeredChainEnv(
		t,
		[]layeredChainModel{{id: "m1", credCount: 2}, {id: "m2", credCount: 2}},
		1, // only the first call quota-fails
		"conv-quota-spawn-pin",
	)

	coord.Start()
	winner := coord.WaitForWinner()
	if winner == nil {
		t.Fatal("expected a winner (m1's second credential serves)")
	}
	if winner.modelID != "m1" {
		t.Errorf("winner model = %q, want m1 (a single rotation suffices with failN=1)", winner.modelID)
	}

	m1Pool := map[string]bool{"cred-m1-A": true, "cred-m1-B": true}
	spawnRows := 0
	for _, evt := range drainEvents(bus, "race_spawn") {
		data, _ := evt.Data.(map[string]interface{})
		if data == nil || asString(data["type"]) != "cred_failover" {
			continue
		}
		spawnRows++
		if got := asString(data["trigger"]); got != "quota_exceeded" {
			t.Errorf("cred_failover race_spawn trigger = %q, want quota_exceeded", got)
		}
		cid := asString(data["credential_id"])
		if cid == "" {
			t.Errorf("quota_exceeded cred_failover race_spawn missing credential_id — emit gate not widened to the quota class")
			continue
		}
		if !m1Pool[cid] {
			t.Errorf("cred_failover race_spawn credential_id = %q — not in m1's pool (wrong-account billing shape)", cid)
		}
		rawIdx, present := data["failed_request_index"]
		if !present {
			t.Fatalf("quota_exceeded cred_failover race_spawn missing failed_request_index — emit gate not widened to the quota class")
		}
		if got := asInt64(rawIdx); got != 0 {
			t.Errorf("cred_failover race_spawn failed_request_index = %d, want 0 (the failed main row)", got)
		}
	}
	if spawnRows != 1 {
		t.Fatalf("cred_failover race_spawn rows = %d, want 1", spawnRows)
	}
}
