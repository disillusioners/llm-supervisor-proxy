package stickyorder

import (
	"log"
	"sync"
	"time"
)

// Production defaults. Both are package constants, not config fields
// (leader lock): the TTL is a policy choice justified by the provider
// prompt-cache window, not an operator knob.
const (
	// DefaultStickyTTL is the sliding IDLE TTL of a winner record. It
	// is ~one provider prompt-cache window (Anthropic ~5m, OpenAI
	// comparable) PLUS a realistic conversation-resumption gap, and 48x
	// shorter than the credentiallb 24h binding default — a 24h-old
	// winner is likely stale under fleet/config drift and would
	// over-suppress racing diversity, while 5m would drop affinity
	// during ordinary think-time gaps.
	DefaultStickyTTL = 30 * time.Minute

	// DefaultSweepInterval is the janitor cadence (house default, same
	// as credentiallb.DefaultSweepInterval and grouplb's).
	DefaultSweepInterval = 5 * time.Minute
)

// winnerEntry is one conversation's last DELIVERED success.
type winnerEntry struct {
	modelID   string
	updatedAt time.Time
}

// Engine maps conversationKey -> last delivered-success modelID with a
// sliding idle TTL.
//
// Concurrency: safe for concurrent use. All exported methods are
// thread-safe and nil-receiver-safe (a nil *Engine is a no-op).
//
// Lifecycle:
//
//	e := NewEngine(DefaultStickyTTL, DefaultSweepInterval)
//	defer e.Stop()
type Engine struct {
	mu      sync.RWMutex           // house rule: plain map + explicit RWMutex, NO sync.Map
	winners map[string]winnerEntry // conversationKey -> last delivered-success model

	ttl      time.Duration // sliding IDLE ttl, refreshed on RecordSuccess only
	sweepInt time.Duration // janitor cadence

	stopCh    chan struct{} // janitor shutdown
	sweepDone chan struct{} // janitor exit ack
	stopOnce  sync.Once

	// clock is the time source. It is a nil-by-default-to-time.Now
	// TEST SEAM: production never sets it; package tests swap it to
	// drive TTL expiry deterministically without sleeps. Unexported so
	// it adds no public API surface.
	clock func() time.Time
}

// NewEngine builds an engine and starts its janitor goroutine. ttl and
// sweepInterval <= 0 fall back to the package defaults.
func NewEngine(ttl, sweepInterval time.Duration) *Engine {
	if ttl <= 0 {
		ttl = DefaultStickyTTL
	}
	if sweepInterval <= 0 {
		sweepInterval = DefaultSweepInterval
	}
	e := &Engine{
		winners:   make(map[string]winnerEntry),
		ttl:       ttl,
		sweepInt:  sweepInterval,
		stopCh:    make(chan struct{}),
		sweepDone: make(chan struct{}),
		clock:     time.Now,
	}
	go e.runJanitor()
	return e
}

// now returns the engine's current time (test seam aware).
func (e *Engine) now() time.Time {
	if e.clock == nil {
		return time.Now()
	}
	return e.clock()
}

// runJanitor is the single background sweep goroutine.
func (e *Engine) runJanitor() {
	defer close(e.sweepDone)
	ticker := time.NewTicker(e.sweepInt)
	defer ticker.Stop()
	for {
		select {
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.sweepOnce()
		}
	}
}

// sweepOnce performs one janitor pass, deleting entries whose idle age
// exceeds the TTL. The body is wrapped in defer/recover: a panic is
// logged and the next tick runs another sweep instead of silently
// killing the janitor.
func (e *Engine) sweepOnce() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[WARN] [STICKY] janitor sweep recovered from panic: %v", r)
		}
	}()
	now := e.now()
	e.mu.Lock()
	defer e.mu.Unlock()
	for key, entry := range e.winners {
		if now.Sub(entry.updatedAt) > e.ttl {
			delete(e.winners, key)
		}
	}
}

// Get returns the recorded last-success model for key. It is read-only:
// it does NOT refresh the TTL (a promoted winner that then LOSES the
// race must not immortalize its slot — the next winner's RecordSuccess
// overwrites and refreshes it). An entry past its TTL is a miss even if
// the janitor has not swept it yet.
//
// No-op on "" key / nil receiver.
func (e *Engine) Get(key string) (modelID string, ok bool) {
	if e == nil || key == "" {
		return "", false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	entry, found := e.winners[key]
	if !found {
		return "", false
	}
	if e.now().Sub(entry.updatedAt) > e.ttl {
		return "", false
	}
	return entry.modelID, true
}

// RecordSuccess records modelID as the last DELIVERED success for key
// and refreshes the record's timestamp (sliding TTL).
//
// No-op on "" key, "" modelID, or nil receiver.
func (e *Engine) RecordSuccess(key, modelID string) {
	if e == nil || key == "" || modelID == "" {
		return
	}
	e.mu.Lock()
	e.winners[key] = winnerEntry{modelID: modelID, updatedAt: e.now()}
	e.mu.Unlock()
	log.Printf("[STICKY] success recorded key=%s model=%s", KeyPrefix(key), modelID)
}

// InvalidateOnFailure deletes the record ONLY when modelID equals the
// currently recorded winner: a fallback's failure says nothing about the
// winner's health, and winner-matching keeps the record a pure
// last-delivered-success signal. Any other failure is a no-op.
//
// No-op on "" key / nil receiver; logs only on an actual deletion.
func (e *Engine) InvalidateOnFailure(key, modelID string) {
	if e == nil || key == "" {
		return
	}
	e.mu.Lock()
	entry, found := e.winners[key]
	if !found || entry.modelID != modelID {
		e.mu.Unlock()
		return
	}
	delete(e.winners, key)
	e.mu.Unlock()
	log.Printf("[STICKY] invalidated key=%s model=%s reason=attempt_failure", KeyPrefix(key), modelID)
}

// Stop terminates the janitor goroutine. Idempotent, nil-safe and
// zero-value-safe: repeat calls return immediately (the ack channel is
// closed), and a ZERO-VALUE Engine (fields never set by NewEngine — its
// channels are nil) must not block forever on a nil sweepDone, so the
// nil-channel case returns immediately.
func (e *Engine) Stop() {
	if e == nil || e.stopCh == nil || e.sweepDone == nil {
		return
	}
	e.stopOnce.Do(func() {
		close(e.stopCh)
	})
	<-e.sweepDone
}

// KeyPrefix renders a conversation key for [STICKY] log lines: the
// 8-char prefix precedent from the [LB] conversationKey lines, followed
// by an ellipsis. A key of 8 characters or fewer is rendered whole, with
// the same ellipsis marker — the ONE canonical rendering, shared by the
// engine's own log lines and the proxy handler's promotion line (no
// divergent short-key handling at the call sites).
func KeyPrefix(key string) string {
	if len(key) <= 8 {
		return key + "…"
	}
	return key[:8] + "…"
}
