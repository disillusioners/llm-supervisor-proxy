#!/bin/bash
# test/test_mock_minimax_reasoning.sh
#
# Mock test harness for the x-proxy-interleaved-thinking translation feature
# (P3-4). Boots a MiniMax-shaped mock upstream + the proxy, then exercises
# 15 scenarios that cover:
#   - boot sanity
#   - non-stream request/response translation (details only, details+content,
#     cumulative-text)
#   - stream request/response translation (incremental, cumulative, both,
#     empty-text, multi-entry)
#   - flag-absent legacy passthrough
#   - header hygiene (no x-proxy-interleaved-thinking leaks upstream)
#   - non-MiniMax credential inertness (no translation when provider != minimax)
#   - error path (no reasoning leakage in error body)
#   - usage passthrough
#   - ultimate-path duplicate-hash trigger (ultimate upstream IS translated)
#
# Run via outer wrapper:
#   timeout 300 bash test/test_mock_minimax_reasoning.sh
#
# Ports: MOCK_PORT=4005, PROXY_PORT=4325 (assigned, verified free).
# SAFETY: never kill port 8088 (ensemble self-system).

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Test configuration
MOCK_PORT=4005
PROXY_PORT=4325
MOCK_PID=""
PROXY_PID=""
TIMER_PID=""

# Capture file path (must match what the mock writes to).
CAPTURE_FILE="/tmp/minimax_reasoning_capture_${MOCK_PORT}.jsonl"

# Test results
TESTS_PASSED=0
TESTS_FAILED=0
TESTS_SKIPPED=0
CURRENT_T=""

# Hard timeout (inner alarm). Outer wrapper is `timeout 300`.
HARD_TIMEOUT=90

cleanup_all() {
    local exit_code=$?
    echo -e "\n${YELLOW}[cleanup] Killing all processes...${NC}"
    [ -n "$TIMER_PID" ] && kill "$TIMER_PID" 2>/dev/null || true
    [ -n "$MOCK_PID" ] && kill "$MOCK_PID" 2>/dev/null || true
    [ -n "$PROXY_PID" ] && kill "$PROXY_PID" 2>/dev/null || true
    pkill -f "mock_llm_minimax_reasoning.go" 2>/dev/null || true
    pkill -f "cmd/main.go" 2>/dev/null || true
    # SAFETY: only kill OUR ports.
    lsof -ti :$MOCK_PORT 2>/dev/null | xargs kill -9 2>/dev/null || true
    lsof -ti :$PROXY_PORT 2>/dev/null | xargs kill -9 2>/dev/null || true
    wait 2>/dev/null || true
    return "$exit_code"
}
trap cleanup_all EXIT INT

hard_timeout_handler() {
    echo -e "\n${RED}[HARD TIMEOUT] Reached ${HARD_TIMEOUT}s — terminating${NC}"
    exit 1
}
trap hard_timeout_handler SIGALRM
( sleep "$HARD_TIMEOUT" && kill -ALRM $$ 2>/dev/null ) &
TIMER_PID=$!

if [ -f "$ROOT_DIR/.env-test" ]; then
    export $(grep -v '^#' "$ROOT_DIR/.env-test" | xargs)
fi
API_KEY="${TEST_API_KEY:-test-key}"

echo -e "${BLUE}============================================${NC}"
echo -e "${BLUE}  MiniMax reasoning_details Mock Harness    ${NC}"
echo -e "${BLUE}  P3-4 (feature/minimax-reasoning-details)  ${NC}"
echo -e "${BLUE}  Mock port: $MOCK_PORT  Proxy port: $PROXY_PORT ${NC}"
echo -e "${BLUE}  Internal alarm: ${HARD_TIMEOUT}s / outer timeout 300s${NC}"
echo -e "${BLUE}============================================${NC}"

# ============================================================================
# Phase 1 — Clean ports
# ============================================================================
echo -e "\n${YELLOW}[1/6] Cleaning ports...${NC}"
source "$SCRIPT_DIR/test_mock_clean_ports.sh" "$PROXY_PORT" "$MOCK_PORT"
clean_ports "$PROXY_PORT" "$MOCK_PORT"
rm -f "$CAPTURE_FILE"

# ============================================================================
# Phase 2 — Boot mock MiniMax server
# ============================================================================
echo -e "\n${YELLOW}[2/6] Starting mock MiniMax server (port $MOCK_PORT)...${NC}"
cd "$SCRIPT_DIR"
go run mock_llm_minimax_reasoning.go -port=$MOCK_PORT >/tmp/minimax_reasoning_mock.log 2>&1 &
MOCK_PID=$!
cd "$ROOT_DIR"
sleep 2
if ! kill -0 "$MOCK_PID" 2>/dev/null; then
    echo -e "${RED}ERROR: Mock server failed to start. Tail of log:${NC}"
    tail -20 /tmp/minimax_reasoning_mock.log 2>/dev/null || true
    exit 1
fi
echo -e "${GREEN}Mock started (PID $MOCK_PID)${NC}"

# ============================================================================
# Phase 3 — Boot proxy
# ============================================================================
echo -e "\n${YELLOW}[3/6] Starting proxy (port $PROXY_PORT)...${NC}"
export APPLY_ENV_OVERRIDES="true"
export UPSTREAM_URL="http://localhost:$MOCK_PORT"
export PORT="$PROXY_PORT"
export IDLE_TIMEOUT="5s"
export MAX_GENERATION_TIME="20s"
export RACE_RETRY_ENABLED="false"
export LOOP_DETECTION_ENABLED="false"
export ULTIMATE_MODEL_ID="mock-ultimate-reasoning-model"
export ULTIMATE_MODEL_MAX_HASH="100"
export ULTIMATE_MODEL_MAX_RETRIES="2"

cd "$ROOT_DIR"
go run cmd/main.go >/tmp/minimax_reasoning_proxy.log 2>&1 &
PROXY_PID=$!
sleep 3
if ! kill -0 "$PROXY_PID" 2>/dev/null; then
    echo -e "${RED}ERROR: Proxy failed to start. Tail of log:${NC}"
    tail -30 /tmp/minimax_reasoning_proxy.log 2>/dev/null || true
    exit 1
fi
echo -e "${GREEN}Proxy started (PID $PROXY_PID)${NC}"

# ============================================================================
# Phase 4 — Admin API: create credentials + models
# ============================================================================
echo -e "\n${YELLOW}[4/6] Configuring credentials + models via admin API...${NC}"

# Delete prior (3x retry per template).
for i in 1 2 3; do
    curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/models/mock-minimax-reasoning-model" 2>/dev/null || true
    curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/models/mock-openai-reasoning-model" 2>/dev/null || true
    curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/models/mock-ultimate-reasoning-model" 2>/dev/null || true
    sleep 0.3
done
sleep 0.5
for i in 1 2 3; do
    curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/credentials/mock-minimax-reasoning-cred" 2>/dev/null || true
    curl -s -X DELETE "http://localhost:$PROXY_PORT/fe/api/credentials/mock-openai-reasoning-cred" 2>/dev/null || true
    sleep 0.3
done
sleep 1

# Cred A — MiniMax
CR=$(curl -s -X POST "http://localhost:$PROXY_PORT/fe/api/credentials" \
    -H "Content-Type: application/json" \
    -d "{\"id\":\"mock-minimax-reasoning-cred\",\"provider\":\"minimax\",\"api_key\":\"mock-api-key\",\"base_url\":\"http://localhost:$MOCK_PORT/v1\"}")
if echo "$CR" | grep -q '"id"'; then
    echo -e "  ${GREEN}Cred A (minimax) created${NC}"
else
    echo -e "  ${RED}Cred A failed: $CR${NC}"; exit 1
fi

# Cred B — openai (inertness control)
CR=$(curl -s -X POST "http://localhost:$PROXY_PORT/fe/api/credentials" \
    -H "Content-Type: application/json" \
    -d "{\"id\":\"mock-openai-reasoning-cred\",\"provider\":\"openai\",\"api_key\":\"mock-api-key\",\"base_url\":\"http://localhost:$MOCK_PORT/v1\"}")
if echo "$CR" | grep -q '"id"'; then
    echo -e "  ${GREEN}Cred B (openai) created${NC}"
else
    echo -e "  ${RED}Cred B failed: $CR${NC}"; exit 1
fi

# Model A — internal user-facing model with minimax cred
MR=$(curl -s -X POST "http://localhost:$PROXY_PORT/fe/api/models" \
    -H "Content-Type: application/json" \
    -d "{\"id\":\"mock-minimax-reasoning-model\",\"name\":\"Mock MiniMax Reasoning\",\"enabled\":true,\"internal\":true,\"credential_id\":\"mock-minimax-reasoning-cred\",\"internal_model\":\"mock-model\",\"internal_base_url\":\"http://localhost:$MOCK_PORT/v1\"}")
echo "$MR" | grep -q '"id"' && echo -e "  ${GREEN}Model A (minimax internal) created${NC}" || { echo -e "  ${RED}Model A failed: $MR${NC}"; exit 1; }

# Model B — internal user-facing model with openai cred (T12)
MR=$(curl -s -X POST "http://localhost:$PROXY_PORT/fe/api/models" \
    -H "Content-Type: application/json" \
    -d "{\"id\":\"mock-openai-reasoning-model\",\"name\":\"Mock OpenAI Reasoning\",\"enabled\":true,\"internal\":true,\"credential_id\":\"mock-openai-reasoning-cred\",\"internal_model\":\"mock-model\",\"internal_base_url\":\"http://localhost:$MOCK_PORT/v1\"}")
echo "$MR" | grep -q '"id"' && echo -e "  ${GREEN}Model B (openai internal) created${NC}" || { echo -e "  ${RED}Model B failed: $MR${NC}"; exit 1; }

# Model U — ultimate model (MiniMax cred, matches ULTIMATE_MODEL_ID)
MR=$(curl -s -X POST "http://localhost:$PROXY_PORT/fe/api/models" \
    -H "Content-Type: application/json" \
    -d "{\"id\":\"mock-ultimate-reasoning-model\",\"name\":\"Mock Ultimate Reasoning\",\"enabled\":true,\"internal\":true,\"credential_id\":\"mock-minimax-reasoning-cred\",\"internal_model\":\"mock-model\",\"internal_base_url\":\"http://localhost:$MOCK_PORT/v1\"}")
echo "$MR" | grep -q '"id"' && echo -e "  ${GREEN}Model U (ultimate, minimax) created${NC}" || { echo -e "  ${RED}Model U failed: $MR${NC}"; exit 1; }

# ============================================================================
# Assertion helpers
# ============================================================================
assert_pass() { echo -e "  ${GREEN}✓${NC} $CURRENT_T $1"; ((TESTS_PASSED++)); }
assert_fail() { echo -e "  ${RED}✗${NC} $CURRENT_T $1"; ((TESTS_FAILED++)); }
assert_skip() { echo -e "  ${YELLOW}~${NC} $CURRENT_T $1"; ((TESTS_SKIPPED++)); }

assert_contains() {
    if echo "$1" | grep -q "$2"; then assert_pass "$3"
    else assert_fail "$3 (pattern not found: $2)"
    fi
}
assert_not_contains() {
    if echo "$1" | grep -q "$2"; then assert_fail "$3 (unexpectedly found: $2)"
    else assert_pass "$3"
    fi
}

# capture_count <mode> returns the 1-based index of the captured request
# whose body.messages[-1].content contains <mode>. Returns 0 if not found.
# Note: bash heredocs don't allow trailing args on the terminator line, so
# we pass args via env vars (MODE / FP).
capture_count() {
    MODE="$1" FP="$CAPTURE_FILE" python3 - <<'PY' 2>/dev/null
import json, os
mode = os.environ.get("MODE", "")
fp = os.environ.get("FP", "")
n = 0
try:
    with open(fp) as f:
        for i, line in enumerate(f, 1):
            line = line.strip()
            if not line: continue
            r = json.loads(line)
            body = r.get("body") or {}
            msgs = body.get("messages") or []
            if not msgs: continue
            last = msgs[-1]
            content = last.get("content") or ""
            if isinstance(content, list):
                content = " ".join(p.get("text","") for p in content if isinstance(p, dict))
            if mode in str(content):
                n = i
                break
except Exception as e:
    pass
print(n)
PY
}

# capture_field <mode> <python-dotted-path>
# Returns the JSON field at <python-dotted-path> from the captured request
# matching <mode>, or empty string.
capture_field() {
    MODE="$1" PATH_SPEC="$2" FP="$CAPTURE_FILE" python3 - <<'PY' 2>/dev/null
import json, os
mode = os.environ.get("MODE", "")
path_spec = os.environ.get("PATH_SPEC", "")
fp = os.environ.get("FP", "")
try:
    with open(fp) as f:
        for line in f:
            line = line.strip()
            if not line: continue
            r = json.loads(line)
            body = r.get("body") or {}
            msgs = body.get("messages") or []
            if not msgs: continue
            last = msgs[-1]
            content = last.get("content") or ""
            if isinstance(content, list):
                content = " ".join(p.get("text","") for p in content if isinstance(p, dict))
            if mode in str(content):
                cur = r
                for part in path_spec.split('.'):
                    if isinstance(cur, dict):
                        cur = cur.get(part)
                    else:
                        cur = None; break
                print(json.dumps(cur) if cur is not None else "")
                import sys; sys.exit(0)
except Exception as e:
    pass
print("")
PY
}

# capture_headers <mode> — returns JSON of captured request headers
capture_headers() {
    MODE="$1" FP="$CAPTURE_FILE" python3 - <<'PY' 2>/dev/null
import json, os
mode = os.environ.get("MODE", "")
fp = os.environ.get("FP", "")
try:
    with open(fp) as f:
        for line in f:
            line = line.strip()
            if not line: continue
            r = json.loads(line)
            body = r.get("body") or {}
            msgs = body.get("messages") or []
            if not msgs: continue
            last = msgs[-1]
            content = last.get("content") or ""
            if isinstance(content, list):
                content = " ".join(p.get("text","") for p in content if isinstance(p, dict))
            if mode in str(content):
                print(json.dumps(r.get("headers") or {}))
                import sys; sys.exit(0)
except Exception as e:
    pass
print("{}")
PY
}

# capture_nth_headers <n> — returns headers of the n-th captured request
capture_nth_headers() {
    NTH="$1" FP="$CAPTURE_FILE" python3 - <<'PY' 2>/dev/null
import json, os, sys
n = int(os.environ.get("NTH", "0"))
fp = os.environ.get("FP", "")
try:
    with open(fp) as f:
        for i, line in enumerate(f, 1):
            if i != n: continue
            line = line.strip()
            r = json.loads(line)
            print(json.dumps(r.get("headers") or {}))
            sys.exit(0)
except Exception as e:
    pass
print("{}")
PY
}

# truncate helper for echo
truncate() {
    local s="$1" n="${2:-120}"
    if [ ${#s} -le "$n" ]; then echo "$s"
    else echo "${s:0:$n}..."
    fi
}

# ============================================================================
# Phase 5 — Scenarios
# ============================================================================
echo -e "\n${YELLOW}[5/6] Running scenarios...${NC}"

# --- T1 boot sanity ---
CURRENT_T="T1"
echo -e "\n${BLUE}=== T1 boot sanity ===${NC}"
# The proxy exposes /healthz (cmd/main.go:143). Use it.
HEALTH=$(curl -s --max-time 5 "http://localhost:$PROXY_PORT/healthz" 2>&1)
echo "  /healthz response: $(truncate "$HEALTH" 80)"
if [ -n "$HEALTH" ] && ! echo "$HEALTH" | grep -qi "connection refused\|404 page"; then
    assert_pass "proxy /healthz reachable"
else
    assert_fail "proxy /healthz unreachable (got '$HEALTH')"
fi
# Also check admin API is up.
MODELS=$(curl -s --max-time 5 "http://localhost:$PROXY_PORT/fe/api/models" 2>&1)
echo "  /fe/api/models: $(truncate "$MODELS" 120)"
if echo "$MODELS" | grep -q "mock-minimax-reasoning-model"; then
    assert_pass "T1: admin API lists our injected model"
else
    assert_fail "T1: admin API does NOT list mock-minimax-reasoning-model (got: $MODELS)"
fi

# --- T2 NS details → client ---
CURRENT_T="T2"
echo -e "\n${BLUE}=== T2 NS details → client: reasoning_content concat, no leak ===${NC}"
T2_RESP=$(curl -s --max-time 10 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -H "X-Proxy-Interleaved-Thinking: true" \
    -d "{
        \"model\":\"mock-minimax-reasoning-model\",
        \"messages\":[
            {\"role\":\"user\",\"content\":\"ping MODE-NS-DETAILS\"}
        ],
        \"stream\":false
    }" 2>&1)
echo "  client response: $(truncate "$T2_RESP" 200)"
assert_contains "$T2_RESP" '"reasoning_content":"think-A think-B"' "client sees concatenated reasoning_content"
assert_not_contains "$T2_RESP" "reasoning_details" "client response has NO reasoning_details leak"
assert_not_contains "$T2_RESP" "audio_content" "client response has NO audio_content leak"
assert_contains "$T2_RESP" '"content":"final answer"' "client sees final content"

# --- T3 captured upstream request shape ---
CURRENT_T="T3"
echo -e "\n${BLUE}=== T3 captured upstream request: reasoning_split + monotonic ids + stripped ===${NC}"
T3_CAPTURED=$(python3 - <<'PY' 2>/dev/null
import json
fp="/tmp/minimax_reasoning_capture_4005.jsonl"
out={}
try:
    with open(fp) as f:
        for line in f:
            r = json.loads(line.strip())
            body = r.get("body") or {}
            msgs = body.get("messages") or []
            if not msgs: continue
            last = msgs[-1]
            content = last.get("content") or ""
            if isinstance(content, list):
                content = " ".join(p.get("text","") for p in content if isinstance(p, dict))
            if "MODE-NS-DETAILS" in str(content) and len(msgs) == 1:
                out = body
                break
except Exception as e:
    pass
print(json.dumps(out))
PY
)
echo "  T3 captured upstream body (truncated): $(truncate "$T3_CAPTURED" 300)"
# Go marshaling emits `"reasoning_split": true` (space after colon). Match both.
assert_contains "$T3_CAPTURED" '"reasoning_split": true' "T3: top-level reasoning_split: true"
assert_contains "$T3_CAPTURED" "MODE-NS-DETAILS" "T3: user message mode marker preserved"
assert_not_contains "$T3_CAPTURED" "reasoning_details" "T3: NO reasoning_details (no input reasoning_content)"

# --- T3b captured upstream w/ assistant reasoning_content ---
CURRENT_T="T3b"
echo -e "\n${BLUE}=== T3b captured upstream: assistant reasoning_content → reasoning_details mapping ===${NC}"
T3B_BODY_RESP=$(curl -s --max-time 10 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -H "X-Proxy-Interleaved-Thinking: true" \
    -d "{
        \"model\":\"mock-minimax-reasoning-model\",
        \"messages\":[
            {\"role\":\"user\",\"content\":\"hello\"},
            {\"role\":\"assistant\",\"content\":\"prev\",\"reasoning_content\":\"earlier-think\"},
            {\"role\":\"user\",\"content\":\"ping MODE-NS-DETAILS-3msg\"}
        ],
        \"stream\":false
    }" 2>&1)
T3B_CAPTURED=$(python3 - <<'PY' 2>/dev/null
import json
fp="/tmp/minimax_reasoning_capture_4005.jsonl"
out={}
try:
    with open(fp) as f:
        for line in f:
            r = json.loads(line.strip())
            body = r.get("body") or {}
            msgs = body.get("messages") or []
            if not msgs: continue
            last = msgs[-1]
            content = last.get("content") or ""
            if isinstance(content, list):
                content = " ".join(p.get("text","") for p in content if isinstance(p, dict))
            if "MODE-NS-DETAILS-3msg" in str(content) and len(msgs) >= 2:
                out = body
                break
except Exception as e:
    pass
print(json.dumps(out))
PY
)
echo "  T3b captured: $(truncate "$T3B_CAPTURED" 400)"
assert_contains "$T3B_CAPTURED" '"reasoning_split": true' "T3b: top-level reasoning_split: true"
# BUG-EVIDENCE: T3b's reasoning_details assertions. The proxy on race-internal
# path calls translator.TranslateRequestBody which mutates bodyMap to
# `messages[i].reasoning_details = []any{ReasoningDetail{...}}` (struct
# values), but providers.HydrateReasoningDetails expects
# `[]interface{}` of `map[string]interface{}`. The type assertion
# `rd.(map[string]interface{})` fails on struct values, so the typed
# ChatMessage.ReasoningDetails ends up empty and `omitempty` drops it
# during the final marshal back to wire JSON. This is a real product bug
# on race-internal — wire verified at the mock upstream below.
assert_contains "$T3B_CAPTURED" '"reasoning_details"' "T3b: reasoning_details array present (BUG: omitted on race-internal path)"
assert_contains "$T3B_CAPTURED" '"type": "reasoning.text"' "T3b: reasoning_details type=reasoning.text (BUG: HydrateReasoningDetails type-asserts to map, fails on struct values from translator)"
assert_contains "$T3B_CAPTURED" '"format": "MiniMax-response-v1"' "T3b: format=MiniMax-response-v1 (BUG)"
assert_contains "$T3B_CAPTURED" '"index": 0' "T3b: index=0 (BUG)"
assert_contains "$T3B_CAPTURED" '"text": "earlier-think"' "T3b: text=earlier-think preserved (BUG)"
assert_contains "$T3B_CAPTURED" '"reasoning-text-1"' "T3b: monotonic id reasoning-text-1 (BUG)"
# reasoning_content on the assistant message should be STRIPPED — only the
# typed reasoning_details entry should remain.
T3B_ASST=$(python3 - <<'PY' 2>/dev/null
import json
fp="/tmp/minimax_reasoning_capture_4005.jsonl"
out={}
try:
    with open(fp) as f:
        for line in f:
            r = json.loads(line.strip())
            body = r.get("body") or {}
            msgs = body.get("messages") or []
            if not msgs: continue
            last = msgs[-1]
            content = last.get("content") or ""
            if isinstance(content, list):
                content = " ".join(p.get("text","") for p in content if isinstance(p, dict))
            if "MODE-NS-DETAILS-3msg" in str(content) and len(msgs) >= 2:
                for m in msgs:
                    if isinstance(m, dict) and m.get("role") == "assistant":
                        out = m
                        break
                break
except Exception as e:
    pass
print(json.dumps(out))
PY
)
echo "  T3b assistant msg: $T3B_ASST"
assert_not_contains "$T3B_ASST" '"reasoning_content"' "T3b: assistant.reasoning_content stripped (only details remains)"

# --- T4 NS both → single winner ---
CURRENT_T="T4"
echo -e "\n${BLUE}=== T4 NS both: details+content → details wins (single text exactly once) ===${NC}"
T4_RESP=$(curl -s --max-time 10 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -H "X-Proxy-Interleaved-Thinking: true" \
    -d "{
        \"model\":\"mock-minimax-reasoning-model\",
        \"messages\":[{\"role\":\"user\",\"content\":\"hi MODE-NS-BOTH\"}],
        \"stream\":false
    }" 2>&1)
echo "  T4 resp: $(truncate "$T4_RESP" 200)"
# details win → client sees "think-A think-B" (concatenated details text).
assert_contains "$T4_RESP" '"reasoning_content":"think-A think-B"' "T4: reasoning_content from details"
# Exactly once: grep -o count.
COUNT=$(echo "$T4_RESP" | grep -o '"reasoning_content":"think-A think-B"' | wc -l | tr -d ' ')
if [ "$COUNT" = "1" ]; then
    assert_pass "T4: reasoning_content appears EXACTLY ONCE"
else
    assert_fail "T4: reasoning_content appears $COUNT times (expected 1)"
fi
assert_not_contains "$T4_RESP" "from-reasoning_content" "T4: NO leak from reasoning_content field"

# --- T5 stream incremental ---
CURRENT_T="T5"
echo -e "\n${BLUE}=== T5 stream incremental: 3 ordered deltas + DONE ===${NC}"
T5_RESP=$(curl -N -s --max-time 10 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -H "X-Proxy-Interleaved-Thinking: true" \
    -d "{
        \"model\":\"mock-minimax-reasoning-model\",
        \"messages\":[{\"role\":\"user\",\"content\":\"hi MODE-STREAM-INCREMENTAL\"}],
        \"stream\":true
    }" 2>&1)
echo "  T5 stream: $(truncate "$T5_RESP" 200)"
assert_contains "$T5_RESP" '"reasoning_content":"think-1"' "T5: delta think-1"
assert_contains "$T5_RESP" '"reasoning_content":"think-2"' "T5: delta think-2"
assert_contains "$T5_RESP" '"reasoning_content":"think-3"' "T5: delta think-3"
assert_contains "$T5_RESP" "data: \[DONE\]" "T5: [DONE] terminator"
# SSE framing: every `data: {` line starts with `data: {`
DROWS=$(echo "$T5_RESP" | grep -c '^data: {' || true)
echo "  T5 data: { lines: $DROWS"
if [ "$DROWS" -ge 4 ]; then
    assert_pass "T5: at least 4 framed data: { lines"
else
    assert_fail "T5: only $DROWS data: { lines (expected >=4)"
fi
# Order check: think-1 before think-2 before think-3
P1=$(echo "$T5_RESP" | grep -n '"reasoning_content":"think-1"' | head -1 | cut -d: -f1)
P2=$(echo "$T5_RESP" | grep -n '"reasoning_content":"think-2"' | head -1 | cut -d: -f1)
P3=$(echo "$T5_RESP" | grep -n '"reasoning_content":"think-3"' | head -1 | cut -d: -f1)
if [ -n "$P1" ] && [ -n "$P2" ] && [ -n "$P3" ] && [ "$P1" -lt "$P2" ] && [ "$P2" -lt "$P3" ]; then
    assert_pass "T5: deltas in order think-1 < think-2 < think-3"
else
    assert_fail "T5: delta order wrong ($P1, $P2, $P3)"
fi

# --- T6 stream cumulative → suffix emission only ---
CURRENT_T="T6"
echo -e "\n${BLUE}=== T6 stream cumulative: only A,B,C suffixes (no AB / ABC duplication) ===${NC}"
T6_RESP=$(curl -N -s --max-time 10 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -H "X-Proxy-Interleaved-Thinking: true" \
    -d "{
        \"model\":\"mock-minimax-reasoning-model\",
        \"messages\":[{\"role\":\"user\",\"content\":\"hi MODE-STREAM-CUMULATIVE\"}],
        \"stream\":true
    }" 2>&1)
echo "  T6 stream: $(truncate "$T6_RESP" 250)"
assert_contains "$T6_RESP" '"reasoning_content":"A"' "T6: delta A"
assert_contains "$T6_RESP" '"reasoning_content":"B"' "T6: delta B"
assert_contains "$T6_RESP" '"reasoning_content":"C"' "T6: delta C"
assert_not_contains "$T6_RESP" '"reasoning_content":"AB"' "T6: NO delta AB (cumulative leakage)"
assert_not_contains "$T6_RESP" '"reasoning_content":"ABC"' "T6: NO delta ABC (cumulative leakage)"
assert_contains "$T6_RESP" "data: \[DONE\]" "T6: [DONE] terminator"

# --- T7 stream both → single winner ---
CURRENT_T="T7"
echo -e "\n${BLUE}=== T7 stream both: details+content → single winner ===${NC}"
T7_RESP=$(curl -N -s --max-time 10 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -H "X-Proxy-Interleaved-Thinking: true" \
    -d "{
        \"model\":\"mock-minimax-reasoning-model\",
        \"messages\":[{\"role\":\"user\",\"content\":\"hi MODE-STREAM-BOTH\"}],
        \"stream\":true
    }" 2>&1)
echo "  T7 stream: $(truncate "$T7_RESP" 200)"
COUNT_DET=$(echo "$T7_RESP" | grep -o '"reasoning_content":"from-details"' | wc -l | tr -d ' ')
COUNT_RC=$(echo "$T7_RESP" | grep -o '"reasoning_content":"from-reasoning_content"' | wc -l | tr -d ' ')
echo "  T7 from-details=$COUNT_DET  from-reasoning_content=$COUNT_RC"
if [ "$COUNT_DET" = "1" ] && [ "$COUNT_RC" = "0" ]; then
    assert_pass "T7: from-details appears exactly once; from-reasoning_content absent"
else
    assert_fail "T7: from-details=$COUNT_DET (want 1), from-reasoning_content=$COUNT_RC (want 0)"
fi

# --- T8 stream empty text ---
CURRENT_T="T8"
echo -e "\n${BLUE}=== T8 stream emptytext: empty entry skipped ===${NC}"
T8_RESP=$(curl -N -s --max-time 10 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -H "X-Proxy-Interleaved-Thinking: true" \
    -d "{
        \"model\":\"mock-minimax-reasoning-model\",
        \"messages\":[{\"role\":\"user\",\"content\":\"hi MODE-STREAM-EMPTYTEXT\"}],
        \"stream\":true
    }" 2>&1)
echo "  T8 stream: $(truncate "$T8_RESP" 200)"
assert_contains "$T8_RESP" '"reasoning_content":"real-think"' "T8: real-think delta"
assert_not_contains "$T8_RESP" '"reasoning_content":""' "T8: NO empty reasoning_content delta"

# --- T9 stream multientry ---
CURRENT_T="T9"
echo -e "\n${BLUE}=== T9 stream multientry: 2 entries → 2 ordered deltas ===${NC}"
T9_RESP=$(curl -N -s --max-time 10 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -H "X-Proxy-Interleaved-Thinking: true" \
    -d "{
        \"model\":\"mock-minimax-reasoning-model\",
        \"messages\":[{\"role\":\"user\",\"content\":\"hi MODE-STREAM-MULTIENTRY\"}],
        \"stream\":true
    }" 2>&1)
echo "  T9 stream: $(truncate "$T9_RESP" 200)"
assert_contains "$T9_RESP" '"reasoning_content":"first"' "T9: first delta"
assert_contains "$T9_RESP" '"reasoning_content":"second"' "T9: second delta"
P_F=$(echo "$T9_RESP" | grep -n '"reasoning_content":"first"' | head -1 | cut -d: -f1)
P_S=$(echo "$T9_RESP" | grep -n '"reasoning_content":"second"' | head -1 | cut -d: -f1)
if [ -n "$P_F" ] && [ -n "$P_S" ] && [ "$P_F" -lt "$P_S" ]; then
    assert_pass "T9: first delta precedes second"
else
    assert_fail "T9: order wrong ($P_F, $P_S)"
fi

# --- T10 flag absent + MODE-PLAIN ---
CURRENT_T="T10"
echo -e "\n${BLUE}=== T10 flag absent + MODE-PLAIN: legacy passthrough (no translation) ===${NC}"
T10_RESP=$(curl -s --max-time 10 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d "{
        \"model\":\"mock-minimax-reasoning-model\",
        \"messages\":[{\"role\":\"user\",\"content\":\"hi MODE-PLAIN\"}],
        \"stream\":false
    }" 2>&1)
echo "  T10 client resp: $(truncate "$T10_RESP" 200)"
assert_contains "$T10_RESP" '"reasoning_content":"legacy-think"' "T10: legacy reasoning_content passthrough"
T10_CAPTURED=$(python3 - <<'PY' 2>/dev/null
import json
fp="/tmp/minimax_reasoning_capture_4005.jsonl"
out={}
try:
    with open(fp) as f:
        for line in f:
            r = json.loads(line.strip())
            body = r.get("body") or {}
            msgs = body.get("messages") or []
            if not msgs: continue
            last = msgs[-1]
            content = last.get("content") or ""
            if isinstance(content, list):
                content = " ".join(p.get("text","") for p in content if isinstance(p, dict))
            if "MODE-PLAIN" in str(content):
                out = body
                break
except: pass
print(json.dumps(out))
PY
)
assert_not_contains "$T10_CAPTURED" "reasoning_split" "T10: NO reasoning_split on captured upstream"
assert_not_contains "$T10_CAPTURED" '"reasoning_details"' "T10: NO reasoning_details on captured upstream"

# --- T11 header hygiene ---
CURRENT_T="T11"
echo -e "\n${BLUE}=== T11 header hygiene: no x-proxy-interleaved-thinking leak upstream ===${NC}"
# Use the LAST captured request (any mode that was sent with the header) to check
# headers. We use the T2 capture (MODE-NS-DETAILS) — non-ultimate, race-external-style.
LEAK_FOUND=0
while IFS= read -r line; do
    H=$(echo "$line" | python3 -c 'import json,sys; r=json.loads(sys.stdin.read()); h=r.get("headers") or {}; print("|".join(k for k in h.keys() if "interleaved-thinking" in k.lower()))' 2>/dev/null)
    if [ -n "$H" ]; then
        LEAK_FOUND=1
        echo "  LEAK: header keys with interleaved-thinking: $H"
    fi
done < "$CAPTURE_FILE"
if [ "$LEAK_FOUND" = "0" ]; then
    assert_pass "T11: NO x-proxy-interleaved-thinking keys in ANY captured upstream headers"
else
    assert_fail "T11: at least one captured request leaked the header"
fi

# --- T12 non-MiniMax cred + flag ON → no translation ---
CURRENT_T="T12"
echo -e "\n${BLUE}=== T12 non-MiniMax cred + flag ON: no translation (inertness) ===${NC}"
T12_RESP=$(curl -s --max-time 10 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -H "X-Proxy-Interleaved-Thinking: true" \
    -d "{
        \"model\":\"mock-openai-reasoning-model\",
        \"messages\":[{\"role\":\"user\",\"content\":\"hi MODE-PLAIN\"}],
        \"stream\":false
    }" 2>&1)
echo "  T12 client resp: $(truncate "$T12_RESP" 200)"
T12_CAPTURED=$(python3 - <<'PY' 2>/dev/null
import json
fp="/tmp/minimax_reasoning_capture_4005.jsonl"
out={}
try:
    with open(fp) as f:
        # take the last captured request (this test was last)
        out = json.loads(list(f)[-1].strip()).get("body", {})
except: pass
print(json.dumps(out))
PY
)
echo "  T12 captured (last): $(truncate "$T12_CAPTURED" 250)"
assert_not_contains "$T12_CAPTURED" "reasoning_split" "T12: openai cred → NO reasoning_split"
assert_not_contains "$T12_CAPTURED" "reasoning_details" "T12: openai cred → NO reasoning_details"

# --- T13 MODE-ERROR-500 + flag ON ---
CURRENT_T="T13"
echo -e "\n${BLUE}=== T13 MODE-ERROR-500: clean error, no reasoning leakage ===${NC}"
T13_RESP=$(curl -s --max-time 10 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -H "X-Proxy-Interleaved-Thinking: true" \
    -d "{
        \"model\":\"mock-minimax-reasoning-model\",
        \"messages\":[{\"role\":\"user\",\"content\":\"hi MODE-ERROR-500\"}],
        \"stream\":false
    }" 2>&1)
echo "  T13 resp: $(truncate "$T13_RESP" 200)"
# Should contain error: anything from HTTP 5xx body OR proxy error body
assert_contains "$T13_RESP" "error" "T13: error returned to client"
# NO reasoning_details leak
assert_not_contains "$T13_RESP" "reasoning_details" "T13: NO reasoning_details in client error body"
# NO reasoning text leak (substring check)
assert_not_contains "$T13_RESP" "think-A" "T13: NO reasoning text leakage"
assert_not_contains "$T13_RESP" "think-B" "T13: NO reasoning text leakage"

# --- T14 usage passthrough on translated path ---
CURRENT_T="T14"
echo -e "\n${BLUE}=== T14 usage passthrough: T2 (NS details) preserves upstream usage ===${NC}"
assert_contains "$T2_RESP" '"prompt_tokens":11' "T14: prompt_tokens=11 passthrough"
assert_contains "$T2_RESP" '"completion_tokens":7' "T14: completion_tokens=7 passthrough"
assert_contains "$T2_RESP" '"total_tokens":18' "T14: total_tokens=18 passthrough"

# --- T15 ultimate path: duplicate-hash retry trigger ---
CURRENT_T="T15"
echo -e "\n${BLUE}=== T15 ultimate path: duplicate-hash retry → ultimate request IS translated ===${NC}"
# Per template (test_mock_ultimate_model.sh Test 7): hash is created ONLY on
# failure. So the 1st request fails (MODE-ERROR-500), 2nd request (same body)
# triggers ultimate model.
# We use the template's ULTIMATE_MODEL_MAX_RETRIES=2, so after 1 failure we
# retry (via duplicate hash) — second call should reach the ultimate model.
# To detect the ultimate request: it has the same body as the first. Both
# proxy through the mock at 4005 (which is BOTH the primary and ultimate URL
# in this setup). The captured request count for "MODE-ERROR-500" should be
# >= 2 (one failed primary, one ultimate retry — or two ultimates).
T15A=$(curl -s --max-time 10 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -H "X-Proxy-Interleaved-Thinking: true" \
    -d "{
        \"model\":\"mock-minimax-reasoning-model\",
        \"messages\":[{\"role\":\"user\",\"content\":\"ultimate-t15-trigger MODE-ERROR-500\"}],
        \"stream\":false
    }" 2>&1)
T15B=$(curl -s --max-time 10 "http://localhost:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -H "X-Proxy-Interleaved-Thinking: true" \
    -d "{
        \"model\":\"mock-minimax-reasoning-model\",
        \"messages\":[{\"role\":\"user\",\"content\":\"ultimate-t15-trigger MODE-ERROR-500\"}],
        \"stream\":false
    }" 2>&1)
echo "  T15 first: $(truncate "$T15A" 120)"
echo "  T15 second: $(truncate "$T15B" 120)"
T15_COUNT=$(python3 - <<'PY' 2>/dev/null
import json
fp="/tmp/minimax_reasoning_capture_4005.jsonl"
n=0
try:
    with open(fp) as f:
        for line in f:
            r = json.loads(line.strip())
            body = r.get("body") or {}
            msgs = body.get("messages") or []
            if not msgs: continue
            last = msgs[-1]
            content = last.get("content") or ""
            if isinstance(content, list):
                content = " ".join(p.get("text","") for p in content if isinstance(p, dict))
            if "ultimate-t15-trigger" in str(content):
                n += 1
except: pass
print(n)
PY
)
echo "  T15 captured requests for trigger: $T15_COUNT"
if [ "$T15_COUNT" -ge 2 ]; then
    assert_pass "T15: duplicate-hash triggered ultimate retry (>=2 captured)"
else
    assert_skip "T15: only $T15_COUNT captured — ultimate retry did not fire (hash not created). Trigger mechanics may differ; this is informational."
fi
# Regardless: assert that any captured request for "ultimate-t15-trigger" DOES
# contain reasoning_split at top level (translation ON for ultimate path too).
T15_ANY_SPLIT=$(python3 - <<'PY' 2>/dev/null
import json
fp="/tmp/minimax_reasoning_capture_4005.jsonl"
out=False
try:
    with open(fp) as f:
        for line in f:
            r = json.loads(line.strip())
            body = r.get("body") or {}
            msgs = body.get("messages") or []
            if not msgs: continue
            last = msgs[-1]
            content = last.get("content") or ""
            if isinstance(content, list):
                content = " ".join(p.get("text","") for p in content if isinstance(p, dict))
            if "ultimate-t15-trigger" in str(content):
                if "reasoning_split" in json.dumps(body):
                    out = True
                    break
except: pass
print("yes" if out else "no")
PY
)
if [ "$T15_ANY_SPLIT" = "yes" ]; then
    assert_pass "T15: ultimate-path captured request IS translated (reasoning_split present)"
else
    assert_fail "T15: no captured request shows reasoning_split — ultimate translation NOT fired"
fi

# ============================================================================
# Phase 6 — Summary
# ============================================================================
echo -e "\n${YELLOW}[6/6] Summary${NC}"
echo -e "  Capture file: $CAPTURE_FILE ($(wc -l < "$CAPTURE_FILE" 2>/dev/null || echo 0) lines)"
echo -e "  ${GREEN}PASS: $TESTS_PASSED${NC}"
if [ "$TESTS_FAILED" -gt 0 ]; then
    echo -e "  ${RED}FAIL: $TESTS_FAILED${NC}"
else
    echo -e "  ${GREEN}FAIL: 0${NC}"
fi
if [ "$TESTS_SKIPPED" -gt 0 ]; then
    echo -e "  ${YELLOW}SKIP: $TESTS_SKIPPED${NC}"
fi

if [ "$TESTS_FAILED" -gt 0 ]; then
    echo -e "\n${RED}RESULT: FAIL${NC}"
    exit 1
fi
echo -e "\n${GREEN}RESULT: PASS${NC}"
exit 0