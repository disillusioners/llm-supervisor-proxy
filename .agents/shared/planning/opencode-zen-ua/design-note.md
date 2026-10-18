# opencode (Zen) Provider-Native User-Agent — Design Note

**Commission:** provider-native UA injection for the `opencode` (OpenCode Zen)
provider, env-overridable, inside the existing attribution machinery
(2026-10-18), on branch `fix/test-connection-provider-headers` (base latest
@ 14c040f). Backend-only; LOCAL ONLY — no push, no merge (see commit 320bc59).

**Inputs:** (.agents/shared/planning/opencode-go-surface-research/report.md
§c/§e — the header/UA contract) + the 2026-10-17 Zen research note
(OpenCode Zen = model gateway; free tier requires a Bearer key; the OpenAI
route's error envelope is Anthropic-shaped, e.g. `FreeTierError` on 403).

---

## 1. UA policy — which providers carry a native UA default and why

Two OpenCode provider identities share the OpenAI-compatible wire and now
share ONE attribution seam:

| Provider | Identity | Native UA default? | Session header? | Why |
|---|---|---|---|---|
| `opencode-go` | OpenCode Go (subscription) | YES — `opencode/stable/2.0.32/cli` | YES — `x-opencode-session` (`ses_…`) | Go surface contract: the gateway hard-gates chat on `x-opencode-session` (400 `MissingSessionID` when absent) and the vendor client sends the official UA. |
| `opencode` (Zen) | OpenCode Zen (pay-per-use) | YES — `opencode/stable/2.0.32/cli` | **NO** (deliberate) | Zen's **free tier is client-locked** — third-party clients are refused (`free models still require Bearer key from third-party clients`, 2026-10-17 Zen research note). Presenting the provider-native client UA is the compatibility posture. No documented session-header gate exists for Zen, so an unproven session header is NOT added (see §4). |

**Why the same UA string for both:** the vendor client computes the UA via
`App.useragent(app)` (`packages/core/src/app.ts:24-26`) **provider-independent**
— the same `opencode/{channel}/{version}/{name}` value rides every surface.
`openCodeUAChannel/Version/Name` are therefore shared constants
(`pkg/providers/opencodego.go`), and `DefaultOpenCodeUserAgent() ==
DefaultOpenCodeGoUserAgent() == "opencode/stable/2.0.32/cli"`. The two exported
functions are kept separate so each provider's policy and override scope read
explicitly at its call site (and may diverge if a surface ever needs a distinct
client name).

**All other providers unchanged:** no attribution UA (Go transport default UA),
no session header — structurally guaranteed (the flavour flags are set only by
the `ProviderOpencode` / `ProviderOpencodeGo` factory cases; the attribution
seam is a kind-checked no-op otherwise). Pinned by
`TestOtherProviders_NoAttributionUA_Unchanged` and
`TestOpencodeGo_ByteIdentical`.

## 2. Env overrides — names, scope, precedence

| Knob | Provider | Resolution | Precedence |
|---|---|---|---|
| `OPENCODE_USER_AGENT` | `opencode` (Zen) | `ResolveOpenCodeUserAgent()` | trimmed non-empty wins; else `DefaultOpenCodeUserAgent()` |
| `OPENCODE_GO_USER_AGENT` | `opencode-go` | `ResolveOpenCodeGoUserAgent()` | trimmed non-empty wins; else `DefaultOpenCodeGoUserAgent()` |

- **Fully independent:** each flavour reads its own env var only. `OPENCODE_USER_AGENT`
  never leaks to `opencode-go` (or any other provider), and vice-versa — pinned by
  `TestOpencode_EnvOverrides_ScopedToTheirProvider`.
- **Env-name DECISION (recorded 2026-10-18):** `OPENCODE_USER_AGENT` is KEPT;
  `OPENCODE_ZEN_USER_AGENT` was considered and REJECTED. Provider-scoped naming
  wins: the env var must match the registry entry (`opencode`) — "Zen" is absent
  from the provider vocabulary — and it mirrors the sibling
  `OPENCODE_GO_USER_AGENT` exactly (one knob per provider identity, same shape).
  The zero-breakage window for renaming closed at merge (320bc59 shipped the
  name); any later rename would break deployed operator env config for zero
  functional gain.
- **When read:** in `AttachOpenCodeGoAttribution`, at attach time — the same
  timing as the Go variant (`read per-request at attach time`). Whitespace-only
  values fall back to the default.
- **Honest posture available:** the docs require only self-identification
  ("such as `my-coding-agent/1.0`") and do NOT require impersonating opencode;
  operators who prefer an honest UA set the override (e.g.
  `OPENCODE_USER_AGENT=llm-supervisor-proxy/<ver>`). The official-client format
  is the DEFAULT by explicit user direction (first-class client posture) —
  consistent with the opencode-go decision.

## 3. Application site — one seam, zero per-path wiring

The fix lives INSIDE the shared attribution machinery, not in any caller:

```
factory (ProviderOpencode case)  →  openCodeZen = true
        │
        ▼
providers.AttachOpenCodeGoAttribution(provider, conversationKey, modelID, credentialID)
        │   invoked UNCONDITIONALLY at every provider-construction site
        │   kind-checked: openCodeGo ⇒ session+UA ; openCodeZen ⇒ UA-only
        ▼
OpenAIProvider.SetSessionAttribution(SessionAttribution{…})
        │
        ▼
OpenAIProvider.setHeaders   ← the SINGLE header-stamping site
        │   each header gated independently (non-empty):
        │   SessionID!="" ⇒ x-opencode-session ; UserAgent!="" ⇒ User-Agent
        ▼
upstream request
```

Because the seam is shared, every provider-constructing path inherits the Zen
UA with no per-path change:

1. `pkg/proxy/race_executor.go:379` — the real internal path
   (`/v1/chat/completions` internal; race attempts + credential failover).
2. `pkg/proxy/internal_handler.go:351` — `/v1/messages` internal.
3. `pkg/ultimatemodel/handler_internal.go:117` — ultimate internal.
4. `pkg/ui/server.go:1909` — the UI Test-Model button (`handleTestModel`).

**Not wired (deliberately, unchanged):** the raw header-passthrough arcs
(`executeExternalRequest` in `pkg/proxy/race_executor.go`, and
`pkg/ultimatemodel/handler_external.go`) never construct an `OpenAIProvider`
(they forward to a configured `UpstreamURL` with the client's own headers), so
they cannot be an `opencode` credential and are untouched — outside this
commission's scope by construction.

The function name `AttachOpenCodeGoAttribution` is retained (it predates Zen)
for call-site stability; it now covers the whole OpenCode family and is
documented as such.

## 4. Open question — gate depth (UA-keyed vs deeper) + verification plan

**Unproven:** whether Zen's free-tier client-lock keys on the User-Agent alone
or on a deeper signal (TLS fingerprint / client header set / entitlement
server-side). The R3 live probe on the `opencode` surface used a **curl UA**, so
it does NOT discriminate — a curl-UA 403 is equally consistent with a UA-gated
check and with a deeper check.

**Post-rollout verification plan:** once this lands, re-press the Test-Model
button against the **Zen free-tier model** with a valid key. The button now
issues the provider-native UA (via this fix); if the free-tier request
succeeds, UA-only gating is corroborated; if it still 403s, the gate is deeper
than UA and further surface work is needed. (No session header is sent for Zen,
by design.)

## 5. Known limitation — the test button's credential pinning (documented, not fixed here)

The Test-Model button (`handleTestModel`) tests exactly ONE credential — the
`credential_id` in the request body — plus whatever request-level overrides the
FE supplies (`internal_model`, and on the request path `api_key` /
`internal_base_url` override the stored credential). The FE pins
`credentials[0]` and may carry **unsaved form values**, so a green test proves
the FIRST credential + the on-screen values work — not that the whole saved
multi-credential config is healthy. This is a pre-existing property of the test
button; fixing it (credential picker / saved-state pinning) is OUT of scope for
this commission and would be a FE change. Documented here so the limitation is
not mistaken for a regression.

## 6. DEFERRED — passthrough-arc header consolidation → `fix/upstream-header-forwarding`

The two raw client-header forwarding arcs maintain **manually-drifted
skip-lists**:

- `pkg/proxy/race_executor.go` (the "Copy headers from original request" loop,
  ~:516-531): skips `Content-Length`, `Host`, `x-llmproxy-*`, and strips
  `x-proxy-interleaved-thinking`.
- `pkg/ultimatemodel/handler_external.go` (~:136-153): skips `Host`,
  `Content-Length`, `Transfer-Encoding`, and strips
  `x-proxy-interleaved-thinking`.

These overlapping-but-not-identical lists are a consolidation candidate
(shared skip-list helper), but it is **explicitly DEFERRED** and assigned to the
`fix/upstream-header-forwarding` commission to avoid collisions with this
backend-only, attribution-scoped change. Do NOT fold that refactor in here.

## 7. Tests

- `pkg/providers/opencodezen_test.go` — UA default shape (+ equality with the Go
  default), `OPENCODE_USER_AGENT` override/whitespace, real-path seam
  (UA-only + no session), conversation-independence (real-path/test-path UA
  parity anchor), env scoping (no cross-provider leak), `opencode-go`
  byte-identical, all-other-providers control (derived from
  `pkg/models.ValidProviderNames()` minus the OpenCode entries), a
  setHeaders-seam pin, and a setHeaders flavour-gate negative pin (a
  Zen-flagged instance handed a SessionID never emits a session header).
- `pkg/ui/handlers_models_test_connection_test.go` — test-path twin: `handleTestModel`
  carries the SAME exported default UA as the real path for Zen; `opencode-go`
  carries UA + a deterministic degenerate-seed `ses_` session; `openai` control
  unchanged; plus a cross-path parity test driving the real path and
  `handleTestModel` against the same mock upstream and asserting the captured
  UA (and, for opencode-go, the session) are EQUAL.
- Updated `pkg/providers/opencodego_test.go`: the former Zen "no UA" gate is
  superseded by `TestOpencode_Zen_UAOnly_NoSession` (Zen now carries the native
  UA; still no session header).
