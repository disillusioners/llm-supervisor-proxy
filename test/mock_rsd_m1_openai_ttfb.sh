#!/usr/bin/env bash
# Mock Test M1 — real-streaming-default: OpenAI path live-binary TTFB smoke (merge gate)
#
# Branch: feature/real-streaming-default @ 22e76d6
# Ports: 10110 (proxy), 10111 (mock OpenAI upstream). Strict isolation.
# Never touches 8088 or other workers' ports.
#
# Verifies:
#   A. /v1/chat/completions DEFAULT (no header) - TTFB <= 1000ms, incremental streaming
#      (>=3 distinct chunk-arrival timestamps >=150ms apart), assembled content correct
#   B. /v1/chat/completions + X-LLMProxy-Buffer-Response: true - TTFB >= 1300ms, single
#      burst (spread <= 250ms), assembled content identical to A
#   C. Informational: raw SSE body bytes A vs B - report equal/diff verbatim
#   D. /healthz still 200 after both scenarios (no panic, no leak)
#
# Uses an UNREGISTERED model name in the client request so the proxy falls through
# to the race-external path with the env-pinned UPSTREAM_URL pointing at the mock.
# Auth is therefore not required (handler.go:411 gate: requiresAuth || tokenStore != nil;
# for external/unregistered models requiresAuth=false and a fresh DB has no tokenStore).

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

# ---- strict port isolation ----
PROXY_PORT=10110
MOCK_PORT=10111
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

cleanup() {
    local code=$?
    if [ -n "$ALARM_PID" ]; then kill "$ALARM_PID" 2>/dev/null || true; fi
    if [ -n "$MOCK_PID" ]; then kill "$MOCK_PID" 2>/dev/null || true; fi
    if [ -n "$PROXY_PID" ]; then kill "$PROXY_PID" 2>/dev/null || true; fi
    # Targeted port cleanup — only kill PIDs we started, or PIDs bound to OUR ports
    for port in "$PROXY_PORT" "$MOCK_PORT"; do
        for pid in $(lsof -ti:"$port" 2>/dev/null); do
            cmd=$(ps -o command= -p "$pid" 2>/dev/null || true)
            case "$cmd" in
                *rsd_m1_proxy*|*mock_rsd_m1_openai*) kill -9 "$pid" 2>/dev/null || true ;;
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
echo "${BLUE}  M1 — OpenAI path live-binary TTFB smoke  ${NC}"
echo "${BLUE}  Branch: feature/real-streaming-default    ${NC}"
echo "${BLUE}  Proxy port: $PROXY_PORT | Mock port: $MOCK_PORT    ${NC}"
echo "${BLUE}===========================================${NC}"

# ---- 1. build proxy binary FIRST (using default HOME for go module cache) ----
echo "${YELLOW}[1/8] Building proxy binary from HEAD ($(git -C "$ROOT_DIR" rev-parse --short HEAD))...${NC}"
TMPDIR=$(mktemp -d -t rsd-m1-XXXXXX)
PROXY_BIN="$TMPDIR/rsd_m1_proxy"
( cd "$ROOT_DIR" && go build -o "$PROXY_BIN" ./cmd ) > "$TMPDIR/build.log" 2>&1
if [ ! -x "$PROXY_BIN" ]; then
    echo "${RED}go build failed; log:${NC}"
    tail -30 "$TMPDIR/build.log"
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

# ---- 4. start mock OpenAI upstream on 10111 ----
echo "${YELLOW}[3/8] Starting mock OpenAI upstream on $MOCK_PORT...${NC}"
cat > "$TMPDIR/mock_openai_upstream.py" <<'PYEOF'
#!/usr/bin/env python3
"""Mock OpenAI-upstream on 10111 for M1 test.

Serves:
  POST /v1/chat/completions → real OpenAI SSE: 5 content chunks × 300ms inter-chunk delay
                              (total ≈1.5s), then data: [DONE].
                              Real OpenAI wire framing (data: {json}\n\n).
  GET  /healthz              → 200 OK.

Self-verifies the SSE framing parses as OpenAI chunks before serving.
"""
import json
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

CHUNKS = ["Hello", ", ", "world", " from ", "mock-openai."]
MOCK_MODEL = "mock-openai-m1"

def sse_data_only(data: dict) -> bytes:
    return f"data: {json.dumps(data, separators=(',', ':'))}\n\n".encode()

def build_stream() -> bytes:
    """Pre-build the entire stream as bytes (for byte-equality assertion in C).
    The actual handler writes incrementally with sleeps to drive the timing
    check, but the byte content MUST match this pre-build."""
    out = []
    for i, content in enumerate(CHUNKS):
        out.append(sse_data_only({
            "id": "chatcmpl-mock",
            "object": "chat.completion.chunk",
            "created": 1700000000,
            "model": MOCK_MODEL,
            "choices": [{
                "index": 0,
                "delta": {"content": content},
                "finish_reason": None,
            }],
        }))
    # Final chunk with finish_reason=stop + usage
    out.append(sse_data_only({
        "id": "chatcmpl-mock",
        "object": "chat.completion.chunk",
        "created": 1700000000,
        "model": MOCK_MODEL,
        "choices": [{
            "index": 0,
            "delta": {},
            "finish_reason": "stop",
        }],
        "usage": {"prompt_tokens": 10, "completion_tokens": 17, "total_tokens": 27},
    }))
    out.append(b"data: [DONE]\n\n")
    return b"".join(out)

# Self-verify the wire shape before serving: parse what we just built.
PREAMBLE = build_stream()
SELF_CHECK = []
for ln in PREAMBLE.split(b"\n\n"):
    if not ln:
        continue
    if not ln.startswith(b"data: "):
        SELF_CHECK.append(("BAD_PREFIX", ln[:40]))
        continue
    payload = ln[len(b"data: "):]
    if payload.strip() == b"[DONE]":
        SELF_CHECK.append(("DONE", b""))
        continue
    try:
        obj = json.loads(payload)
        assert obj.get("object") == "chat.completion.chunk", f"object={obj.get('object')!r}"
        assert obj.get("id") == "chatcmpl-mock", f"id={obj.get('id')!r}"
        choices = obj.get("choices") or []
        assert len(choices) >= 1, "no choices"
        delta = choices[0].get("delta") or {}
        SELF_CHECK.append(("OK", str(delta.get("content", "<no-content>"))))
    except Exception as e:
        SELF_CHECK.append(("PARSE_ERR", str(e).encode()))
        sys.stderr.write(f"mock self-check failed: {e} for payload {payload[:120]}\n")
        sys.exit(3)

CONTENT_DELTAS = [c for kind, c in SELF_CHECK if kind == "OK"]
EXPECTED_CONTENT = "".join(CHUNKS)
ASSEMBLED = "".join(c for c in CONTENT_DELTAS if c not in ("<no-content>", ""))
assert ASSEMBLED == EXPECTED_CONTENT, (
    f"mock self-check content mismatch: got {ASSEMBLED!r}, expected {EXPECTED_CONTENT!r}"
)
print(f"[mock-openai] self-check OK: {len(CONTENT_DELTAS)} content deltas, "
      f"assembled={ASSEMBLED!r}", flush=True)
sys.stdout.flush()

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args, **kwargs):
        pass  # quiet

    def do_GET(self):
        if self.path == "/healthz":
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.end_headers()
            self.wfile.write(b"OK")
            return
        self.send_response(404); self.end_headers()

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
                # Send headers + first chunk immediately, then write+flush per
                # chunk with 300ms gaps so the proxy can forward live.
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.send_header("Cache-Control", "no-cache")
                self.send_header("Connection", "close")
                self.send_header("X-Accel-Buffering", "no")
                self.end_headers()
                for content in CHUNKS:
                    self.wfile.write(sse_data_only({
                        "id": "chatcmpl-mock",
                        "object": "chat.completion.chunk",
                        "created": 1700000000,
                        "model": MOCK_MODEL,
                        "choices": [{
                            "index": 0,
                            "delta": {"content": content},
                            "finish_reason": None,
                        }],
                    }))
                    self.wfile.flush()
                    time.sleep(0.3)
                # Final chunk with usage
                self.wfile.write(sse_data_only({
                    "id": "chatcmpl-mock",
                    "object": "chat.completion.chunk",
                    "created": 1700000000,
                    "model": MOCK_MODEL,
                    "choices": [{
                        "index": 0,
                        "delta": {},
                        "finish_reason": "stop",
                    }],
                    "usage": {"prompt_tokens": 10, "completion_tokens": 17, "total_tokens": 27},
                }))
                self.wfile.flush()
                self.wfile.write(b"data: [DONE]\n\n")
                self.wfile.flush()
                return
            else:
                # Non-stream: return single JSON response
                data = json.dumps({
                    "id": "chatcmpl-mock",
                    "object": "chat.completion",
                    "created": 1700000000,
                    "model": MOCK_MODEL,
                    "choices": [{
                        "index": 0,
                        "message": {"role": "assistant", "content": "".join(CHUNKS)},
                        "finish_reason": "stop",
                    }],
                    "usage": {"prompt_tokens": 10, "completion_tokens": 17, "total_tokens": 27},
                }, separators=(',', ':')).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)
                return
        self.send_response(404); self.end_headers()

if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 10111
    srv = ThreadingHTTPServer(("127.0.0.1", port), Handler)
    print(f"[mock-openai] listening on 127.0.0.1:{port}", flush=True)
    srv.serve_forever()
PYEOF

python3 "$TMPDIR/mock_openai_upstream.py" "$MOCK_PORT" > "$TMPDIR/mock.log" 2>&1 &
MOCK_PID=$!

# Wait for mock /healthz
for i in $(seq 1 50); do
    sleep 0.1
    if curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:$MOCK_PORT/healthz" 2>/dev/null | grep -q "200"; then
        echo "${GREEN}[3/8] mock upstream ready (PID $MOCK_PID)${NC}"
        break
    fi
    if [ "$i" -eq 50 ]; then
        echo "${RED}mock failed to start; log:${NC}"
        tail -30 "$TMPDIR/mock.log"
        exit 1
    fi
done

# ---- 5. start proxy on 10110 ----
echo "${YELLOW}[4/8] Starting proxy binary on $PROXY_PORT (UPSTREAM_URL=http://127.0.0.1:$MOCK_PORT)...${NC}"
export APPLY_ENV_OVERRIDES="true"
export PORT="$PROXY_PORT"
export UPSTREAM_URL="http://127.0.0.1:$MOCK_PORT"
export IDLE_TIMEOUT="10s"
export MAX_GENERATION_TIME="60s"
export LOOP_DETECTION_ENABLED="false"
export LOOP_DETECTION_SHADOW_MODE="false"
# Encryption disabled (no INTERNAL_ENCRYPTION_KEY) — pass-through mode;
# we don't use registered credentials, so encryption isn't needed.

"$PROXY_BIN" > "$TMPDIR/proxy.log" 2>&1 &
PROXY_PID=$!

# Wait for proxy /healthz
for i in $(seq 1 50); do
    sleep 0.1
    if curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:$PROXY_PORT/healthz" 2>/dev/null | grep -q "200"; then
        echo "${GREEN}[4/8] proxy ready (PID $PROXY_PID)${NC}"
        break
    fi
    if [ "$i" -eq 50 ]; then
        echo "${RED}proxy failed to start; log:${NC}"
        tail -30 "$TMPDIR/proxy.log"
        exit 1
    fi
done

# ---- 6. Client probe (timestamped OpenAI wire reader) ----
echo "${YELLOW}[5/8] === Scenario A: DEFAULT (no header) ===${NC}"

probe_openai() {
    # $1=label, $2=extra_header_block (or "" for none)
    # Stdout: SUMMARY_JSON line on its own. Stderr: per-line jsonl log.
    # Call site handles redirection.
    local label="$1"
    local extra="$2"
    python3 - "$PROXY_PORT" "$label" "$extra" <<'PYEOF'
import socket
import sys
import time
import json

proxy_port = int(sys.argv[1])
label = sys.argv[2]
extra = sys.argv[3]  # extra header lines (including \r\n) or empty

hdr_lines = [
    "POST /v1/chat/completions HTTP/1.1",
    "Host: 127.0.0.1:%d" % proxy_port,
    "Content-Type: application/json",
    "Accept: text/event-stream",
    "Connection: close",
]
if extra:
    hdr_lines.append(extra)
body = json.dumps({
    "model": "mock-m1-unregistered-model",
    "messages": [{"role": "user", "content": "rsd-m1-probe"}],
    "stream": True,
    "max_tokens": 50,
})
hdr_block = "\r\n".join(hdr_lines) + "\r\nContent-Length: %d\r\n\r\n" % len(body)
payload = (hdr_block + body).encode()

s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.settimeout(15)
t_connect_start = time.time()
s.connect(("127.0.0.1", proxy_port))
t_after_connect = time.time()
s.sendall(payload)

# Read everything, recording arrival time of each chunk arrival at the client.
buf = b""
arrivals = []  # list of (ts, raw_chunk_bytes)
chunk_log = []  # newline-delimited JSON: {ts_ms, kind, line}
t_first_byte = None
t_last_byte = None
deadline = time.time() + 12
while time.time() < deadline:
    try:
        chunk = s.recv(4096)
    except socket.timeout:
        break
    if not chunk:
        break
    now = time.time()
    if t_first_byte is None:
        t_first_byte = now
    t_last_byte = now
    arrivals.append((now, chunk))
    buf += chunk

s.close()

# Parse the SSE framing from concatenated body for record.
# Find the body (after \r\n\r\n).
hdr_end = buf.find(b"\r\n\r\n")
if hdr_end < 0:
    sys.stderr.write("no header terminator found\n")
    sys.exit(4)
status_line = buf[:buf.find(b"\r\n")].decode(errors="replace")
body_bytes = buf[hdr_end + 4:]

# Split into SSE messages (separated by \n\n) and emit per-line arrival info.
# We record arrival at chunk granularity (recv() boundaries) AND line granularity
# (parsing out each `data: ...` line within the body for arrival timing).
# For arrival timing of content, we use the chunk arrival timestamp of the
# recv() call that first included that line.
lines = body_bytes.split(b"\n")
cur_ts = None
data_line_count = 0
content_deltas = []
for raw in lines:
    line = raw.decode(errors="replace").rstrip("\r")
    if not line:
        continue
    if line.startswith("data: "):
        # Find the recv() chunk this line belongs to (last arrival whose cumulative
        # length covers this position). Approximate by recomputing cumulative
        # bytes — for simplicity, mark arrival as last chunk arrival timestamp.
        cur_ts = arrivals[-1][0] if arrivals else time.time()
        payload_data = line[len("data: "):]
        ts_ms = int((cur_ts - t_first_byte) * 1000) if t_first_byte else -1
        chunk_log.append(json.dumps({
            "kind": "data",
            "ts_ms": ts_ms,
            "line": line,
        }))
        if payload_data.strip() != "[DONE]":
            try:
                obj = json.loads(payload_data)
                choices = obj.get("choices") or []
                if choices:
                    delta = choices[0].get("delta") or {}
                    if "content" in delta and delta["content"] is not None:
                        content_deltas.append(delta["content"])
            except Exception:
                pass
        data_line_count += 1

# Compute content arrivals more precisely: re-derive cumulative byte positions.
# Walk the body and for each \n-delimited line, attribute its timestamp to the
# last recv() arrival whose cumulative span covers that line's start.
content_arrivals = []  # ts_ms of first recv() that contained each data: line's start
all_arrivals_per_byte = []  # (start_offset, end_offset, ts) for each recv chunk
off = 0
for (ts, chunk_bytes) in arrivals:
    end = off + len(chunk_bytes)
    all_arrivals_per_byte.append((off, end, ts))
    off = end

# Body start offset = hdr_end + 4
body_start = hdr_end + 4
line_pos = body_start
first_data_line_offset = None
for raw in lines:
    if not raw:
        line_pos += len(raw) + 1
        continue
    line_text = raw.decode(errors="replace").rstrip("\r")
    line_end = line_pos + len(raw)
    if line_text.startswith("data: "):
        # Find ts = ts of recv() whose span contains line_pos
        ts = None
        for (a, b, t) in all_arrivals_per_byte:
            if a <= line_pos < b:
                ts = t
                break
        if ts is None and all_arrivals_per_byte:
            ts = all_arrivals_per_byte[-1][2]
        if ts and t_first_byte:
            content_arrivals.append(int((ts - t_first_byte) * 1000))
        if first_data_line_offset is None:
            first_data_line_offset = line_pos
    line_pos = line_end + 1  # for \n

# TTFB = time from connect-start to FIRST `data: ` LINE arrival.
# This excludes the proxy's `: connected\n\n` preamble (which the proxy
# writes immediately in both modes and isn't user content).
t_first_data_byte = None
if first_data_line_offset is not None:
    for (a, b, t) in all_arrivals_per_byte:
        if a <= first_data_line_offset < b:
            t_first_data_byte = t
            break
if t_first_data_byte is None and all_arrivals_per_byte:
    t_first_data_byte = all_arrivals_per_byte[-1][2]

# Compute summary
ttfb_ms = int((t_first_data_byte - t_connect_start) * 1000) if t_first_data_byte else -1
total_ms = int((t_last_byte - t_connect_start) * 1000) if t_last_byte else -1

# Output structured JSON summary on stdout (separate from the per-line log)
# We write the per-line log to stderr so the caller captures it.
assembled = "".join(content_deltas)
summary = {
    "label": label,
    "status_line": status_line,
    "ttfb_ms": ttfb_ms,
    "total_ms": total_ms,
    "n_data_lines": data_line_count,
    "n_content_deltas": len(content_deltas),
    "content_arrivals_ms": content_arrivals,
    "assembled_content": assembled,
    "raw_body_hex": body_bytes.hex(),
    "raw_body_len": len(body_bytes),
}
sys.stdout.write("SUMMARY_JSON=" + json.dumps(summary) + "\n")
sys.stdout.flush()

# Per-line log to stderr
for entry in chunk_log:
    sys.stderr.write(entry + "\n")
sys.stderr.flush()
PYEOF
}

A_FILE="$TMPDIR/A.jsonl"
A_RAW="$TMPDIR/A.raw"
probe_openai "A_default" "" > "$A_RAW" 2> "$A_FILE"

# Extract the SUMMARY_JSON line from probe stdout (single line on stdout)
A_SUMMARY=$(grep '^SUMMARY_JSON=' "$A_RAW" | head -1 | sed 's/^SUMMARY_JSON=//')
A_TTFB=$(echo "$A_SUMMARY" | python3 -c 'import json,sys; print(json.load(sys.stdin)["ttfb_ms"])')
A_TOTAL=$(echo "$A_SUMMARY" | python3 -c 'import json,sys; print(json.load(sys.stdin)["total_ms"])')
A_N=$(echo "$A_SUMMARY" | python3 -c 'import json,sys; print(json.load(sys.stdin)["n_content_deltas"])')
A_TEXT=$(echo "$A_SUMMARY" | python3 -c 'import json,sys; print(json.load(sys.stdin)["assembled_content"])')
A_ARRIVALS_JSON=$(echo "$A_SUMMARY" | python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin)["content_arrivals_ms"]))')
A_HEX=$(echo "$A_SUMMARY" | python3 -c 'import json,sys; print(json.load(sys.stdin)["raw_body_hex"])')
A_LEN=$(echo "$A_SUMMARY" | python3 -c 'import json,sys; print(json.load(sys.stdin)["raw_body_len"])')

# Count distinct arrivals >= 150ms apart
A_GAPS=$(echo "$A_ARRIVALS_JSON" | python3 -c '
import json, sys
arr = json.loads(sys.stdin.read())
gaps = []
for i in range(1, len(arr)):
    gaps.append(arr[i] - arr[i-1])
big = [g for g in gaps if g >= 150]
print(f"n_data_arrivals={len(arr)}, gaps={gaps}, big_gaps>=150={len(big)}")
')

EXPECTED_TEXT="Hello, world from mock-openai."

A_PASS="PASS"
A_REASONS=()
[ "$A_TTFB" -le 1000 ] || { A_PASS="FAIL"; A_REASONS+=("TTFB=$A_TTFB > 1000 (expected <=1000ms)"); }
# < 60% of total duration
A_60PCT=$(( A_TOTAL * 60 / 100 ))
[ "$A_TTFB" -lt "$A_60PCT" ] || { A_PASS="FAIL"; A_REASONS+=("TTFB=$A_TTFB >= 60% of total=$A_TOTAL"); }
A_GAPS_COUNT=$(echo "$A_GAPS" | sed 's/.*big_gaps>=150=//')
[ "$A_GAPS_COUNT" -ge 3 ] || { A_PASS="FAIL"; A_REASONS+=("big_gaps=$A_GAPS_COUNT < 3 (expected >=3 distinct arrivals >=150ms apart)"); }
[ "$A_TEXT" = "$EXPECTED_TEXT" ] || { A_PASS="FAIL"; A_REASONS+=("assembled text mismatch: got '$A_TEXT'"); }
echo "  A: ttfb=${A_TTFB}ms total=${A_TOTAL}ms n_content=$A_N text='$A_TEXT'"
echo "  A gaps: $A_GAPS"
echo "  → Scenario A: $A_PASS ${A_REASONS:+(${A_REASONS[*]})}"

# ============================================================
# Scenario B: WITH X-LLMProxy-Buffer-Response: true
# ============================================================
echo ""
echo "${YELLOW}[6/8] === Scenario B: WITH X-LLMProxy-Buffer-Response: true ===${NC}"

B_FILE="$TMPDIR/B.jsonl"
B_RAW="$TMPDIR/B.raw"
probe_openai "B_buffered" "X-LLMProxy-Buffer-Response: true" > "$B_RAW" 2> "$B_FILE"

B_SUMMARY=$(grep '^SUMMARY_JSON=' "$B_RAW" | head -1 | sed 's/^SUMMARY_JSON=//')
B_TTFB=$(echo "$B_SUMMARY" | python3 -c 'import json,sys; print(json.load(sys.stdin)["ttfb_ms"])')
B_TOTAL=$(echo "$B_SUMMARY" | python3 -c 'import json,sys; print(json.load(sys.stdin)["total_ms"])')
B_N=$(echo "$B_SUMMARY" | python3 -c 'import json,sys; print(json.load(sys.stdin)["n_content_deltas"])')
B_TEXT=$(echo "$B_SUMMARY" | python3 -c 'import json,sys; print(json.load(sys.stdin)["assembled_content"])')
B_ARRIVALS_JSON=$(echo "$B_SUMMARY" | python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin)["content_arrivals_ms"]))')
B_HEX=$(echo "$B_SUMMARY" | python3 -c 'import json,sys; print(json.load(sys.stdin)["raw_body_hex"])')
B_LEN=$(echo "$B_SUMMARY" | python3 -c 'import json,sys; print(json.load(sys.stdin)["raw_body_len"])')

# Spread = last arrival - first arrival
B_SPREAD=$(echo "$B_ARRIVALS_JSON" | python3 -c '
import json, sys
arr = json.loads(sys.stdin.read())
if len(arr) < 2:
    print(-1)
else:
    print(arr[-1] - arr[0])
')

B_PASS="PASS"
B_REASONS=()
[ "$B_TTFB" -ge 1300 ] || { B_PASS="FAIL"; B_REASONS+=("TTFB=$B_TTFB < 1300 (expected buffered >=1.3s)"); }
[ "$B_SPREAD" -le 250 ] || { B_PASS="FAIL"; B_REASONS+=("spread=$B_SPREAD >250 (expected single burst)"); }
[ "$B_TEXT" = "$A_TEXT" ] || { B_PASS="FAIL"; B_REASONS+=("text differs from A"); }
echo "  B: ttfb=${B_TTFB}ms total=${B_TOTAL}ms n_content=$B_N spread=${B_SPREAD}ms text='$B_TEXT'"
echo "  B arrivals: $B_ARRIVALS_JSON"
echo "  → Scenario B: $B_PASS ${B_REASONS:+(${B_REASONS[*]})}"

# ============================================================
# Scenario C: Informational — raw SSE body bytes A vs B
# ============================================================
echo ""
echo "${YELLOW}[7/8] === Scenario C: raw SSE body bytes A vs B (informational) ===${NC}"
if [ "$A_HEX" = "$B_HEX" ]; then
    C_VERDICT="EQUAL"
else
    C_VERDICT="DIFFER"
fi
# A_LEN vs B_LEN already shown
echo "  A body_len=$A_LEN  B body_len=$B_LEN  verdict=$C_VERDICT"
if [ "$C_VERDICT" = "EQUAL" ]; then
    echo "  (A and B produced byte-identical SSE bodies — buffered mode is byte-for-byte)"
else
    echo "  (NOTE: byte bodies differ. This is informational only; content equality is the hard assert.)"
fi
C_PASS="INFO"

# ============================================================
# Scenario D: /healthz still 200 after both scenarios
# ============================================================
echo ""
echo "${YELLOW}[8/8] === Scenario D: /healthz still 200 after both scenarios ===${NC}"
HZ=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:$PROXY_PORT/healthz")
echo "  /healthz: $HZ"
D_PASS="FAIL"
[ "$HZ" = "200" ] && D_PASS="PASS"
echo "  → Scenario D: $D_PASS"

# ============================================================
# Write results.json + final report
# ============================================================
cat > "$TMPDIR/results.json" <<JSONEOF
{
  "branch": "feature/real-streaming-default",
  "head_sha": "$(git -C "$ROOT_DIR" rev-parse --short HEAD)",
  "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "scenario_A": {
    "result": "$A_PASS",
    "ttfb_ms": $A_TTFB,
    "total_ms": $A_TOTAL,
    "n_content_deltas": $A_N,
    "content_arrivals_ms": $A_ARRIVALS_JSON,
    "big_gaps_>=150ms": $A_GAPS_COUNT,
    "assembled_content": "$A_TEXT",
    "raw_body_len": $A_LEN,
    "reasons": $(printf '%s' "${A_REASONS[*]:-}" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read().split()))' 2>/dev/null || echo '[]')
  },
  "scenario_B": {
    "result": "$B_PASS",
    "ttfb_ms": $B_TTFB,
    "total_ms": $B_TOTAL,
    "n_content_deltas": $B_N,
    "content_arrivals_ms": $B_ARRIVALS_JSON,
    "spread_ms": $B_SPREAD,
    "assembled_content": "$B_TEXT",
    "raw_body_len": $B_LEN,
    "reasons": $(printf '%s' "${B_REASONS[*]:-}" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read().split()))' 2>/dev/null || echo '[]')
  },
  "scenario_C": {
    "result": "INFO",
    "verdict": "$C_VERDICT",
    "A_body_len": $A_LEN,
    "B_body_len": $B_LEN,
    "note": "informational only — content equality is the hard assert"
  },
  "scenario_D": {
    "result": "$D_PASS",
    "healthz_status": "$HZ"
  }
}
JSONEOF
echo "  Results: $TMPDIR/results.json"

echo ""
echo "${BLUE}===========================================${NC}"
echo "${BLUE}  RESULTS                                  ${NC}"
echo "${BLUE}===========================================${NC}"
echo "A: $A_PASS (ttfb=${A_TTFB}ms, total=${A_TOTAL}ms, big_gaps>=150=${A_GAPS_COUNT})"
echo "B: $B_PASS (ttfb=${B_TTFB}ms, total=${B_TOTAL}ms, spread=${B_SPREAD}ms)"
echo "C: $C_PASS (verdict=$C_VERDICT)"
echo "D: $D_PASS (healthz=$HZ)"

# Determine overall exit
if [ "$A_PASS" = "PASS" ] && [ "$B_PASS" = "PASS" ] && [ "$D_PASS" = "PASS" ]; then
    echo "${GREEN}OVERALL: PASS (A+B+D hard; C informational byte-equality check)${NC}"
    exit 0
else
    echo "${RED}OVERALL: FAIL${NC}"
    exit 1
fi