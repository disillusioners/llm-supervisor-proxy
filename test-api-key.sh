#!/bin/bash
# Test script for internal upstream API key authentication
# Usage: ./test-api-key.sh [API_KEY] [MODEL] [PROXY_URL]

API_KEY="${1:-sk-a96c6864f2d86ee77ee33c6a2612d934ccb56236e2cc16edbe93bdbf350e3e26}"
MODEL="${2:-MiniMax-M2.5}"
PROXY_URL="${3:-http://localhost:4321}"

echo "============================================"
echo "Testing API Key Authentication"
echo "============================================"
echo "Proxy URL: $PROXY_URL"
echo "Model: $MODEL"
echo "API Key: ${API_KEY:0:20}..."
echo ""

# Test 1: Using Authorization Bearer header
echo "Test 1: Authorization Bearer header"
echo "---"
curl -s -w "\nHTTP Status: %{http_code}\n" \
  -X POST "$PROXY_URL/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $API_KEY" \
  -d '{
    "model": "'"$MODEL"'",
    "messages": [{"role": "user", "content": "hi"}],
    "stream": false
  }'
echo ""

# Test 2: Using X-API-Key header
echo "Test 2: X-API-Key header"
echo "---"
curl -s -w "\nHTTP Status: %{http_code}\n" \
  -X POST "$PROXY_URL/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -d '{
    "model": "'"$MODEL"'",
    "messages": [{"role": "user", "content": "hi"}],
    "stream": false
  }'
echo ""

# Test 3: No API key (should fail with 401)
echo "Test 3: No API key (should return 401)"
echo "---"
curl -s -w "\nHTTP Status: %{http_code}\n" \
  -X POST "$PROXY_URL/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "'"$MODEL"'",
    "messages": [{"role": "user", "content": "hi"}],
    "stream": false
  }'
echo ""

# Test 4: Wrong API key (should fail with 401)
echo "Test 4: Wrong API key (should return 401)"
echo "---"
curl -s -w "\nHTTP Status: %{http_code}\n" \
  -X POST "$PROXY_URL/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-wrong-key-1234567890123456789012345678901234567890123456789012" \
  -d '{
    "model": "'"$MODEL"'",
    "messages": [{"role": "user", "content": "hi"}],
    "stream": false
  }'
echo ""

echo "============================================"
echo "Tests completed"
echo "============================================"
