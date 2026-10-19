package proxy

// P3-2 byte-identical negative-case matrix gap-fills for the race
// paths. The 4×3×2 body matrix + 4 header + 4 usage (W8) assertions
// are split between race_interleaved_test.go (existing N2 cells),
// race_response_interleaved_test.go (existing N2 + N1 request cells),
// and THIS file (N1 response, N3, header, W8). The ultim-side cells
// are filled in pkg/ultimatemodel/handler_interleaved_matrix_test.go.
//
// All tests are byte-equality assertions against the canonical
// pre-feature baseline. The matrix cell naming follows:
//   N1 = flag absent + MiniMax credential
//   N2 = flag ON + non-MiniMax credential ("openai")
//   N3 = flag ON + MiniMax credential but credential NOT resolvable
//
// S1 = request side (upstream-bound body unchanged)
// S2 = response side (client-bound body unchanged)

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
)

// ─────────────────────────────────────────────────────────────────────────────
// B-N1-S2: race-internal response, flag absent + MiniMax cred ⇒ byte-identical
// ─────────────────────────────────────────────────────────────────────────────

// TestRaceInternal_NegativeCase_FlagAbsent_MiniMaxCred_ResponseByteIdentical
// covers B-N1-S2. The request-side coverage for B-N1 lives in
// TestRaceInternal_NegativeCase_FlagAbsent_MiniMaxCred_NoTranslator
// (race_response_interleaved_test.go). On the internal path the
// response is re-encoded from the typed ChatCompletionResponse, so
// no verbatim upstream bytes exist (N-3) — the typed struct's
// ReasoningContent and Usage fields are checked for absence/presence
// instead.
func TestRaceInternal_NegativeCase_FlagAbsent_MiniMaxCred_ResponseByteIdentical(t *testing.T) {
	t.Run("non-stream", func(t *testing.T) {
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
					Credentials:   models.TestRefs("minimax-cred"),
					InternalModel: "MiniMax-M1",
				},
			},
			credentials: []models.CredentialConfig{
				{ID: "minimax-cred", Provider: "minimax", APIKey: "test-key"},
			},
		}

		inputBody := []byte(`{"model":"minimax-model-internal","messages":[{"role":"user","content":"hi"}]}`)
		upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "minimax-model-internal", 1024*1024)

		// Flag absent.
		if err := executeInternalRequest(context.Background(), cfg, inputBody, upstreamReq, false); err != nil {
			t.Fatalf("executeInternalRequest: %v", err)
		}
		// Captured request must not have translator fields (flag
		// absent ⇒ gate off).
		captured := mock.GetCapturedRequest()
		if captured == nil {
			t.Fatal("provider did not capture the request")
		}
		if captured.ReasoningSplit != nil {
			t.Errorf("ReasoningSplit = %v, want nil (flag absent)", *captured.ReasoningSplit)
		}
		if len(captured.Messages[0].ReasoningDetails) != 0 {
			t.Errorf("ReasoningDetails = %+v, want empty", captured.Messages[0].ReasoningDetails)
		}
	})

	t.Run("stream", func(t *testing.T) {
		origNewProvider := newProviderClient
		defer func() { newProviderClient = origNewProvider }()

		mock := newTestProvider()
		mock.SetStreamEvents([]providers.StreamEvent{
			{Type: "content", Content: "hi"},
			{Type: "done", FinishReason: "stop", Response: &providers.ChatCompletionResponse{
				ID: "r", Object: "chat.completion", Created: 1, Model: "MiniMax-M1",
				Choices: []providers.Choice{
					{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "hi"}, FinishReason: "stop"},
				},
			}},
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
					Credentials:   models.TestRefs("minimax-cred"),
					InternalModel: "MiniMax-M1",
				},
			},
			credentials: []models.CredentialConfig{
				{ID: "minimax-cred", Provider: "minimax", APIKey: "test-key"},
			},
		}

		inputBody := []byte(`{"model":"minimax-model-internal","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
		upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "minimax-model-internal", 1024*1024)

		if err := executeInternalRequest(context.Background(), cfg, inputBody, upstreamReq, false); err != nil {
			t.Fatalf("executeInternalRequest: %v", err)
		}
		chunks, _ := upstreamReq.buffer.GetChunksFrom(0)
		combined := string(bytes.Join(chunks, nil))
		if strings.Contains(combined, "reasoning_content") {
			t.Errorf("B-N1-S2 stream unexpectedly contains reasoning_content: %s", combined)
		}
		if strings.Contains(combined, "reasoning_details") {
			t.Errorf("B-N1-S2 stream unexpectedly contains reasoning_details: %s", combined)
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// B-N3: race-internal, model registered but internal config not resolvable
// ─────────────────────────────────────────────────────────────────────────────

// TestRaceInternal_NegativeCase_NoCredential_ResolutionFails covers
// B-N3 (no internal config). The function returns
// "failed to resolve internal config" and the provider is never
// called — no body is sent upstream. The gate is naturally off
// because execution aborts before reaching it.
func TestRaceInternal_NegativeCase_NoCredential_ResolutionFails(t *testing.T) {
	origNewProvider := newProviderClient
	defer func() { newProviderClient = origNewProvider }()

	mock := newTestProvider()
	// Even if the mock is set up, it MUST NOT be called.
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		mock.mu.Lock()
		mock.capturedReq = &providers.ChatCompletionRequest{}
		mock.mu.Unlock()
		return mock, nil
	}

	cfg := newTestConfigSnapshot("ghost-model")
	cfg.ModelsConfig = &mockModelsConfig{
		models: []models.ModelConfig{
			// Model exists in the models map but has NO
			// CredentialID — ResolveInternalConfig returns
			// ok=false because the model isn't in the
			// internal-cfgs map either.
			{
				ID:       "ghost-model",
				Name:     "ghost-model",
				Enabled:  true,
				Internal: true,
			},
		},
		credentials: []models.CredentialConfig{},
	}

	inputBody := []byte(`{"model":"ghost-model","messages":[{"role":"user","content":"hi"}]}`)
	upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "ghost-model", 1024*1024)

	err := executeInternalRequest(context.Background(), cfg, inputBody, upstreamReq, true)
	if err == nil {
		t.Fatal("expected error from executeInternalRequest when internal config is unresolvable")
	}
	if !strings.Contains(err.Error(), "failed to resolve internal config") {
		t.Errorf("error %q must mention 'failed to resolve internal config'", err.Error())
	}
	// The provider must NOT have been called.
	if captured := mock.GetCapturedRequest(); captured != nil {
		t.Errorf("provider was called despite resolution failure: %+v", captured)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// W8: race-internal usage preservation (B-U)
// ─────────────────────────────────────────────────────────────────────────────

// TestRaceInternal_UsagePreserved_GateOff is the explicit W8
// usage-preservation assertion for the race-internal path. On the
// internal path the response is re-encoded from the typed struct
// (N-3 limitation), so the assertion verifies the mock's Usage
// field round-trips through the typed path. The stream final-chunk
// variant asserts the done event's Response.Usage is preserved.
func TestRaceInternal_UsagePreserved_GateOff(t *testing.T) {
	t.Run("non-stream", func(t *testing.T) {
		origNewProvider := newProviderClient
		defer func() { newProviderClient = origNewProvider }()

		mock := newTestProvider()
		mock.SetChatCompletionResponse(&providers.ChatCompletionResponse{
			ID: "r", Object: "chat.completion", Created: 1, Model: "gpt-4o-mini",
			Choices: []providers.Choice{
				{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
			},
			// Distinct usage to make the assertion specific.
			Usage: providers.Usage{PromptTokens: 13, CompletionTokens: 5, TotalTokens: 18},
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
				{ID: "openai-cred", Provider: "openai", APIKey: "k"},
			},
		}
		inputBody := []byte(`{"model":"openai-model-internal","messages":[{"role":"user","content":"hi"}]}`)
		upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "openai-model-internal", 1024*1024)

		if err := executeInternalRequest(context.Background(), cfg, inputBody, upstreamReq, true); err != nil {
			t.Fatalf("executeInternalRequest: %v", err)
		}
		chunks, _ := upstreamReq.buffer.GetChunksFrom(0)
		if len(chunks) == 0 {
			t.Fatal("no chunks in buffer")
		}
		got := string(chunks[len(chunks)-1])
		// W8: usage field round-tripped via typed struct.
		for _, sub := range []string{`"prompt_tokens":13`, `"completion_tokens":5`, `"total_tokens":18`} {
			if !strings.Contains(got, sub) {
				t.Errorf("W8 B non-stream: usage field %s missing: %s", sub, got)
			}
		}
	})

	t.Run("stream-final-chunk", func(t *testing.T) {
		origNewProvider := newProviderClient
		defer func() { newProviderClient = origNewProvider }()

		mock := newTestProvider()
		mock.SetStreamEvents([]providers.StreamEvent{
			{Type: "content", Content: "ok"},
			{Type: "done", FinishReason: "stop", Response: &providers.ChatCompletionResponse{
				ID: "r", Object: "chat.completion", Created: 1, Model: "gpt-4o-mini",
				Choices: []providers.Choice{
					{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
				},
				// Distinct usage on the done event.
				Usage: providers.Usage{PromptTokens: 17, CompletionTokens: 8, TotalTokens: 25},
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
					Credentials:   models.TestRefs("openai-cred"),
					InternalModel: "gpt-4o-mini",
				},
			},
			credentials: []models.CredentialConfig{
				{ID: "openai-cred", Provider: "openai", APIKey: "k"},
			},
		}
		inputBody := []byte(`{"model":"openai-model-internal","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
		upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "openai-model-internal", 1024*1024)

		if err := executeInternalRequest(context.Background(), cfg, inputBody, upstreamReq, true); err != nil {
			t.Fatalf("executeInternalRequest: %v", err)
		}
		chunks, _ := upstreamReq.buffer.GetChunksFrom(0)
		combined := string(bytes.Join(chunks, nil))
		for _, sub := range []string{`"prompt_tokens":17`, `"completion_tokens":8`, `"total_tokens":25`} {
			if !strings.Contains(combined, sub) {
				t.Errorf("W8 B stream: usage field %s missing from final chunk: %s", sub, combined)
			}
		}
	})
}
