package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/imggen"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// =============================================================================
// Image-generation TEST button — POST /fe/api/image_generation/test
//
// The suite pins the contracts the feature depends on:
//
//   (a) a successful generation relays the upstream's image URLs (the
//       FE renders them directly) + the upstream's counts,
//   (b) the upstream's own error statuses (401 / 402 / 429 / 5xx)
//       surface as a CLASSIFIED status on the test DTO (not a raw
//       dump),
//   (c) a MiniMax BUSINESS failure inside an HTTP 200 is a FAILURE
//       (the green-lie guard) — the FE must never see a green
//       checkmark for a rejected generation,
//   (d) the canned body + a SINGLE Content-Type + the Authorization
//       header reach the upstream — and the probe drives the SAME
//       /image_generation upstream path as the production handler,
//   (e) the model / credential preconditions (exact row id, the
//       image-gen KIND GATE, first-credential selection),
//   (f) the route is actually REGISTERED on the real mux,
//   (g) the single-flight guard rejects a concurrent probe with the
//       classified "busy" outcome.
//
// No live network: an httptest server plays the upstream.
// =============================================================================

// imgGenUpstreamHit is one recorded hit of the fake MiniMax upstream.
type imgGenUpstreamHit struct {
	Path          string
	Method        string
	ContentTypes  []string
	Authorization string
	Body          []byte
}

// imgGenUpstream is a scriptable httptest fixture for the MiniMax
// image_generation endpoint. It records EVERY hit (the cross-path
// parity test compares the production path against the test-button
// path).
type imgGenUpstream struct {
	mu     sync.Mutex
	hits   []imgGenUpstreamHit
	status int
	body   string
	delay  time.Duration
	// hitCh is closed on the first hit (the single-flight test waits
	// for the first probe to be in flight before firing the second).
	hitCh   chan struct{}
	hitOnce sync.Once
}

// newImgGenUpstream spins an httptest upstream answering with the
// given status + body and recording the inbound requests.
func newImgGenUpstream(t *testing.T, status int, respBody string) (*httptest.Server, *imgGenUpstream) {
	t.Helper()
	up := &imgGenUpstream{status: status, body: respBody, hitCh: make(chan struct{})}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 0)
		if r.Body != nil {
			buf = readAllForImgGenTest(r)
		}
		up.mu.Lock()
		up.hits = append(up.hits, imgGenUpstreamHit{
			Path:          r.URL.Path,
			Method:        r.Method,
			ContentTypes:  r.Header.Values("Content-Type"),
			Authorization: r.Header.Get("Authorization"),
			Body:          buf,
		})
		delay := up.delay
		up.mu.Unlock()
		up.hitOnce.Do(func() { close(up.hitCh) })
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(up.status)
		_, _ = w.Write([]byte(up.body))
	}))
	t.Cleanup(srv.Close)
	return srv, up
}

// readAllForImgGenTest reads the request body (bounded).
func readAllForImgGenTest(r *http.Request) []byte {
	defer r.Body.Close()
	buf, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	return buf
}

// hitCount returns the number of recorded upstream hits.
func (u *imgGenUpstream) hitCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.hits)
}

// hit returns the n-th recorded hit.
func (u *imgGenUpstream) hit(n int) imgGenUpstreamHit {
	u.mu.Lock()
	defer u.mu.Unlock()
	if n >= len(u.hits) {
		return imgGenUpstreamHit{}
	}
	return u.hits[n]
}

// imgGenSuccessBody is a MiniMax success body in the FIXTURE shape
// (`data` = array of objects — pkg/imggen/handler_test.go:111).
func imgGenSuccessBody() string {
	return `{"data":[{"image_urls":["https://cdn.example/a.png"]}],"metadata":{"success_count":"1","failed_count":"0"},"base_resp":{"status_code":0,"status_msg":"success"}}`
}

// imgGenBusinessErrorBody is a MiniMax business failure INSIDE an
// HTTP 200 (base_resp.status_code = 2013 — the live fixture).
func imgGenBusinessErrorBody() string {
	return `{"data":null,"base_resp":{"status_code":2013,"status_msg":"invalid parameters"}}`
}

// newImgGenProbeServer wires a server with ONE image-gen model whose
// first credential points at the given upstream.
func newImgGenProbeServer(t *testing.T, upstreamURL string) *Server {
	t.Helper()
	mock := newMockModelsConfigForHandlers()
	if err := mock.AddCredential(models.CredentialConfig{
		ID:       "cred-img-1",
		Provider: "minimax",
		APIKey:   "sk-imggen-test-key",
		BaseURL:  upstreamURL,
	}); err != nil {
		t.Fatalf("add credential: %v", err)
	}
	if err := mock.AddModel(models.ModelConfig{
		ID:            "minimax-image-01",
		Name:          "Image Gen 01",
		Enabled:       true,
		Kind:          models.KindImageGen,
		Internal:      true,
		InternalModel: "image-01",
		Credentials: []models.CredentialRef{
			{CredentialID: "cred-img-1", Weight: 1, Position: 0},
		},
	}); err != nil {
		t.Fatalf("add model: %v", err)
	}
	return &Server{modelsConfig: mock}
}

// postImgGenTest drives handleImgGenTest and decodes the DTO.
func postImgGenTest(t *testing.T, s *Server, modelID string) (int, ImgGenTestResponse) {
	t.Helper()
	body, err := json.Marshal(ImgGenTestRequest{ModelID: modelID})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/fe/api/image_generation/test", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleImgGenTest(w, req)

	var resp ImgGenTestResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v (body=%s)", err, w.Body.String())
		}
	}
	return w.Code, resp
}

// TestHandleImgGenTest_SuccessRelaysImageURLs — (a) a successful
// generation relays the upstream's image URLs + counts.
func TestHandleImgGenTest_SuccessRelaysImageURLs(t *testing.T) {
	upstream, up := newImgGenUpstream(t, http.StatusOK, imgGenSuccessBody())

	s := newImgGenProbeServer(t, upstream.URL)
	code, resp := postImgGenTest(t, s, "minimax-image-01")

	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !resp.Success {
		t.Fatalf("success = false, want true (resp=%+v)", resp)
	}
	if resp.Status != http.StatusOK {
		t.Errorf("status field = %d, want 200", resp.Status)
	}
	if resp.Code != imggen.CodeOK {
		t.Errorf("code = %q, want %q", resp.Code, imggen.CodeOK)
	}
	if resp.Model != "minimax-image-01" {
		t.Errorf("model = %q, want the proxy row id", resp.Model)
	}
	if resp.Provider != "minimax" {
		t.Errorf("provider = %q, want minimax", resp.Provider)
	}
	if resp.CredentialID != "cred-img-1" {
		t.Errorf("credential_id = %q, want cred-img-1", resp.CredentialID)
	}
	if len(resp.ImageURLs) != 1 || resp.ImageURLs[0] != "https://cdn.example/a.png" {
		t.Errorf("image_urls = %v, want [https://cdn.example/a.png]", resp.ImageURLs)
	}
	if resp.ImageCount != 1 {
		t.Errorf("image_count = %d, want 1", resp.ImageCount)
	}
	if resp.SuccessCount != 1 {
		t.Errorf("success_count = %d, want 1", resp.SuccessCount)
	}
	if resp.FailedCount != 0 {
		t.Errorf("failed_count = %d, want 0", resp.FailedCount)
	}
	if resp.DurationMS < 0 {
		t.Errorf("duration_ms = %d, want >= 0", resp.DurationMS)
	}
	if up.hitCount() != 1 {
		t.Errorf("upstream calls = %d, want 1", up.hitCount())
	}
}

// TestHandleImgGenTest_UpstreamErrorStatusesAreClassified — (b) the
// upstream's own error statuses stay the operator-facing class.
func TestHandleImgGenTest_UpstreamErrorStatusesAreClassified(t *testing.T) {
	cases := []struct {
		status int
		code   string
	}{
		{http.StatusUnauthorized, imggen.CodeUpstreamHTTPError},
		{http.StatusPaymentRequired, imggen.CodeUpstreamHTTPError},
		{http.StatusTooManyRequests, imggen.CodeCooldown},
		{http.StatusInternalServerError, imggen.CodeUpstreamHTTPError},
	}
	for _, tc := range cases {
		upstream, _ := newImgGenUpstream(t, tc.status, `{"error":{"message":"upstream says no"}}`)

		s := newImgGenProbeServer(t, upstream.URL)
		code, resp := postImgGenTest(t, s, "minimax-image-01")

		// The wire status is ALWAYS 200 for a classified outcome (the
		// FE contract is ONE shape).
		if code != http.StatusOK {
			t.Errorf("wire status = %d, want 200 (classified outcomes use one shape)", code)
		}
		if resp.Success {
			t.Errorf("success = true, want false for upstream status %d", tc.status)
		}
		if resp.Status != tc.status {
			t.Errorf("status = %d, want %d (the upstream status is the class)", resp.Status, tc.status)
		}
		if resp.UpstreamStatus != tc.status {
			t.Errorf("upstream_status = %d, want %d", resp.UpstreamStatus, tc.status)
		}
		if resp.Code != tc.code {
			t.Errorf("code = %q, want %q", resp.Code, tc.code)
		}
	}
}

// TestHandleImgGenTest_BusinessErrorInHTTP200IsNotSuccess — (c) THE
// GREEN-LIE GUARD at the route level: MiniMax reports a business
// failure INSIDE an HTTP 200. The DTO must say success=false with
// the business-error code even though the upstream answered 200.
func TestHandleImgGenTest_BusinessErrorInHTTP200IsNotSuccess(t *testing.T) {
	upstream, _ := newImgGenUpstream(t, http.StatusOK, imgGenBusinessErrorBody())

	s := newImgGenProbeServer(t, upstream.URL)
	code, resp := postImgGenTest(t, s, "minimax-image-01")

	if code != http.StatusOK {
		t.Fatalf("wire status = %d, want 200", code)
	}
	if resp.Success {
		t.Fatalf("success = true — a business failure inside a 200 must NOT be a success (resp=%+v)", resp)
	}
	if resp.Code != imggen.CodeUpstreamBusinessError {
		t.Errorf("code = %q, want %q", resp.Code, imggen.CodeUpstreamBusinessError)
	}
	if resp.Status != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.Status)
	}
	if resp.UpstreamStatus != http.StatusOK {
		t.Errorf("upstream_status = %d, want 200 (the upstream DID answer 200)", resp.UpstreamStatus)
	}
	if !strings.Contains(resp.Error, "invalid parameters") {
		t.Errorf("error = %q, want the upstream's own message", resp.Error)
	}
	if !strings.Contains(resp.Reason, "base_resp.status_code=2013") {
		t.Errorf("reason = %q, want the upstream's numeric code", resp.Reason)
	}
	if len(resp.ImageURLs) != 0 {
		t.Errorf("image_urls = %v, want none on a failure", resp.ImageURLs)
	}
}

// TestHandleImgGenTest_CannedBodyShape — (d) the canned body reaches
// the upstream clean: exactly ONE Content-Type, the credential
// bearer, the model rewritten to the upstream alias, and the
// /image_generation path.
func TestHandleImgGenTest_CannedBodyShape(t *testing.T) {
	upstream, up := newImgGenUpstream(t, http.StatusOK, imgGenSuccessBody())

	s := newImgGenProbeServer(t, upstream.URL)
	if _, resp := postImgGenTest(t, s, "minimax-image-01"); !resp.Success {
		t.Fatalf("success = false, want true (resp=%+v)", resp)
	}

	if up.hitCount() != 1 {
		t.Fatalf("upstream calls = %d, want exactly 1", up.hitCount())
	}
	hit := up.hit(0)
	if hit.Path != "/image_generation" {
		t.Errorf("upstream path = %q, want /image_generation", hit.Path)
	}
	if hit.Method != http.MethodPost {
		t.Errorf("upstream method = %q, want POST", hit.Method)
	}
	if len(hit.ContentTypes) != 1 {
		t.Errorf("Content-Type count = %d, want exactly 1 (values=%v)", len(hit.ContentTypes), hit.ContentTypes)
	}
	if hit.Authorization != "Bearer sk-imggen-test-key" {
		t.Errorf("Authorization = %q, want the credential bearer", hit.Authorization)
	}
	var sent map[string]interface{}
	if err := json.Unmarshal(hit.Body, &sent); err != nil {
		t.Fatalf("upstream body unparseable: %v (body=%s)", err, string(hit.Body))
	}
	if sent["model"] != "image-01" {
		t.Errorf("model = %v, want image-01 (the row id is rewritten to the upstream alias)", sent["model"])
	}
	if sent["prompt"] == nil || sent["prompt"] == "" {
		t.Errorf("prompt = %v, want the canned prompt", sent["prompt"])
	}
	if sent["aspect_ratio"] != "1:1" {
		t.Errorf("aspect_ratio = %v, want 1:1", sent["aspect_ratio"])
	}
	if n, ok := sent["n"].(float64); !ok || n != 1 {
		t.Errorf("n = %v, want 1", sent["n"])
	}
	if sent["response_format"] != "url" {
		t.Errorf("response_format = %v, want url (the FE renders the CDN URLs)", sent["response_format"])
	}
}

// TestHandleImgGenTest_RejectsNonImageGenModel — (e) the KIND GATE:
// a chat row posted to the image-gen test route is refused BEFORE any
// credential resolution or upstream call (the production
// /v1/image_generation path has no such gate).
func TestHandleImgGenTest_RejectsNonImageGenModel(t *testing.T) {
	upstream, up := newImgGenUpstream(t, http.StatusOK, imgGenSuccessBody())

	mock := newMockModelsConfigForHandlers()
	if err := mock.AddCredential(models.CredentialConfig{
		ID: "cred-chat-1", Provider: "openai", APIKey: "k", BaseURL: upstream.URL,
	}); err != nil {
		t.Fatalf("add credential: %v", err)
	}
	if err := mock.AddModel(models.ModelConfig{
		ID: "chat-model-1", Name: "Chat", Enabled: true, Internal: true, InternalModel: "gpt-4o",
		Credentials: []models.CredentialRef{{CredentialID: "cred-chat-1", Weight: 1, Position: 0}},
	}); err != nil {
		t.Fatalf("add model: %v", err)
	}
	s := &Server{modelsConfig: mock}

	code, _ := postImgGenTest(t, s, "chat-model-1")
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (a chat row is not an image-gen model)", code)
	}
	if up.hitCount() != 0 {
		t.Errorf("upstream calls = %d, want 0 (the gate fires before any upstream call)", up.hitCount())
	}
}

// TestHandleImgGenTest_UnknownModelIsNotFound — (e) an unknown row id
// is a 404 and never reaches the upstream.
func TestHandleImgGenTest_UnknownModelIsNotFound(t *testing.T) {
	upstream, up := newImgGenUpstream(t, http.StatusOK, imgGenSuccessBody())

	s := newImgGenProbeServer(t, upstream.URL)
	code, _ := postImgGenTest(t, s, "no-such-image-model")
	if code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
	if up.hitCount() != 0 {
		t.Errorf("upstream calls = %d, want 0", up.hitCount())
	}
}

// TestHandleImgGenTest_UsesFirstCredentialByExactID — (e) the
// test-button credential convention: the model's FIRST credential is
// selected via an exact-ID lookup (not the LB engine's pick). The
// second credential is a different provider pointing at
// example.invalid — if it were picked, the provider would change
// AND the upstream URL would be unreachable.
func TestHandleImgGenTest_UsesFirstCredentialByExactID(t *testing.T) {
	upstream, up := newImgGenUpstream(t, http.StatusOK, imgGenSuccessBody())

	mock := newMockModelsConfigForHandlers()
	// The FIRST credential (the one the convention selects).
	if err := mock.AddCredential(models.CredentialConfig{
		ID: "cred-first", Provider: "minimax", APIKey: "sk-first", BaseURL: upstream.URL,
	}); err != nil {
		t.Fatalf("add credential: %v", err)
	}
	// A decoy second credential: a different provider and an
	// unreachable base URL. A pick of it would change the provider
	// and the upstream URL.
	if err := mock.AddCredential(models.CredentialConfig{
		ID: "cred-second", Provider: "anthropic", APIKey: "sk-second", BaseURL: "https://example.invalid",
	}); err != nil {
		t.Fatalf("add credential: %v", err)
	}
	if err := mock.AddModel(models.ModelConfig{
		ID: "minimax-image-01", Name: "Image Gen 01", Enabled: true,
		Kind: models.KindImageGen, Internal: true, InternalModel: "image-01",
		Credentials: []models.CredentialRef{
			{CredentialID: "cred-first", Weight: 1, Position: 0},
			{CredentialID: "cred-second", Weight: 1, Position: 1},
		},
	}); err != nil {
		t.Fatalf("add model: %v", err)
	}
	s := &Server{modelsConfig: mock}

	_, resp := postImgGenTest(t, s, "minimax-image-01")
	if !resp.Success {
		t.Fatalf("success = false, want true (resp=%+v)", resp)
	}
	if resp.CredentialID != "cred-first" {
		t.Errorf("credential_id = %q, want cred-first (the FIRST credential)", resp.CredentialID)
	}
	if resp.Provider != "minimax" {
		t.Errorf("provider = %q, want minimax (the decoy credential's provider must not be picked)", resp.Provider)
	}
	if up.hitCount() != 1 {
		t.Fatalf("upstream calls = %d, want 1", up.hitCount())
	}
	if want := "Bearer sk-first"; up.hit(0).Authorization != want {
		t.Errorf("Authorization = %q, want %q (the first credential's key)", up.hit(0).Authorization, want)
	}
}

// TestHandleImgGenTest_NoCredentialsIsRejected — (e) an image-gen row
// without credentials cannot be probed.
func TestHandleImgGenTest_NoCredentialsIsRejected(t *testing.T) {
	mock := newMockModelsConfigForHandlers()
	if err := mock.AddModel(models.ModelConfig{
		ID: "minimax-image-01", Name: "Image Gen 01", Enabled: true,
		Kind: models.KindImageGen, Internal: true, InternalModel: "image-01",
	}); err != nil {
		t.Fatalf("add model: %v", err)
	}
	s := &Server{modelsConfig: mock}

	code, _ := postImgGenTest(t, s, "minimax-image-01")
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}

// TestHandleImgGenTest_UnknownCredentialIsNotFound — (e) a dangling
// credential reference is a 404 (the operator's row is broken).
func TestHandleImgGenTest_UnknownCredentialIsNotFound(t *testing.T) {
	mock := newMockModelsConfigForHandlers()
	if err := mock.AddModel(models.ModelConfig{
		ID: "minimax-image-01", Name: "Image Gen 01", Enabled: true,
		Kind: models.KindImageGen, Internal: true, InternalModel: "image-01",
		Credentials: []models.CredentialRef{{CredentialID: "cred-missing", Weight: 1, Position: 0}},
	}); err != nil {
		t.Fatalf("add model: %v", err)
	}
	s := &Server{modelsConfig: mock}

	code, _ := postImgGenTest(t, s, "minimax-image-01")
	if code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}

// TestHandleImgGenTest_CredentialWithoutAPIKeyIsRejected — (e) a
// credential with an empty API key cannot be probed.
func TestHandleImgGenTest_CredentialWithoutAPIKeyIsRejected(t *testing.T) {
	mock := newMockModelsConfigForHandlers()
	if err := mock.AddCredential(models.CredentialConfig{
		ID: "cred-nokey", Provider: "minimax", APIKey: "", BaseURL: "https://api.minimax.io/v1",
	}); err != nil {
		t.Fatalf("add credential: %v", err)
	}
	if err := mock.AddModel(models.ModelConfig{
		ID: "minimax-image-01", Name: "Image Gen 01", Enabled: true,
		Kind: models.KindImageGen, Internal: true, InternalModel: "image-01",
		Credentials: []models.CredentialRef{{CredentialID: "cred-nokey", Weight: 1, Position: 0}},
	}); err != nil {
		t.Fatalf("add model: %v", err)
	}
	s := &Server{modelsConfig: mock}

	code, _ := postImgGenTest(t, s, "minimax-image-01")
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}

// TestHandleImgGenTest_MissingModelIDIsBadRequest — a request with no
// model_id is a 400 (bare /fe/api envelope).
func TestHandleImgGenTest_MissingModelIDIsBadRequest(t *testing.T) {
	upstream, _ := newImgGenUpstream(t, http.StatusOK, imgGenSuccessBody())
	s := newImgGenProbeServer(t, upstream.URL)

	req := httptest.NewRequest(http.MethodPost, "/fe/api/image_generation/test", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	s.handleImgGenTest(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "model_id") {
		t.Errorf("body = %q, want the model_id complaint", w.Body.String())
	}
}

// TestHandleImgGenTest_RejectsNonPost — the method guard is a plain
// 405 (house style — NOT the JSON envelope).
func TestHandleImgGenTest_RejectsNonPost(t *testing.T) {
	upstream, up := newImgGenUpstream(t, http.StatusOK, imgGenSuccessBody())
	s := newImgGenProbeServer(t, upstream.URL)

	req := httptest.NewRequest(http.MethodGet, "/fe/api/image_generation/test", nil)
	w := httptest.NewRecorder()
	s.handleImgGenTest(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
	if up.hitCount() != 0 {
		t.Errorf("upstream calls = %d, want 0", up.hitCount())
	}
}

// TestHandleImgGenTest_TransportFailureCarriesTheCause — a dead
// upstream is a classified 502 whose error carries the CAUSE (the
// FE renders the class + the cause, never a bare "Upstream error").
func TestHandleImgGenTest_TransportFailureCarriesTheCause(t *testing.T) {
	upstream, _ := newImgGenUpstream(t, http.StatusOK, imgGenSuccessBody())
	baseURL := upstream.URL
	upstream.Close() // the upstream is gone

	s := newImgGenProbeServer(t, baseURL)
	code, resp := postImgGenTest(t, s, "minimax-image-01")

	if code != http.StatusOK {
		t.Fatalf("wire status = %d, want 200 (a classified outcome)", code)
	}
	if resp.Success {
		t.Fatal("success = true, want false")
	}
	if resp.Status != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.Status)
	}
	if resp.Code != imggen.CodeUpstreamTransportError {
		t.Errorf("code = %q, want %q", resp.Code, imggen.CodeUpstreamTransportError)
	}
	if resp.Error == "" {
		t.Error("error = empty, want the transport cause")
	}
}

// TestHandleImgGenTest_EmptyImageIsClassified — a 200 with no image
// is the empty_image class (never a green checkmark).
func TestHandleImgGenTest_EmptyImageIsClassified(t *testing.T) {
	upstream, _ := newImgGenUpstream(t, http.StatusOK,
		`{"data":null,"metadata":{"success_count":"0","failed_count":"0"},"base_resp":{"status_code":0,"status_msg":"success"}}`)

	s := newImgGenProbeServer(t, upstream.URL)
	code, resp := postImgGenTest(t, s, "minimax-image-01")

	if code != http.StatusOK {
		t.Fatalf("wire status = %d, want 200", code)
	}
	if resp.Success {
		t.Fatal("success = true, want false (no image was produced)")
	}
	if resp.Code != imggen.CodeEmptyImage {
		t.Errorf("code = %q, want %q", resp.Code, imggen.CodeEmptyImage)
	}
	if resp.ImageCount != 0 {
		t.Errorf("image_count = %d, want 0", resp.ImageCount)
	}
}

// TestHandleImgGenTest_BusyWhenAnotherProbeIsInFlight — the
// single-flight guard: a second concurrent probe is rejected
// immediately with the classified "busy" outcome (never queued — a
// queued probe would be a second paid generation).
func TestHandleImgGenTest_BusyWhenAnotherProbeIsInFlight(t *testing.T) {
	upstream, up := newImgGenUpstream(t, http.StatusOK, imgGenSuccessBody())
	up.mu.Lock()
	up.delay = 300 * time.Millisecond
	up.mu.Unlock()

	s := newImgGenProbeServer(t, upstream.URL)

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		// NOTE: no t.Fatalf in this goroutine (a fatal in a
		// non-test goroutine is a vet error) — the first probe's
		// outcome is asserted through the upstream hit count.
		req := httptest.NewRequest(http.MethodPost, "/fe/api/image_generation/test",
			strings.NewReader(`{"model_id":"minimax-image-01"}`))
		w := httptest.NewRecorder()
		s.handleImgGenTest(w, req)
	}()

	// Wait until the first probe is actually in flight upstream.
	select {
	case <-up.hitCh:
	case <-time.After(3 * time.Second):
		t.Fatal("the first probe never reached the upstream")
	}

	code, resp := postImgGenTest(t, s, "minimax-image-01")
	if code != http.StatusOK {
		t.Errorf("wire status = %d, want 200 (busy is a classified outcome)", code)
	}
	if resp.Success {
		t.Error("success = true, want false")
	}
	if resp.Code != imggen.CodeBusy {
		t.Errorf("code = %q, want %q", resp.Code, imggen.CodeBusy)
	}
	if resp.Status != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", resp.Status)
	}

	<-firstDone
	if up.hitCount() != 1 {
		t.Errorf("upstream calls = %d, want 1 (the busy probe must NOT reach the upstream)", up.hitCount())
	}
}

// TestHandleImgGenTest_RouteIsRegistered — (f) ROUTE REGISTRATION.
// The decision lane never had this test: the route existed in the
// mux but a rename would have gone unnoticed. This builds the REAL
// mux through the server's own registration and drives the route
// over HTTP.
func TestHandleImgGenTest_RouteIsRegistered(t *testing.T) {
	upstream, _ := newImgGenUpstream(t, http.StatusOK, imgGenSuccessBody())

	s := newImgGenProbeServer(t, upstream.URL)
	mux := http.NewServeMux()
	s.RegisterHandlers(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// (a) GET is rejected with a 405 — the route EXISTS.
	resp, err := http.Get(srv.URL + "/fe/api/image_generation/test")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405 (the route must be registered)", resp.StatusCode)
	}

	// (b) POST reaches the handler and returns the classified DTO.
	body := strings.NewReader(`{"model_id":"minimax-image-01"}`)
	post, err := http.Post(srv.URL+"/fe/api/image_generation/test", "application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer post.Body.Close()
	if post.StatusCode != http.StatusOK {
		t.Fatalf("POST status = %d, want 200 (body=%s)", post.StatusCode, readAllString(post.Body))
	}
	var decoded ImgGenTestResponse
	if err := json.NewDecoder(post.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !decoded.Success {
		t.Errorf("success = false, want true (resp=%+v)", decoded)
	}
}

// readAllString reads a body (bounded) as a string.
func readAllString(r io.Reader) string {
	buf, _ := io.ReadAll(io.LimitReader(r, 1<<20))
	return string(buf)
}

// imgGenParityTokenStore is a minimal in-memory token store for the
// cross-path parity test: it validates the plaintext against the
// registered token's SHA-256 hash (auth.HashToken) — the same check
// the production token store performs.
type imgGenParityTokenStore struct {
	tokens map[string]*auth.AuthToken
}

func newImgGenParityTokenStore(plaintext string) *imgGenParityTokenStore {
	return &imgGenParityTokenStore{tokens: map[string]*auth.AuthToken{
		"tok-parity": {
			ID:            "tok-parity",
			Name:          "Parity Token",
			TokenHash:     auth.HashToken(plaintext),
			AllowedModels: nil, // nil = all models allowed
		},
	}}
}

func (m *imgGenParityTokenStore) ValidateToken(ctx context.Context, plaintext string) (*auth.AuthToken, error) {
	for _, tok := range m.tokens {
		if auth.HashToken(plaintext) == tok.TokenHash {
			return tok, nil
		}
	}
	return nil, auth.ErrTokenNotFound
}

func (m *imgGenParityTokenStore) CreateToken(ctx context.Context, name string, expiresAt *time.Time, createdBy string, ultimateModelEnabled bool, ultimateModelID string, allowedModels []string) (string, *auth.AuthToken, error) {
	return "", nil, auth.ErrTokenNotFound
}

func (m *imgGenParityTokenStore) DeleteToken(ctx context.Context, id string) error {
	return auth.ErrTokenNotFound
}

func (m *imgGenParityTokenStore) ListTokens(ctx context.Context) ([]auth.AuthToken, error) {
	out := make([]auth.AuthToken, 0, len(m.tokens))
	for _, t := range m.tokens {
		out = append(out, *t)
	}
	return out, nil
}

func (m *imgGenParityTokenStore) GetTokenByID(ctx context.Context, id string) (*auth.AuthToken, error) {
	t, ok := m.tokens[id]
	if !ok {
		return nil, auth.ErrTokenNotFound
	}
	return t, nil
}

func (m *imgGenParityTokenStore) UpdateTokenPermission(ctx context.Context, id string, ultimateModelEnabled bool, ultimateModelID string, allowedModels []string) error {
	return auth.ErrTokenNotFound
}

// imgGenParityConfig is the config stub the real imggen handler
// needs (it only reads the image-gen timeout).
type imgGenParityConfig struct{ timeout time.Duration }

func (c imgGenParityConfig) GetImageGenTimeout() time.Duration { return c.timeout }

// TestHandleImgGenTest_CrossPathParity_RealVsTestButton — (d) the
// MACHINERY-REUSE proof: the REAL /v1/image_generation path and the
// TEST BUTTON path are driven against ONE scriptable fake MiniMax
// upstream, and both hits must reach the SAME upstream path with the
// SAME shape (one Content-Type, the same bearer, the same rewritten
// model). The test path therefore drives the SAME production
// machinery — a future divergence (a different URL suffix, a
// duplicated Content-Type, a missing model rewrite) fails HERE.
func TestHandleImgGenTest_CrossPathParity_RealVsTestButton(t *testing.T) {
	upstream, up := newImgGenUpstream(t, http.StatusOK, imgGenSuccessBody())

	const (
		credKey       = "sk-parity-key"
		rowID         = "minimax-image-01"
		upstreamModel = "image-01"
	)

	// ---- Path 1: the REAL /v1/image_generation handler ------------
	realHandler := imggen.NewHandler(
		imgGenParityConfig{timeout: 5 * time.Second},
		nil, // no event bus
		newImgGenParityTokenStore("sk-test-token"),
		nil, // no usage counter
	)
	realHandler.SetModelResolver(func(id string) (*models.ModelConfig, bool) {
		if id != rowID {
			return nil, false
		}
		return &models.ModelConfig{
			ID:              rowID,
			Name:            "Image Gen 01",
			Kind:            models.KindImageGen,
			Enabled:         true,
			Internal:        true,
			InternalModel:   upstreamModel,
			InternalBaseURL: upstream.URL,
			Credentials:     []models.CredentialRef{{CredentialID: "cred-parity"}},
		}, true
	})
	realHandler.SetCredentialResolver(func(id string) (models.ResolvedCredential, bool) {
		if id != rowID {
			return models.ResolvedCredential{}, false
		}
		return models.ResolvedCredential{
			Provider:      "minimax",
			APIKey:        credKey,
			BaseURL:       upstream.URL,
			InternalModel: upstreamModel,
		}, true
	})

	realReq := httptest.NewRequest(http.MethodPost, "/v1/image_generation",
		strings.NewReader(`{"model":"`+rowID+`","prompt":"a small red apple","aspect_ratio":"1:1","n":1}`))
	realReq.Header.Set("Content-Type", "application/json")
	realReq.Header.Set("Authorization", "Bearer sk-test-token")
	realRec := httptest.NewRecorder()
	realHandler.HandleImageGeneration(realRec, realReq)
	if realRec.Code != http.StatusOK {
		t.Fatalf("real path status = %d, want 200 (body=%s)", realRec.Code, realRec.Body.String())
	}

	// ---- Path 2: the TEST BUTTON path ------------------------------
	mock := newMockModelsConfigForHandlers()
	if err := mock.AddCredential(models.CredentialConfig{
		ID: "cred-parity", Provider: "minimax", APIKey: credKey, BaseURL: upstream.URL,
	}); err != nil {
		t.Fatalf("add credential: %v", err)
	}
	if err := mock.AddModel(models.ModelConfig{
		ID: rowID, Name: "Image Gen 01", Enabled: true,
		Kind: models.KindImageGen, Internal: true, InternalModel: upstreamModel,
		Credentials: []models.CredentialRef{{CredentialID: "cred-parity", Weight: 1, Position: 0}},
	}); err != nil {
		t.Fatalf("add model: %v", err)
	}
	s := &Server{modelsConfig: mock}
	if _, resp := postImgGenTest(t, s, rowID); !resp.Success {
		t.Fatalf("test path failed: %+v", resp)
	}

	// ---- The parity assertions -------------------------------------
	if up.hitCount() != 2 {
		t.Fatalf("upstream calls = %d, want 2 (the real path + the test path)", up.hitCount())
	}
	real, probe := up.hit(0), up.hit(1)

	if real.Path != probe.Path {
		t.Errorf("upstream path parity: real %q != probe %q", real.Path, probe.Path)
	}
	if real.Path != "/image_generation" {
		t.Errorf("upstream path = %q, want /image_generation (BOTH paths must use the image-generation endpoint)", real.Path)
	}
	if real.Method != probe.Method {
		t.Errorf("upstream method parity: real %q != probe %q", real.Method, probe.Method)
	}
	if len(real.ContentTypes) != 1 || len(probe.ContentTypes) != 1 {
		t.Errorf("Content-Type count parity: real %d (%v) != probe %d (%v) — BOTH must be exactly 1",
			len(real.ContentTypes), real.ContentTypes, len(probe.ContentTypes), probe.ContentTypes)
	}
	if real.Authorization != probe.Authorization {
		t.Errorf("Authorization parity: real %q != probe %q", real.Authorization, probe.Authorization)
	}
	if want := "Bearer " + credKey; real.Authorization != want || probe.Authorization != want {
		t.Errorf("Authorization: real %q / probe %q, want %q (the credential bearer, never the client's sk- token)",
			real.Authorization, probe.Authorization, want)
	}
	// The model rewrite must be identical on both paths: the proxy
	// row id never reaches the upstream.
	for name, hit := range map[string]imgGenUpstreamHit{"real": real, "probe": probe} {
		var sent map[string]interface{}
		if err := json.Unmarshal(hit.Body, &sent); err != nil {
			t.Fatalf("%s path body unparseable: %v (body=%s)", name, err, string(hit.Body))
		}
		if sent["model"] != upstreamModel {
			t.Errorf("%s path model = %v, want %q (the row id is rewritten on BOTH paths)", name, sent["model"], upstreamModel)
		}
	}
}

// TestHandleImgGenTest_ProbesAreNotMetered — the probe must NOT write
// usage rows: it is a probe of a model the operator is merely
// checking, and a probe row would corrupt the production accounting
// (IncrementModelImages is the exclusive writer of that row).
func TestHandleImgGenTest_ProbesAreNotMetered(t *testing.T) {
	upstream, _ := newImgGenUpstream(t, http.StatusOK, imgGenSuccessBody())
	s := newImgGenProbeServer(t, upstream.URL)

	// The Server carries no usage counter at all — the probe path
	// must not need one (a nil counter would panic on a metered
	// call).
	if _, resp := postImgGenTest(t, s, "minimax-image-01"); !resp.Success {
		t.Fatalf("success = false, want true (resp=%+v)", resp)
	}
}

// TestHandleImgGenTest_ErrorMessageIsCapped — the upstream message
// the FE receives is capped (a pathological upstream body must not
// flood the operator panel).
func TestHandleImgGenTest_ErrorMessageIsCapped(t *testing.T) {
	longMsg := strings.Repeat("x", 900)
	upstream, _ := newImgGenUpstream(t, http.StatusOK,
		fmt.Sprintf(`{"data":null,"base_resp":{"status_code":2013,"status_msg":%q}}`, longMsg))

	s := newImgGenProbeServer(t, upstream.URL)
	_, resp := postImgGenTest(t, s, "minimax-image-01")

	// The cap is byte-based (the house convention — the decision lane
	// truncates the same way) and the ellipsis is a 3-byte rune.
	if len(resp.Error) > imgGenTestMaxMessageLen+len("…") {
		t.Errorf("error length = %d, want <= %d", len(resp.Error), imgGenTestMaxMessageLen+len("…"))
	}
}

// TestHandleImgGenTest_AdapterReasonIsCapped — the adapter reason
// the FE receives is capped as well: the reason quotes an upstream-
// controlled token (the adapter's "unparseable
// base_resp.status_code %q"), so an upstream that answers with a
// pathological status_code must not flood the operator panel.
//
// The cap is the SAME bound + the SAME helper the upstream message
// uses (byte-based, house convention), and the head of the reason
// is preserved (the class prefix stays readable).
func TestHandleImgGenTest_AdapterReasonIsCapped(t *testing.T) {
	longToken := strings.Repeat("z", 900)
	upstream, _ := newImgGenUpstream(t, http.StatusOK,
		fmt.Sprintf(`{"data":null,"base_resp":{"status_code":%q}}`, longToken))

	s := newImgGenProbeServer(t, upstream.URL)
	_, resp := postImgGenTest(t, s, "minimax-image-01")

	if !strings.HasPrefix(resp.Reason, "unparseable base_resp.status_code ") {
		t.Fatalf("reason = %q, want the adapter's reason prefix", resp.Reason)
	}
	if len(resp.Reason) > imgGenTestMaxMessageLen+len("…") {
		t.Errorf("reason length = %d, want <= %d", len(resp.Reason), imgGenTestMaxMessageLen+len("…"))
	}
}
