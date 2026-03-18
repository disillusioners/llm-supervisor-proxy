package normalizers

import (
	"encoding/json"
	"strings"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

// ToolCallArgumentsRepairNormalizer repairs malformed JSON in tool_call arguments
// using the toolrepair package. This normalizer processes streaming chunks and
// fixes JSON issues like missing quotes, trailing commas, embedded text, etc.
//
// DEPRECATED: This normalizer is broken for streaming responses because tool call
// arguments are incrementally streamed across multiple chunks. Per-chunk repair cannot
// work because each chunk contains partial JSON that cannot be meaningfully repaired.
//
// Note: This normalizer requires a config to be set via SetConfig before use.
// It is NOT enabled by default and must be added to the registry dynamically.
//
// The proxy now uses post-stream repair (accumulate → repair → rewrite) instead.
// See pkg/proxy/tool_call_accumulator.go and pkg/proxy/buffer_rewriter.go
type ToolCallArgumentsRepairNormalizer struct {
	config *toolrepair.Config
}

// NewToolCallArgumentsRepairNormalizer creates a new normalizer with the given config
func NewToolCallArgumentsRepairNormalizer(config *toolrepair.Config) *ToolCallArgumentsRepairNormalizer {
	if config == nil {
		config = toolrepair.DisabledConfig()
	}
	return &ToolCallArgumentsRepairNormalizer{
		config: config,
	}
}

// Name returns the normalizer's identifier
func (n *ToolCallArgumentsRepairNormalizer) Name() string {
	return "tool_call_arguments_repair"
}

// EnabledByDefault returns false - this normalizer should be explicitly enabled via config
func (n *ToolCallArgumentsRepairNormalizer) EnabledByDefault() bool {
	return false
}

// Reset clears state for a new request stream (stateless normalizer)
func (n *ToolCallArgumentsRepairNormalizer) Reset(ctx *NormalizeContext) {
	// Stateless - no reset needed
}

// SetConfig updates the configuration for this normalizer
func (n *ToolCallArgumentsRepairNormalizer) SetConfig(config *toolrepair.Config) {
	n.config = config
}

// Normalize repairs malformed JSON in tool_call arguments
// Handles: {"delta": {"tool_calls": [{"function": {"arguments": "{key: value}"}}]}}
// Becomes: {"delta": {"tool_calls": [{"function": {"arguments": "{\"key\": \"value\"}"}}]}}
func (n *ToolCallArgumentsRepairNormalizer) Normalize(line []byte, ctx *NormalizeContext) ([]byte, bool) {
	// Skip if repairer is disabled
	if n.config == nil || !n.config.Enabled {
		return line, false
	}

	// Skip non-data lines
	lineStr := strings.TrimSpace(string(line))
	if lineStr == "" || lineStr == "data: [DONE]" || lineStr == "[DONE]" {
		return line, false
	}

	// Strip "data: " prefix if present
	data := line
	hasDataPrefix := strings.HasPrefix(lineStr, "data: ")
	if hasDataPrefix {
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
	repairer := toolrepair.NewRepairer(n.config)

	// Process each tool call
	for _, tc := range toolCalls {
		tcMap, ok := tc.(map[string]interface{})
		if !ok {
			continue
		}

		// Get function object
		fn, ok := tcMap["function"].(map[string]interface{})
		if !ok {
			continue
		}

		// Get arguments string
		args, ok := fn["arguments"].(string)
		if !ok || args == "" {
			continue
		}

		// Check if arguments are already valid JSON
		if isValidJSONArgs(args) {
			continue
		}

		// Attempt repair
		result := repairer.RepairArguments(args, "")
		if result.Success && result.Repaired != args {
			fn["arguments"] = result.Repaired
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
	if hasDataPrefix {
		return []byte("data: " + string(normalized)), true
	}

	return normalized, true
}

// isValidJSONArgs checks if a string is valid JSON
func isValidJSONArgs(s string) bool {
	var js interface{}
	return json.Unmarshal([]byte(s), &js) == nil
}
