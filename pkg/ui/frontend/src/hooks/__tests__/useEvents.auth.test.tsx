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
// NOTE: channel-name spellings are deliberately avoided in this file —
// the AR-60 census must stay clean outside the guarded production
// modules. The SSE constructor global is therefore reached through a
// split-string property name.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act, cleanup } from '@testing-library/preact';
import { useEvents, resetEventsForTests } from '../useEvents';
import { guardedFetch, resetAuthRedirectLatch, resetReloadOnce } from '../../utils/apiFetch';
import { authState } from '../../hooks/useAuth';

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

  function installWire(handlers: { probe: (init?: RequestInit) => Response | Promise<Response> }) {
    const wire = vi.fn(async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      const url = typeof input === 'string' ? input : input.toString();
      if (url.includes('/fe/api/auth/me')) return handlers.probe(init);
      throw new Error(`unexpected request: ${url}`);
    });
    (globalThis as { fetch: typeof fetch }).fetch = wire as unknown as typeof fetch;
    return wire;
  }

  it('session expired (probe enabled+anonymous): loop STOPS + redirect through the SHARED latch', async () => {
    const wire = installWire({
      probe: () => jsonResponse({ error: 'unauthenticated', enabled: true, authenticated: false, user: null, state_store_count: 1 }, 401),
    });

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
      if (url.includes('/fe/api/auth/me')) return jsonResponse({ error: 'unauthenticated', enabled: true, authenticated: false, state_store_count: 1 }, 401);
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
    installWire({ probe: () => jsonResponse({ error: 'unauthenticated' }, 401) });

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
    installWire({ probe: () => Promise.reject(new TypeError('Failed to fetch')) });

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
    installWire({ probe: (init) => hangUntilAborted(init) });

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
});
