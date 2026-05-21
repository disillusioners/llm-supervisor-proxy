# Phase 3: Tests & Verification

## Objective
Update all existing MCP tests to use new endpoint paths (`/v1/mcp/{id}/*` for proxy routes), verify management route auth removal, add new tests for auth routing (no auth on management, proxy token auth on proxy endpoints), and perform end-to-end verification.

## Coupling
- **Depends on**: Phase 1 (backend routes and auth changes) + Phase 2 (frontend status type)
- **Coupling type**: tight — tests directly reference route registrations and auth behavior from Phase 1
- **Shared files with other phases**:
  - All `*_test.go` files in `pkg/mcp/` — test the routes Phase 1 registered
  - `pkg/mcp/mcp.go` — tests call `RegisterAPIHandlers()` and `RegisterProxyHandlers()`
  - `pkg/mcp/auth.go` — tests verify `proxyAuthMiddleware` behavior
- **Shared APIs/interfaces**: `RegisterProxyHandlers()` is new and must be tested

## Context
- Phase 1 moved proxy routes to `/v1/mcp/{id}/*` on main server
- Phase 1 removed auth from management routes (`/fe/api/mcp-servers/*`)
- Phase 1 fixed `extractServerID()` to parse `/v1/mcp/` prefix
- Phase 1 fixed `rewriteEndpointData()` to return `/v1/mcp/{id}/messages` URLs
- Phase 1 removed `GetMCPProxyPort()`, `Start()`, `setupRoutes()`
- Phase 1 removed `MCPStatusResponse.Port` field and updated `Enabled` to check `s.store != nil` (RW1)
- Management paths unchanged — `handlers_api_test.go` path tests should still pass
- `mcp_test.go` has 3 tests referencing removed code that need major rewrite (RW3)

## Tasks

### Part A: Update Existing Unit Tests for Proxy Route Paths

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | **Update `handlers_sse_test.go`** | Change ALL test request URLs:
  - `/mcp/{id}/sse` → `/v1/mcp/{id}/sse`
  - `/mcp/{id}/messages` → `/v1/mcp/{id}/messages`
  Verify auth tests still expect 401 without token (auth still required on proxy routes). Verify `rewriteEndpointData` tests expect `/v1/mcp/` prefix in rewritten URLs. | `pkg/mcp/handlers_sse_test.go` |
| 2 | **Update `handlers_streamable_test.go`** | Change ALL test request URLs:
  - `/mcp/{id}/` → `/v1/mcp/{id}/`
  - `/mcp/{id}` → `/v1/mcp/{id}`
  Verify auth still required. Verify `extractServerID` correctly parses new paths. | `pkg/mcp/handlers_streamable_test.go` |
| 3 | **Update `proxy_test.go`** | If connection manager tests reference old `/mcp/{id}` paths, update to `/v1/mcp/{id}`. Check for any hardcoded path assertions. | `pkg/mcp/proxy_test.go` |
| 4 | **Convert `handlers_api_test.go` auth expectations (A2)** | Lines 1284-1389 contain tests asserting 401 for unauthenticated management requests. After removing auth from management routes, these will **fail**. Convert these tests:
  - Remove auth requirement assertions — requests without `Authorization` header should now return 200 (or appropriate non-401 status)
  - Keep the request/response body logic tests intact
  - If tests test auth middleware behavior specifically, move those to `auth_routing_test.go` targeting proxy routes only | `pkg/mcp/handlers_api_test.go` lines 1284-1389 |
| 5 | **Update `auth_test.go`** | Update auth middleware tests:
  - Management routes: assert 200 without Authorization header (was 401)
  - Proxy routes: assert 401 without Authorization header (unchanged behavior)
  - Remove any tests that verify auth on management routes | `pkg/mcp/auth_test.go` |

### Part B: Remove Obsolete Test References

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 6 | **Remove/update `GetMCPProxyPort` test calls** | Phase 1 removes `GetMCPProxyPort()`. Find all test references and remove them. | Any `*_test.go` that calls `GetMCPProxyPort()` |
| 7 | **Remove/update `Start()` test calls** | Phase 1 removes `Start()`. Find all test references and remove them. | Any `*_test.go` that calls `s.Start()` |
| 8 | **Update `NewServer()` test calls** | Phase 1 changes `NewServer` signature (removes port param). Update all test instantiations: `mcp.NewServer(port, store, bus, tokenStore)` → `mcp.NewServer(store, bus, tokenStore)`. | All `*_test.go` in `pkg/mcp/` |
| 9 | **Remove `setupRoutes` test references** | If any tests call `setupRoutes()` directly, remove those tests (method deleted). | Any `*_test.go` |
| 10 | **Major rewrite of `mcp_test.go` (RW3)** | `pkg/mcp/mcp_test.go` has 3 tests referencing removed code that need complete rewrite:
  - **`TestGetMCPProxyPort`**: Function removed entirely. Delete this test.
  - **`TestNewServer`**: Tests old signature `NewServer(port, store, bus, tokenStore)`. Update to new signature `NewServer(store, bus, tokenStore)`. Remove port-related assertions.
  - **`TestShutdown_NilHTTPServer`**: Tests nil HTTP server guard in `Shutdown()`. Since `httpServer` field is removed, rewrite to test connection cleanup only (or delete if behavior is trivial).
  All three tests reference code that Phase 1 removes — this is a mandatory rewrite, not optional cleanup. | `pkg/mcp/mcp_test.go` |

### Part C: New Auth Routing Tests

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 11 | **Management routes — no auth test** | Test that ALL management endpoints return 200 without Authorization header:
  - `GET /fe/api/mcp-servers/` → 200
  - `GET /fe/api/mcp-servers/{id}` → 200
  - `POST /fe/api/mcp-servers/` → 200 (or 400 if body invalid — but not 401)
  - `PUT /fe/api/mcp-servers/{id}` → 200 (or 400)
  - `DELETE /fe/api/mcp-servers/{id}` → 200
  - `POST /fe/api/mcp-servers/{id}/test` → 200 (or appropriate non-401) | New: `pkg/mcp/auth_routing_test.go` |
| 12 | **Proxy routes — unauthenticated returns 401** | Test that ALL proxy endpoints return 401 without auth:
  - `GET /v1/mcp/{id}/sse` → 401
  - `POST /v1/mcp/{id}/messages` → 401
  - `POST /v1/mcp/{id}/` → 401
  - `POST /v1/mcp/{id}` → 401 | `pkg/mcp/auth_routing_test.go` |
| 13 | **Proxy routes — invalid token returns 401** | Test proxy endpoints with invalid/expired tokens → 401. | `pkg/mcp/auth_routing_test.go` |
| 14 | **Proxy routes — valid token passes auth** | Test proxy endpoints with valid proxy token → not 401 (may be other errors depending on upstream, but auth passes). | `pkg/mcp/auth_routing_test.go` |
| 15 | **Route registration verification test** | Test that `RegisterAPIHandlers` registers routes with `/fe/api/mcp-servers/` prefix and `RegisterProxyHandlers` registers routes with `/v1/mcp/` prefix. Verify correct handler mapping. | `pkg/mcp/auth_routing_test.go` |

### Part D: Update E2E Tests

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 16 | **Update `e2e_test.go` proxy paths + remove port** | `pkg/mcp/e2e_test.go`:
  - Update proxy request URLs from `/mcp/{id}/*` to `/v1/mcp/{id}/*`
  - Remove separate port usage (connect to main server port)
  - Update `NewServer()` call (no port param)
  - Verify full flow: create server → connect SSE → send message → delete | `pkg/mcp/e2e_test.go` |
| 17 | **Remove `e2e_test.go` port field assertions (A1)** | Lines 824, 842, 853, 871 contain port field assertions in status response checks. After removing the separate MCP port, these will **fail**. Remove all `.Port` and `port` field assertions from status response checks. Status response now only has `{"enabled": bool}` — no `port` field. | `pkg/mcp/e2e_test.go` lines 824, 842, 853, 871 |
| 18 | **Verify rewriteEndpointData E2E** | Test that SSE clients receive `/v1/mcp/{id}/messages` endpoint URL (not old `/mcp/{id}/messages`). This is the C3 fix validation. | `pkg/mcp/e2e_test.go` or `handlers_sse_test.go` |

### Part E: Full Test Suite Verification

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 19 | **Run full Go test suite** | `go test ./...` — all packages must pass. Document any pre-existing failures separately. | All `*_test.go` |
| 20 | **Run `go vet`** | `go vet ./...` — must be clean. | All `.go` files |
| 21 | **Run frontend build** | `cd pkg/ui/frontend && npm run build` — must pass. | Frontend |
| 22 | **Manual API verification with curl** | Test each endpoint group:
  - Management: `curl localhost:4321/fe/api/mcp-servers/` → 200 (no auth)
  - Proxy unauthenticated: `curl localhost:4321/v1/mcp/test-id/sse` → 401
  - Proxy authenticated: `curl -H "Authorization: Bearer sk-xxx" localhost:4321/v1/mcp/test-id/sse` → passes auth
  - Status: `curl localhost:4321/fe/api/mcp/status` → `{"enabled": true}` | N/A |
| 23 | **Document test results** | Record: total packages, total tests, new tests added, all pass/fail status. | Plan file update |

## Key Files

| File | Purpose | Change Type |
|------|---------|-------------|
| `pkg/mcp/handlers_sse_test.go` | SSE proxy tests | Path updates to `/v1/mcp/{id}/*` |
| `pkg/mcp/handlers_streamable_test.go` | Streamable HTTP tests | Path updates to `/v1/mcp/{id}/*` |
| `pkg/mcp/handlers_api_test.go` | Management handler tests | Convert auth expectations: remove 401 assertions for unauthenticated mgmt requests (A2) |
| `pkg/mcp/auth_test.go` | Auth middleware tests | Refactor: no auth on management, auth on proxy |
| `pkg/mcp/mcp_test.go` | Core server tests | **Major rewrite**: delete `TestGetMCPProxyPort`, update `TestNewServer` signature, rewrite `TestShutdown_NilHTTPServer` (RW3) |
| `pkg/mcp/proxy_test.go` | Connection manager tests | Path updates (if any) |
| `pkg/mcp/e2e_test.go` | End-to-end tests | Path + port updates, remove port field assertions at lines 824, 842, 853, 871 (A1) |
| New: `pkg/mcp/auth_routing_test.go` | Auth routing verification | New file |

## Constraints
- All tests must pass — no pre-existing failures allowed to be masked
- New auth tests must cover both positive (valid token) and negative (no token, invalid token) cases
- E2E tests should verify the complete request flow, not just route matching
- Tests should be runnable without a real upstream MCP server (use mocks)
- Frontend type change from Phase 2 must not cause TypeScript build failures

## Deliverables
- [ ] All existing MCP tests updated and passing
- [ ] `handlers_sse_test.go` uses `/v1/mcp/{id}/*` paths
- [ ] `handlers_streamable_test.go` uses `/v1/mcp/{id}/*` paths
- [ ] `handlers_api_test.go` auth tests converted — management requests without auth return non-401 (A2)
- [ ] `e2e_test.go` port field assertions removed at lines 824, 842, 853, 871 (A1)
- [ ] `mcp_test.go` rewritten — removed function tests deleted, signature tests updated (RW3)
- [ ] Management route tests verify no auth required
- [ ] New `auth_routing_test.go` with auth routing tests for both endpoint types
- [ ] `go test ./...` passes all packages
- [ ] `go vet ./...` is clean
- [ ] `npm run build` passes
- [ ] curl-based verification of all endpoint groups
- [ ] Test count documented (baseline vs new)
