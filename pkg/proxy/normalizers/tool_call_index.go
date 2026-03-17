package normalizers

import (
	"encoding/json"
	"strings"
)

// FixMissingToolCallIndexNormalizer fixes the issue where tool_calls don't have
// an index field in streaming chunks. This normalizer tracks tool call IDs across
// chunks and assigns indices based on when each new tool call first appears.
//
// State tracking:
// - First chunk with tool_calls: index = 0
// - Subsequent chunks: increment index when new tool call ID appears
// - State is stored in NormalizeContext.SeenToolCallIDs to avoid race conditions
//   when multiple streams are processed concurrently
type FixMissingToolCallIndexNormalizer struct{}

// NewFixMissingToolCallIndexNormalizer creates a new FixMissingToolCallIndexNormalizer
func NewFixMissingToolCallIndexNormalizer() *FixMissingToolCallIndexNormalizer {
	return &FixMissingToolCallIndexNormalizer{}
}

// Name returns the normalizer's identifier
func (n *FixMissingToolCallIndexNormalizer) Name() string {
	return "fix_tool_call_index"
}

// EnabledByDefault returns true - this normalizer should be enabled by default
func (n *FixMissingToolCallIndexNormalizer) EnabledByDefault() bool {
	return true
}

// Reset clears state for a new request stream
// State is stored in context to avoid race conditions
func (n *FixMissingToolCallIndexNormalizer) Reset(ctx *NormalizeContext) {
	ctx.SeenToolCallIDs = make(map[string]int)
}

// Normalize adds missing index field to tool_calls
// Handles: {"tool_calls": [{"id": "call_1", ...}]}
// Becomes: {"tool_calls": [{"index": 0, "id": "call_1", ...}]}
func (n *FixMissingToolCallIndexNormalizer) Normalize(line []byte, ctx *NormalizeContext) ([]byte, bool) {
	// Skip non-data lines
	lineStr := strings.TrimSpace(string(line))
	if lineStr == "" || lineStr == "data: [DONE]" || lineStr == "[DONE]" {
		return line, false
	}

	// Strip "data: " prefix if present
	data := line
	if strings.HasPrefix(lineStr, "data: ") {
		data = []byte(strings.TrimPrefix(lineStr, "data: "))
	}

	// Try to parse as JSON
	var chunk map[string]interface{}
	if err := json.Unmarshal(data, &chunk); err != nil {
		// Not valid JSON, return as-is
		return line, false
	}

	// Navigate to delta.tool_calls
	delta, ok := chunk["delta"].(map[string]interface{})
	if !ok {
		return line, false
	}

	toolCalls, ok := delta["tool_calls"].([]interface{})
	if !ok || len(toolCalls) == 0 {
		return line, false
	}

	modified := false

	// Process each tool call
	for i, tc := range toolCalls {
		tcMap, ok := tc.(map[string]interface{})
		if !ok {
			continue
		}

		// Check if this tool call already has an index
		if _, hasIndex := tcMap["index"]; hasIndex {
			continue
		}

		// Get the tool call ID
		id, hasID := tcMap["id"].(string)
		if !hasID || id == "" {
			// No ID, can't track - assign index based on position in array
			tcMap["index"] = i
			modified = true
			continue
		}

		// Ensure the map is initialized (lazy initialization for thread safety)
		if ctx.SeenToolCallIDs == nil {
			ctx.SeenToolCallIDs = make(map[string]int)
		}

		// Check if we've seen this ID before
		index, seen := ctx.SeenToolCallIDs[id]
		if !seen {
			// New tool call - assign next available index
			index = len(ctx.SeenToolCallIDs)
			ctx.SeenToolCallIDs[id] = index
		}

		tcMap["index"] = index
		modified = true
	}

	if !modified {
		return line, false
	}

	// Marshal back to JSON
	normalized, err := json.Marshal(chunk)
	if err != nil {
		// Failed to marshal, return original
		return line, false
	}

	// Re-add "data: " prefix if it was present
	if strings.HasPrefix(lineStr, "data: ") {
		return []byte("data: " + string(normalized)), true
	}

	return normalized, true
}
