// Package skipcache — the global upstream skip cache (wi-7769a812
// part 2 / USER RULING D3, user-designed, FINAL).
//
// A process-wide, in-memory registry of LIMIT-CLASS upstream failures:
// (credential, model) and (provider, model) pairs are MARKED when an
// upstream answer is quota/rate/billing-class (429/402 family —
// providers.IsRateLimitError / IsQuotaError). NEVER marked on 5xx,
// timeouts, or client-attributable failures. A mark carries the
// CONFIG-OWNED TTL (config.upstream_skip_cache_ttl, default 60s, hot
// per-request via the proxy's ConfigSnapshot); the expiry TIME is
// minted at mark time so every reader sees one consistent window.
//
// Selection consults the cache: a member whose upstreams are all
// marked is skipped while unmarked alternatives exist (the Phase-0
// A2 credential-blind-selection fix), and a provider-wide wall
// (every credential of the provider marked for the model) skips
// every member on that provider.
//
// THE CACHE IS AN OPTIMIZER, NEVER AN AVAILABILITY GATE: when every
// candidate is marked, the caller DEGRADES to one honest attempt
// against the SOONEST-EXPIRING mark (SoonestExpiry). Nothing in this
// package ever refuses a request.
//
// Concurrency: the read path is a single RLock map lookup on a
// composite string key (no allocation on the read path beyond the
// key build); mark/expiry are race-safe (guarded by the same RWMutex;
// pinned by -race tests in skip_test.go). Marks are idempotent:
// re-marking while marked EXTENDS the window (matches the engine
// cooldown shape it subsumes).
//
// Scope vocabulary (single source of truth with the marking sites):
//
//	ScopeCredential — key = credential ID
//	ScopeProvider   — key = lowercased provider name
package skipcache

import (
	"sync"
	"time"
)

// Scope constants — the composite key's first segment.
const (
	ScopeCredential = "credential"
	ScopeProvider   = "provider"
)

// DefaultTTL is the package fallback when the caller has no config
// (nil-cache-safe call sites pass this so behavior degrades to the
// documented 60s default rather than a zero-length window). The
// CONFIG value owns the real TTL; this constant is the floor for
// legacy embeddings only.
const DefaultTTL = 60 * time.Second

// Cache is the upstream skip registry. Nil-receiver-safe: every
// method on a nil *Cache is the "unmarked" no-op, so unwired callers
// (legacy tests, cache-layer-disabled boots) degrade to pre-feature
// behavior byte-identically.
type Cache struct {
	mu     sync.RWMutex
	marked map[string]time.Time // composite key → expiry
}

// New returns an empty cache.
func New() *Cache {
	return &Cache{marked: make(map[string]time.Time)}
}

// key builds the composite map key. Scopes and keys are caller-
// controlled ids (credential IDs, provider names) — never user text;
// the '|' separator cannot collide because ids are opaque tokens
// without separators in practice AND the scope segment is fixed.
func key(scope, id, model string) string {
	return scope + "|" + id + "|" + model
}

// Mark records a limit-class failure for (scope, id, model) for ttl.
// ttl <= 0 is a no-op (callers without a configured window must not
// mint zero-TTL marks — that would silently disable the skip). A
// live mark is extended (idempotent re-mark shape). Nil receiver: no-op.
func (c *Cache) Mark(scope, id, model string, ttl time.Duration) {
	if c == nil || ttl <= 0 || id == "" || model == "" {
		return
	}
	until := time.Now().Add(ttl)
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.marked[key(scope, id, model)]; ok && existing.After(until) {
		return // a longer window is already live — never shorten
	}
	c.marked[key(scope, id, model)] = until
}

// Marked reports whether (scope, id, model) is currently marked.
// Expired marks are lazily deleted. Nil receiver: false (never skip).
func (c *Cache) Marked(scope, id, model string) bool {
	if c == nil || id == "" || model == "" {
		return false
	}
	k := key(scope, id, model)
	c.mu.RLock()
	until, ok := c.marked[k]
	c.mu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().After(until) {
		c.mu.Lock()
		// Re-check under the write lock — another reader may have
		// refreshed the mark between the RUnlock and this Lock.
		if cur, ok2 := c.marked[k]; ok2 && time.Now().After(cur) {
			delete(c.marked, k)
		}
		c.mu.Unlock()
		return false
	}
	return true
}

// Pair is one (scope, id) candidate to probe for a model — the shape
// selection call sites build from a member's credential refs.
type Pair struct {
	Scope string
	ID    string
}

// AnyMarked reports whether ANY of the pairs is marked for model —
// the "at least one healthy upstream remains" probe. Nil receiver /
// empty pairs: false.
func (c *Cache) AnyMarked(pairs []Pair, model string) bool {
	if c == nil {
		return false
	}
	for _, p := range pairs {
		if c.Marked(p.Scope, p.ID, model) {
			return true
		}
	}
	return false
}

// SoonestExpiry returns the EARLIEST live expiry among the pairs for
// model — the degradation anchor ("one honest attempt against the
// soonest-expiring mark"). ok=false when nothing is marked. Nil
// receiver: ok=false.
func (c *Cache) SoonestExpiry(pairs []Pair, model string) (time.Time, bool) {
	if c == nil {
		return time.Time{}, false
	}
	now := time.Now()
	var soonest time.Time
	found := false
	c.mu.RLock()
	for _, p := range pairs {
		until, ok := c.marked[key(p.Scope, p.ID, model)]
		if !ok || now.After(until) {
			continue
		}
		if !found || until.Before(soonest) {
			soonest, found = until, true
		}
	}
	c.mu.RUnlock()
	return soonest, found
}
