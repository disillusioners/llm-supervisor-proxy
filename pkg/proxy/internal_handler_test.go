package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
)

// streamEventProvider is a minimal providers.Provider that emits a scripted
// list of StreamEvents on StreamChatCompletion. Used by handleStream unit tests.
type streamEventProvider struct {
	events []providers.StreamEvent
}

func (p *streamEventProvider) Name() string { return "stream-event-mock" }

func (p *streamEventProvider) ChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	return nil, nil
}

func (p *streamEventProvider) StreamChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	ch := make(chan providers.StreamEvent, len(p.events))
	for _, e := range p.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func (p *streamEventProvider) IsRetryable(err error) bool { return false }

// mockModelsConfig implements models.ModelsConfigInterface for testing
type mockModelsConfig struct {
	models      []models.ModelConfig
	credentials []models.CredentialConfig
}

func (m *mockModelsConfig) GetModels() []models.ModelConfig {
	return m.models
}

func (m *mockModelsConfig) GetEnabledModels() []models.ModelConfig {
	var result []models.ModelConfig
	for _, model := range m.models {
		if model.Enabled {
			result = append(result, model)
		}
	}
	return result
}

func (m *mockModelsConfig) GetModel(modelID string) *models.ModelConfig {
	for _, model := range m.models {
		if model.ID == modelID {
			copy := model
			return &copy
		}
	}
	return nil
}

func (m *mockModelsConfig) GetModelByName(modelName string) *models.ModelConfig {
	for _, model := range m.models {
		if model.Name == modelName {
			copy := model
			return &copy
		}
	}
	return nil
}

func (m *mockModelsConfig) GetTruncateParams(modelID string) []string {
	if model := m.GetModel(modelID); model != nil {
		return model.TruncateParams
	}
	return nil
}

func (m *mockModelsConfig) GetFallbackChain(modelID string) []string {
	if model := m.GetModel(modelID); model != nil {
		return model.FallbackChain
	}
	return nil
}

func (m *mockModelsConfig) AddModel(model models.ModelConfig) error {
	m.models = append(m.models, model)
	return nil
}

func (m *mockModelsConfig) UpdateModel(modelID string, model models.ModelConfig) error {
	for i, existing := range m.models {
		if existing.ID == modelID {
			m.models[i] = model
			return nil
		}
	}
	return models.ErrModelNotFound
}

func (m *mockModelsConfig) RemoveModel(modelID string) error {
	for i, model := range m.models {
		if model.ID == modelID {
			m.models = append(m.models[:i], m.models[i+1:]...)
			return nil
		}
	}
	return models.ErrModelNotFound
}

func (m *mockModelsConfig) Save() error {
	return nil
}

func (m *mockModelsConfig) Validate() error {
	return nil
}

func (m *mockModelsConfig) GetCredential(id string) *models.CredentialConfig {
	for _, cred := range m.credentials {
		if cred.ID == id {
			copy := cred
			return &copy
		}
	}
	return nil
}

func (m *mockModelsConfig) GetCredentials() []models.CredentialConfig {
	return m.credentials
}

func (m *mockModelsConfig) AddCredential(cred models.CredentialConfig) error {
	m.credentials = append(m.credentials, cred)
	return nil
}

func (m *mockModelsConfig) UpdateCredential(id string, cred models.CredentialConfig) error {
	for i, existing := range m.credentials {
		if existing.ID == id {
			m.credentials[i] = cred
			return nil
		}
	}
	return models.ErrCredentialNotFound
}

func (m *mockModelsConfig) RemoveCredential(id string) error {
	for i, cred := range m.credentials {
		if cred.ID == id {
			m.credentials = append(m.credentials[:i], m.credentials[i+1:]...)
			return nil
		}
	}
	return models.ErrCredentialNotFound
}

func (m *mockModelsConfig) ResolveInternalConfig(modelID string) (provider, apiKey, baseURL, model string, ok bool) {
	modelConfig := m.GetModel(modelID)
	if modelConfig == nil || !modelConfig.Internal {
		return "", "", "", "", false
	}

	if modelConfig.PrimaryCredentialID() == "" {
		return "", "", "", "", false
	}

	cred := m.GetCredential(modelConfig.PrimaryCredentialID())
	if cred == nil {
		return "", "", "", "", false
	}

	// Provider comes only from credential
	provider = cred.Provider

	baseURL = modelConfig.InternalBaseURL
	if baseURL == "" {
		baseURL = cred.BaseURL
	}

	return provider, cred.APIKey, baseURL, modelConfig.InternalModel, true
}

func TestCanHandleInternal(t *testing.T) {
	tests := []struct {
		name     string
		config   *models.ModelConfig
		expected bool
	}{
		{
			name:     "nil config",
			config:   nil,
			expected: false,
		},
		{
			name:     "not internal",
			config:   &models.ModelConfig{Internal: false},
			expected: false,
		},
		{
			name:     "internal true",
			config:   &models.ModelConfig{Internal: true},
			expected: true,
		},
		{
			name:     "internal with credential",
			config:   &models.ModelConfig{Internal: true, Credentials: models.TestRefs("test-cred")},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CanHandleInternal(tt.config)
			if result != tt.expected {
				t.Errorf("CanHandleInternal() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestInternalHandler_convertRequest(t *testing.T) {
	mockResolver := &mockModelsConfig{}
	handler := &InternalHandler{
		config:   &models.ModelConfig{ID: "test-model", InternalModel: "gpt-4"},
		resolver: mockResolver,
	}

	tests := []struct {
		name    string
		body    map[string]interface{}
		checkFn func(*testing.T, *providers.ChatCompletionRequest)
	}{
		{
			name: "basic request",
			body: map[string]interface{}{
				"model": "test-model",
				"messages": []interface{}{
					map[string]interface{}{"role": "user", "content": "Hello"},
				},
			},
			checkFn: func(t *testing.T, req *providers.ChatCompletionRequest) {
				if len(req.Messages) != 1 {
					t.Errorf("expected 1 message, got %d", len(req.Messages))
				}
				if req.Messages[0].Role != "user" {
					t.Errorf("expected role 'user', got %q", req.Messages[0].Role)
				}
			},
		},
		{
			name: "with temperature",
			body: map[string]interface{}{
				"model":       "test-model",
				"messages":    []interface{}{},
				"temperature": 0.7,
			},
			checkFn: func(t *testing.T, req *providers.ChatCompletionRequest) {
				if req.Temperature == nil || *req.Temperature != 0.7 {
					t.Error("expected temperature 0.7")
				}
			},
		},
		{
			name: "with max_tokens",
			body: map[string]interface{}{
				"model":      "test-model",
				"messages":   []interface{}{},
				"max_tokens": float64(100),
			},
			checkFn: func(t *testing.T, req *providers.ChatCompletionRequest) {
				if req.MaxTokens == nil || *req.MaxTokens != 100 {
					t.Error("expected max_tokens 100")
				}
			},
		},
		{
			name: "with stream",
			body: map[string]interface{}{
				"model":    "test-model",
				"messages": []interface{}{},
				"stream":   true,
			},
			checkFn: func(t *testing.T, req *providers.ChatCompletionRequest) {
				if !req.Stream {
					t.Error("expected stream true")
				}
			},
		},
		{
			name: "extra fields go to Extra",
			body: map[string]interface{}{
				"model":        "test-model",
				"messages":     []interface{}{},
				"custom_field": "custom_value",
			},
			checkFn: func(t *testing.T, req *providers.ChatCompletionRequest) {
				if req.Extra["custom_field"] != "custom_value" {
					t.Error("expected custom_field in Extra")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := handler.convertRequest(tt.body)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tt.checkFn(t, req)
		})
	}
}

func TestNewInternalHandler(t *testing.T) {
	mockResolver := &mockModelsConfig{}
	config := &models.ModelConfig{
		ID:            "test-model",
		Name:          "Test Model",
		Internal:      true,
		Credentials:   models.TestRefs("test-cred"),
		InternalModel: "gpt-4",
	}

	handler := NewInternalHandler(config, mockResolver)
	if handler == nil {
		t.Fatal("expected handler, got nil")
	}
	if handler.config != config {
		t.Error("handler config mismatch")
	}
	if handler.resolver != mockResolver {
		t.Error("handler resolver mismatch")
	}
}

// runHandleStreamWithEvents drives handleStream with a scripted provider
// and returns the captured SSE bytes plus the optional thinking sink. Pass
// sink=nil when the test does not care about captured thinking; pass a fresh
// strings.Builder to wire the side channel exactly as doAnthropicInternalRequest
// does at runtime. The body is returned verbatim so each test can assert on
// its own assertions.
func runHandleStreamWithEvents(t *testing.T, events []providers.StreamEvent, sink *strings.Builder) string {
	t.Helper()

	provider := &streamEventProvider{events: events}
	handler := &InternalHandler{
		config:   &models.ModelConfig{ID: "test-model", InternalModel: "gpt-4"},
		resolver: &mockModelsConfig{},
	}
	if sink != nil {
		// A fresh handler has no prior sink, so double-set is impossible
		// here; a failure is a test-harness bug.
		if err := handler.SetThinkingSink(sink); err != nil {
			t.Fatalf("SetThinkingSink: %v", err)
		}
	}

	req := &providers.ChatCompletionRequest{
		Model: "test-model",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "Hi"},
		},
		Stream: true,
	}

	rec := &flushingResponseRecorder{httptest.NewRecorder()}
	if err := handler.handleStream(context.Background(), provider, req, rec, "gpt-4"); err != nil {
		t.Fatalf("handleStream returned error: %v", err)
	}
	return rec.Body.String()
}

// TestInternalHandler_handleStream_ThinkingCaptured asserts that thinking
// StreamEvents are captured via the side-channel sink (so they reach the
// persisted store.Message.Thinking field via doAnthropicInternalRequest's
// wiring) and that the recorder body is BYTE-FREE of thinking — no
// reasoning_content, no thinking SSE chunks. Content deltas remain wire-visible
// exactly as before. This is the corrective test for d7300cb: it fixes the
// regression where reasoning_content leaked into the recorder and got
// translated into Anthropic thinking blocks on the wire.
func TestInternalHandler_handleStream_ThinkingCaptured(t *testing.T) {
	var sink strings.Builder
	body := runHandleStreamWithEvents(t, []providers.StreamEvent{
		{Type: "thinking", ReasoningContent: "Let me think about this..."},
		{Type: "content", Content: "Hello, world!"},
		{Type: "thinking", ReasoningContent: " More thinking."},
		{Type: "done", FinishReason: "stop"},
	}, &sink)

	// Negative: thinking text MUST NOT appear in the recorder body (wire).
	// The downstream translator.TranslateBufferedStream would otherwise turn
	// these into Anthropic thinking blocks the client would receive.
	if strings.Contains(body, "reasoning_content") {
		t.Errorf("recorder must not contain reasoning_content; body=%q", body)
	}
	if strings.Contains(body, `"thinking":`) {
		t.Errorf("recorder must not contain thinking delta; body=%q", body)
	}
	if strings.Contains(body, "Let me think about this") {
		t.Errorf("thinking text must not leak into recorder body; body=%q", body)
	}

	// Positive: content chunk is still wire-visible and unaffected.
	if !strings.Contains(body, `"content":"Hello, world!"`) {
		t.Errorf("expected content chunk to be captured; body=%q", body)
	}

	// Positive: side-channel sink has the concatenated thinking text. This
	// is the value doAnthropicInternalRequest copies into
	// arc.accumulatedThinking so the persisted store.Message.Thinking gets
	// populated.
	if got := sink.String(); got != "Let me think about this... More thinking." {
		t.Errorf("expected sink to receive concatenated thinking; got %q", got)
	}

	// Negative: extractOpenAIResponseContentFromSSE on the recorder body now
	// returns empty thinking (no longer carries reasoning_content), which
	// proves the recorder is the wire-clean surface — the persistence path
	// uses the sink instead.
	_, thinking, _ := extractOpenAIResponseContentFromSSE([]byte(body))
	if thinking != "" {
		t.Errorf("recorder-based extraction must now yield empty thinking; got %q", thinking)
	}
}

// TestInternalHandler_handleStream_NoThinkingUnchanged is the regression
// guard: a stream without thinking events must produce a body that does not
// contain any reasoning_content fields, and content extraction is unaffected.
func TestInternalHandler_handleStream_NoThinkingUnchanged(t *testing.T) {
	body := runHandleStreamWithEvents(t, []providers.StreamEvent{
		{Type: "content", Content: "Hello, world!"},
		{Type: "done", FinishReason: "stop"},
	}, nil)

	if strings.Contains(body, `reasoning_content`) {
		t.Errorf("unexpected reasoning_content in body when no thinking events emitted; body=%q", body)
	}
	if !strings.Contains(body, `"content":"Hello, world!"`) {
		t.Errorf("expected content chunk to be captured; body=%q", body)
	}

	content, thinking, _ := extractOpenAIResponseContentFromSSE([]byte(body))
	if content != "Hello, world!" {
		t.Errorf("expected extracted content 'Hello, world!', got %q", content)
	}
	if thinking != "" {
		t.Errorf("expected empty thinking, got %q", thinking)
	}
}

// normalizeSSERecorderBody strips the per-chunk non-deterministic fields
// (id, created, chunk index inside model timestamp) from a recorder SSE body
// so two structurally-identical streams can be byte-compared. The function
// preserves the wire shape (line layout, content, tool_calls, finish_reason,
// [DONE] marker) so any drift in those fields would still show up.
func normalizeSSERecorderBody(s string) string {
	var out strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(s))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		}
		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			// Leave the line unchanged so the comparison catches malformed bytes.
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		}
		delete(chunk, "id")
		delete(chunk, "created")
		normalized, _ := json.Marshal(chunk)
		out.WriteString("data: ")
		out.Write(normalized)
		out.WriteByte('\n')
	}
	return out.String()
}

// TestInternalHandler_handleStream_ThinkingByteIdentity is the byte-identity
// regression that guards the hard constraint: a thinking-event stream must
// produce a recorder body that, when normalized, is byte-identical to a
// stream that emits only the same content events (i.e. thinking contributes
// zero bytes to the recorder — exactly the pre-fix behaviour). Per-chunk
// `id`/`created` are stripped from both bodies so the comparison is stable
// across runs.
func TestInternalHandler_handleStream_ThinkingByteIdentity(t *testing.T) {
	contentA := "Hello, world!"
	contentB := "Goodbye!"

	// Stream WITH thinking events. Side channel is wired but irrelevant for
	// the byte-identity comparison — we only look at the recorder body.
	var sink strings.Builder
	bodyWithThinking := runHandleStreamWithEvents(t, []providers.StreamEvent{
		{Type: "thinking", ReasoningContent: "deep thought A"},
		{Type: "content", Content: contentA},
		{Type: "thinking", ReasoningContent: " even deeper thought A"},
		{Type: "content", Content: contentB},
		{Type: "done", FinishReason: "stop"},
	}, &sink)

	// Stream WITHOUT thinking events (control).
	bodyWithoutThinking := runHandleStreamWithEvents(t, []providers.StreamEvent{
		{Type: "content", Content: contentA},
		{Type: "content", Content: contentB},
		{Type: "done", FinishReason: "stop"},
	}, nil)

	// Sanity: the sink must have the thinking text on the thinking stream
	// (catches a future regression where someone drops the sink wiring).
	if sink.String() == "" {
		t.Errorf("expected sink to be populated on thinking stream")
	}

	normA := normalizeSSERecorderBody(bodyWithThinking)
	normB := normalizeSSERecorderBody(bodyWithoutThinking)
	if normA != normB {
		t.Errorf("recorder body must be byte-identical (modulo id/created) when thinking events are present vs absent; diff:\nwith=%q\nwithout=%q", normA, normB)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// W1: Thinking-sink invariant tests
// ─────────────────────────────────────────────────────────────────────────────

// TestInternalHandler_SetThinkingSink_DoubleSetError asserts the W1
// invariant: a second SetThinkingSink call without an intervening
// ResetThinkingSink is a programming error and must return an error while
// leaving the ORIGINAL sink installed (the second builder must never receive
// any bytes — a double-set would otherwise split the captured thinking text
// across two builders).
func TestInternalHandler_SetThinkingSink_DoubleSetError(t *testing.T) {
	handler := &InternalHandler{
		config:   &models.ModelConfig{ID: "test-model"},
		resolver: &mockModelsConfig{},
	}

	var first strings.Builder
	if err := handler.SetThinkingSink(&first); err != nil {
		t.Fatalf("first SetThinkingSink must succeed, got error: %v", err)
	}

	var second strings.Builder
	err := handler.SetThinkingSink(&second)
	if err == nil {
		t.Fatal("second SetThinkingSink without intervening reset must return an error")
	}

	// The original sink must remain installed and usable.
	if handler.thinkingSink != &first {
		t.Error("double-set must leave the original sink installed")
	}
	handler.thinkingSink.WriteString("still the first sink")
	if got := first.String(); got != "still the first sink" {
		t.Errorf("original sink must stay functional; got %q", got)
	}
	if got := second.String(); got != "" {
		t.Errorf("second builder must never receive bytes; got %q", got)
	}

	// Reset is the sanctioned way to make a subsequent set succeed.
	handler.ResetThinkingSink()
	var third strings.Builder
	if err := handler.SetThinkingSink(&third); err != nil {
		t.Fatalf("SetThinkingSink after ResetThinkingSink must succeed, got error: %v", err)
	}
	if handler.thinkingSink != &third {
		t.Error("post-reset SetThinkingSink must install the new sink")
	}
}

// TestInternalHandler_handleStream_NilSinkNoPanic asserts the W1 nil-sink
// invariant: with NO sink installed, a stream containing thinking events must
// not panic and must keep the recorder byte-clean of thinking — the
// documented base behaviour of fea5874 (thinking silently not captured).
func TestInternalHandler_handleStream_NilSinkNoPanic(t *testing.T) {
	body := runHandleStreamWithEvents(t, []providers.StreamEvent{
		{Type: "thinking", ReasoningContent: "partial internal thinking"},
		{Type: "content", Content: "visible answer"},
		{Type: "thinking", ReasoningContent: " more thinking"},
		{Type: "done", FinishReason: "stop"},
	}, nil)

	if strings.Contains(body, "reasoning_content") {
		t.Errorf("nil-sink stream must not contain reasoning_content on the wire; body=%q", body)
	}
	if strings.Contains(body, "partial internal thinking") {
		t.Errorf("thinking text must never leak to the wire, even without a sink; body=%q", body)
	}
	if !strings.Contains(body, `"content":"visible answer"`) {
		t.Errorf("content chunks must stay wire-visible under nil sink; body=%q", body)
	}
}
