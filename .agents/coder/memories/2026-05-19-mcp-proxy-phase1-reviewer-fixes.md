# Phase 1 MCP Proxy Server — Reviewer Fixes

## Date: 2026-05-19
## Branch: feature/mcp-proxy-server
## Commits: 71b8204 (initial) → 47f8086 (reviewer fixes)

## Reviewer Issues Fixed:

### Critical:
- **C1+C2**: SSRF bypass via hex/decimal/octal IPs — added `net.LookupIP()` DNS resolution after `net.ParseIP()` to catch alternative IP formats
- **C3**: Cross-request token clearing — empty AuthToken in update now treated as nil (skip update)
- **C4**: Empty string token overwrite — store skips encryption when AuthToken is empty string

### Warnings:
- **W1**: Added `authorization` and `proxy-authorization` to header blocklist
- **W2**: Added CRLF injection check in header values
- **W3**: Added empty bearer token guard in auth middleware
- **W4**: Made DeleteServer return nil for not-found (consistent with GetServer)

### Suggestions:
- **S1**: Replaced custom `joinStrings` with `strings.Join`
- **S2**: Removed unused `ctx` parameter from `Start()`

### New Test Files:
- `pkg/mcp/auth_test.go` — 6 middleware test cases
- `pkg/mcp/mcp_test.go` — 9 bootstrap test cases
- Added SSRF, header injection test cases to validation_test.go

## Stats (reviewer fixes): 9 files changed, 390 insertions, 42 deletions
## All tests pass (with 3 SSRF skips due to DNS env)
