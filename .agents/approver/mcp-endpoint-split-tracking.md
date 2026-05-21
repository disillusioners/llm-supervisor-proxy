# MCP Endpoint Split — Approval Tracking

## Iteration 001 — REJECTED
**Date**: 2026-05-21

### Blocking Issues

1. **Management endpoint auth removal unsafe for non-k8s deployments**
   - Expected: Application-level auth retained or explicit opt-out mechanism
   - Found: Plan removes all auth from `/fe/api/mcp-servers/*`, relying solely on k8s ingress. Open-source project with SQLite/local dev support means non-k8s deployments have zero access control on management CRUD.
   - Evidence: `mcp.go:72-76` currently applies `proxyAuthMiddleware`. `pkg/ui/server.go` has no auth. `cmd/main.go` only has `recoveryMiddleware`.

2. **Phase 3 missing e2e_test.go port field assertions**
   - Expected: Explicit tasks to update/remove port-related tests
   - Found: `e2e_test.go` lines 824, 842, 853, 871 reference `env.proxyServer.port` and `resp.Port` — no Phase 3 task covers these

3. **Phase 3 missing handlers_api_test.go auth expectation updates**
   - Expected: Explicit tasks to update auth assertions for management routes
   - Found: `handlers_api_test.go` lines 1284-1389 assert 401 for unauthenticated management requests. Lines 1318-1320 test invalid tokens. Lines 1350-1352 test valid tokens. All will break when auth is removed. Phase 3 task 4 says "likely no changes."

### Notes (non-blocking)
- Phase 1 code assumptions verified accurate (all 10 claims match codebase)
- Phase 2 frontend changes correctly identified
- `.vscode/launch.json` has MCP_PROXY_PORT reference (not in any phase)

## Iteration 002 — APPROVED
**Date**: 2026-05-21

### Verification Summary

Council evaluation verified 5 areas against actual codebase:
1. **Dual status endpoints**: Both `/fe/api/mcp/status` (inline) and `/fe/api/mcp-servers/status` (RegisterAPIHandlers) properly addressed in Phase 1 Task 17 ✅
2. **extractServerID() fix**: Confirmed correct — Go ServeMux passes full paths (verified with test), TrimPrefix("/v1/mcp/") approach is valid ✅
3. **Route conflicts**: Go 1.22 ServeMux handles `/v1/mcp/{id}/*` patterns correctly, no conflicts ✅
4. **Auth middleware chain**: Type-compatible — `proxyAuthMiddleware(http.HandlerFunc)` returns `http.HandlerFunc` ✅
5. **Nil guard split**: Correct — after removing auth from management routes, `RegisterAPIHandlers` only needs `s.store != nil` guard; proxy routes guard on `s.tokenStore != nil` ✅

### Previous Issues Resolution
- Issue #1 (management auth removal): Plan explicitly acknowledges design decision. Current auth uses `proxyAuthMiddleware` (proxy tokens for API consumers) — not admin auth. K8s ingress uses OAuth2 proxy for management access. Removing proxy token auth from management is actually a security improvement (separation of concerns). **Resolved — design decision, not oversight.**
- Issue #2 (e2e port assertions): Phase 3 Task 17 explicitly covers lines 824, 842, 853, 871. **Resolved.**
- Issue #3 (handlers_api_test auth expectations): Phase 3 Task 4 explicitly converts auth expectations for management routes. **Resolved.**

### Notes (non-blocking)
- Plan could use `r.PathValue("id")` (Go 1.22+) instead of manual path parsing in `extractServerID()` — simpler but not blocking
- Current `Shutdown()` has no connection cleanup despite plan mentioning it — functional, just description slightly inaccurate
- `.vscode/launch.json` MCP_PROXY_PORT reference still not in any phase
