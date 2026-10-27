#!/usr/bin/env bash
# =============================================================================
# Provider-registration runtime smoke pack (reusable, brand-parameterized)
#
# WHAT IT PROVES
#   That a provider brand registered in the proxy is USABLE AT RUNTIME through
#   the real binary + the real HTTP API — not merely present in a static
#   registry or a frontend TypeScript union.
#
#   This is the ONLY e2e proof that a brand is usable at the API/config layer.
#   The DeepSeek-class bug shipped precisely there: the brand compiled, the
#   FE type union listed it, the unit tripwire passed — but the runtime
#   allowlist (pkg/models/credential.go validProviders) did not carry it, so
#   POST /fe/api/credentials answered 400 "invalid provider" and the brand was
#   unreachable from the UI. The React credential dropdown consumes
#   GET /fe/api/providers, so the LISTING is API-derived and only a real
#   request can prove the dropdown can offer the brand.
#
#   Origin: the 2026-10-27 openrouter registration gate (feature/openrouter-provider
#   @ 2be5d47), preserved and generalized from a proven throwaway script.
#
# USAGE
#   test/test_provider_registration_smoke.sh [PROVIDER_BRAND] [DISPLAY_NAME] [BASE_URL] [PORT]
#
#   Defaults: openrouter / OpenRouter / https://openrouter.ai/api/v1 / 18240
#
#   To gate a NEW brand, pass its registry values, e.g.:
#     test/test_provider_registration_smoke.sh acme "Acme" https://api.acme.test/v1 18250
#
# ASSERTIONS (18)
#   A1  build (real binary)                                   A10 model create -> 2xx (brand-bound credential)
#   A2  GET /healthz -> 200                                   A11 NEG trailing-space brand -> 4xx
#   A3  config.db under the isolated temp HOME                A12 NEG wrong-case brand    -> 4xx
#   A4  providers listing has the brand entry                 A13 shared-provider rule: mixed-brand model -> 4xx
#   A5  entry name  == DISPLAY_NAME                           A14 GET /ui/ -> 200
#   A6  entry base_url == BASE_URL                            A15 /ui/ Content-Type is text/html
#   A7  credential create (brand) -> 2xx                      A16 /ui/ body carries HTML markup
#   A8  credential persisted with provider == brand           A17 SIGTERM -> graceful exit, exit code 0
#   A9  model validate (brand-bound) -> valid:true            A18 port freed + developer real DB untouched
#
# SAFETY PROPERTIES (all enforced, not aspirational)
#   * ISOLATED HOME — the child process runs with HOME=<mktemp scratch>/home and
#     XDG_CONFIG_HOME=<scratch>/.config, so os.UserConfigDir() resolves INSIDE
#     the scratch dir on every platform (Linux honours XDG_CONFIG_HOME; macOS
#     uses $HOME/Library/Application Support — NOT ~/.config). The config DB
#     path is discovered platform-agnostically via
#     `find "$HOME_DIR" -name config.db`; the resolved path is then ASSERTED to
#     be under the scratch HOME or the pack ABORTS LOUDLY (non-zero).
#   * The developer's real ~/.config/llm-supervisor-proxy/config.db is NEVER
#     opened. It is only stat()'d (metadata) to prove its mtime is unchanged.
#   * DATABASE_URL is unset (env -u) so the SQLite path is always taken.
#   * NO NETWORK EGRESS: the build runs with GOPROXY=off / GOTOOLCHAIN=local /
#     -mod=readonly, so the pack can never reach a module proxy, a toolchain
#     server, npm, a registry, or an upstream provider. It needs NO API key.
#     The upstream-dialing endpoints (/fe/api/models/test, /v1/chat/completions)
#     are NEVER called — credential-create and model-create are offline
#     schema/allowlist checks (pkg/models and pkg/store/database contain no
#     outbound HTTP at all).
#   * PORT SAFETY: the port must be >= 10000 and must never be 8088 (the
#     ensemble self-system). The port is verified FREE before boot (lsof) and
#     FREED after shutdown. A foreign process on the port is NEVER killed —
#     the pack fails loudly instead.
#   * Only PIDs spawned by THIS script are killed (trap cleanup, exact PID).
#   * No repo file is written at runtime: build output, logs and evidence all
#     land in the mktemp scratch dir.
#
# SELF-DEADLINE: internal 280s watchdog (kills the server + exits 124); the
# caller wraps this with `timeout 300`. Dual layer — neither alone suffices.
#
# EXIT CODES: 0 = PASS, 1 = FAIL, 124 = TIMEOUT
# TEST CODE ONLY — no production source is modified by this script.
# =============================================================================
set -u
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

# ------------------------------------------------------------------ params --
BRAND="${1:-openrouter}"
DISPLAY_NAME="${2:-OpenRouter}"
BASE_URL="${3:-https://openrouter.ai/api/v1}"
PORT="${4:-18240}"

# The mixed-provider negative control needs a brand that is NOT the brand
# under test. deepseek is unrelated to openrouter; fall back to openai if
# somebody gates the deepseek brand itself.
OTHER_BRAND="deepseek"
[ "$OTHER_BRAND" = "$BRAND" ] && OTHER_BRAND="openai"

# ------------------------------------------------------------------ colors --
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'
else
    RED=''; GREEN=''; YELLOW=''; BLUE=''; NC=''
fi

# ------------------------------------------------------------------ paths ---
SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/llmproxy-provider-smoke.XXXXXX")" || {
    echo "${RED}ABORT: could not create scratch dir${NC}"; exit 1; }
HOME_DIR="$SCRATCH/home"
EV="$SCRATCH/evidence"
BIN="$SCRATCH/llm-supervisor-proxy"
LOG="$SCRATCH/server.log"
MARKER="$SCRATCH/.watchdog_fired"
BASE="http://127.0.0.1:${PORT}"

# The inherited HOME — used ONLY to stat() the developer's real config DB so we
# can prove we never touched it. Its contents are never opened.
INHERITED_HOME="${HOME:-}"
REAL_DB_CANDIDATES=(
  "${INHERITED_HOME}/.config/llm-supervisor-proxy/config.db"
  "${INHERITED_HOME}/Library/Application Support/llm-supervisor-proxy/config.db"
  "${XDG_CONFIG_HOME:-${INHERITED_HOME}/.config}/llm-supervisor-proxy/config.db"
)

SERVER_PID=""
WATCHDOG_PID=""
PASS_N=0
FAIL_N=0
SUMMARY=""

# ---------------------------------------------------------------- helpers ---
say()  { printf '%s\n' "$*"; }
hdr()  { printf '\n%s=== %s ===%s\n' "$BLUE" "$*" "$NC"; }
pass() { PASS_N=$((PASS_N+1)); SUMMARY="${SUMMARY}PASS | $1"$'\n'; printf '%sPASS:%s %s\n' "$GREEN" "$NC" "$1"; }
fail() { FAIL_N=$((FAIL_N+1)); SUMMARY="${SUMMARY}FAIL | $1"$'\n'; printf '%sFAIL:%s %s\n' "$RED" "$NC" "$1"; }
abort() { printf '\n%sABORT: %s%s\n' "$RED" "$1" "$NC"; exit 1; }

BODY=""; CODE=""
req() { # req <method> <url> [json-payload]
  local method="$1" url="$2" data="${3:-}"
  BODY="$EV/.body"
  if [ -n "$data" ]; then
    CODE=$(curl -sS -o "$BODY" -w '%{http_code}' -X "$method" \
      -H 'Content-Type: application/json' --data-binary "$data" "$url" 2>"$EV/.curlerr")
  else
    CODE=$(curl -sS -o "$BODY" -w '%{http_code}' -X "$method" "$url" 2>"$EV/.curlerr")
  fi
}
body() { cat "$BODY" 2>/dev/null; }
body_head() { head -c 600 "$BODY" 2>/dev/null; echo; }

# wait for a PID to exit (zombie-aware: a reaped-but-unwaited child still
# shows up in `ps`, so check the state letter, not just pid liveness)
wait_for_exit() { # $1=pid $2=half-seconds
  local pid="$1" limit="$2" i=0 st=""
  while [ "$i" -lt "$limit" ]; do
    st=$(ps -o stat= -p "$pid" 2>/dev/null | tr -d ' ')
    if [ -z "$st" ]; then echo "gone"; return 0; fi
    case "$st" in Z*) echo "zombie"; return 0;; esac
    sleep 0.5
    i=$((i+1))
  done
  echo "alive"
}

port_free() { ! lsof -ti:"$PORT" >/dev/null 2>&1; }

cleanup() {
  local rc=$?
  if [ -n "$WATCHDOG_PID" ]; then
    pkill -P "$WATCHDOG_PID" 2>/dev/null   # kill the `sleep` child first
    kill "$WATCHDOG_PID" 2>/dev/null
  fi
  if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill -TERM "$SERVER_PID" 2>/dev/null
    local i=0
    while [ $i -lt 10 ]; do kill -0 "$SERVER_PID" 2>/dev/null || break; sleep 0.5; i=$((i+1)); done
    kill -0 "$SERVER_PID" 2>/dev/null && kill -KILL "$SERVER_PID" 2>/dev/null
    wait "$SERVER_PID" 2>/dev/null
  fi
  if [ -f "$MARKER" ]; then
    printf '\n%sRESULT: TIMEOUT%s\n' "$YELLOW" "$NC"
    exit 124
  fi
  if [ "$FAIL_N" -eq 0 ] && [ "$rc" -eq 0 ]; then
    rm -rf "$SCRATCH" 2>/dev/null
  else
    printf '\n%sArtifacts kept for inspection:%s %s\n' "$YELLOW" "$NC" "$SCRATCH"
  fi
  return $rc
}
trap cleanup EXIT INT TERM

# ============================================================ 0. preflight --
hdr "0. Preflight"
say "brand        : $BRAND  (display: $DISPLAY_NAME)"
say "base url     : $BASE_URL"
say "port         : $PORT"
say "scratch      : $SCRATCH"
say "repo root    : $ROOT_DIR"

# --- required tooling --------------------------------------------------------
# jq/curl/lsof/ps are already house dependencies of the existing test/*.sh
# packs (test_mock_credential_lb.sh, test_proxy_glm5.sh, test_zai_direct.sh).
for tool in curl jq lsof ps go; do
  command -v "$tool" >/dev/null 2>&1 || abort "required tool '$tool' is not on PATH — install it and re-run"
done
say "required tools present: curl, jq, lsof, ps, go"

# --- port safety ------------------------------------------------------------
case "$PORT" in
  ''|*[!0-9]*) abort "port must be numeric (got '$PORT')" ;;
esac
if [ "$PORT" = "8088" ]; then
  abort "port 8088 is the ensemble self-system port — NEVER touch it. Pick another port."
fi
if [ "$PORT" -lt 10000 ]; then
  abort "port $PORT is below 10000 — the 1-9999 range is reserved for production/dev and 20000+ for other reservations. Pick a free port in 10000-19999."
fi
if [ "$PORT" -gt 65535 ]; then
  abort "port $PORT is out of range (>65535)"
fi
say "port $PORT passes the safety floor (>=10000) and is not 8088"

if ! port_free; then
  say "${RED}port $PORT is ALREADY IN USE by a foreign process:${NC}"
  lsof -i:"$PORT" 2>/dev/null || true
  abort "refusing to kill a foreign process on port $PORT — pick a free port (or pass one as argv[4])"
fi
say "port $PORT is FREE (lsof empty)"

mkdir -p "$EV" "$HOME_DIR"

# --- real-DB mtime snapshot (metadata only — the file is NEVER opened) ------
REAL_DB_MTIME_BEFORE=""
REAL_DB_PATH=""
for cand in "${REAL_DB_CANDIDATES[@]}"; do
  if [ -e "$cand" ]; then
    REAL_DB_PATH="$cand"
    REAL_DB_MTIME_BEFORE=$(stat -f '%m' "$cand" 2>/dev/null || stat -c '%Y' "$cand" 2>/dev/null)
    break
  fi
done
if [ -n "$REAL_DB_PATH" ]; then
  say "developer real DB (stat only, never opened): $REAL_DB_PATH (mtime $REAL_DB_MTIME_BEFORE)"
else
  say "developer real DB: not present on this machine — nothing to protect"
fi

# ================================================================ 1. build --
hdr "1. Build (real binary — GOPROXY=off: this pack never reaches the network)"
BUILD_START=$(date +%s)
( cd "$ROOT_DIR" && GOPROXY=off GOFLAGS=-mod=readonly GOTOOLCHAIN=local \
    go build -o "$BIN" ./cmd ) 2>&1 | tee "$EV/build.log"
BUILD_RC=${PIPESTATUS[0]}
BUILD_SECS=$(( $(date +%s) - BUILD_START ))
if [ "$BUILD_RC" -ne 0 ] || [ ! -x "$BIN" ]; then
  fail "build succeeded (rc=$BUILD_RC)"
  say "build output:"; cat "$EV/build.log"
  say ""
  say "${YELLOW}This pack is offline by design (GOPROXY=off). If the Go module cache"
  say "is cold, run 'go mod download' ONCE outside this pack, then re-run.${NC}"
  exit 1
fi
pass "build succeeded in ${BUILD_SECS}s (offline: GOPROXY=off)"
say "binary: $BIN ($(stat -f '%z' "$BIN" 2>/dev/null || stat -c '%s' "$BIN" 2>/dev/null) bytes)"

# internal watchdog (layer 2). fds MUST be redirected to /dev/null: otherwise
# the watchdog subshell (and its `sleep` child) inherit stdout and hold the
# caller's pipe open, stalling `... | tee` until the watchdog expires.
( sleep 280; touch "$MARKER"; kill -TERM $$ 2>/dev/null ) >/dev/null 2>&1 &
WATCHDOG_PID=$!
say "internal watchdog armed: 280s"

# ================================================================= 2. boot --
hdr "2. Boot (HOME=$HOME_DIR — isolated; DATABASE_URL unset; XDG contained)"
env -u DATABASE_URL \
    HOME="$HOME_DIR" \
    XDG_CONFIG_HOME="$HOME_DIR/.config" \
    APPLY_ENV_OVERRIDES=1 PORT="$PORT" \
    "$BIN" >"$LOG" 2>&1 &
SERVER_PID=$!
say "server pid: $SERVER_PID"

BOOT_OK=0
for i in $(seq 1 60); do
  hc=$(curl -sS -o /dev/null -w '%{http_code}' "$BASE/healthz" 2>/dev/null)
  if [ "$hc" = "200" ]; then BOOT_OK=1; break; fi
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then break; fi
  sleep 0.5
done
if [ "$BOOT_OK" -ne 1 ]; then
  fail "server healthy on /healthz (200)"
  say "--- server.log (tail 40) ---"; tail -n 40 "$LOG"
  exit 1
fi
pass "server healthy on GET /healthz -> 200"

say "--- boot banner ---"
grep -m1 "starting on port" "$LOG" || true
grep -m1 "SQLite database" "$LOG" || true

# --- A3 containment: the config DB MUST live under the isolated scratch HOME
DB_PATH="$(find "$HOME_DIR" -name 'config.db' -type f 2>/dev/null | head -1)"
if [ -z "$DB_PATH" ]; then
  say "${RED}no config.db found under $HOME_DIR${NC}"
  find "$HOME_DIR" -name '*.db' 2>/dev/null || true
  abort "the binary did not resolve its config dir inside the isolated HOME — refusing to continue (HOME/XDG_CONFIG_HOME handling changed; a real config dir may have been written outside the scratch dir)"
fi
case "$DB_PATH" in
  "$HOME_DIR"/*) : ;;
  *) abort "resolved config path '$DB_PATH' is NOT under the isolated HOME '$HOME_DIR' — aborting loudly" ;;
esac
pass "config.db resolved under the isolated HOME: $DB_PATH"

# ================================================== 3. providers listing --
hdr "3. GET /fe/api/providers  (the FE credential dropdown's source)"
req GET "$BASE/fe/api/providers"
say "HTTP $CODE"
cp "$BODY" "$EV/providers.json" 2>/dev/null
say "--- brand entry (verbatim) ---"
BRAND_ENTRY=$(jq -c --arg b "$BRAND" '.[] | select(.type==$b)' "$EV/providers.json" 2>/dev/null)
if [ -z "$BRAND_ENTRY" ]; then
  say "${RED}NO '$BRAND' ENTRY IN THE PROVIDERS LISTING${NC} — full listing:"
  cat "$EV/providers.json"; echo
  fail "providers listing contains a '$BRAND' entry"
else
  say "$BRAND_ENTRY" | jq .
  say "--- verbatim (compact) ---"
  say "$BRAND_ENTRY"
  GOT_NAME=$(jq -r --arg b "$BRAND" '.[] | select(.type==$b) | .name' "$EV/providers.json")
  GOT_BASE=$(jq -r --arg b "$BRAND" '.[] | select(.type==$b) | .base_url' "$EV/providers.json")
  [ "$GOT_NAME" = "$DISPLAY_NAME" ] \
    && pass "listing name == '$DISPLAY_NAME'" \
    || fail "listing name == '$DISPLAY_NAME' (got '$GOT_NAME')"
  [ "$GOT_BASE" = "$BASE_URL" ] \
    && pass "listing base_url == '$BASE_URL'" \
    || fail "listing base_url == '$BASE_URL' (got '$GOT_BASE')"
fi

# =================================================== 4. credential create --
hdr "4. POST /fe/api/credentials  provider=$BRAND   (THE runtime proof)"
CRED_ID="smoke-${BRAND}-cred"
CREQ="$(printf '{"id":"%s","provider":"%s","api_key":"sk-smoke-%s-test"}' "$CRED_ID" "$BRAND" "$BRAND")"
say "payload: $CREQ"
req POST "$BASE/fe/api/credentials" "$CREQ"
say "HTTP $CODE"
cp "$BODY" "$EV/credential_create.json" 2>/dev/null
say "body: $(body)"
if [ "$CODE" -ge 200 ] && [ "$CODE" -lt 300 ]; then
  pass "credential create with provider '$BRAND' ACCEPTED (HTTP $CODE)"
else
  fail "credential create with provider '$BRAND' ACCEPTED (HTTP $CODE)"
fi

req GET "$BASE/fe/api/credentials"
cp "$BODY" "$EV/credentials_after.json" 2>/dev/null
say "--- GET /fe/api/credentials ---"
jq -c --arg c "$CRED_ID" '.[] | select(.id==$c)' "$EV/credentials_after.json" 2>/dev/null
STORED_PROVIDER=$(jq -r --arg c "$CRED_ID" '.[] | select(.id==$c) | .provider' "$EV/credentials_after.json" 2>/dev/null)
[ "$STORED_PROVIDER" = "$BRAND" ] \
  && pass "stored credential provider == '$BRAND'" \
  || fail "stored credential provider == '$BRAND' (got '$STORED_PROVIDER')"

# ===================================================== 5. model validation --
hdr "5. Model bound to the '$BRAND' credential passes validation"
MODEL_ID="${BRAND}/smoke-model"
MPROV="$(printf '{"id":"%s","name":"%s","enabled":true,"internal":true,"internal_model":"smoke-model","credentials":[{"credential_id":"%s","weight":1}]}' "$MODEL_ID" "$MODEL_ID" "$CRED_ID")"
say "payload: $MPROV"

req POST "$BASE/fe/api/models/validate" "$MPROV"
say "POST /fe/api/models/validate -> HTTP $CODE"
cp "$BODY" "$EV/model_validate.json" 2>/dev/null
say "body: $(body)"
if [ "$CODE" = "200" ] && [ "$(jq -r '.valid' "$EV/model_validate.json" 2>/dev/null)" = "true" ]; then
  pass "model validation accepted ${BRAND}-bound model (valid:true)"
else
  fail "model validation accepted ${BRAND}-bound model (valid:true)"
fi

hdr "5b. POST /fe/api/models  (create the ${BRAND}-bound model)"
req POST "$BASE/fe/api/models" "$MPROV"
say "HTTP $CODE"
cp "$BODY" "$EV/model_create.json" 2>/dev/null
say "body: $(body_head)"
if [ "$CODE" -ge 200 ] && [ "$CODE" -lt 300 ]; then
  pass "model create with ${BRAND} credential ACCEPTED (HTTP $CODE)"
else
  fail "model create with ${BRAND} credential ACCEPTED (HTTP $CODE)"
fi

req GET "$BASE/fe/api/models"
cp "$BODY" "$EV/models_after.json" 2>/dev/null
say "--- persisted model (verbatim) ---"
jq -c --arg m "$MODEL_ID" '.[] | select(.id==$m)' "$EV/models_after.json" 2>/dev/null

# ==================================================== 6. negative controls --
hdr "6. NEGATIVE CONTROLS (the brand is lowercase-exact)"

req POST "$BASE/fe/api/credentials" \
  "$(printf '{"id":"smoke-%s-trailspace","provider":"%s ","api_key":"sk-smoke-%s-test"}' "$BRAND" "$BRAND" "$BRAND")"
say "--- provider '$BRAND ' (trailing space) -> HTTP $CODE"
cp "$BODY" "$EV/neg_trailing_space.json" 2>/dev/null
say "body: $(body)"
if [ "$CODE" -ge 400 ] && [ "$CODE" -lt 500 ]; then
  pass "trailing-space provider REJECTED (HTTP $CODE)"
else
  fail "trailing-space provider REJECTED (got HTTP $CODE — acceptance is a FAIL)"
fi

# Wrong-case probe: pick the first candidate that is NOT a valid provider
# type in the live listing, so the control is always genuinely invalid.
VALID_TYPES=$(jq -r '.[].type' "$EV/providers.json" 2>/dev/null)
is_valid_type() { printf '%s\n' "$VALID_TYPES" | grep -qxF "$1"; }
capitalize_first() { local s="$1"; printf '%s%s' "${s:0:1}" "${s:1}"; }
WRONGCASE=""
for cand in "$DISPLAY_NAME" "$(capitalize_first "$BRAND")" "$(printf '%s' "$BRAND" | tr '[:lower:]' '[:upper:]')"; do
  if [ -n "$cand" ] && [ "$cand" != "$BRAND" ] && ! is_valid_type "$cand"; then
    WRONGCASE="$cand"; break
  fi
done
if [ -z "$WRONGCASE" ]; then
  WRONGCASE="OpenRouter"
  say "${YELLOW}no invalid-case candidate derived — falling back to '$WRONGCASE'${NC}"
fi
req POST "$BASE/fe/api/credentials" \
  "$(printf '{"id":"smoke-%s-wrongcase","provider":"%s","api_key":"sk-smoke-%s-test"}' "$BRAND" "$WRONGCASE" "$BRAND")"
say "--- provider '$WRONGCASE' (wrong case) -> HTTP $CODE"
cp "$BODY" "$EV/neg_wrong_case.json" 2>/dev/null
say "body: $(body)"
if [ "$CODE" -ge 400 ] && [ "$CODE" -lt 500 ]; then
  pass "wrong-case provider '$WRONGCASE' REJECTED (HTTP $CODE)"
else
  fail "wrong-case provider '$WRONGCASE' REJECTED (got HTTP $CODE — acceptance is a FAIL)"
fi

# --- shared-provider rule: a model may not mix two different providers ------
hdr "6b. shared-provider rule (a model must not mix two providers)"
OTHER_CRED_ID="smoke-${OTHER_BRAND}-cred"
req POST "$BASE/fe/api/credentials" \
  "$(printf '{"id":"%s","provider":"%s","api_key":"sk-smoke-%s-test"}' "$OTHER_CRED_ID" "$OTHER_BRAND" "$OTHER_BRAND")"
say "setup $OTHER_BRAND credential -> HTTP $CODE"
MIXP="$(printf '{"id":"%s/smoke-mixed","name":"%s/smoke-mixed","enabled":true,"internal":true,"internal_model":"smoke-model","credentials":[{"credential_id":"%s","weight":1},{"credential_id":"%s","weight":1}]}' "$BRAND" "$BRAND" "$CRED_ID" "$OTHER_CRED_ID")"
req POST "$BASE/fe/api/models/validate" "$MIXP"
say "mixed-provider model validate -> HTTP $CODE"
cp "$BODY" "$EV/mixed_provider.json" 2>/dev/null
say "body: $(body)"
if [ "$CODE" -ge 400 ] && [ "$CODE" -lt 500 ]; then
  pass "mixed-provider model REJECTED (shared-provider rule enforced)"
else
  fail "mixed-provider model REJECTED (got HTTP $CODE)"
fi

# ======================================================== 7. embedded UI ---
hdr "7. Embedded UI still serves"
UI_CODE=$(curl -sS -o "$EV/ui.html" -w '%{http_code}' -D "$EV/ui.headers" "$BASE/ui/" 2>/dev/null)
UI_CT=$(grep -i '^content-type:' "$EV/ui.headers" 2>/dev/null | head -1 | tr -d '\r')
say "HTTP $UI_CODE | $UI_CT"
say "first line: $(head -n 1 "$EV/ui.html" 2>/dev/null)"
if [ "$UI_CODE" = "200" ]; then pass "GET /ui/ -> 200"; else fail "GET /ui/ -> 200 (got $UI_CODE)"; fi
case "$UI_CT" in
  *text/html*) pass "GET /ui/ Content-Type is text/html" ;;
  *) fail "GET /ui/ Content-Type is text/html (got '$UI_CT')" ;;
esac
if grep -qi '<div\|id="root"' "$EV/ui.html" 2>/dev/null; then
  pass "GET /ui/ body contains HTML markup (<div / root)"
else
  fail "GET /ui/ body contains HTML markup (<div / root)"
fi

# ================================================== 8. graceful shutdown ---
hdr "8. Graceful shutdown (SIGTERM -> drain -> exit)"
if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
  kill -TERM "$SERVER_PID"
  STATE=$(wait_for_exit "$SERVER_PID" 20)   # 20 * 0.5s = 10s budget
  say "process state after SIGTERM: $STATE"
  if [ "$STATE" = "alive" ]; then
    fail "server exited within 10s of SIGTERM (still alive)"
    kill -KILL "$SERVER_PID" 2>/dev/null
  else
    wait "$SERVER_PID" 2>/dev/null
    EXIT_RC=$?
    say "server exit code: $EXIT_RC (0 = graceful, not SIGKILLed)"
    if [ "$EXIT_RC" = "0" ]; then
      pass "server exited gracefully on SIGTERM (exit 0, within 10s)"
    else
      fail "server exited gracefully on SIGTERM (exit $EXIT_RC)"
    fi
  fi
else
  fail "server was alive to shut down"
fi
SERVER_PID=""

if port_free; then
  pass "port $PORT freed after shutdown (lsof empty)"
else
  fail "port $PORT freed after shutdown — NOT killing the holder (not ours):"
  lsof -i:"$PORT" 2>/dev/null || true
fi

say "--- last ~8 log lines (graceful drain) ---"
tail -n 8 "$LOG"
cp "$LOG" "$EV/server.log" 2>/dev/null

# ==================================================== 9. isolation proof ---
hdr "9. Isolation proof (developer real DB untouched)"
if [ -n "$REAL_DB_PATH" ] && [ -e "$REAL_DB_PATH" ]; then
  REAL_DB_MTIME_AFTER=$(stat -f '%m' "$REAL_DB_PATH" 2>/dev/null || stat -c '%Y' "$REAL_DB_PATH" 2>/dev/null)
  say "real db mtime after: $REAL_DB_MTIME_AFTER"
  if [ "$REAL_DB_MTIME_BEFORE" = "$REAL_DB_MTIME_AFTER" ]; then
    pass "developer real DB mtime unchanged ($REAL_DB_MTIME_BEFORE)"
  else
    fail "developer real DB mtime unchanged (before=$REAL_DB_MTIME_BEFORE after=$REAL_DB_MTIME_AFTER)"
  fi
elif [ -z "$REAL_DB_PATH" ]; then
  pass "developer real DB still absent (nothing to protect on this machine)"
else
  fail "developer real DB still absent"
fi
say "isolated db: $(ls -la "$DB_PATH" 2>/dev/null | tail -1)"

# ============================================================ 10. summary --
hdr "SUMMARY"
printf '%s' "$SUMMARY"
say "PASS count: $PASS_N   FAIL count: $FAIL_N"
if [ "$FAIL_N" -eq 0 ]; then
  say ""
  say "RESULT: PASS"
  exit 0
fi
say ""
say "RESULT: FAIL"
exit 1
