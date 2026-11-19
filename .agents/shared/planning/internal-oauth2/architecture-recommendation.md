# Architecture Recommendation — `internal-oauth2` (Pre-Reviewer Enrichment & Security Review)

**Feature:** `internal-oauth2` — dormant-by-default in-binary OAuth2/OIDC authentication layer (`pkg/oauth2/`) to replace the edge `oauth2-proxy` + Casdoor gate on the operator surfaces.
**Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-internal-oauth2` (branch `feature/internal-oauth2`, base `origin/latest` `92c4234`, tree parity with `origin/master` `df12c62` = v203, live on both hosts).
**Reviewed artifacts:** `plan-overview.md` (228 L), `phase1-plan.md` (1,091 L), `phase2-switch-plan.md` (578 L), `adr-token-permission-model.md` (v203 token authz), `k8s/` chart, `cmd/main.go`, `pkg/ui/server.go`, `pkg/proxy/handler.go`, `pkg/authz/`, `pkg/mcp/`, `pkg/ui/frontend/`, `data-center-scripts` manifests (read-only).
**Date:** 2026-11-19
**Status:** Architectural review complete — **Phase 1 shippable after amendment; Phase 2 (switch) must not be executed as written.**
**Companion:** `architecture-decision-record.md` (ADR-001 edge→app auth handover interlock).

---

## 1. Executive summary

The plan's **core design is sound and should proceed**: dormant-by-default with zero behavioral delta, an explicit `pkg/ui` consumer-side identity interface (no `pkg/ui` → `pkg/oauth2` import), env-only secrets (`config.Config` untouched, so `GET /fe/api/config` cannot leak them), a structurally disjoint principal (`ui.Identity` ≠ `auth.AuthToken` ≠ `authz.Resolve`), and a `/v1/*` data plane that stays outside the gate. Five independent reviews (four skill-dimension workers + one council) found **no defect in those load-bearing properties**.

What the reviews did find is concentrated in two places:

1. **Seven blocking security/composition defects in Phase 1** — the most serious being that `state` is not bound to the initiating browser (**login-CSRF / session fixation**), that path classification is **fail-open** (every route added after this ship is public by default), and that `redirect_uri` + cookie `Secure` are **derived from request-controlled headers**.
2. **The Phase 2 switch plan cannot be executed as written** — its diff does not actually switch the chart, its env vocabulary does not match the binary's, and its step ordering creates an **unauthenticated admin-plane window on the backup** and a **deterministic redirect-loop lockout on the primary**. The council resolved the mechanism: **per-path `/oauth2`-first choreography with a three-state chart**.

**Counts:** 22 blocking (🔴) · 34 significant (🟡) · 12 improvement (🟢) — full register in §4–§6.

**Recommendation:** proceed to Reviewer **only after** the 🔴 register is folded into `phase1-plan.md` and `phase2-switch-plan.md`. Phase 1 (dormant build) is a normal implementation commission once amended — it changes nothing observable on either host. Phase 3/4 execution is a separate, harder gate that should not be scheduled until the phase-2 rewrite lands.

---

## 2. Method, provenance, and confidence

| Input | Skill | Scope | Instance |
|---|---|---|---|
| Worker A | `security-design` | crypto/OIDC design commitments vs live Casdoor reality | `5270fa47` |
| Worker B | `resilience-design` | IdP resilience, JWKS concurrency, failure modes | `b01ff17b` |
| Worker C | `data-flow-design` | route×mode composition, v203 authz separation, FE data flow | `551d81e0` |
| Worker D | `system-decomposition` | Helm decomposition + two-host cutover topology | `be167709` |
| Council | `system-decomposition` | the one contested mechanism question (interlock) | governor `367e6b36`, 2 councilors, models `agentic` + `coding` |

- All findings were produced **read-only, inside the worktree**; the stale-main-checkout trap was respected by every reviewer. No secret value was read or reproduced (paths + key names only).
- Citations are reproduced as read by the reviewers. Implementers must re-read at the pinned commit — the plan's own **R17** rule ("citations taken from the stale main checkout") applies to this document too.
- **Confidence: High** on the Phase 1 crypto/composition findings (each corroborated by primary evidence — live Casdoor discovery/JWKS documents, `casdoor/object/token_jwt.go`, a Go 1.26 `ServeMux` probe, the house rollout runbook). **Medium** on the Phase 2 topology findings (rendered from chart + GitOps manifests; the live `oauth2-proxy` pod spec on ensem was inferred from manifests, not observed).

---

## 3. The edge→app auth handover — council decision (ADR-001)

### 3.1 The problem

OAuth today is enforced **only at the edge** (`oauth2-proxy` v7.6.0, Casdoor OIDC, cookie `_oauth2_proxy`) on both hosts: daoduc via Helm/nginx split ingress, ensem via raw `kubectl apply` + Envoy Gateway `HTTPRoute` in reverse-proxy mode. The plan moves the gate **into the binary**. During handover, three windows exist — all confirmed by the reviewers and the council:

- **Window A (daoduc, exposure).** One `helm upgrade --atomic` drops the nginx auth annotations; nginx reloads in seconds while pods roll in tens of seconds → the **old, un-gated pod serves `/fe/api/*`** unauthenticated (credentials incl. API keys, model config, token store). `--atomic` only rolls back on failure; `--wait` waits after apply. Neither closes it.
- **Window B (daoduc, forced by the two documents jointly).** The phase-2 diff flips only `env.OAUTH2_ENABLED` and never sets the chart's `internalOAuth2.enabled` → the `secretKeyRef` block never renders → no `OAUTH2_CLIENT_ID` → startup fail-fast → **CrashLoopBackOff → the backup's `/v1/*` dies**. "Fixing" it by enabling `internalOAuth2.enabled` trips the D15 render guard, funneling the operator into Window A. **The two documents jointly force A-or-B.**
- **Window C (ensem, lockout — PRIMARY).** Step 1 (deploy with `OAUTH2_ENABLED=true`) → Step 2 (HTTPRoute repoint). The app's guard is *inside the binary* and demands its own `llmproxy_oauth2` cookie, but the edge still owns `/oauth2` and holds only `_oauth2_proxy` → app 302 → edge `/oauth2` → oauth2-proxy → Casdoor → edge cookie only → app still 401 → 302 again: **infinite redirect loop**, operator-paced and therefore unbounded. The plan calls it "brief, non-exposing"; its E11 probe expects 302 — which a loop also produces.

### 3.2 Council consensus — per-path `/oauth2`-first, three-state chart

**Mechanism:** transfer `/oauth2` ownership to the app **before** enabling app enforcement anywhere; hold an explicit **overlap state** (edge still gating `/`,`/ui`,`/fe/api` while the app enforces behind it, with the app's own flow reachable); remove the edge from the path **last**.

**No new trust surface** — no trusted header, no delegation hop, no CIDR, no foreign-cookie parsing. **OQ-5 stands unamended.** The three states are *chart-level render states*, not new application modes (the app keeps `dormant`/`enforce` only). This is the decisive advantage over the bridge/shadow options.

### 3.3 Why it wins — option comparison (5 axes)

| Option | Complexity | Scalability | Maintainability | Risk | Cost | Verdict |
|---|---|---|---|---|---|---|
| **5. Per-path `/oauth2`-first + three-state chart** | Med — 3 render states + short-circuit | N/A (2 hosts) | High — states are declarative in the chart | **Low** — no new trust boundary, no new app mode | Med — chart template logic only | ✅ **Recommended** |
| 4. Session bridge (app asks edge `/oauth2/auth`) | High — new delegation hop + identity mapping | N/A | Med — two identity sources | **Med-High** — transition-time hard dependency on edge pods; rescopes OQ-5 | High | Runner-up — **probe-gated** (needs ensem cookie-forwarding + daoduc `--set-xauthrequest` verified; wins only if zero bounce is operationally required) |
| 1. Shadow mode (app mints, doesn't enforce) | Med-High — new mode + edge-trust source | N/A | Med | Med — buys rehearsal, **zero additional window-freedom** | Med | **Demoted** to optional rehearsal add-on |
| 2. Edge-trust header (`X-Auth-Request-User`) | Med | N/A | Low — permanent trust seam | **High** — spoofable header; nginx does not strip client copies by default | Med | ❌ Rejected |
| 3. Pure sequencing at *object* granularity (as written) | Low | N/A | Low | **High** — Window A/B/C all survive | Low | ❌ Refuted by both councilors |

### 3.4 The three handover invariants (both councilors, independently)

1. **Loop** — whenever the app enforces, `/oauth2/*` must route to the app.
2. **Exposure** — edge paths/annotations may drop only after *every* serving pod enforces.
3. **Liveness** — the activation that mounts enforcement must mount the secret env in the same act (never a bare env flip).

### 3.5 Corrected choreography — auth posture per step

**`daoduc` (Phase 3, Helm, backup)**

| Step | Action | `/`,`/ui`,`/fe/api` | `/oauth2` |
|---|---|---|---|
| D0 | Pre-flight: `helm history` (capture live + v203 revs), `helm template` == live, stage Recreate/maxSurge-0 patch | edge-gated | edge |
| D1 | **Overlap upgrade**: `internalOAuth2.enabled=true` + `oauth2Proxy.enabled=true` + `overlap=true` → annotations STAY, `/oauth2`→app, `secretKeyRef` env renders, enforcing pods roll **behind the live edge** | edge + app-enforce | → app |
| D2 | Probe matrix (overlap column) + login rehearsal: operator holds a live edge session (P7′) and mints `llmproxy_oauth2` through the app's flow | edge + app-enforce | app |
| D3 | **Internal-only upgrade**: `overlap=false`, `oauth2Proxy.enabled=false`, `oauth2Proxy.replicas=0` → annotations drop against **already-enforcing** pods; **no pod rollout in this step** | app-enforced | app |
| D4 | Soak + full matrix; assert edge Deployment+Service still rendered (P-J) | app-enforced | app |

**`ensem` (Phase 4, raw manifests, PRIMARY — HARD HELD)** — three pre-staged applies, back-to-back

| Step | Action | `/`,`/ui`,`/fe/api` | `/oauth2` |
|---|---|---|---|
| A | HTTPRoute: repoint **`/oauth2` only** → app:4321 | edge-gated (app dormant; fresh logins degraded-but-closed) | → **app** |
| B | Apply deployment+configmap enforcing (+ Recreate patch); wait `rollout status` | **overlap**: edge + app-enforce — loop-free because the app's flow is already reachable | app |
| C | Immediately repoint `/`,`/ui`,`/fe/api` → app (ends the overlap loop structurally) | app-enforced | app |
| D | Soak + matrix; edge idled later (`replicas: 0`) | app-enforced | app |

**Rollback (both hosts, IdP-independent):** ensem R1 = revert HTTPRoute (seconds; the app may stay enforcing = the safe overlap state), R2 = revert deployment. daoduc = `helm rollback` one revision (annotations + edge return; the app stays enforcing = overlap = safe). **The app-enforcing-behind-live-edge state is itself the safe state to roll back into.**

### 3.6 Probe matrix — distinguishing gated / looping / exposed

| ID | Probe | dormant+edge | overlap | internal-only | ALARM |
|---|---|---|---|---|---|
| P-A | anon `/fe/api/models` | 302 (edge) | 302 (edge) | **401 JSON** `{"error":"unauthenticated"}`, no `Location` | **200 = EXPOSED** |
| P-B | follow redirects on `/ui` (jar, max 5) | exits to edge/IdP | terminates after one app bounce | `…/oauth2/start` → external Casdoor hop → 200 + `Set-Cookie: llmproxy_oauth2` | **≥3 same-origin 302s = LOOP** |
| P-C | anon `/` | 302 → edge signin | 302 → edge | 302 → `/oauth2/start?rd=` (same-origin, `no-store`) | 200 = exposed |
| P-D | `/ui/` + valid app cookie | 302 | 200 | 200 | 302 in overlap/internal = app cookie not honored |
| P-E | edge cookie only → `/fe/api/models` | 200 | 302 → app flow → 200 + app cookie minted (rehearsal) | 401/302 | — |
| P-F | forged `X-Auth-Request-*` / forged cookie, direct-to-Service | 401 | 401 | 401 | **200 = header-trust bypass** |
| P-G | anon `/fe/api/credentials` `/tokens` `/config` | 401 | 401 | 401 | **200 = exposed admin plane** |
| P-H | `/v1/models` 200; `/healthz` 200; `/v1/chat/completions` without token ⇒ proxy 401 | ✓ | ✓ | ✓ | any change = `/v1` affected |
| P-I | pods 1/1 Running, restartCount 0, env names present | ✓ | ✓ | ✓ | CrashLoopBackOff = Window B live |
| P-J | `kubectl get deploy oauth2-proxy` exists, replicas as intended | ✓ | ✓ (≥1) | ✓ (0) | missing = Helm pruned the rollback anchor |
| P-K | `/oauth2/start` with a valid app cookie | n/a | **302 `rd`, zero IdP hop** | 302 `rd` | IdP hop = short-circuit missing |
| P-L | dormant `/oauth2/start` | 404 (pinned) | — | — | unpinned = doc gap |

---

## 4. Blocking amendment register (🔴) — must be in the plan text before Reviewer

> Each row: finding → evidence → required amendment → target file/section.

### Area I — Cryptographic & OIDC security (`pkg/oauth2/`)

**AR-01 — `state` is not bound to the initiating browser ⇒ login-CSRF / session fixation.**
*Evidence:* `phase1-plan.md:406-414` — the state record is server-side only, keyed by the state value; no companion cookie exists in either plan (repo-wide grep for a state cookie = 0 hits); D6 (`:60`) claims CSRF is closed by state+nonce+PKCE. RFC 6749 §10.12 requires state to bind the request to the user-agent session. *Attack:* attacker runs `GET /oauth2/start` → obtains state S → authenticates at Casdoor → captures their own `?code=C&state=S` → lures the victim to `/oauth2/callback?code=C&state=S`; the server finds S, exchanges C with the **server-side** verifier, and sets the **attacker's** session cookie in the **victim's** browser. PKCE does not help (the verifier never leaves the server); nor does the nonce (the server compares its own stored nonce).
*Amendment:* at `GET /oauth2/start` set a transaction cookie `llmproxy_oauth2_txn` = 32 random bytes (base64url), `HttpOnly; Secure; SameSite=Lax; Path=/oauth2; Max-Age=600`; store `SHA-256(cookieValue)` in the state record; at callback require presence + constant-time match, then delete (single use). Add **D6a** + `TestCallback_RejectsStateFromAnotherBrowser`. `SameSite=Lax` is correct (the callback is a top-level cross-site GET). Derive a **separate HKDF subkey** for the txn cookie (stdlib `crypto/hkdf`, Go 1.26) so a txn ciphertext pasted under the session cookie name fails closed. → `phase1-plan.md` §5.6 + D6, `plan-overview.md` §5.

**AR-02 — Path classification is fail-open ⇒ every future route is public by default.**
*Evidence:* `phase1-plan.md:461-471` class 7 = "anything else → **pass**"; `:543-566` enumerates only today's routes, while `pkg/ui/server.go:193-246` registers ~25 `/fe/api/*` routes and `pkg/mcp/mcp.go:55-62` more. A Go 1.26 `ServeMux` probe confirms no traversal bypass exists today (`//fe/api/config`, `/x/../fe/api/config` → 307 to the cleaned path, then 401) — **but that is a property of the mux, not of the gate**; the gate classifies on `r.URL.Path` while the mux matches the escaped path. `GET /ui` (exact) is missing from §6.2 entirely and falls into class 7.
*Amendment:* invert the default — class 7 becomes **gated**; the pass set becomes an explicit allowlist (`/v1/`, `/healthz`, `/oauth2/`, `/fe/api/auth/me`, `GET /fe/api/version` per AR-30). Add **D2a** + a **route-census test** that enumerates every pattern registered on the mux (`pkg/ui.RegisterHandlers`, `pkg/mcp`, `cmd/main.go:409-436`) and fails if a registered route is neither allow-listed nor gated. Add a §6.2 row for `GET /ui` (exact) and state class 7 wording as "pass — the mux's own 404/307 answer is preserved". Normalize with `path.Clean` semantics before classifying. → `phase1-plan.md` §5.7, §6.2, §9.1, §9.2.

**AR-03 — No security decision may derive from request-controlled headers.**
*Evidence:* `phase1-plan.md:89` — `redirect_uri` derived per-request from `Host` + `X-Forwarded-Proto`; `:94`/`:182-184` — `CookieSecure` auto-derived from the request scheme. A client-supplied `Host:` still yields an absolute URL ⇒ the derived callback is attacker-chosen and is sent as `redirect_uri` in both the authorization request and the token exchange; the same spoofable header decides `Secure` ⇒ **the session cookie travels in cleartext** on an https deployment. nginx does not strip client-supplied `X-Forwarded-Proto` by default.
*Amendment:* make `OAUTH2_EXTERNAL_URL` (or `OAUTH2_REDIRECT_URL`) **mandatory when enabled** (fail-fast with the other required vars); delete the per-request derivation branch (`:424`); `OAUTH2_COOKIE_SECURE` defaults **true**, with `auto` only under an explicit opt-in and a documented ingress requirement; add `TestConfig_EnabledRequiresExternalURL`. → `phase1-plan.md` §4.2, §5.1, §5.6; `plan-overview.md` §4.4 (16-env table).

**AR-04 — Principal admission control is "any Casdoor account" by default.**
*Evidence:* `phase1-plan.md:96` — `OAUTH2_EMAIL_DOMAINS` default `*`; `:433` — 403 only on a missing email or a domain mismatch. Live Casdoor emits `email_verified`; today's edge gate is `--email-domain=*` ⇒ **parity, not a regression** — but the plan is the natural moment to make it explicit rather than silently permissive.
*Amendment:* `*` requires an explicit `OAUTH2_ALLOW_ANY_EMAIL_DOMAIN=true` + a startup WARN; require `email_verified == true`, and if the claim is absent ⇒ **403 (fail closed)** unless the allowlist is `*`; the domain allowlist MUST be set at cutover pre-flight. → `phase1-plan.md` §4.2, §5.6, §12.

**AR-05 — Discovery/JWKS endpoints are not pinned to the issuer origin ⇒ `client_secret` exfiltration.**
*Evidence:* `phase1-plan.md:298` validates only "absolute https"; `:295` uses a default `http.Client` (**redirects followed**); `:302` POSTs the code + `client_secret` to the token endpoint. Casdoor supports the `password` grant, so the reused secret is a live credential.
*Amendment:* every advertised endpoint MUST be same-origin (scheme+host+port) with the configured issuer, else hard error (an explicit allowlist env is the only escape hatch); `CheckRedirect` rejects any cross-origin redirect and never re-sends credentials; add `TestOIDC_RejectsCrossOriginEndpoints` + `TestOIDC_CrossOriginRedirectRejected`. → `phase1-plan.md` §5.3.

**AR-06 — Alg allowlist default is far wider than the IdP can emit.**
*Evidence:* live `https://door.daoduc.org/.well-known/openid-configuration` ⇒ `id_token_signing_alg_values_supported: [RS256, RS512, ES256, ES384, ES512]` — **no RS384, no EdDSA**; the live JWKS is `{"use":"sig","kty":"RSA","kid":"cert-built-in","alg":"RS256","n":…,"e":"AQAB","x5c":[…]}` (2048-bit, `kid` present); `casdoor/object/token_jwt.go` has **no EdDSA branch** in `getJwtSigningMethod`. The plan's default (`:97`) is `RS256,RS384,RS512,ES256,ES384,ES512,EdDSA` ⇒ every extra alg is an unexercised verifier path (ES raw-R‖S, curve binding, OKP).
*Amendment:* default **`RS256`**; log WARN when the effective set differs from the default; reject `HS*`/`none` **case-insensitively**; pin `kty↔alg↔crv` at JWK parse time (RSA⇒RS\*, EC⇒ES\* with `crv↔alg`, OKP+Ed25519⇒EdDSA) and type-assert per alg in the verifier map (never a bare cast). → `phase1-plan.md` §4.2, §5.4, §5.5, §9.1.

### Area II — IdP resilience & concurrency

**AR-07 — The single-flight primitive in the plan cannot fire twice.**
*Evidence:* `phase1-plan.md` §5.4 declares `JWKSCache.inflight *sync.Once`. A `sync.Once` fires once per **process lifetime**: after the first refresh the field is permanently spent, so every later TTL-expiry or kid-miss refresh either runs concurrently (thundering herd) or never refreshes at all.
*Amendment:* replace it with a stdlib-only, **re-armable** single-flight (mutex + waitgroup / done-channel) that resets per refresh and takes a `ctx`. Collapse TTL-expiry and kid-miss onto one constant key (`"jwks-refresh"`) so they cannot issue two fetches. → `phase1-plan.md` §5.4.

**AR-08 — Discovery has no single-flight; concurrent starts multiply IdP fetches.**
*Evidence:* §5.3 `OIDCProvider` uses `sync.RWMutex` + `lastAttempt` backoff only — the attempt is not stamped atomically with the fetch, so N concurrent `/oauth2/start` calls during an IdP outage issue N discovery fetches.
*Amendment:* single-flight around `Discover`; stamp the negative result atomically with the attempt (negative caching of a failed discovery). → `phase1-plan.md` §5.3.

**AR-09 — Token-exchange and userinfo calls have no timeout.**
*Evidence:* §5.3 pins `http.Client.Timeout = cfg.DiscoveryTimeout` for **discovery only**; no timeout exists for the `ExchangeCode` POST or the optional userinfo GET ⇒ a slow IdP holds the callback goroutine and the browser indefinitely. Precedent for the hazard: the store's periodic reconcile holds a write mutex ~90 s over an unbounded context.
*Amendment:* one shared `*http.Client` with a keep-alive transport (never a client per request) and per-request `context.WithTimeout`; add `OAUTH2_TOKEN_EXCHANGE_TIMEOUT` (default 10 s) and `OAUTH2_USERINFO_TIMEOUT` (default 5 s) to §4.2/§5.1 with validation/clamps; every IdP call must use a request-derived context — **never `context.Background()`**. → `phase1-plan.md` §4.2, §5.1, §5.3.

**AR-10 — The token-exchange retry policy is ambiguous on a single-use credential.**
*Evidence:* §5.6 step 4 says "failure ⇒ 502 token_exchange_failed" but never states retries. An `authorization_code` is single-use: a retry after a slow-but-successful first attempt burns the code and fails the user.
*Amendment:* pin **single attempt, zero retries**; surface `502 {"error":"token_exchange_failed"}` (not 504); log only `sub`-level info — never the code, token, or cookie value. → `phase1-plan.md` §5.6.

**AR-11 — The state store is unbounded and per-pod.**
*Evidence:* §5.6 `stateStore` is in-memory, TTL 10 min, sweeper-pruned, **no size cap**; `/oauth2/start` is unauthenticated ⇒ unbounded growth for the TTL window. It is also per-pod: `k8s/values.yaml` has `replicaCount: 1` and **no `strategy:`** ⇒ the default RollingUpdate runs two pods during `helm upgrade --atomic` ⇒ `/oauth2/start` on the old pod and `/oauth2/callback` on the new ⇒ `400 invalid_state` — a login that fails precisely during a rollout/soak.
*Amendment:* cap the store (max-N + eviction, 429 on overflow) and expose the count; rate-limit `/oauth2/start` per IP; then either (a) make the state **stateless** (carry state+nonce+PKCE verifier in a short-lived AAD-bound encrypted cookie — the AES-GCM codec already exists), or (b) state a normative invariant (`replicaCount` MUST stay 1 in internal mode; scale-out REQUIRES a shared state store) and set `strategy: Recreate` / `maxSurge: 0` for internal mode; add a §12 risk row. → `phase1-plan.md` §5.6, §8.3, §12.

**AR-12 — `rd` sanitization permits an infinite login loop.**
*Evidence:* D10 + §5.6 sanitize `rd`; the §9.1 table covers `//evil.com`, `/\evil.com`, `https://evil.com`, `javascript:…`, `""` ⇒ `/ui/`. A **path-relative** `rd=/oauth2/start` passes every rule ⇒ callback → 302 → `/oauth2/start` → fresh state → IdP → callback ⇒ **infinite loop** (each pass mints a new state; nothing breaks the cycle).
*Amendment:* reject `/oauth2/*` and `/fe/api/auth/me` ⇒ fall back to `/ui/`; add table cases `"/oauth2/start"`, `"/oauth2/callback"`, `"/fe/api/auth/me"`. → `phase1-plan.md` §5.6, §9.1.

### Area III — Composition with v203 authz & the dormant guarantee

**AR-13 — Gating `/fe/api/version` breaks the house deploy gate G4.**
*Evidence:* `phase1-plan.md:547` gates `GET /fe/api/version` (401 JSON) and `:466-467` has **no exemption** for it (unlike `/fe/api/auth/me`). House runbook gate **G4** = `curl -s 127.0.0.1:4321/fe/api/version` must equal `{"version":"<EXPECTED_RUN>"}` (runbook `:312`, "the decisive identity check" at `:511`), and `phase2-switch-plan.md:152`/`:447` instruct running the full G0–G10 gate **post-switch**. The probe is **pod-direct**, so the in-binary gate applies — unlike today, where only the edge is gated. §9.5-2 never asserts `/fe/api/version`, so the failing gate is untested.
*Amendment:* exempt `GET /fe/api/version` (exact) when enabled — a new class 4b alongside `/fe/api/auth/me`. Record the posture delta explicitly (the build number becomes visible in internal mode; it is already public via image tags and the public repo). → `phase1-plan.md` §5.7 (class 4b), §6.2, §9.1, §9.5, `plan-overview.md` §4.2 + §5 (D11 addendum), `phase2-switch-plan.md` §3.4/§4.4.

**AR-14 — Phase 4 acceptance E11 expects the wrong status, and cannot detect a lockout.**
*Evidence:* `phase2-switch-plan.md:459` (E11) and `:495` (post-rollback re-check) expect **302** — today's edge behavior. The internal gate answers **401 `{"error":"unauthenticated"}`** by design (`phase1-plan.md:527`, D11). A redirect loop also produces 302 ⇒ the probe passes for a broken build and fails for a correct one.
*Amendment:* E11 must assert **401** (the intent — "not 200" — is preserved and the status becomes the stricter proof); add **E11b** loop detector (≥3 same-origin 302s on `/ui` = LOOP) and **E12** short-circuit probe; add the missing no-exposure check to the daoduc checklist §3.4 (S1–S8 has no E11 equivalent — asymmetry). → `phase2-switch-plan.md` §3.4, §4.6, §4.7.

### Area IV — Frontend & single-image coupling

**AR-15 — The SSE auth probe can leave the EventLog permanently silent.**
*Evidence:* `phase1-plan.md:753-773` — `void probeAuth().then(...)`: **no timeout, no `.catch()`**; `probeAuth()` = `fetch('/fe/api/auth/me')` (`:776`). But §9.3 (`useEvents.auth.test.ts`) asserts that a failed probe keeps the existing backoff reconnect. The snippet cannot satisfy it: a rejected or hung probe never reaches `scheduleReconnect()`, bypassing the backoff at `useEvents.ts:54-78`. `EventSource` exposes no status (a 401 looks exactly like a drop) ⇒ the stream dies until a manual reload.
*Amendment:* make `probeAuth()` a **bounded** probe (`AbortController` + 3 s timeout, mirroring §7.3's `authState.isEnabled()` budget); chain `.then(onAuth).catch(() => scheduleReconnect())` so a failure/timeout resumes the existing backoff; add `TestSSE_ProbeTimeoutKeepsBackoff` to §9.3; the SSE redirect must share the **same one-shot latch** as `guardedFetch` (the plan says "shared helper" but never states the latch is shared ⇒ double navigation). → `phase1-plan.md` §7.5, §9.3.

**AR-16 — A probe timeout cached as `false` kills the 401→redirect path for the tab.**
*Evidence:* `phase1-plan.md:709` — `authState.isEnabled()` "awaits it **with a 3 s timeout**; on timeout it resolves `false`", with one cached `enabledPromise`. Every later 401 awaits the same already-timed-out promise ⇒ `false` ⇒ **no redirect, ever**.
*Amendment:* on timeout, null the cached promise (re-probe on the next 401) and cache only an **explicit** answer; never render "signed out" on a timeout. → `phase1-plan.md` §7.3.

### Area V — Helm chart & cutover architecture

**AR-17 — Phase 3's diff does not switch the chart (Window B) and uses the wrong env vocabulary.**
*Evidence:* `phase2-switch-plan.md:102` claims the diff "touches only the `env:` block"; `:112` flips `OAUTH2_ENABLED`. But the chart's internal mode is **toggle-driven** (`phase1-plan.md:873-876`): `deployment.yaml` emits the `OAUTH2_*` env only when `internalOAuth2.enabled`; `ingress-protected.yaml` drops the auth annotations only when `internalOAuth2.enabled`; `ingress-public.yaml` repoints `/oauth2` only when `internalOAuth2.enabled`. `values-pinned.yaml` today has **no** `oauth2Proxy:`/`internalOAuth2:` blocks ⇒ chart defaults apply ⇒ the render keeps the nginx annotations **and** the `/oauth2 → oauth2-proxy:4180` backend (`k8s/templates/ingress-public.yaml:29-35`), while `env.OAUTH2_ENABLED=true` still reaches the pod through the generic `.Values.env` loop (`k8s/templates/deployment.yaml:53-58`) ⇒ no `OAUTH2_CLIENT_ID` ⇒ **fail-fast ⇒ CrashLoopBackOff ⇒ the backup's `/v1/*` dies**. Separately, the phase-2 env names (`OAUTH2_PROVIDER`, `OAUTH2_EMAIL_DOMAIN` singular, `OAUTH2_COOKIE_EXPIRE`, `OAUTH2_SKIP_PROVIDER_BUTTON` at `:112-117`/`:257-264`) **do not exist** in phase 1's authoritative table (`:83-101`), and phase 2 omits `OAUTH2_CLIENT_ID`/`OAUTH2_CLIENT_SECRET`/`OAUTH2_SESSION_TTL`/`OAUTH2_EMAIL_DOMAINS`; the ensem diff mounts the secret as `CLIENT_ID`/`CLIENT_SECRET`/`COOKIE_SECRET` (`:330-345`) while the binary reads `OAUTH2_CLIENT_ID` (no fallback) — only `OAUTH2_COOKIE_SECRET`→`COOKIE_SECRET` is covered.
*Amendment:* rewrite §3.2 to the **overlap-state** values per §3.5 (`internalOAuth2.enabled=true` + `oauth2Proxy.enabled=true` + `overlap=true` + issuer/external URL + the secretKeyRef block) — **never a bare `env.OAUTH2_ENABLED` flip**; reconcile the env vocabulary to §4.2 (single source of truth) and delete the drifted names; add a **static pre-flight validator** (rendered env ⊇ required set) so a missing key is caught before any apply; make §8.2's client-id/secret fields `secretKeyRef` sources (else the client id is duplicated into the GitOps values file). → `phase2-switch-plan.md` §3.2, §4.2, `phase1-plan.md` §4.2, §8.2.

**AR-18 — The daoduc single-upgrade step creates an unauthenticated admin-plane window (Window A).**
*Evidence:* `phase2-switch-plan.md:135-142` flips env + ingress in one `helm upgrade --atomic`. Helm applies Ingress **after** Deployment, but the nginx controller reloads the dropped annotations in **seconds** while the pod rollout takes **tens of seconds** — the old, **un-gated** pod keeps serving ⇒ `/`, `/ui`, `/fe/api/*` are reachable unauthenticated for the rollout duration. `--atomic` rolls back only on upgrade failure; `--wait` waits after all objects are applied. The plan states no atomicity argument at all.
*Amendment:* adopt the two-upgrade choreography of §3.5 (D1 overlap → D2 verify → D3 edge-off against **already-enforcing** pods, with **no pod rollout in D3**); re-specify §3.6's rollback tiers per step (R1 currently assumes "ingress unchanged", which is false after the split). → `phase2-switch-plan.md` §3.3, §3.6.

**AR-19 — The ensem Step 1→2 gap is a deterministic redirect lockout (Window C), and §4.7 R1's rationale is false.**
*Evidence:* `phase2-switch-plan.md:417` calls the window "a brief, non-exposing transition"; `:438` says oauth2-proxy "may 404 or redirect oddly". In fact the app's guard is inside the binary (`phase1-plan.md:522-530`) and runs on **every request that reaches the app**, so behind oauth2-proxy the app still demands its own `llmproxy_oauth2` cookie → 302 → `/oauth2/start` → the edge's `/oauth2` rule still points at oauth2-proxy (HTTPRoute `:19-26`) → oauth2-proxy redirects back to `rd` ⇒ **infinite loop** on the primary's operator UI, operator-paced ⇒ unbounded. §4.7 R1's parenthetical "oauth2-proxy in front ignores the proxy's internal handler" is **false** — R1 alone converts "app-gated and working" into "app-gated behind the edge" = the loop.
*Amendment:* adopt the per-path choreography of §3.5 (A: repoint **`/oauth2` only** → app; B: deploy+enforce → overlap, loop-free; C: repoint `/`,`/ui`,`/fe/api` → app; D: soak); **delete the false R1 parenthetical** and bind R1+R2 into **one scripted rollback unit** (P7 lane) — because "repointed HTTPRoute + rolled-back deployment" = unauthenticated exposure, which is the reason the order is HTTPRoute-first; replace the post-rollback E11 re-check with a session-bearing round-trip. → `phase2-switch-plan.md` §4.4, §4.5, §4.7.

**AR-20 — The cutover would deploy the stale main checkout's chart.**
*Evidence:* `phase2-switch-plan.md:132` sets `CHART=/home/nea/Code/opensource-projects/llm-supervisor-proxy/k8s` — that checkout is at `5ce0ead` (master), not the worktree base `92c4234` and not v203 `df12c62`; it contains **no** internal-OAuth2 chart support ⇒ the "switch" renders as today + one env var ⇒ AR-17's crash-loop. The runbook's chart-cleanliness gate (`git status --short -- k8s/` MUST be empty, runbook `:176-180`) is **not** in §3.1's pre-flight.
*Amendment:* pin `CHART` to a **clean checkout at the merged Phase-1 commit** and state how it is obtained; add the runbook §1.3 cleanliness check + a chart-commit-identity assertion to §3.1. → `phase2-switch-plan.md` §3.1, §3.3.

**AR-21 — Helm prunes the oauth2-proxy objects ⇒ OQ-7's "idle, don't delete" rollback anchor is impossible.**
*Evidence:* the chart renders the oauth2-proxy **Deployment and Service in one file** (`k8s/templates/oauth2-proxy-deployment.yaml:1-47`) — no orphaned Service lingers (good), but when §8.3 gates the file off, `helm upgrade` **deletes both** (Helm 3 prunes release-managed objects). That breaks OQ-7 / `phase2-switch-plan.md:165` ("do not delete … preserve the fastest rollback path"), breaks M4 (`:506` "verify via oauth2-proxy pod logs" — the pod is gone), and converts the rollback from scale-up to re-create.
*Amendment:* split `oauth2Proxy.enabled` (in-path) from **`oauth2Proxy.replicas`** (object existence): idle = rendered at `replicas: 0`, objects never pruned; redefine D15 from "both layers enabled" to the three-state model (§3.2) — "internal ON ⇒ oauth2-proxy not serving (replicas 0) **and** edge annotations absent"; §3.5/§3.7-T3.5/§5.1-M4 define idle per host (ensem: `kubectl scale` — the Service keeps 0 endpoints, harmless; daoduc: `replicas: 0` via the chart). → `phase1-plan.md` §8.2, §8.3, `phase2-switch-plan.md` §3.5, §3.7, §5.1.

**AR-22 — D15's mutual-exclusion guard must become a three-state model (council).**
*Evidence:* D15 (`plan-overview.md:137`) forbids both layers simultaneously; §8.3 implements it as a render-time `fail`. Under the council's choreography the **overlap state is designed**, not a failure — and the council's §3.2/§3.5 requires an `overlap=true` render state (annotations kept **and** app env rendered **and** `/oauth2`→app).
*Amendment:* replace the binary guard with three render states — **edge-only** (default) / **overlap** / **internal-only** — each with its own render assertions; keep a `fail` for genuinely contradictory input (both `internalOAuth2.enabled` and `oauth2Proxy.enabled` **without** `overlap=true`); §12/R12 is rewritten from "both auth layers active = high risk" to "the overlap state is a designed, probe-gated transition". → `phase1-plan.md` §3 (D15), §8.3, §8.4, §12; `plan-overview.md` §5, §9.

---

## 5. Significant amendments (🟡) — grouped, with target sections

| # | Finding (evidence) | Required amendment | Target |
|---|---|---|---|
| AR-23 | Logout end-session redirect sends the browser to a dead IdP during an outage (B-5) | best-effort: clear the local cookie first, skip the end-session hop when discovery metadata is stale/unavailable, fall back to 302 `/ui/` | §5.6 |
| AR-24 | `OAUTH2_DISCOVERY_TIMEOUT` has no default or clamp (B-7) | default 5 s, clamp [1 s, 30 s], validated in `ConfigFromEnv` | §4.2, §5.1 |
| AR-25 | Kid-miss waiter semantics unspecified; TTL-refresh and kid-miss can double-fetch (B-8, B-11) | triggering + concurrent waiters block on the in-flight refresh up to the request deadline; only rate-limited (too-soon) misses fail fast with 503 + `Retry-After`; one constant single-flight key | §5.4 |
| AR-26 | Grace expiry and key-rotation semantics unstated (B-9, B-10) | document: the 2×TTL grace covers **fetch failures**, not key removal — a successful refresh that drops a rotated-away kid invalidates old id_tokens by design; make the grace a knob | §4.4, §5.4, §12 |
| AR-27 | No offline session-continuity commitment (B-12) | invariant: cookie validation is fully offline (AES-GCM + exp); the IdP is touched **only** at `/oauth2/start`, `/oauth2/callback`, and best-effort `/oauth2/logout`; no group-claim refresh mid-session | §5.7 |
| AR-28 | Cutover verification silently depends on Casdoor (B-13) | document: a Casdoor outage during soak fails verification but is not a code defect; rollback changes the **auth layer**, not the IdP dependency; existing internal sessions stay valid; new logins die; `/v1/*` unaffected | §3.4, §4.5 |
| AR-29 | `/oauth2/start` unauthenticated with real first-use cost (B-14) | per-IP rate limit (or rely on the state-store cap) + explicit assumption statement | §5.6 |
| AR-30 | `azp` never validated; `aud` rule too loose (A-Y2) | `azp` (when present) MUST equal the client id; `len(aud)>1` ⇒ `azp == client_id`; single-audience ⇒ `aud[0] == client_id` (OIDC Core §3.1.3.7) | §5.5 |
| AR-31 | ID token vs access token not distinguished (A-Y3) | add `TokenType` to `Claims`; require `"id-token"` when present (Casdoor mints them separately — RFC 8725 §3.12) | §5.5 |
| AR-32 | The cookie key is shared with the edge layer; derivation ladder misses base64url (A-Y6) | dedicated `OAUTH2_COOKIE_SECRET` (already preferred by §4.2:91) so the internal key ≠ the ingress session key; add `base64.URLEncoding` + `RawURLEncoding` to the ladder with an explicit mode table; passphrase branch requires ≥16 bytes | §4.3, §5.1 |
| AR-33 | Cookie budget checked on **open**, not seal (A-Y7) | enforce at seal time; per-claim caps (`sub`≤64, `email`≤254, display ≤128 — truncate display fields, reject on sub/email); check the **encoded** value against 4096; drop `preferred_username` (Casdoor's default token format does not emit it) | §5.2, D9 |
| AR-34 | `iss` normalized on one side only (A-Y4) | normalize **both** sides identically; the mismatch error prints both issuers and says "issuer mismatch" | §5.3 |
| AR-35 | userinfo claims not bound to the verified id_token (A-Y10) | require `userinfo.sub == id_token.sub` before accepting any userinfo claim (else 403); cap the body (64 KiB); prefer the id_token's email | §5.3 |
| AR-36 | Logout is CSRF-able; redirect target unvalidated; `OAUTH2_LOGOUT_REDIRECT` is undefined in §4.2/§5.1 (A-Y9) | POST-only logout (GET ⇒ 405) or a CSRF token; `post_logout_redirect_uri` pinned to a fixed validated path under `OAUTH2_EXTERNAL_URL`; add the env knob consistently; document that the Casdoor SSO session survives a local logout | §4.2, §5.1, §5.6 |
| AR-37 | Deferred bearer mode is unpinned (A-Y11) | add **D16**: a future `/fe/api/*` bearer mode MUST use a **different audience** (e.g. `aud="llmproxy-admin-api"`), never be accepted on `/v1/*`, never accept an `sk-` token, answer 401 with `WWW-Authenticate: Bearer realm="llmproxy-admin", error="invalid_token"` + the bare `{"error":…}` body, TTL ≤15 min, and never be written into `auth.AuthToken` | `plan-overview.md` §5 |
| AR-38 | Phase 1 attribution boundary unstated (C-F10) | record: Phase 1 is **authentication only** — the proxy does **not** populate `X-User`; `created_by` remains the literal `"api"`; identity is exposed via `/fe/api/auth/me` + request context only. `X-User` has **no producer** in the repo today (`pkg/ui/server.go:2722-2725` is the sole consumer) — populating a client header would let anyone mint `created_by:"admin"` | `plan-overview.md` §2, D14 |
| AR-39 | Log-surface contradiction (C-F11) | one authoritative statement: stdout only — `mode=`/`secret_mode=` at boot; `sub` + email **domain** at INFO per login; never id_token/access_token/code/cookie value/secret value; **no OAuth2 event is published to the events bus** (so `/fe/api/events` carries only proxy telemetry) — reconcile §4.3 with §5.6 | §4.3, §4.4, §5.6 |
| AR-40 | §6.2 completeness gaps (C-F12, F13, F14, F15) | add the `GET /ui` (exact) row; mark `/fe/api/mcp-servers*` rows **conditional** on `MCP_ENABLED` (otherwise 404, not 401); fix the `/fe/api/models*` citation (`:196-199`, incl. `models/test`); add a "§5.7 class ⇄ §6.2 row" cross-reference + one anti-drift assertion | §6.2, §9.1 |
| AR-41 | Dormant delta wording (C-F17) | "delta = one constant-JSON route + **one probe request per page load**" (the SPA calls `/fe/api/auth/me` on every load) | §9.5, `plan-overview.md` §5 D1 |
| AR-42 | Redirect latch never resets in production (C-F5) | latch cooldown (re-arm after 5–10 s) or bound it to N attempts, then `location.reload()`; state the reset semantics (reload = new module state; same-tab retry = cooldown) | §7.3 |
| AR-43 | The dormant/edge world premise is wrong (C-F6) | today the edge answers **302** → cross-origin redirect → `fetch()` rejects with a TypeError (**not** a 401) ⇒ no dormant recovery exists; correct §7.3's comment and add a one-shot `location.reload()` on a fetch rejection / non-JSON HTML response so the edge's 302→IdP can take over — this is what keeps the SPA usable on the still-edged host (ensem stays edged until Phase 4) | §7.3 |
| AR-44 | Envoy `/oauth2` rule has no timeout (D-A12) | the Gateway default (15 s) vs lazy discovery (≤ timeout) + token exchange ⇒ plausible 504 on first login; add `timeouts: request: 60s` to the `/oauth2` rule | `phase2-switch-plan.md` §4.4 |
| AR-45 | HTTPRoute apply has no acceptance check (D-A13) | add an accepted-status check + per-path probes + the failure action (revert the HTTPRoute); note `/v1` is a separate rule ⇒ the ensemble lane survives | `phase2-switch-plan.md` §4.5, §4.6 |
| AR-46 | Rollback operand never captured (D-A14) | §3.1 must capture **both** the live revision and the v203 revision into the evidence block (§3.6 R3 references the v203 rev) | `phase2-switch-plan.md` §3.1 |
| AR-47 | GitOps pin order unspecified (D-A15) | explicit order — daoduc pin first, then primary, one commit per host, `main` untouched; pins ride along with the deploy (standing grant 2026-11-17) | `phase2-switch-plan.md` §5.2 |
| AR-48 | Render guard weak in internal mode (D-A16) | `required` the issuer/client values when `internalOAuth2.enabled`; H6 (byte-parity) must demand an **empty** diff — every parameterised literal equals today's hardcoded value | `phase1-plan.md` §8.3, §8.4 |
| AR-49 | Cloudflare premise + masking (D-A18) | Cloudflare fronts **llm.daoduc.org** (not llm.ensem.dev); the tunnel masks origin 5xx bodies as a 16-byte `error code: 5xx` ⇒ the `502 {"error":"oidc_discovery_unavailable"}` path is masked ⇒ debug at/below nginx | `phase2-switch-plan.md` §3.4 |
| AR-50 | `gate-ensem.sh` does not exist on disk (D-A11) | drop E9's reference or replace it; T3.4 must enumerate the probe matrix (path → expected status pre-switch → post-switch) and name the owner/repo/path | `phase2-switch-plan.md` §3.7, §4.6 |
| AR-51 | Runbook citation path ambiguous (D-A17) | give the absolute path (the runbook lives in **data-center-scripts**, not the proxy repo) | `plan-overview.md` §11 |
| AR-52 | Overlap-state operator precondition (council residual) | precondition **P7′** — the operator holds a live edge session during the overlap; schedule in a low-traffic window with a user notice; users **without** a live edge session who attempt a fresh login during the overlap get a fail-closed ping-pong that terminates only when the edge leaves the path | `phase2-switch-plan.md` §1.4, §2, §3.4, §4.5 |
| AR-53 | Pod straddle during the overlap (council residual) | `strategy: Recreate` / `maxSurge: 0` in internal mode; logins only after `rollout status` completes | `phase1-plan.md` §8.3 |
| AR-54 | `/oauth2/start` valid-session short-circuit missing (council) | a valid `llmproxy_oauth2` ⇒ 302 `rd` immediately — no state, no IdP hop (kills the overlap ping-pong's Casdoor round-trips) | `phase1-plan.md` §5.6, §9.1 |

---

## 6. Improvements (🟢) — fold into the same edits

- **AR-55** — JOSE hygiene: reject a `crit` header and `b64:false` (RFC 7515 §4.1.11/§5.2); the deferred bearer mode must accept `typ:"at+jwt"` (RFC 9068). (§5.5)
- **AR-56** — Rewrite the ES256 cell normatively: signature length = exactly 2×keyBytes (64/96/132 for P-256/384/521), `r = sig[:n]`, `s = sig[n:]`, verify with `ecdsa.Verify(pub, hash, r, s)`; **DER must be rejected** (the §9.1 test already asserts this — make the table agree; the current cell is self-contradictory). (§5.5)
- **AR-57** — Pin `JWK.alg` (when present) MUST equal the token header alg; `key_ops` (if present) must contain `verify`; the verifier map must type-assert per alg so a mismatched key type is a hard error, never a cast. (§5.4, §5.5)
- **AR-58** — §6.1 rationale 2 overstates the gzip risk: `pkg/middleware/gzipmw/gzip.go:39` already caps decompression. Cite the existing cap; gate-outside remains correct. (§6.1)
- **AR-59** — Add `TestMiddleware_UncleanPathCannotBypassGate` (Go 1.26 301-redirects unclean paths — `net/http/server.go:2689`). (§9.1)
- **AR-60** — The FE census must cover more than `fetch(`: pin `EventSource|WebSocket|XMLHttpRequest|sendBeacon|axios|navigator.serviceWorker` (today: zero hits outside `useEvents.ts`); note that `<img>/<script>` subresources under `/ui/*` 302 on session expiry (silent subresource failures). (§7.4, §9.3)
- **AR-61** — `pkg/mcp` status/test endpoints are commented "unprotected" but are still operator-gated (edge today, internal gate later) — record the intent in §6.2. (§6.2)
- **AR-62** — Update §15/§16 research assumptions: (a) **resolved** — the discovery doc exists, `jwks_uri` = `/.well-known/jwks` (**not** `jwks.json`); (b) **resolved** — JWKS `kid:"cert-built-in"`, `alg:"RS256"`, `use:"sig"`, `n`/`e` present ⇒ the kid-lookup and "JWK alg ∈ allowlist" rules hold; (c) **partly wrong** — `email` + `email_verified` are present, `preferred_username` is **absent**; the app's `TokenSigningMethod` must be read at commissioning (the JWKS `alg` is the authority). (§16)
- **AR-63** — Confirm the mode-detection ladder is deterministic across std-base64 / base64url / raw-24B, and log only the mode string. (§4.3)
- **AR-64** — Record the `app-root:/ui/` behavior in internal mode (one nginx short-circuit redirect before the upstream) as benign. (§8.3)
- **AR-65** — State the single-replica/state-store placement limitation explicitly in §5.6. (§5.6)
- **AR-66** — Keep the existing SPA build determinism proof (T14) and the embedded-`pkg/ui/static` rebuild as the single-image coupling gate. (§7.7)

---

## 7. Cross-cutting rulings (architect decisions)

| # | Ruling | Rationale | Corroboration |
|---|---|---|---|
| R-1 | **`GET /fe/api/version` is exempted when the internal gate is enabled** | the house deploy gate G4 is a pod-direct probe of that exact path; the build number is already public via image tags and the public repo; gating it breaks the release gate for no security gain | C-F1 + D-A6 + runbook `:312` |
| R-2 | **E11 asserts 401 JSON, not 302** | 401 is the stricter proof of "not exposed"; a redirect loop also yields 302, so 302 cannot detect a lockout | C-F2 + D-A10 + D11 |
| R-3 | **Phase 1 authenticates only; it does not stamp write attribution** | `X-User` has no producer in the repo; `created_by` is always `"api"` today. Populating a client-supplied header would let anyone mint `created_by:"admin"` — an existing hole that this feature must not widen | C-F10 + §2 non-goals |
| R-4 | **The path classification defaults to gated (fail-closed) with an explicit pass allowlist + route-census test** | a security gate whose default is "public" makes every future route public by default; the allowlist is small, explicit, and machine-checked | A-C2 + C-F12 |
| R-5 | **`OAUTH2_EXTERNAL_URL` is mandatory when enabled; no request-derived security decisions** | `Host`/`X-Forwarded-Proto` are client-spoofable; the same header decides `Secure` ⇒ cleartext session cookies | A-C4 + C-F7 |
| R-6 | **`state` binds to the browser via a txn cookie + HKDF subkey** | server-side-only state is login-CSRF/session fixation; PKCE cannot substitute because the verifier never leaves the server | A-C1 |
| R-7 | **The interlock is per-path `/oauth2`-first with a three-state chart; OQ-5 stands** | it satisfies all three handover invariants with **zero new trust surface** and no new application mode; the bridge is probe-gated, edge-trust widens the trust boundary, object-granularity sequencing leaves all three windows open | council consensus + D-A1..A5 |
| R-8 | **The state store must be capped; and either stateless or `strategy: Recreate` + a single-replica invariant** | unauthenticated growth + a login that dies mid-rollout are both unacceptable | B-6 + C-F9 + council |
| R-9 | **The cookie key is dedicated to the internal layer (`OAUTH2_COOKIE_SECRET`) and separated by HKDF subkeys per cookie purpose** | one leak of the shared edge secret would compromise both layers; name-AAD alone is too thin a boundary for session vs transaction cookies | A-Y6 + A-G6 |

---

## 8. Risks after amendment

| Sev | Risk | Mitigation (post-amendment) |
|---|---|---|
| 🔴 | **Three-state chart template logic** — a wrong conditional silently re-opens Window A (annotations dropped before pods enforce) | byte-level render assertions for all three states (§8.4 H-series) + P-I/P-J post-upgrade probes; the chart is the riskiest artifact in the change |
| 🔴 | **Residual fail-open gap** — any route registered outside the census test | route-census test enumerating the live mux; fail-closed default (R-4) |
| 🟡 | **Overlap-state ping-pong** for users without a live edge session (fail-closed, self-terminating only when the edge leaves the path) | minutes-short overlap, low-traffic window, user notice, `/oauth2/start` short-circuit (AR-54), operator holds both cookies (P7′) |
| 🟡 | **Pod straddle** — in-memory state + `/oauth2`→app during a roll ⇒ dead-end (404) or `400 invalid_state` | `strategy: Recreate`/`maxSurge: 0`; logins only after `rollout status` |
| 🟡 | **Env-name drift** (silent no-op email-domain filter) | reconcile §3.2 to §4.2; static pre-flight validator (AR-17) |
| 🟡 | **IdP outage during soak** — verification fails for environmental reasons | documented (AR-28); rollback is IdP-independent |
| 🟡 | **First real exercise of the app's auth-code flow is the enforcement flip** | daoduc-first (identical flow code) + P-E rehearsal + shadow mode as an optional add-on |
| 🟡 | **Stale-citation risk** — this document's line numbers were read by reviewers in the worktree at `92c4234` | implementers re-read at the pinned commit (the plan's own R17) |

---

## 9. Decisions pending (operator / Emperor)

1. **Principal admission at cutover** — keep `OAUTH2_EMAIL_DOMAINS=*` (edge parity, zero lockout risk) or tighten to the operator's domain and require `email_verified`? *Recommendation: tighten at cutover on daoduc (proving posture), keep `*` on ensem until a soak proves no operator account is excluded.* The plan must state which, per host (AR-04).
2. **`/fe/api/version` exemption** — confirmed by this review as **exempt** (R-1). If the operator prefers zero public surface, every version-based probe must be re-baselined onto an authenticated path — that is a strictly more fragile gate design; not recommended.
3. **Interlock mechanism** — council consensus recommends per-path `/oauth2`-first with a three-state chart (R-7). Adopting the runner-up (session bridge) requires the operator to accept (a) re-scoping OQ-5, (b) a transition-time hard dependency on edge-pod availability, and (c) two currently-unverified probes (ensem upstream cookie forwarding, daoduc `--set-xauthrequest`).
4. **`ensem` Phase 4 scheduling** — the plan already holds it; this review adds that Phase 4's irreversible step is now a **single HTTPRoute apply** (pre-staged and diffed), which should be executed by an independent shell lane with the operator holding a live session (P7′).

---

## 10. Open questions (2, with my decision under each)

1. **Is `email_verified` guaranteed true for every operator account on both Casdoor instances?** If some operator accounts are unverified, AR-04's rule must relax to "allowlist match permitted on an unverified email" for those accounts. *Under "yes":* enforce `email_verified` strictly from Phase 1. *Under "no":* keep the strict rule but require the allowlist to be set before any cutover, and log a WARN on an unverified-but-allowlisted login rather than 403-ing. Either way the answer is needed **before Phase 3, not before Phase 1** (Phase 1 ships dormant).
2. **Does the operator accept a bounded, fail-closed overlap window (minutes) on the primary, or is the ping-pong risk on the primary unacceptable in any form?** *Under "accept":* the council's choreography ships as designed, with the overlap kept short, scheduled in a low-traffic window, with a user notice. *Under "no":* the primary must not be switched until the shadow-mode rehearsal add-on (council's demoted Option 1) is also implemented, so the app's flow is proven before any overlap — which adds a Phase-1 mode and its own trust source.

---

## 11. Verification plan (house gates + the new assertions)

**Existing (unchanged):** Pack A / Pack B (re-measure the package count — never copy the number), `make test`, `go build ./cmd`, `go vet ./...`, gofmt zero-new-drift (flagged-line-set diff, not hunk overlap), `HOME`-isolated test runs + real-home `config.json` hash unchanged, boundary diff (`pkg/config`, `pkg/auth`, `pkg/proxy` = 0 bytes), `go.mod`/`go.sum` zero-diff, anti-vacuity census (every added `func Test` shows a literal `--- PASS`), tsc baseline parity, SPA build determinism proof.

**New assertions required by this review:**

- **Crypto/OIDC:** `TestCallback_RejectsStateFromAnotherBrowser`; `TestConfig_EnabledRequiresExternalURL`; `TestOIDC_RejectsCrossOriginEndpoints`; `TestOIDC_CrossOriginRedirectRejected`; alg-confusion (HS256 signed with the RSA public key); tampered cookie / AAD mismatch; ES256 raw-vs-DER; `TestMiddleware_UncleanPathCannotBypassGate`; `TestMiddleware_VersionIsExempt`; route-census test (every registered mux pattern is allow-listed or gated).
- **Resilience:** kid-throttle + stale-grace tests; discovery single-flight (concurrent starts ⇒ one fetch); token-exchange **zero-retry** test; state-store cap + 429 overflow; `TestSSE_ProbeTimeoutKeepsBackoff`; probe-timeout-not-cached test.
- **Chart:** `helm template` matrix over **all three states** (edge-only = **empty diff** vs the pre-change chart; overlap = annotations present **and** app env present **and** `/oauth2`→app; internal-only = annotations absent + edge rendered at `replicas: 0`); `fail` on contradictory input.
- **Cutover:** probe matrix P-A…P-L with an explicit **LOOP** and **EXPOSED** detector; P-J asserts the edge Deployment still exists after the edge is idled.

---

## 12. Gaps & unverified (disclosed)

- **ensem's live `oauth2-proxy` pod spec** was inferred from manifests, not observed (upstream-mode detail matters only to the bridge runner-up).
- **The live ingress-nginx `auth-signin` 302 chain** and Envoy/oauth2-proxy redirect semantics were reasoned from the chart annotations and the plan's topology notes, not observed on the wire.
- **`gate-ensem.sh` does not exist on disk** — the plan's E9 reference is unresolvable; AR-50 replaces it with a probe matrix.
- **Casdoor-side commissioning checks** (S256 acceptance, exact-match redirect-URI allowlist, the app's `TokenSigningMethod`) remain commissioning-time verifications — the plan already lists them (§16/§15); AR-04 depends on the allowlist being exact.
- **No `pkg/oauth2` code exists yet** — every gate/middleware/handler conclusion is read against the plan text plus the existing code.

---

## 13. Handoff

1. **Fold the 🔴 register (§4) into `phase1-plan.md` and `phase2-switch-plan.md` first** — AR-01…AR-22. The 🟡/🟢 registers (§5–§6) are strongly recommended in the same edit pass; each is a table row, not a redesign.
2. **Re-baseline the §3.2/§4.2 env vocabulary** as a single source of truth before any cutover.
3. **Adopt ADR-001** (three-state chart + per-path `/oauth2`-first) and rewrite phase 2 around it; do not execute Phase 3/4 against the current text.
4. **Re-review** the amended plan (the Reviewer stage) with this document attached — the amendment set is explicit enough to verify mechanically.
