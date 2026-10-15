// Package decision is the decision-models endpoint for the
// supervisor proxy (Decision Models commission / §3.2).
//
// Architectural rules (binding on the package):
//
//   - Import direction: pkg/decision → {pkg/models, pkg/auth,
//     pkg/usage, pkg/events} ONLY; never pkg/proxy (the chat
//     supervision exclusion boundary) or pkg/imggen (the
//     chat-imggen interaction surface). The import guard
//     runs in `make test` (Makefile:grep + handler_test.go
//     TestPackage_ImportGuard_NoProxyImports +
//     TestPackage_ImportGuard_TransitiveClosure +
//     TestPackage_ImportGuard_NoImggenImports); the guard is
//     mechanical and fails the build on any pkg/proxy or
//     pkg/imggen (transitive) import.
//
//   - One upstream attempt per request (no retry, no fallback,
//     no race coordinator — decisions are cheap+fast, sync, and
//     not supervised). The handler is shaped off imggen's
//     skeleton (pkg/imggen/handler.go is the template).
//
//   - Single classifier seam (Outcome{RelayStatus, Reason}) —
//     InspectResponse is called for EVERY upstream response
//     (no pre-filter on resp.StatusCode). Interface lives at
//     converter.go with the unified 2-arg typed signature
//     (finding 5): InspectResponse(respBytes, originalReq).
//
//   - Bounded RAM: request MaxBytesReader 16 MB; response
//     io.LimitReader 64 MB; one read, classify+relay+meter+event
//     reuse the same []byte (Amendment 12 single-materialization
//     audit, ported from imggen).
//
//   - Misroute closes BOTH directions at the chat surface: a
//     chat-model posted to /v1/decisions gets a 400 + house
//     OpenAI envelope code "decision_misroute" (closes the
//     imggen-style reverse-direction gap — chat models flow
//     freely on their own routes, decision models ONLY on
//     /v1/decisions).
//
//   - Provider ablation: typed seam per amendment 4. The
//     openai-decision adapter is byte-faithful (Q18 / A26);
//     the typesafe adapter does a typed struct ↔ System One
//     translation. Both satisfy the unified UpstreamProvider
//     interface in converter.go.
package decision
