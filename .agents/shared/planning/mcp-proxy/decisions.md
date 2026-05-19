# Architecture Decisions: MCP Proxy Server

## AD-1: MCP Port Configuration via Environment Variable

**Decision**: Use `MCP_PROXY_PORT` environment variable instead of adding to `Config` struct.

**Rationale**:
- Avoids modifying `Config` struct, `ManagerInterface`, and all its implementations
- MCP module is truly additive — zero changes to existing config system
- MCP port is infrastructure-level config (like `DATABASE_URL`), not application config
- Simpler for deployment: one env var to enable/disable

**Alternatives considered**:
- Add `mcp_proxy_port` to `Config` JSON — requires modifying 4+ files, breaking config interface
- Separate config file — over-engineering for a single integer

---

## AD-2: Management API on Main Port, Proxy Transport on MCP Port

**Decision**: 
- `/fe/api/mcp-servers/*` (CRUD management) → main LLM proxy port (:4321)
- `/mcp/{id}/sse`, `/mcp/{id}/messages`, `/mcp/{id}/http` (transport) → MCP proxy port (:4322)

**Rationale**:
- Frontend is served on main port — API calls from browser must avoid CORS
- MCP clients connect to the MCP port — clean separation of concerns
- Transport routes are MCP-protocol specific, not web UI
- Management API follows existing `/fe/api/` convention

**Alternatives considered**:
- All routes on MCP port — CORS issues for frontend API calls
- All routes on main port — MCP clients would need to use same port as LLM clients, potential confusion

---

## AD-3: Near-Raw HTTP Proxying with SSE Endpoint Rewriting

**Decision**: Implement proxy at HTTP level — forward raw SSE events and HTTP requests without parsing MCP JSON-RPC messages, **with one critical exception**: SSE `endpoint` events must be parsed and their URLs rewritten from the upstream URL to the proxy URL.

**Rationale**:
- MCP protocol is JSON-RPC 2.0 over SSE/HTTP — proxy doesn't need to understand message semantics for most events
- **Exception**: The SSE transport sends an `endpoint` event containing the upstream URL where clients should POST messages. If forwarded raw, clients would bypass the proxy entirely and connect directly to the upstream.
- The endpoint rewriting is minimal SSE parsing (line-by-line scan for `event: endpoint` + `data:` lines), not full MCP JSON-RPC parsing
- Future MCP protocol changes are automatically supported (only the endpoint event needs special handling)
- No dependency on Go MCP SDK (none exists for network transports as of 2026)

**Implementation**:
1. Read upstream SSE stream line-by-line
2. Detect `event: endpoint` → flag next `data:` line for rewriting
3. Rewrite data URL: `http://upstream:3001/messages?token=abc` → `/mcp/{server_id}/messages?token=abc`
4. Forward rewritten event to client
5. All other events forwarded as-is

**Alternatives considered**:
- Fully raw proxy — **REJECTED**: Leaks upstream URLs to clients (C1)
- Parse and validate all MCP JSON-RPC — adds complexity, requires protocol version tracking, no clear benefit for a proxy
- Use Go MCP SDK — no suitable SDK exists for network transports

---

## AD-4: Encrypted Auth Token Storage (AES-256-GCM)

**Decision**: Encrypt `auth_token` at rest using `crypto.Encrypt()`/`crypto.Decrypt()` (AES-256-GCM, already in codebase at `pkg/crypto/`).

**Rationale**:
- The codebase already has a proven encryption module with `crypto.Encrypt()` and `crypto.Decrypt()`
- Used by existing credential storage — same security posture
- Graceful degradation: if no encryption key is configured, functions pass through plaintext (with warning log)
- No new dependencies or encryption code to write
- Consistent with existing security patterns

**Implementation**:
- `MCPStore.CreateServer()`: `crypto.Encrypt(req.AuthToken)` before INSERT
- `MCPStore.UpdateServer()`: `crypto.Encrypt(*req.AuthToken)` before UPDATE (if token changed)
- `MCPStore.GetServer()`: `crypto.Decrypt(stored.AuthToken)` after SELECT
- `MCPStore.ListServers()`: `crypto.Decrypt()` for each server's auth_token
- API responses: mask with `maskAuthToken()` regardless (first 6 + "***" + last 3)

**Alternatives considered**:
- Plaintext storage — **REJECTED**: Inconsistent with existing credential handling, security risk (C4)
- Separate encryption scheme — reinventing the wheel, inconsistent with codebase patterns

---

## AD-5: No Connection Pooling for v1

**Decision**: Each client SSE connection creates a new upstream SSE connection (1:1 mapping). No connection reuse or pooling.

**Rationale**:
- MCP SSE connections are stateful (session-specific) — sharing connections between clients would break session isolation
- Simpler implementation and reasoning about connection lifecycle
- Acceptable for v1 with moderate concurrent connections (< 100)
- Future improvement: connection pooling for Streamable HTTP (stateless requests)

---

## AD-6: MCP Module as Separate Package

**Decision**: All MCP code lives in `pkg/mcp/` — single Go package.

**Rationale**:
- Matches project convention (e.g., `pkg/proxy/`, `pkg/auth/`, `pkg/usage/`)
- Small enough for single package (est. 1200-1500 lines with tests)
- If it grows, can be split into `pkg/mcp/` sub-packages later
- Clear boundary: `pkg/mcp/` is the MCP proxy, `pkg/proxy/` is the LLM proxy

---

## AD-7: Migration 026 Only

**Decision**: Single migration (026) creates the `mcp_servers` table. No down migration needed.

**Rationale**:
- New feature — no existing data to migrate
- Single table, simple schema
- Following project convention: most migrations are up-only (no down migrations for most)
- Down migration can be added later if needed

---

## AD-8: Mandatory Authentication on MCP Transport Endpoints

**Decision**: All MCP proxy transport routes (`/mcp/{id}/*`) require API key authentication via `Authorization: Bearer <key>` header. Reuse existing `auth.TokenStoreInterface`.

**Rationale**:
- Without auth, anyone reaching port 4322 can use all upstream MCP servers — critical security gap (C2)
- Token system already exists — `auth.TokenStore.ValidateToken()` — no new auth code needed
- Consistent with existing LLM proxy auth model
- MCP clients can be configured with API keys (Claude Desktop, IDE plugins support custom headers)

**Implementation**:
- Auth middleware wraps all transport handlers on the MCP port
- Management API routes (on main port) use existing UI auth patterns (no additional middleware needed)
- Token info injected into request context for logging/auditing

---

## AD-9: Terminal Connection Loss (No Auto-Reconnect)

**Decision**: When upstream MCP connection drops, the proxy closes the client SSE stream. No automatic reconnection.

**Rationale**:
- MCP sessions are stateful — auto-reconnecting without re-initialization creates inconsistent state (tools list, resources, session context would be stale or missing)
- MCP clients already handle reconnection logic (they must re-initialize the session)
- Simpler proxy implementation — no reconnection state machine, exponential backoff, or session recovery
- Event published to `events.Bus` for monitoring (`mcp_connection_lost`)
- Client sees closed SSE stream and reconnects naturally

**Alternatives considered**:
- Transparent auto-reconnect — **REJECTED**: Creates session inconsistency, complex state management (C3)
- Reconnect + re-initialize — Proxy doesn't know the initialization state, can't replay initialization messages

---

## AD-10: URL Validation for SSRF Prevention

**Decision**: Reject `upstream_url` values that point to localhost, private networks, link-local addresses, or non-HTTP(S) schemes.

**Rationale**:
- MCP servers are configured via the management API — an attacker with UI access could probe internal network
- SSRF via upstream_url is a well-known attack vector (W1)
- Validation occurs on create and update in both API handlers and store layer

**Blocked patterns**:
- `localhost`, `127.0.0.1`, `0.0.0.0`, `::1` (loopback)
- `10.x.x.x`, `172.16-31.x.x`, `192.168.x.x` (RFC 1918 private)
- `169.254.x.x`, `fe80::` (link-local)
- `file://`, `ftp://`, etc. (non-HTTP schemes)

---

## AD-11: Header Blocklist for Custom Headers

**Decision**: Custom headers configured for upstream connections are filtered through a blocklist. `Host`, `Content-Length`, `Transfer-Encoding`, and `Connection` are silently stripped.

**Rationale**:
- These headers control HTTP transport behavior — injecting them could break the connection or cause security issues (W2)
- MCP proxy manages the HTTP connection lifecycle — these headers must be set correctly by the HTTP client
- Blocklist is applied in the `ConnectionManager.injectAuth()` method

---

## AD-12: Test Connection Endpoint Logic

**Decision**: Transport-specific test logic with 5s timeout.

| Transport | Test Method | Success Criteria |
|-----------|------------|------------------|
| SSE | `GET {upstream_url}/sse` | Status 200 + `Content-Type: text/event-stream` |
| Streamable HTTP | `POST {upstream_url}` (empty body) | Status 200 or 202 |

**Rationale**:
- Lightweight check — doesn't require full MCP handshake
- Different transports have different expectations (SSE needs correct content type, HTTP just needs reachability)
- 5s timeout balances responsiveness vs. slow network tolerance
- Returns `{ success, latency_ms, transport, error }` for UI display

---

## AD-13: Route Method Partitioning in mcp.go (B1)

**Decision**: Phase 1 creates two empty stub methods in `mcp.go`: `setupRoutes()` and `RegisterAPIHandlers()`. Phase 2 fills `setupRoutes()` with transport routes only. Phase 3 fills `RegisterAPIHandlers()` with management API routes only.

**Rationale**:
- Phase 2 and Phase 3 were originally marked "independent" but both modify `mcp.go`, creating merge conflicts in parallel execution
- Partitioning into separate methods eliminates file-level conflicts — each phase touches a different function
- Phase 1 creates the stubs, so both phases have clear entry points
- Updated coupling to "loose" — Phase 2 must complete before Phase 3 because Phase 3's delete handler needs `ConnectionRegistry`

---

## AD-14: Connection Registry for Server Deletion Cleanup (B2)

**Decision**: `ConnectionRegistry` in `proxy.go` tracks active SSE connections per server ID using `map[string][]context.CancelFunc`. Delete handler calls `CloseConnections(serverID)` before DB deletion.

**Rationale**:
- Without cleanup, deleting an MCP server leaves orphaned SSE streams that continue consuming resources
- SSE connections are long-lived — they don't self-detect that their server config was deleted
- Using `context.CancelFunc` leverages existing Go cancellation patterns — cancelling the context closes the upstream connection, which triggers the SSE handler's error path, which closes the client stream
- `Register` on connection start, `Unregister` on connection end (deferred), `CloseConnections` on delete

---

## AD-15: Multi-Line SSE Data Accumulation for Endpoint Rewriting (B3)

**Decision**: The SSE proxy accumulates all `data:` lines for each event into a buffer, then applies endpoint URL rewriting after the complete event is collected (on empty-line boundary). This handles both single-line and multi-line SSE data fields.

**Rationale**:
- SSE spec allows multi-line data: consecutive `data:` lines are joined with `\n`
- Single-line-only rewriting would miss URLs in multi-line endpoint data, leaking the upstream URL — exactly what C1 prevents
- Accumulating the full event before forwarding is a minor latency increase (one event delay) but is necessary for correctness
- The `sseEvent` struct tracks `eventType` and `dataLines` separately; rewriting only applies when `eventType == "endpoint"`

---

## AD-16: tokenStore in Constructor from Day One (NB1)

**Decision**: `NewServer(port, store, bus, tokenStore)` includes `tokenStore auth.TokenStoreInterface` as a parameter in Phase 1. No retrofit in Phase 4.

**Rationale**:
- Auth middleware was added in review iteration 2 as a Phase 2 requirement
- Phase 1 creates the constructor — `tokenStore` should be there from the start
- Avoids a "update constructor" task in Phase 4 that would conflict with Phase 2's auth middleware work
- Consistent with constructor injection pattern used throughout the codebase

---

## AD-17: MCP Status Endpoint for Frontend Detection (NB2)

**Decision**: Add `GET /fe/api/mcp-servers/status` returning `{ "enabled": bool, "port": int }`. Frontend fetches this on mount to decide whether to show the server list or a "MCP Proxy is not enabled" message.

**Rationale**:
- Frontend has no way to detect if MCP module is enabled (MCP proxy runs on a separate port)
- Without status detection, the MCP tab would show an empty server list even when the module is disabled — confusing UX
- The endpoint reads `GetMCPProxyPort()` (env var), no DB query needed — lightweight
- Registered in `RegisterAPIHandlers()` alongside other management routes
