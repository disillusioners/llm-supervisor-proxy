// S10 — the redirect-storm breaker tests (internal-oauth2 phase 1,
// review item S10).
//
// Pins the bounded per-tab breaker on top of the AR-42 latch:
//   - 3 consecutive redirect initiations across SIMULATED PAGE LOADS
//     (the module latch is re-armed between hops — a fresh page load
//     creates a new module state — while the sessionStorage counter
//     persists) => the 4th initiation is ABORTED and the visible,
//     dismissible error state is on screen;
//   - a successful auth probe resolving authenticated:true clears the
//     counter (a later 401 redirects again normally);
//   - dormant mode never arms the breaker (the counter stays untouched
//     and no banner appears even when the tab carries a stale count).
//
// NOTE: channel-name spellings are deliberately avoided in this file —
// the AR-60 census must stay clean outside the guarded production
// modules.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  guardedFetch,
  resetAuthRedirectLatch,
  resetReloadOnce,
  getRedirectHopCount,
  incrementRedirectHopCount,
  isRedirectStormTripped,
  redirectStormErrorVisible,
  REDIRECT_STORM_THRESHOLD,
} from '../apiFetch';
import { authState } from '../../hooks/useAuth';

const realFetch = globalThis.fetch;

function jsonResponse(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const probeEnabledTrue = () => jsonResponse({ enabled: true, authenticated: false, state_store_count: 1 });
const probeDormant = () => jsonResponse({ enabled: false, authenticated: false, state_store_count: 0 });
const probeAuthenticated = () =>
  jsonResponse({ enabled: true, authenticated: true, user: { sub: 'op-1' }, state_store_count: 2 });

describe('S10 — the redirect-storm breaker', () => {
  let assignSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    vi.useFakeTimers();
    resetAuthRedirectLatch();
    resetReloadOnce();
    authState.resetForTests(); // also clears the breaker counter
    assignSpy = vi.spyOn(window.location, 'assign').mockImplementation(() => {});
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
    (globalThis as { fetch: typeof fetch }).fetch = realFetch;
    authState.resetForTests();
  });

  /** Install a wire stub answering by URL; returns the mock for call assertions. */
  function installWire(handlers: {
    probe?: () => Response;
    target?: () => Response;
  }) {
    const wire = vi.fn(async (input: RequestInfo | URL): Promise<Response> => {
      const url = typeof input === 'string' ? input : input.toString();
      if (url.includes('/fe/api/auth/me')) {
        if (!handlers.probe) throw new Error('unexpected probe request');
        return handlers.probe();
      }
      if (!handlers.target) throw new Error('unexpected target request');
      return handlers.target();
    });
    (globalThis as { fetch: typeof fetch }).fetch = wire as unknown as typeof fetch;
    return wire;
  }

  it('3 hops across simulated page loads redirect; the 4th is suppressed and the error state is visible', async () => {
    installWire({ probe: () => probeEnabledTrue(), target: () => jsonResponse({ error: 'unauthenticated' }, 401) });

    for (let hop = 0; hop < REDIRECT_STORM_THRESHOLD; hop++) {
      resetAuthRedirectLatch(); // a fresh page load: the module latch re-arms
      await guardedFetch('/fe/api/models'); // 401 -> enabled probe -> redirect initiation
      expect(assignSpy).toHaveBeenCalledTimes(hop + 1);
      expect(getRedirectHopCount()).toBe(hop + 1);
    }

    expect(isRedirectStormTripped()).toBe(true);

    // The 4th page load: the breaker ABORTS the cycle.
    resetAuthRedirectLatch();
    await guardedFetch('/fe/api/models');
    expect(assignSpy).toHaveBeenCalledTimes(REDIRECT_STORM_THRESHOLD); // NOT incremented
    expect(getRedirectHopCount()).toBe(REDIRECT_STORM_THRESHOLD);
    // ...and the operator sees the visible error state.
    expect(redirectStormErrorVisible()).toBe(true);
    expect(document.querySelector('[role="alert"]')).not.toBeNull();
    expect(document.body.textContent).toContain('redirect loop');
  });

  it('the error state is dismissible (the Dismiss button removes the banner)', async () => {
    installWire({ probe: () => probeEnabledTrue(), target: () => jsonResponse({ error: 'unauthenticated' }, 401) });
    for (let hop = 0; hop < REDIRECT_STORM_THRESHOLD; hop++) {
      resetAuthRedirectLatch();
      await guardedFetch('/fe/api/models');
    }
    resetAuthRedirectLatch();
    await guardedFetch('/fe/api/models');
    expect(redirectStormErrorVisible()).toBe(true);

    const banner = document.querySelector('[data-testid="redirect-storm-breaker"]');
    expect(banner).not.toBeNull();
    const dismiss = banner!.querySelector('button');
    expect(dismiss).not.toBeNull();
    (dismiss as HTMLButtonElement).click();
    expect(redirectStormErrorVisible()).toBe(false);
    // The breaker itself stays tripped: the next attempt is still aborted.
    resetAuthRedirectLatch();
    await guardedFetch('/fe/api/models');
    expect(assignSpy).toHaveBeenCalledTimes(REDIRECT_STORM_THRESHOLD);
  });

  it('a successful authenticated probe clears the counter — a later 401 redirects again normally', async () => {
    installWire({ probe: () => probeEnabledTrue(), target: () => jsonResponse({ error: 'unauthenticated' }, 401) });
    for (let hop = 0; hop < REDIRECT_STORM_THRESHOLD; hop++) {
      resetAuthRedirectLatch();
      await guardedFetch('/fe/api/models');
    }
    expect(isRedirectStormTripped()).toBe(true);

    // The redirect cycle legitimately completes: a FRESH page load (new
    // module state — the probe cache resets) still carries the hop
    // counter in sessionStorage, and the probe on that load resolves
    // authenticated:true (a 200 with a session).
    authState.resetForTests(); // new page load: fresh probe cache
    for (let i = 0; i < REDIRECT_STORM_THRESHOLD; i++) incrementRedirectHopCount(); // the counter survived the load
    expect(isRedirectStormTripped()).toBe(true);
    installWire({ probe: () => probeAuthenticated() });
    const probeResult = await authState.probe();
    expect(probeResult?.authenticated).toBe(true);
    expect(getRedirectHopCount()).toBe(0);
    expect(isRedirectStormTripped()).toBe(false);

    // A LATER 401 redirects again normally (fresh cycle, hop 1).
    installWire({ probe: () => probeEnabledTrue(), target: () => jsonResponse({ error: 'unauthenticated' }, 401) });
    resetAuthRedirectLatch();
    await guardedFetch('/fe/api/models');
    expect(assignSpy).toHaveBeenCalledTimes(REDIRECT_STORM_THRESHOLD + 1);
    expect(getRedirectHopCount()).toBe(1);
  });

  it('dormant mode never arms the breaker — the counter stays untouched and no banner appears', async () => {
    installWire({ probe: () => probeDormant(), target: () => jsonResponse({ error: 'unauthenticated' }, 401) });

    await guardedFetch('/fe/api/models'); // dormant 401: no redirect, no counter
    expect(assignSpy).not.toHaveBeenCalled();
    expect(getRedirectHopCount()).toBe(0);
    expect(redirectStormErrorVisible()).toBe(false);

    // Even with a STALE count carried in the tab, the dormant path never
    // trips: no redirect attempt is made, so no banner is surfaced and
    // the count is never advanced by dormant traffic.
    for (let i = 0; i < REDIRECT_STORM_THRESHOLD; i++) incrementRedirectHopCount();
    expect(isRedirectStormTripped()).toBe(true);
    await guardedFetch('/fe/api/models');
    expect(assignSpy).not.toHaveBeenCalled();
    expect(getRedirectHopCount()).toBe(REDIRECT_STORM_THRESHOLD); // untouched by dormant mode
    expect(redirectStormErrorVisible()).toBe(false);
  });

  it('the per-page-load AR-42 latch is untouched: two attempts inside one page load count ONE hop', async () => {
    installWire({ probe: () => probeEnabledTrue(), target: () => jsonResponse({ error: 'unauthenticated' }, 401) });

    await guardedFetch('/fe/api/models');
    await guardedFetch('/fe/api/models'); // blocked by the 8 s cooldown — no hop
    expect(assignSpy).toHaveBeenCalledTimes(1);
    expect(getRedirectHopCount()).toBe(1);
    expect(isRedirectStormTripped()).toBe(false);
  });
});
