// Package proxyheader provides shared parsing for proxy-only HTTP headers
// used by both pkg/proxy and pkg/ultimatemodel. It exists as a separate
// package to avoid an import cycle (proxy → ultimatemodel → proxy would
// otherwise be unavoidable when ultimatemodel needs the same accepted-value
// list).
package proxyheader

import (
	"net/http"
	"strconv"
	"strings"
)

// InterleavedThinkingHeader is the canonical header name. Go's
// http.Header.Get canonicalizes the lookup name via
// textproto.CanonicalMIMEHeaderKey, so any case variant of this
// constant (lowercase, UPPERCASE, MiXeD) is treated identically. No
// additional case-folding is required here — the stdlib already does
// it. Callers that pass a raw http.Header.Get lookup (race path:
// pkg/proxy/handler.go:479; ultimate path:
// pkg/ultimatemodel/handler.go:623) inherit the canonicalization for
// free.
const InterleavedThinkingHeader = "X-Proxy-Interleaved-Thinking"

// ParseInterleavedThinkingHeaderValue returns true iff v parses as a
// Go boolean (strconv.ParseBool): accepted truthy values are "1",
// "t", "T", "TRUE", "true", "True". Accepted falsy values are "0",
// "f", "F", "FALSE", "false", "False". Anything else — including
// "yes", "on", "enabled", empty string, or any whitespace-padded
// variant — returns false. Case-insensitivity covers all
// alphabetic variants ("true", "True", "TRUE" are equivalent).
//
// This is a deliberate relaxation of the prior strict `== "true" || ==
// "1"` semantics (the original matched the X-Force-Ultimate-Model
// precedent at pkg/proxy/handler.go:466). The relaxation was made to
// accommodate clients that send the flag in non-lowercase form
// (notably JS / Python clients that emit JSON-stringified booleans
// or the default Python str(True) = "True" output). The accepted-value
// set stays narrow — anything outside strconv.ParseBool's whitelist
// still resolves to false — and the gate's provider-side
// `providerIsMiniMax` short-circuit (race_executor.go:62,
// ultimatemodel/handler_external.go:98) bounds any false-positive
// blast radius to MiniMax-only paths.
//
// W11 cheap: this godoc explicitly enumerates the accepted values
// (via the strconv.ParseBool reference) so a future reader does not
// have to read the implementation to know the wire contract.
func ParseInterleavedThinkingHeaderValue(v string) bool {
	// strconv.ParseBool rejects leading/trailing whitespace; trim
	// first so common variants like "true " or " true" are
	// accepted without surprising the caller. Anything that is not
	// empty after trim and does not parse is a hard false (no error
	// to propagate — value parser returns bool, by contract).
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false
	}
	return b
}

// ParseInterleavedThinkingHeader reads the canonical header from r and
// applies the value parser. Returns false when the header is absent or
// holds a rejected value. Nil request safely returns false.
//
// Header NAME is case-insensitive via Go's http.Header.Get
// canonicalization (textproto.CanonicalMIMEHeaderKey) — no extra
// folding required here. Header VALUE is case-insensitive via
// strconv.ParseBool (see ParseInterleavedThinkingHeaderValue).
func ParseInterleavedThinkingHeader(r *http.Request) bool {
	if r == nil {
		return false
	}
	return ParseInterleavedThinkingHeaderValue(r.Header.Get(InterleavedThinkingHeader))
}
