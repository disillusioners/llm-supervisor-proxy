#!/bin/bash

# Test script for token usage forwarding
# This tests that the proxy correctly forwards token usage from upstream to clients

set -e

# Load test API key
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
if [ -f "$ROOT_DIR/.env-test" ]; then
    source "$ROOT_DIR/.env-test"
fi

PROXY_PORT=${PROXY_PORT:-4123}
PROXY_URL="http://localhost:${PROXY_PORT}/v1/chat/completions"

echo "========================================"
echo "Token Usage Forwarding Test"
echo "========================================"
echo "Testing against: $PROXY_URL"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

pass_count=0
fail_count=0

# Function to test streaming response for usage
test_streaming_usage() {
    local test_name="$1"
    local prompt="$2"
    local model="$3"
    local expected_in_response="$4"
    
    echo -e "\n--- Test: $test_name ---"
    echo "Prompt: $prompt"
    echo "Model: $model"
    
    response=$(curl -s -N -X POST "$PROXY_URL" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $TEST_API_KEY" \
        -d "{\"stream\": true, \"model\": \"$model\", \"messages\": [{\"role\": \"user\", \"content\": \"$prompt\"}]}")
    
    # Check if usage is in the response
    if echo "$response" | grep -q "$expected_in_response"; then
        echo -e "${GREEN}✓ PASS${NC}: Found usage data: $expected_in_response"
        # Show the usage part
        echo "Usage data:"
        echo "$response" | grep -o '"usage":[^}]*}' | head -1
        pass_count=$((pass_count + 1))
    else
        echo -e "${RED}✗ FAIL${NC}: Did not find usage data: $expected_in_response"
        echo "Response (last 5 lines):"
        echo "$response" | tail -5
        fail_count=$((fail_count + 1))
    fi
}

# Function to test non-streaming response for usage
test_non_streaming_usage() {
    local test_name="$1"
    local prompt="$2"
    local model="$3"
    
    echo -e "\n--- Test: $test_name ---"
    echo "Prompt: $prompt"
    echo "Model: $model"
    
    response=$(curl -s -X POST "$PROXY_URL" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $TEST_API_KEY" \
        -d "{\"stream\": false, \"model\": \"$model\", \"messages\": [{\"role\": \"user\", \"content\": \"$prompt\"}]}")
    
    # Check if usage is in the response
    if echo "$response" | grep -q '"usage"'; then
        # Extract and display usage
        echo -e "${GREEN}✓ PASS${NC}: Found usage data"
        echo "Usage:"
        echo "$response" | grep -o '"usage":[^}]*}' 
        pass_count=$((pass_count + 1))
    else
        echo -e "${RED}✗ FAIL${NC}: Did not find usage data"
        echo "Response:"
        echo "$response" | head -10
        fail_count=$((fail_count + 1))
    fi
}

# Check if mock server is running, if not start it
if ! curl -s "http://localhost:4001/v1/chat/completions" -X POST -d '{}' > /dev/null 2>&1; then
    echo -e "${YELLOW}Starting mock LLM server...${NC}"
    cd "$SCRIPT_DIR"
    go run mock_llm.go &
    MOCK_PID=$!
    sleep 2
fi

# Check if proxy is running
if ! curl -s "$PROXY_URL" -X POST -d '{}' > /dev/null 2>&1; then
    echo -e "${RED}Error: Proxy is not running on port $PROXY_PORT${NC}"
    echo "Please ensure the proxy is running with:"
    echo "  go run cmd/main.go"
    exit 1
fi

echo -e "\n${YELLOW}Proxy is running, starting tests...${NC}\n"

# Model to use - we use manual-test which properly routes
MODEL="manual-test"

# Test 1: Non-streaming response - use a model that works
test_non_streaming_usage "Non-Streaming Response" "hello" "$MODEL"

# Test 2: Streaming response with mock server (use just mock-think to trigger mock routing)
test_streaming_usage "Streaming Response with Mock" "mock-think" "mock-internal-model" '"usage"'

# Test 3: Streaming with thinking
test_streaming_usage "Streaming with Thinking" "mock-think" "mock-internal-model" '"usage"'

# Test 4: Streaming with tool call
test_streaming_usage "Streaming with Tool Call" "mock-tool" "mock-internal-model" '"usage"'

# Summary
echo -e "\n========================================"
echo "Test Summary"
echo "========================================"
echo -e "${GREEN}Passed: $pass_count${NC}"
echo -e "${RED}Failed: $fail_count${NC}"

if [ $fail_count -eq 0 ]; then
    echo -e "\n${GREEN}All tests passed!${NC}"
    exit 0
else
    echo -e "\n${RED}Some tests failed!${NC}"
    exit 1
fi
