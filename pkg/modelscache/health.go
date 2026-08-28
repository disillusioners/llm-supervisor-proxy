// Package modelscache implements the DB caching / resilience layer
// (db-cache-layer Phase 1, Option A+): caching decorators installed at
// the composition-root seams so the proxy survives ≥1h database
// outages with zero silent misroutes, zero silent auth drops, and
// zero silent-empty model lists.
//
// Two decorators:
//
//   - CachedModelsConfig wraps models.ModelsConfigInterface (models +
//     decrypted credentials; deep copy-on-read; negative caching; boot
//     priming with abort-on-error; 60s reconciler with
//     swap-only-on-success; 24h staleness cap; synchronous
//     write-through on model mutators; invalidate-only on credential
//     mutators).
//   - CachedTokenStore wraps auth.TokenStoreInterface (three-tier:
//     positive / stale-positive served ONLY on infra-class error /
//     negative never stale; 60s positive & negative TTL; LRU cap 10k).
//
// Dependency edge (one-way): this package imports pkg/models,
// pkg/auth and pkg/store/database (the ErrDecryptionFailed alias
// only). It MUST NOT import pkg/proxy or pkg/ui — they import it.
package modelscache

import (
	"context"
	"database/sql/driver"
	"errors"
	"net"
	"strings"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// ConfigStoreHealth is the minimal health capability exposed by
// CachedModelsConfig and type-asserted at the two proxy boundary
// sites (pkg/proxy/handler_functions.go, handler_anthropic.go):
// resolvedModel == nil && !Healthy() → fail-fast 503
// config_store_unavailable instead of silent external passthrough.
type ConfigStoreHealth interface {
	// Healthy reports whether the config store was reachable as of
	// the last refresh (boot priming or reconciler tick). During a DB
	// outage it returns false while cached data keeps being served.
	Healthy() bool
}

// Exported contract errors (consumed by the proxy boundary sites and
// the contract tests).
var (
	// ErrConfigUnavailable — the underlying configuration store could
	// not answer and the requested entity is not cached (failure-mode
	// matrix row 4). Surfaced at the boundary as 503
	// config_store_unavailable.
	ErrConfigUnavailable = errors.New("configuration store unavailable")

	// ErrCredentialMissing — a cached model references a credential
	// that is missing or undecryptable (matrix row 5). Never a
	// dangling reference, never ciphertext.
	ErrCredentialMissing = errors.New("credential missing or undecryptable")

	// ErrDecryptionFailed is an alias of the database-level sentinel
	// (single source of truth) so errors.Is matches across both
	// packages.
	ErrDecryptionFailed = database.ErrDecryptionFailed
)

// infraErrorFragments are the last-resort string matches used by
// isInfraError. Fragility note (planner ruling I): Go's net package
// does not always wrap errors in *net.OpError (e.g. driver-level
// timeouts), so an opaque string match is the final fallback. The
// list is intentionally short.
var infraErrorFragments = []string{
	"connection refused",
	"no such host",
	"i/o timeout",
	"connection reset",
	"database is closed", // sqlite pool closed under the cache (dev mode / teardown races)
	// PostgreSQL mid-flight disconnect shapes (review remediation
	// 2026-08-28). Without these, a TTL-expired valid token 401s
	// instead of stale-serving for the seconds-wide outage-onset
	// window where the connection drops mid-call.
	"unexpected eof",
	"server closed the connection unexpectedly",
}

// isInfraError classifies an inner-store error as "infrastructure
// cannot answer right now" (→ stale-tier fallback allowed) versus
// "verdict is no" (→ fail-closed). Whitelist per planner ruling I:
//
//   - *net.OpError with non-empty Op (connection refused, DNS, …)
//   - context.DeadlineExceeded (timeout)
//   - database/sql driver.ErrBadConn
//   - string-match fallback on the fragments above
//
// context.Canceled is deliberately NOT whitelisted here: caller-side
// cancellation is a verdict about the caller, not an outage (the
// cache's own shutdown cancellation never reaches ValidateToken
// because the cache forwards the caller's context).
func isInfraError(err error) bool {
	if err == nil {
		return false
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) && netErr.Op != "" {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if errors.Is(err, driver.ErrBadConn) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, frag := range infraErrorFragments {
		if strings.Contains(msg, frag) {
			return true
		}
	}
	return false
}
