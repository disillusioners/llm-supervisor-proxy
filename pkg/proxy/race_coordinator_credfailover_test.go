package proxy

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
)

// ─────────────────────────────────────────────────────────────────────────────
// Phase 3 / Tasks 18-20 — coordinator credential-failover test harness.
//
// The harness is mock-based (no live DB): an engine-backed resolver
// wraps the package's mockModelsConfig, mirroring
// ModelsManager.ResolveInternalConfigWithAffinity semantics (0/1 creds
// → legacy fast path; 2+ → engine.GetOrSelect + GetCredential), and a
// scripted fake provider 429s its first N calls then succeeds — the
// tests are deterministic regardless of WHICH credential the weighted
// pick lands on first.
// ─────────────────────────────────────────────────────────────────────────────

// engineBackedResolver implements models.ModelsConfigInterface by
// embedding mockModelsConfig and overriding the affinity seam with a
// real credentiallb.Engine (mirrors store.go semantics minus DB healing).
type engineBackedResolver struct {
	*mockModelsConfig
	engine *credentiallb.Engine
}

func (e *engineBackedResolver) ResolveInternalConfigWithAffinity(modelID, conversationKey string) (models.ResolvedCredential, bool) {
	mc := e.GetModel(modelID)
	if mc == nil || !mc.Internal {
		return models.ResolvedCredential{}, false
	}
	if len(mc.Credentials) <= 1 {
		// E-3 single-credential fast path: no engine call, no map writes.
		return e.mockModelsConfig.ResolveInternalConfigWithAffinity(modelID, conversationKey)
	}
	credID, newlyBound, err := e.engine.GetOrSelect(modelID, conversationKey)
	if err != nil || credID == "" {
		return models.ResolvedCredential{}, false
	}
	cred := e.GetCredential(credID)
	if cred == nil {
		return models.ResolvedCredential{}, false
	}
	baseURL := mc.InternalBaseURL
	if baseURL == "" {
		baseURL = cred.BaseURL
	}
	return models.ResolvedCredential{
		Provider:      cred.Provider,
		APIKey:        cred.APIKey,
		BaseURL:       baseURL,
		InternalModel: mc.InternalModel,
		CredentialID:  credID,
		NewlyBound:    newlyBound,
	}, true
}

// scriptedProvider 429s its first failN ChatCompletion calls, then
// succeeds. Records every (apiKey) it served so tests can assert the
// failover chain visited distinct credentials.
type scriptedProvider struct {
	mu        sync.Mutex
	failN     int
	calls     int
	apiKeys   []string
	lastModel string
}

func (p *scriptedProvider) Name() string { return "scripted" }

func (p *scriptedProvider) ChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.mu.Unlock()
	if n <= p.failN {
		return nil, &providers.ProviderError{
			Provider:   "scripted",
			StatusCode: 429,
			Message:    "rate limited (scripted)",
			ErrorType:  "rate_limit",
			ErrorCode:  "rate_limit",
			Retryable:  true,
			RetryAfter: 0, // absent header ⇒ engine default cooldown
		}
	}
	return &providers.ChatCompletionResponse{
		ID:      "chatcmpl-scripted",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []providers.Choice{
			{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
		},
		Usage: providers.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
	}, nil
}

func (p *scriptedProvider) StreamChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	return nil, &providers.ProviderError{StatusCode: 429, ErrorType: "rate_limit"}
}

func (p *scriptedProvider) IsRetryable(err error) bool { return true }

func (p *scriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// newCredFailoverTestEnv builds a coordinator wired for credential
// failover: engine-backed resolver over a 3-credential internal model,
// scripted provider override, event bus, conversation key.
func newCredFailoverTestEnv(t *testing.T, credCount int, failN int, conversationKey string) (*raceCoordinator, *engineBackedResolver, *scriptedProvider, *events.Bus) {
	t.Helper()

	mock := &mockModelsConfig{}
	creds := make([]models.CredentialConfig, credCount)
	ids := make([]string, credCount)
	for i := 0; i < credCount; i++ {
		ids[i] = "cred-" + string(rune('A'+i))
		creds[i] = models.CredentialConfig{
			ID:       ids[i],
			Provider: "openai",
			APIKey:   "key-" + ids[i],
			BaseURL:  "http://upstream-" + ids[i],
		}
		mock.AddCredential(creds[i])
	}
	mock.AddModel(models.ModelConfig{
		ID:            "m1",
		Name:          "m1",
		Enabled:       true,
		Internal:      true,
		InternalModel: "upstream-m1",
		Credentials:   models.TestRefs(ids...),
	})

	engine := credentiallb.NewEngine(time.Hour, time.Hour, 42, 60*time.Second)
	t.Cleanup(engine.Stop)
	engine.RebindFromStore("m1", models.TestRefs(ids...))

	resolver := &engineBackedResolver{mockModelsConfig: mock, engine: engine}

	provider := &scriptedProvider{failN: failN}
	origNew := newProviderClient
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		provider.mu.Lock()
		provider.apiKeys = append(provider.apiKeys, apiKey)
		provider.mu.Unlock()
		return provider, nil
	}
	t.Cleanup(func() { newProviderClient = origNew })

	bus := events.NewBus()

	cfg := newTestConfigSnapshot("m1")
	cfg.ModelsConfig = resolver
	// Keep the test fast and bounded even on failure paths.
	cfg.StreamDeadline = 5 * time.Second
	cfg.MaxGenerationTime = 10 * time.Second

	rawBody := []byte(`{"model":"m1","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	coord := newRaceCoordinatorWithEvents(context.Background(), cfg, newTestRequest(), rawBody, []string{"m1"}, bus, "test-req", false, engine, conversationKey, false)
	return coord, resolver, provider, bus
}

// TestCoordinator_CredFailover_ThreeCredChain (Phase 3 / Task 18
// acceptance (a) + Task 19): 3-credential single-model, first two
// attempts 429, third succeeds. Asserts:
//   - the race completes with a winner (rescue scenario);
//   - EXACTLY 2 credFailover spawns (budget = len(credentials)−1);
//   - ZERO modelTypeFallback rows (credential failover precedes model
//     switching — precedence);
//   - modelAttempts stays 1 (B1 separate accounting);
//   - the failover visited DISTINCT credentials (tried-set).
func TestCoordinator_CredFailover_ThreeCredChain(t *testing.T) {
	coord, _, provider, bus := newCredFailoverTestEnv(t, 3, 2, "conv-key-1")

	coord.Start()
	winner := coord.WaitForWinner()

	if winner == nil {
		t.Fatal("expected a winner after credential failover (3rd credential succeeds)")
	}
	if got := coord.failoverAttempts; got != 2 {
		t.Errorf("failoverAttempts = %d, want exactly 2 (len(credentials)-1)", got)
	}
	if got := coord.modelAttemptCountLocked(); got != 1 {
		t.Errorf("modelAttempts = %d, want 1 (main only; credFailover rows are B1-exempt)", got)
	}
	for _, r := range coord.requests {
		if r.modelType == modelTypeFallback {
			t.Errorf("precedence violated: modelTypeFallback row spawned before credential exhaustion")
		}
	}
	// Distinct credentials visited: main + 2 failovers = 3 distinct keys.
	seen := map[string]bool{}
	for _, r := range coord.requests {
		seen[r.resolved.CredentialID] = true
	}
	if len(seen) != 3 {
		t.Errorf("distinct credentials used = %d (%v), want 3", len(seen), seen)
	}
	if provider.callCount() != 3 {
		t.Errorf("provider calls = %d, want 3 (attempt + 2 failovers)", provider.callCount())
	}

	// S4: credFailover rows surface in GetRequestStatuses.
	statuses := coord.GetRequestStatuses()
	if _, ok := statuses["cred_failover_0"]; !ok {
		t.Errorf("GetRequestStatuses missing cred_failover_0: %v", statuses)
	}
	if _, ok := statuses["cred_failover_1"]; !ok {
		t.Errorf("GetRequestStatuses missing cred_failover_1: %v", statuses)
	}
	if statuses["main"] != "failed" {
		t.Errorf("main status = %q, want failed", statuses["main"])
	}

	// Task 20: exactly 2 model_credential_failover events with the
	// canonical payload keys.
	failovers := drainEvents(bus, credentiallb.EventCredentialFailover)
	if len(failovers) != 2 {
		t.Fatalf("model_credential_failover events = %d, want 2", len(failovers))
	}
	for _, evt := range failovers {
		data, _ := evt.Data.(map[string]interface{})
		if data == nil {
			t.Fatalf("failover event Data not a map: %T", evt.Data)
		}
		for _, key := range []string{"model_id", "from_credential_id", "to_credential_id", "reason", "retry_after_ms", "cooldown_ms", "attempt_index"} {
			if _, ok := data[key]; !ok {
				t.Errorf("failover event payload missing key %q: %v", key, data)
			}
		}
		if data["reason"] != "rate_limit" {
			t.Errorf("reason = %v, want rate_limit", data["reason"])
		}
	}
	if evtType := failovers[0].Type; evtType != "model_credential_failover" {
		t.Errorf("event type = %q, want model_credential_failover", evtType)
	}
}

// TestCoordinator_CredFailover_BudgetExhaustionTerminates (Task 19
// acceptance): all 3 credentials 429 → exactly 2 failover attempts,
// then TERMINAL (no infinite loop, no third failover spawn).
func TestCoordinator_CredFailover_BudgetExhaustionTerminates(t *testing.T) {
	coord, _, provider, _ := newCredFailoverTestEnv(t, 3, 99, "conv-key-2") // always 429

	coord.Start()
	winner := coord.WaitForWinner()

	if winner != nil {
		t.Fatalf("expected terminal (all-failed), got winner %v", winner)
	}
	if got := coord.failoverAttempts; got != 2 {
		t.Errorf("failoverAttempts = %d, want exactly 2 (budget cap len-1)", got)
	}
	if got := provider.callCount(); got != 3 {
		t.Errorf("provider calls = %d, want 3 (attempt + 2 failovers, then stop)", got)
	}
	credFailoverRows := 0
	for _, r := range coord.requests {
		if r.modelType == modelTypeCredFailover {
			credFailoverRows++
		}
	}
	if credFailoverRows != 2 {
		t.Errorf("credFailover rows = %d, want 2", credFailoverRows)
	}
}

// TestCoordinator_CredFailover_NonRateLimitSkipsToModelFallback
// (control test, Phase 5 Task 46 shape): a non-rate-limit error must
// NOT spawn any credFailover row — it follows the existing
// model-fallback/terminal path directly.
func TestCoordinator_CredFailover_NonRateLimitSkipsToModelFallback(t *testing.T) {
	coord, _, _, bus := newCredFailoverTestEnv(t, 3, 0, "conv-key-3")

	// Override the provider to fail with a NON-rate-limit error.
	failErr := &providers.ProviderError{Provider: "scripted", StatusCode: 500, Message: "boom"}
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		return &alwaysFailProvider{err: failErr}, nil
	}

	coord.Start()
	winner := coord.WaitForWinner()

	if winner != nil {
		t.Fatalf("expected all-failed terminal, got winner")
	}
	if coord.failoverAttempts != 0 {
		t.Errorf("failoverAttempts = %d, want 0 for non-rate-limit error", coord.failoverAttempts)
	}
	for _, r := range coord.requests {
		if r.modelType == modelTypeCredFailover {
			t.Errorf("non-rate-limit error spawned a credFailover row — control path violated")
		}
	}
	if got := len(drainEvents(bus, credentiallb.EventCredentialFailover)); got != 0 {
		t.Errorf("failover events = %d, want 0", got)
	}
}

type alwaysFailProvider struct {
	err error
}

func (p *alwaysFailProvider) Name() string { return "always-fail" }
func (p *alwaysFailProvider) ChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	return nil, p.err
}
func (p *alwaysFailProvider) StreamChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	return nil, p.err
}
func (p *alwaysFailProvider) IsRetryable(err error) bool { return false }

// TestCoordinator_ModelCredentialSelected_OncePerFirstBinding (Task 6 /
// W-1 / exit #4): model_credential_selected fires EXACTLY ONCE per
// (modelID, conversationKey) pair — the first binding — and NEVER for
// pinned reuse on the follow-up request. Both runs share ONE engine
// (fresh engines would trivially rebind).
func TestCoordinator_ModelCredentialSelected_OncePerFirstBinding(t *testing.T) {
	mock := &mockModelsConfig{}
	for _, id := range []string{"cred-A", "cred-B"} {
		mock.AddCredential(models.CredentialConfig{ID: id, Provider: "openai", APIKey: "key-" + id, BaseURL: "http://upstream-" + id})
	}
	mock.AddModel(models.ModelConfig{
		ID: "m1", Name: "m1", Enabled: true, Internal: true, InternalModel: "up-m1",
		Credentials: models.TestRefs("cred-A", "cred-B"),
	})
	engine := credentiallb.NewEngine(time.Hour, time.Hour, 11, 60*time.Second)
	t.Cleanup(engine.Stop)
	engine.RebindFromStore("m1", models.TestRefs("cred-A", "cred-B"))
	resolver := &engineBackedResolver{mockModelsConfig: mock, engine: engine}

	provider := &scriptedProvider{failN: 0}
	origNew := newProviderClient
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) { return provider, nil }
	t.Cleanup(func() { newProviderClient = origNew })

	run := func(key string) *events.Bus {
		bus := events.NewBus()
		cfg := newTestConfigSnapshot("m1")
		cfg.ModelsConfig = resolver
		cfg.StreamDeadline = 5 * time.Second
		cfg.MaxGenerationTime = 10 * time.Second
		rawBody := []byte(`{"model":"m1","messages":[{"role":"user","content":"hi"}],"stream":false}`)
		coord := newRaceCoordinatorWithEvents(context.Background(), cfg, newTestRequest(), rawBody, []string{"m1"}, bus, "test-req", false, engine, key, false)
		coord.Start()
		if w := coord.WaitForWinner(); w == nil {
			t.Fatalf("no winner for key=%q", key)
		}
		return bus
	}

	bus1 := run("affinity-key-X")
	selected := drainEvents(bus1, "model_credential_selected")
	if len(selected) != 1 {
		t.Fatalf("first binding: model_credential_selected events = %d, want exactly 1", len(selected))
	}
	payload, _ := selected[0].Data.(map[string]interface{})
	if payload == nil {
		t.Fatalf("selected event Data not a map: %T", selected[0].Data)
	}
	if payload["model_id"] != "m1" {
		t.Errorf("event model_id = %v, want m1", payload["model_id"])
	}
	if cid, _ := payload["credential_id"].(string); cid == "" || !strings.HasPrefix(cid, "cred-") {
		t.Errorf("event credential_id = %v, want a cred-* id", payload["credential_id"])
	}
	if pfx, _ := payload["conversation_key_prefix"].(string); pfx != "affinity" {
		t.Errorf("conversation_key_prefix = %q, want first 8 chars of the key", pfx)
	}

	// Same conversation key, fresh coordinator + SAME engine: binding
	// hit → NewlyBound=false → ZERO events.
	bus2 := run("affinity-key-X")
	if got := len(drainEvents(bus2, "model_credential_selected")); got != 0 {
		t.Errorf("pinned reuse: model_credential_selected events = %d, want 0", got)
	}
}

// TestCoordinator_ModelCredentialSelected_EmptyKey_NeverFires (Task 6
// acceptance / C2 / W-2): empty conversationKey requests pick FRESH per
// request, store NO binding, and emit ZERO model_credential_selected
// events — across 5 consecutive requests.
func TestCoordinator_ModelCredentialSelected_EmptyKey_NeverFires(t *testing.T) {
	mock := &mockModelsConfig{}
	for _, id := range []string{"cred-A", "cred-B"} {
		mock.AddCredential(models.CredentialConfig{ID: id, Provider: "openai", APIKey: "key-" + id, BaseURL: "http://upstream-" + id})
	}
	mock.AddModel(models.ModelConfig{
		ID: "m1", Name: "m1", Enabled: true, Internal: true, InternalModel: "up-m1",
		Credentials: models.TestRefs("cred-A", "cred-B"),
	})
	engine := credentiallb.NewEngine(time.Hour, time.Hour, 7, 60*time.Second)
	t.Cleanup(engine.Stop)
	engine.RebindFromStore("m1", models.TestRefs("cred-A", "cred-B"))
	resolver := &engineBackedResolver{mockModelsConfig: mock, engine: engine}

	provider := &scriptedProvider{failN: 0}
	origNew := newProviderClient
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) { return provider, nil }
	t.Cleanup(func() { newProviderClient = origNew })

	total := 0
	for i := 0; i < 5; i++ {
		bus := events.NewBus()
		cfg := newTestConfigSnapshot("m1")
		cfg.ModelsConfig = resolver
		cfg.StreamDeadline = 5 * time.Second
		cfg.MaxGenerationTime = 10 * time.Second
		rawBody := []byte(`{"model":"m1","messages":[{"role":"user","content":"hi"}],"stream":false}`)
		coord := newRaceCoordinatorWithEvents(context.Background(), cfg, newTestRequest(), rawBody, []string{"m1"}, bus, "test-req", false, engine, "", false)
		coord.Start()
		if w := coord.WaitForWinner(); w == nil {
			t.Fatalf("run %d: no winner (empty-key requests must still be served)", i)
		}
		total += len(drainEvents(bus, "model_credential_selected"))
	}
	if total != 0 {
		t.Errorf("empty-key requests emitted %d model_credential_selected events, want 0 (C2)", total)
	}
}

// TestCoordinator_CredFailoverPlan_TriedSetBlocksRepeat (R3-5): the
// tried-set intersection makes a plan for an already-tried credential
// impossible — the coordinator falls to model fallback instead.
func TestCoordinator_CredFailoverPlan_TriedSetBlocksRepeat(t *testing.T) {
	coord, _, _, _ := newCredFailoverTestEnv(t, 2, 0, "conv-key-ts")

	// Mark BOTH credentials as already tried.
	coord.triedCredIDs["cred-A"] = true
	coord.triedCredIDs["cred-B"] = true

	// Force a failed latest request with a rate-limit error.
	req := newUpstreamRequest(0, modelTypeMain, "m1", 1024)
	req.resolved = models.ResolvedCredential{CredentialID: "cred-A"}
	req.resolvedOK = true
	req.MarkFailed(&providers.ProviderError{StatusCode: 429, ErrorType: "rate_limit"})
	coord.requests = append(coord.requests, req)

	plan := coord.credFailoverPlanLocked(req)
	if plan != nil {
		t.Fatalf("plan = %+v, want nil when every credential is already tried", plan)
	}
	// The plan-level tried-set check is the blocker even while the raw
	// budget counter has room — the OUTCOME is model fallback either way.
}

// TestComputeConversationKey_TokenSalting (Task 2a / A-1): two tokens
// with the SAME first user message must produce INDEPENDENT keys (no
// templated-agent-fleet skew); same principal + message is stable.
func TestComputeConversationKey_TokenSalting(t *testing.T) {
	k1 := credentiallb.ComputeConversationKey("m1", "token-1", "hello world")
	k2 := credentiallb.ComputeConversationKey("m1", "token-2", "hello world")
	k1b := credentiallb.ComputeConversationKey("m1", "token-1", "hello world")

	if k1 == k2 {
		t.Errorf("two tokens with identical message produced the same key — token salting defeated (A-1)")
	}
	if k1 != k1b {
		t.Errorf("same (model, token, message) produced different keys — not deterministic")
	}
	if len(k1) != 64 {
		t.Errorf("key length = %d, want 64 hex chars", len(k1))
	}
}

// drainEvents reads all currently-buffered events of the given type
// off the subscription channel (non-blocking) and unsubscribes.
func drainEvents(bus *events.Bus, eventType string) []events.Event {
	ch, err := bus.Subscribe()
	if err != nil {
		return nil
	}
	defer bus.Unsubscribe(ch)
	var out []events.Event
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return out
			}
			if evt.Type == eventType {
				out = append(out, evt)
			}
		default:
			return out
		}
	}
}

// TestCoordinator_CredFailover_TwoModelChain_WrongModelFix (Round 3j
// C1 critical — the guardrail). The legacy 675556f code hard-coded
// c.models[0] in the modelTypeCredFailover spawn case; in a 2-model
// chain, a fallback-row 429 would have spawned a credFailover attempt
// targeting m1's modelID with m2's picked credential pool — running
// m2's credential against m1's InternalModel and bumping m1's billing
// for a request meant for m2. The fix rides the source modelID on the
// trigger info (C3(2)) so the spawned row stays on the SAME model that
// 429'd.
//
// All existing tests use single-model chains — that blind spot hid the
// bug. This test is the only regression guard; it MUST cover the
// 2-model case. Two layers of assertion:
//
//  1. spawn() targets triggerInfo.modelID, NOT c.models[0]
//     (the C1 fix at race_coordinator.go:spawn).
//  2. resolveSpecificInternalCredential refuses a credential NOT in
//     the model's Credentials list (the C1 belt-and-braces at
//     race_executor.go:resolveSpecificInternalCredential).
//
// NOTE on integration: the direct-spawn() design was chosen because
// a full integration test that fires Case 1 on the fallback row in a
// 2-model chain requires running concurrent scripted providers with
// timing coordination. The spawn-layer and resolver-layer guardrails
// in this test exercise the exact code paths the C1 fix changed, and
// they fail loudly if reverted.
func TestCoordinator_CredFailover_TwoModelChain_WrongModelFix(t *testing.T) {
	mock := &mockModelsConfig{}
	// m1 credentials (the LEGACY pool that the bug would have used)
	m1IDs := []string{"cred-A", "cred-B", "cred-C"}
	for _, id := range m1IDs {
		mock.AddCredential(models.CredentialConfig{ID: id, Provider: "openai", APIKey: "key-" + id, BaseURL: "http://upstream-" + id})
	}
	// m2 credentials (the CORRECT pool for the fallback row's 429)
	m2IDs := []string{"cred-X", "cred-Y"}
	for _, id := range m2IDs {
		mock.AddCredential(models.CredentialConfig{ID: id, Provider: "openai", APIKey: "key-" + id, BaseURL: "http://upstream-" + id})
	}
	// Both models are internal + multi-credential. Distinct internal
	// model names so the test can assert the credFailover row ran
	// the RIGHT model (m2's, not m1's).
	mock.AddModel(models.ModelConfig{
		ID: "m1", Name: "m1", Enabled: true, Internal: true,
		InternalModel: "internal-m1",
		Credentials:   models.TestRefs(m1IDs...),
	})
	mock.AddModel(models.ModelConfig{
		ID: "m2", Name: "m2", Enabled: true, Internal: true,
		InternalModel: "internal-m2",
		Credentials:   models.TestRefs(m2IDs...),
	})

	engine := credentiallb.NewEngine(time.Hour, time.Hour, 103, 60*time.Second)
	t.Cleanup(engine.Stop)
	engine.RebindFromStore("m1", models.TestRefs(m1IDs...))
	engine.RebindFromStore("m2", models.TestRefs(m2IDs...))

	resolver := &engineBackedResolver{mockModelsConfig: mock, engine: engine}

	bus := events.NewBus()
	cfg := newTestConfigSnapshot("m1")
	cfg.ModelsConfig = resolver
	cfg.StreamDeadline = 5 * time.Second
	cfg.MaxGenerationTime = 10 * time.Second

	rawBody := []byte(`{"model":"m1","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	coord := newRaceCoordinatorWithEvents(context.Background(), cfg, newTestRequest(), rawBody,
		[]string{"m1", "m2"}, bus, "test-req", false, engine, "conv-key-2model", false)

	// ── Layer 1 — spawn() targets triggerInfo.modelID (the C1 fix) ──
	// Simulate the Case-1 classification site: the fallback row just
	// 429'd and the coordinator pre-resolved a credential from m2's
	// pool. The trigger info MUST carry m2 as the source modelID so
	// spawn() targets m2 (NOT c.models[0] = m1).
	coord.spawn(modelTypeCredFailover, spawnTriggerInfo{
		trigger:       triggerRateLimit,
		credentialID:  "cred-X",
		modelID:       "m2", // C1 fix: ride the source modelID
		failedRequest: -1,
	})

	// Find the spawned row (must be the only credFailover row so far).
	var cf *upstreamRequest
	for _, r := range coord.requests {
		if r.modelType == modelTypeCredFailover {
			cf = r
			break
		}
	}
	if cf == nil {
		t.Fatalf("spawn did not create a credFailover row")
	}
	if cf.modelID != "m2" {
		t.Errorf("C1 REGRESSION: spawned credFailover row modelID = %q, want %q (the fix routes via triggerInfo.modelID; legacy hard-coded c.models[0])",
			cf.modelID, "m2")
	}
	if cf.reselectedCredentialID != "cred-X" {
		t.Errorf("reselectedCredentialID = %q, want cred-X", cf.reselectedCredentialID)
	}

	// ── Layer 2 — verify the belt-and-braces in resolveSpecificInternalCredential ──
	// The bug fundamentally was a CROSS-MODEL resolve: m2's cred being
	// handed to m1's model. The belt-and-braces refuses any credential
	// NOT in the model's own Credentials list.
	if _, ok := resolveSpecificInternalCredential(cfg, "m1", "cred-X"); ok {
		t.Errorf("belt-and-braces BROKEN: resolveSpecificInternalCredential accepted m2's credential cred-X for m1 — cross-model resolve would bill m1 for an m2 request")
	}
	if _, ok := resolveSpecificInternalCredential(cfg, "m2", "cred-A"); ok {
		t.Errorf("belt-and-braces BROKEN: resolveSpecificInternalCredential accepted m1's credential cred-A for m2 — cross-model resolve")
	}
	// Sanity — credentials of the SAME model still resolve.
	if _, ok := resolveSpecificInternalCredential(cfg, "m2", "cred-X"); !ok {
		t.Errorf("belt-and-braces TOO STRICT: same-model resolve for m2/cred-X refused")
	}
	if _, ok := resolveSpecificInternalCredential(cfg, "m1", "cred-A"); !ok {
		t.Errorf("belt-and-braces TOO STRICT: same-model resolve for m1/cred-A refused")
	}

	// ── Layer 3 — negative test for the legacy fallback path ──
	// Without triggerInfo.modelID, spawn() falls back to c.models[0]
	// (the legacy single-model chain — preserves the W2 reasoning
	// that defensive fallback is needed for tests / corrupt triggers).
	// This is DOCUMENTED behavior, not a bug, but it MUST select m1
	// (the existing single-model chain) so unit tests that omit
	// modelID don't silently misfire.
	coord2 := newRaceCoordinatorWithEvents(context.Background(), cfg, newTestRequest(), rawBody,
		[]string{"m1", "m2"}, bus, "test-req-2", false, engine, "conv-key-2model-neg", false)
	coord2.spawn(modelTypeCredFailover, spawnTriggerInfo{
		trigger:       triggerRateLimit,
		credentialID:  "cred-Y",
		modelID:       "", // legacy: omit the source modelID
		failedRequest: -1,
	})
	var cf2 *upstreamRequest
	for _, r := range coord2.requests {
		if r.modelType == modelTypeCredFailover {
			cf2 = r
			break
		}
	}
	if cf2 == nil {
		t.Fatalf("negative-test spawn did not create a credFailover row")
	}
	if cf2.modelID != "m1" {
		t.Errorf("legacy fallback modelID = %q, want %q (c.models[0] — preserves single-model chain defensively)",
			cf2.modelID, "m1")
	}

	_ = resolver // anchored for future test expansion

	// JOIN the fire-and-forget executors launched by spawn() above:
	// they call the newProviderClient seam (a package var) while the
	// NEXT test's env builder writes its own override there — an
	// unjoined executor is a race-detector exposure across tests.
	joinExecutors := func(c *raceCoordinator) {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			allDone := true
			for _, r := range c.requests {
				if !r.IsDone() {
					allDone = false
					break
				}
			}
			if allDone {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	joinExecutors(coord)
	joinExecutors(coord2)
}

// ─────────────────────────────────────────────────────────────────────────────
// Phase 2 / real-streaming-default — review row (f): live-mode credential
// failover window pin. Post-first-byte (post-winner) the credential
// failover window is CLOSED: a rate-limit error hitting the winner's
// attempt AFTER the first forwarded byte spawns NOTHING and the stream
// terminates with the error envelope. Pre-first-byte 429 failover is
// UNCHANGED in live mode (companion).
// ─────────────────────────────────────────────────────────────────────────────

// liveStreamScriptedProvider is a streaming scripted provider for the
// live-mode failover window tests:
//   - mode "post-byte": call 1 streams ONE content event (the first
//     forwardable byte — winner selection), then errors with a 429
//     ProviderError (HTTP-status rate-limit path);
//   - mode "pre-byte": call 1 returns a 429 ProviderError immediately
//     (no byte); call 2 succeeds with content + done.
type liveStreamScriptedProvider struct {
	mu      sync.Mutex
	mode    string // "post-byte" | "pre-byte"
	calls   int
	apiKeys []string
}

func (p *liveStreamScriptedProvider) Name() string { return "live-scripted" }

func (p *liveStreamScriptedProvider) ChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	return nil, &providers.ProviderError{StatusCode: 500, Message: "unused non-stream path"}
}

func (p *liveStreamScriptedProvider) StreamChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.mu.Unlock()

	rateLimitErr := &providers.ProviderError{
		Provider:   "live-scripted",
		StatusCode: 429,
		Message:    "rate limited (live scripted)",
		ErrorType:  "rate_limit",
		ErrorCode:  "rate_limit",
		Retryable:  true,
	}

	if p.mode == "pre-byte" && n == 1 {
		// 429 BEFORE any byte — the classic pre-first-byte failover case.
		return nil, rateLimitErr
	}

	ch := make(chan providers.StreamEvent, 4)
	ch <- providers.StreamEvent{Type: "content", Content: "live-first-byte"} // first forwardable byte
	if p.mode == "post-byte" {
		// HOLD the error past at least one manage-loop tick (100ms) so
		// the first-byte winner gate actually observes
		// `TotalLen>0 && err==nil`; the 429 then hits the WINNER's
		// attempt mid-stream (post-first-byte window).
		go func() {
			time.Sleep(300 * time.Millisecond)
			ch <- providers.StreamEvent{Type: "error", Error: rateLimitErr}
			close(ch)
		}()
		return ch, nil
	}
	ch <- providers.StreamEvent{
		Type:         "done",
		FinishReason: "stop",
		Response: &providers.ChatCompletionResponse{
			ID: "chatcmpl-live", Object: "chat.completion", Created: time.Now().Unix(), Model: req.Model,
			Usage: providers.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
		},
	}
	close(ch)
	return ch, nil
}

func (p *liveStreamScriptedProvider) IsRetryable(err error) bool { return true }

func (p *liveStreamScriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// newLiveCredFailoverEnv wires the engine-backed 2-credential harness with
// a STREAMING scripted provider and the LIVE first-byte gate (the existing
// newCredFailoverTestEnv hardcodes liveFirstByteGate=false and a
// non-stream body — the live variant needs its own builder).
func newLiveCredFailoverEnv(t *testing.T, mode string) (*raceCoordinator, *liveStreamScriptedProvider, *events.Bus) {
	t.Helper()

	mock := &mockModelsConfig{}
	ids := []string{"cred-A", "cred-B"}
	for _, id := range ids {
		if err := mock.AddCredential(models.CredentialConfig{
			ID: id, Provider: "openai", APIKey: "key-" + id, BaseURL: "http://upstream-" + id,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := mock.AddModel(models.ModelConfig{
		ID: "m1", Name: "m1", Enabled: true, Internal: true, InternalModel: "upstream-m1",
		Credentials: models.TestRefs(ids...),
	}); err != nil {
		t.Fatal(err)
	}

	engine := credentiallb.NewEngine(time.Hour, time.Hour, 42, 60*time.Second)
	t.Cleanup(engine.Stop)
	engine.RebindFromStore("m1", models.TestRefs(ids...))

	resolver := &engineBackedResolver{mockModelsConfig: mock, engine: engine}

	provider := &liveStreamScriptedProvider{mode: mode}
	origNew := newProviderClient
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		provider.mu.Lock()
		provider.apiKeys = append(provider.apiKeys, apiKey)
		provider.mu.Unlock()
		return provider, nil
	}
	t.Cleanup(func() { newProviderClient = origNew })

	bus := events.NewBus()

	cfg := newTestConfigSnapshot("m1")
	cfg.ModelsConfig = resolver
	cfg.StreamDeadline = 5 * time.Second
	cfg.MaxGenerationTime = 10 * time.Second

	rawBody := []byte(`{"model":"m1","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	coord := newRaceCoordinatorWithEvents(context.Background(), cfg, newTestRequest(), rawBody,
		[]string{"m1"}, bus, "live-failover-req", false, engine, "conv-live-failover", true /* liveFirstByteGate */)
	return coord, provider, bus
}

// TestLiveRelay_NoCredFailoverAfterFirstByte (review row f): the winner's
// attempt 429s AFTER the first forwarded byte. The post-first-byte window
// is CLOSED — no credFailover spawn, no model_credential_failover event;
// the stream terminates with the buffer error (terminal envelope upstream
// of the client).
func TestLiveRelay_NoCredFailoverAfterFirstByte(t *testing.T) {
	coord, provider, bus := newLiveCredFailoverEnv(t, "post-byte")

	coord.Start()
	winner := coord.WaitForWinner()
	if winner == nil {
		t.Fatal("expected a first-byte winner (provider streams a content event before erroring)")
	}

	// The winner's stream must terminate with the rate-limit error.
	select {
	case <-winner.GetBuffer().Done():
	case <-time.After(3 * time.Second):
		t.Fatal("winner stream never terminated after the post-first-byte 429")
	}
	if err := winner.GetBuffer().Err(); err == nil {
		t.Fatal("expected the winner buffer to close with the rate-limit error")
	}

	// NO credential failover fired: single provider call (the original
	// attempt), zero failover bookkeeping, zero failover rows/events.
	if got := provider.callCount(); got != 1 {
		t.Errorf("provider calls = %d, want 1 (any second call would be a post-first-byte failover spawn)", got)
	}
	if got := coord.failoverAttempts; got != 0 {
		t.Errorf("failoverAttempts = %d, want 0 (post-first-byte window closed)", got)
	}
	for _, r := range coord.requests {
		if r.modelType == modelTypeCredFailover {
			t.Errorf("credFailover row spawned post-first-byte: %+v", r)
		}
	}
	if failovers := drainEvents(bus, credentiallb.EventCredentialFailover); len(failovers) != 0 {
		t.Errorf("model_credential_failover events = %d, want 0 post-first-byte", len(failovers))
	}
}

// TestLiveRelay_PreFirstByte429_FailoverUnchangedInLiveMode (review row f
// companion): a 429 BEFORE any forwardable byte still fails over to the
// second credential in LIVE mode — pre-winner semantics are untouched by
// the gate swap.
func TestLiveRelay_PreFirstByte429_FailoverUnchangedInLiveMode(t *testing.T) {
	coord, provider, bus := newLiveCredFailoverEnv(t, "pre-byte")

	coord.Start()
	winner := coord.WaitForWinner()
	if winner == nil {
		t.Fatal("expected failover to rescue the request (2nd credential succeeds)")
	}

	// Exactly one failover: main (429) → cred_failover row (success).
	if got := coord.failoverAttempts; got != 1 {
		t.Errorf("failoverAttempts = %d, want 1 (pre-first-byte failover UNCHANGED in live mode)", got)
	}
	if got := provider.callCount(); got != 2 {
		t.Errorf("provider calls = %d, want 2 (main 429 + failover success)", got)
	}
	seen := map[string]bool{}
	for _, r := range coord.requests {
		seen[r.resolved.CredentialID] = true
	}
	if len(seen) != 2 {
		t.Errorf("distinct credentials used = %d (%v), want 2", len(seen), seen)
	}
	if failovers := drainEvents(bus, credentiallb.EventCredentialFailover); len(failovers) != 1 {
		t.Errorf("model_credential_failover events = %d, want 1", len(failovers))
	}

	// The winner carries the rescued content and completes cleanly.
	select {
	case <-winner.GetBuffer().Done():
	case <-time.After(3 * time.Second):
		t.Fatal("failover winner never completed")
	}
	if err := winner.GetBuffer().Err(); err != nil {
		t.Errorf("failover winner buffer error = %v, want clean completion", err)
	}
}
