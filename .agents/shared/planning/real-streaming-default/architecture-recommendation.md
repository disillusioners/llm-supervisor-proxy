# Architecture Recommendation: real-streaming-default — Resolved-Decision Mechanics

Date: 2026-08-27
Branch: `feature/real-streaming-default` @ 9842c77
Architect: architect (controller) — analysis dispatched to 3 skill-equipped workers:
- Worker A `architect-worker-r1-dataflow` (`data-flow-design`) — R1-variant gate mechanics + R2 verification
- Worker B `architect-worker-translator-structural` (`structural-design`) — incremental translator design
- Worker C `architect-worker-concurrency-resilience` (`resilience-design`) — concurrency/deadlock + failure modes

Status: **Complete** — all leader resolutions deepened into file:line-cited mechanics; plan.md amended inline (amendment list in §6).
Confidence: **High** overall (evidence-cited against `9842c77`); **Medium** on the three items flagged in §7.

---

## 0. Scope of This Document

The leader resolved R1/R2/Q1–Q4. Direction is LOCKED — this document deepens **mechanics only** and records the exact plan.md amendments (applied directly; see §6). Nothing here relitigates direction.

Headline findings that CHANGED plan text:

| # | Finding | Verdict |
|---|---------|---------|
| F1 | StreamDeadline in live mode: today's `handleStreamingDeadline` **promotes a 0-byte winner and keeps streaming** (`race_coordinator.go:661-704`, "even with 0 bytes" at `:674`) | Leader's expected semantics ("no forwardable byte in time → hard failure / in-band error") are **correct as spec** but require a **new 0-byte guard**, not reuse of existing machinery |
| F2 | R2: the tokenizer-fallback usage estimator consumes **raw buffer bytes** (`GetAllRawBytesOnce`, `handler.go:1220`), NOT `rc.accumulatedResponse`; mid-stream `Prune` (relay call sites `handler.go:1081/:1121/:1356`) non-deterministically truncates it in live mode | **Neither prior claim is right.** Plan §8.2 "no degradation" describes the best case; locked #6 "degrades to zero" the worst. Fix: gate Prune to buffered mode (§1.2) |
| F3 | Phase 2 shape: no `streamLiveRelay` is needed — `WaitForWinner` returning at the first-byte winner + the **existing unchanged `streamResult` body IS the live relay** | Phase 2 is a **gate redefinition** (3 files + tests), smaller than the plan's bypass framing |
| F4 | External error chunks are `Add`-ed **before** the error check (`race_executor.go:1573` vs `:1583`) | A dying attempt could transiently win the new predicate — requires a data-flow reorder fix (§1.1) |

---

## 1. Resolved Decisions — Mechanics (file:line-cited)

### 1.1 R1 → First-Forwardable-Byte-Wins (winner-gate redefinition)

#### 1.1.1 Exact winner predicate

**Predicate (live mode, `isStream==true` only):**

```go
// race_coordinator.go manage-loop winner-eligibility block (:406-435), live branch:
// replaces req.IsCompleted() && req.GetError()==nil   (:409)
req.GetError() == nil && req.GetBuffer().TotalLen() > 0
```

- `TotalLen()` is an atomic read (`stream_buffer.go:305-313`); `GetBuffer()` is lock-guarded (`race_request.go:190-194`) — the same access pattern the manage loop already uses at `race_coordinator.go:418, :666`.
- **Definition**: "first forwardable data chunk" = **the attempt's `streamBuffer` transitions from 0 chunks to ≥1 chunk**. Chunk path verified: upstream line → normalizers (`race_executor.go:1501`) → MiniMax StreamTranslator (`:1532-1542`) → `toolCallBuffer.ProcessChunk` (`:1548-1552`) → `buf.Add` (`:1573-1577`). Toolcall buffering sits strictly **before** Add on both paths (external `:1548-1577`, internal `:697-928`), so every Add-ed chunk is post-buffering — i.e., genuinely forwardable.
- **Role-only first chunks: COUNT as forwardable (ruled).** Today's relay forwards ALL buffer chunks including role-only ones (`handler.go:1063-1076`); the internal path merges role+content into one chunk (`race_executor.go:704-719`), so role-only firsts arise only on external passthrough. Committing on them preserves byte-identical chunk ORDER vs buffered mode. The acceptance gate ("first content byte on client wire before upstream completion") is satisfied by construction: the role-only chunk forwards immediately and every subsequent content chunk forwards on `notifyCh` (signaled per Add, `stream_buffer.go:145-149`).
- **Preamble/heartbeat exclusion: confirmed.** `: connected\n\n` is written directly to `w` pre-coordinator (`handler.go:886-889`); heartbeats write `: heartbeat\n\n` directly under `writeMu` (`heartbeat.go:63, :74-77`). Neither ever enters a streamBuffer — neither can trigger the predicate.
- **Usage-only / finish_reason-only / comment chunks: COUNT.** They are real data events the relay forwards today; the predicate commits on "upstream alive and streaming." External upstream comment lines (`: ping`) also enter the buffer (`len(line)>0` guard only, `race_executor.go:1487`) — commit-on-comment is consistent with commit-on-alive.
- **Evaluation site: the existing manage-loop eligibility block on the existing 100ms tick** (`race_coordinator.go:364, :406-435`). Everything downstream is reused verbatim: index preference (`:412`), `race_winner_selected` event (`:423-429`), `onceStream` close (`:431`), cancel-and-exit (`:440-446`). **No new channels, no executor changes, no relay changes.**
- **Worst-case +100ms first-byte latency** (tick granularity) — accepted; irrelevant to the acceptance gate.
- `liveMode` reaches the coordinator via the constructor (`newRaceCoordinatorWithEvents`; call site `handler.go:924`) as `liveFirstByteGate = !rc.bufferMode && rc.isStream`. **Must be isStream-scoped**: non-stream Adds one body chunk (`race_executor.go:627, :1201`) and keeps the `IsCompleted` gate (`handler.go:962` waits `buffer.Done()`).

#### 1.1.2 Predicate hazard (🔴 fixed in Phase 2)

External **error chunks are Added BEFORE the error check** (`race_executor.go:1573` Add-loop vs `:1583-1585` `isStreamErrorChunk`). A dying attempt can transiently have `TotalLen>0`, win the tick race, and preempt fallback — forwarding an error-chunk byte then the envelope. **Fix**: move the `isStreamErrorChunk` check **before** the Add loop. Behavior-neutral for buffered mode (failed buffers are never read there; only the on-error raw dump content at `handler.go:1159-1164` changes).

#### 1.1.3 Mid-stream `cancelAllExcept` ordering (data-flow)

Sequence: winner's first Add → (≤100ms) tick → `c.winner` set + `streamCh` closed (`race_coordinator.go:431`) → `cancelAllExcept(winner)` (`:442`) → losers `Cancel()` (`race_request.go:234-250`): cancel ctx → drain+close resp.Body → `buffer.Close(context.Canceled)` + `r.buffer=nil` (`:261-273`); late Adds return false (`stream_buffer.go:118-120`) or land in the executor's orphaned buffer snapshot (`race_executor.go:642, :1397`).

**No loser byte can reach the client**: the relay reads only `winner.GetBuffer()` (`handler.go:1039`), and no other component writes loser buffers to `w`. The winner is NOT cancelled (its cancel is the existing defer at `handler.go:938-942`); it keeps streaming; the relay's entry drain (`:1062-1083`) then `NotifyCh` loop (`:1101-1123`) forward chunks as they arrive.

Concurrency safety (Worker C): `c.mu` is released at `race_coordinator.go:441` **before** `cancelAllExcept` — mirroring today; lock chain `c.mu → r.mu → sb.mu` never inverts; the relay holds an RLock-protected buffer snapshot (`race_request.go:190-194`) that stays valid after Close per the 9842c77 SIGSEGV fix. Full worst-case interleaving (loser mid-Add + mid-stream cancel + client disconnect) traced safe — see §3.

#### 1.1.4 StreamDeadline in live mode

- **Post-winner: deadline cannot interfere.** The manage loop RETURNS after winner selection (`race_coordinator.go:445`) and `defer streamDeadlineTimer.Stop()` (`:371`) disarms it; belt-and-braces `c.winner != nil` guard at `:653-655`. Concurrent timer+gate fire is safe (Go select randomness + `onceStream` at `:150` + both paths serialize on `c.mu` at `:399/:650`).
- **Pre-winner with no forwardable byte — the gap (F1):** `handleStreamingDeadline` picks the best among non-Done requests with `bestLen=-1` (`:661-672`) and **today promotes a 0-byte winner and keeps streaming** (`:674`; winner not cancelled `:695-704`); the no-content failure branch (`:705-722`) fires only when ALL requests are Done. In live mode this would hang the client on headers+heartbeats until the hard deadline — **contradicting the leader's spec**.
- **Confirmed spec + required guard:** in live mode, if the deadline-best has `TotalLen()==0` → take the `:705-722` branch (set `streamDeadlineError` `:712`, close done+streamCh `:720-721`) **and add an explicit `cancelAll()`** (that branch today cancels nothing; teardown otherwise relies on handler-return → baseCtx).
- **Surfacing:** `WaitForWinner` (`:831-839`) returns nil → `handler.go:967-979` → `handleRaceFailure("all_models_failed")` → `GetStreamDeadlineError()` non-nil (`:1008`, accessor `:941-946`) → `rc.headersSent==true` → in-band `sendSSEError` (`:1028-1029`). No HTTP status change — envelope shape identical to `handler.go:1169-1178`.
- **Rationale for the guard:** any non-empty buffer would have won within one 100ms tick long before 110s, so at deadline all buffers are empty by construction (the guard honors data if the same-instant race delivers bytes).
- **Hard deadline (`MaxGenerationTime`, `:376, :393-397`) unchanged** — cancels ALL including the winner (`:739-743`); the relay's Done branch sees `buffer.Err()` → same in-band envelope (`handler.go:1149-1178`). Absolute cap preserved in live mode.

**Pinned spec sentence:** *StreamDeadline (live mode) = "no forwardable byte within StreamDeadline ⇒ terminal in-band SSE error envelope + all attempts cancelled". Requires the 0-byte guard in `handleStreamingDeadline` — it is NOT today's behavior.*

#### 1.1.5 `headersSent` / `: connected` preamble ordering

Headers + `WriteHeader(200)` + `rc.headersSent=true` + `: connected\n\n` + flush + heartbeat start all happen at `handler.go:870-903`, BEFORE coordinator construction (`:924`) and `Start()` (`:925`). Mode dispatch happens later ⇒ **identical in both modes; R1-variant changes nothing here.** The only other pre-winner client writes are heartbeat comments (`heartbeat.go:63`). (`rc.streamBuffer` at `handler_helpers.go:76/:135` is declared/reset but never used in the race path — untouched.)

#### 1.1.6 Credential LB

- `GetOrSelect` fires inside `executeInternalRequest` per attempt, pre-stream; the `model_credential_selected` publish closure is wired in `c.execute` (`race_coordinator.go:784-802`) and fired at the single source site after resolution (`race_executor.go:124-135`) — **unchanged**.
- Failover eligibility requires latest attempt Done+error+rate-limit AND `c.winner==nil` (`race_coordinator.go:1275-1292`), adjudicated in the spawn branch (`:476-526`); `ExcludeAndReselect` runs only there (`:1349-1354`) — **mid-stream failover remains unavailable, as today**.
- A 429 arrives as HTTP status BEFORE any stream byte (`race_executor.go:532-543`) ⇒ 429ing attempts have empty buffers ⇒ cannot fire the predicate ⇒ **the pre-first-byte failover window is genuinely unchanged** (a non-nil winner — first byte seen — closes it, exactly locked decision #2).

### 1.2 R2 → Usage-Estimator Reconciliation (verdict: both prior claims wrong; fix = Prune gating)

**Code truth** (`handler.go:1206-1235`): the fallback estimator consumes `winner.GetBuffer().GetAllRawBytesOnce()` (`:1220`) → `token.ExtractCompletionTextFromChunks` (`token/prompts.go:57-114`) — **raw buffer bytes, NOT `rc.accumulatedResponse.String()`** (that builder feeds only the assistant log message, `:1279`).

- **Buffered mode today**: relay starts post-completion and reads all chunks at entry (`:1063`); `readIndex==len` ⇒ `ShouldPrune` false (`stream_buffer.go:294-303`) ⇒ Prune **never fires** ⇒ full bytes at `:1220`.
- **Live mode (if unmitigated)**: relay runs concurrently with Adds; `ShouldPrune` fires whenever Adds land during the write loop (`handler.go:1081, :1121, :1356`) ⇒ Prune nils the prefix and invalidates the cache (`stream_buffer.go:282-289`) ⇒ `GetAllRawBytesOnce` rebuild skips nils (`:210-214`) ⇒ **non-deterministically truncated completion text — worst case ≈0, best case full**.

**Fix (Phase 2 task):** gate the three Prune call sites (`handler.go:1081, :1121, :1356`) to buffered mode — skip Prune when `!rc.bufferMode`. Justification:

1. Prune is **already effectively dead in buffered mode** (never fires — see above), so gating it in live mode preserves the effective behavior of BOTH modes and upholds the locked "buffer degrades to passive recorder (still Add()s every chunk)" contract.
2. It fixes **two consumers in one switch**: the estimator (`:1220`) and the bufferstore Done-capture (`:1182-1187`).
3. Memory cost: live mode holds the full response in the winner's buffer — **identical to today's buffered profile** (no regression; live mode simply forgoes a memory win).

**Additional bufferstore fix:** the ENTRY raw-capture (`handler.go:1053-1058`) saves a partial prefix in live mode and double-saves with the Done capture (`:1182-1187`). Gate the entry capture to buffered mode (preserving today's double-save behavior there — parity), making the Done capture authoritative in live mode. `bufferStore.Save` overwrite semantics unverified — developer confirms at implementation (§7).

**Corrected plan text (§8.2 row):** the estimator mechanism sentence is replaced with the `GetAllRawBytesOnce` truth + the Prune-gating fix; locked #6's "degrades to zero" is replaced by "non-deterministic truncation **if Prune is left enabled**; with Prune gated to buffered mode, **no degradation** holds by construction."

### 1.3 Q1 → Single Relay Path (no new orchestration)

**`streamLiveRelay` is ELIMINATED.** `WaitForWinner` returning at the first-byte winner + the existing `streamResult` body (`handler.go:1038-1380`) **IS the live relay** — entry drain of already-buffered chunks (`:1062-1083`) then the `NotifyCh` loop (`:1101-1123`), per-chunk `writeMu` writes, tap, heartbeat interleave, error envelope — all unchanged. Phase 2's file list shrinks to: `race_coordinator.go` (gate + deadline guard), `race_executor.go` (error-chunk reorder), `handler.go` (gate flag + Prune/entry-capture gating + first-write flag), `handler_helpers.go` (flag type). Tasks 2.1/2.2 in the old plan collapse. Full updated Phase 2 design: §2.

### 1.4 Q2 → Incremental Streaming Translator (primary; mechanics)

State machine (Worker B, structural-design). Full detail in Worker B's report; mechanics pinned here:

- **API**: `pkg/proxy/translator/incremental_stream.go` — `NewIncrementalStreamTranslator(originalModel string)`; `ProcessChunk(rawOpenAIChunk []byte) ([]string, error)` (caller does SSE framing via `ParseOpenAISSEChunk`, `stream.go:346-366`); `Finalize() ([]string, error)`; typed-event entry `ProcessEvent(ev)` for the internal variant.
- **States**: NotStarted → MessageStarted → {TextBlockOpen, ThinkingBlockOpen, ToolBlockOpen} interleave → Finalizing → Done. Guards: `messageStartSent && pingSent` before any `content_block_*`; open-block before its deltas; `message_delta` before `message_stop`.
- **First chunk**: `message_start` + `ping` verbatim from `stream.go:148-169`, emitted **once** (flags gate double-emission) — even for empty streams (batch parity, `:148-169` unconditional).
- **Interleave ruling — single-open-block-of-each-kind**: at most one open `text` and one open `thinking` block; deltas route by kind without close-and-reopen. Real Anthropic streams interleave thinking/text (passthrough parser switches on both delta types, `handler_anthropic.go:1263-1278`); batch's grouped order (`:176-197`) is an artifact of accumulation, not a wire requirement. **Documented wire difference live-vs-buffered for interleaved upstreams** — pinned by a run-both-translators parity test (event-SET equality; ORDER equality for non-interleaved fixtures; ORDER inequality pinned for interleaved).
- **Tool calls**: per-index tracking keyed by OpenAI `tool_calls[i].index`; `content_block_start` on first delta carrying id+name; `input_json_delta` per fragment (accumulate `partial_json`, mirror `stream.go:135-137`); **`content_block_stop` deferred to `Finalize()`** (differs from batch's per-tool close at `:214` — documented; SDK accepts blocks-close-at-message-end). Arguments-before-name edge: buffer fragments internally until id+name arrive (mirrors batch skip `:201-202`).
- **Usage + `message_delta`**: emitted ONCE in `Finalize()`, before `message_stop`, carrying stop_reason + usage (`:226-235` shape); **always emitted, with zero usage when absent** (batch parity).
- **Stream-end-without-`[DONE]`**: `Finalize()` ALWAYS emits `message_stop`; then the caller writes `sendAnthropicSSEError` (`handler_anthropic.go:880-886`) — sequence: block-stops → `message_delta` → `message_stop` → error event.
- **thinkingSink (internal variant)**: preferred integration is the **typed-event entry** (`ProcessEvent`) called from the `case "thinking"` arm (`internal_handler.go:393-394`) BESIDE the unchanged sink write; translator installed via a setter mirroring `SetThinkingSink` (`internal_handler.go:109-115`); constructed in `doAnthropicInternalRequest` (`handler_anthropic.go:600-685`) when live. Buffered mode byte-for-byte unchanged (recorder + `TranslateBufferedStream` path untouched, `:612/:728`).
- **toolcall-buffer ordering constraint**: the buffer's `Flush()` (`internal_handler.go:438-440`, on `case "done"`) emits final tool chunks AFTER done — **`Finalize()` must run after `toolCallBuffer.Flush()` completes** (same site where `[DONE]` is written today). Test required.
- **Sequential fallback loop break**: **loop-top guard** in `handler_anthropic.go:256`, immediately after the `baseCtx.Err()` check (`:261-264`): `if arc.headersSent && !arc.bufferMode { break }`. Loop-top (not inside `attemptAnthropicModel`) avoids wasted per-model setup (`:275-299`) on iterations that can never switch. `headersSent` set-sites: `:808`, `:742`, `:1237`, + new live entry point. Pre-first-byte fallback (iteration 0, headers not sent) unchanged.
- **Reuse inventory**: `StreamState`/`ToolCallState`/event constants (`types.go:236-252, :114-120`) as-is; ALL format helpers (`stream.go:251-340`) as-is; `generateAnthropicMessageID`/`mapFinishReason`/`translateUsage` (`response.go:68-111`) as-is; `extractChunkContent` body refactored into `accumulateChunk(chunk, state)` shared by batch and incremental (≤5 LOC, no behavior change); `generateAnthropicEvents` NOT reused (batch-only). New file ≈250 LOC + ≈350 LOC tests.
- **Buffer-translation fallback (option b)**: retained ONLY as explicitly-flagged slip risk per variant, per the locked stance and the per-variant acceptance-gate table (plan.md:185-192, unchanged).

### 1.5 Q3 → Header Wins

- Buffered mode = **byte-for-byte current behavior**: the coordinator predicate swap and deadline guard are live-mode-only branches; buffered mode executes today's `IsCompleted` gate and today's deadline machinery verbatim.
- Live mode = coordinator runs with the first-byte gate; StreamDeadline = no-forwardable-byte timeout (§1.1.4). Per-model `ReleaseStreamChunkDeadline` stays dormant (zero call sites — planner-verified).
- Preamble/`headersSent` ordering identical in both modes (§1.1.5).

### 1.6 Q4 → `rc.streamingNonRetryable` (reuse + atomic refinement)

- **Set site**: relay's first successful client write, inside the existing `rc.writeMu` critical section (`handler.go:1064-1080`) — exactly the leader's resolution.
- **Refinement (recommended, one-line)**: change the field type to `sync/atomic.Bool` (`handler_helpers.go:102`; reset `:139` becomes `Store(false)`). Rationale: **zero readers exist today** (full-tree grep); `writeMu` is over-scoped for any future reader (heartbeat also takes it); `atomic.Bool` provides a happens-before edge independent of the writer's lock. The leader's "set under `rc.writeMu` on first successful client write" is still honored — the `Store(true)` happens inside that critical section; the atomic type simply makes any future `Load()` race-free by construction.
- Functional effect under R1-variant: none on current code paths (the gate + `c.winner==nil` checks already close the failover window); the flag formalizes the boundary for future readers. Phase 1 truth table unchanged.

---

## 2. Updated Phase 2 Design (gate redefinition, not bypass)

**Objective.** When `rc.bufferMode==false && rc.isStream==true`, select the race winner at the **first forwardable chunk** (`TotalLen()>0 && err==nil`), cancel losers mid-stream, and relay the winner live via the **existing unchanged `streamResult` loop**. Pre-first-byte racing/fallback/credential-failover unchanged; post-first-byte failure = today's terminal in-band SSE envelope (`handler.go:1169-1178`). In live mode, StreamDeadline = no-forwardable-byte timeout (§1.1.4); Prune and the entry raw-capture are gated to buffered mode (§1.2).

**Files.**

| # | File | Action | What |
|---|------|--------|------|
| 1 | `pkg/proxy/race_coordinator.go` | MODIFY | `:406-435` — live-mode predicate swap behind `liveFirstByteGate` (everything downstream reused). Constructor `~:140-215` + call site — thread `liveFirstByteGate = !bufferMode && isStream`. `:661-722` — `handleStreamingDeadline` live 0-byte guard → `:705-722` error branch + explicit `cancelAll()`. |
| 2 | `pkg/proxy/race_executor.go` | MODIFY | `:1573-1585` — move `isStreamErrorChunk` check BEFORE the Add loop (predicate hazard, §1.1.2). |
| 3 | `pkg/proxy/handler.go` | MODIFY | `:924` — pass the gate flag. `:1081/:1121/:1356` — gate Prune call sites to buffered mode. `:1053-1058` — gate entry raw-capture to buffered mode. Relay first-write `streamingNonRetryable.Store(true)` (~`:1064-1080`). `streamResult` otherwise **unchanged**. |
| 4 | `pkg/proxy/handler_helpers.go` | MODIFY | `:99-102` — `streamingNonRetryable atomic.Bool` + comment update (Q4); `:139` — `Store(false)`. |
| 5 | `pkg/proxy/race_coordinator_test.go` | MODIFY | NEW gate tests + deadline-guard tests (this file IS affected — superseding the old plan's claim). |
| 6 | `pkg/proxy/handler_test.go`, `handler_integration_test.go` | MODIFY | Live-relay tests per §5 test deltas. |

**Tasks.**

| # | Task | Acceptance | Design Notes |
|---|------|------------|--------------|
| 2.1 | Coordinator gate swap | Live mode + isStream: winner = first `TotalLen()>0 && err==nil` attempt; buffered/non-stream: `IsCompleted` gate unchanged. | Predicate + site per §1.1.1; `race_winner_selected` fires with the tiny-buffer payload (UI note, §4 R13). |
| 2.2 | Deadline 0-byte guard | Live mode + deadline + all buffers empty ⇒ in-band SSE envelope (`GetStreamDeadlineError` path), all attempts cancelled. Byte-at-boundary honored. | Guard + `cancelAll()` per §1.1.4; buffered deadline behavior byte-identical. |
| 2.3 | Executor error-chunk reorder | A stream whose only chunk is an error chunk does NOT win the predicate; fallback proceeds. | §1.1.2; behavior-neutral in buffered mode. |
| 2.4 | Prune + entry-capture gating (R2 fix) | Live mode: `GetAllRawBytesOnce` at `buffer.Done()` returns the FULL stream; bufferstore Done capture complete; no partial entry capture. Buffered mode: today's behavior (incl. double-save) unchanged. | §1.2; memory tradeoff documented (§4 R17). |
| 2.5 | Flag set on first write | `streamingNonRetryable` set exactly once per request, inside the first successful relay write; reset verified. | §1.6; atomic.Bool. |
| 2.6 | Tests | §5 delta list green; `go test -race ./pkg/proxy/...` green. | Merged Worker A/C test tables. |

**Exit criteria.** (1) First-byte timing tests pass (winner selected at first forwardable chunk; relayed before upstream `[DONE]`). (2) Mid-stream loser cancellation tests pass (`-race`). (3) Live-mode deadline test passes (0 bytes ⇒ envelope, no hang). (4) R2 tests pass (non-zero completion tokens under prune pressure; complete bufferstore dump). (5) Buffered-mode parity: existing `race_coordinator_test.go` + `race_retry_test.go` deadline-path tests pass unchanged **with the header set** (see §5 flip note). (6) `TestLiveRelay_NoCredFailoverAfterFirstByte` passes; pre-first-byte 429 failover unchanged.

---

## 3. Concurrency & SIGSEGV-Fix Interaction Verdict (Worker C)

- **Lock map**: handler relay (`rc.writeMu` per chunk — `handler.go:1064/:1104/:1127/:1337`); coordinator manage (`c.mu` write `:399-446`, **released `:441` before `cancelAllExcept`**); executors (`sb.mu` only); heartbeat (`rc.writeMu`, `heartbeat.go:74-77/:113-114`); disconnect watcher (inside streamResult). **No new lock-order inversion**; relay's buffer snapshot (`race_request.go:190-194`) stays valid after Close per 9842c77.
- **9842c77 interaction: COVERED.** Worst-case interleaving traced: loser mid-Add (`sb.mu` held) + manage sets winner + `cancelAllExcept` → loser `Cancel` → `cleanup` → `sb.Close` blocks on `sb.mu` + client disconnect closes `clientGoneCh` + relay returns + late Add completes + Close lands (`ctx.Canceled`). Relay never touches loser buffers. Idle-termination `winner.Cancel()` (`handler.go:1373`) → cleanup → `r.buffer=nil` — relay snapshot safe, `:1375` returns. All steps safe.
- **Pre-Add-then-Err winner edge**: gate-Add's `notifyCh` signal (cap 1, `stream_buffer.go:103`) persists; relay reads the 1 chunk, then `buffer.Done()` → `buffer.Err()` → envelope. Client sees `[chunk] + [envelope]` — **well-formed**.
- **Heartbeat**: start `handler.go:902`, stop `:906-910`/`:989`; no change under R1-variant; pre-winner window shrinks ⇒ contention decreases.
- **Late deadline after winner**: safe — `defer streamDeadlineTimer.Stop()` (`:371`) + `c.winner != nil` guard (`:653`); concurrent fire resolved by `onceStream` (`:150`) + `c.mu` serialization; no double-winner.

---

## 4. Updated Risk Register (merged A+B+C, deduped, severity-ordered)

| # | Sev | Risk | Mitigation |
|---|-----|------|-----------|
| R1 | 🔴 | **Live-mode estimator/bufferstore truncation via mid-stream Prune** (`handler.go:1220/:1184` × `stream_buffer.go:282-289`) — non-deterministic usage undercount + truncated debug dumps | Phase 2 task 2.4 (Prune gating); test asserts non-zero completion tokens under prune pressure + full bufferstore dump |
| R2 | 🔴 | **Translator ordering drift vs `generateAnthropicEvents`** — live interleaves thinking/text blocks; batch groups (`stream.go:176-197`) | Documented wire change; run-both-translators parity test pins SET equality + ORDER for non-interleaved fixtures + pinned divergence for interleaved |
| R3 | 🔴 | **thinkingSink invariant break on internal live path** (thinking bytes to `w` outside the translator seam) | Typed-event entry (`ProcessEvent`); sink code in `case "thinking"` unchanged (`internal_handler.go:393-395`); dedicated test asserts wire `thinking_delta` AND recorder zero-thinking AND sink populated |
| R4 | 🟡 | **0-byte winner promotion at deadline hangs live clients** until hard deadline (today's `:661-704` behavior) | Phase 2 task 2.2 guard + `cancelAll()`; dedicated test |
| R5 | 🟡 | **Error-chunk-Added-before-error-return wins the predicate** and preempts fallback (`race_executor.go:1573` vs `:1583`) | Phase 2 task 2.3 reorder; test: error-chunk-only stream must NOT win |
| R6 | 🟡 | **Winner-validity**: first-byte winner may later fail; a loser might have succeeded. Client sees partial bytes + terminal envelope, no retry | Leader-accepted; documented in `docs/real-streaming-default.md`; envelope well-formed per §3 trace |
| R7 | 🟡 | **Loser late-Add post-cancel silently dropped** (manage never observed; gate didn't fire for them) | Idempotent by design (`Add` returns false on closed buffer, `stream_buffer.go:119`); test `TestLiveRelay_LoserAddAfterCancel` |
| R8 | 🟡 | **`streamingNonRetryable` future-reader data race** | `atomic.Bool` type change (§1.6); `-race` test with concurrent Load/Store |
| R9 | 🟡 | **+100ms worst-case first-byte latency** (tick-based predicate, `race_coordinator.go:364`) | Accepted; irrelevant to acceptance gate |
| R10 | 🟡 | **Deferred `content_block_stop` differs from batch** (batch closes per-tool at `:214`; incremental closes at `Finalize`) | Documented; SDK accepts blocks-close-at-message-end; ordering test |
| R11 | 🟡 | **toolcall-buffer `Flush()` emits final tool chunks AFTER `case "done"`** (`internal_handler.go:438-440`) — translator `Finalize()` ordering | Constraint pinned: `Finalize()` runs after `buffer.Flush()` at the done site; test |
| R12 | 🟡 | **Anthropic SDK tolerance for interleaved thinking/text blocks** — assumption based on the passthrough parser (`handler_anthropic.go:1263-1278`), not SDK inspection | Verify with a recorded real-Anthropic stream at Phase 3; option (b) fallback documented if violated |
| R13 | 🟢 | `race_winner_selected` now fires mid-stream with tiny `buffer_bytes` (`:423-429`) | UI consumers must not assume completed buffers; note in docs |
| R14 | 🟢 | Late-deadline-after-winner | Verified safe (§3) |
| R15 | 🟢 | Heartbeat-vs-relay `writeMu` contention | Decreases in live mode; existing 3s cap fine |
| R16 | 🟢 | credFailover-spawn vs gate race in same tick | Gate wins by design (`:406` short-circuits via `c.winner != nil` at `:462`); test |
| R17 | 🟢 | **Live-mode buffer memory = full response** (Prune gated off) | Identical to today's buffered profile — no regression; documented tradeoff |
| R18 | 🟢 | Pre-Add-then-Err winner edge | Well-formed wire (§3); test |

Racing cost pre-first-byte: **unchanged** (the coordinator races exactly as today until the predicate fires; 429-failover window preserved, §1.1.6).

---

## 5. Test-Strategy Deltas (merged)

**First-byte acceptance (redefined)**: winner selected at **first forwardable chunk**, relayed **before upstream `[DONE]`** — mock upstream sends chunk 1 then blocks; assert client receives it before completion (L8 gate). Role-only-first-chunk and thinking-first streams trigger winner + forward in order; two attempts ⇒ first-buffered-bytes wins, not first-completed; error-chunk-only stream must NOT win.

**Parity**: buffered-mode byte-identity unchanged (golden matrix). Existing deadline-path tests (`race_retry_test.go` sets `StreamDeadline: 5s`) and post-winner-failover tests **opt into buffered mode via the header** (or run-mode split) — these exercise `IsCompleted`-gate/deadline semantics that live mode redefines.

**New — race gate/deadline (Phase 2)**: mid-stream loser cancellation (loser mid-Add at cancel; `-race`); StreamDeadline-in-live-mode (0 bytes ⇒ in-band envelope, no 0-byte stream; byte-at-boundary honored); late-deadline-after-winner (no double-winner); client-disconnect-during-mid-stream-cancel (no panic, goroutine cleanup); first-byte-then-immediate-err (chunk + envelope); idle-termination mid-stream; no-cred-failover-after-first-byte (429 after chunk ⇒ envelope, no second credential); pre-first-byte 429 failover unchanged; atomic flag under `-race`.

**New — R2 (Phase 2)**: live-mode long stream with prune pressure + no usage chunk ⇒ non-zero completion tokens (fails until task 2.4 lands); bufferstore capture == full stream; no partial entry capture in live mode.

**New — translator (Phase 3, from Worker B)**: message_start-once; deferred-close ordering (all block-stops before message_stop); finalize-without-`[DONE]` emits message_stop; tool-call arguments-before-name; usage injection (zero-usage default); run-both-translators parity (§4 R2); thinking-sink triple assertion (§4 R3); huge-chunk (1MB scanner cap parity, `handler_anthropic.go:821`); `Finalize`-after-toolcall-`Flush` ordering.

---

## 6. Plan Amendments (APPLIED DIRECTLY to plan.md — list for review)

| # | Location (original line) | Amendment |
|---|--------------------------|-----------|
| A1 | after :19 | New addendum block: "Architect resolution integrated (2026-08-27)" — R1/R2/Q1–Q4 resolved; mechanics in `architecture-recommendation.md`; amendments applied inline |
| A2 | :89 (L4) | "no racing after the first forwarded content byte" → "no retry/fallback AFTER first forwarded byte; before that, unchanged"; winner gate redefined to first forwardable chunk |
| A3 | :90 (L5) | Live mode does NOT bypass the coordinator — same coordinator, redefined gate; StreamDeadline in live mode = no-forwardable-byte timeout (in-band SSE error + cancelAll; requires 0-byte guard `race_coordinator.go:661-722`) |
| A4 | :99-132 (§4 Q1) | Replaced bypass framing with R1-variant resolution: gate redefinition; no `streamLiveRelay`; predicate + hazard fix + deadline guard; corrected touch points |
| A5 | :161-198 (§4 Q2) | Added "Resolution mechanics (architect)": typed-event entry, single-open-block interleave ruling, deferred `content_block_stop`, `message_delta`-at-Finalize with zero-usage default, `Finalize`-after-`toolCallBuffer.Flush`, run-both-translators parity test |
| A6 | :258-263 (§4 Q3 pinned rule) | "coordinator skipped ⇒ NO deadline machinery" → coordinator runs with first-byte gate; deadline = no-forwardable-byte timeout |
| A7 | :391-501 (Phase 2) | Full rewrite per §2 of this document (objective/scope/files/tasks/risks/exit criteria; stale cite `:1138-1148` → `:1169-1178`) |
| A8 | :429 | Struck "Any change to race_coordinator.go's race logic" from Out-of-scope (now IN scope, live-mode-gated) |
| A9 | :871 (§6 coupling) | "both add `streamLive*` functions" → corrected (no new relay function; writeMu discipline unchanged) |
| A10 | :892 (§7 row 7) | "Coordinator logic unchanged" → coordinator gate + deadline guard changed; `race_coordinator_test.go` gains tests |
| A11 | §7 new row | `race_retry_test.go` deadline-path tests (+ post-winner failover tests) opt into buffered mode via header |
| A12 | :923/:925 (§8.1) | Usage-metering row: estimator input = raw bytes via `GetAllRawBytesOnce` (live-mode completeness via Prune gating); bufferstore row: entry capture buffered-only, Done capture authoritative |
| A13 | :933/:937-939 (§8.2) | Racing row mitigation updated; estimator row rewritten (R2 verdict + fix); partial-release row rewritten (superseded by first-byte gate); StreamDeadline row rewritten (machinery runs, redefined) |
| A14 | :960 (§9 E9) | "Coordinator logic is unchanged" → coordinator IS changed (live-mode-scoped gate + deadline guard) |
| A15 | §10 | Assumptions added: Prune-gating memory tradeoff (R17); `race_winner_selected` mid-stream (R13); atomic.Bool (§1.6) |
| A16 | §11 | All four open questions marked RESOLVED with answers (Q4 keep name + atomic.Bool; Q2 (b) = slip-fallback only; L5 default confirmed; loop-top break) |
| A17 | §12 appendix | New citations: predicate sites, deadline branch, Prune call sites, estimator input, fallback-loop guard site |

Phases 2/3/4 remain **file-disjoint and parallelizable after Phase 1** (Phase 2: `race_coordinator/race_executor/handler.go(:924+, relay)/handler_helpers`; Phase 3: `handler_anthropic/internal_handler/translator`; Phase 4: `ultimatemodel/handler.go(:597-862)` — handler.go regions disjoint).

---

## 7. Residual Risks / Unresolved Items (flagged for reviewer)

1. **Anthropic SDK interleave tolerance (R12)** — assumption-based (passthrough parser evidence only). Verify against a recorded real-Anthropic stream (or SDK source) before Phase 3 exit; option (b) per-variant fallback is the documented escape.
2. **`bufferStore.Save` overwrite semantics** — the buffered-mode double-save (entry + Done) behavior at `handler.go:1053-1058/:1182-1187` was not read this pass; developer confirms the overwrite model when gating the entry capture.
3. **StreamDeadline default 110s** — carried from plan L5 / technical-analysis (config.go:165 not re-opened this pass). No decision hinges on the value; do not hardcode.
4. **Gate implementation form** — Worker C's safety analysis assumes the predicate lands in the existing ticker arm with `onceStream` close semantics (`race_coordinator.go:431`); if the implementer chooses a different form (e.g., per-attempt atomic flag), re-verify H1–H4 hazards and the `:653` deadline guard.
5. **Real-provider first-chunk behavior** (role-only / usage-only / comment-first) — asserted from wire convention + mocks, not provider fixtures; Phase 5 golden matrix should include provider-shaped first chunks.
6. **`Finalize`-after-`Flush` ordering on the internal variant (R11)** — constraint pinned and testable, but the exact call-site reshuffle at `internal_handler.go:856-928` is developer territory; flagged so it is not dropped.

---

## 8. Approach Comparison (locked resolutions vs. rejected alternatives — mechanics-validated)

| Approach | Complexity | Scalability | Maintainability | Risk | Cost | Recommendation |
|----------|------------|-------------|-----------------|------|------|----------------|
| **R1-variant: gate redefinition** (locked) | **Low** — predicate swap + deadline guard + reorder; relay untouched | Good — one goroutine fewer per request vs bypass? No: same topology as today | **High** — coordinator remains single source of truth for racing | **Med** — mid-stream cancel + deadline guard need care (mitigated §3) | **Low** (~3 files + tests) | ✅ **Adopted** — validated |
| R1-strict: bypass coordinator, single attempt | Med — new relay path + LB re-wiring | Neutral | Med — two racing truths | Med — loses pre-first-byte fallback chains | Med | Rejected by leader — confirmed inferior |
| Option B: warm-up window + promote | High — 3 new mechanisms incl. deadline-handoff fix | Neutral | Low | High (deadline-path test churn) | High | Rejected (planner + this pass concur) |
| **Q2 (a) incremental translator** (locked) | Med-High (~250 LOC + tests) but helpers reused | Good — per-request state, GC'd | High — own file, batch untouched | Med (ordering) — pinned by parity tests | Med | ✅ **Adopted** — mechanics in §1.4 |
| Q2 (b) buffered-translation-only variants | Low | Neutral | High | Low | Low | **Slip-fallback only** (per-variant gate softening) |
| **Q4 atomic.Bool reuse** (locked + refinement) | Trivial (2 lines) | n/a | High — completes reserved slot | Low (eliminates race class) | Trivial | ✅ **Adopted** with atomic refinement |

---

> **End of architecture recommendation.** plan.md carries the applied amendments (§6); the developer implements from the amended plan; `docs/real-streaming-default.md` (Phase 5) must carry the documented wire changes: live-mode thinking blocks (internal variant), live-mode block interleaving, winner-validity, StreamDeadline redefinition, and the R2 fix.
