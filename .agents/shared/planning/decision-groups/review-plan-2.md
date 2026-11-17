# review-plan-2 — Cycle-2 Delta Re-Review, feature commission "decision-groups" (llm-supervisor-proxy)

- **Date:** 2026-11-16 · **Supersedes:** review-plan-1.md verdict (cycle-1 findings remain historically valid; this file governs the delta)
- **Mode:** 🔴 Deep-Review continuation — cycle-1 governor revived with full context (2 councilors on models agentic + coding, cycle-1 memory intact; Round 0 + 2 refinement rounds). Read-only compliance clean; **no councilor wrote any file**.
- **Corpus verified against reality (both models):** decisions.md 1128 · phase1-plan.md 2504 · phase2-plan.md 889 · phase3-plan.md 1409 · plan-overview.md 327 · advisory (final, §0-§8) 990; HEAD `2b987bf94e39…`; tracked tree clean, planning docs untracked; base delta `24c51ef..2b987bf` = 15 commits / 36 files / +3507/−630 (matches `phase3-plan.md:308`).
- **Session IDs:** governor `fac8c958-3347-464b-8207-b98e9421732a`; councilors agentic `8aefd01f…0f53`, coding `4bb0b56e…9994`.
- **Reviewer:** ensemble reviewer[v2] (controller/aggregator — findings produced by the council).

---

## VERDICT: CRITICAL GAPS

Unanimous, cross-validated through 2 refinement rounds (both councilors self-corrected under cross-validation — conclusions changed, not rubber-stamped). **The tranche is a real, substantive improvement: the ruling encoding is sound, the four architect RED gaps are folded as engineering with named tests, and 69/5/47 reconciles set-identically. All residue is in the operational/gate layer — every item is a small localized doc fix; none requires redesign.**

---

## A. Cycle-1 blocker verification (encoding, not markers)

| Blocker | Status | Evidence |
|---|---|---|
| CG-1 whole-file byte-identity on `handle_models_imggen_test.go` | **FIXED** (narrow) | Tier-2 per-function rule `phase3:294`/`:511-513`; file moved `:538`; recipe `:547-557`; absent from command `:564-573`. The split mechanism introduced C2-5. |
| CG-2 shared-instance contradiction | **PARTIAL** | Binding rule now shared-instance + layer fence (`phase1:2066-2111`; `decisions:1015-1026`); pin test present BY NAME (`phase1:573` → `TestDecision_Group_CacheLayerSeparation_SharedUpstreamInstanceOneAnswer`; `phase3:707`); R5 double-marked (`:473`); task 4.10 (`:405`); §A2 (`:1247`); withdrawn sibling marked (`:572`). BUT four operative sites unmarked ⇒ **C2-2**. |
| CG-3 G11 self-fail | **FIXED** | Single rule `phase3:298`/`:445`; rationale `:473-483`; script `:492-493` ("empty porcelain is NOT a failure"); old FAIL-on-empty gone. Residue: R8 `:1390` (optional). |
| CG-4 base currency | **FIXED substantively** | Re-anchor `phase1:2034-2045`/`phase3:306-312`; **178 pins / 45 rows re-derived** (`phase1:2229-2281`); fence = "no v194 semantic regression" (`:2283-2289`; `phase3:310`); hotspots extended (`phase3:314`/`:1060-1068`); full gate re-run on merge regardless of conflict (`:316`/`:1074`). **Spot-check PASSED**: the previously inverted ordering pins are genuinely re-derived — five v194 names verified in-tree (`phase1:596`/`:2273`; `phase3:525-532`/`:312`); old `NilCacheByteIdentical`/`HealthAwareSelection` correctly gone. Residue: header assertions `plan-overview.md:8`, `decisions.md:3` (optional). |
| CG-5 rollback ladder | **PARTIAL** | Ladder corrected (`phase3:302`/`:1265-1266`), fresh-verify (`:304`/`:1261`), STALE-BY-CONSTRUCTION (`:1268`), CI ≥ 195 (`:302`) ⇒ but **C2-3 + C2-4** survive. |

---

## 🔴 CRITICAL GAPS (blocking — cycle 2)

### C2-1 — AC-R9's doctrine-line pin targets a **dissolved** file
- **Evidence:** `phase1-plan.md:560`, `:1330`, `:1420`, `:1558` name `pkg/decision/layer_upstream.go` — dissolved by Amendment 5 §E1 (`phase1-plan.md:2056`); no replacement seam named. The gate-enforced test `TestDecision_DoctrineLine_PinnedVerbatimAtSeam` (phase3 expected list `:697-778`) therefore has **no implementable target**. Realistic failure mode: an implementer creates a decision-local UPSTREAM cache file to satisfy the pin, re-introducing the lane-local instance ADR-16 voided.
- **Fix:** name the real seam. Coding's finding: `pkg/skipcache/skipcache.go:29-31` already carries the literal verbatim — but the architect's §0 table forbids rewriting `pkg/skipcache` docs this release ⇒ **leader must pick the target** (named decision below).

### C2-2 — four operative, unmarked "separate instance / never shared" sites
- **Evidence:** binding rule is shared-instance + layer-fence (see CG-2 row), but: `phase1-plan.md:404` (Stage 4.8 — also cites the v194-**removed** manager seam `SetSkipCache`, zero hits repo-wide at `2b987bf`); `phase3-plan.md:75` (Amendment 1 §4(a)); `phase3-plan.md:1198` (operator note — "a SECOND skip-cache instance … never shared", void under ADR-16: an operator would verify an impossible invariant and report a false defect); `phase1-plan.md:1603-1604` (A19 AM-1/AM-2 rows unmarked while AM-3 on the same table is marked). The tranche's "both stale sites marked" sub-claim is therefore **false**.
- **Fix:** supersession markers on all four; correct `:404`'s dead `SetSkipCache` reference.

### C2-3 — the CRITICAL abort row R11 still prescribes a v193-only two-release rollback
- **Evidence:** `phase3-plan.md:1332` verbatim: "Roll back: primary first (`:b11d7c8`), then backup (`helm rollback 36`) … both hosts at v193 (rev 36 / gen 33-equivalent)" — **operative** (inside `## Risks & abort criteria` `:1318`; global abort rule `:1349`; next heading `:1383`), absent from Amendment-4's in-place index (`:347-364`), directly contradicting the corrected ladder (`:1265-1268`, tier-1 = `:fd1649c` / helm 37).
- **Why blocking:** under incident pressure the operator strips v194 cache-layer-separation from the substrate the whole ensemble depends on. This is CG-5's core defect surviving in the highest-stakes section.

### C2-4 — stale deploy/abort numbers the fresh-verify rule does not reach
- **Evidence:** `phase3-plan.md:1189` (rev 36), `:1193` (new rev 37 — actual 38), `:1220` (gen 33→34 — already 34), `:1357` (rev 36/gen 33), `:1359` ("expected v194" vs `:302` "CI ≥ 195"). The rule's scope is "every number **below**" (`:304`, `:1261`) ⇒ governs §7.2's ladder only; the sole in-place pointer in the deploy runbook is `:1208`. Same class: the **dcs backlog figure** (`:1131` operative ESCALATE-BEFORE-PUSHING instruction; R14 `:1335`, `:1361`) is stated as measured and unmarked — dcs is now in sync (`7f89eac`), so the escalation targets a non-existent backlog and R14's trigger can never fire.
- **Fix:** extend the fresh-verify scope to every plan-time deploy/dcs number, or mark them; correct the dcs figure.

### C2-5 — Tier-1 gate: one silent enforcement gap + one unsatisfiable row, OPPOSITE fixes
- **Evidence:** Tier-1 table = 12 files (`phase3-plan.md:517-528`); executable command = 10 files (`:564-573`); the `comm` difference is exactly two files:
  - `pkg/proxy/win_clear_winner_identity_test.go` (`:524`): phase 1 does NOT edit it (citations only, `phase1:542`/`:702`; the decorator-through clone lands in the NEW `pkg/modelscache/models_group_lane_test.go` — `phase1:519-520`/`:700-701`/`:860`; already Tier-2 at `phase3:544`). Tier-1 byte-identity row is **satisfiable but unasserted** — absent from the command; net-churn (`:577-598`) **cannot** substitute: steps 1–3 compare name inventories only, step 4 greps `^-func (Test…)` so assertion-body edits are invisible (Tier-2 recipe `:552-556` same blind spot). **Fix:** add to the command (one line) + fix `:524`'s cell wording to name the clone's file.
  - `pkg/models/group_rules_test.go` (`:528`): phase 1 DOES edit it (5 new functions, `phase1:543`) — Tier-1 row **unsatisfiable as written**. **Fix:** delete the Tier-1 row (already correctly absent from the command).
- **Severity adjudication (transparent):** coding 🟡 (no deterministic self-fail; one-line fix) vs agentic 🔴 (silent enforcement gap on the text lane's flagship v193 decorator-trap pin). **Governor ruling: blocking** — a named Tier-1 byte-identity pin with no executable enforcement path is a hole in the corpus's sole regression mechanism (checklist item 4: are the pins sufficient — no). Caveat: cannot cause a false STOP; can only let a regression through. Cost: one line.

### REMAINING GAP (explicitly stated, per the disposition contract)
**C.1 — T1–T4 vocabulary pins are NOT encoded.** `decisions.md:1104-1107` records the set as "not authorized this tranche — only T5's FE half (V1) landed"; the word DEFERRED appears nowhere; **no phase-1 task exists**. T2 (out-of-set provider rejected), T3 (source-literal pin across the 5 surfaces), T4 (anthropic credential saves while a decision model references it) have no named test and no task; T1 is only partially covered by two already-named tests; T5's Go half (registry key set ≡ gate allow-list) is unencoded. Both councilors: non-blocking for the implementation gate, **but the disposition as stated is not encoded** — needs a phase-1 addendum naming the four pins, or an explicit reviewer deferral recorded in `decisions.md`.

---

## B. New-since-cycle-1 items — status

1. **Four RED gaps: ENCODED ✓** — RED-1 `phase1:2338-2382` (tasks 2c.1–2c.3; `TestGroup_RewriteGroupEntries_DecisionGroupInChatChain_BoundaryRefusal :2377`; `TestUpdateModel_FallbackChain_DecisionGroupTarget_Rejected :2381`; adjudication `decisions:1090-1098`); RED-2 `:2384-2403` (O-1..O-4 + `TestPackage_ImportGuard_GroupPolicyFiles_NoCacheImports :2499`); RED-3 `:2405-2431` (compile-time `var _` on all four consumers + 3 `_ThroughCachedConfig` effect tests); RED-4 `:2433-2442` (all-models fold + `TestHandleModels_FilterDecisionGroup_ThroughCachedConfig`).
2. **Dissolutions encoded** (I-1 `phase1:1217-1221`/`:1247-1248`/`:2051`; AM-1 `:2052`; three plan-local layer files `:2054-2056`; `store_group_policy.go` retracted `:2064`/`:2176-2184`; ADR-16 `:977-991`; ADR-17 `:1055-1057`) — **but dangling references survive** (blockers C2-1/C2-2 + optional list below).
3. **v194 consumption coherent ✓** — shared instance + layer fence `phase1:2066-2111`; MODEL layer = landed `pkg/modelcache`, chain reader still ABSENT and recorded `:2113-2136`; `CooldownUntil` D-C precondition `:2138-2152` (`credentiallb/engine.go:468-477` verified in-tree); AST-pin obligations `:2154-2174` (`cache_layer_separation_test.go:752`).
4. **G4 = 69/5/47 CONFIRMED by both, independently, whole-document** — heredoc `phase3:697-778` = 69 unique names, every one present in phase1 (89 distinct `Test*` = 69 added + 5 withdrawn + 15 base-existing); ledger sums 69 (`phase1:428`/`:530-532`); withdrawn 5 (`:288`/`:432`); FE 47 = 44 new + 3 updated (`phase2:866`/`:425`); **zero one-sided names**.
5. **Three cycle-1 carries evidenced ✓** — (a) decorator enumeration at `2b987bf` `phase1:2186-2202` (modelscache zero-diff ⇒ `GroupLane` still the only new method); (b) D-2 negative controls named incl. `TestGroup_SweepKindParameterization_MutationControl` `:2303-2314`/`:574`; (c) AM-3 P0.9 both guard layers + pinned go1.26 + negative control `phase3:287`/`:387`/`:938-942`.
6. **ADR-1 precedent REAL ✓** — `pkg/ui/server.go:864-868` `groupReject` → **400** + bare `{"error":…}`; member-kind `:942`; shadow `:959`/`:963` ⇒ 400 (409 never triggered); encoded `phase1:2220-2227`, `decisions:1084-1086`.

## C. Leader dispositions (encoding adjudicated; dispositions honored)

| # | Disposition | Encoding verdict |
|---|---|---|
| 1 | T1–T4 → phase-1 named tasks | **REMAINING GAP — not encoded** (C.1 above) |
| 2 | doc.go closure prose as Stage-1 task 1.6 | **VERIFIED** — `phase1:192` + D5 `:1976-1985` covers import prose AND `doc.go:17-19` falsehood |
| 3 | FE session-long staleness ACCEPTED | **PARTIAL** — substance present (both bounds `phase2:868-872`; risk row amended in place `:625`; "Flagged for the leader" `:872`) but no literal ACCEPTED label or wishlist pointer |
| 4 | v194 Phase-4 comment → parallel text-lane handoff | **VERIFIED** — `decisions:1027-1029` "flagged (not ours to fix…)"; `phase1:2246`; `plan-overview:23` |
| 5 | dcs backlog fresh-verify-at-ship | **PARTIAL** — fresh-verify present `:1131`/`:1361`/R14 `:1335`, but the "8 commits ahead (`b0e98f4`)" figure is unmarked and operative ⇒ folded into **C2-4** |

## D. Cycle-1 dispositioned warnings — status

- **member_skipped → member_suppressed:** ENCODED (`decisions:917-919`; `phase1:1353`/`:1392`/`:1397-1401`/`:1408`; `phase2:840`; `phase3:177`/`:345`). Cosmetic: `phase1:1961` heading renders "member_suppressed → member_suppressed".
- **tsc PIPESTATUS[0]:** FIXED (`phase3:976-978`).
- **E2E AllowedModels pre-check:** FIXED (`phase3:1242` row E4b, before E5; `:337`).
- **stale count prose:** PARTIAL — `phase3:11` ("#23–#27" vs 23–30, `decisions:362-397` + `phase2:877`); `:696` ("Pack A = 41 names" **inside the executable script**; actual 60); `:1341` (R21 "already in the 50"; names 3 withdrawn not 5); `:1388` (R6 names two non-existent pins + false CONFIRMED); `:1390` (R8 old G11 verdict); blanket `:372` covers amendment-section counts only.
- **D-5 leftover:** FIXED (`decisions.md:240` supersession marker confirmed at re-located position).

## 🟢 OPTIONAL IMPROVEMENTS (non-blocking)

Dangling references to dissolved items: `phase1:1327` (`layer_group.go` row unmarked while siblings marked); `:868-869`/`:1843-1844` (three dissolved layer files still listed as to-create); `:2324-2325` ("63 names" superseded by 69); `phase3:77` (AM-1 exported `pkg/grouplb` selector); `:416`; `:1083` (plan-time `origin/master` value); `plan-overview:8`/`:209`/`:231`/`:237-239`. **Advisory doc:** `architecture-recommendation.md:741`/`:778` still recommend `store_group_policy.go` (retracted only at `:884` — violates ADR-17 `decisions:1055-1057` "NOWHERE except as a retracted proposal"); `:303`/`:724` still prescribe RED-1's `FailedGroupID` vs the plan's endorsed drop-and-continue (§7a `:964-990` doesn't record the adjudication). Small: `phase3:627` "adds 1 new function" under-count; `phase1:703` stale sub-cite ":191" (second fn at `:217`); `phase3:145`/`:214` FE counts 46/48; `phase1:1961` typo; `phase2:857-874` add literal ACCEPTED label + wishlist pointer; fold T1–T4 deferral into a named wishlist (`decisions:1104-1107`).

## Ship-time watch (both councilors flagged)

`origin/latest` = `a2952a9`, `origin/master` = `8d434ba` — **both already moved past the corpus base `2b987bf`/`:fd1649c`.** The "base moved" class (cycle-1 CG-4) can recur before ship; the fresh-verify rule must be honored at ship time, and the base-currency check deserves a permanent gate step rather than a one-time re-anchor.

## What is sound (credit where due)

Ruling encoding coherent and mechanically verified; amendment discipline survived a hard pivot; the four RED gaps are engineering, not references; **the reuse re-derivation is load-bearing** (dissolving the three plan-local layer files + the exported grouplb selector in favour of the landed `pkg/skipcache`/`pkg/modelcache`/sweep re-scopes the fence from *instance*-separation to *layer*-separation, with structural proof at `cache_layer_separation_test.go:752-786`); the RED-1 refusal adjudication resolves a genuine tension in the architect's own text in the only direction that does not reintroduce the defect (healthy primary must not 502) — pinned by a named test; ADR-1 settled by real precedent; D-A shadow question closed with evidence (`phase1:2204-2218`); the fail-open hardening (`phase1:1939-1960`) is nuanced rather than blunt.

## Process notes (transparency)

- Both councilors self-corrected under cross-validation (coding: CG-5 FIXED→PARTIAL, stale-count FIXED→PARTIAL; agentic: CG-1 FIXED→PARTIAL, R2 withdrawal of a wrong inference). Refinement rounds changed conclusions.
- One citation slip: the net-churn block is `phase3:577-598` (agentic had cited `:436-438`).
- Governor disclosure: a one-word typo in the Round-2 dispatch header ("READ-ORY"); the prohibition list beneath was verbatim, the councilor executed fully read-only, corrections were busy-guarded. No impact on findings or conduct.

## Recommendations (priority order)

1. **C2-3 + C2-4** — rewrite the operative R11 abort row (`phase3:1332`) to the corrected ladder and extend fresh-verify scope to ALL plan-time deploy/dcs numbers (incl. the dcs "8 ahead" figure that now targets a non-existent backlog). Pure doc fixes; highest stakes (incident-pressure path).
2. **C2-1** — leader picks the doctrine-line pin target (skipcache.go:29-31 vs the architect's no-skipcache-docs-rewrite constraint) and the plan names the real seam at all four sites.
3. **C2-2** — supersession markers on the four unmarked sites; kill the dead `SetSkipCache` reference (`phase1:404`).
4. **C2-5** — add `win_clear_winner_identity_test.go` to the Tier-1 command + fix its cell wording; delete the `group_rules_test.go` Tier-1 row. One-line-class fixes.
5. **C.1** — phase-1 addendum naming T2/T3/T4 (+T5 Go half) as staged test tasks, or an explicit recorded deferral in `decisions.md`.
6. Optional sweep while amending: the dangling-reference list above (esp. advisory `:741`/`:778` vs ADR-17), disposition-#3 ACCEPTED label, stale count prose (`phase3:696` sits inside an executable script — highest-value of the prose items).
7. Ship-time: honor fresh-verify; encode a permanent base-currency gate step (the class already recurred — `a2952a9`/`8d434ba`).

**Bottom line for the approver: CRITICAL GAPS — 5 blocking items + 1 explicit remaining gap (T1–T4), every one a small localized doc fix in the gate/runbook/labeling layer. The ruling set, the four RED-gap encodings, the v194 consumption, and the 69/5/47 test contract are approvable as they stand.**
