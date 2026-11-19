// internal-oauth2 phase 1 (plan §7.2 / §9.3) — the identity probe hook.
//
// Pins: one probe per page load regardless of mounts; the §5.8 shape
// mapping; a 401 probe reporting not-authenticated WITHOUT redirecting
// (the endpoint is gate-exempt); and AR-16 — a timed-out probe leaves
// the state UNKNOWN (never a cached "signed out"), so the next probe
// re-fetches.
//
// NOTE: channel-name spellings are deliberately avoided in this file —
// the AR-60 census must stay clean outside the guarded modules.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act, cleanup } from '@testing-library/preact';
import { useAuth, authState } from '../useAuth';

const realFetch = globalThis.fetch;

function jsonResponse(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function hangUntilAborted(init?: RequestInit): Promise<Response> {
  return new Promise<Response>((_resolve, reject) => {
    const signal = init?.signal;
    if (signal) {
      signal.addEventListener('abort', () =>
        reject(new DOMException('The operation was aborted.', 'AbortError')),
      );
    }
  });
}

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

  function installWire(handlers: { probe: (init?: RequestInit) => Response | Promise<Response> }) {
    const wire = vi.fn(async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      const url = typeof input === 'string' ? input : input.toString();
      if (url.includes('/fe/api/auth/me')) return handlers.probe(init);
      throw new Error(`unexpected request: ${url}`);
    });
    (globalThis as { fetch: typeof fetch }).fetch = wire as unknown as typeof fetch;
    return wire;
  }

  it('probes exactly once across multiple mounts (module-level shared probe)', async () => {
    const wire = installWire({
      probe: () => jsonResponse({ enabled: true, authenticated: true, user: { sub: 's1' }, state_store_count: 3 }),
    });

    const first = renderHook(() => useAuth());
    const second = renderHook(() => useAuth());
    await act(drain);

    expect(wire).toHaveBeenCalledTimes(1);
    const calls = wire.mock.calls.filter(([u]) => String(u).includes('/fe/api/auth/me'));
    expect(calls.length).toBe(1);
    expect(first.result.current.loading).toBe(false);
    expect(second.result.current.loading).toBe(false);
    first.unmount();
    second.unmount();
  });

  it('maps the §5.8 contract shape (enabled/authenticated/user/state_store_count)', async () => {
    installWire({
      probe: () =>
        jsonResponse({
          enabled: true,
          authenticated: true,
          user: { sub: 'sub-1', email: 'op@example.com', name: 'Operator' },
          state_store_count: 7,
        }),
    });

    const { result } = renderHook(() => useAuth());
    expect(result.current.loading).toBe(true);
    await act(drain);

    expect(result.current).toEqual({
      enabled: true,
      authenticated: true,
      user: { sub: 'sub-1', email: 'op@example.com', name: 'Operator' },
      stateStoreCount: 7,
      loading: false,
      error: null,
    });
  });

  it('a 401 probe with the real body reports enabled:true (T4)', async () => {
    installWire({
      probe: () =>
        jsonResponse({ error: 'unauthenticated', enabled: true, authenticated: false, user: null, state_store_count: 3 }, 401),
    });

    const { result } = renderHook(() => useAuth());
    await act(drain);

    expect(result.current.enabled).toBe(true);
    expect(result.current.authenticated).toBe(false);
    expect(result.current.user).toBeNull();
    expect(result.current.stateStoreCount).toBe(3);
    expect(result.current.loading).toBe(false);
    expect(result.current.error).toBeNull();
    expect(assignSpy).not.toHaveBeenCalled();
  });

  it('a 401 probe with an unparseable body leaves state UNKNOWN (T5, AR-16)', async () => {
    const wire = installWire({
      probe: () => new Response('not json', { status: 401, headers: { 'Content-Type': 'text/html' } }),
    });

    const { result } = renderHook(() => useAuth());
    await act(drain);

    expect(result.current.loading).toBe(false);
    expect(result.current.enabled).toBe(false);
    expect(result.current.error).not.toBeNull();
    expect(authState.isEnabledResolved()).toBe(false);

    wire.mockImplementation(async () =>
      jsonResponse({ enabled: true, authenticated: false, state_store_count: 1 }),
    );
    await act(async () => {
      await authState.probe();
    });
    const probeCalls = wire.mock.calls.filter(([u]) => String(u).includes('/fe/api/auth/me')).length;
    expect(probeCalls).toBe(2);
  });

  it('an anonymous probe reports authenticated:false with enabled preserved', async () => {
    installWire({ probe: () => jsonResponse({ enabled: true, authenticated: false, state_store_count: 2 }) });

    const { result } = renderHook(() => useAuth());
    await act(drain);

    expect(result.current.enabled).toBe(true);
    expect(result.current.authenticated).toBe(false);
    expect(result.current.user).toBeNull();
    expect(result.current.stateStoreCount).toBe(2);
  });

  it('a probe TIMEOUT leaves the state UNKNOWN — never a cached false (AR-16)', async () => {
    const wire = installWire({ probe: (init) => hangUntilAborted(init) });

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
    wire.mockImplementation(async (): Promise<Response> =>
      jsonResponse({ enabled: true, authenticated: false, state_store_count: 1 }),
    );
    await act(async () => {
      await authState.probe();
    });
    const probeCalls = wire.mock.calls.filter(([u]) => String(u).includes('/fe/api/auth/me')).length;
    expect(probeCalls).toBe(2);
  });
});
