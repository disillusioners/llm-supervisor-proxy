package proxy

// handler_anthropic_failsafe_test.go — db-cache-layer 1D boundary
// contract tests (Anthropic path), mirroring the OpenAI trio:
//   - nil + healthy              → today's legit external passthrough
//   - nil + !healthy             → 503 config_store_unavailable
//   - nil + no health capability → today's behavior (legacy safe)

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

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
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytesReader(raw))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestAnthropicBoundary_HealthyNilReturnsExternalPassthrough(t *testing.T) {
	var hits atomic.Int64
	mc := &healthStubModels{ModelsConfig: models.NewModelsConfig(), healthy: true}
	h, _ := newTestHandler(t, upstreamCountingHandler(&hits), mc)

	w := httptest.NewRecorder()
	h.HandleAnthropicMessages(w, anthropicRequest(t, "not-configured-model"))

	if w.Code != http.StatusOK {
		t.Fatalf("nil + healthy must be today's external passthrough, got %d: %s", w.Code, w.Body.String())
	}
	if hits.Load() == 0 {
		t.Error("external passthrough must reach the upstream")
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

func TestAnthropicBoundary_NoHealthCapabilityLegacySafe(t *testing.T) {
	var hits atomic.Int64
	mc := &plainModels{ModelsConfig: models.NewModelsConfig()}
	h, _ := newTestHandler(t, upstreamCountingHandler(&hits), mc)

	w := httptest.NewRecorder()
	h.HandleAnthropicMessages(w, anthropicRequest(t, "legacy-external-model"))

	if w.Code != http.StatusOK {
		t.Fatalf("no-health-capability must keep today's behavior (passthrough), got %d: %s", w.Code, w.Body.String())
	}
	if hits.Load() == 0 {
		t.Error("legacy path must still reach the upstream")
	}
}
