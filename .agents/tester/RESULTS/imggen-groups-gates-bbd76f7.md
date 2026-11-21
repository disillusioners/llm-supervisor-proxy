# imggen-groups Gate Artifact — bbd76f7

**Date:** 2026-11-21 · **Branch:** `feature/imggen-groups-text-subtabs` · **Base:** `5cf1ecdc` · **Gated HEAD:** `bbd76f7` · **Worktree:** `../llm-supervisor-proxy-wt-imggen-groups`
**Plan:** `.agents/shared/planning/imggen-groups/phase4-plan.md` (§3 gates G1–G15 + preconditions)
**Skill applied:** `llmproxy-pack-a-release-gate` (Pack A slice execution + adjudication)

## Preconditions (P0)

- P0.1 base re-anchor: `git merge-base HEAD origin/latest` = `5cf1ecdce73353a23bee30f7c33a8ba69994af74` (matches plan base). ✓
- P0.2 working tree clean at kickoff; HEAD `bbd76f7`. ✓
- P0.4 migration boundary: pre-filed = NO migration. Verified: `git diff 5cf1ecdc..bbd76f7 -- pkg/store/database/migrations/ pkg/store/database/migrate.go` = 0 bytes; 88 .sql files both sides. ✓
- P0.5 boundary fence: k8s/ .github/ = 0 bytes; pkg/config delta bounded to the walk-budget knob (verified §G8). ✓
- P0.6 toolchain: `go version` → `go1.26.0` (cached toolchain). ✓
- P0.7 HOME quarantine: real-home config hash captured before (`1c52053aeff99a58dedfecc4ce19d803`) and re-verified after the gate (unchanged). ✓
- P0.8 FE toolchain: `pkg/ui/frontend/node_modules` present. ✓
- P0.9 import guards: live in `make test` (run, all 7 guards green). ✓

## G1–G15 verdict table

| # | Gate | Verdict | Evidence |
|---|------|---------|----------|
| G1 | build + vet + gofmt | PASS | `go build ./...` EXIT=0; `go vet ./...` EXIT=0; touched .go gofmt -l = 0 files (NEW drift 0). |
| G2 | Pack A (9 pkgs) | PASS | EXIT=0; all 9 `ok`; proxy 145.233s, store/database 53.988s, decision 1.975s, ui 6.976s, cmd 0.022s; 1489 `--- PASS`, 0 `--- FAIL`, 11 `--- SKIP`, 2551 `=== RUN`. Log: `/tmp/gate-bbd76f7/packA.log`. |
| G3 | Pack B (37 pkgs, module-anchored) | PASS (adjudicated) | EXIT=1 solely from the 2 quarantined `pkg/mcp` httpbin redirect tests (`TestHandleTestMCPServerDirect_RedirectToLocalhostBlocked`, `_RedirectToPrivateIPBlocked`) — deterministic third-party 403, QUARANTINE.md row 11. All other 36 pkgs `ok`. 1380 `--- PASS`, 2 `--- FAIL`, 1 `--- SKIP`, 2611 `=== RUN`. Log: `/tmp/gate-bbd76f7/packB.log`. |
| G4 | `make test` (import guards + census) | PASS (adjudicated) | EXIT=2 = recipe failure from the same 2 quarantined mcp tests only. All 7 import guards printed (no FAIL); census gate `registered=44 census-paths=45 — OK`. Re-run with `GOFLAGS=-count=1` (uncached) identical. Logs: `/tmp/gate-bbd76f7/make_test.log`, `make_test2.log`. |
| G5 | FE vitest | PASS | EXIT=0; 53 test files, 569 tests, 0 ×. Log: `/tmp/gate-bbd76f7/vitest.log`. |
| G6 | tsc freeze | PASS | EXIT=2 (expected); 18 errors / 9 files — EXACT parity with baseline (`TOTAL_ERRORS=18 TOTAL_FILES=9`); per-file counts identical (SettingsPage 5, ModelsTab 6, …); `tsc-baseline.txt` 0-byte diff vs base. Log: `/tmp/gate-bbd76f7/tsc.log`. |
| G7 | Anti-vacuous ledger | PASS | 53 added top-level `func Test*` (vs base) → each shows exactly one `--- PASS` in pack logs; 0 MISS, 0 DUP, 0 SKIP. Removed: 0 Go func Test (the 4 retired group_models tab cases are FE `it()` cases in `SettingsPage.tabs.test.tsx`, replaced by P3T1–P3T7 — verified as the COMPLETE removal list). FE tokens: 569 vitest, `×`=0. |
| G8 | Boundary check (pre-declared shapes) | PASS | `pkg/decision/` = 0 bytes; `pkg/authz/` = 0 bytes; `migrations/` + `migrate.go` = 0 bytes; `k8s/` = 0 bytes; `.github/` = 0 bytes. `pkg/config/` delta = exactly the D-17 walk-budget knob: field `ImageGenGroupWalkTimeout` + `ManagerInterface` getter + `Defaults` 240s + clamp [60s,10min] + `Manager` getter (verified in diff). |
| G9 | Migration reconciliation | N/A (no migration) | 88 .sql files both sides (delta 0); registry pairing unchanged; branch 1 (no migration) applies. |
| G10 | Commission pins | PASS | Discriminating pins (catalog-exclusion EMPTY id-set diff; misroute battery incl. both chat-family `image_gen_misroute` + imggen `image_misroute`; 403-vs-502 discriminator + A-6 zero-engine-state; authz composition) are among the 53 added tests, all PASS exactly once. |
| G11 | `-race` spot | PASS | Heavy `./pkg/store/... ./pkg/proxy` EXIT=0 (store/database 270.013s, proxy 304.200s), 0 DATA RACE. Light `./pkg/authz ./pkg/auth ./pkg/modelscache ./pkg/decision ./pkg/imggen` EXIT=0 (authz 1.265s), 0 DATA RACE. Logs: `/tmp/gate-bbd76f7/race_heavy.log`, `race_light.log`. |
| G12 | static/ rebuild (FE changed) | PASS | (a) pairing rule: FE src + static changes landed in the same commits (e.g. `0bfea1d` ADR-19 joint fold). (b) rebuild determinism: two consecutive `npm run build` → sha256 list byte-identical (`static_after_1.txt` == `static_after_2.txt`). (c) bundle carries `imggen-container-tab-group`, `Image-Gen Groups`, `text-container-tab-group`; `group_models` ABSENT. `git status --porcelain pkg/ui/static` EMPTY at end. |
| G13 | Boot smoke in isolation | PASS | `/healthz` 200, `/fe/api/version` = `{"version":"dev"}`, `/v1/models` 200, `/fe/api/models` 200; zero panics; clean SIGTERM; real-home config hash unchanged. Log: `/tmp/gate-bbd76f7/boot.log`. |
| G14 | FE freeze spot-checks | PASS | `data-tab-id="group_models"` retired (only a null-assertion in `SettingsPage.tabs.test.tsx`); new container test-ids `text-container-tab-group` / `imggen-container-tab-group` present in src + bundle. |
| G15 | FE↔BE contract sweep | PASS | `groupLane.ts` mirrors the 3-lane vocabulary (chat/decision/image-gen); ADR-19 joint fold in commit `0bfea1d` (Go lane fold + FE twin in one commit, not adjacent). |

## Flake adjudications (§4)

- `pkg/mcp` httpbin redirect pair (`TestHandleTestMCPServerDirect_RedirectToLocalhostBlocked`, `_RedirectToPrivateIPBlocked`): deterministic third-party 403; `pkg/mcp` tree hash bit-identical base↔HEAD (branch-orthogonal). QUARANTINE.md row 11. Adjudicated, not chased.
- No TOCTOU / PeakHourParity / CloseLifecycle / usage-stress flakes fired this gate.

## Anti-vacuous ledger (G7)

- Added Go `func Test*`: **53** (vs `5cf1ecdc`); each literal `--- PASS` exactly once across packA+packB logs; 0 MISS / 0 DUP / 0 SKIP.
- Removed Go `func Test*`: **0**.
- FE removed `it()` cases: **4** (`load-balance-groups P3.6` group_models tab cases in `SettingsPage.tabs.test.tsx`), replaced by **P3T1–P3T7** (7 new) — verified complete removal list.
- FE added test files: `GroupMemberEditor.imageGenLane`, `GroupModelForm.imageGenLane`, `GroupModelsTab.imageGenLane`, `ImgGenModelsContainer`, `TextModelsContainer` (5 new), plus `groupLane.test.ts` modifications.

## Boundary fence byte-counts (G8)

- `pkg/decision/`: 0 bytes
- `pkg/authz/`: 0 bytes
- `pkg/store/database/migrations/` + `migrate.go`: 0 bytes (88 .sql both sides)
- `k8s/`: 0 bytes
- `.github/`: 0 bytes
- `pkg/config/`: 2 files (+59 config.go, new test) — bounded to the D-17 walk-budget knob only.

## Prelude / postlude

- `git status --porcelain` empty both before and after.
- `git rev-parse HEAD` = `bbd76f7e870a1d20e31e4a77392cc98dd70f78b3` both before and after.
- Real-home config.db md5 = `1c52053aeff99a58dedfecc4ce19d803` unchanged.

## Artifacts

- Gate logs pinned under `/tmp/gate-bbd76f7/`: `packA.log`, `packB.log`, `make_test.log`, `make_test2.log`, `vitest.log`, `tsc.log`, `vite1.log`, `vite2.log`, `static_after_1.txt`, `static_after_2.txt`, `race_heavy.log`, `race_light.log`, `boot.log`, `gofmt_head_touched.txt`.
- Pack A per-package: proxy 145.233s, normalizers 0.031s, token 0.783s, translator 0.052s, store 0.015s, store/database 53.988s, decision 1.975s, ui 6.976s, cmd 0.022s.
- Pack B: 37 pkgs, 36 ok + 1 quarantined (mcp).

## Overall verdict: GATES PASS (G1–G15 green; only the pre-registered, branch-orthogonal quarantined mcp pair fails, adjudicated per QUARANTINE.md row 11).
