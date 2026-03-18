package proxy

import (
	"encoding/json"
	"testing"
)

func TestHasToolCalls(t *testing.T) {
	tests := []struct {
		name     string
		chunk    []byte
		expected bool
	}{
		{
			name:     "chunk with tool_calls",
			chunk:    []byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0}]}}]}`),
			expected: true,
		},
		{
			name:     "chunk without tool_calls",
			chunk:    []byte(`data: {"choices":[{"delta":{"content":"Hello"}}]}`),
			expected: false,
		},
		{
			name:     "empty chunk",
			chunk:    []byte{},
			expected: false,
		},
		{
			name:     "chunk with tool_calls in string value (edge case)",
			chunk:    []byte(`data: {"choices":[{"delta":{"content":"use tool_calls function"}}]}`),
			expected: true, // String check will match, but JSON parsing later will handle correctly
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasToolCalls(tt.chunk)
			if result != tt.expected {
				t.Errorf("hasToolCalls(%q) = %v, expected %v", string(tt.chunk), result, tt.expected)
			}
		})
	}
}

func TestRepairChunkArgs(t *testing.T) {
	tests := []struct {
		name         string
		chunk        []byte
		repairedArgs map[int]string
		expectChange bool
	}{
		{
			name:         "no repair needed - no tool_calls",
			chunk:        []byte(`data: {"choices":[{"delta":{"content":"Hello"}}]}`),
			repairedArgs: map[int]string{0: "fixed_value"},
			expectChange: false,
		},
		{
			name:         "repair tool_call index 0",
			chunk:        []byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"broken"}}]}}]}`),
			repairedArgs: map[int]string{0: "fixed_value"},
			expectChange: true,
		},
		{
			name:         "no repair - index not in repairedArgs",
			chunk:        []byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"original"}}]}}]}`),
			repairedArgs: map[int]string{0: "fixed_value"},
			expectChange: false,
		},
		{
			name:         "no repair - same value",
			chunk:        []byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"same"}}]}}]}`),
			repairedArgs: map[int]string{0: "same"},
			expectChange: false,
		},
		{
			name:         "repair without data prefix",
			chunk:        []byte(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"old"}}]}}]}`),
			repairedArgs: map[int]string{0: "new_value"},
			expectChange: true,
		},
		{
			name:         "repair multiple tool_calls in same chunk",
			chunk:        []byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"a"}},{"index":1,"function":{"arguments":"b"}}]}}]}`),
			repairedArgs: map[int]string{0: "fixed_a", 1: "fixed_b"},
			expectChange: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := repairChunkArgs(tt.chunk, tt.repairedArgs)

			if tt.expectChange {
				// Verify the chunk was modified
				if string(result) == string(tt.chunk) {
					t.Errorf("repairChunkArgs() expected change but chunk unchanged")
				}

				// Parse result and verify the repaired arguments are present
				var obj map[string]interface{}
				// Strip "data: " prefix if present
				resultStr := string(result)
				if len(resultStr) > 6 && resultStr[:6] == "data: " {
					resultStr = resultStr[6:]
				}
				if err := json.Unmarshal([]byte(resultStr), &obj); err != nil {
					t.Errorf("Result is not valid JSON: %v", err)
					return
				}

				// Navigate to tool_calls and verify arguments
				choices := obj["choices"].([]interface{})
				delta := choices[0].(map[string]interface{})["delta"].(map[string]interface{})
				toolCalls := delta["tool_calls"].([]interface{})

				for _, tc := range toolCalls {
					tcMap := tc.(map[string]interface{})
					idx := int(tcMap["index"].(float64))
					if repaired, has := tt.repairedArgs[idx]; has {
						fn := tcMap["function"].(map[string]interface{})
						args := fn["arguments"].(string)
						if args != repaired {
							t.Errorf("repairChunkArgs() args[%d] = %q, expected %q", idx, args, repaired)
						}
					}
				}
			}
		})
	}
}

func TestRepairChunkArgs_InvalidJSON(t *testing.T) {
	// Invalid JSON should return original chunk
	chunk := []byte("not valid json")
	repairedArgs := map[int]string{0: "fixed"}

	result := repairChunkArgs(chunk, repairedArgs)

	if string(result) != string(chunk) {
		t.Errorf("repairChunkArgs(invalid JSON) should return original, got %q", string(result))
	}
}

func TestRepairChunkArgs_PreservesPrefix(t *testing.T) {
	// Test that "data: " prefix is preserved
	chunk := []byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"old"}}]}}]}`)
	repairedArgs := map[int]string{0: "new"}

	result := repairChunkArgs(chunk, repairedArgs)

	// Check that "data: " prefix is present
	if len(result) < 6 || string(result[:6]) != "data: " {
		t.Errorf("repairChunkArgs() lost 'data: ' prefix, got: %q", string(result))
	}
}

func TestRewriteBufferWithRepairedArgs(t *testing.T) {
	// Create a buffer with mixed chunks
	oldBuffer := newStreamBuffer(1024 * 1024)

	// Add chunks
	chunks := [][]byte{
		[]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n"),
		[]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"broken\"}}]}}]}\n"),
		[]byte("data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n"),
		[]byte("data: [DONE]\n"),
	}

	for _, chunk := range chunks {
		if !oldBuffer.Add(chunk) {
			t.Fatal("Failed to add chunk to buffer")
		}
	}

	// Repair args for index 0
	repairedArgs := map[int]string{
		0: `{"location": "Paris"}`,
	}

	// Rewrite buffer
	newBuffer := rewriteBufferWithRepairedArgs(oldBuffer, repairedArgs)

	if newBuffer == nil {
		t.Fatal("rewriteBufferWithRepairedArgs() returned nil")
	}

	// Get chunks from new buffer
	newChunks, _ := newBuffer.GetChunksFrom(0)

	if len(newChunks) != 4 {
		t.Errorf("rewriteBufferWithRepairedArgs() returned %d chunks, expected 4", len(newChunks))
	}

	// Verify the tool_call chunk was modified by parsing it
	toolCallChunk := string(newChunks[1])
	// Strip "data: " prefix
	if len(toolCallChunk) > 6 && toolCallChunk[:6] == "data: " {
		toolCallChunk = toolCallChunk[6:]
	}

	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(toolCallChunk), &obj); err != nil {
		t.Fatalf("Tool call chunk is not valid JSON: %v", err)
	}

	// Navigate to arguments
	choices := obj["choices"].([]interface{})
	delta := choices[0].(map[string]interface{})["delta"].(map[string]interface{})
	toolCalls := delta["tool_calls"].([]interface{})
	fn := toolCalls[0].(map[string]interface{})["function"].(map[string]interface{})
	args := fn["arguments"].(string)

	if args != `{"location": "Paris"}` {
		t.Errorf("Tool call arguments = %q, expected %q", args, `{"location": "Paris"}`)
	}

	// Verify non-tool_call chunks still contain expected content
	contentChunk1 := string(newChunks[0])
	if !containsString([]byte(contentChunk1), "Hello") {
		t.Errorf("First content chunk was modified unexpectedly: %q", contentChunk1)
	}

	contentChunk2 := string(newChunks[2])
	if !containsString([]byte(contentChunk2), "world") {
		t.Errorf("Second content chunk was modified unexpectedly: %q", contentChunk2)
	}
}

func TestRewriteBufferWithRepairedArgs_NoToolCalls(t *testing.T) {
	// Create a buffer with no tool calls
	oldBuffer := newStreamBuffer(1024 * 1024)

	chunks := [][]byte{
		[]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n"),
		[]byte("data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n"),
		[]byte("data: [DONE]\n"),
	}

	for _, chunk := range chunks {
		if !oldBuffer.Add(chunk) {
			t.Fatal("Failed to add chunk to buffer")
		}
	}

	repairedArgs := map[int]string{0: "fixed"}

	newBuffer := rewriteBufferWithRepairedArgs(oldBuffer, repairedArgs)

	newChunks, _ := newBuffer.GetChunksFrom(0)

	// Verify content is preserved
	if len(newChunks) != 3 {
		t.Errorf("Expected 3 chunks, got %d", len(newChunks))
	}

	// Verify content chunks contain expected text
	if !containsString(newChunks[0], "Hello") {
		t.Errorf("First chunk lost content")
	}
	if !containsString(newChunks[1], "world") {
		t.Errorf("Second chunk lost content")
	}
}

func TestRewriteBufferWithRepairedArgs_EmptyRepairedArgs(t *testing.T) {
	// Create a buffer with tool calls
	oldBuffer := newStreamBuffer(1024 * 1024)

	chunks := [][]byte{
		[]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"original\"}}]}}]}\n"),
	}

	for _, chunk := range chunks {
		if !oldBuffer.Add(chunk) {
			t.Fatal("Failed to add chunk to buffer")
		}
	}

	// Empty repaired args - nothing should change
	repairedArgs := map[int]string{}

	newBuffer := rewriteBufferWithRepairedArgs(oldBuffer, repairedArgs)

	newChunks, _ := newBuffer.GetChunksFrom(0)

	// Chunk should be unchanged since no repairs were specified
	if len(newChunks) != 1 {
		t.Errorf("Expected 1 chunk, got %d", len(newChunks))
	}
}

func TestAddPrefix(t *testing.T) {
	tests := []struct {
		name     string
		chunk    []byte
		prefix   []byte
		expected string
	}{
		{
			name:     "with prefix",
			chunk:    []byte(`{"key":"value"}`),
			prefix:   []byte("data: "),
			expected: "data: {\"key\":\"value\"}",
		},
		{
			name:     "no prefix",
			chunk:    []byte(`{"key":"value"}`),
			prefix:   nil,
			expected: "{\"key\":\"value\"}",
		},
		{
			name:     "empty prefix",
			chunk:    []byte(`{"key":"value"}`),
			prefix:   []byte{},
			expected: "{\"key\":\"value\"}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := addPrefix(tt.chunk, tt.prefix)
			if string(result) != tt.expected {
				t.Errorf("addPrefix() = %q, expected %q", string(result), tt.expected)
			}
		})
	}
}

func TestRewriteBuffer_PreservesFormat(t *testing.T) {
	// Verify that the output format matches expected SSE format
	oldBuffer := newStreamBuffer(1024 * 1024)

	// Add a tool call chunk with specific formatting
	originalChunk := []byte("data: {\"id\":\"chatcmpl-123\",\"object\":\"chat.completion.chunk\",\"created\":1234567890,\"model\":\"gpt-4\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"get_weather\",\"arguments\":\"{city: NYC}\"}}]},\"finish_reason\":null}]}\n")

	if !oldBuffer.Add(originalChunk) {
		t.Fatal("Failed to add chunk to buffer")
	}

	repairedArgs := map[int]string{
		0: `{"city": "NYC"}`,
	}

	newBuffer := rewriteBufferWithRepairedArgs(oldBuffer, repairedArgs)
	newChunks, _ := newBuffer.GetChunksFrom(0)

	if len(newChunks) != 1 {
		t.Fatalf("Expected 1 chunk, got %d", len(newChunks))
	}

	result := string(newChunks[0])

	// Verify SSE format is preserved
	if len(result) < 6 || result[:6] != "data: " {
		t.Errorf("SSE format not preserved - missing 'data: ' prefix: %q", result)
	}

	// Parse and verify the repaired arguments are present
	var obj map[string]interface{}
	resultJSON := result[6:] // Strip "data: " prefix
	if err := json.Unmarshal([]byte(resultJSON), &obj); err != nil {
		t.Fatalf("Result is not valid JSON: %v", err)
	}

	// Navigate to arguments
	choices := obj["choices"].([]interface{})
	delta := choices[0].(map[string]interface{})["delta"].(map[string]interface{})
	toolCalls := delta["tool_calls"].([]interface{})
	fn := toolCalls[0].(map[string]interface{})["function"].(map[string]interface{})
	args := fn["arguments"].(string)

	if args != `{"city": "NYC"}` {
		t.Errorf("Repaired arguments = %q, expected %q", args, `{"city": "NYC"}`)
	}

	// Verify other fields are preserved
	if obj["id"] != "chatcmpl-123" {
		t.Errorf("ID field not preserved: %v", obj["id"])
	}
	if fn["name"] != "get_weather" {
		t.Errorf("Function name not preserved: %v", fn["name"])
	}
}

// Helper function
func containsString(data []byte, substr string) bool {
	return len(data) >= len(substr) && containsSubstring(string(data), substr)
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
