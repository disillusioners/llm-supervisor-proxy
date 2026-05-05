#!/bin/bash
# =============================================================================
# E2E Test: reasoning_content Forwarding
# =============================================================================
# Tests that the proxy correctly forwards reasoning_content from DeepSeek API
# responses in multi-turn conversations. DeepSeek requires reasoning_content to
# be passed back in follow-up requests when using thinking mode.
#
# Usage:
#   AUTH_TOKEN=<token> ./test_reasoning_content.sh
#   AUTH_TOKEN=<token> PROXY_URL=http://localhost:8080 MODEL=deepseek ./test_reasoning_content.sh
#   ./test_reasoning_content.sh --help
#
# Environment Variables:
#   PROXY_URL    - Proxy URL (default: http://localhost:8080)
#   AUTH_TOKEN   - Required: Authentication token for the proxy
#   MODEL        - Model ID (default: deepseek)
# =============================================================================

set -euo pipefail

# ─────────────────────────────────────────────────────────────────────────────
# Configuration
# ─────────────────────────────────────────────────────────────────────────────

PROXY_URL="${PROXY_URL:-http://localhost:8080}"
AUTH_TOKEN="${AUTH_TOKEN:-}"
MODEL="${MODEL:-deepseek}"

# ─────────────────────────────────────────────────────────────────────────────
# Colors & Output Formatting
# ─────────────────────────────────────────────────────────────────────────────

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

# Print colored output
print_header() { echo -e "${BOLD}${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"; }
print_subheader() { echo -e "${BOLD}${CYAN}── $1${NC}"; }
print_info() { echo -e "${YELLOW}ℹ${NC} $1"; }
print_pass() { echo -e "${GREEN}✓ PASS${NC}: $1"; }
print_fail() { echo -e "${RED}✗ FAIL${NC}: $1"; }
print_error() { echo -e "${RED}ERROR${NC}: $1" >&2; }

# ─────────────────────────────────────────────────────────────────────────────
# Test Results Tracking
# ─────────────────────────────────────────────────────────────────────────────

declare -a TEST_NAMES=()
declare -a TEST_RESULTS=()
declare -a TEST_DETAILS=()
TESTS_PASSED=0
TESTS_FAILED=0

# Record a test result
# Usage: record_result "Test Name" "PASS|FAIL" "Details"
record_result() {
    TEST_NAMES+=("$1")
    TEST_RESULTS+=("$2")
    TEST_DETAILS+=("$3")
    if [[ "$2" == "PASS" ]]; then
        ((TESTS_PASSED++))
    else
        ((TESTS_FAILED++))
    fi
}

# ─────────────────────────────────────────────────────────────────────────────
# Helper Functions
# ─────────────────────────────────────────────────────────────────────────────

# Temporary files for cleanup
declare -a TEMP_FILES=()

# Trap for cleanup
cleanup() {
    for f in "${TEMP_FILES[@]:-}"; do
        rm -f "$f" 2>/dev/null || true
    done
}
trap cleanup EXIT

# Show help message
show_help() {
    cat << EOF
${BOLD}E2E Test: reasoning_content Forwarding${NC}

Tests that the proxy correctly forwards reasoning_content in multi-turn
conversations with DeepSeek API.

${BOLD}Usage:${NC}
    AUTH_TOKEN=<token> $0 [options]

${BOLD}Environment Variables:${NC}
    PROXY_URL    Proxy URL (default: http://localhost:8080)
    AUTH_TOKEN   Required: Authentication token for the proxy
    MODEL        Model ID (default: deepseek)

${BOLD}Options:${NC}
    -h, --help    Show this help message

${BOLD}Test Cases:${NC}
    1. Turn 1 non-streaming - Verify reasoning_content exists in response
    2. Turn 2 WITH reasoning_content - Verify multi-turn works correctly
    3. Turn 2 WITHOUT reasoning_content - Negative test (should fail)
    4a. Turn 1 streaming - Verify SSE streaming works correctly
    4b. Turn 2 streaming WITH reasoning_content - Verify streaming multi-turn works
    4c. Turn 2 streaming WITHOUT reasoning_content - Streaming negative test (should fail)

${BOLD}Example:${NC}
    AUTH_TOKEN=my-token ./test_reasoning_content.sh
    AUTH_TOKEN=my-token PROXY_URL=http://localhost:4321 MODEL=deepseek $0

EOF
    exit 0
}

# Check dependencies
check_dependencies() {
    if ! command -v curl &> /dev/null; then
        print_error "curl is required but not installed"
        exit 1
    fi

    if ! command -v jq &> /dev/null; then
        print_error "jq is required but not installed"
        print_info "Install jq: brew install jq (macOS) or apt install jq (Ubuntu)"
        exit 1
    fi
}

# Validate required configuration
validate_config() {
    if [[ -z "$AUTH_TOKEN" ]]; then
        print_error "AUTH_TOKEN is required"
        print_info "Set AUTH_TOKEN environment variable or pass it directly:"
        print_info "  AUTH_TOKEN=<token> $0"
        exit 1
    fi
}

# Make API request and capture response
# Usage: make_request <json_payload> [optional: --stream]
# Returns: Sets RESPONSE and HTTP_CODE variables
RESPONSE=""
HTTP_CODE=""

make_request() {
    local payload="$1"
    local stream_flag="${2:-}"
    local curl_args=(-s -w '\n%{http_code}')

    if [[ "$stream_flag" == "--stream" ]]; then
        curl_args=(-N -s -w '\n%{http_code}')
    fi

    local full_response
    full_response=$(curl "${curl_args[@]}" \
        -X POST "$PROXY_URL/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $AUTH_TOKEN" \
        -d "$payload" 2>&1)

    # Check if curl itself failed (network error, etc.)
    if [[ $? -ne 0 ]] || [[ "$full_response" == "" ]]; then
        HTTP_CODE="000"
        RESPONSE="{\"error\": {\"message\": \"curl failed: $full_response\"}}"
        return 1
    fi

    # Split response into body and HTTP code
    HTTP_CODE=$(echo "$full_response" | tail -n 1)
    RESPONSE=$(echo "$full_response" | sed '$d')
}

# Extract reasoning_content from non-streaming response
# Usage: extract_reasoning_content <json_response>
extract_reasoning_content() {
    local resp="$1"
    echo "$resp" | jq -r '.choices[0].message.reasoning_content // empty'
}

# Extract content from non-streaming response
extract_content() {
    local resp="$1"
    echo "$resp" | jq -r '.choices[0].message.content // empty'
}

# Check if response contains error about reasoning_content
has_reasoning_content_error() {
    local resp="$1"
    local error_msg
    error_msg=$(echo "$resp" | jq -r '.error.message // .message // empty')
    echo "$error_msg" | grep -qi "reasoning_content" && return 0 || return 1
}

# Build a conversation with 3 messages (user, assistant, new_user)
# Usage: build_conversation <user_msg> <assistant_msg> <new_user_msg>
build_conversation() {
    local user_msg="$1"
    local assistant_msg="$2"
    local new_user_msg="$3"

    # Escape content for JSON
    local escaped_user=$(echo "$user_msg" | jq -Rs .)
    local escaped_assistant=$(echo "$assistant_msg" | jq -Rs .)
    local escaped_new_user=$(echo "$new_user_msg" | jq -Rs .)

    # Remove surrounding quotes from jq -Rs output
    escaped_user="${escaped_user:1:-1}"
    escaped_assistant="${escaped_assistant:1:-1}"
    escaped_new_user="${escaped_new_user:1:-1}"

    cat << EOF
[
  {"role": "user", "content": $escaped_user},
  {"role": "assistant", "content": $escaped_assistant},
  {"role": "user", "content": $escaped_new_user}
]
EOF
}

# Build a 3-message conversation with reasoning_content in assistant message
# Usage: build_conversation_with_reasoning <user_msg> <assistant_content> <reasoning_content> <new_user_msg>
build_conversation_with_reasoning() {
    local user_msg="$1"
    local assistant_content="$2"
    local reasoning_content="$3"
    local new_user_msg="$4"

    # Escape all content for JSON
    local escaped_user=$(echo "$user_msg" | jq -Rs .)
    local escaped_content=$(echo "$assistant_content" | jq -Rs .)
    local escaped_reasoning=$(echo "$reasoning_content" | jq -Rs .)
    local escaped_new_user=$(echo "$new_user_msg" | jq -Rs .)

    # Remove surrounding quotes from jq -Rs output
    escaped_user="${escaped_user:1:-1}"
    escaped_content="${escaped_content:1:-1}"
    escaped_reasoning="${escaped_reasoning:1:-1}"
    escaped_new_user="${escaped_new_user:1:-1}"

    cat << EOF
[
  {"role": "user", "content": $escaped_user},
  {"role": "assistant", "content": $escaped_content, "reasoning_content": $escaped_reasoning},
  {"role": "user", "content": $escaped_new_user}
]
EOF
}

# Build a 3-message conversation WITHOUT reasoning_content (for negative tests)
# Usage: build_conversation_without_reasoning <user_msg> <assistant_content> <new_user_msg>
build_conversation_without_reasoning() {
    local user_msg="$1"
    local assistant_content="$2"
    local new_user_msg="$3"

    # Escape all content for JSON
    local escaped_user=$(echo "$user_msg" | jq -Rs .)
    local escaped_content=$(echo "$assistant_content" | jq -Rs .)
    local escaped_new_user=$(echo "$new_user_msg" | jq -Rs .)

    # Remove surrounding quotes from jq -Rs output
    escaped_user="${escaped_user:1:-1}"
    escaped_content="${escaped_content:1:-1}"
    escaped_new_user="${escaped_new_user:1:-1}"

    cat << EOF
[
  {"role": "user", "content": $escaped_user},
  {"role": "assistant", "content": $escaped_content},
  {"role": "user", "content": $escaped_new_user}
]
EOF
}

# Build assistant message JSON with reasoning_content
# Usage: build_assistant_with_reasoning <content> <reasoning_content>
build_assistant_with_reasoning() {
    local content="$1"
    local reasoning="$2"

    local escaped_content=$(echo "$content" | jq -Rs .)
    local escaped_reasoning=$(echo "$reasoning" | jq -Rs .)

    # Remove surrounding quotes
    escaped_content="${escaped_content:1:-1}"
    escaped_reasoning="${escaped_reasoning:1:-1}"

    cat << EOF
{
  "role": "assistant",
  "content": $escaped_content,
  "reasoning_content": $escaped_reasoning
}
EOF
}

# Build assistant message JSON without reasoning_content (for negative test)
# Usage: build_assistant_without_reasoning <content>
build_assistant_without_reasoning() {
    local content="$1"

    local escaped_content=$(echo "$content" | jq -Rs .)
    escaped_content="${escaped_content:1:-1}"

    cat << EOF
{
  "role": "assistant",
  "content": $escaped_content
}
EOF
}

# Make streaming API request and capture SSE response
# Usage: make_streaming_request <json_payload>
# Returns: Sets RESPONSE and HTTP_CODE variables
make_streaming_request() {
    local payload="$1"
    local curl_args=(-N -s -w '\n%{http_code}')

    local full_response
    full_response=$(curl "${curl_args[@]}" \
        -X POST "$PROXY_URL/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $AUTH_TOKEN" \
        -d "$payload" 2>&1)

    # Check if curl itself failed (network error, etc.)
    if [[ $? -ne 0 ]] || [[ "$full_response" == "" ]]; then
        HTTP_CODE="000"
        RESPONSE="{\"error\": {\"message\": \"curl failed: $full_response\"}}"
        return 1
    fi

    # Split response into body and HTTP code
    HTTP_CODE=$(echo "$full_response" | tail -n 1)
    RESPONSE=$(echo "$full_response" | sed '$d')
}

# Print truncated content for display
# Usage: print_truncated <content> <max_length>
print_truncated() {
    local content="$1"
    local max_len="${2:-200}"

    if [[ ${#content} -gt $max_len ]]; then
        echo "${content:0:$max_len}...[truncated, ${#content} total chars]"
    else
        echo "$content"
    fi
}

# ─────────────────────────────────────────────────────────────────────────────
# Test Cases
# ─────────────────────────────────────────────────────────────────────────────

# Test 1: Turn 1 Non-Streaming
# Verifies that reasoning_content exists in the first response
test_turn1_nonstreaming() {
    print_subheader "Test 1: Turn 1 Non-Streaming"

    local payload
    payload=$(jq -n \
        --arg model "$MODEL" \
        '{
            model: $model,
            max_tokens: 500,
            messages: [{"role": "user", "content": "What is 2+2? Please show your reasoning."}],
            stream: false
        }')

    print_info "Sending request to $PROXY_URL/v1/chat/completions"
    make_request "$payload"

    print_info "HTTP Status: $HTTP_CODE"

    # Check HTTP status
    if [[ "$HTTP_CODE" != "200" ]]; then
        print_info "Response: $RESPONSE"
        record_result "Turn 1 Non-Streaming" "FAIL" "HTTP $HTTP_CODE != 200"
        return 1
    fi

    # Extract reasoning_content
    local reasoning_content
    reasoning_content=$(extract_reasoning_content "$RESPONSE")

    if [[ -z "$reasoning_content" ]]; then
        print_info "Response: $RESPONSE"
        record_result "Turn 1 Non-Streaming" "FAIL" "reasoning_content not found in response"
        return 1
    fi

    # Print the reasoning_content
    print_info "reasoning_content found (${#reasoning_content} chars):"
    echo "  $(print_truncated "$reasoning_content" 300)"

    # Store for use in Turn 2
    TURN1_REASONING="$reasoning_content"
    TURN1_CONTENT="$(extract_content "$RESPONSE")"

    print_pass "Turn 1: reasoning_content present"
    record_result "Turn 1 Non-Streaming" "PASS" "reasoning_content: ${#reasoning_content} chars"
    return 0
}

# Variables to store Turn 1 data for Turn 2 tests
TURN1_REASONING=""
TURN1_CONTENT=""

# Test 2: Turn 2 WITH reasoning_content
# Verifies that passing reasoning_content back works correctly
test_turn2_with_reasoning() {
    print_subheader "Test 2: Turn 2 WITH reasoning_content"

    if [[ -z "$TURN1_REASONING" ]]; then
        print_info "Skipping: Turn 1 data not available (run Test 1 first)"
        record_result "Turn 2 WITH reasoning_content" "FAIL" "Skipped: Turn 1 data missing"
        return 1
    fi

    # Build 3-message conversation: original user, assistant (with reasoning), new user question
    local messages_json
    messages_json=$(build_conversation_with_reasoning \
        "What is 2+2? Please show your reasoning." \
        "$TURN1_CONTENT" \
        "$TURN1_REASONING" \
        "Why is that correct?")

    local payload
    payload=$(jq -n \
        --arg model "$MODEL" \
        --argjson messages "$messages_json" \
        '{
            model: $model,
            max_tokens: 500,
            messages: $messages,
            stream: false
        }')

    print_info "Sending multi-turn request with reasoning_content"

    # Create a temporary file for the payload to handle complex JSON
    local temp_payload
    temp_payload=$(mktemp)
    TEMP_FILES+=("$temp_payload")
    echo "$payload" > "$temp_payload"

    make_request "$(cat "$temp_payload")"

    print_info "HTTP Status: $HTTP_CODE"

    # Check HTTP status FIRST
    if [[ "$HTTP_CODE" != "200" ]]; then
        print_info "Response: $RESPONSE"
        record_result "Turn 2 WITH reasoning_content" "FAIL" "HTTP $HTTP_CODE != 200"
        return 1
    fi

    # Check for reasoning_content error
    if has_reasoning_content_error "$RESPONSE"; then
        print_info "Response: $RESPONSE"
        print_fail "Turn 2: Got reasoning_content error (bug detected!)"
        record_result "Turn 2 WITH reasoning_content" "FAIL" "reasoning_content error: $RESPONSE"
        return 1
    fi

    # Extract and verify reasoning_content in response
    local reasoning_content
    reasoning_content=$(extract_reasoning_content "$RESPONSE")

    if [[ -z "$reasoning_content" ]]; then
        print_info "Response: $RESPONSE"
        record_result "Turn 2 WITH reasoning_content" "FAIL" "No reasoning_content in Turn 2 response"
        return 1
    fi

    print_info "Turn 2 reasoning_content: $(print_truncated "$reasoning_content" 200)"
    print_pass "Turn 2 WITH reasoning_content: Success"
    record_result "Turn 2 WITH reasoning_content" "PASS" "Multi-turn with reasoning_content works"

    # Store for streaming test
    TURN2_REASONING="$reasoning_content"
    TURN2_CONTENT="$(extract_content "$RESPONSE")"
    return 0
}

TURN2_REASONING=""
TURN2_CONTENT=""

# Test 3: Turn 2 WITHOUT reasoning_content (Negative Test)
# Verifies that omitting reasoning_content causes an error (proves bug detection works)
test_turn2_without_reasoning() {
    print_subheader "Test 3: Turn 2 WITHOUT reasoning_content (Negative Test)"

    if [[ -z "$TURN1_CONTENT" ]]; then
        print_info "Skipping: Turn 1 data not available (run Test 1 first)"
        record_result "Turn 2 WITHOUT reasoning_content" "FAIL" "Skipped: Turn 1 data missing"
        return 1
    fi

    # Build 3-message conversation WITHOUT reasoning_content
    local messages_json
    messages_json=$(build_conversation_without_reasoning \
        "What is 2+2? Please show your reasoning." \
        "$TURN1_CONTENT" \
        "Why is that correct?")

    local payload
    payload=$(jq -n \
        --arg model "$MODEL" \
        --argjson messages "$messages_json" \
        '{
            model: $model,
            max_tokens: 500,
            messages: $messages,
            stream: false
        }')

    print_info "Sending request WITHOUT reasoning_content (expecting error)"

    # Create a temporary file for the payload
    local temp_payload
    temp_payload=$(mktemp)
    TEMP_FILES+=("$temp_payload")
    echo "$payload" > "$temp_payload"

    make_request "$(cat "$temp_payload")"

    print_info "HTTP Status: $HTTP_CODE"

    # This test PASSES if we get the expected error
    if has_reasoning_content_error "$RESPONSE"; then
        local error_msg
        error_msg=$(echo "$RESPONSE" | jq -r '.error.message // .message // empty')
        print_info "Expected error received: $(print_truncated "$error_msg" 200)"
        print_pass "Turn 2 WITHOUT reasoning_content: Correctly rejected (API validation working)"
        record_result "Turn 2 WITHOUT reasoning_content" "PASS" "Expected error received"
        return 0
    fi

    # If we didn't get an error, the test FAILED (proxy should have rejected this request)
    print_info "Response: $RESPONSE"
    print_fail "Turn 2: Did NOT get expected reasoning_content error"
    record_result "Turn 2 WITHOUT reasoning_content" "FAIL" "Expected error but got HTTP $HTTP_CODE"
    return 1
}

# Test 4a: Turn 1 Streaming
test_turn1_streaming() {
    print_subheader "Test 4a: Turn 1 Streaming"

    local payload
    payload=$(jq -n \
        --arg model "$MODEL" \
        '{
            model: $model,
            max_tokens: 500,
            messages: [{"role": "user", "content": "What is 3+3? Show your reasoning."}],
            stream: true
        }')

    print_info "Sending streaming request"

    make_streaming_request "$payload"

    print_info "HTTP Status: $HTTP_CODE"

    # Check HTTP status FIRST
    if [[ "$HTTP_CODE" != "200" ]]; then
        # Try to parse error from SSE
        local error_msg
        error_msg=$(echo "$RESPONSE" | grep "^data: " | head -1 | sed 's/^data: //' | jq -r '.error.message // .message // empty')
        print_info "Error: ${error_msg:-$RESPONSE}"
        record_result "Turn 1 Streaming" "FAIL" "HTTP $HTTP_CODE: $error_msg"
        return 1
    fi

    # Extract reasoning_content from SSE chunks using grep/sed/jq
    local reasoning_content=""
    local content=""

    while IFS= read -r data; do
        # Skip [DONE] message
        [[ "$data" == "[DONE]" ]] && continue

        # Extract from delta
        local delta_reasoning
        delta_reasoning=$(echo "$data" | jq -r '.choices[0].delta.reasoning_content // empty')
        local delta_content
        delta_content=$(echo "$data" | jq -r '.choices[0].delta.content // empty')

        if [[ -n "$delta_reasoning" ]]; then
            reasoning_content+="$delta_reasoning"
        fi
        if [[ -n "$delta_content" ]]; then
            content+="$delta_content"
        fi
    done < <(echo "$RESPONSE" | grep "^data: " | sed 's/^data: //')

    if [[ -z "$reasoning_content" ]]; then
        print_info "SSE chunks received:"
        echo "$RESPONSE" | head -20
        record_result "Turn 1 Streaming" "FAIL" "No reasoning_content in stream"
        return 1
    fi

    print_info "reasoning_content in stream (${#reasoning_content} chars):"
    echo "  $(print_truncated "$reasoning_content" 300)"

    # Store for Turn 2 streaming test
    STREAM_TURN1_REASONING="$reasoning_content"
    STREAM_TURN1_CONTENT="$content"

    print_pass "Turn 1 Streaming: reasoning_content found in SSE"
    record_result "Turn 1 Streaming" "PASS" "Stream has reasoning_content: ${#reasoning_content} chars"
    return 0
}

STREAM_TURN1_REASONING=""
STREAM_TURN1_CONTENT=""

# Test 4b: Turn 2 Streaming WITH reasoning_content
test_turn2_streaming_with_reasoning() {
    print_subheader "Test 4b: Turn 2 Streaming WITH reasoning_content"

    if [[ -z "$STREAM_TURN1_REASONING" ]]; then
        print_info "Skipping: Turn 1 streaming data not available"
        record_result "Turn 2 Streaming WITH reasoning_content" "FAIL" "Skipped: Turn 1 data missing"
        return 1
    fi

    # Build 3-message conversation with reasoning_content
    local messages_json
    messages_json=$(build_conversation_with_reasoning \
        "What is 3+3? Show your reasoning." \
        "$STREAM_TURN1_CONTENT" \
        "$STREAM_TURN1_REASONING" \
        "Can you explain that differently?")

    local payload
    payload=$(jq -n \
        --arg model "$MODEL" \
        --argjson messages "$messages_json" \
        '{
            model: $model,
            max_tokens: 500,
            messages: $messages,
            stream: true
        }')

    print_info "Sending streaming multi-turn request with reasoning_content"

    make_streaming_request "$payload"

    print_info "HTTP Status: $HTTP_CODE"

    # Check HTTP status FIRST
    if [[ "$HTTP_CODE" != "200" ]]; then
        # Try to parse error from SSE
        local error_msg
        error_msg=$(echo "$RESPONSE" | grep "^data: " | head -1 | sed 's/^data: //' | jq -r '.error.message // .message // empty')
        if [[ -z "$error_msg" ]]; then
            error_msg="$RESPONSE"
        fi
        print_info "Error: $(print_truncated "$error_msg" 200)"
        record_result "Turn 2 Streaming WITH reasoning_content" "FAIL" "HTTP $HTTP_CODE: $error_msg"
        return 1
    fi

    # Extract reasoning_content from SSE using grep/sed/jq
    local reasoning_content=""

    while IFS= read -r data; do
        # Skip [DONE] message
        [[ "$data" == "[DONE]" ]] && continue

        # Check for error in chunk
        local error_msg
        error_msg=$(echo "$data" | jq -r '.error.message // empty')
        if [[ -n "$error_msg" ]]; then
            print_info "Error in chunk: $error_msg"
            record_result "Turn 2 Streaming WITH reasoning_content" "FAIL" "Error in stream: $error_msg"
            return 1
        fi

        local delta_reasoning
        delta_reasoning=$(echo "$data" | jq -r '.choices[0].delta.reasoning_content // empty')

        if [[ -n "$delta_reasoning" ]]; then
            reasoning_content+="$delta_reasoning"
        fi
    done < <(echo "$RESPONSE" | grep "^data: " | sed 's/^data: //')

    if [[ -z "$reasoning_content" ]]; then
        print_info "No reasoning_content in Turn 2 stream"
        record_result "Turn 2 Streaming WITH reasoning_content" "FAIL" "No reasoning_content in Turn 2 stream"
        return 1
    fi

    print_info "Turn 2 stream reasoning_content (${#reasoning_content} chars):"
    echo "  $(print_truncated "$reasoning_content" 200)"

    print_pass "Turn 2 Streaming WITH reasoning_content: Success"
    record_result "Turn 2 Streaming WITH reasoning_content" "PASS" "Stream multi-turn works"
    return 0
}

# Test 4c: Turn 2 Streaming WITHOUT reasoning_content (Negative Test)
# Verifies that omitting reasoning_content in streaming mode causes an error
test_turn2_streaming_without_reasoning() {
    print_subheader "Test 4c: Turn 2 Streaming WITHOUT reasoning_content (Negative Test)"

    if [[ -z "$STREAM_TURN1_CONTENT" ]]; then
        print_info "Skipping: Turn 1 streaming data not available"
        record_result "Turn 2 Streaming WITHOUT reasoning_content" "FAIL" "Skipped: Turn 1 data missing"
        return 1
    fi

    # Build 3-message conversation WITHOUT reasoning_content
    local messages_json
    messages_json=$(build_conversation_without_reasoning \
        "What is 3+3? Show your reasoning." \
        "$STREAM_TURN1_CONTENT" \
        "Can you explain that differently?")

    local payload
    payload=$(jq -n \
        --arg model "$MODEL" \
        --argjson messages "$messages_json" \
        '{
            model: $model,
            max_tokens: 500,
            messages: $messages,
            stream: true
        }')

    print_info "Sending streaming request WITHOUT reasoning_content (expecting error)"

    make_streaming_request "$payload"

    print_info "HTTP Status: $HTTP_CODE"

    # This test PASSES if we get the expected error
    local error_found=false
    local error_msg=""

    # Check for error in HTTP status or SSE chunks
    if [[ "$HTTP_CODE" != "200" ]]; then
        error_msg=$(echo "$RESPONSE" | grep "^data: " | head -1 | sed 's/^data: //' | jq -r '.error.message // empty')
        if [[ -z "$error_msg" ]]; then
            error_msg="$RESPONSE"
        fi
        # Verify it's a reasoning_content error
        if echo "$error_msg" | grep -qi "reasoning_content"; then
            error_found=true
        fi
    else
        # Check SSE chunks for error
        while IFS= read -r data; do
            [[ "$data" == "[DONE]" ]] && continue
            local chunk_error
            chunk_error=$(echo "$data" | jq -r '.error.message // empty')
            if [[ -n "$chunk_error" ]]; then
                error_msg="$chunk_error"
                if echo "$chunk_error" | grep -qi "reasoning_content"; then
                    error_found=true
                    break
                fi
            fi
        done < <(echo "$RESPONSE" | grep "^data: " | sed 's/^data: //')
    fi

    if [[ "$error_found" == "true" ]]; then
        print_info "Expected error received: $(print_truncated "$error_msg" 200)"
        print_pass "Turn 2 Streaming WITHOUT reasoning_content: Correctly got error"
        record_result "Turn 2 Streaming WITHOUT reasoning_content" "PASS" "Expected reasoning_content error in stream"
        return 0
    fi

    # If we didn't get an error, the test FAILED
    print_info "Response: $RESPONSE"
    print_fail "Turn 2 Streaming: Did NOT get expected reasoning_content error"
    record_result "Turn 2 Streaming WITHOUT reasoning_content" "FAIL" "Expected error but got HTTP $HTTP_CODE"
    return 1
}

# ─────────────────────────────────────────────────────────────────────────────
# Print Summary
# ─────────────────────────────────────────────────────────────────────────────

print_summary() {
    print_header
    echo -e "${BOLD}Test Summary${NC}"
    print_header

    printf "%-45s %s\n" "Test Case" "Result"
    echo "────────────────────────────────────────────────────────"

    local i
    for i in "${!TEST_NAMES[@]}"; do
        local name="${TEST_NAMES[$i]}"
        local result="${TEST_RESULTS[$i]}"
        local detail="${TEST_DETAILS[$i]}"

        if [[ "$result" == "PASS" ]]; then
            printf "%-45s ${GREEN}✓ PASS${NC}\n" "$name"
        else
            printf "%-45s ${RED}✗ FAIL${NC}\n" "$name"
        fi
        if [[ -n "$detail" ]]; then
            printf "  %s\n" "$detail"
        fi
    done

    echo "────────────────────────────────────────────────────────"
    printf "%-45s " "Total:"
    echo "$TESTS_PASSED passed, $TESTS_FAILED failed"
    print_header

    if [[ $TESTS_FAILED -eq 0 ]]; then
        echo -e "${GREEN}${BOLD}All tests passed!${NC}"
        return 0
    else
        echo -e "${RED}${BOLD}Some tests failed!${NC}"
        return 1
    fi
}

# ─────────────────────────────────────────────────────────────────────────────
# Main
# ─────────────────────────────────────────────────────────────────────────────

main() {
    # Parse arguments
    for arg in "$@"; do
        case $arg in
            -h|--help)
                show_help
                ;;
        esac
    done

    echo ""
    print_header
    echo -e "${BOLD}E2E Test: reasoning_content Forwarding${NC}"
    print_header
    echo ""
    print_info "Configuration:"
    echo "  Proxy URL:  $PROXY_URL"
    echo "  Model:      $MODEL"
    echo "  Auth Token: ${AUTH_TOKEN:0:10}..."
    echo ""

    # Check dependencies
    print_info "Checking dependencies..."
    check_dependencies
    print_info "Dependencies OK (curl, jq found)"

    # Validate config
    print_info "Validating configuration..."
    validate_config
    print_info "Configuration OK"

    echo ""
    print_info "Starting tests..."
    echo ""

    # Run tests in order (dependencies between tests)
    test_turn1_nonstreaming || true
    echo ""

    test_turn2_with_reasoning || true
    echo ""

    test_turn2_without_reasoning || true
    echo ""

    test_turn1_streaming || true
    echo ""

    test_turn2_streaming_with_reasoning || true
    echo ""

    test_turn2_streaming_without_reasoning || true
    echo ""

    # Print summary and exit with appropriate code
    print_summary
    exit_code=$?

    exit $exit_code
}

# Run main
main "$@"
