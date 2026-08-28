# 2026-08-27 — Integration review: real-streaming-default (9148de3..193bbef)

**Verdict: APPROVED** — deep-review council (governor 34121220, 2 councilors), 0 CRITICAL / 1 MINOR / 5 DEFERRED.

## Confirmed pinned forms — do NOT re-flag in Phase 5
- Strict winner predicate is a REPLACE (`race_coordinator.go:472-480`); buffered branch keeps `IsCompleted() && err==nil` verbatim.
- C2 unlock-before-cancel (`race_coordinator.go:779` unlock → `:780` cancelAll); old `:651` defer removed; all four deadline branches clean.
- M3 `liveFirstByteGate := !rc.bufferMode && rc.isStream` (`handler.go:944/946`); non-stream deadline path untouched.
- Prune gated buffered-only at all 5 handler sites (`handler.go:1082/:1122/:1167/:1238/:1357`) + executor estimators (`race_executor.go:938/:1649`).
- `ultimatemodel.ExecuteOptions{BufferMode}` wired at `handler.go:733-734`; both ultimate triggers (force + schedule) funnel through the single call site.
- `models/config.go:127` DORMANT comment SURVIVED at HEAD — no re-land item.
- Header truth table exact (`handler_functions.go:173-205`); passthrough mode-agnostic; /v1/models + stream=false no-ops.

## Open items carried into Phase 5
- **MIN-1** plan bookkeeping: `race_coordinator_credfailover_test.go` changed beyond the "2-line legacy" expectation (~8 modified lines + ~250 new L7 battery lines, `:702-900`) — constructor-arity forced, semantically neutral; correct the plan/PR record.
- DEFERRED: multi-value `X-LLMProxy-Buffer-Response` = silent first-wins (`handler_functions.go:178`) — document or switch to last-wins.
- DEFERRED: `streamingNonRetryable` write-only, no production reader — add consumer or invariant comment.
- DEFERRED (pre-existing): ultimate heartbeat starts before SSE headers are set (same at baseline `9148de3^`).
- DEFERRED (pre-existing): `convertProvidersToolCallsToMapList` renumbers indices by loop counter, not `tc.Index` (`internal_handler.go:967`) — breaks only under sparse upstream indices.
- Contextual: no usage extraction on Anthropic paths in either mode (pre-existing gap, trivially parity-equivalent).

## Review-pattern note
Council re-partitioned requester's 4-way split → 2 (only 2 canonical models in the allowed set). Councilor-coding ran `go build`/`go vet`/`go test -race` on modified packages (build-cache only, no repo mutation) — evidence upgraded to verified-green. Tree HEAD during review was `b3ecded` (parallel fixture-race fix); source verdict reflects `193bbef`.
