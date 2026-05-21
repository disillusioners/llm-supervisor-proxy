# Phase 1: Backend Routing, Auth & Handler Path Updates

## Objective
Move MCP proxy endpoints from the separate MCP proxy server to the main server at `/v1/mcp/{id}/*`. Remove the separate MCP HTTP server entirely. Remove auth from management routes (`/fe/api/mcp-servers/*`). Update all hardcoded path references in handlers (`extractServerID()`, `rewriteEndpointData()`, `TrimPrefix` calls). Add middleware-based proxy token auth for proxy routes.

## Coupling
- **Depends on**: None (root phase)
- **Coupling type**: —
- **Shared files with other phases**:
  - `pkg/mcp/mcp.go` — route registration (Phase 3 tests reference these)
  - `pkg/mcp/auth.go` — auth middleware (Phase 3 tests verify auth behavior)
  - `cmd/main.go` — server startup (Phase 2 touches for init trigger)
  - `pkg/mcp/types.go` — Server struct (Phase 2 touches for init trigger)
- **Shared APIs/interfaces**: `RegisterAPIHandlers()`, new `RegisterProxyHandlers()`, `proxyAuthMiddleware()`

## Context
- Current state: MCP proxy runs on separate `MCP_PROXY_PORT` with its own HTTP server
- Management API registered on main mux via `mcpServer.RegisterAPIHandlers(mux)` at `/fe/api/mcp-servers/*` with `proxyAuthMiddleware`
- Proxy endpoints on separate MCP server via `setupRoutes()` at `/mcp/{id}/*`
- Multiple handlers have **hardcoded path references** that will break if paths change

### Critical Path References Found in Exploration

| File | Line | Code | Issue |
|------|------|------|-------|
| `handlers_api.go` | 101, 170, 226, 263 | `TrimPrefix("/fe/api/mcp-servers/")` | Must stay — management paths unchanged ✅ |
| `handlers_sse.go` | 25 | `fmt.Sprintf("/mcp/%s/messages", serverID)` | Must update to `/v1/mcp/%s/messages` (C3) |
| `handlers_sse.go` | 99 | Comment `// The proxy endpoint is always /mcp/{id}/messages` | Update comment |
| `handlers_sse.go` | 408 | `if parts[0] != "mcp"` | Must update — `parts[0]` will be `"v1"` (C2) |
| `handlers_streamable.go` | 22 | `extractServerID(r.URL.Path)` | Delegates to broken function — fix upstream (C2) |
| `mcp.go` | 53-58 | `/mcp/{id}/sse`, `/mcp/{id}/messages`, `/mcp/{id}/`, `/mcp/{id}` | Must update to `/v1/mcp/...` prefix |

## Tasks

### Part A: Update Route Registration in pkg/mcp/mcp.go

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | **Remove `setupRoutes()` method** | Delete the entire `setupRoutes()` method (lines 48-61) that creates a separate mux for the MCP server. No longer needed. | `pkg/mcp/mcp.go` |
| 2 | **Add `RegisterProxyHandlers(mux *http.ServeMux)` method** | New method that registers proxy routes on the **main server mux**. Applies `proxyAuthMiddleware` to all proxy routes. **Add `s.tokenStore == nil` guard here** (moved from `RegisterAPIHandlers` — RW2). Route table (W6): | `pkg/mcp/mcp.go` |

**Route table for `RegisterProxyHandlers`:**

| Method | Pattern | Handler | Auth |
|--------|---------|---------|------|
| GET | `/v1/mcp/{id}/sse` | `proxyAuthMiddleware(s.handleSSEConnection)` | Token |
| POST | `/v1/mcp/{id}/messages` | `proxyAuthMiddleware(s.handleSSEMessage)` | Token |
| POST | `/v1/mcp/{id}/` | `proxyAuthMiddleware(s.handleStreamableHTTP)` | Token |
| POST | `/v1/mcp/{id}` | `proxyAuthMiddleware(s.handleStreamableHTTP)` | Token |

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 3 | **Remove `proxyAuthMiddleware` from `RegisterAPIHandlers()`** | Management routes (`/fe/api/mcp-servers/*`) currently wrap all handlers with auth. Remove auth wrapping from all 7 CRUD handlers. They remain at `/fe/api/mcp-servers/*` paths — **no path changes for management** (C4, C7/C8). **Also update nil guard (RW2):** line 65 checks `s.store == nil || s.tokenStore == nil`. Since management no longer needs auth, change to `s.store == nil` only. Move `s.tokenStore == nil` guard to `RegisterProxyHandlers()`. | `pkg/mcp/mcp.go` lines 63-113 |
| 4 | **Remove `httpServer` field from Server struct** | Remove `httpServer *http.Server` from struct (line ~33). Remove `port int` field. Server struct keeps: `store`, `bus`, `tokenStore`. | `pkg/mcp/mcp.go` lines 27-35 |
| 5 | **Update `NewServer()` signature** | Remove `port int` parameter. New signature: `NewServer(store *MCPStore, bus *EventBus, tokenStore auth.TokenStoreInterface) *Server`. No longer creates `http.Server`. | `pkg/mcp/mcp.go` lines 37-46 |
| 6 | **Remove `Start()` method** | The `Start()` method starts the separate HTTP server. Remove entirely. | `pkg/mcp/mcp.go` |
| 7 | **Update `Shutdown()` — keep connection cleanup only** | `Shutdown()` currently shuts down the HTTP server + cleans connections. Remove HTTP server shutdown. Keep only SSE connection registry cleanup. | `pkg/mcp/mcp.go` |
| 8 | **Remove `GetMCPProxyPort()` entirely** | Delete the function (lines 15-25). It will have no callers after refactor. (W7) | `pkg/mcp/mcp.go` |

### Part B: Fix extractServerID() for New Path Prefix (C2)

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 9 | **Update `extractServerID()` path parsing** | Current code at line 408: `if parts[0] != "mcp"` — this breaks because `/v1/mcp/{id}/` splits to `["v1", "mcp", "{id}", ...]`. New logic: parse `/v1/mcp/{id}/` only. Since the separate MCP server is removed, no routes serve `/mcp/{id}/` anymore — do NOT add backward compat for old prefix (RS1). Implementation: use `strings.TrimPrefix(r.URL.Path, "/v1/mcp/")` for clean extraction. Validate that remaining path starts with a server ID. | `pkg/mcp/handlers_sse.go` lines 396-422 |
| 10 | **Verify handlers_streamable.go calls work** | Line 22 calls `extractServerID(r.URL.Path)`. Once extractServerID is fixed, this works automatically. Verify no other path assumptions. | `pkg/mcp/handlers_streamable.go` |

### Part C: Fix rewriteEndpointData() Hardcoded URL (C3)

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 11 | **Update `rewriteEndpointData()` SSE URL** | Line 25: `fmt.Sprintf("/mcp/%s/messages", serverID)` → `fmt.Sprintf("/v1/mcp/%s/messages", serverID)`. This is the URL clients receive for posting messages back. Must match the new proxy route path. | `pkg/mcp/handlers_sse.go` line 25 |
| 12 | **Update comment at line 99** | Change comment from `// The proxy endpoint is always /mcp/{id}/messages` to `// The proxy endpoint is always /v1/mcp/{id}/messages`. | `pkg/mcp/handlers_sse.go` line 99 |

### Part D: Verify handlers_api.go TrimPrefix Calls (C1)

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 13 | **Verify TrimPrefix calls are correct** | Management paths stay at `/fe/api/mcp-servers/*` (C4). The 4 `TrimPrefix("/fe/api/mcp-servers/")` calls at lines 101, 170, 226, 263 are **still correct** — no changes needed. Verify by tracing route → handler: `RegisterAPIHandlers` registers at `/fe/api/mcp-servers/` prefix, handlers TrimPrefix the same. ✅ No change needed. | `pkg/mcp/handlers_api.go` |

### Part E: Update cmd/main.go

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 14 | **Replace MCP_PROXY_PORT check with MCP_ENABLED** | Change from `mcpPort := mcp.GetMCPProxyPort(); if mcpPort > 0` to checking `MCP_ENABLED` env var. Add deprecation warning if `MCP_PROXY_PORT` is set: `"MCP_PROXY_PORT is deprecated, use MCP_ENABLED=true instead. MCP now runs on the main port."` (C6) | `cmd/main.go` lines ~114-143 |
| 15 | **Remove MCP proxy goroutine** | Remove the `go func()` block that starts the separate MCP HTTP server. Keep server initialization: `mcpServer = mcp.NewServer(mcpStore, bus, tokenStore)`. | `cmd/main.go` lines ~131-143 |
| 16 | **Add proxy handler registration** | After `mcpServer.RegisterAPIHandlers(mux)`, add: `mcpServer.RegisterProxyHandlers(mux)` to register proxy routes on main server. | `cmd/main.go` after line ~130 |
| 17 | **Update MCP status endpoint + `MCPStatusResponse` struct (RW1)** | Three changes:
  1. **`pkg/mcp/handlers_api.go:47-51`**: Remove `Port int` field from `MCPStatusResponse` struct.
  2. **`pkg/mcp/handlers_api.go:69`**: Change `s.port > 0` → `s.store != nil` for `Enabled` field (no port to check).
  3. **`cmd/main.go:113-121`**: Update inline status handler to check `MCP_ENABLED` instead of `MCP_PROXY_PORT`. Response: `{"enabled": bool}` only — no `port` field (C5). | `pkg/mcp/handlers_api.go`, `cmd/main.go` |
| 18 | **Simplify graceful shutdown** | Remove MCP server HTTP shutdown call. Keep only `mcpServer.Shutdown(ctx)` for connection cleanup (now connection-only). | `cmd/main.go` lines ~213-220 |

## Key Files

| File | Purpose | Change Type |
|------|---------|-------------|
| `pkg/mcp/mcp.go` | Route registration, server struct, lifecycle | Major refactor: remove server, add RegisterProxyHandlers, remove GetMCPProxyPort |
| `pkg/mcp/auth.go` | Auth middleware | No changes — `proxyAuthMiddleware` reused as-is for proxy routes |
| `pkg/mcp/handlers_sse.go` | SSE proxy handlers | Fix `extractServerID()` (C2) and `rewriteEndpointData()` (C3) |
| `pkg/mcp/handlers_streamable.go` | Streamable HTTP proxy handlers | Verify `extractServerID` call works after fix (no direct changes) |
| `pkg/mcp/handlers_api.go` | Management CRUD handlers + `MCPStatusResponse` struct | Remove `Port` from struct, update `Enabled` check (RW1) |
| `cmd/main.go` | Server startup and wiring | Major: new init trigger, remove goroutine, add proxy registration |

## Constraints
- Handler logic remains functionally equivalent — only path extraction and URL rewriting are updated (W1)
- MCP module must still be optional — controlled by `MCP_ENABLED` env var
- The `proxyAuthMiddleware` function in `auth.go` is unchanged — it's applied at route registration level, not handler level
- SSE connection registry cleanup must still work on shutdown
- `TrimPrefix` calls in `handlers_api.go` are verified correct — management paths unchanged
- `extractServerID()` handles `/v1/mcp/{id}/` only — no backward compat for old `/mcp/{id}/` prefix (RS1, dead code)
- `RegisterAPIHandlers` nil guard checks `s.store == nil` only; `RegisterProxyHandlers` checks `s.tokenStore == nil` (RW2)

## Deliverables
- [ ] Proxy endpoints registered on main server at `/v1/mcp/{id}/*` with `proxyAuthMiddleware`
- [ ] Management endpoints at `/fe/api/mcp-servers/*` with no auth
- [ ] No separate MCP HTTP server (removed `setupRoutes`, `Start`, `httpServer`)
- [ ] `extractServerID()` correctly parses `/v1/mcp/{id}/*` paths (C2)
- [ ] `rewriteEndpointData()` returns `/v1/mcp/{id}/messages` URLs to clients (C3)
- [ ] `GetMCPProxyPort()` removed (W7)
- [ ] `MCPStatusResponse` struct has no `Port` field, `Enabled` checks `s.store != nil` (RW1)
- [ ] `RegisterAPIHandlers` nil guard checks `s.store == nil` only; `RegisterProxyHandlers` checks `s.tokenStore == nil` (RW2)
- [ ] `go build` passes
