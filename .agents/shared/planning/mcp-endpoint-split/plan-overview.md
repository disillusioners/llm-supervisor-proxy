# Plan Overview: MCP Endpoint Split

## Objective
Split the MCP module into two distinct endpoint types on the **main server**: (1) Management CRUD endpoints stay at `/fe/api/mcp-servers/*` with **no auth** (k8s handles access), and (2) Proxy/Usage endpoints move to `/v1/mcp/{id}/*` with **proxy token auth** (same as `/v1/chat/completions`). Eliminate the separate MCP proxy server entirely — everything runs on the main port.

## Scope Assessment
**Medium-Large** — Touches routing, auth middleware, server startup, path extraction in handlers, frontend API paths, and configuration. ~15-20 files, but well-contained within the MCP module and cmd/main.go.

## Context
- Project: llm-supervisor-proxy
- Working Directory: /Users/nguyenminhkha/All/Code/opensource-projects/llm-supervisor-proxy
- Branch suggestion: `feature/mcp-endpoint-split`

## Current State → Target State

### Current Architecture
```
Main Server (port 4321)
├── /fe/api/mcp/status          → status (no auth)
├── /fe/api/mcp-servers/*       → CRUD management (proxyAuthMiddleware)
├── /v1/chat/completions        → chat proxy (handler-integrated auth)
├── /v1/messages                → messages proxy (handler-integrated auth)
└── /ui/*, /fe/api/*            → frontend

MCP Proxy Server (MCP_PROXY_PORT - separate goroutine)
├── /mcp/{id}/sse               → SSE proxy (proxyAuthMiddleware)
├── /mcp/{id}/messages          → SSE messages proxy (proxyAuthMiddleware)
├── /mcp/{id}/                  → Streamable HTTP proxy (proxyAuthMiddleware)
└── /mcp/{id}                   → Streamable HTTP proxy (proxyAuthMiddleware)
```

### Target Architecture
```
Main Server (port 4321) — SINGLE SERVER
├── /fe/api/mcp/status          → status (no auth) [unchanged path]
├── /fe/api/mcp-servers/*       → CRUD management (no auth) [unchanged path, auth removed]
├── /v1/mcp/{id}/sse            → SSE proxy (proxy token auth, middleware wrapper)
├── /v1/mcp/{id}/messages       → SSE messages proxy (proxy token auth, middleware wrapper)
├── /v1/mcp/{id}/               → Streamable HTTP proxy (proxy token auth, middleware wrapper)
├── /v1/mcp/{id}                → Streamable HTTP proxy (proxy token auth, middleware wrapper)
├── /v1/chat/completions        → chat proxy (unchanged)
├── /v1/messages                → messages proxy (unchanged)
└── /ui/*, /fe/api/*            → frontend (unchanged)
```

### Security Model — K8s Ingress Separation
```
k8s/templates/ingress-protected.yaml  → /fe/api/mcp-servers/* (management CRUD, internal only)
k8s/templates/ingress-public.yaml     → /v1/mcp/* (proxy endpoints, token-authenticated)
```
**No application-level auth on CRUD.** K8s network policies enforce access control for management endpoints. Proxy endpoints use proxy token auth at the application level (same as `/v1/chat/completions`).

## Phase Index

| Phase | Name | Objective | Dependencies | Coupling | Est. Time |
|-------|------|-----------|-------------|----------|-----------|
| 1 | Backend Routing, Auth & Handler Path Updates | Move proxy routes to main server at `/v1/mcp/*`, remove separate MCP server, update all hardcoded paths in handlers, add middleware auth for proxy | None | — | 4-5h |
| 2 | Frontend, Config & Init Trigger Updates | Update frontend MCP status display, remove MCP_PROXY_PORT references, define new init trigger | Phase 1 | loose | 1-2h |
| 3 | Tests & Verification | Update all tests for new paths, add auth routing tests, E2E verification, rewrite mcp_test.go | Phase 1+2 | tight | 3h |

### Coupling Assessment

| Transition | Coupling | Rationale |
|-----------|----------|-----------|
| Phase 1 → Phase 2 | **loose** | Phase 2 only changes frontend status display and config — depends on knowing new behavior (no separate port) but not on internal implementation |
| Phase 1 → Phase 3 | **tight** | Phase 3 tests directly reference route registrations and auth behavior from Phase 1 |
| Phase 2 → Phase 3 | **loose** | Phase 3 tests frontend paths but those are unchanged (`/fe/api/mcp-servers/*` stays) |

## Key Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Management path | `/fe/api/mcp-servers/*` — **unchanged** | Frontend `apiFetch()` prepends `/fe/api` base to all paths. Changing would break the base path convention. Only proxy routes get new paths. (C4) |
| MCP proxy path | `/v1/mcp/{id}/*` | Aligns with `/v1/chat/completions` and `/v1/messages` — proxy endpoints under `/v1/` namespace |
| Auth for proxy | **Middleware wrapper** (`proxyAuthMiddleware`) applied at route registration in `RegisterProxyHandlers()` | Cleaner than handler-integrated auth. Existing `proxyAuthMiddleware` in `pkg/mcp/auth.go` already works — just route it to the right handler chain. (W4) |
| Management auth | **Remove** `proxyAuthMiddleware` from management routes | K8s ingress handles access control at network level (C7/C8) |
| MCP_PROXY_PORT | **Deprecate** with warning log | Log warning if set. New trigger: `MCP_ENABLED` env var or always-init when store exists (see Phase 2 task) |
| GetMCPProxyPort() | **Remove entirely** | No callers after refactor (status endpoint returns `enabled: bool` only, no port) (W7) |
| MCPServerStatus.port | **Remove from response and types** | No separate port exists anymore. Backend returns `{enabled: bool}` only (C5) |
| MCP init trigger | **`MCP_ENABLED` env var** (bool) | Explicit opt-in. Empty = disabled. "true"/"1" = enabled. Clearer than presence-of-port (C6) |

## Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Breaking change for existing MCP users using separate port | High | Log deprecation warning if `MCP_PROXY_PORT` is set; document migration path in commit message |
| Auth regression — management endpoints exposed without auth | High | K8s ingress policies enforce separation; document in k8s templates; add clear code comments |
| SSE `extractServerID()` breaks with new path prefix | Critical (C2) | Explicit task to update path parsing in Phase 1 |
| SSE `rewriteEndpointData()` sends wrong URLs to clients | Critical (C3) | Explicit task to update hardcoded URL in Phase 1 |
| Frontend shows stale port info | Low | Remove `port` from status response and types (C5, W5) |
| "Set MCP_PROXY_PORT" message outdated in UI | Low | Explicit task to update in Phase 2 (W3) |

| `MCPStatusResponse` Go struct port field | Low | Remove `Port` from `MCPStatusResponse` struct + update `Enabled` to check `s.store != nil` (RW1) |
| `RegisterAPIHandlers` nil guard overchecks `tokenStore` | Low | Move `tokenStore == nil` guard to `RegisterProxyHandlers()` only (RW2) |

## Success Criteria
- [ ] MCP management CRUD works at `/fe/api/mcp-servers/*` with no auth
- [ ] MCP proxy works at `/v1/mcp/{id}/*` with proxy token auth (middleware)
- [ ] No separate MCP server process — everything on main port
- [ ] SSE proxy still works end-to-end (clients receive correct `/v1/mcp/{id}/messages` URLs)
- [ ] Streamable HTTP proxy still works end-to-end
- [ ] Frontend MCP management UI still functions correctly
- [ ] `extractServerID()` correctly parses `/v1/mcp/{id}/` paths
- [ ] All `TrimPrefix` calls in `handlers_api.go` use correct prefix
- [ ] All existing tests pass + new auth routing tests added
- [ ] `mcp_test.go` rewritten (removed function tests updated for new signatures)
- [ ] `e2e_test.go` port field assertions removed
- [ ] `handlers_api_test.go` auth expectations converted to unauthenticated
- [ ] `MCP_ENABLED` env var controls module activation
- [ ] `MCP_PROXY_PORT` triggers deprecation warning if set
- [ ] `go test ./...`, `go vet ./...`, `npm run build` all pass

## Tracking
- Created: 2026-05-20
- Last Updated: 2026-05-20 (v3 — Approver fixes A1/A2, Reviewer warnings RW1-RW3, RS1)
- Status: draft
