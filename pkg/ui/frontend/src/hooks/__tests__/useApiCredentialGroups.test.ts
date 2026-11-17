// credential-groups commission (2026-11-16) — the credential-group
// API layer.
//
// Four contracts are pinned here:
//
//  1. H1 — every group MUTATION invalidates the SAME four model-list
//     cache keys as the credential mutations ('models',
//     'imggen-models', 'decision-models', 'group-models'). A group
//     create/update/delete changes the flat credential lists the model
//     tabs render, so a stale list would be served up to its TTL.
//
//  2. H2 — the group errors are STATUS-BEARING (ApiError). The FE
//     branches on the HTTP status code (409 = the group is referenced
//     by ≥1 model), NEVER on the server's message text. The body parse
//     is guarded: a text/plain body must not throw a SyntaxError.
//
//  3. H3/H4 — the list is UNCACHED and carries the server-side
//     `used_by` (the badge/used-by data must never come from the
//     15s-TTL models cache).
//
//  4. H5 — the URL FORM: the request paths carry NO trailing slash.
//     The trailing slash exists only in the server's ServeMux
//     registration string ("/fe/api/credential-groups/" — the subtree
//     pattern that routes the {id} requests).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import {
  ApiError,
  CREDENTIAL_GROUP_IN_USE_MESSAGE,
  createCredentialGroup,
  deleteCredentialGroup,
  getCredentialGroups,
  updateCredentialGroup,
} from '../useApi';
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
});

function jsonResponse(body: unknown, init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
    ...init,
  });
}

// The four model-list cache keys that must be cleared on a group
// mutation — the same contract the credential mutations carry.
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

function lastCall(): [string, RequestInit] {
  const calls = fetchMock.mock.calls as unknown as [string, RequestInit][];
  return calls[calls.length - 1];
}

describe('credential-group API (credential-groups commission)', () => {
  it('H1 — createCredentialGroup invalidates all four model-list keys', async () => {
    seedAllKeys();
    fetchMock.mockResolvedValueOnce(jsonResponse({ id: 'pool-1', name: 'Pool 1' }));
    await createCredentialGroup({
      id: 'pool-1',
      name: 'Pool 1',
      provider: 'openai',
      members: ['cred-a'],
    });
    assertKeysAbsent();
  });

  it('H1 — updateCredentialGroup invalidates all four model-list keys', async () => {
    seedAllKeys();
    fetchMock.mockResolvedValueOnce(jsonResponse({ id: 'pool-1', name: 'Pool 1 renamed' }));
    await updateCredentialGroup('pool-1', {
      id: 'pool-1',
      name: 'Pool 1 renamed',
      provider: 'openai',
      members: ['cred-a'],
    });
    assertKeysAbsent();
  });

  it('H1 — deleteCredentialGroup invalidates all four model-list keys', async () => {
    seedAllKeys();
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));
    await deleteCredentialGroup('pool-1');
    assertKeysAbsent();
  });

  it('H1 — a FAILED mutation leaves the caches intact (the retry cleans them)', async () => {
    seedAllKeys();
    fetchMock.mockResolvedValueOnce(jsonResponse({ error: 'boom' }, { status: 500 }));
    await expect(deleteCredentialGroup('pool-1')).rejects.toThrow();
    for (const k of MODEL_KEYS) {
      expect(defaultAPICache.get(k)).toBeDefined();
    }
  });

  it('H2 — a 409 delete throws an ApiError carrying the status (never message-text branching)', async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ error: 'credential group "pool-1" is referenced by 2 models' }, { status: 409 }),
    );
    let caught: unknown = null;
    try {
      await deleteCredentialGroup('pool-1');
    } catch (e) {
      caught = e;
    }
    expect(caught).toBeInstanceOf(ApiError);
    expect((caught as ApiError).status).toBe(409);
    // The server's text is carried for humans; the FE message is the
    // NAMED constant, never the server's string.
    expect(CREDENTIAL_GROUP_IN_USE_MESSAGE).toContain('Inline its members');
    expect((caught as ApiError).message).toContain('referenced by');
  });

  it('H2 — a text/plain error body does not throw a SyntaxError (the parse is guarded)', async () => {
    fetchMock.mockResolvedValueOnce(
      new Response('credential group is in use', {
        status: 409,
        headers: { 'Content-Type': 'text/plain' },
      }),
    );
    let caught: unknown = null;
    try {
      await deleteCredentialGroup('pool-1');
    } catch (e) {
      caught = e;
    }
    expect(caught).toBeInstanceOf(ApiError);
    expect((caught as ApiError).status).toBe(409);
    expect((caught as ApiError).message).toContain('HTTP 409');
  });

  it('H3 — the list carries the server-side used_by', async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse([
        { id: 'pool-1', name: 'Pool 1', provider: 'openai', members: ['cred-a'], used_by: ['model-x'] },
      ]),
    );
    const groups = await getCredentialGroups();
    expect(groups).toHaveLength(1);
    expect(groups[0].used_by).toEqual(['model-x']);
  });

  it('R6 — a 200 with a text/plain body resolves to [] (the list parse is guarded)', async () => {
    // The guard at useApi.ts:1079 — `await res.json().catch(() => null)` —
    // must swallow a non-JSON success body. Without it the SyntaxError
    // propagates out of getCredentialGroups().
    fetchMock.mockResolvedValueOnce(
      new Response('not json', {
        status: 200,
        headers: { 'Content-Type': 'text/plain' },
      }),
    );
    await expect(getCredentialGroups()).resolves.toEqual([]);
  });

  it('H4 — the group list is fetched UNCACHED (every call hits the network)', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([]));
    await getCredentialGroups();
    fetchMock.mockResolvedValueOnce(jsonResponse([]));
    await getCredentialGroups();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('H5 — the request URLs carry NO trailing slash', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([]));
    await getCredentialGroups();
    expect(lastCall()[0]).toBe('/fe/api/credential-groups');

    fetchMock.mockResolvedValueOnce(jsonResponse({ id: 'pool-1' }));
    await createCredentialGroup({ id: 'pool-1', name: 'Pool 1', provider: 'openai', members: [] });
    expect(lastCall()[0]).toBe('/fe/api/credential-groups');
    expect(lastCall()[1].method).toBe('POST');

    fetchMock.mockResolvedValueOnce(jsonResponse({ id: 'pool-1' }));
    await updateCredentialGroup('pool-1', { name: 'Pool 1 renamed' });
    expect(lastCall()[0]).toBe('/fe/api/credential-groups/pool-1');
    expect(lastCall()[1].method).toBe('PUT');

    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));
    await deleteCredentialGroup('pool-1');
    expect(lastCall()[0]).toBe('/fe/api/credential-groups/pool-1');
    expect(lastCall()[1].method).toBe('DELETE');

    // No request path ends with a slash.
    for (const call of fetchMock.mock.calls as unknown as [string][]) {
      expect(call[0].endsWith('/')).toBe(false);
    }
  });

  it('H5 — the create payload is exactly {id,name,provider,members}', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ id: 'pool-1' }));
    await createCredentialGroup({
      id: 'pool-1',
      name: 'Pool 1',
      provider: 'openai',
      members: ['cred-a', 'cred-b'],
    });
    const body = JSON.parse(String(lastCall()[1].body)) as Record<string, unknown>;
    expect(Object.keys(body).sort()).toEqual(['id', 'members', 'name', 'provider']);
    expect(body.members).toEqual(['cred-a', 'cred-b']);
  });
});
