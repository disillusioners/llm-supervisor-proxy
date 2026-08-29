# Tester Documentation: llm-supervisor-proxy

## Project Overview
Go-based proxy server for supervising and managing LLM API requests. Uses SQLite/PostgreSQL for storage, Preact frontend for UI.

## Technology Stack
- **Backend**: Go (net/http, custom migration framework)
- **Frontend**: Preact + Vite + TypeScript + Tailwind CSS
- **Database**: SQLite (default), PostgreSQL (supported)
- **Build**: `go build ./cmd/main.go`, `npm run build` (frontend)

## Testing History

| Phase | Date | Status | Details |
|-------|------|--------|---------|
| Phase 1 | 2026-03-31 | ✅ PASS | Token hourly usage (backend), 355+ tests |
| Phase 2 | 2026-03-31 | ✅ PASS | Usage API endpoints, 202 tests |
| Phase 3 | 2026-03-31 | ✅ PASS | Frontend visualization, 231 tests |
| Test Pack | 2026-04-06 | ✅ PASS | 819 tests, 5 new test files, 4109 lines |
| Idle Termination | 2026-04-06 | ✅ PASS | 8 new tests, 575 new lines, commit 068aa0d |
| Memory Traps Fix | 2026-04-08 | ✅ PASS | Full integration test with race, 23 packages, quick fix commit 972dd01 |
| Token Ultimate Permission | 2026-04-09 | ✅ PASS | 22/22 packages, 9/9 API tests, feature branch `feature/token-ultimate-permission` |
| Fallback Token Count Tests | 2026-04-11 | ✅ PASS | 23 test functions, 129 subcases, 1107 lines, 21 packages, race clean |
| Usage Chart + Daily Bug Fix | 2026-04-12 | ✅ PASS | 21 packages, API verified (hourly/daily), browser automation 6/6, bug fix confirmed |
| Usage Chart + Daily Fix | 2026-04-12 | ✅ PASS | 21/21 Go tests, API verified, browser 6/6 PASS, bug fix verified, branch `feature/usage-chart-view` |
| Secondary Upstream Model (Phase 4) | 2026-04-13 | ✅ PASS | 7 new/extended test files, 2011 lines, 935 tests total, commit 9b20182 |
| C1+C2 Critical Test Gaps | 2026-04-13 | ✅ PASS | 8 new test functions, 1144 lines, execution-level model swap + peak combo, commit 3cd5d56 |
| Token Allowed Models | 2026-05-01 | ✅ PASS | 23 integration tests, 3 bugs found+fixed, branch `feature/token-allowed-models` |
| Token Ultimate Model Override | 2026-05-02 | ✅ PASS | 24 new tests, 1297 lines, branch `feature/token-ultimate-model` |
| DeepSeek Provider | 2026-05-04 | ✅ PASS | 22 packages, 2229+ tests, frontend build PASS, branch `feature/deepseek-provider` |
| Reasoning Content Fix | 2026-05-05 | ✅ PASS | 22 packages, 14 E2E subtests, ensure.md ALL PASS, branch `fix/forward-reasoning-content` |
| E2E Reasoning Content Tests | 2026-05-05 | ✅ PASS | 5 E2E tests, 25 packages regression, commit 3c9d6b8 |
| Ultimate Model "Not Found" Fix | 2026-05-07 | ✅ PASS | 22/22 packages regression, 26 focused tests, 361 new lines, commit 8250179 |
| Token Ultimate Model ID Refactor | 2026-05-07 | ✅ PASS | 24/24 packages, 1044 tests, 7 new tests, frontend PASS, branch `fix/ultimate-model-use-id` |
| Model ID Everywhere | 2026-05-07 | ✅ PASS | 24/24 packages, 5 critical bugs found+fixed, 579-line access control test, frontend PASS, branch `fix/models-use-id-everywhere` |
| X-Force-Ultimate-Model Security | 2026-05-07 | ✅ PASS | 21/21 packages, TestA5 PASS, header gated behind auth+admin, commit `17a7f59` |
| Model Resolution Pipeline Refactor | 2026-05-07 | ✅ PASS | 24/24 packages, 171 functional tests, frontend PASS, branch `fix/remove-getmodelidbyname` |
| Remove ResolveModelByName | 2026-05-08 | ✅ PASS | 24/24 packages, access control tests PASS, frontend PASS, commit `a14a0dc`, branch `fix/remove-resolve-model-by-name` |
| Delete Model Button Fix | 2026-05-08 | ✅ PASS | 22/23 packages (2 pre-existing), frontend PASS, browser 5/5, quick fix `585fe6a`, branch `fix/delete-model-button` |
| Ultimate Model Not Found Log Fix | 2026-05-10 | ✅ PASS | 25/25 packages, ultimatemodel 97 tests, proxy 200+ tests, commit `8510485`, branch `fix/ultimate-model-not-found-log` |
| Credential ID Model Create Fix | 2026-05-10 | ✅ PASS | 25/27 packages (2 pre-existing race), frontend PASS, code logic verified, commit `a2a6c41`, branch `fix/model-credential-create` |
| Model Usage Chart | 2026-05-18 | ✅ PASS | 24/24 packages, 1192 new test lines, 25 test functions, 2 race fixes, branch `feature/model-usage-chart` |
| MCP Proxy Server | 2026-05-19 | ✅ PASS | 25/25 packages (new pkg/mcp), 100+ MCP tests, functional verified, quick fix `32cb5cf` |
| MCP Endpoint Split | 2026-05-20 | ✅ PASS | 23/23 packages, 15 new functional tests (664 lines), all 7 areas verified, branch `feature/mcp-endpoint-split` |
| MCP Test Connection Fix | 2026-05-21 | ✅ PASS | 25/25 packages, 4146 tests, browser 3/3, quick fix `ab85359`, branch `fix/mcp-test-connection` |
| SQLite BUSY Fix | 2026-05-21 | ✅ PASS | 25/25 packages, 2698 tests, 7 new stress tests (670 lines), zero SQLITE_BUSY errors, branch `fix/sqlite-busy-usage-counter` |
| MCP Accept Header Fix | 2026-05-21 | ✅ PASS | 27/27 packages, browser verified zai-web-read SUCCESS (not 400), commit `3729f5d` |
| SQLite Usage Hang Fix | 2026-05-21 | ✅ PASS | 30/30 packages, concurrent reads test PASS, stress tests PASS, frontend PASS, commit `f5a1670`, branch `fix/sqlite-usage-hang` |
| Exclude from Ultimate Switching | 2026-05-30 | ✅ PASS | 27/27 packages, 9/9 feature tests, browser E2E ON+OFF persist verified, 2 quick fixes (9560db0, 4ccef35) |
| Reasoning Content Ultimate-Internal (verify 83814b0) | 2026-08-18 | ✅ PASS | Symptom GONE: new capturing-upstream e2e (`c3a4b35`) PASS→FAIL-on-revert→PASS; ultimatemodel+proxy unit packs PASS; ultimate shell mock 48/48 + buffered 60/60 (after pre-existing Test 8 harness repair `2f67976`+`d1028be`); vet/build clean; 3rd converter probed REACHABLE-but-dormant (no live loss path) |
| Gzip Request-Body Support (verify 7a9ecff) | 2026-08-28 | ✅ PASS | 37/37 packages green (build+vet clean; new gzipmw pack 21+18; proxy 455+475 incl. 5 new gzip tests; ultimatemodel 155 incl. 3 new); E2E original scenario 6/6 vs real binary (byte-identity both /v1 protocols; corrupt→400; 150 MiB bomb→413+liveness; SSE passthrough); zero regressions vs 22e76d6 baseline; report-only, no fixes; branch `feature/gzip-request-support` |
| Model-Credential LB Phase-1 Exit Gate (@ 564b64e) | 2026-08-25 | ✅ PASS | MANDATORY PG 028 txn gate CLOSED (.vscode/.env.dev → TEST_DATABASE_URL); build+vet clean; 10 unit packs = full `go test ./...` set green (0 fail); M-1 shadow spot-check 10/10 non-vacuous; golden-tuple 2/2; 2 anomalies reported (testrefs.go placement, PEAK-DBG logging); testing-only, no code changes |
| Model-Credential LB Phase-2 Formal Gate (@ 315f4e8) | 2026-08-26 | ✅ PASS (1 adjudication) | Full suite 0-fail (10 packs, 9 byte-identical to baseline = no spillover; store +12 Phase-2 tests); race gate CLEAN incl. new pkg/credentiallb; 10/10 spot-proof claims VERIFIED w/ quoted assertions (B2 guard, SoonestExpiry, empty-key, E-3, weighted dist, renorm 1:2.03 empirical, invalidation, mutation-proofed RemoveModel, bus-drain, heal Cooldowns/Failovers==1); grep gates 4/5 (ForTest=13 in W8 testhooks.go — leader ruling pending); boundary diff 866900f..315f4e8 pkg/proxy+ultimatemodel EMPTY |
| Model-Credential LB FINAL Merge Gate (@ d6368bd, Phases 1-5) | 2026-08-26 | ✅ ALL 7 GATES GREEN | Full suite 0-fail (10 packs, growth-only: proxy +21, ultimate +3, misc +17 incl. 11 ui DTO tests; zero shrinks); race set CLEAN (9 pkgs); PG gate ×2 DOUBLE-RUN SAFE (sentinel fix holds); E2E 6/6 first-attempt 91s no-flake (full PR paste in RESULTS; Test 9 = original-symptom 429+429→200 walk proven); frontend build PASS + typecheck 30/30 NEW=0 (repo is npm-managed, adapted); cross-pkg 5 trees green; testing-only, tree left clean |
| LB Final Re-Verification (@ f3164a2, review fixes) | 2026-08-27 | ✅ 5/5 GREEN | E2E ×2 consecutive 6/6 (91s+90s, outcome-(a) clean isolation; Test 2b F1-hardened: 10×1 picks, draws A5/B3/C2 + A6/B3/C1, max≤7/10); race set CLEAN 9 pkgs (suspected race_executor.go:526↔race_request.go:255 NOT tripped — non-reproduction evidence logged for adjudication); full suite 31 ok + 5 no-test, 0 FAIL; W1 defer grep-gate claim-checked 3/3 (real main.go, strict-< order, mutation-sensitive); tree only-expected dirt |
| LIVE E2E vs real MiniMax (user's :4123 proxy, post-merge) | 2026-08-27 | ✅ 4/4 PASS | Smoke OpenAI+Anthropic 200s; affinity sticky (1 binding event, 0 further across 4 turns); distribution 7:5 across 12 distinct convs (p≈0.56 — both creds ≥1); 0 natural 429 (failover mock-verified). Anomalies surfaced: 🔴 /fe/api/events + /fe/api/tokens UNAUTHENTICATED; 🟠 /v1/messages path silent on selected-events; 🟠 stale .env-test token; server untouched, 24 tiny requests total |
| LIVE Stream+Non-Stream verify (user question, :4123) | 2026-08-27 | ✅ 3/3 PASS | stream:false → 200 JSON chat.completion; stream:true → 200 SSE, 4 incremental chunks + [DONE], coherent assembled deltas; mix-affinity: 1 selected event on non-stream first-binding, stream reuse silent (sticky across request shapes); both credentials served stream path (company + mindspath); 5/6 request budget; server untouched |
| MiniMax reasoning_details E2E Gate (P3-2/4/5/6/7) | 2026-08-19 | ✅ READY | Shell mock 53/53 (`882fa3f`), e2e 43/43 (`166aa7f`), matrix 32/32 (`ee590c1`), header table 21 verified, sweep 12/12 zero flakes, drift 0; **2 product bugs found+fixed**: T3b race-internal reasoning loss (`068317c`) + S3 id parity (`b2dfde0`); verification-report.md written; HEAD `fa8c11d` |
| UI Reasoning Observability (verify db7aca0) | 2026-08-21 | ✅ READY — SYMPTOM CLOSED | Closure E2E 4/4 + path matrix 16/16 + anthropic leak 3/3 (mutation-proven) + byte-identity corpus 6 packs + regression 10/10 packs + ensure 4/4 + LIVE glm-5.3 smoke 2/2; new packs `fe_reasoning_observability` + `anthropic_thinking_leak`; matrix `\|` vacuous-pass repaired (`ec1efb6`); mcp pack materialized (`b18de9a`); zero MiniMax usage |
| Ultimate Trigger Schedule §8 Merge Gate (@ a0f4cd1) | 2026-08-27 | ✅ READY TO MERGE | Independent re-run, 11 workers/3 waves: §8 slices + fresh-cache make test (31 ok/0 FAIL) + build/vet/vite PASS; ultimate mock 49/49 (Test 8 header-exact 5/10/20/30/40, 41st exhausted) + minimax 53/53 (T15 green); independent 42-drive boundary matrix EXACT (#41/#42 `ultimate_model_retry_exhausted` "attempt N of 40 max"); committed bundle CLEAN + rebuild byte-identical; Settings max-retries knob GONE (DOM 0 hits), EventLog 41/40 via live events API + bundle aria-label; URL-capture ZERO non-localhost; 1 pre-existing flake quarantined (TestStoreEngine_CloseLifecycle, base df795c8); report-only, tree clean |
| DB Cache Layer MVP Gate (@ 42d98f3→a753698) | 2026-08-29 | ⚠️ MISROUTE BUG CLOSED; NOT READY overall | 14 workers: full `-race` sweep 12/12 slices + modelscache `-race -count=10` 10/10 green; E2E real-SQLite-corruption outage pack ×3 deterministic: ZERO misroutes to :4001, zero Race-attempt WARNs, 503 `config_store_unavailable` both endpoints, recovery 59s≤120s, rollback flag-off reproduces original bug 8/8, UI+write-through PASS; **F1 (HIGH): SQLite error shapes (NOTADB/malformed/CANTOPEN) not infra-classified → token stale-tier never engages → 401s >60s into outage on default dialect** (mock-audit finding confirmed end-to-end); F2/F3 credential write-through ≤60s (invalidate-only by design); F5 /v1/messages auth-parity gap; F7 no live-process self-recovery after file corruption; flake stabilized `c9559cf`; S3 anthropic bug FIXED on branch; ensure.md Critical 4/4 (+TS contradiction notice); test-only commits c9559cf/b48a14f/0be8b8f/a753698 — see RESULTS/2026-08-29-dbcache-layer-gate.md |

## Test Commands
- **Unit tests**: `go test ./... -count=1`
- **Unit tests (verbose)**: `go test ./... -v -count=1`
- **Unit tests (with race)**: `go test ./... -v -race`
- **Go vet**: `go vet ./...`
- **Frontend build**: `cd pkg/ui/frontend && npm run build`
- **Full build**: `go build ./cmd/main.go` (note: `go build .` conflicts with `test_load.go`)

## Test Pack Structure

| Pack | Directory | Tests | Key Files |
|------|-----------|-------|-----------|
| proxy_unit_test | pkg/proxy/ | ~300+ | race_executor_test.go, handler_*.go, adapter_*.go |
| token_unit_test | pkg/proxy/token/ | ~23 | counter_test.go, prompts_test.go |
| ultimatemodel_unit_test | pkg/ultimatemodel/ | ~114 | handler_external_test.go, handler_internal_test.go, usage_test.go |
| store_unit_test | pkg/store/database/ | ~50+ | querybuilder_test.go, database_test.go |
| models_unit_test | pkg/models/ | ~100 | peak_hours_test.go, config_deep_test.go, config_secondary_test.go |
| toolrepair_unit_test | pkg/toolrepair/ | ~45 | strategies_test.go, repair_test.go |
| loopdetection_unit_test | pkg/loopdetection/ | ~31 | detector_test.go |
| auth_unit_test | pkg/auth/ | ~20 | token_test.go, store_test.go |
| misc_unit_test | pkg/{config,crypto,events,...} | ~60+ | various |

## Testing Conventions
- Standard Go testing with `testing` package
- Table-driven tests for parameterized scenarios
- No external test frameworks required
- In-memory SQLite for database-layer tests
- Interfaces used for mockability (e.g., `auth.TokenStoreInterface`)
- `httptest.NewServer` for HTTP handler mocking
