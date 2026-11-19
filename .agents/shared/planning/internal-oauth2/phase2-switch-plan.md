# Phase 2: Per-Host Switch Plan — Internal OAuth2 Cutover

**Feature:** `internal-oauth2` — in-binary OAuth2 replacing the edge `oauth2-proxy` + Casdoor gate on the operator surfaces of both hosts.
**Plan ref:** `.agents/shared/planning/internal-oauth2/phase2-switch-plan.md`
**Authority:** ADR-001 (`architecture-decision-record.md`) — **Approach 5: per-path `/oauth2`-first + three-state Helm chart**. Council consensus `367e6b36` + architect, 2026-11-19.
**Amendment register:** `architecture-recommendation.md` §4–§6 (AR-01…AR-66). Every AR addressed here is cross-referenced in **Appendix A**.
**Author:** plan-creation worker (planner[v2])
**Date:** 2026-11-19
**Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-internal-oauth2` (branch `feature/internal-oauth2`, base `92c4234`)
**Data-center-scripts repo (READ-ONLY here):** `/home/nea/Code/data-center-scripts`
**Companions:** `phase1-plan.md` (1,428 L — §4.2 env table is the **single source of truth** for env names; §8.2/§8.3 define the three-state chart values), `plan-overview.md` (planner's), `architecture-recommendation.md`, `architecture-decision-record.md`.
**Status:** Draft — Ready for Review (supersedes the pre-ADR-001 draft of this file)

---

## 0. Hard Rules (read first)

### 0.1 Tooling & variables (define once; every command block below is copy-paste runnable)

```bash
export HELM=/home/nea/bin/helm                       # not on PATH (explorer finding)
export KCTL=kubectl
export KC_DAODUC=/home/nea/Code/data-center-scripts/hetzner/connection/kubeconfig.yaml
export KC_ENSEM=/home/nea/.kube/config
# per-host kubeconfig selection is done by reassigning KC="$KC_DAODUC" / KC="$KC_ENSEM"
# inside each section — never rely on a default context.
```

1. **Write scope.** This document is the only artifact written by this pass. No file in `data-center-scripts` is modified; no Go/TS/Helm code is touched. All `data-center-scripts` paths below are **read-only references** supplying exact diff context.
2. **Secret hygiene.** NEVER read or echo secret VALUES from `rancher-local-ensem-dev/secret.yaml`, `rancher-local-ensem-dev/oauth2-proxy/secret.yaml`, `hetzner/connection/kubeconfig.yaml`, or `.agents/shared/planning/llm-daoduc-to-ensem/phase1-extract-from-hetzner.md`. Reference **paths and key NAMES only** (`CLIENT_ID`, `CLIENT_SECRET`, `COOKIE_SECRET`, `url`, `password`). Key-name inspection uses `kubectl get secret … -o jsonpath='{.data}' | jq 'keys'` — keys only, never a value.
3. **Evidence-only files.** `infranstructure/k8s/llmproxy/ingress-public.yaml` and `ingress-protected.yaml` are **live-object dumps** (carry `resourceVersion`/`uid`); they are **never applied**. On daoduc the chart templates are the source of truth. Referenced here for context only.
4. **v203 Rollback Ordering Invariant.** Migration 034 (`allowed_groups`) is live on both hosts (`df12c62` = v203). Phase 1's dormant image builds **on** v203. Toggling OAuth2 off, or rolling back to the Phase 1 dormant image / `df12c62`, does **not** cross migration 034. Rolling back **past** v203 (to `0082773`/v202 or earlier) is **FORBIDDEN** without reversing 034 first (guarded SQL from the frozen down file). Restated in every rollback block (§3.6, §4.8).
5. **`ensem` is HARD HELD for Phase 4** until §4.1's unhold criteria are all met.
6. **No new trust surface.** No trusted header, no delegation hop, no CIDR, no foreign-cookie parsing. The internal cookie is `llmproxy_oauth2` and is **not** interoperable with `_oauth2_proxy` (OQ-5, unamended). The app keeps exactly two modes: `dormant` / `enforce`.
7. **The chart is the highest-risk artifact in this change** (three-state template logic). A wrong conditional silently re-opens Window A. Every state transition is asserted by a rendered-manifest diff **and** a live probe (§6).

---

## 1. Cutover Overview & Phase Gating

### 1.1 Phase map

| Phase | Host(s) | Action | Gate to proceed |
|---|---|---|---|
| **Phase 1** (dormant) | both | Ship the image with the internal layer present but inert (`OAUTH2_ENABLED` unset/false). Chart gains the **three-state** toggles (`internalOAuth2.enabled`, `internalOAuth2.overlap`, `oauth2Proxy.enabled`, `oauth2Proxy.replicas`) and the `secretKeyRef` env wiring (phase1 §8.2/§8.3, T15). Edge `oauth2-proxy` stays in front, unchanged. | All house gates green; dormant verification; `OAUTH2_ENABLED` confirmed absent/false in pod env; chart renders **edge-only** byte-identical to today (phase1 H6 = empty diff). |
| **Phase 2** (this plan) | — | This document. No cluster mutation. | Plan approved. |
| **Phase 3** | daoduc (backup) | **TWO upgrades + pre-stage**: D0 pre-flight → D1 **overlap** upgrade → D2 probe matrix + login rehearsal → D3 **internal-only** upgrade → D4 soak. | Phase 1 live on both hosts; Phase 2 approved; §2 prerequisites green; daoduc soak green. |
| **Phase 4** | ensem (primary) | 🛑 **HARD HELD.** Four pre-staged applies (A→B→C→D): **A** `/oauth2` repoint → **B** enforcing deploy → **C** protected-path repoint → **D** soak. | Phase 3 soak green **+ explicit Emperor/operator sign-off + independent shell lane** (§4.1). |

### 1.2 ADR-001 — the three handover invariants

The cutover is valid **iff** all three hold (ADR-001 §Invariants; recommendation §3.4):

| # | Invariant | Operational meaning in this plan |
|---|---|---|
| **I-1 Loop** | Whenever the app enforces, `/oauth2/*` must route to the app. | daoduc: the **D1** overlap upgrade repoints `/oauth2` → app in the **same** release that starts enforcement. ensem: step **A** repoints `/oauth2` → app **before** step **B** turns enforcement on. A loop (Window C) is structurally impossible. |
| **I-2 Exposure** | Edge paths/annotations may drop only after **every** serving pod enforces. | daoduc: annotations drop only in **D3**, after D1's enforcing rollout is complete and probed (D2). ensem: `/`,`/ui`,`/fe/api` move to the app only in step **C**, after B's rollout is complete and probed. The chart's `ingress-protected` template keeps the annotations in the overlap state (phase1 §8.3). |
| **I-3 Liveness** | The activation that mounts enforcement must mount the secret env **in the same act** — never a bare env flip. | daoduc: `internalOAuth2.enabled: true` renders the `secretKeyRef` env block **in the same chart release** (phase1 §8.3 `deployment.yaml` row). ensem: step **B** applies the configmap **and** the deployment (with all three `secretKeyRef` entries) as one unit. **Never** a bare `env.OAUTH2_ENABLED` flip — that renders no `OAUTH2_CLIENT_ID` ⇒ startup fail-fast ⇒ **CrashLoopBackOff ⇒ the host's `/v1/*` dies (Window B)**. |

### 1.3 Casdoor non-mutation guarantee
- Phase 3/4 **do not touch Casdoor**. No client registration, secret rotation, redirect-URI change, or issuer config change. The existing Casdoor client is reused **as-is** (OQ-1).
- Issuer + redirect reused verbatim: daoduc `https://door.daoduc.org` / `https://llm.daoduc.org/oauth2/callback`; ensem `https://door.ensem.dev` / `https://llm.ensem.dev/oauth2/callback`.
- Rationale: zero external human action, zero secret rotation, and the rollback is a chart/route revert — not an IdP operation.

### 1.4 Secret key reuse (no regeneration)
- The existing `oauth2-proxy-secret` (ns `llmproxy`; keys `CLIENT_ID`, `CLIENT_SECRET`, `COOKIE_SECRET`) is **reused as-is**. No new Secret, no key rotation, no `COOKIE_SECRET` regeneration.
- The chart reads all three keys from it via `internalOAuth2.existingSecret` (phase1 §8.2/§8.3); the ensem manifests read them via explicit `secretKeyRef` (§4.4).
- The two layers never encrypt the same cookie with the same key (different names, different HKDF subkeys — phase1 §4.3 / AR-32 / ruling R-9), so co-existence during the overlap state is safe by construction.

### 1.5 Browser session re-login expectation
- **DECIDED (OQ-5): no cross-format compatibility.** The internal cookie is `llmproxy_oauth2` (AES-256-GCM, AAD-bound) and is never `_oauth2_proxy`. Existing `_oauth2_proxy` cookies are **not** accepted by the in-binary handler ⇒ **forced re-login for every browser session on the switched host**, certain and accepted (ADR-001 §Context; recommendation AR-05-adjacent OQ-5).
- Session lifetime defaults to **`OAUTH2_SESSION_TTL=24h`** (planner OQ-3), parity with today's `--cookie-expire=24h` ⇒ the switch does not change re-login cadence. Clamp `[5m, 24h]`; tightening later is an explicit operator env change.
- Communicate the forced re-login in the cutover window (§3.3 D2, §4.7 E3).

### 1.6 Overlap-state operator precondition (P7′) — AR-52
- The overlap state is a **designed, probe-gated transition**, not a failure. Two operator preconditions:
  - **P7′** — the operator holds a **live edge session** (a valid `_oauth2_proxy` cookie) during the overlap. With P7′ the operator can mint `llmproxy_oauth2` through the app's own flow and verify both layers at once (probe **P-E**).
  - **Low-traffic window + user notice.** The overlap window is minutes, scheduled, and announced to operators before it opens.
- Users **without** a live edge session who attempt a fresh login during the overlap get a **fail-closed ping-pong** (app 302 → app `/oauth2/start` → short-circuit 302 `rd` → app 302 …). It **terminates only when the edge leaves the path** (D3 / step C). It is fail-closed — never an exposure — but it is user-visible, hence the notice and the short window.
- The `/oauth2/start` valid-session short-circuit (AR-54, phase1 §5.6 step 0) is what keeps the ping-pong cheap: a request carrying a valid `llmproxy_oauth2` gets an immediate `302 rd` with **zero IdP hop** (probe **P-K**).

### 1.7 Principal admission posture — `email_verified` (leader ruling 5)
- Phase 1 enforces **strictly**: `OAUTH2_REQUIRE_EMAIL_VERIFIED=true` (phase1 §4.2, AR-04) ⇒ an id_token whose `email_verified` claim is absent or false ⇒ **403** (fail closed), unless the allowlist is `*` **and** `OAUTH2_ALLOW_ANY_EMAIL_DOMAIN=true`.
- **Per-host admission posture recorded explicitly:**

| Host | Phase | `OAUTH2_EMAIL_DOMAINS` | `OAUTH2_ALLOW_ANY_EMAIL_DOMAIN` | `OAUTH2_REQUIRE_EMAIL_VERIFIED` | Rationale |
|---|---|---|---|---|---|
| daoduc (backup) | 3 | **`daoduc.org`** (tightened at cutover) | `false` | `true` | Proving posture. The tightened allowlist must be set **before** any cutover (AR-04). |
| ensem (primary) | 4 | `*` **until** a soak proves no operator account is excluded | `true` | `true` | Zero-lockout insurance on the primary; the `*`+flag combination logs a startup WARN (phase1 §4.2/§4.4). Tightening on ensem is a follow-up env change after soak. |

- **Daoduc pre-cutover read-only IdP verification step** — mandatory before D1 (§2.3). If any operator account is unverified ⇒ the allowlist is tightened **then**, and the cutover does not proceed until every operator account on `door.daoduc.org` is verified (or the allowlist is tightened to verified accounts only). **Fail-closed: no runtime bypass, no WARN mitigation.** Strict 403 in every mode.

### 1.8 Casdoor outage during soak — AR-28 (semantics)
- A Casdoor outage during the soak **fails verification environmentally; it is NOT a code defect.** Do not roll back the chart/route on an IdP outage alone.
- **Rollback is IdP-independent**: it is a chart/route revert, not an IdP operation.
- **Existing internal sessions stay valid** — cookie validation is fully offline (AES-GCM + exp; phase1 §5.7 / AR-27). The IdP is touched only at `/oauth2/start`, `/oauth2/callback`, and best-effort `/oauth2/logout`.
- **New logins die** while the IdP is down (`/oauth2/start` ⇒ `502 {"error":"oidc_discovery_unavailable"}` with a 30 s backoff — phase1 §4.4).
- **`/v1/*` is unaffected** — the gate is on the operator surface only; the LLM data plane has no IdP dependency (probe **P-H**).

### 1.9 Cloudflare masking note — AR-49
- Cloudflare fronts **`llm.daoduc.org`** (not `llm.ensem.dev`). The Cloudflare tunnel replaces **any** origin-5xx response body with a plain 16-byte `error code: 5xx`; 4xx bodies pass through verbatim.
- Consequence: the app's `502 {"error":"oidc_discovery_unavailable"}` (§1.8) is **masked** at the public URL. Debug 5xx **at or below nginx**, not through the public URL:
  ```bash
  kubectl --kubeconfig "$KC_DAODUC" -n nginx port-forward svc/nginx-ingress-nginx-controller 18443:443
  curl -k https://localhost:18443/fe/api/models -H 'Host: llm.daoduc.org'
  ```

---

## 2. Pre-Cutover Prerequisites

Each item is a hard gate. All must be green before **D1** (daoduc) and before **step A** (ensem).

### 2.1 Gate list

- [ ] **P1 — Phase 1 dormant image live & verified on BOTH hosts.** `OAUTH2_ENABLED` unset/false in pod env; `/fe/api/version` reports the Phase 1 run number on both hosts; dormant render is byte-identical to today (phase1 H6 empty diff).
- [ ] **P2 — Chart identity + cleanliness (AR-20).** `CHART` points at a **clean checkout at the merged Phase-1 commit** — **NEVER** the stale main checkout at `5ce0ead` (it has no internal-OAuth2 chart support ⇒ the "switch" renders as today + one env var ⇒ Window B). Assert:
  ```bash
  git -C "$CHART_CHECKOUT" rev-parse HEAD          # == the merged Phase-1 commit SHA, recorded in the evidence block
  git -C "$CHART_CHECKOUT" status --short -- k8s/   # MUST be empty (runbook §1.3 chart-cleanliness gate)
  ```
  Both assertions run at **D0** (§3.1) and again immediately before the D1/D3 upgrades.
- [ ] **P3 — `oauth2-proxy-secret` key inspection (keys only, never values):**
  ```bash
  kubectl --kubeconfig "$KC" -n llmproxy get secret oauth2-proxy-secret -o jsonpath='{.data}' | jq 'keys'
  ```
  Expect `CLIENT_ID`, `CLIENT_SECRET`, `COOKIE_SECRET`.
- [ ] **P4 — Secret mounted & app boots clean with the secret present (Phase 1, dormant).** The dormant image must start with the three `secretKeyRef` entries resolvable (mounted, inert) and produce **zero** panics (house G10) — a cookie-key mismatch must not surface as a crash in dormant mode.
- [ ] **P5 — Cookie-format posture recorded.** No cross-format compatibility (OQ-5); the internal cookie is `llmproxy_oauth2`; `OAUTH2_SESSION_TTL=24h` parity confirmed.
- [ ] **P6 — Browser re-login expectation communicated** (§1.5) to the operator list, in the cutover window notice.
- [ ] **P7 — P7′ scheduled.** The operator will hold a live edge session during the overlap; the overlap window is scheduled in a low-traffic period and announced (AR-52).
- [ ] **P8 — Rollback anchors captured (§3.1 D0, AR-46).** daoduc: **both** the live helm revision **and** the v203 revision; ensem: the live Deployment revision + generation.
- [ ] **P9 — IdP verification (daoduc only, §2.3).** Read-only confirmation that operator accounts on `door.daoduc.org` have verified emails.
- [ ] **P10 — Independent shell lane for ensem (§4.1).** A terminal that does not depend on `llm.ensem.dev` for its own LLM lane (human terminal or pre-staged commands run to completion without an LLM call mid-procedure).
- [ ] **P11 — P7′ edge-session acquisition (before D1 / before step A).** While the edge still owns `/oauth2`, the operator logs in via the edge flow and **stores the live `_oauth2_proxy` cookie** (e.g. in a cookie jar under `/tmp/ensem-oauth2-rollback/edge-session.jar` / the daoduc equivalent) for use in the D2/P-E(ii) rehearsal. After `/oauth2` is repointed to the app, the edge's own issuance path is unreachable, so this must happen **before** D1 (daoduc) and **before** step A (ensem).

### 2.2 Static pre-flight env validator — AR-17

**Run before ANY apply** (D1, D3, and ensem step B). It reads the **rendered** manifests, never the live cluster, and fails closed if the env vocabulary drifts from phase1 §4.2 (the single source of truth).

```bash
# daoduc — render from the CLEAN Phase-1 chart + the pinned values
RENDER=$(mktemp)
$HELM template llm-supervisor-proxy "$CHART" -n llmproxy -f "$VALUES" > "$RENDER"

# 1. REQUIRED set — the names the binary fail-fasts on when missing (phase1 §4.2 "yes" column) PLUS
#    OAUTH2_CLIENT_SECRET (required for confidential clients; phase1 §4.2 marks it "yes (PKCE public
#    clients may omit)" — included here so a confidential-client deploy cannot ship without it).
for v in OAUTH2_ENABLED OAUTH2_ISSUER_URL OAUTH2_CLIENT_ID OAUTH2_CLIENT_SECRET OAUTH2_COOKIE_SECRET \
         OAUTH2_EXTERNAL_URL OAUTH2_SESSION_TTL OAUTH2_EMAIL_DOMAINS OAUTH2_REQUIRE_EMAIL_VERIFIED; do
  grep -q "name: ${v}\$" "$RENDER" || { echo "VALIDATOR FAIL: missing $v"; exit 1; }
done
# 2. MEMBERSHIP — every OAUTH2_* name in the render MUST be a member of the phase1 §4.2 table.
#    The pre-ADR draft of this plan carried four names that are NOT in that table (AR-17);
#    a set-membership check catches every one of them without re-listing them here.
ALLOWED="OAUTH2_ENABLED OAUTH2_ISSUER_URL OAUTH2_CLIENT_ID OAUTH2_CLIENT_SECRET OAUTH2_REDIRECT_URL \
OAUTH2_EXTERNAL_URL OAUTH2_COOKIE_SECRET OAUTH2_SESSION_TTL OAUTH2_COOKIE_NAME OAUTH2_COOKIE_SECURE \
OAUTH2_COOKIE_SAMESITE OAUTH2_EMAIL_DOMAINS OAUTH2_ALLOW_ANY_EMAIL_DOMAIN OAUTH2_REQUIRE_EMAIL_VERIFIED \
OAUTH2_ALLOWED_ALGS OAUTH2_JWKS_CACHE_TTL OAUTH2_JWKS_MIN_REFRESH_INTERVAL OAUTH2_JWKS_STALE_GRACE \
OAUTH2_DISCOVERY_TIMEOUT OAUTH2_TOKEN_EXCHANGE_TIMEOUT OAUTH2_USERINFO_TIMEOUT OAUTH2_ALLOWED_ENDPOINTS \
OAUTH2_LOGOUT_REDIRECT OAUTH2_STATE_STORE_MAX OAUTH2_RATE_LIMIT_RPM OAUTH2_RATE_LIMIT_BURST"
# Excluded with reason (not in phase1 §4.2 as a standalone row, and not rendered by the chart
# per phase1 §8.3): OAUTH2_ALLOW_INSECURE_ISSUER — referenced only in OAUTH2_ISSUER_URL's
# semantics (phase1 §4.2 row for OAUTH2_ISSUER_URL) and in the EnvAllowInsecureIssuer Go
# constant (phase1 §4.1). If the phase-1 worker adds a standalone row, re-add it here.
for v in $(grep -o 'name: OAUTH2_[A-Z_]*' "$RENDER" | awk '{print $2}' | sort -u); do
  case " $ALLOWED " in *" $v "*) : ;; *) echo "VALIDATOR FAIL: $v is not in the phase1 §4.2 table"; exit 1;; esac
done
# 4. The three-state guard must NOT fire on the values under test.
$HELM template llm-supervisor-proxy "$CHART" -n llmproxy -f "$VALUES" >/dev/null || { echo "VALIDATOR FAIL: render refused"; exit 1; }
rm -f "$RENDER"
```

**Secret posture (S2):** the three secret values (`OAUTH2_CLIENT_ID`, `OAUTH2_CLIENT_SECRET`, `OAUTH2_COOKIE_SECRET`) are **always** `secretKeyRef` when internal mode renders env — never literals in the GitOps values/manifest. Literal `env` entries are permitted **only** for non-secret tuning knobs (e.g. `OAUTH2_SESSION_TTL`, `OAUTH2_COOKIE_SECURE`). phase1 §8.3 is being amended to the same posture.

**ensem variant** — the same name-set check against the two staged manifest files (no chart involved). `ALLOWED` is the same set defined in the daoduc validator above (`OAUTH2_STATE_STORE_MAX` included per S1).

```bash
# (BLK-4) Split the loop: the three SECRET names (CLIENT_ID/CLIENT_SECRET/COOKIE_SECRET)
# exist ONLY in the Deployment (secretKeyRef); the non-secret OAUTH2_* names must be
# present in BOTH the Deployment (configMapKeyRef) and the ConfigMap (data).
for v in OAUTH2_CLIENT_ID OAUTH2_CLIENT_SECRET OAUTH2_COOKIE_SECRET; do
  grep -q "$v" "$DEPLOY" || { echo "VALIDATOR FAIL: $DEPLOY missing $v"; exit 1; }
done
for v in OAUTH2_EXTERNAL_URL OAUTH2_EMAIL_DOMAINS OAUTH2_SESSION_TTL; do
  grep -q "$v" "$DEPLOY"   || { echo "VALIDATOR FAIL: $DEPLOY missing $v"; exit 1; }
  grep -q "$v" "$CONFIGMAP" || { echo "VALIDATOR FAIL: $CONFIGMAP missing $v"; exit 1; }
done
# Secret posture (S2): the three OAUTH2_CLIENT_* names above MUST appear in DEPLOY as
# secretKeyRef entries (never literals, and never in the ConfigMap). The exact keyRef
# mapping (CLIENT_ID -> CLIENT_ID, etc.) is asserted by the §4.5.2 diff.

# Membership check (P4): every OAUTH2_* name in the two staged manifests MUST be in the
# phase1 §4.2 ALLOWED set. ALLOWED is defined in the daoduc validator above.
for v in $(grep -oh 'OAUTH2_[A-Z_]*' "$DEPLOY" "$CONFIGMAP" | sort -u); do
  case " $ALLOWED " in
    *" $v "*) : ;;
    *) echo "VALIDATOR FAIL: $v is not in the phase1 §4.2 table"; exit 1 ;;
  esac
done
```

### 2.3 Daoduc read-only IdP verification (leader ruling 5) — before D1

**Read-only. No Casdoor mutation. No client registration, no secret rotation, no redirect-URI edit.**

1. For each account that will sign in on `door.daoduc.org` (operator accounts), confirm the account's email is **verified** (`email_verified == true` as surfaced by Casdoor's own account/admin view).
2. If **every** account is verified → keep the strict posture: `OAUTH2_EMAIL_DOMAINS: "daoduc.org"`, `OAUTH2_REQUIRE_EMAIL_VERIFIED: true` ⇒ unverified ⇒ 403.
3. If **any** account is unverified → **tighten the allowlist first** (set `OAUTH2_EMAIL_DOMAINS` to the exact operator domain(s)) and **do not proceed** until every operator account on `door.daoduc.org` is verified. **Fail-closed: no runtime bypass, no WARN mitigation.** Strict 403 in every mode. Record the decision + the outcome in the evidence block.
4. Record the outcome in the evidence block (§5.3) and confirm the corresponding rendered env before D1 (§2.2 validator).
5. **HARD PRECONDITION (tester R2 / phase1 §5.5's live cross-check):** before enabling on any host — here, before D1 — decode ONE real Casdoor id_token from this host's IdP (`door.daoduc.org`) and assert the internal verifier accepts it. Inspect the **JOSE header `typ` + claims shape ONLY** — never echo a secret or a token value; record only the shape. Record the outcome in the evidence block (§5.3).

---

## 2.4 Mandatory pre-flight harness — Phase 3 gate (NEW)

> **Phase 3 may NOT execute until this harness PASSES.** The harness is mechanical (no judgment): every check below is a command with a defined pass/fail and a recorded evidence line. Run it in the same shell that will execute D1 (same-shell rule, runbook §Environment block). Record the evidence block (§2.4.4) in the evidence block (§5.3).
> **HARD PRECONDITION (tester R2 / phase1 §5.5's live cross-check):** before enabling on any host, decode ONE real Casdoor id_token from that host's IdP and assert the internal verifier accepts it — inspect the **JOSE header `typ` + claims shape ONLY** (never echo a secret or a token value) and record the PASS/FAIL line in the §2.4.4 evidence block.

### 2.4.1 (a) Mechanical chart validation — three states (REAL render, not prose)

```bash
: "${CHART:?CHART unset}"; : "${VALUES:?VALUES unset}"
export HELM=/home/nea/bin/helm
# edge-only (today's render — must be byte-identical to the pre-change chart)
$HELM template llm-supervisor-proxy "$CHART" -n llmproxy -f "$VALUES" > /tmp/render.edgeonly.yaml
# overlap (D1 target)
$HELM template llm-supervisor-proxy "$CHART" -n llmproxy -f "$VALUES"   --set internalOAuth2.enabled=true --set internalOAuth2.overlap=true --set oauth2Proxy.enabled=true   > /tmp/render.overlap.yaml
# internal-only (D3 target)
$HELM template llm-supervisor-proxy "$CHART" -n llmproxy -f "$VALUES"   --set internalOAuth2.enabled=true --set oauth2Proxy.enabled=false --set oauth2Proxy.replicas=0   > /tmp/render.internalonly.yaml

# edge-only: empty diff vs the pre-change chart render (phase1 §8.4 H6)
$HELM template llm-supervisor-proxy "$CHART" -n llmproxy -f "$VALUES" > /tmp/render.pre.yaml
diff /tmp/render.pre.yaml /tmp/render.edgeonly.yaml && echo "PASS edge-only = empty diff" || { echo "FAIL edge-only drift"; exit 1; }

# overlap: nginx auth annotations PRESENT AND app env PRESENT AND /oauth2 -> app
grep -q "auth-signin" /tmp/render.overlap.yaml && echo "PASS overlap: annotations present" || { echo "FAIL overlap: annotations missing"; exit 1; }
grep -q "name: OAUTH2_ENABLED" /tmp/render.overlap.yaml && echo "PASS overlap: app env present" || { echo "FAIL overlap: app env missing"; exit 1; }
grep -q -- "- path: /oauth2" /tmp/render.overlap.yaml && echo "PASS overlap: /oauth2 -> app" || { echo "FAIL overlap: /oauth2 not on app"; exit 1; }

# internal-only: annotations ABSENT AND edge Deployment rendered at replicas: 0 AND /oauth2 -> app
grep -q "auth-signin" /tmp/render.internalonly.yaml && { echo "FAIL internal-only: annotations still present"; exit 1; } || echo "PASS internal-only: annotations absent"
grep -q "name: oauth2-proxy" /tmp/render.internalonly.yaml && echo "PASS internal-only: edge objects rendered" || { echo "FAIL internal-only: edge objects pruned (AR-21)"; exit 1; }
awk '/^# Source: .*oauth2-proxy-deployment\.yaml$/{f=1;next} f&&/^# Source: /{f=0} f' /tmp/render.internalonly.yaml | grep -q "replicas: 0"   && echo "PASS internal-only: edge replicas: 0" || { echo "FAIL internal-only: edge not at replicas: 0"; exit 1; }
grep -q -- "- path: /oauth2" /tmp/render.internalonly.yaml && echo "PASS internal-only: /oauth2 -> app" || { echo "FAIL internal-only: /oauth2 not on app"; exit 1; }
```

### 2.4.2 (b) `kubectl apply --dry-run=server` field-ownership check (ensem manifest)

```bash
: "${KC_ENSEM:?KC_ENSEM unset}"; export KC="$KC_ENSEM"
DEPLOY=/home/nea/Code/data-center-scripts/infranstructure/k8s/llmproxy/rancher-local-ensem-dev/deployment.yaml
# Proves the spec.strategy patch cannot be clobbered: dry-run the apply and inspect the
# resulting strategy field. The manifest is authoritative (BLK-1): strategy must be Recreate.
$KCTL --kubeconfig "$KC" -n llmproxy apply --dry-run=server -f "$DEPLOY" -o yaml   | grep -A2 "strategy:" | head -4
# live re-check (enforcement gate) — must equal Recreate, else abort (§4.5.3)
STRATEGY=$($KCTL --kubeconfig "$KC" -n llmproxy get deployment llm-supervisor-proxy             -o jsonpath='{.spec.strategy.type}')
[ "$STRATEGY" = "Recreate" ] && echo "PASS spec.strategy.type=Recreate" || { echo "FAIL spec.strategy.type=$STRATEGY"; exit 1; }
```

### 2.4.3 (c) `bash -n` on EVERY script block in this plan

```bash
# Extract every ```bash block and syntax-check it. Report: N blocks, 0 failures.
python3 - <<'PY'
import re, subprocess
plan = open('/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-internal-oauth2/.agents/shared/planning/internal-oauth2/phase2-switch-plan.md').read()
blocks = re.findall(r'```bash\n(.*?)```', plan, re.S)
fails = [i for i,b in enumerate(blocks) if subprocess.run(['bash','-n'], input=b, capture_output=True, text=True).returncode != 0]
print(f"blocks: {len(blocks)}  failures: {len(fails)}")
if fails: print("failing blocks:", fails)
PY
```

### 2.4.4 Recorded evidence block (Phase 3 may not execute until this harness passes)

```
Phase-3 pre-flight harness result:
  Date (UTC): 
  Operator: 
  (a) Mechanical chart validation: [PASS / FAIL]   (edge-only empty diff; overlap annotations+env+/oauth2->app; internal-only annotations absent+edge@replicas:0+/oauth2->app)
  (b) kubectl apply --dry-run=server field-ownership: [PASS / FAIL]   (spec.strategy.type=Recreate)
  (c) bash -n on all script blocks: [N blocks, 0 failures]
  Verdict: [PASS — Phase 3 may execute | FAIL — STOP]
```

---

## 3. Host 1: `daoduc` (Backup — Phase 3) — TWO UPGRADES + PRE-STAGE

> The cutover is **two Helm upgrades plus a pre-stage**, not one. A single upgrade that drops the nginx auth annotations while the pods roll creates **Window A** (the old, un-gated pod serves `/fe/api/*` unauthenticated for the rollout duration). The split below closes it by construction: the annotations drop only in **D3**, against pods that are **already enforcing** (I-2).

### 3.0 Choreography table

| Step | Action | `/`,`/ui`,`/fe/api` | `/oauth2` | App pods |
|---|---|---|---|---|
| **D0** | Pre-flight: `helm history` (capture **live rev AND v203 rev**), `helm template` == live render, chart identity + cleanliness, stage Recreate/maxSurge-0 | edge-gated | edge | dormant |
| **D1** | **Overlap upgrade**: `internalOAuth2.enabled=true` + `oauth2Proxy.enabled=true` + `overlap=true` + issuer/external URL + `secretKeyRef` block | **edge + app-enforce** | → **app** | **roll to enforcing (behind the live edge)** |
| **D2** | Probe matrix (**overlap column**) + login rehearsal (P-E): operator holds a live edge session (P7′) and mints `llmproxy_oauth2` through the app's flow | edge + app-enforce | app | enforcing, probed |
| **D3** | **Internal-only upgrade**: `overlap=false`, `oauth2Proxy.enabled=false`, `oauth2Proxy.replicas=0` | **app-enforced** | app | **no rollout** (pod template unchanged) |
| **D4** | Soak + full matrix; P-J asserts the edge Deployment+Service are **still rendered** at `replicas: 0` | app-enforced | app | enforcing |

### 3.1 D0 — pre-flight (no mutation)

```bash
: "${KC_DAODUC:?environment block not loaded}"
export KC="$KC_DAODUC"

# G0 kubeconfig guard (daoduc server must be https://46.62.137.105:6443)
GOT=$($KCTL --kubeconfig "$KC" config view --minify -o jsonpath='{.clusters[0].cluster.server}')
[ "$GOT" = "https://46.62.137.105:6443" ] || { echo "WRONG CLUSTER: $GOT — STOP"; exit 1; }

# 1. AR-46 — capture BOTH revisions into the evidence block: the LIVE rev (rollback anchor)
#    and the v203 rev (deep-fallback anchor). Never assume either number.
$HELM --kubeconfig "$KC" history llm-supervisor-proxy -n llmproxy | tee /tmp/helm-history.daoduc.txt
#    LIVE_REVISION = the revision whose status is "deployed"  -> tier-1 anchor
#    V203_REVISION = the revision whose chart/app_version is v203 (df12c62) -> tier-2 anchor
echo "LIVE_REVISION=<fill from helm history>"; echo "V203_REVISION=<fill from helm history>"

# 2. AR-20 — CHART must be a CLEAN checkout at the MERGED PHASE-1 COMMIT.
#    NEVER /home/nea/Code/opensource-projects/llm-supervisor-proxy/k8s (stale main checkout at 5ce0ead).
export CHART_CHECKOUT=/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-phase1-merged   # clean worktree at the merged SHA
export CHART="$CHART_CHECKOUT/k8s"
git -C "$CHART_CHECKOUT" rev-parse HEAD            # MUST equal the merged Phase-1 commit SHA (record it)
git -C "$CHART_CHECKOUT" status --short -- k8s/   # MUST be EMPTY (runbook §1.3 chart-cleanliness gate)
[ -z "$(git -C "$CHART_CHECKOUT" status --short -- k8s/)" ] || { echo "CHART DIRTY — STOP"; exit 1; }

# 3. helm template == live render (runbook §1.4). Expect: trailing-blank-line difference only.
export VALUES=/home/nea/Code/data-center-scripts/infranstructure/k8s/llmproxy/hetzner-daoduc/values-pinned.yaml
$HELM template llm-supervisor-proxy "$CHART" -n llmproxy -f "$VALUES" > /tmp/render.pre.yaml
$HELM --kubeconfig "$KC" get manifest llm-supervisor-proxy -n llmproxy > /tmp/live.man.yaml
diff /tmp/live.man.yaml /tmp/render.pre.yaml

# 4. Edge ownership + secret key names (keys only)
$KCTL --kubeconfig "$KC" -n llmproxy get deploy,svc oauth2-proxy -o wide
$KCTL --kubeconfig "$KC" -n llmproxy get secret oauth2-proxy-secret -o jsonpath='{.data}' | jq 'keys'

# 5. P7′ — acquire and store the operator's LIVE EDGE SESSION while the edge still owns /oauth2.
#    After D1 repoints /oauth2 -> app, the edge's own issuance path is unreachable, so this MUST
#    happen before D1. Store the cookie jar for the D2/P-E(ii) rehearsal.
mkdir -p /tmp/daoduc-oauth2-rollback
curl -sS -c /tmp/daoduc-oauth2-rollback/edge-session.jar -b /tmp/daoduc-oauth2-rollback/edge-session.jar   https://llm.daoduc.org/oauth2/start?rd=%2Fui%2F >/dev/null
#    (complete the Casdoor login in the browser; the jar now holds _oauth2_proxy)
[ -s /tmp/daoduc-oauth2-rollback/edge-session.jar ] || { echo "P7' acquisition FAIL"; exit 1; }

# 6. AR-53 — stage Recreate/maxSurge-0 (see the branch below); DO NOT apply yet.
```

**⚠️ Revision-count discrepancy (ledger).** `values-pinned.yaml:302` says rev 46→47 while the ship note says 47→48. **Do not trust the file** — the live revision is read from `helm history` above and recorded in the evidence block (§5.3). The same applies to the v203 revision used by the tier-2 rollback.

**AR-53 branch — Recreate / maxSurge 0 (pod straddle).** The internal layer's state store is **per-pod** (phase1 §5.6 / AR-11 / AR-65): a rolling update would strand a login across two pods (`400 invalid_state` mid-rollout). Decide by inspecting the **rendered D0 baseline** (`/tmp/render.pre.yaml`, saved above — read-only at D0 time; the overlap render is produced later in §3.2.2 and re-checks the same question in internal mode):
```bash
grep -q "strategy:" /tmp/render.pre.yaml && echo "chart renders spec.strategy in DORMANT mode — unexpected; investigate" || echo "chart does not render spec.strategy in dormant mode — the AR-53 patch is required (re-check against /tmp/render.overlap.yaml in §3.2.2)"
```
- The D0 render is the dormant render, so `strategy:` is expected to be **absent** here ⇒ the patch is required. The D1 render (§3.2.2, internal mode) re-checks the same question: if the merged Phase-1 chart renders `spec.strategy` in internal mode (phase1 §8.2 `strategy: Recreate` / `maxSurge: 0`), the patch is a no-op and helm owns it; if it does **not**, the patch is mandatory.
- If the patch is required, stage the house-precedent patch (same shape as the v203 C4 ruling) and apply it **immediately before D1**:
  ```bash
  $KCTL --kubeconfig "$KC" -n llmproxy patch deployment llm-supervisor-proxy --type=strategic \
    -p '{"spec":{"strategy":{"type":"Recreate","rollingUpdate":null}}}'
  ```
  and **revert** it after D4 goes green (house precedent — a `spec.strategy`-only change causes no rollout, no restart):
  ```bash
  $KCTL --kubeconfig "$KC" -n llmproxy patch deployment llm-supervisor-proxy --type=strategic \
    -p '{"spec":{"strategy":{"type":"RollingUpdate","rollingUpdate":{"maxSurge":1,"maxUnavailable":0}}}}'
  ```

### 3.2 D1 — OVERLAP upgrade (exact diff + commands)

**What D1 must set (I-1 + I-3):** `internalOAuth2.enabled: true`, `internalOAuth2.overlap: true`, `oauth2Proxy.enabled: true` (edge stays in-path), `oauth2Proxy.replicas: 1`, the issuer + external URL, and the `secretKeyRef` block (`internalOAuth2.existingSecret: oauth2-proxy-secret`).

**What D1 must NOT do:** flip a bare `env.OAUTH2_ENABLED` — that renders **no** `OAUTH2_CLIENT_ID` ⇒ startup fail-fast ⇒ **CrashLoopBackOff ⇒ the backup's `/v1/*` dies (Window B)** (AR-17). The `OAUTH2_*` env block is emitted by the chart **only** when `internalOAuth2.enabled` (phase1 §8.3), so the toggle is the switch.

#### 3.2.1 Exact unified diff — `data-center-scripts/infranstructure/k8s/llmproxy/hetzner-daoduc/values-pinned.yaml`

The `image:` block is **not** part of this diff: Phase 1 already repinned `image.tag` to the Phase-1 SHA (the diff context shows it unchanged).

```diff
--- a/infranstructure/k8s/llmproxy/hetzner-daoduc/values-pinned.yaml
+++ b/infranstructure/k8s/llmproxy/hetzner-daoduc/values-pinned.yaml
@@ -361,8 +361,19 @@ ingress:
     - path: /
       pathType: Prefix
   tls:
   - hosts:
       - llm.daoduc.org
     secretName: llm-supervisor-proxy-tls
+internalOAuth2:
+  enabled: true
+  overlap: true
+  issuerURL: "https://door.daoduc.org"
+  externalURL: "https://llm.daoduc.org"
+  emailDomains: "daoduc.org"
+  allowAnyEmailDomain: false
+  requireEmailVerified: true
+  allowedAlgs: "RS256"
+  sessionTTL: "24h"
+  existingSecret: "oauth2-proxy-secret"
 livenessProbe:
   failureThreshold: 3
   httpGet:
@@ -373,7 +384,13 @@ nameOverride: ""
 namespace: llmproxy
 nodeSelector: {}
+oauth2Proxy:
+  enabled: true
+  replicas: 1
+  issuerURL: "https://door.daoduc.org"
+  emailDomain: "*"
+  cookieExpire: "24h"
+  existingSecret: "oauth2-proxy-secret"
 podSecurityContext: {}
 readinessProbe:
   failureThreshold: 3
   httpGet:
```

Notes:
- `internalOAuth2.emailDomains: "daoduc.org"` — the **tightened** daoduc posture (leader ruling 5, §1.7). `allowAnyEmailDomain: false` + `requireEmailVerified: true` ⇒ strict `email_verified` (403 on unverified).
- `internalOAuth2.existingSecret: "oauth2-proxy-secret"` — the **same** Secret as the edge's; the three keys resolve via `secretKeyRef` (phase1 §8.3). **No new Secret, no regeneration** (OQ-1, §1.4).
- The `oauth2Proxy` block is pinned **explicitly** to the chart defaults (which equal today's hardcoded values, per phase1 §8.4 H6) so that the D3 delta is exactly three lines and no default drift can surprise the switch.
- `image.tag` is untouched here (Phase 1 already repinned it).

#### 3.2.2 Render assertions for the OVERLAP state (phase1 §8.4 H2) + AR-17 validator

```bash
$HELM template llm-supervisor-proxy "$CHART" -n llmproxy -f "$VALUES" > /tmp/render.overlap.yaml

# a. edge annotations STAY (I-2 not yet reached)
grep -q "auth-signin" /tmp/render.overlap.yaml && echo "OK annotations stay" || { echo "FAIL annotations dropped too early"; exit 1; }
grep -q "auth-url"   /tmp/render.overlap.yaml || { echo "FAIL auth-url missing"; exit 1; }
# b. /oauth2 -> APP service (I-1): daoduc renders a nginx Ingress, so the path is
#    "path: /oauth2" (NOT "value: /oauth2" — that form is the ensem Envoy HTTPRoute).
#    Render evidence (verified against the merged Phase-1 chart + the real pinned values at
#    data-center-scripts/.../hetzner-daoduc/values-pinned.yaml, helm at /home/nea/bin/helm):
#      grep -c "path: /oauth2"   /tmp/render.overlap.yaml  -> 1
#      grep -c "value: /oauth2"  /tmp/render.overlap.yaml  -> 0
grep -q -- "- path: /oauth2" /tmp/render.overlap.yaml && echo "OK /oauth2 -> app" || { echo "FAIL /oauth2 not on app"; exit 1; }
# c. app env block + secretKeyRef present (I-3)
grep -q "name: OAUTH2_ENABLED" /tmp/render.overlap.yaml || { echo "FAIL OAUTH2_ENABLED not rendered"; exit 1; }
grep -q "secretKeyRef" /tmp/render.overlap.yaml || { echo "FAIL no secretKeyRef wiring"; exit 1; }
# c2. secretKeyRef KEY-NAME assertion (note 2): the three secret envs must resolve to the
#     phase1 §8.2 canonical key names (clientID/clientSecret/cookieSecret -> their phase1
#     canonical keys) and NONE may be empty — a key:"" makes the pod unstartable = Window B.
#     (phase1 §8.3 currently names them CLIENT_ID/CLIENT_SECRET/COOKIE_SECRET in the secret;
#      the chart must map to those exact names; phase1 is being amended to the same posture.)
for kv in "OAUTH2_CLIENT_ID:CLIENT_ID" "OAUTH2_CLIENT_SECRET:CLIENT_SECRET" "OAUTH2_COOKIE_SECRET:COOKIE_SECRET"; do
  envname="${kv%%:*}"; keyname="${kv##*:}"
  grep -A2 "name: ${envname}" /tmp/render.overlap.yaml | grep -q "key: ${keyname}"     || { echo "FAIL secretKeyRef key mismatch for ${envname} (expected ${keyname})"; exit 1; }
  grep -A2 "name: ${envname}" /tmp/render.overlap.yaml | grep -q "key: """     && { echo "FAIL empty secretKeyRef key for ${envname} (Window B risk)"; exit 1; } || true
done
echo "OK secretKeyRef key names match phase1 §8.2 canonical names"
# d. the three-state guard must NOT fire
$HELM template llm-supervisor-proxy "$CHART" -n llmproxy -f "$VALUES" >/dev/null || { echo "FAIL render refused (three-state guard)"; exit 1; }
# e. the full AR-17 static validator (§2.2) — REQUIRED set, FORBIDDEN set, secretKeyRef
```

#### 3.2.3 Deploy D1

```bash
: "${KC_DAODUC:?environment block not loaded}"
export KC="$KC_DAODUC"
# (apply the Recreate patch here ONLY if §3.1 step 5 said "patch required")

$HELM --kubeconfig "$KC" upgrade llm-supervisor-proxy "$CHART" \
  -n llmproxy -f "$VALUES" --wait --timeout 10m --atomic --cleanup-on-fail

# Capture the revision this deploy created — used by the D3 rollback tier (§3.7).
D1_REVISION=$($HELM --kubeconfig "$KC" history llm-supervisor-proxy -n llmproxy | awk '/deployed/{r=$1} END{print r}')
echo "D1_REVISION=$D1_REVISION"
```

- `--atomic` (implies `--wait`) rolls back **to the immediately-previous revision only when the upgrade itself fails** — for D1 that is the Phase-1 dormant revision (edge-only), which is safe.
- **No `--reuse-values`** (it takes the live release values and ignores the pinned file — the file stops being the source of truth). **No `kubectl set image` on daoduc** (it desyncs the Deployment from helm release state and mints phantom revisions).
- Expected `--wait` duration: ~25–40 s (house precedent for the Phase-1 rollout).
- After the upgrade returns, the enforcing pods are live **behind** the still-present edge annotations.

### 3.3 D2 — probe matrix (overlap column) + login rehearsal (P-E)

Run the house gate **G0–G10** (runbook §3) with `EXPECTED_DIGEST`/`EXPECTED_RUN` = the Phase-1 values, `LINE_HOST=llm.daoduc.org`, `LT=daoduc`, **plus** the overlap-column probes of the matrix (§6):

| Probe | Overlap expectation | ALARM if different |
|---|---|---|
| **P-A** anon `/fe/api/models` | **302** (edge) | **200 = EXPOSED** |
| **P-B** follow redirects on `/ui` (jar, max 5) | terminates after **one** app bounce | **≥3 same-origin 302s = LOOP** |
| **P-C** anon `/` | 302 → edge signin | 200 = exposed |
| **P-D** `/ui/` + valid app cookie | 302 (no app cookie yet in overlap) | **ALARM (conditional — see note below):** a 302 is a failure ONLY if the P-E browser rehearsal has already run and minted `llmproxy_oauth2`; before that rehearsal, 302 is expected. |
| **P-E(i)** scripted probe — anon `/fe/api/models` (no cookie, no token) | **401 JSON `{"error":"unauthenticated"}`** (class 5; the app never mints a session cookie on `/fe/api/*` — cookies come ONLY from `/oauth2/start` → `/oauth2/callback`) | **200 = EXPOSED; 302 = the edge is still gating (D3 not effective)** |
| **P-E(ii)** browser rehearsal — operator holds a live edge session (P7′) and runs the app's own flow | **200 + `Set-Cookie: llmproxy_oauth2`** after the app's flow completes | failure to mint = flow broken; a 302 here = LOOP (Window C live) |
| **P-F** forged `X-Auth-Request-*` / forged cookie, direct-to-Service | 401 | **200 = header-trust bypass** |
| **P-G** anon `/fe/api/credentials` `/tokens` `/config` | 401 | **200 = exposed admin plane** |
| **P-H** `/v1/models` 200; `/healthz` 200; `/v1/chat/completions` without token ⇒ proxy 401 | all green | any change = `/v1` affected |
| **P-I** pods 1/1 Running, restartCount 0, env names present | green | **CrashLoopBackOff = Window B live** |
| **P-J** `kubectl get deploy oauth2-proxy` exists, replicas as intended | exists, `replicas: 1` | **missing = Helm pruned the rollback anchor** |
| **P-K** `/oauth2/start` with a valid app cookie | **302 `rd`, zero IdP hop** | IdP hop = short-circuit missing |
| **P-L** dormant `/oauth2/start` | n/a (the app is enforcing in overlap) | unpinned = doc gap |

**P-D ALARM — conditional (BLK-3).** P-D's ALARM fires **only** once the P-E(ii) browser rehearsal has actually run and minted `llmproxy_oauth2`. Before that rehearsal, a 302 on `/ui/` with an app cookie is expected (the cookie was never minted); after the rehearsal, a 302 is a failure (the app cookie is not being honoured). The scripted probe P-E(i) never expects a 302 at all — it expects 401 JSON.

**P-E login rehearsal (P7′).** With the operator holding a **live edge session** (a valid `_oauth2_proxy` cookie — P7′) and the app now enforcing behind the edge:
1. Browser → `https://llm.daoduc.org/ui/` → the **edge** passes the operator through (edge cookie valid) → the **app** guard runs (it runs on every request that reaches the app) → the app has no `llmproxy_oauth2` yet → 302 → `/oauth2/start` → **now on the app** (I-1) → Casdoor → callback → the app mints `llmproxy_oauth2` → back to `/ui/` → **200**.
2. Assert: response carries `Set-Cookie: llmproxy_oauth2=…` and **not** `_oauth2_proxy`.
3. Repeat once: `/ui/` now returns 200 with the app cookie present; `/oauth2/start` returns **302 `rd`** with **zero** IdP hop (P-K — the AR-54 short-circuit).
4. **Record** the rehearsal outcome in the evidence block. A failed rehearsal ⇒ **STOP before D3** — do not remove the edge while the app's flow is unproven.

### 3.4 D3 — INTERNAL-ONLY upgrade (exact diff + commands)

**What D3 sets:** `internalOAuth2.overlap: false`, `oauth2Proxy.enabled: false` (edge leaves the path ⇒ nginx annotations drop), `oauth2Proxy.replicas: 0` (the edge is **idled**, not deleted — AR-21/OQ-7).

#### 3.4.1 Exact unified diff — `values-pinned.yaml`

> **Apply-on-top note:** this diff applies to the file **as it stands after the D1 pin commit** (§3.2.1), i.e. the D1 state — not to the pristine repository file. The context lines below are therefore the D1-produced `internalOAuth2:` / `oauth2Proxy:` blocks. Verify with `git diff` after editing, exactly as the runbook does.

```diff
--- a/infranstructure/k8s/llmproxy/hetzner-daoduc/values-pinned.yaml
+++ b/infranstructure/k8s/llmproxy/hetzner-daoduc/values-pinned.yaml
@@ -363,13 +363,13 @@ ingress:
 internalOAuth2:
   enabled: true
-  overlap: true
+  overlap: false
   issuerURL: "https://door.daoduc.org"
   externalURL: "https://llm.daoduc.org"
   emailDomains: "daoduc.org"
   allowAnyEmailDomain: false
   requireEmailVerified: true
   allowedAlgs: "RS256"
   sessionTTL: "24h"
   existingSecret: "oauth2-proxy-secret"
 livenessProbe:
   failureThreshold: 3
@@ -384,8 +384,8 @@ nodeSelector: {}
 oauth2Proxy:
-  enabled: true
-  replicas: 1
+  enabled: false
+  replicas: 0
   issuerURL: "https://door.daoduc.org"
   emailDomain: "*"
   cookieExpire: "24h"
   existingSecret: "oauth2-proxy-secret"
 podSecurityContext: {}
 readinessProbe:
   failureThreshold: 3
```

#### 3.4.2 Render assertions for the INTERNAL-ONLY state (phase1 §8.4 H3) — **no app pod rollout**

```bash
$HELM template llm-supervisor-proxy "$CHART" -n llmproxy -f "$VALUES" > /tmp/render.internalonly.yaml

# a. annotations DROP (I-2 — legal now: every serving pod already enforces)
grep -q "auth-signin" /tmp/render.internalonly.yaml && { echo "FAIL annotations still present"; exit 1; } || echo "OK annotations dropped"
# b. /oauth2 -> APP service (I-1 stays true)
# c. the edge Deployment + Service are STILL RENDERED at replicas 0 (AR-21 — Helm must not prune).
#    Real assertions (not inert notes): both objects exist AND the edge Deployment's replicas: 0.
grep -q "name: oauth2-proxy" /tmp/render.internalonly.yaml || { echo "FAIL edge objects pruned — the rollback anchor is gone (AR-21)"; exit 1; }
# extract the edge Deployment document (bounded by # Source: lines) and assert replicas: 0
awk '/^# Source: .*oauth2-proxy-deployment\.yaml$/{f=1;next} f&&/^# Source: /{f=0} f' /tmp/render.internalonly.yaml | grep -q "replicas: 0"   || { echo "FAIL edge Deployment not at replicas: 0 (AR-21)"; exit 1; }
echo "OK edge Deployment+Service rendered at replicas: 0"
# d. THE APP POD TEMPLATE MUST BE UNCHANGED vs the D1 render ⇒ NO ROLLOUT IN D3
#    The awk is bounded to the app Deployment DOCUMENT ONLY: it starts at the
#    "# Source: .../deployment.yaml" line and stops at the next "# Source:" line,
#    so the oauth2-proxy document's env (rendered in the same output) cannot be
#    picked up and false-fail a correct D3 diff.
diff <(awk '/^# Source: .*deployment\.yaml$/{f=1;next} f&&/^# Source: /{f=0} f' /tmp/render.overlap.yaml) \
     <(awk '/^# Source: .*deployment\.yaml$/{f=1;next} f&&/^# Source: /{f=0} f' /tmp/render.internalonly.yaml) \
  && echo "OK app pod template identical — D3 triggers NO rollout" \
  || { echo "FAIL app pod template changed — D3 would roll the app"; exit 1; }
# e. AR-17 validator (§2.2) again
# f. Stable-operand note (Issue 6): /tmp/render.pre.yaml (saved at D0) is retained as the
#    baseline operand for any D3 drift check; the D3 assertions above compare the
#    overlap vs internal-only renders (both saved to files, never inline pipes).
```

> **AR-21 dependency (chart-side).** For D3 to idle the edge without Helm pruning it, the merged Phase-1 chart must render the edge Deployment+Service whenever `oauth2Proxy.replicas` is explicitly set (even at `0`), i.e. the render gate is *"enabled **or** replicas explicitly present"*, not *"enabled"* alone (phase1 §8.2 `oauth2Proxy.replicas` split; phase1 §8.3). **Consistency with phase-1's Issue 1:** the edge Deployment+Service are **ALWAYS rendered by the chart** — `enabled:false` never prunes them — and D3 relies on exactly this. **If the merged chart prunes the edge objects at `enabled: false`, D3 MUST NOT RUN** — the rollback anchor would be destroyed. Escalate to the chart owner; the fix is a one-line template change, and it belongs in Phase 1, not in the cutover window.

#### 3.4.3 Deploy D3

```bash
: "${KC_DAODUC:?environment block not loaded}"
export KC="$KC_DAODUC"
$HELM --kubeconfig "$KC" upgrade llm-supervisor-proxy "$CHART" \
  -n llmproxy -f "$VALUES" --wait --timeout 10m --atomic --cleanup-on-fail
```
- `--atomic` on D3 rolls back **to the D1 revision** (the overlap state): the nginx annotations and the edge return **while the app stays enforcing** ⇒ the rollback lands in the **safe overlap state**, not an exposure (ADR-001 §Rollback).
- **Expected: NO pod rollout** (the app pod template is unchanged — asserted in §3.4.2 step d). If the rollout counter moves, the render assertion was wrong ⇒ stop and investigate.

### 3.5 D4 — soak + full matrix + P-J

- Run the **full** matrix (§6) in its **internal-only** column, plus the house gate G0–G10 and the daoduc checklist §3.6.
- **P-J (AR-21):** `kubectl --kubeconfig "$KC" -n llmproxy get deploy,svc oauth2-proxy` ⇒ the Deployment and Service **still exist**, with `replicas: 0`. **Missing = Helm pruned the rollback anchor** ⇒ immediate rollback (§3.6).
- Soak **10 minutes** (G9): zero restarts, identical pod name, `/v1/models` id-set unchanged vs the pre-switch baseline.
- After the soak: revert the Recreate patch if one was applied (§3.1 step 5).

### 3.6 Daoduc post-switch verification checklist (S-rows)

| # | Check | Expectation | ALARM |
|---|---|---|---|
| **S1** | `OAUTH2_ENABLED` in pod env | `true` | `false`/absent = enforcement never turned on |
| **S2** | Casdoor login round-trip (browser) | callback → `/ui/` 200, `Set-Cookie: llmproxy_oauth2` | no cookie minted = flow broken |
| **S3** | `/fe/api/models` post-login | 200 + model list | 302 to Casdoor = session not honoured |
| **S4** | `/v1/*` unaffected | id-set unchanged; smoke 200 | any change = `/v1` affected (P-H) |
| **S5** | `/healthz` in-cluster (port-forward) | `ok` | — |
| **S6** | SSE `/fe/api/events` through the browser | events arrive | 502/504 = broken |
| **S7** | logout | clears the session; `/` re-triggers login | — |
| **S8** | soak 10 min (G9) | 0 restarts, same pod, id-set unchanged | — |
| **S9** | **no-exposure assertion (AR-14)** — anon `/fe/api/models` (no cookie, no token) | **401 JSON `{"error":"unauthenticated"}`** | **302 = the edge is still gating (D3 not effective); 200 = EXPOSED** |
| **S10** | **`/fe/api/version` is gate-EXEMPT** — pod-direct `curl -s 127.0.0.1:4321/fe/api/version` | **200 `{"version":"…"}`** | 401 = the exemption is broken; house gate **G4** is pod-direct, so gating this path would break the release gate (AR-13 / ruling R-1; phase1 §6.2 class **4b**, `TestMiddleware_VersionIsExempt`) |
| **S11** | P-J edge objects | Deployment+Service exist at `replicas: 0` | missing = pruned anchor |
| **S12** | P-K short-circuit | `/oauth2/start` + valid app cookie ⇒ 302 `rd`, **zero** IdP hop | IdP hop = AR-54 short-circuit missing |

### 3.7 Per-step rollback plan (daoduc) — the tier differs per step (AR-18)

**The rollback target after D1 and after D3 are NOT the same.** Both are safe; they are different states.

| When | Command | Resulting state | Safe? |
|---|---|---|---|
| **After D1** (overlap live) | `$HELM --kubeconfig "$KC" rollback llm-supervisor-proxy <LIVE_REVISION> -n llmproxy --wait --timeout 180s` | **edge-only**: annotations + edge in path, app back to dormant (the Phase-1 dormant revision) | **yes** — the edge gates everything again |
| **After D3** (internal-only live) | `$HELM --kubeconfig "$KC" rollback llm-supervisor-proxy <D1_REVISION> -n llmproxy --wait --timeout 180s` | **overlap**: nginx annotations + edge **return**, the app **stays enforcing** | **yes** — this is the SAFE overlap state (ADR-001 §Rollback: "the app-enforcing-behind-live-edge state is itself the safe state to roll back into") |
| **Deep fallback (both)** | `$HELM --kubeconfig "$KC" rollback llm-supervisor-proxy <V203_REVISION> -n llmproxy --wait --timeout 180s` | pre-Phase-1 v203 posture (edge-only, no internal layer) | **yes** — Phase 1 builds ON v203, so this does **not** cross migration 034 |

- `<LIVE_REVISION>` / `<V203_REVISION>` are the values captured in **D0** (§3.1 step 1, AR-46). **Never assume a revision number.**
- **FORBIDDEN:** any rollback past v203 (`0082773`/v202 or earlier) **without reversing migration 034 first** (guarded SQL from the frozen down file; the ADR §7.1 ordering rule). A pre-034 binary treats an empty `allowed_models` as a wildcard, so image-first rollback would promote every migrated narrow token to full access.
- After **any** rollback, revert the GitOps pin per runbook §5.5 (§5.2) and re-verify with the house gate G0–G8 using the tier's `EXPECTED_DIGEST`/`EXPECTED_RUN`.

### 3.8 Phase 3 task list

| # | Task | Depends on | Acceptance |
|---|---|---|---|
| **T3.1** | D0 pre-flight: `helm history` (live + v203 revs), chart identity + cleanliness, `helm template` == live, secret key names, Recreate branch decision | — | §3.1 commands green; both revisions recorded in the evidence block |
| **T3.2** | Stage the D1 values diff in `data-center-scripts` (chart toggles — **not** a bare env flip) + run the AR-17 static validator | T3.1 | §2.2 validator green; §3.2.2 render assertions green |
| **T3.3** | D1 overlap upgrade | T3.2 | enforcing pods live; **no** exposure; G0–G10 green |
| **T3.4** | **Enumerate the probe matrix** (path → expected status **pre-switch** → expected status **post-switch**) for every operator path, with owner / repo / path — the replacement for the operator gate script named in the pre-ADR draft (AR-50: that script does not exist on disk, so it is not referenced). Owner: the Phase-3 operator; artifact: `.agents/tester/RESULTS/<date>-internal-oauth2-phase3-gate.md` in the proxy repo; the live paths are the §6 matrix (P-A…P-L) plus the §3.6 S-rows | T3.3 | matrix written, every path has a pre/post expectation, owner + repo + path recorded |
| **T3.5** | D2 login rehearsal (P-E) under P7′ | T3.4 | `llmproxy_oauth2` minted; P-K short-circuit green |
| **T3.6** | D3 internal-only upgrade | T3.5 | annotations dropped; **no** app pod rollout; P-J green (edge objects at `replicas: 0`) |
| **T3.7** | D4 soak + full matrix; revert the Recreate patch if applied | T3.6 | 10-min soak clean; §6 internal-only column green |
| **T3.8** | GitOps pin commit for the daoduc host (§5.2) | T3.7 | one commit per host; `main` untouched |

---

## 4. Host 2: `ensem` (Primary — Phase 4 — HARD HELD)

> ### 🛑 HARD HOLD — DO NOT EXECUTE until ALL of:
> 1. Phase 3 daoduc soak passes (§3.5 D4: 10-min soak clean, zero restarts, full matrix green).
> 2. Explicit Emperor/operator sign-off recorded (date + approver in the evidence block, §5.3).
> 3. **Independent shell lane** available (§2 P10) — a terminal that does not depend on `llm.ensem.dev` for its own LLM lane. The ensemble reaches all providers through this host; a mid-procedure failure of the primary takes the operator's own reasoning path down with it (runbook §4 / Risk R-20).
> 4. The **Phase-4 overlap decision** (§4.1) is made and recorded.
> 5. P7′ scheduled: the operator holds a live edge session during the overlap; low-traffic window; user notice sent (AR-52).
> 6. **Staged rollback operands present**: `/tmp/ensem-oauth2-rollback/` contains the pre-switch `llm.ensem.dev-httproute.yaml`, `deployment.yaml`, `configmap.yaml` (Issue 5 — staged-file existence is an unhold precondition).
>
> **Unhold procedure:** record all SIX as satisfied in the evidence block, then proceed to §4.2. Any single condition unsatisfied ⇒ remain held.

### 4.1 The Phase-4 overlap decision — the Emperor decides at unhold time

Two options. Both keep Phase 4 held until this choice is made explicitly.

| Option | What it is | Cost | Risk profile |
|---|---|---|---|
| **(a) Bounded fail-closed overlap window** — the council's design as-is | Steps A→B→C as specified. Between B (enforcement on) and C (edge leaves the protected paths) the edge gates `/`,`/ui`,`/fe/api` while the app enforces behind it. Users **without** a live edge session get a fail-closed ping-pong that terminates when the edge leaves the path (AR-52). | Minutes of degraded-but-closed UX for fresh logins during the overlap; requires the P7′ operator session + low-traffic window + user notice. | The ping-pong is fail-closed (never an exposure). The window is operator-paced and therefore **unbounded unless the operator enforces the schedule** — the shell must carry a hard timer. |
| **(b) Shadow-mode rehearsal add-on first** | Before any overlap, ship the demoted Option 1 from ADR-001 §Options: the app **mints** a session but does **not** enforce, so the whole OIDC flow is proven against the live IdP with zero user-visible risk. Only then run A→B→C. | Adds a Phase-1 mode (a third app mode beyond `dormant`/`enforce`) **and** its own trust source; more Phase-1 work and more Phase-1 review. | Proves the flow before any overlap. Removes the ping-pong risk entirely (no enforcement ⇒ no loop) at the cost of a new mode + trust surface — the very things ADR-001 §Decision was chosen to avoid. |

**Recommendation (council):** **(a)**, as designed — it satisfies I-1/I-2/I-3 with zero new trust surface and no new application mode. **(b)** is the answer if the Emperor judges the primary's ping-pong unacceptable in any form (recommendation §10-2).

**Either way, the irreversible step on ensem is ONE pre-staged, diffed `HTTPRoute` apply**, executed by an independent shell lane with the operator holding a live session (P7′) — recommendation §9-4.

### 4.2 A → B → C → D choreography

| Step | Action | `/`,`/ui`,`/fe/api` | `/oauth2` | App pods |
|---|---|---|---|---|
| **Stage 0** | **Pre-A staging (read-only):** copy the pre-switch `llm.ensem.dev-httproute.yaml`, `deployment.yaml`, `configmap.yaml` into `/tmp/ensem-oauth2-rollback/` **before** apply A. Staged-file existence is an unhold precondition (§4.1). | (unchanged) | (unchanged) | (unchanged) |
| **A** | `HTTPRoute`: repoint **`/oauth2` only** → `llm-supervisor-proxy:4321` (+ `timeouts: request: 60s` on that rule — AR-44) | edge-gated (app **dormant**; fresh logins **degraded-but-closed**) | → **app** | dormant |
| **B** | Apply `configmap.yaml` + `deployment.yaml` **enforcing** (+ Recreate patch); wait `rollout status` | **overlap**: edge + app-enforce — **loop-free** because the app's flow is already reachable (I-1) | app | **roll to enforcing** |
| **C** | **IMMEDIATELY** repoint `/`,`/ui`,`/fe/api` → app (ends the overlap loop **structurally**) | app-enforced | app | enforcing |
| **D** | Soak + full matrix; edge idled later (`kubectl scale` — the Service keeps 0 endpoints, harmless) | app-enforced | app | enforcing |

**Window C (the ensem lockout) — why the order is A→B→C.** The app's guard is **inside the binary** and runs on **every request that reaches the app**. The pre-ADR ordering (deploy enforcing, then repoint the route) left the edge owning `/oauth2` while the app demanded its own `llmproxy_oauth2` cookie: app 302 → edge `/oauth2` → oauth2-proxy → Casdoor → edge cookie only → app still 401 → 302 again ⇒ **infinite redirect loop**, operator-paced and therefore unbounded. **A moves `/oauth2` to the app first** (while the app is still dormant, so nothing enforces yet), so by the time B turns enforcement on the app's flow is already reachable ⇒ the loop is structurally impossible (I-1).

### 4.3 Pre-flight (read-only)

```bash
: "${KC_ENSEM:?environment block not loaded}"
export KC="$KC_ENSEM"
# G0 kubeconfig guard (ensem server must be https://192.168.1.250:6443)
GOT=$($KCTL --kubeconfig "$KC" config view --minify -o jsonpath='{.clusters[0].cluster.server}')
[ "$GOT" = "https://192.168.1.250:6443" ] || { echo "WRONG CLUSTER: $GOT — STOP"; exit 1; }

# 0. STAGE 0 — pre-A staging (Issue 5): copy the pre-switch manifests into the rollback dir
#    BEFORE any apply. Staged-file existence is an unhold precondition (§4.1).
mkdir -p /tmp/ensem-oauth2-rollback
D=/home/nea/Code/data-center-scripts/infranstructure/k8s/llmproxy/rancher-local-ensem-dev
for f in llm.ensem.dev-httproute.yaml deployment.yaml configmap.yaml; do
  cp "$D/$f" "/tmp/ensem-oauth2-rollback/$f"
done
ls -la /tmp/ensem-oauth2-rollback/   # MUST show all three files
[ -s /tmp/ensem-oauth2-rollback/llm.ensem.dev-httproute.yaml ] || { echo "STAGING FAIL"; exit 1; }
[ -s /tmp/ensem-oauth2-rollback/deployment.yaml ] || { echo "STAGING FAIL"; exit 1; }
[ -s /tmp/ensem-oauth2-rollback/configmap.yaml ] || { echo "STAGING FAIL"; exit 1; }

# 0b. P7′ — acquire and store the operator's LIVE EDGE SESSION while the edge still owns /oauth2.
#     After step A repoints /oauth2 -> app, the edge's own issuance path is unreachable.
mkdir -p /tmp/ensem-oauth2-rollback
curl -sS -c /tmp/ensem-oauth2-rollback/edge-session.jar -b /tmp/ensem-oauth2-rollback/edge-session.jar   https://llm.ensem.dev/oauth2/start?rd=%2Fui%2F >/dev/null
#    (complete the Casdoor login in the browser; the jar now holds _oauth2_proxy)
[ -s /tmp/ensem-oauth2-rollback/edge-session.jar ] || { echo "P7' acquisition FAIL"; exit 1; }

# 1. rollout history — capture the live revision + generation (AR-46)
$KCTL --kubeconfig "$KC" -n llmproxy rollout history deployment/llm-supervisor-proxy

# 2. secret key names (keys only)
$KCTL --kubeconfig "$KC" -n llmproxy get secret oauth2-proxy-secret -o jsonpath='{.data}' | jq 'keys'

# 3. Phase-1 tag verification in the manifest (the image is already the Phase-1 SHA)
grep -n "image: docker.io/disillusioners/llm-supervisor-proxy:" \
  /home/nea/Code/data-center-scripts/infranstructure/k8s/llmproxy/rancher-local-ensem-dev/deployment.yaml

# 4. pre-switch HTTPRoute backends (baseline for the diff)
grep -n "oauth2-proxy\|llm-supervisor-proxy" \
  /home/nea/Code/data-center-scripts/infranstructure/k8s/llmproxy/rancher-local-ensem-dev/llm.ensem.dev-httproute.yaml

# 5. the AR-17 static validator over the two staged manifests (§2.2 ensem variant)

# 6. snapshot the pre-switch /v1/models id-set (G5 baseline) — separate DB per host, compare only against its own baseline
curl -s https://llm.ensem.dev/v1/models | jq -r '.data[].id' | sort > /tmp/models-ensem.pre.txt
```

### 4.4 A — `HTTPRoute`: repoint `/oauth2` **only** → app (exact diff)

#### 4.4.1 Exact unified diff — `data-center-scripts/infranstructure/k8s/llmproxy/rancher-local-ensem-dev/llm.ensem.dev-httproute.yaml`

```diff
--- a/infranstructure/k8s/llmproxy/rancher-local-ensem-dev/llm.ensem.dev-httproute.yaml
+++ b/infranstructure/k8s/llmproxy/rancher-local-ensem-dev/llm.ensem.dev-httproute.yaml
@@ -17,13 +17,15 @@ spec:
   rules:
     # oauth2-proxy auth endpoints
     - matches:
         - path:
             type: PathPrefix
             value: /oauth2
       backendRefs:
-        - name: oauth2-proxy
-          port: 4180
+        - name: llm-supervisor-proxy
+          port: 4321
+      timeouts:
+        request: 60s
     # Public LLM API (API-key auth in-app) — extended timeout for streaming
     - matches:
         - path:
             type: PathPrefix
             value: /v1
@@ -37,30 +39,30 @@ spec:
     # Protected UI (root)
     - matches:
         - path:
             type: Exact
             value: /
       backendRefs:
-        - name: oauth2-proxy
-          port: 4180
+        - name: oauth2-proxy
+          port: 4180
       timeouts:
         request: 30m
     # Protected UI subpaths
     - matches:
         - path:
             type: PathPrefix
             value: /ui
       backendRefs:
-        - name: oauth2-proxy
-          port: 4180
+        - name: oauth2-proxy
+          port: 4180
       timeouts:
         request: 30m
     # Protected frontend API
     - matches:
         - path:
             type: PathPrefix
             value: /fe/api
       backendRefs:
-        - name: oauth2-proxy
-          port: 4180
+        - name: oauth2-proxy
+          port: 4180
       timeouts:
         request: 30m
 ---
```

**How to read the diff above:** it shows the **whole file's** backend map so that the *unchanged* rules are unambiguous. **Only the `/oauth2` rule changes in step A** — the `/`,`/ui`,`/fe/api` rules stay on `oauth2-proxy:4180` in step A (they are repointed in step C, §4.6). The three `- name: oauth2-proxy / + name: oauth2-proxy` pairs are shown **unchanged** to make that explicit; they are **not** edits. The only real edits in A are:

```diff
       backendRefs:
-        - name: oauth2-proxy
-          port: 4180
+        - name: llm-supervisor-proxy
+          port: 4321
+      timeouts:
+        request: 60s
```
applied **once**, to the `/oauth2` rule only.

**AR-44 — the 60 s timeout.** The Gateway API default request timeout is **15 s**. The app's `/oauth2/start` performs **lazy OIDC discovery** (≤ `OAUTH2_DISCOVERY_TIMEOUT` = 5 s) and `/oauth2/callback` performs a **token exchange** (≤ `OAUTH2_TOKEN_EXCHANGE_TIMEOUT` = 10 s) plus JWKS fetch — a cold path can exceed 15 s ⇒ **504 on first login**. The `/oauth2` rule therefore carries `timeouts: request: 60s`. (The `/v1` rule already carries 30 m for streaming.)

**AR-45 — apply acceptance check + failure action.** After each `HTTPRoute` apply, assert the Gateway accepted it:
```bash
$KCTL --kubeconfig "$KC" -n llmproxy get httproute llm-ensem-dev-route \
  -o jsonpath='{range .status.parents[*]}{.conditions[?(@.type=="Accepted")].status}{" "}{.conditions[?(@.type=="ResolvedRefs")].status}{"\n"}{end}'
# expect: True True
```
- **`/v1` is a SEPARATE rule** in the same `HTTPRoute` ⇒ a failed/partial apply of the other rules never touches the ensemble's LLM lane.
- **Failure action:** if `Accepted` is not `True`, **revert the `HTTPRoute`** (apply the previous version immediately) and stop — do not continue to B.

#### 4.4.2 Apply A + verify

```bash
export HTTPR=/home/nea/Code/data-center-scripts/infranstructure/k8s/llmproxy/rancher-local-ensem-dev/llm.ensem.dev-httproute.yaml
$KCTL --kubeconfig "$KC" -n llmproxy diff -f "$HTTPR" || true      # judge the printed diff, not the exit code
$KCTL --kubeconfig "$KC" -n llmproxy apply --dry-run=server -f "$HTTPR"
$KCTL --kubeconfig "$KC" -n llmproxy apply -f "$HTTPR"
# AR-45 acceptance check + per-path probes (the /oauth2 rule only):
#   anon /oauth2/start  -> 404 while the app is still DORMANT (P-L: the dormant /oauth2/start is 404 by design)
#   /v1/models          -> 200 (the /v1 rule is untouched)
#   /                   -> 302 (still edge-gated)
```

### 4.5 B — apply the enforcing deployment + configmap (exact diffs)

#### 4.5.1 Exact unified diff — `configmap.yaml`

Reconciled to phase1 §4.2 (the single source of truth). The drifted names from the pre-ADR draft are **gone**; the missing ones are **present**.

```diff
--- a/infranstructure/k8s/llmproxy/rancher-local-ensem-dev/configmap.yaml
+++ b/infranstructure/k8s/llmproxy/rancher-local-ensem-dev/configmap.yaml
@@ -9,4 +9,13 @@ metadata:
     app.kubernetes.io/instance: llm-supervisor-proxy
 data:
   LOG_LEVEL: info
   MCP_ENABLED: "true"
   SSE_HEARTBEAT_ENABLED: "true"
+  OAUTH2_ENABLED: "true"
+  OAUTH2_ISSUER_URL: "https://door.ensem.dev"
+  OAUTH2_EXTERNAL_URL: "https://llm.ensem.dev"
+  OAUTH2_EMAIL_DOMAINS: "*"
+  OAUTH2_ALLOW_ANY_EMAIL_DOMAIN: "true"
+  OAUTH2_REQUIRE_EMAIL_VERIFIED: "true"
+  OAUTH2_ALLOWED_ALGS: "RS256"
+  OAUTH2_SESSION_TTL: "24h"
+  OAUTH2_COOKIE_SECURE: "true"
```

Notes:
- **ensem posture**: `OAUTH2_EMAIL_DOMAINS: "*"` **+** `OAUTH2_ALLOW_ANY_EMAIL_DOMAIN: "true"` (AR-04 — `*` requires the explicit opt-in; the pair logs a startup WARN) and `OAUTH2_REQUIRE_EMAIL_VERIFIED: "true"`. The `*` posture is held on ensem **until a soak proves no operator account is excluded** (§1.7); tightening later is an explicit env change.
- `OAUTH2_SESSION_TTL: "24h"` — parity with today's `--cookie-expire=24h`; re-login cadence unchanged (§1.5).
- `OAUTH2_COOKIE_SECURE: "true"` — the phase1 default is already `true` (AR-03); stated explicitly here so the cutover value is visible in GitOps.
- **Drifted names removed (AR-17):** the pre-ADR draft of this plan carried four `OAUTH2_*` names that do **not** exist in the phase1 §4.2 table (listed in the AR-17 row of Appendix A). A silent no-op email-domain filter was the failure mode; the §2.2 membership check now fails the cutover **closed** on any name outside the phase1 table.

#### 4.5.2 Exact unified diff — `deployment.yaml`

**The three secret keys map by name** (`OAUTH2_CLIENT_ID` ← `CLIENT_ID`, etc.) — the binary reads `OAUTH2_*` names, so the mapping must be exact (AR-17: only `COOKIE_SECRET` had a fallback).

```diff
--- a/infranstructure/k8s/llmproxy/rancher-local-ensem-dev/deployment.yaml
+++ b/infranstructure/k8s/llmproxy/rancher-local-ensem-dev/deployment.yaml
@@ -11,10 +11,6 @@ spec:
   strategy:
-    type: RollingUpdate
-    rollingUpdate:
-      maxUnavailable: 0
-      maxSurge: 1
+    type: Recreate
+    rollingUpdate: null
@@ -28,7 +24,7 @@ spec:
       labels:
         app.kubernetes.io/name: llm-supervisor-proxy
         app.kubernetes.io/instance: llm-supervisor-proxy
       annotations:
-        checksum/config: e9f348e70f068c396cc6df5032f919ee33400b516c327e793af333839d384f2c
+        checksum/config: <RECOMPUTE after applying the configmap — see §4.5.3>
     spec:
       containers:
         - name: llm-supervisor-proxy
@@ -230,12 +230,54 @@ spec:
             - name: SSE_HEARTBEAT_ENABLED
               valueFrom:
                 configMapKeyRef:
                   name: llm-supervisor-proxy
                   key: SSE_HEARTBEAT_ENABLED
+            - name: OAUTH2_ENABLED
+              valueFrom:
+                configMapKeyRef:
+                  name: llm-supervisor-proxy
+                  key: OAUTH2_ENABLED
+            - name: OAUTH2_ISSUER_URL
+              valueFrom:
+                configMapKeyRef:
+                  name: llm-supervisor-proxy
+                  key: OAUTH2_ISSUER_URL
+            - name: OAUTH2_EXTERNAL_URL
+              valueFrom:
+                configMapKeyRef:
+                  name: llm-supervisor-proxy
+                  key: OAUTH2_EXTERNAL_URL
+            - name: OAUTH2_EMAIL_DOMAINS
+              valueFrom:
+                configMapKeyRef:
+                  name: llm-supervisor-proxy
+                  key: OAUTH2_EMAIL_DOMAINS
+            - name: OAUTH2_ALLOW_ANY_EMAIL_DOMAIN
+              valueFrom:
+                configMapKeyRef:
+                  name: llm-supervisor-proxy
+                  key: OAUTH2_ALLOW_ANY_EMAIL_DOMAIN
+            - name: OAUTH2_REQUIRE_EMAIL_VERIFIED
+              valueFrom:
+                configMapKeyRef:
+                  name: llm-supervisor-proxy
+                  key: OAUTH2_REQUIRE_EMAIL_VERIFIED
+            - name: OAUTH2_ALLOWED_ALGS
+              valueFrom:
+                configMapKeyRef:
+                  name: llm-supervisor-proxy
+                  key: OAUTH2_ALLOWED_ALGS
+            - name: OAUTH2_SESSION_TTL
+              valueFrom:
+                configMapKeyRef:
+                  name: llm-supervisor-proxy
+                  key: OAUTH2_SESSION_TTL
+            - name: OAUTH2_COOKIE_SECURE
+              valueFrom:
+                configMapKeyRef:
+                  name: llm-supervisor-proxy
+                  key: OAUTH2_COOKIE_SECURE
+            - name: OAUTH2_CLIENT_ID
+              valueFrom:
+                secretKeyRef:
+                  name: oauth2-proxy-secret
+                  key: CLIENT_ID
+            - name: OAUTH2_CLIENT_SECRET
+              valueFrom:
+                secretKeyRef:
+                  name: oauth2-proxy-secret
+                  key: CLIENT_SECRET
+            - name: OAUTH2_COOKIE_SECRET
+              valueFrom:
+                secretKeyRef:
+                  name: oauth2-proxy-secret
+                  key: COOKIE_SECRET
           resources:
             requests:
               cpu: 100m
               memory: 128Mi
             limits:
```

Notes:
- **Exact keyRef mapping (AR-17):** `OAUTH2_CLIENT_ID` ← `oauth2-proxy-secret/CLIENT_ID`; `OAUTH2_CLIENT_SECRET` ← `oauth2-proxy-secret/CLIENT_SECRET`; `OAUTH2_COOKIE_SECRET` ← `oauth2-proxy-secret/COOKIE_SECRET` (phase1 §4.2 fallback chain: `OAUTH2_COOKIE_SECRET` → `COOKIE_SECRET`; here the dedicated name is used, so the fallback is not relied on).
- The `checksum/config` annotation must be **recomputed** so the pod restarts when the configmap changes (house convention). Recompute in §4.5.3 and paste the new value.
- **BLK-1 — `spec.strategy` ownership.** The manifest is **authoritative**: `kubectl apply -f $DEPLOY` re-sets `spec.strategy` to the manifest's value (`Recreate`, `rollingUpdate: null`), so the live object cannot drift. The `§4.5.3` step-2 re-check (below) is the **enforcement gate**: it re-asserts `spec.strategy.type == Recreate` **after** the apply and **before** `rollout status`, aborting if a controller or a stale patch reverted it. The idle step (§4.7) reverts `spec.strategy` back to `RollingUpdate` (maxSurge 1 / maxUnavailable 0).
- The app Deployment's **image tag is not touched** (Phase 1 already repinned it).

#### 4.5.3 Apply B — configmap + deployment, Recreate patch, wait rollout

```bash
export CONFIGMAP=/home/nea/Code/data-center-scripts/infranstructure/k8s/llmproxy/rancher-local-ensem-dev/configmap.yaml
export DEPLOY=/home/nea/Code/data-center-scripts/infranstructure/k8s/llmproxy/rancher-local-ensem-dev/deployment.yaml

# 0. AR-17 static validator over both staged manifests (§2.2 ensem variant) — BEFORE any apply

# 1. AR-53 — Recreate / maxSurge 0 (per-pod state store; no two-pod straddle)
$KCTL --kubeconfig "$KC" -n llmproxy patch deployment llm-supervisor-proxy --type=strategic \
  -p '{"spec":{"strategy":{"type":"Recreate","rollingUpdate":null}}}'

# 2. apply the configmap FIRST (it is the env source), then recompute checksum/config, then the deployment
$KCTL --kubeconfig "$KC" -n llmproxy apply -f "$CONFIGMAP"
NEW_SUM=$($KCTL --kubeconfig "$KC" -n llmproxy get configmap llm-supervisor-proxy -o yaml \
          | grep -v '^\s*\(creationTimestamp\|resourceVersion\|uid\|managedFields\|annotations\|labels\)' | sha256sum | cut -d' ' -f1)
#   paste NEW_SUM into deployment.yaml's checksum/config annotation, then:
$KCTL --kubeconfig "$KC" -n llmproxy apply -f "$DEPLOY"

# BLK-1 enforcement gate: re-assert spec.strategy.type == Recreate AFTER the apply and BEFORE
# rollout status. The manifest is authoritative (kubectl apply re-sets the field), but a stale
# controller patch or a server-side default can still revert it — if it is not Recreate, abort.
STRATEGY=$($KCTL --kubeconfig "$KC" -n llmproxy get deployment llm-supervisor-proxy \
            -o jsonpath='{.spec.strategy.type}')
[ "$STRATEGY" = "Recreate" ] || { echo "ABORT: spec.strategy.type=$STRATEGY (expected Recreate)"; exit 1; }
echo "OK spec.strategy.type=Recreate"

# 3. wait for the rollout (AR-53 — logins only AFTER rollout status completes)
$KCTL --kubeconfig "$KC" -n llmproxy rollout status deployment/llm-supervisor-proxy --timeout=300s

# 4. prove enforcement is live (P-I) — env names present, restartCount 0, 1/1 Running
$KCTL --kubeconfig "$KC" -n llmproxy get pods -l app.kubernetes.io/name=llm-supervisor-proxy \
  -o json | jq -r '.items[] | "\(.metadata.name) ready=\(.status.containerStatuses[0].ready) restarts=\(.status.containerStatuses[0].restartCount) imageID=\(.status.containerStatuses[0].imageID)"'
$KCTL --kubeconfig "$KC" -n llmproxy exec deploy/llm-supervisor-proxy -- env | grep -E '^OAUTH2_' | sed 's/=.*$/=<set>/'
#   expect OAUTH2_ENABLED=true, OAUTH2_ISSUER_URL, OAUTH2_EXTERNAL_URL, OAUTH2_CLIENT_ID, OAUTH2_CLIENT_SECRET, OAUTH2_COOKIE_SECRET, OAUTH2_SESSION_TTL, OAUTH2_EMAIL_DOMAINS ...
#   NOTE: values are NOT echoed (secrets) — names only.

# 5. OVERLAP probes (matrix, overlap column) + P-E rehearsal under P7′ BEFORE step C
```

> **I-3 warning.** The configmap and the deployment are applied **as one unit** — never the configmap alone with `OAUTH2_ENABLED: "true"` and a deployment that still lacks the `secretKeyRef` entries. That combination is the ensem equivalent of Window B (startup fail-fast ⇒ CrashLoopBackOff ⇒ **the ensemble's own LLM lane dies**).

### 4.6 C — repoint `/`,`/ui`,`/fe/api` → app (exact diff)

#### 4.6.1 Exact unified diff — `llm.ensem.dev-httproute.yaml`

```diff
--- a/infranstructure/k8s/llmproxy/rancher-local-ensem-dev/llm.ensem.dev-httproute.yaml
+++ b/infranstructure/k8s/llmproxy/rancher-local-ensem-dev/llm.ensem.dev-httproute.yaml
@@ -37,30 +37,30 @@ spec:
     # Protected UI (root)
     - matches:
         - path:
             type: Exact
             value: /
       backendRefs:
-        - name: oauth2-proxy
-          port: 4180
+        - name: llm-supervisor-proxy
+          port: 4321
       timeouts:
         request: 30m
     # Protected UI subpaths
     - matches:
         - path:
             type: PathPrefix
             value: /ui
       backendRefs:
-        - name: oauth2-proxy
-          port: 4180
+        - name: llm-supervisor-proxy
+          port: 4321
       timeouts:
         request: 30m
     # Protected frontend API
     - matches:
         - path:
             type: PathPrefix
             value: /fe/api
       backendRefs:
-        - name: oauth2-proxy
-          port: 4180
+        - name: llm-supervisor-proxy
+          port: 4321
       timeouts:
         request: 30m
 ---
```

#### 4.6.2 Apply C + acceptance + per-path probes

```bash
$KCTL --kubeconfig "$KC" -n llmproxy diff -f "$HTTPR" || true
$KCTL --kubeconfig "$KC" -n llmproxy apply -f "$HTTPR"
# AR-45 acceptance check (Accepted/ResolvedRefs == True)
# per-path probes (matrix, internal-only column): P-A 401 JSON, P-C 302 same-origin, P-D 200 with app cookie, P-G 401 ...
```

**Why C must be IMMEDIATE after B:** C is what ends the overlap **structurally**. Between B and C the edge still gates the protected paths, so a user without a live edge session is in the fail-closed ping-pong (AR-52). The B→C gap is therefore the **overlap window** — minutes, operator-paced, and it must be a hard-timer window (low-traffic slot + user notice + P7′).

### 4.7 D — soak + matrix; edge idled later

- Run the **full** matrix (§6) in its **internal-only** column + the house gate G0–G10 + the ensem checklist §4.8.
- Soak **10 minutes** (G9): zero restarts, identical pod, `/v1/models` id-set unchanged vs the ensem baseline.
- **Edge idle (AR-21 / OQ-7):** **after** the soak, scale the edge to zero — **do not delete**:
  ```bash
  $KCTL --kubeconfig "$KC" -n llmproxy scale deployment/oauth2-proxy --replicas=0
  ```
  The Service **keeps 0 endpoints** (harmless), and the Deployment+Service **still exist** ⇒ the rollback anchor survives (P-J). Deletion is deferred to a **single cleanup PR for both hosts after Phase 4** (decision D4, §6.1).
- **Reconciliation of the edge `replicas` value (note 7).** The committed ensem manifest `rancher-local-ensem-dev/oauth2-proxy/deployment.yaml` ships the edge at **`replicas: 1`** — that is the **authoritative ship-time value**. §4.7's `scale --replicas=0` is the **runtime idle operation performed after the soak**; it is NOT a manifest edit. The manifest stays at `replicas: 1` so that a rollback (§4.9) re-creates a serving edge pod; the live object is scaled to 0 only after the soak proves the app stands alone. Do not "fix" the manifest to `replicas: 0` — that would prune the rollback anchor before the soak.
- Revert the Recreate patch (house precedent — a `spec.strategy`-only change causes no rollout):
  ```bash
  $KCTL --kubeconfig "$KC" -n llmproxy patch deployment llm-supervisor-proxy --type=strategic \
    -p '{"spec":{"strategy":{"type":"RollingUpdate","rollingUpdate":{"maxSurge":1,"maxUnavailable":0}}}}'
  ```

### 4.8 ensem post-switch verification checklist (E-rows)

| # | Check | Expectation | ALARM |
|---|---|---|---|
| **E1** | `OAUTH2_ENABLED` in pod env | `true` | absent = enforcement never turned on |
| **E2** | Secret env mounted | `OAUTH2_CLIENT_ID` / `OAUTH2_CLIENT_SECRET` / `OAUTH2_COOKIE_SECRET` present (names only) | missing = Window B risk |
| **E3** | Casdoor login round-trip (browser) | `https://llm.ensem.dev/` → `door.ensem.dev` → callback → `/ui/` 200, `Set-Cookie: llmproxy_oauth2`; **forced re-login** of legacy `_oauth2_proxy` sessions is expected (OQ-5) | no cookie minted = flow broken |
| **E4** | `/fe/api/models` post-login | 200 + model list | 302 = session not honoured |
| **E5** | `/v1/*` unaffected | id-set unchanged; smoke 200 | any change = `/v1` affected (P-H) |
| **E6** | `/healthz` in-cluster (port-forward 4322:4321) | `ok` | — |
| **E7** | SSE `/fe/api/events` | events arrive | 502/504 = broken |
| **E8** | logout | session cleared; `/` re-triggers login | — |
| **E9** | **operator-surface gate matrix** (AR-50) — the full path × expected-status enumeration is the ensem gate; the operator gate script named in the pre-ADR draft **does not exist on disk** and is not referenced | every operator path probed, pre/post expectations recorded (T3.4 artifact) | — |
| **E10** | soak 10 min (G9) | 0 restarts, same pod, id-set unchanged | — |
| **E11** | **no-exposure assertion (AR-14 / ruling R-2)** — anon `/fe/api/models` (no cookie, no token) | **401 JSON `{"error":"unauthenticated"}`** | **302 cannot distinguish working from looping — it is NOT a pass criterion; 200 = EXPOSED** |
| **E11b** | **loop detector (AR-14)** — follow redirects on `/ui` with a cookie jar, max 5 hops | terminates after one app bounce | **≥3 same-origin 302s = LOOP (Window C live)** |
| **E12** | **short-circuit probe (AR-54 / P-K)** — `/oauth2/start` **with a valid app cookie** | **302 `rd`, zero IdP hop** | an IdP hop = short-circuit missing |
| **E13** | `HTTPRoute` acceptance (AR-45) | `Accepted=True`, `ResolvedRefs=True` | not True ⇒ revert the HTTPRoute |
| **E14** | edge idled (AR-21) | `deploy/oauth2-proxy` exists at `replicas: 0`; Service still present | **missing = pruned rollback anchor** |
| **E15** | **`/fe/api/version` gate-EXEMPT (AR-13 / R-1)** | pod-direct `curl -s 127.0.0.1:4322/fe/api/version` → **200 `{"version":"…"}`** | 401 = exemption broken; house gate **G4** is pod-direct (phase1 §6.2 class **4b**, `TestMiddleware_VersionIsExempt`) |

### 4.9 One scripted rollback unit (AR-19)

**R1 and R2 are a single scripted unit** — they are executed together, in order, from one script. The reason is the exposure asymmetry:

> **Reverted `HTTPRoute` + rolled-back `deployment` = unauthenticated exposure.** If the `HTTPRoute` still points `/`,`/ui`,`/fe/api` at the app while the app has been rolled back to **dormant**, the operator plane is served with **no gate at all**. That is why the order is **HTTPRoute first**.

| Sub-step | Action | State after | Exposure? |
|---|---|---|---|
| **R1** | Revert the `HTTPRoute` (`/`,`/ui`,`/fe/api` → `oauth2-proxy:4180`); **keep** `/oauth2` → app (I-1) | edge gates the protected paths; the app still enforces behind it — the **safe overlap state** | no |
| **R2** | Revert `deployment.yaml` + `configmap.yaml` to the pre-switch state | app dormant; edge in path; `/oauth2` → app ⇒ dormant `/oauth2/start` is **404** (P-L by design) | no |
| **R3** | Revert the `/oauth2` rule → `oauth2-proxy:4180` | the **original** topology, exactly as before the cutover | no |
| **R4** | Revert the Recreate patch; re-derive the rollback anchors from `rollout history` (numbers go stale) | — | no |

```bash
#!/usr/bin/env bash
# ONE UNIT — do not run the sub-steps separately. set -euo pipefail aborts on any failure.
set -euo pipefail
: "${KC_ENSEM:?environment block not loaded}"
export KC="$KC_ENSEM"
D=/home/nea/Code/data-center-scripts/infranstructure/k8s/llmproxy/rancher-local-ensem-dev
STAGE=/tmp/ensem-oauth2-rollback
# Stage 0 must have populated $STAGE (Issue 5 — staged-file existence is an unhold precondition).
[ -s "$STAGE/llm.ensem.dev-httproute.yaml" ] || { echo "STAGE MISSING: httproute"; exit 1; }
[ -s "$STAGE/deployment.yaml" ] || { echo "STAGE MISSING: deployment"; exit 1; }
[ -s "$STAGE/configmap.yaml" ] || { echo "STAGE MISSING: configmap"; exit 1; }

# R1 — revert the protected paths (/ , /ui, /fe/api) to the edge, KEEPING /oauth2 on the app.
#     Resulting state = the SAFE overlap state (edge gates the protected paths, the app stays
#     enforcing, /oauth2 -> app). Table row R1 = "revert the HTTPRoute (seconds; the app may remain
#     enforcing — that is the safe overlap state)". This is NOT the staged pre-switch file (that
#     file carries /oauth2 -> oauth2-proxy:4180); R1 builds the overlap-preserving route inline.
cat > "$STAGE/httproute.r1.yaml" <<'YAML'
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: llm-ensem-dev-route
  namespace: llmproxy
spec:
  parentRefs:
    - name: envoy-gateway
      namespace: gateway
      sectionName: https-gateway-ensem-dev
  hostnames:
    - "llm.ensem.dev"
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /oauth2
      backendRefs:
        - name: llm-supervisor-proxy
          port: 4321
      timeouts:
        request: 60s
    - matches:
        - path:
            type: PathPrefix
            value: /v1
      backendRefs:
        - name: llm-supervisor-proxy
          port: 4321
      timeouts:
        request: 30m
    - matches:
        - path:
            type: Exact
            value: /
      backendRefs:
        - name: oauth2-proxy
          port: 4180
      timeouts:
        request: 30m
    - matches:
        - path:
            type: PathPrefix
            value: /ui
      backendRefs:
        - name: oauth2-proxy
          port: 4180
      timeouts:
        request: 30m
    - matches:
        - path:
            type: PathPrefix
            value: /fe/api
      backendRefs:
        - name: oauth2-proxy
          port: 4180
      timeouts:
        request: 30m
YAML
$KCTL --kubeconfig "$KC" -n llmproxy apply -f "$STAGE/httproute.r1.yaml"

# R2 — revert the app to dormant (deployment + configmap as ONE unit) from the STAGED files
$KCTL --kubeconfig "$KC" -n llmproxy apply -f "$STAGE/configmap.yaml"
$KCTL --kubeconfig "$KC" -n llmproxy apply -f "$STAGE/deployment.yaml"
$KCTL --kubeconfig "$KC" -n llmproxy rollout status deployment/llm-supervisor-proxy --timeout=300s

# R3 — restore /oauth2 to the edge (original topology): apply the STAGED pre-switch file verbatim
$KCTL --kubeconfig "$KC" -n llmproxy apply -f "$STAGE/llm.ensem.dev-httproute.yaml"

# R4 — revert the Recreate patch (house precedent — a spec.strategy-only change, no rollout)
$KCTL --kubeconfig "$KC" -n llmproxy patch deployment llm-supervisor-proxy --type=strategic \
  -p '{"spec":{"strategy":{"type":"RollingUpdate","rollingUpdate":{"maxSurge":1,"maxUnavailable":0}}}}'
```

**Post-rollback verification — a session-bearing round-trip** (replaces the old E11 re-check):
1. The operator logs in through the **edge** flow (Casdoor → `_oauth2_proxy` cookie) — the edge is back in path.
2. `GET /fe/api/models` with that cookie ⇒ **200** + the model list (the edge forwards to the app; the app is dormant again ⇒ no gate conflict).
3. `GET /v1/models` ⇒ **200**, id-set unchanged.
4. `kubectl get deploy,svc oauth2-proxy` ⇒ both **present** (the rollback anchor is intact).

> **Correction to the pre-ADR draft.** The earlier text claimed that an edge proxy sitting in front of the app makes the app's own internal handler inert. **That rationale was false and is deleted**: the app's guard is **inside the binary** and runs on **every request that reaches the app** — behind `oauth2-proxy` the app still demands its own `llmproxy_oauth2` cookie, which is exactly how the Window C loop was created. The correct statement is the one above: R1 (edge back in front of the protected paths) is safe **because the app's flow stays reachable**, and R1+R2 are bound into one unit because a reverted route with a still-enforcing app is a loop while a rolled-back deployment with a route still pointing at the app is an **exposure**.

**v203 boundary (unchanged).** R1/R2/R3/R4 never cross migration 034. Rolling back **past** v203 (`0082773`/v202) is **FORBIDDEN** without reversing 034 first.

---

## 5. Master Verification Checklist & GitOps Pin Commit Plan

### 5.1 Master checklist (both hosts, post-switch)

- [ ] **M1** — Both hosts report the Phase-1 image digest on **running** pods (pipeline-02 byte-exact). No `:latest`/`:master` tags anywhere.
- [ ] **M2** — **daoduc (Phase 3)**: D0→D4 complete; full matrix green in the internal-only column; S1–S12 green; 10-min soak clean; P-J green (edge objects at `replicas: 0`).
- [ ] **M3** — **ensem (Phase 4)**: A→D complete; full matrix green in the internal-only column; E1–E15 green; 10-min soak clean; edge scaled to 0 (Deployment+Service still present).
- [ ] **M4** — **Edge rollback anchor intact on BOTH hosts** (AR-21 — redefined): the edge **Deployment and Service objects still exist** at their intended replica count (daoduc: rendered by the chart at `replicas: 0`; ensem: `kubectl scale` to 0, Service retains 0 endpoints). **Missing = Helm pruned the anchor ⇒ immediate rollback.** (The old pod-log inspection is dropped — there is no serving pod to read.)
- [ ] **M5** — `oauth2-proxy-secret` still present and readable by the proxy (keys only).
- [ ] **M6** — `/v1/models` id-set unchanged vs each host's **own** pre-switch baseline (the hosts have separate DBs; never compare across hosts).
- [ ] **M7** — The v203/migration-034 boundary respected: no rollback past v203 attempted without reversing 034 first.
- [ ] **M8** — `GET /fe/api/version` remains **gate-exempt** (AR-13 / R-1) and the house gate **G4** passed pod-direct on both hosts (phase1 §6.2 class **4b**; `TestMiddleware_VersionIsExempt`).
- [ ] **M9** — The forced re-login was communicated (OQ-5) and no operator reported an unexpected lockout (the overlap ping-pong terminates once the edge left the path).

### 5.2 GitOps pin commit plan — AR-47

**Pin order: daoduc first, then the primary. One commit per host. `main` untouched. Pins ride along with the deploy** (standing grant, 2026-11-17 — pins no longer need a separate authorization ritual).

| Order | Host | Repo / branch | Files in the commit |
|---|---|---|---|
| 1 | **daoduc** (backup) | `data-center-scripts`, branch `feature/llmproxy-pin-<phase1-sha>` | `infranstructure/k8s/llmproxy/hetzner-daoduc/values-pinned.yaml` (chart toggles + the repin comment block) |
| 2 | **ensem** (primary) | `data-center-scripts`, branch `feature/llmproxy-pin-<phase1-sha>-ensem` | `infranstructure/k8s/llmproxy/rancher-local-ensem-dev/{configmap.yaml,deployment.yaml,llm.ensem.dev-httproute.yaml}` |

- The **daoduc** commit lands and is applied **before** the ensem commit exists — the backup host proves the switch first.
- `main`/`master` of `data-center-scripts` is **not touched** by these pins. The proxy repo's `master` is the release line; the dc-scripts pins ride the `latest` flow only (blueprint: Git Branch Topology).
- After any rollback, the pin **must** be reverted in the same order (runbook §5.5) — otherwise the next apply re-ships the bad state.

### 5.3 Evidence block template

```
Phase: [1-dormant | 3-daoduc-switch | 4-ensem-switch]
Date (UTC): 
Operator: 
Host(s): 
Chart checkout path + merged Phase-1 commit SHA (AR-20): 
Chart `git status --short -- k8s/` (must be empty): 
LIVE_REVISION (daoduc, helm) / LIVE_REVISION (ensem, rollout): 
D1_REVISION (daoduc, helm — overlap revision; required for the D3 rollback tier): 
V203_REVISION (daoduc, helm): 
Rendered-env validator (AR-17) result: 
IdP email_verified verification result + posture chosen (leader ruling 5): 
Overlap decision made at unhold: [(a) bounded overlap | (b) shadow-mode rehearsal]
P7' held / low-traffic window / user notice sent: 
Probe matrix result per state (edge-only / overlap / internal-only): 
Soak duration + result: 
Edge anchor state (M4): 
Rollback performed: [none | daoduc R-after-D1 | daoduc R-after-D3 | ensem R1+R2 unit]
Notes: 
```

---

## 6. Probe Matrix P-A…P-L — the verification backbone

Every step of both hosts runs the relevant columns of this matrix. **The ALARM column is the pass/fail criterion** — a probe that returns a different status than the column says is a failure with a named signature.

| ID | Probe | dormant + edge | overlap | internal-only | ALARM |
|---|---|---|---|---|---|
| **P-A** | anon `/fe/api/models` | 302 (edge) | 302 (edge) | **401 JSON** `{"error":"unauthenticated"}`, no `Location` | **200 = EXPOSED** |
| **P-B** | follow redirects on `/ui` (jar, max 5) | exits to edge/IdP | terminates after one app bounce | `…/oauth2/start` → external Casdoor hop → 200 + `Set-Cookie: llmproxy_oauth2` | **≥3 same-origin 302s = LOOP** |
| **P-C** | anon `/` | 302 → edge signin | 302 → edge | 302 → `/oauth2/start?rd=` (same-origin, `no-store`) | 200 = exposed |
| **P-D** | `/ui/` + valid app cookie | 302 | 200 | 200 | **conditional (BLK-3):** a 302 is a failure **only** once the P-E(ii) browser rehearsal has minted `llmproxy_oauth2`; before that rehearsal, 302 is expected |
| **P-E(i)** | scripted probe — anon `/fe/api/models` (no cookie, no token) | 401 JSON | 401 JSON | 401 JSON | **200 = EXPOSED; 302 = the edge is still gating (D3 not effective)** |
| **P-E(ii)** | browser rehearsal — operator holds a live edge session (P7′) and runs the app's own flow | 200 (edge) | 302 → app flow → 200 + `Set-Cookie: llmproxy_oauth2` (rehearsal) | 401/302 | failure to mint = flow broken; a 302 here = LOOP (Window C live) |
| **P-F** | forged `X-Auth-Request-*` / forged cookie, direct-to-Service | 401 | 401 | 401 | **200 = header-trust bypass** |
| **P-G** | anon `/fe/api/credentials` `/tokens` `/config` | 401 | 401 | 401 | **200 = exposed admin plane** |
| **P-H** | `/v1/models` 200; `/healthz` 200; `/v1/chat/completions` without token ⇒ proxy 401 | ✓ | ✓ | ✓ | any change = `/v1` affected |
| **P-I** | pods 1/1 Running, restartCount 0, env names present | ✓ | ✓ | ✓ | **CrashLoopBackOff = Window B live** |
| **P-J** | `kubectl get deploy oauth2-proxy` exists, replicas as intended | ✓ | ✓ (≥1) | ✓ (0) | **missing = Helm pruned the rollback anchor** |
| **P-K** | `/oauth2/start` with a valid app cookie | n/a | **302 `rd`, zero IdP hop** | 302 `rd` | IdP hop = short-circuit missing |
| **P-L** | dormant `/oauth2/start` | 404 (pinned) | — | — | unpinned = doc gap |

**Which columns run when:**

| Step | Columns |
|---|---|
| D0 (daoduc, pre-flight) | P-H, P-I, P-J, P-L (baseline capture) |
| D1 → D2 (overlap) | full matrix, **overlap column** + P-E rehearsal |
| D3 → D4 (internal-only) | full matrix, **internal-only column** |
| ensem A | P-L (dormant 404 — the app is still dormant), P-H |
| ensem B (overlap) | full matrix, **overlap column** + P-E rehearsal under P7′ |
| ensem C → D | full matrix, **internal-only column** |

---

## 7. Decisions & Residual Verifications

### 7.1 Decisions (closed)

| # | Item | Decision | Reference |
|---|---|---|---|
| **D1** | Interlock mechanism | **ADR-001 Approach 5** — per-path `/oauth2`-first + three-state chart. Rewritten around it; the pre-ADR draft is superseded. | ADR-001; recommendation §3.2/§3.3 |
| **D2** | Chart secret wiring on daoduc | **Phase 1 (dormant), T15 scope** — Phase 3 is a pure toggle. If Phase 1 verification shows the wiring missing, **STOP**; do not improvise env wiring during the switch. | OQ-4; phase1 §8.3 |
| **D3** | Cookie format compatibility | **No cross-format compatibility; forced re-login certain and accepted.** Internal cookie `llmproxy_oauth2` (AES-256-GCM, AAD-bound), never `_oauth2_proxy`. | OQ-5; ADR-001 §Context |
| **D4** | Legacy `oauth2-proxy` retirement | **Idle it through Phase 4** — daoduc: `oauth2Proxy.replicas: 0` via the chart; ensem: `kubectl scale --replicas=0`. Then **one cleanup PR for both hosts** after Phase 4. Never delete inside the cutover window. | OQ-7; AR-21 |
| **D5** | Session TTL default | **`OAUTH2_SESSION_TTL=24h`** (clamp `[5m, 24h]`), parity with `--cookie-expire=24h` — the switch does not change re-login cadence. | OQ-3; phase1 §4.2 |
| **D6** | Gate status for the no-exposure probe | **E11/S9 assert 401 JSON** (not 302) — 302 cannot distinguish "working" from "looping". | AR-14; ruling R-2 |
| **D7** | `/fe/api/version` | **Gate-exempt** (class 4b) — house gate G4 is pod-direct; gating it would break the release gate for no security gain. | AR-13; ruling R-1 |
| **D8** | Phase-4 overlap shape | **The Emperor decides at unhold time** between (a) the bounded fail-closed overlap window (council design) and (b) the shadow-mode rehearsal add-on first. Phase 4 stays held until then. | leader ruling 6; recommendation §9-4/§10-2 |
| **D9** | Principal admission posture | **daoduc: tightened** (`daoduc.org`, strict `email_verified`) **at cutover**, after a read-only IdP verification step; **ensem: `*` + explicit opt-in** until a soak proves no operator account is excluded. Strict 403 in every mode (fail-closed; no runtime bypass). | leader ruling 5; AR-04; recommendation §9-1/§10-1 |
| **D10** | Rollback unit on ensem | **R1+R2 are ONE scripted unit**, in order (route first, then deployment), because route-reverted + deployment-rolled-back is an **exposure**. Post-rollback proof is a **session-bearing round-trip**. | AR-19; ADR-001 §Rollback |
| **D11** | daoduc cutover shape | **Two upgrades + pre-stage** (D0→D4). A single upgrade that drops the annotations while the pods roll is **Window A**. | AR-18; recommendation §3.5 |
| **D12** | Chart source for the cutover | **A clean checkout at the merged Phase-1 commit** — never the stale main checkout (`5ce0ead`), which has no internal-OAuth2 chart support. | AR-20 |

### 7.2 Residual verifications (genuinely unresolved — confirm at execution time)

1. **v203 helm-revision discrepancy (daoduc, AR-46).** The ledger's revision count disagrees with the ship record by one. The **live** revision and the **v203** revision are read from `helm history` at **D0** and written into the evidence block; **never** assume a number.
2. **Casdoor claim assumptions.** The plan assumes the existing Casdoor client accepts the proxy as an OIDC client with the same redirect URI, and that no console-side change is needed (§1.3). Verified at execution by the login rehearsal (**P-E**) and the §2.3 IdP verification step. If Casdoor rejects the client (redirect-URI mismatch, PKCE not enabled, or an unverified email on an operator account) ⇒ **stop and escalate** — do not mutate Casdoor silently.
3. **The merged Phase-1 chart's edge-object render semantics (AR-21).** D3 depends on the chart rendering the edge Deployment+Service at `replicas: 0` rather than pruning them. Verify against the merged chart before D3; if it prunes, D3 does not run until the chart is corrected (§3.4.2 note).
4. **The live `oauth2-proxy` pod spec on ensem** was inferred from manifests, not observed on the wire (recommendation §12). The bridge option is not selected, so this only affects the observed 302 shape in the overlap window — re-verify with P-B/P-E.
5. **The operator gate script named in the pre-ADR draft does not exist on disk** (AR-50). The operator-surface gate is the **probe matrix** itself (§6) plus the enumerated pre/post expectations produced by task **T3.4**.

---

## 8. Research Insights (from the council, the ADR, phase1, and the manifests)

- OAuth gating today is **ingress-layer only**: the app has no auth middleware on `/fe/api/*`; the edge (`oauth2-proxy` v7.6.0` + Casdoor) gates `/`,`/ui`,`/fe/api` on both hosts (explorer, 2026-11-19; recommendation §1).
- daoduc: **Helm**-managed, nginx split ingress, Casdoor `https://door.daoduc.org`, redirect `https://llm.daoduc.org/oauth2/callback`, secret `oauth2-proxy-secret`, 24 h cookie. ensem: **`kubectl apply`**, Envoy Gateway `HTTPRoute`, Casdoor `https://door.ensem.dev`, redirect `https://llm.ensem.dev/oauth2/callback` (manifests + explorer).
- The three windows (A exposure / B crash-loop / C lockout) are **forced by the pre-ADR object-granularity sequencing**; ADR-001 resolves them structurally (recommendation §3.1; ADR-001 §Context).
- The chart is the **highest-risk artifact** in the change — a wrong conditional silently re-opens Window A (ADR-001 §Consequences; recommendation §8).
- `/v1/*` is **public by design** (proxy's own token store; `/v1/models` public). OAuth fronts only `/`,`/ui`,`/fe/api` (POK `llmproxy-v1-public-no-ingress-auth`).
- `ingress-public.yaml` / `ingress-protected.yaml` in `data-center-scripts` are **evidence-only dumps** — never apply them (runbook §1.5).
- v203 (`df12c62`) is the base for Phase 1; migration 034 (`allowed_groups`) is live on both hosts; no rollback past v203 without reversing 034 (POK + runbook §5.3; ADR §Rollback).
- The runbook's chart-cleanliness gate (`git status --short -- k8s/` MUST be empty) is part of D0 (runbook §1.3, absolute path: `/home/nea/Code/data-center-scripts/.agents/shared/planning/llmproxy-backup-first-rollout-2026-11/runbook.md`).
- Phase 1 is **dormant by default**: with `OAUTH2_ENABLED` unset/false the behaviour is unchanged, so Phase 3/4 are pure routing/chart events.

---

## Appendix A — AR register cross-reference (architecture council, 2026-11-19)

Source of authority: `architecture-recommendation.md` (AR-01…AR-66) + `architecture-decision-record.md` (ADR-001). Rows whose target is `phase1-plan.md` are owned by the parallel Phase-1 worker and are listed here for completeness only.

| AR | Sev | Subject | Landed in this plan (`phase2-switch-plan.md`) |
|----|-----|---------|------------------------------------------|
| AR-13 / R-1 | 🔴 | `/fe/api/version` breaks house gate G4 | §3.6 **S10**; §4.8 **E15**; §5.1 **M8**; §7.1 **D7** (gate-exempt, class 4b, `TestMiddleware_VersionIsExempt`) |
| AR-14 | 🔴 | Phase-4 acceptance expects the wrong status; cannot detect a lockout | §3.6 **S9** (401 JSON, daoduc no-exposure row); §4.8 **E11** (401 JSON) + **E11b** (loop detector ≥3 same-origin 302s) + **E12** (short-circuit); §6 matrix ALARM column; §7.1 **D6** |
| AR-17 | 🔴 | Phase-3 diff does not switch the chart; wrong env vocabulary | §3.2.1 **D1 values diff sets the chart toggles** (not a bare env flip); §2.2 **static pre-flight validator** (required set + phase1 §4.2 membership + secretKeyRef presence); §4.5.1/§4.5.2 env reconciliation (drifted names removed, missing names added, exact keyRef mapping); §7.1 **D2** |
| AR-18 | 🔴 | daoduc single upgrade creates Window A | §3.0 choreography table (D0→D4); §3.2/§3.4 **two upgrades**; §3.7 **rollback tiers differ per step** (after D1 vs after D3); §7.1 **D11** |
| AR-19 | 🔴 | ensem Window C lockout; false R1 rationale | §4.2 **A→B→C→D** per-path choreography; §4.2 **Window C** analysis; §4.9 **R1+R2 as ONE scripted unit** + the false rationale **deleted** and corrected; §4.9 **session-bearing round-trip** replaces the E11 re-check; §7.1 **D10** |
| AR-20 | 🔴 | Cutover would deploy the stale main checkout's chart | §2.1 **P2**; §3.1 D0 step 2 (chart identity + cleanliness assertions, `5ce0ead` forbidden); §3.2.3/§3.4.3 use `$CHART`; §7.1 **D12** |
| AR-21 | 🔴 | Helm prunes the edge objects ⇒ the rollback anchor is destroyed | §3.4.1 D3 `oauth2Proxy.replicas: 0`; §3.4.2 render assertion; §3.5 **P-J**; §4.7 `kubectl scale` (Service keeps 0 endpoints); §5.1 **M4** redefined (objects exist at intended replicas, pod-log check dropped); §6 P-J; §7.1 **D4** |
| AR-22 | 🔴 | D15 mutual-exclusion → three-state model | §3.2/§3.4 (the three states); §3.2.2/§3.4.2 render assertions per state (phase1 §8.4 H1–H4); §1.2 invariants; §2.2 step 4 (guard must not fire) |
| AR-27 | 🟡 | Offline session continuity | §1.8 (existing internal sessions stay valid during an IdP outage) |
| AR-28 | 🟡 | Cutover verification silently depends on Casdoor | §1.8 **Casdoor outage semantics** (environmental failure, not a code defect; rollback is IdP-independent; sessions survive; new logins die; `/v1/*` unaffected) |
| AR-44 | 🟡 | Envoy `/oauth2` rule has no timeout | §4.4.1 diff (`timeouts: request: 60s` on the `/oauth2` rule) + the 15 s-vs-60 s rationale; §4.9 R1 rollback route carries the same timeout |
| AR-45 | 🟡 | HTTPRoute apply has no acceptance check | §4.4.2 (**Accepted/ResolvedRefs** check + per-path probes + failure action = revert the route; `/v1` is a separate rule ⇒ the ensemble lane survives); §4.6.2; §4.8 **E13** |
| AR-46 | 🟡 | Rollback operand never captured | §3.1 D0 step 1 (**both** the live rev and the v203 rev) → §5.3 evidence block; §3.7 rollback table; §4.3 step 1 |
| AR-47 | 🟡 | GitOps pin order unspecified | §5.2 (**daoduc first, then the primary; one commit per host; `main` untouched; pins ride along with the deploy**) |
| AR-49 | 🟡 | Cloudflare premise + 5xx masking | §1.9 (Cloudflare fronts llm.daoduc.org; the tunnel masks origin 5xx as `error code: 5xx`; debug at/below nginx) |
| AR-50 | 🟡 | the operator gate script named in the pre-ADR draft does not exist on disk | §4.8 **E9** (reference replaced by the matrix + the T3.4 enumeration); §3.8 **T3.4** (enumerate path → pre-switch → post-switch with owner/repo/path); §7.2 residual 5 |
| AR-51 | 🟡 | Runbook citation path ambiguous | §8 (absolute path `/home/nea/Code/data-center-scripts/.agents/shared/planning/llmproxy-backup-first-rollout-2026-11/runbook.md`) |
| AR-52 | 🟡 | Overlap-state operator precondition | §1.6 (**P7′**, low-traffic window, user notice, fail-closed ping-pong that terminates only when the edge leaves the path); §2.1 **P7**; §3.3 P-E rehearsal; §4.1; §4.2 |
| AR-53 | 🟡 | Pod straddle during the overlap | §3.1 D0 step 5 (Recreate/maxSurge-0 branch + patch + revert); §3.5 (revert after soak); §4.5.3 step 1 (Recreate patch before B) + §4.7 (revert after soak) |
| AR-54 | 🟡 | `/oauth2/start` valid-session short-circuit missing | §1.6 (short-circuit keeps the ping-pong cheap); §4.8 **E12** + §6 **P-K**; §3.6 **S12**; §3.3 step 3 |
| AR-64 | 🟢 | `app-root:/ui/` short-circuit in internal mode | §3.2.2 (annotations + `/ui/` behaviour unchanged in overlap; benign one-hop redirect in internal mode — phase1 §8.3) |
| AR-04 | 🔴 | Principal admission control (phase-1 side) | §1.7 **per-host posture table**; §2.3 **daoduc read-only IdP verification step**; §3.2.1 `internalOAuth2.emailDomains` / `allowAnyEmailDomain` / `requireEmailVerified`; §4.5.1 ensem `*` + explicit opt-in; §7.1 **D9** |
| AR-03 / R-5 | 🔴 | Request-derived security decisions | §4.5.1 `OAUTH2_EXTERNAL_URL` is set explicitly (never derived from a spoofable header); §3.2.1 same on daoduc |
| AR-32 / R-9 | 🟡 | Cookie key separation | §1.4 (different names + HKDF subkeys ⇒ the two layers never collide); §4.5.2 (`OAUTH2_COOKIE_SECRET` ← `COOKIE_SECRET`, dedicated name used) |
| AR-23 | 🟡 | Logout during an IdP outage | §1.8 (logout is best-effort; the local cookie clears first) |
| AR-01…AR-12, AR-15…AR-16, AR-24…AR-42, AR-48, AR-55…AR-66 | — | Phase-1-owned rows | *`phase1-plan.md` §17 cross-reference (parallel worker). Not restated here except where a cutover step depends on them (§3.3, §4.5, §4.9).* |

**Net effect on Phase 2:** the cutover is now **two Helm upgrades** on the backup and **three pre-staged applies** on the primary, both built on the same three-state chart; the interlock is **structural** (I-1/I-2/I-3) rather than sequencing-dependent; the rollback on the primary is **one scripted unit** with the exposure rationale recorded; and every step is gated by a probe matrix whose ALARM column names the failure it detects.

---

**End of plan.**
