package providers

import (
	"context"
)

// Provider defines the interface for AI providers
type Provider interface {
	// Name returns the provider name (e.g., "openai", "anthropic")
	Name() string

	// ChatCompletion sends a non-streaming chat completion request
	ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error)

	// StreamChatCompletion sends a streaming chat completion request
	// Returns a channel of normalized StreamEvent
	StreamChatCompletion(ctx context.Context, req *ChatCompletionRequest) (<-chan StreamEvent, error)

	// IsRetryable returns true if the error should trigger a retry
	IsRetryable(err error) bool
}

// ChatCompletionRequest represents a chat completion request (OpenAI-compatible format)
type ChatCompletionRequest struct {
	Model            string                 `json:"model"`
	Messages         []ChatMessage          `json:"messages"`
	MaxTokens        *int                   `json:"max_tokens,omitempty"`
	Temperature      *float64               `json:"temperature,omitempty"`
	TopP             *float64               `json:"top_p,omitempty"`
	N                *int                   `json:"n,omitempty"`
	Stream           bool                   `json:"stream,omitempty"`
	Stop             interface{}            `json:"stop,omitempty"` // string or []string
	PresencePenalty  *float64               `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64               `json:"frequency_penalty,omitempty"`
	LogitBias        map[string]float64     `json:"logit_bias,omitempty"`
	User             string                 `json:"user,omitempty"`
	Tools            []Tool                 `json:"tools,omitempty"`
	ToolChoice       interface{}            `json:"tool_choice,omitempty"` // "none", "auto", "required", or specific tool
	Extra            map[string]interface{} `json:"-"`                     // Provider-specific extra fields
}

// ChatMessage represents a single message in a chat
type ChatMessage struct {
	Role       string      `json:"role"`
	Content    interface{} `json:"content"` // string or []ContentPart for multimodal
	Name       string      `json:"name,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"` // Required for tool role messages
}

// Tool represents a tool definition
type Tool struct {
	Type     string       `json:"type"` // "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction represents a function definition
type ToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

// ToolCall represents a tool call in a message
type ToolCall struct {
	Index    int              `json:"index,omitempty"` // Used in streaming deltas
	ID       string           `json:"id"`
	Type     string           `json:"type"` // "function"
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction represents the function details in a tool call
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ContentPart represents a content part in multimodal messages
type ContentPart struct {
	Type     string    `json:"type"` // "text" or "image_url"
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL represents an image URL in multimodal content
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"` // "auto", "low", "high"
}

// ChatCompletionResponse represents a chat completion response (OpenAI-compatible format)
type ChatCompletionResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice represents a completion choice
type Choice struct {
	Index        int          `json:"index"`
	Message      *ChatMessage `json:"message,omitempty"`
	Delta        *ChatMessage `json:"delta,omitempty"`
	FinishReason string       `json:"finish_reason"`
}

// Usage represents token usage statistics
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// StreamEvent represents a normalized streaming event
type StreamEvent struct {
	Type             string                  // "content", "tool_call", "thinking", "done", "error"
	Content          string                  // Text content delta
	ReasoningContent string                  // Reasoning content delta (DeepSeek-style)
	ToolCalls        []ToolCall              // Tool call deltas for "tool_call" event
	FinishReason     string                  // Finish reason if type is "done"
	Error            error                   // Error if type is "error"
	Response         *ChatCompletionResponse // Full response for "done" event
}

// ProviderError wraps provider-specific errors with retry information
type ProviderError struct {
	Provider   string
	StatusCode int
	Message    string
	Retryable  bool
	BufferID   string // Optional: ID of saved request buffer for debugging
}

func (e *ProviderError) Error() string {
	return e.Provider + ": " + e.Message
}

// IsRetryable implements Provider interface check
func (e *ProviderError) IsRetryable() bool {
	return e.Retryable
}
