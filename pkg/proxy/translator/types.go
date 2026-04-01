// Package translator provides protocol translation between Anthropic Messages API
// and OpenAI Chat Completions API formats.
package translator

import (
	"encoding/json"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// Anthropic Request Types
// ─────────────────────────────────────────────────────────────────────────────

// AnthropicRequest represents an Anthropic Messages API request.
type AnthropicRequest struct {
	Model         string                 `json:"model"`
	MaxTokens     int                    `json:"max_tokens"`
	System        interface{}            `json:"system,omitempty"` // string or []ContentBlock
	Messages      []AnthropicMessage     `json:"messages"`
	Stream        bool                   `json:"stream,omitempty"`
	Temperature   *float64               `json:"temperature,omitempty"`
	TopP          *float64               `json:"top_p,omitempty"`
	TopK          *int                   `json:"top_k,omitempty"`
	StopSequences []string               `json:"stop_sequences,omitempty"`
	Tools         []AnthropicTool        `json:"tools,omitempty"`
	ToolChoice    interface{}            `json:"tool_choice,omitempty"` // string or object
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
	Thinking      *ThinkingConfig        `json:"thinking,omitempty"`
}

// ThinkingConfig represents extended thinking configuration.
type ThinkingConfig struct {
	Type         string `json:"type"`          // "enabled"
	BudgetTokens int    `json:"budget_tokens"`
}

// AnthropicMessage represents a message in the Anthropic format.
type AnthropicMessage struct {
	Role    string      `json:"role"`    // "user" or "assistant"
	Content interface{} `json:"content"` // string or []ContentBlock
}

// ContentBlock represents a content block in Anthropic format.
type ContentBlock struct {
	Type string `json:"type"` // "text", "image", "tool_use", "tool_result", "thinking"

	// For type "text"
	Text string `json:"text,omitempty"`

	// For type "image"
	Source *ImageSource `json:"source,omitempty"`

	// For type "tool_use"
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// For type "tool_result"
	ToolUseID string      `json:"tool_use_id,omitempty"`
	Content   interface{} `json:"content,omitempty"` // string or []ContentBlock
	IsError   bool        `json:"is_error,omitempty"`

	// For type "thinking"
	Thinking string `json:"thinking,omitempty"`
}

// ImageSource represents the source of an image in Anthropic format.
type ImageSource struct {
	Type      string `json:"type"` // "base64" or "url"
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// AnthropicTool represents a tool definition in Anthropic format.
type AnthropicTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Anthropic Response Types
// ─────────────────────────────────────────────────────────────────────────────

// AnthropicResponse represents an Anthropic Messages API response.
type AnthropicResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"` // "message"
	Role         string         `json:"role"` // "assistant"
	Content      []ContentBlock `json:"content"`
	Model        string         `json:"model"`
	StopReason   *string        `json:"stop_reason,omitempty"`
	StopSequence *string        `json:"stop_sequence,omitempty"`
	Usage        UsageInfo      `json:"usage"`
}

// UsageInfo represents token usage information.
type UsageInfo struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Anthropic Streaming Event Types
// ─────────────────────────────────────────────────────────────────────────────

// StreamEventType represents the type of a streaming event.
type StreamEventType string

const (
	EventMessageStart      StreamEventType = "message_start"
	EventContentBlockStart StreamEventType = "content_block_start"
	EventContentBlockDelta StreamEventType = "content_block_delta"
	EventContentBlockStop  StreamEventType = "content_block_stop"
	EventMessageDelta      StreamEventType = "message_delta"
	EventMessageStop       StreamEventType = "message_stop"
	EventPing              StreamEventType = "ping"
	EventError             StreamEventType = "error"
)

// StreamEvent represents a generic streaming event.
type StreamEvent struct {
	Type StreamEventType `json:"type"`

	// For message_start
	Message *AnthropicResponse `json:"message,omitempty"`

	// For content_block_start
	Index        int           `json:"index,omitempty"`
	ContentBlock *ContentBlock `json:"content_block,omitempty"`

	// For content_block_delta
	Delta *ContentDelta `json:"delta,omitempty"`

	// For message_delta
	Usage *UsageInfo `json:"usage,omitempty"`

	// For error
	Error *AnthropicError `json:"error,omitempty"`
}

// ContentDelta represents a delta in a content block.
type ContentDelta struct {
	Type string `json:"type"` // "text_delta", "input_json_delta", "thinking_delta"

	// For type "text_delta"
	Text string `json:"text,omitempty"`

	// For type "input_json_delta"
	PartialJSON string `json:"partial_json,omitempty"`

	// For type "thinking_delta"
	Thinking string `json:"thinking,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Anthropic Error Types
// ─────────────────────────────────────────────────────────────────────────────

// AnthropicError represents an error in Anthropic format.
type AnthropicError struct {
	Type    string `json:"type"` // "invalid_request_error", "authentication_error", etc.
	Message string `json:"message"`
}

// AnthropicErrorResponse represents an error response in Anthropic format.
type AnthropicErrorResponse struct {
	Type  string         `json:"type"` // "error"
	Error AnthropicError `json:"error"`
}

// ─────────────────────────────────────────────────────────────────────────────
// OpenAI Types (for reference/translation)
// ─────────────────────────────────────────────────────────────────────────────

// OpenAIMessage represents a message in OpenAI format.
type OpenAIMessage struct {
	Role    string      `json:"role"`    // "system", "user", "assistant", "tool"
	Content interface{} `json:"content"` // string or []OpenAIContentPart
	Name    string      `json:"name,omitempty"`

	// For assistant messages with tool calls
	ToolCalls []OpenAIToolCall `json:"tool_calls,omitempty"`

	// For tool response messages
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// OpenAIContentPart represents a content part in OpenAI format.
type OpenAIContentPart struct {
	Type     string          `json:"type"` // "text" or "image_url"
	Text     string          `json:"text,omitempty"`
	ImageURL *OpenAIImageURL `json:"image_url,omitempty"`
}

// OpenAIImageURL represents an image URL in OpenAI format.
type OpenAIImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"` // "auto", "low", "high"
}

// OpenAIToolCall represents a tool call in OpenAI format.
type OpenAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"` // "function"
	Function OpenAIFunctionCall `json:"function"`
}

// OpenAIFunctionCall represents a function call.
type OpenAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// OpenAITool represents a tool definition in OpenAI format.
type OpenAITool struct {
	Type     string               `json:"type"` // "function"
	Function OpenAIFunctionSchema `json:"function"`
}

// OpenAIFunctionSchema represents a function schema.
type OpenAIFunctionSchema struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Stream State (for batch translation)
// ─────────────────────────────────────────────────────────────────────────────

// StreamState holds state for translating a buffered stream.
type StreamState struct {
	MessageID          string
	OriginalModel      string
	AccumulatedContent strings.Builder
	ThinkingContent    strings.Builder
	ToolCalls          []ToolCallState
	Usage              UsageInfo
	StopReason         string
}

// ToolCallState holds state for a tool call during streaming.
type ToolCallState struct {
	Index     int
	ID        string
	Name      string
	Arguments strings.Builder
}

// ─────────────────────────────────────────────────────────────────────────────
// Model Mapping Config
// ─────────────────────────────────────────────────────────────────────────────

// ModelMappingConfig holds configuration for mapping Anthropic models to OpenAI models.
type ModelMappingConfig struct {
	DefaultModel string            `json:"default_model,omitempty"`
	Mapping      map[string]string `json:"mapping,omitempty"`
}

// GetMappedModel returns the OpenAI model name for an Anthropic model.
func (c *ModelMappingConfig) GetMappedModel(anthropicModel string) string {
	if c == nil {
		return anthropicModel
	}
	if mapped, ok := c.Mapping[anthropicModel]; ok {
		return mapped
	}
	if c.DefaultModel != "" {
		return c.DefaultModel
	}
	return anthropicModel
}
