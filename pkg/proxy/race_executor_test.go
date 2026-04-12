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

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
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

// =============================================================================
// Tests for Secondary Upstream Model - Execution Level (E2E)
// =============================================================================

// mockProviderWithCapture is a mock provider that captures the model name
// from ChatCompletion calls for verification purposes.
type mockProviderWithCapture struct {
	name               string
	capturedModel      string
	chatCompletionResp *providers.ChatCompletionResponse
	chatCompletionErr  error
	streamEvents       []providers.StreamEvent
	streamErr          error
	mu                 sync.Mutex
}

func newMockProviderWithCapture() *mockProviderWithCapture {
	return &mockProviderWithCapture{name: "mock-with-capture"}
}

func (m *mockProviderWithCapture) Name() string {
	return m.name
}

func (m *mockProviderWithCapture) ChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	// Capture the model name from the request
	m.mu.Lock()
	m.capturedModel = req.Model
	m.mu.Unlock()

	if m.chatCompletionErr != nil {
		return nil, m.chatCompletionErr
	}
	return m.chatCompletionResp, nil
}

func (m *mockProviderWithCapture) StreamChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	// Capture the model name from the request
	m.mu.Lock()
	m.capturedModel = req.Model
	m.mu.Unlock()

	if m.streamErr != nil {
		return nil, m.streamErr
	}

	ch := make(chan providers.StreamEvent, len(m.streamEvents))
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

func (m *mockProviderWithCapture) IsRetryable(err error) bool {
	var providerErr *providers.ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.Retryable
	}
	return false
}

func (m *mockProviderWithCapture) setStreamEvents(events []providers.StreamEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streamEvents = events
}

func (m *mockProviderWithCapture) getCapturedModel() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.capturedModel
}

// TestExecuteInternalRequest_SecondaryModelSwap_E2E_NonStream tests that when
// useSecondaryUpstream=true, the secondary model is used instead of the primary
// model in the actual provider call (non-streaming).
func TestExecuteInternalRequest_SecondaryModelSwap_E2E_NonStream(t *testing.T) {
	// Create mock provider that captures model name
	provider := newMockProviderWithCapture()
	provider.chatCompletionResp = &providers.ChatCompletionResponse{
		ID:      "chatcmpl-123",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   "glm-4-flash",
		Choices: []providers.Choice{
			{
				Index: 0,
				Message: &providers.ChatMessage{
					Role:    "assistant",
					Content: "Hello from secondary",
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

	// Create models config with primary and secondary models
	modelsConfig := models.NewModelsConfig()
	modelsConfig.Models = []models.ModelConfig{
		{
			ID:                     "test-internal-model",
			Name:                   "Test Internal Model",
			Enabled:                true,
			Internal:               true,
			CredentialID:           "test-cred",
			InternalModel:          "glm-5.0",     // Primary model
			SecondaryUpstreamModel: "glm-4-flash", // Secondary model
		},
	}
	modelsConfig.Credentials = models.NewCredentialsConfig()
	_ = modelsConfig.Credentials.AddCredential(models.CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-key",
	})

	// Create config snapshot with models config
	cfg := &ConfigSnapshot{
		ModelID:            "test-internal-model",
		ModelsConfig:       modelsConfig,
		IdleTimeout:        60 * time.Second,
		StreamDeadline:     110 * time.Second,
		MaxGenerationTime:  300 * time.Second,
		RaceMaxBufferBytes: 1024 * 1024,
	}

	// Create upstream request with useSecondaryUpstream=true
	req := newUpstreamRequest(1, modelTypeSecond, "test-internal-model", 1024*1024)
	req.SetUseSecondaryUpstream(true)

	// Set up a mock provider factory that returns our captured provider
	// We'll use a channel to pass the provider to the executor
	providerCh := make(chan providers.Provider, 1)
	providerCh <- provider
	close(providerCh)

	// Save original and replace with mock
	originalNewProvider := newProviderClient
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		select {
		case p, ok := <-providerCh:
			if !ok {
				t.Fatal("provider channel closed unexpectedly")
			}
			return p, nil
		default:
			return provider, nil
		}
	}
	defer func() {
		newProviderClient = originalNewProvider
	}()

	rawBody := []byte(`{"messages":[{"role":"user","content":"test"}],"stream":false}`)

	// Call executeInternalRequest
	err := executeInternalRequest(context.Background(), cfg, rawBody, req)
	if err != nil {
		t.Fatalf("executeInternalRequest failed: %v", err)
	}

	// CRITICAL ASSERTION: Verify the provider received the secondary model, NOT the primary
	capturedModel := provider.getCapturedModel()
	if capturedModel != "glm-4-flash" {
		t.Errorf("Provider received model %q, want %q (secondary model should be used)",
			capturedModel, "glm-4-flash")
	}
	if capturedModel == "glm-5.0" {
		t.Error("Provider received primary model 'glm-5.0' - secondary model swap did NOT happen!")
	}
}

// TestExecuteInternalRequest_SecondaryModelSwap_E2E_Stream tests that when
// useSecondaryUpstream=true, the secondary model is used in streaming requests.
func TestExecuteInternalRequest_SecondaryModelSwap_E2E_Stream(t *testing.T) {
	// Create mock provider that captures model name
	provider := newMockProviderWithCapture()
	provider.setStreamEvents([]providers.StreamEvent{
		{Type: "content", Content: "Hello"},
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

	// Create models config with primary and secondary models
	modelsConfig := models.NewModelsConfig()
	modelsConfig.Models = []models.ModelConfig{
		{
			ID:                     "test-internal-model",
			Name:                   "Test Internal Model",
			Enabled:                true,
			Internal:               true,
			CredentialID:           "test-cred",
			InternalModel:          "glm-5.0",     // Primary model
			SecondaryUpstreamModel: "glm-4-flash", // Secondary model
		},
	}
	modelsConfig.Credentials = models.NewCredentialsConfig()
	_ = modelsConfig.Credentials.AddCredential(models.CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-key",
	})

	// Create config snapshot with models config
	cfg := &ConfigSnapshot{
		ModelID:            "test-internal-model",
		ModelsConfig:       modelsConfig,
		IdleTimeout:        60 * time.Second,
		StreamDeadline:     110 * time.Second,
		MaxGenerationTime:  300 * time.Second,
		RaceMaxBufferBytes: 1024 * 1024,
	}

	// Create upstream request with useSecondaryUpstream=true
	req := newUpstreamRequest(1, modelTypeSecond, "test-internal-model", 1024*1024)
	req.SetUseSecondaryUpstream(true)

	// Set up a mock provider factory
	providerCh := make(chan providers.Provider, 1)
	providerCh <- provider
	close(providerCh)

	originalNewProvider := newProviderClient
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		select {
		case p, ok := <-providerCh:
			if !ok {
				t.Fatal("provider channel closed unexpectedly")
			}
			return p, nil
		default:
			return provider, nil
		}
	}
	defer func() {
		newProviderClient = originalNewProvider
	}()

	rawBody := []byte(`{"messages":[{"role":"user","content":"test"}],"stream":true}`)

	// Call executeInternalRequest
	err := executeInternalRequest(context.Background(), cfg, rawBody, req)
	if err != nil {
		t.Fatalf("executeInternalRequest failed: %v", err)
	}

	// CRITICAL ASSERTION: Verify the provider received the secondary model
	capturedModel := provider.getCapturedModel()
	if capturedModel != "glm-4-flash" {
		t.Errorf("Provider received model %q, want %q (secondary model should be used)",
			capturedModel, "glm-4-flash")
	}
	if capturedModel == "glm-5.0" {
		t.Error("Provider received primary model 'glm-5.0' - secondary model swap did NOT happen!")
	}
}

// TestExecuteInternalRequest_NoSecondary_UsesPrimary_E2E tests that when
// useSecondaryUpstream=true but SecondaryUpstreamModel is empty, the primary
// model is used (no swap happens).
func TestExecuteInternalRequest_NoSecondary_UsesPrimary_E2E(t *testing.T) {
	// Create mock provider that captures model name
	provider := newMockProviderWithCapture()
	provider.chatCompletionResp = &providers.ChatCompletionResponse{
		ID:      "chatcmpl-123",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   "glm-5.0",
		Choices: []providers.Choice{
			{
				Index: 0,
				Message: &providers.ChatMessage{
					Role:    "assistant",
					Content: "Hello from primary",
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

	// Create models config with only primary model (no secondary)
	modelsConfig := models.NewModelsConfig()
	modelsConfig.Models = []models.ModelConfig{
		{
			ID:            "test-internal-no-secondary",
			Name:          "Test Internal No Secondary",
			Enabled:       true,
			Internal:      true,
			CredentialID:  "test-cred",
			InternalModel: "glm-5.0", // Primary only, no secondary
		},
	}
	modelsConfig.Credentials = models.NewCredentialsConfig()
	_ = modelsConfig.Credentials.AddCredential(models.CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-key",
	})

	// Create config snapshot with models config
	cfg := &ConfigSnapshot{
		ModelID:            "test-internal-no-secondary",
		ModelsConfig:       modelsConfig,
		IdleTimeout:        60 * time.Second,
		StreamDeadline:     110 * time.Second,
		MaxGenerationTime:  300 * time.Second,
		RaceMaxBufferBytes: 1024 * 1024,
	}

	// Create upstream request with useSecondaryUpstream=true (but no secondary configured)
	req := newUpstreamRequest(1, modelTypeSecond, "test-internal-no-secondary", 1024*1024)
	req.SetUseSecondaryUpstream(true)

	// Set up a mock provider factory
	providerCh := make(chan providers.Provider, 1)
	providerCh <- provider
	close(providerCh)

	originalNewProvider := newProviderClient
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		select {
		case p, ok := <-providerCh:
			if !ok {
				t.Fatal("provider channel closed unexpectedly")
			}
			return p, nil
		default:
			return provider, nil
		}
	}
	defer func() {
		newProviderClient = originalNewProvider
	}()

	rawBody := []byte(`{"messages":[{"role":"user","content":"test"}],"stream":false}`)

	// Call executeInternalRequest
	err := executeInternalRequest(context.Background(), cfg, rawBody, req)
	if err != nil {
		t.Fatalf("executeInternalRequest failed: %v", err)
	}

	// CRITICAL ASSERTION: Verify the provider received the primary model (no secondary to swap to)
	capturedModel := provider.getCapturedModel()
	if capturedModel != "glm-5.0" {
		t.Errorf("Provider received model %q, want %q (should fall back to primary)",
			capturedModel, "glm-5.0")
	}
}

// TestExecuteInternalRequest_SecondaryFalse_UsesPrimary_E2E tests that when
// useSecondaryUpstream=false, the primary model is used even if SecondaryUpstreamModel is configured.
func TestExecuteInternalRequest_SecondaryFalse_UsesPrimary_E2E(t *testing.T) {
	// Create mock provider that captures model name
	provider := newMockProviderWithCapture()
	provider.chatCompletionResp = &providers.ChatCompletionResponse{
		ID:      "chatcmpl-123",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   "glm-5.0",
		Choices: []providers.Choice{
			{
				Index: 0,
				Message: &providers.ChatMessage{
					Role:    "assistant",
					Content: "Hello from primary",
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

	// Create models config with primary and secondary models
	modelsConfig := models.NewModelsConfig()
	modelsConfig.Models = []models.ModelConfig{
		{
			ID:                     "test-internal-model",
			Name:                   "Test Internal Model",
			Enabled:                true,
			Internal:               true,
			CredentialID:           "test-cred",
			InternalModel:          "glm-5.0",     // Primary model
			SecondaryUpstreamModel: "glm-4-flash", // Secondary model
		},
	}
	modelsConfig.Credentials = models.NewCredentialsConfig()
	_ = modelsConfig.Credentials.AddCredential(models.CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-key",
	})

	// Create config snapshot with models config
	cfg := &ConfigSnapshot{
		ModelID:            "test-internal-model",
		ModelsConfig:       modelsConfig,
		IdleTimeout:        60 * time.Second,
		StreamDeadline:     110 * time.Second,
		MaxGenerationTime:  300 * time.Second,
		RaceMaxBufferBytes: 1024 * 1024,
	}

	// Create upstream request with useSecondaryUpstream=FALSE
	req := newUpstreamRequest(0, modelTypeMain, "test-internal-model", 1024*1024)
	// Don't call SetUseSecondaryUpstream(true) - default is false

	// Set up a mock provider factory
	providerCh := make(chan providers.Provider, 1)
	providerCh <- provider
	close(providerCh)

	originalNewProvider := newProviderClient
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		select {
		case p, ok := <-providerCh:
			if !ok {
				t.Fatal("provider channel closed unexpectedly")
			}
			return p, nil
		default:
			return provider, nil
		}
	}
	defer func() {
		newProviderClient = originalNewProvider
	}()

	rawBody := []byte(`{"messages":[{"role":"user","content":"test"}],"stream":false}`)

	// Call executeInternalRequest
	err := executeInternalRequest(context.Background(), cfg, rawBody, req)
	if err != nil {
		t.Fatalf("executeInternalRequest failed: %v", err)
	}

	// CRITICAL ASSERTION: Verify the provider received the primary model (not secondary)
	capturedModel := provider.getCapturedModel()
	if capturedModel != "glm-5.0" {
		t.Errorf("Provider received model %q, want %q (should use primary when useSecondary=false)",
			capturedModel, "glm-5.0")
	}
	if capturedModel == "glm-4-flash" {
		t.Error("Provider received secondary model 'glm-4-flash' - primary model should be used when useSecondary=false!")
	}
}

// TestResolveInternalConfig_SecondaryModelConfigured tests that when a model has
// SecondaryUpstreamModel configured, it can be retrieved from the config.
func TestResolveInternalConfig_SecondaryModelConfigured(t *testing.T) {
	// Create models config with primary and secondary models
	modelsConfig := models.NewModelsConfig()
	modelsConfig.Models = []models.ModelConfig{
		{
			ID:                     "test-internal-model",
			Name:                   "Test Internal Model",
			Enabled:                true,
			Internal:               true,
			CredentialID:           "test-cred",
			InternalModel:          "glm-5.0",     // Primary model
			SecondaryUpstreamModel: "glm-4-flash", // Secondary model
		},
	}
	modelsConfig.Credentials = models.NewCredentialsConfig()
	_ = modelsConfig.Credentials.AddCredential(models.CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-key",
	})

	// Get the model config and verify secondary model is set
	modelConfig := modelsConfig.GetModel("test-internal-model")
	if modelConfig == nil {
		t.Fatal("model config should not be nil")
	}
	if modelConfig.SecondaryUpstreamModel != "glm-4-flash" {
		t.Errorf("SecondaryUpstreamModel = %s, want glm-4-flash", modelConfig.SecondaryUpstreamModel)
	}
	if modelConfig.InternalModel != "glm-5.0" {
		t.Errorf("InternalModel = %s, want glm-5.0", modelConfig.InternalModel)
	}

	// ResolveInternalConfig should return primary model (secondary is only used by executor)
	provider, apiKey, baseURL, model, ok := modelsConfig.ResolveInternalConfig("test-internal-model")
	if !ok {
		t.Fatal("ResolveInternalConfig should return ok=true for internal model")
	}
	if model != "glm-5.0" {
		t.Errorf("ResolveInternalConfig should return primary model 'glm-5.0', got '%s'", model)
	}
	_ = provider
	_ = apiKey
	_ = baseURL
}

// TestResolveInternalConfig_SecondaryModelEmptyFallsBack tests that when
// SecondaryUpstreamModel is empty, ResolveInternalConfig returns the primary model.
func TestResolveInternalConfig_SecondaryModelEmptyFallsBack(t *testing.T) {
	// Create models config with primary model but NO secondary model
	modelsConfig := models.NewModelsConfig()
	modelsConfig.Models = []models.ModelConfig{
		{
			ID:            "test-internal-no-secondary",
			Name:          "Test Internal No Secondary",
			Enabled:       true,
			Internal:      true,
			CredentialID:  "test-cred",
			InternalModel: "glm-5.0", // Primary only, no secondary
		},
	}
	modelsConfig.Credentials = models.NewCredentialsConfig()
	_ = modelsConfig.Credentials.AddCredential(models.CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-key",
	})

	// Get the model config - should have empty secondary model
	modelConfig := modelsConfig.GetModel("test-internal-no-secondary")
	if modelConfig == nil {
		t.Fatal("model config should not be nil")
	}
	if modelConfig.SecondaryUpstreamModel != "" {
		t.Errorf("SecondaryUpstreamModel should be empty, got: %s", modelConfig.SecondaryUpstreamModel)
	}

	// ResolveInternalConfig should return the primary model
	provider, apiKey, baseURL, model, ok := modelsConfig.ResolveInternalConfig("test-internal-no-secondary")
	if !ok {
		t.Fatal("ResolveInternalConfig should return ok=true for internal model")
	}
	if model != "glm-5.0" {
		t.Errorf("Expected model 'glm-5.0' (primary), got '%s'", model)
	}
	_ = provider
	_ = apiKey
	_ = baseURL
}

// TestResolveInternalConfig_NonInternalModel_IgnoresSecondaryFlag tests that for
// non-internal models, secondary upstream is not applicable.
func TestResolveInternalConfig_NonInternalModel_IgnoresSecondaryFlag(t *testing.T) {
	// Create models config with a non-internal model
	modelsConfig := models.NewModelsConfig()
	modelsConfig.Models = []models.ModelConfig{
		{
			ID:       "test-external-model",
			Name:     "Test External Model",
			Enabled:  true,
			Internal: false, // External model - no secondary support
		},
	}

	// Get the model config
	modelConfig := modelsConfig.GetModel("test-external-model")
	if modelConfig == nil {
		t.Fatal("model config should not be nil")
	}
	if modelConfig.Internal {
		t.Error("model should be non-internal")
	}

	// ResolveInternalConfig should return ok=false for non-internal models
	_, _, _, _, ok := modelsConfig.ResolveInternalConfig("test-external-model")
	if ok {
		t.Error("ResolveInternalConfig should return ok=false for non-internal model")
	}
}

// TestResolveInternalConfig_UseSecondaryUpstreamFalse_UsesPrimary tests that when
// useSecondaryUpstream=false (default), ResolveInternalConfig returns the primary model.
func TestResolveInternalConfig_UseSecondaryUpstreamFalse_UsesPrimary(t *testing.T) {
	// Create models config with both primary and secondary models
	modelsConfig := models.NewModelsConfig()
	modelsConfig.Models = []models.ModelConfig{
		{
			ID:                     "test-both-models",
			Name:                   "Test Both Models",
			Enabled:                true,
			Internal:               true,
			CredentialID:           "test-cred",
			InternalModel:          "glm-5.0",     // Primary model
			SecondaryUpstreamModel: "glm-4-flash", // Secondary model
		},
	}
	modelsConfig.Credentials = models.NewCredentialsConfig()
	_ = modelsConfig.Credentials.AddCredential(models.CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-key",
	})

	// ResolveInternalConfig should return the primary model
	provider, apiKey, baseURL, model, ok := modelsConfig.ResolveInternalConfig("test-both-models")
	if !ok {
		t.Fatal("ResolveInternalConfig should return ok=true for internal model")
	}
	if model != "glm-5.0" {
		t.Errorf("Expected primary model 'glm-5.0', got '%s'", model)
	}
	_ = provider
	_ = apiKey
	_ = baseURL
}

// TestResolveInternalConfig_ModelNotFound tests behavior when model is not found
func TestResolveInternalConfig_ModelNotFound(t *testing.T) {
	// Create models config without the model we're looking for
	modelsConfig := models.NewModelsConfig()
	modelsConfig.Models = []models.ModelConfig{
		{
			ID:                     "some-other-model",
			Name:                   "Some Other Model",
			Enabled:                true,
			Internal:               true,
			CredentialID:           "test-cred",
			InternalModel:          "glm-5.0",
			SecondaryUpstreamModel: "glm-4-flash",
		},
	}
	modelsConfig.Credentials = models.NewCredentialsConfig()
	_ = modelsConfig.Credentials.AddCredential(models.CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-key",
	})

	// GetModel should return nil
	modelConfig := modelsConfig.GetModel("nonexistent-model")
	if modelConfig != nil {
		t.Error("GetModel should return nil for non-existent model")
	}

	// ResolveInternalConfig should return ok=false
	_, _, _, _, ok := modelsConfig.ResolveInternalConfig("nonexistent-model")
	if ok {
		t.Error("ResolveInternalConfig should return ok=false for non-existent model")
	}
}

// TestUpstreamRequest_SecondaryFlag_WithModelTypeSecond tests that the secondary
// flag is correctly associated with modelTypeSecond requests.
func TestUpstreamRequest_SecondaryFlag_WithModelTypeSecond(t *testing.T) {
	// Create a second request
	req := newUpstreamRequest(1, modelTypeSecond, "test-internal-model", 1024*1024)

	// By default, new requests should not have secondary upstream set
	if req.UseSecondaryUpstream() {
		t.Error("newUpstreamRequest() should default to false for useSecondaryUpstream")
	}

	// Manually set the flag (simulating what race coordinator does)
	req.SetUseSecondaryUpstream(true)
	if !req.UseSecondaryUpstream() {
		t.Error("SetUseSecondaryUpstream(true) should make UseSecondaryUpstream() return true")
	}

	// Verify the request info
	if req.GetModelType() != modelTypeSecond {
		t.Errorf("GetModelType() = %v, want %v", req.GetModelType(), modelTypeSecond)
	}
	if req.GetID() != 1 {
		t.Errorf("GetID() = %d, want 1", req.GetID())
	}

	// Reset the flag
	req.SetUseSecondaryUpstream(false)
	if req.UseSecondaryUpstream() {
		t.Error("SetUseSecondaryUpstream(false) should reset the flag")
	}
}

// TestConfigSnapshot_WithModelsConfig tests that ConfigSnapshot can hold a ModelsConfig
// with secondary upstream models.
func TestConfigSnapshot_WithModelsConfig(t *testing.T) {
	// Create models config
	modelsConfig := models.NewModelsConfig()
	modelsConfig.Models = []models.ModelConfig{
		{
			ID:                     "snapshot-test-model",
			Name:                   "Snapshot Test Model",
			Enabled:                true,
			Internal:               true,
			CredentialID:           "test-cred",
			InternalModel:          "glm-5.0",
			SecondaryUpstreamModel: "glm-4-flash",
		},
	}
	modelsConfig.Credentials = models.NewCredentialsConfig()
	_ = modelsConfig.Credentials.AddCredential(models.CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-key",
	})

	// Create config snapshot with models config
	cfg := &ConfigSnapshot{
		ModelID:            "snapshot-test-model",
		ModelsConfig:       modelsConfig,
		IdleTimeout:        60 * time.Second,
		StreamDeadline:     110 * time.Second,
		MaxGenerationTime:  300 * time.Second,
		RaceMaxBufferBytes: 1024 * 1024,
	}

	// Verify models config is accessible
	if cfg.ModelsConfig == nil {
		t.Fatal("ModelsConfig should not be nil")
	}

	// Get the model from config
	modelConfig := cfg.ModelsConfig.GetModel("snapshot-test-model")
	if modelConfig == nil {
		t.Fatal("GetModel should return model config")
	}

	if modelConfig.SecondaryUpstreamModel != "glm-4-flash" {
		t.Errorf("SecondaryUpstreamModel = %s, want glm-4-flash", modelConfig.SecondaryUpstreamModel)
	}
}

// =============================================================================
// C2: Peak Hour + Secondary Model Combo Runtime Tests
// =============================================================================

// TestExecuteInternalRequest_PeakHourAndSecondary_Combo_NonStream tests the interaction
// between peak hours and secondary model during runtime execution (non-streaming).
// When peak hours are active AND secondary model is configured:
// - Main request (useSecondary=false) should use peak hour model
// - Second request (useSecondary=true) should use secondary model (NOT peak model)
func TestExecuteInternalRequest_PeakHourAndSecondary_Combo_NonStream(t *testing.T) {
	// Get current time to configure peak hours around now in local timezone
	now := time.Now()
	_, localOffset := now.Zone()
	localOffsetHours := localOffset / 3600

	// Format as +H or -H for the peak hour timezone
	var tzOffset string
	if localOffsetHours >= 0 {
		tzOffset = fmt.Sprintf("+%d", localOffsetHours)
	} else {
		tzOffset = fmt.Sprintf("%d", localOffsetHours)
	}

	currentHour := now.Hour()
	// Configure peak hours to include the current hour (start 1 hour before, end 1 hour after)
	peakStart := (currentHour - 1 + 24) % 24
	peakEnd := (currentHour + 1) % 24

	peakStartStr := fmt.Sprintf("%02d:00", peakStart)
	peakEndStr := fmt.Sprintf("%02d:00", peakEnd)

	// Create mock provider that captures model name for main request
	mainProvider := newMockProviderWithCapture()
	mainProvider.chatCompletionResp = &providers.ChatCompletionResponse{
		ID:      "chatcmpl-main",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   "glm-peak-model",
		Choices: []providers.Choice{
			{
				Index: 0,
				Message: &providers.ChatMessage{
					Role:    "assistant",
					Content: "Hello from peak model",
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

	// Create mock provider that captures model name for second request
	secondaryProvider := newMockProviderWithCapture()
	secondaryProvider.chatCompletionResp = &providers.ChatCompletionResponse{
		ID:      "chatcmpl-secondary",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   "glm-4-flash",
		Choices: []providers.Choice{
			{
				Index: 0,
				Message: &providers.ChatMessage{
					Role:    "assistant",
					Content: "Hello from secondary model",
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

	// Create models config with peak hour and secondary model
	modelsConfig := models.NewModelsConfig()
	modelsConfig.Models = []models.ModelConfig{
		{
			ID:                     "peak-secondary-model",
			Name:                   "Peak and Secondary Model",
			Enabled:                true,
			Internal:               true,
			CredentialID:           "test-cred",
			InternalModel:          "glm-5.0",        // Base model
			PeakHourEnabled:        true,             // Peak hours ACTIVE
			PeakHourStart:          peakStartStr,     // Start time (local)
			PeakHourEnd:            peakEndStr,       // End time (local)
			PeakHourTimezone:       tzOffset,         // Local timezone offset
			PeakHourModel:          "glm-peak-model", // Peak hour model
			SecondaryUpstreamModel: "glm-4-flash",    // Secondary model
		},
	}
	modelsConfig.Credentials = models.NewCredentialsConfig()
	_ = modelsConfig.Credentials.AddCredential(models.CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-key",
	})

	// Create config snapshot with models config
	cfg := &ConfigSnapshot{
		ModelID:            "peak-secondary-model",
		ModelsConfig:       modelsConfig,
		IdleTimeout:        60 * time.Second,
		StreamDeadline:     110 * time.Second,
		MaxGenerationTime:  300 * time.Second,
		RaceMaxBufferBytes: 1024 * 1024,
	}

	// =========================================================================
	// Test 1: Main request (useSecondary=false) should get peak hour model
	// =========================================================================

	// Create main upstream request with useSecondaryUpstream=false (default)
	mainReq := newUpstreamRequest(0, modelTypeMain, "peak-secondary-model", 1024*1024)
	// Don't set secondary flag - it should be false by default

	// Set up mock provider factory
	providerCh := make(chan providers.Provider, 1)
	providerCh <- mainProvider
	close(providerCh)

	originalNewProvider := newProviderClient
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		select {
		case p, ok := <-providerCh:
			if !ok {
				t.Fatal("provider channel closed unexpectedly")
			}
			return p, nil
		default:
			return mainProvider, nil
		}
	}

	rawBody := []byte(`{"messages":[{"role":"user","content":"test"}],"stream":false}`)

	// Call executeInternalRequest for main request
	err := executeInternalRequest(context.Background(), cfg, rawBody, mainReq)
	if err != nil {
		t.Fatalf("executeInternalRequest failed for main request: %v", err)
	}

	// CRITICAL ASSERTION: Main request should receive peak hour model, NOT base model
	mainCapturedModel := mainProvider.getCapturedModel()
	if mainCapturedModel != "glm-peak-model" {
		t.Errorf("Main request received model %q, want %q (peak hour model should be used)",
			mainCapturedModel, "glm-peak-model")
	}
	if mainCapturedModel == "glm-5.0" {
		t.Error("Main request received base model 'glm-5.0' - peak hour model should be used!")
	}

	// Restore original provider factory for second request test
	newProviderClient = originalNewProvider

	// =========================================================================
	// Test 2: Second request (useSecondary=true) should get secondary model
	// =========================================================================

	// Reset provider channel for second request
	providerCh2 := make(chan providers.Provider, 1)
	providerCh2 <- secondaryProvider
	close(providerCh2)

	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		select {
		case p, ok := <-providerCh2:
			if !ok {
				t.Fatal("provider channel closed unexpectedly")
			}
			return p, nil
		default:
			return secondaryProvider, nil
		}
	}
	defer func() {
		newProviderClient = originalNewProvider
	}()

	// Create second upstream request with useSecondaryUpstream=true
	secondReq := newUpstreamRequest(1, modelTypeSecond, "peak-secondary-model", 1024*1024)
	secondReq.SetUseSecondaryUpstream(true)

	// Call executeInternalRequest for second request
	err = executeInternalRequest(context.Background(), cfg, rawBody, secondReq)
	if err != nil {
		t.Fatalf("executeInternalRequest failed for second request: %v", err)
	}

	// CRITICAL ASSERTION: Second request should receive secondary model, NOT peak model
	secondaryCapturedModel := secondaryProvider.getCapturedModel()
	if secondaryCapturedModel != "glm-4-flash" {
		t.Errorf("Second request received model %q, want %q (secondary model should be used, NOT peak)",
			secondaryCapturedModel, "glm-4-flash")
	}
	if secondaryCapturedModel == "glm-peak-model" {
		t.Error("Second request received peak hour model 'glm-peak-model' - secondary model should be used!")
	}
	if secondaryCapturedModel == "glm-5.0" {
		t.Error("Second request received base model 'glm-5.0' - secondary model should be used!")
	}
}

// TestExecuteInternalRequest_PeakHourAndSecondary_Combo_Stream tests the interaction
// between peak hours and secondary model during streaming execution.
// Verifies that streaming requests follow the same model selection logic.
func TestExecuteInternalRequest_PeakHourAndSecondary_Combo_Stream(t *testing.T) {
	// Get current time to configure peak hours around now in local timezone
	now := time.Now()
	_, localOffset := now.Zone()
	localOffsetHours := localOffset / 3600

	// Format as +H or -H for the peak hour timezone
	var tzOffset string
	if localOffsetHours >= 0 {
		tzOffset = fmt.Sprintf("+%d", localOffsetHours)
	} else {
		tzOffset = fmt.Sprintf("%d", localOffsetHours)
	}

	currentHour := now.Hour()
	peakStart := (currentHour - 1 + 24) % 24
	peakEnd := (currentHour + 1) % 24

	peakStartStr := fmt.Sprintf("%02d:00", peakStart)
	peakEndStr := fmt.Sprintf("%02d:00", peakEnd)

	// Create mock providers for main and second requests
	mainProvider := newMockProviderWithCapture()
	mainProvider.setStreamEvents([]providers.StreamEvent{
		{Type: "content", Content: "Hello from peak"},
		{
			Type:         "done",
			FinishReason: "stop",
			Response: &providers.ChatCompletionResponse{
				Usage: providers.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			},
		},
	})

	secondaryProvider := newMockProviderWithCapture()
	secondaryProvider.setStreamEvents([]providers.StreamEvent{
		{Type: "content", Content: "Hello from secondary"},
		{
			Type:         "done",
			FinishReason: "stop",
			Response: &providers.ChatCompletionResponse{
				Usage: providers.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			},
		},
	})

	// Create models config with peak hour and secondary model
	modelsConfig := models.NewModelsConfig()
	modelsConfig.Models = []models.ModelConfig{
		{
			ID:                     "peak-secondary-stream",
			Name:                   "Peak and Secondary Stream",
			Enabled:                true,
			Internal:               true,
			CredentialID:           "test-cred",
			InternalModel:          "glm-5.0",
			PeakHourEnabled:        true,
			PeakHourStart:          peakStartStr,
			PeakHourEnd:            peakEndStr,
			PeakHourTimezone:       tzOffset,
			PeakHourModel:          "glm-peak-model",
			SecondaryUpstreamModel: "glm-4-flash",
		},
	}
	modelsConfig.Credentials = models.NewCredentialsConfig()
	_ = modelsConfig.Credentials.AddCredential(models.CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-key",
	})

	cfg := &ConfigSnapshot{
		ModelID:            "peak-secondary-stream",
		ModelsConfig:       modelsConfig,
		IdleTimeout:        60 * time.Second,
		StreamDeadline:     110 * time.Second,
		MaxGenerationTime:  300 * time.Second,
		RaceMaxBufferBytes: 1024 * 1024,
	}

	// Test main request (streaming, useSecondary=false)
	mainReq := newUpstreamRequest(0, modelTypeMain, "peak-secondary-stream", 1024*1024)

	providerCh := make(chan providers.Provider, 1)
	providerCh <- mainProvider
	close(providerCh)

	originalNewProvider := newProviderClient
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		select {
		case p, ok := <-providerCh:
			if !ok {
				t.Fatal("provider channel closed unexpectedly")
			}
			return p, nil
		default:
			return mainProvider, nil
		}
	}

	rawBody := []byte(`{"messages":[{"role":"user","content":"test"}],"stream":true}`)

	err := executeInternalRequest(context.Background(), cfg, rawBody, mainReq)
	if err != nil {
		t.Fatalf("executeInternalRequest failed for streaming main request: %v", err)
	}

	// Verify main request got peak hour model
	mainCapturedModel := mainProvider.getCapturedModel()
	if mainCapturedModel != "glm-peak-model" {
		t.Errorf("Streaming main request received model %q, want %q",
			mainCapturedModel, "glm-peak-model")
	}

	// Restore and test second request
	newProviderClient = originalNewProvider

	providerCh2 := make(chan providers.Provider, 1)
	providerCh2 <- secondaryProvider
	close(providerCh2)

	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		select {
		case p, ok := <-providerCh2:
			if !ok {
				t.Fatal("provider channel closed unexpectedly")
			}
			return p, nil
		default:
			return secondaryProvider, nil
		}
	}
	defer func() {
		newProviderClient = originalNewProvider
	}()

	secondReq := newUpstreamRequest(1, modelTypeSecond, "peak-secondary-stream", 1024*1024)
	secondReq.SetUseSecondaryUpstream(true)

	err = executeInternalRequest(context.Background(), cfg, rawBody, secondReq)
	if err != nil {
		t.Fatalf("executeInternalRequest failed for streaming second request: %v", err)
	}

	// Verify second request got secondary model
	secondaryCapturedModel := secondaryProvider.getCapturedModel()
	if secondaryCapturedModel != "glm-4-flash" {
		t.Errorf("Streaming second request received model %q, want %q",
			secondaryCapturedModel, "glm-4-flash")
	}
	if secondaryCapturedModel == "glm-peak-model" {
		t.Error("Streaming second request received peak model - should use secondary!")
	}
}
