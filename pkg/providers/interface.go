package providers

import (
	"context"
	"errors"
	"strings"
	"time"
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
	Stream           bool                   `json:"stream"`
	Stop             interface{}            `json:"stop,omitempty"` // string or []string
	PresencePenalty  *float64               `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64               `json:"frequency_penalty,omitempty"`
	LogitBias        map[string]float64     `json:"logit_bias,omitempty"`
	User             string                 `json:"user,omitempty"`
	Tools            []Tool                 `json:"tools,omitempty"`
	ToolChoice       interface{}            `json:"tool_choice,omitempty"` // "none", "auto", "required", or specific tool
	Extra            map[string]interface{} `json:"-"`                     // Provider-specific extra fields

	// ReasoningSplit is the typed transport for the MiniMax top-level
	// reasoning_split flag (D1). Pointer type so absent-vs-false stays
	// distinguishable; omitempty drops the wire key when nil.
	ReasoningSplit *bool `json:"reasoning_split,omitempty"`
}

// ReasoningDetailEntry mirrors the wire shape of a single MiniMax
// reasoning_details entry on internal request paths. It is defined here
// (not imported from pkg/proxy/translator) because R3 forbids pkg/providers
// from depending on pkg/proxy/translator types — the translator→providers
// direction is the allowed one. The boundary code (convertRequest,
// extractReasoningContent) is responsible for translating between this
// local type and the translator's ReasoningDetail when needed.
type ReasoningDetailEntry struct {
	Type   string `json:"type,omitempty"`
	ID     string `json:"id,omitempty"`
	Format string `json:"format,omitempty"`
	Index  int    `json:"index,omitempty"`
	Text   string `json:"text,omitempty"`
}

// ChatMessage represents a single message in a chat
type ChatMessage struct {
	Role             string      `json:"role"`
	Content          interface{} `json:"content"` // string or []ContentPart for multimodal
	Name             string      `json:"name,omitempty"`
	ToolCalls        []ToolCall  `json:"tool_calls,omitempty"`
	ToolCallID       string      `json:"tool_call_id,omitempty"`      // Required for tool role messages
	ReasoningContent string      `json:"reasoning_content,omitempty"` // For DeepSeek R1-style thinking models

	// ReasoningDetails carries MiniMax-format per-message reasoning_details
	// on request-side map→struct hydration (D1). It is intentionally NOT
	// exported on StreamEvent (D2: openai.go's stream parser emits one
	// thinking StreamEvent per entry directly, making the field dead surface
	// on the stream type). omitempty drops the key when nil.
	ReasoningDetails []ReasoningDetailEntry `json:"reasoning_details,omitempty"`
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

// ProviderError wraps provider-specific errors with retry information.
//
// Round 3 (R3-1) ADDITIVE extension — RetryAfter + ErrorType + ErrorCode
// (Round 3c — W2). No existing field is renamed, removed, or reordered;
// the new fields are appended at the tail so existing error-comparison
// code paths and tests keep compiling.
//
// RetryAfter is captured from the Retry-After response header on the
// 429 path (openai.go:handleError wire-up) so ExcludeAndReselect can
// seed the per-credential cooldown. Zero when the header is absent
// or unparseable; the F.2 caller treats RetryAfter=0 as the "caller
// applies default cooldown (60s)" signal — NEVER "no cooldown".
//
// ErrorType + ErrorCode mirror the unmarshaled anonymous-local
// {Error:{Message,Type,Code}} body shape (openai.go:handleError wire-up).
// They make the Round-3b classifier matrix row 11 (503 + rate-limit
// body) implementable and the W1 /v1/messages anthropic-passthrough
// classifier readable without re-parsing raw response bodies on the
// hook site.
type ProviderError struct {
	Provider   string
	StatusCode int
	Message    string
	Retryable  bool
	BufferID   string // Optional: ID of saved request buffer for debugging

	// Round 3 — R3-1 (additive). See struct doc-comment above.
	RetryAfter time.Duration
	ErrorType  string
	ErrorCode  string
}

func (e *ProviderError) Error() string {
	return e.Provider + ": " + e.Message
}

// IsRetryable implements Provider interface check
func (e *ProviderError) IsRetryable() bool {
	return e.Retryable
}

// IsRateLimitError (Round 3 — R3-1) classifies an error as a 429-style
// rate-limit condition.
//
// True when the error wraps a *ProviderError whose:
//   - HTTP status is 429, OR
//   - error-body code matches the rate-limit vocabulary
//     (equality on {rate_limit, rate_limit_error, rate_limit_exceeded}),
//     OR whose:
//   - error-body type contains the substring "rate_limit"
//     (case-insensitive; covers `rate_limit`, `rate_limit_error`,
//     `rate_limit_exceeded` and any `-`/no-underscore variant the
//     upstream brand emits).
//
// Vocabulary (Round 3b pinned; Round 3c — S1 adds rate_limit_exceeded):
//   - type-substring match on "rate_limit" (the ONLY vocabulary
//     check on the type field)
//   - code-equality set = {rate_limit, rate_limit_error, rate_limit_exceeded}
//   - status == 429
//
// Classifier returns true when ANY of the three match. The error
// body's `code` and `type` fields are read from the extended
// ProviderError.ErrorCode / ErrorType (Round 3c — W2), not from a raw
// body re-parse — the API path already decoded them in handleError.
//
// Returns false for nil errors and for non-*ProviderError errors.
// Safe to call from any goroutine.
func IsRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	// errors.As (not a direct type assertion): callers up the stack may
	// wrap the *ProviderError with fmt.Errorf("%w", ...) — the Task 18
	// coordinator reads latestReq.GetError() which can be wrapped; the
	// classifier must see through the wrapping (Task 21 uses the same
	// errors.As form for the ultimate guard).
	var pe *ProviderError
	if !errors.As(err, &pe) || pe == nil {
		return false
	}
	if pe.StatusCode == 429 {
		return true
	}
	if strings.Contains(strings.ToLower(pe.ErrorType), "rate_limit") {
		return true
	}
	switch pe.ErrorCode {
	case "rate_limit", "rate_limit_error", "rate_limit_exceeded":
		return true
	}
	return false
}
