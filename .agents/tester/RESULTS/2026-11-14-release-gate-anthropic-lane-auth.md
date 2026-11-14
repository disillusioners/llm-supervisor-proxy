# Release Gate: fix/anthropic-lane-auth (anomaly 4a231ed0 closure — Anthropic-lane auth 401 + IsModelAllowed 403 parity)

Date: 2026-11-14 (~11:00–11:20 UTC)
Gated HEAD: 9f4fd2b (base d9cdbea = origin/latest tip; 3 commits: 1c06b59 fix → 8113caf tests → 9f4fd2b style; 4 files changed — 1 production + NEW pkg/proxy/handler_anthropic_auth_test.go + parity-test mods + e2e harness mods)
Reviewer: council 2/2 APPROVE @ 8113caf (deferred follow-up: 401/403 use OpenAI envelope, not Anthropic type:error — handler_anthropic.go:374/:490)
Instance IDs: gate-packA a116ab1f-3902-462f-89f9-94f5c78256fe · gate-packB e6c3317f-e224-4745-8d5d-0c9f4a46ecb8 · gate-e2e-anthropic 225491c5-f41f-4af3-854a-1383105e0f8b · gate-static-inspect/anti-vacuous be252fe4-a944-486c-80d2-27ff91e0737f

## VERDICT: **GO** — all 5 gates green. No push/merge/deploy performed by tester (giter landing step next).

## Gate Results

### Gate 1 — Pack A (release slice): **PASS** (EXIT=0, first pass, no adjudication)
`timeout 300 go test -count=1 -v -timeout=240s ./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...` (HOME-quarantined, ambient DATABASE_URL/APPLY_ENV_OVERRIDES/PORT/POSTGRES_*/DAEMON_PORT unset)
- Wall 120s; 1242 top-level `--- PASS` / 0 FAIL / 11 pre-existing skips (SSE-heartbeat ×2, mock-LLM-hang ×1, heartbeat-mock ×4, PG-gated ×4)
- `ok .../pkg/proxy 112.849s` · `ok .../pkg/store/database 45.475s` · normalizers 0.030s · token 1.120s · translator 0.122s · store 0.030s · decision 1.200s · ui 3.904s · cmd 0.021s
- Pre-registered flakes PASSED outright: `--- PASS: TestStoreEngine_CloseLifecycle (0.30s)`; `--- PASS: TestStoreEngine_PeakHourParity (0.38s)` (23:59-UTC-only defect; run at ~11:05 UTC)
- Log: /tmp/gate-anthropic-packA.log (kept)

### Gate 2 — Pack B (anchored complement): **PASS-with-quarantine**
`go list ./... | grep -vE 'github.com/disillusioners/llm-supervisor-proxy/(cmd|pkg/(proxy|store|decision|ui))(/|$)'` → 32 packages; same hermetic env + wrappers; wall 63s
- 1085 `--- PASS` / 2 FAIL — the 2 = pre-registered quarantined httpbin redirect pair (QUARANTINE row 2026-11-06), signature-exact:
  - `--- FAIL: TestHandleTestMCPServerDirect_RedirectToLocalhouseBlocked` @ `handlers_api_test.go:1864` — "status=403, content-type=text/plain" (httpbin.org 403s internal redirect target pre-classifier)
  - `--- FAIL: TestHandleTestMCPServerDirect_RedirectToPrivateIPBlocked` @ `handlers_api_test.go:1904` — "Unexpected status: 403"
- Not triggered: TOCTOU (pkg/credentiallb ok 3.794s), usage stress pair (pkg/usage ok 58.507s), TempDir cleanup hook
- Log: /tmp/gate-anthropic-packB.log (kept)

### Gate 3 — Anti-vacuous: **PASS** (5/5 added tests literally executed + passed)
Diff `d9cdbea..HEAD -- '*_test.go'` → 5 added `func Test` (0 removed), all in NEW pkg/proxy/handler_anthropic_auth_test.go. Each has `=== RUN` + literal `--- PASS` in the packA log (note: go test -v emits THREE spaces after `=== RUN`); zero FAIL/SKIP for any of them in packA or packB:
- `--- PASS: TestAnthropic_NoToken_Returns401 (0.00s)`
- `--- PASS: TestAnthropic_InvalidToken_Returns401 (0.00s)`
- `--- PASS: TestAnthropic_ModelNotAllowed_Returns403 (0.00s)`
- `--- PASS: TestAnthropic_ModelAllowed_PassesThrough (0.00s)`
- `--- PASS: TestAnthropic_UnrestrictedToken_PassesThrough (0.00s)`
- Dev-report "6th" test `TestAnthropic_ConversationKeyPerTokenSalting` = PRE-EXISTING modified-in-place (not an addition) — corroborated passing: `--- PASS: TestAnthropic_ConversationKeyPerTokenSalting (0.00s)` (its anonymous-case pins were rewritten from "unsalted key" to "401 before key derivation")
- 2233 `=== RUN` lines in packA log

### Gate 4 — gofmt zero-new-drift: **PASS** (ZERO NEW DRIFT: YES)
Toolchain gofmt @ go1.26.0; base snapshot via `git archive d9cdbea` into /tmp (worktree untouched)
- F_HEAD = F_BASE = 47 flagged files, sets byte-identical (F_HEAD − F_BASE = ∅)
- F_HEAD ∩ changed-*.go = 2 files, both PRE-EXISTING (base-identical hunks, branch-hunk-disjoint):
  - pkg/proxy/handler_anthropic_tokenid_parity_test.go: EOF missing-newline hunk @@ -258,4 vs base @@ -252,4 (same content; branch hunks end ≈208 — no overlap)
  - test/e2e_anthropic_thinking_leak/harness_test.go: struct-literal alignment @@ -275,7 byte-identical to base; branch hunks all ≥295 — no overlap
- pkg/proxy/handler_anthropic.go (production) and pkg/proxy/handler_anthropic_auth_test.go (new) — NOT flagged at all
- Actual flag count 47 (brief anticipated ~52) — identical base↔HEAD, immaterial

### Gate 5 — e2e anthropic_thinking_leak: **PASS 3/3** (EXIT=0)
`timeout 300 go test -count=1 -v -timeout=240s ./test/e2e_anthropic_thinking_leak/` (hermetic env; harness authenticates since 1c06b59)
- `--- PASS: TestS1A_Buffered_InternalStream_NoThinkingOnWire (1.03s)`
- `--- PASS: TestS1B_Live_InternalStream_ThinkingDeltaEmitted (1.32s)`
- `--- PASS: TestS3_InternalNonStream_PersistedThinking (1.13s)`
- `ok .../test/e2e_anthropic_thinking_leak 3.501s` — matches expected 3/3 (older 4/4 registry rows were a different-era suite membership)
- Log: /tmp/gate-anthropic-e2e.log (kept)

## Scope Decision
Full release-gate suite WAS warranted: security hotfix to a public unauthenticated route (anonymity hole closure), gate explicitly commissioned by leader; executed as the 2 registered release-gate slices + explicit e2e slice + static gates. No additional packs (FE, boot smoke, vet/build) — outside this gate's commissioned scope (tidier delta is comments/test-shape only per leader).

## Quarantine Status
- 5 rows active, ZERO new quarantines this gate; row 2026-11-06 (mcp httpbin redirect pair) reconfirmed (sole B-slice failures, signature :1864/:1904)
- Rows 2026-08-27 (CloseLifecycle), 2026-10-06 (TOCTOU), 2026-11-14 (PeakHourParity) did NOT fire this gate — all passed outright

## Action Needed
- [ ] giter: landing step (merge to latest/master, deploy daoduc) per commission — NOT done by tester
- [ ] Deferred (council): Anthropic-style `type:error` envelope for 401/403 on the Anthropic lane (currently OpenAI envelope) — handler_anthropic.go:374/:490
- [ ] Tidier lane (optional, pre-existing): EOF trailing-newline on pkg/proxy/handler_anthropic_tokenid_parity_test.go (gofmt-flagged at base AND head, out of gate scope)

## Documentation Updated
- [x] PACKS.md — 2026-11-14 gate note (Timeout Configuration) + anthropic_thinking_leak row (Last Run 2026-11-14, PASS 3/3) + mcp row reconfirmation
- [x] QUARANTINE.md — row 2026-11-06 reconfirmation (+2F attempts, signature :1864/:1904)
- [x] RESULTS/2026-11-14-release-gate-anthropic-lane-auth.md — this report
- [ ] rules/ensure.md — untouched (user-owned)

## Code Changes Summary (by tester)
- .agents/tester/PACKS.md, .agents/tester/QUARANTINE.md, .agents/tester/RESULTS/2026-11-14-release-gate-anthropic-lane-auth.md — registry/gate artifacts only (house convention: committed on the branch)
- Commit: <filled by commit worker>
- Gated code HEAD at execution: 9f4fd2b (verified by every worker pre-run; worktree clean at `git status --porcelain` throughout)
