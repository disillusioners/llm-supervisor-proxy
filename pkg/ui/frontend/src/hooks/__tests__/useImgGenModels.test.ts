import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { renderHook, waitFor, act, cleanup } from '@testing-library/preact';
import { useImgGenModels } from '../useApi';
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

describe('useImgGenModels — hook contract (fe-spec §2.3)', () => {
  describe('fetch shape (C-06)', () => {
    it('hits /fe/api/models?kind=image-gen on first call', async () => {
      fetchMock.mockResolvedValue(jsonResponse([]));
      const { result } = renderHook(() => useImgGenModels());

      await waitFor(() => expect(result.current.loading).toBe(false));

      expect(fetchMock).toHaveBeenCalledTimes(1);
      const url = fetchMock.mock.calls[0][0] as string;
      expect(url).toContain('/fe/api/models');
      expect(url).toContain('kind=image-gen');
    });

    it('parses the response into the returned models array', async () => {
      const rows = [
        { id: 'a', name: 'A', enabled: true, fallback_chain: [], kind: 'image-gen' },
        { id: 'b', name: 'B', enabled: false, fallback_chain: [], kind: 'image-gen' },
      ];
      fetchMock.mockResolvedValue(jsonResponse(rows));
      const { result } = renderHook(() => useImgGenModels());

      await waitFor(() => expect(result.current.loading).toBe(false));

      expect(result.current.models).toEqual(rows);
    });
  });

  describe('cache key (C-06, C-07)', () => {
    it('uses cache key "imggen-models" — a second render without mutation does not re-fetch', async () => {
      fetchMock.mockResolvedValue(jsonResponse([]));
      const { result, rerender } = renderHook(() => useImgGenModels());

      await waitFor(() => expect(result.current.loading).toBe(false));
      expect(fetchMock).toHaveBeenCalledTimes(1);

      // Re-render the same hook instance; the cache should serve the same data.
      rerender();
      // Wait a tick for any potential re-fetch to settle.
      await act(async () => {
        await Promise.resolve();
      });
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });

    it('addModel drops BOTH "models" and "imggen-models" cache keys (C-07)', async () => {
      // Spy on defaultAPICache.delete to verify the two key drops.
      const deleteSpy = vi.spyOn(defaultAPICache, 'delete');

      fetchMock.mockResolvedValueOnce(jsonResponse([])); // initial fetch
      const { result } = renderHook(() => useImgGenModels());
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

      // Both keys are deleted at least once during the add flow.
      const deletedKeys = deleteSpy.mock.calls.map(([k]) => k as string);
      expect(deletedKeys).toContain('imggen-models');
      expect(deletedKeys).toContain('models');

      deleteSpy.mockRestore();
    });

    it('updateModel drops BOTH keys (C-07)', async () => {
      const deleteSpy = vi.spyOn(defaultAPICache, 'delete');

      fetchMock.mockResolvedValueOnce(jsonResponse([
        { id: 'img-1', name: 'IMG', enabled: true, fallback_chain: [] },
      ]));
      const { result } = renderHook(() => useImgGenModels());
      await waitFor(() => expect(result.current.loading).toBe(false));

      deleteSpy.mockClear();

      // PUT response.
      fetchMock.mockResolvedValueOnce(jsonResponse({ id: 'img-1', name: 'IMG2', enabled: true, fallback_chain: [] }));
      // Refetch after invalidation.
      fetchMock.mockResolvedValueOnce(jsonResponse([
        { id: 'img-1', name: 'IMG2', enabled: true, fallback_chain: [] },
      ]));

      await act(async () => {
        await result.current.updateModel('img-1', { name: 'IMG2' });
      });

      const deletedKeys = deleteSpy.mock.calls.map(([k]) => k as string);
      expect(deletedKeys).toContain('imggen-models');
      expect(deletedKeys).toContain('models');

      deleteSpy.mockRestore();
    });

    it('deleteModel drops BOTH keys (C-07)', async () => {
      const deleteSpy = vi.spyOn(defaultAPICache, 'delete');

      fetchMock.mockResolvedValueOnce(jsonResponse([
        { id: 'img-1', name: 'IMG', enabled: true, fallback_chain: [] },
      ]));
      const { result } = renderHook(() => useImgGenModels());
      await waitFor(() => expect(result.current.loading).toBe(false));

      deleteSpy.mockClear();

      // DELETE response (204).
      fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));
      // Refetch.
      fetchMock.mockResolvedValueOnce(jsonResponse([]));

      await act(async () => {
        await result.current.deleteModel('img-1');
      });

      const deletedKeys = deleteSpy.mock.calls.map(([k]) => k as string);
      expect(deletedKeys).toContain('imggen-models');
      expect(deletedKeys).toContain('models');

      deleteSpy.mockRestore();
    });
  });
});
