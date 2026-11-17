# review-plan-1 — Plan Review, feature commission "decision-groups" (llm-supervisor-proxy)

- **Date:** 2026-11-16
- **Mode:** 🔴 Deep-Review — governor council, 2 councilors (`worker` agent, models **agentic** + **coding**), skill `plan-review`; Round 0 full corpus review + 1 targeted refinement round (verbatim-quote adjudication). Read-only compliance clean for both councilors.
- **Corpus reviewed (frozen):** branch `feature/decision-groups` @ `24c51ef`, worktree `llm-supervisor-proxy-wt-decision-groups` — decisions.md (902 ln), phase1-plan.md (1831 ln), phase2-plan.md (798 ln), phase3-plan.md (1201 ln), plan-overview.md (316 ln), research-{groups,decision,fe-precedent}.md; advisory architecture-recommendation.md (not plan of record).
- **Session IDs:** governor `fac8c958-3347-464b-8207-b98e9421732a`; councilors agentic `8aefd01f…0f53`, coding `4bb0b56e…9994`.
- **Reviewer:** ensemble reviewer[v2] (controller/aggregator — all findings produced by the council; file:line citations verified in-worktree by councilors).

---

## VERDICT: CRITICAL GAPS

Unanimous and cross-validated. **The ruling/design layer (D-1..D-8, ADR-1..ADR-14) is coherent and faithfully encoded** — verified mechanically and independently by both councilors: 59-name Go test set identity across phase1↔phase3, 48 FE cases consistent, all six ruled semantics coherent, decorator-forward enumeration complete at `24c51ef`. **The blocking defects are concentrated in the phase-3 gate/runbook layer and base currency**: 5 critical gaps, 16 warnings, 7 advisory contradictions. All fixes are doc amendments + rebase re-anchoring — **no redesign required**.

---

## 🔴 CRITICAL GAPS (blocking — amend before approver sign-off)

### CG-1 — Phase-3 Tier-1 gate self-contradicts on `handle_models_imggen_test.go`
- **Evidence:** `phase3-plan.md:407-418` demands a whole-file byte-identical diff for a file Phase 1 *must edit* — `phase1-plan.md:575` adds `TestHandleModels_FilterDecisionGroup` there (blast-radius row 22, `decisions.md:336`; corroborated by phase3's own rows `:397`, `:471`, and the G4 name list `:594`).
- **Why blocking:** a compliant implementation either violates G4 or trips the STOP — the gate is unsatisfiable as written.
- **Fix:** demote to a Tier-2 per-function byte-identity check (recipe already present as advisory at `phase3-plan.md:498-503`).
- **Attribution:** coding #2 ≡ agentic CG-1; undisputed.

### CG-2 — Skip-cache instance: internal plan-of-record contradiction (R5 unmarked)
- **Evidence:** `phase1-plan.md:397` (Stage 4.8): "the **same** skip-cache instance the manager reads … otherwise the marks would never demote"; second unmarked site `phase1-plan.md:464` (risk **R5**): "wires the **SAME** `upstreamSkipCache` instance into the manager and the decision handler" — vs binding Amendment 1 §A5 `phase1-plan.md:1306` + `:1562`/`:1571` and `phase3-plan.md:75`/`:104`/`:1192`: "A separate instance … never shared" (ADR-9 byte-untouched fence).
- **Why blocking:** `:397` is banner-covered (`phase1-plan.md:283-288`), but **R5 `:464` is unmarked, prescribes concrete wiring, and none of the 59 tests pins instance separation** — an implementer following R5 passes every named gate while opening the cross-lane mark-leak that AM-2 exists to prevent. Exactly the corpus's twice-burned drift class (14-vs-16 count; all-marked contradiction).
- **Fix:** supersession marker on R5 + add an instance-separation pin test.
- **Attribution:** coding critical #1; agentic Round-1 conceded and sharpened the factual predicate. Severity divergence adjudicated **blocking** (see divergence table).

### CG-3 — G11-a runbook verdict is self-contradictory and deterministically self-fails
- **Evidence:** `phase3-plan.md:345` / `:370-372`: empty `git status --porcelain pkg/ui/static` ⇒ "G11-a FAIL … stale embedded UI; exit 1". But P0.1 (`:280`) requires the tree **committed** pre-gate, and deterministic vite rebuild (corpus's own premise, `phase2-plan.md:552`, `:805-808`) reproduces committed content-hashed assets ⇒ porcelain is **empty** ⇒ self-fail; step 9's own comment (`:810`) treats the same observation as "zero-mod proof ✅".
- **Fix:** collapse to a single verdict rule; G11-b (same-commit pairing, `:373-377`) + step-9 sha256 proof suffice.
- **Attribution:** agentic CG-4; coding Round-1 CONCUR with independent line verification — cross-validated.

### CG-4 — v194 base move unacknowledged anywhere in the plan of record
- **Evidence:** corpus-wide grep: **zero** occurrences of `2b987bf`/`fd1649c`/rebase/v194; all five plan-of-record docs still assert `24c51ef == origin/latest` (`decisions.md:3`; `phase1-plan.md:8-9`; `phase2-plan.md:6`; `phase3-plan.md:5-6`; `plan-overview.md:8`) — now false (v194 cache-layer-separation landed at master `fd1649c`, latest `2b987bf`).
- **Unencoded consequences:**
  - Every BASE-relative line-pin is stale; the skip-cache ordering pins at `phase3-plan.md:399` are re-anchored and **semantically inverted** at `2b987bf` (advisory `architecture-recommendation.md:381-386`).
  - "Text-lane byte-untouched" must be redefined as "no v194 semantic regression".
  - ADR-9 collision-hotspot list (`decisions.md:701-715`) is missing v194 files (`pkg/modelcache/`, `race_executor.go`, `cache_layer_separation_test.go`).
  - Ship Step 1's merge absorbs v194's text-lane changes **after** local gates, with the full-gate re-run trigger tied only to *conflicts*, not clean merges (`phase3-plan.md:879-884`).
- **Fix:** execute the mandatory rebase `24c51ef → 2b987bf`; re-anchor all BASE-relative pins; redefine the untouched-fence; extend ADR-9 hotspots; re-trigger full gates on the merge regardless of conflict.
- **Attribution:** agentic CG-2; coding held the same facts at warning severity — divergence adjudicated **blocking** (gate methodology is BASE-relative diffing; approving it pinned to a dead base fails the feasibility bar; the dispatch itself marks the rebase mandatory).

### CG-5 — Deploy/rollback facts one release stale; encoded ladder would strip v194 from the critical substrate
- **Evidence:** `phase3-plan.md:1060-1067` hard-codes rollback = **v193 only** (helm rev 36 / `:b11d7c8`); R11 repeats it (`:1125`). Live state: daoduc rev 37 / primary gen 34 @ `fd1649c` — the encoded ladder is a **two-release rollback** reverting cache-layer-separation. Same class: expected daoduc rev 38 not 37 (`:993`/`:997`), primary gen 35 not 34 (`:1023`), CI **#195** not v194 (`:915`, `:1152` — #194 is taken by `fd1649c`), dcs synced @ `7f89eac` not 8-ahead (`:938`, `:1154`). `:1150`'s "re-read before deploy" mitigates execution but not the encoded R11 abort target.
- **Fix:** tier-1 rollback = `:fd1649c` / `helm rollback 37`; tier-2 = v193; refresh rev/gen/CI/dcs numbers throughout phase3 + R11.
- **Attribution:** agentic CG-3; coding warning #5 — same facts; divergence adjudicated **blocking** (rollback correctness is precisely what the approver signs on the ensemble's critical substrate).

### Severity-divergence record (transparency — facts never in dispute, only severity)

| Item | coding | agentic | Governor ruling |
|---|---|---|---|
| CG-2 skip-cache internal contradiction | 🔴 | 🟡 blocking-adjacent | **Blocking** — R5 unmarked + no pin; operative semantic unambiguous, fix cheap |
| CG-4 base-move unacknowledged | 🟡 | 🔴 | **Blocking** — BASE-relative gate methodology; rebase mandatory |
| CG-5 stale rollback ladder | 🟡 | 🔴 | **Blocking** — critical substrate; encoded R11 target wrong |

All three rulings err toward blocking, consistent with the project's criticality posture.

---

## 🟡 WARNINGS (non-blocking; fix before ship)

1. Fail-open `groupLaneOf` fallback (`phase1-plan.md:384`) + missing compile-time `var _` assertion / decorator-through effect pin.
2. Crowded "skip" outcome namespace — `member_skipped` vs 68 v193 hits; `model_fallback_*` taken by v194; rename e.g. `member_suppressed`.
3. ADR-1 scope: `shadow_model_id` retyping hole + 409-vs-4xx — pending leader decisions.
4. CI recovery wording: `workflow_dispatch` is first-class (`docker-ci.yml:6-11`) vs "retry rebuilds nothing" (`phase3-plan.md:925`); tsc gate `| tee` swallows exit code — use `PIPESTATUS[0]`.
5. E2E traps: (a) token `AllowedModels` 403 false-negative needs a pre-check before E5; (b) all-marked case **not exercisable on primary** (`.invalid` transport ≠ limit-class) — keep it in the local harness.
6. E2E group delete-vs-keep is encoded as an exit criterion while still a pending leader decision.
7. Stale count prose beside authoritative tables (33/41/50 vs 59; "FE #23-27" vs `decisions.md` 23-30).
8. Duplicated FE-updated-files heredoc (`phase3-plan.md:648-667`).
9. `doc.go:17-19` "one upstream attempt, no retry/fallback" becomes **false** under this commission — extend Stage-1 task 1.6 acceptance.
10. All-credentials-**cooling** case has no OQ-1-style honest-attempt rule — needs an explicit ruling before AM-4 rotation lands (open question for the leader).
11. FE lane-consistency guard: unknown-lane groups uneditable in **both** tabs — repair degenerates to delete+recreate (`phase2-plan.md:239-243` vs `:330-336`).
12. FE fixed-constants copy promises "race chain" fallback — false on the decision lane (OQ-3); lane-gate the line.
13. Vacuous FE tests B8/D7 assert absence of a never-existing field.
14. 15s TTL staleness understated — live session never refetches; state both bounds.
15. D-5 leftover at `decisions.md:240` still cites the superseded sweep rule vs ADR-11 policy-head — the one residual ruled-semantic drift.
16. Provider-vocabulary proof lacks a registry↔gate set-equality pin (T5) — the "NO vocabulary change" claim is asserted, not fully pinned.

---

## ADVISORY contradictions vs ADRs (flagged, NOT adjudicated — ADRs govern)

1. Instance binding: advisory W5 "reuse-and-bind" vs plan AM-2 "separate instance" (resolution is base-dependent — re-examine post-rebase).
2. Pick seam: advisory `store_group_policy.go` + closure injection vs phase1 A2 exported `pkg/grouplb` selector.
3. CI recovery path (warning 4).
4. Secondary rename-proof marker constant.
5. ADR-1 shadow-model scope.
6. ADR-1 status code 409 vs 4xx.
7. Advisory W9 "ADR-1 nowhere" row is **stale** — phase1 Amendment 2 §B1 (`phase1-plan.md:1610-1646`) folds it; treat the corpus as authoritative there.

---

## Review-checklist coverage map

| # | Checklist item | Outcome |
|---|---|---|
| 1 | Completeness | Substantively complete — backend/FE/tests/runbook all present and traceable; defects are contradiction/staleness (CG-1, CG-3, CG-5), not omission. FE residuals = warnings 11-14. |
| 2 | Ruling coherence | Mechanically verified across all five docs; **planner's "zero open items" is FALSE as to internal consistency** (CG-2 unmarked R5) and has one residual ruled-semantic drift (warning 15, `decisions.md:240`). Otherwise upheld. |
| 3 | Feasibility | Sequencing + dependencies sane (consensus); decorator-forward enumeration **complete** at `24c51ef` (`GroupLane` the only new ModelsManager method) — must be **re-verified post-rebase** (v194 touched modelscache). Base-currency feasibility failure = CG-4. Partially-evidenced sub-points carried as explicit re-verification items for the amended corpus: D-2 negative-control + would-have-caught-v193 tests; AM-3 P0.9 both-guard-layer recipe (lane-inference seam itself was reviewed — warning 1). |
| 4 | Regression protection | Pins present (Tier-1/Tier-2 machinery, `phase3-plan.md:397-471`); sufficiency **degraded by CG-4** (BASE-relative pins stale; ADR-9 hotspot list missing v194 files; conflict-only re-run trigger `phase3-plan.md:879-884`). |
| 5 | Test-plan quality | Sound: bidirectional anti-vacuous diff, `-run` top-level-name trap respected, known flakes quarantined per `.agents/tester/QUARANTINE.md`, FE gates (vite sha256 zero-mod, tsc count+file-set). Residuals: warnings 7, 13, 16. |
| 6 | Risk register | Gaps/underweights found: warnings 1, 2, 5, 10, 11, 14 + the CG-4/CG-5 staleness class (base/deploy facts need a currency-check step in the runbook). |
| 7 | Advisory | 7 flags above; ADRs govern; advisory W9 itself stale. |

---

## What is sound (consensus — for the approver's context)

Ruling encoding coherent and mechanically verified; amendment discipline survived a hard pivot with only the D-5 leftover (`decisions.md:240`); E2E design exemplary (throwaway-member probe, before/after credential diffs, "failover not observed ≠ rollback"); gate engineering real (bidirectional anti-vacuous diff, net-churn verification, HOME quarantine, hermetic pack slices); decorator-forward enumeration complete at `24c51ef`; quarantine conventions followed; `-run` trap respected.

---

## Recommendations (priority order)

1. **Execute CG-4 first** (rebase `24c51ef → 2b987bf` + re-anchor + fence redefinition + ADR-9 extension + unconditional full-gate re-run on merge) — it re-anchors everything the other fixes touch.
2. Amend CG-1 (Tier-2 per-function check), CG-3 (single G11 verdict rule), CG-5 (rollback tier-1 = `:fd1649c`/helm 37, tier-2 = v193; refresh rev/gen/CI/dcs numbers).
3. CG-2 fix: supersession marker on R5 (`phase1-plan.md:464`) + new instance-separation pin test.
4. Leader decisions required before the affected stages: ADR-1 `shadow_model_id` scope + 409-vs-4xx (warning 3); E2E delete-vs-keep exit criterion (warning 6); all-credentials-cooling honest-attempt rule (warning 10, before AM-4 rotation).
5. Cheap drift-hygiene sweep while amending: warnings 7 and 15 (count prose, D-5 leftover) — same class as the corpus's two prior drifts.
6. On the amended corpus, re-verify: decorator-forward enumeration post-rebase; D-2 negative-control + would-have-caught-v193 tests; AM-3 P0.9 both-guard-layer coverage; provider-vocabulary set-equality pin (warning 16).
7. Pre-ship renames/checks: `member_skipped` → `member_suppressed` (warning 2); E2E token `AllowedModels` pre-check (warning 5a); all-marked case stays local-harness-only (warning 5b).

**Bottom line:** CRITICAL GAPS — the architecture/ruling layer is approvable; the phase-3 gate/runbook layer and base currency are not. All fixes are doc amendments + rebase re-anchoring, not redesign.
