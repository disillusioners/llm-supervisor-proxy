package ultimatemodel

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// --- Tests for handleInternalNonStream ---

func TestHandleInternalNonStream_Success(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	// Create mock provider with successful response
	usage := providers.Usage{
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
	}
	mockResp := &providers.ChatCompletionResponse{
		ID:      "chatcmpl-test",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   "test-model",
		Choices: []providers.Choice{
			{
				Index:        0,
				Message:      &providers.ChatMessage{Role: "assistant", Content: "Hello!"},
				FinishReason: "stop",
			},
		},
		Usage: usage,
	}

	p := &mockProvider{
		name:     "mock",
		chatResp: mockResp,
	}

	req := &providers.ChatCompletionRequest{
		Model: "test-model",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "Hi"},
		},
	}

	w := httptest.NewRecorder()
	result, err := h.handleInternalNonStream(context.Background(), p, req, w, "test-model", nil)

	if err != nil {
		t.Fatalf("handleInternalNonStream returned error: %v", err)
	}

	if result == nil {
		t.Fatal("Expected ExecuteResult to be returned")
	}

	if result.Usage == nil {
		t.Fatal("Expected Usage to be returned")
	}

	if result.Usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", result.Usage.PromptTokens)
	}
	if result.Usage.CompletionTokens != 5 {
		t.Errorf("CompletionTokens = %d, want 5", result.Usage.CompletionTokens)
	}
	if result.Usage.TotalTokens != 15 {
		t.Errorf("TotalTokens = %d, want 15", result.Usage.TotalTokens)
	}

	// Check Content-Type header
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	// Check response body is valid JSON
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Errorf("Response body should be valid JSON: %v", err)
	}
}

func TestHandleInternalNonStream_ProviderError(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	// Create mock provider that returns error
	expectedErr := errors.New("provider error: connection refused")
	p := &mockProvider{
		name:    "mock",
		chatErr: expectedErr,
	}

	req := &providers.ChatCompletionRequest{
		Model: "test-model",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "Hi"},
		},
	}

	w := httptest.NewRecorder()
	_, err := h.handleInternalNonStream(context.Background(), p, req, w, "test-model", nil)

	if err == nil {
		t.Fatal("Expected error from provider")
	}

	if !strings.Contains(err.Error(), "provider error") {
		t.Errorf("Error should contain 'provider error', got: %v", err)
	}
}

func TestHandleInternalNonStream_EmptyUsage(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	// Response with zero usage
	mockResp := &providers.ChatCompletionResponse{
		ID:      "chatcmpl-test",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   "test-model",
		Choices: []providers.Choice{
			{
				Index:        0,
				Message:      &providers.ChatMessage{Role: "assistant", Content: ""},
				FinishReason: "stop",
			},
		},
		Usage: providers.Usage{}, // Zero values
	}

	p := &mockProvider{
		name:     "mock",
		chatResp: mockResp,
	}

	req := &providers.ChatCompletionRequest{
		Model: "test-model",
	}

	w := httptest.NewRecorder()
	result, err := h.handleInternalNonStream(context.Background(), p, req, w, "test-model", nil)

	if err != nil {
		t.Fatalf("handleInternalNonStream returned error: %v", err)
	}

	if result.Usage == nil {
		t.Fatal("Expected Usage to be returned")
	}
	if result.Usage.PromptTokens != 0 {
		t.Errorf("PromptTokens = %d, want 0", result.Usage.PromptTokens)
	}
}

// --- Tests for handleInternalStream ---

func TestHandleInternalStream_ContentAndDone(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	// Create mock provider with streaming events
	p := &mockProvider{
		name: "mock",
		streamEvents: []providers.StreamEvent{
			{Type: "content", Content: "Hello"},
			{Type: "content", Content: " World"},
			{Type: "done", FinishReason: "stop", Response: &providers.ChatCompletionResponse{
				Usage: providers.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			}},
		},
	}

	req := &providers.ChatCompletionRequest{
		Model: "test-model",
	}

	w := httptest.NewRecorder()
	usage, err := h.handleInternalStream(context.Background(), p, req, w, "test-model", nil, ExecuteOptions{BufferMode: true}) // H8 flip (plan section 7 row 16): buffered-era test opts into buffered mode

	if err != nil {
		t.Fatalf("handleInternalStream returned error: %v", err)
	}

	// Check usage was extracted
	if usage == nil {
		t.Fatal("Expected ExecuteResult to be returned")
	}
	if usage.Usage == nil {
		t.Fatal("Expected Usage to be returned")
	}
	if usage.Usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", usage.Usage.PromptTokens)
	}

	// Check SSE headers
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/event-stream")
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-cache, no-transform" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-cache, no-transform")
	}

	// Check SSE body contains expected events
	body := w.Body.String()

	// Should contain content chunks
	if !strings.Contains(body, "Hello") {
		t.Error("Response should contain first content chunk")
	}
	if !strings.Contains(body, "World") {
		t.Error("Response should contain second content chunk")
	}

	// Should contain finish chunk with finish_reason
	if !strings.Contains(body, "finish_reason") {
		t.Error("Response should contain finish_reason")
	}

	// Should contain [DONE]
	if !strings.Contains(body, "[DONE]") {
		t.Error("Response should contain [DONE]")
	}
}

func TestHandleInternalStream_WithToolCalls(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	// Create mock provider with tool call events
	p := &mockProvider{
		name: "mock",
		streamEvents: []providers.StreamEvent{
			{Type: "content", Content: "Let me "},
			{Type: "content", Content: "use the tool"},
			{Type: "tool_call", ToolCalls: []providers.ToolCall{
				{ID: "call_123", Type: "function", Function: providers.ToolCallFunction{Name: "get_weather", Arguments: "{"}},
			}},
			{Type: "tool_call", ToolCalls: []providers.ToolCall{
				{ID: "call_123", Function: providers.ToolCallFunction{Name: "", Arguments: "\"loc\""}},
			}},
			{Type: "tool_call", ToolCalls: []providers.ToolCall{
				{ID: "call_123", Function: providers.ToolCallFunction{Name: "", Arguments: ": \"NYC\"}"}},
			}},
			{Type: "done", FinishReason: "tool_calls", Response: &providers.ChatCompletionResponse{
				Usage: providers.Usage{PromptTokens: 5, CompletionTokens: 20, TotalTokens: 25},
			}},
		},
	}

	req := &providers.ChatCompletionRequest{
		Model: "test-model",
	}

	w := httptest.NewRecorder()
	usage, err := h.handleInternalStream(context.Background(), p, req, w, "test-model", nil, ExecuteOptions{BufferMode: true}) // H8 flip (plan section 7 row 16): buffered-era test opts into buffered mode

	if err != nil {
		t.Fatalf("handleInternalStream returned error: %v", err)
	}

	if usage == nil {
		t.Fatal("Expected ExecuteResult to be returned")
	}
	if usage.Usage == nil {
		t.Fatal("Expected Usage to be returned")
	}

	// Check SSE body
	body := w.Body.String()

	// Should contain tool_calls
	if !strings.Contains(body, "tool_calls") {
		t.Error("Response should contain tool_calls")
	}

	// Should contain tool function name
	if !strings.Contains(body, "get_weather") {
		t.Error("Response should contain tool function name")
	}

	// Should have finish_reason = tool_calls
	if !strings.Contains(body, "tool_calls") {
		t.Error("Response should contain tool_calls as finish_reason")
	}
}

func TestHandleInternalStream_Thinking(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	// Create mock provider with thinking events
	p := &mockProvider{
		name: "mock",
		streamEvents: []providers.StreamEvent{
			{Type: "thinking", ReasoningContent: "Let me think about this..."},
			{Type: "content", Content: "Here's my answer"},
			{Type: "done", FinishReason: "stop", Response: &providers.ChatCompletionResponse{
				Usage: providers.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			}},
		},
	}

	req := &providers.ChatCompletionRequest{
		Model: "test-model",
	}

	w := httptest.NewRecorder()
	_, err := h.handleInternalStream(context.Background(), p, req, w, "test-model", nil, ExecuteOptions{BufferMode: true}) // H8 flip (plan section 7 row 16): buffered-era test opts into buffered mode

	if err != nil {
		t.Fatalf("handleInternalStream returned error: %v", err)
	}

	// Check SSE body contains thinking content
	body := w.Body.String()
	if !strings.Contains(body, "reasoning_content") {
		t.Error("Response should contain reasoning_content field")
	}
	if !strings.Contains(body, "Let me think about this...") {
		t.Error("Response should contain thinking content")
	}
}

func TestHandleInternalStream_Error(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	// Create mock provider with error event
	p := &mockProvider{
		name: "mock",
		streamEvents: []providers.StreamEvent{
			{Type: "content", Content: "Partial"},
			{Type: "error", Error: errors.New("stream interrupted")},
		},
	}

	req := &providers.ChatCompletionRequest{
		Model: "test-model",
	}

	w := httptest.NewRecorder()
	_, err := h.handleInternalStream(context.Background(), p, req, w, "test-model", nil, ExecuteOptions{BufferMode: true}) // H8 flip (plan section 7 row 16): buffered-era test opts into buffered mode

	if err == nil {
		t.Fatal("Expected error from stream")
	}

	if !strings.Contains(err.Error(), "stream interrupted") {
		t.Errorf("Error should contain 'stream interrupted', got: %v", err)
	}

	// Check that partial content was written
	body := w.Body.String()
	if !strings.Contains(body, "Partial") {
		t.Error("Response should contain partial content before error")
	}
}

// --- Tests for convertRequest (additional cases) ---

func TestConvertRequest_NilBody(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	req, err := h.convertRequest(nil)
	if err != nil {
		t.Fatalf("convertRequest with nil body failed: %v", err)
	}

	// Should return empty request with empty model
	if req.Model != "" {
		t.Errorf("Model = %q, want empty", req.Model)
	}
}

func TestConvertRequest_EmptyBody(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	req, err := h.convertRequest(map[string]interface{}{})
	if err != nil {
		t.Fatalf("convertRequest with empty body failed: %v", err)
	}

	if req.Model != "" {
		t.Errorf("Model = %q, want empty", req.Model)
	}
	if len(req.Messages) != 0 {
		t.Errorf("Messages count = %d, want 0", len(req.Messages))
	}
}

func TestConvertRequest_MissingModelField(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	body := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "Hello",
			},
		},
	}

	req, err := h.convertRequest(body)
	if err != nil {
		t.Fatalf("convertRequest failed: %v", err)
	}

	if req.Model != "" {
		t.Errorf("Model = %q, want empty (missing field)", req.Model)
	}

	if len(req.Messages) != 1 {
		t.Errorf("Messages count = %d, want 1", len(req.Messages))
	}
}

func TestConvertRequest_EmptyMessagesArray(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	body := map[string]interface{}{
		"model":    "test-model",
		"messages": []interface{}{},
	}

	req, err := h.convertRequest(body)
	if err != nil {
		t.Fatalf("convertRequest failed: %v", err)
	}

	if len(req.Messages) != 0 {
		t.Errorf("Messages count = %d, want 0", len(req.Messages))
	}
}

func TestConvertRequest_InvalidMessagesType(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	// messages is a string instead of array - should fail gracefully
	body := map[string]interface{}{
		"model":    "test-model",
		"messages": "not an array",
	}

	req, err := h.convertRequest(body)
	if err != nil {
		t.Fatalf("convertRequest failed: %v", err)
	}

	// Should handle invalid type gracefully
	if len(req.Messages) != 0 {
		t.Errorf("Messages count = %d, want 0", len(req.Messages))
	}
}

func TestConvertRequest_ExtraFieldsPreserved(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	body := map[string]interface{}{
		"model": "test-model",
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "Hello",
			},
		},
		"extra": map[string]interface{}{
			"custom_field":  "value",
			"another_field": 123,
			"nested_object": map[string]interface{}{"key": "val"},
		},
	}

	req, err := h.convertRequest(body)
	if err != nil {
		t.Fatalf("convertRequest failed: %v", err)
	}

	if req.Extra == nil {
		t.Fatal("Extra field should be preserved")
	}

	if req.Extra["custom_field"] != "value" {
		t.Errorf("Extra custom_field = %v, want 'value'", req.Extra["custom_field"])
	}
	if req.Extra["another_field"] != 123 {
		t.Errorf("Extra another_field = %v, want 123", req.Extra["another_field"])
	}
}

func TestConvertRequest_ToolCallsInMessages(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	body := map[string]interface{}{
		"model": "test-model",
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "What's the weather?",
			},
			map[string]interface{}{
				"role":    "assistant",
				"content": "",
				"tool_calls": []interface{}{
					map[string]interface{}{
						"id":   "call_abc123",
						"type": "function",
						"function": map[string]interface{}{
							"name":      "get_weather",
							"arguments": `{"location":"NYC"}`,
						},
					},
				},
			},
			map[string]interface{}{
				"role":         "tool",
				"tool_call_id": "call_abc123",
				"content":      "Sunny, 72°F",
			},
		},
	}

	req, err := h.convertRequest(body)
	if err != nil {
		t.Fatalf("convertRequest failed: %v", err)
	}

	if len(req.Messages) != 3 {
		t.Fatalf("Messages count = %d, want 3", len(req.Messages))
	}

	// Check tool call in assistant message
	assistantMsg := req.Messages[1]
	if assistantMsg.Role != "assistant" {
		t.Errorf("Second message role = %q, want 'assistant'", assistantMsg.Role)
	}
	if len(assistantMsg.ToolCalls) != 1 {
		t.Errorf("ToolCalls count = %d, want 1", len(assistantMsg.ToolCalls))
	}
	if assistantMsg.ToolCalls[0].ID != "call_abc123" {
		t.Errorf("ToolCall ID = %q, want 'call_abc123'", assistantMsg.ToolCalls[0].ID)
	}
	if assistantMsg.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("ToolCall Function.Name = %q, want 'get_weather'", assistantMsg.ToolCalls[0].Function.Name)
	}

	// Check tool message
	toolMsg := req.Messages[2]
	if toolMsg.Role != "tool" {
		t.Errorf("Third message role = %q, want 'tool'", toolMsg.Role)
	}
	if toolMsg.ToolCallID != "call_abc123" {
		t.Errorf("ToolCallID = %q, want 'call_abc123'", toolMsg.ToolCallID)
	}
	if toolMsg.Content != "Sunny, 72°F" {
		t.Errorf("Tool message content = %q, want 'Sunny, 72°F'", toolMsg.Content)
	}
}

func TestConvertRequest_ReasoningContent(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	// Case 1: single assistant message with reasoning_content — must survive conversion.
	body := map[string]interface{}{
		"model": "deepseek-r1",
		"messages": []interface{}{
			map[string]interface{}{
				"role":              "assistant",
				"content":           "The answer is 42.",
				"reasoning_content": "Let me think about this step by step...",
			},
		},
	}

	req, err := h.convertRequest(body)
	if err != nil {
		t.Fatalf("convertRequest failed: %v", err)
	}

	if len(req.Messages) != 1 {
		t.Fatalf("Messages count = %d, want 1", len(req.Messages))
	}

	if got, want := req.Messages[0].ReasoningContent, "Let me think about this step by step..."; got != want {
		t.Errorf("Messages[0].ReasoningContent = %q, want %q", got, want)
	}

	// Case 2: multi-message request where reasoning_content lives on the second message.
	body = map[string]interface{}{
		"model": "deepseek-r1",
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "What is 2+2?",
			},
			map[string]interface{}{
				"role":              "assistant",
				"content":           "4",
				"reasoning_content": "I need to add 2 and 2 together.",
			},
		},
	}

	req, err = h.convertRequest(body)
	if err != nil {
		t.Fatalf("convertRequest failed: %v", err)
	}

	if len(req.Messages) != 2 {
		t.Fatalf("Messages count = %d, want 2", len(req.Messages))
	}

	if got, want := req.Messages[1].ReasoningContent, "I need to add 2 and 2 together."; got != want {
		t.Errorf("Messages[1].ReasoningContent = %q, want %q", got, want)
	}

	// Case 3: message without reasoning_content must yield an empty string (no panic, no leak).
	body = map[string]interface{}{
		"model": "gpt-4",
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "Hello",
			},
		},
	}

	req, err = h.convertRequest(body)
	if err != nil {
		t.Fatalf("convertRequest failed: %v", err)
	}

	if len(req.Messages) != 1 {
		t.Fatalf("Messages count = %d, want 1", len(req.Messages))
	}

	if got := req.Messages[0].ReasoningContent; got != "" {
		t.Errorf("Messages[0].ReasoningContent = %q, want empty", got)
	}

	// Case 4: empty reasoning_content should be preserved as-is (no defaulting to non-empty).
	body = map[string]interface{}{
		"model": "deepseek-r1",
		"messages": []interface{}{
			map[string]interface{}{
				"role":              "assistant",
				"content":           "Answer",
				"reasoning_content": "",
			},
		},
	}

	req, err = h.convertRequest(body)
	if err != nil {
		t.Fatalf("convertRequest failed: %v", err)
	}

	if len(req.Messages) != 1 {
		t.Fatalf("Messages count = %d, want 1", len(req.Messages))
	}

	if got := req.Messages[0].ReasoningContent; got != "" {
		t.Errorf("Messages[0].ReasoningContent = %q, want empty", got)
	}
}

func TestConvertRequest_ToolChoice(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	body := map[string]interface{}{
		"model": "test-model",
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "Use a tool",
			},
		},
		"tool_choice": "auto",
	}

	req, err := h.convertRequest(body)
	if err != nil {
		t.Fatalf("convertRequest failed: %v", err)
	}

	if req.ToolChoice != "auto" {
		t.Errorf("ToolChoice = %v, want 'auto'", req.ToolChoice)
	}
}

func TestConvertRequest_Stream(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	body := map[string]interface{}{
		"model":    "test-model",
		"stream":   true,
		"messages": []map[string]interface{}{{"role": "user", "content": "Hi"}},
	}

	req, err := h.convertRequest(body)
	if err != nil {
		t.Fatalf("convertRequest failed: %v", err)
	}

	if !req.Stream {
		t.Error("Stream should be true")
	}
}

// --- Tests for executeInternal ---

func TestExecuteInternal_ResolveFailure(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	// Don't add internal model config - ResolveInternalConfig will fail
	modelsCfg.AddModel(models.ModelConfig{
		ID:       "unresolved-model",
		Name:     "unresolved-model",
		Enabled:  true,
		Internal: true,
	})

	h := NewHandler(cfg, modelsCfg, nil, nil)

	body := map[string]interface{}{
		"model":    "unresolved-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Hi"}},
	}

	modelCfg := modelsCfg.GetModel("unresolved-model")
	if modelCfg == nil {
		t.Fatal("Model not configured")
	}

	w := httptest.NewRecorder()
	requestBodyBytes, _ := json.Marshal(body)
	_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelCfg, false, false, "")

	if err == nil {
		t.Fatal("Expected error for unresolved internal config")
	}

	if !strings.Contains(err.Error(), "failed to resolve internal config") {
		t.Errorf("Error should contain 'failed to resolve internal config', got: %v", err)
	}
}

// --- Tests for convertRequest with temperature/max_tokens ---

func TestConvertRequest_TemperatureAndMaxTokens(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	body := map[string]interface{}{
		"model":       "test-model",
		"temperature": float64(0.8),
		"max_tokens":  float64(150),
		"messages":    []map[string]interface{}{{"role": "user", "content": "Hi"}},
	}

	req, err := h.convertRequest(body)
	if err != nil {
		t.Fatalf("convertRequest failed: %v", err)
	}

	if req.Temperature == nil || *req.Temperature != 0.8 {
		t.Errorf("Temperature = %v, want 0.8", req.Temperature)
	}

	if req.MaxTokens == nil || *req.MaxTokens != 150 {
		t.Errorf("MaxTokens = %v, want 150", req.MaxTokens)
	}
}

// --- Edge case tests ---

func TestHandleInternalStream_EmptyContent(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	// Stream with empty content followed by done
	p := &mockProvider{
		name: "mock",
		streamEvents: []providers.StreamEvent{
			{Type: "content", Content: ""},
			{Type: "done", FinishReason: "stop", Response: &providers.ChatCompletionResponse{
				Usage: providers.Usage{PromptTokens: 1, CompletionTokens: 0, TotalTokens: 1},
			}},
		},
	}

	req := &providers.ChatCompletionRequest{
		Model: "test-model",
	}

	w := httptest.NewRecorder()
	usage, err := h.handleInternalStream(context.Background(), p, req, w, "test-model", nil, ExecuteOptions{BufferMode: true}) // H8 flip (plan section 7 row 16): buffered-era test opts into buffered mode

	if err != nil {
		t.Fatalf("handleInternalStream returned error: %v", err)
	}

	if usage == nil {
		t.Fatal("Expected usage")
	}
}

func TestHandleInternalStream_MultipleToolCalls(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	// Stream with multiple tool calls at once
	p := &mockProvider{
		name: "mock",
		streamEvents: []providers.StreamEvent{
			{Type: "tool_call", ToolCalls: []providers.ToolCall{
				{ID: "call_1", Type: "function", Function: providers.ToolCallFunction{Name: "tool1", Arguments: "{"}},
				{ID: "call_2", Type: "function", Function: providers.ToolCallFunction{Name: "tool2", Arguments: "{"}},
			}},
			{Type: "done", FinishReason: "tool_calls", Response: &providers.ChatCompletionResponse{
				Usage: providers.Usage{PromptTokens: 5, CompletionTokens: 10, TotalTokens: 15},
			}},
		},
	}

	req := &providers.ChatCompletionRequest{
		Model: "test-model",
	}

	w := httptest.NewRecorder()
	_, err := h.handleInternalStream(context.Background(), p, req, w, "test-model", nil, ExecuteOptions{BufferMode: true}) // H8 flip (plan section 7 row 16): buffered-era test opts into buffered mode

	if err != nil {
		t.Fatalf("handleInternalStream returned error: %v", err)
	}

	body := w.Body.String()
	// Both tool calls should be present
	if !strings.Contains(body, "tool1") {
		t.Error("Response should contain tool1")
	}
	if !strings.Contains(body, "tool2") {
		t.Error("Response should contain tool2")
	}
}

func TestConvertRequest_MissingRole(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	// Message without role field
	body := map[string]interface{}{
		"model": "test-model",
		"messages": []interface{}{
			map[string]interface{}{
				"content": "Just content, no role",
			},
		},
	}

	req, err := h.convertRequest(body)
	if err != nil {
		t.Fatalf("convertRequest failed: %v", err)
	}

	// Should handle missing role gracefully
	if len(req.Messages) != 1 {
		t.Errorf("Messages count = %d, want 1", len(req.Messages))
	}
	if req.Messages[0].Role != "" {
		t.Errorf("Role = %q, want empty", req.Messages[0].Role)
	}
}

func TestConvertRequest_MissingContent(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	// Message with role but no content
	body := map[string]interface{}{
		"model": "test-model",
		"messages": []interface{}{
			map[string]interface{}{
				"role": "assistant",
			},
		},
	}

	req, err := h.convertRequest(body)
	if err != nil {
		t.Fatalf("convertRequest failed: %v", err)
	}

	if len(req.Messages) != 1 {
		t.Errorf("Messages count = %d, want 1", len(req.Messages))
	}
	if req.Messages[0].Role != "assistant" {
		t.Errorf("Role = %q, want 'assistant'", req.Messages[0].Role)
	}
}

// --- Test usage extraction ---

func TestHandleInternalStream_UsageFromDoneEvent(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	// Create mock provider with usage only in done event
	p := &mockProvider{
		name: "mock",
		streamEvents: []providers.StreamEvent{
			{Type: "content", Content: "Hello"},
			{Type: "done", FinishReason: "stop", Response: &providers.ChatCompletionResponse{
				Usage: providers.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
			}},
		},
	}

	req := &providers.ChatCompletionRequest{
		Model: "test-model",
	}

	w := httptest.NewRecorder()
	usage, err := h.handleInternalStream(context.Background(), p, req, w, "test-model", nil, ExecuteOptions{BufferMode: true}) // H8 flip (plan section 7 row 16): buffered-era test opts into buffered mode

	if err != nil {
		t.Fatalf("handleInternalStream returned error: %v", err)
	}

	if usage == nil {
		t.Fatal("Expected ExecuteResult")
	}

	// Verify usage from done event was extracted correctly
	expected := &store.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150}
	if usage.Usage == nil {
		t.Fatal("Expected Usage to be extracted")
	}
	if usage.Usage.PromptTokens != expected.PromptTokens {
		t.Errorf("PromptTokens = %d, want %d", usage.Usage.PromptTokens, expected.PromptTokens)
	}
	if usage.Usage.CompletionTokens != expected.CompletionTokens {
		t.Errorf("CompletionTokens = %d, want %d", usage.Usage.CompletionTokens, expected.CompletionTokens)
	}
	if usage.Usage.TotalTokens != expected.TotalTokens {
		t.Errorf("TotalTokens = %d, want %d", usage.Usage.TotalTokens, expected.TotalTokens)
	}
}

func TestHandleInternalStream_NoUsageInDone(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	// Create mock provider with no usage in done event and empty stream
	// This tests that fallback counting returns zero usage when no content is streamed
	p := &mockProvider{
		name: "mock",
		streamEvents: []providers.StreamEvent{
			{Type: "done", FinishReason: "stop", Response: nil}, // No usage, no content
		},
	}

	req := &providers.ChatCompletionRequest{
		Model: "test-model",
	}

	w := httptest.NewRecorder()
	usage, err := h.handleInternalStream(context.Background(), p, req, w, "test-model", nil, ExecuteOptions{BufferMode: true}) // H8 flip (plan section 7 row 16): buffered-era test opts into buffered mode

	if err != nil {
		t.Fatalf("handleInternalStream returned error: %v", err)
	}

	// With fallback token counting, usage is now a zero struct instead of nil
	// when no usage is provided in the done event.
	// Since requestBodyBytes is nil and no content was streamed, both should be 0.
	if usage == nil {
		t.Errorf("ExecuteResult should not be nil")
	} else if usage.Usage == nil {
		t.Errorf("Usage should not be nil - fallback should return zero-usage struct")
	} else if usage.Usage.PromptTokens != 0 {
		t.Errorf("PromptTokens = %d, want 0 (no requestBodyBytes for fallback)", usage.Usage.PromptTokens)
	} else if usage.Usage.CompletionTokens != 0 {
		t.Errorf("CompletionTokens = %d, want 0 (empty stream for fallback)", usage.Usage.CompletionTokens)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Phase 4 (real-streaming default) — live-mode tests
// ─────────────────────────────────────────────────────────────────────────────

// gatedStreamProvider is a provider whose event channel the test feeds
// manually, so the stream can be held open mid-flight (the internal-path
// analogue of a blocking upstream).
type gatedStreamProvider struct {
	events chan providers.StreamEvent
}

func (p *gatedStreamProvider) Name() string { return "gated" }

func (p *gatedStreamProvider) ChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	return nil, errors.New("gated provider: ChatCompletion not implemented")
}

func (p *gatedStreamProvider) StreamChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	return p.events, nil
}

func (p *gatedStreamProvider) IsRetryable(err error) bool { return false }

// TestUltimateInternal_LiveStream_ForwardBytesBeforeCompletion asserts
// the internal eventCh path forwards `data: ...` events as they arrive,
// BEFORE the `case "done"` terminator closes the channel (Phase 4 /
// task 4.3). Events are fed from a goroutine that runs concurrently
// with client.Get — the handler's first receive unblocks the feeder
// as soon as it is scheduled, then a live write flushes the SSE
// headers and client.Get returns; the first `data: ...` line is then
// observable on the client side while the channel is still open.
func TestUltimateInternal_LiveStream_ForwardBytesBeforeCompletion(t *testing.T) {
	events := make(chan providers.StreamEvent)
	p := &gatedStreamProvider{events: events}
	h := NewHandler(newMockConfigManager(), newMockModelsConfig(), nil, nil)

	handlerErr := make(chan error, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No options = live mode (the new default).
		_, err := h.handleInternalStream(r.Context(), p, &providers.ChatCompletionRequest{Model: "test-model"}, w, "test-model", nil)
		handlerErr <- err
	}))
	defer proxy.Close()

	// Feeder goroutine — the feeder's ONLY responsibility is to send the
	// events and close. The feeder does NOT need to be coordinated with
	// client.Get beyond starting concurrently: the handler can't receive
	// until it is scheduled, so the first send blocks; once it does,
	// the handler writes+flushes, client.Get returns.
	feedDone := make(chan struct{})
	go func() {
		defer close(feedDone)
		events <- providers.StreamEvent{Type: "content", Content: "early"}
		events <- providers.StreamEvent{Type: "done", FinishReason: "stop", Response: &providers.ChatCompletionResponse{
			Usage: providers.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		}}
		close(events)
	}()

	client := &http.Client{Timeout: 5 * time.Second}
	cr, err := client.Get(proxy.URL)
	if err != nil {
		t.Fatalf("client Get: %v (if this is a timeout, the path buffered instead of live-forwarding)", err)
	}
	defer cr.Body.Close()

	reader := bufio.NewReader(cr.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("reading first data event: %v", err)
	}
	if !strings.HasPrefix(line, "data: ") || !strings.Contains(line, "early") {
		t.Fatalf("first event = %q, want a data: line carrying %q", line, "early")
	}

	sawDone := false
	for !sawDone {
		l, rerr := reader.ReadString('\n')
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			t.Fatalf("draining stream: %v", rerr)
		}
		if strings.Contains(l, "[DONE]") {
			sawDone = true
		}
	}
	if !sawDone {
		t.Error("stream finished without [DONE]")
	}
	<-feedDone
	if err := <-handlerErr; err != nil {
		t.Errorf("handleInternalStream: %v", err)
	}
}

// TestUltimateCapture_LiveMode_IdenticalToBuffered_Internal is the
// internal-path half of the C1 regression pin (plan task 4.4): the
// done event carries NO usage, so Usage comes exclusively from the
// tokenizer-fallback estimator fed by buf.Bytes(). Live and buffered
// runs of the SAME event sequence must return identical
// ExecuteResults; a regressed capture-side accumulator reports
// live CompletionTokens=0 while buffered keeps the correct count.
func TestUltimateCapture_LiveMode_IdenticalToBuffered_Internal(t *testing.T) {
	newEvents := func() []providers.StreamEvent {
		return []providers.StreamEvent{
			{Type: "thinking", ReasoningContent: "think "},
			{Type: "content", Content: "hello "},
			{Type: "content", Content: "there"},
			{Type: "tool_call", ToolCalls: []providers.ToolCall{
				{ID: "call_1", Type: "function", Function: providers.ToolCallFunction{Name: "get_weather", Arguments: "{\"city\":"}},
			}},
			{Type: "tool_call", ToolCalls: []providers.ToolCall{
				{ID: "call_1", Function: providers.ToolCallFunction{Name: "", Arguments: "\"NYC\"}"}},
			}},
			// done WITHOUT Response ⇒ no usage ⇒ fallback estimator.
			{Type: "done", FinishReason: "tool_calls"},
		}
	}

	run := func(opts ...ExecuteOptions) (*ExecuteResult, string) {
		h := NewHandler(newMockConfigManager(), newMockModelsConfig(), nil, nil)
		p := &mockProvider{name: "mock", streamEvents: newEvents()}
		w := httptest.NewRecorder()
		result, err := h.handleInternalStream(context.Background(), p, &providers.ChatCompletionRequest{Model: "test-model"}, w, "test-model", nil, opts...)
		if err != nil {
			t.Fatalf("handleInternalStream: %v", err)
		}
		return result, w.Body.String()
	}

	buffered, bufferedBody := run(ExecuteOptions{BufferMode: true})
	live, liveBody := run() // default = live

	if buffered.Content != live.Content {
		t.Errorf("Content: buffered=%q live=%q", buffered.Content, live.Content)
	}
	if buffered.Thinking != live.Thinking {
		t.Errorf("Thinking: buffered=%q live=%q", buffered.Thinking, live.Thinking)
	}
	if len(buffered.ToolCalls) != len(live.ToolCalls) {
		t.Fatalf("ToolCalls: buffered=%d live=%d", len(buffered.ToolCalls), len(live.ToolCalls))
	}
	for i := range buffered.ToolCalls {
		if buffered.ToolCalls[i] != live.ToolCalls[i] {
			t.Errorf("ToolCalls[%d]: buffered=%+v live=%+v", i, buffered.ToolCalls[i], live.ToolCalls[i])
		}
	}
	if buffered.Usage == nil || live.Usage == nil {
		t.Fatalf("Usage nil: buffered=%v live=%v", buffered.Usage, live.Usage)
	}
	// C1 pin: fallback estimator (no usage in done) — buffered sanity
	// non-zero, live EXACT match; regression mode: live=0.
	if buffered.Usage.CompletionTokens == 0 {
		t.Fatalf("buffered CompletionTokens=0 — fixture lost its completion text")
	}
	if live.Usage.CompletionTokens != buffered.Usage.CompletionTokens {
		t.Errorf("C1 REGRESSION: CompletionTokens live=%d buffered=%d", live.Usage.CompletionTokens, buffered.Usage.CompletionTokens)
	}
	if live.Usage.PromptTokens != buffered.Usage.PromptTokens ||
		live.Usage.TotalTokens != buffered.Usage.TotalTokens {
		t.Errorf("Usage mismatch: live=%+v buffered=%+v", live.Usage, buffered.Usage)
	}
	if normalizeSSEIDs(bufferedBody) != normalizeSSEIDs(liveBody) {
		t.Errorf("wire bytes diverged between modes (IDs normalized):\n buffered=%q\n live=%q",
			normalizeSSEIDs(bufferedBody), normalizeSSEIDs(liveBody))
	}
}

// TestUltimate_HeartbeatRaceVsLiveRelay (approver iteration 001,
// Blocking 2) runs a chunked live stream against a SHORTENED heartbeat
// interval under `go test -race`: Execute spawns the SSE heartbeat
// goroutine while the live relay writes per-event — any unsynchronized
// w.Write pair is a data race the detector fails the test on. Chunks
// arrive at intervals ≫ the tick so several heartbeats fire between
// chunks, maximizing the interleaving window. Also asserts final body
// integrity: heartbeat lines are SSE-legal comments and every data
// line survives uncorrupted.
//
// During development the un-mutexed form was verified to be caught by
// -race (see the Phase 4 report); the test is committed in the FIXED
// form.
func TestUltimate_HeartbeatRaceVsLiveRelay(t *testing.T) {
	// Shorten the heartbeat interval (production 15s → 50ms), restored
	// on cleanup. HeartbeatInterval is a package var for exactly this.
	origInterval := HeartbeatInterval
	HeartbeatInterval = 50 * time.Millisecond
	t.Cleanup(func() { HeartbeatInterval = origInterval })

	events := make(chan providers.StreamEvent)
	p := &gatedStreamProvider{events: events}

	origNewProvider := newProviderClient
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		return p, nil
	}
	t.Cleanup(func() { newProviderClient = origNewProvider })

	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddInternalModel("ultimate-model", "openai", "test-key", "", "gpt-4")
	h := NewHandler(cfg, modelsCfg, nil, nil)

	handlerErr := make(chan error, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headersSent := true
		_, err := h.Execute(r.Context(), w, r, map[string]interface{}{
			"model":    "ultimate-model",
			"stream":   true,
			"messages": []map[string]interface{}{{"role": "user", "content": "hi"}},
		}, "original-model", "race-test-hash", &headersSent, nil, "")
		handlerErr <- err
	}))
	defer proxy.Close()

	const chunks = 10
	// Feeder runs concurrently with client.Get: the handler cannot
	// receive until it is scheduled, so the first send blocks; once it
	// does, the handler writes+flushes headers and client.Get returns.
	feedDone := make(chan struct{})
	go func() {
		defer close(feedDone)
		for i := 0; i < chunks; i++ {
			events <- providers.StreamEvent{Type: "content", Content: fmt.Sprintf("chunk-%d ", i)}
			// Interval ≫ tick (150ms ≫ 50ms): ~3 heartbeats between chunks.
			time.Sleep(150 * time.Millisecond)
		}
		events <- providers.StreamEvent{Type: "done", FinishReason: "stop", Response: &providers.ChatCompletionResponse{
			Usage: providers.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
		}}
		close(events)
	}()

	client := &http.Client{Timeout: 30 * time.Second}
	cr, err := client.Get(proxy.URL)
	if err != nil {
		t.Fatalf("client Get: %v", err)
	}

	var body bytes.Buffer
	if _, err := io.Copy(&body, cr.Body); err != nil {
		t.Fatalf("reading body: %v", err)
	}
	cr.Body.Close()
	<-feedDone
	if err := <-handlerErr; err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Body integrity: heartbeat comments are SSE-legal; every data line
	// must parse — a torn write (heartbeat spliced into a data line)
	// fails the JSON unmarshal below.
	heartbeats := 0
	dataLines := 0
	sawDone := false
	for _, line := range strings.Split(body.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ":") {
			heartbeats++
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			t.Fatalf("corrupt SSE line (torn write?): %q", line)
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			sawDone = true
			continue
		}
		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("torn data line %q: %v", line, err)
		}
		dataLines++
	}
	if heartbeats == 0 {
		t.Error("no heartbeat interleaved — the race window was not exercised")
	}
	if !sawDone {
		t.Error("missing [DONE]")
	}
	if dataLines < chunks+1 {
		t.Errorf("data lines = %d, want >= %d", dataLines, chunks+1)
	}
	for i := 0; i < chunks; i++ {
		if !strings.Contains(body.String(), fmt.Sprintf("chunk-%d ", i)) {
			t.Errorf("content chunk %d lost or corrupted", i)
		}
	}
}
