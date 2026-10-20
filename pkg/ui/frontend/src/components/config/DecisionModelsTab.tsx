import { useState } from 'preact/hooks';
import type { Model, DecisionModel, DecisionTestResult } from '../../types';
import { escapeHtml } from '../../utils/helpers';
import { testDecisionModel } from '../../hooks/useApi';
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

// ─────────────────────────────────────────────────────────────────────────────
// Decision TEST button — per-row probe.
//
// The row's Test button fires ONE real decision request through the
// proxy's decision path (POST /fe/api/decisions/test) and renders the
// outcome inline:
//
//   - success → the model + the typed answers (a predicate's
//     probability, a choice/score's probabilities),
//   - failure → a CLASSIFIED message (402 insufficient funds, 429 rate
//     limited, 401 auth, 422 endpoint unavailable, ...) plus the
//     upstream's own short message. NEVER a raw body dump.
//
// The classified status comes from the BE (`status` = the upstream's
// own error status when it answered with one).
// ─────────────────────────────────────────────────────────────────────────────

// DECISION_ERROR_CLASSES maps the classified status to a short,
// operator-facing label. Anything unmapped falls back to
// classifyDecisionTestError's default branch.
const DECISION_ERROR_CLASSES: Record<number, string> = {
  0: 'Test request failed — the proxy did not answer',
  400: 'Request rejected by the decision adapter',
  401: 'Authentication failed — check the credential API key',
  402: 'Insufficient funds — top up the provider account',
  403: 'Access denied — the credential may not use this model',
  404: 'Not found — check the upstream base URL',
  408: 'Upstream timeout',
  422: 'Endpoint unavailable',
  429: 'Rate limited — retry later',
  500: 'Upstream error',
  502: 'Upstream error',
  503: 'Upstream unavailable — retry later',
  504: 'Upstream timed out',
};

// classifyDecisionTestError maps a classified status to its label.
export function classifyDecisionTestError(status: number): string {
  const mapped = DECISION_ERROR_CLASSES[status];
  if (mapped) return mapped;
  if (status >= 500) return 'Upstream error';
  return 'Unexpected error';
}

// DECISION_TEST_MAX_MESSAGE caps the upstream message rendered in the
// row (the BE already caps it — this is the belt-and-suspenders
// guard on the FE side).
const DECISION_TEST_MAX_MESSAGE = 300;

// formatProbability renders a 0..1 probability as a percentage.
function formatProbability(value: number): string {
  return `${(value * 100).toFixed(1)}%`;
}

// truncateDecisionTestMessage caps the upstream message.
function truncateDecisionTestMessage(message: string): string {
  if (message.length <= DECISION_TEST_MAX_MESSAGE) return message;
  return `${message.slice(0, DECISION_TEST_MAX_MESSAGE)}…`;
}

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
  // Per-row TEST state: the ids of the rows whose probe is currently
  // in flight (SEVERAL rows may probe concurrently — each row owns
  // its own busy flag) + the last result per row id (the panel stays
  // until the next probe on that row replaces it).
  const [testingIds, setTestingIds] = useState<string[]>([]);
  const [testResults, setTestResults] = useState<Record<string, DecisionTestResult>>({});

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

  // handleTest fires ONE real decision request for the row and stores
  // the classified result under the row id (per-row state — a probe
  // on one row never touches another row's panel).
  //
  // The busy flag is PER ROW: two rows may probe concurrently and
  // each keeps its own "Testing..." state. A single global slot
  // would re-enable the first row's button while its probe was still
  // in flight (a second click = a duplicate billed call) and the
  // first row's finally{} would clear the second row's busy state.
  const handleTest = async (model: Model) => {
    setTestingIds((prev) => [...prev, model.id]);
    // Drop the previous result for this row so the panel cannot show
    // a stale outcome while the new probe is in flight.
    setTestResults((prev) => {
      const next = { ...prev };
      delete next[model.id];
      return next;
    });
    try {
      const result = await testDecisionModel(model.id);
      setTestResults((prev) => ({ ...prev, [model.id]: result }));
    } catch (e) {
      // The FE call itself failed (transport / non-JSON answer). The
      // status 0 maps to the "the proxy did not answer" class.
      setTestResults((prev) => ({
        ...prev,
        [model.id]: {
          success: false,
          status: 0,
          error: e instanceof Error ? e.message : 'Test request failed',
        },
      }));
    } finally {
      // Drop ONLY this row's busy flag — another row's in-flight
      // probe must keep its own "Testing..." state.
      setTestingIds((prev) => prev.filter((id) => id !== model.id));
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
                const testResult = testResults[model.id];
                const isTesting = testingIds.includes(model.id);
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
                        {/* remove-external-upstream P5/A4 (R3): badge
                            guard dropped — chips render
                            unconditionally post-032. */}
                        {(
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
                        {model.internal_model && (
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
                        {/* TEST result panel — per row. Success renders the
                            model + the typed answers; failure renders the
                            CLASSIFIED status + the upstream's short message
                            (never a raw body dump). */}
                        {testResult && (
                          <div
                            class={`mt-2 rounded-md border p-2 text-xs ${
                              testResult.success
                                ? 'border-green-700/50 bg-green-900/20'
                                : 'border-red-800/50 bg-red-900/20'
                            }`}
                            role={testResult.success ? 'status' : 'alert'}
                            data-testid={`decision-test-result-${model.id}`}
                          >
                            {testResult.success ? (
                              <>
                                <p
                                  class="text-green-300 font-medium"
                                  data-testid="decision-test-success"
                                >
                                  Decision OK — model {testResult.model || model.id}
                                </p>
                                <ul class="mt-1 space-y-1">
                                  {(testResult.answers ?? []).map((answer, idx) => (
                                    <li
                                      key={`${answer.name ?? 'answer'}-${idx}`}
                                      data-testid="decision-test-answer"
                                    >
                                      <span class="text-gray-200 font-medium">
                                        {answer.type}
                                        {answer.name ? ` · ${answer.name}` : ''}
                                      </span>
                                      {answer.probability !== undefined && (
                                        <span class="text-gray-300">
                                          {' '}— probability {formatProbability(answer.probability)}
                                        </span>
                                      )}
                                      {answer.choice !== undefined && (
                                        <span class="text-gray-300">
                                          {' '}— choice {String(answer.choice)}
                                        </span>
                                      )}
                                      {answer.score !== undefined && (
                                        <span class="text-gray-300">
                                          {' '}— score {answer.score}
                                        </span>
                                      )}
                                      {answer.confidence !== undefined && (
                                        <span class="text-gray-300">
                                          {' '}— confidence {formatProbability(answer.confidence)}
                                        </span>
                                      )}
                                      {answer.refusal && (
                                        <span class="text-gray-300">
                                          {' '}— refused: {answer.refusal}
                                        </span>
                                      )}
                                      {answer.probabilities && answer.probabilities.length > 0 && (
                                        <span class="text-gray-400">
                                          {' '}—{' '}
                                          {answer.probabilities
                                            .map((p) => `${String(p.value)}${p.label ? ` (${p.label})` : ''}: ${formatProbability(p.probability)}`)
                                            .join(', ')}
                                        </span>
                                      )}
                                    </li>
                                  ))}
                                </ul>
                                {testResult.usage && (
                                  <p class="text-gray-400 mt-1">
                                    tokens — in {testResult.usage.input_tokens} / out {testResult.usage.output_tokens}
                                  </p>
                                )}
                              </>
                            ) : (
                              <>
                                <p
                                  class="text-red-300 font-medium"
                                  data-testid="decision-test-error"
                                >
                                  {classifyDecisionTestError(testResult.status)} (HTTP {testResult.status})
                                </p>
                                {testResult.error && (
                                  <p
                                    class="text-gray-300 mt-1 break-words"
                                    data-testid="decision-test-error-message"
                                  >
                                    {truncateDecisionTestMessage(testResult.error)}
                                  </p>
                                )}
                              </>
                            )}
                          </div>
                        )}
                      </div>
                    </div>
                    <div class="flex items-center gap-1 flex-shrink-0 ml-4">
                      {/* Per-row TEST button — fires ONE real decision
                          request through the proxy's decision path and
                          renders the classified outcome in the row. */}
                      <button
                        type="button"
                        onClick={() => handleTest(model)}
                        class={`text-xs font-medium px-2 py-1 rounded-md border transition-colors ${
                          isTesting
                            ? 'border-gray-600 bg-gray-700 text-gray-400 cursor-not-allowed'
                            : 'border-green-700/50 bg-green-900/40 text-green-300 hover:bg-green-800/50'
                        }`}
                        disabled={isTesting}
                        title="Send a real decision request through this model"
                        aria-label={`Test ${model.name}`}
                        data-testid={`decision-test-button-${model.id}`}
                      >
                        {isTesting ? 'Testing...' : 'Test'}
                      </button>
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
