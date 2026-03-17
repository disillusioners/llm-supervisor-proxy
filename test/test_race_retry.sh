#!/bin/bash

# Test script for Race Retry functionality
# Tests idle timeout spawning and stream deadline behavior

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

# Load test API key from .env-test
if [ -f "$ROOT_DIR/.env-test" ]; then
    export $(grep -v '^#' "$ROOT_DIR/.env-test" | xargs)
fi

# Use TEST_API_KEY or fallback
API_KEY="${TEST_API_KEY:-test-key}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Test configuration
MOCK_PORT=4001
PROXY_PORT=4321
MOCK_PID=""
PROXY_PID=""

cleanup() {
    echo -e "\n${YELLOW}Cleaning up...${NC}"
    if [ ! -z "$MOCK_PID" ]; then
        kill $MOCK_PID 2>/dev/null || true
    fi
    if [ ! -z "$PROXY_PID" ]; then
        kill $PROXY_PID 2>/dev/null || true
    fi
    # Wait for processes to fully terminate
    sleep 1
}

trap cleanup EXIT

echo -e "${BLUE}======================================${NC}"
echo -e "${BLUE}   Race Retry Functionality Tests    ${NC}"
echo -e "${BLUE}======================================${NC}"

# Start mock LLM server with short timeouts for testing
echo -e "\n${YELLOW}[1/6] Starting Mock LLM Race Server (port $MOCK_PORT)...${NC}"
echo -e "  idle-pause=8s (should trigger proxy's 5s idle timeout)"
echo -e "  deadline-interval=2s (should trigger proxy's 20s deadline)"
cd "$SCRIPT_DIR"
go run mock_llm_race.go -port=$MOCK_PORT -idle-pause=8 -deadline-interval=2 &
MOCK_PID=$!
cd "$ROOT_DIR"

# Wait for mock server to be ready
sleep 2
if ! kill -0 $MOCK_PID 2>/dev/null; then
    echo -e "${RED}ERROR: Mock server failed to start${NC}"
    exit 1
fi
echo -e "${GREEN}Mock server started (PID: $MOCK_PID)${NC}"

# Kill any existing processes on the ports
pkill -f "cmd/main.go" 2>/dev/null || true
lsof -ti :$PROXY_PORT | xargs kill 2>/dev/null || true
sleep 1

# Start proxy with race retry enabled
# Note: We use the existing config file but override specific settings via env vars
# The test upstream URL is passed via X-LLMProxy-Test-Upstream header in requests
echo -e "\n${YELLOW}[2/6] Starting Proxy with Race Retry enabled...${NC}"
echo -e "  Using existing config file (proxy will use default upstream)"
echo -e "  Test requests will use X-LLMProxy-Test-Upstream header to route to mock"
echo -e "  IDLE_TIMEOUT=5s (short for testing)"
echo -e "  MAX_GENERATION_TIME=20s (short for testing)"
echo -e "  RACE_RETRY_ENABLED=true"
echo -e "  RACE_PARALLEL_ON_IDLE=true"

# Export config overrides for testing (these override config file)
export IDLE_TIMEOUT="5s"
export MAX_GENERATION_TIME="20s"
export MAX_REQUEST_TIME="60s"
export RACE_RETRY_ENABLED="true"
export RACE_PARALLEL_ON_IDLE="true"
export RACE_MAX_PARALLEL="3"
export LOOP_DETECTION_ENABLED="false"

go run cmd/main.go &
PROXY_PID=$!

# Wait for proxy to be ready
sleep 3
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
    
    # Use X-LLMProxy-Test-Upstream header to route to mock server
    curl -N -s --max-time "$max_time" "http://localhost:$PROXY_PORT/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $API_KEY" \
        -H "X-LLMProxy-Test-Upstream: http://localhost:$MOCK_PORT" \
        -d "{
            \"model\": \"mock-model\",
            \"messages\": [{\"role\": \"user\", \"content\": \"$prompt\"}],
            \"stream\": true
        }" 2>&1 | head -n 30
    
    end_time=$(date +%s)
    duration=$((end_time - start_time))
    echo -e "\n${YELLOW}Duration: ${duration}s${NC}"
}

# Test 1: Fast complete (baseline - should complete quickly)
echo -e "\n${YELLOW}[3/6] Test 1: Fast Complete (baseline)${NC}"
echo -e "Expected: Completes quickly without spawning parallel requests"
test_streaming "Fast Complete" "mock-fast-complete" 10

# Test 2: Slow start (should complete after initial delay)
echo -e "\n${YELLOW}[4/6] Test 2: Slow Start${NC}"
echo -e "Expected: Waits 5s then completes quickly"
test_streaming "Slow Start" "mock-slow-start" 15

# Test 3: Idle timeout (main scenario - should spawn parallel requests)
echo -e "\n${YELLOW}[5/6] Test 3: Idle Timeout (KEY TEST)${NC}"
echo -e "Expected behavior:"
echo -e "  1. Main request starts streaming initial tokens"
echo -e "  2. After IDLE_TIMEOUT (5s), proxy spawns parallel requests"
echo -e "  3. Check proxy logs for: 'Main request idle, spawning parallel request'"
echo -e "  4. First request to complete wins the race"
echo -e "\n${YELLOW}Starting test (will run for ~10s)...${NC}"
test_streaming "Idle Timeout" "mock-idle-timeout" 12

# Test 4: Streaming deadline (should pick best buffer)
echo -e "\n${YELLOW}[6/6] Test 4: Streaming Deadline (KEY TEST)${NC}"
echo -e "Expected behavior:"
echo -e "  1. Main request streams slowly (10s per token)"
echo -e "  2. After MAX_GENERATION_TIME (20s), proxy picks best buffer"
echo -e "  3. Check proxy logs for: 'Streaming deadline reached, picking best buffer'"
echo -e "  4. Returns partial content accumulated so far"
echo -e "\n${YELLOW}Starting test (will run for ~25s)...${NC}"
test_streaming "Streaming Deadline" "mock-streaming-deadline" 30

# Summary
echo -e "\n${BLUE}======================================${NC}"
echo -e "${BLUE}           Test Summary              ${NC}"
echo -e "${BLUE}======================================${NC}"
echo -e ""
echo -e "${GREEN}Tests completed. Check the proxy logs above for:${NC}"
echo -e ""
echo -e "  ${YELLOW}Idle Timeout Test:${NC}"
echo -e "    Look for: [RACE] Main request idle, spawning parallel request"
echo -e "    Look for: [RACE] Spawning second request"
echo -e ""
echo -e "  ${YELLOW}Streaming Deadline Test:${NC}"
echo -e "    Look for: [RACE] Streaming deadline reached, picking best buffer"
echo -e "    Look for: [RACE] Picked best buffer"
echo -e ""
echo -e "${GREEN}If you see these log messages, the features are working correctly!${NC}"
echo -e ""
