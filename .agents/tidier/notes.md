# Tidier Notes — llm-supervisor-proxy

## Review 001 — minimax-reasoning-details (2026-08-19)
- Diff: ac877ea..54f6ea3, 30 files, ~7.1k insertions. Iteration 001/3.
- Dispatch: 2 workers — tidier-readable-code + tidier-static-hygiene (robustness skipped: no error-handling focus).
- Verdict: Needs Work — 0 High / 6 Medium / 8 Low.
- Top items: stale H2-dedup comments vs W6 single-winner (minimax_stream.go:157-159, openai.go:766-768); wire-literal duplication (handler_internal.go:104-106, openai.go:739,898); convertRequest ~30-line dup ×2 (race_executor.go:836-864, handler_internal.go:525-560); 2 file-hygiene threshold comments (minimax_stream_test.go 1302L, race_coordinator.go 1006L).
- Known deferred by decision (NEVER report): extractReasoningDetailsFromRawData dead walker; "Finding 4 helper". Out of scope: pkg/auth/token.go + pkg/mcp/* gofmt debt.
- Follow-up beyond diff: translator package doc duplicated across pre-existing tools.go/types.go/request.go (new files model the correct banner pattern).
