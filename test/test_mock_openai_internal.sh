#!/bin/bash

# Test script for OpenAI Format Internal Path
# Validates full OpenAI format support: tool calls, reasoning, DONE marker, finish_reason
# Maximum runtime: 30 seconds

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Test configuration
MOCK_PORT=4001
PROXY_PORT=4322
MOCK_PID=""
PROXY_PID=""
TIMER_PID=""

# Test results
TESTS_PASSED=0
TESTS_FAILED=0

# Hard timeout: kill everything after 30 seconds
HARD_TIMEOUT=30

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
    pkill -f "mock_llm_openai.go" 2>/dev/null || true
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
echo -e "${BLUE}   OpenAI Format Internal Path Tests ${NC}"
echo -e "${BLUE}   (Max runtime: ${HARD_TIMEOUT}s)         ${NC}"
echo -e "${BLUE}======================================${NC}"

# Clean ports first (source the clean_ports script)
source "$SCRIPT_DIR/test_mock_clean_ports.sh" "$PROXY_PORT" "$MOCK_PORT"
clean_ports "$PROXY_PORT" "$MOCK_PORT"

# Start mock OpenAI server
echo -e "\n${YELLOW}[1/8] Starting Mock OpenAI Server (port $MOCK_PORT)...${NC}"
cd "$SCRIPT_DIR"
go run mock_llm_openai.go -port=$MOCK_PORT &
MOCK_PID=$!
cd "$ROOT_DIR"

sleep 2
if ! kill -0 $MOCK_PID 2>/dev/null; then
    echo -e "${RED}ERROR: Mock server failed to start${NC}"
    exit 1
fi
echo -e "${GREEN}Mock server started (PID: $MOCK_PID)${NC}"

# Start proxy
echo -e "\n${YELLOW}[2/8] Starting Proxy (port $PROXY_PORT)...${NC}"
export APPLY_ENV_OVERRIDES="true"
export UPSTREAM_URL="http://localhost:4001"
export PORT="$PROXY_PORT"
export IDLE_TIMEOUT="5s"
export MAX_GENERATION_TIME="20s"
export RACE_RETRY_ENABLED="false"
export LOOP_DETECTION_ENABLED="false"

go run cmd/main.go &
PROXY_PID=$!

sleep 2
if ! kill -0 $PROXY_PID 2>/dev/null; then
    echo -e "${RED}ERROR: Proxy failed to start${NC}"
    exit 1
fi
echo -e "${GREEN}Proxy started (PID: $PROXY_PID)${NC}"

# Configure internal model via API
echo -e "\n${YELLOW}[3/8] Configuring internal mock model via API...${NC}"

# Delete model first (must be done before credential), then credential
# Retry deletion in case of race conditions from previous test runs
for i in 1 2 3; do
    curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/models/mock-openai-model" 2>/dev/null || true
    sleep 0.3
done
sleep 0.5
for i in 1 2 3; do
    curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/credentials/mock-openai-cred" 2>/dev/null || true
    sleep 0.3
done
sleep 1

CREDENTIAL_RESPONSE=$(curl -s -X POST "http://localhost:$PROXY_PORT/fe/api/credentials" \
    -H "Content-Type: application/json" \
    -d "{
        \"id\": \"mock-openai-cred\",
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
        \"id\": \"mock-openai-model\",
        \"name\": \"Mock OpenAI Model\",
        \"enabled\": true,
        \"internal\": true,
        \"credential_id\": \"mock-openai-cred\",
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

# Test 1: Normal streaming with DONE marker and finish_reason
echo -e "\n${YELLOW}[4/8] Test 1: Normal Streaming (DONE + finish_reason=stop)${NC}"
OUTPUT1=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-openai-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"hello\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT1" "data: \[DONE\]" "DONE marker present"
assert_contains "$OUTPUT1" '"finish_reason":"stop"' "finish_reason=stop"
assert_contains "$OUTPUT1" '"content"' "Content delta present"

# Test 2: Streaming tool call
echo -e "\n${YELLOW}[5/8] Test 2: Streaming Tool Call${NC}"
OUTPUT2=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-openai-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-tool-stream\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT2" '"tool_calls"' "Tool calls field present"
assert_contains "$OUTPUT2" '"function"' "Function field present"
assert_contains "$OUTPUT2" 'get_weather' "Tool name present"
assert_contains "$OUTPUT2" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"
assert_contains "$OUTPUT2" "data: \[DONE\]" "DONE marker present"

# Test 3: Multiple streaming tool calls
echo -e "\n${YELLOW}[6/8] Test 3: Multiple Streaming Tool Calls${NC}"
OUTPUT3=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-openai-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-tool-multi-stream\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT3" 'get_weather' "First tool (get_weather) present"
assert_contains "$OUTPUT3" 'get_time' "Second tool (get_time) present"
assert_contains "$OUTPUT3" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"
assert_contains "$OUTPUT3" "data: \[DONE\]" "DONE marker present"

# Test 4: Reasoning content (DeepSeek-style)
echo -e "\n${YELLOW}[7/8] Test 4: Reasoning Content${NC}"
OUTPUT4=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-openai-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-reasoning\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT4" '"reasoning_content"' "Reasoning content field present"
assert_contains "$OUTPUT4" '"content"' "Regular content present"
assert_contains "$OUTPUT4" '"finish_reason":"stop"' "finish_reason=stop"
assert_contains "$OUTPUT4" "data: \[DONE\]" "DONE marker present"

# Test 5: Reasoning followed by tool call
echo -e "\n${YELLOW}[8/10] Test 5: Reasoning + Tool Call${NC}"
OUTPUT5=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-openai-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-reasoning-tool\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT5" '"reasoning_content"' "Reasoning content field present"
assert_contains "$OUTPUT5" '"tool_calls"' "Tool calls field present"
assert_contains "$OUTPUT5" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"
assert_contains "$OUTPUT5" "data: \[DONE\]" "DONE marker present"

# Test 6: Streaming tool call with malformed JSON arguments
# Note: Tool repair now happens POST-STREAM (after accumulation is complete).
# The mock server sends chunks with partial malformed JSON like: {loc, ation:, etc.
# These chunks are accumulated, and after the stream completes, the complete malformed
# JSON {location: "San Francisco", unit: "celsius"} is repaired to valid JSON
# {"location": "San Francisco", "unit": "celsius"} (quotes added around keys).
echo -e "\n${YELLOW}[9/9] Test 6: Streaming Tool Call with Malformed JSON (Post-Stream Tool Repair)${NC}"
OUTPUT6=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-openai-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-tool-malformed-stream\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT6" '"tool_calls"' "Tool calls field present"
assert_contains "$OUTPUT6" 'get_weather' "Tool name present"
assert_contains "$OUTPUT6" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"
assert_contains "$OUTPUT6" "data: \[DONE\]" "DONE marker present"

# Verify that post-stream tool repair was applied.
# The malformed JSON {location: "San Francisco"} should be repaired to {"location": "San Francisco"}
# Note: In the SSE stream, JSON arguments are escaped, so we look for the escaped form.
# The repair adds quotes around unquoted keys like location -> \"location\"
if echo "$OUTPUT6" | grep -q '\\"location\\"'; then
    echo -e "  ${GREEN}✓${NC} Tool repair: found properly quoted key (malformed JSON was repaired)"
    ((TESTS_PASSED++))
else
    echo -e "  ${RED}✗${NC} Tool repair: missing quoted key (repair may not have been applied)"
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
    echo -e "${GREEN}OpenAI Format Support Verified:${NC}"
    echo -e "  ${YELLOW}✓${NC} Normal streaming with DONE marker"
    echo -e "  ${YELLOW}✓${NC} finish_reason: stop"
    echo -e "  ${YELLOW}✓${NC} finish_reason: tool_calls"
    echo -e "  ${YELLOW}✓${NC} Streaming tool calls"
    echo -e "  ${YELLOW}✓${NC} Multiple tool calls in sequence"
    echo -e "  ${YELLOW}✓${NC} Reasoning content (DeepSeek-style)"
    echo -e "  ${YELLOW}✓${NC} Reasoning + tool call combination"
    echo -e "  ${YELLOW}✓${NC} Post-stream tool repair (malformed JSON repaired after stream completes)"
    exit 0
else
    echo -e "${RED}Some tests failed. Check the output above for details.${NC}"
    exit 1
fi
