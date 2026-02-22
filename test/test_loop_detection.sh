#!/bin/bash
# test_loop_detection.sh — Integration test for loop detection via mock_llm_loop.go
#
# This script:
#   1. Starts mock_llm_loop.go on port 4002
#   2. Sends requests through the proxy (which must be running with UPSTREAM_URL=http://localhost:4002)
#   3. Displays responses and checks proxy logs for [LOOP-DETECTION] entries
#
# Pre-requisites:
#   - The proxy must be running: UPSTREAM_URL=http://localhost:4002 go run cmd/main.go
#   - OR: modify config.json to set upstream_url to http://localhost:4002
#
# Usage:
#   cd test && bash test_loop_detection.sh

set -e

MOCK_PORT=4002
PROXY_PORT=4321 # default proxy port

echo "════════════════════════════════════════════════════════════════"
echo "  Loop Detection Integration Test"
echo "════════════════════════════════════════════════════════════════"

# Start mock LLM
# echo -e "\n▶ Starting mock_llm_loop on port $MOCK_PORT..."
# go run mock_llm_loop.go &
# MOCK_PID=$!
# sleep 2

# Make sure mock is running
# if ! kill -0 $MOCK_PID 2>/dev/null; then
#     echo "❌ Failed to start mock_llm_loop"
#     exit 1
# fi
echo "✔ Mock LLM running (already started by user)"

# Cleanup on exit
# cleanup() {
#     echo -e "\n▶ Cleaning up..."
#     kill $MOCK_PID 2>/dev/null || true
#     wait $MOCK_PID 2>/dev/null || true
#     echo "✔ Mock LLM stopped"
# }
# trap cleanup EXIT

send_request() {
    local label="$1"
    local keyword="$2"
    echo -e "\n────────────────────────────────────────────────────────────────"
    echo "  TEST: $label"
    echo "  Prompt keyword: \"$keyword\""
    echo "────────────────────────────────────────────────────────────────"
    
    curl -sN --max-time 15 -X POST "http://localhost:$PROXY_PORT/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -d "{
            \"model\": \"mock-model\",
            \"messages\": [{\"role\": \"user\", \"content\": \"$keyword\"}],
            \"stream\": true
        }" 2>/dev/null
    
    echo ""
}

# ───────────────────────────────────────────────────────────────────
# Test: Normal (baseline — should NOT trigger detection)
# ───────────────────────────────────────────────────────────────────
send_request "Normal Response (baseline)" "Hello, how are you?"

# ───────────────────────────────────────────────────────────────────
# Test: Exact loop
# Send the same request multiple times — each response is identical,
# so the detector should see identical messages in its window.
# Note: In shadow-mode Phase 1, the detection happens WITHIN a single
# streaming response. For multi-turn detection across requests, the
# detector would need to persist state (Phase 2).
# ───────────────────────────────────────────────────────────────────
send_request "Exact Loop (request 1 of 3)" "loop-exact"
send_request "Exact Loop (request 2 of 3)" "loop-exact"
send_request "Exact Loop (request 3 of 3)" "loop-exact"

# ───────────────────────────────────────────────────────────────────
# Test: Similarity loop
# ───────────────────────────────────────────────────────────────────
send_request "Similarity Loop (request 1 of 3)" "loop-similar"
send_request "Similarity Loop (request 2 of 3)" "loop-similar"
send_request "Similarity Loop (request 3 of 3)" "loop-similar"

# ───────────────────────────────────────────────────────────────────
# Test: Action repeat loop
# ───────────────────────────────────────────────────────────────────
send_request "Action Repeat Loop (request 1 of 3)" "loop-action"
send_request "Action Repeat Loop (request 2 of 3)" "loop-action"
send_request "Action Repeat Loop (request 3 of 3)" "loop-action"

# ───────────────────────────────────────────────────────────────────
# Test: Thinking loop
# ───────────────────────────────────────────────────────────────────
send_request "Thinking Loop" "loop-thinking"

echo -e "\n════════════════════════════════════════════════════════════════"
echo "  Tests complete!"
echo ""
echo "  Check the PROXY logs for lines containing:"
echo "    [LOOP-DETECTION][SHADOW]"
echo ""
echo "  These indicate where the detector would have flagged a loop."
echo "  In shadow mode, no requests are interrupted."
echo "════════════════════════════════════════════════════════════════"
