package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/loopdetection"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/supervisor"
)

// ─────────────────────────────────────────────────────────────────────────────
// requestContext holds all mutable state for a single request lifecycle.
// It is passed through the sub-functions to avoid huge parameter lists.
// ─────────────────────────────────────────────────────────────────────────────

type requestContext struct {
	conf      ConfigSnapshot
	targetURL string
	reqID     string
	startTime time.Time
	reqLog    *store.RequestLog
	modelList []string

	// Request body (mutated on retries)
	requestBody map[string]interface{}
	isStream    bool

	// Original request metadata
	originalHeaders http.Header
	method          string
	baseCtx         context.Context

	// Original messages (immutable snapshot for retry reconstruction)
	originalMessages []interface{}

	// Accumulated response buffers
	accumulatedResponse strings.Builder
	accumulatedThinking strings.Builder

	// State
	headersSent bool

	// Loop detection (persists across retries within this request)
	loopDetector *loopdetection.Detector
}

// ─────────────────────────────────────────────────────────────────────────────
// attemptResult represents the outcome of a single upstream attempt.
// ─────────────────────────────────────────────────────────────────────────────

type attemptResult int

const (
	attemptSuccess           attemptResult = iota // Request completed successfully
	attemptReturnImmediately                      // Handler should return (error written or headers already sent)
	attemptContinueRetry                          // Retry current model
	attemptBreakToFallback                        // Move to next model (fallback)
)

// ─────────────────────────────────────────────────────────────────────────────
// retryCounters tracks per-model retry state.
// ─────────────────────────────────────────────────────────────────────────────

type retryCounters struct {
	errorRetries int
	idleRetries  int
	genRetries   int
	lastErr      error
}

func (rc *retryCounters) totalAttempts() int {
	return rc.errorRetries + rc.idleRetries + rc.genRetries
}

// ─────────────────────────────────────────────────────────────────────────────
// Pure helper functions (no Handler receiver)
// ─────────────────────────────────────────────────────────────────────────────

// parseMessages converts the raw JSON "messages" array to store.Message slice.
func parseMessages(requestBody map[string]interface{}) []store.Message {
	var storeMessages []store.Message
	if msgs, ok := requestBody["messages"].([]interface{}); ok {
		for _, m := range msgs {
			if msgMap, ok := m.(map[string]interface{}); ok {
				role, _ := msgMap["role"].(string)
				content, _ := msgMap["content"].(string)
				storeMessages = append(storeMessages, store.Message{Role: role, Content: content})
			}
		}
	}
	return storeMessages
}

// buildModelList constructs [originalModel, fallback1, fallback2, ...] from config.
func buildModelList(originalModel string, modelsConfig models.ModelsConfigInterface) []string {
	var allModels []string
	if modelsConfig != nil {
		fallbackChain := modelsConfig.GetFallbackChain(originalModel)
		if len(fallbackChain) > 0 {
			allModels = fallbackChain[1:]
		}
	}
	if allModels == nil {
		allModels = []string{}
	}
	modelList := []string{originalModel}
	modelList = append(modelList, allModels...)
	return modelList
}

// copyHeaders copies request headers from src to dst, skipping Content-Length.
func copyHeaders(dst *http.Request, src http.Header) {
	for name, values := range src {
		if name == "Content-Length" {
			continue
		}
		for _, value := range values {
			dst.Header.Add(name, value)
		}
	}
}

// extractNonStreamContent extracts content and thinking from a non-streaming response body.
func extractNonStreamContent(bodyBytes []byte, response, thinking *strings.Builder) {
	var respMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &respMap); err != nil {
		return
	}
	choices, ok := respMap["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return
	}
	msg, ok := choice["message"].(map[string]interface{})
	if !ok {
		return
	}

	if content, ok := msg["content"].(string); ok {
		response.WriteString(content)
	}
	if rc, ok := msg["reasoning_content"].(string); ok {
		thinking.WriteString(rc)
	}
	if psf, ok := msg["provider_specific_fields"].(map[string]interface{}); ok {
		if rc, ok := psf["reasoning_content"].(string); ok {
			thinking.WriteString(rc)
		}
	}
}

// extractStreamChunkContent extracts content and thinking from a single SSE chunk.
func extractStreamChunkContent(data []byte, response, thinking *strings.Builder) {
	var chunk map[string]interface{}
	if err := json.Unmarshal(data, &chunk); err != nil {
		return
	}
	choices, ok := chunk["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return
	}
	delta, ok := choice["delta"].(map[string]interface{})
	if !ok {
		return
	}

	if content, ok := delta["content"].(string); ok {
		response.WriteString(content)
	}
	if t, ok := delta["reasoning_content"].(string); ok {
		thinking.WriteString(t)
	} else if t, ok := delta["thinking"].(string); ok {
		thinking.WriteString(t)
	}
}

// determineFailureReason determines the reason for failure based on the last error and attempt count.
func determineFailureReason(err error, errorRetries, maxUpstreamErrorRetries, idleRetries, maxIdleRetries, genRetries, maxGenRetries int) string {
	if err != nil && errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if err != nil && errors.Is(err, supervisor.ErrIdleTimeout) {
		return "idle_timeout"
	}
	if idleRetries > maxIdleRetries {
		return "max_idle_retries"
	}
	if genRetries > maxGenRetries {
		return "max_generation_retries"
	}
	if errorRetries > maxUpstreamErrorRetries {
		return "max_upstream_error_retries"
	}
	return "upstream_error"
}

// extractToolCallActions extracts tool call actions from an SSE chunk's raw JSON.
// Returns nil if no tool_calls are present in the chunk.
func extractToolCallActions(data []byte) []loopdetection.Action {
	var chunk map[string]interface{}
	if err := json.Unmarshal(data, &chunk); err != nil {
		return nil
	}
	choices, ok := chunk["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return nil
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return nil
	}
	delta, ok := choice["delta"].(map[string]interface{})
	if !ok {
		return nil
	}
	toolCalls, ok := delta["tool_calls"].([]interface{})
	if !ok || len(toolCalls) == 0 {
		return nil
	}

	var actions []loopdetection.Action
	for _, tc := range toolCalls {
		tcMap, ok := tc.(map[string]interface{})
		if !ok {
			continue
		}
		fn, ok := tcMap["function"].(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := fn["name"].(string)
		args, _ := fn["arguments"].(string)
		if name != "" {
			// Use function name as Type, extract target from arguments if possible
			target := extractTargetFromArgs(args)
			actions = append(actions, loopdetection.Action{
				Type:   name,
				Target: target,
			})
		}
	}
	return actions
}

// extractTargetFromArgs tries to extract a "path" or "file" field from
// tool call arguments JSON. Returns the raw args string as fallback.
func extractTargetFromArgs(args string) string {
	if args == "" {
		return ""
	}
	var argsMap map[string]interface{}
	if err := json.Unmarshal([]byte(args), &argsMap); err != nil {
		return args // Return raw args if not valid JSON
	}
	// Common field names for file/path targets
	for _, key := range []string{"path", "file", "filename", "target", "query"} {
		if val, ok := argsMap[key].(string); ok {
			return val
		}
	}
	return args
}

// isStreamErrorChunk detects if a line is an error response dumped into the stream.
// This happens when upstream crashes mid-stream and dumps raw error JSON instead of
// proper SSE format. Returns the error message if detected, empty string otherwise.
func isStreamErrorChunk(line []byte) string {
	// Skip empty lines and whitespace-only lines
	lineStr := strings.TrimSpace(string(line))
	if lineStr == "" {
		return ""
	}

	// Valid SSE data lines start with "data: "
	if bytes.HasPrefix(line, []byte("data: ")) {
		// Check if the data payload itself contains an error
		data := bytes.TrimPrefix(line, []byte("data: "))
		dataStr := strings.TrimSpace(string(data))

		// Check for [DONE] marker - this is valid, not an error
		if dataStr == "[DONE]" {
			return ""
		}

		// Try to parse as JSON and check for error structure
		if strings.HasPrefix(dataStr, "{") && strings.HasSuffix(dataStr, "}") {
			var errorResp map[string]interface{}
			if err := json.Unmarshal(data, &errorResp); err == nil {
				if errMsg := extractNestedError(errorResp); errMsg != "" {
					return errMsg
				}
			}
		}

		// Not an error, valid SSE data
		return ""
	}

	// Check for plain text error patterns (non-JSON lines that indicate errors)
	// These are common when upstream crashes mid-stream
	lowerLine := strings.ToLower(lineStr)
	errorIndicators := []string{
		"error:",
		"exception:",
		"apierror:",
		"litellm.",
		"runtimeerror:",
		"valueerror:",
		"typeerror:",
		"connectionerror:",
		"timeouterror:",
		"internal server error",
		"service unavailable",
		"bad gateway",
		"gateway timeout",
	}
	for _, indicator := range errorIndicators {
		if strings.Contains(lowerLine, indicator) {
			return lineStr
		}
	}

	// Check if this looks like a JSON error response
	if !strings.HasPrefix(lineStr, "{") || !strings.HasSuffix(lineStr, "}") {
		return ""
	}

	var errorResp map[string]interface{}
	if err := json.Unmarshal(line, &errorResp); err != nil {
		return ""
	}

	// Check for common error structures
	// LiteLLM format: {"error": {"message": "...", "type": "..."}}
	if errMsg := extractNestedError(errorResp); errMsg != "" {
		return errMsg
	}

	return ""
}

// extractNestedError extracts error message from various error response formats.
func extractNestedError(errorResp map[string]interface{}) string {
	// LiteLLM/API format: {"error": {"message": "..."}}
	if errObj, ok := errorResp["error"].(map[string]interface{}); ok {
		if msg, ok := errObj["message"].(string); ok {
			return msg
		}
		if msg, ok := errObj["type"].(string); ok {
			return msg
		}
	}

	// OpenAI format: {"error": {"message": "...", "type": "..."}}
	if errStr, ok := errorResp["error"].(string); ok {
		return errStr
	}

	// Some APIs return: {"detail": "..."}
	if detail, ok := errorResp["detail"].(string); ok {
		return detail
	}

	// Check for error indicators
	if _, hasError := errorResp["error"]; hasError {
		if bytes, err := json.Marshal(errorResp); err == nil {
			return string(bytes)
		}
	}

	return ""
}
