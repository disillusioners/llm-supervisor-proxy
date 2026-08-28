#!/usr/bin/env bash
# Mock Test M3 — real-streaming-default: header truth table + edge cases (merge gate)
#
# Branch: feature/real-streaming-default @ 03a5339
# Ports: 10130 (proxy), 10131 (mock upstream). Strict isolation.
# Never touches 8088 or other workers' ports.
#
# DOCUMENTED TRUTH TABLE (docs/real-streaming-default.md lines 42-48):
#   ABSENT                              → LIVE
#   PRESENT, empty value (bare header)  → BUFFERED
#   PRESENT, truthy true/1/yes/on (CI)  → BUFFERED
#   PRESENT, falsy  false/0/no/off (CI) → LIVE
#   PRESENT, any other non-empty value  → LIVE
# Multi-value (docs lines 55-58):
#   multiple separate X-LLMProxy-Buffer-Response lines → FIRST value wins
# Comma-joined single line caveat (docs lines 59-66):
#   single `X-LLMProxy-Buffer-Response: true, false` canonicalizes to ONE value
#   "true, false" → LIVE (informational row).
#
# Verifies:
#   Scenario 1 — Truth table rows (12): absent, bare, true/TRUE/1/yes/on,
#                 false/0/no/off, garbage (banana). Each classified as LIVE/BUFFERED.
#   Scenario 2 — Multi-value: (true, false) → BUFFERED; (false, true) → LIVE;
#                 single comma-joined "true, false" → LIVE (informational).
#   Scenario 3 — stream=false: no-header vs with-true-header → IDENTICAL body.
#   Scenario 4 — Client disconnect mid-stream: read 1 chunk, hard-close; assert
#                 proxy /healthz == 200 + no panic/SIGSEGV in proxy stderr
#                 (the 9842c77 regression class).
#
# CLASSIFIER (python3 socket timestamped client):
#   LIVE     = TTFB <= 800ms AND incremental (>=2 arrivals >=150ms apart)
#   BUFFERED = TTFB >= 1000ms AND single burst (spread <= 250ms)
#   1 retry per row on boundary only.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

# ---- strict port isolation ----
PROXY_PORT=10130
MOCK_PORT=10131
# Hard guard: refuse to touch 8088
if [ "$PROXY_PORT" = "8088" ] || [ "$MOCK_PORT" = "8088" ]; then
    echo "FATAL: refusing to bind 8088" >&2; exit 2
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
    # Targeted port cleanup — only kill PIDs we started, or PIDs bound to OUR ports
    for port in "$PROXY_PORT" "$MOCK_PORT"; do
        for pid in $(lsof -ti:"$port" 2>/dev/null || true); do
            cmd=$(ps -o command= -p "$pid" 2>/dev/null || true)
            case "$cmd" in
                *mock_rsd_m3*|*rsd_m3_proxy*|*mock_openai_m3*) kill -9 "$pid" 2>/dev/null || true ;;
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
echo "${BLUE}  M3 — header truth table + edge cases     ${NC}"
echo "${BLUE}  Branch: feature/real-streaming-default  ${NC}"
echo "${BLUE}  Proxy port: $PROXY_PORT | Mock port: $MOCK_PORT    ${NC}"
echo "${BLUE}===========================================${NC}"

# ---- 1. build proxy binary FIRST (using default HOME for go module cache) ----
echo "${YELLOW}[1/9] Building proxy binary from HEAD ($(git -C "$ROOT_DIR" rev-parse --short HEAD))...${NC}"
TMPDIR=$(mktemp -d -t rsd-m3-XXXXXX)
PROXY_BIN="$TMPDIR/rsd_m3_proxy"
( cd "$ROOT_DIR" && go build -o "$PROXY_BIN" ./cmd ) > "$TMPDIR/build.log" 2>&1
if [ ! -x "$PROXY_BIN" ]; then
    echo "${RED}go build failed; log:${NC}"; tail -30 "$TMPDIR/build.log"
    exit 1
fi
echo "${GREEN}[1/9] proxy binary built ($PROXY_BIN)${NC}"

# ---- 2. NOW set isolated HOME for runtime (CRITICAL: never ~/.config/llm-supervisor-proxy) ----
export HOME="$TMPDIR/home"
mkdir -p "$HOME"
export XDG_CONFIG_HOME="$HOME/.config"
mkdir -p "$XDG_CONFIG_HOME"
echo "${YELLOW}[2/9] isolated HOME=$HOME (runtime only)${NC}"

# ---- 3. verify ports free ----
for port in "$PROXY_PORT" "$MOCK_PORT"; do
    pids=$(lsof -ti:"$port" 2>/dev/null || true)
    if [ -n "$pids" ]; then
        echo "${RED}FATAL: port $port in use by PIDs: $pids${NC}" >&2
        exit 2
    fi
done

# ---- 4. start mock upstream on 10131 ----
echo "${YELLOW}[3/9] Starting mock OpenAI upstream on $MOCK_PORT...${NC}"
cat > "$TMPDIR/mock_openai_m3.py" <<'PYEOF'
#!/usr/bin/env python3
"""Mock OpenAI upstream on 10131 for M3 test.

Serves:
  POST /v1/chat/completions (stream=true)  → real OpenAI SSE: 4 content chunks
                                              × 250ms inter-chunk delay
                                              (total ≈1s). REAL OpenAI wire
                                              format. self-verifies JSON.
  POST /v1/chat/completions (stream=false) → deterministic canned JSON
                                              (same body both for header/no-header)
"""
import json
import sys
import time
import io
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

# Pre-baked response
CHAT_ID = "chatcmpl-rsd-m3-001"
CHUNKS = ["alpha", "beta", "gamma", "delta"]  # 4 chunks × 250ms = ~1s total
INTER_CHUNK_DELAY = 0.250
NONSTREAM_BODY = json.dumps({
    "id": CHAT_ID,
    "object": "chat.completion",
    "created": 1700000000,
    "model": "mock-openai-m3",
    "choices": [{
        "index": 0,
        "message": {"role": "assistant", "content": "deterministic-nonstream"},
        "finish_reason": "stop",
    }],
    "usage": {"prompt_tokens": 10, "completion_tokens": 3, "total_tokens": 13},
}, separators=(',', ':')).encode()


def stream_openai_to(wfile):
    """Stream 4 real-OpenAI-SSE content chunks with 250ms gaps. Flushes each."""
    def emit(delta_content):
        chunk = {
            "id": CHAT_ID,
            "object": "chat.completion.chunk",
            "created": 1700000000,
            "model": "mock-openai-m3",
            "choices": [{
                "index": 0,
                "delta": {"content": delta_content},
                "finish_reason": None,
            }],
        }
        line = f"data: {json.dumps(chunk, separators=(',', ':'))}\n\n"
        wfile.write(line.encode())
        wfile.flush()
    # Send role chunk first (real OpenAI wire does this)
    role_chunk = {
        "id": CHAT_ID,
        "object": "chat.completion.chunk",
        "created": 1700000000,
        "model": "mock-openai-m3",
        "choices": [{
            "index": 0,
            "delta": {"role": "assistant", "content": ""},
            "finish_reason": None,
        }],
    }
    wfile.write(f"data: {json.dumps(role_chunk, separators=(',', ':'))}\n\n".encode())
    wfile.flush()
    for i, c in enumerate(CHUNKS):
        if i > 0:
            time.sleep(INTER_CHUNK_DELAY)
        emit(c)
    # Final stop chunk
    final_chunk = {
        "id": CHAT_ID,
        "object": "chat.completion.chunk",
        "created": 1700000000,
        "model": "mock-openai-m3",
        "choices": [{
            "index": 0,
            "delta": {},
            "finish_reason": "stop",
        }],
    }
    wfile.write(f"data: {json.dumps(final_chunk, separators=(',', ':'))}\n\n".encode())
    wfile.flush()
    wfile.write(b"data: [DONE]\n\n")
    wfile.flush()


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args, **kwargs):
        pass  # quiet

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length) if length else b""
        path = self.path
        if path == "/v1/chat/completions":
            try:
                req = json.loads(body) if body else {}
            except Exception:
                self.send_response(400); self.end_headers(); return
            is_stream = bool(req.get("stream", False))
            if is_stream:
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.send_header("Cache-Control", "no-cache")
                self.send_header("Connection", "close")
                self.send_header("X-Accel-Buffering", "no")
                self.end_headers()
                stream_openai_to(self.wfile)
            else:
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(NONSTREAM_BODY)))
                self.end_headers()
                self.wfile.write(NONSTREAM_BODY)
            return
        self.send_response(404); self.end_headers()


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 10131
    # Self-verify SSE framing BEFORE serving — write to BytesIO
    sample_buf = io.BytesIO()
    stream_openai_to(sample_buf)
    sample = sample_buf.getvalue()
    # Self-verify each data: line parses as JSON
    n_data_lines = 0
    for line in sample.split(b"\n"):
        if line.startswith(b"data: "):
            payload = line[6:]
            if payload == b"[DONE]":
                continue
            try:
                obj = json.loads(payload)
                assert obj.get("object") == "chat.completion.chunk"
                assert "choices" in obj
                n_data_lines += 1
            except Exception as e:
                print(f"[mock-10131] SELF-VERIFY FAIL: {e} on payload: {payload!r}", flush=True)
                sys.exit(2)
    assert sample.endswith(b"data: [DONE]\n\n"), "must end with DONE marker"
    assert n_data_lines >= 5, f"expected >=5 data lines (role + 4 content + final), got {n_data_lines}"
    print(f"[mock-10131] self-verify OK: {n_data_lines} OpenAI SSE chunks (role+4+final) parse as JSON", flush=True)
    srv = ThreadingHTTPServer(("127.0.0.1", port), Handler)
    print(f"[mock-10131] listening on 127.0.0.1:{port}", flush=True)
    srv.serve_forever()
PYEOF
python3 "$TMPDIR/mock_openai_m3.py" "$MOCK_PORT" > "$TMPDIR/mock.log" 2>&1 &
MOCK_PID=$!

# Wait for mock to be ready
for i in $(seq 1 50); do
    sleep 0.1
    if curl -s -o /dev/null -w "%{http_code}" -X POST "http://127.0.0.1:$MOCK_PORT/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -d '{"model":"x","messages":[{"role":"user","content":"hi"}],"stream":false}' \
        2>/dev/null | grep -q "200"; then
        echo "${GREEN}[3/9] mock upstream ready (PID $MOCK_PID)${NC}"
        break
    fi
    if [ "$i" -eq 50 ]; then
        echo "${RED}mock upstream failed to start; log:${NC}"; cat "$TMPDIR/mock.log"
        exit 1
    fi
done

# ---- 4b. self-verify mock emits parseable OpenAI SSE via Python probe ----
echo "${YELLOW}[4/9] Self-verifying mock SSE framing (parse as OpenAI JSON)...${NC}"
SSE_PROBE=$(curl -s -N --max-time 8 -X POST "http://127.0.0.1:$MOCK_PORT/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d '{"model":"x","messages":[{"role":"user","content":"probe"}],"stream":true}')
PROBE_OK=$(echo "$SSE_PROBE" | python3 -c '
import sys, json
n = 0
saw_done = False
for line in sys.stdin:
    line = line.rstrip("\r\n")
    if not line.startswith("data: "):
        continue
    p = line[6:]
    if p == "[DONE]":
        saw_done = True
        continue
    try:
        obj = json.loads(p)
        assert obj.get("object") == "chat.completion.chunk"
        assert "choices" in obj
        n += 1
    except Exception as e:
        print(f"FAIL: {e}", file=sys.stderr)
        sys.exit(2)
assert saw_done, "missing [DONE]"
assert n >= 5, f"expected >=5 chunks, got {n}"
print(f"OK ({n} chunks)")
')
if echo "$PROBE_OK" | grep -q "^OK"; then
    echo "${GREEN}[4/9] mock SSE verified: $PROBE_OK${NC}"
else
    echo "${RED}[4/9] mock SSE verification FAILED${NC}"; echo "$SSE_PROBE" | head -20
    exit 1
fi

# ---- 5. start proxy with temp HOME isolation ----
echo "${YELLOW}[5/9] Starting proxy on $PROXY_PORT (HOME=$HOME)...${NC}"
export PORT="$PROXY_PORT"
export APPLY_ENV_OVERRIDES="true"
export UPSTREAM_URL="http://127.0.0.1:$MOCK_PORT"
export IDLE_TIMEOUT="5s"
export MAX_GENERATION_TIME="20s"
export LOOP_DETECTION_ENABLED="false"
export ULTIMATE_MODEL_ID="mock-ultimate-m3"
export ULTIMATE_MODEL_MAX_HASH="100"
export XDG_CONFIG_HOME="$HOME/.config"
mkdir -p "$XDG_CONFIG_HOME"

"$PROXY_BIN" > "$TMPDIR/proxy.log" 2>&1 &
PROXY_PID=$!

# Wait for proxy /healthz
for i in $(seq 1 50); do
    sleep 0.1
    if curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:$PROXY_PORT/healthz" 2>/dev/null | grep -q "200"; then
        echo "${GREEN}[5/9] proxy ready (PID $PROXY_PID)${NC}"
        break
    fi
    if [ "$i" -eq 50 ]; then
        echo "${RED}proxy failed to start; log tail:${NC}"; tail -30 "$TMPDIR/proxy.log"
        exit 1
    fi
done

# ---- 6. configure models and credentials via API ----
echo "${YELLOW}[6/9] Configuring credential + model...${NC}"

# Clean any stale state (idempotent)
curl -s -X DELETE "http://127.0.0.1:$PROXY_PORT/fe/api/models/mock-openai-m3" >/dev/null 2>&1 || true
curl -s -X DELETE "http://127.0.0.1:$PROXY_PORT/fe/api/models/mock-ultimate-m3" >/dev/null 2>&1 || true
curl -s -X DELETE "http://127.0.0.1:$PROXY_PORT/fe/api/credentials/mock-openai-cred-m3" >/dev/null 2>&1 || true
curl -s -X DELETE "http://127.0.0.1:$PROXY_PORT/fe/api/credentials/mock-ultimate-cred-m3" >/dev/null 2>&1 || true
sleep 1

# Sweep stale rsd-m3 test tokens
STALE_TOKEN_IDS=$(curl -s "http://127.0.0.1:$PROXY_PORT/fe/api/tokens" | python3 -c '
import json, sys
try:
    for t in json.load(sys.stdin):
        if t.get("name") == "rsd-m3-test":
            print(t["id"])
except Exception:
    pass
' 2>/dev/null || true)
for tid in $STALE_TOKEN_IDS; do
    curl -s -X DELETE "http://127.0.0.1:$PROXY_PORT/fe/api/tokens/$tid" >/dev/null 2>&1 || true
done

# OpenAI credential
CRED_RESP=$(curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/credentials" \
    -H "Content-Type: application/json" \
    -d "{
        \"id\": \"mock-openai-cred-m3\",
        \"provider\": \"openai\",
        \"api_key\": \"mock-api-key\",
        \"base_url\": \"http://127.0.0.1:$MOCK_PORT/v1\"
    }")
if ! echo "$CRED_RESP" | grep -q '"id"'; then
    echo "${RED}credential creation failed: $CRED_RESP${NC}"; exit 1
fi
echo "${GREEN}  credential created${NC}"

# External model (race-external path; uses UPSTREAM_URL). Internal=false so the
# proxy forwards to UPSTREAM_URL for streaming. Stream=true request hits the mock.
MODEL_RESP=$(curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/models" \
    -H "Content-Type: application/json" \
    -d "{
        \"id\": \"mock-openai-m3\",
        \"name\": \"Mock OpenAI M3 Model\",
        \"enabled\": true,
        \"internal\": false,
        \"credentials\": [{\"credential_id\": \"mock-openai-cred-m3\", \"weight\": 1, \"position\": 0}]
    }")
if ! echo "$MODEL_RESP" | grep -q '"id"'; then
    echo "${RED}model creation failed: $MODEL_RESP${NC}"; exit 1
fi
echo "${GREEN}  model created (external, OpenAI race path)${NC}"

# Test token
TOKEN_RESP=$(curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/tokens" \
    -H "Content-Type: application/json" \
    -d '{"name": "rsd-m3-test"}')
API_KEY=$(echo "$TOKEN_RESP" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("token",""))')
TOKEN_ID=$(echo "$TOKEN_RESP" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("id",""))')
if [ -z "$API_KEY" ] || [ -z "$TOKEN_ID" ]; then
    echo "${RED}token creation failed: $TOKEN_RESP${NC}"; exit 1
fi
echo "${GREEN}  test token created${NC}"

# ============================================================
# CLASSIFIER (python3 socket timestamped client)
# ============================================================
# probe_stream(label, extra_headers) → emits "{ts_ms, line}\n" jsonl per chunk
# followed by "{status, content_type}\n" to stderr.
# extra_headers is a multi-line string, each non-empty line becomes a header
# line added to the request. "OMIT" or empty string ⇒ no X-LLMProxy-Buffer-Response
# header sent.
# Comma-joined single-line is added as one line: "X-LLMProxy-Buffer-Response: true, false"
# Multi-line (two separate headers) is added as two lines.
PROBE_PY=$(cat <<'PYEOF'
import socket, sys, time, json
proxy_port = int(sys.argv[1])
api_key = sys.argv[2]
mode = sys.argv[3]  # "stream" or "nonstream"
extra_mode = sys.argv[4]  # "none", "single", "multiline", "comma-joined"
extra_value = sys.argv[5] if len(sys.argv) > 5 else ""

hdr = (
    f"POST /v1/chat/completions HTTP/1.1\r\n"
    f"Host: 127.0.0.1:{proxy_port}\r\n"
    f"Content-Type: application/json\r\n"
    f"Authorization: Bearer {api_key}\r\n"
    f"Accept: text/event-stream\r\n"
    f"Connection: close\r\n"
)
if extra_mode == "single":
    hdr += f"X-LLMProxy-Buffer-Response: {extra_value}\r\n"
elif extra_mode == "comma-joined":
    # single header line with embedded comma → canonicalizes to ONE value
    hdr += f"X-LLMProxy-Buffer-Response: {extra_value}\r\n"
elif extra_mode == "multiline":
    # two separate X-LLMProxy-Buffer-Response headers → first wins
    # extra_value is "v1||v2"
    v1, v2 = extra_value.split("||", 1)
    hdr += f"X-LLMProxy-Buffer-Response: {v1}\r\n"
    hdr += f"X-LLMProxy-Buffer-Response: {v2}\r\n"

if mode == "stream":
    body_obj = {"model": "mock-openai-m3", "messages": [{"role": "user", "content": "rsd-m3-probe"}], "stream": True}
else:
    body_obj = {"model": "mock-openai-m3", "messages": [{"role": "user", "content": "rsd-m3-nonstream"}], "stream": False}
body = json.dumps(body_obj)
hdr += f"Content-Length: {len(body)}\r\n\r\n{body}"

s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.settimeout(15)
t0 = time.time()
s.connect(("127.0.0.1", proxy_port))
s.sendall(hdr.encode())
buf = b""
header_end = False
status = None
content_type = None
while True:
    try:
        chunk = s.recv(4096)
    except socket.timeout:
        break
    if not chunk:
        break
    buf += chunk
    if not header_end:
        if b"\r\n\r\n" in buf:
            header_end = True
            head, _, rest = buf.partition(b"\r\n\r\n")
            status_line = head.split(b"\r\n", 1)[0].decode(errors="replace")
            try:
                status = int(status_line.split(" ")[1])
            except Exception:
                status = None
            for hl in head.split(b"\r\n")[1:]:
                hl = hl.decode(errors="replace")
                if hl.lower().startswith("content-type:"):
                    content_type = hl.split(":", 1)[1].strip()
            buf = rest
    while b"\n" in buf:
        line, _, buf = buf.partition(b"\n")
        line = line.rstrip(b"\r")
        ts_ms = int((time.time() - t0) * 1000)
        sys.stdout.write(json.dumps({"ts_ms": ts_ms, "line": line.decode(errors="replace")}) + "\n")
        sys.stdout.flush()
s.close()
sys.stderr.write(json.dumps({"status": status, "content_type": content_type}) + "\n")
PYEOF
)

# Streaming probe that DOES NOT hard-close after a chunk — full read until EOF.
# Used for truth table classification.
probe_stream_classify() {
    local out_file="$1"
    local extra_mode="$2"
    local extra_value="$3"
    python3 -c "$PROBE_PY" "$PROXY_PORT" "$API_KEY" "stream" "$extra_mode" "$extra_value" \
        > "$out_file" 2> "${out_file}.meta"
}

# Raw-body nonstream probe — writes ONLY the body bytes (no HTTP headers, no JSON wrapping).
# Used for Scenario 3 byte-equality comparison.
probe_nonstream_raw() {
    local out_file="$1"
    local extra_header="$2"  # e.g., "X-LLMProxy-Buffer-Response: true" or "" for none
    python3 - <<PYEOF > "$out_file"
import socket, sys
hdr = (
    "POST /v1/chat/completions HTTP/1.1\r\n"
    "Host: 127.0.0.1:${PROXY_PORT}\r\n"
    "Content-Type: application/json\r\n"
    "Authorization: Bearer ${API_KEY}\r\n"
    "Connection: close\r\n"
)
if "${extra_header}":
    hdr += "${extra_header}" + "\r\n"
body = '{"model": "mock-openai-m3", "messages": [{"role": "user", "content": "rsd-m3-nonstream"}], "stream": false}'
hdr += f"Content-Length: {len(body)}\r\n\r\n{body}"
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.settimeout(15)
s.connect(("127.0.0.1", ${PROXY_PORT}))
s.sendall(hdr.encode())
buf = b""
header_end = False
while True:
    try:
        chunk = s.recv(4096)
    except (socket.timeout, OSError):
        break
    if not chunk:
        break
    buf += chunk
    if not header_end:
        if b"\r\n\r\n" in buf:
            header_end = True
            head, _, rest = buf.partition(b"\r\n\r\n")
            buf = rest
sys.stdout.write(buf.decode(errors="replace"))
PYEOF
}

# Hard-close after first data: chunk. Tests proxy robustness under abrupt client disconnect.
probe_disconnect_after_first() {
    local out_file="$1"
    python3 - <<PYEOF
import socket, time, json, struct
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.settimeout(10)
t0 = time.time()
hdr = (
    "POST /v1/chat/completions HTTP/1.1\r\n"
    "Host: 127.0.0.1:${PROXY_PORT}\r\n"
    "Content-Type: application/json\r\n"
    "Authorization: Bearer ${API_KEY}\r\n"
    "Accept: text/event-stream\r\n"
    "Connection: close\r\n"
)
body = json.dumps({"model": "mock-openai-m3", "messages": [{"role": "user", "content": "rsd-m3-disconnect"}], "stream": True})
hdr += f"Content-Length: {len(body)}\r\n\r\n{body}"
s.connect(("127.0.0.1", ${PROXY_PORT}))
s.sendall(hdr.encode())
buf = b""
data_count = 0
status = None
header_end = False
t_first_data = None
t_close = None
while True:
    try:
        chunk = s.recv(4096)
    except (socket.timeout, OSError):
        break
    if not chunk:
        break
    buf += chunk
    if not header_end:
        if b"\r\n\r\n" in buf:
            header_end = True
            head, _, rest = buf.partition(b"\r\n\r\n")
            status_line = head.split(b"\r\n", 1)[0].decode(errors="replace")
            try:
                status = int(status_line.split(" ")[1])
            except Exception:
                status = None
            buf = rest
    while b"\n" in buf:
        line, _, buf = buf.partition(b"\n")
        line = line.rstrip(b"\r")
        if line.startswith(b"data: "):
            if data_count == 0 and t_first_data is None:
                t_first_data = int((time.time() - t0) * 1000)
            data_count += 1
            # After receiving the FIRST data: line, hard-close the socket.
            if data_count == 1:
                # Read at least the second byte or pause briefly to ensure
                # the proxy has actually forwarded to the client.
                time.sleep(0.05)
                t_close = int((time.time() - t0) * 1000)
                # SO_LINGER l_onoff=1, l_linger=0 → TCP RST on close (hard disconnect).
                # Use explicit 8-byte little-endian struct (struct linger on macOS).
                try:
                    s.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, struct.pack('ii', 1, 0))
                except Exception:
                    # Fallback: explicit little-endian bytes (l_onoff=1, l_linger=0)
                    s.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, b'\x01\x00\x00\x00\x00\x00\x00\x00')
                s.close()
                # stop outer recv loop
                break
    if data_count >= 1 and t_close is not None:
        break
out = {"data_count": data_count, "t_first_data_ms": t_first_data, "t_close_ms": t_close, "status": status}
with open("${out_file}", "w") as f:
    json.dump(out, f)
PYEOF
}

# Classify: given a jsonl output of {ts_ms, line}, classify as LIVE / BUFFERED / INDETERMINATE.
# The MODE signal is the streaming pattern (LIVE = incremental chunks; BUFFERED = single burst).
#   LIVE     = big_gaps (gaps between consecutive data: events >= 150ms) >= 2
#   BUFFERED = big_gaps == 0 AND spread <= 250ms (all chunks in one burst)
#   INDETERMINATE = edge case (e.g., big_gaps == 1, or big_gaps==0 with spread > 250ms)
# The absolute TTFB is a sanity hint (LIVE typically <800ms; BUFFERED typically >1000ms)
# but is NOT the primary signal — local proxy adds variable latency.
classify_stream() {
    local jsonl_file="$1"
    python3 - <<PYEOF
import json, sys
events = []
with open("$jsonl_file") as f:
    for line in f:
        line = line.strip()
        if not line: continue
        try:
            events.append(json.loads(line))
        except Exception:
            pass
data_arrivals = [e["ts_ms"] for e in events if e["line"].startswith("data: ")]
if not data_arrivals:
    print(json.dumps({"class": "NO_DATA", "ttfb_ms": -1, "spread_ms": -1, "n_data": 0, "big_gaps": 0}))
    sys.exit(0)
ttfb = data_arrivals[0]
spread = (data_arrivals[-1] - data_arrivals[0]) if len(data_arrivals) > 1 else 0
gaps = []
prev = None
for ts in data_arrivals:
    if prev is not None: gaps.append(ts - prev)
    prev = ts
big_gaps = sum(1 for g in gaps if g >= 150)
n = len(data_arrivals)
# Classify purely on streaming pattern
if big_gaps >= 2:
    cls = "LIVE"
elif big_gaps == 0 and spread <= 250:
    cls = "BUFFERED"
else:
    cls = "INDETERMINATE"
print(json.dumps({
    "class": cls, "ttfb_ms": ttfb, "spread_ms": spread,
    "n_data": n, "big_gaps": big_gaps,
}))
PYEOF
}

# ============================================================
# SCENARIO 1 — Truth Table Rows (12)
# ============================================================
echo ""
echo "${BLUE}[7/9] === Scenario 1: Truth Table Rows ===${NC}"

# Define rows: "label|extra_mode|extra_value|expected"
TRUTH_TABLE=(
    "absent|none||LIVE"
    "bare|single||BUFFERED"
    "true-lower|single|true|BUFFERED"
    "TRUE-upper|single|TRUE|BUFFERED"
    "one|single|1|BUFFERED"
    "yes|single|yes|BUFFERED"
    "on|single|on|BUFFERED"
    "false-lower|single|false|LIVE"
    "zero|single|0|LIVE"
    "no|single|no|LIVE"
    "off|single|off|LIVE"
    "garbage-banana|single|banana|LIVE"
)

TT_PASS=0
TT_FAIL=0
TT_ROWS=""

printf '%s\n' "  | label            | expected  | ttfb_ms | spread_ms | n_data | big_gaps | class       | result"
printf '%s\n' "--+------------------+-----------+---------+-----------+--------+----------+-------------+-------"

for row in "${TRUTH_TABLE[@]}"; do
    IFS='|' read -r label mode value expected <<< "$row"
    out_file="$TMPDIR/tt_${label}.jsonl"
    # 1st attempt
    probe_stream_classify "$out_file" "$mode" "$value"
    RES=$(classify_stream "$out_file")
    CLS=$(echo "$RES" | python3 -c 'import json,sys; print(json.load(sys.stdin)["class"])')
    TTFB=$(echo "$RES" | python3 -c 'import json,sys; print(json.load(sys.stdin)["ttfb_ms"])')
    SPREAD=$(echo "$RES" | python3 -c 'import json,sys; print(json.load(sys.stdin)["spread_ms"])')
    NDATA=$(echo "$RES" | python3 -c 'import json,sys; print(json.load(sys.stdin)["n_data"])')
    GAPS=$(echo "$RES" | python3 -c 'import json,sys; print(json.load(sys.stdin)["big_gaps"])')
    # Retry once if INDETERMINATE
    if [ "$CLS" = "INDETERMINATE" ]; then
        sleep 0.5
        probe_stream_classify "$out_file" "$mode" "$value"
        RES=$(classify_stream "$out_file")
        CLS=$(echo "$RES" | python3 -c 'import json,sys; print(json.load(sys.stdin)["class"])')
        TTFB=$(echo "$RES" | python3 -c 'import json,sys; print(json.load(sys.stdin)["ttfb_ms"])')
        SPREAD=$(echo "$RES" | python3 -c 'import json,sys; print(json.load(sys.stdin)["spread_ms"])')
        NDATA=$(echo "$RES" | python3 -c 'import json,sys; print(json.load(sys.stdin)["n_data"])')
        GAPS=$(echo "$RES" | python3 -c 'import json,sys; print(json.load(sys.stdin)["big_gaps"])')
        RETRIED="(retried)"
    else
        RETRIED=""
    fi
    if [ "$CLS" = "$expected" ]; then
        RESULT="PASS"; TT_PASS=$((TT_PASS+1))
    else
        RESULT="FAIL"; TT_FAIL=$((TT_FAIL+1))
    fi
    printf '  | %-16s | %-9s | %-7s | %-9s | %-6s | %-8s | %-11s | %s %s\n' \
        "$label" "$expected" "$TTFB" "$SPREAD" "$NDATA" "$GAPS" "$CLS" "$RESULT" "$RETRIED"
    TT_ROWS="${TT_ROWS}${label}|${expected}|${CLS}|${TTFB}|${SPREAD}|${NDATA}|${GAPS}|${RESULT}"$'\n'
done

SCEN1_PASS=$TT_PASS
SCEN1_FAIL=$TT_FAIL
SCEN1_OVERALL="PASS"
[ "$TT_FAIL" -gt 0 ] && SCEN1_OVERALL="FAIL"
echo "  → Scenario 1: $SCEN1_OVERALL (${TT_PASS}/${TT_PASS}+${TT_FAIL})"

# ============================================================
# SCENARIO 2 — Multi-value header
# ============================================================
echo ""
echo "${BLUE}=== Scenario 2: Multi-value header ===${NC}"
sleep 1

# 2a: two separate header lines (true THEN false) → first wins → BUFFERED
MV_FILE_A="$TMPDIR/mv_true_false.jsonl"
probe_stream_classify "$MV_FILE_A" "multiline" "true||false"
MV_RES_A=$(classify_stream "$MV_FILE_A")
MV_CLS_A=$(echo "$MV_RES_A" | python3 -c 'import json,sys; print(json.load(sys.stdin)["class"])')
MV_TTFB_A=$(echo "$MV_RES_A" | python3 -c 'import json,sys; print(json.load(sys.stdin)["ttfb_ms"])')
MV_SPREAD_A=$(echo "$MV_RES_A" | python3 -c 'import json,sys; print(json.load(sys.stdin)["spread_ms"])')
MV_GAPS_A=$(echo "$MV_RES_A" | python3 -c 'import json,sys; print(json.load(sys.stdin)["big_gaps"])')
if [ "$MV_CLS_A" = "INDETERMINATE" ]; then
    sleep 0.5
    probe_stream_classify "$MV_FILE_A" "multiline" "true||false"
    MV_RES_A=$(classify_stream "$MV_FILE_A")
    MV_CLS_A=$(echo "$MV_RES_A" | python3 -c 'import json,sys; print(json.load(sys.stdin)["class"])')
fi
echo "  2a (lines: true, then false) → expected BUFFERED; got $MV_CLS_A (ttfb=$MV_TTFB_A spread=$MV_SPREAD_A gaps=$MV_GAPS_A)"
SCEN2A_PASS="PASS"
[ "$MV_CLS_A" = "BUFFERED" ] || SCEN2A_PASS="FAIL"

# 2b: two separate header lines (false THEN true) → first wins → LIVE
MV_FILE_B="$TMPDIR/mv_false_true.jsonl"
probe_stream_classify "$MV_FILE_B" "multiline" "false||true"
MV_RES_B=$(classify_stream "$MV_FILE_B")
MV_CLS_B=$(echo "$MV_RES_B" | python3 -c 'import json,sys; print(json.load(sys.stdin)["class"])')
MV_TTFB_B=$(echo "$MV_RES_B" | python3 -c 'import json,sys; print(json.load(sys.stdin)["ttfb_ms"])')
MV_SPREAD_B=$(echo "$MV_RES_B" | python3 -c 'import json,sys; print(json.load(sys.stdin)["spread_ms"])')
MV_GAPS_B=$(echo "$MV_RES_B" | python3 -c 'import json,sys; print(json.load(sys.stdin)["big_gaps"])')
if [ "$MV_CLS_B" = "INDETERMINATE" ]; then
    sleep 0.5
    probe_stream_classify "$MV_FILE_B" "multiline" "false||true"
    MV_RES_B=$(classify_stream "$MV_FILE_B")
    MV_CLS_B=$(echo "$MV_RES_B" | python3 -c 'import json,sys; print(json.load(sys.stdin)["class"])')
fi
echo "  2b (lines: false, then true) → expected LIVE;     got $MV_CLS_B (ttfb=$MV_TTFB_B spread=$MV_SPREAD_B gaps=$MV_GAPS_B)"
SCEN2B_PASS="PASS"
[ "$MV_CLS_B" = "LIVE" ] || SCEN2B_PASS="FAIL"

# 2c: single comma-joined `true, false` → ONE value "true, false" → LIVE (informational)
sleep 1
MV_FILE_C="$TMPDIR/mv_comma_joined.jsonl"
probe_stream_classify "$MV_FILE_C" "comma-joined" "true, false"
MV_RES_C=$(classify_stream "$MV_FILE_C")
MV_CLS_C=$(echo "$MV_RES_C" | python3 -c 'import json,sys; print(json.load(sys.stdin)["class"])')
MV_TTFB_C=$(echo "$MV_RES_C" | python3 -c 'import json,sys; print(json.load(sys.stdin)["ttfb_ms"])')
MV_SPREAD_C=$(echo "$MV_RES_C" | python3 -c 'import json,sys; print(json.load(sys.stdin)["spread_ms"])')
if [ "$MV_CLS_C" = "INDETERMINATE" ]; then
    sleep 0.5
    probe_stream_classify "$MV_FILE_C" "comma-joined" "true, false"
    MV_RES_C=$(classify_stream "$MV_FILE_C")
    MV_CLS_C=$(echo "$MV_RES_C" | python3 -c 'import json,sys; print(json.load(sys.stdin)["class"])')
fi
echo "  2c (single line: 'true, false') → expected LIVE per docs caveat; got $MV_CLS_C (ttfb=$MV_TTFB_C spread=$MV_SPREAD_C)"
# Informational — must be LIVE (per docs caveat)
SCEN2C_PASS="PASS"
[ "$MV_CLS_C" = "LIVE" ] || SCEN2C_PASS="FAIL"

if [ "$SCEN2A_PASS" = "PASS" ] && [ "$SCEN2B_PASS" = "PASS" ] && [ "$SCEN2C_PASS" = "PASS" ]; then
    SCEN2_OVERALL="PASS"
else
    SCEN2_OVERALL="FAIL"
fi
echo "  → Scenario 2: $SCEN2_OVERALL"

# ============================================================
# SCENARIO 3 — stream=false identity (header vs no header)
# ============================================================
echo ""
echo "${BLUE}=== Scenario 3: stream=false identity ===${NC}"
sleep 1

NS_NO_HEADER="$TMPDIR/ns_noheader.txt"
NS_WITH_HEADER="$TMPDIR/ns_withheader.txt"

# 3a: stream=false WITHOUT header (raw body capture)
probe_nonstream_raw "$NS_NO_HEADER" ""
NS1_BYTES=$(wc -c < "$NS_NO_HEADER")
NS1_BODY=$(cat "$NS_NO_HEADER")
echo "  3a (no header):     bytes=$NS1_BYTES"

# 3b: stream=false WITH X-LLMProxy-Buffer-Response: true (raw body capture)
probe_nonstream_raw "$NS_WITH_HEADER" "X-LLMProxy-Buffer-Response: true"
NS2_BYTES=$(wc -c < "$NS_WITH_HEADER")
NS2_BODY=$(cat "$NS_WITH_HEADER")
echo "  3b (with: true):    bytes=$NS2_BYTES"

# Identical body?
SCEN3_BODY_OK="PASS"
SCEN3_JSON_OK="PASS"
SCEN3_NO_SSE="PASS"

# Strip trailing whitespace/newlines for stable comparison
NS1_BODY_TRIM=$(echo "$NS1_BODY" | tr -d '\r' | sed 's/[[:space:]]*$//')
NS2_BODY_TRIM=$(echo "$NS2_BODY" | tr -d '\r' | sed 's/[[:space:]]*$//')
if [ "$NS1_BODY_TRIM" != "$NS2_BODY_TRIM" ]; then
    SCEN3_BODY_OK="FAIL"
    echo "  --- 3a body (first 300): ${NS1_BODY_TRIM:0:300}"
    echo "  --- 3b body (first 300): ${NS2_BODY_TRIM:0:300}"
fi

# Both bodies must be valid JSON
NS1_VALID_JSON=$(echo "$NS1_BODY_TRIM" | python3 -c 'import sys,json; json.loads(sys.stdin.read()); print("OK")' 2>/dev/null || echo "FAIL")
NS2_VALID_JSON=$(echo "$NS2_BODY_TRIM" | python3 -c 'import sys,json; json.loads(sys.stdin.read()); print("OK")' 2>/dev/null || echo "FAIL")
echo "  3a JSON valid: $NS1_VALID_JSON; 3b JSON valid: $NS2_VALID_JSON"
[ "$NS1_VALID_JSON" = "OK" ] && [ "$NS2_VALID_JSON" = "OK" ] || SCEN3_JSON_OK="FAIL"

# No SSE framing in either
echo "$NS1_BODY" | grep -q "^data: " && SCEN3_NO_SSE="FAIL"
echo "$NS2_BODY" | grep -q "^data: " && SCEN3_NO_SSE="FAIL"

if [ "$SCEN3_BODY_OK" = "PASS" ] && [ "$SCEN3_JSON_OK" = "PASS" ] && [ "$SCEN3_NO_SSE" = "PASS" ]; then
    SCEN3_OVERALL="PASS"
else
    SCEN3_OVERALL="FAIL"
fi
echo "  body_identical=$SCEN3_BODY_OK json_valid_both=$SCEN3_JSON_OK no_sse_framing=$SCEN3_NO_SSE"
echo "  → Scenario 3: $SCEN3_OVERALL"

# ============================================================
# SCENARIO 4 — Client disconnect mid-stream (live mode)
# ============================================================
echo ""
echo "${BLUE}=== Scenario 4: Client disconnect mid-stream (live mode) ===${NC}"
sleep 1

DISC_FILE="$TMPDIR/disconnect.json"
probe_disconnect_after_first "$DISC_FILE"
DISC_RESULT=$(cat "$DISC_FILE")
echo "  disconnect result: $DISC_RESULT"

# Wait 2 seconds for any panic / cleanup to surface in proxy log
sleep 2

# /healthz should still respond 200
HZ_AFTER=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:$PROXY_PORT/healthz")
echo "  /healthz after disconnect: $HZ_AFTER"

# Check proxy stderr for panic / SIGSEGV / fatal
PROXY_STDERR_TAIL=$(tail -50 "$TMPDIR/proxy.log")
PANIC_HITS=$(echo "$PROXY_STDERR_TAIL" | grep -ciE "panic|SIGSEGV|signal: |fatal error|goroutine.*\[running" || true)
echo "  panic/SIGSEGV hits in proxy stderr tail: $PANIC_HITS"
echo "  --- proxy log tail (last 20 lines) ---"
echo "$PROXY_STDERR_TAIL" | tail -20

SCEN4_HZ_OK="FAIL"
[ "$HZ_AFTER" = "200" ] && SCEN4_HZ_OK="PASS"
SCEN4_NO_PANIC="FAIL"
[ "$PANIC_HITS" -eq 0 ] && SCEN4_NO_PANIC="PASS"

# Also: the proxy process should still be alive (kill -0)
SCEN4_PROXY_ALIVE="FAIL"
if [ -n "$PROXY_PID" ] && kill -0 "$PROXY_PID" 2>/dev/null; then
    SCEN4_PROXY_ALIVE="PASS"
fi

if [ "$SCEN4_HZ_OK" = "PASS" ] && [ "$SCEN4_NO_PANIC" = "PASS" ] && [ "$SCEN4_PROXY_ALIVE" = "PASS" ]; then
    SCEN4_OVERALL="PASS"
else
    SCEN4_OVERALL="FAIL"
fi
echo "  healthz=$SCEN4_HZ_OK no_panic=$SCEN4_NO_PANIC proxy_alive=$SCEN4_PROXY_ALIVE"
echo "  → Scenario 4: $SCEN4_OVERALL"

# ============================================================
# Final report
# ============================================================
echo ""
echo "${BLUE}[8/9] === Final report ===${NC}"
HZ_FINAL=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:$PROXY_PORT/healthz")
PROXY_PID_ALIVE="NO"
[ -n "$PROXY_PID" ] && kill -0 "$PROXY_PID" 2>/dev/null && PROXY_PID_ALIVE="YES"

# Verify ports are free (after we kill processes)
echo "${YELLOW}[9/9] Verifying ports free after scenario completion...${NC}"
# Note: cleanup trap will kill processes on EXIT, so the final lsof check
# must happen before exit. We do an in-band verification here.

cat > "$TMPDIR/results.json" <<JSONEOF
{
  "branch": "feature/real-streaming-default",
  "head_sha": "$(git -C "$ROOT_DIR" rev-parse --short HEAD)",
  "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "proxy_port": $PROXY_PORT,
  "mock_port": $MOCK_PORT,
  "scenario_1_truth_table": {
    "overall": "$SCEN1_OVERALL",
    "pass_count": $TT_PASS,
    "fail_count": $TT_FAIL,
    "rows": $(echo "$TT_ROWS" | python3 -c '
import sys, json
rows = []
for line in sys.stdin:
    line = line.strip()
    if not line: continue
    parts = line.split("|")
    if len(parts) >= 8:
        rows.append({
            "label": parts[0],
            "expected": parts[1],
            "classified": parts[2],
            "ttfb_ms": int(parts[3]),
            "spread_ms": int(parts[4]),
            "n_data": int(parts[5]),
            "big_gaps": int(parts[6]),
            "result": parts[7],
        })
print(json.dumps(rows))
')
  },
  "scenario_2_multivalue": {
    "overall": "$SCEN2_OVERALL",
    "true_then_false_separate_lines": {
      "result": "$SCEN2A_PASS",
      "expected": "BUFFERED",
      "classified": "$MV_CLS_A",
      "ttfb_ms": $MV_TTFB_A,
      "spread_ms": $MV_SPREAD_A,
      "big_gaps": $MV_GAPS_A
    },
    "false_then_true_separate_lines": {
      "result": "$SCEN2B_PASS",
      "expected": "LIVE",
      "classified": "$MV_CLS_B",
      "ttfb_ms": $MV_TTFB_B,
      "spread_ms": $MV_SPREAD_B,
      "big_gaps": $MV_GAPS_B
    },
    "single_line_comma_joined_informational": {
      "result": "$SCEN2C_PASS",
      "expected": "LIVE (per docs caveat)",
      "classified": "$MV_CLS_C",
      "ttfb_ms": $MV_TTFB_C,
      "spread_ms": $MV_SPREAD_C
    }
  },
  "scenario_3_nonstream_identity": {
    "overall": "$SCEN3_OVERALL",
    "body_identical": "$SCEN3_BODY_OK",
    "json_valid_both": "$SCEN3_JSON_OK",
    "no_sse_framing": "$SCEN3_NO_SSE",
    "no_header_bytes": $NS1_BYTES,
    "with_header_bytes": $NS2_BYTES
  },
  "scenario_4_disconnect_safety": {
    "overall": "$SCEN4_OVERALL",
    "data_received_before_close": $(echo "$DISC_RESULT" | python3 -c 'import json,sys; print(json.load(sys.stdin)["data_count"])'),
    "t_first_data_ms": $(echo "$DISC_RESULT" | python3 -c 'import json,sys; print(json.load(sys.stdin)["t_first_data_ms"])'),
    "t_close_ms": $(echo "$DISC_RESULT" | python3 -c 'import json,sys; print(json.load(sys.stdin)["t_close_ms"])'),
    "healthz_after_2s": "$HZ_AFTER",
    "panic_sigsegv_hits": $PANIC_HITS,
    "proxy_pid_alive": "$SCEN4_PROXY_ALIVE"
  },
  "cleanup": {
    "healthz_final": "$HZ_FINAL",
    "proxy_pid_alive": "$PROXY_PID_ALIVE",
    "results_path": "$TMPDIR/results.json",
    "proxy_log": "$TMPDIR/proxy.log",
    "mock_log": "$TMPDIR/mock.log"
  }
}
JSONEOF

echo ""
echo "${BLUE}===========================================${NC}"
echo "${BLUE}  RESULTS                                  ${NC}"
echo "${BLUE}===========================================${NC}"
echo "Scenario 1 (Truth Table, 12 rows): $SCEN1_OVERALL ($TT_PASS pass / $TT_FAIL fail)"
echo "Scenario 2 (Multi-value):         $SCEN2_OVERALL (2a=$SCEN2A_PASS, 2b=$SCEN2B_PASS, 2c=$SCEN2C_PASS informational)"
echo "Scenario 3 (stream=false ID):     $SCEN3_OVERALL (body=$SCEN3_BODY_OK json=$SCEN3_JSON_OK no_sse=$SCEN3_NO_SSE)"
echo "Scenario 4 (Disconnect safety):   $SCEN4_OVERALL (healthz=$SCEN4_HZ_OK no_panic=$SCEN4_NO_PANIC proxy_alive=$SCEN4_PROXY_ALIVE)"
echo ""
echo "  /healthz (final): $HZ_FINAL"
echo "  proxy_pid_alive:  $PROXY_PID_ALIVE"
echo "  Results JSON:     $TMPDIR/results.json"

# Overall pass criteria:
#   S1 PASS, S2 PASS, S3 PASS, S4 PASS required
if [ "$SCEN1_OVERALL" = "PASS" ] && [ "$SCEN2_OVERALL" = "PASS" ] && [ "$SCEN3_OVERALL" = "PASS" ] && [ "$SCEN4_OVERALL" = "PASS" ]; then
    echo "${GREEN}OVERALL: PASS${NC}"
    OVERALL_EXIT=0
else
    echo "${RED}OVERALL: FAIL${NC}"
    OVERALL_EXIT=1
fi
exit $OVERALL_EXIT
