// Package modelcache — the MODEL-layer cache of request-time
// separation (wi-4d2c8e17 / ruling 2). It answers exactly one
// question:
//
//	"is this MODEL limited/unavailable?"
//
// LAYER CONTRACT (single source of truth for the three-cache split):
//
//	GROUP    — "is this member servable IN THIS GROUP?"  (grouplb policy)
//	MODEL    — "is this MODEL limited/unavailable?"      (this package)
//	UPSTREAM — "is this REMOTE/CREDENTIAL working?"      (pkg/skipcache)
//
// The layers NEVER read each other's state. This cache is WRITTEN by
// model-attributable failures only (the model's own plan/usage wall —
// e.g. an upstream "Usage limit reached for 5 hour. Your limit will
// reset at …" body) and READ by the model/chain layer only (suppress
// that model's ATTEMPT at zero cost and proceed INTO its fallback
// chain). It is NEVER read by the group pick — by construction the
// group-pick code path does not import this package (see
// pkg/store/database/store_group_rewrite.go).
//
// DOCTRINE (extends pkg/skipcache verbatim):
//
//	The cache is an optimization, not the truth: it may reorder
//	candidates, never exclude them — and it may suppress an ATTEMPT,
//	never a PICK or a CHAIN.
//
// Concurrency: reads take an RLock map lookup; writes/expiry take the
// write lock. Marks are idempotent (re-marking EXTENDS the window) and
// nil-receiver-safe (an unwired Cache is the always-unmarked no-op, so
// legacy/test embeddings degrade byte-identically).
package modelcache

import (
	"sync"
	"time"
)

// DefaultTTL is the package fallback window when a wall is detected
// but no reset instant can be parsed (or a caller has no config). 15m
// matches the quota-class horizon: a model that is usage-walled
// recovers on a plan/top-up boundary, not a per-request cooldown.
const DefaultTTL = 15 * time.Minute

// Cache is the model-layer registry. Nil-receiver-safe.
type Cache struct {
	mu     sync.RWMutex
	marked map[string]time.Time // model (wire identity) → expiry
}

// New returns an empty cache.
func New() *Cache {
	return &Cache{marked: make(map[string]time.Time)}
}

// Mark records a model-attributable limit for ttl. ttl <= 0 or empty
// model is a no-op. A live mark is EXTENDED (idempotent re-mark shape;
// never shortened). Nil receiver: no-op.
func (c *Cache) Mark(model string, ttl time.Duration) {
	if c == nil || ttl <= 0 || model == "" {
		return
	}
	c.MarkUntil(model, time.Now().Add(ttl))
}

// MarkUntil records a model-attributable limit through an explicit
// instant. until <= now or empty model is a no-op. A live mark is
// EXTENDED but never shortened. Nil receiver: no-op.
func (c *Cache) MarkUntil(model string, until time.Time) {
	if c == nil || model == "" || !until.After(time.Now()) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.marked[model]; ok && existing.After(until) {
		return // a longer window is already live — never shorten
	}
	c.marked[model] = until
}

// Marked reports whether model is currently marked. Expired marks are
// lazily deleted. Nil receiver: false (never suppress).
func (c *Cache) Marked(model string) bool {
	if c == nil || model == "" {
		return false
	}
	c.mu.RLock()
	until, ok := c.marked[model]
	c.mu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().After(until) {
		c.mu.Lock()
		// Re-check under the write lock — another reader may have
		// refreshed the mark in the gap.
		if cur, ok2 := c.marked[model]; ok2 && time.Now().After(cur) {
			delete(c.marked, model)
		}
		c.mu.Unlock()
		return false
	}
	return true
}

// Until returns the live expiry for model. ok=false when unmarked.
// Nil receiver: false.
func (c *Cache) Until(model string) (time.Time, bool) {
	if c == nil || model == "" {
		return time.Time{}, false
	}
	c.mu.RLock()
	until, ok := c.marked[model]
	c.mu.RUnlock()
	if !ok || time.Now().After(until) {
		return time.Time{}, false
	}
	return until, true
}

// Clear removes a live mark for model — the WIN-CLEAR seam (a proven
// healthy attempt clears its own model mark). Missing mark: no-op.
// Nil receiver / empty model: no-op.
func (c *Cache) Clear(model string) {
	if c == nil || model == "" {
		return
	}
	c.mu.Lock()
	delete(c.marked, model)
	c.mu.Unlock()
}
