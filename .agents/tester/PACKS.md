# Test Packs

## Summary
- Total: 14 unit packs (12 prior + gzipmw_unit_test + build_gate_test, added 2026-08-28 for the gzip feature gate) + mock/E2E packs below, across 37 Go packages
- Unit: 14 | Integration: 1 | E2E: 1 | Mock: 1
- All packs enforce **2-minute timeout** via `timeout` command (subprocess-based)

## Timeout Configuration
- **Script timeout**: 120s (`timeout 120s`)
- **Go test timeout**: 110s (`-timeout=110s`)
- **Buffer**: 10s for script overhead/cleanup
- **Exit codes**: 0=PASS, 1=FAIL, 124=TIMEOUT

## Unit Test Packs

| Pack | Script | Scope | Timeout | Last Run | Status |
|------|--------|-------|---------|----------|--------|
| proxy_unit_test | test/packs/proxy_unit_test.sh | handler, race_executor, adapters, streaming, auth | 120s | 2026-08-28 | PASS (446+475 sub, 0 fail, 7 branch-gated skips; @ 22e76d6 rsd merge gate) |
| ultimatemodel_unit_test | test/packs/ultimatemodel_unit_test.sh | handler, handler_external, handler_internal, usage | 120s | 2026-08-28 | PASS (152+87 sub, 0 fail, +7 vs d6368bd; @ 22e76d6 rsd merge gate) |
| store_unit_test | test/packs/store_unit_test.sh | database, querybuilder, mock_store | 120s | 2026-08-28 | PASS (99 + 4 PG-skips; quarantined CloseLifecycle did NOT fire; @ 7a9ecff gzip gate) |
| models_unit_test | test/packs/models_unit_test.sh | config, peak_hours, credentials, errors, secondary_upstream | 120s | 2026-08-28 | PASS (87 + 267 sub; @ 7a9ecff gzip gate) |
| toolrepair_unit_test | test/packs/toolrepair_unit_test.sh | repair, strategies, fixer | 120s | 2026-08-28 | PASS (17/105; @ 7a9ecff gzip gate) |
| loopdetection_unit_test | test/packs/loopdetection_unit_test.sh | detector, fingerprint, strategies | 120s | 2026-08-28 | PASS (33/33; @ 22e76d6 rsd gate) |
| auth_unit_test | test/packs/auth_unit_test.sh | token, store | 120s | 2026-08-28 | PASS (48+39; @ 22e76d6 rsd gate) |
| token_unit_test | pkg/proxy/token/ (inline) | counter, prompts, encoding, extraction | 120s | 2026-08-28 | PASS (ok 0.158s non-verbose; prior baseline 23 + 123 sub; @ 7a9ecff gzip gate) |
| mcp_unit_test | test/packs/mcp_unit_test.sh | pkg/mcp/ — store, validation, auth, proxy, handlers_sse, handlers_streamable, handlers_api, e2e, endpoint_split_validation | 120s | 2026-08-28 | PASS (245+471, 3 env-conditional SSRF skips; @ 7a9ecff gzip gate) |
| misc_unit_test | test/packs/misc_unit_test.sh | config, crypto, events, bufferstore, providers, supervisor, toolcall, ui, usage | 120s | 2026-08-28 | PASS (284+119, 9 pkgs; @ 22e76d6 rsd gate) |
| translator_unit_test | test/packs/translator_unit_test.sh | pkg/proxy/translator — wire translation incl. incremental_stream (real-streaming-default Phase 3) | 120s | 2026-08-28 | PASS (169 entries, 19 incremental_stream cases; FIRST registered run — closed coverage gap; @ 22e76d6 rsd gate) |
| gap_unit_test | test/packs/gap_unit_test.sh | credentiallb, proxyheader, proxy/normalizers, loopdetection/fingerprint, store parent (memory) | 120s | 2026-08-28 | PASS (274 entries, 118 funcs; FIRST registered run — closed coverage gap; @ 22e76d6 rsd gate) |
| testroot_unit_test | test/packs/testroot_unit_test.sh | test/ root — access_control + integration_allowed_models | 120s | 2026-08-28 | PASS (35 entries; FIRST registered run — closed coverage gap; @ 22e76d6 rsd gate) |
| gzipmw_unit_test | test/packs/gzipmw_unit_test.sh | pkg/middleware/gzipmw — gzip request-body decompression middleware (21 test funcs) | 120s | — | PASS (FIRST run 2026-08-28; @ 7a9ecff gzip gate) |
| build_gate_test | test/packs/build_gate_test.sh | go build ./... + go vet ./... (Go-only gate; npm/tsc excluded by design — 30 standing tsc errors are known baseline debt) | 120s | — | PASS (FIRST run 2026-08-28; @ 7a9ecff gzip gate) |
| reasoning_content_dir | inline: `go test ./test/reasoning_content/ -v -count=1 -timeout 240s` | serialization chain + non-stream reasoning_content | 240s go-test / `timeout 300` outer | 2026-08-28 | PASS (2 funcs / 14 subtests; @ 7a9ecff gzip gate) |

## Race Slices (real-streaming-default merge gate 2026-08-28)

| Slice | Exact command | Result |
|-------|---------------|--------|
| raceA_proxy | `timeout 300 go test -race -run 'TestRealStreaming\|TestLiveRelay\|TestLiveMode\|TestUltimateCapturePersistence' -count=1 ./pkg/proxy/ -timeout 280s` | **PASS** — 0 races, 24s |
| raceB_ultimate | `timeout 300 go test -race -run 'TestUltimate' -count=1 ./pkg/ultimatemodel/ -timeout 280s` | **PASS** — 0 races, 2.8s |
| raceC_translator | `timeout 300 go test -race -count=1 ./pkg/proxy/translator/ -timeout 280s` | **PASS** — 0 races, 1.1s |

## Mock Test Packs

| Pack | Script | Type | Timeout | Last Run | Status |
|------|--------|------|---------|----------|--------|
| peak_hour_fallback_mock | test/mock_llm_peak_hour_fallback.go | E2E Mock | N/A (Go test) | 2026-04-09 | PASS |
| frontend_api_cache_mock | test/mock_frontend_api_cache.mjs | Unit | 60s | 2026-04-09 | PASS |
| allowed_models_integration | test/integration_allowed_models_test.go | Integration | N/A (Go test) | 2026-05-01 | PASS |
| e2e_reasoning_content | test/e2e_reasoning_content/ | E2E | N/A (Go test) | 2026-05-05 | PASS |
| ultimate_model_shell_mock | test/test_mock_ultimate_model.sh | Shell E2E (mock ultimate fallback; ports 4001/4322 harness-fixed, pre-existing convention) | 75s internal / `timeout 300` outer | 2026-08-28 | **PASS 49/49** @ 22e76d6 (rsd merge gate; real-binary ultimate paths incl. live streaming defaults; ~150s wall) |
| openai_internal_buffered_shell_mock | test/test_mock_openai_internal_buffered.sh | Shell E2E (buffered openai internal; ports 4003/4324 harness-fixed, pre-existing convention) | 60s internal / `timeout 300` outer | 2026-08-28 (re-run) | **PASS 60/60** @ 61fa02a — harness repaired by 61fa02a (credentials[] payload + buffer header on all 15 curls); prior FAIL 0/60 (credential-LB schema rot) superseded |
| e2e_ultimate_internal_reasoning | test/e2e_ultimate_internal_reasoning/ | E2E Mock (capturing in-process upstream) | 110s go-test / `timeout 300` outer | 2026-08-28 | PASS 1/1 @ 22e76d6 (rsd merge gate) |
| minimax_reasoning_shell_mock | test/test_mock_minimax_reasoning.sh | Shell E2E (mock MiniMax upstream w/ reasoning_details replay + capture; ports 4005/4325 harness-fixed) | 120s internal / `timeout 300` outer | 2026-08-28 | **PASS 53/53** @ 22e76d6 (rsd merge gate; 11s wall — previous near-cap warning RESOLVED, was a cold-build artifact) |
| e2e_minimax_reasoning | test/e2e_minimax_reasoning/ | E2E Mock (capturing in-process upstream; 4-path scenario suite, P3-5) | 240s go-test / `timeout 300` outer | 2026-08-28 | **PASS 43/43** @ 22e76d6 (rsd merge gate; drift delta 0; header hygiene 0 leaks) |
| minimax_interleaved_matrix | inline (exact command in code block below — **NOT** `\|`, see warning) | Unit (P3-2 byte-identical negative matrix: 24 body + 4 header + 4 usage) | `timeout 300` | 2026-08-21 | **PASS** — 34/34 test funcs (46 PASS incl. 12 subtests) @ `355f06c`; quoting repair: registered `\|` form was vacuous (0 tests run, see LESSONS/2026-08-21-interleaved-matrix-regex-vacuous-pass.md); 2 N/A cells noted 2026-08-19 stand |
| proxyheader_header_table | inline: `go test ./pkg/proxyheader/ -run 'Interleaved' -count=1` | Unit (P3-7 header value truth table verify) | `timeout 300` | 2026-08-19 | **PASS** — satisfied by existing 21 sub-cases (all 7 plan values covered, file:line-cited); precedent match confirmed; single-source semantics confirmed (2 call sites via proxyheader.*); NO gap-fill needed |
| fe_reasoning_observability | test/e2e_fe_reasoning_observability/ | E2E Mock (in-process proxy + real-HTTP FE API mount; closure gate + 16-row path matrix) | 240s go-test / `timeout 300` outer | 2026-08-28 | **PASS 20/20** @ 22e76d6 (rsd merge gate: closure 4/4 + matrix 16/16; capture taps mode-independent; NOTE: no anthropic-internal-nonstream row — S3-class bug not covered here) |
| anthropic_thinking_leak | test/e2e_anthropic_thinking_leak/ | E2E Mock (in-process anthropic /v1/messages; sink-vs-wire dual assertion) | 240s go-test / `timeout 300` outer | 2026-08-28 | **PARTIAL @ 22e76d6 (post S1 re-base 22e76d6)**: S1A buffered ✅ S1B live thinking_delta ✅ (D8) S2 ✅ — **S3 ❌ REAL BUG** (internal-Anthropic NON-stream persistence empty; first-bad e717be3; see LESSONS/2026-08-28-rsd-s3-nonstream-persistence-bug.md) |
| rsd_m1_openai_ttfb | test/mock_rsd_m1_openai_ttfb.sh | Shell E2E (real binary; OpenAI path TTFB default-vs-buffered; ports 10110/10111) | 240s internal / `timeout 300` outer | 2026-08-28 | **PASS** @ 22e76d6 (A: TTFB 103ms vs total 1536ms, 5 gaps ≥150ms; B: TTFB 1603ms spread 0ms; C info: byte-diff = live `: keepalive` only, content identical; D healthz 200; 3× stable) |
| rsd_m2_anthropic_ultimate_ui | test/mock_rsd_m2_anthropic_ultimate_ui.sh | Shell E2E (real binary; /v1/messages both modes + ultimate + UI records; ports 10120/10121) | 240s internal / `timeout 300` outer | 2026-08-28 (final gate) | **PASS — E is HARD since e60de91** @ e60de91 (A: TTFB 1ms/incremental; B: 1551ms/single-burst; **E HARD: non-stream id-normalized byte-identity — both 322B, sha-identical, Anthropic-shape both, OpenAI-shape negative guard clean**; F: S3 records exact; C documented-impractical). See RESULTS/2026-08-28-rsd-m2-e-hard-gate-final.md |
| rsd_m3_edge_cases | test/mock_rsd_m3_edge_cases.sh | Shell E2E (real binary; header truth table + first-wins + stream=false + disconnect; ports 10130/10131) | 240s internal / `timeout 300` outer | 2026-08-28 | **PASS 21/21** @ 22e76d6 (truth table 12/12 incl. case+garbage; multi-line first-wins both directions; stream=false byte-identical; disconnect: healthz 200, no panic) |
| e2e_reasoning_content | test/e2e_reasoning_content/ | E2E Mock (reasoning_content chain, streaming + non-stream) | 240s go-test / `timeout 300` outer | 2026-08-28 | PASS 5 top-level + 7 sub @ 22e76d6 (rsd merge gate) |
| gzip_request_decompression | test/mock_gzip_request_decompression.sh | Shell E2E (real binary; gzip request-body original scenario a-f; ports 10140/10141) | 240s internal / `timeout 300` outer | 2026-08-28 | **PASS 6/6** @ 7a9ecff (gzip feature gate; byte-identity both /v1 protocols, corrupt→400, 150 MiB bomb→413 + liveness, SSE passthrough; 3 harness-only fixes on first run — see LESSONS/2026-08-28-gzip-e2e-harness-lessons.md) |

#### anthropic_thinking_leak — historical note (superseded 2026-08-28)
- Pre-rsd-gate baseline: PASS 3/3 @ 63b7701; mutation-proven non-vacuous (leak re-injected in worktree → S1 FAILs with 4 detections); S3 wire byte-identical at parent effc345.

#### minimax_interleaved_matrix — exact runnable command

```bash
# ⚠️ NOT 'Interleaved\|MiniMax\|Reasoning' — in single quotes, go's RE2 reads
# \| as an ESCAPED LITERAL PIPE and matches ZERO tests (vacuous exit-0 PASS).
# The `\|` form only ever worked unquoted, where the shell strips the backslash.
timeout 300 go test ./pkg/proxy/ ./pkg/ultimatemodel/ -run 'Interleaved|MiniMax|Reasoning' -count=1 -timeout 280s
```

Expected: 17 test funcs in `pkg/proxy` + 17 in `pkg/ultimatemodel` = **34 test functions** (46 `--- PASS` lines incl. 12 subtests), exit 0. If output says `[no tests to run]`, the quoting regression has returned — see `LESSONS/2026-08-21-interleaved-matrix-regex-vacuous-pass.md`.

---

## Updating PACKS.md

Update after each test run:
- **Last Run**: timestamp
- **Status**: PASS/FAIL/TIMEOUT
- Add new entry for new packs
- Mark deprecated packs as DEPRECATED

## Integrity Checks
- ✅ All 8 unit test pack scripts exist in `test/packs/`
- ✅ All scripts are executable (`chmod +x`)
- ✅ All scripts use `timeout 120s` for subprocess-based enforcement
- ✅ All scripts output `RESULT: PASS|FAIL|TIMEOUT`
- ✅ All scripts have cleanup traps on EXIT

## Recent Test Results (2026-04-13)

## Recent Test Results (2026-05-08)

### Commits d578427 + 585fe6a - Delete Model Button Fix
| Category | Status | Details |
|----------|--------|---------|
| Go Build | ✅ PASS | `go build ./cmd/main.go` |
| Go Unit Tests | ✅ PASS | 22/23 packages (2 pre-existing failures in ultimatemodel, also fail on master) |
| Go Vet | ✅ PASS | No issues |
| Frontend Build | ✅ PASS | 1.46s, 4 chunks, no warnings |
| Browser Tests | ✅ 5/5 | Delete dialog, cancel, delete, loading state, overlay dismiss |
| Quick Fixes | 1 | Overlay click dismiss handler (commit 585fe6a) |

*Note: `ultimatemodel_unit_test` has 2 pre-existing failures unrelated to this PR (also fail on master):
`TestExecute_PerTokenOverride_WithID` and `TestExecute_AllLookupsUseID/per-token_uses_ID_lookup`

### Commit 3cd5d56 - Critical Test Gaps Fix (C1+C2)
| Category | Status | Details |
|----------|--------|---------|
| Go Build | ✅ PASS | `go build ./cmd/main.go` |
| Go Unit Tests | ✅ PASS | 21/21 packages, 946 tests |
| Go Vet | ✅ PASS | No issues |
| New Test Functions | ✅ 8 | 4 executor E2E + 4 coordinator peak+secondary |

#### New Test Functions
| Function | Location | Verifies |
|----------|----------|----------|
| TestExecuteInternalRequest_SecondaryModelSwap_E2E_NonStream | race_executor_test.go | Provider receives secondary model (non-stream) |
| TestExecuteInternalRequest_SecondaryModelSwap_E2E_Stream | race_executor_test.go | Provider receives secondary model (stream) |
| TestExecuteInternalRequest_NoSecondary_UsesPrimary_E2E | race_executor_test.go | Empty secondary → primary used |
| TestExecuteInternalRequest_SecondaryFalse_UsesPrimary_E2E | race_executor_test.go | Flag=false → primary used |
| TestRaceCoordinator_PeakHourWithSecondaryModel | race_coordinator_test.go | Main=peak, second=secondary |
| TestRaceCoordinator_PeakHourModelOnly_NoSecondary | race_coordinator_test.go | Peak active, no secondary |
| TestRaceCoordinator_SecondaryOverridesPeakHour | race_coordinator_test.go | Secondary independent of peak |
| TestRaceCoordinator_NoPeakHour_UsesInternalModel | race_coordinator_test.go | Peak disabled → internal model |

### Commit 9b20182 - Secondary Upstream Model Phase 4 Tests
| Category | Status | Details |
|----------|--------|---------|
| Go Build | ✅ PASS | `go build ./cmd/main.go` |
| Frontend Build | ✅ PASS | `npm run build` (1.00s) |
| Go Unit Tests | ✅ PASS | 21/21 packages, 935 tests |
| Go Vet | ✅ PASS | No issues |
| New Test Files | ✅ 7 files | 2011 lines of new tests |
| Test Matrix | ✅ 4/4 | All scenarios covered |

### Phase 4 Test Files
| File | Lines | Type |
|------|-------|------|
| pkg/models/config_secondary_test.go | 360 | NEW — validation |
| pkg/proxy/race_coordinator_test.go | 215 | EXTENDED — coordinator |
| pkg/proxy/race_executor_test.go | 279 | EXTENDED — executor |
| pkg/proxy/race_request_test.go | 162 | EXTENDED — request flag |
| pkg/proxy/race_retry_test.go | 68 | EXTENDED — retry integration |
| pkg/store/database/database_test.go | 370 | EXTENDED — store CRUD |
| pkg/ui/handlers_models_test.go | 557 | NEW — API handlers |

### Commit a2a6c41 - Credential ID Model Creation Fix (Branch: fix/model-credential-create)

| Category | Status | Details |
|----------|--------|---------|
| Go Build | ✅ PASS | `go build ./cmd/main.go` |
| Go Unit Tests | ✅ PASS | 25/27 packages (2 pre-existing race in stream_buffer.go:166) |
| Go Vet | ✅ PASS | No issues |
| Frontend Build | ✅ PASS | Built in 1.72s |
| Code Logic Verification | ✅ PASS | All 6 checks passed |

**Pre-existing failures (unrelated to fix):**
- `pkg/proxy` — nil pointer dereference in `streamBuffer.GetAllRawBytesOnce()` (stream_buffer.go:166)
- `test/e2e_reasoning_content` — same root cause, different call path through race_executor.go:621

### Commit 1a7bb68 - Model Usage Chart Tests (Branch: feature/model-usage-chart)

| Category | Status | Details |
|----------|--------|---------|
| Go Build | ✅ PASS | `go build ./cmd/main.go` |
| Go Unit Tests | ✅ PASS | 24/24 packages |
| Go Vet | ✅ PASS | No issues |
| Frontend Build | ✅ PASS | Built in 1.07s |
| Race Detector | ✅ PASS | 24/24 packages, 0 races (after 2 quick fixes) |
| New Test Lines | ✅ 1192 | counter_test.go (493), handlers_usage_test.go (699) |
| New Test Functions | ✅ 25 | 8 usage + 17 API handler tests |

**Quick fixes applied:**
- `2f15b2c` — Fix race condition in TestHeartbeat (safeResponseWriter wrapper)
- `02f1b84` — Fix race condition in TestRaceScenario_FallbackWins (atomic.Int64)

### Commits 725b115 + d5fe2d7 + ab85359 - MCP Test Connection Button Fix (Branch: fix/mcp-test-connection)

| Category | Status | Details |
|----------|--------|---------|
| Go Build | ✅ PASS | `go build ./cmd/main.go` |
| Go Unit Tests | ✅ PASS | 25/25 packages, 4146 tests |
| Go Vet | ✅ PASS | No issues |
| Frontend Build | ✅ PASS | Built in 2.84s |
| Browser Tests | ✅ 3/3 | Add Server, Edit Server, SSRF protection |
| Quick Fixes | 1 | Missing error field in testResult (commit `ab85359`) |

**Browser automation verified:**
- Test A: Add Server mode — button calls `/fe/api/mcp-servers/test-connection` ✅
- Test B: Edit Server mode — button calls API ✅
- Test C: SSRF protection — localhost URLs blocked ✅
 blocked ✅
ction — localhost URLs blocked ✅
 blocked ✅
✅
ion verified:**
- Test A: Add Server mode — button calls `/fe/api/mcp-servers/test-connection` ✅
- Test B: Edit Server mode — button calls API ✅
- Test C: SSRF protection — localhost URLs blocked ✅
 blocked ✅
ction — localhost URLs blocked ✅
 blocked ✅
✅
cked ✅
ction — localhost URLs blocked ✅
 blocked ✅
✅
blocked ✅
✅
