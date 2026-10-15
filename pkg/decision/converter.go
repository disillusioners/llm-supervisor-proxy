package decision

// Outcome is the single classification result returned by
// UpstreamProvider.InspectResponse — mirror of imggen's Outcome
// (Decision Models commission / §3.2 / amendment 3, CRITICAL).
// The handler never branches on resp.StatusCode directly.
//
//   - RelayStatus == 0 ⇒ PassAsIs. The handler relays the upstream
//     status + body verbatim. Covers BOTH upstream HTTP ≥400
//     passthrough AND 200-success.
//
//   - RelayStatus != 0 ⇒ PromoteToError. The handler writes
//     RelayStatus as the HTTP status and writes the upstream body
//     preserved verbatim (Decision Models mirror of imggen
//     PromoteToError semantics).
//
//   - Reason is a free-form log line; always set on non-zero
//     RelayStatus, "" on PassAsIs.
type Outcome struct {
	RelayStatus int
	Reason      string
}

// UpstreamProvider is the per-decision-provider seam (Decision
// Models commission / §3.2 / R1.5). The interface expresses
// exactly the four per-provider variables observed at P2 design
// time (after P2 lands the openai-decision + typesafe adapters
// will satisfy it). The registry is keyed on the lowercase
// credential provider string ("openai" or "typesafe" at P2;
// empty at P1).
//
// InspectResponse uses the UNIFIED 2-arg signature per R1.5:
// originalReq is captured at BuildUpstream time so both
// openaidecision (which doesn't strictly need it but captures
// once cheaply) and typesafe (which needs it for
// answer-order preservation) satisfy the same interface.
//
// At P1 this interface is declared but only the registry is
// populated — provider lookup returns ok=false until P2
// registers entries.
type UpstreamProvider interface {
	// BuildUpstream constructs the upstream URL + body from the
	// resolved credential and the original client body. The
	// openai-decision adapter is a near-pure byte-faithful
	// pass-through (Q18 / R1.3): capture the raw request bytes,
	// rewrite only the `model` field, never re-marshal through
	// typed structs.
	BuildUpstream(cred interface{}, clientBody []byte, originalReq interface{}) (upstreamURL string, upstreamBody []byte, err error)

	// InspectResponse classifies the upstream response into a
	// relay decision. Called for EVERY upstream response (no
	// pre-filter on resp.StatusCode). The Outcome carries the
	// relay decision.
	//
	// Unified 2-arg signature per R1.5 — originalReq is captured
	// at BuildUpstream time so both adapters satisfy one shape.
	InspectResponse(upstreamStatus int, body []byte, originalReq interface{}) Outcome

	// ExtractUsage returns the typed usage block for the
	// success-only meter (P2.4). At P1 this is unimplemented;
	// P2 lands the typed struct per the plan.
	ExtractUsage(body []byte) (interface{}, error)
}
