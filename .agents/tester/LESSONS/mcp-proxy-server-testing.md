# MCP Proxy Server Testing Lessons

## Date: 2026-05-19

## MCP Module Quick Fix — Always-Available Status Endpoint

### Issue
When `MCP_PROXY_PORT` env var was unset, the `/fe/api/mcp/status` endpoint returned 404. The frontend MCP tab had no way to know if MCP was disabled vs. a routing issue.

### Fix (commit: 32cb5cf)
Added an always-available `/fe/api/mcp/status` endpoint in `cmd/main.go` that returns `{"enabled":false,"port":0}` when MCP is disabled, and `{"enabled":true,"port:N}` when enabled.

### Lesson
When modules can be conditionally enabled/disabled via env vars, always register a status endpoint regardless of whether the module is active. The frontend should always be able to query the status of optional features.

## MCP Module Test Quality
- 7 test files, ~7000+ lines of tests
- All critical edge cases covered (SSRF, encryption, endpoint rewriting, connection cleanup)
- Minor gap: No graceful shutdown test with active connections
- The MCP routes use `/fe/api/mcp-servers/` (plural) for CRUD, `/fe/api/mcp/status` for status

## Port Note
- MCP proxy runs on configurable port via `MCP_PROXY_PORT` env var
- Functional tests used port 14322 for MCP proxy, 18080 for main server
- Always verify cleanup after functional tests
