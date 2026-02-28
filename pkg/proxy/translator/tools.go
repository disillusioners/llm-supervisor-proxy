// Package translator provides protocol translation between Anthropic Messages API
// and OpenAI Chat Completions API formats.
package translator

import (
	"encoding/json"
)

// ─────────────────────────────────────────────────────────────────────────────
// Tool Definition Translation
// ─────────────────────────────────────────────────────────────────────────────

// TranslateTools translates Anthropic tool definitions to OpenAI format.
func TranslateTools(anthropicTools []AnthropicTool) []OpenAITool {
	if anthropicTools == nil {
		return nil
	}

	openAITools := make([]OpenAITool, len(anthropicTools))
	for i, tool := range anthropicTools {
		openAITools[i] = OpenAITool{
			Type: "function",
			Function: OpenAIFunctionSchema{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		}
	}
	return openAITools
}

// TranslateToolChoice translates Anthropic tool_choice to OpenAI format.
func TranslateToolChoice(choice interface{}) interface{} {
	if choice == nil {
		return nil
	}

	// Handle string case: "auto", "any", "none"
	if str, ok := choice.(string); ok {
		return map[string]string{
			"type": str,
		}
	}

	// Handle object case: {"type": "tool", "name": "get_weather"}
	if m, ok := choice.(map[string]interface{}); ok {
		result := make(map[string]interface{})
		if t, ok := m["type"].(string); ok {
			result["type"] = t
		}
		if name, ok := m["name"].(string); ok {
			result["function"] = map[string]string{
				"name": name,
			}
		}
		return result
	}

	return choice
}

// ─────────────────────────────────────────────────────────────────────────────
// Tool Use Translation (Request - Assistant Message)
// ─────────────────────────────────────────────────────────────────────────────

// TranslateToolUseInRequest translates Anthropic tool_use content blocks to OpenAI tool_calls.
// Returns OpenAIMessage with tool_calls populated.
func TranslateToolUseInRequest(blocks []ContentBlock) []OpenAIToolCall {
	if blocks == nil {
		return nil
	}

	var toolCalls []OpenAIToolCall
	for _, block := range blocks {
		if block.Type == "tool_use" {
			// Input is json.RawMessage (already JSON bytes), use directly
			var arguments []byte
			if len(block.Input) > 0 {
				arguments = block.Input
			} else {
				arguments = []byte("{}")
			}
			toolCalls = append(toolCalls, OpenAIToolCall{
				ID:   block.ID,
				Type: "function",
				Function: OpenAIFunctionCall{
					Name:      block.Name,
					Arguments: string(arguments),
				},
			})
		}
	}
	return toolCalls
}

// ─────────────────────────────────────────────────────────────────────────────
// Tool Result Translation (Request - User Message)
// ─────────────────────────────────────────────────────────────────────────────

// TranslateToolResult translates Anthropic tool_result to OpenAI role:tool message.
func TranslateToolResult(block ContentBlock) OpenAIMessage {
	content := ""

	// Handle content which can be string or []ContentBlock
	switch v := block.Content.(type) {
	case string:
		content = v
	case []ContentBlock:
		// If it's a slice of blocks, extract text from them
		content = extractTextFromBlocks(v)
	default:
		// Handle other types by JSON marshaling
		if v != nil {
			if b, err := json.Marshal(v); err == nil {
				content = string(b)
			}
		}
	}

	return OpenAIMessage{
		Role:       "tool",
		ToolCallID: block.ToolUseID,
		Content:    content,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tool Use Translation (Response)
// ─────────────────────────────────────────────────────────────────────────────

// TranslateOpenAIToolCallsToContent translates OpenAI tool_calls to Anthropic content blocks.
func TranslateOpenAIToolCallsToContent(toolCalls []interface{}) []ContentBlock {
	if toolCalls == nil {
		return nil
	}

	blocks := make([]ContentBlock, 0, len(toolCalls))
	for _, tc := range toolCalls {
		// Convert interface{} to map
		tcMap, ok := tc.(map[string]interface{})
		if !ok {
			continue
		}

		id, _ := tcMap["id"].(string)
		tcType, _ := tcMap["type"].(string)

		// Get function object
		funcMap, ok := tcMap["function"].(map[string]interface{})
		if !ok {
			continue
		}

		name, _ := funcMap["name"].(string)
		arguments, _ := funcMap["arguments"].(string)

		// Parse arguments JSON
		var input json.RawMessage
		if arguments != "" {
			input = json.RawMessage(arguments)
		} else {
			input = json.RawMessage("{}")
		}

		block := ContentBlock{
			Type:  "tool_use",
			ID:    id,
			Name:  name,
			Input: input,
		}

		// Only add if it's a function type
		if tcType == "function" {
			blocks = append(blocks, block)
		}
	}

	return blocks
}

// ─────────────────────────────────────────────────────────────────────────────
// Helper Functions
// ─────────────────────────────────────────────────────────────────────────────

// hasToolUse checks if content blocks contain tool_use.
func hasToolUse(blocks []ContentBlock) bool {
	if blocks == nil {
		return false
	}
	for _, block := range blocks {
		if block.Type == "tool_use" {
			return true
		}
	}
	return false
}

// hasToolResult checks if content blocks contain tool_result.
func hasToolResult(blocks []ContentBlock) bool {
	if blocks == nil {
		return false
	}
	for _, block := range blocks {
		if block.Type == "tool_result" {
			return true
		}
	}
	return false
}

// extractTextFromBlocks extracts text content from blocks.
func extractTextFromBlocks(blocks []ContentBlock) string {
	if blocks == nil {
		return ""
	}

	var text string
	for _, block := range blocks {
		if block.Type == "text" {
			text += block.Text
		}
	}
	return text
}
