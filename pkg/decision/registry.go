package decision

import "strings"

// decision_providerRegistry is the per-provider lookup. EMPTY at
// P1 by design (Decision Models commission / §3.2 / §10 P1 exit
// criterion: "POST /v1/decisions returns 502 — no providers
// registered yet but the route is live"). P2.2 / P2.3 register
// `openai` (reused; near-pure byte-faithful pass-through) and
// `typesafe` (typed struct → System One translation). The
// handler does NOT call providers.NewProvider — the local
// registry returns per-provider HTTP handlers built on
// sharedHTTPClient (mirroring imggen/minimax.go:199-215).
//
// Keyed on lowercase credential provider string per imggen.
// The handler's provider-lookup step (Step 9 of §3.2 lifecycle)
// treats an empty / unknown registration as 502 — no silent
// fallback to a different provider.
var decision_providerRegistry = map[string]UpstreamProvider{}

// lookup returns the registered UpstreamProvider for a
// lowercase credential provider name. Returns (nil, false) when
// the provider is not registered (the handler emits a 502 in
// that case). Provider-name normalization mirrors imggen's
// registry.toLowerCase-on-lookup semantic so callers can pass
// either case for symmetry with cred.Provider (which is
// already lowercase via the credential validator's
// validateCred).
func lookup(providerName string) (UpstreamProvider, bool) {
	p, ok := decision_providerRegistry[strings.ToLower(providerName)]
	return p, ok
}
