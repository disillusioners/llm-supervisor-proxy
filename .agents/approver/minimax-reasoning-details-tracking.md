# Tracking: MiniMax reasoning_details translation

Plan: .agents/shared/planning/minimax-reasoning-details/ (plan-overview.md + 5 supporting docs, 1,333 lines)
Repo: llm-supervisor-proxy @ feature/minimax-reasoning-details (ac877ea), implementation not started
Approval type: Plan approval (large multi-section plan → 2 parallel workers)

## Iteration 001 — 2026-08-19

Workers:
- approve-worker-plan (7ae648b9-a57e-41d2-9925-fee882277d9e) — full plan sweep — plan-approval — APPROVED, 0 blocking, 10 notes
- approve-worker-compat (ddbb8e5d-fe93-4f27-be38-23906383af91) — backward-compat invariant + test plan — plan-approval — APPROVED, 0 blocking, 7 notes

Aggregate verdict: APPROVED (no blocking issues from any worker; notes deduplicated below)

Notes carried forward (non-blocking, post-approval observations):
1. Stale mermaid diagram in technical-analysis.md (lines 99-110) contradicts binding D2 (pre-dates decisions; phase plans correct)
2. Stale construction-point table in architecture-recommendation.md §3.3 (pre-D2; binding §8 row correct)
3. Phase 2 task count in plan-overview.md line 64 says 9, phase2-plan.md has 10 (P2-1..P2-10); total should be 27 not 26
4. phase1-plan.md Risks line 54: H1 title says "ultim-ext" only, mitigation column correctly says BOTH external paths (title drift)
5. plan-overview.md line 156: typo "Flag ONtest:" (readable from context)
6. AM-R3 layering inversion: pkg/providers/openai.go imports translator.extractEntryText helper-only — annotation comment required at import site
7. Openai.go internal-path gate is implicit at the caller (P2-1); byte-identical tests would catch misimplementation — spec could be more explicit
8. Gate-on non-stream + non-JSON body → HTTP 502 (new behavior for opted-in clients only; does not touch the invariant)
9. Error-path (upstream 4xx/5xx) translation behavior under gate-on not enumerated in plan docs
10. "Non-MiniMax upstream never emits reasoning_details" is an assumption, not a guarantee — runtime contract
11. Internal paths intentionally do NOT strip x-proxy-interleaved-thinking (header never reaches MiniMax via internal abstraction) — plan could state explicitly
12. Live MiniMax wire-format verification (Q1/Q2/R9 — cumulative vs incremental) deferred to post-merge P3-6 acceptance gate; plan ships safe defaults
13. P3-3 AST drift-trap test deferred: P1/P2 can merge before the trap exists; drift in the gap would not be caught by their exit gates (documented AM-10/AM-R3)
14. Drift trap scans composite literals only; inline map mutation outside translator funnel would not trigger
15. Mock harness CI integration for P3-5 currently a known gap pending P3-4 work
16. Dormant twin C (internal_handler.go:272) de-scoped but not removed; drift trap does not scan it — follow-up cleanup
17. No explicit race-retry fixture with reasoning enabled; per-stream instance is retry-safe by construction (H6)

Final status: APPROVED (iteration 001)
