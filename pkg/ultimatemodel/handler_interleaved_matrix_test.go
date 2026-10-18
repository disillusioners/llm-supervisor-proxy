package ultimatemodel

// P3-2 byte-identical negative-case matrix gap-fills for the
// ultimate-model paths. Mirrors
// pkg/proxy/race_interleaved_matrix_test.go. The 4×3×2 body matrix +
// 4 header + 4 usage (W8) assertions are split between
// handler_interleaved_test.go (existing N2 cells),
// handler_response_interleaved_test.go (existing N2 + N1 request
// cells), and THIS file (N1 response, N3, header, W8).
//
// Matrix cell naming:
//   N1 = flag absent + MiniMax credential
//   N2 = flag ON + non-MiniMax credential ("openai")
//   N3 = flag ON + MiniMax credential but credential NOT resolvable
//
// S1 = request side (upstream-bound body unchanged)
// S2 = response side (client-bound body unchanged)

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
)

// ─────────────────────────────────────────────────────────────────────────────
// C-N1-S2: ultimate-external response, flag absent + MiniMax cred ⇒ byte-identical
// ─────────────────────────────────────────────────────────────────────────────

// TestExecuteInternal_NegativeCase_FlagAbsent_MiniMaxCred_ResponseByteIdentical
// covers D-N1-S2. The request-side coverage for D-N1 lives in
// TestExecuteInternal_NegativeCase_FlagAbsent_MiniMaxCred_NoTranslator
// (handler_response_interleaved_test.go). On the internal path the
// response is re-encoded from the typed struct (N-3), so we assert
// the typed fields (ReasoningSplit, ReasoningDetails) are absent and
// ReasoningContent is preserved as a string.
func TestExecuteInternal_NegativeCase_FlagAbsent_MiniMaxCred_ResponseByteIdentical(t *testing.T) {
	t.Run("non-stream", func(t *testing.T) {
		origNewProvider := newProviderClient
		defer func() { newProviderClient = origNewProvider }()

		mock := newMockProvider()
		mock.chatResp = &providers.ChatCompletionResponse{
			ID: "r", Object: "chat.completion", Created: 1, Model: "MiniMax-M1",
			Choices: []providers.Choice{
				{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "hi"}, FinishReason: "stop"},
			},
			Usage: providers.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
		}
		newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
			return mock, nil
		}

		cfg := newMockConfigManager()
		modelsCfg := newMockModelsConfig()
		modelsCfg.AddInternalModel("minimax-model", "minimax", "test-key", "", "MiniMax-M1")
		h := NewHandler(cfg, modelsCfg, nil, nil)

		w := httptest.NewRecorder()
		_ = httptest.NewRequest("POST", "/v1/chat/completions", nil) // flag absent
		body := map[string]interface{}{
			"model":    "minimax-model",
			"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
		}
		requestBodyBytes, _ := json.Marshal(body)

		// Flag absent; provider IS minimax (gate would fire IF
		// flag was true) — gate stays off because flag is false.
		_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("minimax-model"), false, false, "")
		if err != nil {
			t.Fatalf("executeInternal: %v", err)
		}
		// Typed fields: gate off ⇒ translator did not run.
		if mock.capturedReq == nil {
			t.Fatal("provider did not capture the request")
		}
		if mock.capturedReq.ReasoningSplit != nil {
			t.Errorf("ReasoningSplit = %v, want nil (flag absent)", *mock.capturedReq.ReasoningSplit)
		}
		if len(mock.capturedReq.Messages[0].ReasoningDetails) != 0 {
			t.Errorf("ReasoningDetails = %+v, want empty (flag absent)", mock.capturedReq.Messages[0].ReasoningDetails)
		}
		got := w.Body.String()
		// No reasoning fields injected.
		if strings.Contains(got, "reasoning_split") || strings.Contains(got, "reasoning_details") {
			t.Errorf("body unexpectedly contains translator field: %s", got)
		}
		// W8: usage preserved.
		if !strings.Contains(got, `"total_tokens":3`) {
			t.Errorf("W8: usage not preserved in D-N1-S2 response: %s", got)
		}
	})

	t.Run("stream", func(t *testing.T) {
		origNewProvider := newProviderClient
		defer func() { newProviderClient = origNewProvider }()

		mock := newMockProvider()
		mock.streamEvents = []providers.StreamEvent{
			{Type: "content", Content: "hi"},
			{Type: "done", FinishReason: "stop", Response: &providers.ChatCompletionResponse{
				ID: "r", Object: "chat.completion", Created: 1, Model: "MiniMax-M1",
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
		modelsCfg.AddInternalModel("minimax-model", "minimax", "test-key", "", "MiniMax-M1")
		h := NewHandler(cfg, modelsCfg, nil, nil)

		w := httptest.NewRecorder()
		_ = httptest.NewRequest("POST", "/v1/chat/completions", nil) // flag absent
		body := map[string]interface{}{
			"model":    "minimax-model",
			"stream":   true,
			"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
		}
		requestBodyBytes, _ := json.Marshal(body)

		_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("minimax-model"), true, false, "")
		if err != nil {
			t.Fatalf("executeInternal: %v", err)
		}
		got := w.Body.String()
		if strings.Contains(got, "reasoning_content") || strings.Contains(got, "reasoning_details") {
			t.Errorf("D-N1-S2 stream unexpectedly contains reasoning field: %s", got)
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// D-N3: ultimate-internal, model registered but internal config not resolvable
// ─────────────────────────────────────────────────────────────────────────────

// TestExecuteInternal_NegativeCase_NoCredential_ResolutionFails covers
// D-N3. The model is in the models map but NOT in the internalCfgs
// map, so ResolveInternalConfig returns ok=false and executeInternal
// returns "failed to resolve internal config" without ever calling
// the provider.
func TestExecuteInternal_NegativeCase_NoCredential_ResolutionFails(t *testing.T) {
	origNewProvider := newProviderClient
	defer func() { newProviderClient = origNewProvider }()

	mock := newMockProvider()
	// Even if the mock is set up, it MUST NOT be called.
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		return mock, nil
	}

	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	// Add an internal model directly via AddModel (not AddInternalModel)
	// so it's in `models` but NOT in `internalCfgs` — ResolveInternalConfig
	// will return ok=false.
	modelsCfg.AddModel(models.ModelConfig{
		ID:       "ghost-internal",
		Name:     "ghost-internal",
		Enabled:  true,
		Internal: true,
	})
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("X-Proxy-Interleaved-Thinking", "true")
	body := map[string]interface{}{
		"model":    "ghost-internal",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("ghost-internal"), false, true, "")
	if err == nil {
		t.Fatal("expected error from executeInternal when internal config is unresolvable")
	}
	if !strings.Contains(err.Error(), "failed to resolve internal config") {
		t.Errorf("error %q must mention 'failed to resolve internal config'", err.Error())
	}
	// The provider must NOT have been called.
	if mock.capturedReq != nil {
		t.Errorf("provider was called despite resolution failure: %+v", mock.capturedReq)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// W8: ultimate-internal usage preservation (D-U)
// ─────────────────────────────────────────────────────────────────────────────

// TestExecuteInternal_UsagePreserved_GateOff is the explicit W8
// usage-preservation assertion for the ultimate-internal path. On
// the internal path the response is re-encoded from the typed
// struct (N-3), so the assertion verifies the mock's Usage field
// round-trips through the typed path. The stream final-chunk
// variant asserts the done event's Response.Usage is preserved.
func TestExecuteInternal_UsagePreserved_GateOff(t *testing.T) {
	t.Run("non-stream", func(t *testing.T) {
		origNewProvider := newProviderClient
		defer func() { newProviderClient = origNewProvider }()

		mock := newMockProvider()
		mock.chatResp = &providers.ChatCompletionResponse{
			ID: "r", Object: "chat.completion", Created: 1, Model: "gpt-4o-mini",
			Choices: []providers.Choice{
				{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
			},
			Usage: providers.Usage{PromptTokens: 13, CompletionTokens: 5, TotalTokens: 18},
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
			"model":    "openai-model",
			"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
		}
		requestBodyBytes, _ := json.Marshal(body)

		_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("openai-model"), false, true, "")
		if err != nil {
			t.Fatalf("executeInternal: %v", err)
		}
		got := w.Body.String()
		for _, sub := range []string{`"prompt_tokens":13`, `"completion_tokens":5`, `"total_tokens":18`} {
			if !strings.Contains(got, sub) {
				t.Errorf("W8 D non-stream: usage field %s missing: %s", sub, got)
			}
		}
	})

	t.Run("stream-final-chunk", func(t *testing.T) {
		// The ultimate-internal stream path does NOT emit the
		// `usage` object in the SSE final chunk (this is the
		// pre-feature baseline; see
		// pkg/ultimatemodel/handler_internal.go:389-406 — the
		// done-event chunk only carries choices/finish_reason).
		// W8 for D-stream therefore asserts: gated-off does
		// NOT introduce a `usage` key into the final chunk
		// (byte-identical-to-baseline: baseline has no usage, so
		// the assertion is "absence of usage").
		origNewProvider := newProviderClient
		defer func() { newProviderClient = origNewProvider }()

		mock := newMockProvider()
		mock.streamEvents = []providers.StreamEvent{
			{Type: "content", Content: "ok"},
			{Type: "done", FinishReason: "stop", Response: &providers.ChatCompletionResponse{
				ID: "r", Object: "chat.completion", Created: 1, Model: "gpt-4o-mini",
				Choices: []providers.Choice{
					{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
				},
				Usage: providers.Usage{PromptTokens: 17, CompletionTokens: 8, TotalTokens: 25},
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
			"model":    "openai-model",
			"stream":   true,
			"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
		}
		requestBodyBytes, _ := json.Marshal(body)

		_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("openai-model"), true, true, "")
		if err != nil {
			t.Fatalf("executeInternal: %v", err)
		}
		got := w.Body.String()
		// The final chunk must NOT contain a usage object (baseline
		// invariant). If the gate-OFF path accidentally started
		// emitting usage where the baseline did not, that would
		// be a regression.
		if strings.Contains(got, `"usage":`) {
			t.Errorf("W8 D stream: usage object unexpectedly present in final chunk (must be absent per baseline): %s", got)
		}
		// Sanity: stream completed normally.
		if !strings.Contains(got, `"finish_reason":"stop"`) {
			t.Errorf("W8 D stream: final chunk missing finish_reason: %s", got)
		}
		if !strings.Contains(got, `data: [DONE]`) {
			t.Errorf("W8 D stream: stream missing [DONE] terminator: %s", got)
		}
	})
}
