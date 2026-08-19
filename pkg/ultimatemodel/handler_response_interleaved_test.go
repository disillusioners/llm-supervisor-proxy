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
		ID:     "ultimate-model",
		Name:   "ultimate-model",
		Enabled: true,
		Internal: false,
	})
	h := NewHandler(cfg, modelsCfg, nil)

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
	h := NewHandler(cfg, modelsCfg, nil)

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
	h := NewHandler(cfg, modelsCfg, nil)

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

	_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("openai-model"), false, true)
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
	h := NewHandler(cfg, modelsCfg, nil)

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

	_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelsCfg.GetModel("openai-model"), true, true)
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
		ID:     "ultimate-model",
		Name:   "ultimate-model",
		Enabled: true,
		Internal: false,
	})
	h := NewHandler(cfg, modelsCfg, nil)

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
	h := NewHandler(cfg, modelsCfg, nil)

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

// Reference to imports needed for tests; some are used in
// negative-case fixture setup, others are reserved for future
// positive-case coverage.
var (
	_ = io.ReadAll
	_ = sync.Mutex{}
)
