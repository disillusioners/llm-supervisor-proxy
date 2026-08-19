// Package proxyheader provides shared parsing for proxy-only HTTP headers
// used by both pkg/proxy and pkg/ultimatemodel. It exists as a separate
// package to avoid an import cycle (proxy → ultimatemodel → proxy would
// otherwise be unavoidable when ultimatemodel needs the same accepted-value
// list).
package proxyheader

import "net/http"

// InterleavedThinkingHeader is the canonical header name. Go's
// http.Header.Get handles both cases via textproto.CanonicalMIMEHeaderKey.
const InterleavedThinkingHeader = "X-Proxy-Interleaved-Thinking"

// ParseInterleavedThinkingHeaderValue returns true iff v is exactly
// one of the two accepted values: "true" or "1". Anything else
// (including "True", "TRUE", "yes", "0", empty string, or any other
// variant) returns false. The accepted-value list is intentionally
// narrow: matches the X-Force-Ultimate-Model precedent at
// pkg/proxy/handler.go:465 and is used by both pkg/proxy/handler.go
// (race paths) and pkg/ultimatemodel.Execute (ultimate paths) for
// identical semantics (R8/Q3 resolution per architecture-
// recommendation §11). Callers MUST go through this function for
// the value check — do not parse the header ad hoc; that has
// produced inconsistent semantics in past iterations.
//
// W11 cheap: this godoc explicitly enumerates the two accepted
// values so a future reader does not have to read the
// implementation to know the wire contract.
func ParseInterleavedThinkingHeaderValue(v string) bool {
	return v == "true" || v == "1"
}

// ParseInterleavedThinkingHeader reads the canonical header from r and
// applies the value parser. Returns false when the header is absent or
// holds a rejected value. Nil request safely returns false.
func ParseInterleavedThinkingHeader(r *http.Request) bool {
	if r == nil {
		return false
	}
	return ParseInterleavedThinkingHeaderValue(r.Header.Get(InterleavedThinkingHeader))
}
