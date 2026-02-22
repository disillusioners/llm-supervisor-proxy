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
		conf:            conf,
		targetURL:       targetURL,
		reqID:           reqID,
		startTime:       startTime,
		reqLog:          reqLog,
		modelList:       modelList,
		requestBody:     requestBody,
		isStream:        isStream,
		originalHeaders: r.Header,
		method:          r.Method,
		baseCtx:         r.Context(),
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
			h.prepareRetry(rc, attempt, counters)
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
func (h *Handler) prepareRetry(rc *requestContext, attempt int, counters *retryCounters) {
	log.Printf("Retrying request (attempt %d)...", attempt)
	rc.reqLog.Retries = attempt
	rc.reqLog.Status = "retrying"
	h.store.Add(rc.reqLog)

	h.publishEvent("retry_attempt", map[string]interface{}{"attempt": attempt, "id": rc.reqID})

	if rc.isStream {
		messages, ok := rc.requestBody["messages"].([]interface{})
		if !ok {
			log.Println("Could not find messages, aborting retry")
			return
		}

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
	newBodyBytes, _ := json.Marshal(rc.requestBody)

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

	// Headers already sent, can't send a different status
	resp.Body.Close()
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
		rc.loopDetector = loopdetection.NewDetector(loopdetection.DefaultConfig())
	}
	detector := rc.loopDetector
	streamBuf := detector.NewStreamBuffer()

	for scanner.Scan() {
		line := scanner.Bytes()

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
							h.publishLoopEvent(rc.reqID, result)
						}
					}
				}

				break
			}

			// Track chunk content for both existing accumulation and loop detection
			prevLen := rc.accumulatedResponse.Len()
			extractStreamChunkContent(data, &rc.accumulatedResponse, &rc.accumulatedThinking)
			newContent := rc.accumulatedResponse.String()[prevLen:]

			// Feed content to loop detection buffer
			if detector.IsEnabled() {
				// Add text content
				if len(newContent) > 0 {
					streamBuf.AddText(newContent)
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
						h.publishLoopEvent(rc.reqID, result)
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

// publishLoopEvent emits a loop detection event to the event bus.
func (h *Handler) publishLoopEvent(reqID string, result *loopdetection.DetectionResult) {
	h.publishEvent("loop_detected", map[string]interface{}{
		"id":           reqID,
		"strategy":     result.Strategy,
		"severity":     result.Severity.String(),
		"evidence":     result.Evidence,
		"confidence":   result.Confidence,
		"pattern":      result.Pattern,
		"repeat_count": result.RepeatCount,
	})
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

		h.publishEvent("fallback_triggered", events.FallbackEvent{
			FromModel: currentModel,
			ToModel:   nextModel,
			Reason:    "upstream_error", // simplified; the specific events were already published
		})
	}

	// Only track in "FallbackUsed" if the model that *just failed* was actually a fallback
	if modelIndex > 0 {
		rc.reqLog.FallbackUsed = append(rc.reqLog.FallbackUsed, currentModel)
	}
}
