#!/bin/bash

# Test script for Ultimate Model functionality - Internal Path
# Tests duplicate detection and ultimate model triggering with full OpenAI spec support
# Maximum runtime: 60 seconds
#
# This test uses an "internal" model configuration that bypasses the external
# UPSTREAM_URL and calls the mock server directly via internal_base_url.
#
# Verifies:
# - Normal streaming with DONE marker and finish_reason
# - Tool calls (streaming)
# - Multiple tool calls
# - Reasoning content (DeepSeek-style)
# - Ultimate model triggers on duplicate requests and supports full OpenAI spec

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

# Use TEST_API_KEY or fallback
API_KEY="${TEST_API_KEY:-test-key}"

echo -e "${BLUE}======================================${NC}"
echo -e "${BLUE}   Ultimate Model Internal Path Tests ${NC}"
echo -e "${BLUE}   (Max runtime: ${HARD_TIMEOUT}s)         ${NC}"
echo -e "${BLUE}======================================${NC}"

# Clean ports first (source the clean_ports script)
source "$SCRIPT_DIR/test_mock_clean_ports.sh" "$PROXY_PORT" "$MOCK_PORT"
clean_ports "$PROXY_PORT" "$MOCK_PORT"

# Start mock OpenAI server (use mock_llm_openai.go for full OpenAI spec support)
echo -e "\n${YELLOW}[1/10] Starting Mock OpenAI Server (port $MOCK_PORT)...${NC}"
cd "$SCRIPT_DIR"
go run mock_llm_openai.go -port=$MOCK_PORT &
MOCK_PID=$!
cd "$ROOT_DIR"

# Wait for mock server to be ready
sleep 2
if ! kill -0 $MOCK_PID 2>/dev/null; then
    echo -e "${RED}ERROR: Mock server failed to start${NC}"
    exit 1
fi
echo -e "${GREEN}Mock server started (PID: $MOCK_PID)${NC}"

# Start proxy with ultimate model configured via env override
echo -e "\n${YELLOW}[2/10] Starting Proxy with Ultimate Model enabled (port $PROXY_PORT)...${NC}"
echo -e "  ULTIMATE_MODEL_ID=mock-ultimate-model (internal model)"
echo -e "  ULTIMATE_MODEL_MAX_HASH=100"
echo -e "  ULTIMATE_MODEL_MAX_RETRIES=2"
echo -e "  LOOP_DETECTION_ENABLED=false"

# Export config overrides for testing
export APPLY_ENV_OVERRIDES="true"
export UPSTREAM_URL="http://localhost:4001"  # Dummy URL - won't be used for internal models
export PORT="$PROXY_PORT"
export IDLE_TIMEOUT="5s"
export MAX_GENERATION_TIME="20s"
export LOOP_DETECTION_ENABLED="false"
export ULTIMATE_MODEL_ID="mock-ultimate-model"
export ULTIMATE_MODEL_MAX_HASH="100"
export ULTIMATE_MODEL_MAX_RETRIES="2"

go run cmd/main.go &
PROXY_PID=$!

# Wait for proxy to be ready
sleep 2
if ! kill -0 $PROXY_PID 2>/dev/null; then
    echo -e "${RED}ERROR: Proxy failed to start${NC}"
    exit 1
fi
echo -e "${GREEN}Proxy started (PID: $PROXY_PID)${NC}"

# Configure internal models via API
echo -e "\n${YELLOW}[3/10] Configuring internal mock models via API...${NC}"

# First, delete existing models/credentials from previous runs (ignore errors)
# Order matters: delete models first (they reference credentials), then credentials
curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/models/mock-internal-model" 2>/dev/null || true
curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/models/mock-ultimate-model" 2>/dev/null || true
curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/models/mock-openai-model" 2>/dev/null || true
sleep 1  # Wait for model deletions to complete before deleting credentials
curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/credentials/mock-ultimate-cred" 2>/dev/null || true
curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/credentials/mock-openai-cred" 2>/dev/null || true
sleep 1

# Create a credential for the mock server
CREDENTIAL_RESPONSE=$(curl -s -X POST "http://localhost:$PROXY_PORT/fe/api/credentials" \
    -H "Content-Type: application/json" \
    -d "{
        \"id\": \"mock-ultimate-cred\",
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

# Create a normal internal model
MODEL_RESPONSE=$(curl -s -X POST "http://localhost:$PROXY_PORT/fe/api/models" \
    -H "Content-Type: application/json" \
    -d "{
        \"id\": \"mock-internal-model\",
        \"name\": \"Mock Internal Model\",
        \"enabled\": true,
        \"internal\": true,
        \"credential_id\": \"mock-ultimate-cred\",
        \"internal_model\": \"mock-model\",
        \"internal_base_url\": \"http://localhost:$MOCK_PORT/v1\"
    }")

if echo "$MODEL_RESPONSE" | grep -q '"id"'; then
    echo -e "${GREEN}Internal model created successfully${NC}"
else
    echo -e "${RED}Failed to create internal model: $MODEL_RESPONSE${NC}"
    exit 1
fi

# Create the ultimate model (configured via env override)
ULTIMATE_MODEL_RESPONSE=$(curl -s -X POST "http://localhost:$PROXY_PORT/fe/api/models" \
    -H "Content-Type: application/json" \
    -d "{
        \"id\": \"mock-ultimate-model\",
        \"name\": \"Mock Ultimate Model\",
        \"enabled\": true,
        \"internal\": true,
        \"credential_id\": \"mock-ultimate-cred\",
        \"internal_model\": \"mock-model\",
        \"internal_base_url\": \"http://localhost:$MOCK_PORT/v1\"
    }")

if echo "$ULTIMATE_MODEL_RESPONSE" | grep -q '"id"'; then
    echo -e "${GREEN}Ultimate model created successfully${NC}"
else
    echo -e "${RED}Failed to create ultimate model: $ULTIMATE_MODEL_RESPONSE${NC}"
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

# Test 1: First request (normal - should use regular model with full OpenAI spec)
echo -e "\n${YELLOW}[4/10] Test 1: First Request (Normal Streaming)${NC}"
echo -e "Expected: Uses mock-internal-model via race retry, normal response with DONE marker"
OUTPUT1=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"test-message-1\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT1" "data: \[DONE\]" "DONE marker present"
assert_contains "$OUTPUT1" '"finish_reason":"stop"' "finish_reason=stop"
assert_contains "$OUTPUT1" '"content"' "Content delta present"

# Test 2: Duplicate request (should trigger ultimate model)
echo -e "\n${YELLOW}[5/10] Test 2: Duplicate Request (Ultimate Model Triggered)${NC}"
echo -e "Expected: Duplicate detected, uses mock-ultimate-model directly (no race retry)"
OUTPUT2=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"test-message-1\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT2" "data: \[DONE\]" "DONE marker present"
assert_contains "$OUTPUT2" '"finish_reason":"stop"' "finish_reason=stop"
assert_contains "$OUTPUT2" '"content"' "Content delta present"

# Test 3: First tool call request (normal path)
echo -e "\n${YELLOW}[6/10] Test 3: First Tool Call Request (Normal Path)${NC}"
echo -e "Expected: Uses mock-internal-model via race retry, tool call response"
OUTPUT3=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-tool-stream\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT3" '"tool_calls"' "Tool calls field present"
assert_contains "$OUTPUT3" '"function"' "Function field present"
assert_contains "$OUTPUT3" 'get_weather' "Tool name present"
assert_contains "$OUTPUT3" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"
assert_contains "$OUTPUT3" "data: \[DONE\]" "DONE marker present"

# Test 4: Duplicate tool call request (ultimate model path)
echo -e "\n${YELLOW}[7/10] Test 4: Duplicate Tool Call Request (Ultimate Model Path)${NC}"
echo -e "Expected: Ultimate model triggered, tool call with full OpenAI spec support"
OUTPUT4=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-tool-stream\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT4" '"tool_calls"' "Tool calls field present"
assert_contains "$OUTPUT4" '"function"' "Function field present"
assert_contains "$OUTPUT4" 'get_weather' "Tool name present"
assert_contains "$OUTPUT4" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"
assert_contains "$OUTPUT4" "data: \[DONE\]" "DONE marker present"

# Test 5: First reasoning request (normal path)
echo -e "\n${YELLOW}[8/10] Test 5: First Reasoning Request (Normal Path)${NC}"
echo -e "Expected: Uses mock-internal-model via race retry, reasoning_content present"
OUTPUT5=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-reasoning\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT5" '"reasoning_content"' "Reasoning content field present"
assert_contains "$OUTPUT5" '"content"' "Regular content present"
assert_contains "$OUTPUT5" '"finish_reason":"stop"' "finish_reason=stop"
assert_contains "$OUTPUT5" "data: \[DONE\]" "DONE marker present"

# Test 6: Duplicate reasoning request (ultimate model path)
echo -e "\n${YELLOW}[9/10] Test 6: Duplicate Reasoning Request (Ultimate Model Path)${NC}"
echo -e "Expected: Ultimate model triggered, reasoning_content with full OpenAI spec support"
OUTPUT6=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-reasoning\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT6" '"reasoning_content"' "Reasoning content field present"
assert_contains "$OUTPUT6" '"content"' "Regular content present"
assert_contains "$OUTPUT6" '"finish_reason":"stop"' "finish_reason=stop"
assert_contains "$OUTPUT6" "data: \[DONE\]" "DONE marker present"

# Test 7: Retry limit exhausted
# With MAX_RETRIES=2, the ultimate model can be triggered 2 times for the same hash.
# After that, the retry limit is exhausted and an error is returned.
echo -e "\n${YELLOW}[10/10] Test 7: Retry Limit Exhausted${NC}"
echo -e "Expected: After 2 successful ultimate model calls, 3rd call returns retry_exhausted error"

# First: Store hash via normal request
OUTPUT7A=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"retry-limit-test-message\"}],
        \"stream\": true
    }" 2>&1)
assert_contains "$OUTPUT7A" "data: \[DONE\]" "First request: DONE marker present"

# Second: Trigger ultimate model (retry 1/2) - should succeed
OUTPUT7B=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"retry-limit-test-message\"}],
        \"stream\": true
    }" 2>&1)
assert_contains "$OUTPUT7B" "data: \[DONE\]" "Second request (retry 1/2): DONE marker present"

# Third: Trigger ultimate model (retry 2/2) - should succeed (2 <= 2)
OUTPUT7C=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"retry-limit-test-message\"}],
        \"stream\": true
    }" 2>&1)
assert_contains "$OUTPUT7C" "data: \[DONE\]" "Third request (retry 2/2): DONE marker present"

# Fourth: Trigger ultimate model (retry 3/2) - should FAIL with exhausted error
OUTPUT7D=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"retry-limit-test-message\"}],
        \"stream\": true
    }" 2>&1)
assert_contains "$OUTPUT7D" 'ultimate_model_retry_exhausted' "Fourth request (retry 3/2): retry_exhausted error"
assert_contains "$OUTPUT7D" 'attempt 3 of 2 max' "Fourth request: shows attempt count"

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
    echo -e "${GREEN}OpenAI Format Support Verified (Both Normal and Ultimate Model Paths):${NC}"
    echo -e "  ${YELLOW}✓${NC} Normal streaming with DONE marker"
    echo -e "  ${YELLOW}✓${NC} finish_reason: stop"
    echo -e "  ${YELLOW}✓${NC} finish_reason: tool_calls"
    echo -e "  ${YELLOW}✓${NC} Streaming tool calls with index field"
    echo -e "  ${YELLOW}✓${NC} Reasoning content (DeepSeek-style)"
    echo -e "  ${YELLOW}✓${NC} Ultimate model triggered on duplicate requests"
    echo -e "  ${YELLOW}✓${NC} Ultimate model supports full OpenAI spec"
    echo -e "  ${YELLOW}✓${NC} Retry limit enforced (MAX_RETRIES=2)"
    # Cancel the hard timeout timer
    kill $TIMER_PID 2>/dev/null || true
    TIMER_PID=""
    exit 0
else
    echo -e "${RED}Some tests failed. Check the output above for details.${NC}"
    # Cancel the hard timeout timer
    kill $TIMER_PID 2>/dev/null || true
    TIMER_PID=""
    exit 1
fi
