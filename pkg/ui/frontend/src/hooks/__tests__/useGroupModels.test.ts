import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { renderHook, waitFor, act, cleanup } from '@testing-library/preact';
import { useGroupModels } from '../useApi';
import { defaultAPICache } from '../../utils/apiCache';

// Stub the global fetch used by the hook. The hook is a small wrapper
// around defaultAPICache.getOrFetch which calls fetch directly, so
// stubbing globalThis.fetch is the cleanest seam. Mirrors the sibling
// hook tests (useModels / useImgGenModels / useDecisionModels).
const fetchMock = vi.fn();
const realFetch = globalThis.fetch;
beforeEach(() => {
  (globalThis as { fetch: typeof fetch }).fetch = fetchMock as unknown as typeof fetch;
  // Reset cache + mock between tests so each one starts clean.
  defaultAPICache.clear();
  fetchMock.mockReset();
});
afterEach(() => {
  (globalThis as { fetch: typeof fetch }).fetch = realFetch;
  cleanup();
});

// Helper — build a fake Response that json() resolves to the given body.
function jsonResponse(body: unknown, init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
    ...init,
  });
}

// Helper — assert that ALL FOUR cache keys were deleted at least once in
// the spy call log. Used by every mutation test (load-balance-groups /
// plan §6.3 / P3.3): every useGroupModels mutation MUST delete all four
// keys to keep the four model lists in lock-step. Strict invariant
// (P3.3 acceptance; the same four-key pin extends the three sibling
// mutation-test files).
//
// Uses the loose spy-on-method signature (`{ mock: { calls } }`) so the
// helper accepts both `vi.spyOn(defaultAPICache, 'delete')` (the
// overload tsc infers) and a bare `vi.fn()` — both appear in the file.
function assertAllFourKeysDeleted(deleteSpy: { mock: { calls: Array<Array<unknown>> } }): void {
  const deletedKeys = deleteSpy.mock.calls.map(([k]) => k as string);
  expect(deletedKeys).toContain('models');
  expect(deletedKeys).toContain('imggen-models');
  expect(deletedKeys).toContain('decision-models');
  expect(deletedKeys).toContain('group-models');
}

describe('useGroupModels — hook contract (load-balance-groups P3.2)', () => {
  describe('fetch shape (load-balance-groups / plan §6.3)', () => {
    it('hits /fe/api/models?kind=group on first call', async () => {
      fetchMock.mockResolvedValue(jsonResponse([]));
      const { result } = renderHook(() => useGroupModels());

      await waitFor(() => expect(result.current.loading).toBe(false));

      expect(fetchMock).toHaveBeenCalledTimes(1);
      const url = fetchMock.mock.calls[0][0] as string;
      expect(url).toContain('/fe/api/models');
      expect(url).toContain('kind=group');
    });

    it('parses the response into the returned models array', async () => {
      const rows = [
        { id: 'g1', name: 'G1', enabled: true, fallback_chain: [], kind: 'group', members: [{ model_id: 'a', weight: 1 }], shadow_model_id: '' },
        { id: 'g2', name: 'G2', enabled: false, fallback_chain: [], kind: 'group', members: [{ model_id: 'a', weight: 1 }, { model_id: 'b', weight: 2 }], shadow_model_id: 'a' },
      ];
      fetchMock.mockResolvedValue(jsonResponse(rows));
      const { result } = renderHook(() => useGroupModels());

      await waitFor(() => expect(result.current.loading).toBe(false));

      expect(result.current.models).toEqual(rows);
    });
  });

  describe('cache key', () => {
    it('uses cache key "group-models" — a second render without mutation does not re-fetch', async () => {
      fetchMock.mockResolvedValue(jsonResponse([]));
      const { result, rerender } = renderHook(() => useGroupModels());

      await waitFor(() => expect(result.current.loading).toBe(false));
      expect(fetchMock).toHaveBeenCalledTimes(1);

      // Re-render the same hook instance; the cache should serve the same data.
      rerender();
      await act(async () => {
        await Promise.resolve();
      });
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });
  });

  describe('quadruple-key invalidation on every mutation (load-balance-groups / plan §6.3)', () => {
    // The load-balance-groups P3 contract: every model mutation hook
    // (useModels, useImgGenModels, useDecisionModels, useGroupModels)
    // MUST delete all four keys on every mutation so the four tab lists
    // never go stale relative to each other. The strict invariant is
    // enforced here.

    it('addModel deletes all four keys (plan §6.3)', async () => {
      const deleteSpy = vi.spyOn(defaultAPICache, 'delete');

      fetchMock.mockResolvedValueOnce(jsonResponse([])); // initial fetch
      const { result } = renderHook(() => useGroupModels());
      await waitFor(() => expect(result.current.loading).toBe(false));

      // Reset spy to only observe the addModel-time deletes.
      deleteSpy.mockClear();

      // Mock the POST.
      fetchMock.mockResolvedValueOnce(jsonResponse({ id: 'new', name: 'new', enabled: true, fallback_chain: [] }));
      // Mock the refetch that happens after the deletes.
      fetchMock.mockResolvedValueOnce(jsonResponse([]));

      await act(async () => {
        await result.current.addModel({
          id: 'new',
          name: 'new',
          enabled: true,
          fallback_chain: [],
        });
      });

      assertAllFourKeysDeleted(deleteSpy);

      deleteSpy.mockRestore();
    });

    it('updateModel deletes all four keys (plan §6.3)', async () => {
      const deleteSpy = vi.spyOn(defaultAPICache, 'delete');

      fetchMock.mockResolvedValueOnce(jsonResponse([
        { id: 'group-1', name: 'G1', enabled: true, fallback_chain: [], kind: 'group' },
      ]));
      const { result } = renderHook(() => useGroupModels());
      await waitFor(() => expect(result.current.loading).toBe(false));

      deleteSpy.mockClear();

      // PUT response.
      fetchMock.mockResolvedValueOnce(jsonResponse({ id: 'group-1', name: 'G1-updated', enabled: true, fallback_chain: [], kind: 'group' }));
      // Refetch after invalidation.
      fetchMock.mockResolvedValueOnce(jsonResponse([
        { id: 'group-1', name: 'G1-updated', enabled: true, fallback_chain: [], kind: 'group' },
      ]));

      await act(async () => {
        await result.current.updateModel('group-1', { name: 'G1-updated' });
      });

      assertAllFourKeysDeleted(deleteSpy);

      deleteSpy.mockRestore();
    });

    it('deleteModel deletes all four keys (plan §6.3)', async () => {
      const deleteSpy = vi.spyOn(defaultAPICache, 'delete');

      fetchMock.mockResolvedValueOnce(jsonResponse([
        { id: 'group-1', name: 'G1', enabled: true, fallback_chain: [], kind: 'group' },
      ]));
      const { result } = renderHook(() => useGroupModels());
      await waitFor(() => expect(result.current.loading).toBe(false));

      deleteSpy.mockClear();

      // DELETE response (204).
      fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));
      // Refetch.
      fetchMock.mockResolvedValueOnce(jsonResponse([]));

      await act(async () => {
        await result.current.deleteModel('group-1');
      });

      assertAllFourKeysDeleted(deleteSpy);

      deleteSpy.mockRestore();
    });
  });
});
