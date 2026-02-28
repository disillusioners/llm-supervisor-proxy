// Package translator provides protocol translation between Anthropic Messages API
// and OpenAI Chat Completions API formats.
package translator

import (
	"encoding/json"
	"fmt"
)

// TranslateRequest translates an Anthropic request to OpenAI format.
// It converts the Anthropic Messages API request structure to OpenAI
// Chat Completions API format.
func TranslateRequest(anthropic *AnthropicRequest, modelMapping *ModelMappingConfig) map[string]interface{} {
	result := make(map[string]interface{})

	// 1. Model mapping
	if modelMapping != nil {
		result["model"] = modelMapping.GetMappedModel(anthropic.Model)
	} else {
		result["model"] = anthropic.Model
	}

	// 2. System message - insert as first message with role: "system"
	if anthropic.System != nil {
		systemContent := translateSystem(anthropic.System)
		if systemContent != "" {
			result["messages"] = append([]interface{}{
				map[string]interface{}{
					"role":    "system",
					"content": systemContent,
				},
			}, translateMessages(anthropic.Messages)...)
		} else {
			result["messages"] = translateMessages(anthropic.Messages)
		}
	} else {
		result["messages"] = translateMessages(anthropic.Messages)
	}

	// 3. Parameters - direct mapping
	if anthropic.MaxTokens > 0 {
		result["max_tokens"] = anthropic.MaxTokens
	}

	if anthropic.Temperature != nil {
		result["temperature"] = *anthropic.Temperature
	}

	if anthropic.TopP != nil {
		result["top_p"] = *anthropic.TopP
	}

	// stop_sequences -> stop
	if len(anthropic.StopSequences) > 0 {
		result["stop"] = anthropic.StopSequences
	}

	// stream
	result["stream"] = anthropic.Stream

	// 4. Tools translation - use helper from tools.go
	if len(anthropic.Tools) > 0 {
		tools := TranslateTools(anthropic.Tools)
		// Convert []OpenAITool to []map[string]interface{}
		toolsMap := make([]map[string]interface{}, len(tools))
		for i, t := range tools {
			toolsMap[i] = map[string]interface{}{
				"type": t.Type,
				"function": map[string]interface{}{
					"name":        t.Function.Name,
					"description": t.Function.Description,
					"parameters":  t.Function.Parameters,
				},
			}
		}
		result["tools"] = toolsMap
	}

	// 5. Tool choice (if present) - use helper from tools.go
	if anthropic.ToolChoice != nil {
		result["tool_choice"] = TranslateToolChoice(anthropic.ToolChoice)
	}

	return result
}

// translateMessages translates Anthropic messages to OpenAI format.
func translateMessages(messages []AnthropicMessage) []interface{} {
	result := make([]interface{}, len(messages))
	for i, msg := range messages {
		result[i] = translateMessage(msg)
	}
	return result
}

// translateMessage translates a single Anthropic message to OpenAI format.
func translateMessage(msg AnthropicMessage) map[string]interface{} {
	openAIMsg := map[string]interface{}{
		"role": msg.Role,
	}

	// Handle content
	content := translateContent(msg.Content)
	openAIMsg["content"] = content

	// If role is tool, also add tool_call_id
	if msg.Role == "tool" {
		if toolCallID, ok := msg.Content.(string); ok {
			openAIMsg["tool_call_id"] = toolCallID
		}
	}

	return openAIMsg
}

// translateContent translates Anthropic content to OpenAI format.
// Content can be a string or []ContentBlock.
func translateContent(content interface{}) interface{} {
	if content == nil {
		return nil
	}

	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		// Array of content blocks
		result := make([]interface{}, 0, len(c))
		for _, block := range c {
			if translated := translateContentBlock(block); translated != nil {
				result = append(result, translated)
			}
		}
		if len(result) == 1 {
			// If only one block and it's text, return as string
			if textBlock, ok := result[0].(string); ok {
				return textBlock
			}
		}
		return result
	case []ContentBlock:
		result := make([]interface{}, 0, len(c))
		for _, block := range c {
			if translated := translateContentBlock(block); translated != nil {
				result = append(result, translated)
			}
		}
		if len(result) == 1 {
			if textBlock, ok := result[0].(string); ok {
				return textBlock
			}
		}
		return result
	default:
		return fmt.Sprintf("%v", c)
	}
}

// translateContentBlock translates a single content block from Anthropic to OpenAI format.
func translateContentBlock(block interface{}) interface{} {
	// Handle the case where block is a map[string]interface{}
	var blockType string
	var text string
	var source *ImageSource
	var id, name string
	var input json.RawMessage
	var toolUseID string
	var toolContent interface{}
	var isError bool

	switch b := block.(type) {
	case map[string]interface{}:
		blockType, _ = b["type"].(string)
		text, _ = b["text"].(string)
		if src, ok := b["source"].(map[string]interface{}); ok {
			source = &ImageSource{}
			if t, ok := src["type"].(string); ok {
				source.Type = t
			}
			if mt, ok := src["media_type"].(string); ok {
				source.MediaType = mt
			}
			if d, ok := src["data"].(string); ok {
				source.Data = d
			}
			if u, ok := src["url"].(string); ok {
				source.URL = u
			}
		}
		id, _ = b["id"].(string)
		name, _ = b["name"].(string)
		if inp, ok := b["input"].(string); ok {
			input = json.RawMessage(inp)
		} else if inpMap, ok := b["input"].(map[string]interface{}); ok {
			inpBytes, _ := json.Marshal(inpMap)
			input = json.RawMessage(inpBytes)
		}
		toolUseID, _ = b["tool_use_id"].(string)
		toolContent = b["content"]
		if err, ok := b["is_error"].(bool); ok {
			isError = err
		}

	case ContentBlock:
		blockType = b.Type
		text = b.Text
		source = b.Source
		id = b.ID
		name = b.Name
		input = b.Input
		toolUseID = b.ToolUseID
		toolContent = b.Content
		isError = b.IsError

	default:
		return nil
	}

	switch blockType {
	case "text":
		// Anthropic: {"type": "text", "text": "..."}
		// OpenAI: "..." (string) or {"type": "text", "text": "..."}
		// For simplicity, return as string for text blocks
		return text

	case "image":
		// Anthropic:
		// {"type":"image","source":{"type":"base64","media_type":"image/png","data":"..."}}
		// OpenAI:
		// {"type":"image_url","image_url":{"url":"data:image/png;base64,..."}}
		if source == nil {
			return nil
		}
		return translateImageSource(source)

	case "tool_use":
		// Anthropic tool_use -> OpenAI tool_calls
		// Use helper from tools.go
		return translateToolUseInMessage(ContentBlock{
			Type:  blockType,
			ID:    id,
			Name:  name,
			Input: input,
		})

	case "tool_result":
		// Anthropic tool_result -> OpenAI role: "tool"
		// Use helper from tools.go
		return translateToolResultInMessage(ContentBlock{
			Type:      blockType,
			ToolUseID: toolUseID,
			Content:   toolContent,
			IsError:   isError,
		})

	case "thinking":
		// Anthropic thinking block - skip in OpenAI request
		return nil

	default:
		return nil
	}
}

// translateToolUseInMessage translates Anthropic tool_use to OpenAI tool_calls format for message content.
func translateToolUseInMessage(toolUse ContentBlock) map[string]interface{} {
	// OpenAI tool_calls format in message:
	// {
	//   "type": "function",
	//   "id": "...",
	//   "function": {
	//     "name": "...",
	//     "arguments": "{\"key\": \"value\"}"
	//   }
	// }

	args := "{}"
	if len(toolUse.Input) > 0 {
		args = string(toolUse.Input)
	}

	return map[string]interface{}{
		"type": "function",
		"id":   toolUse.ID,
		"function": map[string]interface{}{
			"name":      toolUse.Name,
			"arguments": args,
		},
	}
}

// translateToolResultInMessage translates Anthropic tool_result to OpenAI tool message format.
func translateToolResultInMessage(toolResult ContentBlock) map[string]interface{} {
	// OpenAI tool result format:
	// {
	//   "role": "tool",
	//   "tool_call_id": "...",
	//   "content": "..."
	// }

	var content string
	switch c := toolResult.Content.(type) {
	case string:
		content = c
	case []interface{}:
		// Array of content blocks - extract text
		for _, item := range c {
			if textBlock, ok := item.(map[string]interface{}); ok {
				if text, ok := textBlock["text"].(string); ok {
					content += text
				}
			}
		}
	case []ContentBlock:
		for _, block := range c {
			content += block.Text
		}
	default:
		content = fmt.Sprintf("%v", toolResult.Content)
	}

	if toolResult.IsError {
		content = "Error: " + content
	}

	return map[string]interface{}{
		"role":         "tool",
		"tool_call_id": toolResult.ToolUseID,
		"content":      content,
	}
}

// translateImageSource translates Anthropic image source to OpenAI image_url format.
func translateImageSource(source *ImageSource) map[string]interface{} {
	if source == nil {
		return nil
	}

	var url string
	switch source.Type {
	case "base64":
		// Format: data:{media_type};base64,{data}
		mediaType := source.MediaType
		if mediaType == "" {
			mediaType = "image/png"
		}
		url = fmt.Sprintf("data:%s;base64,%s", mediaType, source.Data)
	case "url":
		url = source.URL
	default:
		url = source.URL
	}

	return map[string]interface{}{
		"type": "image_url",
		"image_url": map[string]interface{}{
			"url":    url,
			"detail": "auto",
		},
	}
}

// translateSystem extracts system prompt from Anthropic format.
// Handles both string and array formats.
func translateSystem(system interface{}) string {
	if system == nil {
		return ""
	}

	switch s := system.(type) {
	case string:
		return s

	case []interface{}:
		// Array of content blocks - flatten to single string
		var result string
		for _, item := range s {
			switch item := item.(type) {
			case string:
				result += item
			case map[string]interface{}:
				if text, ok := item["text"].(string); ok {
					result += text
				}
			case ContentBlock:
				result += item.Text
			}
		}
		return result

	case []ContentBlock:
		var result string
		for _, block := range s {
			result += block.Text
		}
		return result

	default:
		return fmt.Sprintf("%v", system)
	}
}
