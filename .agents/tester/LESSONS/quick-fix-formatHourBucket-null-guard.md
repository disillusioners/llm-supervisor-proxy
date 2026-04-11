# Quick Fix: formatHourBucket() Null Guard

**Date**: 2026-04-12
**Branch**: feature/usage-chart-view
**Commit**: 5d00972
**Reviewer Note**: W3

## Issue
`formatHourBucket()` in `pkg/ui/frontend/src/utils/helpers.ts` had no null guard. If the backend returned a null or undefined `bucket` value, the function would crash.

## Root Cause
The function directly processed the bucket string without checking for null/undefined first.

## Fix
Added null guard at the top of the function:
```typescript
if (!bucket) return '';
```

## Impact
- Prevents crash when usage data has null bucket values
- Returns empty string instead of throwing TypeError

## Lesson
Always add null guards for string parameters that come from API responses. Backend data can be null in edge cases (empty result sets, missing fields).
