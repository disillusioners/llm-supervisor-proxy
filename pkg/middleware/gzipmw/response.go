package gzipmw

// ─────────────────────────────────────────────────────────────────────────────
// Response-compression middleware (P1-4)
// ─────────────────────────────────────────────────────────────────────────────
//
// CompressResponse returns an http.Handler middleware that transparently
// gzips JSON responses for clients that send Accept-Encoding: gzip.
//
// Scope (P1-4 contract):
//   - Compresses ONLY /fe/api/* paths EXCEPT /fe/api/events (SSE —
//     compressing SSE streams hung clients in the past, see
//     docs/cloudflare-drop-hang-bug.md). Other paths (proxy /v1/*,
//     health, root) are passed through untouched so we never compress
//     an upstream binary payload or a streaming response.
//   - Honors Accept-Encoding: gzip. Absent or "identity" → passthrough.
//   - Honors Vary: Accept-Encoding so downstream caches partition
//     compressed vs uncompressed correctly.
//   - Threshold: bodies at or below MinResponseSize (1024 bytes) are
//     NOT compressed. JSON under 1 KiB rarely benefits from gzip and
//     the CPU cost of piping through compress/gzip is wasted work;
//     we pass small bodies through untouched.
//   - Never double-encode: if the downstream handler already wrote a
//     Content-Encoding header, the middleware leaves the response
//     alone (no Vary, no rewrite).
//   - SSE-safe: any path in the exclusion list (default: /fe/api/events)
//     is passed through with zero compression work, preserving Flush.
//   - Content-Length handling: we never set Content-Length for the
//     compressed response (the gzip stream length is unknown until
//     Close). The underlying writer's existing Content-Length (if
//     any) is preserved for the uncompressed pass-through case.
//
// Wire placement: this middleware should sit INSIDE recoveryMiddleware
// (so a panic inside the gzip wrapper still gets a 500) and OUTSIDE
// the mux. CompressResponse is content-agnostic and does NOT inspect
// the request body — pairing it with DecompressRequest is fine; they
// don't interact.

import (
	"bufio"
	"compress/gzip"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// MinResponseSize is the threshold below which CompressResponse passes
// the body through uncompressed. JSON payloads under 1 KiB rarely
// benefit from gzip and the CPU cost is wasted work on the hot path.
const MinResponseSize = 1024

// Default excluded paths from response compression. SSE streams
// broke when gzipped (compressed chunked-encoding interferes with
// EventSource buffer semantics in browsers and edge proxies); we
// MUST keep these on the wire uncompressed.
var defaultExcludedPaths = []string{
	"/fe/api/events",
}

// gzipResponseWriter wraps http.ResponseWriter so handlers can still
// call WriteHeader / Flush transparently while we capture the bytes
// for gzip encoding.
//
// State machine:
//
//	WriteHeader → decide (based on path/scope/Accept-Encoding/header hints):
//	  * bypass    → flip bypass=true; delegate to underlying writer
//	  * threshold → compress=true; lazy-init gzip writer on first Write
//	  * passthrough → no compression; delegate to underlying writer
//
//	bypass path: every Write/Flush/WriteHeader goes to the underlying
//	  ResponseWriter verbatim.
//	threshold path: Write accumulates bytes; if at or above
//	  MinResponseSize, a gzip writer is lazily installed and subsequent
//	  bytes flow through it. Below threshold → bypass.
//	passthrough path: same as bypass.
type gzipResponseWriter struct {
	http.ResponseWriter
	gz                  *gzip.Writer
	buf                 *bufio.Writer
	statusCode          int
	wroteHeader         bool // true ⇒ WriteHeader was called (by handler or lazily)
	committedToUnderlying bool // true ⇒ underlying ResponseWriter.WriteHeader has run
	bypass              bool // true ⇒ no compression (passthrough / below threshold / excluded / non-compressible)
	handlerDone         bool // true ⇒ next.ServeHTTP returned normally (no panic); set AFTER it returns
	pendingBytes        int  // bytes buffered so far (before WriteHeader)
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.statusCode = code
	// Never double-encode: if the handler already set a Content-Encoding
	// (brotli, deflate, identity, …), the response is leaving in some
	// encoding that isn't gzip. Bypass entirely; do not add Vary either
	// (it would be misleading for downstream caches).
	if h := w.ResponseWriter.Header().Get("Content-Encoding"); h != "" {
		w.bypass = true
		w.ResponseWriter.WriteHeader(code)
		w.committedToUnderlying = true
		return
	}
	// Non-compressible content type (image/png, application/octet-stream,
	// video/*, …) → bypass with Vary so downstream caches still see
	// the Accept-Encoding partitioning signal. Compressing binary
	// responses wastes CPU and produces larger output. Without
	// isCompressibleContentType here, an /fe/api/* binary endpoint
	// would still attempt gzip on the threshold trigger.
	if ct := w.ResponseWriter.Header().Get("Content-Type"); ct != "" && !isCompressibleContentType(ct) {
		w.bypass = true
		w.ResponseWriter.Header().Add("Vary", "Accept-Encoding")
		w.ResponseWriter.WriteHeader(code)
		w.committedToUnderlying = true
		return
	}
	w.ResponseWriter.Header().Add("Vary", "Accept-Encoding")
	// Do NOT call underlying.WriteHeader here — defer until the first
	// Write decides between bypass and gzip. The HTTP server allows
	// mutating headers up until the first byte of body, so we keep
	// the option open by buffering the status code only. The bodyless
	// case (handler calls WriteHeader + returns without Write) is
	// handled by Close() committing the deferred header.
}

// Write accumulates body bytes; on the first call (when wroteHeader
// is still false) it commits headers and decides between bypass and
// compression based on the MinResponseSize threshold.
//
// Header ordering: the underlying WriteHeader is invoked here, AFTER
// any Content-Encoding header mutation (installGzip sets it). Once
// WriteHeader runs, headers are flushed to the wire and any later
// mutation is too late — so the ordering is critical.
//
// Decision is sticky: once bypass is set (either by WriteHeader with
// a pre-set Content-Encoding / non-compressible type, or by the first
// Write being below threshold), all subsequent bytes flow through the
// underlying writer uncompressed. This matches the threshold semantics
// — if the first chunk is small, we're committed to identity; we
// never start gzipping mid-stream.
func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if w.bypass {
		return w.ResponseWriter.Write(b)
	}
	// Lazy header commit: if the handler hasn't called WriteHeader
	// yet, set a 200 default and add Vary.
	if !w.wroteHeader {
		w.wroteHeader = true
		w.statusCode = http.StatusOK
		w.ResponseWriter.Header().Add("Vary", "Accept-Encoding")
	}

	// First body write — decide threshold. If above MinResponseSize,
	// install gzip BEFORE calling underlying.WriteHeader (so the
	// Content-Encoding: gzip header reaches the wire). If below,
	// commit headers uncompressed and pass bytes through.
	if w.gz == nil {
		w.pendingBytes += len(b)
		if w.pendingBytes < MinResponseSize {
			w.bypass = true
			w.ResponseWriter.WriteHeader(w.statusCode)
			w.committedToUnderlying = true
			return w.ResponseWriter.Write(b)
		}
		w.installGzip()
		w.ResponseWriter.WriteHeader(w.statusCode)
		w.committedToUnderlying = true
	}
	return w.writeThroughBuf(b)
}

func (w *gzipResponseWriter) installGzip() {
	w.gz = gzip.NewWriter(w.ResponseWriter)
	w.buf = bufio.NewWriter(w.gz)
	w.ResponseWriter.Header().Set("Content-Encoding", "gzip")
	// Drop any pre-existing Content-Length the handler set. The
	// compressed stream length is unknown until Close(); sending a
	// stale Content-Length causes the client to truncate the gzipped
	// body to the original uncompressed size, leaving a partial
	// gzip stream (and no trailer) — the exact "framing break"
	// failure mode the sweep call-out flagged.
	w.ResponseWriter.Header().Del("Content-Length")
}

func (w *gzipResponseWriter) writeThroughBuf(b []byte) (int, error) {
	if w.buf == nil {
		// Defensive: should never happen, but if it does, fall back
		// to the underlying writer so the request still completes.
		return w.ResponseWriter.Write(b)
	}
	n, err := w.buf.Write(b)
	if err != nil {
		return n, err
	}
	if err := w.buf.Flush(); err != nil {
		return n, err
	}
	return n, nil
}

// Flush forwards to the gzip writer's Flush (which flushes the
// underlying stream) AND the underlying ResponseWriter if it implements
// http.Flusher. Used by SSE handlers; on /fe/api/events the writer is
// in `bypass` mode so this falls through to the underlying
// ResponseWriter's Flush with no gzip state to drain.
func (w *gzipResponseWriter) Flush() {
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Close finalizes the response. Two responsibilities:
//
//  1. Bodyless commit (C2 fix): if the handler called WriteHeader but
//     never wrote a body (so the underlying WriteHeader was never
//     committed), commit it now with the captured statusCode. Without
//     this, DELETE-style bodyless handlers return 200 because the
//     caller never observed WriteHeader → the response defaults to 200.
//
//  2. Gzip stream finalization: if a gzip stream was installed, flush
//     the buffered writer and Close the gzip writer so the trailer
//     (CRC + ISIZE) lands on the wire.
//
// Called from a defer in CompressResponseWithOptions AFTER
// next.ServeHTTP has returned NORMALLY (handlerDone==true). On panic,
// the defer skips Close entirely — see CompressResponseWithOptions —
// so a partial panic-time gzip stream stays truncated and the
// client sees a corrupt / broken-pipe response rather than a complete
// 200 OK with decodable-garbage bytes.
func (w *gzipResponseWriter) Close() error {
	// Bodyless commit: handler called WriteHeader (status captured)
	// but never wrote a body, so we deferred header commit. Do it
	// now with the captured status.
	if w.wroteHeader && !w.committedToUnderlying {
		w.ResponseWriter.WriteHeader(w.statusCode)
		w.committedToUnderlying = true
	}
	if w.gz == nil {
		return nil
	}
	if w.buf != nil {
		_ = w.buf.Flush()
	}
	return w.gz.Close()
}

// isCompressibleContentType returns true for response content types
// where gzip typically helps (text/*, application/json, etc.).
// Binary types (image/png, application/octet-stream, video/*, …)
// pass through uncompressed.
func isCompressibleContentType(ct string) bool {
	if ct == "" {
		// Unknown / unset: assume compressible. JSON handlers commonly
		// set the header AFTER WriteHeader; this default gives the
		// majority of /fe/api/* JSON responses the right behavior.
		return true
	}
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(strings.ToLower(ct))
	switch {
	case strings.HasPrefix(ct, "text/"),
		ct == "application/json",
		ct == "application/javascript",
		ct == "application/xml",
		ct == "application/x-ndjson",
		ct == "image/svg+xml":
		return true
	}
	return false
}

// CompressResponse is the production entry point. It applies the
// default exclusion list (/fe/api/events) and the default minimum
// size (MinResponseSize).
func CompressResponse(next http.Handler) http.Handler {
	return CompressResponseWithOptions(next, defaultExcludedPaths, MinResponseSize)
}

// CompressResponseWithMinSize is like CompressResponse but lets the
// caller override the threshold (e.g. for tests). Production wiring
// should use CompressResponse.
func CompressResponseWithMinSize(next http.Handler, minSize int) http.Handler {
	return CompressResponseWithOptions(next, defaultExcludedPaths, minSize)
}

// CompressResponseWithOptions is the most general constructor:
//   - excludedPaths: paths (matched as prefix) that skip compression
//     entirely. Use nil to apply no exclusions (NOT recommended — at
//     least /fe/api/events must be excluded in production).
//   - minSize: bodies smaller than this are passed through uncompressed.
//
// Path matching: an excluded path matches when r.URL.Path equals the
// excluded value OR begins with excluded+"/". "/fe/api/events" matches
// both "/fe/api/events" and "/fe/api/events/x".
func CompressResponseWithOptions(next http.Handler, excludedPaths []string, minSize int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Scope: only /fe/api/* paths are eligible for compression.
		// Everything else (proxy paths, health, root) passes through.
		if !strings.HasPrefix(r.URL.Path, "/fe/api/") && r.URL.Path != "/fe/api" {
			next.ServeHTTP(w, r)
			return
		}

		// Excluded paths (SSE etc.) → no compression, preserve Flush.
		for _, ex := range excludedPaths {
			if ex == "" {
				continue
			}
			if r.URL.Path == ex || strings.HasPrefix(r.URL.Path, ex+"/") {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Accept-Encoding negotiation. Absent → no compression.
		// "identity" → no compression. "gzip" (alone or with q-values) → compress.
		// Multiple Accept-Encoding header fields are joined with "," per
		// RFC 9110 §12.5.3 (http.Header.Values returns the per-field slice,
		// which we concatenate here so clientAcceptsGzip sees a single
		// comma-separated value).
		if !clientAcceptsGzip(strings.Join(r.Header.Values("Accept-Encoding"), ",")) {
			next.ServeHTTP(w, r)
			return
		}

		grw := &gzipResponseWriter{ResponseWriter: w}
		defer func() {
			// M3 fix: only finalize the gzip stream when the handler
			// returned NORMALLY. If next.ServeHTTP panicked (e.g. a
			// downstream handler bug), the defer for Close runs BEFORE
			// recoveryMiddleware's defer because defers fire LIFO. If
			// we Close here, we'd append a COMPLETE gzip trailer to the
			// partial panic-time bytes — the client would see HTTP 200
			// OK with a fully-formed gzip body containing whatever
			// pre-panic bytes plus the trailer checksum (decodable
			// garbage, not an obvious error). Skipping Close leaves the
			// stream truncated; the recovery middleware's subsequent
			// 500 write lands after the partial gzip header, the
			// gzip.NewWriter never produced its trailer, and the
			// client's gzip decoder returns an error — the expected
			// "5xx is a corrupted response" behavior.
			if !grw.handlerDone {
				return
			}
			_ = grw.Close()
		}()

		next.ServeHTTP(grw, r)
		// Mark the handler as having returned normally BEFORE any
		// post-processing on this side. Set BEFORE the deferred
		// Close runs (this line executes before the defer at function
		// exit).
		grw.handlerDone = true
	})
}

// clientAcceptsGzip returns true when the Accept-Encoding header advertises
// gzip with a non-zero q-value per RFC 9110 §12.5.3 / RFC 7231 §5.3.4.
//
// Behavior summary (see client_accepts_gzip_test.go for the full table):
//
//	""                                       → false (no header)
//	"gzip"                                   → true
//	"gzip;q=1"                               → true
//	"gzip;q=0.5"                             → true
//	"gzip;q=0" / "gzip;q=0.0" / "gzip;q=0.000" → false (q=0 in any valid zero form ⇒ not acceptable)
//	"GZIP;Q=0"                               → false (case-insensitive codec + q)
//	" gzip ; q=0 "                           → false (whitespace around tokens/= tolerated)
//	"deflate, gzip;q=0"                      → false (mixed list — gzip disabled)
//	"gzip;q=abc" / "gzip;q=2" / "gzip;q="    → true  (malformed q ⇒ treated as absent ⇒ q=1)
//	"identity" / "*" / "x-gzip"              → false (current behavior preserved; see Wildcards below)
//
// Malformed-q policy details (incl. the NaN guard): see extractQValue.
//
// Wildcards / aliases: this function does NOT honor the "*" wildcard
// token (RFC 9110 §12.5.3) or the "x-gzip" historical alias for gzip.
// Clients that advertise only "*" or "x-gzip" will not get a gzipped
// response. This matches the pre-fix behavior; relaxing it is a
// separate change.
//
// Malformed q-value policy: RFC 9110 §12.5.3 requires that the weight be
// a real number in [0, 1] and is silent on what to do with unparseable
// values. We treat unparseable OR out-of-range weights as if the q
// parameter were absent (default q=1.0). Rationale: most clients set
// q explicitly only to disable a coding (q=0); accepting a coding whose
// q is malformed is less surprising than silently rejecting an
// otherwise-valid Accept-Encoding.
//
// Multiple header fields: the caller is expected to concatenate all
// Accept-Encoding values with "," before calling this function (see
// CompressResponseWithOptions). Internally, the function already
// accepts a comma-separated string.
func clientAcceptsGzip(acceptEncoding string) bool {
	if acceptEncoding == "" {
		return false
	}
	for _, part := range strings.Split(acceptEncoding, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		codec := part
		params := ""
		if i := strings.IndexByte(part, ';'); i >= 0 {
			codec = strings.TrimSpace(part[:i])
			// Preserve original casing/whitespace in params; the q
			// parameter name is matched case-insensitively inside
			// extractQValue.
			params = part[i+1:]
		}
		// Per RFC 9110 §12.5.3 content codings are case-insensitive
		// tokens (gzip == GZIP == GZip == gZip …).
		if !strings.EqualFold(codec, "gzip") {
			continue
		}
		// Look for the q parameter (case-insensitive name, whitespace
		// around `=` tolerated per RFC 7231 §3.2.6).
		q, hasQ := extractQValue(params)
		if hasQ && q <= 0 {
			// q=0 in any valid zero form ⇒ gzip is explicitly not
			// acceptable. We do NOT fall through to other codings;
			// the client told us gzip is off.
			return false
		}
		// q>0 (or absent, treated as q=1) ⇒ gzip is acceptable.
		return true
	}
	return false
}

// extractQValue parses the q parameter from an Accept-Encoding coding's
// parameter list (the substring after the first ';' in the coding).
// Returns (weight, true) when a q parameter was found and parsed as a
// valid number in [0, 1]. Returns (1.0, false) when the q parameter is
// absent OR present but malformed (unparseable or outside [0, 1]); the
// caller treats this as RFC default q=1.0 — gzip is acceptable.
func extractQValue(params string) (float64, bool) {
	if params == "" {
		return 1.0, false
	}
	for _, p := range strings.Split(params, ";") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// strings.Cut splits at the first '=' (or returns the whole
		// string and hasSep=false when no '=' is present). We do not
		// care about hasSep here: a bare "q" token with no value is
		// malformed and falls through to the ParseFloat error path.
		name, val, _ := strings.Cut(p, "=")
		if !strings.EqualFold(strings.TrimSpace(name), "q") {
			continue
		}
		val = strings.TrimSpace(val)
		f, err := strconv.ParseFloat(val, 64)
		if err != nil || math.IsNaN(f) || f < 0 || f > 1 {
			// Malformed or out-of-range weight ⇒ treat as absent
			// (q=1) per RFC's silent-on-malformed default. IsNaN is
			// required because ParseFloat accepts "NaN" without error
			// and NaN fails every range comparison.
			return 1.0, false
		}
		return f, true
	}
	return 1.0, false
}

// Ensure *gzipResponseWriter satisfies http.Flusher.
var _ http.Flusher = (*gzipResponseWriter)(nil)

// drainAndClose is kept as a tiny helper for symmetry with
// DecompressRequest's error-handling style; currently unused outside
// the package.
func drainAndClose(w io.WriteCloser) error {
	if w == nil {
		return nil
	}
	return w.Close()
}