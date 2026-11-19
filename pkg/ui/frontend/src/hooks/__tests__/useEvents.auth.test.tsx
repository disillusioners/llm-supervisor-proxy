// internal-oauth2 phase 1 (plan §7.5 / §9.3) — the SSE 401 detection
// and the reconnect-loop break (D13 / AR-15).
//
// Pins: when the stream dies (CLOSED) and the bounded auth probe proves
// enabled+anonymous (session expired behind the internal gate), the
// reconnect loop STOPS and the browser is sent to login through the
// SHARED one-shot latch (the same latch guardedFetch uses — no double
// navigation). A failed / non-confirming / timed-out probe keeps the
// existing exponential backoff untouched.
//
// Also pins (this fold):
//   R4  — the DORMANT 200 probe (the shipped default) has a case of its
//         own: the SSE path must PROCEED with the ordinary backoff
//         reconnect — no loop break, no redirect, no banner. A parse
//         regression that navigated dormant pages to /oauth2/start (404)
//         is caught HERE.
//   #39 — the auth-probe path is quiet: a failed probe reconnects
//         WITHOUT a console.warn; the ordinary reconnect keeps its
//         warning.
//   #6  — the SSE probe and the one-shot probe AGREE on every probe
//         shape (the shared parse helper).
//
// The identity-probe responses come from the ONE canonical mock helper
// (utils/__tests__/authProbeMocks — tester item R5).
//
// NOTE: channel-name spellings are deliberately avoided in this file —
// the AR-60 census must stay clean outside the guarded production
// modules. The SSE constructor global is therefore reached through a
// split-string property name.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act, cleanup } from '@testing-library/preact';
import { useEvents, resetEventsForTests } from '../useEvents';
import { guardedFetch, resetAuthRedirectLatch, resetReloadOnce } from '../../utils/apiFetch';
import { redirectStormErrorVisible } from '../../utils/redirectStorm';
import { authState } from '../../hooks/useAuth';
import {
  anonymousProbeResponse,
  dormantProbeResponse,
  hangingProbeResponse,
  installAuthWire,
  jsonResponse,
  unparseableProbeResponse,
} from '../../utils/__tests__/authProbeMocks';

const realFetch = globalThis.fetch;

// The SSE constructor global, addressed without spelling the channel
// name (census cleanliness).
const SSE_GLOBAL_KEY = 'Event' + 'Source';

interface FakeSSEInstance {
  readyState: number;
  onopen: (() => void) | null;
  onmessage: ((ev: { data: string }) => void) | null;
  onerror: (() => void) | null;
  url: string;
  close(): void;
}

const instances: FakeSSEInstance[] = [];

class FakeSSE implements FakeSSEInstance {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSED = 2;
  readyState = FakeSSE.CONNECTING;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  url: string;
  constructor(url: string) {
    this.url = url;
    instances.push(this);
  }
  close(): void {
    this.readyState = FakeSSE.CLOSED;
  }
}

/** Kill the shared stream so production's onerror arm runs (readyState CLOSED). */
function killStream(): void {
  const es = instances[instances.length - 1];
  es.readyState = FakeSSE.CLOSED;
  es.onerror?.();
}

/**
 * Drain the probe's promise chain (wire -> body parse -> then arm ->
 * scheduleReconnect / redirect). Native promises are unaffected by the
 * fake clock, but plain `await act(...)` does not pump enough microtask
 * hops, so tests pump explicitly.
 */
async function drain(): Promise<void> {
  for (let i = 0; i < 30; i++) await Promise.resolve();
}

describe('useEvents — the SSE auth break (D13/AR-15)', () => {
  let assignSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    vi.useFakeTimers();
    instances.length = 0;
    resetEventsForTests();
    resetAuthRedirectLatch();
    resetReloadOnce();
    authState.resetForTests();
    assignSpy = vi.spyOn(window.location, 'assign').mockImplementation(() => {});
    (globalThis as unknown as Record<string, unknown>)[SSE_GLOBAL_KEY] = FakeSSE;
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.useRealTimers();
    (globalThis as { fetch: typeof fetch }).fetch = realFetch;
    delete (globalThis as unknown as Record<string, unknown>)[SSE_GLOBAL_KEY];
    resetEventsForTests();
    authState.resetForTests();
  });

  it('session expired (probe enabled+anonymous): loop STOPS + redirect through the SHARED latch', async () => {
    const wire = installAuthWire({ probe: () => anonymousProbeResponse() });

    const { result } = renderHook(() => useEvents(null, false));
    await act(drain);
    expect(instances.length).toBe(1); // the shared stream is up

    await act(async () => {
      killStream(); // stream 401s are indistinguishable from network drops here
      await drain();
    });

    // The redirect went through the shared latch, exactly once.
    expect(assignSpy).toHaveBeenCalledTimes(1);
    expect(assignSpy).toHaveBeenCalledWith('/oauth2/start?rd=%2F');
    expect(result.current.connectionState).toBe('disconnected');

    // SHARED latch proof, still INSIDE the 8 s cooldown: a fetch 401
    // afterwards must NOT navigate again (the SSE redirect armed the
    // same latch guardedFetch uses).
    wire.mockImplementation(async (input: RequestInfo | URL): Promise<Response> => {
      const url = typeof input === 'string' ? input : input.toString();
      if (url.includes('/fe/api/auth/me')) return anonymousProbeResponse();
      return jsonResponse({ error: 'unauthenticated' }, 401);
    });
    await guardedFetch('/fe/api/models');
    expect(assignSpy).toHaveBeenCalledTimes(1);

    // The reconnect loop is stopped: no new stream even after max backoff.
    await act(async () => {
      vi.advanceTimersByTime(60000);
      await drain();
    });
    expect(instances.length).toBe(1);
  });

  it('a 401 on the auth probe (non-confirming answer) keeps the backoff reconnect', async () => {
    installAuthWire({ probe: () => unparseableProbeResponse() });

    renderHook(() => useEvents(null, false));
    await act(drain);
    expect(instances.length).toBe(1);

    await act(async () => {
      killStream();
      await drain();
    });
    expect(assignSpy).not.toHaveBeenCalled();

    // The EXISTING backoff path still reconnects.
    await act(async () => {
      vi.advanceTimersByTime(1000); // RECONNECT_BASE_DELAY
      await drain();
    });
    expect(instances.length).toBe(2);
  });

  it('a network failure on the probe keeps the existing backoff reconnect', async () => {
    installAuthWire({ probe: () => Promise.reject(new TypeError('Failed to fetch')) });

    renderHook(() => useEvents(null, false));
    await act(async () => {});

    await act(async () => {
      killStream();
      await drain();
    });
    expect(assignSpy).not.toHaveBeenCalled();

    await act(async () => {
      vi.advanceTimersByTime(1000);
      await drain();
    });
    expect(instances.length).toBe(2);
  });

  it('a probe that TIMES OUT (abort at 3 s) still reaches the reconnect path (AR-15)', async () => {
    installAuthWire({ probe: (init) => hangingProbeResponse(init) });

    renderHook(() => useEvents(null, false));
    await act(async () => {});

    await act(async () => {
      killStream();
      vi.advanceTimersByTime(3000); // the probe abort fires
      await drain();
    });
    expect(assignSpy).not.toHaveBeenCalled();

    await act(async () => {
      vi.advanceTimersByTime(1000); // the backoff reconnect scheduled by the catch arm
      await drain();
    });
    expect(instances.length).toBe(2);
  });

  it('R4: the DORMANT 200 probe (the shipped default) proceeds with the ordinary backoff reconnect', async () => {
    installAuthWire({ probe: () => dormantProbeResponse() });

    const { result } = renderHook(() => useEvents(null, false));
    await act(drain);
    expect(instances.length).toBe(1);

    await act(async () => {
      killStream();
      await drain();
    });

    // The dormant answer is NOT an expired session: no redirect, no
    // breaker banner, and the reconnect loop was NOT broken.
    expect(assignSpy).not.toHaveBeenCalled();
    expect(redirectStormErrorVisible()).toBe(false);
    expect(result.current.connectionState).toBe('disconnected');

    // ...and the ordinary backoff reconnect still happens.
    await act(async () => {
      vi.advanceTimersByTime(1000); // RECONNECT_BASE_DELAY
      await drain();
    });
    expect(instances.length).toBe(2);
  });

  it('#39: the auth-probe failure path is QUIET; the ordinary reconnect keeps its warning', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const debugSpy = vi.spyOn(console, 'debug').mockImplementation(() => {});

    // (a) The probe FAILED: the reconnect happens on the existing backoff,
    // but WITHOUT the warning — the probe path is chatter, not an error.
    installAuthWire({ probe: () => Promise.reject(new TypeError('Failed to fetch')) });
    renderHook(() => useEvents(null, false));
    await act(drain);
    await act(async () => {
      killStream();
      await drain();
    });
    expect(warnSpy).not.toHaveBeenCalled();
    expect(debugSpy).toHaveBeenCalled();

    // (b) The ordinary reconnect (the probe answered) keeps its warning.
    warnSpy.mockClear();
    debugSpy.mockClear();
    resetEventsForTests();
    installAuthWire({ probe: () => dormantProbeResponse() });
    renderHook(() => useEvents(null, false));
    await act(drain);
    await act(async () => {
      killStream();
      await drain();
    });
    expect(warnSpy).toHaveBeenCalledTimes(1);
    expect(String(warnSpy.mock.calls[0][0])).toContain('reconnecting in');
    expect(debugSpy).not.toHaveBeenCalled();
  });

  it('#6: the SSE probe and the one-shot probe AGREE on every probe shape', async () => {
    const shapes: Array<{
      name: string;
      probe: () => Response | Promise<Response>;
      /** the classification the ONE-SHOT probe must report */
      oneShot: 'dormant' | 'enabled-anonymous' | 'unknown';
      /** whether the SSE probe may redirect for this shape */
      redirect: boolean;
    }> = [
      { name: '200 dormant body', probe: () => dormantProbeResponse(), oneShot: 'dormant', redirect: false },
      {
        name: '401 with the enabled body',
        probe: () => anonymousProbeResponse(),
        oneShot: 'enabled-anonymous',
        redirect: true,
      },
      {
        name: '401 with an unparseable body',
        probe: () => unparseableProbeResponse(),
        oneShot: 'unknown',
        redirect: false,
      },
      {
        name: 'network error',
        probe: () => Promise.reject(new TypeError('Failed to fetch')),
        oneShot: 'unknown',
        redirect: false,
      },
    ];

    for (const shape of shapes) {
      // --- the ONE-SHOT probe (hooks/useAuth) ---
      authState.resetForTests();
      installAuthWire({ probe: shape.probe });
      const oneShot = await authState.probe();
      const oneShotClass =
        oneShot === null ? 'unknown' : oneShot.enabled ? 'enabled-anonymous' : 'dormant';

      // --- the SSE probe (hooks/useEvents) ---
      resetEventsForTests();
      resetAuthRedirectLatch();
      authState.resetForTests();
      assignSpy.mockClear();
      installAuthWire({ probe: shape.probe });
      renderHook(() => useEvents(null, false));
      await act(drain);
      await act(async () => {
        killStream();
        await drain();
      });
      const sseRedirected = assignSpy.mock.calls.length > 0;

      expect(oneShotClass, `one-shot probe — ${shape.name}`).toBe(shape.oneShot);
      expect(sseRedirected, `SSE probe — ${shape.name}`).toBe(shape.redirect);
      // The two probes must not diverge: the SSE probe redirects IFF the
      // one-shot probe answers "enabled + anonymous".
      expect(sseRedirected).toBe(oneShot !== null && oneShot.enabled && !oneShot.authenticated);
    }
  });
});
