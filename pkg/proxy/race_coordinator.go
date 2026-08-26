package proxy

// ─────────────────────────────────────────────────────────────────────────────
// race_coordinator.go — race lifecycle coordination (owner of attempt
// orchestration, coordinator construction, and event fan-out)
// ─────────────────────────────────────────────────────────────────────────────
//
// What this file owns:
//
//   - Coordinator construction ........ newRaceCoordinator,
//                                         newRaceCoordinatorWithEvents
//                                         (raceCoordinator constructors,
//                                         eventBus wiring, snapshot of
//                                         the ConfigSnapshot for the
//                                         race lifetime).
//
//   - Attempt orchestration ........... Start, spawn, manage (the
//                                         select-loop that ticks the
//                                         race forward — spawn-replicas,
//                                         pick-winner, hand-back via
//                                         completion channels),
//                                         handleStreamingDeadline,
//                                         handleHardDeadline,
//                                         cancelAll / cancelAllExcept.
//
//   - Event fan-out .................... publishEvent (the race state
//                                         publishes to the events.Bus
//                                         so observability / metrics
//                                         layers can subscribe without
//                                         touching the race itself).
//
//   - Finalization + introspection ..... execute (the racing proxy's
//                                         single-attempt trampoline
//                                         into race_executor.go),
//                                         GetWinner / WaitForWinner
//                                         (winner hand-back to the
//                                         caller), GetStats,
//                                         GetRequestStatuses,
//                                         GetStreamDeadlineError,
//                                         GetFinalErrorInfo (post-race
//                                         diagnostics), FinalErrorInfo,
//                                         RaceStats types.
//
// Companion file:
//
//   - race_executor.go: per-attempt EXECUTION — the upstream HTTP
//     round-trip for ONE model attempt (header construction, request
//     body hydration via providers.convertRequest, response handling,
//     tool-call repair, reasoning translation, SSE streaming). The
//     coordinator calls into the executor's runOne helper once per
//     spawned attempt and routes the per-attempt result back through
//     the orchestration loop. If you are adding a per-upstream-call
//     behavior, you are in the wrong file — race_coordinator owns the
//     RACE; race_executor owns each ATTEMPT.
// ─────────────────────────────────────────────────────────────────────────────

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
)

// spawnTrigger indicates why parallel requests are being spawned
type spawnTrigger string

const (
	triggerIdleTimeout spawnTrigger = "idle_timeout"
	triggerMainError   spawnTrigger = "main_error"
	// triggerRateLimit (Phase 3 / Round 3b canonical naming) identifies
	// the Case-1 rate-limit trigger that spawns a modelTypeCredFailover
	// row (same model, re-selected credential) instead of a model
	// fallback. triggerMainError continues to identify the plain
	// Case-1 model-fallback trigger.
	triggerRateLimit spawnTrigger = "rate_limit"
)

// spawnTriggerInfo contains detailed information about why a spawn was triggered
type spawnTriggerInfo struct {
	trigger       spawnTrigger
	errorMessage  string // Only populated when trigger is main_error
	failedRequest int    // Index of the failed request, -1 if not applicable

	// credentialID (Phase 3 / Round 3c C3(1)) is populated ONLY for
	// triggerRateLimit spawns: the reselected credential handed back by
	// engine.ExcludeAndReselect. It rides the trigger info from the
	// Case-1 classification site to spawn()/the executor, which applies
	// it to the new upstreamRequest BEFORE the attempt goroutine starts
	// (race-free by construction — the write happens-before `go execute`).
	// Empty for every other trigger type.
	credentialID string

	// modelID (Phase 3 / Round 3j C1 critical fix) is consumed ONLY
	// by modelTypeCredFailover spawns: the credFailover row MUST target
	// the SAME model that just 429'd (NOT c.models[0] — which is wrong
	// in a 2-model chain where the fallback row carries models[1]'s
	// modelID). It is populated at the Case-1 classification site from
	// latestReq.modelID for the rate-limit path.
	//
	// The modelTypeFallback / modelTypeMain / modelTypeSecond spawn
	// paths do NOT consume this field — they read c.models[0] /
	// c.models[1] directly in spawn(). triggerMainError-driven
	// modelTypeFallback rows therefore leave modelID empty (no need to
	// populate); spawn()'s case modelTypeCredFailover is the sole
	// reader, and if the field is empty for that path it defensively
	// falls back to c.models[0] (single-model chain safety).
	modelID string
}

// raceCoordinator manages multiple parallel upstream requests
type raceCoordinator struct {
	mu sync.RWMutex

	baseCtx context.Context
	cfg     *ConfigSnapshot
	req     *http.Request
	rawBody []byte

	// interleaved is the X-Proxy-Interleaved-Thinking flag parsed from the
	// incoming request header (handler.go entry). Threaded down through
	// executeRequest → executeInternalRequest / executeExternalRequest so
	// race-internal sites have the flag without needing *requestContext
	// (which never crosses into race_executor). See B3 (architecture §4.1).
	interleaved bool

	// Phase 3 / Round 3c C3 — engine + conversationKey reach the
	// race coordinator through the constructor; both are optional
	// (nil-safe) and consumed by execute() + manage().
	engine          *credentiallb.Engine
	conversationKey string

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

	// Phase 3 / Task 18 + R3-5 — credential failover bookkeeping. Lives
	// on the coordinator (NOT requestContext — R3-5 keeps the engine
	// stateless about retries; the coordinator is the single owner of
	// the per-race budget and is constructed fresh per race). Mutated
	// only from manage() under c.mu.
	triedCredIDs                      map[string]bool // mutated only from manage() under c.mu — race-free by construction
	failoverAttempts                  int            // mutated only from manage() under c.mu — race-free by construction
	credFailoverSoonestExpiryAttempted bool           // mutated only from manage() under c.mu — race-free by construction

	// Event publishing
	eventBus  *events.Bus
	requestID string

	// Stream deadline error info (set when stream deadline fires with no content)
	streamDeadlineError *FinalErrorInfo
}

func newRaceCoordinator(ctx context.Context, cfg *ConfigSnapshot, req *http.Request, rawBody []byte, models []string, interleaved bool) *raceCoordinator {
	return newRaceCoordinatorWithEvents(ctx, cfg, req, rawBody, models, nil, "", interleaved, nil, "")
}

// newRaceCoordinatorWithEvents (Phase 3 / Round 3c C3) gains two more
// parameters: engine (the Load-Balancing engine; nil-safe) and
// conversationKey (the per-request affinity key). Constructor-only
// injection per W-3 holds end-to-end (cmd/main.go → NewHandler →
// coordinator). The 7th positional arg in NewHandler is the source.
func newRaceCoordinatorWithEvents(
	ctx context.Context,
	cfg *ConfigSnapshot,
	req *http.Request,
	rawBody []byte,
	models []string,
	eventBus *events.Bus,
	requestID string,
	interleaved bool,
	engine *credentiallb.Engine,
	conversationKey string,
) *raceCoordinator {
	if len(models) == 0 {
		models = []string{cfg.ModelID}
	}
	return &raceCoordinator{
		baseCtx:         ctx,
		cfg:             cfg,
		req:             req,
		rawBody:         rawBody,
		interleaved:     interleaved,
		engine:          engine,
		conversationKey: conversationKey,
		models:          models,
		requests:        make([]*upstreamRequest, 0, len(models)),
		winnerIdx:       -1,
		done:            make(chan struct{}),
		streamCh:        make(chan struct{}),
		startTime:       time.Now(),
		spawnTriggers:   make([]spawnTriggerInfo, 0),
		// Phase 3 / Task 19 — per-request tried-set (R3-5: request
		// context, NOT engine state). Initialized here so manage()'s
		// first mutation never sees a nil map.
		triedCredIDs: make(map[string]bool),
		eventBus:     eventBus,
		requestID:    requestID,
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
	case modelTypeCredFailover:
		// Phase 3 / Round 3j C1 critical fix — the credFailover row MUST
		// target the SAME model that just 429'd, NOT c.models[0]. The
		// prior code's hard-coded c.models[0] was wrong in a 2-model
		// chain: a fallback-row 429 would have spawned a credFailover
		// attempt running models[1]'s InternalModel on models[0]'s
		// credential pool (wrong model served + wrong-account billing).
		//
		// The trigger info carries the source modelID (C3(2) wiring,
		// populated at the Case-1 classification site from
		// latestReq.modelID). If unset (defensive — should never happen
		// for a real triggerRateLimit spawn), fall back to c.models[0]
		// to preserve the legacy single-model chain.
		//
		// NOTE (B1 separate accounting): this row does NOT consume a
		// spawn-window slot — the idx guard below main/second/fallback
		// does NOT apply here (a 3-cred single-model chain spawns idx
		// 1 and 2 past len(c.models), by design).
		if len(c.models) == 0 {
			log.Printf("[RACE] Cannot spawn cred-failover: no model available")
			return
		}
		if triggerInfo.modelID != "" {
			modelID = triggerInfo.modelID
		} else {
			modelID = c.models[0]
		}
	}

	req := newUpstreamRequest(idx, mType, modelID, c.cfg.RaceMaxBufferBytes)

	// Set flag to use secondary upstream model for second requests
	if mType == modelTypeSecond {
		req.SetUseSecondaryUpstream(true)
	}

	// Phase 3 / Round 3c C3(1) — a credFailover row carries the
	// pre-resolved credential (engine.ExcludeAndReselect output) on the
	// trigger info. Applied BEFORE `go c.execute(req)` below, so the
	// executor's resolveSpecificInternalCredential read is race-free
	// (goroutine start is the happens-before edge).
	if mType == modelTypeCredFailover && triggerInfo.credentialID != "" {
		req.reselectedCredentialID = triggerInfo.credentialID
	}

	c.requests = append(c.requests, req)
	c.spawnTriggers = append(c.spawnTriggers, triggerInfo)

	log.Printf("[RACE] Spawning %s request (id=%d, model=%s, trigger=%s)", mType, idx, modelID, triggerInfo.trigger)

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
	// Phase 3 / Task 18 — surface the reselected credential on
	// credFailover spawns (operator observability via the SSE feed).
	if triggerInfo.trigger == triggerRateLimit && triggerInfo.credentialID != "" {
		eventData["credential_id"] = triggerInfo.credentialID
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
			//
			// Phase 3 / Round 3c — C1 HOISTED ADMISSION GATE (replaces the
			// pre-Phase-3 window check `len(c.requests) < len(c.models)`):
			//    gate := modelAttempts < len(models) || credFailoverEligibleWithBudget()
			// modelAttempts counts ONLY modelType ∈ {main, second, fallback}
			// (B1 separate accounting — modelTypeCredFailover rows are EXEMPT
			// from the window count and from the all-failed accounting below).
			// The hoisted form is what lets the core scenario (3 creds /
			// 1 model / no fallback chain: `1 < 1` false from the first
			// failure) get its FULL failover chain before terminal.
			modelAttempts := c.modelAttemptCountLocked()
			credEligible := c.credFailoverEligibleLocked()
			gate := modelAttempts < len(c.models) || credEligible
			spawnedCredFailover := false
			if c.winner == nil && gate {
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

						// ── HOISTED CRED-FAILOVER BRANCH (Round 3c C1;
						// Tasks 18-20) ── rate-limit classified AND internal
						// multi-credential model AND pre-first-byte (race guard:
						// c.winner == nil per Task 23 — framing headers +
						// ": connected" carry no model content) AND retry budget
						// remaining ⇒ ExcludeAndReselect + same-model
						// modelTypeCredFailover spawn INSTEAD of model fallback.
						// Precedence: credential failover BEFORE model switching.
						plan := c.credFailoverPlanLocked(latestReq)
						if plan != nil {
							c.mu.Unlock()
							c.publishCredentialFailover(latestReq.modelID, plan.fromCredentialID, plan.credentialID, plan.retryAfter)
							c.spawn(modelTypeCredFailover, spawnTriggerInfo{
								trigger:       triggerRateLimit,
								errorMessage:  errMsg,
								failedRequest: latestReq.id,
								credentialID:  plan.credentialID,
								// Phase 3 / Round 3j C1 critical fix —
								// ride the source modelID on the trigger
								// info so spawn() targets the SAME model
								// that just 429'd (not c.models[0]).
								modelID: latestReq.modelID,
							})
							c.mu.Lock()
							// Budget bookkeeping (Task 19 / R3-5 — mutated only
							// from manage() under c.mu — race-free by construction).
							c.failoverAttempts++
							c.triedCredIDs[plan.credentialID] = true
							if plan.soonestExpiry {
								// W3 single-shot: the SoonestExpiry attempt is
								// spent; the NEXT 429 routes straight to model
								// fallback (no double spawn, no loop).
								c.credFailoverSoonestExpiryAttempted = true
							}
							spawnedCredFailover = true
							// Skip the model-fallback spawn this tick — the
							// credFailover row IS this tick's spawn.
						} else {
							log.Printf("[RACE] Latest request %d failed: %s, spawning fallback directly", latestReq.id, errMsg)
							shouldSpawn = true
							triggerInfo = spawnTriggerInfo{
								trigger:       triggerMainError,
								errorMessage:  errMsg,
								failedRequest: latestReq.id,
							}
						}
					}

					// Case 2: Main request is idle (Parallel race retry)
					// Phase 3 / Round 3c W4 (RE-SCOPE of Round-3b ruling iii):
					// this path spawns second/fallback UNCHANGED — at
					// idle-spawn time NO error exists to classify
					// (latestReq.GetError() is nil for a stalled-but-
					// unfinished request). The stalled attempt's eventual
					// 429 is adjudicated by Case 1 above, which the C1
					// hoisted gate makes reachable in the multi-attempt
					// environment this spawn creates.
					// FIXED: Now uses IsIdle() which tracks last activity time during streaming
					// This correctly detects idle even after the request has started streaming
					if !shouldSpawn && !spawnedCredFailover && c.cfg.RaceParallelOnIdle && len(c.requests) == 1 {
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

				if spawnedCredFailover {
					// credFailover row already spawned this tick — skip the
					// model-fallback spawn path below.
				} else if shouldSpawn {

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
			//
			// Phase 3 / B1 amendment: the accounting counts MODEL attempts only
			// (modelTypeCredFailover rows are exempt). Terminal fires when every
			// request has failed AND no credFailover row was just spawned (that
			// row is still running); after credential exhaustion the flow falls
			// through to the model-fallback spawn above when c.models[1] exists.
			if c.winner == nil && !spawnedCredFailover && modelAttempts >= len(c.models) {
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

// execute is a wrapper for executeRequest.
//
// Phase 3 / Tasks 4 + 6 + race-coordinator-level wiring (Round 3c C3):
// receive the per-coordinator engine + conversationKey from the
// constructor; forward them down to executeRequest on every call so
// the publish site has them.
func (c *raceCoordinator) execute(req *upstreamRequest) {
	// Create context for this specific attempt
	ctx, cancel := context.WithCancel(c.baseCtx)
	req.SetContext(ctx, cancel)

	// Build the per-request "publish model_credential_selected" callback
	// bound to this coordinator's eventBus. The callback reads
	// NewlyBound off the resolved struct (already populated by
	// executeInternalRequest) and fires the event on the bus.
	var publish func(modelID string, cred models.ResolvedCredential)
	if c.eventBus != nil && c.engine != nil {
		publish = func(modelID string, cred models.ResolvedCredential) {
			keyPrefix := c.conversationKey
			if len(keyPrefix) > 8 {
				keyPrefix = keyPrefix[:8]
			}
			c.eventBus.Publish(events.Event{
				Type:      "model_credential_selected",
				Timestamp: time.Now().Unix(),
				Data: map[string]interface{}{
					"model_id":                 modelID,
					"credential_id":            cred.CredentialID,
					"conversation_key_prefix": keyPrefix,
					"id":                       c.requestID,
				},
			})
		}
	}

	err := executeRequest(ctx, c.cfg, c.req, c.rawBody, req, c.interleaved, c.conversationKey, c.engine, publish)

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

// GetRequestStatuses now lives with the Phase 3 Task 18 helpers at the
// bottom of this file (S4 — credFailover rows surfacing). The original
// three-key shape (main/second/fallback) is preserved; the additive
// cred_failover_<n> keys extend it without breaking legacy readers.

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


// ─────────────────────────────────────────────────────────────────────────────
// Phase 3 / Task 18 (Round 3b B1/B3 + Round 3c C1/C3/W3) — coordinator
// credential-failover helpers. All *Locked functions run under c.mu from
// the manage() loop (single-goroutine mutation invariant, R3-5).
// ─────────────────────────────────────────────────────────────────────────────

// credFailoverPlan is the decision output of credFailoverPlanLocked: the
// spawn the Case-1 branch should perform INSTEAD of a model fallback.
type credFailoverPlan struct {
	credentialID     string        // reselected credential (engine output) — rides spawnTriggerInfo.credentialID
	fromCredentialID string        // the credential that 429'd
	retryAfter       time.Duration // from the upstream Retry-After header (0 ⇒ engine default cooldown)
	soonestExpiry    bool          // W3: single-shot SoonestExpiry attempt
}

// modelAttemptCountLocked returns the B1 separate-accounting count of
// MODEL attempts: rows with modelType ∈ {main, second, fallback} only.
// modelTypeCredFailover rows are EXEMPT from the spawn-window gate and
// from the all-failed accounting (Round 3b B1 / Round 3c C1).
func (c *raceCoordinator) modelAttemptCountLocked() int {
	n := 0
	for _, r := range c.requests {
		switch r.modelType {
		case modelTypeMain, modelTypeSecond, modelTypeFallback:
			n++
		}
	}
	return n
}

// credFailoverEligibleLocked is the cheap (side-effect-free) form of
// credFailoverEligibleWithBudget() used by the C1-hoisted admission
// gate. It performs NO engine calls (ExcludeAndReselect has cooldown
// side effects and must run at most once per Case-1 decision).
func (c *raceCoordinator) credFailoverEligibleLocked() bool {
	if c.engine == nil || c.cfg == nil || c.cfg.ModelsConfig == nil || len(c.requests) == 0 {
		return false
	}
	latest := c.requests[len(c.requests)-1]
	if !latest.IsDone() || latest.GetError() == nil {
		return false
	}
	if !providers.IsRateLimitError(latest.GetError()) {
		return false
	}
	// Task 23 pre-first-byte guard (race path): c.winner == nil. Framing
	// headers + ": connected\n\n" precede coordinator.Start() but carry
	// NO model content (handler.go:783-797); the guard is the winner,
	// not headersSent.
	if c.winner != nil {
		return false
	}
	modelCfg := c.cfg.ModelsConfig.GetModel(latest.modelID)
	if modelCfg == nil || !modelCfg.Internal || len(modelCfg.Credentials) <= 1 {
		return false
	}
	// W3: once the single SoonestExpiry attempt is spent, the next 429
	// routes straight to model fallback.
	if c.credFailoverSoonestExpiryAttempted {
		return false
	}
	// R3-5 budget: each remaining credential at most once per request;
	// total failover attempts ≤ len(credentials)−1.
	return c.failoverAttempts < len(modelCfg.Credentials)-1
}

// credFailoverPlanLocked runs the full Round-3b/3c decision chain for
// the failed latestReq: extract Retry-After (errors.As — the executor
// propagates the raw *ProviderError up to the coordinator), call
// engine.ExcludeAndReselect (mode-aware per B3/C2), and map the mode:
//
//   - ReselectHealthy (incl. B2 no-op + empty-key fresh pick) → proceed
//     with the returned credential (spawn below).
//   - ReselectSoonestExpiry → single attempt (soonestExpiry=true; the
//     caller sets the W3 single-shot flag after spawning).
//   - ReselectNone → fall through to model fallback (plan == nil).
//
// The tried-set intersection (R3-5) makes re-selection of a
// tried-this-request credential impossible — no loops by construction.
func (c *raceCoordinator) credFailoverPlanLocked(latestReq *upstreamRequest) *credFailoverPlan {
	if !c.credFailoverEligibleLocked() {
		return nil
	}

	err := latestReq.GetError()

	// Retry-After from the extended ProviderError (Task 17 — survives
	// wrapping; errors.As sees through fmt.Errorf("%w", ...) up the
	// executor's return path). Zero ⇒ the engine seeds its
	// DefaultCooldown (60s) — the classifier itself does NOT default.
	var retryAfter time.Duration
	var providerErr *providers.ProviderError
	if errors.As(err, &providerErr) {
		retryAfter = providerErr.RetryAfter
	}

	// The credential that 429'd: the attempt's resolved credential.
	// modelTypeSecond rows resolve the PRIMARY credential at the
	// executor layer (race_executor.go secondary branch) — a 429 there
	// re-keys failover to the primary credential of c.models[0]
	// (specified behavior per Round 3b, not an accident).
	fromCred := latestReq.resolved.CredentialID
	if fromCred == "" {
		// Resolution itself failed (no credential was used) — not a
		// credential failover candidate.
		return nil
	}

	reselected, mode := c.engine.ExcludeAndReselect(
		latestReq.modelID,
		c.conversationKey, // Round 3c C3(3): injected via constructor
		fromCred,
		retryAfter,
	)

	switch mode {
	case credentiallb.ReselectHealthy:
		if reselected == "" || reselected == fromCred || c.triedCredIDs[reselected] {
			// Engine returned nothing new (or a credential this request
			// already tried — belt-and-braces on top of the engine's
			// cooldown skip): fall through to model fallback.
			log.Printf("[LB-FAILOVER] model=%s no healthy untried credential (reselected=%q); falling through to model fallback",
				latestReq.modelID, reselected)
			return nil
		}
		log.Printf("[LB-FAILOVER] model=%s rate-limited on cred=%s; failing over to cred=%s (attempt %d/%d)",
			latestReq.modelID, fromCred, reselected, c.failoverAttempts+1, modelBudget(c, latestReq.modelID))
		return &credFailoverPlan{
			credentialID:     reselected,
			fromCredentialID: fromCred,
			retryAfter:       retryAfter,
		}

	case credentiallb.ReselectSoonestExpiry:
		// 0-of-N healthy (Round 3b 0-of-N pin): the engine already
		// emitted its WARN; caller performs a SINGLE attempt then falls
		// through (W3 flag + F.4 contract).
		if reselected == "" || reselected == fromCred || c.triedCredIDs[reselected] {
			return nil
		}
		log.Printf("[WARN] [LB-FAILOVER] model=%s all credentials cooling; single attempt with soonest-expiring cred=%s then model fallback",
			latestReq.modelID, reselected)
		return &credFailoverPlan{
			credentialID:     reselected,
			fromCredentialID: fromCred,
			retryAfter:       retryAfter,
			soonestExpiry:    true,
		}

	default: // credentiallb.ReselectNone — no credential available
		// (single-credential model or 0 valid credentials; B2 no-op and
		// empty-key are ReselectHealthy per the C2 unified enum).
		log.Printf("[LB-FAILOVER] model=%s no reselectable credential (mode=%v); falling through to model fallback",
			latestReq.modelID, mode)
		return nil
	}
}

// modelBudget returns len(credentials)−1 (R3-5 budget cap) for the
// INFO log line; 0 when the model config is unavailable.
func modelBudget(c *raceCoordinator, modelID string) int {
	if c.cfg == nil || c.cfg.ModelsConfig == nil {
		return 0
	}
	mc := c.cfg.ModelsConfig.GetModel(modelID)
	if mc == nil {
		return 0
	}
	return len(mc.Credentials) - 1
}

// publishCredentialFailover (Task 20 / R3-8) emits the canonical
// model_credential_failover event with the payload
// {model_id, from_credential_id, to_credential_id, reason,
// retry_after_ms, cooldown_ms, attempt_index}. cooldown_ms mirrors the
// engine's effective seed (retryAfter when the upstream supplied it,
// else credentiallb.DefaultCooldown) — the engine itself never
// publishes (it must not import pkg/events).
func (c *raceCoordinator) publishCredentialFailover(modelID, fromID, toID string, retryAfter time.Duration) {
	if c.eventBus == nil {
		return
	}
	cooldown := credentiallb.DefaultCooldown
	if retryAfter > 0 {
		cooldown = retryAfter
	}
	data := map[string]interface{}{
		"model_id":           modelID,
		"from_credential_id": fromID,
		"to_credential_id":   toID,
		"reason":             "rate_limit",
		"retry_after_ms":     retryAfter.Milliseconds(),
		"cooldown_ms":        cooldown.Milliseconds(),
		"attempt_index":      c.failoverAttempts + 1,
	}
	if c.requestID != "" {
		data["id"] = c.requestID
	}
	c.eventBus.Publish(events.Event{
		Type:      credentiallb.EventCredentialFailover,
		Timestamp: time.Now().Unix(),
		Data:      data,
	})
}

// GetRequestStatuses (Phase 3 / Round 3c — S4): credFailover rows are
// surfaced as additive "cred_failover_<n>" keys alongside the legacy
// main/second/fallback slots (B1 separate accounting keeps the legacy
// three keys model-attempt-only). Each row's reselected credential is
// exposed as "cred_failover_<n>_credential" for operator visibility.
//
// Round 3j W4 — the fallback to `req.resolved.CredentialID` was DROPPED
// rather than reading it under `req.mu`: the only meaningful credential
// identity for a credFailover row is the one the coordinator pre-resolved
// at the Case-1 classification site (`req.reselectedCredentialID`, set
// under c.mu before `go execute(req)` — race-free by construction).
// Reading `req.resolved.CredentialID` here without locking would race
// against `executeInternalRequest`'s lock-free write at
// race_executor.go:293. The belt-and-braces check in
// resolveSpecificInternalCredential (Round 3j C1 step 4) already
// guarantees that a row whose reselected credential was rejected never
// gets past the executor — so the resolved-fallback was both racy AND
// redundant.
func (c *raceCoordinator) GetRequestStatuses() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	statuses := make(map[string]string)
	// Initialize all as not_started
	statuses["main"] = "not_started"
	statuses["second"] = "not_started"
	statuses["fallback"] = "not_started"

	credIdx := 0
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
		case modelTypeCredFailover:
			key := fmt.Sprintf("cred_failover_%d", credIdx)
			statuses[key] = status
			// Round 3j W4 — use ONLY reselectedCredentialID.
			// Reading req.resolved.CredentialID here would race the
			// executor's lock-free write (race_executor.go:293); the
			// reselected credential IS the row's identity for operator
			// observability.
			if req.reselectedCredentialID != "" {
				statuses[key+"_credential"] = req.reselectedCredentialID
			}
			credIdx++
		}
	}

	return statuses
}
