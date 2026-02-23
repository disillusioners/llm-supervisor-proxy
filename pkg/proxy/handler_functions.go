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

	reqLog := &store.RequestLog{
		ID:            reqID,
		Status:        "running",
		Model:         model,
		OriginalModel: originalModel,
		StartTime:     startTime,
		Messages:      storeMessages,
		Retries:       0,
		FallbackUsed:  []string{},
	}
	h.store.Add(reqLog)

	modelList := buildModelList(originalModel, conf.ModelsConfig)

	isStream := false
	if s, ok := requestBody["stream"].(bool); ok && s {
		isStream = true
	}

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

// prepareRetry updates request log and modifies the request body for retry.
// It rebuilds the messages array from originalMessages to prevent duplication
// across multiple retry attempts.
// For streaming responses where headers are already sent, it sends SSE keep-alive
// comments to prevent client timeout during the retry.
func (h *Handler) prepareRetry(w http.ResponseWriter, rc *requestContext, attempt int, counters *retryCounters) {
	log.Printf("Retrying request (attempt %d)...", attempt)
	rc.reqLog.Retries = attempt
	rc.reqLog.Status = "retrying"
	h.store.Add(rc.reqLog)

	h.publishEvent("retry_attempt", map[string]interface{}{"attempt": attempt, "id": rc.reqID})

	// For streams where headers are already sent, send keep-alive comment
	// to prevent client timeout while we retry
	if rc.isStream && rc.headersSent {
		keepAliveMsg := fmt.Sprintf(": retrying-attempt-%d\n", attempt)
		w.Write([]byte(keepAliveMsg))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}

	if rc.isStream {
		// Rebuild messages from the immutable originalMessages snapshot
		// This prevents duplication when multiple retries occur
		messages := make([]interface{}, len(rc.originalMessages))
		copy(messages, rc.originalMessages)

		if rc.accumulatedResponse.Len() > 0 {
			messages = append(messages, map[string]string{
				"role":    "assistant",
				"content": rc.accumulatedResponse.String(),
			})
		}

		messages = append(messages, map[string]string{
			"role":    "user",
			"content": "The previous response was interrupted. Continue exactly where you stopped.",
		})

		rc.requestBody["messages"] = messages
	}
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

	copyHeaders(proxyReq, rc.originalHeaders)

	resp, err := h.client.Do(proxyReq)
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
func (h *Handler) handleNonOKStatus(w http.ResponseWriter, rc *requestContext, resp *http.Response, modelIndex int, counters *retryCounters) attemptResult {
	if !rc.headersSent {
		if resp.StatusCode == http.StatusBadRequest {
			bodyBytes, _ := io.ReadAll(resp.Body)
			log.Printf("Upstream returned 400 (Bad Request). Error body: %s", string(bodyBytes))
			resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		}

		// Retry on 5xx or 429
		if resp.StatusCode >= 500 || resp.StatusCode == 429 {
			resp.Body.Close()
			log.Printf("Upstream returned %d", resp.StatusCode)
			h.publishEvent("upstream_error_status", map[string]interface{}{"status": resp.StatusCode, "id": rc.reqID})
			counters.errorRetries++
			time.Sleep(1 * time.Second)
			return attemptContinueRetry
		}

		// If there's a fallback model available, break to try it
		if modelIndex+1 < len(rc.modelList) {
			resp.Body.Close()
			log.Printf("Upstream returned %d for model %s. Triggering fallback.", resp.StatusCode, rc.requestBody["model"])
			return attemptBreakToFallback
		}

		// Pass through error to client
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		resp.Body.Close()

		rc.reqLog.Status = "failed"
		rc.reqLog.Error = fmt.Sprintf("Upstream returned %d", resp.StatusCode)
		rc.reqLog.EndTime = time.Now()
		rc.reqLog.Duration = time.Since(rc.startTime).String()
		h.store.Add(rc.reqLog)
		return attemptReturnImmediately
	}

	// Headers already sent (stream retry scenario)
	// The original stream started successfully but failed mid-way, triggering a retry.
	// If the retry attempt also fails, prioritize fallback over retrying the same model.
	resp.Body.Close()

	// If there's a fallback model available, try it immediately
	// This is preferred over retrying the same model when headers are already sent
	if modelIndex+1 < len(rc.modelList) {
		log.Printf("Upstream returned %d for model %s during stream retry. Triggering fallback.", resp.StatusCode, rc.requestBody["model"])
		return attemptBreakToFallback
	}

	// No fallback available - retry on 5xx or 429 within same model
	if resp.StatusCode >= 500 || resp.StatusCode == 429 {
		log.Printf("Upstream returned %d during stream retry (headers already sent, no fallback)", resp.StatusCode)
		h.publishEvent("upstream_error_status_retry", map[string]interface{}{"status": resp.StatusCode, "id": rc.reqID})
		counters.errorRetries++
		time.Sleep(1 * time.Second)
		return attemptContinueRetry
	}

	// No fallback available, give up
	log.Printf("No fallback available after stream error with status %d", resp.StatusCode)
	return attemptReturnImmediately
}

// handleReadError categorizes a read error (idle timeout, deadline exceeded,
// or generic stream error) and increments the appropriate retry counter.
func (h *Handler) handleReadError(rc *requestContext, monitor *supervisor.MonitoredReader, err error, attemptCtx context.Context, attempt int, counters *retryCounters) attemptResult {
	counters.lastErr = err

	if errors.Is(err, supervisor.ErrIdleTimeout) {
		log.Println("Stream idle timeout detected!")
		h.publishEvent("timeout_idle", map[string]interface{}{"timeout": rc.conf.IdleTimeout.String(), "id": rc.reqID})
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

// ─────────────────────────────────────────────────────────────────────────────
// Response processing (OK status)
// ─────────────────────────────────────────────────────────────────────────────

// handleOKResponse handles a 200 OK upstream response, dispatching to either
// non-streaming or streaming processing.
func (h *Handler) handleOKResponse(w http.ResponseWriter, rc *requestContext, resp *http.Response, attemptCtx context.Context, attempt int, counters *retryCounters) attemptResult {
	monitor := supervisor.NewMonitoredReader(resp.Body, rc.conf.IdleTimeout)

	if !rc.headersSent {
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(http.StatusOK)
		rc.headersSent = true
	}

	if !rc.isStream {
		return h.handleNonStreamResponse(w, rc, monitor, attemptCtx, attempt, counters)
	}
	return h.handleStreamResponse(w, rc, monitor, attemptCtx, attempt, counters)
}

// handleNonStreamResponse reads the entire response body and writes it to the client.
func (h *Handler) handleNonStreamResponse(w http.ResponseWriter, rc *requestContext, monitor *supervisor.MonitoredReader, attemptCtx context.Context, attempt int, counters *retryCounters) attemptResult {
	bodyBytes, err := io.ReadAll(monitor)
	if err != nil {
		return h.handleReadError(rc, monitor, err, attemptCtx, attempt, counters)
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

// handleStreamResponse processes a Server-Sent Events stream.
func (h *Handler) handleStreamResponse(w http.ResponseWriter, rc *requestContext, monitor *supervisor.MonitoredReader, attemptCtx context.Context, attempt int, counters *retryCounters) attemptResult {
	scanner := bufio.NewScanner(monitor)
	buffer := make([]byte, 0, 1024*1024)
	scanner.Buffer(buffer, 1024*1024)

	streamEndedSuccessfully := false

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
		line := scanner.Bytes()

		// Check if this line is an error response dumped into the stream
		// (happens when upstream crashes mid-stream and dumps raw error JSON)
		if errorMsg := isStreamErrorChunk(line); errorMsg != "" {
			log.Printf("Stream error chunk detected: %s", errorMsg)
			h.publishEvent("stream_error_chunk", map[string]interface{}{"error": errorMsg, "id": rc.reqID})
			monitor.Close()
			counters.errorRetries++
			return attemptContinueRetry
		}

		w.Write(line)
		w.Write([]byte("\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		if bytes.HasPrefix(line, []byte("data: ")) {
			data := bytes.TrimPrefix(line, []byte("data: "))
			if string(data) == "[DONE]" {
				streamEndedSuccessfully = true
				w.Write([]byte("\n"))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}

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
		}
	}

	// Handle scanner error
	if err := scanner.Err(); err != nil {
		return h.handleReadError(rc, monitor, err, attemptCtx, attempt, counters)
	}

	// Stream completed successfully
	if streamEndedSuccessfully {
		h.publishEvent("request_completed", map[string]interface{}{"id": rc.reqID})
		h.finalizeSuccess(rc)
		monitor.Close()
		return attemptSuccess
	}

	// Stream ended without [DONE] and without error — unexpected EOF
	log.Println("Stream ended unexpectedly without [DONE]")
	h.publishEvent("stream_ended_unexpectedly", map[string]interface{}{"id": rc.reqID})
	monitor.Close()
	counters.errorRetries++
	return attemptContinueRetry
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
