# Tracking: Real-Streaming Default + Buffered Opt-In (X-LLMProxy-Buffer-Response)

Plan: .agents/shared/planning/real-streaming-default/plan.md (supporting: plan-overview.md in same directory)

## Iteration 001 — 2026-08-27 — REJECTED

Workers dispatched (plan-approval ×3, section-partitioned, cold context):
- approve-worker-p12 (e134f4d9-b37e-4317-b6c3-77db434a7d63), scope §§1-4 + Phases 1-2: REJECTED — 1 blocking
- approve-worker-p34 (0282cec1-d8ae-47c7-8690-fd08bd35d884), scope Q2 + Phases 3-4: REJECTED — 1 blocking
- approve-worker-p5 (4fd5037f-0063-4273-989f-9a5a6c78f0bf), scope Phase 5 + §§6-12: APPROVED — 0 blocking, 6 notes

### Blocking issues carried into verdict

1. **Phase 1 parser sketch self-contradiction** — `parseBufferResponseHeader` sketch switches on `r.Header.Get` value; Go returns `""` for BOTH header-absent and header-empty, and the sketch returns `true` for `""` → absent header (the new default case) maps to BUFFERED, inverting the feature. Contradicts the plan's own L1 truth table and Phase 1 test strategy. Refs: Phase 1 File 1 "What" (~L471), task 1.1 design notes (~L480), L1 truth table (~L165), test strategy (~L495).
   Fix: presence detection via `r.Header.Values(...)` / direct map access `r.Header["X-LLMProxy-Buffer-Response"]`; parser must distinguish absent (→ real streaming) from present-empty (→ buffered per L1 truth table).

2. **Phase 4 unserialized heartbeat goroutine vs live-mode relay writes** — `Execute` starts `go startSSEHeartbeat(w, …)` (handler.go:720-726); `sendHeartbeat` (pkg/ultimatemodel/heartbeat.go:21+) writes to `w` with NO mutex; live-mode per-chunk `w.Write+flusher.Flush` races it for the whole stream → concurrent http.ResponseWriter writes (data race per net/http contract; SSE framing corruption possible). Plan never mentions pkg/ultimatemodel/heartbeat.go; §8.1 heartbeat row cites only the writeMu-guarded proxy-side heartbeat (pkg/proxy/heartbeat.go:27,74-75). Refs: Phase 4 risks (~L974-982), Phase 4 Files row 1, §8.1 (~L1160).
   Fix: pin a mitigation in the plan (mirror proxy-side `writeMu` discipline or equivalent serialization) and add a `-race` test with shortened heartbeat interval to Phase 4's test strategy.

### Notable non-blocking (for revision round)
- Phase 3 exit criterion 6 fixture/dataflow muddle — as written the gate can be satisfied vacuously (recorded Anthropic SSE is sequential, not interleaved; translator consumes OpenAI chunks, not Anthropic SSE). Suggested: synthesize interleaved OpenAI-chunk fixture → translator → Anthropic SDK parser.
- Internal-variant mid-stream error: Finalize wiring unpinned (internal_handler.go:477-480 returns with no Finalize site).
- `ParityVsBatch` "event-SET equality" undefined (batch=1 aggregated delta vs incremental=N deltas).
- Citation fixes: §12 `pkg/token/prompts.go` → `pkg/proxy/token/prompts.go:57-114`; Phase 4 "internal non-stream :830-840" → `handler_internal.go:313`; M7 `*ProviderError` mechanism wording; executor estimators :939/:1650 → :938/:1649.
- Phase 5: rerun-on-flake CI policy for first-byte timing test (prefer event-based assertion); parity-failure bisection procedure; 6-path rollout order; §10 #11 gate-form assumption tracking.
- Phase 2: pin `isStream` threading option (recommend computing `liveFirstByteGate` in handler.go before constructor); `rawChunks` dead-accumulator disposition (task 4.3); live-entry `: connected` preamble pin.

Status: IN_PROGRESS — iteration 002 pending plan revision.

## Iteration 002 — 2026-08-27 — APPROVED (final)

Workers dispatched (plan-approval ×2, cold context, no rejection history shared):
- approve-plan-doc (c67c357a-f733-4187-9800-28d87bc0320d), whole plan document-level: APPROVED — 0 blocking, 8 notes
- approve-plan-code (bbda3587-6eb0-4706-b4f4-6917f08b363b), technical claims vs source: APPROVED — 0 blocking, 14 notes

### Reconciliation vs iteration 001
- Blocking 1 (Phase 1 parser default inversion): RESOLVED — cold worker verified presence-aware `r.Header.Values` + 4-row truth table as the only correct implementation under Go net/http semantics, applied uniformly.
- Blocking 2 (ultimate-path heartbeat write race): RESOLVED — cold worker verified per-request write-mutex discipline pinned on both ultimate live paths, with `-race` test (shortened heartbeat interval); race diagnosis confirmed at pkg/ultimatemodel/heartbeat.go:47-69.
- Round-1 non-blocking items confirmed addressed in revision: synthesized interleaved fixture at Phase 3 exit-criterion 6; ParityVsBatch event-SET equality defined (NB3); event-based first-byte assertion (NB5) replaces wall-clock threshold; Phase 5 6-path rollout order pinned; C2 deadlock sequencing pinned (capture under lock → release → cancel).

### Notes carried forward (non-blocking, for implementer)
- Citation drifts to correct pre-merge: plan.md:640 constructor vicinity (actual `newRaceCoordinatorWithEvents` at race_coordinator.go:183-220, cited range is `repairMalformedToolCalls`); Phase 4 heartbeat spawn cite (spawn is inside Execute, ultimatemodel/handler.go:725, not handler.go:720-726); §12 `extractStreamChunkContent` undercount (4 sites incl. :1348); §7 row 19 test line numbers unverified.
- Deliberate wire-shape change (§8.2): internal-Anthropic live mode emits `thinking_delta` blocks absent in buffered mode — recommend surfacing in docs/real-streaming-default.md for downstream clients.
- C1 usage parity: `captureBuf` must append in lockstep with the per-chunk wire write; no-usage-chunk fixture is mandatory (REQUIRED parity).
- Residual unverified at plan level, gated by implementation exit criteria: Anthropic SDK interleaved-block tolerance (Phase 3 exit-criterion 6); golden-file byte stability (Phase 5); error-path `Finalize()` wire byte order (task 3.4).

Status: APPROVED — iteration 002 final.

## Tracked issue — gate-form safety re-review at merge time

**Source:** Plan §10 #11 (architect flag for implementation) + plan task 5.12.

**Issue:** The resilience review's safety conclusions depend on the Phase-2
gate-form landing in the EXISTING manage-loop ticker arm
(`race_coordinator.go:406-435`) with `onceStream` close semantics at
`race_coordinator.go:431` and the deadline guard's reuse of the
`:653-655` winner-nil check. This is the only place the safety story is
told in the plan (§10 #11, §12 #11, Phase 2 CONSTRAINTS). A later
refactor that changes the form (e.g., per-attempt atomic first-byte
flag) would invalidate the traced hazards in
`architecture-recommendation.md` §3 and §7 item 4.

**Action (tracked issue, not a code task):**
Re-verify the pinned gate form on merged code AT MERGE TIME:

1. `race_coordinator.go:406-435` — manage-loop ticker arm; the live-mode
   predicate swap lands HERE (not as a per-attempt atomic flag).
2. `race_coordinator.go:431` — `onceStream` close semantics preserved
   (single-close guard via `sync.Once` or equivalent).
3. `race_coordinator.go:653-655` — deadline guard's `c.winner != nil`
   early-return path reused.

**Acceptance:** confirm all three sites match the Phase-2 CONSTRAINTS
pins; document any deviation + re-trace the hazards before merge.

**Status:** OPEN. Tied to merge of `feature/real-streaming-default`
@ b3ecded (or successor).
