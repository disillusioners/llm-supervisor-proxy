package ultimatemodel

// fix/minimax-reasoning-translation-gate unit matrix for the
// ultimate sites (twin B + 3 external gate sites).
//
// The race-internal twin A unit matrix is in
// pkg/proxy/race_minimax_body_gate_test.go. This file pins the
// same widening at the four ultim sites:
//
//   1. ultimate-INTERNAL request gate (handler_internal.go:137 — twin B)
//   2. ultimate-EXTERNAL request gate (handler_external.go:111-112)
//   3. ultimate-EXTERNAL non-stream RESPONSE gate (handler_external.go:184)
//   4. ultimate-EXTERNAL stream RESPONSE gate (handler_external.go:342,
//      inside streamResponse)
//
// Hard rules from the bugfix spec:
//   - Both halves of each pair (request ↔ typed setter / request ↔
//     response translator) must fire together — never one without
//     the other (untested half-state forbidden).
//   - Empty `reasoning_content: ""` MUST NOT count as present.
//   - Native reasoning_details clients (no reasoning_content string)
//     must stay untouched (no double-translation).

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

// ─────────────────────────────────────────────────────────────────────────────
// 1. ultimate-INTERNAL body-gate matrix
// ─────────────────────────────────────────────────────────────────────────────

// TestExecuteInternal_BodyGate_NoHeader_ReasoningContent_MiniMax_Translates
// pins the bugfix case for the ultimate-internal twin-B site. The
// client sends reasoning_content without the header; the resolved
// credential is MiniMax. The widened gate fires the typed
// ReasoningSplit setter AND the message-level reasoning_content →
// reasoning_details translation.
func TestExecuteInternal_BodyGate_NoHeader_ReasoningContent_MiniMax_Translates(t *testing.T) {
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
	_ = httptest.NewRequest("POST", "/v1/chat/completions", nil) // NO header

	body := map[string]interface{}{
		"model": "minimax-model",
		"messages": []interface{}{
			map[string]interface{}{"role": "assistant", "content": "answer", "reasoning_content": "think-1"},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	// Flag absent (interleaved=false); body carries reasoning_content.
	_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("minimax-model"), false, false, "")
	if err != nil {
		t.Fatalf("executeInternal: %v", err)
	}

	if mock.capturedReq == nil {
		t.Fatal("provider did not capture the request")
	}
	// Typed ReasoningSplit MUST be set (gate fires on body content).
	if mock.capturedReq.ReasoningSplit == nil || *mock.capturedReq.ReasoningSplit != true {
		t.Errorf("ReasoningSplit = %v, want ptr(true) for MiniMax body-gate hit", mock.capturedReq.ReasoningSplit)
	}
	if len(mock.capturedReq.Messages[0].ReasoningDetails) != 1 {
		t.Fatalf("len(ReasoningDetails) = %d, want 1", len(mock.capturedReq.Messages[0].ReasoningDetails))
	}
	rd := mock.capturedReq.Messages[0].ReasoningDetails[0]
	if rd.Type != "reasoning.text" || rd.Format != "MiniMax-response-v1" || rd.Text != "think-1" {
		t.Errorf("ReasoningDetails[0] = %+v, want {Type:reasoning.text, Format:MiniMax-response-v1, Text:think-1}", rd)
	}
	// Strip-and-replace: reasoning_content MUST be cleared.
	if mock.capturedReq.Messages[0].ReasoningContent != "" {
		t.Errorf("ReasoningContent = %q, want empty (strip-and-replace)", mock.capturedReq.Messages[0].ReasoningContent)
	}
}

// TestExecuteInternal_BodyGate_NoHeader_EmptyReasoningContent_MiniMax_NotTranslated
// pins case 4 of the spec matrix: empty reasoning_content must not
// count. The gate stays off; the typed request carries
// ReasoningSplit=nil and an empty reasoning_content string (no
// strip-and-replace happens because the translator was not invoked).
func TestExecuteInternal_BodyGate_NoHeader_EmptyReasoningContent_MiniMax_NotTranslated(t *testing.T) {
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
	body := map[string]interface{}{
		"model": "minimax-model",
		"messages": []interface{}{
			map[string]interface{}{"role": "assistant", "content": "answer", "reasoning_content": ""},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("minimax-model"), false, false, "")
	if err != nil {
		t.Fatalf("executeInternal: %v", err)
	}

	if mock.capturedReq == nil {
		t.Fatal("provider did not capture the request")
	}
	if mock.capturedReq.ReasoningSplit != nil {
		t.Errorf("ReasoningSplit = %v, want nil (empty reasoning_content does NOT count)", *mock.capturedReq.ReasoningSplit)
	}
	if len(mock.capturedReq.Messages[0].ReasoningDetails) != 0 {
		t.Errorf("ReasoningDetails = %+v, want empty", mock.capturedReq.Messages[0].ReasoningDetails)
	}
}

// TestExecuteInternal_BodyGate_NonMiniMax_HeaderAndContent_NotTranslated
// pins case 5 of the spec matrix at the ultimate-internal site. The
// widened gate is provider-scoped — a non-MiniMax credential must
// see no ReasoningSplit stamping and no reasoning_details
// synthesis.
func TestExecuteInternal_BodyGate_NonMiniMax_HeaderAndContent_NotTranslated(t *testing.T) {
	origNewProvider := newProviderClient
	defer func() { newProviderClient = origNewProvider }()

	mock := newMockProvider()
	mock.chatResp = &providers.ChatCompletionResponse{
		ID: "r", Object: "chat.completion", Model: "gpt-4o-mini",
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
	modelsCfg.AddInternalModel("openai-model", "openai", "test-key", "", "gpt-4o-mini")
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("X-Proxy-Interleaved-Thinking", "true") // header ON

	body := map[string]interface{}{
		"model": "openai-model",
		"messages": []interface{}{
			map[string]interface{}{"role": "assistant", "content": "answer", "reasoning_content": "think-1"},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("openai-model"), false, true, "")
	if err != nil {
		t.Fatalf("executeInternal: %v", err)
	}

	if mock.capturedReq == nil {
		t.Fatal("provider did not capture the request")
	}
	if mock.capturedReq.ReasoningSplit != nil {
		t.Errorf("ReasoningSplit = %v, want nil (non-MiniMax credential)", *mock.capturedReq.ReasoningSplit)
	}
	if len(mock.capturedReq.Messages[0].ReasoningDetails) != 0 {
		t.Errorf("ReasoningDetails = %+v, want empty (non-MiniMax credential)", mock.capturedReq.Messages[0].ReasoningDetails)
	}
	// OpenAI is the original carrier of reasoning_content — preserve it.
	if mock.capturedReq.Messages[0].ReasoningContent != "think-1" {
		t.Errorf("ReasoningContent = %q, want think-1 (non-MiniMax carrier)", mock.capturedReq.Messages[0].ReasoningContent)
	}
}

// TestExecuteInternal_BodyGate_NativeReasoningDetails_NoHeader_MiniMax_NoDoubleTranslate
// pins the no-double-translation contract at the ultimate-internal
// twin-B site (handler_internal.go:148). A client that already speaks
// native MiniMax `reasoning_details` arrays (no `reasoning_content`
// string, no header) MUST pass through untouched: the gate stays
// off, no `reasoning_split` is stamped on the typed request, and the
// client's reasoning_details entry is preserved verbatim on the typed
// `ChatMessage.ReasoningDetails`. Mirrors the race-internal twin A
// pin TestRaceInternal_BodyGate_NativeReasoningDetails_NoHeader_MiniMax_NoDoubleTranslate.
func TestExecuteInternal_BodyGate_NativeReasoningDetails_NoHeader_MiniMax_NoDoubleTranslate(t *testing.T) {
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
	_ = httptest.NewRequest("POST", "/v1/chat/completions", nil) // NO header

	// Native MiniMax reasoning_details client: assistant message
	// carries the typed entry, NO `reasoning_content` string.
	body := map[string]interface{}{
		"model": "minimax-model",
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "assistant",
				"content": "answer",
				"reasoning_details": []interface{}{
					map[string]interface{}{
						"type":   "reasoning.text",
						"id":     "reasoning-text-7",
						"format": "MiniMax-response-v1",
						"index":  0,
						"text":   "client-side reasoning",
					},
				},
			},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	// Flag absent (interleaved=false); body carries NO reasoning_content.
	_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("minimax-model"), false, false, "")
	if err != nil {
		t.Fatalf("executeInternal: %v", err)
	}

	if mock.capturedReq == nil {
		t.Fatal("provider did not capture the request")
	}
	// Gate stays off: no reasoning_split is stamped.
	if mock.capturedReq.ReasoningSplit != nil {
		t.Errorf("ReasoningSplit = %v, want nil (native reasoning_details client + no header ⇒ gate off, no double-translate)", *mock.capturedReq.ReasoningSplit)
	}
	// The client-supplied reasoning_details entry is preserved verbatim —
	// convertRequest delegates to providers.HydrateReasoningDetails.
	if len(mock.capturedReq.Messages[0].ReasoningDetails) != 1 {
		t.Fatalf("len(ReasoningDetails) = %d, want 1 (the client-supplied entry, untouched)", len(mock.capturedReq.Messages[0].ReasoningDetails))
	}
	rd := mock.capturedReq.Messages[0].ReasoningDetails[0]
	if rd.ID != "reasoning-text-7" || rd.Text != "client-side reasoning" || rd.Type != "reasoning.text" || rd.Format != "MiniMax-response-v1" || rd.Index != 0 {
		t.Errorf("ReasoningDetails[0] = %+v, want the client-supplied entry preserved verbatim", rd)
	}
	// No reasoning_content string was ever present ⇒ still empty.
	if mock.capturedReq.Messages[0].ReasoningContent != "" {
		t.Errorf("ReasoningContent = %q, want empty", mock.capturedReq.Messages[0].ReasoningContent)
	}
}

// TestExecuteInternal_BodyGate_NoHeader_NoReasoning_MiniMax_NotTranslated
// pins matrix case 3 at the ultimate-internal twin-B site
// (handler_internal.go:148, typed-setter path): no header,
// reasoning-less body (no `reasoning_content` anywhere), MiniMax
// credential ⇒ gate OFF. No `reasoning_split` is stamped on the
// typed request and no `reasoning_details` are synthesized on any
// captured message. Internal-gate counterpart of
// TestExecuteExternal_BodyGate_NoHeader_NoReasoning_MiniMax_NotTranslated
// (matrix case 3 on the external side).
func TestExecuteInternal_BodyGate_NoHeader_NoReasoning_MiniMax_NotTranslated(t *testing.T) {
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
	_ = httptest.NewRequest("POST", "/v1/chat/completions", nil) // NO header

	// Reasoning-less body: a user message and a plain assistant
	// answer. No reasoning_content anywhere, no reasoning_details.
	body := map[string]interface{}{
		"model": "minimax-model",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
			map[string]interface{}{"role": "assistant", "content": "answer"},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	// Flag absent (interleaved=false); reasoning-less body. Gate stays
	// off (hasReasoning=false; MiniMax provider alone does not fire it).
	_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("minimax-model"), false, false, "")
	if err != nil {
		t.Fatalf("executeInternal: %v", err)
	}

	if mock.capturedReq == nil {
		t.Fatal("provider did not capture the request")
	}
	// Gate off ⇒ typed ReasoningSplit MUST NOT be stamped.
	if mock.capturedReq.ReasoningSplit != nil {
		t.Errorf("ReasoningSplit = %v, want nil (reasoning-less body ⇒ gate off)", *mock.capturedReq.ReasoningSplit)
	}
	// Gate off ⇒ no reasoning_details synthesis on ANY captured message.
	for i, msg := range mock.capturedReq.Messages {
		if len(msg.ReasoningDetails) != 0 {
			t.Errorf("Messages[%d].ReasoningDetails = %+v, want empty (reasoning-less body ⇒ no synthesis)", i, msg.ReasoningDetails)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 2. ultimate-EXTERNAL request-side body-gate matrix
// ─────────────────────────────────────────────────────────────────────────────

// TestExecuteExternal_BodyGate_NoHeader_ReasoningContent_MiniMax_Translates
// pins case 7a of the spec matrix — the ultimate-external request
// gate at handler_external.go:111-112. The proxy forwards the
// upstream-bound body; when the body carries reasoning_content and
// the credential is MiniMax, the upstream MUST see reasoning_details
// arrays + top-level reasoning_split.
func TestExecuteExternal_BodyGate_NoHeader_ReasoningContent_MiniMax_Translates(t *testing.T) {
	var capturedBody []byte
	var capturedMu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMu.Lock()
		capturedBody, _ = io.ReadAll(r.Body)
		capturedMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"r","choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	defer upstream.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = upstream.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{
		ID:       "minimax-model",
		Name:     "minimax-model",
		Enabled:  true,
		Internal: false,
	})
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil) // NO header

	body := map[string]interface{}{
		"model": "minimax-model",
		"messages": []interface{}{
			map[string]interface{}{"role": "assistant", "content": "answer", "reasoning_content": "think-1"},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	// upstreamProvider="minimax" (matches the credential), no header.
	if _, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("minimax-model"), false, false, "minimax"); err != nil {
		t.Fatalf("executeExternal: %v", err)
	}

	capturedMu.Lock()
	got := string(capturedBody)
	capturedMu.Unlock()

	if !strings.Contains(got, `"reasoning_split":true`) {
		t.Errorf("upstream body missing reasoning_split:true: %s", got)
	}
	if !strings.Contains(got, `"reasoning_details"`) {
		t.Errorf("upstream body missing reasoning_details: %s", got)
	}
	if strings.Contains(got, `"reasoning_content":"think-1"`) {
		t.Errorf("upstream body still carries raw reasoning_content (not stripped): %s", got)
	}
}

// TestExecuteExternal_BodyGate_NoHeader_EmptyReasoningContent_MiniMax_NotTranslated
// pins the empty-doesn't-count rule on the ultimate-external
// request side. The upstream body MUST be byte-identical to the
// input (no translator invocation).
func TestExecuteExternal_BodyGate_NoHeader_EmptyReasoningContent_MiniMax_NotTranslated(t *testing.T) {
	var capturedBody []byte
	var capturedMu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMu.Lock()
		capturedBody, _ = io.ReadAll(r.Body)
		capturedMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"r","choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	defer upstream.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = upstream.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "minimax-model", Name: "minimax-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil) // NO header

	body := map[string]interface{}{
		"model": "minimax-model",
		"messages": []interface{}{
			map[string]interface{}{"role": "assistant", "content": "answer", "reasoning_content": ""},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	if _, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("minimax-model"), false, false, "minimax"); err != nil {
		t.Fatalf("executeExternal: %v", err)
	}

	capturedMu.Lock()
	got := string(capturedBody)
	capturedMu.Unlock()

	// Body MUST NOT carry translator-injected fields.
	if strings.Contains(got, "reasoning_split") || strings.Contains(got, "reasoning_details") {
		t.Errorf("body unexpectedly contains translator field (empty reasoning_content must not count): %s", got)
	}
	// Empty reasoning_content preserved on the wire.
	if !strings.Contains(got, `"reasoning_content":""`) {
		t.Errorf("body lost empty reasoning_content (must be preserved on gate off): %s", got)
	}
}

// TestExecuteExternal_BodyGate_NonMiniMax_HeaderAndContent_NotTranslated
// pins provider-scope on the ultimate-external request side: a
// non-MiniMax upstreamProvider sees no translator invocation even
// when both signals are present.
func TestExecuteExternal_BodyGate_NonMiniMax_HeaderAndContent_NotTranslated(t *testing.T) {
	var capturedBody []byte
	var capturedMu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMu.Lock()
		capturedBody, _ = io.ReadAll(r.Body)
		capturedMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"r","choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	defer upstream.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = upstream.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "openai-model", Name: "openai-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("X-Proxy-Interleaved-Thinking", "true") // header ON

	body := map[string]interface{}{
		"model": "openai-model",
		"messages": []interface{}{
			map[string]interface{}{"role": "assistant", "content": "answer", "reasoning_content": "think-1"},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	if _, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("openai-model"), false, true, "openai"); err != nil {
		t.Fatalf("executeExternal: %v", err)
	}

	capturedMu.Lock()
	got := string(capturedBody)
	capturedMu.Unlock()

	if strings.Contains(got, "reasoning_split") || strings.Contains(got, "reasoning_details") {
		t.Errorf("body unexpectedly contains translator field (non-MiniMax provider): %s", got)
	}
	if !strings.Contains(got, `"reasoning_content":"think-1"`) {
		t.Errorf("body lost original reasoning_content (non-MiniMax carrier): %s", got)
	}
}

// TestExecuteExternal_BodyGate_NativeReasoningDetails_NoHeader_MiniMax_NoDoubleTranslate
// pins the no-double-translation contract at the ultimate-external
// request gate (handler_external.go:123). A client that already
// speaks native MiniMax `reasoning_details` arrays (no
// `reasoning_content` string, no header) MUST pass through
// untouched: the upstream-bound body MUST NOT be re-wrapped — no
// top-level `reasoning_split` is stamped and the client's
// reasoning_details array is preserved verbatim. Mirrors the
// race-internal twin A pin
// TestRaceInternal_BodyGate_NativeReasoningDetails_NoHeader_MiniMax_NoDoubleTranslate.
func TestExecuteExternal_BodyGate_NativeReasoningDetails_NoHeader_MiniMax_NoDoubleTranslate(t *testing.T) {
	var capturedBody []byte
	var capturedMu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMu.Lock()
		capturedBody, _ = io.ReadAll(r.Body)
		capturedMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"r","choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	defer upstream.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = upstream.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "minimax-model", Name: "minimax-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil) // NO header

	// Native MiniMax reasoning_details client: assistant message
	// carries the typed entry, NO `reasoning_content` string.
	body := map[string]interface{}{
		"model": "minimax-model",
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "assistant",
				"content": "answer",
				"reasoning_details": []interface{}{
					map[string]interface{}{
						"type":   "reasoning.text",
						"id":     "reasoning-text-7",
						"format": "MiniMax-response-v1",
						"index":  0,
						"text":   "client-side reasoning",
					},
				},
			},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	// Flag absent (interleaved=false); upstreamProvider="minimax";
	// body carries NO reasoning_content. Gate stays off.
	if _, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("minimax-model"), false, false, "minimax"); err != nil {
		t.Fatalf("executeExternal: %v", err)
	}

	capturedMu.Lock()
	got := string(capturedBody)
	capturedMu.Unlock()

	// Gate off ⇒ no translator invocation. The upstream-bound body
	// MUST NOT be re-wrapped: no top-level reasoning_split stamped.
	if strings.Contains(got, "reasoning_split") {
		t.Errorf("upstream body unexpectedly carries top-level reasoning_split (native reasoning_details client ⇒ gate off): %s", got)
	}
	// The client's reasoning_details array MUST be preserved verbatim.
	if !strings.Contains(got, `"reasoning_details"`) {
		t.Errorf("upstream body lost client's reasoning_details array (must pass through verbatim): %s", got)
	}
	if !strings.Contains(got, `"id":"reasoning-text-7"`) {
		t.Errorf("upstream body lost client-supplied reasoning_details entry id: %s", got)
	}
	if !strings.Contains(got, `"text":"client-side reasoning"`) {
		t.Errorf("upstream body lost client-supplied reasoning_details entry text: %s", got)
	}
	// No raw reasoning_content was ever present on the wire.
	if strings.Contains(got, `"reasoning_content"`) {
		t.Errorf("upstream body unexpectedly contains reasoning_content (native client, no echo): %s", got)
	}
}

// TestExecuteExternal_BodyGate_NoHeader_NoReasoning_MiniMax_NotTranslated
// pins matrix case 3 at the ultimate-external request gate: no
// header, no `reasoning_content` (reasoning-less body), MiniMax
// provider ⇒ NOT translated. The upstream-bound body MUST stay
// reasoning-less — no `reasoning_split` injected, no `reasoning_details`
// synthesized. Symmetry pin against
// TestExecuteInternal_BodyGate_NoHeader_NoReasoning_MiniMax_NotTranslated's
// internal-gate counterpart (matrix case 3 on the internal side).
func TestExecuteExternal_BodyGate_NoHeader_NoReasoning_MiniMax_NotTranslated(t *testing.T) {
	var capturedBody []byte
	var capturedMu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMu.Lock()
		capturedBody, _ = io.ReadAll(r.Body)
		capturedMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"r","choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	defer upstream.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = upstream.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "minimax-model", Name: "minimax-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil) // NO header

	// Reasoning-less body: just a user message and a plain assistant
	// answer. No reasoning_content anywhere, no reasoning_details.
	body := map[string]interface{}{
		"model": "minimax-model",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
			map[string]interface{}{"role": "assistant", "content": "answer"},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	// Flag absent (interleaved=false); upstreamProvider="minimax";
	// reasoning-less body. Gate stays off (hasReasoning=false).
	if _, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("minimax-model"), false, false, "minimax"); err != nil {
		t.Fatalf("executeExternal: %v", err)
	}

	capturedMu.Lock()
	got := string(capturedBody)
	capturedMu.Unlock()

	// Gate off ⇒ body MUST NOT carry translator-injected fields.
	if strings.Contains(got, "reasoning_split") {
		t.Errorf("upstream body unexpectedly carries top-level reasoning_split (reasoning-less body ⇒ gate off): %s", got)
	}
	if strings.Contains(got, "reasoning_details") {
		t.Errorf("upstream body unexpectedly carries reasoning_details (reasoning-less body ⇒ no synthesis): %s", got)
	}
	// The reasoning-less assistant message MUST be untouched.
	if !strings.Contains(got, `"role":"assistant"`) {
		t.Errorf("upstream body lost assistant message: %s", got)
	}
	if !strings.Contains(got, `"content":"answer"`) {
		t.Errorf("upstream body lost assistant content: %s", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 3. ultimate-EXTERNAL non-stream RESPONSE gate
// ─────────────────────────────────────────────────────────────────────────────

// TestExecuteExternal_NonStreamResponse_Gate_ReasoningContent_MiniMax_Translates
// pins the response-side gate at handler_external.go:184. When the
// request body carries reasoning_content and the credential is
// MiniMax, the upstream response's reasoning_details must be
// translated back to reasoning_content for the client.
func TestExecuteExternal_NonStreamResponse_Gate_ReasoningContent_MiniMax_Translates(t *testing.T) {
	// Upstream returns a MiniMax-shape response with reasoning_details.
	const upstreamBody = `{"id":"r","object":"chat.completion","created":1,"model":"minimax-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello","reasoning_details":[{"type":"reasoning.text","id":"reasoning-text-1","format":"MiniMax-response-v1","index":0,"text":"upstream-side reasoning"}]},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(upstreamBody))
	}))
	defer upstream.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = upstream.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "minimax-model", Name: "minimax-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil) // NO header

	body := map[string]interface{}{
		"model": "minimax-model",
		"messages": []interface{}{
			map[string]interface{}{"role": "assistant", "content": "answer", "reasoning_content": "think-1"},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	if _, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("minimax-model"), false, false, "minimax"); err != nil {
		t.Fatalf("executeExternal: %v", err)
	}

	got := w.Body.String()
	// The response translator should have run (gate fires because
	// the request body has reasoning_content + MiniMax cred), so the
	// client sees reasoning_content (not reasoning_details).
	if !strings.Contains(got, `"reasoning_content":"upstream-side reasoning"`) {
		t.Errorf("response missing reasoning_content (response translator should have run): %s", got)
	}
	if strings.Contains(got, `"reasoning_details"`) {
		t.Errorf("response still carries reasoning_details (response translator did not strip): %s", got)
	}
}

// TestExecuteExternal_NonStreamResponse_Gate_NoReasoning_NonMiniMax_ByteIdentical
// pins the byte-identical negative case at the response-side gate:
// no reasoning_content in body AND non-MiniMax credential ⇒ response
// passes through verbatim.
func TestExecuteExternal_NonStreamResponse_Gate_NoReasoning_NonMiniMax_ByteIdentical(t *testing.T) {
	const upstreamBody = `{"id":"r","object":"chat.completion","created":1,"model":"openai-model","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(upstreamBody))
	}))
	defer upstream.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = upstream.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "openai-model", Name: "openai-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)

	body := map[string]interface{}{
		"model":    "openai-model",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	if _, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("openai-model"), false, false, "openai"); err != nil {
		t.Fatalf("executeExternal: %v", err)
	}

	got := w.Body.String()
	// Byte-identical to upstream (ultimate-external non-stream does
	// not append a trailing newline; the proxy streams the upstream
	// body verbatim to the client).
	if !bytes.Equal(w.Body.Bytes(), []byte(upstreamBody)) {
		t.Errorf("response not byte-identical to upstream:\n want=%q\n  got=%q", upstreamBody, got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 4. ultimate-EXTERNAL stream RESPONSE gate (handler_external.go:342)
// ─────────────────────────────────────────────────────────────────────────────

// TestStreamResponse_Gate_ReasoningContent_MiniMax_StripsDetails
// pins the stream-side response gate. When the request body carries
// reasoning_content and the credential is MiniMax, the per-chunk
// stream translator must run — the client sees reasoning_content on
// delta lines, not reasoning_details.
func TestStreamResponse_Gate_ReasoningContent_MiniMax_StripsDetails(t *testing.T) {
	const upstreamStream = "data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hi\",\"reasoning_details\":[{\"type\":\"reasoning.text\",\"id\":\"reasoning-text-1\",\"format\":\"MiniMax-response-v1\",\"index\":0,\"text\":\"think\"}]}}]}\n\n" +
		"data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(upstreamStream))
	}))
	defer upstream.Close()

	resp, err := http.Get(upstream.URL)
	if err != nil {
		t.Fatalf("http.Get: %v", err)
	}
	defer resp.Body.Close()

	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "minimax-model", Name: "minimax-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()

	// Gate should fire because hasReasoning=true (caller passed it in
	// from executeExternal's body scan) AND providerIsMiniMax=true.
	// The stream translator is constructed, which strips
	// reasoning_details from each chunk and emits a
	// reasoning_content chunk on the side channel.
	_, err = h.streamResponse(w, resp, "minimax-model", nil, false /*interleaved*/, true /*hasReasoning*/, true /*providerIsMiniMax*/, ExecuteOptions{BufferMode: true})
	if err != nil {
		t.Fatalf("streamResponse: %v", err)
	}

	got := w.Body.String()
	// The stream translator should have run, stripping reasoning_details.
	// We assert that the response-side reasoning_content chunk was
	// emitted somewhere in the stream (a positive signal that the
	// gate fired).
	if !strings.Contains(got, "reasoning_content") {
		t.Errorf("stream missing reasoning_content emission (gate should fire on body content + MiniMax cred): %s", got)
	}
	// Strip pin: the gate firing means the stream translator MUST
	// strip reasoning_details from every re-marshaled chunk — the
	// client must not see the raw array anywhere in the stream.
	if strings.Contains(got, "reasoning_details") {
		t.Errorf("stream still carries reasoning_details (stream translator did not strip): %s", got)
	}
	// Value pin (mirrors the symmetric non-stream pin at
	// TestExecuteExternal_NonStreamResponse_Gate_ReasoningContent_MiniMax_Translates):
	// the upstream delta's reasoning_details.text ("think") must land
	// verbatim as delta.reasoning_content on the emitted chunk.
	if !strings.Contains(got, `"reasoning_content":"think"`) {
		t.Errorf("stream missing reasoning_content:\"think\" (upstream reasoning_details.text must land as reasoning_content): %s", got)
	}
}

// TestStreamResponse_Gate_NoReasoning_NoMiniMax_ByteIdentical
// pins the byte-identical negative case on the stream side: no
// body reasoning_content AND non-MiniMax credential ⇒ the stream
// translator is NOT constructed ⇒ output is byte-identical to
// upstream.
func TestStreamResponse_Gate_NoReasoning_NoMiniMax_ByteIdentical(t *testing.T) {
	const upstreamStream = "data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(upstreamStream))
	}))
	defer upstream.Close()

	resp, err := http.Get(upstream.URL)
	if err != nil {
		t.Fatalf("http.Get: %v", err)
	}
	defer resp.Body.Close()

	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "openai-model", Name: "openai-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()

	// Both signals absent (interleaved=false, hasReasoning=false)
	// AND non-MiniMax ⇒ gate off ⇒ byte-identical stream.
	_, err = h.streamResponse(w, resp, "openai-model", nil, false, false, false, ExecuteOptions{BufferMode: true})
	if err != nil {
		t.Fatalf("streamResponse: %v", err)
	}

	// Strip the trailing newlines the buffer may have appended for
	// the byte-equality check.
	got := bytes.TrimRight(w.Body.Bytes(), "\n")
	want := bytes.TrimRight([]byte(upstreamStream), "\n")
	if !bytes.Equal(got, want) {
		t.Errorf("stream not byte-identical to upstream:\n want=%q\n  got=%q", want, got)
	}
}
