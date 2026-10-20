// Image-generation TEST button — the operator-facing single-shot
// image-gen probe (image-gen test-button commission).
//
// This file owns ONE new exported entry point (ExecuteTest) plus its
// result carrier (TestResult). Everything else stays
// package-private.
//
// Why the call lives HERE and not in pkg/ui: the image-gen upstream
// call carries a pile of easy-to-regress discipline that the
// production handler (pkg/imggen/handler.go) already owns —
//
//   - exactly ONE Content-Type on the upstream request (the
//     duplicate-CT bug class): the probe builds a FRESH request and
//     uses Header.Set (never Header.Add), and it copies NO client
//     header at all — a client-supplied Content-Type can therefore
//     never reach the upstream,
//   - the client Accept-Encoding is NOT copied (Go's transport only
//     transparently decompresses a gzip response when IT added
//     Accept-Encoding itself — a copied header disables
//     auto-decompression and a gzipped body hits the JSON parser),
//   - the response body cap (64 MB),
//   - the per-request timeout ctx (owned by the caller, NOT
//     r.Context()),
//   - the adapter seam (BuildUpstream → InspectResponse →
//     BillableImages) and the Outcome classification.
//
// Re-implementing that stack inside pkg/ui would fork the bug
// surface — the same failure class the decision commission already
// paid for once (pkg/decision/test_exec.go exists for exactly that
// reason). So the UI route resolves the model + credential and hands
// the resolved credential + the canned body to ExecuteTest, which
// runs the SAME steps the handler runs.
//
// THE CLASSIFIER IS STATUS-FIRST, LIKE THE DECISION LANE. An upstream
// status >= 400 keeps that status as the operator class (401 auth /
// 402 funds / 429 rate limit are the most actionable classes). It
// differs from the decision lane in TWO places: the
// outcome.Code == CodeUpstreamBusinessError branch (a MiniMax
// BUSINESS failure reported INSIDE an HTTP 200 — base_resp.
// status_code != 0, the live 2013 fixture — is a FAILURE, never a
// success) and the SUCCESS GATE (a success needs the adapter's
// pass-through verdict AND at least one image URL, so an HTTP 200
// alone never becomes a success — the green lie).
//
// ExecuteTest is NOT a handler: it does NOT authenticate, does NOT
// meter, does NOT publish events, and does NOT write an HTTP
// response. It returns the classified outcome; the caller decides
// how to present it.
package imggen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestResult is the classified outcome of one image-generation test
// execution (ExecuteTest).
//
// Three status-ish fields coexist on purpose:
//
//   - Status is the operator-facing classification status. It is
//     the UPSTREAM HTTP status whenever the upstream answered with an
//     error status (401 / 402 / 429 / 5xx ... — the classes the FE
//     renders), the adapter's RelayStatus when the upstream answered
//     2xx with an unusable body (502), and a local status (400 /
//     500 / 502 / 504) for the pre-upstream failure modes.
//
//   - RelayStatus is the adapter's Outcome.RelayStatus VERBATIM
//     (0 ⇒ PassAsIs). The MiniMax adapter promotes every business
//     failure to 502 — that is the production relay contract and
//     this field preserves it unchanged.
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
	// success). For a MiniMax business failure it carries the
	// upstream's own numeric code ("base_resp.status_code=2013").
	Reason string
	// Code is the stable machine-readable slug of the class
	// (CodeOK / CodeUpstreamBusinessError / ...). "" is never
	// returned on a classified outcome.
	Code string
	// UpstreamStatus is the raw upstream HTTP status (0 when the
	// upstream never answered).
	UpstreamStatus int
	// Body is the upstream body VERBATIM (empty when the upstream
	// never answered). It is retained for DIAGNOSTICS ONLY: the
	// image-gen caller never reads it, the FE-facing message is
	// composed by the seam from Outcome.Message / Outcome.Reason,
	// and the raw body is NEVER relayed to the FE.
	Body []byte
	// Error is a short human-readable failure message ("" on
	// success).
	Error string
	// ImageURLs are the upstream's image URLs, extracted from the
	// response body (the FE renders them directly — no second
	// proxy hop, no base64).
	ImageURLs []string
	// ImageCount is len(ImageURLs).
	ImageCount int
	// SuccessCount is the upstream's metadata.success_count (the
	// billable image count).
	SuccessCount int
	// FailedCount is the upstream's metadata.failed_count.
	FailedCount int
}

// ExecuteTest runs ONE image-generation request against the upstream
// and returns the classified outcome.
//
// It mirrors the essentials of the /v1/image_generation handler
// (pkg/imggen/handler.go — the numbered phases 6) through 12)):
//
//  1. adapter lookup on the credential's provider (the private
//     registry — the image-gen path deliberately does NOT go through
//     providers.NewProvider: a chat provider would post to
//     /chat/completions and fail quietly in the wrong place),
//  2. provider.BuildUpstream (the `model` → InternalModel rewrite +
//     the /image_generation URL suffix),
//  3. a single upstream call under the caller-supplied timeout,
//  4. the 64 MB response cap,
//  5. provider.InspectResponse → Outcome (the classifier seam),
//  6. provider.BillableImages + the image-URL extraction.
//
// The credential is supplied by the caller (the UI route resolves
// it), so the credential-selection policy stays with the caller. The
// timeout is supplied by the caller too (the UI route reuses the
// configured ImageGenTimeout — the same knob the handler uses); a
// non-positive timeout falls back to the package default (120s).
func ExecuteTest(cred models.ResolvedCredential, rawBody []byte, timeout time.Duration) *TestResult {
	res := &TestResult{}

	// Step 1 — adapter lookup on the local registry.
	provider, ok := lookup(cred.Provider)
	if !ok {
		res.Status = http.StatusBadGateway
		res.Code = CodeNoAdapter
		res.Error = fmt.Sprintf("no image-generation adapter registered for provider %q", cred.Provider)
		return res
	}

	// Step 2 — BuildUpstream (adapter-owned translation).
	upstreamURL, upstreamBody, err := provider.BuildUpstream(cred, rawBody)
	if err != nil {
		res.Status = http.StatusBadRequest
		res.Code = CodePreflightRejected
		res.Error = "failed to construct upstream request: " + err.Error()
		return res
	}

	// Step 3 — timeout ctx. Owned here (never r.Context()) so the
	// clamp binds only this call and a client disconnect cannot kill
	// a probe that is already billing the upstream.
	if timeout <= 0 {
		timeout = defaultImageGenTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(upstreamBody))
	if err != nil {
		res.Status = http.StatusInternalServerError
		res.Code = CodePreflightRejected
		res.Error = "failed to build upstream HTTP request"
		return res
	}
	// EXACTLY ONE Content-Type: Set (not Add) on a fresh request —
	// the duplicate-CT bug class must not come back.
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Authorization", "Bearer "+cred.APIKey)
	// NO client headers are forwarded — the probe body is
	// server-generated, and copying a client Accept-Encoding would
	// disable the transport's transparent gzip decompression.

	// Single upstream call (no retry / fallback / race — image
	// generations are slow and billable).
	resp, err := sharedHTTPClient.Do(upstreamReq)
	if err != nil {
		status := http.StatusBadGateway
		code := CodeUpstreamTransportError
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
			status = http.StatusGatewayTimeout
			code = CodeTimeout
		}
		res.Status = status
		res.Code = code
		res.Error = fmt.Sprintf("upstream request failed: %v", err)
		return res
	}
	defer resp.Body.Close()

	// Step 4 — response body bounded (the handler's 64 MB cap, with
	// the same test-only override hook).
	cap := int64(maxResponseBodyBytes)
	if maxResponseBodyBytesForTest > 0 {
		cap = maxResponseBodyBytesForTest
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, cap+1))
	if err != nil {
		res.Status = http.StatusBadGateway
		res.Code = CodeUpstreamReadFailed
		res.UpstreamStatus = resp.StatusCode
		res.Error = "upstream read failed"
		return res
	}
	if int64(len(respBody)) > cap {
		res.Status = http.StatusBadGateway
		res.Code = CodeResponseTooLarge
		res.UpstreamStatus = resp.StatusCode
		res.Error = fmt.Sprintf("upstream response exceeds %d MB cap", cap>>20)
		return res
	}

	res.UpstreamStatus = resp.StatusCode
	res.Body = respBody

	// Step 5 — InspectResponse (the adapter seam).
	outcome := provider.InspectResponse(resp.StatusCode, respBody)
	res.RelayStatus = outcome.RelayStatus
	res.Reason = outcome.Reason
	if outcome.Code != "" {
		res.Code = outcome.Code
	}

	// Step 6 — CLASSIFICATION.
	//
	// The ORDER is the whole point (the green-lie guard):
	//
	//  1. an upstream HTTP error status wins the STATUS (401 auth /
	//     402 funds / 429 rate limit are the most actionable classes
	//     an operator can act on), but it NEVER turns a business
	//     failure into a success;
	//  2. a MiniMax BUSINESS failure (base_resp.status_code != 0)
	//     reported INSIDE an HTTP 200 is a failure — status 502, the
	//     upstream_business_error code;
	//  3. an adapter failure on a 2xx body (unclassifiable body) is
	//     a failure — status 502, the upstream_malformed code;
	//  4. only a 2xx answer the adapter accepted is a success — and
	//     it still has to carry at least one image URL.
	switch {
	case res.UpstreamStatus >= 400:
		res.Status = res.UpstreamStatus
		if res.UpstreamStatus == http.StatusTooManyRequests {
			res.Code = CodeCooldown
		} else {
			res.Code = CodeUpstreamHTTPError
		}
		res.Error = composeTestError(outcome)
	case outcome.Code == CodeUpstreamBusinessError:
		// The upstream answered 2xx and refused the generation.
		res.Status = outcome.RelayStatus
		if res.Status == 0 {
			res.Status = http.StatusBadGateway
		}
		res.Code = CodeUpstreamBusinessError
		res.Error = composeTestError(outcome)
	case outcome.RelayStatus != 0:
		// The adapter could not classify the body (empty /
		// unparseable / missing base_resp — the API-drift guard).
		res.Status = outcome.RelayStatus
		if res.Code == "" {
			res.Code = CodeUpstreamMalformed
		}
		res.Error = composeTestError(outcome)
	default:
		// Success path — but ONLY if the body actually carries an
		// image. A 2xx with zero images is a failure the operator
		// must see (empty_image), not a green checkmark.
		res.Status = http.StatusOK
		res.Code = CodeOK
		res.ImageURLs = extractImageURLs(respBody)
		res.ImageCount = len(res.ImageURLs)
		if n, err := provider.BillableImages(respBody); err == nil {
			res.SuccessCount = n
		} else {
			log.Printf("[imggen-test] BillableImages failed: %v (credential=%s)", err, cred.CredentialID)
		}
		res.FailedCount = failedCountFromBody(respBody)
		if res.ImageCount == 0 {
			res.Code = CodeEmptyImage
			res.Error = "the upstream accepted the request but returned no image URL"
		}
	}
	return res
}

// composeTestError builds the short human-readable failure message
// for the UI. Precedence: the upstream's own business message → the
// adapter's classification reason. For a business failure the
// reason is APPENDED (it carries the upstream's numeric code) so the
// operator sees "invalid parameters (base_resp.status_code=2013)".
//
// It never returns the raw upstream body.
func composeTestError(outcome Outcome) string {
	msg := strings.TrimSpace(outcome.Message)
	reason := strings.TrimSpace(outcome.Reason)
	switch {
	case msg != "" && reason != "" && outcome.Code == CodeUpstreamBusinessError:
		return msg + " (" + reason + ")"
	case msg != "":
		return msg
	default:
		return reason
	}
}

// extractImageURLs pulls the image URLs out of a MiniMax response
// body.
//
// TWO data shapes are observed in the wild and BOTH are tolerated
// (the fixtures disagree — pkg/imggen/handler_test.go uses an
// array-of-objects, pkg/imggen/live_e2e_test.go uses an object):
//
//	"data": [{"image_urls": ["https://…"]}]      (fixture shape)
//	"data": {"image_urls": ["https://…"]}        (live shape)
//
// The URLs are upstream-generated CDN locators, not an echo of the
// request, so they are safe to relay verbatim to the FE.
func extractImageURLs(body []byte) []string {
	if len(body) == 0 {
		return nil
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	data, ok := resp["data"]
	if !ok || data == nil {
		return nil
	}
	var out []string
	switch shape := data.(type) {
	case []interface{}:
		for _, item := range shape {
			out = append(out, imageURLsFromItem(item)...)
		}
	case map[string]interface{}:
		out = append(out, imageURLsFromItem(shape)...)
	}
	return out
}

// imageURLsFromItem extracts the image_urls of ONE data element.
// The element is normally an object; a bare string element is
// tolerated (defensive — upstream API drift).
func imageURLsFromItem(item interface{}) []string {
	switch v := item.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		return []string{v}
	case map[string]interface{}:
		switch urls := v["image_urls"].(type) {
		case []interface{}:
			var out []string
			for _, u := range urls {
				if s, ok := u.(string); ok && strings.TrimSpace(s) != "" {
					out = append(out, s)
				}
			}
			return out
		case string:
			if strings.TrimSpace(urls) == "" {
				return nil
			}
			return []string{urls}
		}
	}
	return nil
}

// failedCountFromBody reads metadata.failed_count (the upstream's
// partial-success counter). It reuses the handler's own extractor —
// the live body carries the STRING form ("1"), the numeric form is
// tolerated too.
func failedCountFromBody(body []byte) int {
	n, ok := extractFailedCount(body)
	if !ok {
		return 0
	}
	return n
}
