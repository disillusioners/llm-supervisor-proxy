// token-scope contract (auth commission / phase-3 FE, FE-8 / FE-9 / FE-10)
// — the token GRANT WIRE builders.
//
// These tests pin the REQUEST BODY the two builders emit, because the body
// is the security-relevant artifact: the tri-state on the wire is
//
//   key missing OR explicit null => ABSENT => UNRESTRICTED (wildcard)
//   []                           => PRESENT-EMPTY => grants NOTHING
//   [ids...]                     => PRESENT with exactly those ids
//
// CREATE is a FULL WRITE of both grant columns, so both keys are ALWAYS
// present and an absent axis is an explicit `null`.
//
// PATCH is a MERGE: an untouched axis is a MISSING key, and because Go
// decodes an omitted key and an explicit `null` into the same nil
// *[]string, `null` on PATCH is a KEEP, not a clear. Clearing an axis MUST
// send `[]`. The pre-change FE sent `null` to clear — under this BE that was
// a silent no-op (operator believes access was revoked; it was not) and
// under a pre-change BE it was a wildcard ESCALATION. The regression this
// file exists to catch is exactly `[]` being "simplified" back to `null`.
//
// Style: mirrors useApiCredentials.test.ts — stub globalThis.fetch and
// assert on the request body (JSON.parse of init.body), never on the DOM.
// @ts-nocheck — vitest's own types pull in @types/node, which the FE
// tsconfig does not ship (same rationale as useApiCredentials.test.ts).
// This file runs under the vitest runner only; the production tsc surface
// and its frozen baseline (pkg/ui/frontend/tsc-baseline.txt) are untouched.
// @ts-nocheck
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { renderHook, waitFor, act, cleanup } from '@testing-library/preact';
import { useTokens } from '../useApi';
import { defaultAPICache } from '../../utils/apiCache';

const fetchMock = vi.fn();
const realFetch = globalThis.fetch;

// A stored token row as the list endpoint returns it. Both grant axes are
// always present in a real payload (the BE assigns a non-nil *[]string); the
// row below starts with a present MODEL grant and an ABSENT GROUP grant.
const TOKEN_ROW = {
  id: 'tok-1',
  name: 'my token',
  prefix: 'sk-proxy-***',
  created_at: '2026-01-01T00:00:00Z',
  allowed_models: ['keep-me'],
  allowed_groups: null,
};

beforeEach(() => {
  (globalThis as { fetch: typeof fetch }).fetch = fetchMock as unknown as typeof fetch;
  defaultAPICache.clear();
  fetchMock.mockReset();
  // Router: GET list -> [TOKEN_ROW]; POST create -> created row;
  // PATCH -> {"success":true}. Anything else fails loudly.
  fetchMock.mockImplementation((url: string, init?: RequestInit) => {
    const method = (init?.method ?? 'GET').toUpperCase();
    if (method === 'GET' && String(url).endsWith('/tokens')) {
      return Promise.resolve(jsonResponse([TOKEN_ROW]));
    }
    if (method === 'POST' && String(url).endsWith('/tokens')) {
      return Promise.resolve(jsonResponse({ ...TOKEN_ROW, id: 'tok-new', token: 'sk-proxy-plaintext' }));
    }
    if (method === 'PATCH') {
      return Promise.resolve(jsonResponse({ success: true }));
    }
    return Promise.reject(new Error(`unexpected fetch: ${method} ${url}`));
  });
});

afterEach(() => {
  (globalThis as { fetch: typeof fetch }).fetch = realFetch;
  cleanup();
});

function jsonResponse(body: unknown, init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
    ...init,
  });
}

/** Bodies of every fetch call made with the given HTTP method. */
function bodiesFor(method: string): Array<Record<string, unknown>> {
  const calls = fetchMock.mock.calls as Array<[string, RequestInit | undefined]>;
  return calls
    .filter(([, init]) => ((init?.method ?? 'GET').toUpperCase()) === method)
    .map(([, init]) => JSON.parse(String(init?.body)) as Record<string, unknown>);
}

/** Render the tokens hook and wait for the initial list fetch to settle. */
async function mountTokens() {
  const rendered = renderHook(() => useTokens());
  await waitFor(() => expect(rendered.result.current.loading).toBe(false));
  return rendered;
}

describe('useApi token grant wire (token-scope FE-8/FE-9/FE-10)', () => {
  describe('createToken — FULL WRITE, both axes always on the wire', () => {
    it('useApi_CreateToken_SendsBothAxesNullWhenNothingSelected', async () => {
      const { result } = await mountTokens();

      await act(async () => {
        await result.current.createToken('no grants', null);
      });

      const bodies = bodiesFor('POST');
      expect(bodies).toHaveLength(1);
      const body = bodies[0];
      // ABSENT must be an EXPLICIT null, not an omitted key: the POST is a
      // full write of both columns, and omission would be indistinguishable
      // from "not provided" for a future full-write reader.
      expect(Object.prototype.hasOwnProperty.call(body, 'allowed_models')).toBe(true);
      expect(Object.prototype.hasOwnProperty.call(body, 'allowed_groups')).toBe(true);
      expect(body.allowed_models).toBeNull();
      expect(body.allowed_groups).toBeNull();
    });

    it('useApi_CreateToken_SendsEmptyArrayForRestrictedEmptyAxis', async () => {
      const { result } = await mountTokens();

      await act(async () => {
        // MODEL axis = PRESENT-empty (grants nothing) — NOT absent, NOT
        // wildcard. GROUP axis omitted => absent/unrestricted.
        await result.current.createToken('empty model grant', null, []);
      });

      const body = bodiesFor('POST')[0];
      expect(Object.prototype.hasOwnProperty.call(body, 'allowed_models')).toBe(true);
      expect(body.allowed_models).toEqual([]);
      expect(body.allowed_groups).toBeNull();
    });

    it('useApi_CreateToken_SendsIdArrayForRestrictedAxis', async () => {
      const { result } = await mountTokens();

      await act(async () => {
        await result.current.createToken('restricted', null, ['m1', 'm2'], ['g1']);
      });

      const body = bodiesFor('POST')[0];
      expect(body.allowed_models).toEqual(['m1', 'm2']);
      expect(body.allowed_groups).toEqual(['g1']);
    });
  });

  describe('updateTokenPermission — MERGE, [] clears and null/omitted keeps', () => {
    it('useApi_UpdateTokenPermission_SendsEmptyArrayToClearAxis', async () => {
      const { result } = await mountTokens();

      await act(async () => {
        // Clearing the MODEL axis MUST be `[]`. `null` here would be a KEEP
        // on this BE and a wildcard escalation on a pre-change BE.
        await result.current.updateTokenPermission('tok-1', []);
      });

      const body = bodiesFor('PATCH')[0];
      expect(body.allowed_models).toEqual([]);
      // The untouched GROUP axis is not named at all.
      expect(Object.prototype.hasOwnProperty.call(body, 'allowed_groups')).toBe(false);
    });

    it('useApi_UpdateTokenPermission_OmitsUntouchedAxis', async () => {
      const { result } = await mountTokens();

      await act(async () => {
        await result.current.updateTokenPermission('tok-1', ['m1']);
      });

      const body = bodiesFor('PATCH')[0];
      expect(body.allowed_models).toEqual(['m1']);
      expect(Object.prototype.hasOwnProperty.call(body, 'allowed_groups')).toBe(false);
    });

    it('useApi_UpdateTokenPermission_ExplicitNullMeansKeep', async () => {
      const { result } = await mountTokens();

      await act(async () => {
        // Ruling 2: an explicit null and an omitted key both mean KEEP (Go
        // decodes both to a nil *[]string). It must never be confused with
        // a clear — and it must not clobber the optimistic local state.
        await result.current.updateTokenPermission('tok-1', null);
      });

      const body = bodiesFor('PATCH')[0];
      expect(Object.prototype.hasOwnProperty.call(body, 'allowed_models')).toBe(true);
      expect(body.allowed_models).toBeNull();
      // Local state for the nulled axis is UNCHANGED (still the stored grant).
      expect(result.current.tokens[0].allowed_models).toEqual(['keep-me']);
      expect(result.current.tokens[0].allowed_groups).toBeNull();
    });

    it('useApi_UpdateTokenPermission_SendsIdArrayToReplace', async () => {
      const { result } = await mountTokens();

      await act(async () => {
        await result.current.updateTokenPermission('tok-1', ['m7', 'm8'], ['g4']);
      });

      const body = bodiesFor('PATCH')[0];
      expect(body.allowed_models).toEqual(['m7', 'm8']);
      expect(body.allowed_groups).toEqual(['g4']);
    });

    it('useApi_UpdateTokenPermission_OptimisticMergeUpdatesBothAxes', async () => {
      const { result } = await mountTokens();

      await act(async () => {
        await result.current.updateTokenPermission('tok-1', ['new-m'], ['new-g']);
      });

      const token = result.current.tokens[0];
      expect(token.allowed_models).toEqual(['new-m']);
      expect(token.allowed_groups).toEqual(['new-g']);
      // A PATCH must not resurrect the whole list — the row id is preserved.
      expect(token.id).toBe('tok-1');
    });
  });
});
