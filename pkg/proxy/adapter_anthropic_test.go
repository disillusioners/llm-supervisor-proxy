package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/translator"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

func TestAnthropicAdapter_Protocol(t *testing.T) {
	adapter := NewAnthropicAdapter()
	if adapter.Protocol() != "anthropic" {
		t.Errorf("expected protocol 'anthropic', got '%s'", adapter.Protocol())
	}
}

func TestAnthropicAdapter_IsStream(t *testing.T) {
	adapter := NewAnthropicAdapter()

	tests := []struct {
		name     string
		body     map[string]interface{}
		expected bool
	}{
		{
			name:     "stream true",
			body:     map[string]interface{}{"stream": true},
			expected: true,
		},
		{
			name:     "stream false",
			body:     map[string]interface{}{"stream": false},
			expected: false,
		},
		{
			name:     "no stream field",
			body:     map[string]interface{}{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := adapter.IsStream(tt.body); got != tt.expected {
				t.Errorf("IsStream() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestAnthropicAdapter_ParseRequest(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantErr   bool
		wantModel string
		wantMeta  bool
	}{
		{
			name:      "valid request",
			body:      `{"model":"claude-3-sonnet","max_tokens":1024,"messages":[{"role":"user","content":"Hi"}]}`,
			wantModel: "claude-3-sonnet",
			wantMeta:  true,
		},
		{
			name:      "with stream",
			body:      `{"model":"claude-3-sonnet","max_tokens":1024,"messages":[{"role":"user","content":"Hi"}],"stream":true}`,
			wantModel: "claude-3-sonnet",
			wantMeta:  true,
		},
		{
			name:      "with temperature",
			body:      `{"model":"claude-3-sonnet","max_tokens":1024,"messages":[{"role":"user","content":"Hi"}],"temperature":0.7}`,
			wantModel: "claude-3-sonnet",
			wantMeta:  true,
		},
		{
			name:    "invalid json",
			body:    `not json`,
			wantErr: true,
		},
		{
			name:    "missing model",
			body:    `{"max_tokens":1024,"messages":[{"role":"user","content":"Hi"}]}`,
			wantErr: true,
		},
		{
			name:    "missing max_tokens",
			body:    `{"model":"claude-3-sonnet","messages":[{"role":"user","content":"Hi"}]}`,
			wantErr: true,
		},
		{
			name:    "missing messages",
			body:    `{"model":"claude-3-sonnet","max_tokens":1024}`,
			wantErr: true,
		},
		{
			name:    "invalid role",
			body:    `{"model":"claude-3-sonnet","max_tokens":1024,"messages":[{"role":"system","content":"Hi"}]}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := NewAnthropicAdapter()
			req := httptest.NewRequest("POST", "/", strings.NewReader(tt.body))

			_, meta, err := adapter.ParseRequest(req)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseRequest() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && tt.wantMeta {
				if meta.ClientModel != tt.wantModel {
					t.Errorf("expected model '%s', got '%s'", tt.wantModel, meta.ClientModel)
				}
			}
		})
	}
}

func TestAnthropicAdapter_ExtractUpstreamModel(t *testing.T) {
	adapter := NewAnthropicAdapter()

	body := map[string]interface{}{"model": "claude-3-sonnet"}
	model := adapter.ExtractUpstreamModel(body)
	if model != "claude-3-sonnet" {
		t.Errorf("expected 'claude-3-sonnet', got '%s'", model)
	}
}

func TestValidateAnthropicAdapterRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     map[string]interface{}
		wantErr bool
	}{
		{
			name: "valid request",
			req: map[string]interface{}{
				"model":      "claude-3-sonnet",
				"max_tokens": 1024,
				"messages": []interface{}{
					map[string]interface{}{"role": "user", "content": "Hi"},
				},
			},
			wantErr: false,
		},
		{
			name: "missing model",
			req: map[string]interface{}{
				"max_tokens": 1024,
				"messages":   []interface{}{},
			},
			wantErr: true,
		},
		{
			name: "missing max_tokens",
			req: map[string]interface{}{
				"model":    "claude-3-sonnet",
				"messages": []interface{}{},
			},
			wantErr: true,
		},
		{
			name: "missing messages",
			req: map[string]interface{}{
				"model":      "claude-3-sonnet",
				"max_tokens": 1024,
			},
			wantErr: true,
		},
		{
			name: "invalid role",
			req: map[string]interface{}{
				"model":      "claude-3-sonnet",
				"max_tokens": 1024,
				"messages": []interface{}{
					map[string]interface{}{"role": "system", "content": "Hi"},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			anthropicReq := parseAnthropicReq(tt.req)
			err := validateAnthropicAdapterRequest(anthropicReq)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateAnthropicAdapterRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConvertAnthropicMessagesToStoreAdapter(t *testing.T) {
	tests := []struct {
		name              string
		messages          []translator.AnthropicMessage
		expected          int
		expectedContent   string
		expectedThinking  string
		expectedToolCalls []store.ToolCall
	}{
		{
			name: "simple string content",
			messages: []translator.AnthropicMessage{
				{Role: "user", Content: "Hello"},
			},
			expected:        1,
			expectedContent: "Hello",
		},
		{
			name: "array content with text",
			messages: []translator.AnthropicMessage{
				{Role: "user", Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "Hello"},
				}},
			},
			expected:        1,
			expectedContent: "Hello",
		},
		{
			name: "array content with multiple texts",
			messages: []translator.AnthropicMessage{
				{Role: "user", Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "Hello"},
					map[string]interface{}{"type": "text", "text": " World"},
				}},
			},
			expected:        1,
			expectedContent: "Hello World",
		},
		{
			name: "mixed content blocks",
			messages: []translator.AnthropicMessage{
				{Role: "user", Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "Hello"},
					map[string]interface{}{"type": "image", "source": map[string]interface{}{"type": "base64"}},
				}},
			},
			expected:        1,
			expectedContent: "Hello",
		},
		{
			name: "tool_use round trip",
			messages: []translator.AnthropicMessage{
				{
					Role: "assistant",
					Content: []interface{}{
						map[string]interface{}{
							"type":  "tool_use",
							"id":    "toolu_01A09q90tc1q visual",
							"name":  "get_weather",
							"input": map[string]interface{}{"location": "San Francisco, CA"},
						},
					},
				},
			},
			expected:        1,
			expectedContent: "",
			expectedToolCalls: []store.ToolCall{
				{
					ID:   "toolu_01A09q90tc1q visual",
					Type: "function",
					Function: store.Function{
						Name:      "get_weather",
						Arguments: `{"location":"San Francisco, CA"}`,
					},
				},
			},
		},
		{
			name: "tool_result with string content",
			messages: []translator.AnthropicMessage{
				{
					Role: "user",
					Content: []interface{}{
						map[string]interface{}{
							"type":        "tool_result",
							"tool_use_id": "toolu_01A09q90tc1q",
							"content":     "65 degrees and sunny",
						},
					},
				},
			},
			expected:        1,
			expectedContent: "[tool_result toolu_01A09q90tc1q] 65 degrees and sunny",
		},
		{
			name: "tool_result with array of text content",
			messages: []translator.AnthropicMessage{
				{
					Role: "user",
					Content: []interface{}{
						map[string]interface{}{
							"type":        "tool_result",
							"tool_use_id": "toolu_01A09q90tc1q",
							"content": []interface{}{
								map[string]interface{}{"type": "text", "text": "Line 1"},
								map[string]interface{}{"type": "text", "text": "Line 2"},
							},
						},
					},
				},
			},
			expected:        1,
			expectedContent: "[tool_result toolu_01A09q90tc1q] Line 1\nLine 2",
		},
		{
			name: "thinking block",
			messages: []translator.AnthropicMessage{
				{
					Role: "assistant",
					Content: []interface{}{
						map[string]interface{}{
							"type":     "thinking",
							"thinking": "Thinking deeply about the weather...",
						},
						map[string]interface{}{
							"type": "text",
							"text": "Here is the forecast",
						},
					},
				},
			},
			expected:         1,
			expectedContent:  "Here is the forecast",
			expectedThinking: "Thinking deeply about the weather...",
		},
		{
			name: "mixed text and tool_use in single message",
			messages: []translator.AnthropicMessage{
				{
					Role: "assistant",
					Content: []interface{}{
						map[string]interface{}{
							"type": "text",
							"text": "Let me look up the weather for you.",
						},
						map[string]interface{}{
							"type":  "tool_use",
							"id":    "toolu_weather1",
							"name":  "get_weather",
							"input": map[string]interface{}{"city": "Paris"},
						},
					},
				},
			},
			expected:        1,
			expectedContent: "Let me look up the weather for you.",
			expectedToolCalls: []store.ToolCall{
				{
					ID:   "toolu_weather1",
					Type: "function",
					Function: store.Function{
						Name:      "get_weather",
						Arguments: `{"city":"Paris"}`,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertAnthropicMessagesToStoreAdapter(tt.messages)
			if len(result) != tt.expected {
				t.Fatalf("expected %d messages, got %d", tt.expected, len(result))
			}
			if tt.expected > 0 {
				msg := result[0]
				if msg.Content != tt.expectedContent {
					t.Errorf("expected Content %q, got %q", tt.expectedContent, msg.Content)
				}
				if msg.Thinking != tt.expectedThinking {
					t.Errorf("expected Thinking %q, got %q", tt.expectedThinking, msg.Thinking)
				}
				if len(msg.ToolCalls) != len(tt.expectedToolCalls) {
					t.Fatalf("expected %d tool calls, got %d", len(tt.expectedToolCalls), len(msg.ToolCalls))
				}
				for i, wantTC := range tt.expectedToolCalls {
					gotTC := msg.ToolCalls[i]
					if gotTC.ID != wantTC.ID {
						t.Errorf("tool_call[%d].ID = %q, want %q", i, gotTC.ID, wantTC.ID)
					}
					if gotTC.Type != wantTC.Type {
						t.Errorf("tool_call[%d].Type = %q, want %q", i, gotTC.Type, wantTC.Type)
					}
					if gotTC.Function.Name != wantTC.Function.Name {
						t.Errorf("tool_call[%d].Function.Name = %q, want %q", i, gotTC.Function.Name, wantTC.Function.Name)
					}
					if gotTC.Function.Arguments != wantTC.Function.Arguments {
						t.Errorf("tool_call[%d].Function.Arguments = %q, want %q", i, gotTC.Function.Arguments, wantTC.Function.Arguments)
					}
				}
			}
		})
	}
}

func TestConvertAnthropicMessagesToStore_FullFidelity(t *testing.T) {
	t.Run("tool_use round trip", func(t *testing.T) {
		msgs := []translator.AnthropicMessage{
			{
				Role: "assistant",
				Content: []interface{}{
					map[string]interface{}{
						"type":  "tool_use",
						"id":    "toolu_abc123",
						"name":  "calculator",
						"input": map[string]interface{}{"expression": "2+2"},
					},
				},
			},
		}

		// Verify both entry points produce identical store messages
		res1 := convertAnthropicMessagesToStore(msgs)
		res2 := convertAnthropicMessagesToStoreAdapter(msgs)

		for _, res := range [][]store.Message{res1, res2} {
			if len(res) != 1 {
				t.Fatalf("expected 1 message, got %d", len(res))
			}
			m := res[0]
			if m.Role != "assistant" {
				t.Errorf("expected role 'assistant', got %q", m.Role)
			}
			if len(m.ToolCalls) != 1 {
				t.Fatalf("expected 1 tool call, got %d", len(m.ToolCalls))
			}
			tc := m.ToolCalls[0]
			if tc.ID != "toolu_abc123" {
				t.Errorf("expected ID 'toolu_abc123', got %q", tc.ID)
			}
			if tc.Type != "function" {
				t.Errorf("expected Type 'function', got %q", tc.Type)
			}
			if tc.Function.Name != "calculator" {
				t.Errorf("expected function name 'calculator', got %q", tc.Function.Name)
			}
			if tc.Function.Arguments != `{"expression":"2+2"}` {
				t.Errorf("expected arguments '{\"expression\":\"2+2\"}', got %q", tc.Function.Arguments)
			}
		}
	})

	t.Run("tool_result with string content", func(t *testing.T) {
		msgs := []translator.AnthropicMessage{
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{
						"type":        "tool_result",
						"tool_use_id": "toolu_abc123",
						"content":     "result is 4",
					},
				},
			},
		}
		res := convertAnthropicMessagesToStore(msgs)
		if len(res) != 1 {
			t.Fatalf("expected 1 message, got %d", len(res))
		}
		expectedContent := "[tool_result toolu_abc123] result is 4"
		if res[0].Content != expectedContent {
			t.Errorf("expected content %q, got %q", expectedContent, res[0].Content)
		}
	})

	t.Run("tool_result with array of text content", func(t *testing.T) {
		msgs := []translator.AnthropicMessage{
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{
						"type":        "tool_result",
						"tool_use_id": "toolu_abc123",
						"content": []interface{}{
							map[string]interface{}{"type": "text", "text": "line 1"},
							map[string]interface{}{"type": "text", "text": "line 2"},
						},
					},
				},
			},
		}
		res := convertAnthropicMessagesToStore(msgs)
		if len(res) != 1 {
			t.Fatalf("expected 1 message, got %d", len(res))
		}
		expectedContent := "[tool_result toolu_abc123] line 1\nline 2"
		if res[0].Content != expectedContent {
			t.Errorf("expected content %q, got %q", expectedContent, res[0].Content)
		}
	})

	t.Run("thinking block", func(t *testing.T) {
		msgs := []translator.AnthropicMessage{
			{
				Role: "assistant",
				Content: []interface{}{
					map[string]interface{}{
						"type":     "thinking",
						"thinking": "Step 1: calculate 2+2.",
					},
					map[string]interface{}{
						"type":     "thinking",
						"thinking": " Step 2: verify 4.",
					},
					map[string]interface{}{
						"type": "text",
						"text": "The answer is 4.",
					},
				},
			},
		}
		res := convertAnthropicMessagesToStore(msgs)
		if len(res) != 1 {
			t.Fatalf("expected 1 message, got %d", len(res))
		}
		if res[0].Content != "The answer is 4." {
			t.Errorf("expected content 'The answer is 4.', got %q", res[0].Content)
		}
		expectedThinking := "Step 1: calculate 2+2. Step 2: verify 4."
		if res[0].Thinking != expectedThinking {
			t.Errorf("expected thinking %q, got %q", expectedThinking, res[0].Thinking)
		}
	})

	t.Run("mixed text and tool_use in single message", func(t *testing.T) {
		msgs := []translator.AnthropicMessage{
			{
				Role: "assistant",
				Content: []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": "Checking weather...",
					},
					map[string]interface{}{
						"type":  "tool_use",
						"id":    "toolu_weather_1",
						"name":  "get_weather",
						"input": map[string]interface{}{"location": "Boston"},
					},
				},
			},
		}
		res := convertAnthropicMessagesToStore(msgs)
		if len(res) != 1 {
			t.Fatalf("expected 1 message, got %d", len(res))
		}
		if res[0].Content != "Checking weather..." {
			t.Errorf("expected content 'Checking weather...', got %q", res[0].Content)
		}
		if len(res[0].ToolCalls) != 1 {
			t.Fatalf("expected 1 tool call, got %d", len(res[0].ToolCalls))
		}
		if res[0].ToolCalls[0].ID != "toolu_weather_1" {
			t.Errorf("expected tool call id 'toolu_weather_1', got %q", res[0].ToolCalls[0].ID)
		}
		if res[0].ToolCalls[0].Type != "function" {
			t.Errorf("expected tool call type 'function', got %q", res[0].ToolCalls[0].Type)
		}
		if res[0].ToolCalls[0].Function.Name != "get_weather" {
			t.Errorf("expected function name 'get_weather', got %q", res[0].ToolCalls[0].Function.Name)
		}
		if res[0].ToolCalls[0].Function.Arguments != `{"location":"Boston"}` {
			t.Errorf("expected arguments '{\"location\":\"Boston\"}', got %q", res[0].ToolCalls[0].Function.Arguments)
		}
	})

	t.Run("typed []translator.ContentBlock support", func(t *testing.T) {
		msgs := []translator.AnthropicMessage{
			{
				Role: "assistant",
				Content: []translator.ContentBlock{
					{
						Type:     "thinking",
						Thinking: "Pondering...",
					},
					{
						Type: "text",
						Text: "Answer is here.",
					},
					{
						Type:  "tool_use",
						ID:    "toolu_typed_1",
						Name:  "fetch_data",
						Input: []byte(`{"id":123}`),
					},
				},
			},
			{
				Role: "user",
				Content: []translator.ContentBlock{
					{
						Type:      "tool_result",
						ToolUseID: "toolu_typed_1",
						Content:   "data payload",
					},
				},
			},
		}
		res := convertAnthropicMessagesToStore(msgs)
		if len(res) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(res))
		}
		// Message 0
		if res[0].Thinking != "Pondering..." {
			t.Errorf("expected thinking 'Pondering...', got %q", res[0].Thinking)
		}
		if res[0].Content != "Answer is here." {
			t.Errorf("expected content 'Answer is here.', got %q", res[0].Content)
		}
		if len(res[0].ToolCalls) != 1 {
			t.Fatalf("expected 1 tool call, got %d", len(res[0].ToolCalls))
		}
		if res[0].ToolCalls[0].Function.Arguments != `{"id":123}` {
			t.Errorf("expected arguments '{\"id\":123}', got %q", res[0].ToolCalls[0].Function.Arguments)
		}

		// Message 1
		if res[1].Content != "[tool_result toolu_typed_1] data payload" {
			t.Errorf("expected tool_result content, got %q", res[1].Content)
		}
	})

	t.Run("W2 multi-text-block join without newline", func(t *testing.T) {
		msgs := []translator.AnthropicMessage{
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "Part 1"},
					map[string]interface{}{"type": "text", "text": " - Part 2"},
					map[string]interface{}{"type": "text", "text": " - Part 3"},
				},
			},
			{
				Role: "assistant",
				Content: []translator.ContentBlock{
					{Type: "text", Text: "Hello "},
					{Type: "text", Text: "World!"},
				},
			},
		}
		res := convertAnthropicMessagesToStore(msgs)
		if len(res) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(res))
		}
		if res[0].Content != "Part 1 - Part 2 - Part 3" {
			t.Errorf("expected %q, got %q", "Part 1 - Part 2 - Part 3", res[0].Content)
		}
		if res[1].Content != "Hello World!" {
			t.Errorf("expected %q, got %q", "Hello World!", res[1].Content)
		}
	})

	t.Run("W3 tool_use arguments object validation and fallback", func(t *testing.T) {
		msgs := []translator.AnthropicMessage{
			{
				Role: "assistant",
				Content: []interface{}{
					// Valid object in map[string]interface{}
					map[string]interface{}{
						"type":  "tool_use",
						"id":    "tu_valid_map",
						"name":  "fn1",
						"input": map[string]interface{}{"a": 1},
					},
					// Non-object in string input (e.g. number or array string)
					map[string]interface{}{
						"type":  "tool_use",
						"id":    "tu_array_map",
						"name":  "fn2",
						"input": "[1, 2, 3]",
					},
					// Invalid JSON string in input
					map[string]interface{}{
						"type":  "tool_use",
						"id":    "tu_invalid_map",
						"name":  "fn3",
						"input": "{invalid json}",
					},
					// Primitive input (int)
					map[string]interface{}{
						"type":  "tool_use",
						"id":    "tu_prim_map",
						"name":  "fn4",
						"input": 12345,
					},
				},
			},
			{
				Role: "assistant",
				Content: []translator.ContentBlock{
					// Valid object bytes
					{
						Type:  "tool_use",
						ID:    "tu_valid_typed",
						Name:  "fn5",
						Input: []byte(`{"k":"v"}`),
					},
					// Non-object bytes (array)
					{
						Type:  "tool_use",
						ID:    "tu_array_typed",
						Name:  "fn6",
						Input: []byte(`["item"]`),
					},
					// Invalid JSON bytes
					{
						Type:  "tool_use",
						ID:    "tu_invalid_typed",
						Name:  "fn7",
						Input: []byte(`not json`),
					},
					// Empty bytes
					{
						Type:  "tool_use",
						ID:    "tu_empty_typed",
						Name:  "fn8",
						Input: []byte(``),
					},
				},
			},
		}

		res := convertAnthropicMessagesToStore(msgs)
		if len(res) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(res))
		}

		// Check message 0 (raw map inputs)
		if len(res[0].ToolCalls) != 4 {
			t.Fatalf("expected 4 tool calls in msg 0, got %d", len(res[0].ToolCalls))
		}
		if res[0].ToolCalls[0].Function.Arguments != `{"a":1}` {
			t.Errorf("tc 0 expected {\"a\":1}, got %q", res[0].ToolCalls[0].Function.Arguments)
		}
		if res[0].ToolCalls[1].Function.Arguments != "{}" {
			t.Errorf("tc 1 expected {}, got %q", res[0].ToolCalls[1].Function.Arguments)
		}
		if res[0].ToolCalls[2].Function.Arguments != "{}" {
			t.Errorf("tc 2 expected {}, got %q", res[0].ToolCalls[2].Function.Arguments)
		}
		if res[0].ToolCalls[3].Function.Arguments != "{}" {
			t.Errorf("tc 3 expected {}, got %q", res[0].ToolCalls[3].Function.Arguments)
		}

		// Check message 1 (typed block inputs)
		if len(res[1].ToolCalls) != 4 {
			t.Fatalf("expected 4 tool calls in msg 1, got %d", len(res[1].ToolCalls))
		}
		if res[1].ToolCalls[0].Function.Arguments != `{"k":"v"}` {
			t.Errorf("tc 4 expected {\"k\":\"v\"}, got %q", res[1].ToolCalls[0].Function.Arguments)
		}
		if res[1].ToolCalls[1].Function.Arguments != "{}" {
			t.Errorf("tc 5 expected {}, got %q", res[1].ToolCalls[1].Function.Arguments)
		}
		if res[1].ToolCalls[2].Function.Arguments != "{}" {
			t.Errorf("tc 6 expected {}, got %q", res[1].ToolCalls[2].Function.Arguments)
		}
		if res[1].ToolCalls[3].Function.Arguments != "{}" {
			t.Errorf("tc 7 expected {}, got %q", res[1].ToolCalls[3].Function.Arguments)
		}
	})

	t.Run("S1 is_error prefix test", func(t *testing.T) {
		msgs := []translator.AnthropicMessage{
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{
						"type":        "tool_result",
						"tool_use_id": "tu_err_1",
						"content":     "connection refused",
						"is_error":    true,
					},
				},
			},
			{
				Role: "user",
				Content: []translator.ContentBlock{
					{
						Type:      "tool_result",
						ToolUseID: "tu_err_2",
						Content:   "file not found",
						IsError:   true,
					},
				},
			},
		}

		res := convertAnthropicMessagesToStore(msgs)
		if len(res) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(res))
		}
		expected0 := "[tool_result tu_err_1] Error: connection refused"
		if res[0].Content != expected0 {
			t.Errorf("expected %q, got %q", expected0, res[0].Content)
		}
		expected1 := "[tool_result tu_err_2] Error: file not found"
		if res[1].Content != expected1 {
			t.Errorf("expected %q, got %q", expected1, res[1].Content)
		}
	})

	t.Run("S2 non-text blocks placeholder in tool_result content", func(t *testing.T) {
		msgs := []translator.AnthropicMessage{
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{
						"type":        "tool_result",
						"tool_use_id": "tu_img_1",
						"content": []interface{}{
							map[string]interface{}{"type": "image", "source": map[string]interface{}{"type": "base64"}},
							map[string]interface{}{"type": "text", "text": "image caption"},
							map[string]interface{}{"type": "document", "data": "xyz"},
						},
					},
				},
			},
			{
				Role: "user",
				Content: []translator.ContentBlock{
					{
						Type:      "tool_result",
						ToolUseID: "tu_img_2",
						Content: []translator.ContentBlock{
							{Type: "image"},
							{Type: "custom_type"},
						},
					},
				},
			},
		}

		res := convertAnthropicMessagesToStore(msgs)
		if len(res) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(res))
		}
		expected0 := "[tool_result tu_img_1] [image]\nimage caption\n[unsupported block]"
		if res[0].Content != expected0 {
			t.Errorf("expected %q, got %q", expected0, res[0].Content)
		}
		expected1 := "[tool_result tu_img_2] [image]\n[unsupported block]"
		if res[1].Content != expected1 {
			t.Errorf("expected %q, got %q", expected1, res[1].Content)
		}
	})

	t.Run("S3 empty tool_use_id yields bare content without marker", func(t *testing.T) {
		msgs := []translator.AnthropicMessage{
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{
						"type":        "tool_result",
						"tool_use_id": "",
						"content":     "bare tool result content",
					},
				},
			},
			{
				Role: "user",
				Content: []translator.ContentBlock{
					{
						Type:      "tool_result",
						ToolUseID: "",
						Content:   "typed bare tool result",
					},
				},
			},
		}

		res := convertAnthropicMessagesToStore(msgs)
		if len(res) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(res))
		}
		if res[0].Content != "bare tool result content" {
			t.Errorf("expected %q, got %q", "bare tool result content", res[0].Content)
		}
		if res[1].Content != "typed bare tool result" {
			t.Errorf("expected %q, got %q", "typed bare tool result", res[1].Content)
		}
	})
}

func parseAnthropicReq(m map[string]interface{}) *translator.AnthropicRequest {
	bodyBytes, _ := json.Marshal(m)
	var req translator.AnthropicRequest
	json.Unmarshal(bodyBytes, &req)
	return &req
}

func TestAnthropicAdapter_WriteNonStreamResponse(t *testing.T) {
	adapter := NewAnthropicAdapter()

	openaiResp := `{"choices":[{"message":{"role":"assistant","content":"Hello"}}]}`
	rec := httptest.NewRecorder()

	err := adapter.WriteNonStreamResponse(rec, []byte(openaiResp))
	if err != nil {
		t.Fatalf("WriteNonStreamResponse() error = %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["type"] != "message" {
		t.Error("expected type 'message' in response")
	}
}

func TestAnthropicAdapter_WriteStreamEvent(t *testing.T) {
	adapter := NewAnthropicAdapter()

	rec := httptest.NewRecorder()
	chunk := []byte(`{"choices":[{"delta":{"content":"Hello"}}]}`)

	err := adapter.WriteStreamEvent(rec, chunk)
	if err == nil {
		t.Error("expected error for streaming (requires buffered translation)")
	}
}

func TestAnthropicAdapter_WriteStreamDone(t *testing.T) {
	adapter := NewAnthropicAdapter()

	rec := httptest.NewRecorder()

	err := adapter.WriteStreamDone(rec)
	if err != nil {
		t.Fatalf("WriteStreamDone() error = %v", err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "event: message_stop") {
		t.Error("expected 'event: message_stop' in response")
	}
}

func TestAnthropicAdapter_SetStreamHeaders(t *testing.T) {
	adapter := NewAnthropicAdapter()

	rec := httptest.NewRecorder()

	adapter.SetStreamHeaders(rec)

	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Error("expected Content-Type: text/event-stream")
	}
	if rec.Header().Get("Cache-Control") != "no-cache, no-transform" {
		t.Error("expected Cache-Control: no-cache")
	}
}

func TestAnthropicAdapter_WriteError(t *testing.T) {
	adapter := NewAnthropicAdapter()

	rec := httptest.NewRecorder()

	adapter.WriteError(rec, "invalid_request_error", "Invalid request", 400)

	if rec.Code != 400 {
		t.Errorf("expected status 400, got %d", rec.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["type"] != "error" {
		t.Error("expected type 'error' in response")
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected error object in response")
	}
	if errObj["type"] != "invalid_request_error" {
		t.Errorf("expected error type 'invalid_request_error', got '%v'", errObj["type"])
	}
}

func TestAnthropicAdapter_WriteStreamError(t *testing.T) {
	adapter := NewAnthropicAdapter()

	rec := httptest.NewRecorder()

	adapter.WriteStreamError(rec, "rate_limit_error", "Rate limit exceeded")

	body := rec.Body.String()
	if !strings.Contains(body, "event: message_stop") {
		t.Error("expected 'event: message_stop' in response")
	}
	if !strings.Contains(body, "event: error") {
		t.Error("expected 'event: error' in response")
	}

	// Find the data line that follows the error event (not message_stop)
	lines := strings.Split(body, "\n")
	var dataLine string
	for i, line := range lines {
		if strings.TrimSpace(line) == "event: error" {
			// Next non-empty line should be the data line
			for j := i + 1; j < len(lines); j++ {
				if strings.HasPrefix(lines[j], "data: ") {
					dataLine = strings.TrimPrefix(lines[j], "data: ")
					break
				}
			}
			break
		}
	}
	if dataLine == "" {
		t.Fatal("expected 'data: {...}' line after 'event: error'")
	}

	var errResp map[string]interface{}
	if err := json.Unmarshal([]byte(dataLine), &errResp); err != nil {
		t.Fatalf("failed to parse error response JSON: %v", err)
	}

	errObj, ok := errResp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'error' object in response")
	}
	if errObj["type"] != "rate_limit_error" {
		t.Errorf("expected type 'rate_limit_error', got '%v'", errObj["type"])
	}
	if errObj["message"] != "Rate limit exceeded" {
		t.Errorf("expected message 'Rate limit exceeded', got '%v'", errObj["message"])
	}
}

func TestAnthropicAdapter_WriteStreamErrorWithCode(t *testing.T) {
	adapter := NewAnthropicAdapter()

	rec := httptest.NewRecorder()

	adapter.WriteStreamErrorWithCode(rec, "invalid_request_error", "INVALID_MODEL", "Model not found")

	body := rec.Body.String()
	if !strings.Contains(body, "event: message_stop") {
		t.Error("expected 'event: message_stop' in response")
	}
	if !strings.Contains(body, "event: error") {
		t.Error("expected 'event: error' in response")
	}

	// Find the data line that follows the error event (not message_stop)
	lines := strings.Split(body, "\n")
	var dataLine string
	for i, line := range lines {
		if strings.TrimSpace(line) == "event: error" {
			// Next non-empty line should be the data line
			for j := i + 1; j < len(lines); j++ {
				if strings.HasPrefix(lines[j], "data: ") {
					dataLine = strings.TrimPrefix(lines[j], "data: ")
					break
				}
			}
			break
		}
	}
	if dataLine == "" {
		t.Fatal("expected 'data: {...}' line after 'event: error'")
	}

	var errResp map[string]interface{}
	if err := json.Unmarshal([]byte(dataLine), &errResp); err != nil {
		t.Fatalf("failed to parse error response JSON: %v", err)
	}

	errObj, ok := errResp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'error' object in response")
	}
	if errObj["type"] != "invalid_request_error" {
		t.Errorf("expected type 'invalid_request_error', got '%v'", errObj["type"])
	}
	if errObj["message"] != "Model not found" {
		t.Errorf("expected message 'Model not found', got '%v'", errObj["message"])
	}
	if errObj["code"] != "INVALID_MODEL" {
		t.Errorf("expected code 'INVALID_MODEL', got '%v'", errObj["code"])
	}
}

func TestAnthropicAdapter_WriteErrorWithCode(t *testing.T) {
	adapter := NewAnthropicAdapter()

	rec := httptest.NewRecorder()

	adapter.WriteErrorWithCode(rec, "rate_limit_error", "RATE_LIMIT", "Too many requests", 429)

	if rec.Code != 429 {
		t.Errorf("expected status 429, got %d", rec.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected error object in response")
	}
	if errObj["code"] != "RATE_LIMIT" {
		t.Errorf("expected code 'RATE_LIMIT', got '%v'", errObj["code"])
	}
}

func TestAnthropicAdapter_ToUpstreamRequest(t *testing.T) {
	adapter := NewAnthropicAdapter()

	body := map[string]interface{}{
		"model":      "claude-3-sonnet",
		"max_tokens": 1024,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "Hello"},
		},
	}

	result, err := adapter.ToUpstreamRequest(body)
	if err != nil {
		t.Fatalf("ToUpstreamRequest() error = %v", err)
	}

	var parsed map[string]interface{}
	json.Unmarshal(result, &parsed)

	// Should be converted to OpenAI format
	if parsed["model"] == nil {
		t.Error("expected model in converted request")
	}
}

func TestAnthropicAdapter_ToStoreMessages(t *testing.T) {
	adapter := NewAnthropicAdapter()

	body := map[string]interface{}{
		"model":      "claude-3-sonnet",
		"max_tokens": 1024,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "Hello"},
		},
	}

	messages := adapter.ToStoreMessages(body)
	if len(messages) == 0 {
		t.Error("expected non-empty messages")
	}
}

func TestAnthropicAdapter_ToStoreMessages_WithSystem(t *testing.T) {
	adapter := NewAnthropicAdapter()

	body := map[string]interface{}{
		"model":      "claude-3-sonnet",
		"max_tokens": 1024,
		"system":     "You are helpful",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "Hello"},
		},
	}

	messages := adapter.ToStoreMessages(body)
	if len(messages) != 2 {
		t.Errorf("expected 2 messages (system + user), got %d", len(messages))
	}
	if messages[0].Role != "system" {
		t.Errorf("expected first message role 'system', got '%s'", messages[0].Role)
	}
}
