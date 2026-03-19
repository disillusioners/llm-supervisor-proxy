#!/bin/bash

# Test script for OpenAI Format Internal Path
# Validates full OpenAI format support: tool calls, reasoning, DONE marker, finish_reason
# Comprehensive testing per OpenAI streaming tool calls spec (docs/openai-streaming-tool-calls-spec.md)
# Maximum runtime: 60 seconds (increased for more edge case tests)

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
export TOOL_CALL_BUFFER_DISABLED="true"

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
echo -e "\n${YELLOW}[4/18] Test 1: Normal Streaming (DONE + finish_reason=stop)${NC}"
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
echo -e "\n${YELLOW}[5/18] Test 2: Streaming Tool Call${NC}"
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
echo -e "\n${YELLOW}[6/18] Test 3: Multiple Streaming Tool Calls${NC}"
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
echo -e "\n${YELLOW}[7/18] Test 4: Reasoning Content${NC}"
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
echo -e "\n${YELLOW}[8/18] Test 5: Reasoning + Tool Call${NC}"
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
echo -e "\n${YELLOW}[9/18] Test 6: Streaming Tool Call with Malformed JSON (Post-Stream Tool Repair)${NC}"
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
if echo "$OUTPUT6" | grep -q '\\"location\\"'; then
    echo -e "  ${GREEN}✓${NC} Tool repair: found properly quoted key (malformed JSON was repaired)"
    ((TESTS_PASSED++))
else
    echo -e "  ${RED}✗${NC} Tool repair: missing quoted key (repair may not have been applied)"
    ((TESTS_FAILED++))
fi

# ============================================================================
# EDGE CASE TESTS (per OpenAI streaming tool calls spec section 9 & 11)
# ============================================================================

# Test 7: Tool call WITHOUT index field (spec section 11 - Gemini compatibility)
# Per spec: "Fallback if index missing → assume 0"
echo -e "\n${YELLOW}[10/18] Test 7: Tool Call WITHOUT Index Field (Gemini-style)${NC}"
OUTPUT7=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-openai-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-tool-no-index\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT7" '"tool_calls"' "Tool calls field present"
assert_contains "$OUTPUT7" 'get_weather' "Tool name present"
assert_contains "$OUTPUT7" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"
assert_contains "$OUTPUT7" "data: \[DONE\]" "DONE marker present"
# The tool call should still be processed despite missing index (defaults to 0)

# Test 8: Tool call WITHOUT ID field (spec section 4 - Ollama compatibility)
# Per spec: "id - Present only in the first chunk (usually)" - should be tolerated
echo -e "\n${YELLOW}[11/18] Test 8: Tool Call WITHOUT ID Field (Ollama-style)${NC}"
OUTPUT8=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-openai-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-tool-no-id\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT8" '"tool_calls"' "Tool calls field present"
assert_contains "$OUTPUT8" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"
assert_contains "$OUTPUT8" "data: \[DONE\]" "DONE marker present"
# Tool call should be processed even without ID

# Test 9: Interleaved tool calls (spec section 5)
# Per spec: "chunks may interleave like: Chunk A → index 0, Chunk B → index 1, Chunk C → index 0"
echo -e "\n${YELLOW}[12/18] Test 9: Interleaved Tool Calls (index 0,1,0,1 pattern)${NC}"
OUTPUT9=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-openai-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-tool-interleaved\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT9" 'get_weather' "First tool (get_weather) present"
assert_contains "$OUTPUT9" 'get_time' "Second tool (get_time) present"
assert_contains "$OUTPUT9" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"
assert_contains "$OUTPUT9" "data: \[DONE\]" "DONE marker present"

# Test 10: Tool call with empty deltas mixed in (spec section 9)
# Per spec: "You may get: { 'delta': {} } - Ignore safely"
echo -e "\n${YELLOW}[13/18] Test 10: Tool Call with Empty Deltas${NC}"
OUTPUT10=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-openai-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-tool-empty-delta\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT10" '"tool_calls"' "Tool calls field present"
assert_contains "$OUTPUT10" 'get_weather' "Tool name present"
assert_contains "$OUTPUT10" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"
assert_contains "$OUTPUT10" "data: \[DONE\]" "DONE marker present"

# Test 11: Minimal tool call - only index field (spec section 9)
# Per spec: "A chunk may contain ONLY: { 'index': 0 }"
echo -e "\n${YELLOW}[14/18] Test 11: Minimal Tool Call (index only)${NC}"
OUTPUT11=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-openai-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-tool-minimal\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT11" '"tool_calls"' "Tool calls field present"
assert_contains "$OUTPUT11" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"
assert_contains "$OUTPUT11" "data: \[DONE\]" "DONE marker present"

# Test 12: Partial JSON arguments (spec section 9)
# Per spec: "Arguments not valid JSON until the end - Do NOT parse early"
echo -e "\n${YELLOW}[15/18] Test 12: Partial JSON Arguments${NC}"
OUTPUT12=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-openai-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-tool-partial-json\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT12" '"tool_calls"' "Tool calls field present"
assert_contains "$OUTPUT12" 'search' "Tool name present"
assert_contains "$OUTPUT12" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"
assert_contains "$OUTPUT12" "data: \[DONE\]" "DONE marker present"

# Test 13: Tool call WITHOUT type field
# Per spec section 4: "type - Always 'function' - Often only appears in first chunk"
echo -e "\n${YELLOW}[16/18] Test 13: Tool Call WITHOUT Type Field${NC}"
OUTPUT13=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-openai-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-tool-no-type\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT13" '"tool_calls"' "Tool calls field present"
assert_contains "$OUTPUT13" 'get_weather' "Tool name present"
assert_contains "$OUTPUT13" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"
assert_contains "$OUTPUT13" "data: \[DONE\]" "DONE marker present"

# Test 14: Tool call with large index value (edge case)
# Tests that proxy handles non-contiguous indices correctly
echo -e "\n${YELLOW}[17/18] Test 14: Tool Call with Large Index (50)${NC}"
OUTPUT14=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-openai-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-tool-large-index\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT14" '"tool_calls"' "Tool calls field present"
assert_contains "$OUTPUT14" 'get_weather' "Tool name present"
assert_contains "$OUTPUT14" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"
assert_contains "$OUTPUT14" "data: \[DONE\]" "DONE marker present"

# Test 15: Sparse index tool calls (indices 0, 5, 10)
# Tests that proxy handles non-contiguous indices correctly
echo -e "\n${YELLOW}[18/18] Test 15: Sparse Index Tool Calls (0, 5, 10)${NC}"
OUTPUT15=$(curl -N -s --max-time 5 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\": \"mock-openai-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"mock-tool-sparse-index\"}],
        \"stream\": true
    }" 2>&1)

assert_contains "$OUTPUT15" 'get_weather' "First tool (get_weather, index 0) present"
assert_contains "$OUTPUT15" 'get_time' "Second tool (get_time, index 5) present"
assert_contains "$OUTPUT15" 'search' "Third tool (search, index 10) present"
assert_contains "$OUTPUT15" '"finish_reason":"tool_calls"' "finish_reason=tool_calls"
assert_contains "$OUTPUT15" "data: \[DONE\]" "DONE marker present"

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
    echo -e "  ${YELLOW}✓${NC} Post-stream tool repair (malformed JSON)"
    echo -e ""
    echo -e "${GREEN}Edge Case Support (per OpenAI spec):${NC}"
    echo -e "  ${YELLOW}✓${NC} Tool call without index (Gemini compatibility)"
    echo -e "  ${YELLOW}✓${NC} Tool call without ID (Ollama compatibility)"
    echo -e "  ${YELLOW}✓${NC} Interleaved tool calls"
    echo -e "  ${YELLOW}✓${NC} Empty deltas handling"
    echo -e "  ${YELLOW}✓${NC} Minimal tool call (index only)"
    echo -e "  ${YELLOW}✓${NC} Partial JSON arguments"
    echo -e "  ${YELLOW}✓${NC} Tool call without type field"
    echo -e "  ${YELLOW}✓${NC} Large index values"
    echo -e "  ${YELLOW}✓${NC} Sparse index values"
    exit 0
else
    echo -e "${RED}Some tests failed. Check the output above for details.${NC}"
    exit 1
fi
