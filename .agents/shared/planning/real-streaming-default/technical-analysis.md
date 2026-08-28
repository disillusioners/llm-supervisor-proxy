# Technical Analysis: real-streaming-default

Date: 2026-08-27 (re-verified against `feature/real-streaming-default @ 9842c77`)
Author: technical-analysis worker (executed by `worker` via injected `technical-analysis` skill)
Analysis depth: deep-dive (per-question)
Status: Draft (input to architect's deepening — not a final design)

## Question

Resolve the four OPEN design questions for the `real-streaming-default` feature of `llm-supervisor-proxy`:

- **Q1 (OpenAI seam)** — Option A (bypass race coordinator: single direct attempt + live relay reusing the `streamResult` relay-loop pattern, `handler.go:1038-1380`) vs Option B (warm-up window then promote winner). Leader leans A. Known B complication: the partial-release deadline-handoff gap (`race_coordinator.go:649-723` closes `streamCh` at deadline but the relay still waits on `buffer.Done()` / hard-cancel).
- **Q2 (Anthropic translation variants)** — OpenAI-wire upstream → Anthropic-wire client. The pivotal question because `TranslateBufferedStream` (`translator/stream.go:19`) cannot be flag-flipped. (a) NEW incremental/streaming translator vs (b) default-mode buffers ONLY translation-required variants. Also: internal-variant recorder problem (`flushingResponseRecorder` `Flush` no-op at `handler_anthropic.go:591-598`) vs threading the true `w` into `InternalHandler.HandleRequest` while preserving the `thinkingSink` invariant (`internal_handler.go:32-41, 379-396`).
- **Q3 (Precedence)** — `X-LLMProxy-Buffer-Response` opt-in buffering vs per-model `ReleaseStreamChunkDeadline` auto-release (`pkg/models/config.go:123-150`, DB column `pkg/store/database/store.go:454`). Leader suggestion: explicit header wins.
- **Q4 (State field)** — Reuse `rc.streamingNonRetryable` (`handler_helpers.go:102`, declared-but-unset, apparently reserved) vs a new `rc` field.

Locked decisions (NOT relitigated here): real-streaming is the DEFAULT for `/v1/chat/completions`; existing `X-LLMProxy-Buffer-Response` opt-in preserves today's buffered behavior; mid-stream error semantics (post-first-content-byte failure = terminal in-band SSE error matching today's post-winner semantics) is preserved; `model_credential_selected` asymmetry left as-is; ultimate-model paths stay raw-passthrough (no change); `SetCredentialFailover` / `credEngine` injection plumbing stays unchanged.

## Context Summary

Today the proxy is buffered end-to-end with two islands of real streaming: the Anthropic→Anthropic-credential passthrough (`handler_anthropic.go:1225`, `handlePassthroughStreamResponse`) and the MCP proxy. The OpenAI race path *appears* to stream (`streamResult` is a live relay loop at `handler.go:1038-1380`) but the relay is gated by `coordinator.WaitForWinner()` at `handler.go:927`, and the coordinator only closes `streamCh` after `req.IsCompleted()` returns true (`race_coordinator.go:409, 431`). The relay cannot begin forwarding chunks until one upstream has emitted `[DONE]` (or the `streamDeadlineTimer` fires at `race_coordinator.go:370` and `handleStreamingDeadline` picks the best buffer at `:649`). Both ultimate-model paths (`pkg/ultimatemodel/handler_external.go`, `pkg/ultimatemodel/handler_internal.go`) are raw passthroughs that already stream — `executeExternal` writes to `w` directly, `executeInternal` resolves to the typed provider path; no change is needed there. The Anthropic translation paths buffer either via `TranslateBufferedStream` (`handler_anthropic.go:728` for internal-non-Anthropic, `:851` for external-non-Anthropic) or via the recorder (`handler_anthropic.go:612`).

Headline re-verification findings the analysis below depends on (file:line evidence):

1. The OpenAI relay loop is already live. `streamResult` at `handler.go:1038-1380` reads `buffer.NotifyCh()` and `buffer.Done()` and writes `chunk` directly to `w` under `rc.writeMu`. The `: connected\n\n` preamble is sent pre-winner at `handler.go:882-889`. The race coordinator's gate is the only thing delaying first content byte.
2. The race winner is selected on completion, not on first content. `race_coordinator.go:406-435`: "Only select winner when request is fully completed with `[DONE]` signal." The `IsCompleted()` boundary is what gates `streamCh` close.
3. The Anthropic→Anthropic passthrough already streams. `handlePassthroughStreamResponse` (`handler_anthropic.go:1225-1295`) is the in-tree template for live relay: scanner-per-line, `flusher.Flush()` after every `w.Write(line)`, error semantics on scanner error.
4. The `thinkingSink` invariant. `internal_handler.go:32-41` declares: "thinking bytes must NEVER be written to the ResponseWriter `w`". Reinforced at `internal_handler.go:98-100` (capture-only side channel) and enforced at the `case "thinking"` arm (`internal_handler.go:379-396`) which writes to `h.thinkingSink` ONLY when non-nil.
5. **`rc.streamingNonRetryable` is declared-and-reset, never set.** Declared at `handler_helpers.go:99-102`, reset at `handler_helpers.go:139`; zero `=` assignments elsewhere in the tree (full-tree grep confirmed).
6. **`ReleaseStreamChunkDeadline` is configured but never invoked.** The runtime uses `c.cfg.StreamDeadline` (`race_coordinator.go:370`, `handler.go:42, 67`, `race_executor.go:635`) — a GLOBAL field on `ConfigSnapshot`. The per-model getter `ModelConfig.GetReleaseStreamChunkDeadline()` (`pkg/models/config.go:143-151`) has zero call sites in `pkg/proxy`. The only references are DB column (`store.go:454`), UI wire/CRUD (`ui/server.go:54, 521, 611, 729`), and a config-deep test (`pkg/models/config_deep_test.go:147-160`). The `StreamChunkDeadlineEvent` type exists (`pkg/events/bus.go:59-60`) but is never published in production code paths. **The per-model deadline field is dormant.**
7. The `X-LLMProxy-Buffer-Response` header does not yet exist in the codebase. Full-tree grep for `X-Buffer` / `BufferResponse` / `force_buffer` returns only the ultimate header `X-LLMProxy-Ultimate-Model` and unrelated helpers. The header is a NEW contract proposed in this analysis.

---

## Q1 — OpenAI path seam

### Question

Where does the seam between client-write and upstream first-byte live? Option A: bypass the race coordinator entirely when real-streaming is the default, do a single direct upstream attempt with a live relay that reuses `streamResult`'s pattern. Option B: keep the coordinator, add a "warm-up window then promote winner" mechanism that streams from the leading attempt without waiting for `[DONE]`.

### Current Patterns

- **Race-coordinator-driven relay.** `handler.go:924-947`: `coordinator.Start()` → `WaitForWinner()` blocks on `c.streamCh`; only after a winner is selected does `streamResult(w, rc, winner)` begin forwarding.
- **Winner eligibility.** `race_coordinator.go:406-435`: "Only select winner when request is fully completed with `[DONE]` signal." `IsCompleted()` is the gate. The relay cannot start until *some* upstream emits `[DONE]`.
- **Pre-winner preamble.** `handler.go:882-889`: `: connected\n\n` is written BEFORE the coordinator is started. This is already in place — Option A needs no preamble change.
- **Mid-stream error path.** `streamResult` `case <-buffer.Err()` (`handler.go:1149-1178`): writes an OpenAI-shaped SSE error event, returns. This is the post-first-content-byte failure semantics the task pins.
- **Heartbeat.** `handler.go:902, 938-909, 902-909`: 5-second cadence comment heartbeats keep the client socket warm while the relay is idle; the relay acquires `rc.writeMu` before every `w.Write` so heartbeats never interleave.
- **Idle termination.** `handler.go:1361-1377`: if `IdleTerminationEnabled` and the winner is idle past `IdleTerminationTimeout`, `winner.Cancel()` and exit. Compatible with Option A as long as the same `rc` fields drive it.
- **`extractStreamChunkContent` tap.** `handler.go:1071-1073, 1111-1113, 1137-1139, 1346-1348`: every chunk that hits `w` is also fed into the inline tap for `rc.accumulatedResponse / accumulatedThinking / accumulatedToolCalls`. **This is the UI record-parity tap**; the analysis below treats it as mandatory.

### Options

#### Option A — Bypass race coordinator with single direct attempt + live relay

A new code path under `handler.go` (or a sibling function) that, when real-streaming is the default and the request is OpenAI-streaming, skips the coordinator entirely:

1. Resolve credential (current `credentiallb.Engine.PickFresh` path or pre-resolved via `rc.conversationKey`) — single credential, no fallback.
2. Spawn ONE direct upstream attempt against the resolved credential, returning a `*upstreamRequest` whose `streamBuffer` is already wired (today `race_executor.go:executeRequest` builds that — see below for divergence surface).
3. The `streamResult` function at `handler.go:1038` is called with `winner` set to that single attempt as soon as the buffer is non-empty (or on a configurable first-chunk-deadline), not on `IsCompleted()`.

**Divergence surface:** the relay loop itself (`handler.go:1038-1380`) is REUSED. What changes is the WAIT-FOR-WINNER step (`handler.go:924-927`). The single attempt still goes through `executeRequest` (race_executor.go) to keep the credential resolution + SSE parse + normalizer + toolrepair wiring intact; what is bypassed is the `raceCoordinator.Start()` + `coordinator.WaitForWinner()` pair and the `cancelAllExcept(winner)` machinery that runs once a winner is found.

The simplest concrete design: a new `Handler.executeSingleStreamingRequest(w, rc)` that
- calls `credEngine.PickFresh(...)` once,
- calls the existing single-attempt builder (extract the inner goroutine of `race_coordinator.go:execute`),
- runs the existing `streamResult` body with a sentinel "winner is the single attempt" and a poll-on-buffer loop instead of `WaitForWinner`.

The race coordinator code path remains the fallback (legacy single-attempt, retry/fallback) AND remains the DEFAULT when the client sends `X-LLMProxy-Buffer-Response: true` (so buffered semantics are preserved byte-identical).

**Failure modes:**
- Credential resolution failure → fall back to the buffered coordinator path (the current behavior). No new error class.
- Upstream connect error → no chunk has reached `w` yet; send the existing `handleRaceFailure` envelope (`handler.go:967-978`) via the new single-attempt wrapper.
- Upstream mid-stream error after first content byte → existing `buffer.Err()` path at `handler.go:1149-1178`. **No change.**
- Client disconnect → existing `clientGoneCh` and `rc.baseCtx.Done()` paths in `streamResult`. **No change.**
- **Subtle:** if the single upstream is slow on first byte, the client sees the `: connected\n\n` preamble but no chunks for the first-chunk-deadline. This matches today's behavior (`: connected\n\n` is sent pre-winner at `handler.go:886`); no degradation.

**Retry boundary:** under Option A, "retry on error before first content byte" is replaced with "credential failover on error before first content byte" — single attempt, single credential, no model-fallback. This is consistent with the locked decision (the single-credential real-streaming default is the user's choice) but worth stating explicitly so the architect does not accidentally preserve retry semantics.

**Test burden:** Option A can reuse the existing `streamResult` body almost verbatim. New tests needed:
- (a) the single-attempt path (one mocked upstream, one credential, real streaming).
- (b) credential-failover before first byte (engine picks another credential; relay restarts).
- (c) mid-stream error after first byte (terminal SSE error, no retry).
- (d) preamble-only timeout (client sees `: connected`, no chunks, no retry — connect vs read-timeout regression test).

#### Option B — Warm-up window then promote winner

Keep the coordinator. Add a new winner-eligibility mode: "winner can be promoted after `T` ms OR after first content chunk arrives, whichever comes first, instead of waiting for `[DONE]`."

**Divergence surface:** `race_coordinator.go:406-435` (the `IsCompleted()` gate), `handleStreamingDeadline` (`race_coordinator.go:649-723`, currently invoked only after the timer fires), and the `WaitForWinner` semantic. `streamResult`'s relay loop already handles the post-promotion case correctly — it reads `buffer.NotifyCh()` and forwards whatever's in the buffer.

**The deadline-handoff gap** (the user's stated concern). Today, `handleStreamingDeadline` (`race_coordinator.go:649-723`) closes `streamCh` at `:693` and `c.onceStream.Do(func() { close(c.streamCh) })`. The relay in `streamResult` starts AFTER `WaitForWinner` returns; that return happens when `<-c.streamCh` fires (line 833). So `WaitForWinner` unblocks, then `streamResult` calls `buffer.GetChunksFrom(readIndex)` (line 1063) which returns whatever chunks were buffered so far, and writes them. The relay then enters `for { select { case <-buffer.NotifyCh(): ... } }` (line 1089-1123). This *should* work after the deadline.

But there is a real gap. Today, when `handleStreamingDeadline` fires (`race_coordinator.go:649`), it cancels the non-winner requests (`:700-704`) but does NOT cancel the winner. The winner keeps streaming into its buffer until either `[DONE]` or `MaxGenerationTime` (`hardDeadlineTimer` at `race_coordinator.go:376, 393`). The relay reads chunks off the buffer as they arrive. **That works.** The bug the user is referring to is more subtle: today, `streamCh` is closed by `handleStreamingDeadline` (`:693`), but the `WaitForWinner` select also waits on `<-c.done` (`:835`) and `<-c.baseCtx.Done()` (`:837`). If the winner is the *only* request and it's slow but emitting chunks, `WaitForWinner` does not return on `<-buffer.NotifyCh()` because there is no such select case. The relay cannot start until either `streamCh` or `done` fires. Today, `streamCh` fires only when winner completes (`:431`) OR when deadline fires (`:693`). So Option B requires either:

- (B1) Adding a new winner-promotion path that closes `streamCh` on first-chunk OR deadline (whichever is first) — this is *new* code in `race_coordinator.go` and needs to be careful about which buffer to pick (today, the deadline path picks "most bytes"; first-chunk path picks "first chunk arrived").
- (B2) Adding a "leader" pattern — track which buffer has been advancing most actively and promote that. Same complexity.

**Failure modes (Option B):**
- First-chunk-deadline misfires: if we promote on first chunk and the upstream stalls, we've already committed to that buffer with no recourse (today, race coordinator cancels the others on winner-promote at `race_coordinator.go:442`).
- Subtle: partial-release machinery (`handleStreamingDeadline` at `:649`) is currently invoked only AFTER `streamDeadlineTimer.C` fires; it's not currently used as the "warm-up window" mechanism. Re-purposing it changes its semantics from "best-effort late promotion" to "fast-path default promotion." That risks race-coordinator tests that rely on the current semantics (e.g. `race_retry_test.go:72, 134, 183, 241, 285, 338` all set `StreamDeadline: 5 * time.Second` — they test the deadline path specifically).

**Retry boundary:** under Option B, model-fallback retry-before-first-content is preserved; mid-stream error semantics remain identical (no retry). This is a closer behavioral fit to the current production path — both buffered and real-streaming share retry-before-first-byte semantics.

**Test burden:** higher. New tests for:
- (a) warm-up promotion on first chunk (winning buffer pre-empts; other buffer cancelled).
- (b) warm-up promotion on first-chunk-deadline (best-buffer promotion; per-deadline logic intact).
- (c) post-promotion mid-stream error (terminal SSE error, no retry).
- (d) regression of existing `race_retry_test.go` deadline-path tests.
- (e) regression of credential-failover logic (`race_coordinator.go:285-311` and the `credFailoverEligibleLocked` gate — must still fire on 429 before first content byte).

### Comparison (Q1)

| Criterion | Option A (bypass) | Option B (warm-up) | Winner |
|---|---|---|---|
| Code paths touched | `handler.go:924-947` replaced; `streamResult` REUSED; `race_executor.go` inner goroutine extracted | `race_coordinator.go:406-435, 649-723, 924-947` modified; `streamResult` reused | A — minimal divergence |
| Correctness (semantics drift) | Medium — must keep retry-before-first-byte + credential failover; mid-stream = no retry | Low — close to current behavior | B |
| Complexity (LOC estimate) | ~150 LOC new + reuse of `streamResult`; ~30 LOC removed | ~80 LOC new in coordinator + new select cases; existing deadline path coexists | A |
| Divergence from current behavior | Higher — bypasses coordinator; small risk of missing a fallback edge case | Lower — keeps coordinator as source of truth | B |
| Test burden | Medium — new single-attempt path; reuse existing relay tests; few regressions | High — new warm-up promotion path; existing deadline tests must still pass; first-chunk arbitration needs new tests | A |
| **Total** | — | — | **A on balance** (low divergence + low LOC, but B has lower correctness risk) |

### Recommendation (Q1)

**Pick: Option A** — bypass race coordinator with single direct attempt + live relay, reusing `streamResult`.

**Reasoning:** the relay loop is already proven. The `: connected\n\n` preamble is already pre-winner. The coordinator's value-add (retry, model fallback, race-promotion of best-of-N) is wasted under real-streaming-default because we cannot retry after first content byte. Option A reuses all of that machinery for the buffered/legacy path and bypasses it for the streaming-default path. **The retry-before-first-byte window** (where the coordinator earns its keep for credential failover) is the only behavior worth preserving across both paths, and that lives at `race_coordinator.go:285-311` (the `modelTypeCredFailover` case + `credFailoverEligibleLocked` gate). A clean way to keep it: the single-attempt path calls `credEngine.PickFresh(...)`, performs ONE direct upstream call, and on rate-limit-error pre-first-byte calls `credEngine.ExcludeAndReselect(...)` and retries ONCE with the reselected credential. This mirrors today's "single 429 → one credFailover attempt" logic without standing up the full coordinator.

**Assumptions:**
- A1. The locked decision is "real-streaming is the default." That implies the buffered path is the legacy fallback for clients who explicitly opt in via `X-LLMProxy-Buffer-Response: true`. Both paths must work; they must NOT diverge in error semantics for pre-first-byte failures.
- A2. The model-fallback chain (`resolvedModel.FallbackChain`) is preserved for the buffered/legacy path AND for the buffered-fallback-after-real-streaming-failure path (real-streaming attempt fails → fall back to coordinator path on the same request). **This is a behavior the architect must explicitly decide.** The simplest version: real-streaming attempt fails pre-first-byte → return to the legacy coordinator path with the error classified (try the next model in the fallback chain). This is what Option A above assumes. **🔴 uncertain** — the user did not specify whether model-fallback after real-streaming-attempt-failure should be the same-chain continuation, a separate chain, or skip-to-buff-legacy.
- A3. Single-credential resolution is acceptable for the real-streaming path. The locked decision implies this; if conversation-sticky affinity (`credentiallb.ComputeConversationKey` + `credEngine`) is preserved, the resolution seam at `handler.go:456-474` still applies.

**Reversibility:** high. The buffered coordinator path is untouched. If Option A misbehaves, a flag flip on the real-streaming-default code path (e.g. `useRealStreamingDefault := false`) reverts production to today's behavior.

### What the architect should verify before finalizing

- V1. **Pre-first-byte credential-failover contract.** Option A as described keeps single-credential + single-credFailover-retry. Confirm this is the intended scope (vs preserving the full model-fallback chain via the coordinator when the single-credential attempt fails).
- V2. **`streamBuffer.GetChunksFrom(0)` race.** The first relay write (the "existing chunks first" branch at `handler.go:1063-1083`) runs before the relay loop. Under Option A, this fires whenever the buffer has accumulated ≥1 chunk before `streamResult` enters the loop. Verify this is the desired behavior — currently true under both paths, so no change.
- V3. **Failure-classification handoff.** When the single-attempt under Option A returns a non-retryable error (e.g. 400 bad-request), the buffered coordinator path must NOT receive a request to retry it. The architect should define an explicit "do not retry this" flag at the requestContext level (this is exactly `rc.streamingNonRetryable` — see Q4).
- V4. **The "warm-up window" race in Option B.** If the architect prefers B after seeing V1–V3, then confirm whether "promote winner on first chunk" means "first byte to leave the upstream's TCP socket" (race-executor-internal) or "first byte to be `Add`-ed into `streamBuffer`" (buffer-internal). Today these are within milliseconds; the choice affects test timing.

---

## Q2 — Anthropic translation variants

### Question

The Anthropic adapter's `WriteStreamEvent` (`adapter_anthropic.go:154-159`) returns an error: "streaming requires buffered translation - use BufferedStreamTranslator." The current code uses `TranslateBufferedStream` (`translator/stream.go:19`), which consumes the WHOLE upstream stream and emits the WHOLE Anthropic event sequence. For real-streaming-default, we cannot consume the whole stream before emitting — we must emit Anthropic events as OpenAI SSE deltas arrive. Two paths to explore:

- **(a) NEW incremental/streaming translator** — stateful across chunks; emit Anthropic `message_start`, `content_block_start/delta/stop` (incl. thinking blocks), `message_delta`, `message_stop` as the OpenAI deltas arrive.
- **(b) Default-mode buffers ONLY translation-required variants** — external/LiteLLM at `handler_anthropic.go:819+851` and internal-non-Anthropic via recorder at `:612/:728`. Real-streaming applies to the Anthropic→Anthropic passthrough (already streaming) and to OpenAI→OpenAI / external→external streaming where no translation is needed.

### Current Patterns

- **Translation-required variants:** four entry points, all buffered:
  - External OpenAI→Anthropic streaming: `handleAnthropicStreamResponse` (`handler_anthropic.go:796-887`), buffered at `:819-878`, translated at `:851` after `[DONE]` is detected.
  - External OpenAI→Anthropic non-streaming: `handleAnthropicNonStreamResponse` (`:763-793`), body fully buffered at `:765`, translated at `:778`. (No real-streaming concern.)
  - Internal non-Anthropic streaming: `handleAnthropicInternalStreamResponse` (`:719-761`), `recorder.Body.Bytes()` (`:642`) passed in, translated at `:728`. The recorder pattern is the "internal variant" problem.
  - Internal non-Anthropic non-streaming: not streaming-relevant.
- **Translation-required→real-streaming candidate:** none. All four entry points buffer end-of-stream.
- **`AnthropicAdapter.WriteStreamEvent` refusal:** `adapter_anthropic.go:154-159`: "For streaming, we need to translate each OpenAI chunk to Anthropic format / This is more complex and handled by the stream translator / For now, we'll use the buffered approach" → returns error. This is a STATEMENT that the buffered approach is intentional; today no caller of `WriteStreamEvent` for Anthropic exists in production (greppable).
- **Translator state machine.** `TranslateBufferedStream` (`translator/stream.go:19-64`) builds a `*StreamState` with `MessageID`, `OriginalModel`, `AccumulatedContent`, `ThinkingContent`, `ToolCalls`, `Usage`, `StopReason`, then calls `generateAnthropicEvents(state)` (`:144-244`) which emits the entire event sequence in order:
  1. `message_start` (`:148-163`)
  2. `ping` (`:166-169`)
  3. `content_block_start` for thinking (`:178`)
  4. `content_block_delta` for thinking (`:181`)
  5. `content_block_stop` for thinking (`:184`)
  6. `content_block_start` for text (`:191`)
  7. `content_block_delta` for text (`:194`)
  8. `content_block_stop` for text (`:197`)
  9. `content_block_start` + `input_json_delta` + `content_block_stop` per tool call (`:200-223`)
  10. `message_delta` with final usage + stop_reason (`:226-235`)
  11. `message_stop` (`:238-241`)
- **In-tree passthrough template.** `handlePassthroughStreamResponse` (`handler_anthropic.go:1225-1295`): scanner-per-line, `flusher.Flush()` after every `w.Write(line)`. This is the implementation pattern that the new incremental translator must match (real-time flush per line).

### Option (a) — NEW incremental/streaming translator

A new `pkg/proxy/translator/stream_incremental.go` that holds state across calls and emits Anthropic SSE events as OpenAI deltas arrive.

#### Concrete design sketch

**State.** A `StreamTranslator` struct with:
- `msgID` (generated at `message_start`, consistent with today's `generateAnthropicMessageID()`).
- `originalModel` (from the request, set once at construction).
- `blockIndex int` (next content block index to emit).
- `blocks []activeBlock` where `activeBlock` records the block type and accumulated state (text / thinking / tool_use).
- `textContent strings.Builder`, `thinkingContent strings.Builder` (raw accumulators — same as today's state).
- `toolCalls []ToolCallState` (same as today's, but indexed by OpenAI `tool_calls[i].index`).
- `messageStartSent bool`, `pingSent bool`, `usageProcessed bool`, `stopReason string`, `outputTokens int`.
- `outputBuffer bytes.Buffer` (SSE-formatted lines ready to flush) + `mu sync.Mutex`.

**Per-chunk handler.** `ProcessChunk(data []byte) ([]byte, error)` consumes one OpenAI `data:` line (parsed by caller; the new translator does NOT do SSE framing — that's the caller's job, mirroring `extractChunkContent`'s parser at `translator/stream.go:67-141`) and returns the Anthropic SSE lines to emit. Per OpenAI delta:
- `role: "assistant"` first chunk (OpenAI sends this once on stream start) → emit `message_start` (line:147-163 of today) + `ping` (line:166-169). Set `messageStartSent=true`, `pingSent=true`.
- `delta.content` non-empty → ensure a `text` block is open (emit `content_block_start` if first delta); emit `content_block_delta` with `type: "text_delta"`.
- `delta.reasoning_content` or `delta.thinking` non-empty → ensure a `thinking` block is open; emit `content_block_delta` with `type: "thinking_delta"`. **Note: must mirror the `thinkingSink` invariant** — but since this translator is in the Anthropic-wire path, thinking blocks ARE delivered to the client (vs the OpenAI-wire path where thinking is captured only for persistence). See "Cross-cutting invariants" below.
- `delta.tool_calls[i]` — for each tool call delta, emit `content_block_start` (if new id+name arrived), `content_block_delta` with `type: "input_json_delta"` (accumulating `function.arguments`), `content_block_stop` (when `finish_reason` is `tool_calls` or stream ends).
- `usage` (typically last chunk) → emit `message_delta` with `output_tokens` from upstream usage. Set `usageProcessed=true`. **NOTE: Anthropic requires `message_delta` BEFORE `message_stop`.** Today's buffered path emits them in this order (`:235, :241`).
- `finish_reason` → store in `stopReason`; defer emission of `message_delta` until `[DONE]` or stream end (so we can attach the final usage).
- `[DONE]` → emit `message_delta` (if not yet emitted), emit `message_stop`. Close any still-open content blocks (`content_block_stop` for each `blocks[i]` not yet closed).

**Mapping the buffered logic to streaming:** the `extractChunkContent` logic at `translator/stream.go:67-141` is the per-chunk parser — it can be lifted into the streaming translator with one change: instead of accumulating into state and emitting at the end, the streaming translator emits immediately based on the delta. The `generateAnthropicEvents` function (`translator/stream.go:144-244`) becomes the "what to emit when stream ends" path — it must be split into per-block-close handlers, per-message-end handlers, and a single `message_stop` finalizer.

**`AnthropicAdapter.WriteStreamEvent` interaction.** Today this returns an error (`:158`). Under (a), it becomes a thin wrapper: `return a.writeStreamEventIncremental(w, openaiChunk)` which calls the new translator's `ProcessChunk` and writes the returned bytes. The error from `WriteStreamEvent` is today only consumed by tests; production callsites do not exist for Anthropic streaming. This is the cleanest integration point.

**Failure modes:**
- JSON parse error on a delta → log + skip (same as today's `extractChunkContent` line 45). The translator must NEVER crash on a malformed chunk — current `extractChunkContent` handles this via `continue`.
- Out-of-order deltas (e.g. tool call deltas arriving before content) → emit in received order; the Anthropic client tolerates content_block_start with empty content_block (today's `formatContentBlockStart` (`:257-277`) initializes `text` and `thinking` to `""`).
- Mid-stream error from upstream → the caller (new code in `handler_anthropic.go`) emits `sendAnthropicSSEError` (`:1129`) on top of any partially-emitted content. This is the same as today's "post-first-content-byte terminal error" semantics.
- Stream end without `[DONE]` → today's `handleAnthropicStreamResponse` (`:880-886`) calls `sendAnthropicSSEError`. The streaming translator under (a) must emit `message_stop` (so clients see a well-formed end) THEN allow the caller to emit the SSE error. Sequence: translator emits `message_stop`, then caller writes the error event.

**Test burden (a):**
- (a1) Per-event-type translation tests (mirror `translator/stream_test.go`).
- (a2) Cross-chunk stateful ordering tests (thinking→text, text→tool, tool→message_delta, message_delta→message_stop).
- (a3) Reordering / out-of-order deltas.
- (a4) Mid-stream error after first content byte.
- (a5) Stream end without `[DONE]` (the upstream never sent `[DONE]`, scanner ends).
- (a6) Compatibility tests: replay today-recorded upstream traces through the new translator and assert Anthropic event equivalence with the buffered translator's output.

### Option (b) — Default-mode buffers ONLY translation-required variants

Real-streaming is the default ONLY where translation is NOT required:
- OpenAI→OpenAI (OpenAI client + OpenAI upstream) — already real streaming via `streamResult`.
- External non-Anthropic upstream + OpenAI client — already real streaming via `streamResult`.
- Anthropic→Anthropic-credential passthrough — already real streaming via `handlePassthroughStreamResponse` (`handler_anthropic.go:1225`).
- Anthropic client + OpenAI upstream (any variant) — STAYS BUFFERED. This is the only variant under (b) that violates "header absent ⇒ first content byte forwarded before upstream completion."

**Variants under (b) that DO violate the acceptance gate** (header absent ⇒ first content byte forwarded before upstream completion):
- **External non-Anthropic upstream + Anthropic client:** `handleAnthropicStreamResponse` (`handler_anthropic.go:796-887`). The buffered path requires buffering the entire upstream stream before emitting the first Anthropic event. **Violates.**
- **Internal non-Anthropic upstream + Anthropic client:** `handleAnthropicInternalStreamResponse` (`handler_anthropic.go:719-761`). The recorder is fully buffered (`handleAnthropicInternalRequest` calls `internalHandler.HandleRequest(arc.baseCtx, openaiReq, recorder, ...)` at `:642`). **Violates.**

**Variants under (b) that satisfy the gate:**
- OpenAI-wire everywhere (`handler.go`'s `streamResult` path). 🟢 already real-streaming.
- Anthropic passthrough (`handler_anthropic.go:1225` `handlePassthroughStreamResponse`). 🟢 already real-streaming.
- Anthropic client + non-streaming — no real-streaming concern.
- Ultimate-model raw passthrough (both internal and external). 🟢 already real-streaming in `pkg/ultimatemodel/`.

**Internal-variant recorder problem.** `flushingResponseRecorder` (`handler_anthropic.go:591-598`) is a wrapper around `httptest.ResponseRecorder` with `Flush()` as a no-op. The recorder is fed by `internalHandler.HandleRequest(arc.baseCtx, openaiReq, recorder, ...)` at `:642`. Today's design: `internalHandler.HandleRequest`'s `case "content"` arm (`internal_handler.go:357-377`) calls `fmt.Fprintf(w, "data: %s\n\n", data)` + `flusher.Flush()`; `flusher` is `w.(http.Flusher)`. Since `flushingResponseRecorder.Flush()` is no-op, the bytes are still captured by `ResponseRecorder.Body` but NOT forwarded to the client. Today, the recorder is purely a sink; the Anthropic-wire client only sees the translated output (after buffering all of it). To make the internal variant real-streaming, the architect must:

- **Either:** thread the true `w` (the Anthropic-wire client) into `internalHandler.HandleRequest`, replacing the recorder. But this breaks the `thinkingSink` invariant: `internal_handler.go:379-396` writes thinking to `h.thinkingSink` ONLY and never to `w`. If `w` is the Anthropic client, thinking text would be written there as OpenAI-shaped SSE data lines, which `TranslateBufferedStream` would then convert to Anthropic `content_block_delta` thinking events. **That would leak thinking to the Anthropic client.** The pre-fix fea5874 documented behavior DROPS thinking from the wire entirely for this exact reason.
- **Or:** introduce a NEW recorder variant that captures the body AND implements a real `Flush()` that forwards each new line to the true `w` (the Anthropic client) under the `thinkingSink` invariant — meaning the recorder strips thinking lines before forwarding. This is more code than (b) asks for; the recorder no longer is just a sink.

**Or (variant b'):** Keep the recorder pattern (no `w` threading). Build the incremental translator inside `pkg/proxy/translator` (option (a) above) and feed it the recorder body incrementally — but this requires `ResponseRecorder` to support chunk-by-chunk reads, which it does NOT (it captures all writes into `Body` and exposes only the final result). The only way to make incremental work with the recorder is to read `Body` as it grows, which is not the stdlib's contract.

**Conclusion for the recorder problem under (b):** the internal non-Anthropic → Anthropic streaming path cannot trivially become real-streaming without (a). The recorder pattern must be broken OR the incremental translator must be built. **Under pure option (b), the internal non-Anthropic → Anthropic streaming path stays buffered.**

### Comparison (Q2)

| Criterion | Option (a) incremental | Option (b) buffers-only-translation-variants | Winner |
|---|---|---|---|
| Code paths touched | New `pkg/proxy/translator/stream_incremental.go` (≈300 LOC); new internal Anthropic streaming entry point; `adapter_anthropic.go:154-159` becomes real | One-line routing in `handleAnthropicStreamResponse` + `handleAnthropicInternalStreamResponse`; no new file | (b) |
| Correctness (semantics drift) | Lower — matches existing Anthropic event sequence; higher test risk (stateful) | Higher — violates the acceptance gate for the two listed variants; requires explicit fallback scoping | (a) for variants that violate, (b) for those that don't |
| Complexity (LOC estimate) | High — ~300 LOC + state machine + tests | Low — option-flag + test fallback list | (b) |
| Divergence from current behavior | High — new translator; old translator becomes the buffered-mode driver | Low — same code path for translation-required variants, gated by header | (b) |
| Test burden | Very high — see (a1)–(a6) | Low — same buffered tests pass; just gate-flag tests | (b) |
| Operational risk | New code path with non-trivial state machine; risk of subtle ordering bugs | Recursion into existing buffered code; well-understood | (b) |
| **Total** | — | — | **depends on acceptance gate strictness** |

### Recommendation (Q2)

**Pick: Option (b) — default-mode buffers ONLY translation-required variants — IF the acceptance gate is "first content byte forwarded before upstream completion" applied strictly to non-translation paths only.** If the acceptance gate MUST apply uniformly (including translation variants), then Option (a) is required for the internal non-Anthropic → Anthropic streaming path; the external non-Anthropic → Anthropic streaming path could still use (b) ONLY IF we accept a non-uniform contract — the architect should explicitly decide whether this asymmetry is acceptable.

**Reasoning:**
- Today's real-streaming coverage is already 80% of the surface (OpenAI↔OpenAI, Anthropic→Anthropic passthrough, ultimate-model raw passthrough, MCP). The translation variants are the 20% minority.
- Option (a) is a significant new component (300+ LOC stateful translator) with high test burden and meaningful operational risk. The investigation report flags this as "the biggest new component."
- The Anthropic-event sequence is well-defined but complex (11 event types including thinking + tool_use + usage). Stateful incremental translation is a known footgun (tool-call ordering, partial blocks, `[DONE]`-but-partial-content edge cases).
- The user-visible benefit of option (a) for translation variants: first Anthropic `content_block_delta` arrives before `[DONE]`. This is meaningful for long responses, but the cost is high.
- The user-visible cost of option (b): Anthropic clients wait for `[DONE]` before receiving any Anthropic events. Today's behavior; no degradation. The acceptance gate violation is documented as the explicit fallback scope.

**Fallback scoping (precise, for option (b) under uniform acceptance gate violation):**

| Variant | Real-streaming? | Reason |
|---|---|---|
| OpenAI client + OpenAI upstream | ✅ Yes | `streamResult` path; no translation |
| OpenAI client + non-Anthropic external upstream | ✅ Yes | `streamResult` path; no translation |
| Anthropic client + Anthropic-credential upstream | ✅ Yes | `handlePassthroughStreamResponse` (`handler_anthropic.go:1225`); byte-identical passthrough |
| Anthropic client + non-Anthropic external upstream | ❌ Buffered | `handleAnthropicStreamResponse` (`handler_anthropic.go:796`); requires `TranslateBufferedStream` |
| Anthropic client + internal non-Anthropic upstream | ❌ Buffered | `handleAnthropicInternalStreamResponse` (`handler_anthropic.go:719`); requires recorder + `TranslateBufferedStream` |
| Ultimate-model raw passthrough (both paths) | ✅ Yes | `executeExternal` + `executeInternal` write to `w` directly; no buffering |
| Ultimate-model anthropic-wire external | ❓ See note | The ultimate handler writes to `w` directly (raw passthrough); if the ultimate credential is Anthropic, the client sees Anthropic bytes directly — first-byte streaming. ✅ |
| Ultimate-model anthropic-wire internal | ❓ See note | Same as above for the response wire — first-byte streaming. ✅ |

For the ❓ rows: `pkg/ultimatemodel/handler_external.go` is byte-identical raw passthrough; `pkg/ultimatemodel/handler_internal.go` resolves to the typed provider path; both write SSE chunks to `w` as they arrive. These are already real-streaming in the same sense the OpenAI relay is real-streaming. The locked decision is "ultimate-model paths stay raw-passthrough (no change)." Confirm this.

**Assumptions:**
- A1. The investigation report's "OpenAI→Anthropic translation variant" mapping is correct: external = `handleAnthropicStreamResponse`; internal = `handleAnthropicInternalStreamResponse` via the recorder. **Verified** at `handler_anthropic.go:851` (external `TranslateBufferedStream` call) and `:728` (internal `TranslateBufferedStream` call on `recorder.Body.Bytes()`).
- A2. The `thinkingSink` invariant (`internal_handler.go:32-41, 98-100, 379-396`) MUST be preserved under option (a) for the internal non-Anthropic → Anthropic streaming path. Under option (a), the thinking must reach `thinkingSink` (capture for persistence) but MUST NOT reach `w` (the Anthropic client) as a wire-format thinking block. This means option (a) requires a NEW translator variant that emits thinking blocks for the Anthropic client WHEN the source wire is OpenAI-from-internal-handler (which never writes thinking to the recorder thanks to `thinkingSink`), OR the architect explicitly decides that Anthropic clients do receive thinking blocks from internal upstream (which is a behavior change vs the pre-fix fea5874 baseline). **🔴 this needs explicit decision.**
- A3. The pre-fix fea5874 behavior DROPPED thinking entirely from the Anthropic wire. The current code keeps `thinkingSink` capture for persistence. If the architect wants the Anthropic client to receive thinking blocks when the upstream emits them, that's a behavior change with no in-tree test coverage today.

**Reversibility:** both options are reversible. Under (a), the new translator lives in its own file; deleting it reverts to (b). Under (b), the routing flag flip reverts to today's behavior. Option (a) is more reversible because (a) is additive; option (b) requires no architectural change so it's already the floor.

### What the architect should verify before finalizing

- V1. **Acceptance gate strictness.** Confirm whether the gate "header absent ⇒ first content byte forwarded before upstream completion" must apply uniformly across all variants or only to non-translation variants. This is the single decision that picks (a) vs (b).
- V2. **Anthropic client + thinking content.** Under (a), does the Anthropic client receive `content_block_delta` thinking events when the upstream emits them? Today (post-fea5874) the answer is no for the OpenAI→Anthropic translation path; yes for the Anthropic→Anthropic passthrough. Confirm what the new behavior should be.
- V3. **`usage` chunk placement.** OpenAI emits `usage` in a specific chunk (usually the last one before `[DONE]`). The buffered translator emits `message_delta` with `usage.output_tokens` near the end. Under (a), the translator must buffer `message_delta` until EITHER `usage` arrives OR the stream ends (so it can attach final usage). Confirm that Anthropic clients tolerate `message_delta` after `[DONE]` — they don't: `message_stop` is the terminal event and must come last. The streaming translator must emit `message_delta` BEFORE `message_stop`.
- V4. **Tool-call ordering.** OpenAI tool calls may arrive in any order; Anthropic requires `content_block_start` before `content_block_delta`. Under (a), the translator must defer `content_block_stop` for tool_use until either `finish_reason == "tool_calls"` OR the stream ends. The current buffered path emits per-tool-call `content_block_stop` at the end (`:214`). The streaming translator must follow the same convention.
- V5. **Recorder replacement under (a).** If option (a) is chosen AND the architect wants the internal non-Anthropic → Anthropic streaming path to be real-streaming, the recorder pattern must be broken. The cleanest way: replace `flushingResponseRecorder` with a new `chunkedResponseRecorder` that implements `http.Flusher` and forwards each new SSE line to the true `w` (under the `thinkingSink` invariant). Verify this does not break the existing `internal_handler_test.go:397` recorder test.

---

## Q3 — Precedence: opt-in header vs per-model deadline auto-release

### Question

When `X-LLMProxy-Buffer-Response: true` is set on a request AND a model has `ReleaseStreamChunkDeadline` configured, what happens? And vice versa — when the request is in default real-streaming mode AND a model has a deadline configured? The leader suggestion: explicit header wins; in real-streaming mode the deadline is irrelevant because no gate exists.

### Current Patterns (and a critical re-verification finding)

- **`c.cfg.StreamDeadline` is the ACTIVE deadline in the runtime.** Used at `race_coordinator.go:370` (`streamDeadlineTimer := time.NewTimer(c.cfg.StreamDeadline)`), at `handler.go:42, 67` (snapshot fields), and at `race_executor.go:635` (`streamDeadline time.Duration` parameter). This is the GLOBAL field on `ConfigSnapshot`, not the per-model field.
- **`ModelConfig.GetReleaseStreamChunkDeadline()` has ZERO call sites in `pkg/proxy`.** Full-tree grep confirmed. The only references:
  - Declaration: `pkg/models/config.go:127`, getter at `:143-151`.
  - Test: `pkg/models/config_deep_test.go:147-160` (asserts `0 = 0`, `110s = 110s`).
  - DB column: `pkg/store/database/store.go:454, 762, 783, 849, 870, 903, 912, 929, 950, 1018, 1066, 1149, 1163` (reads, writes, migrations).
  - UI wire/CRUD: `pkg/ui/server.go:54, 521, 611, 729` (the API exposes it for read/write).
  - Event type stub: `pkg/events/bus.go:59-60` (`StreamChunkDeadlineEvent`) — declared but never published in production paths.
- **Conclusion: the per-model deadline field is DORMANT today.** It is wired through persistence and UI but the proxy code never reads it. Any "auto-release" behavior the user attributes to it does not currently exist.

This is a critical fact that reframes the question. The user's mental model assumed `ReleaseStreamChunkDeadline` is the active mechanism that auto-flushes the buffer mid-stream; in reality, the active mechanism is `StreamDeadline` (global), and the per-model field is plumbing that was never connected to the relay.

### Options

#### Option P1 — Explicit header wins; deadline unchanged

Spec: under buffered mode (`X-LLMProxy-Buffer-Response: true`), the existing global `StreamDeadline` continues to drive the auto-release machinery at `race_coordinator.go:649-723` (the deadline picker). Under real-streaming mode (header absent), the deadline is irrelevant because the relay forwards chunks as they arrive; no buffering gate exists to "release" against. The per-model `ReleaseStreamChunkDeadline` field is untouched — it remains dormant until someone explicitly decides to wire it.

**Behavior:**
- Header absent + per-model deadline set → real-streaming (per Q1 / Q2); deadline ignored.
- Header present + per-model deadline set → buffered; global `StreamDeadline` fires the auto-release at the deadline (today's behavior).
- Header absent + per-model deadline unset → real-streaming; deadline ignored.
- Header present + per-model deadline unset → buffered; today's default behavior.

#### Option P2 — Per-model deadline is wired under buffered mode

Spec: in buffered mode, the per-model deadline (if set) overrides the global `StreamDeadline` for that model. Otherwise today's behavior. This requires:
- Reading `resolvedModel.GetReleaseStreamChunkDeadline()` at the relay entry (`handler.go:924+` or wherever the buffered path starts).
- Threading the deadline into the coordinator (today the coordinator reads `c.cfg.StreamDeadline` from `ConfigSnapshot` at `race_coordinator.go:370`).
- Adding the `rc.conf.StreamDeadline = max(global, perModel)` logic or a separate field on `ConfigSnapshot` populated at request entry.

This is a NEW wiring job — currently the field is dead. Wiring it is a separate decision from this feature; doing both at once risks scope creep.

#### Option P3 — Per-model deadline is irrelevant; remove the field

Spec: remove `ReleaseStreamChunkDeadline` from `ModelConfig`, from the DB schema, from the UI. Replace the auto-release mechanism with a single global `StreamDeadline`. This is a cleanup decision, not a feature decision.

### Comparison (Q3)

| Criterion | Option P1 | Option P2 | Option P3 | Winner |
|---|---|---|---|---|
| Code paths touched | None (header is the only new contract) | New wiring for per-model deadline in `ConfigSnapshot` + coordinator init | DB migration + UI update + `ModelConfig` field removal | P1 |
| Correctness | Matches leader suggestion | Risk of incorrect override logic | Cleanup; removes dead code | P1 (no risk) |
| Divergence from current behavior | None for buffered path; real-streaming path is new | None for buffered path | Removes operator-visible feature (dead, but UI-exposed) | P1 |
| Scope creep | None | Real — new feature bundled into the streaming work | Tangential cleanup | P1 |
| Test burden | Low — header routing tests only | Medium — per-model override tests + global-vs-per-model arbitration | Medium — DB migration + UI updates | P1 |
| **Total** | — | — | — | **P1** |

### Recommendation (Q3)

**Pick: Option P1 — explicit header wins; deadline machinery unchanged.**

**Reasoning:** the user's mental model conflated two things — the global `StreamDeadline` (which IS active in the coordinator's auto-release path) and the per-model `ReleaseStreamChunkDeadline` (which is dormant). Under the locked decision "real-streaming is the default; existing opt-in preserves today's buffered behavior," there is no design tension because the per-model field is not invoked. P1 is the minimal-change answer: no new wiring, no scope creep, header is the only new contract. The per-model field can be wired (P2) or removed (P3) in a SEPARATE feature if/when someone needs per-model auto-release.

**Spec sentence (precise, for the analysis):**

> The `X-LLMProxy-Buffer-Response: true` request header opts the request into the buffered relay mode. In buffered mode, the relay uses today's behavior end-to-end, including the global `StreamDeadline` auto-release machinery at `race_coordinator.go:649-723`. In real-streaming mode (header absent), chunks are forwarded live as they arrive; no buffering gate exists, so the `StreamDeadline` is irrelevant. The per-model `ReleaseStreamChunkDeadline` field (`pkg/models/config.go:127`) is not currently invoked by the proxy code; it remains dormant under both modes and is OUT OF SCOPE for this feature.

**Truth table:**

| Header | Per-model deadline | Mode | Active deadline | Behavior |
|---|---|---|---|---|
| absent | set or unset | real-streaming | none | live relay; chunks forwarded as they arrive |
| present | set | buffered | none (per-model deadline is dormant) | today's buffered behavior; global `StreamDeadline` may still fire if a fallback path runs the coordinator |
| present | unset | buffered | global `StreamDeadline` | today's buffered behavior |

> **Note:** rows 2 and 3 differ only by whether the dormant field has a non-zero value; behavior is the same because the dormant field is not invoked.

### Cross-cutting invariants (Q3, do not change)

- I1. **`c.cfg.StreamDeadline` remains the global auto-release mechanism** for the buffered coordinator path. Its call sites (`race_coordinator.go:370`, `handler.go:42, 67`, `race_executor.go:635`) are unchanged.
- I2. **The dormant per-model deadline field is not invoked by the proxy.** No new call site to `ModelConfig.GetReleaseStreamChunkDeadline()` is added by this feature.
- I3. **The header is the ONLY new contract** for opting into buffered mode. No new config field, no new wire schema beyond the header.

### What the architect should verify before finalizing

- V1. **Confirm the dormant-field reading.** The analysis assumes the per-model field is not invoked in proxy code. If the architect finds a call site I missed (full-tree grep did not), the spec sentence must be updated.
- V2. **Header parsing.** Confirm the header parses as `X-LLMProxy-Buffer-Response: true|1` (case-insensitive; boolean) — same precedent as `X-Proxy-Interleaved-Thinking` at `handler.go:465` (per the existing comment about case-sensitivity in `handler_helpers.go:113-116`).
- V3. **Header precedence across multiple values.** If the client sends `X-LLMProxy-Buffer-Response: true` and `X-LLMProxy-Buffer-Response: false` in the same request, which wins? Standard Go `r.Header.Get` returns the first value; confirm this is the intended behavior. (Low priority — typical clients send one value.)
- V4. **Global `StreamDeadline` default.** Today's default is set in `config.NewManager` (file:line outside the inspected set; **🔴 uncertain** — verify before locking). The buffered path inherits this default.

---

## Q4 — State field: `rc.streamingNonRetryable` reuse

### Question

The user observed `streamingNonRetryable` is declared at `handler_helpers.go:102` but never set. Reuse it, or introduce a new field?

### Evidence

Full-tree grep (`*.go`) for `streamingNonRetryable`:
- `pkg/proxy/handler_helpers.go:102` — declaration in the `requestContext` struct (lines 99-102):
  ```go
  // Streaming non-retryable state
  // When true, this request will not retry upstream on errors
  // This is set after ReleaseStreamChunkDeadline is reached and buffer is flushed
  streamingNonRetryable bool
  ```
- `pkg/proxy/handler_helpers.go:139` — reset in `reset()`:
  ```go
  rc.streamingNonRetryable = false
  ```

Zero other occurrences. No setter, no getter, no consumer.

**Semantic conflict check.** The comment at lines 99-101 says "set after ReleaseStreamChunkDeadline is reached and buffer is flushed." This names the SAME mechanism (per-model deadline auto-release) that Q3 confirms is dormant. The field was apparently reserved for that feature and never wired.

**What the feature actually needs.** Under Q1 Option A (bypass coordinator + single direct attempt + live relay), a state field is needed to communicate "the relay has already started forwarding; do not retry upstream on error" — i.e. exactly the semantics implied by `streamingNonRetryable`'s name. Under Q2 Option (b) (buffered-translation-only), the same field tells the credential-failover hook "do not attempt another credential; the first byte already left to the client."

This semantic fits `streamingNonRetryable` exactly. There is no conflict — the dormant field's NAME matches the feature's need.

**Reset path.** `reset()` at `handler_helpers.go:139` already resets the field to `false`. The `defer rc.reset()` at `handler.go:398-402` ensures pool-recycled `requestContext`s start fresh.

### Options

#### Option F1 — Reuse `rc.streamingNonRetryable`

Spec: the field is renamed conceptually to "the relay has forwarded at least one byte to the client; do not retry upstream on error." Set at the same site where the relay writes the first chunk to `w` (specifically, just before the first `w.Write(chunk)` in `streamResult` at `handler.go:1066` under Option A, or at the equivalent site in any new real-streaming relay).

Under Option A (Q1) this means: at `handler.go:1066` (the first relay write), set `rc.streamingNonRetryable = true`. Then in the single-attempt wrapper (where the direct upstream attempt is running in its own goroutine), check this flag on error returns: if `rc.streamingNonRetryable`, do not retry / fall back — surface the error to the client and exit.

Under option (b) (Q2): the field is set the moment any chunk is written to the Anthropic client (in the new real-streaming relay paths, if any). For the translation-required variants that STAY buffered, the field is set when the buffered translator writes the first translated Anthropic event to `w`. This is the post-`message_start` boundary.

#### Option F2 — New field

Spec: introduce `rc.relayStarted bool` (or similar), reset in `reset()`, set at the same site. Drawback: leaves `streamingNonRetryable` as another piece of dormant code.

### Comparison (Q4)

| Criterion | Option F1 (reuse) | Option F2 (new field) | Winner |
|---|---|---|---|
| Code paths touched | One-line set; one-line read in retry/failover hook | New field declaration + reset + set + read | F1 |
| Correctness | Same semantics as F2; no conflict (verified above) | Same | tie |
| Divergence from current behavior | None — completes a reserved field's intended purpose | None — adds another field | tie |
| Test burden | Low — wire the field, add test | Same — wire the field, add test | tie |
| Dead code hygiene | Removes dead field | Adds more dead code | F1 |
| **Total** | — | — | **F1** |

### Recommendation (Q4)

**Pick: Option F1 — reuse `rc.streamingNonRetryable`.**

**Reasoning:** the field is declared, reset, and has the right NAME for the intended use. Wiring it does not introduce new state — it completes a reserved slot. There is no semantic conflict between "set after ReleaseStreamChunkDeadline reached and buffer flushed" (the dormant comment) and "set when first byte leaves the relay" (this feature's need): both express "we've committed to streaming; no more retries."

**Set site (proposed):** the first `w.Write(chunk)` inside the `streamResult` relay loop (`handler.go:1066` under Q1 Option A). The relay loop is the single entry point under Option A; setting the flag here is the natural "first byte committed to client" signal.

**Read site (proposed):** the single-attempt wrapper (the new code path under Option A) checks `rc.streamingNonRetryable` on error returns from the direct upstream goroutine. If true, do NOT retry via credFailover; instead emit `sendSSEError` and return. Same logic for the buffered coordinator path: when the relay starts, no further model fallback on stream errors (today's behavior — already implicit; the new field formalizes it).

**Reset site:** already in `reset()` at `handler_helpers.go:139`. No change.

### Cross-cutting invariants (Q4)

- I1. The field is set EXACTLY ONCE per request lifecycle (on first `w.Write`). Setting it on every chunk is wasteful.
- I2. The field is read on error returns from the upstream goroutine. If the upstream succeeds, the field is irrelevant.
- I3. The field does NOT affect non-retry paths (e.g. 400 bad-request from upstream — those are non-retryable regardless). It only gates retry decisions.

### What the architect should verify before finalizing

- V1. **Confirm zero setter calls.** The analysis asserts the field has no setter; verify by reading the full `pkg/proxy/handler.go`, `pkg/proxy/handler_anthropic.go`, and `pkg/proxy/race_*.go` trees. (Grep covers all `.go` files; no setter found.)
- V2. **Race-coordinator consumers.** Today the coordinator does NOT read this field (since it has no setter). The Q1 Option A path bypasses the coordinator; the coordinator path does not need to read it. Confirm the buffered path does not need it either (today's behavior is: once the relay starts via `streamResult`, no retry; this is implicit in `WaitForWinner` + `cancelAllExcept` post-winner).
- V3. **Concurrent read/write.** The relay writes the field on the HTTP handler goroutine; the upstream goroutine reads it on error. If the upstream errors before the relay has written any chunk (i.e. pre-first-byte error), the read may observe `false`. If the relay writes the field before the upstream errors (i.e. mid-stream error after first chunk), the read observes `true`. Both cases are correct. **Verify there is no memory-model concern** — `bool` writes/reads are not necessarily atomic in Go without a synchronization primitive. The simplest fix: set the field under `rc.writeMu` (already held during the first relay write at `handler.go:1064`); read under the same mutex on the error path. Or use `sync/atomic.Bool`. **🔴 this is an architectural micro-decision** — pick one and document it.

---

## Cross-cutting invariants to hold under ANY option

These are the contracts every option must preserve. They are derived from the existing code base, not from new design — they are the floor.

### I-X1. **Thinking bytes never reach `w` from `internal_handler.go`.**

Source: `internal_handler.go:32-41, 98-100, 379-396`. The `thinkingSink` field is OPTIONAL and capture-only. The `case "thinking"` arm writes to `h.thinkingSink` ONLY when non-nil; never to `w`. This invariant survives Option A (bypass coordinator), Option B (warm-up), Option (a) (incremental translator), Option (b) (buffered-translation-only). Any new code that threads the true `w` into `InternalHandler.HandleRequest` MUST preserve this.

- **Verification:** search for any new code that writes to `w` from `case "thinking"` — there must be none.
- **Test:** `internal_handler_test.go:397` uses `flushingResponseRecorder`; that test plus any new recorder-replacement test must assert "thinking not on wire."

### I-X2. **Passive `ExecuteResult` capture.**

Source: `pkg/ultimatemodel/handler.go`, `handler_external.go`, `handler_internal.go`. The ultimate-model paths write raw passthrough bytes to `w` and capture `ExecuteResult{Usage, Content, Thinking, ToolCalls}` passively from SSE chunks (not from the request/response boundary). Any new code under Q1/Q2 that touches ultimate-model paths MUST NOT mutate written bytes (per the locked decision).

- **Verification:** no `buf` allocation or `bytes.Replace` in the ultimate paths.
- **Test:** existing ultimate-model tests (`pkg/ultimatemodel/handler_test.go`).

### I-X3. **Per-chunk transforms stay upstream of the client write.**

Source: `handler.go:1071-1073, 1111-1113, 1137-1139, 1346-1348`. The `extractStreamChunkContent` tap extracts content from each chunk as it is written to `w`. Under any option, this tap MUST fire before the chunk is written — so accumulated UI record parity is preserved.

- **Verification:** every `w.Write(chunk)` in the relay loops is paired with `extractStreamChunkContent(chunk, ...)` in the same code path, under the same mutex.
- **Test:** existing streaming tests in `pkg/proxy/*_test.go` cover the tap.

### I-X4. **UI record parity via the inline content tap.**

Source: same as I-X3. The UI sees `accumulatedResponse`, `accumulatedThinking`, `accumulatedToolCalls`, and the final assistant message (`handler.go:1279-1286`) is built from these. Under real-streaming, the first byte of `accumulatedResponse` arrives on the FIRST relay write, not on `[DONE]`. The UI record will populate incrementally as bytes flow. This is the desired behavior under real-streaming.

- **Verification:** UI record shows partial content during streaming, complete content on `[DONE]`.
- **Test:** existing `race_retry_test.go` tests assert `accumulatedResponse` content; new tests should assert partial-vs-complete under real-streaming.

### I-X5. **Mid-stream error semantics: post-first-content-byte failure = terminal in-band SSE error.**

Source: `handler.go:1149-1178` (OpenAI path), `handler_anthropic.go:1129-1139` (`sendAnthropicSSEError`). After any byte has been forwarded to the client, an upstream error is converted to a wire-format error event in-band; the stream is not closed without an error event; no retry is attempted. This is the "matching today's post-winner semantics" contract. Under any option, this MUST hold.

- **Verification:** the relay loop's `case <-buffer.Err()` (or equivalent under Option A) emits the SSE error event before returning.
- **Test:** existing `stream_buffer_test.go` tests + new tests under Option A for the single-attempt relay.

### I-X6. **`model_credential_selected` asymmetry left as-is.**

Source: locked decision; `pkg/proxy/race_executor.go:executeRequest` is the single source of truth. Ultimate-model paths discard `NewlyBound` (`pkg/ultimatemodel/handler_internal.go:58-61`). Under any option, the asymmetry persists — ultimate paths do NOT publish `model_credential_selected`; race-path requests do.

- **Verification:** no new publisher added under `pkg/ultimatemodel/`.
- **Test:** existing ultimate-model + race tests.

### I-X7. **Pre-preamble `: connected\n\n` already established.**

Source: `handler.go:882-889` (OpenAI), `handler_anthropic.go:801-815` (Anthropic). The preamble is sent BEFORE the coordinator / upstream goroutine starts. Under Option A, the preamble stays; no change. Under Option B, the preamble stays; no change.

- **Verification:** the preamble write happens before the `WaitForWinner` / upstream goroutine call.
- **Test:** existing `race_retry_test.go` tests assert preamble byte sequence.

---

## Trade-offs

### Alternatives considered (cross-question)

1. **Q1 — Option A (bypass coordinator + live relay)** vs **Option B (warm-up window + promote)**. A wins on divergence surface + LOC + test burden; B wins on correctness (closer to current behavior). **Pick A.**
2. **Q2 — Option (a) (new incremental translator)** vs **Option (b) (buffer translation variants)**. (a) requires a stateful translator with high test burden; (b) preserves today's translation paths with one-line routing. **Pick (b)** unless uniform first-byte acceptance gate required.
3. **Q3 — Option P1 (header wins, deadline unchanged)** vs **P2 (wire per-model deadline)** vs **P3 (remove per-model field)**. P1 is no-op; P2 is new feature; P3 is cleanup. **Pick P1.**
4. **Q4 — F1 (reuse `rc.streamingNonRetryable`)** vs **F2 (new field)**. F1 reuses a reserved slot; F2 adds another. **Pick F1.**

### Comparison summary

| Question | Recommendation | Reversibility | New LOC | Test burden |
|---|---|---|---|---|
| Q1 | Option A (bypass) | High | ~150 | Medium |
| Q2 | Option (b) (buffer translation variants) — conditional on gate strictness | High | ~50 (routing) | Low |
| Q3 | P1 (header wins) | High | ~20 (header parse) | Low |
| Q4 | F1 (reuse field) | High | ~10 (set + read) | Low |

### Recommendation aggregate

**Pick: Q1 Option A + Q2 Option (b) [conditional] + Q3 P1 + Q4 F1.**

**Reasoning:**
- The relay loop is already real-streaming-capable (verified at `handler.go:1038-1380`).
- The `: connected\n\n` preamble is already pre-winner (verified at `handler.go:882-889`).
- The OpenAI→OpenAI / external→external streaming paths need only the bypass wiring (Q1).
- The Anthropic translation variants can stay buffered (Q2 option (b)) with documented fallback scope.
- The header is the only new contract (Q3).
- The `streamingNonRetryable` field is a perfect fit (Q4).

**Assumptions (aggregate):**
- AA1. The locked decision "real-streaming is the default" implies the buffered path is the explicit opt-in fallback.
- AA2. Pre-first-byte credential failover is preserved in the Q1 Option A single-attempt path.
- AA3. Model-fallback chain behavior after a real-streaming-attempt failure is the architect's decision (see Q1 V2).
- AA4. The acceptance gate "header absent ⇒ first content byte forwarded before upstream completion" applies uniformly OR with the documented Q2 fallback scope.
- AA5. The per-model `ReleaseStreamChunkDeadline` field remains dormant.
- AA6. The `streamingNonRetryable` field is set under `rc.writeMu` to avoid memory-model concerns.

**Reversibility:** all four picks are reversible. Q1 and Q3 are config-flag-flip reversible; Q2 (b) reverts to today by removing the routing flag; Q4 reverts by removing the set/read lines.

## Scalability

### Growth assumptions

- **Volume:** the proxy is a single-binary with stdlib `http.ServeMux`. Throughput is bounded by the upstream providers, not the proxy.
- **Stream length:** Anthropic events can be 1k–100k tokens; tool-call responses with 10+ tool calls and 100+ deltas are common. Buffering translates to 5–500ms extra latency for first byte in today's buffered mode; real-streaming reduces this to upstream-RTT.
- **Concurrency:** each request is one HTTP handler goroutine + one upstream goroutine (race-coordinator) + heartbeat goroutine. Real-streaming under Q1 Option A: HTTP handler + single upstream + heartbeat. Net reduction in goroutines per request (no coordinator manager goroutine).

### Current bottlenecks

- **First-byte latency under buffered mode:** the user waits for `[DONE]` (or `StreamDeadline`) before seeing any content. On slow upstreams, this can be 5–30 seconds for long responses. **This is the bottleneck the feature removes.**
- **Heartbeat scheduler:** under Q1 Option A, the heartbeat remains; no change.
- **Race coordinator overhead:** coordinator spawns + manages N attempts; for N=1 (real-streaming default), the coordinator is pure overhead. Q1 Option A removes this.

### Scaling characteristics

- **Vertical vs horizontal:** no change. Single-binary, stateless per request.
- **Stateless vs stateful:** per-request state (the `requestContext` and the `streamBuffer`) is GC'd on completion. No change.
- **Sync vs async:** the relay loop is sync per request; multiple requests scale horizontally via Go's HTTP server. No change.

### Scaling cliffs

- **Per-request memory under buffered mode:** `streamBuffer` allocates 5MB default (`stream_buffer.go:93`) per upstream attempt. Under Q1 Option A with N=1, the buffer is 5MB. Under today's coordinator with N=2, it's 10MB. **Q1 saves 5MB per request.**
- **Heartbeat goroutines:** one per streaming request. Today and after: same.

## Technical Debt

### Items affecting this analysis

| # | Debt Item | Impact on Recommendation | Severity | File:Line |
|---|-----------|--------------------------|----------|-----------|
| 1 | `streamingNonRetryable` field declared-and-reset-but-never-set | Low — Q4 F1 reuses it | Low | `handler_helpers.go:99-102, 139` |
| 2 | `ReleaseStreamChunkDeadline` field is dormant (configured but never invoked) | Low — Q3 P1 leaves it dormant; explicitly out of scope | Medium | `pkg/models/config.go:127, 143-151` |
| 3 | `StreamChunkDeadlineEvent` type declared but never published | Low — no impact | Low | `pkg/events/bus.go:59-60` |
| 4 | `AnthropicAdapter.WriteStreamEvent` returns error "streaming requires buffered translation" | High — Q2 must address this for option (a) | Medium | `pkg/proxy/adapter_anthropic.go:154-159` |
| 5 | `flushingResponseRecorder.Flush()` is no-op | Medium — Q2 option (a) requires recorder redesign for internal non-Anthropic → Anthropic streaming | Medium | `pkg/proxy/handler_anthropic.go:591-598` |
| 6 | `c.cfg.StreamDeadline` (global) and per-model `ReleaseStreamChunkDeadline` exist as parallel mechanisms (one wired, one dormant) | Low — Q3 P1 keeps the dormant one dormant | Low | `race_coordinator.go:370` + `pkg/models/config.go:127` |

### Items NOT affecting this analysis

- **Ultimate-model raw passthrough paths** — locked decision is "no change." Out of scope.
- **`model_credential_selected` asymmetry** — locked decision is "left as-is." Out of scope.
- **`SetCredentialFailover` / `credEngine` injection plumbing** — locked decision is "unchanged." Out of scope.
- **Anthropic passthrough `handlePassthroughStreamResponse`** — already real-streaming; no change needed.
- **`pkg/ui/frontend` type-safety debt** — orthogonal; 30 `tsc --noEmit` errors exist but this feature touches only the API contract (no UI changes).

### Recommended paydown

In priority order, only if they affect this analysis:

1. **Q2 option (a) requires** redesigning `flushingResponseRecorder` and adding `StreamChunkDeadlineEvent` publisher. If option (a) is chosen, these are in-scope.
2. **Q4** requires setting `streamingNonRetryable` under `rc.writeMu` (memory-model). Trivial.
3. **Q3** does not require paydown — the dormant field stays dormant.

## Open Questions

- **OQ1.** Does model-fallback chain continue after a real-streaming-attempt failure (pre-first-byte)? E.g. request comes in for model X with fallback chain [X, Y, Z]; real-streaming attempt on X fails pre-first-byte → coordinator picks up from Y (real-streaming attempt on Y) → buffered legacy on Z if Y also fails? Or: real-streaming attempt fails → entire request falls back to today's buffered coordinator path with the same model X? **Architect's decision.**
- **OQ2.** What is the default behavior for `X-LLMProxy-Buffer-Response: false`? Should `false` be a valid opt-out, or should only `true` (or `1`) trigger buffered mode? **Architect's decision.**
- **OQ3.** Q2 option (a) introduces a stateful incremental translator. Where does the thinking block emission live? Does the Anthropic client receive thinking blocks when the upstream emits them, OR does the existing `thinkingSink` invariant suppress them at the wire? **Architect's decision.** Today's behavior for the Anthropic→Anthropic passthrough: thinking IS on the wire. Today's behavior for OpenAI→Anthropic translation: thinking is NOT on the wire (post-fea5874 fix). The architect must decide whether option (a) preserves this asymmetry or unifies it.
- **OQ4.** Q1 Option A's single-attempt path: does pre-first-byte failure trigger the buffered coordinator path (model fallback chain continues) OR terminate the request (single credential, single attempt)? **Architect's decision.** The locked decision does not specify.
- **OQ5.** The `c.cfg.StreamDeadline` default value — full-tree grep did not surface the config initializer. **🔴 unknown.** Architect must verify the default value to write the truth-table header column "global `StreamDeadline`."

## References

- **Investigation report** (primary evidence source, file:line-cited) — provided in task context.
- **Locked decisions** — provided in task context.
- `cmd/main.go:218` — `recoveryMiddleware` registration for UI mux.
- `pkg/proxy/handler.go:378-979` — OpenAI handler entry + race coordinator wiring.
- `pkg/proxy/handler.go:1038-1380` — `streamResult` relay loop (live relay pattern under any option).
- `pkg/proxy/handler.go:882-889` — `: connected\n\n` preamble (already pre-winner).
- `pkg/proxy/race_coordinator.go:238-256` — coordinator Start + spawn main.
- `pkg/proxy/race_coordinator.go:370-446` — manage loop with deadline timers + winner eligibility (`req.IsCompleted()` gate).
- `pkg/proxy/race_coordinator.go:649-723` — `handleStreamingDeadline` (the partial-release machinery; deadline-handoff gap is the `streamCh` close at `:693`).
- `pkg/proxy/handler_helpers.go:99-102, 139` — `rc.streamingNonRetryable` declaration + reset.
- `pkg/proxy/handler_anthropic.go:591-598` — `flushingResponseRecorder` (no-op `Flush`).
- `pkg/proxy/handler_anthropic.go:600-761` — `doAnthropicInternalRequest` + `handleAnthropicInternalStreamResponse` (recorder path; `TranslateBufferedStream` at `:728`).
- `pkg/proxy/handler_anthropic.go:795-887` — `handleAnthropicStreamResponse` (external non-Anthropic → Anthropic; `TranslateBufferedStream` at `:851`).
- `pkg/proxy/handler_anthropic.go:1225-1295` — `handlePassthroughStreamResponse` (in-tree real-streaming template).
- `pkg/proxy/internal_handler.go:32-41, 98-100, 379-396` — `thinkingSink` invariant + `case "thinking"` enforcement.
- `pkg/proxy/translator/stream.go:19-244` — `TranslateBufferedStream` + `extractChunkContent` + `generateAnthropicEvents` (the buffered translator).
- `pkg/proxy/adapter_anthropic.go:154-159` — `WriteStreamEvent` refusal ("streaming requires buffered translation").
- `pkg/models/config.go:123-150` — `ReleaseStreamChunkDeadline` field + getter.
- `pkg/models/config.go:143-151` — `GetReleaseStreamChunkDeadline` (dormant).
- `pkg/store/database/store.go:454, 1149, 1163` — DB persistence of `ReleaseStreamChunkDeadline`.
- `pkg/ui/server.go:54, 521, 611, 729` — UI/API exposure of `ReleaseStreamChunkDeadline`.
- `pkg/events/bus.go:59-60` — `StreamChunkDeadlineEvent` type stub.
- `pkg/ultimatemodel/handler_external.go:38` — "no retry, no fallback, no buffering, no loop detection" comment.
- `pkg/ultimatemodel/handler_internal.go:30` — same comment.