# Phase 2: MCP Proxy Protocol Engine

## Objective
Implement the bidirectional proxy logic for SSE and Streamable HTTP MCP transports, with SSE `endpoint` URL rewriting, authentication middleware, and upstream connection lifecycle management. This is the core proxy engine.

## Coupling
- **Depends on**: Phase 1 (tightly)
- **Coupling type**: tight — uses `MCPServer` struct, `MCPStore` for config lookups, `mcp.Server` route handlers, `auth.TokenStoreInterface` for auth, `validation.go` for header blocklist
- **Shared files with other phases**: `pkg/mcp/mcp.go` (fills `setupRoutes()` only — B1), `pkg/mcp/proxy.go` (ConnectionRegistry used by Phase 3 delete handler — B2)
- **Shared APIs/interfaces**: `MCPServer.UpstreamURL`, `MCPServer.TransportType`, `MCPServer.AuthType/AuthToken`, `auth.TokenStoreInterface.ValidateToken()`, `ConnectionRegistry.Register(serverID, cancel)` / `ConnectionRegistry.CloseConnections(serverID)`
- **Why this coupling**: Proxy handlers need data model, store, auth, and validation from Phase 1

## Context
- Phase 1 delivered: `mcp.Server` with route stubs, `MCPStore`, `MCPServer` struct, `GetMCPProxyPort()`, validation functions
- MCP Protocol: JSON-RPC 2.0 over SSE or Streamable HTTP
- The proxy is **near-transparent** with one critical exception: SSE `endpoint` event URL rewriting
- Authentication is mandatory — all proxy endpoints require valid API key via `Authorization: Bearer <key>` header

## Protocol Design

### SSE Transport Proxy (with Endpoint Rewriting — C1)

```
Client                          MCP Proxy                        Upstream
  │                                │                                │
  │── GET /mcp/{id}/sse ──────────▶│                                │
  │   Authorization: Bearer <key>  │── GET /upstream/sse ──────────▶│
  │                                │◀── SSE: endpoint: /messages ───│
  │                                │   (rewrite to /mcp/{id}/messages)
  │◀── SSE: endpoint: /mcp/{id}/messages ──────────────────────────│
  │                                │                                │
  │◀── SSE event (forwarded) ─────│◀── SSE event ─────────────────│
  │                                │                                │
  │── POST /mcp/{id}/messages ────▶│── POST /upstream/messages ───▶│
  │   Authorization: Bearer <key>  │                                │
  │                                │                                │
  │◀── SSE event (response) ──────│◀── SSE event (response) ──────│
  │                                │                                │
  │                    [upstream drops]                             │
  │◀── SSE stream closed ─────────│   (terminal event, NO reconnect)
  │                                │   (client must re-initialize)
```

**Endpoint Rewriting Logic (C1 + B3 — multi-line `data:` handling):**

SSE spec allows multi-line data fields (multiple consecutive `data:` lines, joined by `\n`). The endpoint rewriting must handle this:

1. Read upstream SSE stream line-by-line
2. **Accumulate all consecutive `data:` lines** into a buffer (joined with `\n`) for each event
3. Detect events where `event: endpoint` was seen
4. For endpoint events, after all `data:` lines are collected, scan the accumulated data for URL patterns
5. Rewrite any URL matching the upstream host to the proxy path: `/mcp/{server_id}/messages`
6. Forward the rewritten event to client (emit `data:` lines individually)
7. All other events forwarded as-is (no modification)

```
# Single-line endpoint event (common):
event: endpoint
data: http://upstream:3001/messages?token=abc

# Multi-line endpoint event (SSE spec compliant):
event: endpoint
data: {"uri": "http://upstream:3001/messages",
data:  "token": "abc"}

# Both must be rewritten to:
event: endpoint
data: /mcp/{server_id}/messages?token=abc
```

### Streamable HTTP Transport Proxy

```
Client                          MCP Proxy                        Upstream
  │                                │                                │
  │── POST /mcp/{id}/http ────────▶│── POST /upstream ─────────────▶│
  │   Authorization: Bearer <key>  │    (forwarded body+headers)    │
  │   Mcp-Session-Id: abc123       │   Mcp-Session-Id: abc123       │
  │                                │◀── SSE stream response ────────│
  │◀── SSE stream (forwarded) ────│   Mcp-Session-Id: abc123       │
  │   Mcp-Session-Id: abc123       │                                │
  │                                │                                │
```

### Connection Loss Handling (C3)

When the upstream connection drops:
1. **No auto-reconnect** — MCP sessions are stateful, reconnecting without re-initialization creates inconsistent state
2. Close the client SSE stream cleanly (send final error event if possible, then close)
3. Client detects closed stream and must reconnect + re-initialize the MCP session
4. Publish `mcp_connection_lost` event to `events.Bus` for monitoring

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Create `pkg/mcp/auth.go` — Auth middleware | `authMiddleware(next http.HandlerFunc) http.HandlerFunc` — extracts `Authorization: Bearer <key>` header, calls `tokenStore.ValidateToken()`. Returns 401 on missing/invalid token. Applied to ALL proxy transport routes (`/mcp/{id}/*`). | `pkg/mcp/auth.go` |
| 2 | Create `pkg/mcp/proxy.go` — ConnectionManager + ConnectionRegistry | **ConnectionManager**: `ConnectUpstream(ctx, server) (*http.Response, error)`, `ForwardHTTPRequest(ctx, server, r) (*http.Response, error)`. Handles: auth header injection (with blocked header filtering), upstream HTTP client, connection timeouts. **No reconnection logic** (C3). **ConnectionRegistry** (B2): `map[string][]context.CancelFunc` keyed by server ID. `Register(serverID, cancel)` — called when SSE connection starts. `Unregister(serverID, cancel)` — called when SSE connection ends (deferred). `CloseConnections(serverID)` — cancels all contexts for a server ID, effectively closing all active SSE streams. Used by Phase 3 delete handler to clean up before DB deletion. | `pkg/mcp/proxy.go` |
| 3 | Create `pkg/mcp/handlers_sse.go` — SSE proxy | `handleSSEConnection(w, r)` — authenticated, looks up server config, creates upstream SSE connection, **line-by-line event forwarding with multi-line `data:` accumulation and endpoint URL rewriting** (C1, B3). Registers connection in ConnectionRegistry (B2). `handleSSEMessage(w, r)` — POST handler, forwards message to upstream. Both handle: upstream connection loss (close client stream, no reconnect), context cancellation, `Mcp-Session-Id` passthrough. | `pkg/mcp/handlers_sse.go` |
| 4 | Create `pkg/mcp/handlers_streamable.go` — Streamable HTTP proxy | `handleStreamableHTTP(w, r)` — authenticated, looks up server config, forwards POST to upstream with auth + custom headers (filtered through blocklist), receives SSE response, streams back to client. Handles: `Mcp-Session-Id` header passthrough (request and response), chunked transfer encoding, upstream errors. | `pkg/mcp/handlers_streamable.go` |
| 5 | Fill `setupRoutes()` in `pkg/mcp/mcp.go` (B1) | Wire transport route handlers (from tasks 1-4) into the `setupRoutes()` stub created by Phase 1. Each transport route wrapped with auth middleware. Extract server ID from URL path. This method is ONLY for MCP port routes — Phase 3 handles `RegisterAPIHandlers()` independently. | `pkg/mcp/mcp.go` |
| 6 | Add connection metrics/events | Publish events to `events.Bus`: `mcp_connection_established`, `mcp_connection_lost`, `mcp_proxy_error`, `mcp_request_forwarded`. Use existing `events.Event` pattern. | `pkg/mcp/proxy.go`, `pkg/mcp/handlers_*.go` |
| 7 | Write auth middleware tests | Test: valid token passes through, invalid token returns 401, missing header returns 401, expired token returns 401. | `pkg/mcp/auth_test.go` |
| 8 | Write proxy tests | Test SSE forwarding, **multi-line `data:` endpoint URL rewriting** (B3 — single-line and multi-line data), Streamable HTTP forwarding, auth header injection, upstream failure handling (connection loss closes client stream), header blocklist enforcement, **ConnectionRegistry register/unregister/closeConnections** (B2). Use `httptest.Server` as mock upstream. | `pkg/mcp/proxy_test.go`, `pkg/mcp/handlers_sse_test.go`, `pkg/mcp/handlers_streamable_test.go` |

## Key Files
- `pkg/mcp/auth.go` — Authentication middleware (NEW)
- `pkg/mcp/proxy.go` — Upstream connection manager (NEW)
- `pkg/mcp/handlers_sse.go` — SSE transport proxy with endpoint rewriting (NEW)
- `pkg/mcp/handlers_streamable.go` — Streamable HTTP transport proxy (NEW)
- `pkg/mcp/mcp.go` — Wire handlers + auth to routes (MODIFY from Phase 1)
- `pkg/mcp/auth_test.go` — Auth middleware tests (NEW)
- `pkg/mcp/proxy_test.go` — Connection manager tests (NEW)
- `pkg/mcp/handlers_sse_test.go` — SSE proxy tests including endpoint rewriting (NEW)
- `pkg/mcp/handlers_streamable_test.go` — Streamable HTTP tests (NEW)

## Constraints
- **SSE endpoint rewriting is mandatory** — raw forwarding would leak upstream URLs (C1)
- Auth middleware on ALL proxy transport routes — no unauthenticated access (C2)
- No auto-reconnect on upstream connection loss — close client stream (C3)
- Proxy must not modify MCP JSON-RPC message bodies (only SSE `endpoint` event data is rewritten)
- Must handle both `Content-Type: text/event-stream` and `Content-Type: application/json` responses
- `Mcp-Session-Id` header must be passed through for session state
- Auth headers for upstream injected based on server config (filtered through header blocklist from Phase 1)
- Connection timeouts: upstream connect timeout (30s), idle timeout (5min), no read timeout for SSE streams
- All events published to `events.Bus` use `mcp_` prefix for type names

## Detailed Implementation Notes

### Auth Middleware (C2)

```go
// auth.go
package mcp

import (
    "context"
    "log"
    "net/http"
    "strings"
    
    "github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
)

type contextKey string
const tokenContextKey contextKey = "mcp_token"

// authMiddleware validates API key and injects token info into request context
func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        authHeader := r.Header.Get("Authorization")
        if authHeader == "" {
            http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
            return
        }
        
        parts := strings.SplitN(authHeader, " ", 2)
        if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
            http.Error(w, `{"error":"invalid authorization format, expected Bearer <key>"}`, http.StatusUnauthorized)
            return
        }
        
        token, err := s.tokenStore.ValidateToken(r.Context(), parts[1])
        if err != nil {
            log.Printf("[MCP] Auth failed: %v", err)
            http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
            return
        }
        
        // Inject token into context for downstream handlers
        ctx := context.WithValue(r.Context(), tokenContextKey, token)
        next.ServeHTTP(w, r.WithContext(ctx))
    }
}
```

### SSE Endpoint Rewriting (C1 + B3 — multi-line data accumulation)

```go
// handlers_sse.go — SSE forwarding with multi-line data accumulation and endpoint rewriting

// sseEvent accumulates all lines for a single SSE event before forwarding
type sseEvent struct {
    eventType string   // value from "event: <type>" line
    dataLines []string // all consecutive "data: <value>" lines
    rawLines  []string // all other lines (event:, id:, retry:, comments)
}

// rewriteEndpointData rewrites URLs in the endpoint event data to proxy paths.
// Handles both single-line and multi-line data fields (B3).
func rewriteEndpointData(dataLines []string, serverID string) []string {
    proxyPath := fmt.Sprintf("/mcp/%s/messages", serverID)
    rewritten := make([]string, len(dataLines))
    for i, line := range dataLines {
        value := strings.TrimPrefix(line, "data: ")
        // Check if this line contains the upstream URL (http:// or https://)
        if strings.Contains(value, "http://") || strings.Contains(value, "https://") {
            if idx := strings.Index(value, "?"); idx >= 0 {
                value = proxyPath + value[idx:]
            } else {
                value = proxyPath
            }
        }
        rewritten[i] = "data: " + value
    }
    return rewritten
}

func (s *Server) handleSSEConnection(w http.ResponseWriter, r *http.Request) {
    serverID := extractServerID(r.URL.Path, "/sse")
    server, err := s.store.GetServer(r.Context(), serverID)
    // ... error handling ...
    
    flusher, ok := w.(http.Flusher)
    if !ok { http.Error(w, "Streaming not supported", 500); return }
    
    w.Header().Set("Content-Type", "text/event-stream")
    w.Header().Set("Cache-Control", "no-cache")
    w.Header().Set("Connection", "keep-alive")
    w.WriteHeader(200)
    flusher.Flush()
    
    // Register connection in registry for cleanup on deletion (B2)
    ctx, cancel := context.WithCancel(r.Context())
    defer cancel()
    s.connMgr.registry.Register(serverID, cancel)
    defer s.connMgr.registry.Unregister(serverID, cancel)
    
    // Connect to upstream
    upstreamResp, err := s.connMgr.ConnectUpstream(ctx, server)
    // ... error handling ...
    defer upstreamResp.Body.Close()
    
    // Line-by-line SSE forwarding with event accumulation (B3)
    scanner := bufio.NewScanner(upstreamResp.Body)
    var currentEvent *sseEvent
    
    for scanner.Scan() {
        line := scanner.Text()
        
        if line == "" {
            // End of event — flush accumulated event to client
            if currentEvent != nil {
                if currentEvent.eventType == "endpoint" {
                    currentEvent.dataLines = rewriteEndpointData(currentEvent.dataLines, serverID)
                }
                for _, l := range currentEvent.rawLines {
                    fmt.Fprintf(w, "%s\n", l)
                }
                for _, l := range currentEvent.dataLines {
                    fmt.Fprintf(w, "%s\n", l)
                }
                fmt.Fprintf(w, "\n") // empty line terminates event
                flusher.Flush()
            }
            currentEvent = nil
            continue
        }
        
        if currentEvent == nil {
            currentEvent = &sseEvent{}
        }
        
        if strings.HasPrefix(line, "event: ") {
            currentEvent.eventType = strings.TrimPrefix(line, "event: ")
            currentEvent.rawLines = append(currentEvent.rawLines, line)
        } else if strings.HasPrefix(line, "data: ") || line == "data:" {
            currentEvent.dataLines = append(currentEvent.dataLines, line)
        } else {
            currentEvent.rawLines = append(currentEvent.rawLines, line)
        }
    }
    
    // Upstream disconnected — terminal event (C3), no reconnect
    // Client must reconnect and re-initialize
    s.bus.Publish(events.Event{
        Type:      "mcp_connection_lost",
        Timestamp: time.Now().Unix(),
        Data:      map[string]string{"server_id": serverID, "reason": "upstream_disconnected"},
    })
}
```

### Connection Registry (B2)

```go
// proxy.go — ConnectionRegistry tracks active connections per server for cleanup

type ConnectionRegistry struct {
    mu       sync.Mutex
    conns    map[string][]context.CancelFunc // serverID → list of cancel functions
}

func NewConnectionRegistry() *ConnectionRegistry {
    return &ConnectionRegistry{conns: make(map[string][]context.CancelFunc)}
}

func (r *ConnectionRegistry) Register(serverID string, cancel context.CancelFunc) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.conns[serverID] = append(r.conns[serverID], cancel)
}

func (r *ConnectionRegistry) Unregister(serverID string, cancel context.CancelFunc) {
    r.mu.Lock()
    defer r.mu.Unlock()
    cancels := r.conns[serverID]
    for i, c := range cancels {
        // Compare by pointer — cancel funcs from same context.WithCancel are comparable
        if fmt.Sprintf("%p", c) == fmt.Sprintf("%p", cancel) {
            r.conns[serverID] = append(cancels[:i], cancels[i+1:]...)
            break
        }
    }
    if len(r.conns[serverID]) == 0 {
        delete(r.conns, serverID)
    }
}

// CloseConnections cancels all active connections for a server.
// Called by Phase 3 delete handler before DB deletion.
func (r *ConnectionRegistry) CloseConnections(serverID string) int {
    r.mu.Lock()
    cancels := r.conns[serverID]
    delete(r.conns, serverID)
    r.mu.Unlock()
    
    for _, cancel := range cancels {
        cancel()
    }
    return len(cancels)
}
```

### Connection Manager with Header Filtering

```go
// proxy.go
func (cm *ConnectionManager) injectAuth(req *http.Request, server *MCPServer) {
    switch server.AuthType {
    case AuthBearer:
        req.Header.Set("Authorization", "Bearer "+server.AuthToken)
    case AuthBasic:
        encoded := base64.StdEncoding.EncodeToString([]byte(server.AuthToken))
        req.Header.Set("Authorization", "Basic "+encoded)
    case AuthAPIKey:
        req.Header.Set("X-API-Key", server.AuthToken)
    }
    
    // Custom headers (filtered through blocklist from validation.go)
    if server.Headers != "" && server.Headers != "{}" {
        var headers map[string]string
        json.Unmarshal([]byte(server.Headers), &headers)
        for k, v := range headers {
            if !blockedHeaders[strings.ToLower(k)] {
                req.Header.Set(k, v)
            }
        }
    }
}
```

## Deliverables
- [ ] Auth middleware validates API key on all proxy transport routes
- [ ] `ConnectionManager` connects to upstream SSE and Streamable HTTP endpoints
- [ ] `ConnectionRegistry` tracks active connections per server, supports `CloseConnections(serverID)` for cleanup (B2)
- [ ] SSE proxy with **multi-line data accumulation and endpoint URL rewriting** — clients never see upstream URLs (C1, B3)
- [ ] Streamable HTTP proxy — POST forwarded, SSE response streamed back
- [ ] Auth header injection for all auth types (bearer, basic, api_key, custom)
- [ ] Custom headers filtered through blocklist (Host, Content-Length, etc.)
- [ ] `Mcp-Session-Id` header passthrough
- [ ] Upstream connection loss = terminal event (close client stream, no reconnect)
- [ ] Events published to `events.Bus` for connection lifecycle
- [ ] `setupRoutes()` filled with transport routes wrapped in auth middleware (B1)
- [ ] Test coverage: auth middleware, SSE forwarding + multi-line endpoint rewriting, HTTP forwarding, auth injection, failure handling, header blocklist, connection registry
