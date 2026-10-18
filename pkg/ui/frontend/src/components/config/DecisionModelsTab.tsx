import { useState } from 'preact/hooks';
import type { Model, DecisionModel } from '../../types';
import { escapeHtml } from '../../utils/helpers';
import { DecisionModelForm, type DecisionModelPayload } from './DecisionModelForm';

interface DecisionModelsTabProps {
  // Server-side pre-filtered to kind: 'decision' by the parent hook
  // (useDecisionModels hits /fe/api/models?kind=decision). The tab
  // itself never re-filters client-side — the BE is the source of
  // truth.
  models: Model[];
  onAddModel: (model: Omit<Model, 'id'> & { id: string }) => Promise<void>;
  onUpdateModel: (id: string, updates: Partial<Model>) => Promise<void>;
  onDeleteModel: (id: string) => Promise<void>;
  onToggleModel: (model: Model) => Promise<void>;
  setStatus: (status: { type: 'success' | 'error'; message: string; restartRequired?: boolean } | null) => void;
  onNavigateToCredentials?: () => void;
  // Error surfacing (plan §6.7 / A11Y-3). The hook catches the fetch
  // error and surfaces the message; the tab renders an alert-role
  // banner and offers a Retry button. Mirrors ImgGenModelsTab §2.7.
  fetchError?: string | null;
  onRetry?: () => void;
}

// Toast/success message copy — Decision Commission 2026-10-15 /
// plan §6.5. Per-tab copy mirror of ImgGenModelsTab with the strings
// swapped to "Decision" instead of "Image-Gen".
const TOAST = {
  added: 'Decision model added successfully',
  updated: 'Decision model updated successfully',
  deleted: 'Decision model deleted successfully',
  toggled: 'Decision model toggled successfully',
} as const;

export function DecisionModelsTab({
  models,
  onAddModel,
  onUpdateModel,
  onDeleteModel,
  onToggleModel,
  setStatus,
  onNavigateToCredentials,
  fetchError,
  onRetry,
}: DecisionModelsTabProps) {
  const [showForm, setShowForm] = useState(false);
  const [formMode, setFormMode] = useState<'add' | 'edit'>('add');
  const [modelToEdit, setModelToEdit] = useState<DecisionModel | undefined>(undefined);
  const [modelToDelete, setModelToDelete] = useState<Model | null>(null);
  const [deleting, setDeleting] = useState(false);

  const handleOpenAdd = () => {
    setModelToEdit(undefined);
    setFormMode('add');
    setShowForm(true);
    setStatus(null);
  };

  const handleOpenEdit = (model: Model) => {
    // Cast is safe — parent hook already filtered to decision; the
    // form never asks for chat-shaped fields.
    setModelToEdit(model as DecisionModel);
    setFormMode('edit');
    setShowForm(true);
    setStatus(null);
  };

  const handleSave = async (payload: DecisionModelPayload) => {
    try {
      if (formMode === 'add') {
        // Strip the local-only wrapper to a wire-shaped Model. The
        // form has already stamped kind/internal/internal_provider/
        // exclude_from_ultimate_switching (internal is now
        // unconditionally true repo-wide — no external upstream), so the
        // parent's addModel just forwards the body to POST
        // /fe/api/models. Decision models never have a fallback chain
        // (decision models reject it per plan §6.2 row decision rules),
        // so we send an empty array.
        const { id, name, kind, internal, internal_provider, internal_model, internal_base_url, credentials, enabled, exclude_from_ultimate_switching } = payload;
        await onAddModel({
          id,
          name,
          enabled,
          fallback_chain: [],
          truncate_params: [],
          kind,
          internal,
          internal_provider,
          internal_model,
          internal_base_url,
          credentials,
          exclude_from_ultimate_switching,
        });
        setStatus({ type: 'success', message: TOAST.added });
      } else {
        const { id, name, internal_model, internal_base_url, credentials, enabled, exclude_from_ultimate_switching, internal_provider } = payload;
        await onUpdateModel(id, {
          name,
          enabled,
          internal_model,
          internal_base_url,
          credentials,
          internal_provider,
          exclude_from_ultimate_switching,
          // kind/internal are immutable in edit — the form does not
          // surface them and the BE rejects a kind flip anyway.
          // (internal is now unconditionally true repo-wide.)
        });
        setStatus({ type: 'success', message: TOAST.updated });
      }
      setShowForm(false);
      setModelToEdit(undefined);
    } catch (e) {
      setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to save decision model' });
    }
  };

  const handleConfirmDelete = async () => {
    if (!modelToDelete) return;
    try {
      setStatus(null);
      setDeleting(true);
      await onDeleteModel(modelToDelete.id);
      setStatus({ type: 'success', message: TOAST.deleted });
      setModelToDelete(null);
    } catch (e) {
      setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to delete decision model' });
    } finally {
      setDeleting(false);
    }
  };

  return (
    <div class="space-y-4">
      {!showForm ? (
        <>
          <div class="flex justify-between items-center mb-2">
            <h3 class="text-white font-medium">Decision Models</h3>
            <button
              type="button"
              onClick={handleOpenAdd}
              class="bg-blue-600 hover:bg-blue-500 text-white text-sm font-medium py-1.5 px-3 rounded-md transition-colors flex items-center gap-1"
            >
              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 6v6m0 0v6m0-6h6m-6 0H6" />
              </svg>
              Add Decision Model
            </button>
          </div>

          {/* Error banner (plan §6.7 / A11Y-3). Same posture as
              ImgGenModelsTab — role="alert" so SRs announce it on
              appearance. The banner is conditionally mounted — it
              does not appear when there is no error. */}
          {fetchError && (
            <div
              class="bg-red-900/30 border border-red-800/50 rounded-md p-3 text-sm text-red-300 flex items-center justify-between"
              role="alert"
              data-testid="decision-fetch-error"
            >
              <span>Failed to load decision models. {fetchError}</span>
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

          {/* Models list — mirrors ImgGenModelsTab chrome with the
              🎯 prefix glyph (Q19 default) and decision-provider badge. */}
          <div class="space-y-2" data-testid="decision-models-list">
            {models.length === 0 ? (
              <div
                class="bg-gray-700/50 rounded-md p-6 border border-gray-700 border-dashed flex flex-col items-center justify-center"
                data-testid="decision-empty-state"
              >
                <svg class="w-10 h-10 text-gray-500 mb-2" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 19V6l12-3v13M9 19c0 1.105-1.343 2-3 2s-3-.895-3-2 1.343-2 3-2 3 .895 3 2zm12-3c0 1.105-1.343 2-3 2s-3-.895-3-2 1.343-2 3-2 3 .895 3 2zM9 10l12-3" />
                </svg>
                <p class="text-gray-400 text-sm">No decision models configured</p>
                <p class="text-gray-500 text-xs mt-1">Add your first decision model to enable decision endpoints.</p>
              </div>
            ) : (
              models.map((model) => {
                const credCount = model.credentials?.length ?? 0;
                return (
                  <div
                    key={model.id}
                    data-testid="decision-model-row"
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
                          {model.internal && (
                            <span
                              class="inline-flex items-center gap-1 text-xs bg-purple-900/50 text-purple-300 border border-purple-700/50 px-1.5 py-0.5 rounded"
                              title={`Internal upstream: ${model.internal_provider || 'unknown'}`}
                            >
                              <span aria-hidden="true">🎯</span>
                              {model.internal_provider || 'openai'}
                            </span>
                          )}
                        </p>
                        <p class="text-gray-400 text-sm truncate font-mono bg-gray-800/50 px-1 py-0.5 rounded mt-1 inline-block">
                          {escapeHtml(model.id)}
                        </p>
                        {model.internal && model.internal_model && (
                          <div class="mt-1 flex items-center gap-1.5 flex-wrap">
                            <span class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium
                                         bg-blue-900/50 text-blue-300 border border-blue-700">
                              {escapeHtml(model.internal_model)}
                            </span>
                          </div>
                        )}
                        {/* Credentials count line — warning state when zero
                            (mirrors ImgGenModelsTab §2.3 row content). */}
                        <p
                          class={`text-xs mt-1 ${credCount === 0 ? 'text-yellow-400' : 'text-gray-500'}`}
                        >
                          CREDENTIALS: {credCount}
                          {credCount === 0 && (
                            <span class="ml-1">— No credentials — model will not function</span>
                          )}
                        </p>
                        {model.internal_base_url && (
                          <p class="text-gray-400 text-sm truncate font-mono bg-gray-800/50 px-1 py-0.5 rounded mt-1 inline-block max-w-full">
                            {escapeHtml(model.internal_base_url)}
                          </p>
                        )}
                      </div>
                    </div>
                    <div class="flex items-center gap-1 flex-shrink-0 ml-4">
                      <button
                        type="button"
                        onClick={() => handleOpenEdit(model)}
                        class="text-gray-400 hover:text-blue-400 transition-colors p-1.5 rounded-md hover:bg-gray-600/50"
                        title="Edit decision model"
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
                        title="Delete decision model"
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
        <DecisionModelForm
          mode={formMode}
          initialData={modelToEdit}
          onSave={handleSave}
          onCancel={() => {
            setShowForm(false);
            setModelToEdit(undefined);
          }}
          onStatus={setStatus}
          onNavigateToCredentials={onNavigateToCredentials}
        />
      )}

      {/* Delete Confirmation Dialog — mirrors ImgGenModelsTab
          with decision-specific copy. */}
      {modelToDelete && (
        <div
          class="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-[60]"
          onClick={() => setModelToDelete(null)}
          data-testid="decision-delete-modal"
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
            <h3 class="text-xl font-semibold text-white mb-2">Delete Decision Model</h3>
            <p class="text-gray-300 mb-6">
              Are you sure you want to delete <span class="font-semibold text-white">"{modelToDelete.name}"</span>? This action cannot be undone.
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
                data-testid="decision-delete-confirm"
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
