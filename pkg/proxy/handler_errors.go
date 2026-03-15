// ─────────────────────────────────────────────────────────────────────────────
// Error handling
// ─────────────────────────────────────────────────────────────────────────────

package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/supervisor"
)

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
		h.publishEvent("client_disconnected", map[string]interface{}{"id": rc.reqID})
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
		h.publishEvent("client_disconnected_during_stream", map[string]interface{}{"id": rc.reqID})
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
