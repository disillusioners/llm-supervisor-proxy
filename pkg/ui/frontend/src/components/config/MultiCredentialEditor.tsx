import type { Credential, CredentialRef } from '../../types';

interface MultiCredentialEditorProps {
  credentials: CredentialRef[];
  availableCredentials: Credential[];
  loading: boolean;
  onChange(next: CredentialRef[]): void;
  onNavigateToCredentials?: () => void;
}

const MAX_CREDENTIALS = 16;

/**
 * Editor for a model's weighted, ordered list of same-provider credentials
 * (Phase 4: multi-credential load balancing). The first row is the primary
 * credential. All entries must share a provider.
 */
export function MultiCredentialEditor({
  credentials,
  availableCredentials,
  loading,
  onChange,
  onNavigateToCredentials,
}: MultiCredentialEditorProps) {
  const handleCredentialChange = (index: number, credentialId: string) => {
    const next = credentials.map((row, i) =>
      i === index
        ? {
            ...row,
            credential_id: credentialId,
            // Default weight to 1 when the user picks a credential on a fresh row.
            weight: row.weight && row.weight > 0 ? row.weight : 1,
          }
        : row,
    );
    onChange(next);
  };

  const handleWeightChange = (index: number, raw: string) => {
    // Allow empty in-flight; clamp on blur / submit. Keep the row's
    // existing credential_id so the user can correct the number.
    const parsed = parseInt(raw, 10);
    const weight = Number.isFinite(parsed) ? parsed : 0;
    const next = credentials.map((row, i) =>
      i === index ? { ...row, weight } : row,
    );
    onChange(next);
  };

  const handleAdd = () => {
    if (credentials.length >= MAX_CREDENTIALS) return;
    onChange([
      ...credentials,
      { credential_id: '', weight: 1, position: credentials.length },
    ]);
  };

  const handleRemove = (index: number) => {
    if (credentials.length <= 1) return;
    const next = credentials.filter((_, i) => i !== index);
    onChange(next);
  };

  // Row 0 defines the required provider for all other rows.
  const primaryCred = availableCredentials.find(
    (c) => c.id === credentials[0]?.credential_id,
  );
  const primaryProvider = primaryCred?.provider;

  const isOptionDisabled = (
    rowIndex: number,
    credId: string,
    credProvider: string,
  ): { disabled: boolean; title?: string } => {
    // Already selected on another row → disabled (duplicate prevention).
    const usedElsewhere = credentials.some(
      (row, i) => i !== rowIndex && row.credential_id === credId,
    );
    if (usedElsewhere) {
      return { disabled: true, title: 'Already selected in another row' };
    }
    // After row 0 is set, mixed-provider rows are not allowed.
    if (
      rowIndex > 0 &&
      primaryProvider &&
      credProvider &&
      credProvider !== primaryProvider
    ) {
      return {
        disabled: true,
        title: 'All credentials must share the same provider',
      };
    }
    return { disabled: false };
  };

  return (
    <div>
      <div class="flex items-center justify-between mb-1">
        <label class="block text-sm font-medium text-gray-300">Credentials</label>
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
      <p class="text-xs text-gray-400 mb-2">
        Add up to {MAX_CREDENTIALS} same-provider credentials. Higher weight = more requests routed to that credential. The first row is the primary credential.
      </p>

      {loading ? (
        <div class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-gray-400 text-sm">
          Loading credentials...
        </div>
      ) : (
        <div class="space-y-2">
          {credentials.map((row, index) => (
            <div
              key={index}
              class="flex items-center gap-2 bg-gray-900/40 border border-gray-700 rounded-md p-2"
            >
              <div class="flex-1">
                <label class="block text-[10px] uppercase tracking-wide text-gray-500 mb-0.5">
                  {index === 0 ? 'Primary' : `Credential ${index + 1}`}
                </label>
                <select
                  value={row.credential_id}
                  onChange={(e) =>
                    handleCredentialChange(
                      index,
                      (e.target as HTMLSelectElement).value,
                    )
                  }
                  class="w-full px-2 py-1.5 bg-gray-700 border border-gray-600 rounded-md text-white text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                >
                  <option value="">Select a credential</option>
                  {availableCredentials.map((cred) => {
                    const state = isOptionDisabled(
                      index,
                      cred.id,
                      cred.provider,
                    );
                    return (
                      <option
                        key={cred.id}
                        value={cred.id}
                        disabled={state.disabled}
                        title={state.title}
                      >
                        {cred.id} ({cred.provider || 'unknown'})
                      </option>
                    );
                  })}
                </select>
              </div>
              <div class="w-20">
                <label class="block text-[10px] uppercase tracking-wide text-gray-500 mb-0.5">
                  Weight
                </label>
                <input
                  type="number"
                  min={1}
                  step={1}
                  value={Number.isFinite(row.weight) ? row.weight : ''}
                  onInput={(e) =>
                    handleWeightChange(
                      index,
                      (e.target as HTMLInputElement).value,
                    )
                  }
                  class="w-full px-2 py-1.5 bg-gray-700 border border-gray-600 rounded-md text-white text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                  placeholder="1"
                />
              </div>
              <button
                type="button"
                onClick={() => handleRemove(index)}
                disabled={credentials.length <= 1}
                class="self-start mt-5 px-2 py-1.5 text-gray-400 hover:text-red-400 disabled:text-gray-600 disabled:cursor-not-allowed transition-colors"
                title={
                  credentials.length <= 1
                    ? 'At least one credential row is required'
                    : 'Remove credential'
                }
                aria-label="Remove credential"
              >
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                </svg>
              </button>
            </div>
          ))}

          <button
            type="button"
            onClick={handleAdd}
            disabled={credentials.length >= MAX_CREDENTIALS}
            class="text-xs text-blue-400 hover:text-blue-300 disabled:text-gray-600 disabled:cursor-not-allowed transition-colors"
          >
            + Add credential
          </button>
        </div>
      )}

      {availableCredentials.length === 0 && !loading && (
        <p class="text-xs text-gray-400 mt-2">
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
    </div>
  );
}
