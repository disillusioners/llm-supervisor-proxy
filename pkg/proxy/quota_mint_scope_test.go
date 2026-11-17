package proxy

// ─────────────────────────────────────────────────────────────────────────────
// quota_mint_scope_test.go — the MINT-SCOPE acceptance tests
// (wi-4b8d21e7).
//
// THE INCIDENT (2026-11-17 03:56:55Z): an ACCOUNT-scoped body (the
// Token-Plan class) minted a MODEL-layer mark. The same request's
// credential-failover attempt was then suppressed at zero cost by the
// model-layer read site — ONE DRY ACCOUNT HID THE MODEL'S OTHER
// (HEALTHY) CREDENTIALS.
//
// THE RULING (verbatim):
//
//	"basically, a model need try all credential before declare quota
//	 limit reach. If a request (hash) bound to quota limit cred ->
//	 proxy need auto switch cred."
//
// The tests below pin:
//   - the 03:56 shape — a model with 2 credentials, one Token-Plan
//     walled, MUST rotate to the healthy credential and succeed;
//   - the all-credentials-walled case — the model must NOT be
//     suppressed by a model mark (the degradation is the upstream
//     layer's: prefer clean sibling → soonest-recovering → one honest
//     attempt);
//   - the hash-bound binding — the MEMBER (model) is preserved while
//     the CREDENTIAL moves (member affinity with credential mobility);
//   - the regular per-credential quota rotation (regression pin).
// ─────────────────────────────────────────────────────────────────────────────

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelcache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/skipcache"
)

// quotaScopeEnv is the shared state of the quota-scope harness: the
// API key of every upstream call, in order.
//
// The recorder itself is the SHARED upstreamCallRecorder (see
// upstream_call_recorder_test.go) — the Anthropic-lane harness uses the
// same one, so the two lanes cannot drift apart in what they observe.
type quotaScopeEnv struct {
	upstreamCallRecorder
}

// quotaScopeProvider serves ONE credential. A WALLED credential answers
// the OBSERVED live body — HTTP 402 + "Token Plan usage limit" (the
// z.ai code-2067 shape, wired exactly like the existing
// quotaScriptedProvider: the numeric code cannot survive into the
// string-typed ErrorCode field, so the class is carried by the status
// + the message text). Every other credential serves normally.
type quotaScopeProvider struct {
	env    *quotaScopeEnv
	apiKey string
	walled bool
}

func (p *quotaScopeProvider) Name() string { return "quota-scope" }

func (p *quotaScopeProvider) ChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	p.env.record(p.apiKey)
	if p.walled {
		return nil, &providers.ProviderError{
			Provider:   "quota-scope",
			StatusCode: 402,
			Message:    "Token Plan usage limit",
			ErrorCode:  "",
			Retryable:  false,
		}
	}
	return &providers.ChatCompletionResponse{
		ID:      "chatcmpl-quota-scope",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []providers.Choice{
			{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
		},
		Usage: providers.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
	}, nil
}

func (p *quotaScopeProvider) StreamChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	return nil, &providers.ProviderError{StatusCode: 402, Message: "Token Plan usage limit", ErrorCode: ""}
}

func (p *quotaScopeProvider) IsRetryable(err error) bool { return true }

// newQuotaScopeEnv builds a race coordinator over a chain of internal
// models, each with its own credential pool (cred-<model>-A/B/…).
//
// walledCredIDs names the credentials whose ACCOUNT is exhausted — they
// answer the Token-Plan 402; every other credential serves normally.
//
// pinnedCredID (when set) pins the conversation-key binding to that
// credential before the run: the engine's pick is weighted-random, and
// the incident is about the credential the HASH is bound to. The pin
// makes the first attempt deterministic.
//
// Returns the coordinator, the call recorder, the engine (whose
// credential-layer state the assertions read) and the model-layer cache
// (whose marks the assertions read).
func newQuotaScopeEnv(t *testing.T, chain []layeredChainModel, walledCredIDs map[string]bool, pinnedCredID, conversationKey string) (*raceCoordinator, *quotaScopeEnv, *credentiallb.Engine, *modelcache.Cache, *skipcache.Cache) {
	t.Helper()

	mock := &mockModelsConfig{}
	engine := credentiallb.NewEngine(time.Hour, time.Hour, 42, 60*time.Second)
	t.Cleanup(engine.Stop)

	// The credentials point at a LOCAL stub server. The stubbed
	// provider never uses it, but a spawn that resolves AFTER the
	// newProviderClient stub is restored would otherwise build a REAL
	// client and attempt an OUTBOUND request (the review's stray-attempt
	// risk). With a local endpoint the late resolve is harmless.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(stub.Close)

	modelIDs := make([]string, 0, len(chain))
	for _, m := range chain {
		ids := make([]string, 0, m.credCount)
		for i := 0; i < m.credCount; i++ {
			id := "cred-" + m.id + "-" + string(rune('A'+i))
			ids = append(ids, id)
			mock.AddCredential(models.CredentialConfig{
				ID: id, Provider: "openai", APIKey: "key-" + id, BaseURL: stub.URL,
			})
		}
		mock.AddModel(models.ModelConfig{
			ID: m.id, Name: m.id, Enabled: true, Internal: true,
			InternalModel: "internal-" + m.id,
			Credentials:   models.TestRefs(ids...),
		})
		engine.RebindFromStore(m.id, models.TestRefs(ids...))
		modelIDs = append(modelIDs, m.id)
	}

	resolver := &engineBackedResolver{mockModelsConfig: mock, engine: engine}

	env := &quotaScopeEnv{}
	origNew := newProviderClient
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		credID := strings.TrimPrefix(apiKey, "key-")
		return &quotaScopeProvider{env: env, apiKey: apiKey, walled: walledCredIDs[credID]}, nil
	}
	t.Cleanup(func() { newProviderClient = origNew })

	if pinnedCredID != "" {
		engine.InjectPreconditionStateForTest(modelIDs[0], conversationKey, pinnedCredID)
	}

	bus := events.NewBus()
	cfg := newTestConfigSnapshot(modelIDs[0])
	cfg.ModelsConfig = resolver
	cfg.UpstreamSkipCache = skipcache.New()
	cfg.UpstreamSkipCacheTTL = 60 * time.Second
	cfg.ModelSkipCache = modelcache.New()
	cfg.ModelSkipCacheTTL = 30 * time.Minute
	cfg.StreamDeadline = 5 * time.Second
	cfg.MaxGenerationTime = 10 * time.Second

	rawBody := []byte(`{"model":"` + modelIDs[0] + `","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	coord := newRaceCoordinatorWithEvents(context.Background(), cfg, newTestRequest(), rawBody, modelIDs, bus, "quota-scope-req", false, engine, conversationKey, false, nil)
	return coord, env, engine, cfg.ModelSkipCache, cfg.UpstreamSkipCache
}

// TestQuotaMintScope_TokenPlanAccountWallRotatesToHealthyCredential is
// THE 03:56:55Z shape, red-first.
//
// Model m1 has TWO credentials; credential A's ACCOUNT is Token-Plan
// walled, credential B is healthy.
//
// BASE behavior (before the fix): the account body minted a MODEL mark,
// so the credential-failover attempt on the SAME model was suppressed at
// zero cost — credential B was never called and the request fell
// through to the fallback model.
//
// POST-FIX behavior: the account body mints the CREDENTIAL layer only
// (the cooldown row + the upstream demotion); the model is NOT marked,
// the rotation reaches credential B, and m1 serves the request.
func TestQuotaMintScope_TokenPlanAccountWallRotatesToHealthyCredential(t *testing.T) {
	const convKey = "conv-quota-mint-scope-0356"
	coord, env, engine, modelCache, upstream := newQuotaScopeEnv(t,
		[]layeredChainModel{{id: "m1", credCount: 2}, {id: "m2", credCount: 2}},
		map[string]bool{"cred-m1-A": true}, // the Token-Plan-walled ACCOUNT
		"cred-m1-A", convKey)

	coord.Start()
	winner := coord.WaitForWinner()
	if winner == nil {
		t.Fatal("no winner: the healthy sibling credential must serve the request")
	}
	if winner.modelID != "m1" {
		t.Fatalf("winner model = %q, want m1 — a walled ACCOUNT must not push the request onto the fallback model", winner.modelID)
	}
	if !env.called("key-cred-m1-B") {
		t.Fatalf("the healthy sibling credential was never called (calls=%v) — the model-layer mark suppressed the credential failover", env.calledKeys())
	}
	if modelCache.Marked("m1") {
		t.Fatal("the model layer marked m1 — an account-scoped body must never mint a model mark")
	}
	// The walled ACCOUNT is benched at the CREDENTIAL layer, for the
	// quota-class window (the engine default, 15m).
	until, ok := engine.CooldownUntil("m1", "cred-m1-A")
	if !ok {
		t.Fatal("the walled credential has no cooldown row — the account body must mint at the credential layer")
	}
	if d := time.Until(until); d < 14*time.Minute || d > 16*time.Minute {
		t.Fatalf("credential cooldown = %v, want ~15m (the quota-class cooldown)", d)
	}
	// The UPSTREAM layer demoted the walled account as well (the
	// account body's marks live at the credential + upstream layers).
	if !upstream.Marked(skipcache.ScopeCredential, "cred-m1-A", "internal-m1") {
		t.Fatal("the upstream layer did not demote the walled credential — the account body must mint at the upstream layer")
	}
	if upstream.Marked(skipcache.ScopeCredential, "cred-m1-B", "internal-m1") {
		t.Fatal("the healthy sibling credential was demoted — the walled ACCOUNT must not demote its siblings")
	}
	// The rotation happened on the SAME model (the member binding is
	// preserved) and on a DIFFERENT credential.
	var failoverCred string
	for _, r := range coord.requests {
		if r.modelType == modelTypeCredFailover {
			failoverCred = r.resolved.CredentialID
			if r.modelID != "m1" {
				t.Fatalf("cred-failover row model = %q, want m1 (the member binding must be preserved)", r.modelID)
			}
		}
	}
	if failoverCred != "cred-m1-B" {
		t.Fatalf("cred-failover credential = %q, want cred-m1-B (the healthy sibling)", failoverCred)
	}
}

// TestQuotaMintScope_AllCredentialsWalled_NoModelSuppression is the
// all-exhausted case: EVERY credential of the model is walled.
//
// The model must NOT be suppressed by a model mark — the degradation is
// the upstream layer's (prefer a clean sibling → else the soonest-
// recovering one → one honest attempt). The request therefore pays the
// honest attempts and the chain serves the fallback model.
func TestQuotaMintScope_AllCredentialsWalled_NoModelSuppression(t *testing.T) {
	const convKey = "conv-quota-mint-scope-all-walled"
	coord, env, engine, modelCache, upstream := newQuotaScopeEnv(t,
		[]layeredChainModel{{id: "m1", credCount: 2}, {id: "m2", credCount: 2}},
		map[string]bool{"cred-m1-A": true, "cred-m1-B": true}, // BOTH accounts walled
		"cred-m1-A", convKey)

	coord.Start()
	winner := coord.WaitForWinner()
	if winner == nil {
		t.Fatal("no winner: the chain must serve the request once the model's pool is exhausted")
	}
	if winner.modelID != "m2" {
		t.Fatalf("winner model = %q, want m2 (the chain target after the model's pool is exhausted)", winner.modelID)
	}
	if modelCache.Marked("m1") {
		t.Fatal("the model layer marked m1 — an account-scoped body must never mint a model mark (not even when every credential is exhausted)")
	}
	// Both credentials were honestly attempted (no zero-cost suppression).
	for _, cred := range []string{"key-cred-m1-A", "key-cred-m1-B"} {
		if !env.called(cred) {
			t.Fatalf("credential %s was never attempted (calls=%v) — the model-layer mark suppressed the honest attempt", cred, env.calledKeys())
		}
	}
	// The UPSTREAM layer demoted BOTH walled accounts (this mint is not
	// budget-gated — every limit-class failure writes it).
	for _, credID := range []string{"cred-m1-A", "cred-m1-B"} {
		if !upstream.Marked(skipcache.ScopeCredential, credID, "internal-m1") {
			t.Fatalf("the upstream layer did not demote credential %s — the account body must mint at the upstream layer", credID)
		}
	}
	// The CREDENTIAL layer benched the rotated account for the
	// quota-class window (the rotation's exclusion writes it).
	until, ok := engine.CooldownUntil("m1", "cred-m1-A")
	if !ok {
		t.Fatal("the rotated-away account has no credential-layer cooldown row")
	}
	if d := time.Until(until); d < 14*time.Minute || d > 16*time.Minute {
		t.Fatalf("credential cooldown = %v, want ~15m (the quota-class cooldown)", d)
	}
}

// TestQuotaMintScope_AllCooling_OneHonestAttemptSoonestRecovering is the
// all-cooling degradation pin: every credential of the model is
// already cooling, so the rotation has no clean sibling and must make
// ONE honest attempt with the SOONEST-RECOVERING credential (the W3
// single-shot), then advance into the chain.
//
// The model layer must hold no mark: the degradation belongs to the
// upstream/credential layer, and an attempt is never EXCLUDED.
func TestQuotaMintScope_AllCooling_OneHonestAttemptSoonestRecovering(t *testing.T) {
	const convKey = "conv-quota-mint-scope-all-cooling"
	coord, env, engine, modelCache, _ := newQuotaScopeEnv(t,
		[]layeredChainModel{{id: "m1", credCount: 3}, {id: "m2", credCount: 2}},
		map[string]bool{"cred-m1-A": true, "cred-m1-B": true, "cred-m1-C": true},
		"cred-m1-C", convKey)
	// cred-m1-A and cred-m1-B are ALREADY cooling (the engine's default
	// cooldown) — only cred-m1-C is servable, and it is walled too.
	engine.InjectAllCoolingForTest("m1", "cred-m1-C")

	coord.Start()
	winner := coord.WaitForWinner()
	if winner == nil {
		t.Fatal("no winner: the chain must serve the request")
	}
	if modelCache.Marked("m1") {
		t.Fatal("the model layer marked m1 — an account-scoped body must never mint a model mark")
	}
	// Exactly ONE credential-failover row: the single honest attempt
	// with the soonest-recovering credential (the W3 single-shot).
	var failoverRows []string
	for _, r := range coord.requests {
		if r.modelType == modelTypeCredFailover {
			failoverRows = append(failoverRows, r.resolved.CredentialID)
		}
	}
	if len(failoverRows) != 1 {
		t.Fatalf("cred-failover rows = %v, want exactly 1 (the all-cooling single-shot)", failoverRows)
	}
	if failoverRows[0] != "cred-m1-A" {
		t.Fatalf("the honest attempt used %q, want cred-m1-A (the soonest-recovering credential)", failoverRows[0])
	}
	if !env.called("key-cred-m1-A") {
		t.Fatalf("the soonest-recovering credential was never attempted (calls=%v)", env.calledKeys())
	}
}

// TestQuotaMintScope_HashBoundMemberAutoSwitch is the hash-bound
// requirement: "a request (hash) bound to quota limit cred -> proxy
// need auto switch cred", WITHOUT losing the member binding.
//
// The conversation key is bound to the WALLED credential. After the
// request the binding must point at the SIBLING credential (the
// credential moved) while the MEMBER (the model) is unchanged — member
// affinity with credential mobility.
//
// THE OBSERVATION IS READ-ONLY (review B-1): the binding is read with
// credentiallb.Engine.BoundCredentialForTest, NOT with GetOrSelect.
// GetOrSelect is get-or-SELECT — a cooling binding is dropped and a
// fresh pick is written — so a GetOrSelect-based assertion writes the
// state it then asserts: it cannot distinguish "the failover rebound
// the key" from "my observation re-picked it". Deleting the engine's
// rebind write leaves a GetOrSelect-based test PASSING; the read-only
// observation fails it (the bound credential stays the walled one).
//
// TWO-POINT OBSERVATION: BEFORE the request the key is bound to the
// walled credential; AFTER the request the SAME key is bound to the
// healthy sibling.
//
// The rebind's "was this a first binding" flag is deliberately NOT
// pinned here: the standing ruling keeps "one rebind per active
// conversation per 30m" as intended behavior, and this assertion must
// not constrain it.
func TestQuotaMintScope_HashBoundMemberAutoSwitch(t *testing.T) {
	const convKey = "conv-quota-mint-scope-hash-bound"
	coord, env, engine, _, _ := newQuotaScopeEnv(t,
		[]layeredChainModel{{id: "m1", credCount: 2}, {id: "m2", credCount: 2}},
		map[string]bool{"cred-m1-A": true},
		"cred-m1-A", convKey)

	// BEFORE — the hash is bound to the WALLED credential (read-only).
	before, bound := engine.BoundCredentialForTest("m1", convKey)
	if !bound {
		t.Fatal("no hash binding before the request — the conversation key must be bound to a credential")
	}
	if before != "cred-m1-A" {
		t.Fatalf("hash-bound credential BEFORE = %q, want cred-m1-A (the walled account)", before)
	}

	coord.Start()
	winner := coord.WaitForWinner()
	if winner == nil {
		t.Fatal("no winner: the hash-bound request must auto-switch to the sibling credential")
	}
	if winner.modelID != "m1" {
		t.Fatalf("winner model = %q, want m1 — the member binding must survive the credential switch", winner.modelID)
	}
	if !env.called("key-cred-m1-B") {
		t.Fatalf("the sibling credential was never called (calls=%v)", env.calledKeys())
	}
	// AFTER — the SAME key is bound to the healthy sibling: the
	// credential is the mobile part of the binding, the member (the
	// model) is not.
	after, bound := engine.BoundCredentialForTest("m1", convKey)
	if !bound {
		t.Fatal("no hash binding after the request")
	}
	if after != "cred-m1-B" {
		t.Fatalf("hash-bound credential AFTER = %q, want cred-m1-B — the binding must follow the auto-switch", after)
	}
}

// TestQuotaMintScope_PerCredentialQuotaRotationRegression is the
// regular per-credential quota rotation pin: the credential-layer
// rotation (and the 15m quota cooldown) still work, and the model layer
// still holds NO mark for the account body.
func TestQuotaMintScope_PerCredentialQuotaRotationRegression(t *testing.T) {
	const convKey = "conv-quota-mint-scope-rotation"
	coord, env, engine, modelCache, _ := newQuotaScopeEnv(t,
		[]layeredChainModel{{id: "m1", credCount: 2}, {id: "m2", credCount: 2}},
		map[string]bool{"cred-m1-A": true},
		"cred-m1-A", convKey)

	coord.Start()
	winner := coord.WaitForWinner()
	if winner == nil {
		t.Fatal("no winner")
	}
	if winner.modelID != "m1" {
		t.Fatalf("winner model = %q, want m1 (the credential rotation must serve the request)", winner.modelID)
	}
	if !env.called("key-cred-m1-B") {
		t.Fatalf("the sibling credential was never called (calls=%v)", env.calledKeys())
	}
	if modelCache.Marked("m1") {
		t.Fatal("the model layer marked m1 — the account body must mint at the credential layer only")
	}
	until, ok := engine.CooldownUntil("m1", "cred-m1-A")
	if !ok {
		t.Fatal("the walled credential has no cooldown row")
	}
	if d := time.Until(until); d < 14*time.Minute || d > 16*time.Minute {
		t.Fatalf("credential cooldown = %v, want ~15m (the quota-class cooldown, not the 60s rate-limit one)", d)
	}
}
