import { useState } from 'preact/hooks';
import type { Credential, CredentialGroup } from '../../types';
import { useProviders } from '../../hooks/useApi';
import {
  CredentialGroupMemberPicker,
  MAX_GROUP_MEMBERS,
} from './CredentialGroupMemberPicker';

// credential-groups commission (2026-11-16) — the credential-group
// form. Mirrors CredentialForm (CredentialsTab): ID / Name / Provider
// plus the ordered member picker. There is NO base_url field — a group
// is a bundle of EXISTING credentials and each member carries its own
// base_url.
//
// The submit-time guards mirror the backend (defense in depth): ID and
// name are required, at least one member, no duplicate member, every
// member must exist, and every member must be a credential of the
// group's provider.

interface CredentialGroupFormProps {
  mode: 'add' | 'edit';
  initialData?: CredentialGroup;
  /** Every known credential — used for the member picker + validation. */
  credentials: Credential[];
  onSave: (data: {
    id: string;
    name: string;
    provider: string;
    members: string[];
  }) => Promise<void>;
  onCancel: () => void;
  saving: boolean;
}

export function CredentialGroupForm({
  mode,
  initialData,
  credentials,
  onSave,
  onCancel,
  saving,
}: CredentialGroupFormProps) {
  const [id, setId] = useState(initialData?.id || '');
  const [name, setName] = useState(initialData?.name || '');
  const [provider, setProvider] = useState(initialData?.provider || 'openai');
  const [members, setMembers] = useState<string[]>(initialData?.members ? [...initialData.members] : []);
  const [error, setError] = useState<string | null>(null);

  // Provider vocabulary — the single source of truth (useProviders).
  const { providers } = useProviders();

  const handleSubmit = async (e: Event) => {
    e.preventDefault();
    const trimmedId = id.trim();
    const trimmedName = name.trim();

    if (!trimmedId) {
      setError('Credential group ID is required');
      return;
    }
    if (!trimmedName) {
      setError('Credential group name is required');
      return;
    }
    // Unfilled rows ('') are dropped before validation.
    const filled = members.filter((m) => m !== '');
    if (filled.length === 0) {
      setError('Add at least one member credential');
      return;
    }
    if (filled.length > MAX_GROUP_MEMBERS) {
      setError(`A credential group can hold at most ${MAX_GROUP_MEMBERS} members`);
      return;
    }
    const seen = new Set<string>();
    for (const memberId of filled) {
      if (seen.has(memberId)) {
        setError(`Member ${memberId} is listed more than once`);
        return;
      }
      seen.add(memberId);
      const cred = credentials.find((c) => c.id === memberId);
      if (!cred) {
        setError(`Member ${memberId} does not exist`);
        return;
      }
      if (cred.provider.toLowerCase() !== provider.toLowerCase()) {
        setError(`Member ${memberId} is not a ${provider} credential`);
        return;
      }
    }

    setError(null);
    await onSave({
      id: trimmedId,
      name: trimmedName,
      provider,
      members: filled,
    });
  };

  return (
    <div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
      <h3 class="text-lg font-semibold text-white mb-4">
        {mode === 'add' ? 'Add Credential Group' : 'Edit Credential Group'}
      </h3>
      <form onSubmit={handleSubmit} class="space-y-4">
        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">Credential group ID</label>
          <input
            type="text"
            value={id}
            onInput={(e) => setId((e.target as HTMLInputElement).value)}
            disabled={mode === 'edit'}
            required
            data-testid="credential-group-id-input"
            class={`w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent ${mode === 'edit' ? 'opacity-60 cursor-not-allowed' : ''}`}
            placeholder="e.g., openai-pool"
          />
          {mode === 'edit' && <p class="text-xs text-gray-500 mt-1">ID cannot be changed</p>}
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">Name</label>
          <input
            type="text"
            value={name}
            onInput={(e) => setName((e.target as HTMLInputElement).value)}
            required
            data-testid="credential-group-name-input"
            class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            placeholder="e.g., OpenAI Pool"
          />
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">Provider</label>
          <select
            value={provider}
            onChange={(e) => setProvider((e.target as HTMLSelectElement).value)}
            required
            data-testid="credential-group-provider-select"
            class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
          >
            {providers.map((p) => (
              <option key={p.type} value={p.type}>
                {p.name}
              </option>
            ))}
          </select>
          <p class="text-xs text-gray-500 mt-1">
            Every member must be a credential of this provider.
          </p>
        </div>

        <CredentialGroupMemberPicker
          provider={provider}
          credentials={credentials}
          members={members}
          onChange={setMembers}
        />

        {error && (
          <p class="text-red-400 text-sm" data-testid="credential-group-form-error">
            {error}
          </p>
        )}

        <div class="flex gap-3 pt-2">
          <button
            type="submit"
            disabled={saving}
            data-testid="credential-group-submit"
            class="flex-1 px-4 py-2.5 bg-blue-600 hover:bg-blue-500 text-white rounded-lg transition-colors font-medium disabled:opacity-50"
          >
            {saving ? 'Saving...' : mode === 'add' ? 'Add Credential Group' : 'Save Changes'}
          </button>
          <button
            type="button"
            onClick={onCancel}
            class="flex-1 px-4 py-2.5 bg-gray-700 hover:bg-gray-600 text-white rounded-lg transition-colors font-medium border border-gray-600"
          >
            Cancel
          </button>
        </div>
      </form>
    </div>
  );
}
