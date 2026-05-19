# Approval Tracking: MCP Proxy Server Module

## Iteration 001 — 2026-05-20

**Verdict**: REJECTED

### Blocking Issues

1. **Phase 2 & Phase 3 NOT independent — both modify `pkg/mcp/mcp.go`**
   - Expected: Phase 2+3 are "independent" with "no import relationship" (plan-overview.md line 62-63)
   - Found: Phase 2 Task #5 wires route handlers + auth in `mcp.go`; Phase 3 Task #2 registers API routes via `RegisterAPIHandlers(mux)` in `mcp.go`. Both modify the same file, breaking the claimed parallelism.
   - Fix: Partition `mcp.go` methods across phases. Phase 1 creates stubs for both `setupRoutes()` and `RegisterAPIHandlers()`. Phase 2 fills `setupRoutes()`. Phase 3 fills `RegisterAPIHandlers()`. Update coupling table to "loose" (same file, different methods).

2. **Active SSE connections not cleaned up on server deletion**
   - Expected: Deleting an MCP server should clean up active proxy connections
   - Found: Phase 3 delete handler removes DB record but Phase 2 proxy handlers have no connection registry. Active SSE connections to a deleted upstream continue running orphaned.
   - Fix: Add connection registry in Phase 2 (`ConnectionManager` tracks active connections by server_id). Phase 3 delete handler calls cleanup before DB deletion.

3. **SSE multi-line `data:` field handling incomplete**
   - Expected: Endpoint rewriting handles all SSE data formats
   - Found: MCP SSE spec allows multi-line `data:` fields (data split across multiple lines). Plan's `rewriteEndpointData()` only handles single-line `data: <url>`. Multi-line data would pass through unrewritten, leaking the upstream URL.
   - Fix: Add multi-line data accumulation in SSE forwarding logic — collect all consecutive `data:` lines, rewrite the accumulated value, then emit.

### Non-blocking Notes

- Frontend lacks MCP enabled/disabled detection — consider a `/fe/api/mcp-servers/status` endpoint
- `crypto.Encrypt` pass-through when no key configured — for upstream auth tokens, should this fail-closed?
- Phase 4 Task #2 updates `NewServer` constructor signature, but Phase 1 already creates the constructor — the `tokenStore` parameter should be in Phase 1's constructor, not Phase 4's update

---

## Iteration 002 — 2026-05-19

**Verdict**: APPROVED

### Verification of Previous Blocking Issues

1. ✅ **Phase 2 & Phase 3 file conflict** — FIXED by AD-13: Route method partitioning. Phase 1 creates empty stubs (`setupRoutes()` and `RegisterAPIHandlers()`). Phase 2 fills `setupRoutes()` only. Phase 3 fills `RegisterAPIHandlers()` only. No file-level conflict.

2. ✅ **Active SSE connections cleanup** — FIXED by AD-14: `ConnectionRegistry` in `proxy.go` with `Register/Unregister/CloseConnections`. Phase 2 implements the registry. Phase 3 delete handler calls `CloseConnections(serverID)` before DB deletion.

3. ✅ **SSE multi-line data handling** — FIXED by AD-15: Complete `sseEvent` struct with `dataLines` accumulation, empty-line boundary detection, `rewriteEndpointData()` function handling multi-line data. Detailed implementation code provided (phase2-plan.md lines 177-281).

### Verification of Previous Non-blocking Notes

- ✅ Status endpoint added — AD-17 (NB2): `GET /fe/api/mcp-servers/status` returning `{ enabled, port }`
- ✅ tokenStore in constructor from day one — AD-16 (NB1): `NewServer(port, store, bus, tokenStore)` in Phase 1

### Independent Evaluation Findings

**Council raised 3 "blocking" issues. Independent verification found:**

1. **Encryption passthrough** — NOT a new issue. The plan's AD-4 explicitly states this behavior and mirrors the existing credential handling pattern in `pkg/models/credential.go` and `pkg/store/database/store.go`. Same `crypto.Encrypt()` is used for existing API keys with identical passthrough behavior. Consistency with existing codebase is correct.

2. **ConnectionRegistry thread safety** — Already specified in the plan. `sync.Mutex` with `Lock()/Unlock()` is shown in phase2-plan.md lines 290-332. All three methods (`Register`, `Unregister`, `CloseConnections`) are mutex-protected. Not a valid blocking issue.

3. **SSE rewriting algorithm underspecified** — Already detailed in phase2-plan.md lines 177-281 with `sseEvent` struct, `dataLines` accumulation, empty-line boundary detection, and `rewriteEndpointData()` function. Memory bounds are a valid non-blocking concern but not blocking for a v1 implementation.

**Additional independent findings:**
- Event naming convention (`mcp_connection_lost` etc.) matches existing snake_case convention (`race_secondary_model_used`, `stream_normalize`, `tool_repair`)
- Migration 026 is correct (verified latest is 025)
- All referenced interfaces exist: `auth.TokenStoreInterface`, `crypto.Encrypt/Decrypt`, `events.Bus.Publish`
- Route patterns match existing `/fe/api/` convention
- Rollback strategy is sound: remove MCP wiring from cmd/main.go, table is harmless

### Non-blocking Notes

- DNS rebinding SSRF vector: validation at config save time only, not at connection time. Acceptable for v1.
- No explicit memory bounds on SSE event accumulation buffer. Recommend 1MB cap during implementation.
- Client disconnect handling could be more explicit (context cancellation propagation to upstream)
- Streamable HTTP test connection sends empty body — some MCP servers may reject empty POST; acceptable heuristic for v1
