Current Plan: token-authz-allowed-groups (AUTH COMMISSION — Token Permission Model: allowed_groups token field + per-layer exclusion + decision-group allowlist fix)
Tracking File: token-authz-allowed-groups-tracking.md
Iteration: 003 (final)
Status: ESCALATED
Mode: Plan Approval — iter 001: 2 workers, REJECTED (6 blocking); iter 002 (frozen c7efdccf): REJECTED (5 blocking; decision-side APPROVED); iter 003 (frozen cb2820c0, gen 7): plan-approval REJECTED (3 blocking, all documentation-contract; landed code verified correct on each) + decision-approval APPROVED (0 blocking) ⇒ REJECTED
Worktree: /home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-token-authz @ 81e8788 (16 commits, 91 files +9623/−890; landed code worker-verified: freeze reproduces, 112-pin ledger literally true, security mechanisms landed, build green); corpus frozen cb2820c0736b4265d5feb4a84a0c3eef2133553d85731e04fd5261c8a498b4a6 (untracked)
Final Verdict: REJECTED x3 (001, 002, 003) — max iterations reached (3), escalated to Leader 2026-11-19.
Unresolved (iteration 003, all doc-contract): I3-1 fail-open scanner semantics restated at plan.md:359/phase1-plan.md:97/adr:441 (contradicts landed INVALID⇒DENY-ALL); I3-2 decisions.md:110-111 Q1 rollback story omits reverse-repartition-first (contradicts §7.1 MUST); I3-3 §12 FE contract describes the rejected one-picker design (ledger #81 + landed FE = two per-axis pickers).
Last Updated: 2026-11-19 (iteration-003 verdict, ESCALATED)
Superseded entry: decision-groups (REJECTED x3, ESCALATED 2026-11-16 — see decision-groups-tracking.md; prior supersede chain: load-balance-groups APPROVED 2026-10-16)
