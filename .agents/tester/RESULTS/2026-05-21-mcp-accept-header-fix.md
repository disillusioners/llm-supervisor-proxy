# MCP Test Connection Fix Verification (Accept Header)
Date: 2026-05-21
Commit: 3729f5d

## Summary
Verified the fix for MCP streamable_http test connection missing Accept header.

## What Was Fixed
The test connection for streamable_http transport was missing the required `Accept: application/json, text/event-stream` header per MCP spec. Upstream servers like z.ai return 400 when this header is absent.

**File changed**: `pkg/mcp/handlers_api.go` line 417
```go
req.Header.Set("Accept", "application/json, text/event-stream")
```

## Verification Results

### Browser Automation (Playwright)
| Test | Result | Details |
|------|--------|---------|
| zai-web-read Test Connection | ✅ PASS | Shows SUCCESS, NOT "Unexpected status: 400" |
| Add Server mode | ✅ PASS | Button calls API, shows result |
| Screenshot | /tmp/mcp-final-result.png | Green SUCCESS indicator |

### API Verification
```bash
$ curl -s -X POST http://localhost:4123/fe/api/mcp-servers/zai-web-read/test | jq .
{
  "success": true,
  "latency_ms": 492,
  "transport": "streamable_http"
}
```

### Go Tests
| Category | Result | Details |
|----------|--------|---------|
| Go Test | ✅ PASS | 27/27 packages |
| Go Vet | ✅ PASS | No issues |
| Frontend Dev | ✅ Running | Port 5172 |
| Backend | ✅ Running | Port 4123 |

## Verdict
**Fix verified: YES** ✅

The zai-web-read server now successfully connects with streamable_http transport, returning success instead of "Unexpected status: 400". The Accept header is correctly sent in the test connection request.
