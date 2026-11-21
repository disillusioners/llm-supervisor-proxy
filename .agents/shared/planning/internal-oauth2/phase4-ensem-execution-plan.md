# Phase 4: ensem (primary) Execution Plan — Internal OAuth2 Cutover

**Feature:** `internal-oauth2` — in-binary OAuth2 replacing the edge `oauth2-proxy` + Casdoor gate on `llm.ensem.dev`.
**Status:** DECISION-READY — awaiting Emperor option choice + unhold word (through Ari).
**Predecessors:** Phase 1 (v204 dormant, both hosts, CI #204 @ `abd61d62`→`abd61d7`, digest `sha256:cfc34862…`) · Phase 3 (daoduc cutover COMPLETE 2026-11-20: helm rev 51, internal-only, edge idled @0, Emperor ruling "B" parity posture, zero-restart choreography, full proof ledger in `data-center-scripts` values-pinned.yaml @ `756fa80`).
**Authority:** ADR-001 (Approach 5) + phase2-switch-plan.md §4 (draft choreography — this document supersedes §4 with live pre-flight data) + Emperor directive 2026-11-20 ("prepare plan for llm.ensem first").
**Hard fences (unchanged):** primary HARD HELD until the §6 unhold checklist is green AND the Emperor's word arrives through Ari. Zero mutations in preparation. All staged operands live under `/tmp/ensem-oauth2-rollback/` — NEVER on any branch a reconciler could apply.

---

## §0 ONE-SCREEN DECISION SUMMARY (relay verbatim)

**The switch moves llm.ensem.dev's login gate from the edge oauth2-proxy into the app binary — exactly what we did on daoduc tonight, adapted to the primary's Envoy/kubectl mechanics. The `/v1` LLM lane (the ensemble's own brain) is never touched by any step.**

**Option A — bounded fail-closed overlap (RECOMMENDED; daoduc-proven tonight):**
1. You log into `https://llm.ensem.dev/ui/` through the CURRENT gate (door.ensem.dev) — keeps your session valid across the switch (2 min).
2. I repoint the `/oauth2` route rule to the app, then apply the enforcing deployment (Recreate — a ~10–30 s `/v1` gap on the PRIMARY; the standing failover to daoduc absorbs it, then I repoint `/`,`/ui`,`/fe/api` to the app and idle the edge (2–4 min, mostly rollout wait).
3. You reload the UI once — your session mints the app cookie (1 min).
**Your hands-on time: ~3 minutes** (same shape as tonight). Total window ~15 min including soak-start. Fresh (cookie-less) logins during the middle minute bounce fail-closed; nobody is exposed; P7′-holders (you) sail through.

**Option B — shadow-mode rehearsal first:** adds a THIRD app mode (mint-but-don't-enforce) + a second Casdoor app registration — **requires your Casdoor console work** (~15 min) + new code + new review + a second deploy cycle before the real switch. It proves the OIDC flow before any overlap — but Option A's pre-flight (below) already proves everything B would, with tonight's live daoduc cutover as full-scale evidence on the SAME binary, SAME chart semantics, SAME Casdoor version. **Your hands-on time: ~18+ min across two windows.**

**My recommendation: Option A.** Tonight's daoduc cutover exercised the identical flow end-to-end (real id_token → PKCE exchange → internal verifier → cookie mint → SSE session) with zero lockout and zero rollback. The ensem deltas are mechanical (§4), not semantic.

**Recommended window: 05:00–09:00 Asia/Ho_Chi_Minh (22:00–02:00 UTC)** — your sleep hours: the ensemble lane is idle (tonight's ring: 94% ensemble-tagged requests during your 23:40–00:17 local session; the only night traffic is the 15-min canary, which doubles as the failover probe). Avoid your active hours.

**Lockout-impossibility (§5):** structurally impossible under Option A — every intermediate state leaves a known-good path for the P7′-holding operator, and the one-unit rollback restores the edge in <60 s. Pre-flight additionally confirms: door.ensem.dev = **casdoor v1.887.0** (same as daoduc → same claim shape → all 5 IdP accounts admissible under the recommended posture), redirect `https://llm.ensem.dev/oauth2/callback` already registered on app `llm-ensem`, secret keys present, zero Casdoor mutation needed.

---

## §1 Pre-flight findings (read-only, 2026-11-20 ~17:20 UTC — evidence-gated)

### 1.1 door.ensem.dev Casdoor (ns `casdoor`, primary cluster)
- **Version: `casbin/casdoor:v1.887.0`** — IDENTICAL to daoduc. ⇒ **Claim-shape verdict: camelCase `emailVerified`** (v1.887.0 `object/token_jwt.go:68`, `UserWithoutThirdIdp`), i.e. the standard `email_verified` claim is **ABSENT** from id_tokens → the app verifier (snake_case-only parser, no fallback) reads it as absent → admitted under `*` + `ALLOW_ANY_EMAIL_DOMAIN=true` (anyMode). **Parity-compatible.** ⚠️ Standing rider (recorded in POK `idp-01-casdoor-claim-shape-parity`): any future Casdoor upgrade re-materializes standard `email_verified:false` → strict admission 403s EVERY account → re-verify claim shape before any IdP upgrade or posture tightening.
- **Users (5, identities only):** `admin@code-aria.com`, `admin@example.com`, `admin-mtri@example.com`, `khanguyen@gmail.com`, `user_ktc22e@example.com` — **all `email_verified=f`** (irrelevant today: claim-absent admission). All 5 admissible under the recommended posture. **Zero accounts excluded.**
- **App registrations:** `llm-ensem` (client `2cb227ee012d9d5dceda` — the public id already in every authorize redirect), **redirect URIs = exactly `["https://llm.ensem.dev/oauth2/callback"]`** ✓ (OQ-1 reuse holds — no console action), `token_format=JWT`, `cert-ensem-dev` (own cert → `kid` in JWKS), RS256 default, expire 168h.
- **Posture sub-decision (Emperor, 1-line):** original plan §1.7 said ensem `*`+opt-in+`requireEmailVerified=true`; ruling-B daoduc precedent + code reality (the strictness knob is parsed but **unread** in the shipped admission path — present-false 403s unconditionally, absent passes anyMode) ⇒ **recommend `OAUTH2_REQUIRE_EMAIL_VERIFIED: "false"` (ruling-B parity)**. Either value admits all 5 accounts TODAY; `false` is honest about the no-op and matches daoduc. Changing it later = one configmap edit.

### 1.2 §4.1 six unhold conditions vs LIVE state (all verified read-only)
1. ✅ Phase 3 daoduc soak green (10-min clean, full matrix, 2026-11-20).
2. ⏸ Emperor sign-off — THIS document requests it.
3. ✅ Independent shell lane available (the DevOps bash lane reaches daoduc directly; commands are pre-staged and none require llm.ensem.dev mid-procedure — verified: this preparation ran entirely without the primary's LLM lane).
4. ⏸ Phase-4 overlap decision — Option A recommended (§0).
5. ⏸ P7′ + low-traffic window + user notice — window recommended (§0); notice at unhold.
6. ⏸ Staged rollback operands — **Stage 0 in §2 creates them** (pre-flight step, first mutation-free action of the execution window).

### 1.3 Live primary state (baseline for every diff)
- Deployment `llm-supervisor-proxy`: **gen 44/44**, `RollingUpdate` (maxSurge 1/maxUnavailable 0), image `abd61d7`, running digest `sha256:139b612a…` == verified index. Pod `llm-supervisor-proxy-6b94cd7cdb-pgmrv` ready.
- ⚠️ **`restarts=1` — pre-existing v204 defect, NOT oauth2:** container died 2026-11-20T10:44:13Z, exit 2 — `panic: nil pointer dereference` in `pkg/proxy/sendHeartbeat` (heartbeat.go:114; `bufio.Writer` nil on an SSE flush after a client disconnect, post 14.7s stream). One crash in ~14h; recovered clean; zero panics since. **Handoff: Developer to fix the SSE-heartbeat client-disconnect race (guard the flush / recover the goroutine).** Plan impact: if a crash lands mid-window, the Recreate rollout state machine still converges; the R1+R2 unit is unaffected. Not a blocker; noted in the risk table.
- Edge: `oauth2-proxy` deploy 1/1 + svc (113d), secret `oauth2-proxy-secret` keys `CLIENT_ID/CLIENT_SECRET/COOKIE_SECRET` ✓.
- HTTPRoute `llm-ensem-dev-route`: live spec == GitOps (`/oauth2`→edge, `/v1`→app+30m, `/`,`/ui`,`/fe/api`→edge+30m); redirect companion intact. **No SecurityPolicy on the llm path** (only unrelated `nonself/opa-auth-api`, `test-envy/example-jwt-policy`).
- ConfigMap `llm-supervisor-proxy` (3 keys: LOG_LEVEL, MCP_ENABLED, SSE_HEARTBEAT_ENABLED).

### 1.4 Blast radius — shared IdP siblings (zero)
door.ensem.dev also fronts: `app-mtri-ecom`, `app-mtri-ari`, `code-aria-app`, `app-built-in` (Casdoor apps) and the **ensemble guard** (ns `ensemble`, www.ensem.dev) + **9router guard** (ns `9router`). This plan touches ONLY: HTTPRoute `llm-ensem-dev-route` (ns llmproxy), Deployment/ConfigMap `llm-supervisor-proxy` (ns llmproxy), `kubectl scale deploy/oauth2-proxy -n llmproxy` (idle). **No Casdoor object, no ensemble/9router object, no Gateway object is modified. Sibling guards' blast radius = zero.**

### 1.5 Traffic shape → window
Requests ring (100 entries): 94% `ensemble` tag, ring span ~37 min during the Emperor's 23:40–00:17 UTC+7 session — the lane tracks his active hours. Night traffic = 15-min canaries only. **Recommended: 05:00–09:00 UTC+7** (his sleep). The Recreate `/v1` gap (~10–30 s) is absorbed by the standing daoduc failover (daoduc `/v1` is token-authenticated and independent of its own oauth2 cutover).

---

## §2 Option A — bounded fail-closed overlap (RECOMMENDED)

Adapts tonight's daoduc D1→D3 to kubectl-apply mechanics. Invariant mapping: I-1 = step A before B; I-2 = step C only after B's rollout complete; I-3 = configmap+deployment applied as one unit (env + secretKeyRef in the same act).

### Stage 0 — pre-flight staging (first action of the window; still mutation-free)
```bash
export KC=~/.kube/config   # context default; binaries /home/nea/bin
# G0 cluster guard: server must be https://192.168.1.250:6443
/home/nea/bin/kubectl --context default config view --minify -o jsonpath='{.clusters[0].cluster.server}'
# stage rollback operands (unhold condition #6)
mkdir -p /tmp/ensem-oauth2-rollback
D=/home/nea/Code/data-center-scripts/infranstructure/k8s/llmproxy/rancher-local-ensem-dev
for f in llm.ensem.dev-httproute.yaml deployment.yaml configmap.yaml; do cp "$D/$f" "/tmp/ensem-oauth2-rollback/$f"; done
# anchors: gen (expect 44), digest (cfc34862), rollout history, /v1/models id-set baseline (own-host baseline)
curl -s https://llm.ensem.dev/v1/models | python3 -c 'import json,sys; print("\n".join(sorted(x["id"] for x in json.load(sys.stdin)["data"])))' > /tmp/models-ensem.pre.txt
```
**P7′ — Emperor logs in NOW** (browser, `https://llm.ensem.dev/ui/`, door.ensem.dev credentials, through the CURRENT edge). Keep the browser open. His `_oauth2_proxy` cookie (24 h TTL) is the overlap free-pass.

### Step A — HTTPRoute: repoint `/oauth2` ONLY → app (the irreversible-looking step; instantly reversible by re-apply)
Diff (documented operand — lives in `/tmp`, never on a branch):
```diff
       backendRefs:
-        - name: oauth2-proxy
-          port: 4180
+        - name: llm-supervisor-proxy
+          port: 4321
+      timeouts:
+        request: 60s          # AR-44: cold OIDC discovery + token exchange + JWKS > Gateway default 15s
```
— applied to the `/oauth2` rule ONLY; `/`,`/ui`,`/fe/api` stay `oauth2-proxy:4180`; `/v1` rule untouched; redirect companion untouched.
```bash
kubectl --context default -n llmproxy apply -f /tmp/ensem-oauth2-rollback/llm.ensem.dev-httproute-A.yaml   # staged variant
# AR-45 acceptance: parents conditions Accepted=True ResolvedRefs=True
# probes: anon /oauth2/start → 404 (app still DORMANT — P-L, by design); /v1/models → 200; / → 302 (edge)
```
**State:** edge-gated + app-dormant-serving-/oauth2. Fresh logins degraded-but-closed (404); P7′ holders unaffected. `/v1` untouched — **the ensemble keeps thinking.**
**Failure/rollback:** Accepted≠True or any probe red → re-apply the Stage-0 httproute → back to baseline in <60 s.

### Step B — enforcing deployment + configmap (ONE unit; Recreate)
1. Configmap apply: adds `OAUTH2_ENABLED=true, ISSUER_URL=https://door.ensem.dev, EXTERNAL_URL=https://llm.ensem.dev, EMAIL_DOMAINS=*, ALLOW_ANY_EMAIL_DOMAIN=true, REQUIRE_EMAIL_VERIFIED=false (ruling-B parity — §1.1 sub-decision), ALLOWED_ALGS=RS256, SESSION_TTL=24h, COOKIE_SECURE=true` (exact phase1 §4.2 names; AR-17 ensem-variant validator runs FIRST).
2. Deployment apply: env block = 9 `configMapKeyRef` + 3 `secretKeyRef` (`OAUTH2_CLIENT_ID`←`CLIENT_ID`, `OAUTH2_CLIENT_SECRET`←`CLIENT_SECRET`, `OAUTH2_COOKIE_SECRET`←`COOKIE_SECRET` from `oauth2-proxy-secret`) + **`spec.strategy: Recreate` IN THE FILE** (approver fix; apply re-sets the field — post-apply re-check gate: `spec.strategy.type == Recreate` else ABORT) + recomputed `checksum/config`. Image tag untouched (abd61d7).
3. `rollout status --timeout=300s`. **Expected: ~10–30 s single-pod gap** (Recreate, AR-53 no-straddle) → **`/v1` briefly down on the primary; standing failover to daoduc absorbs** (ensemble lane keeps thinking).
4. Verify: pod 1/1, 0 restarts on the NEW pod, env names present (names only), boot line `[oauth2] internal OAuth2 ENABLED … issuer=https://door.ensem.dev`, digest unchanged.
**State:** OVERLAP — edge reverse-proxies `/`,`/ui`,`/fe/api` (P7′ cookie honored), app enforces behind it, `/oauth2` on the app. **Cookie-LESS users in overlap:** `/` → edge 302 → `/oauth2/start` (APP) → app 302 → Casdoor → credentials → callback (app) → mint → rd → `/` → edge STILL no edge-cookie → 302 → `/oauth2/start` → app short-circuit (AR-54, valid cookie, zero IdP hop) → rd → `/` → edge → **ping-pong, fail-closed, never an exposure** — identical semantics to tonight's nginx loop, one hop-shape different (edge's own 302 instead of nginx auth-signin). Ends only at step C. This is why P7′-BEFORE-A is a hard invariant and the window is minutes.
**Failure/rollback:** CrashLoop/failed rollout → R1+R2 ONE UNIT (below). Probe failures → R1+R2.

### Step C — protected-path repoint + edge idle (ends the overlap structurally)
1. HTTPRoute re-apply (staged variant C): `/`,`/ui`,`/fe/api` rules → `llm-supervisor-proxy:4321` (30m timeouts kept); `/oauth2` stays app; `/v1` untouched.
2. AR-45 acceptance + probes: anon `/fe/api/models` → **401 JSON `{"error":"unauthenticated"}`**; `/` → 302 `/oauth2/start?rd=` same-origin; `/v1/models` 200.
3. **Edge idle (never delete):** `kubectl --context default -n llmproxy scale deploy/oauth2-proxy --replicas=0` — Deployment+Service remain (rollback anchor; Service keeps 0 endpoints, harmless).
**Emperor reloads the UI once** → edge cookie no longer needed; app session already minted in overlap (or mints now through the app-only path). FE principal renders.
**Failure/rollback:** R1+R2 unit (below), edge scaled back to 1 FIRST.

### Step D — soak + full matrix (E-rows)
10-min soak: 0 restarts on the new pod, `/v1/models` id-set == baseline (own-host), boot + oauth2 log families clean, canary `[RACE] Winner` observed (bonus, not gate). Full matrix (internal-only column): S9 anon-401 edge-level, S10 version-exempt 200 pod-direct, P-F forged-header/cookie 401, P-G admin-plane 401 ×3, P-H anon `/v1` both protocols 401 + models 200, P-J edge objects @0, P-K short-circuit (browser-level via Emperor), **S7 logout probe (deferred from Phase 3 — scheduled HERE, see §6)**. Then revert `spec.strategy` → `RollingUpdate 1/0` in the manifest + apply (spec-only, no rollout) — house default restored.

### R1+R2 — the ONE-UNIT rollback (never split)
Order matters: **route FIRST, deployment SECOND** (route-reverted + enforcing-deploy = Window-C loop for fresh logins; deploy-reverted + route-new = dormant app 404s /oauth2 — both fail-closed but degraded; the unit avoids dwelling in either):
```bash
kubectl --context default -n llmproxy apply -f /tmp/ensem-oauth2-rollback/llm.ensem.dev-httproute.yaml   # R1: full pre-switch route
kubectl --context default -n llmproxy scale deploy/oauth2-proxy -n llmproxy --replicas=1                  # edge back FIRST-class citizen
kubectl --context default -n llmproxy apply -f /tmp/ensem-oauth2-rollback/configmap.yaml                  # R2a
kubectl --context default -n llmproxy apply -f /tmp/ensem-oauth2-rollback/deployment.yaml                 # R2b: pre-switch spec (RollingUpdate, no OAUTH2 env)
kubectl --context default -n llmproxy rollout status deployment/llm-supervisor-proxy --timeout=300s
# post-rollback proof: session-bearing round-trip (Emperor reload) + /v1 smoke + edge 1/1
```
**gen semantics (no helm ladder):** "rollback" = re-apply of the staged pre-switch files; gen increments monotonically (the ANCHOR is the file + digest `cfc34862`, not a revision number). Deep fallback: none needed — the pre-switch files ARE v204 dormant; never roll past v204 without the 034-migration ordering rule.

---

## §3 Option B — shadow-mode rehearsal first

**What it is (original plan §4.1(b)):** ship a third app mode — mint-but-don't-enforce — so the full OIDC flow runs against live door.ensem.dev with zero enforcement risk, before Option A's A→B→C.
**What it requires:** (1) NEW code: a `shadow` mode in pkg/oauth2 (mode tri-state instead of dormant/enforce — a hard-rule-6 amendment); (2) a SECOND Casdoor app registration (e.g. `llm-ensem-shadow`) so shadow sessions can't be confused with enforcing ones — **an Emperor console action (~15 min)** + a new secret; (3) its own review + test pack + image cycle (CI + digest verify + deploy) before the real switch; (4) a second Emperor browser window.
**What it proves beyond Option A's pre-flight:** the OIDC code-path against door.ensem.dev specifically — but that is ALREADY proven at full scale: same binary (v204), same chart semantics, same Casdoor version (v1.887.0), same claim shape, same client mechanics — tonight's daoduc cutover (R2 log-bound: real id_token → PKCE exchange 200 → verifier → mint → authorized session). The ensem deltas (Envoy rules, kubectl semantics) are NOT exercised by B either — they're covered by A's dry-run/acceptance gates.
**Cost:** ~1 dev cycle + review + 2 deploys + 2 Emperor windows (~18+ min hands-on) + a new trust surface (the thing ADR-001 was chosen to avoid).
**Failure modes:** shadow-mode bugs (new code), console misconfiguration, rehearsal passing while A still needs its own window anyway.
**Verdict:** not recommended; the residual risk it addresses is already retired by evidence.

---

## §4 Mechanical differences from daoduc (the skim section)

| Dimension | daoduc (done) | ensem (this plan) |
|---|---|---|
| Edge gate | nginx `auth-url` subrequest (edge = auth-checker) | oauth2-proxy **reverse-proxy** (edge carries traffic) |
| Route object | Ingress paths in helm chart | **HTTPRoute `llm-ensem-dev-route`** rules (Envoy Gateway); redirect companion untouched |
| Apply semantics | helm 3-way merge (`-f` explicit; `--reuse-values` nil-pointer quirk) | **kubectl apply full-file** — every apply re-writes EVERY field in the file ⇒ `spec.strategy: Recreate` must be IN the diff + post-apply re-check gate (no `--reuse-values` twin; the hazard is field ownership) |
| Pod transition | chart-rendered Recreate (D1), zero-restart D3 | Recreate via manifest ⇒ **~10–30 s `/v1` gap on the primary — standing failover to daoduc absorbs** (daoduc `/v1` independent of its cutover) |
| IdP | dedicated door.daoduc.org | SHARED door.ensem.dev (also fronts ensemble + 9router guards — **untouched**, §1.4) |
| Posture | ruling-B parity (`*`, any, verified=false) | same recommended (§1.1 sub-decision; all 5 accounts admissible either way) |
| Edge idle | chart `oauth2Proxy.replicas: 0` | `kubectl scale deploy/oauth2-proxy --replicas=0` (objects never deleted) |
| Rollback | helm ladder (49/50/51) | re-apply staged pre-switch files (R1+R2 one unit; anchor = file + digest) |
| Live lane risk | backup-only | **THE ensemble lane thinks through this host** — hence: sleep-hours window, failover pre-verified, independent shell lane (commands pre-staged, none need llm.ensem.dev mid-procedure) |

---

## §5 Lockout-impossibility analysis (architect's v1 warning, resolved)

Under Option A, operator lockout is **structurally impossible**:
1. **P7′-before-A:** the operator's edge cookie (24 h TTL) passes the reverse-proxy gate for every state from baseline through C — he is never without a working path while the edge is in-path.
2. **I-1 (loop-free by construction):** `/oauth2` reaches the app BEFORE enforcement turns on (A before B) — the app's login flow is reachable from the first enforcing second; the ping-pong affects only cookie-LESS fresh logins and is fail-closed (never an exposure), ending at C.
3. **I-3 (no bare env flip):** configmap + deployment + secretKeyRef land as one apply — a CrashLoop-on-missing-env (Window B) is impossible; if the pod fails anyway, R1+R2 restores the pre-switch spec in <60 s.
4. **Pre-flight gates (§1):** claim-shape verdict (v1.887.0 absent-claim — same as tonight's PROVEN daoduc flow), all 5 IdP accounts admissible, redirect already registered, secret keys present — the four ways the MINT itself could fail are each retired by direct evidence.
5. **Rollback = re-apply of staged files** — no IdP operation, no migration, no image change; edge scale-back is part of the unit.
Option B adds nothing to this list — it re-proves #4 by rehearsal instead of by tonight's production evidence.

---

## §6 Unhold checklist + scheduled probes + post-cutover pin (documented only)

**Unhold (ALL must be live-green at execution time):**
- [ ] §4.1 conditions 1 (daoduc soak ✅ 2026-11-20) and 3 (independent shell lane ✅) re-verified
- [ ] 2 — **Emperor sign-off: option choice + window** (this document, relayed through Ari)
- [ ] 4 — overlap decision recorded (Option A default)
- [ ] 5 — P7′ scheduled + low-traffic window confirmed + user notice sent
- [ ] 6 — Stage 0 staged operands exist in `/tmp/ensem-oauth2-rollback/` (first execution action)
- [ ] §1 pre-flight gates re-run green (claim shape, user admissibility, redirect, secret keys, live spec == GitOps)
- [ ] heartbeat panic (§1.3) — acknowledged as accepted-risk OR Developer fix landed (optional, not a blocker)

**S7 logout probe — scheduled in THIS window (deferred from Phase 3):** post-C, Emperor clicks sign-out on llm.ensem.dev → assert session clears, `/` re-triggers login chain. **Optional pre-warm NOW (my call, offered):** the same probe on daoduc — the Emperor's daoduc session is disposable; 30 seconds of his time retires the S7 row for BOTH hosts' code path (same binary). Zero risk; his call.

**Post-cutover dcs pin (DOCUMENTED ONLY — push happens after execution is authorized):** one commit on `feature/llmproxy-oauth2-ensem-cutover` touching ONLY `rancher-local-ensem-dev/{llm.ensem.dev-httproute.yaml, deployment.yaml, configmap.yaml}` in their FINAL state (route: app backends; deploy: OAUTH2 env + Recreate→rolling-revert noted; configmap: parity envs) → `--no-ff` into `latest` → push. NO primary file changes before execution. Rollback reverts the pin in the same shape.

**Evidence ledger (to fill at execution):** anchors (gen 44→45, digest, staged-file SHAs), per-step timings, probe matrix results, Emperor session proof (nginx/gateway + casdoor `access_token` 200 log-binding as tonight), soak summary, dcs SHA.

---
*Prepared 2026-11-20 by the DevOps deploy lane. Read-only preparation: zero primary mutations, zero dcs changes; the only artifacts created are this document (docs branch) and /tmp staging directories.*
