// Package decision is the decision-models endpoint for the
// supervisor proxy (Decision Models commission / §3.2).
//
// P1 SCOPE — this file documents the package architecture at the
// SKELETON level only. The Phase 1 surface is:
//
//   - The route exists at POST /v1/decisions (registered in
//     cmd/main.go) but is BY DESIGN a 502 barrier until P2
//     providers land (registry is empty at P1 — provider lookup
//     fails ⇒ "no provider registered" 502).
//   - P1 implements: handler.go (entry, auth, parse, model
//     resolve with explicit IsDecision() check, credential
//     resolve, provider lookup, timeout ctx), client.go (HTTP
//     client port), converter.go (Outcome + UpstreamProvider
//     interface), registry.go (local registry; empty at P1),
//     doc.go (this file).
//   - P1 does NOT implement: types.go (typed structs), validators.go
//     (typed gotcha predicates), openaidecision.go / typesafe.go
//     (per-provider adapters, success-only metering, DecisionEvent
//     publish). Those land in P2.
//
// Architectural rules (binding on the package — Decision Models
// commission / §3.2 / amendment 1):
//
//   - Import direction: pkg/decision → {pkg/models, pkg/auth,
//     pkg/usage, pkg/events} ONLY; never pkg/proxy (the chat
//     supervision exclusion boundary) or pkg/imggen (the
//     chat-imggen interaction surface). The import guard
//     runs in `make test` (Makefile:grep + handler_test.go
//     TestPackage_ImportGuard_NoProxyImports +
//     TestPackage_ImportGuard_TransitiveClosure); the guard is
//     mechanical and fails the build on any pkg/proxy or
//     pkg/imggen (transitive) import.
//   - One upstream attempt per request (no retry, no fallback, no
//     race coordinator — decisions are cheap+fast, sync, and not
//     supervised). The handler is shaped off imggen's skeleton
//     (handle.chat_misroute_test.go and pkg/imggen/handler.go
//     are the templates).
//   - Single classifier seam (Outcome{RelayStatus, Reason}) —
//     InspectResponse is called for EVERY upstream response
//     (no pre-filter on resp.StatusCode). Interface lives at
//     converter.go (signature unified per R1.5:
//     InspectResponse(respBytes, originalReq)).
//   - Bounded RAM: request MaxBytesReader 16 MB; response
//     io.LimitReader 64 MB; one read, classify+relay+meter+event
//     reuse the same []byte (Amendment 12 single-materialization
//     audit, ported from imggen).
//   - Misroute closes BOTH directions at the chat surface: a
//     chat-model posted to /v1/decisions gets a 400 + house
//     OpenAI envelope code "decision_misroute" (closes the
//     imggen-style reverse-direction gap — chat models flow
//     freely on their own routes, decision models ONLY on
//     /v1/decisions).
package decision
