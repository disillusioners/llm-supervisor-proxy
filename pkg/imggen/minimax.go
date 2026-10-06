package imggen

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// MiniMaxProvider is the Phase-1 identity adapter for
// /v1/image_generation (BE-R2 / T1.4.2). The body-level pass-
// through is intentional (B1): the MiniMax request and response
// shapes (prompt / aspect_ratio / n / subject_reference;
// data[].image_urls / metadata / base_resp) are forwarded
// byte-for-byte. The only mutation is the `model` field rewrite
// (client-facing name → InternalModel upstream alias).
//
// Provider name (keyed in the Registry): "minimax" (lowercase;
// matches the credential provider convention — see
// pkg/providers/factory.go:17 ProviderMiniMax).
const MiniMaxProviderName = "minimax"

// MiniMaxProvider returns the MiniMax identity UpstreamProvider.
// Registered in the package-level registry at init time.
func NewMiniMaxProvider() UpstreamProvider {
	return &minimaxProvider{}
}

type minimaxProvider struct{}

// BuildUpstream (BE-R5 / BE-R6 / T1.4.2):
//   - upstreamURL = TrimSuffix(cred.BaseURL, "/") + "/image_generation"
//     (the credential's baseURL already carries "/v1" — see
//     pkg/providers/factory.go:48 for the default MiniMax baseURL)
//   - upstreamBody = shallow-copy of the client body with the
//     "model" field rewritten to cred.InternalModel (the upstream
//     alias). All other body keys untouched — CN-only params
//     (style, aigc_watermark) and future MiniMax params flow
//     through without proxy changes.
//
// The body is JSON-encoded for the upstream POST. The handler
// reads the upstream response body from the same `[]byte` that
// classification + metering + event reuse (Amendment 12 single-
// materialization).
func (p *minimaxProvider) BuildUpstream(cred models.ResolvedCredential, clientBody []byte) (string, []byte, error) {
	// Shallow-decode the client body. We do NOT validate the
	// shape (the upstream does that; the proxy is a pass-through).
	// A parse failure here is an operator-visible 400 at the
	// handler; we surface it as a build error so the handler can
	// emit a clean OpenAI envelope.
	var body map[string]interface{}
	if err := json.Unmarshal(clientBody, &body); err != nil {
		return "", nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	if body == nil {
		body = make(map[string]interface{})
	}

	// Rewrite model → InternalModel (the upstream alias, e.g.
	// client "minimax-image-01" → upstream "image-01"). All other
	// body keys untouched.
	if cred.InternalModel != "" {
		body["model"] = cred.InternalModel
	}

	upstreamBody, err := json.Marshal(body)
	if err != nil {
		return "", nil, fmt.Errorf("failed to marshal upstream body: %w", err)
	}

	upstreamURL := strings.TrimSuffix(cred.BaseURL, "/") + "/image_generation"
	return upstreamURL, upstreamBody, nil
}

// InspectResponse is the MiniMax-specific classifier (BE-T3 /
// T1.4.2 / Amendment 3 single-classifier seam):
//   - status_code == 0 ⇒ success (any HTTP status, including
//     ≥400 — the upstream HTTP status is relayed verbatim).
//   - status_code != 0 ⇒ 502 with the upstream body preserved
//     verbatim (the body carries MiniMax's base_resp + status_msg
//     for client-side interpretation).
//   - missing / malformed / unparseable / empty body ⇒ 502 with
//     the upstream body preserved verbatim (the preserved body
//     is empty in the empty-body case; the log Reason distinguishes
//     the three cases per Amendment 11).
//   - failed_count > 0 with status_code == 0 ⇒ 200 success
//     (partial success is MiniMax-native; RelayStatus: 0).
//
// Coerces status_code from number OR string (live: the value is
// typically a JSON number; the string form is tolerated for
// future API drift).
func (p *minimaxProvider) InspectResponse(upstreamStatus int, body []byte) Outcome {
	// Empty body is a fail-closed 502 with a distinct log
	// (Amendment 11). Relays the empty body verbatim.
	if len(body) == 0 {
		return Outcome{RelayStatus: 502, Reason: "empty body"}
	}

	// Unparseable JSON is a fail-closed 502. The upstream body is
	// preserved verbatim so the operator / client can see the
	// upstream's actual bytes (HTML intermediary page, truncated
	// stream, etc.).
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return Outcome{RelayStatus: 502, Reason: "unparseable JSON"}
	}

	// Missing base_resp key is a fail-closed 502 (API-drift
	// signal — the field this guard exists to catch).
	baseResp, ok := resp["base_resp"].(map[string]interface{})
	if !ok {
		return Outcome{RelayStatus: 502, Reason: "missing base_resp key"}
	}

	// status_code == 0 is the ONLY success signal (BE-T3).
	// Tolerate number OR string (Amendment 4 from T4 — coerce
	// both forms; the string form has not been observed live but
	// is harmless to support for API-drift resilience).
	var statusCode int
	switch v := baseResp["status_code"].(type) {
	case float64:
		statusCode = int(v)
	case int:
		statusCode = v
	case int64:
		statusCode = int(v)
	case string:
		// Best-effort int parse; empty / non-numeric → fail-
		// closed 502. Per the live 2013 fixture, the field is a
		// number; the string form is defensive.
		var n int
		_, err := fmt.Sscanf(v, "%d", &n)
		if err != nil {
			return Outcome{RelayStatus: 502, Reason: fmt.Sprintf("unparseable base_resp.status_code %q", v)}
		}
		statusCode = n
	default:
		return Outcome{RelayStatus: 502, Reason: "missing base_resp.status_code"}
	}
	if statusCode != 0 {
		return Outcome{RelayStatus: 502, Reason: fmt.Sprintf("base_resp.status_code=%d", statusCode)}
	}

	// success (status_code == 0). failed_count > 0 is the partial-
	// success case (MiniMax-native) — RelayStatus stays 0; the
	// BillableImages call at the handler decides the billable
	// count from metadata.success_count.
	return Outcome{RelayStatus: 0, Reason: ""}
}

// BillableImages returns the count of billable images in the
// upstream response (BE-M3 / T1.4.2). Single point of coercion
// for metadata.success_count (string→int, live: "1"; default 0
// on absent / missing / data:null — the live 2013 error body
// has data:null and no metadata key).
//
// Called ONLY on a successful relay (status_code==0); the handler
// bills 0 on error-in-200 / 5xx.
func (p *minimaxProvider) BillableImages(body []byte) (int, error) {
	if len(body) == 0 {
		return 0, nil
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, nil
	}
	metadata, ok := resp["metadata"].(map[string]interface{})
	if !ok {
		return 0, nil
	}
	// Live: success_count is a JSON STRING ("1", not 1). Tolerate
	// both forms; treat unparseable strings as 0 (defensive —
	// the per-image billable truth is best-effort; an unparseable
	// count is logged but does not break the request).
	switch v := metadata["success_count"].(type) {
	case string:
		var n int
		_, err := fmt.Sscanf(v, "%d", &n)
		if err != nil {
			return 0, nil
		}
		return n, nil
	case float64:
		return int(v), nil
	case int:
		return v, nil
	case int64:
		return int(v), nil
	default:
		return 0, nil
	}
}

// registry is the per-provider lookup. MiniMax is the only
// registration in Phase 1 (binding decision #2 — no speculative
// converters). Future providers (OpenAI, Stability, Gemini, …)
// register here without touching the handler.
var registry = map[string]UpstreamProvider{}

// init registers the MiniMax identity provider. The map is
// init-only-written under Go's import-time single-thread invariant
// (no concurrent access until after main starts), so no mutex.
func init() {
	registry[MiniMaxProviderName] = NewMiniMaxProvider()
}

// lookup returns the registered UpstreamProvider for a
// lowercase credential provider name. Returns (nil, false) when
// the provider is not registered (the handler emits a 502 in
// that case — no silent fallback).
func lookup(providerName string) (UpstreamProvider, bool) {
	p, ok := registry[strings.ToLower(providerName)]
	return p, ok
}
