package translator

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"
)

// TranslateNonStreamResponse translates an OpenAI non-streaming response to Anthropic format
func TranslateNonStreamResponse(openaiBody []byte, originalModel string) ([]byte, error) {
	// Parse OpenAI response
	var openaiResp map[string]interface{}
	if err := json.Unmarshal(openaiBody, &openaiResp); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI response: %w", err)
	}

	// Build Anthropic response
	anthropicResp := make(map[string]interface{})

	// Generate new Anthropic message ID
	anthropicResp["id"] = generateAnthropicMessageID()
	anthropicResp["type"] = "message"
	anthropicResp["role"] = "assistant"

	// Extract message content from choices
	var content []ContentBlock
	if choices, ok := openaiResp["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if msg, ok := choice["message"].(map[string]interface{}); ok {
				content = extractContentFromOpenAIMessage(msg)
			}
		}
	}
	anthropicResp["content"] = content

	// Set model (use original Anthropic model)
	anthropicResp["model"] = originalModel

	// Map stop reason
	var stopReason string
	if choices, ok := openaiResp["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if finishReason, ok := choice["finish_reason"].(string); ok {
				stopReason = mapFinishReason(finishReason)
			}
		}
	}
	if stopReason != "" {
		anthropicResp["stop_reason"] = stopReason
	}

	// Translate usage
	if usage, ok := openaiResp["usage"].(map[string]interface{}); ok {
		anthropicResp["usage"] = translateUsage(usage)
	}

	// Marshal back to JSON
	result, err := json.Marshal(anthropicResp)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Anthropic response: %w", err)
	}

	return result, nil
}

// generateAnthropicMessageID generates a message ID in Anthropic format
func generateAnthropicMessageID() string {
	// Anthropic format: msg_ + 24 character base62 string
	chars := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	id := make([]byte, 24)
	if _, err := rand.Read(id); err != nil {
		// Fallback to timestamp-based ID if crypto/rand fails
		return fmt.Sprintf("msg_%d%016d", time.Now().UnixNano()/1000000, time.Now().Nanosecond())
	}
	for i := range id {
		id[i] = chars[int(id[i])%len(chars)]
	}
	return "msg_" + string(id)
}

// mapFinishReason maps OpenAI finish_reason to Anthropic stop_reason
func mapFinishReason(finishReason string) string {
	switch finishReason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	default:
		return "end_turn"
	}
}

// translateUsage translates OpenAI usage to Anthropic format
func translateUsage(openaiUsage map[string]interface{}) UsageInfo {
	usage := UsageInfo{}

	// prompt_tokens -> input_tokens
	if promptTokens, ok := openaiUsage["prompt_tokens"].(float64); ok {
		usage.InputTokens = int(promptTokens)
	}

	// completion_tokens -> output_tokens
	if completionTokens, ok := openaiUsage["completion_tokens"].(float64); ok {
		usage.OutputTokens = int(completionTokens)
	}

	return usage
}

// extractContentFromOpenAIMessage extracts content blocks from OpenAI message
func extractContentFromOpenAIMessage(msg map[string]interface{}) []ContentBlock {
	var blocks []ContentBlock

	// Check for reasoning_content (thinking)
	var reasoningContent string
	if rc, ok := msg["reasoning_content"].(string); ok && rc != "" {
		reasoningContent = rc
	}

	// Check for provider_specific_fields.reasoning_content
	if providerFields, ok := msg["provider_specific_fields"].(map[string]interface{}); ok {
		if rc, ok := providerFields["reasoning_content"].(string); ok && rc != "" && reasoningContent == "" {
			reasoningContent = rc
		}
	}

	// Add thinking block if reasoning_content exists
	if reasoningContent != "" {
		blocks = append(blocks, ContentBlock{
			Type:     "thinking",
			Thinking: reasoningContent,
		})
	}

	// Extract text content
	if content, ok := msg["content"].(string); ok && content != "" {
		blocks = append(blocks, ContentBlock{
			Type: "text",
			Text: content,
		})
	}

	// Extract tool calls if present
	if toolCalls, ok := msg["tool_calls"].([]interface{}); ok {
		for _, tc := range toolCalls {
			if tcMap, ok := tc.(map[string]interface{}); ok {
				toolUseBlock := ContentBlock{
					Type: "tool_use",
				}

				if id, ok := tcMap["id"].(string); ok {
					toolUseBlock.ID = id
				}

				// Handle function object for name and arguments
				if function, ok := tcMap["function"].(map[string]interface{}); ok {
					if name, ok := function["name"].(string); ok {
						toolUseBlock.Name = name
					}
					if args, ok := function["arguments"].(string); ok {
						toolUseBlock.Input = json.RawMessage(args)
					}
				}

				blocks = append(blocks, toolUseBlock)
			}
		}
	}

	// If no content at all, return empty text block
	if len(blocks) == 0 {
		blocks = append(blocks, ContentBlock{
			Type: "text",
			Text: "",
		})
	}

	return blocks
}
