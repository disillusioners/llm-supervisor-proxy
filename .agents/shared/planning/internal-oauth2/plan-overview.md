# Internal OAuth2 (Dormant by Default) — Plan Overview

**Feature:** `internal-oauth2` — in-binary OAuth2 **authentication** (level only; no roles/authorization) that can eventually replace the edge `oauth2-proxy` + Casdoor gate on `llm-supervisor-proxy`'s operator surfaces.
**Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-internal-oauth2` (branch `feature/internal-oauth2`, base `origin/latest` `92c4234`, tree parity with `origin/master` `df12c62`, v203 live on both hosts)
**Data-center-scripts:** `/home/nea/Code/data-center-scripts` — **read-only**; secret VALUES never read or echoed (paths + key names only)
**Date:** 2026-11-19 · **Status:** Architect-amended (22 🔴) · Reviewer-approved-with-notes · Approver iterations 001+002 folds applied · **iteration 003 (FINAL) folds applied — 5/5 blockers + notes, cross-doc sweep clean**
**Companion documents (authoritative detail):**
- [`phase1-plan.md`](./phase1-plan.md) — implementation plan (T1–T16), invariants D1–D17, env spec, package design (incl. `pkg/identity` neutral leaf), test matrix, §17 AR-register cross-reference appendix
- [`phase2-switch-plan.md`](./phase2-switch-plan.md) — per-host cutover rewritten around **ADR-001 Approach 5** (per-path `/oauth2`-first + three-state chart); P1–P5 executable-defect folds applied
- [`architecture-recommendation.md`](./architecture-recommendation.md) — the review itself (AR-01…AR-66, probe matrix P-A…P-L, rulings R-1…R-9)
- [`architecture-decision-record.md`](./architecture-decision-record.md) — ADR-001: the interlock decision, invariants I-1/I-2/I-3, options, consequences

---

## 1. Goal & Definition of Done

**Goal.** Add an internal OAuth2/OIDC authentication layer to the proxy binary that is **dormant by default** — inert unless `OAUTH2_ENABLED=true` — so that a later, separately-gated cutover can retire the edge `oauth2-proxy` without a second image ship, and without any change to `/v1/*` token authentication or the v203 authorization model.

**Definition of done (Phase 1):**

1. `pkg/oauth2/` exists, stdlib-only, implementing the auth-code + PKCE + **browser-bound state** + JWKS/JWT verification path and a fail-closed middleware `Guard`.
2. `cmd/main.go` installs the gate **only** when enabled; dormant boot is behaviourally identical to today (plus one constant-JSON route `/fe/api/auth/me` and one probe request per SPA page load — AR-41).
3. Identity flows through a **neutral leaf package `pkg/identity`** (`Identity` struct + `Provider` interface): both `pkg/ui` and `pkg/oauth2` import it; `pkg/oauth2` **never** imports `pkg/ui` (the import guard stays enforceable); `cmd` wires `*oauth2.Guard` → `identity.Provider` with an explicit nil-safe pattern (`var idp identity.Provider; if guard != nil { idp = guard }`).
4. The SPA handles 401 → `/oauth2/start?rd=…` at a single choke point (with cooldown-re-armed latch and edge-302 `TypeError` recovery), breaks the SSE reconnect loop on 401 via a **bounded** probe, and shows a user badge/logout only when the internal layer is active.
5. The Helm chart implements the **three-state model** (edge-only / overlap / internal-only) with `oauth2Proxy.enabled` (in-path) split from **`oauth2Proxy.replicas`** (object existence), defaulting to today's exact render.
6. Every house gate is green (Pack A, Pack B re-measured, `make test`, `go vet`, gofmt zero-new-drift, tsc parity, `go.mod` zero-diff, boundary diff, anti-vacuity census) **plus** the new council assertions (route census, txn-cookie rejection, cross-origin endpoint rejection, unclean-path gating, version exemption, single-flight, zero-retry, state-cap, SSE-probe-timeout tests; three-state `helm template` matrix).

---

## 2. Scope / Non-Goals

### In scope

| Area | Content |
|---|---|
| Go | **8 production Go source files**: `pkg/oauth2/` (7: `config`, `cookie`, `oidc`, `jwks`, `jwt`, `handler`, `middleware`) + `pkg/identity/` (1: `identity.go` — neutral leaf `Identity` + `Provider`); `cmd/main.go` wiring (`guard != nil` → `idp = guard`, via `buildOAuth2FromEnv()`); `pkg/ui` `handleAuthMe` + `/fe/api/auth/me` |
| Authn surface | `/`, `/ui`, `/ui/*`, `/fe/api/*` gated when enabled — **in-binary**, fail-closed default classification (D2a) |
| FE | `guardedFetch` + latch, `useAuth`, shared identity probe (`authProbe`), redirect-storm breaker, 22 fetch-site migration + broadened census (AR-60), bounded SSE probe, `Header.tsx` badge/logout, `pkg/ui/static/` rebuild |
| Helm | Three-state chart: `values.yaml` toggles (`internalOAuth2.enabled`, `oauth2Proxy.enabled`, `overlap`, `oauth2Proxy.replicas`), 5 template changes, render assertions H1–H7 |
| Tests | **13 Go test files** (11 in §9.1 — 10 unconditional + `guard_test.go` (the conditional fired) — and 2 in §9.2: `pkg/ui/handlers_auth_test.go`, `cmd/oauth2_boot_test.go`); **9 FE suite rows** (§9.3: 7 new + 2 existing re-asserted); `helm template` three-state matrix; house gates |
| Phase 2 doc | ADR-001 choreography per host: daoduc D0–D4 two-upgrade; ensem A→B→C→D per-path `/oauth2`-first (HARD HELD) |

### Non-goals (explicit)

- **No authorization/roles.** Authentication establishes *who*; no group claims, no per-route role checks, no `allowed_groups` semantics for the OAuth principal. **Deferred bearer mode pinned by D16** (AR-37): separate audience, never on `/v1/*`, never `sk-`, TTL ≤15 min, never written into `auth.AuthToken`.
- **No write attribution.** Phase 1 does **not** populate `X-User`; `created_by` stays the literal `"api"` — `X-User` has no producer in the repo today, and populating a client header would let anyone mint `created_by:"admin"` (AR-38, ruling R-3).
- **No change to `/v1/*`.** Bearer/X-API-Key token store, `authz.Resolve`, v203 reach scoping, public `/v1/models`, `/healthz` — exactly as shipped in `df12c62`.
- **No `config.Config` fields.** OAuth2 settings are `os.Getenv`-only; nothing persisted, nothing in `GET /fe/api/config`.
- **No new Go dependencies.** `go.mod`/`go.sum` diffs empty.
- **No Casdoor mutation.** Same issuer, same client, same redirect URI.
- **No cluster mutation in this cycle.** Phase 3/4 documented, not executed; `ensem` hard-held.

---

## 3. Phase Map & Gating (post-ADR-001)

| Phase | Host(s) | Action | Gate to proceed |
|---|---|---|---|
| **1 — Dormant build & ship** | both | New image with the layer inert (`OAUTH2_ENABLED` unset). Chart defaults render today's manifests **byte-identically** (H6 = empty diff). `oauth2-proxy` stays in front. Tier-1 rollback anchor = Phase-1 dormant image / v203 `df12c62`. | All house gates + new council assertions green; dormant verification (§7). |
| **2 — Switch planning** | — | `phase2-switch-plan.md` rewritten around ADR-001 (this cycle). No mutation. | Plan approved (Reviewer). |
| **3 — `daoduc` switch** | backup | **Two-upgrade choreography D0–D4**: D0 pre-flight (both helm revs captured; `helm template` == live; CHART = clean checkout at merged Phase-1 commit; Recreate/maxSurge-0 staged) → D1 **overlap upgrade** (`internalOAuth2.enabled=true` + `oauth2Proxy.enabled=true` + `overlap=true` + secretKeyRef — annotations STAY, `/oauth2`→app, pods enforce behind the live edge) → D2 probe matrix + P-E login rehearsal (P7′) → D3 **internal-only upgrade** (`overlap=false`, `oauth2Proxy.enabled=false`, `oauth2Proxy.replicas=0` — annotations drop against already-enforcing pods, no pod rollout) → D4 soak + full matrix. | Phase 1 live on both hosts; P1–P7′ green; email_verified IdP check (§9, ruling 5). |
| **4 — `ensem` switch** | primary | 🛑 **HARD HELD.** **Per-path `/oauth2`-first, three pre-staged applies:** A) HTTPRoute repoint `/oauth2` **only** → app (+60 s timeout) → B) deployment+configmap enforcing (+ Recreate), wait rollout → C) immediately repoint `/`,`/ui`,`/fe/api` → app → D) soak; edge idled. The irreversible step is **one pre-staged, diffed HTTPRoute apply**, executed by an independent shell lane with the operator holding a live session (P7′). | Phase 3 soak green **+ explicit Emperor sign-off + independent shell lane**. At unhold the Emperor chooses: (a) bounded fail-closed overlap as designed, or (b) shadow-mode rehearsal add-on first (§9). |

**The three handover invariants (ADR-001; validity condition of the whole choreography):**
- **I-1 Loop** — whenever the app enforces, `/oauth2/*` must route to the app.
- **I-2 Exposure** — edge paths/annotations may drop only after *every* serving pod enforces.
- **I-3 Liveness** — the activation that mounts enforcement must mount the secret env in the same act (never a bare env flip).

**Why this shape:** the council refuted object-granularity sequencing (Windows A/B/C: daoduc unauthenticated admin-plane exposure, daoduc CrashLoopBackOff, ensem infinite redirect lockout) and the edge-trust header; the session bridge stays a probe-gated runner-up. Approach 5 satisfies I-1/I-2/I-3 with **zero new trust surface** (no trusted header, no delegation, no foreign-cookie parsing — **OQ-5 stands**), and the app-enforcing-behind-live-edge overlap state is itself the **safe state to roll back into**.

---

## 4. Architecture Summary

### 4.1 Today (both hosts, v203 `df12c62`)

The binary has **zero auth middleware** (`cmd/main.go:514`: `recoveryMiddleware(gzipmw.DecompressRequest(gzipmw.CompressResponse(mux)))`; `pkg/ui/server.go` registers `/fe/api/*` bare). OAuth is enforced **entirely at the edge** by `oauth2-proxy` v7.6.0 (Casdoor OIDC, 24 h `_oauth2_proxy` cookie): `daoduc` = Helm + nginx split ingress (`auth-url`/`auth-signin`, issuer `https://door.daoduc.org`); `ensem` = raw manifests + Envoy `HTTPRoute` reverse-proxy mode (issuer `https://door.ensem.dev`). `/v1/*` uses the proxy's own token store; `/v1/models` public; `/healthz` unauthenticated.

### 4.2 Target (Phase 3/4, internal mode)

In-binary gate on the **same** operator paths, same Casdoor issuer, same redirect URI — `/oauth2/start|callback|logout` path parity keeps Casdoor untouched. `/v1/*` and `/healthz` are never touched by the gate.

```
request → recoveryMiddleware
            └─ oauth2.Guard (ONLY when OAUTH2_ENABLED=true)   [outside gzipmw]
                 ├─ pass allowlist: /v1/, /healthz, /oauth2/, /fe/api/auth/me,
                 │    GET /fe/api/version (class 4b — house gate G4, AR-13/R-1)
                 ├─ /oauth2/start|callback|logout → handler
                 ├─ /, /ui, /ui/*              → 302 /oauth2/start?rd=<sanitized>
                 ├─ /fe/api/*                  → 401 application/json {"error":"unauthenticated"}
                 ├─ everything else (class 7)  → GATED (fail-closed, D2a)
                 └─ authenticated              → next (identity in request context)
            └─ gzipmw.DecompressRequest → gzipmw.CompressResponse → mux
```

### 4.3 `pkg/oauth2/` (7 files) + `pkg/identity` (1 file) — stdlib only — key security properties post-amendment

| File | Responsibility | Notable properties (amended) |
|---|---|---|
| `pkg/identity/identity.go` | neutral leaf: `Identity` struct + `Provider` interface (`Enabled()`, `Current(r)`, `StateStoreCount()`) | imported by BOTH `pkg/ui` and `pkg/oauth2`; `pkg/oauth2` never imports `pkg/ui` (B1) |
| `config.go` | env → typed config; cookie-key derivation | `OAUTH2_EXTERNAL_URL`/`REDIRECT_URL` **mandatory** when enabled (fail-fast); **no request-derived security decisions** (AR-03/R-5); `COOKIE_SECURE` default **true**; `OAUTH2_ALLOWED_ENDPOINTS` escape hatch (AR-05); RS256-only default (AR-06) |
| `cookie.go` | AES-256-GCM seal/open, AAD = cookie name | **EXACTLY 2 HKDF subkeys (txn, session)** from a dedicated root key — a txn ciphertext under the session-cookie name fails closed (AR-01/R-9); ONE pinned wire format: `base64url(16B nonce ‖ GCM ciphertext ‖ tag)`, `TxnDigest` = SHA-256 over the **full sealed cookie value string**; **seal-time** 4096 B budget + per-claim caps (AR-33); 6-rung base64 ladder (AR-63) |
| `oidc.go` | lazy discovery, issuer-match, token exchange | **issuer-origin pinning** — every advertised endpoint same-origin or hard error; `CheckRedirect` rejects cross-origin, never re-sends credentials (AR-05); discovery **single-flight + negative cache** (AR-08); one shared keep-alive client, per-request ctx timeouts, never `context.Background()` (AR-09) |
| `jwks.go` | JWK parse (RSA/EC/OKP), cache, refresh | **re-armable** single-flight (NOT `*sync.Once`), one constant key `"jwks-refresh"`; `kty↔alg↔crv` pinned at parse; `JWK.alg`==header alg, `key_ops`⊇verify; kid-miss 1/60 s throttle; stale-while-error grace covers fetch failures only (AR-07/25/26/57) |
| `jwt.go` | claims + alg-whitelist verification | default **`RS256`** (live Casdoor evidence); `HS*`/`none` rejected case-insensitively; type-asserted verifier map; ES256 raw `R‖S` normative cell, DER rejected; `azp`/`aud` rules (OIDC Core §3.1.3.7); `typ` rule — **`JWT` or absent accepted (case-insensitive), `at+jwt` rejected** in the id-token path (access-token type, RFC 9068); id-token vs access-token distinguished (AR-06/30/31/55/56) |
| `handler.go` | `/oauth2/start`, `/oauth2/callback`, `/oauth2/logout` | **state bound to the initiating browser** via `llmproxy_oauth2_txn` cookie (32 B, HttpOnly Secure SameSite=Lax Path=/oauth2 Max-Age=600; SHA-256 in state record; constant-time match; single-use delete — AR-01/R-6); PKCE S256 + nonce; token exchange **single attempt, zero retries** ⇒ 502; capped state store + 429 overflow + per-IP rate limit; `rd` rejects `/oauth2/*` and `/fe/api/auth/me`; valid-session short-circuit at `/oauth2/start` (302 `rd`, zero IdP hop — AR-54); `email_verified` strict (absent ⇒ 403 unless allowlist `*` with explicit `OAUTH2_ALLOW_ANY_EMAIL_DOMAIN=true` + WARN); POST-only logout + pinned `post_logout_redirect_uri` |
| `middleware.go` | the `Guard` + `identity.Provider` adapter (neutral leaf `pkg/identity`) | **fail-closed default classification** + explicit pass allowlist (AR-02/R-4); `path.Clean` normalization; 302 browser / 401 JSON XHR; `GET /fe/api/version` exempt (class 4b, AR-13/R-1) |

### 4.4 Configuration & secret handling (post-amendment)

- All settings via `os.Getenv` (production does not set `APPLY_ENV_OVERRIDES`); nothing enters `config.Config`; `GET /fe/api/config` cannot leak anything (D3 + body-substring test).
- Env table extended per the council: `OAUTH2_EXTERNAL_URL` (mandatory), `OAUTH2_REQUIRE_EMAIL_VERIFIED` (default true), `OAUTH2_ALLOW_ANY_EMAIL_DOMAIN` (default false), `OAUTH2_ALLOWED_ENDPOINTS`, `OAUTH2_TOKEN_EXCHANGE_TIMEOUT` (10 s), `OAUTH2_USERINFO_TIMEOUT` (5 s), `OAUTH2_DISCOVERY_TIMEOUT` (5 s, clamp [1 s, 30 s]), `OAUTH2_JWKS_STALE_GRACE`, `OAUTH2_LOGOUT_REDIRECT`, `OAUTH2_STATE_STORE_MAX`, `OAUTH2_RATE_LIMIT_RPM` (10/min per IP), `OAUTH2_RATE_LIMIT_BURST` (20) — full table in `phase1-plan.md` §4.2 (single source of truth; phase 2 reconciles to it, AR-17).
- **Key separation (R-9):** the internal layer derives its **own** root key from `OAUTH2_COOKIE_SECRET` (preferred) — never from the shared edge secret when the dedicated var is present — then HKDF-splits per purpose. The existing `oauth2-proxy-secret` remains reusable for the *client* credentials and, if no dedicated var is set, the cookie secret; modes only are logged.
- **Resilience split:** config errors fail fast; discovery/JWKS lazy with single-flight + negative caching; IdP outage degrades the admin UI only — cookie validation is fully offline (AES-GCM + exp), `/v1/*` structurally unaffected (AR-27).
- **Log surface (authoritative, AR-39):** stdout only — mode/secret_mode at boot; `sub` + email domain at INFO per login; never code/token/cookie/secret values; **no OAuth2 event on the events bus**.

### 4.5 Helm three-state chart

| State | `internalOAuth2.enabled` | `oauth2Proxy.enabled` | `overlap` | Render |
|---|---|---|---|---|
| **edge-only** (default) | false | true | — | **byte-identical to today** (H6 = empty diff); edge Deployment+Service **always rendered** |
| **overlap** | true | true | true | nginx auth annotations STAY; `/oauth2`→app; `OAUTH2_*` env + secretKeyRef render; `strategy: Recreate`/`maxSurge: 0` nested under `internalOAuth2:` (default render untouched) |
| **internal-only** | true | false | false | annotations absent; `/oauth2`→app; edge Deployment+Service **still rendered at `replicas: 0`** |

**Object-existence rule (Issue 1):** the edge `oauth2-proxy` Deployment+Service are rendered **unconditionally** — Helm can never prune the rollback anchor; `oauth2Proxy.enabled` gates **only the ingress wiring** (auth annotations + `/oauth2` backend); `oauth2Proxy.replicas` is the idle knob (template must test for the *presence* of the key, since a bare `{{if}}` treats `0` as falsy).

`fail` only on genuinely contradictory input (`internalOAuth2.enabled` + `oauth2Proxy.enabled` **without** `overlap=true`). Render assertions H1–H7 cover all three states; `required` guard demands issuer/client values whenever internal mode is on (AR-48).

---

## 5. Design Invariants (normative, post-amendment)

Condensed from `phase1-plan.md` §3 (D1–D17; each has a named test):

| # | Invariant |
|---|---|
| D1 | Dormant by default: no middleware, no `/oauth2/*` routes, no key derivation, no network calls. Delta = one constant-JSON route `/fe/api/auth/me` **+ one probe request per SPA page load** + a gated `fetch`-`TypeError` recovery: `reloadOnce()` fires **ONLY after the `/fe/api/auth/me` probe has resolved `enabled:true`** (only `/fe/api` GETs, at most once per load); before/without that resolution a TypeError is a plain error — dormant mode is provably inert. Real dormant-wiring test boots the actual binary with `OAUTH2_ENABLED` unset ⇒ `/fe/api/auth/me` → 200 `{"enabled":false}` (no typed-nil 500). |
| D2 | `/v1/*` and `/healthz` are **never** gated. |
| **D2a** | Path classification is **fail-closed** — class 7 default = gated; pass set is an explicit allowlist (`/v1/`, `/healthz`, `/oauth2/`, `/fe/api/auth/me`, `GET /fe/api/version`); route-census test enumerates every registered mux pattern; `path.Clean` normalization (AR-02/R-4). |
| D3 | Secrets never enter `config.Config`, the DB, or `/fe/api/config`. |
| D4 | Zero new Go dependencies. |
| D5 | `alg` whitelist (default **RS256**, live-Casdoor-evidenced); `HS*`/`none` structurally impossible, case-insensitive rejection; `kty↔alg↔crv` pinned. |
| D6 | PKCE S256 + `state` + `nonce` mandatory on the callback. |
| **D6a** | `state` is bound to the **initiating browser** via the txn cookie + HKDF subkey; constant-time digest match; single-use (AR-01/R-6). |
| D7 | `kid` miss ⇒ rate-limited re-armable single-flight JWKS refresh (one key, 1/60 s), fail-closed 503. |
| D8 | Cookie = AES-256-GCM, AAD-bound, opaque, **per-purpose HKDF subkeys** (R-9). |
| D9 | Cookie budget enforced at **seal** time (encoded ≤4096 B; per-claim caps; reject, never truncate). |
| D10 | `rd` path-relative only; rejects `/oauth2/*` + `/fe/api/auth/me` (loop-proofing); schemes/`//`/control chars rejected. |
| D11 | Browser paths → 302; `/fe/api/*` → 401 JSON. **Addendum:** `GET /fe/api/version` exempt (class 4b) — house deploy gate G4 wins (AR-13/R-1). |
| D12 | FE never redirect-loops: one-shot latch shared by `guardedFetch` and the SSE path, cooldown-re-armed (5–10 s), probe exempt, edge-302 `TypeError` ⇒ one-shot `location.reload()`. |
| D13 | SSE breaks its reconnect loop on 401 via a **bounded** probe (AbortController, 3 s; timeout ⇒ resume backoff, never cached as `false`). |
| D14 | v203 token authorization untouched — the OAuth principal is a separate concept, never an `auth.AuthToken`; **no `X-User` producer; `created_by` stays `"api"`** (AR-38/R-3). |
| D15 | *(replaced)* ~~mutual exclusion~~ → the **three-state model** (edge-only / overlap / internal-only); overlap is a designed, probe-gated transition (AR-22). |
| **D16** | Deferred bearer mode (future) MUST use a separate audience, never `/v1/*`, never `sk-`, 401 + `WWW-Authenticate: Bearer`, TTL ≤15 min, never an `auth.AuthToken` (AR-37). |
| **D17** | The three handover invariants I-1 Loop / I-2 Exposure / I-3 Liveness hold at every step of both cutovers (ADR-001). |

---

## 6. Deliverables & Change Inventory (Phase 1)

| Layer | Paths | Nature |
|---|---|---|
| New Go package | `pkg/oauth2/{config,cookie,oidc,jwks,jwt,handler,middleware}.go` (+ `_test.go` per file, incl. the council's negative tests) | new |
| Composition | `cmd/main.go` (env → config → guard → routes → middleware order), `cmd/oauth2_boot_test.go` | edit + new test |
| UI server | `pkg/identity/identity.go` (new leaf package), `pkg/ui` `handleAuthMe` + `handlers_auth_test.go` | new + edit |
| Frontend | `src/utils/apiFetch.ts`, `src/utils/authProbe.ts`, `src/utils/redirectStorm.ts`, `src/hooks/useAuth.ts`, 21 fetch sites in `useApi.ts` + `ModelForm.tsx:462` (22 total), `useEvents.ts` (bounded probe), `Header.tsx`, `App.tsx` + 9 FE suite rows (7 new + 2 existing re-asserted); census pins `EventSource\|WebSocket\|XMLHttpRequest\|sendBeacon\|axios\|navigator.serviceWorker` | edit + new |
| Embedded SPA | `pkg/ui/static/` rebuilt & committed (determinism proof kept, T14) | regenerated |
| Helm | `k8s/values.yaml` + 5 templates implementing the three states + `oauth2Proxy.replicas` split + `required` guard | edit |
| Docs | the five files in `.agents/shared/planning/internal-oauth2/` | this cycle |

**Not touched:** `pkg/config/`, `pkg/auth/`, `pkg/proxy/`, `pkg/grouplb/`, `pkg/credentiallb/`, `pkg/modelscache/`, migrations, `go.mod`/`go.sum`.

---

## 7. Test & Acceptance Summary (post-amendment)

- **Crypto/OIDC:** `TestCallback_RejectsStateFromAnotherBrowser` (txn binding); `TestConfig_EnabledRequiresExternalURL`; `TestOIDC_RejectsCrossOriginEndpoints` + `…CrossOriginRedirectRejected`; alg-confusion (HS256 signed with the RSA public key); tampered cookie/AAD; ES256 raw-vs-DER; `azp`/`aud`/`typ` rules.
- **Composition:** `TestMiddleware_RouteCensus` (every registered mux pattern allow-listed or gated) + `TestMiddleware_UnknownPathIsGated` + `TestMiddleware_UncleanPathCannotBypassGate` (Go 1.26 301 behaviour); `TestMiddleware_VersionIsExempt`; **real dormant-wiring boot test** (`TestMain_DormantAuthMeIs200`: actual binary, `OAUTH2_ENABLED` unset ⇒ 200 `{"enabled":false}`); dormant boot census; `/fe/api/config` body-substring test.
- **Resilience:** kid-throttle + stale-grace; discovery single-flight (concurrent starts ⇒ one fetch) + negative cache; token-exchange **zero-retry**; state-store cap + 429; `TestSSE_ProbeTimeoutKeepsBackoff`; probe-timeout-not-cached.
- **Chart:** `helm template` over **all three states** — edge-only = **empty diff** vs the pre-change chart; overlap = annotations present **and** app env present **and** `/oauth2`→app; internal-only = annotations absent + edge rendered at `replicas: 0`; `fail` on contradictory input.
- **Cutover (Phase 3/4):** probe matrix **P-A…P-L** with explicit ALARM columns — 200 on `/fe/api/*` = EXPOSED; ≥3 same-origin 302s on `/ui` = LOOP; CrashLoopBackOff = Window B; missing edge Deployment = pruned anchor; unpinned dormant `/oauth2/start` 404 = doc gap. E11 asserts **401 JSON**; E11b loop detector; E12 short-circuit probe; **P-E split** — scripted probe expects **401 JSON** on `/fe/api/models` (the app never mints a session cookie on `/fe/api/*`), browser rehearsal expects **200 + `Set-Cookie: llmproxy_oauth2`** (P-D's ALARM applies only after the browser rehearsal actually ran). **Phase 3 may not execute until the mechanical pre-flight harness passes** (`helm template` three-state render + grep assertions, `kubectl apply --dry-run=server` field-ownership check, `bash -n` on every script block).
- **House gates:** Pack A, Pack B (re-measured; 35 → 37 packages — `pkg/oauth2` + `pkg/identity`), `make test`, `go build ./cmd`, `go vet ./...`, gofmt zero-new-drift, `HOME` isolation + real-home `config.json` hash, boundary diff (`pkg/config`, `pkg/proxy`, migrations = 0 bytes), `go.mod` zero-diff, anti-vacuity census, tsc parity (**18 errors / 9 files** — `TOTAL_ERRORS=18`; the house blueprint's 19/9 is stale), SPA determinism proof.

---

## 8. Cutover Summary (Phase 3 / Phase 4 — ADR-001)

Full detail + exact unified diffs in `phase2-switch-plan.md`.

**`daoduc` (Phase 3, Helm — two upgrades, never one):** D0 pre-flight (capture **both** the live revision and the v203 revision — the ledger and ship record disagree by one; `helm template` baseline saved to `render.pre.yaml`; **CHART = clean checkout at the merged Phase-1 commit**, never the stale main checkout at `5ce0ead`; runbook chart-cleanliness gate). D1 overlap upgrade (three-state values + secretKeyRef; annotations stay; `/oauth2`→app; pods enforce behind the live edge). D2 probe matrix + P-E login rehearsal (operator holds live edge session, mints `llmproxy_oauth2`). D3 internal-only upgrade (`overlap=false`, `oauth2Proxy.enabled=false`, `oauth2Proxy.replicas=0`) — annotations drop against **already-enforcing** pods, **no pod rollout**. D4 soak + full matrix; P-J asserts the edge objects still exist at 0 replicas. Static pre-flight validator: rendered env ⊇ `phase1-plan.md` §4.2 required set. Pre-cutover IdP check (fail-closed): all operator accounts on `door.daoduc.org` verified, else fix the account / tighten the allowlist first — `email_verified:false` stays strict 403. **Rollback:** `helm rollback` one revision returns annotations + edge while the app stays enforcing = the safe overlap state; tier-1 anchor = Phase-1 dormant image; never below v203 without reversing migration 034 first.

**`ensem` (Phase 4, raw manifests — HARD HELD, per-path `/oauth2`-first):** A) HTTPRoute repoint **`/oauth2` only** → app (+ `timeouts: request: 60s`, AR-44) with Gateway accepted-status check (AR-45) — **pre-A staging step copies the pre-switch manifests to a rollback staging dir; staged-file existence is an unhold precondition**; B) deployment+configmap enforcing (+ Recreate patch), wait `rollout status`; C) **immediately** repoint `/`,`/ui`,`/fe/api` → app (ends the overlap loop structurally); D) soak; edge idled via `kubectl scale`. **R1+R2 are ONE scripted rollback unit** (reverted HTTPRoute + rolled-back deployment = unauthenticated exposure — that is why the order is HTTPRoute-first) restoring the staged operands; post-rollback check is a session-bearing round-trip. The ensemble `/v1` lane is a separate HTTPRoute rule and survives every step. Phase-4's irreversible step is one pre-staged, diffed HTTPRoute apply by an independent shell lane with the operator holding a live session (P7′).

**v203 Rollback Ordering Invariant:** Phase 1 builds on `df12c62`; migration 034 (`allowed_groups`) is live on both hosts. Toggling OAuth2 off, or rolling back to the Phase-1 dormant image / `df12c62`, does **not** cross 034. Rolling back **past** v203 is **forbidden** without reversing 034 first.

---

## 9. Decisions Recorded

### Planner adjudications (2026-11-19, standing)

| # | Decision |
|---|---|
| OQ-1 | **Reuse** the existing `oauth2-proxy-secret` `CLIENT_ID`/`CLIENT_SECRET`; no Casdoor console work. (Cookie key separation now additionally preferred via dedicated `OAUTH2_COOKIE_SECRET` — R-9.) |
| OQ-2 | Phase 1 dormant on **both** hosts; activation per-host. |
| OQ-3 | `OAUTH2_SESSION_TTL` default **24h** (parity with `--cookie-expire=24h`). |
| OQ-4 | Chart `secretKeyRef` env wiring ships in **Phase 1 (T15)** — Phase 3 is a pure chart-toggle (three-state, per AR-17). |
| OQ-5 | **No** `_oauth2_proxy` cookie interop; forced re-login accepted. **Reaffirmed by ADR-001** (no foreign-cookie parsing ever). |
| OQ-6 | `gate-ensem.sh` does not exist on disk — dropped; the probe matrix (P-A…P-L) is the Phase-3/4 verification backbone (AR-50). |
| OQ-7 | Idle (never prune) `oauth2-proxy` through Phase 4 — now **enforced by the chart** (`oauth2Proxy.replicas` split, AR-21); single cleanup PR after Phase 4. |

### Architect rulings adopted (leader-approved, 2026-11-19)

R-1 `/fe/api/version` exempt · R-2 E11 asserts 401 · R-3 no `X-User` producer / `created_by` stays `"api"` · R-4 fail-closed classification + route census · R-5 `OAUTH2_EXTERNAL_URL` mandatory, no request-derived security decisions · R-6 state binds to the browser (txn cookie + HKDF) · R-7 interlock = per-path `/oauth2`-first + three-state chart (ADR-001) · R-8 capped state store + Recreate/single-replica invariant · R-9 dedicated cookie key + per-purpose HKDF subkeys.

### Emperor-pending (do not block Phase 1)

1. **Phase-4 overlap acceptance (at unhold):** (a) bounded fail-closed overlap window (minutes, low-traffic, user notice, P7′, AR-54 short-circuit) — the council's design; or (b) shadow-mode rehearsal add-on first (adds a Phase-1 mode + trust source). Documented both; decision at unhold.
2. **Principal admission at cutover (per host):** `email_verified:false` is **strict 403 in every mode — no runtime knob, no bypass** (leader decision, final pass). The Phase-3 pre-flight IdP check must verify **ALL operator accounts on `door.daoduc.org` have verified emails BEFORE cutover**; if any is unverified, the operator fixes the account in Casdoor or tightens the allowlist **before** proceeding (fail-closed). Recommendation stands to tighten `OAUTH2_EMAIL_DOMAINS` on `daoduc` at Phase 3 and keep `*` on `ensem` until a soak proves no operator account is excluded.
3. **`ensem` unhold authority:** currently *Phase 3 soak green + explicit Emperor sign-off + independent shell lane*.

---

## 10. Top Risks (post-amendment)

| # | Risk | Severity | Mitigation |
|---|---|---|---|
| 1 | **Three-state chart template logic** — a wrong conditional silently re-opens Window A | 🔴 | byte-level render assertions for all three states (H-series) + P-I/P-J post-upgrade probes; the chart is the riskiest artifact |
| 2 | **Residual fail-open gap** — a route registered outside the census | 🔴 | route-census test + fail-closed default (D2a/R-4) |
| R1' | `alg`-confusion forgery | Critical | typed verifier map; HS/`none` impossible; dedicated negative test |
| R2 | Secret leakage into `/fe/api/config` | Critical | env-only; body-substring test; boundary diff |
| R7 | IdP outage | High | lazy discovery + single-flight + negative cache; offline cookie validation; `/v1/*` unaffected |
| R12' | Overlap-state ping-pong for users without a live edge session | 🟡 | short overlap, low-traffic window, user notice, AR-54 short-circuit, P7′ |
| R13' | Pod straddle during overlap roll | 🟡 | `strategy: Recreate`/`maxSurge: 0`; logins only after `rollout status` |
| R14' | Env-name drift (silent no-op filter) | 🟡 | §4.2 single source of truth + static pre-flight validator |
| R17 | Stale-citation risk | 🟡 | every citation re-read in the worktree; implementers re-read at the pinned commit |

(Full register: `phase1-plan.md` §12 R1–R20; operational risks in `phase2-switch-plan.md`.)

---

## 11. Evidence Base

- **Architecture review:** `architecture-recommendation.md` (five reviewers + council; live Casdoor discovery/JWKS probes; Go 1.26 `ServeMux` probe) and `architecture-decision-record.md` (ADR-001) — same directory.
- **Worktree survey:** `cmd/main.go`, `pkg/ui/server.go`, `pkg/config/config.go`, `pkg/crypto/encryption.go`, `go.mod` + `/home/nea/go/pkg/mod`, `pkg/ui/frontend/*`, `k8s/*`, `Makefile`, `.agents/tester/PACKS.md`, `.agents/shared/planning/token-authz-allowed-groups/adr-token-permission-model.md`.
- **Infrastructure survey:** `/home/nea/Code/data-center-scripts/infranstructure/k8s/llmproxy/**` + `.agents/shared/planning/llmproxy-backup-first-rollout-2026-11/runbook.md` (absolute path — the runbook lives in data-center-scripts, not the proxy repo; AR-51).
- **Project knowledge:** `llmproxy-v1-public-no-ingress-auth`, `probe-c-primary-v203-authz-live`, `main-checkout-stale-tree-trap`; POK `llmproxy` critical-substrate routing.

---

## 12. Handoff

1. **Reviewer stage:** re-review the amended plan with `architecture-recommendation.md` attached — the amendment set is explicit enough to verify mechanically (each AR → plan section in `phase1-plan.md` §17 and the phase-2 appendix).
2. **Approve Phase 1** → implementation commission (T1–T16) in this worktree on `feature/internal-oauth2`.
3. Phase 3 (`daoduc`) executes only against the rewritten phase-2 text; **Phase 4 (`ensem`) stays hard-held** with the Emperor's unhold-time choices documented (§9).

> Planning only: no Go/TS/Helm file modified (`git status --porcelain` = `?? .agents/shared/planning/internal-oauth2/` only); no secret value read or reproduced at any point.
