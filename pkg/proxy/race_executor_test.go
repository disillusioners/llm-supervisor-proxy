package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/normalizers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

func TestIntValue(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected int
	}{
		{
			name:     "int value",
			input:    42,
			expected: 42,
		},
		{
			name:     "float64 value",
			input:    3.14159,
			expected: 3,
		},
		{
			name:     "int64 value",
			input:    int64(100),
			expected: 100,
		},
		{
			name:     "string numeric value",
			input:    "123",
			expected: 0,
		},
		{
			name:     "nil value",
			input:    nil,
			expected: 0,
		},
		{
			name:     "bool value",
			input:    true,
			expected: 0,
		},
		{
			name:     "negative float64 value",
			input:    -5.7,
			expected: -5,
		},
		{
			name:     "negative int value",
			input:    -10,
			expected: -10,
		},
		{
			name:     "large float64 value",
			input:    1e10,
			expected: 10000000000,
		},
		{
			name:     "empty string",
			input:    "",
			expected: 0,
		},
		{
			name:     "slice value",
			input:    []int{1, 2, 3},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := intValue(tt.input)
			if result != tt.expected {
				t.Errorf("intValue(%v) = %d, want %d", tt.input, result, tt.expected)
			}
		})
	}
}

func TestExtractUsageFromSSEChunk(t *testing.T) {
	tests := []struct {
		name           string
		line           string
		wantUsageSet   bool
		wantPrompt     int
		wantCompletion int
		wantTotal      int
	}{
		{
			name:           "valid SSE line with usage",
			line:           `data: {"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}}`,
			wantUsageSet:   true,
			wantPrompt:     100,
			wantCompletion: 50,
			wantTotal:      150,
		},
		{
			name:           "line without usage",
			line:           `data: {"choices":[{"index":0,"delta":{"content":"Hello"}}]}`,
			wantUsageSet:   false,
			wantPrompt:     0,
			wantCompletion: 0,
			wantTotal:      0,
		},
		{
			name:         "empty line",
			line:         "",
			wantUsageSet: false,
		},
		{
			name:         "short line without data prefix",
			line:         "hello",
			wantUsageSet: false,
		},
		{
			name:         "malformed JSON",
			line:         "data: {invalid json",
			wantUsageSet: false,
		},
		{
			name:         "usage with only prompt_tokens",
			line:         `data: {"usage":{"prompt_tokens":100}}`,
			wantUsageSet: false, // completion and total are 0, so won't be set
		},
		{
			name:         "usage with zero values",
			line:         `data: {"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
			wantUsageSet: false, // all zeros, won't be set
		},
		{
			name:           "usage with only some fields non-zero",
			line:           `data: {"usage":{"prompt_tokens":50,"completion_tokens":0,"total_tokens":50}}`,
			wantUsageSet:   true,
			wantPrompt:     50,
			wantCompletion: 0,
			wantTotal:      50,
		},
		{
			name:           "usage missing completion_tokens",
			line:           `data: {"usage":{"prompt_tokens":50,"total_tokens":100}}`,
			wantUsageSet:   true,
			wantPrompt:     50,
			wantCompletion: 0,
			wantTotal:      100,
		},
		{
			name:         "data prefix only",
			line:         "data: ",
			wantUsageSet: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newUpstreamRequest(0, modelTypeMain, "gpt-4", 1024)

			extractUsageFromSSEChunk(req, []byte(tt.line))

			if tt.wantUsageSet {
				usage := req.GetUsage()
				if usage == nil {
					t.Errorf("extractUsageFromSSEChunk() did not set usage, want non-nil")
					return
				}
				if usage.PromptTokens != tt.wantPrompt {
					t.Errorf("PromptTokens = %d, want %d", usage.PromptTokens, tt.wantPrompt)
				}
				if usage.CompletionTokens != tt.wantCompletion {
					t.Errorf("CompletionTokens = %d, want %d", usage.CompletionTokens, tt.wantCompletion)
				}
				if usage.TotalTokens != tt.wantTotal {
					t.Errorf("TotalTokens = %d, want %d", usage.TotalTokens, tt.wantTotal)
				}
			}
		})
	}
}

func TestGetKeys(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		wantLen  int
		wantKeys []string
	}{
		{
			name: "normal map",
			input: map[string]interface{}{
				"key1": "value1",
				"key2": 42,
				"key3": true,
			},
			wantLen:  3,
			wantKeys: []string{"key1", "key2", "key3"},
		},
		{
			name:     "empty map",
			input:    map[string]interface{}{},
			wantLen:  0,
			wantKeys: []string{},
		},
		{
			name:     "nil map",
			input:    nil,
			wantLen:  0,
			wantKeys: []string{},
		},
		{
			name: "nested map",
			input: map[string]interface{}{
				"outer": map[string]interface{}{
					"inner": "value",
				},
				"simple": "value",
			},
			wantLen:  2,
			wantKeys: []string{"outer", "simple"},
		},
		{
			name: "single key",
			input: map[string]interface{}{
				"only": "one",
			},
			wantLen:  1,
			wantKeys: []string{"only"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getKeys(tt.input)
			if len(result) != tt.wantLen {
				t.Errorf("getKeys() returned %d keys, want %d", len(result), tt.wantLen)
			}

			// Check that all expected keys are present (order-independent)
			for _, wantKey := range tt.wantKeys {
				found := false
				for _, gotKey := range result {
					if gotKey == wantKey {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("getKeys() missing key %q", wantKey)
				}
			}
		})
	}
}

func TestConvertToProviderRequest(t *testing.T) {
	tests := []struct {
		name           string
		body           map[string]interface{}
		model          string
		wantModel      string
		wantMsgCount   int
		wantTemp       *float64
		wantMaxTokens  *int
		wantStream     bool
		wantToolsCount int
	}{
		{
			name: "valid body with all fields",
			body: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{
						"role":    "user",
						"content": "Hello",
					},
				},
				"temperature": 0.7,
				"max_tokens":  float64(100),
				"stream":      true,
				"tools": []interface{}{
					map[string]interface{}{
						"type": "function",
						"function": map[string]interface{}{
							"name":        "get_weather",
							"description": "Get weather",
							"parameters":  map[string]interface{}{},
						},
					},
				},
			},
			model:          "gpt-4",
			wantModel:      "gpt-4",
			wantMsgCount:   1,
			wantTemp:       floatPtr(0.7),
			wantMaxTokens:  intPtr(100),
			wantStream:     true,
			wantToolsCount: 1,
		},
		{
			name:           "missing messages",
			body:           map[string]interface{}{},
			model:          "gpt-4",
			wantModel:      "gpt-4",
			wantMsgCount:   0,
			wantTemp:       nil,
			wantMaxTokens:  nil,
			wantStream:     false,
			wantToolsCount: 0,
		},
		{
			name:           "nil body",
			body:           nil,
			model:          "gpt-4",
			wantModel:      "gpt-4",
			wantMsgCount:   0,
			wantTemp:       nil,
			wantMaxTokens:  nil,
			wantStream:     false,
			wantToolsCount: 0,
		},
		{
			name: "empty model",
			body: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{
						"role":    "user",
						"content": "Hello",
					},
				},
			},
			model:          "",
			wantModel:      "",
			wantMsgCount:   1,
			wantTemp:       nil,
			wantMaxTokens:  nil,
			wantStream:     false,
			wantToolsCount: 0,
		},
		{
			name: "complex nested body with tool_calls",
			body: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{
						"role":    "assistant",
						"content": "I'll help you",
					},
					map[string]interface{}{
						"role": "assistant",
						"tool_calls": []interface{}{
							map[string]interface{}{
								"id":   "call_123",
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
						"tool_call_id": "call_123",
						"content":      "sunny, 72F",
					},
				},
				"temperature": 1.0,
				"tool_choice": "auto",
			},
			model:          "gpt-4",
			wantModel:      "gpt-4",
			wantMsgCount:   3,
			wantTemp:       floatPtr(1.0),
			wantStream:     false,
			wantToolsCount: 0,
		},
		{
			name: "multimodal content",
			body: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{
						"role": "user",
						"content": []interface{}{
							map[string]interface{}{
								"type": "text",
								"text": "What's in this image?",
							},
							map[string]interface{}{
								"type": "image_url",
								"image_url": map[string]interface{}{
									"url":    "https://example.com/image.png",
									"detail": "high",
								},
							},
						},
					},
				},
			},
			model:          "gpt-4-vision",
			wantModel:      "gpt-4-vision",
			wantMsgCount:   1,
			wantToolsCount: 0,
		},
		{
			name: "extra field passthrough",
			body: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{
						"role":    "user",
						"content": "Hello",
					},
				},
				"extra": map[string]interface{}{
					"custom_field": "value",
				},
			},
			model:          "gpt-4",
			wantModel:      "gpt-4",
			wantMsgCount:   1,
			wantToolsCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := convertToProviderRequest(tt.body, tt.model)
			if err != nil {
				t.Fatalf("convertToProviderRequest() error = %v", err)
			}

			if req.Model != tt.wantModel {
				t.Errorf("Model = %q, want %q", req.Model, tt.wantModel)
			}

			if len(req.Messages) != tt.wantMsgCount {
				t.Errorf("Messages count = %d, want %d", len(req.Messages), tt.wantMsgCount)
			}

			if tt.wantTemp != nil && (req.Temperature == nil || *req.Temperature != *tt.wantTemp) {
				t.Errorf("Temperature = %v, want %v", req.Temperature, tt.wantTemp)
			}
			if tt.wantTemp == nil && req.Temperature != nil {
				t.Errorf("Temperature = %v, want nil", *req.Temperature)
			}

			if tt.wantMaxTokens != nil && (req.MaxTokens == nil || *req.MaxTokens != *tt.wantMaxTokens) {
				t.Errorf("MaxTokens = %v, want %v", req.MaxTokens, tt.wantMaxTokens)
			}
			if tt.wantMaxTokens == nil && req.MaxTokens != nil {
				t.Errorf("MaxTokens = %v, want nil", *req.MaxTokens)
			}

			if req.Stream != tt.wantStream {
				t.Errorf("Stream = %v, want %v", req.Stream, tt.wantStream)
			}

			if len(req.Tools) != tt.wantToolsCount {
				t.Errorf("Tools count = %d, want %d", len(req.Tools), tt.wantToolsCount)
			}
		})
	}
}

func TestRepairToolCallArgumentsInNonStreamingResponse(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		config       toolrepair.Config
		wantModified bool
	}{
		{
			name: "valid JSON with tool_calls needing repair - disabled config",
			body: `{"choices":[{"message":{"tool_calls":[{"function":{"name":"test","arguments":"{invalid"}},{"function":{"name":"test2","arguments":"{}"}}]}}]}`,
			config: toolrepair.Config{
				Enabled:    false,
				Strategies: []string{},
			},
			wantModified: false,
		},
		{
			name: "valid JSON with tool_calls needing repair - enabled config with library_repair",
			body: `{"choices":[{"message":{"tool_calls":[{"function":{"name":"test","arguments":"{invalid"}}]}}]}`,
			config: toolrepair.Config{
				Enabled:          true,
				Strategies:       []string{"trim_trailing_garbage", "extract_json", "library_repair"},
				MaxArgumentsSize: 100 * 1024,
			},
			wantModified: true,
		},
		{
			name: "valid JSON with tool_calls needing repair - extract_json only cannot fix",
			body: `{"choices":[{"message":{"tool_calls":[{"function":{"name":"test","arguments":"{invalid"}}]}}]}`,
			config: toolrepair.Config{
				Enabled:          true,
				Strategies:       []string{"extract_json"},
				MaxArgumentsSize: 100 * 1024,
			},
			wantModified: false,
		},
		{
			name: "no tool_calls",
			body: `{"choices":[{"message":{"content":"Hello"}}]}`,
			config: toolrepair.Config{
				Enabled:    true,
				Strategies: []string{"extract_json"},
			},
			wantModified: false,
		},
		{
			name: "malformed JSON",
			body: `not valid json`,
			config: toolrepair.Config{
				Enabled:    true,
				Strategies: []string{"extract_json"},
			},
			wantModified: false,
		},
		{
			name: "empty body",
			body: `{}`,
			config: toolrepair.Config{
				Enabled:    true,
				Strategies: []string{"extract_json"},
			},
			wantModified: false,
		},
		{
			name: "tool_calls with valid arguments",
			body: `{"choices":[{"message":{"tool_calls":[{"function":{"name":"test","arguments":"{}"}}]}}]}`,
			config: toolrepair.Config{
				Enabled:    true,
				Strategies: []string{"extract_json"},
			},
			wantModified: false,
		},
		{
			name: "empty tool_calls array",
			body: `{"choices":[{"message":{"tool_calls":[]}}]}`,
			config: toolrepair.Config{
				Enabled:    true,
				Strategies: []string{"extract_json"},
			},
			wantModified: false,
		},
		{
			name: "no choices",
			body: `{"model":"gpt-4"}`,
			config: toolrepair.Config{
				Enabled:    true,
				Strategies: []string{"extract_json"},
			},
			wantModified: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, modified := repairToolCallArgumentsInNonStreamingResponse([]byte(tt.body), tt.config)
			if modified != tt.wantModified {
				t.Errorf("modified = %v, want %v", modified, tt.wantModified)
			}
			// If not modified, result should be same as input
			if !modified && string(result) != tt.body {
				t.Errorf("expected unchanged body when not modified")
			}
		})
	}
}

func TestGetNormalizerDescription(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "fix_empty_role",
			input:    "fix_empty_role",
			expected: "Fixed empty role field in delta (changed to 'assistant')",
		},
		{
			name:     "fix_tool_call_index",
			input:    "fix_tool_call_index",
			expected: "Added missing index field to tool_calls",
		},
		{
			name:     "tool_call_arguments_repair",
			input:    "tool_call_arguments_repair",
			expected: "Repaired malformed JSON in tool_call arguments",
		},
		{
			name:     "unknown normalizer",
			input:    "some_unknown_normalizer",
			expected: "Normalized stream chunk",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "Normalized stream chunk",
		},
		{
			name:     "another unknown normalizer",
			input:    "custom_normalizer",
			expected: "Normalized stream chunk",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getNormalizerDescription(tt.input)
			if result != tt.expected {
				t.Errorf("getNormalizerDescription(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// Helper functions for creating pointers
func floatPtr(v float64) *float64 {
	return &v
}

func intPtr(v int) *int {
	return &v
}

// =============================================================================
// Mock Provider for Internal Tests
// =============================================================================

// mockProvider implements providers.Provider for testing internal handlers
type mockProvider struct {
	name               string
	chatCompletionResp *providers.ChatCompletionResponse
	chatCompletionErr  error
	streamEvents       []providers.StreamEvent
	streamErr          error
	mu                 sync.Mutex
}

func newMockProvider() *mockProvider {
	return &mockProvider{name: "mock"}
}

func (m *mockProvider) Name() string {
	return m.name
}

func (m *mockProvider) ChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	if m.chatCompletionErr != nil {
		return nil, m.chatCompletionErr
	}
	return m.chatCompletionResp, nil
}

func (m *mockProvider) StreamChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	if m.streamErr != nil {
		return nil, m.streamErr
	}

	ch := make(chan providers.StreamEvent, len(m.streamEvents))

	// Copy events to avoid race conditions
	events := make([]providers.StreamEvent, len(m.streamEvents))
	copy(events, m.streamEvents)

	go func() {
		defer close(ch)
		for _, event := range events {
			select {
			case ch <- event:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, nil
}

func (m *mockProvider) IsRetryable(err error) bool {
	var providerErr *providers.ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.Retryable
	}
	return false
}

func (m *mockProvider) setStreamEvents(events []providers.StreamEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streamEvents = events
}

// =============================================================================
// Test Helpers
// =============================================================================

// Note: newTestConfigSnapshot is already defined in race_coordinator_test.go

func newTestUpstreamRequest(modelID string) *upstreamRequest {
	return newUpstreamRequest(0, modelTypeMain, modelID, 1024*1024)
}

// Helper to create an http.Response with a body
func newResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
	}
}

// Helper to create a minimal ChatCompletionRequest
func newChatCompletionRequest() *providers.ChatCompletionRequest {
	return &providers.ChatCompletionRequest{
		Model: "test-model",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "Hello"},
		},
	}
}

// errorReader is an io.Reader that always returns an error
type errorReader struct{}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, errors.New("read error")
}

func (e *errorReader) Close() error {
	return nil
}

// =============================================================================
// Tests for handleNonStreamingResponse
// =============================================================================

func TestHandleNonStreamingResponse_Success(t *testing.T) {
	cfg := newTestConfigSnapshot("test-model")
	req := newTestUpstreamRequest("test-model")

	body := `{"id":"chatcmpl-123","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}]}`
	resp := newResponse(body)

	err := handleNonStreamingResponse(context.Background(), cfg, resp, req, []byte(`{"messages":[{"role":"user","content":"test"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify buffer contains the response
	chunks, _ := req.buffer.GetChunksFrom(0)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}

	// Verify content
	chunkStr := string(chunks[0])
	if !strings.Contains(chunkStr, `"content":"Hello"`) {
		t.Errorf("expected chunk to contain response, got: %s", chunkStr)
	}
}

func TestHandleNonStreamingResponse_WithUsage(t *testing.T) {
	cfg := newTestConfigSnapshot("test-model")
	req := newTestUpstreamRequest("test-model")

	body := `{"id":"chatcmpl-123","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
	resp := newResponse(body)

	err := handleNonStreamingResponse(context.Background(), cfg, resp, req, []byte(`{"messages":[{"role":"user","content":"test"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify usage was extracted
	usage := req.GetUsage()
	if usage == nil {
		t.Fatal("expected usage to be set")
	}
	if usage.PromptTokens != 10 {
		t.Errorf("expected prompt_tokens=10, got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != 5 {
		t.Errorf("expected completion_tokens=5, got %d", usage.CompletionTokens)
	}
	if usage.TotalTokens != 15 {
		t.Errorf("expected total_tokens=15, got %d", usage.TotalTokens)
	}
}

func TestHandleNonStreamingResponse_WithoutUsage(t *testing.T) {
	cfg := newTestConfigSnapshot("test-model")
	req := newTestUpstreamRequest("test-model")

	body := `{"id":"chatcmpl-123","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}]}`
	resp := newResponse(body)

	err := handleNonStreamingResponse(context.Background(), cfg, resp, req, []byte(`{"messages":[{"role":"user","content":"test"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify usage IS set (fallback token counting kicks in when provider doesn't return usage)
	usage := req.GetUsage()
	if usage == nil {
		t.Fatal("expected usage to be set via fallback token counting, got nil")
	}
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0 {
		t.Errorf("expected at least some tokens to be counted via fallback, got prompt=%d completion=%d", usage.PromptTokens, usage.CompletionTokens)
	}
}

func TestHandleNonStreamingResponse_EmptyUsage(t *testing.T) {
	cfg := newTestConfigSnapshot("test-model")
	req := newTestUpstreamRequest("test-model")

	// Response with zero usage values - fallback token counting should kick in
	body := `{"id":"chatcmpl-123","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`
	resp := newResponse(body)

	err := handleNonStreamingResponse(context.Background(), cfg, resp, req, []byte(`{"messages":[{"role":"user","content":"test"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify usage IS set (fallback token counting kicks in when provider returns zero usage)
	usage := req.GetUsage()
	if usage == nil {
		t.Fatal("expected usage to be set via fallback token counting, got nil")
	}
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0 {
		t.Errorf("expected at least some tokens to be counted via fallback, got prompt=%d completion=%d", usage.PromptTokens, usage.CompletionTokens)
	}
}

func TestHandleNonStreamingResponse_ReadError(t *testing.T) {
	cfg := newTestConfigSnapshot("test-model")
	req := newTestUpstreamRequest("test-model")

	// Create a response that fails to read
	resp := &http.Response{
		StatusCode: 200,
		Body:       &errorReader{},
		Header:     http.Header{},
	}

	err := handleNonStreamingResponse(context.Background(), cfg, resp, req, []byte(`{"messages":[{"role":"user","content":"test"}]}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "read error") {
		t.Errorf("expected 'read error' in error message, got: %v", err)
	}
}

func TestHandleNonStreamingResponse_InvalidJSON(t *testing.T) {
	cfg := newTestConfigSnapshot("test-model")
	req := newTestUpstreamRequest("test-model")

	// Invalid JSON - should still add to buffer, just won't extract usage
	body := `not valid json`
	resp := newResponse(body)

	err := handleNonStreamingResponse(context.Background(), cfg, resp, req, []byte(`{"messages":[{"role":"user","content":"test"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Buffer should still contain the invalid JSON
	chunks, _ := req.buffer.GetChunksFrom(0)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
}

func TestHandleNonStreamingResponse_WithToolRepair(t *testing.T) {
	cfg := newTestConfigSnapshot("test-model")
	cfg.ToolRepair = toolrepair.Config{
		Enabled:    true,
		Strategies: []string{"extract_json"},
	}
	req := newTestUpstreamRequest("test-model")

	// Response with malformed tool_call arguments
	body := `{"id":"chatcmpl-123","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"Let me check that","tool_calls":[{"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":"{bad json}"}}]},"finish_reason":"tool_calls"}]}`
	resp := newResponse(body)

	err := handleNonStreamingResponse(context.Background(), cfg, resp, req, []byte(`{"messages":[{"role":"user","content":"test"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should still add to buffer
	chunks, _ := req.buffer.GetChunksFrom(0)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
}

// =============================================================================
// Tests for handleStreamingResponse
// =============================================================================

func TestHandleStreamingResponse_NormalStream(t *testing.T) {
	cfg := newTestConfigSnapshot("test-model")
	req := newTestUpstreamRequest("test-model")
	req.MarkStreaming()

	// Create a mock SSE server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// Send chunks
		chunks := []string{
			`data: {"choices":[{"delta":{"role":"assistant","content":"Hello"}}]}`,
			`data: {"choices":[{"delta":{"content":" world"}}]}`,
			`data: {"choices":[{"delta":{}}]}`,
			`data: [DONE]`,
		}

		for _, chunk := range chunks {
			fmt.Fprintln(w, chunk)
		}
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("failed to get server response: %v", err)
	}
	defer resp.Body.Close()

	err = handleStreamingResponse(context.Background(), cfg, resp, req, "openai", []byte(`{"messages":[{"role":"user","content":"test"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify buffer contains all chunks
	chunks, _ := req.buffer.GetChunksFrom(0)
	// 4 data lines sent (including [DONE])
	expectedChunks := 4
	if len(chunks) != expectedChunks {
		t.Errorf("expected %d chunks, got %d", expectedChunks, len(chunks))
	}

	// Verify content is in buffer
	allContent := req.buffer.GetAllRawBytes()
	if !strings.Contains(string(allContent), "Hello") {
		t.Error("expected 'Hello' in buffer")
	}
	if !strings.Contains(string(allContent), "world") {
		t.Error("expected 'world' in buffer")
	}
}

func TestHandleStreamingResponse_WithUsage(t *testing.T) {
	cfg := newTestConfigSnapshot("test-model")
	req := newTestUpstreamRequest("test-model")
	req.MarkStreaming()

	// Create a mock SSE server that includes usage in a chunk
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"Hello"}}]}`)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("failed to get server response: %v", err)
	}
	defer resp.Body.Close()

	err = handleStreamingResponse(context.Background(), cfg, resp, req, "openai", []byte(`{"messages":[{"role":"user","content":"test"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify usage was extracted
	usage := req.GetUsage()
	if usage == nil {
		t.Fatal("expected usage to be set")
	}
	if usage.PromptTokens != 10 {
		t.Errorf("expected prompt_tokens=10, got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != 5 {
		t.Errorf("expected completion_tokens=5, got %d", usage.CompletionTokens)
	}
	if usage.TotalTokens != 15 {
		t.Errorf("expected total_tokens=15, got %d", usage.TotalTokens)
	}
}

func TestHandleStreamingResponse_EmptyStream(t *testing.T) {
	cfg := newTestConfigSnapshot("test-model")
	req := newTestUpstreamRequest("test-model")
	req.MarkStreaming()

	// Create a mock SSE server that sends just [DONE]
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("failed to get server response: %v", err)
	}
	defer resp.Body.Close()

	err = handleStreamingResponse(context.Background(), cfg, resp, req, "openai", []byte(`{"messages":[{"role":"user","content":"test"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Buffer should contain just [DONE]
	chunks, _ := req.buffer.GetChunksFrom(0)
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(chunks))
	}
}

func TestHandleStreamingResponse_ContextCancellation(t *testing.T) {
	cfg := newTestConfigSnapshot("test-model")
	req := newTestUpstreamRequest("test-model")
	req.MarkStreaming()

	// Create a mock SSE server that sends data slowly
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// Send first chunk
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"Hello"}}]}`)

		// Flush and wait - we'll cancel before this completes
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}

		// Wait for cancellation (but don't block the test too long)
		time.Sleep(100 * time.Millisecond)

		// Try to send more (may not complete)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"world"}}]}`)
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("failed to get server response: %v", err)
	}
	defer resp.Body.Close()

	// Cancel context immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before calling handler

	err = handleStreamingResponse(ctx, cfg, resp, req, "openai", []byte(`{"messages":[{"role":"user","content":"test"}]}`))
	if err == nil {
		t.Fatal("expected error due to context cancellation")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestHandleStreamingResponse_PrematureClose(t *testing.T) {
	cfg := newTestConfigSnapshot("test-model")
	req := newTestUpstreamRequest("test-model")
	req.MarkStreaming()

	// Create a mock SSE server that closes connection without [DONE]
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// Send chunks but NO [DONE]
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"Hello"}}]}`)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"world"}}]}`)

		// Flush but don't send [DONE]
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("failed to get server response: %v", err)
	}
	defer resp.Body.Close()

	err = handleStreamingResponse(context.Background(), cfg, resp, req, "openai", []byte(`{"messages":[{"role":"user","content":"test"}]}`))
	if err == nil {
		t.Fatal("expected error due to premature close")
	}
	if !strings.Contains(err.Error(), "prematurely") {
		t.Errorf("expected 'prematurely' in error message, got: %v", err)
	}
}

func TestHandleStreamingResponse_StreamErrorChunk(t *testing.T) {
	cfg := newTestConfigSnapshot("test-model")
	req := newTestUpstreamRequest("test-model")
	req.MarkStreaming()

	// Create a mock SSE server that sends an error chunk
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// Send error chunk
		fmt.Fprintln(w, `data: {"error":{"message":"stream failed"}}`)
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("failed to get server response: %v", err)
	}
	defer resp.Body.Close()

	err = handleStreamingResponse(context.Background(), cfg, resp, req, "openai", []byte(`{"messages":[{"role":"user","content":"test"}]}`))
	if err == nil {
		t.Fatal("expected error due to stream error chunk")
	}
	if !strings.Contains(err.Error(), "streamed error") {
		t.Errorf("expected 'streamed error' in error message, got: %v", err)
	}
}

// =============================================================================
// Tests for handleInternalNonStream
// =============================================================================

func TestHandleInternalNonStream_Success(t *testing.T) {
	provider := newMockProvider()
	provider.chatCompletionResp = &providers.ChatCompletionResponse{
		ID:      "chatcmpl-123",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   "test-model",
		Choices: []providers.Choice{
			{
				Index: 0,
				Message: &providers.ChatMessage{
					Role:    "assistant",
					Content: "Hello from internal",
				},
				FinishReason: "stop",
			},
		},
		Usage: providers.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}

	req := newTestUpstreamRequest("internal-model")

	err := handleInternalNonStream(context.Background(), provider, newChatCompletionRequest(), req, "internal-model", []byte(`{"messages":[{"role":"user","content":"test"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify buffer contains the response
	chunks, _ := req.buffer.GetChunksFrom(0)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}

	// Verify response can be unmarshaled
	var resp providers.ChatCompletionResponse
	if err := json.Unmarshal(chunks[0], &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Choices[0].Message.Content != "Hello from internal" {
		t.Errorf("expected content 'Hello from internal', got: %v", resp.Choices[0].Message.Content)
	}

	// Verify usage was extracted
	usage := req.GetUsage()
	if usage == nil {
		t.Fatal("expected usage to be set")
	}
	if usage.PromptTokens != 10 {
		t.Errorf("expected prompt_tokens=10, got %d", usage.PromptTokens)
	}
}

func TestHandleInternalNonStream_ProviderError(t *testing.T) {
	provider := newMockProvider()
	provider.chatCompletionErr = &providers.ProviderError{
		Provider:   "mock",
		StatusCode: 500,
		Message:    "internal server error",
		Retryable:  true,
	}

	req := newTestUpstreamRequest("internal-model")

	err := handleInternalNonStream(context.Background(), provider, newChatCompletionRequest(), req, "internal-model", []byte(`{"messages":[{"role":"user","content":"test"}]}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Verify HTTP status was set
	if req.GetHTTPStatus() != 500 {
		t.Errorf("expected HTTP status 500, got %d", req.GetHTTPStatus())
	}
}

func TestHandleInternalNonStream_NoUsage(t *testing.T) {
	provider := newMockProvider()
	provider.chatCompletionResp = &providers.ChatCompletionResponse{
		ID:      "chatcmpl-123",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   "test-model",
		Choices: []providers.Choice{
			{
				Index: 0,
				Message: &providers.ChatMessage{
					Role:    "assistant",
					Content: "Hello",
				},
				FinishReason: "stop",
			},
		},
		// No usage - zero values
		Usage: providers.Usage{},
	}

	req := newTestUpstreamRequest("internal-model")

	err := handleInternalNonStream(context.Background(), provider, newChatCompletionRequest(), req, "internal-model", []byte(`{"messages":[{"role":"user","content":"test"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify usage IS set (fallback token counting kicks in when provider returns zero usage)
	usage := req.GetUsage()
	if usage == nil {
		t.Fatal("expected usage to be set via fallback token counting, got nil")
	}
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0 {
		t.Errorf("expected at least some tokens to be counted via fallback, got prompt=%d completion=%d", usage.PromptTokens, usage.CompletionTokens)
	}
}

// =============================================================================
// Tests for handleInternalStream
// =============================================================================

func TestHandleInternalStream_Success(t *testing.T) {
	provider := newMockProvider()
	provider.setStreamEvents([]providers.StreamEvent{
		{Type: "content", Content: "Hello"},
		{Type: "content", Content: " world"},
		{
			Type:         "done",
			FinishReason: "stop",
			Response: &providers.ChatCompletionResponse{
				Usage: providers.Usage{
					PromptTokens:     10,
					CompletionTokens: 5,
					TotalTokens:      15,
				},
			},
		},
	})

	cfg := newTestConfigSnapshot("internal-model")
	req := newTestUpstreamRequest("internal-model")

	err := handleInternalStream(
		context.Background(),
		provider,
		newChatCompletionRequest(),
		req,
		"internal-model",
		normalizers.NewContext("openai", "0"),
		cfg.ToolRepair,
		cfg.StreamDeadline,
		[]byte(`{"messages":[{"role":"user","content":"test"}]}`),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify buffer contains streamed chunks + final chunk + [DONE]
	chunks, _ := req.buffer.GetChunksFrom(0)
	if len(chunks) < 3 {
		t.Errorf("expected at least 3 chunks, got %d", len(chunks))
	}

	// Verify usage was extracted
	usage := req.GetUsage()
	if usage == nil {
		t.Fatal("expected usage to be set")
	}
	if usage.PromptTokens != 10 {
		t.Errorf("expected prompt_tokens=10, got %d", usage.PromptTokens)
	}
}

func TestHandleInternalStream_WithThinking(t *testing.T) {
	provider := newMockProvider()
	provider.setStreamEvents([]providers.StreamEvent{
		{Type: "thinking", ReasoningContent: "Hmm, let me think..."},
		{Type: "thinking", ReasoningContent: "I need to consider..."},
		{Type: "content", Content: "Here's my answer:"},
		{Type: "done", FinishReason: "stop"},
	})

	cfg := newTestConfigSnapshot("internal-model")
	req := newTestUpstreamRequest("internal-model")

	err := handleInternalStream(
		context.Background(),
		provider,
		newChatCompletionRequest(),
		req,
		"internal-model",
		normalizers.NewContext("openai", "0"),
		cfg.ToolRepair,
		cfg.StreamDeadline,
		[]byte(`{"messages":[{"role":"user","content":"test"}]}`),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify buffer has content
	allContent := req.buffer.GetAllRawBytes()
	if !strings.Contains(string(allContent), "reasoning_content") {
		t.Error("expected 'reasoning_content' in buffer for thinking chunks")
	}
}

func TestHandleInternalStream_WithToolCalls(t *testing.T) {
	provider := newMockProvider()
	provider.setStreamEvents([]providers.StreamEvent{
		{Type: "content", Content: "I'll check the weather."},
		{
			Type: "tool_call",
			ToolCalls: []providers.ToolCall{
				{
					Index: 0,
					ID:    "call_abc",
					Type:  "function",
					Function: providers.ToolCallFunction{
						Name:      "get_weather",
						Arguments: `{"location":"Boston"}`,
					},
				},
			},
		},
		{
			Type:         "done",
			FinishReason: "tool_calls",
			Response: &providers.ChatCompletionResponse{
				Usage: providers.Usage{
					PromptTokens:     10,
					CompletionTokens: 8,
					TotalTokens:      18,
				},
			},
		},
	})

	cfg := newTestConfigSnapshot("internal-model")
	req := newTestUpstreamRequest("internal-model")

	err := handleInternalStream(
		context.Background(),
		provider,
		newChatCompletionRequest(),
		req,
		"internal-model",
		normalizers.NewContext("openai", "0"),
		cfg.ToolRepair,
		cfg.StreamDeadline,
		[]byte(`{"messages":[{"role":"user","content":"test"}]}`),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify tool_calls are in buffer
	allContent := req.buffer.GetAllRawBytes()
	if !strings.Contains(string(allContent), "tool_calls") {
		t.Error("expected 'tool_calls' in buffer")
	}
	if !strings.Contains(string(allContent), "get_weather") {
		t.Error("expected 'get_weather' function name in buffer")
	}
}

func TestHandleInternalStream_ProviderError(t *testing.T) {
	provider := newMockProvider()
	provider.streamErr = &providers.ProviderError{
		Provider:   "mock",
		StatusCode: 500,
		Message:    "stream failed",
		Retryable:  true,
	}

	cfg := newTestConfigSnapshot("internal-model")
	req := newTestUpstreamRequest("internal-model")

	err := handleInternalStream(
		context.Background(),
		provider,
		newChatCompletionRequest(),
		req,
		"internal-model",
		normalizers.NewContext("openai", "0"),
		cfg.ToolRepair,
		cfg.StreamDeadline,
		[]byte(`{"messages":[{"role":"user","content":"test"}]}`),
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Verify HTTP status was set
	if req.GetHTTPStatus() != 500 {
		t.Errorf("expected HTTP status 500, got %d", req.GetHTTPStatus())
	}
}

func TestHandleInternalStream_ErrorEvent(t *testing.T) {
	provider := newMockProvider()
	provider.setStreamEvents([]providers.StreamEvent{
		{Type: "content", Content: "Hello"},
		{
			Type:  "error",
			Error: errors.New("stream error occurred"),
		},
	})

	cfg := newTestConfigSnapshot("internal-model")
	req := newTestUpstreamRequest("internal-model")

	err := handleInternalStream(
		context.Background(),
		provider,
		newChatCompletionRequest(),
		req,
		"internal-model",
		normalizers.NewContext("openai", "0"),
		cfg.ToolRepair,
		cfg.StreamDeadline,
		[]byte(`{"messages":[{"role":"user","content":"test"}]}`),
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "provider stream error") {
		t.Errorf("expected 'provider stream error' in error message, got: %v", err)
	}
}

func TestHandleInternalStream_EmptyStream(t *testing.T) {
	provider := newMockProvider()
	// No events - stream ends immediately without "done"
	provider.setStreamEvents([]providers.StreamEvent{})

	cfg := newTestConfigSnapshot("internal-model")
	req := newTestUpstreamRequest("internal-model")

	err := handleInternalStream(
		context.Background(),
		provider,
		newChatCompletionRequest(),
		req,
		"internal-model",
		normalizers.NewContext("openai", "0"),
		cfg.ToolRepair,
		cfg.StreamDeadline,
		[]byte(`{"messages":[{"role":"user","content":"test"}]}`),
	)

	// Should error because stream ended without "done" signal
	if err == nil {
		t.Fatal("expected error for stream without done signal")
	}
}

func TestHandleInternalStream_WithToolRepair(t *testing.T) {
	provider := newMockProvider()
	provider.setStreamEvents([]providers.StreamEvent{
		{Type: "content", Content: "Checking weather..."},
		{
			Type: "tool_call",
			ToolCalls: []providers.ToolCall{
				{
					Index: 0,
					ID:    "call_abc",
					Type:  "function",
					Function: providers.ToolCallFunction{
						Name:      "get_weather",
						Arguments: `{"location":"Boston"}`,
					},
				},
			},
		},
		{Type: "done", FinishReason: "tool_calls"},
	})

	cfg := newTestConfigSnapshot("internal-model")
	cfg.ToolRepair = toolrepair.Config{
		Enabled:    true,
		Strategies: []string{"extract_json"},
	}
	req := newTestUpstreamRequest("internal-model")

	err := handleInternalStream(
		context.Background(),
		provider,
		newChatCompletionRequest(),
		req,
		"internal-model",
		normalizers.NewContext("openai", "0"),
		cfg.ToolRepair,
		cfg.StreamDeadline,
		[]byte(`{"messages":[{"role":"user","content":"test"}]}`),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify buffer has content
	chunks, _ := req.buffer.GetChunksFrom(0)
	if len(chunks) < 2 {
		t.Errorf("expected at least 2 chunks, got %d", len(chunks))
	}
}

// =============================================================================
// Integration-style Tests
// =============================================================================

func TestHandleStreamingResponse_MultipleChunks(t *testing.T) {
	cfg := newTestConfigSnapshot("test-model")
	req := newTestUpstreamRequest("test-model")
	req.MarkStreaming()

	// Create a mock SSE server that sends many chunks
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// Send 20 chunks
		for i := 0; i < 20; i++ {
			chunk := fmt.Sprintf(`data: {"choices":[{"delta":{"content":"word%d"}}]}`, i)
			fmt.Fprintln(w, chunk)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}

		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("failed to get server response: %v", err)
	}
	defer resp.Body.Close()

	err = handleStreamingResponse(context.Background(), cfg, resp, req, "openai", []byte(`{"messages":[{"role":"user","content":"test"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify all chunks are in buffer
	chunks, _ := req.buffer.GetChunksFrom(0)
	// 20 content chunks + final chunk + [DONE]
	expectedMin := 20
	if len(chunks) < expectedMin {
		t.Errorf("expected at least %d chunks, got %d", expectedMin, len(chunks))
	}
}
