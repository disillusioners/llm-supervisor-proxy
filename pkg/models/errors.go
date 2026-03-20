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

// OpenCodeErrorResponse is the OpenCode-compatible error format
type OpenCodeErrorResponse struct {
	Type  string       `json:"type"` // Always "error"
	Error ErrorDetails `json:"error"`
}

type ErrorDetails struct {
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// NewOpenCodeError creates a new OpenCode-compatible error response
func NewOpenCodeError(errorType, code, message string) OpenCodeErrorResponse {
	return OpenCodeErrorResponse{
		Type: "error",
		Error: ErrorDetails{
			Type:    errorType,
			Code:    code,
			Message: message,
		},
	}
}

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
