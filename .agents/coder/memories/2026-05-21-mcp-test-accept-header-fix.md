# MCP Test Connection Accept Header Fix

**Date:** 2026-05-21
**Commit:** 3729f5d
**Bug:** MCP Test Connection returns "Unexpected status: 400" for streamable_http servers

## Root Cause
The `testServerConnection` function in `pkg/mcp/handlers_api.go` was missing the `Accept` header for streamable_http transport test requests. The MCP Streamable HTTP spec requires `Accept: application/json, text/event-stream` for POST requests. Without it, spec-compliant servers (like z.ai) return 400 with message "Accept header must include both application/json and text/event-stream".

## Fix
Added one line in the `TransportStreamableHTTP` case:
```go
req.Header.Set("Accept", "application/json, text/event-stream")
```

## Key Insight
- MCP Streamable HTTP spec requires the Accept header for content negotiation
- The SSE test path already had `Accept: text/event-stream` (correct for SSE)
- The proxy code passes through client headers via `ForwardHTTPRequest`, so it didn't have this issue
- The test connection creates its own request from scratch, so it needs explicit headers
