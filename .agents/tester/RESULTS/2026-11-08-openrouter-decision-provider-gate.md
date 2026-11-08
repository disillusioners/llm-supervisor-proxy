# TEST GATE: feature/openrouter-decision-provider (2 commits @ a88684f, base 4cd8b36)

**Date:** 2026-11-08 · **Gate:** merge readiness — openrouter as decision-model provider + R3/R4 canonical criteria closure
**Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-openrouter`
**Commits gated:** `e5e8a0f` (feat(decision): openrouter decision provider + R3/R4 canonical shape closure) → `a88684f` (fix(decision): strip /v1 tail in systemOneProvider + mock 422 negative control)

**STATUS: FINAL — all five gates complete. Verdict: PASS-WITH-QUARANTINE** (quarantined = pre-existing registry-known third-party pkg/mcp pair; zero commission-caused failures). Merge gate: **GO**.

## Scope Decision

Full gate suite warranted: explicit merge gate; 18-file cross-module diff (pkg/decision ×10, pkg/models ×1, pkg/ui Go ×2, FE src ×2, FE bundle artifacts ×3); gates = release Pack A + Pack B complement + FE suite + `make test` umbrella + anti-vacuous sweep, per commission.

## Diff verification (worker da4699c6)

- State: branch `feature/openrouter-decision-provider`, HEAD `a88684f191cf…`, exactly 2 commits over base, merge-base == `4cd8b36` (linear) — **5/5 ✓**. Working tree fully clean (not even .agents session notes dirty).
- Stats: 18 files, **measured +922/−145** vs claimed 18/+921/−144 — file count exact, line delta exactly +1/−1 (immaterial; claim likely taken one edit before final a88684f). Zero deleted files; 2 new files (both test files): `pkg/decision/openrouter_decision_test.go` (+214), `pkg/decision/r3r4_canonical_shape_test.go` (+409).
- `package.json` / lockfile: **unchanged** (FE dependency tree stable).

## Environment (all workers)

- go1.26.0 cached toolchain (absolute path `$HOME/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.linux-amd64/bin/go`); `GOTOOLCHAIN=local`; offline pins `GOPATH=/home/nea/go GOMODCACHE=/home/nea/go/pkg/mod GOCACHE=/home/nea/.cache/go-build`.
- HOME quarantine per run (`mktemp -d`) + `env -u PORT -u DATABASE_URL` — zero config.json rewrite incidents this gate.
- Dual-layer timeouts: outer `timeout 300` everywhere; internal `-timeout=110s` on slice runs (`make test` target carries no internal timeout — outer cap governs it).
- Go slices run single-stream (no concurrent go invocations) per contention hazard.

## Gate 1 — Pack A: ✅ PASS (adjudicated) · worker 19d6cb19 · skill test-pack-execution

`go test -v -count=1 -timeout=110s ./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...`

- **Initial run: exit 1, wall 119s** — `pkg/proxy` tripped the internal 110s cap **by 59ms** (110.059s) under intra-run package-parallel contention (`pkg/store/database` 33.2s running concurrently). **Zero assertion failures** (`--- FAIL:` count 0 across the run). Active test at timeout: `TestToolRepair_NonStreamingIntegration` (0s — just entered). All other 9 package trees ok, incl. **commission-owned `pkg/decision` (0.901s) and `pkg/ui` (2.041s) green on the FIRST run**.
- **Adjudication re-run** (isolated `./pkg/proxy/...`, identical env recipe): **exit 0, wall 112s** — `pkg/proxy ok 109.601s`, all 1482 proxy-tree tests pass. Log `/tmp/gate-openrouter-packA-proxy-adjudication.log` (1482 `--- PASS:` lines).
- Classification: infrastructure contention (59ms over cap), not a code failure → **PASS**.
- Primary log: `/tmp/gate-openrouter-packA.log` (2199 `--- PASS:` lines). Gate independence: 0 files modified.

## Gate 2 — Pack B (complement): ✅ PASS (net; 2 KNOWN quarantined carved) · worker 13e09d8c · skill test-pack-execution

`go list ./... | grep -vE 'github.com/disillusioners/llm-supervisor-proxy/(cmd|pkg/(proxy|store|decision|ui))(/|$)'` → 33 packages → `go test -v -count=1 -timeout=110s $PKGS`

- **Raw exit 1, wall 51s**; 29 ok / 1 FAIL / 3 no-test-files; **2297 `--- PASS:` / 2 `--- FAIL:`**.
- The 2 failures are **EXACTLY the registry-known pkg/mcp redirect pair** (`TestHandleTestMCPServerDirect_RedirectToLocalhostBlocked` @ handlers_api_test.go:1865, `_RedirectToPrivateIPBlocked` @ :1905 — QUARANTINE.md 2026-11-06, deterministic third-party httpbin.org 403). Signature byte-identical to registry. **All other pkg/mcp tests green** (full SSRF suite, redirect-to-external-allowed, TooManyRedirects, auth suites).
- Adjudication watches: credentiallb `TestEngine_GetOrSelect_TOCTOU_CoolingReverify` **PASSED in-package** (0.10s, log line 3073); pkg/usage stress pair **PASSED** (46.79s, zero SQLITE_BUSY); **commission-owned `pkg/models` ok 0.070s, zero failures**.
- Complement anchor sanity ✓: includes pkg/models, pkg/mcp, pkg/credentiallb, pkg/usage, pkg/grouplb, pkg/stickyorder, `pkg/proxyheader` (edge case correctly NOT excluded by `(/|$)`), root stub, all 7 `test/` trees; excludes every Pack A package (zero overlap in log). Package list: `/tmp/gate-openrouter-packB.pkgs`; log: `/tmp/gate-openrouter-packB.log`.

## Gate 3 — FE suite: ✅ PASS · worker b9f9a173 · skill test-pack-execution

- vitest run: **246/246 tests, 23/23 files, exit 0, 16s** (vitest 13.56s) under `timeout 300` — includes the in-suite `tsc-baseline.test.ts` gate test (5/5).
- Typecheck baseline: **19 errors / 9 files — EXACT per-file match** to `tsc-baseline.txt` (ModelsTab 6, SettingsPage 6, 7 files × 1) → zero new tsc errors from the DecisionModelForm change. Standalone `tsc --noEmit` exit 2 = baseline-frozen errors only.
- node_modules: pre-provisioned in worktree (220 entries); `package.json` + `package-lock.json` byte-identical to main checkout — no provisioning needed, no network.
- Gate independence: TRACKED_DIRTY_COUNT 0 post-run. Logs: `/tmp/gate-openrouter-fe.log`, verbose `/tmp/gate-openrouter-fe-dmf-verbose.log`, `/tmp/gate-openrouter-tsc.log`.

## Gate 4 — `make test` umbrella: ✅ PASS (net; same 2 KNOWN carved) · worker c083951f · skill test-pack-execution

- Target recipe verified (Makefile:51–68): 3 import guards (imggen ↛ proxy; decision ↛ proxy; decision ↛ imggen — the latter two are this commission's §3.5 guards) + `@go test ./...` (no internal timeout, no -race, non-verbose). **All 3 guards PASS.**
- **make exit 2 / go test exit 1 — solely the 2 KNOWN mcp quarantined tests** (identical signature). Wall 115s. **38 ok, 0 cached, 3 no-test-files** (41 listed).
- Commission surface all ok: pkg/decision 0.711s · pkg/models 0.030s · pkg/ui 2.312s · pkg/proxy 110.753s · pkg/store/database 36.914s · cmd 0.017s.
- credentiallb TOCTOU + usage stress pair: **PASSED again**. Log: `/tmp/gate-openrouter-maketest.log`.

## Gate 5 — Anti-vacuous sweep: ✅ 17/17 ALL-VERIFIED · worker d99e2f45

Every ADDED test in `git diff 4cd8b36..a88684f -- '*_test.go'` (+ FE additions) shows a **literal PASS line in this gate's OWN logs**; zero `--- FAIL:`/`--- SKIP:` hits for any added test across all logs (mirror: `/tmp/anti-vacuous-table.md`):

| # | Test | File | Pack/Run | PASS evidence (log:line) |
|---|------|------|----------|--------------------------|
| 1 | TestDecisionRegistry_OpenRouter_Registered | pkg/decision/openrouter_decision_test.go | Pack A | packA.log:10171 |
| 2 | TestOpenRouterDecisionProvider_BuildUpstream_DefaultURL (+4 sub) | pkg/decision/openrouter_decision_test.go | Pack A | packA.log:10177 |
| 3 | TestHandler_OpenRouter_BearerAuth_AtSystemOneURL | pkg/decision/openrouter_decision_test.go | Pack A | packA.log:10183 |
| 4 | TestHandler_OpenRouter_SafetyIdentifier_400 | pkg/decision/openrouter_decision_test.go | Pack A | packA.log:10185 |
| 5 | TestHandler_OpenRouter_UnregisteredChatProvider_502 | pkg/decision/openrouter_decision_test.go | Pack A | packA.log:10187 |
| 6 | TestSystemOneProvider_BuildUpstream_V1TailStrip (+4 sub) | pkg/decision/openrouter_decision_test.go | Pack A | packA.log:10193 |
| 7 | TestR3R4_CanonicalShape_PredicateAccepted | pkg/decision/r3r4_canonical_shape_test.go | Pack A | packA.log:10213 |
| 8 | TestR3R4_CanonicalShape_ChoiceObjectAccepted | pkg/decision/r3r4_canonical_shape_test.go | Pack A | packA.log:10215 |
| 9 | TestR3R4_CanonicalShape_ChoiceFlatAccepted | pkg/decision/r3r4_canonical_shape_test.go | Pack A | packA.log:10217 |
| 10 | TestR3R4_CanonicalShape_ScoreObjectAccepted | pkg/decision/r3r4_canonical_shape_test.go | Pack A | packA.log:10219 |
| 11 | TestR3R4_CanonicalShape_ScoreFlatAccepted | pkg/decision/r3r4_canonical_shape_test.go | Pack A | packA.log:10221 |
| 12 | TestR3R4_CanonicalShape_BothProviders (+2 sub) | pkg/decision/r3r4_canonical_shape_test.go | Pack A | packA.log:10225 |
| 13 | TestR3R4_CanonicalShape_RejectsNestedShape | pkg/decision/r3r4_canonical_shape_test.go | Pack A | packA.log:10229 |
| 14 | TestValidateSystemOneShape_RejectsOldNestedShape | pkg/decision/r3r4_canonical_shape_test.go | Pack A | packA.log:10231 |
| 15 | TestKindRules_Parity_Positive_OpenRouterDecisionModelAccepted | pkg/ui/kind_rules_parity_test.go | Pack A | packA.log:10825 |
| FE1 | "openrouter provider -> internal_base_url = https://openrouter.ai/api on submit" | DecisionModelForm.test.tsx | FE verbose | fe-dmf-verbose.log:6 |
| FE2 | "provider dropdown lists openai + typesafe + openrouter (Q11 recommended identity)" | DecisionModelForm.test.tsx | FE verbose | fe-dmf-verbose.log:7 |

Sanity `grep -c -- '--- FAIL:'`: packA 0 · adjudication 0 · packB 2 (= the mcp pair only) · fe 0 · maketest 2 (same pair). No added-test name appears in any FAIL/SKIP line anywhere.

## Static hygiene

- `go vet ./...`: **exit 0, 4s**.
- gofmt: 1 flagged changed file — `pkg/models/config.go` — **PRE-EXISTING at base** (identical `gofmt -d` hunk `@@ -351,9 +351,9 @@` at base and HEAD; branch hunks `@@ -106,7 @@`/`@@ -192,7 @@`/`@@ -271,7 @@`/`@@ -288,17 @@` don't overlap). Distinct file from the POK-known `pkg/config/config.go` flag, which is NOT in this diff. **Zero NEW drift.** Both new test files gofmt-clean.

## Flake / quarantine adjudications

| Item | Registry | This gate | Action |
|---|---|---|---|
| pkg/mcp redirect pair (:1865/:1905) | QUARANTINED 2026-11-06, deterministic third-party (httpbin.org 403) | fired in Pack B + make test, signature byte-identical | carved from verdicts; skip-wiring remains deferred (post-gate mainline) |
| credentiallb TestEngine_GetOrSelect_TOCTOU_CoolingReverify | QUARANTINED 2026-10-06, flake ~1/10 full-package context | PASSED in Pack B AND make test | no action |
| pkg/usage SQLITE_BUSY stress pair | QUARANTINED 2026-09-22, flake | PASSED both runs | no action |
| store CloseLifecycle | QUARANTINED 2026-08-27, macOS-only | PASSED (Linux) | no action |

QUARANTINE.md: no changes required (no new flakes, no resolved entries to move).

## Maintenance watch (documented; NO mid-gate fix — gate independence)

**pkg/proxy full-package duration has outgrown the 110s internal slice cap.** Data points: 99.5s (sticky gate 2026-11-06 full-suite) → 109.601s (this gate, isolated re-run) → 110.753s (this gate, make test). Pack A's initial run tripped by 59ms under package-parallel contention. **PACKS.md timeout note updated with this commit**: release-gate slice runs (Pack A/B, umbrella) use internal `-timeout=240s` inside the unchanged outer `timeout 300` hard cap; unit-pack scripts keep 110s. Durable fix (split pkg/proxy test files into two packs, or serialize package execution `-p 1`) = post-gate mainline test-architecture item.

## Worker instances

da4699c6 diff-inventory/static-hygiene · 19d6cb19 Pack A · b9f9a173 FE suite · 13e09d8c Pack B · d99e2f45 anti-vacuous sweep · c083951f make test · a89f83b2 commit-artifacts

## Verdict

**PASS-WITH-QUARANTINE.** All five gates green; 15 Go + 2 FE added tests verified non-vacuous with literal evidence; commission-owned packages (pkg/decision, pkg/ui, pkg/models, FE) 100% clean on first runs; the only failures anywhere in the suite are the two pre-existing, registry-quarantined, third-party httpbin.org pkg/mcp tests (byte-identical on base at the 2026-11-06 gate). **Merge gate: GO.**
