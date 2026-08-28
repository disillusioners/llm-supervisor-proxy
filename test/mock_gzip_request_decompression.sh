#!/usr/bin/env bash
# Mock Test GZ — gzip request-body decompression E2E (feature/gzip-request-support @ 7a9ecff)
#
# Branch: feature/gzip-request-support @ 7a9ecff
# Ports: 10140 (proxy under test), 10141 (stub upstream). Strict isolation.
# Never touches 8088 (ensemble self-system) or 1011x/1012x/1013x (other mocks).
#
# Verifies the gzipmw middleware (pkg/middleware/gzipmw, wired in cmd/main.go as
# recoveryMiddleware(gzipmw.DecompressRequest(mux))):
#   a) /v1/chat/completions identity (control vs gzip+Content-Encoding) — 4 assertions
#   b) /v1/messages identity (Anthropic path) — 4 assertions on translated upstream bytes
#   c) gzip body WITHOUT Content-Encoding header → 4xx, upstream NOT called
#   d) corrupt gzip + Content-Encoding: gzip → 400, upstream NOT called
#   e) zip bomb (150 MiB zeros gzip'd) + Content-Encoding: gzip → 413 (cap = 100 MiB),
#      then healthz 200 + normal request 200 (proxy alive, not hung, <30s)
#   f) passthrough untouched (no header): /healthz 200, /fe/api/config 200,
#      /fe/api/events SSE delivers ≥1 byte within 20s (event-triggered; heartbeat
#      in code is 30s — see deviation note in final report)
#
# Stub upstream records EXACT received body bytes to body_<n>.bin and headers
# to hdr_<n>.txt, returns a FIXED deterministic non-stream OpenAI completion
# (same bytes every call).
#
# Authoritative spec: .agents/tester/MOCK_TESTS.md §"Mock Test: Gzip Request-Body
# Decompression E2E" (lines 559-616).
#
# Self-kill: dual-layer timeout — internal 240s subshell alarm + outer
# `timeout 300` expected from CI dispatch.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

# ---- strict port isolation ----
PROXY_PORT=10140
MOCK_PORT=10141
# Hard guard: refuse to touch 8088
if [ "$PROXY_PORT" = "8088" ] || [ "$MOCK_PORT" = "8088" ]; then
    echo "FATAL: refusing to bind 8088" >&2
    exit 2
fi

# ---- colors ----
RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[1;33m'; BLUE=$'\033[0;34m'; NC=$'\033[0m'

# ---- tracking ----
PROXY_PID=""
MOCK_PID=""
ALARM_PID=""
TMPDIR=""
TOKEN_ID=""

cleanup() {
    local code=$?
    # Delete test token while proxy still alive
    if [ -n "$TOKEN_ID" ] && [ -n "$PROXY_PID" ] && kill -0 "$PROXY_PID" 2>/dev/null; then
        curl -s -X DELETE "http://127.0.0.1:$PROXY_PORT/fe/api/tokens/$TOKEN_ID" >/dev/null 2>&1 || true
    fi
    if [ -n "$ALARM_PID" ]; then kill "$ALARM_PID" 2>/dev/null || true; fi
    if [ -n "$MOCK_PID" ]; then kill "$MOCK_PID" 2>/dev/null || true; fi
    if [ -n "$PROXY_PID" ]; then kill "$PROXY_PID" 2>/dev/null || true; fi
    # Targeted port cleanup — only kill PIDs we started
    for port in "$PROXY_PORT" "$MOCK_PORT"; do
        for pid in $(lsof -ti:"$port" 2>/dev/null || true); do
            cmd=$(ps -o command= -p "$pid" 2>/dev/null || true)
            case "$cmd" in
                *gzip_proxy*|*mock_gzip_upstream*|*mock_gzip_request_decompression*) kill -9 "$pid" 2>/dev/null || true ;;
                *) ;;
            esac
        done
    done
    [ -n "$TMPDIR" ] && [ -d "$TMPDIR" ] && rm -rf "$TMPDIR"
    return "$code"
}
trap cleanup EXIT

# ---- internal 240s alarm (timeout enforcement) ----
HARD_TIMEOUT=240
(
    sleep "$HARD_TIMEOUT"
    echo "${RED}[FATAL] internal ${HARD_TIMEOUT}s alarm fired${NC}" >&2
    pkill -P $$ 2>/dev/null || true
    kill -9 $$ 2>/dev/null || true
) &
ALARM_PID=$!

echo "${BLUE}===========================================${NC}"
echo "${BLUE}  GZ — gzip request-body decompression     ${NC}"
echo "${BLUE}  Branch: feature/gzip-request-support   ${NC}"
echo "${BLUE}  Proxy port: $PROXY_PORT | Mock port: $MOCK_PORT    ${NC}"
echo "${BLUE}===========================================${NC}"

# ---- 1. build proxy binary FIRST (using default HOME for go module cache) ----
echo "${YELLOW}[1/8] Building proxy binary from HEAD ($(git -C "$ROOT_DIR" rev-parse --short HEAD))...${NC}"
TMPDIR=$(mktemp -d -t gzip-XXXXXX)
PROXY_BIN="$TMPDIR/gzip_proxy"
( cd "$ROOT_DIR" && go build -o "$PROXY_BIN" ./cmd ) > "$TMPDIR/build.log" 2>&1
if [ ! -x "$PROXY_BIN" ]; then
    echo "${RED}go build failed; log:${NC}"; tail -30 "$TMPDIR/build.log"
    exit 1
fi
echo "${GREEN}[1/8] proxy binary built ($PROXY_BIN)${NC}"

# ---- 2. NOW set isolated HOME for runtime (CRITICAL: never ~/.config/llm-supervisor-proxy) ----
export HOME="$TMPDIR/home"
mkdir -p "$HOME"
export XDG_CONFIG_HOME="$HOME/.config"
mkdir -p "$XDG_CONFIG_HOME"
echo "${YELLOW}[2/8] isolated HOME=$HOME (runtime only)${NC}"

# ---- 3. verify ports free ----
for port in "$PROXY_PORT" "$MOCK_PORT"; do
    pids=$(lsof -ti:"$port" 2>/dev/null || true)
    if [ -n "$pids" ]; then
        echo "${RED}FATAL: port $port in use by PIDs: $pids${NC}" >&2
        exit 2
    fi
done

# ---- 4. start mock upstream on 10141 ----
echo "${YELLOW}[3/8] Starting mock upstream on $MOCK_PORT (records body_n.bin + hdr_n.txt)...${NC}"
RECORD_DIR="$TMPDIR/records"
mkdir -p "$RECORD_DIR"
cat > "$TMPDIR/mock_gzip_upstream.py" <<PYEOF
#!/usr/bin/env python3
"""Mock upstream for gzip test (port ${MOCK_PORT}).

Records EXACT received body bytes to body_<n>.bin and headers to hdr_<n>.txt.
Returns a FIXED deterministic non-stream OpenAI completion (same bytes every call).
The record directory and port are passed via argv to keep the script self-contained.

The body counter is process-local and monotonic; with stream=false non-stream
requests, every successful POST increments it by exactly 1.
"""
import json
import os
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

RECORD_DIR = "${RECORD_DIR}"
PORT = ${MOCK_PORT}
_counter_lock = threading.Lock()
_counter = 0

# Pre-baked FIXED response — identical bytes every call.
FIXED_RESPONSE = json.dumps({
    "id": "chatcmpl-gzip-001",
    "object": "chat.completion",
    "created": 1700000000,
    "model": "mock-gzip",
    "choices": [{
        "index": 0,
        "message": {"role": "assistant", "content": "gzip-fixed-completion"},
        "finish_reason": "stop",
    }],
    "usage": {"prompt_tokens": 10, "completion_tokens": 3, "total_tokens": 13},
}, separators=(',', ':')).encode()


def next_n():
    global _counter
    with _counter_lock:
        _counter += 1
        return _counter


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args, **kwargs):
        pass  # quiet

    def do_POST(self):
        n = next_n()
        length = int(self.headers.get("Content-Length", "0"))
        # Read exactly Content-Length bytes (no Transfer-Encoding expected on
        # proxy→upstream POSTs; the proxy always sets Content-Length).
        body = self.rfile.read(length) if length else b""

        with open(os.path.join(RECORD_DIR, f"body_{n}.bin"), "wb") as f:
            f.write(body)
        with open(os.path.join(RECORD_DIR, f"hdr_{n}.txt"), "w") as f:
            for k, v in self.headers.items():
                f.write(f"{k}: {v}\n")

        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(FIXED_RESPONSE)))
        self.end_headers()
        self.wfile.write(FIXED_RESPONSE)


if __name__ == "__main__":
    srv = ThreadingHTTPServer(("127.0.0.1", PORT), Handler)
    print(f"[mock-{PORT}] listening on 127.0.0.1:{PORT}, recording to {RECORD_DIR}", flush=True)
    srv.serve_forever()
PYEOF

python3 "$TMPDIR/mock_gzip_upstream.py" > "$TMPDIR/mock.log" 2>&1 &
MOCK_PID=$!

# Wait for mock to be ready
for i in $(seq 1 50); do
    sleep 0.1
    if curl -s -o /dev/null -w "%{http_code}" -X POST "http://127.0.0.1:$MOCK_PORT/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -d '{"model":"x","messages":[{"role":"user","content":"hi"}],"stream":false}' \
        2>/dev/null | grep -q "200"; then
        echo "${GREEN}[3/8] mock upstream ready (PID $MOCK_PID)${NC}"
        break
    fi
    if [ "$i" -eq 50 ]; then
        echo "${RED}mock upstream failed to start; log:${NC}"; cat "$TMPDIR/mock.log"
        exit 1
    fi
done

# ---- 5. start proxy with temp HOME isolation ----
echo "${YELLOW}[4/8] Starting proxy on $PROXY_PORT (HOME=$HOME, no DATABASE_URL → SQLite)...${NC}"
export PORT="$PROXY_PORT"
export APPLY_ENV_OVERRIDES="true"
export UPSTREAM_URL="http://127.0.0.1:$MOCK_PORT/v1"
export IDLE_TIMEOUT="5s"
export MAX_GENERATION_TIME="20s"
export LOOP_DETECTION_ENABLED="false"
export ULTIMATE_MODEL_ID=""
export ULTIMATE_MODEL_MAX_HASH="100"

# XDG_CONFIG_HOME for explicit isolation (same as M2/M3)
export XDG_CONFIG_HOME="$HOME/.config"
mkdir -p "$XDG_CONFIG_HOME"

# Sanity: ensure DATABASE_URL is NOT set (test mandates SQLite-backed fresh DB)
unset DATABASE_URL

"$PROXY_BIN" > "$TMPDIR/proxy.log" 2>&1 &
PROXY_PID=$!

# Wait for proxy /healthz (max 15s)
for i in $(seq 1 150); do
    sleep 0.1
    if curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:$PROXY_PORT/healthz" 2>/dev/null | grep -q "200"; then
        echo "${GREEN}[4/8] proxy ready (PID $PROXY_PID)${NC}"
        break
    fi
    if [ "$i" -eq 150 ]; then
        echo "${RED}proxy failed to start; log tail:${NC}"; tail -30 "$TMPDIR/proxy.log"
        exit 1
    fi
done

# ---- 6. configure credential + model + token via /fe/api ----
echo "${YELLOW}[5/8] Configuring credential + model + token...${NC}"

# Clean any stale state (idempotent)
curl -s -X DELETE "http://127.0.0.1:$PROXY_PORT/fe/api/models/mock-gzip-model" >/dev/null 2>&1 || true
curl -s -X DELETE "http://127.0.0.1:$PROXY_PORT/fe/api/credentials/mock-gzip-cred" >/dev/null 2>&1 || true
sleep 1

# Sweep stale gzip-test tokens
STALE_TOKEN_IDS=$(curl -s "http://127.0.0.1:$PROXY_PORT/fe/api/tokens" | python3 -c '
import json, sys
try:
    for t in json.load(sys.stdin):
        if t.get("name") == "gzip-test":
            print(t["id"])
except Exception:
    pass
' 2>/dev/null || true)
for tid in $STALE_TOKEN_IDS; do
    curl -s -X DELETE "http://127.0.0.1:$PROXY_PORT/fe/api/tokens/$tid" >/dev/null 2>&1 || true
done

# Credential (OpenAI provider → Anthropic→OpenAI translation path)
CRED_RESP=$(curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/credentials" \
    -H "Content-Type: application/json" \
    -d "{
        \"id\": \"mock-gzip-cred\",
        \"provider\": \"openai\",
        \"api_key\": \"mock-api-key\",
        \"base_url\": \"http://127.0.0.1:$MOCK_PORT/v1\"
    }")
if ! echo "$CRED_RESP" | grep -q '"id"'; then
    echo "${RED}credential creation failed: $CRED_RESP${NC}"; exit 1
fi
echo "${GREEN}  credential created (provider=openai)${NC}"

# External model — uses UPSTREAM_URL for both OpenAI path (race_external) and
# Anthropic path (external translation → /v1/chat/completions). internal=false.
MODEL_RESP=$(curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/models" \
    -H "Content-Type: application/json" \
    -d "{
        \"id\": \"mock-gzip-model\",
        \"name\": \"Mock Gzip Model\",
        \"enabled\": true,
        \"internal\": false,
        \"credentials\": [{\"credential_id\": \"mock-gzip-cred\", \"weight\": 1, \"position\": 0}]
    }")
if ! echo "$MODEL_RESP" | grep -q '"id"'; then
    echo "${RED}model creation failed: $MODEL_RESP${NC}"; exit 1
fi
echo "${GREEN}  external model created (OpenAI race-external + Anthropic translation)${NC}"

# Test token
TOKEN_RESP=$(curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/tokens" \
    -H "Content-Type: application/json" \
    -d '{"name": "gzip-test"}')
API_KEY=$(echo "$TOKEN_RESP" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("token",""))')
TOKEN_ID=$(echo "$TOKEN_RESP" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("id",""))')
if [ -z "$API_KEY" ] || [ -z "$TOKEN_ID" ]; then
    echo "${RED}token creation failed: $TOKEN_RESP${NC}"; exit 1
fi
echo "${GREEN}  test token created${NC}"

# ============================================================
# Helpers
# ============================================================

# Count body files in the record directory (= upstream call count for non-stream).
count_bodies() {
    ls "$RECORD_DIR"/body_*.bin 2>/dev/null | wc -l | tr -d ' '
}

# Print a scenario result line in the spec's required format.
print_scenario() {
    local label="$1" status="$2" evidence="$3"
    if [ "$status" = "PASS" ]; then
        echo "${GREEN}SCENARIO $label: PASS${NC} — $evidence"
    else
        echo "${RED}SCENARIO $label: FAIL${NC} — $evidence"
    fi
}

# Per-scenario result accumulators
RES_A="PASS"; RES_B="PASS"; RES_C="PASS"; RES_D="PASS"; RES_E="PASS"; RES_F="PASS"

# Body files captured for each scenario
A_FILES=()
B_FILES=()

# ============================================================
# SCENARIO a: /v1/chat/completions identity
# ============================================================
echo ""
echo "${BLUE}[a] /v1/chat/completions identity (control vs gzip)${NC}"

# Non-trivial JSON: unicode + nested tool definition + stream:false.
# The string is intentionally identical bytes between control and gzip paths
# (so after decompression the gzip path sees the same bytes the control path
# sent uncompressed).
TEST_BODY_A='{"model":"mock-gzip-model","messages":[{"role":"user","content":"Hello 世界 🌍 — café résumé naïve"}],"tools":[{"type":"function","function":{"name":"search","description":"Search the web — multi-line\nwith literal newline","parameters":{"type":"object","properties":{"query":{"type":"string","description":"Search query"}},"required":["query"]}}}],"stream":false,"max_tokens":128,"temperature":0.5}'

# Persist body to disk (so gzip + curl --data-binary use identical bytes)
echo -n "$TEST_BODY_A" > "$TMPDIR/a_body.json"

COUNT_BEFORE_A=$(count_bodies)

# Control POST (no gzip)
CTRL_HTTP=$(curl -s -o "$TMPDIR/a_ctrl_body" -D "$TMPDIR/a_ctrl_hdr" \
    -w "%{http_code}" --max-time 20 \
    -X POST "http://127.0.0.1:$PROXY_PORT/v1/chat/completions" \
    -H "Authorization: Bearer $API_KEY" \
    -H "Content-Type: application/json" \
    --data-binary "@$TMPDIR/a_body.json")
echo "  control status=$CTRL_HTTP"

# Gzip + Content-Encoding
gzip -c "$TMPDIR/a_body.json" > "$TMPDIR/a_body.json.gz"
GZ_HTTP=$(curl -s -o "$TMPDIR/a_gz_body" -D "$TMPDIR/a_gz_hdr" \
    -w "%{http_code}" --max-time 20 \
    -X POST "http://127.0.0.1:$PROXY_PORT/v1/chat/completions" \
    -H "Authorization: Bearer $API_KEY" \
    -H "Content-Type: application/json" \
    -H "Content-Encoding: gzip" \
    --data-binary "@$TMPDIR/a_body.json.gz")
echo "  gzip status=$GZ_HTTP"

COUNT_AFTER_A=$(count_bodies)

# Locate the two newest body files (control then gzip, in order)
A_BODIES=($(ls -1 "$RECORD_DIR"/body_*.bin 2>/dev/null | sort))
NUM_A=${#A_BODIES[@]}
A_BODY_CTRL="${A_BODIES[$NUM_A-2]:-}"
A_BODY_GZ="${A_BODIES[$NUM_A-1]:-}"
# FIX (harness): mock writes hdr_N.txt, not body_N.txt — derive correctly.
A_HDR_CTRL="${A_BODY_CTRL/body_/hdr_}"
A_HDR_CTRL="${A_HDR_CTRL/.bin/.txt}"
A_HDR_GZ="${A_BODY_GZ/body_/hdr_}"
A_HDR_GZ="${A_HDR_GZ/.bin/.txt}"

EVID_A=""
OK_A=true

# (a1) both client responses 200
if [ "$CTRL_HTTP" != "200" ] || [ "$GZ_HTTP" != "200" ]; then
    OK_A=false; EVID_A="$EVID_A http=ctrl:$CTRL_HTTP/gz:$GZ_HTTP"
else
    EVID_A="$EVID_A http=200/200"
fi

# (a2) client response bodies byte-identical
if cmp -s "$TMPDIR/a_ctrl_body" "$TMPDIR/a_gz_body"; then
    SZ=$(wc -c < "$TMPDIR/a_ctrl_body" | tr -d ' ')
    EVID_A="$EVID_A client-body=cmp-ok($SZ B)"
else
    OK_A=false
    EVID_A="$EVID_A client-body=DIFF"
    EVID_A="$EVID_A cmp-l-head:$(cmp -l "$TMPDIR/a_ctrl_body" "$TMPDIR/a_gz_body" 2>/dev/null | head -5 | tr '\n' '|')"
fi

# (a3) stub-recorded upstream bodies byte-identical (last two)
if [ -n "$A_BODY_CTRL" ] && [ -n "$A_BODY_GZ" ] && [ -f "$A_BODY_CTRL" ] && [ -f "$A_BODY_GZ" ]; then
    if cmp -s "$A_BODY_CTRL" "$A_BODY_GZ"; then
        SZ=$(wc -c < "$A_BODY_CTRL" | tr -d ' ')
        EVID_A="$EVID_A upstream-body=cmp-ok($SZ B)"
    else
        OK_A=false
        EVID_A="$EVID_A upstream-body=DIFF ctrl=$(basename "$A_BODY_CTRL") gz=$(basename "$A_BODY_GZ")"
        EVID_A="$EVID_A cmp-l-head:$(cmp -l "$A_BODY_CTRL" "$A_BODY_GZ" 2>/dev/null | head -5 | tr '\n' '|')"
    fi
else
    OK_A=false; EVID_A="$EVID_A upstream-body=missing ctrl=$A_BODY_CTRL gz=$A_BODY_GZ"
fi

# (a4) stub saw NO Content-Encoding header on either upstream request
if [ -f "$A_HDR_CTRL" ] && [ -f "$A_HDR_GZ" ]; then
    if grep -qi '^Content-Encoding' "$A_HDR_CTRL" || grep -qi '^Content-Encoding' "$A_HDR_GZ"; then
        OK_A=false; EVID_A="$EVID_A stub-saw-Content-Encoding"
    else
        EVID_A="$EVID_A stub-no-content-encoding"
    fi
else
    OK_A=false; EVID_A="$EVID_A stub-hdr-missing"
fi

# (a5) client response carries NO Content-Encoding header (no response compression)
if grep -qi '^Content-Encoding' "$TMPDIR/a_ctrl_hdr" || grep -qi '^Content-Encoding' "$TMPDIR/a_gz_hdr"; then
    OK_A=false; EVID_A="$EVID_A client-resp-has-Content-Encoding"
else
    EVID_A="$EVID_A client-resp-no-content-encoding"
fi

# (a6) upstream call count grew by exactly 2
GROWTH_A=$((COUNT_AFTER_A - COUNT_BEFORE_A))
if [ "$GROWTH_A" -ne 2 ]; then
    OK_A=false; EVID_A="$EVID_A upstream-growth=$GROWTH_A(expect 2)"
else
    EVID_A="$EVID_A upstream-growth=2"
fi

if [ "$OK_A" = true ]; then
    print_scenario "a" "PASS" "$EVID_A"
else
    RES_A="FAIL"; print_scenario "a" "FAIL" "$EVID_A"
fi

# ============================================================
# SCENARIO b: /v1/messages (Anthropic path) identity
# ============================================================
echo ""
echo "${BLUE}[b] /v1/messages identity (Anthropic path, control vs gzip)${NC}"

# Anthropic-shape JSON. After translation by the proxy, this becomes OpenAI
# shape on the wire to the stub upstream. The translated bytes are what
# the stub records; control and gzip must produce identical translated bytes.
TEST_BODY_B='{"model":"mock-gzip-model","max_tokens":128,"messages":[{"role":"user","content":"Bonjour 世界 🌍 — naïve"}],"system":"You are a helpful assistant — résumé","stream":false,"temperature":0.7}'

echo -n "$TEST_BODY_B" > "$TMPDIR/b_body.json"

COUNT_BEFORE_B=$(count_bodies)

# Control POST (Anthropic path, no gzip)
CTRL_HTTP_B=$(curl -s -o "$TMPDIR/b_ctrl_body" -D "$TMPDIR/b_ctrl_hdr" \
    -w "%{http_code}" --max-time 20 \
    -X POST "http://127.0.0.1:$PROXY_PORT/v1/messages" \
    -H "x-api-key: $API_KEY" \
    -H "anthropic-version: 2023-06-01" \
    -H "Content-Type: application/json" \
    --data-binary "@$TMPDIR/b_body.json")
echo "  control status=$CTRL_HTTP_B"

# Gzip + Content-Encoding
gzip -c "$TMPDIR/b_body.json" > "$TMPDIR/b_body.json.gz"
GZ_HTTP_B=$(curl -s -o "$TMPDIR/b_gz_body" -D "$TMPDIR/b_gz_hdr" \
    -w "%{http_code}" --max-time 20 \
    -X POST "http://127.0.0.1:$PROXY_PORT/v1/messages" \
    -H "x-api-key: $API_KEY" \
    -H "anthropic-version: 2023-06-01" \
    -H "Content-Type: application/json" \
    -H "Content-Encoding: gzip" \
    --data-binary "@$TMPDIR/b_body.json.gz")
echo "  gzip status=$GZ_HTTP_B"

COUNT_AFTER_B=$(count_bodies)

B_BODIES=($(ls -1 "$RECORD_DIR"/body_*.bin 2>/dev/null | sort))
NUM_B=${#B_BODIES[@]}
B_BODY_CTRL="${B_BODIES[$NUM_B-2]:-}"
B_BODY_GZ="${B_BODIES[$NUM_B-1]:-}"
# FIX (harness): mock writes hdr_N.txt, not body_N.txt — derive correctly.
B_HDR_CTRL="${B_BODY_CTRL/body_/hdr_}"
B_HDR_CTRL="${B_HDR_CTRL/.bin/.txt}"
B_HDR_GZ="${B_BODY_GZ/body_/hdr_}"
B_HDR_GZ="${B_HDR_GZ/.bin/.txt}"

EVID_B=""
OK_B=true

# (b1) both client responses 200
if [ "$CTRL_HTTP_B" != "200" ] || [ "$GZ_HTTP_B" != "200" ]; then
    OK_B=false; EVID_B="$EVID_B http=ctrl:$CTRL_HTTP_B/gz:$GZ_HTTP_B"
else
    EVID_B="$EVID_B http=200/200"
fi

# (b2) client response bodies byte-identical — INFORMATIONAL ONLY for scenario b.
# The spec ("same four assertions as (a) on the translated upstream bytes") applies the
# byte-equality check to UPSTREAM bytes (covered by b3 below), not client bytes. The
# Anthropic translator generates per-request `msg_` IDs that legitimately differ between
# control and gzip+CE requests with semantically identical content (visible in the
# cmp-l-head diff below). Recording the observation here is still useful for debugging,
# but it does NOT fail scenario b — the gzip middleware assertions that matter are b1
# (both 200), b3 (upstream bodies byte-identical), b4 (no Content-Encoding leaked), and
# b6 (upstream-growth=2).
if cmp -s "$TMPDIR/b_ctrl_body" "$TMPDIR/b_gz_body"; then
    SZ=$(wc -c < "$TMPDIR/b_ctrl_body" | tr -d ' ')
    EVID_B="$EVID_B client-body=cmp-ok($SZ B,Anthropic)"
else
    # Informational: report the diff location, but do NOT set OK_B=false.
    B_DIFF=$(cmp -l "$TMPDIR/b_ctrl_body" "$TMPDIR/b_gz_body" 2>/dev/null | head -3 | tr '\n' '|')
    EVID_B="$EVID_B client-body=DIFF(informational,Anthropic-msg-id-differ:$B_DIFF)"
fi

# (b3) stub-recorded upstream bodies byte-identical (translated OpenAI shape)
if [ -n "$B_BODY_CTRL" ] && [ -n "$B_BODY_GZ" ] && [ -f "$B_BODY_CTRL" ] && [ -f "$B_BODY_GZ" ]; then
    if cmp -s "$B_BODY_CTRL" "$B_BODY_GZ"; then
        SZ=$(wc -c < "$B_BODY_CTRL" | tr -d ' ')
        EVID_B="$EVID_B upstream-body=cmp-ok($SZ B)"
    else
        OK_B=false
        EVID_B="$EVID_B upstream-body=DIFF ctrl=$(basename "$B_BODY_CTRL") gz=$(basename "$B_BODY_GZ")"
        EVID_B="$EVID_B cmp-l-head:$(cmp -l "$B_BODY_CTRL" "$B_BODY_GZ" 2>/dev/null | head -5 | tr '\n' '|')"
    fi
else
    OK_B=false; EVID_B="$EVID_B upstream-body=missing"
fi

# (b4) stub saw NO Content-Encoding header on either upstream request
if [ -f "$B_HDR_CTRL" ] && [ -f "$B_HDR_GZ" ]; then
    if grep -qi '^Content-Encoding' "$B_HDR_CTRL" || grep -qi '^Content-Encoding' "$B_HDR_GZ"; then
        OK_B=false; EVID_B="$EVID_B stub-saw-Content-Encoding"
    else
        EVID_B="$EVID_B stub-no-content-encoding"
    fi
else
    OK_B=false; EVID_B="$EVID_B stub-hdr-missing"
fi

# (b5) client response carries NO Content-Encoding header
if grep -qi '^Content-Encoding' "$TMPDIR/b_ctrl_hdr" || grep -qi '^Content-Encoding' "$TMPDIR/b_gz_hdr"; then
    OK_B=false; EVID_B="$EVID_B client-resp-has-Content-Encoding"
else
    EVID_B="$EVID_B client-resp-no-content-encoding"
fi

# (b6) upstream call count grew by exactly 2
GROWTH_B=$((COUNT_AFTER_B - COUNT_BEFORE_B))
if [ "$GROWTH_B" -ne 2 ]; then
    OK_B=false; EVID_B="$EVID_B upstream-growth=$GROWTH_B(expect 2)"
else
    EVID_B="$EVID_B upstream-growth=2"
fi

if [ "$OK_B" = true ]; then
    print_scenario "b" "PASS" "$EVID_B"
else
    RES_B="FAIL"; print_scenario "b" "FAIL" "$EVID_B"
fi

# ============================================================
# SCENARIO c: gzip body WITHOUT Content-Encoding header → 4xx
# ============================================================
echo ""
echo "${BLUE}[c] gzip body WITHOUT Content-Encoding header → 4xx (feature NOT triggered)${NC}"

# Send a gzip-compressed JSON with NO Content-Encoding header. The middleware
# must NOT trigger (it only activates on Content-Encoding: gzip). The body
# arrives at the handler as raw gzip bytes; the handler tries to parse them
# as JSON → JSON parse error → 4xx. Upstream must NOT be called.
TEST_BODY_C='{"model":"mock-gzip-model","messages":[{"role":"user","content":"this JSON is fine"}],"stream":false}'
# FIX (harness): write body to a regular file first — `<(echo ...)` produces a fifo
# on macOS that newer gzip rejects ("/dev/fd/63 is not a regular file"), causing
# gzip to emit a 0-byte file. The scenario still passes incidentally (proxy rejects
# non-JSON bytes), but using a regular file is the correct mechanical approach.
echo -n "$TEST_BODY_C" > "$TMPDIR/c_body.json"
gzip -c "$TMPDIR/c_body.json" > "$TMPDIR/c_body.json.gz"

COUNT_BEFORE_C=$(count_bodies)

C_HTTP=$(curl -s -o "$TMPDIR/c_body" -D "$TMPDIR/c_hdr" \
    -w "%{http_code}" --max-time 20 \
    -X POST "http://127.0.0.1:$PROXY_PORT/v1/chat/completions" \
    -H "Authorization: Bearer $API_KEY" \
    -H "Content-Type: application/json" \
    --data-binary "@$TMPDIR/c_body.json.gz")
echo "  status=$C_HTTP"

COUNT_AFTER_C=$(count_bodies)

EVID_C=""
OK_C=true

# (c1) status is 4xx (JSON parse failure)
case "$C_HTTP" in
    4??)
        EVID_C="$EVID_C http=$C_HTTP(4xx)"
        ;;
    200)
        OK_C=false; EVID_C="$EVID_C http=200(want 4xx)"
        ;;
    5??)
        OK_C=false; EVID_C="$EVID_C http=$C_HTTP(5xx-not-allowed)"
        ;;
    *)
        OK_C=false; EVID_C="$EVID_C http=$C_HTTP(unexpected)"
        ;;
esac

# (c2) upstream call count unchanged
GROWTH_C=$((COUNT_AFTER_C - COUNT_BEFORE_C))
if [ "$GROWTH_C" -ne 0 ]; then
    OK_C=false; EVID_C="$EVID_C upstream-growth=$GROWTH_C(expect 0)"
else
    EVID_C="$EVID_C upstream-growth=0"
fi

if [ "$OK_C" = true ]; then
    print_scenario "c" "PASS" "$EVID_C"
else
    RES_C="FAIL"; print_scenario "c" "FAIL" "$EVID_C"
fi

# ============================================================
# SCENARIO d: corrupt gzip + Content-Encoding: gzip → 400
# ============================================================
echo ""
echo "${BLUE}[d] corrupt gzip + Content-Encoding: gzip → 400${NC}"

# Send raw bytes that are NOT a valid gzip stream, with the header. The
# middleware's gzip.NewReader will fail → 400 invalid_gzip_body. Upstream
# must NOT be called.
echo -n "definitely not gzip — just plain text bytes" > "$TMPDIR/d_corrupt.bin"

COUNT_BEFORE_D=$(count_bodies)

D_HTTP=$(curl -s -o "$TMPDIR/d_body" -D "$TMPDIR/d_hdr" \
    -w "%{http_code}" --max-time 20 \
    -X POST "http://127.0.0.1:$PROXY_PORT/v1/chat/completions" \
    -H "Authorization: Bearer $API_KEY" \
    -H "Content-Type: application/json" \
    -H "Content-Encoding: gzip" \
    --data-binary "@$TMPDIR/d_corrupt.bin")
echo "  status=$D_HTTP"

COUNT_AFTER_D=$(count_bodies)

EVID_D=""
OK_D=true

# (d1) status is 400
if [ "$D_HTTP" = "400" ]; then
    EVID_D="$EVID_D http=400"
else
    OK_D=false; EVID_D="$EVID_D http=$D_HTTP(want 400)"
fi

# (d2) upstream call count unchanged
GROWTH_D=$((COUNT_AFTER_D - COUNT_BEFORE_D))
if [ "$GROWTH_D" -ne 0 ]; then
    OK_D=false; EVID_D="$EVID_D upstream-growth=$GROWTH_D(expect 0)"
else
    EVID_D="$EVID_D upstream-growth=0"
fi

if [ "$OK_D" = true ]; then
    print_scenario "d" "PASS" "$EVID_D"
else
    RES_D="FAIL"; print_scenario "d" "FAIL" "$EVID_D"
fi

# ============================================================
# SCENARIO e: zip bomb (150 MiB zeros gzip'd) → 413
#   gzipmw cap = MaxDecompressedBytes = 100*1024*1024 = 104857600 B (100 MiB)
# ============================================================
echo ""
echo "${BLUE}[e] zip bomb (150 MiB zeros gzip'd) + Content-Encoding: gzip → 413${NC}"

# Build bomb: 150 MiB of zeros piped through gzip → ~150 KB file.
# The middleware reads at most MaxDecompressedBytes+1 bytes from the gzip
# stream, detects the overflow (>100 MiB cap), returns 413.
# Use explicit 1048576-byte blocks for portability across BSD/GNU dd.
# Use gzip -9 (max compression) so the compressed output is ~150 KB.
echo "${YELLOW}  building 150 MiB bomb (~150 KB compressed)...${NC}"
dd if=/dev/zero bs=1048576 count=150 2>/dev/null | gzip -9 > "$TMPDIR/e_bomb.gz"
BOMB_SZ=$(wc -c < "$TMPDIR/e_bomb.gz" | tr -d ' ')
echo "  bomb.gz size = $BOMB_SZ B (target ~150 KB)"

COUNT_BEFORE_E=$(count_bodies)

# Send with Content-Encoding: gzip + accurate Content-Length. Use --max-time
# 60s to allow plenty of room under the 240s internal alarm.
START_E=$(date +%s)
E_HTTP=$(curl -s -o "$TMPDIR/e_body" -D "$TMPDIR/e_hdr" \
    -w "%{http_code}" --max-time 60 \
    -X POST "http://127.0.0.1:$PROXY_PORT/v1/chat/completions" \
    -H "Authorization: Bearer $API_KEY" \
    -H "Content-Type: application/json" \
    -H "Content-Encoding: gzip" \
    --data-binary "@$TMPDIR/e_bomb.gz")
END_E=$(date +%s)
DUR_E=$((END_E - START_E))
echo "  status=$E_HTTP (resolved in ${DUR_E}s)"

COUNT_AFTER_E=$(count_bodies)

EVID_E=""
OK_E=true

# (e1) status is 413 (per spec). If different, record actual vs expected.
if [ "$E_HTTP" = "413" ]; then
    EVID_E="$EVID_E http=413(expected)"
elif [ "$E_HTTP" = "400" ]; then
    # The middleware's compressed-side LimitReader can also surface as 400
    # (ErrUnexpectedEOF) when the gzip stream is truncated mid-record —
    # per pkg/middleware/gzipmw/gzip.go:240-243 this is documented behavior.
    OK_E=false
    EVID_E="$EVID_E http=400(actual vs expected 413 — see pkg/middleware/gzipmw/gzip.go:240-243)"
else
    OK_E=false; EVID_E="$EVID_E http=$E_HTTP(want 413)"
fi

# (e2) bomb request resolved in <30s
if [ "$DUR_E" -lt 30 ]; then
    EVID_E="$EVID_E duration=${DUR_E}s(<30s)"
else
    OK_E=false; EVID_E="$EVID_E duration=${DUR_E}s(>=30s)"
fi

# (e3) upstream call count unchanged (bomb rejected before handler/upstream)
GROWTH_E=$((COUNT_AFTER_E - COUNT_BEFORE_E))
if [ "$GROWTH_E" -ne 0 ]; then
    OK_E=false; EVID_E="$EVID_E upstream-growth=$GROWTH_E(expect 0)"
else
    EVID_E="$EVID_E upstream-growth=0"
fi

# (e4) liveness: /healthz 200
HZ_HTTP=$(curl -s -o /dev/null -w "%{http_code}" --max-time 10 \
    "http://127.0.0.1:$PROXY_PORT/healthz")
if [ "$HZ_HTTP" = "200" ]; then
    EVID_E="$EVID_E post-bomb-healthz=200"
else
    OK_E=false; EVID_E="$EVID_E post-bomb-healthz=$HZ_HTTP"
fi

# (e5) liveness: one normal control request → 200
LIVE_BODY='{"model":"mock-gzip-model","messages":[{"role":"user","content":"alive check"}],"stream":false}'
echo -n "$LIVE_BODY" > "$TMPDIR/e_live.json"
LIVE_HTTP=$(curl -s -o /dev/null -w "%{http_code}" --max-time 10 \
    -X POST "http://127.0.0.1:$PROXY_PORT/v1/chat/completions" \
    -H "Authorization: Bearer $API_KEY" \
    -H "Content-Type: application/json" \
    --data-binary "@$TMPDIR/e_live.json")
if [ "$LIVE_HTTP" = "200" ]; then
    EVID_E="$EVID_E post-bomb-control=200"
else
    OK_E=false; EVID_E="$EVID_E post-bomb-control=$LIVE_HTTP"
fi

if [ "$OK_E" = true ]; then
    print_scenario "e" "PASS" "$EVID_E"
else
    RES_E="FAIL"; print_scenario "e" "FAIL" "$EVID_E"
fi

# ============================================================
# SCENARIO f: passthrough untouched (no header anywhere)
# ============================================================
echo ""
echo "${BLUE}[f] passthrough untouched — healthz + config + SSE events${NC}"

EVID_F=""
OK_F=true

# (f1) GET /healthz → 200
HZ2_HTTP=$(curl -s -o /dev/null -w "%{http_code}" --max-time 10 \
    "http://127.0.0.1:$PROXY_PORT/healthz")
if [ "$HZ2_HTTP" = "200" ]; then
    EVID_F="$EVID_F healthz=200"
else
    OK_F=false; EVID_F="$EVID_F healthz=$HZ2_HTTP"
fi

# (f2) GET /fe/api/config → 200
CFG_HTTP=$(curl -s -o /dev/null -w "%{http_code}" --max-time 10 \
    "http://127.0.0.1:$PROXY_PORT/fe/api/config")
if [ "$CFG_HTTP" = "200" ]; then
    EVID_F="$EVID_F config=200"
else
    OK_F=false; EVID_F="$EVID_F config=$CFG_HTTP"
fi

# (f3) /fe/api/events: open SSE in background, trigger an event (config PUT
#       publishes a "config.updated" event on the bus → SSE handler emits
#       "data: {...}\n\n" within ms), wait up to 20s for any byte.
#       The proxy's SSE heartbeat is 30s (pkg/ui/server.go:366); the spec
#       writes 15s but the actual code is 30s — we work around by triggering
#       an event so the byte arrives immediately, not waiting for heartbeat.
SSE_OUT="$TMPDIR/sse_out.bin"
: > "$SSE_OUT"

# Open SSE in background. Capture bytes as they arrive.
( curl -s -N --max-time 20 "http://127.0.0.1:$PROXY_PORT/fe/api/events" > "$SSE_OUT" 2>/dev/null ) &
SSE_PID=$!

# Give curl a moment to establish the connection
sleep 0.5

# Trigger an event by PUTting the current config back (this publishes
# config.updated on the bus, which the SSE handler forwards).
# Re-PUT exact same content — server validates and emits the event.
SSE_CFG_BODY=$(curl -s "http://127.0.0.1:$PROXY_PORT/fe/api/config")
echo "$SSE_CFG_BODY" | curl -s -o /dev/null -w "%{http_code}" --max-time 10 \
    -X PUT "http://127.0.0.1:$PROXY_PORT/fe/api/config" \
    -H "Content-Type: application/json" \
    --data-binary @- > "$TMPDIR/sse_cfg_put_status"
SSE_PUT_HTTP=$(cat "$TMPDIR/sse_cfg_put_status")

# Wait for SSE curl to finish (max-time 20s, plus a small grace)
wait "$SSE_PID" 2>/dev/null || true
SSE_BYTES=$(wc -c < "$SSE_OUT" | tr -d ' ')
SSE_HEAD=$(head -c 200 "$SSE_OUT" | tr '\n' '|' | tr -d '\r')

# PASS if received ≥1 byte
if [ "$SSE_BYTES" -ge 1 ]; then
    EVID_F="$EVID_F sse-bytes=$SSE_BYTES put=$SSE_PUT_HTTP head='$SSE_HEAD'"
else
    OK_F=false; EVID_F="$EVID_F sse-bytes=0 put=$SSE_PUT_HTTP FAIL-no-byte-within-20s"
fi

if [ "$OK_F" = true ]; then
    print_scenario "f" "PASS" "$EVID_F"
else
    RES_F="FAIL"; print_scenario "f" "FAIL" "$EVID_F"
fi

# ============================================================
# Final result
# ============================================================
echo ""
if [ "$RES_A" = "PASS" ] && [ "$RES_B" = "PASS" ] && [ "$RES_C" = "PASS" ] && \
   [ "$RES_D" = "PASS" ] && [ "$RES_E" = "PASS" ] && [ "$RES_F" = "PASS" ]; then
    echo "${GREEN}RESULT: PASS${NC}"
else
    echo "${RED}RESULT: FAIL${NC} (a=$RES_A b=$RES_B c=$RES_C d=$RES_D e=$RES_E f=$RES_F)"
    # Note: do NOT exit non-zero from the cleanup trap; cleanup still runs.
    # The dispatcher reads the RESULT line.
fi
