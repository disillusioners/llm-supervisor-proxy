import { useState, useEffect, useCallback, useRef } from 'preact/hooks';
import type { RequestListItem, RequestDetail, AppConfig, ConfigUpdateResponse, Model, ApiToken, Credential, Provider, UsageResponse, UsageToken, UsageSummary, ModelUsageResponse, MCPServer, MCPServerStatus, MCPServerTestResult, CreateMCPServerRequest } from '../types';
import { defaultAPICache } from '../utils/apiCache';

const API_BASE = '/fe/api';
// Default page size for the metadata-only list endpoint. The list is now
// bounded by count (not bytes), and the backend caps it at 200.
const REQUEST_LIST_LIMIT = 50;

// Generic fetch helper
async function apiFetch<T>(path: string, options?: RequestInit & { signal?: AbortSignal }): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    ...options,
    signal: options?.signal,
    headers: {
      'Content-Type': 'application/json',
      ...options?.headers,
    },
  });
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: 'Request failed' }));
    throw new Error(err.error || 'Request failed');
  }
  // Handle 204 No Content - return empty object for void responses
  if (res.status === 204) {
    return {} as T;
  }
  return res.json();
}

// Helper to check if error is from AbortController
function isAbortError(err: unknown): boolean {
  return err instanceof DOMException && err.name === 'AbortError';
}

// Requests API
//
// Returns a list of metadata-only entries (`RequestListItem`) by default.
// This is a deliberate contract change to keep the list payload small
// (the previous shape carried full message bodies for every entry, which
// dominated RAM and bandwidth). Callers that need the full conversation
// fetch `RequestDetail` via `useRequestDetail(id)`.
//
// The list is cached with a short TTL and served stale-while-revalidate:
//   * `refetch()` no longer pre-deletes the cache entry, so an SSE burst
//     that triggers N refetches within the TTL window only downloads
//     once. The next call after expiry fetches fresh data and updates
//     the cache, but callers always see the previous snapshot instantly.
//   * `patchListEntry(id, partial)` lets SSE handlers splice in updates
//     from event payloads without ever re-downloading the whole list.
export function useRequests(initialAppTag?: string) {
  const [requests, setRequests] = useState<RequestListItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [currentAppTag, setCurrentAppTag] = useState(initialAppTag);
  const [refreshKey, setRefreshKey] = useState(0);

  // Mirror of `requests` for synchronous read in non-render callbacks
  // (e.g. fetchAndPatchById's "is the row already there?" check). The
  // ref avoids a setRequests-as-read hack and is updated every render.
  const requestsRef = useRef<RequestListItem[]>(requests);
  useEffect(() => {
    requestsRef.current = requests;
  }, [requests]);

  useEffect(() => {
    const controller = new AbortController();

    async function fetchRequests() {
      try {
        setLoading(true);
        const tag = initialAppTag !== undefined ? initialAppTag : currentAppTag;
        // Use cache key based on app tag filter
        const cacheKey = tag ? `requests:${tag}` : 'requests';
        const qs = new URLSearchParams();
        qs.set('limit', String(REQUEST_LIST_LIMIT));
        // offset intentionally omitted (= 0) on the initial / cache-keyed path;
        // cache key does not encode offset so pagination stays simple.
        const queryString = qs.toString();
        const data = await defaultAPICache.getOrFetch<RequestListItem[]>(cacheKey, async () => {
          const path = `/requests?${queryString}${tag ? `&app=${encodeURIComponent(tag)}` : ''}`;
          const response = await fetch(`${API_BASE}${path}`, {
            signal: controller.signal,
            headers: { 'Content-Type': 'application/json' },
          });
          if (!response.ok) throw new Error(`HTTP ${response.status}`);
          return response.json() as Promise<RequestListItem[]>;
        }, 5000);
        if (!controller.signal.aborted) {
          setRequests(data || []);
          setError(null);
        }
      } catch (err) {
        if (isAbortError(err)) return;
        setError(err instanceof Error ? err.message : 'Failed to fetch requests');
      } finally {
        if (!controller.signal.aborted) setLoading(false);
      }
    }

    fetchRequests();
    return () => controller.abort();
  }, [currentAppTag, initialAppTag, refreshKey]);

  // SWR refetch: do NOT pre-delete the cache. If the entry is still
  // fresh, getOrFetch returns it instantly without hitting the network.
  // If it's expired, getOrFetch kicks the fetcher in the background;
  // component state updates when fresh data arrives. Either way, the
  // user sees the previous snapshot instantly, no spinner flicker.
  const refetch = useCallback(() => {
    setRefreshKey(k => k + 1);
  }, []);

  // Patch a single list entry by id from an SSE payload. Avoids the full
  // refetch when the event already carries the data we need to update
  // the row (e.g. status transition from running → completed).
  const patchListEntry = useCallback(<K extends keyof RequestListItem>(
    id: string,
    partial: Pick<RequestListItem, K>,
  ) => {
    setRequests(prev => {
      const idx = prev.findIndex(r => r.id === id);
      if (idx === -1) return prev;
      const next = prev.slice();
      next[idx] = { ...next[idx], ...partial };
      return next;
    });
  }, []);

  // Fetch a single request's metadata from the detail endpoint and
  // merge it into the cached list. Used when an SSE event tells us
  // a new request was created but doesn't carry enough data to
  // synthesize the list entry client-side.
  //
  // Optimization: when the row is already in the cached list (e.g.
  // it was inserted by an earlier full list refresh), skip the fetch
  // entirely. Any drift between the row we already have and the
  // backend ground truth is reconciled by the SSE handler's trailing
  // debouncedRefresh() (which uses forceRefetch and so always hits
  // the network). Saves one HTTP roundtrip on the common
  // request_started-during-active-traffic case.
  const fetchAndPatchById = useCallback(async (id: string) => {
    if (requestsRef.current.some(r => r.id === id)) return;
    try {
      const data = await apiFetch<RequestListItem>(`/requests/${id}/summary`);
      setRequests(prev => {
        const idx = prev.findIndex(r => r.id === id);
        if (idx === -1) {
          return [data, ...prev];
        }
        const next = prev.slice();
        next[idx] = { ...next[idx], ...data };
        return next;
      });
    } catch (err) {
      // Fall back to a full refetch on failure so we never miss updates
      // due to a single bad fetch.
      if (isAbortError(err)) return;
      setRefreshKey(k => k + 1);
    }
  }, []);

  // Force-refetch (skip cache). Used after explicit mutations (delete,
  // config change) where stale data is unacceptable. Named distinctly
  // from `refetch` to make call sites self-documenting.
  const forceRefetch = useCallback(() => {
    const tag = initialAppTag !== undefined ? initialAppTag : currentAppTag;
    const cacheKey = tag ? `requests:${tag}` : 'requests';
    defaultAPICache.delete(cacheKey);
    setRefreshKey(k => k + 1);
  }, [currentAppTag, initialAppTag]);

  return {
    requests,
    loading,
    error,
    refetch,
    forceRefetch,
    patchListEntry,
    fetchAndPatchById,
    setAppTag: setCurrentAppTag,
  };
}

export function useRequestDetail(id: string | null) {
  const [detail, setDetail] = useState<RequestDetail | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!id) {
      setDetail(null);
      return;
    }

    const controller = new AbortController();

    async function fetchDetail() {
      try {
        setLoading(true);
        const data = await apiFetch<RequestDetail>(`/requests/${id}`, { signal: controller.signal });
        setDetail(data);
        setError(null);
      } catch (err) {
        if (isAbortError(err)) return;
        setError(err instanceof Error ? err.message : 'Failed to fetch request');
      } finally {
        setLoading(false);
      }
    }

    fetchDetail();
    return () => controller.abort();
  }, [id]);

  return { detail, loading, error };
}

// Config API
export function useConfig() {
  const [config, setConfig] = useState<AppConfig | null>(null);
  const [loading, setLoading] = useState(true);

  const fetchConfig = useCallback(async (signal?: AbortSignal) => {
    try {
      setLoading(true);
      const data = await defaultAPICache.getOrFetch<AppConfig>('config', async () => {
        const response = await fetch(`${API_BASE}/config`, {
          signal,
          headers: { 'Content-Type': 'application/json' },
        });
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        return response.json() as Promise<AppConfig>;
      }, 30000);
      setConfig(data);
    } catch (err) {
      if (isAbortError(err)) return;
      console.error('Failed to fetch config:', err);
    } finally {
      setLoading(false);
    }
  }, []);

  const updateConfig = useCallback(async (updates: Partial<AppConfig>): Promise<ConfigUpdateResponse> => {
    const response = await apiFetch<ConfigUpdateResponse>('/config', {
      method: 'PUT',
      body: JSON.stringify(updates),
    });
    defaultAPICache.delete('config');
    await fetchConfig();
    return response;
  }, [fetchConfig]);

  useEffect(() => {
    const controller = new AbortController();
    fetchConfig(controller.signal);
    return () => controller.abort();
  }, [fetchConfig]);

  return { config, loading, updateConfig, refetch: fetchConfig };
}

// Models API
export function useModels() {
  const [models, setModels] = useState<Model[]>([]);
  const [loading, setLoading] = useState(true);

  const fetchModels = useCallback(async (signal?: AbortSignal) => {
    try {
      setLoading(true);
      const data = await defaultAPICache.getOrFetch<Model[]>('models', async () => {
        const response = await fetch(`${API_BASE}/models`, {
          signal,
          headers: { 'Content-Type': 'application/json' },
        });
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        return response.json() as Promise<Model[]>;
      }, 15000);
      setModels(data || []);
    } catch (err) {
      if (isAbortError(err)) return;
      console.error('Failed to fetch models:', err);
    } finally {
      setLoading(false);
    }
  }, []);

  const addModel = useCallback(async (model: Omit<Model, 'id'> & { id: string }) => {
    await apiFetch<Model>('/models', {
      method: 'POST',
      body: JSON.stringify(model),
    });
    defaultAPICache.delete('models');
    await fetchModels();
  }, [fetchModels]);

  const updateModel = useCallback(async (id: string, updates: Partial<Model>) => {
    const current = models.find(m => m.id === id);
    const merged = { ...current, ...updates, id };
    await apiFetch<Model>(`/models/${id}`, {
      method: 'PUT',
      body: JSON.stringify(merged),
    });
    defaultAPICache.delete('models');
    await fetchModels();
  }, [fetchModels, models]);

  const deleteModel = useCallback(async (id: string) => {
    await apiFetch<void>(`/models/${id}`, { method: 'DELETE' });
    defaultAPICache.delete('models');
    await fetchModels();
  }, [fetchModels]);

  useEffect(() => {
    const controller = new AbortController();
    fetchModels(controller.signal);
    return () => controller.abort();
  }, [fetchModels]);

  return { models, loading, addModel, updateModel, deleteModel, refetch: fetchModels };
}

// Duration formatting utility - backend now accepts string durations directly
export function formatDuration(value: string | number): string {
  if (typeof value === 'number') {
    // Convert nanoseconds to seconds if numeric
    return (value / 1e9) + 's';
  }
  // Add 's' suffix if missing
  if (value && !value.endsWith('s') && !value.endsWith('m') && !value.endsWith('ms')) {
    return value + 's';
  }
  return value;
}

// App Tags API - fetch unique app tags for filtering
export function useAppTags() {
  const [appTags, setAppTags] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    const controller = new AbortController();

    async function fetchAppTags() {
      try {
        setLoading(true);
        const data = await defaultAPICache.getOrFetch<string[]>('app-tags', async () => {
          const response = await fetch(`${API_BASE}/app-tags`, {
            signal: controller.signal,
            headers: { 'Content-Type': 'application/json' },
          });
          if (!response.ok) throw new Error(`HTTP ${response.status}`);
          return response.json() as Promise<string[]>;
        }, 30000);
        setAppTags(data || []);
      } catch (err) {
        if (isAbortError(err)) return;
        console.error('Failed to fetch app tags:', err);
      } finally {
        setLoading(false);
      }
    }

    fetchAppTags();
    return () => controller.abort();
  }, [refreshKey]);

  const refetch = useCallback(() => {
    // Invalidate cache so next refetch gets fresh data
    defaultAPICache.delete('app-tags');
    setRefreshKey(k => k + 1);
  }, []);

  return { appTags, loading, refetch };
}

// Version API
export function useVersion() {
  const [version, setVersion] = useState<string>('dev');
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const controller = new AbortController();

    async function fetchVersion() {
      try {
        setLoading(true);
        const data = await defaultAPICache.getOrFetch<{ version: string }>('version', async () => {
          const response = await fetch(`${API_BASE}/version`, {
            signal: controller.signal,
            headers: { 'Content-Type': 'application/json' },
          });
          if (!response.ok) throw new Error(`HTTP ${response.status}`);
          return response.json() as Promise<{ version: string }>;
        }, 300000);
        setVersion(data.version);
      } catch (err) {
        if (isAbortError(err)) return;
        console.error('Failed to fetch version:', err);
      } finally {
        setLoading(false);
      }
    }

    fetchVersion();
    return () => controller.abort();
  }, []);

  return { version, loading };
}

// RAM API with polling
export function useRam() {
  const [allocMB, setAllocMB] = useState<number>(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    let interval: ReturnType<typeof setInterval> | null = null;
    let isFetching = false;

    const fetchRam = async () => {
      // Don't start new fetch if hidden or already fetching
      if (document.hidden || isFetching) return;

      isFetching = true;
      try {
        const data = await apiFetch<{ alloc_bytes: number; alloc_mb: number }>('/ram', { signal: controller.signal });
        setAllocMB(data.alloc_mb);
        setError(null);
      } catch (err) {
        if (isAbortError(err)) return;
        setError(err instanceof Error ? err.message : 'Failed to fetch RAM');
      } finally {
        setLoading(false);
        isFetching = false;
      }
    };

    const startInterval = () => {
      if (!interval) {
        fetchRam(); // Fetch immediately when starting
        interval = setInterval(fetchRam, 5000);
      }
    };

    const stopInterval = () => {
      if (interval) {
        clearInterval(interval);
        interval = null;
      }
    };

    const handleVisibilityChange = () => {
      if (document.hidden) {
        stopInterval();
      } else {
        startInterval();
      }
    };

    document.addEventListener('visibilitychange', handleVisibilityChange);

    // Start interval if not hidden, otherwise wait for visibility change
    if (!document.hidden) {
      startInterval();
    }

    return () => {
      controller.abort();
      stopInterval();
      document.removeEventListener('visibilitychange', handleVisibilityChange);
    };
  }, []);

  return { allocMB, loading, error };
}

// Tokens API
export function useTokens() {
  const [tokens, setTokens] = useState<ApiToken[]>([]);
  const [loading, setLoading] = useState(true);

  const fetchTokens = useCallback(async (signal?: AbortSignal) => {
    try {
      setLoading(true);
      const data = await defaultAPICache.getOrFetch<ApiToken[]>('tokens', async () => {
        const response = await fetch(`${API_BASE}/tokens`, {
          signal,
          headers: { 'Content-Type': 'application/json' },
        });
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        return response.json() as Promise<ApiToken[]>;
      }, 15000);
      setTokens(data || []);
    } catch (err) {
      if (isAbortError(err)) return;
      console.error('Failed to fetch tokens:', err);
    } finally {
      setLoading(false);
    }
  }, []);

  const createToken = useCallback(async (name: string, expiresAt: string | null, ultimateModelEnabled?: boolean, allowedModels?: string[], ultimateModel?: string): Promise<ApiToken> => {
    const body: Record<string, unknown> = {
      name,
      expires_at: expiresAt,
      ultimate_model_enabled: ultimateModelEnabled,
      allowed_models: allowedModels && allowedModels.length > 0 ? allowedModels : null,
    };
    // Include ultimate_model if provided (even empty string to clear any previous value)
    if (ultimateModel !== undefined) {
      body.ultimate_model = ultimateModel;
    }
    const token = await apiFetch<ApiToken>('/tokens', {
      method: 'POST',
      body: JSON.stringify(body),
    });
    defaultAPICache.delete('tokens');
    await fetchTokens();
    return token;
  }, [fetchTokens]);

  const updateTokenPermission = useCallback(async (id: string, ultimateModelEnabled: boolean, allowedModels?: string[], ultimateModel?: string): Promise<boolean> => {
    try {
      const response = await fetch(`${API_BASE}/tokens/${id}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          ultimate_model_enabled: ultimateModelEnabled,
          allowed_models: allowedModels !== undefined && allowedModels.length > 0 ? allowedModels : null,
          ...(ultimateModel !== undefined && { ultimate_model: ultimateModel }),
        }),
      });
      if (!response.ok) {
        if (response.status === 404) return false;
        const err = await response.json().catch(() => ({ error: 'Request failed' }));
        throw new Error(err.error || 'Request failed');
      }
      setTokens(prev => prev.map(t => t.id === id ? { ...t, ultimate_model_enabled: ultimateModelEnabled, allowed_models: allowedModels !== undefined && allowedModels.length > 0 ? allowedModels : null, ultimate_model: ultimateModel ?? t.ultimate_model } : t));
      // Invalidate cache so next fetch gets fresh data
      defaultAPICache.delete('tokens');
      return true;
    } catch (e) {
      throw e;
    }
  }, []);

  const deleteToken = useCallback(async (id: string) => {
    await apiFetch<void>(`/tokens/${id}`, { method: 'DELETE' });
    defaultAPICache.delete('tokens');
    await fetchTokens();
  }, [fetchTokens]);

  useEffect(() => {
    const controller = new AbortController();
    fetchTokens(controller.signal);
    return () => controller.abort();
  }, [fetchTokens]);

  return { tokens, loading, createToken, updateTokenPermission, deleteToken, refetch: fetchTokens };
}

// Credentials API
export async function getCredentials(): Promise<Credential[]> {
  const res = await fetch('/fe/api/credentials');
  if (!res.ok) throw new Error('Failed to fetch credentials');
  return res.json();
}

export async function createCredential(cred: Credential): Promise<Credential> {
  const res = await fetch('/fe/api/credentials', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(cred),
  });
  if (!res.ok) {
    const err = await res.json();
    throw new Error(err.error || 'Failed to create credential');
  }
  return res.json();
}

export async function updateCredential(id: string, cred: Credential): Promise<Credential> {
  const res = await fetch(`/fe/api/credentials/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(cred),
  });
  if (!res.ok) {
    const err = await res.json();
    throw new Error(err.error || 'Failed to update credential');
  }
  return res.json();
}

export async function deleteCredential(id: string): Promise<void> {
  const res = await fetch(`/fe/api/credentials/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const err = await res.json();
    throw new Error(err.error || 'Failed to delete credential');
  }
}

// Providers API
export function useProviders() {
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const controller = new AbortController();

    async function fetchProviders() {
      try {
        setLoading(true);
        const data = await defaultAPICache.getOrFetch<Provider[]>('providers', async () => {
          const response = await fetch(`${API_BASE}/providers`, {
            signal: controller.signal,
            headers: { 'Content-Type': 'application/json' },
          });
          if (!response.ok) throw new Error(`HTTP ${response.status}`);
          return response.json() as Promise<Provider[]>;
        }, 60000);
        setProviders(data || []);
      } catch (err) {
        if (isAbortError(err)) return;
        console.error('Failed to fetch providers:', err);
      } finally {
        setLoading(false);
      }
    }

    fetchProviders();
    return () => controller.abort();
  }, []);

  return { providers, loading, refetch: () => {/* handled by effect */} };
}

export async function getProviders(): Promise<Provider[]> {
  const res = await fetch('/fe/api/providers');
  if (!res.ok) throw new Error('Failed to fetch providers');
  return res.json();
}

// Usage API
export function useUsage() {
  const [usageData, setUsageData] = useState<UsageResponse | null>(null);
  const [usageTokens, setUsageTokens] = useState<UsageToken[]>([]);
  const [summary, setSummary] = useState<UsageSummary | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const fetchUsage = useCallback(async (params: {
    token_id?: string;
    from?: string;
    to?: string;
    view?: 'hourly' | 'daily';
  }) => {
    try {
      setLoading(true);
      setError(null);
      const qs = new URLSearchParams();
      if (params.token_id) qs.set('token_id', params.token_id);
      if (params.from) qs.set('from', params.from);
      if (params.to) qs.set('to', params.to);
      if (params.view) qs.set('view', params.view);
      const data = await apiFetch<UsageResponse>('/usage?' + qs.toString());
      setUsageData(data);
      return data;
    } catch (e) {
      setUsageData(null);
      setError(e instanceof Error ? e.message : 'Failed to fetch usage');
      return null;
    } finally {
      setLoading(false);
    }
  }, []);

  const fetchTokens = useCallback(async () => {
    try {
      const data = await apiFetch<{ tokens: UsageToken[] }>('/usage/tokens');
      setUsageTokens(data.tokens || []);
    } catch (e) {
      // silently fail - tokens list is not critical
    }
  }, []);

  const fetchSummary = useCallback(async (from?: string, to?: string) => {
    try {
      const qs = new URLSearchParams();
      if (from) qs.set('from', from);
      if (to) qs.set('to', to);
      const data = await apiFetch<UsageSummary>('/usage/summary?' + qs.toString());
      setSummary(data);
      return data;
    } catch (e) {
      setSummary(null);
      setError(e instanceof Error ? e.message : 'Failed to fetch summary');
      return null;
    }
  }, []);

  const fetchModelUsage = useCallback(async (params: {
    from?: string;
    to?: string;
    view?: 'hourly' | 'daily';
  }) => {
    try {
      const qs = new URLSearchParams();
      if (params.from) qs.set('from', params.from);
      if (params.to) qs.set('to', params.to);
      if (params.view) qs.set('view', params.view);
      const data = await apiFetch<ModelUsageResponse>('/usage/models?' + qs.toString());
      return data;
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to fetch model usage');
      return null;
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    const doFetchTokens = async () => {
      try {
        const data = await apiFetch<{ tokens: UsageToken[] }>('/usage/tokens', { signal: controller.signal });
        setUsageTokens(data.tokens || []);
      } catch (err) {
        if (isAbortError(err)) return;
        // silently fail
      }
    };
    doFetchTokens();
    return () => controller.abort();
  }, []);

  return { usageData, usageTokens, summary, loading, error, fetchUsage, fetchTokens, fetchSummary, fetchModelUsage };
}

// MCP Servers API
export function useMCPServers() {
  const [servers, setServers] = useState<MCPServer[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    const controller = new AbortController();

    async function fetchServers() {
      try {
        setLoading(true);
        const data = await defaultAPICache.getOrFetch<MCPServer[]>('mcp-servers', async () => {
          return apiFetch<MCPServer[]>('/mcp-servers', { signal: controller.signal });
        }, 15000);
        setServers(data || []);
        setError(null);
      } catch (err) {
        if (isAbortError(err)) return;
        setError(err instanceof Error ? err.message : 'Failed to fetch MCP servers');
      } finally {
        setLoading(false);
      }
    }

    fetchServers();
    return () => controller.abort();
  }, [refreshKey]);

  const refetch = useCallback(() => {
    defaultAPICache.delete('mcp-servers');
    setRefreshKey(k => k + 1);
  }, []);

  return { servers, loading, error, refetch };
}

export function useMCPStatus() {
  const [status, setStatus] = useState<MCPServerStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();

    async function fetchStatus() {
      try {
        setLoading(true);
        const data = await apiFetch<MCPServerStatus>('/mcp-servers/status', { signal: controller.signal });
        setStatus(data);
        setError(null);
      } catch (err) {
        if (isAbortError(err)) return;
        setError(err instanceof Error ? err.message : 'Failed to fetch MCP status');
        setStatus(null);
      } finally {
        setLoading(false);
      }
    }

    fetchStatus();
    return () => controller.abort();
  }, []);

  return { status, loading, error };
}

export async function createMCPServer(data: CreateMCPServerRequest): Promise<MCPServer> {
  const server = await apiFetch<MCPServer>('/mcp-servers', {
    method: 'POST',
    body: JSON.stringify(data),
  });
  defaultAPICache.delete('mcp-servers');
  return server;
}

export async function updateMCPServer(id: string, data: Partial<MCPServer>): Promise<MCPServer> {
  const server = await apiFetch<MCPServer>(`/mcp-servers/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  });
  defaultAPICache.delete('mcp-servers');
  return server;
}

export async function deleteMCPServer(id: string): Promise<void> {
  await apiFetch<void>(`/mcp-servers/${id}`, { method: 'DELETE' });
  defaultAPICache.delete('mcp-servers');
}

export async function testMCPServer(id: string): Promise<MCPServerTestResult> {
  return apiFetch<MCPServerTestResult>(`/mcp-servers/${id}/test`, { method: 'POST' });
}

export async function testMCPServerDirect(upstreamUrl: string, transportType: string, authType?: string, authToken?: string): Promise<MCPServerTestResult> {
  return apiFetch<MCPServerTestResult>('/mcp-servers/test-connection', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      upstream_url: upstreamUrl,
      transport_type: transportType,
      auth_type: authType,
      auth_token: authToken,
    }),
  });
}
