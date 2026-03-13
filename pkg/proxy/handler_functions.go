package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/logger"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/loopdetection"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/supervisor"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
	"github.com/google/uuid"
)

// ─────────────────────────────────────────────────────────────────────────────
// Initialization
// ─────────────────────────────────────────────────────────────────────────────

// shouldStartShadow checks if shadow retry should be triggered
func shouldStartShadow(rc *requestContext, counters *retryCounters) bool {
	// Must be enabled in config
	if !rc.conf.ShadowRetryEnabled {
		return false
	}
	// Only on first idle timeout
	if counters.idleRetries != 0 {
		return false
	}
	// Only if no data has been sent to client yet (buffer is empty)
	if rc.streamBuffer.Len() > 0 {
		return false
	}
	// Must have a fallback model available
	if len(rc.modelList) < 2 {
		return false
	}
	// Shadow must not already be running
	if rc.shadow != nil {
		return false
	}
	return true
}

// startShadowRequest spawns a background goroutine to make a parallel request
// to the fallback model. If shadow completes successfully before the main request,
// its buffer will be used instead.
func (h *Handler) startShadowRequest(rc *requestContext) {
	// Get fallback model (next in chain)
	shadowModelIndex := 1 // Always use first fallback for shadow
	if shadowModelIndex >= len(rc.modelList) {
		return
	}
	shadowModel := rc.modelList[shadowModelIndex]

	// Create shadow state
	shadowCtx, shadowCancel := context.WithCancel(rc.baseCtx)

	rc.shadow = &shadowRequestState{
		done:       make(chan shadowResult, 1),
		cancelFunc: shadowCancel,
		started:    true,
		model:      shadowModel,
		startTime:  time.Now(),
	}

	h.publishEvent("shadow_retry_started", map[string]interface{}{
		"id":      rc.reqID,
		"model":   shadowModel,
		"trigger": "idle_timeout",
	})

	log.Printf("[SHADOW] Starting shadow request to model %s for request %s", shadowModel, rc.reqID)

	go func() {
		defer shadowCancel()
		defer close(rc.shadow.done)

		// Build request body with shadow model
		shadowBody := make(map[string]interface{})
		for k, v := range rc.requestBody {
			shadowBody[k] = v
		}
		shadowBody["model"] = shadowModel

		// Clone body bytes
		newBodyBytes, _ := json.Marshal(shadowBody)

		// Create request with shadow context
		proxyReq, err := http.NewRequestWithContext(shadowCtx, rc.method, rc.targetURL, bytes.NewBuffer(newBodyBytes))
		if err != nil {
			rc.shadow.done <- shadowResult{err: err}
			return
		}

		copyHeaders(proxyReq, rc.originalHeaders)

		// Set auth if configured
		if rc.conf.UpstreamCredentialID != "" {
			proxyReq.Header.Del("Authorization")
			proxyReq.Header.Del("X-API-Key")
			proxyReq.Header.Del("x-api-key")
			proxyReq.Header.Del("api-key")
			cred := rc.conf.ModelsConfig.GetCredential(rc.conf.UpstreamCredentialID)
			if cred != nil {
				if apiKey := cred.ResolveAPIKey(); apiKey != "" {
					proxyReq.Header.Set("Authorization", "Bearer "+apiKey)
				}
			}
		}

		// Make request
		resp, err := h.client.Do(proxyReq)
		if err != nil {
			rc.shadow.done <- shadowResult{err: err}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			rc.shadow.done <- shadowResult{err: fmt.Errorf("shadow request failed with status %d: %s", resp.StatusCode, string(bodyBytes))}
			return
		}

		// Stream response to buffer
		buffer := &bytes.Buffer{}
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

		completed := false
		for scanner.Scan() {
			line := scanner.Bytes()
			buffer.Write(line)
			buffer.Write([]byte("\n"))

			// Check for [DONE] marker
			if bytes.HasPrefix(line, []byte("data: [DONE]")) {
				completed = true
				break
			}
		}

		if err := scanner.Err(); err != nil {
			rc.shadow.done <- shadowResult{err: err}
			return
		}

		rc.shadow.done <- shadowResult{
			buffer:    buffer,
			completed: completed,
		}
	}()
}

// initRequestContext parses the incoming request, creates the request log,
// resolves the fallback chain, and returns a fully populated requestContext.
func (h *Handler) initRequestContext(r *http.Request) (*requestContext, error) {
	conf := h.config.Clone()
	targetURL, err := url.JoinPath(conf.UpstreamURL, "/v1/chat/completions")
	if err != nil {
		return nil, fmt.Errorf("invalid_upstream_url")
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read_body_failed")
	}
	r.Body.Close()

	var requestBody map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &requestBody); err != nil {
		return nil, fmt.Errorf("invalid_json")
	}

	reqID := uuid.New().String()
	startTime := time.Now()

	storeMessages := parseMessages(requestBody)
	model, _ := requestBody["model"].(string)
	originalModel := model

	// Deep-copy original messages for retry reconstruction
	var originalMessages []interface{}
	if msgs, ok := requestBody["messages"].([]interface{}); ok {
		originalMessages = make([]interface{}, len(msgs))
		copy(originalMessages, msgs)
	}

	isStream := false
	if s, ok := requestBody["stream"].(bool); ok && s {
		isStream = true
	}

	// Extract parameters (exclude standard fields that are shown separately)
	parameters := extractParameters(requestBody)

	reqLog := &store.RequestLog{
		ID:            reqID,
		Status:        "running",
		Model:         model,
		OriginalModel: originalModel,
		StartTime:     startTime,
		Messages:      storeMessages,
		Retries:       0,
		FallbackUsed:  []string{},
		IsStream:      isStream,
		Parameters:    parameters,
	}
	h.store.Add(reqLog)

	modelList := buildModelList(originalModel, conf.ModelsConfig)

	// Extract proxy-only flags from headers (these are stripped before forwarding upstream)
	bypassInternal := strings.EqualFold(r.Header.Get("x-llmproxy-bypass-internal"), "true")

	return &requestContext{
		conf:             conf,
		targetURL:        targetURL,
		reqID:            reqID,
		startTime:        startTime,
		reqLog:           reqLog,
		modelList:        modelList,
		requestBody:      requestBody,
		isStream:         isStream,
		originalHeaders:  r.Header,
		method:           r.Method,
		baseCtx:          r.Context(),
		originalMessages: originalMessages,
		bypassInternal:   bypassInternal,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Per-model attempt loop
// ─────────────────────────────────────────────────────────────────────────────

// attemptModel runs the retry loop for a single model. Returns true if the
// request completed successfully (response has been written to w).
func (h *Handler) attemptModel(w http.ResponseWriter, rc *requestContext, modelIndex int, currentModel string) bool {
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

// ─────────────────────────────────────────────────────────────────────────────
// Error handling
// ─────────────────────────────────────────────────────────────────────────────

// handleUpstreamRequestError handles errors from h.client.Do (connection errors,
// deadline exceeded, client disconnected, etc.)
func (h *Handler) handleUpstreamRequestError(rc *requestContext, err error, attemptCtx context.Context, attempt int, counters *retryCounters) attemptResult {
	counters.lastErr = err

	if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
		log.Printf("Attempt %d generation deadline exceeded", attempt)
		h.publishEvent("error_deadline_exceeded", map[string]interface{}{"id": rc.reqID})
		counters.genRetries++
		return attemptContinueRetry
	}

	if errors.Is(rc.baseCtx.Err(), context.Canceled) {
		log.Println("Client disconnected")
		rc.reqLog.Status = "failed"
		rc.reqLog.Error = "Client disconnected"
		rc.reqLog.EndTime = time.Now()
		rc.reqLog.Duration = time.Since(rc.startTime).String()
		h.store.Add(rc.reqLog)
		return attemptReturnImmediately
	}

	log.Printf("Upstream request failed: %v", err)
	h.publishEvent("upstream_error", map[string]interface{}{"error": err.Error(), "id": rc.reqID})
	counters.errorRetries++
	time.Sleep(500 * time.Millisecond)
	return attemptContinueRetry
}

// handleNonOKStatus handles HTTP responses with non-200 status codes.
// All upstream errors are retried (not passed through to client).
// 4xx errors are logged for debugging but still trigger retry/fallback.
func (h *Handler) handleNonOKStatus(w http.ResponseWriter, rc *requestContext, resp *http.Response, modelIndex int, counters *retryCounters) attemptResult {
	statusCode := resp.StatusCode

	// Log all error responses for debugging
	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	log.Printf("Upstream returned %d. Error body: %s", statusCode, string(bodyBytes))

	// Save request body to file for debugging and include buffer_id in event
	eventData := map[string]interface{}{
		"status": statusCode,
		"body":   string(bodyBytes),
		"id":     rc.reqID,
	}
	if h.bufferStore != nil {
		if requestJSON, err := json.MarshalIndent(rc.requestBody, "", "  "); err == nil {
			bufferID := fmt.Sprintf("%s_request", rc.reqID)
			if saveErr := h.bufferStore.Save(bufferID, requestJSON); saveErr != nil {
				log.Printf("Warning: failed to save request body: %v", saveErr)
			} else {
				eventData["buffer_id"] = bufferID
			}
		}
	}
	h.publishEvent("upstream_error_status", eventData)

	if !rc.headersSent {
		// If there's a fallback model available, try it first
		if modelIndex+1 < len(rc.modelList) {
			log.Printf("Upstream returned %d for model %s. Triggering fallback.", statusCode, rc.requestBody["model"])
			return attemptBreakToFallback
		}

		// No fallback available - retry within same model
		// All error codes (4xx, 5xx, 429) trigger retry
		log.Printf("Upstream returned %d (no fallback). Retrying within same model.", statusCode)
		counters.errorRetries++
		time.Sleep(1 * time.Second)
		return attemptContinueRetry
	}

	// Headers already sent (stream retry scenario)
	// The original stream started successfully but failed mid-way, triggering a retry.
	// If the retry attempt also fails, prioritize fallback over retrying the same model.

	// If there's a fallback model available, try it immediately
	if modelIndex+1 < len(rc.modelList) {
		log.Printf("Upstream returned %d for model %s during stream retry. Triggering fallback.", statusCode, rc.requestBody["model"])
		return attemptBreakToFallback
	}

	// No fallback available - retry within same model
	log.Printf("Upstream returned %d during stream retry (headers already sent, no fallback). Retrying.", statusCode)
	h.publishEvent("upstream_error_status_retry", map[string]interface{}{"status": statusCode, "id": rc.reqID})
	counters.errorRetries++
	time.Sleep(1 * time.Second)
	return attemptContinueRetry
}

// handleReadError categorizes a read error (idle timeout, deadline exceeded,
// or generic stream error) and increments the appropriate retry counter.
// Even if headers were already sent (streaming), we retry silently - the client
// just sees a pause while we attempt fallback. Only when all models exhaust
// their retries do we send an SSE error event (handled by the outer loop).
func (h *Handler) handleReadError(w http.ResponseWriter, rc *requestContext, monitor *supervisor.MonitoredReader, err error, attemptCtx context.Context, attempt int, counters *retryCounters) attemptResult {
	counters.lastErr = err

	// CHECK FOR STREAMING NON-RETRYABLE STATE FIRST
	// If the request is marked as non-retryable (after release deadline), return immediately
	// This prevents retry attempts after content has already been sent to the client
	if rc.streamingNonRetryable {
		log.Printf("[STREAM_NON_RETRYABLE] Request marked non-retryable after deadline flush, returning immediately (error: %v)", err)
		h.publishEvent("stream_non_retryable_error", map[string]interface{}{
			"id":         rc.reqID,
			"error":      err.Error(),
			"bufferSize": rc.streamBuffer.Len(),
		})
		monitor.Close()
		// Send SSE error to client since we can't retry
		h.sendSSEError(w, fmt.Sprintf("Stream error after deadline: %v", err))
		return attemptReturnImmediately
	}

	// Check for client disconnection FIRST - this is not an error, client just left
	// This can manifest as "context canceled" when the base context is canceled
	if rc.baseCtx.Err() == context.Canceled {
		log.Println("Client disconnected during stream read")
		rc.reqLog.Status = "failed"
		rc.reqLog.Error = "Client disconnected"
		rc.reqLog.EndTime = time.Now()
		rc.reqLog.Duration = time.Since(rc.startTime).String()
		h.store.Add(rc.reqLog)
		monitor.Close()
		return attemptReturnImmediately
	}

	// Log if headers already sent (for observability), but still retry
	if rc.headersSent {
		// Log accumulated buffer for debugging false positive stream errors
		bufferPreview := rc.streamBuffer.String()
		log.Printf("[STREAM_ERROR_AFTER_HEADERS_DEBUG] Error: %v, Buffer so far (%d bytes): %s", err, rc.streamBuffer.Len(), bufferPreview)
		log.Printf("Stream error after headers sent (will retry silently): %v", err)

		// Save buffer content to file and publish buffer_id instead of full content
		bufferID := fmt.Sprintf("%s_buffer", rc.reqID)
		eventData := map[string]interface{}{
			"error":       err.Error(),
			"id":          rc.reqID,
			"buffer_size": rc.streamBuffer.Len(),
		}

		// Save buffer to file if BufferStore is available
		if h.bufferStore != nil && rc.streamBuffer.Len() > 0 {
			if saveErr := h.bufferStore.Save(bufferID, rc.streamBuffer.Bytes()); saveErr != nil {
				log.Printf("Warning: failed to save buffer content: %v", saveErr)
			} else {
				eventData["buffer_id"] = bufferID
			}
		}

		h.publishEvent("stream_error_after_headers", eventData)
	}

	if errors.Is(err, supervisor.ErrIdleTimeout) {
		log.Println("Stream idle timeout detected!")
		h.publishEvent("timeout_idle", map[string]interface{}{"timeout": rc.conf.IdleTimeout.String(), "id": rc.reqID})

		// TRIGGER SHADOW RETRY on first idle timeout
		// This starts a parallel request to fallback model while main continues retrying
		if shouldStartShadow(rc, counters) {
			h.startShadowRequest(rc)
		}

		monitor.Close()
		counters.idleRetries++
		return attemptContinueRetry
	}

	if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
		log.Printf("Attempt %d generation deadline exceeded", attempt)
		h.publishEvent("error_deadline_exceeded", map[string]interface{}{"id": rc.reqID})
		monitor.Close()
		counters.genRetries++
		return attemptContinueRetry
	}

	log.Printf("Stream error: %v", err)
	h.publishEvent("stream_error", map[string]interface{}{"error": err.Error(), "id": rc.reqID})
	monitor.Close()
	counters.errorRetries++
	return attemptContinueRetry
}

// sendSSEError sends an error as an SSE event to the client.
// This is used when a streaming error occurs after headers have been sent,
// so we can't send a regular HTTP error response.
func (h *Handler) sendSSEError(w http.ResponseWriter, message string) {
	errorEvent := fmt.Sprintf("event: error\ndata: {\"error\": %q}\n\n", message)
	w.Write([]byte(errorEvent))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// startSSEHeartbeat starts a goroutine that sends SSE comments every 10 seconds
// to keep the client connection alive while buffering upstream data.
// Returns a cancel function to stop the heartbeat.
func (h *Handler) startSSEHeartbeat(w http.ResponseWriter, ctx context.Context) context.CancelFunc {
	heartbeatCtx, cancel := context.WithCancel(ctx)

	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				// Send SSE comment as heartbeat - use non-blocking write with timeout
				// to prevent blocking the select loop if the client TCP buffer is full
				heartbeatData := []byte(": heartbeat\n\n")
				written := make(chan bool, 1)

				// Use WaitGroup to ensure goroutine completes before exiting
				var wg sync.WaitGroup
				wg.Add(1)

				go func() {
					defer wg.Done()
					_, err := w.Write(heartbeatData)
					if err != nil {
						log.Printf("[HEARTBEAT] Write error: %v", err)
					}
					// Use non-blocking send to prevent goroutine leak
					select {
					case written <- (err == nil):
					default:
					}
				}()

				// Wait for write to complete or context canceled, with timeout
				select {
				case <-heartbeatCtx.Done():
					wg.Wait() // Wait for goroutine to complete before returning
					return
				case ok := <-written:
					if ok {
						log.Printf("Sent heartbeat at %s\n", time.Now().Format(time.RFC3339))
						if f, ok := w.(http.Flusher); ok {
							f.Flush()
						}
					}
					wg.Wait() // Ensure goroutine completes
				case <-time.After(3 * time.Second):
					// Timeout - heartbeat write took too long
					log.Printf("[HEARTBEAT] Write timeout, client may be slow or disconnected")
					wg.Wait() // Wait for goroutine to complete before continuing
				}
			}
		}
	}()

	return cancel
}

// ─────────────────────────────────────────────────────────────────────────────
// Response processing (OK status)
// ─────────────────────────────────────────────────────────────────────────────

// handleOKResponse handles a 200 OK upstream response,// dispatching to either non-streaming or streaming processing.
// For streaming: headers are sent IMMEDIATELY to establish SSE connection (solves TTFB),
// while body content is buffered until [DONE] for retry safety.
func (h *Handler) handleOKResponse(w http.ResponseWriter, rc *requestContext, resp *http.Response, attemptCtx context.Context, attempt int, counters *retryCounters) attemptResult {
	monitor := supervisor.NewMonitoredReader(resp.Body, rc.conf.IdleTimeout)

	// For streaming: send headers immediately to establish SSE connection
	// This solves TTFB/TTFT timeout issues while body remains buffered for retry safety
	if rc.isStream && !rc.headersSent {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		// Copy relevant headers from upstream
		if v := resp.Header.Get("X-Request-Id"); v != "" {
			w.Header().Set("X-Request-Id", v)
		}
		w.WriteHeader(http.StatusOK)
		rc.headersSent = true

		// Send initial SSE comment to establish byte stream (prevents TTFB timeouts)
		w.Write([]byte(": connected\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}

	// Start heartbeat for streaming responses to keep connection alive while buffering
	var heartbeatStop context.CancelFunc
	if rc.isStream {
		heartbeatStop = h.startSSEHeartbeat(w, rc.baseCtx)
	}

	// For non-streaming: send headers immediately (no retry risk since full body is read)
	if !rc.isStream && !rc.headersSent {
		// Copy response headers
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(http.StatusOK)
		rc.headersSent = true
	}

	if !rc.isStream {
		return h.handleNonStreamResponse(w, rc, monitor, attemptCtx, attempt, counters)
	}
	return h.handleStreamResponse(w, rc, resp, monitor, attemptCtx, attempt, counters, heartbeatStop)
}

// handleNonStreamResponse reads the entire response body and writes it to the client.
func (h *Handler) handleNonStreamResponse(w http.ResponseWriter, rc *requestContext, monitor *supervisor.MonitoredReader, attemptCtx context.Context, attempt int, counters *retryCounters) attemptResult {
	bodyBytes, err := io.ReadAll(monitor)
	if err != nil {
		return h.handleReadError(w, rc, monitor, err, attemptCtx, attempt, counters)
	}

	w.Write(bodyBytes)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	extractNonStreamContent(bodyBytes, &rc.accumulatedResponse, &rc.accumulatedThinking)

	h.publishEvent("request_completed", map[string]interface{}{"id": rc.reqID})
	h.finalizeSuccess(rc)
	monitor.Close()
	return attemptSuccess
}

// handleStreamResponse processes a Server-Sent Events stream with buffering.
// All chunks are buffered in memory until the stream completes successfully.
// This enables safe retry mid-stream since nothing is sent to client until [DONE].
func (h *Handler) handleStreamResponse(w http.ResponseWriter, rc *requestContext, resp *http.Response, monitor *supervisor.MonitoredReader, attemptCtx context.Context, attempt int, counters *retryCounters, heartbeatStop context.CancelFunc) (result attemptResult) {
	// Panic recovery to ensure resources are cleaned up even on unexpected errors
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC recovered in handleStreamResponse: %v", r)
			result = attemptBreakToFallback
		}
	}()

	// Ensure heartbeat is always stopped (prevents goroutine leak)
	// Note: We also stop it explicitly before writes to prevent race conditions
	var heartbeatStopped bool
	stopHeartbeat := func() {
		if !heartbeatStopped && heartbeatStop != nil {
			heartbeatStop()
			heartbeatStopped = true
		}
	}
	defer func() {
		stopHeartbeat()
		// Ensure monitor is always closed (prevents connection leak)
		if monitor != nil {
			monitor.Close()
		}
	}()

	scanner := bufio.NewScanner(monitor)
	buffer := make([]byte, 0, 1024*1024)
	scanner.Buffer(buffer, 1024*1024)

	streamEndedSuccessfully := false

	// Clear any previous buffer content from failed attempts
	rc.streamBuffer.Reset()
	rc.accumulatedResponse.Reset()
	rc.accumulatedThinking.Reset()
	// Reset stream ID caching to get fresh ID from new upstream
	rc.streamIDSet = false
	rc.streamID = ""

	// Reset loop detector state between retries to prevent memory accumulation
	if rc.loopDetector != nil {
		rc.loopDetector.Reset()
	}

	// Initialize detector if first attempt (detector state is reset between retries above)
	if rc.loopDetector == nil {
		ldCfg := loopdetection.Config{
			Enabled:              rc.conf.LoopDetection.Enabled,
			ShadowMode:           rc.conf.LoopDetection.ShadowMode,
			MessageWindow:        rc.conf.LoopDetection.MessageWindow,
			ActionWindow:         rc.conf.LoopDetection.ActionWindow,
			ExactMatchCount:      rc.conf.LoopDetection.ExactMatchCount,
			SimilarityThreshold:  rc.conf.LoopDetection.SimilarityThreshold,
			MinTokensForSimHash:  rc.conf.LoopDetection.MinTokensForSimHash,
			ActionRepeatCount:    rc.conf.LoopDetection.ActionRepeatCount,
			OscillationCount:     rc.conf.LoopDetection.OscillationCount,
			MinTokensForAnalysis: rc.conf.LoopDetection.MinTokensForAnalysis,
			// Phase 3: Advanced detection
			ThinkingMinTokens:         rc.conf.LoopDetection.ThinkingMinTokens,
			TrigramThreshold:          rc.conf.LoopDetection.TrigramThreshold,
			MaxCycleLength:            rc.conf.LoopDetection.MaxCycleLength,
			ReasoningModelPatterns:    rc.conf.LoopDetection.ReasoningModelPatterns,
			ReasoningTrigramThreshold: rc.conf.LoopDetection.ReasoningTrigramThreshold,
		}
		rc.loopDetector = loopdetection.NewDetector(ldCfg)
	}
	detector := rc.loopDetector
	streamBuf := detector.NewStreamBuffer()

	// ─────────────────────────────────────────────────────────────────────────────
	// RELEASE STREAM CHUNK DEADLINE
	// After this duration, flush buffered content to downstream even if stream hasn't completed.
	// This prevents clients with idle chunk detection from dropping the connection.
	// Once deadline is reached:
	// 1. Flush buffer to downstream
	// 2. Mark request as non-retryable (streamingNonRetryable=true)
	// 3. Continue streaming without buffering
	//
	// Note: If releaseDeadline is 0, the feature is disabled (stream buffers until [DONE]).
	// ─────────────────────────────────────────────────────────────────────────────
	var releaseDeadline time.Duration
	currentModel, _ := rc.requestBody["model"].(string)
	if rc.conf.ModelsConfig != nil {
		if modelCfg := rc.conf.ModelsConfig.GetModel(currentModel); modelCfg != nil {
			releaseDeadline = modelCfg.GetReleaseStreamChunkDeadline()
		}
	}
	// Only set deadlineTime if deadline is enabled (> 0)
	var deadlineTime time.Time
	var deadlineReached bool
	if releaseDeadline > 0 {
		deadlineTime = rc.startTime.Add(releaseDeadline)
	}

	for scanner.Scan() {
		// CHECK RELEASE DEADLINE FIRST
		// If deadline has passed and we haven't flushed yet, flush buffer now
		// Only check if deadline is enabled (releaseDeadline > 0)
		if releaseDeadline > 0 && !deadlineReached && time.Now().After(deadlineTime) && rc.streamBuffer.Len() > 0 {
			log.Printf("[RELEASE_DEADLINE] Flushing buffer after %v (deadline: %v, bufferSize: %d)",
				time.Since(rc.startTime), releaseDeadline, rc.streamBuffer.Len())
			h.publishEvent("stream_chunk_deadline", events.StreamChunkDeadlineEvent{
				RequestID:  rc.reqID,
				Deadline:   releaseDeadline.String(),
				BufferSize: rc.streamBuffer.Len(),
				Elapsed:    time.Since(rc.startTime).String(),
			})

			// Mark request as non-retryable - content has been sent to client
			rc.streamingNonRetryable = true
			deadlineReached = true

			// Stop heartbeat before flush to prevent race condition
			stopHeartbeat()

			// Flush entire buffer to downstream
			if _, err := w.Write(rc.streamBuffer.Bytes()); err != nil {
				log.Printf("[RELEASE_DEADLINE] Failed to flush buffer: %v", err)
				monitor.Close()
				return attemptReturnImmediately
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}

			// Clear buffer for any subsequent content
			rc.streamBuffer.Reset()

			// Note: We don't restart heartbeat here because after deadline:
			// 1. streamingNonRetryable=true means no more buffering
			// 2. Content is streamed directly to client
			// 3. Direct streaming keeps connection alive naturally
		}

		// Check total stream duration (MaxGenerationTime) to prevent indefinite streams
		// This catches slow upstreams that send tokens within IdleTimeout but never complete
		if attemptCtx.Err() != nil {
			log.Printf("Stream exceeded max generation time (%v), aborting", rc.conf.MaxGenerationTime)
			h.publishEvent("stream_max_generation_time_exceeded", map[string]interface{}{"id": rc.reqID, "duration": time.Since(rc.startTime).String()})
			monitor.Close()
			counters.genRetries++
			return attemptContinueRetry
		}

		// Check if client disconnected while we're buffering
		// This prevents wasting upstream resources on abandoned requests
		if rc.baseCtx.Err() != nil {
			log.Printf("Client disconnected during streaming, aborting")
			h.publishEvent("client_disconnected_during_buffering", map[string]interface{}{"id": rc.reqID, "bufferSize": rc.streamBuffer.Len()})
			monitor.Close()
			return attemptReturnImmediately
		}

		// CHECK SHADOW RETRY RESULT
		// If a shadow request is running and has completed successfully, use its buffer instead
		if rc.shadow != nil {
			select {
			case result := <-rc.shadow.done:
				if result.err == nil && result.completed && result.buffer != nil {
					log.Printf("[SHADOW] Shadow request completed successfully, using shadow buffer (model: %s)", rc.shadow.model)
					h.publishEvent("shadow_retry_won", map[string]interface{}{
						"id":        rc.reqID,
						"model":     rc.shadow.model,
						"duration":  time.Since(rc.shadow.startTime).String(),
						"mainModel": currentModel,
					})

					// Cancel shadow context to signal completion
					if rc.shadow.cancelFunc != nil {
						rc.shadow.cancelFunc()
					}

					// Swap buffers - use shadow's completed buffer instead of main
					rc.streamBuffer = *result.buffer

					// Update accumulated response from shadow buffer
					rc.accumulatedResponse.Reset()
					rc.accumulatedThinking.Reset()
					// Parse shadow buffer to extract content
					shadowScanner := bufio.NewScanner(bytes.NewReader(rc.streamBuffer.Bytes()))
					shadowScanner.Buffer(make([]byte, 1024*1024), 1024*1024)
					for shadowScanner.Scan() {
						shadowLine := shadowScanner.Bytes()
						if bytes.HasPrefix(shadowLine, []byte("data: ")) {
							data := bytes.TrimPrefix(shadowLine, []byte("data: "))
							if string(data) != "[DONE]" {
								extractStreamChunkContent(data, &rc.accumulatedResponse, &rc.accumulatedThinking, nil)
							}
						}
					}

					// Mark shadow as completed
					rc.shadow.mu.Lock()
					rc.shadow.completed = true
					rc.shadow.mu.Unlock()

					// Stop heartbeat and monitor
					stopHeartbeat()
					monitor.Close()

					// Flush shadow buffer to client
					w.Write(rc.streamBuffer.Bytes())
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}

					h.publishEvent("request_completed", map[string]interface{}{"id": rc.reqID})
					h.finalizeSuccess(rc)
					return attemptSuccess
				} else if result.err != nil {
					log.Printf("[SHADOW] Shadow request failed: %v", result.err)
					h.publishEvent("shadow_retry_failed", map[string]interface{}{
						"id":    rc.reqID,
						"model": rc.shadow.model,
						"error": result.err.Error(),
					})
					// Mark shadow as completed (failed) so we don't check again
					rc.shadow.mu.Lock()
					rc.shadow.completed = true
					rc.shadow.mu.Unlock()
				}
			default:
				// No shadow result yet, continue with main stream
			}
		}

		line := scanner.Bytes()

		// Check if this line is an error response dumped into the stream
		// (happens when upstream crashes mid-stream and dumps raw error JSON)
		if errorMsg := isStreamErrorChunk(line); errorMsg != "" {
			// Log raw data at info level for debugging false positives
			log.Printf("[STREAM_ERROR_CHUNK_DEBUG] Raw upstream data (first 500 chars): %.500s", string(line))
			log.Printf("Stream error chunk detected: %s", errorMsg)
			h.publishEvent("stream_error_chunk", map[string]interface{}{
				"error":    errorMsg,
				"id":       rc.reqID,
				"raw_data": string(line),
			})
			monitor.Close()
			counters.errorRetries++
			return attemptContinueRetry
		}

		// Check if this is a data line
		if bytes.HasPrefix(line, []byte("data: ")) {
			data := bytes.TrimPrefix(line, []byte("data: "))

			// Validate JSON - skip corrupted/incomplete chunks
			if !isValidStreamChunk(data) {
				log.Printf("Skipping invalid/incomplete JSON chunk: %s", string(data)[:min(100, len(data))])
				continue
			}

			// Normalize chunk - rewrite ID and strip role for transparent fallbacks
			normalizedData := normalizeStreamChunk(data, rc)

			// Buffer the normalized chunk (don't send to client yet)
			rc.streamBuffer.Write([]byte("data: "))
			rc.streamBuffer.Write(normalizedData)
			rc.streamBuffer.Write([]byte("\n"))

			// Check buffer size limit
			// If MaxStreamBufferSize is configured, use that; otherwise use 100MB hard cap
			bufferLimit := rc.conf.MaxStreamBufferSize
			if bufferLimit <= 0 {
				bufferLimit = 100 * 1024 * 1024 // 100MB hard cap when unlimited
			}
			if rc.streamBuffer.Len() > bufferLimit {
				log.Printf("Stream buffer exceeded limit (%d > %d bytes)", rc.streamBuffer.Len(), bufferLimit)
				h.publishEvent("stream_buffer_overflow", map[string]interface{}{"size": rc.streamBuffer.Len(), "limit": bufferLimit, "id": rc.reqID})
				monitor.Close()
				counters.errorRetries++
				return attemptContinueRetry
			}

			// Continue processing for [DONE] check and content extraction
			if string(normalizedData) == "[DONE]" {
				streamEndedSuccessfully = true
				rc.streamBuffer.Write([]byte("\n"))

				// Flush remaining buffer for final analysis
				if detector.IsEnabled() {
					if text, actions := streamBuf.Flush(); len(text) > 0 || len(actions) > 0 {
						if result := detector.Analyze(text, actions); result != nil && result.LoopDetected {
							h.publishLoopEvent(rc.reqID, result, detector.IsShadowMode())
						}
					}
				}
				break
			}

			// Carry for content/loop extraction
			data = normalizedData

			// Track chunk content for both existing accumulation and loop detection
			prevLen := rc.accumulatedResponse.Len()
			prevThinkLen := rc.accumulatedThinking.Len()
			extractStreamChunkContent(data, &rc.accumulatedResponse, &rc.accumulatedThinking, &rc.accumulatedToolCalls)
			newContent := rc.accumulatedResponse.String()[prevLen:]
			newThinking := rc.accumulatedThinking.String()[prevThinkLen:]

			// Feed content to loop detection buffer
			if detector.IsEnabled() {
				// Add text content
				if len(newContent) > 0 {
					streamBuf.AddText(newContent)
				}

				// Feed thinking content to ThinkingStrategy for trigram analysis
				if len(newThinking) > 0 {
					detector.AddThinkingContent(newThinking)
				}

				// Extract and add tool call actions from chunk
				if toolActions := extractToolCallActions(data); len(toolActions) > 0 {
					for _, action := range toolActions {
						streamBuf.AddAction(action)
					}
				}

				// Run analysis when buffer threshold is met
				if streamBuf.ShouldAnalyze(false) {
					text, actions := streamBuf.Flush()
					if result := detector.Analyze(text, actions); result != nil && result.LoopDetected {
						h.publishLoopEvent(rc.reqID, result, detector.IsShadowMode())

						// Phase 4: Hard interruption (when NOT in shadow mode)
						if !detector.IsShadowMode() && result.Severity == loopdetection.SeverityCritical {
							log.Printf("[LOOP-DETECTION][INTERRUPT] Stopping stream — %s: %s", result.Strategy, result.Evidence)
							h.publishEvent("loop_interrupted", events.LoopDetectionEvent{
								RequestID:   rc.reqID,
								Strategy:    result.Strategy,
								Severity:    result.Severity.String(),
								Evidence:    result.Evidence,
								Confidence:  result.Confidence,
								Pattern:     result.Pattern,
								RepeatCount: result.RepeatCount,
								ShadowMode:  false,
							})

							// Sanitize context window and trigger retry/fallback
							h.sanitizeAndRetry(rc, result)
							monitor.Close()
							counters.errorRetries++
							return attemptContinueRetry
						}
					}
				}
			}
		} else {
			// Buffer any other content (empty lines, SSE comments, etc.)
			rc.streamBuffer.Write(line)
			rc.streamBuffer.Write([]byte("\n"))
		}
	}

	// Handle scanner error - check for client disconnection first
	if err := scanner.Err(); err != nil {
		// Check if this is a client disconnection (context canceled)
		// This can happen when the downstream client disconnects mid-stream
		if rc.baseCtx.Err() == context.Canceled || errors.Is(err, context.Canceled) {
			log.Printf("Client disconnected during stream scan, aborting (buffered %d bytes)", rc.streamBuffer.Len())
			h.publishEvent("client_disconnected_during_scan", map[string]interface{}{"id": rc.reqID, "bufferSize": rc.streamBuffer.Len()})
			rc.reqLog.Status = "failed"
			rc.reqLog.Error = "Client disconnected"
			rc.reqLog.EndTime = time.Now()
			rc.reqLog.Duration = time.Since(rc.startTime).String()
			h.store.Add(rc.reqLog)
			monitor.Close()
			return attemptReturnImmediately
		}
		return h.handleReadError(w, rc, monitor, err, attemptCtx, attempt, counters)
	}

	// Stream completed successfully - flush buffered body to client
	// Note: Headers were already sent immediately when upstream responded (TTFB fix)
	if streamEndedSuccessfully {
		// Cancel shadow request if running (main succeeded first)
		if rc.shadow != nil && rc.shadow.cancelFunc != nil {
			log.Printf("[SHADOW] Main stream succeeded, cancelling shadow request")
			h.publishEvent("shadow_retry_lost", map[string]interface{}{
				"id":        rc.reqID,
				"model":     rc.shadow.model,
				"duration":  time.Since(rc.shadow.startTime).String(),
				"mainModel": currentModel,
			})
			rc.shadow.cancelFunc()
		}

		// Stop heartbeat BEFORE writing to prevent race condition
		stopHeartbeat()

		// Flush entire buffer to client
		w.Write(rc.streamBuffer.Bytes())
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		h.publishEvent("request_completed", map[string]interface{}{"id": rc.reqID})
		h.finalizeSuccess(rc)
		monitor.Close()
		return attemptSuccess
	}

	// Stream ended without [DONE] and without error — unexpected EOF
	log.Println("Stream ended unexpectedly without [DONE]")
	h.publishEvent("stream_ended_unexpectedly", map[string]interface{}{"id": rc.reqID})
	monitor.Close()

	// Stop heartbeat BEFORE writing to prevent race condition
	stopHeartbeat()

	// Headers already sent - send error as SSE event
	h.sendSSEError(w, "Stream ended unexpectedly without [DONE]")
	return attemptReturnImmediately
}

// publishLoopEvent emits a typed loop detection event to the event bus.
func (h *Handler) publishLoopEvent(reqID string, result *loopdetection.DetectionResult, shadowMode bool) {
	h.publishEvent("loop_detected", events.LoopDetectionEvent{
		RequestID:   reqID,
		Strategy:    result.Strategy,
		Severity:    result.Severity.String(),
		Evidence:    result.Evidence,
		Confidence:  result.Confidence,
		Pattern:     result.Pattern,
		RepeatCount: result.RepeatCount,
		ShadowMode:  shadowMode,
	})
}

// sanitizeAndRetry modifies the request context's message history to break the
// loop pattern. It removes repetitive messages and injects a system prompt
// telling the model to take a different approach.
func (h *Handler) sanitizeAndRetry(rc *requestContext, result *loopdetection.DetectionResult) {
	messages, ok := rc.requestBody["messages"].([]interface{})
	if !ok {
		return
	}

	// Convert to the format expected by SanitizeLoopHistory
	msgMaps := make([]map[string]interface{}, 0, len(messages))
	for _, m := range messages {
		if mm, ok := m.(map[string]interface{}); ok {
			msgMaps = append(msgMaps, mm)
		} else if mm, ok := m.(map[string]string); ok {
			// Convert string map to interface map
			converted := make(map[string]interface{}, len(mm))
			for k, v := range mm {
				converted[k] = v
			}
			msgMaps = append(msgMaps, converted)
		}
	}

	// Also append the accumulated partial response as an assistant message
	if rc.accumulatedResponse.Len() > 0 {
		msgMaps = append(msgMaps, map[string]interface{}{
			"role":    "assistant",
			"content": rc.accumulatedResponse.String(),
		})
	}

	sanitized := loopdetection.SanitizeLoopHistory(msgMaps, result)

	// Convert back to []interface{}
	newMessages := make([]interface{}, len(sanitized))
	for i, m := range sanitized {
		newMessages[i] = m
	}
	rc.requestBody["messages"] = newMessages

	// Reset accumulated response since we're starting fresh
	rc.accumulatedResponse.Reset()
	rc.accumulatedThinking.Reset()

	log.Printf("[LOOP-DETECTION] Context sanitized: %d → %d messages", len(messages), len(sanitized))
}

// ─────────────────────────────────────────────────────────────────────────────
// Finalization
// ─────────────────────────────────────────────────────────────────────────────

// finalizeSuccess updates the request log with the completed status.
func (h *Handler) finalizeSuccess(rc *requestContext) {
	rc.reqLog.Status = "completed"

	// Append assistant message to Messages array
	// This is the source of response content - no separate Response/Thinking fields needed
	assistantMsg := store.Message{
		Role:     "assistant",
		Content:  rc.accumulatedResponse.String(),
		Thinking: rc.accumulatedThinking.String(),
	}

	// Include tool calls if any were accumulated
	if len(rc.accumulatedToolCalls) > 0 {
		assistantMsg.ToolCalls = rc.accumulatedToolCalls
	}

	rc.reqLog.Messages = append(rc.reqLog.Messages, assistantMsg)

	rc.reqLog.EndTime = time.Now()
	rc.reqLog.Duration = time.Since(rc.startTime).String()
	h.store.Add(rc.reqLog)
}

// handleModelFailure updates the request log and publishes events when a model
// has exhausted all retries.
func (h *Handler) handleModelFailure(rc *requestContext, modelIndex int, currentModel string) {
	log.Printf("Model %s failed (retries exhausted or unrecoverable error)", currentModel)
	h.publishEvent("error_max_upstream_error_retries", map[string]interface{}{"id": rc.reqID})

	rc.reqLog.Status = "failed"
	rc.reqLog.Error = "Model failed"
	rc.reqLog.EndTime = time.Now()
	rc.reqLog.Duration = time.Since(rc.startTime).String()
	h.store.Add(rc.reqLog)

	// Notify about fallback transition
	if modelIndex+1 < len(rc.modelList) {
		nextModel := rc.modelList[modelIndex+1]
		rc.reqLog.CurrentFallback = nextModel

		h.publishEvent("fallback_triggered", map[string]interface{}{
			"id":         rc.reqID,
			"from_model": currentModel,
			"to_model":   nextModel,
			"reason":     "upstream_error",
		})
	}

	// Only track in "FallbackUsed" if the model that *just failed* was actually a fallback
	if modelIndex > 0 {
		rc.reqLog.FallbackUsed = append(rc.reqLog.FallbackUsed, currentModel)
	}
}
