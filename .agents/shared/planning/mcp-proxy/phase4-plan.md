# Phase 4: Integration Wiring & Testing

## Objective
Wire the MCP proxy module into `cmd/main.go`, run all existing tests to verify no regressions, and perform end-to-end testing of the complete MCP proxy flow including authentication, endpoint rewriting, and encrypted storage. This phase ties Phases 1-3 together into a working system.

## Coupling
- **Depends on**: Phases 1, 2, 3 (tight)
- **Coupling type**: tight — imports from `pkg/mcp/`, starts the MCP HTTP server alongside the LLM proxy, passes auth token store
- **Shared files with other phases**: `cmd/main.go` (the integration point)
- **Why this coupling**: This is the assembly phase — everything comes together here

## Context
- Phase 1 delivered: `mcp.NewServer()`, `mcp.MCPStore` (with crypto), `mcp.GetMCPProxyPort()`, validation
- Phase 2 delivered: Auth middleware, SSE + Streamable HTTP proxy handlers with endpoint rewriting, `ConnectionManager`
- Phase 3 delivered: REST API handlers with test connection, frontend MCP tab
- `cmd/main.go` currently: initializes DB, config, bus, proxy handler, UI server, starts single `http.Server`
- Auth: `auth.NewTokenStore(dbStore.DB, dbStore.Dialect)` already created in main.go

## Wiring Design

### Changes to `cmd/main.go`

```go
// Add import:
import "github.com/disillusioners/llm-supervisor-proxy/pkg/mcp"

// After existing proxy handler initialization (~line 106):

// Initialize MCP Proxy Server (optional — only if MCP_PROXY_PORT is set)
var mcpServer *mcp.Server
if mcpPort := mcp.GetMCPProxyPort(); mcpPort > 0 {
    mcpStore := mcp.NewMCPStore(dbStore.DB, dbStore.Dialect)
    mcpServer = mcp.NewServer(mcpPort, mcpStore, bus, tokenStore)
    
    // Register MCP management API routes on the main mux (for frontend access)
    mcpServer.RegisterAPIHandlers(mux)
    
    // Start MCP proxy server on separate port (goroutine)
    go func() {
        log.Printf("MCP Proxy Server starting on port %d", mcpPort)
        log.Printf("MCP SSE endpoint: http://localhost:%d/mcp/{server_id}/sse", mcpPort)
        log.Printf("MCP HTTP endpoint: http://localhost:%d/mcp/{server_id}/http", mcpPort)
        if err := mcpServer.Start(ctx); err != nil && err != http.ErrServerClosed {
            log.Printf("MCP Proxy Server error: %v", err)
        }
    }()
} else {
    log.Printf("MCP Proxy disabled (set MCP_PROXY_PORT to enable)")
}

// Modify graceful shutdown (~line 179):
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
if err := srv.Shutdown(ctx); err != nil {
    log.Fatal("Server forced to shutdown:", err)
}
if mcpServer != nil {
    if err := mcpServer.Stop(ctx); err != nil {
        log.Printf("MCP Proxy shutdown error: %v", err)
    }
}
```

### Route Design Confirmation

- **Management API** (`/fe/api/mcp-servers/*`) → main LLM proxy port (:4321) — no CORS issues for frontend
- **MCP proxy transport** (`/mcp/{id}/sse`, `/mcp/{id}/messages`, `/mcp/{id}/http`) → MCP proxy port (:4322) — where MCP clients connect
- **Auth middleware** only on MCP transport routes (port :4322), NOT on management API (uses existing UI auth pattern)

### NewServer Signature

```go
// Constructor accepts tokenStore from day one (NB1 — no retrofit needed)
func NewServer(port int, store *MCPStore, bus *events.Bus, tokenStore auth.TokenStoreInterface) *Server
```

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Wire MCP module in `cmd/main.go` | Add import for `pkg/mcp`. Call `mcp.GetMCPProxyPort()`. Conditionally create `MCPStore` + `mcp.NewServer(port, mcpStore, bus, tokenStore)` (tokenStore already in constructor from Phase 1). Call `RegisterAPIHandlers(mux)`. Start MCP server goroutine. Add to graceful shutdown. ~25 lines of new code. | `cmd/main.go` |
| 2 | Run all existing tests | `go test ./...` — verify zero regressions. All 24+ existing packages must pass. | — |
| 3 | End-to-end MCP SSE proxy test | Test: start MCP proxy server → create mock upstream SSE server → create MCP server config via store → connect authenticated SSE client to proxy → verify endpoint URL rewritten (single-line and multi-line data) → send message via proxy → verify response forwarded → test upstream disconnect closes client stream. | `pkg/mcp/e2e_test.go` |
| 4 | End-to-end Streamable HTTP test | Same flow for Streamable HTTP: POST to proxy → verify forwarded to upstream → verify SSE response streamed back → verify `Mcp-Session-Id` passthrough. | `pkg/mcp/e2e_test.go` |
| 5 | End-to-end auth test | Test: no auth header → 401, invalid token → 401, valid token → 200. | `pkg/mcp/e2e_test.go` |
| 6 | End-to-end encryption test | Test: create server with auth_token → verify encrypted in DB (raw SQL check) → get server → verify decrypted correctly. | `pkg/mcp/e2e_test.go` |
| 7 | End-to-end connection cleanup test | Test: create server → connect SSE client → delete server → verify client stream closed → verify server absent from DB. (B2) | `pkg/mcp/e2e_test.go` |
| 8 | End-to-end status endpoint test | Test: verify `/fe/api/mcp-servers/status` returns `{ enabled: true, port: N }` when MCP_PROXY_PORT set, `{ enabled: false, port: 0 }` when unset. (NB2) | `pkg/mcp/e2e_test.go` |
| 9 | Frontend build test | `cd pkg/ui/frontend && npm run build` — verify frontend compiles with new MCP tab. `go build ./cmd` — verify Go binary builds. | — |
| 10 | Disabled-mode verification | Test that when `MCP_PROXY_PORT` is unset, no MCP server starts, no errors, all existing functionality works. | Manual / test |
| 11 | Connection test endpoint E2E | Verify `/fe/api/mcp-servers/{id}/test` correctly detects reachable/unreachable upstreams for both SSE and Streamable HTTP transports. | `pkg/mcp/e2e_test.go` |

## Key Files
- `cmd/main.go` — Integration wiring (MODIFY)
- `pkg/mcp/e2e_test.go` — End-to-end integration tests (NEW)

## Constraints
- `cmd/main.go` changes must be minimal — only ~25 lines of new code
- Existing test suites must pass without any changes
- No changes to `pkg/proxy/`, `pkg/providers/`, or any existing handler files
- Frontend must build cleanly with `npm run build`
- Go binary must build cleanly with `go build ./cmd`
- MCP module must be completely optional — all tests pass whether or not `MCP_PROXY_PORT` is set
- `tokenStore` is already in `NewServer()` constructor from Phase 1 (NB1) — no retrofit needed
- `tokenStore` is already created in `cmd/main.go` at line 92 — reuse, don't recreate

## Testing Strategy

### Unit Tests (per phase, already covered)
- **Phase 1**: Store CRUD (encrypt/decrypt), validation (URL SSRF, headers, enums), boolean conversion
- **Phase 2**: Auth middleware, SSE forwarding + multi-line endpoint rewriting (B3), HTTP forwarding, connection loss, ConnectionRegistry (B2)
- **Phase 3**: API handler CRUD, token masking, test connection, input validation, connection cleanup on delete (B2), status endpoint (NB2)

### E2E Integration Tests (Phase 4)
- **MCP SSE proxy E2E**: Mock upstream → MCP proxy → Authenticated client
  - Connect with valid token → receive events with rewritten endpoint URL (single-line and multi-line data)
  - Send message via proxy → verify forwarded to upstream
  - Verify auth headers injected on upstream requests
  - Upstream disconnect → client stream closed (no reconnect)
  - Invalid token → 401
- **MCP Streamable HTTP E2E**: Same flow for HTTP transport
  - POST with valid token → response streamed back
  - `Mcp-Session-Id` passthrough (request and response)
- **Encryption E2E**: Create server with auth_token → raw DB query shows encrypted value → API GET shows masked → store GetServer returns decrypted
- **Connection cleanup E2E** (B2): Create server → connect SSE client → delete server → verify client stream closed → verify server absent from DB
- **Connection test E2E**: Reachable upstream → `{success: true, latency_ms: N}`, unreachable → `{success: false, error: "..."}`
- **Status endpoint E2E** (NB2): MCP_PROXY_PORT set → `{enabled: true, port: N}`, unset → `{enabled: false, port: 0}`

### Regression Tests
- `go test ./...` — all existing packages pass (0 regressions)
- `go vet ./...` — no vet issues
- Frontend build succeeds

## Deliverables
- [ ] MCP proxy server starts conditionally based on `MCP_PROXY_PORT` env var
- [ ] Management API accessible on main LLM proxy port (:4321)
- [ ] MCP transport proxy accessible on separate MCP port (:4322)
- [ ] Auth middleware active on all transport routes
- [ ] Graceful shutdown for both servers
- [ ] All existing tests pass (0 regressions)
- [ ] E2E test: SSE proxy flow with multi-line endpoint rewriting + auth (B3)
- [ ] E2E test: Streamable HTTP proxy flow with auth
- [ ] E2E test: Encryption at rest (encrypted in DB, decrypted on read)
- [ ] E2E test: Connection cleanup on server deletion (B2)
- [ ] E2E test: Connection test endpoint
- [ ] E2E test: Status endpoint returns correct enabled/port state (NB2)
- [ ] Go build + frontend build succeed
- [ ] Disabled mode: no errors when MCP_PROXY_PORT unset
