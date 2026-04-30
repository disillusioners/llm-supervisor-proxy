package translator

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// Batch Stream Translation
// ─────────────────────────────────────────────────────────────────────────────

// TranslateBufferedStream translates buffered OpenAI SSE chunks to Anthropic events.
// This is called after [DONE] is received, with all chunks buffered in memory.
// It returns the complete Anthropic SSE event sequence.
func TranslateBufferedStream(openaiBuffer []byte, originalModel string) ([]byte, error) {
	// Parse all OpenAI chunks from the buffer
	state := &StreamState{
		MessageID:     generateAnthropicMessageID(),
		OriginalModel: originalModel,
	}

	// Scan through all SSE data lines
	scanner := bufio.NewScanner(bytes.NewReader(openaiBuffer))
	for scanner.Scan() {
		line := scanner.Bytes()

		// Only process data lines
		if bytes.HasPrefix(line, []byte("data: ")) {
			data := bytes.TrimPrefix(line, []byte("data: "))

			// Skip [DONE] marker
			if string(data) == "[DONE]" {
				continue
			}

			// Parse the chunk
			var chunk map[string]interface{}
			if err := json.Unmarshal(data, &chunk); err != nil {
				log.Printf("Failed to parse stream chunk: %v (data: %.100s...)", err, string(data))
				continue // Skip invalid chunks
			}

			// Extract content from the chunk
			extractChunkContent(chunk, state)
		}
	}

	// Generate Anthropic events
	events := generateAnthropicEvents(state)

	// Combine all events into a single byte slice
	var buf bytes.Buffer
	for _, event := range events {
		buf.WriteString(event)
	}

	// Trap 5 Fix: Copy buffer into new slice before returning to prevent retaining large backing array
	return append([]byte(nil), buf.Bytes()...), nil
}

// extractChunkContent extracts content from an OpenAI stream chunk into state
func extractChunkContent(chunk map[string]interface{}, state *StreamState) {
	choices, ok := chunk["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return
	}

	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return
	}

	// Extract usage if present (usually in the final chunk)
	if usage, ok := chunk["usage"].(map[string]interface{}); ok {
		state.Usage = translateUsage(usage)
	}

	// Extract finish_reason
	if finishReason, ok := choice["finish_reason"].(string); ok {
		state.StopReason = mapFinishReason(finishReason)
	}

	// Extract delta content
	delta, ok := choice["delta"].(map[string]interface{})
	if !ok {
		return
	}

	// Text content
	if content, ok := delta["content"].(string); ok {
		state.AccumulatedContent.WriteString(content)
	}

	// Reasoning/thinking content
	if reasoning, ok := delta["reasoning_content"].(string); ok {
		state.ThinkingContent.WriteString(reasoning)
	}
	if thinking, ok := delta["thinking"].(string); ok {
		state.ThinkingContent.WriteString(thinking)
	}

	// Tool calls
	if toolCalls, ok := delta["tool_calls"].([]interface{}); ok {
		for _, tc := range toolCalls {
			tcMap, ok := tc.(map[string]interface{})
			if !ok {
				continue
			}

			// CRITICAL: Preserve the index from OpenAI
			index := 0
			if idx, ok := tcMap["index"].(float64); ok {
				index = int(idx)
			}

			// Ensure we have enough slots
			for len(state.ToolCalls) <= index {
				state.ToolCalls = append(state.ToolCalls, ToolCallState{Index: index})
			}

			// Update tool call state
			if id, ok := tcMap["id"].(string); ok && id != "" {
				state.ToolCalls[index].ID = id
			}

			if function, ok := tcMap["function"].(map[string]interface{}); ok {
				if name, ok := function["name"].(string); ok {
					state.ToolCalls[index].Name = name
				}
				if args, ok := function["arguments"].(string); ok {
					state.ToolCalls[index].Arguments.WriteString(args)
				}
			}
		}
	}
}

// generateAnthropicEvents generates the complete sequence of Anthropic SSE events
func generateAnthropicEvents(state *StreamState) []string {
	var events []string

	// 1. message_start event
	messageStart := map[string]interface{}{
		"type": EventMessageStart,
		"message": map[string]interface{}{
			"id":          state.MessageID,
			"type":        "message",
			"role":        "assistant",
			"content":     []interface{}{},
			"model":       state.OriginalModel,
			"stop_reason": nil,
			"usage": map[string]interface{}{
				"input_tokens":  state.Usage.InputTokens,
				"output_tokens": 0,
			},
		},
	}
	events = append(events, formatSSEEvent(string(EventMessageStart), messageStart))

	// 1b. ping event (sent after message_start per Anthropic spec)
	pingEvent := map[string]interface{}{
		"type": EventPing,
	}
	events = append(events, formatSSEEvent(string(EventPing), pingEvent))

	// Determine content blocks to emit
	var contentBlocks []ContentBlock
	var blockIndex int

	// 2. Thinking block (separate content block with type "thinking")
	if state.ThinkingContent.Len() > 0 {
		// content_block_start for thinking
		events = append(events, formatContentBlockStart(blockIndex, "thinking"))

		// content_block_delta for thinking
		events = append(events, formatThinkingBlockDelta(blockIndex, state.ThinkingContent.String()))

		// content_block_stop
		events = append(events, formatContentBlockStop(blockIndex))
		blockIndex++
	}

	// 3. Text block (separate content block with type "text")
	if state.AccumulatedContent.Len() > 0 {
		// content_block_start for text
		events = append(events, formatContentBlockStart(blockIndex, "text"))

		// content_block_delta for text
		events = append(events, formatContentBlockDelta(blockIndex, "text_delta", state.AccumulatedContent.String()))

		// content_block_stop
		events = append(events, formatContentBlockStop(blockIndex))
		blockIndex++
	}
	for _, tc := range state.ToolCalls {
		if tc.ID == "" || tc.Name == "" {
			continue
		}

		// content_block_start for tool_use
		events = append(events, formatToolUseBlockStart(blockIndex, tc.ID, tc.Name))

		// content_block_delta for input_json_delta (if there are arguments)
		if tc.Arguments.Len() > 0 {
			events = append(events, formatInputJsonDelta(blockIndex, tc.Arguments.String()))
		}

		// content_block_stop
		events = append(events, formatContentBlockStop(blockIndex))

		contentBlocks = append(contentBlocks, ContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Name,
			Input: json.RawMessage(tc.Arguments.String()),
		})
		blockIndex++
	}

	// 5. message_delta event (with final usage and stop_reason)
	messageDelta := map[string]interface{}{
		"type": EventMessageDelta,
		"delta": map[string]interface{}{
			"stop_reason": state.StopReason,
		},
		"usage": map[string]interface{}{
			"output_tokens": state.Usage.OutputTokens,
		},
	}
	events = append(events, formatSSEEvent(string(EventMessageDelta), messageDelta))

	// 6. message_stop event
	messageStop := map[string]interface{}{
		"type": EventMessageStop,
	}
	events = append(events, formatSSEEvent(string(EventMessageStop), messageStop))

	return events
}

// ─────────────────────────────────────────────────────────────────────────────
// SSE Event Formatting Helpers
// ─────────────────────────────────────────────────────────────────────────────

// formatSSEEvent formats an SSE event with event type and data
func formatSSEEvent(eventType string, data interface{}) string {
	dataBytes, _ := json.Marshal(data)
	return fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, string(dataBytes))
}

// formatContentBlockStart formats a content_block_start event
func formatContentBlockStart(index int, blockType string) string {
	contentBlock := map[string]interface{}{
		"type": blockType,
	}

	// Initialize text/thinking field based on block type
	// This is required by the Anthropic SDK to avoid None type errors
	switch blockType {
	case "text":
		contentBlock["text"] = ""
	case "thinking":
		contentBlock["thinking"] = ""
	}

	event := map[string]interface{}{
		"type":          EventContentBlockStart,
		"index":         index,
		"content_block": contentBlock,
	}
	return formatSSEEvent(string(EventContentBlockStart), event)
}

// formatToolUseBlockStart formats a content_block_start event for tool_use
func formatToolUseBlockStart(index int, id, name string) string {
	event := map[string]interface{}{
		"type":  EventContentBlockStart,
		"index": index,
		"content_block": map[string]interface{}{
			"type":  "tool_use",
			"id":    id,
			"name":  name,
			"input": map[string]interface{}{},
		},
	}
	return formatSSEEvent(string(EventContentBlockStart), event)
}

// formatContentBlockDelta formats a content_block_delta event for text_delta type
func formatContentBlockDelta(index int, deltaType, text string) string {
	event := map[string]interface{}{
		"type":  EventContentBlockDelta,
		"index": index,
		"delta": map[string]interface{}{
			"type": deltaType,
			"text": text,
		},
	}
	return formatSSEEvent(string(EventContentBlockDelta), event)
}

// formatThinkingBlockDelta formats a content_block_delta event for thinking_delta type
func formatThinkingBlockDelta(index int, thinking string) string {
	event := map[string]interface{}{
		"type":  EventContentBlockDelta,
		"index": index,
		"delta": map[string]interface{}{
			"type":     "thinking_delta",
			"thinking": thinking,
		},
	}
	return formatSSEEvent(string(EventContentBlockDelta), event)
}

// formatInputJsonDelta formats a content_block_delta event for tool input JSON
func formatInputJsonDelta(index int, partialJSON string) string {
	event := map[string]interface{}{
		"type":  EventContentBlockDelta,
		"index": index,
		"delta": map[string]interface{}{
			"type":         "input_json_delta",
			"partial_json": partialJSON,
		},
	}
	return formatSSEEvent(string(EventContentBlockDelta), event)
}

// formatContentBlockStop formats a content_block_stop event
func formatContentBlockStop(index int) string {
	event := map[string]interface{}{
		"type":  EventContentBlockStop,
		"index": index,
	}
	return formatSSEEvent(string(EventContentBlockStop), event)
}

// ─────────────────────────────────────────────────────────────────────────────
// Streaming SSE Parser (for incremental translation if needed)
// ─────────────────────────────────────────────────────────────────────────────

// ParseOpenAISSEChunk parses a single OpenAI SSE data line
// Returns the parsed JSON and whether it's the [DONE] marker
func ParseOpenAISSEChunk(line string) (map[string]interface{}, bool, error) {
	line = strings.TrimSpace(line)

	if !strings.HasPrefix(line, "data: ") {
		return nil, false, nil // Not a data line
	}

	data := strings.TrimPrefix(line, "data: ")

	if data == "[DONE]" {
		return nil, true, nil
	}

	var chunk map[string]interface{}
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return nil, false, fmt.Errorf("failed to parse chunk JSON: %w", err)
	}

	return chunk, false, nil
}
