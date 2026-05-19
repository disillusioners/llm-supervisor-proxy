# MCP Proxy Phase 2 — Proxy Protocol Engine

## Date: 2026-05-20
## Branch: feature/mcp-proxy-server
## Commit: 24b81f9

## What Was Built
Phase 2 implemented the core MCP proxy protocol engine with SSE and Streamable HTTP transport handling.

## Files Created
- `pkg/mcp/proxy.go` — ConnectionManager (connectionManagerImpl), ConnectionRegistry
- `pkg/mcp/handlers_sse.go` — SSE proxy with multi-line data accumulation + endpoint URL rewriting
- `pkg/mcp/handlers_streamable.go` — Streamable HTTP proxy with Mcp-Session-Id passthrough
- `pkg/mcp/proxy_test.go` — ConnectionRegistry and injectAuth tests
- `pkg/mcp/handlers_sse_test.go` — SSE proxy tests including endpoint rewriting
- `pkg/mcp/handlers_streamable_test.go` — Streamable HTTP tests

## Files Modified
- `pkg/mcp/auth.go` — Full auth middleware implementation (was stub from Phase 1)
- `pkg/mcp/auth_test.go` — Fixed to work with method-based proxyAuthMiddleware
- `pkg/mcp/mcp.go` — Filled setupRoutes(), updated connMgr type, initialized in NewServer()

## Key Learnings
1. **SSE endpoint rewriting is tricky**: The rewriteFullURL function had a bug where original path was appended after query string. Fix: only use proxyPath + queryString, ignore original path entirely.
2. **ConnectionManager naming conflict**: Phase 1 created a placeholder `ConnectionManager` struct. Phase 2 implementation used `connectionManagerImpl` to avoid redeclaration. The mcp.go Server struct uses `*connectionManagerImpl`.
3. **Function pointer comparison in Unregister**: Uses `fmt.Sprintf("%p", fn)` for pointer comparison. Unconventional but works in Go.
4. **Per-request ConnectionManager trap**: Streamable HTTP handler initially created NewConnectionManager() per request — wastes resources, defeats connection pooling. Must use server's shared connMgr.
5. **Timestamp consistency**: SSE and streamable handlers must use same timestamp function (Unix() vs UnixMilli()).

## Architecture
- SSE transport: Client → GET /mcp/{id}/sse → upstream SSE → line-by-line event forwarding with endpoint rewriting
- Streamable HTTP: Client → POST /mcp/{id} → forward to upstream → stream response back
- ConnectionRegistry: mutex-protected map[serverID][]context.CancelFunc for cleanup on delete
- Auth middleware wraps all transport routes
- Events: mcp_connection_lost, mcp_request_forwarded published to events.Bus

## Commit History
- `24b81f9` — Initial Phase 2 implementation
- `6869a01` — Reviewer fixes (C1-C3 critical + W1-W3 warnings)

## Reviewer Fix Learnings
1. **Path stripping in proxy**: ForwardHTTPRequest must strip `/mcp/{id}` prefix — client paths include proxy routing prefix, upstream doesn't know about it.
2. **http.Client.Do contract**: Can return both non-nil resp AND non-nil err. Always check and close resp.Body on error paths.
3. **Blocked headers apply to client requests too**: Client sends Authorization header for proxy auth — must be stripped before forwarding to upstream, then server-configured auth injected via injectAuth().

## Stats
- 9 files changed, 3,890 insertions, 90 deletions (initial)
- +6 files changed, 217 insertions, 23 deletions (reviewer fixes)
- 145 tests passing
- Full project build: PASS
