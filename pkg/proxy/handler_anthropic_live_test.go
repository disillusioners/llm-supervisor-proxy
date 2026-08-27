package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/translator"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

type liveFlushingRecorder struct {
	http.ResponseWriter
	mu  sync.Mutex
	buf bytes.Buffer
}

func (r *liveFlushingRecorder) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, err := r.ResponseWriter.Write(b)
	r.buf.Write(b[:n])
	return n, err
}

func (r *liveFlushingRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *liveFlushingRecorder) snapshotLocked() []byte {
	out := make([]byte, r.buf.Len())
	copy(out, r.buf.Bytes())
	return out
}

type liveStreamEventProvider struct {
	events []providers.StreamEvent
}

func (p *liveStreamEventProvider) Name() string { return "mock" }
func (p *liveStreamEventProvider) ChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	return nil, nil
}
func (p *liveStreamEventProvider) StreamChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	ch := make(chan providers.StreamEvent, len(p.events)+1)
	for _, e := range p.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}
func (p *liveStreamEventProvider) IsRetryable(err error) bool { return false }

func makeOpenAITextChunk(content string) string {
	chunk := map[string]interface{}{
		"id":      "chatcmpl-test",
		"object":  "chat.completion.chunk",
		"created": 1700000000,
		"model":   "gpt-4o",
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"delta": map[string]interface{}{
					"content": content,
				},
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

func makeLiveAnthropicRequest(t *testing.T, body map[string]interface{}, buffered bool) *http.Request {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "test-key")
	req.Header.Set("anthropic-version", "2023-06-01")
	if buffered {
		req.Header.Set("X-LLMProxy-Buffer-Response", "true")
	}
	return req
}

func TestAnthropicLiveStream_ForwardBytesBeforeCompletion(t *testing.T) {
	chunks := []string{
		makeOpenAITextChunk("Hello"),
		makeOpenAITextChunk(" world"),
		makeOpenAITextChunk("!"),
	}
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		r.Body.Close()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
			select {
			case <-time.After(50 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	t.Cleanup(mock.Close)

	t.Setenv("APPLY_ENV_OVERRIDES", "true")
	t.Setenv("UPSTREAM_URL", mock.URL)
	mgr, _ := config.NewManager()
	cfg := &Config{ConfigMgr: mgr, ModelsConfig: models.NewModelsConfig()}
	h := NewHandler(cfg, events.NewBus(), store.NewRequestStore(100), nil, nil, nil, nil)

	body := anthropicBody("claude-sonnet-4-5", true, []map[string]interface{}{
		{"role": "user", "content": "hi"},
	})
	req := makeLiveAnthropicRequest(t, body, false) // live mode
	rr := httptest.NewRecorder()
	h.HandleAnthropicMessages(rr, req)

	respBody := rr.Body.String()
	deltaCount := strings.Count(respBody, `"text_delta"`)
	if deltaCount < 3 {
		t.Errorf("live mode: expected at least 3 text_delta events (one per chunk); got %d. body=%q", deltaCount, respBody)
	}
	if !strings.Contains(respBody, "event: message_start") {
		t.Errorf("live mode: missing message_start")
	}
	if !strings.Contains(respBody, "event: message_stop") {
		t.Errorf("live mode: missing message_stop")
	}
}

func TestAnthropicLiveStream_InternalVariant(t *testing.T) {
	var capturedThinking strings.Builder
	provider := &liveStreamEventProvider{events: []providers.StreamEvent{
		{Type: "thinking", ReasoningContent: "Let me think"},
		{Type: "content", Content: "Hello"},
		{Type: "content", Content: " world"},
		{Type: "done", FinishReason: "stop"},
	}}

	handler := NewInternalHandler(
		&models.ModelConfig{ID: "test-model", InternalModel: "gpt-4"},
		&mockModelsConfig{},
	)
	tr := translator.NewIncrementalStreamTranslator("claude-sonnet-4-5")
	if err := handler.SetThinkingSink(&capturedThinking); err != nil {
		t.Fatalf("SetThinkingSink: %v", err)
	}
	if err := handler.SetLiveTranslator(tr); err != nil {
		t.Fatalf("SetLiveTranslator: %v", err)
	}
	// Install a dummy arc for the lazy preamble — the test exercises
	// handleStream directly, not the full HandleAnthropicMessages
	// path that normally constructs the arc. The nil-safe check in
	// emitLivePreamble would panic on nil, so we wire a minimal arc.
	handler.SetLiveArc(&anthropicRequestContext{})

	rec := httptest.NewRecorder()
	rec.Body = &bytes.Buffer{}
	rw := &flushingResponseRecorder{ResponseRecorder: rec}

	req := &providers.ChatCompletionRequest{
		Model:    "test-model",
		Messages: []providers.ChatMessage{{Role: "user", Content: "Hi"}},
		Stream:   true,
	}

	if err := handler.handleStream(context.Background(), provider, req, rw, "gpt-4"); err != nil {
		t.Fatalf("handleStream: %v", err)
	}

	body := rw.Body.String()
	if !strings.Contains(body, "event: message_start") {
		t.Errorf("internal live: missing message_start; body=%q", body)
	}
	if !strings.Contains(body, "event: ping") {
		t.Errorf("internal live: missing ping; body=%q", body)
	}
	if !strings.Contains(body, `"type":"thinking"`) {
		t.Errorf("internal live: missing thinking block; body=%q", body)
	}
	if !strings.Contains(body, "Hello") {
		t.Errorf("internal live: missing text content 'Hello'; body=%q", body)
	}
	if !strings.Contains(body, " world") {
		t.Errorf("internal live: missing text content ' world'; body=%q", body)
	}
	if !strings.Contains(body, "event: message_stop") {
		t.Errorf("internal live: missing message_stop; body=%q", body)
	}

	if capturedThinking.String() != "Let me think" {
		t.Errorf("sink should capture 'Let me think'; got %q", capturedThinking.String())
	}
}

func TestAnthropicLiveStream_ThinkingEmittedOnWire(t *testing.T) {
	provider := &liveStreamEventProvider{events: []providers.StreamEvent{
		{Type: "thinking", ReasoningContent: "Live thinking content"},
		{Type: "content", Content: "Live answer"},
		{Type: "done", FinishReason: "stop"},
	}}
	handler := NewInternalHandler(
		&models.ModelConfig{ID: "test-model", InternalModel: "gpt-4"},
		&mockModelsConfig{},
	)
	var sink strings.Builder
	tr := translator.NewIncrementalStreamTranslator("claude-sonnet-4-5")
	handler.SetThinkingSink(&sink)
	handler.SetLiveTranslator(tr)
	handler.SetLiveArc(&anthropicRequestContext{})

	rec := httptest.NewRecorder()
	rec.Body = &bytes.Buffer{}
	rw := &flushingResponseRecorder{ResponseRecorder: rec}

	req := &providers.ChatCompletionRequest{
		Model:    "test-model",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}},
		Stream:   true,
	}
	if err := handler.handleStream(context.Background(), provider, req, rw, "gpt-4"); err != nil {
		t.Fatalf("handleStream (live): %v", err)
	}

	body := rw.Body.String()
	if !strings.Contains(body, `"thinking_delta"`) {
		t.Errorf("live mode: expected Anthropic thinking_delta on the wire; body=%q", body)
	}
	if !strings.Contains(body, "Live thinking content") {
		t.Errorf("live mode: thinking text should be on the wire; body=%q", body)
	}
	if sink.String() != "Live thinking content" {
		t.Errorf("sink should capture 'Live thinking content'; got %q", sink.String())
	}

	provider2 := &liveStreamEventProvider{events: []providers.StreamEvent{
		{Type: "thinking", ReasoningContent: "Buffered thinking content"},
		{Type: "content", Content: "Buffered answer"},
		{Type: "done", FinishReason: "stop"},
	}}
	handler2 := NewInternalHandler(
		&models.ModelConfig{ID: "test-model", InternalModel: "gpt-4"},
		&mockModelsConfig{},
	)
	var sink2 strings.Builder
	handler2.SetThinkingSink(&sink2)

	rec2 := httptest.NewRecorder()
	rec2.Body = &bytes.Buffer{}
	rw2 := &flushingResponseRecorder{ResponseRecorder: rec2}

	if err := handler2.handleStream(context.Background(), provider2, req, rw2, "gpt-4"); err != nil {
		t.Fatalf("handleStream (buffered): %v", err)
	}

	body2 := rw2.Body.String()
	if strings.Contains(body2, "reasoning_content") {
		t.Errorf("buffered mode: recorder body must NOT contain reasoning_content; body=%q", body2)
	}
	if strings.Contains(body2, `"thinking":`) {
		t.Errorf("buffered mode: recorder body must NOT contain thinking bytes; body=%q", body2)
	}
	if sink2.String() != "Buffered thinking content" {
		t.Errorf("buffered sink should capture thinking; got %q", sink2.String())
	}
}

func TestAnthropicLiveStream_SequentialFallbackBreak(t *testing.T) {
	internalUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		chunk := makeOpenAITextChunk("first byte")
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		flusher.Flush()
	}))
	t.Cleanup(internalUpstream.Close)

	var fallbackAttempts int32
	externalUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fallbackAttempts, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		chunk := makeOpenAITextChunk("fallback response")
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	t.Cleanup(externalUpstream.Close)

	t.Setenv("APPLY_ENV_OVERRIDES", "true")
	t.Setenv("UPSTREAM_URL", externalUpstream.URL)
	mgr, _ := config.NewManager()
	modelsCfg := models.NewModelsConfig()
	modelsCfg.AddCredential(models.CredentialConfig{
		ID:       "live-cred",
		Provider: "openai",
		APIKey:   "sk-test",
		BaseURL:  internalUpstream.URL,
	})
	modelsCfg.AddModel(models.ModelConfig{
		ID:            "live-primary",
		Name:          "live-primary",
		Enabled:       true,
		Internal:      true,
		Credentials:   models.TestRefs("live-cred"),
		InternalModel: "gpt-4o-internal",
		FallbackChain: []string{"fallback-external"},
	})

	cfg := &Config{
		ConfigMgr:    mgr,
		ModelsConfig: modelsCfg,
	}
	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)
	h := NewHandler(cfg, bus, reqStore, nil, nil, nil, nil)

	body := anthropicBody("live-primary", true, []map[string]interface{}{
		{"role": "user", "content": "hi"},
	})
	req := makeLiveAnthropicRequest(t, body, false) // live mode
	rr := httptest.NewRecorder()
	h.HandleAnthropicMessages(rr, req)

	if got := atomic.LoadInt32(&fallbackAttempts); got != 0 {
		t.Errorf("live mode fallback guard FAILED: external upstream was hit %d times after first byte", got)
	}

	respBody := rr.Body.String()
	if !strings.Contains(respBody, "first byte") {
		t.Errorf("expected first byte text on the wire; body=%q", respBody)
	}
}

func TestAnthropicLiveStream_BufferedModeUnchanged(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for _, c := range []string{"Hello", " world"} {
			fmt.Fprintf(w, "data: %s\n\n", makeOpenAITextChunk(c))
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	t.Cleanup(mock.Close)

	t.Setenv("APPLY_ENV_OVERRIDES", "true")
	t.Setenv("UPSTREAM_URL", mock.URL)
	mgr, _ := config.NewManager()
	cfg := &Config{ConfigMgr: mgr, ModelsConfig: models.NewModelsConfig()}
	h := NewHandler(cfg, events.NewBus(), store.NewRequestStore(100), nil, nil, nil, nil)

	body := anthropicBody("claude-sonnet-4-5", true, []map[string]interface{}{
		{"role": "user", "content": "hi"},
	})
	req := makeLiveAnthropicRequest(t, body, true) // BUFFERED MODE
	rr := httptest.NewRecorder()
	h.HandleAnthropicMessages(rr, req)

	respBody := rr.Body.String()
	if !strings.Contains(respBody, "event: message_start") {
		t.Errorf("buffered mode: missing message_start")
	}
	if !strings.Contains(respBody, "Hello world") {
		t.Errorf("buffered mode: missing accumulated text; body=%q", respBody)
	}
	if !strings.Contains(respBody, "event: message_stop") {
		t.Errorf("buffered mode: missing message_stop")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestReviewFix2_MultiToolMirror is the regression test for Phase 3
// review-fix #2 (handler_anthropic.go:1164-1172). Before the fix, the
// arc mirror for tool_use content_block_start only filled EMPTY id/name
// on the last entry — so when tool B's content_block_start arrived after
// tool A already had id+name, the new id/name was SILENTLY DROPPED and
// B's input_json_deltas later appended into A's Arguments, producing
// ONE persisted tool call with CONCATENATED JSON from both tools.
//
// The fix appends a NEW entry when the last entry has a DIFFERENT id
// (or is empty after a different id). This test drives two distinct
// tool_use content_block_start events through applyTranslatorEventPayloadToArc
// and asserts TWO persisted entries with separate arguments.
// ─────────────────────────────────────────────────────────────────────────────

func TestReviewFix2_MultiToolMirror(t *testing.T) {
	arc := &anthropicRequestContext{}

	// Tool A — first content_block_start at index 0.
	cbA, _ := json.Marshal(map[string]interface{}{
		"type": "content_block_start",
		"index": 0,
		"content_block": map[string]interface{}{
			"type": "tool_use",
			"id":   "toolu_A",
			"name": "tool_a",
		},
	})
	applyTranslatorEventPayloadToArc(string(translator.EventContentBlockStart), string(cbA), arc)

	// Tool A's input_json_delta.
	deltaA, _ := json.Marshal(map[string]interface{}{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]interface{}{
			"type":         "input_json_delta",
			"partial_json": `{"a":1}`,
		},
	})
	applyTranslatorEventPayloadToArc(string(translator.EventContentBlockDelta), string(deltaA), arc)

	// Tool B — second content_block_start at index 1.
	cbB, _ := json.Marshal(map[string]interface{}{
		"type": "content_block_start",
		"index": 1,
		"content_block": map[string]interface{}{
			"type": "tool_use",
			"id":   "toolu_B",
			"name": "tool_b",
		},
	})
	applyTranslatorEventPayloadToArc(string(translator.EventContentBlockStart), string(cbB), arc)

	// Tool B's input_json_delta.
	deltaB, _ := json.Marshal(map[string]interface{}{
		"type":  "content_block_delta",
		"index": 1,
		"delta": map[string]interface{}{
			"type":         "input_json_delta",
			"partial_json": `{"b":2}`,
		},
	})
	applyTranslatorEventPayloadToArc(string(translator.EventContentBlockDelta), string(deltaB), arc)

	if len(arc.accumulatedToolCalls) != 2 {
		t.Fatalf("MultiToolMirror: arc.accumulatedToolCalls len = %d, want 2 (got %+v)",
			len(arc.accumulatedToolCalls), arc.accumulatedToolCalls)
	}

	// Tool A: id=toolu_A, name=tool_a, args=`{"a":1}`
	a := arc.accumulatedToolCalls[0]
	if a.ID != "toolu_A" {
		t.Errorf("MultiToolMirror: tool A ID = %q, want %q", a.ID, "toolu_A")
	}
	if a.Function.Name != "tool_a" {
		t.Errorf("MultiToolMirror: tool A Name = %q, want %q", a.Function.Name, "tool_a")
	}
	if got, want := a.Function.Arguments, `{"a":1}`; got != want {
		t.Errorf("MultiToolMirror: tool A Arguments = %q, want %q", got, want)
	}
	if a.Type != "tool_use" {
		t.Errorf("MultiToolMirror: tool A Type = %q, want %q", a.Type, "tool_use")
	}

	// Tool B: id=toolu_B, name=tool_b, args=`{"b":2}`
	b := arc.accumulatedToolCalls[1]
	if b.ID != "toolu_B" {
		t.Errorf("MultiToolMirror: tool B ID = %q, want %q", b.ID, "toolu_B")
	}
	if b.Function.Name != "tool_b" {
		t.Errorf("MultiToolMirror: tool B Name = %q, want %q", b.Function.Name, "tool_b")
	}
	if got, want := b.Function.Arguments, `{"b":2}`; got != want {
		t.Errorf("MultiToolMirror: tool B Arguments = %q, want %q (NOT concatenated with A's args %q)",
			got, want, a.Function.Arguments)
	}
	if b.Type != "tool_use" {
		t.Errorf("MultiToolMirror: tool B Type = %q, want %q", b.Type, "tool_use")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestReviewFix2_MultiToolMirror_SameID_FillsEmpty is a complementary
// regression for review-fix #2 — when the SAME id arrives across
// content_block_start events (the OpenAI upstream's split-arrival
// quirk where id arrives in one delta and name in a later one, both
// routed through the same content_block_start on the wire), the
// mirror should fill the empty field on the SAME last entry — NOT
// append a duplicate. This preserves the legacy fill-empty behavior
// for the single-tool case.
// ─────────────────────────────────────────────────────────────────────────────

func TestReviewFix2_MultiToolMirror_SameID_FillsEmpty(t *testing.T) {
	arc := &anthropicRequestContext{}

	// First content_block_start with id but no name.
	cb1, _ := json.Marshal(map[string]interface{}{
		"type": "content_block_start",
		"index": 0,
		"content_block": map[string]interface{}{
			"type": "tool_use",
			"id":   "toolu_X",
		},
	})
	applyTranslatorEventPayloadToArc(string(translator.EventContentBlockStart), string(cb1), arc)
	if len(arc.accumulatedToolCalls) != 1 {
		t.Fatalf("first content_block_start should create 1 entry; got %d", len(arc.accumulatedToolCalls))
	}
	if arc.accumulatedToolCalls[0].ID != "toolu_X" {
		t.Errorf("first content_block_start: ID = %q, want %q", arc.accumulatedToolCalls[0].ID, "toolu_X")
	}
	if arc.accumulatedToolCalls[0].Function.Name != "" {
		t.Errorf("first content_block_start: Name should be empty; got %q", arc.accumulatedToolCalls[0].Function.Name)
	}

	// Second content_block_start with SAME id but now name arrives.
	cb2, _ := json.Marshal(map[string]interface{}{
		"type": "content_block_start",
		"index": 0,
		"content_block": map[string]interface{}{
			"type": "tool_use",
			"id":   "toolu_X",
			"name": "lookup",
		},
	})
	applyTranslatorEventPayloadToArc(string(translator.EventContentBlockStart), string(cb2), arc)

	// Should still be 1 entry (same id, fill-empty path).
	if len(arc.accumulatedToolCalls) != 1 {
		t.Fatalf("same-id content_block_start should NOT append a new entry; got %d entries: %+v",
			len(arc.accumulatedToolCalls), arc.accumulatedToolCalls)
	}
	if arc.accumulatedToolCalls[0].ID != "toolu_X" {
		t.Errorf("same-id second start: ID = %q, want %q", arc.accumulatedToolCalls[0].ID, "toolu_X")
	}
	if arc.accumulatedToolCalls[0].Function.Name != "lookup" {
		t.Errorf("same-id second start: Name should be filled; got %q", arc.accumulatedToolCalls[0].Function.Name)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestReviewFix3_PreamblePreservedOnParseError is the regression test
// for Phase 3 review-fix #3 (handler_anthropic.go ProcessChunk parse
// error path). Before the fix, when the FIRST upstream data line
// failed to parse, ProcessChunk returned the preamble events
// alongside the error — but the handler's `continue` on error DROPPED
// those events. messageStartSent/pingSent were already true, so the
// wire never received message_start/ping.
//
// The fix writes the returned preamble events even on parse error. This
// test drives an upstream that emits an unparseable JSON line followed
// by a parseable one, and asserts message_start + ping appear on the
// wire BEFORE any subsequent chunk.
// ─────────────────────────────────────────────────────────────────────────────

func TestReviewFix3_PreamblePreservedOnParseError(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		// First upstream data line: INTENTIONAL unparseable JSON.
		// This is exactly the shape the legacy handler dropped the
		// preamble on.
		fmt.Fprint(w, "data: {not-valid-json\n\n")
		flusher.Flush()

		// Second upstream data line: a valid chunk with text content.
		fmt.Fprintf(w, "data: %s\n\n", makeOpenAITextChunk("recovered"))
		flusher.Flush()

		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	t.Cleanup(mock.Close)

	t.Setenv("APPLY_ENV_OVERRIDES", "true")
	t.Setenv("UPSTREAM_URL", mock.URL)
	mgr, _ := config.NewManager()
	cfg := &Config{ConfigMgr: mgr, ModelsConfig: models.NewModelsConfig()}
	h := NewHandler(cfg, events.NewBus(), store.NewRequestStore(100), nil, nil, nil, nil)

	body := anthropicBody("claude-sonnet-4-5", true, []map[string]interface{}{
		{"role": "user", "content": "hi"},
	})
	req := makeLiveAnthropicRequest(t, body, false) // live mode
	rr := httptest.NewRecorder()
	h.HandleAnthropicMessages(rr, req)

	respBody := rr.Body.String()

	// message_start + ping MUST appear on the wire even when the first
	// chunk is unparseable (the legacy code dropped them).
	if !strings.Contains(respBody, "event: message_start") {
		t.Errorf("ReviewFix3: message_start missing on wire despite parse-fail preamble preservation; body=%q", respBody)
	}
	if !strings.Contains(respBody, "event: ping") {
		t.Errorf("ReviewFix3: ping missing on wire despite parse-fail preamble preservation; body=%q", respBody)
	}

	// The recovered text content must still appear.
	if !strings.Contains(respBody, "recovered") {
		t.Errorf("ReviewFix3: recovered text content missing; body=%q", respBody)
	}

	// message_start MUST appear BEFORE the recovered text — that's the
	// whole point of the preamble preservation. Without the fix, the
	// first wire byte after the SSE preamble would be a text_delta,
	// which is a malformed Anthropic stream.
	idxStart := strings.Index(respBody, "event: message_start")
	idxText := strings.Index(respBody, "recovered")
	if idxStart < 0 || idxText < 0 || idxStart >= idxText {
		t.Errorf("ReviewFix3: message_start (%d) must precede text content (%d) on the wire; body=%q",
			idxStart, idxText, respBody)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestReviewFix5_NonDataLinesDroppedOnLiveWire is the regression test
// for Phase 3 review-fix #5 (handler_anthropic.go:994-1002). Before
// the fix, every non-`data:` line on the live external path was
// forwarded verbatim — so an upstream that emitted `event: ...` lines
// (OpenAI-shape framing) would leak those bytes onto the Anthropic
// wire. Buffered mode strips everything except `data:` lines; live
// mode now matches that hygiene: forward ONLY SSE comments (`: ...`),
// drop every other non-`data:` line.
//
// This test feeds a stream containing both `event:` framing lines and
// SSE comments plus the normal data: lines, and asserts:
//   - the comment IS forwarded (`: heartbeat` survives),
//   - the `event:` framing is DROPPED from the wire.
// ─────────────────────────────────────────────────────────────────────────────

func TestReviewFix5_NonDataLinesDroppedOnLiveWire(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		// OpenAI-shape `event:` framing — should be DROPPED on the live
		// Anthropic wire (review-fix #5).
		fmt.Fprint(w, "event: completion\n")
		flusher.Flush()

		// SSE comment — should be FORWARDED (kept alive by clients).
		fmt.Fprint(w, ": heartbeat\n\n")
		flusher.Flush()

		// Empty framing line — should be DROPPED (treated as
		// non-data, non-comment).
		fmt.Fprint(w, "id: 42\n")
		flusher.Flush()

		// Normal data line — should be TRANSLATED to Anthropic wire.
		fmt.Fprintf(w, "data: %s\n\n", makeOpenAITextChunk("ok"))
		flusher.Flush()

		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	t.Cleanup(mock.Close)

	t.Setenv("APPLY_ENV_OVERRIDES", "true")
	t.Setenv("UPSTREAM_URL", mock.URL)
	mgr, _ := config.NewManager()
	cfg := &Config{ConfigMgr: mgr, ModelsConfig: models.NewModelsConfig()}
	h := NewHandler(cfg, events.NewBus(), store.NewRequestStore(100), nil, nil, nil, nil)

	body := anthropicBody("claude-sonnet-4-5", true, []map[string]interface{}{
		{"role": "user", "content": "hi"},
	})
	req := makeLiveAnthropicRequest(t, body, false) // live mode
	rr := httptest.NewRecorder()
	h.HandleAnthropicMessages(rr, req)

	respBody := rr.Body.String()

	// `event: completion` framing must NOT leak to the wire.
	if strings.Contains(respBody, "event: completion\n") {
		t.Errorf("ReviewFix5: 'event: completion' OpenAI framing leaked to Anthropic wire; body=%q", respBody)
	}

	// `id: 42` framing must NOT leak to the wire.
	if strings.Contains(respBody, "id: 42") {
		t.Errorf("ReviewFix5: 'id: 42' SSE framing leaked to Anthropic wire; body=%q", respBody)
	}

	// The SSE comment `: heartbeat` should still be present.
	if !strings.Contains(respBody, ": heartbeat") {
		t.Errorf("ReviewFix5: SSE comment ': heartbeat' should be forwarded; body=%q", respBody)
	}

	// The translated text should still arrive.
	if !strings.Contains(respBody, "ok") {
		t.Errorf("ReviewFix5: text content 'ok' missing; body=%q", respBody)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestReviewFix8_NilArcEmitLivePreamble is the regression test for
// Phase 3 review-fix #8 (internal_handler.go emitLivePreamble nil-guard).
// Before the fix, emitLivePreamble panicked when called with a nil arc
// (the wire-writer code path dereferenced arc.headersSent unconditionally).
// The fix treats nil arc as "preamble already sent / not relevant" — never
// panic, never set headersSent.
// ─────────────────────────────────────────────────────────────────────────────

func TestReviewFix8_NilArcEmitLivePreamble(t *testing.T) {
	handler := NewInternalHandler(
		&models.ModelConfig{ID: "test-model", InternalModel: "gpt-4"},
		&mockModelsConfig{},
	)
	rec := httptest.NewRecorder()
	rec.Body = &bytes.Buffer{}
	rw := &flushingResponseRecorder{ResponseRecorder: rec}

	// Should NOT panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("emitLivePreamble(nil) panicked: %v", r)
		}
	}()

	if got := handler.emitLivePreamble(rw, nil); got != false {
		t.Errorf("emitLivePreamble(nil) = %v, want false (no preamble emitted, no panic)", got)
	}
	// Should not have written anything.
	if rw.Body.Len() != 0 {
		t.Errorf("emitLivePreamble(nil) wrote %d bytes; want 0 (no panic, no write)", rw.Body.Len())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestLiveMode_ToolCallRepair_Active is the regression test for the
// Phase 3 follow-up ruling (plan decision #5 — preserve tool-call
// buffering + repair in live mode on the Anthropic path). Before the
// follow-up, the live tool_call arm bypassed toolCallBuffer entirely
// (review-fix #4 had documented this as deliberate), so malformed tool
// args reached the Anthropic wire raw. The follow-up wires the buffer
// into the live arm — per-call hold, repair chain active, emission on
// JSON completion.
//
// This test sends ONE tool_call event with MALFORMED JSON args
// (missing closing brace — invalid JSON). The buffer holds the call
// (isCompleteLocked returns false), then Flush() at end-of-stream
// triggers the repairer (toolrepair's library_repair strategy uses
// jsonrepair to add the missing brace). The REPAIRED args must reach
// the wire.
//
// A failure of this test means the live arm bypasses repair again —
// raw malformed args leaking to streaming clients.
// ─────────────────────────────────────────────────────────────────────────────

func TestLiveMode_ToolCallRepair_Active(t *testing.T) {
	handler := NewInternalHandler(
		&models.ModelConfig{ID: "test-model", InternalModel: "gpt-4"},
		&mockModelsConfig{},
	)
	// Configure the buffer with the default repair config (Enabled,
	// strategies: trim_trailing_garbage, extract_json, library_repair,
	// remove_reasoning).
	handler.SetToolCallBufferConfig(1024*1024, false, toolrepair.DefaultConfig())

	tr := translator.NewIncrementalStreamTranslator("claude-sonnet-4-5")
	if err := handler.SetLiveTranslator(tr); err != nil {
		t.Fatalf("SetLiveTranslator: %v", err)
	}
	handler.SetLiveArc(&anthropicRequestContext{})

	// ONE tool_call event with INVALID JSON args (missing closing
	// brace). The buffer holds the call (isCompleteLocked fails on
	// truncated JSON), then Flush() at end-of-stream runs the repairer.
	provider := &liveStreamEventProvider{events: []providers.StreamEvent{
		{
			Type: "tool_call",
			ToolCalls: []providers.ToolCall{
				{
					ID:   "toolu_repair_test",
					Type: "function",
					Function: providers.ToolCallFunction{
						Name:      "lookup",
						Arguments: `{"q":"hi"`, // INVALID — missing closing brace
					},
				},
			},
		},
		{Type: "done", FinishReason: "stop"},
	}}

	rec := httptest.NewRecorder()
	rec.Body = &bytes.Buffer{}
	rw := &flushingResponseRecorder{ResponseRecorder: rec}

	req := &providers.ChatCompletionRequest{
		Model:    "test-model",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}},
		Stream:   true,
	}

	if err := handler.handleStream(context.Background(), provider, req, rw, "gpt-4"); err != nil {
		t.Fatalf("handleStream: %v", err)
	}

	body := rw.Body.String()

	// The repaired JSON (with closing brace added by jsonrepair) must
	// be on the Anthropic wire. The translator wraps input_json_delta
	// in `"partial_json":"..."` with the inner JSON's quotes escaped.
	// Parse the input_json_delta event's data payload and check the
	// partial_json field is the repaired value (valid JSON object).
	var foundRepaired bool
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		var ev map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		if ev["type"] != "content_block_delta" {
			continue
		}
		delta, ok := ev["delta"].(map[string]interface{})
		if !ok {
			continue
		}
		if delta["type"] != "input_json_delta" {
			continue
		}
		pj, ok := delta["partial_json"].(string)
		if !ok {
			continue
		}
		// Verify the partial_json is valid JSON ending with the
		// repaired closing brace — confirms repair added it.
		var js interface{}
		if err := json.Unmarshal([]byte(pj), &js); err == nil {
			// Also assert the repaired shape: {"q":"hi"}.
			if m, ok := js.(map[string]interface{}); ok {
				if q, ok := m["q"].(string); ok && q == "hi" {
					foundRepaired = true
				}
			}
		}
	}
	if !foundRepaired {
		t.Errorf("LiveMode repair NOT active: expected repaired JSON `{\"q\":\"hi\"}` (with closing brace) on wire; body=%q", body)
	}
	// Tool name should appear (content_block_start with name=lookup).
	if !strings.Contains(body, `"name":"lookup"`) {
		t.Errorf("LiveMode repair: tool name missing from wire; body=%q", body)
	}
	// Preamble + close events must appear.
	if !strings.Contains(body, "event: message_start") {
		t.Errorf("LiveMode repair: missing message_start; body=%q", body)
	}
	if !strings.Contains(body, "event: message_stop") {
		t.Errorf("LiveMode repair: missing message_stop; body=%q", body)
	}
	// The translator's StreamState.ToolCalls must also reflect the
	// repaired args (per-review-fix-#1 ProcessEvent accumulation mirror).
	st := tr.State()
	if len(st.ToolCalls) != 1 {
		t.Fatalf("LiveMode repair: State().ToolCalls len = %d, want 1", len(st.ToolCalls))
	}
	if got, want := st.ToolCalls[0].Arguments.String(), `{"q":"hi"}`; got != want {
		t.Errorf("LiveMode repair: State().ToolCalls[0].Arguments = %q, want %q (REPAIRED)", got, want)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestLiveMode_NonToolChunkLatency is the second regression test for
// the Phase 3 follow-up ruling. The follow-up explicitly accepts
// "arrival-order divergence" — a held tool call may complete AFTER
// later text/thinking chunks have already streamed to the wire (the
// per-call hold is BOUNDED to ONE tool call's JSON completion, not a
// full-response hold). The translator assigns block indices at
// emission time, so the held call lands later on the wire but with a
// valid Anthropic structure.
//
// This test drives:
//   1. tool_call (incomplete JSON — held)
//   2. content "HELLO"  ← must reach wire BEFORE the tool completes
//   3. tool_call (completes JSON — buffer emits)
//   4. done
//
// Assertions:
//   - "HELLO" appears on the wire BEFORE the tool_use content_block_start
//     (content pass-through latency — no cross-kind hold).
//   - The completed tool IS on the wire (held calls do reach the
//     client eventually).
//   - message_start / message_stop / structure still well-formed.
// ─────────────────────────────────────────────────────────────────────────────

func TestLiveMode_NonToolChunkLatency(t *testing.T) {
	handler := NewInternalHandler(
		&models.ModelConfig{ID: "test-model", InternalModel: "gpt-4"},
		&mockModelsConfig{},
	)
	handler.SetToolCallBufferConfig(1024*1024, false, toolrepair.DefaultConfig())

	tr := translator.NewIncrementalStreamTranslator("claude-sonnet-4-5")
	if err := handler.SetLiveTranslator(tr); err != nil {
		t.Fatalf("SetLiveTranslator: %v", err)
	}
	handler.SetLiveArc(&anthropicRequestContext{})

	// Sequence: held tool_call start → text → completing tool_call → done.
	// Combined args across events 1+3 = `{"k":"v"}` (valid JSON).
	provider := &liveStreamEventProvider{events: []providers.StreamEvent{
		{
			Type: "tool_call",
			ToolCalls: []providers.ToolCall{
				{ID: "toolu_lat", Type: "function", Function: providers.ToolCallFunction{Name: "lookup", Arguments: `{"k":`}},
			},
		},
		{Type: "content", Content: "HELLO"},
		{
			Type: "tool_call",
			ToolCalls: []providers.ToolCall{
				{ID: "toolu_lat", Type: "function", Function: providers.ToolCallFunction{Name: "lookup", Arguments: `"v"}`}},
			},
		},
		{Type: "done", FinishReason: "stop"},
	}}

	rec := httptest.NewRecorder()
	rec.Body = &bytes.Buffer{}
	rw := &flushingResponseRecorder{ResponseRecorder: rec}

	req := &providers.ChatCompletionRequest{
		Model:    "test-model",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}},
		Stream:   true,
	}

	if err := handler.handleStream(context.Background(), provider, req, rw, "gpt-4"); err != nil {
		t.Fatalf("handleStream: %v", err)
	}

	body := rw.Body.String()

	// Preamble + close events still well-formed.
	if !strings.Contains(body, "event: message_start") {
		t.Errorf("latency: missing message_start; body=%q", body)
	}
	if !strings.Contains(body, "event: message_stop") {
		t.Errorf("latency: missing message_stop; body=%q", body)
	}

	// HELLO and the completed tool's content_block_start must both
	// appear. Find their byte positions on the wire.
	idxContent := strings.Index(body, "HELLO")
	idxToolStart := strings.Index(body, `"type":"tool_use"`)
	if idxContent < 0 {
		t.Fatalf("latency: missing content 'HELLO' on wire; body=%q", body)
	}
	if idxToolStart < 0 {
		t.Fatalf("latency: missing tool_use block on wire (held tool did not land); body=%q", body)
	}

	// Non-tool pass-through latency — the content chunk must appear
	// BEFORE the held tool's completion event (no cross-kind hold).
	if idxContent >= idxToolStart {
		t.Errorf("latency violated: content 'HELLO' at byte %d must appear BEFORE tool_use block at byte %d; body=%q",
			idxContent, idxToolStart, body)
	}

	// The completed tool's combined args (the buffer concatenated the
	// two held fragments and emitted) must appear on the wire. The
	// translator wraps input_json_delta in `"partial_json":"..."` with
	// the inner JSON's quotes escaped — parse the payload and assert
	// the partial_json parses to {"k":"v"}.
	var foundCombined bool
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		var ev map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		if ev["type"] != "content_block_delta" {
			continue
		}
		delta, ok := ev["delta"].(map[string]interface{})
		if !ok {
			continue
		}
		if delta["type"] != "input_json_delta" {
			continue
		}
		pj, ok := delta["partial_json"].(string)
		if !ok {
			continue
		}
		var js interface{}
		if err := json.Unmarshal([]byte(pj), &js); err == nil {
			if m, ok := js.(map[string]interface{}); ok {
				if k, ok := m["k"].(string); ok && k == "v" {
					foundCombined = true
				}
			}
		}
	}
	if !foundCombined {
		t.Errorf("latency: completed tool's combined args missing from wire; body=%q", body)
	}

	// The translator's StreamState must reflect the completed tool
	// (per-review-fix-#1 ProcessEvent accumulation mirror — proves
	// the arc mirror in doAnthropicInternalRequest will see it).
	st := tr.State()
	if len(st.ToolCalls) != 1 {
		t.Fatalf("latency: State().ToolCalls len = %d, want 1", len(st.ToolCalls))
	}
	if got, want := st.ToolCalls[0].Arguments.String(), `{"k":"v"}`; got != want {
		t.Errorf("latency: State().ToolCalls[0].Arguments = %q, want %q", got, want)
	}
	if got, want := st.AccumulatedContent.String(), "HELLO"; got != want {
		t.Errorf("latency: State().AccumulatedContent = %q, want %q", got, want)
	}
}
