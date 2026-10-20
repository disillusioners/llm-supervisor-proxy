package imggen

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// =============================================================================
// ExecuteTest — the operator-facing single-shot image-generation probe.
//
// The suite pins the contracts the FE test button depends on:
//
//   - the GREEN-LIE GUARD: a MiniMax BUSINESS failure reported INSIDE
//     an HTTP 200 (base_resp.status_code != 0) is a FAILURE, never a
//     success. A decision-lane-style classifier (HTTP status first)
//     would report every MiniMax rejection as a success.
//   - the upstream's own error status (401 / 402 / 429 / 5xx) stays
//     the operator-facing classification status,
//   - a 200 with base_resp.status_code == 0 but NO image is the
//     empty_image class (not a green checkmark),
//   - the upstream request carries EXACTLY ONE Content-Type, the
//     credential's bearer token, NO client Accept-Encoding, and hits
//     the upstream EXACTLY ONCE.
//
// Same-package white-box (package imggen), stdlib-only — mirrors the
// house test style of pkg/imggen/handler_test.go (the fakeMiniMax
// fixture is reused from there).
// =============================================================================

// testExecCred builds a resolved credential for the MiniMax adapter.
func testExecCred(baseURL string) models.ResolvedCredential {
	return models.ResolvedCredential{
		Provider:      MiniMaxProviderName,
		APIKey:        "sk-imggen-exec-key",
		BaseURL:       baseURL,
		InternalModel: "image-01",
		CredentialID:  "cred-exec-1",
	}
}

// testExecBody builds the raw canned body of a probe.
func testExecBody(t *testing.T, modelID string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]interface{}{
		"model":           modelID,
		"prompt":          "a small red apple on a plain white background",
		"aspect_ratio":    "1:1",
		"n":               1,
		"response_format": "url",
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return raw
}

// TestExecuteTest_SuccessReturnsImageURLs — the happy path: a 200
// with base_resp.status_code == 0 relays the image URLs and the
// upstream's counts.
func TestExecuteTest_SuccessReturnsImageURLs(t *testing.T) {
	srv, fake := newFakeMiniMax(t, fakeScript{
		status: http.StatusOK,
		body:   successBody(1, 0),
	})

	res := ExecuteTest(testExecCred(srv.URL), testExecBody(t, "minimax-image-01"), 5*time.Second)

	if res.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (error=%q reason=%q)", res.Status, res.Error, res.Reason)
	}
	if res.Code != CodeOK {
		t.Errorf("code = %q, want %q", res.Code, CodeOK)
	}
	if res.RelayStatus != 0 {
		t.Errorf("relay_status = %d, want 0 (PassAsIs)", res.RelayStatus)
	}
	if res.UpstreamStatus != http.StatusOK {
		t.Errorf("upstream_status = %d, want 200", res.UpstreamStatus)
	}
	if res.ImageCount != 1 {
		t.Fatalf("image_count = %d, want 1", res.ImageCount)
	}
	if len(res.ImageURLs) != 1 || res.ImageURLs[0] != "https://x/a.png" {
		t.Errorf("image_urls = %v, want [https://x/a.png]", res.ImageURLs)
	}
	if res.SuccessCount != 1 {
		t.Errorf("success_count = %d, want 1 (metadata.success_count)", res.SuccessCount)
	}
	if res.FailedCount != 0 {
		t.Errorf("failed_count = %d, want 0", res.FailedCount)
	}
	if res.Error != "" {
		t.Errorf("error = %q, want empty on success", res.Error)
	}
	if got := fake.hitCount.Load(); got != 1 {
		t.Errorf("upstream hits = %d, want exactly 1", got)
	}
}

// TestExecuteTest_BusinessErrorInHTTP200IsNotSuccess — THE GREEN-LIE
// REGRESSION. MiniMax reports a business failure INSIDE an HTTP 200
// (base_resp.status_code = 2013). The probe must classify it as a
// FAILURE (502 + the business-error code) even though the upstream
// HTTP status says 200.
func TestExecuteTest_BusinessErrorInHTTP200IsNotSuccess(t *testing.T) {
	srv, fake := newFakeMiniMax(t, fakeScript{
		status: http.StatusOK,
		body:   errorIn200Body(2013, "invalid parameters"),
	})

	res := ExecuteTest(testExecCred(srv.URL), testExecBody(t, "minimax-image-01"), 5*time.Second)

	if res.Status == http.StatusOK {
		t.Fatalf("status = 200 — a business failure inside a 200 must NOT be a success (reason=%q error=%q)",
			res.Reason, res.Error)
	}
	if res.Status != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 (the adapter's promotion)", res.Status)
	}
	if res.UpstreamStatus != http.StatusOK {
		t.Errorf("upstream_status = %d, want 200 (the upstream DID answer 200)", res.UpstreamStatus)
	}
	if res.Code != CodeUpstreamBusinessError {
		t.Errorf("code = %q, want %q", res.Code, CodeUpstreamBusinessError)
	}
	if res.RelayStatus != http.StatusBadGateway {
		t.Errorf("relay_status = %d, want 502", res.RelayStatus)
	}
	if !strings.Contains(res.Reason, "base_resp.status_code=2013") {
		t.Errorf("reason = %q, want the upstream's numeric code", res.Reason)
	}
	if !strings.Contains(res.Error, "invalid parameters") {
		t.Errorf("error = %q, want the upstream's own message", res.Error)
	}
	// No image was produced — the URLs must be empty (a failure must
	// never carry an image).
	if res.ImageCount != 0 {
		t.Errorf("image_count = %d, want 0 on a business failure", res.ImageCount)
	}
	if got := fake.hitCount.Load(); got != 1 {
		t.Errorf("upstream hits = %d, want 1", got)
	}
}

// TestExecuteTest_UpstreamHTTPErrorIsClassified — the upstream's own
// error status stays the operator-facing classification status (the
// FE renders 401 / 402 / 429 / 5xx as distinct classes).
func TestExecuteTest_UpstreamHTTPErrorIsClassified(t *testing.T) {
	cases := []struct {
		status int
		code   string
	}{
		{http.StatusUnauthorized, CodeUpstreamHTTPError},
		{http.StatusPaymentRequired, CodeUpstreamHTTPError},
		{http.StatusTooManyRequests, CodeCooldown},
		{http.StatusInternalServerError, CodeUpstreamHTTPError},
		{http.StatusServiceUnavailable, CodeUpstreamHTTPError},
	}
	for _, tc := range cases {
		srv, fake := newFakeMiniMax(t, fakeScript{
			status: tc.status,
			body:   []byte(`{"error":{"message":"upstream says no"}}`),
		})

		res := ExecuteTest(testExecCred(srv.URL), testExecBody(t, "minimax-image-01"), 5*time.Second)

		if res.Status != tc.status {
			t.Errorf("status = %d, want %d (the upstream status is the classification signal)", res.Status, tc.status)
		}
		if res.UpstreamStatus != tc.status {
			t.Errorf("upstream_status = %d, want %d", res.UpstreamStatus, tc.status)
		}
		if res.Code != tc.code {
			t.Errorf("code = %q, want %q", res.Code, tc.code)
		}
		if res.ImageCount != 0 {
			t.Errorf("image_count = %d, want 0 on an error", res.ImageCount)
		}
		if got := fake.hitCount.Load(); got != 1 {
			t.Errorf("upstream hits = %d, want 1", got)
		}
		srv.Close()
	}
}

// TestExecuteTest_EmptyImageIsClassified — a 200 whose body carries
// no image URL is the empty_image class: the upstream accepted the
// request and produced nothing (NOT a green checkmark).
func TestExecuteTest_EmptyImageIsClassified(t *testing.T) {
	srv, _ := newFakeMiniMax(t, fakeScript{
		status: http.StatusOK,
		body: mustJSON(map[string]interface{}{
			"data":      nil,
			"metadata":  map[string]interface{}{"success_count": "0", "failed_count": "0"},
			"base_resp": map[string]interface{}{"status_code": 0, "status_msg": "success"},
		}),
	})

	res := ExecuteTest(testExecCred(srv.URL), testExecBody(t, "minimax-image-01"), 5*time.Second)

	if res.Status != http.StatusOK {
		t.Errorf("status = %d, want 200 (the upstream answered fine)", res.Status)
	}
	if res.Code != CodeEmptyImage {
		t.Errorf("code = %q, want %q", res.Code, CodeEmptyImage)
	}
	if res.ImageCount != 0 {
		t.Errorf("image_count = %d, want 0", res.ImageCount)
	}
	if res.Error == "" {
		t.Error("error = empty, want a readable explanation")
	}
}

// TestExecuteTest_MalformedBodyIsMalformed — a 2xx body the adapter
// cannot classify (no base_resp — the API-drift guard) is a
// failure, never a success.
func TestExecuteTest_MalformedBodyIsMalformed(t *testing.T) {
	srv, _ := newFakeMiniMax(t, fakeScript{
		status: http.StatusOK,
		body:   []byte(`{"data":[]}`),
	})

	res := ExecuteTest(testExecCred(srv.URL), testExecBody(t, "minimax-image-01"), 5*time.Second)

	if res.Status != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", res.Status)
	}
	if res.Code != CodeUpstreamMalformed {
		t.Errorf("code = %q, want %q", res.Code, CodeUpstreamMalformed)
	}
	if res.ImageCount != 0 {
		t.Errorf("image_count = %d, want 0", res.ImageCount)
	}
}

// TestExecuteTest_UnsupportedProviderIsBadGateway — a provider with
// no image-generation adapter is a clean 502 (no upstream call).
func TestExecuteTest_UnsupportedProviderIsBadGateway(t *testing.T) {
	srv, fake := newFakeMiniMax(t, fakeScript{status: http.StatusOK, body: []byte(`{}`)})

	cred := testExecCred(srv.URL)
	cred.Provider = "anthropic"

	res := ExecuteTest(cred, testExecBody(t, "minimax-image-01"), 5*time.Second)

	if res.Status != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", res.Status)
	}
	if res.Code != CodeNoAdapter {
		t.Errorf("code = %q, want %q", res.Code, CodeNoAdapter)
	}
	if !strings.Contains(res.Error, "no image-generation adapter") {
		t.Errorf("error = %q, want the 'no image-generation adapter' message", res.Error)
	}
	if got := fake.hitCount.Load(); got != 0 {
		t.Errorf("upstream hits = %d, want 0", got)
	}
}

// TestExecuteTest_TransportErrorIsBadGateway — a dead upstream is a
// 502 with a readable message (never a raw dump).
func TestExecuteTest_TransportErrorIsBadGateway(t *testing.T) {
	srv, _ := newFakeMiniMax(t, fakeScript{status: http.StatusOK, body: []byte(`{}`)})
	baseURL := srv.URL
	srv.Close() // the upstream is gone — the call must fail cleanly

	res := ExecuteTest(testExecCred(baseURL), testExecBody(t, "minimax-image-01"), 2*time.Second)

	if res.Status != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", res.Status)
	}
	if res.Code != CodeUpstreamTransportError {
		t.Errorf("code = %q, want %q", res.Code, CodeUpstreamTransportError)
	}
	if res.Error == "" {
		t.Errorf("error = %q, want a readable failure message", res.Error)
	}
}

// TestExecuteTest_TimeoutIsGatewayTimeout — the probe deadline is
// owned by the seam (ctx, never r.Context()) and surfaces as a 504.
func TestExecuteTest_TimeoutIsGatewayTimeout(t *testing.T) {
	srv, _ := newFakeMiniMax(t, fakeScript{
		status: http.StatusOK,
		body:   successBody(1, 0),
		delay:  400 * time.Millisecond,
	})

	res := ExecuteTest(testExecCred(srv.URL), testExecBody(t, "minimax-image-01"), 50*time.Millisecond)

	if res.Status != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want 504", res.Status)
	}
	if res.Code != CodeTimeout {
		t.Errorf("code = %q, want %q", res.Code, CodeTimeout)
	}
}

// TestExecuteTest_ResponseOverCapIsRejected — the 64 MB response cap
// (the test-only override hook keeps the allocation small) discards
// the body and classifies the outcome.
func TestExecuteTest_ResponseOverCapIsRejected(t *testing.T) {
	prev := maxResponseBodyBytesForTest
	maxResponseBodyBytesForTest = 1024
	defer func() { maxResponseBodyBytesForTest = prev }()

	big := make([]byte, 4096)
	for i := range big {
		big[i] = 'a'
	}
	body, err := json.Marshal(map[string]interface{}{
		"data":      []interface{}{map[string]interface{}{"image_urls": []string{"https://x/" + string(big)}}},
		"base_resp": map[string]interface{}{"status_code": 0, "status_msg": "success"},
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	srv, _ := newFakeMiniMax(t, fakeScript{status: http.StatusOK, body: body})

	res := ExecuteTest(testExecCred(srv.URL), testExecBody(t, "minimax-image-01"), 5*time.Second)

	if res.Status != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", res.Status)
	}
	if res.Code != CodeResponseTooLarge {
		t.Errorf("code = %q, want %q", res.Code, CodeResponseTooLarge)
	}
	if len(res.Body) != 0 {
		t.Errorf("body length = %d, want 0 (the over-cap body must be discarded)", len(res.Body))
	}
	if res.ImageCount != 0 {
		t.Errorf("image_count = %d, want 0 (the discarded body must not yield URLs)", res.ImageCount)
	}
}

// TestExecuteTest_MachinePins — the request discipline the FE depends
// on: EXACTLY ONE upstream call, EXACTLY ONE Content-Type, the
// credential bearer, NO client Accept-Encoding, the model rewritten
// to the upstream alias, and the /image_generation URL suffix.
func TestExecuteTest_MachinePins(t *testing.T) {
	srv, fake := newFakeMiniMax(t, fakeScript{
		status: http.StatusOK,
		body:   successBody(1, 0),
	})

	res := ExecuteTest(testExecCred(srv.URL), testExecBody(t, "minimax-image-01"), 5*time.Second)
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (error=%q)", res.Status, res.Error)
	}

	if got := fake.hitCount.Load(); got != 1 {
		t.Errorf("upstream hits = %d, want exactly 1 (no retry / fallback / race)", got)
	}
	if got := len(fake.lastCT); got != 1 {
		t.Errorf("Content-Type count = %d, want exactly 1 (values=%v)", got, fake.lastCT)
	}
	if fake.lastAuthH != "Bearer sk-imggen-exec-key" {
		t.Errorf("Authorization = %q, want the credential bearer", fake.lastAuthH)
	}
	// The probe is server-generated — NO client header is forwarded.
	// The transport still owns encoding negotiation and may add its
	// own "Accept-Encoding: gzip" (that is the transport's, not a
	// leaked client header). Anything else would be a leak — the
	// production path's precedent is the same assertion
	// (handler_test.go: "the transport may add exactly gzip").
	for _, v := range fake.lastAE {
		if v != "gzip" {
			t.Errorf("Accept-Encoding = %v, want only the transport-added [gzip] (the probe forwards no client header)", fake.lastAE)
		}
	}
	if !strings.Contains(string(fake.lastBody), `"model":"image-01"`) {
		t.Errorf("upstream body = %s, want the model rewritten to the upstream alias", string(fake.lastBody))
	}
	if strings.Contains(string(fake.lastBody), "minimax-image-01") {
		t.Errorf("upstream body = %s, want the proxy row id rewritten away", string(fake.lastBody))
	}
}

// TestExecuteTest_UpstreamPathIsImageGeneration — the probe drives
// the SAME upstream path as the production handler
// (/image_generation), never the chat /chat/completions endpoint.
func TestExecuteTest_UpstreamPathIsImageGeneration(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(successBody(1, 0))
	}))
	defer srv.Close()

	_ = ExecuteTest(testExecCred(srv.URL), testExecBody(t, "minimax-image-01"), 5*time.Second)

	if path != "/image_generation" {
		t.Errorf("upstream path = %q, want /image_generation", path)
	}
}

// TestExtractImageURLs_ToleratesBothDataShapes — the upstream's
// `data` shape differs between the fixture (an array of objects) and
// the live e2e (an object). BOTH must be extracted.
func TestExtractImageURLs_ToleratesBothDataShapes(t *testing.T) {
	arrayShape := mustJSON(map[string]interface{}{
		"data": []interface{}{
			map[string]interface{}{"image_urls": []string{"https://x/a.png", "https://x/b.png"}},
			map[string]interface{}{"image_urls": []string{"https://x/c.png"}},
		},
	})
	got := extractImageURLs(arrayShape)
	if len(got) != 3 {
		t.Fatalf("array shape urls = %v, want 3 URLs", got)
	}
	if got[2] != "https://x/c.png" {
		t.Errorf("array shape urls = %v, want the third URL to survive", got)
	}

	objectShape := mustJSON(map[string]interface{}{
		"data": map[string]interface{}{"image_urls": []string{"https://x/live.png"}},
	})
	got = extractImageURLs(objectShape)
	if len(got) != 1 || got[0] != "https://x/live.png" {
		t.Errorf("object shape urls = %v, want [https://x/live.png]", got)
	}

	if got := extractImageURLs([]byte(`{"data":null}`)); len(got) != 0 {
		t.Errorf("null data urls = %v, want none", got)
	}
	if got := extractImageURLs([]byte(`not json`)); len(got) != 0 {
		t.Errorf("garbage urls = %v, want none", got)
	}
}
