import { useState, useEffect, useCallback } from 'preact/hooks';
import type { Request, RequestDetail, AppConfig, ConfigUpdateResponse, Model, ApiToken, Credential, Provider } from '../types';

const API_BASE = '/fe/api';

// Generic fetch helper
async function apiFetch<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    ...options,
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

// Requests API
export function useRequests(initialAppTag?: string) {
  const [requests, setRequests] = useState<Request[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [currentAppTag, setCurrentAppTag] = useState(initialAppTag);

  const fetchRequests = useCallback(async (overrideTag?: string) => {
    try {
      setLoading(true);
      const tag = overrideTag !== undefined ? overrideTag : currentAppTag;
      const url = tag ? `/requests?app=${encodeURIComponent(tag)}` : '/requests';
      const data = await apiFetch<Request[]>(url);
      setRequests(data);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to fetch requests');
    } finally {
      setLoading(false);
    }
  }, [currentAppTag]);

  useEffect(() => {
    fetchRequests();
  }, [fetchRequests]);

  return { requests, loading, error, refetch: fetchRequests, setAppTag: setCurrentAppTag };
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

    const fetchDetail = async () => {
      try {
        setLoading(true);
        const data = await apiFetch<RequestDetail>(`/requests/${id}`);
        setDetail(data);
        setError(null);
      } catch (e) {
        setError(e instanceof Error ? e.message : 'Failed to fetch request');
      } finally {
        setLoading(false);
      }
    };

    fetchDetail();
  }, [id]);

  return { detail, loading, error };
}

// Config API
export function useConfig() {
  const [config, setConfig] = useState<AppConfig | null>(null);
  const [loading, setLoading] = useState(true);

  const fetchConfig = useCallback(async () => {
    try {
      setLoading(true);
      const data = await apiFetch<AppConfig>('/config');
      setConfig(data);
    } catch (e) {
      console.error('Failed to fetch config:', e);
    } finally {
      setLoading(false);
    }
  }, []);

  const updateConfig = useCallback(async (updates: Partial<AppConfig>): Promise<ConfigUpdateResponse> => {
    const response = await apiFetch<ConfigUpdateResponse>('/config', {
      method: 'PUT',
      body: JSON.stringify(updates),
    });
    await fetchConfig();
    return response;
  }, [fetchConfig]);

  useEffect(() => {
    fetchConfig();
  }, [fetchConfig]);

  return { config, loading, updateConfig, refetch: fetchConfig };
}

// Models API
export function useModels() {
  const [models, setModels] = useState<Model[]>([]);
  const [loading, setLoading] = useState(true);

  const fetchModels = useCallback(async () => {
    try {
      setLoading(true);
      const data = await apiFetch<Model[]>('/models');
      setModels(data || []);
    } catch (e) {
      console.error('Failed to fetch models:', e);
    } finally {
      setLoading(false);
    }
  }, []);

  const addModel = useCallback(async (model: Omit<Model, 'id'> & { id: string }) => {
    await apiFetch<Model>('/models', {
      method: 'POST',
      body: JSON.stringify(model),
    });
    await fetchModels();
  }, [fetchModels]);

  const updateModel = useCallback(async (id: string, updates: Partial<Model>) => {
    // Merge with the current model to ensure all required fields (e.g. `name`) are always present,
    // even for partial updates like toggling `enabled`.
    const current = models.find(m => m.id === id);
    const merged = { ...current, ...updates, id };
    await apiFetch<Model>(`/models/${id}`, {
      method: 'PUT',
      body: JSON.stringify(merged),
    });
    await fetchModels();
  }, [fetchModels, models]);

  const deleteModel = useCallback(async (id: string) => {
    await apiFetch<void>(`/models/${id}`, { method: 'DELETE' });
    await fetchModels();
  }, [fetchModels]);

  useEffect(() => {
    fetchModels();
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

  const fetchAppTags = useCallback(async () => {
    try {
      setLoading(true);
      const data = await apiFetch<string[]>('/app-tags');
      setAppTags(data || []);
    } catch (e) {
      console.error('Failed to fetch app tags:', e);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchAppTags();
  }, []);

  return { appTags, loading, refetch: fetchAppTags };
}

// Version API
export function useVersion() {
  const [version, setVersion] = useState<string>('dev');
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const fetchVersion = async () => {
      try {
        setLoading(true);
        const data = await apiFetch<{ version: string }>('/version');
        setVersion(data.version);
      } catch (e) {
        console.error('Failed to fetch version:', e);
      } finally {
        setLoading(false);
      }
    };

    fetchVersion();
  }, []);

  return { version, loading };
}

// RAM API with polling
export function useRam() {
  const [allocMB, setAllocMB] = useState<number>(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchRam = useCallback(async () => {
    try {
      const data = await apiFetch<{ alloc_bytes: number; alloc_mb: number }>('/ram');
      setAllocMB(data.alloc_mb);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to fetch RAM');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchRam();
    const interval = setInterval(fetchRam, 5000);
    return () => clearInterval(interval);
  }, [fetchRam]);

  return { allocMB, loading, error };
}

// Tokens API
export function useTokens() {
  const [tokens, setTokens] = useState<ApiToken[]>([]);
  const [loading, setLoading] = useState(true);

  const fetchTokens = useCallback(async () => {
    try {
      setLoading(true);
      const data = await apiFetch<ApiToken[]>('/tokens');
      setTokens(data || []);
    } catch (e) {
      console.error('Failed to fetch tokens:', e);
    } finally {
      setLoading(false);
    }
  }, []);

  const createToken = useCallback(async (name: string, expiresAt: string | null): Promise<ApiToken> => {
    const token = await apiFetch<ApiToken>('/tokens', {
      method: 'POST',
      body: JSON.stringify({ name, expires_at: expiresAt }),
    });
    await fetchTokens();
    return token;
  }, [fetchTokens]);

  const deleteToken = useCallback(async (id: string) => {
    await apiFetch<void>(`/tokens/${id}`, { method: 'DELETE' });
    await fetchTokens();
  }, [fetchTokens]);

  useEffect(() => {
    fetchTokens();
  }, [fetchTokens]);

  return { tokens, loading, createToken, deleteToken, refetch: fetchTokens };
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

  const fetchProviders = useCallback(async () => {
    try {
      setLoading(true);
      const data = await apiFetch<Provider[]>('/providers');
      setProviders(data || []);
    } catch (e) {
      console.error('Failed to fetch providers:', e);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchProviders();
  }, [fetchProviders]);

  return { providers, loading, refetch: fetchProviders };
}

export async function getProviders(): Promise<Provider[]> {
  const res = await fetch('/fe/api/providers');
  if (!res.ok) throw new Error('Failed to fetch providers');
  return res.json();
}
