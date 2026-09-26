# MiniMax Fallback 400 (2013) — reasoning translation is gated behind a client header clients don't know about

**Date:** 2026-09-26 · **Discovered during:** fleet-backend incident triage (2026-09-25 08:59Z)
**Severity:** any client that does not send `X-Proxy-Interleaved-Thinking` gets a hard 400 from MiniMax whenever fallback lands there
**Working reference:** ensemble traffic (sends the header + echoes `reasoning_content`) survives the same fallback path

## Issue

A proxy client (yedda-agent-fleet backend) received `openai: invalid params, 400 (2013)` mid-stream. Proxy logs show the failure happened **only on the MiniMax fallback attempt** — the primary GLM attempt had failed with a rate limit, and the MiniMax-M3 fallback rejected the request body:

```
2026/09/25 08:59:15 [RACE] Race attempt 0 calling internal provider: zai (model=glm-5.3, credential_id=glm-main)
2026/09/25 08:59:15 [RACE] Request 0 failed: openai: Rate limit reached for requests
2026/09/25 08:59:15 [RACE] Spawning fallback request (id=1, model=coding, trigger=main_error)
2026/09/25 08:59:15 [credentiallb] model=coding conv=2c69…→cred=minimax-company newlyBound=true
2026/09/25 08:59:15 [DEBUG] Race attempt 1 calling internal provider: minimax (model=MiniMax-M3, baseURL=https://api.minimax.io/v1)
2026/09/25 08:59:16 [RACE] Request 1 failed: openai: invalid params, 400 (2013)
2026/09/25 08:59:16 [RACE] All requests failed
```

Routing context (`models` table): `agentic` = glm-5.3 @ zai, fallback chain `["coding"]`; `coding` = MiniMax-M3. Also affected: `agentic-turbo` → fallback `["coding"]`, `quick` → fallback `["minimax-fast"]`.

## Root Cause

The request-side MiniMax translation (`reasoning_content` → `reasoning_details` + top-level `reasoning_split: true`) exists and is correct — but on the **race-internal** path it only runs when the client sends `X-Proxy-Interleaved-Thinking`:

`pkg/proxy/race_executor.go` (~line 366 and ~388):

```go
if interleaved && raceIntProviderIsMiniMax(cfg, req.modelID) {
    if err := translator.TranslateRequestBody(bodyMap); err != nil { … }
}
…
if interleaved && raceIntProviderIsMiniMax(cfg, req.modelID) && providerReq.ReasoningSplit == nil {
    t := true
    providerReq.ReasoningSplit = &t
}
```

Two client classes fall through this gate onto MiniMax:

1. **Clients that echo no `reasoning_content` at all** (residual gap — old/minimal builds, not the incident fleet): MiniMax receives a reasoning-less tool-call history and rejects it — 2013. (Same family of provider requirement as DeepSeek thinking mode: with `tools` present, prior assistant messages must carry reasoning.)
2. **Clients that echo `reasoning_content` but don't send the header** (THE FLEET AT INCIDENT TIME — user-confirmed via devops, the bugfix's primary class): `ChatMessage.ReasoningContent` is a typed field (`pkg/providers/interface.go`), so the string passes through **untranslated** — MiniMax gets raw `reasoning_content` instead of `reasoning_details`, no `reasoning_split`. **Wire-verified in production**: request `6d303458` (2026-09-26 18:28:33Z, captured via `/fe/api/requests`) shows the fleet sending `reasoning_content` on in-run assistant messages; the store displays it under the `Thinking` alias (`NewOpenAIAdapter().ToStoreMessages` capture path, `handler_functions.go:83-88` — the `ToStoreMessages` definition lives at `adapter_openai.go:68`). This is the untranslated-passthrough class that the shipped fix closes.

The gate's intent (per the D1/D2 comments in `pkg/proxy/translator/minimax.go`) was client opt-in for interleaved mode. But on an internal path where **the proxy chooses the fallback provider**, the client never decided to talk to MiniMax — the wire-format translation is a provider requirement, not a client feature preference.

## Fix Proposal — gate on provider, not header

On race-internal (and ultimate-internal, symmetric twin B), change:

```go
// before
if interleaved && raceIntProviderIsMiniMax(cfg, req.modelID) { … }

// after
if raceIntProviderIsMiniMax(cfg, req.modelID) { … }
```

Apply to both the `TranslateRequestBody` call and the `ReasoningSplit` typed setter (translation without `reasoning_split` is an untested half-state — the known-good ensemble path always has both). The header stays parsed for any future opt-out/annotation semantics; it just stops being load-bearing for correctness.

**In parallel (client-side):** the fleet will start sending `X-Proxy-Interleaved-Thinking` (its reasoning-echo build is already deployed) — replicating the ensemble's known-good configuration. The two fixes are complementary, not alternatives.

### Implemented fix — gate on `(interleaved || hasReasoning)` rather than provider-only

The shipped fix refines the provider-only proposal above. Provider-only would stamp `reasoning_split: true` on every reasoning-less body that landed on MiniMax — wasted wire bytes plus confusing logs, and it would break native-reasoning-details clients (no `reasoning_content` string at all) by adding `reasoning_split` to a body that already carries `reasoning_details`.

The shipped condition is `(interleaved || non-empty reasoning_content present) && provider is MiniMax`, applied symmetrically to:

- `pkg/proxy/race_executor.go:374` (race-internal `TranslateRequestBody` call) and `:401` (race-internal typed `ReasoningSplit` setter)
- `pkg/ultimatemodel/handler_internal.go:148` (ultimate-internal typed `ReasoningSplit` setter + message-level reasoning_content→reasoning_details translation — twin B)
- `pkg/ultimatemodel/handler_external.go:123` (ultimate-external request-body translator) and `:196` (ultimate-external non-stream response translator) and `:363` (ultimate-external stream response translator inside `streamResponse`, which gained a new `hasReasoning` parameter)

The body-content scan lives in `pkg/proxy/translator/minimax.go:HasReasoningContent` — same iteration shape as `TranslateMessagesReasoning` (nil/absent/non-array messages ⇒ false; non-map entries skipped; empty / non-string `reasoning_content` ⇒ false), read-only, cheap (returns on first hit). Class 1 (reasoning-less body) still 400s on MiniMax fallback — the residual gap is intentional and remains documented below.

### Residual gap (documented, not fixable proxy-side)

Clients that never echo `reasoning_content` (old builds, minimal integrations) will still 400 on MiniMax fallback — the proxy cannot synthesize missing reasoning. Optional experiment: inject a stub `reasoning_details` entry for tool-carrying assistant messages lacking one, and check whether MiniMax accepts stubs (DeepSeek-style APIs usually validate shape, not content — unverified).

## Acceptance criteria

1. Unit: with a MiniMax-internal model config and a request **without** the header carrying `messages[].reasoning_content`, the outgoing provider request contains `reasoning_details` arrays + top-level `reasoning_split: true`.
2. Unit: non-MiniMax providers (zai/glm) are untouched — no translation, no split, byte-identical body.
3. E2E (mock MiniMax): race with forced primary failure (429) lands on MiniMax with the translated body; mock asserts the wire shape.
4. Certification (live): bind `coding` to a MiniMax credential and send a tool-call history WITHOUT the header (a) without reasoning echo → expect documented 400 (residual gap), (b) with reasoning echo → expect success.

## Evidence & reproduction

- Fleet-side full bisection (payload-level): `yedda-agent-fleet` repo `docs/fix-instruction-2026-09-25-invalid-params-2013-history-window-nondeterminism.md` and `docs/note-2026-09-25-reasoning-content-echo.md` — includes the exact replay corpus (`/tmp/diag2013/` on the agent-fleet EC2 host).
- Key confirming observation: every payload replay against `agentic` passed at the time — because GLM was healthy and MiniMax was never reached. The failure is only observable when the fallback fires (rate limit on `glm-main`).
- Wire-verification of the fleet's fixed build: proxy request store (`/fe/api/requests/6d303458-…`, 2026-09-26 18:28:33Z) captured the fleet sending `reasoning_content` on in-run assistant tool-call messages (displayed as `thinking` — the store's display alias for `reasoning_content`, NOT a distinct wire field). The fleet does not send `X-Proxy-Interleaved-Thinking`, so this is live class-2 traffic today.
- GLM accepts both shapes (reasoning-less history AND raw `reasoning_content` echo, verified live 2026-09-25) — this is strictly a MiniMax-wire-format issue.

## Related but separate (do not conflate)

2026-09-26 18:28:38Z: mid-stream drop fleet↔proxy (`Request 0 failed: context canceled` + `client write failed: short write`, GLM healthy, no fallback) — the known occasional stream-drop class; caddy has no access logs on llmproxy to pin the killing layer. Separate investigation; see also `docs/cloudflare-drop-hang-bug.md` for the disconnect-handling history.
