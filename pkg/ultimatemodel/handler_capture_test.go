package ultimatemodel

// Fix 3 capture tests for the reasoning-observability effort.
//
// Verifies that the passive capture in pkg/ultimatemodel:
//  1. Correctly accumulates assistant Content + Thinking from
//     every wire path (non-stream external, stream external,
//     non-stream internal, stream internal).
//  2. Persists those via store.Message from the outer handler
//     (proxy-level test below in TestExecute_PersistsAssistantMessage).
//  3. DOES NOT change what the client receives — bytes are
//     byte-identical with capture enabled.
//
// The byte-identity tests use the exact "golden upstream bytes"
// pattern established by
// TestExecuteExternal_NegativeCase_FlagAbsent_MiniMaxCred_ResponseByteIdentical
// (handler_interleaved_matrix_test.go). The capture path is
// compared against the same upstream-passthrough that the existing
// byte-identity tests already prove is identical to upstream. If
// capture mutated bytes, those existing tests would also break —
// which means the capture additions here stay in lockstep with
// the no-mutation invariant.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// captureGoldenExternalNonStream is the canonical non-stream
// upstream body. Used as the byte-identity reference for the
// capture-on path. Each test below asserts that the client-received
// bytes equal this exactly.
const captureGoldenExternalNonStream = `{"id":"r","object":"chat.completion","created":1,"model":"ultimate-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello there","reasoning_content":"thinking 1"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`

// TestCapture_ExternalNonStream_ReturnsContentAndThinking asserts
// that the non-stream external path returns ExecuteResult with
// Content + Thinking populated from the upstream response
// (post-translation) body.
func TestCapture_ExternalNonStream_ReturnsContentAndThinking(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(captureGoldenExternalNonStream))
	}))
	defer upstream.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = upstream.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "ultimate-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "hi"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	result, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), false, false, "")
	if err != nil {
		t.Fatalf("executeExternal: %v", err)
	}
	if result == nil {
		t.Fatal("ExecuteResult should not be nil")
	}
	if result.Content != "hello there" {
		t.Errorf("Content = %q, want %q", result.Content, "hello there")
	}
	if result.Thinking != "thinking 1" {
		t.Errorf("Thinking = %q, want %q", result.Thinking, "thinking 1")
	}
	// And wire bytes must be byte-identical to the upstream body.
	if !bytes.Equal(w.Body.Bytes(), []byte(captureGoldenExternalNonStream)) {
		t.Errorf("capture-on non-stream bytes diverged from upstream:\n want=%q\n  got=%q",
			captureGoldenExternalNonStream, w.Body.String())
	}
}

// TestCapture_ExternalStream_ReturnsContentAndThinking asserts
// that the stream external path accumulates Content + Thinking
// across multiple SSE chunks and returns them on ExecuteResult.
//
// Also asserts that the byte stream written to the client is
// exactly the concatenation of the upstream SSE chunks (no
// re-framing, no padding, no extra chunks). This is the strongest
// capture-vs-wire proof: if the capture loop altered any byte
// or added a chunk, this test would fail.
func TestCapture_ExternalStream_ReturnsContentAndThinking(t *testing.T) {
	const goldenStream = `data: {"id":"1","choices":[{"index":0,"delta":{"content":"hello "}}]}` + "\n\n" +
		`data: {"id":"1","choices":[{"index":0,"delta":{"reasoning_content":"think-1 "}}]}` + "\n\n" +
		`data: {"id":"1","choices":[{"index":0,"delta":{"content":"there"}}]}` + "\n\n" +
		`data: {"id":"1","choices":[{"index":0,"delta":{"reasoning_content":"think-2"}}]}` + "\n\n" +
		`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(goldenStream))
	}))
	defer upstream.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = upstream.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	resp, err := http.Get(upstream.URL)
	if err != nil {
		t.Fatalf("http.Get: %v", err)
	}
	defer resp.Body.Close()

	result, err := h.streamResponse(w, resp, "ultimate-model", nil, false, false)
	if err != nil {
		t.Fatalf("streamResponse: %v", err)
	}
	if result == nil {
		t.Fatal("ExecuteResult should not be nil")
	}
	if result.Content != "hello there" {
		t.Errorf("Content = %q, want %q", result.Content, "hello there")
	}
	if result.Thinking != "think-1 think-2" {
		t.Errorf("Thinking = %q, want %q", result.Thinking, "think-1 think-2")
	}
	// Wire bytes must equal the upstream stream VERBATIM. The
	// capture loop runs alongside buf.Write — it never mutates
	// chunk bytes or changes the flush order.
	if !bytes.Equal(w.Body.Bytes(), []byte(goldenStream)) {
		t.Errorf("capture-on stream bytes diverged from upstream:\n want=%q\n  got=%q",
			goldenStream, w.Body.String())
	}
}

// TestCapture_InternalNonStream_ReturnsContentAndThinking asserts
// that the internal non-stream path extracts Content +
// ReasoningContent from the typed response struct (no JSON parsing,
// no mutation of wire bytes).
func TestCapture_InternalNonStream_ReturnsContentAndThinking(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	mockResp := &providers.ChatCompletionResponse{
		ID:      "chatcmpl-test",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   "test-model",
		Choices: []providers.Choice{
			{
				Index: 0,
				Message: &providers.ChatMessage{
					Role:             "assistant",
					Content:          "the answer",
					ReasoningContent: "deep thoughts",
				},
				FinishReason: "stop",
			},
		},
		Usage: providers.Usage{
			PromptTokens:     5,
			CompletionTokens: 3,
			TotalTokens:      8,
		},
	}
	p := &mockProvider{name: "mock", chatResp: mockResp}
	req := &providers.ChatCompletionRequest{Model: "test-model"}

	w := httptest.NewRecorder()
	result, err := h.handleInternalNonStream(context.Background(), p, req, w, "test-model", nil)
	if err != nil {
		t.Fatalf("handleInternalNonStream: %v", err)
	}
	if result == nil {
		t.Fatal("ExecuteResult should not be nil")
	}
	if result.Content != "the answer" {
		t.Errorf("Content = %q, want %q", result.Content, "the answer")
	}
	if result.Thinking != "deep thoughts" {
		t.Errorf("Thinking = %q, want %q", result.Thinking, "deep thoughts")
	}
	// The wire body is the JSON-encoded response. We don't need
	// to byte-compare to a golden here because json.NewEncoder
	// output for a fixed struct is deterministic in this path,
	// and the existing TestHandleInternalNonStream_Success already
	// verifies Content-Type + valid JSON. The KEY byte-identity
	// invariant — that capture does not mutate the encoder
	// output — holds by construction because capture reads from
	// the typed struct AFTER the encoder has already finished.
	var parsed map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("wire body is not valid JSON: %v", err)
	}
	choices, _ := parsed["choices"].([]interface{})
	if len(choices) == 0 {
		t.Fatal("expected at least one choice in wire body")
	}
	choice0, _ := choices[0].(map[string]interface{})
	msg, _ := choice0["message"].(map[string]interface{})
	if msg["content"] != "the answer" {
		t.Errorf("wire content = %v, want %q", msg["content"], "the answer")
	}
	if msg["reasoning_content"] != "deep thoughts" {
		t.Errorf("wire reasoning_content = %v, want %q", msg["reasoning_content"], "deep thoughts")
	}
}

// TestCapture_InternalStream_ReturnsContentAndThinking asserts
// that the internal stream path accumulates Content + Thinking
// from typed events as they are emitted (events here deliberately
// interleave thinking/content; each accumulator collects its own
// field). It also spot-checks that the wire body still carries
// every content and reasoning delta AFTER the capture loop ran
// alongside the write loop — a presence check, not an ordering or
// byte-identity proof (full stream byte-identity is covered by the
// external-path golden tests in this file).
func TestCapture_InternalStream_ReturnsContentAndThinking(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	p := &mockProvider{
		name: "mock",
		streamEvents: []providers.StreamEvent{
			{Type: "thinking", ReasoningContent: "deep thought 1 "},
			{Type: "content", Content: "answer part 1 "},
			{Type: "thinking", ReasoningContent: "deep thought 2"},
			{Type: "content", Content: "answer part 2"},
			{Type: "done", FinishReason: "stop", Response: &providers.ChatCompletionResponse{
				Usage: providers.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
			}},
		},
	}
	req := &providers.ChatCompletionRequest{Model: "test-model"}

	w := httptest.NewRecorder()
	result, err := h.handleInternalStream(context.Background(), p, req, w, "test-model", nil)
	if err != nil {
		t.Fatalf("handleInternalStream: %v", err)
	}
	if result == nil {
		t.Fatal("ExecuteResult should not be nil")
	}
	if result.Content != "answer part 1 answer part 2" {
		t.Errorf("Content = %q, want %q", result.Content, "answer part 1 answer part 2")
	}
	if result.Thinking != "deep thought 1 deep thought 2" {
		t.Errorf("Thinking = %q, want %q", result.Thinking, "deep thought 1 deep thought 2")
	}

	// Wire body sanity: capture must not have altered any byte
	// that the proxy wrote. Spot-check that the wire body still
	// carries both reasoning_content deltas AND content deltas.
	body := w.Body.String()
	if !strings.Contains(body, "reasoning_content") {
		t.Error("wire body missing reasoning_content")
	}
	if !strings.Contains(body, "answer part 1") || !strings.Contains(body, "answer part 2") {
		t.Error("wire body missing content deltas")
	}
	if !strings.Contains(body, "deep thought 1") || !strings.Contains(body, "deep thought 2") {
		t.Error("wire body missing reasoning deltas")
	}
}

// TestCapture_Helpers_PureFunctions asserts the standalone
// capture helpers are non-mutating best-effort parsers. They
// must return "" for malformed input rather than panic or
// silently corrupt the input slice.
func TestCapture_Helpers_PureFunctions(t *testing.T) {
	t.Run("captureFromNonStreamResponse", func(t *testing.T) {
		cases := []struct {
			name         string
			body         string
			wantContent  string
			wantThinking string
		}{
			{"both present", `{"choices":[{"message":{"role":"assistant","content":"hi","reasoning_content":"why"}}]}`, "hi", "why"},
			{"only content", `{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`, "hi", ""},
			{"only thinking", `{"choices":[{"message":{"role":"assistant","reasoning_content":"why"}}]}`, "", "why"},
			{"neither", `{"choices":[{"message":{"role":"assistant"}}]}`, "", ""},
			{"empty body", ``, "", ""},
			{"garbage body", `not json`, "", ""},
			{"no choices", `{"x":1}`, "", ""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				content, thinking := captureFromNonStreamResponse([]byte(tc.body))
				if content != tc.wantContent || thinking != tc.wantThinking {
					t.Errorf("got (%q,%q), want (%q,%q)", content, thinking, tc.wantContent, tc.wantThinking)
				}
			})
		}
	})

	t.Run("captureFromSSEChunkBytes", func(t *testing.T) {
		var content, thinking strings.Builder
		captureFromSSEChunkBytes([]byte(`data: {"choices":[{"delta":{"content":"a","reasoning_content":"b"}}]}`+"\n"), &content, &thinking)
		if content.String() != "a" || thinking.String() != "b" {
			t.Errorf("got (%q,%q), want (a,b)", content.String(), thinking.String())
		}
		// Non-data lines / [DONE] / garbage must be no-ops.
		var c2, t2 strings.Builder
		captureFromSSEChunkBytes([]byte(`data: [DONE]`+"\n"), &c2, &t2)
		if c2.Len() != 0 || t2.Len() != 0 {
			t.Errorf("[DONE] should be ignored, got (%q,%q)", c2.String(), t2.String())
		}
		captureFromSSEChunkBytes([]byte("event: ping\n"), &c2, &t2)
		if c2.Len() != 0 || t2.Len() != 0 {
			t.Errorf("event: line should be ignored, got (%q,%q)", c2.String(), t2.String())
		}
		captureFromSSEChunkBytes([]byte(`garbage`), &c2, &t2)
		if c2.Len() != 0 || t2.Len() != 0 {
			t.Errorf("garbage should be ignored, got (%q,%q)", c2.String(), t2.String())
		}
	})

	// W6: bare-JSON fallback. Some upstreams emit unframed JSON
	// lines; captureFromSSEChunkBytes must still harvest
	// delta.content / delta.reasoning_content from them, while
	// `data: `-framed chunks and true garbage keep their existing
	// behaviors (captured / silently ignored).
	t.Run("captureFromSSEChunkBytes_bareJSONFallback", func(t *testing.T) {
		cases := []struct {
			name         string
			chunk        string
			wantContent  string
			wantThinking string
		}{
			{
				name:         "bare JSON line is captured",
				chunk:        `{"choices":[{"index":0,"delta":{"content":"hello ","reasoning_content":"think "}}]}` + "\n",
				wantContent:  "hello ",
				wantThinking: "think ",
			},
			{
				name:         "bare JSON no trailing newline is captured",
				chunk:        `{"choices":[{"index":0,"delta":{"content":"there"}}]}`,
				wantContent:  "there",
				wantThinking: "",
			},
			{
				name:         "data-prefixed chunk unchanged (captured)",
				chunk:        `data: {"choices":[{"index":0,"delta":{"content":"framed"}}]}` + "\n",
				wantContent:  "framed",
				wantThinking: "",
			},
			{
				name:        "data-prefixed [DONE] still ignored",
				chunk:       `data: [DONE]` + "\n",
				wantContent: "",
			},
			{
				name:        "bare [DONE] still ignored",
				chunk:       `[DONE]` + "\n",
				wantContent: "",
			},
			{
				name:        "garbage is silently ignored",
				chunk:       `not json at all`,
				wantContent: "",
			},
			{
				name:        "valid JSON but not a chat chunk is ignored",
				chunk:       `{"event":"ping"}`,
				wantContent: "",
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var content, thinking strings.Builder
				captureFromSSEChunkBytes([]byte(tc.chunk), &content, &thinking)
				if content.String() != tc.wantContent || thinking.String() != tc.wantThinking {
					t.Errorf("got (%q,%q), want (%q,%q)",
						content.String(), thinking.String(), tc.wantContent, tc.wantThinking)
				}
			})
		}
	})
}

// TestExecute_PersistsAssistantMessage_Contract verifies the
// persistence half of the ExecuteResult contract at the
// ultimatemodel layer: the outer proxy handler (pkg/proxy) builds
// store.Message{Role: "assistant", Content, Thinking} from the
// ExecuteResult and the store persists that message as JSON for
// the Web UI.
//
// S6: this test was previously a tautology — it assigned
// res.Content to msg.Content and asserted the constant back
// against itself, which can never fail (the compiler already
// guarantees the field assignment). It now asserts the REAL
// contract: a populated ExecuteResult maps onto a store.Message
// whose JSON serialization carries exactly the field names the
// store and Web UI read (role/content/thinking), and empty
// Thinking is omitted (omitempty) so capture-less paths do not
// persist an empty thinking field. The pkg/proxy integration
// itself is covered by in-package tests in pkg/proxy.
func TestExecute_PersistsAssistantMessage_Contract(t *testing.T) {
	res := &ExecuteResult{
		Usage:    &store.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
		Content:  "captured content",
		Thinking: "captured thinking",
	}
	// The outer handler pattern from pkg/proxy/handler.go is:
	//   rc.reqLog.Messages = append(rc.reqLog.Messages, store.Message{
	//       Role: "assistant", Content: execResult.Content,
	//       Thinking: execResult.Thinking,
	//   })
	msg := store.Message{
		Role:     "assistant",
		Content:  res.Content,
		Thinking: res.Thinking,
	}

	// Serialize exactly as the store does and assert the persisted
	// shape — this fails if store.Message's JSON tags drift away
	// from what the persistence layer and Web UI expect.
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal store.Message: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("persisted message is not valid JSON: %v", err)
	}
	if got["role"] != "assistant" {
		t.Errorf("persisted role = %v, want \"assistant\"", got["role"])
	}
	if got["content"] != "captured content" {
		t.Errorf("persisted content = %v, want %q", got["content"], "captured content")
	}
	if got["thinking"] != "captured thinking" {
		t.Errorf("persisted thinking = %v, want %q", got["thinking"], "captured thinking")
	}

	// omitempty contract: a message with no captured thinking must
	// not persist an empty thinking key.
	emptyThinking := store.Message{Role: "assistant", Content: res.Content}
	rawEmpty, err := json.Marshal(emptyThinking)
	if err != nil {
		t.Fatalf("marshal empty-thinking message: %v", err)
	}
	var gotEmpty map[string]interface{}
	if err := json.Unmarshal(rawEmpty, &gotEmpty); err != nil {
		t.Fatalf("empty-thinking message is not valid JSON: %v", err)
	}
	if _, present := gotEmpty["thinking"]; present {
		t.Errorf("empty Thinking should be omitted (omitempty), got %s", rawEmpty)
	}
}

// TestCapture_ExternalStream_ByteIdentity_PreservesUpstreamOrder
// is the gold-standard byte-identity test: capture must not change
// the chunk ordering, must not drop chunks, must not insert
// chunks, must not re-frame them. We use a deliberately
// inter-leaved upstream (think / content / think / content) to
// prove ordering is preserved and that capture picks them up in
// the same order.
func TestCapture_ExternalStream_ByteIdentity_PreservesUpstreamOrder(t *testing.T) {
	upstreamLines := []string{
		`data: {"id":"1","choices":[{"index":0,"delta":{"content":"a"}}]}`,
		`data: {"id":"1","choices":[{"index":0,"delta":{"reasoning_content":"T1"}}]}`,
		`data: {"id":"1","choices":[{"index":0,"delta":{"content":"b"}}]}`,
		`data: {"id":"1","choices":[{"index":0,"delta":{"reasoning_content":"T2"}}]}`,
		`data: {"id":"1","choices":[{"index":0,"delta":{"content":"c"}}]}`,
		`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}
	golden := strings.Join(upstreamLines, "\n\n") + "\n\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, line := range upstreamLines {
			fmt.Fprintf(w, "data: %s\n\n", strings.TrimPrefix(line, "data: "))
			flusher.Flush()
			time.Sleep(2 * time.Millisecond)
		}
	}))
	defer upstream.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = upstream.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "ultimate-model",
		"stream":   true,
		"messages": []map[string]interface{}{{"role": "user", "content": "x"}},
	}
	requestBodyBytes, _ := json.Marshal(body)

	result, err := h.executeExternal(context.Background(), w, r, body, requestBodyBytes, modelsCfg.GetModel("ultimate-model"), true, false, "")
	if err != nil {
		t.Fatalf("executeExternal: %v", err)
	}

	// Capture correctness: ordering must be preserved exactly.
	if result.Content != "abc" {
		t.Errorf("Content = %q, want %q", result.Content, "abc")
	}
	if result.Thinking != "T1T2" {
		t.Errorf("Thinking = %q, want %q", result.Thinking, "T1T2")
	}

	// Byte-identity: client-received bytes equal golden. If
	// capture mutated buf.Write order, added padding, or
	// dropped chunks, this comparison fails.
	if !bytes.Equal(w.Body.Bytes(), []byte(golden)) {
		t.Errorf("capture-on stream bytes diverged from upstream golden:\n want=%q\n  got=%q", golden, w.Body.String())
	}
}
