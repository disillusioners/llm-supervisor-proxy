package proxy

// remove-external-upstream P5 — the D1 behavioral exit pins (the
// commission's core guarantee) plus the D5 unauthenticated-hoist pins.
//
// D1: an unknown model — or a LEGACY `internal = 0` row that still
// RESOLVES (GetModel never filters `enabled`, so the `!Internal` conjunct
// is the SOLE 404 mechanism for the permanent post-032 legacy rows) —
// must produce a clean 404 with ZERO upstream requests, on BOTH protocol
// routes, with each route's own envelope shape.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// legacyInternalZeroModels builds a ModelsConfig carrying the EXACT
// permanent legacy shape (internal = 0, enabled = false, no credentials).
// The direct slice write BYPASSES the AddModel Internal=true write-stamp —
// that is the point: migration 032's disable arm leaves `internal = 0` on
// those rows permanently, so only the boundary `!Internal` conjunct can
// 404 them.
func legacyInternalZeroModels() *models.ModelsConfig {
	mc := models.NewModelsConfig()
	mc.Models = append(mc.Models, models.ModelConfig{
		ID:       "glm-4.5-air",
		Name:     "glm-4.5-air",
		Enabled:  false,
		Internal: false,
	})
	return mc
}

// ─── D1: OpenAI route ────────────────────────────────────────────────────────

func Test404Gate_Chat_UnknownModel(t *testing.T) {
	var hits atomic.Int64
	h, _ := newTestHandler(t, upstreamCountingHandler(&hits), nil)

	rec := httptest.NewRecorder()
	h.HandleChatCompletions(rec, makeRequest(t, simpleBody("no-such-model-anywhere", false)))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown model: status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"type":"invalid_request_error"`) ||
		!strings.Contains(rec.Body.String(), `"code":"model_not_found"`) {
		t.Errorf("404 body must be the OpenAI envelope with code model_not_found: %s", rec.Body.String())
	}
	if hits.Load() != 0 {
		t.Errorf("unknown model must make ZERO upstream requests; got %d", hits.Load())
	}
}

func Test404Gate_Chat_LegacyInternalZeroRow(t *testing.T) {
	var hits atomic.Int64
	h, _ := newTestHandler(t, upstreamCountingHandler(&hits), legacyInternalZeroModels())

	rec := httptest.NewRecorder()
	h.HandleChatCompletions(rec, makeRequest(t, simpleBody("glm-4.5-air", false)))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("legacy internal=0 row: status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"model_not_found"`) {
		t.Errorf("404 body must carry code model_not_found: %s", rec.Body.String())
	}
	if hits.Load() != 0 {
		t.Errorf("legacy internal=0 row must make ZERO upstream requests; got %d", hits.Load())
	}
}

// ─── D1: Anthropic route ─────────────────────────────────────────────────────

func Test404Gate_Anthropic_UnknownModel(t *testing.T) {
	var hits atomic.Int64
	h, _ := newTestHandler(t, upstreamCountingHandler(&hits), nil)

	rec := httptest.NewRecorder()
	h.HandleAnthropicMessages(rec, anthropicRequest(t, "no-such-model-anywhere"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown model: status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"type":"not_found_error"`) ||
		!strings.Contains(rec.Body.String(), `"code":"model_not_found"`) {
		t.Errorf("404 body must be the Anthropic envelope with not_found_error / model_not_found: %s", rec.Body.String())
	}
	if hits.Load() != 0 {
		t.Errorf("unknown model must make ZERO upstream requests; got %d", hits.Load())
	}
}

func Test404Gate_Anthropic_LegacyInternalZeroRow(t *testing.T) {
	var hits atomic.Int64
	h, _ := newTestHandler(t, upstreamCountingHandler(&hits), legacyInternalZeroModels())

	rec := httptest.NewRecorder()
	h.HandleAnthropicMessages(rec, anthropicRequest(t, "glm-4.5-air"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("legacy internal=0 row: status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"type":"not_found_error"`) ||
		!strings.Contains(rec.Body.String(), `"code":"model_not_found"`) {
		t.Errorf("404 body must be the Anthropic envelope: %s", rec.Body.String())
	}
	if hits.Load() != 0 {
		t.Errorf("legacy internal=0 row must make ZERO upstream requests; got %d", hits.Load())
	}
}

// ─── D1 (spec item 6): the dead bypass header confers nothing ────────────────

func Test404Gate_DeadBypassHeader_Still404(t *testing.T) {
	var hits atomic.Int64
	h, _ := newTestHandler(t, upstreamCountingHandler(&hits), nil)

	req := makeRequest(t, simpleBody("no-such-model-anywhere", false))
	req.Header.Set("x-llmproxy-bypass-internal", "true")
	rec := httptest.NewRecorder()
	h.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("dead bypass header: status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
	if hits.Load() != 0 {
		t.Errorf("the retired bypass header must confer NOTHING; got %d upstream requests", hits.Load())
	}
}

// ─── D5: force-ultimate + unresolvable body model + no/bad auth ──────────────

// TestForceUltimateHoist_Unauthenticated_NoPremature404 pins the
// verifier-recommended behavioural guard: the force header defers the
// boundary 404 gate, but with NO credentials the request must stop at the
// AUTH region (401) — never leak a premature model_not_found 404 before
// auth, and never run the ultimate path for an anonymous caller.
func TestForceUltimateHoist_Unauthenticated_NoPremature404(t *testing.T) {
	env := setupHoistTest(t, true /* ultimateModelEnabled */)

	rec := env.doNoAuth(t, true /* forceHeader */)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated force-ultimate: status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "model_not_found") {
		t.Errorf("premature 404 leak before auth: %s", rec.Body.String())
	}
	for _, log := range env.reqStore.List() {
		if log.UltimateModelUsed {
			t.Error("UltimateModelUsed=true for an unauthenticated caller — the ultimate path must not run")
		}
	}
}

// TestForceUltimateHoist_BadToken_NoPremature404 — same guard with a
// malformed bearer token.
func TestForceUltimateHoist_BadToken_NoPremature404(t *testing.T) {
	env := setupHoistTest(t, true /* ultimateModelEnabled */)

	rec := env.doBadToken(t, true /* forceHeader */)

	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Fatalf("bad-token force-ultimate: status = %d, want 401/403 (body=%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "model_not_found") {
		t.Errorf("premature 404 leak before auth: %s", rec.Body.String())
	}
	for _, log := range env.reqStore.List() {
		if log.UltimateModelUsed {
			t.Error("UltimateModelUsed=true for a bad-token caller — the ultimate path must not run")
		}
	}
}

// ─── D3: the lightllm provider routes like any other provider ────────────────

// TestProviderRouting_LightLLM_NonStreamAndStream drives a chat request
// (non-stream + stream) for an INTERNAL model whose credential provider is
// "lightllm" — proving lightllm is a regular provider end-to-end through
// the proxy (factory → OpenAI-compatible wire → httptest upstream), with
// the credential's key as the upstream Authorization.
func TestProviderRouting_LightLLM_NonStreamAndStream(t *testing.T) {
	var seenPaths []string
	var seenAuth []string

	upstreamHandler := func(w http.ResponseWriter, r *http.Request) {
		seenPaths = append(seenPaths, r.URL.Path)
		seenAuth = append(seenAuth, r.Header.Get("Authorization"))

		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		isStream, _ := body["stream"].(bool)
		if !isStream {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"ll-upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"lightllm ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: %s\n\n", makeOpenAITextChunk("lightllm stream ok"))
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}

	mc := models.NewModelsConfig()
	if err := mc.AddCredential(models.CredentialConfig{
		ID: "ll-cred", Provider: "lightllm", APIKey: "ll-key", BaseURL: "",
	}); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}
	h, upstream := newTestHandler(t, upstreamHandler, mc)
	// Point the lightllm credential at the mock upstream (the fixture's
	// test-cred is separate and unused by this model).
	if err := mc.UpdateCredential("ll-cred", models.CredentialConfig{
		ID: "ll-cred", Provider: "lightllm", APIKey: "ll-key", BaseURL: upstream.URL,
	}); err != nil {
		t.Fatalf("UpdateCredential: %v", err)
	}
	if err := mc.AddModel(models.ModelConfig{
		ID: "ll-model", Name: "ll-model", Enabled: true, Internal: true,
		InternalModel: "ll-upstream-model", Credentials: models.TestRefs("ll-cred"),
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	// Non-stream.
	rec := httptest.NewRecorder()
	h.HandleChatCompletions(rec, makeRequest(t, simpleBody("ll-model", false)))
	if rec.Code != http.StatusOK {
		t.Fatalf("lightllm non-stream: status = %d (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "lightllm ok") {
		t.Errorf("lightllm non-stream: client body missing upstream content: %s", rec.Body.String())
	}

	// Stream.
	recS := httptest.NewRecorder()
	h.HandleChatCompletions(recS, makeRequest(t, simpleBody("ll-model", true)))
	if recS.Code != http.StatusOK {
		t.Fatalf("lightllm stream: status = %d (body=%s)", recS.Code, recS.Body.String())
	}
	if !strings.Contains(recS.Body.String(), "[DONE]") {
		t.Errorf("lightllm stream: missing [DONE]: %s", recS.Body.String())
	}

	if len(seenPaths) != 2 {
		t.Fatalf("expected 2 upstream calls, got %d", len(seenPaths))
	}
	for _, p := range seenPaths {
		if p != "/chat/completions" {
			t.Errorf("lightllm upstream path = %q, want /chat/completions (OpenAI-compatible wire)", p)
		}
	}
	for _, a := range seenAuth {
		if a != "Bearer ll-key" {
			t.Errorf("lightllm upstream Authorization = %q, want the credential key", a)
		}
	}
}
