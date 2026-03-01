#!/bin/bash
# E2E test for Anthropic endpoint
# Usage: ./test/test_anthropic_e2e.sh [API_KEY] [MODEL] [PROXY_URL]

API_KEY="${1:-sk-a96c6864f2d86ee77ee33c6a2612d934ccb56236e2cc16edbe93bdbf350e3e26}"
MODEL="${2:-glm-5}"
PROXY_URL="${3:-http://localhost:4321}"

echo "============================================"
echo "E2E Test: Anthropic Endpoint"
echo "============================================"
echo "Proxy URL: $PROXY_URL"
echo "Model: $MODEL"
echo "API Key: ${API_KEY:0:20}..."
echo ""

# Test 1: Non-streaming request with simple "hi" message
echo "Test 1: Non-streaming (simple 'hi')"
echo "---"
curl -s -w "\nHTTP Status: %{http_code}\n" \
  -X POST "$PROXY_URL/v1/messages" \
  -H "Content-Type: application/json" \
  -H "x-api-key: $API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "'"$MODEL"'",
    "max_tokens": 100,
    "messages": [{"role": "user", "content": "hi"}],
    "stream": false
  }'
echo ""

# Test 2: Streaming request with simple "hi" message
echo "Test 2: Streaming (simple 'hi')"
echo "---"
curl -N -s -w "\nHTTP Status: %{http_code}\n" \
  -X POST "$PROXY_URL/v1/messages" \
  -H "Content-Type: application/json" \
  -H "x-api-key: $API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "'"$MODEL"'",
    "max_tokens": 100,
    "messages": [{"role": "user", "content": "hi"}],
    "stream": true
  }'
echo ""

# Test 3: With system prompt
echo "Test 3: Non-streaming with system prompt"
echo "---"
curl -s -w "\nHTTP Status: %{http_code}\n" \
  -X POST "$PROXY_URL/v1/messages" \
  -H "Content-Type: application/json" \
  -H "x-api-key: $API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "'"$MODEL"'",
    "max_tokens": 100,
    "system": "You are a helpful assistant.",
    "messages": [{"role": "user", "content": "hi"}],
    "stream": false
  }'
echo ""

# Test 4: No API key (should return 401)
echo "Test 4: No API key (should return 401)"
echo "---"
curl -s -w "\nHTTP Status: %{http_code}\n" \
  -X POST "$PROXY_URL/v1/messages" \
  -H "Content-Type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "'"$MODEL"'",
    "max_tokens": 100,
    "messages": [{"role": "user", "content": "hi"}],
    "stream": false
  }'
echo ""

echo "============================================"
echo "Tests completed"
echo "============================================"
