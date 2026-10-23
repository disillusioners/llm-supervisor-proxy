# Functional Verification Gate — feature/auto-build-version (FE auto build version)

- **Date:** 2026-10-23 (11:27–11:49 UTC)
- **Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-auto-build-version`
- **Branch/HEAD:** `feature/auto-build-version` @ `e12c52b` (4 commits: 8df81a5 → 97c8c31 → 30a6362 → e12c52b; base `89a4f308`) — verified by discovery worker, tree clean before/after
- **Gate owner:** tester (dispatch); 9 worker instances executed everything (see Instance IDs)
- **Budget:** $0 — all local/anonymous (GitHub API anonymous, 4 calls, 57/60 remaining)

## VERDICT: **PASS — READY** (7/7 required checks green; symptom "badge frozen at v150" CLOSED by check 3b)

| # | Check | Result | Evidence headline |
|---|-------|--------|-------------------|
| 1 | Full Go suite (isolated HOME) | ✅ PASS | 41/41 packages `ok` across 4 partition packs (G1–G4 union-proven = `go list ./...`), go1.26.0 toolchain, `-count=1`, zero failures, zero flakes fired |
| 2 | Full FE suite + tsc-baseline comparator | ✅ PASS | 23/23 files, 237/237 tests, 0F/0S; comparator 5/5 (in-vitest, not raw tsc); NEW Header.test.tsx green |
| 3 | E2E injection proof (3 builds) | ✅ PASS 3/3 | (a) no ldflags → `{"version":"dev"}` + `(build dev)`; (b) `-X main.Version=424242` → `{"version":"424242"}` + `(build 424242)`; (c) EMPTY `-X main.Version=` → `{"version":"dev"}` (not `""`) + `(build dev)` |
| 4 | Fresh guard re-runs | ✅ PASS | `go test ./cmd ./pkg/ui -run Version`: 4 tests + 3 subtests all PASS (names verbatim); vitest Header.test.tsx 1/1 standalone |
| 5 | Static wiring audit | ✅ PASS | APP_VERSION once at job env; fail-fast precedes both consumers; identical ldflags both build paths; build-args present; triggers + publish gates byte-identical to base; ZERO VERSION-file references; YAML parses; Dockerfile ARG precedes consumer |
| 6 | CI run_number prediction | ✅ Done | Workflow id 237418657 "Docker CI/CD"; latest run **N=181** (push/success/49beb28a, 2026-10-20T18:57:37Z) → next push-triggered run badges **v182** (caveats below) |
| 7 | No browser automation | ✅ Honored | Host has no chromium; vitest + happy-dom IS the FE gate (stated by both FE-running workers) |

## Scope Decision
Full-suite verification was **warranted** (release-gate class change: CI pipeline + build-time injection + composition root `cmd/main.go` + FE badge chain) — the gate brief mandated full re-verification and it was honored. Within that: the Go suite was executed as **4 parallel package-group packs** (G1/G2/G3/G4) instead of a single `go test ./...` invocation, to respect the 5-min pack cap and parallelize; the partition is **union-proven**: G1(4) + G2(9) + G3(17) + G4(11) = **41 = `go list ./...` count**. Per-package `ok` matrix gives equal-or-better evidence than a single opaque run.

## Check 1 — Full Go suite (detail)
All groups: `PATH=…/toolchain@v0.0.1-go1.26.0.linux-amd64/bin` first, `GOTOOLCHAIN=local`, `GOMODCACHE/GOCACHE/GOPATH` pinned to real caches, **HOME isolated per worker** (`/tmp/gate-*`), `timeout 300` outer + `-timeout 240s` internal, `-count=1`, `-skip TestB1_UltimateModelAllowed`.

| Group | Packages (n) | Result | Wall | Slowest |
|-------|--------------|--------|------|---------|
| G1 `./pkg/proxy/...` | proxy, normalizers, token, translator (4) | 4/4 ok | 92s | pkg/proxy 85.0s |
| G2 persistence/LB | store, store/database, credentiallb, grouplb, modelscache, auth, crypto, config, models (9) | 9/9 ok | 45s | store/database 40.4s |
| G3 infra/misc | root, scripts, providers, mcp, ultimatemodel, toolcall, toolrepair, supervisor, bufferstore, events, usage, memlimit, middleware/gzipmw, proxyheader, loopdetection(+fingerprint), logger (17; 3 have no test files) | 17/17 ok | ~50s | usage 47.5s |
| G4 kinds/root | cmd, ui, imggen, decision, test, test/e2e_*×5, test/reasoning_content (11) | 11/11 ok | 31s | test 26.6s |

**Flake adjudications — none fired (all passed run-1, no re-runs needed):**
- `TestEngine_GetOrSelect_TOCTOU_CoolingReverify` (pkg/credentiallb, QUARANTINE ~1/10): PASS run-1 (G2, pkg ok 3.722s).
- `TestStoreEngine_CloseLifecycle` (pkg/store, macOS-only quarantine): PASS run-1 (0.015s); Linux green as expected.
- `TestConcurrentIncrementModelUsage_Stress` / `TestConcurrentDifferentBuckets_Stress` (pkg/usage, SQLITE_BUSY class): PASS run-1 (pkg ok 47.540s, no lock errors).
- pkg/mcp httpbin.org live-calls (environmental): PASS run-1 (22.552s, no network-shaped errors).
- SSRF octal/decimal/hex subtests: PASS — QUARANTINE entry confirmed stale again (post-137b8f0), not a regression.
- `TestB1_UltimateModelAllowed`: **skipped via `-skip`** per QUARANTINE.md 2026-10-18 (deterministic pre-existing prod panic, handler.go:763); skip verified by G4 — targeted re-run printed `no tests to run`, no panic anywhere in output.

**Known pre-existing (not blockers):** credentiallb TOCTOU (~1/10) and mcp httpbin dependency did not fire this run; characterization unchanged.

## Check 2 — FE suite (detail)
`cd pkg/ui/frontend && timeout 300 npx vitest run` (node_modules symlink → main checkout, verified resolving; no npm install/ci from worktree).
- 23/23 test files, **237 tests passed / 0 failed / 0 skipped**, 19s wall.
- tsc-baseline comparator (`src/utils/__tests__/tsc-baseline.test.ts`): **PASS 5/5** — tsc --noEmit ran inside vitest, diff vs `tsc-baseline.txt` clean, zero NEW type errors. Verdict taken from the comparator test, never raw tsc exit codes.
- NEW `Header.test.tsx` (fetch-stub + invocation assertion): PASS (60ms).
- No browser automation — no chromium on host; vitest 3.2.7 + happy-dom is the FE gate.

## Check 3 — E2E injection proof (closure of the v150 symptom)
Three builds, isolated HOMEs, ports 18123/18133/18143 (verified free pre-start, freed post-kill), boot via `APPLY_ENV_OVERRIDES=1 PORT=…`, `/healthz` polled to 200 before probing.

| Scenario | Build | `/fe/api/version` (actual, verbatim) | Banner (actual, verbatim) |
|----------|-------|--------------------------------------|---------------------------|
| (a) default | `go build ./cmd` | `{"version":"dev"}` | `LLM Supervisor Proxy (build dev) starting on port 18123` |
| (b) injected | `-ldflags "-X main.Version=424242 -s -w"` | `{"version":"424242"}` | `LLM Supervisor Proxy (build 424242) starting on port 18133` |
| (c) EMPTY injection | `-ldflags "-X main.Version= -s -w"` | `{"version":"dev"}` (NOT `""`) | `LLM Supervisor Proxy (build dev) starting on port 18143` |

**(b) is the closure proof**: a per-build injected number reaches the FE-served endpoint — the badge can no longer be frozen at a stale committed value. **(c) is the fail-fast-missed case**: `resolvedVersion()` degrades empty injection to `dev` at BOTH seams (`ui.SetVersion` cmd/main.go:242 and banner :438) — a bare `v` badge is impossible. Cleanup: 3/3 PIDs killed (3845252/3845416/3845633), all ports verified free, port 8088 never touched. Runtime 36s (builds 3s/1s/2s — warm cache).

## Check 4 — Fresh guards
`timeout 120 go test ./cmd ./pkg/ui -run Version -count=1 -v` → PASS (2s):
```
--- PASS: TestVersionDefaultIsDev
--- PASS: TestResolvedVersionFallback (+ empty→dev, whitespace→dev, ci_run_number_passes_through)
--- PASS: TestNoVersionFileAtRepoRoot
--- PASS: TestHandleVersionServesSetVersionValue
```
`timeout 240 npx vitest run src/components/__tests__/Header.test.tsx` → PASS 1/1 (54ms).

## Check 5 — Static wiring audit (highlights; full quoted evidence in worker report)
- `APP_VERSION: ${{ github.run_number }}` defined **exactly once** (job `build` env, docker-ci.yml:56; definition-sweep count = 1).
- Fail-fast guard (:62–68, `[ -z "${APP_VERSION}" ] → ::error:: + exit 1`) **precedes both consumers**: compile-verify (:95/:98) and docker build/push (:135).
- Identical injection both paths: compile-verify `go build -ldflags="-X main.Version=${APP_VERSION} -s -w"` (:98) ≡ Dockerfile `ARG APP_VERSION=dev` (:21) → `-ldflags "-X main.Version=${APP_VERSION} -s -w"` (:35); ARG declared before consumer, same builder stage.
- `build-args: APP_VERSION=${{ env.APP_VERSION }}` present on build-push (:146–147; raw `${{ }}` form correct — action inputs are not shell-evaluated).
- Publish gates (`event_name == 'push'`, :110/:117/:121/:136/:153) and the `on:` triggers block are **byte-identical to base** (diffed).
- Full workflow diff limited to exactly: job env APP_VERSION + fail-fast step + ldflags swap (`VERSION=$(cat VERSION)` removed) + build-args. No trigger/gate/reorder/permission changes.
- VERSION-file sweep: 24 grep hits, **all classified** — every hit is APP_VERSION usage, Makefile variable, comment, or **negative assertion** (`cmd/version_test.go:51-54` asserts the file stays deleted). Zero `cat VERSION` / file reads remain. Base file content was `150`; deletion commit 8df81a5; `git ls-files` no longer tracks it. Makefile auto-increment block (the drift source) removed; `VERSION ?= dev` + override documented in AGENTS.md.
- YAML parse: `python3 + PyYAML` → OK.
- `resolvedVersion()` (cmd/main.go:49–54): `strings.TrimSpace(Version) == "" → "dev"`; feeds both `ui.SetVersion` (:242) and banner (:438). Chain verified end-to-end: SetVersion → package var → handleVersion → GET /fe/api/version → Header.tsx:46 `v{version}`.
- Observation (non-issue): non-empty values with surrounding whitespace (e.g. `" 182 "`) pass through untrimmed — spec (empty/whitespace → dev) satisfied; `github.run_number` is a bare integer.

## Check 6 — Prediction
- Workflow: id **237418657**, name "Docker CI/CD", path `.github/workflows/docker-ci.yml`, active.
- Latest run: **run_number 181**, event push, conclusion success, head 49beb28a (master), created 2026-10-20T18:57:37Z (total_count 181; recent: 181 push/success, 180 push/failure, 179 push/success).
- **Prediction: the next push-triggered run displays badge `v182`.** Caveats (verbatim): PR runs and workflow_dispatch runs consume run_numbers too (non-contiguous by design); dispatch runs build/test but do NOT publish (publish gates on `event_name == 'push'`); a zero-delta push emits no workflow event at all; `feature/auto-build-version` is not a trigger branch — nothing fires until the branch reaches `master` via a non-zero-delta push.

## Check 7 — Browser automation
Not used anywhere in this gate. Host has no chromium; vitest + happy-dom is the FE gate (explicitly stated by the FE-suite and guards workers).

## Deviations
1. Go full suite run as 4 parallel partition packs (not one `go test ./...`) — 5-min cap compliance + parallelism; union-proven coverage (41/41).
2. `TestB1_UltimateModelAllowed` pre-skipped via `-skip` per QUARANTINE.md (mirrors registered testroot pack wiring); skip verified, did not run.
3. Harness-level only: G1/G3 workers' `/bin/sh` (dash) rejected a trailing `${PIPESTATUS[0]}` echo AFTER pack completion — verdicts adjudicated from tee'd full-log signatures (all-`ok` matrices, 0 FAIL/panic/race lines). Not test failures; lesson recorded in LESSONS/2026-10-23-dash-pipestatus-harness-quirk.md.
4. No re-dispatches needed; all 9 workers reported with evidence (zero junk reports).

## Instance IDs (all work dispatched, none executed by tester directly)
| Worker | Instance | Runtime |
|--------|----------|---------|
| gate-discovery | 5faba35e-1317-4158-898b-9d6126237ae2 | ~1 min |
| gate-g1-proxy | bc484bc8-f73c-48de-b665-7357c75e2368 | 92s |
| gate-g2-persistence | 00fa2853-429e-463f-b8a4-42bca33c449f | 45s |
| gate-g3-infra | 736af854-0017-4ee6-87ec-6dd401071b90 | ~50s |
| gate-g4-kinds-root | 9406fcf0-8185-415e-bd51-8fb43dec380d | 31s |
| gate-fe-vitest | 685db194-fac9-4899-913e-b8441c5fb459 | 19s |
| gate-audit-predict | fc2f0b5f-569f-4cf9-9858-3008548ae496 | ~4 min (incl. API) |
| gate-e2e-injection | bfcb2a71-2448-4cfb-b224-67ea5e74c21f | 36s |
| gate-guards-fresh | 0b2e5129-1b44-4eb2-bb30-bd36b814aa05 | 7s |

## Code changes
None — verification gate only; zero quick fixes (nothing failed). Branch remains `e12c52b`, tree clean, **nothing pushed**.

## Overall Status
- Check 1 Go suite: ✅ PASS · Check 2 FE suite: ✅ PASS · Check 3 E2E injection: ✅ PASS (3/3) · Check 4 guards: ✅ PASS · Check 5 audit: ✅ PASS · Check 6 prediction: v182 next push run · Check 7 no-browser: ✅ honored
- **Functional verification gate: PASS — READY** (release-decision input; merge/push actions remain with the leader per "never push without user instruction")
