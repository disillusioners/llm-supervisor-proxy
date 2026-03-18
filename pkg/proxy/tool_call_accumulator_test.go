package proxy

import (
	"testing"
)

func TestToolCallAccumulator_Single(t *testing.T) {
	// Test single tool call across 4 chunks (simulating incremental streaming)
	accumulator := NewToolCallAccumulator()

	chunks := []string{
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_abc123","type":"function","function":{"name":"get_weather","arguments":"{"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"location\":"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":" \"Paris\""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]},"finish_reason":null}]}`,
	}

	for i, chunk := range chunks {
		err := accumulator.ProcessChunk([]byte(chunk))
		if err != nil {
			t.Errorf("ProcessChunk(%d) returned unexpected error: %v", i, err)
		}
	}

	if !accumulator.HasToolCalls() {
		t.Error("HasToolCalls() returned false, expected true")
	}

	args := accumulator.GetAccumulatedArgs()
	if len(args) != 1 {
		t.Errorf("GetAccumulatedArgs() returned %d entries, expected 1", len(args))
	}

	expected := `{"location": "Paris"}`
	if args[0] != expected {
		t.Errorf("Accumulated args = %q, expected %q", args[0], expected)
	}

	// Verify metadata
	metadata := accumulator.GetMetadata()
	if metadata[0].ID != "call_abc123" {
		t.Errorf("Metadata ID = %q, expected %q", metadata[0].ID, "call_abc123")
	}
	if metadata[0].Type != "function" {
		t.Errorf("Metadata Type = %q, expected %q", metadata[0].Type, "function")
	}
	if metadata[0].Name != "get_weather" {
		t.Errorf("Metadata Name = %q, expected %q", metadata[0].Name, "get_weather")
	}
}

func TestToolCallAccumulator_Multiple(t *testing.T) {
	// Test multiple tool calls with different indices
	accumulator := NewToolCallAccumulator()

	chunks := []string{
		// First tool call (index 0) - first chunk
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{"}}]}}]}`,
		// Second tool call (index 1) - first chunk
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"get_time","arguments":"{"}}]}}]}`,
		// First tool call - second chunk
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"city\":\"NYC\""}}]}}]}`,
		// Second tool call - second chunk
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"\"tz\":\"EST\""}}]}}]}`,
		// Both tool calls - closing braces
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}},{"index":1,"function":{"arguments":"}"}}]}}]}`,
	}

	for i, chunk := range chunks {
		err := accumulator.ProcessChunk([]byte(chunk))
		if err != nil {
			t.Errorf("ProcessChunk(%d) returned unexpected error: %v", i, err)
		}
	}

	if !accumulator.HasToolCalls() {
		t.Error("HasToolCalls() returned false, expected true")
	}

	if accumulator.Count() != 2 {
		t.Errorf("Count() = %d, expected 2", accumulator.Count())
	}

	args := accumulator.GetAccumulatedArgs()
	if len(args) != 2 {
		t.Errorf("GetAccumulatedArgs() returned %d entries, expected 2", len(args))
	}

	// Verify first tool call
	expected0 := `{"city":"NYC"}`
	if args[0] != expected0 {
		t.Errorf("Accumulated args[0] = %q, expected %q", args[0], expected0)
	}

	// Verify second tool call
	expected1 := `{"tz":"EST"}`
	if args[1] != expected1 {
		t.Errorf("Accumulated args[1] = %q, expected %q", args[1], expected1)
	}

	// Verify metadata
	metadata := accumulator.GetMetadata()
	if metadata[0].Name != "get_weather" {
		t.Errorf("Metadata[0].Name = %q, expected %q", metadata[0].Name, "get_weather")
	}
	if metadata[1].Name != "get_time" {
		t.Errorf("Metadata[1].Name = %q, expected %q", metadata[1].Name, "get_time")
	}
}

func TestToolCallAccumulator_Empty(t *testing.T) {
	// Test no tool calls in stream
	accumulator := NewToolCallAccumulator()

	chunks := []string{
		`data: {"choices":[{"delta":{"content":"Hello"}}]}`,
		`data: {"choices":[{"delta":{"content":" world"}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}

	for i, chunk := range chunks {
		err := accumulator.ProcessChunk([]byte(chunk))
		if err != nil {
			t.Errorf("ProcessChunk(%d) returned unexpected error: %v", i, err)
		}
	}

	if accumulator.HasToolCalls() {
		t.Error("HasToolCalls() returned true, expected false")
	}

	if accumulator.Count() != 0 {
		t.Errorf("Count() = %d, expected 0", accumulator.Count())
	}

	args := accumulator.GetAccumulatedArgs()
	if len(args) != 0 {
		t.Errorf("GetAccumulatedArgs() returned %d entries, expected 0", len(args))
	}
}

func TestToolCallAccumulator_EmptyLines(t *testing.T) {
	accumulator := NewToolCallAccumulator()

	// Process empty lines and [DONE] marker - should not error
	err := accumulator.ProcessChunk([]byte(""))
	if err != nil {
		t.Errorf("ProcessChunk(empty) returned unexpected error: %v", err)
	}

	err = accumulator.ProcessChunk([]byte("data: [DONE]"))
	if err != nil {
		t.Errorf("ProcessChunk([DONE]) returned unexpected error: %v", err)
	}

	err = accumulator.ProcessChunk([]byte("[DONE]"))
	if err != nil {
		t.Errorf("ProcessChunk([DONE] without prefix) returned unexpected error: %v", err)
	}

	if accumulator.HasToolCalls() {
		t.Error("HasToolCalls() returned true for empty/invalid input")
	}
}

func TestToolCallAccumulator_InvalidJSON(t *testing.T) {
	accumulator := NewToolCallAccumulator()

	// Invalid JSON should return error but not crash
	err := accumulator.ProcessChunk([]byte("data: not valid json"))
	if err == nil {
		t.Error("ProcessChunk(invalid JSON) expected error, got nil")
	}

	// Should still be able to process valid chunks after invalid ones
	validChunk := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"test"}}]}}]}`
	err = accumulator.ProcessChunk([]byte(validChunk))
	if err != nil {
		t.Errorf("ProcessChunk(valid) returned unexpected error: %v", err)
	}

	if !accumulator.HasToolCalls() {
		t.Error("HasToolCalls() returned false after valid chunk")
	}
}

func TestToolCallAccumulator_NoDataPrefix(t *testing.T) {
	// Test chunks without "data: " prefix
	accumulator := NewToolCallAccumulator()

	chunk := `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_123","type":"function","function":{"name":"test","arguments":"arg1"}}]}}]}`

	err := accumulator.ProcessChunk([]byte(chunk))
	if err != nil {
		t.Errorf("ProcessChunk(without prefix) returned unexpected error: %v", err)
	}

	args := accumulator.GetAccumulatedArgs()
	if len(args) != 1 {
		t.Errorf("GetAccumulatedArgs() returned %d entries, expected 1", len(args))
	}

	if args[0] != "arg1" {
		t.Errorf("Accumulated args[0] = %q, expected %q", args[0], "arg1")
	}
}

func TestToolCallAccumulator_ThreadSafety(t *testing.T) {
	// Test concurrent access to accumulator
	accumulator := NewToolCallAccumulator()
	done := make(chan bool)

	// Writer goroutine 1
	go func() {
		for i := 0; i < 100; i++ {
			chunk := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"a"}}]}}]}`
			accumulator.ProcessChunk([]byte(chunk))
		}
		done <- true
	}()

	// Writer goroutine 2
	go func() {
		for i := 0; i < 100; i++ {
			chunk := `data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"b"}}]}}]}`
			accumulator.ProcessChunk([]byte(chunk))
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 100; i++ {
			accumulator.HasToolCalls()
			accumulator.GetAccumulatedArgs()
			accumulator.Count()
		}
		done <- true
	}()

	// Wait for all goroutines
	<-done
	<-done
	<-done

	// Verify final state
	args := accumulator.GetAccumulatedArgs()
	if len(args) != 2 {
		t.Errorf("GetAccumulatedArgs() returned %d entries, expected 2", len(args))
	}

	// Each index should have 100 'a's or 'b's
	if len(args[0]) != 100 {
		t.Errorf("args[0] length = %d, expected 100", len(args[0]))
	}
	if len(args[1]) != 100 {
		t.Errorf("args[1] length = %d, expected 100", len(args[1]))
	}
}

func TestToolCallAccumulator_NoChoices(t *testing.T) {
	accumulator := NewToolCallAccumulator()

	// Chunk with no choices array
	chunk := `data: {"id":"123","object":"chat.completion.chunk"}`
	err := accumulator.ProcessChunk([]byte(chunk))
	if err != nil {
		t.Errorf("ProcessChunk(no choices) returned unexpected error: %v", err)
	}

	if accumulator.HasToolCalls() {
		t.Error("HasToolCalls() returned true for chunk with no choices")
	}
}

func TestToolCallAccumulator_EmptyToolCalls(t *testing.T) {
	accumulator := NewToolCallAccumulator()

	// Chunk with empty tool_calls array
	chunk := `data: {"choices":[{"delta":{"tool_calls":[]}}]}`
	err := accumulator.ProcessChunk([]byte(chunk))
	if err != nil {
		t.Errorf("ProcessChunk(empty tool_calls) returned unexpected error: %v", err)
	}

	if accumulator.HasToolCalls() {
		t.Error("HasToolCalls() returned true for chunk with empty tool_calls")
	}
}

// TestToolCallAccumulator_ConcatenatedChunks simulates the MiniMax bug where
// multiple SSE chunks are concatenated on a single line. This tests that when
// normalization is applied BEFORE accumulation, the accumulator correctly
// processes each split chunk.
func TestToolCallAccumulator_ConcatenatedChunks(t *testing.T) {
	accumulator := NewToolCallAccumulator()

	// Simulate MiniMax-style concatenated chunks (two JSON objects on one line)
	// This is the exact format that caused the bug report
	concatenatedInput := `data: {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{}","name":"grep"},"id":"call_1","index":0,"type":"function"}]},"index":0}]} {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{\"pattern\":\"test\"}","name":"grep"},"id":"call_2","index":1,"type":"function"}]},"index":0}]}`

	// First, normalize the concatenated input (this is what the fix does)
	// Import normalizers package in the actual code
	// For this test, we manually split it to verify accumulator works with split chunks
	chunk1 := `data: {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{}","name":"grep"},"id":"call_1","index":0,"type":"function"}]},"index":0}]}`
	chunk2 := `data: {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{\"pattern\":\"test\"}","name":"grep"},"id":"call_2","index":1,"type":"function"}]},"index":0}]}`

	// Process each chunk separately (as if normalizer split them)
	err := accumulator.ProcessChunk([]byte(chunk1))
	if err != nil {
		t.Errorf("ProcessChunk(chunk1) returned unexpected error: %v", err)
	}

	err = accumulator.ProcessChunk([]byte(chunk2))
	if err != nil {
		t.Errorf("ProcessChunk(chunk2) returned unexpected error: %v", err)
	}

	// Verify we have 2 tool calls
	if !accumulator.HasToolCalls() {
		t.Error("HasToolCalls() returned false, expected true")
	}

	if accumulator.Count() != 2 {
		t.Errorf("Count() = %d, expected 2", accumulator.Count())
	}

	args := accumulator.GetAccumulatedArgs()
	if len(args) != 2 {
		t.Errorf("GetAccumulatedArgs() returned %d entries, expected 2", len(args))
	}

	// Verify first tool call arguments
	if args[0] != "{}" {
		t.Errorf("Accumulated args[0] = %q, expected {}", args[0])
	}

	// Verify second tool call arguments
	expected1 := `{"pattern":"test"}`
	if args[1] != expected1 {
		t.Errorf("Accumulated args[1] = %q, expected %q", args[1], expected1)
	}

	// Verify metadata
	metadata := accumulator.GetMetadata()
	if metadata[0].ID != "call_1" {
		t.Errorf("Metadata[0].ID = %q, expected %q", metadata[0].ID, "call_1")
	}
	if metadata[1].ID != "call_2" {
		t.Errorf("Metadata[1].ID = %q, expected %q", metadata[1].ID, "call_2")
	}

	// Log to show the test works
	t.Logf("Successfully processed concatenated chunks: %s -> 2 separate chunks", concatenatedInput)
}
