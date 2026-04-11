package token

import (
	"testing"
)

func TestExtractPromptText_SingleMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "single_user_message",
			body: `{"messages": [{"role": "user", "content": "Hello"}]}`,
			want: "Hello",
		},
		{
			name: "single_system_message",
			body: `{"messages": [{"role": "system", "content": "You are a helpful assistant."}]}`,
			want: "You are a helpful assistant.",
		},
		{
			name: "single_assistant_message",
			body: `{"messages": [{"role": "assistant", "content": "I can help you."}]}`,
			want: "I can help you.",
		},
		{
			name: "empty_content",
			body: `{"messages": [{"role": "user", "content": ""}]}`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPromptText([]byte(tt.body))
			if got != tt.want {
				t.Errorf("extractPromptText(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

func TestExtractPromptText_MultipleMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "system_then_user",
			body: `{"messages": [{"role": "system", "content": "You are helpful."}, {"role": "user", "content": "Hi there"}]}`,
			want: "You are helpful.Hi there",
		},
		{
			name: "full_conversation",
			body: `{"messages": [{"role": "system", "content": "You are a coding assistant"}, {"role": "user", "content": "Write a function"}, {"role": "assistant", "content": "Here is the function:"}]}`,
			want: "You are a coding assistantWrite a functionHere is the function:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPromptText([]byte(tt.body))
			if got != tt.want {
				t.Errorf("extractPromptText(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

func TestExtractPromptText_MultimodalContentArray(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "text_type_only",
			body: `{"messages": [{"role": "user", "content": [{"type": "text", "text": "describe this image"}]}]}`,
			want: "describe this image",
		},
		{
			name: "text_and_image_url",
			body: `{"messages": [{"role": "user", "content": [{"type": "text", "text": "what do you see"}, {"type": "image_url", "url": "https://example.com/img.png"}]}]}`,
			want: "what do you see",
		},
		{
			name: "multiple_text_entries",
			body: `{"messages": [{"role": "user", "content": [{"type": "text", "text": "part one"}, {"type": "text", "text": "part two"}]}]}`,
			want: "part onepart two",
		},
		{
			name: "image_url_only_no_text",
			body: `{"messages": [{"role": "user", "content": [{"type": "image_url", "url": "https://example.com/img.png"}]}]}`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPromptText([]byte(tt.body))
			if got != tt.want {
				t.Errorf("extractPromptText(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

func TestExtractPromptText_ToolCallsMessage(t *testing.T) {
	t.Parallel()

	// Note: extractPromptText does not currently handle tool_calls.
	// It only extracts string content and text-type array items.
	// These tests verify the current (limited) behavior.
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "tool_calls_array",
			body: `{"messages": [{"role": "assistant", "content": null, "tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "get_weather", "arguments": "{\"location\": \"NYC\"}"}}]}]}`,
			want: "",
		},
		{
			name: "tool_result_message",
			body: `{"messages": [{"role": "tool", "tool_call_id": "call_1", "content": "Sunny, 72°F"}]}`,
			want: "Sunny, 72°F",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPromptText([]byte(tt.body))
			if got != tt.want {
				t.Errorf("extractPromptText(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

func TestExtractPromptText_SimplePromptField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "simple_prompt",
			body: `{"prompt": "Translate this text"}`,
			want: "Translate this text",
		},
		{
			name: "empty_prompt",
			body: `{"prompt": ""}`,
			want: "",
		},
		{
			name: "messages_take_precedence",
			body: `{"messages": [{"role": "user", "content": "from messages"}], "prompt": "from prompt"}`,
			want: "from messages",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPromptText([]byte(tt.body))
			if got != tt.want {
				t.Errorf("extractPromptText(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

func TestExtractPromptText_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body []byte
		want string
	}{
		{
			name: "empty_body",
			body: []byte{},
			want: "",
		},
		{
			name: "nil_body",
			body: nil,
			want: "",
		},
		{
			name: "malformed_json",
			body: []byte(`{not valid json`),
			want: "{not valid json",
		},
		{
			name: "no_messages_or_prompt",
			body: []byte(`{"model": "gpt-4"}`),
			want: "",
		},
		{
			name: "empty_messages_array",
			body: []byte(`{"messages": []}`),
			want: "",
		},
		{
			name: "messages_not_an_array",
			body: []byte(`{"messages": "not an array"}`),
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPromptText(tt.body)
			if got != tt.want {
				t.Errorf("extractPromptText(%q) = %q, want %q", string(tt.body), got, tt.want)
			}
		})
	}
}

func TestExtractCompletionTextFromChunks_StandardSSE(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "single_content_chunk",
			data: `data: {"choices":[{"delta":{"content":"Hello"}}]}`,
			want: "Hello",
		},
		{
			name: "multiple_content_chunks",
			data: `data: {"choices":[{"delta":{"content":"Hello"}}]}
data: {"choices":[{"delta":{"content":" world"}}]}
data: {"choices":[{"delta":{"content":"!"}}]}`,
			want: "Hello world!",
		},
		{
			name: "empty_content_delta",
			data: `data: {"choices":[{"delta":{"content":""}}]}
data: {"choices":[{"delta":{"content":"text"}}]}`,
			want: "text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractCompletionTextFromChunks([]byte(tt.data))
			if got != tt.want {
				t.Errorf("ExtractCompletionTextFromChunks(%q) = %q, want %q", tt.data, got, tt.want)
			}
		})
	}
}

func TestExtractCompletionTextFromChunks_DONEMarker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "done_marker_after_content",
			data: `data: {"choices":[{"delta":{"content":"Hello"}}]}
data: {"choices":[{"delta":{"content":" world"}}]}
data: [DONE]`,
			want: "Hello world",
		},
		{
			name: "done_marker_only",
			data: `data: [DONE]`,
			want: "",
		},
		{
			name: "empty_data_line_before_done",
			data: `data: {"choices":[{"delta":{"content":"text"}}]}

data: [DONE]`,
			want: "text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractCompletionTextFromChunks([]byte(tt.data))
			if got != tt.want {
				t.Errorf("ExtractCompletionTextFromChunks(%q) = %q, want %q", tt.data, got, tt.want)
			}
		})
	}
}

func TestExtractCompletionTextFromChunks_ToolCalls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "tool_call_arguments",
			// arguments contains JSON: {"location": "NYC"}
			data: "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"function\":{\"arguments\":\"{\\\"location\\\": \\\"NYC\\\"}\"}}]}}]}\n",
			want: `{"location": "NYC"}`,
		},
		{
			name: "content_and_tool_calls_mixed",
			// Each SSE event on its own line (separated by \n) for the parser
			data: "data: {\"choices\":[{\"delta\":{\"content\":\"Let me check\"}}]}\ndata: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"function\":{\"arguments\":\"{\\\"query\\\": \\\"weather\\\"}\"}}]}}]}\ndata: {\"choices\":[{\"delta\":{\"content\":\" the weather.\"}}]}\n",
			want: `Let me check{"query": "weather"} the weather.`,
		},
		{
			name: "multiple_tool_calls",
			// Each SSE event on its own line
			data: "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"function\":{\"arguments\":\"{\\\"arg1\\\": 1}\"}}]}}]}\ndata: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"function\":{\"arguments\":\"{\\\"arg2\\\": 2}\"}}]}}]}\n",
			want: `{"arg1": 1}{"arg2": 2}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractCompletionTextFromChunks([]byte(tt.data))
			if got != tt.want {
				t.Errorf("ExtractCompletionTextFromChunks(%q) = %q, want %q", tt.data, got, tt.want)
			}
		})
	}
}

func TestExtractCompletionTextFromChunks_NonStreamingResponse(t *testing.T) {
	t.Parallel()

	// Test embedded non-streaming response within SSE data lines
	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "message_content_in_sse",
			data: `data: {"choices":[{"message":{"content":"Full response text"}}]}`,
			want: "Full response text",
		},
		{
			name: "message_content_and_delta",
			data: `data: {"choices":[{"delta":{"content":"partial"}}]}
data: {"choices":[{"message":{"content":"full response"}}]}`,
			want: "partialfull response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractCompletionTextFromChunks([]byte(tt.data))
			if got != tt.want {
				t.Errorf("ExtractCompletionTextFromChunks(%q) = %q, want %q", tt.data, got, tt.want)
			}
		})
	}
}

func TestExtractCompletionTextFromChunks_AnthropicFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "content_block_delta",
			data: `data: {"type": "content_block_delta", "delta": {"text": "Hello"}}
data: {"type": "content_block_delta", "delta": {"text": " world"}}`,
			want: "Hello world",
		},
		{
			name: "unknown_type_skipped",
			data: `data: {"type": "content_block_delta", "delta": {"text": "Hello"}}
data: {"type": "unknown_event", "data": "ignored"}
data: {"type": "content_block_delta", "delta": {"text": " world"}}`,
			want: "Hello world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractCompletionTextFromChunks([]byte(tt.data))
			if got != tt.want {
				t.Errorf("ExtractCompletionTextFromChunks(%q) = %q, want %q", tt.data, got, tt.want)
			}
		})
	}
}

func TestExtractCompletionTextFromChunks_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		want string
	}{
		{
			name: "empty_data",
			data: []byte{},
			want: "",
		},
		{
			name: "nil_data",
			data: nil,
			want: "",
		},
		{
			name: "malformed_json_line",
			data: []byte(`data: {"choices":[{"delta":{"content":"valid"}}]}
data: not json at all
data: {"choices":[{"delta":{"content":"more"}}]}`),
			want: "validmore",
		},
		{
			name: "no_data_prefix",
			data: []byte(`just some text
data: {"choices":[{"delta":{"content":"Hello"}}]}`),
			want: "Hello",
		},
		{
			name: "data_prefix_without_space",
			// Code only handles "data: " (with space), not "data:" (no space)
			data: []byte(`data:{"choices":[{"delta":{"content":"Hello"}}]}`),
			want: "",
		},
		{
			name: "empty_json_object",
			data: []byte(`data: {}`),
			want: "",
		},
		{
			name: "choices_empty_array",
			data: []byte(`data: {"choices": []}`),
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractCompletionTextFromChunks(tt.data)
			if got != tt.want {
				t.Errorf("ExtractCompletionTextFromChunks(%q) = %q, want %q", string(tt.data), got, tt.want)
			}
		})
	}
}

func TestExtractCompletionTextFromJSON_NonStreaming(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "standard_response",
			body: `{"choices":[{"message":{"content":"Full response text"}}]}`,
			want: "Full response text",
		},
		{
			name: "first_choice_used",
			body: `{"choices":[{"message":{"content":"first"}}, {"message":{"content":"second"}}]}`,
			want: "first",
		},
		{
			name: "empty_content",
			body: `{"choices":[{"message":{"content":""}}]}`,
			want: "",
		},
		{
			name: "no_choices",
			body: `{"model": "gpt-4"}`,
			want: "",
		},
		{
			name: "choices_not_array",
			body: `{"choices": "not an array"}`,
			want: "",
		},
		{
			name: "empty_choices_array",
			body: `{"choices": []}`,
			want: "",
		},
		{
			name: "malformed_json",
			body: `{not valid`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractCompletionTextFromJSON([]byte(tt.body))
			if got != tt.want {
				t.Errorf("ExtractCompletionTextFromJSON(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

// ExtractCompletionText is the public wrapper. Since it is not defined in prompts.go,
// we document here that it is not a separate function — the exported functions are
// ExtractCompletionTextFromChunks and ExtractCompletionTextFromJSON.
// These tests cover the delegation behavior by testing each function individually.

// Integration-style test: simulate a full streaming round-trip
func TestExtractCompletionTextFromChunks_FullConversation(t *testing.T) {
	t.Parallel()

	// Simulate a full streaming conversation response
	data := `data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1677858242,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"I"},"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1677858242,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"'m"},"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1677858242,"model":"gpt-4","choices":[{"index":0,"delta":{"content":" a"},"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1677858242,"model":"gpt-4","choices":[{"index":0,"delta":{"content":" helpful"},"finish_reason":null}]}

data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1677858242,"model":"gpt-4","choices":[{"index":0,"delta":{"content":" assistant."},"finish_reason":"stop"}]}

data: [DONE]`

	want := "I'm a helpful assistant."
	got := ExtractCompletionTextFromChunks([]byte(data))
	if got != want {
		t.Errorf("ExtractCompletionTextFromChunks full conversation = %q, want %q", got, want)
	}
}
