// Package stickyorder remembers, per conversation key, which model last
// DELIVERED a successful response, so the proxy's race chain can promote
// that model to index 0 on the next request. A first-shot hit on the
// provider's warm prompt cache is the payoff: lower latency, higher
// cache-hit rate, lower cost.
//
// # Scope
//
// This package touches race ORDER only. It never changes chain
// membership, never changes model definitions, and has no coupling to
// the group load balancer (pkg/grouplb) or the credential load balancer
// (pkg/credentiallb). The conversation key is the caller's existing
// affinity key (credentiallb.ComputeConversationKey) — this package
// never computes a hash of its own.
//
// # House pattern
//
// Mirrors the credentiallb binding registry shape:
//
//   - plain map + explicit sync.RWMutex (NO sync.Map)
//   - sliding idle TTL, refreshed on RecordSuccess only
//   - background janitor with defer/recover (a panicking sweep is
//     logged and the next tick retries), 5m default cadence
//   - stopCh/sweepDone/stopOnce shutdown; Stop is idempotent
//   - in-memory only: a restart is a cold start (no rehydration), per
//     the grouplb no-rehydration ruling
//
// # Deliberate deviation
//
// Every exported method is a no-op on a nil receiver (credentiallb's
// methods are nil-safe too, but stickyorder leans on it as a documented
// seam contract): the proxy handler's sticky engine is an optional
// constructor injection, and nil means "feature absent" for legacy and
// test paths. Making the engine itself nil-tolerant keeps the six
// call-site families free of nil guards.
//
// # Layering
//
// Imports the standard library only. It must never import pkg/proxy,
// pkg/store, or pkg/config.
package stickyorder
