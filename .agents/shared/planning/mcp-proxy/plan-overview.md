# Plan Overview: MCP Proxy Server Module

## Objective
Add a new, independent MCP (Model Context Protocol) proxy server module to `llm-supervisor-proxy` that transparently proxies network-based MCP connections (SSE + Streamable HTTP transports) to upstream MCP servers, with full CRUD management via the existing admin UI.

## Scope Assessment
**LARGE** — New `pkg/mcp/` package with proxy server, database persistence, REST API, frontend tab, and wiring in `cmd/main.go`. Estimated 2-3 days of work across 4 phases. Touches 5+ files, adds ~23+ new files, but modifies only 3 existing files (`cmd/main.go` + migration registry in `migrate.go` + `SettingsPage.tsx`).

## Context
- **Project**: llm-supervisor-proxy
- **Working Directory**: `/Users/nguyenminhkha/All/Code/opensource-projects/llm-supervisor-proxy`
- **Module path**: `github.com/disillusioners/llm-supervisor-proxy`
- **Current migration**: 025 (new starts at 026)
- **Existing patterns**: `database.Store` with `DB *sql.DB` + `Dialect`, `QueryBuilder`, embedded SQL migrations, `ConfigManager`, `events.Bus`, Preact+Tailwind+Vite frontend
- **Key constraint**: Do NOT modify existing LLM proxy code (`pkg/proxy/`, `pkg/providers/`, existing tests). Only add to `pkg/mcp/` and wire in `cmd/main.go`.

## Architecture

```
MCP Client (Claude Desktop, IDE, etc.)
    │
    │ API key in Authorization header
    ▼ SSE or Streamable HTTP
MCP Proxy Server (:4322)  ← NEW, separate http.Server
    │
    ├── Auth middleware (validates API key via auth.TokenStoreInterface)
    │
    ├── Route by path: /mcp/{server_id}/sse  or  /mcp/{server_id}/http
    │
    ├── Config lookup (mcp_servers table)
    │
    ▼ SSE or Streamable HTTP
Upstream MCP Server (remote)
```

The proxy is **transparent** — clients connect to it as if it were the MCP server, with two critical exceptions:
1. **SSE endpoint rewriting**: The proxy parses SSE `endpoint` events and rewrites the upstream URL to the proxy URL, so clients don't bypass the proxy.
2. **Authentication**: MCP clients must present a valid API key (same token system as LLM proxy) to access proxy endpoints.

### MCP Transport Summary

| Transport | Client → Proxy | Proxy → Upstream | Description |
|-----------|---------------|-----------------|-------------|
| **SSE** | Client opens `/mcp/{id}/sse` GET, receives events (endpoint URL rewritten); sends POST to `/mcp/{id}/messages` | Same bidirectional pattern, events forwarded line-by-line | Legacy MCP transport |
| **Streamable HTTP** | Client sends POST to `/mcp/{id}/http`, receives SSE stream response | Same pattern, response streamed through | Modern MCP transport |

## Phase Index

| Phase | Name | Objective | Dependencies | Coupling | Est. Time |
|-------|------|-----------|-------------|----------|-----------|
| 1 | Data Model & Server Bootstrap | DB schema, Go structs, MCP server bootstrap with port config + auth + route stubs | None | — | 5h |
| 2 | MCP Proxy Protocol Engine | SSE (with multi-line endpoint rewriting) + Streamable HTTP proxy + auth middleware + connection registry | Phase 1 (tight) | tight | 7h |
| 3 | Management REST API + Frontend | CRUD API endpoints (with connection cleanup on delete) + MCP tab + status detection | Phase 1 (loose for data model) + Phase 2 (loose for connection registry) | loose | 5h |
| 4 | Integration Wiring & Testing | Wire in cmd/main.go, end-to-end tests, cleanup | Phases 1, 2, 3 | tight | 4h |

### Coupling Assessment

| Pair | Coupling | Reasoning |
|------|----------|-----------|
| Phase 1 → Phase 2 | **tight** | Phase 2 imports `mcp.Store` methods, server config types, auth integration, and `GetMCPProxyPort()` defined in Phase 1 |
| Phase 1 → Phase 3 | **loose** | Phase 3 uses `mcp.Store` CRUD methods only — interface is simple and predictable |
| Phase 2 + Phase 3 | **loose** | Both modify `mcp.go` but on **separate methods**: Phase 2 fills `setupRoutes()` (transport), Phase 3 fills `RegisterAPIHandlers()` (management API). Phase 3 also depends on Phase 2's `ConnectionRegistry.CloseConnections()` for delete cleanup. **Must merge Phase 2 before Phase 3.** |
| All → Phase 4 | **tight** | Phase 4 wires everything together in `cmd/main.go` |

### Parallelism Opportunity
- Phase 2 and Phase 3 are **loosely coupled** — they can run in parallel after Phase 1 if coders coordinate on `mcp.go` method boundaries (Phase 2: `setupRoutes()`, Phase 3: `RegisterAPIHandlers()`). However, Phase 3's delete handler needs Phase 2's `ConnectionRegistry` — **recommended: run Phase 2 first, then Phase 3**.
- Phase 4 must wait for all three

## Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| SSE `endpoint` event URL leak — clients bypass proxy | critical | Parse SSE events line-by-line; accumulate multi-line `data:` fields; detect and rewrite `endpoint` event URLs to proxy paths (C1, B3) |
| Unauthenticated access to MCP proxy endpoints | critical | Reuse `auth.TokenStoreInterface` for API key validation on all proxy routes; mandatory auth (C2) |
| SSE auto-reconnect creates inconsistent MCP session state | high | Connection loss is terminal — close client SSE stream, let client reconnect and re-initialize (C3) |
| Orphaned SSE connections after server deletion | high | `ConnectionRegistry` tracks active connections per server; delete handler calls `CloseConnections(serverID)` before DB deletion (B2) |
| Phase 2+3 both modify `mcp.go` | medium | Partition into separate methods: `setupRoutes()` (Phase 2) and `RegisterAPIHandlers()` (Phase 3). Phase 1 creates empty stubs for both. Sequential execution recommended (B1) |
| Plaintext auth_token storage in database | medium | Use `crypto.Encrypt()`/`crypto.Decrypt()` (AES-256-GCM, already in codebase) from day one (C4) |
| SSRF via malicious `upstream_url` | medium | Validate URLs rejecting localhost, 127.0.0.1, 10.x, 172.16-31.x, 192.168.x, link-local, file:// (W1) |
| Header injection via custom headers config | medium | Block `Host`, `Content-Length`, `Transfer-Encoding`, `Connection` in header injection (W2) |
| Streamable HTTP protocol edge cases (chunked transfer, partial responses) | medium | Test with real MCP clients early; handle `Mcp-Session-Id` header passthrough |
| Migration conflicts if other features merge simultaneously | low | Use migration 026 (current is 025); clearly document in commit message |
| Port conflict if MCP port not configured or in use | low | Make MCP module optional (disabled if port=0 or not configured); log clear error on bind failure; don't crash main server |
| Graceful shutdown of long-lived SSE connections | medium | Use `context.Context` for cancellation; `Shutdown()` with timeout drains active connections |

## Success Criteria
- [ ] MCP proxy server starts on configured port (default disabled)
- [ ] SSE transport: client connects to `/mcp/{id}/sse`, receives forwarded events with rewritten endpoint URL (including multi-line data), can POST to `/mcp/{id}/messages`
- [ ] Streamable HTTP: client POSTs to `/mcp/{id}/http`, receives proxied SSE stream response
- [ ] Authentication: all proxy endpoints require valid API key via `Authorization: Bearer` header
- [ ] CRUD: add/edit/delete MCP upstream servers via frontend UI
- [ ] Server deletion closes all active SSE connections to that upstream (no orphans)
- [ ] Frontend detects if MCP module is enabled/disabled via `/fe/api/mcp-servers/status`
- [ ] Auth tokens encrypted at rest using `crypto.Encrypt()`
- [ ] URL validation prevents SSRF attacks
- [ ] Existing LLM proxy and frontend work unchanged
- [ ] All existing tests pass without modification
- [ ] New tests cover: store CRUD, proxy protocol handlers, API endpoints, auth middleware, connection cleanup

## File Manifest

### New Files (≈23)

```
pkg/mcp/
├── mcp.go                       # Package entry, Server struct, constructor (with tokenStore), Start/Stop, GetMCPProxyPort(), setupRoutes() stub, RegisterAPIHandlers() stub
├── store.go                     # MCPStore — CRUD for mcp_servers table (with crypto.Encrypt/Decrypt)
├── types.go                     # MCPServer struct, TransportType enum, validation helpers
├── auth.go                      # Auth middleware — validates API key via auth.TokenStoreInterface
├── proxy.go                     # ConnectionManager + ConnectionRegistry (active connections tracking + cleanup)
├── handlers_sse.go              # SSE proxy handler with endpoint URL rewriting
├── handlers_streamable.go       # Streamable HTTP proxy handler
├── handlers_api.go              # REST API handlers (CRUD for /fe/api/mcp-servers)
├── validation.go                # URL validation (SSRF prevention), header blocklist, enum validation
├── store_test.go                # MCPStore CRUD tests
├── auth_test.go                 # Auth middleware tests
├── proxy_test.go                # Connection manager tests
├── handlers_sse_test.go         # SSE proxy tests (including endpoint rewriting)
├── handlers_streamable_test.go  # Streamable HTTP tests
├── handlers_api_test.go         # API handler tests (CRUD, test connection, token masking)
├── validation_test.go           # URL validation, header blocklist, enum validation tests
├── e2e_test.go                  # End-to-end integration tests

pkg/store/database/migrations/sqlite/
├── 026_add_mcp_servers.up.sql

pkg/store/database/migrations/postgres/
├── 026_add_mcp_servers.up.sql

pkg/ui/frontend/src/components/mcp/
├── MCPServersTab.tsx            # Main MCP tab — list, add, edit, delete
├── MCPServerForm.tsx            # Add/Edit form modal
```

### Modified Files (3)

```
cmd/main.go                    # Wire MCP server: read port from env, create MCP module with auth, start separate http.Server
pkg/store/database/migrate.go  # Register migration 026 in the migrations list
pkg/ui/frontend/src/components/SettingsPage.tsx  # Add 'mcp_servers' tab type + tab button + content rendering
```

### Minimal-touch Files (2)

```
pkg/ui/frontend/src/types.ts   # Add MCPServer type definition
pkg/ui/frontend/src/hooks/useApi.ts  # Add useMCPServers() hook + standalone CRUD functions
```

## Tracking
- Created: 2026-05-19
- Last Updated: 2026-05-19
- Status: approved (iteration 3 — all blocking issues resolved)
