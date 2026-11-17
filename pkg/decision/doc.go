// Package decision is the decision-models endpoint for the
// supervisor proxy (Decision Models commission / §3.2).
//
// Architectural rules (binding on the package):
//
//   - Import direction: the chat supervision subtrees are BANNED
//     from this package (the chat-imggen interaction surface and the
//     chat supervision exclusion boundary). The import guard runs in
//     `make test` (Makefile:grep + handler_test.go
//     TestPackage_ImportGuard_NoProxyImports +
//     TestPackage_ImportGuard_TransitiveClosure +
//     TestPackage_ImportGuard_NoImggenImports); the guard is
//     mechanical and fails the build on any banned (transitive)
//     import. The guard is RAW TEXT over every .go file of the
//     package (tests included) — so a comment that merely QUOTES a
//     banned path fails it. Refer to them as "the banned subtrees".
//
//     The real import closure is wider than the four packages the
//     original doc listed: the configuration, the store, the group
//     engine, the two health layers, the tool-repair, the credential
//     engine and the crypto packages are all in it (the crypto one
//     through the credential engine). The doc names the RULE, not an
//     exhaustive list, so it cannot go stale again.
//
//   - The upstream attempt contract: a SINGLE-MODEL decision request
//     performs one upstream attempt (no retry, no race coordinator —
//     decisions are cheap+fast, sync, and not supervised). A GROUP
//     request performs a member loop instead: the per-member
//     credential attempts (the failure-class policy mirrors the text
//     lane: rotation-eligible classes rotate within the member, a 5xx
//     advances, the client-attributable classes fail fast) and the
//     group-level member fallback, with exactly ONE client write — at
//     the winner. There is still no speculative/hedge race engine.
//     The handler is shaped off the banned subtrees' skeleton.
//
//   - Single classifier seam (Outcome{RelayStatus, Reason,
//     UpstreamStatus, ErrorType, ErrorCode, ErrorMessage}) —
//     InspectResponse is called for EVERY upstream response
//     (no pre-filter on resp.StatusCode). Interface lives at
//     converter.go with the unified 2-arg typed signature
//     (finding 5): InspectResponse(respBytes, originalReq). The
//     handler stamps the raw upstream status onto the Outcome (the
//     adapter seam never sees it) so the lane can classify the
//     failure.
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
