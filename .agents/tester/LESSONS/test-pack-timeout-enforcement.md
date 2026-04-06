# Test Pack Script Creation - Timeout Enforcement

**Date**: 2026-04-06
**Issue**: PACKS.md listed 8 unit test packs but zero script files existed — no timeout enforcement

## Problem
- PACKS.md was documentation-only — pack names mapped to Go package paths, not executable scripts
- No subprocess-based timeout enforcement on any pack
- Violated rule: "Every pack entry must have a valid, existing script path"

## Solution
Created 8 bash scripts in `test/packs/` with:
- `timeout 120s` wrapping `go test` call (subprocess-based enforcement)
- `go test -timeout=110s` as inner timeout (10s buffer for script overhead)
- Cleanup trap on EXIT (kills child processes, removes temp files)
- Standardized output: `=== Test Pack: <name> ===` → test output → `RESULT: PASS|FAIL|TIMEOUT`
- Exit codes: 0=PASS, 1=FAIL, 124=TIMEOUT
- Portable `SCRIPT_DIR` derivation for project root (not hardcoded paths)

## Script Template
```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
PACK_NAME="<name>"
OUTPUT_FILE="/tmp/${PACK_NAME}_output.txt"

cleanup() {
    pkill -P $$ 2>/dev/null || true
    rm -f "$OUTPUT_FILE"
}
trap cleanup EXIT

echo "=== Test Pack: ${PACK_NAME} ==="

timeout 120s bash -c "cd '$PROJECT_ROOT' && go test -v -count=1 -timeout=110s ./pkg/..." > "$OUTPUT_FILE" 2>&1
EXIT_CODE=$?

cat "$OUTPUT_FILE"

if [ $EXIT_CODE -eq 124 ]; then
    echo "RESULT: TIMEOUT"
    exit 124
elif [ $EXIT_CODE -ne 0 ]; then
    echo "RESULT: FAIL"
    exit 1
else
    echo "RESULT: PASS"
    exit 0
fi
```

## Actual Run Times (for reference)
- proxy: ~14s (heaviest)
- ultimatemodel: ~5.6s
- misc: ~2.6s
- config: ~1.3s
- store: ~0.9s
- auth: ~0.4s
- models, toolrepair, loopdetection: <0.2s each

All well within 120s limit.
