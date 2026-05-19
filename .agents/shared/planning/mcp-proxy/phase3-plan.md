# Phase 3: Management REST API + Frontend

## Objective
Create the REST API endpoints for CRUD management of MCP servers (including connection testing and connection cleanup on delete) and the frontend MCP tab with enabled/disabled detection. This phase fills the `RegisterAPIHandlers()` stub from Phase 1 independently from Phase 2's `setupRoutes()`.

## Coupling
- **Depends on**: Phase 1 (loose for data model) + Phase 2 (loose for `ConnectionRegistry`)
- **Coupling type**: loose — uses `MCPStore` CRUD methods and `ConnectionRegistry.CloseConnections()` for delete cleanup
- **Shared files with other phases**: `pkg/mcp/store.go` (read-only consumer), `pkg/mcp/types.go` (data types), `pkg/mcp/mcp.go` (fills `RegisterAPIHandlers()` only — B1), `pkg/mcp/proxy.go` (uses `ConnectionRegistry`)
- **Shared APIs/interfaces**: `MCPStore.ListServers()`, `GetServer()`, `CreateServer()`, `UpdateServer()`, `DeleteServer()`, `ConnectionRegistry.CloseConnections(serverID)`
- **Why this coupling**: API handlers need the store for persistence. Delete handler needs `ConnectionRegistry` to close active SSE connections before DB deletion (B2).

## Context
- Phase 1 delivered: `MCPServer` struct, `MCPStore` with CRUD, `mcp.Server`, validation functions
- Existing pattern: `pkg/ui/server.go` has `RegisterHandlers(mux)` with `/fe/api/` routes
- Frontend pattern: `SettingsPage.tsx` with `TabType` union, tab buttons, conditional rendering
- API pattern: `apiFetch<T>()` helper, hooks return `{ data, loading, error, refetch, mutate }`

## API Design

### REST Endpoints

All endpoints under `/fe/api/mcp-servers` (following existing `/fe/api/` convention):

| Method | Path | Description | Handler |
|--------|------|-------------|---------|
| GET | `/fe/api/mcp-servers/status` | Check if MCP module is enabled + configured port | `handleMCPServersStatus` |
| GET | `/fe/api/mcp-servers` | List all MCP servers (auth_token masked) | `handleListMCPServers` |
| GET | `/fe/api/mcp-servers/{id}` | Get single MCP server (auth_token masked) | `handleGetMCPServer` |
| POST | `/fe/api/mcp-servers` | Create new MCP server | `handleCreateMCPServer` |
| PUT | `/fe/api/mcp-servers/{id}` | Update MCP server | `handleUpdateMCPServer` |
| DELETE | `/fe/api/mcp-servers/{id}` | Delete MCP server (closes active connections first) | `handleDeleteMCPServer` |
| POST | `/fe/api/mcp-servers/{id}/test` | Test connection to upstream MCP server | `handleTestMCPServer` |

### Test Connection Endpoint (C6)

**How it tests:**

| Transport | Test Method | Success Criteria |
|-----------|------------|------------------|
| **SSE** | `GET {upstream_url}/sse` with 5s timeout | Response status 200 AND `Content-Type` contains `text/event-stream` |
| **Streamable HTTP** | `POST {upstream_url}` with empty body + `Mcp-Session-Id` header with 5s timeout | Response status 200 or 202 (doesn't need to be valid MCP response, just reachable) |

```json
// POST /fe/api/mcp-servers/{id}/test — Response
{
    "success": true,
    "latency_ms": 45,
    "transport": "streamable_http",
    "error": ""
}

// Failure response
{
    "success": false,
    "latency_ms": 5000,
    "transport": "sse",
    "error": "connection refused: upstream unreachable at http://remote:3001"
}
```

### Status Endpoint (NB2)

Frontend needs to detect if MCP module is enabled to show appropriate UI.

```json
// GET /fe/api/mcp-servers/status — Response
{
    "enabled": true,
    "port": 4322
}

// When MCP_PROXY_PORT is not set
{
    "enabled": false,
    "port": 0
}
```

**Implementation**: Reads `GetMCPProxyPort()` (env var). Does not query DB. Lightweight — can be called on every SettingsPage load.

### Delete Handler with Connection Cleanup (B2)

Deleting an MCP server must close all active SSE connections to that upstream before removing the DB record:

```go
func (s *Server) handleDeleteMCPServer(w http.ResponseWriter, r *http.Request) {
    serverID := extractID(r.URL.Path)
    
    // 1. Close all active SSE connections for this server (B2)
    closed := s.connMgr.registry.CloseConnections(serverID)
    if closed > 0 {
        log.Printf("[MCP] Closed %d active connections for server %s before deletion", closed, serverID)
    }
    
    // 2. Delete from database
    if err := s.store.DeleteServer(r.Context(), serverID); err != nil {
        http.Error(w, `{"error":"failed to delete"}`, http.StatusInternalServerError)
        return
    }
    
    // 3. Publish event
    s.bus.Publish(events.Event{
        Type:      "mcp_server_deleted",
        Timestamp: time.Now().Unix(),
        Data:      map[string]interface{}{"server_id": serverID, "connections_closed": closed},
    })
    
    w.WriteHeader(http.StatusNoContent)
}
```

### Token Masking (C4)

All GET responses mask the `auth_token` field:

```go
func maskAuthToken(token string) string {
    if token == "" || len(token) <= 9 {
        return "***"
    }
    return token[:6] + "***" + token[len(token)-3:]
}
```

### API Response Format

```json
// GET /fe/api/mcp-servers
[
  {
    "id": "uuid-1234",
    "name": "My Remote MCP",
    "description": "Production MCP server",
    "upstream_url": "http://remote-host:3001",
    "transport_type": "streamable_http",
    "auth_type": "bearer",
    "auth_token": "sk-abc***xyz",
    "headers": "{}",
    "enabled": true,
    "created_at": "2026-05-19T10:00:00Z",
    "updated_at": "2026-05-19T10:00:00Z"
  }
]

// POST /fe/api/mcp-servers (request)
{
  "name": "My MCP Server",
  "upstream_url": "http://remote:3001",
  "transport_type": "streamable_http",
  "auth_type": "bearer",
  "auth_token": "secret-token-here",
  "enabled": true
}
```

### Validation on Create/Update (W10)

API handlers validate before calling store:
- `name`: non-empty, unique (enforced by DB)
- `upstream_url`: valid URL, passes `ValidateUpstreamURL()` (SSRF check)
- `transport_type`: passes `TransportType.Valid()`
- `auth_type`: passes `AuthType.Valid()`
- `headers`: passes `ValidateCustomHeaders()` (valid JSON, no blocked headers)

## Frontend Design

### SettingsPage Tab Registration

```typescript
// In types.ts
export interface MCPServer {
  id: string;
  name: string;
  description: string;
  upstream_url: string;
  transport_type: 'sse' | 'streamable_http';
  auth_type: 'none' | 'bearer' | 'basic' | 'api_key';
  auth_token?: string;
  headers: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}
```

```typescript
// In SettingsPage.tsx — add to TabType
type TabType = 'proxy' | 'models' | 'credentials' | 'loop_detection' | 'tool_repair' | 'tokens' | 'usage' | 'mcp_servers';

// Add tab button (after Usage tab):
<button
  class={`px-6 py-3 font-medium transition-colors whitespace-nowrap ${activeTab === 'mcp_servers'
    ? 'text-blue-400 border-b-2 border-blue-400'
    : 'text-gray-400 hover:text-white'
  }`}
  onClick={() => setActiveTab('mcp_servers')}
>
  🔌 MCP Servers
</button>

// Add tab content:
{activeTab === 'mcp_servers' && (
  <MCPServersTab setStatus={setStatusWrapper} />
)}
```

### MCPServersTab Component

```
┌─────────────────────────────────────────────────────────┐
│  MCP Servers                              [+ Add Server] │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  ┌─ My Remote MCP ──────────────────────────────────┐  │
│  │  URL: http://remote:3001                          │  │
│  │  Transport: Streamable HTTP                       │  │
│  │  Auth: Bearer Token                               │  │
│  │  Status: ● Connected  Latency: 45ms              │  │
│  │                                    [Edit] [Delete]│  │
│  └───────────────────────────────────────────────────┘  │
│                                                         │
│  ┌─ Legacy SSE Server ──────────────────────────────┐  │
│  │  URL: http://old-server:8080                      │  │
│  │  Transport: SSE                                   │  │
│  │  Auth: None                                       │  │
│  │  Status: ○ Disabled                               │  │
│  │                                    [Edit] [Delete]│  │
│  └───────────────────────────────────────────────────┘  │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

### MCPServerForm Modal

```
┌─────────────────────────────────────────────────────────┐
│  Add MCP Server                                    [X]  │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  Name:        [________________________]                │
│  Description: [________________________]                │
│  Upstream URL: [________________________]               │
│  Transport:   [Streamable HTTP ▼]                       │
│                                                         │
│  ── Authentication ──                                   │
│  Type:  [None ▼]                                        │
│  Token: [________________________]  (if bearer/api_key) │
│                                                         │
│  ── Custom Headers ──                                   │
│  (JSON format)                                          │
│  [{"key": "value"}]                                     │
│                                                         │
│  ☑ Enabled                                              │
│                                                         │
│        [Cancel]              [Test Connection] [Save]   │
└─────────────────────────────────────────────────────────┘
```

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Create `pkg/mcp/handlers_api.go` | REST API handlers: list, get, create, update, **delete (with connection cleanup — B2)**, test connection, **status (NB2)**. Follow existing `pkg/ui/server.go` handler pattern. Validate inputs using Phase 1 validation functions. Mask `auth_token` in GET responses using `maskAuthToken()`. Implement test connection with transport-specific logic (C6). Delete handler calls `ConnectionRegistry.CloseConnections(serverID)` before DB deletion. | `pkg/mcp/handlers_api.go` |
| 2 | Fill `RegisterAPIHandlers()` in `pkg/mcp/mcp.go` (B1) | Fill the `RegisterAPIHandlers(mux)` stub from Phase 1 with management API routes: `/fe/api/mcp-servers/status`, `/fe/api/mcp-servers`, `/fe/api/mcp-servers/`. This method is ONLY for main-server API routes — Phase 2 handles `setupRoutes()` independently. | `pkg/mcp/mcp.go` |
| 3 | Add `MCPServer` type to `types.ts` | TypeScript interface matching Go struct | `pkg/ui/frontend/src/types.ts` |
| 4 | Add `useMCPServers()` hook + CRUD functions + status check | `useMCPServers()` hook with `servers`, `loading`, `error`, `refetch`. `useMCPStatus()` hook returning `{ enabled, port }`. Standalone functions: `createMCPServer()`, `updateMCPServer()`, `deleteMCPServer()`, `testMCPServer()`. Follow existing `apiFetch<T>` pattern. | `pkg/ui/frontend/src/hooks/useApi.ts` |
| 5 | Create `MCPServersTab.tsx` | List view with server cards, status indicators, edit/delete actions, add button, empty state. **On mount, fetch `/fe/api/mcp-servers/status`**: if `enabled: false`, show "MCP Proxy is not enabled. Set MCP_PROXY_PORT environment variable to enable." with no server list (NB2). | `pkg/ui/frontend/src/components/mcp/MCPServersTab.tsx` |
| 6 | Create `MCPServerForm.tsx` | Modal form for add/edit with: name, description, upstream_url, transport_type dropdown, auth_type dropdown (conditional token field), headers JSON input, enabled toggle, test connection button. | `pkg/ui/frontend/src/components/mcp/MCPServerForm.tsx` |
| 7 | Register MCP tab in SettingsPage | Add `'mcp_servers'` to `TabType`, add tab button, add conditional rendering of `<MCPServersTab />`. | `pkg/ui/frontend/src/components/SettingsPage.tsx` |
| 8 | Write API handler tests | Test CRUD endpoints with `httptest`, mock store, verify JSON responses, error handling, token masking, input validation, test connection (mock upstream). | `pkg/mcp/handlers_api_test.go` |

## Key Files

### New Files
- `pkg/mcp/handlers_api.go` — REST API handlers with validation + test connection
- `pkg/mcp/handlers_api_test.go` — API handler tests
- `pkg/ui/frontend/src/components/mcp/MCPServersTab.tsx` — MCP tab component
- `pkg/ui/frontend/src/components/mcp/MCPServerForm.tsx` — MCP form modal

### Modified Files
- `pkg/ui/frontend/src/types.ts` — Add `MCPServer` interface
- `pkg/ui/frontend/src/hooks/useApi.ts` — Add `useMCPServers()` + CRUD functions
- `pkg/ui/frontend/src/components/SettingsPage.tsx` — Add `mcp_servers` tab

## Constraints
- API handlers follow existing pattern: `w.Header().Set("Content-Type", "application/json")`, `json.NewEncoder(w).Encode()`, `http.Error()` for errors
- `auth_token` must be masked in GET responses (first 6 chars + "***" + last 3 chars)
- All inputs validated using Phase 1 validation functions before store operations
- Test connection uses 5s timeout, transport-specific test logic (C6)
- **Delete handler must close active connections via `ConnectionRegistry.CloseConnections(serverID)` before DB deletion** (B2)
- `RegisterAPIHandlers()` only touches its own method in `mcp.go` — not `setupRoutes()` (B1)
- Status endpoint (`/fe/api/mcp-servers/status`) returns enabled state + port, no DB query (NB2)
- Frontend shows disabled message when `enabled: false` from status endpoint (NB2)
- No new dependencies — use existing Preact, Tailwind, and browser fetch API
- MCP tab is self-contained — all state management within `MCPServersTab` component

## Deliverables
- [ ] 7 REST API endpoints (status, list, get, create, update, delete, test)
- [ ] Delete handler closes active SSE connections before DB deletion (B2)
- [ ] Status endpoint returns `{ enabled, port }` from env var (NB2)
- [ ] Input validation on create/update (URL, enum, headers)
- [ ] `auth_token` masking in GET responses
- [ ] Test connection endpoint with transport-specific logic (SSE vs Streamable HTTP)
- [ ] `MCPServer` TypeScript type
- [ ] `useMCPServers()` hook with full CRUD + `useMCPStatus()` hook (NB2)
- [ ] `MCPServersTab` component with list view, add/edit/delete flows, disabled state detection
- [ ] `MCPServerForm` modal with all fields + test connection button
- [ ] Tab registered in SettingsPage
- [ ] `RegisterAPIHandlers()` filled independently from `setupRoutes()` (B1)
- [ ] API handler test coverage (CRUD, validation, masking, test connection, connection cleanup on delete)
