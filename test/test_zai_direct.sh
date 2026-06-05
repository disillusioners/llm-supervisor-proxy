#!/bin/bash
# Test script to send a streaming "hi" request directly to ZAI provider

set -e

# Configuration
ZAI_API_KEY="${ZAI_API_KEY:-(your-api-key-here)}"
ZAI_BASE_URL="https://api.z.ai/api/coding/paas/v4"
MODEL="${MODEL:-glm-5}"

echo "=== ZAI Direct Streaming Test ==="
echo "URL: $ZAI_BASE_URL/chat/completions"
echo "Model: $MODEL"
echo ""

# JSON payload with streaming enabled
PAYLOAD=$(cat <<EOF
{
  "model": "$MODEL",
  "stream": true,
  "messages": [
    {"role": "user", "content": "hi"}
  ]
}
EOF
)

echo "Request Payload:"
echo "$PAYLOAD" | jq .
echo ""
echo "=== Response Stream ==="

# Send streaming request using curl
curl -s -N \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $ZAI_API_KEY" \
  -d "$PAYLOAD" \
  "$ZAI_BASE_URL/chat/completions"

echo ""
echo ""
echo "=== Stream Complete ==="
