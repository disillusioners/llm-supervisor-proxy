// internal-oauth2 phase 1 (plan §7.2 / §9.3) — the identity probe hook.
//
// Pins: one probe per page load regardless of mounts; the §5.8 shape
// mapping; a 401 probe reporting not-authenticated WITHOUT redirecting
// (the endpoint is gate-exempt); and AR-16 — a timed-out probe leaves
// the state UNKNOWN (never a cached "signed out"), so the next probe
// re-fetches.
//
// The identity-probe responses come from the ONE canonical mock helper
// (utils/__tests__/authProbeMocks — tester item R5): the
// enabled+anonymous shape is answered with HTTP 401 + a body carrying
// `enabled:true` (the backend contract, plan §5.8), never with a 200.
//
// NOTE: channel-name spellings are deliberately avoided in this file —
// the AR-60 census must stay clean outside the guarded modules.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act, cleanup } from '@testing-library/preact';
import { useAuth, authState } from '../useAuth';
import {
  anonymousProbeResponse,
  authenticatedProbeResponse,
  hangingProbeResponse,
  installAuthWire,
  probeCallCount,
  unparseableProbeResponse,
} from '../../utils/__tests__/authProbeMocks';

const realFetch = globalThis.fetch;

/**
 * Drain the probe's promise chain: fetch wire -> body parse -> then ->
 * setState. Plain `await act(async () => {})` does not pump enough
 * microtask hops for the whole chain, so tests pump explicitly. Native
 * promises are unaffected by the fake clock.
 */
async function drain(): Promise<void> {
  for (let i = 0; i < 30; i++) await Promise.resolve();
}

describe('useAuth — the one-shot identity probe', () => {
  let assignSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    vi.useFakeTimers();
    authState.resetForTests();
    assignSpy = vi.spyOn(window.location, 'assign').mockImplementation(() => {});
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.useRealTimers();
    (globalThis as { fetch: typeof fetch }).fetch = realFetch;
    authState.resetForTests();
  });

  it('probes exactly once across multiple mounts (module-level shared probe)', async () => {
    const wire = installAuthWire({ probe: () => authenticatedProbeResponse() });

    const first = renderHook(() => useAuth());
    const second = renderHook(() => useAuth());
    await act(drain);

    expect(wire).toHaveBeenCalledTimes(1);
    expect(probeCallCount(wire)).toBe(1);
    expect(first.result.current.loading).toBe(false);
    expect(second.result.current.loading).toBe(false);
    first.unmount();
    second.unmount();
  });

  it('maps the §5.8 contract shape (enabled/authenticated/user/state_store_count)', async () => {
    installAuthWire({ probe: () => authenticatedProbeResponse() });

    const { result } = renderHook(() => useAuth());
    expect(result.current.loading).toBe(true);
    await act(drain);

    expect(result.current).toEqual({
      enabled: true,
      authenticated: true,
      user: { sub: 'sub-1', email: 'op@example.com', name: 'Operator' },
      stateStoreCount: 2,
      loading: false,
      error: null,
    });
  });

  it('a 401 probe with the real body reports enabled:true (T4)', async () => {
    installAuthWire({ probe: () => anonymousProbeResponse() });

    const { result } = renderHook(() => useAuth());
    await act(drain);

    expect(result.current.enabled).toBe(true);
    expect(result.current.authenticated).toBe(false);
    expect(result.current.user).toBeNull();
    expect(result.current.stateStoreCount).toBe(1);
    expect(result.current.loading).toBe(false);
    expect(result.current.error).toBeNull();
    expect(assignSpy).not.toHaveBeenCalled();
  });

  it('a 401 probe with an unparseable body leaves state UNKNOWN (T5, AR-16)', async () => {
    const wire = installAuthWire({ probe: () => unparseableProbeResponse() });

    const { result } = renderHook(() => useAuth());
    await act(drain);

    expect(result.current.loading).toBe(false);
    expect(result.current.enabled).toBe(false);
    expect(result.current.error).not.toBeNull();
    expect(authState.isEnabledResolved()).toBe(false);

    wire.mockImplementation(async () => anonymousProbeResponse());
    await act(async () => {
      await authState.probe();
    });
    expect(probeCallCount(wire)).toBe(2);
  });

  it('an anonymous probe reports authenticated:false with enabled preserved', async () => {
    installAuthWire({ probe: () => anonymousProbeResponse() });

    const { result } = renderHook(() => useAuth());
    await act(drain);

    expect(result.current.enabled).toBe(true);
    expect(result.current.authenticated).toBe(false);
    expect(result.current.user).toBeNull();
    expect(result.current.stateStoreCount).toBe(1);
  });

  it('a probe TIMEOUT leaves the state UNKNOWN — never a cached false (AR-16)', async () => {
    const wire = installAuthWire({ probe: (init) => hangingProbeResponse(init) });

    const { result } = renderHook(() => useAuth());
    await act(async () => {
      vi.advanceTimersByTime(3000); // fires the probe's abort timer
      await drain();
    });

    // Render-safe: not signed in, not loading — but the error field
    // carries the UNKNOWN reason (never a cached "signed out" answer).
    expect(result.current.loading).toBe(false);
    expect(result.current.enabled).toBe(false);
    expect(result.current.authenticated).toBe(false);
    expect(result.current.error).not.toBeNull();
    // B2: an UNKNOWN probe is not an explicit enabled:true answer.
    expect(authState.isEnabledResolved()).toBe(false);

    // AR-16: the timeout was NOT cached — a fresh probe re-fetches.
    wire.mockImplementation(async (): Promise<Response> => anonymousProbeResponse());
    await act(async () => {
      await authState.probe();
    });
    expect(probeCallCount(wire)).toBe(2);
  });

  it('#11b: a REJECTED probe never escapes as an unhandled rejection — the state stays UNKNOWN', async () => {
    const unhandled: unknown[] = [];
    const onUnhandled = (event: Event) => unhandled.push(event);
    window.addEventListener('unhandledrejection', onUnhandled);
    // The shared probe promise rejects (a hard failure inside the probe
    // machinery). The hook handles the rejection EXPLICITLY: the state
    // follows the UNKNOWN path and nothing is thrown.
    vi.spyOn(authState, 'probe').mockRejectedValue(new Error('probe exploded'));

    try {
      const { result } = renderHook(() => useAuth());
      await act(drain);

      expect(result.current.loading).toBe(false);
      expect(result.current.enabled).toBe(false);
      expect(result.current.authenticated).toBe(false);
      expect(result.current.error).not.toBeNull();
      expect(authState.isEnabledResolved()).toBe(false);
      expect(unhandled).toEqual([]);
    } finally {
      window.removeEventListener('unhandledrejection', onUnhandled);
    }
  });
});
