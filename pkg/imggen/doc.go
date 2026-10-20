// Package imggen is the image-generation endpoint for the
// supervisor proxy. It implements POST /v1/image_generation as a
// raw single-attempt pass-through to MiniMax (ImgGen Models
// commission / BE-R1). It is structurally a sibling of
// pkg/ultimatemodel/handler_external.go and lives outside
// pkg/proxy so the chat-supervision exclusion boundary is
// mechanically enforceable via the import audit at task 1.8.2.
//
// Architectural rules (binding on the package):
//
//   - Import direction: pkg/imggen → {pkg/models, pkg/auth,
//     pkg/usage, pkg/events} ONLY; never pkg/proxy, never
//     pkg/decision (the parent → child inversion: the decision
//     adapters were built FROM the image-gen template). The import
//     guard (task 1.8.2) is mechanical: a grep gate in the
//     `make test` target, mirrored by the in-package guards
//     TestPackage_ImportGuard_NoProxyImports +
//     TestPackage_ImportGuard_NoDecisionImports +
//     TestPackage_ImportGuard_TransitiveClosure, fails the build on
//     any pkg/proxy / pkg/decision import (direct OR transitive).
//
//   - One upstream attempt per request (no retry, no fallback, no
//     MonitoredReader idle wrap; the live 17-60s silent generation
//     would false-fire any idle/silence-based supervision).
//
//   - Single classifier seam: InspectResponse is called for EVERY
//     upstream response (Architect Amendment 3, CRITICAL). The
//     handler never branches on resp.StatusCode directly — the
//     Outcome returned by InspectResponse carries the relay-status
//     override.
//
//   - Bounded RAM: request MaxBytesReader 16 MB; response
//     io.LimitReader 64 MB (one read, classified + metered + event-
//     reuses the same []byte — Architect Amendment 12 single-
//     materialization audit).
//
//   - ExecuteTest (test_exec.go) is the operator-facing single-shot
//     PROBE seam used by the UI Test button
//     (POST /fe/api/image_generation/test). It runs the same
//     upstream-call discipline as the handler (ONE Content-Type, no
//     client headers, the 64 MB cap, a ctx-owned timeout) but is NOT
//     a handler: no auth, no metering, no events, no HTTP write.
//     Its classifier is STATUS-FIRST: an upstream status >= 400
//     keeps that status as the operator class (401 auth / 402 funds
//     / 429 rate limit are the most actionable classes), and a
//     MiniMax BUSINESS failure reported INSIDE an HTTP 200
//     (base_resp.status_code != 0) stays a failure (502). The green
//     lie is prevented by the SUCCESS GATE: a success needs the
//     adapter's pass-through verdict (Outcome.RelayStatus == 0 —
//     which the minimax adapter reports as Code == ok) AND at least
//     one image URL, so an HTTP 200 alone never becomes a success.
package imggen
