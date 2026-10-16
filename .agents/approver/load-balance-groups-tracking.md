# load-balance-groups — Approver Tracking

Plan: load-balance-groups (virtual models as load-balancing groups) — PLAN-ONLY commission
Artifact: .agents/shared/planning/load-balance-groups/ (plan.md 706L primary; decisions.md 381L Q1–Q20 companion; research-codebase.md / architecture-recommendation.md supporting)
Worktree: /home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-lb-groups-plan (feature/load-balance-groups @ 5b13388)
Plan-only constraint verified at dispatch: git status porcelain shows only the untracked planning dir — zero source changes.

## Iteration 001 — 2026-10-16 — APPROVED (final)

Mode: Plan Approval (`plan-approval`) × 2 workers — large-plan exception (>500L multi-module), section-partitioned, cold-context dispatch.

Workers:
- approve-worker-plan (f2744849-d5ee-40b6-8f88-c552d8d6fab6) — plan.md core. Verdict: APPROVED, 0 blocking, 7 notes. Independently re-verified ~40 load-bearing anchors against source @ 5b13388, including both red architect amendments (Am1 internal_handler.go:240/:244; Am3 metering sites :908-934/:1446-1472/:1656-1682), storage arithmetic (SELECT 20→22, write args 19→21, pins), migration registry (030 @ migrate.go:63), credentiallb clone seams, FE anchors (9 useApi.ts mutation sites, types.ts:123/:155/:161).
- approve-worker-decisions (933363f2-1bf4-40ee-bc6f-a2ec6a811f58) — decisions.md Q1–Q20 + plan↔decisions cross-consistency. Verdict: APPROVED, 0 blocking, 5 notes. 20/20 defaults match plan body; USER-DECIDED marks consistent with mid-flight scope guidance; 7 load-bearing citations independently verified in source.

Aggregated verdict: APPROVED — 0 blocking from all workers; 12 non-blocking notes (execution-time polish; none alters design, phase ordering, or acceptance criteria):
- Anchor precision: dbModelRow store.go:467 (cited :455-484); :884→:888; SELECT starts :857/:939
- Am10 wording ("pure compute" vs GetOrSelect binding writes — no-new-cycle conclusion stands)
- Appendix A "~45 files" vs manifest ~60 items (~37 non-test); LOC arithmetic consistent
- §3.8 disabled-shadow-target corner not enumerated (defensible; one sentence)
- §3.4 /v1/models hide mechanism underspecified (interface-constrained scan approach)
- "Resolved/resolution" wording on approval-pending rows (§4 Q5, §3.2 Q2)
- decisions.md line 11 "Q17–Q20 pending" scope ambiguity (all 20 are pending)
- N14/N15 identifier collision between plan Appendix B and decisions.md amendment history
- Q16/§3.6.5 Anthropic-side Am1 behavior for fallback-reached groups unstated
- decisions.md line-count nit (382 vs 381)

Next: plan package ready for user go/no-go on Q1–Q20 (decisions.md). No approver iterations consumed beyond 001.

---

## Iteration 002 — 2026-10-16 19:11Z — REJECTED (1 blocking)

Trigger: caller-requested fresh re-run on the amended Round-2 working tree (plan.md 706→747L, decisions.md 381→421L; baseline c6e0fd5 + uncommitted amendments ~+461/−289 across the three live docs). Caller instructed append, not overwrite, of iteration 001.

Mode: Plan Approval (`plan-approval`) × 2 workers — large-plan exception, section-partitioned, cold-context dispatch (no iteration/pivot history passed).

Workers:
- approve-worker-plan (7e5cb761-390b-4d29-b12d-78bb22a4abaa) — plan.md core. Verdict: REJECTED, 1 blocking, 4 notes. Re-verified ~40 load-bearing anchors @ 5b13388+c6e0fd5 (kind system, store gates, migration registry 030 @ migrate.go:63, modelList build twins handler_functions.go:149-159 ≡ handler_anthropic.go:210-219 byte-identical, metering sites, conv-key group-salting, modelscache eviction, FE anchors, querybuilder 19-arg pins). Plan-only posture verified: all phases pending; worktree diff touches only planning files.
- approve-worker-consistency (3166bfa5-0755-4cbf-8a07-12cd80391fc0) — package cross-consistency + decision coverage + factual grounding. Verdict: APPROVED, 0 blocking, ~10 notes. 25+ anchors verified; Q1–Q21 all answerable or properly superseded; Am21–Am31 folds coherent; phasing P1→P4 strict-order DAG.

Aggregated verdict: REJECTED — 1 blocking (Worker A), upheld.

BLOCKING 1 (Worker A; upheld — internal contradiction in the authoritative decision surface):
- plan.md §13 Q11 (line 531) retains superseded Round-1 wording: question "Dangling member refs (credential deleted underneath a member)" + recommended default "credential delete is refused while any group member references it (ErrCredentialInUse-family…)" — contradicts post-pivot design §3.4 Am27 (:168), P1.8 (:418), §9.3 (:369): guard is on model deletion (DeleteModel refuses while the model ID is any group's member, error naming the group); Round-1 credential-side scan explicitly removed. Review note C5 (:730) claims the rewording happened; the cell does not contain it. §13's Recommended-default column is declared authoritative and mirrored verbatim into decisions.md → the stale cell would misdirect the user Q&A, this commission's immediate next step. Fix: one-cell rewrite (Q11 question + default → Am27 DeleteModel-scan phrasing); no design change. Check the decisions.md Q11 mirror for the same stale phrasing.

Conflict adjudication: Worker B's cross-artifact table mapped Q11↔Am27 consistent at concept level (no cell text cited); Worker A quoted the cell verbatim with line refs → kept the most specific variant (Worker A). Recorded, not silently dropped.

Notes (non-blocking, deduped):
- Uncommitted working-tree amendments (~+461/−289) — commit the reviewed package before implementation (Worker B).
- P1.5/§5.1 "single production caller": validateBeforeWrite has two production call sites (store.go:1351, :1438); rationale inverted; latent bulk ModelsManager.Validate() path (store.go:1561, zero non-test callers today) uncovered for group rows — one clarifying sentence advised (Worker A).
- Appendix A duplicate pkg/ultimatemodel/ NOT-changed bullet (:625-626) (Worker A).
- §9.4 "5 named cases" vs N21 "Am29 equivalence ×4" (:609) residue — 1+4 breakdown is correct (Worker A).
- Round-1/Round-2 supersession banners (Q14/Q18, §3.2/§3.5/§3.6.x) can mis-read as live design to unfamiliar readers — acceptable audit lineage (Worker B).
- Anchor drift ±1 line everywhere checked — below noise floor for an implementation plan (Worker A).

Next: fix §13 Q11 cell (+ decisions.md mirror if stale); resubmit for iteration 003. Rejections consumed: 1 of 3.

---

## Iteration 003 — 2026-10-16 19:26Z — APPROVED (final)

Trigger: resubmission after the iteration-002 fix pass (B1 §13 Q11 cell + decisions.md mirror rewritten to the Am27 DeleteModel member-scan; N1 validateBeforeWrite rationale corrected to two call sites + bulk-Validate latent-gap note; N2 duplicate ultimatemodel bullet removed; N3 counts reconciled to "5 named cases (1+4)"). Artifact: plan.md 752L, decisions.md 420L, research-codebase.md 775L (working tree, baseline c6e0fd5).

Mode: Plan Approval (`plan-approval`) × 2 workers — large-plan exception, section-partitioned, cold-context dispatch (no fix history passed).

Workers:
- a2f44ce4-eb67-4291-8d56-e5a120e704b9 (instance name approve-worker-consistency-i3; executed the plan.md-core scope) — APPROVED, 0 blocking, 5 notes. ~60 anchors verified @ 5b13388 (kind constants, ValidateKindRules default arm, modelList build twins, metering sites :934/:1472/:1682, validateModelAgainstCredentials Internal gate :1597-1604, validateBeforeWrite :1736 with BOTH production callers :1351/:1438, registry 030 @ migrate.go:63 + trap comments, querybuilder pins 19, credentiallb Engine/key.go/survivor-filter, modelscache negByID :375, FE anchors incl. exact DecisionModelForm 527L / MultiCredentialEditor 225L). Plan-only fence verified: git diff touches only planning files; pkg/grouplb + migration 031 absent; all phases pending.
- 521d2d87-73a0-47db-a568-8594eaa45e22 (instance name approve-worker-plan-i3; executed the package-consistency scope) — APPROVED, 0 blocking, 12 notes. §13 ↔ decisions.md mirror verified cell-for-cell (numbering, question text, recommended defaults, USER-DECIDED locks on Q2/Q3/Q4/Q7-flat); Q11 fix confirmed landed in BOTH files (DeleteModel member scan, error naming the group; credential-side scan explicitly gone; audit-log line :746-747 consistent); migration 031 next after 030; 9 FE mutation sites counted; auth IsModelAllowed + ComputeConversationKey + conversation-key group-salting anchors verified.

Aggregated verdict: APPROVED (final) — 0 blocking from both workers; no conflicting findings; both independently verified the iteration-002 blocker resolved and the plan-only fence intact.

Notes carried (non-blocking, deduped):
- modelList-build anchors off by one (handler_functions.go cited 149-159, actual build 150-159; handler_anthropic.go cited 210-219, actual 209-219) — cosmetic; Appendix C I17 already records the class.
- Appendix A arithmetic loose: "≈3,500 LOC / ~33 files" excludes the 23 test files from the file count (true item total ≈53-58); LOC components sum honestly; indicative only.
- Appendix C Am29 log-line residue "four equivalence tests" vs reconciled live text "5 named cases (1+4)" — historical log only.
- §12 A13 malformed trailing table cell (Round-2 splice class; renderers truncate; cosmetic).
- R1/R2/R13 retain Round-1 mitigation text under explicit MOOT/audit markers — audit trail, not live instructions.
- G8 "is not the group's own ID" clause vs "self-shadow is LEGAL" momentarily reads contradictory; §3.4/Am22 identity rule resolves it (claiming the group's own member ID = legal; claiming the group's own ID = banned) — implementer onboarding note.
- tsc baseline file and named test files intentionally unverified — implementation-time artifacts; PLAN-ONLY posture correct.

Rejections consumed: 1 of 3 (iteration 002 only). Final: plan package APPROVED for user go/no-go on decisions.md Q1-Q21. Lineage: 001 APPROVED (pre-pivot artifact 706L/381L) → 002 REJECTED (§13 Q11 stale Round-1 wording) → 003 APPROVED (final).
