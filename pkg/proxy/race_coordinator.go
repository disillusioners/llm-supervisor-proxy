package proxy

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// spawnTrigger indicates why parallel requests are being spawned
type spawnTrigger string

const (
	triggerIdleTimeout spawnTrigger = "idle_timeout"
	triggerMainError   spawnTrigger = "main_error"
)

// spawnTriggerInfo contains detailed information about why a spawn was triggered
type spawnTriggerInfo struct {
	trigger       spawnTrigger
	errorMessage  string // Only populated when trigger is main_error
	failedRequest int    // Index of the failed request, -1 if not applicable
}

// raceCoordinator manages multiple parallel upstream requests
type raceCoordinator struct {
	mu sync.RWMutex

	baseCtx context.Context
	cfg     *ConfigSnapshot
	req     *http.Request
	rawBody []byte

	requests    []*upstreamRequest
	models      []string
	winner      *upstreamRequest
	winnerIdx   int
	failedCount int

	done     chan struct{} // Closed when a winner is found and finished, or all failed
	streamCh chan struct{} // Signals when streaming can start

	onceStream sync.Once
	onceDone   sync.Once

	// Metrics for logging/monitoring
	startTime     time.Time
	spawnTriggers []spawnTriggerInfo // Track detailed info about why requests were spawned

	// Event publishing
	eventBus  *events.Bus
	requestID string

	// Stream deadline error info (set when stream deadline fires with no content)
	streamDeadlineError *FinalErrorInfo
}

func newRaceCoordinator(ctx context.Context, cfg *ConfigSnapshot, req *http.Request, rawBody []byte, models []string) *raceCoordinator {
	return newRaceCoordinatorWithEvents(ctx, cfg, req, rawBody, models, nil, "")
}

func newRaceCoordinatorWithEvents(ctx context.Context, cfg *ConfigSnapshot, req *http.Request, rawBody []byte, models []string, eventBus *events.Bus, requestID string) *raceCoordinator {
	if len(models) == 0 {
		models = []string{cfg.ModelID}
	}
	return &raceCoordinator{
		baseCtx:       ctx,
		cfg:           cfg,
		req:           req,
		rawBody:       rawBody,
		models:        models,
		requests:      make([]*upstreamRequest, 0, len(models)),
		winnerIdx:     -1,
		done:          make(chan struct{}),
		streamCh:      make(chan struct{}),
		startTime:     time.Now(),
		spawnTriggers: make([]spawnTriggerInfo, 0),
		eventBus:      eventBus,
		requestID:     requestID,
	}
}

// publishEvent publishes an event to the event bus if available
func (c *raceCoordinator) publishEvent(eventType string, data map[string]interface{}) {
	if c.eventBus == nil {
		return
	}
	// Always include request ID for correlation
	if c.requestID != "" {
		data["id"] = c.requestID
	}
	c.eventBus.Publish(events.Event{
		Type:      eventType,
		Timestamp: time.Now().Unix(),
		Data:      data,
	})
}

// Start initiates the race
func (c *raceCoordinator) Start() {
	log.Printf("[RACE] Starting race coordinator with %d models: %v", len(c.models), c.models)

	log.Printf("[PEAK-DBG] race_coordinator.Start: models=%v, len=%d", c.models, len(c.models))

	// Publish race_started event
	c.publishEvent("race_started", map[string]interface{}{
		"models": c.models,
	})

	// 1. Spawn main request (no trigger - it's the initial request)
	c.spawn(modelTypeMain, spawnTriggerInfo{
		trigger:       "",
		errorMessage:  "",
		failedRequest: -1,
	})

	// 2. Start manager loop
	go c.manage()
}

func (c *raceCoordinator) spawn(mType upstreamModelType, triggerInfo spawnTriggerInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()

	idx := len(c.requests)

	// Assign model based on request type
	var modelID string
	switch mType {
	case modelTypeMain:
		if idx >= len(c.models) {
			log.Printf("[RACE] Cannot spawn main: only %d model(s) available", len(c.models))
			return
		}
		modelID = c.models[0]
	case modelTypeSecond:
		if idx >= len(c.models) {
			log.Printf("[RACE] Cannot spawn second: only %d model(s) available", len(c.models))
			return
		}
		modelID = c.models[0]
	case modelTypeFallback:
		if len(c.models) < 2 {
			log.Printf("[RACE] Cannot spawn fallback: only %d model(s) available", len(c.models))
			return
		}
		modelID = c.models[1]
	}

	req := newUpstreamRequest(idx, mType, modelID, c.cfg.RaceMaxBufferBytes)

	// Set flag to use secondary upstream model for second requests
	if mType == modelTypeSecond {
		req.SetUseSecondaryUpstream(true)
	}

	c.requests = append(c.requests, req)
	c.spawnTriggers = append(c.spawnTriggers, triggerInfo)

	log.Printf("[RACE] Spawning %s request (id=%d, model=%s, trigger=%s)", mType, idx, modelID, triggerInfo.trigger)
	log.Printf("[PEAK-DBG] race_coordinator.spawn: mType=%v, idx=%d, modelID=%q", mType, idx, modelID)

	// Build event data with detailed error info if available
	eventData := map[string]interface{}{
		"request_index": idx,
		"model":         modelID,
		"type":          string(mType),
		"trigger":       string(triggerInfo.trigger),
	}

	// Add detailed error information if this spawn was triggered by an error
	if triggerInfo.trigger == triggerMainError {
		eventData["trigger_reason"] = triggerInfo.errorMessage
		eventData["failed_request_index"] = triggerInfo.failedRequest
	}

	// Publish race_spawn event
	c.publishEvent("race_spawn", eventData)

	// Execute in background
	go c.execute(req)
}

func (c *raceCoordinator) manage() {
	// HEARTBEAT / MONITORING LOOP
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	// STREAMING DEADLINE TIMER
	// When StreamDeadline is reached, pick the best buffer and continue streaming.
	// This allows us to start streaming to the client even if the upstream hasn't finished.
	streamDeadlineTimer := time.NewTimer(c.cfg.StreamDeadline)
	defer streamDeadlineTimer.Stop()

	// HARD DEADLINE TIMER
	// When MaxGenerationTime is reached, forcefully terminate all requests.
	// This is the absolute hard timeout for the entire request lifecycle.
	hardDeadlineTimer := time.NewTimer(c.cfg.MaxGenerationTime)
	defer hardDeadlineTimer.Stop()

	// Track when we started monitoring for idle
	idleCheckStart := time.Now()

	for {
		select {
		case <-c.baseCtx.Done():
			c.cancelAll()
			c.onceDone.Do(func() { close(c.done) })
			c.onceStream.Do(func() { close(c.streamCh) })
			return
		case <-streamDeadlineTimer.C:
			// Streaming deadline reached - pick best buffer and continue streaming
			c.handleStreamingDeadline()
			return
		case <-hardDeadlineTimer.C:
			// Hard deadline reached - force end everything
			log.Printf("[RACE] Hard deadline reached after %v, forcing end", time.Since(c.startTime))
			c.handleHardDeadline()
			return
		case <-ticker.C:
			c.mu.Lock()

			// Check for winner eligibility
			// IMPORTANT: Winner is only selected when request is COMPLETED (received [DONE] signal)
			// BUG FIX: Previously selected winner when IsStreaming() was true, which caused
			// premature winner selection when request finishes within idle timeout.
			// The correct behavior: buffer > wait till DONE > select winner
			if c.winner == nil {
				for i, req := range c.requests {
					// Only select winner when request is fully completed with [DONE] signal
					if req.IsCompleted() && req.GetError() == nil {
						// We found a potential winner!
						// Preference: earlier requests (lower index)
						if c.winner == nil || i < c.winnerIdx {
							c.winner = req
							c.winnerIdx = i

							// Enhanced logging with timing and buffer stats
							elapsed := time.Since(c.startTime)
							bufferLen := req.buffer.TotalLen()
							log.Printf("[RACE] Winner selected: request %d (%s, %s) after %v, buffer=%d bytes",
								i, req.modelType, req.modelID, elapsed.Round(time.Millisecond), bufferLen)

							// Publish race_winner_selected event
							c.publishEvent("race_winner_selected", map[string]interface{}{
								"winner_index": c.winnerIdx,
								"winner_type":  string(c.winner.modelType),
								"winner_model": c.winner.modelID,
								"duration_ms":  elapsed.Milliseconds(),
								"buffer_bytes": bufferLen,
							})

							c.onceStream.Do(func() { close(c.streamCh) })
						}
					}
				}
			}

			// If we have a winner, stop all other attempts and exit management loop.
			// The winning attempt will continue to stream into its buffer,
			// and the handler will read from that buffer.
			if c.winner != nil {
				c.mu.Unlock()
				c.cancelAllExcept(c.winner)
				// We don't close c.done here yet, but we can exit the manager loop.
				// c.done will be closed when the context is cancelled.
				return
			}
			// Spawning logic (on failure or idle)
			if c.winner == nil && len(c.requests) < len(c.models) {
				running := 0
				for _, r := range c.requests {
					if !r.IsDone() {
						running++
					}
				}

				shouldSpawn := false
				var triggerInfo spawnTriggerInfo

				if running < c.cfg.RaceMaxParallel {
					// Case 1: Latest request failed - spawn fallback directly (skip retry with same model)
					latestReq := c.requests[len(c.requests)-1]
					if latestReq.IsDone() && latestReq.GetError() != nil {

						errMsg := latestReq.GetError().Error()
						log.Printf("[RACE] Latest request %d failed: %s, spawning fallback directly", latestReq.id, errMsg)
						shouldSpawn = true
						triggerInfo = spawnTriggerInfo{
							trigger:       triggerMainError,
							errorMessage:  errMsg,
							failedRequest: latestReq.id,
						}
					}

					// Case 2: Main request is idle (Parallel race retry)
					// FIXED: Now uses IsIdle() which tracks last activity time during streaming
					// This correctly detects idle even after the request has started streaming
					if !shouldSpawn && c.cfg.RaceParallelOnIdle && len(c.requests) == 1 {
						mainReq := c.requests[0]
						// Check for idle in two ways:
						// 1. statusRunning: hasn't received first byte yet (use start time)
						// 2. statusStreaming: has received data but is now idle (use last activity time)
						if mainReq.GetStatus() == statusRunning {
							// Haven't received first byte yet - check from start time
							if time.Since(idleCheckStart) > time.Duration(c.cfg.IdleTimeout) {
								log.Printf("[RACE] Main request idle (no first byte), spawning parallel request")
								shouldSpawn = true
								triggerInfo = spawnTriggerInfo{
									trigger:       triggerIdleTimeout,
									errorMessage:  "",
									failedRequest: -1,
								}
							}
						} else if mainReq.IsIdle(time.Duration(c.cfg.IdleTimeout)) {
							// Has received data but is now idle
							log.Printf("[RACE] Main request idle (no data for %v), spawning parallel request", time.Duration(c.cfg.IdleTimeout))
							shouldSpawn = true
							triggerInfo = spawnTriggerInfo{
								trigger:       triggerIdleTimeout,
								errorMessage:  "",
								failedRequest: -1,
							}
						}
					}
				}

				if shouldSpawn {

					c.mu.Unlock()

					if triggerInfo.trigger == triggerIdleTimeout {
						// On idle timeout: spawn both second AND fallback together
						c.spawn(modelTypeSecond, triggerInfo)
						if len(c.models) > 1 {
							c.spawn(modelTypeFallback, triggerInfo)
						}
					} else {
						// On error: spawn fallback directly (skip retry with same model)
						if len(c.models) > 1 {
							c.spawn(modelTypeFallback, triggerInfo)
						} else {
							log.Printf("[RACE] Main failed but no fallback available")
						}
					}

					c.mu.Lock()
				}
			}

			// If no winner and reached max parallel attempts, check if all failed
			if c.winner == nil && len(c.requests) >= len(c.models) {
				allFailed := true
				for _, r := range c.requests {
					if !r.IsDone() || r.GetError() == nil {
						allFailed = false
						break
					}
				}
				if allFailed {
					log.Printf("[RACE] All requests failed")

					// Collect all error details
					errors := make([]map[string]interface{}, 0, len(c.requests))
					for _, r := range c.requests {
						if r.GetError() != nil {
							errors = append(errors, map[string]interface{}{
								"request_index": r.id,
								"model":         r.modelID,
								"type":          string(r.modelType),
								"error":         r.GetError().Error(),
							})
						}
					}

					// Publish race_all_failed event with detailed error info
					c.publishEvent("race_all_failed", map[string]interface{}{
						"total_attempts": len(c.requests),
						"duration_ms":    time.Since(c.startTime).Milliseconds(),
						"errors":         errors,
					})

					c.mu.Unlock()
					c.onceDone.Do(func() { close(c.done) })
					c.onceStream.Do(func() { close(c.streamCh) })
					return
				}
			}

			c.mu.Unlock()
		}
	}
}

// handleStreamingDeadline picks the best buffer when StreamDeadline is reached
// Per the unified race retry design:
// - Pick the request with the most content (best candidate to continue)
// - DON'T cancel the winner - let it continue streaming until complete or hard deadline
// - Cancel only the other requests
func (c *raceCoordinator) handleStreamingDeadline() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.winner != nil {
		return // Already have a winner
	}

	log.Printf("[RACE] Streaming deadline reached after %v, picking best buffer", time.Since(c.startTime))

	// Find request with most content (best candidate to continue)
	// If all buffers are equal, prefer the first (main) request
	var best *upstreamRequest
	var bestLen int64 = -1 // Start at -1 so first request always gets selected

	for _, req := range c.requests {
		if req != nil && !req.IsDone() {
			bufferLen := req.buffer.TotalLen()
			if bufferLen > bestLen {
				best = req
				bestLen = bufferLen
			}
		}
	}

	// If we have a winner (even with 0 bytes), stream it
	// Only error if no requests exist at all
	if best != nil {
		c.winner = best
		c.winnerIdx = best.id

		log.Printf("[RACE] Picked best buffer: request %d (%s, %s) with %d bytes",
			best.id, best.modelType, best.modelID, bestLen)

		// Publish race_deadline_pick event
		c.publishEvent("race_deadline_pick", map[string]interface{}{
			"winner_index": c.winnerIdx,
			"winner_type":  string(best.modelType),
			"winner_model": best.modelID,
			"buffer_bytes": bestLen,
			"duration_ms":  time.Since(c.startTime).Milliseconds(),
		})

		// Signal that streaming can start
		c.onceStream.Do(func() { close(c.streamCh) })

		// DON'T cancel the winner - let it continue streaming until complete or hardDeadline
		// This is per the unified race retry design: "Continue streaming winner until complete or hard deadline"
		// The winner was picked because it has the most content, so we want to keep receiving more data.

		// Cancel only the other requests
		for _, req := range c.requests {
			if req != nil && req != best {
				req.Cancel()
			}
		}
	} else {
		// No content at all - all failed or no requests started
		log.Printf("[RACE] Streaming deadline reached, no content available")

		// Build error info for the handler to use
		errInfo := c.getFinalErrorInfoLocked()
		errInfo.Message = "Request timeout - no response received"
		c.streamDeadlineError = &errInfo

		c.publishEvent("race_all_failed", map[string]interface{}{
			"total_attempts": len(c.requests),
			"duration_ms":    time.Since(c.startTime).Milliseconds(),
			"reason":         "streaming_deadline_no_content",
		})

		c.onceDone.Do(func() { close(c.done) })
		c.onceStream.Do(func() { close(c.streamCh) })
	}
}

// handleHardDeadline forcefully terminates all requests when MaxGenerationTime is reached
// This is the absolute hard timeout - no requests are allowed to continue past this point.
func (c *raceCoordinator) handleHardDeadline() {
	c.mu.Lock()
	defer c.mu.Unlock()

	log.Printf("[RACE] Hard deadline reached, cancelling all requests")

	// Publish race_hard_deadline event
	c.publishEvent("race_hard_deadline", map[string]interface{}{
		"duration_ms": time.Since(c.startTime).Milliseconds(),
	})

	// Cancel ALL requests immediately (including winner if any)
	for _, req := range c.requests {
		if req != nil {
			req.Cancel()
		}
	}

	// Signal done
	c.onceDone.Do(func() { close(c.done) })
	c.onceStream.Do(func() { close(c.streamCh) })
}

func (c *raceCoordinator) cancelAll() {
	c.cancelAllExcept(nil)
}

// cancelAllExcept cancels all requests except the winner.
// Each call to req.Cancel() performs full cleanup:
//   - Cancels the request context
//   - Drains and closes the HTTP response body
//   - Releases the stream buffer
func (c *raceCoordinator) cancelAllExcept(winner *upstreamRequest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, req := range c.requests {
		if req != winner {
			req.Cancel()
		}
	}
}

// execute is a wrapper for executeRequest
func (c *raceCoordinator) execute(req *upstreamRequest) {
	// Create context for this specific attempt
	ctx, cancel := context.WithCancel(c.baseCtx)
	req.SetContext(ctx, cancel)

	err := executeRequest(ctx, c.cfg, c.req, c.rawBody, req)

	if err != nil {
		req.MarkFailed(err)
		log.Printf("[RACE] Request %d failed: %v", req.id, err)

		// Publish race_request_failed event with detailed error info
		c.publishEvent("race_request_failed", map[string]interface{}{
			"request_index": req.id,
			"model":         req.modelID,
			"type":          string(req.modelType),
			"error":         err.Error(),
		})
	} else {
		req.MarkCompleted()
		log.Printf("[RACE] Request %d completed successfully", req.id)
	}
}

// GetWinner returns the winner request
func (c *raceCoordinator) GetWinner() *upstreamRequest {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.winner
}

// WaitForWinner blocks until a winner is found or all requests fail
func (c *raceCoordinator) WaitForWinner() *upstreamRequest {
	select {
	case <-c.streamCh:
		return c.GetWinner()
	case <-c.done:
		return c.GetWinner()
	case <-c.baseCtx.Done():
		return nil
	}
}

// GetCommonFailureStatus returns the HTTP status code if all failed requests share the same status, else 0
func (c *raceCoordinator) GetCommonFailureStatus() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.requests) == 0 {
		return 0
	}

	var commonStatus int
	for _, req := range c.requests {
		err := req.GetError()
		if err == nil {
			return 0 // Not failed yet or didn't fail
		}

		// First check if we have the HTTP status stored
		httpStatus := req.GetHTTPStatus()
		if httpStatus >= 400 {
			// Use the stored HTTP status directly
			if commonStatus == 0 {
				commonStatus = httpStatus
			} else if commonStatus != httpStatus {
				return 0 // Mismatch
			}
			continue
		}

		// Fallback: Parse status text like "upstream returned error: 429 Too Many Requests"
		errStr := err.Error()
		var status int
		if strings.Contains(errStr, "upstream returned error: ") {
			fmt.Sscanf(errStr, "upstream returned error: %d", &status)
		} else if strings.Contains(errStr, "idle timeout") || strings.Contains(errStr, "context") || strings.Contains(errStr, "timeout") {
			status = http.StatusGatewayTimeout
		} else if strings.Contains(errStr, "buffer limit") {
			status = http.StatusRequestEntityTooLarge
		} else {
			status = http.StatusBadGateway
		}

		if status == 0 {
			status = http.StatusBadGateway
		}

		if commonStatus == 0 {
			commonStatus = status
		} else if commonStatus != status {
			return 0 // Mismatch
		}
	}

	return commonStatus
}

// RaceStats contains statistics about a completed race
type RaceStats struct {
	TotalRequests   int           `json:"total_requests"`
	WinnerType      string        `json:"winner_type"`
	WinnerModel     string        `json:"winner_model"`
	WinnerIndex     int           `json:"winner_index"`
	Duration        time.Duration `json:"duration"`
	SpawnTriggers   []string      `json:"spawn_triggers"`
	FailedCount     int           `json:"failed_count"`
	WinnerBufferLen int64         `json:"winner_buffer_bytes"`
}

// GetStats returns statistics about the race for logging/metrics
func (c *raceCoordinator) GetStats() RaceStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stats := RaceStats{
		TotalRequests: len(c.requests),
		WinnerIndex:   c.winnerIdx,
		Duration:      time.Since(c.startTime),
		FailedCount:   c.failedCount,
	}

	// Convert spawn triggers to strings
	for _, t := range c.spawnTriggers {
		stats.SpawnTriggers = append(stats.SpawnTriggers, string(t.trigger))
	}

	// Winner info
	if c.winner != nil {
		stats.WinnerType = string(c.winner.modelType)
		stats.WinnerModel = c.winner.modelID
		stats.WinnerBufferLen = c.winner.buffer.TotalLen()
	}

	return stats
}

// GetRequestStatuses returns the status of each upstream request type (main, second, fallback)
// Status values: "success" (completed), "failed" (error), "not_started" (never spawned or cancelled)
func (c *raceCoordinator) GetRequestStatuses() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	statuses := make(map[string]string)
	// Initialize all as not_started
	statuses["main"] = "not_started"
	statuses["second"] = "not_started"
	statuses["fallback"] = "not_started"

	for _, req := range c.requests {
		var status string
		switch req.GetStatus() {
		case statusCompleted:
			status = "success"
		case statusFailed:
			status = "failed"
		default:
			// pending, running, streaming - treat as not_started (cancelled or in-progress)
			status = "not_started"
		}

		switch req.modelType {
		case modelTypeMain:
			statuses["main"] = status
		case modelTypeSecond:
			statuses["second"] = status
		case modelTypeFallback:
			statuses["fallback"] = status
		}
	}

	return statuses
}

// GetStreamDeadlineError returns the error info if stream deadline fired with no content
func (c *raceCoordinator) GetStreamDeadlineError() *FinalErrorInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.streamDeadlineError
}

// FinalErrorInfo contains information for building the final error response
type FinalErrorInfo struct {
	HTTPStatus int    // HTTP status code to return
	ErrorType  string // Error type (e.g., "rate_limit", "upstream_error")
	ErrorCode  string // Error code for retry detection (e.g., "rate_limit", "unavailable")
	Message    string // Human-readable error message
}

// GetFinalErrorInfo builds the final error info based on all failed requests.
// This implements the OpenCode-compatible error format with proper rate_limit detection.
// Key rules:
// - Rate limit code is only added after ALL models are exhausted with 429
// - Context overflow errors never get rate_limit code (triggers compaction instead)
// - HTTP 503 maps to type "too_many_requests" with code "unavailable"
func (c *raceCoordinator) GetFinalErrorInfo() FinalErrorInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.requests) == 0 {
		return FinalErrorInfo{
			HTTPStatus: http.StatusBadGateway,
			ErrorType:  models.ErrorTypeServerError,
			ErrorCode:  "",
			Message:    "No upstream requests were made",
		}
	}

	// Collect all failed requests and their errors
	var failedRequests []*upstreamRequest
	var lastError error
	anyModel429 := false
	allModelsExhausted := true
	hasContextOverflow := false

	for _, req := range c.requests {
		err := req.GetError()
		if err != nil {
			failedRequests = append(failedRequests, req)
			lastError = err

			// Check for 429 rate limit
			if req.GetHTTPStatus() == http.StatusTooManyRequests {
				anyModel429 = true
			}

			// Check for context overflow
			if models.IsContextOverflowError(err) {
				hasContextOverflow = true
			}
		} else {
			// A request succeeded (shouldn't happen if we're building error, but check)
			allModelsExhausted = false
		}
	}

	// If no errors, this shouldn't be called
	if lastError == nil {
		return FinalErrorInfo{
			HTTPStatus: http.StatusBadGateway,
			ErrorType:  models.ErrorTypeServerError,
			ErrorCode:  "",
			Message:    "Unknown error",
		}
	}

	// Determine HTTP status from common status
	httpStatus := c.getCommonFailureStatusLocked()

	// Build error message
	message := "All upstream models failed"
	if lastError != nil {
		message = lastError.Error()
	}

	// CRITICAL: Never add rate_limit code to context overflow errors
	// OpenCode checks context overflow BEFORE retry logic to trigger compaction
	if hasContextOverflow {
		return FinalErrorInfo{
			HTTPStatus: http.StatusBadRequest,
			ErrorType:  models.ErrorTypeContextOverflow,
			ErrorCode:  "", // No code - let OpenCode trigger compaction
			Message:    message,
		}
	}

	// Determine error type and code based on HTTP status
	var errType string
	var errCode string

	switch httpStatus {
	case http.StatusTooManyRequests: // 429
		// Only add rate_limit code if ALL models exhausted
		// If we have working models, no need to signal retry
		if allModelsExhausted && anyModel429 {
			errType = models.ErrorTypeRateLimit
			errCode = models.ErrorCodeRateLimit
			message = "All models rate limited"
		} else {
			errType = models.ErrorTypeRateLimit
			// No code if fallback worked (but this shouldn't happen since all failed)
			errCode = ""
		}
	case http.StatusBadGateway: // 502
		errType = models.ErrorTypeUpstreamError
		errCode = models.ErrorCodeUnavailable
	case http.StatusNotFound: // 404
		errType = models.ErrorTypeServerError
		errCode = "model_not_found"
	case http.StatusInternalServerError: // 500
		errType = models.ErrorTypeServerError
		errCode = ""
	case http.StatusServiceUnavailable: // 503
		errType = models.ErrorTypeTooManyRequests
		errCode = models.ErrorCodeUnavailable
	case http.StatusGatewayTimeout: // 504
		errType = models.ErrorTypeUpstreamError
		errCode = ""
	default:
		// httpStatus 0 means mismatched statuses (e.g., 429 + context canceled)
		// Default to BadGateway to ensure valid HTTP status
		httpStatus = http.StatusBadGateway
		errType = models.ErrorTypeServerError
		errCode = ""
	}

	return FinalErrorInfo{
		HTTPStatus: httpStatus,
		ErrorType:  errType,
		ErrorCode:  errCode,
		Message:    message,
	}
}

// getFinalErrorInfoLocked is the internal version of GetFinalErrorInfo that assumes lock is held
func (c *raceCoordinator) getFinalErrorInfoLocked() FinalErrorInfo {
	if len(c.requests) == 0 {
		return FinalErrorInfo{
			HTTPStatus: http.StatusBadGateway,
			ErrorType:  models.ErrorTypeServerError,
			ErrorCode:  "",
			Message:    "No upstream requests were made",
		}
	}

	// Collect all failed requests and their errors
	var failedRequests []*upstreamRequest
	var lastError error
	anyModel429 := false
	allModelsExhausted := true
	hasContextOverflow := false

	for _, req := range c.requests {
		err := req.GetError()
		if err != nil {
			failedRequests = append(failedRequests, req)
			lastError = err

			// Check for 429 rate limit
			if req.GetHTTPStatus() == http.StatusTooManyRequests {
				anyModel429 = true
			}

			// Check for context overflow
			if models.IsContextOverflowError(err) {
				hasContextOverflow = true
			}
		} else {
			allModelsExhausted = false
		}
	}

	if lastError == nil {
		return FinalErrorInfo{
			HTTPStatus: http.StatusBadGateway,
			ErrorType:  models.ErrorTypeServerError,
			ErrorCode:  "",
			Message:    "Unknown error",
		}
	}

	httpStatus := c.getCommonFailureStatusLocked()

	message := "All upstream models failed"
	if lastError != nil {
		message = lastError.Error()
	}

	if hasContextOverflow {
		return FinalErrorInfo{
			HTTPStatus: http.StatusBadRequest,
			ErrorType:  models.ErrorTypeContextOverflow,
			ErrorCode:  "",
			Message:    message,
		}
	}

	var errType string
	var errCode string

	switch httpStatus {
	case http.StatusTooManyRequests:
		if allModelsExhausted && anyModel429 {
			errType = models.ErrorTypeRateLimit
			errCode = models.ErrorCodeRateLimit
			message = "All models rate limited"
		} else {
			errType = models.ErrorTypeRateLimit
			errCode = ""
		}
	case http.StatusBadGateway:
		errType = models.ErrorTypeUpstreamError
		errCode = models.ErrorCodeUnavailable
	case http.StatusNotFound: // 404
		errType = models.ErrorTypeServerError
		errCode = "model_not_found"
	case http.StatusInternalServerError: // 500
		errType = models.ErrorTypeServerError
		errCode = ""
	case http.StatusServiceUnavailable:
		errType = models.ErrorTypeTooManyRequests
		errCode = models.ErrorCodeUnavailable
	case http.StatusGatewayTimeout:
		errType = models.ErrorTypeUpstreamError
		errCode = ""
	default:
		// httpStatus 0 means mismatched statuses (e.g., 429 + context canceled)
		// Default to BadGateway to ensure valid HTTP status
		httpStatus = http.StatusBadGateway
		errType = models.ErrorTypeServerError
		errCode = ""
	}

	return FinalErrorInfo{
		HTTPStatus: httpStatus,
		ErrorType:  errType,
		ErrorCode:  errCode,
		Message:    message,
	}
}

// getCommonFailureStatusLocked is the internal version of GetCommonFailureStatus that assumes lock is held
func (c *raceCoordinator) getCommonFailureStatusLocked() int {
	if len(c.requests) == 0 {
		return 0
	}

	var commonStatus int
	for _, req := range c.requests {
		err := req.GetError()
		if err == nil {
			return 0 // Not failed yet or didn't fail
		}

		// First check if we have the HTTP status stored
		httpStatus := req.GetHTTPStatus()
		if httpStatus >= 400 {
			// Use the stored HTTP status directly
			if commonStatus == 0 {
				commonStatus = httpStatus
			} else if commonStatus != httpStatus {
				return 0 // Mismatch
			}
			continue
		}

		// Fallback: Parse status text
		errStr := err.Error()
		var status int
		if strings.Contains(errStr, "upstream returned error: ") {
			fmt.Sscanf(errStr, "upstream returned error: %d", &status)
		} else if strings.Contains(errStr, "idle timeout") || strings.Contains(errStr, "context") || strings.Contains(errStr, "timeout") {
			status = http.StatusGatewayTimeout
		} else if strings.Contains(errStr, "buffer limit") {
			status = http.StatusRequestEntityTooLarge
		} else {
			status = http.StatusBadGateway
		}

		if status == 0 {
			status = http.StatusBadGateway
		}

		if commonStatus == 0 {
			commonStatus = status
		} else if commonStatus != status {
			return 0 // Mismatch
		}
	}

	return commonStatus
}
