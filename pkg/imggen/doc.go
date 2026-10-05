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
//     pkg/usage, pkg/events} ONLY; never pkg/proxy. The build-time
//     import guard (task 1.8.2) fails CI on any pkg/proxy import.
//   - One upstream attempt per request (no retry, no fallback, no
//     MonitoredReader idle wrap; the live 17-60s silent generation
//     would false-fire any idle/silence-based supervision).
//   - Single classifier seam: InspectResponse is called for EVERY
//     upstream response (Architect Amendment 3, CRITICAL). The
//     handler never branches on resp.StatusCode directly — the
//     Outcome returned by InspectResponse carries the relay-status
//     override.
//   - Bounded RAM: request MaxBytesReader 16 MB; response
//     io.LimitReader 64 MB (one read, classified + metered + event-
//     reuses the same []byte — Architect Amendment 12 single-
//     materialization audit).
package imggen
