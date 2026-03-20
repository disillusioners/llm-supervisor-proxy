package models

import "strings"

// Error type constants (for error.type field)
const (
	ErrorTypeRateLimit           = "rate_limit"
	ErrorTypeTooManyRequests     = "too_many_requests"
	ErrorTypeAuthenticationError = "authentication_error"
	ErrorTypeContextOverflow     = "context_length_exceeded"
	ErrorTypeUpstreamError       = "upstream_error"
	ErrorTypeServerError         = "server_error"
)

// Error code constants (for error.code field - triggers OpenCode retry detection)
const (
	ErrorCodeRateLimit   = "rate_limit"
	ErrorCodeExhausted   = "exhausted"
	ErrorCodeUnavailable = "unavailable"
)

// OpenAIErrorResponse is the OpenAI-compatible error format.
// Used for OpenAI endpoint responses.
// Format: {"error": {"type": "...", "code": "...", "message": "..."}}
type OpenAIErrorResponse struct {
	Error ErrorDetails `json:"error"`
}

// AnthropicErrorResponse is the Anthropic-compatible error format.
// Used for Anthropic endpoint responses (HTTP errors, not SSE).
// Format: {"type": "error", "error": {"type": "...", "message": "..."}}
type AnthropicErrorResponse struct {
	Type  string       `json:"type"` // Always "error"
	Error ErrorDetails `json:"error"`
}

type ErrorDetails struct {
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// NewOpenAIError creates a new OpenAI-compatible error response.
// OpenAI format has NO "type" field at root level.
func NewOpenAIError(errorType, code, message string) OpenAIErrorResponse {
	return OpenAIErrorResponse{
		Error: ErrorDetails{
			Type:    errorType,
			Code:    code,
			Message: message,
		},
	}
}

// NewAnthropicError creates a new Anthropic-compatible error response.
// Anthropic format HAS "type": "error" at root level.
func NewAnthropicError(errorType, code, message string) AnthropicErrorResponse {
	return AnthropicErrorResponse{
		Type: "error",
		Error: ErrorDetails{
			Type:    errorType,
			Code:    code,
			Message: message,
		},
	}
}

// Backward compatibility aliases
// Deprecated: Use NewOpenAIError instead for OpenAI endpoint
var NewOpenCodeError = NewOpenAIError

// Deprecated: Use OpenAIErrorResponse instead for OpenAI endpoint
type OpenCodeErrorResponse = OpenAIErrorResponse

// IsContextOverflowError checks if an error indicates context window overflow.
// OpenCode checks these patterns BEFORE retry logic to trigger compaction instead.
func IsContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	patterns := []string{
		"context_length_exceeded",
		"prompt is too long",
		"exceeds the context window",
		"maximum context length",
		"input is too long",
		"reduce the length of the messages",
	}
	for _, p := range patterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}
