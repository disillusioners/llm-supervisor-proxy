// Package gzipmw provides a transparent request-body decompression
// middleware for llm-supervisor-proxy.
//
// The middleware is applied at the composition root (cmd/main.go) so it
// sits BEFORE every handler — proxy, ultimatemodel, UI, MCP. When a
// client sends Content-Encoding: gzip, the middleware decompresses the
// body in place (capped at MaxDecompressedBytes) and rewrites the
// request so downstream code sees an ordinary uncompressed request:
//
//	r.Body           = io.NopCloser(bytes.NewReader(decompressed))
//	r.ContentLength  = int64(len(decompressed))
//	r.Header.Set("Content-Length", ...)
//	r.Header.Del("Content-Encoding")
//
// Requests without the header (or with `identity`) pass through
// untouched. The middleware emits client-facing JSON errors in the
// proxy's OpenAI-compatible envelope shape ({"error":{"type":...,
// "code":...,"message":...}}) so 4xx responses match the format used
// by handler.sendError (pkg/proxy/handler.go:357) and friends.
//
// Import path is github.com/disillusioners/llm-supervisor-proxy/pkg/middleware/gzipmw
// (the package name is `gzipmw`, deliberately, to avoid colliding with
// the stdlib `compress/gzip` package which this file imports).
package gzipmw

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// MaxDecompressedBytes caps the size of a decompressed request body
// to defend against zip-bomb attacks.
//
// Parity story (with the proxy's existing per-path body caps of 10MB
// in adapter_openai.go:34, handler_anthropic.go:51, handler_functions.go:31):
//   - For uncompressed bodies up to 10MB and gzip bodies whose
//     decompressed size is up to 10MB, behavior is byte-for-byte
//     identical to the no-header path.
//   - For gzip bodies whose decompressed size is 10MB..100MB, the
//     proxy accepts the request (an equivalent uncompressed request
//     would have already failed with the existing 10MB cap); this is
//     the deliberate trade-off — gzip payloads can be 5-10x smaller
//     than decompressed, so allowing up to 100MB of decompressed data
//     lets clients ship large conversation histories that an
//     uncompressed equivalent could not.
//   - For decompressed bodies over MaxDecompressedBytes the middleware
//     returns 413 Payload Too Large WITHOUT fully buffering the bomb.
//     Decompression streams through io.LimitReader(gz,
//     MaxDecompressedBytes+1); io.ReadAll then returns at most
//     MaxDecompressedBytes+1 decompressed bytes regardless of the
//     underlying gzip stream length, and we use an explicit
//     len(decompressed) > MaxDecompressedBytes check to produce
//     the 413. The in-memory cap is MaxDecompressedBytes+1.
const MaxDecompressedBytes = 100 * 1024 * 1024 // 100 MB

// Content-Encoding header constants (canonical MIME form per net/http).
const (
	headerContentEncoding = "Content-Encoding"
	headerContentLength   = "Content-Length"
	encodingGzip          = "gzip"
	encodingIdentity      = "identity"
)

// errorEnvelope matches the OpenAI-compatible error shape used by
// pkg/proxy.handler.sendError (pkg/proxy/handler.go:357) and
// pkg/models.NewOpenAIError. We use this from middleware-level errors
// because no Handler instance exists yet at the entry seam — the
// middleware fires BEFORE the mux dispatches to a handler, so it
// cannot call h.sendError.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// writeClientError writes the OpenAI-shape error envelope with the
// given HTTP status. It sets Content-Type: application/json and
// Content-Length so the client can parse the body cleanly.
func writeClientError(w http.ResponseWriter, status int, errType, code, message string) {
	body := errorEnvelope{Error: errorBody{Type: errType, Code: code, Message: message}}
	encoded, err := json.Marshal(body)
	if err != nil {
		// Fallback to plain text if JSON marshalling itself fails —
		// this should never happen for our static struct shape, but
		// we never want the middleware to panic.
		http.Error(w, message, status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
	w.WriteHeader(status)
	_, _ = w.Write(encoded)
}

// DecompressRequest returns an http.Handler middleware that transparently
// decompresses gzip-encoded request bodies before delegating to next.
//
// The middleware is intentionally minimal and conditional: it activates
// ONLY when r.Header.Get("Content-Encoding") is present and resolves to
// "gzip" (case-insensitive). The following values pass through
// untouched (no body rewrite, no header changes):
//
//   - Header absent (the common case).
//   - "identity" (HTTP/1.1 default; per RFC 7231 the proxy should
//     treat it as no encoding).
//
// The following values are rejected with a 4xx client error envelope:
//
//   - "deflate", "br", "zstd", or any other unsupported encoding →
//     415 Unsupported Media Type. The proxy only speaks gzip; we
//     refuse rather than silently pass through, because passing a
//     compressed body downstream would break every reader.
//   - Multiple stacked encodings like "gzip, br" or repeated header
//     lines like "gzip, gzip" → 400 Bad Request. We do not attempt
//     to peel a chain because (a) the spec is ambiguous on the
//     ordering semantics of multi-valued Content-Encoding and (b)
//     no caller in the proxy expects to handle a chained encoding.
//
// Errors:
//
//   - Corrupt / non-gzip body bytes (gzip header present but payload
//     fails the gzip checksum) → 400 Bad Request.
//   - Decompressed body exceeds MaxDecompressedBytes → 413 Payload
//     Too Large. The cap is enforced via io.LimitReader so the
//     bomb is not fully buffered into memory; on overflow we
//     close the original body and write the error envelope.
//
// Ordering with recoveryMiddleware: this middleware is intentionally
// safe to place either INSIDE or OUTSIDE recoveryMiddleware. It
// performs no work that could panic on ordinary input (gzip.NewReader
// and io.Copy are panic-free for non-malformed inputs; malformed
// inputs produce errors that we handle explicitly). Recovery would
// only fire on a bug inside compress/gzip itself, in which case the
// request still gets a 500 instead of crashing the process — placing
// this middleware INSIDE recoveryMiddleware (i.e. with recovery as
// the OUTER wrapper) gives that safety net; the production wiring in
// cmd/main.go follows that order:
//
//	srv.Handler = recoveryMiddleware(gzipmw.DecompressRequest(mux))
func DecompressRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Fast path: no Content-Encoding header at all. This is the
		// overwhelmingly common case — every request without gzip
		// must take this branch with zero allocations and zero
		// header inspection cost.
		rawValues, hasHeader := r.Header[headerContentEncoding]
		if !hasHeader || len(rawValues) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		// Reject repeated Content-Encoding lines (e.g. two
		// `Content-Encoding: gzip` headers) BEFORE we inspect
		// values. net/http keeps each repeated header LINE as a
		// separate element of the values slice (it does NOT
		// concatenate multi-values with ", "); Header.Get returns
		// only the first value, which is why we must index the map
		// directly here. A literal repeat of the SAME encoding on
		// two distinct header lines is still semantically
		// suspicious — clients should send a single header. We
		// reject so we never have to guess.
		if len(rawValues) > 1 {
			_ = r.Body.Close()
			writeClientError(w, http.StatusBadRequest,
				"invalid_request_error", "unsupported_encoding",
				"multiple Content-Encoding header values are not supported")
			return
		}

		// Single header value. Trim whitespace; the stdlib does
		// not trim header values for us.
		enc := strings.TrimSpace(rawValues[0])
		if enc == "" {
			// RFC 7231: an empty Content-Encoding value is
			// equivalent to no encoding (i.e. "identity"). Pass
			// through.
			next.ServeHTTP(w, r)
			return
		}

		// Reject multi-codec stacks BEFORE we check the
		// canonical token. The spec is ambiguous on chained
		// encoding semantics and no caller in the proxy expects
		// to peel a chain — a stacked value gets 400, not 415,
		// because the client did something structurally invalid
		// rather than asking for an encoding we don't speak.
		if strings.Contains(enc, ",") {
			_ = r.Body.Close()
			writeClientError(w, http.StatusBadRequest,
				"invalid_request_error", "unsupported_encoding",
				"multiple Content-Encoding values are not supported; send a single encoding")
			return
		}

		// Canonicalize: tokens are case-insensitive per RFC 7231.
		encLower := strings.ToLower(enc)

		// identity / no-op passthrough.
		if encLower == encodingIdentity {
			next.ServeHTTP(w, r)
			return
		}

		// Only gzip is supported. Anything else (deflate, br,
		// zstd, ...) is rejected with 415. We use case-insensitive
		// comparison so "GZIP" / "Gzip" / "gzip" are all accepted;
		// anything outside that whitelist — including misspellings
		// like "GZIp" — falls through to 415.
		if encLower != encodingGzip {
			_ = r.Body.Close()
			writeClientError(w, http.StatusUnsupportedMediaType,
				"invalid_request_error", "unsupported_encoding",
				fmt.Sprintf("Content-Encoding %q is not supported; only %q is accepted",
					enc, encodingGzip))
			return
		}

		// Decompression path. Wrap the original body in a
		// LimitReader that allows MaxDecompressedBytes+1 bytes so
		// the gzip reader returns io.ErrUnexpectedEOF (or our
		// own io.EOF after we've already detected the overflow)
		// as soon as the cap is exceeded. We must not fully
		// buffer the bomb into memory.
		//
		// Edge case: a valid gzip stream whose COMPRESSED size
		// alone exceeds MaxDecompressedBytes surfaces as 400
		// invalid_gzip_body (ErrUnexpectedEOF from the gzip
		// reader, triggered when the compressed-side LimitReader
		// truncates the stream mid-record) rather than 413.
		// Acceptable because JSON is highly compressible and the
		// reject behavior is identical.
		limited := io.LimitReader(r.Body, MaxDecompressedBytes+1)

		gz, err := gzip.NewReader(limited)
		if err != nil {
			// Common case: client claimed gzip but the bytes
			// are not actually a gzip stream. Close the body so
			// the connection can be reused and return 400.
			_ = r.Body.Close()
			writeClientError(w, http.StatusBadRequest,
				"invalid_request_error", "invalid_gzip_body",
				"failed to decompress request body: not a valid gzip stream")
			return
		}
		// gzip.Reader MUST be closed to release internal state;
		// close-on-success is the documented idiom.
		defer gz.Close()

		// Read into an io.LimitReader-bounded buffer so the cap
		// check is exact: io.ReadAll(io.LimitReader(gz,
		// MaxDecompressedBytes+1)) returns at most
		// MaxDecompressedBytes+1 decompressed bytes regardless of
		// the underlying gzip stream length, and the explicit
		// len(decompressed) > MaxDecompressedBytes check below
		// distinguishes pass (<=cap) from 413 (>cap).
		decompressed, readErr := io.ReadAll(io.LimitReader(gz, MaxDecompressedBytes+1))
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			_ = r.Body.Close()
			writeClientError(w, http.StatusBadRequest,
				"invalid_request_error", "invalid_gzip_body",
				"failed to decompress request body: "+readErr.Error())
			return
		}
		if int64(len(decompressed)) > MaxDecompressedBytes {
			// The gzip stream produced more bytes than the cap
			// allows. Drop the bomb and reply 413 — do NOT
			// substitute the truncated buffer into the
			// request, that would let a malicious client smuggle
			// arbitrarily large payloads past the cap.
			_ = r.Body.Close()
			writeClientError(w, http.StatusRequestEntityTooLarge,
				"invalid_request_error", "request_body_too_large",
				fmt.Sprintf("decompressed request body exceeds %d bytes",
					MaxDecompressedBytes))
			return
		}

		// Successful decompression. Close the original
		// (compressed) body so we don't leak the underlying
		// connection's read half, then swap r.Body with a fresh
		// reader over the decompressed bytes.
		_ = r.Body.Close()

		r.Body = io.NopCloser(bytes.NewReader(decompressed))
		r.ContentLength = int64(len(decompressed))

		// Rewrite the headers so downstream code sees an
		// ordinary uncompressed request. Content-Length must be
		// set as a canonical MIME header key (net/http canonicalizes
		// via textproto.CanonicalMIMEHeaderKey on Set/Get — both
		// forms are safe to Set).
		r.Header.Del(headerContentEncoding)
		r.Header.Set(headerContentLength, strconv.Itoa(len(decompressed)))

		next.ServeHTTP(w, r)
	})
}
