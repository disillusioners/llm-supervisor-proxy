package decision

// Coverage pass for the handler helpers + lifecycle error
// branches (Decision Models / §10 P4.1 — package coverage
// ≥ 85%): decisionTimeout fallbacks, extractAPIKey precedence,
// meterOnSuccess guard + error paths, resolver plumbing,
// header helpers, and the HandleDecision branches the P2
// suite never drove (body read error, invalid JSON, missing
// model, upstream request build failure, truncated upstream
// body). Same-package white-box, stdlib-only, house style.

import (
	"bytes"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/usage"
)

// ---------- decisionTimeout ----------

// TestDecisionTimeout_Fallbacks — nil config manager and a
// non-positive knob both fall back to the 30s default; a
// positive knob is honored verbatim.
func TestDecisionTimeout_Fallbacks(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil)
	if got := h.decisionTimeout(); got != 30*time.Second {
		t.Errorf("nil configMgr timeout = %v, want 30s default", got)
	}
	h = NewHandler(stubConfig{timeout: 0}, nil, nil, nil)
	if got := h.decisionTimeout(); got != 30*time.Second {
		t.Errorf("zero knob timeout = %v, want 30s default", got)
	}
	h = NewHandler(stubConfig{timeout: 2 * time.Second}, nil, nil, nil)
	if got := h.decisionTimeout(); got != 2*time.Second {
		t.Errorf("configured timeout = %v, want 2s", got)
	}
}

// ---------- extractAPIKey ----------

// TestExtractAPIKey_HeaderPrecedence — Bearer Authorization
// wins over X-API-Key; a bare X-API-Key is honored; no header
// → empty.
func TestExtractAPIKey_HeaderPrecedence(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/decisions", nil)
	r.Header.Set("Authorization", "Bearer sk-one")
	r.Header.Set("X-API-Key", "sk-two")
	if got := extractAPIKey(r); got != "sk-one" {
		t.Errorf("Bearer precedence: got %q, want sk-one", got)
	}

	r = httptest.NewRequest(http.MethodPost, "/v1/decisions", nil)
	r.Header.Set("X-API-Key", "sk-two")
	if got := extractAPIKey(r); got != "sk-two" {
		t.Errorf("X-API-Key: got %q, want sk-two", got)
	}

	r = httptest.NewRequest(http.MethodPost, "/v1/decisions", nil)
	if got := extractAPIKey(r); got != "" {
		t.Errorf("no headers: got %q, want empty", got)
	}
}

// ---------- meterOnSuccess guards + error paths ----------

// captureDecisionLogs redirects the std log while fn runs and
// returns what was written.
func captureDecisionLogs(t *testing.T, fn func()) string {
	t.Helper()
	old := log.Writer()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })
	fn()
	return buf.String()
}

// TestMeterOnSuccess_NilUsageCounter — a nil usage counter
// degrades to a silent skip (no panic).
func TestMeterOnSuccess_NilUsageCounter(t *testing.T) {
	h := NewHandler(stubConfig{}, nil, nil, nil)
	provider := NewTypesafeProvider()
	resp := &DecisionResponse{Usage: Usage{InputTokens: 5, OutputTokens: 3, TotalTokens: 8}}
	h.meterOnSuccess(&auth.AuthToken{ID: "tok-1"}, "m", provider, resp)
}

// TestMeterOnSuccess_NilResponse — a nil typed response is a
// silent skip (nothing to extract usage from).
func TestMeterOnSuccess_NilResponse(t *testing.T) {
	db, _ := newDecisionUsageTestDB(t)
	h := NewHandler(stubConfig{}, nil, nil, usage.NewCounter(db, database.SQLite))
	h.meterOnSuccess(&auth.AuthToken{ID: "tok-1"}, "m", NewTypesafeProvider(), nil)
}

// TestMeterOnSuccess_ZeroTokenUsage_Skipped — a successful
// relay with zero tokens writes nothing and logs the skip.
func TestMeterOnSuccess_ZeroTokenUsage_Skipped(t *testing.T) {
	db, _ := newDecisionUsageTestDB(t)
	h := NewHandler(stubConfig{}, nil, nil, usage.NewCounter(db, database.SQLite))
	logs := captureDecisionLogs(t, func() {
		h.meterOnSuccess(&auth.AuthToken{ID: "tok-1"}, "m", NewTypesafeProvider(), &DecisionResponse{})
	})
	if !strings.Contains(logs, "skipping meter for zero-token usage") {
		t.Errorf("logs = %q, want zero-token skip line", logs)
	}
}

// TestMeterOnSuccess_IncrementErrors_AreLogged — a broken
// meter target (closed DB) logs both writer failures instead
// of panicking or silently dropping the failure.
func TestMeterOnSuccess_IncrementErrors_AreLogged(t *testing.T) {
	db, _ := newDecisionUsageTestDB(t)
	_ = db.Close() // the counter now fails on every write
	h := NewHandler(stubConfig{}, nil, nil, usage.NewCounter(db, database.SQLite))
	logs := captureDecisionLogs(t, func() {
		h.meterOnSuccess(&auth.AuthToken{ID: "tok-1"}, "m",
			NewTypesafeProvider(),
			&DecisionResponse{Usage: Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}})
	})
	if !strings.Contains(logs, "[decision] Increment failed") {
		t.Errorf("logs = %q, want Increment failure line", logs)
	}
	if !strings.Contains(logs, "[decision] IncrementModelUsage failed") {
		t.Errorf("logs = %q, want IncrementModelUsage failure line", logs)
	}
}

// ---------- resolver plumbing ----------

// TestHandlerSetResolvers_NilReceiver — the setters are
// nil-receiver safe (no panic on a zero-value handler).
func TestHandlerSetResolvers_NilReceiver(t *testing.T) {
	var h *Handler
	h.SetModelResolver(func(string) (*models.ModelConfig, bool) { return nil, false })
	h.SetCredentialResolver(func(string) (models.ResolvedCredential, bool) { return models.ResolvedCredential{}, false })
}

// TestSafeResolveCredential_NilResolver — no credential
// resolver installed ⇒ (zero, false), never a nil-deref.
func TestSafeResolveCredential_NilResolver(t *testing.T) {
	h := NewHandler(stubConfig{}, nil, nil, nil)
	cred, ok := h.safeResolveCredential("any-model")
	if ok || cred != (models.ResolvedCredential{}) {
		t.Errorf("safeResolveCredential = (%+v, %v), want (zero, false)", cred, ok)
	}
}

// TestNewResolversFromConfig_NilFunc — a nil backing func
// yields a resolver that reports not-found for every id.
func TestNewResolversFromConfig_NilFunc(t *testing.T) {
	if _, ok := NewModelResolverFromConfig(nil)("m"); ok {
		t.Errorf("nil model getter: ok = true, want false")
	}
	if _, ok := NewCredentialResolverFromConfig(nil)("m"); ok {
		t.Errorf("nil credential resolver: ok = true, want false")
	}
}

// ---------- header helpers ----------

// TestIsHopByHopHeader — the three stripped headers (case-
// insensitive) and a pass-through counter-example.
func TestIsHopByHopHeader(t *testing.T) {
	for _, h := range []string{"Host", "content-length", "TRANSFER-ENCODING"} {
		if !isHopByHopHeader(h) {
			t.Errorf("isHopByHopHeader(%q) = false, want true", h)
		}
	}
	if isHopByHopHeader("Accept") {
		t.Errorf("isHopByHopHeader(Accept) = true, want false")
	}
}

// ---------- HandleDecision uncovered lifecycle branches ----------

// failingBody is an io.Reader whose Read always errors (not
// with *http.MaxBytesError) — drives the generic read-error
// branch.
type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("synthetic read failure") }

// TestHandleDecision_ReadError_500 — a body that fails
// mid-read (not over-cap) maps to 500 + house envelope.
func TestHandleDecision_ReadError_500(t *testing.T) {
	env := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(1, 1)})
	r := httptest.NewRequest(http.MethodPost, "/v1/decisions", failingBody{})
	w := httptest.NewRecorder()
	env.handler.HandleDecision(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", w.Code, w.Body.String())
	}
	msg, _, _ := decodeOpenAIError(t, w.Body.Bytes())
	if msg != "failed to read request body" {
		t.Errorf("message = %q, want read-failure message", msg)
	}
}

// TestHandleDecision_InvalidJSON_400 — an unparseable body
// maps to 400 + house envelope.
func TestHandleDecision_InvalidJSON_400(t *testing.T) {
	env := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(1, 1)})
	rec := env.sendRequest(http.MethodPost, `{nope`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	msg, _, _ := decodeOpenAIError(t, rec.Body.Bytes())
	if msg != "invalid JSON body" {
		t.Errorf("message = %q, want invalid-JSON message", msg)
	}
}

// TestHandleDecision_MissingModel_400 — a parseable body
// without `model` maps to 400 + house envelope.
func TestHandleDecision_MissingModel_400(t *testing.T) {
	env := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(1, 1)})
	rec := env.sendRequest(http.MethodPost, `{"input":"x"}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	msg, _, _ := decodeOpenAIError(t, rec.Body.Bytes())
	if msg != "model field is required" {
		t.Errorf("message = %q, want missing-model message", msg)
	}
}

// TestHandleDecision_RequestBuildFailed_500 — an upstream
// base URL the net/http parser rejects (space in host)
// surfaces as 500 + "failed to build upstream HTTP request"
// (the request-build branch, upstream never contacted).
func TestHandleDecision_RequestBuildFailed_500(t *testing.T) {
	env := newEnvTypesafe(t, fakeScript{status: 200, body: systemOneSuccessBody(nil, 1, 1)})
	env.handler.SetCredentialResolver(func(string) (models.ResolvedCredential, bool) {
		return models.ResolvedCredential{
			Provider: "typesafe", APIKey: "k",
			BaseURL: "ht tp://bad", InternalModel: "jev-1.13.0", CredentialID: "c1",
		}, true
	})
	rec := env.sendRequest(http.MethodPost, standardRequestBody(env.modelID), nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	msg, _, _ := decodeOpenAIError(t, rec.Body.Bytes())
	if msg != "failed to build upstream HTTP request" {
		t.Errorf("message = %q, want request-build message", msg)
	}
}

// TestHandleDecision_UpstreamReadFailed_502 — an upstream that
// declares Content-Length 100 but writes 5 bytes makes the
// bounded body read fail → 502 + house envelope.
func TestHandleDecision_UpstreamReadFailed_502(t *testing.T) {
	trunc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
	}))
	t.Cleanup(trunc.Close)

	env := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(1, 1)})
	env.handler.SetCredentialResolver(func(string) (models.ResolvedCredential, bool) {
		return models.ResolvedCredential{
			Provider: "openai", APIKey: "k",
			BaseURL: trunc.URL, InternalModel: "gpt-6-luna", CredentialID: "c1",
		}, true
	})
	rec := env.sendRequest(http.MethodPost, standardRequestBody(env.modelID), nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	msg, _, _ := decodeOpenAIError(t, rec.Body.Bytes())
	if msg != "upstream read failed" {
		t.Errorf("message = %q, want upstream-read message", msg)
	}
}

// ---------- openai-decision adapter uncovered branches ----------

// TestOpenAIBuildUpstream_Guards — nil typed request and an
// empty cred.InternalModel both fail the build with typed
// errors (the handler maps them to
// decision_upstream_build_failed 502s).
func TestOpenAIBuildUpstream_Guards(t *testing.T) {
	p := NewOpenAIDecisionProvider()
	raw := []byte(`{"model":"m","input":"x","questions":[]}`)

	_, _, err := p.BuildUpstream(models.ResolvedCredential{InternalModel: "gpt-6-luna"}, nil, raw)
	if err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("nil req err = %v, want nil-request error", err)
	}
	_, _, err = p.BuildUpstream(models.ResolvedCredential{InternalModel: ""}, &DecisionRequest{Model: "m"}, raw)
	if err == nil || !strings.Contains(err.Error(), "no InternalModel") {
		t.Errorf("empty InternalModel err = %v, want InternalModel-required error", err)
	}
}

// TestOpenAIInspectResponse_UnrecognizedShape_502 — a body
// that is neither a typed DecisionResponse nor an error
// envelope falls through to the catch-all 502.
func TestOpenAIInspectResponse_UnrecognizedShape_502(t *testing.T) {
	p := NewOpenAIDecisionProvider()
	_, outcome, err := p.InspectResponse([]byte(`{}`), &DecisionRequest{Model: "x"})
	if err != nil || outcome == nil || outcome.RelayStatus != 502 {
		t.Fatalf("outcome=%+v err=%v, want 502", outcome, err)
	}
	if outcome.Reason != "openai-decision body is neither a typed DecisionResponse nor an error envelope" {
		t.Errorf("reason = %q, want catch-all reason", outcome.Reason)
	}
}

// TestOpenAIExtractUsage_NilResponse — nil typed response →
// zero Usage.
func TestOpenAIExtractUsage_NilResponse(t *testing.T) {
	if got := (NewOpenAIDecisionProvider()).ExtractUsage(nil); got != (Usage{}) {
		t.Errorf("ExtractUsage(nil) = %+v, want zero Usage", got)
	}
}

// ---------- validator uncovered branches ----------

// TestValidateFirstFailed_NilReq — nil request → the canned
// "request body is empty" failure.
func TestValidateFirstFailed_NilReq(t *testing.T) {
	msg, failed := validateFirstFailed(nil)
	if !failed || msg != "request body is empty" {
		t.Errorf("validateFirstFailed(nil) = (%q, %v), want (request body is empty, true)", msg, failed)
	}
}

// TestTypesafeValidateFirstFailed_NilAndFailure — the subset
// runner rejects nil requests and re-asserts the shape
// validators (choice with 1 option).
func TestTypesafeValidateFirstFailed_NilAndFailure(t *testing.T) {
	msg, failed := typesafeValidateFirstFailed(nil)
	if !failed || msg != "request body is empty" {
		t.Errorf("nil = (%q, %v), want canned empty failure", msg, failed)
	}
	msg, failed = typesafeValidateFirstFailed(&DecisionRequest{
		Questions: []Question{{Type: QuestionTypeChoice, Name: "d", Choices: []ChoiceOption{{Value: "only"}}}},
	})
	if !failed || !strings.Contains(msg, "needs at least 2 options") {
		t.Errorf("one-option = (%q, %v), want choice-count failure", msg, failed)
	}
}

// TestValidateDuplicateQuestionNames_SkipsEmptyNames — empty
// names don't collide with each other (the skip branch); two
// real duplicates still fail.
func TestValidateDuplicateQuestionNames_SkipsEmptyNames(t *testing.T) {
	if msg, failed := ValidateDuplicateQuestionNames(&DecisionRequest{
		Questions: []Question{{Name: ""}, {Name: "a"}},
	}); failed {
		t.Errorf("empty-name skip: msg=%q failed=%v, want pass", msg, failed)
	}
	if msg, failed := ValidateDuplicateQuestionNames(&DecisionRequest{
		Questions: []Question{{Name: "a"}, {Name: " a "}},
	}); !failed || !strings.Contains(msg, "duplicate") {
		t.Errorf("trimmed dup: msg=%q failed=%v, want failure", msg, failed)
	}
}

// TestValidateChoiceOptionsCount_TooManyOptions — a choice
// question with 256 options breaches the OpenAI-side cap.
func TestValidateChoiceOptionsCount_TooManyOptions(t *testing.T) {
	many := make([]ChoiceOption, 256)
	for i := range many {
		many[i] = ChoiceOption{Value: i}
	}
	msg, failed := ValidateChoiceOptionsCount(&DecisionRequest{
		Questions: []Question{{Type: QuestionTypeChoice, Name: "big", Choices: many}},
	})
	if !failed || !strings.Contains(msg, "too many options") {
		t.Errorf("256 options = (%q, %v), want too-many failure", msg, failed)
	}
}

// TestValidateBooleanStringCollision_OrderedBranches — the
// orderings of the bool/string collision detector: string-case
// detection, inverse detection for true and for false, and a
// non-choice question skip + clean string-only pass.
//
// NOTE (out-of-scope finding, not asserted here): the bool
// case self-synthesizes its string form into stringSet, so the
// inverse checks fire for ANY boolean-valued choice option —
// e.g. ["maybe", true] is flagged as a collision. That looks
// like an over-rejection (translateQuestion/labelFromValue
// explicitly support boolean values downstream); flagged to
// the dispatcher, test-only scope here.
func TestValidateBooleanStringCollision_OrderedBranches(t *testing.T) {
	// String arrives after the bool: string-case detection.
	if msg, failed := ValidateBooleanStringCollision(&DecisionRequest{
		Questions: []Question{{Type: QuestionTypeChoice, Name: "q", Choices: []ChoiceOption{
			{Value: true}, {Value: "true"},
		}}},
	}); !failed || !strings.Contains(msg, "type-collision") {
		t.Errorf("[true, \"true\"] = (%q, %v), want string-case collision", msg, failed)
	}
	// Bool arrives after the string: inverse detection (true).
	if msg, failed := ValidateBooleanStringCollision(&DecisionRequest{
		Questions: []Question{{Type: QuestionTypeChoice, Name: "q", Choices: []ChoiceOption{
			{Value: "true"}, {Value: true},
		}}},
	}); !failed || !strings.Contains(msg, "type-collision") {
		t.Errorf("[\"true\", true] = (%q, %v), want inverse-true collision", msg, failed)
	}
	// Inverse detection (false).
	if msg, failed := ValidateBooleanStringCollision(&DecisionRequest{
		Questions: []Question{{Type: QuestionTypeChoice, Name: "q", Choices: []ChoiceOption{
			{Value: "false"}, {Value: false},
		}}},
	}); !failed || !strings.Contains(msg, "type-collision") {
		t.Errorf("[\"false\", false] = (%q, %v), want inverse-false collision", msg, failed)
	}
	// A non-choice question is skipped; a string-only choice
	// never collides.
	if msg, failed := ValidateBooleanStringCollision(&DecisionRequest{
		Questions: []Question{
			{Type: QuestionTypePredicate, Name: "p"},
			{Type: QuestionTypeChoice, Name: "q", Choices: []ChoiceOption{
				{Value: "maybe"}, {Value: "other"},
			}},
		},
	}); failed {
		t.Errorf("[\"maybe\", \"other\"] = (%q, %v), want pass", msg, failed)
	}
}

// guard: keep the io import meaningful if the failing-body
// reader shape ever changes (compile-time interface check).
var _ io.Reader = failingBody{}
