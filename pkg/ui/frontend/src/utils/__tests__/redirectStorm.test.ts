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
// F3 (reviewer rider): the page loads are REAL — `vi.resetModules()` +
// a re-import gives a fresh module state (the AR-42 latch, the one-shot
// reload latch and the probe cache are per-page-load) while the TAB
// storage survives. Every phase asserts the sessionStorage state
// explicitly, so the cross-page-load property is genuinely exercised:
// a counter that lived in the MODULE instead of sessionStorage would
// reset on every simulated load and fail these tests.
//
// The identity-probe responses come from the ONE canonical mock helper
// (./authProbeMocks — tester item R5).
//
// NOTE: channel-name spellings are deliberately avoided in this file —
// the AR-60 census must stay clean outside the guarded production
// modules.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  anonymousProbeResponse,
  authenticatedProbeResponse,
  dormantProbeResponse,
  installAuthWire,
  jsonResponse,
} from './authProbeMocks';

const realFetch = globalThis.fetch;

/**
 * Simulate a PAGE LOAD: a fresh module state (the AR-42 redirect latch,
 * the AR-43 one-shot reload latch and the identity-probe cache are all
 * per-page-load) while the TAB storage (sessionStorage) survives — the
 * exact reload semantics the breaker relies on.
 */
async function loadPage() {
  vi.resetModules();
  const apiFetch = await import('../apiFetch');
  const redirectStorm = await import('../redirectStorm');
  const useAuth = await import('../../hooks/useAuth');
  return { apiFetch, redirectStorm, authState: useAuth.authState };
}

/** The hop counter as it is stored in the tab (not the module view). */
function storedHopCount(key: string): number {
  const raw = sessionStorage.getItem(key);
  if (raw === null) return 0;
  const n = Number.parseInt(raw, 10);
  return Number.isNaN(n) ? 0 : n;
}

/**
 * A sessionStorage stand-in for a privacy-mode / storage-disabled tab:
 * every WRITE throws (and the reads answer "absent"). Installed with
 * vi.stubGlobal so the production module reads it through the global.
 */
function disabledStorage(): Storage {
  const boom = () => {
    throw new DOMException('storage disabled', 'SecurityError');
  };
  return {
    get length() {
      return 0;
    },
    key: () => null,
    getItem: () => null,
    setItem: boom,
    removeItem: boom,
    clear: () => {},
  } as unknown as Storage;
}

describe('S10 — the redirect-storm breaker', () => {
  let assignSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    vi.useFakeTimers();
    sessionStorage.clear(); // a fresh TAB per test case
    assignSpy = vi.spyOn(window.location, 'assign').mockImplementation(() => {});
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
    (globalThis as { fetch: typeof fetch }).fetch = realFetch;
    sessionStorage.clear();
  });

  it('3 hops across simulated page loads redirect; the 4th is suppressed and the error state is visible', async () => {
    installAuthWire({
      probe: () => anonymousProbeResponse(),
      target: () => jsonResponse({ error: 'unauthenticated' }, 401),
    });

    let page = await loadPage();
    const STORAGE_KEY = page.redirectStorm.REDIRECT_STORM_STORAGE_KEY;
    const THRESHOLD = page.redirectStorm.REDIRECT_STORM_THRESHOLD;

    for (let hop = 0; hop < THRESHOLD; hop++) {
      // A fresh page load: the module state is NEW (the AR-42 latch is
      // re-armed), the TAB storage is NOT.
      page = await loadPage();
      await page.apiFetch.guardedFetch('/fe/api/models'); // 401 -> enabled probe -> redirect initiation
      expect(assignSpy).toHaveBeenCalledTimes(hop + 1);
      expect(page.redirectStorm.getRedirectHopCount()).toBe(hop + 1);
      // The counter LIVES in sessionStorage — the tab carries it across
      // the page load.
      expect(sessionStorage.getItem(STORAGE_KEY)).toBe(String(hop + 1));
      expect(storedHopCount(STORAGE_KEY)).toBe(hop + 1);
    }

    expect(page.redirectStorm.isRedirectStormTripped()).toBe(true);
    expect(storedHopCount(STORAGE_KEY)).toBe(THRESHOLD);

    // The 4th page load: the breaker ABORTS the cycle.
    page = await loadPage();
    await page.apiFetch.guardedFetch('/fe/api/models');
    expect(assignSpy).toHaveBeenCalledTimes(THRESHOLD); // NOT incremented
    expect(page.redirectStorm.getRedirectHopCount()).toBe(THRESHOLD);
    expect(sessionStorage.getItem(STORAGE_KEY)).toBe(String(THRESHOLD)); // untouched
    // ...and the operator sees the visible error state.
    expect(page.redirectStorm.redirectStormErrorVisible()).toBe(true);
    expect(document.querySelector('[role="alert"]')).not.toBeNull();
    expect(document.body.textContent).toContain('redirect loop');
  });

  it('the error state is dismissible (the Dismiss button removes the banner)', async () => {
    installAuthWire({
      probe: () => anonymousProbeResponse(),
      target: () => jsonResponse({ error: 'unauthenticated' }, 401),
    });
    let page = await loadPage();
    const THRESHOLD = page.redirectStorm.REDIRECT_STORM_THRESHOLD;

    for (let hop = 0; hop < THRESHOLD; hop++) {
      page = await loadPage();
      await page.apiFetch.guardedFetch('/fe/api/models');
    }
    page = await loadPage();
    await page.apiFetch.guardedFetch('/fe/api/models');
    expect(page.redirectStorm.redirectStormErrorVisible()).toBe(true);

    const banner = document.querySelector('[data-testid="redirect-storm-breaker"]');
    expect(banner).not.toBeNull();
    const dismiss = banner!.querySelector('button');
    expect(dismiss).not.toBeNull();
    (dismiss as HTMLButtonElement).click();
    expect(page.redirectStorm.redirectStormErrorVisible()).toBe(false);
    // The breaker itself stays tripped: the next attempt is still aborted.
    page = await loadPage();
    await page.apiFetch.guardedFetch('/fe/api/models');
    expect(assignSpy).toHaveBeenCalledTimes(THRESHOLD);
  });

  it('a successful authenticated probe clears the counter — a later 401 redirects again normally', async () => {
    installAuthWire({
      probe: () => anonymousProbeResponse(),
      target: () => jsonResponse({ error: 'unauthenticated' }, 401),
    });
    let page = await loadPage();
    const STORAGE_KEY = page.redirectStorm.REDIRECT_STORM_STORAGE_KEY;
    const THRESHOLD = page.redirectStorm.REDIRECT_STORM_THRESHOLD;

    for (let hop = 0; hop < THRESHOLD; hop++) {
      page = await loadPage();
      await page.apiFetch.guardedFetch('/fe/api/models');
    }
    expect(page.redirectStorm.isRedirectStormTripped()).toBe(true);

    // The redirect cycle legitimately completes: a FRESH page load (new
    // module state — the probe cache resets) still carries the hop
    // counter in sessionStorage, and the probe on that load resolves
    // authenticated:true (a 200 with a session).
    page = await loadPage();
    expect(page.redirectStorm.getRedirectHopCount()).toBe(THRESHOLD); // the counter survived the load
    expect(sessionStorage.getItem(STORAGE_KEY)).toBe(String(THRESHOLD));
    installAuthWire({ probe: () => authenticatedProbeResponse() });
    const probeResult = await page.authState.probe();
    expect(probeResult?.authenticated).toBe(true);
    expect(page.redirectStorm.getRedirectHopCount()).toBe(0);
    expect(sessionStorage.getItem(STORAGE_KEY)).toBeNull();
    expect(page.redirectStorm.isRedirectStormTripped()).toBe(false);

    // A LATER 401 redirects again normally (fresh cycle, hop 1).
    installAuthWire({
      probe: () => anonymousProbeResponse(),
      target: () => jsonResponse({ error: 'unauthenticated' }, 401),
    });
    page = await loadPage();
    await page.apiFetch.guardedFetch('/fe/api/models');
    expect(assignSpy).toHaveBeenCalledTimes(THRESHOLD + 1);
    expect(page.redirectStorm.getRedirectHopCount()).toBe(1);
    expect(sessionStorage.getItem(STORAGE_KEY)).toBe('1');
  });

  it('dormant mode never arms the breaker — the counter stays untouched and no banner appears', async () => {
    installAuthWire({
      probe: () => dormantProbeResponse(),
      target: () => jsonResponse({ error: 'unauthenticated' }, 401),
    });
    const page = await loadPage();
    const STORAGE_KEY = page.redirectStorm.REDIRECT_STORM_STORAGE_KEY;

    await page.apiFetch.guardedFetch('/fe/api/models'); // dormant 401: no redirect, no counter
    expect(assignSpy).not.toHaveBeenCalled();
    expect(page.redirectStorm.getRedirectHopCount()).toBe(0);
    expect(sessionStorage.getItem(STORAGE_KEY)).toBeNull();
    expect(page.redirectStorm.redirectStormErrorVisible()).toBe(false);

    // Even with a STALE count carried in the tab, the dormant path never
    // trips: no redirect attempt is made, so no banner is surfaced and
    // the count is never advanced by dormant traffic.
    for (let i = 0; i < page.redirectStorm.REDIRECT_STORM_THRESHOLD; i++) {
      page.redirectStorm.incrementRedirectHopCount();
    }
    expect(page.redirectStorm.isRedirectStormTripped()).toBe(true);
    expect(sessionStorage.getItem(STORAGE_KEY)).toBe(String(page.redirectStorm.REDIRECT_STORM_THRESHOLD));
    await page.apiFetch.guardedFetch('/fe/api/models');
    expect(assignSpy).not.toHaveBeenCalled();
    expect(page.redirectStorm.getRedirectHopCount()).toBe(page.redirectStorm.REDIRECT_STORM_THRESHOLD); // untouched by dormant mode
    expect(sessionStorage.getItem(STORAGE_KEY)).toBe(String(page.redirectStorm.REDIRECT_STORM_THRESHOLD));
    expect(page.redirectStorm.redirectStormErrorVisible()).toBe(false);
  });

  it('the per-page-load AR-42 latch is untouched: two attempts inside one page load count ONE hop', async () => {
    installAuthWire({
      probe: () => anonymousProbeResponse(),
      target: () => jsonResponse({ error: 'unauthenticated' }, 401),
    });
    const page = await loadPage();
    const STORAGE_KEY = page.redirectStorm.REDIRECT_STORM_STORAGE_KEY;

    await page.apiFetch.guardedFetch('/fe/api/models');
    await page.apiFetch.guardedFetch('/fe/api/models'); // blocked by the 8 s cooldown — no hop
    expect(assignSpy).toHaveBeenCalledTimes(1);
    expect(page.redirectStorm.getRedirectHopCount()).toBe(1);
    expect(sessionStorage.getItem(STORAGE_KEY)).toBe('1');
    expect(page.redirectStorm.isRedirectStormTripped()).toBe(false);
  });

  it('#11a: a storage-disabled tab degrades to a NO-OP breaker — the write never throws and the login path survives', async () => {
    installAuthWire({
      probe: () => anonymousProbeResponse(),
      target: () => jsonResponse({ error: 'unauthenticated' }, 401),
    });
    const page = await loadPage();
    const STORAGE_KEY = page.redirectStorm.REDIRECT_STORM_STORAGE_KEY;

    // A privacy-mode / storage-disabled tab: every sessionStorage write
    // THROWS. The breaker must degrade (no counter, no throw) — an
    // unguarded setItem would break the login path itself.
    vi.stubGlobal('sessionStorage', disabledStorage());
    try {
      expect(() => page.redirectStorm.incrementRedirectHopCount()).not.toThrow();
      expect(page.redirectStorm.getRedirectHopCount()).toBe(0);
      expect(sessionStorage.getItem(STORAGE_KEY)).toBeNull();

      // The guarded 401 path still completes: the redirect is initiated
      // (the counter simply does not advance).
      await expect(page.apiFetch.guardedFetch('/fe/api/models')).resolves.toBeDefined();
      expect(assignSpy).toHaveBeenCalledTimes(1);
      expect(page.redirectStorm.getRedirectHopCount()).toBe(0);

      // The reset path degrades the same way (a throwing removeItem must
      // never break the "the login succeeded" path).
      expect(() => page.redirectStorm.resetRedirectStormBreaker()).not.toThrow();
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
