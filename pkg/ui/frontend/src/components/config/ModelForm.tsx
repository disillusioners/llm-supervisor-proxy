import { useState, useEffect } from 'preact/hooks';
import type { Model, Credential } from '../../types';
import { getCredentials } from '../../hooks/useApi';

interface ModelFormProps {
  mode: 'add' | 'edit';
  initialData?: Model;
  onSave: (data: {
    id: string;
    name: string;
    fallback_chain: string[];
    truncate_params: string[];
    internal?: boolean;
    credential_id?: string;
    internal_api_key?: string;
    internal_base_url?: string;
    internal_model?: string;
    release_stream_chunk_deadline?: string;
  }) => Promise<void>;
  onCancel: () => void;
  onStatus: (status: { type: 'success' | 'error'; message: string } | null) => void;
  onNavigateToCredentials?: () => void;
}

const PROVIDER_DISPLAY_NAMES: Record<string, string> = {
  openai: 'OpenAI',
  zhipu: 'Zhipu (智谱)',
  azure: 'Azure OpenAI',
  zai: 'ZAI',
  minimax: 'MiniMax',
};

export function ModelForm({ mode, initialData, onSave, onCancel, onStatus, onNavigateToCredentials }: ModelFormProps) {
  const [formData, setFormData] = useState({
    id: '',
    name: '',
    fallback_chain: '',
    truncate_params: '',
    internal: false,
    credential_id: '',
    internal_api_key: '',
    internal_base_url: '',
    internal_model: '',
    release_stream_chunk_deadline: '',
  });
  const [credentials, setCredentials] = useState<Credential[]>([]);
  const [loadingCredentials, setLoadingCredentials] = useState(false);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);

  // Fetch credentials on mount
  useEffect(() => {
    const fetchCredentials = async () => {
      setLoadingCredentials(true);
      try {
        const data = await getCredentials();
        setCredentials(data || []);
      } catch (e) {
        console.error('Failed to fetch credentials:', e);
      } finally {
        setLoadingCredentials(false);
      }
    };
    fetchCredentials();
  }, []);

  useEffect(() => {
    if (mode === 'edit' && initialData) {
      let truncateParams = (initialData.truncate_params ?? []).join(', ');
      if (truncateParams.trim() === '' && initialData.id.toLowerCase().includes('glm')) {
        truncateParams = 'max_completion_tokens, store';
      }

      setFormData({
        id: initialData.id,
        name: initialData.name,
        fallback_chain: initialData.fallback_chain.join(', '),
        truncate_params: truncateParams,
        internal: initialData.internal ?? false,
        credential_id: initialData.credential_id ?? '',
        internal_api_key: '',
        internal_base_url: initialData.internal_base_url || '',
        internal_model: initialData.internal_model ?? '',
        release_stream_chunk_deadline: initialData.release_stream_chunk_deadline ?? '',
      });
    } else if (mode === 'add') {
      setFormData({
        id: '',
        name: '',
        fallback_chain: '',
        truncate_params: '',
        internal: false,
        credential_id: '',
        internal_api_key: '',
        internal_base_url: '',
        internal_model: '',
        release_stream_chunk_deadline: '',
      });
    }
  }, [mode, initialData]);

  const handleInputChange = (field: string, value: string | boolean) => {
    setFormData(prev => {
      const updated = { ...prev, [field]: value };
      
      // Auto-fill truncate_params for GLM models when adding
      if (field === 'id' && mode === 'add' && typeof value === 'string') {
        if (value.toLowerCase().includes('glm') && prev.truncate_params.trim() === '') {
          updated.truncate_params = 'max_completion_tokens, store';
        }
      }
      
      // Auto-fill truncate_params for GLM when name changes
      if (field === 'name' && typeof value === 'string') {
        if (value.toLowerCase().includes('glm') && prev.truncate_params.trim() === '') {
          updated.truncate_params = 'max_completion_tokens, store';
        }
      }
      
      // Clear base URL override when credential changes
      if (field === 'credential_id') {
        updated.internal_base_url = '';
      }

      // Clear credential when toggling internal off
      if (field === 'internal' && value === false) {
        updated.credential_id = '';
        updated.internal_api_key = '';
      }
      
      return updated;
    });
  };

  const handleSubmit = async () => {
    try {
      setSaving(true);
      onStatus(null);
      
      const fallback = formData.fallback_chain.split(',').map(s => s.trim()).filter(Boolean);
      const truncate = formData.truncate_params.split(',').map(s => s.trim()).filter(Boolean);

      if (mode === 'add') {
        if (!formData.id || !formData.name) {
          throw new Error('ID and Name are required');
        }
      } else {
        if (!formData.name) {
          throw new Error('Name is required');
        }
      }

      await onSave({
        id: formData.id,
        name: formData.name,
        fallback_chain: fallback,
        truncate_params: truncate,
        internal: formData.internal || undefined,
        credential_id: formData.internal && formData.credential_id ? formData.credential_id : undefined,
        internal_api_key: formData.internal && formData.internal_api_key ? formData.internal_api_key : undefined,
        internal_base_url: formData.internal && formData.internal_base_url ? formData.internal_base_url : undefined,
        internal_model: formData.internal && formData.internal_model ? formData.internal_model : undefined,
        release_stream_chunk_deadline: formData.release_stream_chunk_deadline || undefined,
      });
    } catch (e) {
      onStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to save model' });
    } finally {
      setSaving(false);
    }
  };

  const handleTestConnection = async () => {
    try {
      setTesting(true);
      onStatus(null);

      // Get provider from selected credential
      const selectedCred = credentials.find(c => c.id === formData.credential_id);
      const baseUrl = formData.internal_base_url || selectedCred?.base_url;

      const payload: Record<string, string | undefined> = {
        credential_id: formData.credential_id || undefined,
        api_key: formData.internal_api_key || undefined,
        internal_base_url: baseUrl,
        internal_model: formData.internal_model,
      };

      // Note: We don't pass model_id anymore because we want to test with the
      // current form data, not the saved model data. This allows testing
      // internal config before saving the model.

      const response = await fetch('/fe/api/models/test', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(payload),
      });

      if (!response.ok) {
        const errorData = await response.json().catch(() => ({}));
        throw new Error(errorData.error || `HTTP ${response.status}: Connection test failed`);
      }

      onStatus({ type: 'success', message: 'Connection test successful!' });
    } catch (e) {
      onStatus({ type: 'error', message: e instanceof Error ? e.message : 'Connection test failed' });
    } finally {
      setTesting(false);
    }
  };

  // Can test if: need credential (or API key) and model name
  const canTestConnection = formData.internal_model && 
    (mode === 'edit' || formData.credential_id || formData.internal_api_key);

  const isValid = mode === 'add' 
    ? formData.id.trim() !== '' && formData.name.trim() !== ''
    : formData.name.trim() !== '';

  // Get the currently selected credential
  const selectedCredential = credentials.find(cred => cred.id === formData.credential_id);

  // Compute the default base URL (from credential)
  const defaultBaseUrl = selectedCredential?.base_url || 'Provider default';

  // Get provider display name from selected credential
  const selectedProvider = selectedCredential?.provider;

  return (
    <div class="bg-gray-700/50 rounded-lg p-5 border border-gray-600">
      <h3 class="text-lg font-medium text-white mb-4">
        {mode === 'add' ? 'Add New Model' : 'Edit Model'}
      </h3>
      <div class="space-y-4">
        {mode === 'add' && (
          <div>
            <label class="block text-sm font-medium text-gray-300 mb-1">Model ID <span class="text-red-400">*</span></label>
            <input
              type="text"
              value={formData.id}
              onInput={(e) => handleInputChange('id', (e.target as HTMLInputElement).value)}
              class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
              placeholder="e.g., gpt-4"
            />
          </div>
        )}
        
        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">Display Name <span class="text-red-400">*</span></label>
          <input
            type="text"
            value={formData.name}
            onInput={(e) => handleInputChange('name', (e.target as HTMLInputElement).value)}
            class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="e.g., GPT-4"
          />
        </div>
        
        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">Fallback Chain (comma-separated IDs)</label>
          <input
            type="text"
            value={formData.fallback_chain}
            onInput={(e) => handleInputChange('fallback_chain', (e.target as HTMLInputElement).value)}
            class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="e.g., gpt-3.5-turbo, claude-2"
          />
          <p class="text-xs text-gray-400 mt-1">Leave empty for no fallbacks.</p>
        </div>
        
        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">Strip Params <span class="text-gray-500">(optional)</span></label>
          <input
            type="text"
            value={formData.truncate_params}
            onInput={(e) => handleInputChange('truncate_params', (e.target as HTMLInputElement).value)}
            class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-purple-500 focus:border-transparent transition-shadow"
            placeholder="e.g., max_completion_tokens, store"
          />
          <p class="text-xs text-gray-400 mt-1">Parameters to remove before forwarding to this model (e.g. unsupported OpenAI params).</p>
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">Stream Chunk Deadline <span class="text-gray-500">(optional)</span></label>
          <div class="relative">
            <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400">
              <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
              </svg>
            </span>
            <input
              type="text"
              value={formData.release_stream_chunk_deadline}
              onInput={(e) => handleInputChange('release_stream_chunk_deadline', (e.target as HTMLInputElement).value)}
              class="w-full pl-10 pr-4 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
              placeholder="e.g., 1m30s, 2m"
            />
          </div>
          <p class="text-xs text-gray-400 mt-1">
            Time limit for buffering stream chunks before releasing to client. Impro responsiveness for slow connections. Leave empty for no deadline.
          </p>
        </div>

        {/* Internal Upstream Section */}
        <div class="border-t border-gray-600 pt-4">
          <label class="flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={formData.internal}
              onInput={(e) => handleInputChange('internal', (e.target as HTMLInputElement).checked)}
              class="w-4 h-4 rounded border-gray-600 bg-gray-700 text-blue-500 focus:ring-blue-500 focus:ring-offset-gray-800"
            />
            <span class="text-gray-300 font-medium">Internal Upstream</span>
            <span class="text-xs text-gray-500">(use custom provider instead of global upstream)</span>
          </label>
        </div>

        {formData.internal && (
          <div class="bg-gray-800/50 rounded-md p-4 space-y-3 border border-gray-600/50">
            <div>
              <div class="flex items-center justify-between mb-1">
                <label class="block text-sm font-medium text-gray-300">Credential</label>
                {onNavigateToCredentials && (
                  <button
                    type="button"
                    onClick={onNavigateToCredentials}
                    class="text-xs text-blue-400 hover:text-blue-300 transition-colors"
                  >
                    Manage Credentials
                  </button>
                )}
              </div>
              {loadingCredentials ? (
                <div class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-gray-400 text-sm">
                  Loading credentials...
                </div>
              ) : (
                <select
                  value={formData.credential_id}
                  onChange={(e) => handleInputChange('credential_id', (e.target as HTMLSelectElement).value)}
                  class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                >
                  <option value="">Select a credential</option>
                  {credentials.map((cred) => (
                    <option key={cred.id} value={cred.id}>
                      {cred.id} ({cred.provider || 'unknown'})
                    </option>
                  ))}
                </select>
              )}
              {credentials.length === 0 && !loadingCredentials && (
                <p class="text-xs text-gray-400 mt-1">
                  No credentials found. 
                  {onNavigateToCredentials && (
                    <button
                      type="button"
                      onClick={onNavigateToCredentials}
                      class="text-blue-400 hover:text-blue-300 ml-1"
                    >
                      Add a credential
                    </button>
                  )}
                </p>
              )}
              {selectedProvider && (
                <div class="mt-2 flex items-center gap-2">
                  <span class="text-xs text-gray-400">Provider:</span>
                  <span class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-blue-900/50 text-blue-300 border border-blue-700">
                    {PROVIDER_DISPLAY_NAMES[selectedProvider] || selectedProvider}
                  </span>
                </div>
              )}
              {selectedCredential?.base_url && (
                <p class="text-xs text-gray-500 mt-1">
                  Credential base URL: <span class="text-gray-400">{selectedCredential.base_url}</span>
                </p>
              )}
            </div>

            {formData.credential_id && (
              <div>
                <label class="block text-sm font-medium text-gray-300 mb-1">
                  API Key Override <span class="text-gray-500">(optional)</span>
                </label>
                <input
                  type="password"
                  value={formData.internal_api_key}
                  onInput={(e) => handleInputChange('internal_api_key', (e.target as HTMLInputElement).value)}
                  class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                  placeholder="Overrides the credential's API key if set"
                />
                <p class="text-xs text-gray-400 mt-1">Overrides the credential's API key if set</p>
              </div>
            )}

            <div>
              <label class="block text-sm font-medium text-gray-300 mb-1">Base URL Override <span class="text-gray-500">(optional)</span></label>
              <input
                type="text"
                value={formData.internal_base_url}
                onInput={(e) => handleInputChange('internal_base_url', (e.target as HTMLInputElement).value)}
                class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                placeholder={defaultBaseUrl}
              />
              <p class="text-xs text-gray-400 mt-1">Default: {defaultBaseUrl}</p>
            </div>

            <div>
              <label class="block text-sm font-medium text-gray-300 mb-1">Upstream Model Name <span class="text-red-400">*</span></label>
              <input
                type="text"
                value={formData.internal_model}
                onInput={(e) => handleInputChange('internal_model', (e.target as HTMLInputElement).value)}
                class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                placeholder={selectedProvider === 'openai' ? 'gpt-4o' : selectedProvider === 'zhipu' ? 'glm-4' : 'gpt-4'}
              />
              <p class="text-xs text-gray-400 mt-1">Actual model name at the provider (e.g., "gpt-4o" for OpenAI)</p>
            </div>

            <div class="pt-2">
              <button
                onClick={handleTestConnection}
                class="px-4 py-2 bg-green-600 hover:bg-green-500 text-white rounded-md transition-colors text-sm font-medium"
                disabled={!canTestConnection || testing}
              >
                {testing ? 'Testing...' : 'Test Connection'}
              </button>
            </div>
          </div>
        )}

        <div class="flex justify-end gap-3 pt-2">
          <button
            onClick={onCancel}
            class="px-4 py-2 bg-gray-600 hover:bg-gray-500 text-white rounded-md transition-colors text-sm font-medium"
          >
            Cancel
          </button>
          <button
            onClick={handleSubmit}
            class="px-4 py-2 bg-blue-600 hover:bg-blue-500 text-white rounded-md transition-colors text-sm font-medium"
            disabled={!isValid || saving}
          >
            {saving ? 'Saving...' : mode === 'add' ? 'Add Model' : 'Save Changes'}
          </button>
        </div>
      </div>
    </div>
  );
}
