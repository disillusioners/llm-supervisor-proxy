# Approach Comparison: Credential-LB Plan Variants (Council-Synthesized)

Date: 2026-08-21
Source: governor council (`trade-off-analysis` skill; models `agentic` + `coding`; both
completed, independently scored; identical winner and ranking). Full context in
`architecture-recommendation.md`.

## Matrix

| Approach | Complexity (20%) | Scalability (20%) | Maintainability (25%) | Risk (20%, inv.) | Cost (15%, inv.) | Weighted Total | Recommendation |
|----------|------------------|-------------------|-----------------------|-------------------|-------------------|----------------|----------------|
| (i) Plan as-is (destructive DROP in 028, unsalted key) | 4 / 3 — fewest moving parts | 3 / 4 — engine scales fine; skew risk under templated load | 3 / 3 — single source of truth, but five internal doc contradictions | 2 / 2 — irreversible column drop; lossy rollback; old-binary breakage | 5 / 4 — one migration, no shadow writes | **3.30 / 3.15** | Reject — Risk axis (inverted) is decisively worst |
| **(ii) Plan + non-destructive migration (M-1) + token-salted key (A-1)** | 3 / 2 — derived-shadow writes add ~3 lines | 3 / 4 — same engine; A-1 restores weight distribution under templated load | 4 / 4 — graceful rollback; deprecation window; docs reconciled | **4 / 4 — old binary degrades (no crash); lossless down-migration; tooling keeps working** | 4 / 3 — one extra migration (029) later | **3.60 / 3.45 ✅** | **Adopt** — wins on the heaviest axis (Maintainability 25%) and on Risk from both models |
| (iii) Plan + restart-persistent bindings (DB table) | 2 / 2 — bindings table + startup load + write path | 4 / 3 — survives restarts | 2 / 3 — first join table (contradicts §B); stale-binding edge cases | 2 / 4 — new failure modes (stale bindings after downtime) | 2 / 2 — per-first-selection UPSERT + migration | **2.40 / 2.85** | Reject — dominated; restart cost is a cache-miss burst, not a correctness break; cross-instance affinity is v2/Redis |

Cells: `agentic / coding` scores (1–5; Risk and Cost inverted so higher = safer/cheaper).
Weighted total = C×0.20 + S×0.20 + M×0.25 + R×0.20 + K×0.15.

## Decision Logic

- **Dominant axis:** Maintainability (25% weight) + inverted Risk. (ii) wins or ties both
  models on each; (i) loses Risk 2-vs-4; (iii) loses Maintainability 2-3-vs-4.
- **Robustness:** the `coding` councilor verified (ii) wins on every combination of
  accepting/rejecting A-1 and M-1 individually — the ranking is not an artifact of bundling
  the amendments.
- **Flip conditions:** (ii)→(i) if no external reader of `models.credential_id` exists AND
  deployments are single-operator/backup-disciplined AND binary rollbacks never happen.
  (ii) degrades below (i) if 029 is never scheduled. (ii)→(iii) only if multi-instance shared
  affinity becomes near-term.
