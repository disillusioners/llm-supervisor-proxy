package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

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

	// Accumulated response buffers
	accumulatedResponse strings.Builder
	accumulatedThinking strings.Builder

	// State
	headersSent bool
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
func buildModelList(originalModel string, modelsConfig *models.ModelsConfig) []string {
	var allModels []string
	if modelsConfig != nil {
		fallbackChain := modelsConfig.GetFallbackChain(originalModel)
		if len(fallbackChain) > 0 {
			allModels = fallbackChain[1:]
		}
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
