#!/usr/bin/env bash
set -euo pipefail

# Scope: go-only build + vet gate. Excludes frontend (npm/tsc):
#   - 30 standing `tsc --noEmit` errors across 11 files are documented baseline debt
#     (ModelsTab 10, SettingsPage 6, ConfigModal 6, etc. — see Frontend Web UI blueprint)
#   - vite build does NOT run tsc (npm run build = `vite build` ONLY), so type-safety
#     is checked manually. Out of scope for the Go gate.
# Each phase is subprocess-wrapped with its own 90s timeout; the overall script budget
# is 120s.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
PACK_NAME="build_gate_test"
BUILD_OUTPUT="/tmp/${PACK_NAME}_build.txt"
VET_OUTPUT="/tmp/${PACK_NAME}_vet.txt"

cleanup() {
    pkill -P $$ 2>/dev/null || true
    rm -f "$BUILD_OUTPUT" "$VET_OUTPUT"
}
trap cleanup EXIT

echo "=== Test Pack: ${PACK_NAME} (Go build + vet, total <= 120s) ==="

# Phase 1: go build ./...
timeout 90s bash -c "cd '$PROJECT_ROOT' && go build ./..." > "$BUILD_OUTPUT" 2>&1
BUILD_EXIT=$?

# Phase 2: go vet ./...
timeout 90s bash -c "cd '$PROJECT_ROOT' && go vet ./..." > "$VET_OUTPUT" 2>&1
VET_EXIT=$?

# Decide outcome: TIMEOUT > FAIL > PASS
if [ $BUILD_EXIT -eq 124 ] || [ $VET_EXIT -eq 124 ]; then
    echo "--- TIMEOUT detected ---"
    if [ $BUILD_EXIT -eq 124 ]; then
        echo "--- go build TIMEOUT (exit=124) ---"
        cat "$BUILD_OUTPUT"
    fi
    if [ $VET_EXIT -eq 124 ]; then
        echo "--- go vet TIMEOUT (exit=124) ---"
        cat "$VET_OUTPUT"
    fi
    echo "RESULT: TIMEOUT"
    exit 124
fi

if [ $BUILD_EXIT -ne 0 ] || [ $VET_EXIT -ne 0 ]; then
    echo "--- FAIL detected ---"
    if [ $BUILD_EXIT -ne 0 ]; then
        echo "--- go build FAILED (exit=$BUILD_EXIT) ---"
        cat "$BUILD_OUTPUT"
    fi
    if [ $VET_EXIT -ne 0 ]; then
        echo "--- go vet FAILED (exit=$VET_EXIT) ---"
        cat "$VET_OUTPUT"
    fi
    echo "RESULT: FAIL"
    exit 1
fi

echo "go build: PASS"
echo "go vet: PASS"
echo "RESULT: PASS"
exit 0
