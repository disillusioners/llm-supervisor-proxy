// Package translator provides protocol translation between Anthropic Messages API
// and OpenAI Chat Completions API formats.
package translator

import (
	"encoding/json"
	"fmt"
	"strings"
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
		systemContent := TranslateSystem(anthropic.System)
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
// Note: A single Anthropic message with tool_result blocks may expand to multiple OpenAI messages.
func translateMessages(messages []AnthropicMessage) []interface{} {
	var result []interface{}
	for _, msg := range messages {
		translated := translateMessage(msg)
		result = append(result, translated...)
	}
	return result
}

// translateMessage translates a single Anthropic message to OpenAI format.
// Returns a slice because Anthropic messages with tool_result blocks become multiple OpenAI messages.
func translateMessage(msg AnthropicMessage) []interface{} {
	// First, check if this message contains tool_result blocks
	toolResults := extractToolResults(msg.Content)

	// If there are tool results, we need to handle them specially
	if len(toolResults) > 0 {
		var result []interface{}

		// Add tool result messages
		for _, tr := range toolResults {
			result = append(result, map[string]interface{}{
				"role":         "tool",
				"tool_call_id": tr.ToolUseID,
				"content":      tr.Content,
			})
		}

		// If there's other content (text), add it as a user message
		otherContent := extractNonToolResultContent(msg.Content)
		if otherContent != "" {
			result = append([]interface{}{map[string]interface{}{
				"role":    msg.Role,
				"content": otherContent,
			}}, result...)
		}

		return result
	}

	// Check if this is an assistant message with tool_use blocks
	// In OpenAI format, tool_calls must be a separate field, not in content
	if msg.Role == "assistant" {
		toolUses := extractToolUses(msg.Content)
		if len(toolUses) > 0 {
			// Extract text content only (no tool_use in content)
			textContent := extractTextContentOnly(msg.Content)

			openaiMsg := map[string]interface{}{
				"role":    "assistant",
				"content": textContent,
			}

			// Add tool_calls field
			var toolCalls []interface{}
			for _, tu := range toolUses {
				toolCalls = append(toolCalls, map[string]interface{}{
					"type": "function",
					"id":   tu.ID,
					"function": map[string]interface{}{
						"name":      tu.Name,
						"arguments": tu.Input,
					},
				})
			}
			openaiMsg["tool_calls"] = toolCalls

			return []interface{}{openaiMsg}
		}
	}

	// No tool results - standard translation
	return []interface{}{
		map[string]interface{}{
			"role":    msg.Role,
			"content": translateContent(msg.Content),
		},
	}
}

// translateContent translates Anthropic content to OpenAI format.
// Content can be a string or []ContentBlock.
// Returns an array for multimodal content, string for text-only content.
func translateContent(content interface{}) interface{} {
	if content == nil {
		return nil
	}

	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		// Check if we have multimodal content (images, etc.)
		hasMultimodal := false
		for _, block := range c {
			if bm, ok := block.(map[string]interface{}); ok {
				if blockType, ok := bm["type"].(string); ok {
					// Check for non-text content types (skip tool_result as it's handled separately)
					if blockType != "text" && blockType != "tool_result" && blockType != "thinking" {
						hasMultimodal = true
						break
					}
				}
			}
		}

		if hasMultimodal {
			// Return array of content parts for multimodal
			var result []interface{}
			for _, block := range c {
				if translated := translateContentBlock(block); translated != nil {
					// Skip tool_result blocks - they're handled separately
					if bm, ok := block.(map[string]interface{}); ok {
						if blockType, ok := bm["type"].(string); ok && blockType == "tool_result" {
							continue
						}
					}
					result = append(result, translated)
				}
			}
			return result
		}

		// Text-only content - flatten to string
		var sb strings.Builder
		for _, block := range c {
			if translated := translateContentBlock(block); translated != nil {
				if textObj, ok := translated.(map[string]interface{}); ok {
					if textObj["type"] == "text" {
						if text, ok := textObj["text"].(string); ok {
							sb.WriteString(text)
						}
					}
				}
			}
		}
		return sb.String()

	case []ContentBlock:
		// Check if we have multimodal content (images, etc.)
		hasMultimodal := false
		for _, block := range c {
			// Check for non-text content types (skip tool_result as it's handled separately)
			if block.Type != "text" && block.Type != "tool_result" && block.Type != "thinking" {
				hasMultimodal = true
				break
			}
		}

		if hasMultimodal {
			// Return array of content parts for multimodal
			var result []interface{}
			for _, block := range c {
				// Skip tool_result blocks - they're handled separately
				if block.Type == "tool_result" {
					continue
				}
				if translated := translateContentBlock(block); translated != nil {
					result = append(result, translated)
				}
			}
			return result
		}

		// Text-only content - flatten to string
		var sb strings.Builder
		for _, block := range c {
			if block.Type == "tool_result" {
				continue
			}
			if translated := translateContentBlock(block); translated != nil {
				if textObj, ok := translated.(map[string]interface{}); ok {
					if textObj["type"] == "text" {
						if text, ok := textObj["text"].(string); ok {
							sb.WriteString(text)
						}
					}
				}
			}
		}
		return sb.String()

	default:
		return fmt.Sprintf("%v", c)
	}
}

// toolResultInfo holds extracted tool result information
type toolResultInfo struct {
	ToolUseID string
	Content   string
}

// toolUseInfo holds extracted tool use information
type toolUseInfo struct {
	ID    string
	Name  string
	Input string
}

// extractToolResults extracts all tool_result blocks from content
func extractToolResults(content interface{}) []toolResultInfo {
	var results []toolResultInfo

	switch c := content.(type) {
	case []interface{}:
		for _, block := range c {
			if bm, ok := block.(map[string]interface{}); ok {
				if blockType, ok := bm["type"].(string); ok && blockType == "tool_result" {
					tr := toolResultInfo{}
					tr.ToolUseID, _ = bm["tool_use_id"].(string)

					// Extract content
					if contentStr, ok := bm["content"].(string); ok {
						tr.Content = contentStr
					} else if contentArr, ok := bm["content"].([]interface{}); ok {
						// Content can be array of text blocks
						var sb strings.Builder
						for _, item := range contentArr {
							if itemMap, ok := item.(map[string]interface{}); ok {
								if text, ok := itemMap["text"].(string); ok {
									sb.WriteString(text)
								}
							}
						}
						tr.Content = sb.String()
					}
					results = append(results, tr)
				}
			}
		}
	case []ContentBlock:
		for _, block := range c {
			if block.Type == "tool_result" {
				tr := toolResultInfo{
					ToolUseID: block.ToolUseID,
				}
				switch cont := block.Content.(type) {
				case string:
					tr.Content = cont
				case []interface{}:
					var sb strings.Builder
					for _, item := range cont {
						if itemMap, ok := item.(map[string]interface{}); ok {
							if text, ok := itemMap["text"].(string); ok {
								sb.WriteString(text)
							}
						}
					}
					tr.Content = sb.String()
				}
				results = append(results, tr)
			}
		}
	}

	return results
}

// extractNonToolResultContent extracts text content that is NOT tool_result
func extractNonToolResultContent(content interface{}) string {
	var sb strings.Builder

	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		for _, block := range c {
			if bm, ok := block.(map[string]interface{}); ok {
				if blockType, ok := bm["type"].(string); ok {
					// Only extract text blocks, skip tool_result
					if blockType == "text" {
						if text, ok := bm["text"].(string); ok {
							sb.WriteString(text)
						}
					}
				}
			}
		}
	case []ContentBlock:
		for _, block := range c {
			if block.Type == "text" {
				sb.WriteString(block.Text)
			}
		}
	}

	return sb.String()
}

// extractToolUses extracts all tool_use blocks from content
func extractToolUses(content interface{}) []toolUseInfo {
	var results []toolUseInfo

	switch c := content.(type) {
	case []interface{}:
		for _, block := range c {
			if bm, ok := block.(map[string]interface{}); ok {
				if blockType, ok := bm["type"].(string); ok && blockType == "tool_use" {
					tu := toolUseInfo{}
					tu.ID, _ = bm["id"].(string)
					tu.Name, _ = bm["name"].(string)
					// Input can be string or object
					if inputStr, ok := bm["input"].(string); ok {
						tu.Input = inputStr
					} else if inputMap, ok := bm["input"].(map[string]interface{}); ok {
						if bytes, err := json.Marshal(inputMap); err == nil {
							tu.Input = string(bytes)
						}
					}
					if tu.Input == "" {
						tu.Input = "{}"
					}
					results = append(results, tu)
				}
			}
		}
	case []ContentBlock:
		for _, block := range c {
			if block.Type == "tool_use" {
				input := "{}"
				if len(block.Input) > 0 {
					input = string(block.Input)
				}
				results = append(results, toolUseInfo{
					ID:    block.ID,
					Name:  block.Name,
					Input: input,
				})
			}
		}
	}

	return results
}

// extractTextContentOnly extracts only text content, excluding tool_use and tool_result
func extractTextContentOnly(content interface{}) string {
	var sb strings.Builder

	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		for _, block := range c {
			if bm, ok := block.(map[string]interface{}); ok {
				if blockType, ok := bm["type"].(string); ok && blockType == "text" {
					if text, ok := bm["text"].(string); ok {
						sb.WriteString(text)
					}
				}
			}
		}
	case []ContentBlock:
		for _, block := range c {
			if block.Type == "text" {
				sb.WriteString(block.Text)
			}
		}
	}

	return sb.String()
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
		// OpenAI: {"type": "text", "text": "..."} or just string
		// Return as OpenAI content part object for consistency in arrays
		return map[string]interface{}{
			"type": "text",
			"text": text,
		}

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
		var sb strings.Builder
		for _, item := range c {
			if textBlock, ok := item.(map[string]interface{}); ok {
				if text, ok := textBlock["text"].(string); ok {
					sb.WriteString(text)
				}
			}
		}
		content = sb.String()
	case []ContentBlock:
		var sb strings.Builder
		for _, block := range c {
			sb.WriteString(block.Text)
		}
		content = sb.String()
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

// TranslateSystem extracts system prompt from Anthropic format.
// Handles both string and array formats.
func TranslateSystem(system interface{}) string {
	if system == nil {
		return ""
	}

	switch s := system.(type) {
	case string:
		return s

	case []interface{}:
		// Array of content blocks - flatten to single string
		var sb strings.Builder
		for _, item := range s {
			switch item := item.(type) {
			case string:
				sb.WriteString(item)
			case map[string]interface{}:
				if text, ok := item["text"].(string); ok {
					sb.WriteString(text)
				}
			case ContentBlock:
				sb.WriteString(item.Text)
			}
		}
		return sb.String()

	case []ContentBlock:
		var sb strings.Builder
		for _, block := range s {
			sb.WriteString(block.Text)
		}
		return sb.String()

	default:
		return fmt.Sprintf("%v", system)
	}
}
