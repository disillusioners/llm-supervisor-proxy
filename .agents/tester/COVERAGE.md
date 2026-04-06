# Coverage Tracking: llm-supervisor-proxy

## Current Coverage Summary (2026-04-06)

### Test Inventory

| Area | Test File(s) | Tests | Status |
|------|-------------|-------|--------|
| Auth / Token Identity | `pkg/auth/token_test.go`, `pkg/auth/store_test.go` | ~20 | ✅ PASS |
| Buffer Store | `pkg/bufferstore/store_test.go` | ~8 | ✅ PASS |
| Config Management | `pkg/config/config_test.go` | ~22 | ✅ PASS |
| Crypto | `pkg/crypto/encryption_test.go` | ~9 | ✅ PASS |
| Events | `pkg/events/bus_test.go` | ~6 | ✅ PASS |
| Loop Detection | `pkg/loopdetection/detector_test.go`, `pkg/loopdetection/fingerprint/fingerprint_test.go` | ~31 | ✅ PASS |
| Models | `pkg/models/peak_hours_test.go`, `pkg/models/config_peak_hour_test.go`, `pkg/models/config_deep_test.go`, `pkg/models/credential_test.go`, `pkg/models/errors_test.go` | ~100 | ✅ PASS |
| Providers | `pkg/providers/factory_test.go`, `pkg/providers/openai_test.go` | ~28 | ✅ PASS |
| **Proxy Handler** | `pkg/proxy/handler_test.go`, `pkg/proxy/handler_integration_test.go`, `pkg/proxy/handler_finalize_test.go`, `pkg/proxy/handler_helpers_test.go`, etc. | ~80 | ✅ PASS |
| **Proxy Race Executor** | `pkg/proxy/race_executor_test.go` | **~74** | ✅ PASS |
| Proxy Adapters | `pkg/proxy/adapter_test.go`, `pkg/proxy/adapter_anthropic_test.go`, `pkg/proxy/adapter_helpers_test.go` | ~30 | ✅ PASS |
| Proxy Auth | `pkg/proxy/authenticate_test.go` | ~9 | ✅ PASS |
| Proxy Counting Hooks | `pkg/proxy/counting_hooks_test.go` | ~39 | ✅ PASS |
| Proxy Streaming | `pkg/proxy/stream_buffer_test.go` | ~15 | ✅ PASS |
| Proxy Coordinators | `pkg/proxy/race_coordinator_test.go`, `pkg/proxy/race_request_test.go`, `pkg/proxy/race_retry_test.go` | ~20 | ✅ PASS |
| Proxy Translators | `pkg/proxy/translator/translator_test.go` | ~10 | ✅ PASS |
| Proxy Normalizers | `pkg/proxy/normalizers/*.go` | ~20 | ✅ PASS |
| Store / Database | `pkg/store/database/database_test.go`, `pkg/store/database/mock_store_test.go`, `pkg/store/database/querybuilder_test.go` | ~50 | ✅ PASS |
| Store / Memory | `pkg/store/memory_test.go` | ~5 | ✅ PASS |
| Supervisor | `pkg/supervisor/monitor_test.go` | ~5 | ✅ PASS |
| Tool Call | `pkg/toolcall/buffer_test.go`, `pkg/toolcall/buffer_minimax_test.go` | ~15 | ✅ PASS |
| **Tool Repair** | `pkg/toolrepair/repair_test.go`, `pkg/toolrepair/strategies_test.go` | **~45** | ✅ PASS |
| **UI Handlers** | `pkg/ui/handlers_usage_test.go` | ~34 | ✅ PASS |
| **Ultimate Model** | `pkg/ultimatemodel/handler_test.go`, `pkg/ultimatemodel/usage_test.go`, `pkg/ultimatemodel/hash_cache_test.go` | ~60 | ✅ PASS |
| **Ultimate Model External** | `pkg/ultimatemodel/handler_external_test.go` | **~27** | ✅ PASS |
| **Ultimate Model Internal** | `pkg/ultimatemodel/handler_internal_test.go` | **~27** | ✅ PASS |
| Usage Counter | `pkg/usage/counter_test.go` | ~10 | ✅ PASS |
| **Total** | | **~819** | |

### Changes Since Last Update (2026-03-31 → 2026-04-06)

| What Changed | Details |
|-------------|---------|
| **NEW: race_executor_test.go** | 1593 lines, ~74 tests covering helper functions, streaming/non-streaming response handlers, internal handlers |
| **NEW: querybuilder_test.go** | 392 lines, covering SQLite/PostgreSQL query building for all operations |
| **NEW: handler_external_test.go** | 820 lines, ~27 tests for external upstream streaming/non-streaming |
| **NEW: handler_internal_test.go** | 897 lines, ~27 tests for internal upstream streaming/non-streaming |
| **NEW: strategies_test.go** | 407 lines, ~30 tests for tool repair strategies, fixer, schema validation |

### Coverage Gaps (Remaining)

| Gap | Risk | Recommendation |
|-----|------|----------------|
| pkg/ui/server.go (1187 lines) | Medium | Route registration, middleware chain tests |
| pkg/store/database/store.go (1237 lines) | Medium | Full CRUD coverage beyond database_test.go |
| pkg/store/database/migrate.go | Low | Migration version tracking |
| pkg/store/database/connection.go | Low | Connection string parsing |
| cmd/main.go | Low | Entry point, typically not unit tested |

### Quick Fix History

| Date | Commit | Description |
|------|--------|-------------|
| 2026-04-06 | `3f5e761` | Fixed go vet errors in handler_external_test.go (resp used before err check) |
| 2026-03-31 | `5881e6e` | Removed unused imports in handlers_usage.go |
| 2026-03-31 | `4b1c3ad` | Fixed race condition in counting_hooks_test.go |
