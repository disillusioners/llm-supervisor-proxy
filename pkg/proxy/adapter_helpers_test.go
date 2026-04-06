package proxy

import (
	"encoding/json"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

func TestAdapterExtractOpenAIParameters(t *testing.T) {
	tests := []struct {
		name    string
		body    map[string]interface{}
		want    map[string]interface{}
		wantNil bool
	}{
		{
			name:    "nil body",
			body:    nil,
			wantNil: true,
		},
		{
			name:    "empty body",
			body:    map[string]interface{}{},
			wantNil: true,
		},
		{
			name: "only standard fields",
			body: map[string]interface{}{
				"model":       "gpt-4",
				"messages":    []interface{}{},
				"temperature": 0.7,
			},
			wantNil: true,
		},
		{
			name: "mixed standard and non-standard fields",
			body: map[string]interface{}{
				"model":        "gpt-4",
				"temperature":  0.7,
				"custom_param": "value",
				"seed":         42,
			},
			want: map[string]interface{}{
				"custom_param": "value",
				"seed":         42,
			},
			wantNil: false,
		},
		{
			name: "only non-standard fields",
			body: map[string]interface{}{
				"response_format": map[string]interface{}{"type": "json_object"},
				"seed":            123,
				"extra":           "data",
			},
			want: map[string]interface{}{
				"response_format": map[string]interface{}{"type": "json_object"},
				"seed":            123,
				"extra":           "data",
			},
			wantNil: false,
		},
		{
			name: "all standard fields present",
			body: map[string]interface{}{
				"model":             "gpt-4",
				"messages":          []interface{}{},
				"stream":            true,
				"max_tokens":        100,
				"temperature":       0.7,
				"top_p":             0.9,
				"n":                 1,
				"stop":              nil,
				"presence_penalty":  0.0,
				"frequency_penalty": 0.0,
				"logit_bias":        map[string]int{},
				"user":              "user123",
				"tools":             []interface{}{},
				"tool_choice":       "auto",
			},
			wantNil: true,
		},
		{
			name: "non-standard with complex nested value",
			body: map[string]interface{}{
				"model":      "gpt-4",
				"metadata":   map[string]interface{}{"key": "value"},
				"dimensions": 1024,
			},
			want: map[string]interface{}{
				"metadata":   map[string]interface{}{"key": "value"},
				"dimensions": 1024,
			},
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractOpenAIParameters(tt.body)
			if tt.wantNil {
				if got != nil {
					t.Errorf("extractOpenAIParameters() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Errorf("extractOpenAIParameters() = nil, want %v", tt.want)
				return
			}
			// Check each expected key exists with the same value
			for k, v := range tt.want {
				if gv, ok := got[k]; !ok || !interfaceEqual(gv, v) {
					t.Errorf("extractOpenAIParameters()[%q] = %v, want %v", k, gv, v)
				}
			}
			// Check no extra keys
			for k, v := range got {
				if _, ok := tt.want[k]; !ok {
					t.Errorf("extractOpenAIParameters() has unexpected key %q = %v", k, v)
				}
			}
		})
	}
}

func TestAdapterParseOpenAIMessages(t *testing.T) {
	tests := []struct {
		name string
		body map[string]interface{}
		want []store.Message
	}{
		{
			name: "nil body",
			body: nil,
			want: []store.Message{},
		},
		{
			name: "empty body",
			body: map[string]interface{}{},
			want: []store.Message{},
		},
		{
			name: "no messages key",
			body: map[string]interface{}{
				"model": "gpt-4",
			},
			want: []store.Message{},
		},
		{
			name: "messages is not array",
			body: map[string]interface{}{
				"messages": "not an array",
			},
			want: []store.Message{},
		},
		{
			name: "empty messages array",
			body: map[string]interface{}{
				"messages": []interface{}{},
			},
			want: []store.Message{},
		},
		{
			name: "single user message",
			body: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{
						"role":    "user",
						"content": "Hello",
					},
				},
			},
			want: []store.Message{
				{Role: "user", Content: "Hello"},
			},
		},
		{
			name: "multiple messages with roles",
			body: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{"role": "system", "content": "You are helpful"},
					map[string]interface{}{"role": "user", "content": "Hi"},
					map[string]interface{}{"role": "assistant", "content": "Hello!"},
				},
			},
			want: []store.Message{
				{Role: "system", Content: "You are helpful"},
				{Role: "user", Content: "Hi"},
				{Role: "assistant", Content: "Hello!"},
			},
		},
		{
			name: "message without role is skipped",
			body: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{"role": "user", "content": "Hello"},
					map[string]interface{}{"content": "no role"},
					map[string]interface{}{"role": "assistant", "content": "Hi"},
				},
			},
			want: []store.Message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "Hi"},
			},
		},
		{
			name: "message with empty role is skipped",
			body: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{"role": "", "content": "empty role"},
					map[string]interface{}{"role": "user", "content": "valid"},
				},
			},
			want: []store.Message{
				{Role: "user", Content: "valid"},
			},
		},
		{
			name: "message with reasoning_content",
			body: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{
						"role":              "assistant",
						"content":           "Let me think...",
						"reasoning_content": "Thinking process here",
					},
				},
			},
			want: []store.Message{
				{Role: "assistant", Content: "Let me think...", Thinking: "Thinking process here"},
			},
		},
		{
			name: "message with tool calls",
			body: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{
						"role":    "assistant",
						"content": "Using a tool",
						"tool_calls": []interface{}{
							map[string]interface{}{
								"id":   "call_123",
								"type": "function",
								"function": map[string]interface{}{
									"name":      "get_weather",
									"arguments": `{"location": "NYC"}`,
								},
							},
						},
					},
				},
			},
			want: []store.Message{
				{
					Role:    "assistant",
					Content: "Using a tool",
					ToolCalls: []store.ToolCall{
						{
							ID:   "call_123",
							Type: "function",
							Function: store.Function{
								Name:      "get_weather",
								Arguments: `{"location": "NYC"}`,
							},
						},
					},
				},
			},
		},
		{
			name: "message with empty tool_calls array",
			body: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{
						"role":       "assistant",
						"content":    "No tools",
						"tool_calls": []interface{}{},
					},
				},
			},
			want: []store.Message{
				{Role: "assistant", Content: "No tools", ToolCalls: nil},
			},
		},
		{
			name: "non-map message in array is skipped",
			body: map[string]interface{}{
				"messages": []interface{}{
					"not a map",
					map[string]interface{}{"role": "user", "content": "valid"},
					123,
				},
			},
			want: []store.Message{
				{Role: "user", Content: "valid"},
			},
		},
		{
			name: "multimodal content",
			body: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{
						"role": "user",
						"content": []interface{}{
							map[string]interface{}{"type": "text", "text": "Hello"},
							map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "http://..."}},
						},
					},
				},
			},
			want: []store.Message{
				{Role: "user", Content: "Hello"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseOpenAIMessages(tt.body)
			if len(got) != len(tt.want) {
				t.Errorf("parseOpenAIMessages() returned %d messages, want %d", len(got), len(tt.want))
				return
			}
			for i := range got {
				if got[i].Role != tt.want[i].Role {
					t.Errorf("parseOpenAIMessages()[%d].Role = %q, want %q", i, got[i].Role, tt.want[i].Role)
				}
				if got[i].Content != tt.want[i].Content {
					t.Errorf("parseOpenAIMessages()[%d].Content = %q, want %q", i, got[i].Content, tt.want[i].Content)
				}
				if got[i].Thinking != tt.want[i].Thinking {
					t.Errorf("parseOpenAIMessages()[%d].Thinking = %q, want %q", i, got[i].Thinking, tt.want[i].Thinking)
				}
				if len(got[i].ToolCalls) != len(tt.want[i].ToolCalls) {
					t.Errorf("parseOpenAIMessages()[%d].ToolCalls = %v, want %v", i, got[i].ToolCalls, tt.want[i].ToolCalls)
				}
				for j := range tt.want[i].ToolCalls {
					if got[i].ToolCalls[j].ID != tt.want[i].ToolCalls[j].ID {
						t.Errorf("parseOpenAIMessages()[%d].ToolCalls[%d].ID = %q, want %q", i, j, got[i].ToolCalls[j].ID, tt.want[i].ToolCalls[j].ID)
					}
					if got[i].ToolCalls[j].Type != tt.want[i].ToolCalls[j].Type {
						t.Errorf("parseOpenAIMessages()[%d].ToolCalls[%d].Type = %q, want %q", i, j, got[i].ToolCalls[j].Type, tt.want[i].ToolCalls[j].Type)
					}
					if got[i].ToolCalls[j].Function.Name != tt.want[i].ToolCalls[j].Function.Name {
						t.Errorf("parseOpenAIMessages()[%d].ToolCalls[%d].Function.Name = %q, want %q", i, j, got[i].ToolCalls[j].Function.Name, tt.want[i].ToolCalls[j].Function.Name)
					}
					if got[i].ToolCalls[j].Function.Arguments != tt.want[i].ToolCalls[j].Function.Arguments {
						t.Errorf("parseOpenAIMessages()[%d].ToolCalls[%d].Function.Arguments = %q, want %q", i, j, got[i].ToolCalls[j].Function.Arguments, tt.want[i].ToolCalls[j].Function.Arguments)
					}
				}
			}
		})
	}
}

func TestAdapterExtractContentAsString(t *testing.T) {
	tests := []struct {
		name    string
		content interface{}
		want    string
	}{
		{
			name:    "nil content",
			content: nil,
			want:    "",
		},
		{
			name:    "string content",
			content: "Hello, world!",
			want:    "Hello, world!",
		},
		{
			name:    "empty string",
			content: "",
			want:    "",
		},
		{
			name: "multimodal content with single text",
			content: []interface{}{
				map[string]interface{}{"type": "text", "text": "Hello"},
			},
			want: "Hello",
		},
		{
			name: "multimodal content with multiple text parts",
			content: []interface{}{
				map[string]interface{}{"type": "text", "text": "First part"},
				map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "http://..."}},
				map[string]interface{}{"type": "text", "text": "Second part"},
			},
			want: "First part\nSecond part",
		},
		{
			name: "multimodal content with no text parts",
			content: []interface{}{
				map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "http://..."}},
			},
			want: "",
		},
		{
			name:    "multimodal content with empty array",
			content: []interface{}{},
			want:    "",
		},
		{
			name:    "number content",
			content: 42,
			want:    "",
		},
		{
			name:    "bool content",
			content: true,
			want:    "",
		},
		{
			name: "multimodal with empty text string",
			content: []interface{}{
				map[string]interface{}{"type": "text", "text": ""},
			},
			want: "",
		},
		{
			name: "multimodal with nested non-string in part",
			content: []interface{}{
				map[string]interface{}{"type": "text", "text": "Valid"},
				map[string]interface{}{"type": "other", "data": 123},
			},
			want: "Valid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractContentAsString(tt.content)
			if got != tt.want {
				t.Errorf("extractContentAsString(%v) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}

func TestAdapterSafeGetString(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]interface{}
		key  string
		want string
	}{
		{
			name: "nil map",
			m:    nil,
			key:  "foo",
			want: "",
		},
		{
			name: "empty map",
			m:    map[string]interface{}{},
			key:  "foo",
			want: "",
		},
		{
			name: "key not present",
			m: map[string]interface{}{
				"other": "value",
			},
			key:  "foo",
			want: "",
		},
		{
			name: "key present with string value",
			m: map[string]interface{}{
				"foo": "bar",
			},
			key:  "foo",
			want: "bar",
		},
		{
			name: "key present with non-string value (int)",
			m: map[string]interface{}{
				"foo": 42,
			},
			key:  "foo",
			want: "",
		},
		{
			name: "key present with non-string value (map)",
			m: map[string]interface{}{
				"foo": map[string]interface{}{},
			},
			key:  "foo",
			want: "",
		},
		{
			name: "key present with empty string",
			m: map[string]interface{}{
				"foo": "",
			},
			key:  "foo",
			want: "",
		},
		{
			name: "multiple keys, getting second",
			m: map[string]interface{}{
				"first":  "1",
				"second": "2",
				"third":  3,
			},
			key:  "second",
			want: "2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := adapterGetString(tt.m, tt.key)
			if got != tt.want {
				t.Errorf("adapterGetString(%v, %q) = %q, want %q", tt.m, tt.key, got, tt.want)
			}
		})
	}
}

func TestAdapterExtractToolCallsFromOpenAI(t *testing.T) {
	tests := []struct {
		name   string
		msgMap map[string]interface{}
		want   []store.ToolCall
	}{
		{
			name:   "nil map",
			msgMap: nil,
			want:   nil,
		},
		{
			name:   "empty map",
			msgMap: map[string]interface{}{},
			want:   nil,
		},
		{
			name: "no tool_calls key",
			msgMap: map[string]interface{}{
				"role":    "assistant",
				"content": "Hello",
			},
			want: nil,
		},
		{
			name: "tool_calls is not array",
			msgMap: map[string]interface{}{
				"tool_calls": "not an array",
			},
			want: nil,
		},
		{
			name: "empty tool_calls array",
			msgMap: map[string]interface{}{
				"tool_calls": []interface{}{},
			},
			want: nil,
		},
		{
			name: "single tool call with full function details",
			msgMap: map[string]interface{}{
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
			want: []store.ToolCall{
				{
					ID:   "call_abc123",
					Type: "function",
					Function: store.Function{
						Name:      "get_weather",
						Arguments: `{"location":"NYC"}`,
					},
				},
			},
		},
		{
			name: "multiple tool calls",
			msgMap: map[string]interface{}{
				"tool_calls": []interface{}{
					map[string]interface{}{
						"id":   "call_1",
						"type": "function",
						"function": map[string]interface{}{
							"name":      "func1",
							"arguments": `{}`,
						},
					},
					map[string]interface{}{
						"id":   "call_2",
						"type": "function",
						"function": map[string]interface{}{
							"name":      "func2",
							"arguments": `{"key":"value"}`,
						},
					},
				},
			},
			want: []store.ToolCall{
				{ID: "call_1", Type: "function", Function: store.Function{Name: "func1", Arguments: `{}`}},
				{ID: "call_2", Type: "function", Function: store.Function{Name: "func2", Arguments: `{"key":"value"}`}},
			},
		},
		{
			name: "tool call without function details",
			msgMap: map[string]interface{}{
				"tool_calls": []interface{}{
					map[string]interface{}{
						"id":   "call_no_fn",
						"type": "function",
					},
				},
			},
			want: []store.ToolCall{
				{
					ID:       "call_no_fn",
					Type:     "function",
					Function: store.Function{},
				},
			},
		},
		{
			name: "tool call with empty function",
			msgMap: map[string]interface{}{
				"tool_calls": []interface{}{
					map[string]interface{}{
						"id":       "call_empty_fn",
						"type":     "function",
						"function": map[string]interface{}{},
					},
				},
			},
			want: []store.ToolCall{
				{
					ID:       "call_empty_fn",
					Type:     "function",
					Function: store.Function{},
				},
			},
		},
		{
			name: "mixed valid and invalid tool calls",
			msgMap: map[string]interface{}{
				"tool_calls": []interface{}{
					"not a map",
					map[string]interface{}{
						"id":   "call_valid",
						"type": "function",
						"function": map[string]interface{}{
							"name":      "valid_func",
							"arguments": `{"a":1}`,
						},
					},
					123,
				},
			},
			want: []store.ToolCall{
				{
					ID:       "call_valid",
					Type:     "function",
					Function: store.Function{Name: "valid_func", Arguments: `{"a":1}`},
				},
			},
		},
		{
			name: "tool call with non-string function fields",
			msgMap: map[string]interface{}{
				"tool_calls": []interface{}{
					map[string]interface{}{
						"id":   "call_nested",
						"type": "function",
						"function": map[string]interface{}{
							"name":      "nested_func",
							"arguments": 42, // non-string, should be ignored
						},
					},
				},
			},
			want: []store.ToolCall{
				{
					ID:       "call_nested",
					Type:     "function",
					Function: store.Function{Name: "nested_func", Arguments: ""},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractToolCallsFromOpenAI(tt.msgMap)
			if len(got) != len(tt.want) {
				t.Errorf("extractToolCallsFromOpenAI() returned %d tool calls, want %d", len(got), len(tt.want))
				return
			}
			for i := range got {
				if got[i].ID != tt.want[i].ID {
					t.Errorf("extractToolCallsFromOpenAI()[%d].ID = %q, want %q", i, got[i].ID, tt.want[i].ID)
				}
				if got[i].Type != tt.want[i].Type {
					t.Errorf("extractToolCallsFromOpenAI()[%d].Type = %q, want %q", i, got[i].Type, tt.want[i].Type)
				}
				if got[i].Function.Name != tt.want[i].Function.Name {
					t.Errorf("extractToolCallsFromOpenAI()[%d].Function.Name = %q, want %q", i, got[i].Function.Name, tt.want[i].Function.Name)
				}
				if got[i].Function.Arguments != tt.want[i].Function.Arguments {
					t.Errorf("extractToolCallsFromOpenAI()[%d].Function.Arguments = %q, want %q", i, got[i].Function.Arguments, tt.want[i].Function.Arguments)
				}
			}
		})
	}
}

func TestAdapterExtractResponseFromOpenAI(t *testing.T) {
	tests := []struct {
		name      string
		response  string
		wantCont  string
		wantThink string
		wantTC    []store.ToolCall
		wantErr   bool
	}{
		{
			name:     "invalid JSON",
			response: `not valid json`,
			wantErr:  true,
		},
		{
			name:      "empty response object",
			response:  `{}`,
			wantCont:  "",
			wantThink: "",
			wantTC:    nil,
			wantErr:   false,
		},
		{
			name:     "empty choices array",
			response: `{"choices": []}`,
			wantErr:  false,
		},
		{
			name:     "nil choices",
			response: `{"choices": null}`,
			wantErr:  false,
		},
		{
			name:     "choices is not array",
			response: `{"choices": "not array"}`,
			wantErr:  false,
		},
		{
			name:     "choice without message",
			response: `{"choices": [{}]}`,
			wantErr:  false,
		},
		{
			name:     "choice with nil message",
			response: `{"choices": [{"message": null}]}`,
			wantErr:  false,
		},
		{
			name:      "choice with empty message",
			response:  `{"choices": [{"message": {}}]}`,
			wantCont:  "",
			wantThink: "",
			wantTC:    nil,
			wantErr:   false,
		},
		{
			name:      "simple response with content",
			response:  `{"choices": [{"message": {"content": "Hello, world!"}}]}`,
			wantCont:  "Hello, world!",
			wantThink: "",
			wantTC:    nil,
			wantErr:   false,
		},
		{
			name:      "response with reasoning_content",
			response:  `{"choices": [{"message": {"content": "The answer is 42", "reasoning_content": "Let me calculate..."}}]}`,
			wantCont:  "The answer is 42",
			wantThink: "Let me calculate...",
			wantTC:    nil,
			wantErr:   false,
		},
		{
			name:     "response with null content",
			response: `{"choices": [{"message": {"content": null}}]}`,
			wantCont: "",
			wantErr:  false,
		},
		{
			name:     "response with tool calls",
			response: `{"choices": [{"message": {"content": "Using tool", "tool_calls": [{"id": "tc1", "type": "function", "function": {"name": "test", "arguments": "{}"}}]}}]}`,
			wantCont: "Using tool",
			wantTC: []store.ToolCall{
				{ID: "tc1", Type: "function", Function: store.Function{Name: "test", Arguments: "{}"}},
			},
			wantErr: false,
		},
		{
			name:     "response with multiple tool calls",
			response: `{"choices": [{"message": {"content": "", "tool_calls": [{"id": "1", "type": "f", "function": {"name": "a", "arguments": "x"}}, {"id": "2", "type": "f", "function": {"name": "b", "arguments": "y"}}]}}]}`,
			wantTC: []store.ToolCall{
				{ID: "1", Type: "f", Function: store.Function{Name: "a", Arguments: "x"}},
				{ID: "2", Type: "f", Function: store.Function{Name: "b", Arguments: "y"}},
			},
			wantErr: false,
		},
		{
			name:     "response with additional fields",
			response: `{"id": "chatcmpl-123", "object": "chat.completion", "choices": [{"message": {"content": "Hi"}}], "usage": {"prompt_tokens": 10, "completion_tokens": 5}}`,
			wantCont: "Hi",
			wantErr:  false,
		},
		{
			name:     "non-string content (number)",
			response: `{"choices": [{"message": {"content": 42}}]}`,
			wantCont: "",
			wantErr:  false,
		},
		{
			name:     "content is array (invalid but handled)",
			response: `{"choices": [{"message": {"content": ["text"]}}]}`,
			wantCont: "",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCont, gotThink, gotTC, err := extractResponseFromOpenAI([]byte(tt.response))
			if (err != nil) != tt.wantErr {
				t.Errorf("extractResponseFromOpenAI() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotCont != tt.wantCont {
				t.Errorf("extractResponseFromOpenAI() content = %q, want %q", gotCont, tt.wantCont)
			}
			if gotThink != tt.wantThink {
				t.Errorf("extractResponseFromOpenAI() thinking = %q, want %q", gotThink, tt.wantThink)
			}
			if len(gotTC) != len(tt.wantTC) {
				t.Errorf("extractResponseFromOpenAI() toolCalls = %v, want %v", gotTC, tt.wantTC)
				return
			}
			for i := range gotTC {
				if gotTC[i].ID != tt.wantTC[i].ID {
					t.Errorf("extractResponseFromOpenAI() toolCalls[%d].ID = %q, want %q", i, gotTC[i].ID, tt.wantTC[i].ID)
				}
				if gotTC[i].Function.Name != tt.wantTC[i].Function.Name {
					t.Errorf("extractResponseFromOpenAI() toolCalls[%d].Function.Name = %q, want %q", i, gotTC[i].Function.Name, tt.wantTC[i].Function.Name)
				}
			}
		})
	}
}

// Helper function to compare interface{} values
func interfaceEqual(a, b interface{}) bool {
	aJSON, errA := json.Marshal(a)
	bJSON, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return string(aJSON) == string(bJSON)
}
