package normalizers

import (
	"encoding/json"
	"strings"
)

// FixEmptyRoleNormalizer fixes the issue where delta.role is an empty string
// instead of "assistant" or omitted. This is a common issue with some providers
// like glm-5.
type FixEmptyRoleNormalizer struct{}

// NewFixEmptyRoleNormalizer creates a new FixEmptyRoleNormalizer
func NewFixEmptyRoleNormalizer() *FixEmptyRoleNormalizer {
	return &FixEmptyRoleNormalizer{}
}

// Name returns the normalizer's identifier
func (n *FixEmptyRoleNormalizer) Name() string {
	return "fix_empty_role"
}

// EnabledByDefault returns true - this normalizer should be enabled by default
func (n *FixEmptyRoleNormalizer) EnabledByDefault() bool {
	return true
}

// Reset clears any state (no-op for this normalizer)
func (n *FixEmptyRoleNormalizer) Reset(ctx *NormalizeContext) {
	// No state to reset
}

// Normalize fixes empty role in delta fields
// Handles: {"delta": {"role": ""}}
// Becomes: {"delta": {"role": "assistant"}}
func (n *FixEmptyRoleNormalizer) Normalize(line []byte, ctx *NormalizeContext) ([]byte, bool) {
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

	modified := false

	// Check if this is a chunk with delta.role = ""
	if delta, ok := chunk["delta"].(map[string]interface{}); ok {
		if role, ok := delta["role"].(string); ok && role == "" {
			// Fix: replace empty string with "assistant"
			delta["role"] = "assistant"
			modified = true
		}
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
