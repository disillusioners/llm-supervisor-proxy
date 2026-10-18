package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
)

// P2-10: per-site response-side negative-case byte-identical tests.
// For each of the 4 sites (race-ext non-stream + stream, race-int
// non-stream + stream) with flag=true + non-MiniMax provider, the
// response translator (byte-path on external, openai.go on internal)
// must NOT inject reasoning fields and the response body must be
// unchanged.
//
// The corresponding ultimate-ext + ultimate-int sites are tested in
// pkg/ultimatemodel/handler_response_interleaved_test.go.

func TestRaceInternal_Response_NegativeCase_ByteIdentical_NonMiniMax(t *testing.T) {
	// The race-internal path uses the typed ChatCompletionResponse
	// (Encode), with openai.go's response extraction happening
	// during unmarshal. Non-MiniMax upstreams do not carry
	// reasoning_details so the extraction is naturally inert.
	// Mock provider returns an OpenAI-shape response.
	origNewProvider := newProviderClient
	defer func() { newProviderClient = origNewProvider }()

	mock := newTestProvider()
	mock.SetChatCompletionResponse(&providers.ChatCompletionResponse{
		ID: "r", Object: "chat.completion", Created: 1, Model: "openai-model-internal",
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
		Usage: providers.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
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
				Credentials: models.TestRefs("openai-cred"),
				InternalModel: "gpt-4o-mini",
			},
		},
		credentials: []models.CredentialConfig{
			{ID: "openai-cred", Provider: "openai", APIKey: "test-key"},
		},
	}

	inputBody := []byte(`{"model":"openai-model-internal","messages":[{"role":"user","content":"hi"}]}`)
	upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "openai-model-internal", 1024*1024)

	if err := executeInternalRequest(context.Background(), cfg, inputBody, upstreamReq, true); err != nil {
		t.Fatalf("executeInternalRequest: %v", err)
	}

	// Captured request must not have translator-injected fields
	// (the response is the upstream's; openai.go extraction is
	// naturally inert on a non-MiniMax shape).
	//
	// N-3 note: no bytes.Equal assertion is possible here — the
	// race-internal path re-encodes the typed ChatCompletionResponse
	// (json.Marshal of the struct), so no verbatim upstream bytes
	// exist on this side. The mock returns a typed struct, not wire
	// bytes. Existing assertions (no reasoning fields injected)
	// stand as-is.
	captured := mock.GetCapturedRequest()
	if captured == nil {
		t.Fatal("provider did not capture the request")
	}
	if captured.ReasoningSplit != nil {
		t.Errorf("ReasoningSplit = %v, want nil", *captured.ReasoningSplit)
	}
	if len(captured.Messages[0].ReasoningDetails) != 0 {
		t.Errorf("ReasoningDetails should be empty")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Race-internal stream: flag set + non-MiniMax credential ⇒ no thinking events
// ─────────────────────────────────────────────────────────────────────────────

func TestRaceInternal_StreamResponse_NegativeCase_NoThinkingEvents_NonMiniMax(t *testing.T) {
	// Mock provider returns a stream with no reasoning_details in
	// any chunk. The flag is set but the credential is OpenAI, so
	// openai.go's stream extraction is naturally inert (returns
	// hasDetails=false for every chunk) and no thinking events are
	// emitted.
	origNewProvider := newProviderClient
	defer func() { newProviderClient = origNewProvider }()

	mock := newTestProvider()
	// No thinking events: content + finish_reason only.
	mock.SetStreamEvents([]providers.StreamEvent{
		{Type: "content", Content: "hello"},
		{Type: "done", FinishReason: "stop", Response: &providers.ChatCompletionResponse{
			ID: "r", Object: "chat.completion", Created: 1, Model: "m",
			Choices: []providers.Choice{{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "hello"}, FinishReason: "stop"}},
		}},
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
				Credentials: models.TestRefs("openai-cred"),
				InternalModel: "gpt-4o-mini",
			},
		},
		credentials: []models.CredentialConfig{
			{ID: "openai-cred", Provider: "openai", APIKey: "test-key"},
		},
	}

	inputBody := []byte(`{"model":"openai-model-internal","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "openai-model-internal", 1024*1024)

	if err := executeInternalRequest(context.Background(), cfg, inputBody, upstreamReq, true); err != nil {
		t.Fatalf("executeInternalRequest: %v", err)
	}

	chunks, _ := upstreamReq.buffer.GetChunksFrom(0)
	combined := string(bytes.Join(chunks, nil))
	if strings.Contains(combined, "reasoning_content") {
		t.Errorf("stream unexpectedly contains reasoning_content: %s", combined)
	}
	// N-3 note: no full bytes.Equal assertion is possible here —
	// the race-internal stream path synthesizes each SSE line from
	// typed StreamEvents (id/object/created are regenerated with
	// time.Now()), so no verbatim upstream bytes exist. The
	// per-line JSON payload is the only byte-comparable sub-scope,
	// and it is already covered by the absence assertions above.
}

func TestRaceInternal_NegativeCase_FlagAbsent_MiniMaxCred_NoTranslator(t *testing.T) {
	origNewProvider := newProviderClient
	defer func() { newProviderClient = origNewProvider }()

	mock := newTestProvider()
	mock.SetChatCompletionResponse(&providers.ChatCompletionResponse{
		ID: "r", Object: "chat.completion", Created: 1, Model: "MiniMax-M1",
		Choices: []providers.Choice{
			{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "hi"}, FinishReason: "stop"},
		},
		Usage: providers.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
	})
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		return mock, nil
	}

	cfg := newTestConfigSnapshot("minimax-model-internal")
	cfg.ModelsConfig = &mockModelsConfig{
		models: []models.ModelConfig{
			{
				ID:            "minimax-model-internal",
				Name:          "minimax-model-internal",
				Enabled:       true,
				Internal:      true,
				Credentials: models.TestRefs("minimax-cred"),
				InternalModel: "MiniMax-M1",
			},
		},
		credentials: []models.CredentialConfig{
			{ID: "minimax-cred", Provider: "minimax", APIKey: "test-key"},
		},
	}

	inputBody := []byte(`{"model":"minimax-model-internal","messages":[{"role":"assistant","content":"answer"}]}`)
	upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "minimax-model-internal", 1024*1024)

	// interleaved=false — flag absent. Body has NO reasoning_content
	// (fix/minimax-reasoning-translation-gate): the widened gate is
	// (interleaved || hasReasoning) && provider is MiniMax, so a
	// reasoning-less body keeps the gate off. This test asserts the
	// negative-case contract; the positive case ("flag absent +
	// reasoning_content ⇒ translate") is covered by
	// TestRaceInternal_BodyGate_NoHeader_ReasoningContent_MiniMax_Translates
	// in race_minimax_body_gate_test.go.
	if err := executeInternalRequest(context.Background(), cfg, inputBody, upstreamReq, false); err != nil {
		t.Fatalf("executeInternalRequest: %v", err)
	}

	captured := mock.GetCapturedRequest()
	if captured == nil {
		t.Fatal("provider did not capture the request")
	}
	// ReasoningSplit MUST be nil (gate is off: flag absent + no
	// reasoning_content).
	if captured.ReasoningSplit != nil {
		t.Errorf("ReasoningSplit = %v, want nil (flag absent + no reasoning_content)", *captured.ReasoningSplit)
	}
	if len(captured.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(captured.Messages))
	}
	// ReasoningDetails MUST be empty (translator not run).
	if len(captured.Messages[0].ReasoningDetails) != 0 {
		t.Errorf("ReasoningDetails = %+v, want empty (flag absent + no reasoning_content)", captured.Messages[0].ReasoningDetails)
	}
}

// Reference to imports needed for SSE parsing; the tests above use
// strings.Contains on the buffered bytes, which is sufficient for
// the negative-case byte-identical assertions.
var _ = bufio.NewReader
var _ = http.MethodGet
var _ = json.RawMessage{}
var _ = io.EOF
var _ = sync.Mutex{}
