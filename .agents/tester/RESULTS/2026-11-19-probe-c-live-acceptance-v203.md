# Probe C — Live acceptance verification, AUTH COMMISSION v203 (deployed both hosts)

**Date**: 2026-11-19 ~09:00–09:20Z
**Subject**: llm-supervisor-proxy master `df12c62` (image `:df12c62`, index digest `sha256:f752aa8f…45ddf`), migration 034 live both hosts
**Hosts**: PRIMARY `https://llm.ensem.dev` (daoduc backup `https://llm.daoduc.org`)
**Instance**: tester `6e527a84` · workers `1ccc0b23` (primary, e2e-test) + `be450bb5` (daoduc, e2e-test)
**Mode**: LIVE acceptance probe — read-only against the live stores, EXCEPT one commissioned transient token (P5, created + revoked)

---

## Verdict

**PASS** — the AUTH COMMISSION v203 per-layer authorization enforcement is live and correct on the primary host (9/9 probes), and on the backup host every probe that could be exercised is green (7/7 + 2 supplementary positive controls).

**One finding, NOT a bypass failure**: the backup host (daoduc) does **not** carry the `dg-e2e-decision-group` row — admin model rows **primary 37 / daoduc 36**, sole diff = that group. The prescribed P1 target therefore 404s on daoduc (unknown model — the 404 precedes the scope gate) and is **untestable there**, not failed. See "Gaps" below.

**No bypass was observed on either host.** Every out-of-scope request on every lane returned 403/400/401; every in-scope request returned 200.

---

## Summary

| Metric | Value |
|---|---|
| Probes executed | 9 primary + 9 daoduc (+3 supplementary controls) |
| Passed | 9/9 primary · 7/9 daoduc prescribed + 2/2 supplementary PASS · 1 untestable (daoduc P1) |
| Failed | 0 |
| Untestable | 1 (daoduc P1 — target row absent) |
| Upstream calls (real provider) | **3 total** (primary: P4a, P4b, P5; daoduc: **0** — every daoduc probe rejected pre-upstream) |
| Live-store writes | 1 transient token (P5) — created, used once, revoked (204), verified gone |
| Anomalies | 1 (daoduc↔primary model-row drift) |

---

## Primary host results (`llm.ensem.dev`)

| Probe | Expected | Actual | Body (sanitized) | Verdict |
|---|---|---|---|---|
| P1 decision-group | 403 `access_denied`, exact envelope, no `code` | **403** | `{"error":{"message":"model not allowed for this token","model":"dg-e2e-decision-group","type":"access_denied"}}` — `error` has exactly 3 keys (message/model/type) | **PASS** |
| P2 decision-model | 403 `access_denied` | **403** | same shape, `model":"decision"` | **PASS** |
| P3 misroute precedence | 400 `decision_misroute` (NOT 403) | **400** | `{"error":{"type":"server_error","code":"decision_misroute","message":"decision models are served by /v1/decisions only (got kind \"group\")"}}` | **PASS** — 400 ≻ 403 preserved |
| P4a chat `quick-group` | 200 | **200** | `model=quick-group`, `finish_reason=stop`, usage 52 | **PASS** |
| P4b chat `coding-group` | 200 | **200** | `model=coding-group`, `finish_reason=stop`, usage 210 | **PASS** |
| P5 wildcard decision | 200 real decision body | **200** | keys `answers/model/usage`; `model=gpt-6-luna`; `answers[0]={type:predicate,name:is_clear,probability:1}`; usage 162 | **PASS** |
| P6a(a) anon `/v1/decisions` | 401 | **401** | `{"error":{"type":"authentication_error","message":"Invalid or expired API key"}}` | **PASS** |
| P6a(b) anon `/v1/messages` (catalog-known `9router-pool`) | 401 | **401** | same shape | **PASS** |
| P8a version | 203 | **200** `{"version":"203"}` | pod-direct `127.0.0.1:28443` → `deploy/llm-supervisor-proxy:4321`, ns `llmproxy`, ctx `default` | **PASS** |

## Daoduc (backup) host results (`llm.daoduc.org`)

| Probe | Expected | Actual | Body (sanitized) | Verdict |
|---|---|---|---|---|
| P1 decision-group | 403 | **404** | `{"error":{"type":"server_error","message":"unknown model \"dg-e2e-decision-group\""}}` | **UNTESTABLE** — row absent on daoduc (env fact, not a bypass failure) |
| P2 decision-model | 403 `access_denied` | **403** | `{"error":{"message":"model not allowed for this token","model":"decision","type":"access_denied"}}` — no `code` key | **PASS** |
| P3 misroute precedence | 400 `decision_misroute` | **400** | `{"error":{"type":"server_error","code":"decision_misroute","message":"decision models are served by /v1/decisions only (got kind \"group\")"}}` | **PASS** |
| P6b(a) anon `/v1/decisions` | 401 | **401** | `authentication_error` | **PASS** |
| P6b(b) anon `/v1/messages` (catalog-known `9router-pool`) | 401 | **401** | `authentication_error` | **PASS** |
| P8b version | 203 | **200** `{"version":"203"}` | pod-direct `127.0.0.1:18444` → 4321 (hetzner ctx) | **PASS** |
| PARITY-CATALOG | 27 == 27, identical sets | **27 vs 27, diff EMPTY** | `GET /v1/models` 200 both hosts | **PASS** |
| EDGE-ROOT primary | 302 | **302** | → `https://door.ensem.dev/login/oauth/authorize` | **PASS** |
| EDGE-ROOT daoduc | 302 | **302** | → `https://llm.daoduc.org/ui/` (nginx, no OAuth on backup edge) | **PASS** |

### Supplementary positive controls (daoduc worker, all pre-upstream, 0 upstream calls)

| Probe | Request | Actual | Purpose | Verdict |
|---|---|---|---|---|
| P1b | daoduc `/v1/chat/completions` model=`coding-group`, narrow token | **403** `access_denied` | proves the **group-target scope gate is live on daoduc** (`pkg/proxy/handler.go:847`, runs before group rewrite / any upstream attempt) | **PASS** |
| P1c | primary `/v1/decisions` model=`decision`, narrow token | **403** | decision-**model** arm on primary | **PASS** |
| P1d | primary `/v1/decisions` model=`dg-e2e-decision-group`, narrow token | **403** `access_denied`, `model` = the requested id, no `code` | **exact prescribed P1 shape re-run** — pins the decision-GROUP-arm 403 envelope on the host where the group exists | **PASS** |

Gate ordering proven from source @ `df12c62`: decision-lane scope gate `pkg/decision/handler.go:359` (step 7) + group arm ARM#1 `pkg/decision/group.go:124/:139`; chat-lane scope gate `pkg/proxy/handler.go:847` — all **precede** credential resolution / candidate construction / upstream attempt. Hence every 401/403/400/404 above is a genuine authorization verdict, not a masked upstream failure.

---

## Wildcard credential provenance — transient create + revoke

No local wildcard credential existed. `/home/nea/.config/llmproxy-smoke/` holds exactly three mode-600 token files (`token` 68 B, `token-primary` 67 B, `token-primary-agentic` 68 B) + 2 probe scripts + 2 logs. Admin read-only listing confirmed all three are **narrow**:

- `smoke-primary-agentic` → `allowed_groups=[agentic-group]`
- `smoke-primary` → `allowed_groups=[coding-group,quick-group]`
- `token` = `smoke-15m` → `[coding-group,quick-group]` (daoduc-only row)

The three in-store wildcards (`Ha`, `tuyen`, `kha`, both axes NULL) expose **no token values** from the list endpoint (row keys: allowed_groups, allowed_groups_invalid, allowed_models, allowed_models_invalid, created_at, created_by, id, name) and their values are not on disk ⇒ no usable local wildcard.

**Commissioned transient path (primary only):**
- Mint: `POST /fe/api/tokens` (pod-direct) body `{"name":"probe-c-wildcard-transient-20261119T090822Z","expires_at":"<+30 min>"}` — both grant axes **omitted** ⇒ NULL ⇒ wildcard (conjunctive rule). Response **201**; `allowed_models=null`, `allowed_groups=null`.
- Token id `e8715e6a-21fb-461f-bdc2-41978cd1052f`, created_at `2026-11-19T09:08:22Z`. 30-min expiry added as a fail-safe belt.
- Plaintext value never printed / logged / argv'd — written to a mode-600 temp file, piped via `curl --config -`, deleted after use.
- **Single use**: exactly one `POST /v1/decisions` → 200.
- **Revoke**: `DELETE /fe/api/tokens/e8715e6a-21fb-461f-bdc2-41978cd1052f` → **204** (explicit call + trap-guarded fallback).
- **Verified**: re-list → 5 rows `['smoke-primary-agentic','smoke-primary','Ha','tuyen','kha']`; transient row present = **False**. Store restored to its pre-probe row set.

---

## Request bodies used (no secrets)

- Decision lane (P1/P2/P3/P5):
  `{"model":"<ID>","input":"The onboarding flow is clear and fast.","questions":[{"type":"predicate","name":"is_clear","instructions":"Is the onboarding flow clear?"}]}`
  `<ID>` ∈ `dg-e2e-decision-group` (P1, P5), `decision` (P2), `coding-group` (P3).
  Derived from `pkg/decision/types.go` `DecisionRequest{model,input,questions}` + `pkg/decision/validators.go` — the body clears validation so 400-for-shape can never be confused with 403-for-authz.
- Chat lane (P4a/P4b): `{"model":"<ID>","messages":[{"role":"user","content":"Reply with exactly: OK"}],"max_tokens":128,"stream":false}`
- Anonymous messages (P6a(b)/P6b(b)): `{"model":"9router-pool","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`
- Admin mint: `{"name":"probe-c-wildcard-transient-20261119T090822Z","expires_at":"<RFC3339 +30min>"}`

---

## Environment facts

1. **Decision-kind ids are absent from the public catalog.** `GET /v1/models` → 200, 27 ids, none of them a decision id. Decision ids resolved pod-direct via `GET /fe/api/models`. `dg-e2e-decision-group` = `kind:"group"`, members `decision` + `decision-openrouter`, both `kind:"decision"` ⇒ unanimous decision lane (`pkg/models/group_lane.go`); the group has **no `shadow_model_id`** ⇒ the group arm is only reachable by naming the group id directly (which is exactly what P1/P1d do).
2. **Model-row drift primary→daoduc**: primary 37 / daoduc 36 rows; sole diff = `dg-e2e-decision-group`. daoduc's DB was resynced 2026-11-17; the decision group post-dates it. Public catalog parity (27==27) still holds — the drift is in internal/decision rows only.
3. **Token row counts differ**: primary 5 (2 narrow + 3 wildcards), daoduc 6 (adds `smoke-15m`). `smoke-primary` / `smoke-primary-agentic` rows are byte-identical across hosts (same row ids) ⇒ the narrow token IS valid on daoduc (a 401 would have indicated otherwise).
4. **P3's 400 envelope carries `error.code="decision_misroute"` and `error.type="server_error"`** — unlike the 403 envelope, which correctly has no `code` key. Expected per `openAIErrorWithCode`; not a defect.
5. **Local checkout is stale** (`master`=5ce0ead vs `origin/master`=df12c62) — all source reading was done via `git show df12c62:<path>` (read-only); no checkout, branch mutation, or push.
6. **Edge asymmetry**: primary `/` → OAuth `door.ensem.dev`; backup `/` → nginx 302 to `/ui/` (no OAuth on the backup edge).

---

## Gaps (action required, outside the probe's read-only mandate)

1. 🔴 **daoduc↔primary model-row drift** — the decision group `dg-e2e-decision-group` exists only on primary. Consequence: (a) the decision-GROUP arm cannot be exercised on the backup host, and (b) a failover to daoduc loses the decision-group capability entirely.
   **Recommended action for the leader**: resync daoduc←primary DB (a live-store write — outside the tester's mandate), then re-run P1 unchanged; it must return 403 with `error.model = "dg-e2e-decision-group"`. The backup host's row set is otherwise intact (036/037 diff = 1 row).
2. 🟠 **P1 on daoduc remains UNTESTED** until (1) is done. Do not record it as passed.
3. 🟢 (informational, carried from Phase-1 review, not re-verified here) DTO cannot express DENY-ALL (`TokenResponse` omits `Models`/`GroupsInvalid`) — the FE shows a green "Unrestricted" badge on a deny-all token. Out of scope for this probe lane.

---

## Safety compliance

- Zero writes to any live store except the single commissioned transient token (created, used once, revoked 204, verified gone).
- No pod restart / scale / patch; no helm; no `kubectl apply`; deploy lane untouched.
- No signal sent to port 8088 (ensemble self-system) or any port not opened by the probe workers; port-forwards were PID-scoped and torn down (`ss -ltnp` free, no orphan `kubectl port-forward`).
- Tokens piped only via `printf … | curl --config -` (never argv); no `curl -v` / `--trace`; no `set -x`; post-run leak scan of both evidence files for `Bearer <literal>` = **0**.
- Every curl bounded (`--max-time 30` auth/catalog/admin, `--max-time 120` inference).

## Evidence

- `/tmp/probe-c-primary-250760.txt` (5783 B) — sanitized transcript, token-grant table, credential dir listing, mint id/timestamps, 204 revoke, post-revoke verification.
- `/tmp/probe-c-daoduc-256203.txt` (10464 B, 150 lines) — sanitized transcript, admin row-count diff, source-proven gate ordering, port teardown proof.
- New skill `live-deploy-acceptance-probe` (`55e48bb9-8083-49ff-8cb5-de1880856fd9`) — the reusable recipe extracted from this lane.
- POK `daoduc-primary-group-row-drift` + POK `probe-c-wildcard-mint-revoke-technique`.
