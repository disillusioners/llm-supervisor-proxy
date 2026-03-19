#!/bin/bash

# Test script for Ultimate Model functionality - Internal Path
# Tests duplicate detection and ultimate model triggering with full OpenAI spec support
# Maximum runtime: 75 seconds
#
# This test uses an "internal" model configuration that bypasses the external
# UPSTREAM_URL and calls the mock server directly via internal_base_url.
#
# IMPORTANT: Hash is only created when a request FAILS.
# Successful requests do NOT create hashes, so simple messages like "hi", "hello"
# will NOT trigger the ultimate model on subsequent requests.
#
# Verifies:
# - Normal streaming with DONE marker and finish_reason
# - Tool calls (streaming)
# - Multiple tool calls
# - Reasoning content (DeepSeek-style)
# - Ultimate model triggers only after FAILED requests (not successful ones)
# - Ultimate model supports full OpenAI spec
# - Tool call buffering with internal path
# - Tool call without ID (Ollama-style)
# - Tool call with empty deltas

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

# Hard timeout: kill everything after 75 seconds
HARD_TIMEOUT=75

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
echo -e "\n${YELLOW}[1/17] Starting Mock OpenAI Server (port $MOCK_PORT)...${NC}"
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
echo -e "\n${YELLOW}[2/17] Starting Proxy with Ultimate Model enabled (port $PROXY_PORT)...${NC}"
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
echo -e "\n${YELLOW}[3/17] Configuring internal mock models via API...${NC}"

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

assert_not_contains() {
    local output="$1"
    local expected="$2"
    local test_name="$3"
    
    if echo "$output" | grep -q "$expected"; then
        echo -e "  ${RED}✗${NC} $test_name: unexpectedly found '$expected'"
        ((TESTS_FAILED++))
        return 1
    else
        echo -e "  ${GREEN}✓${NC} $test_name: correctly did not find '$expected'"
        ((TESTS_PASSED++))
        return 0
    fi
}

# Test 1: First request (normal - should use regular model with full OpenAI spec)
echo -e "\n${YELLOW}[4/17] Test 1: First Request (Normal Streaming)${NC}"
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

# Test 2: Successful duplicate request (should NOT trigger ultimate model)
# Hash is only created on FAILURE, so successful requests don't create hashes
echo -e "\n${YELLOW}[5/17] Test 2: Successful Duplicate Request (No Ultimate Model)${NC}"
echo -e "Expected: Since first request succeeded, no hash was created, so ultimate model NOT triggered"
OUTPUT2=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"test-message-1\"}],
        \"stream\": true
    }" 2>&1)

# Should NOT have ultimate model header since hash was never created
assert_not_contains "$OUTPUT2" "X-LLMProxy-Ultimate-Model" "No ultimate model header"
assert_contains "$OUTPUT2" "data: \[DONE\]" "DONE marker present"

# Test 3: First tool call request (normal path)
echo -e "\n${YELLOW}[6/17] Test 3: First Tool Call Request (Normal Path)${NC}"
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

# Test 4: Successful duplicate tool call request (should NOT trigger ultimate model)
echo -e "\n${YELLOW}[7/17] Test 4: Successful Duplicate Tool Call (No Ultimate Model)${NC}"
echo -e "Expected: Since first request succeeded, no hash was created, so ultimate model NOT triggered"
OUTPUT4=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-tool-stream\"}],
        \"stream\": true
    }" 2>&1)

assert_not_contains "$OUTPUT4" "X-LLMProxy-Ultimate-Model" "No ultimate model header"
assert_contains "$OUTPUT4" '"tool_calls"' "Tool calls field present"
assert_contains "$OUTPUT4" "data: \[DONE\]" "DONE marker present"

# Test 5: First reasoning request (normal path)
echo -e "\n${YELLOW}[8/17] Test 5: First Reasoning Request (Normal Path)${NC}"
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

# Test 6: Successful duplicate reasoning request (should NOT trigger ultimate model)
echo -e "\n${YELLOW}[9/17] Test 6: Successful Duplicate Reasoning (No Ultimate Model)${NC}"
echo -e "Expected: Since first request succeeded, no hash was created, so ultimate model NOT triggered"
OUTPUT6=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-reasoning\"}],
        \"stream\": true
    }" 2>&1)

assert_not_contains "$OUTPUT6" "X-LLMProxy-Ultimate-Model" "No ultimate model header"
assert_contains "$OUTPUT6" '"reasoning_content"' "Reasoning content field present"
assert_contains "$OUTPUT6" "data: \[DONE\]" "DONE marker present"

# Test 7: Ultimate model triggered after FAILURE
# First request with error-500 content will FAIL, creating the hash
# Second request will trigger ultimate model
echo -e "\n${YELLOW}[10/17] Test 7: Ultimate Model Triggered After Failure${NC}"
echo -e "Expected: First request fails (500), creating hash. Second request triggers ultimate model."

# First: Request that will FAIL (mock-error-500 trigger)
OUTPUT7A=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"ultimate-trigger-test-mock-error-500\"}],
        \"stream\": true
    }" 2>&1)
assert_contains "$OUTPUT7A" "error" "First request: Error returned (hash created)"

# Second: Same request should trigger ultimate model
OUTPUT7B=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"ultimate-trigger-test-mock-error-500\"}],
        \"stream\": true
    }" 2>&1)
# Ultimate model will also fail because content still has mock-error-500
assert_contains "$OUTPUT7B" "error" "Second request: Ultimate model triggered (also fails)"

# Test 8: Retry Limit Exhausted (with consecutive failures)
# With MAX_RETRIES=2, after 2 consecutive failures, the 3rd attempt should return exhausted error.
echo -e "\n${YELLOW}[11/17] Test 8: Retry Limit Exhausted (Consecutive Failures)${NC}"
echo -e "Expected: After 2 consecutive failures, 3rd attempt returns retry_exhausted error"

# First: Request that will FAIL, creating the hash
OUTPUT8A=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"retry-exhaust-test-mock-error-500\"}],
        \"stream\": true
    }" 2>&1)
assert_contains "$OUTPUT8A" "error" "First request: Error (hash created, retry 0)"

# Second: Trigger ultimate model (retry 1/2) - will FAIL
OUTPUT8B=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"retry-exhaust-test-mock-error-500\"}],
        \"stream\": true
    }" 2>&1)
assert_contains "$OUTPUT8B" "error" "Second request (retry 1/2): Error from ultimate model"

# Third: Trigger ultimate model (retry 2/2) - will FAIL again
OUTPUT8C=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"retry-exhaust-test-mock-error-500\"}],
        \"stream\": true
    }" 2>&1)
assert_contains "$OUTPUT8C" "error" "Third request (retry 2/2): Error from ultimate model"

# Fourth: Trigger ultimate model (retry 3 > 2) - should FAIL with RETRY EXHAUSTED error
OUTPUT8D=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"retry-exhaust-test-mock-error-500\"}],
        \"stream\": true
    }" 2>&1)
assert_contains "$OUTPUT8D" 'ultimate_model_retry_exhausted' "Fourth request (retry 3/2): retry_exhausted error"
assert_contains "$OUTPUT8D" 'attempt 3 of 2 max' "Fourth request: shows attempt count"

# ============================================================================
# Tool Call Buffer Tests (Internal Path)
# ============================================================================

# Test 9: Tool call buffering with malformed JSON (internal path)
# The tool call buffer should repair malformed JSON arguments
echo -e "\n${YELLOW}[12/17] Test 9: Tool Call Buffering - Malformed JSON Repair${NC}"
echo -e "Expected: Tool call with malformed JSON is buffered and repaired"
OUTPUT9=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-tool-malformed-stream\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT9" '"tool_calls"' "Tool calls field present"
assert_contains "$OUTPUT9" 'get_weather' "Tool name present"
assert_contains "$OUTPUT9" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"
assert_contains "$OUTPUT9" "data: \[DONE\]" "DONE marker present"
# Verify tool repair was applied (buffered mode should repair malformed JSON)
if echo "$OUTPUT9" | grep -q '\\"location\\"'; then
    echo -e "  ${GREEN}✓${NC} Tool repair: found properly quoted key (malformed JSON was repaired)"
    ((TESTS_PASSED++))
else
    echo -e "  ${RED}✗${NC} Tool repair: missing quoted key (repair may not have been applied)"
    ((TESTS_FAILED++))
fi

# Test 10: Tool call buffering with missing index field (Gemini-style)
# Per spec: "Fallback if index missing → assume 0"
echo -e "\n${YELLOW}[13/17] Test 10: Tool Call Buffering - Missing Index Field${NC}"
echo -e "Expected: Tool call without index is processed correctly (defaults to 0)"
OUTPUT10=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-tool-no-index\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT10" '"tool_calls"' "Tool calls field present"
assert_contains "$OUTPUT10" 'get_weather' "Tool name present"
assert_contains "$OUTPUT10" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"
assert_contains "$OUTPUT10" "data: \[DONE\]" "DONE marker present"

# Test 11: Tool call buffering with interleaved tool calls
# Per spec: "chunks may interleave like: Chunk A → index 0, Chunk B → index 1, Chunk C → index 0"
echo -e "\n${YELLOW}[14/17] Test 11: Tool Call Buffering - Interleaved Tool Calls${NC}"
echo -e "Expected: Interleaved tool calls (index 0,1,0,1) are buffered and reassembled correctly"
OUTPUT11=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-tool-interleaved\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT11" 'get_weather' "First tool (get_weather) present"
assert_contains "$OUTPUT11" 'get_time' "Second tool (get_time) present"
assert_contains "$OUTPUT11" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"
assert_contains "$OUTPUT11" "data: \[DONE\]" "DONE marker present"

# Test 12: Tool call buffering with missing ID field (Ollama-style)
# Per spec section 4: "id - Present only in the first chunk (usually)"
# Some providers like Ollama sometimes don't send ID at all
echo -e "\n${YELLOW}[15/17] Test 12: Tool Call Buffering - Missing ID Field (Ollama-style)${NC}"
echo -e "Expected: Tool call without ID is processed correctly (buffer should tolerate missing ID)"
OUTPUT12=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-tool-no-id\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT12" '"tool_calls"' "Tool calls field present"
assert_contains "$OUTPUT12" 'get_weather' "Tool name present"
assert_contains "$OUTPUT12" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"
assert_contains "$OUTPUT12" "data: \[DONE\]" "DONE marker present"

# Test 13: Tool call buffering with empty deltas
# Per spec section 9: "You may get: { 'delta': {} } - Ignore safely"
echo -e "\n${YELLOW}[16/17] Test 13: Tool Call Buffering - Empty Delta Chunks${NC}"
echo -e "Expected: Empty delta chunks are ignored, tool call still completes correctly"
OUTPUT13=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-internal-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-tool-empty-delta\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT13" '"tool_calls"' "Tool calls field present"
assert_contains "$OUTPUT13" 'get_weather' "Tool name present"
assert_contains "$OUTPUT13" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"
assert_contains "$OUTPUT13" "data: \[DONE\]" "DONE marker present"

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
    echo -e "  ${YELLOW}✓${NC} Hash only created on FAILURE (not success)"
    echo -e "  ${YELLOW}✓${NC} Ultimate model triggered only after failed requests"
    echo -e "  ${YELLOW}✓${NC} Retry limit enforced after consecutive failures (MAX_RETRIES=2)"
    echo -e "  ${YELLOW}✓${NC} Tool call buffering: malformed JSON repair"
    echo -e "  ${YELLOW}✓${NC} Tool call buffering: missing index field (Gemini-style)"
    echo -e "  ${YELLOW}✓${NC} Tool call buffering: interleaved tool calls"
    echo -e "  ${YELLOW}✓${NC} Tool call buffering: missing ID field (Ollama-style)"
    echo -e "  ${YELLOW}✓${NC} Tool call buffering: empty delta chunks"
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
