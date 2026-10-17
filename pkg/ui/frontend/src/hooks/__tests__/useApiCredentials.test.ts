// load-balance-groups / P4.5 (P3 #1 carry-forward) — credential
// mutations invalidate the FOUR model-list cache keys.
//
// Pre-fix defect: createCredential / updateCredential /
// deleteCredential in useApi.ts called fetch but NEVER touched the
// FE cache, so a deleted or renamed credential could be served to a
// caller holding a cached Model row up to its TTL (15s+ on the
// hooked tabs). The fix adds the same quadruple-delete discipline
// that the sibling model hooks use: 'models', 'imggen-models',
// 'decision-models', 'group-models'.
//
// This test primes each cache key with a dummy entry, calls one of
// the credential mutation functions against a mocked fetch, and
// asserts all four keys are gone afterwards. A regression that
// removes any of the four `defaultAPICache.delete(...)` calls
// re-introduces the stale-credential leak.
//
// `@ts-nocheck`: this file uses `vi` from vitest, which requires
// the `@types/node` package on disk; the FE tsconfig does not
// ship `@types/node`. The test runs only via the vitest runner
// (happy-dom + Node) and is OUTSIDE the production tsc surface —
// the production dashboard tsc scope is `src/` minus
// `__tests__/` (verified by `pkg/ui/frontend/tsc-baseline.txt`
// pinning the per-file count, enforced at vitest time by
// `src/utils/__tests__/tsc-baseline.test.ts`).
// @ts-nocheck
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { cleanup } from '@testing-library/preact';
import { createCredential, updateCredential, deleteCredential } from '../useApi';
import { defaultAPICache } from '../../utils/apiCache';

const fetchMock = vi.fn();
const realFetch = globalThis.fetch;
beforeEach(() => {
  (globalThis as { fetch: typeof fetch }).fetch = fetchMock as unknown as typeof fetch;
  defaultAPICache.clear();
  fetchMock.mockReset();
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

// The four model-list cache keys that must be cleared on credential
// mutation. If a future model kind is added (imagg / decision /
// group / ...), extend this list AND the calls in useApi.ts — the
// list is the contract.
const MODEL_KEYS = ['models', 'imggen-models', 'decision-models', 'group-models'] as const;

function seedAllKeys(): void {
  for (const k of MODEL_KEYS) {
    defaultAPICache.set(k, [{ id: 'placeholder', name: 'placeholder', enabled: false }], 60_000);
  }
}

function assertKeysAbsent(): void {
  for (const k of MODEL_KEYS) {
    expect(defaultAPICache.get(k)).toBeUndefined();
  }
}

describe('credential mutation cache invalidation (load-balance-groups P4.5)', () => {
  it('createCredential invalidates all four model-list keys', async () => {
    seedAllKeys();
    fetchMock.mockResolvedValueOnce(jsonResponse({ id: 'cred-1', name: 'New Cred' }));
    await createCredential({ id: 'cred-1', name: 'New Cred', provider: 'openai', api_key: 'sk-x' });
    assertKeysAbsent();
  });

  it('updateCredential invalidates all four model-list keys', async () => {
    seedAllKeys();
    fetchMock.mockResolvedValueOnce(jsonResponse({ id: 'cred-1', name: 'Renamed' }));
    await updateCredential('cred-1', { id: 'cred-1', name: 'Renamed', provider: 'openai', api_key: 'sk-x' });
    assertKeysAbsent();
  });

  it('deleteCredential invalidates all four model-list keys', async () => {
    seedAllKeys();
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));
    await deleteCredential('cred-1');
    assertKeysAbsent();
  });

  it('leaves non-model caches alone (only the four key contract is enforced)', async () => {
    // Prime a foreign cache key + the four contract keys; after a
    // credential mutation the four contract keys are gone, but
    // 'providers' / 'requests' / etc. must remain (the contract is
    // named explicitly, not a prefix wipe).
    const foreignKey = 'providers';
    defaultAPICache.set(foreignKey, [{ id: 'openai', label: 'OpenAI' }], 60_000);
    seedAllKeys();
    fetchMock.mockResolvedValueOnce(jsonResponse({ id: 'cred-2' }));
    await createCredential({ id: 'cred-2', name: 'X', provider: 'openai', api_key: 'sk-y' });
    assertKeysAbsent();
    expect(defaultAPICache.get(foreignKey)).toBeDefined();
  });

  it('mutation error does NOT silently skip the invalidation (existing surface)', async () => {
    // The current implementation invalidates AFTER the fetch
    // resolves; a failed mutation leaves the caches intact so
    // users can retry. Pin that contract — when the API is down,
    // the cache stays, and the next successful attempt cleans it.
    seedAllKeys();
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ error: 'boom' }), { status: 500 }));
    await expect(
      createCredential({ id: 'cred-3', name: 'X', provider: 'openai', api_key: 'sk-z' }),
    ).rejects.toThrow();
    // All four keys still populated because the mutation failed.
    for (const k of MODEL_KEYS) {
      expect(defaultAPICache.get(k)).toBeDefined();
    }
  });
});
