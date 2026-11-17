import { useState, useEffect } from 'preact/hooks';
import type { Credential, CredentialGroup, Model } from '../../types';
import { escapeHtml } from '../../utils/helpers';
import {
  ApiError,
  CREDENTIAL_GROUP_IN_USE_MESSAGE,
  createCredentialGroup,
  deleteCredentialGroup,
  getCredentials,
  updateCredentialGroup,
} from '../../hooks/useApi';
import { CredentialGroupForm } from './CredentialGroupForm';

// credential-groups commission (2026-11-16) — the credential-group
// management surface.
//
// Mirrors CredentialsTab's three-state shape (list ↔ inline form ↔
// fixed-overlay modal). The tab does NOT fetch the group list: the
// CONTAINER owns that fetch (one source of truth for the badge map and
// the "used by" map) and hands the list down; the tab re-reads it
// through the `onRefreshGroups` callback after every mutation.
//
// The tab DOES self-fetch the CREDENTIAL list — the member picker needs
// the credentials, and they are a different resource from the groups.

interface CredentialGroupsTabProps {
  groups: CredentialGroup[];
  models: Model[];
  /** group id → referencing model ids (server-side `used_by`). */
  usedBy: Record<string, string[]>;
  /** The 409 recovery action — inline the members, then delete. */
  onInlineAndDelete: (group: CredentialGroup) => Promise<void>;
  /** Re-read the (uncached) group list; resolves with the fresh list. */
  onRefreshGroups: () => Promise<CredentialGroup[]>;
  status: { type: 'success' | 'error'; message: string } | null;
  setStatus: (status: { type: 'success' | 'error'; message: string } | null) => void;
}

export function CredentialGroupsTab({
  groups,
  models,
  usedBy,
  onInlineAndDelete,
  onRefreshGroups,
  status,
  setStatus,
}: CredentialGroupsTabProps) {
  const [credentials, setCredentials] = useState<Credential[]>([]);
  const [loading, setLoading] = useState(true);
  const [showForm, setShowForm] = useState(false);
  const [formMode, setFormMode] = useState<'add' | 'edit'>('add');
  const [groupToEdit, setGroupToEdit] = useState<CredentialGroup | undefined>(undefined);
  const [groupToDelete, setGroupToDelete] = useState<CredentialGroup | null>(null);
  // The referencing models of a BLOCKED delete (409). Null = no block.
  const [blockedModelIds, setBlockedModelIds] = useState<string[] | null>(null);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [inlining, setInlining] = useState(false);

  // Self-fetch the CREDENTIALS list for the member picker.
  useEffect(() => {
    let cancelled = false;
    const fetchCredentials = async () => {
      try {
        const data = await getCredentials();
        if (!cancelled) setCredentials(Array.isArray(data) ? data : []);
      } catch (e) {
        if (!cancelled) {
          setStatus({
            type: 'error',
            message: e instanceof Error ? e.message : 'Failed to fetch credentials',
          });
        }
      } finally {
        if (!cancelled) setLoading(false);
      }
    };
    void fetchCredentials();
    return () => {
      cancelled = true;
    };
    // setStatus is intentionally not a dependency: the tab mounts once
    // per container panel; a new setter identity must not re-fetch.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const modelLabel = (modelId: string): string => {
    const model = models.find((m) => m.id === modelId);
    return model ? model.name : modelId;
  };

  const referencingModels = (group: CredentialGroup): string[] =>
    usedBy[group.id] || group.used_by || [];

  const handleOpenAdd = () => {
    setGroupToEdit(undefined);
    setFormMode('add');
    setShowForm(true);
    setStatus(null);
  };

  const handleOpenEdit = (group: CredentialGroup) => {
    setGroupToEdit(group);
    setFormMode('edit');
    setShowForm(true);
    setStatus(null);
  };

  const handleSave = async (data: {
    id: string;
    name: string;
    provider: string;
    members: string[];
  }) => {
    try {
      setSaving(true);
      setStatus(null);
      if (formMode === 'add') {
        await createCredentialGroup({
          id: data.id,
          name: data.name,
          provider: data.provider,
          members: data.members,
        });
        setStatus({ type: 'success', message: 'Credential group added successfully' });
      } else {
        await updateCredentialGroup(data.id, {
          id: data.id,
          name: data.name,
          provider: data.provider,
          members: data.members,
        });
        setStatus({ type: 'success', message: 'Credential group updated successfully' });
      }
      // I-4: re-fetch the list after every mutation.
      await onRefreshGroups();
      setShowForm(false);
      setGroupToEdit(undefined);
    } catch (e) {
      setStatus({
        type: 'error',
        message: e instanceof Error ? e.message : 'Failed to save credential group',
      });
    } finally {
      setSaving(false);
    }
  };

  const handleConfirmDelete = async () => {
    if (!groupToDelete) return;
    const target = groupToDelete;
    try {
      setDeleting(true);
      setBlockedModelIds(null);
      await deleteCredentialGroup(target.id);
      setStatus({ type: 'success', message: 'Credential group deleted successfully' });
      setGroupToDelete(null);
      await onRefreshGroups();
    } catch (e) {
      // F2: branch on the STATUS CODE only — never on the message text.
      if (e instanceof ApiError && e.status === 409) {
        // Re-read the (uncached) group list and take the blocked group's
        // server-side `used_by` from the RETURNED array.
        let fresh: CredentialGroup[] = [];
        try {
          fresh = await onRefreshGroups();
        } catch {
          // The re-read failed — the block is still reported, the model
          // list is simply unknown.
          fresh = [];
        }
        const blocked = fresh.find((g) => g.id === target.id);
        setBlockedModelIds(blocked?.used_by || []);
        setStatus({ type: 'error', message: CREDENTIAL_GROUP_IN_USE_MESSAGE });
      } else {
        setStatus({
          type: 'error',
          message: e instanceof Error ? e.message : 'Failed to delete credential group',
        });
      }
    } finally {
      setDeleting(false);
    }
  };

  const handleInlineAndDelete = async () => {
    if (!groupToDelete) return;
    const target = groupToDelete;
    try {
      setInlining(true);
      await onInlineAndDelete(target);
      setBlockedModelIds(null);
      setGroupToDelete(null);
      await onRefreshGroups();
    } catch (e) {
      setStatus({
        type: 'error',
        message: e instanceof Error ? e.message : 'Failed to inline credential group members',
      });
    } finally {
      setInlining(false);
    }
  };

  if (loading) {
    return (
      <div class="flex items-center justify-center p-8">
        <div class="animate-spin w-6 h-6 border-2 border-blue-500 border-t-transparent rounded-full"></div>
        <span class="ml-3 text-gray-400">Loading credential groups...</span>
      </div>
    );
  }

  return (
    <div class="space-y-4">
      {status && (
        <div
          data-testid="credential-groups-status"
          class={`rounded-md px-3 py-2 text-sm ${
            status.type === 'error'
              ? 'bg-red-900/30 border border-red-800/50 text-red-300'
              : 'bg-green-900/30 border border-green-800/50 text-green-300'
          }`}
        >
          {escapeHtml(status.message)}
        </div>
      )}

      {!showForm ? (
        <>
          <div class="flex justify-between items-center mb-2">
            <h3 class="text-white font-medium">Credential Groups</h3>
            <button
              type="button"
              onClick={handleOpenAdd}
              data-testid="credential-groups-add"
              class="bg-blue-600 hover:bg-blue-500 text-white text-sm font-medium py-1.5 px-3 rounded-md transition-colors flex items-center gap-1"
            >
              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 6v6m0 0v6m0-6h6m-6 0H6" />
              </svg>
              Add Credential Group
            </button>
          </div>

          {/* Credential Groups List */}
          <div class="space-y-2">
            {groups.length === 0 ? (
              <div
                class="bg-gray-700/50 rounded-md p-6 border border-gray-700 border-dashed flex flex-col items-center justify-center"
                data-testid="credential-groups-empty"
              >
                <p class="text-gray-400 text-sm">No credential groups configured</p>
                <p class="text-gray-500 text-xs mt-1">
                  Members are flattened into the referencing models at resolution — grouping changes
                  no routing behaviour.
                </p>
              </div>
            ) : (
              <div class="overflow-x-auto">
                <table class="w-full" data-testid="credential-groups-list">
                  <thead>
                    <tr class="text-left text-xs text-gray-500 uppercase border-b border-gray-700">
                      <th class="pb-2 font-medium">ID</th>
                      <th class="pb-2 font-medium">Name</th>
                      <th class="pb-2 font-medium">Provider</th>
                      <th class="pb-2 font-medium">Members</th>
                      <th class="pb-2 font-medium">Used by</th>
                      <th class="pb-2 font-medium text-right">Actions</th>
                    </tr>
                  </thead>
                  <tbody class="divide-y divide-gray-700/50">
                    {groups.map((group) => {
                      const usedByCount = referencingModels(group).length;
                      return (
                        <tr
                          key={group.id}
                          data-testid={`credential-group-row-${group.id}`}
                          class="hover:bg-gray-700/30 transition-colors"
                        >
                          <td class="py-3 px-2">
                            <span class="text-gray-100 font-mono text-sm bg-gray-800/50 px-2 py-1 rounded">
                              {escapeHtml(group.id)}
                            </span>
                          </td>
                          <td class="py-3 px-2 text-gray-200 text-sm">{escapeHtml(group.name)}</td>
                          <td class="py-3 px-2">
                            <span class="inline-flex items-center text-xs px-2 py-1 rounded border bg-gray-900/50 text-gray-300 border-gray-800/40">
                              {escapeHtml(group.provider)}
                            </span>
                          </td>
                          <td class="py-3 px-2 text-gray-400 font-mono text-xs">
                            {(group.members || []).map((m) => escapeHtml(m)).join(', ') || '—'}
                          </td>
                          <td class="py-3 px-2">
                            <span
                              data-testid={`credential-group-used-by-${group.id}`}
                              class="inline-flex items-center text-xs px-2 py-1 rounded border bg-purple-900/50 text-purple-300 border-purple-800/40"
                            >
                              Used by {usedByCount} models
                            </span>
                          </td>
                          <td class="py-3 px-2 text-right">
                            <div class="flex items-center justify-end gap-1">
                              <button
                                type="button"
                                onClick={() => handleOpenEdit(group)}
                                data-testid={`credential-group-edit-${group.id}`}
                                class="text-gray-400 hover:text-blue-400 transition-colors p-1.5 rounded-md hover:bg-gray-600/50"
                                title="Edit credential group"
                              >
                                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v12a2 2 0 002 2h12a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
                                </svg>
                              </button>
                              <button
                                type="button"
                                onClick={() => {
                                  setBlockedModelIds(null);
                                  setGroupToDelete(group);
                                }}
                                data-testid={`credential-group-delete-${group.id}`}
                                class="text-gray-400 hover:text-red-400 transition-colors p-1.5 rounded-md hover:bg-gray-600/50"
                                title="Delete credential group"
                              >
                                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a2 2 0 00-1-1h-4a2 2 0 00-1 1v3M4 7h16" />
                                </svg>
                              </button>
                            </div>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </>
      ) : (
        <CredentialGroupForm
          mode={formMode}
          initialData={groupToEdit}
          credentials={credentials}
          onSave={handleSave}
          onCancel={() => {
            setShowForm(false);
            setGroupToEdit(undefined);
          }}
          saving={saving}
        />
      )}

      {/* Delete Confirmation Dialog */}
      {groupToDelete && (
        <div
          class="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-[60]"
          onClick={() => setGroupToDelete(null)}
        >
          <div
            class="bg-gray-800 rounded-lg shadow-2xl max-w-sm w-full mx-4 border border-gray-700 p-6 flex flex-col items-center text-center"
            onClick={(e) => e.stopPropagation()}
          >
            <div class="w-12 h-12 bg-red-900/30 text-red-400 rounded-full flex items-center justify-center mb-4 border border-red-800/50">
              <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 2 1.732 3z" />
              </svg>
            </div>
            <h3 class="text-xl font-semibold text-white mb-2">Delete Credential Group</h3>
            <p class="text-gray-300 mb-6">
              Are you sure you want to delete{' '}
              <span class="font-semibold text-white">"{escapeHtml(groupToDelete.id)}"</span>? This
              action cannot be undone.
            </p>

            {blockedModelIds && (
              <div
                class="w-full rounded-md bg-yellow-900/20 border border-yellow-800/40 p-3 mb-4 text-left"
                data-testid="credential-group-blocked"
              >
                <p class="text-yellow-300 text-sm mb-2" data-testid="credential-group-blocked-message">
                  {escapeHtml(CREDENTIAL_GROUP_IN_USE_MESSAGE)}
                </p>
                <div data-testid="credential-group-blocked-models">
                  <p class="text-gray-400 text-xs uppercase mb-1">Referencing models</p>
                  {blockedModelIds.length === 0 ? (
                    <p class="text-gray-400 text-sm">No referencing models reported.</p>
                  ) : (
                    <ul class="space-y-1">
                      {blockedModelIds.map((modelId) => (
                        <li key={modelId} class="text-gray-200 text-sm">
                          {escapeHtml(modelLabel(modelId))}
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
                <button
                  type="button"
                  onClick={handleInlineAndDelete}
                  disabled={inlining}
                  data-testid="credential-group-inline-delete"
                  class="mt-3 w-full px-3 py-2 bg-blue-600 hover:bg-blue-500 text-white rounded-lg transition-colors font-medium text-sm disabled:opacity-50"
                >
                  {inlining ? 'Inlining...' : 'Inline members then delete'}
                </button>
              </div>
            )}

            <div class="flex gap-3 w-full">
              <button
                type="button"
                onClick={() => setGroupToDelete(null)}
                class="flex-1 px-4 py-2.5 bg-gray-700 hover:bg-gray-600 text-white rounded-lg transition-colors font-medium border border-gray-600"
                disabled={deleting}
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={handleConfirmDelete}
                data-testid="credential-group-delete-confirm"
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
