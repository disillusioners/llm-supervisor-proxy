# Tidier Notes — llm-supervisor-proxy

## Review 001 — minimax-reasoning-details (2026-08-19)
- Diff: ac877ea..54f6ea3, 30 files, ~7.1k insertions. Iteration 001/3.
- Dispatch: 2 workers — tidier-readable-code + tidier-static-hygiene (robustness skipped: no error-handling focus).
- Verdict: Needs Work — 0 High / 6 Medium / 8 Low.
- Top items: stale H2-dedup comments vs W6 single-winner (minimax_stream.go:157-159, openai.go:766-768); wire-literal duplication (handler_internal.go:104-106, openai.go:739,898); convertRequest ~30-line dup ×2 (race_executor.go:836-864, handler_internal.go:525-560); 2 file-hygiene threshold comments (minimax_stream_test.go 1302L, race_coordinator.go 1006L).
- Known deferred by decision (NEVER report): extractReasoningDetailsFromRawData dead walker; "Finding 4 helper". Out of scope: pkg/auth/token.go + pkg/mcp/* gofmt debt.
- Follow-up beyond diff: translator package doc duplicated across pre-existing tools.go/types.go/request.go (new files model the correct banner pattern).

## Review 002 — ultimate-model-trigger-schedule (2026-08-26)
- Diff: f2fd1cf..d088c0b, 25 files, +868/−787. Iteration 001/3. Post-reviewer, pre-merge pass.
- Dispatch: 3 workers (readable + hygiene + robustness) + 1 verification round to readable worker.
- Verdict: Needs Work — 1 High / 5 Medium / 10 Low.
- High: docs/ultimate-model-design.md:150-151 stale `Remove` semantics (failure no longer removes hash).
- ⚠️ REFUTED finding (never re-report): test_mock_ultimate_model.sh:411 lowercase "X-Llmproxy" is CORRECT — Go net/http canonicalizes header keys (X-LLMProxy → X-Llmproxy on wire); commit 23eff70 documents the deliberate canonical casing. Two workers independently mis-derived casing from source literals; empirical scratch-program + commit-message evidence settled it. Always verify wire casing empirically in Go, never from Header.Set literals.
- Known deferred by decision (NEVER report): current_retry/max_retries payload keys (wire compat, documented at publish sites); triggerAttempts var-vs-const (plan-specified); race-package upstreamRequest.MarkFailed (untouched by design); pre-existing tsc errors; Test 8 runtime.
- Follow-up beyond diff: stale frontend bundle pkg/ui/static/assets/index-BJE2UMhG.js contains old "Max retries exceeded"/"ULTIMATE_RETRY_EXHAUSTED" strings — needs rebuild before merge.
