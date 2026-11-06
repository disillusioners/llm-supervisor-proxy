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
// # Recording is conservative — delivered success only
//
// The proxy records a winner only when the response actually reached the
// client as a completed completion. These shapes are deliberately NOT
// recorded (each one would pin a model that did not deliver):
//
//   - race_deadline_pick: the best-partial buffer promoted by the stream
//     deadline is a partial artifact, even when the stream happens to
//     run to completion afterwards (upstreamRequest.deadlinePick gates
//     the record sites).
//   - race_winner_selected: first-byte eligibility, not delivery.
//   - client write failure, BOTH lanes, symmetric: the last client
//     write's outcome gates the record — the OpenAI stream lane
//     returns before its record site; the OpenAI non-stream lane and
//     finalizeAnthropicSuccess capture the last write's outcome and
//     skip the record when it failed.
//   - Anthropic truncated terminals (missing upstream [DONE] on the
//     live path, scanner read error, client disconnect): closed with
//     an error event, never recorded — mirrors the buffered path's
//     existing skip. Known residual: the internal-live path's write
//     outcomes are not surfaced and are not gated; truncated
//     terminals do not fire winner-matched invalidation (stale pins
//     self-heal on the next failed attempt).
//   - idle-terminated partial: an idle-terminated stream is closed with
//     an error/termination marker before any completed publish, so no
//     record fires.
//   - the ultimate-model path: not a race delivered success at all, and
//     its model is outside the raced list.
//
// An unrecorded conversation is exactly today's behaviour (un-promoted
// racing), so every exclusion degrades to the pre-feature path.
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
