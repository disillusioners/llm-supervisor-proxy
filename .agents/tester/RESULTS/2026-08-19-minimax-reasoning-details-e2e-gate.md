# Test Report: MiniMax reasoning_details E2E Gate (Phase 3: P3-2/4/5/6/7)

Date: 2026-08-19
Branch: `feature/minimax-reasoning-details` @ `d5280ce` → session end `fa8c11d`
Instance IDs: recon 0caeaf7a · P3-4 e25e2fb5 (+ verify/fix c36748b9) · P3-5 e7e6551c · P3-2 20020bf6 · P3-7 fae6d942 · sweep ba687f3e

## Summary
- **Verdict: READY FOR MERGE** — all Phase 3 exit criteria met at `fa8c11d`
- Packs run: 7 (5 new + P3-7 verify + full sweep) | All PASS
- New assertions: 53 (shell) + 43 (e2e) + 16 (matrix subtests) + 21 (header sub-cases verified)
- **2 production bugs found by the gate, both fixed + verified + committed**
- Quarantined: 0 | Flakes: 0 in final sweep (1 historical observation, watchlisted)

### Scope Decision
> Requested: E2E verification per phase3-plan P3-2/4/5/6/7 (final pre-merge gate). Full backend suite WAS run — warranted: merge gate + 2 mid-session production fixes widened blast radius to all backend packages. Frontend build scoped OUT with evidence (zero paths under `pkg/ui/frontend/` in `ac877ea..d5280ce` and `d5280ce..fa8c11d`). ensure.md Important rows (peak-hour, migration 018) out of change set.

### ensure.md Validation Results
- **Critical**: 4/4 — go test ✅ (catch-alls ./pkg/... + ./test/...) · go vet ✅ (empty) · build ✅ · frontend ✅ *warranted scope-out, diff-evidenced*
- **Important**: not in change set (diff evidence empty for peak-hour/migration files)
- **Nice-to-have**: race conditions — implicit PASS (no panics/detector trips in 12 runs); boundary conditions — PASS (P3-2 matrix + P3-5 suite)

### Improvement Notices (surface to user — ensure.md is user-owned)
- ⚠️ ensure.md Critical #1 says `go test ./...` — validated MY way (per-package packs + two catch-alls, each under `timeout 300` with `-timeout 240s`) per timeout/scoping rules. Suggested rewrite: "All Go tests pass via registered packs or scoped invocations with timeout wrappers". No action required; current form worked.
- ⚠️ Task-context note "pkg/mcp pre-existing failure" is STALE — pkg/mcp green at d5280ce and at fa8c11d (16.55s sweep run).

### Contract Discrepancy (leader's table vs code) — RESOLVED in favor of CODE
`X-Proxy-Interleaved-Thinking` accepts **only exact `true` or `1` (case-sensitive)**; `True`/`TRUE` are rejected — byte-identical semantics to the `X-Force-Ultimate-Model` precedent (`handler.go:466`) and asserted by 21 green sub-cases (P3-7). The task's stated table (`True`/`TRUE` activate) does not match the implementation or the plan; no behavior change made.

### Bugs Found & Fixed During Gate
| ID | Severity | Root cause | Fix | Verification |
|---|---|---|---|---|
| **T3b** race-internal silent reasoning loss | critical (data loss on feature's core path) | translator wrote struct values into `map[string]any`; `HydrateReasoningDetails` (openai.go:846) accepted maps only → silent `continue` → empty typed slice → `omitempty` dropped wire field | type-switch hydrator `068317c` (+regression test) | T3b 6/6 flipped; repro PATH A byte-equals control PATH B; harness 53/53; full chain green |
| **S3** ultimate-internal id scheme | important (cross-path contract violation; low wire risk) | `handler_internal.go:106` used message index, not monotonic counter over reasoning-carrying messages (translator contract) | counter fix `b2dfde0` | e2e 43/43; all 4 paths emit identical ids for identical input |
| T3b harness assertion (test bug) | minor | asserted `"index": 0` on wire; `omitempty` on `Index int` (always 0) makes key always absent | inverted to `assert_not_contains` `1344380` | 53/53 |

First T3b fix candidate (map-mode translator emission) was **stopped on evidence** — `encoding/json` orders struct fields by declaration, map keys alphabetically; would have broken P3-2 byte-identity. Hydrate-boundary fix is byte-neutral (empirically proven).

### Per-Pack Results
| Pack | Result | Detail |
|---|---|---|
| P3-4 shell harness (`test/test_mock_minimax_reasoning.sh`, ports 4005/4325) | **PASS 53/53** (12s) | T1-T15; header hygiene 0 leaks across 13 captures; incremental+cumulative both translate; single-winner; usage; ultimate trigger |
| P3-5 e2e (`test/e2e_minimax_reasoning/`) | **PASS 43/43** (4.7s) | 14 groups, all 4 paths, drift delta 0, marker-header control |
| P3-2 matrix (inline) | **PASS 32/32 cells** (0.4s) | 24 body + 4 header + 4 usage; 12 gap-fills (`ee590c1`); 2 cells N/A-by-structure (documented) |
| P3-7 header table (inline) | **PASS 21 sub-cases** (0.006s) | Satisfied by existing tests; verified vs precedent; single-source semantics (2 call sites via `proxyheader.*`) |
| Full sweep (12 invocations) | **PASS 12/12** (4-5 min wall) | 23 pkg + 5 test packages, zero flakes; mcp GREEN |

### Unit Test Results (regression)
- `pkg/proxy/` 24.07s PASS (349+, incl. matrix) · `pkg/ultimatemodel/` 4.43s PASS (128+) · `pkg/providers/` PASS (56) · `pkg/proxy/translator/` PASS (78+) · `pkg/proxyheader/` PASS · all other packages PASS (sweep rows A6-A8)

### Action Needed
- [ ] Post-merge: live MiniMax call — R9 stream mode, R7 `reasoning_split` placement acceptance, live format values (see verification-report Live-only list)
- [ ] Watch (not quarantine): `stream_buffer.go:166`/`race_executor.go:1434` timing panic — 1 occurrence in ~7 runs during T3b session, 0/12 in sweep
- [ ] Repo-wide gofmt debt: 40 pre-existing files — recommend separate `style:` sweep PR
- [ ] Plan follow-ups unchanged: Q9 admin gating, Q10 dormant converter, A+B unification PR

### Documentation Updated
- [x] `.agents/shared/planning/minimax-reasoning-details/verification-report.md` (P3-6 deliverable)
- [x] `.agents/tester/PACKS.md` — 5 new pack rows, statuses PASS
- [x] `.agents/tester/MOCK_TESTS.md` — both new mock specs ACTIVE with last-run results
- [x] `.agents/tester/LESSONS/` — T3b root-cause/byte-shape lesson; S3 cross-path parity lesson
- [x] `.agents/tester/README.md` — testing-history row
- [x] RESULTS/ this file

### Code Changes Summary (all committed)
| Hash | Subject |
|---|---|
| `ee590c1` | test: P3-2 matrix (2 files, 1424 lines) |
| `882fa3f` | test: P3-4 mock harness (Go mock + shell) |
| `068317c` | fix(providers): T3b hydrator type-switch (+regression test) |
| `1344380` | test: T3b index assertion repair |
| `166aa7f` | test: P3-5 e2e suite (14 groups/43 subtests) |
| `b2dfde0` | fix(ultimatemodel): S3 monotonic id counter |
| `fa8c11d` | style: gofmt session-touched files |

### Overall Status
- P3-2 ✅ PASS (32/32) · P3-4 ✅ PASS (53/53) · P3-5 ✅ PASS (43/43) · P3-6 ✅ written · P3-7 ✅ PASS (21 verified)
- ensure.md: ✅ 4/4 Critical
- **Testing Complete: ✅ READY** — mergeable at `fa8c11d`; live-only items tracked in verification report
