// The ONE shared identity-probe contract (utils/authProbe) — the
// tidier #6 dedup cluster.
//
// Pins the shared probe-parse helper: the runtime type guard for the
// untrusted body (no bare `as` on network input) and the outcome
// classification both probe call sites delegate to. The cross-site
// agreement pin (the one-shot probe vs the SSE probe) lives in
// hooks/__tests__/useEvents.auth.test.tsx.
//
// NOTE: channel-name spellings are deliberately avoided in this file —
// the AR-60 census must stay clean outside the guarded production
// modules.
import { describe, it, expect } from 'vitest';
import {
  AUTH_ME_PATH,
  AUTH_PROBE_TIMEOUT_MS,
  parseAuthProbeResponse,
  toAuthProbeBody,
} from '../authProbe';
import {
  anonymousProbeResponse,
  authenticatedProbeResponse,
  dormantProbeResponse,
  jsonResponse,
  textResponse,
  unparseableProbeResponse,
} from './authProbeMocks';

describe('authProbe — the shared identity-probe contract', () => {
  it('publishes ONE endpoint path and ONE timeout budget', () => {
    expect(AUTH_ME_PATH).toBe('/fe/api/auth/me');
    expect(AUTH_PROBE_TIMEOUT_MS).toBe(3000);
  });

  it('the type guard accepts only a body with a boolean `enabled`', () => {
    expect(toAuthProbeBody(null)).toBeNull();
    expect(toAuthProbeBody('enabled')).toBeNull();
    expect(toAuthProbeBody([])).toBeNull();
    expect(toAuthProbeBody({})).toBeNull();
    expect(toAuthProbeBody({ enabled: 'true' })).toBeNull(); // a string is NOT an explicit answer
    expect(toAuthProbeBody({ enabled: true })).toEqual({
      enabled: true,
      authenticated: false,
      state_store_count: 0,
    });
  });

  it('the type guard normalises the user sub-shape (never preferred_username)', () => {
    const body = toAuthProbeBody({
      enabled: true,
      authenticated: true,
      user: { sub: 'sub-1', email: 'op@example.com', name: 'Operator', preferred_username: 'nope' },
      state_store_count: 3,
    });
    expect(body).toEqual({
      enabled: true,
      authenticated: true,
      state_store_count: 3,
      user: { sub: 'sub-1', email: 'op@example.com', name: 'Operator' },
    });
    expect(Object.keys(body?.user ?? {})).not.toContain('preferred_username');
  });

  it('a 200 dormant body is an EXPLICIT answer (enabled:false)', async () => {
    const outcome = await parseAuthProbeResponse(dormantProbeResponse());
    expect(outcome.kind).toBe('answer');
    expect(outcome.kind === 'answer' && outcome.body.enabled).toBe(false);
    expect(outcome.kind === 'answer' && outcome.body.authenticated).toBe(false);
  });

  it('a 200 signed-in body is an EXPLICIT answer (enabled + authenticated)', async () => {
    const outcome = await parseAuthProbeResponse(authenticatedProbeResponse());
    expect(outcome.kind).toBe('answer');
    expect(outcome.kind === 'answer' && outcome.body.enabled).toBe(true);
    expect(outcome.kind === 'answer' && outcome.body.authenticated).toBe(true);
    expect(outcome.kind === 'answer' && outcome.body.user?.sub).toBe('sub-1');
  });

  it('a 401 body with a boolean `enabled` is an EXPLICIT answer (the enabled+anonymous shape)', async () => {
    const outcome = await parseAuthProbeResponse(anonymousProbeResponse());
    expect(outcome.kind).toBe('answer');
    expect(outcome.kind === 'answer' && outcome.body.enabled).toBe(true);
    expect(outcome.kind === 'answer' && outcome.body.authenticated).toBe(false);
  });

  it('a 401 with an UNPARSEABLE body is UNKNOWN — never a cached enabled:false (AR-16)', async () => {
    expect((await parseAuthProbeResponse(unparseableProbeResponse())).kind).toBe('unknown');
    expect((await parseAuthProbeResponse(textResponse('<html>401</html>', 401))).kind).toBe('unknown');
    // A parseable body WITHOUT a boolean `enabled` is UNKNOWN too.
    expect((await parseAuthProbeResponse(jsonResponse({ error: 'unauthenticated' }, 401))).kind).toBe(
      'unknown',
    );
  });

  it('a status that is neither 200 nor 401 is `not-in-charge` (the internal mode is not in charge)', async () => {
    expect((await parseAuthProbeResponse(jsonResponse({}, 404))).kind).toBe('not-in-charge');
    expect((await parseAuthProbeResponse(jsonResponse({}, 500))).kind).toBe('not-in-charge');
  });
});
