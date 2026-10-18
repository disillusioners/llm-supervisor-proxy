package ultimatemodel

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
)

// P2-10: per-site response-side negative-case byte-identical tests
// for the ultimate paths. With flag=true + non-MiniMax provider, the
// response translator (byte-path on external, openai.go typed
// path on internal) must NOT inject reasoning fields and the
// response body must be unchanged.
//
// The corresponding race-side tests live in
// pkg/proxy/race_response_interleaved_test.go.

// ─────────────────────────────────────────────────────────────────────────────
// Ultimate-internal non-stream: flag set + non-MiniMax credential ⇒ no reasoning fields
// ─────────────────────────────────────────────────────────────────────────────

func TestExecuteInternal_Response_NegativeCase_ByteIdentical_NonMiniMax(t *testing.T) {
	// Ultimate-internal uses the typed ChatCompletionResponse
	// (json.Encode) on the response side. openai.go's extraction
	// is naturally inert on a non-MiniMax shape.
	origNewProvider := newProviderClient
	defer func() { newProviderClient = origNewProvider }()

	mock := newMockProvider()
	mock.chatResp = &providers.ChatCompletionResponse{
		ID: "r", Object: "chat.completion", Created: 1, Model: "gpt-4o-mini",
		Choices: []providers.Choice{
			{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
		},
		Usage: providers.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
	}
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		return mock, nil
	}

	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddInternalModel("openai-model", "openai", "test-key", "", "gpt-4o-mini")
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("X-Proxy-Interleaved-Thinking", "true")

	body := map[string]interface{}{
		"model": "openai-model",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("openai-model"), false, true, "")
	if err != nil {
		t.Fatalf("executeInternal: %v", err)
	}

	got := w.Body.String()
	// No reasoning fields injected (openai.go's extraction is
	// naturally inert on a non-MiniMax shape).
	//
	// N-3 note: no bytes.Equal assertion is possible here — the
	// ultimate-internal path re-encodes the typed
	// ChatCompletionResponse via json.Marshal of the struct, so no
	// verbatim upstream bytes exist (the mock returns a typed
	// struct, not wire bytes). Existing assertions stand as-is.
	if strings.Contains(got, "reasoning_split") {
		t.Errorf("body unexpectedly contains reasoning_split: %s", got)
	}
	if strings.Contains(got, "reasoning_details") {
		t.Errorf("body unexpectedly contains reasoning_details: %s", got)
	}
	if !strings.Contains(got, `"content":"ok"`) {
		t.Errorf("upstream content lost: %s", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Ultimate-internal stream: flag set + non-MiniMax credential ⇒ no thinking events
// ─────────────────────────────────────────────────────────────────────────────

func TestExecuteInternal_StreamResponse_NegativeCase_NoThinkingEvents_NonMiniMax(t *testing.T) {
	origNewProvider := newProviderClient
	defer func() { newProviderClient = origNewProvider }()

	mock := newMockProvider()
	mock.streamEvents = []providers.StreamEvent{
		{Type: "content", Content: "hi"},
		{Type: "done", FinishReason: "stop", Response: &providers.ChatCompletionResponse{
			ID: "r", Object: "chat.completion", Created: 1, Model: "gpt-4o-mini",
			Choices: []providers.Choice{
				{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "hi"}, FinishReason: "stop"},
			},
		}},
	}
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		return mock, nil
	}

	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddInternalModel("openai-model", "openai", "test-key", "", "gpt-4o-mini")
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("X-Proxy-Interleaved-Thinking", "true")

	body := map[string]interface{}{
		"model":  "openai-model",
		"stream": true,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("openai-model"), true, true, "")
	if err != nil {
		t.Fatalf("executeInternal: %v", err)
	}

	got := w.Body.String()
	if strings.Contains(got, "reasoning_content") {
		t.Errorf("stream unexpectedly contains reasoning_content: %s", got)
	}
	if strings.Contains(got, "reasoning_details") {
		t.Errorf("stream unexpectedly contains reasoning_details: %s", got)
	}
	// N-3 note: no bytes.Equal assertion is possible here — the
	// ultimate-internal stream path synthesizes SSE lines from typed
	// StreamEvents (regenerated id/created), so no verbatim upstream
	// bytes exist. Absence assertions above stand as-is.
}

// ─────────────────────────────────────────────────────────────────────────────
