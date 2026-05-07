import { useState } from 'preact/hooks';
import { ModelMultiSelect } from './ModelMultiSelect';
import type { ApiToken } from '../../types';

interface TokenFormProps {
  models: { name: string; id: string }[];
  token?: ApiToken;  // For editing existing tokens
  onSubmit: (name: string, expiresAt: string | null, ultimateModelEnabled?: boolean, allowedModels?: string[], ultimateModel?: string) => Promise<void>;
  onCancel: () => void;
  onStatus: (status: { type: 'success' | 'error'; message: string } | null) => void;
}

export function TokenForm({ models, token, onSubmit, onCancel, onStatus }: TokenFormProps) {
  const [name, setName] = useState(token?.name || '');
  const [expiresAt, setExpiresAt] = useState(() => {
    if (token?.expires_at) {
      return token.expires_at.split('T')[0]; // Extract date part only
    }
    return '';
  });
  const [ultimateModelEnabled, setUltimateModelEnabled] = useState(token?.ultimate_model_enabled || false);
  const [ultimateModel, setUltimateModel] = useState(token?.ultimate_model || '');
  const [selectedModels, setSelectedModels] = useState<string[]>(token?.allowed_models || []);
  const [saving, setSaving] = useState(false);

  const isEditing = !!token;

  const handleSubmit = async () => {
    if (!name.trim()) {
      onStatus({ type: 'error', message: 'Token name is required' });
      return;
    }

    try {
      setSaving(true);
      onStatus(null);
      const expires = expiresAt ? new Date(expiresAt).toISOString() : null;
      // When selectedModels is empty, it means all models are allowed
      // When ultimateModelEnabled is false, clear ultimateModel to ""
      const ultimateModelValue = ultimateModelEnabled ? ultimateModel : '';
      await onSubmit(name.trim(), expires, ultimateModelEnabled, selectedModels, ultimateModelValue);
    } catch (e) {
      onStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to save token' });
    } finally {
      setSaving(false);
    }
  };

  // Calculate min date (today) for the date input
  const today = new Date().toISOString().split('T')[0];

  return (
    <div class="bg-gray-700/50 rounded-lg p-5 border border-gray-600">
      <h3 class="text-lg font-medium text-white mb-4">
        {isEditing ? 'Edit API Token' : 'Create New API Token'}
      </h3>
      <div class="space-y-4">
        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">
            Token Name <span class="text-red-400">*</span>
          </label>
          <input
            type="text"
            value={name}
            onInput={(e) => setName((e.target as HTMLInputElement).value)}
            class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="e.g., Development Token"
          />
          <p class="text-xs text-gray-400 mt-1">A descriptive name to identify this token</p>
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">
            Expires At <span class="text-gray-500">(optional)</span>
          </label>
          <input
            type="date"
            value={expiresAt}
            min={today}
            onInput={(e) => setExpiresAt((e.target as HTMLInputElement).value)}
            class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
          />
          <p class="text-xs text-gray-400 mt-1">Leave empty for no expiration</p>
        </div>

        {/* Allowed Models Multi-Select */}
        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">
            Allowed Models <span class="text-gray-500">(optional)</span>
          </label>
          <ModelMultiSelect
            models={models}
            selected={selectedModels}
            onChange={setSelectedModels}
          />
          <p class="text-xs text-gray-400 mt-1">
            {selectedModels.length === 0
              ? 'No selection means all models are allowed'
              : `Only these models are allowed: ${selectedModels.join(', ')}`
            }
          </p>
        </div>

        {/* Ultimate Model Access Toggle */}
        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">
            Ultimate Model Access <span class="text-gray-500">(optional)</span>
          </label>
          <div class="flex items-center gap-3">
            <button
              type="button"
              onClick={() => {
                setUltimateModelEnabled(!ultimateModelEnabled);
                if (ultimateModelEnabled) {
                  // Clearing - reset ultimateModel value
                  setUltimateModel('');
                }
              }}
              class={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
                ultimateModelEnabled ? 'bg-blue-600' : 'bg-gray-600'
              }`}
            >
              <span class={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                ultimateModelEnabled ? 'translate-x-6' : 'translate-x-1'
              }`} />
            </button>
            <span class="text-sm text-gray-400">
              {ultimateModelEnabled ? 'Enabled' : 'Disabled'}
            </span>
          </div>
          <p class="text-xs text-gray-400 mt-1">
            Allow this token to trigger the ultimate model for duplicate request handling
          </p>
        </div>

        {/* Ultimate Model Override - only shown when enabled */}
        {ultimateModelEnabled && (
          <div>
            <label class="block text-sm font-medium text-gray-300 mb-1">
              Ultimate Model Override <span class="text-gray-500">(optional)</span>
            </label>
            <select
              value={ultimateModel}
              onChange={(e) => setUltimateModel((e.target as HTMLSelectElement).value)}
              class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            >
              <option value="">Global Default</option>
              {[...models]
                .sort((a, b) => a.name.localeCompare(b.name))
                .map((model) => (
                  <option key={model.id} value={model.id}>
                    {model.name}
                  </option>
                ))}
            </select>
            <p class="text-xs text-gray-400 mt-1">
              Overrides the global ultimate model configuration for this token
            </p>
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
            disabled={!name.trim() || saving}
          >
            {saving ? (isEditing ? 'Saving...' : 'Creating...') : (isEditing ? 'Save Changes' : 'Create Token')}
          </button>
        </div>
      </div>
    </div>
  );
}
