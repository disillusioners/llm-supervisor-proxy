package proxy

// handler_functions_boundary_test.go — db-cache-layer 1D boundary
// contract tests (OpenAI path). The gate at the initRequestContext
// resolution site must:
//   - nil + healthy              → 404 model_not_found (remove-external-
//     upstream P1: the external passthrough that used to serve an
//     unresolved model is GONE; a healthy store answering nil is a
//     definitive unknown model and must NOT forward upstream)
//   - nil + !healthy             → 503 config_store_unavailable
//   - nil + no health capability → 404 model_not_found (same gate; the
//     health capability only decides 503-vs-404, never a passthrough)

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// healthStubModels is a models config with an injectable Healthy().
type healthStubModels struct {
	*models.ModelsConfig
	healthy bool
}

func (h *healthStubModels) Healthy() bool { return h.healthy }

// plainModels is models.NewModelsConfig() WITHOUT ConfigStoreHealth —
// the legacy seam (decorator forgotten in wiring).
type plainModels struct {
	*models.ModelsConfig
}

func upstreamCountingHandler(hits *atomic.Int64) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}
}

func TestOpenAIBoundary_HealthyUnknownReturns404(t *testing.T) {
	var hits atomic.Int64
	mc := &healthStubModels{ModelsConfig: models.NewModelsConfig(), healthy: true}
	h, _ := newTestHandler(t, upstreamCountingHandler(&hits), mc)

	w := httptest.NewRecorder()
	h.HandleChatCompletions(w, makeRequest(t, simpleBody("not-configured-model", false)))

	// remove-external-upstream P1: an unresolved model 404s at the
	// boundary — the deleted external passthrough is never reached.
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

func TestOpenAIBoundary_UnhealthyNilReturnsServiceUnavailable(t *testing.T) {
	var hits atomic.Int64
	mc := &healthStubModels{ModelsConfig: models.NewModelsConfig(), healthy: false}
	h, _ := newTestHandler(t, upstreamCountingHandler(&hits), mc)

	// Review fix 2026-08-28 (finding 2): the 503 path must ALSO
	// publish config_store_unavailable — parity with the Anthropic
	// boundary site. Before the fix the `rc != nil` guard here was
	// dead code (initRequestContext returns nil on this path) and the
	// event never fired. The shared drainEvents helper replays the bus
	// history (last 100 events), so subscribing after the call is fine.
	w := httptest.NewRecorder()
	h.HandleChatCompletions(w, makeRequest(t, simpleBody("never-seen-model", false)))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil + !healthy must 503, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !containsAll(body, `"type":"config_store_unavailable"`, "never-seen-model") {
		t.Errorf("error payload must carry error_type=config_store_unavailable and the model name: %s", body)
	}
	if hits.Load() != 0 {
		t.Error("503 must NEVER forward the request upstream (the misroute class)")
	}

	matches := drainEvents(h.bus, "config_store_unavailable")
	if len(matches) == 0 {
		t.Fatal("503 path must publish config_store_unavailable on the event bus (UI event log parity)")
	}
	data, ok := matches[0].Data.(map[string]interface{})
	if !ok {
		t.Fatalf("event data must be a map, got %T", matches[0].Data)
	}
	if data["model"] != "never-seen-model" {
		t.Errorf("event must carry the model name, got %v", data["model"])
	}
	if id, _ := data["id"].(string); id == "" {
		t.Error("event must carry a non-empty request id (sentinel-carried reqID)")
	}
}

func TestOpenAIBoundary_NoHealthCapabilityUnknownReturns404(t *testing.T) {
	var hits atomic.Int64
	mc := &plainModels{ModelsConfig: models.NewModelsConfig()}
	h, _ := newTestHandler(t, upstreamCountingHandler(&hits), mc)

	w := httptest.NewRecorder()
	h.HandleChatCompletions(w, makeRequest(t, simpleBody("legacy-external-model", false)))

	// remove-external-upstream P1: the no-health-capability seam still
	// 404s an unresolved model — the health capability only decides
	// 503-vs-404, never an upstream passthrough.
	if w.Code != http.StatusNotFound {
		t.Fatalf("no-health-capability + unresolved model must 404, got %d: %s", w.Code, w.Body.String())
	}
	if hits.Load() != 0 {
		t.Error("unknown model must NEVER reach the upstream")
	}
}

func containsAll(s string, frags ...string) bool {
	for _, f := range frags {
		if !strings.Contains(s, f) {
			return false
		}
	}
	return true
}
