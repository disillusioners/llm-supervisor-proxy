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
type Outcome struct {
	RelayStatus int
	Reason      string
}

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
