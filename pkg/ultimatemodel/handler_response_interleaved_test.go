package ultimatemodel

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

// P2-10: per-site response-side negative-case byte-identical tests
// for the ultimate paths. With flag=true + non-MiniMax provider, the
// response translator (byte-path on external, openai.go typed
// path on internal) must NOT inject reasoning fields and the
// response body must be unchanged.
//
// The corresponding race-side tests live in
// pkg/proxy/race_response_interleaved_test.go.

// ─────────────────────────────────────────────────────────────────────────────
// Ultimate-external non-stream: flag set + non-MiniMax credential ⇒ byte-identical
// ─────────────────────────────────────────────────────────────────────────────

func TestExecuteExternal_Response_NegativeCase_ByteIdentical_NonMiniMax(t *testing.T) {
	const upstreamBody = `{"id":"r","object":"chat.completion","created":1,"model":"ultimate-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("X-Proxy-Interleaved-Thinking", "true")
	r.Header.Set("Authorization", "Bearer client-key")

	body := map[string]interface{}{
		"model": "ultimate-model",
		"messages": []map[string]interface{}{
			{"role": "user", "content": "hi"},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	// upstreamProvider="" ⇒ non-MiniMax ⇒ gate OFF ⇒ response
	// translator NOT invoked.
	_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false, true, "")
	if err != nil {
		t.Fatalf("executeExternal: %v", err)
	}

	got := w.Body.String()
	if !strings.Contains(got, `"content":"hello"`) {
		t.Errorf("upstream content lost: %s", got)
	}
	// N-3: full byte-identity of the client-received response body
	// vs the exact upstream bytes. executeExternal writes bodyBytes
	// to the client verbatim on gate-OFF (no translator, no tool
	// repair on this path in the test config), so the recorder body
	// must equal the upstream mock's response byte-for-byte. This
	// guards against any future silent re-marshal regression.
	if !bytes.Equal(w.Body.Bytes(), []byte(upstreamBody)) {
		t.Errorf("gate-OFF non-stream body not byte-identical to upstream:\n want=%q\n  got=%q", upstreamBody, got)
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
	// D4: header-strip assertion on BOTH external paths (per
	// binding decision D4) — the single header
	// x-proxy-interleaved-thinking is NOT in the outbound headers
	// to upstream. We can only assert this on the negative case
	// when we have access to the captured headers; the
	// positive-case header strip is exercised in
	// TestExecuteExternal_NegativeCase_ByteIdentical_NonMiniMax
	// in handler_interleaved_test.go (P1-9).
}

// ─────────────────────────────────────────────────────────────────────────────
// Ultimate-external stream: flag set + non-MiniMax credential ⇒ no reasoning emitted
// ─────────────────────────────────────────────────────────────────────────────

func TestStreamResponse_NegativeCase_ByteIdentical_NonMiniMax(t *testing.T) {
	// Mock upstream returns an SSE stream with no reasoning_details.
	// Flag set, non-MiniMax credential ⇒ stream translator NOT
	// invoked ⇒ no reasoning_content chunks emitted.
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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get: %v", err)
	}
	defer resp.Body.Close()

	w := httptest.NewRecorder()
	// Flag=true, providerIsMiniMax=false ⇒ gate OFF.
	_, err = h.streamResponse(w, resp, "ultimate-model", nil, true, false)
	if err != nil {
		t.Fatalf("streamResponse: %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, `"content":"hello"`) {
		t.Errorf("upstream content lost: %s", body)
	}
	// N-3: full byte-identity of the client-received stream vs the
	// exact upstream bytes. Unlike the race-external loop (which
	// strips the trailing '\n' per line and breaks before the blank
	// line after [DONE]), the ultimate-external streamResponse reads
	// lines WITH their '\n' and buffers them unmodified on gate-OFF
	// (normalizers and toolCallBuffer pass these lines through
	// untouched), so the entire upstream stream — including the
	// final blank line — must round-trip byte-for-byte.
	if !bytes.Equal(w.Body.Bytes(), []byte(upstreamStream)) {
		t.Errorf("gate-OFF stream not byte-identical to upstream:\n want=%q\n  got=%q", upstreamStream, body)
	}
	if strings.Contains(body, "reasoning_content") {
		t.Errorf("body unexpectedly contains reasoning_content: %s", body)
	}
	if strings.Contains(body, "reasoning_details") {
		t.Errorf("body unexpectedly contains reasoning_details: %s", body)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Ultimate-internal non-stream: flag set + non-MiniMax credential ⇒ no reasoning fields
// ─────────────────────────────────────────────────────────────────────────────

func TestExecuteInternal_Response_NegativeCase_ByteIdentical_NonMiniMax(t *testing.T) {
	// Ultimate-internal uses the typed ChatCompletionResponse
	// (json.Encode) on the response side. openai.go's extraction
	// is naturally inert on a non-MiniMax shape.
	origNewProvider := newProviderClient
	defer func() { newProviderClient = origNewProvider }()

	mock := newMockProvider()
	mock.chatResp = &providers.ChatCompletionResponse{
		ID: "r", Object: "chat.completion", Created: 1, Model: "gpt-4o-mini",
		Choices: []providers.Choice{
			{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
		},
		Usage: providers.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
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
		"model": "openai-model",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("openai-model"), false, true, "")
	if err != nil {
		t.Fatalf("executeInternal: %v", err)
	}

	got := w.Body.String()
	// No reasoning fields injected (openai.go's extraction is
	// naturally inert on a non-MiniMax shape).
	//
	// N-3 note: no bytes.Equal assertion is possible here — the
	// ultimate-internal path re-encodes the typed
	// ChatCompletionResponse via json.Marshal of the struct, so no
	// verbatim upstream bytes exist (the mock returns a typed
	// struct, not wire bytes). Existing assertions stand as-is.
	if strings.Contains(got, "reasoning_split") {
		t.Errorf("body unexpectedly contains reasoning_split: %s", got)
	}
	if strings.Contains(got, "reasoning_details") {
		t.Errorf("body unexpectedly contains reasoning_details: %s", got)
	}
	if !strings.Contains(got, `"content":"ok"`) {
		t.Errorf("upstream content lost: %s", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Ultimate-internal stream: flag set + non-MiniMax credential ⇒ no thinking events
// ─────────────────────────────────────────────────────────────────────────────

func TestExecuteInternal_StreamResponse_NegativeCase_NoThinkingEvents_NonMiniMax(t *testing.T) {
	origNewProvider := newProviderClient
	defer func() { newProviderClient = origNewProvider }()

	mock := newMockProvider()
	mock.streamEvents = []providers.StreamEvent{
		{Type: "content", Content: "hi"},
		{Type: "done", FinishReason: "stop", Response: &providers.ChatCompletionResponse{
			ID: "r", Object: "chat.completion", Created: 1, Model: "gpt-4o-mini",
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
	modelsCfg.AddInternalModel("openai-model", "openai", "test-key", "", "gpt-4o-mini")
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("X-Proxy-Interleaved-Thinking", "true")

	body := map[string]interface{}{
		"model":  "openai-model",
		"stream": true,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("openai-model"), true, true, "")
	if err != nil {
		t.Fatalf("executeInternal: %v", err)
	}

	got := w.Body.String()
	if strings.Contains(got, "reasoning_content") {
		t.Errorf("stream unexpectedly contains reasoning_content: %s", got)
	}
	if strings.Contains(got, "reasoning_details") {
		t.Errorf("stream unexpectedly contains reasoning_details: %s", got)
	}
	// N-3 note: no bytes.Equal assertion is possible here — the
	// ultimate-internal stream path synthesizes SSE lines from typed
	// StreamEvents (regenerated id/created), so no verbatim upstream
	// bytes exist. Absence assertions above stand as-is.
}

// ─────────────────────────────────────────────────────────────────────────────
// Ultimate-external positive control: flag set + MiniMax credential ⇒ translator fires
// ─────────────────────────────────────────────────────────────────────────────

func TestExecuteExternal_Response_PositiveCase_MiniMaxAppliesTranslator(t *testing.T) {
	const upstreamBody = `{"id":"r","object":"chat.completion","created":1,"model":"ultimate-model","choices":[{"index":0,"message":{"role":"assistant","content":"final","reasoning_details":[{"type":"reasoning.text","text":"think-1"},{"type":"reasoning.text","text":"think-2"}]}}]}`

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("X-Proxy-Interleaved-Thinking", "true")

	body := map[string]interface{}{
		"model": "ultimate-model",
		"messages": []map[string]interface{}{
			{"role": "user", "content": "hi"},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	// upstreamProvider="minimax" ⇒ gate fires ⇒ response translator
	// runs and converts reasoning_details to reasoning_content.
	_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false, true, "minimax")
	if err != nil {
		t.Fatalf("executeExternal: %v", err)
	}

	got := w.Body.String()
	if !strings.Contains(got, `"reasoning_content":"think-1think-2"`) {
		t.Errorf("body missing translated reasoning_content: %s", got)
	}
	if strings.Contains(got, `"reasoning_details"`) {
		t.Errorf("body should have reasoning_details stripped: %s", got)
	}
}

func TestStreamResponse_PositiveCase_MiniMaxEmitsReasoning(t *testing.T) {
	const upstreamStream = `data: {"id":"1","choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"think-1"}]}}]}` + "\n\n" +
		`data: {"id":"1","choices":[{"index":0,"delta":{"content":"final"}}]}` + "\n\n" +
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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get: %v", err)
	}
	defer resp.Body.Close()

	w := httptest.NewRecorder()
	// Flag=true, providerIsMiniMax=true ⇒ gate fires.
	_, err = h.streamResponse(w, resp, "ultimate-model", nil, true, true)
	if err != nil {
		t.Fatalf("streamResponse: %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, `"reasoning_content":"think-1"`) {
		t.Errorf("stream missing reasoning_content translation: %s", body)
	}
	if strings.Contains(body, `"reasoning_details"`) {
		t.Errorf("stream should have reasoning_details stripped: %s", body)
	}
}

// TestStreamResponse_PositiveCase_FramingPreserved (C2a) is the
// stream-framing regression test for the ultimate-external
// path. C1 was a BLOCKER because gate-ON passthrough chunks
// reached the client unframed (raw JSON with no `data: `
// prefix, no trailing `\n\n`) — invalid SSE. This test feeds
// a mixed SSE stream (content + finish + reasoning_details)
// through the wired path and asserts that EVERY data line in
// the client-received output starts with `data: ` and the
// stream terminates with `data: [DONE]`.
func TestStreamResponse_PositiveCase_FramingPreserved(t *testing.T) {
	const upstreamStream = `data: {"id":"1","choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"think-1"}]}}]}` + "\n\n" +
		`data: {"id":"1","choices":[{"index":0,"delta":{"content":"hello"}}]}` + "\n\n" +
		`data: {"id":"1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]}}]}` + "\n\n" +
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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get: %v", err)
	}
	defer resp.Body.Close()

	w := httptest.NewRecorder()
	_, err = h.streamResponse(w, resp, "ultimate-model", nil, true, true)
	if err != nil {
		t.Fatalf("streamResponse: %v", err)
	}

	body := w.Body.String()
	// Split on the SSE event boundary `\n\n`. Each non-empty
	// split must start with `data: `.
	parts := strings.Split(body, "\n\n")
	dataLines := 0
	sawDone := false
	for _, p := range parts {
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
		t.Errorf("no data lines received: %s", body)
	}
	if !sawDone {
		t.Errorf("stream did not terminate with data: [DONE]: %s", body)
	}
	if !strings.Contains(body, `"reasoning_content":"think-1"`) {
		t.Errorf("stream missing reasoning_content translation: %s", body)
	}
	if !strings.Contains(body, `"content":"hello"`) {
		t.Errorf("stream lost content chunk: %s", body)
	}
	if !strings.Contains(body, `"tool_calls"`) {
		t.Errorf("stream lost tool_calls chunk: %s", body)
	}
	if strings.Contains(body, `"reasoning_details"`) {
		t.Errorf("stream should have reasoning_details stripped: %s", body)
	}
}

// TestExecuteExternal_NegativeCase_FlagAbsent_MiniMaxCred_NoTranslator
// (C2b) is the flag-absent × MiniMax-credential negative test
// for the ultimate-external non-stream path. The credential
// is MiniMax but the flag is absent, so the gate is OFF.
// The outbound body must NOT carry reasoning_split /
// reasoning_details; the original reasoning_content survives.
func TestExecuteExternal_NegativeCase_FlagAbsent_MiniMaxCred_NoTranslator(t *testing.T) {
	var capturedBody []byte
	var mu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"r","choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	defer upstream.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = upstream.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	// NO X-Proxy-Interleaved-Thinking header — flag absent.

	body := map[string]interface{}{
		"model": "ultimate-model",
		"messages": []map[string]interface{}{
			{"role": "assistant", "content": "answer", "reasoning_content": "think-1"},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	// upstreamProvider="minimax" — credential IS MiniMax. flag is
	// false. Gate stays off.
	_, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false, false, "minimax")
	if err != nil {
		t.Fatalf("executeExternal: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if strings.Contains(string(capturedBody), "reasoning_split") {
		t.Errorf("body unexpectedly contains reasoning_split: %s", capturedBody)
	}
	if strings.Contains(string(capturedBody), "reasoning_details") {
		t.Errorf("body unexpectedly contains reasoning_details: %s", capturedBody)
	}
	if !strings.Contains(string(capturedBody), "think-1") {
		t.Errorf("body lost original reasoning_content: %s", capturedBody)
	}
}

// TestExecuteInternal_NegativeCase_FlagAbsent_MiniMaxCred_NoTranslator
// (C2b) is the flag-absent × MiniMax-credential negative test
// for the ultimate-internal non-stream path. The provider
// is MiniMax but the flag is absent, so the typed
// ReasoningSplit is nil and per-message reasoning_details is
// NOT synthesized (the W5 translation does not run).
func TestExecuteInternal_NegativeCase_FlagAbsent_MiniMaxCred_NoTranslator(t *testing.T) {
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
	_ = httptest.NewRequest("POST", "/v1/chat/completions", nil) // flag absent
	// NO X-Proxy-Interleaved-Thinking header — flag absent.

	body := map[string]interface{}{
		"model": "minimax-model",
		"messages": []interface{}{
			map[string]interface{}{"role": "assistant", "content": "answer", "reasoning_content": "think-1"},
		},
	}
	requestBodyBytes, _ := json.Marshal(body)

	// interleaved=false — flag absent.
	_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("minimax-model"), false, false, "")
	if err != nil {
		t.Fatalf("executeInternal: %v", err)
	}

	if mock.capturedReq == nil {
		t.Fatal("provider did not capture the request")
	}

	// ReasoningSplit MUST be nil (flag absent).
	if mock.capturedReq.ReasoningSplit != nil {
		t.Errorf("ReasoningSplit = %v, want nil (flag absent)", *mock.capturedReq.ReasoningSplit)
	}
	if len(mock.capturedReq.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(mock.capturedReq.Messages))
	}
	// ReasoningDetails MUST be empty (W5 translator not run).
	if len(mock.capturedReq.Messages[0].ReasoningDetails) != 0 {
		t.Errorf("ReasoningDetails = %+v, want empty (flag absent)", mock.capturedReq.Messages[0].ReasoningDetails)
	}
	// ReasoningContent preserved (pre-existing behavior).
	if mock.capturedReq.Messages[0].ReasoningContent != "think-1" {
		t.Errorf("ReasoningContent = %q, want think-1", mock.capturedReq.Messages[0].ReasoningContent)
	}
}

// Reference to imports needed for tests; some are used in
// negative-case fixture setup, others are reserved for future
// positive-case coverage.
var (
	_ = io.ReadAll
	_ = sync.Mutex{}
)
