package proxy

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// spawnTrigger indicates why parallel requests are being spawned
type spawnTrigger string

const (
	triggerIdleTimeout spawnTrigger = "idle_timeout"
	triggerMainError   spawnTrigger = "main_error"
)

// raceCoordinator manages multiple parallel upstream requests
type raceCoordinator struct {
	mu sync.RWMutex

	baseCtx context.Context
	cfg     *ConfigSnapshot
	req     *http.Request
	rawBody []byte

	requests   []*upstreamRequest
	models     []string
	winner     *upstreamRequest
	winnerIdx  int
	failedCount int

	done     chan struct{} // Closed when a winner is found and finished, or all failed
	streamCh chan struct{} // Signals when streaming can start
	
	onceStream sync.Once
	onceDone   sync.Once

	// Metrics for logging/monitoring
	startTime       time.Time
	spawnTriggers   []spawnTrigger // Track why requests were spawned
}

func newRaceCoordinator(ctx context.Context, cfg *ConfigSnapshot, req *http.Request, rawBody []byte, models []string) *raceCoordinator {
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
		spawnTriggers: make([]spawnTrigger, 0),
	}
}

// Start initiates the race
func (c *raceCoordinator) Start() {
	log.Printf("[RACE] Starting race coordinator with %d models: %v", len(c.models), c.models)
	
	// 1. Spawn main request (no trigger - it's the initial request)
	c.spawn(modelTypeMain, "")

	// 2. Start manager loop
	go c.manage()
}

func (c *raceCoordinator) spawn(mType upstreamModelType, trigger spawnTrigger) {
	c.mu.Lock()
	defer c.mu.Unlock()

	idx := len(c.requests)
	if idx >= len(c.models) {
		log.Printf("[RACE] Cannot spawn more requests: reached max models (%d)", len(c.models))
		return
	}

	modelID := c.models[idx]
	req := newUpstreamRequest(idx, mType, modelID, c.cfg.RaceMaxBufferBytes)
	c.requests = append(c.requests, req)
	c.spawnTriggers = append(c.spawnTriggers, trigger)

	log.Printf("[RACE] Spawning %s request (id=%d, model=%s, trigger=%s)", mType, idx, modelID, trigger)

	// Execute in background
	go c.execute(req)
}

func (c *raceCoordinator) manage() {
	// HEARTBEAT / MONITORING LOOP
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	idleTimerStarted := false
	var idleDeadline time.Time

	for {
		select {
		case <-c.baseCtx.Done():
			c.cancelAll()
			c.onceDone.Do(func() { close(c.done) })
			c.onceStream.Do(func() { close(c.streamCh) })
			return
		case <-ticker.C:
			c.mu.Lock()
			
			// Check for winner eligibility
			if c.winner == nil {
				for i, req := range c.requests {
					if req.IsStreaming() || req.IsDone() && req.GetError() == nil {
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
				var trigger spawnTrigger
				nextType := modelTypeSecond
				if len(c.requests) >= 2 {
					nextType = modelTypeFallback
				}

				if running < c.cfg.RaceMaxParallel {
					// Case 1: Latest request failed
					latestReq := c.requests[len(c.requests)-1]
					if latestReq.IsDone() && latestReq.GetError() != nil {
						log.Printf("[RACE] Latest request %d failed, spawning next attempt", latestReq.id)
						shouldSpawn = true
						trigger = triggerMainError
					}

					// Case 2: Main request is idle (Parallel race retry)
					if !shouldSpawn && c.cfg.RaceParallelOnIdle && len(c.requests) == 1 {
						mainReq := c.requests[0]
						if mainReq.GetStatus() == statusRunning {
							if !idleTimerStarted {
								idleDeadline = time.Now().Add(time.Duration(c.cfg.IdleTimeout))
								idleTimerStarted = true
							} else if time.Now().After(idleDeadline) {
								log.Printf("[RACE] Main request idle, spawning parallel request")
								shouldSpawn = true
								trigger = triggerIdleTimeout
								idleTimerStarted = false
							}
						}
					}
				}

				if shouldSpawn {
					c.mu.Unlock()
					c.spawn(nextType, trigger)
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

func (c *raceCoordinator) cancelAll() {
	c.cancelAllExcept(nil)
}

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

		// Parse status text like "upstream returned error: 429 Too Many Requests"
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
	TotalRequests   int            `json:"total_requests"`
	WinnerType      string         `json:"winner_type"`
	WinnerModel     string         `json:"winner_model"`
	WinnerIndex     int            `json:"winner_index"`
	Duration        time.Duration  `json:"duration"`
	SpawnTriggers   []string       `json:"spawn_triggers"`
	FailedCount     int            `json:"failed_count"`
	WinnerBufferLen int64          `json:"winner_buffer_bytes"`
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
		stats.SpawnTriggers = append(stats.SpawnTriggers, string(t))
	}

	// Winner info
	if c.winner != nil {
		stats.WinnerType = string(c.winner.modelType)
		stats.WinnerModel = c.winner.modelID
		stats.WinnerBufferLen = c.winner.buffer.TotalLen()
	}

	return stats
}
