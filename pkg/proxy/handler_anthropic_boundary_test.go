package proxy

// handler_anthropic_boundary_test.go — db-cache-layer 1D boundary
// contract tests (Anthropic path), mirroring the OpenAI trio:
//   - nil + healthy              → 404 not_found_error / model_not_found
//     (remove-external-upstream P1: the external passthrough is GONE)
//   - nil + !healthy             → 503 config_store_unavailable
//   - nil + no health capability → 404 model_not_found (same gate)

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

func anthropicRequest(t *testing.T, model string) *http.Request {
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
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestAnthropicBoundary_HealthyUnknownReturns404(t *testing.T) {
	var hits atomic.Int64
	mc := &healthStubModels{ModelsConfig: models.NewModelsConfig(), healthy: true}
	h, _ := newTestHandler(t, upstreamCountingHandler(&hits), mc)

	w := httptest.NewRecorder()
	h.HandleAnthropicMessages(w, anthropicRequest(t, "not-configured-model"))

	// remove-external-upstream P1: an unresolved model 404s at the
	// boundary with the Anthropic envelope; the upstream is untouched.
	if w.Code != http.StatusNotFound {
		t.Fatalf("healthy + unresolved model must 404, got %d: %s", w.Code, w.Body.String())
	}
	if !containsAll(w.Body.String(), `"code":"model_not_found"`, "not-configured-model") {
		t.Errorf("404 payload must carry code=model_not_found and the model name: %s", w.Body.String())
	}
	if hits.Load() != 0 {
		t.Error("unknown model must NEVER reach the upstream (the anti-bypass guarantee)")
	}
}

func TestAnthropicBoundary_UnhealthyNilReturnsServiceUnavailable(t *testing.T) {
	var hits atomic.Int64
	mc := &healthStubModels{ModelsConfig: models.NewModelsConfig(), healthy: false}
	h, _ := newTestHandler(t, upstreamCountingHandler(&hits), mc)

	// Lock the unconditional publish this site always had (review fix
	// 2026-08-28 parity check for the OpenAI mirror). drainEvents
	// replays bus history, so subscribing after the call is fine.
	w := httptest.NewRecorder()
	h.HandleAnthropicMessages(w, anthropicRequest(t, "never-seen-model"))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil + !healthy must 503, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !containsAll(body, `"type":"config_store_unavailable"`, "never-seen-model") {
		t.Errorf("error payload must carry the error type and model name: %s", body)
	}
	if hits.Load() != 0 {
		t.Error("503 must NEVER forward the request upstream (the misroute class)")
	}

	matches := drainEvents(h.bus, "config_store_unavailable")
	if len(matches) == 0 {
		t.Fatal("503 path must publish config_store_unavailable on the event bus")
	}
	data, ok := matches[0].Data.(map[string]interface{})
	if !ok {
		t.Fatalf("event data must be a map, got %T", matches[0].Data)
	}
	if data["model"] != "never-seen-model" {
		t.Errorf("event must carry the model name, got %v", data["model"])
	}
	if id, _ := data["id"].(string); id == "" {
		t.Error("event must carry a non-empty request id")
	}
}

func TestAnthropicBoundary_NoHealthCapabilityUnknownReturns404(t *testing.T) {
	var hits atomic.Int64
	mc := &plainModels{ModelsConfig: models.NewModelsConfig()}
	h, _ := newTestHandler(t, upstreamCountingHandler(&hits), mc)

	w := httptest.NewRecorder()
	h.HandleAnthropicMessages(w, anthropicRequest(t, "legacy-external-model"))

	// remove-external-upstream P1: same 404 gate without the health
	// capability (which only decides 503-vs-404).
	if w.Code != http.StatusNotFound {
		t.Fatalf("no-health-capability + unresolved model must 404, got %d: %s", w.Code, w.Body.String())
	}
	if hits.Load() != 0 {
		t.Error("unknown model must NEVER reach the upstream")
	}
}
