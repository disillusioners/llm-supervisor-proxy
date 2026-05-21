# Review Report: MCP Endpoint Split Plan

## Review Summary
**🟡 Needs Work** — 8 Critical, 9 Warnings, 9 Suggestions

The plan is well-structured and the phased approach is sound, but there are significant gaps in handler path updates, a contradiction in the "no handler changes" constraint, frontend API path architecture, and serious security concerns about removing auth from management endpoints.

## Scope
- 4 plan files: plan-overview.md, phase1-plan.md, phase2-plan.md, phase3-plan.md
- Cross-referenced against: pkg/mcp/*.go, cmd/main.go, pkg/ui/frontend/src/**/*

## Sessions Used
- review-backend (ses_1b730686dffe6pN5YMjomezbVn)
- review-frontend (ses_1b7306896ffeVMh0fcBY1cEw96)
- review-security (ses_1b7306877ffe9878IlfJc3x09D)

---

## Findings

### 🔴 Critical

#### C1: Handler Path Extraction Will Break — handlers_api.go
- **Area:** Backend handlers
- **File:** `pkg/mcp/handlers_api.go:101, 170, 226, 263`
- **Issue:** All 4 handler functions extract server IDs using `strings.TrimPrefix(r.URL.Path, "/fe/api/mcp-servers/")`. When routes change to `/mcp/servers/`, every TrimPrefix will produce incorrect IDs. The plan incorrectly states "handlers_api.go — Minor: no functional changes."
- **Fix:** Add explicit task to Phase 1 to update all 4 TrimPrefix calls from `"/fe/api/mcp-servers/"` to `"/mcp/servers/"`.

#### C2: extractServerID() Will Fail — handlers_sse.go
- **Area:** Backend handlers
- **File:** `pkg/mcp/handlers_sse.go:396-422`
- **Issue:** `extractServerID()` checks `parts[0] != "mcp"` but new routes are `/v1/mcp/{id}/sse` — so `parts[0]` will be `"v1"`, not `"mcp"`. Every SSE and streamable HTTP request will return empty server ID → 400 errors.
- **Fix:** Rewrite `extractServerID()` to handle `[\"v1\", \"mcp\", \"{serverID}\", ...]` pattern. Add this as an explicit task in Phase 1.

#### C3: SSE Endpoint Rewriting Hardcoded — handlers_sse.go
- **Area:** Backend handlers
- **File:** `pkg/mcp/handlers_sse.go:25`
- **Issue:** `rewriteEndpointData()` hardcodes `/mcp/%s/messages` for URL rewriting. Under the new architecture, this must be `/v1/mcp/%s/messages`. If not updated, SSE clients will receive stale endpoint URLs pointing to non-existent paths.
- **Fix:** Update to `fmt.Sprintf("/v1/mcp/%s/messages", serverID)`. Add as explicit task in Phase 1.

#### C4: Frontend apiFetch() Incompatibility
- **Area:** Frontend API path architecture
- **File:** `pkg/ui/frontend/src/hooks/useApi.ts:5-8`
- **Issue:** `apiFetch()` prepends `API_BASE` (`/fe/api`) to all paths. Currently `apiFetch('/mcp-servers')` → `/fe/api/mcp-servers`. If the backend moves to `/mcp/servers/*`, calling `apiFetch('/mcp/servers')` would produce `/fe/api/mcp/servers` — WRONG. The plan doesn't specify how to handle this architectural mismatch.
- **Fix:** Choose one approach: (1) Create `mcpApiFetch()` without `/fe/api` prefix, (2) Add `skipBase` option to `apiFetch()`, or (3) Keep management routes under `/fe/api/mcp-servers/*` (simplest, least risk). Document the decision.

#### C5: MCPServerStatus Type Missing Port Field
- **Area:** Frontend types
- **File:** `pkg/ui/frontend/src/types.ts:429-432`
- **Issue:** `MCPServerStatus` has `port: number`. Phase 1 removes port from backend response. Will cause TypeScript errors or undefined display at `MCPServersTab.tsx:111` which renders `Port {status?.port}`.
- **Fix:** Remove `port` field from `MCPServerStatus` type. Update MCPServersTab.tsx to remove port badge display.

#### C6: MCP Module Optional Without Trigger
- **Area:** Backend initialization
- **File:** `cmd/main.go:123-143`
- **Issue:** Current MCP init is gated behind `MCP_PROXY_PORT > 0`. If this env var is removed/ignored, there's no clear mechanism for when MCP initializes. The plan doesn't specify the replacement trigger.
- **Fix:** Define new gating: always init MCP store and register routes when database has MCP tables, OR use a simple boolean config, OR check `mcpStore.HasServers()`. Document in Phase 1 Task 9.

#### C7: Unauthenticated CRUD on Credential-Storing Resources
- **Area:** Security — Management endpoint auth
- **File:** `pkg/mcp/mcp.go:64-112`
- **Issue:** Plan removes auth from `/mcp/servers/*` CRUD endpoints. These manage records with `auth_token` fields (encrypted upstream credentials). For an open-source project with SQLite defaults (no k8s), anyone with network access can create/modify/delete MCP servers, redirecting proxy traffic to attacker-controlled servers.
- **Fix:** Keep auth on management endpoints. Use the handler-integrated `authenticate()` pattern from `pkg/proxy/handler.go:253` which gracefully handles `tokenStore == nil`. Auth should be a code-level guarantee, not an ops-level assumption.

#### C8: SSRF-to-Proxy Escalation Path
- **Area:** Security — SSRF protection
- **File:** `pkg/mcp/validation.go:14-47`, `pkg/mcp/proxy.go`
- **Issue:** Combined with C7 (no auth on management), an attacker can: (1) Create MCP server pointing to internal service, (2) Use proxy endpoints to reach those services. SSRF validation happens only at creation time — DNS rebinding allows bypassing this check.
- **Fix:** (1) Keep auth on management (fixes C7, primary defense). (2) Add SSRF validation at proxy-time (dial-time IP check) for defense-in-depth.

---

### 🟡 Warnings

#### W1: Phase 1 "No Handler Changes" Constraint Is Incorrect
- **Area:** Plan accuracy
- **File:** `phase1-plan.md:71-72`
- **Issue:** Constraint states "Existing handler logic (SSE, Streamable HTTP, CRUD) should not change — only routing and auth wiring." This is contradicted by C1, C2, C3 — handlers MUST change.
- **Fix:** Revise constraint to: "Handler logic should remain functionally equivalent but path extraction and URL rewriting must be updated."

#### W2: Wrong File Paths in Plan
- **Area:** Plan accuracy
- **File:** `phase2-plan.md:11-12`
- **Issue:** Plan references `components/config/MCPServersTab.tsx` and `components/config/MCPServerForm.tsx` but actual files are in `components/mcp/`.
- **Fix:** Correct paths to `components/mcp/MCPServersTab.tsx` and `components/mcp/MCPServerForm.tsx`.

#### W3: MCP Not-Enabled Message Will Be Wrong
- **Area:** Frontend UI
- **File:** `pkg/ui/frontend/src/components/mcp/MCPServersTab.tsx:94`
- **Issue:** Message says "Set the MCP_PROXY_PORT environment variable to enable MCP server proxying." — outdated after MCP_PROXY_PORT removal.
- **Fix:** Update to reflect new enablement mechanism (whatever replaces MCP_PROXY_PORT gating).

#### W4: Auth Pattern Ambiguity
- **Area:** Security — Auth implementation
- **File:** Plan vs `pkg/mcp/auth.go` vs `pkg/proxy/handler.go`
- **Issue:** Plan says "reuse existing authenticate() pattern" but doesn't specify middleware vs handler-integrated approach. The two patterns behave differently with nil tokenStore (middleware panics, handler-integrated gracefully disables).
- **Fix:** Standardize on handler-integrated pattern with nil guards. Document choice in Phase 1.

#### W5: No Migration Path for Existing MCP Clients
- **Area:** Backward compatibility
- **File:** `pkg/mcp/mcp.go:49-61`
- **Issue:** Existing clients configured for separate port + `/mcp/{id}/sse` paths will break. Plan mentions deprecation warning but no dual-path support period.
- **Fix:** Consider supporting both old and new paths for one major version. When `MCP_PROXY_PORT` is set, register routes on both old port AND main server. Add `Deprecation` header on old-port responses.

#### W6: RegisterProxyHandlers Route Details Missing
- **Area:** Plan completeness
- **File:** `phase1-plan.md` Task 2
- **Issue:** New `RegisterProxyHandlers(mux)` method is mentioned but exact route patterns aren't specified.
- **Fix:** Add explicit route table:
  ```
  /v1/mcp/{id}/sse       GET  → handleSSEConnection (auth)
  /v1/mcp/{id}/messages  POST → handleSSEMessage (auth)
  /v1/mcp/{id}/          POST → handleStreamableHTTP (auth)
  /v1/mcp/{id}           POST → handleStreamableHTTP (auth)
  ```

#### W7: GetMCPProxyPort() Return Value Unclear
- **Area:** Backend cleanup
- **File:** `phase1-plan.md` Task 15
- **Issue:** Plan says "Either remove entirely or return 0 always." No decision made.
- **Fix:** Decide: Remove entirely (clean break) or return 0 (backward compat). Document choice.

#### W8: k8s-Only Auth Is Unsafe Default
- **Area:** Security — Deployment assumptions
- **File:** Plan architectural decision
- **Issue:** "k8s handles access" is an unsafe assumption for an open-source project. Default deployment is single-binary SQLite — no k8s.
- **Fix:** Auth should be default-on with explicit opt-out env var if needed. Never rely solely on infrastructure for security.

#### W9: Connection URL Display Task May Be N/A
- **Area:** Plan accuracy
- **File:** `phase2-plan.md` Task 8
- **Issue:** Plan mentions updating connection URL display, but the frontend doesn't currently display connection URLs. The task may be unnecessary.
- **Fix:** Verify if connection URLs are displayed anywhere. If not, mark task as N/A or remove from deliverables.

---

### 🟢 Suggestions

#### S1: Update SSE Handler Comment
- **File:** `pkg/mcp/handlers_sse.go:99`
- **Issue:** Comment says "The proxy endpoint is always /mcp/{id}/messages" — outdated.
- **Fix:** Update to `/v1/mcp/{id}/messages`.

#### S2: Trailing Slash Route Pattern Verification
- **File:** `pkg/mcp/mcp.go:57-58`
- **Issue:** Both `/mcp/{id}/` and `/mcp/{id}` are registered. Verify Go 1.22+ mux handles `/v1/mcp/{id}` correctly with and without trailing slash.
- **Fix:** Test or keep both patterns for safety.

#### S3: Frontend/Backend Changes Should Be Atomic
- **Issue:** Management path changes affect both backend routes and frontend API calls. If deployed independently, frontend hits 404s while new unauthenticated endpoints are exposed.
- **Fix:** Ensure frontend and backend path changes land in the same commit/PR.

#### S4: Auth Token Context Unused By Downstream Handlers
- **File:** `pkg/mcp/auth.go:56` → `pkg/mcp/handlers_sse.go`
- **Issue:** `proxyAuthMiddleware` injects `*auth.AuthToken` into context but downstream handlers never consume it.
- **Fix:** Document as intentional (auth is "can you use the proxy?" not "can you access this server?").

#### S5: SSE Token Revocation Gap (Not A Regression)
- **File:** `pkg/mcp/handlers_sse.go:153-246`
- **Issue:** SSE connections authenticate at establishment. Revoked tokens continue receiving events.
- **Fix:** Document as known behavior. Same limitation exists for chat proxy. Low priority.

#### S6: CORS Is Same-Origin — No Action Needed
- **Issue:** UI at `/ui/*` calls endpoints on same origin. MCP clients use direct HTTP. No CORS issue.
- **Fix:** No action needed.

#### S7: TokenStore Nil Guard Needed for Proxy Routes
- **File:** `pkg/mcp/auth.go:48`
- **Issue:** If tokenStore is nil, `s.tokenStore.ValidateToken()` panics.
- **Fix:** Add nil guard pattern from `handler.go:254-256`.

#### S8: Frontend Cache Keys Unaffected
- **File:** `useApi.ts:653,671`
- **Issue:** Cache key `'mcp-servers'` is internal, not URL-based.
- **Fix:** No action needed.

#### S9: Phase 2 Task 9 (pkg/ui/server.go) Is N/A
- **Issue:** Plan says "check for frontend handler routes that proxy to MCP" — no such routes exist.
- **Fix:** Mark as N/A.

---

## Recommendations

### Must Fix Before Implementation
1. **🔴 Add missing handler change tasks to Phase 1** — C1 (TrimPrefix), C2 (extractServerID), C3 (rewriteEndpointData). These are showstoppers.
2. **🔴 Resolve apiFetch architecture** — C4. The frontend cannot call `/mcp/servers/*` through the current `apiFetch()` without code changes not specified in the plan.
3. **🔴 Reconsider removing auth from management** — C7 + C8. Keep auth, use handler-integrated pattern. k8s network policy as additive layer, not replacement.
4. **🔴 Define MCP initialization trigger** — C6. What replaces `MCP_PROXY_PORT` gating?

### Should Fix Before Implementation
5. **🟡 Correct the "no handler changes" constraint** — W1 is misleading.
6. **🟡 Fix file paths in plan** — W2.
7. **🟡 Add explicit route table** — W6 for `RegisterProxyHandlers`.
8. **🟡 Standardize auth pattern** — W4, with nil guards (S7).

### Consider for Future Iterations
9. Dual-path backward compatibility (W5).
10. Dial-time SSRF validation (C8 defense-in-depth).
11. SSE token revocation (S5).
