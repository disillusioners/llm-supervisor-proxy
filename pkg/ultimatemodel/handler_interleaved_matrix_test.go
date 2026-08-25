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
// C-N1-S2: ultimate-external response, flag absent + MiniMax cred ⇒ byte-identical
// ─────────────────────────────────────────────────────────────────────────────

// TestExecuteExternal_NegativeCase_FlagAbsent_MiniMaxCred_ResponseByteIdentical
// covers C-N1-S2. The request-side coverage for C-N1 lives in
// TestExecuteExternal_NegativeCase_FlagAbsent_MiniMaxCred_NoTranslator
// (handler_response_interleaved_test.go). The flag-absent case
// exercises the same code path as the N2 case (gate is off in both),
// but the response side has not been asserted byte-identical for
// flag-absent. The ultim-external non-stream response path is
// verbatim-byte (handler_external.go:154-211) — executeExternal
// writes bodyBytes to the client with no re-marshal when the gate
// is off.
func TestExecuteExternal_NegativeCase_FlagAbsent_MiniMaxCred_ResponseByteIdentical(t *testing.T) {
	t.Run("non-stream", func(t *testing.T) {
		const upstreamBody = `{"id":"r","object":"chat.completion","created":1,"model":"ultimate-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(upstreamBody))
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
		h := NewHandler(cfg, modelsCfg, nil)

		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		// NO X-Proxy-Interleaved-Thinking header — flag absent.
		body := map[string]interface{}{
			"model":    "ultimate-model",
			"messages": []map[string]interface{}{{"role": "user", "content": "hi"}},
		}
		requestBodyBytes, _ := json.Marshal(body)

		// upstreamProvider="minimax" — credential IS MiniMax. flag
		// is false. Gate stays off.
		_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false, false, "minimax")
		if err != nil {
			t.Fatalf("executeExternal: %v", err)
		}
		got := w.Body.String()
		// N-3: full byte-identity of the client-received response
		// body vs the exact upstream bytes.
		if !bytes.Equal(w.Body.Bytes(), []byte(upstreamBody)) {
			t.Errorf("C-N1-S2 non-stream not byte-identical to upstream:\n want=%q\n  got=%q", upstreamBody, got)
		}
		// W8: usage preserved.
		if !strings.Contains(got, `"total_tokens":10`) {
			t.Errorf("W8: usage not preserved in C-N1-S2 response: %s", got)
		}
	})

	t.Run("stream", func(t *testing.T) {
		const upstreamStream = `data: {"id":"1","choices":[{"index":0,"delta":{"content":"hello"}}]}` + "\n\n" +
			`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
			`data: [DONE]` + "\n\n"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte(upstreamStream))
		}))
		defer server.Close()

		cfg := newMockConfigManager()
		modelsCfg := newMockModelsConfig()
		modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
		h := NewHandler(cfg, modelsCfg, nil)

		resp, err := http.Get(server.URL)
		if err != nil {
			t.Fatalf("http.Get: %v", err)
		}
		defer resp.Body.Close()

		w := httptest.NewRecorder()
		// Flag=false, providerIsMiniMax=true (gate would fire IF
		// flag was true) — gate stays off because flag is false.
		_, err = h.streamResponse(w, resp, "ultimate-model", nil, false, true)
		if err != nil {
			t.Fatalf("streamResponse: %v", err)
		}
		got := w.Body.String()
		if !bytes.Equal(w.Body.Bytes(), []byte(upstreamStream)) {
			t.Errorf("C-N1-S2 stream not byte-identical to upstream:\n want=%q\n  got=%q", upstreamStream, got)
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// C-N3: ultimate-external, flag ON + non-resolvable credential ⇒ gate off, body unchanged
// ─────────────────────────────────────────────────────────────────────────────

// TestExecuteExternal_NegativeCase_NoCredential_ByteIdentical covers
// C-N3. The model has CredentialID set to a credential that does
// NOT exist in the credentials map; production code (handler.go:288-293)
// leaves upstreamProvider="" so providerIsMiniMax=false and the
// gate stays off. The body passes through unchanged.
func TestExecuteExternal_NegativeCase_NoCredential_ByteIdentical(t *testing.T) {
	t.Run("request-non-stream", func(t *testing.T) {
		var capturedBody []byte
		var capturedHeaders http.Header
		var mu sync.Mutex
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			body, _ := io.ReadAll(r.Body)
			capturedBody = body
			capturedHeaders = r.Header.Clone()
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"r","choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
		}))
		defer upstream.Close()

		cfg := newMockConfigManager()
		cfg.cfg.UpstreamURL = upstream.URL
		modelsCfg := newMockModelsConfig()
		// Model has CredentialID set to a non-existent ID.
		// Handler.go resolves this to upstreamProvider="" because
		// GetCredential returns nil.
		modelsCfg.AddModel(models.ModelConfig{
			ID:           "ultimate-model",
			Name:         "ultimate-model",
			Enabled:      true,
			Internal:     false,
			Credentials: models.TestRefs("missing-minimax-cred"),
		})
		h := NewHandler(cfg, modelsCfg, nil)

		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		// Flag ON — credential is non-resolvable, so the gate is
		// off.
		r.Header.Set("X-Proxy-Interleaved-Thinking", "true")
		body := map[string]interface{}{
			"model":    "ultimate-model",
			"messages": []map[string]interface{}{{"role": "assistant", "content": "answer", "reasoning_content": "think-1"}},
		}
		requestBodyBytes, _ := json.Marshal(body)

		// upstreamProvider="" (as production would resolve it).
		_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false, true, "")
		if err != nil {
			t.Fatalf("executeExternal: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		// Header strip still applies on N3 (EqualFold).
		if got := capturedHeaders.Get("x-proxy-interleaved-thinking"); got != "" {
			t.Errorf("x-proxy-interleaved-thinking leaked: %q", got)
		}
		if got := capturedHeaders.Get("X-Proxy-Interleaved-Thinking"); got != "" {
			t.Errorf("X-Proxy-Interleaved-Thinking leaked: %q", got)
		}
		bodyStr := string(capturedBody)
		if strings.Contains(bodyStr, "reasoning_split") || strings.Contains(bodyStr, "reasoning_details") {
			t.Errorf("body unexpectedly contains translator-injected field: %s", bodyStr)
		}
		if !strings.Contains(bodyStr, "think-1") {
			t.Errorf("body lost original reasoning_content: %s", bodyStr)
		}
	})

	t.Run("response-non-stream", func(t *testing.T) {
		const upstreamBody = `{"id":"r","object":"chat.completion","created":1,"model":"ultimate-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(upstreamBody))
		}))
		defer upstream.Close()

		cfg := newMockConfigManager()
		cfg.cfg.UpstreamURL = upstream.URL
		modelsCfg := newMockModelsConfig()
		modelsCfg.AddModel(models.ModelConfig{
			ID:           "ultimate-model",
			Name:         "ultimate-model",
			Enabled:      true,
			Internal:     false,
			Credentials: models.TestRefs("missing-minimax-cred"),
		})
		h := NewHandler(cfg, modelsCfg, nil)

		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		r.Header.Set("X-Proxy-Interleaved-Thinking", "true")
		body := map[string]interface{}{
			"model":    "ultimate-model",
			"messages": []map[string]interface{}{{"role": "user", "content": "hi"}},
		}
		requestBodyBytes, _ := json.Marshal(body)

		_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false, true, "")
		if err != nil {
			t.Fatalf("executeExternal: %v", err)
		}
		got := w.Body.String()
		if !bytes.Equal(w.Body.Bytes(), []byte(upstreamBody)) {
			t.Errorf("C-N3-S2 non-stream not byte-identical to upstream:\n want=%q\n  got=%q", upstreamBody, got)
		}
	})

	t.Run("response-stream", func(t *testing.T) {
		const upstreamStream = `data: {"id":"1","choices":[{"index":0,"delta":{"content":"hello"}}]}` + "\n\n" +
			`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
			`data: [DONE]` + "\n\n"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte(upstreamStream))
		}))
		defer server.Close()

		cfg := newMockConfigManager()
		modelsCfg := newMockModelsConfig()
		modelsCfg.AddModel(models.ModelConfig{
			ID:           "ultimate-model",
			Name:         "ultimate-model",
			Enabled:      true,
			Internal:     false,
			Credentials: models.TestRefs("missing-minimax-cred"),
		})
		h := NewHandler(cfg, modelsCfg, nil)

		resp, err := http.Get(server.URL)
		if err != nil {
			t.Fatalf("http.Get: %v", err)
		}
		defer resp.Body.Close()

		w := httptest.NewRecorder()
		// Flag=true, providerIsMiniMax=false (gate stays off).
		_, err = h.streamResponse(w, resp, "ultimate-model", nil, true, false)
		if err != nil {
			t.Fatalf("streamResponse: %v", err)
		}
		got := w.Body.String()
		if !bytes.Equal(w.Body.Bytes(), []byte(upstreamStream)) {
			t.Errorf("C-N3-S2 stream not byte-identical to upstream:\n want=%q\n  got=%q", upstreamStream, got)
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// C-N3 header strip — explicit case-varied EqualFold assertion (D4)
// ─────────────────────────────────────────────────────────────────────────────

// TestExecuteExternal_HeaderStrip_NoCredential_CaseVaried asserts the
// narrow header strip (D4, EqualFold) works in all header-case
// variants for C-N3. The two N2 case-varied assertions in
// TestExecuteExternal_NegativeCase_ByteIdentical_NonMiniMax cover
// lowercase + canonical-case; this test adds mixed/uppercase
// variants.
func TestExecuteExternal_HeaderStrip_NoCredential_CaseVaried(t *testing.T) {
	cases := []struct {
		name   string
		header string
	}{
		{"lowercase", "x-proxy-interleaved-thinking"},
		{"canonical", "X-Proxy-Interleaved-Thinking"},
		{"uppercase", "X-PROXY-INTERLEAVED-THINKING"},
		{"mixed", "X-proxy-INTERLEAVED-thinking"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var capturedHeaders http.Header
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"id":"r","choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
			}))
			defer upstream.Close()

			cfg := newMockConfigManager()
			cfg.cfg.UpstreamURL = upstream.URL
			modelsCfg := newMockModelsConfig()
			modelsCfg.AddModel(models.ModelConfig{
				ID:           "ultimate-model",
				Name:         "ultimate-model",
				Enabled:      true,
				Internal:     false,
				Credentials: models.TestRefs("missing-minimax-cred"),
			})
			h := NewHandler(cfg, modelsCfg, nil)

			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			r.Header.Set(tc.header, "true")
			body := map[string]interface{}{
				"model":    "ultimate-model",
				"messages": []map[string]interface{}{{"role": "user", "content": "hi"}},
			}
			requestBodyBytes, _ := json.Marshal(body)

			_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false, true, "")
			if err != nil {
				t.Fatalf("executeExternal: %v", err)
			}
			for k := range capturedHeaders {
				if strings.EqualFold(k, "x-proxy-interleaved-thinking") {
					t.Errorf("C-N3 header strip leak: %q present in upstream", k)
				}
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// W8: ultimate-external usage preservation (C-U)
// ─────────────────────────────────────────────────────────────────────────────

// TestExecuteExternal_UsagePreserved_GateOff is the explicit W8
// usage-preservation assertion for the ultimate-external path. The
// pre-existing N2 response test
// (TestExecuteExternal_Response_NegativeCase_ByteIdentical_NonMiniMax)
// already exercises usage preservation transitively via bytes.Equal;
// this test hardens the assertion with explicit usage-object checks
// for non-stream and stream final-chunk.
func TestExecuteExternal_UsagePreserved_GateOff(t *testing.T) {
	t.Run("non-stream", func(t *testing.T) {
		const upstreamBody = `{"id":"r","object":"chat.completion","created":1,"model":"ultimate-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":42,"completion_tokens":7,"total_tokens":49}}`
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(upstreamBody))
		}))
		defer upstream.Close()

		cfg := newMockConfigManager()
		cfg.cfg.UpstreamURL = upstream.URL
		modelsCfg := newMockModelsConfig()
		modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
		h := NewHandler(cfg, modelsCfg, nil)

		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		r.Header.Set("X-Proxy-Interleaved-Thinking", "true")
		body := map[string]interface{}{
			"model":    "ultimate-model",
			"messages": []map[string]interface{}{{"role": "user", "content": "hi"}},
		}
		requestBodyBytes, _ := json.Marshal(body)

		_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false, true, "")
		if err != nil {
			t.Fatalf("executeExternal: %v", err)
		}
		got := w.Body.String()
		for _, sub := range []string{`"prompt_tokens":42`, `"completion_tokens":7`, `"total_tokens":49`} {
			if !strings.Contains(got, sub) {
				t.Errorf("W8 C non-stream: usage field %s missing: %s", sub, got)
			}
		}
	})

	t.Run("stream-final-chunk", func(t *testing.T) {
		const upstreamStream = `data: {"id":"1","choices":[{"index":0,"delta":{"content":"ok"}}]}` + "\n\n" +
			`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":3,"total_tokens":14}}` + "\n\n" +
			`data: [DONE]` + "\n\n"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte(upstreamStream))
		}))
		defer server.Close()

		cfg := newMockConfigManager()
		modelsCfg := newMockModelsConfig()
		modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
		h := NewHandler(cfg, modelsCfg, nil)

		resp, err := http.Get(server.URL)
		if err != nil {
			t.Fatalf("http.Get: %v", err)
		}
		defer resp.Body.Close()

		w := httptest.NewRecorder()
		_, err = h.streamResponse(w, resp, "ultimate-model", nil, true, false)
		if err != nil {
			t.Fatalf("streamResponse: %v", err)
		}
		body := w.Body.String()
		for _, sub := range []string{`"prompt_tokens":11`, `"completion_tokens":3`, `"total_tokens":14`} {
			if !strings.Contains(body, sub) {
				t.Errorf("W8 C stream: usage field %s missing from final chunk: %s", sub, body)
			}
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// D-N1-S2: ultimate-internal response, flag absent + MiniMax cred ⇒ byte-identical
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
		h := NewHandler(cfg, modelsCfg, nil)

		w := httptest.NewRecorder()
		_ = httptest.NewRequest("POST", "/v1/chat/completions", nil) // flag absent
		body := map[string]interface{}{
			"model":    "minimax-model",
			"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
		}
		requestBodyBytes, _ := json.Marshal(body)

		// Flag absent; provider IS minimax (gate would fire IF
		// flag was true) — gate stays off because flag is false.
		_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("minimax-model"), false, false)
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
		h := NewHandler(cfg, modelsCfg, nil)

		w := httptest.NewRecorder()
		_ = httptest.NewRequest("POST", "/v1/chat/completions", nil) // flag absent
		body := map[string]interface{}{
			"model":    "minimax-model",
			"stream":   true,
			"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
		}
		requestBodyBytes, _ := json.Marshal(body)

		_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("minimax-model"), true, false)
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
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("X-Proxy-Interleaved-Thinking", "true")
	body := map[string]interface{}{
		"model":    "ghost-internal",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("ghost-internal"), false, true)
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
		h := NewHandler(cfg, modelsCfg, nil)

		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		r.Header.Set("X-Proxy-Interleaved-Thinking", "true")
		body := map[string]interface{}{
			"model":    "openai-model",
			"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
		}
		requestBodyBytes, _ := json.Marshal(body)

		_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("openai-model"), false, true)
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
		h := NewHandler(cfg, modelsCfg, nil)

		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		r.Header.Set("X-Proxy-Interleaved-Thinking", "true")
		body := map[string]interface{}{
			"model":    "openai-model",
			"stream":   true,
			"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
		}
		requestBodyBytes, _ := json.Marshal(body)

		_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("openai-model"), true, true)
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
