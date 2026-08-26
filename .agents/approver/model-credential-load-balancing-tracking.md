# Approval Tracking: Model-Credential Load Balancing

Plan: .agents/shared/planning/model-credential-load-balancing/plan-overview.md (+ decisions.md, technical-analysis.md, phase1-5-plan.md)
Skill: plan-approval (single worker, fresh-eyes)

---

## Iteration 001 — 2026-08-21 12:0x — VERDICT: APPROVED

Worker: approve-worker-plan (a22e4de3-a4a3-4303-a3a9-6c9c0857c0af)
Worker verdict: APPROVED — 0 blocking issues, 8 non-blocking notes.

Verified: requirements coverage (per-model weighted LB across same-provider
credentials, weight default 1, first-request-only randomization, conversation
stickiness via first-message hash, single-credential fast path unchanged),
cross-doc consistency (amendment records resolve contradictions), feasibility
(file:line claims spot-checked against handler.go, race_executor.go, store.go),
safety (migration 028 gated by PG test, rollback lossless, provider-match
enforced at 3 layers, engine ownership pinned to ModelsManager).

Non-blocking notes carried forward (for implementer, not gating):
1. Migration 029 (DROP INDEX/COLUMN legacy credential_id) must be filed as a
   tracked issue BEFORE the PR opens, not "at merge time" — deprecation clock.
2. Risk #7 mitigation thin: add CI grep-guard for legacy ResolveInternalConfig
   call sites (pattern already used for W-3).
3. Within-principal templated skew accepted for v1; revisit if large same-token
   templated fleets become common.
4. Phase 4: add test asserting legacy credential_id field is ignored when
   credentials present in same payload.
5. Cross-instance (Redis) affinity sharing is v2 — document in README/CHANGELOG
   for multi-proxy operators.
6. One-line doc note: race-external (UpstreamCredentialID env) traffic is not
   LB'd across credentials.
7. architecture-recommendation.md not read by worker; amendment record in
   plan-overview.md treated as operational source of truth (no contradiction found).
8. PG jsonb semantic-compare equivalence for migration 028 not independently
   verified; gated by mandated test.

Status: APPROVED (iteration 001). No further approver action.

---

## Iteration 002 — 2026-08-25 20:2x — VERDICT: APPROVED (re-approval, expanded scope)

Scope reset note: prior APPROVED verdict (iter 001, 2026-08-21) covered the
original 3-requirement scope. Caller re-submitted with a NEW requirement #4
(rate-limit credential failover BEFORE model-switching fallback + per-credential
cooldown), post Round 3/3b/3c plan amendments. Re-approved fresh at iteration
reset 001→logged here as the tracking file's second entry; active.md reset to
001 for this cycle.

Skill: plan-approval (2 section-parallel workers, fresh-eyes — large-plan
exception: ~8.3k lines / 11 files)

Worker A: approve-worker-overview (d2c3ceb4-9635-4a34-a785-f18c464c1f7e)
  — plan-overview.md + decisions.md + architecture docs
  Verdict: APPROVED — 0 blocking issues.
  All 4 requirements verified addressed (R1 §B/phase1 T6; R2 §A token-salted
  key + GetOrSelect sliding TTL + E-3; R3 §D fast path + M-1 + Phase5 golden
  tuple; R4 §F F.1-F.5 + R3-1..R3-8). Citations verified against 8f67bdf.

Worker B: approve-worker-phases (21bf384f-6fa5-4fd0-8692-4fd27083fa15)
  — phase1-5-plan.md + technical-analysis.md
  Verdict: APPROVED — 0 blocking issues.
  Requirement #4 implementation verified: Round 3c hoisted admission gate
  (gate := modelAttempts < len(models) || credFailoverEligibleWithBudget())
  at race_coordinator.go:338 window gate fixes the Round-3b dead-loop in the
  3-cred/1-model/no-fallback-chain core scenario. B2 idempotent rebind,
  ReselectNone/ReselectSoonestExpiry unified mapping (C2), retry budget,
  cooldown clearing on credential delete (S6) all pinned with grep-gates.

Aggregation: 0 blocking issues across both workers → APPROVED.

Deduplicated non-blocking notes carried forward (for implementer, not gating):
1. [Both workers] CI Postgres availability for the MANDATORY PG-gated 028
   transaction test (//go:build pg_integration) is unverified infra —
   load-bearing for the rollout reviewer; run pre-merge.
2. [Both workers] E2E mock capability (--hit-counter-file/--credential-identity/
   --fail-429-once) is built in Phase 5 Task 23 (REWRITE) — original
   --hit-count-file premise did not exist at HEAD; build-tag precedent verified.
3. [A] 8.3k-line doc set: final grep-gate consistency pass recommended at
   Phase 5 exit (contract = technical-analysis.md as tiebreaker).
4. [A] Migration 029 deferred to tracked issue filed at merge time (confirmed
   as intended scope, matching prior iteration note #1).
5. [B] Engine is in-memory: restarts break affinity for 24h × restart window
   (accepted decision C; operator note in Phase 5 Task 47).
6. [B] Rate-limit error-envelope shapes verified for MiniMax mapping only;
   vocabulary table not exhaustively checked across all 7 brands.
7. [B] Compile-sweep breadth: CredentialID: grep now 249 hits incl. fixtures
   (was 137 at measurement); TestRefs fixture factory is the mitigation.

Status: APPROVED. No further approver action.
