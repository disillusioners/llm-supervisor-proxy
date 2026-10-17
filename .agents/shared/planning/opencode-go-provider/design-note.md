# opencode-go Provider — Design Note

**Commission:** opencode-go as a DISTINCT provider with provider-native session/UA handling (2026-10-17), extended in-flight with the F11 (reasoning_content string) + F12 (duplicate usage emission) scope additions backed by the live R4 E2E.
**Status:** implemented on `feature/opencode-go-provider` (base latest @ 42a1985). LOCAL ONLY — no push, no merge.
**Inputs:** surface research `.agents/shared/planning/opencode-go-surface-research/report.md` (R3-verified contract) + live evidence `.agents/tester/RESULTS/2026-10-17-opencode-e2e-r4.md` (R4, first successful Go-surface round-trip).

---

## 1. Distinct provider identity (user design point 1)

`opencode-go` is a NEW provider identity, NOT a base_url override of `opencode` (Zen). Zen (pay-per-use) and Go (subscription) are different products with different entitlements — the vendor's own Console client treats them as two integration IDs (`"opencode"` / `"opencode-go"`, research report §b). The existing `opencode` (Zen) provider is byte-untouched by this work (structural non-regression: the attribution flag is set only by the `ProviderOpencodeGo` factory case; gate-(e) test proves Zen requests carry no session header and no UA change).

- Const: `providers.ProviderOpencodeGo = "opencode-go"`; display name **"OpenCode Go"**; default base URL `https://opencode.ai/inference/go/openai/v1` (models-dev snapshot `"opencode-go".api`; the Go realm is selected by URL path alone — no Go-specific headers exist client-side).
- Transport: shared `OpenAIProvider` (OpenAI-compatible wire), flavour flag `openCodeGo` set by the factory only.
- Surfaces wired: `validProviders`, factory case + `IsProviderSupported` + provider metadata + `GetProviderTypes` (FE dropdown), FE `InternalProvider` union, README table row.

## 2. Session derivation (user design point 2)

`x-opencode-session` is hard-gated by the Go gateway (absent → `400 {"error":{"type":"MissingSessionID"}}`, live-confirmed R3; gate order auth → session-header → region, R4). The value must be the conversation-ROOT session id, stable across retries.

**Derivation formula (deterministic, no storage, generated at request time):**

```
seed   = conversationKey                                        (normal case)
       | credentiallb.ComputeConversationKey(modelID, credID, "")  (degenerate fallback)
H      = SHA256("opencode-go-session/v1|" + seed)
hexSlot = lowercase hex(H[0:6])  (6 raw bytes hex-encoded)       → 12 chars
b62Slot = base62(BigEndian(H[6:16])) left-padded '0' to 14       → 14 chars
id     = "ses_" + hexSlot + b62Slot                              → 30 chars wire
```

- **Reuse, not fork:** the seed is the proxy's own `credentiallb.ComputeConversationKey(modelID, tokenID, firstUserMessage)` product (pkg/credentiallb/key.go — the sha256 conversation key from the load-balance work; `pkg/grouplb` imports it, does not fork it). It is threaded handler → coordinator → executor → provider construction, and re-derived identically on every race attempt / credential-failover re-drive of one request — which is exactly the retry-stability the ses_ contract wants (same conversation ⇒ identical id; pure function of the conversation).
- **Shape compliance:** `ses_` + 12 hex + 14 base62 `[0-9A-Za-z]` = the vendor client's format (session-id.ts + identifier.ts). Base62 ordering (digits, upper, lower) is ours; the format contract cares about the set. **Correction to the research report:** its "Total length 29" line is an arithmetic slip — `ses_` (4) + 26 = **30**. The 12+14 slot split is the load-bearing fact and is implemented as such (test pins length 30).
- **Timestamp-slot semantics deliberately dropped:** the client's hex slot encodes `timestamp_ms*0x1000+counter` bit-inverted (newest-sorts-first). The gateway does not validate the semantics (foreign clients' native session ids accepted, research §c/§i), and content-derived ids have no time order — we fill the slot from the hash verbatim (shape-compliant, maximally compatible per the report).
- **Degenerate fallback (no user-role message ⇒ conversationKey == "", the W-2 case):** deterministic re-key over `(modelID, credentialID)` — stable across same-credential retries; credential failover intentionally re-keys (the degenerate seed includes credentialID). Distinct per (model, credential). Distinct degenerate conversations of one credential intentionally share one id: that population is system-only traffic, and a shared stable id is strictly better citizenship than no id (gateway 400) or per-attempt ids (breaks retry stability).
- **Documented nuance:** the conversation key includes modelID, so a mid-conversation model switch derives a new session id. This matches upstream routing/cache reality (different model = different cache domain) and the proxy's own affinity semantics.
- Domain-separation tag `opencode-go-session/v1` prevents correlation between session ids and the credential-affinity bindings that use the raw key.

## 3. UA policy (user design point 3 — including the honesty tension)

- **Default:** official-client format `opencode/{channel}/{version}/{name}` → **`opencode/stable/2.0.32/cli`** (channel `stable`, version `2.0.32` = client version pinned by the research; name `cli` — the production constants' real-world shape, research §c). User-directed: the proxy is to be treated as a first-class opencode client.
- **Honesty tension, documented:** the upstream docs require only an honest self-identifying UA ("such as `my-coding-agent/1.0`") and the research's good-citizen guidance says spoofing is NOT required. The user explicitly directed the official-client format as the DEFAULT anyway — a deliberate product decision, implemented as directed. **Live corroboration (R4): the honest UA `llm-supervisor-proxy-e2e/1.0` passed the Go surface end-to-end**, which validates that the upstream does not gate on UA shape — i.e. the override is a fully viable honest posture whenever an operator prefers it.
- **Override mechanism:** `OPENCODE_GO_USER_AGENT` env var (non-empty, trimmed wins) — provider-level, matching the proxy's existing env-var conventions (`DATABASE_URL`, `MCP_ENABLED`, `INTERNAL_ENCRYPTION_KEY`). A per-credential `user_agent` column was assessed and REJECTED: the UA identifies the client, not the credential (no legitimate per-credential differentiation), and it would cost a migration-032 schema change plus FE form surface outside this commission's FE scope (deliverable = type union + README only). Revisit as migration 032 if a per-credential knob is ever genuinely needed.

## 4. Catalog decision (deliverable 5): MANUAL models

Models stay **manual** (operator configures Go-catalog model ids in the proxy config, like every other provider here). Rationale: (1) the vendor's own client NEVER calls `/inference/go/v1/models` — its catalog comes from a bundled snapshot + a Console config endpoint (research §e); (2) catalog auth is unreliable — docs show a no-auth fetch while our key-carrying probe got 401 (realm validation of the presented key) — auto-fetching would import that unreliability; (3) the proxy's `/v1/models` serves configured models, so manual config needs zero new machinery and zero new upstream traffic. Operator-facing note: use Go-catalog ids (e.g. `deepseek-v4.1-flash` — present in BOTH Zen and Go catalogs with the same id but different surfaces/pricing; `-free` variants are Zen-only).

## 5. Attribution mechanics (deliverable 1)

- `OpenAIProvider` gains an unexported `openCodeGo` flag (factory-set) + `attribution *SessionAttribution`; `setHeaders` stamps `x-opencode-session` + `User-Agent` only when attribution is attached. No other provider is touched — no session header, UA unchanged (Go transport default), structurally guaranteed.
- `providers.AttachOpenCodeGoAttribution(provider, conversationKey, modelID, credentialID)` is invoked UNCONDITIONALLY at the four provider-construction sites that have conversation context; it is a kind-checked no-op otherwise:
  1. `pkg/proxy/race_executor.go` — race-internal (the /v1/chat/completions internal path; covers race attempts + credential-failover re-drives);
  2. `pkg/proxy/internal_handler.go` — /v1/messages internal (conversationKey threaded through `executeWithResolved`; covers the failover retry);
  3. `pkg/ultimatemodel/handler_internal.go` — ultimate internal (conversationKey threaded through `executeInternalWithResolved`);
  4. `pkg/ui/server.go` TestModel — the UI test-connection button (synthetic request; degenerate seed keeps the id stable across test clicks — same-credential stability only: the degenerate seed includes credentialID, so credential failover intentionally re-keys; without the seed the operator could not validate an opencode-go credential, as the gateway 400s).
- NOT wired (deliberately): race-external env-upstream path (provider comes from env, never a credential → cannot be opencode-go); `/v1/decisions` (D4 gate accepts only `openai`/`typesafe`); imggen (image-gen kind requires provider `minimax`). Generic per-credential static headers (F1 idea) are SUPERSEDED by this provider-native design for opencode-go and are NOT implemented.

## 6. F11 — reasoning_content is a STRING on this surface (R4 scope addition)

The Go surface emits reasoning as a flat `reasoning_content` string (non-stream `message.reasoning_content`, stream `delta.reasoning_content`) — never OpenAI's `reasoning_details` array. The path accepts BOTH shapes:

- **Stream:** the typed parser's existing dual-channel logic already covers it — `reasoning_details` presence wins (MiniMax-style typed thinking), else the DeepSeek-style `reasoning_content` string emits `thinking` events; the internal relay re-emits them to clients as `delta.reasoning_content` (target shape already). No code change needed; pinned by the verbatim-signature test.
- **Non-stream:** `Message.ReasoningContent` is a typed field (absorbed by JSON decode); `reasoning_details` arrays are absorbed by the D2 single-winner rule (details' text → `reasoning_content`, typed field nil'd for re-marshal). Pinned by tests for both shapes.
- **`""`-inactive nuance (R4):** the inactive field is the literal empty string, never null/absent. The parser's `!= ""` guards treat `""` as inactive-but-present — no spurious content/thinking events (pinned: the verbatim 16-chunk stream yields exactly 7 thinking + 7 content deltas, nothing from the `""` fields). The internal relay emits map-serialized chunks carrying ONLY the active key, so downstream normalizers never see a `""`-conflation trigger at all.
- Diagnostic notes folded into tests/expectations: no `system_fingerprint` on this surface (nothing assumes it); `finish_reason` rides the combined final chunk (most-compact form — the parser takes finish_reason from `lastResponse`, which is that chunk); stream ids are 50-char hex (not `chatcmpl-*`) — the relay generates its own chunk ids, so no assumption exists.

## 7. F12 — duplicate usage emission (R4 scope addition) — dedupe design

**Live signature (R4):** combined finish chunk (finish_reason + last content delta + FULL usage) → trailing usage-only chunk (`choices:[]`, usage verbatim-identical) → `[DONE]`. Any meter counting every usage-bearing chunk double-counts.

**Design call: LAST-WINS-COUNTED-ONCE** (not first-wins, not blanket-ignoring usage-only chunks):
- Two seams carry the guard:
  1. **Raw-SSE metering extractor** (`pkg/proxy/race_executor.go:extractUsageFromSSEChunk`): after parsing a usage triple, an exact repeat of the already-stored triple is absorbed (no state change); a genuinely new triple updates the slot (running estimate → final totals works). **Framing correction (review): this seam is DEFENSIVE-ONLY** — reachable only from the `executeExternalRequest` env-upstream path (env-chosen provider ⇒ never opencode-go). The LOAD-BEARING F12 fix is seam 2 below (the typed `lastStreamUsage` parser); `SetUsage` is a single-slot overwrite read once per completion (`pkg/proxy/race_request.go:319-328`), so the meter cannot accumulate on either seam.
  2. **Typed stream parser** (`pkg/providers/openai.go:processStream`): new `lastStreamUsage` last-wins tracker feeds the done event; a usage-only chunk (previously dropped with its `choices:[]` guard) now contributes — fixing a REAL gap where OpenAI `include_usage`-shaped streams (usage ONLY in the trailing chunk) would have missed metering entirely and fallen back to tokenizer estimates.
- **Why last-wins:** matches the pre-existing `SetUsage` overwrite semantics and the client-facing reverse-scan extraction (handler.go takes the LAST usage-bearing chunk); trailing chunks are the authoritative final usage under the include_usage convention.
- **Non-regression:** single-usage streams are provably unaffected — first stamp lands (slot nil → no skip); finish-chunk-usage streams land identically (the Go duplicate is a no-op overwrite either way); include_usage trailing-only shape now meters where it previously fell back to estimation (covered by dedicated tests on both seams, plus the pre-existing extractor table suite still passing).
- **Meter increment count:** the usage counter reads the single slot ONCE per request completion (handler.go) — one final value ⇒ exactly ONE increment per stream, for every usage shape.

## 8. Diagnostic response headers (x-opencode-*) — surfaced? NO

`x-opencode-endpoint-id` / `x-opencode-log-id` / `x-opencode-upstream-model-id` / `x-zen-model` are present on Go-surface 200s. NOT surfaced into request metadata: the provider layer does not thread response headers to the proxy's request-log metadata today, so surfacing them means new cross-layer plumbing (provider → executor → store → FE) disproportionate to the value; they remain visible whenever raw-upstream-response logging is enabled. Explicitly skipped, not forgotten.

## 9. Out of scope / unchanged

Retry/quota semantics (terminal quota codes, Retry-After handling) — the proxy's existing machinery, untouched per commission. Zen provider, decision, imggen, race/fallback behavior — untouched. FE changes are the type union only (no runtime JS ⇒ no static-bundle rebuild required; the type-only commit carries no `pkg/ui/static` change per the fresh-clone 404 rule).

## 10. Verification summary

- `go build ./...` green; `go vet ./...` clean (1.26 toolchain).
- Full `go test ./...` mandated gate — see the commission report for the final counts.
- Behavioral coverage: gates (a)–(e) of the original commission; F11 both shapes non-stream + the verbatim 16-chunk stream (7 thinking + 7 content, one done, usage once); F12 duplicate absorbed / last-wins / include_usage trailing-only / zero-usage, at BOTH guard seams; base62 shape/width/round-trip; derivation determinism (100×) + distinctness (500×) + degenerate fallback; Zen + plain-OpenAI non-regression.
