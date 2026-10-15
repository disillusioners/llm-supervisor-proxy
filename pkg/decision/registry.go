package decision

import (
	"strings"
)

// Decision Models commission / §5.1.a / §3.2 step 9 — the
// per-provider registry. At P2 init() registers two adapters:
//
//   - "openai" → openaiDecisionProvider (byte-faithful pass-through
//     per Q18 / A26)
//   - "typesafe" → typesafeProvider (typed struct → System One)
//
// The registry is keyed on the lowercase credential provider
// string per imggen (mirror of pkg/imggen/minimax.go:199-215).
// The handler does NOT call providers.NewProvider — the local
// registry returns per-provider HTTP handlers built on
// sharedHTTPClient.
//
// Finding 7 — the variable is now camelCase Go convention
// (`providerRegistry`); was `decision_providerRegistry` at P1.
var providerRegistry = map[string]UpstreamProvider{}

// lookup returns the registered UpstreamProvider for a
// lowercase credential provider name. Returns (nil, false)
// when the provider is not registered (the handler emits a
// clean 502 + "no decision adapter registered for provider"
// in that case — finding 6 trimmed the previous P1 wording).
//
// Provider-name normalization mirrors imggen's
// registry.toLowerCase-on-lookup semantic so callers can pass
// either case for symmetry with cred.Provider (which is
// already lowercase via the credential validator's
// validateCred).
func lookup(providerName string) (UpstreamProvider, bool) {
	p, ok := providerRegistry[strings.ToLower(providerName)]
	return p, ok
}

// init wires the two upstream providers (Decision Models /
// P2.2 + P2.3). The map is init-only-written under Go's
// import-time single-thread invariant (no concurrent access
// until after main starts), so no mutex — mirrors
// pkg/imggen/minimax.go:204-206.
func init() {
	providerRegistry[OpenAIDecisionProviderName] = NewOpenAIDecisionProvider()
	providerRegistry[TypesafeProviderName] = NewTypesafeProvider()
}
