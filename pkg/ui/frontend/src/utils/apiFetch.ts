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
import {
  isRedirectStormTripped,
  showRedirectStormError,
  incrementRedirectHopCount,
} from './redirectStorm';

// S10 re-export: the breaker API stays reachable from the guard module
// (the single choke point) without a second import path for consumers.
export {
  REDIRECT_STORM_THRESHOLD,
  REDIRECT_STORM_MAX_HOPS,
  MAX_REDIRECT_HOPS,
  getRedirectHopCount,
  redirectHopCount,
  isRedirectStormTripped,
  redirectStormTripped,
  incrementRedirectHopCount,
  resetRedirectStormBreaker,
  resetRedirectHopCounter,
  resetRedirectHopCount,
  resetRedirectCounter,
  resetStormBreaker,
  resetBreaker,
  clearRedirectStormBreaker,
  clearRedirectHopCounter,
  showRedirectStormError,
  removeRedirectStormError,
  redirectStormErrorVisible,
  redirectStormStorageKey,
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

export function isAuthProbe(input: RequestInfo | URL): boolean {
  const url = typeof input === 'string' ? input : input.toString();
  return url.includes('/fe/api/auth/me');
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
    // (bounded 3 s; a timeout resolves false and is never cached).
    const enabled = await authState.isEnabled(); // resolves the pending probe
    if (enabled) redirectToLogin();
  }
  return res;
}
