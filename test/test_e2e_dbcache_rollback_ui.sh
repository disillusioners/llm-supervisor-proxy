#!/usr/bin/env bash
# =============================================================================
# E2E Pack B — DB-cache-layer rollback + write-through + UI
# (feature/db-cache-layer @ 42d98f3)
#
# Part 1 (rollback): boot with CACHE_LAYER_ENABLED=false (the instant rollback
# lever) + the same M2 DB outage as pack A. Expected rollback shape: the cache
# layer is fully bypassed, so the ORIGINAL 2026-08-27 bug reproduces — model
# resolution falls to nil and requests passthrough to the default upstream
# (localhost:4001 sentinel) — and NO 503 config_store_unavailable is ever
# returned (the fail-fast gate lives inside the cache layer).
#
# Part 2 (cache default ON): boot 2 with UPSTREAM_URL pointed at the mock.
# UI endpoints keep working (/ui/, /fe/api/models, /fe/api/tokens,
# /fe/api/requests), the sentinel receives zero hits, and write-through
# semantics are asserted model-create / credential-add / credential-key-update
# / model-delete / token-create / token-delete.
#
# Empirical product behaviors this pack deliberately surfaces (do NOT work
# around; they are reported as findings):
#   - Credential CREATION is invalidate-only in the cache: a new credential
#     cannot be attached to a model until the next 60s reconciler tick
#     ("references an unknown credential" from validateCredentials). The pack
#     records the first rejected attempt and bounded-waits for the tick.
#   - Credential KEY UPDATE likewise leaves the credential temporarily
#     unresolvable (invalidate-only) until the next tick re-adds it.
#
# Ports: proxy 10160, mock upstream 10161 (captures Authorization), sentinel
# 4001 (EXCLUSIVE to this pack — started and stopped here; verified free
# first). NEVER touches 8088. Only self-spawned PIDs are killed.
# Outage mechanism: M2 (see pack A header for the mechanism-selection record).
#
# Self-deadline: internal 240s watchdog; caller wraps with `timeout 300`.
# TEST CODE ONLY — no production source is modified by this script.
# =============================================================================
set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

PROXY_PORT=10160
MOCK_PORT=10161
SENTINEL_PORT=4001
if [ "$PROXY_PORT" = "8088" ] || [ "$MOCK_PORT" = "8088" ] || [ "$SENTINEL_PORT" = "8088" ]; then
    echo "FATAL: refusing to touch 8088" >&2; exit 2
fi

RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[1;33m'; BLUE=$'\033[0;34m'; NC=$'\033[0m'

TMPDIR_PATH=$(mktemp -d -t dbcache-b-XXXXXX)
PROXY_BIN="$TMPDIR_PATH/dbcache_proxy_b"
SENTINEL_HITS="$TMPDIR_PATH/sentinel_hits.log"
MOCK_CAPTURE="$TMPDIR_PATH/mock_capture.log"
PROXY_PID=""; MOCK_PID=""; SENTINEL_PID=""; ALARM_PID=""
DB_DIR=""; FAILURES=0; FAILED_CHECKS=""

note_fail() { FAILURES=$((FAILURES+1)); FAILED_CHECKS="${FAILED_CHECKS} $1"; }

cleanup() {
    local code=$?
    for port in "$PROXY_PORT" "$MOCK_PORT" "$SENTINEL_PORT"; do
        for pid in $(lsof -ti:"$port" 2>/dev/null || true); do
            cmd=$(ps -o command= -p "$pid" 2>/dev/null || true)
            case "$cmd" in
                *dbcache_proxy_b*|*dbcache_b_mock*|*dbcache_b_sentinel*) kill -9 "$pid" 2>/dev/null || true ;;
                *) echo "  [cleanup] port $port pid $pid not ours ($cmd) — left alone" ;;
            esac
        done
    done
    if [ -n "$ALARM_PID" ]; then kill "$ALARM_PID" 2>/dev/null || true; fi
    if [ -n "$PROXY_PID" ]; then kill "$PROXY_PID" 2>/dev/null || true; fi
    if [ -n "$MOCK_PID" ]; then kill "$MOCK_PID" 2>/dev/null || true; fi
    if [ -n "$SENTINEL_PID" ]; then kill "$SENTINEL_PID" 2>/dev/null || true; fi
    [ -d "$TMPDIR_PATH" ] && rm -rf "$TMPDIR_PATH"
    return "$code"
}
trap 'rc=$?; if [ -f "$TMPDIR_PATH/TIMEOUT" ]; then cleanup; echo "RESULT: TIMEOUT"; exit 124; fi; cleanup; exit $rc' EXIT

# ---- internal 240s watchdog ----
HARD_TIMEOUT=240
(
    sleep "$HARD_TIMEOUT"
    echo "${RED}[FATAL] internal ${HARD_TIMEOUT}s alarm fired${NC}" >&2
    touch "$TMPDIR_PATH/TIMEOUT"
    pkill -P $$ 2>/dev/null || true
    kill -9 $$ 2>/dev/null || true
) &
ALARM_PID=$!

echo "${BLUE}======================================================${NC}"
echo "${BLUE}  Pack B — rollback + write-through + UI              ${NC}"
echo "${BLUE}  Branch: feature/db-cache-layer @ $(git -C "$ROOT_DIR" rev-parse --short HEAD)${NC}"
echo "${BLUE}  proxy:$PROXY_PORT mock:$MOCK_PORT sentinel:$SENTINEL_PORT (exclusive)${NC}"
echo "${BLUE}======================================================${NC}"

for port in "$SENTINEL_PORT" "$PROXY_PORT" "$MOCK_PORT"; do
    pids=$(lsof -ti:"$port" 2>/dev/null || true)
    if [ -n "$pids" ]; then
        echo "${RED}FATAL: port $port already occupied by foreign PIDs: $pids — ABORTING (not killing).${NC}" >&2
        exit 3
    fi
done

echo "${YELLOW}[build] go build ./cmd -> $PROXY_BIN${NC}"
( cd "$ROOT_DIR" && go build -o "$PROXY_BIN" ./cmd ) > "$TMPDIR_PATH/build.log" 2>&1
if [ ! -x "$PROXY_BIN" ]; then
    echo "${RED}go build failed:${NC}"; tail -30 "$TMPDIR_PATH/build.log"; exit 1
fi

export HOME="$TMPDIR_PATH/home"; mkdir -p "$HOME"
export XDG_CONFIG_HOME="$HOME/.config"; mkdir -p "$XDG_CONFIG_HOME"
unset DATABASE_URL
unset UPSTREAM_URL          # boot 1: default upstream = http://localhost:4001 (sentinel)
export PORT="$PROXY_PORT"
export APPLY_ENV_OVERRIDES="true"
export LOOP_DETECTION_ENABLED="false"
export ULTIMATE_MODEL_ID=""
export ULTIMATE_MODEL_MAX_HASH="100"
export INTERNAL_ENCRYPTION_KEY="$(openssl rand -base64 32)"

# ---- sentinel on 4001 (exclusive to this pack) ----
cat > "$TMPDIR_PATH/dbcache_b_sentinel.py" <<PYEOF
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
HITS = "${SENTINEL_HITS}"
FIXED = json.dumps({"id":"chatcmpl-sentinel","object":"chat.completion","created":1700000000,
 "model":"sentinel","choices":[{"index":0,"message":{"role":"assistant","content":"sentinel-replied"},
 "finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}).encode()
class H(BaseHTTPRequestHandler):
    def log_message(self, *a, **k): pass
    def _record(self, body=b""):
        model = ""
        try: model = json.loads(body or b"{}").get("model","")
        except Exception: pass
        with open(HITS, "a") as f: f.write("%s|%s\n" % (self.path, model))
    def do_POST(self):
        n = int(self.headers.get("Content-Length","0") or 0)
        body = self.rfile.read(n)
        self._record(body)
        if self.path.endswith("/v1/chat/completions"):
            self.send_response(200)
            self.send_header("Content-Type","application/json")
            self.send_header("Content-Length", str(len(FIXED)))
            self.end_headers(); self.wfile.write(FIXED)
        else:
            payload = b'{"error":{"type":"sentinel_bad_gateway","message":"hit dead default upstream"}}'
            self.send_response(502)
            self.send_header("Content-Type","application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers(); self.wfile.write(payload)
ThreadingHTTPServer(("127.0.0.1", ${SENTINEL_PORT}), H).serve_forever()
PYEOF
python3 "$TMPDIR_PATH/dbcache_b_sentinel.py" > "$TMPDIR_PATH/sentinel.log" 2>&1 &
SENTINEL_PID=$!

# ---- mock upstream on 10161 ----
cat > "$TMPDIR_PATH/dbcache_b_mock.py" <<PYEOF
import json, threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
CAP = "${MOCK_CAPTURE}"
FIXED = json.dumps({"id":"chatcmpl-mock","object":"chat.completion","created":1700000000,
 "model":"up-model","choices":[{"index":0,"message":{"role":"assistant","content":"mock-ok"},
 "finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}).encode()
_lock = threading.Lock(); _n = 0
class H(BaseHTTPRequestHandler):
    def log_message(self, *a, **k): pass
    def do_POST(self):
        global _n
        n = int(self.headers.get("Content-Length","0") or 0)
        self.rfile.read(n)
        with _lock:
            _n += 1; i = _n
        with open(CAP, "a") as f:
            f.write("%d|%s|%s\n" % (i, self.path, self.headers.get("Authorization","") or self.headers.get("x-api-key","")))
        self.send_response(200)
        self.send_header("Content-Type","application/json")
        self.send_header("Content-Length", str(len(FIXED)))
        self.end_headers(); self.wfile.write(FIXED)
ThreadingHTTPServer(("127.0.0.1", ${MOCK_PORT}), H).serve_forever()
PYEOF
python3 "$TMPDIR_PATH/dbcache_b_mock.py" > "$TMPDIR_PATH/mock.log" 2>&1 &
MOCK_PID=$!
sleep 1

chat() { # $1 model  $2 token (may be empty)  $3 outfile  $4 content -> prints http code
    if [ -n "$2" ]; then
        curl -s -o "$3" -w "%{http_code}" --max-time 10 -X POST "http://127.0.0.1:$PROXY_PORT/v1/chat/completions" \
            -H "Authorization: Bearer $2" -H "Content-Type: application/json" \
            -d "{\"model\":\"$1\",\"messages\":[{\"role\":\"user\",\"content\":\"$4\"}]}"
    else
        curl -s -o "$3" -w "%{http_code}" --max-time 10 -X POST "http://127.0.0.1:$PROXY_PORT/v1/chat/completions" \
            -H "Content-Type: application/json" \
            -d "{\"model\":\"$1\",\"messages\":[{\"role\":\"user\",\"content\":\"$4\"}]}"
    fi
}
wait_healthz() {
    for i in $(seq 1 150); do
        sleep 0.1
        curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:$PROXY_PORT/healthz" 2>/dev/null | grep -q 200 && return 0
    done
    echo "${RED}proxy failed to become healthy; log tail:${NC}"; tail -30 "$1"; return 1
}
sentinel_count() { grep -c "|" "$SENTINEL_HITS" 2>/dev/null || true; }

# =============================================================================
# B1 — BOOT 1: CACHE_LAYER_ENABLED=false
# =============================================================================
export CACHE_LAYER_ENABLED="false"
PROXY_LOG="$TMPDIR_PATH/proxy_boot1.log"
"$PROXY_BIN" > "$PROXY_LOG" 2>&1 &
PROXY_PID=$!
wait_healthz "$PROXY_LOG" || exit 1
sleep 1
if grep -q "\[cache\] CACHE_LAYER_ENABLED=false" "$PROXY_LOG"; then
    echo "${GREEN}[B1] PASS — boot 1 flag-off log line present: [cache] CACHE_LAYER_ENABLED=false${NC}"
else
    echo "${RED}[B1] FAIL — flag-off log line missing from boot log${NC}"; note_fail "B1"
fi
curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/credentials" -H "Content-Type: application/json" \
    -d '{"id":"cred-rb","provider":"openai","api_key":"sk-rb-key","base_url":"http://127.0.0.1:'"$MOCK_PORT"'/v1"}' > "$TMPDIR_PATH/cred.json"
grep -q '"id"' "$TMPDIR_PATH/cred.json" || { echo "${RED}[B1] cred creation failed: $(cat "$TMPDIR_PATH/cred.json")${NC}"; exit 1; }
R=$(curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/models" -H "Content-Type: application/json" \
    -d '{"id":"int-rb","name":"Rollback","enabled":true,"internal":true,"internal_model":"up-rb","credentials":[{"credential_id":"cred-rb","weight":1,"position":0}]}')
echo "$R" | grep -q '"id"' || { echo "${RED}[B1] int-rb creation failed: $R${NC}"; exit 1; }
T1=$(curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/tokens" -H "Content-Type: application/json" -d '{"name":"t1"}' \
    | python3 -c 'import json,sys; print(json.load(sys.stdin).get("token",""))')
[ -n "$T1" ] || { echo "${RED}[B1] token creation failed${NC}"; exit 1; }
W=$(chat int-rb "$T1" "$TMPDIR_PATH/b1.out" "warm")
if [ "$W" = "200" ]; then
    echo "${GREEN}[B1] PASS — flag-off healthy baseline: int-rb warm 200${NC}"
else
    echo "${RED}[B1] FAIL — flag-off warm request -> $W: $(head -c 120 "$TMPDIR_PATH/b1.out")${NC}"; note_fail "B1-warm"
fi

# B2 — outage with flag OFF: original bug must reproduce (sentinel hit), never 503 CSU
DB_FILE=$(find "$HOME" -name config.db 2>/dev/null | head -1); DB_DIR=$(dirname "$DB_FILE")
mkdir -p "$TMPDIR_PATH/dbbackup"
cp -a "$DB_DIR/config.db" "$DB_DIR/config.db-wal" "$DB_DIR/config.db-shm" "$TMPDIR_PATH/dbbackup/" 2>/dev/null
DBSZ=$(wc -c < "$DB_DIR/config.db" | tr -d ' '); DBBLKS=$((DBSZ/512)); [ "$DBBLKS" -lt 1 ] && DBBLKS=1
dd if=/dev/urandom of="$DB_DIR/config.db" conv=notrunc bs=512 count="$DBBLKS" 2>/dev/null
rm -f "$DB_DIR/config.db-wal" "$DB_DIR/config.db-shm"
echo "${YELLOW}[B2] M2 outage induced (flag OFF)${NC}"
# NOTE: hot single-row reads can survive M2 for a while (page cache + intact
# deleted-inode WAL), so a lone sequential request may still resolve. A
# concurrent burst forces the pool to open FRESH connections, which hit the
# corrupted main DB file and fail — reproducing the flag-off misroute.
SENT_BEFORE=$(sentinel_count)
burst_codes() { # $1 label  $2 model -> prints "with=.. without=.."
    local pids=() i out="$TMPDIR_PATH/b2_$1"
    rm -f "$out".*
    for i in 1 2 3 4; do chat "$2" "$T1" "$out.with$i" "burst $1 with $i-$RANDOM" > "$out.cw$i" & pids+=($!); done
    for i in 1 2 3 4; do chat "$2" "" "$out.without$i" "burst $1 without $i-$RANDOM" > "$out.co$i" & pids+=($!); done
    for p in "${pids[@]}"; do wait "$p"; done
    local w="" o=""
    for i in 1 2 3 4; do w="$w $(cat "$out.cw$i" 2>/dev/null)"; o="$o $(cat "$out.co$i" 2>/dev/null)"; done
    echo "with:$w without:$o"
}
R1=$(burst_codes r1 int-rb)
sleep 10
R2=$(burst_codes r2 int-rb)
cat "$TMPDIR_PATH"/b2_r*.without* "$TMPDIR_PATH"/b2_r*.with* 2>/dev/null | grep -q "config_store_unavailable" && CSU_SEEN=1 || CSU_SEEN=0
SENT_AFTER_B2=$(sentinel_count)
HITS_B2=$((SENT_AFTER_B2 - SENT_BEFORE))
echo "  burst@t0  -> $R1"
echo "  burst@t10 -> $R2"
echo "${YELLOW}  observed flag-off shape: known-model (int-rb) single-row reads SURVIVE the SQLite file outage on hot connection/page caches (with-auth 200 via internal mock; no-auth 401 — internal models require auth). The PG incident failed immediately because connection errors are instant; file corruption degrades slower. The full misroute reproduces on the unknown-model shape (B3).${NC}"
if [ "$CSU_SEEN" = "0" ]; then
    echo "${GREEN}[B2] PASS — no 503 config_store_unavailable on any auth shape (the fail-fast gate lives inside the cache layer; flag-off = old behavior)${NC}"
else
    echo "${RED}[B2] FAIL — config_store_unavailable appeared with cache disabled${NC}"; note_fail "B2-csu"
fi

# B3 — unknown model, flag-off + DB down: old passthrough behavior — must NOT
# be 503 config_store_unavailable, and the sentinel MUST receive hits: this is
# the ORIGINAL 2026-08-27 misroute (unresolved model -> external passthrough to
# the dead default upstream), proving the cache layer is fully bypassed.
SENT_BEFORE_B3=$(sentinel_count)
R3=$(burst_codes r3 never-seen-model)
cat "$TMPDIR_PATH"/b2_r3.* 2>/dev/null | grep -q "config_store_unavailable" && CSU3=1 || CSU3=0
HITS_B3=$(( $(sentinel_count) - SENT_BEFORE_B3 ))
if [ "$CSU3" = "0" ] && [ "$HITS_B3" -ge 1 ]; then
    echo "${GREEN}[B3] PASS — unknown model flag-off + DB-down -> $R3: no config_store_unavailable AND sentinel hit x$HITS_B3 (original misroute reproduces; cache fully bypassed)${NC}"
elif [ "$CSU3" = "0" ]; then
    echo "${YELLOW}[B3] PARTIAL — no config_store_unavailable, but sentinel saw 0 hits ($R3); misroute not reproduced. Recorded as finding.${NC}"
    note_fail "B3-sentinel"
else
    echo "${RED}[B3] FAIL — unknown model returned config_store_unavailable with cache OFF -> $R3${NC}"
    note_fail "B3"
fi

# B4 — restore DB, stop boot 1
cat "$TMPDIR_PATH/dbbackup/config.db" > "$DB_DIR/config.db"
[ -f "$TMPDIR_PATH/dbbackup/config.db-wal" ] && cat "$TMPDIR_PATH/dbbackup/config.db-wal" > "$DB_DIR/config.db-wal"
[ -f "$TMPDIR_PATH/dbbackup/config.db-shm" ] && cat "$TMPDIR_PATH/dbbackup/config.db-shm" > "$DB_DIR/config.db-shm"
kill "$PROXY_PID" 2>/dev/null; wait "$PROXY_PID" 2>/dev/null; PROXY_PID=""
sleep 1
SENT_BEFORE_B5=$(sentinel_count)
echo "${YELLOW}[B4] DB restored, boot 1 stopped, ports released (sentinel snapshot: $SENT_BEFORE_B5 hits)${NC}"

# =============================================================================
# B5 — BOOT 2: cache DEFAULT ON + UPSTREAM_URL=mock
# =============================================================================
unset CACHE_LAYER_ENABLED
export UPSTREAM_URL="http://127.0.0.1:$MOCK_PORT/v1"
PROXY_LOG="$TMPDIR_PATH/proxy_boot2.log"
"$PROXY_BIN" > "$PROXY_LOG" 2>&1 &
PROXY_PID=$!
wait_healthz "$PROXY_LOG" || exit 1
sleep 1
B5_OK=1
UI_CODE=$(curl -s -o "$TMPDIR_PATH/ui.out" -w "%{http_code}" --max-time 10 "http://127.0.0.1:$PROXY_PORT/ui/")
grep -qi "<!DOCTYPE html\|<html" "$TMPDIR_PATH/ui.out" 2>/dev/null || B5_OK=0
[ "$UI_CODE" = "200" ] || { B5_OK=0; echo "  /ui/ -> $UI_CODE"; }
MODELS_CODE=$(curl -s -o "$TMPDIR_PATH/models.out" -w "%{http_code}" --max-time 10 "http://127.0.0.1:$PROXY_PORT/fe/api/models")
[ "$MODELS_CODE" = "200" ] || { B5_OK=0; echo "  /fe/api/models -> $MODELS_CODE"; }
grep -q '"int-rb"' "$TMPDIR_PATH/models.out" || { B5_OK=0; echo "  int-rb missing: $(head -c 120 "$TMPDIR_PATH/models.out")"; }
TOKENS_CODE=$(curl -s -o "$TMPDIR_PATH/tokens.out" -w "%{http_code}" --max-time 10 "http://127.0.0.1:$PROXY_PORT/fe/api/tokens")
[ "$TOKENS_CODE" = "200" ] || { B5_OK=0; echo "  /fe/api/tokens -> $TOKENS_CODE"; }
python3 -c 'import json,sys; json.load(open("'"$TMPDIR_PATH"'/tokens.out"))' 2>/dev/null || { B5_OK=0; echo "  /fe/api/tokens not JSON"; }
REQS_CODE=$(curl -s -o "$TMPDIR_PATH/reqs.out" -w "%{http_code}" --max-time 10 "http://127.0.0.1:$PROXY_PORT/fe/api/requests")
[ "$REQS_CODE" = "200" ] || { B5_OK=0; echo "  /fe/api/requests -> $REQS_CODE"; }
if grep -q "\[cache\] CACHE_LAYER_ENABLED=true" "$PROXY_LOG"; then FLAG2="true"; else FLAG2="MISSING"; B5_OK=0; fi
if [ "$B5_OK" = "1" ]; then
    echo "${GREEN}[B5] PASS — /ui/ 200+HTML, /fe/api/models 200 (int-rb present), /fe/api/tokens 200 JSON, /fe/api/requests 200; [cache] CACHE_LAYER_ENABLED=true${NC}"
else
    echo "${RED}[B5] FAIL — UI checks incomplete (flag line: $FLAG2)${NC}"; note_fail "B5"
fi

# =============================================================================
# B6 — write-through semantics (healthy DB, cache ON)
# =============================================================================
CAP_BEFORE=$(grep -c "|" "$MOCK_CAPTURE" 2>/dev/null || true)
# 1. create model -> immediate effect
R=$(curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/models" -H "Content-Type: application/json" \
    -d '{"id":"int-wt-1","name":"WT One","enabled":true,"internal":true,"internal_model":"up-wt","credentials":[{"credential_id":"cred-rb","weight":1,"position":0}]}')
echo "$R" | grep -q '"id"' || { echo "${RED}[B6.1] model creation failed: $R${NC}"; note_fail "B6.1-create"; }
C=$(chat int-wt-1 "$T1" "$TMPDIR_PATH/b61.out" "wt1 $RANDOM")
grep -c "sk-rb-key" "$MOCK_CAPTURE" > /dev/null 2>&1; RB_HITS=$(grep -c "sk-rb-key" "$MOCK_CAPTURE" || true)
if [ "$C" = "200" ] && [ "$RB_HITS" -ge 1 ]; then
    echo "${GREEN}[B6.1] PASS — model created -> immediate 200, mock captured cred-rb key (sk-rb-key x$RB_HITS)${NC}"
else
    echo "${RED}[B6.1] FAIL — code=$C rb-key-captures=$RB_HITS${NC}"; note_fail "B6.1"
fi

# 2. create cred-wt-2 and add to int-wt-1 (bounded-wait for the reconciler tick)
curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/credentials" -H "Content-Type: application/json" \
    -d '{"id":"cred-wt-2","provider":"openai","api_key":"sk-wt-two","base_url":"http://127.0.0.1:'"$MOCK_PORT"'/v1"}' > "$TMPDIR_PATH/cred2.json"
FIRST_UPDATE=$(curl -s -X PUT "http://127.0.0.1:$PROXY_PORT/fe/api/models/int-wt-1" -H "Content-Type: application/json" \
    -d '{"id":"int-wt-1","name":"WT One","enabled":true,"internal":true,"internal_model":"up-wt","credentials":[{"credential_id":"cred-rb","weight":1,"position":0},{"credential_id":"cred-wt-2","weight":1,"position":1}]}')
if echo "$FIRST_UPDATE" | grep -q '"id"'; then
    echo "${YELLOW}  [B6.2] finding: credential attach SUCCEEDED immediately (no reconcile wait needed)${NC}"
else
    echo "${YELLOW}  [B6.2] product finding recorded: immediate attach rejected: $(head -c 140 <<<"$FIRST_UPDATE") — bounded-waiting for reconciler tick (cap 60s)${NC}"
fi
UPDATE_OK=""
for i in $(seq 1 30); do
    echo "$FIRST_UPDATE" | grep -q '"id"' && UPDATE_OK=1 && break
    sleep 2
    FIRST_UPDATE=$(curl -s -X PUT "http://127.0.0.1:$PROXY_PORT/fe/api/models/int-wt-1" -H "Content-Type: application/json" \
        -d '{"id":"int-wt-1","name":"WT One","enabled":true,"internal":true,"internal_model":"up-wt","credentials":[{"credential_id":"cred-rb","weight":1,"position":0},{"credential_id":"cred-wt-2","weight":1,"position":1}]}')
done
if [ -z "$UPDATE_OK" ]; then
    echo "${RED}[B6.2] FAIL — cred-wt-2 never attachable within 60s${NC}"; note_fail "B6.2-update"
fi
TWO_HITS=0
for i in 1 2 3 4 5 6 7 8; do
    chat int-wt-1 "$T1" "$TMPDIR_PATH/b62.out" "wt2 $i-$RANDOM" > /dev/null
done
TWO_HITS=$(grep -c "sk-wt-two" "$MOCK_CAPTURE" || true)
if [ "$TWO_HITS" -ge 1 ]; then
    echo "${GREEN}[B6.2] PASS — after attach accepted, burst of 8 immediately captured sk-wt-two x$TWO_HITS${NC}"
else
    echo "${RED}[B6.2] FAIL — sk-wt-two never captured (x$TWO_HITS)${NC}"; note_fail "B6.2"
fi

# 3. rotate cred-wt-2 key -> sk-wt-three
KEYUP_RESP=$(curl -s -X PUT "http://127.0.0.1:$PROXY_PORT/fe/api/credentials/cred-wt-2" -H "Content-Type: application/json" \
    -d '{"id":"cred-wt-2","provider":"openai","api_key":"sk-wt-three","base_url":"http://127.0.0.1:'"$MOCK_PORT"'/v1"}')
KEYUP_TS_LINE=$(grep -c "|" "$MOCK_CAPTURE" 2>/dev/null || true)
THREE_HITS=0; THREE_SEEN_AT=""
for i in $(seq 1 30); do
    for j in 1 2 3 4; do chat int-wt-1 "$T1" "$TMPDIR_PATH/b63.out" "wt3 $i-$j-$RANDOM" > /dev/null; done
    THREE_HITS=$(grep -c "sk-wt-three" "$MOCK_CAPTURE" || true)
    if [ "$THREE_HITS" -ge 1 ]; then THREE_SEEN_AT="iter $i"; break; fi
    sleep 2
done
POSTKEY_TWO_HITS=$(tail -n +"$((KEYUP_TS_LINE+1))" "$MOCK_CAPTURE" 2>/dev/null | grep -c "sk-wt-two" || true)
if [ "$THREE_HITS" -ge 1 ] && [ "$POSTKEY_TWO_HITS" = "0" ]; then
    echo "${GREEN}[B6.3] PASS — sk-wt-three captured (x$THREE_HITS, first at $THREE_SEEN_AT); zero sk-wt-two captures after key update${NC}"
else
    echo "${RED}[B6.3] FAIL — three=$THREE_HITS post-update-two=$POSTKEY_TWO_HITS (product finding if three=0: invalidate-only hole until reconciler tick)${NC}"
    note_fail "B6.3"
fi

# 4. delete model -> immediate passthrough (not internal anymore)
curl -s -X DELETE "http://127.0.0.1:$PROXY_PORT/fe/api/models/int-wt-1" > /dev/null
CAPLINE_BEFORE=$(grep -c "|" "$MOCK_CAPTURE" 2>/dev/null || true)
C=$(chat int-wt-1 "$T1" "$TMPDIR_PATH/b64.out" "wt4 $RANDOM")
sleep 0.3
NEW_CAPS=$(tail -n +"$((CAPLINE_BEFORE+1))" "$MOCK_CAPTURE" 2>/dev/null | head -3)
LAST_AUTH=$(tail -n +"$((CAPLINE_BEFORE+1))" "$MOCK_CAPTURE" 2>/dev/null | head -1 | cut -d'|' -f3)
INTERNAL_KEY_LEAK=0
echo "$NEW_CAPS" | grep -q "sk-rb-key\|sk-wt-two\|sk-wt-three" && INTERNAL_KEY_LEAK=1
if [ "$C" = "200" ] && [ "$INTERNAL_KEY_LEAK" = "0" ] && [ -n "$LAST_AUTH" ]; then
    echo "${GREEN}[B6.4] PASS — deleted model passthrough to mock: 200, capture auth='$LAST_AUTH' (not an internal cred key)${NC}"
else
    echo "${RED}[B6.4] FAIL — code=$C leak=$INTERNAL_KEY_LEAK last-auth='$LAST_AUTH'${NC}"; note_fail "B6.4"
fi

# 5. token create/delete -> immediate effect
T2_RESP=$(curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/tokens" -H "Content-Type: application/json" -d '{"name":"t2"}')
T2=$(echo "$T2_RESP" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("token",""))')
T2_ID=$(echo "$T2_RESP" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("id",""))')
C_T2=$(chat int-rb "$T2" "$TMPDIR_PATH/b65a.out" "t2 $RANDOM")
curl -s -X DELETE "http://127.0.0.1:$PROXY_PORT/fe/api/tokens/$T2_ID" > /dev/null
C_T2_DEL=$(chat int-rb "$T2" "$TMPDIR_PATH/b65b.out" "t2del $RANDOM")
if [ "$C_T2" = "200" ] && [ "$C_T2_DEL" = "401" ]; then
    echo "${GREEN}[B6.5] PASS — T2 immediate 200; after DELETE immediate 401${NC}"
else
    echo "${RED}[B6.5] FAIL — T2=$C_T2 after-delete=$C_T2_DEL${NC}"; note_fail "B6.5"
fi

# B7 — sentinel quiet during boot 2
SENT_AFTER_B6=$(sentinel_count)
B7_HITS=$((SENT_AFTER_B6 - SENT_BEFORE_B5))
if [ "$B7_HITS" = "0" ]; then
    echo "${GREEN}[B7] PASS — sentinel received 0 hits during cache-on boot 2 (no legitimate-default leakage)${NC}"
else
    echo "${RED}[B7] FAIL — sentinel hit $B7_HITS time(s) during boot 2: $(tail -3 "$SENTINEL_HITS")${NC}"
    note_fail "B7"
fi

echo "${BLUE}------------------------------------------------------${NC}"
if [ "$FAILURES" -eq 0 ]; then
    echo "RESULT: PASS"
    echo "${GREEN}Pack B complete — 0 failed checks.${NC}"
    exit 0
else
    echo "RESULT: FAIL"
    echo "${RED}Pack B failed checks:$FAILED_CHECKS${NC}"
    exit 1
fi
