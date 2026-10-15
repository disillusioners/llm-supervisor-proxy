//go:build live_decision

// Package decision live e2e (Decision Models commission /
// plan §9.4 / P4.2).
//
// This file is build-tagged `live_decision` and runs ONLY when
// the tag is set (sibling of pkg/imggen's `live_imggen`). It is
// EXCLUDED from `go build ./...` and the default `go test ./...`
// — prove it compiles separately with:
//
//	go vet -tags live_decision ./pkg/decision/
//	go test -tags live_decision -count=1 -run '^$' ./pkg/decision/
//	gofmt -l pkg/decision/live_e2e_test.go   (must print nothing)
//
// Each test skips — with a message naming the missing key —
// when its API key is absent. The exported environment variable
// is the PRIMARY gate (plan §9.4: "silently skips without env
// vars"): an exported OPENAI_API_KEY / TYPESAFE_API_KEY is
// honored WITHOUT any file. The fallback source is the
// out-of-tree file
//
//	/home/nea/Code/opensource-projects/llm-supervisor-proxy/.env-decision
//
// (mirroring imggen's .env-minimax), sourced one-shot at run
// time — NEVER copied into the worktree, NEVER inlined into the
// test source, and masked in every log line / assertion
// message.
//
// HARD BUDGET (§9.4): ≤3 upstream calls per provider, ≤6 total.
// Every real upstream POST goes through liveCallLedger.spend;
// a breach is a t.Fatalf — the binary stops rather than
// burning vendor budget.
//
// PROBE-FIRST protocol (OpenAI arm): the first (and only)
// budgeted call is a minimal 1-question request. If the
// upstream rejects the key (401/403 — e.g. an infra-routed key
// that is invalid at api.openai.com), the error arrives as a
// promoted 502 with the upstream error envelope preserved
// verbatim; the test records the observed shape (the A7
// verification note) and aborts — no further calls.
//
// Build:  go test -tags live_decision -run TestLiveE2E ./pkg/decision/...
package decision

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/usage"
)

// decisionEnvFilePath is the absolute path the test sources
// provider keys from when the env var is unset. The worktree
// MUST NOT contain .env-decision (mirrors the imggen
// .env-minimax HR-4 rule).
const decisionEnvFilePath = "/home/nea/Code/opensource-projects/llm-supervisor-proxy/.env-decision"

// loadDecisionKey returns the API key for envVarName. The
// exported env var wins (primary gate per §9.4); the
// out-of-tree .env-decision file is the fallback. The key is
// NEVER copied into the worktree or the test source.
func loadDecisionKey(t *testing.T, envVarName string) (string, bool) {
	t.Helper()
	if v := strings.TrimSpace(os.Getenv(envVarName)); v != "" {
		return v, true
	}
	if _, err := os.Stat(decisionEnvFilePath); err != nil {
		return "", false
	}
	cmd := exec.Command("bash", "-c",
		fmt.Sprintf("source %q && echo -n \"$%s\"", decisionEnvFilePath, envVarName))
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	key := strings.TrimSpace(string(out))
	if key == "" {
		return "", false
	}
	return key, true
}

// maskDecisionKey returns the key with all but the last 4
// chars replaced with asterisks — the only key-bearing form
// allowed in any log line / assertion message.
func maskDecisionKey(key string) string {
	if len(key) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(key)-4) + key[len(key)-4:]
}

// liveCallLedger enforces the §9.4 hard budget: ≤3 upstream
// calls per provider, ≤6 total across both providers. spend is
// called exactly once per real upstream POST (the handler is
// single-attempt — one HandleDecision = one upstream call).
type liveCallLedger struct {
	mu          sync.Mutex
	perProvider map[string]int
	maxProvider int
	total       int
	maxTotal    int
}

func newLiveCallLedger(maxPerProvider, maxTotal int) *liveCallLedger {
	return &liveCallLedger{
		perProvider: map[string]int{},
		maxProvider: maxPerProvider,
		maxTotal:    maxTotal,
	}
}

func (l *liveCallLedger) spend(t *testing.T, provider string) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.perProvider[provider]++
	l.total++
	if l.perProvider[provider] > l.maxProvider {
		t.Fatalf("live e2e budget breach: provider %q has spent %d upstream calls (hard max %d/provider, §9.4)",
			provider, l.perProvider[provider], l.maxProvider)
	}
	if l.total > l.maxTotal {
		t.Fatalf("live e2e budget breach: %d total upstream calls (hard max %d, §9.4)",
			l.total, l.maxTotal)
	}
}

// preservedUpstreamError is the parsed shape of an upstream
// OpenAI error envelope preserved verbatim through a promoted
// 502 (verified P2 behavior: PromoteToError on all error
// envelopes, body preserved).
type preservedUpstreamError struct {
	code       string
	errType    string
	message    string
	authShaped bool
}

// parsePreservedUpstreamError extracts an upstream error
// envelope from a promoted-502 body; nil when the body is not
// that shape.
func parsePreservedUpstreamError(_ *testing.T, body []byte) *preservedUpstreamError {
	var env struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil || env.Error.Message == "" {
		return nil
	}
	lower := strings.ToLower(env.Error.Code + " " + env.Error.Type + " " + env.Error.Message)
	authShaped := strings.Contains(lower, "invalid_api_key") ||
		strings.Contains(lower, "api key") ||
		strings.Contains(lower, "authentication") ||
		strings.Contains(lower, "unauthorized")
	return &preservedUpstreamError{
		code:       env.Error.Code,
		errType:    env.Error.Type,
		message:    env.Error.Message,
		authShaped: authShaped,
	}
}

// newLiveDecisionHandler wires a decision handler at the REAL
// upstream endpoints (credential BaseURL empty → adapter
// defaults) with an in-memory SQLite usage counter so
// success-path metering is verifiable. tokenStore is nil →
// auth is disabled (house test posture; the client never sees
// the credential bearer — the handler injects it upstream).
func newLiveDecisionHandler(t *testing.T, modelID, provider, internalModel, apiKey string) (*Handler, *usage.Counter) {
	t.Helper()
	db, cleanup := newDecisionUsageTestDB(t)
	t.Cleanup(cleanup)
	uc := usage.NewCounter(db, database.SQLite)
	h := NewHandler(stubConfig{timeout: 60 * time.Second}, events.NewBus(), nil, uc)
	h.SetModelResolver(func(id string) (*models.ModelConfig, bool) {
		if id != modelID {
			return nil, false
		}
		return &models.ModelConfig{
			ID:            modelID,
			Kind:          models.KindDecision,
			Enabled:       true,
			Internal:      true,
			InternalModel: internalModel,
			Credentials: []models.CredentialRef{
				{CredentialID: "live-cred", Weight: 1, Position: 0},
			},
			ExcludeFromUltimateSwitching: true,
		}, true
	})
	h.SetCredentialResolver(func(id string) (models.ResolvedCredential, bool) {
		if id != modelID {
			return models.ResolvedCredential{}, false
		}
		return models.ResolvedCredential{
			Provider:      provider,
			APIKey:        apiKey,
			BaseURL:       "", // adapter default = the real vendor endpoint
			InternalModel: internalModel,
			CredentialID:  "live-cred",
		}, true
	})
	return h, uc
}

// postDecision POSTs a raw body through the handler in-process.
func postDecision(h *Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleDecision(rec, req)
	return rec
}

// waitForModelUsageRow polls the usage counter for a
// model_hourly_usage row for modelID (the meter write is a
// detached-context write; 2s grace mirrors handler_test.go).
func waitForModelUsageRow(t *testing.T, uc *usage.Counter, modelID string) bool {
	t.Helper()
	hourBucket := time.Now().UTC().Format("2006-01-02T15")
	for i := 0; i < 40; i++ {
		rows, err := uc.GetModelUsage(context.Background(), hourBucket, hourBucket)
		if err == nil {
			for _, r := range rows {
				if r.ModelID == modelID && r.RequestCount > 0 {
					return true
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// TestLiveE2E_OpenAI_Decisions_BasicRoundTrip (§9.4) — one
// minimal live call through the proxy handler to the REAL
// OpenAI Decision endpoint:
//
//   - URL builder proof (no call spent): openai adapter with an
//     empty credential BaseURL must build
//     https://api.openai.com/v1/decisions and rewrite the
//     model token to the internal model (gpt-6-luna).
//   - POST → relay; response shape {model, answers[], usage}
//     with NO id/object/created (A2).
//   - Usage metered on success (success-only meter, Q5).
//
// PROBE-FIRST: on an upstream auth rejection (401/403, arriving
// as a promoted 502 with the upstream error envelope preserved)
// the test records the observed shape (A7 note) and skips —
// live verify pending a proper key.
func TestLiveE2E_OpenAI_Decisions_BasicRoundTrip(t *testing.T) {
	apiKey, ok := loadDecisionKey(t, "OPENAI_API_KEY")
	if !ok {
		t.Skipf("OPENAI_API_KEY unavailable (env unset and no key at %s) — live e2e skipped (§9.4 budget reserved)", decisionEnvFilePath)
	}
	masked := maskDecisionKey(apiKey)
	ledger := newLiveCallLedger(3, 6)

	const (
		modelID       = "decision-openai-live"
		internalModel = "gpt-6-luna"
	)

	// URL-builder proof against the REAL endpoint — zero
	// upstream calls spent.
	buildReq := &DecisionRequest{Model: modelID}
	rawBody := []byte(`{"model":"` + modelID + `","input":"ping","questions":[{"type":"predicate","name":"is_x","instructions":"is it true?"}]}`)
	upstreamURL, rewritten, err := (&openaiDecisionProvider{}).BuildUpstream(
		models.ResolvedCredential{Provider: OpenAIDecisionProviderName, APIKey: apiKey, BaseURL: "", InternalModel: internalModel},
		buildReq, rawBody)
	if err != nil {
		t.Fatalf("openai BuildUpstream: %v", err)
	}
	if upstreamURL != "https://api.openai.com/v1/decisions" {
		t.Fatalf("openai URL builder = %q, want https://api.openai.com/v1/decisions", upstreamURL)
	}
	if !strings.Contains(string(rewritten), `"`+internalModel+`"`) {
		t.Fatalf("openai BuildUpstream body missing rewritten model %q: %s", internalModel, rewritten)
	}

	h, uc := newLiveDecisionHandler(t, modelID, OpenAIDecisionProviderName, internalModel, apiKey)
	t.Logf("[live e2e] openai: spending 1/%d budgeted calls to POST %s (key=%s)",
		ledger.maxProvider, upstreamURL, masked)

	// PROBE-FIRST minimal request: 1 predicate question, short
	// input, no safety_identifier (A1-minimal).
	body := `{"model":"` + modelID + `","input":"Customer wrote: I was charged twice for order #4411 and want a refund.",` +
		`"questions":[{"type":"predicate","name":"is_refund_request","instructions":"Is the customer asking for a refund?"}]}`
	ledger.spend(t, "openai")
	rec := postDecision(h, body)

	if rec.Code != http.StatusOK {
		up := parsePreservedUpstreamError(t, rec.Body.Bytes())
		if up != nil && up.authShaped {
			// A7 verification note: record the observed error
			// shape and abort (mission probe-first rule).
			t.Skipf("OPENAI_API_KEY present but rejected by api.openai.com — live verify pending a proper key. "+
				"Observed error shape via promoted-502 (body preserved verbatim): code=%q type=%q message=%q (A7)",
				up.code, up.errType, up.message)
		}
		t.Fatalf("proxy returned %d: %s", rec.Code, rec.Body.String())
	}

	// A2: response shape is {model, answers[], usage} — the
	// proxy must NOT synthesize id/object/created.
	var raw map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("response body unparseable: %v; body=%s", err, rec.Body.String())
	}
	for _, banned := range []string{"id", "object", "created"} {
		if _, present := raw[banned]; present {
			t.Errorf("response carries root-level %q — A2 violated (proxy must not synthesize it): %s", banned, rec.Body.String())
		}
	}
	var typed DecisionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &typed); err != nil {
		t.Fatalf("response not a typed DecisionResponse: %v; body=%s", err, rec.Body.String())
	}
	if typed.Model == "" {
		t.Fatalf("response model empty (A2): %s", rec.Body.String())
	}
	if typed.Model != internalModel {
		t.Errorf("response model = %q, want %q (upstream echoed a different versioned ID — record as an A-facts finding)", typed.Model, internalModel)
	}
	if len(typed.Answers) != 1 {
		t.Fatalf("answers = %d, want 1: %s", len(typed.Answers), rec.Body.String())
	}
	ans := typed.Answers[0]
	if ans.Type != QuestionTypePredicate || ans.Name != "is_refund_request" {
		t.Errorf("answer[0] = {type=%s name=%q}, want {predicate is_refund_request}", ans.Type, ans.Name)
	}
	if ans.Probability == nil {
		t.Errorf("predicate answer missing probability: %+v", ans)
	}
	if typed.Usage.InputTokens == 0 && typed.Usage.OutputTokens == 0 {
		t.Logf("[live e2e] upstream usage 0/0 — metering assert degraded (success-only meter skips zero-token usage by policy)")
	} else if !waitForModelUsageRow(t, uc, modelID) {
		t.Errorf("usage not metered: no model_hourly_usage row for %q within 2s", modelID)
	} else {
		t.Logf("[live e2e] usage metered (input=%d output=%d total_tokens=%d)",
			typed.Usage.InputTokens, typed.Usage.OutputTokens, typed.Usage.TotalTokens)
	}
	t.Logf("[live e2e] openai round trip OK: model=%q answers=%d probability=%v (A1 schema accepted, A2 shape confirmed)",
		typed.Model, len(typed.Answers), ans.Probability)
}

// TestLiveE2E_TypeSafe_SystemOne_RoundTrip (§9.4) — one live
// call through the proxy handler to the REAL TypeSafe System
// One endpoint:
//
//   - URL builder proof (no call spent): typesafe adapter with
//     an empty credential BaseURL must build
//     https://api.typesafe.ai/v1/systemone (A10); the handler
//     injects `Authorization: Bearer <key>` (A9).
//   - POST input:string + 3 questions (predicate + choice +
//     score) → System One `{model, answers{}, usage}` is
//     translated back into the ordered OpenAI answers[] (A13 +
//     §4): answers[i].name == questions[i].name, per-type
//     fields populated (predicate→probability, choice→choice+
//     probabilities, score→score+probabilities).
func TestLiveE2E_TypeSafe_SystemOne_RoundTrip(t *testing.T) {
	apiKey, ok := loadDecisionKey(t, "TYPESAFE_API_KEY")
	if !ok {
		t.Skipf("TYPESAFE_API_KEY unavailable (env unset and no key at %s) — live verify pending keys (§9.4)", decisionEnvFilePath)
	}
	masked := maskDecisionKey(apiKey)
	ledger := newLiveCallLedger(3, 6)

	const (
		modelID       = "decision-typesafe-live"
		internalModel = "jev-latest"
	)

	// URL-builder proof against the REAL endpoint — zero
	// upstream calls spent.
	buildReq := &DecisionRequest{
		Model: modelID,
		Input: json.RawMessage(`"state text"`),
		Questions: []Question{
			{Type: QuestionTypePredicate, Name: "is_positive", Instructions: "Is the statement positive?"},
		},
	}
	upstreamURL, _, err := (&typesafeProvider{}).BuildUpstream(
		models.ResolvedCredential{Provider: TypesafeProviderName, APIKey: apiKey, BaseURL: "", InternalModel: internalModel},
		buildReq, nil)
	if err != nil {
		t.Fatalf("typesafe BuildUpstream: %v", err)
	}
	if upstreamURL != "https://api.typesafe.ai/v1/systemone" {
		t.Fatalf("typesafe URL builder = %q, want https://api.typesafe.ai/v1/systemone", upstreamURL)
	}

	h, _ := newLiveDecisionHandler(t, modelID, TypesafeProviderName, internalModel, apiKey)
	t.Logf("[live e2e] typesafe: spending 1/%d budgeted calls to POST %s (key=%s)",
		ledger.maxProvider, upstreamURL, masked)

	// input:string + 3 questions; names are REQUIRED (System
	// One keys answers{} on them) and unique.
	body := `{"model":"` + modelID + `","input":"The new onboarding flow is clear and fast.",` +
		`"questions":[` +
		`{"type":"predicate","name":"is_positive","instructions":"Is the statement positive?"},` +
		`{"type":"choice","name":"sentiment","instructions":"Which sentiment fits best?","choices":[{"value":"positive"},{"value":"neutral"},{"value":"negative"}]},` +
		`{"type":"score","name":"quality","instructions":"Rate the writing quality.","levels":[{"label":"low"},{"label":"medium"},{"label":"high"}]}` +
		`]}`
	ledger.spend(t, "typesafe")
	rec := postDecision(h, body)

	if rec.Code != http.StatusOK {
		t.Fatalf("proxy returned %d: %s", rec.Code, rec.Body.String())
	}

	var typed DecisionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &typed); err != nil {
		t.Fatalf("response not a typed DecisionResponse: %v; body=%s", err, rec.Body.String())
	}
	// A16: the response model reports the versioned ID that
	// actually answered (e.g. jev-1.13.0 for the jev-latest
	// alias) — assert non-empty, record the actual.
	if typed.Model == "" {
		t.Fatalf("response model empty (A13): %s", rec.Body.String())
	}
	t.Logf("[live e2e] upstream model id = %q (requested alias %q — A16)", typed.Model, internalModel)
	if len(typed.Answers) != 3 {
		t.Fatalf("answers = %d, want 3 (A13 + §4 ordering): %s", len(typed.Answers), rec.Body.String())
	}
	// Ordering per A13 + §4: answers[] preserves questions[]
	// order.
	wantOrder := []string{"is_positive", "sentiment", "quality"}
	for i, want := range wantOrder {
		if typed.Answers[i].Name != want {
			t.Errorf("answers[%d].name = %q, want %q (A13 ordering violated)", i, typed.Answers[i].Name, want)
		}
	}
	if a0 := typed.Answers[0]; a0.Type != QuestionTypePredicate || a0.Probability == nil {
		t.Errorf("answers[0] = {type=%s probability=%v}, want predicate with probability", a0.Type, a0.Probability)
	}
	if a1 := typed.Answers[1]; a1.Type != QuestionTypeChoice || a1.Choice == "" || len(a1.Probabilities) == 0 {
		t.Errorf("answers[1] = {type=%s choice=%q probabilities=%d}, want choice with value + distribution",
			a1.Type, a1.Choice, len(a1.Probabilities))
	}
	if a2 := typed.Answers[2]; a2.Type != QuestionTypeScore || a2.Score == nil {
		t.Errorf("answers[2] = {type=%s score=%v}, want score with level index (A24)", a2.Type, a2.Score)
	}
	t.Logf("[live e2e] typesafe round trip OK: model=%q answers=%d (A10 endpoint, A11 schema, A13 shape+ordering confirmed; A9 bearer exercised live)",
		typed.Model, len(typed.Answers))
}
