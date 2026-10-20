package imggen

import (
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// Outcome is the single classification result returned by
// UpstreamProvider.InspectResponse (Architect Amendment 3,
// CRITICAL — the handler never branches on resp.StatusCode
// directly).
//
//   - RelayStatus == 0 ⇒ PassAsIs. The handler relays the upstream
//     status + body verbatim. This covers BOTH upstream HTTP ≥400
//     passthrough AND 200-success. There is no separate
//     resp.StatusCode branch in the handler — that second
//     classification path was folded into the seam.
//
//   - RelayStatus != 0 ⇒ PromoteToError. The handler writes
//     RelayStatus as the HTTP status and writes the upstream body
//     preserved verbatim. MiniMax's adapter uses 502 for
//     non-zero / missing / invalid base_resp.status_code.
//
//   - Reason is a free-form log line ("missing base_resp key",
//     "unparseable JSON", "empty body", "base_resp.status_code=2013:
//     invalid params", etc.). Always set on non-zero RelayStatus;
//     "" on PassAsIs. Amendment 11: the log line MUST distinguish
//     three failure modes (missing-key / unparseable-JSON /
//     empty-body) so the API-drift signal this guard catches is
//     not lost in a conflated message.
//
//   - Code is a STABLE machine-readable slug for the classified
//     outcome (see the Code* constants below). It exists so the
//     operator-facing surfaces (the UI test button's DTO + the FE
//     label overlay) can branch on a machine token instead of
//     parsing the free-form Reason string. The numeric upstream
//     business code stays in Reason ("base_resp.status_code=2013")
//     — Code is a slug, never a number.
//
//   - Message is the upstream's own human-readable business message
//     (MiniMax: base_resp.status_msg). It is kept SEPARATELY from
//     Reason so the seam can compose the operator-facing message
//     without string-parsing Reason (composeTestError in
//     test_exec.go). NO message field crosses the wire: the FE-facing
//     `error` is composed inside the seam, and the FE DTO carries no
//     message member at all.
type Outcome struct {
	RelayStatus int
	Reason      string
	// Code is the stable machine-readable slug ("" = not set).
	Code string
	// Message is the upstream's own business message ("" = none).
	Message string
}

// Outcome code slugs — the machine-readable vocabulary the CLASSIFIER
// emits (pkg/imggen/test_exec.go). The operator-facing FE overlay
// (pkg/ui/frontend/src/components/config/probeErrorClasses.ts) labels
// the slugs it knows.
//
// NOTE: these are CLASSIFIER slugs only. Route-level client rejections
// (the kind gate / no credential / no API key in
// pkg/ui/handlers_imggen.go) are bare {"error": "..."} envelopes —
// they never reach a code.
//
// The slugs are deliberately generic enough to survive upstream API
// drift: they describe the CLASS of failure, never a provider's
// internal numbering. The upstream's own numeric code (MiniMax's
// base_resp.status_code) travels in Outcome.Reason, never here.
const (
	// CodeOK is an upstream that ACCEPTED the generation
	// (base_resp.status_code == 0). The probe layer downgrades it to
	// CodeEmptyImage when the body carries no image URL.
	CodeOK = "ok"
	// CodeEmptyImage is a 200 with base_resp.status_code == 0 but
	// NO image URLs — the upstream accepted the request and produced
	// nothing.
	CodeEmptyImage = "empty_image"
	// CodeUpstreamBusinessError is a business failure reported INSIDE
	// an HTTP 200 (MiniMax base_resp.status_code != 0). This is the
	// green-lie class: the HTTP status alone says "success".
	CodeUpstreamBusinessError = "upstream_business_error"
	// CodeUpstreamMalformed is an upstream body the adapter could
	// not classify (empty body / unparseable JSON / missing
	// base_resp / missing base_resp.status_code).
	CodeUpstreamMalformed = "upstream_malformed"
	// CodeUpstreamHTTPError is an upstream HTTP error status
	// (401 / 402 / 403 / 404 / 422 / 5xx ...). The status itself
	// carries the operator-facing class, so no FE code label is
	// registered for this slug (the status label wins).
	CodeUpstreamHTTPError = "upstream_http_error"
	// CodeUpstreamTransportError is a transport-level failure (the
	// upstream was unreachable / TLS failed / connection reset).
	CodeUpstreamTransportError = "upstream_transport_error"
	// CodeUpstreamReadFailed is a failure while reading the upstream
	// response body.
	CodeUpstreamReadFailed = "upstream_read_failed"
	// CodeTimeout is the probe deadline exceeded.
	CodeTimeout = "timeout"
	// CodeResponseTooLarge is an upstream body over the 64 MB cap.
	CodeResponseTooLarge = "response_too_large"
	// CodeNoAdapter is a credential provider with no registered
	// image-generation adapter (no upstream call was made).
	CodeNoAdapter = "no_adapter"
	// CodePreflightRejected is an adapter that refused to build the
	// upstream request.
	CodePreflightRejected = "preflight_rejected"
	// CodeCooldown is an upstream rate-limit answer (HTTP 429).
	CodeCooldown = "cooldown"
	// CodeBusy is the test-probe single-flight rejection: another
	// probe is already in flight (the probe is never queued).
	CodeBusy = "busy"
)

// UpstreamProvider is the per-image-provider seam (BE-R2 / T1.4.1).
// The interface expresses exactly the three per-provider variables
// observed from the MiniMax integration; a future OpenAI / Stability
// / Gemini adapter drops in without touching the handler.
//
// The registry is keyed by the lowercase credential provider string
// (e.g., "minimax"). The MiniMax identity implementation is the
// only registration in Phase 1 (binding decision #2 — no
// speculative converters).
type UpstreamProvider interface {
	// BuildUpstream constructs the upstream URL + body from the
	// resolved credential and the (already JSON-decoded) client
	// body. MiniMax is an identity provider at the body level
	// (rewrites only the `model` field to cred.InternalModel) and
	// appends "/image_generation" to the credential's baseURL.
	BuildUpstream(cred models.ResolvedCredential, clientBody []byte) (upstreamURL string, upstreamBody []byte, err error)

	// InspectResponse classifies the upstream response into a
	// relay decision. Called for EVERY upstream response (no
	// pre-filter on resp.StatusCode). MiniMax inspects base_resp
	// (status_code, success_count, failed_count) and coerces
	// status_code from number or string.
	InspectResponse(upstreamStatus int, body []byte) Outcome

	// BillableImages returns the count of billable images in the
	// upstream response. The single-point of coercion for
	// metadata.success_count (string→int, default 0 on absent /
	// missing). Called ONLY on a successful relay (status_code==0);
	// the handler bills 0 on error-in-200 / 5xx.
	BillableImages(body []byte) (int, error)
}
