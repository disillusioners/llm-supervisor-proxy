# Plan: Real-Streaming Default (with `X-LLMProxy-Buffer-Response` opt-in)

Branch: `feature/real-streaming-default` @ 9842c77
Date: 2026-08-27
Author: planner[v2] via plan-creation worker
Status: Draft — Ready for Implementation (addendum-integrated + architect-resolved + narrow-amendment + micro-cleanup + approver-iteration-001 amended 2026-08-27)

> **Narrow amendment pass (2026-08-27 — review pass — C2 deadlock + C1 usage-parity + M1-M7/#8/#9):**
> Review verdict was REJECTED narrowly (2 CRITICAL directive defects; ~95% verified sound). Fixes
> applied against the architect-amended content, surgical replacements only:
> **C2 (deadlock)** — task 2.2's "take the `:705-722` error branch + explicit `cancelAll()`"
> directive re-acquires `c.mu` while the deadline guard holds it (`race_coordinator.go:650-651` +
> `:760-766`); re-pinned to capture state under lock, RELEASE, then cancel (precedent
> `race_coordinator.go:441-446`); only one form pinned (inline `req.Cancel()` under held lock
> explicitly NOT pinned).
> **C1 (usage undercount)** — Phase 4 tasks 4.2/4.3 said "drop `var buf bytes.Buffer`" but
> `handler_external.go:483` and `handler_internal.go:710` feed the tokenizer-fallback estimator
> from `buf.Bytes()`; tasks reworded to keep a CAPTURE-SIDE accumulator in live mode (estimator
> parity = not an accepted degradation), test 4.4 gains MANDATORY no-usage-chunk fixture, §8
> synced.
> **M1** truth-table sketch uses `EqualFold("true")` only → aligned with locked 10-variant
> semantics.
> **M2** stale "or reuse ... depending on Q4" hedging removed; `bufferMode` and
> `streamingNonRetryable` are distinct flags with different lifecycles.
> **M3** 0-byte guard scoped to `liveFirstByteGate = !bufferMode && isStream` (not
> `!bufferMode` alone — would change non-stream deadline behavior); non-stream deadline
> regression test added to Phase 2 test strategy.
> **M4** Prune enumeration corrected: FIVE sites (`handler.go:1082/:1122/:1167/:1238/:1357`)
> plus executor-side fallback estimators (`race_executor.go:938/:1649`) as unlisted consumers;
> gating stays single-switch.
> **M5** Phase 3 content/tool_call arms — translator output REPLACES OpenAI-chunk writes
> (not "ALSO emits"); `arc.accumulatedResponse` population is the typed-event entry's job.
> **M6** Recorded real-Anthropic-stream SDK-interleave verification promoted from risk-table
> prose to Phase 3 EXIT-CRITERION item 6.
> **M7** 429 wording softened — true only for HTTP-status 429s; in-stream error chunks
> never classify as rate-limit (`isStreamErrorChunk` returns plain `fmt.Errorf`, not
> `*ProviderError`).
> **#8** Pinned gate form (ticker arm `:406-435` + `onceStream` `:431` + winner-nil `:653-655`)
> restated as Phase-2 CONSTRAINT (not only §10 assumption #11).
> **#9** Explicit `bufferStore.Save` double-save test added (task 2.4.1).
> **Deferred (intentionally unapplied):** ring-buffer test, CoordinatorOptions refactor,
> static harness assertion, heartbeat-interleave test, latency-bound quantification,
> §5.1 fixture matrix, **§7 row 8** (not verified by glob in this pass), and the
> `internal_handler.go:32-41` invariant comment update. Two deferred items APPLIED in passing:
> §7 row 8 (verified by inspection — `race_coordinator_test.go`, `race_coordinator_credfailover_test.go`,
> `handler_lb_phase5_test.go`); and a one-line Phase 3 task to update the stale invariant
> comment at `internal_handler.go:32-41` (dual-mode wording). All other deferred items stay
> untouched per dispatcher pre-authorization.

> **Micro-cleanup pass (2026-08-27 — review pass — 4 warnings + 4 suggestions applied):**
> Reviewer APPROVED with 4 editorial warnings + 4 suggestions; all 8 applied (editorial only,
> no substantive changes; `technical-analysis.md` untouched). **W1:** added task 2.2
> sentence to remove the `defer c.mu.Unlock()` at `:651` in the live-guard form (avoid
> double-unlock fail-fast panic). **W2:** appended "(sequencing per task 2.2: `c.mu.Unlock()`
> before `cancelAll()`)" at `:349-353` and `:531-532` (the two stale pre-amendment shorthand
> spots). **W3:** rewrote the three Phase 1 Q4-reuse-hedging residuals (`:434-436`, `:464`,
> `:488`) to the resolved distinct-flags form. **W4:** synced the stale three-site Prune
> lists at `:1150`, `:1268`, and the `:535-536` row to the five-site list
> (`handler.go:1082/:1122/:1167/:1238/:1357`). **S5:** filename nit
> `bufferstore/bufferstore.go` → `pkg/bufferstore/store.go`. **S6:** added mandatory
> no-usage-chunk fixture to the Phase 4 test-strategy bullet (mirroring task 4.4). **S7:**
> reworded the Phase-2 CONSTRAINTS entry to distinguish `req.Cancel()` (acquires `r.mu` only)
> from the `cancelAll`→`cancelAllExcept`→`c.mu` chain. **S8:** added optional cross-ref to
> task 3.5's "ONLY wire-write path" from the Phase 4 internal-thinking risk row.

> **AMENDED 2026-08-27 (approver iteration 001 — parser default + ultimate heartbeat race):**
> Approver verdict REJECTED (2 blocking + non-blocking notes folded in). **Blocking 1 (Phase 1
> parser default inversion):** the `parseBufferResponseHeader` sketch switched on `r.Header.Get`
> value and returned `true` for `""`, but `Get` returns `""` for BOTH absent and empty-value
> headers ⇒ absent-header requests (the new default case) mapped to BUFFERED, inverting the
> feature. Fixed: mechanism is now presence-aware (`r.Header.Values("X-LLMProxy-Buffer-Response")`;
> `len==0` ⇒ absent ⇒ live streaming); truth table rewritten to 4 rows (PRESENT+truthy / PRESENT+empty
> ⇒ buffered; PRESENT+falsy / PRESENT+unknown ⇒ live; ABSENT ⇒ live); Phase 1 Files-row sketch,
> task 1.1 design notes, and task 1.4 test fixtures all updated to cover empty-value-PRESENT
> and unknown-value rows. **Blocking 2 (Phase 4 unserialized heartbeat race):** the plan cited
> only `pkg/proxy/heartbeat.go` (writeMu-guarded); never mentioned `pkg/ultimatemodel/heartbeat.go:21+`
> where `sendHeartbeat` writes to `w` with NO mutex. Fixed: pinned a per-request write-mutex
> discipline on BOTH ultimate live paths (mirror proxy-side `rc.writeMu`); Files row 1
> (`pkg/ultimatemodel/handler.go`) updated; Phase 4 risks row added (covers
> `pkg/ultimatemodel/heartbeat.go` heartbeat vs relay); §8.1 PRESERVED heartbeat row updated
> to cover BOTH proxies; Phase 4 test strategy gains a `-race` test with shortened heartbeat
> interval. **Non-blocking notes folded in:** (1) Phase 3 exit-criterion 6 redefined as
> SYNTHESIZED interleaved OpenAI-chunk fixture → translator → Anthropic SDK parser
> (recorded Anthropic SSE is sequential, vacuously satisfiable as originally written); (2)
> internal-variant mid-stream error `Finalize` site pinned (`internal_handler.go:477-480` error
> path returns immediately — `Finalize()` runs at the call-c of `handleInternalError` if any);
> (3) `ParityVsBatch` event-SET equality defined precisely: block-structure +
> concatenated-payload equality (batch emits 1 aggregated delta vs N incremental); (4)
> citation fixes — `pkg/token/prompts.go` → `pkg/proxy/token/prompts.go:57-114`; Phase 4
> "internal non-stream `:830-840`" → `handler_internal.go:313` (the
> `handleInternalNonStream` function); M7 mechanism wording tightened; executor estimator
> sites `:939/:1650` → `:938/:1649`; (5) Phase 5 — replace rerun-on-flake with event-based
> assertion for first-byte timing; add parity-failure bisection procedure; state 6-path
> rollout order; track §10 #11 (gate-form assumption) for safety re-review; (6) Phase 2 —
> pin `isStream` threading (compute `liveFirstByteGate` in `handler.go` BEFORE the constructor
> call, not from within the constructor — removes the isStream-flow-into-coordinator risk);
> (7) Phase 4 task 4.3 — settle the `rawChunks` dead accumulator (REMOVE; was only used for
> fallback token counting and is supplanted by the capture-side accumulator); (8) Phase 3 —
> pin the `: connected\n\n` preamble on live entry (emit once on the live path before first
> forwarding).

> **Addendum integrated (2026-08-27).** The parallel `technical-analysis.md` worker
> returned with material corrections. This plan was updated to integrate them:
> **L1 header value semantics** are LOCKED (empty header = buffered, full truth table); **L5
> deadline premise corrected** — per-model `ReleaseStreamChunkDeadline` is DORMANT, the
> ACTIVE deadline is the global `StreamDeadline` inside the race coordinator (which
> real-streaming mode bypasses); **Q2 divergent option (b)** documented with the
> thinking-block emission question answered (lock: live mode emits `thinking_delta` blocks
> on the wire, deliberate wire-shape change); **L8 addendum spec points i + ii** —
> pre-first-byte upstream failure surfaces as in-band SSE error envelope, AND the
> Anthropic sequential fallback loop at `handler_anthropic.go:256` exits after first
> byte. **Q1 confirmed** (Option A) and **Q4 confirmed** (`rc.streamingNonRetryable`
> reuse, safe to repurpose). All edits are inline; no companion file.

> **Architect resolution integrated (2026-08-27).** The leader RESOLVED R1, R2, Q1–Q4;
> the architect deepened the mechanics via 3 dispatched analysts (data-flow,
> structural, resilience) and amended this plan inline. Headlines: **R1 →
> first-forwardable-byte-wins** — the race coordinator is NOT bypassed; the winner gate
> moves to "first forwardable data chunk" (`race_coordinator.go:406-435` predicate swap),
> `cancelAllExcept` fires mid-stream, the EXISTING `streamResult` loop is the live relay
> (no `streamLiveRelay`). **R2 → neither prior claim was right** — the usage estimator
> consumes raw `GetAllRawBytesOnce` bytes (`handler.go:1220`); mid-stream Prune truncates
> them in live mode; fix = gate Prune + entry raw-capture to buffered mode. **Q1 →
> single relay path** (Phase 2 = gate redefinition, 3 files + tests). **Q2 → incremental
> translator** mechanics pinned (typed-event entry; single-open-block interleave ruling;
> deferred `content_block_stop`; `Finalize` after `toolCallBuffer.Flush`). **Q3 → header
> wins**; StreamDeadline in live mode = "no forwardable byte within deadline ⇒ in-band
> SSE error + cancelAll" (requires a NEW 0-byte guard in `handleStreamingDeadline`
> `:661-722` — today it promotes 0-byte winners). **Q4 → reuse `streamingNonRetryable`**
> set on first successful client write, type-changed to `sync/atomic.Bool`.
> Full mechanics + risk register + evidence: `architecture-recommendation.md` (sibling).
> Amendments list: `architecture-recommendation.md` §6.

> **Note on sibling artifacts.** This file is the single planning artifact. A parallel worker
> owns `.agents/shared/planning/real-streaming-default/technical-analysis.md` (a detailed
> investigation report, file:line-cited) and the architect owns
> `architecture-recommendation.md` (resolved-decision mechanics). Where the plan and the
> investigation disagree, the **investigation wins on evidence** (file:line cites); the plan
> wins on phase shape and acceptance gates; the architect's resolution supersedes both on
> the resolved items. The investigation is the PRIMARY evidence source for this plan.

---

## 1. Objective

Default streaming on `llm-supervisor-proxy` becomes REAL streaming — chunks forwarded to the
client as they arrive, no full-response accumulation. The current fully-buffered supervised
behavior becomes opt-in via request header `X-LLMProxy-Buffer-Response`. Header present ⇒ current
behavior 100% unchanged (bit-for-bit semantics). Real-streaming mode loses features that
**architecturally require buffering** — accepted by design and documented.

A feature is complete when, for a streaming request with no `X-LLMProxy-Buffer-Response` header,
the **first content byte the upstream emits is on the client wire before upstream completion**,
while for a streaming request with the header present the proxy still gates forwarding on a
completed race cycle (today's behavior). Non-streaming requests are unaffected in both modes.

---

## 2. Background (from `research-analysis.md`)

The wanderer's three-trace investigation confirmed:

| # | Path (stream=true) | Today's behavior | Source of buffering |
|---|--------------------|------------------|---------------------|
| 1 | `/v1/chat/completions` race | race coordinator waits for winner (`race_coordinator.go:649-723` partial-release + `handleStreamingDeadline`); then `streamResult` relays buffered chunks (`handler.go:1038-1380`) | race winner gate + retry buffer (`streamBuffer.Add`/`NotifyCh`) |
| 2 | `/v1/chat/completions` ultimate-internal | `pkg/ultimatemodel/handler_internal.go:484-735` builds `buf` across the event channel, single end-of-stream `w.Write+flusher.Flush` at `:727-728` | per-event `buf.Write` + end-of-stream single write |
| 3 | `/v1/chat/completions` ultimate-external | `pkg/ultimatemodel/handler_external.go:438-500` accumulates `buf` across the upstream scanner; single `w.Write+flusher.Flush` at `:499-500` | per-line `buf.Write` + end-of-stream single write |
| 4 | `/v1/messages` Anthropic→Anthropic | **ALREADY REAL STREAMS** (template) — `handlePassthroughStreamResponse` (`handler_anthropic.go:1225-1290`) | n/a (passthrough only) |
| 5 | `/v1/messages` Anthropic→OpenAI translate (passthrough+translate) | `handleAnthropicStreamResponse` / `handleAnthropicInternalStreamResponse` call `translator.TranslateBufferedStream(openaiBody, …)` (`handler_anthropic.go:728`) AFTER all chunks are buffered; `pkg/proxy/translator/stream.go:19` is BATCH | `flushingResponseRecorder` (Flush no-op at `handler_anthropic.go:591-598`) + `translator.TranslateBufferedStream` |
| 6 | `/v1/messages` internal-Anthropic | recorder → batch translator | recorder + batch translator |

**Two islands already real-stream** (paths 4 and — debatably — the relay loop in path 1; the
delay in path 1 is the **race winner gate**, not the relay). **Nothing else is real-time
today.** No feature intrinsically requires full-response buffering; the blockers are
architectural gates.

The investigation further confirms:

- `rc.streamingNonRetryable bool` exists in `handler_helpers.go:102`, declared but never set
  — reserved for this feature (Q4 answer: reuse it).
- `rc.streamBuffer bytes.Buffer` (`handler_helpers.go:76`) is part of the post-winner drain in
  the current buffered path; real-streaming mode bypasses it entirely.
- `bypassInternal` header parsing at `handler_functions.go:123` is the house style:
  `strings.EqualFold(r.Header.Get("x-llmproxy-bypass-internal"), "true")`. The new header
  parses identically.
- `flushingResponseRecorder` (`handler_anthropic.go:591-598`) has `Flush() {}` no-op — real
  streaming needs the real `w` threaded through.
- `thinkingSink` invariant at `internal_handler.go:32-41` and the "case thinking" silent-write
  at `:379-396` must be preserved — thinking bytes must NEVER reach `w` when the recorder is
  being fed (today's Anthropic-internal path). In real-streaming mode the recorder goes away;
  the silent-write invariant translates to "thinking events emitted as Anthropic `thinking_delta`
  blocks on the wire, content events emitted as `text_delta` blocks, in the new incremental
  translator."

---

## 3. Locked Decisions (Restated — Plan Complies, Does Not Re-Litigate)

| # | Locked decision | Citation |
|---|-----------------|----------|
| L1 | Default mode is **real streaming**; opt-in is `X-LLMProxy-Buffer-Response` header. **Header value semantics LOCKED** (dispatch L1 + approver iteration 001 correction): presence-aware read (`r.Header.Values("X-LLMProxy-Buffer-Response")`; `len(vals)==0` ⇒ ABSENT ⇒ live streaming). **PRESENT** with value in `{"true","1","yes","on"}` (case-insensitive `strings.EqualFold`) OR PRESENT with empty value ⇒ buffered. **PRESENT** with value in `{"false","0","no","off"}` (case-insensitive) OR PRESENT with any OTHER non-empty value (unknown) ⇒ live streaming (opt-in must be explicit and correctly spelled). **ABSENT** ⇒ live streaming. The parser is case-insensitive on the value (`strings.EqualFold`); `r.Header.Values` canonicalizes the header key. This is NOT an open question. | dispatch message, approver iteration 001 |
| L2 | Header present ⇒ **bit-for-bit identical** to current behavior (parity suite proves it) | dispatch message |
| L3 | **Anthropic→Anthropic passthrough** at `handler_anthropic.go:1225` is the reference template — already real-streams; **verify untouched** | dispatch message, `technical-analysis.md` §1a |
| L4 | **RESOLVED (leader + architect):** no retry/fallback/racing **AFTER the first forwarded byte; before that, unchanged**. Winner selection is redefined to the **first forwardable data chunk** (R1-variant — see `architecture-recommendation.md` §1.1): the coordinator's winner gate moves from `IsCompleted() && err==nil` to `TotalLen()>0 && err==nil`; configured racing and fallback chains operate unchanged until the winner fires; `cancelAllExcept(winner)` fires mid-stream; the existing `streamResult` loop relays live. | dispatch message, `architecture-recommendation.md` §1.1 |
| L5 | **RESOLVED (architect mechanics, supersedes prior text):** the per-model `ReleaseStreamChunkDeadline` field at `pkg/models/config.go:127` is **DORMANT** — zero call sites in `pkg/proxy` — and stays dormant in both modes. The **ACTIVE** deadline is the global `c.cfg.StreamDeadline` (`pkg/config/config.go:71`, default `110*time.Second` per `:165`) inside the race coordinator (`race_coordinator.go:370`). Buffered mode: coordinator + deadline run exactly as today. Live mode: the coordinator **RUNS** with the first-byte winner gate (NOT bypassed); the deadline's meaning becomes **"no forwardable byte within StreamDeadline ⇒ terminal in-band SSE error envelope + all attempts cancelled"** — this requires a NEW 0-byte guard in `handleStreamingDeadline` (`race_coordinator.go:661-722`; today it promotes a 0-byte winner and keeps streaming). Hard deadline (`MaxGenerationTime`) unchanged in both modes. | `architecture-recommendation.md` §1.1.4 |
| L6 | Heartbeat / usage metering / inline content tap / normalizers / per-chunk MiniMax translator / tool-call buffering / credential LB pre-stream `GetOrSelect` / bufferstore debug dumps / ultimate trigger counting **all PRESERVED in real-streaming mode**. (Architect note: bufferstore + tokenizer-fallback estimator require the Phase 2 Prune/entry-capture gating to be preserved COMPLETE — see §8.2 and `architecture-recommendation.md` §1.2.) | dispatch message |
| L7 | `model_credential_selected` event asymmetry **NOT touched** — Anthropic path (`pkg/proxy/internal_handler.go:146-256`) discards `NewlyBound` and never publishes, OpenAI race path is the sole publisher (`race_executor.go:134`, `race_coordinator.go:791-803`) | dispatch message, `technical-analysis.md` (current project blueprint "Credential Load-Balancing Engine" §Events) |
| L8 | Acceptance test design: **first content byte forwarded BEFORE upstream completion**, deterministic via mock upstream with blocking/partial writes + client-side read timing. **Per addendum spec point i:** pre-first-byte upstream failure surfaces as in-band SSE error envelope (SSE headers + `: connected` already written at `handler.go:882-889`); **per addendum spec point ii:** the Anthropic path's sequential model-fallback loop (`handler_anthropic.go:256`) needs the same treatment in live mode — resolved as a loop-top guard (see Phase 3 §3.4). Under R1-variant the first-byte gate itself is additionally tested: winner selected at first forwardable chunk, relayed before upstream `[DONE]`. | dispatch message, `architecture-recommendation.md` §5 |

---

## 4. Open Design Questions — RESOLVED (2026-08-27, leader + architect)

### Q1 — OpenAI race path: R1-VARIANT (first-forwardable-byte-wins gate redefinition)

**RESOLVED: the winner gate is REDEFINED, not bypassed.** Live mode keeps the race coordinator
with all racing/fallback machinery intact; only the winner-eligibility predicate changes.

**Mechanics (verified, `architecture-recommendation.md` §1.1):**

- **Predicate (live mode, `isStream==true` only):** `req.GetError() == nil &&
  req.GetBuffer().TotalLen() > 0` — the attempt's `streamBuffer` transitions from 0 chunks to
  ≥1 chunk. `TotalLen()` is an atomic read (`stream_buffer.go:305-313`); `GetBuffer()` is
  lock-guarded (`race_request.go:190-194`). Toolcall buffering sits strictly BEFORE Add on
  both executor paths (`race_executor.go:1548-1577` external, `:697-928` internal), so every
  Add-ed chunk is post-buffering — genuinely forwardable.
- **Role-only first chunks COUNT** (today's relay forwards ALL buffer chunks,
  `handler.go:1063-1076`; the internal path merges role+content, `race_executor.go:704-719`).
  Committing on them preserves byte-identical chunk order vs buffered mode; content bytes
  reach the wire mid-stream by construction via `notifyCh` (`stream_buffer.go:145-149`).
  Usage-only / finish_reason-only / comment chunks also count (commit-on-alive). Preamble
  (`handler.go:886-889`) and heartbeats (`heartbeat.go:63`) never enter the buffer — cannot
  trigger.
- **Evaluation site:** the existing manage-loop eligibility block (`race_coordinator.go:406-435`)
  on the existing 100ms tick (`:364`); everything downstream reused verbatim (index preference
  `:412`, `race_winner_selected` `:423-429`, `onceStream` close `:431`, cancel-and-exit
  `:440-446`). `liveFirstByteGate = !rc.bufferMode && rc.isStream` threaded via the constructor
  (call site `handler.go:924`). **Must be isStream-scoped** — non-stream keeps `IsCompleted`
  (`handler.go:962`). Worst-case +100ms first-byte latency (tick granularity) — accepted.
- **Predicate hazard (fixed in Phase 2):** external error chunks are Added BEFORE the error
  check (`race_executor.go:1573` vs `:1583`) — a dying attempt could transiently win. Fix:
  move `isStreamErrorChunk` before the Add loop (behavior-neutral in buffered mode).
- **Relay:** NO new relay function. `WaitForWinner` returning at the first-byte winner + the
  EXISTING `streamResult` body (`handler.go:1038-1380`) IS the live relay (entry drain
  `:1062-1083`, `NotifyCh` loop `:1101-1123`, per-chunk `writeMu` writes, tap, error envelope —
  all unchanged). Phase 2's old `streamLiveRelay` task is ELIMINATED.
- **Concurrency safety (resilience worker):** no new lock-order inversion (`c.mu` released at
  `race_coordinator.go:441` before `cancelAllExcept` — mirrors today); relay's buffer snapshot
  (`race_request.go:190-194`) stays valid after Close per the 9842c77 SIGSEGV fix; worst-case
  interleaving (loser mid-Add + mid-stream cancel + client disconnect) traced safe.
  `race_winner_selected` now fires mid-stream with a tiny `buffer_bytes` payload — UI
  consumers must not assume completed buffers (documented).

**Where this differs from the original Option A framing:** Option A (bypass + single direct
attempt) is SUPERSEDED — the leader's R1-variant resolution keeps the coordinator (preserving
pre-first-byte fallback chains and credential failover for free) at the cost of one predicate
swap. Multi-credential and multi-model requests behave identically pre-first-byte in both
modes.

### Q2 — Anthropic translation: NEW incremental streaming translator (option a) — RESOLVED with mechanics

> **Divergent recommendation from `technical-analysis.md` (addendum 2026-08-27):** the
> parallel worker recommends option (b) — default mode buffers ONLY the two
> translation-required Anthropic variants (external/LiteLLM
> `handler_anthropic.go:819/:851` and internal-non-Anthropic recorder
> `:612/:728`), CONDITIONAL on acceptance-gate strictness, because under (b) those variants
> violate "header absent ⇒ first content byte forwarded before upstream completion".
> I keep option (a) but explicitly accept option (b) as architect-selectable if the new
> translator slips. The thinking-block emission question is answered below.

**Recommendation: NEW incremental/streaming Anthropic translator.** (Leader RESOLVED: option (a) primary; option (b) only as explicitly-flagged slip risk per variant.)

**Rationale.**

- `TranslateBufferedStream` (`pkg/proxy/translator/stream.go:19`) cannot be flag-flipped to
  streaming because it builds a `StreamState` from all chunks then calls
  `generateAnthropicEvents(state)` once. Anthropic `message_start`, `content_block_start`,
  `content_block_delta`, `content_block_stop`, `message_delta`, `message_stop`, and the
  required `ping` event must be emitted in order as the OpenAI chunks arrive — that's a
  different state machine, not a different framing.
- The existing `StreamState` (`pkg/proxy/translator/stream.go:21-24`) and helpers
  (`extractChunkContent`, `formatContentBlockStart`, `formatContentBlockDelta`,
  `formatThinkingBlockDelta`, `formatInputJsonDelta`, `formatContentBlockStop`,
  `formatSSEEvent`) are reusable — the new translator is a thin per-chunk state machine that
  drives them.

**Thinking-block emission question (addendum spec point — answer locked here).** The
addendum flagged a 🔴 open question (OQ3): the Anthropic→Anthropic passthrough variant
(`handlePassthroughStreamResponse` at `handler_anthropic.go:1225`) emits Anthropic
`thinking_delta` blocks on the wire (because the upstream IS Anthropic and carries thinking
natively); the translation variants (`handleAnthropicStreamResponse` at `:795` and
`handleAnthropicInternalStreamResponse` at `:720`) today do NOT emit thinking blocks on
the wire — they go through `TranslateBufferedStream` which DOES build a thinking content block
from accumulated `reasoning_content` per `stream.go:176-186`, but the
internal-`flushingResponseRecorder` path suppresses thinking bytes from reaching the recorder
(`internal_handler.go:379-396`).

**Locked stance (option (a) adoption):** the new incremental translator emits an Anthropic
`thinking` content block on the wire when an OpenAI chunk carries `reasoning_content`,
matching today's batch-translator behavior at `stream.go:176-186`. For the
internal-non-Anthropic variant, the live-mode `thinkingSink` capture (`internal_handler.go:32-41`)
is preserved AND the wire-side emission is ADDITIONAL (vs today's "sink only, no wire").
**Acceptance gate implication: the wire shape changes** — buffered mode emits no thinking
blocks for the internal-non-Anthropic variant; live mode DOES emit them. This is a
**deliberate wire-shape change** for that variant in live mode only, documented in
`docs/real-streaming-default.md`. Parity tests assert byte-identity in buffered mode and
explicitly assert the new thinking-block emission in live mode.

**Resolution mechanics (architect, 2026-08-27 — pins the design):**

- **API:** `pkg/proxy/translator/incremental_stream.go` — `NewIncrementalStreamTranslator(originalModel string)`;
  `ProcessChunk(rawOpenAIChunk []byte) ([]string, error)` (caller does SSE framing via
  `ParseOpenAISSEChunk`, `stream.go:346-366`; returns Anthropic SSE events for THIS chunk only);
  `Finalize() ([]string, error)` (on `[DONE]` OR stream-end-without-`[DONE]`); typed-event entry
  `ProcessEvent(ev)` for the internal variant.
- **State machine:** NotStarted → MessageStarted → {TextBlockOpen, ThinkingBlockOpen,
  ToolBlockOpen} interleave → Finalizing → Done. Guards: `messageStartSent && pingSent` before
  any `content_block_*`; open-block-before-its-deltas; `message_delta` before `message_stop`.
  First chunk emits `message_start` + `ping` ONCE (`stream.go:148-169` verbatim; flags gate
  double-emission) — even for empty streams (batch parity, `:148-169` unconditional).
- **Interleave ruling — single-open-block-of-each-kind:** at most one open `text` and one open
  `thinking` block; deltas route by kind without close-and-reopen. Real Anthropic streams
  interleave thinking/text (the passthrough parser switches on both delta types,
  `handler_anthropic.go:1263-1278`); batch's grouped order (`:176-197`) is an artifact of
  accumulation, not a wire requirement. **Documented wire difference live-vs-buffered for
  interleaved upstreams** — pinned by a run-both-translators parity test (event-SET equality;
  ORDER equality for non-interleaved fixtures; ORDER inequality pinned for interleaved).
- **Tool calls:** per-index tracking keyed by OpenAI `tool_calls[i].index`; `content_block_start`
  on first delta carrying id+name; `input_json_delta` per fragment (accumulate `partial_json`,
  mirror `stream.go:135-137`); **`content_block_stop` DEFERRED to `Finalize()`** (differs from
  batch's per-tool close at `:214` — documented; SDK accepts blocks-close-at-message-end).
  Arguments-before-name edge: buffer fragments internally until id+name arrive (mirrors batch
  skip `:201-202`).
- **Usage + `message_delta`:** emitted ONCE in `Finalize()`, before `message_stop`, carrying
  stop_reason + usage (`:226-235` shape); **always emitted, with zero usage when absent**
  (batch parity). Stream-end-without-`[DONE]`: `Finalize()` ALWAYS emits `message_stop`; then
  the caller writes `sendAnthropicSSEError` (`handler_anthropic.go:880-886`).
- **`thinkingSink` integration (internal variant):** typed-event entry (`ProcessEvent`) called
  from the `case "thinking"` arm (`internal_handler.go:393-394`) BESIDE the unchanged sink
  write; translator installed via a setter mirroring `SetThinkingSink` (`:109-115`);
  constructed in `doAnthropicInternalRequest` (`handler_anthropic.go:600-685`) when live.
  Buffered mode byte-for-byte unchanged (recorder + `TranslateBufferedStream` untouched,
  `:612/:728`).
- **toolcall-buffer ordering constraint:** the buffer's `Flush()` (`internal_handler.go:438-440`,
  on `case "done"`) emits final tool chunks AFTER done — **`Finalize()` must run after
  `toolCallBuffer.Flush()` completes** (same site where `[DONE]` is written today). Test required.
- **Reuse inventory:** `StreamState`/`ToolCallState`/event constants (`types.go:236-252, :114-120`)
  as-is; ALL format helpers (`stream.go:251-340`) as-is; `generateAnthropicMessageID`/
  `mapFinishReason`/`translateUsage` (`response.go:68-111`) as-is; `extractChunkContent` body
  refactored into `accumulateChunk(chunk, state)` shared by batch and incremental (≤5 LOC, no
  behavior change); `generateAnthropicEvents` NOT reused. New file ≈250 LOC + ≈350 LOC tests.

**Acceptance-gate implication per variant (precise, per addendum request).**

| Variant | Buffered mode (header present) | Live mode (header absent) under (a) | Live mode (header absent) under (b) — fallback |
|---------|-------------------------------|--------------------------------------|---------------------------------------------|
| OpenAI race (`/v1/chat/completions`) | today's behavior, full parity | first byte before completion (first-forwardable-byte winner gate, Phase 2) | n/a (not an Anthropic translation) |
| Ultimate external stream | today's H8 single-write, full parity | per-line flush, first byte before completion | n/a (not an Anthropic translation) |
| Ultimate internal stream | today's per-event-buf single-write | per-event flush, first byte before completion | n/a (not an Anthropic translation) |
| Anthropic→Anthropic passthrough | already real-streams (template, locked L3) | already real-streams — unchanged | n/a |
| Anthropic→OpenAI-wire external translation (`handler_anthropic.go:819/:851`) | today's buffered batch translation, full parity | **incremental translator (option a)** — first byte before completion; thinking blocks emitted on wire per locked stance above | **BUFFERED in default mode** — does NOT meet "first byte before completion" acceptance gate (slip fallback only, per leader resolution) |
| Anthropic→OpenAI-wire internal translation (`doAnthropicInternalRequest` at `:600-685` with recorder `:612` / `case "thinking"` at `:379-396`) | today's recorder + batch translation; thinking captured into sink, NOT on wire | **incremental translator (option a)** — first byte before completion; thinking blocks emitted on wire per locked stance above | **BUFFERED in default mode** — does NOT meet "first byte before completion" acceptance gate (slip fallback only, per leader resolution) |

**Fallback cost** (when the new translator is unavailable / slips): default-mode falls back
to buffering for translation-only variants, with the per-variant gate softened to "first byte
forwarded before upstream completion, OR the variant is documented as requiring buffered mode
due to translation requirement (slip)". Mitigation: ship the new translator as part of Phase 3 —
it's the same shape as the existing batch translator and the existing helpers do the heavy
lifting; estimated effort is comparable to writing the batch translator once.

**Internal-Anthropic variant** (`handler_anthropic.go:600-685`): the recorder is replaced by
the true `w` (`doAnthropicInternalRequest` at `:642`) in live mode only; the `thinkingSink`
invariant at `internal_handler.go:32-41` and `:379-396` is preserved — the "case thinking" arm
still captures into the optional sink (sink invariant UNCHANGED) and additionally emits a
`thinking_delta` block on the wire in live mode (per the locked stance above). The new
translator's input becomes each provider `StreamEvent` (already typed by the provider
layer), not raw SSE bytes — via the typed-event entry (`ProcessEvent`). The internal_handler's
existing `case "content"` / `case "thinking"` / `case "tool_call"` switch becomes the place
where the new translator is driven.

### Q3 — Precedence: header opt-in vs deadline machinery — RESOLVED

> **Correction from `technical-analysis.md` addendum (2026-08-27):** the per-model
> `ReleaseStreamChunkDeadline` field at `pkg/models/config.go:127` is **DORMANT** —
> zero call sites in `pkg/proxy`. The **ACTIVE** deadline is the global
> `c.cfg.StreamDeadline` (`race_coordinator.go:370`) inside the race coordinator. The
> leader's suggested precedence rule survives, but is rewritten against the corrected
> facts — and re-rewritten by the architect resolution (live mode KEEPS the coordinator).

**RESOLVED: explicit header wins.** Buffered mode = the global `StreamDeadline` inside the
coordinator continues to run exactly as today. Live mode = the coordinator runs with the
first-byte winner gate; the deadline's meaning is redefined: **no forwardable byte within
`StreamDeadline` ⇒ terminal in-band SSE error envelope + all attempts cancelled.**

**Mechanics (verified, `architecture-recommendation.md` §1.1.4):**

- Post-winner the deadline cannot interfere: the manage loop returns after winner selection
  (`race_coordinator.go:445`) and `defer streamDeadlineTimer.Stop()` (`:371`) disarms it;
  belt-and-braces `c.winner != nil` guard at `:653-655`.
- Pre-winner with no forwardable byte — **today's behavior is WRONG for live mode**: the
  deadline picker promotes the best among non-Done requests with `bestLen=-1` (`:661-672`)
  and promotes a **0-byte winner that keeps streaming** (`:674`, `:695-704`). **Phase 2 adds
  the live-mode 0-byte guard:** best has `TotalLen()==0` ⇒ take the `:705-722` error branch
  (`streamDeadlineError` `:712`, close done+streamCh `:720-721`) **plus an explicit
  `cancelAll()`** (that branch today cancels nothing). Surfacing: `WaitForWinner` (`:831-839`)
  returns nil → `handleRaceFailure` (`handler.go:967-979`) → `GetStreamDeadlineError()`
  (sequencing per task 2.2: `c.mu.Unlock()` before `cancelAll()`).
  (`:1008`, accessor `:941-946`) → `rc.headersSent==true` → in-band `sendSSEError`
  (`:1028-1029`). Envelope shape identical to `handler.go:1169-1178`.
- Hard deadline (`MaxGenerationTime`, `:376, :393-397`) unchanged in both modes — absolute cap.
- The per-model `ReleaseStreamChunkDeadline` field stays dormant exactly as today. If a future
  feature wires the per-model field, that future plan sets the precedence.
- No model-config field changes; no model-config test changes.
- **Verified:** the global `c.cfg.StreamDeadline` default value (`110*time.Second` per
  `pkg/config/config.go:165`) is read at runtime — never hardcode.

**Pinned precedence rule (RESOLVED):**

```
Header present  ⇒ buffered mode      ⇒ coordinator runs with IsCompleted gate ⇒ StreamDeadline = today's behavior (incl. 0-byte promotion)
Header absent   ⇒ real-streaming mode ⇒ coordinator runs with first-byte gate  ⇒ StreamDeadline = no-forwardable-byte timeout (in-band SSE error + cancelAll, 0-byte guard)
```

### Q4 — State field: reuse `rc.streamingNonRetryable` — RESOLVED (keep name, atomic.Bool)

**RESOLVED: reuse `rc.streamingNonRetryable`**, set on first successful client write inside
the existing `rc.writeMu` critical section (`handler.go:1064-1080`), with a recommended
type change to `sync/atomic.Bool` (`handler_helpers.go:102`; reset `:139` becomes
`Store(false)`).

**Rationale.**

- Field exists at `handler_helpers.go:99-102` with matching intent. The existing `reset()` at
  `handler_helpers.go:139` clears it — consistent with the per-request lifecycle. No struct
  addition; no new lifecycle to maintain.
- **Zero readers exist today** (full-tree grep); `atomic.Bool` gives any future reader a
  happens-before edge independent of `writeMu` (which is over-scoped for readers — the
  heartbeat also takes it). The set still happens under `writeMu` on the first successful
  relay write, exactly per the leader's resolution; the atomic type simply makes any future
  `Load()` race-free by construction.
- Functional effect under R1-variant: none on current code paths (the first-byte gate +
  `c.winner==nil` checks already close the failover window, `race_coordinator.go:1275-1292`);
  the flag formalizes the "no retry/fallback after first forwarded byte" boundary for future
  readers.
- Keep the name (minimal diff); update the comment to "stream is live: no racing / fallback /
  mid-stream credential failover". Phase 1 truth table unchanged.

---

## 5. Phases

> **House style** (per the model-credential-load-balancing plan set): each phase carries
> **Objective / Context / Scope (In + Out) / Files** table (`| # | File | Action | What |`,
> file:line) / **Tasks** table (`| # | Task | Acceptance | Design Notes |`) / **Test strategy**
> (new + flipped) / **Risks + mitigations** / **Exit criteria**.

Phase ordering reflects dependency: Phase 1 (foundation) unblocks Phase 2 (OpenAI race path,
which is the architectural reference) and Phase 4 (ultimate paths, which are simpler variants
of Phase 2). Phase 3 (Anthropic incremental translator) is the biggest new component and
parallelizes with Phases 2/4 once Phase 1 lands. Phase 5 (parity + acceptance harness) is
sequential after 2/3/4.

```
Phase 1 (foundation) ──┬──> Phase 2 (OpenAI race path)         ──┐
                        ├──> Phase 3 (Anthropic incremental)    ──┼──> Phase 5 (parity + acceptance)
                        └──> Phase 4 (ultimate paths)           ──┘
```

---

### Phase 1: Mode Foundation — Header Parsing, rc Plumbing, Precedence Rule

**Objective.** Add the `X-LLMProxy-Buffer-Response` opt-in header, plumb a `bufferMode bool`
through `requestContext`, and define the precedence rule that the deadline auto-release
applies only in buffered mode. After Phase 1, every streaming path reads `rc.bufferMode` to
decide its delivery policy. No wire behavior changes; this is pure plumbing.

**Context.** The current `bypassInternal` parse at `handler_functions.go:123` is the house
style. The `rc.streamingNonRetryable` field (`handler_helpers.go:102`) exists but is unused
(declared-but-unset). We adopt both.

**Scope.**

**In scope:**

- Header parse at the same site as `bypassInternal`.
- `requestContext.bufferMode bool` — a NEW field (RESOLVED per Q4 amendment 2026-08-27:
  `bufferMode` is the per-request header-derived mode; `streamingNonRetryable` is a distinct
  flag with a different lifecycle — set inside the relay loop, reset per request).
- Precedence comment + test (header wins over model config — buffered mode = current behavior;
  real-streaming mode ignores the deadline).
- Unit tests for the parse function (true/1/yes/on/empty/absent/false/0/no/off, case variants,
  mixed-case header keys).
- Lint / vet / `go build` over `pkg/proxy`.

**Out of scope:**

- Any actual streaming-path behavior change (Phases 2/3/4).
- Header parsing in `cmd/main.go` (the header is a per-request signal, not a global config).
- Changes to `pkg/proxy/translator/` (no new code yet).
- Documentation beyond in-code comments and the test docstring.

**Files (create / modify).**

| # | File | Action | What |
|---|------|--------|------|
| 1 | `pkg/proxy/handler_functions.go` | MODIFY (around :123) | Add `bufferMode` parse alongside `bypassInternal`. Implementation per the **locked L1 presence-aware semantics (approver iteration 001)**: `vals := r.Header.Values("X-LLMProxy-Buffer-Response")` (canonicalizes the key); `len(vals)==0` ⇒ ABSENT ⇒ live streaming. PRESENT ⇒ `parseBufferResponseValue(vals[0])`: `true` on `"true"/"1"/"yes"/"on"/""` (empty PRESENT value), `false` on `"false"/"0"/"no"/"off"` AND on any OTHER non-empty value (unknown values fall to live streaming — opt-in must be explicit and correctly spelled; `switch strings.ToLower(strings.TrimSpace(v))` with the listed truthy cases; default branch `return false`). Case-insensitive on the value (`strings.EqualFold`). Wire canonicalization in tests is irrelevant for client-SENT headers — see Test Strategy. Sketch: `func bufferModeFor(r *http.Request) bool { vals := r.Header.Values("X-LLMProxy-Buffer-Response"); if len(vals) == 0 { return false }; return parseBufferResponseValue(vals[0]) }`. |
| 2 | `pkg/proxy/handler_helpers.go` | MODIFY (around :23-145, :139 reset) | Add **`bufferMode bool`** to `requestContext` (a NEW field, distinct from `streamingNonRetryable`). `bufferMode` is per-request mode (parsed from header); `streamingNonRetryable` is the post-first-byte "stream is live" flag set inside the relay loop. Different lifecycles; both reset to `false` in `reset()` (`:139`). Populate `bufferMode` from `initRequestContext`. **Per Q4 resolution (RESOLVED, 2026-08-27):** `streamingNonRetryable` becomes `sync/atomic.Bool` (type at `:102`, `Store(false)` at `:139`) — land the type change here, it is one line each. |
| 3 | `pkg/proxy/handler_functions_test.go` | MODIFY (append tests) | Add `TestBufferModeHeaderParsing` — table-driven over canonical truthy/falsy strings + mixed-case header keys. |
| 4 | `pkg/proxy/handler_helpers_test.go` | MODIFY (append tests) | Add `TestRequestContext_BufferModeReset` — assert reset returns the flag to `false` after a request lifecycle. |

**Tasks.**

| # | Task | Acceptance | Design Notes |
|---|------|------------|--------------|
| 1.1 | Add `bufferMode` parse at `handler_functions.go` | Header value semantics **LOCKED per dispatch** (not open): the **presence-aware** read is the ONLY correct mechanism — `r.Header.Get` conflates absent with empty-value and would invert the default. Use `r.Header.Values("X-LLMProxy-Buffer-Response")`; `len==0` ⇒ ABSENT ⇒ live streaming. PRESENT ⇒ parse value: `"true"/"1"/"yes"/"on"` OR empty PRESENT value ⇒ buffered; `"false"/"0"/"no"/"off"` OR any OTHER non-empty value (unknown) ⇒ live streaming. Case-insensitive on the value (`strings.EqualFold`). | Implementation: a two-stage helper. `bufferModeFor(r *http.Request) bool` does the presence check via `r.Header.Values(...)` and dispatches to `parseBufferResponseValue(string) bool`. The value parser is a `switch strings.ToLower(strings.TrimSpace(v)) { case "true","1","yes","on","": return true; default: return false }` (default branch covers `"false","0","no","off"` AND unknown values — opt-in must be explicit and correctly spelled; misspelled values fall to live streaming, NOT to legacy buffered). The helpers live next to the parse site at `handler_functions.go`. **Empty PRESENT value = buffered** per the locked semantics (the dispatch's deliberate choice — let clients opt in by sending a bare header without a value). |
| 1.2 | Populate `rc.bufferMode` from `initRequestContext` | `requestContext` exposes `bufferMode bool`; reset clears it. | The `bufferMode` field is the per-request header-derived mode (distinct from `streamingNonRetryable` — see resolved Q4); populate from the parse at task 1.1. |
| 1.3 | Add precedence rule comment at the existing global `StreamDeadline` evaluation site (and document the dormant per-model field) | A `// Precedence (Phase 1): buffered mode runs today's deadline machinery; live mode runs the coordinator with the first-byte winner gate — StreamDeadline = no-forwardable-byte timeout (see race_coordinator.go Phase 2).` comment at `race_coordinator.go:370` (where `streamDeadlineTimer` is created) and at any other site that reads `c.cfg.StreamDeadline`. Plus a doc-comment on the dormant `pkg/models/config.go:127` field noting "DORMANT — no call sites in pkg/proxy; the active deadline is the global `StreamDeadline` in `race_coordinator`". | The per-model field stays dormant exactly as today; no model-config test changes. |
| 1.4 | Unit tests for header parsing | All truthy variants + empty ⇒ buffered; all falsy variants + absent ⇒ real streaming; case-insensitive value (`True`, `TRUE`, `true`); mixed-case header keys (`X-LLMProxy-Buffer-Response`, `x-llmproxy-buffer-response`, `X-LLMPROXY-BUFFER-RESPONSE`). | Table-driven over `(name, value, wantBufferMode)`. Tests cover the locked 10-variant truth table verbatim — see Test Strategy below for the full fixture list. |
| 1.5 | Build + lint | `go build ./...` and `go vet ./pkg/proxy/...` green. | No behavior change; only compile-check. |

**Test strategy.**

**New tests:**
- `TestBufferModeHeaderParsing` in `handler_functions_test.go` — table-driven header truth
  table per the locked semantics (approver iteration 001): for each test case set
  `r.Header["X-LLMProxy-Buffer-Response"] = []string{value}` (use direct map access to
  distinguish PRESENT+empty from ABSENT — `http.Header.Set` collapses to a single-element
  slice with value `""` for PRESENT+empty; absence = no entry). Fixtures cover ALL four
  locked rows:
  - **PRESENT + truthy** ⇒ buffered: `("true", true)`, `("1", true)`, `("yes", true)`,
    `("on", true)` (case-insensitive: `"True"`, `"TRUE"`, `"YES"` ⇒ all `true`).
  - **PRESENT + empty** ⇒ buffered: `("", true)` (the dispatch's deliberate opt-in-without-value).
  - **PRESENT + falsy** ⇒ live: `("false", false)`, `("0", false)`, `("no", false)`,
    `("off", false)` (case-insensitive: `"FALSE"`, `"No"` ⇒ all `false`).
  - **PRESENT + unknown** ⇒ live: `("maybe", false)`, `("trueish", false)`, `(" ", false)`
    (whitespace-only treated as unknown after TrimSpace; misspelled/garbage falls to live
    streaming, NOT to legacy buffered).
  - **ABSENT** ⇒ live: `(no header entry, false)`.
  - Mixed-case header keys: `"X-LLMProxy-Buffer-Response"`, `"x-llmproxy-buffer-response"`,
    `"X-LLMPROXY-BUFFER-RESPONSE"` (canonicalization done by `r.Header.Values`).
- `TestRequestContext_BufferModeReset` in `handler_helpers_test.go` — reset round-trip.

**Flipped tests:** none (Phase 1 changes no observable wire behavior).

**Risks + mitigations.**

| Risk | Impact | Likelihood | Mitigation |
|------|--------|-------------|-----------|
| Header parse diverges from locked semantics (e.g., parser is stricter than dispatch ruled) | Medium (clients with bare header opt out unintentionally) | Low | Test the full truth table at Phase 1; the parser is the single source of truth for the semantics. |
| Distinct-flag lifecycle clarity (post-resolution) | Low (future-reader confusion between `bufferMode` and `streamingNonRetryable`) | Low | Q4 RESOLVED 2026-08-27: both fields are distinct, both reset to `false` in `reset()` (`:139`); `bufferMode` is set by `initRequestContext` (per-request header), `streamingNonRetryable` is set inside the relay loop on first successful write. Field comments updated accordingly. |
| Empty header value (PRESENT+empty): buffered; ABSENT: live | Low (opt-in UX); High (default-inversion bug class if implementation falls back to `r.Header.Get`) | Low (after approver iteration 001 fix — mechanism is presence-aware via `r.Header.Values`) | The locked semantics explicitly distinguish PRESENT+empty (buffered — dispatch's deliberate choice) from ABSENT (live — the new default). The mechanism uses `r.Header.Values(...)` (presence-aware) NOT `r.Header.Get(...)` (which conflates absent with empty and would invert the default). `TestBufferModeHeaderParsing` covers the full 4-row truth table including both PRESENT+empty (buffered) and ABSENT (live) using direct map access to set `r.Header[key] = []string{""}` (PRESENT+empty) vs no entry at all (ABSENT). |

**Exit criteria (independently testable).**

1. `go build ./... && go vet ./...` green from `feature/real-streaming-default`.
2. `TestBufferModeHeaderParsing` passes for the truth table.
3. `TestRequestContext_BufferModeReset` passes.
4. No existing test changes (no flipped expectations).
5. `rc.bufferMode` is reachable from `requestContext` everywhere downstream sites read it
   (already the case — it's a struct field).

---

### Phase 2: OpenAI Race Path Real Streaming — First-Forwardable-Byte-Wins Gate (R1-Variant)

> **REWRITTEN per architect resolution (2026-08-27).** This phase is a **gate redefinition**,
> not a coordinator bypass. The previous draft's `streamLiveRelay` function is ELIMINATED —
> `WaitForWinner` returning at the first-byte winner + the existing `streamResult` body IS the
> live relay. Mechanics + evidence: `architecture-recommendation.md` §1.1–§1.3, §2.

**Objective.** When `rc.bufferMode == false && rc.isStream == true`, select the race winner at
the **first forwardable data chunk** (`req.GetError()==nil && req.GetBuffer().TotalLen()>0`),
cancel losers mid-stream, and relay the winner live via the **existing unchanged
`streamResult` loop**. Pre-first-byte racing / fallback / credential failover operate
unchanged (the coordinator runs exactly as today until the predicate fires); post-first-byte
upstream failure = today's terminal in-band SSE error envelope (`handler.go:1169-1178`). In
live mode, `StreamDeadline` means "no forwardable byte within the deadline ⇒ in-band SSE error
+ `cancelAll()`" (new 0-byte guard). Prune and the entry raw-capture are gated to buffered mode
(R2 fix — preserves the usage estimator and bufferstore inputs).

**Context.** Today's path (`handler.go:867-963`) starts the race coordinator, waits for a
winner (`WaitForWinner` at `:927` — blocked until `IsCompleted()` at
`race_coordinator.go:409/:431`), then calls `streamResult(w, rc, winner)` (`:946`) which drains
the winner's `streamBuffer` via `NotifyCh` (`:1038-1380`). The relay loop is ALREADY a live
per-chunk relay; only the winner gate delays it. The gate swap is live-mode- and
isStream-scoped; buffered mode executes today's code verbatim.

**Scope.**

**In scope:**

- `race_coordinator.go`: live-mode predicate swap at `:406-435`; `liveFirstByteGate` threading
  via the constructor (call site `handler.go:924`); live-mode 0-byte deadline guard at
  `:661-722` (+ explicit `cancelAll()` in the error branch; sequencing per task 2.2:
  `c.mu.Unlock()` before `cancelAll()`).
- `race_executor.go`: `isStreamErrorChunk` check moved BEFORE the Add loop (`:1573-1585`) —
  predicate hazard fix (a dying attempt must not win via its error chunk).
- `handler.go`: pass `liveFirstByteGate` at `:924`; gate the **FIVE** Prune call sites
  (`:1082, :1122, :1167, :1238, :1357`) to buffered mode; gate the entry raw-capture (`:1053-1058`) to
  buffered mode; set `rc.streamingNonRetryable` (`Store(true)`) on the first successful relay
  write (~`:1064-1080`). `streamResult` otherwise **unchanged** — the error envelope at
  `:1169-1178` is reused verbatim for mid-stream failure.
- Empty-key credential picks still fire `model_credential_selected` from
  `race_executor.go:134` (single source of truth — no change).
- Heartbeat continues to run until the relay finishes; cancelled by the same defer
  (`handler.go:906-910`).

**Phase-2 CONSTRAINTS (pinned gate form, per amendment #8).** The resilience review's safety
conclusions depend on the first-byte winner predicate landing in the existing manage-loop ticker
arm (`race_coordinator.go:406-435`) with `onceStream` close semantics (`:431`) and the
deadline guard's reuse of the `:653-655` winner-nil check. The implementer MUST NOT use a
per-attempt atomic first-byte flag (re-entrant lock hazards, plus loss of the `onceStream`
single-close contract). The deadline guard's cancellation path MUST NOT call `cancelAll()` /
`cancelAllExcept()` while the deadline guard still holds `c.mu` — the deadlock hazard is
the `cancelAll`→`cancelAllExcept`→`c.mu` chain at `:760` (re-acquiring a held `c.mu`);
`req.Cancel()` itself only acquires `r.mu` (per-attempt) and is NOT the deadlock source —
the pinned form captures the request set under `c.mu`, `Unlock()`s, THEN iterates `req.Cancel()`
on the snapshot (see task 2.2 design notes). Both forms are detected at code review and would
invalidate the plan's risk register.

**Out of scope:**

- Race retry / fallback / credential failover after the first forwarded byte (accepted
  degradation; locked L4 as resolved). Pre-first-byte behavior is UNCHANGED — no coordinator
  racing semantics are touched beyond the winner predicate itself.
- Mid-stream credential failover (accepted degradation; `ExcludeAndReselect` still runs only
  in the pre-winner spawn branch, `race_coordinator.go:1349-1354`).
- Non-streaming requests (`isStream == false`) — both modes keep the `IsCompleted` gate
  (`handler.go:962`) and call `handleNonStreamResult` identically.
- Anthropic path (Phase 3), ultimate paths (Phase 4).
- Any change to BUFFERED-mode coordinator behavior — the predicate swap, deadline guard, Prune
  gating, and capture gating are all live-mode branches; buffered mode must execute today's
  code verbatim (parity suite proves it).

**Files (create / modify).**

| # | File | Action | What |
|---|------|--------|------|
| 1 | `pkg/proxy/race_coordinator.go` | MODIFY (`:406-435`, ctor `~:140-215`, `:661-722`) | Live-mode predicate swap: replace `req.IsCompleted() && req.GetError()==nil` (`:409`) with `req.GetError()==nil && req.GetBuffer().TotalLen()>0` behind `liveFirstByteGate`; everything downstream reused verbatim (`:412` index preference, `:423-429` event, `:431` onceStream close, `:440-446` cancel-and-exit). **isStream threading (approver iteration 001 NB6):** `liveFirstByteGate` is computed in `handler.go` BEFORE the constructor call (e.g. `liveFirstByteGate := !rc.bufferMode && rc.isStream` at the call site `handler.go:924`), passed as a constructor arg to `newRaceCoordinatorWithEvents`. Do NOT compute the gate inside the constructor — `isStream` is a `*requestContext` field, not a `*ConfigSnapshot`, and the constructor signature is `ConfigSnapshot`-based (`race_executor.go` lines 1320-1370 vicinity). Computing at the call site keeps the coordinator's signature stable and the threading direction explicit. Deadline guard at `:661-722`: live mode + best `TotalLen()==0` ⇒ take the `:705-722` error branch (`streamDeadlineError` `:712`, close done+streamCh `:720-721`). **Cancellation sequencing is PINNED in task 2.2 (do NOT inline `req.Cancel()` under the held lock — deadlock via the `:650-651`/`760` re-entrant chain).** |
| 2 | `pkg/proxy/race_executor.go` | MODIFY (`:1573-1585`) | Move `isStreamErrorChunk` check BEFORE the Add loop — a stream whose only chunk is an error chunk must NOT win the predicate (behavior-neutral in buffered mode; only the on-error raw dump content at `handler.go:1159-1164` changes). |
| 3 | `pkg/proxy/handler.go` | MODIFY (`:924`, `:1053-1058`, FIVE `:1082/:1122/:1167/:1238/:1357` Prune sites, `~:1064-1080`) | Pass the gate flag at `:924`. Gate the entry raw-capture (`:1053-1058`) to buffered mode (live mode: the Done capture `:1182-1187` is authoritative). Gate the **FIVE** Prune call sites (`handler.go:1082/:1122/:1167/:1238/:1357`) to buffered mode (R2 fix; gating stays single-switch — `bufferMode ? current : current` so the buffered branch is byte-identical). **Unlisted consumers of raw bytes** (executor-side fallback estimators at `race_executor.go:938/:1649`) consume the same `GetAllRawBytesOnce()` bytes; gating Prune off in live mode keeps them complete. `streamingNonRetryable.Store(true)` inside the first successful relay write (already under `rc.writeMu`). `streamResult` and the error envelope (`:1169-1178`) otherwise unchanged. Cancellation sequencing for the deadline-guard path is PINNED in task 2.2 — this file consumes `winner` only after the coordinator has promoted; no cancellation changes here. |
| 4 | `pkg/proxy/handler_helpers.go` | MODIFY (`:99-102`, `:139`) | Per Q4: `streamingNonRetryable` → `sync/atomic.Bool`; reset `Store(false)`; comment update ("stream is live: no racing / fallback / mid-stream credential failover"). (Type change landed in Phase 1; this row is the set-site wiring.) |
| 5 | `pkg/proxy/race_coordinator_test.go` | MODIFY (append tests) | NEW gate tests (first-byte winner, role-only-first, thinking-first, two-attempt first-buffered-wins, error-chunk-must-not-win) + deadline-guard tests (0-byte ⇒ envelope; byte-at-boundary honored; late-deadline-after-winner). **This file IS affected** — superseding this plan's earlier claim that coordinator logic was unchanged. |
| 6 | `pkg/proxy/handler_test.go`, `pkg/proxy/handler_integration_test.go` | MODIFY (append tests) | Live-relay tests: first-byte-before-completion, mid-stream loser cancellation, client-disconnect-during-cancel, no-cred-failover-after-first-byte, R2 (estimator + bufferstore under prune pressure). |
| 7 | `pkg/proxy/race_coordinator_credfailover_test.go` | MODIFY (append tests) — **MIN-1 BOOKKEEPING CORRECTION (LEADER DECISION 1, Phase 5)** | NEW L7 (credential LB) battery: `TestLiveRelay_NoCredFailoverAfterFirstByte` + `TestLiveRelay_PreFirstByte429_FailoverUnchangedInLiveMode` (~250 LOC). The test file legitimately gained ~8 lines of constructor-arity plumbing (the 11-arg `newRaceCoordinatorWithEvents` invocation) because the Phase 2 gate flag was added as the 11th positional parameter — every call site in this file had to be updated. **Legacy callers keep buffered semantics via the 6-arg wrapper** `newRaceCoordinator` (`race_coordinator.go:186-188`). This row corrects the original Phase 2 Files table which omitted this test file; the omission was an oversight, the test-file growth is real and required. **Fixing the record, NOT the tests.** |

**Tasks.**

| # | Task | Acceptance | Design Notes |
|---|------|------------|--------------|
| 2.1 | Coordinator gate swap | Live mode + isStream: winner = first attempt with `TotalLen()>0 && err==nil`; buffered mode + non-stream: `IsCompleted` gate unchanged (byte-identical). | Predicate + site per Q1 mechanics; `race_winner_selected` fires mid-stream with tiny `buffer_bytes` (`:423-429`) — UI consumers must not assume completed buffers (document in Phase 5 docs). Worst-case +100ms first-byte latency (tick granularity, `:364`) — accepted. **Scope: `liveFirstByteGate = !bufferMode && isStream` (NOT `!bufferMode` alone)** — `!bufferMode` alone would change NON-STREAM deadline behavior (today: 0-byte promotion + keep waiting, `race_coordinator.go:674/:695-704`), violating E2 stream=false-unaffected. The predicate swap, the 0-byte deadline guard (task 2.2), the Prune + entry-capture gating (task 2.4), and the Prune site gating (file 1 column) all gate on the same `liveFirstByteGate` flag. **CONSTRAINT (Pinned gate form, per amendment #8):** the predicate swap MUST land in the existing manage-loop ticker arm (`race_coordinator.go:406-435`) — NOT in a per-attempt atomic first-byte flag — because the resilience review's safety conclusions depend on the `onceStream` close semantics at `:431` and the deadline guard's reuse of the `:653-655` winner-nil check. If a different form is chosen, re-verify the traced hazards (`architecture-recommendation.md` §3, §7 item 4). |
| 2.2 | Deadline 0-byte guard | Live mode + deadline + all buffers empty ⇒ in-band SSE envelope via `GetStreamDeadlineError` (`race_coordinator.go:1008`, accessor `:941-946`) + `handleRaceFailure` (`handler.go:967-979`); all attempts cancelled. Byte arriving at the boundary is honored. Buffered-mode deadline behavior byte-identical. | Guard at `:661-722`. **PINNED cancellation form (avoids `c.mu` re-entrance deadlock):** `handleStreamingDeadline` holds `c.mu` for its entire body (`:650-651`); `cancelAll()` calls `cancelAllExcept(nil)` which re-acquires `c.mu` at `:760` — non-reentrant `sync.Mutex` would HANG every live-mode deadline expiry. **The PINNED form** (dispatcher decision, following the codebase's own pattern at `:441-446`): under the held `c.mu`, capture the needed state (request set snapshot, e.g. `toCancel := make([]*upstreamRequest, 0, len(c.requests)); for _, r := range c.requests { if r != nil { toCancel = append(toCancel, r) } }`), perform the existing streamCh/done close + `streamDeadlineError` write (`:712-:721`), then `c.mu.Unlock()` BEFORE calling `cancelAll()` / iterating and calling `req.Cancel()` on the snapshot. **Implementation note (mandatory):** the live-guard form MUST remove the `defer c.mu.Unlock()` at `race_coordinator.go:651` (the partial implementation that keeps the defer AND adds the mid-body `Unlock()` would double-unlock — fail-fast panic; caught by `TestLiveRelay_StreamDeadlineInLiveMode` but the plan must say it). **Alternative considered and NOT pinned:** inline `req.Cancel()` per request while still holding `c.mu` (would deadlock via the same chain if `req.Cancel()` ever needs the coordinator lock — keep one form only). Byte-at-boundary honored: any byte that arrived between the ticker check and the deadline fire is already in `winner` (the predicate fires synchronously inside the same lock window); if a byte arrives AFTER the unlock but BEFORE the cancel iterates, it goes into a closed buffer and `Add` returns false (`:119`) — idempotent. Buffered mode executes the existing `:674-:704` promotion unchanged (today's `bestLen=-1` ⇒ first-non-done wins). |
| 2.3 | Executor error-chunk reorder | A stream whose only chunk is an error chunk does NOT win the predicate; fallback proceeds. | `race_executor.go:1573-1585` reorder per Q1 hazard; neutral in buffered mode. |
| 2.4 | Prune + entry-capture gating (R2 fix) | Live mode: `GetAllRawBytesOnce` at `buffer.Done()` (`handler.go:1220`) returns the FULL stream; bufferstore Done capture (`:1182-1187`) complete; no partial entry capture. Buffered mode: today's behavior (incl. entry+Done double-save) unchanged. | R2 verdict: the estimator consumes RAW buffer bytes (not `rc.accumulatedResponse`); mid-stream Prune (`:1082/:1122/:1167/:1238/:1357` → `stream_buffer.go:282-289`) truncates them non-deterministically. Prune NEVER fires in buffered mode today (`readIndex==len` at relay entry ⇒ `ShouldPrune` false, `stream_buffer.go:294-303`), so gating it in live mode preserves effective behavior in both modes. Memory tradeoff: live mode holds the full response in the winner's buffer — identical to today's buffered profile (no regression). Developer verifies `bufferStore.Save` overwrite semantics when gating the entry capture (see 2.4.1). |
| 2.4.1 | Explicit `bufferStore.Save` double-save test | `TestLiveRelay_BufferStoreDoubleSave_Benign` (new) — gated on `LogRawUpstreamResponse=true`. Run a buffered-mode relay that fails partway, assert `bufferStore.Save` was called EXACTLY ONCE per upstream response (no double-write). Source-verified benign (`pkg/bufferstore/store.go` writes to a unique temp file + atomic rename, `:32-59`), so the test asserts NO double-save rather than asserting the temp-file correctness. | Tracked under #9 — even though the source is benign, the gating is a non-trivial behavior change (entry capture skipped in live mode) so we want a regression pin. The test uses the existing `bufferstore.BufferStore` harness; it does NOT need a new test infra. |
| 2.5 | Flag set on first write | `streamingNonRetryable` set exactly once per request, inside the first successful relay write (under `rc.writeMu`); `reset()` clears it. | Q4 mechanics; `atomic.Bool` type from Phase 1. |
| 2.6 | Tests | §Test strategy below green; `go test -race ./pkg/proxy/...` green. | Merged data-flow + resilience test tables. |

**Test strategy.**

**New tests:**
- `TestLiveRelay_ForwardBytesBeforeCompletion` — mock upstream emits `data: chunk1\n\n`,
  sleeps 100ms, emits `data: chunk2\n\n`, sleeps 100ms, `[DONE]`. Assert client reads
  `chunk1` within <50ms of upstream writing it (NOT within 200ms+ of upstream completion).
  **First-byte acceptance redefined: winner selected at first forwardable chunk, relayed
  before upstream `[DONE]`** — mock sends chunk 1 then blocks; assert client receives it
  before completion.
- `TestLiveRelay_PredicateVariants` — role-only-first-chunk and thinking-first streams trigger
  winner + forward in order; two parallel attempts ⇒ first-buffered-bytes wins, NOT
  first-completed; error-chunk-only stream does NOT win (fallback proceeds).
- `TestLiveRelay_MidStreamLoserCancel` — two parallel upstreams; winner emits byte 1 at T+50ms;
  loser cancelled at T+100ms while mid-Add; client receives byte 1; loser `IsCancelled()`;
  `go test -race` green. Includes `TestLiveRelay_LoserAddAfterCancel` (late Add returns
  false on closed buffer — idempotent).
- `TestLiveRelay_StreamDeadlineInLiveMode` — upstream holds first byte indefinitely; short
  `StreamDeadline`; assert in-band SSE envelope after the deadline (NOT a 0-byte stream, NOT
  HTTP 5xx; `headersSent==true`). Byte-at-boundary variant honored. Late-deadline-after-winner
  variant: no double-winner, no panic in `handleStreamingDeadline`.
- `TestLiveRelay_ClientDisconnectDuringMidStreamCancel` — disconnect at T+50ms, cancel at
  T+75ms; no panic; goroutine cleanup (`-race`).
- `TestLiveRelay_FirstByteThenImmediateErr` — mock emits 1 chunk then closes/500s; client
  receives 1 chunk + SSE error envelope (well-formed wire; `handler.go:1169-1178` shape).
- `TestLiveRelay_PreFirstByteError` — mock returns 500 before any byte; client receives the
  in-band SSE error envelope on the response stream (headers already sent; no HTTP error
  status). Wire shape asserted byte-identical to the post-first-byte envelope.
- `TestLiveRelay_NoCredFailoverAfterFirstByte` — multi-credential model; first credential
  rate-limits AFTER first byte; assert NO failover (second credential NOT attempted); SSE
  envelope terminal. Pre-first-byte 429 failover variant: UNCHANGED in live mode.
- `TestLiveRelay_R2_EstimatorUnderPrunePressure` — live-mode long stream, no usage chunk;
  assert non-zero completion tokens at `buffer.Done()` (FAILS until task 2.4 lands — this is
  the regression pin). Bufferstore variant: Done capture == full stream; no partial entry
  capture in live mode.
- `TestLiveRelay_StreamingNonRetryableAtomic` — concurrent `Load()` under `-race` while relay
  writes once.
- `TestLiveRelay_HeaderDispatch` — same mock upstream, request 1 has
  `X-LLMProxy-Buffer-Response: true`, request 2 omits it. Assert request 1 waits for
  `[DONE]` before any byte; request 2 gets first byte before upstream completion.
- `TestLiveRelay_NonStreamUnaffected` — REGRESSION PIN (per amendment M3): `isStream=false`,
  `X-LLMProxy-Buffer-Response` ABSENT (live mode by header semantics). Assert the coordinator
  keeps the `IsCompleted` gate (no predicate swap), the deadline still promotes a 0-byte winner
  per today's `race_coordinator.go:674/:695-704`, no in-band envelope at deadline. Same test
  variant with header PRESENT ⇒ byte-identical to today's non-stream behavior. Locks E2
  (stream=false-unaffected) against the `liveFirstByteGate` mis-scoping bug class.

**Flipped tests:** see §6 master inventory. Specifically:
- `handler_test.go` / `handler_integration_test.go` tests that assume "wait for `[DONE]` then
  forward" — they need the header to opt into the old behavior.
- `race_coordinator_test.go` + `race_retry_test.go` deadline-path tests (they set
  `StreamDeadline: 5s` and exercise the `IsCompleted` gate / 0-byte promotion semantics) —
  must opt into buffered mode via the header (or explicit `bufferMode=true`), since live mode
  redefines both. Post-winner-failover / LB matrix tests likewise.
- `request_completed` event-timing tests may need adjustments (the event fires at request end,
  not at first byte) — plus note `race_winner_selected` now fires mid-stream.

**Risks + mitigations.**

| Risk | Impact | Likelihood | Mitigation |
|------|--------|-------------|-----------|
| **Live-mode estimator/bufferstore truncation via mid-stream Prune** (`handler.go:1220/:1184` × `stream_buffer.go:282-289`) — non-deterministic usage undercount + truncated debug dumps | High | High (if unfixed) | Task 2.4 gates Prune + entry capture to buffered mode; `TestLiveRelay_R2_EstimatorUnderPrunePressure` is the regression pin. |
| **0-byte winner promotion at deadline hangs live clients** until the hard deadline (today's `:661-704` behavior) | High | Medium (if unfixed) | Task 2.2 guard + `cancelAll()`; `TestLiveRelay_StreamDeadlineInLiveMode`. |
| **Error-chunk-Added-before-error-return wins the predicate** and preempts fallback (`race_executor.go:1573` vs `:1583`) | High | Medium | Task 2.3 reorder; predicate-variant test. |
| **Winner-validity**: first-byte winner may later fail (a loser might have succeeded); client sees partial bytes + terminal envelope, no retry | Medium | Medium | Leader-ACCEPTED; documented in `docs/real-streaming-default.md`; envelope well-formed (first-byte-then-err trace verified). |
| Loser late-Add post-cancel silently dropped (manage never observed; gate didn't fire for them) | Low | High | Idempotent by design (`Add` returns false on closed buffer, `stream_buffer.go:119`); `TestLiveRelay_LoserAddAfterCancel`. |
| Mid-stream cancel races with loser executor / client disconnect / relay teardown | High (crash class) | Low | Resilience review traced worst-case interleavings safe under the 9842c77 SIGSEGV fix (nil-receiver hardening + RLock buffer snapshot + `c.mu` released before `cancelAllExcept`); `-race` test battery. |
| Heartbeat writes interleave with relay writes | Medium (SSE framing corruption) | Low | Both go through `rc.writeMu` (`heartbeat.go:74-77` + `handler.go:1104`); pre-winner window shrinks in live mode ⇒ contention decreases. No change. |
| `streamingNonRetryable` future-reader data race | Medium (UB) | Low (no reader today) | `atomic.Bool` type change (Phase 1); `-race` test. |
| +100ms worst-case first-byte latency (tick-based predicate, `race_coordinator.go:364`) | Low | Certain | Accepted; irrelevant to the acceptance gate (mock inter-chunk delays ≫ tick). |
| `race_winner_selected` fires mid-stream with tiny `buffer_bytes` | Low (UI assumptions) | Certain | Document: UI consumers must not assume completed buffers. |
| Multi-credential model: no post-first-byte rate-limit failover | Medium (regression on the credential-LB feature) | Medium | Locked L4/L7 + accepted degradation §8. Pre-first-byte `GetOrSelect` + 429 failover window verified UNCHANGED (M7 mechanism wording per approver iteration 001 NB4): HTTP-status 429s surface as `*providers.ProviderError` (a Go typed error carrying the upstream HTTP status; classified via `providers.IsRateLimitError(providerErr)` which inspects the status code) BEFORE any stream byte — so the empty-buffer predicate cannot fire — `race_executor.go:531-543` (the error-return path of `executeExternalRequest` / `executeInternalRequest` short-circuits before `Add`). In-stream errors are a different path: `isStreamErrorChunk` returns plain `fmt.Errorf` (NOT a `*ProviderError`), task 2.3's reorder is what guards in-stream errors from winning. |

**Exit criteria (independently testable).**

1. `TestLiveRelay_*` tests pass — first-byte timing, predicate variants, mid-stream loser
   cancellation (`-race`), deadline-in-live-mode, pre/post-first-byte error envelopes,
   no-cred-failover-after-first-byte, R2 estimator/bufferstore.
2. `TestLiveRelay_HeaderDispatch` confirms header-driven path selection.
3. Existing `handler_test.go` / `handler_integration_test.go` tests that assumed buffered
   behavior either pass with the header set OR are flipped per §6.
4. `race_coordinator_test.go` + `race_retry_test.go` deadline-path tests pass with the header
   set (buffered mode); the coordinator's new gate + guard tests pass in live mode.
5. Both pre-first-byte and post-first-byte error envelopes match the buffered-mode error
   envelope byte-for-byte (`models.NewOpenAIError` JSON shape).
6. Locked L4 confirmed: `rc.streamingNonRetryable` set to `true` on first successful
   `w.Write` (under `rc.writeMu`); no racing/fallback/mid-stream-failover after that
   point in real-streaming mode.

---

### Phase 3: Anthropic Path — New Incremental Streaming Translator

**Objective.** Real-stream the Anthropic → Anthropic translation paths (`/v1/messages` with
OpenAI-wire upstream credential AND internal-Anthropic variant). Build a new
stateful-per-chunk translator (`pkg/proxy/translator/incremental_stream.go`) that emits
Anthropic SSE events as OpenAI chunks arrive. The internal-Anthropic variant threads the
**true `w`** through `InternalHandler.HandleRequest`, replacing the `flushingResponseRecorder`
with `w` directly while preserving the `thinkingSink` invariant (`internal_handler.go:32-41`,
`:379-396`).

**Context.** Today's Anthropic translation (`handler_anthropic.go:586-588`, `:720-760`) feeds
the recorder body into `translator.TranslateBufferedStream` (`pkg/proxy/translator/stream.go:19`),
which builds a `StreamState` from all chunks then emits Anthropic events in one batch. The
new translator drives the existing `formatContentBlockStart` / `formatContentBlockDelta` /
`formatThinkingBlockDelta` / `formatInputJsonDelta` / `formatContentBlockStop` /
`formatSSEEvent` helpers in order as OpenAI chunks arrive.

**Scope.**

**In scope:**

- New file `pkg/proxy/translator/incremental_stream.go` with `IncrementalStreamTranslator`
  holding `StreamState`, exposing `ProcessChunk(chunk []byte)` returning `(emittedEvents []string, err error)`,
  `Finalize() []string` for `[DONE]`/stream-end, and a typed-event entry `ProcessEvent(ev)`
  for the internal variant. Full state machine + ordering invariants per §4 Q2 "Resolution
  mechanics" (single-open-block interleave ruling; deferred `content_block_stop`;
  `message_delta`-at-`Finalize` with zero-usage default; `Finalize` runs AFTER
  `toolCallBuffer.Flush()`).
- New `handler_anthropic.go` site at `:586` for the streaming translation branch: switch
  from `handleAnthropicStreamResponse(resp, ...)` buffered approach to an incremental
  translator driven off the upstream scanner.
- Internal variant (`handler_anthropic.go:600-685`): replace `flushingResponseRecorder` with
  `w` (the true response writer) inside `doAnthropicInternalRequest` when `arc.bufferMode==false`;
  keep recorder for buffered mode. Drive the new translator from `case "content"` /
  `case "thinking"` / `case "tool_call"` events via the typed-event entry
  (`internal_handler.go:340-420+`); translator installed via a setter mirroring
  `SetThinkingSink` (`:109-115`).
- Preserve the `thinkingSink` invariant (thinking bytes NEVER reach `w` when the recorder is
  the sink; in real-streaming mode, thinking events DO reach the wire as Anthropic
  `thinking_delta` blocks — deliberate, documented, tested wire change). The invariant becomes
  "thinking events captured into the optional sink in BOTH modes; in real-streaming mode the
  sink capture is preserved AND a `thinking_delta` Anthropic event is additionally emitted on
  the wire via the translator".
- Preserve `model_credential_selected` asymmetry (locked L7) — internal-Anthropic path does
  NOT publish.

- **Anthropic-path sequential fallback loop in live mode (per addendum spec point ii — RESOLVED).**
  The Anthropic path uses a sequential fallback loop at `handler_anthropic.go:256`.
  **Resolution (architect): LOOP-TOP guard** — immediately after the `arc.baseCtx.Err()` check
  (`:261-264`), add: `if arc.headersSent && !arc.bufferMode { break }`. Loop-top (not inside
  `attemptAnthropicModel`) avoids wasted per-model setup (`:275-299`) on iterations that can
  never switch. `headersSent` set-sites: `:808` (external), `:742` (internal recorder), plus
  the new live entry point. Pre-first-byte (iteration 0, headers not sent) fallback unchanged.

**Out of scope:**

- Anthropic→Anthropic passthrough (`handler_anthropic.go:1225`) — verify untouched; it
  already real-streams (locked L3).
- Non-streaming requests (`isStream == false`) — both modes call `handleAnthropicNonStreamResponse`
  identically.
- OpenAI race path (Phase 2).
- Ultimate paths (Phase 4).
- New content-block types (text / thinking / tool_use / input_json_delta suffice for the v1
  cut).

**Files (create / modify).**

| # | File | Action | What |
|---|------|--------|------|
| 1 | `pkg/proxy/translator/incremental_stream.go` | **CREATE** | New `IncrementalStreamTranslator` struct + `NewIncrementalStreamTranslator(originalModel string)` + `ProcessChunk(chunk []byte) ([]string, error)` + `Finalize() []string` + typed-event `ProcessEvent(ev)` (internal variant). Reuses `StreamState`, `ParseOpenAISSEChunk`, `formatSSEEvent`, `formatContentBlockStart`, `formatContentBlockDelta`, `formatThinkingBlockDelta`, `formatInputJsonDelta`, `formatContentBlockStop`, `generateAnthropicMessageID`, `mapFinishReason`, `translateUsage`; `extractChunkContent` body refactored into a shared `accumulateChunk(chunk, state)` (≤5 LOC, no behavior change). Emits `message_start` + `ping` ONCE on first chunk (even empty streams — batch parity `stream.go:148-169`); `content_block_stop`s DEFERRED to `Finalize`; `message_delta` (stop_reason + usage, zero-usage default) before `message_stop` at `Finalize`. |
| 2 | `pkg/proxy/translator/incremental_stream_test.go` | **CREATE** | Unit tests per Test strategy below, including the run-both-translators parity test. |
| 3 | `pkg/proxy/handler_anthropic.go` | MODIFY (around :585-588, :720-760, :256-264) | For `rc.bufferMode==false`: drive `IncrementalStreamTranslator` from the upstream scanner loop in a new `handleAnthropicLiveStreamResponse` (copy the passthrough scanner shape at `:1245-1290`); flush after each event. Loop-top fallback guard at `:256-264` per the resolution above. |
| 4 | `pkg/proxy/handler_anthropic.go` | MODIFY (around :600-685) | `doAnthropicInternalRequest` — when `arc.bufferMode==false`: thread the true `w` into `InternalHandler.HandleRequest` (setter-based, mirroring `SetThinkingSink`); drop `flushingResponseRecorder` for the live branch. Drive the translator from `case "content"` / `case "thinking"` / `case "tool_call"` events via `ProcessEvent`. `Finalize()` invoked at the done site AFTER `toolCallBuffer.Flush()` completes. |
| 5 | `pkg/proxy/internal_handler.go` | MODIFY (around :109-115, :379-396, :438-440) | New optional `liveTranslator` field + setter (mirrors `SetThinkingSink`). The "case thinking" arm: sink capture UNCHANGED; in live mode, the translator emits the Anthropic `thinking_delta` event AND **replaces** the current `fmt.Fprintf(w, "data: …\n\n")` raw-OpenAI-chunk write (the recorder sink path is not active in live mode because the recorder is gone — see C1/M5). `case "content"` and `case "tool_call"` likewise: **the translator's emitted events REPLACE** the raw OpenAI-chunk write to `w` (no double-write). `arc.accumulatedResponse` is populated BY THE TYPED-EVENT ENTRY (the translator's `ProcessEvent` accumulates Content/Thinking/ToolCalls into the arc's accumulators as it drives emission — same shape as today's `extractStreamChunkContent` but driven per typed event). `Finalize` ordering vs `toolCallBuffer.Flush()` at the done site (`:438-440`) pinned. |
| 6 | `pkg/proxy/handler_anthropic.go` | MODIFY (around :591-598) | `flushingResponseRecorder` stays in place for buffered-mode tests; not touched. |
| 7 | `pkg/proxy/handler_anthropic_test.go` | MODIFY (append tests) | Live-stream tests using `httptest.NewServer` with controlled chunk timing. |
| 8 | `pkg/proxy/handler_anthropic_tokenid_parity_test.go` | MODIFY (append tests) | Header-driven dispatch tests. |
| 9 | `pkg/proxy/internal_handler_test.go` | MODIFY (around :397) | The `flushingResponseRecorder` usage at `:397` is in the BUFFERED-MODE test path (`runHandleStreamWithEvents` is called from buffered-mode tests only — verify and update if needed). Live-mode tests use the true `w` from `httptest.NewServer`. |

**Tasks.**

| # | Task | Acceptance | Design Notes |
|---|------|------------|--------------|
| 3.1 | Design + implement `IncrementalStreamTranslator` | `ProcessChunk([]byte) ([]string, error)`, `Finalize() []string`, `ProcessEvent(ev)`. `message_start`+`ping` once on first chunk; all `content_block_stop`s + `message_delta` (usage/stop_reason) + `message_stop` at `Finalize`. Zero-usage default. | State machine per §4 Q2 mechanics; single-open-block-of-each-kind interleave ruling; guards per the state table. |
| 3.2 | Unit tests for the translator | All helper emission paths + end-to-end "small OpenAI stream → expected Anthropic wire" + the parity-vs-batch test. | Reuses the `StreamState` test fixtures from `stream_test.go` where applicable. |
| 3.3 | Live-stream Anthropic→OpenAI-wire-upstream variant | `handler_anthropic.go:585-588` branch: when `rc.bufferMode==false`, replace the `handleAnthropicStreamResponse` buffered call with a new `handleAnthropicLiveStreamResponse(w, resp, arc)` that drives the translator off the scanner loop. **Live-entry `: connected\n\n` preamble (approver iteration 001 NB8):** the live handler MUST emit the SSE preamble (`: connected\n\n` + initial flush) ONCE on entry, before the first translator event — mirroring the OpenAI race path's `handler.go:886-889` pattern. This keeps the SSE framing consistent (clients/proxies that detect a comment preamble to mark stream-start will still work) and avoids a race where the first translator event lands before the preamble. The buffered path keeps today's behavior (preamble inside `handleAnthropicStreamResponse` at `:808`). | The scanner loop pattern is already at `handler_anthropic.go:1245-1290` (passthrough) — copy the shape. Mirror the 1MB scanner buffer cap (`:821`). |
| 3.4 | Live-stream Anthropic→internal variant | Thread the true `w` into `InternalHandler.HandleRequest` (setter); drive the translator from `case "content"` / `case "thinking"` / `case "tool_call"` via `ProcessEvent`; `Finalize()` after `toolCallBuffer.Flush()`. Loop-top fallback guard at `:256-264`. | The recorder is kept ONLY for buffered mode. The translator is constructed in `doAnthropicInternalRequest` when `arc.bufferMode==false` and installed via the setter. **Mid-stream error path (approver iteration 001 NB2):** the `case "error"` arm at `internal_handler.go:477-480` returns immediately — for live mode, the translator's `Finalize()` MUST run at the call site (the outer `HandleRequest` returns the error to `doAnthropicInternalRequest`, which calls `Finalize()` BEFORE propagating to the client wire — sequence: `translator.Finalize()` → emit `[DONE]` + flush → return error; or `translator.Finalize()` → write Anthropic `error` event → return error; pin the latter so the wire carries a well-formed Anthropic error event before the function returns). |
| 3.5 | Preserve `thinkingSink` invariant | Thinking events captured into the sink for both modes; in live mode they're emitted AS Anthropic `thinking_delta` events via the translator (translator REPLACES the raw OpenAI-chunk write to `w`; no double-write). | The "case thinking" arm at `internal_handler.go:379-396` runs the sink-write unchanged; in live mode the wire-write path is the translator only. The translator is the ONLY wire-write path for content/tool_call/thinking in live mode and it emits Anthropic-shaped events, never raw OpenAI thinking bytes — wire leak impossible. `arc.accumulatedResponse`/`accumulatedThinking`/`accumulatedToolCalls` are populated BY THE TRANSLATOR's typed-event entry (per-event accumulation as events are emitted). |
| 3.6 | Verify Anthropic→Anthropic passthrough untouched | `handler_anthropic.go:1225` byte-identical to baseline. | Diff-based test: compare `handlePassthroughStreamResponse` body before and after Phase 3. |
| 3.7 | Header-driven dispatch | Same request shape, two modes. Buffered mode = today's behavior; live mode = first content byte forwarded before upstream completion. | `X-LLMProxy-Buffer-Response: true` selects `handleAnthropicStreamResponse`; absence selects `handleAnthropicLiveStreamResponse`. NOTE: `arc` does not carry `bufferMode` today — thread it (Phase 1 plumbing extended to the Anthropic request context). |
| 3.8 | Wire-shape parity for buffered mode | All existing `handler_anthropic_test.go` tests pass unchanged. | No change to the buffered code path. |
| 3.9 | Update `thinkingSink` invariant comment for dual-mode wording | Comment at `internal_handler.go:32-41` rewritten to describe dual-mode behavior: sink capture in BOTH modes; live-mode wire emission via translator ADDITIONAL to sink capture (the "case thinking" arm keeps the sink-write unchanged and adds translator emission when `bufferMode==false`). | Mechanical text edit; no behavior change. Unblocks future readers — today the comment reads "real-streaming mode the recorder goes away" which is stale post-R1/R2. |

**Test strategy.**

**New tests:**
- `TestIncrementalStreamTranslator_MessageStartOnFirstChunk` — first chunk emits `message_start`
  + `ping`; second chunk emits only `content_block_start` + `text_delta`.
- `TestIncrementalStreamTranslator_MessageStartOnce` — the same role-only chunk fed twice
  emits exactly one `message_start` and one `ping` (double-emission guard).
- `TestIncrementalStreamTranslator_ThinkingBlock` — chunk with `reasoning_content` emits a
  `thinking` content block separate from the `text` block.
- `TestIncrementalStreamTranslator_ToolUseBlock` — `tool_calls` array accumulates and emits
  `tool_use` content block with `input_json_delta` fragments.
- `TestIncrementalStreamTranslator_ToolCall_NoID_Dropped` — argument-only chunks buffered
  internally until id+name arrive; single `content_block_start` + accumulated
  `input_json_delta`.
- `TestIncrementalStreamTranslator_FinalizeEmitsStop` — after `[DONE]`, `Finalize` emits
  block-stops + `message_delta` + `message_stop`.
- `TestIncrementalStreamTranslator_DeferredClose_AllBlocksStopBeforeMessageStop` — three block
  types opened across chunks; assert NO `content_block_stop` until `Finalize`, then correct
  order.
- `TestIncrementalStreamTranslator_FinalizeWithoutDone_EmitsMessageStop` — no `[DONE]`;
  `Finalize` still emits `message_stop` (then caller writes `sendAnthropicSSEError`).
- `TestIncrementalStreamTranslator_UsageInjection` — chunk with `usage` populates
  `input_tokens`/`output_tokens` in `message_delta`; zero-usage default when absent.
- `TestIncrementalStreamTranslator_ParityVsBatch` — the SAME OpenAI SSE fixture through BOTH
  translators: event-SET equality always; event-ORDER equality for non-interleaved fixtures;
  ORDER inequality PINNED for interleaved fixtures (the documented live-mode wire difference).
  **Event-SET equality definition (approver iteration 001 NB3):** the SET of `content_block_*`
  events emitted by both translators is equal — same block structure (count, types, indices)
  and same final aggregated payloads per block (concatenating all `content_block_delta.text` /
  `thinking_delta.thinking` / `input_json_delta.partial_json` for each block yields identical
  results). The batch translator emits 1 aggregated delta per block (one `text_delta` carrying
  the full accumulated text); the incremental translator emits N deltas (each carrying one
  delta's worth of text). Event-SET equality is therefore a post-aggregation equality on
  payloads, not on event-count. `message_start`, `ping`, `message_delta`, `message_stop` events
  match exactly. Interleaved-thinking fixtures: ORDER inequality is the documented live-mode
  wire difference (incremental emits in arrival order; batch groups thinking-before-text).
- `TestIncrementalStreamTranslator_HugeChunk` — single delta > 64KB (e.g. 100KB
  `reasoning_content`) — no truncation (scanner cap parity with `handler_anthropic.go:821`).
- `TestAnthropicLiveStream_ForwardBytesBeforeCompletion` — mock upstream emits
  OpenAI-format SSE chunks; assert client receives Anthropic `content_block_delta` events
  before upstream completes.
- `TestAnthropicLiveStream_InternalVariant` — internal credential path; same timing
  assertion; includes `Finalize`-after-`toolCallBuffer.Flush()` ordering.
- `TestAnthropicLiveStream_ThinkingEmittedOnWire` — provider emits thinking events; assert
  Anthropic `thinking_delta` events reach the client AND `arc.accumulatedThinking` is
  populated AND (buffered-mode control) the recorder still receives zero thinking bytes.
- `TestAnthropicLiveStream_SequentialFallbackBreak` — live mode: first model writes headers
  + bytes, then fails; assert the fallback loop does NOT switch models (loop-top guard);
  pre-first-byte failure still falls back.

**Flipped tests:**
- `translator/stream_test.go` — `TranslateBufferedStream` is unchanged, so its tests pass.
  No flip needed.
- `internal_handler_test.go:397` — `flushingResponseRecorder` is the buffered-mode test
  harness; verify the test invokes `handleStream` directly (not through `HandleRequest`)
  and stays in buffered mode. If a new test invokes the live mode, use `httptest.NewServer`
  for the real `w`.
- `handler_anthropic_test.go` — `handleAnthropicStreamResponse` and
  `handleAnthropicInternalStreamResponse` are unchanged in buffered mode; their tests pass.
  Live-mode tests are new.
- `handler_anthropic_tokenid_parity_test.go` — parity tests still run in buffered mode; pass.

**Risks + mitigations.**

| Risk | Impact | Likelihood | Mitigation |
|------|--------|-------------|-----------|
| New translator's emission order breaks Anthropic SDK clients | High (clients reject the wire) | Medium | Match `generateAnthropicEvents` ordering per block and message level; single-open-block interleave ruling; `TestIncrementalStreamTranslator_ParityVsBatch` pins the divergence; test with at least one external SDK shape (e.g., Anthropic Python SDK `messages.stream`). Residual assumption: SDK tolerance for interleaved thinking/text blocks — verify against a recorded real-Anthropic stream (flagged in `architecture-recommendation.md` §7). |
| Internal-Anthropic `thinkingSink` invariant violated when live-mode translator writes thinking | High (wire leak in buffered mode) | Low | Translator instance exists ONLY when `bufferMode==false`; sink code in `case "thinking"` unchanged; the translator is the only new write path and emits Anthropic-shaped events only. Triple-assertion test. |
| Deferred `content_block_stop` differs from batch's per-tool close (`stream.go:214`) — wire-shape divergence | Medium | Medium | Documented; SDK accepts blocks-close-at-message-end; `TestIncrementalStreamTranslator_DeferredClose_*` pins ordering. |
| `Finalize()` runs before `toolCallBuffer.Flush()` completes — final tool chunks processed after `message_stop` | High (malformed wire) | Medium | Ordering constraint pinned at the done site (`internal_handler.go:438-440`); ordering test in `TestAnthropicLiveStream_InternalVariant`. |
| Anthropic→Anthropic passthrough (`handler_anthropic.go:1225`) accidentally modified | Medium (regression on the one path that already worked) | Low | Diff-based test asserts byte-identity; Phase 3 diff scoped to the OpenAI-upstream and internal-Anthropic translation branches. |
| `IncrementalStreamTranslator` concurrent-call state race | Medium (data race) | Low | Translator is per-request, called serially from the scanner/event loop; `mu` in the type doc; note in tests. |
| Mid-stream upstream error mid-translation leaves the wire half-formed | High (client confusion) | Medium | On error: `Finalize()` (block-stops → `message_delta` → `message_stop`) THEN `sendAnthropicSSEError` (`:1129` Anthropic `error` event shape) — well-formed sequence guaranteed. |

**Exit criteria (independently testable).**

1. `TestIncrementalStreamTranslator_*` pass (including ParityVsBatch).
2. `TestAnthropicLiveStream_*` pass (first-byte-before-completion, thinking emitted on wire,
   internal variant incl. Finalize-ordering, mid-stream error, sequential-fallback break).
3. `handler_anthropic.go:1225` (passthrough) byte-identical to baseline.
4. All existing `handler_anthropic_test.go`, `handler_anthropic_tokenid_parity_test.go`,
   `translator/stream_test.go` tests pass unchanged.
5. `internal_handler_test.go:397` `flushingResponseRecorder` usage unchanged for buffered
   mode (live-mode tests use `httptest.NewServer`).
6. **Interleaved-block SDK verification (approver iteration 001 — synthesized OpenAI-chunk fixture per NB1; original "recorded real Anthropic SSE" formulation is vacuously satisfiable because real Anthropic SSE is sequential and the translator consumes OpenAI-wire chunks).**
   Synthesize an OpenAI-chunk fixture that exercises the **interleaved thinking/text** case the single-open-block-of-each-kind ruling depends on: alternating `reasoning_content` and `content` deltas (the same shape an OpenAI-streaming upstream would emit for an extended-thinking model), with at least one interleaved swap mid-stream. Feed the synthesized OpenAI chunks through `IncrementalStreamTranslator`; capture the emitted Anthropic SSE events; parse the captured events with the Anthropic SDK (`messages.stream` parser, SDK 0.40+) and assert it consumes without error. Fixture lives at `pkg/proxy/translator/testdata/synthesized_interleaved_openai_chunks.jsonl` (alongside the original recording path if retained for future use). **If the SDK rejects the interleaved pattern, the Phase 3 single-open-block-of-each-kind interleave ruling must be revisited — the plan assumes SDK tolerance; this verification is the gate.**

---

### Phase 4: Ultimate Model Paths Real Streaming

**Objective.** Real-stream the four ultimate-model paths (external stream/non-stream +
internal stream/non-stream), in the spirit of Phase 2's gate redefinition but with no race
coordinator. The execute path stays the same; the wire-side loop switches from buffered
`buf.Write` + end-of-stream single `w.Write+flusher.Flush` to per-line per-event
`w.Write+flusher.Flush`. Passive `ExecuteResult` capture (`Usage` / `Content` / `Thinking` /
`ToolCalls`) is preserved at all four paths.

**Context.** Today's ultimate paths:
- `pkg/ultimatemodel/handler_external.go:438-500` — upstream SSE scanner loop writes to
  `buf bytes.Buffer`; single `w.Write(buf.Bytes()) + flusher.Flush()` at `:499-500`.
- `pkg/ultimatemodel/handler_internal.go:484-735` — provider event channel `eventCh`
  switch; `case "content"` writes to `buf`; `case "done"` (`:700`) and `case "error"` (`:748`)
  do `w.Write+flusher.Flush`.

**Scope.**

**In scope:**

- Both paths gain a `rc.bufferMode` (or equivalent) read at the entry site.
- External stream path: per-line `w.Write+flusher.Flush` instead of `buf.Write` accumulation
  + end-of-stream single write, when `bufferMode==false`. The translator + toolcall buffer
  + normalizer chain still runs; capture-side `captureFromSSEChunkBytes` /
  `captureToolCallsFromSSEChunk` still populates `capturedContent` / `capturedThinking` /
  `capturedToolCalls`.
- Internal stream path: per-event `w.Write+flusher.Flush` for `case "content"` and per-tool
  for `case "tool_call"`, when `bufferMode==false`. `case "done"` and `case "error"` no longer
  need to drain `buf` because nothing accumulated.
- Non-stream paths (external non-stream `:225-235`, internal non-stream `handler_internal.go:313` (the `handleInternalNonStream` function)):
  unaffected — both modes return a single `w.Write(bodyBytes)` after the body is fully read.
  Header is a no-op for non-streaming requests (both modes write the entire body anyway).
- Trigger counting (handler.go:597-862) is pre-stream and stays pre-stream.
- H8 (no mid-stream flush) is **unchanged in buffered mode** (header present ⇒ buffered
  ⇒ single end-of-stream write); **dropped in live mode** (per-line flush).

**Out of scope:**

- Ultimate trigger schedule (locked, separate feature).
- Any change to `pkg/ultimatemodel/handler.go`'s `ShouldTrigger` / `ForceTrigger` /
  `SendRetryExhaustedError`.
- `pkg/ultimatemodel/handler_external.go:225-235` non-stream — already single-write.
- `pkg/ultimatemodel/handler_internal.go:313` non-stream — already single-write.

**Files (create / modify).**

| # | File | Action | What |
|---|------|--------|------|
| 1 | `pkg/ultimatemodel/handler.go` | MODIFY (around constructor + Execute, plus `pkg/ultimatemodel/heartbeat.go:21+` heartbeat path) | Thread `bufferMode bool` (or a `*ultimatemodel.RequestOptions` struct) from the caller into the execute path. Today's caller is `pkg/proxy/handler.go:597-862` — read `rc.bufferMode` there. **Approver iteration 001 — ultimate heartbeat race (Blocking 2):** `Execute` spawns `go startSSEHeartbeat(w, …)` (handler.go:720-726 area); `sendHeartbeat` (`pkg/ultimatemodel/heartbeat.go:47-69`) calls `w.Write(heartbeatData)` with NO mutex — in live mode with per-chunk `w.Write+flusher.Flush`, this is a net/http data race (the proxy-side heartbeat at `pkg/proxy/heartbeat.go:74-77` is writeMu-guarded; the ultimate-side is NOT). Pin a per-request write-mutex discipline on BOTH ultimate live paths (external + internal) mirroring the proxy-side `rc.writeMu`: a `sync.Mutex` per `Handler.Execute` invocation (or a `*sync.Mutex` in the ExecuteOptions struct), passed through to `startSSEHeartbeat` and used by both `sendHeartbeat` and the live relay loops. Implementation sketch: add a `writeMu *sync.Mutex` field to `ultimatemodel.Handler` (or a request-scoped struct passed through); `sendHeartbeat` does `h.writeMu.Lock(); defer h.writeMu.Unlock(); w.Write(...)`; live relay loops `w.Write+flusher.Flush` acquire `writeMu` for each chunk. Buffered mode is unaffected (single end-of-stream write vs heartbeat concurrency window is microscopic today; in live mode the window becomes the whole stream). |
| 2 | `pkg/proxy/handler.go` | MODIFY (around :597-862) | Pass `rc.bufferMode` through to `ultimatemodel.Handler.Execute` (or via `ExecuteOptions`). |
| 3 | `pkg/ultimatemodel/handler_external.go` | MODIFY (around :280-510) | When `bufferMode==false`: per-line `w.Write(chunk)+flusher.Flush` instead of `buf.Write` accumulation. End-of-stream single write (`:499-500`) becomes a no-op for the live branch. Translator + toolcall buffer + normalizer chain unchanged. |
| 4 | `pkg/ultimatemodel/handler_internal.go` | MODIFY (around :484-755) | When `bufferMode==false`: per-event `w.Write(data)+flusher.Flush` for `case "content"` and per-tool for `case "tool_call"`. `case "done"` and `case "error"` still emit `[DONE]` / error envelope + flush, but don't drain `buf`. |
| 5 | `pkg/ultimatemodel/handler_external_test.go` | MODIFY (append tests) | Live-stream tests for the external path. |
| 6 | `pkg/ultimatemodel/handler_internal_test.go` | MODIFY (append tests) | Live-stream tests for the internal path. |
| 7 | `pkg/proxy/handler_capture_persistence_test.go` | MODIFY (append tests) | Capture persistence verified in both modes (the captured `ExecuteResult` fields are unchanged regardless of wire mode). |

**Tasks.**

| # | Task | Acceptance | Design Notes |
|---|------|------------|--------------|
| 4.1 | Thread `bufferMode` from `handler.go` to ultimate `Execute` | `Execute(ctx, tokenModelID, requestBody, w, …)` gains an `ExecuteOptions` struct (or new param) carrying `BufferMode`. | Default false; existing call sites pass `false`. Header-driven callers pass `rc.bufferMode`. |
| 4.2 | External live-stream path | `handler_external.go` per-line `w.Write+flusher.Flush` when `BufferMode==false`; **a CAPTURE-SIDE accumulator (byte parity of `Usage` between buffered/live — NOT an accepted degradation) is retained** so `token.ExtractCompletionTextFromChunks(buf.Bytes())` at `:483` (tokenizer-fallback estimator) sees the full completion text in live mode. The wire-side loop goes per-chunk `w.Write(chunk); flusher.Flush()`; the SAME chunks are also appended to a capture-side `var captureBuf bytes.Buffer` whose `captureBuf.Bytes()` is the estimator input. Capture-side `captureFromSSEChunkBytes` / `captureToolCallsFromSSEChunk` unchanged. | The capture-side accumulator is the same `bytes.Buffer` instance — wire bytes and estimator input are byte-identical. Buffered mode keeps today's behavior (single `w.Write(buf.Bytes()) + flusher.Flush()` at `:499-500`; `buf.Bytes()` is the estimator input AND the wire input). In live mode the capture-side accumulator feeds ONLY the estimator; the wire gets per-chunk writes. |
| 4.3 | Internal live-stream path | `handler_internal.go` per-event `w.Write+flusher.Flush` for content/tool_call; **a CAPTURE-SIDE accumulator is retained** so `token.ExtractCompletionTextFromChunks(buf.Bytes())` at `:710` (done-arm fallback estimator) sees the full completion text in live mode. `case "done"` and `case "error"` still emit `[DONE]` / error envelope + flush; on `case "done"` the capture-side `captureBuf.Bytes()` is the estimator input (instead of today's `buf.Bytes()` which is dropped in live mode). **Dead-accumulator disposition (approver iteration 001 NB7):** the existing `var rawChunks bytes.Buffer` at `handler_internal.go:481` (plus the `rawChunks.Write(buf.Bytes())` writes at `:726` and `:747`) is DEAD in both modes after Phase 4 — the live branch feeds the estimator from `captureBuf.Bytes()` and the buffered branch feeds it from `buf.Bytes()` (the original sink). REMOVE `rawChunks` (and its two writes) entirely; the fallback estimator input is whichever buffer was used as the estimator sink. Verified no other reader — `rawChunks` has no readers outside the done-arm / error-arm in this file. | Same pattern as 4.2: the capture-side accumulator is appended in the per-event loop (`case "content"`: write to `w`, flush, append synthesized chunk bytes to `captureBuf`). Buffered mode keeps today's behavior (single write at `:727-728` of `buf.Bytes()` which is BOTH wire input AND estimator input). |
| 4.4 | Verify `ExecuteResult` capture parity — estimator parity is REQUIRED, not best-effort | `TestUltimateCapture_LiveMode_IdenticalToBuffered` — same provider events → same `ExecuteResult.Content`/`Thinking`/`ToolCalls`/`Usage` in both modes. **MANDATORY no-usage-chunk fixture (C1 regression pin):** the upstream emits ONLY SSE data lines, NO `usage` chunk — the tokenizer-fallback estimator must produce byte-identical `Usage.CompletionTokens` between buffered and live runs. Failure mode if C1 regresses: live-mode `Usage.CompletionTokens=0` while buffered-mode reports the correct count. | The capture-side helpers (`captureFromSSEChunkBytes`, `captureToolCallsFromSSEChunk`, `captureFromTypedEvent`, the `internal_handler` `case "thinking"` sink) are unchanged in both modes. The capture-side accumulator (tasks 4.2/4.3) is what makes estimator parity achievable — without it the live-mode estimator always sees an empty buffer and reports 0. |
| 4.5 | Trigger counting pre-stream | `handler.go:597-862` ultimate check is unchanged — counting happens before streaming. | No file change beyond threading `bufferMode` through. |
| 4.6 | Tests | Live-stream tests for both paths; parity tests for `ExecuteResult` capture; header-driven dispatch tests. | See Test strategy. |

**Test strategy.**

**New tests:**
- `TestUltimateExternal_LiveStream_ForwardBytesBeforeCompletion` — mock upstream emits
  SSE chunks; assert client receives them before upstream `[DONE]`.
- `TestUltimateInternal_LiveStream_ForwardBytesBeforeCompletion` — mock provider emits
  events; assert client receives `data: …\n\n` before `case "done"` closes the channel.
- `TestUltimateCapture_LiveMode_IdenticalToBuffered` — assert `ExecuteResult` fields
  match between buffered and live runs for the same provider event sequence. **Includes a
  MANDATORY no-usage-chunk fixture** (C1 regression pin, mirrors task 4.4 wording): upstream
  emits ONLY SSE data lines, NO `usage` chunk — tokenizer-fallback estimator must produce
  byte-identical `Usage.CompletionTokens` between buffered and live runs; failure mode if
  the capture-side accumulator regresses: live-mode `CompletionTokens=0`.
- `TestUltimate_HeaderDispatch` — `X-LLMProxy-Buffer-Response: true` selects buffered
  path; absence selects live.
- `TestUltimate_HeartbeatRaceVsLiveRelay` (approver iteration 001 Blocking 2) — `-race`
  test with a SHORTENED heartbeat interval (override `pkg/ultimatemodel/heartbeat.HeartbeatInterval`
  from `15*time.Second` to e.g. `50*time.Millisecond` via a package-level hook gated on a
  build tag or env override — design choice deferred, either works) against a chunked live
  stream: provider emits N chunks at intervals ≫ heartbeat tick; assert `go test -race
  ./pkg/ultimatemodel/...` reports zero data races. Counter-test WITHOUT the per-request
  write-mutex (comment-only assertion — the mutex must be in place) should report the race;
  pin the test in the gated form to verify the fix holds.

**Flipped tests:**
- H8 ("no mid-stream flush") tests in `pkg/ultimatemodel/*_test.go` — these assert the
  buffered-mode single-write behavior. They MUST be updated to either (a) explicitly set
  the header to opt into buffered mode, or (b) split into two tests: buffered-mode
  (header set) and live-mode (header absent). The wire-form gotcha applies to source-set
  headers in test assertions — assert wire-canonicalized form (`X-Llmproxy-...`) for any
  source-set headers; client-SENT headers are case-insensitive.
- `handler_capture_persistence*` tests — capture-side unchanged, so these tests pass as-is
  in buffered mode; new live-mode tests added.

**Risks + mitigations.**

| Risk | Impact | Likelihood | Mitigation |
|------|--------|-------------|-----------|
| External path: per-line flush races with toolcall buffer's per-call JSON-completion hold (`pkg/toolcall/buffer.go:219-222`) | Medium (tool-call deltas split across flushes) | Medium | The toolcall buffer's `ProcessChunk` returns chunks to emit; in live mode the loop is per-iteration `for _, chunk := range chunksToEmit { w.Write(chunk); flusher.Flush() }`. Buffer's per-call hold (emit only when JSON complete) is preserved — multiple chunks per upstream line is the current behavior. |
| Internal path: `case "thinking"` writes to wire AND sink simultaneously | High (recorder sink invariant violated) | Medium | The sink is captured for persistence; in live mode the wire-side emission is ADDITIONAL. The original fea5874 base dropped thinking from wire entirely; live mode RESTORES it as Anthropic `thinking_delta` (for OpenAI-wire ultimate with translation) or as OpenAI `reasoning_content` chunk (for direct OpenAI-wire ultimate). The translator (Phase 3) or the OpenAI delta writer handles this; this phase just moves the bytes earlier. Cross-ref: Phase 3 task 3.5 pins the translator as the ONLY wire-write path for content/tool_call/thinking in live mode (no double-write). |
| `ExecuteResult` capture diverges between buffered and live modes | High (UI records wrong content) | Low | Capture-side helpers are unchanged; only the wire-side loop changes. Parity test (`TestUltimateCapture_LiveMode_IdenticalToBuffered`) is the safety net. |
| Live mode's per-event flush floods the wire on MiniMax `interleaved-thinking` translator expansion | Medium (rate-limit / network pressure) | Low | The translator's `ChunkBytes` returns `(stripped, emitted)` — `emitted` may be a list of synthesized chunks. In live mode each is `w.Write+flusher.Flush`; in buffered mode each is `buf.Write`. Wire pressure is comparable to today's; no flooding risk introduced. |
| Existing H8 tests break the build when CI runs without header | High (CI red) | High | Update tests to explicitly set `X-LLMProxy-Buffer-Response: true` (or pass `BufferMode=true` via the new param) where they assert buffered behavior. New live-mode tests assert live behavior. |
| **Ultimate heartbeat race vs live-mode relay writes (approver iteration 001 Blocking 2)** | `pkg/ultimatemodel/heartbeat.go:21+` (`sendHeartbeat` at `:47-69` calls `w.Write(heartbeatData)` with NO mutex); in live mode per-chunk `w.Write+flusher.Flush` runs concurrently with `go startSSEHeartbeat(w, …)` (handler.go:720-726 area). | High (concurrent `http.ResponseWriter` writes per net/http contract; SSE framing corruption possible — heartbeat comment torn into a data: line) | Medium (latent today; pervasive under this plan) | Per-request write-mutex discipline (Files row 1) on BOTH ultimate live paths mirrors proxy-side `rc.writeMu`: `sendHeartbeat` and live relay loops acquire the mutex. `-race` test (Phase 4 test strategy) with shortened heartbeat interval (override `HeartbeatInterval` from 15s to e.g. 50ms for the test, gated on a build-tag or env override) against a chunked live stream; assert zero data races (`go test -race ./pkg/ultimatemodel/...`). Buffered mode unaffected (window microscopic; documented). |

**Exit criteria (independently testable).**

1. `TestUltimate*_LiveStream_*` pass.
2. `TestUltimateCapture_LiveMode_IdenticalToBuffered` passes.
3. Existing `handler_capture_persistence*` tests pass unchanged in buffered mode.
4. H8 tests updated to opt into buffered mode (or split into buffered/live pairs).
5. `TestUltimate_HeaderDispatch` confirms header-driven path selection.

---

### Phase 5: Parity + Acceptance Harness

**Objective.** A single integrated test suite proves both invariants:
(a) **PARITY** — header present ⇒ bit-for-bit current behavior across all surfaces
(wire bytes, SSE framing/timing, retry/fallback/racing, events, UI request records, usage
metering, bufferstore).
(b) **REAL STREAMING** — header absent ⇒ first content byte forwarded to the client BEFORE
upstream completion (deterministic via mock upstream with blocking/partial writes +
client-side read timing). Under R1-variant this includes the gate itself: winner selected at
the first forwardable chunk.

**Context.** Phases 1-4 land isolated, but no single test proves the feature end-to-end. This
phase consolidates the gates.

**Scope.**

**In scope:**

- One new test file per surface (or a new top-level `real_streaming_parity_test.go`).
- A "parity matrix" test that exercises each path with the header set and asserts byte-level
  identity against a baseline recorded BEFORE Phase 2/3/4 land (snapshot/golden file).
- A "first-byte timing" test per path with the header absent.
- Documentation update: in-code `// PARITY:` comments at the dispatch sites, plus a doc in
  `docs/` (or `AGENTS.md`) summarizing the feature + accepted degradations.

**Out of scope:**

- New production code paths (this is test-only + docs).
- Renaming of the header (locked L1).

**Files (create / modify).**

| # | File | Action | What |
|---|------|--------|------|
| 1 | `pkg/proxy/real_streaming_parity_test.go` | **CREATE** | End-to-end parity suite across all six paths (OpenAI race, ultimate-external stream, ultimate-internal stream, Anthropic→Anthropic passthrough, Anthropic→OpenAI-wire translation, Anthropic-internal). |
| 2 | `pkg/proxy/real_streaming_firstbyte_test.go` | **CREATE** | First-byte timing suite across the same six paths. |
| 3 | `pkg/proxy/real_streaming_golden_test.go` | **CREATE** | Golden-file recorder: capture the buffered-mode wire output once, replay as the parity baseline. |
| 4 | `pkg/proxy/testdata/real_streaming_golden/*.json` | **CREATE** | Golden wire traces for each path (one per path, with content + thinking + tool-call + usage variants; include provider-shaped FIRST chunks — role-only, usage-only, comment-first). |
| 5 | `AGENTS.md` | MODIFY (append) | One-paragraph summary: header, default behavior, accepted degradations. |
| 6 | `docs/real-streaming-default.md` | **CREATE** | Detailed doc: header semantics, per-path behavior, feature table (preserved / degraded), rollback procedure. MUST document: live-mode thinking blocks (internal variant), live-mode block interleaving, winner-validity (first-byte winner may fail; partial + envelope, no retry), StreamDeadline redefinition (live mode), `race_winner_selected` mid-stream timing, and the R2 Prune/entry-capture gating. |

**Tasks.**

| # | Task | Acceptance | Design Notes |
|---|------|------------|--------------|
| 5.1 | Generate golden traces | Run the test fixtures in buffered mode; record wire output to `testdata/real_streaming_golden/*.json`. | Single-shot golden generator; reruns overwrite. Commit the goldens. |
| 5.2 | Parity matrix test | For each path × each content variant (text / thinking / tool_call / usage), assert the buffered-mode wire bytes match the golden file byte-for-byte. | Uses `bytes.Equal` after `httptest.NewServer` captures. |
| 5.3 | First-byte timing test | For each path × minimal text variant, assert client reads the first byte within `< T` of upstream writing it, where `T` is the upstream's inter-chunk delay minus a 50ms safety margin. For the race path: winner selected at first forwardable chunk (predicate variants included). | Mock upstream with controllable `time.Sleep` between chunks. |
| 5.4 | Event-publication parity | Assert `request_started` / `request_completed` / `model_credential_selected` / `request_failed` event sequences match between buffered and live modes (within timing tolerance; `race_winner_selected` fires mid-stream in live mode — presence asserted, completion-assumptions not). | Race-internal path publishes `model_credential_selected`; Anthropic path does NOT (locked L7). |
| 5.5 | UI request records parity | Assert `rc.reqLog` / `arc.reqLog` / `ExecuteResult` capture identical fields in both modes. | Reuses the existing capture-persistence tests; no new tests in this dimension. |
| 5.6 | Usage metering parity | Assert `h.counter.Increment(...)` called with the same token counts in both modes (where upstream returns `usage`). PLUS the tokenizer-fallback variant: no usage chunk + prune pressure ⇒ non-zero completion tokens in live mode (R2 fix). | Reuses existing usage tests + the Phase 2 R2 regression pin. |
| 5.7 | Bufferstore parity | Assert `saveRawResponse` called with the same byte ranges in both modes (where enabled). Live mode: Done capture complete; no partial entry capture. | Developer verifies `bufferStore.Save` overwrite semantics for the buffered-mode double-save before finalizing the assertion shape. |
| 5.8 | Docs | `docs/real-streaming-default.md` + `AGENTS.md` paragraph. | Per scope entry above; the documented wire changes list is mandatory. |
| 5.9 | Rollback procedure | Document the env var / header toggle to disable live mode globally if a regression ships. | Two options: (a) feature flag in `Config`; (b) client default header on the load balancer. Recommend (b) for minimal code change; document both. |
| 5.10 | **6-path rollout order (approver iteration 001 NB5)** | Roll out the parity + acceptance harness in a fixed order so a failure in path N is bisected against the last-green path N-1. The order is: (1) Anthropic→Anthropic passthrough (`handler_anthropic.go:1225`, locked L3 — verify untouched), (2) Anthropic→OpenAI-wire external translation (`handler_anthropic.go:819/:851` LiteLLM), (3) Anthropic→OpenAI-wire internal translation (`doAnthropicInternalRequest` at `:600-685`), (4) `/v1/chat/completions` OpenAI race path, (5) Ultimate external stream (`handler_external.go:438-500`), (6) Ultimate internal stream (`handler_internal.go:484-755`). | Each path flips its parity baseline + acceptance gate independently; rollout ordering keeps the bisection chain tight (each path differs from the prior by exactly one architectural surface). |
| 5.11 | **Parity-failure bisection procedure (approver iteration 001 NB5)** | When `TestRealStreaming_ParityMatrix_AllPaths` fails on a path, the bisection procedure is: (a) verify the path is in the header-PRESENT branch (`X-LLMProxy-Buffer-Response: true`); (b) compare wire bytes against the golden file and identify the first divergent byte/line; (c) check the §6 master test-flip inventory to see if a test on that path was flipped (and whether the flip was applied correctly); (d) check the path's bufferMode threading (Phase 1 plumbing); (e) bisect the commit range using `git bisect` against the failing fixture. | Bisection is mechanical — no heuristic guessing. The Phase 5 docs reference this procedure by name. |
| 5.12 | **§10 #11 gate-form safety re-review (approver iteration 001 NB5)** | Open a tracked issue at merge time to re-verify the Phase-2 CONSTRAINTS gate form (ticker arm `:406-435` + `onceStream` `:431` + winner-nil `:653-655`) on the merged code. The §10 #11 assumption ("resilience review's safety conclusions assume the predicate lands in the existing manage-loop ticker arm") is the only place the safety story is told — a re-review at merge time confirms no later refactor changed the form. | Tracked issue, not a code task; linked from the §10 #11 row directly. |

**Test strategy.**

**New tests:**
- `TestRealStreaming_ParityMatrix_AllPaths` — table-driven over (path, content variant) →
  asserts wire byte equality.
- `TestRealStreaming_FirstByteTiming_AllPaths` — table-driven over (path) → asserts first
  byte reaches client before upstream completes.
- `TestRealStreaming_Events_BufferedEqualsLive` — assert event payloads match.
- `TestRealStreaming_RollbackViaHeader` — assert every code path respects the header.

**Flipped tests:** none (this phase is purely additive — golden files + new tests).

**Risks + mitigations.**

| Risk | Impact | Likelihood | Mitigation |
|------|--------|-------------|-----------|
| Golden files drift due to upstream SSE ordering (e.g., `id` field uses `time.Now().UnixNano()`) | Medium (CI noise) | High | Strip `id` / `created` / timestamp fields before comparison OR commit goldens once and update manually. |
| First-byte timing test flaky on CI | High (CI red) | High | **Event-based assertion (approver iteration 001 NB5):** instead of relying on a wall-clock threshold (which `rerun-on-flake` policy would mask), subscribe to `events.Bus` and assert that a `race_winner_selected` (live-mode) / `request_completed` event with the request's ID is observed BEFORE the upstream `[DONE]` is written — convert the timing assertion to a structural one: "first byte reaches client wire before upstream `[DONE]`" is equivalent to "client reads first byte before observer sees upstream-EOF"; both observable from `events.Bus` + a mock upstream. Use a generous timing budget as a secondary signal (upstream sleeps 500ms; assertion threshold 250ms; tick +100ms budgeted) but the gating assertion is the event-ordering one. |
| Event-publication test fragile to event-bus ordering | Medium (CI noise) | Medium | Use `events.Bus.Subscribe` with a 1-second wait + filter by request_id; assert event presence, not exact ordering. |
| Docs drift from code | Low | Medium | Pin the doc to a specific commit; require doc update in the PR description when behavior changes. |

**Exit criteria (independently testable).**

1. `TestRealStreaming_ParityMatrix_AllPaths` passes for all 6 paths × all content variants.
2. `TestRealStreaming_FirstByteTiming_AllPaths` passes for all 6 paths.
3. `TestRealStreaming_Events_BufferedEqualsLive` passes.
4. `TestRealStreaming_RollbackViaHeader` passes (header opt-in restores buffered mode for
   every path).
5. `docs/real-streaming-default.md` and `AGENTS.md` summary exist and reflect current
   behavior.
6. **Feature complete gate**: running the binary with no header on a streaming request
   produces the first content byte on the wire before upstream completion; running with
   `X-LLMProxy-Buffer-Response: true` produces byte-identical output to `latest @ 9842c77`.

---

## 6. Cross-Phase Dependency Map

| | Phase 1 | Phase 2 | Phase 3 | Phase 4 | Phase 5 |
|---|---|---|---|---|---|
| Phase 1 | — | tight (rc.bufferMode plumbing) | tight (rc.bufferMode plumbing) | tight (bufferMode threading to ultimate) | loose (header parse contract) |
| Phase 2 | tight | — | independent | independent | tight (Phase 5 covers Phase 2 paths) |
| Phase 3 | tight | independent | — | independent | tight (Phase 5 covers Phase 3 paths) |
| Phase 4 | tight | independent | independent | — | tight (Phase 5 covers Phase 4 paths) |
| Phase 5 | loose | tight | tight | tight | — |

**Parallelizable after Phase 1:** Phases 2, 3, 4 are independent (different code paths, only
share the Phase 1 plumbing contract). A team of three developers could land them in parallel
without merge conflicts as long as each phase touches a disjoint set of files (verified in
the Files tables above — Phase 2 owns `race_coordinator.go`/`race_executor.go` +
`handler.go:924+` relay region; Phase 3 owns `handler_anthropic.go`/`internal_handler.go` +
`translator/`; Phase 4 owns `ultimatemodel/` + `handler.go:597-862` threading region; the two
`handler.go` regions are disjoint).

**Phase 5 is sequential** after 2/3/4 (its tests reference all three phases' new code).

**Coupling notes:**
- Phase 2 → Phase 3: no shared new functions (Phase 2 adds NO new relay function — the gate
  redefinition reuses `streamResult`; Phase 3's live handlers are new Anthropic-side
  functions). Both must NOT race on the heartbeat goroutine (both use `rc.writeMu`
  discipline). No file overlap.
- Phase 4 → Phase 3: Phase 4's internal-ultimate path uses Phase 3's translator? **No.**
  Phase 4's internal-ultimate path emits OpenAI-wire chunks (not Anthropic) — it does NOT
  touch the translator. Confirmed at `pkg/ultimatemodel/handler_internal.go:486-529` (the
  `case "content"` arm synthesizes an OpenAI chunk).
- Phase 4 → Phase 2: both modify `pkg/proxy/handler.go` (Phase 2 around `:924-1380`, Phase 4
  around `:597-862`). Different line ranges; safe to merge.

---

## 7. Master Test-Flip Inventory

| # | Test file | Line(s) | What flips | Required update |
|---|-----------|---------|------------|------------------|
| 1 | `pkg/proxy/internal_handler_test.go` | :397 | `flushingResponseRecorder{httptest.NewRecorder()}` is the buffered-mode harness for `handleStream` (called from `runHandleStreamWithEvents`). In live mode, `handleStream` is no longer the entry point — `InternalHandler.HandleRequest` gets the true `w`. | Verify the test calls `handleStream` directly (not through `HandleRequest`); the call stays in buffered mode. New live-mode tests use `httptest.NewServer` for the real `w`. **Most likely no update needed** — confirm at implementation time. |
| 2 | `pkg/proxy/translator/stream_test.go` | :50, :112, :176, :252, :313, :357, :418 | `TranslateBufferedStream` is unchanged; tests stay valid. | None. |
| 3 | `pkg/proxy/translator/translator_test.go` | multiple | Batch translator; unchanged. | None. |
| 4 | `pkg/proxy/stream_buffer_test.go` | all | `streamBuffer` is unchanged; tests stay valid. | None. (Prune is gated at the handler call sites, not inside the buffer.) |
| 5 | `pkg/proxy/race_request_test.go` | all | `upstreamRequest` struct gains NO new fields in Phase 2 (Q4 reuses `rc.streamingNonRetryable` on the `requestContext` side, not the `upstreamRequest` side). | None. |
| 6 | `pkg/proxy/handler_anthropic_test.go` | all | `handleAnthropicStreamResponse` and `handleAnthropicInternalStreamResponse` are unchanged in buffered mode. | None for existing tests. New live-mode tests added. |
| 7 | `pkg/proxy/race_coordinator_test.go` | all | **Coordinator logic IS changed** (live-mode predicate swap at `:406-435` + deadline 0-byte guard at `:661-722`) — superseding the earlier "coordinator unchanged" claim. | Existing tests asserting the `IsCompleted` winner gate / 0-byte deadline promotion opt into buffered mode via the header (or explicit `bufferMode=true`). NEW gate + guard tests added for live mode. |
| 8 | `pkg/proxy/race_coordinator_test.go`, `pkg/proxy/race_coordinator_credfailover_test.go`, `pkg/proxy/handler_lb_phase5_test.go` (verified by inspection) | all | Tests assert credential LB publishes / fails over in race-internal path. Live mode disables racing after first byte; pre-first-byte race + failover are unchanged. | Add `X-LLMProxy-Buffer-Response: true` header to tests asserting post-winner failover (so the test runs in buffered mode where today's behavior holds). Live-mode tests added for "no mid-stream failover". |
| 9 | `pkg/proxy/handler_capture_persistence_test.go`, `handler_capture_persistence_toolcall_test.go` | all | Capture-side helpers unchanged in both modes; `ExecuteResult` fields populated identically. | None. New live-mode tests added. |
| 10 | `pkg/proxy/handler_test.go` | multiple (likely `TestStreamBuffered`, `TestStreamWaitForDone`, etc.) | Tests asserting "wait for `[DONE]` then forward" need to opt into buffered mode via header. | Add `X-LLMProxy-Buffer-Response: true` header to existing tests asserting buffered semantics. New live-mode tests added asserting first-byte forwarding. |
| 11 | `pkg/proxy/handler_integration_test.go` | multiple | Same as #10 — integration tests asserting race winner / fallback semantics need the header to opt into buffered mode. | Add header to existing buffered-behavior tests. |
| 12 | `pkg/proxy/handler_anthropic_tokenid_parity_test.go` | all | Parity tests stay in buffered mode. | None. |
| 13 | `pkg/proxy/handler_finalize_test.go`, `handler_helpers_test.go`, `handler_functions_test.go`, `heartbeat_test.go`, `heartbeat_mock_test.go` | all | No behavior change in Phase 1-5 for these helpers / finalize / heartbeat. | None. |
| 14 | `pkg/proxy/handler_nonstream_thinking_test.go`, `counting_hooks_test.go`, `authenticate_test.go`, `adapter_*_test.go`, `normalizer_buffer_order_test.go` | all | Non-streaming paths unaffected; counting/auth/normalizer logic unchanged. | None. |
| 15 | `pkg/ultimatemodel/handler_external_test.go` | H8-related (single-write assertions) | Tests asserting end-of-stream single write + no mid-stream flush. | Add `BufferMode=true` (or set the header via the new param) to opt into buffered mode. Split into buffered / live tests where appropriate. |
| 16 | `pkg/ultimatemodel/handler_internal_test.go` | H8-related + per-event-write assertions | Similar to #15. | Same as #15. |
| 17 | `pkg/ultimatemodel/handler_capture_persistence_*` (if any) | all | Capture-side unchanged. | None. |
| 18 | `pkg/proxy/internal_handler_failover_test.go` | all | Pre-first-byte rate-limit failover unchanged in both modes. | None. New live-mode test asserts no post-first-byte failover. |
| 19 | `pkg/proxy/race_retry_test.go` | :72, :134, :183, :241, :285, :338 (deadline-path tests setting `StreamDeadline: 5s`) | These exercise the `IsCompleted` winner gate and the deadline's 0-byte-promotion semantics — both redefined in live mode. | Opt into buffered mode via the header (or explicit `bufferMode=true`); live-mode deadline tests added (`TestLiveRelay_StreamDeadlineInLiveMode`). |

**Wire-form gotcha.** Headers SET by the proxy (e.g., `X-Llmproxy-Ultimate-Model` per the
project blueprint "Ultimate Model Raw Proxy" §Headers) appear wire-canonicalized. Client-SENT
headers via `r.Header.Get` are case-insensitive for free. In tests:
- Asserting what the proxy writes back to the client: use the wire-canonicalized form.
- Setting headers on the test request: any case is fine.

---

## 8. Preserve / Degrade Tables

### 8.1 PRESERVED in real-streaming mode

| Feature | Where | Notes |
|---------|-------|-------|
| Normalizers (empty_role, split_concatenated, tool_call_index, tool_call_repair) | `pkg/proxy/normalizers/` registry | Per-chunk — runs on every upstream chunk in both modes; live mode forwards each normalized chunk immediately. |
| Per-chunk MiniMax translator | `pkg/proxy/translator/minimax_stream.go` (`StreamTranslator.ChunkBytes`) | Lives in the ultimate-external pipeline; per-chunk; preserves emit ordering. |
| Tool-call buffering with per-call hold | `pkg/toolcall/buffer.go:219-222` (`isCompleteLocked`) | Emits at per-call JSON completion. Live mode forwards each emitted chunk; the per-call hold is unchanged. |
| Heartbeat keepalives (15s SSE comments) | `pkg/proxy/heartbeat.go:2` (proxy path, writeMu-guarded) AND `pkg/ultimatemodel/heartbeat.go:21+` (ultimate path; was unserialized pre-approver-iteration-001) | Runs in both modes; cancellation unchanged. Pre-winner window shrinks in live mode (proxy). **Ultimate heartbeat (approver iteration 001 Blocking 2 fix):** Phase 4 pins a per-request write-mutex discipline (`ultimatemodel.Handler.writeMu *sync.Mutex` threaded through to `sendHeartbeat` and the live relay loops) mirroring `pkg/proxy/heartbeat.go:74-77`'s `rc.writeMu`. Buffered mode is single-write so the concurrency window is microscopic; live mode now serializes heartbeat writes against per-chunk `w.Write+flusher.Flush` for the whole stream. |
| Usage metering | `handler.go:820-849` (`counter.Increment`) | Final-chunk tap at `handler.go:1189-1204`. **Architect correction:** the tokenizer-FALLBACK estimator consumes `winner.GetBuffer().GetAllRawBytesOnce()` raw bytes (`handler.go:1220`), NOT `rc.accumulatedResponse` — its live-mode completeness is preserved by the Phase 2 Prune gating (task 2.4). Where upstream returns `usage`, metering is identical in both modes. |
| **Ultimate-path tokenizer-fallback estimator parity (C1, REGRESSION PIN)** | `handler_external.go:483` (`token.ExtractCompletionTextFromChunks(buf.Bytes())`) and `handler_internal.go:710` (done-arm) | Phase 4 retains a **capture-side accumulator** in live mode (tasks 4.2/4.3) so the estimator sees byte-identical completion text in both modes. `Usage.CompletionTokens` is parity (NOT an accepted degradation); the MANDATORY no-usage-chunk fixture in `TestUltimateCapture_LiveMode_IdenticalToBuffered` (task 4.4) is the regression pin. Failure mode if the accumulator is dropped: live-mode `CompletionTokens=0` whenever upstream omits a `usage` chunk. |
| Inline content tap (`extractStreamChunkContent`) | `pkg/proxy/handler_helpers.go` | Per-chunk accumulation into `rc.accumulatedResponse` / `accumulatedThinking` / `accumulatedToolCalls` — runs in both modes. UI request records stay identical. |
| Bufferstore debug dumps | `handler.go:1159-1186` (`saveRawResponse`) | **Architect correction:** in live mode the Done capture (`:1182-1187`) is authoritative and complete (Prune gated); the ENTRY capture (`:1053-1058`) is gated to buffered mode (it would save a partial prefix + double-save in live mode). Buffered mode keeps today's entry+Done behavior. |
| Credential LB pre-stream `GetOrSelect` | `pkg/proxy/race_executor.go:executeRequest` / `internal_handler.go:152` | Pre-stream resolution is unchanged in both modes. Locked L7 (Anthropic path does not publish `model_credential_selected`). Pre-first-byte 429 failover window verified UNCHANGED under the first-byte gate — **NB (M7):** the claim "429s cannot win the predicate" applies ONLY to HTTP-status 429s surfaced via `*providers.ProviderError` (`race_executor.go:531-543`); in-stream error chunks surfaced via `isStreamErrorChunk` (`handler_helpers.go:619`, used at `race_executor.go:1583`) return plain `fmt.Errorf` and NEVER classify as rate-limit, so the predicate hazard fix (task 2.3 reorder) is what guards in-stream errors from winning. |
| Ultimate trigger counting | `pkg/ultimatemodel/handler.go` `ShouldTrigger` | Pre-stream; unchanged. |

### 8.2 ACCEPTED degradations in real-streaming mode

| Degradation | Reason | Impact | Mitigation |
|-------------|--------|--------|------------|
| No racing / fallback / retry AFTER first forwarded byte (OpenAI race path) | Architecturally incompatible — a retry would interleave bytes from a different upstream | Clients lose the race-retry feature on streaming requests after the first byte | **Pre-first-byte race is fully preserved** — the coordinator runs with racing, fallback chains, and credential failover exactly as today until the first-byte winner fires (`architecture-recommendation.md` §1.1.6). Post-first-byte failures produce a terminal in-band SSE error envelope (Phase 2 §2.3/§2.4 tasks). Winner-validity (first-byte winner may later fail; a loser might have succeeded) is leader-ACCEPTED and documented. Documented in `docs/real-streaming-default.md`. |
| **Anthropic sequential fallback loop stops after first byte** (per addendum spec point ii) | `handler_anthropic.go:256` loop iterates `arc.modelList`; once `arc.headersSent==true` and live mode, switching models would interleave bytes | Clients with a model `fallback_chain` lose mid-stream fallback on streaming requests | Pre-first-byte fallback is still supported (loop iteration 0 with headers not yet sent); post-first-byte the loop exits via the loop-top guard (Phase 3 §3.4 — RESOLVED placement). |
| **Pre-first-byte upstream failure surface changes** (per addendum spec point i) | SSE headers + `: connected` written pre-attempt at `handler.go:882-889`; L4 disallows fallback after first byte, so the failure must be an in-band SSE error envelope | In HTTP-error terms, clients see HTTP 200 (already sent) + an SSE error data line. Acceptable per Anthropic + OpenAI SSE error envelopes. | Wire shape matches `handler.go:1169-1178` verbatim. Tested by Phase 2 `TestLiveRelay_PreFirstByteError`. In live mode the StreamDeadline-expiry failure surfaces the same way (0-byte guard → `GetStreamDeadlineError` → in-band envelope). |
| No mid-stream credential failover | Same as above — failover would interleave bytes from a different credential | Clients lose rate-limit-driven credential failover after first byte | Pre-first-byte rate-limit failover (the Phase 3 LB work) still runs; post-first-byte failover is gone. Documented. |
| Tokenizer-fallback usage estimator: **no degradation in EITHER mode — CONDITIONAL on the Phase 2 Prune gating** (architect-corrected) | **Architect correction (R2):** the estimator consumes `winner.GetBuffer().GetAllRawBytesOnce()` RAW buffer bytes (`handler.go:1220`), not `rc.accumulatedResponse`. Unmitigated, mid-stream Prune (`:1082/:1122/:1167/:1238/:1357` → `stream_buffer.go:282-289`) truncates that input NON-DETERMINISTICALLY in live mode (worst ≈0, best full). Phase 2 task 2.4 gates Prune (and the entry raw-capture) to buffered mode; Prune already never fires in buffered mode, so effective behavior is preserved in both modes. | None (with task 2.4); usage undercount (without) | Regression pin: `TestLiveRelay_R2_EstimatorUnderPrunePressure` (non-zero completion tokens under prune pressure, no usage chunk). Locked #6's "degrades to zero" is superseded: truncation is non-deterministic-between-zero-and-full if unfixed, and eliminated by task 2.4. |
| Partial-release machinery (`race_coordinator.go:649-723`) superseded in live mode | The first-byte winner gate replaces deadline-driven best-buffer promotion: the winner is picked at the first forwardable chunk, long before any deadline could fire (any non-empty buffer wins within one 100ms tick) | Clients expecting "switch to the buffer with most content at deadline" get the first-byte winner instead — strictly earlier and content-driven | Pre-first-byte behavior in live mode: the deadline NEVER promotes a partial buffer (0-byte guard); the hard deadline (`MaxGenerationTime`) still caps. Buffered mode keeps the full partial-release machinery unchanged. Documented. |
| **Global `StreamDeadline` semantics redefined in live mode** (per L5 resolution) | The coordinator RUNS in live mode with the first-byte gate; the deadline's meaning becomes "no forwardable byte within StreamDeadline ⇒ terminal in-band SSE error + all attempts cancelled" (0-byte guard at `race_coordinator.go:661-722`; today it promotes a 0-byte winner and keeps streaming — wrong for live mode) | Clients expecting deadline-driven partial delivery lose it in live mode (partial buffers are empty by construction at the deadline); clients gain a bounded time-to-first-byte failure | Buffered mode keeps the deadline unchanged (today's behavior, incl. 0-byte promotion). Hard deadline (`MaxGenerationTime`) unchanged in both modes. Documented. |
| Loop detection + supervisor idle monitoring | Already unwired dead code | None — feature never shipped to production | No change needed. |
| Translation-only Anthropic variants temporarily fall back to buffering IF Phase 3's new translator is unavailable | Phase 3 is the biggest new component; if it doesn't ship, the fallback is buffered | Clients on Anthropic-translation paths stay in buffered mode until Phase 3 lands | **Fallback cost** bounded to the Phase 2 → Phase 3 window, per-variant gate softened (documented). Mitigate by landing Phase 3 immediately after Phase 2. |
| **Wire-shape change: Anthropic→OpenAI-wire internal translation emits `thinking_delta` blocks on the wire in live mode** (per Q2 §4 locked stance) | The new incremental translator emits thinking content blocks per `stream.go:176-186`; the internal-`flushingResponseRecorder` path's thinking-suppression (`internal_handler.go:379-396`) does not apply in live mode | Clients see Anthropic `thinking_delta` events on the wire that they do NOT see in buffered mode | Deliberate wire-shape change for live mode only. Documented. Parity tests assert byte-identity in buffered mode and explicit new-emission in live mode. |
| **Wire-shape change: live-mode block interleaving** (architect, 2026-08-27) | The incremental translator emits blocks in ARRIVAL order (single-open-block-of-each-kind); the batch translator always GROUPS thinking-before-text (`stream.go:176-197`) | Interleaved-thinking upstreams produce interleaved thinking/text blocks on the wire in live mode; buffered mode remains grouped. Real Anthropic streams interleave natively (passthrough precedent) | Deliberate, documented; pinned by `TestIncrementalStreamTranslator_ParityVsBatch` (event-SET equality; ORDER equality for non-interleaved; pinned divergence for interleaved). |
| H8 (no mid-stream flush) is dropped in live mode | Live mode's purpose IS mid-stream flushing | The buffered-mode H8 guarantee is lost only when header is absent | Header opt-in restores H8. Documented. |
| **Live-mode winner-buffer memory = full response** (architect, 2026-08-27) | Prune is gated off in live mode to preserve the estimator/bufferstore raw-byte inputs (locked "passive recorder" contract: the buffer still Add()s every chunk) | Live mode holds the full response in the winner's buffer — identical to today's buffered profile (no regression; live mode forgoes a memory win) | Documented tradeoff; revisit only if long-stream memory becomes a measured problem (a future plan could switch the estimator input instead). |
| Anthropic→Anthropic passthrough already real-streams; no change | n/a | None | n/a |

---

## 9. Explicitly EXCLUDED (from scope)

| # | Excluded item | Reason |
|---|---------------|--------|
| E1 | Changes to the `model_credential_selected` event asymmetry (Anthropic path does NOT publish, OpenAI race path is sole publisher) | Locked L7 — the investigation confirms the asymmetry is intentional and the Phase 3 LB feature depends on it. |
| E2 | `stream=false` behavior | Unaffected by header; both modes call `handleNonStreamResult` / `handleAnthropicNonStreamResponse` / ultimate non-stream paths identically. Header is a no-op for non-streaming requests. |
| E3 | `/v1/models` | No streaming behavior; not a target path. |
| E4 | MCP proxy | Out of scope per dispatch; the MCP proxy already has its own real-streaming implementation (per the project blueprint "Ultimate Model Raw Proxy" §Heartbeat). |
| E5 | Renaming the header | Locked L1. |
| E6 | New env var / config flag for the global default | Deferred — per-request header is the v1 contract. Rollback procedure documented in Phase 5 (env-var override on the load balancer side, not in this codebase). |
| E7 | Phase 3 internal-Anthropic path's `thinkingSink` removal | Locked — sink is preserved in BOTH modes. In live mode it's ADDITIONALLY propagated as Anthropic `thinking_delta` events on the wire. |
| E8 | Trigger schedule changes | Locked — separate feature (`feature/ultimate-model-trigger-schedule` shipped @ 6f34912). |
| E9 | **Changes to BUFFERED-mode coordinator behavior** | The Phase 2 predicate swap, deadline guard, Prune gating, and entry-capture gating are all LIVE-MODE branches; buffered mode executes today's code verbatim (parity suite proves it). (Supersedes the earlier "coordinator logic unchanged" text — the coordinator IS changed, live-mode-scoped.) |

---

## 10. Assumptions (Flagged for Caller Confirmation)

1. **Empty header value = buffered (LOCKED).** Per dispatch L1 (now integrated into L1 above),
   the header value semantics are: `"true" / "1" / "yes" / "on"` OR empty value ⇒ buffered;
   absent header OR `"false" / "0" / "no" / "off"` ⇒ real streaming. The `bypassInternal`
   precedent is NOT followed — empty header is an explicit opt-in to buffered mode. Phase 1
   task 1.1 implements this exactly.
2. **Q4 resolution: reuse `rc.streamingNonRetryable`, type-changed to `sync/atomic.Bool`.**
   Set inside the first successful relay write (under `rc.writeMu`); zero readers today; the
   atomic type makes any future reader race-free. `reset()` clears it via `Store(false)`.
3. **Phase 3 lands in the same PR window as Phase 2.** If Phase 3 slips, the
   `X-LLMProxy-Buffer-Response` opt-in restores current behavior on the Anthropic-translation
   paths, so the feature is still shippable. The fallback cost is the Anthropic-translation
   path staying buffered for the slip window — accepted, with the per-variant gate softened
   (Q2 table).
4. **The internal-Anthropic path (`doAnthropicInternalRequest`) is reachable today** (verified
   at `handler_anthropic.go:600-685`) and the recorder pattern is the correct seam. If a
   recent refactor changed this, Phase 3's wiring needs adjustment — verify at implementation
   time.
5. **The race coordinator's `streamCh` (`race_coordinator.go:833`) closes when a winner is
   picked** — under R1-variant via the same `onceStream` site (`:431`). The relay uses
   `WaitForWinner` exactly as today; only the gate predicate changes.
6. **`pkg/proxy/translator/stream.go:19` `TranslateBufferedStream` is the only batch
   translator for the Anthropic-translation paths.** Phase 3's new translator is the only
   streaming translator.
7. **Global `StreamDeadline` default value is `110*time.Second`** per
   `pkg/config/config.go:165`, configurable via env (`STREAM_DEADLINE`) per `config.go:376`.
   Read at runtime — never hardcode.
8. **The Anthropic-path sequential fallback loop at `handler_anthropic.go:256`** iterates
   `arc.modelList` and calls `attemptAnthropicModel` per model. **RESOLVED:** loop-top guard
   (`if arc.headersSent && !arc.bufferMode { break }`) immediately after the `baseCtx.Err()`
   check (`:261-264`). Pre-first-byte (loop iteration 0 with headers not yet sent) is
   unaffected.
9. **Prune-gating memory tradeoff (architect).** Live mode holds the full response in the
   winner's buffer because Prune is gated off (R2 fix). This equals today's buffered-mode
   memory profile — no regression. Accepted and documented (§8.2).
10. **`race_winner_selected` fires mid-stream in live mode** with a tiny `buffer_bytes`
    payload (`race_coordinator.go:423-429`). UI consumers must not assume completed buffers.
    Documented in Phase 5 docs.
11. **Gate implementation form (architect flag for implementation).** The resilience review's
    safety conclusions assume the predicate lands in the existing manage-loop ticker arm with
    `onceStream` close semantics (`race_coordinator.go:431`) and the deadline guard reuses
    the `:653-655` winner-nil check. If the implementer chooses a different form (e.g., a
    per-attempt atomic first-byte flag), re-verify the traced hazards
    (`architecture-recommendation.md` §3, §7 item 4).

    **Phase 5 / task 5.12 — tracked merge-time re-review:** see
    `.agents/approver/real-streaming-default-tracking.md` (entry appended
    in Phase 5) for the gate-form safety re-review AT MERGE TIME — confirm
    the Phase-2 pinned gate form on merged code (ticker arm `:406-435` +
    `onceStream` `:431` + winner-nil `:653-655`); confirm no later refactor
    changed the form.

---

## 11. Open Questions for Caller — RESOLVED (2026-08-27)

> All four were resolved by the leader + architect pass; recorded here for traceability.

1. **Q4 resolution: reuse `rc.streamingNonRetryable` or rename?** **RESOLVED: keep the name,
   type-change to `sync/atomic.Bool`** (Phase 1 lands the type; Phase 2 lands the set-site).
   Comment updated to "stream is live: no racing / fallback / mid-stream credential failover".
2. **Q2 option (b) architect confirm:** **RESOLVED: option (b) is a slip-fallback ONLY** — if
   Phase 3's translator slips, the two translation-only variants stay buffered in default mode
   with the per-variant gate softened and explicitly flagged (Q2 table). Option (a) is primary.
3. **Locked L5 default value:** **RESOLVED: YES** — the global `StreamDeadline` default of
   `110*time.Second` remains the active deadline in buffered mode, unchanged; in live mode it
   is redefined as the no-forwardable-byte timeout (L5 as resolved). No config schema changes.
4. **Locked L4 Anthropic-path fallback:** **RESOLVED: loop-top guard** at
   `handler_anthropic.go:256-264` (immediately after the `baseCtx.Err()` check).

---

## 12. Appendix — File:Line Citation Index (Verification Backlog)

| Claim | File:line |
|-------|-----------|
| `bypassInternal` parse precedent | `pkg/proxy/handler_functions.go:123` |
| `requestContext.streamingNonRetryable` declared but unset | `pkg/proxy/handler_helpers.go:99-102` |
| `requestContext.reset()` clears `streamingNonRetryable` | `pkg/proxy/handler_helpers.go:139` |
| Race coordinator's `streamCh` close-on-winner | `pkg/proxy/race_coordinator.go:833` |
| `onceStream` single-close guard | `pkg/proxy/race_coordinator.go:150, :431` |
| Winner-eligibility gate (predicate swap site) | `pkg/proxy/race_coordinator.go:406-435` |
| Live predicate atomic reads (`TotalLen` / `GetBuffer`) | `pkg/proxy/stream_buffer.go:305-313`, `pkg/proxy/race_request.go:190-194` |
| Manage-loop tick (100ms) | `pkg/proxy/race_coordinator.go:364` |
| `cancelAllExcept` after `c.mu` release | `pkg/proxy/race_coordinator.go:441-446` |
| Partial-release / deadline machinery (0-byte promotion; guard site) | `pkg/proxy/race_coordinator.go:649-723` (0-byte promote `:661-704`, error branch `:705-722`, winner-nil guard `:653-655`) |
| Deadline error surface (`GetStreamDeadlineError`) | `pkg/proxy/race_coordinator.go:941-946, :1008` |
| Executor chunk pipeline (normalizers → translator → toolcall → Add) | `pkg/proxy/race_executor.go:1501, :1532-1542, :1548-1552, :1573-1577` |
| Error-chunk Added before error check (hazard) | `pkg/proxy/race_executor.go:1573` vs `:1583-1585` |
| Internal-path role+content merge; thinking arm; done flush | `pkg/proxy/race_executor.go:704-719, :826-852, :856-928` |
| 429 arrives as HTTP status before stream bytes | `pkg/proxy/race_executor.go:532-543` |
| Cred-failover eligibility (`c.winner==nil` gate) | `pkg/proxy/race_coordinator.go:1275-1292` |
| `ExcludeAndReselect` spawn-branch site | `pkg/proxy/race_coordinator.go:1349-1354` |
| `streamResult` relay loop (unchanged live relay) | `pkg/proxy/handler.go:1038-1380` |
| Relay entry raw-capture (live-gated) | `pkg/proxy/handler.go:1053-1058` |
| Prune call sites (buffered-gated) | `pkg/proxy/handler.go:1082, :1122, :1167, :1238, :1357` |
| `ShouldPrune` false at buffered relay entry | `pkg/proxy/stream_buffer.go:294-303` |
| Prune nils prefix + cache invalidation | `pkg/proxy/stream_buffer.go:282-289` |
| `GetAllRawBytesOnce` rebuild skips nils | `pkg/proxy/stream_buffer.go:210-214` |
| Tokenizer-fallback estimator input (raw bytes) | `pkg/proxy/handler.go:1206-1235` (`:1220`), `pkg/proxy/token/prompts.go:57-114` |
| Post-winner SSE error envelope | `pkg/proxy/handler.go:1169-1178` |
| `NotifyCh` channel pattern (cap 1) | `pkg/proxy/stream_buffer.go:103, :145-149` |
| `notifyCh` signal survives buffer Close (pre-Add-then-Err edge) | `pkg/proxy/stream_buffer.go:103, :146` |
| `flushingResponseRecorder` (Flush no-op) | `pkg/proxy/handler_anthropic.go:591-598` |
| `thinkingSink` invariant | `pkg/proxy/internal_handler.go:32-41` |
| "case thinking" silent write | `pkg/proxy/internal_handler.go:379-396` |
| `SetThinkingSink` setter precedent | `pkg/proxy/internal_handler.go:109-115` |
| toolcall buffer `Flush` on done (Finalize ordering) | `pkg/proxy/internal_handler.go:438-440` |
| Anthropic→Anthropic passthrough (template; interleave parser) | `pkg/proxy/handler_anthropic.go:1225-1290` (parser `:1263-1278`) |
| Anthropic sequential fallback loop + guard site | `pkg/proxy/handler_anthropic.go:256-264` (headersSent sets `:808, :742, :1237`) |
| `TranslateBufferedStream` (batch) | `pkg/proxy/translator/stream.go:19` |
| `generateAnthropicEvents` (batch helper; grouping order) | `pkg/proxy/translator/stream.go:144-244` (`:176-197`) |
| Format helpers (reusable for new translator) | `pkg/proxy/translator/stream.go:251-340` |
| `ParseOpenAISSEChunk` (framing for ProcessChunk input) | `pkg/proxy/translator/stream.go:346-366` |
| Batch translator tolerates malformed chunks / missing id+name | `pkg/proxy/translator/stream.go:201-202` |
| Batch emits `message_delta` before `message_stop`; zero-usage default | `pkg/proxy/translator/stream.go:226-241` |
| Anthropic internal-call site (`doAnthropicInternalRequest`) | `pkg/proxy/handler_anthropic.go:600-685` |
| Anthropic internal-stream translation site | `pkg/proxy/handler_anthropic.go:720-760` |
| Stream-end-without-`[DONE]` error path | `pkg/proxy/handler_anthropic.go:880-886` |
| Ultimate external stream single-write (H8) | `pkg/ultimatemodel/handler_external.go:438-500` |
| Ultimate external end-of-stream `w.Write+flusher.Flush` | `pkg/ultimatemodel/handler_external.go:499-500` |
| Ultimate internal stream event loop | `pkg/ultimatemodel/handler_internal.go:484-755` |
| Ultimate internal `case "done"` `w.Write+flusher.Flush` | `pkg/ultimatemodel/handler_internal.go:727-728` |
| Ultimate internal `case "error"` `w.Write+flusher.Flush` | `pkg/ultimatemodel/handler_internal.go:748-749` |
| `model_credential_selected` publisher | `pkg/proxy/race_coordinator.go:791-803` |
| `model_credential_selected` trigger | `pkg/proxy/race_executor.go:124-135` |
| Anthropic-path asymmetry (no publish) | `pkg/proxy/internal_handler.go:146-256` |
| `ReleaseStreamChunkDeadline` field (dormant) | `pkg/models/config.go:123-150` |
| `ReleaseStreamChunkDeadline` getter | `pkg/models/config.go:143-151` |
| Tool-call buffer per-call JSON-completion hold | `pkg/toolcall/buffer.go:219-222` |
| `extractStreamChunkContent` (inline content tap) | `pkg/proxy/handler_helpers.go` (referenced from `handler.go:1073, :1113, :1139`) |
| `saveRawResponse` (bufferstore) | `pkg/proxy/handler.go:1159-1186` (`:190-230`) |
| Heartbeat keepalive | `pkg/proxy/heartbeat.go:2` (`writeMu` write `:63, :74-77`) |
| Usage metering final-chunk tap | `pkg/proxy/handler.go:1189-1204` |
| `rc.writeMu` shared between heartbeat and relay | `pkg/proxy/handler_helpers.go:84-86` |
| `NewRaceCoordinator` engine+conversationKey params (gate flag threading) | `pkg/proxy/handler.go:924` |
| `coordinator.streamCh` close site | `pkg/proxy/race_coordinator.go:693` |

---

> **End of plan.** Cross-reference `technical-analysis.md` (sibling investigation) and
> `architecture-recommendation.md` (architect resolution mechanics — the authority on the
> resolved items). This plan complies with the locked decisions and the leader's resolutions.
