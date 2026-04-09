import { useState, useEffect, useCallback } from 'preact/hooks';
import type { Request, RequestDetail, AppConfig, ConfigUpdateResponse, Model, ApiToken, Credential, Provider, UsageResponse, UsageToken, UsageSummary } from '../types';
import { defaultAPICache } from '../utils/apiCache';

const API_BASE = '/fe/api';

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
export function useRequests(initialAppTag?: string) {
  const [requests, setRequests] = useState<Request[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [currentAppTag, setCurrentAppTag] = useState(initialAppTag);
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    const controller = new AbortController();

    async function fetchRequests() {
      try {
        setLoading(true);
        const tag = initialAppTag !== undefined ? initialAppTag : currentAppTag;
        const url = tag ? `/requests?app=${encodeURIComponent(tag)}` : '/requests';
        const data = await apiFetch<Request[]>(url, { signal: controller.signal });
        setRequests(data);
        setError(null);
      } catch (err) {
        if (isAbortError(err)) return;
        setError(err instanceof Error ? err.message : 'Failed to fetch requests');
      } finally {
        setLoading(false);
      }
    }

    fetchRequests();
    return () => controller.abort();
  }, [currentAppTag, initialAppTag, refreshKey]);

  const refetch = useCallback(() => {
    setRefreshKey(k => k + 1);
  }, []);

  return { requests, loading, error, refetch, setAppTag: setCurrentAppTag };
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

  const createToken = useCallback(async (name: string, expiresAt: string | null, ultimateModelEnabled?: boolean): Promise<ApiToken> => {
    const token = await apiFetch<ApiToken>('/tokens', {
      method: 'POST',
      body: JSON.stringify({ name, expires_at: expiresAt, ultimate_model_enabled: ultimateModelEnabled }),
    });
    defaultAPICache.delete('tokens');
    await fetchTokens();
    return token;
  }, [fetchTokens]);

  const updateTokenPermission = useCallback(async (id: string, ultimateModelEnabled: boolean): Promise<boolean> => {
    try {
      const response = await fetch(`${API_BASE}/tokens/${id}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ultimate_model_enabled: ultimateModelEnabled }),
      });
      if (!response.ok) {
        if (response.status === 404) return false;
        const err = await response.json().catch(() => ({ error: 'Request failed' }));
        throw new Error(err.error || 'Request failed');
      }
      setTokens(prev => prev.map(t => t.id === id ? { ...t, ultimate_model_enabled: ultimateModelEnabled } : t));
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

  return { usageData, usageTokens, summary, loading, error, fetchUsage, fetchTokens, fetchSummary };
}
