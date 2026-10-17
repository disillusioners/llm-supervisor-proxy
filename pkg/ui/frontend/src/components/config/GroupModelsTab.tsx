import { useState, useEffect } from 'preact/hooks';
import type { Model, GroupModel } from '../../types';
import { escapeHtml } from '../../utils/helpers';
import { GroupModelForm, type GroupModelPayload } from './GroupModelForm';

interface GroupModelsTabProps {
  // Server-side pre-filtered to kind: 'group' by the parent hook
  // (useGroupModels hits /fe/api/models?kind=group). The tab itself
  // never re-filters client-side — the BE is the source of truth.
  models: Model[];
  // Chat-kind models (the parent filters the useModels() list): the
  // member picker + shadow picker source inside the form, and the
  // picker-population for the one-click wrap flow.
  chatModels: Model[];
  onAddModel: (model: Omit<Model, 'id'> & { id: string }) => Promise<void>;
  onUpdateModel: (id: string, updates: Partial<Model>) => Promise<void>;
  onDeleteModel: (id: string) => Promise<void>;
  onToggleModel: (model: Model) => Promise<void>;
  setStatus: (status: { type: 'success' | 'error'; message: string; restartRequired?: boolean } | null) => void;
  // Error surfacing (plan §6.7 posture / A11Y-3). The hook catches the
  // fetch error and surfaces the message; the tab renders an
  // alert-role banner and offers a Retry button. Mirrors the sibling tabs.
  fetchError?: string | null;
  onRetry?: () => void;
  // One-click wrap pre-fill (Am30): when non-null, the tab opens the
  // add-form pre-filled for this chat model, then consumes the signal
  // so the pre-fill fires exactly once.
  wrapPreloadModel?: Model | null;
  onWrapConsumed?: () => void;
}

// Toast/success message copy — load-balance-groups / plan §6.5.
// Per-tab copy mirror of DecisionModelsTab with the strings swapped
// to "Group" instead of "Decision".
const TOAST = {
  added: 'Group model added successfully',
  updated: 'Group model updated successfully',
  deleted: 'Group model deleted successfully',
  toggled: 'Group model toggled successfully',
} as const;

export function GroupModelsTab({
  models,
  chatModels,
  onAddModel,
  onUpdateModel,
  onDeleteModel,
  onToggleModel,
  setStatus,
  fetchError,
  onRetry,
  wrapPreloadModel,
  onWrapConsumed,
}: GroupModelsTabProps) {
  const [showForm, setShowForm] = useState(false);
  const [formMode, setFormMode] = useState<'add' | 'edit'>('add');
  const [modelToEdit, setModelToEdit] = useState<GroupModel | undefined>(undefined);
  const [modelToDelete, setModelToDelete] = useState<Model | null>(null);
  const [deleting, setDeleting] = useState(false);
  // Captured wrap target (Am30): the parent's wrapPreloadModel signal
  // is consumed on arrival (onWrapConsumed clears it immediately), so
  // the pre-fill value is captured into local state — otherwise the
  // form's re-hydrate effect would watch the signal flip to null and
  // wipe the pre-fill it just applied.
  const [wrapMember, setWrapMember] = useState<Model | null>(null);

  // One-click wrap (Am30): open the add-form pre-filled for the wrap
  // target, then consume the signal. Form pre-fill convenience ONLY —
  // no group is created until the operator presses Add.
  useEffect(() => {
    if (wrapPreloadModel) {
      setModelToEdit(undefined);
      setFormMode('add');
      setWrapMember(wrapPreloadModel);
      setShowForm(true);
      setStatus(null);
      onWrapConsumed?.();
    }
  }, [wrapPreloadModel, onWrapConsumed, setStatus]);

  const handleOpenAdd = () => {
    setModelToEdit(undefined);
    setWrapMember(null);
    setFormMode('add');
    setShowForm(true);
    setStatus(null);
  };

  const handleOpenEdit = (model: Model) => {
    // Cast is safe — parent hook already filtered to group; the form
    // never asks for chat-shaped fields.
    setModelToEdit(model as GroupModel);
    setFormMode('edit');
    setShowForm(true);
    setStatus(null);
  };

  const handleSave = async (payload: GroupModelPayload) => {
    try {
      if (formMode === 'add') {
        // The form has already stamped kind/internal/
        // exclude_from_ultimate_switching (G1/G7), the empty
        // fallback_chain (G6), and the empty credentials list (groups
        // carry none); the parent's addModel just forwards the body to
        // POST /fe/api/models.
        await onAddModel(payload);
        setStatus({ type: 'success', message: TOAST.added });
      } else {
        const { id, name, kind, internal, members, shadow_model_id, enabled, exclude_from_ultimate_switching, fallback_chain, credentials } = payload;
        // kind/internal are stamped here too (not just inherited from
        // the hook's client-side merge) because the BE PUT decodes the
        // body wholesale — a sparse PUT that dropped kind would skip
        // the group validation arm entirely.
        await onUpdateModel(id, {
          name,
          enabled,
          kind,
          internal,
          members,
          shadow_model_id,
          exclude_from_ultimate_switching,
          fallback_chain,
          credentials,
        });
        setStatus({ type: 'success', message: TOAST.updated });
      }
      setShowForm(false);
      setModelToEdit(undefined);
    } catch (e) {
      setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to save group model' });
    }
  };

  const handleConfirmDelete = async () => {
    if (!modelToDelete) return;
    try {
      setStatus(null);
      setDeleting(true);
      // A 409 (model is a member of another group, Am27) surfaces here
      // as the house error body's message via apiFetch.
      await onDeleteModel(modelToDelete.id);
      setStatus({ type: 'success', message: TOAST.deleted });
      setModelToDelete(null);
    } catch (e) {
      setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to delete group model' });
    } finally {
      setDeleting(false);
    }
  };

  return (
    <div class="space-y-4">
      {!showForm ? (
        <>
          <div class="flex justify-between items-center mb-2">
            <h3 class="text-white font-medium">Group Models</h3>
            <button
              type="button"
              onClick={handleOpenAdd}
              class="bg-blue-600 hover:bg-blue-500 text-white text-sm font-medium py-1.5 px-3 rounded-md transition-colors flex items-center gap-1"
            >
              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 6v6m0 0v6m0-6h6m-6 0H6" />
              </svg>
              Add Group Model
            </button>
          </div>

          {/* Error banner (plan §6.7 / A11Y-3 posture). Same chrome as
              the sibling tabs — role="alert" so SRs announce it on
              appearance; conditionally mounted when there is an error. */}
          {fetchError && (
            <div
              class="bg-red-900/30 border border-red-800/50 rounded-md p-3 text-sm text-red-300 flex items-center justify-between"
              role="alert"
              data-testid="group-fetch-error"
            >
              <span>Failed to load group models. {fetchError}</span>
              {onRetry && (
                <button
                  type="button"
                  onClick={onRetry}
                  class="bg-red-700 hover:bg-red-600 text-white text-xs font-medium py-1 px-2 rounded"
                >
                  Retry
                </button>
              )}
            </div>
          )}

          {/* Groups list — mirrors the sibling tabs' chrome with the 🔀
              prefix glyph, member chips, and the shadow-claim line. */}
          <div class="space-y-2" data-testid="group-models-list">
            {models.length === 0 ? (
              <div
                class="bg-gray-700/50 rounded-md p-6 border border-gray-700 border-dashed flex flex-col items-center justify-center"
                data-testid="group-empty-state"
              >
                <svg class="w-10 h-10 text-gray-500 mb-2" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 7h12m0 0l-4-4m4 4l-4 4m0 6H4m0 0l4 4m-4-4l4-4" />
                </svg>
                <p class="text-gray-400 text-sm">No group models configured</p>
                <p class="text-gray-500 text-xs mt-1">Add a group to load-balance chat traffic across multiple member models behind one ID.</p>
              </div>
            ) : (
              models.map((model) => {
                const members = model.members ?? [];
                const shadow = model.shadow_model_id ?? '';
                return (
                  <div
                    key={model.id}
                    data-testid="group-model-row"
                    data-model-id={model.id}
                    class="flex items-center justify-between bg-gray-700/80 rounded-md p-3 border border-gray-600/50 hover:bg-gray-700 transition-colors"
                  >
                    <div class="flex items-center gap-3 flex-1 min-w-0">
                      <button
                        type="button"
                        onClick={() => onToggleModel(model)}
                        class={`w-10 h-6 rounded-full flex-shrink-0 relative transition-colors ${model.enabled ? 'bg-green-500' : 'bg-gray-500'
                          }`}
                        title={model.enabled ? 'Enabled' : 'Disabled'}
                        aria-label={`Toggle ${model.name}`}
                      >
                        <span class={`absolute top-1 w-4 h-4 bg-white rounded-full transition-all ${model.enabled ? 'right-1' : 'left-1'
                          }`}></span>
                      </button>
                      <div class="flex-1 min-w-0">
                        <p class="text-gray-100 font-medium truncate flex items-center gap-2">
                          {escapeHtml(model.name)}
                          <span
                            class="inline-flex items-center gap-1 text-xs bg-purple-900/50 text-purple-300 border border-purple-700/50 px-1.5 py-0.5 rounded"
                            title="Virtual load-balancing group"
                          >
                            <span aria-hidden="true">🔀</span>
                            group
                          </span>
                        </p>
                        <p class="text-gray-400 text-sm truncate font-mono bg-gray-800/50 px-1 py-0.5 rounded mt-1 inline-block">
                          {escapeHtml(model.id)}
                        </p>
                        {/* Member chips — model_id ×weight. */}
                        <div class="mt-1 flex items-center gap-1.5 flex-wrap">
                          {members.length === 0 ? (
                            <span class="text-yellow-400 text-xs">MEMBERS: 0 — group will not function</span>
                          ) : (
                            members.map((member) => (
                              <span
                                key={member.model_id}
                                class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-blue-900/50 text-blue-300 border border-blue-700"
                                title={`Member weight: ${member.weight}`}
                              >
                                {escapeHtml(member.model_id)} <span class="text-blue-400/80">×{member.weight}</span>
                              </span>
                            ))
                          )}
                        </div>
                        {/* Shadow-claim line — the claim drops (and the
                            shadowed model reappears) when cleared via
                            edit (Q19). */}
                        <p class={`text-xs mt-1 ${shadow ? 'text-green-400' : 'text-gray-500'}`}>
                          {shadow
                            ? `SHADOWS: ${escapeHtml(shadow)} — requests for that ID route here`
                            : 'No shadow claim'}
                        </p>
                      </div>
                    </div>
                    <div class="flex items-center gap-1 flex-shrink-0 ml-4">
                      <button
                        type="button"
                        onClick={() => handleOpenEdit(model)}
                        class="text-gray-400 hover:text-blue-400 transition-colors p-1.5 rounded-md hover:bg-gray-600/50"
                        title="Edit group model"
                        aria-label={`Edit ${model.name}`}
                      >
                        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
                        </svg>
                      </button>
                      <button
                        type="button"
                        onClick={() => setModelToDelete(model)}
                        class="text-gray-400 hover:text-red-400 transition-colors p-1.5 rounded-md hover:bg-gray-600/50"
                        title="Delete group model"
                        aria-label={`Delete ${model.name}`}
                      >
                        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                        </svg>
                      </button>
                    </div>
                  </div>
                );
              })
            )}
          </div>
        </>
      ) : (
        <GroupModelForm
          mode={formMode}
          initialData={modelToEdit}
          wrapTarget={formMode === 'add' ? wrapMember : null}
          chatModels={chatModels}
          groups={models as GroupModel[]}
          onSave={handleSave}
          onCancel={() => {
            setShowForm(false);
            setModelToEdit(undefined);
            setWrapMember(null);
          }}
          onStatus={setStatus}
        />
      )}

      {/* Delete Confirmation Dialog — mirrors the sibling tabs with
          group-specific copy. */}
      {modelToDelete && (
        <div
          class="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-[60]"
          onClick={() => setModelToDelete(null)}
          data-testid="group-delete-modal"
        >
          <div
            class="bg-gray-800 rounded-lg shadow-2xl max-w-sm w-full mx-4 border border-gray-700 p-6 flex flex-col items-center text-center"
            onClick={(e) => e.stopPropagation()}
          >
            <div class="w-12 h-12 bg-red-900/30 text-red-400 rounded-full flex items-center justify-center mb-4 border border-red-800/50">
              <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
              </svg>
            </div>
            <h3 class="text-xl font-semibold text-white mb-2">Delete Group Model</h3>
            <p class="text-gray-300 mb-6">
              Are you sure you want to delete <span class="font-semibold text-white">"{modelToDelete.name}"</span>? Deleting the group drops any shadow claim — the shadowed model reappears. This action cannot be undone.
            </p>
            <div class="flex gap-3 w-full">
              <button
                type="button"
                onClick={() => setModelToDelete(null)}
                class="flex-1 px-4 py-2.5 bg-gray-700 hover:bg-gray-600 text-white rounded-lg transition-colors font-medium border border-gray-600"
                disabled={deleting}
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={handleConfirmDelete}
                class="flex-1 px-4 py-2.5 bg-red-600 hover:bg-red-500 text-white rounded-lg transition-colors font-medium border border-red-500/50 shadow shadow-red-900/20"
                disabled={deleting}
                data-testid="group-delete-confirm"
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
