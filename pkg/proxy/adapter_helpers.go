package proxy

import (
	"encoding/json"
	"strings"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// Shared adapter helper functions
// ─────────────────────────────────────────────────────────────────────────────

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
