import { useState, useEffect, useCallback } from 'preact/hooks';
import type { Request, RequestDetail, ProxyConfig, Model, ModelsResponse, Event } from '../types';

const API_BASE = '/api';

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
  return res.json();
}

// Requests API
export function useRequests() {
  const [requests, setRequests] = useState<Request[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchRequests = useCallback(async () => {
    try {
      setLoading(true);
      const data = await apiFetch<Request[]>('/requests');
      setRequests(data);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to fetch requests');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchRequests();
  }, [fetchRequests]);

  return { requests, loading, error, refetch: fetchRequests };
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
  const [config, setConfig] = useState<ProxyConfig | null>(null);
  const [loading, setLoading] = useState(true);

  const fetchConfig = useCallback(async () => {
    try {
      setLoading(true);
      const data = await apiFetch<ProxyConfig>('/config');
      setConfig(data);
    } catch (e) {
      console.error('Failed to fetch config:', e);
    } finally {
      setLoading(false);
    }
  }, []);

  const updateConfig = useCallback(async (updates: Partial<ProxyConfig>) => {
    await apiFetch<ProxyConfig>('/config', {
      method: 'POST',
      body: JSON.stringify(updates),
    });
    await fetchConfig();
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
      const data = await apiFetch<ModelsResponse>('/models');
      setModels(data.models || []);
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
    await apiFetch<Model>(`/models/${id}`, {
      method: 'PUT',
      body: JSON.stringify(updates),
    });
    await fetchModels();
  }, [fetchModels]);

  const deleteModel = useCallback(async (id: string) => {
    await apiFetch<void>(`/models/${id}`, { method: 'DELETE' });
    await fetchModels();
  }, [fetchModels]);

  useEffect(() => {
    fetchModels();
  }, [fetchModels]);

  return { models, loading, addModel, updateModel, deleteModel, refetch: fetchModels };
}

// Duration parsing utility
export function parseDuration(s: string): number {
  let mult = 1e9;
  let val = s;
  if (val.endsWith('ms')) {
    mult = 1e6;
    val = val.slice(0, -2);
  } else if (val.endsWith('s')) {
    mult = 1e9;
    val = val.slice(0, -1);
  } else if (val.endsWith('m')) {
    mult = 60 * 1e9;
    val = val.slice(0, -1);
  }
  return Math.floor(parseFloat(val) * mult);
}

export function formatDuration(ns: number): string {
  return ns / 1e9 + 's';
}
