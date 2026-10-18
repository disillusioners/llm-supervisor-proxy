package proxy

import (
	"context"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
)

// P1-9 negative-case byte-identical tests for race paths. The proxy MUST
// remain byte-identical to today when (rc.interleavedThinking=true &&
// cred.Provider="openai"): translator not invoked, no x-proxy-interleaved-
// thinking leak to upstream, body unchanged from a translator perspective.

// ─────────────────────────────────────────────────────────────────────────────
// Race-internal: flag set + non-MiniMax credential ⇒ typed fields NOT set
// ─────────────────────────────────────────────────────────────────────────────

// TestRaceInternal_NegativeCase_TypedFieldsNotSet_NonMiniMax verifies that
// when the request carries X-Proxy-Interleaved-Thinking but the resolved
// internal credential is OpenAI, the typed ReasoningSplit and per-message
// ReasoningDetails are NOT set on the captured ChatCompletionRequest.
func TestRaceInternal_NegativeCase_TypedFieldsNotSet_NonMiniMax(t *testing.T) {
	// Save + restore the newProviderClient variable.
	origNewProvider := newProviderClient
	defer func() { newProviderClient = origNewProvider }()

	// Mock provider that captures the request.
	mock := newTestProvider()
	mock.SetChatCompletionResponse(&providers.ChatCompletionResponse{
		ID:      "r",
		Object:  "chat.completion",
		Created: 1700000000,
		Model:   "openai-model-internal",
		Choices: []providers.Choice{
			{
				Index: 0,
				Message: &providers.ChatMessage{
					Role:    "assistant",
					Content: "hi",
				},
				FinishReason: "stop",
			},
		},
	})
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		return mock, nil
	}

	cfg := newTestConfigSnapshot("openai-model-internal")
	cfg.ModelsConfig = &mockModelsConfig{
		models: []models.ModelConfig{
			{
				ID:            "openai-model-internal",
				Name:          "openai-model-internal",
				Enabled:       true,
				Internal:      true,
				Credentials:   models.TestRefs("openai-cred"),
				InternalModel: "gpt-4o-mini",
			},
		},
		credentials: []models.CredentialConfig{
			{ID: "openai-cred", Provider: "openai", APIKey: "test-key"},
		},
	}

	inputBody := []byte(`{
  "model": "openai-model-internal",
  "messages": [
    {"role": "assistant", "content": "answer", "reasoning_content": "think-1"}
  ]
}`)
	upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "openai-model-internal", 1024*1024)

	if err := executeInternalRequest(context.Background(), cfg, inputBody, upstreamReq, true); err != nil {
		t.Fatalf("executeInternalRequest: %v", err)
	}

	captured := mock.GetCapturedRequest()
	if captured == nil {
		t.Fatal("provider did not capture the request")
	}

	// D1 typed fields must NOT be set (gate is off: non-MiniMax provider)
	if captured.ReasoningSplit != nil {
		t.Errorf("ReasoningSplit = %v, want nil (non-MiniMax credential)", *captured.ReasoningSplit)
	}
	if len(captured.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(captured.Messages))
	}
	if len(captured.Messages[0].ReasoningDetails) != 0 {
		t.Errorf("ReasoningDetails = %+v, want empty (non-MiniMax credential)", captured.Messages[0].ReasoningDetails)
	}
	// The reasoning_content was hydrated as a string (pre-existing behavior
	// is preserved); translator did NOT run so reasoning_content is still
	// present and reasoning_details is NOT synthesized.
	if captured.Messages[0].ReasoningContent != "think-1" {
		t.Errorf("ReasoningContent = %q, want think-1", captured.Messages[0].ReasoningContent)
	}
}
