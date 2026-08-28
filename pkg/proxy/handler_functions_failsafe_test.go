package proxy

// handler_functions_failsafe_test.go — db-cache-layer 1D boundary
// contract tests (OpenAI path). The gate at the initRequestContext
// resolution site must:
//   - nil + healthy            → today's legit external passthrough
//   - nil + !healthy           → 503 config_store_unavailable
//   - nil + no health capability → today's behavior (legacy safe)

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

func TestOpenAIBoundary_HealthyNilReturnsExternalPassthrough(t *testing.T) {
	var hits atomic.Int64
	mc := &healthStubModels{ModelsConfig: models.NewModelsConfig(), healthy: true}
	h, _ := newTestHandler(t, upstreamCountingHandler(&hits), mc)

	w := httptest.NewRecorder()
	h.HandleChatCompletions(w, makeRequest(t, simpleBody("not-configured-model", false)))

	if w.Code != http.StatusOK {
		t.Fatalf("nil + healthy must be today's external passthrough, got %d: %s", w.Code, w.Body.String())
	}
	if hits.Load() == 0 {
		t.Error("external passthrough must reach the upstream")
	}
}

func TestOpenAIBoundary_UnhealthyNilReturnsServiceUnavailable(t *testing.T) {
	var hits atomic.Int64
	mc := &healthStubModels{ModelsConfig: models.NewModelsConfig(), healthy: false}
	h, _ := newTestHandler(t, upstreamCountingHandler(&hits), mc)

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
}

func TestOpenAIBoundary_NoHealthCapabilityLegacySafe(t *testing.T) {
	var hits atomic.Int64
	mc := &plainModels{ModelsConfig: models.NewModelsConfig()}
	h, _ := newTestHandler(t, upstreamCountingHandler(&hits), mc)

	w := httptest.NewRecorder()
	h.HandleChatCompletions(w, makeRequest(t, simpleBody("legacy-external-model", false)))

	if w.Code != http.StatusOK {
		t.Fatalf("no-health-capability must keep today's behavior (passthrough), got %d: %s", w.Code, w.Body.String())
	}
	if hits.Load() == 0 {
		t.Error("legacy path must still reach the upstream")
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
