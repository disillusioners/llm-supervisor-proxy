// authProbeMocks — the ONE canonical mock helper for the identity-probe
// responses (internal-oauth2 phase 1; tester item R5 — "eliminate
// per-file mock drift").
//
// Every FE test that mocks GET /fe/api/auth/me imports these shapes
// instead of hand-rolling a response body. The tester flagged two
// per-file blind spots:
//   * "200-where-401" drift — the enabled+anonymous shape is answered
//     with HTTP 401 + a body carrying `enabled:true` (plan §5.8); a test
//     that mocks it as a 200 hides the 401 arm;
//   * catch-all `{}` bodies — a body with no boolean `enabled` is not an
//     explicit answer at all (AR-16): the FE treats it as UNKNOWN, never
//     as `enabled:false`.
// The five canonical shapes below are the ONLY bodies the FE auth tests
// use, so a status/body drift is a one-line change here instead of a
// per-file divergence.
//
// NOTE: channel-name spellings are deliberately avoided in this file —
// the AR-60 census must stay clean outside the guarded production
// modules.
import { vi } from 'vitest';

/** The identity-probe path fragment (the probe endpoint). */
export const AUTH_ME_FRAGMENT = '/fe/api/auth/me';

/** A JSON response (the default shape of every probe answer). */
export function jsonResponse(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

/** A non-JSON response (the unparseable-body case). */
export function textResponse(body: string, status: number, contentType = 'text/html'): Response {
  return new Response(body, { status, headers: { 'Content-Type': contentType } });
}

/** 200 — the DORMANT shape (the shipped default). */
export function dormantProbeResponse(): Response {
  return jsonResponse({ enabled: false, authenticated: false, state_store_count: 0 });
}

/** 200 — the SIGNED-IN shape (enabled + a session). */
export function authenticatedProbeResponse(): Response {
  return jsonResponse({
    enabled: true,
    authenticated: true,
    user: { sub: 'sub-1', email: 'op@example.com', name: 'Operator' },
    state_store_count: 2,
  });
}

/** 401 — the enabled+anonymous shape (the body carries enabled:true). */
export function anonymousProbeResponse(): Response {
  return jsonResponse(
    {
      error: 'unauthenticated',
      enabled: true,
      authenticated: false,
      user: null,
      state_store_count: 1,
    },
    401,
  );
}

/** 401 — an UNPARSEABLE body (an EXTERNAL ingress 401) → UNKNOWN. */
export function unparseableProbeResponse(): Response {
  return textResponse('not json', 401);
}

/** A probe that never resolves on its own but rejects when aborted. */
export function hangingProbeResponse(init?: RequestInit): Promise<Response> {
  return new Promise<Response>((_resolve, reject) => {
    const signal = init?.signal;
    if (signal) {
      signal.addEventListener('abort', () =>
        reject(new DOMException('The operation was aborted.', 'AbortError')),
      );
    }
  });
}

export interface AuthWireHandlers {
  /** The identity-probe answer. */
  probe?: (init?: RequestInit) => Response | Promise<Response>;
  /** Any other request (the 401 target of the guardedFetch tests). */
  target?: (init?: RequestInit) => Response | Promise<Response>;
  /** A request that throws (the cross-origin-rejection case). */
  throwing?: (input: RequestInfo | URL, init?: RequestInit) => never;
}

/**
 * Install a wire stub answering by URL; returns the mock for the call
 * assertions (probe-call counting in particular).
 */
export function installAuthWire(handlers: AuthWireHandlers) {
  const wire = vi.fn(async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = typeof input === 'string' ? input : input.toString();
    if (url.includes(AUTH_ME_FRAGMENT)) {
      if (!handlers.probe) throw new Error('unexpected probe request');
      return handlers.probe(init);
    }
    if (handlers.throwing) return handlers.throwing(input, init);
    if (!handlers.target) throw new Error(`unexpected request: ${url}`);
    return handlers.target(init);
  });
  (globalThis as { fetch: typeof fetch }).fetch = wire as unknown as typeof fetch;
  return wire;
}

/** Count the probe requests on a wire mock (the AR-16 re-probe pins). */
export function probeCallCount(wire: { mock: { calls: unknown[][] } }): number {
  return wire.mock.calls.filter(([u]) => String(u).includes(AUTH_ME_FRAGMENT)).length;
}
