package ultimatemodel

// fix/minimax-reasoning-translation-gate unit matrix for the
// surviving ultimate site (twin B — the request gate). The three
// former ultimate-EXTERNAL gate sites are gone with the external
// arm (remove-external-upstream P1).
//
// The race-internal twin A unit matrix is in
// pkg/proxy/race_minimax_body_gate_test.go. This file pins the
// same widening at the ultimate site:
//
//   1. ultimate-INTERNAL request gate (handler_internal.go:137 — twin B)
//
// Hard rules from the bugfix spec:
//   - Both halves of each pair (request ↔ typed setter / request ↔
//     response translator) must fire together — never one without
//     the other (untested half-state forbidden).
//   - Empty `reasoning_content: ""` MUST NOT count as present.
//   - Native reasoning_details clients (no reasoning_content string)
//     must stay untouched (no double-translation).

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

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
// captured message (the external-side counterpart was removed with
// the external arm).
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
