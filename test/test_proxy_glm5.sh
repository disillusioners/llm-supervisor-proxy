#!/bin/bash
# Test script to send a streaming request through the proxy to ZAI
#
# Usage:
#   ./test_proxy_glm5.sh                    # Uses default glm-5 model
#   MODEL=glm-4 ./test_proxy_glm5.sh       # Use glm-4-flash
#   MODEL=glm-5 PROMPT="hi" ./test_proxy_glm5.sh  # Custom model and prompt
#
# Available models: glm-5, glm-5.0, glm-4-plus, glm-4-flash

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

# Load .env-test (filter out comments and empty lines)
if [ -f "$ROOT_DIR/.env-test" ]; then
    while IFS='=' read -r key value; do
        [[ -z "$key" || "$key" == \#* ]] && continue
        export "$key=$value"
    done < <(grep -v '^#' "$ROOT_DIR/.env-test" | grep -v '^$')
fi

# Configuration
PROXY_URL="${PROXY_URL:-http://localhost:4321}"
MODEL="${MODEL:-glm-5}"
PROMPT="${PROMPT:-say hello}"
API_KEY="${TEST_API_KEY:-}"

if [ -z "$API_KEY" ]; then
    echo "Error: TEST_API_KEY not found in .env-test"
    exit 1
fi

echo "=== Proxy Streaming Test ==="
echo "Proxy URL: $PROXY_URL"
echo "Model: $MODEL"
echo "Prompt: $PROMPT"
echo ""

# JSON payload with streaming enabled
PAYLOAD=$(cat <<EOF
{
  "model": "$MODEL",
  "stream": true,
  "messages": [
    {"role": "user", "content": "$PROMPT"}
  ]
}
EOF
)

echo "Request Payload:"
echo "$PAYLOAD" | jq .
echo ""
echo "=== Response Stream ==="

# Send streaming request through proxy
curl -s -N \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $API_KEY" \
  -d "$PAYLOAD" \
  "$PROXY_URL/v1/chat/completions"

echo ""
echo ""
echo "=== Stream Complete ==="
