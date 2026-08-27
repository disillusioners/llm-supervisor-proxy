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
