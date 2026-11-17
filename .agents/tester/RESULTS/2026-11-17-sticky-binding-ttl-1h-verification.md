# Verification Report: feature/sticky-binding-ttl-1h (per-layer conversation-hash binding TTL)

- **Date**: 2026-11-17 (06:12–07:3x UTC)
- **Code under test**: worktree `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-sticky-binding-ttl-1h`, branch `feature/sticky-binding-ttl-1h`, HEAD = base `a2952a9`, **uncommitted feature diff** (18 modified + 4 untracked test files)
- **Feature**: per-layer conversation-hash binding TTL — 30m ABSOLUTE from `createdAt`, delete-on-expired-read, NO hit refresh. GROUP layer pkg/grouplb ((groupID, conversationKey)→memberID), MODEL layer pkg/credentiallb ((modelID, conversationKey)→credential). Knobs `group_binding_ttl`/`model_binding_ttl` (default 30m, Validate [1m,24h], env GROUP_BINDING_TTL/MODEL_BINDING_TTL gated by APPLY_ENV_OVERRIDES, boot-wired cmd/main.go, mergeConfig zero-preserving guards pkg/store/database/store.go).
- **Mandate**: INDEPENDENT verdict (developer's 8/8 acceptance not trusted) + FULL regression the developer deferred. Findings only — nothing fixed, nothing committed.
- **Workers**: W1 `0f835623` (ttl-accept-w1) · W2 `d1828ac9` (ttl-suitea-w2) · W3 `28668d91` (ttl-suiteb-w3) · W4 `5773dd3f` (ttl-race1-w4) · W5 `fd849f70` (ttl-race2-w5) · W6 `f110642d` (ttl-sabot-w6) — all load_skill=test-pack-execution, HOME-quarantined, `timeout 300` dual-layer, Go 1.26 pinned toolchain.

## VERDICT: **GATES PASS** — Phase 1 PASS · Phase 2 PASS · Phase 3 PASS · 0 real code findings

Full-suite run — warranted: leader-mandated full regression; feature spans 4 packages + boot wiring + config merge (cross-module). Executed as the registered release-gate slices (A+B) + race slices, NOT a bare `go test ./...`.

---

## Phase 1 — Acceptance verification: PASS (one coverage gap, closed by throwaway probe)

Criteria→test mapping (W1; all runs exit 0, literal `--- PASS` cited):

| Criterion | Test (file:line) | Key assertion | Result |
|---|---|---|---|
| (a) GROUP hit in window: affinity intact, no deadline extension | `TestEngine_TTL_AbsoluteExpiry` (credentiallb/engine_test.go:126) + `TestEngine_CreatedAtNotRefreshedOnHit` (grouplb/engine_test.go:358) | "in-TTL hit must not re-bind"; "in-TTL hit must NOT refresh createdAt (absolute lifetime)" | PASS |
| (b) expired read → no affinity AND map entry deleted | `TestEngine_BindingExpiresDespiteContinuousHits_DeletesEntry` (grouplb + credentiallb abs_ttl_acceptance_test.go:14) + backup twin (:70) | "bindingsCount=%d want 1 (delete-then-insert, no second row)"; backup pool variant "want 0 — the expired row was not deleted on read" | PASS |
| (c) after delete, DEFAULT pick re-reaches member A | **no permanent test (GAP-1)** — closed by W6 throwaway probe | probe: post-expiry read returns **member-a with newly=true**; follow-up hit returns member-a newly=false; bindingsCount==1 | **PROBE PASS** `--- PASS: TestProbeC_DefaultPickReaches_A_AfterExpiry (0.15s)` |
| (d) failure-driven rebind immediate, both layers | `TestEngine_ExcludeAndReselect_CooldownFromRetryAfter` (grouplb) + `TestEngine_ExcludeAndReselect_RebindsConversation` (credentiallb/cooldown_test.go:121) | immediate rebind with TTL=1h, no wait | PASS |
| (e) MODEL layer (a)/(b) equivalents | `TestEngine_TTL_AbsoluteExpiry`, `TestEngine_TTL_LazyExpiry`, `...DeletesEntry` (credentiallb) | "absolute TTL violated: binding did not expire after created_time+ttl" | PASS |
| (f) knob independence + non-positive ignored | `TestEngine_BindingTTL_IndependenceFromCredentialLayer` (grouplb/abs_ttl_acceptance_test.go:176) + `TestBindingTTL_ConfigIndependence` (config/binding_ttl_acceptance_test.go:11) + `TestEngine_SetBindingTTL_HonouredAndNonPositiveIgnored` (both engines) | group TTL change must not move credential engine TTL and vice versa; zero/negative SetBindingTTL no-ops; clamp floor/ceiling | PASS |
| (g) mergeConfig zero-preserving (FE-clobber guard) | `TestMergeConfig_BindingTTLs_PreservedOnZeroIncoming` (store/database/binding_ttl_merge_test.go:10) | "GroupBindingTTL clobbered"/"ModelBindingTTL clobbered"/"not overwritten" | PASS |

Probe determinism note (W6): "B weight 0" is IMPOSSIBLE in pkg/grouplb — `newWeightedSelector` returns nil for weight ≤ 0 → `ErrNoMembers`. Determinism achieved via single-default-member pool (A, weight 1) + B declared `BackupOnly: true`; binding→B planted via `InjectPreconditionStateForTest`; expiry by real sleep past `createdAt+ttl` (no clock seam exists). Probe file deleted after run; byte-identity proven.

## Phase 2 — Sabotage (non-vacuity): PASS — both sabotages bit, feature diff byte-identical after

Baseline (W6): status `18 M + 4 ??`; full-diff sha `595c6370adcc…30f0c`. FE answer: **NO FE change** (zero .ts/.tsx/.css/frontend paths). gofmt -l: **zero drift** on all 22 changed .go files.

1. **Hit-path refresh reintroduced** (one line, `b.createdAt = now // SABOTAGE-1` at the grouplb `getOrSelect` write-lock re-verify — the exact site the diff removed the refresh): BOTH proofs FAILED as required —
   - `engine_test.go:376: in-TTL hit must NOT refresh createdAt (absolute lifetime)` → `--- FAIL: TestEngine_CreatedAtNotRefreshedOnHit`
   - `abs_ttl_acceptance_test.go:55: no rebind after expiry — TTL did not fire despite continuous hits` → `--- FAIL: TestEngine_BindingExpiresDespiteContinuousHits_DeletesEntry`
2. **Expiry ignore-only (no delete)** — faithful variant (skip delete AND its count-mirror decrement): `abs_ttl_acceptance_test.go:58: bindingsCount=3 want 1 (delete-then-insert, no second row)` → `--- FAIL: TestEngine_BindingExpiresDespiteContinuousHits_DeletesEntry`. Reverted.
   - Variant-A observation (remove delete, KEEP decrement): test still passed — the rebind path does an unconditional `bindingsCount++` rebalance so the corrupted count self-heals to 1. This models a count-mirror bug, not ignore-only semantics; see FINDING-2.
3. **Byte-identity**: `engine.go` sha `bed0c107…38c03c` == backups; feature-scope diff sha (`git diff -- . ':(exclude).agents'`) == `595c6370…30f0c` exactly. Status delta vs baseline = ONE external line: ` M .agents/tidier/notes.md` (concurrent tidier agent, 07:15Z — not gate work, left intact; full-diff sha shifted by that note only). No stash/checkout/reset used (would have destroyed the uncommitted feature); cp-backup + sha256 revert methodology.

## Phase 3 — Full regression: PASS — 0 real findings

- **Build**: `go build ./cmd` exit 0, 3s, 26,345,456 bytes (`/tmp/ttl-gate-a-bin`). Root stub trap avoided.
- **Vet (scoped to touched pkgs)**: exit 0, no output.
- **Release-gate Pack A** (proxy/store/decision/ui/cmd, `-timeout=240s` in `timeout 300`, HOME quarantined): **9/9 ok** — pkg/proxy 130.198s (long pole), pkg/store/database 36.408s, all others sub-4s. Zero FAIL, zero panic. No store flakes fired (CloseLifecycle/PeakHourParity protocols not triggered).
- **Release-gate Pack B** (33-pkg complement, module-prefix anchor): **29 ok + 3 [no test files] + 1 pkg FAIL = expected shape**. Partition verified: 42 total − 33 B = 9 = slice A untouched. Only failure: pkg/mcp `TestHandleTestMCPServerDirect_RedirectTo{Localhost,PrivateIP}Blocked` @ handlers_api_test.go:1864/:1904 — the KNOWN QUARANTINED httpbin.org-403-before-classifier pair, signature-exact, all other mcp tests green. Feature-touched packages green: grouplb 0.712s, credentiallb 4.261s, config 0.684s (also modelscache 8.148s / skipcache / modelcache neighbors). TOCTOU and usage-stress flakes did NOT fire — zero isolated re-runs needed.
- **-race pack 1** (grouplb/credentiallb/config): 3/3 ok (2.019s / 8.073s / 3.047s), `WARNING: DATA RACE` count = 0.
- **-race pack 2** (pkg/store/...): 2/2 ok (store 1.057s, store/database 176.407s), `WARNING: DATA RACE` count = 0. CloseLifecycle + PeakHourParity passed in-run. Non-vacuity: `go test -list` confirms the mergeConfig acceptance test was in the race binary; mergeConfig is a pure function (race structurally impossible inside it) — pack value = 176s of surrounding store/engine behavior under the detector. credential_group.go (known mutex-stall area) untouched by the diff.
- **FE gate**: NOT run — NO FE files changed (expected answer confirmed).

## Findings (none blocking)

1. 🟠 **GAP-1 (test debt, criterion (c))**: no permanent automated test proves "default pick re-reaches A after expiry" (member identity). Throwaway probe PASSED (feature correct); recommend the developer adds the probe scenario as a permanent test (single-default-member pool + BackupOnly B + InjectPreconditionStateForTest + real-sleep expiry).
2. 🟢 **FINDING-2 (test robustness)**: the deletion-proof leans on `bindingsCount`; a count-mirror sabotage (delete removed, decrement kept) evades it because rebind rebalances the count unconditionally. Strengthen with a direct len(map)/stale-row-absence assertion in a follow-up.
3. 🟢 **OBSERVATION**: cmd/main.go boot wiring (`SetBindingTTL` calls) untested at runtime (cmd has no tests for it). Informational.
4. 🟢 **PACK HYGIENE**: pkg/store/database under -race = 176.4s vs 240s internal cap (~27% headroom, ~4.9× race amplification of 36.4s plain). Split the store race pack before adding tests there (mirrors the 2026-11-08 pkg/proxy 110s note).
5. ℹ️ **External event**: concurrent tidier agent appended to tracked `.agents/tidier/notes.md` mid-gate (07:15:11Z) — shifted full-diff sha only; feature-scope identity exact. No gate impact.

## Known-flake ledger this run
TOCTOU_CoolingReverify: not fired (plain or -race). TestStoreEngine_PeakHourParity: not fired (outside 23:59 UTC window). CloseLifecycle: not fired. usage stress pair: not fired. mcp httpbin pair: fired as expected (quarantined, deterministic, signature-verified).

## Scope decision
Full suite run — warranted (leader-mandated full regression; cross-module feature). Executed via registered release-gate slices A/B + 2 race slices + acceptance pack + sabotage proofs; every pack ≤ 5 min with dual-layer timeout; parallel waves (acceptance∥A∥B, then race∥race, sabotage serialized last for tree-mutation isolation).

**No code changes, no fixes, no commits. Worktree feature diff byte-identical to pre-gate (feature-scope sha proven).**
