// Package skipcache — the UPSTREAM-layer health cache (wi-4d2c8e17 /
// ruling 2; originally wi-7769a812 part 2 / USER RULING D3).
//
// LAYER CONTRACT (single source of truth for the three-cache split):
//
//	GROUP    — "is this member servable IN THIS GROUP?"  (grouplb policy)
//	MODEL    — "is this MODEL limited/unavailable?"      (pkg/modelcache)
//	UPSTREAM — "is this REMOTE/CREDENTIAL working?"      (this package)
//
// The layers NEVER read each other's state. THIS cache is WRITTEN by
// credential/provider failures (a 429/402 on a specific credential, or
// a provider outage) for a (credential|provider, model) pair, and READ
// by credential selection + attempt suppression ONLY. It is NEVER read
// by the group pick (structurally: pkg/store/database's group-rewrite
// file does not import this package) and never decides membership.
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
// ORDERING-NOT-FILTERING (wi-7769a812 part 3 — P0 fix, USER-AUTHORITATIVE),
// extended by the layer separation (wi-4d2c8e17):
//
//	The cache is an optimization, not the truth: it may reorder
//	candidates, never exclude them — and it may suppress an ATTEMPT,
//	never a PICK or a CHAIN.
//
// Upstream marks REORDER credential preference in the attempt path
// (preferUnmarkedUpstreamCredential: an unmarked, non-cooling sibling
// is preferred; all-marked still attempts the soonest-recovering) —
// attempt SUPPRESSION is the MODEL layer's job (pkg/modelcache), and
// neither ever benches a member from the group pick. A WIN clears the
// winner's own live marks (Cache.Clear) — a proven-healthy identity
// must not stay benched.
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
// mint zero-TTL marks — that would silently disable the demotion).
// A live mark is extended (idempotent re-mark shape). Nil receiver:
// no-op. Marks DEMOTE for the TTL window — they never exclude (see
// the package doctrine above).
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

// Clear removes a live mark for (scope, id, model) — the WIN-CLEAR
// seam: a race WIN on the marked identity is direct evidence of
// health, so the mark must not outlive it. Missing mark: no-op.
// Nil receiver / empty id / empty model: no-op.
//
// The cache is an optimization, not the truth: it may reorder
// candidates, never exclude them — Clear exists so a proven-healthy
// identity returns to the head of the order immediately instead of
// waiting out its TTL at the tail.
func (c *Cache) Clear(scope, id, model string) {
	if c == nil || id == "" || model == "" {
		return
	}
	c.mu.Lock()
	delete(c.marked, key(scope, id, model))
	c.mu.Unlock()
}

// Pair is one (scope, id) candidate to probe for a model — the shape
// the attempt path's credential-reorder reader
// (preferUnmarkedUpstreamCredential) builds from a model's credential
// refs to probe a credential's/provider's live marks.
type Pair struct {
	Scope string
	ID    string
}

// AnyMarked reports whether ANY of the pairs is marked for model.
// Nil receiver / empty pairs: false.
//
// NOTE (wi-4d2c8e17 layer separation): AnyMarked has NO production
// caller — the retired group-selection health probe was its consumer,
// and R1 forbids selection from reading this cache. The API is kept
// (exported surface; pinned by this package's own tests) but nothing
// outside pkg/skipcache may consult it for selection decisions.
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
// model — the recovery-ordering input the attempt path's
// credential-reorder reader uses to prefer the soonest-recovering
// candidate when every credential is marked. ok=false when nothing is
// marked. Nil receiver: ok=false.
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
