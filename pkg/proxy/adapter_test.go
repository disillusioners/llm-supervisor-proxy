package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

func TestResponseState_Reset(t *testing.T) {
	rs := &ResponseState{
		contentBuilder:  strings.Builder{},
		thinkingBuilder: strings.Builder{},
		ToolCalls: []store.ToolCall{
			{ID: "call_1", Type: "function"},
		},
	}
	rs.contentBuilder.WriteString("Hello")
	rs.thinkingBuilder.WriteString("Thinking...")

	rs.Reset()

	if rs.contentBuilder.Len() != 0 {
		t.Errorf("expected empty content builder, got length %d", rs.contentBuilder.Len())
	}
	if rs.thinkingBuilder.Len() != 0 {
		t.Errorf("expected empty thinking builder, got length %d", rs.thinkingBuilder.Len())
	}
	if rs.ToolCalls != nil {
		t.Errorf("expected nil ToolCalls, got %v", rs.ToolCalls)
	}
}

func TestResponseState_AppendContent(t *testing.T) {
	rs := &ResponseState{}
	rs.AppendContent("Hello")
	rs.AppendContent(" World")

	if got := rs.GetContent(); got != "Hello World" {
		t.Errorf("expected 'Hello World', got '%s'", got)
	}
}

func TestResponseState_AppendThinking(t *testing.T) {
	rs := &ResponseState{}
	rs.AppendThinking("Thinking step 1")
	rs.AppendThinking(" step 2")

	if got := rs.GetThinking(); got != "Thinking step 1 step 2" {
		t.Errorf("expected 'Thinking step 1 step 2', got '%s'", got)
	}
}

func TestResponseState_AddToolCall(t *testing.T) {
	rs := &ResponseState{}
	rs.AddToolCall(store.ToolCall{ID: "call_1", Type: "function"})
	rs.AddToolCall(store.ToolCall{ID: "call_2", Type: "function"})

	if len(rs.ToolCalls) != 2 {
		t.Errorf("expected 2 tool calls, got %d", len(rs.ToolCalls))
	}
	if rs.ToolCalls[0].ID != "call_1" {
		t.Errorf("expected first tool call ID 'call_1', got '%s'", rs.ToolCalls[0].ID)
	}
}

func TestResponseState_GetContent(t *testing.T) {
	rs := &ResponseState{}
	rs.AppendContent("Test content")

	if got := rs.GetContent(); got != "Test content" {
		t.Errorf("expected 'Test content', got '%s'", got)
	}
}

func TestResponseState_GetThinking(t *testing.T) {
	rs := &ResponseState{}
	rs.AppendThinking("Test thinking")

	if got := rs.GetThinking(); got != "Test thinking" {
		t.Errorf("expected 'Test thinking', got '%s'", got)
	}
}

func TestResponseState_ToAssistantMessage(t *testing.T) {
	tests := []struct {
		name     string
		state    ResponseState
		expected store.Message
	}{
		{
			name: "content only",
			state: func() ResponseState {
				rs := &ResponseState{}
				rs.AppendContent("Hello")
				rs.AppendThinking("Thinking...")
				return *rs
			}(),
			expected: store.Message{
				Role:     "assistant",
				Content:  "Hello",
				Thinking: "Thinking...",
			},
		},
		{
			name: "with tool calls",
			state: func() ResponseState {
				rs := &ResponseState{}
				rs.AppendContent("Using tool")
				rs.AddToolCall(store.ToolCall{
					ID:   "call_1",
					Type: "function",
					Function: store.Function{
						Name:      "get_weather",
						Arguments: `{"location":"SF"}`,
					},
				})
				return *rs
			}(),
			expected: store.Message{
				Role:    "assistant",
				Content: "Using tool",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.state.ToAssistantMessage()
			if msg.Role != tt.expected.Role {
				t.Errorf("expected role '%s', got '%s'", tt.expected.Role, msg.Role)
			}
			if msg.Content != tt.expected.Content {
				t.Errorf("expected content '%s', got '%s'", tt.expected.Content, msg.Content)
			}
			if msg.Thinking != tt.expected.Thinking {
				t.Errorf("expected thinking '%s', got '%s'", tt.expected.Thinking, msg.Thinking)
			}
		})
	}
}

func TestResponseExtractor_ExtractFromNonStream(t *testing.T) {
	extractor := &ResponseExtractor{}

	tests := []struct {
		name         string
		input        string
		wantContent  string
		wantThinking string
		wantTools    int
		wantErr      bool
	}{
		{
			name:         "simple content",
			input:        `{"choices":[{"message":{"role":"assistant","content":"Hello"}}]}`,
			wantContent:  "Hello",
			wantThinking: "",
			wantTools:    0,
		},
		{
			name:         "with reasoning_content",
			input:        `{"choices":[{"message":{"role":"assistant","content":"Answer","reasoning_content":"Thinking..."}}]}`,
			wantContent:  "Answer",
			wantThinking: "Thinking...",
			wantTools:    0,
		},
		{
			name:        "with tool calls",
			input:       `{"choices":[{"message":{"role":"assistant","content":"Using tool","tool_calls":[{"id":"call_1","type":"function","function":{"name":"test","arguments":"{}"}}]}}]}`,
			wantContent: "Using tool",
			wantTools:   1,
		},
		{
			name:         "empty choices",
			input:        `{"choices":[]}`,
			wantContent:  "",
			wantThinking: "",
			wantTools:    0,
		},
		{
			name:         "nil message",
			input:        `{"choices":[{"message":null}]}`,
			wantContent:  "",
			wantThinking: "",
			wantTools:    0,
		},
		{
			name:    "invalid json",
			input:   `not json`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, err := extractor.ExtractFromNonStream([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Errorf("ExtractFromNonStream() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if state.GetContent() != tt.wantContent {
					t.Errorf("expected content '%s', got '%s'", tt.wantContent, state.GetContent())
				}
				if state.GetThinking() != tt.wantThinking {
					t.Errorf("expected thinking '%s', got '%s'", tt.wantThinking, state.GetThinking())
				}
				if len(state.ToolCalls) != tt.wantTools {
					t.Errorf("expected %d tool calls, got %d", tt.wantTools, len(state.ToolCalls))
				}
			}
		})
	}
}

func TestResponseExtractor_ExtractFromStreamDelta(t *testing.T) {
	extractor := &ResponseExtractor{}

	tests := []struct {
		name     string
		delta    map[string]interface{}
		wantCont string
		wantThnk string
	}{
		{
			name:     "content only",
			delta:    map[string]interface{}{"content": "Hello"},
			wantCont: "Hello",
			wantThnk: "",
		},
		{
			name:     "with reasoning_content",
			delta:    map[string]interface{}{"content": "Answer", "reasoning_content": "Thinking"},
			wantCont: "Answer",
			wantThnk: "Thinking",
		},
		{
			name:     "empty delta",
			delta:    map[string]interface{}{},
			wantCont: "",
			wantThnk: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, thinking := extractor.ExtractFromStreamDelta(tt.delta)
			if content != tt.wantCont {
				t.Errorf("expected content '%s', got '%s'", tt.wantCont, content)
			}
			if thinking != tt.wantThnk {
				t.Errorf("expected thinking '%s', got '%s'", tt.wantThnk, thinking)
			}
		})
	}
}

func TestGetString(t *testing.T) {
	tests := []struct {
		name     string
		m        map[string]interface{}
		key      string
		expected string
	}{
		{
			name:     "existing string",
			m:        map[string]interface{}{"key": "value"},
			key:      "key",
			expected: "value",
		},
		{
			name:     "missing key",
			m:        map[string]interface{}{"other": "value"},
			key:      "key",
			expected: "",
		},
		{
			name:     "non-string value",
			m:        map[string]interface{}{"key": 123},
			key:      "key",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getString(tt.m, tt.key); got != tt.expected {
				t.Errorf("getString() = '%s', want '%s'", got, tt.expected)
			}
		})
	}
}

func TestExtractOpenAIParameters(t *testing.T) {
	tests := []struct {
		name     string
		body     map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name: "only standard fields",
			body: map[string]interface{}{
				"model":       "gpt-4",
				"messages":    []interface{}{},
				"temperature": 0.7,
			},
			expected: nil,
		},
		{
			name: "with extra fields",
			body: map[string]interface{}{
				"model":        "gpt-4",
				"messages":     []interface{}{},
				"custom_param": "value",
				"another":      123,
			},
			expected: map[string]interface{}{
				"custom_param": "value",
				"another":      123,
			},
		},
		{
			name:     "empty body",
			body:     map[string]interface{}{},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractOpenAIParameters(tt.body)
			if tt.expected == nil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			for k, v := range tt.expected {
				if got[k] != v {
					t.Errorf("expected %s=%v, got %v", k, v, got[k])
				}
			}
		})
	}
}

func TestParseOpenAIMessages(t *testing.T) {
	tests := []struct {
		name     string
		body     map[string]interface{}
		expected int
	}{
		{
			name: "simple messages",
			body: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{"role": "user", "content": "Hello"},
					map[string]interface{}{"role": "assistant", "content": "Hi"},
				},
			},
			expected: 2,
		},
		{
			name: "with reasoning_content",
			body: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{"role": "assistant", "content": "Answer", "reasoning_content": "Thinking"},
				},
			},
			expected: 1,
		},
		{
			name: "with tool calls",
			body: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{
						"role":    "assistant",
						"content": "Using tool",
						"tool_calls": []interface{}{
							map[string]interface{}{
								"id":   "call_1",
								"type": "function",
								"function": map[string]interface{}{
									"name":      "test",
									"arguments": "{}",
								},
							},
						},
					},
				},
			},
			expected: 1,
		},
		{
			name:     "no messages",
			body:     map[string]interface{}{},
			expected: 0,
		},
		{
			name: "with multimodal content",
			body: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{
						"role": "user",
						"content": []interface{}{
							map[string]interface{}{"type": "text", "text": "Hello"},
						},
					},
				},
			},
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := parseOpenAIMessages(tt.body)
			if len(messages) != tt.expected {
				t.Errorf("expected %d messages, got %d", tt.expected, len(messages))
			}
		})
	}
}

func TestExtractContentAsString(t *testing.T) {
	tests := []struct {
		name     string
		content  interface{}
		expected string
	}{
		{
			name:     "string content",
			content:  "Hello",
			expected: "Hello",
		},
		{
			name: "text blocks",
			content: []interface{}{
				map[string]interface{}{"type": "text", "text": "Hello"},
				map[string]interface{}{"type": "text", "text": " World"},
			},
			expected: "Hello\n World",
		},
		{
			name:     "mixed blocks with non-text",
			content:  []interface{}{map[string]interface{}{"type": "image", "data": "..."}},
			expected: "",
		},
		{
			name:     "nil content",
			content:  nil,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractContentAsString(tt.content)
			if got != tt.expected {
				t.Errorf("extractContentAsString() = '%s', want '%s'", got, tt.expected)
			}
		})
	}
}

func TestAdapterGetString(t *testing.T) {
	tests := []struct {
		name     string
		m        map[string]interface{}
		key      string
		expected string
	}{
		{
			name:     "existing string",
			m:        map[string]interface{}{"key": "value"},
			key:      "key",
			expected: "value",
		},
		{
			name:     "missing key",
			m:        map[string]interface{}{"other": "value"},
			key:      "key",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := adapterGetString(tt.m, tt.key); got != tt.expected {
				t.Errorf("adapterGetString() = '%s', want '%s'", got, tt.expected)
			}
		})
	}
}

func TestExtractToolCallsFromOpenAI(t *testing.T) {
	tests := []struct {
		name     string
		msgMap   map[string]interface{}
		expected int
	}{
		{
			name: "with tool calls",
			msgMap: map[string]interface{}{
				"tool_calls": []interface{}{
					map[string]interface{}{
						"id":   "call_1",
						"type": "function",
						"function": map[string]interface{}{
							"name":      "test",
							"arguments": "{}",
						},
					},
				},
			},
			expected: 1,
		},
		{
			name:     "no tool calls",
			msgMap:   map[string]interface{}{},
			expected: 0,
		},
		{
			name: "invalid tool_calls type",
			msgMap: map[string]interface{}{
				"tool_calls": "not an array",
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolCalls := extractToolCallsFromOpenAI(tt.msgMap)
			if len(toolCalls) != tt.expected {
				t.Errorf("expected %d tool calls, got %d", tt.expected, len(toolCalls))
			}
		})
	}
}

func TestExtractResponseFromOpenAI(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantCont     string
		wantThinking string
		wantTools    int
		wantErr      bool
	}{
		{
			name:         "simple response",
			input:        `{"choices":[{"message":{"role":"assistant","content":"Hello"}}]}`,
			wantCont:     "Hello",
			wantThinking: "",
			wantTools:    0,
		},
		{
			name:         "with reasoning",
			input:        `{"choices":[{"message":{"role":"assistant","content":"Answer","reasoning_content":"Thinking"}}]}`,
			wantCont:     "Answer",
			wantThinking: "Thinking",
			wantTools:    0,
		},
		{
			name:      "with tool calls",
			input:     `{"choices":[{"message":{"role":"assistant","content":"Tool","tool_calls":[{"id":"call_1","type":"function","function":{"name":"test","arguments":"{}"}}]}}]}`,
			wantCont:  "Tool",
			wantTools: 1,
		},
		{
			name:    "invalid json",
			input:   `not json`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, thinking, toolCalls, err := extractResponseFromOpenAI([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Errorf("extractResponseFromOpenAI() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if content != tt.wantCont {
					t.Errorf("expected content '%s', got '%s'", tt.wantCont, content)
				}
				if thinking != tt.wantThinking {
					t.Errorf("expected thinking '%s', got '%s'", tt.wantThinking, thinking)
				}
				if len(toolCalls) != tt.wantTools {
					t.Errorf("expected %d tool calls, got %d", tt.wantTools, len(toolCalls))
				}
			}
		})
	}
}

func TestOpenAIAdapter_WriteNonStreamResponse(t *testing.T) {
	adapter := NewOpenAIAdapter()

	body := `{"choices":[{"message":{"role":"assistant","content":"Hello"}}]}`
	rec := httptest.NewRecorder()

	err := adapter.WriteNonStreamResponse(rec, []byte(body))
	if err != nil {
		t.Fatalf("WriteNonStreamResponse() error = %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["choices"] == nil {
		t.Error("expected choices in response")
	}
}

func TestOpenAIAdapter_WriteStreamEvent(t *testing.T) {
	adapter := NewOpenAIAdapter()

	rec := httptest.NewRecorder()

	chunk := []byte(`{"choices":[{"delta":{"content":"Hello"}}]}`)
	err := adapter.WriteStreamEvent(rec, chunk)
	if err != nil {
		t.Fatalf("WriteStreamEvent() error = %v", err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "data: ") {
		t.Error("expected SSE format with 'data: ' prefix")
	}
}

func TestOpenAIAdapter_WriteStreamDone(t *testing.T) {
	adapter := NewOpenAIAdapter()

	rec := httptest.NewRecorder()

	err := adapter.WriteStreamDone(rec)
	if err != nil {
		t.Fatalf("WriteStreamDone() error = %v", err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "data: [DONE]") {
		t.Error("expected 'data: [DONE]' in body")
	}
}

func TestOpenAIAdapter_SetStreamHeaders(t *testing.T) {
	adapter := NewOpenAIAdapter()

	rec := httptest.NewRecorder()

	adapter.SetStreamHeaders(rec)

	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Error("expected Content-Type: text/event-stream")
	}
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Error("expected Cache-Control: no-cache")
	}
}

func TestOpenAIAdapter_WriteError(t *testing.T) {
	adapter := NewOpenAIAdapter()

	rec := httptest.NewRecorder()

	adapter.WriteError(rec, "invalid_request", "Missing model", 400)

	if rec.Code != 400 {
		t.Errorf("expected status 400, got %d", rec.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected error object in response")
	}
	if errObj["type"] != "invalid_request" {
		t.Errorf("expected error type 'invalid_request', got '%v'", errObj["type"])
	}
}

func TestOpenAIAdapter_WriteStreamError(t *testing.T) {
	adapter := NewOpenAIAdapter()

	rec := httptest.NewRecorder()

	adapter.WriteStreamError(rec, "rate_limit", "Rate limit exceeded")

	body := rec.Body.String()
	if !strings.Contains(body, "data: ") {
		t.Error("expected SSE format with 'data: ' prefix")
	}
}

func TestOpenAIAdapter_WriteStreamErrorWithCode(t *testing.T) {
	adapter := NewOpenAIAdapter()

	rec := httptest.NewRecorder()

	adapter.WriteStreamErrorWithCode(rec, "invalid_request", "INVALID_MODEL", "Model not found")

	body := rec.Body.String()
	if !strings.Contains(body, `"code":"INVALID_MODEL"`) {
		t.Error("expected error code in response")
	}
}

func TestOpenAIAdapter_WriteErrorWithCode(t *testing.T) {
	adapter := NewOpenAIAdapter()

	rec := httptest.NewRecorder()

	adapter.WriteErrorWithCode(rec, "rate_limit", "RATE_LIMIT", "Too many requests", 429)

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

func TestOpenAIAdapter_Protocol(t *testing.T) {
	adapter := NewOpenAIAdapter()
	if adapter.Protocol() != "openai" {
		t.Errorf("expected protocol 'openai', got '%s'", adapter.Protocol())
	}
}

func TestOpenAIAdapter_IsStream(t *testing.T) {
	adapter := NewOpenAIAdapter()

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

func TestOpenAIAdapter_ExtractUpstreamModel(t *testing.T) {
	adapter := NewOpenAIAdapter()

	body := map[string]interface{}{"model": "gpt-4o"}
	model := adapter.ExtractUpstreamModel(body, nil)
	if model != "gpt-4o" {
		t.Errorf("expected 'gpt-4o', got '%s'", model)
	}
}

func TestOpenAIAdapter_ToUpstreamRequest(t *testing.T) {
	adapter := NewOpenAIAdapter()

	body := map[string]interface{}{
		"model":       "gpt-4o",
		"messages":    []interface{}{map[string]interface{}{"role": "user", "content": "Hi"}},
		"temperature": 0.7,
	}

	result, err := adapter.ToUpstreamRequest(body, nil)
	if err != nil {
		t.Fatalf("ToUpstreamRequest() error = %v", err)
	}

	var parsed map[string]interface{}
	json.Unmarshal(result, &parsed)

	if parsed["model"] != "gpt-4o" {
		t.Errorf("expected model 'gpt-4o', got '%v'", parsed["model"])
	}
}

func TestOpenAIAdapter_ParseRequest(t *testing.T) {
	adapter := NewOpenAIAdapter()

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"Hi"}],"stream":true,"temperature":0.7}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))

	_, meta, err := adapter.ParseRequest(req)
	if err != nil {
		t.Fatalf("ParseRequest() error = %v", err)
	}

	if meta.ClientModel != "gpt-4o" {
		t.Errorf("expected client model 'gpt-4o', got '%s'", meta.ClientModel)
	}
	if !meta.IsStream {
		t.Error("expected IsStream to be true")
	}
}

func TestOpenAIAdapter_ToStoreMessages(t *testing.T) {
	adapter := NewOpenAIAdapter()

	body := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "Hello"},
		},
	}

	messages := adapter.ToStoreMessages(body)
	if len(messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(messages))
	}
	if messages[0].Role != "user" {
		t.Errorf("expected role 'user', got '%s'", messages[0].Role)
	}
}
