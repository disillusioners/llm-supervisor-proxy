# TEST GATE: feature/sticky-last-success-ordering (4 commits @ fee32a8, base 63df316)

**Date:** 2026-11-06 · **Gate:** merge readiness for sticky last-success race ordering
**Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-sticky-last-success-ordering`
**Design:** `.agents/shared/planning/sticky-last-success-ordering/design.md` (committed, v2)
**Commits gated:** `9761093` (feature) → `2a86b10` (review-fix) → `648a4dc` (tidy) → `fee32a8` (Anthropic delivery gate)

**STATUS: FINAL — all stages complete. Gate verdict: GO (details at bottom).**

## Scope Decision

Full suite warranted: 4-commit feature spans race ordering (`pkg/stickyorder` NEW, `pkg/proxy` wiring ×6 files, race_coordinator), config gate, DB merge semantics, FE — cross-module blast radius, explicit merge gate. 58 files touched (12 production .go, 37 test .go, 4 FE src, 4 FE bundle artifacts, 1 design doc); +3763/−157.

## Environment (all workers)

- Toolchain: `go1.26.0` cached (`$HOME/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.linux-amd64/bin/go`); system go 1.22.2 unusable, GOTOOLCHAIN=auto offline-fail.
- HOME quarantine per run (`mktemp -d`, `env -u PORT -u DATABASE_URL`, GOMODCACHE/GOCACHE/GOPATH pinned to real user dirs) — no `~/.config/llm-supervisor-proxy/config.json` rewrite incidents this gate.
- Dual-layer timeouts everywhere: outer `timeout 300` + internal `-timeout=110s`.

## Stage 1 — Full regression: ✅ PASS (FAIL(expected-known-only), zero NEW failures)

**1.1 Full suite** `go test ./... -count=1 -timeout=110s` — worker `stage1-fullsuite` (8c93d67e), 2m35s wall.
- 35 packages: 32 ok, 3 failing tests — ALL known/attribution-cleared:
  - `pkg/credentiallb` `TestEngine_GetOrSelect_TOCTOU_CoolingReverify` — KNOWN flake (~1/10 plain, full-package context only; QUARANTINE.md 2026-10-06). Failed in suite → **PASS on isolated re-run #1** (0.10s) ⇒ flake-attributed, not a regression. `pkg/credentiallb` untouched by this branch.
  - `pkg/mcp` ×2: `TestHandleTestMCPServerDirect_RedirectToLocalhostBlocked` / `_RedirectToPrivateIPBlocked` — see attribution below.
- `pkg/proxy` **ok 99.5s** (largest; under the 110s internal cap). `pkg/stickyorder` ok 0.047s. `pkg/usage` stress PASSED (no SQLITE_BUSY this run). `pkg/store` CloseLifecycle PASSED on Linux (macOS-only quarantine).
- Full log: `/tmp/stage1-fullsuite-run1.log` (2.1 MB).

**1.1b Base attribution** (worker `stage1-base-attrib` 3369096c, scratch detached worktree of `63df316` at /tmp, removed after):
- The 2 pkg/mcp failures are **PRE-EXISTING-IDENTICAL** on clean base `63df316`: byte-identical assertion messages (`handlers_api_test.go:1865` / `:1905`), identical failing-test SET and statuses across 727 RUN/PASS/FAIL name lines; deterministic over 2 runs per side (runtimes 17-24s each).
- Root cause (external): **httpbin.org policy change** — `redirect-to?url=<private-ip>` now returns server-side `403 Forbidden redirect target is an internal address` (text/plain) before the proxy's CheckRedirect classifier runs; direct curl confirms. `httpbin.org/status/200` still 200 ⇒ target-policy-specific, not an outage. Deterministic, not flaky.
- Structural proof: `pkg/mcp` tree bit-identical base↔feature (`git rev-parse <rev>:pkg/mcp` identical; `git diff 63df316..HEAD -- pkg/mcp/` empty).
- ⚠️ Stale quarantine finding: `TestValidateUpstreamURL` SSRF octal/decimal/hex subtests (QUARANTINE.md 2026-09-22 row) **PASS on all 4 runs** — entry is stale; recommend clearing (post-gate).
- Recommend (post-gate): quarantine the 2 httpbin-redirect tests as env-deterministic third-party failures; they will redden every full-suite run until pinned.

**1.2 Race, sticky-scoped** (worker `stage1-race` e5ac26f9):
- `$GO test -race -count=1 ./pkg/stickyorder -timeout=110s` → **PASS** (1.310s test time).
- `$GO test -race -count=1 -run 'Sticky' ./pkg/proxy -timeout=110s` → **PASS** (40.900s test time). 27 matched top-level tests + 7 subtests = 34 executions, zero DATA RACE / FAIL / panic lines. Package-wide `-race ./pkg/proxy` deliberately NOT run (known-unstable, per gate brief).
- Coverage census (test names): gate-off & nil-engine identity, OpenAI+Anthropic promotion, re-entry reorder (OpenAI/Anthropic/backup-pool), record (stream/non-stream), no-record exclusions (first-byte-then-die, deadline-pick, ultimate path, client-write-fail ×2 lanes, Anthropic truncated), invalidation (winner-fail, non-winner-keeps-pin, cred-failover exhaustion, cancelled-loser), snapshot flag propagation, RouteModel untouched, gates+log.

**1.3 FE** (worker `stage1-fe` faf35f48):
- vitest (from `pkg/ui/frontend`, worktree had real node_modules): **245/245 tests, 23/23 files PASS** (16s) — incl. the 3 new sticky tests: flag-load-from-config, toggle round-trip into proxy-Apply payload **preserving race siblings**, family-bearing clear path (sticky=false).
- `tsc-baseline.txt`: **TOTAL_ERRORS=19 / TOTAL_FILES=9 — exactly the expected baseline, no regression** (gate test enforces per-file counts too).
- Go `./pkg/ui`: **177/177 PASS** (2.245s) incl. `TestEmbedStatic_AllAssetReferencesResolve` — regenerated hashed bundles committed in sync (R26 embed discipline honored).

**1.4 Static hygiene** (worker `stage1-vet` 7ac18a37):
- Commit chain verified: exactly 4, expected order/hashes.
- `go vet ./...`: **clean** (exit 0, empty, 3.67s).
- `gofmt -l` (toolchain 1.26, 49 touched .go files): 5 test files listed — **all 5 drift PRE-EXISTING at base** (identical `gofmt -d` line counts base vs HEAD; e.g. handler_integration_test.go 245 lines both sides). Zero NEW drift. Base-level debt, not a gate blocker.
- Branch code clean; only `.agents/` cross-agent notes dirty (approver/tidier working files).

## Stage 4 — FE/API smoke (embedded binary, HTTP): ✅ 8/8 PASS

Worker `stage4-smoke` (6ac6bb7b); binary `go build ./cmd` (26MB real binary, 4s warm build), port 18234, isolated TMPHOME (SQLite), PID-tracked cleanup (confirmed: process gone, port freed, tmp dirs removed; port 8088 never approached).

| Check | Result | Evidence |
|---|---|---|
| C1 /healthz | ✅ 200 | start |
| C2 config GET | ✅ | `sticky_last_success_enabled: false` default confirmed; baseline siblings: race_retry=false, max_parallel=3, on_idle=true, buffer=5242880 |
| C3 /ui/ + asset | ✅ | text/html 548B referencing `index-BWq71yKf.js` (fetched: 200 `text/javascript`, 310,829B) + `index-dFT_1ydU.css` (200 text/css) — real hashed assets, not SPA-shell fallback |
| C4 FE payload sticky=true | ✅ | PUT 200, GET persists true; race siblings byte-identical (only `updated_at`) |
| C5 FE payload sticky=false | ✅ | clears; siblings identical |
| C6 B1 guard LIVE | ✅ | bare `{"sticky_last_success_enabled": true}` PUT 200 → sticky true AND race siblings **byte-identical to baseline** — no family-block clobber |
| C7 bare-false residual | ✅ | bare `{"sticky_last_success_enabled": false}` dropped, value keeps prior true — exactly design §5.2 documented residual |
| C8 /healthz | ✅ 200 | end |

- Launch gotcha (recorded to KB): `PORT` env is honored only with `APPLY_ENV_OVERRIDES=1` (pkg/config/config.go:463-476) — first boot bound 4321; worker adapted, killed only its own PID.
- Toggle-in-served-bundle proof (follow-up): `sticky_last_success_enabled` present **1×** in `SettingsPage-CKhlvAMT.js` (the lazy chunk; 0× in old base chunk `CBY2REVi`, old chunk gone, index chunk references the new hash) — FE exposure confirmed at served-artifact level.

## Stage 2 — Scenario closure: ✅ PASS (additive test commit `5f75216`; zero production edits)

Worker `stage2-scenario` (1af0d128), alone on a quiet machine. **Gap analysis:** no existing test did the multi-request same-prefix RUN1/RUN2 shape (zero wall-clock measurement across all 5 sticky test files; every promotion test pre-seeds the winner via direct `RecordSuccess`). The 27-test wiring matrix (Stage 1.2 census), engine unit (TTL/sweep/Get-no-refresh/concurrency), config default/env, and B1 merge matrix (T-merge-1..4 + probe test) were already covered and green — the scenario pack closed the one real gap.

### RUN 1 vs RUN 2 — same conversation prefix (key `ba526595…`), fake upstreams

| Request | Gate | Slot at start | Attempts-until-success | Upstream hit order | Wall-clock | [STICKY] evidence | race_started models |
|---|---|---|---|---|---|---|---|
| RUN1 r1 | OFF | — | **3** | `[m-a m-b m-c]` | **1102.1ms** | none (asserted) | — |
| RUN1 r2 | OFF | — | **3** | `[m-a m-b m-c]` | **1102.8ms** | none | — |
| RUN2 r1 (cold) | ON | empty | **3** | `[m-a m-b m-c]` | **1102.0ms** | `success recorded key=ba526595… model=m-c` | `[m-a m-b m-c]` |
| RUN2 r2 (warm) | ON | m-c | **1** (HARD ASSERT) | **`[m-c]`** | **303.0ms** | `promoted last-success model=m-c to index 0 … from=[m-a m-b m-c] to=[m-c m-a m-b]` | **`[m-c m-a m-b]` — index 0 = winner** |

**3.64× wall-clock drop; 2 upstream round trips eliminated per warm request.** The original live symptom (every request burning the full chain before the working model) is reproduced with the gate OFF and closed with the gate ON.

### Unpin / re-pin / revert / TTL evidence

- Winner failure (C flipped to fail): hit order `[m-c m-a m-b]` (winner burns first), request 502, then `[STICKY] invalidated key=… model=m-c reason=attempt_failure` — exactly 1 line (winner-matched), slot cleared.
- Empty-slot fallback win (B succeeds): hit `[m-a m-b]`, 200, records m-b; next request promotes m-b (`from=[m-a m-b m-c] to=[m-b m-a m-c]`).
- Second unpin cycle (B flipped to fail): `[m-b m-a m-c]`, 502, invalidated m-b — symmetric.
- Revert (slot empty, C back): original order `[m-a m-b m-c]`, NO promoted line, re-records m-c, 200.
- TTL (handler-wired `stickyorder.NewEngine(150ms, 50ms)`): success + 400ms idle ⇒ `Get` miss; next same-prefix request un-promoted original order, then re-records.
- All 4 added tests appear as literal `--- PASS` (2.45s / 1.68s / 6.27s / 2.90s).

### Measured fake latencies (n=2, ±1% across `-count=2` / combined / `-race` reruns)

| Fake | Mean |
|---|---|
| m-a fail (402 quota) | **300.48ms** |
| m-b fail (429) | **300.44ms** |
| m-c success | **251.00ms** |

Sim proxy-machinery overhead (component sum 851.9ms vs 1102.1ms measured): ≈125ms per failure transition — sim-specific, not attributed to the feature.

### Race/fallback config used

`RaceMaxParallel: 3` (failure-spawn reachable), `RaceParallelOnIdle: false` (deterministic failure-driven sequential burn — mirrors the incident's sequential attempt walk), `RaceRetryEnabled: false` (snapshot-only field, pinned for the record), single credential per model (cred-failover ineligible ⇒ chain advances directly). Execution list `[m-a m-b m-c]` = requested model + `FallbackChain`.

### Commit + verification

- **`5f75216`** `test: sticky last-success gate scenario closure (RUN1/RUN2, unpin, TTL)` — adds ONLY `pkg/proxy/sticky_scenario_test.go` (+601). No production edits were needed: short TTL is an engine constructor param; the engine injects via `NewHandler`'s 8th arg; log/event capture seams already existed.
- Scenario pack `-count=2` PASS; combined sticky pack (31 tests) PASS; sticky-scoped `-race` PASS (56.7s); gofmt clean. Env discipline held on every run.
- **Defect list: EMPTY.** Three authoring-time failures were test-side bugs (fixed in the test file); gotchas recorded in LESSONS.

## Stage 3 — Cache-hit / cost impact model (analysis; every claim labeled MEASURED or MODELED)

No provider cache-hit percentages are claimed — providers cannot be queried from tests. Cache statements are mechanism/condition only.

**(a) Why sticky ON ⇒ the prompt-cache-hit condition holds [MEASURED behavior + MODELED mechanism].** Measured: a same-prefix follow-up's attempt #1 goes to the provider that delivered the previous success (hit order `[m-c]`, `race_started` `[m-c m-a m-b]`, 1 attempt). Provider-side prefix caching (Anthropic prompt caching, OpenAI automatic prefix caching, equivalents) keys on identical prefix bytes at the SAME provider — so concentrating every follow-up of a conversation on its last-success provider keeps that provider's prefix cache warm across the conversation. Under OFF, in windows where earlier chain members are intermittently healthy, the prefix SCATTERS across providers and each provider's cache is re-primed at full input price between misses. In the hard-down incident shape specifically, the success provider received every request even under OFF (as attempt 3) — so the incident-window delta is NOT "winner gets the traffic"; it is the waste/latency in (b) plus scatter-avoidance once degraded members recover intermittently.

**(b) Eliminated waste per avoided failed attempt [MEASURED, sim]:** 1 upstream round trip + its failure latency — 300.48ms (402) / 300.44ms (429); per warm request = **2 RTTs ≈ 601ms pure failure latency**; measured wall-clock drop 1102.1 → 303.0ms (**−72.5%**). Billing assumption [MODELED, stated]: 402 quota / 429 rate-limit failures reject at admission and bill ZERO tokens — in the incident's failure mix the per-attempt saving is latency + provider load + an occupied race slot, NOT tokens. Partial input-token savings materialize only for post-input failures (5xx / mid-processing timeouts); their share in the incident window is unknown and is not claimed.

**(c) Conversation of N follow-up requests [MEASURED sim / MODELED live-scaled]:**
- Sim: saved ≈ **N × 0.80s wall-clock (72.5%)** and N × 2 upstream RTTs per conversation (warm after the first delivered success); token saving ≈ 0 for the 402/429 mix.
- Live-scaled from the user's incident timing (~9.3s / 3 attempts ≈ 3.1s per attempt): warm request ≈ 1 attempt ≈ ~3.1s vs ~9.3s ⇒ **≈66% latency reduction, ≈6.2s saved per follow-up request**.
- Racing duplicate-inference note [MODELED, mechanistic, no rate claimed]: with `RaceMaxParallel > 1` and healthy-but-slower siblings, OFF can run duplicate concurrent inferences (each provider reaching admission = a potential full input bill before first-byte cancellation). Sticky's known-winner-first shrinks the window in which duplicate attempts spawn; magnitude depends on sibling health mix — unmeasured, unclaimed.

## Verdict: ✅ GO — merge-ready from the test gate

All four stages green: full regression zero NEW failures (3 known failures fully attributed: 1 quarantine-listed flake re-run-clean isolated, 2 pre-existing-identical third-party httpbin failures proven on clean base); sticky-scoped `-race` clean; FE complete at source (245/245 incl. 3 new toggle tests, tsc baseline exactly 19/9) AND served-artifact level (toggle in the rebuilt lazy chunk, embed test green); Stage-4 live-binary smoke 8/8 incl. the B1 merge guard and the documented bare-false residual; the original incident symptom measurably closed (RUN2 warm: 1 attempt, index-0 winner, 3.64× faster in sim, ≈66% live-scaled). Gate's only branch delta beyond the 4 feature commits is the additive test commit `5f75216` — no production edits required or made. Non-blocking post-gate housekeeping: quarantine-wire the 2 httpbin-redirect tests (deterministic third-party red on every full suite until then), formally clear the stale SSRF quarantine row (now passing), and schedule base-level gofmt debt (5 pre-existing test files).
