import { useState } from 'preact/hooks';
import type { ApiToken } from '../../types';
import { ModelMultiSelect } from './ModelMultiSelect';

interface TokenListProps {
  tokens: ApiToken[];
  models: { name: string; id: string }[];
  onRevoke: (id: string) => Promise<void>;
  onStatus: (status: { type: 'success' | 'error'; message: string } | null) => void;
  onCreateToken: () => void;
  onUpdatePermission: (id: string, ultimateModelEnabled: boolean, allowedModels?: string[]) => Promise<boolean>;
  onRefetchTokens: () => void;
}

export function TokenList({ tokens, models, onRevoke, onStatus, onCreateToken, onUpdatePermission, onRefetchTokens }: TokenListProps) {
  const [tokenToRevoke, setTokenToRevoke] = useState<ApiToken | null>(null);
  const [revoking, setRevoking] = useState(false);
  const [togglingId, setTogglingId] = useState<string | null>(null);
  const [optimisticValues, setOptimisticValues] = useState<Record<string, boolean>>({});
  const [optimisticAllowedModels, setOptimisticAllowedModels] = useState<Record<string, string[]>>({});
  const [editingModelsId, setEditingModelsId] = useState<string | null>(null);
  const [editingModelsValue, setEditingModelsValue] = useState<string[]>([]);
  const [savingModels, setSavingModels] = useState(false);

  const handleConfirmRevoke = async () => {
    if (!tokenToRevoke) return;
    try {
      setRevoking(true);
      onStatus(null);
      await onRevoke(tokenToRevoke.id);
      onStatus({ type: 'success', message: 'Token revoked successfully' });
      setTokenToRevoke(null);
    } catch (e) {
      onStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to revoke token' });
    } finally {
      setRevoking(false);
    }
  };

  const formatDate = (dateStr: string | undefined) => {
    if (!dateStr) return 'Never';
    try {
      return new Date(dateStr).toLocaleDateString('en-US', {
        year: 'numeric',
        month: 'short',
        day: 'numeric',
      });
    } catch {
      return dateStr;
    }
  };

  const isExpired = (expiresAt: string | undefined) => {
    if (!expiresAt) return false;
    return new Date(expiresAt) < new Date();
  };

  const handleTogglePermission = async (token: ApiToken) => {
    if (togglingId) return; // Guard FIRST - prevent concurrent toggles
    const currentValue = optimisticValues[token.id] ?? token.ultimate_model_enabled;
    const newValue = !currentValue;
    try {
      setTogglingId(token.id);
      onStatus(null);
      // Optimistic update - toggle immediately
      setOptimisticValues(prev => ({ ...prev, [token.id]: newValue }));
      const allowedModels = optimisticAllowedModels[token.id] ?? token.allowed_models ?? [];
      const success = await onUpdatePermission(token.id, newValue, allowedModels);
      if (!success) {
        // 404 - token was deleted, refresh list and revert optimistic update
        setOptimisticValues(prev => {
          const next = { ...prev };
          delete next[token.id];
          return next;
        });
        onRefetchTokens();
        onStatus({ type: 'error', message: 'Token not found. The list has been refreshed.' });
      }
    } catch (e) {
      // Revert optimistic update on error
      setOptimisticValues(prev => {
        const next = { ...prev };
        delete next[token.id];
        return next;
      });
      onStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to update permission' });
    } finally {
      setTogglingId(null);
    }
  };

  const isTokenEnabled = (token: ApiToken) => {
    return token.id in optimisticValues ? optimisticValues[token.id] : token.ultimate_model_enabled;
  };

  // Get allowed models (with optimistic updates)
  const getAllowedModels = (token: ApiToken) => {
    if (token.id in optimisticAllowedModels) {
      return optimisticAllowedModels[token.id];
    }
    return token.allowed_models ?? [];
  };

  // Start editing allowed models for a token
  const startEditModels = (token: ApiToken) => {
    setEditingModelsId(token.id);
    setEditingModelsValue(token.allowed_models ?? []);
  };

  // Cancel editing allowed models
  const cancelEditModels = () => {
    setEditingModelsId(null);
    setEditingModelsValue([]);
  };

  // Save allowed models changes
  const saveEditModels = async (tokenId: string) => {
    try {
      setSavingModels(true);
      onStatus(null);
      const token = tokens.find(t => t.id === tokenId);
      if (!token) return;
      // Optimistic update
      setOptimisticAllowedModels(prev => ({ ...prev, [tokenId]: editingModelsValue }));
      setEditingModelsId(null);
      const success = await onUpdatePermission(tokenId, token.ultimate_model_enabled, editingModelsValue);
      if (!success) {
        // Revert optimistic update
        setOptimisticAllowedModels(prev => {
          const next = { ...prev };
          delete next[tokenId];
          return next;
        });
        onRefetchTokens();
        onStatus({ type: 'error', message: 'Token not found. The list has been refreshed.' });
      }
    } catch (e) {
      // Revert optimistic update
      setOptimisticAllowedModels(prev => {
        const next = { ...prev };
        delete next[tokenId];
        return next;
      });
      onStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to update allowed models' });
    } finally {
      setSavingModels(false);
      setEditingModelsValue([]);
    }
  };

  // Get display text for allowed models
  const getAllowedModelsDisplay = (token: ApiToken) => {
    const allowed = getAllowedModels(token);
    if (!allowed || allowed.length === 0) {
      return <span class="text-xs bg-green-900/50 text-green-300 border border-green-800/40 px-1.5 py-0.5 rounded">All models</span>;
    }
    // Show first few model names, with count for more
    const maxShow = 2;
    const shown = allowed.slice(0, maxShow);
    const remaining = allowed.length - maxShow;
    return (
      <div class="flex flex-wrap items-center gap-1">
        {shown.map(name => (
          <span key={name} class="text-xs bg-gray-700 text-gray-300 border border-gray-600 px-1.5 py-0.5 rounded truncate max-w-[100px]" title={name}>
            {name}
          </span>
        ))}
        {remaining > 0 && (
          <span class="text-xs bg-gray-700 text-gray-400 border border-gray-600 px-1.5 py-0.5 rounded">
            +{remaining}
          </span>
        )}
      </div>
    );
  };

  return (
    <div class="space-y-4">
      <div class="flex justify-between items-center mb-2">
        <h3 class="text-white font-medium">API Tokens</h3>
        <button
          onClick={onCreateToken}
          class="bg-blue-600 hover:bg-blue-500 text-white text-sm font-medium py-1.5 px-3 rounded-md transition-colors flex items-center gap-1"
        >
          <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 6v6m0 0v6m0-6h6m-6 0H6" />
          </svg>
          Create Token
        </button>
      </div>

      {tokens.length === 0 ? (
        <div class="bg-gray-700/50 rounded-md p-6 border border-gray-700 border-dashed flex flex-col items-center justify-center">
          <svg class="w-10 h-10 text-gray-500 mb-2" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z" />
          </svg>
          <p class="text-gray-400 text-sm">No API tokens configured</p>
          <p class="text-gray-500 text-xs mt-1">Create a token to authenticate API requests</p>
        </div>
      ) : (
        <div class="space-y-2">
          {tokens.map((token) => {
            const enabled = isTokenEnabled(token);
            const isToggling = togglingId === token.id;
            const isEditingModels = editingModelsId === token.id;

            return (
              <div
                key={token.id}
                class="bg-gray-700/80 rounded-md p-3 border border-gray-600/50 hover:bg-gray-700 transition-colors"
              >
                <div class="flex items-start justify-between">
                  <div class="flex items-center gap-3 flex-1 min-w-0">
                    <div class="flex-1 min-w-0">
                      <p class="text-gray-100 font-medium truncate flex items-center gap-2 flex-wrap">
                        {token.name}
                        {isExpired(token.expires_at) && (
                          <span class="text-xs bg-red-900/50 text-red-300 border border-red-800/40 px-1.5 py-0.5 rounded">
                            Expired
                          </span>
                        )}
                        {enabled && (
                          <span class="text-xs bg-purple-900/50 text-purple-300 border border-purple-800/40 px-1.5 py-0.5 rounded">
                            ULTIMATE
                          </span>
                        )}
                      </p>
                      <p class="text-gray-400 text-sm truncate font-mono bg-gray-800/50 px-1 py-0.5 rounded mt-1 inline-block">
                        {token.prefix}
                      </p>
                      <div class="mt-1 flex items-center gap-3 text-xs text-gray-500 flex-wrap">
                        <span>Created: {formatDate(token.created_at)}</span>
                        {token.expires_at && (
                          <span>Expires: {formatDate(token.expires_at)}</span>
                        )}
                        {token.last_used_at && (
                          <span>Last used: {formatDate(token.last_used_at)}</span>
                        )}
                      </div>
                    </div>
                  </div>
                  <div class="flex items-center gap-1 flex-shrink-0 ml-4">
                    {/* Ultimate Model Toggle */}
                    <div class="flex items-center gap-2 mr-2" title="Ultimate Model Access">
                      <button
                        type="button"
                        onClick={() => handleTogglePermission(token)}
                        disabled={isToggling}
                        class={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
                          enabled ? 'bg-blue-600' : 'bg-gray-600'
                        } ${isToggling ? 'opacity-50 cursor-not-allowed' : ''}`}
                      >
                        <span class={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                          enabled ? 'translate-x-6' : 'translate-x-1'
                        }`} />
                      </button>
                    </div>
                    <button
                      onClick={() => setTokenToRevoke(token)}
                      class="text-gray-400 hover:text-red-400 transition-colors p-1.5 rounded-md hover:bg-gray-600/50"
                      title="Revoke token"
                    >
                      <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                      </svg>
                    </button>
                  </div>
                </div>

                {/* Allowed Models Row */}
                <div class="mt-3 pt-3 border-t border-gray-600/30">
                  <div class="flex items-center justify-between gap-2">
                    <div class="flex items-center gap-2">
                      <span class="text-xs text-gray-400">Allowed models:</span>
                      {getAllowedModelsDisplay(token)}
                    </div>
                    {isEditingModels ? (
                      <div class="flex items-center gap-2">
                        <button
                          onClick={cancelEditModels}
                          class="text-xs text-gray-400 hover:text-gray-300 px-2 py-1 rounded hover:bg-gray-600/50"
                          disabled={savingModels}
                        >
                          Cancel
                        </button>
                        <button
                          onClick={() => saveEditModels(token.id)}
                          class="text-xs text-blue-400 hover:text-blue-300 px-2 py-1 rounded hover:bg-gray-600/50 bg-blue-600/20"
                          disabled={savingModels}
                        >
                          {savingModels ? 'Saving...' : 'Save'}
                        </button>
                      </div>
                    ) : (
                      <button
                        onClick={() => startEditModels(token)}
                        class="text-xs text-gray-400 hover:text-blue-400 px-2 py-1 rounded hover:bg-gray-600/50"
                        title="Edit allowed models"
                      >
                        <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15.232 5.232l3.536 3.536m-2.036-5.036a2.5 2.5 0 113.536 3.536L6.5 21.036H3v-3.572L16.732 3.732z" />
                        </svg>
                      </button>
                    )}
                  </div>

                  {/* Inline Model Multi-Select Editor */}
                  {isEditingModels && (
                    <div class="mt-3">
                      <ModelMultiSelect
                        models={models}
                        selected={editingModelsValue}
                        onChange={setEditingModelsValue}
                      />
                    </div>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      )}

      {/* Revoke Confirmation Dialog */}
      {tokenToRevoke && (
        <div class="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-[60]">
          <div class="bg-gray-800 rounded-lg shadow-2xl max-w-sm w-full mx-4 border border-gray-700 p-6 flex flex-col items-center text-center">
            <div class="w-12 h-12 bg-red-900/30 text-red-400 rounded-full flex items-center justify-center mb-4 border border-red-800/50">
              <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
              </svg>
            </div>
            <h3 class="text-xl font-semibold text-white mb-2">Revoke Token</h3>
            <p class="text-gray-300 mb-6">
              Are you sure you want to revoke <span class="font-semibold text-white">"{tokenToRevoke.name}"</span>? This action cannot be undone and any applications using this token will lose access.
            </p>
            <div class="flex gap-3 w-full">
              <button
                onClick={() => setTokenToRevoke(null)}
                class="flex-1 px-4 py-2.5 bg-gray-700 hover:bg-gray-600 text-white rounded-lg transition-colors font-medium border border-gray-600"
                disabled={revoking}
              >
                Cancel
              </button>
              <button
                onClick={handleConfirmRevoke}
                class="flex-1 px-4 py-2.5 bg-red-600 hover:bg-red-500 text-white rounded-lg transition-colors font-medium border border-red-500/50 shadow shadow-red-900/20"
                disabled={revoking}
              >
                {revoking ? 'Revoking...' : 'Revoke'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
