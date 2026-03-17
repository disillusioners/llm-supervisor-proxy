#!/bin/bash

# Test script for Ultimate Model functionality - Internal Path
# Tests duplicate detection and ultimate model triggering
# Maximum runtime: 30 seconds
#
# This test uses an "internal" model configuration that bypasses the external
# UPSTREAM_URL and calls the mock server directly via internal_base_url.

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

# Use TEST_API_KEY or fallback
API_KEY="${TEST_API_KEY:-test-key}"

echo -e "${BLUE}======================================${NC}"
echo -e "${BLUE}   Ultimate Model Internal Path Tests ${NC}"
echo -e "${BLUE}   (Max runtime: ${HARD_TIMEOUT}s)         ${NC}"
echo -e "${BLUE}======================================${NC}"

# Clear ports first - kill any processes using our ports
echo -e "\n${YELLOW}[0/5] Cleaning up ports before starting...${NC}"
lsof -ti :$MOCK_PORT | xargs kill -9 2>/dev/null || true
lsof -ti :$PROXY_PORT | xargs kill -9 2>/dev/null || true
pkill -f "mock_llm" 2>/dev/null || true
pkill -f "cmd/main.go" 2>/dev/null || true
sleep 2

# Double-check ports are free
if lsof -i :$MOCK_PORT >/dev/null 2>&1; then
    echo -e "${RED}ERROR: Port $MOCK_PORT is still in use${NC}"
    lsof -i :$MOCK_PORT
    exit 1
fi
if lsof -i :$PROXY_PORT >/dev/null 2>&1; then
    echo -e "${RED}ERROR: Port $PROXY_PORT is still in use${NC}"
    lsof -i :$PROXY_PORT
    exit 1
fi
echo -e "${GREEN}Ports are free${NC}"

# Start mock LLM server (use mock_llm_race.go which supports -port flag)
echo -e "\n${YELLOW}[1/5] Starting Mock LLM Server (port $MOCK_PORT)...${NC}"
cd "$SCRIPT_DIR"
go run mock_llm_race.go -port=$MOCK_PORT -idle-pause=0 -deadline-interval=0 -slow-start=0 &
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
echo -e "\n${YELLOW}[2/5] Starting Proxy with Ultimate Model enabled (port $PROXY_PORT)...${NC}"
echo -e "  ULTIMATE_MODEL_ID=mock-ultimate-model (internal model)"
echo -e "  ULTIMATE_MODEL_MAX_HASH=100"
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
echo -e "\n${YELLOW}[3/5] Configuring internal mock models via API...${NC}"

# First, delete existing models/credentials from previous runs (ignore errors)
curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/models/mock-internal-model" 2>/dev/null || true
curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/models/mock-ultimate-model" 2>/dev/null || true
curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/credentials/mock-ultimate-cred" 2>/dev/null || true
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

# Function to make a streaming request
test_streaming() {
    local test_name="$1"
    local prompt="$2"
    local model="$3"
    local max_time="$4"
    
    echo -e "\n${BLUE}=== Test: $test_name ===${NC}"
    echo -e "Model: $model"
    echo -e "Prompt: $prompt"
    echo -e "Max time: ${max_time}s"
    echo -e "Response:"
    
    start_time=$(date +%s)
    
    curl -N -s --max-time "$max_time" "http://localhost:$PROXY_PORT/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $API_KEY" \
        -d "{
            \"model\": \"$model\",
            \"messages\": [{\"role\": \"user\", \"content\": \"$prompt\"}],
            \"stream\": true
        }" 2>&1 | head -n 15
    
    end_time=$(date +%s)
    duration=$((end_time - start_time))
    echo -e "\n${YELLOW}Duration: ${duration}s${NC}"
}

# Test 1: First request (normal - should use regular model)
echo -e "\n${YELLOW}[4/5] Test 1: First Request (Normal)${NC}"
echo -e "Expected: Uses mock-internal-model, normal response"
test_streaming "First Request" "Hello, this is a test message" "mock-internal-model" 5

# Test 2: Second identical request (should trigger ultimate model)
echo -e "\n${YELLOW}[5/5] Test 2: Duplicate Request (Ultimate Model Triggered)${NC}"
echo -e "Expected: Duplicate detected, uses mock-ultimate-model"
echo -e "${YELLOW}Watch proxy logs for: [UltimateModel] Triggered for duplicate request${NC}"
test_streaming "Duplicate Request" "Hello, this is a test message" "mock-internal-model" 5

# Summary
echo -e "\n${BLUE}======================================${NC}"
echo -e "${BLUE}           Test Summary              ${NC}"
echo -e "${BLUE}======================================${NC}"
echo -e ""
echo -e "${GREEN}Tests completed. Check the proxy logs above for:${NC}"
echo -e ""
echo -e "  ${YELLOW}Ultimate Model Verification:${NC}"
echo -e "    First request: Should show normal processing"
echo -e "    Second request: Should show:"
echo -e "      ${GREEN}[UltimateModel] Triggered for duplicate request, using mock-ultimate-model, hash=...${NC}"
echo -e ""
echo -e "  ${YELLOW}What happened:${NC}"
echo -e "    1. First request: Normal flow, hash stored in cache"
echo -e "    2. Second request: Same message content -> hash match -> Ultimate model triggered"
echo -e "    3. Ultimate model bypasses race retry, loop detection, and fallback"
echo -e ""
echo -e "${GREEN}If you see the UltimateModel log message, the feature is working correctly!${NC}"
echo -e ""

# Cancel the hard timeout timer
kill $TIMER_PID 2>/dev/null || true
TIMER_PID=""
