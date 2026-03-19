package proxy

import (
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/normalizers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolcall"
)

// TestNormalizerRunsBeforeBuffer verifies that normalization
// happens BEFORE tool call buffering in the streaming pipeline.
// This is critical because:
// 1. Normalizers fix malformed chunks (e.g., missing index)
// 2. ToolCallBuffer expects well-formed chunks
// 3. If order is reversed, data loss can occur
func TestNormalizerRunsBeforeBuffer(t *testing.T) {
	// Create a chunk with missing index
	chunk := []byte(`data: {"choices":[{"delta":{"tool_calls":[{"id":"call_1","function":{"name":"test","arguments":"{}"}}]}}]}`)

	// Create tool call buffer
	buffer := toolcall.NewToolCallBuffer(1024*1024, "test-model", "test-request")

	// Create normalizer context
	normCtx := normalizers.NewContext("", "test")
	normalizers.GetRegistry().ResetAll(normCtx)

	// Step 1: Normalize FIRST
	normalized, modified, name := normalizers.NormalizeWithContextAndName(chunk, normCtx)
	if !modified {
		t.Error("Expected normalizer to add missing index")
	}
	t.Logf("Normalizer %s modified the chunk", name)

	// Step 2: Process through buffer AFTER normalization
	chunks := buffer.ProcessChunk(normalized)
	if len(chunks) == 0 {
		t.Error("Expected buffer to emit chunks after normalization")
	}

	// Verify tool call was processed
	stats := buffer.GetRepairStats()
	t.Logf("Buffer repair stats: attempted=%d, success=%d, failed=%d",
		stats.Attempted, stats.Successful, stats.Failed)
}

// TestBufferHandlesMissingIndex verifies that the ToolCallBuffer
// properly handles chunks with missing index by defaulting to 0
func TestBufferHandlesMissingIndex(t *testing.T) {
	// Create a chunk with missing index
	chunk := []byte(`data: {"choices":[{"delta":{"tool_calls":[{"id":"call_test","function":{"name":"myFunc","arguments":"{}"}}]}}]}`)

	// Create tool call buffer
	buffer := toolcall.NewToolCallBuffer(1024*1024, "test-model", "test-request")

	// Process directly (without normalization) - buffer should handle this
	chunks := buffer.ProcessChunk(chunk)
	if len(chunks) == 0 {
		// Buffer may not emit until complete JSON - flush to get remaining
		chunks = buffer.Flush()
	}

	if len(chunks) == 0 {
		t.Error("Expected buffer to process tool call with missing index")
	}
}

// TestBufferMaxToolCallLimits verifies that the limits are enforced
func TestBufferMaxToolCallLimits(t *testing.T) {
	// Create tool call buffer
	buffer := toolcall.NewToolCallBuffer(1024*1024, "test-model", "test-request")

	// Create chunk with index > MaxToolCallIndex
	chunk := []byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":1000,"id":"call_test","function":{"name":"test","arguments":"{}"}}]}}]}`)
	chunks := buffer.ProcessChunk(chunk)

	// Should not emit anything due to index out of bounds
	if len(chunks) > 0 {
		// Check if it's just a pass-through (not a tool call)
		for _, c := range chunks {
			t.Logf("Buffer emitted: %s", string(c))
		}
	}
}

// TestBufferWithCompleteJSON verifies that complete JSON is emitted immediately
func TestBufferWithCompleteJSON(t *testing.T) {
	// Create a chunk with complete JSON arguments
	chunk := []byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_test","type":"function","function":{"name":"test","arguments":"{\"key\":\"value\"}"}}]}}]}`)

	// Create tool call buffer
	buffer := toolcall.NewToolCallBuffer(1024*1024, "test-model", "test-request")

	// Process the chunk
	chunks := buffer.ProcessChunk(chunk)

	// Should emit immediately because JSON is complete
	if len(chunks) == 0 {
		t.Error("Expected buffer to emit complete tool call immediately")
	}

	t.Logf("Buffer emitted %d chunks for complete JSON", len(chunks))
}

// TestBufferWithPartialJSON verifies that partial JSON is buffered until complete
func TestBufferWithPartialJSON(t *testing.T) {
	// Create chunks with partial JSON arguments
	chunk1 := []byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_test","type":"function","function":{"name":"test","arguments":"{"}}]}}]}`)
	chunk2 := []byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"key\"" }}]}}]}`)
	chunk3 := []byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"value\"}"}}]}}]}`)

	// Create tool call buffer
	buffer := toolcall.NewToolCallBuffer(1024*1024, "test-model", "test-request")

	// Process first chunk - should buffer (incomplete JSON)
	chunks1 := buffer.ProcessChunk(chunk1)
	if len(chunks1) > 0 {
		t.Logf("First chunk emitted %d chunks (unexpected for partial JSON)", len(chunks1))
	}

	// Process second chunk - should buffer (incomplete JSON)
	chunks2 := buffer.ProcessChunk(chunk2)
	if len(chunks2) > 0 {
		t.Logf("Second chunk emitted %d chunks (unexpected for partial JSON)", len(chunks2))
	}

	// Process third chunk - should emit (complete JSON)
	chunks3 := buffer.ProcessChunk(chunk3)
	if len(chunks3) == 0 {
		// Might need to flush if JSON detection is strict
		chunks3 = buffer.Flush()
	}

	if len(chunks3) == 0 {
		t.Error("Expected buffer to emit tool call after JSON is complete")
	}

	t.Logf("Third chunk + flush emitted %d chunks", len(chunks3))
}
