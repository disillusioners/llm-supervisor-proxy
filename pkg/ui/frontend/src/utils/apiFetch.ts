// apiFetch — the single 401 choke point for every UI request.
//
// Every client-initiated request channel routes through this module so
// an expired internal-oauth2 session drives ONE bounded login redirect
// instead of a storm of raw errors (internal-oauth2 phase 1, plan
// §7.3 / D12 / AR-42 / AR-43).
//
// AR-42: the redirect latch is bounded by an 8 s cooldown — it re-arms
// after the cooldown, so a same-tab retry can attempt one more
// navigation. A full page reload creates a new module state, which
// re-arms the latch naturally. Never an unbounded loop.
//
// AR-43: in the dormant/edge world the ingress answers a browser
// request with a 302 to the IdP; the underlying request follows that
// redirect cross-origin and REJECTS with a TypeError — it is NOT a
// 401, so the 401 path never fires. On the still-edged host (ensem
// stays edged until Phase 4) that TypeError must trigger a one-shot
// page reload so the browser can perform the edge's 302 -> IdP
// navigation natively. Bounded: only after the auth probe RESOLVED to
// enabled:true, GET requests only, at most once per page load.
//
// S10 (review item): even the per-page-load latch above cannot stop a
// loop that spans page loads (401 -> /oauth2/start -> callback -> SPA
// -> 401 -> ...). redirectToLogin therefore consults the per-tab
// sessionStorage hop counter (utils/redirectStorm): 3 initiated hops
// abort the cycle with a visible, dismissible error banner. The AR-42
// cooldown, the probe exemption, the one-shot reload and the SSE loop
// break are untouched; dormant mode never touches the counter (it
// never redirects).
import { authState } from '../hooks/useAuth';
import { AUTH_ME_PATH } from './authProbe';
import {
  isRedirectStormTripped,
  showRedirectStormError,
  incrementRedirectHopCount,
} from './redirectStorm';

const LOGIN_PATH = '/oauth2/start';
const LATCH_COOLDOWN_MS = 8000; // AR-42: the latch re-arms after a cooldown
let redirectInitiated = false;
let redirectAt = 0;

export function authRedirectTarget(): string {
  const rd = `${window.location.pathname}${window.location.search}`;
  return `${LOGIN_PATH}?rd=${encodeURIComponent(rd)}`;
}

export function resetAuthRedirectLatch(): void {
  // Test hook — a full page reload creates a new module state, which
  // re-arms the latch naturally; tests call this between cases.
  redirectInitiated = false;
  redirectAt = 0;
}

/** AR-42: the latch is bounded by a cooldown, not permanent. */
function latchArmed(): boolean {
  if (!redirectInitiated) return false;
  if (Date.now() - redirectAt > LATCH_COOLDOWN_MS) {
    // re-arm
    redirectInitiated = false;
    return false;
  }
  return true;
}

/**
 * The browser origin the FE is served from — '' when there is no window.
 * Without an origin nothing can be shown to be same-origin, so the
 * matcher answers FALSE for every input in that case.
 */
function currentOrigin(): string {
  if (typeof window === 'undefined') return '';
  const loc = window.location;
  return loc && typeof loc.origin === 'string' ? loc.origin : '';
}

/**
 * The URL string of a request input, whatever shape it arrives in: a
 * plain string, a URL, or a Request. null when the shape carries no
 * readable URL (the matcher then answers FALSE).
 */
function probeUrlCandidate(input: RequestInfo | URL): string | null {
  if (typeof input === 'string') return input;
  if (typeof URL !== 'undefined' && input instanceof URL) return input.href;
  const request = input as unknown as { url?: unknown };
  return typeof request.url === 'string' ? request.url : null;
}

/**
 * The probe exemption predicate — is this request the identity probe?
 *
 * Matched by the PARSED PATHNAME against the SHARED AUTH_ME_PATH
 * constant (utils/authProbe) — never by a substring of the raw URL. The
 * old substring form was over-broad in BOTH directions:
 *   * `/fe/api/models?next=/fe/api/auth/me` matched, so a 401 there
 *     would NEVER redirect (an auth-loop hazard);
 *   * a cross-origin request to the same path is not our probe at all.
 *
 * TOTAL by construction: every input shape yields a boolean.
 *
 * UNPARSEABLE INPUT -> FALSE (the ordinary redirect path). Rationale:
 * the two real probe call sites (hooks/useAuth.ts, hooks/useEvents.ts)
 * issue the RELATIVE AUTH_ME_PATH, which always parses, so an
 * unparseable input CANNOT be the probe and the exemption is never
 * needed for it. FALSE keeps the ordinary 401 -> login redirect for
 * every other input (the safe default for a gate), while TRUE would
 * silently exempt an arbitrary 401 from the redirect. The matcher must
 * never throw either: a throw inside the 401 branch would replace the
 * redirect with an unhandled error — strictly worse than a redirect.
 */
export function isAuthProbe(input: RequestInfo | URL): boolean {
  const raw = probeUrlCandidate(input);
  if (raw === null) return false;
  const origin = currentOrigin();
  let parsed: URL;
  try {
    parsed = origin ? new URL(raw, origin) : new URL(raw);
  } catch {
    return false; // unparseable -> NOT the probe
  }
  return parsed.pathname === AUTH_ME_PATH && parsed.origin === origin;
}

/** AR-43: the shared one-shot reload primitive (at most once per page load). */
let reloadIssued = false;
export function reloadOnce(): void {
  if (reloadIssued) return;
  reloadIssued = true;
  window.location.reload();
}

/** Test hook — resets the per-page-load one-shot reload latch. */
export function resetReloadOnce(): void {
  reloadIssued = false;
}

export function looksLikeCrossOriginRejection(err: unknown): boolean {
  return err instanceof TypeError;
}

/**
 * Resolve the internal-mode flag for the 401 path.
 *
 * #11b: a REJECTED probe is UNKNOWN (AR-16) — it must never become an
 * unhandled rejection and never a redirect. The resolution is awaited
 * inside a try/catch, so a probe that rejects degrades to "not
 * enabled" exactly like a timeout does.
 */
async function resolveEnabled(): Promise<boolean> {
  try {
    return await authState.isEnabled();
  } catch {
    return false;
  }
}

/**
 * The shared one-shot login navigation. The fetch 401 path and the SSE
 * auth break (useEvents) share this latch — a separate latch would
 * allow a double navigation (AR-15).
 *
 * S10: this is the hop counter's single choke point. When the per-tab
 * counter has already burned the threshold hops, the redirect cycle is
 * ABORTED (no navigation, no counter increment) and the visible,
 * dismissible error banner is surfaced instead.
 */
export function redirectToLogin(): void {
  if (isRedirectStormTripped()) {
    showRedirectStormError();
    return;
  }
  if (latchArmed()) return;
  redirectInitiated = true;
  redirectAt = Date.now();
  incrementRedirectHopCount();
  window.location.assign(authRedirectTarget());
}

export async function guardedFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  let res: Response;
  try {
    res = await fetch(input, init);
  } catch (err) {
    // AR-43/B2: the reload fires ONLY when the auth probe has RESOLVED
    // to enabled:true AND the failing request is a GET (the reload path
    // is GET-only — a POST/PUT/DELETE rejection must surface as a normal
    // error, never a page reload). Before/without that resolution a
    // rejection surfaces as a plain error (no reload) — dormant mode is
    // provably inert: the dormant probe answers enabled:false, so the
    // TypeError path stays non-reloading even under a persistent backend
    // outage (no reload loop). See plan §9.5 criterion 1.
    const isGet = !init?.method || init.method.toUpperCase() === 'GET';
    if (authState.isEnabledResolved() && isGet && looksLikeCrossOriginRejection(err)) reloadOnce();
    throw err;
  }
  if (res.status === 401 && !isAuthProbe(input)) {
    // Only redirect when the INTERNAL oauth2 mode is active; in dormant
    // mode a 401 can only come from an upstream ingress and a redirect
    // to /oauth2/start would 404. isEnabled() resolves the pending probe
    // (bounded 3 s; a timeout resolves false and is never cached); a
    // REJECTED probe resolves false as well (#11b — the UNKNOWN path).
    const enabled = await resolveEnabled();
    if (enabled) redirectToLogin();
  }
  return res;
}
