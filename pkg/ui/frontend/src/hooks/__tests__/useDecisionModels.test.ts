import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { renderHook, waitFor, act, cleanup } from '@testing-library/preact';
import { useDecisionModels } from '../useApi';
import { defaultAPICache } from '../../utils/apiCache';

// Stub the global fetch used by the hook. The hook is a small wrapper
// around defaultAPICache.getOrFetch which calls fetch directly, so
// stubbing globalThis.fetch is the cleanest seam.
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

// Helper — assert that ALL three cache keys were deleted at least once in
// the spy call log. Used by every mutation test (Decision Commission
// 2026-10-15 / plan §6.4 / amendment 9d): every useDecisionModels
// mutation MUST delete all three keys to keep the three model lists in
// lock-step. Strict invariant (P3.4 acceptance).
//
// Uses the loose spy-on-method signature (`{ mock: { calls } }`) so the
// helper accepts both `vi.spyOn(defaultAPICache, 'delete')` (the
// overload tsc infers) and a bare `vi.fn()` — both appear in the file.
function assertAllThreeKeysDeleted(deleteSpy: { mock: { calls: Array<Array<unknown>> } }): void {
  const deletedKeys = deleteSpy.mock.calls.map(([k]) => k as string);
  expect(deletedKeys).toContain('models');
  expect(deletedKeys).toContain('imggen-models');
  expect(deletedKeys).toContain('decision-models');
}

describe('useDecisionModels — hook contract (Decision Commission P3.4)', () => {
  describe('fetch shape (Decision Commission 2026-10-15)', () => {
    it('hits /fe/api/models?kind=decision on first call', async () => {
      fetchMock.mockResolvedValue(jsonResponse([]));
      const { result } = renderHook(() => useDecisionModels());

      await waitFor(() => expect(result.current.loading).toBe(false));

      expect(fetchMock).toHaveBeenCalledTimes(1);
      const url = fetchMock.mock.calls[0][0] as string;
      expect(url).toContain('/fe/api/models');
      expect(url).toContain('kind=decision');
    });

    it('parses the response into the returned models array', async () => {
      const rows = [
        { id: 'a', name: 'A', enabled: true, fallback_chain: [], kind: 'decision' },
        { id: 'b', name: 'B', enabled: false, fallback_chain: [], kind: 'decision' },
      ];
      fetchMock.mockResolvedValue(jsonResponse(rows));
      const { result } = renderHook(() => useDecisionModels());

      await waitFor(() => expect(result.current.loading).toBe(false));

      expect(result.current.models).toEqual(rows);
    });
  });

  describe('cache key', () => {
    it('uses cache key "decision-models" — a second render without mutation does not re-fetch', async () => {
      fetchMock.mockResolvedValue(jsonResponse([]));
      const { result, rerender } = renderHook(() => useDecisionModels());

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

  describe('triple-key invalidation on every mutation (Decision Commission 2026-10-15 / amendment 9d)', () => {
    // The Decision Commission P3 contract: every model mutation hook
    // (useModels, useImgGenModels, useDecisionModels) MUST delete all
    // three keys on every mutation so the three tab lists never go stale
    // relative to each other. The strict invariant is enforced here.

    it('addModel deletes all three keys (amendment 9d)', async () => {
      const deleteSpy = vi.spyOn(defaultAPICache, 'delete');

      fetchMock.mockResolvedValueOnce(jsonResponse([])); // initial fetch
      const { result } = renderHook(() => useDecisionModels());
      await waitFor(() => expect(result.current.loading).toBe(false));

      // Reset spy to only observe the addModel-time deletes.
      deleteSpy.mockClear();

      // Mock the POST.
      fetchMock.mockResolvedValueOnce(jsonResponse({ id: 'new', name: 'new', enabled: true, fallback_chain: [] }));
      // Mock the refetch that happens after delete.
      fetchMock.mockResolvedValueOnce(jsonResponse([]));

      await act(async () => {
        await result.current.addModel({
          id: 'new',
          name: 'new',
          enabled: true,
          fallback_chain: [],
        });
      });

      assertAllThreeKeysDeleted(deleteSpy);

      deleteSpy.mockRestore();
    });

    it('updateModel deletes all three keys (amendment 9d)', async () => {
      const deleteSpy = vi.spyOn(defaultAPICache, 'delete');

      fetchMock.mockResolvedValueOnce(jsonResponse([
        { id: 'decision-1', name: 'D1', enabled: true, fallback_chain: [], kind: 'decision' },
      ]));
      const { result } = renderHook(() => useDecisionModels());
      await waitFor(() => expect(result.current.loading).toBe(false));

      deleteSpy.mockClear();

      // PUT response.
      fetchMock.mockResolvedValueOnce(jsonResponse({ id: 'decision-1', name: 'D1-updated', enabled: true, fallback_chain: [], kind: 'decision' }));
      // Refetch after invalidation.
      fetchMock.mockResolvedValueOnce(jsonResponse([
        { id: 'decision-1', name: 'D1-updated', enabled: true, fallback_chain: [], kind: 'decision' },
      ]));

      await act(async () => {
        await result.current.updateModel('decision-1', { name: 'D1-updated' });
      });

      assertAllThreeKeysDeleted(deleteSpy);

      deleteSpy.mockRestore();
    });

    it('deleteModel deletes all three keys (amendment 9d)', async () => {
      const deleteSpy = vi.spyOn(defaultAPICache, 'delete');

      fetchMock.mockResolvedValueOnce(jsonResponse([
        { id: 'decision-1', name: 'D1', enabled: true, fallback_chain: [], kind: 'decision' },
      ]));
      const { result } = renderHook(() => useDecisionModels());
      await waitFor(() => expect(result.current.loading).toBe(false));

      deleteSpy.mockClear();

      // DELETE response (204).
      fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));
      // Refetch.
      fetchMock.mockResolvedValueOnce(jsonResponse([]));

      await act(async () => {
        await result.current.deleteModel('decision-1');
      });

      assertAllThreeKeysDeleted(deleteSpy);

      deleteSpy.mockRestore();
    });
  });
});
