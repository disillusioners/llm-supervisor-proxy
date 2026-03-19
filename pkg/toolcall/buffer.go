package toolcall

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

// Constants for tool call limits to prevent memory exhaustion
const (
	// MaxToolCallsPerStream limits the number of tool calls per streaming response
	// to prevent memory exhaustion from malicious or buggy upstreams
	MaxToolCallsPerStream = 100

	// MaxToolCallIndex limits the maximum index value to prevent sparse array attacks
	MaxToolCallIndex = 99
)

// RepairStats tracks repair statistics for the buffer
type RepairStats struct {
	Attempted  int
	Successful int
	Failed     int
}

// ToolCallBuilder accumulates fragments for a single tool call.
type ToolCallBuilder struct {
	ID         string
	Type       string
	Name       string
	Arguments  strings.Builder
	hasEmitted bool
}

// ToolCallBuffer buffers tool call fragments until complete JSON is formed.
// It sits between the normalizer and the stream buffer, intercepting tool call
// chunks and emitting them only when the arguments form valid JSON.
// When configured with a repairer, it will attempt to repair malformed JSON
// before emitting complete tool calls.
type ToolCallBuffer struct {
	mu        sync.Mutex
	builders  map[int]*ToolCallBuilder
	totalSize int64
	maxSize   int64
	modelID   string
	requestID string

	// Tool repair integration
	repairConfig *toolrepair.Config
	repairer     *toolrepair.Repairer
	repairStats  RepairStats

	// streamingStrategy controls repair behavior for streaming
	// "library_only" avoids LLM-based repair to prevent latency
	streamingStrategy string
}

// NewToolCallBuffer creates a new tool call buffer.
// If maxSize is <= 0, defaults to 1MB.
func NewToolCallBuffer(maxSize int64, modelID, requestID string) *ToolCallBuffer {
	if maxSize <= 0 {
		maxSize = 1024 * 1024 // Default 1MB
	}
	return &ToolCallBuffer{
		builders:          make(map[int]*ToolCallBuilder),
		maxSize:           maxSize,
		modelID:           modelID,
		requestID:         requestID,
		streamingStrategy: "library_only", // Default for streaming
	}
}

// NewToolCallBufferWithRepair creates a buffer with repair capabilities.
// If repairConfig is nil or disabled, behaves like NewToolCallBuffer.
func NewToolCallBufferWithRepair(maxSize int64, modelID, requestID string, repairConfig *toolrepair.Config) *ToolCallBuffer {
	b := NewToolCallBuffer(maxSize, modelID, requestID)
	if repairConfig != nil && repairConfig.Enabled {
		b.repairConfig = repairConfig
		b.repairer = toolrepair.NewRepairer(repairConfig)
	}
	return b
}

// ProcessChunk processes a normalized SSE chunk.
// Returns: chunks to emit (may be empty if buffering, or multiple if flushing).
// - Non-tool-call chunks pass through immediately
// - Tool call fragments are buffered until they form valid JSON
// - Complete tool calls are emitted immediately (repaired if needed)
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

	var chunks [][]byte
	var toEmit []emitRequest

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

		// Index bounds validation - prevent invalid indices
		if idx < 0 || idx > MaxToolCallIndex {
			continue // Skip invalid indices
		}

		// Limit total number of tool calls per stream
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
					// Buffer overflow - emit what we have BEFORE resetting
					if !builder.hasEmitted && builder.Arguments.Len() > 0 {
						argsCopy := builder.Arguments.String()
						toEmit = append(toEmit, emitRequest{idx: idx, args: argsCopy, builder: builder})
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
		if b.isCompleteLocked(idx) && !builder.hasEmitted {
			argsCopy := builder.Arguments.String()
			toEmit = append(toEmit, emitRequest{idx: idx, args: argsCopy, builder: builder})
		}
	}

	b.mu.Unlock()

	// Emit complete tool calls OUTSIDE the mutex lock
	// This is critical - repair operations can be slow
	for _, req := range toEmit {
		chunk := b.emitToolCall(req.idx, req.args, req.builder)
		chunks = append(chunks, chunk)
		b.mu.Lock()
		req.builder.hasEmitted = true
		b.mu.Unlock()
	}

	return chunks
}

// emitRequest holds data needed for emission outside the mutex lock
type emitRequest struct {
	idx     int
	args    string
	builder *ToolCallBuilder
}

// isCompleteLocked checks if tool call arguments form valid JSON.
// Must be called with b.mu held.
func (b *ToolCallBuffer) isCompleteLocked(idx int) bool {
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
// This is called OUTSIDE the mutex lock to allow slow repair operations.
func (b *ToolCallBuffer) emitToolCall(idx int, args string, builder *ToolCallBuilder) []byte {
	// Repair arguments if needed - OUTSIDE mutex lock
	if b.repairer != nil && args != "" {
		// Check if already valid JSON
		var js interface{}
		if json.Unmarshal([]byte(args), &js) != nil {
			// Not valid JSON - attempt repair
			b.repairStats.Attempted++

			result := b.repairer.RepairArguments(args, builder.Name)
			if result.Success {
				args = result.Repaired
				b.repairStats.Successful++
				log.Printf("[TOOL-BUFFER] Repaired tool_call[%d] arguments during streaming (tool: %s)", idx, builder.Name)
			} else {
				b.repairStats.Failed++
				log.Printf("[WARN] Tool repair failed for tool_call[%d] (tool: %s), emitting original", idx, builder.Name)
			}
		}
	}

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
								"arguments": args,
							},
						},
					},
				},
			},
		},
	}

	data, _ := json.Marshal(chunk)
	// Add trailing newline for proper SSE format
	return []byte("data: " + string(data) + "\n")
}

// Flush emits all remaining buffered tool calls (called on stream end).
// This ensures that even incomplete tool calls are emitted at stream end.
func (b *ToolCallBuffer) Flush() [][]byte {
	b.mu.Lock()

	// Get sorted indices for consistent ordering
	indices := make([]int, 0, len(b.builders))
	for idx := range b.builders {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	// Collect data for emission
	var toEmit []emitRequest
	for _, idx := range indices {
		builder := b.builders[idx]
		if !builder.hasEmitted {
			argsCopy := builder.Arguments.String()
			toEmit = append(toEmit, emitRequest{idx: idx, args: argsCopy, builder: builder})
		}
	}

	b.mu.Unlock()

	// Emit outside mutex lock
	var chunks [][]byte
	for _, req := range toEmit {
		chunk := b.emitToolCall(req.idx, req.args, req.builder)
		chunks = append(chunks, chunk)
		b.mu.Lock()
		req.builder.hasEmitted = true
		b.mu.Unlock()
	}

	return chunks
}

// GetRepairStats returns the repair statistics.
// Thread-safe.
func (b *ToolCallBuffer) GetRepairStats() RepairStats {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.repairStats
}

// HasRepairer returns true if this buffer has repair capabilities.
func (b *ToolCallBuffer) HasRepairer() bool {
	return b.repairer != nil
}
