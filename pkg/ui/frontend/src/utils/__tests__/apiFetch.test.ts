// internal-oauth2 phase 1 (plan §7.3 / §9.3) — the guardedFetch contract.
//
// Pins the single 401 choke point: exactly one bounded login redirect
// per cooldown window (AR-42), never on the probe itself, never in
// dormant mode, and the edge-world one-shot reload (AR-43) firing only
// after the identity probe resolved enabled:true (B2).
//
// The identity-probe responses come from the ONE canonical mock helper
// (./authProbeMocks — tester item R5): the enabled+anonymous shape is
// answered with HTTP 401 + a body carrying `enabled:true` (the backend
// contract, plan §5.8), never with a 200.
//
// NOTE: this file deliberately avoids literal channel-name spellings
// (the wrapped global, SSE class names, etc.) — the AR-60 census scan
// must stay clean outside the guarded production modules.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  guardedFetch,
  authRedirectTarget,
  resetAuthRedirectLatch,
  resetReloadOnce,
  isAuthProbe,
  looksLikeCrossOriginRejection,
} from '../apiFetch';
import { authState } from '../../hooks/useAuth';
import {
  anonymousProbeResponse,
  dormantProbeResponse,
  hangingProbeResponse,
  installAuthWire,
  jsonResponse,
  probeCallCount,
} from './authProbeMocks';

const realFetch = globalThis.fetch;

describe('apiFetch — the guarded 401 choke point', () => {
  let assignSpy: ReturnType<typeof vi.spyOn>;
  let reloadSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    vi.useFakeTimers();
    resetAuthRedirectLatch();
    resetReloadOnce();
    authState.resetForTests();
    assignSpy = vi.spyOn(window.location, 'assign').mockImplementation(() => {});
    reloadSpy = vi.spyOn(window.location, 'reload').mockImplementation(() => {});
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
    (globalThis as { fetch: typeof fetch }).fetch = realFetch;
    authState.resetForTests();
  });

  it('exports isAuthProbe and the rejection classifier', () => {
    expect(isAuthProbe('/fe/api/auth/me')).toBe(true);
    expect(isAuthProbe('/fe/api/models')).toBe(false);
    expect(looksLikeCrossOriginRejection(new TypeError('Failed to fetch'))).toBe(true);
    expect(looksLikeCrossOriginRejection(new Error('nope'))).toBe(false);
  });

  it('T1: probe 401 WITH real body then /fe/api/models 401 => exactly ONE redirect (E2E)', async () => {
    const wire = installAuthWire({
      probe: () => anonymousProbeResponse(),
      target: () => jsonResponse({ error: 'unauthenticated' }, 401),
    });

    const res = await guardedFetch('/fe/api/models');
    expect(res.status).toBe(401);
    expect(assignSpy).toHaveBeenCalledTimes(1);
    expect(assignSpy).toHaveBeenCalledWith('/oauth2/start?rd=%2F');
    expect(wire).toHaveBeenCalledTimes(2);
  });

  it('T3: probe 401 with UNPARSEABLE body => no redirect, not cached false, next 401 re-probes (AR-16)', async () => {
    const wire = installAuthWire({
      probe: () => new Response('not json', { status: 401, headers: { 'Content-Type': 'text/html' } }),
      target: () => jsonResponse({ error: 'unauthenticated' }, 401),
    });

    const pending = guardedFetch('/fe/api/models');
    await pending;
    expect(assignSpy).not.toHaveBeenCalled();
    expect(authState.isEnabledResolved()).toBe(false);
    expect(probeCallCount(wire)).toBe(1);

    // next 401 re-probes
    wire.mockImplementation(async (input: RequestInfo | URL): Promise<Response> => {
      const url = typeof input === 'string' ? input : input.toString();
      if (url.includes('/fe/api/auth/me')) return anonymousProbeResponse();
      return jsonResponse({ error: 'unauthenticated' }, 401);
    });

    await guardedFetch('/fe/api/models');
    expect(assignSpy).toHaveBeenCalledTimes(1);
    expect(probeCallCount(wire)).toBe(2);
  });

  it('the auth probe itself never redirects (guarded 401 on /fe/api/auth/me)', async () => {
    installAuthWire({ probe: () => jsonResponse({ error: 'unauthenticated' }, 401) });

    const res = await guardedFetch('/fe/api/auth/me');
    expect(res.status).toBe(401);
    expect(assignSpy).not.toHaveBeenCalled();
    // The probe answered 401 explicitly: internal mode not in charge.
    expect(authState.isEnabledResolved()).toBe(false);
  });

  it('NO redirect when the probe resolved enabled === false (dormant mode)', async () => {
    installAuthWire({
      probe: () => dormantProbeResponse(),
      target: () => jsonResponse({ error: 'unauthenticated' }, 401),
    });

    await guardedFetch('/fe/api/models');
    expect(assignSpy).not.toHaveBeenCalled();
  });

  it('the latch blocks a second redirect within the cooldown and RE-ARMS after it (AR-42)', async () => {
    installAuthWire({
      probe: () => anonymousProbeResponse(),
      target: () => jsonResponse({ error: 'unauthenticated' }, 401),
    });

    await guardedFetch('/fe/api/models');
    expect(assignSpy).toHaveBeenCalledTimes(1);

    await guardedFetch('/fe/api/models'); // within the 8 s cooldown
    expect(assignSpy).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(8001); // cooldown elapsed — latch re-arms
    await guardedFetch('/fe/api/models');
    expect(assignSpy).toHaveBeenCalledTimes(2);
  });

  it('rd is correctly encoded from pathname + search', () => {
    // pushState URL-normalises the raw space, so location.search carries
    // `x=a%20b`; encodeURIComponent must then escape the `%` itself
    // (%2520) — the faithful per-character encoding the BE rd parser
    // reverses exactly.
    window.history.pushState({}, '', '/ui/settings?tab=models&x=a b');
    const target = authRedirectTarget();
    expect(target).toBe('/oauth2/start?rd=%2Fui%2Fsettings%3Ftab%3Dmodels%26x%3Da%2520b');
    window.history.pushState({}, '', '/ui');
  });

  it('a cross-origin rejection (TypeError) triggers exactly ONE reload — only after the probe resolved enabled:true (AR-43/B2)', async () => {
    const wire = installAuthWire({
      probe: () => anonymousProbeResponse(),
      throwing: () => { throw new TypeError('Failed to fetch'); },
    });

    // Before the probe resolves: a TypeError is a plain error, never a reload.
    await expect(guardedFetch('/fe/api/models')).rejects.toBeInstanceOf(TypeError);
    expect(reloadSpy).not.toHaveBeenCalled();

    // Resolve the probe to enabled:true, then the SAME rejection reloads once.
    await authState.isEnabled();
    await expect(guardedFetch('/fe/api/models')).rejects.toBeInstanceOf(TypeError);
    expect(reloadSpy).toHaveBeenCalledTimes(1);
    expect(wire).toHaveBeenCalled();
  });

  it('the reload is GET-only: a POST rejection never reloads', async () => {
    installAuthWire({
      probe: () => anonymousProbeResponse(),
      throwing: () => { throw new TypeError('Failed to fetch'); },
    });

    await authState.isEnabled();
    await expect(guardedFetch('/fe/api/tokens', { method: 'POST' })).rejects.toBeInstanceOf(TypeError);
    expect(reloadSpy).not.toHaveBeenCalled();
  });

  it('reloadOnce fires at most once per page load', async () => {
    installAuthWire({
      probe: () => anonymousProbeResponse(),
      throwing: () => { throw new TypeError('Failed to fetch'); },
    });

    await authState.isEnabled();
    await expect(guardedFetch('/fe/api/models')).rejects.toBeInstanceOf(TypeError);
    await expect(guardedFetch('/fe/api/requests')).rejects.toBeInstanceOf(TypeError);
    expect(reloadSpy).toHaveBeenCalledTimes(1);
  });

  it('in dormant mode a TypeError never reloads (B2 — probe resolved enabled:false)', async () => {
    installAuthWire({
      probe: () => dormantProbeResponse(),
      throwing: () => { throw new TypeError('Failed to fetch'); },
    });

    await authState.isEnabled();
    await expect(guardedFetch('/fe/api/models')).rejects.toBeInstanceOf(TypeError);
    expect(reloadSpy).not.toHaveBeenCalled();
  });

  it('a probe timeout does NOT cache false — the next 401 re-probes (AR-16)', async () => {
    const wire = installAuthWire({
      probe: (init) => hangingProbeResponse(init),
      target: () => jsonResponse({ error: 'unauthenticated' }, 401),
    });

    // First 401: the probe hangs -> aborts at 3 s -> UNKNOWN -> resolves false -> no redirect.
    const pending = guardedFetch('/fe/api/models');
    await vi.advanceTimersByTimeAsync(3000); // fires the probe's abort timer
    await pending;
    expect(assignSpy).not.toHaveBeenCalled();
    expect(authState.isEnabledResolved()).toBe(false);
    expect(probeCallCount(wire)).toBe(1);

    // A working probe replaces the hanging one: the SECOND 401 must re-probe (not reuse the timeout).
    wire.mockImplementation(async (input: RequestInfo | URL): Promise<Response> => {
      const url = typeof input === 'string' ? input : input.toString();
      if (url.includes('/fe/api/auth/me')) return anonymousProbeResponse();
      return jsonResponse({ error: 'unauthenticated' }, 401);
    });

    await guardedFetch('/fe/api/models');
    expect(assignSpy).toHaveBeenCalledTimes(1);
    expect(probeCallCount(wire)).toBe(2); // re-probed — the timeout was not cached
  });

  it('#11b: a REJECTED probe is UNKNOWN — no redirect, no unhandled rejection', async () => {
    installAuthWire({
      probe: () => anonymousProbeResponse(),
      target: () => jsonResponse({ error: 'unauthenticated' }, 401),
    });
    // The shared probe promise rejects (a hard failure inside the probe
    // machinery). The guard must swallow it into the UNKNOWN path — the
    // awaited resolution must not reject and must never redirect.
    vi.spyOn(authState, 'isEnabled').mockRejectedValue(new Error('probe exploded'));

    const res = await guardedFetch('/fe/api/models');
    expect(res.status).toBe(401);
    expect(assignSpy).not.toHaveBeenCalled();
    expect(reloadSpy).not.toHaveBeenCalled();
  });
});
