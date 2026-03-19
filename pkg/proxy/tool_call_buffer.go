package proxy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ToolCallBuffer buffers tool call fragments until complete JSON is formed.
// It sits between the normalizer and the stream buffer, intercepting tool call
// chunks and emitting them only when the arguments form valid JSON.
type ToolCallBuffer struct {
	mu        sync.Mutex
	builders  map[int]*ToolCallBuilder
	totalSize int64
	maxSize   int64
	modelID   string
	requestID string
}

// ToolCallBuilder accumulates fragments for a single tool call.
type ToolCallBuilder struct {
	ID         string
	Type       string
	Name       string
	Arguments  strings.Builder
	hasEmitted bool
}

// NewToolCallBuffer creates a new tool call buffer.
// If maxSize is <= 0, defaults to 1MB.
func NewToolCallBuffer(maxSize int64, modelID, requestID string) *ToolCallBuffer {
	if maxSize <= 0 {
		maxSize = 1024 * 1024 // Default 1MB
	}
	return &ToolCallBuffer{
		builders:  make(map[int]*ToolCallBuilder),
		maxSize:   maxSize,
		modelID:   modelID,
		requestID: requestID,
	}
}

// ProcessChunk processes a normalized SSE chunk.
// Returns: chunks to emit (may be empty if buffering, or multiple if flushing).
// - Non-tool-call chunks pass through immediately
// - Tool call fragments are buffered until they form valid JSON
// - Complete tool calls are emitted immediately
func (b *ToolCallBuffer) ProcessChunk(line []byte) [][]byte {
	lineStr := strings.TrimSpace(string(line))

	// Skip empty lines and [DONE] markers - pass through
	if lineStr == "" || lineStr == "data: [DONE]" || lineStr == "[DONE]" {
		return [][]byte{line}
	}

	// Strip "data: " prefix if present
	data := line
	hasPrefix := strings.HasPrefix(lineStr, "data: ")
	if hasPrefix {
		data = []byte(strings.TrimPrefix(lineStr, "data: "))
	}

	// Try to parse as JSON
	var chunk map[string]interface{}
	if err := json.Unmarshal(data, &chunk); err != nil {
		// Not valid JSON, pass through
		return [][]byte{line}
	}

	// Check if this chunk has tool_calls
	choices, ok := chunk["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return [][]byte{line} // No choices, pass through
	}

	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return [][]byte{line}
	}

	delta, ok := choice["delta"].(map[string]interface{})
	if !ok {
		return [][]byte{line}
	}

	toolCalls, ok := delta["tool_calls"].([]interface{})
	if !ok || len(toolCalls) == 0 {
		return [][]byte{line} // No tool calls, pass through
	}

	// This chunk has tool calls - buffer them
	return b.processToolCallChunk(toolCalls, hasPrefix)
}

// processToolCallChunk buffers tool call fragments and returns complete chunks.
// It accumulates fragments by index and emits when arguments form valid JSON.
func (b *ToolCallBuffer) processToolCallChunk(toolCalls []interface{}, hasPrefix bool) [][]byte {
	b.mu.Lock()
	defer b.mu.Unlock()

	var chunks [][]byte
	var toEmit []int

	for _, tc := range toolCalls {
		tcMap, ok := tc.(map[string]interface{})
		if !ok {
			continue
		}

		// Get index (default to 0 if missing)
		index, ok := tcMap["index"].(float64)
		if !ok {
			index = 0
		}
		idx := int(index)

		// FIX #3: Index bounds validation - prevent invalid indices
		if idx < 0 || idx > MaxToolCallIndex {
			continue // Skip invalid indices
		}

		// FIX #3: Limit total number of tool calls per stream
		if len(b.builders) >= MaxToolCallsPerStream {
			continue // Skip if too many tool calls
		}

		// Get or create builder
		builder, exists := b.builders[idx]
		if !exists {
			builder = &ToolCallBuilder{}
			b.builders[idx] = builder
		}

		// Accumulate metadata
		if id, ok := tcMap["id"].(string); ok && id != "" {
			builder.ID = id
		}
		if typ, ok := tcMap["type"].(string); ok && typ != "" {
			builder.Type = typ
		}

		// Accumulate function details
		if fn, ok := tcMap["function"].(map[string]interface{}); ok {
			if name, ok := fn["name"].(string); ok && name != "" {
				builder.Name = name
			}
			if args, ok := fn["arguments"].(string); ok {
				// Check size limit
				if b.totalSize+int64(len(args)) > b.maxSize {
					// FIX #2: Buffer overflow - emit what we have BEFORE resetting
					if !builder.hasEmitted && builder.Arguments.Len() > 0 {
						chunks = append(chunks, b.emitToolCall(idx))
						builder.hasEmitted = true
					}
					// Reset builder for this index to accept new data
					b.builders[idx] = &ToolCallBuilder{}
					builder = b.builders[idx]
					// Fall through to add the current fragment
				}
				builder.Arguments.WriteString(args)
				b.totalSize += int64(len(args))
			}
		}

		// Check if this tool call is now complete (valid JSON)
		if b.isComplete(idx) && !builder.hasEmitted {
			toEmit = append(toEmit, idx)
		}
	}

	// Emit complete tool calls
	for _, idx := range toEmit {
		chunks = append(chunks, b.emitToolCall(idx))
		b.builders[idx].hasEmitted = true
	}

	return chunks
}

// isComplete checks if tool call arguments form valid JSON.
func (b *ToolCallBuffer) isComplete(idx int) bool {
	builder, exists := b.builders[idx]
	if !exists {
		return false
	}

	args := builder.Arguments.String()
	if args == "" {
		return false
	}

	var js interface{}
	return json.Unmarshal([]byte(args), &js) == nil
}

// emitToolCall creates a complete SSE chunk for a tool call.
// FIX #1: Includes trailing newline for proper SSE format.
func (b *ToolCallBuffer) emitToolCall(idx int) []byte {
	builder := b.builders[idx]

	chunk := map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   b.modelID,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"delta": map[string]interface{}{
					"tool_calls": []map[string]interface{}{
						{
							"index": idx,
							"id":    builder.ID,
							"type":  builder.Type,
							"function": map[string]interface{}{
								"name":      builder.Name,
								"arguments": builder.Arguments.String(),
							},
						},
					},
				},
			},
		},
	}

	data, _ := json.Marshal(chunk)
	// FIX #1: Add trailing newline for proper SSE format
	return []byte("data: " + string(data) + "\n")
}

// Flush emits all remaining buffered tool calls (called on stream end).
// This ensures that even incomplete tool calls are emitted at stream end.
func (b *ToolCallBuffer) Flush() [][]byte {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Get sorted indices for consistent ordering
	indices := make([]int, 0, len(b.builders))
	for idx := range b.builders {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	var chunks [][]byte
	for _, idx := range indices {
		builder := b.builders[idx]
		if !builder.hasEmitted {
			chunks = append(chunks, b.emitToolCall(idx))
			builder.hasEmitted = true
		}
	}

	return chunks
}
