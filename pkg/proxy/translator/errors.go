package translator

import (
	"encoding/json"
)

// Error type constants
const (
	ErrInvalidRequest = "invalid_request_error"
	ErrAuthentication = "authentication_error"
	ErrPermission     = "permission_error"
	ErrNotFound       = "not_found_error"
	ErrRateLimit      = "rate_limit_error"
	ErrAPI            = "api_error"
	ErrOverloaded     = "overloaded_error"
)

// TranslateError translates an OpenAI error to Anthropic format
func TranslateError(openaiBody []byte, statusCode int) ([]byte, error) {
	// Default error type based on status code
	errorType := mapStatusCodeToErrorType(statusCode)
	message := "An error occurred"

	// Try to parse the OpenAI error body
	var errData map[string]interface{}
	if err := json.Unmarshal(openaiBody, &errData); err != nil {
		// If we can't parse, use default
		message = string(openaiBody)
	} else {
		// Extract message from various formats
		message = extractErrorMessage(errData)

		// Try to extract error type from the response
		if extractedType := extractErrorType(errData); extractedType != "" {
			errorType = extractedType
		}
	}

	// If message is empty, provide a default
	if message == "" {
		message = "An error occurred"
	}

	return TranslateErrorFromMessage(errorType, message)
}

// TranslateErrorFromMessage creates an Anthropic error from a message
func TranslateErrorFromMessage(errorType, message string) ([]byte, error) {
	response := AnthropicErrorResponse{
		Type: "error",
		Error: AnthropicError{
			Type:    errorType,
			Message: message,
		},
	}
	return json.Marshal(response)
}

// mapStatusCodeToErrorType maps HTTP status to Anthropic error type
func mapStatusCodeToErrorType(statusCode int) string {
	switch statusCode {
	case 400:
		return ErrInvalidRequest
	case 401:
		return ErrAuthentication
	case 403:
		return ErrPermission
	case 404:
		return ErrNotFound
	case 429:
		return ErrRateLimit
	case 500:
		return ErrAPI
	case 529:
		return ErrOverloaded
	default:
		// For other 5xx errors, return api_error
		if statusCode >= 500 && statusCode < 600 {
			return ErrAPI
		}
		return ErrAPI
	}
}

// extractErrorMessage extracts the error message from various OpenAI error formats
func extractErrorMessage(openaiError map[string]interface{}) string {
	// Format 1: {"error": {"message": "...", "type": "..."}}
	if errorObj, ok := openaiError["error"].(map[string]interface{}); ok {
		if msg, ok := errorObj["message"].(string); ok && msg != "" {
			return msg
		}
	}

	// Format 2: {"error": "..."}
	if msg, ok := openaiError["error"].(string); ok && msg != "" {
		return msg
	}

	// Format 3: {"detail": "..."}
	if msg, ok := openaiError["detail"].(string); ok && msg != "" {
		return msg
	}

	// Format 4: Check for message at root level
	if msg, ok := openaiError["message"].(string); ok && msg != "" {
		return msg
	}

	return ""
}

// extractErrorType extracts error type from OpenAI error
func extractErrorType(openaiError map[string]interface{}) string {
	// Check if there's an error object with type
	if errorObj, ok := openaiError["error"].(map[string]interface{}); ok {
		// Try "type" field
		if errType, ok := errorObj["type"].(string); ok && errType != "" {
			return errType
		}

		// Try "code" field
		if code, ok := errorObj["code"].(string); ok && code != "" {
			// Map common OpenAI error codes to Anthropic types
			switch code {
			case "invalid_api_key", "authentication_error":
				return ErrAuthentication
			case "insufficient_quota", "billing_limit_exceeded":
				return ErrPermission
			case "rate_limit_exceeded":
				return ErrRateLimit
			case "model_not_found":
				return ErrNotFound
			default:
				return code
			}
		}
	}

	return ""
}
