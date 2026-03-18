package proxy

import (
	"encoding/json"
	"log"
	"strings"
	"sync"
)

// Constants for tool call limits to prevent memory exhaustion
const (
	// MaxToolCallsPerStream limits the number of tool calls per streaming response
	// to prevent memory exhaustion from malicious or buggy upstreams
	MaxToolCallsPerStream = 100

	// MaxToolCallIndex limits the maximum index value to prevent sparse array attacks
	MaxToolCallIndex = 99

	// MaxToolCallArgsSize limits the total size of tool call arguments per index
	MaxToolCallArgsSize = 1024 * 1024 // 1MB per tool call
)

// ToolCallAccumulator accumulates tool call arguments during streaming.
// This is necessary because tool call arguments are incrementally streamed
// across multiple SSE chunks, and repair can only be done on the complete JSON.
//
// This mirrors the pattern from handler_helpers.go but is designed for use
// in race_executor.go for the streaming response path.
type ToolCallAccumulator struct {
	mu       sync.Mutex
	args     map[int]*strings.Builder
	metadata map[int]ToolCallMeta
}

// ToolCallMeta holds metadata about a tool call (ID, type, name).
type ToolCallMeta struct {
	ID   string
	Type string
	Name string
}

// NewToolCallAccumulator creates a new ToolCallAccumulator.
func NewToolCallAccumulator() *ToolCallAccumulator {
	return &ToolCallAccumulator{
		args:     make(map[int]*strings.Builder),
		metadata: make(map[int]ToolCallMeta),
	}
}

// ProcessChunk extracts and accumulates tool calls from a streaming SSE chunk.
// This is a side-effect function - the chunk is passed through unchanged.
// It returns an error if the chunk cannot be parsed, but this should not
// interrupt the stream processing.
func (a *ToolCallAccumulator) ProcessChunk(line []byte) error {
	// Parse the SSE line to extract tool_calls
	lineStr := strings.TrimSpace(string(line))

	// Skip empty lines and [DONE] markers
	if lineStr == "" || lineStr == "data: [DONE]" || lineStr == "[DONE]" {
		return nil
	}

	// Strip "data: " prefix if present
	data := line
	if strings.HasPrefix(lineStr, "data: ") {
		data = []byte(strings.TrimPrefix(lineStr, "data: "))
	}

	// Try to parse as JSON
	var chunk map[string]interface{}
	if err := json.Unmarshal(data, &chunk); err != nil {
		// Not valid JSON, skip
		return err
	}

	// Navigate to choices[0].delta.tool_calls
	choices, ok := chunk["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return nil
	}

	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return nil
	}

	delta, ok := choice["delta"].(map[string]interface{})
	if !ok {
		return nil
	}

	toolCalls, ok := delta["tool_calls"].([]interface{})
	if !ok {
		// tool_calls field is not an array - this is unexpected
		log.Printf("[DEBUG] Tool calls field is not an array: %T", delta["tool_calls"])
		return nil
	}
	if len(toolCalls) == 0 {
		// Empty array is valid, but log for debugging
		return nil
	}

	// Lock for thread-safe accumulation
	a.mu.Lock()
	defer a.mu.Unlock()

	// Process each tool call in this chunk
	for _, tc := range toolCalls {
		tcMap, ok := tc.(map[string]interface{})
		if !ok {
			continue
		}

		// Get index (fallback to 0 if missing per OpenAI spec section 11)
		index, ok := tcMap["index"].(float64)
		if !ok {
			log.Printf("[WARN] Tool call missing index field, defaulting to 0")
			index = 0
		}
		idx := int(index)

		// Validate index bounds
		if idx < 0 || idx > MaxToolCallIndex {
			log.Printf("[WARN] Tool call index %d out of bounds (max: %d), skipping", idx, MaxToolCallIndex)
			continue
		}

		// Check max tool call count
		if len(a.args) >= MaxToolCallsPerStream {
			log.Printf("[WARN] Max tool call count (%d) exceeded, skipping index %d", MaxToolCallsPerStream, idx)
			continue
		}

		// Ensure we have a builder for this index
		if _, exists := a.args[idx]; !exists {
			a.args[idx] = &strings.Builder{}
			a.metadata[idx] = ToolCallMeta{}
		}

		// Validate type field if present
		if typ, ok := tcMap["type"].(string); ok && typ != "" && typ != "function" {
			log.Printf("[WARN] Tool call has unexpected type: %s (expected 'function')", typ)
		}

		// Update metadata (ID, type, name) if present in this chunk
		// Note: We need to copy to a local variable first since we can't take address of map element
		meta := a.metadata[idx]

		if id, ok := tcMap["id"].(string); ok && id != "" {
			meta.ID = id
		}
		if typ, ok := tcMap["type"].(string); ok && typ != "" {
			meta.Type = typ
		}

		// Accumulate function details
		if fn, ok := tcMap["function"].(map[string]interface{}); ok {
			if name, ok := fn["name"].(string); ok && name != "" {
				meta.Name = name
			}
			// Accumulate arguments - this is the key part!
			if args, ok := fn["arguments"].(string); ok {
				// Check argument size limit before writing
				if a.args[idx].Len()+len(args) > MaxToolCallArgsSize {
					log.Printf("[WARN] Tool call[%d] arguments exceed size limit (%d bytes), truncating", idx, MaxToolCallArgsSize)
				} else {
					a.args[idx].WriteString(args)
				}
			}
		}

		// Store updated metadata back
		a.metadata[idx] = meta
	}

	return nil
}

// GetAccumulatedArgs returns all accumulated arguments as a map.
// Returns map[index]completeArgumentsString
// Thread-safe.
func (a *ToolCallAccumulator) GetAccumulatedArgs() map[int]string {
	a.mu.Lock()
	defer a.mu.Unlock()

	result := make(map[int]string)
	for idx, builder := range a.args {
		result[idx] = builder.String()
	}
	return result
}

// GetMetadata returns the accumulated metadata for all tool calls.
// Returns map[index]ToolCallMeta
// Thread-safe.
func (a *ToolCallAccumulator) GetMetadata() map[int]ToolCallMeta {
	a.mu.Lock()
	defer a.mu.Unlock()

	result := make(map[int]ToolCallMeta)
	for idx, meta := range a.metadata {
		result[idx] = meta
	}
	return result
}

// HasToolCalls returns true if any tool calls were accumulated.
// Thread-safe.
func (a *ToolCallAccumulator) HasToolCalls() bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	return len(a.args) > 0
}

// Count returns the number of tool call indices that have been accumulated.
// Thread-safe.
func (a *ToolCallAccumulator) Count() int {
	a.mu.Lock()
	defer a.mu.Unlock()

	return len(a.args)
}
