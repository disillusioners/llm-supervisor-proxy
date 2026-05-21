# MCP Test Connection Quick Fix

**Date**: 2026-05-21
**Branch**: `fix/mcp-test-connection`
**Commit**: `ab85359`

## Issue
During browser automation testing of the MCP "Test Connection" button fix, discovered that error messages from the backend were not being displayed in the UI.

## Root Cause
In `MCPServerForm.tsx`, the test result state was only copying `success` and `latency_ms` from the API response, but not the `error` field. When the API returned `success: false` with an error message (e.g., SSRF blocked, connection refused), the UI showed no explanation.

## Fix
1-line change in `MCPServerForm.tsx`:
```typescript
// Before:
setTestResult({ success: result.success, latency: result.latency_ms });

// After:
setTestResult({ success: result.success, latency: result.latency_ms, error: result.error });
```

## Lesson
When consuming API responses in the frontend, always map ALL relevant fields from the response to the local state. Missing the `error` field meant users saw "Connection failed" with no explanation of WHY it failed.
