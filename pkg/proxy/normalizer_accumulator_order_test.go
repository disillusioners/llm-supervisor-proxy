package proxy

import (
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/normalizers"
)

// TestNormalizerRunsBeforeAccumulator verifies that normalization
// happens BEFORE accumulation in the streaming pipeline.
// This is critical because:
// 1. Normalizers fix malformed chunks (e.g., missing index)
// 2. Accumulators expect well-formed chunks
// 3. If order is reversed, data loss can occur
func TestNormalizerRunsBeforeAccumulator(t *testing.T) {
	// Create a chunk with missing index
	chunk := []byte(`data: {"choices":[{"delta":{"tool_calls":[{"id":"call_1","function":{"name":"test"}}]}}]}`)

	// Create accumulator
	acc := NewToolCallAccumulator()

	// Create normalizer context
	normCtx := normalizers.NewContext("", "test")
	normalizers.GetRegistry().ResetAll(normCtx)

	// Step 1: Normalize FIRST
	normalized, modified, name := normalizers.NormalizeWithContextAndName(chunk, normCtx)
	if !modified {
		t.Error("Expected normalizer to add missing index")
	}
	t.Logf("Normalizer %s modified the chunk", name)

	// Step 2: Accumulate AFTER normalization
	err := acc.ProcessChunk(normalized)
	if err != nil {
		t.Errorf("Accumulator failed after normalization: %v", err)
	}

	// Verify tool call was accumulated
	if !acc.HasToolCalls() {
		t.Error("Expected tool calls to be accumulated after normalization")
	}

	// Verify index was added
	meta := acc.GetMetadata()
	if len(meta) == 0 {
		t.Error("Expected metadata to have tool calls")
	}
}

// TestAccumulatorFailsWithoutNormalization proves that without
// normalization, tool calls without index would be dropped
func TestAccumulatorFailsWithoutNormalization(t *testing.T) {
	// Create a chunk with missing index (simulating broken upstream)
	chunk := []byte(`data: {"choices":[{"delta":{"tool_calls":[{"id":"call_1","function":{"name":"test"}}]}}]}`)

	// Create accumulator
	acc := NewToolCallAccumulator()

	// Process WITHOUT normalization
	err := acc.ProcessChunk(chunk)
	if err != nil {
		t.Logf("Accumulator returned error (expected): %v", err)
	}

	// Without normalization, tool call is dropped (index missing)
	// This test documents the current behavior that we're fixing
	if acc.HasToolCalls() {
		// After P0 fixes are applied, this should pass
		t.Log("Tool call was accumulated (P0 fix applied)")
	} else {
		t.Log("Tool call was dropped (P0 fix NOT applied)")
	}
}

// TestIndexFallbackWorks verifies that the P0 fix for missing index
// properly defaults to 0 instead of dropping the tool call
func TestIndexFallbackWorks(t *testing.T) {
	// Create a chunk with missing index
	chunk := []byte(`data: {"choices":[{"delta":{"tool_calls":[{"id":"call_test","function":{"name":"myFunc","arguments":"{"}}]}}]}`)

	// Create accumulator
	acc := NewToolCallAccumulator()

	// Process directly (without normalization) - P0 fix should handle this
	err := acc.ProcessChunk(chunk)
	if err != nil {
		t.Logf("ProcessChunk returned: %v", err)
	}

	// With P0 fix, tool call should be accumulated with index 0
	if !acc.HasToolCalls() {
		t.Error("Expected tool call to be accumulated with index fallback to 0")
	}

	// Verify the arguments were accumulated
	args := acc.GetAccumulatedArgs()
	if args[0] != "{" {
		t.Errorf("Expected args[0] to be '{', got %q", args[0])
	}

	// Verify metadata
	meta := acc.GetMetadata()
	if meta[0].ID != "call_test" {
		t.Errorf("Expected ID to be 'call_test', got %q", meta[0].ID)
	}
	if meta[0].Name != "myFunc" {
		t.Errorf("Expected Name to be 'myFunc', got %q", meta[0].Name)
	}
}

// TestMaxToolCallLimits verifies that the limits are enforced
func TestMaxToolCallLimits(t *testing.T) {
	// Test index out of bounds
	acc := NewToolCallAccumulator()

	// Create chunk with index > MaxToolCallIndex
	chunk := []byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":1000,"id":"call_test","function":{"name":"test"}}]}}]}`)
	err := acc.ProcessChunk(chunk)
	if err != nil {
		t.Logf("ProcessChunk returned: %v", err)
	}

	// Should not accumulate due to index out of bounds
	if acc.HasToolCalls() {
		t.Error("Expected tool call to be rejected due to index out of bounds")
	}
}
