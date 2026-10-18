package ultimatemodel

import (
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

// P1-9 negative-case byte-identical tests for ultimate paths. When the
// flag is set BUT the resolved credential is NOT MiniMax, the body must
// remain unchanged from a translator perspective and the proxy header
// must NOT reach upstream.

// ─────────────────────────────────────────────────────────────────────────────
// Ultimate-external: flag set + non-MiniMax credential ⇒ byte-identical
// ─────────────────────────────────────────────────────────────────────────────

func TestExecuteExternal_NegativeCase_ByteIdentical_NonMiniMax(t *testing.T) {
	var capturedBody []byte
	var capturedHeaders http.Header
	var mu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		capturedHeaders = r.Header.Clone()
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"r","choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	defer upstream.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = upstream.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{
		ID:       "ultimate-model",
		Name:     "ultimate-model",
		Enabled:  true,
		Internal: false,
	})
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	// Flag set; the Execute()-level re-parser would pick this up; for the
	// executeExternal direct-call path we also pass the parsed value
	// directly via the interleaved parameter.
	r.Header.Set("X-Proxy-Interleaved-Thinking", "true")
	r.Header.Set("Authorization", "Bearer client-key")

	body := map[string]interface{}{
		"model": "ultimate-model",
		"messages": []map[string]interface{}{
			{"role": "assistant", "content": "answer", "reasoning_content": "think-1"},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	// Pass empty upstreamProvider so the gate fires only when it equals
	// "minimax" (lowercase). This simulates the credential-derived path
	// where the credential's Provider is not MiniMax.
	_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false, true, "")

	if err != nil {
		t.Fatalf("executeExternal: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// (a) Header strip: x-proxy-interleaved-thinking MUST NOT reach upstream
	if capturedHeaders.Get("x-proxy-interleaved-thinking") != "" {
		t.Errorf("x-proxy-interleaved-thinking leaked to upstream")
	}
	if capturedHeaders.Get("X-Proxy-Interleaved-Thinking") != "" {
		t.Errorf("X-Proxy-Interleaved-Thinking leaked to upstream")
	}

	// (b) No reasoning_split or reasoning_details injected
	bodyStr := string(capturedBody)
	if strings.Contains(bodyStr, "reasoning_split") {
		t.Errorf("body unexpectedly contains reasoning_split: %s", bodyStr)
	}
	if strings.Contains(bodyStr, "reasoning_details") {
		t.Errorf("body unexpectedly contains reasoning_details: %s", bodyStr)
	}
	// Original reasoning_content preserved (translator not run)
	if !strings.Contains(bodyStr, "think-1") {
		t.Errorf("body lost original reasoning_content: %s", bodyStr)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Ultimate-external: flag set + MiniMax credential ⇒ translator fires
// ─────────────────────────────────────────────────────────────────────────────

func TestExecuteExternal_PositiveCase_MiniMaxAppliesTranslator(t *testing.T) {
	var capturedBody []byte
	var mu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"r","choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	defer upstream.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = upstream.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{
		ID:       "ultimate-model",
		Name:     "ultimate-model",
		Enabled:  true,
		Internal: false,
	})
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("X-Proxy-Interleaved-Thinking", "true")

	body := map[string]interface{}{
		"model": "ultimate-model",
		"messages": []map[string]interface{}{
			{"role": "assistant", "content": "answer", "reasoning_content": "think-1"},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	// upstreamProvider="minimax" makes the gate fire.
	_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false, true, "minimax")
	if err != nil {
		t.Fatalf("executeExternal: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	bodyStr := string(capturedBody)
	if !strings.Contains(bodyStr, `"reasoning_split":true`) {
		t.Errorf("body missing reasoning_split:true: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, `"reasoning_details":[{`) {
		t.Errorf("body missing reasoning_details: %s", bodyStr)
	}
	if strings.Contains(bodyStr, `"reasoning_content":"think-1"`) {
		t.Errorf("reasoning_content not stripped: %s", bodyStr)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Ultimate-internal: flag set + non-MiniMax credential ⇒ typed fields NOT set
// ─────────────────────────────────────────────────────────────────────────────

func TestExecuteInternal_NegativeCase_TypedFieldsNotSet_NonMiniMax(t *testing.T) {
	// Save and restore the provider factory.
	origNewProvider := newProviderClient
	defer func() { newProviderClient = origNewProvider }()

	mock := newMockProvider()
	mock.chatResp = &providers.ChatCompletionResponse{
		ID:     "r",
		Object: "chat.completion",
		Model:  "gpt-4o-mini",
		Choices: []providers.Choice{
			{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
		},
		Usage: providers.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		return mock, nil
	}

	// Add an internal model whose ResolveInternalConfig returns
	// provider="openai" — so the gate is OFF.
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
			map[string]interface{}{"role": "assistant", "content": "answer", "reasoning_content": "think-1"},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	// interleaved=true; provider from ResolveInternalConfig is "openai"
	// (not MiniMax) so the gate stays off.
	_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("openai-model"), false, true, "")
	if err != nil {
		t.Fatalf("executeInternal: %v", err)
	}

	if mock.capturedReq == nil {
		t.Fatal("provider did not capture the request")
	}

	// Typed ReasoningSplit MUST be nil (gate off)
	if mock.capturedReq.ReasoningSplit != nil {
		t.Errorf("ReasoningSplit = %v, want nil (non-MiniMax)", *mock.capturedReq.ReasoningSplit)
	}
	// ReasoningDetails MUST be empty (translator not invoked)
	if len(mock.capturedReq.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(mock.capturedReq.Messages))
	}
	if len(mock.capturedReq.Messages[0].ReasoningDetails) != 0 {
		t.Errorf("ReasoningDetails = %+v, want empty", mock.capturedReq.Messages[0].ReasoningDetails)
	}
	// ReasoningContent was hydrated as a string (pre-existing behavior;
	// not from translator)
	if mock.capturedReq.Messages[0].ReasoningContent != "think-1" {
		t.Errorf("ReasoningContent = %q, want think-1", mock.capturedReq.Messages[0].ReasoningContent)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Ultimate-internal: flag set + MiniMax credential ⇒ typed fields SET
// ─────────────────────────────────────────────────────────────────────────────

func TestExecuteInternal_PositiveCase_TypedFieldsSet_MiniMax(t *testing.T) {
	origNewProvider := newProviderClient
	defer func() { newProviderClient = origNewProvider }()

	mock := newMockProvider()
	mock.chatResp = &providers.ChatCompletionResponse{
		ID:     "r",
		Object: "chat.completion",
		Model:  "MiniMax-M1",
		Choices: []providers.Choice{
			{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
		},
		Usage: providers.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		return mock, nil
	}

	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddInternalModel("minimax-model", "minimax", "test-key", "", "MiniMax-M1")
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("X-Proxy-Interleaved-Thinking", "true")

	body := map[string]interface{}{
		"model": "minimax-model",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("minimax-model"), false, true, "")
	if err != nil {
		t.Fatalf("executeInternal: %v", err)
	}

	if mock.capturedReq == nil {
		t.Fatal("provider did not capture the request")
	}

	// Typed ReasoningSplit MUST be set (gate fires for MiniMax)
	if mock.capturedReq.ReasoningSplit == nil || *mock.capturedReq.ReasoningSplit != true {
		t.Errorf("ReasoningSplit = %v, want ptr(true) for MiniMax", mock.capturedReq.ReasoningSplit)
	}
}

// TestExecuteInternal_PositiveCase_TranslatesRequestBody_MiniMax
// (W5 positive) verifies that the ultimate-internal request
// side (handler_internal.go) applies the full strip-and-replace
// translation when the gate fires (flag + MiniMax credential):
//   - ReasoningSplit=true is set on the typed request
//   - Each message's reasoning_content becomes a one-entry
//     reasoning_details array
//   - reasoning_content is cleared (strip-and-replace)
//
// Without the W5 fix, only ReasoningSplit was set, leaking
// reasoning_content on the wire (the SDK does not
// auto-translate).
func TestExecuteInternal_PositiveCase_TranslatesRequestBody_MiniMax(t *testing.T) {
	origNewProvider := newProviderClient
	defer func() { newProviderClient = origNewProvider }()

	mock := newMockProvider()
	mock.chatResp = &providers.ChatCompletionResponse{
		ID: "r", Object: "chat.completion", Model: "MiniMax-M1",
		Choices: []providers.Choice{
			{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
		},
		Usage: providers.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		return mock, nil
	}

	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddInternalModel("minimax-model", "minimax", "test-key", "", "MiniMax-M1")
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("X-Proxy-Interleaved-Thinking", "true")

	body := map[string]interface{}{
		"model": "minimax-model",
		"messages": []interface{}{
			map[string]interface{}{"role": "assistant", "content": "answer", "reasoning_content": "think-1"},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("minimax-model"), false, true, "")
	if err != nil {
		t.Fatalf("executeInternal: %v", err)
	}

	if mock.capturedReq == nil {
		t.Fatal("provider did not capture the request")
	}
	// ReasoningSplit must be set (gate fires for MiniMax).
	if mock.capturedReq.ReasoningSplit == nil || *mock.capturedReq.ReasoningSplit != true {
		t.Errorf("ReasoningSplit = %v, want ptr(true) for MiniMax", mock.capturedReq.ReasoningSplit)
	}
	if len(mock.capturedReq.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(mock.capturedReq.Messages))
	}
	// Per-message translation: reasoning_details must be
	// populated from the original reasoning_content; the
	// original content string must be cleared.
	if len(mock.capturedReq.Messages[0].ReasoningDetails) != 1 {
		t.Errorf("ReasoningDetails = %+v, want 1 entry", mock.capturedReq.Messages[0].ReasoningDetails)
	} else {
		entry := mock.capturedReq.Messages[0].ReasoningDetails[0]
		if entry.Type != "reasoning.text" {
			t.Errorf("entry.Type = %q, want reasoning.text", entry.Type)
		}
		if entry.Text != "think-1" {
			t.Errorf("entry.Text = %q, want think-1", entry.Text)
		}
		if entry.Format != "MiniMax-response-v1" {
			t.Errorf("entry.Format = %q, want MiniMax-response-v1", entry.Format)
		}
	}
	if mock.capturedReq.Messages[0].ReasoningContent != "" {
		t.Errorf("ReasoningContent = %q, want empty (strip-and-replace)", mock.capturedReq.Messages[0].ReasoningContent)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// shared variable to capture the request in mockProvider
// ─────────────────────────────────────────────────────────────────────────────

func init() {
	// ensure sync is used (avoid unused import if tests are filtered out)
	_ = sync.Mutex{}
}
