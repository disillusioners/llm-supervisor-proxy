# Functional Verification Gate — internal-oauth2 Phase 1 (dormant-by-default)

- **Date:** 2026-11-19 (dispatched ~17:01 UTC, completed ~17:4x UTC)
- **Subject:** `feature/internal-oauth2` @ `b5da8cd` (base `92c4234`), worktree `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-internal-oauth2`
- **Mode:** INDEPENDENT RE-GATE — developer's R1–R12 all-PASS claims were NOT trusted; every matrix row re-run from scratch by a dedicated worker.
- **Dispatcher:** tester (b4c3cad7 tree root). **Commit:** not required by commission; this file is the only repo write.

## OVERALL VERDICT: **PASS-WITH-RESIDUALS**

Every matrix row passed on independent evidence. Two residual classes carried forward:
1. **One NEW product defect** (minor, bounded): `rd` path-traversal bypass of the OAuth2 redirect sanitizer denylist (see §NEW DEFECT).
2. **Mock-realism audit findings** (0 critical / 5 important / 5 nice-to-have) — headlined by F8, an unfulfilled plan-mandated live-Casdoor `typ` cross-check that is live-only, deployment-breaking-if-wrong, and invisible to the current suite (matters at cutover, not for dormant ship).

No row FAILed. No new quarantines. Dormant-by-default behavior fully proven at the binary, HTTP, env-fence, and embedded-UI levels.

---

## Worker / row map (adjudicated by instance ID; envelope `name=` labels arrived shuffled — IDs + task content are the identity)

| Row | Mission | Worker instance | Verdict |
|---|---|---|---|
| 1 | Pack A release-gate slice | edc0a81a-66a6-4dee-a99c-3583eb5c0e31 | **PASS** |
| 2 | Pack B anchored complement | c64dc846-a1da-4c35-ad94-3b8ebc25b8d5 | **PASS-with-quarantined-exceptions** (pre-existing external only) |
| 3 | pkg/oauth2 focused + anti-vacuity | cf6eb707-6c40-4850-b6ee-41776f878937 | **PASS** (129/129, 0 vacuous) |
| 4+8 | Dormant-boot real binary + embedded UI | a7474524-f914-45a0-aeb5-3c1acd68d4dc | **PASS** (8/8 probes; 1 disclosed deviation, see §Corrections) |
| 5 | Enabled-mode full-flow E2E vs stub IdP | 946e6961-53b6-4f41-8250-4725d6f61b3e | **PASS** (19/19 cases) + 1 NEW defect |
| 6 | Mock realism audit | 6cc32bce-02b7-4ef6-8c54-fce4b6e665c9 | **FINDINGS** (0 critical / 5 important / 5 NTH) |
| 7 | FE vitest/tsc/vite determinism | d8d27798-4e8e-48f2-9a01-af2a3a8f5f08 | **PASS** (5/5) |
| 9 | Helm three-state + census gate | 10a87e09-8494-498d-9734-d587a625d4df | **PASS** (A/B/C/D/E1/E2) |
| 10 | Boundary + migrations + ensure.md critical | 8e749889-8f37-4fab-a4c5-e9159081168b | **PASS** (4/4) |

Artifacts: `/tmp/oauth2-gate/{1-pack-a,2-pack-b,3-oauth2,4-boundary,5-dormant,6-enabled,7-mockaudit,8-fe,9-helm}/`.

---

## Row evidence (commands condensed; full logs in each artifact dir)

### Row 1 — Pack A: PASS
`env -u DATABASE_URL -u PORT HOME=$(mktemp -d) GOPATH=/home/nea/go GOMODCACHE=/home/nea/go/pkg/mod GOCACHE=/home/nea/.cache/go-build GOTOOLCHAIN=local timeout 300 go test -count=1 -timeout=240s ./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...` → **exit 0, 150 s wall** (bash wrapper, `PIPESTATUS[0]`).
```
ok  pkg/proxy              146.991s   ← under the 240s internal cap, 93s headroom
ok  pkg/store/database      55.796s
ok  pkg/ui                    7.246s   (+ proxy/normalizers, proxy/token, proxy/translator, store, decision, cmd — all ok)
```
- Quarantine flakes CloseLifecycle / PeakHourParity: **did not fire**.
- Hermeticity proven at host level: real `~/.config/llm-supervisor-proxy/config.json` md5 `5234b626…` + mtime 2026-10-18 unchanged pre→post; `go.mod`/`go.sum` md5 unchanged.
- ⚠ Watch item: wall band now 150 s (house record was 110–120 s) — recorded via POK `packa-01-plain-runtime`.

### Row 2 — Pack B: PASS-with-quarantined-exceptions
`go list ./... | grep -vE 'llm-supervisor-proxy/(cmd|pkg/(proxy|store|decision|ui))(/|$)'` (module anchor verified against go.mod) → 32 packages, all `ok`/`[no test files]`, ~50 s.
- Sole failures: the two **quarantined** httpbin.org redirect tests — `handlers_api_test.go:1864` `TestHandleTestMCPServerDirect_RedirectToLocalhostBlocked`, `:1904` `…RedirectToPrivateIPBlocked` — identical server-side `403 Forbidden redirect target` signature on isolated re-run (exit 1). Matches QUARANTINE.md (external third-party policy change; pre-existing; branch-orthogonal). **NOT a feature failure.**
- `pkg/oauth2` inside slice: ok 4.217 s. credentiallb ok 4.480 s (TOCTOU did not fire). usage ok 49.104 s (stress did not fire). SSRF trio absent.
- Evidence: `pkgs.txt`, `pack-b.log`, `httpbin-isolated.log`, `httpbin-exit.txt`.

### Row 3 — pkg/oauth2 focused: PASS, anti-vacuous
Package is **entirely new at base** (`git ls-tree 92c4234 -- pkg/oauth2` → 0 files; 18 files, +6083 lines). All 129 test funcs are ADDED.
`timeout 300 go test -count=1 -timeout=240s -v ./pkg/oauth2/` → exit 0, `ok … 5.376s` (run A) / `4.050s` (pristine-tree run B).
- **129 top-level `--- PASS` / 0 SKIP / 0 FAIL**; zero subtests (129 flat `=== RUN`, no `=== CONT`); both counting conventions coincide.
- **Anti-vacuity: 129/129 added names each match exactly one literal col-0 `--- PASS:` line** — bidirectional set equality (added∖PASS = 0, PASS∖added = 0), zero prefix collisions, zero `FAIL|panic|DATA RACE|--- SKIP` markers. Table: `anti-vacuity-table.txt`.
- Determinism: run B PASS-name set byte-identical to run A.
- Sample literal lines: `--- PASS: TestConfigFromEnv_DormantByDefault`, `--- PASS: TestJWT_RejectsHS256AlgConfusion`, `--- PASS: TestCallback_JWKSRateLimitAnswers503AndPreservesState`, `--- PASS: TestE2E_Flow`.
- ⚠ Toolchain side-effect caught: first run left ` M go.mod` (go1.26 promoted jsonrepair/tiktoken-go indirect→direct; versions unchanged). Proven non-verdict-affecting (`go list -mod=readonly -deps ./pkg/oauth2/` exit 0 empty; neither module in pkg/oauth2 graph), restored, tree pristine. Anomaly dcce0f64 (minor). **Future gates in this repo: `git status` after go commands, restore before shipping.**
- Hermeticity: pkg/oauth2's only repo-internal dep is pkg/identity → config.json-rewrite risk does not apply to this pack (POK `verify-hermetic-01-oauth2-self-contained`).

### Rows 4+8 — Dormant-boot REAL binary + embedded UI: PASS
Build: `go build -o lsp-oauth2-test ./cmd` → 27,037,018 B real binary (`go version -m` = go1.26.0; NOT the 3.7 MB root stub).
Fence verified via `/proc/$PID/environ`: child env has **no OAUTH2_*, no POSTGRES_*, no DATABASE_URL, no DAEMON_PORT**; `HOME=$(mktemp)`, `PORT=11080`, `APPLY_ENV_OVERRIDES=1`; boot.log: `[oauth2] internal OAuth2 DISABLED (set OAUTH2_ENABLED=true to enable)`.
| Probe | Expected | Actual | Verdict |
|---|---|---|---|
| `/fe/api/auth/me` | 200 `{"enabled":false,…}` | **200**, 74 B byte-exact: `{"authenticated":false,"enabled":false,"state_store_count":0,"user":null}` | PASS |
| `/oauth2/start` | 404 | **404** | PASS |
| `/oauth2/callback` | 404 | **404** | PASS |
| `/v1/models` | 200 public | **200** `{"data":[],"object":"list"}` | PASS |
| `/healthz` | 200 | **200** `ok` | PASS |
| `/fe/api/models` | 200 (dormant=ungated) | **200** `[]` | PASS |
| `/` | 302→`/ui/` | **307**→`Location: /ui/` (see §Corrections) | PASS w/ deviation |
| `/ui/` | 200 SPA | **200** `text/html` full SPA shell | PASS |
- No `panic|fatal` in either boot log; clean SIGTERM both boots.
- **Embedded-bundle OAuth2 proof (row 8):** served JS `/ui/assets/index-DlypSZDz.js` → 200 `text/javascript` 318,890 B with literal `oauth2/start` (`const zu="/oauth2/start"`; also `oauth2/logout`, `oauth2_redirect_hops`, `OAuth2`); served CSS 200 `text/css`. Committed-tree marker: `pkg/ui/static/assets/index-DlypSZDz.js` — **same content-hash filename as served** ⇒ served bytes == committed bundle. Browser automation unavailable → curl-level fallback used (disclosed, authorized).
- Cleanup PID-verified (cmdline + `ss -ltnp` port check before each kill); port 11080 freed; 12080/12090 untouched.

### Row 5 — Enabled-mode full-flow E2E (stub IdP): PASS 19/19 + NEW defect
Stub: stdlib Python + openssl RSA signer, Casdoor-shaped discovery (`/login/oauth/authorize`, `/api/login/oauth/access_token`, `/api/userinfo`), JWKS `kty/kid/use/alg/n/e`, 10 control modes. Proxy booted enabled on 12080 (fenced), stub on 12090. Env names resolved from `pkg/oauth2/config.go` (17 OAUTH2_* vars incl. `OAUTH2_RATE_LIMIT_RPM/_BURST`); cookie secret base64-32 (`secret_mode=base64-std-32` logged).
Positive (a–g): unauth `/fe/api/models` **401** `{"error":"unauthenticated"}` → `/oauth2/start?rd=/ui/` **302** to IdP with `state`+`code_challenge`+`S256`+`nonce` + `llmproxy_oauth2_txn` cookie → callback **302 `/ui/`** + `Set-Cookie llmproxy_oauth2=…` → `/fe/api/models` **200** → `/fe/api/auth/me` **200** `{"authenticated":true,"enabled":true,"state_store_count":N,"user":{sub,email,name}}` → logout **302** + `llmproxy_oauth2=; Max-Age=0` → **401** again.
Negative battery:
- h1 replayed state → **400 invalid_state**; h2 cross-jar state → **400** (own-cookie control 302)
- h3 expired / h4 wrong-aud / h5 wrong-iss / h6 `alg=none` / h7 HS256-forged → **callback 400 + protected 401**
- **h8 (W2 alg-pin, BEHAVIORAL):** JWKS alg RS384 vs header RS256 → **400 + 401**; control RS384/RS384 → **302 + 200** (proves the pin is the rejecting mechanism)
- h9 oversized JWKS 300,438 B (valid JSON) → **rejected in 65 ms**, no hang (256 KiB+1 bounded read attributable)
- h10 JWKS HTTP 500 → stale-grace preserved live key set (login 302 through the 500; restored → 302 + protected 200)
- **h11 (S3) rate limit:** RPM=5: 20 starts → 3×302 + 17×**429**; 429s carried **no Set-Cookie, no Location**; `state_store_count` 0→3, delta == 302 count ⇒ **429 came BEFORE state consumption**
- h12 `rd=/oauth2/start` → sanitized to final `Location: /ui/` (all 9 rd variants land on `/ui/`); supplementary kid-mismatch m10 → 400+401
- ⚠ Harness integrity note: initial h8 run was a **false green** (stale port-bound proxy + warm JWKS cache); worker detected it, fixed the harness (busy-port refusal + cold-cache proxy per JWKS-sensitive case) and re-ran — reported verdicts are from the corrected harness.
- Ports 12080/12090 verified free after PID-checked cleanup.

### Row 6 — Mock realism audit: FINDINGS (0 critical / 5 important / 5 nice-to-have)
Contract extracted from production source (`pkg/ui/server.go:285/:305-309`, `pkg/oauth2/middleware.go:188-193`, `jwt.go:166-174`). 7 FE test files enumerated (incl. 1 the auth-grep misses) + 19 Go stub sites. Full detail: `/tmp/oauth2-gate/7-mockaudit/FINDINGS.md` (211 lines).
**Important:**
- **F8** — every stub hardcodes JOSE header `typ:"JWT"`; verifier fails closed on any other present value (`jwt.go:172`); live Casdoor's actual header `typ` is UNVERIFIED. Plan `phase1-plan.md:533` mandates this cross-check pre-merge — **unfulfilled**. If live tokens carry a different `typ`, every callback 400s — deployment-breaking, live-only, zero suite coverage. (Claim-level `typ` parsed but never validated — AR-31 gap.)
- **F7** — stub `aud` is a string; live Casdoor sends array + always `azp`; single-element-array live shape and `azp`-present path never exercised end-to-end (`jwt.go:302-305` regression would pass suite then 400 real logins).
- **F2** — FE SSE auth-break has no 200-probe case ⇒ the **dormant path (shipped default) is untested**; a parse regression would navigate dormant pages to `/oauth2/start` (404) with no FE test catching it.
- **F1** — "enabled+anonymous" mocked as **200** in 4 files where backend answers **401** (apiFetch/redirectStorm/Header.auth/useAuth) — 401 arm IS covered byte-faithfully elsewhere, so mapping-safe but per-file blind.
- **F3** — `Header.test.tsx` answers probe with catch-all `200 {}` — never a real shape; plan-required stub satisfied only accidentally.
Nice-to-have: F4 dropped contract keys (`state_store_count`/`user`), F5 gate-body-on-probe mock (acceptable), F6 FE never asserts /oauth2/* redirects (by design — Go side owns: `handler_test.go:604/:614/:625/:234`), F9 stub /token 200-for-any-code (400 arm exists for PKCE mismatch), F10 stub /userinfo unreachable (email always in id_token), F11 catch-all 200 stubs in oidc_test.go (scoped client-mechanics tests; main stub is path-strict with 404 default).
**Verified-clean:** 401 mocks byte-faithful to both Go bodies; E2E genuinely drives token exchange over HTTP incl. real nonce; real 4xx/5xx arms exist; no shared/global FE mock file (no setupFiles); census test is a real anti-drift lane.

### Row 7 — FE gate: PASS 5/5
All in `/tmp` git-archive scratch (+ node_modules symlink); worktree read-only.
- **vitest:** `47 passed (47)` files, **484 passed (484)** tests, 0 failed/skipped/todo, exit 0; baseline re-assert `tsc-baseline.test.ts` (5 tests) present and passing.
- **tsc:** exit **2** with EXACTLY the frozen **18 errors / 9 files** — per-file counts Δ=0 across all nine baseline files (SettingsPage 5, ModelsTab 6, EventLog/CredentialsTab/ModelForm/MCPServersTab/ModelUsageChart/UsageChart/helpers 1 each).
- **vite build ×2:** B1=B2 byte-identical (empty diff) AND identical to committed `pkg/ui/static/` (empty diff; same 4 content-hashed files); both exits 0; archive-copy fidelity cross-checked pre-build.
- Zero-mod proof: worktree `git status` = only pre-existing `.agents/tidier/notes.md` (concurrent tidier agent); `pkg/ui/static` hash unchanged post-run.

### Row 9 — Helm three-state + census: PASS (A/B/C/D/E1/E2)
Chart `k8s/` (same path at base; helm v3.22.1 at `/home/nea/bin/helm` — not on PATH; discovered, nothing installed).
- **A (H6):** dormant render ≡ base render **byte-identical** (diff empty; sha256 `fd63b5d9…` both, 7758 B); edge objects present in dormant render incl. `Deployment/oauth2-proxy` replicas 1; no strategy/maxSurge block.
- **B (overlap = `internalOAuth2.enabled=true`+`overlap=true`):** `/oauth2` on app ingress → `llmproxy-llm-supervisor-proxy:4321`; edge replicas 1; edge auth annotations retained.
- **C (internal = `oauth2Proxy.enabled=false`+`replicas=0`):** edge replicas 0 with edge Service still rendered; app ingress `/oauth2`; env `secretKeyRef` exactly `CLIENT_ID`/`CLIENT_SECRET`/`COOKIE_SECRET` from `oauth2-proxy-secret`; edge annotations dropped.
- **D (W5):** `--set replicaCount=2` + enabled → **EXIT=1** with guard error (AR-11/AR-65 in-memory state store rationale); same in overlap. Guard ORDER chain all five fire in house order. Recreate renders **no** `maxSurge`/`rollingUpdate`; `strategy=RollingUpdate` control DOES emit maxSurge (conditional works).
- **E1:** `make census-gate` on branch → **EXIT=0** (`registered=44 census-paths=45 — OK`; pure grep, offline-safe).
- **E2:** scratch git-archive copy + one injected unlisted route `/oauth2/probe-unlisted` → **EXIT=2**, failure names the route (BLK-4 anti-drift). Worktree untouched throughout.

### Row 10 — Boundary + ensure.md critical: PASS 4/4
- Production-path diff `92c4234..b5da8cd -- pkg/proxy pkg/auth pkg/authz pkg/decision pkg/imggen pkg/store/database pkg/config .github/ go.mod go.sum` → **0 bytes**.
- Migrations **91 == 91**, reconciled: 88 numbered SQL (44 postgres + 44 sqlite) + migrate.go + 2 migration test files; highest `034_add_allowed_groups` both refs; no migration added.
- Change surface: 55 files, +12,569/−98 (36 A / 17 M / 1 R050 / 1 R081) — all outside the enumerated production set. **Disclosure:** three production files changed OUTSIDE the enumerated set, audited clean: `pkg/ui/server.go` (5 pure-insertion hunks — import, struct field, 2 methods, `/fe/api/auth/me` route; no existing handler modified), `cmd/main.go` (additive wiring; `/healthz` closure → named handler same 200 "ok"; `buildHandlerChain` dormant branch reproduces the original chain exactly), `pkg/identity/identity.go` (new, zero internal imports). Makefile import guards corroborate (oauth2↔proxy/ui/store: no imports either way).
- ensure.md Critical: `go build ./...` **exit 0**, `go vet ./...` **exit 0** (go1.26.0 via cached-toolchain PATH prepend — system go 1.22.2 cannot build offline).

---

## NEW DEFECT (found by row 5; anomaly 34222ee8, severity minor)

**`rd` path-traversal bypass of the OAuth2 redirect sanitizer denylist.**
`sanitizeRedirectTarget` checks the RAW `rd` string, but Go's `http.Redirect` `path.Clean`s the emitted Location AFTER the guard — `rd=/ui/../oauth2/start` escapes the `/oauth2/*` denylist and lands on `/oauth2/start` (likewise `/oauth2/logout`, `/fe/api/auth/me`). Source carries the TODO at `pkg/oauth2/handler.go:662`.
Bounded by measurement: **no open redirect** (traversal yields same-origin paths like `/evil.com`, never `//evil.com`), **no infinite loop** (valid session short-circuits `/oauth2/start`→`/ui/`; chain ends in 2 hops), logout stays POST-only (GET→405), auth/me still 401 unauthenticated.
**Fix:** `path.Clean(rd)` before the prefix/equality checks. Not quick-fixed (gate is report-only by charter).

## Residuals register (carried to leader)

| # | Item | Severity | Owner / when |
|---|---|---|---|
| R1 | NEW defect: rd path-traversal denylist bypass (`handler.go:662` TODO) | 🟠 important (bounded minor per anomaly) | developer, next iteration; fix is ~1 line + tests |
| R2 | F8: live Casdoor JOSE-header `typ` cross-check unfulfilled (plan §533 pre-merge step) | 🟠 important at cutover | **cutover gate** — decode one real id_token (header/claims only) before enabling on any host |
| R3 | F7: stub/live `aud`-array + `azp` shape never exercised end-to-end | 🟠 important | developer (stub realism) next iteration |
| R4 | F2: dormant 200-probe SSE path untested in FE | 🟠 important | developer next iteration |
| R5 | F1/F3: per-file mock status drift (200-where-401; catch-all `{}`) | 🟠 important (per-file blind spots) | developer next iteration |
| R6 | F4/F9/F10/F11 cosmetic/dead-surface cleanups | 🟢 nice-to-have | backlog |
| R7 | Pack A wall-time band now 150 s (240 s cap ⇒ thinner margin) | 🟢 watch | tester mainline watch (POK'd) |

## Gate-prompt model corrections (not product defects)

1. `GET /` returns **307** (`http.StatusTemporaryRedirect`, `pkg/ui/server.go:203`), not 302 — Location `/ui/` exact; pre-existing intentional behavior.
2. W5 guard key is **top-level `replicaCount`**, not `internalOAuth2.replicaCount` (Helm silently accepts unknown keys; the guard works).
3. Overlap state = `internalOAuth2.enabled=true` + `internalOAuth2.overlap=true` (no strategy-based overlap notion; strategy defaults Recreate).
4. Internal state has no authentik/IdP key — the app itself is the OIDC client via `internalOAuth2.issuerURL`.
5. Edge-object retention is governed by `oauth2Proxy.replicas`, not any `internalOAuth2` key.
6. Migration count 91 reconciles as 88 SQL + migrate.go + 2 test files (confirmation, not correction).

## Quarantine & flake status

- No new quarantines; QUARANTINE.md unchanged by this gate (only-write rule).
- httpbin pair reconfirmed (deterministic external 403, identical signature) — sole Pack B failures.
- TOCTOU_CoolingReverify, CloseLifecycle, PeakHourParity, usage-stress, SSRF trio: **none fired**.

## ensure.md status

- **Critical 4/4 PASS**: Go tests (Pack A + Pack B via packs, quarantined exceptions excluded per quarantine-aware rule) · `go vet ./...` exit 0 · `go build ./...` exit 0 · `tsc --noEmit` zero NEW errors vs baseline (exact 18/9 parity).
- Important (peak-hour rows): out of scope by blast radius — `pkg/store/database` production paths diff-empty; module untouched.

## Operational notes for the record

- Concurrent tidier agent shares this worktree (`.agents/tidier/notes.md` writes, mtime 17:26-17:28) ⇒ `git status` cleanliness is NOT assertable here; **committed-state diff is the gate authority** (all rows used it).
- go1.26 toolchain PATH prepend is mandatory (system go 1.22.2 fails offline); go.mod indirect→direct rewrite trap fired once (row 3), restored, POK'd.
- Report envelope `name=` labels arrived shuffled vs spawn names; adjudication keyed on instance IDs + task content (mapping table above) — cosmetic delivery quirk, no identity ambiguity in evidence.
- Ports: 11080 (dormant), 12080/12090 (enabled/stub) — all mock-range, all verified freed; 8088 never touched.
