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

## Review 003 — db-cache-layer (2026-08-28)
- Diff: 68dbe63..793d094, 27 files, +6704/−3. Iteration 001/3. Post-reviewer (APPROVED 0 critical) maintainability/style pass.
- Dispatch: 3 workers (readable + hygiene + robustness), all reported. No gaps.
- Verdict: Needs Work — 2 High / 17 Medium / 12 Low.
- High: neg-cache lookup pattern dup 4× (models.go:347-357/377-389/523-532/571-580); reconciler goroutine lacks panic-recovery vs engine.go:143-148 precedent (models.go:262-337).
- Merges (4): ErrCredentialMissing dead (w1+w2); CachedTokenStore.Healthy() dead (w1 Med + w2 Low); gofmt EOF trailing blanks in handler_functions.go + handler_anthropic.go (w1 Med + w2 Low); twin now() helpers (w1+w2).
- Dispatcher correction: worker-3's reconciler-panic impact narrative was wrong mechanism — unrecovered goroutine panic kills the whole PROCESS in Go, not just the goroutine ("silently frozen cache" impossible). Finding + fix unchanged (mirror engine.go E-4 recover); corrected in report.
- Behavior-change guard: json.Unmarshal error propagation (store.go:807-813) and "surface ErrCredentialMissing from GetCredential" are REPORT-ONLY (caller mandated no behavior changes); mechanical options = mirror line-784 pattern needs ruling / deletion.
- Tooling: go vet clean; go test ./pkg/modelscache/ -race -count=1 PASS (3.756s); gofmt drift introduced ONLY at EOF of pkg/proxy/handler_functions.go + handler_anthropic.go (modelscache/store/main clean; handler.go:129 + handler_anthropic.go:1915 drift is pre-existing).
- Never re-report (this project): Phase 2 backlog (typed PG sentinels, reconciler backoff, pointer-handoff copies, []byte wipe); pre-existing drift in untouched files; doc nits listed in planning docs.

## Review 003 — verification (2026-08-28, iteration 002/3)
- Commit 42d98f3 (20 files, +359/−318). 3 verification workers (same skill-per-worker mapping), all reported.
- Verdict: PASS — 2/2 High, 17/17 Medium closed. Behavior preservation double-verified: SQL byte-identity ×2 independent methods (string reconstruction + Go oracle program); sentinel strings + 503 bodies byte-identical; CreateToken clamp-drift deliberately PRESERVED and documented (tokens.go:113-116); magnitudes 60s/60s/24h/5s/6s intact.
- models.go corruption audit CLEAN (mid-session tail corruption was repaired correctly): 779L, 33 funcs, 4 section banners, no orphan/dup blocks; build/vet green; race tests green (3.78s).
- Open (all 🟢, → trivial follow-up or Phase-2 backlog): outage_test.go:298 "failsafe tests" stale comment (NEW, 1-word fix); skipped optional Lows — sync.Once no-op (tokens.go:89-91), now() twins (models.go:227/tokens.go:82), boundary-gate dup (handler_functions.go:144/handler_anthropic.go:166), strictSource ctor doc sentence; ValidateToken 74L (TOCTOU block extractable → ~50L); handler.go:126-130 pre-existing godoc drift (blame 675556ff, disclosed carve-out).
- Dispatcher ruling: historical planning docs RETAIN old identifiers (WrapModels/PositiveTTL/failsafe_test) as point-in-time design records; misleading factual claims (19-method) WERE fixed in living docs. Current-facing docs (README, context.md) clean.
- Skill note: skill_feedback soft-failed "no usage record" on 2 of 3 verification re-dispatches (workers still followed the contract) — attribution-pipeline gap for repeat-skill dispatch, not skill content.
