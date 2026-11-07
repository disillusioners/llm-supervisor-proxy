package proxy

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/translator"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// convertAnthropicMessagesToStore converts Anthropic messages to store format with full fidelity:
// - tool_use blocks -> store.ToolCall
// - tool_result blocks -> store.Message.Content with "[tool_result <id>] <content>" prefix
// - thinking blocks -> store.Message.Thinking
// - text blocks -> store.Message.Content
func convertAnthropicMessagesToStore(messages []translator.AnthropicMessage) []store.Message {
	return convertAnthropicMessagesToStoreHelper(messages)
}

// convertAnthropicMessagesToStoreAdapter is kept as an alias pointing to the shared helper
// to preserve any package-internal compatibility.
func convertAnthropicMessagesToStoreAdapter(messages []translator.AnthropicMessage) []store.Message {
	return convertAnthropicMessagesToStoreHelper(messages)
}

func convertAnthropicMessagesToStoreHelper(messages []translator.AnthropicMessage) []store.Message {
	var result []store.Message
	for _, msg := range messages {
		var (
			textParts []string
			thinking  strings.Builder
			toolCalls []store.ToolCall
		)

		switch c := msg.Content.(type) {
		case string:
			textParts = append(textParts, c)
		case []interface{}:
			for _, block := range c {
				bm, ok := block.(map[string]interface{})
				if !ok {
					continue
				}
				blockType, _ := bm["type"].(string)
				switch blockType {
				case "text":
					if t, ok := bm["text"].(string); ok && t != "" {
						textParts = append(textParts, t)
					}
				case "tool_use":
					id, _ := bm["id"].(string)
					name, _ := bm["name"].(string)
					args := normalizeJSONArguments(bm["input"])
					toolCalls = append(toolCalls, store.ToolCall{
						ID:   id,
						Type: "function",
						Function: store.Function{
							Name:      name,
							Arguments: args,
						},
					})
				case "tool_result":
					toolUseID, _ := bm["tool_use_id"].(string)
					resContent := formatAnthropicToolResultContent(bm["content"])
					if isErr, _ := bm["is_error"].(bool); isErr {
						resContent = "Error: " + resContent
					}
					prefix := ""
					if toolUseID != "" {
						prefix = fmt.Sprintf("[tool_result %s] ", toolUseID)
					}
					textParts = append(textParts, prefix+resContent)
				case "thinking":
					// signature is a redacted-thinking auth marker — intentionally not persisted.
					if th, ok := bm["thinking"].(string); ok {
						thinking.WriteString(th)
					}
				}
			}
		case []translator.ContentBlock:
			for _, block := range c {
				switch block.Type {
				case "text":
					if block.Text != "" {
						textParts = append(textParts, block.Text)
					}
				case "tool_use":
					args := normalizeJSONArguments(block.Input)
					toolCalls = append(toolCalls, store.ToolCall{
						ID:   block.ID,
						Type: "function",
						Function: store.Function{
							Name:      block.Name,
							Arguments: args,
						},
					})
				case "tool_result":
					resContent := formatAnthropicToolResultContent(block.Content)
					if block.IsError {
						resContent = "Error: " + resContent
					}
					prefix := ""
					if block.ToolUseID != "" {
						prefix = fmt.Sprintf("[tool_result %s] ", block.ToolUseID)
					}
					textParts = append(textParts, prefix+resContent)
				case "thinking":
					if block.Thinking != "" {
						thinking.WriteString(block.Thinking)
					}
				}
			}
		}

		// Anthropic emits text blocks without implicit separators (base-parity: stored text must stay byte-identical to prior behavior); tool_result items are discrete lines.
		result = append(result, store.Message{
			Role:      msg.Role,
			Content:   strings.Join(textParts, ""),
			Thinking:  thinking.String(),
			ToolCalls: toolCalls,
		})
	}
	return result
}

func formatAnthropicToolResultContent(content interface{}) string {
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		var parts []string
		for _, item := range c {
			if itemMap, ok := item.(map[string]interface{}); ok {
				blockType, _ := itemMap["type"].(string)
				switch blockType {
				case "text":
					if t, ok := itemMap["text"].(string); ok {
						parts = append(parts, t)
					}
				case "image":
					parts = append(parts, "[image]")
				default:
					if t, ok := itemMap["text"].(string); ok && t != "" {
						parts = append(parts, t)
					} else {
						parts = append(parts, "[unsupported block]")
					}
				}
			}
		}
		return strings.Join(parts, "\n")
	case []translator.ContentBlock:
		var parts []string
		for _, block := range c {
			switch block.Type {
			case "text":
				parts = append(parts, block.Text)
			case "image":
				parts = append(parts, "[image]")
			default:
				if block.Text != "" {
					parts = append(parts, block.Text)
				} else {
					parts = append(parts, "[unsupported block]")
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		if content == nil {
			return ""
		}
		if b, err := json.Marshal(content); err == nil {
			return string(b)
		}
		return fmt.Sprintf("%v", content)
	}
}

// normalizeJSONArguments unifies tool_use input across both raw-map and typed paths:
// it validates that the resulting Function.Arguments parses as a JSON object;
// on failure or non-object it falls back to "{}".
func normalizeJSONArguments(input interface{}) string {
	if input == nil {
		return "{}"
	}
	var rawStr string
	switch inp := input.(type) {
	case string:
		rawStr = strings.TrimSpace(inp)
	case []byte:
		rawStr = strings.TrimSpace(string(inp))
	case json.RawMessage:
		rawStr = strings.TrimSpace(string(inp))
	default:
		if b, err := json.Marshal(inp); err == nil {
			rawStr = string(b)
		} else {
			return "{}"
		}
	}

	if rawStr == "" {
		return "{}"
	}

	// Validate it parses as a JSON object
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(rawStr), &obj); err != nil || obj == nil {
		return "{}"
	}
	return rawStr
}

// extractOpenAIParameters extracts non-standard parameters from OpenAI request body.
func extractOpenAIParameters(body map[string]interface{}) map[string]interface{} {
	standardFields := map[string]bool{
		"model":             true,
		"messages":          true,
		"stream":            true,
		"max_tokens":        true,
		"temperature":       true,
		"top_p":             true,
		"n":                 true,
		"stop":              true,
		"presence_penalty":  true,
		"frequency_penalty": true,
		"logit_bias":        true,
		"user":              true,
		"tools":             true,
		"tool_choice":       true,
	}

	params := make(map[string]interface{})
	for k, v := range body {
		if !standardFields[k] {
			params[k] = v
		}
	}
	if len(params) == 0 {
		return nil
	}
	return params
}

// parseOpenAIMessages converts OpenAI messages to store format.
func parseOpenAIMessages(body map[string]interface{}) []store.Message {
	messages := []store.Message{}

	msgs, ok := body["messages"].([]interface{})
	if !ok {
		return messages
	}

	for _, m := range msgs {
		msgMap, ok := m.(map[string]interface{})
		if !ok {
			continue
		}

		role, _ := msgMap["role"].(string)
		if role == "" {
			continue
		}

		content := extractContentAsString(msgMap["content"])
		thinking := ""
		var toolCalls []store.ToolCall

		// Extract reasoning_content if present
		if rc, ok := msgMap["reasoning_content"].(string); ok {
			thinking = rc
		}

		// Extract tool calls
		if tcs, ok := msgMap["tool_calls"].([]interface{}); ok {
			for _, tc := range tcs {
				if tcMap, ok := tc.(map[string]interface{}); ok {
					toolCall := store.ToolCall{
						ID:   adapterGetString(tcMap, "id"),
						Type: adapterGetString(tcMap, "type"),
					}
					if fn, ok := tcMap["function"].(map[string]interface{}); ok {
						toolCall.Function.Name = adapterGetString(fn, "name")
						toolCall.Function.Arguments = adapterGetString(fn, "arguments")
					}
					toolCalls = append(toolCalls, toolCall)
				}
			}
		}

		messages = append(messages, store.Message{
			Role:      role,
			Content:   content,
			Thinking:  thinking,
			ToolCalls: toolCalls,
		})
	}

	return messages
}

// extractContentAsString extracts content as a string from various formats.
func extractContentAsString(content interface{}) string {
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		// Multimodal content - extract text parts
		var textParts []string
		for _, part := range c {
			if pm, ok := part.(map[string]interface{}); ok {
				if t, ok := pm["text"].(string); ok {
					textParts = append(textParts, t)
				}
			}
		}
		return strings.Join(textParts, "\n")
	}
	return ""
}

// adapterGetString safely extracts a string from a map.
func adapterGetString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// extractToolCallsFromOpenAI extracts tool calls from OpenAI message format.
func extractToolCallsFromOpenAI(msgMap map[string]interface{}) []store.ToolCall {
	var toolCalls []store.ToolCall

	tcs, ok := msgMap["tool_calls"].([]interface{})
	if !ok {
		return toolCalls
	}

	for _, tc := range tcs {
		if tcMap, ok := tc.(map[string]interface{}); ok {
			toolCall := store.ToolCall{
				ID:   adapterGetString(tcMap, "id"),
				Type: adapterGetString(tcMap, "type"),
			}
			if fn, ok := tcMap["function"].(map[string]interface{}); ok {
				toolCall.Function.Name = adapterGetString(fn, "name")
				toolCall.Function.Arguments = adapterGetString(fn, "arguments")
			}
			toolCalls = append(toolCalls, toolCall)
		}
	}

	return toolCalls
}

// extractResponseFromOpenAI extracts content, thinking, and tool calls from OpenAI response.
func extractResponseFromOpenAI(openaiResponse []byte) (content, thinking string, toolCalls []store.ToolCall, err error) {
	var resp map[string]interface{}
	if err := json.Unmarshal(openaiResponse, &resp); err != nil {
		return "", "", nil, err
	}

	choices, _ := resp["choices"].([]interface{})
	if len(choices) == 0 {
		return "", "", nil, nil
	}

	choice, _ := choices[0].(map[string]interface{})
	message, _ := choice["message"].(map[string]interface{})
	if message == nil {
		return "", "", nil, nil
	}

	// Extract content
	content, _ = message["content"].(string)

	// Extract thinking (from reasoning_content if present)
	thinking, _ = message["reasoning_content"].(string)

	// Extract tool calls
	toolCalls = extractToolCallsFromOpenAI(message)

	return content, thinking, toolCalls, nil
}
