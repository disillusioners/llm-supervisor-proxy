# Phase 2: Frontend, Config & Init Trigger Updates

## Objective
Update the frontend MCP management UI to reflect the new architecture: remove port display, update the "Set MCP_PROXY_PORT" message, remove `port` from TypeScript types, and ensure the MCP status endpoint response is consumed correctly. No frontend API path changes needed — management stays at `/fe/api/mcp-servers/*`.

## Coupling
- **Depends on**: Phase 1 (backend status response format changed, `port` field removed)
- **Coupling type**: loose — only depends on knowing the new status response shape (no `port` field)
- **Shared files with other phases**:
  - `pkg/ui/frontend/src/types.ts` — MCPServerStatus type (Phase 3 tests don't touch this)
  - `pkg/ui/frontend/src/components/mcp/MCPServersTab.tsx` — status display (W2 corrected paths)
  - `cmd/main.go` — MCP_ENABLED env var (already done in Phase 1 task 14)
- **Shared APIs/interfaces**: None — frontend only consumes HTTP endpoints

## Context
- Phase 1 changed status endpoint response from `{"enabled": bool, "port": int}` to `{"enabled": bool}` (C5)
- Phase 1 replaced `MCP_PROXY_PORT` with `MCP_ENABLED` env var (C6)
- Management API paths (`/fe/api/mcp-servers/*`) are **unchanged** — no frontend path updates needed (C4)
- Frontend components are in `components/mcp/`, NOT `components/config/` (W2)

### What Does NOT Need Changing
- `useApi.ts` API paths — all MCP calls already use `/mcp-servers` which prepends to `/fe/api/mcp-servers`. Correct. (C4)
- `MCPServerForm.tsx` — form makes no direct API calls, uses parent callbacks.
- No connection URL display in the frontend — MCPServersTab shows upstream URLs and transport type only, not proxy endpoint URLs. (W9 — task removed)

## Tasks

### Part A: TypeScript Type Updates (C5)

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | **Remove `port` from `MCPServerStatus`** | `types.ts` line 429: Change from `{ enabled: boolean; port: number; }` to `{ enabled: boolean; }`. The backend no longer returns `port` in the status response. | `pkg/ui/frontend/src/types.ts` |

### Part B: MCPServersTab Updates (W3)

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 2 | **Update "Set MCP_PROXY_PORT" message** | `MCPServersTab.tsx` line ~94: Change `"Set the MCP_PROXY_PORT environment variable to enable MCP server proxying."` to `"Set MCP_ENABLED=true environment variable to enable MCP server proxying."` | `pkg/ui/frontend/src/components/mcp/MCPServersTab.tsx` |
| 3 | **Remove port display from MCP status area** | If the status section displays port info (e.g., "MCP Proxy running on port X"), remove it. Show only enabled/disabled state. | `pkg/ui/frontend/src/components/mcp/MCPServersTab.tsx` |
| 4 | **Verify `useMCPStatus` hook works without `port`** | `useApi.ts` line 689: `useMCPStatus` fetches status. After removing `port` from the type, ensure no code references `status.port`. Search for `.port` references in the component. | `pkg/ui/frontend/src/hooks/useApi.ts`, `pkg/ui/frontend/src/components/mcp/MCPServersTab.tsx` |

### Part C: Backend Frontend Handler Review

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 5 | **Check pkg/ui/server.go for MCP route references** | Verify no routes in `pkg/ui/server.go` reference `/fe/api/mcp-servers/` or `/fe/api/mcp/status`. These are handled by `pkg/mcp/mcp.go:RegisterAPIHandlers()` directly — but confirm no duplicate/conflicting registrations exist. | `pkg/ui/server.go` |

## Key Files

| File | Purpose | Change Type |
|------|---------|-------------|
| `pkg/ui/frontend/src/types.ts` | TypeScript types | Remove `port` from MCPServerStatus |
| `pkg/ui/frontend/src/components/mcp/MCPServersTab.tsx` | MCP management tab | Update env var message, remove port display |
| `pkg/ui/frontend/src/hooks/useApi.ts` | API hooks | Verify no `.port` references (may not need changes) |
| `pkg/ui/server.go` | Frontend server routes | Verify no conflicting MCP registrations |

## Constraints
- Frontend must build successfully (`npm run build`)
- MCP management CRUD must still function identically
- No API path changes — only display/config text changes
- Verify no TypeScript compilation errors from type change

## Deliverables
- [ ] `MCPServerStatus` type has no `port` field
- [ ] "Set MCP_PROXY_PORT" message updated to "MCP_ENABLED=true"
- [ ] No port display in MCP status UI
- [ ] No TypeScript errors from `port` removal
- [ ] `npm run build` passes
- [ ] MCP CRUD operations work in browser
