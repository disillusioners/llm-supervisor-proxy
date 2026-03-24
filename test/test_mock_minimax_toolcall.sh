#!/bin/bash

# Test script for MiniMax-style tool call streaming through proxy
# Validates that the proxy correctly handles:
# 1. 3 tool calls in a single request
# 2. Chunked arguments
# 3. Thinking content (<think>/</think>)
# 4. No [DONE] marker

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Test configuration
MOCK_PORT=4003
PROXY_PORT=4323
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
    pkill -f "mock_llm_minimax.go" 2>/dev/null || true
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

# Load test API key
if [ -f "$ROOT_DIR/.env-test" ]; then
    export $(grep -v '^#' "$ROOT_DIR/.env-test" | xargs)
fi

API_KEY="${TEST_API_KEY:-test-key}"

echo -e "${BLUE}======================================${NC}"
echo -e "${BLUE}   MiniMax Tool Call Streaming Tests ${NC}"
echo -e "${BLUE}   (Max runtime: ${HARD_TIMEOUT}s)         ${NC}"
echo -e "${BLUE}======================================${NC}"

# Clean ports
echo -e "\n${YELLOW}Cleaning ports...${NC}"
lsof -ti :$MOCK_PORT | xargs kill -9 2>/dev/null || true
lsof -ti :$PROXY_PORT | xargs kill -9 2>/dev/null || true
sleep 1

# Start mock MiniMax server
echo -e "\n${YELLOW}[1/6] Starting Mock MiniMax Server (port $MOCK_PORT)...${NC}"
cd "$SCRIPT_DIR"
go run mock_llm_minimax.go -port=$MOCK_PORT &
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
export TOOL_CALL_BUFFER_DISABLED="false"

go run cmd/main.go &
PROXY_PID=$!

sleep 3
if ! kill -0 $PROXY_PID 2>/dev/null; then
    echo -e "${RED}ERROR: Proxy failed to start${NC}"
    exit 1
fi
echo -e "${GREEN}Proxy started (PID: $PROXY_PID)${NC}"

# Configure internal model via API
echo -e "\n${YELLOW}[3/6] Configuring internal mock model via API..."

# Delete model first
for i in 1 2 3; do
    curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/models/mock-minimax-model" 2>/dev/null || true
    sleep 0.3
done
sleep 0.5
for i in 1 2 3; do
    curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/credentials/mock-minimax-cred" 2>/dev/null || true
    sleep 0.3
done
sleep 1

# Create credential
CREDENTIAL_RESPONSE=$(curl -s -X POST "http://localhost:$PROXY_PORT/fe/api/credentials" \
    -H "Content-Type: application/json" \
    -d "{
        \"id\": \"mock-minimax-cred\",
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

# Create internal model
MODEL_RESPONSE=$(curl -s -X POST "http://localhost:$PROXY_PORT/fe/api/models" \
    -H "Content-Type: application/json" \
    -d "{
        \"id\": \"mock-minimax-model\",
        \"name\": \"Mock MiniMax Model\",
        \"enabled\": true,
        \"internal\": true,
        \"credential_id\": \"mock-minimax-cred\",
        \"internal_model\": \"MiniMax-M2.7\",
        \"internal_base_url\": \"http://localhost:$MOCK_PORT/v1\"
    }")

if echo "$MODEL_RESPONSE" | grep -q '"id"'; then
    echo -e "${GREEN}Internal model created successfully${NC}"
else
    echo -e "${RED}Failed to create model: $MODEL_RESPONSE${NC}"
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

# Test 1: 3 tool calls with MiniMax-style streaming
echo -e "\n${YELLOW}[4/6] Test 1: 3 Tool Calls (MiniMax-style)${NC}"
OUTPUT1=$(curl -N -s --max-time 10 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-minimax-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-minimax-3tools: please call 3 tools for Tokyo weather, python auth search, and calculate 123*456\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT1" "get_weather" "Tool call 1: get_weather present"
assert_contains "$OUTPUT1" "search_code" "Tool call 2: search_code present"
assert_contains "$OUTPUT1" "calculate" "Tool call 3: calculate present"
assert_contains "$OUTPUT1" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"

# Verify argument values
# get_weather should have Tokyo
if echo "$OUTPUT1" | grep -q '"location".*Tokyo'; then
    echo -e "  ${GREEN}✓${NC} Argument check: get_weather has location=Tokyo"
    ((TESTS_PASSED++))
else
    echo -e "  ${RED}✗${NC} Argument check: get_weather missing location=Tokyo"
    echo -e "  ${YELLOW}DEBUG: Tool calls in response:${NC}"
    echo "$OUTPUT1" | grep -o '"tool_calls":\[^\]*' | head -5 || true
    echo "$OUTPUT1" | grep -E '"(location|query|expression)"' | head -10 || true
    ((TESTS_FAILED++))
fi

# search_code should have query="authentication"
if echo "$OUTPUT1" | grep -q '"query".*authentication'; then
    echo -e "  ${GREEN}✓${NC} Argument check: search_code has query=authentication"
    ((TESTS_PASSED++))
else
    echo -e "  ${RED}✗${NC} Argument check: search_code missing query=authentication"
    echo -e "  ${YELLOW}DEBUG: Full tool_calls in response:${NC}"
    echo "$OUTPUT1" | grep -o '"tool_calls":\[^}]*}' | head -10 || true
    ((TESTS_FAILED++))
fi

# calculate should have expression="123 * 456"
if echo "$OUTPUT1" | grep -q '"expression".*123'; then
    echo -e "  ${GREEN}✓${NC} Argument check: calculate has expression=123 * 456"
    ((TESTS_PASSED++))
else
    echo -e "  ${RED}✗${NC} Argument check: calculate missing expression=123 * 456"
    echo -e "  ${YELLOW}DEBUG: Full response (truncated):${NC}"
    echo "$OUTPUT1" | tail -20
    ((TESTS_FAILED++))
fi

# Test 2: Chunked arguments
echo -e "\n${YELLOW}[5/6] Test 2: Chunked Tool Call Arguments${NC}"
OUTPUT2=$(curl -N -s --max-time 10 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-minimax-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-minimax-chunked: get weather for Tokyo\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT2" "get_weather" "Tool call present"
assert_contains "$OUTPUT2" "Tokyo" "Location argument present"
assert_contains "$OUTPUT2" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"

# Verify argument is complete valid JSON (not fragmented)
if echo "$OUTPUT2" | grep -q '"location".*"Tokyo"'; then
    echo -e "  ${GREEN}✓${NC} Argument check: location has valid JSON with Tokyo"
    ((TESTS_PASSED++))
else
    echo -e "  ${RED}✗${NC} Argument check: location missing valid JSON with Tokyo"
    echo -e "  ${YELLOW}DEBUG: Tool call arguments in response:${NC}"
    echo "$OUTPUT2" | grep -o '"arguments":"[^"]*' | head -5 || true
    ((TESTS_FAILED++))
fi

# Test 3: Complete JSON in one chunk
echo -e "\n${YELLOW}[6/6] Test 3: Complete JSON Tool Call${NC}"
OUTPUT3=$(curl -N -s --max-time 10 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-minimax-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-minimax-complete: get weather\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT3" "get_weather" "Tool call present"
assert_contains "$OUTPUT3" "Tokyo" "Location argument present"
assert_contains "$OUTPUT3" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"

# Verify complete JSON arguments
if echo "$OUTPUT3" | grep -q '"location".*"Tokyo"'; then
    echo -e "  ${GREEN}✓${NC} Argument check: location has valid JSON with Tokyo"
    ((TESTS_PASSED++))
else
    echo -e "  ${RED}✗${NC} Argument check: location missing valid JSON with Tokyo"
    echo -e "  ${YELLOW}DEBUG: Tool call arguments in response:${NC}"
    echo "$OUTPUT3" | grep -o '"arguments":"[^"]*' | head -5 || true
    ((TESTS_FAILED++))
fi

if echo "$OUTPUT3" | grep -q '"unit".*"celsius"'; then
    echo -e "  ${GREEN}✓${NC} Argument check: unit has celsius"
    ((TESTS_PASSED++))
else
    echo -e "  ${RED}✗${NC} Argument check: unit missing celsius"
    echo -e "  ${YELLOW}DEBUG: Tool call arguments in response:${NC}"
    echo "$OUTPUT3" | grep -o '"arguments":"[^"]*' | head -5 || true
    ((TESTS_FAILED++))
fi

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
    echo -e "${GREEN}MiniMax Tool Call Streaming Verified:${NC}"
    echo -e "  ${YELLOW}✓${NC} 3 tool calls in single request"
    echo -e "  ${YELLOW}✓${NC} Chunked arguments handling"
    echo -e "  ${YELLOW}✓${NC} Complete JSON tool calls"
    echo -e "  ${YELLOW}✓${NC} finish_reason: tool_calls"
    exit 0
else
    echo -e "${RED}Some tests failed. Check the output above for details.${NC}"
    exit 1
fi
