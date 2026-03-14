// ─────────────────────────────────────────────────────────────────────────────
// Per-model attempt loop
// ─────────────────────────────────────────────────────────────────────────────

package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/logger"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

// attemptModel runs the retry loop for a single model. Returns true if the
// request completed successfully (response has been written to w).
func (h *Handler) attemptModel(w http.ResponseWriter, rc *requestContext, modelIndex int, currentModel string) bool {
	// Store current model index for shadow retry logic
	rc.currentModelIndex = modelIndex
	counters := &retryCounters{}

	for {
		attempt := counters.totalAttempts()
		if counters.errorRetries > rc.conf.MaxUpstreamErrorRetries ||
			counters.idleRetries > rc.conf.MaxIdleRetries ||
			counters.genRetries > rc.conf.MaxGenerationRetries {
			break
		}

		// Check if client disconnected before attempting retry
		// This prevents unnecessary retry work when the client is already gone
		logger.Debugf("[RETRY-DEBUG] attempt=%d, baseCtx.Err()=%v, headersSent=%v, isStream=%v",
			attempt, rc.baseCtx.Err(), rc.headersSent, rc.isStream)
		if attempt > 0 && rc.baseCtx.Err() != nil {
			if errors.Is(rc.baseCtx.Err(), context.Canceled) {
				log.Println("Client disconnected, aborting retry")
				h.publishEvent("client_disconnected_during_retry", map[string]interface{}{"attempt": attempt, "id": rc.reqID})
				rc.reqLog.Status = "failed"
				rc.reqLog.Error = "Client disconnected"
				rc.reqLog.EndTime = time.Now()
				rc.reqLog.Duration = time.Since(rc.startTime).String()
				h.store.Add(rc.reqLog)
			}
			return true // Don't try fallback - client is gone
		}

		if attempt > 0 {
			h.prepareRetry(w, rc, attempt, counters)
		}

		result := h.doSingleAttempt(w, rc, modelIndex, attempt, counters)
		switch result {
		case attemptSuccess:
			return true
		case attemptReturnImmediately:
			return true // Already wrote error to client
		case attemptContinueRetry:
			continue
		case attemptBreakToFallback:
			return false
		}
	}

	return false
}

// prepareRetry updates request log for retry.
// Headers may have already been sent for streaming requests (to solve TTFB),
// but the response body is buffered until [DONE]. On retry, we buffer fresh
// content and flush on success. The client just sees a pause during retry.
func (h *Handler) prepareRetry(w http.ResponseWriter, rc *requestContext, attempt int, counters *retryCounters) {
	log.Printf("Retrying request (attempt %d)...", attempt)
	rc.reqLog.Retries = attempt
	rc.reqLog.Status = "retrying"
	h.store.Add(rc.reqLog)

	h.publishEvent("retry_attempt", map[string]interface{}{"attempt": attempt, "id": rc.reqID})

	// Note: Headers may have been sent for streaming requests (headersSent=true).
	// The client sees only a pause during retry - no keep-alive needed since
	// SSE is just a long-lived HTTP response.
}

// doSingleAttempt performs a single upstream HTTP request and handles the response.
// For internal models, it routes directly to the AI provider instead of upstream.
func (h *Handler) doSingleAttempt(w http.ResponseWriter, rc *requestContext, modelIndex, attempt int, counters *retryCounters) attemptResult {
	// Build the body to send, optionally truncating unsupported params for this model.
	bodyToSend := rc.requestBody
	currentModel, _ := rc.requestBody["model"].(string)
	if rc.conf.ModelsConfig != nil {
		if toStrip := rc.conf.ModelsConfig.GetTruncateParams(currentModel); len(toStrip) > 0 {
			// Shallow-clone the map so we don't mutate rc.requestBody
			cloned := make(map[string]interface{}, len(rc.requestBody))
			for k, v := range rc.requestBody {
				cloned[k] = v
			}
			for _, param := range toStrip {
				delete(cloned, param)
			}
			bodyToSend = cloned
		}

		// Check if this model uses internal upstream (unless bypass requested)
		if modelConfig := rc.conf.ModelsConfig.GetModel(currentModel); modelConfig != nil && modelConfig.Internal && !rc.bypassInternal {
			return h.doInternalAttempt(w, rc, modelConfig, bodyToSend, attempt, counters)
		}
	}

	newBodyBytes, _ := json.Marshal(bodyToSend)

	attemptCtx, attemptCancel := context.WithTimeout(rc.baseCtx, rc.conf.MaxGenerationTime)
	defer attemptCancel()

	proxyReq, err := http.NewRequestWithContext(attemptCtx, rc.method, rc.targetURL, bytes.NewBuffer(newBodyBytes))
	if err != nil {
		log.Printf("Failed to create request: %v", err)
		return attemptReturnImmediately
	}

	logger.Debugf("[DO-ATTEMPT] Starting attempt %d for request %s, baseCtx.Err()=%v", attempt, rc.reqID, rc.baseCtx.Err())

	copyHeaders(proxyReq, rc.originalHeaders)

	// If UpstreamCredentialID is configured, resolve the credential and set auth header
	// This allows the proxy to authenticate with external upstream providers
	// using a different token than what the client provided
	if rc.conf.UpstreamCredentialID != "" {
		// Remove all auth headers first to avoid conflicts
		proxyReq.Header.Del("Authorization")
		proxyReq.Header.Del("X-API-Key")
		proxyReq.Header.Del("x-api-key")
		proxyReq.Header.Del("api-key")

		// Resolve credential
		cred := rc.conf.ModelsConfig.GetCredential(rc.conf.UpstreamCredentialID)
		if cred != nil {
			apiKey := cred.ResolveAPIKey()
			if apiKey != "" {
				proxyReq.Header.Set("Authorization", "Bearer "+apiKey)
			}
		}
	}

	resp, err := h.client.Do(proxyReq)

	logger.Debugf("[DO-ATTEMPT] Completed attempt %d, err=%v, baseCtx.Err()=%v", attempt, err, rc.baseCtx.Err())

	if err != nil {
		// Per Go's http.Client.Do docs: even on error, resp.Body may be non-nil and must be closed
		if resp != nil {
			resp.Body.Close()
		}
		return h.handleUpstreamRequestError(rc, err, attemptCtx, attempt, counters)
	}

	if resp.StatusCode != http.StatusOK {
		return h.handleNonOKStatus(w, rc, resp, modelIndex, counters)
	}

	return h.handleOKResponse(w, rc, resp, attemptCtx, attempt, counters)
}

// doInternalAttempt handles requests for internal models (direct provider calls)
func (h *Handler) doInternalAttempt(w http.ResponseWriter, rc *requestContext, modelConfig *models.ModelConfig, bodyToSend map[string]interface{}, attempt int, counters *retryCounters) attemptResult {
	attemptCtx, attemptCancel := context.WithTimeout(rc.baseCtx, rc.conf.MaxGenerationTime)
	defer attemptCancel()

	logger.Debugf("[DO-INTERNAL] Starting internal attempt %d for request %s, model %s", attempt, rc.reqID, modelConfig.ID)

	internalHandler := NewInternalHandler(modelConfig, h.config.ModelsConfig)
	internalHandler.SetDebugContext(h.bufferStore, rc.reqID)

	// Set up repairer with optional fixer model
	if rc.conf.ToolRepair.Enabled {
		repairer := toolrepair.NewRepairer(&rc.conf.ToolRepair)

		// Create event callback to publish tool repair events
		eventCallback := func(stats *toolrepair.RepairStats, results []*toolrepair.RepairResult) {
			// Build event data
			details := make([]events.RepairDetail, 0, len(results))
			for _, r := range results {
				details = append(details, events.RepairDetail{
					ToolName:   r.ToolName,
					Success:    r.Success,
					Strategies: strings.Join(r.Strategies, ", "),
					Error:      r.Error,
				})
			}

			// Extract strategy names
			strategiesUsed := make([]string, 0, len(stats.StrategiesUsed))
			for strategy := range stats.StrategiesUsed {
				strategiesUsed = append(strategiesUsed, strategy)
			}

			h.publishEvent("tool_repair", events.ToolRepairEvent{
				RequestID:      rc.reqID,
				TotalToolCalls: stats.TotalToolCalls,
				Repaired:       stats.Repaired,
				Failed:         stats.Failed,
				StrategiesUsed: strategiesUsed,
				Duration:       stats.Duration.String(),
				Details:        details,
			})
		}

		// If fixer model is configured, create a fixer function
		if rc.conf.ToolRepair.FixerModel != "" {
			fixerFunc := func(ctx context.Context, model string, prompt string) (string, error) {
				// Resolve internal config for the fixer model
				provider, apiKey, baseURL, _, ok := h.config.ModelsConfig.ResolveInternalConfig(model)
				if !ok {
					return "", fmt.Errorf("failed to resolve fixer model: %s", model)
				}

				// Create provider client
				providerClient, err := providers.NewProvider(provider, apiKey, baseURL)
				if err != nil {
					return "", fmt.Errorf("failed to create fixer provider: %w", err)
				}

				// Build fixer request
				maxTokens := 2048
				temp := float64(0)
				req := &providers.ChatCompletionRequest{
					Model: model,
					Messages: []providers.ChatMessage{
						{Role: "system", Content: "You are a JSON repair tool. Fix malformed JSON and return ONLY the corrected JSON. No explanations, no markdown code blocks, just valid JSON."},
						{Role: "user", Content: prompt},
					},
					MaxTokens:   &maxTokens,
					Temperature: &temp,
				}

				// Call fixer
				resp, err := providerClient.ChatCompletion(ctx, req)
				if err != nil {
					return "", fmt.Errorf("fixer request failed: %w", err)
				}

				// Extract response
				if len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
					return "", fmt.Errorf("fixer returned empty response")
				}

				// Content is interface{}, need type assertion
				var contentStr string
				switch v := resp.Choices[0].Message.Content.(type) {
				case string:
					contentStr = v
				default:
					return "", fmt.Errorf("fixer returned non-string content: %T", resp.Choices[0].Message.Content)
				}
				return strings.TrimSpace(contentStr), nil
			}
			repairer.SetFixer(toolrepair.NewFixer(fixerFunc, &rc.conf.ToolRepair))
		}

		internalHandler.SetRepairer(repairer, eventCallback)
	}

	err := internalHandler.HandleRequest(attemptCtx, bodyToSend, w, rc.isStream)

	if err != nil {
		logger.Debugf("[DO-INTERNAL] Internal attempt %d failed: %v", attempt, err)

		// Check for client disconnection
		if rc.baseCtx.Err() == context.Canceled {
			log.Println("Client disconnected during internal request")
			rc.reqLog.Status = "failed"
			rc.reqLog.Error = "Client disconnected"
			rc.reqLog.EndTime = time.Now()
			rc.reqLog.Duration = time.Since(rc.startTime).String()
			h.store.Add(rc.reqLog)
			return attemptReturnImmediately
		}

		// Check for deadline exceeded
		if attemptCtx.Err() == context.DeadlineExceeded {
			log.Printf("Internal attempt %d generation deadline exceeded", attempt)
			h.publishEvent("error_deadline_exceeded", map[string]interface{}{"id": rc.reqID})
			counters.genRetries++
			return attemptContinueRetry
		}

		// Other errors - check if retryable
		log.Printf("Internal request failed: %v", err)

		// Check for ProviderError with BufferID
		eventData := map[string]interface{}{"error": err.Error(), "id": rc.reqID}
		var providerErr *providers.ProviderError
		if errors.As(err, &providerErr) {
			if providerErr.BufferID != "" {
				eventData["buffer_id"] = providerErr.BufferID
			}
			if providerErr.StatusCode > 0 {
				eventData["status"] = providerErr.StatusCode
			}
		}
		h.publishEvent("internal_error", eventData)

		// Check if error is retryable
		if errors.As(err, &providerErr) && !providerErr.Retryable {
			// Non-retryable error - break to fallback
			log.Printf("Internal error is non-retryable, breaking to fallback")
			return attemptBreakToFallback
		}

		counters.errorRetries++
		time.Sleep(500 * time.Millisecond)
		return attemptContinueRetry
	}

	// Success
	logger.Debugf("[DO-INTERNAL] Internal attempt %d succeeded", attempt)
	rc.reqLog.Status = "completed"
	rc.reqLog.EndTime = time.Now()
	rc.reqLog.Duration = time.Since(rc.startTime).String()
	h.store.Add(rc.reqLog)
	h.publishEvent("request_completed", map[string]interface{}{"id": rc.reqID})
	return attemptSuccess
}
