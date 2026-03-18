package normalizers

import (
	"encoding/json"
	"strings"
)

// SplitConcatenatedChunksNormalizer fixes the issue where some providers (e.g., MiniMax)
// send multiple SSE data chunks concatenated on a single line instead of proper SSE format.
//
// Malformed input:  data: {...} {...}
// Fixed output:     data: {...}\ndata: {...}
//
// This happens when the upstream provider sends multiple JSON objects separated by spaces
// instead of proper newlines between SSE events.
type SplitConcatenatedChunksNormalizer struct{}

// NewSplitConcatenatedChunksNormalizer creates a new SplitConcatenatedChunksNormalizer
func NewSplitConcatenatedChunksNormalizer() *SplitConcatenatedChunksNormalizer {
	return &SplitConcatenatedChunksNormalizer{}
}

// Name returns the normalizer's identifier
func (n *SplitConcatenatedChunksNormalizer) Name() string {
	return "split_concatenated_chunks"
}

// EnabledByDefault returns true - this normalizer should be enabled by default
func (n *SplitConcatenatedChunksNormalizer) EnabledByDefault() bool {
	return true
}

// Reset clears any state (no-op for this normalizer)
func (n *SplitConcatenatedChunksNormalizer) Reset(ctx *NormalizeContext) {
	// No state to reset
}

// Normalize splits concatenated JSON chunks into proper SSE format
func (n *SplitConcatenatedChunksNormalizer) Normalize(line []byte, ctx *NormalizeContext) ([]byte, bool) {
	// Skip empty lines and special markers
	lineStr := strings.TrimSpace(string(line))
	if lineStr == "" || lineStr == "data: [DONE]" || lineStr == "[DONE]" {
		return line, false
	}

	// Check for "data: " prefix
	hasDataPrefix := strings.HasPrefix(lineStr, "data: ")
	data := lineStr
	if hasDataPrefix {
		data = strings.TrimPrefix(lineStr, "data: ")
	}

	// Try to parse as a single JSON object first
	// Use a decoder that rejects trailing content
	decoder := json.NewDecoder(strings.NewReader(data))
	var singleObj interface{}
	if err := decoder.Decode(&singleObj); err == nil && !decoder.More() {
		// Valid single JSON with no trailing content, no splitting needed
		return line, false
	}

	// Not a single JSON - try to find and extract multiple JSON objects
	chunks := extractJSONObjects(data)
	if len(chunks) <= 1 {
		// No multiple JSON objects found, return as-is
		return line, false
	}

	// Reconstruct as proper SSE format with multiple data: lines
	var result strings.Builder
	for i, chunk := range chunks {
		if hasDataPrefix {
			result.WriteString("data: ")
		}
		result.WriteString(chunk)
		// Add newline between chunks (but not after the last one - the buffer.Add will add it)
		if i < len(chunks)-1 {
			result.WriteString("\n")
		}
	}

	return []byte(result.String()), true
}

// extractJSONObjects finds all complete JSON objects in a string
// This handles the case where multiple JSON objects are concatenated with spaces
func extractJSONObjects(s string) []string {
	var objects []string
	decoder := json.NewDecoder(strings.NewReader(s))

	for {
		var obj interface{}
		err := decoder.Decode(&obj)
		if err != nil {
			break
		}
		// Get the raw JSON bytes that were just decoded
		// We need to re-marshal to get the exact string
		rawBytes, err := json.Marshal(obj)
		if err != nil {
			break
		}
		objects = append(objects, string(rawBytes))
	}

	return objects
}
