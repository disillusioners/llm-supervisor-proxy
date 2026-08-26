#!/bin/bash

# test/test_mock_credential_lb.sh
#
# E2E test for the model-credential load-balancing feature (Phase 5 / Round 3c W9).
#
# Boots THREE concurrent mock upstreams (mock_llm_lb.go) on ports 4001/4002/4003
# with identities A/B/C and a single proxy on port 4322. Drives:
#
#   Phase 1 (mocks WITHOUT -fail-429-once):
#     1.  Affinity — same token, same first-user message ×5
#     2.  Distribution — same token, 100 unique first-user messages
#     2b. Templated first message — N=10 distinct tokens × 1 hit each
#         (leader-mandated; P[all-same] = 1/19683 ≈ 5.08e-5)
#     2e. Multimodal affinity — same token, same multimodal content ×5
#     8.  /v1/messages affinity (Anthropic → OpenAI upstream) ×5
#
#   Phase 2 (mocks RESTARTED with -fail-429-once=1, fresh hit files,
#             fresh model + credentials to keep 429 counters + conversation
#             keys virgin):
#     9.  Full failover chain — 3 creds [A:1e6, B:1e6, C:1], NO fallback chain,
#         1 request. Expected walk: cred_pick_1 → 429 → cred_pick_2 → 429 →
#         cred_pick_3 → 200. Council-computed flake ~3e-6 per run.
#
# Mock worker sibling (test/mock_llm_lb.go) provides:
#   -port=4001             (default 4001)
#   -credential-identity=A (echoed in response content + [identity=A] log prefix)
#   -fail-429-once=N       (first N requests process-global return 429 + Retry-After:1)
#   -hit-counter-file=PATH (append JSONL per request: ts, path, identity,
#                           outcome[success|rate_limited], status)
#
# Model creation DTO (Phase 4): POST /fe/api/models with
#   {"id", "name", "enabled", "internal":true,
#    "credentials":[{"credential_id","weight","position"}],
#    "internal_model","internal_base_url"}
# Credential creation: POST /fe/api/credentials with {id, provider, api_key, base_url}
# Token creation:      POST /fe/api/tokens      with {name}
#                      → response {"token":"sk-..."} (plaintext, shown once)
#
# State isolation: proxy uses SQLite at $UserConfigDir/llm-supervisor-proxy/config.db
# (UserConfigDir respects $HOME on macOS). Setting HOME to a fresh tmp dir gives
# each run a clean DB + buffer storage. DATABASE_URL is unset → SQLite path.
#
# Maximum runtime: 90 seconds.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Configuration — env-parameterized so two concurrent runs don't collide on
# ports. Defaults preserve original behavior when unset.
MOCK_PORT_A="${PORT_A:-4001}"
MOCK_PORT_B="${PORT_B:-4002}"
MOCK_PORT_C="${PORT_C:-4003}"
PROXY_PORT="${PORT_PROXY:-4322}"

# Test results
TESTS_PASSED=0
TESTS_FAILED=0
TEST_NAMES=()

# Hard timeout (seconds)
HARD_TIMEOUT=90

# Concurrent-run lockfile (mkdir is atomic on POSIX — succeeds for the
# first runner, fails for any concurrent runner that finds the dir).
LOCKFILE="/tmp/cred_lb_e2e.lock"
if ! mkdir "$LOCKFILE" 2>/dev/null; then
    echo -e "${RED}ERROR: another run holds $LOCKFILE — aborting to avoid killing it.${NC}" >&2
    echo -e "${RED}       If no other run is active, remove it manually: rm -rf $LOCKFILE${NC}" >&2
    exit 2
fi

# tmp workdir (HOME-scoped) — created fresh each run for state isolation
TMP_HOME="$(mktemp -d -t 'cred_lb_e2e.XXXXXX')"
mkdir -p "$TMP_HOME/Library/Application Support/llm-supervisor-proxy/buffers"
# Keep a copy of the proxy log at a fixed path for debugging between runs.
PROXY_LOG="/tmp/proxy_credential_lb.log"
: > "$PROXY_LOG"

# Hit counter files (JSONL; rotated per phase + scenario)
HITS_A_FILE="$TMP_HOME/hits_A.jsonl"
HITS_B_FILE="$TMP_HOME/hits_B.jsonl"
HITS_C_FILE="$TMP_HOME/hits_C.jsonl"

# PIDs
MOCK_A_PID=""
MOCK_B_PID=""
MOCK_C_PID=""
PROXY_PID=""
TIMER_PID=""

# ─────────────────────────────────────────────────────────────────────────────
# Helpers
# ─────────────────────────────────────────────────────────────────────────────

assert_pass() {
    local name="$1"
    echo -e "  ${GREEN}✓${NC} $name"
    TESTS_PASSED=$((TESTS_PASSED + 1))
    TEST_NAMES+=("PASS: $name")
}

assert_fail() {
    local name="$1"
    local details="$2"
    echo -e "  ${RED}✗${NC} $name"
    if [ -n "$details" ]; then
        echo -e "      ${RED}$details${NC}"
    fi
    TESTS_FAILED=$((TESTS_FAILED + 1))
    TEST_NAMES+=("FAIL: $name")
}

# Wait until an HTTP endpoint responds 200 (with simple curl), or timeout.
wait_for_http() {
    local url="$1"
    local max_wait="${2:-20}"
    local elapsed=0
    while [ "$elapsed" -lt "$max_wait" ]; do
        if curl -s -o /dev/null -w '%{http_code}' --max-time 2 "$url" 2>/dev/null | grep -qE '^(200|404|405)$'; then
            return 0
        fi
        sleep 0.5
        elapsed=$((elapsed + 1))
    done
    return 1
}

# Wait until mock LLM responds on its port (mock has no /healthz, just /v1/*).
wait_for_mock() {
    local port="$1"
    local max_wait="${2:-15}"
    local elapsed=0
    while [ "$elapsed" -lt "$max_wait" ]; do
        # Mock returns 405 (method not allowed) for GET /v1/chat/completions
        # and 404 for GET / (default mux). 405 means it's listening.
        local code
        code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "http://127.0.0.1:$port/v1/chat/completions" -X GET 2>/dev/null)
        if [ "$code" = "405" ]; then
            return 0
        fi
        sleep 0.3
        elapsed=$((elapsed + 1))
    done
    return 1
}

# Sum a jq filter over a JSONL file; returns 0 if file missing or empty.
hits_count() {
    local file="$1"
    local filter="$2"
    if [ ! -s "$file" ]; then
        echo 0
    else
        jq -c "$filter" "$file" 2>/dev/null | jq -s 'length' 2>/dev/null || echo 0
    fi
}

# Read JSON field via jq.
json_field() {
    local body="$1"
    local filter="$2"
    echo "$body" | jq -r "$filter" 2>/dev/null
}

# Truncate a hit file (start fresh for next scenario).
truncate_hits() {
    : > "$HITS_A_FILE"
    : > "$HITS_B_FILE"
    : > "$HITS_C_FILE"
}

# Print current hit counts per identity and total (for test output).
hit_summary() {
    local sa sb sc
    sa=$(hits_count "$HITS_A_FILE" 'select(.outcome=="success")')
    sb=$(hits_count "$HITS_B_FILE" 'select(.outcome=="success")')
    sc=$(hits_count "$HITS_C_FILE" 'select(.outcome=="success")')
    local ra rb rc
    ra=$(hits_count "$HITS_A_FILE" 'select(.outcome=="rate_limited")')
    rb=$(hits_count "$HITS_B_FILE" 'select(.outcome=="rate_limited")')
    rc=$(hits_count "$HITS_C_FILE" 'select(.outcome=="rate_limited")')
    echo "    A: success=$sa rate_limited=$ra | B: success=$sb rate_limited=$rb | C: success=$sc rate_limited=$rc"
}

# ─────────────────────────────────────────────────────────────────────────────
# Cleanup
# ─────────────────────────────────────────────────────────────────────────────

cleanup_all() {
    local exit_code=$?
    echo -e "\n${YELLOW}Cleaning up processes and state...${NC}"
    [ -n "$TIMER_PID" ] && kill "$TIMER_PID" 2>/dev/null || true
    [ -n "$MOCK_A_PID" ] && kill "$MOCK_A_PID" 2>/dev/null || true
    [ -n "$MOCK_B_PID" ] && kill "$MOCK_B_PID" 2>/dev/null || true
    [ -n "$MOCK_C_PID" ] && kill "$MOCK_C_PID" 2>/dev/null || true
    [ -n "$PROXY_PID" ] && kill "$PROXY_PID" 2>/dev/null || true
    pkill -f "mock_llm_lb.go" 2>/dev/null || true
    pkill -f "cmd/main.go" 2>/dev/null || true
    # Force-kill anything still bound to test ports (LISTENERS only — the proxy
#    connects to mock ports as a client; matching ESTABLISHED connections
    #    would SIGKILL the proxy and prevent clean exit).
    for port in "$MOCK_PORT_A" "$MOCK_PORT_B" "$MOCK_PORT_C" "$PROXY_PORT"; do
        lsof -ti ":$port" -sTCP:LISTEN 2>/dev/null | xargs kill -9 2>/dev/null || true
    done
    wait 2>/dev/null || true
    # Wipe tmp state (HOME-scoped SQLite + buffers + logs)
    rm -rf "$TMP_HOME" 2>/dev/null || true
    # Release the concurrent-run lockfile
    rm -rf "$LOCKFILE" 2>/dev/null || true
    return "$exit_code"
}
trap cleanup_all EXIT

hard_timeout_handler() {
    echo -e "\n${RED}Hard timeout (${HARD_TIMEOUT}s) reached! Terminating...${NC}"
    if [ -s "$PROXY_LOG" ]; then
        echo -e "${YELLOW}=== Last 40 lines of proxy log ===${NC}"
        tail -n 40 "$PROXY_LOG"
    fi
    exit 1
}
trap hard_timeout_handler SIGALRM
( sleep "$HARD_TIMEOUT" && kill -ALRM $$ 2>/dev/null ) &
TIMER_PID=$!

# ─────────────────────────────────────────────────────────────────────────────
# Startup
# ─────────────────────────────────────────────────────────────────────────────

echo -e "${BLUE}============================================${NC}"
echo -e "${BLUE}  Model-Credential LB E2E (Phase 5 / W9)   ${NC}"
echo -e "${BLUE}  Max runtime: ${HARD_TIMEOUT}s                     ${NC}"
echo -e "${BLUE}============================================${NC}"
echo -e "  Workdir HOME: $TMP_HOME"
echo -e "  Mock ports:   $MOCK_PORT_A (A), $MOCK_PORT_B (B), $MOCK_PORT_C (C)"
echo -e "  Proxy port:   $PROXY_PORT"

# Clean ports before starting (source the helper for the primary 2 ports)
# shellcheck disable=SC1091
source "$SCRIPT_DIR/test_mock_clean_ports.sh" "$PROXY_PORT" "$MOCK_PORT_A"
clean_ports "$PROXY_PORT" "$MOCK_PORT_A" "$MOCK_PORT_B" "$MOCK_PORT_C"
# Belt-and-braces: test_mock_clean_ports.sh's clean_ports() only handles 2
# ports, so the helper silently ignores $MOCK_PORT_B / $MOCK_PORT_C. Sweep
# them explicitly so a stale listener from a prior crash doesn't bind here.
for port in "$MOCK_PORT_B" "$MOCK_PORT_C"; do
    lsof -ti ":$port" -sTCP:LISTEN 2>/dev/null | xargs kill -9 2>/dev/null || true
done

# ─────────────────────────────────────────────────────────────────────────────
# Build both binaries BEFORE the HOME switch + BEFORE mocks start. With
# HOME=tmpdir, `go run` would force a fresh GOCACHE and compile every time
# (sometimes blowing past wait_for_mock's 15s window on Phase-2 restarts).
# One-time build with the persistent cache is the deterministic fix.
# ─────────────────────────────────────────────────────────────────────────────

echo -e "\n${YELLOW}Building proxy binary (uses persistent GOPATH/GOMODCACHE before HOME switch)...${NC}"
PROXY_BIN="$TMP_HOME/proxy-binary"
if ! go build -o "$PROXY_BIN" ./cmd/main.go >"$TMP_HOME/build.log" 2>&1; then
    echo -e "${RED}Proxy build failed. Log:${NC}"
    tail -n 40 "$TMP_HOME/build.log"
    exit 1
fi

echo -e "${YELLOW}Building mock binary (one-time, persistent cache)...${NC}"
MOCK_BIN="$TMP_HOME/mock-binary"
if ! go build -o "$MOCK_BIN" ./test/mock_llm_lb.go >"$TMP_HOME/mock_build.log" 2>&1; then
    echo -e "${RED}Mock build failed. Log:${NC}"
    tail -n 40 "$TMP_HOME/mock_build.log"
    exit 1
fi

# Start mock A (identity=A) — invoke the pre-built binary directly (see build
# note above about why we don't `go run`).
echo -e "\n${YELLOW}Starting Mock A (port $MOCK_PORT_A, identity=A)${NC}"
"$MOCK_BIN" \
    -port="$MOCK_PORT_A" \
    -credential-identity=A \
    -hit-counter-file="$HITS_A_FILE" \
    >"$TMP_HOME/mock_A.log" 2>&1 &
MOCK_A_PID=$!

if ! wait_for_mock "$MOCK_PORT_A"; then
    echo -e "${RED}Mock A failed to start. Log:${NC}"
    tail -n 40 "$TMP_HOME/mock_A.log"
    exit 1
fi
echo -e "${GREEN}Mock A started (PID $MOCK_A_PID)${NC}"

# Start mock B (identity=B)
echo -e "\n${YELLOW}Starting Mock B (port $MOCK_PORT_B, identity=B)${NC}"
"$MOCK_BIN" \
    -port="$MOCK_PORT_B" \
    -credential-identity=B \
    -hit-counter-file="$HITS_B_FILE" \
    >"$TMP_HOME/mock_B.log" 2>&1 &
MOCK_B_PID=$!

if ! wait_for_mock "$MOCK_PORT_B"; then
    echo -e "${RED}Mock B failed to start. Log:${NC}"
    tail -n 40 "$TMP_HOME/mock_B.log"
    exit 1
fi
echo -e "${GREEN}Mock B started (PID $MOCK_B_PID)${NC}"

# Start mock C (identity=C)
echo -e "\n${YELLOW}Starting Mock C (port $MOCK_PORT_C, identity=C)${NC}"
"$MOCK_BIN" \
    -port="$MOCK_PORT_C" \
    -credential-identity=C \
    -hit-counter-file="$HITS_C_FILE" \
    >"$TMP_HOME/mock_C.log" 2>&1 &
MOCK_C_PID=$!

if ! wait_for_mock "$MOCK_PORT_C"; then
    echo -e "${RED}Mock C failed to start. Log:${NC}"
    tail -n 40 "$TMP_HOME/mock_C.log"
    exit 1
fi
echo -e "${GREEN}Mock C started (PID $MOCK_C_PID)${NC}"

# ─────────────────────────────────────────────────────────────────────────────
# Proxy startup (env overrides on; SQLite at $HOME/Library/Application Support/...)
# ─────────────────────────────────────────────────────────────────────────────

export HOME="$TMP_HOME"
export APPLY_ENV_OVERRIDES="true"
export PORT="$PROXY_PORT"
export IDLE_TIMEOUT="5s"
export MAX_GENERATION_TIME="15s"
export LOOP_DETECTION_ENABLED="false"
# Sequential credential failover is what we want; disable racing so the failover
# walk is observable end-to-end rather than shadowed by parallel attempts.
export RACE_RETRY_ENABLED="false"

echo -e "\n${YELLOW}Starting proxy on port $PROXY_PORT (HOME=$HOME)...${NC}"
"$PROXY_BIN" >"$PROXY_LOG" 2>&1 &
PROXY_PID=$!

if ! wait_for_http "http://127.0.0.1:$PROXY_PORT/healthz" 30; then
    echo -e "${RED}Proxy failed to start. Log:${NC}"
    tail -n 60 "$PROXY_LOG"
    exit 1
fi
echo -e "${GREEN}Proxy started (PID $PROXY_PID)${NC}"

# ─────────────────────────────────────────────────────────────────────────────
# Configuration: 3 credentials + 2 tokens + 1 multi-credential model
# ─────────────────────────────────────────────────────────────────────────────

echo -e "\n${YELLOW}Configuring credentials, tokens, and model...${NC}"

# Three credentials, all provider=openai (mixed providers are rejected by
# validateCredentials rule (d)). api_key is dummy — the mock doesn't validate it.
for entry in "cred-A:$MOCK_PORT_A" "cred-B:$MOCK_PORT_B" "cred-C:$MOCK_PORT_C"; do
    CID="${entry%%:*}"
    PORT="${entry##*:}"
    RESP=$(curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/credentials" \
        -H "Content-Type: application/json" \
        -d "{\"id\":\"$CID\",\"provider\":\"openai\",\"api_key\":\"sk-test-$CID\",\"base_url\":\"http://127.0.0.1:$PORT/v1\"}")
    if ! echo "$RESP" | grep -q "\"id\":\"$CID\""; then
        echo -e "${RED}Failed to create credential $CID: $RESP${NC}"
        exit 1
    fi
done
echo -e "  ${GREEN}✓ Created credentials cred-A, cred-B, cred-C${NC}"

# TOKEN1 = primary token used by Tests 1/2/2e/8/9. TOKEN2 = legacy pair
# (Test 2b used to alternate these two — Test 2b now creates its own N=10
# fresh tokens inline, so TOKEN2 is unused by the assertions). Token
# creation requires only {"name"}; response carries plaintext in .token.
T1_RESP=$(curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/tokens" \
    -H "Content-Type: application/json" \
    -d '{"name":"cred-lb-token-1"}')
TOKEN1="$(json_field "$T1_RESP" '.token')"
if [ -z "$TOKEN1" ] || [ "$TOKEN1" = "null" ]; then
    echo -e "${RED}Failed to create token 1: $T1_RESP${NC}"
    exit 1
fi
T2_RESP=$(curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/tokens" \
    -H "Content-Type: application/json" \
    -d '{"name":"cred-lb-token-2"}')
TOKEN2="$(json_field "$T2_RESP" '.token')"
if [ -z "$TOKEN2" ] || [ "$TOKEN2" = "null" ]; then
    echo -e "${RED}Failed to create token 2: $T2_RESP${NC}"
    exit 1
fi
echo -e "  ${GREEN}✓ Created 2 tokens (sk-... prefix) — TOKEN1 used by Tests 1/2/2e/8/9${NC}"

# Multi-credential model — 3 equal-weight creds, internal=true (required by
# validateCredentials rule (a) when credentials[] is non-empty).
# NOTE: NO internal_base_url — each credential carries its own base_url
# (port 4001/4002/4003) so the engine's failover actually walks
# between SEPARATE upstream processes. Setting internal_base_url on the
# model would make ALL requests go to that URL regardless of which
# credential was picked — the failover would still work logically but
# we'd never observe per-credential hits in the JSONL counters.
MODEL_RESP=$(curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/models" \
    -H "Content-Type: application/json" \
    -d '{
        "id": "cred-lb-model",
        "name": "Credential LB Model",
        "enabled": true,
        "internal": true,
        "credentials": [
            {"credential_id":"cred-A","position":1,"weight":1},
            {"credential_id":"cred-B","position":2,"weight":1},
            {"credential_id":"cred-C","position":3,"weight":1}
        ],
        "internal_model": "mock-model"
    }')
if ! echo "$MODEL_RESP" | grep -q '"id":"cred-lb-model"'; then
    echo -e "${RED}Failed to create model cred-lb-model: $MODEL_RESP${NC}"
    exit 1
fi
echo -e "  ${GREEN}✓ Created model 'cred-lb-model' with 3 equal-weight creds${NC}"

# Helper: send one OpenAI /v1/chat/completions request, capture HTTP status + body.
send_chat() {
    local token="$1"
    local first_msg="$2"
    local stream="${3:-false}"
    local extra_messages="${4:-[]}"
    local out_file="$TMP_HOME/last_resp.txt"
    local payload
    payload=$(jq -nc \
        --arg model "cred-lb-model" \
        --arg msg "$first_msg" \
        --argjson stream "$stream" \
        --argjson extras "$extra_messages" \
        '{model:$model,messages:([{role:"user",content:$msg}]+$extras),stream:$stream}')
    HTTP_STATUS=$(curl -s -o "$out_file" -w '%{http_code}' \
        --max-time 15 \
        -X POST "http://127.0.0.1:$PROXY_PORT/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $token" \
        -d "$payload")
    BODY="$(cat "$out_file")"
}

# ─────────────────────────────────────────────────────────────────────────────
# Test 1 — Affinity (same token, same message × 5)
# ─────────────────────────────────────────────────────────────────────────────

echo -e "\n${BLUE}━━━ Test 1: Affinity (same token, same message ×5) ━━━${NC}"
truncate_hits
AFF_MSG="affinity-probe-constant-1"
for i in 1 2 3 4 5; do
    send_chat "$TOKEN1" "$AFF_MSG" false
    if [ "$HTTP_STATUS" != "200" ]; then
        assert_fail "Test 1 / request $i" "HTTP $HTTP_STATUS — body: $(echo "$BODY" | head -c 200)"
        continue
    fi
    # Sanity: response must carry identity marker
    if ! echo "$BODY" | grep -q "Hello from mock LLM"; then
        assert_fail "Test 1 / request $i content" "no 'Hello from mock LLM' in body"
    fi
done

SA=$(hits_count "$HITS_A_FILE" 'select(.outcome=="success")')
SB=$(hits_count "$HITS_B_FILE" 'select(.outcome=="success")')
SC=$(hits_count "$HITS_C_FILE" 'select(.outcome=="success")')
TOTAL=$((SA + SB + SC))
echo "  Hit counts:$(hit_summary)"
if [ "$TOTAL" -eq 5 ]; then
    # Exactly one identity should have 5 hits; the others should have 0.
    if { [ "$SA" -eq 5 ] && [ "$SB" -eq 0 ] && [ "$SC" -eq 0 ]; } || \
       { [ "$SB" -eq 5 ] && [ "$SA" -eq 0 ] && [ "$SC" -eq 0 ]; } || \
       { [ "$SC" -eq 5 ] && [ "$SA" -eq 0 ] && [ "$SB" -eq 0 ]; }; then
        assert_pass "Test 1 affinity: 5/5 on a single identity"
    else
        assert_fail "Test 1 affinity" "expected exactly one identity=5, others=0; got A=$SA B=$SB C=$SC"
    fi
else
    assert_fail "Test 1 affinity" "expected 5 total successful hits, got $TOTAL (A=$SA B=$SB C=$SC)"
fi

# ─────────────────────────────────────────────────────────────────────────────
# Test 2 — Distribution (same token, 100 unique first-user messages)
# ─────────────────────────────────────────────────────────────────────────────

echo -e "\n${BLUE}━━━ Test 2: Distribution (100 unique first-user messages) ━━━${NC}"
truncate_hits
N_DIST=100
for i in $(seq 1 $N_DIST); do
    send_chat "$TOKEN1" "unique-dist-msg-$i-$(uuidgen 2>/dev/null || echo "$i-$RANDOM")" false >/dev/null
    if [ "$HTTP_STATUS" != "200" ]; then
        assert_fail "Test 2 / req $i HTTP" "$HTTP_STATUS"
    fi
done

SA=$(hits_count "$HITS_A_FILE" 'select(.outcome=="success")')
SB=$(hits_count "$HITS_B_FILE" 'select(.outcome=="success")')
SC=$(hits_count "$HITS_C_FILE" 'select(.outcome=="success")')
TOTAL=$((SA + SB + SC))
echo "  Hit counts:$(hit_summary)"
DIST_OK=true
for V in "$SA" "$SB" "$SC"; do
    if [ "$V" -lt 10 ] || [ "$V" -gt 60 ]; then
        DIST_OK=false
    fi
done
if [ "$TOTAL" -eq "$N_DIST" ] && [ "$DIST_OK" = true ]; then
    assert_pass "Test 2 distribution: $N_DIST hits split across ≥2 identities (each in [10,60])"
else
    assert_fail "Test 2 distribution" "expected 100 hits, each identity in [10,60]; got A=$SA B=$SB C=$SC (total=$TOTAL)"
fi

# ─────────────────────────────────────────────────────────────────────────────
# Test 2b — Templated first message, N independent tokens (leader-mandated)
#
# PRE-FIX flake (council refuted): 2 tokens × 25 hits = only 2 INDEPENDENT
# picks. The 25 hits per token are affinity-bound to the token's first pick,
# so P[both tokens land on same identity] = 1/3 ≈ 33% — a coin flip per run.
#
# POST-FIX design: ≥10 distinct tokens, each sending EXACTLY ONE request
# with the IDENTICAL templated first-user message. 10 independent picks ⇒
# P[all 10 land on same identity] = 3 × (1/3)^10 = 1/19683 ≈ 5.08e-5.
# (We use exactly N_TPL=10; the leader ruling allows ≥10.)
#
# Lopsided-distribution guard: "no identity > 7 of 10 hits". For each
# identity X_i ~ Binomial(10, 1/3), P[X_i ≥ 8] ≈ 0.00339. Union bound over
# 3 identities: P[max ≥ 8] ≤ ~1.02%. Tighter bounds (e.g. max ≤ 6) raise
# the flake to ~5.9% (P[X_i ≥ 7] ≈ 0.0196 per identity). 7 is the tightest
# bound that keeps the per-run flake under ~1%.
# ─────────────────────────────────────────────────────────────────────────────

echo -e "\n${BLUE}━━━ Test 2b: Templated first message, N independent tokens ━━━${NC}"
echo "  Pre-fix:  2 tokens × 25 hits ⇒ 2 independent picks; P[all-same] = 1/3 (~33%, coin-flip per run)."
echo "  Post-fix: 10 tokens × 1 hit each ⇒ 10 independent picks; P[all-same] = ~5.08e-5 (≈ 1 in 20k)."
truncate_hits
N_TPL=10
TPL_MSG="What is the weather today?"

# Create N_TPL fresh tokens for this scenario (existing TOKEN1/TOKEN2 are
# still used by Tests 1/2/2e/8/9, so leave them alone). Each new token gets
# its own POST /fe/api/tokens; the response carries the plaintext once.
declare -a TPL_TOKENS=()
for idx in $(seq 1 $N_TPL); do
    T_RESP=$(curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/tokens" \
        -H "Content-Type: application/json" \
        -d "{\"name\":\"cred-lb-tpl-token-$idx\"}")
    NEW_TOK="$(json_field "$T_RESP" '.token')"
    if [ -z "$NEW_TOK" ] || [ "$NEW_TOK" = "null" ]; then
        echo -e "${RED}Failed to create templated token $idx: $T_RESP${NC}"
        assert_fail "Test 2b token creation ($idx)" "POST /fe/api/tokens returned no token"
        TPL_TOKENS=()
        break
    fi
    TPL_TOKENS+=("$NEW_TOK")
done

TPL_OK=true
for TOK in "${TPL_TOKENS[@]}"; do
    send_chat "$TOK" "$TPL_MSG" false >/dev/null
    if [ "$HTTP_STATUS" != "200" ]; then
        TPL_OK=false
    fi
done

SA=$(hits_count "$HITS_A_FILE" 'select(.outcome=="success")')
SB=$(hits_count "$HITS_B_FILE" 'select(.outcome=="success")')
SC=$(hits_count "$HITS_C_FILE" 'select(.outcome=="success")')
TOTAL=$((SA + SB + SC))
echo "  Hit counts:$(hit_summary)"
NONZERO=0
MAX_HITS=0
for V in "$SA" "$SB" "$SC"; do
    if [ "$V" -gt 0 ]; then NONZERO=$((NONZERO + 1)); fi
    if [ "$V" -gt "$MAX_HITS" ]; then MAX_HITS="$V"; fi
done
DIST_OK=true
if [ "$MAX_HITS" -gt 7 ]; then
    DIST_OK=false
fi
if [ "$TPL_OK" = true ] && [ "$TOTAL" -eq "$N_TPL" ] && [ "$NONZERO" -ge 2 ] && [ "$DIST_OK" = true ]; then
    assert_pass "Test 2b templated: $N_TPL independent picks, ≥2 identities nonzero, max ≤ 7/10 (A=$SA B=$SB C=$SC)"
else
    assert_fail "Test 2b templated" "expected $N_TPL hits, ≥2 identities nonzero, max ≤ 7; got A=$SA B=$SB C=$SC (total=$TOTAL, ok=$TPL_OK, max=$MAX_HITS)"
fi

# ─────────────────────────────────────────────────────────────────────────────
# Test 2e — Multimodal affinity (same token, same multimodal content × 5)
# ─────────────────────────────────────────────────────────────────────────────

echo -e "\n${BLUE}━━━ Test 2e: Multimodal affinity (same content ×5) ━━━${NC}"
truncate_hits
MM_CONTENT='[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}}]'
for i in 1 2 3 4 5; do
    send_chat "$TOKEN1" "$MM_CONTENT" false >/dev/null
    if [ "$HTTP_STATUS" != "200" ]; then
        assert_fail "Test 2e / req $i HTTP" "$HTTP_STATUS"
    fi
done

SA=$(hits_count "$HITS_A_FILE" 'select(.outcome=="success")')
SB=$(hits_count "$HITS_B_FILE" 'select(.outcome=="success")')
SC=$(hits_count "$HITS_C_FILE" 'select(.outcome=="success")')
TOTAL=$((SA + SB + SC))
echo "  Hit counts:$(hit_summary)"
if [ "$TOTAL" -eq 5 ] && \
   { { [ "$SA" -eq 5 ] && [ "$SB" -eq 0 ] && [ "$SC" -eq 0 ]; } || \
     { [ "$SB" -eq 5 ] && [ "$SA" -eq 0 ] && [ "$SC" -eq 0 ]; } || \
     { [ "$SC" -eq 5 ] && [ "$SA" -eq 0 ] && [ "$SB" -eq 0 ]; }; }; then
    assert_pass "Test 2e multimodal affinity: 5/5 on a single identity"
else
    assert_fail "Test 2e multimodal affinity" "expected exactly one identity=5; got A=$SA B=$SB C=$SC"
fi

# ─────────────────────────────────────────────────────────────────────────────
# Test 8 — /v1/messages affinity (Anthropic → OpenAI upstream, same token ×5)
# ─────────────────────────────────────────────────────────────────────────────

echo -e "\n${BLUE}━━━ Test 8: /v1/messages affinity (same Anthropic-shape ×5) ━━━${NC}"
echo "  (Note: the proxy translates /v1/messages → /v1/chat/completions upstream,"
echo "   so hit-file path will be /v1/chat/completions. We assert on identity.)"
truncate_hits
ANTH_MSG="constant-anthropic-affinity-probe"
ANTH_BODY='{"model":"cred-lb-model","max_tokens":64,"messages":[{"role":"user","content":"'"$ANTH_MSG"'"}]}'
for i in 1 2 3 4 5; do
    HTTP_STATUS=$(curl -s -o "$TMP_HOME/last_anth.txt" -w '%{http_code}' \
        --max-time 15 \
        -X POST "http://127.0.0.1:$PROXY_PORT/v1/messages" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $TOKEN1" \
        -d "$ANTH_BODY")
    BODY="$(cat "$TMP_HOME/last_anth.txt")"
    if [ "$HTTP_STATUS" != "200" ]; then
        assert_fail "Test 8 / req $i HTTP" "$HTTP_STATUS — body: $(echo "$BODY" | head -c 200)"
    fi
done

SA=$(hits_count "$HITS_A_FILE" 'select(.outcome=="success")')
SB=$(hits_count "$HITS_B_FILE" 'select(.outcome=="success")')
SC=$(hits_count "$HITS_C_FILE" 'select(.outcome=="success")')
TOTAL=$((SA + SB + SC))
echo "  Hit counts:$(hit_summary)"
if [ "$TOTAL" -eq 5 ] && \
   { { [ "$SA" -eq 5 ] && [ "$SB" -eq 0 ] && [ "$SC" -eq 0 ]; } || \
     { [ "$SB" -eq 5 ] && [ "$SA" -eq 0 ] && [ "$SC" -eq 0 ]; } || \
     { [ "$SC" -eq 5 ] && [ "$SA" -eq 0 ] && [ "$SB" -eq 0 ]; }; }; then
    assert_pass "Test 8 /v1/messages affinity: 5/5 on a single identity"
else
    assert_fail "Test 8 /v1/messages affinity" "expected exactly one identity=5; got A=$SA B=$SB C=$SC"
fi

# ─────────────────────────────────────────────────────────────────────────────
# PHASE 2 — restart mocks with -fail-429-once=1, fresh hit files, fresh creds/model
# ─────────────────────────────────────────────────────────────────────────────

echo -e "\n${BLUE}━━━ PHASE 2: Restarting mocks with -fail-429-once=1 ━━━${NC}"
echo "  (DEVIATION FROM TASK SPEC: only A/B carry -fail-429-once=1."
echo "   C runs WITHOUT the flag — with all 3 fresh mocks 429ing on"
echo "   first contact, the failover chain walks A→B→C and the"
echo "   3rd attempt also returns 429, leaving zero successes."
echo "   With C as the always-200 mock, the chain can complete as"
echo "   2×429 + 1×200. Weights [A:1e6, B:1e6, C:1] bias the first"
echo "   pick toward a fail-mock so the chain walks the expected"
echo "   A→B→C order. Council-computed flake ~3e-6 per run; P[first"
echo "   pick lands on C] = 1/2000001 ≈ 5e-7.)"

# Kill Phase 1 mocks (SIGKILL — the mocks have no state to preserve and
# graceful SIGTERM occasionally leaves the listener bound past the 1s sleep,
# which makes the new `go run` fail to bind port 4001).
for PID in "$MOCK_A_PID" "$MOCK_B_PID" "$MOCK_C_PID"; do
    [ -n "$PID" ] && kill -9 "$PID" 2>/dev/null || true
done
sleep 1
# Belt-and-braces: force-kill any leftover LISTENERS — test_mock_clean_ports.sh's
# clean_ports() only handles 2 ports, so $MOCK_PORT_B/$MOCK_PORT_C aren't swept
# by the helper. -sTCP:LISTEN keeps us from SIGKILLing the proxy's ESTABLISHED
# connections to the mocks.
for port in "$MOCK_PORT_A" "$MOCK_PORT_B" "$MOCK_PORT_C"; do
    lsof -ti ":$port" -sTCP:LISTEN 2>/dev/null | xargs kill -9 2>/dev/null || true
done
sleep 1
# Verify ports are actually free before the next `go run` tries to bind.
for port in "$MOCK_PORT_A" "$MOCK_PORT_B" "$MOCK_PORT_C"; do
    if lsof -ti ":$port" -sTCP:LISTEN >/dev/null 2>&1; then
        echo -e "${RED}Port $port still in use after kill sweep — Phase 2 cannot start.${NC}"
        lsof -i ":$port" -sTCP:LISTEN 2>/dev/null
        exit 1
    fi
done
MOCK_A_PID=""
MOCK_B_PID=""
MOCK_C_PID=""

# Fresh hit counter files (CRITICAL: -fail-429-once is process-global + non-resettable)
HITS_A_FILE="$TMP_HOME/hits_A2.jsonl"
HITS_B_FILE="$TMP_HOME/hits_B2.jsonl"
HITS_C_FILE="$TMP_HOME/hits_C2.jsonl"
truncate_hits

# Restart mock A (with -fail-429-once=1)
echo -e "${YELLOW}Restarting Mock A (port $MOCK_PORT_A, -fail-429-once=1)${NC}"
"$MOCK_BIN" \
    -port="$MOCK_PORT_A" \
    -credential-identity=A \
    -fail-429-once=1 \
    -hit-counter-file="$HITS_A_FILE" \
    >"$TMP_HOME/mock_A2.log" 2>&1 &
MOCK_A_PID=$!
if ! wait_for_mock "$MOCK_PORT_A"; then
    echo -e "${RED}Mock A (phase 2) failed to start${NC}"
    echo "    --- mock_A2.log ---"
    if [ -s "$TMP_HOME/mock_A2.log" ]; then
        cat "$TMP_HOME/mock_A2.log"
    else
        echo "(empty or missing — new mock never wrote to it)"
    fi
    echo "    --- listening on $MOCK_PORT_A ---"
    lsof -i ":$MOCK_PORT_A" -sTCP:LISTEN 2>/dev/null || echo "(no listener)"
    exit 1
fi
echo -e "${GREEN}Mock A (phase 2) ready (PID $MOCK_A_PID)${NC}"

# Restart mock B (with -fail-429-once=1)
echo -e "${YELLOW}Restarting Mock B (port $MOCK_PORT_B, -fail-429-once=1)${NC}"
"$MOCK_BIN" \
    -port="$MOCK_PORT_B" \
    -credential-identity=B \
    -fail-429-once=1 \
    -hit-counter-file="$HITS_B_FILE" \
    >"$TMP_HOME/mock_B2.log" 2>&1 &
MOCK_B_PID=$!
if ! wait_for_mock "$MOCK_PORT_B"; then
    echo -e "${RED}Mock B (phase 2) failed to start${NC}"
    tail -n 30 "$TMP_HOME/mock_B2.log"
    exit 1
fi
echo -e "${GREEN}Mock B (phase 2) ready (PID $MOCK_B_PID)${NC}"

# Restart mock C — NO -fail-429-once (always returns 200). See PHASE 2 banner.
echo -e "${YELLOW}Restarting Mock C (port $MOCK_PORT_C, NO -fail-429-once — see banner)${NC}"
"$MOCK_BIN" \
    -port="$MOCK_PORT_C" \
    -credential-identity=C \
    -hit-counter-file="$HITS_C_FILE" \
    >"$TMP_HOME/mock_C2.log" 2>&1 &
MOCK_C_PID=$!
if ! wait_for_mock "$MOCK_PORT_C"; then
    echo -e "${RED}Mock C (phase 2) failed to start${NC}"
    tail -n 30 "$TMP_HOME/mock_C2.log"
    exit 1
fi
echo -e "${GREEN}Mock C (phase 2) ready (PID $MOCK_C_PID)${NC}"

# Wait briefly for any in-flight Phase-1 connections to settle.
sleep 1

# Create FRESH credentials (different IDs) so the 429 walk can't be muddied by
# pre-existing affinity bindings. Same provider (openai) per validateCredentials
# rule (d).
echo -e "\n${YELLOW}Creating fresh Phase-2 credentials + model...${NC}"
for entry in "cred-A2:$MOCK_PORT_A" "cred-B2:$MOCK_PORT_B" "cred-C2:$MOCK_PORT_C"; do
    CID="${entry%%:*}"
    PORT="${entry##*:}"
    RESP=$(curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/credentials" \
        -H "Content-Type: application/json" \
        -d "{\"id\":\"$CID\",\"provider\":\"openai\",\"api_key\":\"sk-test-$CID\",\"base_url\":\"http://127.0.0.1:$PORT/v1\"}")
    if ! echo "$RESP" | grep -q "\"id\":\"$CID\""; then
        echo -e "${RED}Failed to create Phase-2 credential $CID: $RESP${NC}"
        exit 1
    fi
done
echo -e "  ${GREEN}✓ Created credentials cred-A2, cred-B2, cred-C2${NC}"

# Fresh model — NO fallback chain (single model; failover is credential-internal).
# NOTE: NO internal_base_url (same reason as Phase-1 model — each cred carries
# its own base_url so the failover walks separate upstream processes).
# Weight bias [A:1e6, B:1e6, C:1] makes the first pick almost always
# land on a fail-mock, so the always-200 mock (C) gets reached last in
# the 3-creds walk. See PHASE 2 banner above for the full rationale and
# council-computed flake probability.
RESP=$(curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/models" \
    -H "Content-Type: application/json" \
    -d '{
        "id": "cred-lb-failover",
        "name": "Credential LB Failover",
        "enabled": true,
        "internal": true,
        "credentials": [
            {"credential_id":"cred-A2","position":1,"weight":1000000},
            {"credential_id":"cred-B2","position":2,"weight":1000000},
            {"credential_id":"cred-C2","position":3,"weight":1}
        ],
        "internal_model": "mock-model"
    }')
if ! echo "$RESP" | grep -q '"id":"cred-lb-failover"'; then
    echo -e "${RED}Failed to create Phase-2 model: $RESP${NC}"
    exit 1
fi
echo -e "  ${GREEN}✓ Created model 'cred-lb-failover' (no fallback chain)${NC}"

# ─────────────────────────────────────────────────────────────────────────────
# Test 9 — Full failover chain, 3 creds, no fallback model
# ─────────────────────────────────────────────────────────────────────────────

echo -e "\n${BLUE}━━━ Test 9: Full failover chain (3 creds, no fallback model) ━━━${NC}"
echo "  (Phase 2 mocks: A,B = -fail-429-once=1; C = no flag (always 200))"
echo "  (Expected walk: cred_pick_1 → 429 → cred_pick_2 → 429 → cred_pick_3 → 200)"
echo "  (Weights A:1e6, B:1e6, C:1 bias the chain so cred_C is reached LAST in"
echo "   nearly every run — council-computed flake ~3e-6; see PHASE 2 banner.)"
truncate_hits

FAIL_MSG="failover-probe-$(date +%s)-$$-$(uuidgen 2>/dev/null || echo $RANDOM)"
payload=$(jq -nc \
    --arg model "cred-lb-failover" \
    --arg msg "$FAIL_MSG" \
    '{model:$model,messages:[{role:"user",content:$msg}],stream:false}')

HTTP_STATUS=$(curl -s -o "$TMP_HOME/failover_resp.txt" -w '%{http_code}' \
    --max-time 30 \
    -X POST "http://127.0.0.1:$PROXY_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $TOKEN1" \
    -d "$payload")
BODY="$(cat "$TMP_HOME/failover_resp.txt")"

echo "  HTTP status: $HTTP_STATUS"
echo "  Body (first 300 chars): $(echo "$BODY" | head -c 300)"

SA=$(hits_count "$HITS_A_FILE" 'select(.outcome=="success")')
SB=$(hits_count "$HITS_B_FILE" 'select(.outcome=="success")')
SC=$(hits_count "$HITS_C_FILE" 'select(.outcome=="success")')
RA=$(hits_count "$HITS_A_FILE" 'select(.outcome=="rate_limited")')
RB=$(hits_count "$HITS_B_FILE" 'select(.outcome=="rate_limited")')
RC=$(hits_count "$HITS_C_FILE" 'select(.outcome=="rate_limited")')
echo "  Hit counts: A success=$SA rate_limited=$RA | B success=$SB rate_limited=$RB | C success=$SC rate_limited=$RC"

TOTAL_SUCCESS=$((SA + SB + SC))
TOTAL_RL=$((RA + RB + RC))
# Per-mock line count = successful + rate_limited (each mock should see exactly 1 line)
LA=$((SA + RA))
LB=$((SB + RB))
LC=$((SC + RC))

FAIL_DETAILS=""
# (a) Client got HTTP 200 and content carries an identity marker
if [ "$HTTP_STATUS" != "200" ]; then
    FAIL_DETAILS+="HTTP was $HTTP_STATUS, expected 200. "
fi
if ! echo "$BODY" | grep -qE 'Hello from mock LLM [ABC]'; then
    FAIL_DETAILS+="response content missing identity marker. "
fi

# (b) Exactly 2 rate_limited + 1 success across the 3 hit files
if [ "$TOTAL_RL" -ne 2 ]; then
    FAIL_DETAILS+="rate_limited total=$TOTAL_RL, expected 2. "
fi
if [ "$TOTAL_SUCCESS" -ne 1 ]; then
    FAIL_DETAILS+="success total=$TOTAL_SUCCESS, expected 1. "
fi

# (c) Each credential tried exactly once (each hit file has exactly 1 line)
for entry in "A:$LA" "B:$LB" "C:$LC"; do
    LABEL="${entry%%:*}"
    VAL="${entry##*:}"
    if [ "$VAL" -ne 1 ]; then
        FAIL_DETAILS+="identity $LABEL saw $VAL hits (expected 1). "
    fi
done

if [ -z "$FAIL_DETAILS" ]; then
    assert_pass "Test 9 failover: 2× 429 + 1× 200 across 3 credentials (client got identity marker)"
else
    PROXY_TAIL="$(tail -n 30 "$PROXY_LOG" 2>/dev/null || echo '(proxy log unavailable)')"
    assert_fail "Test 9 failover" "${FAIL_DETAILS}Proxy log tail: ${PROXY_TAIL}"
fi

# ─────────────────────────────────────────────────────────────────────────────
# Summary
# ─────────────────────────────────────────────────────────────────────────────

echo -e "\n${BLUE}============================================${NC}"
echo -e "${BLUE}             Test Summary                   ${NC}"
echo -e "${BLUE}============================================${NC}"
for name in "${TEST_NAMES[@]}"; do
    case "$name" in
        PASS:*) echo -e "  ${GREEN}$name${NC}" ;;
        FAIL:*) echo -e "  ${RED}$name${NC}" ;;
    esac
done
echo -e ""
echo -e "  ${GREEN}Passed: $TESTS_PASSED${NC}"
echo -e "  ${RED}Failed: $TESTS_FAILED${NC}"
echo -e ""
if [ $TESTS_FAILED -eq 0 ]; then
    echo -e "${GREEN}All 6 scenarios passed.${NC}"
    exit 0
else
    echo -e "${RED}$TESTS_FAILED scenario(s) failed.${NC}"
    exit 1
fi