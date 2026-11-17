package proxy

// ─────────────────────────────────────────────────────────────────────────────
// anthropic_quota_class_test.go — the ANTHROPIC-lane quota-class pin
// (wi-4b8d21e7, independent review finding C-2).
//
// WHAT THE BRANCH CHANGED (the code under test):
//
//   pkg/proxy/handler_anthropic.go:888-905 — the Anthropic
//   passthrough lane's credential failover now selects the cooldown
//   CLASS before it reseeds the failed credential:
//
//	isAnthropicQuotaFailure(arc) → credentiallb.FailureClassQuota
//	→ h.credEngine.ExcludeAndReselectForClass(...)
//
//   pkg/providers/interface.go:457 — providers.IsQuotaMessage, the
//   exported twin of the chat lane's message check, so the Anthropic
//   lane (which holds a RAW body, no *ProviderError) classifies a
//   quota rejection with the SAME vocabulary the chat lane uses.
//
// WHY THE GAP WAS REAL: all five acceptance tests in
// quota_mint_scope_test.go exercise the RACE lane. The Anthropic lane
// — the lane that actually carries the new class wiring — had ZERO
// coverage: a regression that classified the Token-Plan body as a
// rate-limit failure would have passed the whole suite.
//
// WHY THE CLASS MATTERS (the cooldown trap): a walled ACCOUNT that
// returns to the pool after the flat rate-limit policy (60s) is
// re-picked by the weighted pool while the account is still
// exhausted — an account recovers on a TOP-UP, not on a short timer.
// The quota class seeds 15m (credentiallb.DefaultQuotaCooldown)
// instead.
//
// THE OBSERVATION STYLE (per the dispatch): the assertions read the
// CREDENTIAL-LAYER STATE (the engine's cooldown rows) and the
// FAILOVER (the upstream call keys) — NOT the HTTP status alone. A
// 429 on the wire proves nothing about the class: both bodies answer
// 429. What distinguishes them is the DURATION of the cooldown the
// engine seeded.
// ─────────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelcache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/skipcache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

const (
	// anthropicQuotaModelID is the internal model the harness seeds.
	anthropicQuotaModelID = "m-anth-quota"
	// anthropicQuotaCredA / anthropicQuotaCredB are the model's TWO
	// credentials — two are REQUIRED: the Anthropic credential
	// failover hook is structurally guarded by
	// len(modelConfig.Credentials) > 1.
	anthropicQuotaCredA = "cred-anth-quota-A"
	anthropicQuotaCredB = "cred-anth-quota-B"
	// anthropicQuotaWireModel is the UPSTREAM model name the harness
	// serves — the key the upstream-layer demotion is written under.
	anthropicQuotaWireModel = "claude-anth-quota"
)

// anthropicQuotaTokenPlanBody is the OBSERVED live Token-Plan body
// (z.ai, code 2067) exactly as it arrives on the wire. The class
// lives in the MESSAGE — the numeric code cannot survive into a
// string-typed field, which is why the chat lane classifies on the
// message vocabulary.
const anthropicQuotaTokenPlanBody = `{"type":"error","error":{"type":"api_error","message":"The Token Plan usage limit has been reached. To avoid interruption, please upgrade your plan or buy credits and turn on auto consumption. (2067)"}}`

// anthropicQuotaPlainRateLimitBody is a PLAIN per-minute throttle: NO
// quota vocabulary anywhere — no "token plan", no credit / balance /
// billing family, no quota type or code. It must classify as the
// RATE-LIMIT class.
const anthropicQuotaPlainRateLimitBody = `{"type":"error","error":{"type":"rate_limit_error","message":"Too many requests"}}`

// anthropicQuotaEnv records the API key of every upstream call, in
// order — the observation of the credential failover.
//
// The recorder is the SHARED upstreamCallRecorder (upstream_call_recorder_test.go):
// the race-lane harness observes the same thing with the same code.
type anthropicQuotaEnv struct {
	upstreamCallRecorder
}

// newAnthropicQuotaEnv builds the Anthropic-lane harness: an internal
// model with TWO anthropic-provider credentials pointed at a stub
// upstream that answers EVERY call with (status, body), plus the
// credentialLB engine whose state the assertions read.
//
// The engine's default cooldown is 60s (the rate-limit class policy)
// and its quota cooldown is the package default (15m) — the two
// windows the assertions distinguish.
func newAnthropicQuotaEnv(t *testing.T, status int, body string) (*Handler, *anthropicQuotaEnv, *credentiallb.Engine, *modelcache.Cache, *skipcache.Cache) {
	t.Helper()

	env := &anthropicQuotaEnv{}
	stubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The passthrough lane authenticates with the x-api-key header
		// (the passthrough branch's request builder).
		env.record(r.Header.Get("x-api-key"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(stubServer.Close)

	mock := &mockModelsConfig{}
	for _, id := range []string{anthropicQuotaCredA, anthropicQuotaCredB} {
		if err := mock.AddCredential(models.CredentialConfig{
			ID: id, Provider: "anthropic", APIKey: "key-" + id, BaseURL: stubServer.URL,
		}); err != nil {
			t.Fatalf("AddCredential(%s): %v", id, err)
		}
	}
	refs := models.TestRefs(anthropicQuotaCredA, anthropicQuotaCredB)
	if err := mock.AddModel(models.ModelConfig{
		ID: anthropicQuotaModelID, Name: anthropicQuotaModelID, Enabled: true, Internal: true,
		InternalModel: "claude-anth-quota", Credentials: refs,
	}); err != nil {
		t.Fatalf("AddModel(%s): %v", anthropicQuotaModelID, err)
	}

	engine := credentiallb.NewEngine(time.Hour, time.Hour, 42, 60*time.Second)
	t.Cleanup(engine.Stop)
	engine.RebindFromStore(anthropicQuotaModelID, refs)

	resolver := &engineBackedResolver{mockModelsConfig: mock, engine: engine}

	// HERMETIC CONFIG DIR (review quality pass): config.NewManager
	// resolves os.UserConfigDir() — the operator's REAL config dir — and
	// Load() CREATES (and, with APPLY_ENV_OVERRIDES=1, REWRITES) the
	// config.json there. The harness must not touch the operator's
	// file: point the manager at a per-test temp dir.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	modelCache := modelcache.New()
	upstream := skipcache.New()
	cfg := &Config{
		ConfigMgr:         mgr,
		ModelsConfig:      resolver,
		UpstreamSkipCache: upstream,
		ModelSkipCache:    modelCache,
	}
	bus := events.NewBus()
	h := NewHandler(cfg, bus, store.NewRequestStore(100), nil, nil, nil, engine, nil)
	return h, env, engine, modelCache, upstream
}

// runAnthropicQuotaEnv drives ONE Anthropic /v1/messages request
// against the harness and returns the upstream call keys.
func runAnthropicQuotaEnv(t *testing.T, h *Handler, env *anthropicQuotaEnv) []string {
	t.Helper()

	rec := httptest.NewRecorder()
	req := makeAnthropicRequestNoAuth(t, map[string]interface{}{
		"model":      anthropicQuotaModelID,
		"max_tokens": 16,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.HandleAnthropicMessages(rec, req)

	// Sanity only: the request must have FAILED (the status alone
	// proves nothing about the class — the cooldown window below is
	// the assertion that carries the meaning).
	if rec.Code < 400 {
		t.Fatalf("upstream failure status = %d, want a failed request (body=%s)", rec.Code, rec.Body.String())
	}
	return env.calledKeys()
}

// benchedCredential returns the credential id of the FIRST upstream
// call — the credential that failed and must be benched at the
// credential layer.
func benchedCredential(t *testing.T, calls []string) string {
	t.Helper()
	if len(calls) == 0 {
		t.Fatal("no upstream call was recorded")
	}
	credID := strings.TrimPrefix(calls[0], "key-")
	if credID == calls[0] {
		t.Fatalf("upstream call %q does not carry the harness API-key prefix", calls[0])
	}
	return credID
}

// TestAnthropicQuotaClass_TokenPlanBodySeedsQuotaCooldown is case (i):
// an Anthropic-lane 429 carrying the OBSERVED Token-Plan body must
// seed the QUOTA-class cooldown (≈15m) at the credential layer.
//
// Before the branch's wiring the same body was classified rate-limit
// and benched the exhausted ACCOUNT for the flat 60s — the cooldown
// trap: the account is still exhausted, but the weighted pool picks
// it again a minute later.
func TestAnthropicQuotaClass_TokenPlanBodySeedsQuotaCooldown(t *testing.T) {
	h, env, engine, modelCache, upstream := newAnthropicQuotaEnv(t, http.StatusTooManyRequests, anthropicQuotaTokenPlanBody)

	calls := runAnthropicQuotaEnv(t, h, env)

	// The credential failover fired: two upstream calls, TWO DISTINCT
	// credentials (the failed one was reseeded with the sibling).
	if len(calls) != 2 {
		t.Fatalf("upstream calls = %v, want 2 (the Anthropic credential failover must reselect the sibling)", calls)
	}
	if calls[0] == calls[1] {
		t.Fatalf("upstream calls = %v, want two DISTINCT credentials (the failover did not reselect)", calls)
	}

	failed := benchedCredential(t, calls)
	until, ok := engine.CooldownUntil(anthropicQuotaModelID, failed)
	if !ok {
		t.Fatalf("the failed credential %s has NO cooldown row — the quota body must mint at the credential layer", failed)
	}
	d := time.Until(until)
	// The quota class: ~15m. Assert the CLASS WINDOW (>10m), not a
	// loose "a row exists".
	if d < credentiallb.DefaultQuotaCooldown-time.Minute {
		t.Fatalf("credential cooldown = %v, want the QUOTA class (~%v) — a Token-Plan body is account exhaustion, not a per-minute throttle", d, credentiallb.DefaultQuotaCooldown)
	}
	if d > credentiallb.DefaultQuotaCooldown+5*time.Minute {
		t.Fatalf("credential cooldown = %v, want the QUOTA class (~%v)", d, credentiallb.DefaultQuotaCooldown)
	}

	// MINT-SCOPE (the branch's other half): an ACCOUNT-scoped body
	// must NOT mint a model-layer mark — the model's sibling
	// credentials have to stay reachable.
	if modelCache.Marked(anthropicQuotaModelID) {
		t.Fatal("the model layer marked the model — an account-scoped body must never mint a model mark")
	}

	// The UPSTREAM layer's demotion of the walled account is WRITTEN —
	// it was previously written but never asserted (review S-5).
	if !upstream.Marked(skipcache.ScopeCredential, failed, anthropicQuotaWireModel) {
		t.Fatalf("the upstream layer did not demote the walled credential %s — the account body must mint at the upstream layer", failed)
	}
}

// TestAnthropicQuotaClass_PlainRateLimitSeedsShortCooldown is case (ii):
// a PLAIN 429 with NO quota vocabulary must seed the RATE-LIMIT class
// cooldown (≈60s) — NOT the quota window.
//
// This is the discriminating half of the pair: the same status code,
// a different class, a different duration. A regression that routed
// EVERY Anthropic 429 into the quota class would over-bench healthy
// throttled credentials for 15 minutes.
func TestAnthropicQuotaClass_PlainRateLimitSeedsShortCooldown(t *testing.T) {
	h, env, engine, _, _ := newAnthropicQuotaEnv(t, http.StatusTooManyRequests, anthropicQuotaPlainRateLimitBody)

	calls := runAnthropicQuotaEnv(t, h, env)

	if len(calls) != 2 {
		t.Fatalf("upstream calls = %v, want 2 (the Anthropic credential failover must reselect the sibling)", calls)
	}
	if calls[0] == calls[1] {
		t.Fatalf("upstream calls = %v, want two DISTINCT credentials (the failover did not reselect)", calls)
	}

	failed := benchedCredential(t, calls)
	until, ok := engine.CooldownUntil(anthropicQuotaModelID, failed)
	if !ok {
		t.Fatalf("the failed credential %s has NO cooldown row — a limit-class failure must mint at the credential layer", failed)
	}
	d := time.Until(until)
	// The rate-limit class: ~60s (the engine's default cooldown; no
	// Retry-After header is sent by the stub). Assert the CLASS
	// WINDOW (<5m), not a loose "a row exists".
	if d > 5*time.Minute {
		t.Fatalf("credential cooldown = %v, want the RATE-LIMIT class (~%v) — a plain throttle is not account exhaustion", d, credentiallb.DefaultCooldown)
	}
	if d < credentiallb.DefaultCooldown/2 {
		t.Fatalf("credential cooldown = %v, want the RATE-LIMIT class (~%v)", d, credentiallb.DefaultCooldown)
	}
}

// TestAnthropicQuotaClass_HashBoundMemberAutoSwitch is the S-11 pin:
// the Anthropic lane's HASH-BOUND failover must move the conversation
// key's credential to the sibling account WITHOUT losing the member
// binding (the model), and the observation must be READ-ONLY.
//
// The same discipline as the race lane's pin (review B-1): the binding
// is read with credentiallb.Engine.BoundCredentialForTest — NOT with
// GetOrSelect, which is get-or-SELECT (a cooling binding is dropped and
// a fresh pick is written), so a GetOrSelect-based assertion would
// write the state it then asserts.
//
// TWO-POINT OBSERVATION: BEFORE the request the key is bound to the
// account that will fail; AFTER the request the SAME key is bound to
// the healthy sibling.
//
// The rebind's "was this a first binding" flag is deliberately NOT
// pinned: the standing ruling keeps "one rebind per active
// conversation per 30m" as intended behavior.
func TestAnthropicQuotaClass_HashBoundMemberAutoSwitch(t *testing.T) {
	// The conversation key the lane derives for the harness request:
	// sha256(modelID | tokenID | leadingUserRun) — the token ID is ""
	// (the harness runs with auth DISABLED) and the leading user run is
	// the canonical JSON of the single user message.
	convKey := credentiallb.ComputeConversationKey(
		anthropicQuotaModelID,
		"",
		credentiallb.ExtractLeadingUserRun([]interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		}),
	)
	if convKey == "" {
		t.Fatal("the harness conversation key is empty — the leading-user-run extraction failed")
	}

	h, env, engine, _, _ := newAnthropicQuotaEnv(t, http.StatusTooManyRequests, anthropicQuotaTokenPlanBody)
	// Pin the binding to the account that will fail (the stub fails
	// every call, so the first credential is the walled one).
	engine.InjectPreconditionStateForTest(anthropicQuotaModelID, convKey, anthropicQuotaCredA)

	// BEFORE — read-only.
	before, bound := engine.BoundCredentialForTest(anthropicQuotaModelID, convKey)
	if !bound {
		t.Fatal("no hash binding before the request")
	}
	if before != anthropicQuotaCredA {
		t.Fatalf("hash-bound credential BEFORE = %q, want %s (the walled account)", before, anthropicQuotaCredA)
	}

	calls := runAnthropicQuotaEnv(t, h, env)
	if len(calls) != 2 {
		t.Fatalf("upstream calls = %v, want 2 (the failover must reselect the sibling)", calls)
	}
	if calls[0] == calls[1] {
		t.Fatalf("upstream calls = %v, want two DISTINCT credentials", calls)
	}

	// AFTER — the SAME key is bound to the healthy sibling: the
	// credential moved, the member (the model) did not.
	after, bound := engine.BoundCredentialForTest(anthropicQuotaModelID, convKey)
	if !bound {
		t.Fatal("no hash binding after the request")
	}
	if after != anthropicQuotaCredB {
		t.Fatalf("hash-bound credential AFTER = %q, want %s — the binding must follow the auto-switch", after, anthropicQuotaCredB)
	}
	if after == before {
		t.Fatal("the binding did not move — the hash-bound credential did not auto-switch")
	}
}

// TestAnthropicQuotaClassification_NegativeAndBoundaryCases pins the
// Anthropic lane's quota classifier on the shapes that must NOT (and
// the shapes that MUST) classify — the review's negative probe set.
//
// It is a UNIT test of the classifier (the arc carries the same fields
// the lane reads), so a regression is visible without driving the
// whole handler.
func TestAnthropicQuotaClassification_NegativeAndBoundaryCases(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
		why    string
	}{
		{"empty body", 400, "", false, "no body — nothing to classify"},
		{"non-JSON body", 400, "not json at all", false, "undecodable and no quota vocabulary"},
		{"JSON array body", 400, `[{"message":"Token Plan usage limit"}]`, false, "an array is not an error object"},
		{"unknown fields only", 400, `{"foo":"bar","detail":"nothing here"}`, false, "no quota field"},
		{"quota phrase in an unrelated field", 429, `{"status":"ok","detail":"insufficient credit to read the docs"}`, false, "the phrase is not in a quota field — the over-bench case"},
		{"client 400 + token-plan message", 400, anthropicQuotaTokenPlanBody, false, "a client-attributable 4xx must not rotate or bench a credential (S-3 — the base behavior)"},
		{"402 with an empty body", 402, "", true, "the status alone is the quota signal"},
		{"type without a message", 400, `{"type":"insufficient_quota"}`, true, "the structured signal (pre-existing)"},
		{"error.code without a message", 400, `{"error":{"code":"quota_exceeded"}}`, true, "the structured signal (pre-existing)"},
		{"429 + token-plan message", 429, anthropicQuotaTokenPlanBody, true, "the message signal on a credential-attributable status"},
		{"JSON-escaped marker", 429, `{"error":{"message":"token plan usage limit reached"}}`, true, "the DECODED message is matched, not the raw text"},
		{"9router nested resource wall", 429, `{"code":429,"message":"You have exhausted your capacity on this model. Your quota will reset after 4h39m9s.","status":"RESOURCE_EXHAUSTED"}`, true, "the observed account-quota body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arc := &anthropicRequestContext{lastStatusCode: tc.status, lastError: []byte(tc.body)}
			if got := isAnthropicQuotaFailure(arc); got != tc.want {
				t.Fatalf("isAnthropicQuotaFailure(status=%d, body=%q) = %v, want %v (%s)", tc.status, tc.body, got, tc.want, tc.why)
			}
		})
	}
}
