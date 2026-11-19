// useAuth — the one-shot identity probe + the module-level auth-state
// store (internal-oauth2 phase 1, plan §7.2/§7.3 — AR-16 / B2).
//
// The hook probes GET /fe/api/auth/me ONCE per page load and shares the
// result with every consumer: the module-level `authState` store holds
// the in-flight / resolved probe so non-hook code (the fetch guard in
// utils/apiFetch) can read the resolved mode without prop-drilling.
//
// The probe BODY contract, the endpoint path, the timeout budget and the
// 401-body interpretation live in utils/authProbe — the ONE shared probe
// module (tidier item #6). The SSE probe (hooks/useEvents) parses
// through the same helper, so the two probes cannot drift apart.
//
// Backend contract (plan §5.8, pinned — do NOT change here):
//   dormant              {"enabled":false,"authenticated":false,"state_store_count":0}
//   enabled + anonymous  {"enabled":true,"authenticated":false,"state_store_count":N}
//   enabled + signed in  {"enabled":true,"authenticated":true,"user":{...},"state_store_count":N}
// `user.preferred_username` DOES NOT EXIST — the FE renders
// `user.name || user.email || user.sub` (AR-33/AR-62).
import { useEffect, useState } from 'preact/hooks';
import { resetRedirectStormBreaker } from '../utils/redirectStorm';
import { AUTH_ME_PATH, AUTH_PROBE_TIMEOUT_MS, parseAuthProbeResponse } from '../utils/authProbe';
import type { AuthProbeBody as AuthMeResponse, AuthProbeUser as AuthUser } from '../utils/authProbe';

// The canonical shapes live in utils/authProbe; they are re-published
// here under their historical names so no consumer has to change.
export type { AuthMeResponse, AuthUser };

export interface UseAuthState {
  enabled: boolean;
  authenticated: boolean;
  user: AuthUser | null;
  stateStoreCount: number;
  loading: boolean;
  error: string | null;
}

// The shared probe promise. Explicit answers are cached; UNKNOWN
// outcomes (timeout / network failure / rejection) null it so the next
// probe re-fetches (AR-16).
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
    // The ONE shared body interpretation (utils/authProbe):
    //   - 200 -> the dormant / signed-in shape;
    //   - 401 -> the enabled+anonymous shape (the body carries enabled:true);
    //   - any other status -> the internal mode is NOT in charge
    //     (a 404 from an older binary, a 5xx): an EXPLICIT answer that
    //     is never a redirect target;
    //   - an unparseable body, or one without a boolean `enabled` ->
    //     UNKNOWN (null), never {enabled:false} (AR-16).
    const outcome = await parseAuthProbeResponse(res);
    if (outcome.kind === 'answer') return outcome.body;
    if (outcome.kind === 'not-in-charge') {
      return { enabled: false, authenticated: false, state_store_count: 0 };
    }
    return null;
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
  return probeAuthMe()
    .then((res) => {
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
    })
    .catch(() => {
      // #11b: the shared probe promise NEVER rejects — a rejection is
      // UNKNOWN (AR-16): not cached, and never an unhandled rejection.
      authMePromise = null;
      return null;
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
    // #11b: the probe rejection is handled EXPLICITLY — a rejected
    // probe follows the UNKNOWN path (the same state a timeout
    // produces), and never escapes as an unhandled rejection.
    const applyUnknown = () => {
      if (cancelled) return;
      // AR-16: a timed-out / failed probe leaves the state UNKNOWN —
      // rendered as not-signed-in (badge hidden, never "signed out"
      // as a truth), with the error field carrying the reason.
      setState({ ...INITIAL, loading: false, error: 'auth probe timed out or failed' });
    };
    authState.probe().then(
      (res) => {
        if (cancelled) return;
        if (res === null) {
          applyUnknown();
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
      },
      () => {
        // A rejected probe — the UNKNOWN path (see above).
        applyUnknown();
      },
    );
    return () => {
      cancelled = true;
    };
  }, []);

  return state;
}
