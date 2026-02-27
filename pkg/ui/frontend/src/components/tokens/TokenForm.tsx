import { useState } from 'preact/hooks';

interface TokenFormProps {
  onSubmit: (name: string, expiresAt: string | null) => Promise<void>;
  onCancel: () => void;
  onStatus: (status: { type: 'success' | 'error'; message: string } | null) => void;
}

export function TokenForm({ onSubmit, onCancel, onStatus }: TokenFormProps) {
  const [name, setName] = useState('');
  const [expiresAt, setExpiresAt] = useState('');
  const [saving, setSaving] = useState(false);

  const handleSubmit = async () => {
    if (!name.trim()) {
      onStatus({ type: 'error', message: 'Token name is required' });
      return;
    }

    try {
      setSaving(true);
      onStatus(null);
      const expires = expiresAt ? new Date(expiresAt).toISOString() : null;
      await onSubmit(name.trim(), expires);
    } catch (e) {
      onStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to create token' });
    } finally {
      setSaving(false);
    }
  };

  // Calculate min date (today) for the date input
  const today = new Date().toISOString().split('T')[0];

  return (
    <div class="bg-gray-700/50 rounded-lg p-5 border border-gray-600">
      <h3 class="text-lg font-medium text-white mb-4">Create New API Token</h3>
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
            {saving ? 'Creating...' : 'Create Token'}
          </button>
        </div>
      </div>
    </div>
  );
}
