import { useState } from 'preact/hooks';
import { ModelMultiSelect } from './ModelMultiSelect';
import type { ApiToken } from '../../types';

interface TokenFormProps {
  // kind flows through from SettingsPage's models.map so the chat-kind
  // filter below actually fires (load-balance-groups found the Decision
  // commission's amendment-7 filter was dead code from this wiring —
  // the mapped object dropped `kind`, so every row passed `!m.kind`).
  models: { name: string; id: string; kind?: 'chat' | 'image-gen' | 'decision' | 'group' }[];
  token?: ApiToken;  // For editing existing tokens
  onSubmit: (name: string, expiresAt: string | null, allowedModels?: string[]) => Promise<void>;
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
  const [selectedModels, setSelectedModels] = useState<string[]>(token?.allowed_models || []);
  const [saving, setSaving] = useState(false);

  const isEditing = !!token;

  // load-balance-groups 2026-10-16 (plan §6.4 arm 4 of 4 — P3.7):
  // the positive filter shape `!m.kind || m.kind === 'chat'` matches
  // the chat-kind baseline and excludes image-gen, decision, and
  // group kinds (defense-in-depth + operator clarity).
  const chatModels = models.filter((m) => !m.kind || m.kind === 'chat');

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
      await onSubmit(name.trim(), expires, selectedModels);
    } catch (e) {
      onStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to save token' });
    } finally {
      setSaving(false);
    }
  };

  // Calculate min date (today) for the date input
  const today = new Date().toISOString().split('T')[0];

  // Get model display name from ID
  const getModelDisplayName = (modelId: string) => {
    return models.find(m => m.id === modelId)?.name ?? modelId;
  };

  const selectedModelsDisplay = selectedModels.length > 0
    ? selectedModels.map(id => getModelDisplayName(id)).join(', ')
    : '';

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

        {/* Allowed Models Multi-Select — Decision Commission 2026-10-15
            (plan §6.7.a / P3.8 / amendment 7): filtered to chat-only models
            so image-gen + decision models never leak into the allowed-models
            picker. Same filter shape as ModelsTab.tsx:157 / C-16. */}
        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">
            Allowed Models <span class="text-gray-500">(optional)</span>
          </label>
          <ModelMultiSelect
            models={chatModels}
            selected={selectedModels}
            onChange={setSelectedModels}
          />
          <p class="text-xs text-gray-400 mt-1">
            {selectedModels.length === 0
              ? 'No selection means all models are allowed'
              : `Only these models are allowed: ${selectedModelsDisplay}`
            }
          </p>
        </div>


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
