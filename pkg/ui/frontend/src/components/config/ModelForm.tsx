import { useState, useEffect } from 'preact/hooks';
import type { Model, InternalProvider } from '../../types';

interface ModelFormProps {
  mode: 'add' | 'edit';
  initialData?: Model;
  onSave: (data: {
    id: string;
    name: string;
    fallback_chain: string[];
    truncate_params: string[];
    internal?: boolean;
    internal_provider?: InternalProvider;
    internal_api_key?: string;
    internal_base_url?: string;
    internal_model?: string;
  }) => Promise<void>;
  onCancel: () => void;
  onStatus: (status: { type: 'success' | 'error'; message: string } | null) => void;
}

const PROVIDER_DEFAULTS: Record<InternalProvider, string> = {
  openai: 'https://api.openai.com/v1',
  zhipu: 'https://open.bigmodel.cn/api/paas/v4',
  azure: '',
  zai: 'https://api.z.ai/api/coding/paas/v4',
  minimax: 'https://api.minimax.io/v1',
};

export function ModelForm({ mode, initialData, onSave, onCancel, onStatus }: ModelFormProps) {
  const [formData, setFormData] = useState({
    id: '',
    name: '',
    fallback_chain: '',
    truncate_params: '',
    internal: false,
    internal_provider: 'openai' as InternalProvider,
    internal_api_key: '',
    internal_base_url: '',
    internal_model: '',
  });
  const [saving, setSaving] = useState(false);

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
        internal_provider: initialData.internal_provider ?? 'openai',
        internal_api_key: '',
        internal_base_url: initialData.internal_base_url ?? '',
        internal_model: initialData.internal_model ?? '',
      });
    } else if (mode === 'add') {
      setFormData({
        id: '',
        name: '',
        fallback_chain: '',
        truncate_params: '',
        internal: false,
        internal_provider: 'openai',
        internal_api_key: '',
        internal_base_url: '',
        internal_model: '',
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
      
      // Auto-set base URL when provider changes
      if (field === 'internal_provider' && typeof value === 'string') {
        updated.internal_base_url = PROVIDER_DEFAULTS[value as InternalProvider] || '';
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
        internal_provider: formData.internal ? formData.internal_provider : undefined,
        internal_api_key: formData.internal && formData.internal_api_key ? formData.internal_api_key : undefined,
        internal_base_url: formData.internal && formData.internal_base_url ? formData.internal_base_url : undefined,
        internal_model: formData.internal && formData.internal_model ? formData.internal_model : undefined,
      });
    } catch (e) {
      onStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to save model' });
    } finally {
      setSaving(false);
    }
  };

  const isValid = mode === 'add' 
    ? formData.id.trim() !== '' && formData.name.trim() !== ''
    : formData.name.trim() !== '';

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
          <label class="block text-sm font-medium text-gray-300 mb-1">Strip Parameters (comma-separated)</label>
          <input
            type="text"
            value={formData.truncate_params}
            onInput={(e) => handleInputChange('truncate_params', (e.target as HTMLInputElement).value)}
            class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-orange-500 focus:border-transparent transition-shadow"
            placeholder="e.g., max_completion_tokens, store"
          />
          <p class="text-xs text-gray-400 mt-1">Parameters to remove before forwarding to this model (e.g. unsupported OpenAI params).</p>
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
              <label class="block text-sm font-medium text-gray-300 mb-1">Provider</label>
              <select
                value={formData.internal_provider}
                onChange={(e) => handleInputChange('internal_provider', (e.target as HTMLSelectElement).value)}
                class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
              >
                <option value="openai">OpenAI</option>
                <option value="zhipu">Zhipu (智谱)</option>
                <option value="azure">Azure OpenAI</option>
                <option value="zai">ZAI</option>
                <option value="minimax">MiniMax</option>
              </select>
            </div>

            <div>
              <label class="block text-sm font-medium text-gray-300 mb-1">
                API Key {mode === 'edit' && <span class="text-gray-500 text-xs">(leave empty to keep existing)</span>}
              </label>
              <input
                type="password"
                value={formData.internal_api_key}
                onInput={(e) => handleInputChange('internal_api_key', (e.target as HTMLInputElement).value)}
                class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                placeholder={mode === 'edit' ? '••••••••' : 'sk-...'}
              />
            </div>

            <div>
              <label class="block text-sm font-medium text-gray-300 mb-1">Base URL <span class="text-gray-500">(optional)</span></label>
              <input
                type="text"
                value={formData.internal_base_url}
                onInput={(e) => handleInputChange('internal_base_url', (e.target as HTMLInputElement).value)}
                class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                placeholder={PROVIDER_DEFAULTS[formData.internal_provider]}
              />
              <p class="text-xs text-gray-400 mt-1">Defaults to: {PROVIDER_DEFAULTS[formData.internal_provider]}</p>
            </div>

            <div>
              <label class="block text-sm font-medium text-gray-300 mb-1">Internal Model Name <span class="text-red-400">*</span></label>
              <input
                type="text"
                value={formData.internal_model}
                onInput={(e) => handleInputChange('internal_model', (e.target as HTMLInputElement).value)}
                class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                placeholder={formData.internal_provider === 'openai' ? 'gpt-4o' : formData.internal_provider === 'zhipu' ? 'glm-4' : 'gpt-4'}
              />
              <p class="text-xs text-gray-400 mt-1">Actual model name at the provider (e.g., "gpt-4o" for OpenAI)</p>
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
