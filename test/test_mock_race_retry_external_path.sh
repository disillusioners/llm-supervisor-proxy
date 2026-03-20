#!/bin/bash

# Test script for Race Retry functionality
# Tests idle timeout spawning and stream deadline behavior
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
PROXY_PORT=4323
MOCK_PID=""
PROXY_PID=""
TIMER_PID=""

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
    pkill -f "mock_llm_race.go" 2>/dev/null || true
    pkill -f "cmd/main.go" 2>/dev/null || true
    lsof -ti :4001 | xargs kill -9 2>/dev/null || true
    lsof -ti :4321 | xargs kill -9 2>/dev/null || true
    wait 2>/dev/null || true
}
trap cleanup_all EXIT

# Start background timer for hard timeout - will exit script after HARD_TIMEOUT seconds
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
echo -e "${BLUE}   Race Retry Functionality Tests    ${NC}"
echo -e "${BLUE}   (Max runtime: ${HARD_TIMEOUT}s)         ${NC}"
echo -e "${BLUE}======================================${NC}"

# Clean ports first (source the clean_ports script)
source "$SCRIPT_DIR/test_mock_clean_ports.sh" "$PROXY_PORT" "$MOCK_PORT"
clean_ports "$PROXY_PORT" "$MOCK_PORT"

# Start mock LLM server with short timeouts for testing
echo -e "\n${YELLOW}[1/8] Starting Mock LLM Race Server (port $MOCK_PORT)...${NC}"
echo -e "  idle-pause=3s (should trigger proxy's 1s idle timeout)"
echo -e "  deadline-interval=1s (should trigger proxy's 6s deadline)"
echo -e "  slow-start=1s (should complete before idle timeout)"
cd "$SCRIPT_DIR"
go run mock_llm_race.go -port=$MOCK_PORT -idle-pause=3 -deadline-interval=1 -slow-start=1 &
MOCK_PID=$!
cd "$ROOT_DIR"

# Wait for mock server to be ready
sleep 2
if ! kill -0 $MOCK_PID 2>/dev/null; then
    echo -e "${RED}ERROR: Mock server failed to start${NC}"
    exit 1
fi
echo -e "${GREEN}Mock server started (PID: $MOCK_PID)${NC}"

# Start proxy with race retry enabled
echo -e "\n${YELLOW}[2/8] Starting Proxy with Race Retry enabled...${NC}"
echo -e "  IDLE_TIMEOUT=1s (short for testing)"
echo -e "  STREAM_DEADLINE=6s (pick best buffer after 6s)"
echo -e "  MAX_GENERATION_TIME=15s (absolute hard timeout)"

# Export config overrides for testing (these override config file)
export APPLY_ENV_OVERRIDES="true"
export UPSTREAM_URL="http://localhost:$MOCK_PORT"
export PORT="$PROXY_PORT"
export IDLE_TIMEOUT="1s"
export STREAM_DEADLINE="6s"
export MAX_GENERATION_TIME="15s"
export RACE_RETRY_ENABLED="true"
export RACE_PARALLEL_ON_IDLE="true"
export RACE_MAX_PARALLEL="3"
export LOOP_DETECTION_ENABLED="false"

go run cmd/main.go &
PROXY_PID=$!

# Wait for proxy to be ready
sleep 2
if ! kill -0 $PROXY_PID 2>/dev/null; then
    echo -e "${RED}ERROR: Proxy failed to start${NC}"
    exit 1
fi
echo -e "${GREEN}Proxy started (PID: $PROXY_PID)${NC}"

# Function to make a streaming request and show results
test_streaming() {
    local test_name="$1"
    local prompt="$2"
    local max_time="$3"
    
    echo -e "\n${BLUE}=== Test: $test_name ===${NC}"
    echo -e "Prompt: $prompt"
    echo -e "Max time: ${max_time}s"
    echo -e "Response:"
    
    start_time=$(date +%s)
    
    # Request goes to proxy which routes to UPSTREAM_URL (mock server)
    curl -N -s --max-time "$max_time" "http://localhost:$PROXY_PORT/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $API_KEY" \
        -d "{
            \"model\": \"mock-model\",
            \"messages\": [{\"role\": \"user\", \"content\": \"$prompt\"}],
            \"stream\": true
        }" 2>&1 | head -n 20
    
    end_time=$(date +%s)
    duration=$((end_time - start_time))
    echo -e "\n${YELLOW}Duration: ${duration}s${NC}"
}

# Function to test error response and verify OpenCode-compatible format
test_error_response() {
    local test_name="$1"
    local prompt="$2"
    local max_time="$3"
    local expected_type="$4"
    local expected_code="$5"
    local test_streaming="$6"  # "true" for streaming, "false" for non-streaming
    
    echo -e "\n${BLUE}=== Test: $test_name ===${NC}"
    echo -e "Prompt: $prompt"
    echo -e "Expected type: $expected_type"
    echo -e "Expected code: $expected_code"
    echo -e "Streaming: $test_streaming"
    echo -e "Response:"
    
    local curl_opts="-s --max-time $max_time"
    local body_json="{
        \"model\": \"mock-model\",
        \"messages\": [{\"role\": \"user\", \"content\": \"$prompt\"}]
    }"
    
    # Add stream:true if streaming test
    if [ "$test_streaming" = "true" ]; then
        body_json="{
            \"model\": \"mock-model\",
            \"messages\": [{\"role\": \"user\", \"content\": \"$prompt\"}],
            \"stream\": true
        }"
        curl_opts="-N $curl_opts"
    fi
    
    local response
    response=$(curl $curl_opts "http://localhost:$PROXY_PORT/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $API_KEY" \
        -d "$body_json" 2>&1)
    
    echo "$response"
    echo ""
    
    # Verify OpenCode-compatible error format
    local success=true
    
    # Check for type:"error" at root level
    if echo "$response" | grep -q '"type":"error"'; then
        echo -e "${GREEN}✓ Has root type:\"error\"${NC}"
    else
        echo -e "${RED}✗ Missing root type:\"error\"${NC}"
        success=false
    fi
    
    # Check for expected error.type
    if echo "$response" | grep -q "\"type\":\"$expected_type\""; then
        echo -e "${GREEN}✓ Has error.type:\"$expected_type\"${NC}"
    else
        echo -e "${RED}✗ Missing error.type:\"$expected_type\"${NC}"
        success=false
    fi
    
    # Check for error.code if expected
    if [ -n "$expected_code" ]; then
        if echo "$response" | grep -q "\"code\":\"$expected_code\""; then
            echo -e "${GREEN}✓ Has error.code:\"$expected_code\"${NC}"
        else
            echo -e "${RED}✗ Missing error.code:\"$expected_code\"${NC}"
            success=false
        fi
    fi
    
    # Return test result
    if [ "$success" = "true" ]; then
        echo -e "${GREEN}✓ PASS: OpenCode-compatible error format verified${NC}"
        return 0
    else
        echo -e "${RED}✗ FAIL: Error format mismatch${NC}"
        return 1
    fi
}

# Test 1: Fast complete (baseline - should complete quickly)
echo -e "\n${YELLOW}[3/8] Test 1: Fast Complete (baseline)${NC}"
echo -e "Expected: Completes quickly without spawning parallel requests"
test_streaming "Fast Complete" "mock-fast-complete" 3

# Test 2: Slow start (should complete after initial delay)
echo -e "\n${YELLOW}[4/8] Test 2: Slow Start${NC}"
echo -e "Expected: Waits 1s then completes quickly"
test_streaming "Slow Start" "mock-slow-start" 4

# Test 3: Idle timeout (main scenario - should spawn parallel requests)
echo -e "\n${YELLOW}[5/8] Test 3: Idle Timeout (KEY TEST)${NC}"
echo -e "Expected: After IDLE_TIMEOUT (1s), proxy spawns parallel requests"
test_streaming "Idle Timeout" "mock-idle-timeout" 6

# Test 4: Streaming deadline (should pick best buffer)
echo -e "\n${YELLOW}[5/10] Test 4: Streaming Deadline (KEY TEST)${NC}"
echo -e "Expected: After STREAM_DEADLINE (6s), proxy picks best buffer"
test_streaming "Streaming Deadline" "mock-streaming-deadline" 8

# Test 5: Rate limit error - OpenCode-compatible format
echo -e "\n${YELLOW}[6/10] Test 5: Rate Limit Error (OpenCode Format)${NC}"
echo -e "Expected: OpenCode-compatible error with type:rate_limit and code:rate_limit"
test_error_response "Rate Limit" "mock-rate-limit-error" 3 "rate_limit" "rate_limit" "true"

# Test 6: Context overflow error - OpenCode should NOT retry
echo -e "\n${YELLOW}[7/10] Test 6: Context Overflow Error (No Retry)${NC}"
echo -e "Expected: type:context_length_exceeded (no code - triggers compaction)"
test_error_response "Context Overflow" "mock-context-overflow" 3 "context_length_exceeded" "" "true"

# Test 7: Upstream unavailable - should have code "unavailable"
echo -e "\n${YELLOW}[8/10] Test 7: Upstream Unavailable (OpenCode Format)${NC}"
echo -e "Expected: type:upstream_error with code:unavailable"
test_error_response "Upstream Unavailable" "mock-upstream-unavailable" 3 "upstream_error" "unavailable" "true"

# Summary
echo -e "\n${BLUE}======================================${NC}"
echo -e "${BLUE}           Test Summary              ${NC}"
echo -e "${BLUE}======================================${NC}"
echo -e ""
echo -e "${GREEN}Tests completed. Check the proxy logs above for:${NC}"
echo -e ""
echo -e "  ${YELLOW}Idle Timeout Test:${NC}"
echo -e "    Look for: [RACE] Main request idle, spawning parallel request"
echo -e ""
echo -e "  ${YELLOW}Streaming Deadline Test:${NC}"
echo -e "    Look for: [RACE] Streaming deadline reached, picking best buffer"
echo -e ""
echo -e "  ${YELLOW}OpenCode Error Format Tests:${NC}"
echo -e "    ✓ Rate Limit: type:\"rate_limit\", code:\"rate_limit\""
echo -e "    ✓ Context Overflow: type:\"context_length_exceeded\" (no code)"
echo -e "    ✓ Upstream Unavailable: type:\"upstream_error\", code:\"unavailable\""
echo -e ""
echo -e "${GREEN}If you see these log messages, the features are working correctly!${NC}"

# Cancel the hard timeout timer
kill $TIMER_PID 2>/dev/null || true
TIMER_PID=""
