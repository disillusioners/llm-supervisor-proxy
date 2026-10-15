package decision

import (
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// Decision Models commission / §3.2 / §4.2 / amendment 3
// CRITICAL — the per-provider seam at the heart of the handler.
// The handler never branches on resp.StatusCode directly; both
// providers satisfy the unified interface here.
//
// Outcome is the single classification result returned by
// UpstreamProvider.InspectResponse:
//
//   - RelayStatus == 0 ⇒ PassAsIs. The handler relays the
//     upstream status + body verbatim. Covers BOTH upstream
//     HTTP ≥400 passthrough AND 200-success.
//
//   - RelayStatus != 0 ⇒ PromoteToError. The handler writes
//     RelayStatus as the HTTP status and writes the upstream
//     body preserved verbatim (imggen-style PromoteToError
//     semantics; the openai-decision + typesafe adapters use
//     non-zero RelayStatus for malformed/missing-Errors-key
//     upstream responses).
//
//   - Reason is a free-form log line; always set on non-zero
//     RelayStatus, "" on PassAsIs.
type Outcome struct {
	RelayStatus int
	Reason      string
}

// UpstreamProvider is the per-decision-provider seam (Decision
// Models / §3.2 / §4.2 / finding 5). Both adapters —
// openaidecision (byte-faithful OpenAI Decision pass-through)
// and typesafe (typed struct → System One translation) —
// satisfy this single interface. The local registry is keyed
// on the lowercase credential provider string ("openai" /
// "typesafe" at P2; entries registered at init() time).
//
// InspectResponse uses the UNIFIED 2-arg typed signature
// (finding 5 / §3.2 step 14): respBytes + the typed
// *DecisionRequest that was captured at BuildUpstream time.
// OpenAI captures the request but doesn't strictly need it;
// typesafe uses it for ordered-answer synthesis. Both
// adapters satisfy one shape — the handler does not branch
// on provider kind to call them.
//
// BuildUpstream is the typed 2-arg signature (finding 3 / Q18
// + §4.2): the resolved credential + the typed
// *DecisionRequest + the captured raw request bytes. The
// openai-decision adapter is byte-faithful — it re-emits the
// raw bytes with only `model` rewritten; the typed struct is
// used only for the model-name token lookup. The typesafe
// adapter ignores the raw bytes and does a clean
// struct→JSON translation.
//
// ExtractUsage returns the typed *Usage struct that the
// success-only meter reads (P2.4). Both providers use the
// same typed Usage — the discriminated detail fields
// (CachedTokens / CacheWriteTokens / ReasoningTokens) are
// zero-filled on the typesafe path per A25.
type UpstreamProvider interface {
	// BuildUpstream constructs the upstream URL + body from
	// the resolved credential + the typed *DecisionRequest +
	// the original request bytes. Returns
	// (upstreamURL, upstreamBody, err); a non-nil err is the
	// typed-validation rejection from BuildUpstream re-checks
	// (typesafe) OR a JSON-encode failure (impossible on the
	// openai path which does no Marshal). The handler maps a
	// non-nil err to 502 + house OpenAI envelope code
	// `decision_upstream_build_failed` (Q15).
	BuildUpstream(cred models.ResolvedCredential, req *DecisionRequest, rawBody []byte) (upstreamURL string, upstreamBody []byte, err error)

	// InspectResponse classifies the upstream response into
	// a typed DecisionResponse AND a relay Outcome. Called
	// for EVERY upstream response (no pre-filter on
	// resp.StatusCode); the Outcome carries the relay
	// decision.
	//
	// Unified 2-arg signature per finding 5 / §3.2 step 14:
	// respBytes + the typed *DecisionRequest captured at
	// BuildUpstream time. On a successful relay (RelayStatus
	// == 0) the returned *DecisionResponse is the typed
	// value the handler relays to the client; on a promoted
	// error the handler relays the respBytes verbatim — the
	// typed value may be nil.
	InspectResponse(respBytes []byte, originalReq *DecisionRequest) (*DecisionResponse, *Outcome, error)

	// ExtractUsage returns the typed *Usage for the
	// success-only meter (P2.4). Called ONLY when
	// InspectResponse returned Outcome.RelayStatus == 0
	// (the response is the typed DecisionResponse shape);
	// the handler reads input_tokens + output_tokens from
	// the returned struct.
	ExtractUsage(resp *DecisionResponse) Usage
}
