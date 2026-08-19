package proxy

import "net/http"

// ─────────────────────────────────────────────────────────────────────────────
// X-Proxy-Interleaved-Thinking header parsing — single source of truth
// ─────────────────────────────────────────────────────────────────────────────
//
// Per R8/Q3 (architecture-recommendation §11), the accepted values match
// the X-Force-Ultimate-Model precedent at pkg/proxy/handler.go:465:
// case-sensitive lowercase "true" or "1" only; everything else (including
// "True", "TRUE", "yes", empty, missing) is rejected.
//
// This file holds the single source of truth — used by both
// pkg/proxy/handler.go (race paths) and pkg/ultimatemodel.Execute
// (ultimate paths, via the exported ParseInterleavedThinkingHeaderValue
// since that package doesn't import this one). Identical semantics
// across callers.
// ─────────────────────────────────────────────────────────────────────────────

// InterleavedThinkingHeader is the canonical header name. Go's
// http.Header.Get handles both cases via textproto.CanonicalMIMEHeaderKey.
const InterleavedThinkingHeader = "X-Proxy-Interleaved-Thinking"

// ParseInterleavedThinkingHeaderValue returns true iff v is exactly "true"
// or "1" (case-sensitive lowercase). Anything else returns false. Empty
// string returns false.
//
// Exported so callers outside this package (notably pkg/ultimatemodel) can
// apply identical semantics without taking a dependency on proxy internals.
func ParseInterleavedThinkingHeaderValue(v string) bool {
	return v == "true" || v == "1"
}

// parseInterleavedThinkingHeader reads the canonical header from r and
// applies the value parser. Returns false when the header is absent or
// holds a rejected value. Unexported because the only in-package caller
// already has direct access to r.Header; external callers should use the
// exported ParseInterleavedThinkingHeaderValue on the value they fetch.
func parseInterleavedThinkingHeader(r *http.Request) bool {
	if r == nil {
		return false
	}
	return ParseInterleavedThinkingHeaderValue(r.Header.Get(InterleavedThinkingHeader))
}