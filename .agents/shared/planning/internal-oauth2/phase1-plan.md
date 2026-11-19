# Phase 1 — DORMANT-BY-DEFAULT INTERNAL OAUTH2 AUTHENTICATION (AUTHENTICATION LEVEL ONLY)

**Date:** 2026-11-19 (amended 2026-11-19 after the architecture council review)
**Author:** plan-creation worker (via `plan-creation` skill), commissioned by the leader
**Status:** Ready for Review — **amended**: all 22 🔴 blockers + the 🟡/🟢 rows targeting Phase 1 from `architecture-recommendation.md` (AR-01…AR-66) and ADR-001 are folded in; see the **§17 AR register cross-reference appendix** for the AR → section map.
**Base commit:** `92c4234` (branch `feature/internal-oauth2`, worktree `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-internal-oauth2`)
**Citation convention:** every `file:line` below was read **inside the worktree** at `92c4234`. The main checkout `/home/nea/Code/opensource-projects/llm-supervisor-proxy` is a **stale tree** (POK `main-checkout-stale-tree-trap`) — line numbers taken from it are INVALID and are never used here.
**Upstream authorities folded in:** `architecture-recommendation.md` (council register AR-01…AR-66), `architecture-decision-record.md` (ADR-001, accepted), `plan-overview.md` §9 (OQ-1…OQ-7).

---

## 1. Objective

Add a **stdlib-only, dormant-by-default** internal OAuth2 / OIDC authentication layer to `llm-supervisor-proxy` that gates the **operator surfaces only** (`/`, `/ui/*`, `/fe/api/*`) behind an in-binary Authorization Code + PKCE flow against a Casdoor-compatible OIDC issuer, while leaving the whole `/v1/*` client API, the public `/v1/models`, `/healthz`, and the v203 token-authorization model **byte-for-byte untouched**.

**Single-sentence completion test:** with `OAUTH2_ENABLED` unset/false, every request on every route behaves exactly as it does today (zero middleware layers added, zero crypto initialized, zero outbound calls); with `OAUTH2_ENABLED=true` and a valid issuer, an unauthenticated browser hitting `/ui/` is redirected into the OIDC code flow and comes back holding an AES-256-GCM session cookie, and an unauthenticated `/fe/api/*` call receives `401 {"error":"unauthenticated"}` — with **no new dependency in `go.mod`**.

**Phase 1 is AUTHENTICATION ONLY.** No roles, no groups, no scopes, no per-user model/credential grants. The phase produces a *principal* (`sub`, `email`, `name`) that the UI displays; it grants **no** additional privilege over the existing admin surface (which remains "any authenticated operator = admin", exactly the status quo at the ingress layer today).

---

## 2. Scope

### 2.1 In Scope

| # | Item | Anchor |
|---|------|--------|
| S1 | New Go package `pkg/oauth2/` — config, OIDC discovery, JWKS cache, JWT verification, session cookie, auth-code flow handlers, gate middleware | new package, stdlib-only |
| S2 | `/oauth2/start`, `/oauth2/callback`, `/oauth2/logout` routes (Casdoor / oauth2-proxy path parity) | `cmd/main.go:384` (mux), `k8s/templates/ingress-public.yaml:29-35` (existing `/oauth2` path) |
| S3 | `/fe/api/auth/me` identity probe (enabled / authenticated / user) | `pkg/ui/server.go:193-246` (the `/fe/api/*` route block) |
| S4 | Gate middleware wrapping the mux in `cmd/main.go`, with an explicit path-class gating table | `cmd/main.go:514` |
| S5 | Frontend: shared 401 handling across **all 22 fetch sites** (21 in `useApi.ts` + `ModelForm.tsx:462`), SSE `EventSource` 401 detection + reconnect-loop break, optional signed-in badge + logout in the header | `pkg/ui/frontend/src/hooks/useApi.ts`, `hooks/useEvents.ts`, `components/Header.tsx`, `components/config/ModelForm.tsx:462` |
| S6 | Helm chart dormant toggles: `oauth2Proxy.enabled: true` (default) + `internalOAuth2.enabled: false` (default), mutual exclusion, **three-state** `helm template` parity proof | `k8s/values.yaml`, `k8s/templates/*.yaml` |
| S7 | Go unit + integration tests, FE Vitest suites, `helm template` verification, Pack A/B + `go vet` + `gofmt` + `HOME`-isolation gates | `.agents/tester/PACKS.md` |
| S8 | Vite rebuild → `pkg/ui/static/` committed (Single-Image FE/BE Coupling) | `pkg/ui/frontend/vite.config.ts` (`outDir: '../static'`) |

### 2.2 Out of Scope (explicitly excluded, with reason)

| # | Excluded | Reason |
|---|----------|--------|
| O1 | Roles / groups / scopes / per-user grants | Commission is **authentication level only**; authorization lives in the v203 `authz` model (`pkg/authz/`) and the ingress already treats any authenticated operator as admin. Mixing them would couple this phase to the frozen v203 ADR. |
| O2 | Any change to `/v1/*` (chat, messages, models, image_generation, decisions, mcp) | v203 + blueprint contract: `/v1/*` Bearer/X-API-Key auth is the CLIENT API; `/v1/models` is public by design. The OAuth2 gate must never see these paths. |
| O3 | Any change to `config.Config` / `pkg/config/` | `GET /fe/api/config` encodes `config.Config` **directly** (`pkg/ui/server.go:589-594`). OAuth2 settings and secrets must therefore live in `os.Getenv` ONLY — never in `config.Config`, never in the DB, never in the FE config payload. |
| O4 | Any new `go.mod` dependency | The offline module cache (`/home/nea/go/pkg/mod`) has `golang.org/x/oauth2@v0.35.0` (client-side only) and **no** JWT/JOSE verifier. Design is **stdlib-only**; `golang.org/x/oauth2` is deliberately **not** adopted either (it is a client-side OAuth2 helper, not a JWT verifier, and adding it would still be a dependency). |
| O5 | Replacing the existing ingress `oauth2-proxy` deployment | It stays the **default** path (`oauth2Proxy.enabled: true`). The internal layer is an **alternative**, dormant unless explicitly enabled. |
| O6 | DB migration / new columns | No persisted server-side session; the session is a stateless encrypted cookie. |
| O7 | Multi-tenancy, IdP-side group mapping, SCIM/provisioning | Out of commission scope. |
| O8 | Live deployment / Helm release / dcs pin push | Phase 1 is code + chart only. Shipping is a later phase under the standing deploy authorization. |

---

## 3. Design Invariants (normative for Phase 1)

| # | Invariant | Rationale / proof obligation |
|---|-----------|------------------------------|
| **D1 — Dormant by default** | `OAUTH2_ENABLED` unset or ≠ `true` ⇒ no auth middleware is installed on the mux, no `/oauth2/*` route is registered, no cookie key is derived, no OIDC/JWKS network call is ever made. The only dormant-mode addition is one new read-only route `/fe/api/auth/me` returning a constant JSON. **B2 (leader pre-ruling):** the FE's `reloadOnce()` recovery path is provably inert in dormant mode — it fires **only** when the `/fe/api/auth/me` probe has resolved to `enabled:true`, and the dormant probe answers `enabled:false`, so a persistent backend outage in the dormant/edge world produces a plain error, never a reload loop. | Test: dormant boot ⇒ `GET /oauth2/start` → 404, `GET /fe/api/auth/me` → 200 `{"enabled":false,...}`, and the mux handler chain length is unchanged (`cmd/main.go:514` untouched in dormant mode). **`TestMain_DormantAuthMeIs200`** boots the real binary with `OAUTH2_ENABLED` unset and asserts `GET /fe/api/auth/me` → 200 `{"enabled":false}` with no 500/panic (B4). |
| **D2 — `/v1/*` and `/healthz` are NEVER gated** | The gate applies to operator surfaces only. | Test: `/v1/models` still 200 unauthenticated; `/v1/chat/completions` still 401 from the proxy's own `authenticate`; `/healthz` still 200. |
| **D2a — Path classification is FAIL-CLOSED by default** | The gate's default for an unrecognised path is **gated**, never public. A security gate whose default is "public" makes every route added after this ship public by default (AR-02). | The pass set is an explicit allowlist: `/v1/`, `/healthz`, `/oauth2/`, `/fe/api/auth/me`, `GET /fe/api/version`. Test: `TestMiddleware_RouteCensus` enumerates the **FULL** mux registration set — `pkg/ui/server.go:164` (`/ui/`), `:184` (`/`), `:193-246` (the `/fe/api/*` set: `:193` version, `:194` ram, `:195` config, `:196` models, `:197` models/, `:198` models/validate, `:199` models/test, `:205` decisions/test, `:211` image_generation/test, `:212` events, `:213` requests, `:223` requests/, `:224` app-tags, `:225` buffers/, `:227` tokens, `:228` tokens/, `:230` credentials, `:231` credentials/, `:238` credential-groups, `:239` credential-groups/, `:241` providers, `:243-246` usage*, `:213` `/fe/api/auth/me`); `pkg/mcp/mcp.go:32-45` (`RegisterProxyHandlers` → `:40` `/v1/mcp/{id}/sse`, `:41` `/v1/mcp/{id}/messages`, `:44` `/v1/mcp/{id}/`, `:45` `/v1/mcp/{id}`) + `pkg/mcp/mcp.go:49-62` (`RegisterAPIHandlers` → `:55` `/fe/api/mcp-servers/status`, `:59` `/fe/api/mcp-servers/test-connection`, `:62` `/fe/api/mcp-servers/`); `cmd/main.go:409-436` (`uiServer.RegisterHandlers` at `:409` + `:412` chat, `:413` messages, `:414` models, `:421` image_generation, `:430` decisions, `:433` `/healthz`); `pkg/oauth2/handler.go:283-285` (`:283` `/oauth2/start`, `:284` `/oauth2/callback`, `:285` `/oauth2/logout`) — and fails if a registered route is neither allow-listed nor gated (**no route may be silently outside the census**); `TestMiddleware_UncleanPathCannotBypassGate` proves `path.Clean` normalisation closes `/x/../fe/api/config` (Go 1.26 `net/http/server.go:2680/:2689` 301-redirects the unclean path). |
| **D3 — Secrets never enter `config.Config`, the DB, or `/fe/api/config`** | `pkg/ui/server.go:589-594` marshals `config.Config` verbatim to the browser. | Test: `GET /fe/api/config` body contains no `oauth2`/`client_secret`/`cookie_secret` substring; `config.json` hash unchanged across boot. |
| **D4 — Zero new Go dependencies** | Offline module cache has no JWT/JOSE library. | Test: `git diff <base>..HEAD -- go.mod go.sum` is **empty**; `go list -deps ./pkg/oauth2` contains only stdlib + intra-repo packages. |
| **D5 — Algorithm whitelist; symmetric algorithms are structurally impossible** | The classic `alg` confusion attack: an attacker re-signs with HS256 using the public key as the HMAC secret. | `jwt.go` maps `alg` → a *typed* verifier; `HS*`/`none`/unknown ⇒ hard error before any key lookup. Test: HS256 token signed with the RSA public key as HMAC secret ⇒ rejected. |
| **D6 — PKCE (S256) + `state` + `nonce` are mandatory** | CSRF on the callback, code interception, and ID-token replay are all closed by the triple. | Test: callback without `state`, with mismatched `state`, with a `code_verifier` that does not hash to the challenge, or with a nonce mismatch ⇒ 400 and no cookie set. |
| **D6a — `state` is bound to the INITIATING BROWSER** | A server-side-only `state` record does **not** close login-CSRF / session fixation: the attacker runs `/oauth2/start`, authenticates at the IdP, and lures the victim to `/oauth2/callback?code=<attacker code>&state=<attacker state>` — the server finds the state, exchanges the code with the **server-side** verifier, and plants the **attacker's** session in the **victim's** browser. PKCE cannot help (the verifier never leaves the server) and neither can the nonce (the server compares its own stored nonce) (AR-01, ruling R-6). | **ONE wire format (B3, leader pre-ruling):** `/oauth2/start` sets the transaction cookie `llmproxy_oauth2_txn` = `Seal(txnSubkey, {nonce,iat,exp})` = `base64url(16B nonce ‖ AES-256-GCM ciphertext ‖ tag)` under the **txn** HKDF subkey, and stores `TxnDigest = SHA-256(<the full sealed cookie value string>)` in the state record. The callback requires the cookie to be present, to open under the **txn** subkey, and its `SHA-256` to match `TxnDigest` under `crypto/subtle.ConstantTimeCompare` — then deletes the record (single use). A txn ciphertext pasted under the session-cookie name fails closed (wrong subkey ⇒ GCM auth failure). `deriveSubkeys` returns **EXACTLY 2 subkeys** — `session` and `txn` (no third "state-digest" subkey; the digest is plain SHA-256 over the sealed value, not HKDF output). `SameSite=Lax` is correct — the callback is a top-level cross-site GET. Test: `TestCallback_RejectsStateFromAnotherBrowser`. |
| **D7 — `kid` miss triggers a rate-limited JWKS refresh** | A rotated signing key must not brick the login; an attacker must not be able to force an unbounded refresh storm (DoS on the IdP). | `jwks.go`: refresh allowed at most **once per 60 s**; on refusal the request fails closed with 503 and a `Retry-After`. |
| **D8 — Cookie confidentiality + integrity: AES-256-GCM, AAD-bound, opaque** | The cookie carries the principal; it must be unforgeable and unreadable. | `cookie.go`: `base64url(nonce ‖ ciphertext ‖ tag)`, 16-byte random nonce, AAD = cookie name; tampered byte ⇒ GCM auth failure ⇒ treated as "no session". |
| **D9 — Cookie budget is enforced at SEAL time, per claim** | Browsers silently drop oversized cookies; a truncated cookie would be a session-integrity bug, and a budget checked only on open would mint an unusable cookie (AR-33). | Claims are minimal and capped: `sub` ≤ 64 B (reject over), `email` ≤ 254 B (reject over), display name ≤ 128 B (**truncate**); `preferred_username` is **dropped** (live Casdoor's default token format does not emit it — AR-62). The **encoded** value is checked against 4096 B at seal time ⇒ over budget ⇒ **reject**, never truncate. |
| **D10 — Open-redirect protection on `rd`** | `rd` is attacker-controlled and lands the user after login — and a *path-relative* value can still build a login loop (AR-12). | `rd` must be a **path-relative** target: must start with a single `/`, must not start with `//` or `/\`, must not contain control chars, must not contain a scheme. Additionally **`/oauth2/*` and `/fe/api/auth/me` are rejected** (a `rd=/oauth2/start` would mint a fresh state and re-enter the IdP ⇒ unbounded loop). Otherwise ⇒ fall back to `/ui/`. |
| **D11 — The gate distinguishes browser traffic from API traffic** | A browser should be redirected (302); an XHR should get a machine-readable 401 so the SPA can act. | `middleware.go`: `/` and `/ui/*` ⇒ 302 to `/oauth2/start?rd=…`; `/fe/api/*` ⇒ 401 `application/json` `{"error":"unauthenticated"}`. **Addendum (AR-13, ruling R-1): `GET /fe/api/version` (exact) is EXEMPT** (class 4b, alongside `/fe/api/auth/me`) — the house deploy gate G4 is a pod-direct probe of that exact path (`curl -s 127.0.0.1:4321/fe/api/version` must equal `{"version":"<EXPECTED_RUN>"}`), and gating it breaks the release gate for no security gain (the build number is already public via image tags and the public repo). Posture delta recorded: in internal mode the build number is visible to an unauthenticated caller. Test: `TestMiddleware_VersionIsExempt`. |
| **D12 — The FE never redirects into an infinite loop** | A 401 handler that redirects, whose target also 401s, would loop. | `guardedFetch` uses a module-level "redirect already initiated" latch and skips the auth probe endpoint itself. |
| **D13 — SSE must not spin on an expired session** | `EventSource` cannot read HTTP status codes; a 401 looks like a generic network error and the existing backoff would reconnect forever. | `useEvents.ts` performs a one-shot `/fe/api/auth/me` probe on `onerror`; a 401 ⇒ stop reconnecting + redirect. A transient network error ⇒ the existing backoff path is untouched. |
| **D14 — v203 token authorization is untouched** | The ADR `token-authz-allowed-groups/adr-token-permission-model.md` is frozen; the internal principal is a **separate** concept from an API token. | The OAuth2 principal is never written into `auth.AuthToken`, never used by `authz.Resolve`, never granted model/credential reach. |
| **D15 — The edge↔app handover is a THREE-STATE chart model (replaces the binary mutual-exclusion rule)** | ADR-001 (council consensus, 2026-11-19): "both layers active" is a **designed, probe-gated transition**, not a failure. The chart renders **edge-only** (default) / **overlap** / **internal-only**; the application keeps only `dormant` / `enforce` (no new app mode, no trusted header, no delegation hop, no foreign-cookie parsing — OQ-5 stands unamended) (AR-22, ruling R-7). | Chart states: **edge-only** = `internalOAuth2.enabled=false` (today's render); **overlap** = `internalOAuth2.enabled=true` **and** `oauth2Proxy.enabled=true` **and** `overlap=true` ⇒ edge annotations **stay**, the app env renders, `/oauth2` → app; **internal-only** = `internalOAuth2.enabled=true` **and** `oauth2Proxy.enabled=false` **and** `oauth2Proxy.replicas=0` ⇒ annotations drop, edge objects **stay rendered at 0 replicas** (never pruned — Helm 3 prunes release-managed objects, which would destroy the rollback anchor, AR-21). The render-time `fail` is kept **only** for genuinely contradictory input: `internalOAuth2.enabled=true` **and** `oauth2Proxy.enabled=true` **without** `overlap=true`. Each state gets byte-level render assertions (§8.4 H-series). |
| **D16 — A future deferred bearer mode (not in Phase 1) has a pinned contract** | Phase 1 is browser-session only; this invariant pre-commits the shape so a later mode cannot be bolted on loosely (AR-37). | A future `/fe/api/*` bearer mode MUST use a **different audience** (e.g. `aud="llmproxy-admin-api"`), MUST never be accepted on `/v1/*`, MUST never accept an `sk-` token, MUST answer 401 with `WWW-Authenticate: Bearer realm="llmproxy-admin", error="invalid_token"` **plus** the bare `{"error":…}` body, MUST have TTL ≤ 15 min, and MUST never be written into `auth.AuthToken`. |
| **D17 — Handover invariants (ADR-001)** | The interlock is valid only if these hold on both hosts (ADR-001 §3.4). | **I-1 Loop** — whenever the app enforces, `/oauth2/*` MUST route to the app. **I-2 Exposure** — edge paths/annotations may drop only after **every** serving pod enforces. **I-3 Liveness** — the activation that mounts enforcement MUST mount the secret env in the same act (never a bare `env.OAUTH2_ENABLED` flip). Phase 1 ships dormant, so all three are inert here; they bind the chart states this phase introduces. |

---

## 4. Environment Configuration Specification

### 4.1 Precedence & isolation rules

1. **All OAuth2 settings come from `os.Getenv` ONLY.** They are never added to `pkg/config.Config`, never persisted, never returned by `/fe/api/config` (D3).
2. **`APPLY_ENV_OVERRIDES` is irrelevant here.** The production k8s deployment does **not** set it (`k8s/values.yaml:50-54` sets only `LOG_LEVEL`, `SSE_HEARTBEAT_ENABLED`, `MCP_ENABLED`; `cmd/main.go:393` reads `MCP_ENABLED` via unconditional `os.Getenv`). The OAuth2 layer therefore reads `os.Getenv` unconditionally, exactly like `MCP_ENABLED` — so the values work in production without any config-file plumbing.
3. `pkg/config`'s env-override block (`pkg/config/config.go:557-655`) is **not** extended — that would put OAuth2 fields into `config.Config` and violate D3.

### 4.2 Environment variable table

| Env var | Required when enabled | Default | Semantics / validation |
|---------|------------------------|---------|------------------------|
| `OAUTH2_ENABLED` | — | `false` | Master toggle. Enabled iff the value is exactly `true` (case-insensitive after `strings.TrimSpace`). Any other value ⇒ dormant (D1). |
| `OAUTH2_ISSUER_URL` | **yes** | — | OIDC issuer base URL, e.g. `https://door.daoduc.org`. Must parse as an absolute `https` URL (or `http` only when `OAUTH2_ALLOW_INSECURE_ISSUER=true`, default false). Trailing `/` is stripped. **Startup fail-fast** if missing/malformed. |
| `OAUTH2_CLIENT_ID` | **yes** | — | OIDC client id. **Startup fail-fast** if missing/empty. |
| `OAUTH2_CLIENT_SECRET` | yes (PKCE public clients may omit) | — | OIDC client secret. If empty, the token request uses the `client_id` only (public client + PKCE). Never logged. |
| `OAUTH2_REDIRECT_URL` | **yes, unless `OAUTH2_EXTERNAL_URL` is set** | derived from `OAUTH2_EXTERNAL_URL` | Absolute callback URL (`https://<host>/oauth2/callback`). **Canonical precedence (Note 3): `OAUTH2_REDIRECT_URL` WINS when BOTH are set** (it is the more specific value); when only `OAUTH2_EXTERNAL_URL` is set, the callback is **derived** as `<OAUTH2_EXTERNAL_URL>/oauth2/callback`; when **neither** is set ⇒ **startup fail-fast**. The per-request derivation branch is **DELETED** (AR-03): `Host` and `X-Forwarded-Proto` are client-spoofable, and a spoofed `Host` yields an attacker-chosen `redirect_uri` in both the authorization request and the token exchange. |
| `OAUTH2_EXTERNAL_URL` | **yes, unless `OAUTH2_REDIRECT_URL` is set** | — | Public base URL of the deployment (e.g. `https://llm.daoduc.org`), used to build the redirect URL, to validate `rd` targets, and to pin `post_logout_redirect_uri`. **Mandatory when enabled** (AR-03, ruling R-5) — no security decision may be derived from a request-controlled header. **Canonical precedence (Note 3):** `OAUTH2_REDIRECT_URL` wins when both are set; `OAUTH2_EXTERNAL_URL` alone ⇒ callback derived as `<external>/oauth2/callback`; neither ⇒ fail-fast. Test: `TestConfig_EnabledRequiresExternalURL`, `TestConfig_RedirectURLWinsOverExternalURL`. |
| `OAUTH2_COOKIE_SECRET` | **yes** | falls back to `COOKIE_SECRET` | Session-cookie encryption secret. See §4.3. **Startup fail-fast** if neither is present/valid. |
| `OAUTH2_SESSION_TTL` | no | `24h` | Session lifetime. Parsed as a Go duration; clamped to `[5m, 24h]`. Also the cookie `Max-Age`. **Decision (planner OQ-3, 2026-11-19): the default is 24 h**, matching today's ingress `--cookie-expire=24h` (`k8s/templates/oauth2-proxy-deployment.yaml:27`) so the cutover does not silently change the operator's re-login cadence; tightening later is an explicit env change, never a silent default shift. |
| `OAUTH2_COOKIE_NAME` | no | `llmproxy_oauth2` | Session cookie name. **MUST NOT** be `_oauth2_proxy` (that name belongs to the ingress `oauth2-proxy` deployment — a collision would make the two layers overwrite each other's cookie). Rejected at startup if it equals `_oauth2_proxy`. |
| `OAUTH2_COOKIE_SECURE` | no | **`true`** | `true`/`false`/`auto`. **The default is `true`** (AR-03): the previous "auto" default derived `Secure` from the request scheme, i.e. from a spoofable header ⇒ the session cookie could travel in cleartext on an https deployment. **Canonical rule (Note 2):** `OAUTH2_COOKIE_SECURE=auto` is honoured **only** when the explicit opt-in env `OAUTH2_COOKIE_SECURE_AUTO_OK=true` is ALSO set — the opt-in requires a documented ingress that terminates TLS and sets a trusted scheme indicator (otherwise `auto` is treated as `true`). `auto` without the opt-in ⇒ **startup fail-fast** (never a silent fallback). `false` on an https deployment ⇒ **startup fail-fast**. |
| `OAUTH2_COOKIE_SAMESITE` | no | `Lax` | `Lax`/`Strict`/`None`. `None` requires `Secure=true` (else browsers drop the cookie) — rejected at startup. `Lax` is required for the txn cookie (the callback is a top-level cross-site GET). |
| `OAUTH2_EMAIL_DOMAINS` | no | `*` | Comma-separated allowlist of email domains (`*` = allow any). A user whose email domain is not allowed ⇒ 403 at the callback (fail closed). **AR-04:** `*` additionally requires the explicit `OAUTH2_ALLOW_ANY_EMAIL_DOMAIN=true` (see below); an explicitly empty value ⇒ **startup fail-fast** (never silently treated as `*`). |
| `OAUTH2_ALLOW_ANY_EMAIL_DOMAIN` | no | `false` | **AR-04:** required to be `true` when `OAUTH2_EMAIL_DOMAINS` is `*` (i.e. "any Casdoor account may sign in"). With `*` **and** this flag unset ⇒ **startup fail-fast**; with both set ⇒ a **startup WARN** is logged. The domain allowlist MUST be set at cutover pre-flight. |
| `OAUTH2_REQUIRE_EMAIL_VERIFIED` | no | **`true`** | **AR-04 (leader posture):** `email_verified == true` is required **strictly from Phase 1**. An id_token with the claim **absent** ⇒ 403 (fail closed) **unless** the allowlist is `*` **and** `OAUTH2_ALLOW_ANY_EMAIL_DOMAIN=true`. |
| `OAUTH2_ALLOWED_ALGS` | no | **`RS256`** | JWT `alg` allowlist. **AR-06:** the default is narrowed to `RS256` — the live Casdoor discovery document advertises `id_token_signing_alg_values_supported: [RS256, RS512, ES256, ES384, ES512]` (no RS384, no EdDSA) and the live JWKS key is `{"kty":"RSA","kid":"cert-built-in","alg":"RS256","use":"sig"}` (RSA-2048), so every extra alg is an unexercised verifier path. `HS*` and `none` are rejected **case-insensitively** regardless of this value. A **WARN** is logged when the effective set differs from the default. |
| `OAUTH2_JWKS_CACHE_TTL` | no | `1h` | JWKS cache lifetime before a background refresh. |
| `OAUTH2_JWKS_MIN_REFRESH_INTERVAL` | no | `60s` | Minimum interval between **forced** refreshes triggered by a `kid` miss (D7). |
| `OAUTH2_JWKS_STALE_GRACE` | no | `2 × OAUTH2_JWKS_CACHE_TTL` | **AR-26:** the grace window during which the **previous** key set keeps serving after a *failed* refresh. The grace covers **fetch failures only** — a **successful** refresh that drops a rotated-away `kid` invalidates old id_tokens **by design**. |
| `OAUTH2_DISCOVERY_TIMEOUT` | no | `5s` | **AR-24:** per-request timeout for the OIDC discovery fetch; clamped to `[1s, 30s]`, validated in `ConfigFromEnv`. |
| `OAUTH2_TOKEN_EXCHANGE_TIMEOUT` | no | `10s` | **AR-09:** per-request timeout for the authorization-code token POST; clamped to `[1s, 60s]`. A slow IdP must never hold the callback goroutine or the browser indefinitely. |
| `OAUTH2_USERINFO_TIMEOUT` | no | `5s` | **AR-09:** per-request timeout for the optional userinfo GET; clamped to `[1s, 30s]`. |
| `OAUTH2_ALLOWED_ENDPOINTS` | no | *(empty)* | **AR-05 escape hatch:** a comma-separated list of **additional** origins (scheme+host+port) permitted to host discovery/JWKS/token/userinfo/end-session endpoints. Empty ⇒ every advertised endpoint MUST be same-origin with `OAUTH2_ISSUER_URL`, else a hard error. |
| `OAUTH2_LOGOUT_REDIRECT` | no | *(empty)* | **AR-36:** absolute path (under `OAUTH2_EXTERNAL_URL`) the browser is sent to after logout. Empty ⇒ `/ui/`. **Pinned**: it MUST be a path under the configured external URL — no absolute foreign URL is accepted. |
| `OAUTH2_STATE_STORE_MAX` | no | `1024` | **S1 (decided: KEEP):** cap on the in-memory `/oauth2/start` state store (max records). Overflow ⇒ the oldest is evicted **once**, then further overflows answer **429** on `/oauth2/start` (§5.6, §9.1 `statestore_test.go`). Clamped to `[64, 65536]` in `ConfigFromEnv`. This is the single source of truth for the cap; §5.6 and the chart (via `OAUTH2_STATE_STORE_MAX` env) reference this value. |
| `OAUTH2_RATE_LIMIT_RPM` | no | `10` | **N1 (approver):** per-IP token bucket sustained rate (requests/min) applied to `/oauth2/start` **and** `/oauth2/callback` combined. Key = the host part of `RemoteAddr`. In-memory per-pod (consistent with the single-replica invariant, AR-11). Clamped to `[1, 600]`. |
| `OAUTH2_RATE_LIMIT_BURST` | no | `20` | **N1 (approver):** per-IP token bucket burst size. Clamped to `[1, 100]`. A request exceeding the bucket ⇒ **429** with `Retry-After`. Tested in §9.1 (`TestRateLimit_PerIP429`). |
| `COOKIE_SECRET` | — | — | **Compatibility fallback** for `OAUTH2_COOKIE_SECRET` (§4.3). This is the key that already exists in the k8s secret `oauth2-proxy-secret` (`k8s/secrets-example.yaml:19`), so the existing secret is reusable **without regeneration**. |

### 4.3 `COOKIE_SECRET` compatibility (§4.3 of the commission brief)

The existing k8s secret `oauth2-proxy-secret` carries `CLIENT_ID`, `CLIENT_SECRET`, `COOKIE_SECRET` (`k8s/secrets-example.yaml:12-20`) and is consumed by the ingress `oauth2-proxy` (`k8s/templates/oauth2-proxy-deployment.yaml:24` → `--cookie-secret=$(COOKIE_SECRET)`). The internal layer must accept that value **as-is**.

**Derivation ladder (deterministic across every base64 dialect; AR-32/AR-63):**

```
raw = first non-empty of (OAUTH2_COOKIE_SECRET, COOKIE_SECRET)
if raw == ""                                -> CONFIG ERROR (fail-fast at startup when enabled)
if b, err := base64.StdEncoding.DecodeString(raw);    err == nil && len(b) == 32 -> AES key = b   (mode "base64-std-32")
else if b, err := base64.RawStdEncoding.DecodeString(raw); err == nil && len(b) == 32 -> AES key = b (mode "base64-std-32")
else if b, err := base64.URLEncoding.DecodeString(raw); err == nil && len(b) == 32 -> AES key = b  (mode "base64-url-32")
else if b, err := base64.RawURLEncoding.DecodeString(raw); err == nil && len(b) == 32 -> AES key = b (mode "base64-url-32")
else if len(raw) >= 16                      -> AES key = SHA-256(raw)                (mode "sha256-passphrase")
else                                        -> CONFIG ERROR (passphrase shorter than 16 bytes)
```

- **All modes are 32 bytes** ⇒ AES-256 in every case.
- The ladder is **deterministic and total**: the first matching rung wins, in the order above; the same secret string always yields the same key ⇒ sessions survive a pod restart (the key is env-derived, not random).
- The **mode string** is the only thing logged — never the value, never a length. (AR-63)
- The **passphrase branch requires ≥ 16 bytes** of entropy-bearing input; a shorter value is a config error, not a silently weak key (AR-32).
- **Rotation caveat (documented in code):** changing the *format* of the same secret (e.g. re-encoding the same bytes) changes the derived key ⇒ all sessions are invalidated. That is the intended rotation semantics.
- Because our cookie is `llmproxy_oauth2` (D: not `_oauth2_proxy`) and our AAD/format is ours alone, **no cookie is interoperable with the ingress `oauth2-proxy` cookie** — by design. The reuse is of the **secret material**, not of the session format.
- **Decision (planner OQ-5, 2026-11-19): no cross-format compatibility attempt.** A forced re-login at cutover is **accepted** — one code path, one cookie format; accepting a foreign cookie format would widen the attack surface for no operational gain. Recorded as decision item 4 in §15.
- **Key separation (AR-32, ruling R-9, B3):** the internal layer derives **its own** AES-256 root key from `OAUTH2_COOKIE_SECRET` (preferred) — never from the shared edge secret when the dedicated var is present — and then derives **EXACTLY 2 per-purpose HKDF subkeys** from that root (`crypto/hkdf`, stdlib, Go 1.26): `session` (session cookie) and `txn` (transaction cookie, D6a). There is **NO third** "state-digest" subkey — `TxnDigest` is plain SHA-256 over the **full sealed txn cookie value**, not HKDF output. A txn ciphertext pasted under the session-cookie name therefore fails closed (wrong subkey ⇒ GCM auth failure). One leak of the shared edge secret must not compromise both layers.

**Authoritative log surface (AR-39 — one statement, reconciled across §4.3/§4.4/§5.6):**

| Surface | Content | Never |
|---------|---------|-------|
| stdout, boot | `mode=`, `issuer=`, `client_id=`, `cookie=`, `ttl=`, `secret_mode=`, `algs=`, `email_domains=` (mode string, never the list values) | any secret value, any key material |
| stdout, login | INFO: `sub` + email **domain** only | the id_token, the access token, the authorization code, the cookie value, the txn cookie value, the state value, the client secret |
| stdout, failure | ERROR: a stable error **code** (`invalid_state`, `token_exchange_failed`, `jwks_unavailable`, …) + a request id | the same forbidden values as above |
| **events bus** | **NO OAuth2 event is ever published** | `/fe/api/events` therefore carries **only** proxy telemetry — the SSE stream is unchanged by this feature (verified by the FE/UI regression suites) |

### 4.4 Startup fail-fast vs lazy OIDC discovery (resilience split)

| Concern | Policy | Reason |
|---------|--------|--------|
| Missing/invalid `OAUTH2_ISSUER_URL`, `OAUTH2_CLIENT_ID`, cookie secret, bad `SameSite`/`Secure` combination, `HS*`/`none` in the allowlist, `OAUTH2_COOKIE_NAME=_oauth2_proxy`, **missing `OAUTH2_EXTERNAL_URL`/`OAUTH2_REDIRECT_URL` (AR-03)**, **`OAUTH2_EMAIL_DOMAINS=*` without `OAUTH2_ALLOW_ANY_EMAIL_DOMAIN=true` (AR-04)**, an explicitly empty `OAUTH2_EMAIL_DOMAINS`, `OAUTH2_COOKIE_SECURE=false` on an https external URL | **Startup fail-fast** — `log.Fatalf` ⇒ process exits non-zero with a precise message | A misconfigured gate that silently falls open is worse than a crash-loop: the operator sees the pod CrashLoopBackOff instead of an unauthenticated admin API. |
| Effective `OAUTH2_ALLOWED_ALGS` ≠ the default (`RS256`) | **WARN**, not fatal | The set may legitimately be widened (live Casdoor advertises RS512/ES256/ES384/ES512) — but the operator must see it (AR-06). |
| `OAUTH2_EMAIL_DOMAINS=*` **with** `OAUTH2_ALLOW_ANY_EMAIL_DOMAIN=true` | **WARN** | "Any Casdoor account may sign in" is a posture the operator must be reminded of (AR-04). |
| OIDC discovery document unreachable at boot | **Do NOT fail startup.** Discovery is **lazy**: attempted on the first `/oauth2/start` and cached for `OAUTH2_JWKS_CACHE_TTL`. Failures return `502 {"error":"oidc_discovery_unavailable"}` and are retried with a 30 s backoff. | The proxy's *client* API (`/v1/*`) must keep serving even when the IdP is down. The gate is on the operator surface only; a hard boot dependency would take the LLM data plane offline for an auth-side outage. |
| JWKS fetch failure at first use | Lazy + cached + rate-limited refresh (D7) ⇒ 503 `{"error":"jwks_unavailable"}` | Same reasoning. |
| JWKS fetch failure **after** a successful load | Serve from cache until the TTL expires; on expiry, a failed refresh keeps serving the **stale** cache for the grace window `OAUTH2_JWKS_STALE_GRACE` (default 2 × TTL) and logs a warning | IdP outage must not log everyone out. |
| A **successful** refresh that drops a rotated-away `kid` | Old id_tokens signed by the removed key become invalid **by design** — the grace window covers **fetch failures only**, never key removal (AR-26) | A removed key means the IdP deliberately stopped signing with it. |
| Session validation while the IdP is down | **Fully offline** (AES-GCM + `exp`); the IdP is touched **only** at `/oauth2/start`, `/oauth2/callback`, and best-effort `/oauth2/logout` — never mid-session (AR-27) | An IdP outage must not invalidate live sessions or block the operator surface for already-signed-in users. |
> **AR-26 AMENDMENT (reviewer, 2026-11-19):** an **EMPTY or wholly-unusable** key set counts as a **FETCH ERROR** under the stale-grace window (the previous keys keep serving; once the window lapses the client sees the retryable `503 jwks_unavailable`) — **NOT** a rotation wipe. The wipe semantics apply **ONLY** to a successful rotation yielding **≥ 1 usable key**.

**Startup log line (never contains a secret):**
`[oauth2] internal OAuth2 ENABLED mode=internal issuer=https://door.daoduc.org client_id=<id> cookie=llmproxy_oauth2 ttl=24h secret_mode=sha256-passphrase algs=RS256 email_domains=restricted`
and in dormant mode exactly one line: `[oauth2] internal OAuth2 DISABLED (set OAUTH2_ENABLED=true to enable)`.

---

## 5. Go `pkg/oauth2/` Package Architecture

**Package rule:** `pkg/oauth2` imports **stdlib only** plus the neutral leaf package **`pkg/identity`** (never `pkg/proxy`, `pkg/ui`, `pkg/store`, or `pkg/auth`); `pkg/ui` imports **`pkg/identity`** but **never `pkg/oauth2`**; `cmd` is the only package that imports both and wires `*oauth2.Guard` → `identity.Provider` at the composition root. The dependency arrow is `pkg/ui → pkg/identity ← pkg/oauth2`, with `cmd` on top — so neither side can reach the other, and the import guards below stay enforceable. Add two Makefile import guards mirroring the house style at `Makefile:44-62`: (a) `pkg/oauth2` must not import `pkg/proxy`/`pkg/ui`/`pkg/store` (unchanged), and (b) `pkg/ui` must not import `pkg/oauth2` (unchanged). `pkg/identity` imports **nothing** from the repo — it is a leaf.

**File layout (N2, approver):** one paragraph pinning which type lives where, so the composition is not re-derived at implementation time:
- **`pkg/identity/identity.go`** (leaf) — `Identity` struct + `Provider` interface only. No logic.
- **`pkg/oauth2/handler.go`** — the **flow endpoints** (`/oauth2/start`, `/oauth2/callback`, `/oauth2/logout`), the `stateStore`, the `stateRecord`, the `Handler` struct, and the `identity.Provider` adaptation (`Enabled()`/`Current()`). Owns the route-gating **decision**? No — see below.
- **`pkg/oauth2/middleware.go`** — the **gate**: `Middleware` + `Wrap`, the path **classification** table (§5.7, fail-closed), the `Guard` adapter, the `Wrap`/`RegisterRoutes` entry points. Owns the route-gating **mechanism** (the per-request pass/gate decision) but **not** the flow endpoints.
- **`pkg/oauth2/config.go`** — env → typed `Config`, validation, `DeriveCookieKey`, `deriveSubkeys`.
- **`pkg/oauth2/cookie.go`** — `CookieCodec`, `Seal`/`Open`, `SetCookie`/`ClearCookie`, the HKDF subkeys.
- **`pkg/oauth2/oidc.go`** — `OIDCProvider`, `Discover`, `ExchangeCode`, `FetchUserinfo`.
- **`pkg/oauth2/jwks.go`** — `JWKSet`/`JWK`, `JWKSCache`, `Key`.
- **`pkg/oauth2/jwt.go`** — `Claims`, `Verifier`, `Verify`.
- **`pkg/ui/server.go`** (existing) — registers `/fe/api/auth/me` and holds an `identity.Provider` via `SetIdentityProvider` (nil-safe).
- **`cmd/main.go`** (existing) — the **only** file that imports both `pkg/oauth2` and `pkg/ui`; wires `*oauth2.Guard` → `identity.Provider` via the explicit nil-safe var pattern (B4).

### 5.1 `pkg/oauth2/config.go` — env → typed config

```go
package oauth2

const (
    EnvEnabled               = "OAUTH2_ENABLED"
    EnvIssuerURL             = "OAUTH2_ISSUER_URL"
    EnvClientID              = "OAUTH2_CLIENT_ID"
    EnvClientSecret          = "OAUTH2_CLIENT_SECRET"
    EnvRedirectURL           = "OAUTH2_REDIRECT_URL"
    EnvExternalURL           = "OAUTH2_EXTERNAL_URL"
    EnvCookieSecret          = "OAUTH2_COOKIE_SECRET"
    EnvCookieSecretFallback  = "COOKIE_SECRET"
    EnvSessionTTL            = "OAUTH2_SESSION_TTL"
    EnvCookieName            = "OAUTH2_COOKIE_NAME"
    EnvCookieSecure          = "OAUTH2_COOKIE_SECURE"
    EnvCookieSameSite        = "OAUTH2_COOKIE_SAMESITE"
    EnvEmailDomains          = "OAUTH2_EMAIL_DOMAINS"
    EnvAllowAnyEmailDomain   = "OAUTH2_ALLOW_ANY_EMAIL_DOMAIN"
    EnvRequireEmailVerified  = "OAUTH2_REQUIRE_EMAIL_VERIFIED"
    EnvAllowedAlgs           = "OAUTH2_ALLOWED_ALGS"
    EnvJWKSCacheTTL          = "OAUTH2_JWKS_CACHE_TTL"
    EnvJWKSMinRefresh        = "OAUTH2_JWKS_MIN_REFRESH_INTERVAL"
    EnvJWKSStaleGrace        = "OAUTH2_JWKS_STALE_GRACE"
    EnvDiscoveryTimeout      = "OAUTH2_DISCOVERY_TIMEOUT"
    EnvTokenExchangeTimeout  = "OAUTH2_TOKEN_EXCHANGE_TIMEOUT"
    EnvUserinfoTimeout       = "OAUTH2_USERINFO_TIMEOUT"
    EnvAllowedEndpoints      = "OAUTH2_ALLOWED_ENDPOINTS"
    EnvLogoutRedirect        = "OAUTH2_LOGOUT_REDIRECT"
    EnvStateStoreMax         = "OAUTH2_STATE_STORE_MAX"
    EnvRateLimitRPM          = "OAUTH2_RATE_LIMIT_RPM"
    EnvRateLimitBurst        = "OAUTH2_RATE_LIMIT_BURST"
    EnvCookieSecureAutoOptIn = "OAUTH2_COOKIE_SECURE_AUTO_OK"   // explicit opt-in for the "auto" path (Note 2)
    EnvAllowInsecureIssuer   = "OAUTH2_ALLOW_INSECURE_ISSUER"
)

// Config is the fully-validated OAuth2 configuration. It is NEVER
// serialized to /fe/api/config (D3) and never persisted.
type Config struct {
    Enabled           bool
    IssuerURL         string
    IssuerOrigin      string   // scheme://host:port of IssuerURL — the pinning anchor (AR-05)
    ClientID          string
    ClientSecret      string
    RedirectURL       string   // MANDATORY when enabled (AR-03) — never request-derived
    ExternalURL       string   // MANDATORY when enabled (AR-03)
    CookieName        string
    TxnCookieName     string   // "llmproxy_oauth2_txn" (AR-01/D6a)
    CookieSecure      bool     // resolved; DEFAULT TRUE (AR-03)
    CookieSecureAuto  bool     // explicit opt-in only; requires a documented trusted scheme indicator
    CookieSameSite    http.SameSite
    SessionTTL        time.Duration
    TxnTTL            time.Duration // 600s (10 min) — the txn cookie Max-Age
    EmailDomains      []string // nil/["*"] = allow any (requires EnvAllowAnyEmailDomain)
    AllowAnyDomain    bool
    RequireEmailVerified bool // DEFAULT TRUE (AR-04)
    AllowedAlgs       map[string]struct{}
    AllowedEndpoints  []string // escape hatch for AR-05 (empty = strict same-origin)
    LogoutRedirect    string   // pinned path under ExternalURL (AR-36)
    JWKSCacheTTL      time.Duration
    JWKSMinRefresh    time.Duration
    JWKSStaleGrace    time.Duration
    StateStoreMax      int   // OAUTH2_STATE_STORE_MAX, clamped [64, 65536] (S1)
    RateLimitRPM       int   // OAUTH2_RATE_LIMIT_RPM, clamped [1, 600], default 10 (N1)
    RateLimitBurst     int   // OAUTH2_RATE_LIMIT_BURST, clamped [1, 100], default 20 (N1)
    CookieSecureAutoOK bool   // explicit opt-in for the "auto" Secure path (Note 2)
    DiscoveryTimeout  time.Duration
    TokenExchangeTimeout time.Duration
    UserinfoTimeout   time.Duration
    AllowInsecure     bool
    SecretMode        string   // "base64-std-32" | "base64-url-32" | "sha256-passphrase" — logged, never the value
    rootKey           []byte   // 32-byte AES root key — never exported, never logged
    sessionKey        []byte   // HKDF subkey: session cookie
    txnKey            []byte   // HKDF subkey: transaction cookie (D6a)
}

// ConfigFromEnv is THE constructor: it parses and validates the environment.
// Returns (nil, nil) when the feature is DISABLED (dormant) — the caller
// then installs NO middleware and registers NO /oauth2/* routes (D1).
// Returns a non-nil error only for a REAL misconfiguration of an ENABLED
// deployment (fail-fast, §4.4).
func ConfigFromEnv() (*Config, error)

// RateLimit returns the per-IP token-bucket settings (N1): the sustained
// requests-per-minute and the burst size for /oauth2/start + /oauth2/callback
// combined. This is the ONLY accessor the handler uses; the raw fields are
// unexported in the implementation.
func (c *Config) RateLimit() (rpm, burst int)

// StateStoreMaxRecords returns the capped state-store size (S1).
func (c *Config) StateStoreMaxRecords() int

// ExternalBaseURL returns the configured public base URL (mandatory when
// enabled — Note 3 precedence).
func (c *Config) ExternalBaseURL() string

// CallbackURL returns the configured redirect URI (mandatory when enabled —
// Note 3 precedence).
func (c *Config) CallbackURL() string

// DeriveCookieKey implements §4.3 (the 6-rung ladder). Exported for unit tests only.
func DeriveCookieKey(raw string) (key []byte, mode string, err error)

// deriveSubkeys returns EXACTLY 2 subkeys — "session" and "txn" (B3, leader
// pre-ruling). There is NO third "state-digest" subkey: TxnDigest is plain
// SHA-256 over the FULL sealed txn cookie value, not HKDF output.
func deriveSubkeys(root []byte) (session, txn []byte, err error)
```

**Validation rules (all produce a precise error string, never a secret value):**

1. `Enabled` ⇒ `IssuerURL`, `ClientID`, a cookie key, **and `OAUTH2_EXTERNAL_URL` or `OAUTH2_REDIRECT_URL`** are **mandatory** (AR-03 — no security decision may come from a request header). **Canonical precedence (Note 3):** `OAUTH2_REDIRECT_URL` wins when both are set; `OAUTH2_EXTERNAL_URL` alone ⇒ callback derived as `<external>/oauth2/callback`; neither ⇒ fail-fast. Test: `TestConfig_EnabledRequiresExternalURL`, `TestConfig_RedirectURLWinsOverExternalURL`.
2. `IssuerURL` must be absolute; scheme `https` unless `AllowInsecure`; host non-empty; query/fragment forbidden; trailing `/` stripped. `IssuerOrigin` = `scheme://host:port` (default port elided) — the pinning anchor for AR-05.
3. `AllowedAlgs` — any entry in the `HS*` family or `none` (matched **case-insensitively**) ⇒ hard error (D5, AR-06). An empty list ⇒ the default set, which is now **`RS256`** (AR-06).
4. `CookieName` must not be `_oauth2_proxy` and must match `^[A-Za-z0-9_-]{1,64}$`. `TxnCookieName` is fixed at `llmproxy_oauth2_txn` and must also differ from `CookieName` (AR-01).
5. `CookieSameSite=None` requires `Secure` ⇒ error otherwise. `CookieSecure` defaults **true**; **`auto` is honoured only when `OAUTH2_COOKIE_SECURE_AUTO_OK=true` is also set (Note 2's canonical rule)** — `auto` without the opt-in ⇒ error; `false` with an `https` `ExternalURL` ⇒ error (AR-03).
6. `SessionTTL` clamped to `[5m, 24h]`; `JWKSCacheTTL` clamped to `[1m, 24h]`; `JWKSMinRefresh` clamped to `[5s, 10m]`; `JWKSStaleGrace` clamped to `[1m, 48h]` and defaults to 2 × `JWKSCacheTTL` (AR-26); `DiscoveryTimeout` clamped to `[1s, 30s]` (default 5 s, AR-24); `TokenExchangeTimeout` clamped to `[1s, 60s]` (default 10 s, AR-09); `UserinfoTimeout` clamped to `[1s, 30s]` (default 5 s, AR-09).
7. `OAUTH2_EMAIL_DOMAINS` explicitly set to the empty string ⇒ error ("explicitly empty is not a wildcard"). `*` **requires** `OAUTH2_ALLOW_ANY_EMAIL_DOMAIN=true`, else error (AR-04); with both set ⇒ startup WARN.
8. `OAUTH2_LOGOUT_REDIRECT` must be a **path** (starting `/`) under `ExternalURL` — an absolute foreign URL is rejected (AR-36).
9. `OAUTH2_ALLOWED_ENDPOINTS` entries must be absolute `scheme://host[:port]` origins (AR-05).
10. `OAUTH2_STATE_STORE_MAX` clamped to `[64, 65536]`, default 1024 (S1).

### 5.2 `pkg/oauth2/cookie.go` — AES-256-GCM session cookie

```go
// Session is the minimal principal carried by the cookie.
// KEEP IT SMALL (D9): every extra byte is cookie budget.
type Session struct {
    Sub         string `json:"sub"`            // ≤ 64 B, reject over
    Email       string `json:"email,omitempty"`// ≤ 254 B, reject over
    Name        string `json:"name,omitempty"` // ≤ 128 B, TRUNCATE over
    IssuedAt    int64  `json:"iat"`
    ExpiresAt   int64  `json:"exp"`
    // preferred_username is DELIBERATELY ABSENT (AR-33/AR-62):
    // live Casdoor's default token format does not emit it.
}

const MaxCookieBytes = 4096

// Claim caps (AR-33) — enforced at SEAL time.
const (
    MaxSubBytes   = 64
    MaxEmailBytes = 254
    MaxNameBytes  = 128
)

type CookieCodec struct {
    name string
    key  []byte // 32 bytes, AES-256 — the HKDF "session" subkey
}

// Seal encrypts the session: base64url(nonce || ciphertext||tag),
// AAD = cookie name (binds the ciphertext to its cookie name).
// Enforces the per-claim caps and the 4096-byte ENCODED budget AT SEAL TIME.
func (c *CookieCodec) Seal(s *Session) (string, error)

// Open decrypts + validates (expiry, size). Any error => "no session"
// (never a partial session).
func (c *CookieCodec) Open(value string) (*Session, error)

// SetCookie seals the session and writes it as a cookie. A seal failure is
// NEVER swallowed: no cookie is written and the error is returned so the
// caller can surface a stable code and refuse the login.
func (c *CookieCodec) SetCookie(w http.ResponseWriter, s *Session) error

// ClearCookie expires the cookie (MaxAge=-1, same Path/Secure/SameSite).
func (c *CookieCodec) ClearCookie(w http.ResponseWriter)
```

**Security properties:**
- 16-byte random nonce from `crypto/rand` (never reused — a fresh nonce per seal).
- `cipher.AEAD` from `crypto/aes` + `crypto/cipher` (GCM).
- AAD = cookie name ⇒ a ciphertext copied into another cookie name fails to open.
- **Per-purpose subkeys** (AR-01/AR-32, ruling R-9): the session cookie uses the HKDF **"session"** subkey, the transaction cookie the **"txn"** subkey ⇒ a txn ciphertext pasted under the session-cookie name fails closed (GCM auth failure), not merely "different AAD".
- **Seal-time budget (AR-33):** `sub`/`email` over their caps ⇒ **reject** (a malformed principal is not a display problem); the display name over its cap ⇒ **truncate**; the **encoded** value over 4096 B ⇒ **reject**, never truncate. `Open` re-checks the size and the caps.
- `Open` rejects: base64 decode failure, short ciphertext, GCM auth failure (tamper), `exp` in the past, payload > 4096 bytes.
- Cookie flags: `HttpOnly`, `Path=/`, `SameSite` per config, `Secure` **default true** (AR-03), `Max-Age` = remaining TTL.
- **The plaintext never contains the client secret, the token, or the raw ID token.** Only the minimal claims. The ID token is verified and then **discarded** (never persisted, never forwarded, never logged).

### 5.3 `pkg/oauth2/oidc.go` — discovery + token exchange

```go
type ProviderMetadata struct {
    Issuer                string `json:"issuer"`
    AuthorizationEndpoint string `json:"authorization_endpoint"`
    TokenEndpoint         string `json:"token_endpoint"`
    JWKSURI               string `json:"jwks_uri"`
    UserinfoEndpoint      string `json:"userinfo_endpoint"`
    EndSessionEndpoint    string `json:"end_session_endpoint"`
}

type OIDCProvider struct {
    cfg      *Config
    client   *http.Client   // ONE shared client, keep-alive transport (AR-09)

    mu       sync.RWMutex
    meta     *ProviderMetadata
    fetchedAt time.Time
    fetchErr  error
    lastAttempt time.Time
    sf        *singleflight   // re-armable single-flight around Discover (AR-08)
}

// Discover lazily fetches {issuer}/.well-known/openid-configuration.
// The returned Issuer MUST equal cfg.IssuerURL after IDENTICAL normalization
// on both sides (OIDC §4.3 / AR-34) — a mismatch means the discovery doc
// was served by a different tenant.
// Single-flighted: N concurrent starts during an IdP outage issue ONE fetch,
// and the failed result is NEGATIVELY CACHED atomically with the attempt (AR-08).
func (p *OIDCProvider) Discover(ctx context.Context) (*ProviderMetadata, error)

// ExchangeCode performs the token request (application/x-www-form-urlencoded)
// and returns the raw id_token + access_token.
// SINGLE ATTEMPT, ZERO RETRIES (AR-10): an authorization_code is single-use —
// a retry after a slow-but-successful first attempt burns the code.
func (p *OIDCProvider) ExchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (idToken, accessToken string, err error)

// FetchUserinfo is optional and best-effort. It is used ONLY when the
// verified id_token lacks an email; userinfo claims are accepted only if
// userinfo.sub == id_token.sub (AR-35).
func (p *OIDCProvider) FetchUserinfo(ctx context.Context, accessToken, idTokenSub string) (map[string]any, error)
```

**Discovery rules:**
- URL = `strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"`.
- **ONE shared `*http.Client`** for every IdP call, with a keep-alive `http.Transport` — never a client per request (AR-09). `Timeout` is left **unset** on the client; every call carries its own `context.WithTimeout` (discovery `OAUTH2_DISCOVERY_TIMEOUT`, token `OAUTH2_TOKEN_EXCHANGE_TIMEOUT`, userinfo `OAUTH2_USERINFO_TIMEOUT`). **Never `context.Background()`** — every IdP call uses a request-derived context.
- The client's `CheckRedirect` **rejects any cross-origin redirect** and never re-sends the credentials (AR-05). Same-origin redirects are followed with the credential headers stripped except for the same-origin case.
- Response body limited to **256 KiB** (`io.LimitReader`) — an IdP that streams forever must not exhaust memory.
- `meta.Issuer` ≠ `cfg.IssuerURL` ⇒ hard error (this is the classic mix-up/tenant-confusion guard). **Both sides are normalized identically** before comparison (trailing `/` stripped, scheme/host lower-cased, default port elided) and the error message prints both issuers and says "issuer mismatch" (AR-34).
- **Issuer-origin pinning (AR-05):** every advertised endpoint — `authorization_endpoint`, `token_endpoint`, `jwks_uri`, `userinfo_endpoint`, `end_session_endpoint` — MUST be **same-origin** (scheme+host+port) with the configured issuer, else a **hard error**. The only escape hatch is the explicit `OAUTH2_ALLOWED_ENDPOINTS` allowlist. This is the `client_secret`-exfiltration defence: Casdoor supports the `password` grant, so the reused secret is a live credential and must never be POSTed to a third-party origin.
- Lazy + cached; a failed fetch sets `fetchErr` **atomically with the attempt stamp** (negative caching — AR-08), is retried at most every 30 s, and surfaces `502 oidc_discovery_unavailable`.

**Token exchange rules:**
- POST `token_endpoint`, body: `grant_type=authorization_code`, `code`, `redirect_uri`, `client_id`, `code_verifier` (+ `client_secret` via HTTP Basic **or** form post, per the IdP — Casdoor accepts both; use form post for compatibility with Casdoor's `application/x-www-form-urlencoded` handler).
- **Single attempt, zero retries** (AR-10). A failure ⇒ `502 {"error":"token_exchange_failed"}` — **not** 504 (the upstream answered; the failure is semantic, not a timeout). The log carries only a stable error code, never the code/token/cookie.
- Response body limited to 1 MiB; `access_token` is used **only** if a userinfo endpoint is configured **and** the id_token lacks an email — otherwise the id_token alone is the source of truth.
- **userinfo binding (AR-35):** when userinfo IS used, `userinfo.sub` MUST equal the verified `id_token.sub`, else the login is **403**; the body is capped at 64 KiB; the id_token's email is preferred over the userinfo email.
- `id_token` is parsed and verified by `jwt.go`; a missing/invalid `id_token` ⇒ the login fails closed.

### 5.4 `pkg/oauth2/jwks.go` — JWKS fetch + cache + `kid` handling

```go
type JWKSet struct {
    Keys []JWK `json:"keys"`
}

type JWK struct {
    Kty string `json:"kty"`   // RSA | EC | OKP
    Kid string `json:"kid"`
    Use string `json:"use"`   // "sig" expected
    Alg string `json:"alg"`
    KeyOps []string `json:"key_ops"` // when present, MUST contain "verify" (AR-57)
    N   string `json:"n"`     // RSA modulus (base64url)
    E   string `json:"e"`     // RSA exponent (base64url)
    Crv string `json:"crv"`   // EC: P-256|P-384|P-521; OKP: Ed25519
    X   string `json:"x"`     // EC x / OKP public key
    Y   string `json:"y"`     // EC y
}

// singleflight is a RE-ARMABLE single-flight (AR-07):
// a *sync.Once fires once per process LIFETIME — after the first refresh
// the primitive is permanently spent, so later refreshes either run
// concurrently (thundering herd) or never run at all.
type singleflight struct {
    mu     sync.Mutex
    done   chan struct{}   // closed when the in-flight call finishes
    rearm  bool
}

func (s *singleflight) Do(ctx context.Context, key string, fn func(context.Context) error) error

type JWKSCache struct {
    mu       sync.RWMutex
    keys     map[string]crypto.PublicKey
    keyAlg   map[string]string   // kid -> the alg each JWK declares ("" when absent)
    fetchedAt time.Time
    lastRefresh time.Time
    sf        *singleflight   // ONE constant key: "jwks-refresh" (AR-07)
    grace    time.Duration   // OAUTH2_JWKS_STALE_GRACE (AR-26)
    ttl, minRefresh time.Duration
    fetch    func(ctx context.Context) (map[string]crypto.PublicKey, map[string]string, error)
}

// The fetch function returns the parsed keys keyed by kid AND the algorithm
// each key declares ("" when the JWK carries no "alg" member) — the declared
// alg is what pins a token's header alg to the key on the verify path
// (verify-path alg pin, AR-57).

func (c *JWKSCache) Key(ctx context.Context, kid, alg string) (crypto.PublicKey, error)

// DeclaredAlg returns the algorithm the key with the given kid declares
// ("" when the JWK carries no "alg" member).
func (c *JWKSCache) DeclaredAlg(kid string) string
```

**Rules:**
- Fetch `{jwks_uri}` with the same timeout/size limits; `keys` is keyed by `kid`. The JWKS URI itself is subject to the **issuer-origin pinning** of §5.3 (AR-05).
- **Key-type mapping (AR-06/AR-57) — `kty↔alg↔crv` pinned at parse time:**
  - `RSA` ⇒ `rsa.PublicKey{N: b64u(n), E: int(b64u(e))}` (reject `E < 3` or `E` even — the classic small-exponent attack). Accepted algs: `RS256/RS384/RS512`.
  - `EC` + `P-256/P-384/P-521` ⇒ `ecdsa.PublicKey{Curve, X, Y}`; the curve MUST match `crv`, and `crv↔alg` MUST agree (`P-256↔ES256`, `P-384↔ES384`, `P-521↔ES512`). Accepted algs: `ES256/ES384/ES512`.
  - `OKP` + `Ed25519` ⇒ `ed25519.PublicKey(b64u(x))` (32 bytes exactly). Accepted algs: `EdDSA`.
  - A `kty`/`alg`/`crv` combination outside those tables ⇒ the key is **skipped** (and logged), never coerced.
- **`JWK.alg`, when present, MUST equal the token header's `alg`** (AR-57); `key_ops`, when present, MUST contain `verify`. The verifier map **type-asserts per alg** (never a bare cast) — a mismatched key type is a hard error.
- **Unknown `kid` ⇒ rate-limited refresh (D7, AR-25):** the triggering request **and every concurrent waiter** block on the in-flight refresh (bounded by the request deadline); **only a too-soon miss** (i.e. the refresh interval has not elapsed) fails fast with `503` + `Retry-After`. TTL-expiry refreshes and kid-miss refreshes collapse onto **ONE constant single-flight key** (`"jwks-refresh"`) so they can never issue two fetches (AR-07).
- **Stale-while-error:** a failed refresh keeps the previous key set usable until `fetchedAt + OAUTH2_JWKS_STALE_GRACE` (default 2 × TTL), then the cache is considered empty and requests fail closed. The grace covers **fetch failures only** — a **successful** refresh that drops a rotated-away `kid` invalidates old id_tokens **by design** (AR-26).
- `use != "sig"` keys are skipped; `alg` present in the JWK must be in the allowlist.

### 5.5 `pkg/oauth2/jwt.go` — JWT verification (stdlib only)

```go
type Claims struct {
    Issuer    string   `json:"iss"`
    Subject   string   `json:"sub"`
    Audience  []string `json:"aud"`     // may be a string or an array — custom UnmarshalJSON
    ExpiresAt int64    `json:"exp"`
    NotBefore int64    `json:"nbf"`
    IssuedAt  int64    `json:"iat"`
    Nonce     string   `json:"nonce"`
    Email     string   `json:"email"`
    EmailVerified *bool `json:"email_verified"` // POINTER — absence is distinguishable from false (AR-04)
    Name      string   `json:"name"`
    TokenType string   `json:"typ"`   // id-token / at+jwt (AR-31, AR-55)
    AuthorizedParty string `json:"azp"` // MUST equal the client id when present (AR-30)
}

// Verify parses and verifies a compact JWS. Checks, in order:
//  1. three dot-separated segments, header/payload are valid base64url JSON
//  2. header.alg is in the allowlist (HS*/none impossible — D5, case-insensitive)
//  2b. header.crit present => REJECT; header.b64 == false => REJECT (AR-55)
//  3. header.typ, when present, MUST be "JWT" (case-insensitive match). ABSENT is also accepted.
//     REJECT typ:"at+jwt" (AR-31) — that is the ACCESS-token type (RFC 9068); accepting it
//     in the id-token path would blur id vs access tokens. Any other present value is rejected.
//     ("at+jwt" acceptance is a D16 / deferred-bearer-mode concern — NOT the id-token path.)
//  4. kid present -> key lookup (rate-limited refresh on miss)
//  5. JWK.alg (if present) MUST equal header.alg; key_ops must allow "verify" (AR-57)
//  6. signature over "header.payload" verified with the mapped key (type-asserted)
//  7. exp present and in the future (60 s leeway); nbf in the past; iat sane
//  8. iss == configured issuer (both sides normalized identically — AR-34)
//  9. aud/azp per OIDC Core §3.1.3.7 (AR-30): single aud => aud[0]==client_id;
//     multiple aud => azp MUST be present and == client_id; azp, when present,
//     MUST always equal the client id
func (v *Verifier) Verify(ctx context.Context, token string) (*Claims, error)
```

**Signature verification (exact stdlib calls) — AR-56 makes the ES cell normative:**

| alg | key (type-asserted) | call |
|-----|-----|------|
| `RS256/384/512` | `*rsa.PublicKey` | `rsa.VerifyPKCS1v15(pub, crypto.SHA256\|SHA384\|SHA512, hash[:], sig)` — hash = `sha256.Sum256(...)` etc. |
| `ES256/384/512` | `*ecdsa.PublicKey` | **NORMATIVE (AR-56):** the signature length MUST be exactly `2 × keyBytes` — **64** (P-256), **96** (P-384), **132** (P-521); `n := len(sig)/2`; `r = sig[:n]`, `s = sig[n:]`; verify with `ecdsa.Verify(pub, hash, r, s)`. **A DER-encoded signature MUST be REJECTED** — DER lengths (≈70–72 bytes for P-256) do not equal 64 ⇒ rejected by the length rule before any curve math. The previous cell in this plan was self-contradictory (`VerifyASN1` + "use `ecdsa.Verify`"); the corrected cell is the one above, and the §9.1 test `TestJWT_RejectsES256WithDERSignature` asserts the rejection. |
| `EdDSA` | `ed25519.PublicKey` | `ed25519.Verify(pub, msg, sig)` — the message is the raw `header.payload` bytes (Ed25519 hashes internally; **no** pre-hash). |

> **Implementation note (load-bearing):** `ecdsa.VerifyASN1` expects DER; JWS ES256 uses the fixed-width `R‖S` form ⇒ use `ecdsa.Verify` with the split and an **exact-length** check. This is the classic copy-paste bug that either fails every ES256 token or (worse) accepts a non-conformant signature.

**Audience parsing:** `aud` may be a string or an array ⇒ a custom `UnmarshalJSON` on a `stringOrSlice` type; **both** forms must be handled (Casdoor emits an array, many IdPs emit a string).

**JOSE hygiene (AR-55):** a `crit` header ⇒ **reject** (RFC 7515 §4.1.11 — the verifier does not understand any critical extension); `b64:false` ⇒ **reject** (RFC 7515 §5.2 — the unsecured-payload form is not supported); `typ:"at+jwt"` is accepted **only** by the future deferred-bearer mode (D16) and **never** by the id_token path.

**Token-type discipline (AR-31):** the id-token path **accepts** `typ: "JWT"` (case-insensitive) or an **ABSENT** `typ` header, and **REJECTS** `typ: "at+jwt"` — that value is the **access-token** type (RFC 9068); accepting it on the id-token path would let a Casdoor access token be mistaken for an id_token. Any other present value is rejected (fail-closed). **The deferred bearer mode's `at+jwt` acceptance is a D16/future concern and is NOT active on the id-token path** — the split is explicit. The `typ` claim and the content-type-style separation of RFC 8725 §3.12 are the reason Casdoor's separately-minted access tokens can never be mistaken for an id_token. **Implementation-landing note:** cross-check the live Casdoor token's `typ` value (or absence) against this rule using the commissioning-time artifact before merging — if live tokens carry a different `typ`, the rule must be widened deliberately, not silently.

### 5.6 `pkg/oauth2/handler.go` — the auth-code flow

```go
type Handler struct {
    cfg      *Config
    provider *OIDCProvider
    verifier *Verifier
    cookie   *CookieCodec
    txn      *CookieCodec   // the "txn" HKDF subkey codec (AR-01/D6a)
    states   *stateStore
}

// stateStore holds the one-time /oauth2/start state records.
// In-memory, per-pod. TTL 10 minutes, single use, pruned by a sweeper,
// SIZE-CAPPED (AR-11).
type stateRecord struct {
    State        string
    Nonce        string
    CodeVerifier string
    RedirectTo   string
    TxnDigest    string   // SHA-256 of the txn cookie value (AR-01/D6a)
    CreatedAt    time.Time
}

// stateStore is CAPPED: max-N records; an overflow => evict the oldest
// and, if the store is still full, answer 429 on /oauth2/start (AR-11).
type stateStore struct {
    mu      sync.Mutex
    records map[string]*stateRecord
    max     int
    count   int   // LIVE record count — exposed via Provider.StateStoreCount()
                  // and reported as "state_store_count" by /fe/api/auth/me (Note 4)
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) // /oauth2/start, /oauth2/callback, /oauth2/logout
```

**`GET /oauth2/start`**
0. **Valid-session short-circuit (AR-54, council):** if the request already carries a valid `llmproxy_oauth2` cookie ⇒ **302 straight to the sanitized `rd`** — no state, no IdP hop. This is what keeps the ADR-001 overlap window free of Casdoor round-trips for a user who already holds an app session.
1. `rd := sanitizeRedirectTarget(r.URL.Query().Get("rd"))` — D10 + AR-12. Invalid, or `rd` under `/oauth2/` or `/fe/api/auth/me` ⇒ `/ui/`.
2. Generate `state` (32 random bytes, base64url), `nonce` (32 random bytes), `code_verifier` (32 random bytes, base64url — RFC 7636 requires 43–128 chars), `code_challenge = base64url(SHA256(code_verifier))` (S256), and the **sealed transaction cookie value** `txnCookieValue = Seal(txnSubkey, {nonce,iat,exp})` = `base64url(16B nonce ‖ AES-256-GCM ciphertext ‖ tag)`.
3. Store `stateRecord` (TTL 10 min, one-time) with `TxnDigest = SHA-256(txnCookieValue)` — **the digest is over the FULL sealed cookie value string** (B3, leader pre-ruling). Plain SHA-256, not HKDF output.
4. Set the **transaction cookie** `llmproxy_oauth2_txn` = `txnCookieValue` (the sealed value), `HttpOnly; Secure; SameSite=Lax; Path=/oauth2; Max-Age=600` (D6a/AR-01).
5. 302 to `authorization_endpoint` with query: `response_type=code`, `client_id`, `redirect_uri`, `scope=openid profile email`, `state`, `nonce`, `code_challenge`, `code_challenge_method=S256`.
   - `redirect_uri` = `cfg.RedirectURL` — **always** the configured value; the per-request derivation branch is **deleted** (AR-03). `cfg.RedirectURL` is mandatory when enabled.

**`GET /oauth2/callback`**
1. `error` param present ⇒ 502 `{"error":"oauth2_callback_error"}` (provider-side denial).
2. `state` missing/unknown/expired ⇒ **400** `{"error":"invalid_state"}` (CSRF defence, D6). The state record is **deleted on use** (one-time) — a replayed callback fails.
3. **Transaction-cookie binding (D6a, AR-01, B3):** the `llmproxy_oauth2_txn` cookie MUST be present, MUST open under the **txn** subkey, and its **SHA-256 over the FULL sealed cookie value string** MUST match the record's `TxnDigest` under a **constant-time** comparison (`crypto/subtle.ConstantTimeCompare`). Absent or mismatched ⇒ **400** `{"error":"invalid_state"}` — this is the login-CSRF / session-fixation defence: a `state` minted in **another** browser is useless. The txn cookie is then **deleted** (single use). Test: `TestCallback_RejectsStateFromAnotherBrowser`.
4. `code` missing ⇒ 400.
5. Token exchange (§5.3) — **single attempt, zero retries** (AR-10). Failure ⇒ **502** `{"error":"token_exchange_failed"}` (not 504); the log carries only a stable error code.
6. Verify the id_token (§5.5) with `nonce` = the stored nonce; **nonce mismatch ⇒ 400** `{"error":"invalid_nonce"}`.
7. `iss`/`aud`/`azp`/`exp` verified inside `Verify`.
8. **Principal admission (AR-04, leader posture — strict from Phase 1):**
   - `email` MUST be present; absent ⇒ **403** `{"error":"email_missing"}`.
   - `email_verified` MUST be **true**. The claim is **absent** ⇒ **403** `{"error":"email_not_verified"}` — **unless** the allowlist is `*` **and** `OAUTH2_ALLOW_ANY_EMAIL_DOMAIN=true` (the explicit "any account" mode). Present-but-false ⇒ **403** in every mode.
   - The email domain MUST match the allowlist; not allowed ⇒ **403** `{"error":"email_domain_not_allowed"}` (fail closed).
   - The domain allowlist MUST be configured at cutover pre-flight (the `*` mode is a WARN, not the default posture).
9. Build the `Session` (5 claims, `preferred_username` dropped — AR-33/AR-62) ⇒ seal (seal-time budget enforced) ⇒ set the cookie ⇒ 302 to the stored `RedirectTo` (already sanitized) or `/ui/`.
10. The id_token, access_token, code, cookie value, and txn value are **never logged**; only `sub` and the email **domain** are logged at INFO on success (AR-39). **No OAuth2 event is published to the events bus** — `/fe/api/events` carries only proxy telemetry.

**`POST /oauth2/logout`** (POST-only — AR-36)
1. A `GET /oauth2/logout` ⇒ **405** (with `Allow: POST`). Logout is a state-changing action; a CSRF-able GET would let a third-party site log the operator out.
2. Clear the session cookie (MaxAge=-1) **first** — the local session is gone regardless of what the IdP does.
3. **Best-effort end-session (AR-23):** the hop to `end_session_endpoint` is attempted **only** when the discovery metadata is present **and fresh**; when it is stale or unavailable the hop is **skipped** (never send the browser to a dead IdP during an outage) and the local redirect is used.
4. `post_logout_redirect_uri` is **pinned** to the configured `OAUTH2_LOGOUT_REDIRECT` (a path under `OAUTH2_EXTERNAL_URL`; default `/ui/`) — never a request-supplied URL.
5. The **Casdoor SSO session survives** a local logout (the IdP session is separate); the operator is re-authenticated by the IdP on the next login. This is documented, not fixed.
6. Logout is **always safe to call** (no session ⇒ still clears + redirects).

**Path parity:** the paths are exactly `/oauth2/start`, `/oauth2/callback`, `/oauth2/logout` — the same three the ingress `oauth2-proxy` uses today (`k8s/templates/ingress-protected.yaml:15` → `https://$host/oauth2/start?rd=…`), so the nginx `auth-signin` annotation and the internal flow are **wire-compatible**. (The logout method differs by design — the ingress proxy's GET logout is not reachable through the app's POST-only route.)

**State-store placement limitation (AR-11/AR-65, explicit):** the state store is **in-memory and per-pod**. Consequently the deployment MUST run **one replica** in internal mode, and the chart MUST set `strategy: Recreate` / `maxSurge: 0` (AR-53) so a rollout never straddles two pods (`/oauth2/start` on the old pod, `/oauth2/callback` on the new ⇒ `400 invalid_state` — a login that dies exactly during a rollout). **Scale-out REQUIRES a shared state store** (Redis/DB) or the **stateless** alternative: carry `state`+`nonce`+PKCE verifier in a short-lived, AAD-bound, HKDF-"txn"-subkey-encrypted cookie (the codec already exists) — that is the documented scale-out path, not implemented in Phase 1.

**Per-IP rate limit (N1, approver):** a token bucket applied to `/oauth2/start` **and** `/oauth2/callback` **combined**, keyed by the **host part of `RemoteAddr`**, with sustained rate `OAUTH2_RATE_LIMIT_RPM` (default 10/min) and burst `OAUTH2_RATE_LIMIT_BURST` (default 20). In-memory per-pod (consistent with the single-replica invariant, AR-11). A request exceeding the bucket ⇒ **429** with `Retry-After`. The keys, defaults, and clamps are the single source of truth in §4.2. Test: `TestRateLimit_PerIP429` (§9.1).

### 5.7 `pkg/oauth2/middleware.go` — the gate

```go
// Middleware gates the operator surface. It is installed ONLY when
// cfg.Enabled (D1). In dormant mode no middleware object exists at all.
type Middleware struct {
    cookie *CookieCodec
    cfg    *Config
    exempt map[string]bool
}

// Wrap returns the gating handler for the given next handler.
func (m *Middleware) Wrap(next http.Handler) http.Handler
```

**Classification — FAIL-CLOSED by default (D2a, AR-02, ruling R-4).**
The path is **normalized with `path.Clean` semantics before classification** (Go 1.26 `net/http/server.go:2680`; an unclean path is 301-redirected by the mux to its cleaned form at `:2689` — but the gate must classify the cleaned path itself, never the raw one).

| # | Path class | Match rule | Behaviour when unauthenticated |
|---|-----------|-----------|--------------------------------|
| 1 | `/healthz` | exact | **pass** (never gated) |
| 2 | `/v1/…` | prefix `/v1/` | **pass** (D2 — the client API keeps its own Bearer auth) |
| 3 | `/oauth2/…` | prefix `/oauth2/` | **pass** (the flow itself) |
| 4 | `/fe/api/auth/me` | exact | **pass** (the identity probe — it must answer when unauthenticated) |
| 4b | `GET /fe/api/version` | exact **and** method GET | **pass** (AR-13, ruling R-1 — the house deploy gate G4 probes this exact path) |
| 5 | `/fe/api/…` | prefix `/fe/api/` | **401** `application/json` `{"error":"unauthenticated"}` (D11) |
| 6 | `/ui` (exact), `/ui/…`, `/` | exact `/ui`, prefix `/ui/`, exact `/` | **302** → `/oauth2/start?rd=<url-encoded absolute-path+query>` (D11) |
| 7 | **anything else** | — | **GATED** (401 JSON for API-shaped requests, 302 for browser-shaped ones) — the **default is closed** (D2a) |

> **Why class 7 changed (AR-02):** the previous wording ("anything else → pass") made every route added after this ship **public by default**. The pass set is now the **explicit allowlist** above, and a **route-census test** (`TestMiddleware_RouteCensus`) enumerates the **FULL** mux registration set — `pkg/ui/server.go:164` (`/ui/`), `:184` (`/`), `:193-246` (the `/fe/api/*` set: `:193` version, `:194` ram, `:195` config, `:196` models, `:197` models/, `:198` models/validate, `:199` models/test, `:205` decisions/test, `:211` image_generation/test, `:212` events, `:213` requests, `:223` requests/, `:224` app-tags, `:225` buffers/, `:227` tokens, `:228` tokens/, `:230` credentials, `:231` credentials/, `:238` credential-groups, `:239` credential-groups/, `:241` providers, `:243-246` usage*, `:213` `/fe/api/auth/me`); `pkg/mcp/mcp.go:32-45` (`RegisterProxyHandlers` → `:40` `/v1/mcp/{id}/sse`, `:41` `/v1/mcp/{id}/messages`, `:44` `/v1/mcp/{id}/`, `:45` `/v1/mcp/{id}`) + `pkg/mcp/mcp.go:49-62` (`RegisterAPIHandlers` → `:55` `/fe/api/mcp-servers/status`, `:59` `/fe/api/mcp-servers/test-connection`, `:62` `/fe/api/mcp-servers/`); `cmd/main.go:409-436` (`uiServer.RegisterHandlers` at `:409` + `:412` chat, `:413` messages, `:414` models, `:421` image_generation, `:430` decisions, `:433` `/healthz`); `pkg/oauth2/handler.go:283-285` (`:283` `/oauth2/start`, `:284` `/oauth2/callback`, `:285` `/oauth2/logout`) — and fails if a registered route is neither allow-listed nor gated. Adding a new route therefore requires a **decision**, not an omission.

**Authenticated short-circuit:** when the cookie is present and valid, the middleware attaches the principal to the request context (`oauth2.WithIdentity(ctx, id)`) and calls `next` — it does **not** re-verify the id_token on every request (the cookie is the session; the id_token was verified at login). The `/v1/*` path never even looks at the cookie.

**Session continuity is fully OFFLINE (AR-27):** cookie validation is AES-GCM + `exp` only — no IdP call, no group-claim refresh, no revocation check. The IdP is touched **only** at `/oauth2/start`, `/oauth2/callback`, and best-effort `/oauth2/logout`. An IdP outage therefore never invalidates a live session.

**Response contract for 401:** `Content-Type: application/json`, body exactly `{"error":"unauthenticated"}` (the `/fe/api/*` bare-error envelope convention), `Cache-Control: no-store`, and **no** `Location` header (an XHR must not be redirected).

**302 contract for browsers:** `Location: /oauth2/start?rd=%2Fui%2Fsettings` (relative, same-origin). `Cache-Control: no-store`.

### 5.8 The `pkg/identity` seam (neutral leaf package — B1 leader pre-ruling)

**There is NO consumer-side interface in `pkg/ui` and NO `*oauth2.Guard` satisfying an interface declared in `pkg/ui`** — that sketch was uncompilable under the plan's own import guard (a named type `oauth2.Guard` satisfying an interface declared in `pkg/ui` would require `pkg/ui` to import `pkg/oauth2`, which §5 forbids and §9.4 greps against). The fix is a **neutral leaf package** that both sides import:

```go
// pkg/identity/identity.go (NEW FILE — leaf: imports stdlib only)
package identity

type Identity struct {
    Sub   string `json:"sub"`
    Email string `json:"email,omitempty"`
    Name  string `json:"name,omitempty"`
    // preferred_username is DELIBERATELY ABSENT (AR-33/AR-62).
}

// Provider is the OAuth2 seam. A nil provider means "internal OAuth2 is
// dormant" — the /fe/api/auth/me endpoint then answers {"enabled":false}.
type Provider interface {
    Enabled() bool
    Current(r *http.Request) (*Identity, error)
    // StateStoreCount returns the LIVE count of the capped /oauth2/start
    // state store (Note 4) — a count only, never any state material.
    // A nil provider (dormant) reports 0.
    StateStoreCount() int
}

// pkg/oauth2 implements this structurally; pkg/ui consumes it.
```

- `pkg/identity` imports **nothing** from the repo — it is a leaf.
- `pkg/ui` imports `pkg/identity` (for `Identity`, `Provider`, and `SetIdentityProvider`); it **never** imports `pkg/oauth2`.
- `pkg/oauth2` imports `pkg/identity` (its `Guard`/`Handler` implements `identity.Provider`); it **never** imports `pkg/ui`.
- `cmd` is the only package that imports **both** `pkg/oauth2` and `pkg/ui`, and wires `*oauth2.Guard` → `identity.Provider` at the composition root (§6.3).

**`GET /fe/api/auth/me`** — registered **always** (`pkg/ui/server.go`, next to the other `/fe/api/*` routes at `:193-246`):

| Situation | Status | Body |
|-----------|--------|------|
| Dormant (no provider / provider disabled) | 200 | `{"enabled":false,"authenticated":false,"user":null,"state_store_count":0}` |
| Enabled, valid session | 200 | `{"enabled":true,"authenticated":true,"user":{"sub":"…","email":"…","name":"…"},"state_store_count":<n>}` |
| Enabled, no/invalid session | 401 | `{"error":"unauthenticated","enabled":true,"authenticated":false,"user":null,"state_store_count":<n>}` |

`state_store_count` (Note 4) is the **live count of the capped `/oauth2/start` state store** (§5.6, `stateStore.count`) — a diagnostic that makes the cap observable (an operator can see the store filling toward `OAUTH2_STATE_STORE_MAX`). It is `0` in dormant mode (no store exists). The value is exposed as a **count only** — no state, nonce, verifier or cookie material ever leaves the process through this field.

The `error` key is present **only** on the 401 (the `/fe/api/*` bare-error envelope convention); `enabled`/`authenticated` are additive keys that let the FE distinguish "dormant" from "enabled but signed out" in one round trip.

## 6. HTTP Endpoints, Middleware & Wiring

### 6.1 Middleware wrapping order (exact)

**Current (unchanged in dormant mode) — `cmd/main.go:514`:**
```
recoveryMiddleware( gzipmw.DecompressRequest( gzipmw.CompressResponse( mux ) ) )
```

**Enabled mode — the ONLY change:**
```
recoveryMiddleware( oauth2MW( gzipmw.DecompressRequest( gzipmw.CompressResponse( mux ) ) ) )
```

**Rationale for inserting the gate OUTSIDE `gzipmw.DecompressRequest`:**

1. **Panic containment** — `recoveryMiddleware` stays the outermost layer, so a panic inside the gate (or inside the decompressor) is still converted to a 500 (`cmd/main.go:571-582`).
2. **DoS surface** — an unauthenticated client must never reach the gzip decompressor. `gzipmw.DecompressRequest` runs for **every** request; putting the gate in front means an unauthenticated gzip bomb is rejected before any decompression work happens. **AR-58 (corrected wording):** the gzip risk is **already bounded** — `pkg/middleware/gzipmw/gzip.go:39` caps decompression at `MaxDecompressedBytes` (≈100 MiB) and answers `413 Payload Too Large` beyond it, so the gate-outside placement is **not** a zip-bomb mitigation; it is a **cheapness + no-compression-side-channel** argument (an unauthenticated request never allocates the decompression buffer at all). The placement therefore stands, but for the stated reason only.
3. **Response shape** — the gate's 401/302 bodies are written **outside** `gzipmw.CompressResponse`, which is self-scoped to `/fe/api/*` and to bodies ≥ 1 KiB anyway (`cmd/main.go:461-483`). A 401 JSON (`{"error":"unauthenticated"}`, ~30 bytes) is under the 1 KiB passthrough threshold ⇒ byte-identical whether or not the gate is inside or outside. Putting the gate outside additionally guarantees that **no** unauthenticated response body is ever compressed (no compression side-channel).
4. `gzipmw.CompressResponse` **excludes** `/fe/api/events` (SSE) — unchanged.

### 6.2 Route gating table (complete)

**Cross-reference:** every row below maps to exactly one class in §5.7's classification table (classes 1, 2, 3, 4, 4b, 5, 6, 7). The anti-drift assertion is `TestMiddleware_RouteCensus` (§9.1): it enumerates the **FULL** mux registration set — `pkg/ui/server.go:164` (`/ui/`), `:184` (`/`), `:193-246` (the `/fe/api/*` set: `:193` version, `:194` ram, `:195` config, `:196` models, `:197` models/, `:198` models/validate, `:199` models/test, `:205` decisions/test, `:211` image_generation/test, `:212` events, `:213` requests, `:223` requests/, `:224` app-tags, `:225` buffers/, `:227` tokens, `:228` tokens/, `:230` credentials, `:231` credentials/, `:238` credential-groups, `:239` credential-groups/, `:241` providers, `:243-246` usage*, `:213` `/fe/api/auth/me`); `pkg/mcp/mcp.go:32-45` (`RegisterProxyHandlers` → `:40` `/v1/mcp/{id}/sse`, `:41` `/v1/mcp/{id}/messages`, `:44` `/v1/mcp/{id}/`, `:45` `/v1/mcp/{id}`) + `pkg/mcp/mcp.go:49-62` (`RegisterAPIHandlers` → `:55` `/fe/api/mcp-servers/status`, `:59` `/fe/api/mcp-servers/test-connection`, `:62` `/fe/api/mcp-servers/`); `cmd/main.go:409-436` (`uiServer.RegisterHandlers` at `:409` + `:412` chat, `:413` messages, `:414` models, `:421` image_generation, `:430` decisions, `:433` `/healthz`); `pkg/oauth2/handler.go:283-285` (`:283` `/oauth2/start`, `:284` `/oauth2/callback`, `:285` `/oauth2/logout`) — and fails if a registered route appears in neither the pass allowlist nor the gated set. **No route may be silently outside the census** (BLK-4): the `/ui/` and `/` patterns (`:164`, `:184`), the MCP **proxy** handlers (`pkg/mcp/mcp.go:32-45`) and the MCP **API** handlers (`:49-62`) are all inside the set, not just the `/fe/api/*` block. **A route added after this ship is therefore GATED by default** (D2a).

| Route | Registered in dormant mode? | Registered in enabled mode? | §5.7 class | Gated? | Unauthenticated response |
|-------|------------------------------|---------------------------|-----------|--------|-------------------------|
| `GET /` | yes (`pkg/ui/server.go:184`) | yes | 6 | **yes** | 302 → `/oauth2/start?rd=%2F` |
| **`GET /ui`** (exact) | yes (`pkg/ui/server.go:164` — the `/ui/` subtree pattern also serves the exact `/ui` after the mux's trailing-slash redirect) | yes | 6 | **yes** | 302 → `/oauth2/start?rd=%2Fui` |
| `GET /ui/` , `GET /ui/*` | yes (`:164`) | yes | 6 | **yes** | 302 → `/oauth2/start?rd=%2Fui%2F…` |
| **`GET /fe/api/version`** | yes (`:193`) | yes | **4b (EXEMPT)** | **no** | 200 `{"version":"…"}` — the house deploy gate **G4** probes this exact path (`curl -s 127.0.0.1:4321/fe/api/version` must equal `{"version":"<EXPECTED_RUN>"}`); gating it would break the release gate for no security gain (the build number is already public via image tags) (AR-13, ruling R-1) |
| `GET /fe/api/ram` | yes (`:194`) | yes | 5 | yes | 401 JSON |
| `GET /fe/api/config` | yes (`:195`) | yes | 5 | yes | 401 JSON |
| `GET /fe/api/models`, `/fe/api/models/*`, `/fe/api/models/validate`, `/fe/api/models/test` | yes (`:196-199`) | yes | 5 | yes | 401 JSON |
| `GET /fe/api/decisions/test`, `/fe/api/image_generation/test` | yes (`:205`,`:211`) | yes | 5 | yes | 401 JSON |
| `GET /fe/api/events` (SSE) | yes (`:212`) | yes | 5 | yes | 401 JSON (the middleware answers **before** the SSE handler sets its headers) |
| `GET /fe/api/requests`, `/fe/api/requests/*` | yes (`:213`,`:223`) | yes | 5 | yes | 401 JSON |
| `GET /fe/api/app-tags` | yes (`:224`) | yes | 5 | yes | 401 JSON |
| `GET /fe/api/buffers/*` | yes (`:225`) | yes | 5 | yes | 401 JSON |
| `/fe/api/tokens`, `/fe/api/tokens/*` | yes (`:227-228`) | yes | 5 | yes | 401 JSON |
| `/fe/api/credentials`, `/fe/api/credentials/*` | yes (`:230-231`) | yes | 5 | yes | 401 JSON |
| `/fe/api/credential-groups`, `/fe/api/credential-groups/*` | yes (`:238-239`) | yes | 5 | yes | 401 JSON |
| `GET /fe/api/providers` | yes (`:241`) | yes | 5 | yes | 401 JSON |
| `/fe/api/usage*` | yes (`:243-246`) | yes | 5 | yes | 401 JSON |
| `/fe/api/mcp-servers*` | **conditional** — registered only when `MCP_ENABLED=true` (`cmd/main.go:393` → `pkg/mcp/mcp.go:55-62`); otherwise the path is **404**, not 401 | conditional | 5 (when registered) | yes | 401 JSON (admin API) — **intent (AR-61):** the MCP status/test handlers are commented "unprotected" but they are still **operator** surfaces (edge-gated today, internal-gated later); the internal gate must treat them as operator surfaces, and the **conditional** registration must be reflected in the census test (a route that does not exist cannot be probed for a 401) |
| **`GET /fe/api/auth/me`** | **yes (NEW)** | yes | **4 (exempt)** | **no (exempt)** | 200 `{"enabled":false,…}` / 401 `{"error":"unauthenticated",…}` |
| **`/oauth2/start`, `/oauth2/callback`** | **no (NEW)** | **yes (NEW)** | 3 | no | n/a (they ARE the flow) |
| **`/oauth2/logout`** (POST-only, AR-36) | **no (NEW)** | **yes (NEW)** | 3 | no | n/a — `GET /oauth2/logout` ⇒ **405** |
| `/v1/chat/completions`, `/v1/messages`, `/v1/models`, `/v1/image_generation`, `/v1/decisions` | yes (`cmd/main.go:412-425`) | yes | 2 | **no** | unchanged (proxy Bearer auth / public models) |
| `/v1/mcp/{id}/sse` | yes (`pkg/mcp/mcp.go:40`, registered by `RegisterProxyHandlers` at `:32`) | yes | 2 | **no** | unchanged (its own `authMW` bearer) |
| `/v1/mcp/{id}/messages` | yes (`:41`) | yes | 2 | **no** | unchanged (its own `authMW` bearer) |
| `/v1/mcp/{id}/` | yes (`:44`) | yes | 2 | **no** | unchanged (its own `authMW` bearer) |
| `/v1/mcp/{id}` | yes (`:45`) | yes | 2 | **no** | unchanged (its own `authMW` bearer) |
| `GET /healthz` | yes (`cmd/main.go:433`) | yes | 1 | **no** | 200 `ok` |
| **anything else** (no registered pattern) | n/a | n/a | **7** | **yes (fail-closed)** | 401 JSON / 302 — the mux's own 404 answer is preserved for an **unmatched** path only after the gate admits it; a new registered route is gated until an explicit allowlist entry is added |

### 6.3 `cmd/main.go` wiring sketch

```go
// ---- after crypto.InitEncryption() (cmd/main.go:213), before mux creation ----

// Internal OAuth2 (dormant by default). ConfigFromEnv returns (nil, nil)
// when OAUTH2_ENABLED is not "true" -> NOTHING below is installed and
// the request path is byte-identical to pre-feature behaviour (D1).
oauth2Cfg, err := oauth2.ConfigFromEnv()
if err != nil {
    // Fail-fast: a misconfigured ENABLED gate must never silently fall open.
    log.Fatalf("[oauth2] configuration error: %v", err)
}
var oauth2Guard *oauth2.Guard   // nil == dormant
if oauth2Cfg != nil {
    oauth2Guard = oauth2.NewGuard(oauth2Cfg)   // builds provider/jwks/verifier/cookie
    // AR-39: the boot line carries the mode strings only — never a secret.
    log.Printf("[oauth2] internal OAuth2 ENABLED mode=internal issuer=%s client_id=%s cookie=%s ttl=%s secret_mode=%s algs=%s email_domains=%s",
        oauth2Cfg.IssuerURL, oauth2Cfg.ClientID, oauth2Cfg.CookieName,
        oauth2Cfg.SessionTTL, oauth2Cfg.SecretMode,
        oauth2Cfg.AllowedAlgsString(), oauth2Cfg.EmailDomainsMode())
} else {
    log.Printf("[oauth2] internal OAuth2 DISABLED (set OAUTH2_ENABLED=true to enable)")
}
```

> **AR-03:** nothing in this block may derive a security decision from a request header. `oauth2Cfg.RedirectURL` and `oauth2Cfg.ExternalURL` are **mandatory** and come from the environment only — there is **no** `Host`/`X-Forwarded-Proto` derivation branch anywhere in the package (the branch is deleted from §5.6).

```go
// ---- mux construction (cmd/main.go:384) ----
mux := http.NewServeMux()
// ... existing registrations unchanged ...

// Identity probe: registered ALWAYS so the SPA can learn the mode.
// It answers {"enabled":false} when the provider is absent.
//
// B4 (leader pre-ruling): typed-nil safety. `oauth2Guard` is
// `*oauth2.Guard` (nil in dormant mode). Passing a typed nil directly
// into the `identity.Provider` interface would produce a non-nil
// interface holding a nil concrete value — `Enabled()` would then run on
// a nil receiver and could 500. Use the explicit-var pattern instead:
var identityProvider identity.Provider   // nil interface in dormant mode
if oauth2Guard != nil {
    identityProvider = oauth2Guard       // *oauth2.Guard satisfies identity.Provider
}
uiServer.SetIdentityProvider(identityProvider)
// The /oauth2/* routes exist only in enabled mode.
if oauth2Guard != nil {
    oauth2Guard.RegisterRoutes(mux)      // /oauth2/start|callback|logout
}
```

```go
// ---- handler wiring (cmd/main.go:514) ----
var handler http.Handler = recoveryMiddleware(gzipmw.DecompressRequest(gzipmw.CompressResponse(mux)))
if oauth2Guard != nil {
    // Gate sits INSIDE recovery and OUTSIDE the gzip decompressor (§6.1).
    handler = recoveryMiddleware(oauth2Guard.Wrap(gzipmw.DecompressRequest(gzipmw.CompressResponse(mux))))
}
srv := &http.Server{ ..., Handler: handler, ... }
```

> **House-style note:** `cmd/main.go` uses the plain `http.ServeMux` with `mux.HandleFunc` registrations and in-handler method guards (no Go 1.22 method-pattern syntax anywhere — see the comment at `cmd/main.go:417-420`). The new routes follow the same style: `mux.HandleFunc("/oauth2/start", guard.HandleStart)` etc., with method guards inside the handlers.

### 6.4 `pkg/ui/server.go` wiring sketch

1. **New file `pkg/identity/identity.go`** (the neutral leaf package — B1) — `Identity` + `Provider` interface + nothing else (leaf: stdlib only). **`pkg/ui` no longer owns an identity file**; it imports `pkg/identity` and its `Server` holds an `identity.Provider` wired via `SetIdentityProvider` (nil-safe).
2. **`pkg/ui/server.go:158` `RegisterHandlers`** — add ONE route next to the existing block:
   ```go
   // Internal OAuth2 identity probe. Registered ALWAYS so the SPA can
   // distinguish "dormant" from "enabled but signed out" in one call.
   // NOT gated by the OAuth2 middleware (it must answer when anonymous).
   mux.HandleFunc("/fe/api/auth/me", s.handleAuthMe)
   ```
3. **`pkg/ui/server.go`** — add `handleAuthMe` (method guard: GET only ⇒ 405 otherwise):
   ```go
   func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
       if r.Method != http.MethodGet { http.Error(w, "Method not allowed", http.StatusMethodNotAllowed); return }
       w.Header().Set("Content-Type", "application/json")
       w.Header().Set("Cache-Control", "no-store")
       p := s.identityProvider()          // nil-safe accessor under s.mu
       if p == nil || !p.Enabled() {
           // Dormant: no provider, no state store -> count 0 (Note 4).
           json.NewEncoder(w).Encode(map[string]any{"enabled": false, "authenticated": false, "user": nil, "state_store_count": 0})
           return
       }
       count := p.StateStoreCount()   // live capped-store count (Note 4)
       id, err := p.Current(r)
       if err != nil || id == nil {
           w.WriteHeader(http.StatusUnauthorized)
           json.NewEncoder(w).Encode(map[string]any{"error": "unauthenticated", "enabled": true, "authenticated": false, "user": nil, "state_store_count": count})
           return
       }
       json.NewEncoder(w).Encode(map[string]any{"enabled": true, "authenticated": true, "user": id, "state_store_count": count})
   }
   ```
4. **NOT touched:** `handleConfig` (`:589`) — `config.Config` gains **no** field (D3). `handleEvents` (`:642`) — the SSE handler is unchanged; the gate answers before it.

---

## 7. Frontend Changes (`pkg/ui/frontend/`)

### 7.1 Constraints

- Preact 10.19 + Vite 5.1 (`outDir: '../static'`, `base: '/ui/'`) + Vitest 3.2.7 (`environment: 'happy-dom'`, `include: ['src/**/__tests__/**/*.test.{ts,tsx}']`).
- `tsc-baseline.txt` is a **frozen count+file-set** baseline: `TOTAL_ERRORS=18`, 9 files. **Every new FE file must be tsc-clean (0 errors)** so the baseline file stays byte-identical. The verifier `src/utils/__tests__/tsc-baseline.test.ts` asserts counts + file set, not line numbers.
- `pkg/ui/static/` is **tracked and embedded** (`//go:embed static/*` at `pkg/ui/server.go:122`) ⇒ the Vite output **must be rebuilt and committed** in the same change (Single-Image FE/BE Coupling). Two consecutive builds must be byte-identical (content-hashed names differ per build; the **mechanism** is the claim — prove it by hashing the emitted asset set before/after).
- `make build-frontend` = `cd pkg/ui/frontend && npm install && npm run build`.

### 7.2 New FE files

| File | Purpose |
|------|---------|
| `src/hooks/useAuth.ts` | `useAuth()` — one-shot `/fe/api/auth/me` probe → `{ enabled, authenticated, user, stateStoreCount, loading, error }` (the FE reads the optional **`state_store_count`** field — the capped store's live count, Note 4 — and may surface it as a diagnostic; it is a count only, never state material); publishes the resolved mode to a **module-level store** (`authState`) so non-hook code (the fetch guard) can read it without prop-drilling. |
| `src/utils/apiFetch.ts` | `guardedFetch(input, init)` — wraps `fetch`; on `401` (and only when the internal mode is enabled) triggers the login redirect exactly once (D12). Also exports `authRedirectTarget()` (computes `rd`) and `resetAuthRedirectLatch()` (test hook). |
| `src/utils/authProbe.ts` | the ONE shared identity-probe contract (fold d860b0a probe-parse dedup): `AUTH_ME_PATH`, `AUTH_PROBE_TIMEOUT_MS` (3000), the runtime type guard for the untrusted body, and the outcome classification (`answer` / `unknown` / `not-in-charge`) — consumed by `useAuth.ts` + `useEvents.ts` so the two probes cannot drift (AR-16). |
| `src/utils/redirectStorm.ts` | the redirect-storm breaker (review item S10): a per-tab `sessionStorage` hop counter that aborts the redirect cycle once the hop threshold is reached and surfaces a visible, dismissible error banner; dormant mode never arms it; a successful authenticated probe clears it. |
| `src/utils/__tests__/apiFetch.test.ts` | Vitest suite for the 401 → redirect contract. |
| `src/hooks/__tests__/useAuth.test.ts` | Vitest suite for the identity probe. |
| `src/hooks/__tests__/useEvents.auth.test.tsx` | Vitest suite for the SSE 401 / reconnect-break contract. |
| `src/components/__tests__/Header.auth.test.tsx` | Vitest suite for the badge/logout rendering. |
| `src/__tests__/clientChannels.test.ts` | AR-60 census suite — the repo-wide `fetch(`/`EventSource`/`WebSocket`/`XMLHttpRequest`/`sendBeacon`/`axios`/`navigator.serviceWorker` scan returns only the guarded call sites + the auth probe + the single `EventSource` in `useEvents.ts`. |
| `src/utils/__tests__/redirectStorm.test.ts` | Vitest suite for the breaker contract (3 hops redirect, the 4th suppressed with a dismissible error state; success clears the counter; dormant never arms; the per-page-load latch counts one page-load as ONE hop). |
| `src/utils/__tests__/authProbe.test.ts` | Vitest suite for the shared probe-parse contract (ONE path + ONE timeout budget; boolean-`enabled` type guard; `answer`/`unknown`/`not-in-charge` outcomes, AR-16). |
| `src/utils/__tests__/authProbeMocks.ts` | the canonical mock helper for `GET /fe/api/auth/me` responses (**a helper, not a suite** — the five canonical shapes the FE auth tests import, eliminating per-file 200-where-401 mock drift; tester item R5). |

### 7.3 `guardedFetch` contract (the single 401 choke point)

```ts
// src/utils/apiFetch.ts
const LOGIN_PATH = '/oauth2/start';
const LATCH_COOLDOWN_MS = 8000;   // AR-42: the latch re-arms after a cooldown
let redirectInitiated = false;
let redirectAt = 0;

export function authRedirectTarget(): string {
  const rd = `${window.location.pathname}${window.location.search}`;
  return `${LOGIN_PATH}?rd=${encodeURIComponent(rd)}`;
}

export function resetAuthRedirectLatch(): void {           // test hook
  redirectInitiated = false;
  redirectAt = 0;
}

/** AR-42: the latch is bounded by a cooldown, not permanent. */
function latchArmed(): boolean {
  if (!redirectInitiated) return false;
  if (Date.now() - redirectAt > LATCH_COOLDOWN_MS) {      // re-arm
    redirectInitiated = false;
    return false;
  }
  return true;
}

export function isAuthProbe(input: RequestInfo | URL): boolean {
  const url = typeof input === 'string' ? input : input.toString();
  return url.includes('/fe/api/auth/me');
}

/** AR-43: the shared one-shot navigation primitive. */
let reloadIssued = false;
export function reloadOnce(): void {
  if (reloadIssued) return;
  reloadIssued = true;
  window.location.reload();
}

/**
 * AR-43: the dormant/edge world answers a browser with a 302 to the
 * IDP. fetch() follows that redirect cross-origin and REJECTS with a
 * TypeError ("Failed to fetch") — it is NOT a 401, so the 401 path
 * never fires. On the still-edged host (ensem stays edged until Phase 4)
 * that TypeError must trigger a one-shot location.reload() so the
 * browser can perform the edge's 302 -> IdP navigation natively.
 */
export function looksLikeCrossOriginRejection(err: unknown): boolean {
  return err instanceof TypeError;
}

export async function guardedFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  let res: Response;
  try {
    res = await fetch(input, init);
  } catch (err) {
    // AR-43/B2: the reload fires ONLY when the /fe/api/auth/me probe has
    // RESOLVED to enabled:true AND the failing request is a GET (the reload
    // path is GET-only — a POST/PUT/DELETE rejection must surface as a
    // normal error, never a page reload). Before/without that resolution a
    // fetch TypeError surfaces as a plain error (no reload) — dormant mode
    // is provably inert: the dormant probe answers enabled:false, so the
    // TypeError path stays non-reloading even under a persistent backend
    // outage (no reload loop). See §9.5 criterion 1.
    const isGet = !init?.method || init.method.toUpperCase() === 'GET';
    if (authState.isEnabledResolved() && isGet && looksLikeCrossOriginRejection(err)) reloadOnce();
    throw err;
  }
  if (res.status === 401 && !isAuthProbe(input)) {
    // Only redirect when the INTERNAL oauth2 mode is active; in dormant
    // mode a 401 can only come from an upstream ingress and a redirect
    // to /oauth2/start would 404.
    const enabled = await authState.isEnabled();     // resolves the pending probe
    if (enabled && !latchArmed()) {
      redirectInitiated = true;
      redirectAt = Date.now();
      window.location.assign(authRedirectTarget());
    }
  }
  return res;
}
```

**`authState` module store (in `useAuth.ts`):**
- `let enabledPromise: Promise<boolean> | null` — created on first use; resolves to the `enabled` flag from `/fe/api/auth/me`.
- `authState.isEnabled()` awaits it **with a 3 s timeout**. **AR-16:** on timeout it **nulls the cached promise** (so the next 401 re-probes) and resolves `false` — a timeout is **never cached as an answer** and never renders "signed out". Only an **explicit** answer (a resolved probe) is cached.
- **`authState.isEnabledResolved()`** (B2): returns `true` **only** when the probe has **resolved to `enabled:true`** (an explicit answer, never a timeout). This is the gate on `reloadOnce()` — so a fetch `TypeError` in dormant mode (probe resolved `enabled:false`) does **not** reload. Before the probe resolves, it returns `false`.
- `authState.setEnabled(bool)` is called by the hook once the probe resolves (so a later 401 knows the mode even if the first probe 404'd).
- **AR-42 reset semantics:** a full page reload creates a **new module state** ⇒ the latch is naturally re-armed; a same-tab retry within the same page is bounded by the 8 s cooldown, after which the latch re-arms and one more navigation may be attempted (bounded attempts, never an unbounded loop).

### 7.4 All fetch sites — mechanical replacement list

**Every** direct `fetch(` in `src/` (excluding `__tests__`) must be routed through `guardedFetch`, except the auth probe itself. Verified site list (from `grep -rn "fetch(" pkg/ui/frontend/src`):

| # | File:line | Call | Replacement |
|---|-----------|------|-------------|
| 1 | `hooks/useApi.ts:26` | `apiFetch` helper (`fetch(\`${API_BASE}${path}\`)`) | `guardedFetch` |
| 2 | `hooks/useApi.ts:96` | requests list | `guardedFetch` |
| 3 | `hooks/useApi.ts:242` | config | `guardedFetch` |
| 4 | `hooks/useApi.ts:286` | models | `guardedFetch` |
| 5 | `hooks/useApi.ts:434` | imggen models | `guardedFetch` |
| 6 | `hooks/useApi.ts:545` | decision models | `guardedFetch` |
| 7 | `hooks/useApi.ts:649` | group models | `guardedFetch` |
| 8 | `hooks/useApi.ts:741` | app-tags | `guardedFetch` |
| 9 | `hooks/useApi.ts:782` | version | `guardedFetch` |
| 10 | `hooks/useApi.ts:882` | tokens list | `guardedFetch` |
| 11 | `hooks/useApi.ts:952` | token permission PATCH | `guardedFetch` |
| 12 | `hooks/useApi.ts:1019` | `getCredentials` | `guardedFetch` |
| 13 | `hooks/useApi.ts:1035` | `createCredential` | `guardedFetch` |
| 14 | `hooks/useApi.ts:1049` | `updateCredential` | `guardedFetch` |
| 15 | `hooks/useApi.ts:1063` | `deleteCredential` | `guardedFetch` |
| 16 | `hooks/useApi.ts:1114` | `getCredentialGroups` | `guardedFetch` |
| 17 | `hooks/useApi.ts:1127` | `createCredentialGroup` | `guardedFetch` |
| 18 | `hooks/useApi.ts:1147` | `updateCredentialGroup` | `guardedFetch` |
| 19 | `hooks/useApi.ts:1164` | `deleteCredentialGroup` | `guardedFetch` |
| 20 | `hooks/useApi.ts:1187` | providers (hook) | `guardedFetch` |
| 21 | `hooks/useApi.ts:1211` | `getProviders` | `guardedFetch` |
| 22 | `components/config/ModelForm.tsx:462` | `/fe/api/models/test` | `guardedFetch` |

(22 call sites; `useApi.ts` holds 21 of them. `grep -rn "fetch("` must afterwards return **only** the `guardedFetch` internals + the auth probe.)

**AR-60 — the census is BROADER than `fetch(`.** The gate must cover **every** client-initiated request channel, not only `fetch`. The acceptance grep is therefore:

```
grep -rnE "(^|[^A-Za-z])fetch\(|EventSource|WebSocket|XMLHttpRequest|sendBeacon|axios|navigator\.serviceWorker" pkg/ui/frontend/src
```

Today this returns **zero** hits outside `useEvents.ts` (the single `EventSource`) — the census proves it and keeps it true. Every hit must be routed through the guard (or be the auth probe itself).

**Subresource caveat (AR-60):** `<img>`/`<script>` subresources loaded under `/ui/*` **302** on session expiry — the browser surfaces them as silent subresource failures, not as XHR errors. They cannot be guarded client-side; the mitigation is that the SPA's own data calls are guarded and the user sees the redirect on the next navigation. This is recorded so the behaviour is not mistaken for a bug.

**Note on error semantics:** the existing `apiFetch` throws `new Error(err.error)` for non-OK responses (`useApi.ts:34-37`). The 401 path must **not** throw a raw error into the UI before the redirect fires — `guardedFetch` triggers the redirect and still returns the response, so the existing error handling is unchanged (the page navigates away anyway).

### 7.5 SSE (`hooks/useEvents.ts`) — 401 detection + reconnect-loop break

Current behaviour (`useEvents.ts:34` → `new EventSource('/fe/api/events')`; `:51-78` `onerror` → if `readyState === CLOSED`, exponential backoff reconnect up to 30 s, forever).

**Problem:** `EventSource` exposes **no** HTTP status. A `401` from the gate looks exactly like a network drop ⇒ the loop would retry every 30 s indefinitely while the session is dead.

**Change (D13, AR-15):** in `es.onerror`, when `readyState === CLOSED`, **before** scheduling the backoff reconnect, run a **bounded** one-shot probe:

```ts
// useEvents.ts (es.onerror arm)
es.onerror = () => {
  isConnecting = false;
  if (es.readyState === EventSource.CLOSED) {
    // A 401 from the OAuth2 gate is indistinguishable from a network
    // drop here (EventSource exposes no status). Probe the auth state
    // ONCE, BOUNDED (AbortController + 3 s): a 401 means the session
    // expired -> stop the reconnect loop and send the browser to the
    // login flow. Anything else (network error, timeout, 5xx) keeps the
    // existing backoff behaviour untouched.
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 3000);   // AR-15 bound
    probeAuth(controller.signal)
      .then((auth) => {
        clearTimeout(timer);
        if (auth && auth.enabled && !auth.authenticated) {
          notifyStateListeners('disconnected');
          stopReconnectLoop();                 // clear pending timeout, do NOT reschedule
          redirectToLogin();                   // SHARED one-shot latch with guardedFetch (AR-15)
          return;
        }
        scheduleReconnect();                  // the EXISTING backoff path, unchanged
      })
      .catch(() => {
        clearTimeout(timer);
        scheduleReconnect();                  // AR-15: a FAILED/TIMED-OUT probe must still reconnect
      });
  }
};
```

- `probeAuth(signal)` = `fetch('/fe/api/auth/me', { signal })` (NOT `guardedFetch` — it must not recurse) → `{enabled, authenticated}`. It is **bounded at 3 s** (mirroring §7.3's `authState.isEnabled()` budget) and **must** `.catch()` into `scheduleReconnect()` — the previous wording ("a failed probe keeps the existing backoff") was **not satisfiable** by an un-caught promise: a rejected or hung probe never reached `scheduleReconnect()` and the EventLog stayed permanently silent.
- `redirectToLogin()` is the **same one-shot latch** as `guardedFetch` (§7.3) — a separate latch would allow a double navigation (SSE redirect + fetch redirect).
- `stopReconnectLoop()` clears `reconnectTimeout` and sets a module flag `authExpired = true` so `createEventSource()` is not re-entered by a late subscriber.
- The backoff constants (`RECONNECT_BASE_DELAY` 1 s / `RECONNECT_MAX_DELAY` 30 s) are **unchanged**.

### 7.6 Header badge + logout (`components/Header.tsx`)

Current right-hand block (`:44-78`): version chip, RAM chip, "System Active" indicator, settings gear.

**Change (rendered ONLY when the internal mode is active — never in dormant mode, never when an external ingress owns the login):**

```tsx
const { enabled, authenticated, user } = useAuth();
// ...
{enabled && authenticated && user && (
  <div class="flex items-center space-x-2">
    <span class="px-2 py-1 bg-gray-700 rounded text-xs text-gray-300" title={user.email || user.sub}>
      {user.name || user.email || user.sub}
    </span>
    <button onClick={handleLogout} class="text-gray-400 hover:text-white transition-colors" title="Sign out">
      Sign out
    </button>
  </div>
)}
```

> **Issue 9 (AR-33/AR-62): the badge renders `user.name || user.email || user.sub` — NO `preferred_username`.** Casdoor's default token format does **not** emit `preferred_username` (AR-62), and the `Identity` type in §5.8 deliberately omits it (AR-33). Including it would (a) always be `undefined` for the live IdP and (b) widen the `Identity` shape beyond the frozen tsc baseline — the `tsc-baseline.test.ts` parity (18 errors / 9 files, `tsc-baseline.txt` byte-identical) is **preserved** by keeping the type minimal. If a future IdP emits it, that is a deliberate type change that must re-baseline tsc.
with
```tsx
const handleLogout = () => {
  // AR-36: logout is POST-only (a GET would be CSRF-able). A form POST
  // (not fetch) so the browser follows the server's 302 to the pinned
  // post_logout_redirect_uri.
  const form = document.createElement('form');
  form.method = 'POST';
  form.action = '/oauth2/logout';
  document.body.appendChild(form);
  form.submit();
};
```

- The badge and the button are **absent** when `enabled === false` (dormant) — the header renders exactly as today.
- `components/__tests__/Header.test.tsx` (existing) must keep passing ⇒ the new `useAuth()` call must be stubbed (the suite must stub `globalThis.fetch` for `/fe/api/auth/me`, or mock the module). The existing suite is **not** modified in a way that weakens its assertions.
- `App.tsx` mounts `useAuth()` once at the top level so the mode is known before the first data fetch resolves (the `authState` store is populated by the same probe).

### 7.7 Vite build / `pkg/ui/static` verification

1. `cd pkg/ui/frontend && npm install && npm run build` → writes `pkg/ui/static/` (tracked).
2. **Zero-mod proof is NOT expected here** (the FE *does* change) — instead prove **determinism**: build twice, hash the emitted asset set (`sha256sum pkg/ui/static/assets/*`) before/after ⇒ identical. (Content-hashed filenames differ across *different* builds only if the content differs; two consecutive builds of the same source must match.)
3. `git status --porcelain pkg/ui/static/` must show the rebuilt assets as staged for commit in the same change as the TS source (Single-Image FE/BE Coupling: the Go binary embeds `pkg/ui/static/*`; a stale bundle would ship a UI without the badge).

---

## 8. Helm Chart (`k8s/`) — Dormant Toggles

### 8.1 Current state (verified)

| File | Content |
|------|---------|
| `k8s/values.yaml` | `env:` map at `:50-54` (`LOG_LEVEL`, `SSE_HEARTBEAT_ENABLED`, `MCP_ENABLED`); `ingress:` at `:20-33`; `livenessProbe`/`readinessProbe` at `:70-87` (both `/healthz` on 4321) |
| `k8s/templates/deployment.yaml` | `env:` block at `:36-58` (`GOMEMLIMIT`, `DATABASE_URL`, then `.Values.env`) |
| `k8s/templates/oauth2-proxy-deployment.yaml` | The ingress `oauth2-proxy` Deployment **+** its Service, `:1-47`; issuer `https://door.daoduc.org` hard-coded at `:21`; `--cookie-secret=$(COOKIE_SECRET)` at `:24`; `--email-domain=*` at `:26`; `--cookie-expire=24h` at `:27`; `envFrom.secretRef: oauth2-proxy-secret` at `:31-33` |
| `k8s/templates/ingress-protected.yaml` | nginx auth annotations at `:14-16` (`auth-url` → `oauth2-proxy…:4180/oauth2/auth`, `auth-signin` → `https://$host/oauth2/start?rd=$escaped_request_uri`, `auth-response-headers`); paths `/` (Exact), `/ui` (Prefix), `/fe/api` (Prefix) at `:32-50` |
| `k8s/templates/ingress-public.yaml` | `/oauth2` (Prefix) → service `oauth2-proxy:4180` at `:29-35`; `/v1` (Prefix) → the app at `:36-42` |
| `k8s/secrets-example.yaml` | `oauth2-proxy-secret` with `CLIENT_ID`/`CLIENT_SECRET`/`COOKIE_SECRET` at `:12-20` |
| `k8s/Chart.yaml` | `apiVersion: v2`, `version: 0.1.0`, `appVersion: "1.0.0"` |

### 8.2 `values.yaml` — new blocks (defaults preserve today's behaviour)

```yaml
# ── Ingress-layer OAuth2 (oauth2-proxy sidecar) ───────────────────────────
# DEFAULT: enabled — this is the CURRENT production topology and must
# keep rendering byte-equivalent manifests when internalOAuth2 is off.
#
# AR-21: `enabled` (in-path) is SEPARATE from `replicas` (object
# existence). The edge is IDLED by scaling it to 0 replicas — the
# Deployment and Service objects stay RENDERED, because Helm 3 PRUNES
# release-managed objects that disappear from the render. Pruning the
# edge would destroy the rollback anchor (OQ-7: "idle, don't delete").
oauth2Proxy:
  enabled: true          # in-path: is the edge in the request path? (gates ONLY the ingress wiring — auth annotations + /oauth2 backend)
  replicas: 1            # object existence / serving count (0 = idled, objects still rendered).
                         # FALSY-AT-0 WARNING (Helm): a bare `{{if .Values.oauth2Proxy.replicas}}` treats 0 as falsy
                         # and would silently drop the `replicas:` line — the template MUST test `ne (toString …) "<nil>"`
                         # (or default-to-1-when-unset) so `replicas: 0` renders. See §8.3 oauth2-proxy-deployment row.
  image:
    registry: quay.io
    repository: oauth2-proxy/oauth2-proxy
    tag: "v7.6.0"
  issuerURL: "https://door.daoduc.org"
  emailDomain: "*"
  cookieExpire: "24h"
  existingSecret: "oauth2-proxy-secret"

# ── In-binary OAuth2 (internal, dormant by default) ──────────────────────
# DEFAULT: disabled. AR-22/ADR-001: the chart renders THREE states —
#   edge-only    = internalOAuth2.enabled=false          (default; today's render)
#   overlap      = internalOAuth2.enabled=true  AND oauth2Proxy.enabled=true
#                  AND internalOAuth2.overlap=true        (edge annotations STAY,
#                                                           app env renders, /oauth2 -> app)
#   internal-only= internalOAuth2.enabled=true  AND oauth2Proxy.enabled=false
#                  AND oauth2Proxy.replicas=0             (annotations drop,
#                                                           edge objects stay at 0 replicas)
# The only render-time FAIL is the genuinely contradictory input:
# internalOAuth2.enabled=true AND oauth2Proxy.enabled=true WITHOUT overlap=true.
#
# OQ-1 (planner, 2026-11-19): clientID / clientSecret / cookieSecret are
# read from internalOAuth2.existingSecret — the SAME oauth2-proxy-secret,
# reused AS-IS. No dedicated Casdoor client, no console work. A dedicated
# client later is a config-only change (new values in the same secret).
internalOAuth2:
  enabled: false
  overlap: false         # AR-22: the designed transition state (see above)
  issuerURL: ""          # e.g. https://door.daoduc.org
  clientID: ""           # rendered via secretKeyRef when set (AR-17/AR-48)
  clientSecret: ""       # optional (public client + PKCE); secretKeyRef source
  cookieSecret: ""       # falls back to the oauth2-proxy secret's COOKIE_SECRET
  externalURL: ""        # e.g. https://llm.daoduc.org — MANDATORY when enabled (AR-03)
  emailDomains: ""       # AR-04: an explicit domain list is the intended posture
  allowAnyEmailDomain: false   # AR-04: `*` requires an explicit true
  requireEmailVerified: true   # AR-04: strict from Phase 1
  allowedAlgs: "RS256"   # AR-06: default RS256 (live Casdoor)
  sessionTTL: "24h"
  existingSecret: "oauth2-proxy-secret"   # reuse the SAME k8s secret — no regeneration

  # AR-11/AR-53: internal mode MUST run a single replica with Recreate /
  # maxSurge 0 — the in-memory state store is per-pod. NESTED HERE (not at
  # the top level) so the default render keeps H6 byte-parity with today's
  # chart: a top-level `strategy:` would alter the default Deployment spec.
  strategy: Recreate
  maxSurge: 0
```

### 8.3 Template changes

| File | Change |
|------|--------|
| `k8s/templates/_helpers.tpl` | Replace the binary mutual-exclusion guard with the **three-state** model (AR-22, ADR-001): `{{- define "llm-supervisor-proxy.oauth2ModeGuard" -}}` → `{{- if and .Values.internalOAuth2.enabled .Values.oauth2Proxy.enabled (not .Values.internalOAuth2.overlap) }}{{- fail "internalOAuth2.enabled=true with oauth2Proxy.enabled=true requires internalOAuth2.overlap=true (three-state model: edge-only / overlap / internal-only)" }}{{- end }}{{- end }}`, plus a `required`-style guard: when `internalOAuth2.enabled`, `issuerURL`, `externalURL` and the client-id source **MUST** be present, else `fail` with a precise message (AR-48). Invoked at the top of `oauth2-proxy-deployment.yaml`, `ingress-protected.yaml`, `ingress-public.yaml`, and `deployment.yaml`. |
| `k8s/templates/oauth2-proxy-deployment.yaml` | **ALWAYS rendered** (AR-21): object existence is UNCONDITIONAL so Helm can never prune the rollback anchor. `oauth2Proxy.enabled` gates ONLY the ingress wiring (auth annotations + `/oauth2` backend), **never** this file. The serving count is `replicas: {{ .Values.oauth2Proxy.replicas }}` (idle = `0`). **Falsy-at-0 warning (Helm):** a bare `{{if .Values.oauth2Proxy.replicas}}` treats `0` as falsy and would silently drop the `replicas:` line — the template MUST use `{{if ne (toString .Values.oauth2Proxy.replicas) "<nil>"}}` (or default-to-1-when-unset) so `replicas: 0` renders. Parameterize the image (`{{ .Values.oauth2Proxy.image.registry }}/{{ .Values.oauth2Proxy.image.repository }}:{{ .Values.oauth2Proxy.image.tag }}`), `--oidc-issuer-url={{ .Values.oauth2Proxy.issuerURL }}`, `--email-domain={{ .Values.oauth2Proxy.emailDomain }}`, `--cookie-expire={{ .Values.oauth2Proxy.cookieExpire }}`, and `secretRef.name={{ .Values.oauth2Proxy.existingSecret }}`. |
| `k8s/templates/ingress-protected.yaml` | The nginx auth annotations (`:14-16`) are rendered when the edge is **in-path** — i.e. `oauth2Proxy.enabled=true` **and** (NOT `internalOAuth2.enabled` **or** `internalOAuth2.overlap=true`). In the overlap state the annotations **stay** (the edge still gates) while the app enforces behind it; in internal-only they drop. The `app-root` + timeout annotations stay in every mode. The paths (`/`, `/ui`, `/fe/api`) are unchanged. **AR-64 (benign):** `app-root:/ui/` makes nginx issue **one** short-circuit redirect to `/ui/` before the upstream — in internal mode that redirect lands on the app's own 302 → `/oauth2/start?rd=…`; no loop, no exposure, but the operator sees one extra hop. |
| `k8s/templates/ingress-public.yaml` | The `/oauth2` path backend is **conditional** (ADR-001 **I-1 Loop**): the app service (`{{ $fullName }}:{{ $svcPort }}`) whenever `internalOAuth2.enabled` — **including the overlap state** (the app's flow must be reachable the moment it enforces); the `oauth2-proxy:4180` service only when the edge is in-path AND the app is not enforcing. `/v1` is unchanged in every mode. |
| `k8s/templates/deployment.yaml` | Append the OAuth2 env block **only** when `internalOAuth2.enabled`: `OAUTH2_ENABLED=true`, `OAUTH2_ISSUER_URL`, `OAUTH2_CLIENT_ID`, `OAUTH2_CLIENT_SECRET`, `OAUTH2_COOKIE_SECRET`, **Secrets posture (S2, decided): the three secret values (`CLIENT_ID`, `CLIENT_SECRET`, `COOKIE_SECRET`) are mounted via `secretKeyRef` ALWAYS when internal mode renders env — literal `values.yaml` env is allowed ONLY for non-secret tuning knobs; `OAUTH2_CLIENT_ID`/`SECRET`/`COOKIE_SECRET` are NEVER literals. The `secretKeyRef` sources are `internalOAuth2.existingSecret` (default `oauth2-proxy-secret`), keys `CLIENT_ID`/`CLIENT_SECRET`/`COOKIE_SECRET`., `OAUTH2_EXTERNAL_URL`, `OAUTH2_EMAIL_DOMAINS`, `OAUTH2_ALLOW_ANY_EMAIL_DOMAIN`, `OAUTH2_REQUIRE_EMAIL_VERIFIED`, `OAUTH2_ALLOWED_ALGS`, `OAUTH2_SESSION_TTL`. In dormant mode **no** `OAUTH2_*` env is emitted (byte-parity with today). **Phase 1 (dormant) decision (planner OQ-4, 2026-11-19): this `secretKeyRef` wiring ships IN THIS PHASE** — the template support is merged while the feature is off, so Phase 3 activation is a pure values toggle and the cutover window touches no template logic. The three secret keys resolve from `internalOAuth2.existingSecret` = `oauth2-proxy-secret` (the SAME secret; OQ-1 reuse — no regeneration). **AR-53:** `strategy: Recreate` / `maxSurge: 0` are applied in internal mode (the in-memory state store is per-pod). |
| `k8s/secrets-example.yaml` | Document that the existing `oauth2-proxy-secret` is reused by the internal mode **as-is** (planner OQ-1, 2026-11-19): `CLIENT_ID` / `CLIENT_SECRET` / `COOKIE_SECRET` are read by both consumers — **no new secret keys, no regeneration, no Casdoor console work**. Note in a comment that a dedicated internal client later is a config-only change (new values in the same secret), which does not gate Phase 1. |

**Secret reuse (no regeneration):** the internal mode reads `COOKIE_SECRET` from the **existing** `oauth2-proxy-secret` (§4.3 fallback). The chart therefore needs **no new Secret object** and **no secret rotation** — the same `stringData` key serves both layers (they encrypt different cookies with different derivations, so there is no cross-layer interference).

### 8.4 Render verification (`helm template`) — three states

`helm` is **not on PATH** — the binary lives at `/home/nea/bin/helm` (the explorer finding: "helm binary at `/home/nea/bin`, not on PATH"). Commands use the absolute path.

| # | Render | Assert |
|---|--------|--------|
| **H1** | **edge-only** — `helm template llm-supervisor-proxy ./k8s` (defaults) | `oauth2-proxy` Deployment **and** Service present with `replicas: 1`; the protected ingress **has** `auth-url`/`auth-signin`; the public ingress `/oauth2` → service `oauth2-proxy`; the app Deployment has **no** `OAUTH2_*` env. |
| **H2** | **overlap** — `/home/nea/bin/helm template llm-supervisor-proxy ./k8s --set internalOAuth2.enabled=true --set internalOAuth2.overlap=true --set oauth2Proxy.enabled=true --set internalOAuth2.issuerURL=https://door.daoduc.org --set internalOAuth2.externalURL=https://llm.daoduc.org` | The edge annotations **STAY**; the app Deployment **does** carry the `OAUTH2_*` env **and** the `secretKeyRef` wiring; the public ingress `/oauth2` → the **app** service (ADR-001 **I-1**); the edge Deployment is still rendered. |
| **H3** | **internal-only** — `/home/nea/bin/helm template llm-supervisor-proxy ./k8s --set internalOAuth2.enabled=true --set oauth2Proxy.enabled=false --set oauth2Proxy.replicas=0 --set internalOAuth2.issuerURL=https://door.daoduc.org --set internalOAuth2.externalURL=https://llm.daoduc.org` | The protected ingress has **no** `auth-url`/`auth-signin`; the public ingress `/oauth2` → the **app** service; the app carries the `OAUTH2_*` env; the edge Deployment **and** Service are **still rendered** with `replicas: 0` (AR-21 — the rollback anchor survives; Helm must not prune it). |
| **H4** | **contradictory input** — `--set internalOAuth2.enabled=true --set oauth2Proxy.enabled=true` (without `overlap=true`) | The render **FAILS** with the three-state guard message (AR-22). |
| **H5** | `helm lint ./k8s` | exit 0. |
| **H6** | **Default-render parity (EMPTY diff)** | The **edge-only** render must be **byte-identical** to the pre-change chart's render: render the base-commit chart into a temp dir and diff the normalized YAML — the diff must be **EMPTY**. Every parameterised literal must equal today's hardcoded value (`https://door.daoduc.org`, `*`, `24h`, `v7.6.0`, `oauth2-proxy-secret`) — **no** permitted structural difference (AR-48). |
| **H7** | **single-replica guard (internal mode)** — the render MUST FAIL when `internalOAuth2.enabled=true` **and** `replicaCount > 1` (AR-11/AR-65: the `/oauth2/start` state store is in-memory per-pod). **The command MUST carry a valid three-state**, else the earlier three-state guard (H4) fires first and masks this one. Correct invocation (internal-only variant): `/home/nea/bin/helm template llm-supervisor-proxy ./k8s --set internalOAuth2.enabled=true --set oauth2Proxy.enabled=false --set internalOAuth2.issuerURL=https://door.daoduc.org --set internalOAuth2.externalURL=https://llm.daoduc.org --set replicaCount=2` (equivalently `--set internalOAuth2.overlap=true` with the edge left enabled) | The render **FAILS** with `internalOAuth2.enabled=true requires replicaCount: 1 (AR-11/AR-65: ... set replicaCount: 1)`. **Negative controls that MUST still render:** the same command with `replicaCount=1` (internal-only **and** overlap), and `replicaCount=2` with `internalOAuth2.enabled` **UNSET** (the app renders `replicas: 2` — the guard must not over-block the dormant path). |

**H7 precedence note:** a bare `--set internalOAuth2.enabled=true --set replicaCount=2` (chart defaults) hits the **H4** three-state guard first and prints the overlap message — that is guard **precedence** working as designed, **not** a missing single-replica guard.

**H2/H3 command note:** the H2/H3 commands carry `issuerURL`/`externalURL` because the AR-48 required-guards fire for ANY `internalOAuth2.enabled=true` render — without them the render aborts at the guard instead of producing the state.

---

## 9. Complete Test Matrix & Acceptance Gates

### 9.1 Go unit tests — `pkg/oauth2/` (new package)

**Canonical file counts (Note 5 — the overview must mirror these numbers, not a prose guess):**

| Artifact | Canonical count | Enumeration |
|----------|-----------------|-------------|
| Production Go source files added | **8** | `pkg/oauth2` **7** — `config.go`, `cookie.go`, `oidc.go`, `jwks.go`, `jwt.go`, `handler.go`, `middleware.go` — plus `pkg/identity` **1** — `identity.go` |
| Go test files (this section, §9.1) | **11** | `config_test.go`, `cookie_test.go`, `jwt_test.go`, `jwks_test.go`, `oidc_test.go`, `handler_test.go`, `middleware_test.go`, `statestore_test.go`, `ratelimit_test.go`, `testhelpers_test.go` (**10 unconditional** — the last is the shared test seam/helper file) + `guard_test.go` (**1 unconditional** — the conditional fired: `Guard` IS a separate type from `Middleware`) |
| Go test files (integration, §9.2) | **2** | `pkg/ui/handlers_auth_test.go`, `cmd/oauth2_boot_test.go` |
| **Total Go test files** | **13** | 11 (§9.1) + 2 (§9.2) |
| FE test suites (§9.3) | **9** rows | **7 new** — `apiFetch.test.ts`, `useAuth.test.ts`, `useEvents.auth.test.tsx`, `Header.auth.test.tsx`, `clientChannels.test.ts`, `redirectStorm.test.ts`, `authProbe.test.ts` — plus **2 existing** re-asserted (`Header.test.tsx`, `tsc-baseline.test.ts`) |


| File | Test names (proposed) | Asserts |
|------|----------------------|---------|
| `config_test.go` | `TestConfigFromEnv_DormantByDefault`, `TestConfigFromEnv_EnabledRequiresIssuerClientIDAndSecret`, **`TestConfig_EnabledRequiresExternalURL`** (AR-03), `TestConfigFromEnv_RejectsHS256InAllowlist` (case-insensitive `HS256`/`hs256`/`none`/`NoNe` — AR-06), `TestConfigFromEnv_RejectsOAuth2ProxyCookieName`, `TestConfigFromEnv_EmptyEmailDomainsIsError`, **`TestConfig_StarDomainRequiresExplicitAllow`** (AR-04: `*` without `OAUTH2_ALLOW_ANY_EMAIL_DOMAIN=true` ⇒ error), **`TestConfig_CookieSecureDefaultsTrue`** (AR-03), **`TestConfig_TimeoutsAreClamped`** (AR-09/AR-24: discovery 5 s, token 10 s, userinfo 5 s), `TestConfigFromEnv_TTLClamps`, `TestDeriveCookieKey_Base64AndPassphrase` (6-rung ladder: std/raw std/url/raw url — AR-32/AR-63), `TestDeriveCookieKey_PassphraseNeeds16Bytes`, `TestDeriveCookieKey_EmptyIsError` | dormant ⇒ `(nil, nil)`; every fail-fast rule fires with a precise message; **a missing `OAUTH2_EXTERNAL_URL`/`OAUTH2_REDIRECT_URL` is fatal**; every secret derivation yields 32 bytes and is deterministic; the secret VALUE never appears in the error strings. |
| `cookie_test.go` | `TestCookie_SealOpenRoundTrip`, `TestCookie_TamperIsRejected`, `TestCookie_ExpiredIsRejected`, `TestCookie_AADBindingRejectsOtherCookieName`, **`TestCookie_TxnCiphertextFailsUnderSessionName`** (AR-01/AR-32 — the HKDF subkey separation), `TestCookie_Under4KB`, **`TestCookie_SealTimeBudgetEnforced`** (AR-33: over-budget ⇒ reject at seal, never truncate), **`TestCookie_ClaimCaps`** (AR-33: `sub`>64 / `email`>254 ⇒ reject; `name`>128 ⇒ truncate), **`TestCookie_NoPreferredUsernameClaim`**, `TestCookie_SetFlags` | round trip; a flipped ciphertext byte ⇒ error (no partial session); expiry; a ciphertext copied under a different cookie name ⇒ GCM failure; **a txn ciphertext under the session-cookie name ⇒ GCM failure**; the encoded cookie is < 4096 bytes; `HttpOnly`/`Path=/`/`SameSite`/`Secure` are set. |
| `jwt_test.go` | `TestJWT_RejectsHS256AlgConfusion`, `TestJWT_RejectsNoneAlg`, `TestJWT_RejectsUnknownAlg`, `TestJWT_RS256RoundTrip`, `TestJWT_ES256RoundTrip`, `TestJWT_EdDSARoundTrip`, `TestJWT_RejectsExpired`, `TestJWT_RejectsWrongIssuer`, `TestJWT_AudienceArrayAndString`, **`TestJWT_AudAzpRules`** (AR-30: single aud ⇒ `aud[0]==client_id`; multi-aud ⇒ `azp` present and equal; `azp` alone always equal), **`TestJWT_RejectsCritHeader`**, **`TestJWT_RejectsB64False`** (AR-55), **`TestJWT_RejectsAtJwtInIdTokenPath`** (AR-31: `typ:"at+jwt"` ⇒ reject on the id-token path; `typ:"JWT"`/absent ⇒ accept), **`TestJWT_RejectsJWKAlgMismatch`**, **`TestJWT_RejectsKeyOpsWithoutVerify`** (AR-57), `TestJWT_RejectsBadSignature`, `TestJWT_RejectsES256WithDERSignature` | **D5** proven by an HS256 token signed with the RSA public key as the HMAC secret ⇒ rejected; every allowlisted alg verifies a self-made token; exp/iss/aud/azp enforced; `aud` in both shapes; a `crit` header and `b64:false` ⇒ rejected; a JWK whose `alg` differs from the header alg ⇒ rejected; the ES256 raw-R‖S form is accepted (exact length 64/96/132) while a DER-encoded signature is rejected. |
| `jwks_test.go` | `TestJWKS_ParsesRSA`, `TestJWKS_ParsesEC`, `TestJWKS_ParsesOKP`, **`TestJWKS_PinsKtyAlgCrv`** (AR-06/AR-57: a mismatched combination is skipped, never coerced), `TestJWKS_KidMissTriggersRefresh`, **`TestJWKS_SingleFlightCollapsesConcurrentMisses`** (AR-07: N concurrent misses ⇒ ONE fetch), **`TestJWKS_SingleFlightRearmsAfterCompletion`** (AR-07: a *second* refresh cycle still fires — the `sync.Once` bug), **`TestJWKS_TTLAndKidMissShareOneKey`** (AR-07), `TestJWKS_RefreshIsRateLimited`, **`TestJWKS_MissWaitersBlockOnInFlightRefresh`** (AR-25), `TestJWKS_StaleCacheSurvivesFetchFailure`, **`TestJWKS_RotatedAwayKidInvalidatesOldTokens`** (AR-26), `TestJWKS_RejectsSmallRSAExponent` | all three key families; a `kid` miss ⇒ exactly ONE refresh; **the single-flight RE-ARMS** (two consecutive refresh cycles ⇒ two fetches); a second miss inside `JWKSMinRefresh` ⇒ no second fetch (throttled ⇒ 503 path); a failed refresh keeps the stale keys usable within the grace window; **the grace covers fetch failures only** — a successful refresh dropping a kid invalidates it; `e < 3` or even `e` is rejected. |
| `oidc_test.go` | `TestOIDC_DiscoveryParses`, `TestOIDC_RejectsIssuerMismatch`, **`TestOIDC_RejectsCrossOriginEndpoints`** (AR-05: a token/jwks/userinfo endpoint on another origin ⇒ hard error unless allow-listed), **`TestOIDC_CrossOriginRedirectRejected`** (AR-05: `CheckRedirect` refuses a cross-origin redirect and never re-sends the credential), **`TestOIDC_DiscoverySingleFlight`** (AR-08: N concurrent `Discover` ⇒ ONE fetch), **`TestOIDC_DiscoveryNegativeCache`** (AR-08: a failed discovery is not retried inside the backoff window), **`TestOIDC_ExchangeIsSingleAttempt`** (AR-10: zero retries; a failed exchange ⇒ 502, not 504), **`TestOIDC_ExchangeTimeout`** (AR-09), **`TestOIDC_UserinfoSubMustMatch`** (AR-35), `TestOIDC_RejectsNonHTTPSEndpoints`, `TestOIDC_DiscoveryFailureIsRetryable`, `TestOIDC_ExchangeCodePostsForm`, `TestOIDC_DiscoveryBodySizeCapped` | the discovery doc is parsed; an `issuer` mismatch ⇒ hard error (both issuers printed); **every advertised endpoint must be same-origin with the issuer**; **a cross-origin redirect is refused**; **concurrent starts issue ONE discovery fetch**; **the token exchange is attempted exactly once**; `http://` endpoints rejected unless allowed; a failed discovery is retryable and surfaces 502; the token request body/headers are exactly as specified; a huge discovery body is truncated at the cap. |
| `handler_test.go` | `TestStart_RedirectsWithStateNoncePKCE`, **`TestStart_SetsTransactionCookie`** (AR-01: `llmproxy_oauth2_txn`, HttpOnly, Secure, SameSite=Lax, Path=/oauth2, Max-Age=600), **`TestStart_ValidSessionShortCircuits`** (AR-54: a valid cookie ⇒ 302 `rd`, no IdP hop, no state minted), `TestStart_SanitizesRedirectTarget` (table: `//evil.com`, `/\evil.com`, `https://evil.com`, `javascript:…`, `""`, **`/oauth2/start`, `/oauth2/callback`, `/fe/api/auth/me`** ⇒ `/ui/` — AR-12), `TestCallback_RejectsMissingState`, `TestCallback_RejectsUnknownState`, `TestCallback_RejectsReplayOfState`, **`TestCallback_RejectsStateFromAnotherBrowser`** (AR-01/D6a: a `state` minted in browser B ⇒ 400), **`TestCallback_RejectsMissingTxnCookie`**, **`TestCallback_RejectsMismatchedTxnDigest`**, `TestCallback_RejectsNonceMismatch`, `TestCallback_RejectsPKCEMismatch`, `TestCallback_SetsCookieAndRedirects`, `TestCallback_EmailDomainNotAllowedIs403`, **`TestCallback_UnverifiedEmailIs403`** (AR-04), **`TestCallback_MissingEmailVerifiedIs403`** (AR-04 — unless the `*` mode is explicitly on), `TestCallback_ProviderErrorIs502`, **`TestLogout_GetIs405`** (AR-36), `TestLogout_ClearsCookie`, **`TestLogout_SkipsEndSessionWhenMetadataStale`** (AR-23), `TestLogout_WorksWithoutSession` | the authorization URL carries `state`, `nonce`, `code_challenge`, `code_challenge_method=S256`; the txn cookie is set with the exact flags; a valid session short-circuits to `rd`; every `rd` variant falls back to `/ui/`; the state is one-time (a replay ⇒ 400); **a state from another browser ⇒ 400**; the cookie is set and the redirect targets the sanitized path; a non-allowed email domain ⇒ 403; **an unverified or absent `email_verified` ⇒ 403**; **logout is POST-only**; logout clears the cookie. |
| `middleware_test.go` | `TestMiddleware_GatingTable` (table over every path class in §6.2 **and** every row of the §6.2 table — the anti-drift assertion), **`TestMiddleware_RouteCensus`** (AR-02/BLK-4: enumerate the **FULL** mux registration set — `pkg/ui/server.go:164` (`/ui/`), `:184` (`/`), `:193-246` (the `/fe/api/*` set: `:193` version, `:194` ram, `:195` config, `:196` models, `:197` models/, `:198` models/validate, `:199` models/test, `:205` decisions/test, `:211` image_generation/test, `:212` events, `:213` requests, `:223` requests/, `:224` app-tags, `:225` buffers/, `:227` tokens, `:228` tokens/, `:230` credentials, `:231` credentials/, `:238` credential-groups, `:239` credential-groups/, `:241` providers, `:243-246` usage*, `:213` `/fe/api/auth/me`); `pkg/mcp/mcp.go:32-45` (`RegisterProxyHandlers` → `:40` `/v1/mcp/{id}/sse`, `:41` `/v1/mcp/{id}/messages`, `:44` `/v1/mcp/{id}/`, `:45` `/v1/mcp/{id}`) + `pkg/mcp/mcp.go:49-62` (`RegisterAPIHandlers` → `:55` `/fe/api/mcp-servers/status`, `:59` `/fe/api/mcp-servers/test-connection`, `:62` `/fe/api/mcp-servers/`); `cmd/main.go:409-436` (`uiServer.RegisterHandlers` at `:409` + `:412` chat, `:413` messages, `:414` models, `:421` image_generation, `:430` decisions, `:433` `/healthz`); `pkg/oauth2/handler.go:283-285` (`:283` `/oauth2/start`, `:284` `/oauth2/callback`, `:285` `/oauth2/logout`); fail if a registered route is neither allow-listed nor gated — **no route may be silently outside the census**), **`TestMiddleware_UncleanPathCannotBypassGate`** (AR-59: `/x/../fe/api/config` and `//fe/api/config` ⇒ gated), `TestMiddleware_API401IsJSON`, `TestMiddleware_BrowserIs302`, `TestMiddleware_V1IsUntouched`, `TestMiddleware_HealthzIsUntouched`, `TestMiddleware_AuthMeIsExempt`, **`TestMiddleware_VersionIsExempt`** (AR-13/R-1), **`TestMiddleware_UnknownPathIsGated`** (AR-02: class 7 defaults to gated), `TestMiddleware_AuthenticatedPassesWithIdentity`, `TestMiddleware_InvalidCookieIsAnonymous` | the complete table in §6.2 is asserted path-by-path; **an unknown path is gated (fail-closed)**; **every registered mux route is accounted for**; **an unclean path cannot bypass the gate**; the 401 body is exactly `{"error":"unauthenticated"}` with `Content-Type: application/json`; the 302 `Location` is `/oauth2/start?rd=…`; `/v1/*` and `/healthz` pass through; `/fe/api/auth/me` and `GET /fe/api/version` are exempt; a valid cookie yields an identity in the context; a tampered cookie is anonymous. |
| `statestore_test.go` | **`TestStateStore_CapAndEviction`** (AR-11: max-N; the oldest is evicted), **`TestStateStore_OverflowIs429`** (AR-11), `TestStateStore_SingleUse` | the store never exceeds its cap; an overflow answers **429** on `/oauth2/start`; a state record is consumed exactly once. |
| `ratelimit_test.go` (new) | **`TestRateLimit_PerIP429`** (N1), `TestRateLimit_PerIPAllowsBurst` | per-IP token bucket on `/oauth2/start`+`/oauth2/callback` combined: sustained 10/min, burst 20 ⇒ 429 + `Retry-After` on exceed; the bucket is in-memory per-pod; `OAUTH2_RATE_LIMIT_RPM`/`_BURST` env knobs with clamps. |
| `guard_test.go` (`Guard` IS a separate type — the conditional fired) | `TestGuard_DormantIsNoOp`, `TestGuard_EnabledWrapsMux` | in dormant mode the guard is never constructed; in enabled mode the mux chain is `recovery → gate → gzip → mux`. |

### 9.2 Go integration tests — `pkg/ui/`, `cmd/`

| File | Test | Asserts |
|------|------|---------|
| `pkg/ui/handlers_auth_test.go` (new) | `TestHandleAuthMe_Dormant`, `TestHandleAuthMe_EnabledAuthenticated`, `TestHandleAuthMe_EnabledAnonymous`, `TestHandleAuthMe_MethodNotAllowed`, `TestRegisterHandlers_AuthMeRouteExists`, **`TestHandleAuthMe_StateStoreCount`** (Note 4) | the three response shapes of §5.8 + the 405; `/fe/api/auth/me` is registered on the mux; **`state_store_count` is present in every shape** — `0` when dormant, the provider's live `StateStoreCount()` when enabled (count only, never state material). Uses a **fake** `identity.Provider` (a stub in the test file) ⇒ no `pkg/oauth2` import in `pkg/ui` (B1 — the seam is `pkg/identity`). |
| `cmd/oauth2_boot_test.go` (new) | `TestMain_OAuth2DisabledByDefault`, `TestMain_OAuth2EnabledRequiresConfig`, **`TestMain_DormantAuthMeIs200`** (B4) | with the env unset, the wiring helper reports "dormant" and the mux has **no** `/oauth2/start` handler (⇒ 404); with `OAUTH2_ENABLED=true` and a missing cookie secret, the wiring helper returns an error (⇒ `log.Fatalf` path). **`TestMain_DormantAuthMeIs200` boots the REAL binary with `OAUTH2_ENABLED` unset and asserts `GET /fe/api/auth/me` → 200 `{"enabled":false}` with no 500/panic** (typed-nil safety, B4). The helper tests drive `buildOAuth2FromEnv()`; the dormant-auth-me test drives the actual `cmd` wiring (the house `cmd` package has only `version_test.go`; a full boot needs a DB, so the test uses the in-process handler construction path). |

### 9.3 Frontend tests (Vitest)

| Suite | Asserts |
|-------|---------|
| `src/utils/__tests__/apiFetch.test.ts` | a 401 from any `/fe/api/*` triggers exactly ONE `window.location.assign('/oauth2/start?rd=…')`; the auth probe (`/fe/api/auth/me`) never redirects; no redirect when `enabled === false`; the redirect latch prevents a second redirect **within the cooldown** and **re-arms after it** (AR-42); `rd` is correctly encoded; **a cross-origin rejection (TypeError) triggers exactly ONE `location.reload()`** (AR-43); **a probe timeout does NOT cache `false`** — the next 401 re-probes (AR-16). |
| `src/hooks/__tests__/useAuth.test.ts` | the hook fetches `/fe/api/auth/me` once; maps `enabled`/`authenticated`/`user`; on a 401 it reports `authenticated: false` **without** redirecting (the probe is exempt); a probe timeout leaves the state **unknown** (not "signed out") (AR-16). |
| `src/hooks/__tests__/useEvents.auth.test.tsx` | a 401 on the auth probe during `es.onerror` **stops** the reconnect loop and redirects **through the shared one-shot latch** (AR-15); a network failure (probe throws / returns non-401) keeps the existing backoff reconnect; **`TestSSE_ProbeTimeoutKeepsBackoff`** — a probe that **times out** (abort at 3 s) still reaches `scheduleReconnect()` (AR-15). |
| `src/components/__tests__/Header.auth.test.tsx` | the badge + "Sign out" render when `enabled && authenticated`; they are **absent** when dormant (`enabled: false`) and absent when unauthenticated; the logout click issues a **POST** form submission to `/oauth2/logout` (AR-36 — a GET would be 405). |
| **`src/__tests__/clientChannels.test.ts`** (new) | **AR-60 census:** `grep -rnE "(^|[^A-Za-z])fetch\(|EventSource|WebSocket|XMLHttpRequest|sendBeacon|axios|navigator\.serviceWorker" pkg/ui/frontend/src` returns only the guarded call sites + the auth probe + the single `EventSource` in `useEvents.ts` — no unguarded channel. |
| **`src/utils/__tests__/redirectStorm.test.ts`** (new) | **the redirect-storm breaker:** 3 hops across simulated page loads redirect; the **4th is suppressed** with a visible, dismissible error state; a successful authenticated probe **clears the counter** (a later 401 redirects normally); dormant mode **never arms** the breaker; the per-page-load AR-42 latch counts one page-load's attempts as ONE hop. |
| **`src/utils/__tests__/authProbe.test.ts`** (new) | **the shared probe-parse contract:** ONE endpoint path (`AUTH_ME_PATH`) + ONE 3000 ms timeout budget (`AUTH_PROBE_TIMEOUT_MS`); the runtime type guard accepts only a body with a boolean `enabled` — anything else is UNKNOWN and **never cached as `enabled:false`** (AR-16); the 200 dormant and 200 signed-in bodies and the 401 enabled+anonymous shape are EXPLICIT answers; a 401 with an unparseable body is UNKNOWN; a status neither 200 nor 401 is `not-in-charge`; `preferred_username` never normalized into the user sub-shape. |
| `src/components/__tests__/Header.test.tsx` (existing) | still passes (the new hook call is stubbed). |
| `src/utils/__tests__/tsc-baseline.test.ts` (existing) | `tsc --noEmit` exits **2** with the **exact** baseline: `TOTAL_ERRORS=18` across the **same 9 files**; `tsc-baseline.txt` is **byte-identical** to the base (sha256 before/after). New FE files contribute **0** errors. |

### 9.4 Release-gate commands (house rules)

| Gate | Command / rule |
|------|-----------------|
| Toolchain | `GOTOOLCHAIN=local`, `GOPATH=/home/nea/go`, `GOMODCACHE=/home/nea/go/pkg/mod`, `GOCACHE=/home/nea/.cache/go-build`; Go 1.26 (`/home/nea/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.linux-amd64/bin/go`). |
| HOME isolation | `export HOME=$(mktemp -d)` for every Go test run (the `pkg/proxy` suite rewrites `config.json` via `APPLY_ENV_OVERRIDES=1`). Hash the **absolute real-home** `config.json` **before** the export and compare after (the `~` form resolves to the quarantine and can never fire). |
| Pack A | `timeout 300 go test -timeout=240s ./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...` — **includes the new `pkg/ui` auth tests and the new `cmd` boot test**. |
| Pack B | **the module-anchored complement of Pack A** (every package except `cmd` + `pkg/proxy` + `pkg/store` + `pkg/decision` + `pkg/ui` and their subpackages) — **the copy-pasteable command is the fenced block immediately below this table**: a command written *inside* a table cell needs its regex alternation escaped as `\|`, which ERE reads as a LITERAL pipe and silently widens the selection back to the whole suite (46 packages instead of 37). **the new `pkg/oauth2` **and `pkg/identity`** land here**; the Pack B package count **grows 35 → 37** (pkg/oauth2 + pkg/identity) — re-measure, never copy the frozen count. **The `(/\|$)` suffix is load-bearing** (escaped here only because this cell is a table row — the copy-pasteable form is in the block below). |
| `make test` | exit 0 except the known quarantine pair (`pkg/mcp` httpbin.org redirect 403s at `handlers_api_test.go:1864/:1904`) — adjudicate as the known third-party failure, identical signature. **`make test` now runs, in order: the six import guards, then `make census-gate` (anti-drift), then `go test ./...`** — the census gate MUST stay green inside the full run. |
| `go build -o /tmp/llmproxy ./cmd` | exit 0 (the **real** entry; the bare `go build ./cmd` form collides with the `cmd/` directory and exits 1 — use the `-o /tmp/llmproxy` form; the repo-root `test_load.go` stub is a 3.7 MB no-port binary — do not gate on it). `go build ./...` + `go vet ./...` exit 0. |
| `gofmt` | **ZERO new drift**: `git show <base>:<file> \| gofmt -d` per touched file; the NEW flagged-line set = HEAD flagged set − base flagged set must be **empty**. Run `gofmt -l` on **all** touched `_test.go` (a sweep can silently add whole-file drift). |
| Import guards | **six** guards pass: pkg/imggen↛pkg/proxy, pkg/decision↛pkg/proxy, pkg/decision↛pkg/imggen (three pre-existing), pkg/oauth2↛pkg/proxy/pkg/ui/pkg/store and pkg/ui↛pkg/oauth2 (this feature's seam B1), and **the pkg/identity leaf guard — pkg/identity must not import ANY repo package** (plan §5.8: it is a leaf). |
| Route census | `make census-gate` — registered mux patterns (`pkg/ui/server.go`, `pkg/mcp/mcp.go`, `cmd/main.go`, `pkg/oauth2/handler.go`) vs the census set in `pkg/oauth2/middleware_test.go`; **44 registered / 45 census entries** at b5da8cd (re-measure, never copy — the set grows with every new route); wired into `make test`. |
| FE | `npm run test` (Vitest) green; `npx tsc --noEmit` exits 2 with exact parity (18/9); `npm run build` ⇒ `pkg/ui/static/` rebuilt + committed; two consecutive builds byte-identical. |
| Boundary diff | `git --no-pager diff --stat $BASE..HEAD -- k8s/ .github/ pkg/store/database/migrations/ pkg/store/database/migrate.go pkg/config/ pkg/proxy/` — the **only** expected entries are under `k8s/` (the chart toggles). `pkg/config/`, `pkg/store/database/migrations/`, `migrate.go`, `pkg/proxy/`, `.github/` ⇒ **0 bytes** (the proxy data plane is untouched by this feature). |
| `go.mod` / `go.sum` | **zero diff** (D4). |
| Anti-vacuity | every ADDED test function must show a literal `--- PASS` line in the gate log; the added-name set must equal the expected set exactly (no missing, no unlisted). |

**Pack B — the copy-pasteable package-selection command** (unescaped pipes; a fenced code block needs no escaping, which is exactly why the command lives here and not in the table row above):

```bash
go list ./... | grep -vE 'github\.com/disillusioners/llm-supervisor-proxy/(cmd|pkg/(proxy|store|decision|ui))(/|$)'
```

Pack B then runs `timeout 300 go test -timeout=240s` over exactly those packages. **The `(/|$)` end-anchor suffix is load-bearing** — measured at this branch state: dropping it (end-anchor-only) widens the selection to 41 packages by leaking Pack A's subpackages (`pkg/proxy/normalizers`, `pkg/proxy/token`, `pkg/proxy/translator`, `pkg/store/database`); the module prefix is currently redundant (the repo-relative form also selects 37) but is retained per house norms. **Expected Pack B count at this branch state: 37 packages** (46 total − 9 Pack A packages) — re-measure, never copy the frozen count.

### 9.5 Acceptance criteria (Phase 1 is done when ALL hold)

1. `OAUTH2_ENABLED` unset ⇒ `go test` proves: no `/oauth2/*` route (404), `/fe/api/auth/me` → `200 {"enabled":false,...}`, `/v1/models` → 200, `/healthz` → 200, `/fe/api/models` → 200 (unauthenticated, as today), `go.mod` unchanged, `config.json` hash unchanged. **The dormant delta is exactly: one constant-JSON route (`/fe/api/auth/me`) + one probe request per page load** (the SPA calls it on every load) — no middleware layer, no crypto, no outbound call (AR-41). **The FE recovery path is also part of the delta (AR-43):** `guardedFetch` catches a fetch `TypeError` — in the dormant/edge world the edge answers 302 → cross-origin redirect → `fetch()` rejects, it is **not** a 401 — and calls `reloadOnce()` so the browser can perform the edge's 302 → IdP navigation natively. This is **bounded**: the reload fires **only after** the `/fe/api/auth/me` probe has resolved, **only** for `/fe/api` GETs, and **at most once per page load**. Vitest case: `src/utils/__tests__/apiFetch.test.ts` asserts `reloadOnce` is invoked at most once per page load, **only after the `/fe/api/auth/me` probe has resolved to `enabled:true`**, and never before the probe resolves (B2).
2. `OAUTH2_ENABLED=true` with a stub OIDC provider (httptest) ⇒ `/ui/` → 302 to `/oauth2/start?rd=%2Fui%2F`; `/fe/api/models` → 401 `{"error":"unauthenticated"}`; `/v1/models` → 200; `/healthz` → 200; **`GET /fe/api/version` → 200** (the G4 exemption, AR-13); **an unregistered path → gated** (401/302, not 404 — AR-02).
3. Full code flow against the httptest IdP: start → callback → cookie set → `/fe/api/auth/me` → 200 `authenticated:true` → `/fe/api/models` → 200.
4. Negative matrix: bad state, **state from another browser**, missing/mismatched txn cookie, replayed state, nonce mismatch, PKCE mismatch, wrong issuer, wrong audience, wrong `azp`, expired token, HS256 token, `crit` header, `b64:false`, unknown kid (throttled), cross-origin discovery endpoint, cross-origin redirect, tampered cookie, disallowed email domain, **unverified/absent `email_verified`**, over-budget cookie — **each** produces the specified status and sets **no** cookie.
5. `helm template` renders cleanly in all **three** states (H1-H3), the contradictory input **fails** (H4), the **edge-only** render is **byte-identical** to the pre-change chart (H6 — EMPTY diff, AR-48), and the internal-mode render with `replicaCount > 1` **fails** (H7 — the single-replica guard, AR-11/AR-65).
6. The FE builds, the Vitest suites pass, `tsc` parity is exact, `pkg/ui/static/` is rebuilt and committed (AR-66 — the determinism proof stays the single-image coupling gate).
7. `go.mod`/`go.sum` untouched; `pkg/config/` untouched; no new migration; **no OAuth2 event published to the events bus** (AR-39 — the SSE payload is byte-identical to today).

---

## 10. Implementation Sequence

| # | Task | Depends on | Acceptance |
|---|------|------------|------------|
| **T1** | `pkg/oauth2/config.go` — env parsing, validation, `DeriveCookieKey` | — | `config_test.go` green; dormant ⇒ `(nil, nil)`; every fail-fast rule covered |
| **T2** | `pkg/oauth2/cookie.go` — GCM seal/open, flags, size cap | T1 | `cookie_test.go` green (tamper/AAD/expiry/size) |
| **T3** | `pkg/oauth2/jwt.go` — claims, alg whitelist, RS/ES/EdDSA verification | T1 | `jwt_test.go` green, incl. the HS256 confusion and DER-vs-raw ES256 cases |
| **T4** | `pkg/oauth2/jwks.go` — JWK parsing, cache, rate-limited refresh, stale grace | T3 | `jwks_test.go` green |
| **T5** | `pkg/oauth2/oidc.go` — lazy discovery, issuer match, token exchange | T3 | `oidc_test.go` green |
| **T6** | `pkg/oauth2/handler.go` — state store, `/oauth2/start|callback|logout` | T2, T3, T4, T5 | `handler_test.go` green; `rd` sanitization table; one-time state |
| **T7** | `pkg/oauth2/middleware.go` + `Guard` adapter (implements `identity.Provider` from `pkg/identity`) | T2, T6 | `middleware_test.go` green — the full gating table |
| **T8** | `pkg/ui` imports `pkg/identity` + adds `handleAuthMe` (no identity file in `pkg/ui`) | T7 | `pkg/ui/handlers_auth_test.go` green; **no** `pkg/oauth2` import in `pkg/ui` (B1 — the seam is `pkg/identity`) |
| **T9** | `cmd/main.go` wiring (env → config → guard → routes → middleware order) **+ extraction of `buildOAuth2FromEnv()` (env → guard, nil when dormant) so `TestMain_DormantAuthMeIs200` boots the real `main` path without duplicating wiring** | T1, T7, T8 | `cmd/oauth2_boot_test.go` green; dormant mode ⇒ **no** change to the handler chain |
| **T10** | FE: `src/utils/apiFetch.ts` + `src/hooks/useAuth.ts` | T8 | Vitest suites green; the 401 contract proven |
| **T11** | FE: replace all 22 fetch sites with `guardedFetch` | T10 | `grep -rn "fetch(" src/` shows only the guard internals + the probe |
| **T12** | FE: `useEvents.ts` auth probe + reconnect break | T10 | `useEvents.auth.test.ts` green |
| **T13** | FE: `Header.tsx` badge + logout; `App.tsx` mounts `useAuth()` | T10 | `Header.auth.test.tsx` + the existing `Header.test.tsx` green |
| **T14** | FE build: `npm run build` → `pkg/ui/static/` committed; determinism proof | T11-T13 | two consecutive builds byte-identical; `git status` shows the assets staged |
| **T15** | Helm: `values.yaml` + the 5 template changes + the **three-state** guard (`edge-only` / `overlap` / `internal-only`; AR-22) + the `oauth2Proxy.replicas` split from `enabled` (AR-21) + `strategy: Recreate` / `maxSurge: 0` in internal mode (AR-53) + the `secretKeyRef` env wiring for `CLIENT_ID` / `CLIENT_SECRET` / `COOKIE_SECRET` in `deployment.yaml` — this lands DORMANT in Phase 1 (planner OQ-4, 2026-11-19) so Phase 3 is a pure toggle and no env wiring is improvised during a cutover | — | H1-H7 green (three states + the contradictory-input failure + the **EMPTY** edge-only diff + the **single-replica fail**, AR-48/AR-65); H1/H3 prove the dormant render emits **zero** `OAUTH2_*` env and that the edge objects stay rendered at 0 replicas; H2 proves the overlap state keeps the annotations **and** renders the app env **and** points `/oauth2` at the app |
| **T16** | Gates: Pack A, Pack B (37 pkgs), `make test`, `go build ./cmd`, `go vet ./...`, `gofmt` zero-new-drift, tsc parity, boundary diff, `go.mod` zero-diff | T1-T15 | every §9.4 row green; anti-vacuity census closed |

**Parallelisable:** T15 (Helm) is independent of T1-T14 and can run in parallel. T11/T12/T13 are independent of each other once T10 lands. T16 is strictly last.

---

## 11. Coupling

|  | T1 config | T2 cookie | T3 jwt | T4 jwks | T5 oidc | T6 handler | T7 middleware | T8 pkg/ui | T9 cmd | T10-14 FE | T15 helm |
|---|---|---|---|---|---|---|---|---|---|---|---|
| **T1** | — | tight (key) | tight (algs) | loose | loose | tight | tight | independent | tight | independent | independent |
| **T2** | tight | — | independent | independent | independent | tight | tight | independent | loose | independent | independent |
| **T3** | tight | independent | — | tight | tight | tight | independent | independent | independent | independent | independent |
| **T4** | loose | — | tight | — | loose | tight | independent | independent | independent | independent | independent |
| **T5** | loose | — | tight | loose | — | tight | independent | independent | independent | independent | independent |
| **T6** | tight | tight | tight | tight | tight | — | tight | independent | loose | independent | independent |
| **T7** | tight | tight | independent | independent | independent | tight | — | tight (interface) | tight | loose | independent |
| **T8** | independent | independent | independent | independent | independent | independent | tight | — | tight | independent | independent |
| **T9** | tight | tight | independent | independent | independent | tight | tight | tight | — | independent | independent |
| **T10-14** | independent | independent | independent | independent | independent | independent | loose | independent | independent | — | independent |
| **T15** | independent | independent | independent | independent | independent | independent | independent | independent | loose | independent | — |

**Tight couplings worth calling out:**
- **T3↔T4** — the JWT verifier's key source is the JWKS cache; the `kid`-miss path is a **cross-package** contract (the verifier must translate a throttled miss into a 503, not a 401).
- **T6↔T7** — the middleware and the handler share the `CookieCodec` and the session semantics; a change to the cookie format changes both.
- **T7↔T8↔T9** — the `identity.Provider` interface (from the neutral leaf `pkg/identity`) is the only seam between the Go packages; `cmd/main.go` is the only place that imports both `pkg/oauth2` and `pkg/ui`.
- **T10-T14 ↔ T8** — the FE contract (`/fe/api/auth/me` shape) is pinned by the BE handler; a shape change must land BE+FE together.

---

## 12. Risks & Mitigations

| # | Risk | Impact | Likelihood | Mitigation |
|---|------|--------|------------|------------|
| R1 | **Alg-confusion forgery** — an attacker signs with HS256 using the public key as the HMAC secret | **Critical** | Medium | The `alg` → verifier map is **typed** and `HS*`/`none` are structurally absent (`jwt.go`); a dedicated test signs an HS256 token with the RSA public key and asserts rejection (D5). |
| R2 | **Secret leakage into `/fe/api/config`** — `config.Config` is marshalled verbatim (`pkg/ui/server.go:589-594`) | **Critical** | Low | OAuth2 settings never enter `config.Config` (D3); a test asserts the `/fe/api/config` body contains no `oauth2`/`client_secret`/`cookie_secret`; the `pkg/config/` boundary diff must be 0 bytes. |
| R3 | **Cookie secret format mismatch** with the existing `oauth2-proxy-secret` | High | Medium | The dual derivation rule (§4.3) accepts both base64-32 and passphrase forms; the `existingSecret` reuse means **no secret regeneration**; the mode is logged (never the value). |
| R4 | **Open redirect** via the `rd` parameter | High | Medium | Strict path-relative validation with a table test covering `//evil.com`, `/\evil.com`, absolute URLs, and `javascript:` (D10). |
| R5 | **CSRF / login-callback hijack** | High | Low | `state` (one-time, 10 min TTL, server-side) + `nonce` (bound into the id_token) + PKCE S256 — three independent defences, each with a negative test (D6). |
| R6 | **JWKS refresh storm** on a `kid` miss (DoS on the IdP) | Medium | Medium | The refresh is single-flight and rate-limited to once per 60 s (D7); the throttled path returns 503 + `Retry-After`. |
| R7 | **IdP outage logs everyone out** | High | Medium | Stale-while-error grace (2× the cache TTL), lazy discovery (never a boot dependency), 502/503 with a clear error, and the `/v1/*` client API is **never** affected (§4.4). |
| R8 | **SSE reconnect storm** on an expired session | Medium | High (if unimplemented) | The auth probe in `es.onerror` breaks the loop (D13) — the single highest-probability FE defect if skipped. |
| R9 | **FE redirect loop** (401 → redirect → 401 → …) | Medium | Medium | The redirect latch + the auth-probe exemption + `enabled`-gated redirecting (D12); covered by Vitest. |
| R10 | **A missing fetch site** keeps calling a raw `fetch` → an expired session shows raw errors instead of redirecting | Medium | Medium | The §7.4 census of 22 sites is exhaustive (`grep -rn "fetch("`); the acceptance gate requires the grep to return only the guard internals + the probe. |
| R11 | **v203 regression** — the new gate touching `/v1/*` or the token model | **Critical** | Low | D2/D14 + explicit tests (`/v1/models` 200 unauthenticated, `/v1/chat/completions` 401 from the proxy, `/healthz` 200) + the boundary diff + the v203 ADR is not edited. |
| R12 | **The overlap state** (edge gating + app enforcing simultaneously) | Medium | **Designed** | **REWRITTEN (AR-22, ADR-001):** overlap is no longer a defect — it is the **designed, probe-gated transition state** between edge-only and internal-only. Its risks are bounded: a fail-closed ping-pong for users **without** a live edge session (mitigated by the `/oauth2/start` valid-session short-circuit (AR-54), a short overlap, a low-traffic window, and the operator holding a live session — precondition P7′), and a **pod straddle** (mitigated by `strategy: Recreate`/`maxSurge: 0`, AR-53). The genuinely contradictory input (both enabled **without** `overlap=true`) still **fails** the render. |
| R13 | **`pkg/ui/static` not rebuilt** ⇒ the binary embeds a stale SPA (no badge, no 401 redirect) | Medium | Medium | The build is a numbered task (T14) with a determinism proof, and the static assets are committed in the same change (Single-Image FE/BE Coupling). |
| R14 | **ES256 signature-format bug** (DER vs raw R‖S) | Medium | Medium | `ecdsa.Verify` with an explicit R/S split, pinned by a test that asserts a DER-encoded signature is rejected. |
| R15 | **Casdoor discovery/claim differences** (e.g. `email` absent, `aud` shape, `preferred_username`) | Medium | Medium | Both `aud` shapes are handled; the email domain filter fails **closed** (403) if the email is missing; the plan flags the Casdoor specifics as an assumption to confirm at commissioning (§15 Q1). |
| R16 | **Boot-time config error crashes a production pod** | High | Low | The fail-fast applies **only** when `OAUTH2_ENABLED=true` (dormant ⇒ never read). Shipping dormant (Q3) keeps the blast radius at zero. |
| R17 | **The stale main checkout is used for line citations** | Medium | Medium | Every citation in this plan was read in the worktree; the implementer must re-read in the worktree (POK `main-checkout-stale-tree-trap`). |
| R18 | **The state store is unbounded and per-pod** (AR-11) | High | Medium | `/oauth2/start` is unauthenticated ⇒ unbounded growth for the TTL window. **Mitigation:** the store is **capped** (max-N + oldest-eviction), overflow ⇒ **429**, the count is exposed for diagnostics, and `/oauth2/start` is **rate-limited per IP**. **Normative invariant (adopted option (b)):** in internal mode the deployment MUST run **one replica** with `strategy: Recreate` / `maxSurge: 0` — the in-memory store is per-pod, so a rolling update would strand a login across two pods (`/oauth2/start` on the old pod, `/oauth2/callback` on the new ⇒ `400 invalid_state` **during a rollout**). **Scale-out alternative (stated, not implemented):** make the state **stateless** — carry `state`+`nonce`+PKCE verifier in a short-lived, AAD-bound, HKDF-"txn"-subkey-encrypted cookie (the codec already exists) — or move the store to a shared backend. |
| R19 | **Three-state chart template logic is the riskiest artifact** (AR-22, ADR-001 §Negative) | **Critical** | Low | A wrong conditional can silently re-open the **exposure** window (annotations dropped before the pods enforce). **Mitigation:** byte-level render assertions for **all three** states (§8.4 H1-H4) — including the H6 **EMPTY-diff** parity check for edge-only — plus the post-upgrade probe matrix (P-I/P-J) owned by the phase-2 plan. The chart is the highest-risk artifact in the change and is gated accordingly. |
| R20 | **The IdP is contacted on every login** and Casdoor supports the `password` grant ⇒ the reused `CLIENT_SECRET` is a live credential | High | Low | **AR-05:** every advertised endpoint must be **same-origin** with the issuer (hard error otherwise), the only escape hatch is an explicit `OAUTH2_ALLOWED_ENDPOINTS` allowlist, and `CheckRedirect` refuses cross-origin redirects without re-sending the credential — so the secret can never be POSTed to a third-party origin. |

---

## 13. Success Criteria

| # | Criterion | How to measure | Threshold |
|---|-----------|----------------|-----------|
| S1 | Dormant mode is byte-identical in behaviour | Booted server + route census | 0 new middleware layers; `/oauth2/*` → 404; `/fe/api/auth/me` → 200 `enabled:false`; every other route's status unchanged |
| S2 | Zero new dependencies | `git diff <base>..HEAD -- go.mod go.sum` | empty |
| S3 | No OAuth2 secret in the config surface | `GET /fe/api/config` body scan | 0 matches for `oauth2`/`client_secret`/`cookie_secret`; `config.json` hash unchanged |
| S4 | The gate table holds | `middleware_test.go` table test + `TestMiddleware_RouteCensus` | 100% of the §6.2 rows asserted; **every registered mux route is either allow-listed or gated** (fail-closed default) |
| S5 | v203 is untouched | `/v1/models` (anon) 200; `/v1/chat/completions` (no bearer) 401; `/healthz` 200; `pkg/authz/` + `pkg/auth/` diff | 0 bytes changed in `pkg/authz/`/`pkg/auth/` |
| S6 | The callback flow is CSRF-proof **and browser-bound** | the negative matrix in §9.5 item 4 | every negative case → the specified status, **no** cookie set — including a `state` minted in another browser |
| S7 | The session cookie is tamper-proof **and key-separated** | `cookie_test.go` | 100% of tamper/AAD/expiry/size/claim-cap cases rejected; a txn ciphertext under the session-cookie name fails closed |
| S8 | The FE handles an expired session everywhere | the **AR-60 census** (`fetch(\|EventSource\|WebSocket\|XMLHttpRequest\|sendBeacon\|axios\|serviceWorker`) + Vitest | 0 unguarded call sites; all 401 → exactly one redirect (shared latch, cooldown-bounded) |
| S9 | The SSE loop is broken on 401 **and survives a probe failure** | `useEvents.auth.test.ts` | 401 ⇒ no reconnect scheduled; **probe timeout/throw ⇒ the backoff still runs**; non-401 ⇒ the existing backoff is unchanged |
| S10 | The FE compiles within the frozen baseline | `npx tsc --noEmit` + `tsc-baseline.test.ts` | exit 2, `TOTAL_ERRORS=18`, the same 9 files; `tsc-baseline.txt` byte-identical |
| S11 | The SPA bundle is committed and deterministic | two consecutive `npm run build` runs | identical asset sha256 set; `pkg/ui/static/` staged in the same commit (AR-66) |
| S12 | The chart renders in **all three states** | `helm template` × 4 + `helm lint` | H1-H7 as specified; the **edge-only render diff is EMPTY** (H6); the contradictory input **fails** (H4); the edge objects stay rendered at 0 replicas (H3); the internal-mode render with `replicaCount > 1` **fails** (H7) |
| S13 | The release gates are green | Pack A, Pack B (37 pkgs), `make test`, `go build ./cmd`, `go vet ./...` | exit 0 (except the known `pkg/mcp` httpbin quarantine pair, adjudicated) |
| S14 | gofmt hygiene | per-file `gofmt -d` vs the base | NEW drift = ∅ |
| S15 | Boundary discipline | the boundary `diff --stat` | only `k8s/` entries; `pkg/config/`, `pkg/proxy/`, `migrations/`, `migrate.go`, `.github/` = 0 bytes |
| **S16** | **No request-derived security decision** (AR-03) | code review + `TestConfig_EnabledRequiresExternalURL` | 0 occurrences of `r.Host` / `X-Forwarded-Proto` in `pkg/oauth2`; a missing external URL is fatal |
| **S17** | **IdP endpoints are pinned to the issuer origin** (AR-05) | `TestOIDC_RejectsCrossOriginEndpoints`, `TestOIDC_CrossOriginRedirectRejected` | every advertised endpoint same-origin or explicitly allow-listed; cross-origin redirects refused |
| **S18** | **The JWKS refresh machinery is bounded and re-armable** (AR-07/AR-08) | `jwks_test.go` + `oidc_test.go` | concurrent misses ⇒ ONE fetch; a second refresh cycle still fires; discovery failures are negatively cached |
| **S19** | **The state store is bounded** (AR-11) | `statestore_test.go` | max-N enforced; overflow ⇒ 429; per-IP rate limit present; the single-replica + Recreate invariant is stated in the chart |
| **S20** | **The principal posture is explicit** (AR-04) | `handler_test.go` | `email_verified` absent/false ⇒ 403 (except the explicit `*` mode); `*` requires `OAUTH2_ALLOW_ANY_EMAIL_DOMAIN=true` + a startup WARN |

---

## 14. Exit Criterion (Phase 1 → Phase 2)

Phase 1 is **DONE** when:

0. **The amendment gate (architecture council, 2026-11-19):** every AR row in §17 that targets Phase 1 is implemented and covered by a named test in §9.1/§9.3 — the **22 blocking amendments** (AR-01…AR-22) have their dedicated negative tests green, and the 🟡/🟢 rows are either implemented or explicitly waived by the leader.
1. Every task T1-T16 in §10 is merged with its tests, and the §9.4 gate rows are all green with an anti-vacuous census (each added test name shows a literal `--- PASS`; added == expected, no missing, no unlisted).
2. The §9.5 acceptance criteria hold, each backed by a named test or command output.
3. `go.mod`/`go.sum`, `pkg/config/`, `pkg/store/database/migrations/`, `pkg/store/database/migrate.go`, and `.github/` are **byte-identical** to the base; the only non-`pkg/oauth2`/non-FE/non-`k8s` changes are `cmd/main.go`, `pkg/ui/server.go`, `pkg/identity/`, and the FE sources. (The new leaf package `pkg/identity/` is explicitly in scope and covered by the boundary diff.)
4. The Helm chart renders in **all three chart states** (edge-only / overlap / internal-only), the contradictory input **fails** the render, and the default (edge-only) render is byte-identical to the pre-change chart (H6 EMPTY diff) — D15/D17 enforced by the three-state guard, not the old binary mutual-exclusion guard.
5. **Nothing is deployed.** The feature ships **dormant** (`internalOAuth2.enabled: false`); no production manifest, secret, or ingress changes.

**Hand-off to Phase 2 (what Phase 2 may assume):** a `pkg/oauth2` package that provides a validated principal (`sub`,`email`,`name`) and a gated operator surface, wired **dormant**; the `identity.Provider` seam (neutral leaf `pkg/identity`); the FE auth store; the chart toggles; and a proven `helm template` **three-state** path.

---

## 15. Decisions Recorded & Residual Open Items (planner adjudications, 2026-11-19)

**Source of authority:** `plan-overview.md` §9 (Decisions Recorded). The items below are **decisions**, not recommendations — implementers apply them as written.

1. **OIDC client registration — DECIDED: reuse the existing `oauth2-proxy-secret` `CLIENT_ID`/`CLIENT_SECRET` as-is.**
   **Decision (planner OQ-1, 2026-11-19):** the internal flow authenticates with the **same** client id/secret the ingress `oauth2-proxy` already uses — **no dedicated Casdoor client, no Casdoor console work** (zero external human action, zero secret rotation). **Reuse is safe because the overlap is a *designed, probe-gated state*, not an accident of misconfiguration**: in the overlap state both consumers run by design (the edge still gates `/`, `/ui`, `/fe/api` while the app enforces behind it and the app's own flow is reachable — ADR-001 §3.2), and the client/secret is **the same application registration** at Casdoor, so the same `aud`/redirect URI is valid for both. There is no redirect-URI mismatch because Casdoor's allowlist is exact-match and the internal flow uses the same registered URI. **A dedicated client remains a config-only change** (new `OAUTH2_CLIENT_ID`/`OAUTH2_CLIENT_SECRET` values in the existing secret) if audit separation is later wanted — it does **not** gate Phase 1. `COOKIE_SECRET` reuse was already the plan (§4.3).
2. **Activation scope — RESOLVED / CONFIRMED (planner OQ-2, 2026-11-19).** The Helm toggle does **not** cover `ensem`, and that is correct: `ensem` is `kubectl apply`-managed, so a chart-only switch would create an unmanaged split-brain. **Phase 1 ships dormant on both hosts**; activation is per-host (Phase 3 = Helm/daoduc, Phase 4 = raw manifests/ensem). This phase changes nothing live.
3. **Session TTL — DECIDED: default `OAUTH2_SESSION_TTL=24h`** (clamp unchanged at `[5m, 24h]`). **Decision (planner OQ-3, 2026-11-19):** the cutover must not silently change re-login cadence versus today's `--cookie-expire=24h` (`k8s/templates/oauth2-proxy-deployment.yaml:27`); tightening to a shorter window later is an **explicit operator env change**, never a silent default shift. Applied in §4.2 (env table), §4.4 (startup log line), and §8.2 (`values.yaml` `sessionTTL`).
4. **Cookie interoperability — DECIDED: no cross-format compatibility attempt.** **Decision (planner OQ-5, 2026-11-19):** a forced re-login at cutover is **accepted**. The internal cookie (`llmproxy_oauth2`, §5.2) and the ingress `_oauth2_proxy` cookie are different formats on purpose — one code path, one cookie format; accepting a foreign cookie format would widen the attack surface for no operational gain. This matches the by-design non-interop already stated in §4.3.
5. **Chart secret wiring — DECIDED: lands in Phase 1 (dormant).** **Decision (planner OQ-4, 2026-11-19):** the Helm `secretKeyRef` env wiring for `CLIENT_ID` / `CLIENT_SECRET` / `COOKIE_SECRET` is **inside T15's scope in this phase** (§8.3, `k8s/templates/deployment.yaml` row), so Phase 3 is a **pure toggle** and no env wiring is improvised during a cutover window. In dormant mode the wiring renders **zero** `OAUTH2_*` env (byte-parity with today, H1/H3 prove it) — the *template support* ships now, the *values* are set only at activation.
6. **Casdoor facts — UPDATED after the architecture review (AR-62); what remains open is narrow.** **RESOLVED by the review's live probing:** (a) the discovery document **exists** at `https://door.daoduc.org/.well-known/openid-configuration` and its `jwks_uri` is **`/.well-known/jwks`** — **not** `jwks.json`; (b) the live JWKS carries `kid:"cert-built-in"`, `alg:"RS256"`, `use:"sig"` with `n`/`e` present (RSA-2048) ⇒ the kid-lookup rule and the "JWK `alg` ∈ allowlist" rule hold as written, and the default `RS256` allowlist (AR-06) matches the live key. **Corrected:** `email` **and** `email_verified` **are** present (so AR-04's strict `email_verified==true` rule is satisfiable), while **`preferred_username` is ABSENT** from the default token format ⇒ it is **dropped** from the session/identity claims (§5.2, §5.8). **STILL OPEN (commissioning-time verification, not a decision):** the Casdoor application's `TokenSigningMethod` must be **read at commissioning** (the live JWKS `alg` is the authority for the key we verify against; the app's configured signing method is what the IdP actually mints) — plus the remaining Casdoor-side checks (exact-match redirect-URI allowlist, PKCE S256 acceptance). These are listed in §16 and are **not** assumed resolved here.
7. **POST-MERGE (reviewer rider 5, not Phase 1 work): singleflight hardening in `pkg/oauth2`** — the restore/consume-context semantics flagged in the craftsmanship review.
8. **POST-MERGE (reviewer rider 6, not Phase 1 work): classify `context.Canceled` in `pkg/oauth2` error paths** — a client disconnect must not read as an IdP failure.
9. **POST-MERGE (reviewer rider 7, not Phase 1 work): document the strict boolean vocabulary (`true`/`false`, case-insensitive) in the phase-2 cutover runbook.**
10. **POST-MERGE (reviewer rider 8, not Phase 1 work): include `state_store_count` in the anonymous 401 `/fe/api/auth/me` body** — it is currently only in the 200 shapes.

---

## 16. Research Findings & Citations (all read inside the worktree at `92c4234`)

| # | Finding | Anchor |
|---|---------|--------|
| 1 | Middleware stack: `recoveryMiddleware(gzipmw.DecompressRequest(gzipmw.CompressResponse(mux)))` | `cmd/main.go:514`; the ordering rationale is documented at `cmd/main.go:460-483`; `recoveryMiddleware` at `cmd/main.go:571-582` |
| 2 | Route registration block (`/v1/*`, `/healthz`) + `uiServer.RegisterHandlers(mux)` | `cmd/main.go:409-436`; `mux := http.NewServeMux()` at `cmd/main.go:384` |
| 3 | Production env vars are read **unconditionally** via `os.Getenv` (no `APPLY_ENV_OVERRIDES` in k8s) | `cmd/main.go:393` (`MCP_ENABLED`); `k8s/values.yaml:50-54`; `pkg/config/config.go:557` (the override block exists but is gated) |
| 4 | `GET /fe/api/config` marshals `config.Config` **directly** ⇒ OAuth2 config must never be in it | `pkg/ui/server.go:589-594` |
| 5 | `ui.Server` shape, `//go:embed static/*`, the `/ui/` SPA fallback, the `/` → `/ui/` redirect, the `/fe/api/*` route block | `pkg/ui/server.go:122`, `:125-135`, `:137-152`, `:158-247`, `:164-181`, `:184-190`, `:193-246` |
| 6 | `crypto.InitEncryption` is a `sync.Once` singleton bound to `INTERNAL_ENCRYPTION_KEY`, passthrough when unset ⇒ the OAuth2 cookie key needs its **own** initialization | `pkg/crypto/encryption.go:17`, `:52-95`, `:99`, `:130`, `:179` |
| 7 | `go.mod` has **zero** OIDC/JWT/JOSE dependencies; the offline cache has `golang.org/x/oauth2@v0.35.0` (client-side only) and no JWT verifier ⇒ **stdlib-only** design | `go.mod:1-30` |
| 8 | The FE is Preact 10.19 + Vite 5.1 (`outDir: '../static'`, `base: '/ui/'`) + Vitest 3.2.7 (happy-dom) with a frozen `tsc` baseline of **18 errors / 9 files** | `pkg/ui/frontend/package.json`, `vite.config.ts`, `vitest.config.ts`, `tsc-baseline.txt` |
| 9 | `apiFetch` + **22** direct `fetch(` sites across `useApi.ts` (21) and `ModelForm.tsx` (1) | `pkg/ui/frontend/src/hooks/useApi.ts:26,96,242,286,434,545,649,741,782,882,952,1019,1035,1049,1063,1114,1127,1147,1164,1187,1211`; `components/config/ModelForm.tsx:462` |
| 10 | `new EventSource('/fe/api/events')` with an exponential-backoff reconnect loop that never terminates on a CLOSED source (⇒ a 401 would loop forever) | `pkg/ui/frontend/src/hooks/useEvents.ts:34`, `:51-78` |
| 11 | The header's right-hand block (version / RAM / status / settings) — the badge + logout slot | `pkg/ui/frontend/src/components/Header.tsx:44-78` |
| 12 | The Helm chart: the ingress `oauth2-proxy` Deployment + Service (`quay.io/oauth2-proxy/oauth2-proxy:v7.6.0`, issuer `https://door.daoduc.org`, `--email-domain=*`, `--cookie-expire=24h`, `secretRef: oauth2-proxy-secret`), the nginx auth annotations, the `/oauth2` → oauth2-proxy public path, and the app's `env:` block | `k8s/templates/oauth2-proxy-deployment.yaml:1-47`, `k8s/templates/ingress-protected.yaml:14-16`, `k8s/templates/ingress-public.yaml:29-42`, `k8s/templates/deployment.yaml:36-58`, `k8s/values.yaml:20-54`, `k8s/secrets-example.yaml:12-20` |
| 13 | v203 token authorization (the ADR to preserve): the tri-state grant scanner, the conjunctive token states, `authz.Resolve` | `.agents/shared/planning/token-authz-allowed-groups/adr-token-permission-model.md` §2 (D3, D10); `pkg/auth/token.go:40-85` |
| 14 | The house release gate: Pack A (9 pkgs) / Pack B (the module-anchored complement, `(/|$)` suffix load-bearing), `make test` import guards, `gofmt` zero-new-drift, `tsc-baseline.test.ts`, `HOME` isolation + the real-home `config.json` hash guard | `.agents/tester/PACKS.md` ("Release-Gate Slices (normative)"), `Makefile:44-72` |
| 15 | The live topology: OAuth is currently enforced **only at the ingress layer**; `/v1/*` uses the proxy's own token store; pod-direct access bypasses OAuth by design | Explorer result `oauth2-proxy-internal-oauth2-authentication-casdoor-daoduc-ensem-helm-deploy_20261119_095813.md` (shared context) |
| 16 | `/v1/mcp/{id}/*` carries its own bearer middleware ⇒ it is already outside the `/fe/api` gate | `pkg/mcp/mcp.go:40-45` vs `:55-62` |
| 17 | The gzip decompression cap the §6.1 rationale cites (AR-58): `MaxDecompressedBytes` ≈100 MiB with a `413` beyond it | `pkg/middleware/gzipmw/gzip.go:39` |
| 18 | `X-User` has **no producer** in the repo — the single consumer is the token-create handler, which sets `created_by` from the header (AR-38 / ruling R-3: Phase 1 must NOT populate it; `created_by` stays the literal `"api"`) | `pkg/ui/server.go:2723` (sole `X-User` read) |
| 19 | Go 1.26 `ServeMux` cleans the request path and 301-redirects an unclean path (AR-59) — the gate must classify the **cleaned** path | Go stdlib `net/http/server.go:2680` (`path = cleanPath(path)`), `:2689` ("Redirect to cleaned path") |

**Explicitly NOT relied upon (assumptions to confirm at commissioning) — updated per AR-62:**
- **RESOLVED (probed live by the architecture review):** the discovery document exists and its `jwks_uri` is `/.well-known/jwks` (**not** `jwks.json`); the live JWKS is `{"use":"sig","kty":"RSA","kid":"cert-built-in","alg":"RS256","n":…,"e":"AQAB"}` (RSA-2048) ⇒ the kid-lookup and JWK-alg rules hold, and the default allowlist `RS256` matches the live key.
- **RESOLVED / corrected:** the id_token carries `email` and `email_verified`; it does **not** carry `preferred_username` (dropped from the session and identity claims — §5.2, §5.8).
- **STILL OPEN:** the Casdoor application's `TokenSigningMethod` must be **read at commissioning** (the JWKS `alg` is the authority for the key we verify against); the Casdoor client's exact-match redirect-URI allowlist must accept the configured `redirect_uri`; PKCE `S256` acceptance must be observed on the live client.
- **STILL OPEN (operator, not engineering):** the daoduc IdP's operator accounts must have `email_verified == true` before the strict AR-04 posture is switched on for a host (the phase-2 worker owns the pre-cutover account-verification step).
- **STILL OPEN (operator):** whether the Emperor accepts the bounded, fail-closed overlap window on the primary (`ensem`) — ADR-001 §10.

---

## 17. Appendix — AR register cross-reference (architecture council, 2026-11-19)

Source of authority: `architecture-recommendation.md` (AR-01…AR-66) + `architecture-decision-record.md` (ADR-001). **Every 🔴 blocker and every 🟡/🟢 row that targets Phase 1 is folded in below.** Rows whose target is the phase-2 plan (AR-14, AR-17…AR-20, AR-44…AR-47, AR-50, AR-51, AR-52-partial) are owned by the parallel phase-2 worker and are listed for completeness only.

| AR | Sev | Subject | Landed in `phase1-plan.md` |
|----|-----|---------|------------------------------|
| AR-01 | 🔴 | `state` not bound to the browser | §3 **D6a**; §5.1 (`TxnCookieName`); §5.2 (HKDF "txn" subkey); §5.6 (`/oauth2/start` step 4, `/oauth2/callback` step 3); §9.1 `TestCallback_RejectsStateFromAnotherBrowser` |
| AR-02 | 🔴 | fail-open path classification | §3 **D2a**; §5.7 class 7 = **gated** + `path.Clean` normalization + allowlist; §6.2 (class column + census cross-reference); §9.1 `TestMiddleware_RouteCensus`, `TestMiddleware_UncleanPathCannotBypassGate` |
| AR-03 | 🔴 | request-derived security decisions | §4.2 (`OAUTH2_EXTERNAL_URL`/`REDIRECT_URL` mandatory, `OAUTH2_COOKIE_SECURE` default **true**); §5.1 validation rules 1/5; §5.6 (derivation branch deleted); §6.3; §9.1 `TestConfig_EnabledRequiresExternalURL` |
| AR-04 | 🔴 | principal admission control | §4.2 (`OAUTH2_ALLOW_ANY_EMAIL_DOMAIN`, `OAUTH2_REQUIRE_EMAIL_VERIFIED`); §4.4 (fail-fast + WARN rows); §5.6 callback step 8; §9.1 `TestCallback_UnverifiedEmailIs403` |
| AR-05 | 🔴 | endpoint origin pinning | §4.2 (`OAUTH2_ALLOWED_ENDPOINTS`); §5.3 (same-origin rule, `CheckRedirect`); §9.1 `TestOIDC_RejectsCrossOriginEndpoints`, `TestOIDC_CrossOriginRedirectRejected`; §12 **R20** |
| AR-06 | 🔴 | alg allowlist too wide | §4.2 (`OAUTH2_ALLOWED_ALGS` default **`RS256`**, case-insensitive HS/none reject, WARN); §5.4 (`kty↔alg↔crv` pinning, `key_ops`); §5.5 (type-asserted verifier map); §9.1 |
| AR-07 | 🔴 | `sync.Once` single-flight | §5.4 (`singleflight` re-armable, one constant key `"jwks-refresh"`); §9.1 `TestJWKS_SingleFlightRearmsAfterCompletion` |
| AR-08 | 🔴 | discovery single-flight | §5.3 (`sf` + negative caching); §9.1 `TestOIDC_DiscoverySingleFlight`, `TestOIDC_DiscoveryNegativeCache` |
| AR-09 | 🔴 | IdP call timeouts | §4.2 (`OAUTH2_TOKEN_EXCHANGE_TIMEOUT`, `OAUTH2_USERINFO_TIMEOUT`, discovery 5 s); §5.1 (clamps); §5.3 (one shared client, per-request ctx, never `context.Background()`); §9.1 |
| AR-10 | 🔴 | token-exchange retry policy | §5.3 + §5.6 step 5 (single attempt, zero retries, 502 not 504); §9.1 `TestOIDC_ExchangeIsSingleAttempt` |
| AR-11 | 🔴 | unbounded, per-pod state store | §5.6 (cap + eviction + 429 + per-IP rate limit + **option (b)**: single-replica invariant, `strategy: Recreate`/`maxSurge: 0`); §8.2/§8.3; §12 **R18**; §9.1 `statestore_test.go` |
| AR-12 | 🔴 | `rd` login loop | §3 **D10**; §5.6 step 1 (`/oauth2/*` and `/fe/api/auth/me` rejected); §9.1 table cases |
| AR-13 / R-1 | 🔴 | `/fe/api/version` breaks gate G4 | §3 **D11 addendum**; §5.7 **class 4b**; §6.2 row + class column; §9.1 `TestMiddleware_VersionIsExempt`; §9.5 criterion 2 |
| AR-14 | 🔴 | phase-2 E11 status | *phase-2 plan (parallel worker)* |
| AR-15 | 🔴 | SSE probe unbounded | §7.5 (AbortController 3 s, `.then().catch(() => scheduleReconnect())`, shared latch); §9.3 `TestSSE_ProbeTimeoutKeepsBackoff` |
| AR-16 | 🔴 | probe timeout cached as `false` | §7.3 (`authState.isEnabled()` nulls the cached promise on timeout); §9.3 |
| AR-17 | 🔴 | phase-2 diff does not switch the chart | *phase-2 plan*; the **Phase-1 half** (chart-driven env, `secretKeyRef` sources) is in §8.2/§8.3 |
| AR-18 | 🔴 | daoduc single-upgrade exposure | *phase-2 plan*; the **chart-side** half is §8.2/§8.3/§8.4 (H-series) |
| AR-19 | 🔴 | ensem redirect lockout | *phase-2 plan*; the **app-side** half is §5.6 AR-54 short-circuit + ADR-001 I-1 |
| AR-20 | 🔴 | stale main checkout chart | *phase-2 plan*; this plan's own R17 covers the citation discipline |
| AR-21 | 🔴 | Helm prunes the edge objects | §8.2 (`oauth2Proxy.replicas` split from `enabled`); §8.3 (objects stay rendered at 0 replicas); §8.4 H3 |
| AR-22 | 🔴 | D15 → three-state model | §3 **D15** rewritten + **D17** (I-1/I-2/I-3); §8.2/§8.3 (overlap state); §8.4 H1-H4; §12 **R12** rewritten + **R19** |
| AR-23 | 🟡 | logout end-session during an outage | §5.6 logout step 3 (best-effort, skip when metadata is stale) |
| AR-24 | 🟡 | discovery timeout default/clamp | §4.2 (5 s, `[1s,30s]`); §5.1 rule 6 |
| AR-25 | 🟡 | kid-miss waiter semantics | §5.4 (waiters block on the in-flight refresh; only a too-soon miss 503s); §9.1 |
| AR-26 | 🟡 | grace/rotation semantics | §4.2 (`OAUTH2_JWKS_STALE_GRACE`); §4.4 (grace covers fetch failures only); §5.4; §9.1 |
| AR-27 | 🟡 | offline session continuity | §4.4 (invariant row); §5.7 (session validation is fully offline) |
| AR-28 | 🟡 | Casdoor outage during soak | *phase-2 plan* |
| AR-29 | 🟡 | `/oauth2/start` first-use cost | §5.6 (per-IP rate limit + the capped store); §12 **R18** |
| AR-30 | 🟡 | `azp` / `aud` rules | §5.5 (OIDC Core §3.1.3.7 rules); §9.1 `TestJWT_AudAzpRules` |
| AR-31 | 🟡 | id_token vs access token | §5.5 (`typ`: accept `"JWT"`/absent, **reject** `"at+jwt"` on the id-token path; `at+jwt` is a D16/future concern); §9.1 `TestJWT_RejectsAtJwtInIdTokenPath` |
| AR-32 | 🟡 | shared cookie key / derivation ladder | §4.3 (6-rung ladder, ≥16-byte passphrase, HKDF subkeys); §5.1/§5.2 |
| AR-33 | 🟡 | cookie budget at seal time | §3 **D9**; §5.2 (per-claim caps, drop `preferred_username`, seal-time encoded budget) |
| AR-34 | 🟡 | issuer normalization | §5.3 (both sides normalized; the error prints both) |
| AR-35 | 🟡 | userinfo claims unbound | §5.3 (`userinfo.sub == id_token.sub` else 403; 64 KiB cap; prefer the id_token email) |
| AR-36 | 🟡 | logout CSRF + unvalidated redirect | §4.2 (`OAUTH2_LOGOUT_REDIRECT`); §5.1 rule 8; §5.6 (**POST-only**, 405 on GET, pinned `post_logout_redirect_uri`); §7.6 (FE posts); §9.1/§9.3 |
| AR-37 | 🟡 | deferred bearer mode unpinned | §3 **D16**; §5.5 (`at+jwt` accepted only in that mode) |
| AR-38 | 🟡 | attribution boundary | §3 **D14** + §16 row 18 (`X-User` has no producer; `created_by` stays `"api"`) |
| AR-39 | 🟡 | log-surface contradiction | §4.3 (**authoritative log-surface table**, no events-bus publication); §4.4 (boot line); §5.6 (login/failure rows) |
| AR-40 | 🟡 | §6.2 completeness | §6.2 (`GET /ui` row, class column, cross-reference, `/fe/api/models*` citation `:196-199`, MCP **conditional** rows) |
| AR-41 | 🟡 | dormant delta wording | §9.5 criterion 1 ("one constant-JSON route + one probe request per page load") |
| AR-42 | 🟡 | redirect latch never resets | §7.3 (8 s cooldown re-arm; reset semantics stated) |
| AR-43 | 🟡 | dormant/edge 302 ⇒ fetch TypeError | §7.3 (`looksLikeCrossOriginRejection` + one-shot `reloadOnce()`) |
| AR-44…AR-47 | 🟡 | ensem/HTTPRoute/runbook/GitOps | *phase-2 plan* |
| AR-48 | 🟡 | weak render guard | §8.3 (`required` issuer/client values when enabled); §8.4 **H6 = EMPTY diff** |
| AR-49 | 🟡 | Cloudflare premise | *phase-2 plan* |
| AR-50 | 🟡 | `gate-ensem.sh` | *phase-2 plan* |
| AR-51 | 🟡 | runbook path | *plan-overview (parallel)* |
| AR-52 | 🟡 | overlap precondition P7′ | §12 **R12** (recorded); the operator schedule is the phase-2 plan's |
| AR-53 | 🟡 | pod straddle | §8.2 (`strategy: Recreate`, `maxSurge: 0`); §8.3; §12 **R18** |
| AR-54 | 🟡 | `/oauth2/start` valid-session short-circuit | §5.6 step 0; §9.1 `TestStart_ValidSessionShortCircuits` |
| AR-55 | 🟢 | JOSE hygiene | §5.5 (`crit` rejected, `b64:false` rejected, `at+jwt` for the deferred bearer mode) |
| AR-56 | 🟢 | ES256 cell normativity | §5.5 (**exact length 64/96/132, `ecdsa.Verify`, DER rejected**) — the previous self-contradictory cell is replaced |
| AR-57 | 🟢 | `JWK.alg` / `key_ops` / type-assert | §5.4 + §5.5; §9.1 |
| AR-58 | 🟢 | gzip risk wording overstated | §6.1 rationale 2 (cites `pkg/middleware/gzipmw/gzip.go:39`; the placement stands for cheapness, not zip-bomb defence) |
| AR-59 | 🟢 | unclean-path bypass | §5.7 (`path.Clean` normalization); §9.1 `TestMiddleware_UncleanPathCannotBypassGate`; §16 row 19 |
| AR-60 | 🟢 | FE census breadth | §7.4 (the `EventSource\|WebSocket\|XMLHttpRequest\|sendBeacon\|axios\|serviceWorker` grep + subresource caveat); §9.3 `clientChannels.test.ts` |
| AR-61 | 🟢 | MCP "unprotected" intent | §6.2 (MCP rows **conditional** + the intent note) |
| AR-62 | 🟢 | research-assumption updates | §15 item 6 + §16 final paragraph (discovery **resolved**, `jwks_uri=/.well-known/jwks`, kid `cert-built-in`/alg `RS256`, `preferred_username` **absent**, `TokenSigningMethod` read at commissioning) |
| AR-63 | 🟢 | mode-detection determinism | §4.3 (6-rung ladder, mode string logged only) |
| AR-64 | 🟢 | `app-root:/ui/` behavior | §8.3 (`ingress-protected.yaml` row — one benign nginx short-circuit redirect) |
| AR-65 | 🟢 | single-replica limitation stated | §5.6 (placement-limitation paragraph) |
| AR-66 | 🟢 | keep the SPA determinism proof | §7.7 (unchanged) + §9.5 criterion 6 |

**Net effect on the Phase-1 scope:** no new feature surface was added — the amendments **harden** the existing design (fail-closed classification, browser-bound `state`, pinned origins, explicit identity posture, bounded concurrency) and **narrow** it (RS256-only default, `preferred_username` dropped, POST-only logout, three-state chart instead of a binary guard). The Phase-1 implementation task list (§10) is extended by the new test names in §9.1/§9.3 and by the chart-state work in T15; **no new production package** is introduced.
