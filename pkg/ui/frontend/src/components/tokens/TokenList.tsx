import { useState } from 'preact/hooks';
import type { ApiToken } from '../../types';
import {
  AXIS_COPY,
  AxisGrantControls,
  AxisMode,
  GrantAxis,
  TokenModelRow,
  effectiveGrantNote,
  modeOfStored,
  splitByAxis,
  wireValue,
} from './grantAxes';

interface TokenListProps {
  tokens: ApiToken[];
  // The UNFILTERED model list (kind included): the axis split is made by ROW
  // KIND, and a group row is never a model grant.
  models: TokenModelRow[];
  onRevoke: (id: string) => Promise<void>;
  onStatus: (status: { type: 'success' | 'error'; message: string } | null) => void;
  onCreateToken: () => void;
  onUpdatePermission: (
    id: string,
    allowedModels?: string[] | null,
    allowedGroups?: string[] | null,
  ) => Promise<boolean>;
  onRefetchTokens: () => void;
}

interface OptimisticGrants {
  models: string[] | null;
  groups: string[] | null;
}

export function TokenList({
  tokens,
  models,
  onRevoke,
  onStatus,
  onCreateToken,
  onUpdatePermission,
  onRefetchTokens,
}: TokenListProps) {
  const [tokenToRevoke, setTokenToRevoke] = useState<ApiToken | null>(null);
  const [revoking, setRevoking] = useState(false);
  // Optimistic grant state for BOTH axes (null = absent/unrestricted,
  // [] = restricted-and-empty, ids = restricted).
  const [optimisticGrants, setOptimisticGrants] = useState<Record<string, OptimisticGrants>>({});
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editModelMode, setEditModelMode] = useState<AxisMode>('unrestricted');
  const [editGroupMode, setEditGroupMode] = useState<AxisMode>('unrestricted');
  const [editModelIds, setEditModelIds] = useState<string[]>([]);
  const [editGroupIds, setEditGroupIds] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);

  // All kinds are selectable; the pickers are split by SURFACE (group rows in
  // the group picker) and the SUBMIT split is made by row kind.
  const modelRows = models.filter((m) => m.kind !== 'group');
  const groupRows = models.filter((m) => m.kind === 'group');

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

  // The effective value of one axis (optimistic state wins over the stored
  // one). A MISSING key and an explicit null are the SAME state (ABSENT).
  const axisValue = (token: ApiToken, axis: GrantAxis): string[] | null => {
    const optimistic = optimisticGrants[token.id];
    if (optimistic) return optimistic[axis];
    return (axis === 'models' ? token.allowed_models : token.allowed_groups) ?? null;
  };

  const isAxisInvalid = (token: ApiToken, axis: GrantAxis): boolean =>
    axis === 'models'
      ? token.allowed_models_invalid === true
      : token.allowed_groups_invalid === true;

  // The wildcard is CONJUNCTIVE: only when BOTH axes are absent (and neither
  // axis is invalid) does the token reach every model.
  const isWildcard = (token: ApiToken): boolean =>
    axisValue(token, 'models') === null &&
    axisValue(token, 'groups') === null &&
    !isAxisInvalid(token, 'models') &&
    !isAxisInvalid(token, 'groups');

  // Get display name from ID
  const getModelDisplayName = (modelId: string) => {
    return models.find((m) => m.id === modelId)?.name ?? modelId;
  };

  // One axis' state, rendered for the operator. The two axes are NEVER
  // collapsed into a single "all models" badge: a groups-only token must not
  // read as "every model".
  const getAxisDisplay = (token: ApiToken, axis: GrantAxis) => {
    if (isAxisInvalid(token, axis)) {
      return (
        <span
          data-axis={axis}
          class="text-xs bg-red-900/50 text-red-300 border border-red-800/40 px-1.5 py-0.5 rounded"
        >
          Deny-all — allow-list unreadable
        </span>
      );
    }
    const value = axisValue(token, axis);
    if (value === null) {
      return (
        <span
          data-axis={axis}
          class="text-xs bg-gray-700/60 text-gray-300 border border-gray-600 px-1.5 py-0.5 rounded"
        >
          No allow-list on this axis (contributes no grants)
        </span>
      );
    }
    if (value.length === 0) {
      return (
        <span
          data-axis={axis}
          class="text-xs bg-yellow-900/40 text-yellow-200 border border-yellow-800/40 px-1.5 py-0.5 rounded"
        >
          Restricted — grants nothing
        </span>
      );
    }
    // Show first few names, with a count for the rest.
    const maxShow = 2;
    const shown = value.slice(0, maxShow);
    const remaining = value.length - maxShow;
    return (
      <div class="flex flex-wrap items-center gap-1">
        {shown.map((id) => (
          <span
            key={id}
            data-axis={axis}
            class="text-xs bg-gray-700 text-gray-300 border border-gray-600 px-1.5 py-0.5 rounded truncate max-w-[100px]"
            title={id}
          >
            {getModelDisplayName(id)}
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

  // Start editing the grants of a token. The mode of each axis is DERIVED
  // from the stored value; an axis that is already restricted can only be
  // narrowed from here.
  const startEdit = (token: ApiToken) => {
    setEditingId(token.id);
    const modelValue = axisValue(token, 'models');
    const groupValue = axisValue(token, 'groups');
    setEditModelMode(modeOfStored(modelValue));
    setEditModelIds(modelValue ?? []);
    setEditGroupMode(modeOfStored(groupValue));
    setEditGroupIds(groupValue ?? []);
  };

  // Cancel editing the grants
  const cancelEdit = () => {
    setEditingId(null);
    setEditModelIds([]);
    setEditGroupIds([]);
  };

  // Save the grant changes. The two axes are split by ROW KIND and written
  // independently: Unrestricted => null (KEEP on PATCH), Restricted => the
  // ids, or [] when the selection was cleared (never null).
  const saveEdit = async (tokenId: string) => {
    const token = tokens.find((t) => t.id === tokenId);
    if (!token) return;
    const modelsInvalid = isAxisInvalid(token, 'models');
    const groupsInvalid = isAxisInvalid(token, 'groups');
    // A DENY-ALL axis is never rendered (nor written) as unrestricted.
    const modelMode = modelsInvalid ? 'restricted' : editModelMode;
    const groupMode = groupsInvalid ? 'restricted' : editGroupMode;
    try {
      setSaving(true);
      onStatus(null);
      const { modelIds, groupIds } = splitByAxis(
        [...editModelIds, ...editGroupIds],
        models,
        token,
      );
      const allowedModels = wireValue(modelMode, modelIds);
      const allowedGroups = wireValue(groupMode, groupIds);
      // Optimistic update — BOTH axes.
      setOptimisticGrants((prev) => ({
        ...prev,
        [tokenId]: { models: allowedModels, groups: allowedGroups },
      }));
      setEditingId(null);
      const success = await onUpdatePermission(tokenId, allowedModels, allowedGroups);
      if (!success) {
        // Revert the optimistic update (BOTH axes)
        setOptimisticGrants((prev) => {
          const next = { ...prev };
          delete next[tokenId];
          return next;
        });
        onRefetchTokens();
        onStatus({ type: 'error', message: 'Token not found. The list has been refreshed.' });
      }
    } catch (e) {
      // Revert the optimistic update (BOTH axes)
      setOptimisticGrants((prev) => {
        const next = { ...prev };
        delete next[tokenId];
        return next;
      });
      onStatus({
        type: 'error',
        message: e instanceof Error ? e.message : 'Failed to update token permissions',
      });
    } finally {
      setSaving(false);
      setEditModelIds([]);
      setEditGroupIds([]);
    }
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
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M12 6v6m0 6v6m0-6h6m-6 0H6" />
          </svg>
          Create Token
        </button>
      </div>

      {tokens.length === 0 ? (
        <div class="bg-gray-700/50 rounded-md p-6 border border-gray-700 border-dashed flex flex-col items-center justify-center">
          <svg class="w-10 h-10 text-gray-500 mb-2" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z" />
          </svg>
          <p class="text-gray-400 text-sm">No API tokens configured</p>
          <p class="text-gray-500 text-xs mt-1">Create a token to authenticate API requests</p>
        </div>
      ) : (
        <div class="space-y-2">
          {tokens.map((token) => {
            const isEditing = editingId === token.id;
            const modelsInvalid = isAxisInvalid(token, 'models');
            const groupsInvalid = isAxisInvalid(token, 'groups');
            // A restricted axis can never be widened back on an existing
            // token — the option is disabled, and the note says why.
            const modelPromotionBlocked = modeOfStored(axisValue(token, 'models')) === 'restricted';
            const groupPromotionBlocked = modeOfStored(axisValue(token, 'groups')) === 'restricted';

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
                    <button
                      onClick={() => setTokenToRevoke(token)}
                      class="text-gray-400 hover:text-red-400 transition-colors p-1.5 rounded-md hover:bg-gray-600/50"
                      title="Revoke token"
                    >
                      <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M6 18L18 6M6 6l12 12" />
                      </svg>
                    </button>
                  </div>
                </div>

                {/* Grant axes */}
                <div class="mt-3 pt-3 border-t border-gray-600/30">
                  <div class="flex items-center justify-between gap-2">
                    <div class="flex flex-col gap-1">
                      {isWildcard(token) ? (
                        <div class="flex items-center gap-2">
                          <span class="text-xs text-gray-400">Reach:</span>
                          <span
                            data-testid="wildcard-badge"
                            class="text-xs bg-green-900/50 text-green-300 border border-green-800/40 px-1.5 py-0.5 rounded"
                          >
                            Unrestricted — all models and groups
                          </span>
                        </div>
                      ) : (
                        <>
                          <div class="flex items-center gap-2 flex-wrap">
                            <span class="text-xs text-gray-400">Allowed models:</span>
                            {getAxisDisplay(token, 'models')}
                          </div>
                          <div class="flex items-center gap-2 flex-wrap">
                            <span class="text-xs text-gray-400">Allowed groups:</span>
                            {getAxisDisplay(token, 'groups')}
                          </div>
                        </>
                      )}
                    </div>
                    {isEditing ? (
                      <div class="flex items-center gap-2">
                        <button
                          onClick={cancelEdit}
                          class="text-xs text-gray-400 hover:text-gray-300 px-2 py-1 rounded hover:bg-gray-600/50"
                          disabled={saving}
                        >
                          Cancel
                        </button>
                        <button
                          onClick={() => saveEdit(token.id)}
                          class="text-xs text-blue-400 hover:text-blue-300 px-2 py-1 rounded hover:bg-gray-600/50 bg-blue-600/20"
                          disabled={saving}
                        >
                          {saving ? 'Saving...' : 'Save'}
                        </button>
                      </div>
                    ) : (
                      <button
                        onClick={() => startEdit(token)}
                        class="text-xs text-gray-400 hover:text-blue-400 px-2 py-1 rounded hover:bg-gray-600/50"
                        title="Edit token grants"
                      >
                        <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M15.232 5.232l3.536 3.536m-2.036-5.036a2.5 2.5 0 113.536 3.536L6.5 21.036H3v-3.572L16.732 3.732z" />
                        </svg>
                      </button>
                    )}
                  </div>

                  {/* Deny-all warnings (one per invalid axis) */}
                  {modelsInvalid && (
                    <div role="alert" class="mt-2 text-xs text-red-300 bg-red-900/30 border border-red-800/50 rounded px-2 py-1.5">
                      {AXIS_COPY.invalid.models}
                    </div>
                  )}
                  {groupsInvalid && (
                    <div role="alert" class="mt-2 text-xs text-red-300 bg-red-900/30 border border-red-800/50 rounded px-2 py-1.5">
                      {AXIS_COPY.invalid.groups}
                    </div>
                  )}

                  {/* Inline grant editor — the LIVE edit surface */}
                  {isEditing && (
                    <div class="mt-3 space-y-3">
                      <AxisGrantControls
                        axis="models"
                        mode={modelsInvalid ? 'restricted' : editModelMode}
                        onModeChange={setEditModelMode}
                        rows={modelRows}
                        selected={editModelIds}
                        onSelectedChange={setEditModelIds}
                        disableUnrestricted={modelPromotionBlocked || modelsInvalid}
                        invalid={modelsInvalid}
                        showCreateOnlyNote
                      />
                      <AxisGrantControls
                        axis="groups"
                        mode={groupsInvalid ? 'restricted' : editGroupMode}
                        onModeChange={setEditGroupMode}
                        rows={groupRows}
                        selected={editGroupIds}
                        onSelectedChange={setEditGroupIds}
                        disableUnrestricted={groupPromotionBlocked || groupsInvalid}
                        invalid={groupsInvalid}
                        showCreateOnlyNote
                      />
                      <p class="text-xs text-gray-300 bg-gray-800/60 border border-gray-700 rounded px-2 py-1.5">
                        {effectiveGrantNote(
                          modelsInvalid ? 'restricted' : editModelMode,
                          groupsInvalid ? 'restricted' : editGroupMode,
                          editModelIds.length,
                          editGroupIds.length,
                        )}
                      </p>
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
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
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
