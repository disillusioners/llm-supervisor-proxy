# Tracking: Ultimate Model Trigger Schedule

Plan: docs/features/ultimate-model-trigger-schedule-plan.md
Branch: feature/ultimate-model-trigger-schedule

## Iteration 001 — 2026-08-26 — REJECTED
Worker: approve-worker-plan (9b09f3ad-cc3d-4f88-b488-57223429ba2a), skill: plan-approval

Blocking:
1. §4.2/§4.3/§4.6 — Plan promises "delete MarkFailed entirely" but enumerates only 10 of 20 call sites; misses 10 sites in pkg/ultimatemodel/handler_test.go (lines 368, 399, 421, 449, 464, 528, 554, 580, 1099, 1155) and 3 dedicated test functions (handler_test.go:358, 444, 455). Build breaks as written.
2. (downgraded to Note by approver — inert after config.go removal) §4.3/§4.6 — same t.Setenv workaround exists in test/e2e_ultimate_internal_reasoning/e2e_ultimate_internal_reasoning_test.go:193-200 and test/e2e_fe_reasoning_observability/harness_test.go:278; left out of sweep.

Notes: SettingsPage.tsx:387/405 + ProxySettings.tsx sites not enumerated (TS compile risk); mock-script cosmetic refs; §3 MaxHash decision-row wording misleading; §5 section-reference typo; SendRetryExhaustedError call site pkg/proxy/handler.go:662 rename lockstep; first-boot MaxRetries=0 observability.

Positive: factual inventory otherwise verified accurate (line refs cross-checked); compat contract and edge-case coverage strong.

## Iteration 002 — 2026-08-26 — REJECTED
Workers: approve-worker-plan (6ad5a382-4d37-4d6e-9b7e-2c00800af3fa, APPROVED, 0 blocking / 5 notes), approve-worker-cites (1fe362d6-7769-4c6e-9ffd-9c2bbe4a9669, REJECTED, 2 blocking) — skill: plan-approval each; aggregated REJECTED.

Blocking:
1. §4.6 (lines 500-512, "HARD implementation procedure") + §7.10 (lines 605-606, final build-gate) — grep gate `grep -rn 'MarkFailed' pkg/ test/` is provably unsatisfiable: pattern collides with the UNRELATED `upstreamRequest.MarkFailed(err)` method (pkg/proxy/race_request.go:149, race_coordinator.go:807, 14 refs in race_request_test.go, 15 in race_coordinator_test.go, race_coordinator_credfailover_test.go:452) + 7 doc refs in docs/fix-ultimate-model-cloudflare-drop.md — ~39 residual matches in code the plan does NOT delete. Gate can never pass as stated. Fix: scope the grep to pkg/ultimatemodel/ pkg/proxy/handler*.go handler_*_test.go, or pattern `ultimateHandler\.MarkFailed`.
2. §4.6 mock e2e scripts (lines 488-498) — "exhaustive enumeration" claim is false: test/test_mock_ultimate_model.sh has 5 unlisted schedule-incompatible references (line 115 echo, :373 comment, :375 echo, and the entire Test 8 block lines 370-432 asserting `attempt 3 of 2 max` at :424 — becomes `attempt 41 of 40 max` under new schedule; Test 8 needs a rewrite to request-41 exhaustion, not a line update). test/test_mock_minimax_reasoning.sh:768 comment also missed. §8 verification gate (bash test/test_mock_ultimate_model.sh) will fail.

Notes (from workers, non-blocking): e2e_minimax_reasoning §4.6 wording ambiguity (APPLY_ENV_OVERRIDES line must stay, only MAX_RETRIES line goes); handler_test.go:792 GetRetryCount not enumerated (§4.1 grep gate covers); no parallel ULTIMATE_MODEL_MAX_RETRIES grep gate for test/; RecordAttempt-after-StoreIfAbsent semantics could be pinned explicitly; MaxRetries=3 fixture rewrite wording (delete assignment since field is removed).

Positive: ~70+ file:line citations verified exact (frontend, config, handler, hash_cache, docs); 23-site MarkFailed deletion sweep verified complete (iteration 001 gap FIXED); wire/event compat well-scoped; edge cases exhaustive; implementation order build-safe. Iteration-001 rejections were NOT re-raised — rev. 3 addressed them.

## Iteration 003 — 2026-08-26 — APPROVED (final)
Workers: approve-worker-spec (7cb516d1-fd8b-4f3e-97e0-91642faba722, APPROVED, 0 blocking / 3 notes), approve-worker-files (2ee07190-0e5d-4d54-b9d5-9ef824c2b304, APPROVED, 0 blocking / 7 notes) — skill: plan-approval each.

Scope split: §1–3+§5–8 (spec/edge/compat/order/verification) and §4.1–4.7 (changes-by-file vs. live sources). Both zero blocking → APPROVED.

Prior blocking issues confirmed FIXED by fresh verification (not inherited): iteration-001 MarkFailed sweep gap → rev. 4 23-site per-site treatment table verified exact; iteration-002 unsatisfiable repo-wide grep gate → two-gate scoping (pkg/ultimatemodel/ + proxy handler trio) verified provably satisfiable, race_request.go:149 collision correctly excluded; iteration-002 mock-script Test 8 → full rewrite to 41-exhaustion verified.

Aggregated notes (deduped, non-blocking):
1. §3 "Decisions" vs §4.1 semantics-pin wording ambiguity on StoreIfAbsent-then-RecordAttempt (returns 2; §3 sentence reads as 1). §4.1 unit-test bullet is unambiguous and takes precedence — recommend rewording §3 during implementation.
2. §4.1 GetRetryCount grep gate (`pkg/ultimatemodel/` → zero) not mirrored in §7 step 10; test-file refs (hash_cache_test.go 12+ sites) not enumerated — go test gate catches residuals.
3. §8 does not enumerate §4.6's specific assertions (RecordAttempt-after-StoreIfAbsent==2, concurrency multiset {1..N}, 4/5/6/…/41/42 boundaries) as required gates.
4. handler.go:741 inline comment ("client can retry until MaxRetries exhausted") will be stale after symbol removal — drop/reword alongside §4.2 comment rewrite.
5. Minor: e2e_ultimate_internal_reasoning :196–199 label off by a couple of lines (semantic intent unambiguous; grep by env-var name resolves).
6. Implementation-time checks: grep config/models-template.json for stale "max_retries"; check pkg/store/database/*.go for UltimateModel.MaxRetries usages (migrations_pg_test not explicitly covered by gates); §4.4 first-boot-observability grep is asserted, not executed.
