package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"os"
	"strings"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
)

// =============================================================================
// opencode-go — OpenCode Go (subscription) distinct provider.
//
// opencode-go is a DISTINCT provider identity from "opencode" (Zen,
// pay-per-use): same OpenAI-compatible wire, different upstream realm,
// different entitlements. Beyond the shared OpenAIProvider transport it
// carries provider-native ATTRIBUTION on every upstream request:
//
//   1. x-opencode-session — a conversation-stable session id, derived
//      deterministically from the proxy's conversation key (the same
//      credentiallb.ComputeConversationKey product that drives
//      credential affinity; hashed over the leading contiguous
//      user-message run). The Go gateway hard-gates chat on this
//      header (400 {"error":{"type":"MissingSessionID"}} when absent —
//      live-verified 2026-10-17, see .agents/shared/planning/
//      opencode-go-surface-research/report.md §c).
//   2. User-Agent — provider-default official-client format
//      opencode/{channel}/{version}/{name} (≈ opencode/stable/2.0.32/cli),
//      overridable via OPENCODE_GO_USER_AGENT (see ResolveOpenCodeGoUserAgent
//      for the honesty-tension rationale).
//
// Attribution is attached by the proxy call sites via
// AttachOpenCodeGoAttribution and stamped in OpenAIProvider.setHeaders.
// Nothing else changes for any other provider: the attribution flag is
// set ONLY by the factory's ProviderOpencodeGo case, and every attach
// helper is a kind-checked no-op otherwise. The existing "opencode"
// (Zen) provider never sets the flag and never carries the headers
// (non-regression test: opencodego_test.go).
//
// Design note: .agents/shared/planning/opencode-go-provider/design-note.md
// =============================================================================

const (
	// ProviderOpencodeGo is the identity for OpenCode Go — the
	// subscription-billed sibling of OpenCode Zen
	// (https://opencode.ai/go). OpenAI-compatible chat-completions
	// provider dispatched through OpenAIProvider at the default base
	// URL https://opencode.ai/inference/go/openai/v1 (the Go realm is
	// selected by URL path alone — no Go-specific headers exist
	// client-side). DISTINCT from ProviderOpencode: Zen and Go are
	// different products with different entitlements (opencode
	// commission 2026-10-17; integration IDs "opencode" vs
	// "opencode-go" in the vendor's own Console client).
	ProviderOpencodeGo ProviderType = "opencode-go"

	// OpenCodeGoDefaultBaseURL is the locked default base URL for the
	// Go realm (models-dev snapshot "opencode-go".api, research
	// report §b).
	OpenCodeGoDefaultBaseURL = "https://opencode.ai/inference/go/openai/v1"

	// EnvOpenCodeGoUserAgent is the provider-level User-Agent override.
	// When set to a non-empty value it replaces the official-client
	// default for opencode-go upstream requests.
	EnvOpenCodeGoUserAgent = "OPENCODE_GO_USER_AGENT"

	// openCodeGoSessionDomain domain-separates session derivation from
	// every other hash use of the conversation key (the raw key also
	// pins credential-affinity bindings; the session id must not be
	// correlatable with those bindings).
	openCodeGoSessionDomain = "opencode-go-session/v1"

	// Official-client UA format constants (research report §c:
	// packages/core/src/app.ts:24-26 — opencode/{channel}/{version}/{name}).
	openCodeGoUAChannel = "stable"
	openCodeGoUAVersion = "2.0.32"
	openCodeGoUAName    = "cli"

	// Session-id shape constants. The vendor client's session id is
	// "ses_" + 26 chars: 12 hex chars (a 48-bit timestamp*0x1000+counter
	// encoding, bit-inverted) + 14 base62 chars (packages/schema/src/
	// session-id.ts + identifier.ts, research report §c). Total wire
	// length 30. NOTE: the research report's "Total length 29" line is
	// an arithmetic slip (4 + 26 = 30); the 12+14 slot split is the
	// load-bearing fact and is self-consistent throughout the report.
	// Why these widths: the hex slot is 12 chars because the vendor
	// packs a 48-bit timestamp*0x1000+counter value into it; the
	// base62 slot is 14 chars because it carries an 80-bit random
	// value (capacity 14·log2(62) ≈ 83.4 bits ≥ 80).
	openCodeGoSessionPrefix = "ses_"
	openCodeGoHexLen        = 12 // hex slot: 48 bits = 6 bytes
	openCodeGoBase62Len     = 14 // base62 slot: 80 bits = 10 bytes (80 < 14*log2(62) ≈ 83.4)

	// base62Charset is the [0-9a-zA-Z] charset used for the random
	// slot. The vendor client draws these chars from crypto.getRandomValues
	// with byte%62; the format contract cares about the SET, not the
	// ordering — this fixed ordering is what makes left-padded
	// encodings deterministic for us.
	base62Charset = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// SessionAttribution carries the provider-native attribution headers
// stamped on opencode-go upstream requests.
type SessionAttribution struct {
	// SessionID is the x-opencode-session value: an opencode-shaped,
	// conversation-stable session id (ses_ + 12 hex + 14 base62).
	SessionID string
	// UserAgent is the User-Agent value.
	UserAgent string
}

// DefaultOpenCodeGoUserAgent returns the provider-default User-Agent in
// the official client format: opencode/{channel}/{version}/{name}.
//
// UA policy note (design decision, user-directed): the research report's
// good-citizen guidance says docs only require an HONEST self-identifying
// UA and spoofing opencode is not required — the user explicitly chose
// the official-client format as the DEFAULT (first-class client posture),
// with this override knob for operators who prefer the honest
// llm-supervisor-proxy/<ver> form. Both are documented as acceptable by
// the upstream docs; see design-note.md "UA policy".
func DefaultOpenCodeGoUserAgent() string {
	return "opencode/" + openCodeGoUAChannel + "/" + openCodeGoUAVersion + "/" + openCodeGoUAName
}

// ResolveOpenCodeGoUserAgent resolves the User-Agent for opencode-go
// upstream requests: OPENCODE_GO_USER_AGENT (trimmed, non-empty) wins,
// otherwise DefaultOpenCodeGoUserAgent.
//
// Scope note: the override is a single provider-level knob (env var,
// matching existing proxy env conventions like DATABASE_URL /
// MCP_ENABLED / INTERNAL_ENCRYPTION_KEY) rather than a per-credential
// config column. A per-credential UA has no legitimate differentiation
// use (the UA identifies the client, not the credential) and would cost
// a schema migration + FE form surface outside this commission's FE
// scope. Revisit as migration 032 if a per-credential knob is ever
// needed.
func ResolveOpenCodeGoUserAgent() string {
	if v := strings.TrimSpace(os.Getenv(EnvOpenCodeGoUserAgent)); v != "" {
		return v
	}
	return DefaultOpenCodeGoUserAgent()
}

// OpenCodeGoSessionSeed returns the session-derivation seed for an
// opencode-go request.
//
//   - conversationKey non-empty (the normal case): the proxy's own
//     conversation key — credentiallb.ComputeConversationKey(modelID,
//     tokenID, leadingUserRun), hashed over the canonical JSON array
//     of the leading contiguous user-message run — is used verbatim.
//     It is threaded through handler.go / handler_anthropic.go
//     post-auth and is stable across race attempts and credential
//     failovers within a request, which is exactly the retry-stability
//     the ses_ contract wants.
//   - conversationKey == "" (degenerate: no leading user run in the
//     request — W-2 in the credentiallb contract): a deterministic
//     fallback key is derived from (modelID, credentialID) so the
//     session stays stable across retries even without a conversation
//     anchor. Distinct degenerate conversations of the same
//     (model, credential) intentionally share one session id — the
//     degenerate population is traffic without a leading user run
//     (system-only / assistant-first), and a shared id is strictly
//     better citizenship there than no id (which the gateway 400s) or
//     per-attempt ids (which break retry stability).
func OpenCodeGoSessionSeed(conversationKey, modelID, credentialID string) string {
	if conversationKey != "" {
		return conversationKey
	}
	return credentiallb.ComputeConversationKey(modelID, credentialID, "")
}

// DeriveOpenCodeGoSessionID maps a conversation seed to an
// opencode-shaped session id: "ses_" + 26 chars (12 hex + 14 base62),
// wire length 30.
//
// Derivation formula (deterministic, no storage):
//
//	H        = SHA256("opencode-go-session/v1|" + seed)
//	hexSlot  = lowercase hex(H[0:6])                       → 12 chars
//	b62Slot  = base62(BigEndian(H[6:16])) left-padded '0'  → 14 chars
//	session  = "ses_" + hexSlot + b62Slot                  → 30 chars
//
//   - Same conversation ⇒ identical seed ⇒ identical id (pure function).
//   - Different conversations ⇒ different SHA256 preimages ⇒ different
//     ids with collision probability ~2^-80 per pair on the base62 slot
//     (and 2^-48 on the hex slot — the pair jointly identity-bound).
//   - The vendor client's hex slot encodes timestamp*0x1000+counter
//     bit-inverted (newest-sorts-first). That semantics is NOT validated
//     server-side (research report §c/§i: foreign session ids accepted),
//     and content-derived ids have no time order to sort — so we fill
//     the slot from the hash verbatim and keep the SHAPE (12 hex +
//     14 base62), the maximally compatible choice per the report.
func DeriveOpenCodeGoSessionID(seed string) string {
	sum := sha256.Sum256([]byte(openCodeGoSessionDomain + "|" + seed))
	hexSlot := hex.EncodeToString(sum[:openCodeGoHexLen/2])
	b62Slot := encodeBase62Padded(new(big.Int).SetBytes(sum[openCodeGoHexLen/2:openCodeGoHexLen/2+10]), openCodeGoBase62Len)
	return openCodeGoSessionPrefix + hexSlot + b62Slot
}

// encodeBase62Padded renders n in base62 over base62Charset, left-padded
// with '0' to width. n is consumed; callers pass a fresh big.Int.
func encodeBase62Padded(n *big.Int, width int) string {
	if n.Sign() == 0 {
		return strings.Repeat("0", width)
	}
	base := big.NewInt(int64(len(base62Charset)))
	remainder := new(big.Int)
	var out []byte
	for n.Sign() > 0 {
		n.QuoRem(n, base, remainder)
		out = append(out, base62Charset[remainder.Int64()])
	}
	// Digits were emitted least-significant first; reverse.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if len(out) < width {
		out = append([]byte(strings.Repeat("0", width-len(out))), out...)
	}
	return string(out)
}

// AttachOpenCodeGoAttribution derives and attaches the opencode-go
// session + UA attribution onto providerClient.
//
// Call-shape contract: invoke UNCONDITIONALLY at every provider
// construction site that has the conversation context in scope. It is a
// no-op unless providerClient is the opencode-go-flavoured
// OpenAIProvider (kind-checked via the factory-set flag), so non-
// opencode-go providers are never touched — "session header injection
// for opencode-go upstream requests ONLY".
//
// The id is derived at request time (no storage): the seed tuple is the
// per-request conversation identity already threaded by the proxy, and
// DeriveOpenCodeGoSessionID is a pure function of it, so every attempt /
// failover re-derivation within one request lands on the SAME session id.
func AttachOpenCodeGoAttribution(providerClient Provider, conversationKey, modelID, credentialID string) {
	op, ok := providerClient.(*OpenAIProvider)
	if !ok || !op.openCodeGo {
		return
	}
	op.SetSessionAttribution(SessionAttribution{
		SessionID: DeriveOpenCodeGoSessionID(OpenCodeGoSessionSeed(conversationKey, modelID, credentialID)),
		UserAgent: ResolveOpenCodeGoUserAgent(),
	})
}
