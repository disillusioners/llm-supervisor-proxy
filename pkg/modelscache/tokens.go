package modelscache

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
)

// tokenEntry is one cached token verdict keyed by the hex SHA-256 of
// the plaintext (auth.HashToken — planner correction N1: the key type
// is whatever HashToken returns; the plaintext is NEVER stored).
//
// Three tiers (planner ruling I / C2):
//
//   - positive      — inner said yes; fresh until min(now+PositiveTTL,
//     token.ExpiresAt-clamped view); after that it transitions to
//     stale-positive, NOT a miss.
//   - stale-positive — expired positive kept for the StaleCap window;
//     served ONLY when the inner store answers with an infra-class
//     error (degraded-allow). Never served for verdict-class errors.
//   - negative      — inner said no (ErrTokenNotFound /
//     ErrInvalidTokenFormat / ErrTokenExpired); fail-closed, TTL
//     bounded, never stale-served.
type tokenEntry struct {
	kind     tokenTier
	token    *auth.AuthToken // nil for negative entries
	verdict  error           // the negative verdict to replay (nil otherwise)
	cachedAt time.Time
	// freshUntil is when the positive stops being "fresh": the
	// min(now+PositiveTTL, token.ExpiresAt) clamp.
	freshUntil time.Time
}

type tokenTier int

const (
	tierPositive tokenTier = iota
	tierStalePositive
	tierNegative
)

// CachedTokenStore decorates auth.TokenStoreInterface with the
// three-tier token cache. Tokens drive no fail-fast (only models do
// — the boundary sites consult ConfigStoreHealth), so Healthy() here
// is informational; the stale tier IS the outage behavior.
type CachedTokenStore struct {
	inner auth.TokenStoreInterface
	opts  Options

	mu      sync.RWMutex
	entries *lru
	// idToHash fans out DeleteToken (planner ruling F / W4): Delete
	// only receives the ID, so a reverse index is required to clear
	// the hash-keyed entries. Bounded at defaultIDIndexCap.
	idToHash map[string]string
	idOrder  []string // FIFO bound for idToHash (simple cap eviction)

	stopOnce sync.Once
}

// WrapTokens builds the token cache decorator. No boot priming
// (tokens are strictly lazy), no goroutine — Stop is a no-op kept for
// the deferred-teardown wiring (W2).
func WrapTokens(inner auth.TokenStoreInterface, opts Options) *CachedTokenStore {
	if inner == nil {
		panic("modelscache: WrapTokens requires a non-nil inner token store")
	}
	o := opts.withDefaults()
	return &CachedTokenStore{
		inner:    inner,
		opts:     o,
		entries:  newLRU(o.LRUCap),
		idToHash: make(map[string]string),
	}
}

// now is the single clock read point (correction N6).
func (c *CachedTokenStore) now() time.Time {
	return c.opts.Clock()
}

// Stop is a no-op (W2): the token cache runs no goroutine and the LRU
// is zero-state; the method exists so the deferred teardown wiring
// can call it unconditionally. Idempotent, non-blocking.
func (c *CachedTokenStore) Stop() {
	c.stopOnce.Do(func() {})
}

// Healthy is informational (tokens do not drive fail-fast; exit
// criterion 1B): true when the last inner contact succeeded or no
// inner contact has happened yet.
func (c *CachedTokenStore) Healthy() bool {
	return true
}

// rememberID records id→hash for DeleteToken fan-out (W4), evicting
// the oldest entries beyond defaultIDIndexCap (straight FIFO; losing
// a stale mapping just falls back to TTL expiry for that token —
// accepted residual per planner ruling F).
func (c *CachedTokenStore) rememberID(id, hash string) {
	if id == "" {
		return
	}
	if _, exists := c.idToHash[id]; !exists {
		c.idOrder = append(c.idOrder, id)
		if len(c.idOrder) > defaultIDIndexCap {
			evict := c.idOrder[0]
			c.idOrder = c.idOrder[1:]
			delete(c.idToHash, evict)
		}
	}
	c.idToHash[id] = hash
}

// ValidateToken implements the three-tier state machine (task 1.B.8).
func (c *CachedTokenStore) ValidateToken(ctx context.Context, plaintext string) (*auth.AuthToken, error) {
	// Pre-DB format check mirrors the inner store — a malformed token
	// is a verdict, never infrastructure.
	if !auth.ValidateTokenFormat(plaintext) {
		return nil, auth.ErrInvalidTokenFormat
	}
	hash := auth.HashToken(plaintext)
	now := c.now()

	c.mu.RLock()
	raw, hit := c.entries.Peek(hash)
	c.mu.RUnlock()

	if hit {
		e := raw.(*tokenEntry)
		switch e.kind {
		case tierPositive:
			if now.Before(e.freshUntil) {
				// Fresh positive — serve from cache, zero DB.
				return e.token, nil
			}
			// Expired → this entry is now a stale-positive; fall
			// through to inner revalidation (which may refresh it,
			// convert it to a negative verdict, or — on an infra
			// error — serve it degraded-allow below).
		case tierNegative:
			if now.Sub(e.cachedAt) <= c.opts.NegativeTTL {
				// Fail-closed verdict replay (never stale-served).
				return nil, e.verdict
			}
			// Negative TTL expired → fall through to revalidation.
		}
	}

	token, err := c.inner.ValidateToken(ctx, plaintext)

	switch {
	case err == nil:
		// (a) success → positive; clamp freshness to
		// min(now+PositiveTTL, token expiry). Populate idToHash on the
		// READ path too (W4) so DeleteToken can fan out.
		freshUntil := now.Add(c.opts.PositiveTTL)
		if token != nil && token.ExpiresAt != nil && token.ExpiresAt.Before(freshUntil) {
			freshUntil = *token.ExpiresAt
		}
		c.mu.Lock()
		c.entries.Put(hash, &tokenEntry{
			kind:       tierPositive,
			token:      token,
			freshUntil: freshUntil,
			cachedAt:   now,
		})
		if token != nil {
			c.rememberID(token.ID, hash)
		}
		c.mu.Unlock()
		return token, nil

	case isVerdictClass(err):
		// (c) verdict-class (not-found / bad format / expired) →
		// negative cache, fail-closed, never stale fallback.
		c.mu.Lock()
		c.entries.Put(hash, &tokenEntry{
			kind:     tierNegative,
			verdict:  err,
			cachedAt: now,
		})
		c.mu.Unlock()
		return nil, err

	case isInfraError(err):
		// (b) infra-class error + a cached positive (fresh or stale)
		// exists → degraded-allow: WARN and serve the stale token
		// (leader decision 1 / C2 — the >=1h-zero-failure goal).
		// Without one this is a cold miss during an outage: fail like
		// today (matrix row C → 401).
		//
		// TOCTOU close (review remediation 2026-08-28): re-Peek under
		// c.mu to read the freshest cache state. Between the pre-call
		// Peek and this branch, a concurrent DeleteToken or a racing
		// verdict-class validate may have replaced the positive with a
		// definitive verdict; without this re-read, the pre-call `raw`
		// would let one stale token slip past a freshly-recorded
		// ErrTokenNotFound. The re-Peek closes the race — if the
		// entry is no longer a usable stale-positive, fall through to
		// the not-found path instead of serving a now-revoked token.
		if hit {
			c.mu.RLock()
			fresh, ok := c.entries.Peek(hash)
			c.mu.RUnlock()
			if ok {
				e := fresh.(*tokenEntry)
				if e.kind == tierPositive && e.token != nil &&
					now.Sub(e.cachedAt) <= c.opts.StaleCap {
					log.Printf("[WARN] [cache] token store unreachable (%v) — serving stale-positive token %s (degraded-allow)", err, e.token.ID)
					return e.token, nil
				}
			}
		}
		return nil, err

	default:
		// (d) unknown error class → surface it, no fallback, no cache
		// write (do not poison either tier with an unclassifiable
		// result).
		return nil, err
	}
}

// isVerdictClass reports whether err is a definitive "no" from the
// inner store (planner ruling I non-infra list): not-found, bad
// format, expired.
func isVerdictClass(err error) bool {
	return errors.Is(err, auth.ErrTokenNotFound) ||
		errors.Is(err, auth.ErrInvalidTokenFormat) ||
		errors.Is(err, auth.ErrTokenExpired)
}

// CreateToken delegates to the inner store, then caches the returned
// token as a positive (write-through) and records id→hash.
func (c *CachedTokenStore) CreateToken(ctx context.Context, name string, expiresAt *time.Time, createdBy string, ultimateModelEnabled bool, ultimateModelID string, allowedModels []string) (string, *auth.AuthToken, error) {
	plaintext, token, err := c.inner.CreateToken(ctx, name, expiresAt, createdBy, ultimateModelEnabled, ultimateModelID, allowedModels)
	if err != nil {
		return "", nil, err
	}
	hash := auth.HashToken(plaintext)
	now := c.now()
	freshUntil := now.Add(c.opts.PositiveTTL)
	if expiresAt != nil && expiresAt.Before(now.Add(c.opts.PositiveTTL)) {
		freshUntil = *expiresAt
	}
	c.mu.Lock()
	c.entries.Put(hash, &tokenEntry{
		kind:       tierPositive,
		token:      token,
		freshUntil: freshUntil,
		cachedAt:   now,
	})
	if token != nil {
		c.rememberID(token.ID, hash)
	}
	c.mu.Unlock()
	return plaintext, token, nil
}

// DeleteToken delegates to the inner store, then clears BOTH the
// positive and negative entries for the token's hash (via the
// idToHash reverse index — planner ruling F) and the index entry
// itself (W4 / matrix row C2).
func (c *CachedTokenStore) DeleteToken(ctx context.Context, id string) error {
	if err := c.inner.DeleteToken(ctx, id); err != nil {
		return err
	}
	c.mu.Lock()
	if hash, ok := c.idToHash[id]; ok {
		c.entries.Delete(hash)
		delete(c.idToHash, id)
	}
	c.mu.Unlock()
	return nil
}

// UpdateTokenPermission delegates to the inner store, then drops the
// cached entry for the token (permission changes must not be masked
// by a stale positive; the next ValidateToken re-reads).
func (c *CachedTokenStore) UpdateTokenPermission(ctx context.Context, id string, ultimateModelEnabled bool, ultimateModelID string, allowedModels []string) error {
	if err := c.inner.UpdateTokenPermission(ctx, id, ultimateModelEnabled, ultimateModelID, allowedModels); err != nil {
		return err
	}
	c.mu.Lock()
	if hash, ok := c.idToHash[id]; ok {
		c.entries.Delete(hash)
		// keep the idToHash entry: the token still exists, only its
		// permissions changed — a later ValidateToken re-populates the
		// same hash mapping.
	}
	c.mu.Unlock()
	return nil
}

// ListTokens passes straight through to the inner store (UI-only;
// W5: a DB-down UI call returns the DB error to the caller — no cache
// hidden behind).
func (c *CachedTokenStore) ListTokens(ctx context.Context) ([]auth.AuthToken, error) {
	return c.inner.ListTokens(ctx)
}

// GetTokenByID passes straight through to the inner store (UI-only).
func (c *CachedTokenStore) GetTokenByID(ctx context.Context, id string) (*auth.AuthToken, error) {
	return c.inner.GetTokenByID(ctx, id)
}

// Compile-time interface checks.
var (
	_ auth.TokenStoreInterface = (*CachedTokenStore)(nil)
)
