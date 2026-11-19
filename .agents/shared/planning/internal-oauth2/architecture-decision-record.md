# ADR-001 — Edge→App Auth Handover Interlock (internal-oauth2)

**Status:** Accepted (architect + council consensus, 2026-11-19) — pending operator sign-off on the `ensem` schedule
**Deciders:** Architect (aggregation), council `architect-council-oauth2-interlock` (2 councilors, models `agentic` + `coding`, skill `system-decomposition`)
**Scope:** `phase1-plan.md` §3/§8.3 (D15), §5.6; `phase2-switch-plan.md` §3.2/§3.3/§3.5/§3.6, §4.4/§4.5/§4.7
**Related:** `architecture-recommendation.md` (full amendment register AR-01…AR-66), `plan-overview.md` §9 (OQ-1…OQ-7)

---

## Context

OAuth is enforced today **only at the edge** (`oauth2-proxy` v7.6.0 + Casdoor) on both hosts. `internal-oauth2` moves the gate **into the binary** (`pkg/oauth2/`, dormant by default) so the operator surfaces (`/`, `/ui`, `/fe/api/*`) can be gated in-process, after which the edge layer is retired per host: Phase 3 `daoduc` (backup, Helm/nginx), Phase 4 `ensem` (primary, raw `kubectl apply` + Envoy Gateway `HTTPRoute`, **HARD HELD**).

The handover is not atomic in any single sense, and the plan as written creates three distinct windows:

- **Window A (exposure, daoduc).** One `helm upgrade --atomic` drops the nginx auth annotations; nginx reloads in seconds while pods roll in tens of seconds ⇒ the **old, un-gated pod serves `/fe/api/*`** unauthenticated (credential API keys, model config, token store). `--atomic` rolls back only on upgrade failure; `--wait` waits after apply.
- **Window B (forced, daoduc).** Phase 2's diff flips only `env.OAUTH2_ENABLED`; the chart's `secretKeyRef` block renders only under `internalOAuth2.enabled` ⇒ no `OAUTH2_CLIENT_ID` ⇒ startup fail-fast ⇒ **CrashLoopBackOff ⇒ the backup's `/v1/*` dies**. Enabling the toggle instead trips the D15 render guard ⇒ Window A. *The two documents jointly force A-or-B.*
- **Window C (lockout, ensem — PRIMARY).** Step 1 (deploy enforcing) → Step 2 (HTTPRoute repoint). The app's guard runs on every request that reaches the app and demands its own `llmproxy_oauth2` cookie, but the edge still owns `/oauth2` and holds only `_oauth2_proxy` ⇒ app 302 → edge `/oauth2` → oauth2-proxy → Casdoor → edge cookie only ⇒ app still 401 ⇒ 302 again: **infinite redirect loop**, operator-paced ⇒ unbounded. The plan's E11 probe expects 302 — which a loop also produces ⇒ the probe cannot distinguish working from locked out.

Additional constraint: the internal cookie is deliberately **not** interoperable with `_oauth2_proxy` (plan decision **OQ-5**).

## Decision

**Adopt per-path `/oauth2`-first choreography over a three-state Helm chart.**

1. **Ownership first.** Transfer `/oauth2` path ownership to the app **before** enabling app enforcement anywhere.
2. **Explicit overlap state.** Hold a designed *overlap* render state: the edge still gates `/`, `/ui`, `/fe/api`, **and** the app enforces behind it with its own flow already reachable.
3. **Edge last.** Remove the edge from the path only after every serving pod enforces.

The three states are **chart-level render states**, not new application modes — the application keeps `dormant` / `enforce` only. **No new trust surface**: no trusted header, no delegation hop, no CIDR, no foreign-cookie parsing. **OQ-5 stands unamended.**

## Invariants (the decision is valid iff these hold)

| # | Invariant |
|---|---|
| **I-1 Loop** | Whenever the app enforces, `/oauth2/*` must route to the app. |
| **I-2 Exposure** | Edge paths/annotations may drop only after *every* serving pod enforces. |
| **I-3 Liveness** | The activation that mounts enforcement must mount the secret env in the same act — never a bare `env.OAUTH2_ENABLED` flip. |

## Options considered

| Option | Verdict | Reason |
|---|---|---|
| **5. Per-path `/oauth2`-first + three-state chart** | ✅ **Accepted** | Satisfies I-1/I-2/I-3 with zero new trust surface, no new app mode, IdP-independent rollback. The overlap state is the safe state to roll back into. |
| 4. Session bridge (app calls the edge's `/oauth2/auth`) | Runner-up — **probe-gated** | Wins only if: ensem upstream cookie forwarding and daoduc `--set-xauthrequest` are verified, zero user-visible bounce is operationally required, and OQ-5 is re-scoped. Its selection depends on probes the primary does not currently pass. |
| 1. Shadow mode (app mints, does not enforce) | Demoted — optional rehearsal add-on | Buys login rehearsal; adds **zero** window-freedom; costs a new mode + a trust source. |
| 2. Edge-trust header (`X-Auth-Request-User`) | ❌ Rejected | Widens the trust boundary on a spoofable header; nginx does not strip client-supplied copies by default. |
| 3. Pure sequencing at *object* granularity (as written) | ❌ Refuted | No window-free ordering exists while `/oauth2` remains edge-owned. |

## Consequences

**Positive.** Zero unauthenticated window by construction (I-2); no unbounded lockout (I-1 — the app's flow is reachable in every enforcing state); `/v1/*` untouched (no proxy-lane code change); rollback is a route/chart revert, seconds-to-minutes, IdP-independent, with **no image rollback** (the v203 / migration-034 boundary is never crossed); the irreversible step on `ensem` reduces to **one pre-staged, diffed HTTPRoute apply**.

**Negative / accepted.** New three-state chart template logic — a wrong conditional can silently re-open Window A, making the chart the highest-risk artifact in the change (mitigated by byte-level render assertions for all three states + probe P-J). A **bounded, fail-closed overlap window** exists on the primary: users without a live edge session who attempt a fresh login during the overlap get a fail-closed ping-pong that terminates only when the edge leaves the path (mitigated by a short overlap, a low-traffic window, a user notice, the `/oauth2/start` valid-session short-circuit, and the operator holding a live session — precondition P7′). The chart must split `oauth2Proxy.enabled` (in-path) from **`oauth2Proxy.replicas`** (object existence) so the edge can be **idled at 0 replicas rather than pruned** — otherwise OQ-7's rollback anchor is destroyed by Helm 3 pruning.

**Supersedes.** The D15 binary mutual-exclusion model (both layers enabled = forbidden) becomes the three-state model; "both layers active" is a *designed* transition, not a failure. The phase-2 §4.7 R1 rationale ("oauth2-proxy in front ignores the proxy's internal handler") is **false** — the app's handler runs on every request that reaches the app — and is deleted; R1+R2 are bound into one scripted rollback unit, because "repointed HTTPRoute + rolled-back deployment" is the unauthenticated-exposure case.

## Rollback

- **`ensem`:** R1 = revert the HTTPRoute (seconds; the app may remain enforcing — that is the safe overlap state), R2 = revert the deployment. Executed as one unit.
- **`daoduc`:** `helm rollback` one revision (annotations + edge return; the app stays enforcing = overlap = safe). Tier-1 anchor remains the Phase-1 dormant image (`df12c62` + Phase 1) — never below v203, because migration 034 must be reversed before any pre-v203 image rollback.

## Open

- Operator confirmation of the `ensem` schedule and of the bounded overlap window (see `architecture-recommendation.md` §9, §10).
- Whether the session-bridge runner-up is preferred once its two probes are verified (it would supersede this ADR).
