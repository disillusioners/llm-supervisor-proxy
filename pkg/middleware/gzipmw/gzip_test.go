package gzipmw

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// HTTP/2 note (scope deferral)
// ─────────────────────────────────────────────────────────────────────────────
// The tests in this file exercise the middleware over HTTP/1.1 via
// httptest.NewServer. HTTP/2 header handling is reasoned-safe but not
// exercised here: Go's net/http2 server re-canonicalizes header keys
// (textproto.CanonicalMIMEHeaderKey) and the middleware's
// r.Header.Del/Set calls operate on the post-canonicalization map,
// so the same TrimSpace + ToLower logic applies uniformly across
// HTTP/1.1 and HTTP/2. Direct httptest with TLS+http2 wiring is a
// future-work item; the security review approved deferral.

// ─────────────────────────────────────────────────────────────────────────────
// Test helpers
// ─────────────────────────────────────────────────────────────────────────────

// gzipBytes returns the gzip-compressed form of plain.
func gzipBytes(t *testing.T, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(plain); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// captureHandler is the downstream handler the middleware wraps. It
// captures the body, Content-Length header, r.ContentLength, and
// Content-Encoding header of the request as seen AFTER the middleware
// has run.
type captureHandler struct {
	body               []byte
	contentLengthHdr   string
	rContentLength     int64
	contentEncodingHdr string
	allHeaders         http.Header
}

func (c *captureHandler) snapshot(r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c.body = body
	c.contentLengthHdr = r.Header.Get(headerContentLength)
	c.rContentLength = r.ContentLength
	c.contentEncodingHdr = r.Header.Get(headerContentEncoding)
	c.allHeaders = r.Header.Clone()
}

// newCapturingServer wraps DecompressRequest around a handler that
// records the inbound request and writes a small JSON echo so the
// caller can assert on the response status / body.
func newCapturingServer() (*httptest.Server, *captureHandler) {
	captured := &captureHandler{}
	mw := DecompressRequest(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.snapshot(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	srv := httptest.NewServer(mw)
	return srv, captured
}

// ─────────────────────────────────────────────────────────────────────────────
// Positive path: gzip body decompresses, headers are rewritten.
// ─────────────────────────────────────────────────────────────────────────────

func TestDecompressRequest_GzipBodyDecompressedAndHeadersFixed(t *testing.T) {
	plain := []byte(`{"model":"gpt-x","messages":[{"role":"user","content":"hi"}]}`)
	compressed := gzipBytes(t, plain)

	srv, captured := newCapturingServer()
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerContentEncoding, encodingGzip)
	req.ContentLength = int64(len(compressed))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !bytes.Equal(captured.body, plain) {
		t.Errorf("downstream body = %q, want %q", captured.body, plain)
	}
	if captured.contentEncodingHdr != "" {
		t.Errorf("Content-Encoding = %q, want empty (header should be stripped)", captured.contentEncodingHdr)
	}
	if got := captured.contentLengthHdr; got != strconv.FormatInt(int64(len(plain)), 10) {
		t.Errorf("Content-Length header = %q, want %q", got, strconv.FormatInt(int64(len(plain)), 10))
	}
	if captured.rContentLength != int64(len(plain)) {
		t.Errorf("r.ContentLength = %d, want %d", captured.rContentLength, len(plain))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Pass-through paths.
// ─────────────────────────────────────────────────────────────────────────────

func TestDecompressRequest_NoHeader_RequestsUntouched(t *testing.T) {
	plain := []byte(`{"model":"gpt-x","messages":[]}`)

	srv, captured := newCapturingServer()
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", bytes.NewReader(plain))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = int64(len(plain))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !bytes.Equal(captured.body, plain) {
		t.Errorf("downstream body = %q, want %q", captured.body, plain)
	}
	// r.ContentLength is left as the stdlib set it (int64(len(plain))
	// because the middleware is a no-op). r.Header must NOT have
	// had Content-Encoding added by the middleware.
	if _, has := captured.allHeaders[headerContentEncoding]; has {
		t.Errorf("Content-Encoding should not be set on pass-through request, got %v",
			captured.allHeaders[headerContentEncoding])
	}
}

func TestDecompressRequest_Identity_RequestsUntouched(t *testing.T) {
	plain := []byte(`{"model":"gpt-x"}`)

	srv, captured := newCapturingServer()
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", bytes.NewReader(plain))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerContentEncoding, encodingIdentity)
	req.ContentLength = int64(len(plain))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !bytes.Equal(captured.body, plain) {
		t.Errorf("downstream body = %q, want %q", captured.body, plain)
	}
}

func TestDecompressRequest_IdentityCaseInsensitive_RequestsUntouched(t *testing.T) {
	plain := []byte(`{"model":"gpt-x"}`)

	srv, captured := newCapturingServer()
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", bytes.NewReader(plain))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerContentEncoding, "Identity") // mixed case
	req.ContentLength = int64(len(plain))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !bytes.Equal(captured.body, plain) {
		t.Errorf("downstream body = %q, want %q", captured.body, plain)
	}
}

func TestDecompressRequest_EmptyContentEncodingValue_Passthrough(t *testing.T) {
	plain := []byte(`{"model":"gpt-x"}`)

	srv, captured := newCapturingServer()
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", bytes.NewReader(plain))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerContentEncoding, "")
	req.ContentLength = int64(len(plain))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !bytes.Equal(captured.body, plain) {
		t.Errorf("downstream body = %q, want %q", captured.body, plain)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Negative paths: corrupt gzip, oversize bomb, unsupported encodings.
// ─────────────────────────────────────────────────────────────────────────────

func TestDecompressRequest_CorruptGzip_Returns400WithEnvelope(t *testing.T) {
	srv, _ := newCapturingServer()
	defer srv.Close()

	// Bytes that LOOK like they could be gzip (magic 1f 8b) but
	// are not a valid stream.
	corrupt := []byte{0x1f, 0x8b, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", bytes.NewReader(corrupt))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(headerContentEncoding, encodingGzip)
	req.ContentLength = int64(len(corrupt))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	assertOpenAIErrorEnvelope(t, resp, http.StatusBadRequest, "invalid_gzip_body")
}

func TestDecompressRequest_NotActuallyGzip_Returns400WithEnvelope(t *testing.T) {
	srv, _ := newCapturingServer()
	defer srv.Close()

	// Plain JSON, but with Content-Encoding: gzip claim.
	plain := []byte(`{"model":"gpt-x"}`)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", bytes.NewReader(plain))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(headerContentEncoding, encodingGzip)
	req.ContentLength = int64(len(plain))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	assertOpenAIErrorEnvelope(t, resp, http.StatusBadRequest, "invalid_gzip_body")
}

// TestDecompressRequest_OversizedBomb_Returns413 verifies that a gzip
// payload that would decompress to more than MaxDecompressedBytes is
// rejected with 413 without buffering the full bomb. We construct a
// payload that is just over the cap by compressing a payload of
// MaxDecompressedBytes+1024 bytes.
func TestDecompressRequest_OversizedBomb_Returns413(t *testing.T) {
	srv, _ := newCapturingServer()
	defer srv.Close()

	// 100 MB + 1 KiB decompressed. Use a high-entropy byte pattern
	// to avoid gzip's literal-block compression collapsing it; a
	// sequence of "0123456789abcdef" repeating is sufficient.
	over := MaxDecompressedBytes + 1024
	plain := make([]byte, over)
	const seed = "0123456789abcdef"
	for i := range plain {
		plain[i] = seed[i%len(seed)]
	}
	compressed := gzipBytes(t, plain)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(headerContentEncoding, encodingGzip)
	req.ContentLength = int64(len(compressed))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	assertOpenAIErrorEnvelope(t, resp, http.StatusRequestEntityTooLarge, "request_body_too_large")
}

func TestDecompressRequest_AtExactCapBoundary_Succeeds(t *testing.T) {
	// The cap is "exactly MaxDecompressedBytes" — bodies AT the cap
	// should succeed. We compress a small payload (<< cap) to keep
	// the test fast; the boundary semantics are exercised by the
	// previous test (which asserts cap+1 fails).
	srv, captured := newCapturingServer()
	defer srv.Close()

	plain := []byte(strings.Repeat("a", MaxDecompressedBytes))
	compressed := gzipBytes(t, plain)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(headerContentEncoding, encodingGzip)
	req.ContentLength = int64(len(compressed))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%q", resp.StatusCode, body)
	}
	if int64(len(captured.body)) != int64(len(plain)) {
		t.Errorf("downstream body length = %d, want %d", len(captured.body), len(plain))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Unsupported encodings (single-value and stacked).
// ─────────────────────────────────────────────────────────────────────────────

func TestDecompressRequest_UnsupportedEncoding_Returns415(t *testing.T) {
	cases := []struct {
		name string
		val  string
	}{
		{"deflate", "deflate"},
		{"br", "br"},
		{"zstd", "zstd"},
		{"compress", "compress"},
		{"uppercase-GZIPP", "GZIPP"},           // GZIPP is not gzip; case-fold != "gzip"
		{"token-with-internal-space", "gz ip"}, // internal whitespace is not part of any token
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newCapturingServer()
			defer srv.Close()

			req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions",
				bytes.NewReader([]byte(`{"x":1}`)))
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			req.Header.Set(headerContentEncoding, tc.val)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			defer resp.Body.Close()

			assertOpenAIErrorEnvelope(t, resp, http.StatusUnsupportedMediaType, "unsupported_encoding")
		})
	}
}

// TestDecompressRequest_GzipCaseInsensitive verifies that "GZIP",
// "Gzip", etc. all activate the decompression path (they should,
// because RFC 7231 says encoding tokens are case-insensitive). Also
// verifies that OWS (optional whitespace — leading/trailing space or
// tab) per RFC 7230 §3.2.3 is tolerated by TrimSpace before the
// case-fold comparison.
func TestDecompressRequest_GzipCaseInsensitive(t *testing.T) {
	plain := []byte(`{"model":"x","messages":[]}`)
	compressed := gzipBytes(t, plain)

	for _, tc := range []struct {
		name string
		val  string
	}{
		{"gzip", "gzip"},
		{"GZIP", "GZIP"},
		{"Gzip", "Gzip"},
		{"gZip", "gZip"},
		{"GZip", "GZip"},
		{"leading-trailing-space", " gzip "},
		{"leading-tab", "\tgzip"},
		{"trailing-tab", "gzip\t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, captured := newCapturingServer()
			defer srv.Close()

			req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions",
				bytes.NewReader(compressed))
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			req.Header.Set(headerContentEncoding, tc.val)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d, want 200; body=%q", resp.StatusCode, body)
			}
			if !bytes.Equal(captured.body, plain) {
				t.Errorf("downstream body mismatch for variant %q", tc.val)
			}
		})
	}
}

func TestDecompressRequest_StackedEncoding_Returns400(t *testing.T) {
	// A single header value containing a comma-separated list — we
	// reject rather than attempt to peel because (a) the spec is
	// ambiguous on chained encoding semantics and (b) no caller in
	// the proxy expects to handle a chain.
	srv, _ := newCapturingServer()
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions",
		bytes.NewReader([]byte(`{"x":1}`)))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(headerContentEncoding, "gzip, br")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	assertOpenAIErrorEnvelope(t, resp, http.StatusBadRequest, "unsupported_encoding")
}

// TestDecompressRequest_StackedGzipRepeated_Returns400 verifies that a
// single Content-Encoding header line containing the SAME encoding
// stacked ("gzip, gzip") is rejected via the comma/stacked branch
// with 400 unsupported_encoding — distinct from the repeated-header
// line case above, which is rejected via the multi-value branch.
func TestDecompressRequest_StackedGzipRepeated_Returns400(t *testing.T) {
	srv, _ := newCapturingServer()
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions",
		bytes.NewReader([]byte(`{"x":1}`)))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(headerContentEncoding, "gzip, gzip")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	assertOpenAIErrorEnvelope(t, resp, http.StatusBadRequest, "unsupported_encoding")
}

// TestDecompressRequest_ChunkedBody_DecompressedAndHeadersFixed
// verifies the success path for a request that arrives without a
// Content-Length (ContentLength = -1), forcing the Go client to use
// Transfer-Encoding: chunked on the wire. After decompression:
//   - the request succeeds with 200,
//   - the downstream handler observes the decompressed body,
//   - r.ContentLength is set to the decompressed byte count,
//   - the Content-Length header reflects the decompressed byte count,
//   - Transfer-Encoding is absent from r.Header (fix #1 invariant).
func TestDecompressRequest_ChunkedBody_DecompressedAndHeadersFixed(t *testing.T) {
	plain := []byte(`{"model":"gpt-x","messages":[{"role":"user","content":"hi"}]}`)
	compressed := gzipBytes(t, plain)

	srv, captured := newCapturingServer()
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions",
		bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerContentEncoding, encodingGzip)
	// Force chunked transfer encoding on the wire by clearing
	// ContentLength; the Go client will then send
	// Transfer-Encoding: chunked.
	req.ContentLength = -1
	req.Header.Del("Content-Length")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%q", resp.StatusCode, body)
	}
	if !bytes.Equal(captured.body, plain) {
		t.Errorf("downstream body = %q, want %q", captured.body, plain)
	}
	if captured.rContentLength != int64(len(plain)) {
		t.Errorf("r.ContentLength = %d, want %d", captured.rContentLength, len(plain))
	}
	if got := captured.contentLengthHdr; got != strconv.Itoa(len(plain)) {
		t.Errorf("Content-Length header = %q, want %q", got, strconv.Itoa(len(plain)))
	}
	if vals, ok := captured.allHeaders["Transfer-Encoding"]; ok && len(vals) > 0 {
		t.Errorf("Transfer-Encoding should be absent after decompression, got %v", vals)
	}
}

func TestDecompressRequest_RepeatedHeaderLine_Returns400(t *testing.T) {
	// Two distinct header lines with the same encoding. net/http
	// exposes this as a 2-element slice on r.Header[...].
	srv, _ := newCapturingServer()
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions",
		bytes.NewReader([]byte(`{"x":1}`)))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Add(headerContentEncoding, encodingGzip)
	req.Header.Add(headerContentEncoding, encodingGzip)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	assertOpenAIErrorEnvelope(t, resp, http.StatusBadRequest, "unsupported_encoding")
}

// TestDecompressRequest_MidStreamCorruption_Returns400 verifies that a
// VALID gzip header followed by a corrupted middle byte (gzip CRC
// checksum will fail on decompress, OR the DEFLATE block will fail
// to decode) is rejected with 400 invalid_gzip_body. This covers the
// failure mode that slipping past gzip.NewReader — corrupt payload,
// not corrupt header.
func TestDecompressRequest_MidStreamCorruption_Returns400(t *testing.T) {
	srv, _ := newCapturingServer()
	defer srv.Close()

	plain := []byte(`{"model":"x","messages":[]}`)
	compressed := gzipBytes(t, plain)

	// Flip a byte in the middle of the compressed payload. The gzip
	// header (first 10 bytes) and footer (CRC32+ISIZE, last 8 bytes)
	// stay intact; flipping a byte somewhere in between forces the
	// DEFLATE decoder and/or the trailing CRC check to fail.
	mid := len(compressed) / 2
	compressed[mid] ^= 0xFF

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions",
		bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(headerContentEncoding, encodingGzip)
	req.ContentLength = int64(len(compressed))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	assertOpenAIErrorEnvelope(t, resp, http.StatusBadRequest, "invalid_gzip_body")
}

// TestDecompressRequest_TruncatedTail_Returns400 verifies that a gzip
// stream with its trailing bytes removed (CRC32/ISIZE footer
// truncated) is rejected with 400 invalid_gzip_body. A truncated
// stream that decodes completely but has no checksum will return
// ErrUnexpectedEOF when the gzip reader reaches its expected end.
func TestDecompressRequest_TruncatedTail_Returns400(t *testing.T) {
	srv, _ := newCapturingServer()
	defer srv.Close()

	plain := []byte(`{"model":"x","messages":[]}`)
	compressed := gzipBytes(t, plain)

	// Drop the last 4 bytes — half of the 8-byte CRC32 + ISIZE
	// footer. The DEFLATE block stays complete; only the
	// trailer is short, so gzip.Reader returns ErrUnexpectedEOF
	// when it tries to read what it expects to be there.
	truncated := compressed[:len(compressed)-4]
	if len(truncated) >= len(compressed) {
		t.Fatalf("truncation produced no change: len=%d", len(truncated))
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions",
		bytes.NewReader(truncated))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(headerContentEncoding, encodingGzip)
	req.ContentLength = int64(len(truncated))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	assertOpenAIErrorEnvelope(t, resp, http.StatusBadRequest, "invalid_gzip_body")
}

// TestDecompressRequest_GETWithGzipHeaderNoBody_Returns400 verifies
// that a GET request advertising Content-Encoding: gzip with no body
// is rejected with 400 invalid_gzip_body. gzip.NewReader on
// http.NoBody (or any empty reader) fails to read the gzip magic
// header and returns an error before we ever enter the decompression
// loop.
func TestDecompressRequest_GETWithGzipHeaderNoBody_Returns400(t *testing.T) {
	srv, _ := newCapturingServer()
	defer srv.Close()

	// http.NewRequest with a nil body leaves r.Body == http.NoBody.
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/chat/completions", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(headerContentEncoding, encodingGzip)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	assertOpenAIErrorEnvelope(t, resp, http.StatusBadRequest, "invalid_gzip_body")
}

// TestDecompressRequest_RepeatedHeaderClosesBody verifies that the
// repeated-Content-Encoding-line reject path (gzip.go ~:167) closes
// the original body before writing the error envelope. This is the
// body-close invariant extended to the multi-value header branch.
func TestDecompressRequest_RepeatedHeaderClosesBody(t *testing.T) {
	rec := &closeRecorder{ReadCloser: io.NopCloser(bytes.NewReader([]byte(`{"x":1}`)))}
	req := httptest.NewRequest(http.MethodPost, "/anything", rec)
	// Two distinct header lines with the same encoding — net/http
	// surfaces this as a 2-element slice.
	req.Header.Add(headerContentEncoding, encodingGzip)
	req.Header.Add(headerContentEncoding, encodingGzip)
	req.ContentLength = int64(len(`{"x":1}`))

	rw := httptest.NewRecorder()
	DecompressRequest(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})).ServeHTTP(rw, req)

	if rw.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rw.Code)
	}
	if !rec.closed {
		t.Errorf("original body was not closed on the repeated-header reject path")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Body-close invariants: the original (compressed) body is closed on
// every code path so we never leak the connection's read half.
// ─────────────────────────────────────────────────────────────────────────────

func TestDecompressRequest_BodyClosedOnAllPaths(t *testing.T) {
	// Wrap r.Body in a body that records Close() calls; the middleware
	// must close the ORIGINAL body when it swaps r.Body (success
	// path) or rejects with an error (failure paths).
	cases := []struct {
		name       string
		encoding   string
		body       []byte
		wantStatus int
	}{
		{"success", encodingGzip, gzipBytes(t, []byte(`{"ok":1}`)), http.StatusOK},
		{"corrupt", encodingGzip, []byte{0x1f, 0x8b, 0x00, 0x00}, http.StatusBadRequest},
		{"deflate", "deflate", []byte(`{"x":1}`), http.StatusUnsupportedMediaType},
		{"stacked", "gzip, br", []byte(`{"x":1}`), http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &closeRecorder{ReadCloser: io.NopCloser(bytes.NewReader(tc.body))}
			req := httptest.NewRequest(http.MethodPost, "/anything", rec)
			req.Header.Set(headerContentEncoding, tc.encoding)
			req.ContentLength = int64(len(tc.body))

			rw := httptest.NewRecorder()
			DecompressRequest(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			})).ServeHTTP(rw, req)

			if rw.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rw.Code, tc.wantStatus)
			}
			if !rec.closed {
				t.Errorf("original body was not closed")
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal sanity tests for the writeClientError envelope shape.
// ─────────────────────────────────────────────────────────────────────────────

func TestWriteClientError_EnvelopeShape(t *testing.T) {
	rw := httptest.NewRecorder()
	writeClientError(rw, http.StatusBadRequest, "invalid_request_error", "bad_gzip", "boom")

	if rw.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rw.Code)
	}
	if ct := rw.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if cl := rw.Header().Get("Content-Length"); cl == "" {
		t.Errorf("Content-Length header should be set")
	}
	var env errorEnvelope
	if err := json.NewDecoder(rw.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Type != "invalid_request_error" {
		t.Errorf("error.type = %q, want invalid_request_error", env.Error.Type)
	}
	if env.Error.Code != "bad_gzip" {
		t.Errorf("error.code = %q, want bad_gzip", env.Error.Code)
	}
	if env.Error.Message != "boom" {
		t.Errorf("error.message = %q, want boom", env.Error.Message)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// assertOpenAIErrorEnvelope asserts that the response carries the
// expected HTTP status, Content-Type application/json, and a JSON body
// shaped like the proxy's existing OpenAI error envelope
// (`{"error":{"type":...,"code":...,"message":...}}`), with the
// expected error.code.
func assertOpenAIErrorEnvelope(t *testing.T, resp *http.Response, wantStatus int, wantCode string) {
	t.Helper()
	if resp.StatusCode != wantStatus {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want %d; body = %q", resp.StatusCode, wantStatus, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var env errorEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != wantCode {
		t.Errorf("error.code = %q, want %q", env.Error.Code, wantCode)
	}
	if env.Error.Message == "" {
		t.Errorf("error.message should be non-empty")
	}
}

// closeRecorder wraps an io.ReadCloser to record whether Close was
// called. Used to assert the middleware closes the original body on
// every code path.
type closeRecorder struct {
	io.ReadCloser
	closed bool
}

func (c *closeRecorder) Close() error {
	c.closed = true
	if c.ReadCloser != nil {
		return c.ReadCloser.Close()
	}
	return nil
}
