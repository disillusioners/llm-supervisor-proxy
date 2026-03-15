// ─────────────────────────────────────────────────────────────────────────────
// Response processing (OK status)
// ─────────────────────────────────────────────────────────────────────────────

package proxy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/loopdetection"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/supervisor"
)

// handleOKResponse handles a 200 OK upstream response, dispatching to either
// non-streaming or streaming processing.
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
	if rc.isStream && rc.conf.SSEHeartbeatEnabled {
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
		// Cancel any running shadow request to prevent goroutine leak
		cancelShadow(rc)
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
		// CHECK HARD DEADLINE FIRST - ensures server never serves a connection longer than MaxRequestTime
		// This is an absolute timeout that covers all retries and attempts
		if time.Now().After(rc.hardDeadline) {
			log.Printf("Stream exceeded hard deadline (%v), aborting", rc.conf.MaxRequestTime)
			h.publishEvent("stream_hard_deadline_exceeded", map[string]interface{}{
				"id":       rc.reqID,
				"duration": time.Since(rc.startTime).String(),
				"limit":    rc.conf.MaxRequestTime.String(),
			})
			monitor.Close()
			return attemptReturnImmediately
		}

		// CHECK RELEASE DEADLINE
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
					// Note: Cancel() is safe to call multiple times (uses sync.Once)
					rc.shadow.Cancel()

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
		// Note: Cancel() is safe to call multiple times (uses sync.Once)
		if rc.shadow != nil {
			log.Printf("[SHADOW] Main stream succeeded, cancelling shadow request")
			h.publishEvent("shadow_retry_lost", map[string]interface{}{
				"id":        rc.reqID,
				"model":     rc.shadow.model,
				"duration":  time.Since(rc.shadow.startTime).String(),
				"mainModel": currentModel,
			})
			rc.shadow.Cancel()
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
