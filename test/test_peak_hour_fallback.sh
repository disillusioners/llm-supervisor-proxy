#!/bin/bash

# Test script for Peak Hour Fallback Bug Investigation
# Tests peak hour behavior with fallback chains
# Maximum runtime: 60 seconds
#
# This test is designed to identify the root cause of peak hour fallback bugs.

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
MOCK_PORT=19001
PROXY_PORT=19002
MOCK_PID=""
PROXY_PID=""
TIMER_PID=""

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
    pkill -f "mock_llm_peak_hour_fallback" 2>/dev/null || true
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
echo -e "${BLUE}  Peak Hour Fallback Bug Investigation  ${NC}"
echo -e "${BLUE}   (Max runtime: ${HARD_TIMEOUT}s)         ${NC}"
echo -e "${BLUE}======================================${NC}"
echo ""
echo -e "${CYAN}Mock Server Endpoints:${NC}"
echo -e "  - mock-peak-upstream -> 500 error"
echo -e "  - mock-fallback-normal -> 200 success"
echo -e "  - mock-fallback-peak-upstream -> 200 success"
echo -e "  - mock-normal-upstream -> 500 error"
echo -e "  - unknown -> 500 error"
echo ""
echo -e "${CYAN}Models Configured:${NC}"
echo -e "  1. test-peak: peak_hour_enabled=true, fallback_chain=[test-fallback-no-peak]"
echo -e "  2. test-fallback-no-peak: NO peak hour"
echo -e "  3. test-fallback-with-peak: peak_hour_enabled=true"
echo -e "  4. test-normal: NO peak hour, fallback_chain=[test-fallback-no-peak]"
echo ""

# Clean ports first
source "$SCRIPT_DIR/test_mock_clean_ports.sh" "$PROXY_PORT" "$MOCK_PORT"
clean_ports "$PROXY_PORT" "$MOCK_PORT"

# Start mock LLM server
echo -e "${YELLOW}[1/5] Starting Mock LLM Peak Hour Fallback Server (port $MOCK_PORT)...${NC}"
cd "$SCRIPT_DIR"
go run mock_llm_peak_hour_fallback.go -port=$MOCK_PORT &
MOCK_PID=$!
cd "$ROOT_DIR"

# Wait for mock server to be ready
sleep 2
if ! kill -0 $MOCK_PID 2>/dev/null; then
    echo -e "${RED}ERROR: Mock server failed to start${NC}"
    exit 1
fi
echo -e "${GREEN}Mock server started (PID: $MOCK_PID)${NC}"

# Start proxy
echo -e "\n${YELLOW}[2/5] Starting Proxy (port $PROXY_PORT)...${NC}"

export APPLY_ENV_OVERRIDES="true"
export UPSTREAM_URL="http://localhost:4001"
export PORT="$PROXY_PORT"
export IDLE_TIMEOUT="2s"
export STREAM_DEADLINE="10s"
export MAX_GENERATION_TIME="30s"
export RACE_RETRY_ENABLED="true"
export RACE_PARALLEL_ON_IDLE="false"
export RACE_MAX_PARALLEL="2"
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

# Configure models via API
echo -e "\n${YELLOW}[3/5] Configuring models via API...${NC}"

# Create credential
echo -e "${CYAN}Creating credential...${NC}"
curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/credentials/test-cred" 2>/dev/null || true
sleep 0.3

CREDENTIAL_RESPONSE=$(curl -s -X POST "http://localhost:$PROXY_PORT/fe/api/credentials" \
    -H "Content-Type: application/json" \
    -d "{
        \"id\": \"test-cred\",
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

# Delete existing models
curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/models/test-peak" 2>/dev/null || true
curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/models/test-fallback-no-peak" 2>/dev/null || true
curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/models/test-fallback-with-peak" 2>/dev/null || true
curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/models/test-normal" 2>/dev/null || true
sleep 0.3

# Create test-fallback-no-peak (NO peak hour)
echo -e "${CYAN}Creating test-fallback-no-peak...${NC}"
FALLBACK_NO_PEAK=$(curl -s -X POST "http://localhost:$PROXY_PORT/fe/api/models" \
    -H "Content-Type: application/json" \
    -d '{
        "id": "test-fallback-no-peak",
        "name": "Test Fallback No Peak",
        "enabled": true,
        "internal": true,
        "credential_id": "test-cred",
        "internal_base_url": "http://localhost:'$MOCK_PORT'/v1",
        "internal_model": "mock-fallback-normal"
    }')
if echo "$FALLBACK_NO_PEAK" | grep -q '"id"'; then
    echo -e "${GREEN}test-fallback-no-peak created${NC}"
else
    echo -e "${RED}Failed: $FALLBACK_NO_PEAK${NC}"
fi

# Create test-fallback-with-peak (WITH peak hour)
echo -e "${CYAN}Creating test-fallback-with-peak...${NC}"
FALLBACK_WITH_PEAK=$(curl -s -X POST "http://localhost:$PROXY_PORT/fe/api/models" \
    -H "Content-Type: application/json" \
    -d '{
        "id": "test-fallback-with-peak",
        "name": "Test Fallback With Peak",
        "enabled": true,
        "internal": true,
        "credential_id": "test-cred",
        "internal_base_url": "http://localhost:'$MOCK_PORT'/v1",
        "internal_model": "mock-fallback-normal-upstream",
        "peak_hour_enabled": true,
        "peak_hour_start": "00:00",
        "peak_hour_end": "23:59",
        "peak_hour_timezone": "+0",
        "peak_hour_model": "mock-fallback-peak-upstream"
    }')
if echo "$FALLBACK_WITH_PEAK" | grep -q '"id"'; then
    echo -e "${GREEN}test-fallback-with-peak created${NC}"
else
    echo -e "${RED}Failed: $FALLBACK_WITH_PEAK${NC}"
fi

# Create test-peak (WITH peak hour, fallback to test-fallback-no-peak)
echo -e "${CYAN}Creating test-peak...${NC}"
TEST_PEAK=$(curl -s -X POST "http://localhost:$PROXY_PORT/fe/api/models" \
    -H "Content-Type: application/json" \
    -d '{
        "id": "test-peak",
        "name": "Test Peak",
        "enabled": true,
        "internal": true,
        "credential_id": "test-cred",
        "internal_base_url": "http://localhost:'$MOCK_PORT'/v1",
        "internal_model": "mock-normal-upstream",
        "fallback_chain": ["test-fallback-no-peak"],
        "peak_hour_enabled": true,
        "peak_hour_start": "00:00",
        "peak_hour_end": "23:59",
        "peak_hour_timezone": "+0",
        "peak_hour_model": "mock-peak-upstream"
    }')
if echo "$TEST_PEAK" | grep -q '"id"'; then
    echo -e "${GREEN}test-peak created${NC}"
else
    echo -e "${RED}Failed: $TEST_PEAK${NC}"
fi

# Create test-normal (NO peak hour, fallback to test-fallback-no-peak)
echo -e "${CYAN}Creating test-normal...${NC}"
TEST_NORMAL=$(curl -s -X POST "http://localhost:$PROXY_PORT/fe/api/models" \
    -H "Content-Type: application/json" \
    -d '{
        "id": "test-normal",
        "name": "Test Normal",
        "enabled": true,
        "internal": true,
        "credential_id": "test-cred",
        "internal_base_url": "http://localhost:'$MOCK_PORT'/v1",
        "internal_model": "mock-normal-upstream",
        "fallback_chain": ["test-fallback-no-peak"]
    }')
if echo "$TEST_NORMAL" | grep -q '"id"'; then
    echo -e "${GREEN}test-normal created${NC}"
else
    echo -e "${RED}Failed: $TEST_NORMAL${NC}"
fi

echo -e "\n${GREEN}Models configured!${NC}"

# Run tests
echo -e "\n${BLUE}======================================${NC}"
echo -e "${BLUE}         Running Tests               ${NC}"
echo -e "${BLUE}======================================${NC}"

# Test A: request test-peak -> peak upstream fails -> should fallback to test-fallback-no-peak -> mock-fallback-normal succeeds
echo -e "\n${YELLOW}[Test A] test-peak with peak hour -> peak fails -> fallback succeeds${NC}"
echo -e "${CYAN}Expected flow:${NC}"
echo -e "  1. Request to test-peak"
echo -e "  2. Peak hour active -> uses mock-peak-upstream"
echo -e "  3. mock-peak-upstream returns 500"
echo -e "  4. Fallback to test-fallback-no-peak"
echo -e "  5. test-fallback-no-peak (no peak) -> uses mock-fallback-normal"
echo -e "  6. mock-fallback-normal returns 200 SUCCESS"
echo -e ""
echo -e "${YELLOW}Response:${NC}"

RESPONSE_A=$(curl -s --max-time 15 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d '{
        "model": "test-peak",
        "messages": [{"role": "user", "content": "Hello"}],
        "stream": false
    }' 2>&1)

echo "$RESPONSE_A" | head -5

if echo "$RESPONSE_A" | grep -q '"choices"'; then
    echo -e "${GREEN}✓ PASS: Received successful response${NC}"
elif echo "$RESPONSE_A" | grep -q '"error"'; then
    echo -e "${RED}✗ FAIL: Received error response${NC}"
    echo -e "${RED}Error details: $RESPONSE_A${NC}"
else
    echo -e "${YELLOW}? UNKNOWN: Could not determine response type${NC}"
fi

# Test B: request test-peak with fallback_chain changed to test-fallback-with-peak
echo -e "\n${YELLOW}[Test B] test-peak with peak fallback -> peak fails -> fallback with peak succeeds${NC}"

# Update test-peak to use test-fallback-with-peak
curl -s -X PUT "http://localhost:$PROXY_PORT/fe/api/models/test-peak" \
    -H "Content-Type: application/json" \
    -d '{
        "id": "test-peak",
        "name": "Test Peak",
        "enabled": true,
        "internal": true,
        "credential_id": "test-cred",
        "internal_base_url": "http://localhost:'$MOCK_PORT'/v1",
        "internal_model": "mock-normal-upstream",
        "fallback_chain": ["test-fallback-with-peak"],
        "peak_hour_enabled": true,
        "peak_hour_start": "00:00",
        "peak_hour_end": "23:59",
        "peak_hour_timezone": "+0",
        "peak_hour_model": "mock-peak-upstream"
    }' > /dev/null

sleep 0.3

echo -e "${CYAN}Expected flow:${NC}"
echo -e "  1. Request to test-peak"
echo -e "  2. Peak hour active -> uses mock-peak-upstream"
echo -e "  3. mock-peak-upstream returns 500"
echo -e "  4. Fallback to test-fallback-with-peak"
echo -e "  5. test-fallback-with-peak HAS peak -> uses mock-fallback-peak-upstream"
echo -e "  6. mock-fallback-peak-upstream returns 200 SUCCESS"
echo -e ""
echo -e "${YELLOW}Response:${NC}"

RESPONSE_B=$(curl -s --max-time 15 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d '{
        "model": "test-peak",
        "messages": [{"role": "user", "content": "Hello"}],
        "stream": false
    }' 2>&1)

echo "$RESPONSE_B" | head -5

if echo "$RESPONSE_B" | grep -q '"choices"'; then
    echo -e "${GREEN}✓ PASS: Received successful response${NC}"
elif echo "$RESPONSE_B" | grep -q '"error"'; then
    echo -e "${RED}✗ FAIL: Received error response${NC}"
    echo -e "${RED}Error details: $RESPONSE_B${NC}"
else
    echo -e "${YELLOW}? UNKNOWN: Could not determine response type${NC}"
fi

# Restore test-peak to original fallback
curl -s -X PUT "http://localhost:$PROXY_PORT/fe/api/models/test-peak" \
    -H "Content-Type: application/json" \
    -d '{
        "id": "test-peak",
        "name": "Test Peak",
        "enabled": true,
        "internal": true,
        "credential_id": "test-cred",
        "internal_base_url": "http://localhost:'$MOCK_PORT'/v1",
        "internal_model": "mock-normal-upstream",
        "fallback_chain": ["test-fallback-no-peak"],
        "peak_hour_enabled": true,
        "peak_hour_start": "00:00",
        "peak_hour_end": "23:59",
        "peak_hour_timezone": "+0",
        "peak_hour_model": "mock-peak-upstream"
    }' > /dev/null

# Test C: Both primary and fallback fail
echo -e "\n${YELLOW}[Test C] Both primary and fallback fail -> error${NC}"
echo -e "${CYAN}Expected flow:${NC}"
echo -e "  1. Request to test-peak"
echo -e "  2. Peak hour active -> uses mock-peak-upstream"
echo -e "  3. mock-peak-upstream returns 500"
echo -e "  4. Fallback to test-fallback-no-peak"
echo -e "  5. test-fallback-no-peak -> uses mock-fallback-normal"
echo -e "  6. But mock-fallback-normal also fails -> returns 500"
echo -e "  7. Error returned"
echo -e ""
echo -e "${YELLOW}Response:${NC}"

# Update mock-fallback-normal to fail temporarily by using unknown model
curl -s -X PUT "http://localhost:$PROXY_PORT/fe/api/models/test-fallback-no-peak" \
    -H "Content-Type: application/json" \
    -d '{
        "id": "test-fallback-no-peak",
        "name": "Test Fallback No Peak",
        "enabled": true,
        "internal": true,
        "credential_id": "test-cred",
        "internal_base_url": "http://localhost:'$MOCK_PORT'/v1",
        "internal_model": "mock-unknown-upstream"
    }' > /dev/null
sleep 0.3

RESPONSE_C=$(curl -s --max-time 15 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d '{
        "model": "test-peak",
        "messages": [{"role": "user", "content": "Hello"}],
        "stream": false
    }' 2>&1)

echo "$RESPONSE_C" | head -5

if echo "$RESPONSE_C" | grep -q '"error"'; then
    echo -e "${GREEN}✓ PASS: Received error response (expected)${NC}"
elif echo "$RESPONSE_C" | grep -q '"choices"'; then
    echo -e "${RED}✗ FAIL: Received success but expected error${NC}"
else
    echo -e "${YELLOW}? UNKNOWN: Could not determine response type${NC}"
fi

# Restore mock-fallback-normal
curl -s -X PUT "http://localhost:$PROXY_PORT/fe/api/models/test-fallback-no-peak" \
    -H "Content-Type: application/json" \
    -d '{
        "id": "test-fallback-no-peak",
        "name": "Test Fallback No Peak",
        "enabled": true,
        "internal": true,
        "credential_id": "test-cred",
        "internal_base_url": "http://localhost:'$MOCK_PORT'/v1",
        "internal_model": "mock-fallback-normal"
    }' > /dev/null

# Test D: Baseline - request test-normal (no peak hour) -> upstream fails -> fallback succeeds
echo -e "\n${YELLOW}[Test D] test-normal (no peak) -> upstream fails -> fallback succeeds${NC}"
echo -e "${CYAN}Expected flow:${NC}"
echo -e "  1. Request to test-normal (no peak hour)"
echo -e "  2. No peak hour -> uses mock-normal-upstream"
echo -e "  3. mock-normal-upstream returns 500"
echo -e "  4. Fallback to test-fallback-no-peak"
echo -e "  5. test-fallback-no-peak (no peak) -> uses mock-fallback-normal"
echo -e "  6. mock-fallback-normal returns 200 SUCCESS"
echo -e ""
echo -e "${YELLOW}Response:${NC}"

RESPONSE_D=$(curl -s --max-time 15 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d '{
        "model": "test-normal",
        "messages": [{"role": "user", "content": "Hello"}],
        "stream": false
    }' 2>&1)

echo "$RESPONSE_D" | head -5

if echo "$RESPONSE_D" | grep -q '"choices"'; then
    echo -e "${GREEN}✓ PASS: Received successful response${NC}"
elif echo "$RESPONSE_D" | grep -q '"error"'; then
    echo -e "${RED}✗ FAIL: Received error response${NC}"
    echo -e "${RED}Error details: $RESPONSE_D${NC}"
else
    echo -e "${YELLOW}? UNKNOWN: Could not determine response type${NC}"
fi

# Summary
echo -e "\n${BLUE}======================================${NC}"
echo -e "${BLUE}         Test Summary              ${NC}"
echo -e "${BLUE}======================================${NC}"
echo ""
echo -e "${GREEN}Tests completed. Check the proxy logs above for [PEAK-DBG] messages.${NC}"
echo ""
echo -e "${CYAN}Key things to look for in logs:${NC}"
echo -e "  ${YELLOW}1. buildModelList: original model, model list, fallback chain${NC}"
echo -e "  ${YELLOW}2. ResolveInternalConfig: peak hour check, internal_model substitution${NC}"
echo -e "  ${YELLOW}3. Race Coordinator: model assignment to main/fallback requests${NC}"
echo -e "  ${YELLOW}4. executeRequest: req.modelID, req.modelType, upstream_model before/after${NC}"
echo ""

# Cancel the hard timeout timer
kill $TIMER_PID 2>/dev/null || true
TIMER_PID=""

echo -e "${GREEN}Test script completed successfully!${NC}"
