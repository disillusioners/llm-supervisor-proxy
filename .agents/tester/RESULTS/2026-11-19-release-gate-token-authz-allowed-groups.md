# Release Gate — AUTH COMMISSION `token-authz-allowed-groups`

**Date:** 2026-11-19 · **Branch:** `feature/token-authz-allowed-groups` · **Base:** `a3f0335`
**Commissioned gate HEAD:** `81e8788` · **Gated tree after the test-only fix:** `eb137a1` (= `81e8788` + `eb137a1`)
**Corpus:** `.agents/shared/planning/token-authz-allowed-groups/` gen-8, manifest `e8f530290ed71b46e5c6bfcb0ec38efdc59b5dc7399da3e72139143044131836` — **REPRODUCED** via the pinned recipe (`phase3-plan.md:163`) before any lane ran.
**Change set:** 16 commits, 91 files, +9623/−890 (auth + enforcement across `pkg/authz`, `pkg/auth`, `pkg/proxy`, `pkg/decision`, `pkg/store`, `pkg/ui`, `pkg/imggen`, migrations 034, FE tokens UI).

> **VERDICT: HELD BY THE LEADER.** All code-level gate items are green; the final PASS/FAIL is deliberately NOT
> issued here because the corpus is being re-pinned (gen-9) for 5 corpus-only defects and a parallel test-only lane
> is landing 9 obligation pins that the §13.1a ledger does not yet name. The 9 are graded **expected-absent**, never
> as code failures. Re-verification of the landing 9 = Pack A + Pack B + FE re-run + re-sweep, then the leader's verdict.

## Scope decision
A **full** release gate was commissioned explicitly, and the blast radius justifies it: a cross-module authorization
change touching the request hot path, 4 inference lanes, the admin API, the token store, the FE token UI and a schema
migration. Scope was **not** reduced. The one scope nuance: the `-race` sweep was split into two lanes (heavy
`pkg/store/... + pkg/proxy`, light `pkg/authz/auth/modelscache/decision/imggen`) for wall-time isolation, and every
other lane was scoped to exactly one pack.

## Gate matrix

| # | Item | Result | Evidence |
|---|------|--------|----------|
| 0 | Corpus manifest reproduces (STOP-gate) | ✅ PASS | `e8f53029…31836` == pinned recipe output; HEAD `81e8788…`; branch confirmed |
| 1 | gofmt zero NEW drift vs `a3f0335` | ✅ PASS | \|F_HEAD\|=39 ⊂ \|F_BASE\|=46, **NEW=∅**; all 39 shared flagged files byte-identical `gofmt -d`; the 7 files cleaned by `81e8788` confirmed |
| 2 | Real binary build | ✅ PASS | `go build ./cmd` exit 0 → 26,817,366 B ELF, `go1.26.0`; binary deleted |
| 3 | Pack A + Pack B full runs | ✅ PASS (1 defect found → fixed → re-verified) | Pack A **9/9 ok**, 1457 PASS / 0 FAIL / 11 SKIP / 2501 RUN, 147 s · Pack B **35 pkgs**, 1187 PASS, 2 FAIL = the 2 quarantined httpbin 403s only |
| 4 | `-race` on the touched/enforcement packages | ✅ PASS | heavy `./pkg/store/... ./pkg/proxy` exit 0, **317 s**, 876 test funcs, **zero data races** · light `./pkg/authz ./pkg/auth ./pkg/modelscache ./pkg/decision ./pkg/imggen` exit 0, **47 s**, zero races; `pkg/authz` guard test RAN (GOPATH/GOMODCACHE pinned) |
| 5 | Anti-vacuous 112-pin sweep | ✅ PASS | `expected=112 (79 Go + 33 FE) | added=112 | unlisted=0 | absent=0 | duplicates=0`; each pin matched exactly once in MY canonical logs |
| 6 | G-H1 tail depth re-verification `156252a..4be0a12` | ✅ PASS | 5/5 items VERIFIED with file:line (see below); 1 council pointer mis-attribution corrected |
| 7 | FE gates | ✅ PASS | vitest **448/448** (41 files, `×`=0) · `tsc --noEmit` exit 2 **EXACT 18 errors / 9 files** parity, baseline byte-identical to HEAD · `vite build` **zero git diff** on `pkg/ui/static/` + **two-build sha256 determinism** (manifest `5564c7e4…` ×3) |
| 8 | G12 ruling checks (a)–(d) | ✅ PASS (1 wording defect reported) | (a) 0 executable `cfg.ModelID` in `race_coordinator.go` · (b) 0 hits over 161 non-test files (sole hit = deprecated `pkg/auth/token.go:260` shim) · (c) 0 executable **and** 0 comment hits for `UltimateModel`/`ExcludeFromUltimateSwitching` · (d) 034 SQL frozen (`git diff 60d0db4 HEAD -- migrations/` empty), both SQLite SET arms NULLIF-wrapped, PG guard structurally inside `DO$$…LOOP`, `pg_input_is_valid` comment-only |
| 9 | Gate artifacts | ✅ DONE | this report + `PACKS.md` note + `QUARANTINE.md` reconfirmation + `LESSONS/2026-11-19-token-authz-release-gate-lessons.md` |

## The one real defect found — and its fix
Pack B run-1 was **EXIT=1 with 2 REAL failures** (not flakes, not quarantined):

- `test/access_control_test.go:332/:335` — `TestA4b_OpenAccessEmptySlice` — expected 200, got 403 `access_denied`
- `test/integration_allowed_models_test.go:615` — `TestHandler_EmptyAllowedModels_AllowsAll` — expected 200, got 403

**Root cause:** commit `60d0db4` migrated these two tests' **setup** to the commission's conjunctive grant semantics
(`auth.FullPermission([]string{}, nil)` = a PRESENT-EMPTY models axis) but left the **pre-commission expectation**
("empty ⇒ allow all"). The shipped contract is conjunctive — UNRESTRICTED requires BOTH axes ABSENT (nil); a
PRESENT-EMPTY axis ⇒ RESTRICTED granting nothing ⇒ 403 (`pkg/authz/scope.go:28-46`, `:86-91`; commission pin
`pkg/authz/scope_test.go:47-57` "one axis present-empty is RESTRICTED and grants nothing"). **Production code was
correct; the assertions were stale.** The two tests live in the repo-root `test/` tier — **Pack-B-exclusive** — so a
green Pack A could never have caught them.

**Fix (test-only, `eb137a1`, 2 files, +23/−7, names unchanged, no production/migration/`.agents/` file touched):**
both assertions now expect 403 + the `access_denied` body shape (`type`, `message`, `model`), each with a one-line
contract citation. Premise was verified BEFORE editing (both tokens carry a present-empty axis, not nil ⇒ contract
applies ⇒ stale test, not a production defect; a nil-axis case would have stopped the lane and escalated).

**Re-verification:** targeted tests PASS; repo-root `test` package EXIT=0 (41 PASS / 0 FAIL / 0 SKIP); Pack B
re-run at `eb137a1` → 1187 PASS (**+2**, exactly the two repaired tests), FAIL 4→**2** (the quarantined httpbin pair
only), `=== RUN` **2410 unchanged** ⇒ no test gained or lost ⇒ not a vacuous green.

## 112-pin anti-vacuous sweep (item 5) — the core evidence
Canonical logs: `packA_final.log` (1457 top-level PASS), `packB_final.log` (1187), `fe_test.log` (448/448, 41 files,
`×`=0). Parsing rules: Go = top-level `^--- PASS: <name>` only (indented subtests excluded); FE = verbose vitest
titles parsed into a name→count map (not grep).

| Metric | Value |
|---|---|
| expected (ledger §13.1a, gen-8 as parsed; plan.md sha256 `f54008c0…`, gen delta 0) | 112 (79 Go + 33 FE) |
| added (`+func Test` vs `a3f0335..eb137a1`) | 112 (79 Go + 33 FE) |
| `added == expected` | TRUE (Go 79=79, FE 33=33) |
| unlisted / absent / duplicates | 0 / 0 / 0 |
| pins absent from logs WITH symbol in tree (real findings) | **0** |
| FAIL/SKIP marks on ledger names | 0 |
| fix-commit perturbation check | added-Go set for `a3f0335..4be0a12` == `a3f0335..eb137a1` (diff empty) |
| FE composition | TokenForm 13, grantAxes 8, TokenList 4, useApi 8 = 33 |
| renames counted once under the new name | `TestScanAllowedModels`→`TestScanGrant_TriState`; `TestNewRaceCoordinatorWithEmptyModels`→`TestRaceCoordinator_EmptyModelList_Refused` |
| speculative NOT-LANDED names (reconciliation) | all 7 absent from added+executed; `TestComputeConversationKey_TokenSalting` pre-existing (executed, not added) |
| **PENDING-9 (gen-9 obligation pins)** | all 9 absent from the gated tree ⇒ **expected-absent**, not failures |

PENDING-9 (absent from the gated tree `eb137a1`, landing in a parallel test-only lane; **not** counted as failures):
`TestDecision_Precedence_401_404_400_403_502` · `TestDecision_DisabledVsOutOfScope_NoLeak` ·
`TestChat_FallbackPositionGroup_NoPhantomPick` · `TestChat_ShadowClaimed_403QuotesClaimedString` ·
`TestChat_ShadowClaimed_AliasOnlyToken_403` · `TestAnthropic_ShadowClaimed_AliasOnlyToken_403` ·
`TestChat_ShadowClaimed_GroupGrant_AdmitsClaimedRequest` · `TestChainGroupEntry_NeverAdmitted_No502` ·
`TestMigration034_PGMalformedRowSkippedNotAbort`.

### Per-name verdict (112/112 — occurrence count in MY logs)
<details><summary>Go pins (79) — all <code>1 PASS</code></summary>

| # | Name | n | Verdict |
|---|---|---|---|
|1|`TestCreateToken_GrantAxesRoundTrip`|1|PASS|
|2|`TestCreateToken_ExplicitEmptyIsNotNull`|1|PASS|
|3|`TestValidateToken_SurfacesInvalidFlags`|1|PASS|
|4|`TestUpdateTokenPermission_BothAxes`|1|PASS|
|5|`TestUpdateTokenPermission_OmitsNilColumn`|1|PASS|
|6|`TestUpdateTokenPermission_RoundTrip`|1|PASS|
|7|`TestScanGrant_TriState`|1|PASS|
|8|`TestScanGrant_OnlySQLNullIsAbsent`|1|PASS|
|9|`TestScanAllowedModels_ShimPreservesIDs`|1|PASS|
|10|`TestPackage_ImportGuard_UsesPinnedToolchain`|1|PASS|
|11|`TestPackage_ImportGuard_AuthzStaysLeaf`|1|PASS|
|12|`TestPackage_ImportGuard_AuthzLeafConstraint_TransitiveClosure`|1|PASS|
|13|`TestPackage_ImportGuard_NegativeControlFires`|1|PASS|
|14|`TestPackage_ImportGuard_AuthzImportsOnlyAuth`|1|PASS|
|15|`TestAuthzResolve_TruthTable`|1|PASS|
|16|`TestAuthzResolve_ConjunctiveWildcard`|1|PASS|
|17|`TestAuthzResolve_NoMembersExpansionForAbsentGroups`|1|PASS|
|18|`TestAuthzScope_AllowsEmptyID`|1|PASS|
|19|`TestAuthzResolve_EmptyTextGrantIsDenyAll_NotWildcard`|1|PASS|
|20|`TestReachFilter_InterfaceShape`|1|PASS|
|21|`TestCachedTokenStore_ForwardsGroupsAndDropsOnUpdate`|1|PASS|
|22|`TestCachedTokenStore_CloneCarriesInvalidFlags`|1|PASS|
|23|`TestCachedTokenStore_ClonePreservesPresentEmpty`|1|PASS|
|24|`TestMigration034_ForwardFreshDB`|1|PASS|
|25|`TestMigration034_SQLiteJSON1Available`|1|PASS|
|26|`TestMigration034_Repartition`|1|PASS|
|27|`TestMigration034_RepartitionIdempotent`|1|PASS|
|28|`TestMigration034_NullNotEmptyArray`|1|PASS|
|29|`TestMigration034_PGGuardShape`|1|PASS|
|30|`TestMigration034_PGuardShape_DownTwinAndGlobalScan`|1|PASS|
|31|`TestMigration034_SQLiteDialectParity`|1|PASS|
|32|`TestTokenAPI_CreateEchoesGroups`|1|PASS|
|33|`TestTokenAPI_PatchMergeSemantics`|1|PASS|
|34|`TestTokenAPI_PatchClearDoesNotEscalate`|1|PASS|
|35|`TestTokenAPI_Validation`|1|PASS|
|36|`TestTokenAPI_PatchNonexistent_404`|1|PASS|
|37|`TestTokenAPI_Validation_MalformedShape`|1|PASS|
|38|`TestTokenAPI_PatchNilAxis_DoesNotHealDenyAll`|1|PASS|
|39|`TestTokenAPI_PatchOmittedInvalidAxis_RealStore_ByteIdentical`|1|PASS|
|40|`TestTokenAPI_InvalidFlagsExposed`|1|PASS|
|41|`TestTokenAPI_StaleClaimAlias_Rejected`|1|PASS|
|42|`TestExcludeGroupMemberScoped_LegacyFallback_RefusesOutOfReachReplacement`|1|PASS|
|43|`TestRewriteGroupEntriesScoped_LegacyFallback_DegradesUnscoped`|1|PASS|
|44|`TestChat_ChainEntry_OutOfScope_NeverSpawned`|1|PASS|
|45|`TestAnthropic_ChainEntry_OutOfScope_NeverAttempted`|1|PASS|
|46|`TestChat_GroupMember_OutOfScope_NeverPicked_SiblingServes`|1|PASS|
|47|`TestChat_Reentry_OutOfScopeTarget_NeverSpawned`|1|PASS|
|48|`TestAnthropic_Reentry_OutOfScopeTarget_NeverAttempted`|1|PASS|
|49|`TestPackage_NoPerAxisLenGuardOutsideAuth`|1|PASS|
|50|`TestPackage_ImportGuard_NoIsModelAllowedInLanes`|1|PASS|
|51|`TestPackage_NoUltimateEscalation`|1|PASS|
|52|`TestPackage_ImportGuard_NoScopeInHotLoop`|1|PASS|
|53|`TestLaneContract_ReachResolutionOnlyViaAuthz`|1|PASS|
|54|`TestGroupRewriterScoped_ProductionDecoratorForward`|1|PASS|
|55|`TestChat_OutOfScopeModel_403_ZeroAttempts`|1|PASS|
|56|`TestChat_WildcardToken_Unchanged_200`|1|PASS|
|57|`TestChat_GroupGrant_AdmitsMembers_200`|1|PASS|
|58|`TestChat_Group_AllMembersOutOfScope_403_Not502`|1|PASS|
|59|`TestChat_DynamicMembership_ReflectsImmediately`|1|PASS|
|60|`TestChat_DenyAll_InvalidGrantRow_403`|1|PASS|
|61|`TestChat_EmptyTextGrantRow_DenyAll`|1|PASS|
|62|`TestReachCompleteness_ExecutionListsSubsetOfReach`|1|PASS|
|63|`TestAnthropic_Group_AllMembersOutOfScope_403_EnvelopeParity`|1|PASS|
|64|`TestAnthropic_OutOfScopeModel_403_ZeroAttempts`|1|PASS|
|65|`TestAnthropic_WildcardToken_Unchanged_200`|1|PASS|
|66|`TestDecision_Group_NotGranted_403_ZeroAttempts`|1|PASS|
|67|`TestDecision_Group_Granted_NotScopeDenied`|1|PASS|
|68|`TestImggen_NotGranted_403`|1|PASS|
|69|`TestDecisionGroup_FilterDoesNotMutateCachedRow`|1|PASS|
|70|`TestHedge_And_CredFailover_NeverIntroduceModel`|1|PASS|
|71|`TestRaceCoordinator_EmptyModelList_Refused`|1|PASS|
|72|`TestPackage_ImportGuard_StoreDatabaseNoAuthImports`|1|PASS|
|73|`TestScopeExclusion_NeverWritesCooldown_MultiToken`|1|PASS|
|74|`TestGroupScopeExclusion_AllMembersOutOfScope_ScopeExcluded`|1|PASS|
|75|`TestGroupScopeExclusion_AllMembersDisabled_NotScopeExcluded`|1|PASS|
|76|`TestGroupExcludeScoped_ScopeOnlySignal`|1|PASS|
|77|`TestGroupScopeExclusion_NeverPicked_AnyPhase`|1|PASS|
|78|`TestFallbackPositionGroup_Unreachable_Dropped`|1|PASS|
|79|`TestIndexZeroGroup_Unreachable_FailsWithScopeExcluded`|1|PASS|

</details>

<details><summary>FE pins (33) — all <code>1 PASS</code></summary>

| # | Name | n | Verdict |
|---|---|---|---|
|80|`TokenForm_PickerIncludesDecisionAndImageGenModels`|1|PASS|
|81|`TokenForm_PickerExcludesGroupModels`|1|PASS|
|82|`TokenForm_GroupPickerEmitsAllowedGroups`|1|PASS|
|83|`TokenForm_GroupIdNeverEmittedUnderAllowedModels`|1|PASS|
|84|`TokenForm_DecisionModelEmitsUnderAllowedModels`|1|PASS|
|85|`TokenForm_UnrestrictedSendsNullAxis`|1|PASS|
|86|`TokenForm_RestrictedEmptySendsEmptyArray`|1|PASS|
|87|`TokenForm_RestrictedSelectionSendsIdArray`|1|PASS|
|88|`TokenForm_ClearingGroupsNeverSendsNull`|1|PASS|
|89|`TokenForm_EditModeCannotPromoteAxisToUnrestricted`|1|PASS|
|90|`TokenForm_InvalidAxisRendersDenyAllWarning`|1|PASS|
|91|`TokenForm_AxisWithoutInvalidFlagIsNotReportedInvalid`|1|PASS|
|92|`TokenForm_EditModeKeepsStaleGroupIdOnGroupsAxis`|1|PASS|
|93|`axisOfId: target-only group id stored in allowed_models stays on models axis`|1|PASS|
|94|`axisOfId: stored group id stays on groups axis`|1|PASS|
|95|`axisOfId: a NEW group id (not stored) classifies by kind onto groups axis`|1|PASS|
|96|`TokenList_TargetOnlyGroupId_SurvivesNoChangeSave`|1|PASS|
|97|`TokenForm_NewGroupSelection_LandsOnGroupsAxis`|1|PASS|
|98|`TokenList_BothAxes_NoChangeSave_KeepsIdOnBothAxes`|1|PASS|
|99|`TokenList_CrossAxisAddition_IsPreserved`|1|PASS|
|100|`TokenList_DuplicateStoredIds_AreDedupedPerAxis`|1|PASS|
|101|`TokenList_BadgeShowsWildcardOnlyWhenBothAxesAbsent`|1|PASS|
|102|`TokenList_SaveSplitsAxesAndRoundTripsBoth`|1|PASS|
|103|`TokenList_InvalidAxisRendersDenyAllWarning`|1|PASS|
|104|`TokenList_GroupChipsAreDistinctFromModelChips`|1|PASS|
|105|`useApi_CreateToken_SendsBothAxesNullWhenNothingSelected`|1|PASS|
|106|`useApi_CreateToken_SendsEmptyArrayForRestrictedEmptyAxis`|1|PASS|
|107|`useApi_CreateToken_SendsIdArrayForRestrictedAxis`|1|PASS|
|108|`useApi_UpdateTokenPermission_SendsEmptyArrayToClearAxis`|1|PASS|
|109|`useApi_UpdateTokenPermission_OmitsUntouchedAxis`|1|PASS|
|110|`useApi_UpdateTokenPermission_ExplicitNullMeansKeep`|1|PASS|
|111|`useApi_UpdateTokenPermission_SendsIdArrayToReplace`|1|PASS|
|112|`useApi_UpdateTokenPermission_OptimisticMergeUpdatesBothAxes`|1|PASS|

</details>

## G-H1 tail depth re-verification (item 6) — file:line evidence
Range `156252a..4be0a12` = 5 commits, 12 files, +483/−56 — enumerated exactly: `1e423de` (C2 DTO flags + C3 axis
escalation) · `63e9665` (C2 BE pin + C3 FE pins) · `ce8f2f5` (W1 stale-claim) · `0be8d13` (W2 conjunct) · `4be0a12`
(F1 per-axis save + F2 comment + static rebuild). HEAD `81e8788` is 1 commit outside the range and touches 4 reviewed
files — **comment-only**, no semantic drift.

1. **W1 row-identity guard — VERIFIED.** `pkg/ui/server.go:2809` `if row == nil || row.ID != id { return nil }`
   (fallback path of `tokenGrantRow` :2793-2813); primary path :2797-2801 is claim-blind `GetModelNative`
   (claim-blindness at `pkg/modelscache/models.go:1526`, `pkg/store/database/store.go:1095`, `pkg/models/config.go:622`);
   consumers :2849/:2860. Pin `TestTokenAPI_StaleClaimAlias_Rejected` (`pkg/ui/handlers_token_flags_test.go:138`,
   fixture `staleClaimModelsConfig` :120, redirect :130-136) asserts **POST ⇒ 400 and PATCH ⇒ 400**.
2. **W2 conjunct — VERIFIED at all 4 arms.** `handler.go:1073` (403 :1084 → 502 `all_models_failed` :1099) ·
   `handler.go:1313` (403 :1314 → `handleRaceFailure("all_models_failed")` :1317) · `handler_anthropic.go:600`
   (403 :604 → 502 :617-620) · `handler_anthropic.go:867` (403 :868 → terminal :872-885).
   **Attribution correction (not a defect):** the council's "handler.go:1214 twin" pointer was a **mis-attribution** —
   at `391f181` that line was the `groupTriedMembers` harvest, not a 403 arm; the semantic twin is the `scopeExhausted`
   arm whose `!rc.headersSent` landed in `156252a` (the range BASE), i.e. *before* W2. No groupPick arms exist in
   `pkg/decision`/`pkg/imggen` (0 hits); non-groupPick 403 arms are pre-loop ⇒ necessarily pre-attempt.
3. **C2 DTO flags — VERIFIED.** Fields `pkg/ui/server.go:2505-2506` (bool, **no omitempty** ⇒ always emitted ⇒
   INVALID ≠ ABSENT on the wire); populated `:2680-2681` (list) and `:2764-2765` (create) from `pkg/auth/token.go:48-49`.
   Pin `TestTokenAPI_InvalidFlagsExposed` (`handlers_token_flags_test.go:16`) asserts the wire shape:
   invalid row ⇒ (true,true), healthy ⇒ (false,false), POST-created ⇒ (true,false). PATCH returns `{"success":true}`
   (:2653) and the FE re-fetches ⇒ no flag gap.
4. **C3/F1 per-axis authoritative save — VERIFIED.** Classifier `grantAxes.tsx:113-132`: **stored-membership-first**
   at `:122` (models) / `:123` (groups); the kind-branch applies only to NEW ids `:126-129`. All **6/6** ledger pins
   present in `grantAxes.test.tsx` at `:26, :34, :39, :49, :96, :174` (+3 bonus F1 pins `:192, :209`). F1 implemented at
   `TokenList.tsx:220-223` and `TokenForm.tsx:102-108`.
5. **Static bundle — VERIFIED.** Deny-all copy present in `pkg/ui/static/assets/SettingsPage-Dwn9gaSg.js` (models +
   groups variants + "Deny-all — allow-list unreadable"). Bundle archaeology proves the C3 fix: the `7ce1787` bundle
   carried the **defective** kind-first classifier; `1e423de` carried the corrected stored-membership-first classifier;
   HEAD carries neither (F1 removed their only consumer) and carries the F1 per-axis writes + C2 flags.

## FE gate outputs (item 7)
| Check | Result |
|---|---|
| `npx vitest run --reporter=verbose` | exit 0, **41 files / 448 tests / 448 passed / 0 failed / 0 `×`**, 16 s wall |
| `npx tsc --noEmit` | exit **2** (expected) — measured **18 errors / 9 files**, per-file table **identical** to the committed `tsc-baseline.txt` (diff empty); baseline file unmodified (sha256 `1bc60a57…` == HEAD) |
| `npm run build` #1 | exit 0, 6 s — `git diff --stat -- pkg/ui/static/` **EMPTY**; `status --porcelain` empty |
| build determinism | manifests before / after-1 / after-2 **byte-identical** (all `sha256 5564c7e4dc619f55bdb4d29418ccd731837c1fdcd2f388e184e0cff85572dd76`); content-hashed asset names identical across both builds |
| exit state | tree clean w.r.t. `pkg/ui/static` (only the pre-existing `.agents/` session edits) |

## ensure.md validation
| Requirement | Priority | Result | Method |
|---|---|---|---|
| All Go unit tests pass (`go test ./...`) | Critical | ✅ PASS (adjudicated) | Pack A (9 pkgs) + Pack B (35 pkgs) = **every** `go list ./...` package, `-count=1`, dual-layer timeout. Sole failures = the 2 quarantined httpbin 403s |
| `go vet ./...` clean | Critical | ✅ PASS | house pack `test/packs/build_gate_test.sh` → `RESULT: PASS` (build+vet), no findings |
| Full project builds | Critical | ✅ PASS | `go build ./cmd` exit 0 + build_gate pack |
| `tsc --noEmit` zero NEW vs baseline | Critical | ✅ PASS | exact 18/9 parity, baseline unmodified |
| Peak-hour rows (cross-midnight, 400 on non-internal upstream, round-trip) | Important | ➖ not in change set | branch touches no peak-hour code; pre-existing rows, out of blast radius |
| Migration 018 SQLite+PG validity | Important | ➖ not in change set | this branch ships migration **034** (validated under item 8d); 018 untouched |
| No race conditions | Nice-to-have | ✅ PASS | `-race` on all touched/enforcement packages — **zero data races** |
| Coverage of boundary conditions | Nice-to-have | ✅ PASS (2 gaps noted) | 112/112 pins executed; O2 below |

### ensure.md Improvement Notices (user-owned file — not modified)
1. **Critical #1's method** is written as a bare `go test ./...`, which conflicts with the house pack/timeout
   discipline (unbounded command, no dual-layer timeout, `-count=1` absent). Validated the INTENT pack-mapped
   (Pack A + Pack B slices). Suggested rewrite: *"All Go packages pass — run the release-gate slices (Pack A + the
   module-anchored Pack B complement) with `-count=1`; see PACKS.md §Release-Gate Slices."*
2. **"locations-vs-counts" wording** — the tsc requirement is correctly count-based here (18/9), but the house
   convention elsewhere compares error *locations*; keeping counts + file-set is the stable form (line numbers drift).
3. `go test ./...` in Critical #1 also does not cover the FE suite or `go vet`; the house pack split (build_gate +
   FE packs) is the accurate description of the real gate.

## Non-blocking findings / follow-ups (no must-fix)
- **O1 (FE hygiene)** `axisOfId` / `splitByAxis` now have **zero production consumers** (F1 removed their consumer) ⇒
  tree-shaken from the bundle. Dead-export cleanup for the FE lane.
- **O2 (coverage gap)** the "target-only group id survives save" pin covers `TokenList` (inline editor) but **not** the
  `TokenForm` **edit** path. Source preserves the id (`TokenForm.tsx:50` → `:102-108`) ⇒ no behaviour defect, but the
  models picker hides group rows (`:84`) so the id survives invisibly. A follow-up pin is recommended.
- **O3 (assertion strength)** the W1 pin asserts **status only** (400), not the `"stale-alias"` error body. Sound in
  context (the sole 400 source on both paths is `validateTokenGrants`), but asserting the message would harden it.
- **O4 (legacy shim)** `pkg/auth/token.go:258 IsModelAllowed` still carries legacy fail-open semantics with **zero
  production callers** (4 unit tests still pass ⇒ green-but-vacuous coverage). Consistent with the council's existing
  legacy-shim follow-up.
- **O5 (pre-ship data check — leader item E1)** the deny-all semantics mean live rows with `allowed_models=''` /
  `allowed_groups=''` are now DENY-ALL; the pre-ship count on both hosts is still required (not a test artifact).
- **O6 (gate budget)** the commissioned `-race` budget of 1200–1500s was a sum-projection; real wall was 317s
  (`max(pkg)`, packages run concurrently). Tighten future race budgets toward ~600 s.

## Quarantine / flake status
- **Fired (quarantined, non-blocking):** the 2 httpbin.org redirect tests in `pkg/mcp` (`handlers_api_test.go:1864/:1904`)
  — deterministic third-party 403, identical signature across every prior gate; `pkg/mcp` is untouched by this
  commission ⇒ branch-orthogonal. QUARANTINE.md updated with the 2026-11-19 reconfirmation.
- **Did NOT fire:** `TestStoreEngine_CloseLifecycle`, `TestStoreEngine_PeakHourParity`,
  `TestEngine_GetOrSelect_TOCTOU_CoolingReverify` — each passed outright in Pack A / Pack B / both `-race` slices.
  No adjudication, no registry change.
- **Harness incident (environmental, not a code defect):** Pack A run-1 showed `pkg/ui` + `cmd` `[setup failed]`
  (`pattern static/*: no matching files found`) because the parallel FE lane's `vite build` emptied `pkg/ui/static`
  mid-compile (`outDir='../static'` wipes before write). Isolated re-run EXIT=0, and the canonical Pack A re-run
  (no FE build running) was **9/9 ok, 0 `[setup failed]`**, with an aggregate identical to the two-run adjudicated
  aggregate ⇒ the incident was environmental. Lesson + POK recorded.

## Artifacts
- This report: `.agents/tester/RESULTS/2026-11-19-release-gate-token-authz-allowed-groups.md`
- `.agents/tester/PACKS.md` — 2026-11-19 gate note; Pack B package count corrected 34 → **35** @ `eb137a1` (+`pkg/authz`)
- `.agents/tester/QUARANTINE.md` — 2026-11-19 reconfirmation bullet (httpbin pair fired; other three rows did not)
- `.agents/tester/LESSONS/2026-11-19-token-authz-release-gate-lessons.md` — 8 durable lessons
- Test-only fix: `eb137a1` (2 files, +23/−7) — the gate artifact commit SHA is reported separately
- Ephemeral logs (not committed): `/tmp/gate-81e8788/{packA_final.log, packB_final.log, fe_test.log, fe_tsc.log,
  race_heavy.log, race_light.log, static.log, tail.md, pins_sweep.tsv, pins_sweep.md, static_*.txt}`

## What remains before the final verdict
1. gen-9 corpus re-pin lands → re-verify the manifest reproduces (same pinned recipe) at the new generation.
2. The 9 obligation pins land in the test-only lane → re-run **Pack A + Pack B + FE** at the new tree and re-sweep
   the grown ledger (121 pins) so the new pins carry literal PASS evidence, not just source presence.
3. Then the leader issues the final gate verdict. **No production code, corpus, or frozen 034 SQL was edited by this
   gate** — the single code-tree change is the test-only assertion correction `eb137a1`.

## Post-gate provenance check — shared-worktree contamination audit

After the artifact commit, a read-only audit was run because the worktree showed
`pkg/store/database/migrations_test.go` as **MODIFIED** — a file belonging to the PARALLEL test-only lane that is
landing the 9 obligation pins (the 9th pin, `TestMigration034_PGMalformedRowSkippedNotAbort`, lives in exactly that
file). It was **never staged** in `eb137a1` or `135e1e8` (that file's committed history ends at `391f181`), and the
frozen migration SQL was untouched. The audit changed nothing (the parallel lane's work was left undisturbed).

**Result: SAFE — no gate evidence was contaminated.** Three independent proofs:

1. **Log name-set (mtime-independent, decisive).** `packA_final.log` contains exactly **8** `TestMigration034_*`
   RUN/PASS names — byte-for-byte the `eb137a1` set (ForwardFreshDB, SQLiteJSON1Available, Repartition,
   RepartitionIdempotent, NullNotEmptyArray, PGGuardShape, PGuardShape_DownTwinAndGlobalScan, SQLiteDialectParity).
   The 9th name is absent and `grep -c 'pgQuotedLiterals' packA_final.log` = **0** ⇒ the `pkg/store/database` binary
   that printed `ok … 54.876s` (log line 10917) compiled a tree **without** the new pin.
2. **Filesystem sweep.** `find . -path ./.git -prune -o -type f -newermt '2026-11-19 03:02:39' ! -newermt '2026-11-19 03:05:07' -print`
   → **EMPTY**. No worktree file of any kind has an mtime inside the Pack A window.
3. **Timeline.** The dirty file's write = **03:21:14.261** (new inode — a *replace*, not an in-place edit), i.e.
   16m 08s after Pack A closed, 11m 20s after Pack B closed, 5m 03s after the pin sweep. The git index write at
   03:21:23.236 is the `135e1e8` commit's own staging.

Pack B was audited on the same basis: the `eb137a1` fix files carry mtimes 03:06:49 / 03:06:55 (inside the Pack B
window by mtime) but are **byte-identical to HEAD** (`git diff HEAD` clean, `git status -- test/` empty) ⇒ Pack B
compiled exactly the committed `eb137a1` content.

Integrity spot-check: `grep -c '^--- PASS:' packA_final.log` = **1457**, matching the canonical top-level PASS count
exactly — the canonical artifact is self-consistent.

Method note (POK `gate-evidence-provenance-by-test-name-set`): a past `go test` run's source version is proven by the
**set of test names in its log** against the file's `func Test` list; an inode `Birth` dates a replace and the git
index mtime dates a commit — an mtime alone cannot date an edit.

## Re-verification recipe for the gen-9 landing (≈4 min)
1. Re-verify the corpus manifest with the **same pinned recipe** at the new generation.
2. Re-run **Pack A** (9 pkgs, ~147 s) + **Pack B** (35 pkgs, ~50 s) + **FE vitest/tsc** (~25 s) at the new tree with
   `-count=1` and the dual-layer timeout; write to NEW log files (keep this gate's logs as the `81e8788`/`eb137a1` record).
3. Re-sweep the grown §13.1a ledger — the 9 new pins must carry **literal PASS evidence in the new logs**, not merely
   source presence; set arithmetic must be re-proven against the new base→tip diff.
4. The leader then issues the final verdict.
