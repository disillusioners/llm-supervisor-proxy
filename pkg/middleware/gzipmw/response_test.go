package gzipmw

// ─────────────────────────────────────────────────────────────────────────────
// Response-compression middleware (P1-4) — tests
// ─────────────────────────────────────────────────────────────────────────────
//
// The compression middleware sits inside the /fe/api/* handlers (NOT on
// the proxy paths). It gzips JSON responses when the client sends
// Accept-Encoding: gzip, EXCLUDING /fe/api/events (SSE — compressing an
// SSE stream hung clients before, see docs/cloudflare-drop-hang-bug.md).
//
// Coverage:
//   - happy path: Accept-Encoding: gzip → Content-Encoding: gzip + Vary
//   - gzip absent → identity (no headers changed)
//   - threshold: bodies ≤ ~1KiB are NOT compressed (avoid wasting CPU on
//     small payloads that already gzip poorly)
//   - gunzip round-trip equals the original body byte-for-byte
//   - SSE exclusion: /fe/api/events handler passes through untouched
//     even with Accept-Encoding: gzip
//   - never double-encode: a downstream handler that already wrote
//     Content-Encoding stays uncompressed on the response
//   - Content-Length handling: when known up-front we set it; when the
//     handler streamed/chunked we drop it so the proxy never sends a
//     mismatched length
//   - SSE Flush preserved: the wrapped handler can still flush
//   - /fe/api/* non-SSE paths compress; non-/fe/api/* paths are untouched

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Test helpers
// ─────────────────────────────────────────────────────────────────────────────

// echoJSONHandler writes a fixed JSON body of `bodyLen` bytes.
// Used to produce predictable payloads for size-threshold tests.
type echoJSONHandler struct {
	bodyLen int
	wrote   int
}

func (h *echoJSONHandler) serve(w http.ResponseWriter, r *http.Request) {
	// Construct a JSON-shaped string of length ~bodyLen. We embed the
	// payload as a JSON string field so the body is valid JSON (the
	// middleware should be content-agnostic).
	pad := bytes.Repeat([]byte("x"), h.bodyLen)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	n, _ := w.Write([]byte(fmt.Sprintf(`{"data":"%s"}`, pad)))
	h.wrote = n
}

// sseEchoHandler mimics /fe/api/events — sets text/event-stream,
// flushes per event, and writes a few SSE-formatted records.
type sseEchoHandler struct {
	flushed bool
}

func (h *sseEchoHandler) serve(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no flusher", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.WriteHeader(http.StatusOK)
	for i := 0; i < 3; i++ {
		fmt.Fprintf(w, "data: {\"i\":%d}\n\n", i)
		flusher.Flush()
		h.flushed = true
	}
}

// buildResponseMiddleware applies CompressResponse around a single handler.
// We use a tiny mux so we can test multiple routes in one server
// (e.g. /fe/api/events vs /fe/api/requests).
func buildResponseMiddleware(t *testing.T, mw func(http.Handler) http.Handler, routes map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, h := range routes {
		// capture h in the loop
		path, h := path, h
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			h(w, r)
		})
	}
	return httptest.NewServer(mw(mux))
}

// gunzipBody decodes a gzip body for assertion. Returns the bytes and
// any error (test fails on error).
func gunzipBody(t *testing.T, r io.Reader) []byte {
	t.Helper()
	gr, err := gzip.NewReader(r)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	defer gr.Close()
	b, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("gunzip read: %v", err)
	}
	return b
}

// ─────────────────────────────────────────────────────────────────────────────
// Positive path: Accept-Encoding: gzip → response is gzipped + headers set
// ─────────────────────────────────────────────────────────────────────────────

func TestCompressResponse_AcceptsGzipAndEncodesBody(t *testing.T) {
	h := &echoJSONHandler{bodyLen: 8 * 1024} // well above 1KiB threshold
	server := buildResponseMiddleware(t, CompressResponse, map[string]http.HandlerFunc{
		"/fe/api/big": h.serve,
	})
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/fe/api/big", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", got)
	}
	if got := resp.Header.Get("Vary"); got != "Accept-Encoding" {
		t.Errorf("Vary = %q, want Accept-Encoding", got)
	}
	if got := resp.StatusCode; got != http.StatusOK {
		t.Errorf("status = %d, want 200", got)
	}

	// Body must gunzip to the original payload.
	body := gunzipBody(t, resp.Body)
	if !bytes.HasPrefix(body, []byte(`{"data":"xxxxx`)) {
		t.Errorf("gunzipped body unexpected: %s...", string(body[:min(40, len(body))]))
	}
}

func TestCompressResponse_GunzipRoundTripEqualsOriginal(t *testing.T) {
	const bodyLen = 16 * 1024
	h := &echoJSONHandler{bodyLen: bodyLen}
	server := buildResponseMiddleware(t, CompressResponse, map[string]http.HandlerFunc{
		"/fe/api/data": h.serve,
	})
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/fe/api/data", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	body := gunzipBody(t, resp.Body)
	// The JSON wrapper is `{"data":"<pad>"}` with pad of length bodyLen.
	wantLen := len(fmt.Sprintf(`{"data":"%s"}`, strings.Repeat("x", bodyLen)))
	if len(body) != wantLen {
		t.Errorf("decoded length = %d, want %d", len(body), wantLen)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Threshold: small bodies (<= minSize) are NOT compressed
// ─────────────────────────────────────────────────────────────────────────────

func TestCompressResponse_BelowThreshold_PassesThrough(t *testing.T) {
	// 100 bytes — well under the 1 KiB threshold.
	h := &echoJSONHandler{bodyLen: 100}
	server := buildResponseMiddleware(t, CompressResponse, map[string]http.HandlerFunc{
		"/fe/api/small": h.serve,
	})
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/fe/api/small", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("small body should NOT be gzipped; got Content-Encoding=%q", got)
	}
	if got := resp.Header.Get("Vary"); got != "Accept-Encoding" {
		t.Errorf("Vary must still be set even when not compressing (helps downstream caches); got %q", got)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.HasPrefix(body, []byte(`{"data":`)) {
		t.Errorf("body should pass through uncompressed; got prefix %q", string(body[:min(20, len(body))]))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Accept-Encoding absent → identity, no encoding
// ─────────────────────────────────────────────────────────────────────────────

func TestCompressResponse_NoAcceptEncoding_PassesThrough(t *testing.T) {
	h := &echoJSONHandler{bodyLen: 8 * 1024}
	server := buildResponseMiddleware(t, CompressResponse, map[string]http.HandlerFunc{
		"/fe/api/data": h.serve,
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/fe/api/data")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding should be absent (no Accept-Encoding); got %q", got)
	}
	if got := resp.Header.Get("Vary"); got != "Accept-Encoding" {
		t.Errorf("Vary should always be set so downstream caches partition correctly; got %q", got)
	}
}

func TestCompressResponse_AcceptEncodingIdentity_PassesThrough(t *testing.T) {
	h := &echoJSONHandler{bodyLen: 8 * 1024}
	server := buildResponseMiddleware(t, CompressResponse, map[string]http.HandlerFunc{
		"/fe/api/data": h.serve,
	})
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/fe/api/data", nil)
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding should be absent for identity; got %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// R5b — Accept-Encoding q-value parsing (RFC 9110 §12.5.3)
// ─────────────────────────────────────────────────────────────────────────────
//
// End-to-end checks for the q=0 cases. The middleware MUST NOT compress
// when the client advertises gzip with q=0 (or any valid zero form),
// even though the request is otherwise identical to a compressible
// gzip-accepting request. The unit tests in
// TestClientAcceptsGzip cover the parser in isolation; these tests pin
// the behavior through the full CompressResponse stack (path scope +
// header negotiation + writer state machine).

func TestCompressResponse_AcceptEncodingGzipQ0_PassesThrough(t *testing.T) {
	// Each row is one Accept-Encoding value that should NOT trigger
	// compression even though it mentions gzip.
	cases := []struct {
		name   string
		header string
	}{
		{"gzip;q=0 integer", "gzip;q=0"},
		{"gzip;q=0.0 float", "gzip;q=0.0"},
		{"gzip;q=0.000 extra precision", "gzip;q=0.000"},
		{"GZIP;Q=0 upper case", "GZIP;Q=0"},
		{"gzip with surrounding whitespace and q=0", " gzip ; q=0 "},
		{"deflate then gzip;q=0", "deflate, gzip;q=0"},
		{"gzip;q=0 then deflate", "gzip;q=0, deflate"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			h := &echoJSONHandler{bodyLen: 8 * 1024} // well above 1KiB threshold
			server := buildResponseMiddleware(t, CompressResponse, map[string]http.HandlerFunc{
				"/fe/api/data": h.serve,
			})
			defer server.Close()

			req, _ := http.NewRequest(http.MethodGet, server.URL+"/fe/api/data", nil)
			req.Header.Set("Accept-Encoding", tc.header)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET failed: %v", err)
			}
			defer resp.Body.Close()

			if got := resp.Header.Get("Content-Encoding"); got != "" {
				t.Errorf("Accept-Encoding %q MUST NOT trigger compression; Content-Encoding=%q",
					tc.header, got)
			}
			// Note: Vary is NOT expected here — when clientAcceptsGzip
			// returns false the middleware short-circuits and never
			// instantiates a gzipResponseWriter, so Vary is not added.
			// That matches the behavior of TestCompressResponse_
			// AcceptEncodingIdentity_PassesThrough.
			// Body must pass through as plain JSON (no gzip header).
			body, _ := io.ReadAll(resp.Body)
			if !bytes.HasPrefix(body, []byte(`{"data":`)) {
				t.Errorf("body should pass through uncompressed; got prefix %q",
					string(body[:min(20, len(body))]))
			}
		})
	}
}

// TestCompressResponse_MultipleAcceptEncodingHeaders_Joined verifies that
// two separate Accept-Encoding header fields are joined per RFC 9110.
// We send a "negation" header first and a positive "gzip" second; the
// middleware must see both and (since gzip has no q=0 in the joined
// list) compress. Conversely, a "gzip;q=0" first + "gzip" second must
// still NOT compress — once gzip is disabled in any header it is off.
func TestCompressResponse_MultipleAcceptEncodingHeaders_Joined(t *testing.T) {
	h := &echoJSONHandler{bodyLen: 8 * 1024}

	// Case 1: identity (separate header) + gzip (separate header) → compress.
	t.Run("identity-then-gzip compresses", func(t *testing.T) {
		server := buildResponseMiddleware(t, CompressResponse, map[string]http.HandlerFunc{
			"/fe/api/data": h.serve,
		})
		defer server.Close()

		req, _ := http.NewRequest(http.MethodGet, server.URL+"/fe/api/data", nil)
		// r.Header.Add appends rather than Set, producing two
		// separate header fields (http.Header.Values sees both).
		req.Header.Add("Accept-Encoding", "identity")
		req.Header.Add("Accept-Encoding", "gzip")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET failed: %v", err)
		}
		defer resp.Body.Close()
		if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
			t.Errorf("joined header (identity,gzip) should compress; Content-Encoding=%q", got)
		}
	})

	// Case 2: gzip;q=0 (separate header) + gzip (separate header) →
	// do NOT compress. The q=0 in the first header disables gzip and
	// the second header does not override it.
	t.Run("gzip;q=0 then gzip stays disabled", func(t *testing.T) {
		server := buildResponseMiddleware(t, CompressResponse, map[string]http.HandlerFunc{
			"/fe/api/data2": h.serve,
		})
		defer server.Close()

		req, _ := http.NewRequest(http.MethodGet, server.URL+"/fe/api/data2", nil)
		req.Header.Add("Accept-Encoding", "gzip;q=0")
		req.Header.Add("Accept-Encoding", "gzip")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET failed: %v", err)
		}
		defer resp.Body.Close()
		if got := resp.Header.Get("Content-Encoding"); got != "" {
			t.Errorf("joined header (gzip;q=0,gzip) must NOT compress; Content-Encoding=%q", got)
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// SSE exclusion (/fe/api/events MUST NOT be compressed)
// ─────────────────────────────────────────────────────────────────────────────

func TestCompressResponse_ExcludesSSE(t *testing.T) {
	h := &sseEchoHandler{}
	server := buildResponseMiddleware(t, CompressResponse, map[string]http.HandlerFunc{
		"/fe/api/events": h.serve,
	})
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/fe/api/events", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("SSE must NEVER be gzipped (compress broke it before); got Content-Encoding=%q", got)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.HasPrefix(string(body), "data:") {
		t.Errorf("SSE body should be plain; got prefix %q", string(body[:min(20, len(body))]))
	}
	if !h.flushed {
		t.Error("SSE handler should still be able to flush")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Path scope: only /fe/api/* gets compression; other paths pass through
// ─────────────────────────────────────────────────────────────────────────────

func TestCompressResponse_OnlyAffectsFeApiPaths(t *testing.T) {
	h := &echoJSONHandler{bodyLen: 8 * 1024}
	server := buildResponseMiddleware(t, CompressResponse, map[string]http.HandlerFunc{
		"/v1/chat/completions": h.serve, // proxy path — must NOT compress
		"/healthz":             h.serve, // root path — must NOT compress
		"/fe/api/requests":     h.serve, // FE path — must compress
	})
	defer server.Close()

	for _, tc := range []struct {
		path            string
		wantCompression bool
	}{
		{"/v1/chat/completions", false},
		{"/healthz", false},
		{"/fe/api/requests", true},
	} {
		req, _ := http.NewRequest(http.MethodGet, server.URL+tc.path, nil)
		req.Header.Set("Accept-Encoding", "gzip")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s failed: %v", tc.path, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		got := resp.Header.Get("Content-Encoding")
		compressed := got == "gzip"
		if compressed != tc.wantCompression {
			t.Errorf("%s: Content-Encoding=%q (compressed=%v), want compressed=%v",
				tc.path, got, compressed, tc.wantCompression)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Never double-encode
// ─────────────────────────────────────────────────────────────────────────────

type alreadyEncodedHandler struct{}

func (alreadyEncodedHandler) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Encoding", "br")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("precompressed"))
}

func TestCompressResponse_DoesNotDoubleEncode(t *testing.T) {
	server := buildResponseMiddleware(t, CompressResponse, map[string]http.HandlerFunc{
		"/fe/api/pre": alreadyEncodedHandler{}.serve,
	})
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/fe/api/pre", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Encoding"); got != "br" {
		t.Errorf("must NOT overwrite Content-Encoding; got %q, want br", got)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "precompressed" {
		t.Errorf("body should pass through unchanged; got %q", string(body))
	}
}

// =============================================================================
// C2 — bodyless response preserves status code (DELETE returns 204, not 200)
// =============================================================================
//
// Regression for the defer-commit path. A handler that calls WriteHeader
// + returns without writing any body MUST still see its status code on
// the wire. Before the fix, WriteHeader was deferred until first Write
// and bodyless handlers returned 200 because no Write ever triggered
// the deferred commit.

func TestCompressResponse_BodylessDelete_KeepsStatus204(t *testing.T) {
	// DELETE-style handler: WriteHeader(204), no body.
	server := buildResponseMiddleware(t, CompressResponse, map[string]http.HandlerFunc{
		"/fe/api/item": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	req, _ := http.NewRequest(http.MethodDelete, server.URL+"/fe/api/item", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204 (bodyless DELETE must not degrade to 200)", resp.StatusCode)
	}
	// Accept-Encoding: gzip + bodyless = no body to gzip; Content-Encoding
	// should NOT be set (gzip on a zero-length body is wasteful).
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("bodyless response must NOT be gzipped; got Content-Encoding=%q", got)
	}
	// Vary should still be set so downstream caches partition correctly
	// even though we didn't compress.
	if got := resp.Header.Get("Vary"); got != "Accept-Encoding" {
		t.Errorf("Vary = %q, want Accept-Encoding", got)
	}
}

func TestCompressResponse_BodylessHandler_DefaultIs200(t *testing.T) {
	// A bodyless handler that does not call WriteHeader at all (the
	// handler is so minimal it just returns) must still produce a
	// status line — default 200, even with Accept-Encoding: gzip.
	server := buildResponseMiddleware(t, CompressResponse, map[string]http.HandlerFunc{
		"/fe/api/empty": func(w http.ResponseWriter, r *http.Request) {},
	})
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/fe/api/empty", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (default for no WriteHeader call)", resp.StatusCode)
	}
}

// =============================================================================
// M3 — panic after partial gzip write does NOT produce a decodable 200
// =============================================================================
//
// Regression: a handler that writes ≥1 KiB (crosses the threshold and
// installs a gzip stream), then panics, MUST NOT leave a fully-closed
// gzip body on the wire. The recovery middleware's 500 must come
// after a TRUNCATED gzip stream so the gzip decoder on the client side
// returns an error rather than decoding random bytes.

func TestCompressResponse_PanicAfterGzipWrite_StreamIsTruncated(t *testing.T) {
	// Handler that installs gzip (writes ≥1 KiB) then panics.
	panicHandler := func(w http.ResponseWriter, r *http.Request) {
		// 8 KiB write to cross the 1 KiB threshold so the gzip
		// stream is installed.
		w.Write(bytes.Repeat([]byte("x"), 8*1024))
		panic("simulated downstream bug")
	}

	// Wrap with recoveryForTest so the panic is caught and a 500 is
	// written. This mirrors the production wiring:
	// recoveryMiddleware(...CompressResponse(mux)).
	stack := func(h http.Handler) http.Handler {
		return recoveryForTest(CompressResponse(h))
	}
	server := httptest.NewServer(stack(http.HandlerFunc(panicHandler)))
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Network error (truncated stream → broken pipe) is acceptable;
		// we mainly care that no decodable gzip body reaches the
		// client. If Do succeeded we'll try to read the body below.
		t.Logf("GET returned err (acceptable if truncated body): %v", err)
		if resp == nil {
			return
		}
	}

	if resp != nil {
		defer resp.Body.Close()
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			t.Logf("body read err (acceptable if stream truncated): %v", readErr)
		}
		// Attempt to gunzip the body. If this succeeds the test
		// fails — the client got a complete, decodable gzip body.
		gr, gzErr := gzip.NewReader(bytes.NewReader(body))
		if gzErr == nil {
			_, readAllErr := io.ReadAll(gr)
			gr.Close()
			if readAllErr == nil {
				t.Errorf("client received a DECODABLE gzip body after panic; M3 regression")
			} else {
				t.Logf("gzip header present but body read failed (acceptable truncation): %v", readAllErr)
			}
		} else {
			t.Logf("gzip.NewReader rejected the body (acceptable truncation): %v", gzErr)
		}
	}
}

// =============================================================================
// SWEEP — installGzip drops Content-Length; isCompressibleContentType is wired
// =============================================================================

func TestCompressResponse_InstallGzip_DropsContentLength(t *testing.T) {
	// Handler that sets a (deliberately wrong) Content-Length and
	// then writes a body >1 KiB. The middleware must DEL the
	// pre-existing Content-Length (the original 999999 was for the
	// uncompressed body); the stdlib server then auto-recomputes a
	// correct Content-Length for the gzipped body (or leaves it off
	// for streaming responses). Either way, the gzipped body MUST
	// NOT be truncated to the original 999999 — the test asserts the
	// body decodes correctly and is not 999999 bytes long.
	server := buildResponseMiddleware(t, CompressResponse, map[string]http.HandlerFunc{
		"/fe/api/length": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", "999999") // wildly wrong on purpose
			w.Write(bytes.Repeat([]byte("y"), 2*1024)) // 2 KiB body
		},
	})
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/fe/api/length", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", got)
	}
	if got := resp.Header.Get("Content-Length"); got == "999999" {
		t.Errorf("middleware must DEL the pre-existing Content-Length=999999; still present (%q)", got)
	}

	// The body must NOT be truncated to 999999 — gunzip and verify.
	body := gunzipBody(t, resp.Body)
	if len(body) == 999999 {
		t.Errorf("body was truncated to 999999 (the stale pre-existing Content-Length); installGzip did not DEL it")
	}
	if len(body) != 2*1024 {
		t.Errorf("decoded body length = %d, want %d (full 2 KiB)", len(body), 2*1024)
	}
}

func TestCompressResponse_NonCompressibleContentType_Bypasses(t *testing.T) {
	// /fe/api/* image/png handler: middleware must NOT compress
	// (binary types pass through uncompressed; gzipping them is
	// wasteful and often produces larger output).
	server := buildResponseMiddleware(t, CompressResponse, map[string]http.HandlerFunc{
		"/fe/api/image": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			// PNG signature + some bytes (well above the 1 KiB threshold)
			w.Write(bytes.Repeat([]byte{0x89, 0x50, 0x4e, 0x47}, 512)) // 2 KiB of PNG bytes
		},
	})
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/fe/api/image", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("image/png must NOT be gzipped; got Content-Encoding=%q", got)
	}
	if got := resp.Header.Get("Vary"); got != "Accept-Encoding" {
		t.Errorf("Vary = %q, want Accept-Encoding (still partition even though not compressed)", got)
	}
}

func TestIsCompressibleContentType(t *testing.T) {
	cases := []struct {
		ct   string
		want bool
	}{
		{"", true}, // unknown → assume compressible (default)
		{"application/json", true},
		{"application/json; charset=utf-8", true},
		{"text/plain", true},
		{"text/html", true},
		{"text/event-stream", true},
		{"application/xml", true},
		{"application/javascript", true},
		{"image/svg+xml", true},
		{"image/png", false},
		{"image/jpeg", false},
		{"application/octet-stream", false},
		{"video/mp4", false},
		{"application/pdf", false},
	}
	for _, tc := range cases {
		if got := isCompressibleContentType(tc.ct); got != tc.want {
			t.Errorf("isCompressibleContentType(%q) = %v, want %v", tc.ct, got, tc.want)
		}
	}
}

// =============================================================================
// Helpers for the panic-interaction test
// =============================================================================

// recoveryForTest is a local copy of cmd/main.go's recoveryMiddleware
// kept here so the test doesn't reach into the cmd package. It catches
// panics and writes a plain 500 with http.Error.
func recoveryForTest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}