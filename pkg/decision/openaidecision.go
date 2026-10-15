package decision

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// Decision Models commission / §3.2 step 10 / §4.2 / Q18 /
// A26 — the openai-decision adapter.
//
// Verbatim byte-faithful pass-through per Q18 (committed per
// finding 3). Unknown future OpenAI Decision fields flow
// through to the upstream verbatim — no typed re-marshal,
// no silent field-drop, no DisallowUnknownFields redaction.
// The typed *DecisionRequest is used ONLY to look up the
// `model` field for the byte-replace; the upstream body is
// raw request bytes with only that one token swapped.
//
// Provider name (keyed in providerRegistry): "openai"
// (lowercase; matches credential.Provider — recommended
// identity B reuses the chat-side `openai` provider id).
const OpenAIDecisionProviderName = "openai"

// NewOpenAIDecisionProvider returns the openai-decision
// identity adapter. Registered at init time.
func NewOpenAIDecisionProvider() UpstreamProvider {
	return &openaiDecisionProvider{}
}

type openaiDecisionProvider struct{}

// BuildUpstream (Decision Models / §3.2 step 10 / §4.2 /
// Q18 / A26):
//
//   - upstreamURL = TrimSuffix(cred.BaseURL, "/") + "/v1/decisions"
//     when cred.BaseURL is set; otherwise the OpenAI default
//     https://api.openai.com/v1/decisions. The credential's
//     BaseURL is honored as-is per Q17 (operator may point at
//     Azure-fronted OpenAI proxy, etc.).
//
//   - upstreamBody = exact rawBody with only the `model`
//     field's JSON token replaced via bytes.Replace on the
//     `"oldModelName"` token. The replace is once-left-most
//     (n=1) — request bodies cannot legitimately contain two
//     `model` fields at the top level (duplicate keys are
//     undefined in JSON), so the first occurrence is always
//     the right one.
//
// No typed re-marshal (the typed struct DOES NOT touch the
// bytes other than the model-name lookup). Unknown future
// OpenAI Decision fields flow through unchanged.
func (p *openaiDecisionProvider) BuildUpstream(cred models.ResolvedCredential, req *DecisionRequest, rawBody []byte) (string, []byte, error) {
	if req == nil {
		return "", nil, DecisionErrorf("typed DecisionRequest is nil for BuildUpstream (openai)")
	}
	if req.Model == "" {
		// The handler step 4 typed parse is the same value
		// already enforced as non-empty by the
		// missing_model 400 path; this is defense-in-depth.
		return "", nil, DecisionErrorf("decision request has empty `model` field (openai BuildUpstream)")
	}

	// upstream URL — Q17 honor cred.BaseURL as-is.
	//
	// The chat provider stores cred.BaseURL with a trailing
	// "/v1" (factory.go:116) when operators use the OpenAI
	// chat convention; the openai-decision endpoint lives at
	// /v1/decisions UNDER that base. So we strip a single
	// "/v1" tail before re-appending "/v1/decisions" — this
	// handles three shapes identically:
	//
	//   - empty (default)        → https://api.openai.com/v1/decisions
	//   - "https://api.openai.com/v1"   (chat convention) → same
	//   - "https://api.openai.com/"    (trailing slash)    → same
	//   - "https://my-proxy.example.com/openai"            → /openai/v1/decisions
	//
	// The prior builder produced "https://api.openai.com/v1/v1/decisions"
	// in BOTH the empty and chat-convention cases (P2 review
	// finding 1, critical).
	upstreamBase := strings.TrimSpace(cred.BaseURL)
	if upstreamBase == "" {
		upstreamBase = "https://api.openai.com"
	}
	upstreamBase = strings.TrimSuffix(upstreamBase, "/v1")
	upstreamBase = strings.TrimSuffix(upstreamBase, "/")
	upstreamURL := upstreamBase + "/v1/decisions"

	// Byte-faithful model rewrite. We replace the literal
	// `<modelName>` JSON token (the bytes between two double
	// quotes) inside rawBody. n=1 keeps us anchored to the
	// first occurrence — duplicate top-level keys are
	// invalid JSON so a single Replace is sufficient.
	modelNameBytes, err := json.Marshal(req.Model)
	if err != nil {
		return "", nil, DecisionErrorf("failed to marshal model name %q: %v", req.Model, err)
	}
	targetModelBytes, err := json.Marshal(cred.InternalModel)
	if err != nil || cred.InternalModel == "" {
		return "", nil, DecisionErrorf("decision credential %q has no InternalModel — required for the openai-decision model-field rewrite", cred.CredentialID)
	}
	rewritten := bytes.Replace(rawBody, modelNameBytes, targetModelBytes, 1)
	if bytes.Equal(rewritten, rawBody) {
		// The original `model` token wasn't found in the raw
		// body. This is a typed-vs-bytes drift (e.g. the
		// bytes were mutated between the parse-once read and
		// this call) — surface as a build error so the
		// handler emits 502 decision_upstream_build_failed.
		return "", nil, DecisionErrorf("openai-decision: model token %q not found in raw request bytes (typed/bytes drift?)", req.Model)
	}

	return upstreamURL, rewritten, nil
}

// InspectResponse (Decision Models / §3.2 step 14 / §4.2):
//
// The OpenAI Decision wire uses the standard OpenAI error
// envelope shape `{"error":{"message","type","code",...}}`.
// On any HTTP status, the body is one of:
//
//   - 200 with the typed DecisionResponse shape ({model,
//     answers[], usage}) ⇒ PassAsIs; return the typed
//     *DecisionResponse for the relay.
//   - 200 with `error` envelope at the root ⇒ PromoteToError;
//     return a non-zero Outcome {RelayStatus: 502, Reason}
//     and the bytes verbatim (no envelope of our own —
//     the upstream's error shape is what the client
//     pattern-matches; research-web §Q1).
//   - non-200 with `error` envelope ⇒ PassAsIs (upstream's
//     own error shape with its own status; relay verbatim).
//     Using PassAsIs on the upstream's non-200 is the same
//     posture as imggen (miniMax errors carry their own
//     status; the handler relays `resp.StatusCode`).
func (p *openaiDecisionProvider) InspectResponse(respBytes []byte, originalReq *DecisionRequest) (*DecisionResponse, *Outcome, error) {
	if len(respBytes) == 0 {
		// Empty body is a fail-closed 502 (Amendment 11 from
		// imggen; the body-preservation-the-empty-body
		// case carries an empty payload but a distinct log
		// reason).
		return nil, &Outcome{RelayStatus: 502, Reason: "empty body"}, nil
	}

	// Quick discrimination: if the body parses as a typed
	// DecisionResponse, treat as success; else probe for an
	// error envelope.
	var resp DecisionResponse
	if err := json.Unmarshal(respBytes, &resp); err == nil && resp.Model != "" && resp.Answers != nil {
		// Typed success shape — return the parsed value.
		return &resp, &Outcome{RelayStatus: 0}, nil
	}

	// Discriminate the error-envelope shape. The OpenAI wire
	// error is `{"error":{"message":..., "type":...,
	// "code":...}}`. Promote to a 502 (imggen-style; the
	// upstream body is preserved verbatim by the handler).
	var errEnv struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBytes, &errEnv); err == nil && errEnv.Error.Message != "" {
		return nil, &Outcome{
			RelayStatus: 502,
			Reason:      fmt.Sprintf("openai-decision returned error envelope (code=%q, type=%q)", errEnv.Error.Code, errEnv.Error.Type),
		}, nil
	}

	// Neither a typed DecisionResponse nor an error
	// envelope. The upstream's response is unparseable against
	// the OpenAI Decision wire — log + 502 + body verbatim.
	return nil, &Outcome{RelayStatus: 502, Reason: "openai-decision body is neither a typed DecisionResponse nor an error envelope"}, nil
}

// ExtractUsage returns the typed *Usage from a typed
// DecisionResponse. The typed shape carries the input_tokens +
// output_tokens that both providers return (research-web §Q1
// verified). The OpenAI Decision wire includes the detail
// blocks (input_tokens_details.{cached_tokens,
// cache_write_tokens} and output_tokens_details.
// {reasoning_tokens}); the openai-decision adapter passes them
// through verbatim — no zero-fill needed on this path.
func (p *openaiDecisionProvider) ExtractUsage(resp *DecisionResponse) Usage {
	if resp == nil {
		return Usage{}
	}
	return resp.Usage
}
