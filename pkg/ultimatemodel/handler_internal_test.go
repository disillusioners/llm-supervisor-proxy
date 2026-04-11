package ultimatemodel

import (
	"context"
	"encoding/json"
	"errors"
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
	h := NewHandler(cfg, modelsCfg, nil)

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
		t.Fatal("Expected usage to be returned")
	}

	if result.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", result.PromptTokens)
	}
	if result.CompletionTokens != 5 {
		t.Errorf("CompletionTokens = %d, want 5", result.CompletionTokens)
	}
	if result.TotalTokens != 15 {
		t.Errorf("TotalTokens = %d, want 15", result.TotalTokens)
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
	h := NewHandler(cfg, modelsCfg, nil)

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
	h := NewHandler(cfg, modelsCfg, nil)

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

	if result.PromptTokens != 0 {
		t.Errorf("PromptTokens = %d, want 0", result.PromptTokens)
	}
}

// --- Tests for handleInternalStream ---

func TestHandleInternalStream_ContentAndDone(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

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
	usage, err := h.handleInternalStream(context.Background(), p, req, w, "test-model", nil)

	if err != nil {
		t.Fatalf("handleInternalStream returned error: %v", err)
	}

	// Check usage was extracted
	if usage == nil {
		t.Fatal("Expected usage to be returned")
	}
	if usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", usage.PromptTokens)
	}

	// Check SSE headers
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/event-stream")
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-cache")
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
	h := NewHandler(cfg, modelsCfg, nil)

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
	usage, err := h.handleInternalStream(context.Background(), p, req, w, "test-model", nil)

	if err != nil {
		t.Fatalf("handleInternalStream returned error: %v", err)
	}

	if usage == nil {
		t.Fatal("Expected usage to be returned")
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
	h := NewHandler(cfg, modelsCfg, nil)

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
	_, err := h.handleInternalStream(context.Background(), p, req, w, "test-model", nil)

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
	h := NewHandler(cfg, modelsCfg, nil)

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
	_, err := h.handleInternalStream(context.Background(), p, req, w, "test-model", nil)

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
	h := NewHandler(cfg, modelsCfg, nil)

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
	h := NewHandler(cfg, modelsCfg, nil)

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
	h := NewHandler(cfg, modelsCfg, nil)

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
	h := NewHandler(cfg, modelsCfg, nil)

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
	h := NewHandler(cfg, modelsCfg, nil)

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
	h := NewHandler(cfg, modelsCfg, nil)

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
	h := NewHandler(cfg, modelsCfg, nil)

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

func TestConvertRequest_ToolChoice(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

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
	h := NewHandler(cfg, modelsCfg, nil)

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

	h := NewHandler(cfg, modelsCfg, nil)

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
	_, err := h.executeInternal(context.Background(), w, body, requestBodyBytes, modelCfg, false)

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
	h := NewHandler(cfg, modelsCfg, nil)

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
	h := NewHandler(cfg, modelsCfg, nil)

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
	usage, err := h.handleInternalStream(context.Background(), p, req, w, "test-model", nil)

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
	h := NewHandler(cfg, modelsCfg, nil)

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
	_, err := h.handleInternalStream(context.Background(), p, req, w, "test-model", nil)

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
	h := NewHandler(cfg, modelsCfg, nil)

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
	h := NewHandler(cfg, modelsCfg, nil)

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
	h := NewHandler(cfg, modelsCfg, nil)

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
	usage, err := h.handleInternalStream(context.Background(), p, req, w, "test-model", nil)

	if err != nil {
		t.Fatalf("handleInternalStream returned error: %v", err)
	}

	if usage == nil {
		t.Fatal("Expected usage")
	}

	// Verify usage from done event was extracted correctly
	expected := &store.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150}
	if usage.PromptTokens != expected.PromptTokens {
		t.Errorf("PromptTokens = %d, want %d", usage.PromptTokens, expected.PromptTokens)
	}
	if usage.CompletionTokens != expected.CompletionTokens {
		t.Errorf("CompletionTokens = %d, want %d", usage.CompletionTokens, expected.CompletionTokens)
	}
	if usage.TotalTokens != expected.TotalTokens {
		t.Errorf("TotalTokens = %d, want %d", usage.TotalTokens, expected.TotalTokens)
	}
}

func TestHandleInternalStream_NoUsageInDone(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	// Create mock provider with no usage in done event
	p := &mockProvider{
		name: "mock",
		streamEvents: []providers.StreamEvent{
			{Type: "content", Content: "Hello"},
			{Type: "done", FinishReason: "stop", Response: nil}, // No usage
		},
	}

	req := &providers.ChatCompletionRequest{
		Model: "test-model",
	}

	w := httptest.NewRecorder()
	usage, err := h.handleInternalStream(context.Background(), p, req, w, "test-model", nil)

	if err != nil {
		t.Fatalf("handleInternalStream returned error: %v", err)
	}

	// With fallback token counting, usage is now a zero struct instead of nil
	// when no usage is provided in the done event (nil requestBodyBytes means fallback counts 0)
	if usage == nil {
		t.Errorf("Usage should not be nil - fallback should return zero-usage struct")
	}
	if usage.PromptTokens != 0 {
		t.Errorf("PromptTokens = %d, want 0 (no requestBodyBytes for fallback)", usage.PromptTokens)
	}
	if usage.CompletionTokens != 0 {
		t.Errorf("CompletionTokens = %d, want 0 (empty stream for fallback)", usage.CompletionTokens)
	}
}
