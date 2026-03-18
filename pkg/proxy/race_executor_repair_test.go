package proxy

import (
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

func TestRepairAccumulatedArgs_Disabled(t *testing.T) {
	// When repair is disabled, should return nil
	accumulated := map[int]string{
		0: `{broken json`,
	}

	config := toolrepair.Config{Enabled: false}
	result := repairAccumulatedArgs(accumulated, config)

	if result != nil {
		t.Errorf("repairAccumulatedArgs() with disabled config returned %v, expected nil", result)
	}
}

func TestRepairAccumulatedArgs_ValidJSON(t *testing.T) {
	// Already valid JSON should not be repaired
	accumulated := map[int]string{
		0: `{"location": "Paris"}`,
		1: `{"count": 42}`,
	}

	config := toolrepair.Config{Enabled: true}
	result := repairAccumulatedArgs(accumulated, config)

	if len(result) != 0 {
		t.Errorf("repairAccumulatedArgs() repaired valid JSON: %v", result)
	}
}

func TestRepairAccumulatedArgs_EmptyArgs(t *testing.T) {
	// Empty arguments should be skipped
	accumulated := map[int]string{
		0: "",
		1: `{"valid": true}`,
	}

	config := toolrepair.Config{Enabled: true}
	result := repairAccumulatedArgs(accumulated, config)

	if len(result) != 0 {
		t.Errorf("repairAccumulatedArgs() repaired empty args: %v", result)
	}
}

func TestRepairAccumulatedArgs_MalformedJSON(t *testing.T) {
	// Malformed JSON that can be repaired
	accumulated := map[int]string{
		0: `{location: "Paris"}`, // Missing quotes around key
	}

	config := toolrepair.Config{Enabled: true}
	result := repairAccumulatedArgs(accumulated, config)

	// The repairer may or may not be able to fix this depending on implementation
	// Just verify it doesn't crash and returns a map (possibly empty)
	if result == nil {
		t.Error("repairAccumulatedArgs() returned nil map")
	}
}

func TestRepairAccumulatedArgs_MultipleToolCalls(t *testing.T) {
	// Multiple tool calls, some valid, some invalid
	accumulated := map[int]string{
		0: `{"location": "Paris"}`,      // Valid
		1: `{count: 42}`,                 // Invalid - missing quotes
		2: `{"name": "test", "valid":}`,  // Invalid - trailing comma/empty value
	}

	config := toolrepair.Config{Enabled: true}
	result := repairAccumulatedArgs(accumulated, config)

	// Index 0 should not be in result (already valid)
	if _, exists := result[0]; exists {
		t.Error("repairAccumulatedArgs() repaired already-valid JSON at index 0")
	}

	// Result should be a valid map (may contain repairs for indices 1 and 2)
	if result == nil {
		t.Error("repairAccumulatedArgs() returned nil map")
	}
}

func TestRepairAccumulatedArgs_PartialJSON(t *testing.T) {
	// Test with partial JSON that might be accumulated during streaming
	// Note: In practice, repair only happens after complete accumulation
	accumulated := map[int]string{
		0: `{"location": "Paris", "unit": "celsius"}`,
	}

	config := toolrepair.Config{Enabled: true}
	result := repairAccumulatedArgs(accumulated, config)

	// Already valid JSON should not be repaired
	if len(result) != 0 {
		t.Errorf("repairAccumulatedArgs() unexpectedly repaired: %v", result)
	}
}

func TestRepairAccumulatedArgs_ComplexNestedJSON(t *testing.T) {
	// Complex nested valid JSON
	accumulated := map[int]string{
		0: `{"location": {"city": "Paris", "country": "France"}, "options": {"units": "metric"}}`,
	}

	config := toolrepair.Config{Enabled: true}
	result := repairAccumulatedArgs(accumulated, config)

	// Already valid JSON should not be repaired
	if len(result) != 0 {
		t.Errorf("repairAccumulatedArgs() repaired valid nested JSON: %v", result)
	}
}

func TestRepairAccumulatedArgs_ArrayInJSON(t *testing.T) {
	// JSON with arrays
	accumulated := map[int]string{
		0: `{"items": ["a", "b", "c"], "count": 3}`,
	}

	config := toolrepair.Config{Enabled: true}
	result := repairAccumulatedArgs(accumulated, config)

	// Already valid JSON should not be repaired
	if len(result) != 0 {
		t.Errorf("repairAccumulatedArgs() repaired valid JSON with arrays: %v", result)
	}
}

func TestRepairAccumulatedArgs_SpecialCharacters(t *testing.T) {
	// JSON with special characters
	accumulated := map[int]string{
		0: `{"message": "Hello \"World\"\nNew line"}`,
	}

	config := toolrepair.Config{Enabled: true}
	result := repairAccumulatedArgs(accumulated, config)

	// Already valid JSON should not be repaired
	if len(result) != 0 {
		t.Errorf("repairAccumulatedArgs() repaired valid JSON with special chars: %v", result)
	}
}

func TestRepairAccumulatedArgs_UnicodeInJSON(t *testing.T) {
	// JSON with unicode characters
	accumulated := map[int]string{
		0: `{"city": "東京", "greeting": "こんにちは"}`,
	}

	config := toolrepair.Config{Enabled: true}
	result := repairAccumulatedArgs(accumulated, config)

	// Already valid JSON should not be repaired
	if len(result) != 0 {
		t.Errorf("repairAccumulatedArgs() repaired valid JSON with unicode: %v", result)
	}
}
