#!/usr/bin/env bash
# =============================================================================
# E2E Pack A — DB-cache-layer outage close-out (feature/db-cache-layer @ 42d98f3)
#
# Replays the 2026-08-27 incident scenario against a REAL proxy process with a
# real SQLite DB: PG outage made GetModel return nil -> internal models treated
# as EXTERNAL -> passthrough to the dead default http://localhost:4001 -> 42
# failed requests. This pack proves the pkg/modelscache fix: through a real DB
# outage, known models keep resolving (cache serving), unknown models fail-fast
# 503 config_store_unavailable (never a silent misroute), the reconciler logs
# the strict-read failure, and the system recovers after DB restore.
#
# Ports: proxy 10150, mock upstream 10151 (captures Authorization), sentinel
# 4001. Port safety: all four are checked BEFORE anything starts — if any is
# occupied by a foreign process the pack ABORTS loudly. NEVER touches 8088.
# Only PIDs spawned by this script are killed (command-pattern verified).
#
# HARNESS DESIGN NOTES (empirically verified on this branch — see the pack's
# companion report):
#   1. Outage mechanism M2: overwrite config.db in place with same-size
#      garbage + rm the -wal/-shm. Driver error observed in proxy log:
#      "file is not a database (26)" (SQLITE_NOTADB). Rejected: M1 (rm+mkdir —
#      the pool keeps serving from deleted inodes: reads never fail, no WARN,
#      no 503; only usage WRITES fail "unable to open database file: out of
#      memory (14)"), M3 (truncate — SIGBUS process crash: modernc.org/sqlite
#      mmaps the -shm), M4 (chmod 000 dir — no effect on open fds).
#   2. Two-boot seeding: the tripwire WARN (criterion K) is boot-only and
#      requires enabled models to exist at boot, and freshly created
#      credentials are only visible to model validation after a reconciler
#      tick (credential mutators are invalidate-only in the cache). So boot 0
#      seeds creds -> waits one tick -> seeds models + tokens; the measured
#      boot then starts with a fully primed DB.
#   3. Sentinel dual-mode: POST /v1/chat/completions is answered 200 with a
#      minimal valid completion; every other request gets the incident-style
#      502. Rationale: non-internal models route to the GLOBAL upstream
#      (race_executor.go: cfg.UpstreamURL — model credentials are NOT used on
#      the external path), so ext-known traffic legitimately lands on
#      localhost:4001 when UPSTREAM_URL is unset. Misroute detection is by
#      per-hit model logging, not by hit count: A7 asserts zero sentinel hits
#      whose model is NOT ext-known.
#   4. A8 restarts the proxy after the in-place DB restore. A live process
#      NEVER recovers from M2 corruption even after restore (poisoned
#      connection state degrades to "database disk image is malformed (11)"
#      forever — verified empirically); restart is the ops-realistic recovery.
#      The ≤120s reconciler-pickup assertion (criterion H) is still exercised
#      on the restarted process via an out-of-band sqlite3 INSERT.
#
# Self-deadline: internal 280s watchdog; caller wraps with `timeout 300`.
# TEST CODE ONLY — no production source is modified by this script.
# =============================================================================
set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

PROXY_PORT=10150
MOCK_PORT=10151
SENTINEL_PORT=4001
if [ "$PROXY_PORT" = "8088" ] || [ "$MOCK_PORT" = "8088" ] || [ "$SENTINEL_PORT" = "8088" ]; then
    echo "FATAL: refusing to touch 8088" >&2; exit 2
fi

RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[1;33m'; BLUE=$'\033[0;34m'; NC=$'\033[0m'

TMPDIR_PATH=$(mktemp -d -t dbcache-a-XXXXXX)
PROXY_BIN="$TMPDIR_PATH/dbcache_proxy_a"
SENTINEL_HITS="$TMPDIR_PATH/sentinel_hits.log"      # lines: <path>|<model>
MOCK_CAPTURE="$TMPDIR_PATH/mock_capture.log"        # lines: <n>|<path>|<auth>
PROXY_LOG=""                                        # set per boot
PROXY_PID=""; MOCK_PID=""; SENTINEL_PID=""; ALARM_PID=""
DB_DIR=""; FAILURES=0; FAILED_CHECKS=""
T0=0

note_fail() { FAILURES=$((FAILURES+1)); FAILED_CHECKS="${FAILED_CHECKS} $1"; }

cleanup() {
    local code=$?
    # Kill only what we spawned, lsof-verified by command pattern (binary and
    # helper names are unique to this run's TMPDIR — never a foreign process,
    # never the script itself, never anything on 8088).
    for port in "$PROXY_PORT" "$MOCK_PORT" "$SENTINEL_PORT"; do
        for pid in $(lsof -ti:"$port" 2>/dev/null || true); do
            cmd=$(ps -o command= -p "$pid" 2>/dev/null || true)
            case "$cmd" in
                *dbcache_proxy_a*|*dbcache_a_mock*|*dbcache_a_sentinel*) kill -9 "$pid" 2>/dev/null || true ;;
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

# ---- internal 280s watchdog ----
HARD_TIMEOUT=280
(
    sleep "$HARD_TIMEOUT"
    echo "${RED}[FATAL] internal ${HARD_TIMEOUT}s alarm fired${NC}" >&2
    touch "$TMPDIR_PATH/TIMEOUT"
    pkill -P $$ 2>/dev/null || true
    kill -9 $$ 2>/dev/null || true
) &
ALARM_PID=$!

echo "${BLUE}======================================================${NC}"
echo "${BLUE}  Pack A — DB-cache-layer outage close-out            ${NC}"
echo "${BLUE}  Branch: feature/db-cache-layer @ $(git -C "$ROOT_DIR" rev-parse --short HEAD)${NC}"
echo "${BLUE}  proxy:$PROXY_PORT mock:$MOCK_PORT sentinel:$SENTINEL_PORT${NC}"
echo "${BLUE}======================================================${NC}"

# ---- strict port isolation (pre-flight) ----
for port in "$SENTINEL_PORT" "$PROXY_PORT" "$MOCK_PORT"; do
    pids=$(lsof -ti:"$port" 2>/dev/null || true)
    if [ -n "$pids" ]; then
        echo "${RED}FATAL: port $port already occupied by foreign PIDs: $pids — ABORTING (not killing).${NC}" >&2
        exit 3
    fi
done

# ---- build proxy binary (default HOME for module cache) ----
echo "${YELLOW}[build] go build ./cmd -> $PROXY_BIN${NC}"
( cd "$ROOT_DIR" && go build -o "$PROXY_BIN" ./cmd ) > "$TMPDIR_PATH/build.log" 2>&1
if [ ! -x "$PROXY_BIN" ]; then
    echo "${RED}go build failed:${NC}"; tail -30 "$TMPDIR_PATH/build.log"
    exit 1
fi

# ---- isolated HOME for runtime ----
export HOME="$TMPDIR_PATH/home"; mkdir -p "$HOME"
export XDG_CONFIG_HOME="$HOME/.config"; mkdir -p "$XDG_CONFIG_HOME"
unset DATABASE_URL
unset UPSTREAM_URL   # deliberately: global default upstream = http://localhost:4001 (sentinel)
export PORT="$PROXY_PORT"
export APPLY_ENV_OVERRIDES="true"
export LOOP_DETECTION_ENABLED="false"
export ULTIMATE_MODEL_ID=""
export ULTIMATE_MODEL_MAX_HASH="100"
export INTERNAL_ENCRYPTION_KEY="$(openssl rand -base64 32)"
echo "${YELLOW}[env] isolated HOME=$HOME; SQLite dialect; UPSTREAM_URL unset (default 4001)${NC}"

# ---- sentinel on 4001 (incident dead-default stand-in; dual-mode) ----
cat > "$TMPDIR_PATH/dbcache_a_sentinel.py" <<PYEOF
import json, os
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
    def do_GET(self):
        self._record()
        payload = b'{"error":{"type":"sentinel_bad_gateway","message":"hit dead default upstream"}}'
        self.send_response(502)
        self.send_header("Content-Type","application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers(); self.wfile.write(payload)
ThreadingHTTPServer(("127.0.0.1", ${SENTINEL_PORT}), H).serve_forever()
PYEOF
python3 "$TMPDIR_PATH/dbcache_a_sentinel.py" > "$TMPDIR_PATH/sentinel.log" 2>&1 &
SENTINEL_PID=$!

# ---- mock upstream on 10151 (captures every request's path + Authorization) ----
cat > "$TMPDIR_PATH/dbcache_a_mock.py" <<PYEOF
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
python3 "$TMPDIR_PATH/dbcache_a_mock.py" > "$TMPDIR_PATH/mock.log" 2>&1 &
MOCK_PID=$!
sleep 1

# ---- request helpers ----
chat() { # $1 model  $2 token  $3 outfile  $4 content -> prints http code
    curl -s -o "$3" -w "%{http_code}" --max-time 10 -X POST "http://127.0.0.1:$PROXY_PORT/v1/chat/completions" \
        -H "Authorization: Bearer $2" -H "Content-Type: application/json" \
        -d "{\"model\":\"$1\",\"messages\":[{\"role\":\"user\",\"content\":\"$4\"}]}"
}
anth() { # $1 model  $2 token  $3 outfile  $4 content -> prints http code
    curl -s -o "$3" -w "%{http_code}" --max-time 10 -X POST "http://127.0.0.1:$PROXY_PORT/v1/messages" \
        -H "x-api-key: $2" -H "anthropic-version: 2023-06-01" -H "Content-Type: application/json" \
        -d "{\"model\":\"$1\",\"max_tokens\":64,\"messages\":[{\"role\":\"user\",\"content\":\"$4\"}]}"
}
wait_healthz() { # $1 pid-to-blame-label
    for i in $(seq 1 150); do
        sleep 0.1
        curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:$PROXY_PORT/healthz" 2>/dev/null | grep -q 200 && return 0
    done
    echo "${RED}proxy failed to become healthy; log tail:${NC}"; tail -30 "$PROXY_LOG"
    return 1
}

# =============================================================================
# BOOT 0 (seed phase): creds -> one reconciler tick -> models + tokens
# =============================================================================
PROXY_LOG="$TMPDIR_PATH/proxy_boot0.log"
"$PROXY_BIN" > "$PROXY_LOG" 2>&1 &
PROXY_PID=$!
wait_healthz "boot0" || exit 1
echo "${YELLOW}[seed-boot] creating 3 credentials (base_url -> mock 10151)...${NC}"
for pair in "cred-single:sk-mock-single-key" "cred-a:sk-mock-a-key" "cred-b:sk-mock-b-key"; do
    cid="${pair%%:*}"; ckey="${pair#*:}"
    curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/credentials" -H "Content-Type: application/json" \
        -d "{\"id\":\"$cid\",\"provider\":\"openai\",\"api_key\":\"$ckey\",\"base_url\":\"http://127.0.0.1:$MOCK_PORT/v1\"}" \
        > "$TMPDIR_PATH/cred_$cid.json"
    grep -q '"id"' "$TMPDIR_PATH/cred_$cid.json" || { echo "${RED}credential $cid creation failed: $(cat "$TMPDIR_PATH/cred_$cid.json")${NC}"; exit 1; }
done
echo "${YELLOW}[seed-boot] waiting for credential visibility (reconciler tick; cap 60s)...${NC}"
CRED_OK=0
for i in $(seq 1 30); do
    sleep 2
    if curl -s "http://127.0.0.1:$PROXY_PORT/fe/api/credentials" | grep -q '"id": *"cred-b"'; then CRED_OK=1; break; fi
done
if [ "$CRED_OK" != "1" ]; then
    echo "${RED}credentials never became visible within 60s — cannot seed models${NC}"
    note_fail "seed-cred-visibility"
    exit 1
fi
echo "${YELLOW}[seed-boot] credentials visible after ~$((i*2))s — creating models...${NC}"
post_model() { # $1 json
    curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/models" -H "Content-Type: application/json" -d "$1"
}
R=$(post_model '{"id":"int-single","name":"Int Single","enabled":true,"internal":true,"internal_model":"up-single","credentials":[{"credential_id":"cred-single","weight":1,"position":0}]}')
echo "$R" | grep -q '"id"' || { echo "${RED}int-single creation failed: $R${NC}"; exit 1; }
R=$(post_model '{"id":"int-multi","name":"Int Multi","enabled":true,"internal":true,"internal_model":"up-multi","credentials":[{"credential_id":"cred-a","weight":1,"position":0},{"credential_id":"cred-b","weight":1,"position":1}]}')
echo "$R" | grep -q '"id"' || { echo "${RED}int-multi creation failed: $R${NC}"; exit 1; }
R=$(post_model '{"id":"ext-known","name":"Ext Known","enabled":true,"internal":false,"credentials":[{"credential_id":"cred-single","weight":1,"position":0}]}')
echo "$R" | grep -q '"id"' || { echo "${RED}ext-known creation failed: $R${NC}"; exit 1; }
TOK_RESP=$(curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/tokens" -H "Content-Type: application/json" -d '{"name":"t-valid"}')
T_VALID=$(echo "$TOK_RESP" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("token",""))')
T_VALID_ID=$(echo "$TOK_RESP" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("id",""))')
[ -n "$T_VALID" ] || { echo "${RED}T_valid creation failed: $TOK_RESP${NC}"; exit 1; }
TOK_RESP=$(curl -s -X POST "http://127.0.0.1:$PROXY_PORT/fe/api/tokens" -H "Content-Type: application/json" -d '{"name":"t-revoked"}')
T_REVOKED=$(echo "$TOK_RESP" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("token",""))')
T_REVOKED_ID=$(echo "$TOK_RESP" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("id",""))')
[ -n "$T_REVOKED" ] || { echo "${RED}T_revoked creation failed: $TOK_RESP${NC}"; exit 1; }
curl -s -X DELETE "http://127.0.0.1:$PROXY_PORT/fe/api/tokens/$T_REVOKED_ID" > /dev/null
echo "${YELLOW}[seed-boot] seeds complete: 3 creds, 3 models, T_valid live, T_revoked deleted. Stopping seed boot.${NC}"
kill "$PROXY_PID" 2>/dev/null; wait "$PROXY_PID" 2>/dev/null; PROXY_PID=""
sleep 1

DB_FILE=$(find "$HOME" -name config.db 2>/dev/null | head -1)
DB_DIR=$(dirname "$DB_FILE")
echo "${YELLOW}[db] DB at $DB_DIR${NC}"

# =============================================================================
# A0 — final boot + healthz + tripwire
# =============================================================================
PROXY_LOG="$TMPDIR_PATH/proxy_main.log"
"$PROXY_BIN" > "$PROXY_LOG" 2>&1 &
PROXY_PID=$!
wait_healthz "main" || exit 1
sleep 1   # let boot priming logs flush
TRIP=$(grep -c "development default http://localhost:4001" "$PROXY_LOG" || true)
if [ "$TRIP" -ge 1 ]; then
    echo "${GREEN}[A0] PASS — boot+healthz OK; tripwire WARN present ($TRIP lines) (criterion K)${NC}"
else
    echo "${RED}[A0] FAIL — boot OK but tripwire WARN 'development default http://localhost:4001' NOT in boot log${NC}"
    note_fail "A0"
fi

# A1 — verify seeds (seeded during boot 0; see header note 2)
MODELS_JSON=$(curl -s "http://127.0.0.1:$PROXY_PORT/fe/api/models")
CREDS_JSON=$(curl -s "http://127.0.0.1:$PROXY_PORT/fe/api/credentials")
A1_OK=1
for m in int-single int-multi ext-known; do echo "$MODELS_JSON" | grep -q "\"$m\"" || A1_OK=0; done
for c in cred-single cred-a cred-b; do echo "$CREDS_JSON" | grep -q "\"$c\"" || A1_OK=0; done
if [ "$A1_OK" = "1" ]; then
    echo "${GREEN}[A1] PASS — seeds verified: 3 models + 3 creds + T_valid (T_revoked revoked while healthy)${NC}"
else
    echo "${RED}[A1] FAIL — seed verification failed: models=$MODELS_JSON creds=$CREDS_JSON${NC}"
    note_fail "A1"
fi

# A2 — warm all models + /v1/messages
A2_OK=1
C1=$(chat int-single "$T_VALID" "$TMPDIR_PATH/a2_1.out" "warm single"); [ "$C1" = "200" ] || { A2_OK=0; echo "  int-single -> $C1"; }
C2=$(anth int-single "$T_VALID" "$TMPDIR_PATH/a2_2.out" "warm anthropic"); [ "$C2" = "200" ] || { A2_OK=0; echo "  /v1/messages int-single -> $C2"; }
MOCK_A_COUNT=0; MOCK_B_COUNT=0
for i in 1 2 3 4 5 6 7 8; do
    C=$(chat int-multi "$T_VALID" "$TMPDIR_PATH/a2_3.out" "warm multi $i-$RANDOM")
    [ "$C" = "200" ] || { A2_OK=0; echo "  int-multi #$i -> $C"; }
done
C4=$(chat ext-known "$T_VALID" "$TMPDIR_PATH/a2_4.out" "warm ext"); [ "$C4" = "200" ] || { A2_OK=0; echo "  ext-known -> $C4"; }
SINGLE_HITS=$(grep -c "sk-mock-single-key" "$MOCK_CAPTURE" || true)
MOCK_A_COUNT=$(grep -c "sk-mock-a-key" "$MOCK_CAPTURE" || true)
MOCK_B_COUNT=$(grep -c "sk-mock-b-key" "$MOCK_CAPTURE" || true)
if [ "$SINGLE_HITS" -lt 1 ] || [ "$MOCK_A_COUNT" -lt 1 ] || [ "$MOCK_B_COUNT" -lt 1 ]; then
    A2_OK=0; echo "  mock capture incomplete: single=$SINGLE_HITS a=$MOCK_A_COUNT b=$MOCK_B_COUNT"
fi
if [ "$A2_OK" = "1" ]; then
    echo "${GREEN}[A2] PASS — warm: int-single 200, int-multi x8 200, ext-known 200, /v1/messages 200; mock capture auth: single=$SINGLE_HITS a=$MOCK_A_COUNT b=$MOCK_B_COUNT${NC}"
else
    echo "${RED}[A2] FAIL — see lines above (codes / capture)${NC}"
    note_fail "A2"
fi

# A3 — backup + induce M2 outage
mkdir -p "$TMPDIR_PATH/dbbackup"
cp -a "$DB_DIR/config.db" "$DB_DIR/config.db-wal" "$DB_DIR/config.db-shm" "$TMPDIR_PATH/dbbackup/" 2>/dev/null
DBSZ=$(wc -c < "$DB_DIR/config.db" | tr -d ' ')
DBBLKS=$((DBSZ/512)); [ "$DBBLKS" -lt 1 ] && DBBLKS=1
dd if=/dev/urandom of="$DB_DIR/config.db" conv=notrunc bs=512 count="$DBBLKS" 2>/dev/null
rm -f "$DB_DIR/config.db-wal" "$DB_DIR/config.db-shm"
T0=$(date +%s)
echo "${YELLOW}[A3] outage induced (M2: $DBBLKS x 512B garbage blocks + rm wal/shm) at t0${NC}"

# scheduled helpers
wait_until() { local target=$((T0+$1)); while [ "$(date +%s)" -lt "$target" ]; do sleep 1; done; }
TOTAL_200=0; TOTAL_REQS=0; FAILED_ROUNDS=""
round() { # $1 label  $2 nonce
    local c ok=0 n=0
    c=$(chat int-single "$T_VALID" "$TMPDIR_PATH/r.out" "round $1 single"); n=$((n+1)); [ "$c" = "200" ] && ok=$((ok+1)) || echo "  [round $1] int-single -> $c : $(head -c 100 "$TMPDIR_PATH/r.out")"
    c=$(chat int-multi "$T_VALID" "$TMPDIR_PATH/r.out" "round $1 multi nonce-$2-$RANDOM"); n=$((n+1)); [ "$c" = "200" ] && ok=$((ok+1)) || echo "  [round $1] int-multi -> $c : $(head -c 100 "$TMPDIR_PATH/r.out")"
    c=$(chat ext-known "$T_VALID" "$TMPDIR_PATH/r.out" "round $1 ext"); n=$((n+1)); [ "$c" = "200" ] && ok=$((ok+1)) || echo "  [round $1] ext-known -> $c : $(head -c 100 "$TMPDIR_PATH/r.out")"
    c=$(anth int-single "$T_VALID" "$TMPDIR_PATH/r.out" "round $1 anthropic"); n=$((n+1)); [ "$c" = "200" ] && ok=$((ok+1)) || echo "  [round $1] /v1/messages -> $c : $(head -c 100 "$TMPDIR_PATH/r.out")"
    TOTAL_REQS=$((TOTAL_REQS+n)); TOTAL_200=$((TOTAL_200+ok))
    [ "$ok" = "$n" ] || FAILED_ROUNDS="$FAILED_ROUNDS $1($ok/$n)"
}
unknown_probes() { # $1 label -> prints result line
    local c1 c2 ok=1
    c1=$(chat never-seen-model "$T_VALID" "$TMPDIR_PATH/u1.out" "unknown $1")
    if [ "$c1" = "503" ] && grep -q "config_store_unavailable" "$TMPDIR_PATH/u1.out"; then r1="503+CSU"; else r1="$c1:$(head -c 80 "$TMPDIR_PATH/u1.out" | tr -d '\n')"; ok=0; fi
    c2=$(anth never-seen-model "$T_VALID" "$TMPDIR_PATH/u2.out" "unknown $1")
    if [ "$c2" = "503" ] && grep -q "config_store_unavailable" "$TMPDIR_PATH/u2.out"; then r2="503+CSU"; else r2="$c2:$(head -c 80 "$TMPDIR_PATH/u2.out" | tr -d '\n')"; ok=0; fi
    if [ "$ok" = "1" ]; then
        echo "${GREEN}  [A5/$1] PASS — unknown model: openai=$r1 anthropic=$r2${NC}"
    else
        echo "${RED}  [A5/$1] FAIL — unknown model: openai=$r1 anthropic=$r2 (expected 503 + config_store_unavailable on both)${NC}"
        note_fail "A5/$1"
    fi
}
A6_VALID_CODE=""; A6_VALID_CODES=""
tier_probe() { # $1 label
    local cv cr cg okv=1 okr=1 okg=1
    cv=$(chat int-single "$T_VALID" "$TMPDIR_PATH/t1.out" "tier $1 valid"); A6_VALID_CODES="$A6_VALID_CODES $cv"
    [ "$cv" = "200" ] || { okv=0; echo "  [A6/$1] T_VALID -> $cv (expected 200 stale-tier) body: $(head -c 120 "$TMPDIR_PATH/t1.out" | tr -d '\n')"; }
    cr=$(chat int-single "$T_REVOKED" "$TMPDIR_PATH/t2.out" "tier $1 revoked")
    [ "$cr" = "401" ] || { okr=0; echo "  [A6/$1] T_REVOKED -> $cr (expected 401)"; }
    cg=$(chat int-single "sk-garbage-xyz" "$TMPDIR_PATH/t3.out" "tier $1 garbage")
    [ "$cg" = "401" ] || { okg=0; echo "  [A6/$1] sk-garbage-xyz -> $cg (expected 401)"; }
    if [ "$okv" = "1" ] && [ "$okr" = "1" ] && [ "$okg" = "1" ]; then
        echo "${GREEN}  [A6/$1] PASS — T_VALID 200 (stale tier), T_revoked 401, garbage 401${NC}"
    else
        echo "${RED}  [A6/$1] FAIL — valid:$okv revoked:$okr garbage:$okg${NC}"
        note_fail "A6/$1"
    fi
}

# ---- outage timeline ----
wait_until 0;   round 1 "r1"
wait_until 10;  unknown_probes "t10"
wait_until 15;  round 2 "r2"
wait_until 30;  round 3 "r3"
wait_until 45;  round 4 "r4"
wait_until 60;  round 5 "r5"
wait_until 70;  tier_probe "t70"
wait_until 75;  round 6 "r6"
wait_until 80;  unknown_probes "t80"
wait_until 90;  round 7 "r7"
tier_probe "t90"

# A4 verdict
if [ -z "$FAILED_ROUNDS" ]; then
    echo "${GREEN}[A4] PASS — 7 rounds x 4 requests: $TOTAL_200/$TOTAL_REQS returned 200 through the outage${NC}"
else
    echo "${RED}[A4] FAIL — rounds with non-200:$FAILED_ROUNDS (total $TOTAL_200/$TOTAL_REQS ok). Expected: ALL 200 (known models keep serving through DB outage). If codes are 401 past t≈60s, that is the stale-tier product finding (SQLite NOTADB errors are not infra-classified) — see final report.${NC}"
    note_fail "A4"
fi

# A7 — sentinel misroutes, race WARNs, strict-read WARN
MISROUTE=$(grep -v "ext-known" "$SENTINEL_HITS" 2>/dev/null | grep -c "|" || true)
SENT_TOTAL=$(grep -c "|" "$SENTINEL_HITS" 2>/dev/null || true)
RACE_WARN=$(grep -c "\[WARN\] Race attempt" "$PROXY_LOG" || true)
STRICT_WARN=$(grep -c "strict read failed" "$PROXY_LOG" || true)
STRICT_TXT=$(grep "strict read failed" "$PROXY_LOG" | head -1 | sed 's/^.*strict read failed/strict read failed/')
A7_OK=1
[ "$MISROUTE" = "0" ] || { A7_OK=0; echo "  sentinel misroute hits: $(grep -v ext-known "$SENTINEL_HITS" | head -5)"; }
[ "$RACE_WARN" = "0" ] || A7_OK=0
[ "$STRICT_WARN" -ge 1 ] || A7_OK=0
if [ "$A7_OK" = "1" ]; then
    echo "${GREEN}[A7] PASS — sentinel misroute hits=0 (total $SENT_TOTAL, all ext-known); '[WARN] Race attempt' count=0; strict-read WARN x$STRICT_WARN: $STRICT_TXT${NC}"
else
    echo "${RED}[A7] FAIL — misroutes=$MISROUTE raceWARN=$RACE_WARN strictWARN=$STRICT_WARN${NC}"
    note_fail "A7"
fi

# A8 — restore + restart + out-of-band INSERT + reconciler pickup (criterion H)
cat "$TMPDIR_PATH/dbbackup/config.db" > "$DB_DIR/config.db"
[ -f "$TMPDIR_PATH/dbbackup/config.db-wal" ] && cat "$TMPDIR_PATH/dbbackup/config.db-wal" > "$DB_DIR/config.db-wal"
[ -f "$TMPDIR_PATH/dbbackup/config.db-shm" ] && cat "$TMPDIR_PATH/dbbackup/config.db-shm" > "$DB_DIR/config.db-shm"
echo "${YELLOW}[A8] DB restored in place; restarting proxy (live process cannot recover from SQLite corruption — see header note 4)${NC}"
kill "$PROXY_PID" 2>/dev/null; wait "$PROXY_PID" 2>/dev/null; PROXY_PID=""
sleep 1
PROXY_LOG="$TMPDIR_PATH/proxy_recovered.log"
"$PROXY_BIN" > "$PROXY_LOG" 2>&1 &
PROXY_PID=$!
wait_healthz "recovered" || exit 1
sleep 1
echo "${YELLOW}[A8] schema evidence:$(sqlite3 "$DB_DIR/config.db" '.schema models' | head -8 | tr '\n' ' ' | head -c 300)${NC}"
echo "${YELLOW}[A8] int-single row: $(sqlite3 "$DB_DIR/config.db" "SELECT id,internal,internal_model,credential_id,credentials_json FROM models WHERE id='int-single'")${NC}"
sqlite3 "$DB_DIR/config.db" "INSERT INTO models (id,name,enabled,credential_id,credentials_json,internal,internal_provider,internal_model,created_at,updated_at) VALUES ('int-recovered','Recovered',1,'cred-single','[{\"credential_id\":\"cred-single\",\"weight\":1,\"position\":0}]',1,'openai','up-single',datetime('now'),datetime('now'));" \
    || { echo "${RED}[A8] out-of-band INSERT failed${NC}"; note_fail "A8-insert"; }
INSERT_TS=$(date +%s)
A8_APPEAR=""
for i in $(seq 1 40); do
    sleep 2
    if curl -s "http://127.0.0.1:$PROXY_PORT/fe/api/models" | grep -q "int-recovered"; then
        A8_APPEAR=$(( $(date +%s) - INSERT_TS )); break
    fi
done
if [ -n "$A8_APPEAR" ]; then
    C=$(chat int-recovered "$T_VALID" "$TMPDIR_PATH/a8.out" "recovered")
    if [ "$C" = "200" ] && [ "$A8_APPEAR" -le 120 ]; then
        echo "${GREEN}[A8] PASS — int-recovered appeared via /fe/api/models after ${A8_APPEAR}s (≤120s, criterion H) and served a 200${NC}"
    else
        echo "${RED}[A8] FAIL — appeared after ${A8_APPEAR}s, request code=$C${NC}"
        note_fail "A8"
    fi
else
    echo "${RED}[A8] FAIL — int-recovered NEVER appeared within polling window (criterion H violated)${NC}"
    note_fail "A8"
fi

# A9 — final sentinel check + summary
MISROUTE2=$(grep -v "ext-known" "$SENTINEL_HITS" 2>/dev/null | grep -c "|" || true)
if [ "$MISROUTE2" = "0" ]; then
    echo "${GREEN}[A9] PASS — sentinel misroute hits still 0 across the whole run${NC}"
else
    echo "${RED}[A9] FAIL — sentinel misroute hits=$MISROUTE2: $(grep -v ext-known "$SENTINEL_HITS" | tail -3)${NC}"
    note_fail "A9"
fi

echo "${BLUE}------------------------------------------------------${NC}"
echo "${BLUE}  Pack A summary — proxy log WARN excerpt:${NC}"
grep -E "strict read failed|Race attempt" "$TMPDIR_PATH/proxy_main.log" 2>/dev/null | head -3 | sed 's/^/    /'
if [ "$FAILURES" -eq 0 ]; then
    echo "RESULT: PASS"
    echo "${GREEN}Pack A complete — 0 failed checks.${NC}"
    exit 0
else
    echo "RESULT: FAIL"
    echo "${RED}Pack A failed checks:$FAILED_CHECKS${NC}"
    exit 1
fi
