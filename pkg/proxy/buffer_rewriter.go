package proxy

import (
	"bytes"
	"encoding/json"
	"strings"
)

// rewriteBufferWithRepairedArgs creates a new buffer with repaired tool call arguments.
// This is a memory-efficient implementation that only rewrites chunks containing tool_calls.
//
// The function:
// 1. Gets all chunks from the old buffer
// 2. For each chunk, checks if it contains tool_calls
// 3. If yes, parses and replaces arguments with repaired versions
// 4. If no, passes through unchanged
// 5. Returns the new buffer
func rewriteBufferWithRepairedArgs(oldBuffer *streamBuffer, repairedArgs map[int]string) *streamBuffer {
	// Get all chunks from old buffer
	chunks, _ := oldBuffer.GetChunksFrom(0)

	// Get the max bytes from old buffer
	maxBytes := oldBuffer.maxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxBufferBytes
	}

	// Create new buffer
	newBuffer := newStreamBuffer(maxBytes)

	for _, chunk := range chunks {
		// Check if chunk contains tool_calls (quick string check before JSON parsing)
		if hasToolCalls(chunk) {
			// Parse, repair, re-serialize
			repairedChunk := repairChunkArgs(chunk, repairedArgs)
			newBuffer.Add(repairedChunk)
		} else {
			// Pass through unchanged (trim trailing newline that was added by buffer)
			trimmed := bytes.TrimSuffix(chunk, []byte("\n"))
			newBuffer.Add(trimmed)
		}
	}

	return newBuffer
}

// hasToolCalls checks if a chunk contains tool_calls in delta.
// This is a quick string check to avoid JSON parsing for non-tool-call chunks.
func hasToolCalls(chunk []byte) bool {
	return bytes.Contains(chunk, []byte("tool_calls"))
}

// repairChunkArgs repairs tool call arguments in a single chunk.
// It parses the chunk, replaces arguments with repaired versions, and re-serializes.
// Returns the original chunk if parsing fails or no modifications needed.
func repairChunkArgs(chunk []byte, repairedArgs map[int]string) []byte {
	// Strip newline if present
	chunk = bytes.TrimSuffix(chunk, []byte("\n"))

	// Strip "data: " prefix if present
	var prefix []byte
	lineStr := strings.TrimSpace(string(chunk))
	if strings.HasPrefix(lineStr, "data: ") {
		prefix = []byte("data: ")
		chunk = bytes.TrimPrefix(chunk, prefix)
	}

	// Parse JSON
	var obj map[string]interface{}
	if err := json.Unmarshal(chunk, &obj); err != nil {
		// Can't parse, return original with prefix if needed
		if len(prefix) > 0 {
			return append(prefix, chunk...)
		}
		return chunk
	}

	// Navigate to choices[0].delta.tool_calls
	choices, ok := obj["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return addPrefix(chunk, prefix)
	}

	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return addPrefix(chunk, prefix)
	}

	delta, ok := choice["delta"].(map[string]interface{})
	if !ok {
		return addPrefix(chunk, prefix)
	}

	toolCalls, ok := delta["tool_calls"].([]interface{})
	if !ok || len(toolCalls) == 0 {
		return addPrefix(chunk, prefix)
	}

	modified := false
	for _, tc := range toolCalls {
		tcMap, ok := tc.(map[string]interface{})
		if !ok {
			continue
		}

		// Get index
		index, ok := tcMap["index"].(float64)
		if !ok {
			continue
		}
		idx := int(index)

		// Check if we have repaired args for this index
		if repaired, has := repairedArgs[idx]; has {
			if fn, ok := tcMap["function"].(map[string]interface{}); ok {
				if origArgs, _ := fn["arguments"].(string); origArgs != repaired {
					fn["arguments"] = repaired
					modified = true
				}
			}
		}
	}

	if !modified {
		return addPrefix(chunk, prefix)
	}

	// Re-serialize
	newChunk, err := json.Marshal(obj)
	if err != nil {
		return addPrefix(chunk, prefix)
	}

	// Re-add prefix
	return addPrefix(newChunk, prefix)
}

// addPrefix adds the SSE prefix back to a chunk if it was present.
func addPrefix(chunk []byte, prefix []byte) []byte {
	if len(prefix) > 0 {
		return append(prefix, chunk...)
	}
	return chunk
}
