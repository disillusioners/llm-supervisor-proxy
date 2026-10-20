// Decision-model TEST button — the operator-facing single-shot
// decision probe (decision commission follow-up).
//
// This file owns ONE new exported entry point (ExecuteTest) plus
// its result carrier (TestResult). Everything else stays
// package-private.
//
// Why the call lives HERE and not in pkg/ui: the decision upstream
// call carries a pile of easy-to-regress discipline that the
// production handler (pkg/decision/handler.go) already owns —
//
//   - exactly ONE Content-Type on the upstream request (the
//     duplicate-CT bug class; fixed at 9b71e63 — OpenAI's
//     /v1/decisions rejects a duplicated CT with "Invalid decision
//     request JSON"),
//   - the client Accept-Encoding is NOT copied (Go's transport
//     only transparently decompresses a gzip response when IT
//     added Accept-Encoding itself — a copied header disables
//     auto-decompression and a gzipped body hits the JSON parser),
//   - the response body cap (64 MB),
//   - the per-request timeout ctx (owned by the caller, NOT
//     r.Context()),
//   - the adapter seam (BuildUpstream → InspectResponse) and the
//     Outcome{RelayStatus, Reason} classification.
//
// Re-implementing that stack inside pkg/ui would fork the bug
// surface — the exact failure class the decision commission
// already paid for once. So the UI route resolves the model +
// credential and hands the resolved credential + the canned body
// to ExecuteTest, which runs the SAME steps the handler runs.
//
// ExecuteTest is NOT a handler: it does NOT authenticate, does NOT
// meter, does NOT publish events, and does NOT write an HTTP
// response. It returns the classified outcome; the caller decides
// how to present it.
package decision

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestResult is the classified outcome of one decision test
// execution (ExecuteTest).
//
// Three status-ish fields coexist on purpose:
//
//   - Status is the operator-facing classification status. It is
//     the UPSTREAM HTTP status whenever the upstream answered with
//     an error status (402 / 429 / 401 / 422 ... — the classes the
//     FE renders), the adapter's RelayStatus when the upstream
//     answered 2xx with an unusable body, and a local status
//     (400 / 502 / 504) for the pre-upstream failure modes.
//
//   - RelayStatus is the adapter's Outcome.RelayStatus VERBATIM
//     (0 ⇒ PassAsIs / success). Both shipped adapters promote
//     EVERY error envelope to 502 (openaidecision.go:171 /
//     typesafe.go:405) — that is the production relay contract
//     and this field preserves it unchanged.
//
//   - UpstreamStatus is the raw upstream HTTP status (0 when the
//     call never produced a response).
type TestResult struct {
	// Status is the classification status (see the type doc).
	Status int
	// RelayStatus is the adapter Outcome.RelayStatus verbatim
	// (0 on success).
	RelayStatus int
	// Reason is the adapter Outcome.Reason verbatim ("" on
	// success).
	Reason string
	// UpstreamStatus is the raw upstream HTTP status (0 when the
	// upstream never answered).
	UpstreamStatus int
	// Response is the typed decision response on success (nil on
	// every failure path).
	Response *DecisionResponse
	// Body is the upstream body VERBATIM (empty when the upstream
	// never answered). The caller extracts the human-readable
	// message from it — the body is never dumped raw to the FE.
	Body []byte
	// Error is a short human-readable failure message ("" on
	// success).
	Error string
}

// ExecuteTest runs ONE decision request against the upstream and
// returns the classified outcome.
//
// It mirrors the essentials of the /v1/decisions handler
// (pkg/decision/handler.go steps 9-15):
//
//  1. adapter lookup on the credential's provider (registry.go),
//  2. the typesafe pre-flight edge check (handler step 9.5),
//  3. adapter.BuildUpstream (the byte-faithful openai rewrite /
//     the typesafe System One translation),
//  4. a single upstream call under the caller-supplied timeout,
//  5. adapter.InspectResponse → typed response + Outcome.
//
// The credential is supplied by the caller (the UI route resolves
// it), so the credential-selection policy stays with the caller.
// The timeout is supplied by the caller too (the UI route reuses
// the configured DecisionTimeout — the same knob the handler
// uses); a non-positive timeout falls back to the package default
// (30s).
func ExecuteTest(cred models.ResolvedCredential, req *DecisionRequest, rawBody []byte, timeout time.Duration) *TestResult {
	res := &TestResult{}

	// Step 1 — adapter lookup on the local registry (the decision
	// path deliberately does NOT go through providers.NewProvider —
	// "typesafe" has no chat provider).
	provider, ok := lookup(cred.Provider)
	if !ok {
		res.Status = http.StatusBadGateway
		res.Error = fmt.Sprintf("no decision adapter registered for provider %q", cred.Provider)
		return res
	}

	// Step 2 — typesafe pre-flight edge check (handler step 9.5).
	// A body carrying safety_identifier is rejected with 400 here
	// (never reaches the upstream).
	if cred.Provider == TypesafeProviderName {
		if msg, failed := typesafePreflightValidate(req); failed {
			res.Status = http.StatusBadRequest
			res.Error = msg
			return res
		}
	}

	// Step 3 — BuildUpstream (adapter-owned translation).
	upstreamURL, upstreamBody, err := provider.BuildUpstream(cred, req, rawBody)
	if err != nil {
		res.Status = http.StatusBadGateway
		res.Error = "failed to construct upstream request: " + err.Error()
		return res
	}

	// Step 4 — timeout ctx. Owned here (never r.Context()) so the
	// clamp binds only this call (handler step 11 rationale).
	if timeout <= 0 {
		timeout = defaultDecisionTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(upstreamBody))
	if err != nil {
		res.Status = http.StatusInternalServerError
		res.Error = "failed to build upstream HTTP request"
		return res
	}
	// EXACTLY ONE Content-Type: Set (not Add) on a fresh request —
	// the duplicate-CT bug class (9b71e63) must not come back.
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Authorization", "Bearer "+cred.APIKey)
	// NO client headers are forwarded: the test probe is
	// server-generated, and copying a client Accept-Encoding would
	// disable the transport's transparent gzip decompression (the
	// handler's rationale).

	// Single upstream call (no retry / fallback / race — decisions
	// are sync + token-billed).
	resp, err := sharedHTTPClient.Do(upstreamReq)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
			status = http.StatusGatewayTimeout
		}
		res.Status = status
		res.Error = fmt.Sprintf("upstream request failed: %v", err)
		return res
	}
	defer resp.Body.Close()

	// Response body bounded (handler step 13 mirror).
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		res.Status = http.StatusBadGateway
		res.UpstreamStatus = resp.StatusCode
		res.Error = "upstream read failed"
		return res
	}
	if len(respBody) > maxResponseBodyBytes {
		res.Status = http.StatusBadGateway
		res.UpstreamStatus = resp.StatusCode
		res.Error = fmt.Sprintf("upstream response exceeds %d MB cap", maxResponseBodyBytes>>20)
		return res
	}

	res.UpstreamStatus = resp.StatusCode
	res.Body = respBody

	// Step 5 — InspectResponse (the adapter seam).
	typedResp, outcome, err := provider.InspectResponse(respBody, req)
	if err != nil {
		res.Status = http.StatusBadGateway
		res.Error = "inspect error: " + err.Error()
		return res
	}
	if outcome != nil {
		res.RelayStatus = outcome.RelayStatus
		res.Reason = outcome.Reason
	}

	// Classification. The upstream's OWN error status is the
	// operator-facing signal (402 insufficient funds / 429 rate
	// limited / 401 auth / 422 endpoint unavailable) — the
	// adapters' promoted RelayStatus (502) would flatten every
	// one of them into the same class. RelayStatus is preserved
	// verbatim alongside it.
	switch {
	case res.UpstreamStatus >= 400:
		res.Status = res.UpstreamStatus
		if res.Error == "" {
			res.Error = res.Reason
		}
	case outcome != nil && outcome.RelayStatus != 0:
		res.Status = outcome.RelayStatus
		res.Error = outcome.Reason
	default:
		res.Status = http.StatusOK
		res.Response = typedResp
	}
	return res
}
