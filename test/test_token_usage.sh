#!/bin/bash

# Test script for token usage forwarding
# Validates that token usage from upstream is correctly forwarded to clients

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Test configuration
MOCK_PORT=4001
PROXY_PORT=4322
MOCK_PID=""
PROXY_PID=""
TIMER_PID=""

# Test results
TESTS_PASSED=0
TESTS_FAILED=0

# Hard timeout: kill everything after 60 seconds
HARD_TIMEOUT=60

cleanup_all() {
    echo -e "\n${YELLOW}Cleaning up all processes...${NC}"
    if [ ! -z "$TIMER_PID" ]; then
        kill $TIMER_PID 2>/dev/null || true
    fi
    if [ ! -z "$MOCK_PID" ]; then
        kill $MOCK_PID 2>/dev/null || true
    fi
    if [ ! -z "$PROXY_PID" ]; then
        kill $PROXY_PID 2>/dev/null || true
    fi
    pkill -f "mock_llm.go" 2>/dev/null || true
    pkill -f "cmd/main.go" 2>/dev/null || true
    lsof -ti :$MOCK_PORT | xargs kill -9 2>/dev/null || true
    lsof -ti :$PROXY_PORT | xargs kill -9 2>/dev/null || true
    wait 2>/dev/null || true
}
trap cleanup_all EXIT

# Start background timer for hard timeout
hard_timeout_handler() {
    echo -e "\n${RED}Hard timeout ($HARD_TIMEOUT s) reached! Terminating...${NC}"
    exit 1
}
trap hard_timeout_handler SIGALRM
( sleep $HARD_TIMEOUT && kill -ALRM $$ 2>/dev/null ) &
TIMER_PID=$!

# Load test API key from .env-test
if [ -f "$ROOT_DIR/.env-test" ]; then
    export $(grep -v '^#' "$ROOT_DIR/.env-test" | xargs)
fi

API_KEY="${TEST_API_KEY:-test-key}"

echo -e "${BLUE}======================================${NC}"
echo -e "${BLUE}   Token Usage Forwarding Tests    ${NC}"
echo -e "${BLUE}   (Max runtime: ${HARD_TIMEOUT}s)         ${NC}"
echo -e "${BLUE}======================================${NC}"

# Clean ports first
source "$SCRIPT_DIR/test_mock_clean_ports.sh" "$PROXY_PORT" "$MOCK_PORT"
clean_ports "$PROXY_PORT" "$MOCK_PORT"

# Start mock server
echo -e "\n${YELLOW}[1/6] Starting Mock LLM Server (port $MOCK_PORT)...${NC}"
cd "$SCRIPT_DIR"
go run mock_llm.go &
MOCK_PID=$!
cd "$ROOT_DIR"

sleep 2
if ! kill -0 $MOCK_PID 2>/dev/null; then
    echo -e "${RED}ERROR: Mock server failed to start${NC}"
    exit 1
fi
echo -e "${GREEN}Mock server started (PID: $MOCK_PID)${NC}"

# Start proxy
echo -e "\n${YELLOW}[2/6] Starting Proxy (port $PROXY_PORT)...${NC}"
export APPLY_ENV_OVERRIDES="true"
export UPSTREAM_URL="http://localhost:$MOCK_PORT"
export PORT="$PROXY_PORT"
export IDLE_TIMEOUT="5s"
export MAX_GENERATION_TIME="20s"
export RACE_RETRY_ENABLED="false"
export LOOP_DETECTION_ENABLED="false"
export TOOL_CALL_BUFFER_DISABLED="true"

go run cmd/main.go &
PROXY_PID=$!

sleep 2
if ! kill -0 $PROXY_PID 2>/dev/null; then
    echo -e "${RED}ERROR: Proxy failed to start${NC}"
    exit 1
fi
echo -e "${GREEN}Proxy started (PID: $PROXY_PID)${NC}"

# Configure internal mock model via API
echo -e "\n${YELLOW}[3/6] Configuring internal mock model...${NC}"

# Delete model first if exists
for i in 1 2 3; do
    curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/models/mock-usage-model" 2>/dev/null || true
    sleep 0.3
done
sleep 0.5
for i in 1 2 3; do
    curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/credentials/mock-usage-cred" 2>/dev/null || true
    sleep 0.3
done
sleep 1

# Create credential
CREDENTIAL_RESPONSE=$(curl -s -X POST "http://localhost:$PROXY_PORT/fe/api/credentials" \
    -H "Content-Type: application/json" \
    -d "{
        \"id\": \"mock-usage-cred\",
        \"provider\": \"openai\",
        \"api_key\": \"mock-api-key\",
        \"base_url\": \"http://localhost:$MOCK_PORT/v1\"
    }")

if echo "$CREDENTIAL_RESPONSE" | grep -q '"id"'; then
    echo -e "${GREEN}Credential created successfully${NC}"
else
    echo -e "${RED}Failed to create credential: $CREDENTIAL_RESPONSE${NC}"
    exit 1
fi

# Create model
MODEL_RESPONSE=$(curl -s -X POST "http://localhost:$PROXY_PORT/fe/api/models" \
    -H "Content-Type: application/json" \
    -d "{
        \"id\": \"mock-usage-model\",
        \"name\": \"Mock Usage Model\",
        \"enabled\": true,
        \"internal\": true,
        \"credential_id\": \"mock-usage-cred\",
        \"internal_model\": \"mock-model\",
        \"internal_base_url\": \"http://localhost:$MOCK_PORT/v1\"
    }")

if echo "$MODEL_RESPONSE" | grep -q '"id"'; then
    echo -e "${GREEN}Internal model created successfully${NC}"
else
    echo -e "${RED}Failed to create internal model: $MODEL_RESPONSE${NC}"
    exit 1
fi

# Test functions
assert_contains() {
    local output="$1"
    local expected="$2"
    local test_name="$3"
    
    if echo "$output" | grep -q "$expected"; then
        echo -e "  ${GREEN}✓${NC} $test_name: found '$expected'"
        ((TESTS_PASSED++))
        return 0
    else
        echo -e "  ${RED}✗${NC} $test_name: NOT found '$expected'"
        ((TESTS_FAILED++))
        return 1
    fi
}

assert_not_contains() {
    local output="$1"
    local unexpected="$2"
    local test_name="$3"
    
    if ! echo "$output" | grep -q "$unexpected"; then
        echo -e "  ${GREEN}✓${NC} $test_name: correctly absent '$unexpected'"
        ((TESTS_PASSED++))
        return 0
    else
        echo -e "  ${RED}✗${NC} $test_name: unexpectedly found '$unexpected'"
        ((TESTS_FAILED++))
        return 1
    fi
}

# Test 1: Non-streaming response should have usage
echo -e "\n${YELLOW}[4/6] Test 1: Non-Streaming Response with Usage${NC}"
OUTPUT1=$(curl -s --max-time 10 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-usage-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"hello\"}],
        \"stream\": false
    }" 2>&1)

echo "Response:"
echo "$OUTPUT1" | head -3 | sed 's/^/  /'

assert_contains "$OUTPUT1" '"usage"' "Usage field present"
assert_contains "$OUTPUT1" '"prompt_tokens"' "Prompt tokens present"
assert_contains "$OUTPUT1" '"completion_tokens"' "Completion tokens present"
assert_contains "$OUTPUT1" '"total_tokens"' "Total tokens present"

# Test 2: Streaming response should have usage in final chunk
echo -e "\n${YELLOW}[5/6] Test 2: Streaming Response with Usage in Final Chunk${NC}"
OUTPUT2=$(curl -N -s --max-time 10 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-usage-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"hello\"}],
        \"stream\": true
    }" 2>&1)

echo "Last 3 chunks:"
echo "$OUTPUT2" | tail -3 | sed 's/^/  /'

assert_contains "$OUTPUT2" '"usage"' "Usage field present in stream"
assert_contains "$OUTPUT2" '"prompt_tokens"' "Prompt tokens present"
assert_contains "$OUTPUT2" '"completion_tokens"' "Completion tokens present"
assert_contains "$OUTPUT2" '"total_tokens"' "Total tokens present"
assert_contains "$OUTPUT2" "data: \[DONE\]" "DONE marker present"

# Test 3: Streaming with thinking should have usage
echo -e "\n${YELLOW}[6/6] Test 3: Streaming with Thinking and Usage${NC}"
OUTPUT3=$(curl -N -s --max-time 10 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-usage-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-think\"}],
        \"stream\": true
    }" 2>&1)

echo "Last 3 chunks:"
echo "$OUTPUT3" | tail -3 | sed 's/^/  /'

assert_contains "$OUTPUT3" '"reasoning_content"' "Reasoning content present"
assert_contains "$OUTPUT3" '"usage"' "Usage field present"
assert_contains "$OUTPUT3" '"prompt_tokens"' "Prompt tokens present"
assert_contains "$OUTPUT3" '"completion_tokens"' "Completion tokens present"
assert_contains "$OUTPUT3" '"total_tokens"' "Total tokens present"
assert_contains "$OUTPUT3" "data: \[DONE\]" "DONE marker present"

# Summary
echo -e "\n${BLUE}======================================${NC}"
echo -e "${BLUE}           Test Summary              ${NC}"
echo -e "${BLUE}======================================${NC}"
echo -e ""
echo -e "${GREEN}Passed: $TESTS_PASSED${NC}"
echo -e "${RED}Failed: $TESTS_FAILED${NC}"
echo -e ""

if [ $TESTS_FAILED -eq 0 ]; then
    echo -e "${GREEN}All tests passed!${NC}"
    echo -e ""
    echo -e "${GREEN}Token Usage Forwarding Verified:${NC}"
    echo -e "  ${YELLOW}✓${NC} Non-streaming responses include usage"
    echo -e "  ${YELLOW}✓${NC} Streaming responses include usage in final chunk"
    echo -e "  ${YELLOW}✓${NC} Usage includes prompt_tokens, completion_tokens, total_tokens"
    echo -e "  ${YELLOW}✓${NC} Streaming with thinking includes usage"
    exit 0
else
    echo -e "${RED}Some tests failed. Check the output above for details.${NC}"
    exit 1
fi
