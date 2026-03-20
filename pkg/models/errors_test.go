package models

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestNewOpenAIError(t *testing.T) {
	tests := []struct {
		name      string
		errorType string
		code      string
		message   string
	}{
		{
			name:      "rate limit error with code",
			errorType: ErrorTypeRateLimit,
			code:      ErrorCodeRateLimit,
			message:   "All models rate limited",
		},
		{
			name:      "error without code",
			errorType: ErrorTypeUpstreamError,
			code:      "",
			message:   "Upstream connection failed",
		},
		{
			name:      "context overflow error",
			errorType: ErrorTypeContextOverflow,
			code:      "",
			message:   "Context window exceeded",
		},
		{
			name:      "unavailable error with code",
			errorType: ErrorTypeUpstreamError,
			code:      ErrorCodeUnavailable,
			message:   "Service unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewOpenAIError(tt.errorType, tt.code, tt.message)

			// OpenAI format does NOT have Type at root
			if err.Error.Type != tt.errorType {
				t.Errorf("Expected Error.Type to be '%s', got '%s'", tt.errorType, err.Error.Type)
			}

			if err.Error.Message != tt.message {
				t.Errorf("Expected Error.Message to be '%s', got '%s'", tt.message, err.Error.Message)
			}

			if err.Error.Code != tt.code {
				t.Errorf("Expected Error.Code to be '%s', got '%s'", tt.code, err.Error.Code)
			}
		})
	}
}

func TestNewAnthropicError(t *testing.T) {
	tests := []struct {
		name      string
		errorType string
		code      string
		message   string
	}{
		{
			name:      "rate limit error with code",
			errorType: ErrorTypeRateLimit,
			code:      ErrorCodeRateLimit,
			message:   "All models rate limited",
		},
		{
			name:      "error without code",
			errorType: ErrorTypeUpstreamError,
			code:      "",
			message:   "Upstream connection failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewAnthropicError(tt.errorType, tt.code, tt.message)

			// Anthropic format HAS Type at root
			if err.Type != "error" {
				t.Errorf("Expected Type to be 'error', got '%s'", err.Type)
			}

			if err.Error.Type != tt.errorType {
				t.Errorf("Expected Error.Type to be '%s', got '%s'", tt.errorType, err.Error.Type)
			}

			if err.Error.Message != tt.message {
				t.Errorf("Expected Error.Message to be '%s', got '%s'", tt.message, err.Error.Message)
			}

			if err.Error.Code != tt.code {
				t.Errorf("Expected Error.Code to be '%s', got '%s'", tt.code, err.Error.Code)
			}
		})
	}
}

func TestOpenAIErrorResponseJSON(t *testing.T) {
	errResp := NewOpenAIError(
		ErrorTypeRateLimit,
		ErrorCodeRateLimit,
		"All models rate limited",
	)

	data, err := json.Marshal(errResp)
	if err != nil {
		t.Fatalf("Failed to marshal error: %v", err)
	}

	jsonStr := string(data)

	// OpenAI format: NO "type":"error" at root level
	if contains(jsonStr, `"type":"error"`) {
		t.Errorf("OpenAI format should NOT have '\"type\":\"error\"' at root, got: %s", jsonStr)
	}

	// Must have error object at root
	if !contains(jsonStr, `"error":{`) {
		t.Errorf("Expected JSON to contain '\"error\":{' at root, got: %s", jsonStr)
	}

	// Must have error.type inside error object
	if !contains(jsonStr, `"type":"rate_limit"`) {
		t.Errorf("Expected JSON to contain '\"type\":\"rate_limit\"', got: %s", jsonStr)
	}

	// Must have error.code for retry detection
	if !contains(jsonStr, `"code":"rate_limit"`) {
		t.Errorf("Expected JSON to contain '\"code\":\"rate_limit\"', got: %s", jsonStr)
	}

	// Must have message
	if !contains(jsonStr, `"message":"All models rate limited"`) {
		t.Errorf("Expected JSON to contain message, got: %s", jsonStr)
	}
}

func TestAnthropicErrorResponseJSON(t *testing.T) {
	errResp := NewAnthropicError(
		ErrorTypeRateLimit,
		ErrorCodeRateLimit,
		"All models rate limited",
	)

	data, err := json.Marshal(errResp)
	if err != nil {
		t.Fatalf("Failed to marshal error: %v", err)
	}

	jsonStr := string(data)

	// Anthropic format: HAS "type":"error" at root level
	if !contains(jsonStr, `"type":"error"`) {
		t.Errorf("Anthropic format should have '\"type\":\"error\"' at root, got: %s", jsonStr)
	}

	// Must have error object
	if !contains(jsonStr, `"error":{`) {
		t.Errorf("Expected JSON to contain '\"error\":{', got: %s", jsonStr)
	}

	// Must have error.type inside error object
	if !contains(jsonStr, `"type":"rate_limit"`) {
		t.Errorf("Expected JSON to contain '\"type\":\"rate_limit\"', got: %s", jsonStr)
	}

	// Must have error.code for retry detection
	if !contains(jsonStr, `"code":"rate_limit"`) {
		t.Errorf("Expected JSON to contain '\"code\":\"rate_limit\"', got: %s", jsonStr)
	}

	// Must have message
	if !contains(jsonStr, `"message":"All models rate limited"`) {
		t.Errorf("Expected JSON to contain message, got: %s", jsonStr)
	}
}

func TestOpenAIErrorWithoutCode(t *testing.T) {
	errResp := NewOpenAIError(
		ErrorTypeUpstreamError,
		"", // No code
		"Connection failed",
	)

	data, err := json.Marshal(errResp)
	if err != nil {
		t.Fatalf("Failed to marshal error: %v", err)
	}

	jsonStr := string(data)

	// OpenAI format: NO type:"error" at root
	if contains(jsonStr, `"type":"error"`) {
		t.Errorf("OpenAI format should NOT have '\"type\":\"error\"' at root, got: %s", jsonStr)
	}

	// Should have error.type inside error object
	if !contains(jsonStr, `"type":"upstream_error"`) {
		t.Errorf("Expected JSON to contain '\"type\":\"upstream_error\"', got: %s", jsonStr)
	}

	// Code field should be omitted when empty (omitempty)
	if contains(jsonStr, `"code":""`) {
		t.Errorf("Expected empty code to be omitted, got: %s", jsonStr)
	}
}

func TestAnthropicErrorWithoutCode(t *testing.T) {
	errResp := NewAnthropicError(
		ErrorTypeUpstreamError,
		"", // No code
		"Connection failed",
	)

	data, err := json.Marshal(errResp)
	if err != nil {
		t.Fatalf("Failed to marshal error: %v", err)
	}

	jsonStr := string(data)

	// Anthropic format: HAS type:"error" at root
	if !contains(jsonStr, `"type":"error"`) {
		t.Errorf("Anthropic format should have '\"type\":\"error\"' at root, got: %s", jsonStr)
	}

	// Should have error.type inside error object
	if !contains(jsonStr, `"type":"upstream_error"`) {
		t.Errorf("Expected JSON to contain '\"type\":\"upstream_error\"', got: %s", jsonStr)
	}

	// Code field should be omitted when empty (omitempty)
	if contains(jsonStr, `"code":""`) {
		t.Errorf("Expected empty code to be omitted, got: %s", jsonStr)
	}
}

func TestIsContextOverflowError(t *testing.T) {
	tests := []struct {
		err      error
		expected bool
	}{
		{fmt.Errorf("context_length_exceeded: max 4096"), true},
		{fmt.Errorf("Context_Length_Exceeded: max 4096"), true}, // Case insensitive
		{fmt.Errorf("prompt is too long"), true},
		{fmt.Errorf("exceeds the context window"), true},
		{fmt.Errorf("maximum context length is 4096 tokens"), true},
		{fmt.Errorf("Input is too long for requested model"), true},
		{fmt.Errorf("reduce the length of the messages"), true},
		{fmt.Errorf("rate limit exceeded"), false},
		{fmt.Errorf("service unavailable"), false},
		{fmt.Errorf("connection timeout"), false},
		{fmt.Errorf("internal server error"), false},
		{nil, false},
	}

	for _, tt := range tests {
		errStr := "nil"
		if tt.err != nil {
			errStr = tt.err.Error()
		}
		t.Run(errStr, func(t *testing.T) {
			result := IsContextOverflowError(tt.err)
			if result != tt.expected {
				t.Errorf("IsContextOverflowError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsMiddle(s, substr)))
}

func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
