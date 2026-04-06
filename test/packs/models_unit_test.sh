#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
PACK_NAME="models_unit_test"
OUTPUT_FILE="/tmp/${PACK_NAME}_output.txt"

cleanup() {
    pkill -P $$ 2>/dev/null || true
    rm -f "$OUTPUT_FILE"
}
trap cleanup EXIT

echo "=== Test Pack: ${PACK_NAME} ==="

timeout 120s bash -c "cd '$PROJECT_ROOT' && go test -v -count=1 -timeout=110s ./pkg/models/" > "$OUTPUT_FILE" 2>&1
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
