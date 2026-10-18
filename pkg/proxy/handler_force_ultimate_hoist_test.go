package proxy

// P1 fixup (leader ruling 2026-10-18) — behavioral pin for the
// force-ultimate hoist ABOVE the boundary unknown/non-internal 404
// gate (handler_functions.go). Ruling, verbatim:
//
//	"LEADER 2026-10-18: force-ultimate interception
//	(X-Force-Ultimate-Model + ultimate_model_enabled token) is HOISTED
//	ABOVE the 404 model-resolution gate (after the 503 store-health
//	check; auth unchanged). Body-model resolvability is NOT load-bearing
//	for ultimate requests (pre-P1 semantics preserved). Normal requests
//	keep the full 404 gate. Reason: commission removes the
//	external-upstream bypass, NOT the ultimate raw-passthrough
//	(internal-model route). Option (c) 'ultimate requires resolvable
//	body model' REJECTED as a silent behavior break of a commissioned
//	feature."
//
// Both directions are pinned here:
//
//   - (a) force header + ultimate-entitled token + UNRESOLVABLE body
//     model → NOT 404; the ultimate raw-passthrough path runs
//     (TestForceUltimateHoist_UnresolvableBodyModel_TakesUltimatePath).
//   - (b) normal request + unresolvable model → full 404 gate, at the
//     boundary (TestForceUltimateHoist_NormalRequest_UnresolvableModel_404)
//     and through the deferred post-auth re-fire when the header is
//     present but the token is NOT ultimate-entitled
//     (TestForceUltimateHoist_ForceHeader_UnentitledToken_404).
//
// The bootstrap mirrors TestUltimateModel_PersistsAssistantContentAnd
// Thinking_Internal (handler_capture_persistence_test.go): an INTERNAL
// ultimate model whose credential points at a mock upstream — the only
// ultimate target shape that still executes post-P1 (ultimatemodel.
// Execute rejects non-internal targets).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/bufferstore"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/usage"
)

// hoistTestEnv is the shared bootstrap for the hoist pin tests.
type hoistTestEnv struct {
	h              *Handler
	reqStore       *store.RequestStore
	plaintextToken string
}

// setupHoistTest wires a handler with an internal ultimate model
// ("ultimate-model") whose credential points at a mock upstream that
// returns a canned non-stream completion. ultimateModelEnabled
// controls the created token's permission.
func setupHoistTest(t *testing.T, ultimateModelEnabled bool) *hoistTestEnv {
	t.Helper()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, mockUltimateInternalResponse("hoist reply", "hoist reasoning", 7, 3, 10))
	}))
	t.Cleanup(upstream.Close)

	db := setupIntegrationDB(t)
	counter := usage.NewCounter(db, database.SQLite)
	tokenStore := auth.NewTokenStore(db, database.SQLite)
	plaintextToken, _, err := tokenStore.CreateToken(
		context.Background(),
		"hoist-token",
		nil,
		"test-user",
		ultimateModelEnabled,
		"",
		nil,
	)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	modelsConfig := models.NewModelsConfig()
	if err := modelsConfig.AddCredential(models.CredentialConfig{
		ID:       "test-credential",
		Provider: "openai",
		APIKey:   "test-api-key",
		BaseURL:  upstream.URL,
	}); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}
	if err := modelsConfig.AddModel(models.ModelConfig{
		ID:              "ultimate-model",
		Name:            "Ultimate Model",
		Enabled:         true,
		Internal:        true, // internal target — the only post-P1 executable shape
		Credentials:     models.TestRefs("test-credential"),
		InternalModel:   "internal-model",
		InternalBaseURL: upstream.URL,
	}); err != nil {
		t.Fatalf("AddModel (ultimate): %v", err)
	}

	t.Setenv("APPLY_ENV_OVERRIDES", "true")
	t.Setenv("MAX_GENERATION_TIME", "10s")
	t.Setenv("RACE_RETRY_ENABLED", "false")
	t.Setenv("ULTIMATE_MODEL_ID", "ultimate-model")

	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)
	bufStore, err := bufferstore.New(t.TempDir(), 1024*1024)
	if err != nil {
		t.Fatalf("NewBufferStore: %v", err)
	}
	h := NewHandler(&Config{ConfigMgr: mgr, ModelsConfig: modelsConfig}, bus, reqStore, bufStore, tokenStore, counter, nil)

	return &hoistTestEnv{h: h, reqStore: reqStore, plaintextToken: plaintextToken}
}

func (e *hoistTestEnv) do(t *testing.T, forceHeader bool) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]interface{}{
		// "no-such-model" is deliberately absent from ModelsConfig —
		// the unresolvable-body-model premise of the ruling.
		"model":  "no-such-model",
		"stream": false,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hoist me"},
		},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.plaintextToken)
	if forceHeader {
		req.Header.Set("X-Force-Ultimate-Model", "true")
	}
	rec := httptest.NewRecorder()
	e.h.HandleChatCompletions(rec, req)
	return rec
}

// TestForceUltimateHoist_UnresolvableBodyModel_TakesUltimatePath pins
// direction (a) of the ruling: a force-ultimate request from an
// ultimate-entitled token with an UNRESOLVABLE body model must NOT 404
// at the boundary gate — the interception is hoisted above it and the
// ultimate raw-passthrough executes (pre-P1 semantics restored).
func TestForceUltimateHoist_UnresolvableBodyModel_TakesUltimatePath(t *testing.T) {
	env := setupHoistTest(t, true /* ultimateModelEnabled */)
	rec := env.do(t, true /* forceHeader */)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("force-ultimate request 404'd on unresolvable body model — hoist not in effect (body: %s)", rec.Body.String())
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from the ultimate path, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-LLMProxy-Ultimate-Model"); got != "ultimate-model" {
		t.Errorf("X-LLMProxy-Ultimate-Model header = %q, want %q (ultimate path must have executed)", got, "ultimate-model")
	}

	var used bool
	for _, rl := range env.reqStore.List() {
		if rl.UltimateModelUsed {
			used = true
		}
	}
	if !used {
		t.Error("no request log with UltimateModelUsed=true — the ultimate path did not run")
	}
}

// TestForceUltimateHoist_NormalRequest_UnresolvableModel_404 pins
// direction (b): WITHOUT the force header the full 404 gate applies at
// the boundary — invalid_request_error + model_not_found, finalized
// request log, zero upstream traffic.
func TestForceUltimateHoist_NormalRequest_UnresolvableModel_404(t *testing.T) {
	env := setupHoistTest(t, true /* ultimateModelEnabled */)
	rec := env.do(t, false /* no forceHeader */)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for normal request with unresolvable model, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "model_not_found") {
		t.Errorf("error envelope missing model_not_found code: %s", body)
	}
	if !strings.Contains(body, "invalid_request_error") {
		t.Errorf("error envelope missing invalid_request_error type: %s", body)
	}

	logs := env.reqStore.List()
	if len(logs) != 1 {
		t.Fatalf("expected 1 request log, got %d", len(logs))
	}
	if logs[0].Status != "failed" {
		t.Errorf("request log status = %q, want failed", logs[0].Status)
	}
	if !strings.Contains(logs[0].Error, "not found") {
		t.Errorf("request log error = %q, want a not-found message", logs[0].Error)
	}
}

// TestForceUltimateHoist_ForceHeader_UnentitledToken_404 pins the
// deferred re-fire: the header alone defers the boundary gate, but a
// token WITHOUT ultimate_model_enabled does not qualify for the
// ultimate path — the request is a normal request and the full 404
// gate fires post-auth with the boundary site's exact envelope.
func TestForceUltimateHoist_ForceHeader_UnentitledToken_404(t *testing.T) {
	env := setupHoistTest(t, false /* ultimateModelEnabled */)
	rec := env.do(t, true /* forceHeader */)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for force-header request from unentitled token, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "model_not_found") {
		t.Errorf("error envelope missing model_not_found code: %s", body)
	}

	logs := env.reqStore.List()
	if len(logs) != 1 {
		t.Fatalf("expected 1 request log, got %d", len(logs))
	}
	if logs[0].Status != "failed" {
		t.Errorf("request log status = %q, want failed", logs[0].Status)
	}
	if logs[0].UltimateModelUsed {
		t.Error("UltimateModelUsed=true on an unentitled force request — the ultimate path must not run")
	}
}

// doNoAuth is do() with NO Authorization header (the unauthenticated
// force-ultimate guard, P5 D5).
func (e *hoistTestEnv) doNoAuth(t *testing.T, forceHeader bool) *httptest.ResponseRecorder {
	t.Helper()
	return e.doWithAuth(t, forceHeader, "")
}

// doBadToken is do() with a malformed bearer token (P5 D5).
func (e *hoistTestEnv) doBadToken(t *testing.T, forceHeader bool) *httptest.ResponseRecorder {
	t.Helper()
	return e.doWithAuth(t, forceHeader, "not-a-real-token")
}

// doWithAuth is the parameterized body of do(): an unresolvable body
// model + an optional bearer token.
func (e *hoistTestEnv) doWithAuth(t *testing.T, forceHeader bool, token string) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]interface{}{
		"model":  "no-such-model",
		"stream": false,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hoist me"},
		},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if forceHeader {
		req.Header.Set("X-Force-Ultimate-Model", "true")
	}
	rec := httptest.NewRecorder()
	e.h.HandleChatCompletions(rec, req)
	return rec
}
