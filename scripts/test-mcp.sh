#!/bin/bash
# Test script for MCP streamable-http endpoint

SERVER_ID="zai-web-read"
BASE_URL="http://localhost:4123/v1/mcp/${SERVER_ID}"
AUTH_TOKEN="sk-4de49a4237e09e98c5aa6ffae5f2cb299835b8c4670119f641888c70c63f21b4"

echo "=== MCP Streamable HTTP Test ==="
echo "URL: ${BASE_URL}"
echo ""

# Initialize request
echo "1. Sending initialize request..."
RESPONSE=$(curl -s -X POST "${BASE_URL}" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "initialize",
    "params": {
      "protocolVersion": "2024-11-05",
      "capabilities": {
        "roots": {
          "listChanged": true
        },
        "sampling": {}
      },
      "clientInfo": {
        "name": "test-client",
        "version": "1.0.0"
      }
    }
  }')

echo "Response:"
echo "${RESPONSE}" | jq . 2>/dev/null || echo "${RESPONSE}"
echo ""

# Send initialize completion (required by MCP spec)
echo "2. Sending initialized notification..."
curl -s -X POST "${BASE_URL}" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  -d '{
    "jsonrpc": "2.0",
    "method": "notifications/initialized",
    "params": {}
  }' > /dev/null
echo "OK"
echo ""

# List tools
echo "3. Listing tools..."
TOOLS_RESPONSE=$(curl -s -X POST "${BASE_URL}" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  -d '{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "tools/list",
    "params": {}
  }')

echo "Response:"
echo "${TOOLS_RESPONSE}" | jq . 2>/dev/null || echo "${TOOLS_RESPONSE}"
echo ""

echo "=== Test Complete ==="