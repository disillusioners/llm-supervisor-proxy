# Plan Overview: Real-Streaming Default (with `X-LLMProxy-Buffer-Response` opt-in)

Date: 2026-08-27 (amended)
Branch: `feature/real-streaming-default` @ 9842c77
Status: **Ready for Implementation** — plan review verdict REJECTED-narrowly → narrow amendment pass applied (C2 deadlock pin, C1 usage-parity pin, M1–M7/#8/#9). Direction, R1-variant gate, F1/F2 corrections, and leader-locked constraints confirmed faithful by review.

## Artifacts

| File | Author | Role |
|------|--------|------|
| [`plan.md`](./plan.md) | plan-creation worker, architect-resolved, narrow-amended | PRIMARY artifact — 5 phases, master test-flip inventory, acceptance criteria, pinned forms (1,315 lines) |
| [`technical-analysis.md`](./technical-analysis.md) | technical-analysis worker | 4 open design questions deep-dive + cross-cutting invariants + re-verification findings (654 lines) |
| `plan-overview.md` | planner (dispatcher) | THIS FILE — synthesis only; defers to plan.md on phase shape and pinned forms |

**Amendment history (chronological, all blockquotes preserved in plan.md):**
1. Worker addendum — design-worker corrections integrated (L1 truth table, L5 dormancy correction, Q2 divergence, spec points i/ii).
2. Architect resolution — R1 resolved as **R1-VARIANT** (gate redefinition; coordinator retained); F1/F2 race-path estimator fixes; Q4 → distinct flags; task 2.4.1, §10 assumption #11.
3. Narrow amendment pass (review REJECTED-narrowly) — C2 deadlock pin, C1 usage-parity pin, M1–M7, #8 constraints block, #9 double-save test; two deferred items applied in passing (§7 row 8 real file names; Phase 3 task 3.9 stale-comment update).

## Objective

Default streaming becomes REAL streaming — chunks forwarded to the client as they arrive, no full-response accumulation. Current fully-buffered supervised behavior becomes opt-in via request header `X-LLMProxy-Buffer-Response` ("true"/"1"/"yes"/"on"/empty ⇒ buffered, bit-for-bit current behavior; absent or "false"/"0"/"no"/"off" ⇒ real streaming). Accepted feature losses in real-streaming mode are documented degradations.

## Phase Summary

| Phase | Name | Objective | Depends on |
|-------|------|-----------|------------|
| 1 | Mode Foundation | Header parsing (locked 10-variant truth table, `parseBufferResponseHeader`), NEW `rc.bufferMode` field (distinct from `streamingNonRetryable` — Q4 resolved), precedence rule, unit tests. No wire behavior change. | — |
| 2 | Race Coordinator Gate Redefinition (R1-variant) | **No `streamLiveRelay`** — the winner predicate is swapped inside the existing manage-loop ticker arm (`race_coordinator.go:406-435`): `req.GetError()==nil && req.GetBuffer().TotalLen()>0` behind `liveFirstByteGate = !bufferMode && isStream`; `onceStream` close (`:431`), winner-nil reuse (`:653-655`), cancel-and-exit (`:440-446`) all reused verbatim. Live-mode 0-byte deadline guard (`:661-722`) with the **C2-pinned lock-aware cancellation** (capture under `c.mu` → release → cancel; precedent `:441-446`). Executor predicate-hazard fix (`isStreamErrorChunk` before Add loop). Prune/raw-capture gating (R2 fix, single-switch). `streamingNonRetryable.Store(true)` on first relay write. Buffered mode executes today's code verbatim; non-stream deadline behavior unchanged (M3/E2). | 1 |
| 3 | Anthropic Path — Incremental Streaming Translator | NEW `IncrementalStreamTranslator` (`translator/incremental_stream.go`); true `w` threaded into `InternalHandler.HandleRequest` replacing `flushingResponseRecorder` in live mode; translator output REPLACES OpenAI-chunk writes (M5); `thinkingSink` invariant preserved; sequential fallback loop stops after `headersSent` in live mode; SDK-interleave verification is an EXIT CRITERION (M6); stale invariant comment update (task 3.9). Passthrough variant untouched. | 1 (parallel with 2, 4) |
| 4 | Ultimate Model Paths Real Streaming | External write-through + internal per-event flush, each retaining a **C1 capture-side accumulator** so the tokenizer-fallback estimators (`handler_external.go:483`, `handler_internal.go:710`) see full completion text in live mode — Usage parity, NOT a degradation; mandatory no-usage-chunk fixture in `TestUltimateCapture_LiveMode_IdenticalToBuffered`. Passive `ExecuteResult` capture preserved; H8 unchanged in buffered mode; trigger counting pre-stream. | 1 (parallel with 2, 3) |
| 5 | Parity + Acceptance Harness | Golden-file parity matrix (6 paths × content variants, byte-identical buffered mode); first-byte-before-completion timing suite (deterministic mock upstream); events/usage/bufferstore parity (incl. `bufferStore.Save` double-save test); docs + rollback procedure. | 2, 3, 4 |

Phases 2/3/4 are file-disjoint and parallelize after Phase 1.

## Design Questions — RESOLVED (formerly open)

| Q | Resolution | Where pinned |
|---|-----------|--------------|
| Q1 OpenAI seam | **R1-VARIANT** (architect): Option A *realized as* coordinator gate redefinition — coordinator retained, winner = first forwardable byte (`TotalLen()>0 && err==nil`), everything downstream reused. No parallel-attempt changes; pre-first-byte spawn semantics untouched. | plan.md Phase 2 + Phase-2 CONSTRAINTS block; §10 #11 |
| Q2 Anthropic translation | **(a) NEW incremental translator** as primary — live mode emits `thinking_delta` on the wire (deliberate, documented wire-shape change for the internal-non-Anthropic variant); **(b)** buffer-translation-only-variants remains the architect-selectable fallback if Phase 3 slips (per-variant acceptance-gate table in plan §4 Q2). | plan.md §4 Q2 |
| Q3 Header vs deadline | **Header wins.** Corrected premise: per-model `ReleaseStreamChunkDeadline` is DORMANT; the active deadline is the global `StreamDeadline` **inside the coordinator — which live mode now KEEPS** with redefined semantics: 0-byte at deadline ⇒ in-band SSE error (`GetStreamDeadlineError` + pinned cancellation); any byte ⇒ first-byte winner predicate. Buffered-mode deadline behavior byte-identical; non-stream deadline behavior unchanged. | plan.md Phase 2 task 2.2 + M3 |
| Q4 State field | **Distinct flags**: NEW `rc.bufferMode` (mode selection, Phase 1) + `rc.streamingNonRetryable` repurposed for its reserved intent (`Store(true)` on first successful relay write, under `rc.writeMu`). Different lifecycles. | plan.md Phase 1/2 (M2) |

## Former Reconciliation Items — RESOLVED

- **R1** (single-attempt vs gate retention): resolved as R1-VARIANT above. Phase 2 = gate redefinition; acceptance gate (first content byte before upstream completion) satisfiable via the predicate swap.
- **R2** (tokenizer estimator): resolved by F2 (race path — Prune/raw-capture gating keeps raw bytes available to executor-side fallback estimators) and **C1** (ultimate paths — capture-side accumulator + mandatory no-usage-chunk regression fixture). Estimator = parity in all modes; **not** an accepted degradation anywhere.

## Pinned Forms (developer must not deviate)

1. **C2 cancellation:** capture state under `c.mu` → perform `:712-721` writes → `c.mu.Unlock()` → then cancel. Never `cancelAll()`/`req.Cancel()` while holding `c.mu` (re-entrant deadlock via `:650-651`/`:760`). Inline-under-lock form explicitly NOT pinned.
2. **Gate form (#8):** ticker arm `:406-435` + `onceStream` `:431` + winner-nil `:653-655`. No per-attempt atomic first-byte flag.
3. **M3 guard scope:** `liveFirstByteGate = !bufferMode && isStream` — never `!bufferMode` alone (non-stream deadline behavior must not change; regression test pins it).
4. **C1 accumulator:** ultimate live mode retains capture-side accumulation feeding the estimators; wire-side loop goes per-chunk.

## Top Risks (post-amendment)

1. Live-mode deadline-guard edge (byte arriving between ticker check and deadline fire) — analyzed idempotent in task 2.2 design notes; covered by boundary tests.
2. New incremental translator emission order vs Anthropic SDK clients — pinned by Phase 3 exit-criterion item 6 (recorded real-stream interleave fixture).
3. Test flips under the new default — master inventory (plan §7, 18 rows, real file names verified).
4. First-byte timing flakiness on CI — generous budgets, rerun-on-flake scoped to the harness.
5. Golden-file drift (timestamps/ids) — strip volatile fields before byte comparison.

## Acceptance Criteria (summary — full detail in plan.md Phase 5)

- **PARITY:** with `X-LLMProxy-Buffer-Response` present, byte-identical wire output vs baseline `9842c77` across all 6 streaming paths × content variants (text/thinking/tool_call/usage), plus identical events, UI records, usage metering (incl. estimator parity fixtures), bufferstore dumps.
- **REAL STREAMING:** header absent ⇒ first content byte on the client wire before upstream completion, deterministically tested via mock upstream with inter-chunk delays + client-side read timing.
