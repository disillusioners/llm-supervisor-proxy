package toolcall

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

// extractToolCallArgs extracts the arguments string from a tool call chunk at the given index
func extractToolCallArgs(obj map[string]interface{}, toolCallIndex int) string {
	choices, ok := obj["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return ""
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return ""
	}
	delta, ok := choice["delta"].(map[string]interface{})
	if !ok {
		return ""
	}
	toolCalls, ok := delta["tool_calls"].([]interface{})
	if !ok || len(toolCalls) <= toolCallIndex {
		return ""
	}
	tc, ok := toolCalls[toolCallIndex].(map[string]interface{})
	if !ok {
		return ""
	}
	fn, ok := tc["function"].(map[string]interface{})
	if !ok {
		return ""
	}
	args, _ := fn["arguments"].(string)
	return args
}

// TestToolCallBuffer_EmitWhenComplete tests that fragments are accumulated and emitted when complete
func TestToolCallBuffer_EmitWhenComplete(t *testing.T) {
	buffer := NewToolCallBuffer(1024*1024, "gpt-4", "test-123")

	// Fragment 1 - incomplete, should not emit
	chunk1 := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}`
	chunks := buffer.ProcessChunk([]byte(chunk1))
	if len(chunks) != 0 {
		t.Errorf("Should not emit incomplete tool call, got %d chunks", len(chunks))
	}

	// Fragment 2 - now complete, should emit
	chunk2 := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Paris\"}"}}]}}]}`
	chunks = buffer.ProcessChunk([]byte(chunk2))
	if len(chunks) != 1 {
		t.Errorf("Should emit complete tool call, got %d chunks", len(chunks))
	}

	// Verify emitted chunk has complete JSON arguments
	var obj map[string]interface{}
	data := strings.TrimPrefix(string(chunks[0]), "data: ")
	data = strings.TrimSuffix(data, "\n")
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		t.Fatalf("Invalid JSON: %v", err)
	}

	args := extractToolCallArgs(obj, 0)
	expectedArgs := `{"city":"Paris"}`
	if args != expectedArgs {
		t.Errorf("Unexpected arguments: got %q, want %q", args, expectedArgs)
	}
}

// TestToolCallBuffer_ContentPassThrough tests that non-tool-call chunks pass through immediately
func TestToolCallBuffer_ContentPassThrough(t *testing.T) {
	buffer := NewToolCallBuffer(1024*1024, "gpt-4", "test-123")

	// Content chunk should pass through immediately
	input := `data: {"choices":[{"delta":{"content":"Hello world"}}]}`
	chunks := buffer.ProcessChunk([]byte(input))
	if len(chunks) != 1 {
		t.Errorf("Content should pass through, got %d chunks", len(chunks))
	}

	// Verify the content is unchanged
	if string(chunks[0]) != input {
		t.Errorf("Content was modified: got %q, want %q", string(chunks[0]), input)
	}
}

// TestToolCallBuffer_MultipleToolCalls tests interleaved tool calls
func TestToolCallBuffer_MultipleToolCalls(t *testing.T) {
	buffer := NewToolCallBuffer(1024*1024, "gpt-4", "test-123")

	// Tool call 0, fragment 1
	tc0Frag1 := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_0","type":"function","function":{"name":"search","arguments":"{\"query\":"}}]}}]}`
	chunks := buffer.ProcessChunk([]byte(tc0Frag1))
	if len(chunks) != 0 {
		t.Errorf("Should buffer incomplete tool call 0, got %d chunks", len(chunks))
	}

	// Tool call 1, fragment 1 (interleaved)
	tc1Frag1 := `data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":"}}]}}]}`
	chunks = buffer.ProcessChunk([]byte(tc1Frag1))
	if len(chunks) != 0 {
		t.Errorf("Should buffer incomplete tool call 1, got %d chunks", len(chunks))
	}

	// Tool call 0, fragment 2 - now complete
	tc0Frag2 := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"test\"}"}}]}}]}`
	chunks = buffer.ProcessChunk([]byte(tc0Frag2))
	if len(chunks) != 1 {
		t.Errorf("Should emit complete tool call 0, got %d chunks", len(chunks))
	}

	// Verify tool call 0 was emitted with correct arguments
	var obj map[string]interface{}
	data := strings.TrimPrefix(string(chunks[0]), "data: ")
	data = strings.TrimSuffix(data, "\n")
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		t.Fatalf("Invalid JSON: %v", err)
	}
	args := extractToolCallArgs(obj, 0)
	expectedArgs := `{"query":"test"}`
	if args != expectedArgs {
		t.Errorf("Tool call 0 arguments incorrect: got %q, want %q", args, expectedArgs)
	}

	// Tool call 1, fragment 2 - now complete
	tc1Frag2 := `data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"\"file.txt\"}"}}]}}]}`
	chunks = buffer.ProcessChunk([]byte(tc1Frag2))
	if len(chunks) != 1 {
		t.Errorf("Should emit complete tool call 1, got %d chunks", len(chunks))
	}

	// Verify tool call 1 was emitted with correct arguments
	data = strings.TrimPrefix(string(chunks[0]), "data: ")
	data = strings.TrimSuffix(data, "\n")
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		t.Fatalf("Invalid JSON: %v", err)
	}
	args = extractToolCallArgs(obj, 0) // Index 0 in the emitted chunk, but it's tool call 1
	expectedArgs = `{"path":"file.txt"}`
	if args != expectedArgs {
		t.Errorf("Tool call 1 arguments incorrect: got %q, want %q", args, expectedArgs)
	}
}

// TestToolCallBuffer_Flush tests stream end behavior
func TestToolCallBuffer_Flush(t *testing.T) {
	buffer := NewToolCallBuffer(1024*1024, "gpt-4", "test-123")

	// Add incomplete fragment
	incomplete := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"test","arguments":"{incomplete"}}]}}]}`
	buffer.ProcessChunk([]byte(incomplete))

	// Flush should emit it anyway (best-effort)
	chunks := buffer.Flush()
	if len(chunks) != 1 {
		t.Errorf("Flush should emit buffered tool call, got %d chunks", len(chunks))
	}

	// Verify the emitted chunk has the accumulated arguments
	var obj map[string]interface{}
	data := strings.TrimPrefix(string(chunks[0]), "data: ")
	data = strings.TrimSuffix(data, "\n")
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		t.Fatalf("Invalid JSON: %v", err)
	}
	args := extractToolCallArgs(obj, 0)
	if args != "{incomplete" {
		t.Errorf("Flush should emit accumulated arguments, got: %s", args)
	}
}

// TestToolCallBuffer_CompleteJSONEmitsImmediately tests that complete JSON emits immediately
func TestToolCallBuffer_CompleteJSONEmitsImmediately(t *testing.T) {
	buffer := NewToolCallBuffer(1024*1024, "gpt-4", "test-123")

	// Complete JSON should emit immediately
	complete := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_0","type":"function","function":{"name":"test","arguments":"{}"}}]}}]}`
	chunks := buffer.ProcessChunk([]byte(complete))
	if len(chunks) != 1 {
		t.Errorf("Complete JSON should emit immediately, got %d chunks", len(chunks))
	}

	// Verify the emitted chunk has valid JSON
	var obj map[string]interface{}
	data := strings.TrimPrefix(string(chunks[0]), "data: ")
	data = strings.TrimSuffix(data, "\n")
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		t.Fatalf("Invalid JSON: %v", err)
	}
	extractedArgs := extractToolCallArgs(obj, 0)
	if extractedArgs != "{}" {
		t.Errorf("Expected empty JSON object, got args: %s", extractedArgs)
	}
}

// TestToolCallBuffer_IndexBoundsValidation tests negative and large indices
func TestToolCallBuffer_IndexBoundsValidation(t *testing.T) {
	buffer := NewToolCallBuffer(1024*1024, "gpt-4", "test-123")

	// Test negative index - should be ignored
	negIdx := `data: {"choices":[{"delta":{"tool_calls":[{"index":-1,"id":"call_neg","function":{"arguments":"{}"}}]}}]}`
	chunks := buffer.ProcessChunk([]byte(negIdx))
	if len(chunks) != 0 {
		t.Errorf("Negative index should be ignored, got %d chunks", len(chunks))
	}

	// Test index > MaxToolCallIndex (99) - should be ignored
	largeIdx := `data: {"choices":[{"delta":{"tool_calls":[{"index":100,"id":"call_100","function":{"arguments":"{}"}}]}}]}`
	chunks = buffer.ProcessChunk([]byte(largeIdx))
	if len(chunks) != 0 {
		t.Errorf("Index > 99 should be ignored, got %d chunks", len(chunks))
	}

	// Test valid index 0 - should be processed
	validIdx := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_0","type":"function","function":{"name":"test","arguments":"{}"}}]}}]}`
	chunks = buffer.ProcessChunk([]byte(validIdx))
	if len(chunks) != 1 {
		t.Errorf("Valid index 0 should emit complete JSON, got %d chunks", len(chunks))
	}
}

// TestToolCallBuffer_MaxToolCallsLimit tests the 100 tool call limit
func TestToolCallBuffer_MaxToolCallsLimit(t *testing.T) {
	buffer := NewToolCallBuffer(1024*1024, "gpt-4", "test-123")

	// Add 100 complete tool calls - each should emit immediately since they have valid JSON
	emittedCount := 0
	for i := 0; i < 100; i++ {
		tc := fmt.Sprintf(`data: {"choices":[{"delta":{"tool_calls":[{"index":%d,"id":"call_%d","type":"function","function":{"name":"test","arguments":"{}"}}]}}]}`, i, i)
		chunks := buffer.ProcessChunk([]byte(tc))
		emittedCount += len(chunks)
	}

	// All 100 should have been emitted immediately (complete JSON)
	if emittedCount != 100 {
		t.Errorf("Should have emitted 100 complete tool calls immediately, got %d", emittedCount)
	}

	// Flush should return 0 since all were already emitted
	chunks := buffer.Flush()
	if len(chunks) != 0 {
		t.Errorf("Flush should return 0 since all were emitted, got %d", len(chunks))
	}

	// Now test that 101st tool call at a new index is ignored when buffer is full
	buffer = NewToolCallBuffer(1024*1024, "gpt-4", "test-123")
	// First fill up 100 slots with incomplete tool calls (they won't emit because incomplete)
	for i := 0; i < 100; i++ {
		tc := fmt.Sprintf(`data: {"choices":[{"delta":{"tool_calls":[{"index":%d,"id":"call_%d","type":"function","function":{"name":"test","arguments":"{incomplete"}}]}}]}`, i, i)
		buffer.ProcessChunk([]byte(tc))
	}

	// Now try to add another tool call at a new index - should be ignored
	// (100 slots already used, index 100 would be 101st)
	tc101 := `data: {"choices":[{"delta":{"tool_calls":[{"index":100,"id":"call_100","function":{"arguments":"{}"}}]}}]}`
	chunks = buffer.ProcessChunk([]byte(tc101))
	if len(chunks) != 0 {
		t.Errorf("Tool call beyond max should be ignored, got %d chunks", len(chunks))
	}

	// Verify index 100 is not in the buffer by checking flush only has 100
	chunks = buffer.Flush()
	if len(chunks) != 100 {
		t.Errorf("Flush should have exactly 100 incomplete tool calls, got %d", len(chunks))
	}
}

// TestToolCallBuffer_SSEOutputFormat tests trailing newline verification
func TestToolCallBuffer_SSEOutputFormat(t *testing.T) {
	buffer := NewToolCallBuffer(1024*1024, "gpt-4", "test-123")

	input := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_0","type":"function","function":{"name":"test","arguments":"{}"}}]}}]}`
	chunks := buffer.ProcessChunk([]byte(input))

	if len(chunks) != 1 {
		t.Fatalf("Expected 1 chunk, got %d", len(chunks))
	}

	// Verify trailing newline
	output := string(chunks[0])
	if !strings.HasSuffix(output, "\n") {
		t.Errorf("SSE output must end with newline, got: %q", output)
	}

	// Verify data: prefix
	if !strings.HasPrefix(output, "data: ") {
		t.Errorf("SSE output must start with 'data: ', got: %q", output)
	}
}

// TestToolCallBuffer_EmptyAndDoneMarkers tests pass-through of empty lines and [DONE]
func TestToolCallBuffer_EmptyAndDoneMarkers(t *testing.T) {
	buffer := NewToolCallBuffer(1024*1024, "gpt-4", "test-123")

	// Empty line should pass through
	chunks := buffer.ProcessChunk([]byte(""))
	if len(chunks) != 1 || string(chunks[0]) != "" {
		t.Errorf("Empty line should pass through unchanged")
	}

	// [DONE] marker should pass through
	chunks = buffer.ProcessChunk([]byte("data: [DONE]"))
	if len(chunks) != 1 || string(chunks[0]) != "data: [DONE]" {
		t.Errorf("[DONE] marker should pass through unchanged")
	}

	// [DONE] without prefix should also pass through
	chunks = buffer.ProcessChunk([]byte("[DONE]"))
	if len(chunks) != 1 || string(chunks[0]) != "[DONE]" {
		t.Errorf("[DONE] without prefix should pass through unchanged")
	}
}

// TestToolCallBuffer_InvalidJSONPassThrough tests that invalid JSON passes through
func TestToolCallBuffer_InvalidJSONPassThrough(t *testing.T) {
	buffer := NewToolCallBuffer(1024*1024, "gpt-4", "test-123")

	// Invalid JSON should pass through unchanged
	input := "data: not valid json at all"
	chunks := buffer.ProcessChunk([]byte(input))
	if len(chunks) != 1 {
		t.Errorf("Invalid JSON should pass through as single chunk, got %d chunks", len(chunks))
	}
	if string(chunks[0]) != input {
		t.Errorf("Invalid JSON should pass through unchanged, got: %s", string(chunks[0]))
	}
}

// TestToolCallBuffer_NoChoicesPassThrough tests chunks without choices array
func TestToolCallBuffer_NoChoicesPassThrough(t *testing.T) {
	buffer := NewToolCallBuffer(1024*1024, "gpt-4", "test-123")

	// Chunk without choices should pass through
	input := `data: {"id":"123","object":"chat.completion.chunk"}`
	chunks := buffer.ProcessChunk([]byte(input))
	if len(chunks) != 1 {
		t.Errorf("Chunk without choices should pass through, got %d chunks", len(chunks))
	}
	if string(chunks[0]) != input {
		t.Errorf("Chunk without choices should pass through unchanged")
	}
}

// TestToolCallBuffer_NoToolCallsPassThrough tests chunks without tool_calls
func TestToolCallBuffer_NoToolCallsPassThrough(t *testing.T) {
	buffer := NewToolCallBuffer(1024*1024, "gpt-4", "test-123")

	// Chunk with choices but no tool_calls should pass through
	input := `data: {"choices":[{"delta":{"content":"hello"}}]}`
	chunks := buffer.ProcessChunk([]byte(input))
	if len(chunks) != 1 {
		t.Errorf("Chunk without tool_calls should pass through, got %d chunks", len(chunks))
	}
	if string(chunks[0]) != input {
		t.Errorf("Chunk without tool_calls should pass through unchanged")
	}
}

// TestToolCallBuffer_MetadataAccumulation tests that ID, type, and name are accumulated
func TestToolCallBuffer_MetadataAccumulation(t *testing.T) {
	buffer := NewToolCallBuffer(1024*1024, "gpt-4", "test-123")

	// First chunk with ID and type
	frag1 := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_abc123","type":"function","function":{"name":"search","arguments":"{"}}]}}]}`
	buffer.ProcessChunk([]byte(frag1))

	// Second chunk with arguments only (completes the JSON)
	frag2 := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]}}]}`
	chunks := buffer.ProcessChunk([]byte(frag2))

	if len(chunks) != 1 {
		t.Fatalf("Expected 1 chunk, got %d", len(chunks))
	}

	// Verify metadata was preserved
	var obj map[string]interface{}
	data := strings.TrimPrefix(string(chunks[0]), "data: ")
	data = strings.TrimSuffix(data, "\n")
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		t.Fatalf("Invalid JSON: %v", err)
	}

	choices := obj["choices"].([]interface{})
	choice := choices[0].(map[string]interface{})
	delta := choice["delta"].(map[string]interface{})
	toolCalls := delta["tool_calls"].([]interface{})
	tc := toolCalls[0].(map[string]interface{})

	if tc["id"] != "call_abc123" {
		t.Errorf("Expected ID 'call_abc123', got: %v", tc["id"])
	}
	if tc["type"] != "function" {
		t.Errorf("Expected type 'function', got: %v", tc["type"])
	}

	fn := tc["function"].(map[string]interface{})
	if fn["name"] != "search" {
		t.Errorf("Expected name 'search', got: %v", fn["name"])
	}
}

// TestToolCallBuffer_FlushOnlyUnemitted tests that Flush only emits unemitted tool calls
func TestToolCallBuffer_FlushOnlyUnemitted(t *testing.T) {
	buffer := NewToolCallBuffer(1024*1024, "gpt-4", "test-123")

	// Add and complete tool call 0
	tc0 := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_0","type":"function","function":{"name":"test","arguments":"{}"}}]}}]}`
	buffer.ProcessChunk([]byte(tc0))

	// Add incomplete tool call 1
	tc1 := `data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_1","type":"function","function":{"name":"test","arguments":"{incomplete"}}]}}]}`
	buffer.ProcessChunk([]byte(tc1))

	// Flush should only emit tool call 1 (tool call 0 was already emitted)
	chunks := buffer.Flush()
	if len(chunks) != 1 {
		t.Errorf("Flush should only emit unemitted tool calls, got %d chunks", len(chunks))
	}
}

// TestToolCallBuffer_DefaultMaxSize tests that default max size is applied
func TestToolCallBuffer_DefaultMaxSize(t *testing.T) {
	// Create buffer with maxSize = 0 (should default to 1MB)
	buffer := NewToolCallBuffer(0, "gpt-4", "test-123")
	if buffer.maxSize != 1024*1024 {
		t.Errorf("Expected default maxSize 1MB, got: %d", buffer.maxSize)
	}

	// Create buffer with negative maxSize (should default to 1MB)
	buffer = NewToolCallBuffer(-1, "gpt-4", "test-123")
	if buffer.maxSize != 1024*1024 {
		t.Errorf("Expected default maxSize 1MB for negative input, got: %d", buffer.maxSize)
	}
}

// TestToolCallBuffer_BufferOverflow tests buffer overflow behavior
// When the buffer exceeds maxSize, it should emit what it has and reset
func TestToolCallBuffer_BufferOverflow(t *testing.T) {
	// Create buffer with very small limit (15 bytes)
	buffer := NewToolCallBuffer(15, "gpt-4", "test-123")

	// Add first fragment - incomplete JSON that fits within limit
	// {"city":" is 10 bytes
	chunks := buffer.ProcessChunk([]byte(
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\""}}]}}]}`,
	))
	// Should not emit (incomplete JSON)
	if len(chunks) != 0 {
		t.Errorf("Should not emit incomplete, got %d chunks", len(chunks))
	}

	// Add second fragment - triggers overflow (10 + 12 = 22 bytes > 15 limit)
	// Paris"} is 8 bytes but total would exceed limit
	chunks = buffer.ProcessChunk([]byte(
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"Paris\"}"}}]}}]}`,
	))
	// Should emit on overflow (the first fragment that was accumulated)
	if len(chunks) != 1 {
		t.Errorf("Should emit on overflow, got %d chunks", len(chunks))
	}

	// Verify the emitted chunk contains the first fragment's arguments
	var obj map[string]interface{}
	data := strings.TrimPrefix(string(chunks[0]), "data: ")
	data = strings.TrimSuffix(data, "\n")
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		t.Fatalf("Invalid JSON: %v", err)
	}
	args := extractToolCallArgs(obj, 0)
	if args != `{"city":"` {
		t.Errorf("Expected first fragment args '{\"city\":\"', got: %s", args)
	}

	// The second fragment should have been written to a fresh builder
	// Flush to see what remains
	chunks = buffer.Flush()
	if len(chunks) != 1 {
		t.Errorf("Flush should emit remaining buffer, got %d chunks", len(chunks))
	}
	// The remaining buffer should have "Paris"}"
	data = strings.TrimPrefix(string(chunks[0]), "data: ")
	data = strings.TrimSuffix(data, "\n")
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		t.Fatalf("Invalid JSON: %v", err)
	}
	args = extractToolCallArgs(obj, 0)
	if args != `Paris"}` {
		t.Errorf("Expected remaining args 'Paris\"}', got: %s", args)
	}
}

// TestToolCallBuffer_BufferOverflowWithCompleteJSON tests that complete JSON emits immediately
// even when approaching buffer limits
func TestToolCallBuffer_BufferOverflowWithCompleteJSON(t *testing.T) {
	// Create buffer with small limit
	buffer := NewToolCallBuffer(20, "gpt-4", "test-123")

	// Add a complete JSON that fits
	chunks := buffer.ProcessChunk([]byte(
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"test","arguments":"{\"a\":1}"}}]}}]}`,
	))
	// Should emit immediately (complete JSON)
	if len(chunks) != 1 {
		t.Errorf("Complete JSON should emit immediately, got %d chunks", len(chunks))
	}

	// Add another complete JSON for DIFFERENT index - should also emit
	chunks = buffer.ProcessChunk([]byte(
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"test","arguments":"{\"b\":2}"}}]}}]}`,
	))
	// Should emit immediately (complete JSON)
	if len(chunks) != 1 {
		t.Errorf("Second complete JSON should emit immediately, got %d chunks", len(chunks))
	}
}

// === REPAIR INTEGRATION TESTS ===

// TestToolCallBufferWithRepair_ValidJSON tests that valid JSON passes through unchanged
func TestToolCallBufferWithRepair_ValidJSON(t *testing.T) {
	config := &toolrepair.Config{
		Enabled:    true,
		Strategies: []string{"library_repair"},
	}
	buffer := NewToolCallBufferWithRepair(1024*1024, "gpt-4", "test", config)

	// Complete valid JSON should emit immediately and unchanged
	input := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_0","type":"function","function":{"name":"test","arguments":"{\"key\":\"value\"}"}}]}}]}`
	chunks := buffer.ProcessChunk([]byte(input))

	if len(chunks) != 1 {
		t.Fatalf("Expected 1 chunk, got %d", len(chunks))
	}

	// Verify arguments are unchanged
	var obj map[string]interface{}
	data := strings.TrimPrefix(string(chunks[0]), "data: ")
	data = strings.TrimSuffix(data, "\n")
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		t.Fatalf("Invalid JSON: %v", err)
	}

	args := extractToolCallArgs(obj, 0)
	expectedArgs := `{"key":"value"}`
	if args != expectedArgs {
		t.Errorf("Valid JSON should be unchanged: got %q, want %q", args, expectedArgs)
	}

	// Verify no repairs were attempted
	stats := buffer.GetRepairStats()
	if stats.Attempted != 0 {
		t.Errorf("Should not attempt repair for valid JSON, got %d attempts", stats.Attempted)
	}
}

// TestToolCallBufferWithRepair_MalformedJSON tests repair of malformed JSON
func TestToolCallBufferWithRepair_MalformedJSON(t *testing.T) {
	config := &toolrepair.Config{
		Enabled:    true,
		Strategies: []string{"library_repair"},
	}
	buffer := NewToolCallBufferWithRepair(1024*1024, "gpt-4", "test", config)

	// Send malformed JSON in two fragments
	// Fragment 1: {key: (missing quotes)
	frag1 := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_0","type":"function","function":{"name":"test","arguments":"{key: "}}]}}]}`
	chunks := buffer.ProcessChunk([]byte(frag1))
	if len(chunks) != 0 {
		t.Errorf("Should buffer incomplete fragment, got %d chunks", len(chunks))
	}

	// Fragment 2: completes the JSON but still malformed (not valid JSON)
	// The buffer won't emit automatically because it's not valid JSON
	frag2 := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"value}"}}]}}]}`
	chunks = buffer.ProcessChunk([]byte(frag2))
	// Not emitted yet because {key: value} is not valid JSON
	if len(chunks) != 0 {
		t.Logf("Warning: expected 0 chunks (malformed JSON not complete), got %d", len(chunks))
	}

	// Flush to force emission of buffered tool calls (this triggers repair)
	chunks = buffer.Flush()
	if len(chunks) != 1 {
		t.Fatalf("Expected 1 chunk after flush, got %d", len(chunks))
	}

	// Verify the arguments were repaired
	var obj map[string]interface{}
	data := strings.TrimPrefix(string(chunks[0]), "data: ")
	data = strings.TrimSuffix(data, "\n")
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		t.Fatalf("Invalid JSON in output: %v", err)
	}

	args := extractToolCallArgs(obj, 0)
	// The repaired JSON should be valid
	var parsedArgs map[string]interface{}
	if err := json.Unmarshal([]byte(args), &parsedArgs); err != nil {
		t.Errorf("Repaired arguments should be valid JSON: %v (args: %s)", err, args)
	}

	// Verify repair was attempted
	stats := buffer.GetRepairStats()
	if stats.Attempted != 1 {
		t.Errorf("Should have attempted 1 repair, got %d", stats.Attempted)
	}
	if stats.Successful != 1 {
		t.Errorf("Should have 1 successful repair, got %d", stats.Successful)
	}
}

// TestToolCallBufferWithRepair_Disabled tests that disabled repair doesn't affect output
func TestToolCallBufferWithRepair_Disabled(t *testing.T) {
	config := &toolrepair.Config{
		Enabled: false,
	}
	buffer := NewToolCallBufferWithRepair(1024*1024, "gpt-4", "test", config)

	// Send malformed JSON
	frag1 := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{key: "}}]}}]}`
	buffer.ProcessChunk([]byte(frag1))

	frag2 := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"value}"}}]}}]}`
	buffer.ProcessChunk([]byte(frag2))

	// Flush to force emission (malformed JSON won't auto-emit)
	chunks := buffer.Flush()
	if len(chunks) != 1 {
		t.Fatalf("Expected 1 chunk after flush, got %d", len(chunks))
	}

	// Verify the arguments are unchanged (malformed)
	var obj map[string]interface{}
	data := strings.TrimPrefix(string(chunks[0]), "data: ")
	data = strings.TrimSuffix(data, "\n")
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		t.Fatalf("Invalid JSON in output: %v", err)
	}

	args := extractToolCallArgs(obj, 0)
	expectedArgs := "{key: value}" // Original malformed JSON
	if args != expectedArgs {
		t.Errorf("Disabled repair should not change args: got %q, want %q", args, expectedArgs)
	}

	// Verify no repairs were attempted
	stats := buffer.GetRepairStats()
	if stats.Attempted != 0 {
		t.Errorf("Should not attempt repair when disabled, got %d attempts", stats.Attempted)
	}
}

// TestToolCallBufferWithRepair_NilConfig tests behavior with nil config
func TestToolCallBufferWithRepair_NilConfig(t *testing.T) {
	buffer := NewToolCallBufferWithRepair(1024*1024, "gpt-4", "test", nil)

	// Should behave like a buffer without repair
	if buffer.HasRepairer() {
		t.Error("Should not have repairer with nil config")
	}

	// Send valid JSON
	input := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]}}]}`
	chunks := buffer.ProcessChunk([]byte(input))

	if len(chunks) != 1 {
		t.Fatalf("Expected 1 chunk, got %d", len(chunks))
	}
}

// TestToolCallBufferWithRepair_TrailingComma tests repair of trailing comma
func TestToolCallBufferWithRepair_TrailingComma(t *testing.T) {
	config := &toolrepair.Config{
		Enabled:    true,
		Strategies: []string{"library_repair"},
	}
	buffer := NewToolCallBufferWithRepair(1024*1024, "gpt-4", "test", config)

	// JSON with trailing comma (malformed)
	frag1 := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"key\":\"value\", "}}]}}]}`
	buffer.ProcessChunk([]byte(frag1))

	frag2 := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]}}]}`
	buffer.ProcessChunk([]byte(frag2))

	// Flush to force emission (malformed JSON won't auto-emit)
	chunks := buffer.Flush()
	if len(chunks) != 1 {
		t.Fatalf("Expected 1 chunk after flush, got %d", len(chunks))
	}

	// Verify the arguments were repaired
	var obj map[string]interface{}
	data := strings.TrimPrefix(string(chunks[0]), "data: ")
	data = strings.TrimSuffix(data, "\n")
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		t.Fatalf("Invalid JSON in output: %v", err)
	}

	args := extractToolCallArgs(obj, 0)
	// The repaired JSON should be valid
	var parsedArgs map[string]interface{}
	if err := json.Unmarshal([]byte(args), &parsedArgs); err != nil {
		t.Errorf("Repaired arguments should be valid JSON: %v (args: %s)", err, args)
	}

	// Verify repair stats
	stats := buffer.GetRepairStats()
	if stats.Attempted != 1 || stats.Successful != 1 {
		t.Errorf("Expected 1 successful repair, got attempted=%d, successful=%d", stats.Attempted, stats.Successful)
	}
}

// TestToolCallBufferWithRepair_MultipleToolCalls tests repair of multiple tool calls
func TestToolCallBufferWithRepair_MultipleToolCalls(t *testing.T) {
	config := &toolrepair.Config{
		Enabled:    true,
		Strategies: []string{"library_repair"},
	}
	buffer := NewToolCallBufferWithRepair(1024*1024, "gpt-4", "test", config)

	// Tool call 0: valid JSON
	tc0 := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_0","function":{"name":"valid","arguments":"{\"a\":1}"}}]}}]}`
	chunks := buffer.ProcessChunk([]byte(tc0))
	if len(chunks) != 1 {
		t.Errorf("Tool call 0 should emit immediately, got %d chunks", len(chunks))
	}

	// Tool call 1: malformed JSON
	tc1Frag1 := `data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_1","function":{"name":"invalid","arguments":"{key: "}}]}}]}`
	buffer.ProcessChunk([]byte(tc1Frag1))

	tc1Frag2 := `data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"value}"}}]}}]}`
	buffer.ProcessChunk([]byte(tc1Frag2))
	// Tool call 1 won't auto-emit because it's malformed

	// Flush to force emission
	chunks = buffer.Flush()
	if len(chunks) != 1 {
		t.Errorf("Tool call 1 should emit after flush, got %d chunks", len(chunks))
	}

	// Verify repair stats: 1 attempted (tool call 1), 0 for tool call 0 (valid)
	stats := buffer.GetRepairStats()
	if stats.Attempted != 1 {
		t.Errorf("Expected 1 repair attempt, got %d", stats.Attempted)
	}
}

// TestToolCallBuffer_GetRepairStats tests repair stats tracking
func TestToolCallBuffer_GetRepairStats(t *testing.T) {
	config := &toolrepair.Config{
		Enabled:    true,
		Strategies: []string{"library_repair"},
	}
	buffer := NewToolCallBufferWithRepair(1024*1024, "gpt-4", "test", config)

	// Initial stats should be zero
	stats := buffer.GetRepairStats()
	if stats.Attempted != 0 || stats.Successful != 0 || stats.Failed != 0 {
		t.Errorf("Initial stats should be zero: %+v", stats)
	}

	// Process a malformed tool call
	frag1 := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{bad: "}}]}}]}`
	buffer.ProcessChunk([]byte(frag1))

	frag2 := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"json}"}}]}}]}`
	buffer.ProcessChunk([]byte(frag2))

	// Flush to force emission and repair
	buffer.Flush()

	// Stats should reflect the repair
	stats = buffer.GetRepairStats()
	if stats.Attempted != 1 {
		t.Errorf("Expected 1 attempt, got %d", stats.Attempted)
	}
	// Either successful or failed
	if stats.Successful+stats.Failed != 1 {
		t.Errorf("Expected successful+failed = 1, got successful=%d, failed=%d", stats.Successful, stats.Failed)
	}
}
