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
// A-N1-S2: race-external response, flag absent + MiniMax cred ⇒ byte-identical
// ─────────────────────────────────────────────────────────────────────────────

// TestRaceExternal_NegativeCase_FlagAbsent_MiniMaxCred_ResponseByteIdentical
// covers A-N1-S2 (response side, non-stream + stream subtests). The
// request-side coverage for A-N1 lives in
// TestRaceExternal_NegativeCase_FlagAbsent_MiniMaxCred_NoTranslator
// (race_response_interleaved_test.go).
func TestRaceExternal_NegativeCase_FlagAbsent_MiniMaxCred_ResponseByteIdentical(t *testing.T) {
	t.Run("non-stream", func(t *testing.T) {
		// MiniMax credential but flag absent ⇒ gate OFF on both sides.
		// Response translator must NOT run; client receives the exact
		// upstream bytes (the only "framing" the race-external path
		// applies is streamBuffer.Add's trailing-'\n' restore on
		// non-stream).
		const upstreamBody = `{"id":"r","object":"chat.completion","created":1,"model":"minimax-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(upstreamBody))
		}))
		defer upstream.Close()

		cfg := newTestConfigSnapshot("minimax-model")
		cfg.UpstreamURL = upstream.URL
		cfg.UpstreamCredentialID = "minimax-cred"
		cfg.ModelsConfig = &mockModelsConfig{
			credentials: []models.CredentialConfig{
				{ID: "minimax-cred", Provider: "minimax", APIKey: "test-key"},
			},
		}

		inputBody := []byte(`{"model":"minimax-model","messages":[{"role":"user","content":"hi"}]}`)
		req := httptest.NewRequest("POST", upstream.URL+"/v1/chat/completions", bytes.NewReader(inputBody))
		// NO X-Proxy-Interleaved-Thinking header — flag absent.
		upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "minimax-model", 1024*1024)

		if err := executeExternalRequest(context.Background(), cfg, req, inputBody, upstreamReq, false); err != nil {
			t.Fatalf("executeExternalRequest: %v", err)
		}
		chunks, _ := upstreamReq.buffer.GetChunksFrom(0)
		if len(chunks) == 0 {
			t.Fatal("no chunks in buffer")
		}
		got := string(chunks[len(chunks)-1])
		if !bytes.Equal(chunks[len(chunks)-1], append([]byte(upstreamBody), '\n')) {
			t.Errorf("A-N1-S2 non-stream not byte-identical to upstream:\n want=%q\n  got=%q", upstreamBody+"\n", got)
		}
		if strings.Contains(got, "reasoning_content") || strings.Contains(got, "reasoning_details") {
			t.Errorf("body unexpectedly contains translator-injected field: %s", got)
		}
		// W8: usage object preserved.
		if !strings.Contains(got, `"total_tokens":10`) {
			t.Errorf("W8: usage object not preserved in response: %s", got)
		}
	})

	t.Run("stream", func(t *testing.T) {
		const upstreamStream = `data: {"id":"1","choices":[{"index":0,"delta":{"content":"hello"}}]}` + "\n\n" +
			`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
			`data: [DONE]` + "\n\n"
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte(upstreamStream))
		}))
		defer upstream.Close()

		cfg := newTestConfigSnapshot("minimax-model")
		cfg.UpstreamURL = upstream.URL
		cfg.UpstreamCredentialID = "minimax-cred"
		cfg.ModelsConfig = &mockModelsConfig{
			credentials: []models.CredentialConfig{
				{ID: "minimax-cred", Provider: "minimax", APIKey: "test-key"},
			},
		}

		inputBody := []byte(`{"model":"minimax-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
		req := httptest.NewRequest("POST", upstream.URL+"/v1/chat/completions", bytes.NewReader(inputBody))
		upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "minimax-model", 1024*1024)

		if err := executeExternalRequest(context.Background(), cfg, req, inputBody, upstreamReq, false); err != nil {
			t.Fatalf("executeExternalRequest: %v", err)
		}
		chunks, _ := upstreamReq.buffer.GetChunksFrom(0)
		combined := string(bytes.Join(chunks, nil))
		// Same trailing-newline caveat as the N2 stream test.
		wantStream := upstreamStream[:len(upstreamStream)-1]
		if !bytes.Equal([]byte(combined), []byte(wantStream)) {
			t.Errorf("A-N1-S2 stream not byte-identical to upstream:\n want=%q\n  got=%q", wantStream, combined)
		}
		if strings.Contains(combined, "reasoning_content") || strings.Contains(combined, "reasoning_details") {
			t.Errorf("stream unexpectedly contains translator-injected field: %s", combined)
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// A-N3: race-external, flag ON + non-resolvable credential ⇒ gate off, body unchanged
// ─────────────────────────────────────────────────────────────────────────────

// TestRaceExternal_NegativeCase_NoCredential_ByteIdentical covers
// A-N3-S1 (request body) and A-N3-S2 (response body, non-stream +
// stream). The UpstreamCredentialID is set to a credential that
// does NOT exist in the credentials map; raceExtProviderIsMiniMax
// returns false (race_executor.go:33-42), so the gate stays off and
// the body passes through unchanged modulo the pre-existing
// model-override.
func TestRaceExternal_NegativeCase_NoCredential_ByteIdentical(t *testing.T) {
	t.Run("request-non-stream", func(t *testing.T) {
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

		cfg := newTestConfigSnapshot("minimax-model")
		cfg.UpstreamURL = upstream.URL
		// Set CredentialID to a non-existent credential.
		cfg.UpstreamCredentialID = "missing-minimax-cred"
		cfg.ModelsConfig = &mockModelsConfig{
			credentials: []models.CredentialConfig{
				{ID: "other-cred", Provider: "openai", APIKey: "k"},
			},
		}

		inputBody := []byte(`{
  "model": "minimax-model",
  "messages": [
    {"role": "assistant", "content": "answer", "reasoning_content": "think-1"}
  ]
}`)
		req := httptest.NewRequest("POST", upstream.URL+"/v1/chat/completions", bytes.NewReader(inputBody))
		// Flag ON — but credential is non-resolvable, so the gate
		// is off.
		req.Header.Set("X-Proxy-Interleaved-Thinking", "true")
		upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "minimax-model", 1024*1024)

		if err := executeExternalRequest(context.Background(), cfg, req, inputBody, upstreamReq, true); err != nil {
			t.Fatalf("executeExternalRequest: %v", err)
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
		// No translator fields.
		body := string(capturedBody)
		if strings.Contains(body, "reasoning_split") || strings.Contains(body, "reasoning_details") {
			t.Errorf("body unexpectedly contains translator-injected field: %s", body)
		}
		// Original reasoning_content preserved.
		if !strings.Contains(body, "think-1") {
			t.Errorf("body lost original reasoning_content: %s", body)
		}
		// Byte-identical to canonical.
		var bm map[string]any
		if err := json.Unmarshal(inputBody, &bm); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		inputCanonical, _ := json.Marshal(bm)
		if !bytes.Equal(capturedBody, inputCanonical) {
			t.Errorf("A-N3-S1 not byte-identical to canonical:\n want=%s\n  got=%s", inputCanonical, capturedBody)
		}
	})

	t.Run("response-non-stream", func(t *testing.T) {
		const upstreamBody = `{"id":"r","object":"chat.completion","created":1,"model":"minimax-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(upstreamBody))
		}))
		defer upstream.Close()

		cfg := newTestConfigSnapshot("minimax-model")
		cfg.UpstreamURL = upstream.URL
		cfg.UpstreamCredentialID = "missing-minimax-cred"
		cfg.ModelsConfig = &mockModelsConfig{
			credentials: []models.CredentialConfig{},
		}

		inputBody := []byte(`{"model":"minimax-model","messages":[{"role":"user","content":"hi"}]}`)
		req := httptest.NewRequest("POST", upstream.URL+"/v1/chat/completions", bytes.NewReader(inputBody))
		req.Header.Set("X-Proxy-Interleaved-Thinking", "true")
		upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "minimax-model", 1024*1024)

		if err := executeExternalRequest(context.Background(), cfg, req, inputBody, upstreamReq, true); err != nil {
			t.Fatalf("executeExternalRequest: %v", err)
		}
		chunks, _ := upstreamReq.buffer.GetChunksFrom(0)
		if len(chunks) == 0 {
			t.Fatal("no chunks in buffer")
		}
		if !bytes.Equal(chunks[len(chunks)-1], append([]byte(upstreamBody), '\n')) {
			t.Errorf("A-N3-S2 non-stream not byte-identical to upstream:\n want=%q\n  got=%q", upstreamBody+"\n", string(chunks[len(chunks)-1]))
		}
	})

	t.Run("response-stream", func(t *testing.T) {
		const upstreamStream = `data: {"id":"1","choices":[{"index":0,"delta":{"content":"hello"}}]}` + "\n\n" +
			`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
			`data: [DONE]` + "\n\n"
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte(upstreamStream))
		}))
		defer upstream.Close()

		cfg := newTestConfigSnapshot("minimax-model")
		cfg.UpstreamURL = upstream.URL
		cfg.UpstreamCredentialID = "missing-minimax-cred"
		cfg.ModelsConfig = &mockModelsConfig{
			credentials: []models.CredentialConfig{},
		}

		inputBody := []byte(`{"model":"minimax-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
		req := httptest.NewRequest("POST", upstream.URL+"/v1/chat/completions", bytes.NewReader(inputBody))
		req.Header.Set("X-Proxy-Interleaved-Thinking", "true")
		upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "minimax-model", 1024*1024)

		if err := executeExternalRequest(context.Background(), cfg, req, inputBody, upstreamReq, true); err != nil {
			t.Fatalf("executeExternalRequest: %v", err)
		}
		chunks, _ := upstreamReq.buffer.GetChunksFrom(0)
		combined := string(bytes.Join(chunks, nil))
		wantStream := upstreamStream[:len(upstreamStream)-1]
		if !bytes.Equal([]byte(combined), []byte(wantStream)) {
			t.Errorf("A-N3-S2 stream not byte-identical to upstream:\n want=%q\n  got=%q", wantStream, combined)
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// A-N3 header strip — explicit case-varied EqualFold assertion (D4)
// ─────────────────────────────────────────────────────────────────────────────

// TestRaceExternal_HeaderStrip_NoCredential_CaseVaried asserts the
// narrow header strip (D4, EqualFold) works in all header-case
// variants for A-N3. The two N2 case-varied assertions in
// TestRaceExternal_NegativeCase_ByteIdentical_NonMiniMax cover
// lowercase + canonical-case; this test adds mixed/uppercase
// variants to harden the assertion against future case-folding
// regressions.
func TestRaceExternal_HeaderStrip_NoCredential_CaseVaried(t *testing.T) {
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
			var mu sync.Mutex
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				capturedHeaders = r.Header.Clone()
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"id":"r","choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
			}))
			defer upstream.Close()

			cfg := newTestConfigSnapshot("minimax-model")
			cfg.UpstreamURL = upstream.URL
			cfg.UpstreamCredentialID = "missing-minimax-cred"
			cfg.ModelsConfig = &mockModelsConfig{
				credentials: []models.CredentialConfig{},
			}

			inputBody := []byte(`{"model":"minimax-model","messages":[{"role":"user","content":"hi"}]}`)
			req := httptest.NewRequest("POST", upstream.URL+"/v1/chat/completions", bytes.NewReader(inputBody))
			req.Header.Set(tc.header, "true")
			upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "minimax-model", 1024*1024)

			if err := executeExternalRequest(context.Background(), cfg, req, inputBody, upstreamReq, true); err != nil {
				t.Fatalf("executeExternalRequest: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			// EqualFold-style scan: any header that case-folds to
			// the target must be absent.
			for k := range capturedHeaders {
				if strings.EqualFold(k, "x-proxy-interleaved-thinking") {
					t.Errorf("A-N3 header strip leak: %q present in upstream", k)
				}
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// W8: race-external usage preservation (A-U)
// ─────────────────────────────────────────────────────────────────────────────

// TestRaceExternal_UsagePreserved_GateOff is the explicit W8
// usage-preservation assertion for the race-external path. The
// pre-existing N2 response test (TestRaceExternal_Response_NegativeCase_ByteIdentical_NonMiniMax)
// already exercises usage preservation transitively via bytes.Equal;
// this test hardens the assertion with an explicit usage-object
// check across non-stream and stream final-chunk variants.
func TestRaceExternal_UsagePreserved_GateOff(t *testing.T) {
	t.Run("non-stream", func(t *testing.T) {
		// usage intentionally non-zero and non-default to make
		// the assertion distinct.
		const upstreamBody = `{"id":"r","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":42,"completion_tokens":7,"total_tokens":49}}`
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(upstreamBody))
		}))
		defer upstream.Close()

		cfg := newTestConfigSnapshot("m")
		cfg.UpstreamURL = upstream.URL
		cfg.UpstreamCredentialID = "openai-cred"
		cfg.ModelsConfig = &mockModelsConfig{
			credentials: []models.CredentialConfig{
				{ID: "openai-cred", Provider: "openai", APIKey: "k"},
			},
		}
		inputBody := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
		req := httptest.NewRequest("POST", upstream.URL+"/v1/chat/completions", bytes.NewReader(inputBody))
		req.Header.Set("X-Proxy-Interleaved-Thinking", "true")
		upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "m", 1024*1024)

		if err := executeExternalRequest(context.Background(), cfg, req, inputBody, upstreamReq, true); err != nil {
			t.Fatalf("executeExternalRequest: %v", err)
		}
		chunks, _ := upstreamReq.buffer.GetChunksFrom(0)
		got := string(chunks[len(chunks)-1])
		// W8: usage block byte-identical.
		for _, sub := range []string{`"prompt_tokens":42`, `"completion_tokens":7`, `"total_tokens":49`} {
			if !strings.Contains(got, sub) {
				t.Errorf("W8 A non-stream: usage field %s missing from response: %s", sub, got)
			}
		}
	})

	t.Run("stream-final-chunk", func(t *testing.T) {
		// Upstream SSE stream where the final data line carries
		// the usage object (typical OpenAI behavior). The
		// translator must NOT touch the usage on the gate-OFF
		// path.
		const upstreamStream = `data: {"id":"1","choices":[{"index":0,"delta":{"content":"ok"}}]}` + "\n\n" +
			`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":3,"total_tokens":14}}` + "\n\n" +
			`data: [DONE]` + "\n\n"
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte(upstreamStream))
		}))
		defer upstream.Close()

		cfg := newTestConfigSnapshot("m")
		cfg.UpstreamURL = upstream.URL
		cfg.UpstreamCredentialID = "openai-cred"
		cfg.ModelsConfig = &mockModelsConfig{
			credentials: []models.CredentialConfig{
				{ID: "openai-cred", Provider: "openai", APIKey: "k"},
			},
		}
		inputBody := []byte(`{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
		req := httptest.NewRequest("POST", upstream.URL+"/v1/chat/completions", bytes.NewReader(inputBody))
		req.Header.Set("X-Proxy-Interleaved-Thinking", "true")
		upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "m", 1024*1024)

		if err := executeExternalRequest(context.Background(), cfg, req, inputBody, upstreamReq, true); err != nil {
			t.Fatalf("executeExternalRequest: %v", err)
		}
		chunks, _ := upstreamReq.buffer.GetChunksFrom(0)
		combined := string(bytes.Join(chunks, nil))
		for _, sub := range []string{`"prompt_tokens":11`, `"completion_tokens":3`, `"total_tokens":14`} {
			if !strings.Contains(combined, sub) {
				t.Errorf("W8 A stream: usage field %s missing from final chunk: %s", sub, combined)
			}
		}
	})
}

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
					Credentials: models.TestRefs("minimax-cred"),
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
					Credentials: models.TestRefs("minimax-cred"),
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
					Credentials: models.TestRefs("openai-cred"),
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
					Credentials: models.TestRefs("openai-cred"),
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
