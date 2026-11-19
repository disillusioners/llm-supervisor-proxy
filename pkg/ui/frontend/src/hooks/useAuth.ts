// useAuth — the one-shot identity probe + the module-level auth-state
// store (internal-oauth2 phase 1, plan §7.2/§7.3 — AR-16 / B2).
//
// The hook probes GET /fe/api/auth/me ONCE per page load and shares the
// result with every consumer: the module-level `authState` store holds
// the in-flight / resolved probe so non-hook code (the fetch guard in
// utils/apiFetch) can read the resolved mode without prop-drilling.
//
// Backend contract (plan §5.8, pinned — do NOT change here):
//   dormant              {"enabled":false,"authenticated":false,"state_store_count":0}
//   enabled + anonymous  {"enabled":true,"authenticated":false,"state_store_count":N}
//   enabled + signed in  {"enabled":true,"authenticated":true,"user":{...},"state_store_count":N}
// `user.preferred_username` DOES NOT EXIST — the FE renders
// `user.name || user.email || user.sub` (AR-33/AR-62).
import { useEffect, useState } from 'preact/hooks';
import { resetRedirectStormBreaker } from '../utils/redirectStorm';

export interface AuthUser {
  sub: string;
  email?: string;
  name?: string;
}

export interface AuthMeResponse {
  enabled: boolean;
  authenticated: boolean;
  user?: AuthUser;
  state_store_count: number;
}

export interface UseAuthState {
  enabled: boolean;
  authenticated: boolean;
  user: AuthUser | null;
  stateStoreCount: number;
  loading: boolean;
  error: string | null;
}

const AUTH_ME_PATH = '/fe/api/auth/me';
export const AUTH_PROBE_TIMEOUT_MS = 3000;

// The shared probe promise. Explicit answers are cached; UNKNOWN
// outcomes (timeout / network failure) null it so the next probe
// re-fetches (AR-16).
let authMePromise: Promise<AuthMeResponse | null> | null = null;
// The explicitly-resolved enabled flag. null means UNKNOWN — never a
// timeout. This is the gate behind isEnabledResolved() (B2).
let resolvedEnabled: boolean | null = null;

async function probeAuthMe(): Promise<AuthMeResponse | null> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), AUTH_PROBE_TIMEOUT_MS);
  try {
    // Deliberately NOT guardedFetch — the probe must not recurse into
    // the guard it feeds (plan §7.5). The endpoint is gate-exempt, so
    // this request never triggers a redirect.
    const res = await fetch(AUTH_ME_PATH, {
      signal: controller.signal,
      headers: { 'Content-Type': 'application/json' },
    });
    // 200 -> an explicit answer in BOTH dormant and enabled+session
    // shapes. Parse and return it verbatim.
    if (res.status === 200) {
      const body = (await res.json()) as AuthMeResponse;
      return body;
    }
    // 401 -> the enabled+anonymous shape (plan §5.8): the body carries
    // enabled:true. Attempt to parse; if it carries a boolean `enabled`,
    // return it as an EXPLICIT answer (this is how the FE learns that
    // internal mode is active). If the body is missing, not JSON, or
    // has no boolean `enabled` (e.g. an EXTERNAL ingress 401 with an
    // HTML body), return null = UNKNOWN — never {enabled:false}, and
    // never cache a false (AR-16): the next probe re-fetches.
    if (res.status === 401) {
      try {
        const body = (await res.json()) as { enabled?: unknown };
        if (body && typeof body === 'object' && typeof body.enabled === 'boolean') {
          return body as AuthMeResponse;
        }
      } catch {
        // unparseable body — fall through to UNKNOWN
      }
      return null;
    }
    // Any other non-ok status (404 from an older binary, 5xx) is an
    // EXPLICIT answer that the internal mode is not in charge:
    // enabled=false, never a redirect target. The probe itself never
    // redirects (the guard exempts it).
    return { enabled: false, authenticated: false, state_store_count: 0 };
  } catch {
    // Abort (timeout) or network failure: the state stays UNKNOWN
    // (AR-16) — null is never cached as an answer and never renders
    // "signed out" as a truth. Only an explicit answer is cached.
    return null;
  } finally {
    clearTimeout(timer);
  }
}

function startProbe(): Promise<AuthMeResponse | null> {
  return probeAuthMe().then((res) => {
    if (res !== null) {
      resolvedEnabled = res.enabled;
      // S10: a legitimately completed redirect cycle clears the storm
      // breaker — an authenticated probe means the login flow SUCCEEDED,
      // so the per-tab hop counter starts fresh for any future session
      // expiry. Dormant / anonymous / UNKNOWN answers never touch it.
      if (res.authenticated === true) resetRedirectStormBreaker();
    } else {
      // AR-16: a timeout/failure is never cached — the next probe
      // (e.g. the next 401-driven isEnabled()) re-fetches.
      authMePromise = null;
    }
    return res;
  });
}

export const authState = {
  /** The shared one-shot probe; explicit answers cached, UNKNOWN not (AR-16). */
  probe(): Promise<AuthMeResponse | null> {
    if (authMePromise === null) authMePromise = startProbe();
    return authMePromise;
  },
  /**
   * Resolves the enabled flag with a bounded probe. A timeout resolves
   * false WITHOUT caching it (AR-16) — the next 401 re-probes.
   */
  async isEnabled(): Promise<boolean> {
    const res = await authState.probe();
    return res !== null && res.enabled;
  },
  /**
   * B2: true ONLY when the probe has resolved to enabled:true (an
   * explicit answer, never a timeout). This is the gate on the edge-world
   * reloadOnce() — before resolution, and in dormant mode, it is false.
   */
  isEnabledResolved(): boolean {
    return resolvedEnabled === true;
  },
  /** Publish an explicit mode answer (called by the hook once the probe resolves). */
  setEnabled(enabled: boolean): void {
    resolvedEnabled = enabled;
  },
  /**
   * S10: clear the redirect-storm breaker (counter + banner) — also
   * reachable directly by consumers/tests that complete a login cycle.
   */
  clearRedirectHopCounter(): void {
    resetRedirectStormBreaker();
  },
  /** Test hook — resets the module store to the never-probed state. */
  resetForTests(): void {
    authMePromise = null;
    resolvedEnabled = null;
    // S10: test isolation — a fresh module store must not inherit the
    // previous case's hop count (sessionStorage outlives the module
    // state inside one test run, which is exactly the cross-page-load
    // property the breaker relies on).
    resetRedirectStormBreaker();
  },
};

const INITIAL: UseAuthState = {
  enabled: false,
  authenticated: false,
  user: null,
  stateStoreCount: 0,
  loading: true,
  error: null,
};

/**
 * One-shot identity probe. Mounts once (App top level + Header) and
 * shares the module probe — the request happens exactly once per page
 * load regardless of how many components call the hook.
 */
export function useAuth(): UseAuthState {
  const [state, setState] = useState<UseAuthState>(INITIAL);

  useEffect(() => {
    let cancelled = false;
    authState.probe().then((res) => {
      if (cancelled) return;
      if (res === null) {
        // AR-16: a timed-out / failed probe leaves the state UNKNOWN —
        // rendered as not-signed-in (badge hidden, never "signed out"
        // as a truth), with the error field carrying the reason.
        setState({ ...INITIAL, loading: false, error: 'auth probe timed out or failed' });
        return;
      }
      authState.setEnabled(res.enabled);
      setState({
        enabled: res.enabled,
        authenticated: res.authenticated,
        user: res.user ?? null,
        stateStoreCount: res.state_store_count,
        loading: false,
        error: null,
      });
    });
    return () => {
      cancelled = true;
    };
  }, []);

  return state;
}
