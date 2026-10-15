# Tracking: decision-model-type

Artifact: .agents/shared/planning/decision-model-type/plan.md (1128 lines, 13 sections + App A/B) + decisions.md (Q1-Q18)
Mode: Plan Approval — 2 section-partitioned workers (large-plan exception), skill `plan-approval` each
Stated goal: third model kind "decision" — POST /v1/decisions (OpenAI Decisions wire client-side), OpenAI pass-through + TypeSafe/Jev bidirectional System-One translation, FE Decision Models tab, zero DB migrations (unconstrained kind column). PLAN-ONLY, pending end-user approval.

## Iteration 001 — 2026-10-15

Workers (fresh-context dispatch, no tracking history passed):
- approve-worker-backend (154c36cb-cf0a-4907-bd60-c4000f251539) — lens: §2-5, §7-8, App A. Verdict: APPROVED, 0 blocking, 18 notes. Skill feedback recorded (skill 314cf403). Code-anchored verification: /v1/messages lacks IsImageGen AND IsDecision guards (handler_anthropic.go — live money-burning gap, plan site 8 closes it); chat-arm leak at server.go:766 (!m.IsImageGen() only — plan site 7 fixes); zero-migration justified (kind TEXT unconstrained, ValidateKindRules sole gate); all 5 kind-system touch points covered.
- approve-worker-fe-process (cece5605-6625-42c4-be44-398cfa20ebbb) — lens: §6, §9-13, App B, decisions.md approval surface. Verdict: APPROVED, 0 blocking, 7 notes. ~25 file:line anchors spot-verified, all accurate. Approval surface: 18/18 forks defaulted + priced overrides + mechanical reply protocol.

Aggregate verdict: **APPROVED** (no blocking issues from any worker; no conflicts; no downgrades needed).

Key non-blocking notes carried to planner:
1. decisions.md Q9/Q14 are the same fork surfaced twice (defaults agree; mark Q9 superseded-by-Q14 or merge).
2. §6.1 tab emoji ("🎯 or ⚖️ — TBD by user") is an orphan TBD not in the decision log.
3. §10 P1 exit criterion over-promises typesafe (validProviders gate lands at P2.1; defer typesafe clause to P2 exit).
4. §9.1 mis-homes four /fe/api/models tests under pkg/decision; §9.2 (handlers_models_kind_test.go) is canonical.
5. §11.1 feature-flag rollback aside contradicts §8.3 no-env-flag posture; primary rollback path (delete kind='decision' rows pre-revert) is correct.
6. §3.1 site 5 wording: two-branch structure (not boolean disjunction) needed to keep distinct misroute error codes; name the reverse-direction error code (decision_misroute vs decision_kind_mismatch).
7. Shared-credential implication of provider identity B (same OpenAI key serves chat + decision) worth one line in FE/security guidance.
8. Token-level kind attribution gap (§7.3 v1 limitation) and global-ultimate model_id kind-gate (§6.7.a deferred) — record follow-up commission triggers.

Prior rejections this slug: none (iteration 001 approved on first pass).
