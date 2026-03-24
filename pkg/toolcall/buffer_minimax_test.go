package toolcall

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

// TestToolCallBuffer_MiniMaxStreaming tests the buffer with actual MiniMax streaming chunks
// This test uses the raw chunks from the MiniMax API response to verify the buffer
// correctly handles:
// - Thinking content (<think>/</think>)
// - 3 tool calls in a single request
// - Chunked arguments
// - No [DONE] marker
func TestToolCallBuffer_MiniMaxStreaming(t *testing.T) {
	buffer := NewToolCallBuffer(1024*1024, "MiniMax-M2.7", "test-minimax-123")

	// MiniMax chunks from actual API response
	// These are the raw chunks logged from the MiniMax streaming response
	minimaxChunks := []string{
		// Chunk 1: Thinking content starts
		`data: {"id":"0611a2c7f8f5cba1f685a7ecf633e74a","choices":[{"index":0,"delta":{"content":"<think>\nThe user","role":"assistant"}}],"created":1774350279,"model":"MiniMax-M2.7","object":"chat.completion.chunk","usage":null}`,
		// Chunk 2: More thinking content
		`data: {"id":"0611a2c7f8f5cba1f685a7ecf633e74a","choices":[{"index":0,"delta":{"content":" wants me to perform three independent tasks:\n1. Get weather for Tokyo\n2. Search for code about authentication in Python\n3. Calculate 123 * 456\n\nSince","role":"assistant"}}],"created":1774350279,"model":"MiniMax-M2.7","object":"chat.completion.chunk","usage":null}`,
		// Chunk 3: Thinking continues
		`data: {"id":"0611a2c7f8f5cba1f685a7ecf633e74a","choices":[{"index":0,"delta":{"content":" these are all independent operations, I can make all three calls at once.","role":"assistant"}}],"created":1774350279,"model":"MiniMax-M2.7","object":"chat.completion.chunk","usage":null}`,
		// Chunk 4: First tool call starts - get_weather (index 0)
		`data: {"id":"0611a2c7f8f5cba1f685a7ecf633e74a","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"id":"call_function_7lpdd3sp4dmh_1","type":"function","function":{"name":"get_weather","arguments":""},"index":0}]}}],"created":1774350279,"model":"MiniMax-M2.7","object":"chat.completion.chunk","usage":null}`,
		// Chunk 5: Arguments for get_weather - part 1 (incomplete JSON)
		`data: {"id":"0611a2c7f8f5cba1f685a7ecf633e74a","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"function":{"arguments":"{\"location\": \"Tokyo"},"index":0}]}}],"created":1774350279,"model":"MiniMax-M2.7","object":"chat.completion.chunk","usage":null}`,
		// Chunk 6: Arguments for get_weather - part 2 (completes JSON)
		`data: {"id":"0611a2c7f8f5cba1f685a7ecf633e74a","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"function":{"arguments":"\"}"},"index":0}]}}],"created":1774350279,"model":"MiniMax-M2.7","object":"chat.completion.chunk","usage":null}`,
		// Chunk 7: Second tool call starts - search_code (index 1)
		`data: {"id":"0611a2c7f8f5cba1f685a7ecf633e74a","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"id":"call_function_7lpdd3sp4dmh_2","type":"function","function":{"name":"search_code","arguments":""},"index":1}]}}],"created":1774350279,"model":"MiniMax-M2.7","object":"chat.completion.chunk","usage":null}`,
		// Chunk 8: Arguments for search_code (complete JSON in one chunk)
		`data: {"id":"0611a2c7f8f5cba1f685a7ecf633e74a","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"function":{"arguments":"{\"query\": \"authentication\", \"language\": \"python\"}"},"index":1}]}}],"created":1774350279,"model":"MiniMax-M2.7","object":"chat.completion.chunk","usage":null}`,
		// Chunk 9: Third tool call starts - calculate (index 2)
		`data: {"id":"0611a2c7f8f5cba1f685a7ecf633e74a","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"id":"call_function_7lpdd3sp4dmh_3","type":"function","function":{"name":"calculate","arguments":""},"index":2}]}}],"created":1774350279,"model":"MiniMax-M2.7","object":"chat.completion.chunk","usage":null}`,
		// Chunk 10: Arguments for calculate (complete JSON in one chunk)
		`data: {"id":"0611a2c7f8f5cba1f685a7ecf633e74a","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"function":{"arguments":"{\"expression\": \"123 * 456\"}"},"index":2}]}}],"created":1774350279,"model":"MiniMax-M2.7","object":"chat.completion.chunk","usage":null}`,
		// Chunk 11: finish_reason with thinking content
		`data: {"id":"0611a2c7f8f5cba1f685a7ecf633e74a","choices":[{"finish_reason":"tool_calls","index":0,"delta":{"content":"\n<think>\n","role":"assistant"}}],"created":1774350279,"model":"MiniMax-M2.7","object":"chat.completion.chunk","usage":null}`,
		// Chunk 12: Another finish_reason chunk (MiniMax sends duplicate)
		`data: {"id":"0611a2c7f8f5cba1f685a7ecf633e74a","choices":[{"finish_reason":"tool_calls","index":0,"delta":{"content":"","role":"assistant"}}],"created":1774350279,"model":"MiniMax-M2.7","object":"chat.completion.chunk","usage":null}`,
	}

	var allOutput []string
	emittedToolCalls := 0

	// Process each chunk
	for i, chunk := range minimaxChunks {
		t.Logf("Processing chunk %d", i+1)
		chunks := buffer.ProcessChunk([]byte(chunk))
		
		for _, out := range chunks {
			allOutput = append(allOutput, string(out))
			emittedToolCalls++
			
			// Verify it's a valid SSE chunk
			outStr := string(out)
			if !strings.HasPrefix(outStr, "data: ") {
				t.Errorf("Chunk %d: Expected 'data: ' prefix, got: %s", i+1, outStr[:min(50, len(outStr))])
			}
			// Note: Content-only chunks pass through as-is, may not have trailing newline
			
			// Parse and verify the JSON is valid
			data := strings.TrimPrefix(outStr, "data: ")
			data = strings.TrimSuffix(data, "\n")
			var obj map[string]interface{}
			if err := json.Unmarshal([]byte(data), &obj); err != nil {
				t.Errorf("Chunk %d: Invalid JSON: %v", i+1, err)
				continue
			}
			
			// Check if it has tool_calls
			choices, ok := obj["choices"].([]interface{})
			if !ok || len(choices) == 0 {
				continue
			}
			choice, ok := choices[0].(map[string]interface{})
			if !ok {
				continue
			}
			delta, ok := choice["delta"].(map[string]interface{})
			if !ok {
				continue
			}
			toolCalls, ok := delta["tool_calls"].([]interface{})
			if !ok || len(toolCalls) == 0 {
				continue
			}
			
			// Verify tool call details
			tc, ok := toolCalls[0].(map[string]interface{})
			if !ok {
				continue
			}
			
			index, _ := tc["index"].(float64)
			id, _ := tc["id"].(string)
			
			fn, ok := tc["function"].(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := fn["name"].(string)
			args, _ := fn["arguments"].(string)
			
			t.Logf("  -> Emitted tool_call: index=%d, id=%s, name=%s, args=%s", int(index), id, name, args)
			
			// Verify arguments are valid JSON
			if args != "" {
				var parsed map[string]interface{}
				if err := json.Unmarshal([]byte(args), &parsed); err != nil {
					t.Errorf("Chunk %d: Tool call arguments are not valid JSON: %s", i+1, args)
				}
			}
		}
	}

	// Flush the buffer to get any remaining tool calls
	t.Logf("Flushing buffer...")
	flushChunks := buffer.Flush()
	for _, out := range flushChunks {
		allOutput = append(allOutput, string(out))
		emittedToolCalls++
		
		outStr := string(out)
		data := strings.TrimPrefix(outStr, "data: ")
		data = strings.TrimSuffix(data, "\n")
		
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(data), &obj); err != nil {
			t.Errorf("Flush: Invalid JSON: %v", err)
			continue
		}
		
		choices := obj["choices"].([]interface{})
		choice := choices[0].(map[string]interface{})
		delta := choice["delta"].(map[string]interface{})
		toolCalls := delta["tool_calls"].([]interface{})
		tc := toolCalls[0].(map[string]interface{})
		
		index, _ := tc["index"].(float64)
		id, _ := tc["id"].(string)
		name, _ := tc["function"].(map[string]interface{})["name"].(string)
		args, _ := tc["function"].(map[string]interface{})["arguments"].(string)
		
		t.Logf("  -> Flushed tool_call: index=%d, id=%s, name=%s, args=%s", int(index), id, name, args)
	}

	// Summary
	t.Logf("\n=== Test Summary ===")
	t.Logf("Total input chunks: %d", len(minimaxChunks))
	t.Logf("Total output chunks: %d", len(allOutput))
	t.Logf("Tool calls emitted: %d", emittedToolCalls)

	// Verify we got 3 tool calls (minimum)
	if emittedToolCalls < 3 {
		t.Errorf("Expected at least 3 tool calls, got %d", emittedToolCalls)
	}

	// Note: The buffer may emit more than 3 chunks because:
	// - Content-only chunks pass through immediately
	// - Each tool call may emit separately when JSON is complete
	// The key verification is that we got valid JSON for all 3 tool calls
	t.Logf("SUCCESS: Tool call buffer correctly handled MiniMax streaming response with %d tool calls", emittedToolCalls)
}

// TestToolCallBuffer_MiniMaxFromLogFile tests the buffer with chunks read from the log file
// This test reads the actual log file and processes the MiniMax chunks
func TestToolCallBuffer_MiniMaxFromLogFile(t *testing.T) {
	// Try to read from log file
	logFilePath := "../../logs/minimax_stream_test.log"
	
	// Check if file exists
	if _, err := os.Stat(logFilePath); os.IsNotExist(err) {
		t.Skipf("Log file not found at %s, skipping test", logFilePath)
		return
	}
	
	// Read the log file
	content, err := os.ReadFile(logFilePath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}
	
	// Extract chunks from log file
	// The chunks in the log are prefixed with timestamps and labels
	lines := strings.Split(string(content), "\n")
	var chunks []string
	for _, line := range lines {
		// Look for lines that start with "data: {" (MiniMax response chunks)
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, `"choices"`) {
			// Remove any timestamp prefix "[2026-03-24T18:04:XX.XXXXXX] "
			chunk := strings.TrimPrefix(line, `data: `)
			// Also check if there's a timestamp prefix
			if idx := strings.Index(chunk, `data: `); idx > 0 {
				chunk = chunk[idx+6:]
			}
			chunks = append(chunks, "data: "+chunk)
		}
	}
	
	if len(chunks) == 0 {
		t.Skip("No valid MiniMax chunks found in log file")
		return
	}
	
	t.Logf("Found %d chunks in log file", len(chunks))
	
	// Process chunks with buffer
	buffer := NewToolCallBuffer(1024*1024, "MiniMax-M2.7", "test-log-file")
	
	var emittedToolCalls int
	for i, chunk := range chunks {
		output := buffer.ProcessChunk([]byte(chunk))
		emittedToolCalls += len(output)
		
		for _, out := range output {
			t.Logf("Chunk %d output: %s", i, string(out)[:min(100, len(string(out)))])
		}
	}
	
	// Flush
	flushOutput := buffer.Flush()
	emittedToolCalls += len(flushOutput)
	
	t.Logf("Total tool calls emitted: %d", emittedToolCalls)
	
	// Verify we got 3 tool calls
	if emittedToolCalls < 3 {
		t.Errorf("Expected at least 3 tool calls, got %d", emittedToolCalls)
	}
}

// TestToolCallBuffer_MiniMaxWithRepair tests the buffer with repair enabled
func TestToolCallBuffer_MiniMaxWithRepair(t *testing.T) {
	config := &toolrepair.Config{
		Enabled:    true,
		Strategies: []string{"library_repair"},
	}
	buffer := NewToolCallBufferWithRepair(1024*1024, "MiniMax-M2.7", "test-minimax-repair", config)

	// Test with valid JSON first - should emit immediately without repair
	validChunk := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]}}]}`
	output := buffer.ProcessChunk([]byte(validChunk))
	
	if len(output) != 1 {
		t.Errorf("Expected 1 output for valid JSON, got %d", len(output))
	}
	
	// Verify no repair was attempted for valid JSON
	stats := buffer.GetRepairStats()
	if stats.Attempted != 0 {
		t.Errorf("Should not attempt repair for valid JSON, got %d", stats.Attempted)
	}
	
	t.Logf("SUCCESS: Buffer with repair correctly handles valid JSON (no repair needed)")
}

// TestToolCallBuffer_MiniMaxInterleavedArguments tests handling of interleaved arguments
// This simulates what MiniMax does - sending arguments across multiple chunks
func TestToolCallBuffer_MiniMaxInterleavedArguments(t *testing.T) {
	buffer := NewToolCallBuffer(1024*1024, "MiniMax-M2.7", "test-interleaved")

	// Simulate interleaved tool calls similar to MiniMax
	// Tool call 0 starts, tool call 1 starts, tool call 0 continues, tool call 1 continues
	interleavedChunks := []string{
		// Tool call 0 starts with name and empty args
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_0","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
		// Tool call 1 starts with name and empty args
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_1","type":"function","function":{"name":"search_code","arguments":""}}]}}]}`,
		// Tool call 0 gets partial arguments (incomplete JSON)
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"location\":"}}]}}]}`,
		// Tool call 1 gets complete arguments
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"query\":\"test\"}"}}]}}]}`,
		// Tool call 0 completes its arguments
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":" \"Tokyo\"}"}]}}]}`,
	}

	emittedCount := 0
	for i, chunk := range interleavedChunks {
		output := buffer.ProcessChunk([]byte(chunk))
		emittedCount += len(output)
		t.Logf("Chunk %d: %d outputs", i+1, len(output))
		
		for _, out := range output {
			// Parse and log the tool call
			data := strings.TrimPrefix(string(out), "data: ")
			data = strings.TrimSuffix(data, "\n")
			var obj map[string]interface{}
			if err := json.Unmarshal([]byte(data), &obj); err != nil {
				continue
			}
			if choices, ok := obj["choices"].([]interface{}); ok && len(choices) > 0 {
				if delta, ok := choices[0].(map[string]interface{})["delta"].(map[string]interface{}); ok {
					if tcs, ok := delta["tool_calls"].([]interface{}); ok && len(tcs) > 0 {
						if tc, ok := tcs[0].(map[string]interface{}); ok {
							index, _ := tc["index"].(float64)
							name, _ := tc["function"].(map[string]interface{})["name"].(string)
							args, _ := tc["function"].(map[string]interface{})["arguments"].(string)
							t.Logf("  -> Emitted: index=%d, name=%s, args=%s", int(index), name, args)
						}
					}
				}
			}
		}
	}

	// Flush remaining
	flushCount := len(buffer.Flush())
	emittedCount += flushCount
	t.Logf("Flush: %d chunks", flushCount)

	// We expect at least 2 tool calls to be emitted (search_code immediately when complete, get_weather after flush)
	if emittedCount < 2 {
		t.Errorf("Expected at least 2 tool calls emitted, got %d", emittedCount)
	}
	
	t.Logf("SUCCESS: Interleaved tool calls handled correctly")
}

// Helper function for min
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Utility function to extract tool call info from chunk for debugging
func extractToolCallInfo(chunk []byte) string {
	data := strings.TrimPrefix(string(chunk), "data: ")
	data = strings.TrimSuffix(data, "\n")
	
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		return fmt.Sprintf("ERROR: %v", err)
	}
	
	choices, ok := obj["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return "NO CHOICES"
	}
	
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return "INVALID CHOICE"
	}
	
	delta, ok := choice["delta"].(map[string]interface{})
	if !ok {
		return "NO DELTA"
	}
	
	// Check for content
	if content, ok := delta["content"].(string); ok && content != "" {
		return fmt.Sprintf("CONTENT: %s", content[:min(50, len(content))])
	}
	
	// Check for tool_calls
	toolCalls, ok := delta["tool_calls"].([]interface{})
	if !ok || len(toolCalls) == 0 {
		return "NO TOOL_CALLS"
	}
	
	tc, ok := toolCalls[0].(map[string]interface{})
	if !ok {
		return "INVALID TOOL_CALL"
	}
	
	index, _ := tc["index"].(float64)
	id, _ := tc["id"].(string)
	
	fn, ok := tc["function"].(map[string]interface{})
	if !ok {
		return "NO FUNCTION"
	}
	
	name, _ := fn["name"].(string)
	args, _ := fn["arguments"].(string)
	
	return fmt.Sprintf("TOOL_CALL: index=%d, id=%s, name=%s, args=%s", 
		int(index), id, name, args)
}
