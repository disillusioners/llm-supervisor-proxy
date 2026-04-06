package proxy

import (
	"testing"

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
