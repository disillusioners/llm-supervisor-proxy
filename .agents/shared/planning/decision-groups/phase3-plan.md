# Phase 3: Verification Gate + Ship/Deploy Runbook — decision-groups

**Commission:** decision-groups (decision models served by a GROUP; admin "Decision Models" container tab with a vertical `[models] [group]` nav).
**Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-decision-groups` — **use this worktree only, never the main checkout** (the main checkout holds `master` + ~72 uncommitted `.agents/` edits; merging there is forbidden).
**Branch:** `feature/decision-groups` @ **`2b987bf`** (== `origin/latest` at plan time, after the ADR-16 base re-anchor; the worktree was re-anchored `24c51ef` → `2b987bf` with the planning docs untracked and verified byte-identical). **Upstream is deliberately UNSET** (anomaly 0679c552) — a bare `git push` from this worktree would target the integration line; the ship runbook uses explicit-SHA refspecs only.
**Base for drift/pin comparisons:** `BASE = $(git merge-base feature/decision-groups origin/latest)` — at plan time `2b987bf94e39abfc9df515cbbadc2bb335dbb47c`; `origin/master` at plan time **`fd1649c`** (v194). **Every base assertion below that reads `24c51ef` is superseded by `2b987bf` (ADR-16).**
**Author:** verification/ship worker (phase-3 planner). **Date:** 2026-11-16. **Status:** Draft — pending approver review.

> **AUTHORITY FENCE — read before anything else.** This document is a **runbook for a FUTURE authorised operator**. Nothing in it was executed while writing it. The commission explicitly forbids: committing, pushing, deploying, and touching the project KB / history / POK (the leader owns those). This plan **specifies** those actions as instructions; it does **not** perform them. Every gate/build/deploy command below is written as a runbook instruction with its expected output.

**Binding inputs.** This phase gates the contracts in `.agents/shared/planning/decision-groups/decisions.md` §"Contracts the phase plans must honour" (backend #1–#22, frontend #23–#27) and D-1..D-8. It does **not** re-litigate the ruling.

---

---

## Amendment 1 (2026-11-16) — user ruling consequences for the gates

**Authority.** `decisions.md` — the **ADRs — USER RULING** block (`decisions.md:573-807`): **ADR-5** (pick is policy; a mark suppresses an ATTEMPT, never a pick), **ADR-6** (R3 order of operations; a member is not failed until its OWN resilience is consumed; this **retracts** the ADR-2/E-6 "member-level only" framing), **ADR-7** (chain-only / deliberately-inactive models are supported, not a defect), **ADR-8** (R5 racing must be consumable), **ADR-9** (coordination — a PARALLEL commission fixes the TEXT lane; this release must not change text-lane behaviour), **ADR-10** (the consolidated criteria **AC-R1..AC-R10**), **ADR-11** (the OQ-1..OQ-4 closures + **AC-C1 FORMALLY VOID**), **ADR-12** (RULING-3 — the three **per-layer caches** + **AC-R11**).

**Precedence.** Where this amendment and an earlier section of this plan disagree, **this amendment wins** (`decisions.md:575-577`). The earlier text is **not silently rewritten** — it carries a `*(SUPERSEDED by Amendment 1 — …)*` marker in place, listed in §8 below.

**Nothing here is code.** No build, no test run, no gate run, no commit, no push.

### §1 — G4's final lists (re-derived, not patched)

**Go — 50 ADDED top-level test names** (41 Pack A + 9 Pack B), enumerated verbatim in § Execution order step 3. **Derivation:** sweep every table row of the amended `phase1-plan.md` (1523 lines), take the backticked `Test*` names on rows not marked `EXISTING`, subtract the 3 `SUPERSEDED` names in `§A16` ⇒ **54** raw; minus the **4** LEFT-ALONE existing tests referenced only in the `## Known breakage sites` prose ⇒ **50**, **set-identical** to the dispatch's list. **Collision check against the base tree (`24c51ef`): 0 collisions.** Per-file: `pkg/decision/handler_group_test.go` **27** · `pkg/store/database/store_group_test.go` **9** · `pkg/models/group_rules_test.go` **5** · `pkg/credclass/classify_test.go` **2** · `pkg/modelscache/models_group_lane_test.go` **2** · `pkg/ui/kind_rules_parity_test.go` **2** · the three `pkg/proxy` files **1 each**.

> **Two names live outside the `## Test plan` table** — `TestGroup_AddModel_DecisionMembers_MixedProviders_Accepted` (`phase1-plan.md:639`) and `TestKindRules_Parity_Group_DecisionMembers_MixedProviders` (`:640`) sit in the `## R3 — Provider vocabulary` proof table. The expected list is therefore a **union across the whole plan document**, not the Test-plan table alone.

**WITHDRAWN (phase1 §A16) — must NOT be implemented; G4 fails if one appears in the diff (R21):**

| Withdrawn name | Replaced by |
|---|---|
| `TestDecision_Group_MarkedMember_DemotedNotFiltered` | `TestDecision_Group_MarkedHead_ZeroUpstreamCallSkip` + `TestDecision_Group_AllMarked_PolicyHeadHonestAttempt` + `TestDecision_Group_PolicyPick_IgnoresMarks` |
| `TestDecision_Group_NoCooldownWriteOnBenchedPick` | `TestDecision_Group_NoCooldownWriteOnSuppressedPick` |
| `TestGroup_RewriteGroupEntries_DecisionGroup_PicksDecisionMember` | `TestGroup_RewriteGroupEntries_DecisionGroup_RefusedOnTextSweep` |

**FE — phase2 Amendment 1, "FINAL FE test list":**

| Bucket | Files | Cases |
|---|---|---|
| **NEW** (5) | `DecisionModelsContainer.test.tsx` (A1–A13) · `GroupModelsTab.decisionLane.test.tsx` (B1–B7 + B9) · `GroupMemberEditor.decisionLane.test.tsx` (C1–C7) · `GroupModelForm.decisionLane.test.tsx` (D1–D6 + D8–D10) · `groupLane.test.ts` (E1–E8) | **45** |
| **UPDATED** (4) | `GroupModelsTab.test.tsx` · `GroupModelForm.test.tsx` · `GroupMemberEditor.test.tsx` (**+ T1** — the text picker still offers an **inactive** chat model: the decision-lane exclusion must not leak) · `EventLog.test.tsx` (**+ F1** — an event type outside the FE's `EventType` union renders through the generic fallbacks) | **2 new** (T1, F1); the 3 mechanical files keep their assertions unchanged |
| **LEFT ALONE** (8) | `SettingsPage.tabs.test.tsx` · `DecisionModelsTab.test.tsx` · `DecisionModelsTab.decisionTest.test.tsx` · `DecisionModelForm.test.tsx` · `useDecisionModels.test.ts` · `useGroupModels.test.ts` · `useApiCredentials.test.ts` · `tsc-baseline.test.ts` | 0 |

**FE total: 48 new cases** (46 in the 5 new files + T1 + F1). The tsc verdict is **UNCHANGED** — `tsc-baseline.txt` stays `TOTAL_ERRORS=19` / `TOTAL_FILES=9`, the same nine-file set, `SettingsPage.tsx` 6, `ModelsTab.tsx` 6, seven files at 1; Amendment 1 adds **no new source file**, and a new file with an error is a **FAILURE, never a baseline bump**.

### §2 — The exhaustion criterion is RE-DERIVED (ADR-5 / ADR-6 / ADR-10; phase1 §A4)

**Old criterion (WITHDRAWN):** "every base-servable member of both pools has had its honest attempt ordered". **New criterion (binding):**

> The 502 `decision_all_members_failed` fires **only** when (a) **every member × credential** pair has been **genuinely attempted** and **failed**, and (b) no unattempted candidate remains. **Suppressed attempts are not attempts and not failures** — they never contribute to the failure count. Per-credential attempts count **individually**.

**AMENDED by Amendment 2 (ADR-13) — the criterion is CLASS-CONDITIONAL.** "Only fail after every member genuinely failed" is now read **per the failure-class policy**: **a member that 5xx'd HAS genuinely failed for this request** (its remaining credentials are neither attempted nor failed — they are **not reached**, because the 5xx advances immediately). Pinned by `TestDecision_Group_Exhaustion_5xxMemberCountsAsGenuinelyFailed`. **AC-R4 caveat (phase1 §B5):** AC-R4's literal wording ("the member's own credentials attempted before advancing") is **class-conditional** — under a 5xx the member advances before its remaining credentials are consumed; the criterion's *intent* (rotation-eligible failures consume the member's own resilience first) is preserved, and **ADR-13 governs**. Wherever this document paraphrases AC-R4, **ADR-13 is cited alongside it**.

The event carries `outcome=all_members_failed` plus `attempted` (genuine attempts), `skipped` (suppressed attempts), `members_attempted` (distinct members attempted); the **wire** still carries only the one code (the detail is operator-facing, never client-facing).

**Effect on this runbook:** any gate text that counted attempts at member granularity is superseded. The E2E expectations (§ Deploy runbook step 7, rows E6/E7) now assert the `attempted` / `skipped` / `members_attempted` counters, with `skipped = 0` for a transport-failure probe (not limit-class ⇒ no mark ⇒ no suppression). The phase-1 row `TestDecision_Group_AllMembersFail_502DecisionAllMembersFailed` carries an inline assertion "the attempt count == the member count" that is **SUPERSEDED** by §A4's criterion (the count is over member × credential, suppressed attempts excluded).

### §3 — AM-3: the `pkg/credentiallb` import legality is FORMALLY UNVERIFIED *(SUPERSEDED by Amendment 3 / ADR-14 — CLOSED, LEGAL unconditionally)*

`phase1-plan.md §A19 → AM-3` (Medium): the per-credential rotation reaches into `pkg/credentiallb` — "import-guard-legal per ADR-6, **not yet formally verified**". The formal guard is the `go list -deps` closure walk (`pkg/decision/handler_test.go:90`, `helpers_test.go:19-39`); a read-only static check found `pkg/credentiallb` importing only `pkg/models` + its own subpackages — no `pkg/proxy` edge.

> **CLOSED by Amendment 3 / ADR-14 (2026-11-16) — verdict: LEGAL, unconditionally.** *(This row's "not yet formally verified" text is **SUPERSEDED**.)* `pkg/decision` **already carries `pkg/credentiallb` in its transitive closure** — `decision → auth → store/database → credentiallb`, a **non-test dep** — so the guard has been passing with that edge present all along. See § Amendment 3 §1.
>
> **Characterization correction (ADR-14 item 1):** `pkg/credentiallb` is **NOT stdlib-only** — it imports `pkg/models` (which imports `pkg/crypto`), and the package has **8** `.go` files, not the 3 the research memo enumerated (`engine.go`/`binding.go`/`failure_class.go`). The conclusion is unaffected (the closure is still clean of the banned paths), but **no phase document should repeat the stdlib-only or 3-file phrasing**. *(The `pkg/grouplb` stdlib-only and `pkg/credclass` stdlib-only claims are **correct** and stay.)*

**Consequence for the gate:** the guard is a **TEST, not a compiler constraint** — it fails at **gate** time, not at build time. ⇒ **new precondition P0.9** (run the three import-guard tests **EARLY**, before the full gate) and **new abort row R22** (guard failure ⇒ **abort with a re-plan**; the per-credential design depends on that path).

> **SUPERSEDED by Amendment 3 / ADR-14 (2026-11-16).** AM-3 is **CLOSED — LEGAL, unconditionally**: `pkg/decision` already carries `pkg/credentiallb` in its **non-test** transitive closure (`decision → auth → store/database → credentiallb`), so the guard has been passing with that edge present all along. The **abort-with-a-re-plan branch (R22) is withdrawn**; P0.9 becomes a **verification recipe** (both guard layers, pinned go1.26 toolchain, non-test-deps scope, evidence grade) — see § Amendment 3 §1.

### §4 — Two new gate-relevant facts

**(a) AM-2 — SUPERSEDED by ADR-16 (C2-2).** The earlier framing of this item ("the decision lane gets its OWN skip-cache instance … **never shared**") is **void**. The invariant is **LAYER separation, not instance separation**: `pkg/skipcache/skipcache.go:4-17` declares itself "a **process-wide** registry … read by credential selection + attempt suppression ONLY"; `cmd/main.go:242`/`:249` construct one instance each and `:255-256` hand **both** to the proxy config (one instance, both lanes); upstream health is per-credential, not per-lane. A decision-lane mark can never influence text-lane ordering **structurally** — because the pick is policy-only and the mark's reader is per-credential — not because two instances never share state. Enforced structurally by `TestCacheLayer_Structural_PackageLevelNoCacheImports` (`pkg/proxy/cache_layer_separation_test.go:752-786`). **The Wave-A instance-separation test is WITHDRAWN** as pinning a wrong invariant, replaced by `TestDecision_Group_CacheLayerSeparation_SharedUpstreamInstanceOneAnswer`.

> ⚠ **An operator verifying instance separation would chase an IMPOSSIBLE invariant and file a false defect.** The verification target is **LAYER separation**: (i) the pick reads only GROUP-layer policy facts (no `pkg/skipcache` / `pkg/modelcache` import in the pick/walk files — `TestPackage_ImportGuard_GroupPolicyFiles_NoCacheImports`); (ii) a credential-layer mark provably cannot alter the group pick (`TestDecision_Group_LayerSeparation_CredentialMarkCannotChangePick`); (iii) the `skipcache` package doc (`:4-15`) declares the LAYER CONTRACT and is cited read-only; (iv) the caches are constructed once and shared (`cmd/main.go:242`/`:249`/`:255-256`). An "instance separation" check has no meaningful pass/fail and must not be reported as a defect.

**(b) AM-1 — phase 1 needs an exported `pkg/grouplb` policy selector.** `pkg/grouplb/selector.go:26`/`:52` are unexported; AM-1's preferred path is an **additive exported API** in `pkg/grouplb` (text-lane-neutral: a new API, no behaviour change), with a small local picker in `pkg/decision` as the fallback. **Pack membership is UNCHANGED either way:** `pkg/grouplb` is **Pack B**, `pkg/decision` is **Pack A**, and neither choice creates a package ⇒ **Pack A stays 10 packages, Pack B stays 32**; the G2/G3 commands are unaffected.

### §5 — AC renumbering (ADR-9's AC-R9 → **AC-R12**)

`decisions.md:735-737`: the criterion originally numbered **AC-R9 inside ADR-9** ("text-lane behaviour is byte-untouched by this release") is now **AC-R12**; **AC-R9 in ADR-10's table** ("doctrine line present **verbatim** at the seam") is **unchanged**. They are **different criteria**. Any citation reading `AC-R9 (ADR-9)` must read **`AC-R12 (ADR-9)`**.

This plan's own criterion references (which used the ruling's contract numbering 1–22, not the AC ids) are unaffected, but the new citations added by this amendment use the corrected forms: **`AC-R12 (ADR-9)`** for the text-lane byte-untouched fence (the coordination rule in § Ship runbook) and **`AC-R9 (ADR-10)`** for the doctrine line (pinned by `TestDecision_DoctrineLine_PinnedVerbatimAtSeam`).

### §6 — AM-4: an OPEN LEADER DECISION (recorded, not resolved)

`phase1-plan.md §A19 → AM-4` (Low, "flagged for the leader"): the ADRs do not spell out how a **non-limit-class failure (5xx)** inside a member is treated by the member-failed predicate. Phase 1's loop **rotates to the member's next credential on ANY failed attempt**, with the failure class only seeding the cooldown. **This plan does not resolve it.** If the leader rules otherwise (e.g. **5xx advances without rotating**), the per-credential test set changes ⇒ **G2/G4 must be re-baselined** against the revised phase-1 table before the gate may run. Recorded as **precondition P0.10** and **abort row R20**.

### §7 — Coordination: the ship step must SURFACE conflicts, never FORCE them

ADR-9 / **AC-R12 (ADR-9)**: a parallel commission is bringing the TEXT lane to the same semantics; this release must not change text-lane behaviour, and a conflict in a hotspot file is *"resolved by **re-deriving from the merged semantics**, never by taking one side wholesale"*. The hotspot file list, the four-step ship rule (surface → re-derive → re-run the full gate → report every hotspot conflict), and the text-lane proof set are in **§ Ship runbook, Step 1**.

### §8 — Index of the in-place corrections this amendment made to this document

| § of this document | Correction | Marker placed |
|---|---|---|
| Gate matrix **G4** | 33 → **50** names (41 Pack A + 9 Pack B) + the 3 WITHDRAWN names + FE 5 NEW / 4 UPDATED / 8 LEFT ALONE | *(SUPERSEDED by Amendment 1 §1)* on the ledger |
| Gate matrix **G13** | unchanged by the amendment (the `pkg/config` fence from E-3 still holds) | — |
| § Execution order **step 3** | the expected list replaced with the final 50 + withdrawn list + FE NEW/UPDATED lists | *(SUPERSEDED by Amendment 1 §1)* |
| § Execution order **step 6** | added the FE-UPDATED-file checks and the T1/F1 case checks | *(AMENDED by Amendment 1 §1)* |
| § Text-lane regression pins — the ledger note | 33 → 50; the "14 vs 16" note **SUPERSEDED** | *(SUPERSEDED by Amendment 1 §1)* |
| § Text-lane regression pins — "RECEIVE NEW FUNCTIONS" | the decision-lane row now cites the re-derived semantics (per-credential attempts, policy pick) | *(AMENDED by Amendment 1 §2)* |
| § Deploy runbook **step 7 (E6/E7)** + § Evidence to retain | exhaustion counters `attempted` / `skipped` / `members_attempted` | *(AMENDED by Amendment 1 §2)* |
| § Deploy runbook **step 6** | the AM-2 second-skip-cache-instance operator note | *(AMENDED by Amendment 1 §4a)* |
| § `pkg/credclass` placement verdict | pack membership after the amendment (no package moves) | *(AMENDED by Amendment 1 §4b)* |
| § **0. Preconditions** | new **P0.9** (AM-3 guard legality), **P0.10** (AM-4 open decision); **P0.8** re-scoped by ADR-6 — **P0.9 superseded by Amendment 3 §1, P0.10 superseded by Amendment 2 §2** | *(AMENDED by Amendment 1 §3, §6)* |
| § **Risks & abort criteria** | new **R20** (AM-4), **R21** (withdrawn name present), **R22** (guard fails ⇒ re-plan) | *(AMENDED by Amendment 1 §1, §3, §6)* |
| § **Ship runbook** | the coordination block — hotspot list + surface-never-force rule | *(AMENDED by Amendment 1 §7)* |
| § **Cross-plan reconciliation** | new row **R16** | *(AMENDED by Amendment 1)* |
| (the whole section) | **the entire Amendment-1 framing is superseded by Amendment 2** where it conflicts — see § Amendment 2 §5 for the per-site index | *(SUPERSEDED by Amendment 2)* |

### §9 — What the amendment did NOT resolve (carried forward)

- **ADR-1 (kind immutability) is NOT folded** into phase 1 (`phase1-plan.md §A20`): it rules that kind retyping is blocked in the same release, which **contradicts** phase 1's E-2 record ("the kind-immutability guard sits OUTSIDE this commission's fence"). The **single-member re-lane hole** E-2 documents therefore remains open until ADR-1 is folded into a stage. **The gate list is unaffected** (the affected test is `TestGroup_GroupLane_InfersChat` / `_InfersDecision` / `_MixedGroupReturnsFalse`, all present), but **decide before implementation**.
- **ADR-2's evidence stands** (credential multiplicity is representable but inert on this lane today); its *decision* is retracted by ADR-6.
- **ADR-3 / ADR-4** (the `data-center-scripts` pin-push question; the environment facts for deploy + E2E) are **out of scope** for the phase plans — the § Ship runbook / § Deploy runbook in this document keeps its own handling and cites the environment facts as point-in-time (§ Not verified).
- **AC-R10 token names** are not fixed by the ruling; the FE's `EventType` union extension + curated labels are deferred to the phase-1 handoff, in the same commit as the BE change. Until then the generic fallbacks carry the trio — pinned by **F1**.
- **The shadow-target picker** is not narrowed to exclude inactive models (ADR-7 rules on group candidates; a shadow claim is a third population). A one-line change plus one test case if the leader wants it — **not implemented here**.

---

---

## Amendment 2 (2026-11-16) — ADR-1 fold + AM-4 ruling

**Authority.** `decisions.md` **ADR-13** — the second tranche of leader rulings (`decisions.md:813-846`): the **AM-4 failure-class policy** (mirrored EXACTLY from the text lane), the **picker asymmetries KEPT AS PLANNED**, the **marker negative control PINNED**, and the **`ExcludeGroupMember` legality** note — plus **ADR-1** (kind immutability for group members, closes Q3, `decisions.md:426-461`). Phase 1 carries the fold as `## Amendment 2` (`phase1-plan.md:1564+`, file now **1699 lines**): **§B1** ADR-1 · **§B2** AM-4 ruled · **§B3** the marker negative control · **§B4** the test-name delta · **§B5** the flags.

**Precedence.** Where this amendment and an earlier section of this document disagree, **this amendment wins**. The earlier text carries `*(SUPERSEDED by Amendment 2 / ADR-…)*` markers in place and stays visible (indexed in §5 below). The **G0–G14 matrix is NOT renumbered** and no gate is weakened.

**Nothing here is code.** No build, no test run, no gate run, no commit, no push.

### §1 — G4's final list: 59 Go names + 4 withdrawn

**Go — 59 ADDED top-level test names** (50 Pack A + 9 Pack B), enumerated verbatim in § Execution order step 3. **Re-derived independently from the 1699-line phase-1 plan** (whole-document table sweep → rows not marked `EXISTING` → subtract the **4** `SUPERSEDED` names and the **4** LEFT-ALONE prose references) ⇒ **59**, **set-identical** to the final list. **Collision check vs base (`24c51ef`): 0.**

**Delta over Amendment 1: +10 names, −1 withdrawn ⇒ 50 → 59.**

| Fold | Added |
|---|---|
| **ADR-1** (§B1, `§B4`) | `TestUpdateModel_KindChange_GroupMember_Rejected` · `TestUpdateModel_KindChange_GroupMember_RejectionNamesGroupAndMember` · `TestUpdateModel_KindChange_NonMember_Succeeds` (all `pkg/store/database/store_group_test.go`) · `TestKindRules_Parity_KindImmutability` (`pkg/ui/kind_rules_parity_test.go`) · `TestGroup_SingleMemberDecisionGroup_MemberRetypeRefused` (`pkg/store/database/store_group_test.go`) |
| **AM-4** (§B2, `§B4`) | `TestDecision_Group_MemberWithTwoCredentials_LimitClassRotatesBeforeAdvancing` · `TestDecision_Group_5xx_AdvancesWithoutRotationOrMark` · `TestDecision_Group_401_RotatesWithinMember` · `TestDecision_Group_400_413_415_422_FailFastAdvanceBlocked` · `TestDecision_Group_Exhaustion_5xxMemberCountsAsGenuinelyFailed` (all `pkg/decision/handler_group_test.go`) |

**WITHDRAWN — now 4** (the earlier three + `TestDecision_Group_MemberWithTwoCredentials_AttemptsBothBeforeAdvancing` → replaced by `…_LimitClassRotatesBeforeAdvancing`; the old name no longer says what it tests, since rotation is **class-conditional** per ADR-13). **Same enforcement: a withdrawn name present in the diff ⇒ G4 FAIL (R21).**

**FE lists are UNCHANGED by Amendment 2** (phase 2 is not amended): **5 NEW** files (45 cases) · **4 UPDATED** files (2 new cases: **T1**, **F1**) · **8 LEFT ALONE** ⇒ **48 new FE cases**; the tsc verdict stays `TOTAL_ERRORS=19` / `TOTAL_FILES=9` with the same nine-file set.

### §2 — AM-4 is RULED (ADR-13:815-829): the lane mirrors the text-lane failure-class policy EXACTLY

| Failure class | Decision-lane behaviour | Upstream-layer write? |
|---|---|---|
| **rotation-eligible** (limit-class + upstream 401/403) | **rotate within the member** — the member's next credential is attempted | **YES** — per classification (`ExcludeAndReselectForClass`) |
| **5xx** | **ADVANCE to the next member immediately** — no within-member rotation, **no mark, no cooldown write** (transient, provider-attributable) | **NO** |
| **memberAdvanceBlocked** (400/413/415/422 + client-cancel) | **fail-fast advance-blocking**, as the text lane | **NO** |

Text-lane cites: `pkg/proxy/cred_failover_class.go:162` (`rotationEligible`), `:190` (`memberAdvanceBlocked`); marks fire on limit-class only (`pkg/proxy/race_coordinator.go:2106-2107`).

**Withdrawn:** Amendment 1 §A5's "rotate to the member's next credential on ANY failed attempt". **Re-derived member-failed predicate (AC-R5):** `memberResilienceExhausted(member)` is TRUE when the member's own **rotation-eligible** credential attempts are consumed **and** failed, **OR** the member's last failure was a **5xx**, **OR** an **advance-blocking** class fired; the chain clause stays **ABSENT** (OQ-2).

**Exhaustiveness re-read:** a member that **5xx'd HAS genuinely failed for this request**; its remaining credentials are neither attempted nor failed — they are **not reached**. Pinned by `TestDecision_Group_Exhaustion_5xxMemberCountsAsGenuinelyFailed`.

**AC-R4 caveat (phase1 §B5):** AC-R4's literal wording is **class-conditional** — under a 5xx the member advances before its remaining credentials are consumed. The criterion's *intent* (rotation-eligible failures consume the member's own resilience first) is preserved; **ADR-13 governs**. **Every AC-R4 paraphrase in this document now cites ADR-13 alongside it.**

**`ExcludeGroupMember` legality (ADR-13:843-846):** usage for **genuine-failure advance bookkeeping** (the request-scoped tried-set; cooldown writes only after genuine failures) **remains legal**; using it to **express a mark/suppression** or **as an ordering device** stays barred. Amendment 1's design (the lane performs suppression itself; the sweep is not consumed) already complies — **no gate change**.

### §3 — The marker NEGATIVE CONTROL is now a PINNED requirement (ADR-13:836-841)

`decision_all_members_failed` is **a NEW Go string this feature introduces**, so the marker check is now a **two-sided pinned control**:

- **positive (blocking):** present in the new build — `strings -a <new-binary> | grep -c -- 'decision_all_members_failed'` ⇒ **≥ 1**;
- **negative (blocking):** **ABSENT from the live v193 binary**, verifiable against the `~/llmproxy-verify/verify_image.sh` cache for the **v193 per-SHA tag** (`b11d7c8` — per-SHA tags are immutable ⇒ the cache entry is safe; moving tags go stale):
  ```bash
  strings -a ~/llmproxy-verify/cache/b11d7c8/llm-supervisor-proxy     | grep -c -- 'decision_all_members_failed'    # MUST be 0
  ```
  **Measured 0 on 2026-11-16** — the negative control already holds, so a post-ship ≥ 1 is a genuine delta;
- **no false-hit classes:** `grep -ci sticky` stays **forbidden** (Tailwind / static-UI false hits) — `grep -c` with the exact literal only.

The harness's own controls must stay > 0, else the extraction is broken — **measured at BOTH tiers**: `quota_exceeded` = 2, `ExcludeAndReselectForClass` = 2, `openrouter` = 5 (v193 `b11d7c8` **and** v194 `fd1649c`). Also measured at v194: `member_suppressed` = 0 and `member_skipped` = 0 — consistent with ADR-15's rename rationale (the decision lane's pick/attempt/suppress trio does not exist yet, and v193's `member_skipped` namespace is absent from v194).

### §4 — ADR-1's fold: the kind-immutability guard, and the D-8 branch conditional

**The guard lands THIS release** (phase1 §B1): the model-update write path (`pkg/store/database/store.go:1699`, `UpdateModel`) refuses a **kind change** when the row is a member of **any** group; group save/validation is **not** the fence; a model that is **not** a member of any group may still be retyped (**scoped**, no blanket immutability); the rejection message **names both ids**; the error is asserted at the canonical write path **and** the API PUT, independently (helper ≡ PUT parity). This **closes the E-2 single-member re-lane hole** — `TestGroup_SingleMemberDecisionGroup_MemberRetypeRefused` is the pin.

**The D-8 branch conditional (a G4 rule, not a gate change).** ADR-1's membership derivation reuses the **already-forwarded** `GetModels` (`pkg/models/config.go:533`), so **D-8's one-method budget stays spent on `GroupLane`** — **no new manager method**. **IF** the implementation proves a new method unavoidable, D-8 / contract 14 requires a decorator forward + a **decorator-through test whose name is NOT among the 59**. ⇒ **G4's rule for that case:** such an extra added test name is **REPORTED FOR ADJUDICATION, NOT AUTO-FAILED** — a **whitelist-of-one** mechanism. It is recorded in the RESULTS report with the **phase1 §B1** citation (`:1595`) and the approver decides. **Every other unlisted name remains a FAIL** (R3).

### §5 — Index of the in-place corrections this amendment made to this document

| § of this document | Correction | Marker placed |
|---|---|---|
| Gate matrix **G4** | 50 → **59** names (50 Pack A + 9 Pack B); 3 → **4** WITHDRAWN; the **D-8 branch** adjudication rule added | *(AMENDED by Amendment 2 §1)* on the row and on the ledger |
| § Execution order **step 3** | expected list → the final 59 + the 4th withdrawn name + the D-8 branch note | *(SUPERSEDED by Amendment 2 §1)* |
| § Execution order **step 6** | added the **D-8 branch adjudication report** (whitelist-of-one) | *(AMENDED by Amendment 2 §1, §4)* |
| § **0. Preconditions P0.10** | AM-4: **OPEN LEADER DECISION → RULED** (ADR-13's failure-class policy) | *(SUPERSEDED by Amendment 2 §2)* |
| § **Risks & abort criteria R20** | the open-decision row → the ruled policy + the one remaining conditional (the D-8 branch) | *(SUPERSEDED by Amendment 2 §2, §4)* |
| § **Amendment 1 §2** (exhaustion criterion) | the criterion is now **class-conditional**; a 5xx'd member **counts as genuinely failed**; the **AC-R4 caveat** (ADR-13 governs) | *(AMENDED by Amendment 2 §2)* |
| § **Deploy runbook step 7 (E6)** and § **Evidence to retain** | "genuinely failed" is per the failure-class policy (ADR-13) | *(AMENDED by Amendment 2 §2)* |
| § **Deploy runbook step 5.1** (marker) | the **negative control** is now a **pinned requirement** (absent from the v193 binary) | *(AMENDED by Amendment 2 §3)* |
| § **Cross-plan reconciliation** | new row **R17**; R1 marked superseded-and-re-derived (50 → 59) | *(AMENDED by Amendment 2)* |

### §6 — What Amendment 2 did NOT resolve (carried forward)

- ~~**The `pkg/credentiallb` import legality is still formally UNVERIFIED** (AM-3).~~ **CLOSED by Amendment 3 / ADR-14 — LEGAL, unconditionally.** ADR-13 settled the *policy* (which class rotates); ADR-14 settled the *import question* — `pkg/decision` already carries `pkg/credentiallb` in its non-test transitive closure, so the guard has been passing with that edge present all along. **P0.9 / R22 no longer stand as an abort gate**; what remains is the verification recipe (§ Amendment 3 §1). *(This bullet is the last open AM-3 item in the document.)*
- **ADR-1's exact stage placement** (phase1 Stage **3b**) and its five acceptance criteria are phase-1's; the gate sees them through the 5 new ADR-1 tests.
- **The picker asymmetries are KEPT AS PLANNED** (ADR-13:831-834): `laneModels` untouched; decision candidates = kind **AND** enabled; **the text picker keeps offering inactive chat models**, pinned by FE test **T1**. Changing the text picker is the **parallel commission's** call, not this release's.
- **AC-R10 token names** remain unfixed; the FE's `EventType` union extension + curated labels stay deferred to the phase-1 handoff (same commit as the BE change), with **F1** pinning the generic-fallback path meanwhile.

---

---

## Amendment 3 (2026-11-16) — AM-3 closure

**Authority.** `decisions.md` **ADR-14** — *AM-3 CLOSED: `pkg/credentiallb` is import-legal from `pkg/decision` (developer verdict, 2026-11-16)* (`decisions.md:850-881`). Completion-grade evidence: developer `b14df3b4`.

**Effect.** **AM-3 is CLOSED: verdict LEGAL, unconditionally.** The G0–G14 matrix is **NOT renumbered**, **no gate is weakened**, and the **G4 name lists are UNCHANGED — 59 Go names (50 Pack A + 9 Pack B) and 48 FE cases (5 NEW files / 46 cases *(dated figure — the current FE ledger is 45 NEW-file cases / 48 tokens after ADR-19)* + 4 UPDATED files / 2 new cases T1 + F1)**. What changes is the **status** of the import-legality item: from an open question with an abort branch to a **verification recipe**.

### §1 — The verification recipe (replaces the abort gate)

**The headline fact (makes the question a no-op):** `pkg/decision` **already carries `pkg/credentiallb` in its transitive closure today** — `decision → auth → store/database → credentiallb`, a **non-test dep** (verified independently: `go list -deps ./pkg/decision` lists `pkg/credentiallb`). The guard's closure walk has been **passing with that edge present all along**; a direct import adds nothing the guard has not already accepted. **P0.9's old "convince the guard" branch is a no-op.**

**(a) Run BOTH guard layers.** Layer 1 = the three guard **tests** (`pkg/decision/handler_test.go:40-63` / `:65-88` / `:90-109`). Layer 2 = the three `grep -rnE` gates the earlier research **missed**, wired into `make test` at **`Makefile:51-68`** (the `test:` target; the gates at `:54-57` / `:59-62` / `:64-67`; `go test ./...` at `:68`) — **plain `go test` alone does NOT run them**, so the operator must run `make test` (or the three greps explicitly).

**(b) Use the PINNED go1.26 toolchain** for every manual closure check — `/home/nea/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.linux-amd64/bin/go`. `/usr/bin/go` (1.22.2) **fails on go.mod** offline: it tries to download go1.26 and reports `toolchain not available`.

**(c) Scope note:** the guard's transitive `go list` walk covers **non-test deps only**.

**Evidence grade to record in the RESULTS report:** literal `=== RUN` / `--- PASS` from the three guard tests; clean closures; a **real direct-import scratch in `/tmp`** that passes ALL guards (positive control); and a **negative control** — a **QUOTED banned path in a doc comment that FAILS the guard** — proving the raw-text mechanism works in both directions.

### §2 — Stale AM-3 citations cleaned

| Site | Was | Now |
|---|---|---|
| § Amendment 2 §3 (the AM-3 row) | "import-guard-legal per ADR-6, **not yet formally verified**" | **CLOSED by Amendment 3 / ADR-14** — verdict LEGAL, unconditionally + the characterization correction (§3 below) |
| § **Not verified** item 13 | "only becomes relevant if the user rejects the member-level scope reduction" | **doubly stale** — the reduction framing was retired by **ADR-6** (member × credential is the order of operations) **and** ADR-11; the item is now **CLOSED** and states the surviving RECIPE |
| § **Risks & abort criteria R22** | "guard FAILS ⇒ **ABORT WITH A RE-PLAN**" (CRITICAL) | **SUPERSEDED** — no longer an abort gate; a guard failure now means the **recipe was not run correctly** (wrong toolchain, or `go test` instead of `make test`) |
| § **Risks & abort criteria R19** | "the scope reduction is reversed … the guard question is UNVERIFIED" | **SUPERSEDED** — the dead framing is retired (ADR-6 / ADR-11 / ADR-13); its only surviving content (the guard question) is **CLOSED by ADR-14** |
| § **Cross-plan reconciliation R13** | "the reduction is **pending user sign-off**" | **RESOLVED → SUPERSEDED (ADR-6/ADR-11/ADR-13) → CLOSED (ADR-14)** |

### §3 — Characterization correction (ADR-14 item 1)

`pkg/credentiallb` is **NOT stdlib-only**: it imports `pkg/models` (which imports `pkg/crypto`), and the package has **8** `.go` files — not the 3 the research memo enumerated (`engine.go` / `binding.go` / `failure_class.go`). The conclusion is unaffected (the closure is still clean of the banned paths), but **no phase document should repeat the stdlib-only or 3-file phrasing**. *(Verified independently: `pkg/credentiallb` → `pkg/models` → `pkg/crypto`; 8 non-test files.)* The **`pkg/grouplb` stdlib-only** and **`pkg/credclass` stdlib-only** claims are **correct** and stay.

### §4 — The raw-text guard trap (ADR-14 item 2)

The `pkg/decision` import guard scans **every `.go` file in the package dir, including `_test.go`**, as **raw text** — a **comment that merely QUOTES a banned path FAILS the guard** (proven by ADR-14's negative control). Consequences for the gate and for the implementer:

- The **doctrine-line pin** and any **layering documentation** written **inside `pkg/decision`** must **not name** those paths — not even in a comment or a doc string.
- This is a **different mechanism** from the cachelayer AST pin — there is **no AST precision** here, so "it's only prose" is not a defence.
- The operator must therefore read the **guard's own output** (which file and line) before concluding a phase-1 change broke something.

### §5 — Placement constraint (ADR-14 item 3)

**Credential-aware logic lives IN `pkg/decision`, NEVER in `pkg/grouplb`.** The upstream-layer cache reader/writer and the per-credential rotation must not be pushed down into `pkg/grouplb`, which **bans `pkg/credentiallb`** (`pkg/grouplb/engine_test.go:401`, `TestEngine_ZeroCredentialImportGuard` — "the engine itself must not know credentiallb at all"). The gate sees this as: `pkg/grouplb`'s zero-credential guard must stay green, and `pkg/grouplb` must remain in **Pack B** with no credential-aware code pushed into it.

### §6 — Index of the in-place corrections this amendment made to this document

| § of this document | Correction | Marker placed |
|---|---|---|
| § **0. Preconditions P0.9** | AM-3 open question → **CLOSED (LEGAL)**; abort branch → **verification recipe** (two guard layers, pinned toolchain, non-test-deps scope, evidence grade) | *(SUPERSEDED by Amendment 3 §1)* |
| § **Risks & abort criteria R22** | abort-with-a-re-plan → superseded; a failure now means the recipe was mis-run | *(SUPERSEDED by Amendment 3 §1, §2)* |
| § **Risks & abort criteria R19** | dead scope-reduction framing retired; guard question closed | *(SUPERSEDED by Amendment 3 §2)* |
| § **Amendment 1 §2 / § Amendment 2 §3** | the AM-3 characterization row gets the closure marker + the characterization correction | *(AMENDED by Amendment 3 §2, §3)* |
| § **Not verified** item 13 | item closed; double supersession marked | *(SUPERSEDED by Amendment 3 §2)* |
| § **Execution order step 7** | the import-guard block rewritten: the **second guard layer** (`Makefile:51-68`) added, the stale `Makefile:38-56` citation corrected, the **raw-text trap** and the **placement constraint** recorded, the pinned-toolchain requirement stated | *(AMENDED by Amendment 3 §1, §4, §5)* |
| § **Cross-plan reconciliation** | new row **R18**; R13 marked superseded-and-closed | *(AMENDED by Amendment 3)* |
| **G4's name lists** | **UNCHANGED** — 59 Go (50 Pack A + 9 Pack B) + 4 WITHDRAWN + 48 FE cases | *(explicitly unchanged by Amendment 3)* |

### §7 — What Amendment 3 does NOT change

- **G0–G14 numbering, every gate, and G4's name lists** — untouched (59 Go / 48 FE).
- **The 59-name derivation** — untouched; the withdrawn list stays at **4**; the D-8 branch adjudication rule stands.
- **AM-4's ruled policy, the exhaustion criterion, the marker negative control, ADR-1's fold, and the ADR-9 coordination rule** — all as folded by Amendments 1 and 2.
- **`pkg/decision/doc.go:6-15`** still claims imports `{models, auth, usage, events}` only, while the real closure already includes `config`, `store/database`, `grouplb`, `skipcache`, `toolrepair`, `credentiallb`, `crypto` (ADR-14 item 4). **Phase 1 Stage 1 must amend `doc.go`**, or the feature reads as a guard violation from day one — **noted, not gated here** (it is a phase-1 task, not a phase-3 gate).

---

---

## Amendment 4 (2026-11-16) — review cycle 1 + base re-anchor + architect fold

**Authority.** `decisions.md` — **ADR-15** (review-cycle-1 leader decisions + ruled warnings, `:885-947`), **ADR-16** (the v194 base re-anchor `24c51ef` → `2b987bf`, `:951-1001`), **ADR-17** (the architect final fold: four RED gaps, the retraction, D5 enforced, AC-R4 upgraded, four rulings, `:1033-1107`), plus the **planner's RED-1 refusal adjudication** (`:1090-1098`) and the **reviewer's three re-verification items** (ADR-15's Wave-B list, `:938-943`). Phase 1 carries the fold as **Amendments 4–6** (final: **2504 lines**); phase 2 as **Amendment 2** (final: **889 lines**).

**Effect.** The **G0–G14 matrix is NOT renumbered** and **no gate is weakened**. Every base assertion in this document moves `24c51ef` → **`2b987bf`**. The G4 lists are re-derived to the **FINAL** set.

### §1 — G4's FINAL lists

**Go — 69 ADDED top-level test names (60 Pack A + 9 Pack B).** Re-derived independently from the **2504-line** phase-1 plan (whole-document table sweep → rows not marked `EXISTING` → subtract the **5** withdrawn names and the LEFT-ALONE prose references) ⇒ **69**, **set-identical** to the final list, **0 base collisions** against `2b987bf`. Per file: `pkg/decision/handler_group_test.go` **34** · `pkg/store/database/store_group_test.go` **16** · `pkg/models/group_rules_test.go` **5** · `pkg/ui/kind_rules_parity_test.go` **3** · `pkg/credclass/classify_test.go` **2** · `pkg/modelscache/models_group_lane_test.go` **2** · `pkg/proxy/handle_models_imggen_test.go` **2** · `pkg/proxy/handler_anthropic_misroute_test.go` **2** · `pkg/decision/handler_test.go` **1** · `pkg/proxy/handler_test.go` **1** · `pkg/proxy/handle_chat_misroute_test.go` **1**.

**WITHDRAWN — 5:** `TestDecision_Group_MarkedMember_DemotedNotFiltered` · `TestDecision_Group_NoCooldownWriteOnBenchedPick` · `TestGroup_RewriteGroupEntries_DecisionGroup_PicksDecisionMember` · `TestDecision_Group_MemberWithTwoCredentials_AttemptsBothBeforeAdvancing` · **`TestDecision_Group_SkipCacheInstance_NotSharedWithTextLane`** — the last pinned a **wrong invariant**: AM-2 was re-derived to a **SHARED** upstream instance with a **layer-separation** fence (ADR-16), so it is replaced by `TestDecision_Group_CacheLayerSeparation_SharedUpstreamInstanceOneAnswer`.

**FE — 48 new cases** (phase 2 Amendment 2 §5 + ADR-19): **5 NEW** files (**45** cases: A1–A13, B1–B7 + B9, C1–C7, D1–D6 + D8–D10, E1–E8) · **5 UPDATED** files (**3** new cases: **T1**, **F1**, **V1**) · **7 LEFT ALONE**. **B8 and D7 were REMOVED as vacuous** (they asserted the absence of a field the group form never had — `GroupModelPayload` has no provider key); their intent is subsumed by **D5**, upgraded to an exact key-set assertion, and the real provider pin is now **V1** — the FE half of ADR-15's **T5** set-equality pin (the rendered provider surface is **exactly** `{openai, typesafe, openrouter}` and is disjoint from the credential vocabulary).

### §2 — CG-1 (BLOCKING): the Tier-1 / Tier-2 split

`pkg/proxy/handle_models_imggen_test.go` **moves from Tier-1 (whole-file byte-identity) to Tier-2 (per-function check)** — phase 1 **edits** it (`TestHandleModels_FilterDecisionGroup` + `TestHandleModels_FilterDecisionGroup_ThroughCachedConfig`), so a whole-file byte-identity assertion would be a **false failure**. The Tier-2 recipe (no pre-existing function removed or renamed; every new function shows an end-anchored `--- PASS`) and the re-derived Tier-1 list are in § MUST PASS UNCHANGED.

### §3 — CG-3 (BLOCKING): G11 is ONE verdict rule

The former **G11-a / G11-b** pair is collapsed into **one** rule: *the regenerated `pkg/ui/static` assets must ride **the same commit** as the FE source change that caused them; an **EMPTY** `git status --porcelain pkg/ui/static` is **NOT a failure*** — it is what a deterministic rebuild of already-committed assets produces. The old G11-a asserted the porcelain output must be NON-EMPTY, which is **wrong** against a deterministic rebuild (and conflicted with the zero-mod premise it was paired with). The step-9 `sha256sum` before/after proof is retained as the mechanism claim.

### §4 — CG-5 (BLOCKING) + ADR-17: the rollback LADDER

**Tier-1 = the currently-live v194** (`:fd1649c`; daoduc `helm rollback 37`) · **Tier-2 = v193** (`:261622cf` / tag `b11d7c8`; daoduc `helm rollback 36`). **Expected CI ≥ 195** (CI #194 was taken by `fd1649c`).

**FRESH-VERIFY RULE — every number carries it.** The ladder encodes **tiers**, not frozen values. Before any deploy, re-read: the **live image tag** on primary, the **daoduc Helm revision**, the **primary deployment generation**, the **GitOps pin values**, the **CI run number**, and the **v194/v193 tags + digests** — each from the live system / giter's fresh state, **never copied from this document**. A stale number here is a hazard, which is why it is written as a rule rather than a constant.

### §5 — ADR-16: the base re-anchor and the fence re-definition

**Base moved `24c51ef` → `2b987bf`** (v194 `fix/cache-layer-separation`, 36 files +3507/−630, 15 commits; `origin/latest` = `2b987bf`, `origin/master` = `fd1649c`). The worktree was re-anchored; the planning docs are untracked and verified byte-identical.

**The fence is re-defined: "no v194 semantic regression", not "text-lane byte-untouched".** v194 **deliberately changed the text lane** (the sweep is policy-headed and cache-blind; the proxy does per-layer suppression), so the invariant is that this release must not move the text lane **relative to `2b987bf`**.

**Tier-1/Tier-2 pins re-derived against v194's NEW pin names.** v194 **rewrote** `pkg/store/database/store_group_skipcache_test.go`: the old `…HealthAwareSelection` (`:62`) and `…NilCacheByteIdentical` (`:165`) are **GONE**, and the **meaning is inverted** (the old tests asserted "the marked member is never picked"; the new ones assert "**the mark does not change the pick**"). The pins of record are `TestGroup_SkipCache_MarksDoNotChangeThePick` `:59` · `…ProviderWallDoesNotChangeThePick` `:85` · `…AllMarked_StillServes` `:106` · `…MarkNeverBenchesOrderingSeam` `:128` · `…ProviderMarkDoesNotWallSibling` `:147` — **verified in-tree at `2b987bf`**.

**The hotspot list is EXTENDED with the v194 files:** `pkg/modelcache/` (the new **MODEL** layer — `modelcache.go`/`policy.go`/`reset.go`), `pkg/proxy/race_executor.go`, `pkg/proxy/cache_layer_separation_test.go` (the **AST pin**), plus the re-listed `store_group_rewrite.go` and `handler_helpers.go`.

**Re-run the FULL gate on the merge REGARDLESS of conflict** (not conflict-only) — a clean merge is not a reason to skip the re-gate.

**The win-clear helper was renamed by v194:** `clearWinnerUpstreamSkip` → **`clearWinnerLayerState`** (`pkg/proxy/handler_helpers.go:873`, doc `:840-872`), and it now clears **all three layers** (GROUP / MODEL / UPSTREAM).

### §6 — The four RED-gap gates

**RED-1** (fallback-chain bypass) → `TestGroup_RewriteGroupEntries_DecisionGroupInChatChain_BoundaryRefusal` + `TestUpdateModel_FallbackChain_DecisionGroupTarget_Rejected`, with the **planner-endorsed refusal semantics**: direct-named ⇒ `FailedGroupID`; fallback-position ⇒ **drop-and-continue, no rotation row**, and the **healthy primary does NOT 502**. **RED-2** (AST-pin pick placement) → `TestPackage_ImportGuard_GroupPolicyFiles_NoCacheImports`; the pick stays in `pkg/store/database` inheriting the AST pin, and **`store_group_policy.go` is RETRACTED** (must appear nowhere but as a retracted proposal). **RED-3** (lane gate on 3 of 4 consumers) → the three `_ThroughCachedConfig` effect tests **plus** the compile-time `var _` assertions — **a removed decorator forward breaks the BUILD, not just a test**. **RED-4** (contract-15 `/v1/models`) → `TestHandleModels_FilterDecisionGroup_ThroughCachedConfig`, pinning the **fold-over-ALL-models** nuance: **disabled members count for lane inference**, so a group with a disabled decision member is still excluded. Full table in § The four RED-gap gates (ADR-17).

### §7 — The three re-verification checks (each NAMED)

| # | Check | Where it lands | Verdict |
|---|---|---|---|
| **V1** | **Decorator enumeration re-verified at `2b987bf`** — `pkg/modelscache/models.go`'s forwarded surface is **UNCHANGED** (`pkg/modelscache` was **zero-diff** in v194; the `SetSkipCache → ModelSkipCache` seam change touched the **manager + proxy config**, not the decorator's forwarded set) ⇒ **`GroupLane` is still the ONLY new `ModelsManager` method** (phase1 §E7) — D-8's one-method budget is intact, and the decorator-through test is still mandatory | § Amendment 4 §7 (this table) + P0.9 + the Tier-2 `pkg/modelscache/models_group_lane_test.go` row | **PASS (recorded)** — no change to the gate |
| **V2** | **D-2's negative controls SPELLED** — the four named controls (§ The four RED-gap gates) with what each would have caught | same | **PASS (recorded)** — the four names are in the 69 |
| **V3** | **AM-3's P0.9 runs BOTH guard layers** — the three guard **tests** + the `Makefile:51-68` grep layer via `make test` | P0.9 + § Execution order step 7 | **PASS (recorded)** — unchanged from Amendment 3 |

### §8 — The warnings folded

| # | Warning | Disposition |
|---|---|---|
| **W4** | **CI recovery** — ~~`workflow_dispatch` **IS first-class** (`docker-ci.yml:6-11`) … **Manual dispatch on `master` rebuilds and publishes with no null-content commit**~~ **SUPERSEDED (B1, tester gate RED): a dispatch run is COMPILE-ONLY — every publish step is gated on `github.event_name == 'push'` (`docker-ci.yml:110/:117/:121/:136/:153`), so a manual dispatch can NEVER publish.** The recovery ladder is § Ship runbook step 3 (rung 1 = admin re-run of the ORIGINAL push-triggered run; rung 2 = NON-ZERO-DELTA content retrigger). Separately, the **tsc gate uses `bash -c` + `PIPESTATUS[0]`** (a bare `\| tee` swallows the exit code). | § Ship runbook step 3 + § Execution order step 9 |
| **W5** | **E2E AllowedModels 403 false-negative PRE-CHECK before E5** (a token whose `AllowedModels` excludes the group yields 403 — the harness defect, not failover evidence) + the **all-marked case is LOCAL-HARNESS-ONLY** (the `.invalid` probe is a transport error, **not** limit-class ⇒ it can never demonstrate the mark rules; the deterministic Go unit tests own that) | § Deploy runbook step 7, rows **E4b** and the all-marked note |
| **W6** | **E2E exit criterion = KEEP the group** (D-B) — the created group is the user's desired end state; the E2E stays non-destructive; **never delete the group** | § Deploy runbook step 7, row **E8b** |
| **W7** | **Stale count prose** — the live gate text's `33`/`41`/`50`/`46` are corrected to **69** (60 + 9) and FE **47**; the Amendment 1/2/3 sections keep their numbers as **dated historical records** (the progression 33 → 50 → 59 → 69 is recorded in § Cross-plan reconciliation R1) | § Execution order steps 3 and 6 |
| **W8** | **Dedupe the FE-updated-files heredoc** — the FE file lists are now **three disjoint heredocs**: NEW (5) / UPDATED (5) / LEFT-ALONE (7), each written once | § Execution order step 3 |
| **W10** | The all-credentials-cooling honest attempt is **`TestDecision_Group_AllCredentialsCooling_PolicyFirstCredentialHonestAttempt`** (ruled D-C) and is in the 69 | § Amendment 4 §1 + the 69-name list |

### §9 — The marker

**Unchanged: `decision_all_members_failed`.** The **negative control is now measured at BOTH tiers**: **0 hits** in the v193 binary (`b11d7c8`) **and 0 hits** in the v194 binary (`fd1649c`) — both measured 2026-11-16 against the `~/llmproxy-verify/verify_image.sh` cache (per-SHA tags ⇒ immutable ⇒ the cache entries are safe; moving tags go stale). `GroupLane` → **0 hits at both**. The harness's own controls stay **> 0** at both tiers (`quota_exceeded` = 2, `ExcludeAndReselectForClass` = 2, `openrouter` = 5). Also measured at v194: `member_suppressed` = 0 and `member_skipped` = 0 (consistent with ADR-15's rename rationale). **No Tailwind / static-UI false-hit classes**; `grep -ci sticky` stays forbidden.

### §10 — Index of the in-place corrections this amendment made to this document

| § of this document | Correction | Marker placed |
|---|---|---|
| **Header** | the base assertions → `2b987bf` / `origin/master` = `fd1649c`; the branch line | *(AMENDED by Amendment 4 §5)* |
| Gate matrix **G4** | 59 → **69** (60 Pack A + 9 Pack B); 4 → **5** WITHDRAWN; FE 48 → **47** | *(AMENDED by Amendment 4 §1)* |
| Gate matrix **G11** | G11-a/G11-b → **ONE verdict rule** (empty porcelain is NOT a failure) | *(AMENDED by Amendment 4 §3)* |
| § **MUST PASS UNCHANGED** | split into **Tier-1** (whole-file byte-identity) and **Tier-2** (per-function); `handle_models_imggen_test.go` **moved to Tier-2** (CG-1); the skip-cache pins **re-derived** against v194's five new names | *(AMENDED by Amendment 4 §2, §5)* |
| § **The four RED-gap gates (ADR-17)** | **NEW subsection** — RED-1..RED-4 with their pinning tests + the D-2 negative controls | *(AMENDED by Amendment 4 §6)* |
| § **Text-lane regression pins** | the fence re-defined: **"no v194 semantic regression"**; the win-clear rename recorded | *(AMENDED by Amendment 4 §5)* |
| § **Ship runbook Step 1** | the hotspot list **extended** with the v194 files; **re-run the FULL gate on the merge regardless of conflict** | *(AMENDED by Amendment 4 §5)* |
| § **Ship runbook Step 3** | ~~**W4** — `workflow_dispatch` is first-class~~ **SUPERSEDED (B1, tester gate RED): a dispatch run is COMPILE-ONLY (every publish step is gated on `github.event_name == 'push'`) — the ladder is rung 1 = admin re-run of the ORIGINAL push-triggered run, rung 2 = NON-ZERO-DELTA content retrigger** | *(AMENDED by Amendment 4 §8; superseded by the B1 correction)* |
| § **Deploy runbook 5.1** | the marker negative control measured at **v193 AND v194** | *(AMENDED by Amendment 4 §9)* |
| § **Deploy runbook 7.2** | the rollback **ladder** (tier-1 v194 / tier-2 v193) + the **fresh-verify rule** + CI ≥ 195 | *(AMENDED by Amendment 4 §4)* |
| § **Deploy runbook Step 7** | **E4b** (AllowedModels 403 pre-check) + **E8b** (KEEP the group) + the all-marked local-harness-only note | *(AMENDED by Amendment 4 §8)* |
| § **Execution order steps 3 & 6** | the 69-name list + 5 withdrawn + the three disjoint FE heredocs; the stale counts fixed (W7) | *(AMENDED by Amendment 4 §1, §8)* |
| § **Execution order step 9** | the tsc recipe → `bash -c` + `PIPESTATUS[0]` (W4) | *(AMENDED by Amendment 4 §8)* |
| § **Cross-plan reconciliation** | new row **R19**; R1 marked superseded-and-re-derived (33 → 50 → 59 → 69) | *(AMENDED by Amendment 4)* |

### §11 — What Amendment 4 does NOT change

- **G0–G14 numbering** and every gate's blocking status — untouched.
- **The gate is still LOCAL, not CI** (`docker-ci.yml:104`'s `|| echo` still swallows Go-test failures); the local gate remains the authority.
- **The D-8 branch adjudication rule** (an extra added test name explained by phase1 §B1 ⇒ reported, not auto-failed) — stands.
- **AM-4's ruled class-conditional policy, the class-conditional exhaustion criterion, ADR-1's kind-immutability guard, ADR-14's LEGAL verdict, and the ADR-9 coordination rule** — all as folded by Amendments 1–3.
- **The § Amendment 1/2/3 sections' counts (33 / 50 / 59, FE 46 / 48)** remain as **dated historical records** of each tranche; the **operative** lists are the Amendment-4 ones.

---

---

## Amendment 5 (2026-11-16) — review cycle 2

**Authority.** `review-plan-2.md` §C2-2..C2-5 + `decisions.md` **ADR-18** (review cycle-2 rulings, `decisions.md:1111-1156`). Phase 1 carries the fold as **Amendments 7–9** (file now 2633 lines); phase 2 is unchanged by this tranche (889 lines).

**Effect.** **G0–G14 are NOT renumbered and no gate is weakened.** The G4 lists are re-derived to the **FINAL** set and the four cycle-2 blocking items are folded in place.

### §1 — G4's FINAL lists (re-derived, verified)

**Go — 74 ADDED top-level test names** (64 Pack A + 10 Pack B), enumerated verbatim in § Execution order step 3. **Re-derived independently from the phase-1 plan** (whole-document table sweep → rows not marked `EXISTING` → subtract the **5** `SUPERSEDED` names and the LEFT-ALONE prose references) ⇒ **74** (approver iteration-002 FIX 1 added `TestValidateGroupMembers_InactiveChainOnlyTarget_Accepted`, the R4 write-time ACCEPTANCE pin; a **pre-existing duplicate** of `TestValidateGroupMembers_DecisionGroup_RejectsChatMember` in the heredoc was also removed — set-counts had masked it), **set-identical** to the final list, **0 base collisions** against `2b987bf`, no dupes. Per file: `pkg/decision/handler_group_test.go` **34** · `pkg/store/database/store_group_test.go` **16** · `pkg/models/group_rules_test.go` **6** · `pkg/ui/kind_rules_parity_test.go` **3** · `pkg/credclass/classify_test.go` **2** · `pkg/modelscache/models_group_lane_test.go` **2** · `pkg/proxy/handle_models_imggen_test.go` **2** · `pkg/proxy/handler_anthropic_misroute_test.go` **2** · `pkg/decision/handler_test.go` **1** · `pkg/proxy/handler_test.go` **1** · `pkg/proxy/handle_chat_misroute_test.go` **1**.

**WITHDRAWN — 5:** `TestDecision_Group_MarkedMember_DemotedNotFiltered` · `TestDecision_Group_NoCooldownWriteOnBenchedPick` · `TestGroup_RewriteGroupEntries_DecisionGroup_PicksDecisionMember` · `TestDecision_Group_MemberWithTwoCredentials_AttemptsBothBeforeAdvancing` · **`TestDecision_Group_SkipCacheInstance_NotSharedWithTextLane`** — the last pinned a **wrong invariant** (AM-2 re-derived to a **SHARED** instance with a **layer-separation** fence, ADR-16); replaced by `TestDecision_Group_CacheLayerSeparation_SharedUpstreamInstanceOneAnswer`.

**FE — 48 new cases** (phase 2 Amendment 2 §5 + ADR-19): **5 NEW** files (45 cases: A1–A13, B1–B7+B9, C1–C7, D1–D6+D8–D10, E1–E8) · **5 UPDATED** files (3 new cases: **T1**, **F1**, **V1**) · **7 LEFT ALONE**. **B8/D7 removed as vacuous** (they asserted the absence of a field the group form never had — `GroupModelPayload` has no provider key); their intent is subsumed by **D5** upgraded to an exact key-set assertion; the real provider pin is **V1** = the FE half of ADR-15's **T5** set-equality pin (the rendered provider surface is exactly `{openai, typesafe, openrouter}` and is disjoint from the credential vocabulary).

### §2 — C2-2 (blocking): LAYER SEPARATION, not instance separation

**Sites fixed (both):** `phase3-plan.md` Amendment 1 §4(a) and the § Deploy runbook step 6 operator-verification note (`:1198`). Both now state the **LAYER-separation** invariant (`pkg/skipcache/skipcache.go:4-17` declares itself "a **process-wide** registry … read by credential selection + attempt suppression ONLY"; `cmd/main.go:242`/`:249` construct one instance each and `:255-256` hand **both** to the proxy config — one instance, both lanes; upstream health is per-credential, not per-lane), the Wave-A instance-separation test is **WITHDRAWN** as pinning a wrong invariant, and the Wave-A instance-separation **verification target is re-scoped**: an "instance separation" check has no meaningful pass/fail and must not be reported as a defect. **The operator verifies LAYER separation**, not instance separation (ADR-18 C2-2) — as written before, an operator would chase an impossible invariant and file a false defect.

### §3 — C2-3 (blocking, highest stakes): the abort row R11 is superseded

R11 now reads: **tier-1 = fresh-verified live-at-incident; tier-2 = prior release; the incident path NEVER strips a newer release than the incident requires** (ADR-18 C2-3). The v193-only two-release-rollback prescription is **SUPERSEDED** — under incident pressure it would strip v194's cache-layer-separation from the substrate the whole ensemble depends on.

### §4 — C2-4 (blocking): fresh-verify scope + dead dcs figure

The fresh-verify rule (§7.2) is **extended to every plan-time deploy/dcs number** (rev 36/37, gen 33/34, CI ≥ 195, `:fd1649c`, `:261622cf`/`b11d7c8`) — each is a **plan-time value, stale by construction**. The dcs "8 commits ahead of `origin/latest`" figure is **DEAD** (dcs synced @ `7f89eac`) — **R14 fires on MEASURED drift only** (measure at ship, compare, act on delta).

### §5 — C2-5 (blocking): Tier-1 table vs command (12 vs 10)

- **`pkg/proxy/win_clear_winner_identity_test.go`** (the :524 row): satisfiable-but-unasserted (net-churn `:577-598` is blind to assertion-body edits) ⇒ **added to the executable command** (one line) **and** the :524 cell wording fixed to name the clone's destination file `pkg/modelscache/models_group_lane_test.go`.
- **`pkg/models/group_rules_test.go`** (the :528 row): **unsatisfiable as commanded** (phase 1 edits it) ⇒ **the Tier-1 row is DELETED** (it was already correctly absent from the command). Sides fixed: command+table brought to agreement — the table shrank to the command's truth, the command grew to cover the pin.

### §6 — Doctrine seam (ADR-18 C2-1)

The doctrine-line pin targets the real seam file — **`pkg/decision/suppression.go`** (owns `decideAttempt`, the D-C branch, and the UPSTREAM + MODEL mark writers; its doc comment carries the verbatim extended doctrine line). `TestDecision_DoctrineLine_PinnedVerbatimAtSeam` targets it; `skipcache.go:29-31` is cited **read-only**, with an **optional drift-detection assertion**. **`pkg/skipcache/skipcache.go:29-31` is NEVER edited** (architect §0). The earlier reference to the dissolved `pkg/decision/layer_upstream.go` in Amendment 1 §4(a) is corrected to the real seam.

### §7 — Stage 0-A (ADR-18 STAGE 0)

New precondition **P0.0** — the **permanent base-currency gate**: at implementation kickoff, giter re-anchors the branch to fresh `origin/latest` (**it moved AGAIN during review** — `a2952a9` / master `8d434ba`; the CG-4 class recurs); the developer runs a **bounded pin-reverification sweep** (every BASE-relative pin re-checked, every drift flagged and **adjudicated before stage work starts**); the anchor-of-record SHA goes into the **KV row**; the ship-step full-gate re-run stays **unconditional regardless of merge conflicts**. **The corpus remains `2b987bf`-pinned for cycle 3 — no re-anchor mid-review.**

### §8 — The marker (negative control, ADR-13 + ADR-18)

**UNCHANGED: `decision_all_members_failed`.** The negative control is now a **pinned requirement measured at BOTH tiers**: **0 hits** in the v193 binary (`b11d7c8`) **and 0 hits** in the v194 binary (`fd1649c`) — both measured 2026-11-16 against the `~/llmproxy-verify/verify_image.sh` cache (per-SHA tags ⇒ immutable ⇒ the cache entries are safe; moving tags go stale). `GroupLane` → **0 hits at both**. The harness's own controls stay **> 0 at both tiers** (`quota_exceeded` = 2, `ExcludeAndReselectForClass` = 2, `openrouter` = 5). Also measured at v194: `member_suppressed` = 0 and `member_skipped` = 0 (consistent with ADR-15's rename rationale). **No Tailwind / static-UI false-hit classes; `grep -ci sticky` stays forbidden.**

### §9 — Index of the in-place corrections this amendment made to this document

| § of this document | Correction | Marker placed |
|---|---|---|
| § Amendment 1 §4(a) | instance-separation framing → **LAYER SEPARATION** (C2-2) | *(SUPERSEDED by Amendment 5 §2)* |
| § Deploy runbook step 6 operator note | instance-separation verification → **LAYER SEPARATION** (C2-2) | *(SUPERSEDED by Amendment 5 §2)* |
| § Risks & abort criteria R11 | v193-only rollback → **ladder: tier-1 = fresh-verified live-at-incident; tier-2 = prior release** (C2-3) | *(SUPERSEDED by Amendment 5 §3)* |
| § Deploy runbook §7.2 | fresh-verify scope extended to every plan-time deploy/dcs number; the dead dcs "8 commits" figure replaced by the measure-at-ship rule (C2-4) | *(AMENDED by Amendment 5 §4)* |
| § Text-lane regression pins (Tier-1) | `win_clear_winner_identity_test.go` **added to the command** + its cell names the clone's destination file (`pkg/modelscache/models_group_lane_test.go`); the `group_rules_test.go` Tier-1 row **deleted** (C2-5) | *(AMENDED by Amendment 5 §5)* |
| § Amendment 1 §4(a) | `layer_upstream.go` → **`pkg/decision/suppression.go`** (the real seam) (C2-1) | *(AMENDED by Amendment 5 §6)* |
| § 0. Preconditions | new **P0.0** (Stage 0-A, ADR-18) | *(AMENDED by Amendment 5 §7)* |
| § Cross-plan reconciliation | new row **R20** | *(AMENDED by Amendment 5)* |
| **G4's name lists** | **74 Go (64 Pack A + 10 Pack B) + 5 WITHDRAWN + 48 FE cases** (74 = 73 + the R4 acceptance pin, approver iteration-002 FIX 1) | *(updated by iteration-002)* | |

### §10 — What Amendment 5 does NOT change

- **G0–G14 numbering** and every gate's blocking status — untouched.
- **The gate is still LOCAL, not CI** (`docker-ci.yml:104`'s `|| echo` still swallows Go-test failures); the local gate remains the authority.
- **G4's lists** (74 Go / 5 withdrawn / 48 FE) — updated by iteration-002 FIX 1 (+1 R4 acceptance pin, −1 pre-existing heredoc duplicate).
- **AM-4's ruled class-conditional policy, the class-conditional exhaustion criterion, ADR-1's kind-immutability guard, ADR-14's LEGAL verdict, the ADR-9 coordination rule, and the four RED-gap gates** — all as folded by Amendments 1–4.
- **The § Amendment 1/2/3/4 sections' counts (33 / 50 / 59 / 69, FE 46 / 48)** remain as **dated historical records**; the **operative** lists are the current ones (**74 / 47** after iteration-002).
- **The corpus remains `2b987bf`-pinned for cycle 3 — no re-anchor mid-review** (ADR-18 STAGE 0).

---

## 0. Preconditions (the operator must clear all of these before the gate starts)

| # | Precondition | How to check | Blocking |
|---|--------------|--------------|----------|
| P0.0 | **Stage 0-A — the permanent base-currency gate (LEADER APPROVED, ADR-18 STAGE 0)** | At implementation **kickoff**, giter re-anchors the branch to fresh `origin/latest` (**it moved again during review** — `a2952a9` / master `8d434ba`; the CG-4 class recurs); the developer runs a **bounded pin-reverification sweep** (every BASE-relative pin re-checked, every drift flagged and **adjudicated BEFORE stage work starts**); the anchor-of-record SHA goes into the **KV row**; the ship-step full-gate re-run stays **unconditional regardless of merge conflicts**. **The corpus remains `2b987bf`-pinned for cycle 3 — no re-anchor mid-review** | YES |
| P0.1 | Phase 1 + phase 2 implemented and reviewed; working tree committed on `feature/decision-groups` | `git status --porcelain` empty; `git log --oneline origin/latest..feature/decision-groups` non-empty | YES |
| P0.2 | The exact list of **ADDED top-level `func Test*`** names is declared by phase 1 (Go) and phase 2 (FE file names) | phase-1/2 plan handoff → see `## Cross-plan reconciliation` R1 | YES |
| P0.3 | No new migration, no `k8s/` change, no `.github/` change | `git diff --stat $BASE..HEAD -- k8s/ .github/ migrations/ pkg/store/database/migrate.go` → **0 bytes** (house invariant, `PACKS.md:15`) | YES |
| P0.4 | Toolchain pinned and offline caches pinned | `go version` → `go1.26.0` from the cached toolchain; `GOPATH/GOMODCACHE/GOCACHE` set (see § Execution order step 1) | YES |
| P0.5 | The **REAL** home's config artifacts hashed **before** any test run, at an **ABSOLUTE path resolved BEFORE the quarantine export** (a `~` form would resolve to the QUARANTINE after the export). **The real entry uses the DB-backed ConfigManager (`017_config_json`), so `config.json` is NEVER created** — the evidence is `config.db` + `buffers/`, not `config.json` | `REAL_HOME_CONFIG_DIR="$(getent passwd "$(id -u)" | cut -d: -f6)/.config/llm-supervisor-proxy"` resolved **before** `export HOME=…`; hash `"$REAL_HOME_CONFIG_DIR/config.db"` and the `buffers/` tree (record "absent" if missing) | YES |
| P0.6 | FE toolchain available | `pkg/ui/frontend/node_modules` is **ABSENT** in this worktree (verified 2026-11-16) → `npm ci` is required and needs network | YES (else FE gates are skipped **and the gate is INCOMPLETE**) |
| P0.7 | **Phase 1 conforms to the injected-seam ruling** for the skip-cache TTL | `git diff --stat $BASE..HEAD -- pkg/config/` ⇒ **0 bytes**; per `phase1-plan.md §Planner adjudications → E-3` (RULED) and the Stage-4 seams row (`:277`), the TTL arrives as `skipCacheTTL func() time.Duration` wired from `cmd/main.go`, the local `configGetter` interface is **UNCHANGED**, and `pkg/config` gains **NO** interface member. The in-place correction is already applied in phase 1; this precondition only re-verifies it at gate time | YES |
| P0.9 | **`pkg/credentiallb` import legality from `pkg/decision` — CLOSED: LEGAL, unconditionally** (`decisions.md` **ADR-14**) | **Not an abort gate any more — a VERIFICATION RECIPE.** ADR-14's verdict is **LEGAL**: `pkg/decision` **already carries `pkg/credentiallb` in its transitive closure today** (`decision → auth → store/database → credentiallb`, a **non-test dep** — verified: `go list -deps ./pkg/decision` lists it), so the guard has been passing with that edge present all along and a direct import adds nothing the guard has not already accepted. **Recipe (run EARLY):** **(a)** run the three `pkg/decision` guard tests (`pkg/decision/handler_test.go:40-63` / `:65-88` / `:90-109`) **AND the second guard layer the research missed** — `Makefile:51-68` wires **three `grep -rnE` gates** into `make test` (`:53-64`), so **plain `go test` alone does NOT run them**: the operator must run `make test` (or the three greps explicitly). **(b)** every manual closure check **MUST use the pinned go1.26 toolchain** (`/home/nea/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.linux-amd64/bin/go`) — `/usr/bin/go` (1.22.2) **fails on go.mod** offline (it tries to download go1.26: "toolchain not available"). **(c)** scope note: the guard's transitive `go list` covers **non-test deps only**. **Evidence grade to record:** literal `=== RUN` / `--- PASS` from the guard tests, clean closures, a real direct-import scratch in `/tmp` passing all guards, and a **negative control** (a QUOTED banned path in a doc comment **FAILING** the guard) proving the raw-text mechanism both directions | YES |
| P0.8 | **Scope reduction settled** — member-level fallback (a member with 2+ credentials attempted once) vs member×credential | `phase1-plan.md §Planner adjudications → E-6` records the reduction as **pending user sign-off** (explicitly **not** adjudicated by the planner). **SUPERSEDED by Amendment 1 / ADR-6**: ADR-6 makes member×credential the **ORDER OF OPERATIONS** (R3), so the reduction is **no longer a reduction** — it is retracted. The gate list is the Amendment-1 list | YES |
| P0.10 | **AM-4 — RULED (ADR-13): the decision lane mirrors the text-lane failure-class policy EXACTLY** | **RULED, not open** (`decisions.md:815-829` ADR-13; phase1 Amendment 2 §B2). The decision lane uses the SAME classes as `pkg/proxy/cred_failover_class.go:162` (`rotationEligible`) / `:190` (`memberAdvanceBlocked`), with marks firing on limit-class only (`pkg/proxy/race_coordinator.go:2106-2107`): **rotation-eligible** (limit-class + upstream 401/403) ⇒ **rotate within the member** (upstream-layer write per classification) · **5xx** ⇒ **advance to the next member immediately — no within-member rotation, no mark, no cooldown write** · **memberAdvanceBlocked** (400/413/415/422 + client-cancel) ⇒ **fail-fast advance-blocking**. **The earlier "rotate on any failed attempt" framing is WITHDRAWN** (it was phase-1 Amendment 1 §A5's superseded text) | YES |

---

## Gate matrix

**The real gate is LOCAL, not CI.** `.github/workflows/docker-ci.yml:104` reads `go test ./... -v 2>&1 || echo "No tests found or tests completed"` — the `|| echo` **swallows every Go-test failure**, and the workflow gates neither Go tests nor the frontend. CI green therefore proves **compilation and image build only**. The local gate below is the authoritative gate.

**Pack definitions** (house release-gate slices, `PACKS.md:14-16`):

- **Pack A** = `./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...` → **9 packages** (re-counted at the MERGED base; membership is re-verified by `go list` at ship time — a package that moves between packs changes the count): `pkg/proxy`, `pkg/proxy/normalizers`, `pkg/proxy/token`, `pkg/proxy/translator`, `pkg/store`, `pkg/store/database`, `pkg/decision`, `pkg/ui`, `cmd`.
- **Pack B** = the anchored complement → **31 packages** today (verified by `go list` 2026-11-16): root stub, `pkg/auth`, `pkg/bufferstore`, `pkg/config`, `pkg/credentiallb`, `pkg/crypto`, `pkg/events`, `pkg/grouplb`, `pkg/imggen`, `pkg/logger`, `pkg/loopdetection(+fingerprint)`, `pkg/mcp`, `pkg/memlimit`, `pkg/middleware/gzipmw`, `pkg/models`, `pkg/modelscache`, `pkg/providers`, `pkg/proxyheader`, `pkg/skipcache`, `pkg/stickyorder`, `pkg/supervisor`, `pkg/toolcall`, `pkg/toolrepair`, `pkg/usage`, `scripts`, `test`, `test/e2e_*`, `test/reasoning_content`.

> **The `(/|$)` END-ANCHOR SUFFIX is load-bearing** (tester finding) — the module prefix alone is not: an **end-anchor-only** variant (`…llm-supervisor-proxy/(cmd|pkg/(proxy|store|decision|ui))` with no `(/|$)`) **leaks to 38 packages** and would silently over-run the slice. `grep -vE` MUST use the module prefix `github\.com/disillusioners/llm-supervisor-proxy/(cmd|pkg/(proxy|store|decision|ui))(/|$)` — prefix **and** the `(/|$)` end-anchor. A repo-relative re-run (`grep -vE '(cmd|pkg/(proxy|store|decision|ui))(/|$)'`) silently re-runs **everything** as "Pack B" and destroys the slice separation the flake adjudication depends on.

### `pkg/credclass` placement verdict — **PACK B**

**Verdict: `pkg/credclass` belongs to Pack B.** Reason: the Pack B anchor excludes only `cmd` and `pkg/{proxy,store,decision,ui}`. `github.com/disillusioners/llm-supervisor-proxy/pkg/credclass` matches **none** of those prefixes (after `pkg/` the next segment is `credclass`, not `proxy|store|decision|ui`), so it is **not** excluded and therefore **is** in Pack B. Pack B grows to **34 packages** (re-counted at the MERGED base)** once phase 1 creates it.

Why Pack B is also *correct* (not merely incidental):

1. Contract #21 (`decisions.md`) makes `pkg/credclass` a **leaf package with no internal imports**, whose moved classifier tests are the pin for the delegation. Those tests are the only proof that `pkg/providers` still delegates and that the vocabulary is unchanged — and they are **Go tests of a new package**, so they can only run in whichever slice contains it.
2. The packages that *consume* `pkg/credclass` — `pkg/providers` (delegation) and `pkg/decision` (import of the leaf) — sit in **opposite slices**: `pkg/providers` is Pack B, `pkg/decision` is Pack A. Putting `pkg/credclass` in Pack A would split the leaf's own tests away from its only delegating consumer; putting it in Pack B keeps `{pkg/credclass, pkg/providers}` together and leaves `pkg/decision` to exercise it through the Pack A decision suite.
3. Moving it into Pack A would require **changing the Pack A anchor**, which changes the release-gate slice composition for every future gate — a house-level change this commission must not make unilaterally.

**Action for phase 1:** do **not** create the package under a Pack A prefix (e.g. `pkg/store/credclass`, `pkg/decision/credclass`) — that would silently move the slice. The path is `pkg/credclass/` at the module root.

**Pack membership after Amendment 1 — UNCHANGED (no package moves).** The amendment adds **no new Go package** beyond `pkg/credclass`: the three per-layer cache files (`pkg/decision/layer_group.go`, `pkg/decision/layer_model.go`, `pkg/decision/layer_upstream.go`, phase1 §A5) are new files **inside** `pkg/decision` (Pack A) — **the doctrine-line pin target is now `pkg/decision/suppression.go`** (phase1 §A5 / ADR-18 C2-1), which owns `decideAttempt`, the D-C branch, and the UPSTREAM + MODEL mark writers; its doc comment carries the verbatim extended doctrine line. `TestDecision_DoctrineLine_PinnedVerbatimAtSeam` targets `suppression.go` and **MAY additionally assert skipcache's copy read-only as drift detection** (`skipcache.go:29-31` is cited, never edited — the architect's §0 rule); the **exported `pkg/grouplb` policy selector** (AM-1's preferred path — `pkg/grouplb/selector.go:26`/`:52` are currently unexported, so phase 1 adds an additive exported API) is an **API addition inside** `pkg/grouplb` (Pack B), text-lane-neutral by construction (a new API, no behaviour change); AM-1's fallback (a small local picker in `pkg/decision`) is also inside an existing package. ⇒ **Pack A stays 10 packages; Pack B stays 32** (31 + `pkg/credclass`). Neither choice changes the slice composition, so the G2/G3 commands are unaffected.

### The matrix *(the G0–G14 ids are UNCHANGED by Amendment 1 — no renumbering)*

| # | Gate | Command (runbook) | Expected output | Blocking? | Pack |
|---|------|-------------------|-----------------|-----------|------|
| G0 | Hermetic env + HOME quarantine | see § Execution order step 1 | `HOME` = fresh temp dir; `DATABASE_URL` unset; `GOTOOLCHAIN=local`; `go version` = go1.26.0 | YES | — |
| G1 | **gofmt zero-NEW-drift** | see § Execution order step 4 | `fmt_new.txt` **EMPTY**; per-file disambiguation for any file in both sets; `gofmt -l` on **all** touched `_test.go` adds no new flagged file | YES | — |
| G2 | **Pack A** | `timeout 300 $GO test -v -count=1 -timeout=240s ./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...` | exit **0**; `ok` for all 9 packages | YES | A |
| G3 | **Pack B** | `timeout 300 $GO test -v -count=1 -timeout=240s $(go list ./... \| grep -vE '<module-anchor>')` | exit **0** except the pre-registered quarantined rows (§ Pre-registered flakes); `ok` for all 34 packages | YES | B |
| G4 *(AMENDED by Amendments 1–5)* | **Anti-vacuous `--- PASS`** | see § Execution order steps 3 + 6 | the **74 named** ADDED Go top-level tests (64 Pack A + 10 Pack B, listed in § Execution order step 3) each show an **end-anchored** `^--- PASS: <name> ` in the A/B logs; the **5 WITHDRAWN names must be ABSENT** from the diff; the **5 NEW** FE test files (45 cases) and the **5 UPDATED** FE files (3 new cases: **T1**, **F1**, **V1**) each show a pass in the vitest report; **zero** misses in either direction of the diff cross-check — **except** a single extra name explained by the documented **D-8 branch** (phase1 §B1), which is **reported for adjudication, not auto-failed** | YES | A+B |

**G4 name-count ledger (re-derived; updated by approver iteration-002 FIX 1).** Per-file breakdown of the **74**: `pkg/decision/handler_group_test.go` **34** (NEW file) · `pkg/store/database/store_group_test.go` **16** · `pkg/store/database/models_manager_kind_test.go` **1** (the 74th name — `TestDecision_Vocabulary_CredentialModelSplit_AnthropicSaves_ModelRejected`, at `pkg/store/database/models_manager_kind_test.go:726`) · `pkg/models/group_rules_test.go` **6** (incl. `TestValidateGroupMembers_InactiveChainOnlyTarget_Accepted`) · `pkg/ui/kind_rules_parity_test.go` **4** (incl. `TestDecision_Vocabulary_OutOfSetProvider_Rejected_AtAllGates`) · `pkg/decision/vocabulary_pin_test.go` **2** (NEW file: `TestDecision_Vocabulary_SourceLiteralPin_FiveSurfaces`, `TestDecision_Vocabulary_RegistryKeySetEqualsGateAllowList`) · `pkg/credclass/classify_test.go` **2** (NEW package) · `pkg/modelscache/models_group_lane_test.go` **2** (NEW file) · `pkg/proxy/handle_models_imggen_test.go` **2** · `pkg/proxy/handler_anthropic_misroute_test.go` **2** · `pkg/decision/handler_test.go` **1** · `pkg/proxy/handler_test.go` **1** · `pkg/proxy/handle_chat_misroute_test.go` **1** ⇒ **74**. Pack A = **64**, Pack B = **10**. **PROVENANCE (tester finding):** 6 of the planned names landed **UPSTREAM** (`13a8e4e`, present since `a2952a9`) — **ZERO commission attribution**; they are NOT "added" relative to the merged base, and the base-collision check must therefore run against the **CURRENT (merged) base**, not `2b987bf`.

**How the 69 was derived (independently re-derived, not patched).** Sweep every table row of the amended `phase1-plan.md` (2504 lines), take the backticked `Test*` names on rows not marked `EXISTING`, then subtract the **5** `SUPERSEDED`/withdrawn names (`§A16` ×3, Amendment 2 `§B2` ×1, Amendment 5 `§E11` ×1) and the LEFT-ALONE existing-test references ⇒ **69**, **set-identical** to the final list above. **Collision check against the base tree (`2b987bf`): 0 collisions** — no listed name exists at base, so no `--- PASS` line can be satisfied by a pre-existing test.

> **The 5 WITHDRAWN names** (present-in-diff ⇒ G4 FAIL, R21): `TestDecision_Group_MarkedMember_DemotedNotFiltered` · `TestDecision_Group_NoCooldownWriteOnBenchedPick` · `TestGroup_RewriteGroupEntries_DecisionGroup_PicksDecisionMember` · `TestDecision_Group_MemberWithTwoCredentials_AttemptsBothBeforeAdvancing` · **`TestDecision_Group_SkipCacheInstance_NotSharedWithTextLane`** — the last one pinned a **wrong invariant**: AM-2 was re-derived to a **SHARED** upstream instance with a **layer-separation** fence (ADR-16), so it is replaced by `TestDecision_Group_CacheLayerSeparation_SharedUpstreamInstanceOneAnswer`.

> **Delta over Amendment 2 (59 → 69): +10 added, +1 withdrawn.** Wave A (ADR-15): `TestDecision_Group_AllCredentialsCooling_PolicyFirstCredentialHonestAttempt` (D-C, the OQ-1 symmetry at member level), `TestDecision_Group_CacheLayerSeparation_SharedUpstreamInstanceOneAnswer` (replacing the withdrawn instance-separation pin), `TestDecision_Group_ChatInferredWithDecisionMember_NeverSilentlyServes` (W-fail-open). Wave B (ADR-16): `TestGroup_SweepKindParameterization_MutationControl` (the D-2 mutation control). Architect fold (ADR-17, the four RED gaps): `TestGroup_RewriteGroupEntries_DecisionGroupInChatChain_BoundaryRefusal` + `TestUpdateModel_FallbackChain_DecisionGroupTarget_Rejected` (RED-1) · `TestPackage_ImportGuard_GroupPolicyFiles_NoCacheImports` (RED-2) · `TestHandleChatCompletions_RejectsDecisionGroupMisroute_ThroughCachedConfig` + `TestAnthropicBoundary_DecisionGroupMisroute_ThroughCachedConfig` + `TestHandleModels_FilterDecisionGroup_ThroughCachedConfig` (RED-3) — and `TestHandleModels_FilterDecisionGroup` (RED-4, contract 15).

> **BASE RE-ANCHORED (ADR-16): every base assertion in this document moves `24c51ef` → `2b987bf`** (v194 `fix/cache-layer-separation`, 36 files +3507/−630, 15 commits; `origin/latest` = `2b987bf`, `origin/master` = `fd1649c`). The architectural fence changes from *"text-lane byte-untouched"* to **"no v194 semantic regression"** — v194 **deliberately changed** the text lane, so the invariant is that this release must not move the text lane **relative to `2b987bf`**.

> **ADJUDICATION NOTE (the earlier 14-vs-16 discrepancy, now superseded).** The previous ledger reported 14 `handler_group_test.go` names and flagged the dispatch's "16". That gap is **closed by Amendment 1**: the file now carries **27** (14 original + 13 added by `§A16`/`§A15`). The earlier 14-vs-16 note is **SUPERSEDED by Amendment 1** — see § Amendment 1 §2. |
| G5 | **Build (real entry)** | `go build -o $LOGDIR/llm-supervisor-proxy ./cmd` | exit 0; binary ≈ 26 MB (NOT the root stub) | YES | — |
| G6 | **Build (full tree)** | `go build ./...` | exit 0 | YES | — |
| G7 | **go vet** | `go vet ./...` | exit 0 | YES | — |
| G8 | **Import guards** | 2× Makefile greps (see § Execution order step 7) + Pack A runs `TestPackage_ImportGuard_*` | exit 0; the 3 guard tests show `--- PASS` | YES | A |
| G9 | **FE vitest** | `cd pkg/ui/frontend && npm ci && timeout 300 npx vitest run` | **all** test files pass; 0 failed; 0 skipped (record the exact file/test counts) | YES | — |
| G10 | **FE tsc baseline parity** | `cd pkg/ui/frontend && timeout 240 npx tsc --noEmit` | **exit code 2** (not 1); `pkg/ui/frontend/tsc-baseline.txt` stays **UNCHANGED** — `TOTAL_ERRORS=19` / `TOTAL_FILES=9`, the same nine-file set, and each touched baseline file keeps its exact per-file count (`SettingsPage.tsx` = 6, `ModelsTab.tsx` = 6, the seven 1-error files = 1). A **new** file with an error is a **FAILURE, not a baseline bump** — no line may be added to `tsc-baseline.txt` to paper over a new error | YES | — |
| G11 | **FE vite build + static proof** | see § Execution order step 9 | **ONE verdict rule (CG-3, collapsing the former G11-a/G11-b pair):** with FE source changing in this commission, the **regenerated assets must ride the same commit** as the FE source change (the step-9 `sha256sum` before/after proof stands); **an EMPTY `git status --porcelain pkg/ui/static` is NOT a failure** — it is the expected result of a deterministic rebuild of already-committed assets. The **only** failure is an asset change that is **not** in the same commit as its source change | YES | — |
| G12 | **Isolated-boot smoke** | see § Execution order step 10 | `/healthz` 200, `/fe/api/version` 200, `/v1/models` 200; zero panics; clean SIGTERM; `config.json` hash unchanged | YES | — |
| G13 | **Boundaries** | `git diff --stat $BASE..HEAD -- k8s/ .github/ migrations/ pkg/store/database/migrate.go pkg/config/` | **0 bytes** — and **0 bytes for `pkg/config/` in particular**: per `phase1-plan.md §Planner adjudications → E-3` (RULED) and the Stage-4 seams row (`:277`, "**No `pkg/config` change**"), the skip-cache TTL arrives as an **injected seam** (`skipCacheTTL func() time.Duration`, wired from `cmd/main.go`), and `pkg/config` gains **NO** new interface member — the config getter is the **fallback only if the seam proves awkward at wiring time**. A `pkg/config` diff is therefore a **fence violation**, not a permitted additive getter (see § Risks & abort criteria R17) | YES | — |
| G14 | **Umbrella corroboration (optional)** | `make test` | exit 0 (redundant with A+B + G8; run only if budget allows) | NO | all |

**G2/G3 timing discipline.** Release-gate slices use internal `-timeout=240s` inside an unchanged outer `timeout 300`; the unit-pack scripts keep 110s/120s (`PACKS.md:13-14`). `pkg/proxy` has outgrown the 110s unit cap (109.5s → 110.7s observed) — the 240s/300s release-gate pair is mandatory, not optional.

**G2/G3 must run with `-v`.** Without `-v`, `go test` prints only package-level `ok` and the anti-vacuous check (G4) has nothing to grep.

### G10 — what "tsc parity" actually means

`pkg/ui/frontend/tsc-baseline.txt` is a **count** baseline, not a line-number baseline: per-file error counts, then a machine footer `TOTAL_ERRORS=19` / `TOTAL_FILES=9`. The comparator `src/utils/__tests__/tsc-baseline.test.ts` asserts (a) the baseline's own internal consistency, (b) actual total == baseline total, (c) actual file count == baseline file count, (d) per-file counts match. Verdicts are therefore **count + file-set**, never line numbers. `tsc --noEmit` **exits 2**, not 1, when errors exist — a `set -e` wrapper that treats non-zero as failure must special-case exit **2** as the expected baseline-parity outcome.

Note the two files already at 6 errors each (`src/components/SettingsPage.tsx`, `src/components/config/ModelsTab.tsx`) — phase 2 touches both ⇒ **one new error in either is a gate failure**. Any **new** file must produce **zero** errors or the file set changes (9 → 10) and the gate fails.

### The four RED-gap gates (ADR-17)

The architect's final fold raised **four RED gaps**; each has a **named gate test** in the 69-name list. The reviewer will look for each — they are enumerated here with their pinning test and the semantics each one locks.

| RED gap | What was wrong | Gate test(s) | What the gate locks |
|---|---|---|---|
| **RED-1 — fallback-chain bypass** | a chat main's `FallbackChain` may name a group id; the rewrite loop iterates ALL entries, the write side validated existence only, and the misroute guards check only the resolved model ⇒ a **decision-lane group could be served on the chat lane**. Fix = the **rewrite-boundary lane check**: a decision-lane group entry reached from the chat lane ⇒ `FailedGroupID`. | `TestGroup_RewriteGroupEntries_DecisionGroupInChatChain_BoundaryRefusal` + `TestUpdateModel_FallbackChain_DecisionGroupTarget_Rejected` | the **planner-endorsed refusal semantics** (ADR-17 adjudication, for cycle-2 review): a **direct-named** decision-lane group ⇒ **`FailedGroupID`** (fail closed); a **fallback-position** entry ⇒ **drop-and-continue** (loud log + a `decision_misroute` event, **NO rotation row written**) — and **the healthy primary does NOT 502** (a fatal 502 on a healthy primary would reintroduce exactly the defect being fixed). `TestUpdateModel_FallbackChain_DecisionGroupTarget_Rejected` closes the write-time side (the existence-only validation). |
| **RED-2 — AST-pin pick placement is load-bearing** | a pick placed in `pkg/store/database` **inherits** the AST pin (`pkg/proxy/cache_layer_separation_test.go:752-786` @`2b987bf`) and therefore **cannot compile a cross-layer read**; placed only in `pkg/decision`, the isolation would revert to a mirror file-level pin. | `TestPackage_ImportGuard_GroupPolicyFiles_NoCacheImports` | the **pick stays in `pkg/store/database`** (inheriting the AST pin) and the **decision pick/walk files carry no `pkg/skipcache` / `pkg/modelcache` import**. **`store_group_policy.go` is RETRACTED** — it must appear NOWHERE in the final corpus except as a retracted proposal (ADR-17 §2c superseded by §8.1). |
| **RED-3 — fail-open lane gate on 3 of 4 consumers** | the lane gate was wired on only **3 of 4** consumers, so one consumer could silently fail open. Fix = **compile-time `var _` assertions + effect tests**. | `TestHandleChatCompletions_RejectsDecisionGroupMisroute_ThroughCachedConfig` + `TestAnthropicBoundary_DecisionGroupMisroute_ThroughCachedConfig` + `TestHandleModels_FilterDecisionGroup_ThroughCachedConfig` | the gate is wired on **ALL FOUR** consumers **through the CACHED decorator** (the production binding). **The compile-time `var _` assertions are part of the gate: a REMOVED decorator forward breaks the BUILD, not just a test** — so a silent no-op is impossible at compile time, and the three effect tests prove the *effect* reaches the consumer. |
| **RED-4 — contract-15 `/v1/models`** | the decision-group exclusion must be **complete** over ALL models (architect's §2a W9 gap list). | `TestHandleModels_FilterDecisionGroup_ThroughCachedConfig` | the **fold-over-ALL-models nuance** (architect G7): **disabled members count for lane inference** — a group with a **DISABLED decision member** is still **excluded** from `/v1/models`. The exclusion must not be defeated by a disabled member. |

**D-2's named negative controls** (the fail-open defect class this release must not re-introduce — phase1 §E10): `TestGroup_RewriteGroupEntries_MixedKindGroup_FailsClosed` (a mixed group must fail closed, not serve *some* members) · `TestGroup_RewriteGroupEntries_DecisionGroup_RefusedOnTextSweep` (the text sweep must never offer a decision member) · `TestDecision_Group_ChatInferredWithDecisionMember_NeverSilentlyServes` (a chat-inferred group must never silently serve a decision member) · `TestGroup_SweepKindParameterization_MutationControl` (the predicate consults the expected kind — servable when decision, **not** servable when chat).

### G11 — ONE verdict rule (CG-3, collapsed)

`vite.config.ts:7-10` sets `build.outDir: '../static'` with `emptyOutDir: true`; `pkg/ui/static/` is **TRACKED** in git (4 files) and is **not** gitignored (only `/pkg/ui/frontend/node_modules` and `/pkg/ui/frontend/dist/` are — `.gitignore:19-20`). The embedded UI is served from `pkg/ui/static/*` via `//go:embed static/*` (`pkg/ui/server.go:90`), pinned by `pkg/ui/embed_regression_test.go` (runs in Pack A).

**FE source changes in this commission** (phase 2): new `DecisionModelsContainer.tsx`, new `groupLane.ts`, and changed `SettingsPage.tsx`, `GroupModelsTab.tsx`, `GroupModelForm.tsx`, `GroupMemberEditor.tsx`, `types.ts`. Vite asset filenames are **content-hashed**, so a rebuild from changed sources yields a different file set.

**THE RULE (one verdict, no sub-branches):**

> The regenerated `pkg/ui/static` assets must ride **the same commit** as the FE source change that caused them. **An empty `git status --porcelain pkg/ui/static` is NOT a failure** — it is what a deterministic rebuild of already-committed assets produces. The only failure is an asset change **split across commits** from its source change.

**Why the former G11-a is retired.** The old G11-a asserted that the porcelain output must be **NON-EMPTY** ("an empty diff would mean stale embedded UI"). That is **wrong** against a deterministic rebuild: `vite build` is content-hash-deterministic, so rebuilding from unchanged committed sources reproduces the committed assets **exactly** and the porcelain output is empty — which is *proof the assets are in sync*, not proof they are stale. G11-a also conflicted with the zero-mod premise it was paired with. **The two sub-branches are collapsed into the single rule above (CG-3); the step-9 `sha256sum` before/after proof is retained as the mechanism claim.**

```bash
# (a) the sha256 before/after proof — the MECHANISM claim: a rebuild from the
#     committed sources reproduces the committed assets byte-for-byte.
find pkg/ui/static -type f | sort | xargs sha256sum > "$LOGDIR/static_before.sha"
( cd pkg/ui/frontend && timeout 300 npm run build ) 2>&1 | tee "$LOGDIR/fe_vite.log"
find pkg/ui/static -type f | sort | xargs sha256sum > "$LOGDIR/static_after.sha"
diff "$LOGDIR/static_before.sha" "$LOGDIR/static_after.sha" \
  && echo "G11 OK: rebuild is byte-identical to the committed assets" \
  || echo "G11: assets differ — they MUST ride the same commit as their source change"

# (b) the pairing rule: for the commit that changed FE source, the assets must
#     be present in THAT commit.
git --no-pager show --stat --oneline HEAD -- pkg/ui/static pkg/ui/frontend/src \
  | tee "$LOGDIR/static_commit.txt"
#     ⇒ one commit must list BOTH a pkg/ui/frontend/src change AND a
#        pkg/ui/static change. Split across two commits => FAILURE.
```

## Text-lane regression pins

The commission's central risk is that widening the group kind-rules (D-1/A, D-2) **leaks across lanes** and breaks the text-lane correctness properties.

> **THE FENCE IS RE-DEFINED (ADR-16): "no v194 semantic regression", NOT "text-lane byte-untouched".** v194 (`fix/cache-layer-separation`) **deliberately changed the text lane** — the sweep is now policy-headed and cache-blind, and the proxy does per-layer suppression. The correct invariant for this release is therefore: **this release must not move the text lane relative to `2b987bf`** — not relative to v193. The old byte-identity framing survives only where it is still literally true (the Tier-1 whole-file pins below). Two consequences: **(a)** the text-group regression pins are **re-derived against v194's text behaviour** (v194 rewrote `store_group_skipcache_test.go` — the old pin names are GONE, the new ones assert the **inverted** meaning "the mark does not change the pick"); **(b)** the win-clear helper was **renamed by v194**: `clearWinnerUpstreamSkip` → **`clearWinnerLayerState`** (`pkg/proxy/handler_helpers.go:873`, doc `:840-872`), and it now clears **all three layers** (GROUP / MODEL / UPSTREAM).

### MUST PASS UNCHANGED — TIER-1 (whole-file byte-identity) and TIER-2 (per-function)

> **CG-1 (blocking) — the tier split.** A file belongs to **Tier-1** only if this release does **not** edit it at all; then the gate asserts a **whole-file byte-identity** diff. A file this release **edits** (new functions added, existing functions untouched) belongs to **Tier-2**; then the gate asserts **per-function** behaviour: the pre-existing functions must be unchanged, the new functions must be present and passing. **`pkg/proxy/handle_models_imggen_test.go` MOVES from Tier-1 to Tier-2** — phase 1 **edits** it (`TestHandleModels_FilterDecisionGroup` + `TestHandleModels_FilterDecisionGroup_ThroughCachedConfig`), so a whole-file byte-identity assertion would be a false failure.

**TIER-1 — whole-file byte-identity to the base (`2b987bf`).** `git diff --exit-code $BASE..HEAD -- <file>` must be EMPTY for every file below.

| File | What it pins | Pack |
|------|--------------|------|
| `pkg/proxy/handler_groups_test.go` | text-lane group entry, Am24/Am26 arms, **`TestHandlerGroups_Am26_NonGroup_ByteIdentity`** (byte-identity pin for non-group requests) | A |
| `pkg/proxy/handler_groups_equivalence_test.go` | group ↔ non-group equivalence | A |
| `pkg/proxy/group_fallback_hidden_member_test.go` | hidden-member fallback (ordering, never filtering) | A |
| `pkg/proxy/group_fallback_part2_test.go` | exhaustive fallback part 2 | A |
| `pkg/proxy/handler_groups_shadow_member_test.go` | shadow-member serving (G8 shadow target) | A |
| `pkg/proxy/handler_groups_metering_test.go` | group metering stamp (`RouteModel`) | A |
| `pkg/proxy/handle_models_group_test.go` | `/v1/models` group visibility | A |
| `pkg/proxy/win_clear_winner_identity_test.go` | win-clear reaches the engine **through the cached decorator** — the existing tests are unchanged; the GroupLane decorator-through clone is an **ADDITION**, landing in **`pkg/modelscache/models_group_lane_test.go`** (the clone's destination file — phase1's decorator-through test lands there, not here) | A |
| `pkg/store/database/store_group_skipcache_test.go` | **RE-DERIVED at `2b987bf`** — v194 **REWROTE** this file, so the old pin names are **GONE**; the pins of record are the five v194 names below | A |
| `pkg/modelscache/models_group_cache_test.go` | claim-blind `GetModelNative` + group cache semantics | B |
| `pkg/grouplb/engine_test.go` | the kind-blind engine, incl. **`TestEngine_ZeroCredentialImportGuard`** (`:401`) — the engine must stay kind-blind and stdlib-only | B |
~~`pkg/models/group_rules_test.go`~~ | ~~the G1/G2/G4/G5/G6/G8 rule arms that are **not** G3~~ | ~~B~~ |
*(DELETED — C2-5: phase 1 **edits** this file (5 new functions), so a whole-file byte-identity row is unsatisfiable as written. It was already correctly absent from the command below.)*

> **The v194 skip-cache pins of record** (`pkg/store/database/store_group_skipcache_test.go`, verified in-tree at `2b987bf`) — the old `…HealthAwareSelection` (`:62`) and `…NilCacheByteIdentical` (`:165`) **no longer exist** (v194 rewrote the file, and the **meaning is inverted**: the old tests asserted "the marked member is never picked"; the new ones assert "**the mark does not change the pick**"):
> `TestGroup_SkipCache_MarksDoNotChangeThePick` `:59` · `TestGroup_SkipCache_ProviderWallDoesNotChangeThePick` `:85` · `TestGroup_SkipCache_AllMarked_StillServes` `:106` · `TestGroup_SkipCache_MarkNeverBenchesOrderingSeam` `:128` · `TestGroup_SkipCache_ProviderMarkDoesNotWallSibling` `:147`.
> **Gate assertion:** all five must still pass, and the file must be byte-identical to the base — **if a Tier-1 file's pin was renamed or removed by v194, the pin list is re-derived again, never copied from an older base.**

**TIER-2 — per-function check (the file IS edited; the existing functions are not).**

| File | What changes | What must stay |
|------|--------------|-----------------|
| `pkg/proxy/handle_models_imggen_test.go` *(moved from Tier-1 — CG-1)* | **NEW** `TestHandleModels_FilterDecisionGroup` + **NEW** `TestHandleModels_FilterDecisionGroup_ThroughCachedConfig` | `TestHandleModels_FilterImageGen` (`:86`), `TestHandleModels_FilterDecision` (`:134`), `TestHandleModels_OnlyImageGen_EmptyData` (`:170`), `TestHandleModels_MethodNotAllowed` (`:196`) — **unchanged** |
| `pkg/proxy/handle_chat_misroute_test.go` | **NEW** `TestHandleChatCompletions_RejectsDecisionGroupMisroute` (+ the `_ThroughCachedConfig` twin in `pkg/proxy/handler_test.go`) | the 3 existing functions unchanged |
| `pkg/proxy/handler_anthropic_misroute_test.go` | **NEW** `TestAnthropicBoundary_DecisionGroupMisroute` + `_ThroughCachedConfig` | the 2 existing functions unchanged |
| `pkg/ui/kind_rules_parity_test.go` | **NEW** `TestKindRules_Parity_Group_DecisionLane` + `_DecisionMembers_MixedProviders` + `_KindImmutability` | the existing tables + the provider pin (`:312-317`) unchanged |
| `pkg/store/database/store_group_test.go` | **16 NEW** functions | every existing function unchanged (the table at `:146` has no member-kind row ⇒ no expectation moves) |
| `pkg/models/group_rules_test.go` | **5 NEW** functions | the existing table (`:105`) unchanged |
| `pkg/modelscache/models_group_lane_test.go` *(NEW file)* | the decorator-through pair | — |
| `pkg/credclass/classify_test.go` *(NEW package)* | the moved vocabulary tables | `pkg/providers/interface_quota_test.go` + `ratelimit_classifier_test.go` unchanged |

**Tier-2 per-function recipe** (the mechanical form of the table above):

```bash
# for each TIER-2 file: (a) every pre-existing function must still exist,
# (b) the file's pre-existing functions must not have changed their assertions,
# and (c) B5 (approver escalation): NO removed/changed ASSERTION-BEARING line —
#     `grep '^-func Test'` only sees header changes, so a body-only assertion
#     edit in a Tier-2 file would otherwise ship green.
git diff $BASE..HEAD -- <tier2-file> \
  | grep -E '^\-func (Test[A-Za-z0-9_]+)' | sed -E 's/^-func (Test[A-Za-z0-9_]+).*/\1/' | sort -u \
  | tee "$LOGDIR/tier2_removed_funcs.txt"
#   ⇒ MUST BE EMPTY (no pre-existing function removed or renamed)
#   ⇒ and each file's new functions must show an end-anchored --- PASS (G4).
# (c) assertion-line guard — additions are ALLOWED (new siblings/new cases);
#     any REMOVED or MODIFIED assertion-bearing line FAILS:
git diff -U0 $BASE..HEAD -- <tier2-file> \
  | grep -E '^-' | grep -Ev '^---' \
  | grep -Ei '(^|[^A-Za-z0-9])(assert|require|expect)\s*\.?\(|t\.(Fatal|Fatalf|Error|Errorf|Skip)|\.(toBe|toEqual|toStrictEqual|toThrow|toHaveBeenCalledWith|toMatchSnapshot|toBeFalsy|toBeTruthy|toContain|toMatch|toBeDefined|toBeNull|toBeUndefined)\(' \
  | tee "$LOGDIR/tier2_removed_assertions.txt"
#   ⇒ MUST BE EMPTY. Pattern list (Go + TS, the Tier-2 corpus spans both):
#     Go:      assert.*|require.*(testify), t.Fatal/Fatalf/Error/Errorf/Skip
#     TS:      expect( .. ), .toBe/.toEqual/.toStrictEqual/.toThrow/
#              .toHaveBeenCalledWith/.toMatchSnapshot/.toBeFalsy/.toBeTruthy/
#              .toContain/.toMatch/.toBeDefined/.toBeNull/.toBeUndefined
#   A non-empty file here is a modified pre-existing expectation ⇒ STOP (R17)
#   unless named + justified in the RESULTS report.
```



**Mechanical check for "unchanged":**

```bash
git diff --exit-code $BASE..HEAD -- pkg/proxy/handler_groups_test.go \
  pkg/proxy/handler_groups_equivalence_test.go \
  pkg/proxy/group_fallback_hidden_member_test.go \
  pkg/proxy/group_fallback_part2_test.go \
  pkg/proxy/handler_groups_shadow_member_test.go \
  pkg/proxy/handler_groups_metering_test.go \
  pkg/proxy/handle_models_group_test.go \
  pkg/proxy/win_clear_winner_identity_test.go \
  pkg/store/database/store_group_skipcache_test.go \
  pkg/modelscache/models_group_cache_test.go \
  pkg/grouplb/engine_test.go
# expect: EMPTY diff. Any non-empty diff is a STOP (see § Risks & abort criteria).
```

**Net-churn verification (phase 1's declared "net churn on existing tests: ZERO expectation changes").** The files above must gain **new sibling functions only** — no pre-existing test function's assertions may change. The gate VERIFIES this mechanically, because "the file was touched" is not the same as "the expectation moved":

```bash
# 1. the base test-function inventory (top-level names + their file)
git show "$BASE" --stat -- '*_test.go' >/dev/null
git grep -h -- '^func Test' "$BASE" -- '*_test.go' \
  | sed -E 's/^func (Test[A-Za-z0-9_]+)\(.*/\1/' | sort -u > "$LOGDIR/base_test_funcs.txt"
# 2. the head inventory
git grep -h -- '^func Test' -- '*_test.go' \
  | sed -E 's/^func (Test[A-Za-z0-9_]+)\(.*/\1/' | sort -u > "$LOGDIR/head_test_funcs.txt"
# 3. every pre-existing function must still exist (no silent rename/deletion)
comm -23 "$LOGDIR/base_test_funcs.txt" "$LOGDIR/head_test_funcs.txt" \
  | tee "$LOGDIR/base_funcs_missing.txt"      # MUST BE EMPTY
# 4. for any PRE-EXISTING function whose body changed, the reviewer must be
#    able to name it + justify it. Produce the candidate list:
git diff $BASE..HEAD -- '*_test.go' \
  | grep -E '^\-func (Test[A-Za-z0-9_]+)' | sed -E 's/^-func (Test[A-Za-z0-9_]+).*/\1/' | sort -u \
  | tee "$LOGDIR/pre_existing_funcs_touched.txt"
#    phase 1 declares this list EMPTY. If it is NOT empty, every name in it must
#    appear in the RESULTS report with a one-line justification; an
#    unjustified pre-existing expectation change is a STOP (see R17).
```

**FE counterpart.** **ONE authoritative classification governs the FE test-file sets: the heredoc lists in § Execution order step 3** (`expected_fe_test_files.txt` = 5 NEW, `updated_fe_test_files.txt` = 5 UPDATED — including `DecisionModelForm.test.tsx`, the V1 carrier — `left_alone_fe_test_files.txt` = 7 LEFT ALONE; see phase2 §Amendment 2's final list). This block does NOT re-classify; it asserts the byte-identity of the LEFT-ALONE set **by reference**, reading the same list the command uses:

```bash
# byte-identity of the LEFT-ALONE set — sourced from the SINGLE authoritative list:
while read -r f; do
  git diff --exit-code $BASE..HEAD -- "$f" || { echo "FAIL left-alone modified: $f"; exit 1; }
done < "$LOGDIR/left_alone_fe_test_files.txt"
echo "OK left-alones byte-identical ($(wc -l < "$LOGDIR/left_alone_fe_test_files.txt") files)"
# expect: one OK line, zero FAIL lines. DecisionModelForm.test.tsx is NOT here —
# it is an UPDATED file (V1) and is asserted in step 6 instead.
```

### RECEIVE NEW FUNCTIONS — existing assertions stay UNCHANGED

> **CORRECTION to this plan's earlier draft.** The earlier draft of this section predicted that the **existing** tables in `group_rules_test.go`, `kind_rules_parity_test.go` and `store_group_test.go` would have their expectations **rewritten** (a "meaning change" for the parameterized predicate). **Phase 1's actual design avoids that entirely**: the existing tables are **LEFT ALONE** and the new lane cases are added as **new sibling functions**, because the existing tables' `mutate func(m *models.ModelConfig)` signature cannot seed a *collection* (a decision member row). The gate therefore becomes **stronger**: the pre-existing assertions must stay byte-identical, not merely "still pass".

| File | What phase 1 does | What stays byte-identical | Ruling / contract |
|------|-------------------|---------------------------|-------------------|
| `pkg/models/group_rules_test.go` | adds **5 new functions** (decision members accepted / mixed rejected / decision-group rejects a chat member / legacy `""` is the chat lane / shadow target must match the lane). The existing table `TestValidateGroupMembers_CollectionRuleViolations` (`:105`) is **LEFT ALONE** — the image-gen member case has an empty valid-lane set, so the chat wording still matches byte-for-byte | `TestValidateGroupMembers_CollectionRuleViolations` and every other existing function in the file | blast site #1; contract #1 |
| `pkg/ui/kind_rules_parity_test.go` | adds **1 new function** `TestKindRules_Parity_Group_DecisionLane` with its own fixture helper (`addDecisionGroupMembers`); the shared `addGroupMembers` fixture and the existing parity table (`:520-620`) are **LEFT ALONE**; the provider-vocabulary parity (`:312-317`) is **LEFT ALONE** | the whole existing table + the provider pin | blast sites #2, #5–#8; contracts #1, #4, #18 |
| `pkg/store/database/store_group_test.go` | adds **6 new functions** (lane inference / decision member picked / mixed fails closed / legacy `""` not servable in a decision group / mixed rejected on the write path / decision members accepted on the write path). `TestGroup_AddModel_GViolations` (`:146`), `TestGroup_RewriteGroupEntries_Am28StaleMember` (`:592`) and `TestGroup_C1_BackupLessGroupsKeepV1Semantics` (`:926`) are **LEFT ALONE** — the Am28 fixture's members are *disabled*, not retyped, so the predicate's `!row.Enabled` arm fires first and the behaviour is identical | every existing function in the file | blast sites #4, #16, #17; contracts #2, #3 |
| `pkg/decision/handler_test.go`, `pkg/decision/proxy_handler_decision_models_test.go` | **NOT modified at all.** The group-branch coverage lives in the NEW `pkg/decision/handler_group_test.go` (**27** functions after Amendment 1 — the earlier "14" is **SUPERSEDED by Amendment 1 §1**) | `TestHandler_ResolveModel_NotDecisionKind_400` (`:692`), `TestDecision_Resolver_ChatModelMisroute_400_DecisionMisroute` (`:156`), `TestDecision_Resolver_ImageGenModelMisroute_400_DecisionMisroute` (`:188`) — the gate is **EXTENDED** with an explicit group branch, not widened | blast sites #9–#14; contracts #5–#14 |
| `pkg/proxy/handle_chat_misroute_test.go` | adds **1 new function** `TestHandleChatCompletions_RejectsDecisionGroupMisroute` | the 3 existing functions | blast site #20; contract #16 |
| `pkg/proxy/handler_anthropic_misroute_test.go` | adds **1 new function** `TestAnthropicBoundary_DecisionGroupMisroute`. **The file EXISTS at base (136 lines)** — the contract is *add a function*, not *create a file* | `TestAnthropicBoundary_DecisionMisroute`, `TestAnthropicBoundary_DecisionMisroute_Negatives` | blast site #21; contract #16 |
| `pkg/proxy/handle_models_imggen_test.go` | adds **1 new function** `TestHandleModels_FilterDecisionGroup` | the 4 existing functions | blast site #22; contract #15 |
| `pkg/modelscache/models_group_lane_test.go` **(NEW file)** | the decorator-through pair: `TestCachedModelsConfig_GroupLane_ReachesInnerManager` (the trap pin — the failure text MUST name the trap: `GroupLane is a no-op through the decorator`) + `TestCachedModelsConfig_GroupLane_MixedGroup_ReturnsFalse` | — | D-8; contract #22 — **MERGE GATE** |
| `pkg/credclass/classify_test.go` **(NEW file)** | the moved vocabulary tables: `TestCredClass_IsQuota_VocabularyTable`, `TestCredClass_IsRateLimit_VocabularyTable`. `pkg/providers/interface_quota_test.go` and `pkg/providers/ratelimit_classifier_test.go` are **unchanged** — the Stage-0 gate | the two provider suites | D-4; contract #21 |

> **"New sibling function" ≠ "regressed"** — and, conversely, a pre-existing function whose assertions DID change is never merely "updated with review": it must be named and justified (see the net-churn verification above and § Risks & abort criteria R17). The reviewer's question per file: "did any pre-existing expectation move?" If yes, and it is not justified, ⇒ STOP.

### Pre-registered flakes (a failure here is ADJUDICATED, never silently ignored)

Format per `.agents/tester/QUARANTINE.md:5-11` — columns `Test | Pack | Date Quarantined | Reason | Retry Budget | Attempts (P/F) | Status`. **Retry budget = 3** for every row below. A quarantined test failing is adjudicated: re-run **isolated**, record the attempts, and cite the QUARANTINE row. A quarantined test failing **with a different signature** is a NEW failure ⇒ STOP.

| Test | Pack | Why it fires | Retry budget | Adjudication |
|------|------|--------------|--------------|--------------|
| `TestEngine_GetOrSelect_TOCTOU_CoolingReverify` | B (`pkg/credentiallb`) | load-context TOCTOU timing race; ~1/10 plain, `-race` deterministically red (fires at `cooldown_test.go:481`/`:517`) | 3 isolated re-runs | isolated PASS ⇒ flake, record. Isolated FAIL ⇒ STOP |
| `TestStoreEngine_PeakHourParity` | A (`pkg/store/database`) | time-of-day: the seeded window `[00:00,23:59)` is **half-open at minute granularity**, so wall-clock minute 23:59 UTC (23:59:00–23:59:59.999) is excluded | 3 isolated re-runs | fires only in that 60-second UTC minute; isolated PASS outside it ⇒ flake, record |
| `TestStoreEngine_CloseLifecycle` | A (`pkg/store/database`) | `t.TempDir()` `RemoveAll` cleanup race ("directory not empty"); assertions never fail | 3 isolated re-runs | isolated PASS ⇒ flake, record |
| `TestHandleTestMCPServerDirect_RedirectTo{Localhost,PrivateIP}Blocked` | B (`pkg/mcp`) | httpbin.org now returns a **server-side 403** for `redirect-to?url=<internal>` before the proxy's `CheckRedirect` classifier runs | 2 (base+feature) | external dependency; identical signature ⇒ known, record |
| `TestValidateUpstreamURL/SSRF_{octal,decimal,hex}_IP_should_be_rejected` | B (`pkg/mcp`) | was env-deterministic; **PASSING on this VM since 2026-11-06** — stale row, formal un-quarantine pending | 3 isolated | currently green; if red ⇒ STOP (not a flake) |
| `TestConcurrentIncrementModelUsage_Stress`, `TestConcurrentDifferentBuckets_Stress` | B (`pkg/usage`) | SQLITE_BUSY "database is locked (5)" under concurrent stress | 3 isolated | isolated PASS ⇒ flake, record |

**Rule:** the QUARANTINE.md file itself is **not** modified during the gate (skip-wiring is deferred by house convention). New flakes discovered by this gate are **recorded in the RESULTS report** and proposed to the skill-keeper/leader — not silently patched.

### `-run` trap (operator warning)

`go test -run` splits on `/` and the **first element is the TOP-LEVEL test name**. A bare subtest name selects **ZERO** tests and exits 0 with `no tests to run` — a silent vacuous PASS. Therefore:

- Never re-run a quarantined test with a subtest-only `-run` pattern.
- Every isolated re-run must show `=== RUN` **and** `--- PASS`/`--- FAIL` for the intended test.
- The house display filter is `grep -v '=== RUN'`.

---

## Execution order (copy-pasteable)

> **Shell discipline.** `dash` has no `PIPESTATUS` → wrap pipelines in `bash -c`. `git --no-pager` for every git read. Long `go test` runs go to a **detached log** and are polled — never run inline where a 120s run blows the interaction window. Interactive commands get `timeout 180`.

```bash
# ── step 1: hermetic environment ─────────────────────────────────────────────
export WT=/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-decision-groups
export GOTOOLCHAIN=local
export GOPATH=/home/nea/go GOMODCACHE=/home/nea/go/pkg/mod GOCACHE=/home/nea/.cache/go-build
export GO=/home/nea/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.linux-amd64/bin/go
export LOGDIR=/tmp/dg-gate/$(date -u +%Y%m%dT%H%M%SZ); mkdir -p "$LOGDIR"
# HOME quarantine — the config.json rewrite (pkg/config/config.go Load()
# create-if-absent + applyEnvOverrides; pkg/proxy sets APPLY_ENV_OVERRIDES=1)
# writes into $HOME/.config/llm-supervisor-proxy/config.json
# FIX 4 (approver iteration-002): capture the ABSOLUTE REAL-HOME config path
# BEFORE the quarantine export — otherwise the guard hashes the quarantine's
# (absent) config and can never fire.
REAL_HOME_CONFIG="$(getent passwd "$(id -u)" | cut -d: -f6)/.config/llm-supervisor-proxy/config.json"
[ -f "$REAL_HOME_CONFIG" ] && sha256sum "$REAL_HOME_CONFIG" > "$LOGDIR/config_real_before.sha" \
  || echo "absent" > "$LOGDIR/config_real_before.sha"
export HOME=$(mktemp -d)/dg-home; mkdir -p "$HOME"     # quarantine (the ACTUAL protection)
unset DATABASE_URL            # → SQLite inside the quarantined HOME
$GO version                    # expect: go version go1.26.0 linux/amd64
cd "$WT"
git --no-pager status --porcelain      # expect: EMPTY
export BASE=$(git merge-base feature/decision-groups origin/latest)
echo "BASE=$BASE"

# ── step 2: fetch + confirm topology (READ ONLY) ─────────────────────────────
timeout 180 git fetch origin
git --no-pager log --oneline -1 origin/latest
git --no-pager log --oneline -1 origin/master
# sanity: the feature branch must be a descendant-or-equal of origin/latest,
# or the step-8 merge will be a real (possibly conflicting) merge — that is OK,
# but it must be KNOWN, not discovered at push time.

# ── step 3: added-test inventory (feeds the anti-vacuous check) ─────────────
# *(SUPERSEDED by Amendment 1 §1 — the expected list below is the FINAL 50;
#   the earlier 33-name list is recorded in § Cross-plan reconciliation R1.)*
# The EXPECTED list is CONCRETE (phase1-plan.md "## Test plan" + phase2-plan.md
# "## 6. Tests") — 69 ADDED Go top-level test names (60 Pack A + 9 Pack B)
# + 5 NEW FE test files (45 cases) + 5 UPDATED FE test files (3 new cases).
# Pack A = 41 names · Pack B = 9 names.   # *(AMENDED by Amendment 1 §1 — was 24 / 9)*
cat > "$LOGDIR/expected_go_tests.txt" <<'EOF'
# Pack A — pkg/decision/handler_group_test.go (NEW file) (34)
TestDecision_DoctrineLine_PinnedVerbatimAtSeam
TestDecision_Group_400_413_415_422_FailFastAdvanceBlocked
TestDecision_Group_401_RotatesWithinMember
TestDecision_Group_5xx_AdvancesWithoutRotationOrMark
TestDecision_Group_AllCredentialsCooling_PolicyFirstCredentialHonestAttempt
TestDecision_Group_AllMarked_PolicyHeadHonestAttempt
TestDecision_Group_AllMembersFail_502DecisionAllMembersFailed
TestDecision_Group_AttemptOrder_PolicyOrderMinusMarked
TestDecision_Group_CacheLayerSeparation_SharedUpstreamInstanceOneAnswer
TestDecision_Group_ChainTargetNotConflatedWithMember
TestDecision_Group_ChatInferredWithDecisionMember_NeverSilentlyServes
TestDecision_Group_DefaultPoolFirst_BackupPoolAfter
TestDecision_Group_Exhaustion_5xxMemberCountsAsGenuinelyFailed
TestDecision_Group_Exhaustion_DoesNotReattemptTriedMember
TestDecision_Group_Exhaustion_ExcludesSuppressedAttempts
TestDecision_Group_FirstFailsSecondSucceeds_ExactlyOneCommit
TestDecision_Group_InactiveMember_NotOfferedAsCandidate
TestDecision_Group_LaneSeamAbsent_FailsClosed400
TestDecision_Group_LayerSeparation_CredentialMarkCannotChangePick
TestDecision_Group_MarkedHead_ZeroUpstreamCallSkip
TestDecision_Group_MemberFailedPredicate_OwnResilienceExhausted
TestDecision_Group_MemberNeverAutoEnabled
TestDecision_Group_MemberWithTwoCredentials_LimitClassRotatesBeforeAdvancing
TestDecision_Group_NoCooldownWriteOnSuppressedPick
TestDecision_Group_NoWriteBeforeWinner
TestDecision_Group_NonGroupRequest_ByteIdentical
TestDecision_Group_PickAttemptSkipEventOutcomes
TestDecision_Group_PolicyPick_IgnoresMarks
TestDecision_Group_ResolvesToDecisionMember
TestDecision_Group_SingleMemberGroup_FullyConsumable
TestDecision_Group_StampedStatusDrivesClassification
TestDecision_Group_ThroughCachedConfig
TestDecision_Group_WeightsAffectPick
TestDecision_Group_WinClear_ClearsWinnerNotFailedHead
# Pack A — pkg/decision/vocabulary_pin_test.go (NEW file) (2)
TestDecision_Vocabulary_SourceLiteralPin_FiveSurfaces
TestDecision_Vocabulary_RegistryKeySetEqualsGateAllowList
# Pack A — pkg/decision/handler_test.go (1)
TestPackage_ImportGuard_GroupPolicyFiles_NoCacheImports
# Pack A — pkg/proxy/handler_test.go (1)
TestHandleChatCompletions_RejectsDecisionGroupMisroute_ThroughCachedConfig
# Pack A — pkg/proxy/handle_chat_misroute_test.go (1)
TestHandleChatCompletions_RejectsDecisionGroupMisroute
# Pack A — pkg/proxy/handle_models_imggen_test.go (2)
TestHandleModels_FilterDecisionGroup
TestHandleModels_FilterDecisionGroup_ThroughCachedConfig
# Pack A — pkg/proxy/handler_anthropic_misroute_test.go (2)
TestAnthropicBoundary_DecisionGroupMisroute
TestAnthropicBoundary_DecisionGroupMisroute_ThroughCachedConfig
# Pack A — pkg/store/database/store_group_test.go (16)
TestGroup_AddModel_DecisionMembers_Accepted
TestGroup_AddModel_DecisionMembers_MixedProviders_Accepted
TestGroup_AddModel_MixedKindMembers_Rejected
TestGroup_GroupLane_InfersChat
TestGroup_GroupLane_InfersDecision
TestGroup_GroupLane_MixedGroupReturnsFalse
TestGroup_RewriteGroupEntries_DecisionGroupInChatChain_BoundaryRefusal
TestGroup_RewriteGroupEntries_DecisionGroup_RefusedOnTextSweep
TestGroup_RewriteGroupEntries_DecisionGroup_RejectsLegacyEmptyKindMember
TestGroup_RewriteGroupEntries_MixedKindGroup_FailsClosed
TestGroup_SingleMemberDecisionGroup_MemberRetypeRefused
TestGroup_SweepKindParameterization_MutationControl
TestUpdateModel_FallbackChain_DecisionGroupTarget_Rejected
TestUpdateModel_KindChange_GroupMember_Rejected
TestUpdateModel_KindChange_GroupMember_RejectionNamesGroupAndMember
TestUpdateModel_KindChange_NonMember_Succeeds
TestDecision_Vocabulary_CredentialModelSplit_AnthropicSaves_ModelRejected
# Pack A — pkg/ui/kind_rules_parity_test.go (4)
TestDecision_Vocabulary_OutOfSetProvider_Rejected_AtAllGates
TestKindRules_Parity_Group_DecisionLane
TestKindRules_Parity_Group_DecisionMembers_MixedProviders
TestKindRules_Parity_KindImmutability
# Pack B — pkg/credclass (NEW package) (2)
TestCredClass_IsQuota_VocabularyTable
TestCredClass_IsRateLimit_VocabularyTable
# Pack B — pkg/models/group_rules_test.go (5)
TestValidateGroupMembers_DecisionMembers_Accepted
TestValidateGroupMembers_MixedKindMembers_Rejected
TestValidateGroupMembers_DecisionGroup_RejectsChatMember
TestValidateGroupMembers_InactiveChainOnlyTarget_Accepted
TestValidateGroupMembers_LegacyEmptyKind_IsChatLane
TestValidateGroupMembers_DecisionGroup_ShadowTargetMustMatchLane
# Pack B — pkg/modelscache/models_group_lane_test.go (NEW file) (2)
TestCachedModelsConfig_GroupLane_ReachesInnerManager
TestCachedModelsConfig_GroupLane_MixedGroup_ReturnsFalse
EOF
grep -v '^#' "$LOGDIR/expected_go_tests.txt" | grep -v '^$' | sort -u > "$LOGDIR/expected_go_tests.clean"
echo "EXPECTED added Go test names: $(wc -l < "$LOGDIR/expected_go_tests.clean")  # MUST BE 74 (64 Pack A + 10 Pack B)"
# WITHDRAWN names (phase1 A16 x3 + Amendment 2 B2 x1 + Amendment 5 E11 x1)
# — must NOT be implemented. If one appears in the diff, G4 FAILS (R21).
cat > "$LOGDIR/withdrawn_go_tests.txt" <<'EOF'
TestDecision_Group_MarkedMember_DemotedNotFiltered
TestDecision_Group_NoCooldownWriteOnBenchedPick
TestGroup_RewriteGroupEntries_DecisionGroup_PicksDecisionMember
TestDecision_Group_MemberWithTwoCredentials_AttemptsBothBeforeAdvancing
TestDecision_Group_SkipCacheInstance_NotSharedWithTextLane
EOF
# the ACTUAL set, from the diff
git diff $BASE..HEAD -- '*.go' \
  | grep -E '^\+func (Test[A-Za-z0-9_]+)' \
  | sed -E 's/^\+func (Test[A-Za-z0-9_]+).*/\1/' | sort -u > "$LOGDIR/added_go_tests.txt"
echo "ACTUAL added Go test names:   $(wc -l < "$LOGDIR/added_go_tests.txt")"
# BOTH directions — a name in either set without the other is a STOP
comm -23 "$LOGDIR/expected_go_tests.clean" "$LOGDIR/added_go_tests.txt" > "$LOGDIR/listed_but_absent.txt"   # expected, not in diff
comm -13 "$LOGDIR/expected_go_tests.clean" "$LOGDIR/added_go_tests.txt" > "$LOGDIR/present_but_unlisted.txt" # in diff, not listed
cat "$LOGDIR/listed_but_absent.txt" "$LOGDIR/present_but_unlisted.txt"
# BOTH files MUST be empty — EXCEPT the single documented D-8 branch, which is
# REPORTED FOR ADJUDICATION, not auto-failed (see the note in step 6):
#   phase1 §B1: IF the ADR-1 membership derivation proves a new manager method
#   unavoidable, D-8 / contract 14 adds a decorator-forward + decorator-through
#   test whose name is NOT in the 69. Such a name is adjudicated, not failed.
# WITHDRAWN names must be ABSENT from the diff:
grep -Fxf "$LOGDIR/withdrawn_go_tests.txt" "$LOGDIR/added_go_tests.txt" \
  && { echo "G4 FAIL: a WITHDRAWN test name is implemented"; exit 1; } \
  || echo "withdrawn names absent: OK"
# NEW FE test files (phase2 Amendment 2 §5 + ADR-19) — 5 NEW, 45 cases:
cat > "$LOGDIR/expected_fe_test_files.txt" <<'EOF'
pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx
pkg/ui/frontend/src/components/config/__tests__/GroupModelsTab.decisionLane.test.tsx
pkg/ui/frontend/src/components/config/__tests__/GroupMemberEditor.decisionLane.test.tsx
pkg/ui/frontend/src/components/config/__tests__/GroupModelForm.decisionLane.test.tsx
pkg/ui/frontend/src/components/config/__tests__/groupLane.test.ts
EOF
# FE UPDATED files (5) — 3 mechanical (assertions unchanged) + T1 + F1 + V1:
cat > "$LOGDIR/updated_fe_test_files.txt" <<'EOF'
pkg/ui/frontend/src/components/config/__tests__/GroupModelsTab.test.tsx
pkg/ui/frontend/src/components/config/__tests__/GroupModelForm.test.tsx
pkg/ui/frontend/src/components/config/__tests__/GroupMemberEditor.test.tsx
pkg/ui/frontend/src/components/__tests__/EventLog.test.tsx
pkg/ui/frontend/src/components/config/__tests__/DecisionModelForm.test.tsx
EOF
# FE LEFT-ALONE files (7) — byte-identical, no edit:
cat > "$LOGDIR/left_alone_fe_test_files.txt" <<'EOF'
pkg/ui/frontend/src/components/__tests__/SettingsPage.tabs.test.tsx
pkg/ui/frontend/src/components/config/__tests__/DecisionModelsTab.test.tsx
pkg/ui/frontend/src/components/config/__tests__/DecisionModelsTab.decisionTest.test.tsx
pkg/ui/frontend/src/hooks/__tests__/useDecisionModels.test.ts
pkg/ui/frontend/src/hooks/__tests__/useGroupModels.test.ts
pkg/ui/frontend/src/hooks/__tests__/useApiCredentials.test.ts
pkg/ui/frontend/src/utils/__tests__/tsc-baseline.test.ts
EOF

# ── step 4: gofmt zero-NEW-drift ─────────────────────────────────────────────
# base tree via `git archive` — NEVER cp -a the worktree: .git is a POINTER
# file and a copy resolves the REAL worktree's git state (2026-11-09 incident).
mkdir -p "$LOGDIR/base" && git archive "$BASE" | tar -x -C "$LOGDIR/base"
( cd "$LOGDIR/base" && find . -name '*.go' -not -path './.git/*' | sed 's|^\./||' \
    | sort | xargs -r gofmt -l | sed 's|^\./||' | sort > "$LOGDIR/fmt_base.txt" )
( cd "$WT" && { git ls-files '*.go'; git ls-files --others --exclude-standard '*.go'; } \
    | sort -u | xargs -r gofmt -l | sort > "$LOGDIR/fmt_head.txt" )
comm -23 "$LOGDIR/fmt_head.txt" "$LOGDIR/fmt_base.txt" > "$LOGDIR/fmt_new.txt"
cat "$LOGDIR/fmt_new.txt"          # MUST BE EMPTY
# Per-file disambiguation for any file present in BOTH sets: compare the actual
# hunks (`gofmt -d <file>` vs `git show $BASE:<file> | gofmt -d`), NOT hunk overlap
# (±3 context lines distort) — use the flagged-LINE-SET difference.
# ALSO: after any aligned-fixture key-removal sweep, run `gofmt -l` on ALL
# touched _test.go files — sweeps silently add whole-file drift:
git diff --name-only $BASE..HEAD -- '*_test.go' | xargs -r gofmt -l | sort > "$LOGDIR/fmt_touched_tests.txt"

# ── step 5: Pack A + Pack B (detached, long) ─────────────────────────────────
nohup bash -c "cd '$WT' && timeout 300 '$GO' test -v -count=1 -timeout=240s \
  ./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/... \
  > '$LOGDIR/A.log' 2>&1; echo \$? > '$LOGDIR/A.exit'" >/dev/null 2>&1 &
nohup bash -c "cd '$WT' && PKGS=\$('$GO' list ./... | grep -vE 'github\.com/disillusioners/llm-supervisor-proxy/(cmd|pkg/(proxy|store|decision|ui))(/|\$)') && \
  timeout 300 '$GO' test -v -count=1 -timeout=240s \$PKGS \
  > '$LOGDIR/B.log' 2>&1; echo \$? > '$LOGDIR/B.exit'" >/dev/null 2>&1 &
# poll (do NOT wait inline):
#   while [ ! -f "$LOGDIR/A.exit" ]; do sleep 15; tail -3 "$LOGDIR/A.log"; done
#   cat "$LOGDIR/A.exit" "$LOGDIR/B.exit"
# exit codes: 0=PASS, 1=FAIL, 124=TIMEOUT

# ── step 6: anti-vacuous (every ADDED test must show a literal --- PASS) ─────
# The grep is END-ANCHORED: "--- PASS: <name> " (a space follows the name in
# go test -v output). This matters: `TestHandleModels_FilterDecision` (EXISTING,
# pkg/proxy/handle_models_imggen_test.go:134) is a STRICT PREFIX of the new
# `TestHandleModels_FilterDecisionGroup` — an unanchored grep would let the
# OLD test's PASS line satisfy the NEW test's name. Same hazard for any future
# name pair. Never use `grep -q "^--- PASS: $t"` without the trailing anchor.
: > "$LOGDIR/anticvacuous.txt"
while read -r t; do
  [ -z "$t" ] && continue
  if grep -qE -- "^--- PASS: $t( |$)" "$LOGDIR/A.log" "$LOGDIR/B.log"; then
    echo "OK   $t" >> "$LOGDIR/anticvacuous.txt"
  else
    echo "MISS $t" >> "$LOGDIR/anticvacuous.txt"   # ← a MISS is a STOP
  fi
done < "$LOGDIR/expected_go_tests.clean"
echo "anti-vacuous OK: $(grep -c '^OK' "$LOGDIR/anticvacuous.txt") / $(wc -l < "$LOGDIR/expected_go_tests.clean")"
grep -c '^MISS' "$LOGDIR/anticvacuous.txt"    # MUST be 0
# D-8 branch adjudication (whitelist-of-one) — an extra added test name that is
# NOT in the 59 is REPORTED, not failed, when it is the documented D-8 /
# contract-14 decorator-through test (phase1 Amendment 2 §B1:1595):
comm -13 "$LOGDIR/expected_go_tests.clean" "$LOGDIR/added_go_tests.txt" \
  | grep -E '^Test' | tee "$LOGDIR/unlisted_extra.txt"
if [ -s "$LOGDIR/unlisted_extra.txt" ]; then
  echo "G4 ADJUDICATION: $(wc -l < "$LOGDIR/unlisted_extra.txt") unlisted added test name(s):"
  cat "$LOGDIR/unlisted_extra.txt"
  echo "-> if this is the documented D-8/contract-14 decorator-through test"
  echo "   (phase1 Amendment 2 §B1:1595), RECORD it in the RESULTS report as an"
  echo "   ADJUDICATION (whitelist-of-one) — it is NOT an auto-fail. Any other"
  echo "   unlisted name IS a FAIL (R3)."
fi
# name-collision guard: no EXPECTED name may already exist at base, and no two
# EXPECTED names may be prefixes of one another (the silent wrong-reason PASS):
while read -r t; do
  if git show "$BASE" -- >/dev/null 2>&1 && git grep -l -- "^func $t(" "$BASE" -- '*_test.go' >/dev/null 2>&1; then
    echo "COLLISION (already exists at base): $t"
  fi
done < "$LOGDIR/expected_go_tests.clean"
# FE equivalent — NEW files (5, 45 cases): each must appear in the vitest
# report with at least one passing test (a mis-named file would silently
# vanish from the report):
for f in $(cat "$LOGDIR/expected_fe_test_files.txt"); do
  b=$(basename "$f")
  grep -q -- "$b" "$LOGDIR/fe_vitest.log" && echo "OK   FE $b" || echo "MISS FE $b"
done
# FE equivalent — UPDATED files (5): the 3 mechanical ones must still PASS with
# their assertions unchanged; EventLog.test.tsx carries F1 (an event type
# outside the FE EventType union renders via the generic fallbacks) and
# DecisionModelForm.test.tsx carries V1 (the provider-surface set-equality pin,
# the FE half of T5) — each NEW case must show a pass:
for f in $(cat "$LOGDIR/updated_fe_test_files.txt"); do
  b=$(basename "$f")
  grep -q -- "$b" "$LOGDIR/fe_vitest.log" && echo "OK   FE-UPD $b" || echo "MISS FE-UPD $b"
done
# FIX 3 (approver iteration-002): every promised FE CASE is asserted by a grep
# that CAN fire — verbose reporter (step 9) prints one line per test with the
# full title, and phase 2's naming contract puts the case id FIRST in the title.
# ALL 48 promised case ids are enumerated (the 45 new-file cases A1–E8 plus the
# 3 updated-file cases T1/F1/V1); each is grepped with a title-anchored pattern
# so a file name or a passing neighbour can never satisfy another case's token:
# FILE-SCOPED census (tester AMBER): the token list carries the FILE the phase-2
# contract assigns to each token, and each token is censused ONLY within that
# file's own log lines. WHY: upstream's credential-groups commission reused the
# A1-A13 / B1-C7 / D1-D4 / E1-E3 label space in 5 of THEIR files, so 35 of our
# tokens match 2-3 lines in a WHOLE-LOG census. OUR contract still holds — each
# of our 48 tokens has exactly ONE line across OUR 8 files — so the correct
# check is the file-scoped one, not a rename.
cat > "$LOGDIR/expected_fe_case_tokens.tsv" <<'EOF'
A1	pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx
A2	pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx
A3	pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx
A4	pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx
A5	pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx
A6	pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx
A7	pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx
A8	pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx
A9	pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx
A10	pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx
A11	pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx
A12	pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx
A13	pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx
B1	pkg/ui/frontend/src/components/config/__tests__/GroupModelsTab.decisionLane.test.tsx
B2	pkg/ui/frontend/src/components/config/__tests__/GroupModelsTab.decisionLane.test.tsx
B3	pkg/ui/frontend/src/components/config/__tests__/GroupModelsTab.decisionLane.test.tsx
B4	pkg/ui/frontend/src/components/config/__tests__/GroupModelsTab.decisionLane.test.tsx
B5	pkg/ui/frontend/src/components/config/__tests__/GroupModelsTab.decisionLane.test.tsx
B6	pkg/ui/frontend/src/components/config/__tests__/GroupModelsTab.decisionLane.test.tsx
B7	pkg/ui/frontend/src/components/config/__tests__/GroupModelsTab.decisionLane.test.tsx
B9	pkg/ui/frontend/src/components/config/__tests__/GroupModelsTab.decisionLane.test.tsx
C1	pkg/ui/frontend/src/components/config/__tests__/GroupMemberEditor.decisionLane.test.tsx
C2	pkg/ui/frontend/src/components/config/__tests__/GroupMemberEditor.decisionLane.test.tsx
C3	pkg/ui/frontend/src/components/config/__tests__/GroupMemberEditor.decisionLane.test.tsx
C4	pkg/ui/frontend/src/components/config/__tests__/GroupMemberEditor.decisionLane.test.tsx
C5	pkg/ui/frontend/src/components/config/__tests__/GroupMemberEditor.decisionLane.test.tsx
C6	pkg/ui/frontend/src/components/config/__tests__/GroupMemberEditor.decisionLane.test.tsx
C7	pkg/ui/frontend/src/components/config/__tests__/GroupMemberEditor.decisionLane.test.tsx
D1	pkg/ui/frontend/src/components/config/__tests__/GroupModelForm.decisionLane.test.tsx
D2	pkg/ui/frontend/src/components/config/__tests__/GroupModelForm.decisionLane.test.tsx
D3	pkg/ui/frontend/src/components/config/__tests__/GroupModelForm.decisionLane.test.tsx
D4	pkg/ui/frontend/src/components/config/__tests__/GroupModelForm.decisionLane.test.tsx
D5	pkg/ui/frontend/src/components/config/__tests__/GroupModelForm.decisionLane.test.tsx
D6	pkg/ui/frontend/src/components/config/__tests__/GroupModelForm.decisionLane.test.tsx
D8	pkg/ui/frontend/src/components/config/__tests__/GroupModelForm.decisionLane.test.tsx
D9	pkg/ui/frontend/src/components/config/__tests__/GroupModelForm.decisionLane.test.tsx
D10	pkg/ui/frontend/src/components/config/__tests__/GroupModelForm.decisionLane.test.tsx
E1	pkg/ui/frontend/src/components/config/__tests__/groupLane.test.ts
E2	pkg/ui/frontend/src/components/config/__tests__/groupLane.test.ts
E3	pkg/ui/frontend/src/components/config/__tests__/groupLane.test.ts
E4	pkg/ui/frontend/src/components/config/__tests__/groupLane.test.ts
E5	pkg/ui/frontend/src/components/config/__tests__/groupLane.test.ts
E6	pkg/ui/frontend/src/components/config/__tests__/groupLane.test.ts
E7	pkg/ui/frontend/src/components/config/__tests__/groupLane.test.ts
E8	pkg/ui/frontend/src/components/config/__tests__/groupLane.test.ts
T1	pkg/ui/frontend/src/components/config/__tests__/GroupMemberEditor.test.tsx
F1	pkg/ui/frontend/src/components/__tests__/EventLog.test.tsx
V1	pkg/ui/frontend/src/components/config/__tests__/DecisionModelForm.test.tsx
EOF
while IFS="$(printf '\t')" read -r tok f; do
  [ -n "$tok" ] || continue
  # vitest prints paths RELATIVE to pkg/ui/frontend/ (e.g. `src/components/...`),
  # while the TSV carries repo-root-absolute paths — strip the prefix so the
  # file filter matches (tester-reproduced: unstripped ⇒ OK=0/MISS=48 on a green
  # log — a FALSE STOP; stripped ⇒ OK=48/MISS=0).
  rel="${f#pkg/ui/frontend/}"
  n=$(grep -F "$rel" "$LOGDIR/fe_vitest.log" | grep -Ec "[^A-Za-z0-9]${tok}[^A-Za-z0-9]*[—:-]")
  if [ "$n" -eq 1 ]; then echo "OK   FE case $tok (file-scoped, $n hit)"
  else echo "MISS FE case $tok (file-scoped, $n hits — expected exactly 1)"; fi
done < "$LOGDIR/expected_fe_case_tokens.tsv"
# (the pattern requires the token as a WHOLE WORD followed by the title's
#  separator — an A1 hit can never satisfy A10, and a file path can never
#  satisfy a case id. The census is FILE-SCOPED so an upstream commission's
#  reuse of the same label space in ITS files cannot inflate our counts.
#  `MISS` — or a count != 1 — on any token ⇒ the case was not run, ran twice,
#  or the naming contract was broken ⇒ the gate fails.)
grep -v '=== RUN' "$LOGDIR/A.log" | grep -E '^(--- |ok |FAIL|PASS)' | tail -40

# ── step 7: build + vet + import guards ─────────────────────────────────────
timeout 300 $GO vet ./...            2>&1 | tee "$LOGDIR/vet.log"     # exit 0
timeout 300 $GO build ./...          2>&1 | tee "$LOGDIR/build_all.log" # exit 0
timeout 300 $GO build -o "$LOGDIR/llm-supervisor-proxy" ./cmd 2>&1 | tee "$LOGDIR/build_cmd.log"
ls -lh "$LOGDIR/llm-supervisor-proxy"   # ~26 MB — the REAL entry
#   (the repo ROOT is a package-main STUB: test_load.go, ~3.7 MB, exits 0
#    without binding a port — `go build .` proves nothing)
# ── IMPORT GUARDS — TWO LAYERS (ADR-14) ──────────────────────────────────────
# Layer 2 = three `grep -rnE` gates wired into `make test` (Makefile:51-68:
# the `test:` target; the gates at :54-57 / :59-62 / :64-67; `go test ./...`
# at :68). **Plain `go test` alone does NOT run them** — run `make test`, or
# the three greps explicitly:
grep -rnE 'github\.com/disillusioners/llm-supervisor-proxy/pkg/proxy(/|")' pkg/imggen/ && echo "GUARD FAIL imggen->proxy"
grep -rnE 'github\.com/disillusioners/llm-supervisor-proxy/pkg/proxy(/|")' pkg/decision/ && echo "GUARD FAIL decision->proxy"
grep -rnE 'github\.com/disillusioners/llm-supervisor-proxy/pkg/imggen(/|")' pkg/decision/ && echo "GUARD FAIL decision->imggen"
# ⇒ all three greps must print NOTHING.
# Layer 1 = the three guard TESTS (pkg/decision/handler_test.go:40-63 / :65-88 /
# :90-109) — run them on the PINNED go1.26 toolchain:
timeout 180 "$GO" test ./pkg/decision/ -run 'TestPackage_ImportGuard' -count=1 -v
# ⇒ literal `=== RUN` + `--- PASS` for each of the three.
# TOOLCHAIN: use $GO (the pinned cached go1.26). /usr/bin/go (1.22.2) FAILS on
# go.mod offline (it tries to download go1.26: "toolchain not available").
# SCOPE: the guard's transitive `go list -deps` walk covers NON-TEST deps only.
#
# ⚠ RAW-TEXT TRAP (ADR-14 item 2): the guard scans EVERY .go file in the
# package dir, INCLUDING _test.go, as RAW TEXT — a COMMENT that merely QUOTES
# a banned path FAILS the guard (proven by ADR-14's negative control). So any
# doctrine-line pin or layering documentation written INSIDE pkg/decision must
# NOT name those paths — not even in a comment or a doc string. This is a
# DIFFERENT mechanism from the cachelayer AST pin: there is no AST precision
# here.
#
# ⚠ PLACEMENT CONSTRAINT (ADR-14 item 3): credential-aware logic — the
# upstream-layer cache reader/writer and the per-credential rotation — lives IN
# pkg/decision and must NEVER be pushed down into pkg/grouplb, which BANS
# pkg/credentiallb (pkg/grouplb/engine_test.go:401, TestEngine_ZeroCredentialImportGuard:
# "the engine itself must not know credentiallb at all").

# ── step 8: boundaries ───────────────────────────────────────────────────────
# B4 (approver escalation): the pathspec `migrations/` matched ZERO files
# (git ls-files verified) — the real migration tree is nested:
git --no-pager diff --stat $BASE..HEAD -- k8s/ .github/ pkg/store/database/migrations/ pkg/store/database/migrate.go
# MUST be 0 bytes (decision D-7: no migration, no chart change)
# NOTE (fold-to-implementation): the house gate in .agents/tester/PACKS.md:15
# carries the SAME defect (bare `migrations/`); the tester fixes the house doc
# at gate time — recorded here so the fix is not lost.

# ── step 9: FE gates ─────────────────────────────────────────────────────────
cd "$WT/pkg/ui/frontend"
[ -d node_modules ] || npm ci          # node_modules is ABSENT in this worktree
bash -c 'timeout 300 npx vitest run --reporter=verbose 2>&1 | tee "$1"; exit ${PIPESTATUS[0]}' \
  _ "$LOGDIR/fe_vitest.log"; echo "vitest exit=$?"   # expect 0 — a FAILING suite must FAIL the gate
# B3 (approver escalation): (a) the bare `| tee` swallowed vitest's exit code —
# now bash -c + PIPESTATUS[0], mirroring the tsc fix below; (b) PASS-blind grep
# guard — the verbose reporter prints `× <title>` for failures and `✓` for
# passes, so a FAILING case could otherwise be recorded `OK FE case A1`. Assert:
FAIL_LINES=$(grep -c '×' "$LOGDIR/fe_vitest.log"); echo "vitest failure lines: $FAIL_LINES  # MUST BE 0"
PASS_LINES=$(grep -c '✓' "$LOGDIR/fe_vitest.log"); echo "vitest pass lines: $PASS_LINES  # MUST BE >= 47"
[ "$FAIL_LINES" -eq 0 ] || { echo "FAIL vitest reported failures"; exit 1; }
[ "$PASS_LINES" -ge 48 ] || { echo "FAIL vitest pass count below the promised 48 cases"; exit 1; }
# FIX 3 (approver iteration-002): the DEFAULT reporter prints per-FILE lines
# only, so per-case grep tokens can never match. The contract is now MATCHABLE:
#   (a) the suite runs with --reporter=verbose ⇒ ONE LINE PER TEST, printing the
#       full test title;
#   (b) phase 2's NAMING CONTRACT puts the case id FIRST in every new/updated
#       test title (e.g. `test('T1 — text picker still offers an inactive chat
#       model', …)`), so each promised case id appears verbatim in the log.
# tsc: use bash -c + PIPESTATUS[0] (W4) — a bare `| tee` swallows the exit code
# (dash has no PIPESTATUS). tsc --noEmit exits 2 on the frozen baseline.
bash -c 'timeout 240 npx tsc --noEmit 2>&1 | tee "$1"; exit ${PIPESTATUS[0]}' \
  _ "$LOGDIR/fe_tsc.log"; echo "tsc exit=$?"   # expect 2
#   parity verdict = per-file COUNT set + FILE set vs tsc-baseline.txt footer
#   (TOTAL_ERRORS=19 / TOTAL_FILES=9). NOT line numbers.
#   parity verdict = per-file COUNT set + FILE set vs tsc-baseline.txt footer
# vite build + static proof (G11 — ONE rule: assets ride the same commit):
cd "$WT"
find pkg/ui/static -type f | sort | xargs sha256sum > "$LOGDIR/static_before.sha"
( cd pkg/ui/frontend && timeout 300 npm run build ) 2>&1 | tee "$LOGDIR/fe_vite.log"
find pkg/ui/static -type f | sort | xargs sha256sum > "$LOGDIR/static_after.sha"
diff "$LOGDIR/static_before.sha" "$LOGDIR/static_after.sha" && echo "STATIC BYTE-IDENTICAL"
git status --porcelain pkg/ui/static | tee "$LOGDIR/static_status.txt"
#   empty  → OK (deterministic rebuild reproduces the committed assets) ✅ NOT a FAILURE
#   non-empty → the assets changed; they MUST be in the SAME commit as their
#                FE source change (G11's single rule)

# ── step 10: isolated-boot smoke ─────────────────────────────────────────────
SMOKE_HOME=$(mktemp -d)/smoke-home; mkdir -p "$SMOKE_HOME"
# B6 (tester AMBER): the real entry uses the DB-backed ConfigManager
# (migration 017_config_json) — `config.json` is NEVER created, so the smoke
# pair must hash the artifacts that DO exist: `config.db` + `buffers/`.
# BEFORE/AFTER of the SAME pair (the smoke's own quarantine).
( cd "$SMOKE_HOME" && find . -path '*llm-supervisor-proxy/*' \( -name 'config.db' -o -path '*buffers*' \) -type f -exec sha256sum {} + 2>/dev/null ) \
  | sort > "$LOGDIR/config_smoke_before.sha"
echo "(before: $(wc -l < "$LOGDIR/config_smoke_before.sha") artifact(s) — empty is expected pre-boot)" >/dev/null
env -u DATABASE_URL APPLY_ENV_OVERRIDES=1 PORT=14399 HOME="$SMOKE_HOME" \
  "$LOGDIR/llm-supervisor-proxy" > "$LOGDIR/smoke.log" 2>&1 &
SMOKE_PID=$!
sleep 3
for p in /healthz /fe/api/version /v1/models; do
  printf '%s -> ' "$p"; curl -s -o /dev/null -w '%{http_code}\n' "http://127.0.0.1:14399$p"
done   # expect 200 / 200 / 200
kill -TERM "$SMOKE_PID"; wait "$SMOKE_PID"; echo "smoke exit=$?"
grep -ci panic "$LOGDIR/smoke.log"   # expect 0
( cd "$SMOKE_HOME" && find . -path '*llm-supervisor-proxy/*' \( -name 'config.db' -o -path '*buffers*' \) -type f -exec sha256sum {} + 2>/dev/null ) \
  | sort > "$LOGDIR/config_smoke_after.sha"
# B6 (approver escalation): the AFTER-hash must use the SAME real-home path as
# the BEFORE-hash ($REAL_HOME_CONFIG, captured before the quarantine export) —
# the previous line hashed the QUARANTINED $HOME, so with a real config present
# the pair ALWAYS differed (a guaranteed false STOP) and with none it was vacuous.
sha256sum "$REAL_HOME_CONFIG" 2>/dev/null > "$LOGDIR/config_real_after.sha" \
  || echo "absent" > "$LOGDIR/config_real_after.sha"
diff "$LOGDIR/config_real_before.sha" "$LOGDIR/config_real_after.sha" \
  && echo "REAL CONFIG UNTOUCHED"
```

**Port discipline.** Use a port in the 10000–19999 range (house convention) — **not 9797** (the ensemble daemon's ambient `PORT` leaks into the environment) and **not 4321** (the config default). `PORT` is inert without `APPLY_ENV_OVERRIDES=1` (`pkg/config/config.go:474-482`: `applyEnvOverrides` reads `APPLY_ENV_OVERRIDES` then `PORT`; `Defaults.Port = 4321` at `pkg/config/config.go:208`; the server binds `":" + port` at `cmd/main.go:460`).

> **Correction to the blueprint citation.** The house blueprint cites `pkg/config/config.go:462` for the `APPLY_ENV_OVERRIDES` gate; in this worktree the actual lines are **`pkg/config/config.go:474`** (`func applyEnvOverrides`) and **`:475`** (the `APPLY_ENV_OVERRIDES` read), with `PORT` read at **`:482`**. `Defaults.Port = 4321` is at **`:208`** (the blueprint's `:211` is off by 3). Cite the verified lines.

---

## Ship runbook (steps 1–3) — for a FUTURE authorised operator

> **Precondition:** the local gate (§ Execution order) is **GO** and recorded. **Explicit user authorisation is required before any push** (house constraint: "NEVER push without explicit user instruction"). Nothing here may be executed as part of planning.

### Step 1 — publish `feature/decision-groups` → `latest` (explicit-SHA refspec)

> **BASE STATE AT THIS RUN (tester context):** `origin/latest` has moved **again** — now
> **`dd8c585`** (master **`3891e9d`**) — so this ship merge is a **REAL merge** (not a fast-forward),
> and the pre-merge gate certified the **pre-merge HEAD `e5f0816`**. Per **ADR-16**, the **FULL gate
> must be re-run on the MERGED tree** — regardless of whether the merge conflicts — **before any
> deploy step**. The G4 anti-vacuous lists and the base-collision check are re-verified against the
> **merged** base (6 of the planned names landed upstream in `13a8e4e`, present since `a2952a9` —
> **zero commission attribution**, so they are not "added" relative to the merged base).

```bash
cd "$WT"
git fetch origin
export FEATURE_TIP=$(git rev-parse feature/decision-groups)
export LATEST_TIP=$(git rev-parse origin/latest)
git checkout feature/decision-groups
git merge --no-ff origin/latest -m "Merge branch 'origin/latest' into feature/decision-groups"
export MERGE_SHA=$(git rev-parse HEAD)
# GATE 1a — exact parents: MUST be "$FEATURE_TIP $LATEST_TIP"
git --no-pager log --format='%H %P' -1 "$MERGE_SHA"
# GATE 1b — the merge must be a fast-forward FORWARD of origin/latest
git merge-base --is-ancestor "$LATEST_TIP" "$MERGE_SHA" && echo "FF-OK"
# PUSH — explicit SHA refspec. NEVER a bare `git push` (upstream is unset).
git push origin "$MERGE_SHA:refs/heads/latest"
git ls-remote origin refs/heads/latest     # expect $MERGE_SHA
```

**Why the explicit-SHA refspec:** `git push origin <sha>:refs/heads/latest` is a fast-forward by construction (the merge has `origin/latest` as a parent), independent of any local-branch staleness and independent of the branch's upstream configuration. A bare `git push` from this worktree is exactly what the upstream-unset anomaly (0679c552) is guarding against.

**COORDINATION — surface, never force (ADR-9 / AC-R12).** A **parallel commission** is bringing the TEXT lane to the same pick/attempt/chain semantics (ADR-9:697-698). This release **MUST NOT** change text-lane behaviour; every shared-file change must be **lane-gated** and provably text-neutral. The **collision-hotspot files** (`decisions.md:701-715`, phase1 §A17) are:

| Hotspot file | Why it collides |
|---|---|
| `pkg/store/database/store_group_rewrite.go` | the four-phase sweep, `memberDemotionExpiry` ordering, the request-time predicate, `ExcludeGroupMember`, `ClearGroupMemberCooldown` |
| `pkg/store/database/store.go` | `ResolveInternalConfigWithAffinity*`, group engine wiring, credential resolution |
| `pkg/grouplb/engine.go`, `selector.go`, `binding.go` | selection, cooldowns, binding semantics |
| `pkg/modelscache/models.go` | decorator forwards — **both** commissions may add methods (the trap site) |
| `pkg/skipcache/skipcache.go` | `Mark`/`Marked`/`SoonestExpiry` + the doctrine comment that now changes |
| `pkg/proxy/handler.go`, `handler_groups.go`, `handler_anthropic.go`, `handler_helpers.go`, `race_coordinator.go` | text-lane pick/attempt/chain logic (this release touches only the three lane-gated guards) |
| `pkg/credentiallb/engine.go` | binding + cooldown seeding (`ExcludeAndReselect*`) |
| `pkg/models/group.go`, `pkg/models/config.go` | group rules, kind rules, decision rule set (D5) |
| `pkg/providers/interface.go` | classification vocabulary; our `pkg/credclass` extraction makes it delegate |
| `pkg/ui/server.go` | group/kind validation mirrors (helper ≡ POST ≡ PUT parity) |
| `cmd/main.go` | wiring for both lanes |

**The hotspot list is EXTENDED with the v194 files** (ADR-16) — these did not exist in the v193-era list and are exactly where the parallel commission's semantics live:

| v194 file | Why it is now a hotspot |
|---|---|
| `pkg/modelcache/` (**new package** — `modelcache.go`, `policy.go`, `reset.go`) | the **MODEL layer**; `pkg/skipcache`'s package doc (`:4-15`) declares the LAYER CONTRACT (GROUP = `pkg/grouplb` policy / MODEL = `pkg/modelcache` / UPSTREAM = `pkg/skipcache`; *"layers never read each other's state"*) |
| `pkg/proxy/race_executor.go` | the race executor the v194 per-layer suppression changed |
| `pkg/proxy/cache_layer_separation_test.go` | the **AST pin** (`TestCacheLayer_Structural_PackageLevelNoCacheImports`, `:752-786`) — it constrains ANY decision-lane edit that touches the cache layers, and it is the structural enforcement of "a credential-layer mark cannot change the pick" |
| `pkg/store/database/store_group_rewrite.go` (re-listed) | v194 rewrote the sweep: policy-headed, cache-blind; the AST pin lives here |
| `pkg/proxy/handler_helpers.go` (re-listed) | v194 renamed the win-clear helper to **`clearWinnerLayerState`** (`:873`) and it now clears all three layers |

**Ship-step rule (binding).** When the merge in Step 1 (or the promotion merge in Step 2) hits a conflict in a hotspot file:

1. **SURFACE it — never force it.** Never resolve a hotspot conflict with `git checkout --theirs/--ours`, `-X ours`, or a "take one side wholesale" resolution. ADR-9's AC-R12: *"A conflict in a hotspot file is resolved by **re-deriving from the merged semantics**, never by taking one side wholesale."*
2. **Re-derive from the merged semantics** — the conflict is resolved by re-deriving the shared behaviour under **both** commissions' rules, with the decision-lane change **lane-gated** (it fires only for a decision-lane group — a shape that cannot exist today).
3. **Re-run the FULL gate on the merge — REGARDLESS of conflict.** ADR-16: the parallel commission's v194 semantics landed on the text lane, so **every** merge (clean **or** conflicted) against the new base must be re-gated in full: Pack A + Pack B + the anti-vacuous list + the FE gates + the boot smoke. A conflict-free merge is **not** a reason to skip the re-gate. A hotspot resolution that changes text-lane behaviour **relative to `2b987bf`** is a **STOP**, not a merge detail.
4. **Report every hotspot conflict** in the RESULTS report — file, hunks, which side's semantics each side took, and the re-derivation. A hotspot conflict resolved silently is a gate failure.

**The text-lane proof set that must stay green across the merge** (AC-R12): `pkg/proxy/handler_groups_test.go:400`, `pkg/proxy/handler_groups_equivalence_test.go`, `pkg/proxy/group_fallback_hidden_member_test.go`, `pkg/proxy/group_fallback_part2_test.go`, `pkg/proxy/handler_groups_shadow_member_test.go`, plus the whole existing `pkg/proxy` / `pkg/store/database` / `pkg/models` / `pkg/ui` suites.

### Step 2 — promote `latest` → `master` **in the SAME sibling worktree**

```bash
cd "$WT"
export PRIOR_MASTER=$(git rev-parse origin/master)   # plan-time value: b11d7c8
export NEW_LATEST=$(git rev-parse origin/latest)
git checkout --detach origin/master
git merge --no-ff origin/latest -m "Merge branch 'latest' (into master)"
export PROMO_SHA=$(git rev-parse HEAD)
# GATE 2a — EXACT parents: MUST be "$PRIOR_MASTER $NEW_LATEST" (parent1 = prior
#           master tip, parent2 = the latest tip). Nothing else.
git --no-pager log --format='%P' -1 "$PROMO_SHA"
# GATE 2b — TREE IDENTITY: tree(promo) == tree(latest tip)
test "$(git rev-parse "$PROMO_SHA^{tree}")" = "$(git rev-parse "$NEW_LATEST^{tree}")" \
  && echo "TREE-IDENTITY-OK"
# PUSH — explicit SHA refspec again
git push origin "$PROMO_SHA:refs/heads/master"
git ls-remote origin refs/heads/master     # expect $PROMO_SHA
```

**Never merge in the main checkout** (`/home/nea/Code/opensource-projects/llm-supervisor-proxy`): it holds `master` checked out and carries ~72 uncommitted `.agents/` edits; a merge there is blocked by the dirty tree *and* by the checkout itself. The sibling-worktree recipe is mandatory.

**House invariants to re-assert after the promotion:**
`origin/master^{tree}` == `origin/latest^{tree}`; `merge-base(origin/master, origin/latest)` == `origin/latest` tip; master adds only a zero-content `--no-ff` promotion commit.

### Step 3 — CI (image build) + **EARLY-STALL GUARD**

A master push builds the image (CI publishes only on a master push). **Version = CI run number** → expect **`v194`** (the next run after #193). Read the real number from the actual run; do not hard-code it.

| Check | When | Evidence to capture | Abort if |
|-------|------|--------------------|----------|
| 3.1 **Run EXISTS** | **within 5 min** of the master push | run number, run URL, status = `in_progress` | no run appeared within 5 min ⇒ the push emitted **no workflow event** (see the zero-delta note below) |
| 3.2 **Build PROGRESSING** | **+15 min** | the build job is past the compile step (or the image publish step has started) | still `queued`/`waiting` at +15 min ⇒ STALL |
| 3.3 **Build COMPLETE** | **+30 min** (normal band is 9–13 min) | job conclusion = `success`; image tags published (sha7 / `latest` / `master`) | not complete at +30 min ⇒ **STALL — report it, do NOT wait silently** |

**Why the guard exists:** CI run **#190 hung for 6 hours** and was cancelled; the re-run was **admin-only**. A silent wait is how that became a 6-hour stall. The operator reports a stall as an anomaly (with the run URL + the observed status timeline) rather than blocking on it.

**Zero-delta contingency + CI recovery ladder (B1, approver escalation):** the workflow's `on.push.paths` filter emits **no event at all** for a zero-delta push, and **every publish step in `docker-ci.yml` is additionally gated on `github.event_name == 'push'` — `workflow_dispatch` is COMPILE-ONLY and can never publish.** (This supersedes this runbook's earlier W4 wording, which wrongly treated a manual dispatch as a publish path.) Recovery ladder, in order:

1. **First line — RE-RUN the original push-triggered run** (Actions UI → the stalled/failed run → *Re-run*). This is **admin-only** (repo admin; run #190's re-run was admin-only too). **Documented as the FAST PATH with explicit user handoff:** the operator reports "CI run #<N> needs an admin re-run — <run URL>" to the user/leader immediately, then waits; do not proceed past it while the re-run is pending.
2. **Autonomous fallback — NON-ZERO-DELTA CONTENT RETRIGGER (house-proven pattern, wi-7769a812):** a **one-line CONTENT commit** (a non-empty diff — **NOT** `--allow-empty`) touching a path **inside `docker-ci.yml`'s `on.push.paths` filter**, then `--no-ff` merge into `latest` → `--no-ff` promotion to `master` → push. **The filter is `'**'` minus `!.git/**`** (`docker-ci.yml:13-16`) ⇒ **any** tracked file outside `.git/` satisfies it; the house-proven choice is a one-line append to a **tracked** path, e.g. the on-branch corpus file `.agents/shared/planning/decision-groups/phase3-plan.md` (the `.agents/` commit convention tracks these), or any other benign tracked file. **WHY the empty commit CANNOT work:** a true empty commit has an **empty changed-file set** ⇒ **`on.push.paths` fires NOTHING** ⇒ **no event, no build, no publish** — stated by the workflow itself (`docker-ci.yml:4-5`) and by this runbook above; a `--allow-empty` retrigger is therefore **useless** (it is the exact trap this rung previously encoded). The non-zero-delta commit ⇒ a **push event fires** ⇒ the publish steps run (they are gated on `github.event_name == 'push'`) ⇒ a **per-SHA tag** exists for step 5's digest verify. It is a **documented CI-stall EMERGENCY EXCEPTION to the promotion-only master invariant** — use it only when the fast path is unavailable (no admin) and a stall is blocking ship. **The retrigger commit is INTENTIONAL and must NOT be "cleaned up" or reverted afterward** — reverting it would emit another push event and trigger another build; record it in the ship evidence like any other commit.
3. Both paths end in a push event ⇒ publish ⇒ per-SHA tag; only then does step 5's digest verify apply. A compile-only dispatch run is **never** sufficient evidence of a publish.

| `W4` | CI recovery ladder (B1): **rung 1** = re-run the ORIGINAL push-triggered run (admin-only fast path, user handoff); **rung 2** = **non-zero-delta content retrigger** (see the rung-2 text above) — a `workflow_dispatch` run is **COMPILE-ONLY and NEVER publishes** (every publish step is gated on `github.event_name == 'push'`) |

**CI green ≠ gate green.** `docker-ci.yml:104` swallows Go-test failures and the workflow gates neither Go tests nor the FE. The § Execution order local gate remains the authority.

### Step 4 (bridge) — GitOps re-pin (separate authorisation)

The chart lives **in-repo** (`k8s/`); the GitOps repo (`/home/nea/Code/data-center-scripts`) holds the pins. They move **together**:

- backup: `infranstructure/k8s/llmproxy/hetzner-daoduc/values-pinned.yaml` → `image.tag: "<sha7>"`
- primary: `infranstructure/k8s/llmproxy/rancher-local-ensem-dev/deployment.yaml` → `image: docker.io/disillusioners/llm-supervisor-proxy:<sha7>` + digest/push-time/rollback comment

Branch `feature/llmproxy-pin-<sha7>` → `--no-ff` `latest` → push; `main` untouched.

> **ESCALATE BEFORE PUSHING:** `data-center-scripts` `latest` is **8 commits ahead of `origin/latest`** at plan time (`b0e98f4`; the v192/v193 pin commits are unpushed). A push would carry that backlog with it. The operator must (a) confirm the leader wants the backlog pushed, or (b) rebase the pin branch onto `origin/latest` first, and (c) report which path was taken. The pin push needs its **own** authorisation — it is a separate repo and a separate push from the proxy repo.

---

## Deploy runbook (steps 5–7)

### Step 5 — image verification (digest + feature marker), BEFORE any deploy

Repo-external harness: `~/llmproxy-verify/verify_image.sh` (registry-API only; this host has no container runtime). Use the **per-SHA tag** — it is immutable, hence safe to cache. **Never** use the moving tags `latest`/`master`: they go STALE after any new push, and the cache under `~/llmproxy-verify/cache/<tag>/` would then serve the wrong artifact.

```bash
export SHA7=<sha7 of the promoted master>
bash ~/llmproxy-verify/verify_image.sh --tag "$SHA7" --json | tee "$LOGDIR/verify_image.json"
```

Record from the JSON/human output: index digest, amd64 manifest digest, config digest, app-layer digest + **layer digest verified = YES**, created timestamp, `org.opencontainers.image.revision` (must equal the promoted master sha), `org.opencontainers.image.version` (must be `v<run_number>`), entrypoint, exposed ports, **env KEYS only** (never values), go buildinfo, binary bytes + binary sha256.

#### 5.1 The feature marker — **PRIMARY: `decision_all_members_failed`** *(AMENDED by Amendment 2 §3 — the negative control is now a PINNED requirement, ADR-13:836-841)*

```bash
export BIN=~/llmproxy-verify/cache/$SHA7/llm-supervisor-proxy
strings -a "$BIN" | grep -c -- 'decision_all_members_failed'    # expect >= 1
strings -a "$BIN" | grep -c -- 'GroupLane'                       # secondary, corroborating only
```

**Why this marker (grounded):**

- `decision_all_members_failed` is a **string literal** bound by D-5 / contract #13 ("Exhaustion ⇒ 502 + code `decision_all_members_failed`"). It is emitted through `openAIErrorWithCode` (`pkg/decision/handler.go:709-714`), so the literal is in the binary's read-only data and survives `-s -w` stripping. It cannot drift with implementation choices, because the *code* is the contract.
- **Negative control measured on the live v193 AND v194 artifacts** (`~/llmproxy-verify/cache/{b11d7c8,fd1649c}/llm-supervisor-proxy`, 2026-11-16): `decision_all_members_failed` → **0 hits at BOTH**; `GroupLane` → **0 hits at BOTH**. A post-ship count ≥ 1 is therefore a real delta, not a pre-existing coincidence.
- **No false-positive family:** the pre-existing decision codes are `decision_misroute`, `decision_upstream_build_failed`, `decision_timeout`, `decision_models` — `decision_all_members_failed` shares no prefix-substring with them, and the string is far too long to occur inside the embedded SPA assets.
- **Control markers must stay > 0** (the harness's own liveness proof): `quota_exceeded` (2 hits today), `ExcludeAndReselectForClass` (2 hits today), `openrouter` (5 hits today). If any control drops to 0, the harness/extraction is broken — re-run before trusting any marker result.
- **The NEGATIVE CONTROL is now a PINNED requirement (ADR-13:836-841), not just a measurement.** The marker literal is **a NEW Go string this feature introduces**, so:
  - **positive:** `decision_all_members_failed` MUST be **present in the new build** (`grep -c` ≥ 1) — the blocking criterion;
  - **negative:** the same string MUST be **ABSENT from BOTH the live v193 binary AND the live v194 binary** (ADR-14's control, now measured at both tiers), verifiable against the `~/llmproxy-verify/verify_image.sh` cache for the **per-SHA tags** (`b11d7c8` = v193, `fd1649c` = v194 — per-SHA tags are immutable, so the cache entries are safe; moving tags go stale):
    ```bash
    for T in b11d7c8 fd1649c; do
      printf 'v193/v194 %s: ' "$T"
      strings -a ~/llmproxy-verify/cache/$T/llm-supervisor-proxy \
        | grep -c -- 'decision_all_members_failed'    # MUST be 0 at BOTH
    done
    ```
    **Measured 0 at BOTH on 2026-11-16** (v193 `b11d7c8` and v194 `fd1649c`), so a post-ship count ≥ 1 is a real delta at either tier;
  - **no false-hit classes:** `grep -ci sticky` stays **forbidden** (Tailwind / static-UI false hits) — `grep -c` with the exact literal only.
- **Cross-document agreement — RE-VERIFIED after the phase-1 rewrite.** phase 1 §Stage 6 (re-read from the **1029-line** plan, post-`## Planner adjudications`) is **UNCHANGED**: the marker literal is still **`decision_all_members_failed`** ("the distinctive, greppable marker … unique in the tree (no false hits) … carries **no Tailwind-class false-positive risk**"), the recipe is still `strings <binary> | grep -c 'decision_all_members_failed'` ⇒ **MUST be ≥ 1**, and the prohibition on `strings | grep -ci sticky` ("Tailwind false hits") still stands. **The two documents agree.** Phase 3 adds the independently **measured** negative controls above (0 hits for `decision_all_members_failed` and for `GroupLane` in the v193 binary; `ExcludeAndReselectForClass` = 2, `quota_exceeded` = 2, `openrouter` = 5), which phase 1 did not compute.

**Secondary marker `GroupLane` (D-8's method name) — corroborating, NOT blocking.** Evidence: in the same stripped v193 binary, the exported method name `ExcludeAndReselectForClass` yields **2 hits**, so exported method names **do** survive `-s -w` in this image (the v193 ship used exactly that kind of marker — POK `agenticfix-landed-ci193`: "marker ClearGroupMemberCooldown in binary"). Residual uncertainty: the `GroupLane` method is reached through a **structural interface assertion** on the decorator's inner value (`pkg/modelscache/models.go:1079-1085` pattern), so the type descriptor must be emitted for the runtime type assertion — expected, but not certain. If phase 1 names the method differently, the marker silently misses. **Therefore: `decision_all_members_failed` ≥ 1 is the blocking criterion; `GroupLane` is a bonus.**

**FORBIDDEN:** `strings "$BIN" | grep -ci sticky` — Tailwind-derived CSS tokens in the embedded SPA assets produce false hits. The house-approved sticky check is the **exact literal** `sticky_last_success_enabled` (the harness already computes it).

### Step 6 — deploy order: **BACKUP (daoduc) FIRST, then PRIMARY**

House order (POK `pipeline-03`): backup rolls first (Helm), gate on health, then primary updates.

#### 6.1 BACKUP — `llm.daoduc.org` (Helm, ns `llmproxy`, context `hetzner`)

```bash
export HELM=/home/nea/bin/helm
export KUBECONFIG=/home/nea/Code/data-center-scripts/hetzner/connection/kubeconfig.yaml
$HELM --kube-context hetzner -n llmproxy list          # release llm-supervisor-proxy, revision 36 (plan-time)
cd "$WT" && git checkout --detach "$PROMO_SHA"          # chart at the shipped code
$HELM --kube-context hetzner -n llmproxy upgrade llm-supervisor-proxy ./k8s \
  -f /home/nea/Code/data-center-scripts/infranstructure/k8s/llmproxy/hetzner-daoduc/values-pinned.yaml
$HELM --kube-context hetzner -n llmproxy history llm-supervisor-proxy   # new revision 37
```

**Quick-check health (additive feature → NO soak):** rollout complete; `restartCount == 0`; `/healthz` 200; `/fe/api/version` returns the new `v<run_number>`; `/v1/models` 200; **anonymous `POST /v1/messages` ⇒ 401** (the fence-16 parity probe — same probe shape as §6.2 PRIMARY, pod-direct port-forward, no credentials sent). Use the pod-direct port-forward (`kubectl -n llmproxy port-forward pod/<pod> 14330:4321` → `http://127.0.0.1:14330/...`) which bypasses the ingress/OAuth door; container port is **4321**, not 8080.

> **Operator note — VERIFY LAYER SEPARATION, NOT INSTANCE SEPARATION (C2-2, ADR-18).** `pkg/skipcache/skipcache.go:4-17` declares itself "a **process-wide** registry … read by credential selection + attempt suppression ONLY"; `cmd/main.go:242`/`:249` construct one instance each and `:255-256` hand **both** to the proxy config — **one instance, both lanes**; upstream health is per-credential, not per-lane. A decision-lane mark can never influence text-lane ordering **structurally** (the pick is policy-only and the mark's reader is per-credential), not because two instances never share state. Enforced structurally by `TestCacheLayer_Structural_PackageLevelNoCacheImports` (`pkg/proxy/cache_layer_separation_test.go:752-786`). **The Wave-A instance-separation test is WITHDRAWN** as pinning a wrong invariant, replaced by `TestDecision_Group_CacheLayerSeparation_SharedUpstreamInstanceOneAnswer`. **Implications to check at deploy:** (a) **memory** — the LAYER separation adds no second map; the decision lane's UPSTREAM layer reuses the same instance, so RSS is unchanged in shape (compare against the v193 baseline as a sanity check); (b) **config** — the mark TTL is still the **config-owned** `upstream_skip_cache_ttl` read through the **injected seam** (`skipCacheTTL func() time.Duration` from `cmd/main.go`, E-3) — there is **no new config key**, so nothing new appears in the config surface; (c) **layer separation** — a decision-lane mark must never appear in the GROUP layer's pick input, and a credential-layer mark must never alter the pick (AC-R11 / AC-R12); the enforcement is structural, not discipline.

> **daoduc has NO decision models configured — deploying there is CODE-ONLY and that is EXPECTED.** An empty decision lane on daoduc is **not** a failed deploy and must not be read as one. Record it in the report as "code-only rollout; decision lane empty by design (no decision models configured on this host)".

#### 6.2 PRIMARY — `llm.ensem.dev` (PLAIN `kubectl apply`, **no Helm**)

```bash
export KUBECONFIG=/home/nea/.kube/config
export KUBECTL=/home/nea/bin/kubectl
$KUBECTL --context default -n llmproxy get deploy llm-supervisor-proxy -o jsonpath='{.spec.template.spec.containers[0].image}'
#   → confirms the currently-live image BEFORE the apply (fresh-verify: the
#     live tag is whatever v194+ is deployed — READ IT, do not assume)
$KUBECTL --context default -n llmproxy apply -f \
  /home/nea/Code/data-center-scripts/infranstructure/k8s/llmproxy/rancher-local-ensem-dev/deployment.yaml
$KUBECTL --context default -n llmproxy rollout status deploy/llm-supervisor-proxy --timeout=180s
$KUBECTL --context default -n llmproxy get pods -n llmproxy -o wide
```

Context is **`default`** — **NOT** `rancher-local-ensem-dev` (the RKE2 cluster name). Binaries are `/home/nea/bin/kubectl` and `/home/nea/bin/helm` (not on `PATH` on this host).

> **Confirm the apply source before applying.** The plan-time reading says the primary's pinned manifest is the GitOps file `infranstructure/k8s/llmproxy/rancher-local-ensem-dev/deployment.yaml` (which carries `image: …:<sha7>` plus the digest/push-time/rollback comment, verified 2026-11-16). Confirm with `kubectl get deploy -o yaml` that the live spec matches the file before applying, so an apply does not silently revert fields the operator did not intend. See `## Not verified`.

**Primary health check:** `restartCount == 0`; generation bumped (plan-time gen 33 → 34); `/healthz` 200; `/fe/api/version` = `v<run_number>`; `/v1/models` 200; **and the text lane still answers** (one authenticated `/v1/chat/completions` request, HTTP 200) — the widening must not have disturbed the text lane. **And fence 16 — the anonymous `/v1/messages` hole (closed in v192) is AFFIRMATIVELY still closed: an UNAUTHENTICATED `POST /v1/messages` through the pod-direct port-forward MUST return 401.** This is a **proactive gate assertion** (the deploy fails the health check on anything other than 401), not a rollback trigger — the reactive trigger #4 and R11 remain as backstops only. Probe shape (no credentials sent): `curl -s -o /dev/null -w '%{http_code}' -X POST http://127.0.0.1:14330/v1/messages -H 'content-type: application/json' -d '{"model":"probe","max_tokens":1,"messages":[{"role":"user","content":"x"}]}'` → expect `401`. Primary stdout **rotates ~7 min under load**, so establish the observation window before grepping.

### Step 7 — E2E on PRIMARY (real decision group, real failover, non-destructive)

**Access:** admin access is the **house kubeconfig + pod-direct port-forward** to `/fe/api` (`kubectl -n llmproxy port-forward pod/<pod> 14330:4321`). The `/fe/api/*` surface is **unauthenticated inside the binary** (OAuth is at the ingress) ⇒ pod-direct calls need **no credential**. The ensemble secret store holds only `9router-api-key`; **there is no admin secret to use — do not go looking for one.**

**Client token for `/v1/decisions`:** the `/v1/*` routes use the proxy's **own Bearer token store**. Obtain a valid client token from the leader (or mint one via the admin token API — if minted, record it and revoke it after the E2E). Never print token values.

**The user's live setup:** two live decision models — one provider `openai`, one provider `openrouter` (per `research-decision.md` §4.4). Their ids/internal names live in the running DB and are **not** known to this plan — read them from the live API.

#### 7.1 SAFE failover design — throwaway third member, zero mutation of live credentials

**The hazard:** proving failover by breaking a live member would mutate the user's live credentials (exclusion/cooldown rows, credential state) — unacceptable.

**The shape (chosen):** add a **THIRD, throwaway decision model** as the group's **FIRST** member, pointed at an **unresolvable / refused** base URL. It fails **pre-commit** on member #1, so the request must fall through to the real members — proving exhaustive fallback **without touching the live credentials at all**. Then remove the throwaway member and leave the group with exactly the 2 live members.

| Step | Action | Notes |
|------|--------|-------|
| E1 | `GET /fe/api/models?kind=decision` (pod-direct) | list the 2 live decision models; record their ids + internal model names |
| E2 | `GET /fe/api/credentials` → **BEFORE snapshot** | record credential **ids**, names, providers, and the model they are attached to. **Never** the secret values. This snapshot is the untouched-ness proof. |
| E3 | Create the **throwaway** decision model | `kind=decision`, provider `openai` (inside the decision vocabulary `{openai, typesafe, openrouter}`), base URL `http://decision-group-failover-probe.invalid` (RFC 2606 reserved TLD — never resolves). Alternative if the admin API rejects that URL: `http://127.0.0.1:1` (loopback refused). Its credential is a **NEW** row (a throwaway placeholder), **not** any live credential. |
| E4 | Create the decision group — **deterministic-failover staging (approver iteration-002 FIX 5)** | The throwaway is the **SOLE default-pool member**; the 2 live members are `backup_only: true`. Rationale: with 3 default-pool members the head is a fresh weighted pick and the throwaway is NOT deterministically first; as the sole default, **the policy head IS the throwaway deterministically** (Phase-1 default pool offered before backup — `store_group_rewrite.go` Phase 1 `:101-112`), and after its genuine pre-commit failure the loop **deterministically advances to a live backup member**. Only OUR created group's pool config carries this staging; no model or credential is touched. |
| **E4b** | **PRE-CHECK (W5) — the AllowedModels 403 false-negative guard, BEFORE E5** | A client token whose `AllowedModels` **excludes the newly created group** yields **403**, which the E2E must **not** misread as failover evidence. Before firing the real request, verify the token's `AllowedModels` **covers the group id** (or that the allow-list is empty = all models allowed). If it does not, **fix the token's allow-list first** (or mint one that covers the group). **A 403 on E5 is a harness defect, not failover evidence** — the E2E must stop and re-prepare rather than record a "failover" |
| E5 | Fire a REAL `/v1/decisions` request | `POST /v1/decisions` with `{"model":"<group-id>","input":"ping","questions":[...]}` (shape per `pkg/decision/types.go:130-135`) and the client Bearer token. Target the **pod-direct port-forward** (or the public edge `https://llm.ensem.dev/v1/decisions`, which the split ingress routes `/v1` → Service 4321, unauthenticated by OAuth). |
| E6 | **Capture the evidence** | (a) HTTP status + response body; (b) the **decision event** for that request — with the **re-derived exhaustion counters** (phase1 §A4 / ADR-5+ADR-6): `attempted` (genuine attempts), `skipped` (suppressed attempts), `members_attempted` (distinct members attempted), plus `outcome`; (c) the **pod stdout** lines for the member attempts (the attempt order and the failed member #1); (d) the **metering/usage row** — the **winner's** member id must be the metered row (contract #14), and the client-facing group id must appear in the event. **"Genuinely failed" is per the failure-class policy (ADR-13)** — a transport-error probe is a genuine failure (it is neither limit-class nor advance-blocking), so it counts in `attempted` and in the failure count. |
| E7 | **Assert the expected observations** | status 200; the response came from a **live backup member** (the winner is one of the two live models, never the throwaway); the event shows the throwaway — the **deterministic policy head** (sole default-pool member) — genuinely attempted and failed **before** any commit; **`attempted` counts genuine attempts only and `skipped` = 0** (the throwaway's transport failure is not limit-class ⇒ no mark ⇒ no suppression; and per ADR-13 a transport error is neither 5xx nor advance-blocking ⇒ it **rotates within the member** — with one credential ref, that rotation is a single attempt); **exactly one** client-facing write (contract #6 — the writer is untouched until the winner); the metered model id is the **winner's member id**, not the group id and not the throwaway. **E4's staging makes the head deterministic; a live member as head would indicate the staging regressed** (or the pool config was not applied) — stop and re-verify rather than record "failover". |
| E8 | **Cleanup — restore the end state** | remove the throwaway member from the group; **restore the 2 live members to the default pool** (`backup_only: false` — undoing E4's staging); delete the throwaway model row and its throwaway credential row. **End state: the group exists with EXACTLY the 2 live members, both in the default pool.** The group itself is **left in place** (it is the commission's working configuration). |
| **E8b** | **E2E EXIT CRITERION (D-B / W6) — KEEP the group** | **The created decision group is KEEPED as the end state** (ADR-15 D-B: "the group left in place is the user's desired end state"). The E2E stays **non-destructive**: the user's 2 live decision models are **never mutated or broken**. The only rows removed are the **throwaway** ones the E2E created. **Never delete the group.** |
| E9 | **Prove the live credentials were untouched** | `GET /fe/api/credentials` → **AFTER snapshot**; diff the credential **id set** + per-credential metadata (name, provider, attached model) against E2. Expect: the 2 live credential ids unchanged, unchanged attachments, no new/changed rows other than the throwaway that was created and deleted in E8. Compare **ids and metadata only — never values.** |
| E10 | Final state assertion | `GET /fe/api/models?kind=group` shows the group with exactly 2 members; one final successful `/v1/decisions` call through the **cleaned** group returns 200. |

**Why this shape is safe:** it creates and deletes **new** rows only; it never edits, disables, excludes, or re-points a live credential or a live model; the ONLY live-adjacent mutation is **our own created group's member pool config** (the `backup_only` staging in E4, fully undone in E8 — weights/positions of live members are never altered); and the final state is the group with the user's 2 live members in the default pool.

**Why a transport failure is the right probe:** a DNS/connection failure is **not** a limit-class failure, so it must **not** mark the skip cache (contract #11 — marks fire only on limit-class failures). The probe therefore also *proves* the mark discipline: after E5, the live members must carry **no** skip mark. Check that explicitly: the live members must still be picked first on the E10 follow-up call.

**If the failover is NOT observed:** do **not** delete the group and do **not** roll back the deploy (the deploy is additive and the text lane is unaffected). Clean up per E8, leave the group with the 2 live members, and report E2E as **NOT VERIFIED** with the captured evidence.

**The all-marked case is LOCAL-HARNESS-ONLY (W5).** The E2E probe uses a `.invalid` host, whose failure is a **transport error**, **not** a limit-class failure — it therefore produces **no mark**, so the E2E can **never** demonstrate the all-marked ⇒ policy-head-honest-attempt rule. That rule is proven by the **deterministic Go unit test** `TestDecision_Group_AllMarked_PolicyHeadHonestAttempt` (and its member-level twin `TestDecision_Group_AllCredentialsCooling_PolicyFirstCredentialHonestAttempt`, ruled D-C) — **the E2E must not attempt to simulate a mark** and must not report the all-marked case as observed.

#### 7.2 The rollback LADDER (CG-5 + ADR-17) and the rollback decision point

> **FRESH-VERIFY RULE — every number below is re-read at ship time; none is copied.** The ladder encodes **tiers**, not frozen values. Before any deploy, re-read: the **live image tag** on primary, the **Helm release revision** on daoduc, the **primary deployment generation**, the **GitOps pin values**, the **CI run number**, and the **v194/v193 image tags + digests** — each from the live system / giter's fresh state, never from this document. A stale number here is a **hazard**, which is why it is written as a rule and not as a constant.
**Scope extension (C2-4, ADR-18).** The fresh-verify rule's scope is **every plan-time deploy/dcs number in this document, not only those in the ladder above** — the prior scope ("every number below") was too narrow and left stale operative numbers in the deploy runbook. The numbers below are **all plan-time values and ALL stale by construction**; each must be re-read from the live system at ship time:

| Plan-time number (this document) | Where it lives | Stale? (re-read) |
|---|---|---|
| daoduc Helm release **rev 36** (v193) | §7.2 ladder (tier-2) + §Risks R11 | stale — v194 moved it (tier-1 is now rev 37) |
| daoduc Helm release **rev 37** (v194, tier-1) | §7.2 ladder (tier-1) | stale by construction — **read at ship** |
| primary deployment **gen 33** (v193) | §7.2 ladder (tier-2) | stale — v194 bumped it (now gen 34) |
| primary deployment **gen 34** (v194) | §6.2 health-check note | stale by construction — **read at ship** |
| "expected CI ≥ **195**" | §7.2 ladder (CI run-number expectation) | stale by construction — **read at ship** |
| `:fd1649c` / `sha256:fd1649c…` (v194) | §7.2 ladder (tier-1) | stale by construction — **read at ship** |
| `:261622cf` / tag `b11d7c8` (v193) | §7.2 ladder (tier-2) | stale by construction — **read at ship** |
| **"8 commits ahead of `origin/latest`"** (dcs backlog, `b0e98f4`) | §4 step 4 (ESCALATE-BEFORE-PUSHING) + R14 | **DEAD** — dcs synced @ `7f89eac`; the figure targets a non-existent backlog. **R14 fires on MEASURED drift only** (measure at ship, compare, act on delta). |

**The dcs escalation rule (rewritten).** `data-center-scripts` `latest` is **no longer 8 commits ahead of `origin/latest`** (it is in sync at `7f89eac`). The "ESCALATE BEFORE PUSHING" instruction and the R14 trigger are therefore **re-scoped**: they fire only on **measured drift** — the operator measures `origin/latest..latest` at ship time, compares, and acts on the delta (push the backlog, or rebase onto `origin/latest` first, and report which path was taken). **R14 is no longer a static trigger; it is a measurement-and-compare step.**


| Tier | Target | Rollback target | Command | When |
|---|---|---|---|---|
| **tier-1** | **`<LIVE_AT_INCIDENT>`** — measured live at incident time (see the measurement block below) | the last known-good release | daoduc: `$HELM --kube-context hetzner -n llmproxy history llm-supervisor-proxy` → identify the revision BEFORE the current one → `$HELM … rollback llm-supervisor-proxy <that-rev>` · primary: `$KUBECTL --context default -n llmproxy get deploy … -o jsonpath='{.spec.template.spec.containers[0].image}'` → `$KUBECTL … set image deploy/llm-supervisor-proxy llm-supervisor-proxy=docker.io/disillusioners/llm-supervisor-proxy:<prior-tag>` | the release **degrades the live service** — tier-1 is the first move and restores the last known-good release |
| **tier-2** | **`<PRIOR_TO_TIER1>`** — the immediately-preceding revision relative to tier-1 (measured the same way) | one release further back | same command shape with the next-preceding helm revision / image tag | tier-1 is **insufficient** — the symptom survives, so back out one further tier |

**ILLUSTRATIVE TEMPLATE — the placeholders above are resolved at incident time, NEVER from this document.** The measurement block (run at incident time): (a) daoduc — `$HELM --kube-context hetzner -n llmproxy history llm-supervisor-proxy` (revisions are numbered and self-describing); (b) primary — the live image tag from `kubectl get deploy`; (c) dcs — the pinned manifest's `image:` tag. **R11 is the operative rule** (ADR-18 C2-3): tier-1 = fresh-verified live-at-incident; tier-2 = the immediately-preceding revision; **the incident path NEVER strips a newer release than the incident requires** — at plan time live was v195-class (`8d434ba` per the approver's measurement), so a frozen "rollback to v194" executed verbatim would strip TWO releases.

> **Rollback does NOT revert the dcs GitOps pin.** A host rollback (helm rollback / kubectl set image) changes the LIVE workload only; the dcs manifest pin is a separate write that pairs with the **measured-drift rule (R14)** — if the pin has drifted from the rolled-back live state, MEASURE, compare, and act on the delta deliberately. Never push dcs as part of an incident rollback.

**Historical example ONLY (STALE BY CONSTRUCTION — never execute from this row):** at plan time, live ≈ v194 (`:fd1649c`, daoduc rev 37) and the prior tier was v193 (`:261622cf`, tag `b11d7c8`, daoduc rev 36). Those numbers illustrate the command shapes above; they are NOT the rollback targets — measure, then roll back.

> **Do NOT roll back to the v192 refs** (`:c3e3fb4` / helm rev 35 / `:43fb7e5`). v192 carried the **anonymous `/v1/messages` auth hole**; rev34/:43fb7e5 is a last-resort only (it regresses/auth-holes).

> **Digest vs tag.** The chart supports only `image.registry` / `image.repository` / `image.tag` — **there is NO `image.digest` field** in `k8s/templates/deployment.yaml`. Therefore the rollback reference is the **tag** (`:fd1649c` for tier-1, `:b11d7c8` for tier-2); the digests above are the *verification* of those tags, not pinnable fields. Use the tag.

> **Expected CI run number for this release: ≥ 195** (#194 was taken by `fd1649c`) — **verify from the actual run**, never assume (the plan-time expectation is written here only so the operator notices a collision).

**Rollback decision point and triggers.** Roll back **only** if the deploy itself degrades the live service. Triggers:

1. `/healthz` or `/v1/models` not 200 after rollout;
2. `restartCount > 0` (crash loop) on the new pod;
3. the **text lane** regresses — an authenticated `/v1/chat/completions` that worked before the deploy now fails, or `/v1/models` loses enabled chat models;
4. the **anonymous `/v1/messages` hole re-opens** — a request to `/v1/messages` without credentials returns 200 instead of 401 (the v193 property that MUST survive).

**NOT a rollback trigger:** the E2E failover not being observed, the decision lane being empty on daoduc (expected), or a decision-group admin-API cosmetic issue. Those are defects to report, not reasons to revert a healthy, additive release.

**Rollback sequence:** rollback the **primary first** (it is the live user-facing host), then the backup — i.e. reverse of the deploy order — then re-verify health on both, then report.

---

## Evidence to retain

**Location:** `.agents/tester/RESULTS/` (house convention — verified: the directory holds `<date>-<kind>-<commission>.md` files, e.g. `2026-11-14-release-gate-wi-7769a812.md`, `2026-11-14-release-gate-anthropic-lane-auth.md`, `2026-11-09-9router-imggen-provider-verification-gate.md`, `2026-10-27-openrouter-provider-registration-gate.md`, `2026-11-06-sticky-last-success-ordering-gate.md`). Two files for this commission:

- `.agents/tester/RESULTS/<date>-release-gate-decision-groups.md` — the local gate (§ Execution order)
- `.agents/tester/RESULTS/<date>-deploy-e2e-decision-groups.md` — ship + deploy + E2E (§ Ship runbook – § Deploy runbook)

Both must carry a one-line `**Verdict GO**` (or NO-GO) and name the deploy scope explicitly (house convention, `research-fe-precedent.md` §B6).

**Artefacts to retain:**

| From | Artefact |
|------|----------|
| Gate | `A.log`, `A.exit`, `B.log`, `B.exit` (full verbose logs), `anticvacuous.txt` (every added test → `--- PASS` line), `added_go_tests.txt` + the phase-1/2 handoff list |
| Gate | `fmt_base.txt`, `fmt_head.txt`, `fmt_new.txt` (MUST be empty), `fmt_touched_tests.txt` |
| Gate | `vet.log`, `build_all.log`, `build_cmd.log` + the built binary's size/sha256 |
| Gate | `fe_vitest.log`, `fe_tsc.log`, `fe_vite.log`, `static_before.sha`, `static_after.sha`, `static_status.txt` |
| Gate | `smoke.log` + the 3 HTTP status codes + **the REAL-home before/after hash pair (SAME file, both sides — `config.db` + `buffers/`, since the DB-backed ConfigManager never creates `config.json`) and the quarantined smoke pair (`$SMOKE_HOME`'s `config.db` + `buffers/`, proving the smoke wrote only inside the quarantine)** — one real pair + one quarantined pair, not a mixed real/quarantined comparison |
| Gate | the import-guard grep outputs (all three empty) and the boundaries diff (0 bytes) |
| Ship | `$FEATURE_TIP`, `$LATEST_TIP`, `$MERGE_SHA`, `$PRIOR_MASTER`, `$PROMO_SHA`; the parent-line proofs (Gate 1a/2a); the tree-identity output (Gate 2b); `git ls-remote` outputs for both refs |
| Ship | CI run number + URL + the **status timeline** (t+5 / t+15 / t+30) |
| Deploy | `verify_image.json` (index/amd64/config/app-layer digests, layer-verified YES, revision label, version label, env KEYS, binary sha256) + the marker grep counts (primary ≥ 1, secondary, and the 3 control counts > 0) |
| Deploy | per-host: Helm revision before/after (daoduc), deployment generation before/after (primary), `restartCount`, `/fe/api/version` value, `/healthz` + `/v1/models` status codes, the text-lane smoke status |
| Deploy | the **fence-16 anonymous `/v1/messages` ⇒ 401 probe result, per host** (proactive gate assertion; record the raw HTTP code from each probe — anything other than 401 fails the health check) |
| E2E | the BEFORE/AFTER credential snapshots (**ids + metadata only — never values**), the request/response excerpt, the decision event **with the `attempted` / `skipped` / `members_attempted` counters**, the member-attempt log lines, the metered row (winner member id), the final group membership, the live-members-have-no-skip-mark check — **plus, when an exhaustion scenario is exercised, the FAIL-FAST decision event's counters (`attempted` / `skipped` / `members_attempted`), so the operator can see ATTEMPTS vs SUPPRESSIONS (ADR-20)** |

**Log discipline:** raw logs live under `$LOGDIR` (a timestamped `/tmp/dg-gate/<ts>/`); the **summarised** evidence goes into the RESULTS markdown, with the raw logs either committed alongside or archived at a stable path recorded in the report. The results files are committed by the tester convention (`.agents/` artefacts are committed on-branch) — **that commit is the operator's action at gate time, not this plan's.**

---

## Risks & abort criteria

| # | Trigger | Severity | Action | State to leave behind |
|---|---------|----------|--------|------------------------|
| R1 | **Pack A or Pack B red** on a non-quarantined test | CRITICAL | **STOP before any push.** Fix in phase 1/2 and re-gate. | branch unpushed; both hosts untouched at v193 |
| R2 | **A text-lane pin fails** (§ MUST PASS UNCHANGED file modified, or a § MUST PASS UNCHANGED test red) | CRITICAL | **STOP.** A text-lane failure means the widening leaked across lanes. | branch unpushed; both hosts untouched |
| R3 | **Anti-vacuous MISS** (an added test never shows `--- PASS`) | CRITICAL | **STOP.** The gate proved nothing about that behaviour. | branch unpushed |
| R4 | **gofmt NEW drift non-empty** | MEDIUM | Fix the **specific** files (never a repo-wide `-w`); re-run § Execution order step 4. | branch unpushed |
| R5 | **Quarantined flake fires** | LOW | Adjudicate: 3 isolated re-runs; record attempts in the RESULTS report; cite the QUARANTINE row. A different signature ⇒ treat as R1. | unchanged |
| R6 | **CI run does not appear within 5 min** | MEDIUM | Apply the **B1 recovery ladder**: (1) re-run the ORIGINAL push-triggered run — admin-only, hand off to the user/leader with the run URL; (2) if no admin is available and ship is blocked, the **non-zero-delta CONTENT retrigger** (a one-line content commit touching a path inside `docker-ci.yml`'s `on.push.paths` filter → `--no-ff` latest → `--no-ff` master — see the rung-2 text above). **The empty-commit / `--allow-empty` variant CANNOT publish**: a true empty commit has an **empty changed-file set** ⇒ **`on.push.paths` fires NOTHING** ⇒ **no event, no build, no publish** (`docker-ci.yml:4-5`) — do **not** use it (the documented CI-stall emergency exception; the commit is intentional and must not be cleaned up). **A `workflow_dispatch` run cannot publish** — never treat a compile-only dispatch as evidence of a publish. Do **not** ship an unbuilt sha. | both hosts untouched at the live release |
| R7 | **CI build stalls (>30 min, no progress)** | HIGH | **REPORT THE STALL** (run URL + status timeline) and abort the deploy. Do **not** wait silently (run #190 precedent: 6 h hang, admin-only re-run). | both hosts untouched at v193; branch already merged (recoverable by a follow-up promotion) |
| R8 | **Digest mismatch** (`layer digest verified = NO`, or the revision label ≠ the promoted sha) | CRITICAL | **STOP** — never deploy an image that is not the reviewed code. | both hosts untouched at v193 |
| R9 | **Primary marker absent** (`decision_all_members_failed` count = 0) | CRITICAL | **STOP.** The image does not contain the feature. Secondary `GroupLane` is corroborating only. | both hosts untouched at v193 |
| R10 | **Harness control count = 0** (`quota_exceeded` / `ExcludeAndReselectForClass` / `openrouter`) | MEDIUM | The extraction or the harness is broken — re-run (and `--no-cache` if the tag was cached oddly) before drawing any conclusion. | both hosts untouched |
| R11 | **Deploy breaks the live service** (healthz/models not 200, restartCount > 0, text lane regression, anonymous `/v1/messages` back to 200) | CRITICAL | **Roll back along the ladder — tier-1 = fresh-verified live-at-incident; tier-2 = prior release; the incident path NEVER strips a newer release than the incident requires** (ADR-18 C2-3). **Never** the v193-only two-release prescription (it is **SUPERSEDED** — under incident pressure it would strip v194's cache-layer-separation from the substrate the whole ensemble depends on). Re-verify both hosts after each tier; report. | both hosts at the tier-1 target (re-verify per the fresh-verify rule, §7.2) |
| R12 | **E2E failover not observed** | MEDIUM | Clean up per E8; **leave the group with the 2 live members**; report NOT VERIFIED. **Not** a rollback trigger (additive feature). | group present with 2 live members; live credentials untouched |
| R13 | **Any step would require mutating a live credential** | CRITICAL | **STOP.** The E2E design forbids it; there is no authorised variant. | live credentials untouched |
| R14 | **GitOps push backlog surprise** (dcs `latest` 8 commits ahead of origin) | MEDIUM | **Escalate to the leader** before pushing pins; do not silently carry the backlog. | dcs unpushed, unchanged |
| R15 | **FE `npm ci` cannot reach the registry** | MEDIUM | FE gates cannot run ⇒ the gate is INCOMPLETE. Do **not** declare GO on a partial gate. | branch unpushed |
| R16 | **A phase-1 test helper imports `pkg/proxy`** inside `pkg/decision` | MEDIUM | The import guard scans **all** `.go` files in the package dir including `_test.go` — the guard test fails. Fix in phase 1. | branch unpushed |
| R17 | **`pkg/config` shows up in the diff** (the skip-cache TTL getter) | HIGH | **FENCE VIOLATION** — this is now an **explicit expected-diff assertion**, not a note. Per `phase1-plan.md §Planner adjudications → E-3` (RULED): the TTL arrives as an **injected seam** (`skipCacheTTL func() time.Duration`, set from `cmd/main.go` — `cmd/main.go:137`/`:144`/`:182-188` are already in scope), the local `configGetter` interface is **UNCHANGED**, and `pkg/config` gains **NO** new interface member; the `GetUpstreamSkipCacheTTL()` getter is the **fallback only if the seam proves awkward at wiring time**. Phase 1's in-place correction is applied (Stage-4 seams row `:277` now reads "No `pkg/config` change"). A `pkg/config` diff at gate time ⇒ flag it as a fence violation, have phase 1 conform to the injected seam, and re-run the gate; do NOT accept it silently. | branch unpushed |
| R18 | **A pre-existing test's assertions changed** (net churn ≠ zero) | HIGH | Every changed pre-existing function must be **named** in the RESULTS report with a one-line justification. Phase 1 declares "net churn on existing tests: ZERO expectation changes"; an unjustified change to a text-lane expectation ⇒ STOP. | branch unpushed |
| R20 | **AM-4 — RULED (ADR-13); the 5xx case is now settled** | LOW *(was MEDIUM, open)* | **No longer an open decision.** ADR-13 rules the lane mirrors the text-lane failure-class policy EXACTLY: rotation-eligible ⇒ rotate within the member; **5xx ⇒ advance immediately, no rotation, no mark, no cooldown write**; 400/413/415/422 + client-cancel ⇒ fail-fast. The gate asserts this through `TestDecision_Group_5xx_AdvancesWithoutRotationOrMark`, `TestDecision_Group_401_RotatesWithinMember`, `TestDecision_Group_400_413_415_422_FailFastAdvanceBlocked`, `TestDecision_Group_MemberWithTwoCredentials_LimitClassRotatesBeforeAdvancing` and `TestDecision_Group_Exhaustion_5xxMemberCountsAsGenuinelyFailed`. **The re-baseline contingency collapses to the ONE remaining conditional: the D-8 branch** (an extra added test name explained by phase1 §B1) — reported for adjudication, not auto-failed | branch unpushed |
| R21 | **A WITHDRAWN test name appears in the diff** | CRITICAL | **STOP.** `TestDecision_Group_MarkedMember_DemotedNotFiltered`, `TestDecision_Group_NoCooldownWriteOnBenchedPick`, `TestGroup_RewriteGroupEntries_DecisionGroup_PicksDecisionMember` are **SUPERSEDED** (phase1 §A16) — implementing them pins the withdrawn "demote-not-filter" framing (ADR-5: a mark suppresses an ATTEMPT, it never reorders). Their replacements are already in the 50. G4 fails on any withdrawn name present in the diff | branch unpushed |
| R22 | **The `pkg/decision` import guard FAILS** (the `pkg/credentiallb` edge) | ~~CRITICAL~~ → **MEDIUM** | **SUPERSEDED by Amendment 3 / ADR-14 — this is no longer an ABORT-WITH-A-RE-PLAN gate.** ADR-14's verdict is **LEGAL, unconditionally**: `pkg/decision` already carries `pkg/credentiallb` in its transitive closure (`decision → auth → store/database → credentiallb`, non-test dep), so the guard has been passing with that edge present all along. **What survives:** the **verification recipe** in P0.9 (guard tests **plus** the `Makefile:51-68` grep layer, on the **pinned go1.26 toolchain**). A guard failure now means the **recipe was not run correctly** (wrong toolchain, or `go test` used instead of `make test`) — re-run the recipe with the pinned toolchain and `make test`; a genuine failure after that ⇒ report it as an anomaly and re-plan. **The per-credential rotation is no longer blocked by an open question** | branch unpushed |
| R19 | **The scope reduction is reversed** (member×credential fallback is required) | ~~MEDIUM~~ → **LOW** | **SUPERSEDED — the dead framing is retired** (ADR-6: member × credential is the **order of operations**, not an optional reduction; ADR-11: the OQ closures; ADR-13: rotation is **class-conditional** — 5xx advances without rotating). The gate list no longer "assumes member-level fallback" — it asserts the **class-conditional** policy (rotation-eligible ⇒ rotate within the member · 5xx ⇒ advance immediately, no rotation/mark/cooldown · 400/413/415/422 + client-cancel ⇒ fail-fast). **The only surviving content of this row was the guard question — and ADR-14 CLOSES it** (verdict: LEGAL, unconditionally; `pkg/decision` already carries `pkg/credentiallb` in its non-test transitive closure). What remains is the P0.9 **verification recipe**, not an abort trigger. *(Characterization corrected per ADR-14 item 1: `pkg/credentiallb` imports `pkg/models` → `pkg/crypto`, 8 files — **not** stdlib-only.)* | branch unpushed |

**Scope-reduction precondition. *(SUPERSEDED by Amendment 1 — see § Amendment 1 §2 and §3.)*** The earlier text of this paragraph recorded a **SCOPE REDUCTION PENDING USER SIGN-OFF** (member-level fallback only, per `phase1-plan.md §Planner adjudications → E-6`). **ADR-6 retracts it**: R3 makes **member × credential** the **ORDER OF OPERATIONS**, not an optional reduction — "a member is NOT 'failed' until its own resilience is consumed", and per-credential attempts are the minimum faithful implementation. **AC-C1 is formally void.** The gate list is therefore the **Amendment-1** list: **50** added Go top-level test names (41 Pack A + 9 Pack B), **3 withdrawn** names that must not be implemented, **5 NEW** FE files (46 cases *(dated figure — the current FE ledger is 45 NEW-file cases / 48 tokens after ADR-19)*) + **4 UPDATED** FE files (2 new cases: T1, F1) + **8 FE files left alone**, the 8 text-lane pins, and the 8 FE left-alone files.

**What still gates this path:** (i) **P0.9 — CLOSED by ADR-14**; the import legality is settled (**LEGAL, unconditionally** — `pkg/decision` already carries `pkg/credentiallb` in its non-test transitive closure), and what remains is the **verification recipe** (§ Amendment 3 §1), not an abort branch; (ii) **P0.10 / R20 — AM-4 is now RULED** (ADR-13), not open: the lane mirrors the text-lane failure-class policy **exactly** (rotation-eligible ⇒ rotate within the member · 5xx ⇒ advance immediately, no rotation/mark/cooldown · 400/413/415/422 + client-cancel ⇒ fail-fast). **No AM-3 item remains open in this document.**

**Global abort rule:** any CRITICAL row ⇒ **stop the sequence at that step**, leave the cluster in the state stated in that row, write the RESULTS report with the captured evidence, and escalate. A partially-shipped commission is worse than an unshipped one.

---

## Not verified

Everything below could **not** be confirmed from this worktree at plan time; it must be re-read at execution time.

1. **Current live versions are point-in-time.** daoduc Helm **rev 36**, primary deployment **gen 33**, v193 artifact = tag `b11d7c8` / digest `sha256:261622cfec19…` — all from POK (2026-11-14) and the GitOps pin files. Re-read `helm history` and `kubectl get deploy` **before** the deploy; the runbook's rollback targets are only valid if those are still the live refs.
2. **Whether `GroupLane` survives as a printable string in the stripped binary** — expected (precedent: `ExcludeAndReselectForClass` = 2 hits in the same `-s -w` binary), but not confirmed for the `GroupLane` symbol because phase 1 has not written it. The blocking marker is the string literal, which is contract-bound.
3. **The CI run number** for the master push (expected **v194**, as the successor of #193) — read it from the actual run.
4. **The primary apply source** — this plan reads the GitOps manifest `infranstructure/k8s/llmproxy/rancher-local-ensem-dev/deployment.yaml` as the apply input (it carries `image: …:b11d7c8` + the digest comment, verified 2026-11-16), but the live cluster was not observable from here. Confirm the live spec matches the file before applying.
5. **The `data-center-scripts` unpushed backlog** — measured as 8 commits (`origin/latest..latest` = `b0e98f4` and its ancestors) at plan time; re-measure before the pin push.
6. **npm registry reachability** from this host for `npm ci` — `pkg/ui/frontend/node_modules` is absent; the FE gates depend on a successful install.
7. **The added-test names are now KNOWN** (33 Go + 5 FE files) and were enumerated from the real phase-1/phase-2 plans, but the **literal top-level name** of one phase-1 entry is still ambiguous: phase 1 writes `TestGroup_GroupLane_InfersChat|Decision|Mixed` (phase1-plan.md:472 and the acceptance checklist) — `\|` is **not** a legal Go identifier character, so it must be one table-driven function with three sub-cases (recorded here as `TestGroup_GroupLane_InfersChat`) or three separate functions. **Phase 1 must publish the literal**; if it is three functions, the expected list in § Execution order step 3 is re-baselined to the three literals before the gate runs. (The name is a **legal** Go identifier as recorded here.)
8. **`pkg/credclass` does not exist yet** (verified absent at `24c51ef`) ⇒ Pack B is 31 packages today, **32** after phase 1. The Pack B anchor and the `pkg/credclass` placement verdict assume the package lands at the module root as `pkg/credclass/` — phase 1 confirms that path, and confirms it is the **only** new Go package (no other package moves between packs).
9. **The user's live decision-model ids / internal model names** live in the running DB — not read (research §9). The E2E reads them from the live API.
10. **The decision event stream's exact field names for member-level detail** — the E2E captures "the decision event for the request" and the operator reads whatever member detail is present; the precise field set is phase 1's implementation.
11. **Whether the E2E's throwaway base URL is accepted by the admin API's URL validation** (e.g. an SSRF-style rejection of loopback/`127.0.0.1`). The `.invalid` host is the primary choice precisely because it avoids the loopback check; if it is rejected, fall back to `http://127.0.0.1:1` and, if that is also rejected, report rather than weakening the probe.
12. **Live `/v1/decisions` upstream behaviour** for the user's two providers (openai / openrouter) — the E2E depends on both being reachable; if one is down, the failover observation is confounded by an upstream outage. Record which member served the request.
13. **Whether `pkg/credentiallb` is permitted in the `pkg/decision` import closure — CLOSED (ADR-14): LEGAL, unconditionally.** *(**SUPERSEDED** — ADR-14.)* Two supersessions land here: **(i)** the "only becomes relevant if the user rejects the member-level scope reduction" framing is **doubly stale** — the member-level scope reduction was **retired by ADR-6** (member × credential is the **order of operations**, not a reduction) and the reduction framing was **retired by ADR-11**; **(ii)** the characterization is corrected per ADR-14 item 1: `pkg/credentiallb` is **not stdlib-only** — it imports `pkg/models` (→ `pkg/crypto`) and has **8** `.go` files (not 3). The verdict itself is no longer open: `pkg/decision` **already carries `pkg/credentiallb`** in its transitive closure (`decision → auth → store/database → credentiallb`, non-test dep). **What remains is not a question but a RECIPE** — § Amendment 3 §1 / P0.9: the three guard tests **plus** the `Makefile:51-68` grep layer, run on the **pinned go1.26 toolchain**.
14. **The two-name discrepancy in `pkg/decision/handler_group_test.go`** (dispatch 16 vs artifact 14) is unresolved on the dispatch side — the artifact on disk is authoritative for this runbook (14). If phase 1 later publishes two further names, the § Execution order step-3 list is re-baselined; the gate mechanism is unchanged. **CLOSED by Amendment 1** — the file now carries **27** and the list was re-derived from the amended plan (§ Amendment 1 §1); the earlier note is superseded.
15. **ADR-1 (kind immutability) — CLOSED by Amendment 2** (`phase1-plan.md §B1`): the guard lands THIS release as Stage **3b**; the single-member re-lane hole E-2 documented is closed by the guard (pin: `TestGroup_SingleMemberDecisionGroup_MemberRetypeRefused`). *Amendment 2 §6 carries the residual: the `pkg/credentiallb` import legality (AM-3) is still formally UNVERIFIED — ADR-13 settles the policy, not the import question.*
16. **AM-4's 5xx-inside-a-member behaviour is an OPEN LEADER DECISION** (phase1 §A19). Phase 1 rotates to the member's next credential on ANY failed attempt; the ADRs do not spell out the 5xx case. Recorded as **P0.10 / R20** — if the leader rules otherwise, G2/G4 are re-baselined.
17. **The phase-2 FE open questions** (phase2 §Amendment 1 → "Open / not ruled"): whether the **shadow-target picker** should also exclude inactive models (a one-line change plus one test case, **not implemented here**), whether member chips should be annotated with servability state (no), and the **AC-R10 token names** (the FE's `EventType` union extension + curated labels are deferred to the phase-1 handoff, in the same commit as the BE change; until then the generic fallbacks carry the trio — pinned by **F1**). If any is decided before the gate, the FE lists are re-baselined.

---

## Cross-plan reconciliation (for the aggregator to verify) — RESOLVED against the real phase-1/phase-2 plans

**Status: all items RESOLVED** against `phase1-plan.md` (822 lines) and `phase2-plan.md` (630 lines) as they now stand on disk. **What changed in this document** is named per row.

| # | Item | RESOLUTION | What changed in phase3-plan.md |
|---|------|-----------|------------------------------|
| R1 | **Anti-vacuous name list** | **RESOLVED — then RE-DERIVED FIVE TIMES: 33 → 50 (Amendment 1) → 59 (Amendment 2) → 73 (Amendment 4/5) → 74 (approver iteration-002 FIX 1)**, each time independently re-derived from the phase-1 plan and **set-identical** to the dispatched list, with **0 base collisions** each time. **Final: 73 ADDED Go top-level test names** (64 Pack A + 9 Pack B) + **5 WITHDRAWN** names + **5 NEW FE files** (45 cases) + **5 UPDATED** FE files (3 new cases: T1, F1, V1) + **7 FE files left alone** ⇒ **48 new FE cases**. *(SUPERSEDED by Amendments 1/2/4/5 — the earlier resolutions are recorded below.)* **Collision check run against the base tree: 0 collisions** at `2b987bf` (no expected name exists at base). **One near-miss prefix found** (still relevant): the EXISTING `TestHandleModels_FilterDecision` (`pkg/proxy/handle_models_imggen_test.go:134`) is a strict prefix of the NEW `TestHandleModels_FilterDecisionGroup` ⇒ the anti-vacuous grep is **end-anchored** (`^--- PASS: <name> (\|$)`) so the old test's PASS line cannot satisfy the new name. | § Execution order step 3 now carries the **concrete 73-name list** (in a heredoc) + a **bidirectional** diff cross-check (`listed_but_absent` / `present_but_unlisted`, both must be empty) + the base-collision guard + the **D-8 branch adjudication** (whitelist-of-one); step 6 uses the end-anchored grep + the FE file checks (NEW 5, UPDATED 5, LEFT-ALONE 7) + the T1/F1/V1 case checks; the G4 row names the 73/5/47 counts. The placeholder "phase 1/2 handoff list" wording is **removed**. |
| R2 | `pkg/proxy/handler_anthropic_misroute_test.go` | **RESOLVED — CONFIRMED.** The file **exists at base** (136 lines) and already holds `TestAnthropicBoundary_DecisionMisroute` + `TestAnthropicBoundary_DecisionMisroute_Negatives`. Phase 1 adds **1 new function**, `TestAnthropicBoundary_DecisionGroupMisroute` (phase1 §4b). Contract = *add a function*, not *create a file*. | The §"RECEIVE NEW FUNCTIONS" table row states the file EXISTS at base and names the 2 functions that stay byte-identical. |
| R3 | GroupLane decorator-through clone | **RESOLVED — location is concrete.** New file **`pkg/modelscache/models_group_lane_test.go`** with **2** functions: `TestCachedModelsConfig_GroupLane_ReachesInnerManager` (the trap pin — failure text MUST name `GroupLane is a no-op through the decorator`, cloned from `pkg/proxy/win_clear_winner_identity_test.go:120`/`:191`) and `TestCachedModelsConfig_GroupLane_MixedGroup_ReturnsFalse`. Phase 1 §Stage 5 + R5/D-8/contract 22 make it a **MERGE GATE**. | Both names are in the § Execution order step-3 list (Pack B) and in the §"RECEIVE NEW FUNCTIONS" table, marked **MERGE GATE**. |
| R4 | `pkg/models/group_rules_test.go` | **RESOLVED — corrected.** Phase 1 adds **5 new functions** and **LEAVES `TestValidateGroupMembers_CollectionRuleViolations` (`:105`) byte-identical** (the image-gen member case has an empty valid-lane set, so the chat wording still matches). The earlier draft of this plan predicted the existing table's expectations would be rewritten — **that prediction was wrong**; the gate is now stricter (the pre-existing assertion must not move at all). | §"EXPECTED TO BE UPDATED" was **replaced** by §"RECEIVE NEW FUNCTIONS — existing assertions stay UNCHANGED", with an explicit **CORRECTION** note; the net-churn verification was added. |
| R5 | `pkg/store/database/store_group_test.go` | **RESOLVED — corrected.** Phase 1 adds **6 new functions** and leaves `TestGroup_AddModel_GViolations` (`:146`), `TestGroup_RewriteGroupEntries_Am28StaleMember` (`:592`) and `TestGroup_C1_BackupLessGroupsKeepV1Semantics` (`:926`) **LEFT ALONE** (the Am28 fixture's members are *disabled*, not retyped ⇒ the predicate's `!row.Enabled` arm fires first ⇒ identical behaviour). No "meaning change" to existing expectations. | Same replacement as R4; the 3 named functions are listed as byte-identical. |
| R6 | `pkg/store/database/store_group_skipcache_test.go` | **RESOLVED — CONFIRMED.** `TestGroup_SkipCache_NilCacheByteIdentical` (`:165`) **and** `TestGroup_SkipCache_HealthAwareSelection` (`:62`) are both **LEFT ALONE** (phase 1 §5). Phase 1's declared claim is **"net churn on existing tests: ZERO expectation changes"** — the gate now **VERIFIES** that claim mechanically (base-vs-head function inventory + a `pre_existing_funcs_touched.txt` candidate list that must be empty or justified). | Both functions added to the § MUST PASS UNCHANGED table; the **net-churn verification block** (base/head function inventories, missing-function check, touched-function candidate list) was added; a FE counterpart (8 left-alone FE test files, `git diff --exit-code`) was added. |
| R7 | `pkg/credclass` placement | **RESOLVED — CONFIRMED.** Phase 1 creates exactly **one new Go package**: `pkg/credclass/` (module root) — `classify.go` + `classify_test.go`, **stdlib-only, no internal imports** (the leaf law). All other phase-1 additions are **new files inside existing packages**: `pkg/models/group_lane.go` (Pack B), `pkg/store/database/store_group_test.go` (Pack A), `pkg/modelscache/models_group_lane_test.go` (Pack B), `pkg/ui/kind_rules_parity_test.go` (Pack A), `pkg/decision/handler_group_test.go` (Pack A), `pkg/proxy/*` (Pack A). **No package moves between packs**: Pack A stays 10 packages, Pack B goes **31 → 32** (`pkg/credclass`). | The `pkg/credclass` placement verdict is unchanged and now cites phase 1's leaf law; the §9 row records the full package-movement audit. |
| R8 | FE change surface | **RESOLVED — flips G11.** Phase 2 **does** change FE source: new `DecisionModelsContainer.tsx` + `groupLane.ts`; changed `SettingsPage.tsx`, `GroupModelsTab.tsx`, `GroupModelForm.tsx`, `GroupMemberEditor.tsx`, `types.ts`. ⇒ `pkg/ui/static/` **will** legitimately differ after `vite build`. Phase 2's tsc verdict: `tsc-baseline.txt` **stays UNCHANGED** (`TOTAL_ERRORS=19` / `TOTAL_FILES=9`, same nine-file set, `SettingsPage.tsx` stays 6, `ModelsTab.tsx` stays 6, the seven 1-error files stay 1); a **new** file with an error is a **FAILURE, not a baseline bump**. | The G11 row now asserts the **"FE source changed" branch**: `git status --porcelain pkg/ui/static` must be **NON-EMPTY** *and* the regenerated assets must be in the **same commit** as the FE source change (an empty diff is itself a FAILURE — stale assets). The G11 sub-section was rewritten to state precisely what is and is not expected to be identical. G10 now carries phase 2's "no baseline bump" verdict. |
| R9 | `pkg/decision` import direction | **RESOLVED — CONFIRMED and sharpened.** The mechanical guard (`Makefile:38-56` greps + `pkg/decision/handler_test.go:39-64`) bans **pkg/proxy** and **pkg/imggen** only, and it scans **every `.go` file in the package dir, including `_test.go`**. A `pkg/providers` import is **not** mechanically banned — but it is **transitively** banned: `pkg/providers/openai.go:31` imports `pkg/proxy/translator` ⇒ the `pkg/proxy/` prefix ⇒ `TestPackage_ImportGuard_TransitiveClosure` fires. **That is the verified mechanism that makes the `pkg/credclass` leaf load-bearing** (phase 1 Stage 0: stdlib-only, no internal imports). | The import-guard warning is retained verbatim in § Execution order step 7 and in abort row R16, now naming the `pkg/providers/openai.go:31` → `pkg/proxy/translator` mechanism as the reason. |
| R10 | Contract ↔ test mapping | **RESOLVED.** Phase 1's §"Acceptance checklist (the ruling's 22 backend contracts)" names the pinning test for each backend contract (e.g. contract 1 → `TestValidateGroupMembers_MixedKindMembers_Rejected`; contract 13 → `TestDecision_Group_AllMembersFail_502DecisionAllMembersFailed`; contract 22 → the decorator-through test). Phase 2's §6.1 gives the FE case→contract mapping for contracts 23–27. Every contract has at least one named added test. | No change needed to the gate; the §9 row records the source of the mapping. |
| R11 | `pkg/ui/kind_rules_parity_test.go` update shape | **RESOLVED — the AND-semantics is preserved.** Phase 1 adds **1 new function** `TestKindRules_Parity_Group_DecisionLane` asserting **each of the four mirror sites independently** (helper ≡ POST ≡ PUT, three separate assertions per case, house discipline); the shared `addGroupMembers` fixture and the existing table are **LEFT ALONE**, with a **separate** `addDecisionGroupMembers` fixture. | The §"RECEIVE NEW FUNCTIONS" table row states the per-site independent assertion and the separate fixture helper. |
| R12 *(new)* | **Skip-cache TTL seam** — leader's ruling | **RESOLVED — a `pkg/config` diff is a FENCE VIOLATION, now an explicit expected-diff assertion.** The TTL arrives as an **injected seam** (`skipCacheTTL func() time.Duration`, set from `cmd/main.go`), the local `configGetter` interface is **UNCHANGED**, and `pkg/config` gains **NO** new interface member; the `GetUpstreamSkipCacheTTL()` getter is the **fallback only if the seam proves awkward at wiring time**. The in-place correction is already applied in phase 1 (Stage-4 seams row `:277` now reads "**No `pkg/config` change**"; risk R7 reworded; the commit-plan mention of `pkg/config/config.go` is superseded). | G13's expected-diff statement now includes `pkg/config/` with an explicit "0 bytes ⇒ else fence violation" + the adjudication citation; new precondition **P0.7**; new abort row **R17** (rewritten with the adjudication reference). |
| R13 *(new)* | **Scope reduction** | **RESOLVED, then SUPERSEDED (ADR-6 / ADR-11 / ADR-13), then CLOSED (ADR-14).** The original record — "member-level fallback only; the reduction is **pending user sign-off**" — is **dead**: ADR-6 makes member × credential the **order of operations**, ADR-11 closes the OQs, and ADR-13 makes the rotation **class-conditional** (5xx advances without rotating). **The only surviving content of this item was the guard question, and ADR-14 CLOSES it** (LEGAL, unconditionally — `pkg/decision` already carries `pkg/credentiallb` in its non-test transitive closure). | Precondition **P0.8** re-scoped by ADR-6; abort rows **R19** and **R22** marked superseded; the "Scope-reduction precondition" paragraph above the global abort rule rewritten to state the **ruled** class-conditional policy; the surviving action is the **P0.9 verification recipe** (§ Amendment 3 §1). |
| R14 *(new)* | **Feature marker agreement** | **RESOLVED — the two documents agree.** Phase 1 §Stage 6 selects the **same** literal: `decision_all_members_failed`, with the same recipe (`strings <binary> \| grep -c 'decision_all_members_failed'` ⇒ **MUST be ≥ 1**) and the same prohibition (`strings \| grep -ci sticky` — Tailwind false hits). Phase 1's rationale: "unique in the tree (no false hits) … no Tailwind-class false-positive risk". **Re-verified after the phase-1 rewrite** (Stage 6 unchanged in the 1029-line plan). | The marker section now states the **cross-document agreement** (with the re-read) and keeps phase 3's independently **measured** negative controls (0 hits for `decision_all_members_failed` and for `GroupLane` in the v193 binary; `ExcludeAndReselectForClass` = 2, `quota_exceeded` = 2, `openrouter` = 5) as the harness-sanity assertion. |
| R20 *(new, Amendment 5)* | **Review cycle 2 (ADR-18)** | **RESOLVED — folded in.** C2-2 (LAYER SEPARATION, both sites fixed — the Amendment 1 §4(a) line and the operator-verification note), C2-3 (R11 → tier-1 = fresh-verified live-at-incident; tier-2 = prior release), C2-4 (fresh-verify scope extended to every plan-time deploy/dcs number + the dead dcs figure → fresh-verify), C2-5 (Tier-1 table vs command: `win_clear_winner_identity_test.go` **added to the command** + its cell names the clone's file `pkg/modelscache/models_group_lane_test.go`; the `group_rules_test.go` Tier-1 row **deleted**). G4 re-derived to the **FINAL 73** (60 Pack A + 9 Pack B) + **5 withdrawn** + **48 FE cases**; the doctrine seam corrected to `pkg/decision/suppression.go`; Stage 0-A referenced as precondition P0.0; the marker's negative control measured at **both tiers** (0 at v193 `b11d7c8` and v194 `fd1649c`). | New consolidated **§ Amendment 5** section (10 subsections); in-place corrections across Amendment 1 §4(a), the operator-verification note, R11, the Tier-1 table/command, the doctrine-seam reference, and the preconditions; **G0–G14 intact**, no gate weakened. |
| R19 *(new, Amendment 4)* | **Review cycle 1 + base re-anchor + architect fold (ADR-15/16/17)** | **RESOLVED — folded in.** G4 re-derived to the **FINAL 69** (60 Pack A + 9 Pack B) + **5 withdrawn** + **48 FE cases**; **CG-1** the Tier-1/Tier-2 split (`handle_models_imggen_test.go` → Tier-2); **CG-3** G11 collapsed to ONE verdict rule (empty porcelain is NOT a failure); **CG-5 + ADR-17** the rollback **ladder** (tier-1 = live v194 `:fd1649c` / helm 37; tier-2 = v193 `:261622cf` / helm 36) with a **fresh-verify-at-ship rule on every number** and CI **≥ 195**; **ADR-16** the base re-anchor `24c51ef` → **`2b987bf`**, the fence re-defined as **"no v194 semantic regression"**, the Tier pins **re-derived** against v194's five new skip-cache pin names, the hotspot list **extended** with the v194 files, and the **FULL gate re-run on the merge regardless of conflict**; the **four RED-gap gates** with their pinning tests and the planner-endorsed RED-1 refusal semantics; the three **re-verification checks** named; the warnings **W4/W5/W6/W7/W8/W10** folded; the marker's negative control **measured at v193 AND v194** (0 hits at both). | New consolidated **§ Amendment 4** section (11 subsections); in-place corrections across the header, G4, G11, the Tier lists, the ship/deploy runbooks, the execution-order steps and §9; **G0–G14 intact**, no gate weakened. |
| R18 *(new, Amendment 3)* | **AM-3 closure (ADR-14)** | **RESOLVED — CLOSED: `pkg/credentiallb` is import-legal from `pkg/decision`, LEGAL unconditionally.** `pkg/decision` **already** carries `pkg/credentiallb` in its non-test transitive closure (`decision → auth → store/database → credentiallb`), so the guard has been passing with that edge present all along. The abort gate (R22) became a **verification recipe** (P0.9): both guard layers (the three guard **tests** + the `Makefile:51-68` grep layer that plain `go test` does not run), the **pinned go1.26 toolchain** (`/usr/bin/go` fails on go.mod offline), the **non-test-deps-only** scope, and the evidence grade (literal `=== RUN`/`--- PASS`, clean closures, a `/tmp` direct-import scratch passing all guards, and a quoted-banned-path **negative control**). The **characterization** is corrected (`pkg/credentiallb` imports `pkg/models` → `pkg/crypto`, 8 files — not stdlib-only), the **raw-text guard trap** and the **`pkg/grouplb` placement constraint** are recorded for the operator and the implementer. | New consolidated **§ Amendment 3** section (7 subsections); P0.9 rewritten; R19/R22 marked superseded; § Not verified item 13 closed; § Execution order step 7's import-guard block rewritten (incl. the stale `Makefile:38-56` → `Makefile:51-68` citation); **G0–G14 intact**, **G4 lists unchanged (59 Go / 48 FE)**. |
| R17 *(new, Amendment 2)* | **ADR-1 fold + AM-4 ruling (ADR-13)** | **RESOLVED — folded in.** G4 re-derived to **59** names (50 Pack A + 9 Pack B) + **4 withdrawn**; the **D-8 branch conditional** (an extra added test name explained by phase1 §B1 ⇒ **reported for adjudication, not auto-failed** — whitelist-of-one); **AM-4 RULED** (rotation-eligible ⇒ rotate within member · 5xx ⇒ advance immediately, no rotation/mark/cooldown · 400/413/415/422 + client-cancel ⇒ fail-fast); the exhaustion criterion made **class-conditional** (a 5xx'd member **counts as genuinely failed**); the **AC-R4 caveat** (ADR-13 governs); the marker's **negative control** now a **pinned requirement** (absent from the live v193 binary). | New consolidated **§ Amendment 2** section (6 subsections + in-place-correction index); new/changed rows: G4, step 3, step 6, P0.10, R20, Amendment 1 §2, deploy step 7 (E6), Evidence to retain, deploy step 5.1; **G0–G14 not renumbered**, no gate weakened. |
| R16 *(new, Amendment 1)* | **User ruling consequences (ADR-5..ADR-12)** | **RESOLVED — folded in.** G4's list re-derived to **50** (41 Pack A + 9 Pack B) + **3 WITHDRAWN** names; the exhaustion criterion re-derived (member × credential; suppressed attempts are not failures); **P0.9** (AM-3 import legality — run the guard EARLY) + **P0.10** (AM-4 open leader decision); the AM-2 second-skip-cache-instance operator note; the AM-1 `pkg/grouplb` export (pack membership unchanged); the **AC-R9 (ADR-9) → AC-R12 (ADR-9)** renumbering; the ADR-9 coordination rule (**surface, never force** hotspot conflicts) added to the ship step. | New consolidated **§ Amendment 1** section (9 subsections + an in-place-correction index) at the head of this document, with `*(SUPERSEDED by Amendment 1 …)*` markers at every corrected site; new abort rows **R20**/**R21**/**R22**; no gate was renumbered or weakened. |
| R15 *(new)* | **Delta re-read** — phase-1 grew 822 → 1029 lines during the reconciliation | **RESOLVED — the name list was re-derived from the NEW artifact, not patched.** The `## Planner adjudications` section (lines 832-1029) records E-1..E-6 and applies four in-place corrections (Stage-4 seams row → injected seam; risk R7 reworded; §Escalations E-2 narrowed to the multi-member form; **one** added Stage-4 test row). A fresh extraction of the NEW `## Test plan` yields **33** NEW names — **identical** to the earlier list (`diff` empty). The adjudication's E-4 row `TestDecision_Group_StampedStatusDrivesClassification` was **already** in the 822-line version and is **already counted**. The dispatch's "16 cases" for `handler_group_test.go` does **not** match the artifact (14 rows, lines 492-505; 14 distinct `TestDecision_Group_*` names anywhere in the file, set-identical to the table rows) — **no names were invented to reach 16**. | The **G4 ledger** records the per-file breakdown (33 = 14 + 6 + 5 + 2 + 2 + 1 + 1 + 1 + 1) and the 14-vs-16 **adjudication note**; the §"RECEIVE NEW FUNCTIONS" decision-lane row says **14**; E-3/E-6 are cited by their adjudication IDs throughout. |

---

## Exit criterion for phase 3

Phase 3 is complete when: the local gate (§ Execution order) is **GO** with every artefact in § Evidence to retain retained; the anti-vacuous check shows **zero** misses; the text-lane pins (§ MUST PASS UNCHANGED) are **unmodified and green**; the ship (§ Ship runbook) and deploy (§ Deploy runbook) steps were executed **only** under explicit user authorisation, with the ship/deploy/E2E evidence recorded in `.agents/tester/RESULTS/<date>-deploy-e2e-decision-groups.md`; the primary E2E shows a real decision group serving from the user's 2 live decision models with the throwaway-member failover observed; the group is **left in place with exactly the 2 live members**; and the live credentials are **provably untouched** (id-set + metadata diff).

**This plan performs none of the above.** It is the instruction set for the operator who will.
