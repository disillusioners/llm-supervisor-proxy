# Ship Gate: fix/cache-layer-separation (wi-4d2c8e17 — cache layer separation / routing-correctness fix)

Date: 2026-11-16 (~14:30–15:10 UTC)
Gated HEAD: 30e3ec41abb9c4aafeb48b163b176e5c7092ee8d (frozen tip; base 24c51ef865f1e8360c681dd7dd6970587f8b4b10 = origin/latest v193 tree; 10 commits, 31 files +3293/−585; worktree `../llm-supervisor-proxy-wt-cachelayer`)
Reviewer: council APPROVE @ review round (0 critical; delta 15709a1..30e3ec4 APPROVE) — this gate is the independent tester re-proof.
Instance IDs: gate-cachelayer-packA 7e0cc22c · gate-cachelayer-packB 31806fcb · gate-cachelayer-fe fab234e2 · gate-cachelayer-gofmt fd764a58 · gate-cachelayer-red d511e0e2 · gate-cachelayer-pin ccc05202 · gate-cachelayer-make c9cf4a6e · gate-cachelayer-vetbuild 2691de12

## VERDICT: **SHIP-GATE-PASS** — all 9 commissioned gates green (G2/G3 with the documented quarantine-pair adjudication only). No push/merge/deploy performed by tester (giter owns landing).

## Gate Results

### Gate 1 — Pack A (release slice): **PASS** (EXIT=0, first pass, no adjudication)
`timeout 300 go test -count=1 -v -timeout=240s ./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...` (HOME-quarantined, ambient DATABASE_URL/APPLY_ENV_OVERRIDES/PORT/POSTGRES_*/DAEMON_PORT unset)
- Wall 132s; 9/9 packages ok — pkg/proxy 129.621s · pkg/store/database 34.604s · pkg/ui 3.517s · pkg/decision 0.877s · cmd 0.023s (+ normalizers/token/translator/store)
- 2267 `=== RUN` · **1274 top-level `--- PASS` / 0 FAIL** · 11 SKIPs all proven pre-existing at base (SSE-heartbeat ×2, mock-LLM-hang ×1, heartbeat-mock ×4, PG-gated ×4)
- Pre-registered flakes PASSED outright: `--- PASS: TestStoreEngine_CloseLifecycle (0.29s)`; `--- PASS: TestStoreEngine_PeakHourParity (0.29s)` (run ~14:3x UTC, outside the 23:59 window)
- Log: /tmp/gate-cachelayer-packA.log (11,143 lines, kept)

### Gate 2 — Pack B (anchored complement): **PASS-WITH-QUARANTINE**
`go list ./... | grep -vE 'github.com/disillusioners/llm-supervisor-proxy/(cmd|pkg/(proxy|store|decision|ui))(/|$)'` → **33 packages** (32 at base + NEW pkg/modelcache); EXIT=1, wall 52s
- 1093 `--- PASS` / 2 FAIL / 1 SKIP (pre-existing file-gated log-skip `TestToolCallBuffer_MiniMaxFromLogFile`)
- The 2 = EXACTLY the quarantined httpbin redirect pair, signature-exact (`handlers_api_test.go:1864/:1904`, httpbin.org 403 pre-classifier; QUARANTINE row 2026-11-06 — reconfirmed, see QUARANTINE.md): `TestHandleTestMCPServerDirect_RedirectToLocalhostBlocked` + `_RedirectToPrivateIPBlocked`; every other package `ok` (mcp the only failing package)
- Known flakes PASSED outright: TOCTOU_CoolingReverify (0.10s), usage stress pair (6.51s/12.17s) — zero adjudications, zero re-runs
- NEW package pkg/modelcache: 8/8 literal `--- PASS` (MarkMarkedClear, NilAndZeroNoOps, MarkUntilExtendsNotShortens, ParseReset_Absolute/Relative/NoParse, ModelWall, ModelWall_AccountQuotaNotModelMarked)
- Log: /tmp/gate-cachelayer-packB.log (41,481 lines, kept)

### Gate 3 — make test (house landing gate): **PASS-WITH-QUARANTINE** (EXIT=2 ×2 runs, wall 135s each)
- Makefile `test` = 3 import guards (imggen↛proxy, decision↛proxy, decision↛imggen — 3/3 PASS) + `go test ./...`; **make test covers NEITHER FE nor go vet** (coverage hole closed by Gate 4 + vet/build supplement below)
- Run 2 with `GOFLAGS=-count=1` (cache-disabled re-proof): normalized diff vs run 1 = exactly 3 `(cached)`→timed transitions; failure set identical
- 41 ok / 1 FAIL package (pkg/mcp = the quarantined pair only, `:1864/:1904`) / 3 no-test-files; pkg/proxy ok 131.638s, pkg/usage 52.993s, store/database 42.394s
- **Branch-orthogonality (strongest proof):** `git rev-parse 24c51ef:pkg/mcp` == `30e3ec4:pkg/mcp` — pkg/mcp bit-identical base↔tip; diff = 0 files
- Static tree sha256 before/after identical (make test never builds FE); hermeticity proven (real ~/.config config.json mtime unchanged; quarantine temp HOMEs absorbed the config.Load() rewrite)
- Logs: /tmp/gate-cachelayer-make.log, /tmp/gate-cachelayer-make2.log

### Gate 4 — FE gate: **PASS** (tsc exact parity · vitest 269/269 · vite build zero-mod)
- (a) `npx tsc --noEmit` EXIT=2 (expected error shape), 9s — **19 errors / 9 files = EXACT parity with tsc-baseline.txt** (per-file counts all match; branch did NOT touch the baseline — empty diff). The single EventLog.tsx error is the pre-existing `secondary_model` handler at line 131; branch additions (line 158+) contribute zero new errors
- (b) Baseline re-assert `tsc-baseline.test.ts` 5/5 green; full vitest suite: **24 files / 269 tests / 0 failed** (14s)
- (c) `npx vite build` EXIT=0 (6s) — pkg/ui/static tree **BYTE-IDENTICAL** before/after (4/4 sha256 match; git status clean; zero-mod proof) ⇒ the committed bundle at f6fc42b is exactly what the source produces. Bundle grep: MODEL_FAILED / MODEL_FALLBACK_ATTEMPTED / MODEL_FALLBACK_ADVANCED / MODEL_FALLBACK_EXHAUSTED present (1× each in index-D10Xutfq.js) — not a stale bundle
- Logs: /tmp/gate-cachelayer-{tsc,baseline-test,vitest,vite}.log, /tmp/static-{before,after}.txt

### Gate 5 — gofmt zero-new-drift: **PASS** (flagged-line-set method; stronger than required)
- 25/25 touched .go files (incl. ALL 9 _test.go; 4 added files) have **EMPTY flagged sets at BOTH base and HEAD** — F_HEAD − F_BASE = ∅ trivially; `gofmt -l` flags ZERO touched files. The branch is gofmt-clean wholesale (no pre-existing drift in the changed set at all)
- Method non-vacuity proven: deliberate drift injection into a /tmp copy flagged exactly 1 line (`func badFmt() {`)
- Change inventory (authoritative): 31 entries — 5 A (design.md, pkg/modelcache ×3, cache_layer_separation_test.go), 24 M, 2 R095/R099 static renames, 0 D; +3293/−585
- Evidence: /tmp/gofmt-{head,base}-*.set (50 files)

### Gate 6 — RED spot-check (executed RED on base): **PASS** — 4/4 mandatory flagships FAIL on base
Scratch = `git archive 24c51ef | tar -x` (+ NEW test file copied in, cmp-identical to the 30e3ec4 blob). First pass was compile-blocked (the test file references 2 branch-only production symbols: `ModelSkipCache` field on Config/ConfigSnapshot; `preferUnmarkedUpstreamCredential` @ race_executor.go:303) → RED-by-absent-surface only. Authorized surgical scratch split (NEVER touching the branch) converted it to executed RED:
- **Excisions (scratch copy only, fully ledged):** (A) one line `ModelSkipCache: mc` deleted from shared harness helper `rebuildHandlerWithCaches` (base-faithful: base has no consumer of the field); (B) 7 branch-only-symbol tests parked verbatim under `//go:build branch_only_excised` (WinClear_PerLayer, D1×2, D7a, D6×2, R2); 2 interleaved type decls restored; vet 0 errors
- **Integrity proof: all 17 surviving test bodies cmp BYTE-IDENTICAL** to the original; the only function-level diff in the file is the one Excision-A line
- Executed REDs (exit 1 each):
  - **T_A** `MarkedHeadStillPicked_ChainServes` — `:203: marked head agentic-Main was ATTEMPTED 1 time(s) … attempted=[agentic-Main]` (**the user's observed regression: the mark suppressed/flipped the pick on base**)
  - **T_F** `Client400_ReturnsPromptly` — `:654: resolution took 1.500837616s — the 400 was not returned promptly (spun toward the 1.5s deadline)`; advance loop repeated "no untried target … chain end, terminal" ~16× (bounded 45s inner budget; deterministic assertion, no panic needed)
  - **T_G** `Empty200DoesNotWin` — `:678: winner = m1, want m2 (the content-less m1 must not win)`
  - **T_E** `SingleModelGetsHedge` — `:611: upstream calls = 1, want >= 2`; base log: `Cannot spawn second: only 1 model(s) available`
- Bonus REDs: **T_B** `MarkDoesNotFlipToBackup` FAIL (`:273`); **structural pin itself RED on base** (`:774: ../store/database/store.go imports pkg/skipcache` + store_group_rewrite.go) — the layer separation provably did not exist at base
- Honest labels: T_C / T_D / GroupPick_StableUnderUpstreamMarks **PASS on base** → they are branch regression-guards, NOT fix-pinning REDs (do not cite them as RED evidence)
- Logs: /tmp/red-{TA,TB,TC,TD,TE,TF,TG,GP,STRUCT}.log, /tmp/red-compile-errors.log, scratch /tmp/gate-red-base

### Gate 7 — isolation pin non-vacuity: **PASS** (mutation caught 4/4)
- Mechanism: `TestCacheLayer_Structural_PackageLevelNoCacheImports` — go/parser AST (`parser.ImportsOnly`) ImportSpec scan; forbidden = pkg/skipcache + pkg/modelcache; recursive `filepath.WalkDir` over pkg/store/database, pkg/grouplb, pkg/modelscache; `_test.go` excluded (load-bearing: store_group_skipcache_test.go:26 imports skipcache today); fail-closed on parse errors/missing dirs
- Positive control present: `TestCacheLayer_Structural_ImportParserIsCommentAware` (comment-only fixture must NOT flag; blank-import fixture MUST flag — both behaviors asserted)
- Mutations in scratch (all still compiled, so the FAIL is the pin's own assertion): M1 blank skipcache import in pkg/grouplb/binding.go → **FAIL**; M2 named modelcache import in pkg/modelscache/models.go → **FAIL**; new subpackage leak pkg/grouplb/newsub/ → **FAIL** (recursive walk proven); `//go:build ignore`-tagged leak → **FAIL** (build-tag-blind). Counter-probe: comment-only path → pin stays green (not a naive grep)
- Report-only gaps (follow-up candidates, NOT gate blockers): (1) **one-hop pin** — transitive reachability unenforced (blank import into pkg/credentiallb leaks skipcache into store/database's deps while the pin stays green; today no such path exists — closures verified); (2) exact-string path match (subpackages/re-export aliases would evade); (3) `_test.go` exclusion lets a pinned package's own tests share cache state
- Logs: /tmp/gate-pinmut_{baseline,m1,m2}.log

### Gate 8 — flake protocol: **ZERO adjudications needed**
Every known flake ran and PASSED outright in the main runs (CloseLifecycle 0.29s, PeakHourParity 0.29s in A; TOCTOU 0.10s, usage stress pair in B). No re-runs consumed. Quarantine count unchanged (5 active rows, 0 new).

### Gate 3/4 supplement — ensure.md Critical static gates: **PASS**
- `go vet ./...` EXIT=0, empty output, 2.58s; `go build ./...` EXIT=0, empty output, 3.19s; house pack `test/packs/build_gate_test.sh` RESULT: PASS (dual confirmation); 42 packages incl. root stub + real entry ./cmd
- ensure.md Critical coverage: #3 go test (Packs A/B + make test, quarantine-adjudicated) ✓ · #1 vet ✓ · #2 build ✓ · #4 tsc zero-NEW (exact parity) ✓. Important/Nice-to-have rows (peak-hour, migration 018) out of blast radius — the 31-file inventory touches none of that surface

## Scope Decision
Full release-gate suite WAS warranted: production routing-correctness fix shipping to BOTH hosts; all gates explicitly commissioned by leader. Executed as the registered release-gate slices + FE gate + static gates + RED/pin proofs. No additional packs needed.

## Ship-note observations (non-blocking, for the landing owner)
1. **Event-vocabulary change is production-visible:** the coordinator chain path now emits `model_failed` / `model_fallback_attempted` / `model_fallback_advanced` / `model_fallback_exhausted` (model-only payloads) where base emitted `group_member_failed` / `group_fallback_exhausted` on that path; the genuine member-hop path still emits `group_*` (handler.go:1081, handler_anthropic.go:718). FE updated in-branch; **any non-FE consumer of the old chain-path event names silently stops receiving them.**
2. 6 removed test funcs are inverted-contract replacements (old "marks DO skip members" contract → new "marks never change the pick"), not coverage loss; `ModelsManager.SetSkipCache` removed entirely (the separation itself); pkg/modelcache is a new production package.
3. Pin gaps above (one-hop reachability foremost) + design-deferred items (D-C lane divergence; FE per-credential observability on upstream reorder) = follow-up ticket material.

## Quarantine Status
5 rows active, ZERO new quarantines this gate; httpbin redirect pair reconfirmed (sole B + make-test failures, pkg/mcp tree-hash identical to base). See QUARANTINE.md row 2026-11-06 for the updated tally.

---

## DELTA VERIFY — fix round #2 (range 7213796..06575ad, appended 2026-11-16)

**Verdict: DELTA-PASS — the SHIP-GATE-PASS @ 7213796/30e3ec4 extends to tip `06575ad791686fe2891de9e48ca23c9e075fd1b7`.** Delta is NON-BEHAVIORAL; focused pass only (no full battery re-run), per commission.

**Range:** 2 commits — `c26996e` (refactor: single-source model-wall policy + truthful win-clear seam) + `06575ad` (docs rewrap). Diff: 10 files, +137/−104 (432 diff lines, audited hunk-by-hunk).

### Δ1 — Diff audit: NON-BEHAVIORAL
- **ApplyWall equivalence (crux) PROVEN:** new `pkg/modelcache/policy.go:31-60` owns classify→attributable→reset-clamp→MarkUntil / fallback+clamp→Mark, **semantically identical** to BOTH former inline blocks (`race_coordinator.markModelWallForErrorLocked` @7213796 AND `handler_anthropic.markAnthropicUpstreamSkip` @7213796), which were themselves equivalent to each other. All surfaces checked: classification order, attributable predicate, clamp bounds (`MinMarkWindow`=1m / `MaxMarkWindow`=24h — values identical to former literals), cache key (ROW identity), `now` capture point, fallback DefaultTTL(15m) path, event/error shapes (untouched). New `ApplyWall` nil/empty guards behaviorally redundant (no-op callee semantics proven). Both lanes are thin callers — exactly 2 `ApplyWall(` call sites, zero leftover inline policy, `ModelWall` single caller.
- **config.go:** literals → `modelcache.MinMarkWindow/MaxMarkWindow` reuse; `Validate` → `Manager.Save` still **REJECTS** out-of-range `model_skip_cache_ttl` (`errors.New`, unchanged control flow) — no clamp-at-save introduced.
- **Rename `clearWinnerUpstreamSkip`→`clearWinnerLayerState`:** complete — 2 live (handler.go:963, handler_anthropic.go:622) + 1 test (cache_layer_separation_test.go:840) call sites; old name retains ZERO live refs (2 doc-only historical mentions); function body **byte-identical** with the name normalized (same args; clears the same three layers — new doc is truthful).
- **`06575ad` alone:** comment/markdown only (design.md + skipcache.go doc), zero code lines; the strengthened `AnyMarked` R1-note verified factually true (no production caller).
- **Blob identity:** `pkg/modelcache/modelcache_test.go` d2d1821f… **IDENTICAL**; `pkg/proxy/win_clear_winner_identity_test.go` 8dfc8c40… **IDENTICAL** (untouched); `pkg/proxy/cache_layer_separation_test.go` differs by **exactly one line** = the mandated rename call site (all assertions unchanged) — deviation accounted by design, ruling pins unedited.
- **FE untouched** (`git diff --name-only … -- pkg/ui` empty); gofmt zero-drift at both ends; no unused imports; source-scan pin `Structural_ModelSuppressionKeyedByRow` holds (readers survive; extraction moved only mark calls).

### Δ2 — Targeted re-run @ 06575ad
- **Pack A full** (`timeout 300` + `-timeout=240s`, `-count=1 -v`, HOME-quarantined): **EXIT=0, WALL=132s, 1274 PASS / 0 FAIL / 11 pre-existing skips** — count-identical to the ship-gate A run. Flake adjudication NOT TRIGGERED.
- **Protected pins, literal evidence:** `=== RUN`/`--- PASS: TestWinClear_ClearsWinnerMemberNotFailedHead (0.43s)` (Pack A log :6664/:6684); `--- PASS: TestModelWall (0.00s)` + `--- PASS: TestModelWall_AccountQuotaNotModelMarked (0.00s)` (explicit pkg/modelcache run, EXIT=0; zero `no tests to run` — false-green trap checked).
- **gofmt -l on all 9 touched .go files: EMPTY.**

### Warnings (recorded, non-blocking)
1. Dev claimed "~9 touched files" — actual **10** (the extra is `pkg/proxy/handler.go`, the second live rename call site; benign).
2. `modelcache.ApplyWall` — the new single-source owner of the entire mark policy — ships with **no direct test pin** (pre-existing condition: the former inline code was equally unpinned; not a regression). **Recommend a direct ApplyWall pin (reset-parse clamp window, fallback TTL, attributable veto) as a fast follow-up before/soon-after landing.**
3. Zero flake adjudications; quarantine registry unchanged by this delta.

Instance IDs (delta pass): gate-cachelayer-delta-diff 671e9e04 · gate-cachelayer-delta-rerun cea5b7d6.
