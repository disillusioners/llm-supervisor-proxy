package translator

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Request Translation Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestTranslateRequest_Basic(t *testing.T) {
	anthropic := &AnthropicRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: 1024,
		Messages: []AnthropicMessage{
			{Role: "user", Content: "Hello"},
		},
	}

	modelMapping := &ModelMappingConfig{
		DefaultModel: "gpt-4o",
		Mapping: map[string]string{
			"claude-sonnet-4-5": "gpt-4o",
		},
	}

	result := TranslateRequest(anthropic, modelMapping)

	// Check model was mapped
	if result["model"] != "gpt-4o" {
		t.Errorf("expected model 'gpt-4o', got %v", result["model"])
	}

	// Check messages
	msgs, ok := result["messages"].([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T", result["messages"])
	}
	if len(msgs) != 1 {
		t.Errorf("expected 1 message, got %d", len(msgs))
	}
	msgMap, ok := msgs[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected map[string]interface{}, got %T", msgs[0])
	}
	if msgMap["role"] != "user" {
		t.Errorf("expected role 'user', got %v", msgMap["role"])
	}
	if msgMap["content"] != "Hello" {
		t.Errorf("expected content 'Hello', got %v", msgMap["content"])
	}

	// Check max_tokens
	if result["max_tokens"] != 1024 {
		t.Errorf("expected max_tokens 1024, got %v", result["max_tokens"])
	}
}

func TestTranslateRequest_WithSystem(t *testing.T) {
	anthropic := &AnthropicRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: 1024,
		System:    "You are a helpful assistant",
		Messages: []AnthropicMessage{
			{Role: "user", Content: "Hi"},
		},
	}

	result := TranslateRequest(anthropic, nil)

	msgs, ok := result["messages"].([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T", result["messages"])
	}

	// Should have system + user = 2 messages
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages, got %d", len(msgs))
	}

	// First message should be system
	sysMsg, ok := msgs[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", msgs[0])
	}
	if sysMsg["role"] != "system" {
		t.Errorf("expected first message role 'system', got %v", sysMsg["role"])
	}
	if sysMsg["content"] != "You are a helpful assistant" {
		t.Errorf("expected system content, got %v", sysMsg["content"])
	}
}

func TestTranslateRequest_WithModelMapping(t *testing.T) {
	anthropic := &AnthropicRequest{
		Model:     "claude-3-opus",
		MaxTokens: 1024,
		Messages:  []AnthropicMessage{{Role: "user", Content: "test"}},
	}

	modelMapping := &ModelMappingConfig{
		DefaultModel: "gpt-4o-mini",
		Mapping: map[string]string{
			"claude-3-opus": "gpt-4-turbo",
		},
	}

	result := TranslateRequest(anthropic, modelMapping)

	if result["model"] != "gpt-4-turbo" {
		t.Errorf("expected model 'gpt-4-turbo', got %v", result["model"])
	}

	// Test default model fallback
	anthropic.Model = "unknown-model"
	result = TranslateRequest(anthropic, modelMapping)
	if result["model"] != "gpt-4o-mini" {
		t.Errorf("expected default model 'gpt-4o-mini', got %v", result["model"])
	}
}

func TestTranslateRequest_Multimodal(t *testing.T) {
	anthropic := &AnthropicRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: 1024,
		Messages: []AnthropicMessage{
			{
				Role: "user",
				Content: []ContentBlock{
					{Type: "text", Text: "What's in this image?"},
					{
						Type: "image",
						Source: &ImageSource{
							Type:      "base64",
							MediaType: "image/png",
							Data:      "base64data",
						},
					},
				},
			},
		},
	}

	result := TranslateRequest(anthropic, nil)

	msgs, ok := result["messages"].([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T", result["messages"])
	}

	msgMap, ok := msgs[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", msgs[0])
	}

	// Content should be an array of parts
	contentParts, ok := msgMap["content"].([]interface{})
	if !ok {
		t.Fatalf("expected []interface{} for content, got %T", msgMap["content"])
	}

	if len(contentParts) != 2 {
		t.Errorf("expected 2 content parts, got %d", len(contentParts))
	}

	// Check second part is image_url
	imgPart, ok := contentParts[1].(map[string]interface{})
	if !ok {
		t.Fatalf("expected map for image part, got %T", contentParts[1])
	}
	if imgPart["type"] != "image_url" {
		t.Errorf("expected image_url type, got %v", imgPart["type"])
	}
	imgURL, ok := imgPart["image_url"].(map[string]interface{})
	if !ok {
		t.Fatal("expected image_url to be a map")
	}
	expectedURL := "data:image/png;base64,base64data"
	if imgURL["url"] != expectedURL {
		t.Errorf("expected URL '%s', got '%v'", expectedURL, imgURL["url"])
	}
}

func TestTranslateRequest_WithToolResult(t *testing.T) {
	// Test that tool_result blocks are properly translated to tool role messages
	anthropic := &AnthropicRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: 1024,
		Messages: []AnthropicMessage{
			{Role: "user", Content: "What's the weather?"},
			{Role: "assistant", Content: []ContentBlock{
				{Type: "text", Text: "Let me check."},
				{Type: "tool_use", ID: "toolu_123", Name: "get_weather", Input: json.RawMessage(`{"location":"SF"}`)},
			}},
			{
				Role: "user",
				Content: []ContentBlock{
					{Type: "tool_result", ToolUseID: "toolu_123", Content: "72°F sunny"},
				},
			},
		},
	}

	result := TranslateRequest(anthropic, nil)

	msgs, ok := result["messages"].([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T", result["messages"])
	}

	// Should have 3 messages:
	// 1. user: "What's the weather?"
	// 2. assistant with tool_calls
	// 3. tool: result for toolu_123
	if len(msgs) != 3 {
		t.Errorf("expected 3 messages, got %d", len(msgs))
		for i, m := range msgs {
			if mm, ok := m.(map[string]interface{}); ok {
				t.Logf("Message %d: role=%s, content=%v", i, mm["role"], mm["content"])
			}
		}
	}

	// Check first message (user)
	msg0, ok := msgs[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", msgs[0])
	}
	if msg0["role"] != "user" {
		t.Errorf("expected first message role 'user', got %v", msg0["role"])
	}

	// Check second message (assistant with tool_calls)
	msg1, ok := msgs[1].(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", msgs[1])
	}
	if msg1["role"] != "assistant" {
		t.Errorf("expected second message role 'assistant', got %v", msg1["role"])
	}

	// Verify tool_calls field exists and is correct format (not in content!)
	toolCalls, ok := msg1["tool_calls"].([]interface{})
	if !ok {
		t.Fatalf("expected tool_calls array, got %T", msg1["tool_calls"])
	}
	if len(toolCalls) != 1 {
		t.Errorf("expected 1 tool_call, got %d", len(toolCalls))
	}

	// Verify tool_call structure
	tc0, ok := toolCalls[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tool_call map, got %T", toolCalls[0])
	}
	if tc0["type"] != "function" {
		t.Errorf("expected tool_call type 'function', got %v", tc0["type"])
	}
	if tc0["id"] != "toolu_123" {
		t.Errorf("expected tool_call id 'toolu_123', got %v", tc0["id"])
	}

	// Verify function details
	func0, ok := tc0["function"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected function map, got %T", tc0["function"])
	}
	if func0["name"] != "get_weather" {
		t.Errorf("expected function name 'get_weather', got %v", func0["name"])
	}

	// Verify content is text only (not an array with tool_use)
	content1, ok := msg1["content"].(string)
	if !ok {
		t.Errorf("expected content to be string (text only), got %T", msg1["content"])
	}
	if content1 != "Let me check." {
		t.Errorf("expected content 'Let me check.', got %v", content1)
	}

	// Check third message (tool result)
	msg2, ok := msgs[2].(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", msgs[2])
	}
	if msg2["role"] != "tool" {
		t.Errorf("expected third message role 'tool', got %v", msg2["role"])
	}
	if msg2["tool_call_id"] != "toolu_123" {
		t.Errorf("expected tool_call_id 'toolu_123', got %v", msg2["tool_call_id"])
	}
	if msg2["content"] != "72°F sunny" {
		t.Errorf("expected content '72°F sunny', got %v", msg2["content"])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Response Translation Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestTranslateNonStreamResponse_Basic(t *testing.T) {
	openaiResp := map[string]interface{}{
		"id":      "chatcmpl-123",
		"object":  "chat.completion",
		"created": 1234567890,
		"model":   "gpt-4o",
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": "Hello! How can I help?",
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]interface{}{
			"prompt_tokens":     float64(10),
			"completion_tokens": float64(5),
		},
	}

	openaiBody, _ := json.Marshal(openaiResp)

	result, err := TranslateNonStreamResponse(openaiBody, "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("translation failed: %v", err)
	}

	var anthropicResp map[string]interface{}
	json.Unmarshal(result, &anthropicResp)

	// Check type
	if anthropicResp["type"] != "message" {
		t.Errorf("expected type 'message', got %v", anthropicResp["type"])
	}

	// Check role
	if anthropicResp["role"] != "assistant" {
		t.Errorf("expected role 'assistant', got %v", anthropicResp["role"])
	}

	// Check model (should be original)
	if anthropicResp["model"] != "claude-sonnet-4-5" {
		t.Errorf("expected model 'claude-sonnet-4-5', got %v", anthropicResp["model"])
	}

	// Check content
	content, ok := anthropicResp["content"].([]interface{})
	if !ok {
		t.Fatalf("expected content array, got %T", anthropicResp["content"])
	}
	if len(content) != 1 {
		t.Errorf("expected 1 content block, got %d", len(content))
	}

	// Check stop_reason
	if anthropicResp["stop_reason"] != "end_turn" {
		t.Errorf("expected stop_reason 'end_turn', got %v", anthropicResp["stop_reason"])
	}

	// Check ID format (should start with msg_)
	id, ok := anthropicResp["id"].(string)
	if !ok || !strings.HasPrefix(id, "msg_") {
		t.Errorf("expected ID starting with 'msg_', got %v", id)
	}
}

func TestTranslateNonStreamResponse_WithThinking(t *testing.T) {
	openaiResp := map[string]interface{}{
		"id":    "chatcmpl-123",
		"model": "gpt-4o",
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"message": map[string]interface{}{
					"role":              "assistant",
					"content":           "The answer is 42.",
					"reasoning_content": "Let me think about this...",
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]interface{}{},
	}

	openaiBody, _ := json.Marshal(openaiResp)

	result, err := TranslateNonStreamResponse(openaiBody, "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("translation failed: %v", err)
	}

	var anthropicResp map[string]interface{}
	json.Unmarshal(result, &anthropicResp)

	content, _ := anthropicResp["content"].([]interface{})

	// Should have thinking + text = 2 blocks
	if len(content) != 2 {
		t.Errorf("expected 2 content blocks (thinking + text), got %d", len(content))
	}

	// First should be thinking
	firstBlock, _ := content[0].(map[string]interface{})
	if firstBlock["type"] != "thinking" {
		t.Errorf("expected first block type 'thinking', got %v", firstBlock["type"])
	}

	// Second should be text
	secondBlock, _ := content[1].(map[string]interface{})
	if secondBlock["type"] != "text" {
		t.Errorf("expected second block type 'text', got %v", secondBlock["type"])
	}
}

func TestTranslateNonStreamResponse_WithToolCalls(t *testing.T) {
	openaiResp := map[string]interface{}{
		"id":    "chatcmpl-123",
		"model": "gpt-4o",
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": "Let me check that.",
					"tool_calls": []interface{}{
						map[string]interface{}{
							"id":   "call_123",
							"type": "function",
							"function": map[string]interface{}{
								"name":      "get_weather",
								"arguments": `{"location":"SF"}`,
							},
						},
					},
				},
				"finish_reason": "tool_calls",
			},
		},
		"usage": map[string]interface{}{},
	}

	openaiBody, _ := json.Marshal(openaiResp)

	result, err := TranslateNonStreamResponse(openaiBody, "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("translation failed: %v", err)
	}

	var anthropicResp map[string]interface{}
	json.Unmarshal(result, &anthropicResp)

	// Check stop_reason
	if anthropicResp["stop_reason"] != "tool_use" {
		t.Errorf("expected stop_reason 'tool_use', got %v", anthropicResp["stop_reason"])
	}

	content, _ := anthropicResp["content"].([]interface{})

	// Should have text + tool_use = 2 blocks
	if len(content) != 2 {
		t.Errorf("expected 2 content blocks, got %d", len(content))
	}

	// Second should be tool_use
	toolBlock, _ := content[1].(map[string]interface{})
	if toolBlock["type"] != "tool_use" {
		t.Errorf("expected tool_use type, got %v", toolBlock["type"])
	}
	if toolBlock["id"] != "call_123" {
		t.Errorf("expected id 'call_123', got %v", toolBlock["id"])
	}
	if toolBlock["name"] != "get_weather" {
		t.Errorf("expected name 'get_weather', got %v", toolBlock["name"])
	}
}

func TestTranslateNonStreamResponse_ArrayContent(t *testing.T) {
	// Test handling of content as array of parts (modern OpenAI format)
	openaiResp := map[string]interface{}{
		"id":    "chatcmpl-123",
		"model": "gpt-4o",
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"message": map[string]interface{}{
					"role": "assistant",
					"content": []interface{}{
						map[string]interface{}{
							"type": "text",
							"text": "Hello! How can I help you?",
						},
					},
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]interface{}{},
	}

	openaiBody, _ := json.Marshal(openaiResp)

	result, err := TranslateNonStreamResponse(openaiBody, "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("translation failed: %v", err)
	}

	var anthropicResp map[string]interface{}
	json.Unmarshal(result, &anthropicResp)

	content, ok := anthropicResp["content"].([]interface{})
	if !ok {
		t.Fatalf("expected content array, got %T", anthropicResp["content"])
	}

	// Should have 1 text block
	if len(content) != 1 {
		t.Errorf("expected 1 content block, got %d", len(content))
	}

	textBlock, ok := content[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected content block map, got %T", content[0])
	}

	if textBlock["type"] != "text" {
		t.Errorf("expected type 'text', got %v", textBlock["type"])
	}

	if textBlock["text"] != "Hello! How can I help you?" {
		t.Errorf("expected text 'Hello! How can I help you?', got %v", textBlock["text"])
	}
}

func TestTranslateNonStreamResponse_ArrayContentWithMultipleParts(t *testing.T) {
	// Test handling of content array with multiple text parts
	openaiResp := map[string]interface{}{
		"id":    "chatcmpl-123",
		"model": "gpt-4o",
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"message": map[string]interface{}{
					"role": "assistant",
					"content": []interface{}{
						map[string]interface{}{
							"type": "text",
							"text": "First paragraph.",
						},
						map[string]interface{}{
							"type": "text",
							"text": "Second paragraph.",
						},
					},
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]interface{}{},
	}

	openaiBody, _ := json.Marshal(openaiResp)

	result, err := TranslateNonStreamResponse(openaiBody, "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("translation failed: %v", err)
	}

	var anthropicResp map[string]interface{}
	json.Unmarshal(result, &anthropicResp)

	content, _ := anthropicResp["content"].([]interface{})

	// Should have 2 text blocks
	if len(content) != 2 {
		t.Errorf("expected 2 content blocks, got %d", len(content))
	}

	// Check both blocks
	firstBlock, _ := content[0].(map[string]interface{})
	if firstBlock["text"] != "First paragraph." {
		t.Errorf("expected first text 'First paragraph.', got %v", firstBlock["text"])
	}

	secondBlock, _ := content[1].(map[string]interface{})
	if secondBlock["text"] != "Second paragraph." {
		t.Errorf("expected second text 'Second paragraph.', got %v", secondBlock["text"])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Error Translation Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestTranslateError_Basic(t *testing.T) {
	openaiError := map[string]interface{}{
		"error": map[string]interface{}{
			"message": "Rate limit exceeded",
			"type":    "rate_limit_error",
		},
	}

	openaiBody, _ := json.Marshal(openaiError)

	result, err := TranslateError(openaiBody, 429)
	if err != nil {
		t.Fatalf("translation failed: %v", err)
	}

	var anthropicError map[string]interface{}
	json.Unmarshal(result, &anthropicError)

	if anthropicError["type"] != "error" {
		t.Errorf("expected type 'error', got %v", anthropicError["type"])
	}

	errObj, _ := anthropicError["error"].(map[string]interface{})
	if errObj["type"] != "rate_limit_error" {
		t.Errorf("expected error type 'rate_limit_error', got %v", errObj["type"])
	}
}

func TestTranslateError_StatusCodes(t *testing.T) {
	tests := []struct {
		statusCode   int
		expectedType string
	}{
		{400, "invalid_request_error"},
		{401, "authentication_error"},
		{403, "permission_error"},
		{404, "not_found_error"},
		{429, "rate_limit_error"},
		{500, "api_error"},
		{529, "overloaded_error"},
	}

	for _, tc := range tests {
		t.Run(string(rune(tc.statusCode)), func(t *testing.T) {
			errorType := mapStatusCodeToErrorType(tc.statusCode)
			if errorType != tc.expectedType {
				t.Errorf("statusCode %d: expected '%s', got '%s'", tc.statusCode, tc.expectedType, errorType)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Stream Translation Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestTranslateBufferedStream_Basic(t *testing.T) {
	// Simulate OpenAI SSE chunks
	openaiBuffer := `data: {"id":"chatcmpl-123","choices":[{"delta":{"role":"assistant"},"index":0}]}

data: {"id":"chatcmpl-123","choices":[{"delta":{"content":"Hello"},"index":0}]}

data: {"id":"chatcmpl-123","choices":[{"delta":{"content":"!"},"index":0}]}

data: {"id":"chatcmpl-123","choices":[{"delta":{},"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2}}

data: [DONE]

`

	result, err := TranslateBufferedStream([]byte(openaiBuffer), "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("translation failed: %v", err)
	}

	resultStr := string(result)

	// Check for required events
	if !strings.Contains(resultStr, "event: message_start") {
		t.Error("expected message_start event")
	}
	if !strings.Contains(resultStr, "event: content_block_start") {
		t.Error("expected content_block_start event")
	}
	if !strings.Contains(resultStr, "event: content_block_delta") {
		t.Error("expected content_block_delta event")
	}
	if !strings.Contains(resultStr, "event: content_block_stop") {
		t.Error("expected content_block_stop event")
	}
	if !strings.Contains(resultStr, "event: message_delta") {
		t.Error("expected message_delta event")
	}
	if !strings.Contains(resultStr, "event: message_stop") {
		t.Error("expected message_stop event")
	}

	// Check for accumulated content
	if !strings.Contains(resultStr, "Hello!") {
		t.Error("expected 'Hello!' in content")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tool Translation Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestTranslateTools(t *testing.T) {
	anthropicTools := []AnthropicTool{
		{
			Name:        "get_weather",
			Description: "Get weather info",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"location": map[string]interface{}{"type": "string"},
				},
			},
		},
	}

	result := TranslateTools(anthropicTools)

	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}

	if result[0].Type != "function" {
		t.Errorf("expected type 'function', got %s", result[0].Type)
	}
	if result[0].Function.Name != "get_weather" {
		t.Errorf("expected name 'get_weather', got %s", result[0].Function.Name)
	}
}

func TestTranslateToolResult(t *testing.T) {
	block := ContentBlock{
		Type:      "tool_result",
		ToolUseID: "toolu_123",
		Content:   "72°F sunny",
	}

	result := TranslateToolResult(block)

	if result.Role != "tool" {
		t.Errorf("expected role 'tool', got %s", result.Role)
	}
	if result.ToolCallID != "toolu_123" {
		t.Errorf("expected tool_call_id 'toolu_123', got %s", result.ToolCallID)
	}
	if result.Content != "72°F sunny" {
		t.Errorf("expected content '72°F sunny', got %v", result.Content)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Model Mapping Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestModelMappingConfig_GetMappedModel(t *testing.T) {
	config := &ModelMappingConfig{
		DefaultModel: "gpt-4o-mini",
		Mapping: map[string]string{
			"claude-sonnet-4-5": "gpt-4o",
			"claude-3-opus":     "gpt-4-turbo",
		},
	}

	// Test mapped model
	if result := config.GetMappedModel("claude-sonnet-4-5"); result != "gpt-4o" {
		t.Errorf("expected 'gpt-4o', got '%s'", result)
	}

	// Test unmapped model (should use default)
	if result := config.GetMappedModel("unknown-model"); result != "gpt-4o-mini" {
		t.Errorf("expected default 'gpt-4o-mini', got '%s'", result)
	}

	// Test nil config
	var nilConfig *ModelMappingConfig
	if result := nilConfig.GetMappedModel("any-model"); result != "any-model" {
		t.Errorf("expected original model, got '%s'", result)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tool Choice Translation Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestTranslateOpenAIToolChoiceToAnthropic(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected interface{}
	}{
		{
			name:     "nil returns nil",
			input:    nil,
			expected: nil,
		},
		{
			name:     "auto maps to auto",
			input:    "auto",
			expected: "auto",
		},
		{
			name:     "none maps to auto",
			input:    "none",
			expected: "auto",
		},
		{
			name:     "required maps to any",
			input:    "required",
			expected: "any",
		},
		{
			name:     "any maps to any",
			input:    "any",
			expected: "any",
		},
		{
			name:     "unknown defaults to auto",
			input:    "unknown",
			expected: "auto",
		},
		{
			name: "function object translates to tool object",
			input: map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name": "get_weather",
				},
			},
			expected: map[string]interface{}{
				"type": "tool",
				"name": "get_weather",
			},
		},
		{
			name: "non-function object returns nil",
			input: map[string]interface{}{
				"type": "other",
				"name": "get_weather",
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TranslateOpenAIToolChoiceToAnthropic(tt.input)

			// Special handling for map comparison
			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}

			// For maps, compare as JSON
			if expectedMap, ok := tt.expected.(map[string]interface{}); ok {
				resultMap, ok := result.(map[string]interface{})
				if !ok {
					t.Errorf("expected map, got %T", result)
					return
				}
				if expectedMap["type"] != resultMap["type"] || expectedMap["name"] != resultMap["name"] {
					t.Errorf("expected %v, got %v", tt.expected, result)
				}
				return
			}

			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestTranslateToolChoice(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected interface{}
	}{
		{
			name:     "nil returns nil",
			input:    nil,
			expected: nil,
		},
		{
			name:     "auto wraps as type object",
			input:    "auto",
			expected: map[string]string{"type": "auto"},
		},
		{
			name:     "any wraps as type object",
			input:    "any",
			expected: map[string]string{"type": "any"},
		},
		{
			name: "tool object preserves name in function",
			input: map[string]interface{}{
				"type": "tool",
				"name": "get_weather",
			},
			expected: map[string]interface{}{
				"type": "tool",
				"function": map[string]string{
					"name": "get_weather",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TranslateToolChoice(tt.input)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}

			// For maps, compare as JSON
			if expectedMap, ok := tt.expected.(map[string]interface{}); ok {
				resultMap, ok := result.(map[string]interface{})
				if !ok {
					t.Errorf("expected map, got %T", result)
					return
				}
				if expectedMap["type"] != resultMap["type"] {
					t.Errorf("expected type %v, got %v", expectedMap["type"], resultMap["type"])
				}
				if fn, ok := expectedMap["function"]; ok {
					resultFn, ok := resultMap["function"].(map[string]string)
					expectedFn := fn.(map[string]string)
					if !ok || resultFn["name"] != expectedFn["name"] {
						t.Errorf("expected function %v, got %v", fn, resultMap["function"])
					}
				}
				return
			}

			// For string maps
			if expectedStrMap, ok := tt.expected.(map[string]string); ok {
				resultStrMap, ok := result.(map[string]string)
				if !ok {
					t.Errorf("expected map[string]string, got %T", result)
					return
				}
				if expectedStrMap["type"] != resultStrMap["type"] {
					t.Errorf("expected type %v, got %v", expectedStrMap["type"], resultStrMap["type"])
				}
				return
			}

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}
