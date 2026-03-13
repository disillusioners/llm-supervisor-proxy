package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/loopdetection"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/supervisor"
)

// shadowResult represents the result from a shadow request
type shadowResult struct {
	buffer    *bytes.Buffer
	completed bool
	err       error
}

// shadowRequestState tracks the state of a shadow request
type shadowRequestState struct {
	mu         sync.RWMutex
	done       chan shadowResult // Closed when shadow completes
	cancelFunc context.CancelFunc
	started    bool
	completed  bool
	model      string
	startTime  time.Time
}

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
	accumulatedResponse  strings.Builder
	accumulatedThinking  strings.Builder
	accumulatedToolCalls []store.ToolCall

	// Stream buffer for retry-safe streaming
	// Chunks are buffered until stream completes successfully, then flushed to client
	// This enables safe retry mid-stream since nothing is sent until [DONE]
	streamBuffer bytes.Buffer

	// State
	headersSent bool

	// Loop detection (persists across retries within this request)
	loopDetector *loopdetection.Detector

	// Stream metadata normalization (for transparent fallbacks)
	// Cached from first chunk to maintain consistency across retries/fallbacks
	streamID    string
	streamIDSet bool

	// Proxy-only flags (stripped before forwarding upstream)
	bypassInternal bool // Force external upstream, skip internal provider routing

	// Streaming non-retryable state
	// When true, this request will not retry upstream on errors
	// This is set after ReleaseStreamChunkDeadline is reached and buffer is flushed
	streamingNonRetryable bool

	// Shadow retry state
	shadow *shadowRequestState
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

// extractParameters extracts request parameters from the request body,
// excluding standard fields that are displayed separately.
func extractParameters(requestBody map[string]interface{}) map[string]interface{} {
	// Fields to exclude (shown separately in UI)
	excludeFields := map[string]bool{
		"messages": true,
		"model":    true,
		"stream":   true,
	}

	params := make(map[string]interface{})
	for key, value := range requestBody {
		if !excludeFields[key] {
			params[key] = value
		}
	}

	// Return nil if no parameters to avoid empty object in JSON
	if len(params) == 0 {
		return nil
	}
	return params
}

// parseMessages converts the raw JSON "messages" array to store.Message slice.
// Handles both string content and array content (OpenAI multimodal format).
// Also extracts tool_calls for assistant messages.
func parseMessages(requestBody map[string]interface{}) []store.Message {
	var storeMessages []store.Message
	if msgs, ok := requestBody["messages"].([]interface{}); ok {
		for _, m := range msgs {
			if msgMap, ok := m.(map[string]interface{}); ok {
				role, _ := msgMap["role"].(string)
				var content string
				switch c := msgMap["content"].(type) {
				case string:
					content = c
				case []interface{}:
					// Flatten array content to string for storage
					for _, part := range c {
						if partMap, ok := part.(map[string]interface{}); ok {
							if text, ok := partMap["text"].(string); ok {
								content += text
							}
						}
					}
				}

				// Extract tool_calls if present (for assistant messages)
				var toolCalls []store.ToolCall
				if tcInterface, ok := msgMap["tool_calls"].([]interface{}); ok {
					for _, tc := range tcInterface {
						if tcMap, ok := tc.(map[string]interface{}); ok {
							toolCall := store.ToolCall{}
							toolCall.ID, _ = tcMap["id"].(string)
							toolCall.Type, _ = tcMap["type"].(string)
							if fn, ok := tcMap["function"].(map[string]interface{}); ok {
								toolCall.Function.Name, _ = fn["name"].(string)
								toolCall.Function.Arguments, _ = fn["arguments"].(string)
							}
							toolCalls = append(toolCalls, toolCall)
						}
					}
				}

				msg := store.Message{Role: role, Content: content}
				if len(toolCalls) > 0 {
					msg.ToolCalls = toolCalls
				}
				storeMessages = append(storeMessages, msg)
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

// copyHeaders copies request headers from src to dst, skipping Content-Length
// and proxy-only headers that should never be forwarded upstream.
func copyHeaders(dst *http.Request, src http.Header) {
	// Headers to strip (never forward upstream)
	stripHeaders := map[string]bool{
		"Content-Length":             true,
		"X-Llmproxy-Bypass-Internal": true,
	}
	for name, values := range src {
		if stripHeaders[name] {
			continue
		}
		for _, value := range values {
			dst.Header.Add(name, value)
		}
	}
}

func extractNonStreamContent(bodyBytes []byte, response, thinking *strings.Builder) {
	var respMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &respMap); err != nil {
		log.Printf("[DEBUG] extractNonStreamContent: failed to parse body: %v", err)
		return
	}

	choices, ok := respMap["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		log.Printf("[DEBUG] extractNonStreamContent: no choices found")
		return
	}

	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		log.Printf("[DEBUG] extractNonStreamContent: choice[0] is not a map")
		return
	}

	msg, ok := choice["message"].(map[string]interface{})
	if !ok {
		log.Printf("[DEBUG] extractNonStreamContent: message is not a map")
		return
	}

	// Extract content
	if content, ok := msg["content"]; ok && content != nil {
		switch c := content.(type) {
		case string:
			if c != "" {
				response.WriteString(c)
				log.Printf("[DEBUG] extractNonStreamContent: string content len=%d", len(c))
			}
		case []interface{}:
			for _, part := range c {
				if partMap, ok := part.(map[string]interface{}); ok {
					partType, _ := partMap["type"].(string)
					switch partType {
					case "text":
						if text, ok := partMap["text"].(string); ok {
							response.WriteString(text)
							log.Printf("[DEBUG] extractNonStreamContent: text part len=%d", len(text))
						}
					case "thinking":
						if t, ok := partMap["thinking"].(string); ok {
							thinking.WriteString(t)
							log.Printf("[DEBUG] extractNonStreamContent: thinking part len=%d", len(t))
						}
					}
				}
			}
		}
	}

	// Extract thinking/reasoning from top-level fields
	var thinkingContent string
	if rc, ok := msg["reasoning_content"].(string); ok && rc != "" {
		thinkingContent = rc
		log.Printf("[DEBUG] extractNonStreamContent: top-level reasoning_content len=%d", len(rc))
	} else if rc, ok := msg["reasoning"].(string); ok && rc != "" {
		thinkingContent = rc
		log.Printf("[DEBUG] extractNonStreamContent: top-level reasoning len=%d", len(rc))
	} else if t, ok := msg["thinking"].(string); ok && t != "" {
		thinkingContent = t
		log.Printf("[DEBUG] extractNonStreamContent: top-level thinking len=%d", len(t))
	}

	if thinkingContent != "" {
		thinking.WriteString(thinkingContent)
	}

	// Check provider_specific_fields
	if psf, ok := msg["provider_specific_fields"].(map[string]interface{}); ok {
		if rc, ok := psf["reasoning_content"].(string); ok && rc != "" && thinking.Len() == 0 {
			thinking.WriteString(rc)
			log.Printf("[DEBUG] extractNonStreamContent: provider_specific_fields.reasoning_content len=%d", len(rc))
		}
	}

	log.Printf("[DEBUG] extractNonStreamContent: total response len=%d, thinking len=%d", response.Len(), thinking.Len())
}

// extractStreamChunkContent extracts content and thinking from a single SSE chunk.
func extractStreamChunkContent(data []byte, response, thinking *strings.Builder, toolCallsAccum *[]store.ToolCall) {
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
	// Extract thinking/reasoning content - support multiple provider field names
	// Priority: reasoning_content > reasoning > thinking
	if t, ok := delta["reasoning_content"].(string); ok && t != "" {
		thinking.WriteString(t)
	} else if t, ok := delta["reasoning"].(string); ok && t != "" {
		thinking.WriteString(t)
	} else if t, ok := delta["thinking"].(string); ok && t != "" {
		thinking.WriteString(t)
	}

	// Extract and accumulate tool calls
	if toolCalls, ok := delta["tool_calls"].([]interface{}); ok && toolCallsAccum != nil {
		for _, tc := range toolCalls {
			tcMap, ok := tc.(map[string]interface{})
			if !ok {
				continue
			}

			// Get index (required for streaming)
			index, ok := tcMap["index"].(float64)
			if !ok {
				continue
			}
			idx := int(index)

			// Ensure accumulator has enough capacity
			for len(*toolCallsAccum) <= idx {
				*toolCallsAccum = append(*toolCallsAccum, store.ToolCall{})
			}

			// Update tool call at index
			toolCall := &(*toolCallsAccum)[idx]

			// ID (usually appears in first chunk)
			if id, ok := tcMap["id"].(string); ok && id != "" {
				toolCall.ID = id
			}

			// Type (usually appears in first chunk)
			if typ, ok := tcMap["type"].(string); ok && typ != "" {
				toolCall.Type = typ
			}

			// Function details (name and arguments are streamed incrementally)
			if fn, ok := tcMap["function"].(map[string]interface{}); ok {
				if name, ok := fn["name"].(string); ok && name != "" {
					toolCall.Function.Name = name
				}
				if args, ok := fn["arguments"].(string); ok {
					// Arguments are streamed in chunks, so append
					toolCall.Function.Arguments += args
				}
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Stream chunk validation and normalization (for transparent fallbacks)
// ─────────────────────────────────────────────────────────────────────────────

// isValidStreamChunk checks if a data payload is valid JSON.
// This prevents corrupted/incomplete chunks from being sent to clients
// when upstream crashes mid-generation.
func isValidStreamChunk(data []byte) bool {
	// [DONE] marker is valid
	if string(data) == "[DONE]" {
		return true
	}
	return json.Valid(data)
}

// normalizeStreamChunk rewrites a stream chunk to maintain consistency across
// retries and fallbacks. It:
// 1. Caches the original stream ID from the first chunk
// 2. Replaces the ID in all subsequent chunks with the cached original
// 3. Strips "role" from delta (should only appear in first chunk)
//
// This prevents strict clients (SDKs) from disconnecting due to:
// - Message ID changing mid-stream
// - Role being re-announced mid-stream
func normalizeStreamChunk(data []byte, rc *requestContext) []byte {
	// Don't process [DONE] marker
	if string(data) == "[DONE]" {
		return data
	}

	var chunk map[string]interface{}
	if err := json.Unmarshal(data, &chunk); err != nil {
		return data // Return as-is if not valid JSON
	}

	// Cache stream ID from first chunk
	isFirstChunk := !rc.streamIDSet
	if !rc.streamIDSet {
		if id, ok := chunk["id"].(string); ok && id != "" {
			rc.streamID = id
			rc.streamIDSet = true
		}
	} else if rc.streamID != "" {
		// Replace ID with cached original
		if _, hasID := chunk["id"]; hasID {
			chunk["id"] = rc.streamID
		}
	}

	// Strip role from delta (should only appear in the very first overall chunk)
	if !isFirstChunk {
		if choices, ok := chunk["choices"].([]interface{}); ok && len(choices) > 0 {
			if choice, ok := choices[0].(map[string]interface{}); ok {
				if delta, ok := choice["delta"].(map[string]interface{}); ok {
					delete(delta, "role")
				}
			}
		}
	}

	normalized, err := json.Marshal(chunk)
	if err != nil {
		return data // Return original on error
	}
	return normalized
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
