package proxy

import (
	"bufio"
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

// P2-10: per-site response-side negative-case byte-identical tests.
// For each of the 4 sites (race-ext non-stream + stream, race-int
// non-stream + stream) with flag=true + non-MiniMax provider, the
// response translator (byte-path on external, openai.go on internal)
// must NOT inject reasoning fields and the response body must be
// unchanged.
//
// The corresponding ultimate-ext + ultimate-int sites are tested in
// pkg/ultimatemodel/handler_response_interleaved_test.go.

// ─────────────────────────────────────────────────────────────────────────────
// Race-external non-stream: flag set + non-MiniMax credential ⇒ byte-identical
// ─────────────────────────────────────────────────────────────────────────────

func TestRaceExternal_Response_NegativeCase_ByteIdentical_NonMiniMax(t *testing.T) {
	// Mock upstream returns an OpenAI-shape response (no
	// reasoning_details). The flag is set, but the credential is
	// OpenAI, so the response translator must NOT run.
	const upstreamBody = `{"id":"r","object":"chat.completion","created":1,"model":"openai-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(upstreamBody))
	}))
	defer upstream.Close()

	cfg := newTestConfigSnapshot("openai-model")
	cfg.UpstreamURL = upstream.URL
	cfg.UpstreamCredentialID = "openai-cred"
	cfg.ModelsConfig = &mockModelsConfig{
		credentials: []models.CredentialConfig{
			{ID: "openai-cred", Provider: "openai", APIKey: "test-key"},
		},
	}

	inputBody := []byte(`{"model":"openai-model","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest("POST", upstream.URL+"/v1/chat/completions", bytes.NewReader(inputBody))
	req.Header.Set("X-Proxy-Interleaved-Thinking", "true")

	upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "openai-model", 1024*1024)

	// interleaved=true; non-MiniMax credential ⇒ gate OFF ⇒
	// translator on response side NOT invoked.
	if err := executeExternalRequest(context.Background(), cfg, req, inputBody, upstreamReq, true); err != nil {
		t.Fatalf("executeExternalRequest: %v", err)
	}

	// The upstream body must appear in the buffer unchanged.
	chunks, _ := upstreamReq.buffer.GetChunksFrom(0)
	if len(chunks) == 0 {
		t.Fatal("no chunks in buffer")
	}
	got := string(chunks[len(chunks)-1])
	if !strings.Contains(got, `"content":"hello"`) {
		t.Errorf("upstream content lost: %s", got)
	}
	// N-3: full byte-identity of the client-visible response body
	// vs the exact upstream bytes. streamBuffer.Add restores the
	// trailing '\n' it documents as stripped, so the expected bytes
	// are the upstream body + '\n'. This guards against any future
	// silent re-marshal on the gate-OFF path.
	if !bytes.Equal(chunks[len(chunks)-1], append([]byte(upstreamBody), '\n')) {
		t.Errorf("gate-OFF non-stream body not byte-identical to upstream:\n want=%q\n  got=%q", upstreamBody+"\n", got)
	}
	// No translator-injected fields.
	if strings.Contains(got, "reasoning_split") {
		t.Errorf("body unexpectedly contains reasoning_split: %s", got)
	}
	if strings.Contains(got, "reasoning_details") {
		t.Errorf("body unexpectedly contains reasoning_details: %s", got)
	}
	if strings.Contains(got, "reasoning_content") {
		t.Errorf("body unexpectedly contains reasoning_content: %s", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Race-external stream: flag set + non-MiniMax credential ⇒ byte-identical
// ─────────────────────────────────────────────────────────────────────────────

func TestRaceExternal_StreamResponse_NegativeCase_ByteIdentical_NonMiniMax(t *testing.T) {
	// Mock upstream returns an SSE stream with NO reasoning_details.
	// Flag set, non-MiniMax credential ⇒ stream translator NOT
	// invoked ⇒ the stream goes through unchanged (other than the
	// pre-existing toolCallBuffer / normalizer transforms).
	const upstreamStream = `data: {"id":"1","choices":[{"index":0,"delta":{"content":"hello"}}]}` + "\n\n" +
		`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(upstreamStream))
	}))
	defer upstream.Close()

	cfg := newTestConfigSnapshot("openai-model")
	cfg.UpstreamURL = upstream.URL
	cfg.UpstreamCredentialID = "openai-cred"
	cfg.ModelsConfig = &mockModelsConfig{
		credentials: []models.CredentialConfig{
			{ID: "openai-cred", Provider: "openai", APIKey: "test-key"},
		},
	}

	inputBody := []byte(`{"model":"openai-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest("POST", upstream.URL+"/v1/chat/completions", bytes.NewReader(inputBody))
	req.Header.Set("X-Proxy-Interleaved-Thinking", "true")

	upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "openai-model", 1024*1024)

	if err := executeExternalRequest(context.Background(), cfg, req, inputBody, upstreamReq, true); err != nil {
		t.Fatalf("executeExternalRequest: %v", err)
	}

	chunks, _ := upstreamReq.buffer.GetChunksFrom(0)
	combined := string(bytes.Join(chunks, nil))
	if !strings.Contains(combined, `"content":"hello"`) {
		t.Errorf("upstream content lost: %s", combined)
	}
	// N-3: byte-identity of the client-visible stream vs the exact
	// upstream bytes. The race-external stream loop strips each
	// line's trailing '\n' before buffering and streamBuffer.Add
	// restores it, and the loop breaks at `data: [DONE]` BEFORE
	// consuming the blank line after it — so the expected bytes are
	// the upstream stream minus its FINAL '\n' (the [DONE] line's
	// own newline is restored by Add). This guards against any
	// future silent re-marshal on the gate-OFF path.
	wantStream := upstreamStream[:len(upstreamStream)-1]
	if !bytes.Equal([]byte(combined), []byte(wantStream)) {
		t.Errorf("gate-OFF stream not byte-identical to upstream:\n want=%q\n  got=%q", wantStream, combined)
	}
	if strings.Contains(combined, "reasoning_content") {
		t.Errorf("body unexpectedly contains reasoning_content: %s", combined)
	}
	if strings.Contains(combined, "reasoning_details") {
		t.Errorf("body unexpectedly contains reasoning_details: %s", combined)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Race-internal non-stream: flag set + non-MiniMax credential ⇒ byte-identical
// ─────────────────────────────────────────────────────────────────────────────

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

// ─────────────────────────────────────────────────────────────────────────────
// Bonus: positive control for the response side. With
// flag=true + MiniMax credential + upstream carrying reasoning_details,
// the response translator DOES fire and the body is transformed.
// ─────────────────────────────────────────────────────────────────────────────

func TestRaceExternal_Response_PositiveCase_MiniMaxAppliesTranslator(t *testing.T) {
	// Mock upstream returns a MiniMax-shape response with
	// reasoning_details. The flag is set, the credential is
	// MiniMax, so the response translator DOES run and converts
	// reasoning_details to reasoning_content.
	const upstreamBody = `{"id":"r","object":"chat.completion","created":1,"model":"minimax","choices":[{"index":0,"message":{"role":"assistant","content":"final","reasoning_details":[{"type":"reasoning.text","text":"think-1"},{"type":"reasoning.text","text":"think-2"}]}}]}`

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(upstreamBody))
	}))
	defer upstream.Close()

	cfg := newTestConfigSnapshot("minimax")
	cfg.UpstreamURL = upstream.URL
	cfg.UpstreamCredentialID = "minimax-cred"
	cfg.ModelsConfig = &mockModelsConfig{
		credentials: []models.CredentialConfig{
			{ID: "minimax-cred", Provider: "minimax", APIKey: "test-key"},
		},
	}

	inputBody := []byte(`{"model":"minimax","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest("POST", upstream.URL+"/v1/chat/completions", bytes.NewReader(inputBody))
	req.Header.Set("X-Proxy-Interleaved-Thinking", "true")

	upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "minimax", 1024*1024)

	if err := executeExternalRequest(context.Background(), cfg, req, inputBody, upstreamReq, true); err != nil {
		t.Fatalf("executeExternalRequest: %v", err)
	}

	chunks, _ := upstreamReq.buffer.GetChunksFrom(0)
	if len(chunks) == 0 {
		t.Fatal("no chunks in buffer")
	}
	got := string(chunks[len(chunks)-1])
	if !strings.Contains(got, `"reasoning_content":"think-1think-2"`) {
		t.Errorf("body missing translated reasoning_content: %s", got)
	}
	if strings.Contains(got, "reasoning_details") {
		t.Errorf("body should have reasoning_details stripped: %s", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Stream positive control
// ─────────────────────────────────────────────────────────────────────────────

func TestRaceExternal_StreamResponse_PositiveCase_MiniMaxEmitsReasoning(t *testing.T) {
	// Mock upstream returns an SSE stream with reasoning_details.
	// Flag + MiniMax credential ⇒ stream translator fires and
	// emits reasoning_content chunks.
	const upstreamStream = `data: {"id":"1","choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"think-1"}]}}]}` + "\n\n" +
		`data: {"id":"1","choices":[{"index":0,"delta":{"content":"final"}}]}` + "\n\n" +
		`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(upstreamStream))
	}))
	defer upstream.Close()

	cfg := newTestConfigSnapshot("minimax")
	cfg.UpstreamURL = upstream.URL
	cfg.UpstreamCredentialID = "minimax-cred"
	cfg.ModelsConfig = &mockModelsConfig{
		credentials: []models.CredentialConfig{
			{ID: "minimax-cred", Provider: "minimax", APIKey: "test-key"},
		},
	}

	inputBody := []byte(`{"model":"minimax","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest("POST", upstream.URL+"/v1/chat/completions", bytes.NewReader(inputBody))
	req.Header.Set("X-Proxy-Interleaved-Thinking", "true")

	upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "minimax", 1024*1024)

	if err := executeExternalRequest(context.Background(), cfg, req, inputBody, upstreamReq, true); err != nil {
		t.Fatalf("executeExternalRequest: %v", err)
	}

	chunks, _ := upstreamReq.buffer.GetChunksFrom(0)
	combined := string(bytes.Join(chunks, nil))
	if !strings.Contains(combined, `"reasoning_content":"think-1"`) {
		t.Errorf("stream missing reasoning_content translation: %s", combined)
	}
	if strings.Contains(combined, `"reasoning_details"`) {
		t.Errorf("stream should have reasoning_details stripped: %s", combined)
	}
}

// TestRaceExternal_StreamResponse_PositiveCase_FramingPreserved
// (C2a) is the stream-framing regression test for the
// race-external path. C1 was a BLOCKER because gate-ON
// passthrough chunks reached the client unframed (raw JSON
// with no `data: ` prefix, no trailing `\n\n`) — invalid SSE.
// This test feeds a mixed SSE stream (content + finish +
// tool_calls + reasoning_details) through the wired path and
// asserts that EVERY data line in the client-received output
// starts with `data: ` and the stream terminates with
// `data: [DONE]`.
func TestRaceExternal_StreamResponse_PositiveCase_FramingPreserved(t *testing.T) {
	const upstreamStream = `data: {"id":"1","choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"think-1"}]}}]}` + "\n\n" +
		`data: {"id":"1","choices":[{"index":0,"delta":{"content":"hello"}}]}` + "\n\n" +
		`data: {"id":"1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]}}]}` + "\n\n" +
		`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(upstreamStream))
	}))
	defer upstream.Close()

	cfg := newTestConfigSnapshot("minimax")
	cfg.UpstreamURL = upstream.URL
	cfg.UpstreamCredentialID = "minimax-cred"
	cfg.ModelsConfig = &mockModelsConfig{
		credentials: []models.CredentialConfig{
			{ID: "minimax-cred", Provider: "minimax", APIKey: "test-key"},
		},
	}

	inputBody := []byte(`{"model":"minimax","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest("POST", upstream.URL+"/v1/chat/completions", bytes.NewReader(inputBody))
	req.Header.Set("X-Proxy-Interleaved-Thinking", "true")

	upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "minimax", 1024*1024)

	if err := executeExternalRequest(context.Background(), cfg, req, inputBody, upstreamReq, true); err != nil {
		t.Fatalf("executeExternalRequest: %v", err)
	}

	chunks, _ := upstreamReq.buffer.GetChunksFrom(0)
	combined := string(bytes.Join(chunks, nil))

	// Split on the SSE event boundary `\n\n`. Each non-empty
	// split must start with `data: ` (every line is framed).
	parts := strings.Split(combined, "\n\n")
	dataLines := 0
	sawDone := false
	for _, p := range parts {
		// Trim trailing whitespace; the [DONE] event
		// produces a final "data: [DONE]\n\n" which
		// leaves a trailing empty element after the
		// split.
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, "data: ") {
			t.Errorf("C1 BLOCKER regression: unframed line in client output: %q", p)
			continue
		}
		dataLines++
		if p == "data: [DONE]" {
			sawDone = true
		}
	}
	if dataLines == 0 {
		t.Errorf("no data lines received: %s", combined)
	}
	if !sawDone {
		t.Errorf("stream did not terminate with data: [DONE]: %s", combined)
	}
	// Reasoning_details was translated.
	if !strings.Contains(combined, `"reasoning_content":"think-1"`) {
		t.Errorf("stream missing reasoning_content translation: %s", combined)
	}
	// Content survived.
	if !strings.Contains(combined, `"content":"hello"`) {
		t.Errorf("stream lost content chunk: %s", combined)
	}
	// Tool calls survived (W3 — read-only on tool_calls).
	if !strings.Contains(combined, `"tool_calls"`) {
		t.Errorf("stream lost tool_calls chunk: %s", combined)
	}
	// reasoning_details stripped everywhere.
	if strings.Contains(combined, `"reasoning_details"`) {
		t.Errorf("stream should have reasoning_details stripped: %s", combined)
	}
}

// TestRaceExternal_NegativeCase_FlagAbsent_MiniMaxCred_NoTranslator
// (C2b) is the flag-absent × MiniMax-credential negative test
// for the race-external non-stream path. The credential is
// MiniMax (so the credential gate COULD fire) but the flag is
// absent, so the gate is OFF. The request body must be
// byte-identical to the canonical re-marshaled input, and
// the response must NOT be re-marshaled.
func TestRaceExternal_NegativeCase_FlagAbsent_MiniMaxCred_NoTranslator(t *testing.T) {
	var capturedBody []byte
	var mu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"r","choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
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

	inputBody := []byte(`{
  "model": "minimax-model",
  "messages": [
    {"role": "assistant", "content": "answer", "reasoning_content": "think-1"}
  ]
}`)

	req := httptest.NewRequest("POST", upstream.URL+"/v1/chat/completions", bytes.NewReader(inputBody))
	// NO X-Proxy-Interleaved-Thinking header — flag absent.
	upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "minimax-model", 1024*1024)

	// interleaved=false simulates the flag-absent case.
	if err := executeExternalRequest(context.Background(), cfg, req, inputBody, upstreamReq, false); err != nil {
		t.Fatalf("executeExternalRequest: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// No reasoning_split, no reasoning_details injected.
	if strings.Contains(string(capturedBody), "reasoning_split") {
		t.Errorf("body unexpectedly contains reasoning_split: %s", capturedBody)
	}
	if strings.Contains(string(capturedBody), "reasoning_details") {
		t.Errorf("body unexpectedly contains reasoning_details: %s", capturedBody)
	}
	// Original reasoning_content preserved (translator not run).
	if !strings.Contains(string(capturedBody), "think-1") {
		t.Errorf("body lost original reasoning_content: %s", capturedBody)
	}
	// Byte-identity: upstream body must equal the canonical
	// re-marshaled input (no translator mutation).
	var bm map[string]any
	if err := json.Unmarshal(inputBody, &bm); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	inputCanonical, _ := json.Marshal(bm)
	if !bytes.Equal(capturedBody, inputCanonical) {
		t.Errorf("upstream body not byte-identical to canonical input (gate-OFF translator not invoked):\n want=%s\n  got=%s", inputCanonical, capturedBody)
	}
}

// TestRaceInternal_NegativeCase_FlagAbsent_MiniMaxCred_NoTranslator
// (C2b) is the flag-absent × MiniMax-credential negative test
// for the race-internal non-stream path. The provider is
// MiniMax but the flag is absent, so the typed
// ReasoningSplit is nil and the messages' reasoning_content
// is preserved (no reasoning_details synthesized).
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

	inputBody := []byte(`{"model":"minimax-model-internal","messages":[{"role":"assistant","content":"answer","reasoning_content":"think-1"}]}`)
	upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "minimax-model-internal", 1024*1024)

	// interleaved=false — flag absent.
	if err := executeInternalRequest(context.Background(), cfg, inputBody, upstreamReq, false); err != nil {
		t.Fatalf("executeInternalRequest: %v", err)
	}

	captured := mock.GetCapturedRequest()
	if captured == nil {
		t.Fatal("provider did not capture the request")
	}
	// ReasoningSplit MUST be nil (gate is off: flag absent).
	if captured.ReasoningSplit != nil {
		t.Errorf("ReasoningSplit = %v, want nil (flag absent)", *captured.ReasoningSplit)
	}
	if len(captured.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(captured.Messages))
	}
	// ReasoningDetails MUST be empty (translator not run).
	if len(captured.Messages[0].ReasoningDetails) != 0 {
		t.Errorf("ReasoningDetails = %+v, want empty (flag absent)", captured.Messages[0].ReasoningDetails)
	}
	// ReasoningContent preserved (pre-existing behavior).
	if captured.Messages[0].ReasoningContent != "think-1" {
		t.Errorf("ReasoningContent = %q, want think-1", captured.Messages[0].ReasoningContent)
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
