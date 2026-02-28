import { useState, useEffect } from 'preact/hooks';
import type { Credential } from '../../types';
import { escapeHtml } from '../../utils/helpers';
import { getCredentials, createCredential, updateCredential, deleteCredential } from '../../hooks/useApi';

interface CredentialsTabProps {
  status: { type: 'success' | 'error'; message: string } | null;
  setStatus: (status: { type: 'success' | 'error'; message: string } | null) => void;
}

const PROVIDERS = ['openai', 'anthropic', 'gemini', 'zhipu', 'azure', 'zai', 'minimax'] as const;
type Provider = typeof PROVIDERS[number];

const PROVIDER_DEFAULTS: Record<Provider, string> = {
  openai: 'https://api.openai.com/v1',
  anthropic: 'https://api.anthropic.com',
  gemini: 'https://generativelanguage.googleapis.com',
  zhipu: 'https://open.bigmodel.cn/api/paas/v4',
  azure: '',
  zai: 'https://api.z.ai/api/coding/paas/v4',
  minimax: 'https://api.minimax.io/v1',
};

const providerColors: Record<Provider, string> = {
  openai: 'bg-green-900/50 text-green-300 border-green-800/40',
  anthropic: 'bg-orange-900/50 text-orange-300 border-orange-800/40',
  gemini: 'bg-blue-900/50 text-blue-300 border-blue-800/40',
  zhipu: 'bg-purple-900/50 text-purple-300 border-purple-800/40',
  azure: 'bg-cyan-900/50 text-cyan-300 border-cyan-800/40',
  zai: 'bg-red-900/50 text-red-300 border-red-800/40',
  minimax: 'bg-yellow-900/50 text-yellow-300 border-yellow-800/40',
};

export function CredentialsTab({ status, setStatus }: CredentialsTabProps) {
  const [credentials, setCredentials] = useState<Credential[]>([]);
  const [loading, setLoading] = useState(true);
  const [showForm, setShowForm] = useState(false);
  const [formMode, setFormMode] = useState<'add' | 'edit'>('add');
  const [credentialToEdit, setCredentialToEdit] = useState<Credential | undefined>(undefined);
  const [credentialToDelete, setCredentialToDelete] = useState<Credential | null>(null);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);

  // Fetch credentials on mount
  useEffect(() => {
    const fetchCredentials = async () => {
      try {
        setLoading(true);
        const data = await getCredentials();
        setCredentials(data || []);
      } catch (e) {
        setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to fetch credentials' });
      } finally {
        setLoading(false);
      }
    };
    fetchCredentials();
  }, [setStatus]);

  const handleOpenAdd = () => {
    setCredentialToEdit(undefined);
    setFormMode('add');
    setShowForm(true);
    setStatus(null);
  };

  const handleOpenEdit = (credential: Credential) => {
    setCredentialToEdit(credential);
    setFormMode('edit');
    setShowForm(true);
    setStatus(null);
  };

  const handleSave = async (data: {
    id: string;
    provider: string;
    api_key?: string;
    base_url?: string;
  }) => {
    try {
      setSaving(true);
      setStatus(null);
      if (formMode === 'add') {
        await createCredential({
          id: data.id,
          provider: data.provider,
          api_key: data.api_key,
          base_url: data.base_url,
        });
        setStatus({ type: 'success', message: 'Credential added successfully' });
      } else {
        const updates: Partial<Credential> = {
          provider: data.provider,
          base_url: data.base_url,
        };
        // Only include api_key if it's not empty (to preserve existing key)
        if (data.api_key && data.api_key.trim() !== '') {
          updates.api_key = data.api_key;
        }
        await updateCredential(data.id, updates as Credential);
        setStatus({ type: 'success', message: 'Credential updated successfully' });
      }
      // Refresh credentials
      const refreshedCredentials = await getCredentials();
      setCredentials(refreshedCredentials || []);
      setShowForm(false);
      setCredentialToEdit(undefined);
    } catch (e) {
      setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to save credential' });
    } finally {
      setSaving(false);
    }
  };

  const handleConfirmDelete = async () => {
    if (!credentialToDelete) return;
    try {
      setDeleting(true);
      setStatus(null);
      await deleteCredential(credentialToDelete.id);
      setStatus({ type: 'success', message: 'Credential deleted successfully' });
      // Refresh credentials
      const refreshedCredentials = await getCredentials();
      setCredentials(refreshedCredentials || []);
      setCredentialToDelete(null);
    } catch (e) {
      setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to delete credential' });
    } finally {
      setDeleting(false);
    }
  };

  if (loading) {
    return (
      <div class="flex items-center justify-center p-8">
        <div class="animate-spin w-6 h-6 border-2 border-blue-500 border-t-transparent rounded-full"></div>
        <span class="ml-3 text-gray-400">Loading credentials...</span>
      </div>
    );
  }

  return (
    <div class="space-y-4">
      {!showForm ? (
        <>
          <div class="flex justify-between items-center mb-2">
            <h3 class="text-white font-medium">Credentials</h3>
            <button
              onClick={handleOpenAdd}
              class="bg-blue-600 hover:bg-blue-500 text-white text-sm font-medium py-1.5 px-3 rounded-md transition-colors flex items-center gap-1"
            >
              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 6v6m0 0v6m0-6h6m-6 0H6" />
              </svg>
              Add Credential
            </button>
          </div>

          {/* Credentials List */}
          <div class="space-y-2">
            {credentials.length === 0 ? (
              <div class="bg-gray-700/50 rounded-md p-6 border border-gray-700 border-dashed flex flex-col items-center justify-center">
                <svg class="w-10 h-10 text-gray-500 mb-2" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z" />
                </svg>
                <p class="text-gray-400 text-sm">No credentials configured</p>
                <p class="text-gray-500 text-xs mt-1">Add credentials to authenticate with LLM providers</p>
              </div>
            ) : (
              <div class="overflow-x-auto">
                <table class="w-full">
                  <thead>
                    <tr class="text-left text-xs text-gray-500 uppercase border-b border-gray-700">
                      <th class="pb-2 font-medium">ID</th>
                      <th class="pb-2 font-medium">Provider</th>
                      <th class="pb-2 font-medium">API Key</th>
                      <th class="pb-2 font-medium">Base URL</th>
                      <th class="pb-2 font-medium text-right">Actions</th>
                    </tr>
                  </thead>
                  <tbody class="divide-y divide-gray-700/50">
                    {credentials.map((cred) => (
                      <tr key={cred.id} class="hover:bg-gray-700/30 transition-colors">
                        <td class="py-3 px-2">
                          <span class="text-gray-100 font-mono text-sm bg-gray-800/50 px-2 py-1 rounded">
                            {escapeHtml(cred.id)}
                          </span>
                        </td>
                        <td class="py-3 px-2">
                          <span class={`inline-flex items-center text-xs px-2 py-1 rounded border ${providerColors[cred.provider as Provider] || 'bg-gray-700 text-gray-300 border-gray-600'}`}>
                            {escapeHtml(cred.provider)}
                          </span>
                        </td>
                        <td class="py-3 px-2">
                          <span class="text-gray-400 font-mono text-sm">
                             {cred.api_key ? escapeHtml(cred.api_key) : <span class="text-gray-600 italic">Not set</span>}
                           </span>
                        </td>
                        <td class="py-3 px-2">
                          {cred.base_url ? (
                            <span class="text-gray-400 text-sm truncate max-w-[200px] block" title={cred.base_url}>
                              {escapeHtml(cred.base_url)}
                            </span>
                          ) : (
                            <span class="text-gray-600 italic text-sm">Default</span>
                          )}
                        </td>
                        <td class="py-3 px-2 text-right">
                          <div class="flex items-center justify-end gap-1">
                            <button
                              onClick={() => handleOpenEdit(cred)}
                              class="text-gray-400 hover:text-blue-400 transition-colors p-1.5 rounded-md hover:bg-gray-600/50"
                              title="Edit credential"
                            >
                              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
                              </svg>
                            </button>
                            <button
                              onClick={() => setCredentialToDelete(cred)}
                              class="text-gray-400 hover:text-red-400 transition-colors p-1.5 rounded-md hover:bg-gray-600/50"
                              title="Delete credential"
                            >
                              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                              </svg>
                            </button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </>
      ) : (
        <CredentialForm
          mode={formMode}
          initialData={credentialToEdit}
          onSave={handleSave}
          onCancel={() => {
            setShowForm(false);
            setCredentialToEdit(undefined);
          }}
          saving={saving}
        />
      )}

      {/* Delete Confirmation Dialog */}
      {credentialToDelete && (
        <div class="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-[60]">
          <div class="bg-gray-800 rounded-lg shadow-2xl max-w-sm w-full mx-4 border border-gray-700 p-6 flex flex-col items-center text-center">
            <div class="w-12 h-12 bg-red-900/30 text-red-400 rounded-full flex items-center justify-center mb-4 border border-red-800/50">
              <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
              </svg>
            </div>
            <h3 class="text-xl font-semibold text-white mb-2">Delete Credential</h3>
            <p class="text-gray-300 mb-6">
              Are you sure you want to delete <span class="font-semibold text-white">"{credentialToDelete.id}"</span>? This action cannot be undone.
            </p>
            <div class="flex gap-3 w-full">
              <button
                onClick={() => setCredentialToDelete(null)}
                class="flex-1 px-4 py-2.5 bg-gray-700 hover:bg-gray-600 text-white rounded-lg transition-colors font-medium border border-gray-600"
                disabled={deleting}
              >
                Cancel
              </button>
              <button
                onClick={handleConfirmDelete}
                class="flex-1 px-4 py-2.5 bg-red-600 hover:bg-red-500 text-white rounded-lg transition-colors font-medium border border-red-500/50 shadow shadow-red-900/20"
                disabled={deleting}
              >
                {deleting ? 'Deleting...' : 'Delete'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

interface CredentialFormProps {
  mode: 'add' | 'edit';
  initialData?: Credential;
  onSave: (data: {
    id: string;
    provider: string;
    api_key?: string;
    base_url?: string;
  }) => Promise<void>;
  onCancel: () => void;
  saving: boolean;
}

function CredentialForm({ mode, initialData, onSave, onCancel, saving }: CredentialFormProps) {
  const [id, setId] = useState(initialData?.id || '');
  const [provider, setProvider] = useState<Provider>(initialData?.provider as Provider || 'openai');
  const [apiKey, setApiKey] = useState('');
  const [baseUrl, setBaseUrl] = useState(initialData?.base_url || '');

  // Prefill base URL when provider changes (only in add mode or if empty)
  useEffect(() => {
    if (mode === 'add') {
      setBaseUrl(PROVIDER_DEFAULTS[provider] || '');
    }
  }, [provider, mode]);

  const handleProviderChange = (newProvider: Provider) => {
    setProvider(newProvider);
    // Update base URL when provider changes
    setBaseUrl(PROVIDER_DEFAULTS[newProvider] || '');
  };

  const handleSubmit = async (e: Event) => {
    e.preventDefault();
    await onSave({
      id,
      provider,
      api_key: apiKey,
      base_url: baseUrl || undefined,
    });
  };

  return (
    <div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
      <h3 class="text-lg font-semibold text-white mb-4">
        {mode === 'add' ? 'Add Credential' : 'Edit Credential'}
      </h3>
      <form onSubmit={handleSubmit} class="space-y-4">
        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">Credential ID</label>
          <input
            type="text"
            value={id}
            onInput={(e) => setId((e.target as HTMLInputElement).value)}
            disabled={mode === 'edit'}
            required
            class={`w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent ${mode === 'edit' ? 'opacity-60 cursor-not-allowed' : ''}`}
            placeholder="e.g., openai-main"
          />
          {mode === 'edit' && (
            <p class="text-xs text-gray-500 mt-1">ID cannot be changed</p>
          )}
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">Provider</label>
          <select
            value={provider}
            onChange={(e) => handleProviderChange((e.target as HTMLSelectElement).value as Provider)}
            required
            class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
          >
            {PROVIDERS.map((p) => (
              <option key={p} value={p}>
                {p.charAt(0).toUpperCase() + p.slice(1)}
              </option>
            ))}
          </select>
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">
            API Key {mode === 'edit' && <span class="text-gray-500 font-normal">(optional)</span>}
          </label>
          <input
            type="password"
            value={apiKey}
            onInput={(e) => setApiKey((e.target as HTMLInputElement).value)}
            required={mode === 'add'}
            class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            placeholder={mode === 'add' ? '${MY_API_KEY} or actual key' : 'Leave empty to keep existing key'}
          />
          {mode === 'edit' && (
            <p class="text-xs text-gray-500 mt-1">Leave empty to keep existing key</p>
          )}
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">
            Base URL <span class="text-gray-500 font-normal">(optional)</span>
          </label>
          <input
            type="text"
            value={baseUrl}
            onInput={(e) => setBaseUrl((e.target as HTMLInputElement).value)}
            class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            placeholder={PROVIDER_DEFAULTS[provider] || 'Custom endpoint URL'}
          />
          {PROVIDER_DEFAULTS[provider] && (
            <p class="text-xs text-gray-500 mt-1">
              Default: <span class="text-gray-400">{PROVIDER_DEFAULTS[provider]}</span>
            </p>
          )}
        </div>

        <div class="flex gap-3 pt-2">
          <button
            type="button"
            onClick={onCancel}
            class="flex-1 px-4 py-2.5 bg-gray-700 hover:bg-gray-600 text-white rounded-lg transition-colors font-medium border border-gray-600"
            disabled={saving}
          >
            Cancel
          </button>
          <button
            type="submit"
            class="flex-1 px-4 py-2.5 bg-blue-600 hover:bg-blue-500 text-white rounded-lg transition-colors font-medium border border-blue-500/50 shadow shadow-blue-900/20"
            disabled={saving || !id || (mode === 'add' && !apiKey)}
          >
            {saving ? 'Saving...' : mode === 'add' ? 'Add Credential' : 'Save Changes'}
          </button>
        </div>
      </form>
    </div>
  );
}
