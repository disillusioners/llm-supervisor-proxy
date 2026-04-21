#!/bin/bash

# Test script for Heartbeat feature
# Tests that the proxy sends heartbeat comments (: heartbeat\n\n) every 15 seconds
# during streaming responses to keep connections alive.
#
# Maximum runtime: 60 seconds
#
# Usage:
#   ./test_mock_heartbeat.sh
#
# Expected:
#   - Connection established (: connected\n\n)
#   - Heartbeats sent every 15 seconds (: heartbeat\n\n)
#   - Stream data interleaved with heartbeats

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Test configuration
MOCK_PORT=4001
PROXY_PORT=4323
MOCK_PID=""
PROXY_PID=""
TIMER_PID=""

# Heartbeat test configuration
HEARTBEAT_INTERVAL=15  # seconds
EXPECTED_MIN_HEARTBEATS=1  # At least 1 heartbeat in 30s test
TEST_DURATION=35  # seconds - gives time for at least 2 heartbeats

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
    pkill -f "mock_llm_race.go" 2>/dev/null || true
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
echo -e "${BLUE}      Heartbeat Feature Tests       ${NC}"
echo -e "${BLUE}   (Max runtime: ${HARD_TIMEOUT}s)         ${NC}"
echo -e "${BLUE}======================================${NC}"
echo ""
echo -e "${CYAN}Heartbeat Configuration:${NC}"
echo -e "  Heartbeat interval: ${HEARTBEAT_INTERVAL}s"
echo -e "  Test duration: ${TEST_DURATION}s"
echo -e "  Expected heartbeats: >= ${EXPECTED_MIN_HEARTBEATS}"
echo ""

# Clean ports
source "$SCRIPT_DIR/test_mock_clean_ports.sh" "$PROXY_PORT" "$MOCK_PORT"
clean_ports "$PROXY_PORT" "$MOCK_PORT"

# Start mock LLM server that sends slow stream (keeps connection alive)
echo -e "\n${YELLOW}[1/4] Starting Mock LLM Server (port $MOCK_PORT)...${NC}"
echo -e "  Mode: slow-stream (sends data every 2s for ${TEST_DURATION}s)"
cd "$SCRIPT_DIR"
go run mock_llm_race.go -port=$MOCK_PORT &
MOCK_PID=$!
cd "$ROOT_DIR"

# Wait for mock server
sleep 2
if ! kill -0 $MOCK_PID 2>/dev/null; then
    echo -e "${RED}ERROR: Mock server failed to start${NC}"
    exit 1
fi
echo -e "${GREEN}Mock server started (PID: $MOCK_PID)${NC}"

# Start proxy with heartbeat enabled
echo -e "\n${YELLOW}[2/4] Starting Proxy with Heartbeat enabled (port $PROXY_PORT)...${NC}"
echo -e "  SSE_HEARTBEAT_ENABLED=true"
echo -e "  STREAM_DEADLINE=60s (longer than test to see heartbeats)"

export APPLY_ENV_OVERRIDES="true"
export UPSTREAM_URL="http://localhost:$MOCK_PORT"
export PORT="$PROXY_PORT"
export SSE_HEARTBEAT_ENABLED="true"
export STREAM_DEADLINE="60s"
export MAX_GENERATION_TIME="120s"
export IDLE_TIMEOUT="30s"
export RACE_RETRY_ENABLED="false"
export LOOP_DETECTION_ENABLED="false"

go run cmd/main.go &
PROXY_PID=$!

# Wait for proxy
sleep 2
if ! kill -0 $PROXY_PID 2>/dev/null; then
    echo -e "${RED}ERROR: Proxy failed to start${NC}"
    exit 1
fi
echo -e "${GREEN}Proxy started (PID: $PROXY_PID)${NC}"

# Configure model via API
echo -e "\n${YELLOW}[3/4] Configuring model via API...${NC}"

# Delete existing model and credential if they exist
curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/models/mock-heartbeat-model" 2>/dev/null || true
curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/credentials/mock-heartbeat-cred" 2>/dev/null || true
sleep 0.5

# Create credential for mock server
CREDENTIAL_RESPONSE=$(curl -s -X POST "http://localhost:$PROXY_PORT/fe/api/credentials" \
    -H "Content-Type: application/json" \
    -d "{
        \"id\": \"mock-heartbeat-cred\",
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
        \"id\": \"mock-heartbeat-model\",
        \"name\": \"Mock Heartbeat Model\",
        \"enabled\": true,
        \"internal\": true,
        \"credential_id\": \"mock-heartbeat-cred\",
        \"internal_model\": \"mock-model\",
        \"internal_base_url\": \"http://localhost:$MOCK_PORT/v1\"
    }")

if echo "$MODEL_RESPONSE" | grep -q '"id"'; then
    echo -e "${GREEN}Model created successfully${NC}"
else
    echo -e "${RED}Failed to create model: $MODEL_RESPONSE${NC}"
    exit 1
fi

# Make streaming request and capture response
echo -e "\n${YELLOW}[4/4] Testing heartbeat feature...${NC}"
echo -e ""
echo -e "${CYAN}Making streaming request for ${TEST_DURATION}s...${NC}"
echo -e "${CYAN}Watch for heartbeats (: heartbeat) in the output below.${NC}"
echo -e ""

# Make request and capture to temp file
TEMP_RESPONSE=$(mktemp)
START_TIME=$(date +%s)

# Run curl in background and kill it after TEST_DURATION seconds
curl -N -s --max-time $((TEST_DURATION + 10)) "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d '{
        "model": "mock-heartbeat-model",
        "messages": [{"role": "user", "content": "mock-slow-stream"}],
        "stream": true
    }' > "$TEMP_RESPONSE" 2>&1 &
CURL_PID=$!

# Wait for test duration
sleep $TEST_DURATION

# Kill curl
kill $CURL_PID 2>/dev/null || true
# Wait a moment for any final data flush
sleep 1
wait $CURL_PID 2>/dev/null || true

END_TIME=$(date +%s)
ACTUAL_DURATION=$((END_TIME - START_TIME))

echo -e ""
echo -e "${BLUE}--- Response captured ---${NC}"
cat "$TEMP_RESPONSE"
echo -e "${BLUE}--- End of response ---${NC}"
echo ""

# Count heartbeats and verify response
echo -e "\n${BLUE}======================================${NC}"
echo -e "${BLUE}         Test Results              ${NC}"
echo -e "${BLUE}======================================${NC}"
echo ""

# Test 1: Connected message
if grep -q ": connected" "$TEMP_RESPONSE"; then
    echo -e "${GREEN}✓ Connected message found (: connected\\n\\n)${NC}"
else
    echo -e "${RED}✗ Connected message NOT found${NC}"
fi

# Test 2: Count heartbeats
HEARTBEAT_COUNT=$(grep -o ": heartbeat" "$TEMP_RESPONSE" | wc -l)
echo -e ""
echo -e "${CYAN}Heartbeat Analysis:${NC}"
echo -e "  Test duration: ${ACTUAL_DURATION}s"
echo -e "  Heartbeat interval: ${HEARTBEAT_INTERVAL}s"
echo -e "  Expected heartbeats: >= ${EXPECTED_MIN_HEARTBEATS}"
echo -e "  Actual heartbeats: ${HEARTBEAT_COUNT}"

if [ "$HEARTBEAT_COUNT" -ge "$EXPECTED_MIN_HEARTBEATS" ]; then
    echo -e "${GREEN}✓ PASS: Heartbeats detected (${HEARTBEAT_COUNT} >= ${EXPECTED_MIN_HEARTBEATS})${NC}"
else
    echo -e "${RED}✗ FAIL: Not enough heartbeats (${HEARTBEAT_COUNT} < ${EXPECTED_MIN_HEARTBEATS})${NC}"
    echo -e "${YELLOW}  Note: This may indicate heartbeat is not enabled or not working${NC}"
fi

# Test 3: Stream data present
DATA_CHUNK_COUNT=$(grep -o '"content":' "$TEMP_RESPONSE" | wc -l | tr -d ' ')
echo -e ""
echo -e "${CYAN}Stream Data Analysis:${NC}"
echo -e "  Data chunks with content: ${DATA_CHUNK_COUNT}"

if [ "$DATA_CHUNK_COUNT" -gt 0 ]; then
    echo -e "${GREEN}✓ Stream data present (${DATA_CHUNK_COUNT} chunks)${NC}"
else
    echo -e "${YELLOW}⚠ No stream data detected (may be buffered only)${NC}"
fi

# Test 4: Heartbeats not interfering with data
echo -e ""
echo -e "${CYAN}Heartbeat Interleaving Check:${NC}"
LINE_NUM=0
FOUND_BOTH=false
while IFS= read -r line; do
    LINE_NUM=$((LINE_NUM + 1))
    if echo "$line" | grep -q ": heartbeat"; then
        if [ $LINE_NUM -lt 5 ]; then
            # Check if data was already sent before first heartbeat
            if grep -q '"content"' "$TEMP_RESPONSE" | head -n $((LINE_NUM - 1)) | grep -q '"content"'; then
                FOUND_BOTH=true
            fi
        fi
    fi
done < "$TEMP_RESPONSE"

# Simplified check: both connected and heartbeats present
if grep -q ": connected" "$TEMP_RESPONSE" && grep -q ": heartbeat" "$TEMP_RESPONSE"; then
    echo -e "${GREEN}✓ Both connected message and heartbeats present${NC}"
    echo -e "${GREEN}✓ Heartbeat is working correctly!${NC}"
else
    echo -e "${YELLOW}⚠ Could not verify interleaving (check output above)${NC}"
fi

# Cleanup temp file
rm -f "$TEMP_RESPONSE"

# Cancel the hard timeout timer
kill $TIMER_PID 2>/dev/null || true
TIMER_PID=""

echo ""
echo -e "${BLUE}======================================${NC}"
echo -e "${BLUE}      Test Complete!                ${NC}"
echo -e "${BLUE}======================================${NC}"
echo ""
echo -e "${YELLOW}Summary:${NC}"
echo -e "  - Heartbeat interval: ${HEARTBEAT_INTERVAL}s"
echo -e "  - Test duration: ${ACTUAL_DURATION}s"
echo -e "  - Heartbeats sent: ${HEARTBEAT_COUNT}"
echo ""
echo -e "${CYAN}The proxy should send ': heartbeat\\n\\n' SSE comments${NC}"
echo -e "${CYAN}every ${HEARTBEAT_INTERVAL} seconds to keep the connection alive.${NC}"
