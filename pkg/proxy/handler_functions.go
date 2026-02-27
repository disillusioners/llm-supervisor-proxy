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
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/logger"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/loopdetection"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/supervisor"
	"github.com/google/uuid"
)

// ─────────────────────────────────────────────────────────────────────────────
// Initialization
// ─────────────────────────────────────────────────────────────────────────────

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
	h.publishEvent("upstream_error_status", map[string]interface{}{"status": statusCode, "body": string(bodyBytes), "id": rc.reqID})

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

// startSSEHeartbeat starts a goroutine that sends SSE comments every 5 seconds
// to keep the client connection alive while buffering upstream data.
// Returns a cancel function to stop the heartbeat.
func (h *Handler) startSSEHeartbeat(w http.ResponseWriter, ctx context.Context) context.CancelFunc {
	heartbeatCtx, cancel := context.WithCancel(ctx)

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				// Send SSE comment as heartbeat
				if _, err := w.Write([]byte(": heartbeat\n\n")); err != nil {
					return
				}
				log.Printf("Sent heartbeat at %s\n", time.Now().Format(time.RFC3339))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
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
func (h *Handler) handleStreamResponse(w http.ResponseWriter, rc *requestContext, resp *http.Response, monitor *supervisor.MonitoredReader, attemptCtx context.Context, attempt int, counters *retryCounters, heartbeatStop context.CancelFunc) attemptResult {
	// Ensure heartbeat is always stopped (prevents goroutine leak)
	// Note: We also stop it explicitly before writes to prevent race conditions
	var heartbeatStopped bool
	stopHeartbeat := func() {
		if !heartbeatStopped && heartbeatStop != nil {
			heartbeatStop()
			heartbeatStopped = true
		}
	}
	defer stopHeartbeat()

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

	// Use per-request detector (persists across retries within this request)
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

	for scanner.Scan() {
		// Check if client disconnected while we're buffering
		// This prevents wasting upstream resources on abandoned requests
		if rc.baseCtx.Err() != nil {
			log.Printf("Client disconnected during streaming, aborting")
			h.publishEvent("client_disconnected_during_buffering", map[string]interface{}{"id": rc.reqID, "bufferSize": rc.streamBuffer.Len()})
			monitor.Close()
			return attemptReturnImmediately
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
			extractStreamChunkContent(data, &rc.accumulatedResponse, &rc.accumulatedThinking)
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
	rc.reqLog.Response = rc.accumulatedResponse.String()
	rc.reqLog.Thinking = rc.accumulatedThinking.String()
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
