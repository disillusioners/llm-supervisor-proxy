package normalizers

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSplitConcatenatedChunks tests the normalizer with various inputs
func TestSplitConcatenatedChunks(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantSplit bool
		wantCount int // number of expected chunks
	}{
		{
			name:      "single valid chunk",
			input:     `data: {"choices":[{"delta":{"content":"hello"},"index":0}]}`,
			wantSplit: false,
			wantCount: 1,
		},
		{
			name:      "two concatenated chunks (MiniMax bug)",
			input:     `data: {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{}","name":"grep"},"id":"call_1","index":1,"type":"function"}]},"index":0}]} {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{\"caseSensitive\":false,\"include\":\"*.go\",\"pattern\":\"test\"}","name":"grep"},"id":"call_2","index":0,"type":"function"}]},"index":0}]}`,
			wantSplit: true,
			wantCount: 2,
		},
		{
			name:      "empty line",
			input:     "",
			wantSplit: false,
			wantCount: 0,
		},
		{
			name:      "done marker",
			input:     "data: [DONE]",
			wantSplit: false,
			wantCount: 1,
		},
		{
			name:      "three concatenated chunks",
			input:     `data: {"id":"1"} {"id":"2"} {"id":"3"}`,
			wantSplit: true,
			wantCount: 3,
		},
		{
			name:      "no data prefix single chunk",
			input:     `{"choices":[{"delta":{"content":"hello"},"index":0}]}`,
			wantSplit: false,
			wantCount: 1,
		},
		{
			name:      "no data prefix concatenated",
			input:     `{"id":"1"} {"id":"2"}`,
			wantSplit: true,
			wantCount: 2,
		},
	}

	n := NewSplitConcatenatedChunksNormalizer()
	ctx := NewContext("test", "test-request")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, modified := n.Normalize([]byte(tt.input), ctx)

			if modified != tt.wantSplit {
				t.Errorf("Normalize() modified=%v, want %v", modified, tt.wantSplit)
			}

			// Count the expected data: chunks in result
			resultStr := string(result)
			count := 0
			for i := 0; i < len(resultStr); i++ {
				if i+6 <= len(resultStr) && resultStr[i:i+6] == "data: " {
					count++
				}
			}

			// For non-prefixed inputs, count JSON objects
			if count == 0 {
				objs := extractJSONObjects(resultStr)
				count = len(objs)
			}

			if count != tt.wantCount {
				t.Errorf("Normalize() produced %d chunks, want %d\nResult: %s", count, tt.wantCount, resultStr)
			}
		})
	}
}

// TestExtractJSONObjects tests the JSON extraction function
func TestExtractJSONObjects(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCount int
	}{
		{
			name:      "single object",
			input:     `{"id":"1"}`,
			wantCount: 1,
		},
		{
			name:      "two objects with space",
			input:     `{"id":"1"} {"id":"2"}`,
			wantCount: 2,
		},
		{
			name:      "three objects",
			input:     `{"id":"1"} {"id":"2"} {"id":"3"}`,
			wantCount: 3,
		},
		{
			name:      "nested objects",
			input:     `{"outer":{"inner":"value"}} {"simple":"obj"}`,
			wantCount: 2,
		},
		{
			name:      "empty string",
			input:     "",
			wantCount: 0,
		},
		{
			name:      "invalid JSON",
			input:     "not json at all",
			wantCount: 0,
		},
		{
			name:      "partial valid JSON",
			input:     `{"valid": true} garbage`,
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractJSONObjects(tt.input)
			if len(result) != tt.wantCount {
				t.Errorf("extractJSONObjects() returned %d objects, want %d\nInput: %s\nResult: %v", len(result), tt.wantCount, tt.input, result)
			}
		})
	}
}

// TestMiniMaxRealErrorCase tests the exact error case from the bug report
func TestMiniMaxRealErrorCase(t *testing.T) {
	// This simulates the MiniMax bug where multiple SSE chunks are concatenated
	input := `data: {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{}","name":"grep"},"id":"call_function_gc0z3psriw4p_2","index":1,"type":"function"}]},"index":0}],"created":1773831570,"id":"chatcmpl-1773831570189075000","model":"MiniMax-M2.5","object":"chat.completion.chunk"} {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{\"caseSensitive\":false,\"include\":\"*.go\"}","name":"grep"},"id":"call_2","index":0,"type":"function"}]},"index":0}],"created":1773831571,"id":"chatcmpl-1773831570799212001","model":"MiniMax-M2.5","object":"chat.completion.chunk"}`

	n := NewSplitConcatenatedChunksNormalizer()
	ctx := NewContext("minimax", "test-request")

	result, modified := n.Normalize([]byte(input), ctx)

	if !modified {
		t.Error("Expected the concatenated MiniMax chunks to be split")
	}

	// The result should have two "data: " prefixes
	resultStr := string(result)
	count := strings.Count(resultStr, "data: ")
	if count != 2 {
		t.Errorf("Expected 2 data: prefixes, got %d\nResult:\n%s", count, resultStr)
	}

	// Verify each JSON chunk is valid by stripping data: prefix
	lines := strings.Split(resultStr, "\n")
	validCount := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			jsonStr := strings.TrimPrefix(line, "data: ")
			var obj interface{}
			if err := json.Unmarshal([]byte(jsonStr), &obj); err == nil {
				validCount++
			}
		}
	}
	if validCount != 2 {
		t.Errorf("Expected 2 valid JSON objects, got %d", validCount)
	}
}
