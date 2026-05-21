# MCP Test Connection Button Fix

## Bug
The "Test Connection" button in the MCP Servers tab didn't call any API in "Add Server" mode. The `handleTest` function in `MCPServerForm.tsx` returned early when `!server` (no saved server ID), showing only a local error message.

## Root Cause
The backend only had `POST /fe/api/mcp-servers/{id}/test` which required a saved server ID. In Add mode, no server exists yet, so no API call could be made.

## Fix (2 commits)

### Commit 1: `725b115` — Core fix
1. **Backend**: Added `POST /fe/api/mcp-servers/test-connection` endpoint that accepts `{ upstream_url, transport_type }` in the body (no server ID needed)
2. **Backend**: Extracted shared `testServerConnection` helper from existing handler for code reuse
3. **Backend**: Added SSRF protection via `ValidateUpstreamURL()` — review caught this as a critical security issue
4. **Frontend**: Added `testMCPServerDirect()` function in useApi.ts
5. **Frontend**: Updated `handleTest` to branch: saved servers use existing endpoint, unsaved servers use new direct endpoint

### Commit 2: `d5fe2d7` — Reviewer fixes (W1 + W2)
**W1: HTTP Redirect SSRF Bypass**
- `http.Client` in `testServerConnection()` followed up to 10 redirects without validating redirect targets
- Fix: Added `CheckRedirect` that calls `ValidateUpstreamURL()` on every redirect target, limits to 3 redirects max
- Tests: redirect to localhost blocked, redirect to private IP blocked, too many redirects blocked, valid redirects allowed

**W2: Direct Test Always Uses AuthNone**
- Direct test endpoint always set `AuthType: AuthNone`, so users couldn't test authenticated servers before saving
- Fix: Added `auth_type` and `auth_token` optional fields to `TestConnectionDirectRequest`, frontend passes form auth values
- Supports Bearer, Basic, API Key auth

## Key Files
- `pkg/mcp/handlers_api.go` — new handler + shared helper + SSRF validation + redirect validation + auth fields
- `pkg/mcp/mcp.go` — route registration (must be before wildcard routes)
- `pkg/ui/frontend/src/hooks/useApi.ts` — new API function with auth params
- `pkg/ui/frontend/src/components/mcp/MCPServerForm.tsx` — updated handleTest with auth passthrough

## Lessons
1. **SSRF on redirects**: Validating only the initial URL is insufficient — `http.Client` follows redirects by default. Must validate every redirect target.
2. **Auth passthrough**: When creating a "test before save" endpoint, include all relevant configuration fields (auth, headers, etc.) so the test is representative.
3. Route ordering matters: specific routes must be registered before wildcard/{id} routes
4. Security review is critical for URL-accepting endpoints — multiple layers of SSRF protection needed
