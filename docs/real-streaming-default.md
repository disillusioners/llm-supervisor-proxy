# Real-Streaming Default (X-LLMProxy-Buffer-Response opt-in)

> **Pinned to commit:** `44f8e63` (Phase 5 review fixes — real passthrough
> parity, variant goldens, event-ordering assertions, dead-code cleanup,
> docs corrections).
>
> **Status:** Shipped — Phases 1–4 merged; Phase 5 parity + acceptance
> harness landed. Path 1 (Anthropic→Anthropic passthrough) is now
> exercised by a REAL passthrough fixture (internal model with
> anthropic-provider credential pointing at an Anthropic-wire upstream;
> see `real_streaming_parity_test.go:1_AnthropicPassthrough_*`). All 6
> streaming paths are covered by golden-trace parity (header-present ⇒
> byte-identical to pre-feature behavior) and event-based first-byte
> timing (header-absent ⇒ first byte reaches the client before upstream
> EOF).

`llm-supervisor-proxy` defaults to **real streaming** on streaming requests: chunks are
forwarded to the client as they arrive from upstream, with no full-response accumulation.
The previous fully-buffered supervised behavior is preserved behind an explicit per-request
opt-in header.

---

## TL;DR

| Aspect                  | Behavior                                                                |
| ----------------------- | ----------------------------------------------------------------------- |
| **Default mode**        | Real streaming — first byte reaches client before upstream `[DONE]`     |
| **Opt-in to legacy**    | Send request header `X-LLMProxy-Buffer-Response: true` (or empty value) |
| **Non-stream requests** | Unaffected — header is a no-op                                          |
| **Mid-stream fallback** | Disabled after first forwarded byte (locked L4)                         |
| **Mid-stream cred-LB**  | Disabled after first forwarded byte (locked L4)                         |
| **Heartbeat / usage**   | Preserved in both modes                                                 |
| **Race-retry fallback** | Pre-first-byte only (post-first-byte is leader-ACCEPTED degradation)    |

---

## Header semantics — locked L1

The header's behavior follows a **presence-aware** 4-row truth table:

| Header state                           | Value               | Mode            |
| -------------------------------------- | ------------------- | --------------- |
| **ABSENT** (no header sent)            | —                   | Live streaming  |
| **PRESENT** + empty value              | `X-LLMProxy-Buffer-Response` (bare) | Buffered (legacy) |
| **PRESENT** + truthy                   | `true`, `1`, `yes`, `on` (case-insensitive) | Buffered (legacy) |
| **PRESENT** + falsy                    | `false`, `0`, `no`, `off` (case-insensitive) | Live streaming   |
| **PRESENT** + unknown / misspelled     | any other non-empty value | Live streaming   |

**Why presence-aware?** Go's `r.Header.Get("X-LLMProxy-Buffer-Response")` conflates ABSENT
with PRESENT+empty. The parser uses `r.Header.Values(...)` and treats a zero-length slice
as ABSENT (⇒ live) and a non-zero slice (even with `""` as the first value) as PRESENT
(dispatched to the value parser). See `pkg/proxy/handler_functions.go:bufferModeFor`.

**Multi-value first-wins** (leader decision 2): if a client sends multiple
`X-LLMProxy-Buffer-Response` headers, the first value wins. The Go canonical-form parser
does this by default (`Values()[0]`).

**Comma-joined single-line form** (reviewer finding #8c): Go's `net/http` also
canonicalizes a single header line with an embedded comma into a ONE-value slice
(`["a, b"]` rather than `["a", "b"]`). So
`X-LLMProxy-Buffer-Response: a, b` arrives at the parser as a single value `"a, b"`
which is not in the truth table → live mode. This is distinct from the multi-line
form (separate `X-LLMProxy-Buffer-Response:` lines on the request) where the first
value wins. Operators sending literal comma lists should use the multi-line form or
send the value as a single canonical truthy/falsy value.

---

## What's preserved in live mode

The following features are **byte-stable** between buffered and live modes (plan §8.1):

- **Normalizers** — `empty_role`, `split_concatenated`, `tool_call_index`,
  `tool_call_repair` (`pkg/proxy/normalizers/`). Per-chunk application; live mode
  forwards each normalized chunk immediately.
- **Per-chunk MiniMax translator** — `StreamTranslator.ChunkBytes` (the ultimate-external
  pipeline). Per-chunk; preserves emit ordering.
- **Tool-call buffering** — `pkg/toolcall/buffer.go` with per-call JSON-completion hold
  (`isCompleteLocked`). Emits at per-call JSON completion; live mode forwards each
  emitted chunk; the per-call hold is unchanged.
- **Heartbeat keepalives** — 15s SSE comments via `pkg/proxy/heartbeat.go` (writeMu-guarded)
  AND `pkg/ultimatemodel/heartbeat.go` (per-request `writeMu` discipline mirroring the
  proxy-side pattern, approver iteration 001 Blocking 2). Runs in both modes;
  cancellation unchanged.
- **Usage metering** — `counter.Increment(...)` final-chunk tap at `handler.go:1189-1204`.
  Where upstream returns `usage`, metering is identical in both modes.
- **Tokenizer-fallback usage estimator** — `handler_external.go:483` /
  `handler_internal.go:710`. **PARITY IN BOTH MODES — not an accepted degradation.**
  The R2 fix (Phase 2 task 2.4) gated Prune + entry-capture to buffered mode so the
  estimator's raw-byte input is preserved complete in live mode. Regression pin:
  `TestLiveRelay_R2_EstimatorUnderPrunePressure` and the C1 fixture in
  `TestUltimateCapture_LiveMode_IdenticalToBuffered`.
- **Inline content tap** — `extractStreamChunkContent` in `pkg/proxy/handler_helpers.go`.
  Per-chunk accumulation into `rc.accumulatedResponse` / `accumulatedThinking` /
  `accumulatedToolCalls`. UI request records stay identical.
- **Bufferstore debug dumps** — `saveRawResponse` (`handler.go:1159-1186`). Done
  capture is authoritative in live mode (Prune gated); entry capture (`:1053-1058`)
  is gated to buffered mode only (avoids double-save in live mode). Buffered mode keeps
  today's entry+Done behavior. Both modes write EXACTLY ONCE per upstream response
  (double-save is benign — unique temp file + atomic rename in `pkg/bufferstore/store.go:32-59`).
- **Credential LB pre-stream `GetOrSelect`** — pre-stream resolution unchanged in both
  modes. **Locked L7:** Anthropic path does NOT publish `model_credential_selected`
  (`internal_handler.go:146-256` discards `NewlyBound`); OpenAI race path is the sole
  publisher (`race_executor.go:124-135`).

---

## Accepted degradations in live mode

The following features lose capability in live mode by **architectural necessity**
(plan §8.2). They are documented, NOT bugs.

### D1. No racing / fallback / retry AFTER first forwarded byte (OpenAI race path)

The race coordinator runs exactly as today until the **first forwardable data chunk**
fires (R1-variant winner gate — `race_coordinator.go:406-435`). `cancelAllExcept(winner)`
fires mid-stream; the EXISTING `streamResult` loop (`handler.go:1038-1380`) IS the live
relay. After the first byte, retry / fallback / mid-stream credential failover are
**off**.

**Why:** A retry would interleave bytes from a different upstream; the client would
see garbled output. Pre-first-byte fallback chains and credential failover are
preserved. Buffered mode keeps the full partial-release machinery unchanged.

### D2. Anthropic sequential fallback loop stops after first byte

The loop at `handler_anthropic.go:256` iterates `arc.modelList`; once
`arc.headersSent==true && !arc.bufferMode`, switching models would interleave bytes.
Loop-top guard: `if arc.headersSent && !arc.bufferMode { break }`.

**Pre-first-byte fallback is still supported** (loop iteration 0 with headers not yet
sent). Post-first-byte fallback is gone.

### D3. Pre-first-byte upstream failure surface changes (in-band SSE error envelope)

SSE headers + `: connected\n\n` written pre-attempt at `handler.go:882-889`; L4 disallows
fallback after first byte, so the failure must be an in-band SSE error envelope. Clients
see **HTTP 200** (already sent) + an SSE `data:` line carrying the OpenAI error object
(equivalent to `handler.go:1169-1178`). This matches the Anthropic + OpenAI SSE error
envelope conventions.

### D4. No mid-stream credential failover

Same as D1 — failover would interleave bytes from a different credential.

**Pre-first-byte rate-limit failover** (the Phase 3 LB work) still runs. Post-first-byte
failover is gone.

### D5. Tokenizer-fallback usage estimator — NO DEGRADATION (R2 fix)

The estimator consumes `winner.GetBuffer().GetAllRawBytesOnce()` raw bytes
(`handler.go:1220`). Mid-stream Prune (the 5 sites `handler.go:1082/:1122/:1167/:1238/:1357`)
would truncate that input NON-DETERMINISTICALLY in live mode (worst ≈0, best full).
**Phase 2 task 2.4 gated Prune (and the entry raw-capture) to buffered mode.** Prune
already never fires in buffered mode today (`readIndex==len` at relay entry ⇒
`ShouldPrune` false, `stream_buffer.go:294-303`), so effective behavior is preserved in
both modes. **Regression pin:** `TestLiveRelay_R2_EstimatorUnderPrunePressure`.

### D6. Partial-release machinery superseded in live mode

The first-byte winner gate replaces deadline-driven best-buffer promotion: the winner
is picked at the first forwardable chunk, long before any deadline could fire. The
**0-byte deadline guard** (live-mode-specific, `race_coordinator.go:661-722`) ensures
that if no byte arrives within `StreamDeadline`, the coordinator surfaces a terminal
in-band SSE error + cancels all attempts (NOT a hung 0-byte winner).

### D7. Global `StreamDeadline` semantics redefined in live mode

- **Buffered mode** (header present): coordinator + deadline run exactly as today
  (`race_coordinator.go:370`), including 0-byte promotion.
- **Live mode** (header absent): the coordinator runs with the first-byte gate; the
  deadline's meaning becomes **"no forwardable byte within `StreamDeadline` ⇒
  terminal in-band SSE error + all attempts cancelled"** (0-byte guard at
  `race_coordinator.go:661-722`). Hard deadline (`MaxGenerationTime`) unchanged in
  both modes.
- The per-model `ReleaseStreamChunkDeadline` field (`pkg/models/config.go:127`) is
  **DORMANT** — zero call sites in `pkg/proxy`; stays dormant in both modes. The
  ACTIVE deadline is the global `StreamDeadline`.

### D8. Wire-shape change: internal-Anthropic LIVE mode emits `thinking_delta` blocks

> ⚠️ **WIRE-SHAPE CHANGE** — downstream clients must not be surprised.

In live mode, the Anthropic→OpenAI-wire internal-translation path emits an Anthropic
`thinking` content block on the wire when an upstream OpenAI chunk carries
`reasoning_content`. Buffered mode does NOT emit thinking blocks (the
`flushingResponseRecorder` path suppresses thinking bytes per
`internal_handler.go:379-396`). This is a **deliberate wire-shape change** for live
mode only.

**Buffered mode wire shape:** no thinking blocks on the wire; thinking captured into
the `thinkingSink` side channel only.
**Live mode wire shape:** thinking blocks emitted as Anthropic `thinking_delta` events;
sink capture ALSO preserved.

Parity tests assert byte-identity in buffered mode and explicit new emission in live
mode. Downstream clients consuming the Anthropic wire must be ready for `thinking`
content blocks on streaming requests in live mode.

### D9. Live-mode block interleaving (architect 2026-08-27)

The incremental translator emits blocks in **ARRIVAL order** (single-open-block-of-
each-kind); the batch translator always GROUPS thinking-before-text
(`stream.go:176-197`). Interleaved-thinking upstreams produce interleaved
thinking/text blocks on the wire in live mode; buffered mode remains grouped. Real
Anthropic streams interleave natively (passthrough precedent). Pinned by
`TestIncrementalStreamTranslator_ParityVsBatch` (event-SET equality; ORDER equality
for non-interleaved; pinned divergence for interleaved).

### D10. H8 (no mid-stream flush) is dropped in live mode

Live mode's purpose IS mid-stream flushing. The buffered-mode H8 guarantee is lost
only when header is absent. Header opt-in restores H8.

### D11. Live-mode winner-buffer memory = full response

Prune is gated off in live mode to preserve the estimator/bufferstore raw-byte inputs
(locked "passive recorder" contract). Live mode holds the full response in the winner's
buffer — identical to today's buffered profile. **No regression** in memory.

### D12. `race_winner_selected` fires mid-stream in live mode

The event fires with a tiny `buffer_bytes` payload (`race_coordinator.go:423-429`).
UI consumers must NOT assume completed buffers (the buffer may still be filling).
Buffered mode fires the same event post-completion.

### D13. Winner-validity (first-byte winner may fail)

A first-byte winner might later fail (a loser might have succeeded). The client sees
partial bytes + a terminal in-band SSE error envelope. **No retry.** Leader-ACCEPTED
documented degradation.

### D14. +100ms worst-case first-byte latency (tick granularity)

The manage-loop ticker (`race_coordinator.go:364`) has 100ms granularity. Worst case
a first byte arrives just after a tick ⇒ next tick fires +100ms later. Accepted.
Irrelevant to the acceptance gate (mock inter-chunk delays ≫ tick).

### D15. bufferstore entry+Done double-save (benign)

In live mode, the Done capture (`handler.go:1182-1187`) is authoritative and complete.
In buffered mode, both the entry capture (`:1053-1058`) AND the Done capture fire.
The bufferstore's `Save` writes to a **unique temp file + atomic rename**
(`pkg/bufferstore/store.go:32-59`), so even if both writes happened the second would
overwrite the first. The test `TestLiveRelay_BufferStoreDoubleSave_Benign` asserts
EXACTLY ONE save per upstream response.

---

## 6-path coverage

Per the plan's 5.10 fixed rollout order (each path differs from the prior by exactly
one architectural surface, so a failure in path N bisects against the last-green
path N-1):

| # | Path | Plan pin | New-matrix goldens | Pre-feature parity pin |
|---|------|----------|--------------------|------------------------|
| 1 | Anthropic→Anthropic passthrough (real fixture) | `handler_anthropic.go:1225 / :1679 handlePassthroughStreamResponse` | `TestRealStreaming_ParityMatrix_AllPaths/1_AnthropicPassthrough_{text,thinking,tool_call,usage}` (4 goldens) | same subtest names |
| 2 | Anthropic→OpenAI-wire external translation | `handler_anthropic.go:819/:851` | `.../2_AnthropicExternalTranslation_{text,thinking,tool_call,usage,role_only,usage_first,comment_first}` (7 goldens) | same subtest names |
| 3 | Anthropic→OpenAI-wire internal translation | `doAnthropicInternalRequest` at `handler_anthropic.go:600-685` | `.../3_AnthropicInternalTranslation_{text,thinking,tool_call,usage}` (4 goldens; role_only/usage_first/comment_first don't apply — internal handler normalizes into typed events) | same subtest names |
| 4 | `/v1/chat/completions` OpenAI race path | `handler.go` race-coord+relay | `.../4_OpenAIRacePath_{text,thinking,tool_call,usage,role_only,usage_first,comment_first}` (7 goldens) | same subtest names |
| 5 | Ultimate external stream | `pkg/ultimatemodel/handler_external.go:438-500` | NO new goldens (out of scope for proxy package; ExecuteOptions-level caveat below) | `pkg/ultimatemodel/handler_external_test.go TestUltimate_HeaderDispatch` (existing) |
| 6 | Ultimate internal stream | `pkg/ultimatemodel/handler_internal.go:484-755` | NO new goldens (same ExecuteOptions-level caveat) | `pkg/ultimatemodel/handler_internal_test.go TestUltimate_HeaderDispatch` (existing) |

**ExecuteOptions-level caveat (paths 5+6):** the ultimatemodel-package
`streamResponse` / `handleInternalStream` methods are package-private
(lowercase) and are reached only via the proxy's full
`HandleChatCompletions` pipeline (which requires the ultimate-trigger
schedule to fire). The `ExecuteOptions` envelope is the proxy's only
reach-into-the-package handle, and the existing ultimatemodel tests
already exercise it directly (with the same buffered/live semantics
and the C1 estimator parity pin).

**Coverage scope summary:**
- **Paths 1-4 (new goldens via this commit):** 4 + 7 + 4 + 7 = **22 new goldens** under
  `pkg/proxy/testdata/real_streaming_golden/`, covering text/thinking/tool_call/usage on all
  four paths and role_only/usage_first/comment_first on the two OpenAI-wire paths.
- **Paths 5-6 (existing ultimatemodel tests):** unchanged; pinned by
  `pkg/ultimatemodel TestUltimate_HeaderDispatch` + `TestUltimateCapture_LiveMode_IdenticalToBuffered`.

The `TestRealStreaming_*` files cover paths 1-4 directly (the surfaces reachable
from the proxy package). Paths 5-6 are pinned by existing ultimatemodel-package
tests.

---

## R2 Prune / entry-capture gating (LOCKED)

Live mode keeps the **capture-side accumulator** for the tokenizer-fallback
estimator. Without this, live-mode `CompletionTokens` would be 0 whenever upstream
omits a `usage` chunk.

**Gating (single switch):**

- **Prune** (`handler.go` 5 sites: `1082, 1122, 1167, 1238, 1357`) — gated to buffered
  mode. Live mode: Prune never fires ⇒ buffer holds the full response (matches
  today's buffered-mode memory profile; no regression).
- **Entry raw-capture** (`handler.go:1053-1058`) — gated to buffered mode. Live mode:
  the Done capture (`:1182-1187`) is authoritative and complete.
- **Capture-side accumulator in ultimate paths** (`handler_external.go` / 
  `handler_internal.go`) — kept in BOTH modes; the per-chunk wire write appends
  the same bytes here so the fallback estimator sees byte-identical completion
  text. Bufferstore Done capture matches the full stream.

**Regression pins:**
- `pkg/proxy/race_coordinator_live_test.go TestLiveRelay_R2_EstimatorUnderPrunePressure`
- `pkg/ultimatemodel/handler_external_test.go TestUltimateCapture_LiveMode_IdenticalToBuffered`
- `pkg/ultimatemodel/handler_internal_test.go TestUltimateCapture_LiveMode_IdenticalToBuffered`

---

## Rollback procedure (task 5.9)

Two options, both viable. **Recommendation: option (b)** for minimal code change.

### Option (a) — Feature flag in `Config` (programmatic)

Add a global toggle (e.g., `ULTIMATE_DEFAULT_MODE=live|buffered`) and default to
`live`. To roll back globally:

```bash
# Restart with the global override
ULTIMATE_DEFAULT_MODE=buffered ./llm-supervisor-proxy
```

This requires a code change (env-var wiring + config plumbing + read-site). Not
currently shipped — no `Config` field exists.

### Option (b) — Client default header on the load balancer (recommended)

Add the `X-LLMProxy-Buffer-Response: true` header to **every outgoing request** from
the load balancer (HAProxy / nginx / Envoy). One-line change in the LB config:

```nginx
# nginx example — append the opt-in to every upstream call
proxy_set_header X-LLMProxy-Buffer-Response "true";
```

This reverts the proxy to bit-for-bit pre-feature behavior without touching the
proxy code. **This is the recommended rollback path** — it requires zero proxy
changes and zero process restarts.

### Pin: every code path respects the header

`TestRealStreaming_RollbackViaHeader` exercises the rollback on every reachable
path (Anthropic passthrough + OpenAI race path + Ultimate external stub). The
parity suite (`TestRealStreaming_ParityMatrix_AllPaths`) verifies the header-present
wire bytes match the committed goldens byte-for-byte.

---

## Parity-failure bisection procedure (task 5.11)

When `TestRealStreaming_ParityMatrix_AllPaths` fails on a path, follow this
**mechanical** procedure — no heuristic guessing.

1. **Verify the path is in the header-PRESENT branch.** Confirm
   `X-LLMProxy-Buffer-Response: true` is set in the failing test's request. If not,
   the test is exercising live mode (wrong fixture).
2. **Compare wire bytes vs golden.** Look at the first divergent line (printed in
   the failure message). Diff the line against the golden under
   `pkg/proxy/testdata/real_streaming_golden/<name>.json`.
3. **Check the §6 master test-flip inventory** (plan.md §7) for that surface. If a
   test on that path was flipped, verify the flip was applied correctly (e.g.,
   `TestStreamBuffered` should set the header; verify it does).
4. **Check the path's bufferMode threading** (Phase 1 plumbing). The 6 paths have
   different `bufferMode` plumbing sites:
   - OpenAI race: `handler.go:924` → coordinator ctor
   - Anthropic (all variants): `handler_anthropic.go` request context
   - Ultimate external: `executeExternal` opts
   - Ultimate internal: `executeInternal` opts
   Verify the call site at the failing path actually consumes `bufferMode=true`
   and threads it through.
5. **Bisect the commit range** using `git bisect` against the failing fixture:

```bash
git bisect start
# The harness landed in commit d608014; bisect from the harness-era
# head backward to the pre-feature baseline.
git bisect bad d608014
git bisect good 9842c77
# Run the failing fixture at each step:
go test -count=1 -run TestRealStreaming_GoldenRecorder/<name> ./pkg/proxy/ -update
go test -count=1 -run TestRealStreaming_ParityMatrix_AllPaths ./pkg/proxy/
```

The first commit that flips the test from PASS to FAIL is the regression.

---

## Test fixtures & goldens

- **Goldens:** `pkg/proxy/testdata/real_streaming_golden/*.json` — one
  per path × content variant (paths 1-4 from the parity matrix).
  Path 1 covers text/thinking/tool_call/usage (4 goldens); paths 2
  and 4 additionally cover role_only/usage_first/comment_first
  (provider-shaped first chunks, plan Files row 4) for 7 goldens
  each; path 3 covers text/thinking/tool_call/usage (4 goldens).
  Total: 22 proxy-package goldens. Paths 5-6 have NO goldens in this
  package — they live in `pkg/ultimatemodel/handler_*_test.go`. The
  TestUltimate_HeaderDispatch tests pin buffered == live wire bytes
  (which transitively pins buffered == pre-feature).
  Generator:
  `go test -count=1 -run TestRealStreaming_GoldenRecorder ./pkg/proxy/ -update`.
- **Determinism rules:** volatile fields stripped before byte comparison
  (`chatcmpl-*` IDs, `msg_*` IDs, `created` timestamps, `: connected`, `: keepalive`,
  `[DONE]` markers). Anchored regex (no global comma rewrites — see
  `volStripPatterns` doc in `real_streaming_parity_test.go`).

---

## Reference

- Plan: `.agents/shared/planning/real-streaming-default/plan.md`
- Architecture: `.agents/shared/planning/real-streaming-default/architecture-recommendation.md`
- Tracking: `.agents/approver/real-streaming-default-tracking.md`
- Parity tests: `pkg/proxy/real_streaming_parity_test.go`
- First-byte tests: `pkg/proxy/real_streaming_firstbyte_test.go`
- Golden generator: `pkg/proxy/real_streaming_golden_test.go`
- Phase 2-4 test battery: `pkg/proxy/race_coordinator_live_test.go`,
  `pkg/proxy/handler_anthropic_live_test.go`
