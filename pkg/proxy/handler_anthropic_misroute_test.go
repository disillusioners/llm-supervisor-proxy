package proxy

// Decision Models commission / §3.1 site 8 + §9.2 (R1.2 / amendment 1):
// NEW test for the Anthropic-route post-resolve `IsDecision()`
// guard at pkg/proxy/handler_anthropic.go. The route had ZERO
// misroute checks today (a verified gap); a decision model posted
// to `/v1/messages` would execute a real chat upstream call.
// The guard fires AFTER model resolution and BEFORE auth /
// upstream call; the error envelope is the route's family
// (Anthropic — per finding 23, route-local envelope family) with
// code "decision_misroute".

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// anthropicDecisionRequest builds an Anthropic wire-shape request
// for the guard test (mirrors handler_anthropic_boundary_test.go's
// anthropicRequest).
func anthropicDecisionRequest(t *testing.T, model string) *http.Request {
	t.Helper()
	body := map[string]interface{}{
		"model":      model,
		"max_tokens": 16,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// TestAnthropicBoundary_DecisionMisroute — decision model posted
// to /v1/messages ⇒ 400 + Anthropic envelope code
// "decision_misroute". Upstream MUST NOT be called.
func TestAnthropicBoundary_DecisionMisroute(t *testing.T) {
	stub := &models.ModelsConfig{}
	credCfg := models.CredentialConfig{ID: "openai-cred", Provider: "openai", APIKey: "k"}
	if err := stub.AddCredential(credCfg); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}
	cred := []models.CredentialRef{{CredentialID: "openai-cred", Weight: 1, Position: 0}}
	if err := stub.AddModel(models.ModelConfig{
		ID: "dec-1", Name: "Decision 1", Enabled: true,
		Kind: models.KindDecision, Internal: true,
		InternalModel: "gpt-6-luna", Credentials: cred,
		ExcludeFromUltimateSwitching: true,
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	var hits atomic.Int64
	h, _ := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"x"}]}`))
	}, stub)

	w := httptest.NewRecorder()
	h.HandleAnthropicMessages(w, anthropicDecisionRequest(t, "dec-1"))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("decision misroute on /v1/messages must 400, got %d: %s", w.Code, w.Body.String())
	}
	if hits.Load() != 0 {
		t.Errorf("upstream reached for decision→/v1/messages misroute; hit count = %d", hits.Load())
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("body not JSON: %v; body=%s", err, w.Body.String())
	}
	// Anthropic envelope: root "type":"error" then "error":{type,code,message}
	if parsed["type"] != "error" {
		t.Errorf("root 'type' = %v, want \"error\" (Anthropic envelope family)", parsed["type"])
	}
	errObj, ok := parsed["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("body has no 'error' object: %s", w.Body.String())
	}
	code, _ := errObj["code"].(string)
	if code != "decision_misroute" {
		t.Errorf("error.code = %q, want %q", code, "decision_misroute")
	}
	// The route-local envelope family means we keep the inner type as
	// "invalid_request_error" (matches the existing Anthropic wire
	// shape — OpenAI envelope family would have used "server_error").
	innerType, _ := errObj["type"].(string)
	if innerType != "invalid_request_error" {
		t.Errorf("error.type = %q, want \"invalid_request_error\" (route-local Anthropic envelope family)", innerType)
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "/v1/decisions") {
		t.Errorf("error.message = %q, want it to mention /v1/decisions", msg)
	}
}

// TestAnthropicBoundary_DecisionMisroute_Negatives — sanity
// checks (Decision Models / §9.2): the IsDecision() guard must
// NOT fire on chat / image-gen / unknown models. The route has
// different fallback paths for unknown models (external
// passthrough that may return 502; tested separately at
// handler_anthropic_boundary_test.go) — here we just verify the
// 400 + decision_misroute code does NOT appear for non-decision
// models.
func TestAnthropicBoundary_DecisionMisroute_Negatives(t *testing.T) {
	t.Run("unknown_model_does_not_fire_decision_misroute", func(t *testing.T) {
		stub := &models.ModelsConfig{}
		h, _ := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"x"}]}`))
		}, stub)

		w := httptest.NewRecorder()
		h.HandleAnthropicMessages(w, anthropicDecisionRequest(t, "not-a-real-model"))

		// Must not be 400 with decision_misroute (the guard
		// must not fire on unresolved models). The route
		// could legitimately return 200 (external passthrough)
		// or 502 (upstream failure) — both are acceptable
		// here.
		if w.Code == http.StatusBadRequest && strings.Contains(w.Body.String(), "decision_misroute") {
			t.Errorf("unknown model fired decision_misroute 400 (guard misfires pre-resolve); body=%s", w.Body.String())
		}
	})
}
