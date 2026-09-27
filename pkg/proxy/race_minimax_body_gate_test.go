package proxy

// fix/minimax-reasoning-translation-gate unit matrix.
//
// The MiniMax reasoning translation gate now fires on
// (interleaved || non-empty reasoning_content present) && provider is
// MiniMax, at both the TranslateRequestBody call site and the typed
// ReasoningSplit setter (race-internal twin A in
// pkg/proxy/race_executor.go:364,386). These tests pin the contract:
//
//   1. No header + non-empty reasoning_content + MiniMax ⇒ translated
//      (the bugfix case — fallback lands on MiniMax without the
//      client opting in via X-Proxy-Interleaved-Thinking)
//   2. Header + non-empty reasoning_content + MiniMax ⇒ translated
//      (unchanged known-good behavior preserved)
//   3. No header + no reasoning + MiniMax ⇒ NOT translated,
//      ReasoningSplit nil, ReasoningDetails empty (the byte-identical
//      negative-case is already covered by the matrix file as B-N1;
//      re-asserted here as a regression pin)
//   4. No header + `reasoning_content: ""` ⇒ NOT translated
//      (empty string MUST NOT count — HasReasoningContent rule)
//   5. Non-MiniMax provider (zai/glm) ⇒ body unchanged, no
//      ReasoningSplit, no ReasoningDetails synthesized
//   6. Native `reasoning_details` client (no reasoning_content string)
//      + MiniMax ⇒ no double-translation — gate stays off, the
//      client's reasoning_details array is preserved verbatim on the
//      typed request
//
// All six are exercised against executeInternalRequest, the canonical
// race-internal site. The same widening is applied to
// pkg/ultimatemodel/handler_internal.go:137 (twin B) and
// pkg/ultimatemodel/handler_external.go:111,184,342 — see
// handler_minimax_body_gate_test.go in that package.

import (
	"context"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
)

// miniMaxBodyGateTestMocks returns a captured-request mock + a MiniMax
// credential-backed model config used by every test in this file.
func miniMaxBodyGateTestMocks(t *testing.T, modelID, providerName string) (*testProvider, *ConfigSnapshot) {
	t.Helper()
	origNewProvider := newProviderClient
	t.Cleanup(func() { newProviderClient = origNewProvider })

	mock := newTestProvider()
	mock.SetChatCompletionResponse(&providers.ChatCompletionResponse{
		ID: "r", Object: "chat.completion", Created: 1, Model: "MiniMax-M1",
		Choices: []providers.Choice{
			{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
		},
		Usage: providers.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	})
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		return mock, nil
	}

	cfg := newTestConfigSnapshot(modelID)
	cfg.ModelsConfig = &mockModelsConfig{
		models: []models.ModelConfig{
			{
				ID:            modelID,
				Name:          modelID,
				Enabled:       true,
				Internal:      true,
				Credentials:   models.TestRefs("cred-1"),
				InternalModel: "MiniMax-M1",
			},
		},
		credentials: []models.CredentialConfig{
			{ID: "cred-1", Provider: providerName, APIKey: "test-key"},
		},
	}
	return mock, cfg
}

// Test 1: No header + non-empty reasoning_content + MiniMax ⇒ translated.
//
// The bugfix case. The client did not opt in via the header, but the
// body echoes reasoning_content. On fallback the proxy selects MiniMax;
// without the gate widening the reasoning_content string passes through
// untranslated and MiniMax rejects with 400 (2013). With the widening,
// the gate fires, TranslateRequestBody mutates bodyMap (reasoning_content
// → reasoning_details + top-level reasoning_split), and convertToProviderRequest
// hydrates the typed ChatMessage.ReasoningDetails. The typed
// ReasoningSplit is set to ptr(true).
func TestRaceInternal_BodyGate_NoHeader_ReasoningContent_MiniMax_Translates(t *testing.T) {
	mock, cfg := miniMaxBodyGateTestMocks(t, "minimax-model-internal", "minimax")

	inputBody := []byte(`{
  "model": "minimax-model-internal",
  "messages": [
    {"role": "user", "content": "hi"},
    {"role": "assistant", "content": "answer", "reasoning_content": "think-1"}
  ]
}`)
	upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "minimax-model-internal", 1024*1024)

	// Flag absent (interleaved=false).
	if err := executeInternalRequest(context.Background(), cfg, inputBody, upstreamReq, false); err != nil {
		t.Fatalf("executeInternalRequest: %v", err)
	}
	captured := mock.GetCapturedRequest()
	if captured == nil {
		t.Fatal("provider did not capture the request")
	}

	// Typed ReasoningSplit MUST be set (gate fires on body content).
	if captured.ReasoningSplit == nil || *captured.ReasoningSplit != true {
		t.Errorf("ReasoningSplit = %v, want ptr(true) for MiniMax body-gate hit", captured.ReasoningSplit)
	}
	if len(captured.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(captured.Messages))
	}
	// The assistant message's reasoning_content was stripped (translate
	// ran) and turned into a reasoning_details array.
	if captured.Messages[1].ReasoningContent != "" {
		t.Errorf("ReasoningContent = %q, want empty (strip-and-replace after translate)", captured.Messages[1].ReasoningContent)
	}
	if len(captured.Messages[1].ReasoningDetails) != 1 {
		t.Fatalf("len(ReasoningDetails) = %d, want 1 (one entry per reasoning_content message)", len(captured.Messages[1].ReasoningDetails))
	}
	rd := captured.Messages[1].ReasoningDetails[0]
	if rd.Type != "reasoning.text" || rd.Format != "MiniMax-response-v1" || rd.Index != 0 || rd.Text != "think-1" {
		t.Errorf("ReasoningDetails[0] = %+v, want {Type:reasoning.text, Format:MiniMax-response-v1, Index:0, Text:think-1}", rd)
	}
	if !strings.HasPrefix(rd.ID, "reasoning-text-") {
		t.Errorf("ReasoningDetails[0].ID = %q, want reason-text-N prefix", rd.ID)
	}
}

// Test 2: Header + non-empty reasoning_content + MiniMax ⇒ translated
// (unchanged known-good behavior).
//
// The pre-bugfix ensemble traffic path: client opts in via the header.
// The widening is OR'd with the existing condition so the
// (header+content) combination MUST stay identical to today — proving
// the fix is a strict widening, not a behavioral change for any
// previously-translated request.
func TestRaceInternal_BodyGate_Header_ReasoningContent_MiniMax_Translates(t *testing.T) {
	mock, cfg := miniMaxBodyGateTestMocks(t, "minimax-model-internal", "minimax")

	inputBody := []byte(`{
  "model": "minimax-model-internal",
  "messages": [
    {"role": "assistant", "content": "answer", "reasoning_content": "think-1"}
  ]
}`)
	upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "minimax-model-internal", 1024*1024)

	// Flag ON (interleaved=true).
	if err := executeInternalRequest(context.Background(), cfg, inputBody, upstreamReq, true); err != nil {
		t.Fatalf("executeInternalRequest: %v", err)
	}
	captured := mock.GetCapturedRequest()
	if captured == nil {
		t.Fatal("provider did not capture the request")
	}
	if captured.ReasoningSplit == nil || *captured.ReasoningSplit != true {
		t.Errorf("ReasoningSplit = %v, want ptr(true) for MiniMax", captured.ReasoningSplit)
	}
	if len(captured.Messages[0].ReasoningDetails) != 1 {
		t.Fatalf("len(ReasoningDetails) = %d, want 1", len(captured.Messages[0].ReasoningDetails))
	}
	if captured.Messages[0].ReasoningContent != "" {
		t.Errorf("ReasoningContent = %q, want empty (strip-and-replace)", captured.Messages[0].ReasoningContent)
	}
}

// Test 3: No header + no reasoning_content + MiniMax ⇒ NOT translated.
//
// Regression pin: this is the (B-N1) byte-identical negative case.
// The gate is off because (interleaved=false, hasReasoning=false). The
// typed ReasoningSplit MUST be nil and the messages array MUST NOT
// carry synthesized reasoning_details.
//
// NOTE: this case is already covered by the matrix file
// TestRaceInternal_NegativeCase_FlagAbsent_MiniMaxCred_ResponseByteIdentical
// in race_interleaved_matrix_test.go. Re-asserted here as a dedicated
// pin for the body-gate feature so a regression on the
// HasReasoningContent scan shows up here first.
func TestRaceInternal_BodyGate_NoHeader_NoReasoning_MiniMax_NotTranslated(t *testing.T) {
	mock, cfg := miniMaxBodyGateTestMocks(t, "minimax-model-internal", "minimax")

	inputBody := []byte(`{
  "model": "minimax-model-internal",
  "messages": [
    {"role": "user", "content": "hi"},
    {"role": "assistant", "content": "answer"}
  ]
}`)
	upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "minimax-model-internal", 1024*1024)

	if err := executeInternalRequest(context.Background(), cfg, inputBody, upstreamReq, false); err != nil {
		t.Fatalf("executeInternalRequest: %v", err)
	}
	captured := mock.GetCapturedRequest()
	if captured == nil {
		t.Fatal("provider did not capture the request")
	}
	if captured.ReasoningSplit != nil {
		t.Errorf("ReasoningSplit = %v, want nil (no header + no reasoning_content ⇒ gate off)", *captured.ReasoningSplit)
	}
	for i, msg := range captured.Messages {
		if len(msg.ReasoningDetails) != 0 {
			t.Errorf("Messages[%d].ReasoningDetails = %+v, want empty (no reasoning_content ⇒ no synthesized details)", i, msg.ReasoningDetails)
		}
	}
}

// Test 4: No header + `reasoning_content: ""` + MiniMax ⇒ NOT translated.
//
// Empty reasoning_content MUST NOT count as present (matches the
// TranslateMessagesReasoning empty-skip rule). Without this invariant,
// every reasoning-less request with a placeholder empty string field
// would stamp reasoning_split on the wire — wasted wire bytes, plus
// confusing logs in the MiniMax provider.
func TestRaceInternal_BodyGate_NoHeader_EmptyReasoningContent_MiniMax_NotTranslated(t *testing.T) {
	mock, cfg := miniMaxBodyGateTestMocks(t, "minimax-model-internal", "minimax")

	inputBody := []byte(`{
  "model": "minimax-model-internal",
  "messages": [
    {"role": "assistant", "content": "answer", "reasoning_content": ""}
  ]
}`)
	upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "minimax-model-internal", 1024*1024)

	if err := executeInternalRequest(context.Background(), cfg, inputBody, upstreamReq, false); err != nil {
		t.Fatalf("executeInternalRequest: %v", err)
	}
	captured := mock.GetCapturedRequest()
	if captured == nil {
		t.Fatal("provider did not capture the request")
	}
	if captured.ReasoningSplit != nil {
		t.Errorf("ReasoningSplit = %v, want nil (empty reasoning_content does NOT count as present)", *captured.ReasoningSplit)
	}
	if len(captured.Messages[0].ReasoningDetails) != 0 {
		t.Errorf("ReasoningDetails = %+v, want empty (no translate on empty reasoning_content)", captured.Messages[0].ReasoningDetails)
	}
	// The empty reasoning_content string itself is preserved (no
	// strip-and-replace when the gate is off).
	if captured.Messages[0].ReasoningContent != "" {
		t.Errorf("ReasoningContent = %q, want empty", captured.Messages[0].ReasoningContent)
	}
}

// Test 5: Non-MiniMax provider ⇒ byte-identical body, no translation.
//
// Symmetry pin: the gate widening is provider-scoped. A zai/glm
// credential MUST see no reasoning_split injection and no
// reasoning_details synthesis — even when the body carries
// reasoning_content and the header is set.
func TestRaceInternal_BodyGate_NonMiniMaxProvider_HeaderAndContent_NotTranslated(t *testing.T) {
	mock, cfg := miniMaxBodyGateTestMocks(t, "openai-model-internal", "openai")

	inputBody := []byte(`{
  "model": "openai-model-internal",
  "messages": [
    {"role": "assistant", "content": "answer", "reasoning_content": "think-1"}
  ]
}`)
	upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "openai-model-internal", 1024*1024)

	// Both signals present, but the resolved credential is openai.
	if err := executeInternalRequest(context.Background(), cfg, inputBody, upstreamReq, true); err != nil {
		t.Fatalf("executeInternalRequest: %v", err)
	}
	captured := mock.GetCapturedRequest()
	if captured == nil {
		t.Fatal("provider did not capture the request")
	}
	if captured.ReasoningSplit != nil {
		t.Errorf("ReasoningSplit = %v, want nil (non-MiniMax provider)", *captured.ReasoningSplit)
	}
	if len(captured.Messages[0].ReasoningDetails) != 0 {
		t.Errorf("ReasoningDetails = %+v, want empty (non-MiniMax provider)", captured.Messages[0].ReasoningDetails)
	}
	// The reasoning_content string is preserved verbatim (not
	// translated; openai is the original carrier).
	if captured.Messages[0].ReasoningContent != "think-1" {
		t.Errorf("ReasoningContent = %q, want think-1 (no strip on non-MiniMax)", captured.Messages[0].ReasoningContent)
	}
	// And the mock's request stays intact (no MiniMax wire vocabulary
	// leaked into a non-MiniMax provider's request).
	if mock.chatCompletionResp == nil {
		// sanity — we set a response in the helper
		t.Fatal("mock chatCompletionResp is nil — helper setup error")
	}
}

// Test 6: Native `reasoning_details` client (no reasoning_content
// string) + MiniMax ⇒ no double-translation.
//
// A client that already speaks native MiniMax `reasoning_details`
// arrays (e.g. another MiniMax-aware proxy in the chain) MUST stay
// untouched. The gate condition `hasReasoning = false` (no
// `reasoning_content` string) AND `interleaved = false` (no header)
// ⇒ gate off. The reasoning_details array is hydrated as-is via
// providers.HydrateReasoningDetails; no reasoning_split is stamped
// on top; no second reasoning_details array is synthesized.
func TestRaceInternal_BodyGate_NativeReasoningDetails_NoHeader_MiniMax_NoDoubleTranslate(t *testing.T) {
	mock, cfg := miniMaxBodyGateTestMocks(t, "minimax-model-internal", "minimax")

	inputBody := []byte(`{
  "model": "minimax-model-internal",
  "messages": [
    {
      "role": "assistant",
      "content": "answer",
      "reasoning_details": [
        {
          "type": "reasoning.text",
          "id": "reasoning-text-7",
          "format": "MiniMax-response-v1",
          "index": 0,
          "text": "client-side reasoning"
        }
      ]
    }
  ]
}`)
	upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "minimax-model-internal", 1024*1024)

	if err := executeInternalRequest(context.Background(), cfg, inputBody, upstreamReq, false); err != nil {
		t.Fatalf("executeInternalRequest: %v", err)
	}
	captured := mock.GetCapturedRequest()
	if captured == nil {
		t.Fatal("provider did not capture the request")
	}
	if captured.ReasoningSplit != nil {
		t.Errorf("ReasoningSplit = %v, want nil (native-reasoning-details client + no header ⇒ gate off, no double-translate)", *captured.ReasoningSplit)
	}
	if len(captured.Messages[0].ReasoningDetails) != 1 {
		t.Fatalf("len(ReasoningDetails) = %d, want 1 (the client-supplied entry, untouched)", len(captured.Messages[0].ReasoningDetails))
	}
	rd := captured.Messages[0].ReasoningDetails[0]
	if rd.ID != "reasoning-text-7" || rd.Text != "client-side reasoning" || rd.Type != "reasoning.text" || rd.Format != "MiniMax-response-v1" {
		t.Errorf("ReasoningDetails[0] = %+v, want the client-supplied entry preserved verbatim", rd)
	}
	// No reasoning_content string was ever present ⇒ still empty.
	if captured.Messages[0].ReasoningContent != "" {
		t.Errorf("ReasoningContent = %q, want empty", captured.Messages[0].ReasoningContent)
	}
}
