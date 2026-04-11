package token

import (
	"bytes"
	"encoding/json"
	"strings"
)

// extractPromptText extracts the prompt text from an LLM API request body
func extractPromptText(body []byte) string {
	var req map[string]json.RawMessage
	if err := json.Unmarshal(body, &req); err != nil {
		return string(body)
	}

	var text string

	// Chat completions: messages array
	if messagesRaw, ok := req["messages"]; ok {
		var messages []map[string]interface{}
		if err := json.Unmarshal(messagesRaw, &messages); err == nil {
			for _, msg := range messages {
				// Handle string content
				if content, ok := msg["content"].(string); ok {
					text += content
				}
				// Handle array content (multimodal)
				if contentArr, ok := msg["content"].([]interface{}); ok {
					for _, item := range contentArr {
						if itemMap, ok := item.(map[string]interface{}); ok {
							if textContent, ok := itemMap["text"].(string); ok {
								text += textContent
							}
						}
					}
				}
			}
		}
	}

	// Legacy completions: prompt field
	if text == "" {
		if promptRaw, ok := req["prompt"]; ok {
			var prompt string
			if err := json.Unmarshal(promptRaw, &prompt); err == nil {
				text = prompt
			}
		}
	}

	return text
}

// ExtractCompletionTextFromChunks parses SSE data lines to get completion text
func ExtractCompletionTextFromChunks(data []byte) string {
	var result strings.Builder

	lines := bytes.Split(data, []byte("\n"))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}

		jsonPart := bytes.TrimPrefix(line, []byte("data: "))
		if bytes.Equal(jsonPart, []byte("[DONE]")) || len(jsonPart) == 0 {
			continue
		}

		var chunk map[string]interface{}
		if err := json.Unmarshal(jsonPart, &chunk); err != nil {
			continue
		}

		// Extract content from choices[0].delta.content
		if choices, ok := chunk["choices"].([]interface{}); ok && len(choices) > 0 {
			if choice, ok := choices[0].(map[string]interface{}); ok {
				if delta, ok := choice["delta"].(map[string]interface{}); ok {
					if content, ok := delta["content"].(string); ok {
						result.WriteString(content)
					}
				}
				// Also check message.content for non-streaming responses embedded in SSE
				if msg, ok := choice["message"].(map[string]interface{}); ok {
					if content, ok := msg["content"].(string); ok {
						result.WriteString(content)
					}
				}
			}
		}

		// Also check for Anthropic-style content_block_delta
		if eventType, ok := chunk["type"].(string); ok {
			if eventType == "content_block_delta" {
				if delta, ok := chunk["delta"].(map[string]interface{}); ok {
					if text, ok := delta["text"].(string); ok {
						result.WriteString(text)
					}
				}
			}
		}
	}

	return result.String()
}

// ExtractCompletionTextFromJSON extracts completion text from a non-streaming JSON response
func ExtractCompletionTextFromJSON(body []byte) string {
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return ""
	}

	// OpenAI format: choices[0].message.content
	if choices, ok := resp["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if msg, ok := choice["message"].(map[string]interface{}); ok {
				if content, ok := msg["content"].(string); ok {
					return content
				}
			}
		}
	}

	return ""
}
