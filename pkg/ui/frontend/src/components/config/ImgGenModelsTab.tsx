import { useState } from 'preact/hooks';
import type { Model, ImgGenModel, ImgGenTestResult } from '../../types';
import { escapeHtml } from '../../utils/helpers';
import { testImgGenModel } from '../../hooks/useApi';
import { ImgGenModelForm, type ImgGenModelPayload } from './ImgGenModelForm';
import { classifyImgGenProbeError, truncateProbeTestMessage } from './probeErrorClasses';

interface ImgGenModelsTabProps {
  // Server-side pre-filtered to kind: 'image-gen' by the parent hook
  // (useImgGenModels hits /fe/api/models?kind=image-gen). The tab itself
  // never re-filters client-side — the BE is the source of truth.
  models: Model[];
  onAddModel: (model: Omit<Model, 'id'> & { id: string }) => Promise<void>;
  onUpdateModel: (id: string, updates: Partial<Model>) => Promise<void>;
  onDeleteModel: (id: string) => Promise<void>;
  onToggleModel: (model: Model) => Promise<void>;
  setStatus: (status: { type: 'success' | 'error'; message: string; restartRequired?: boolean } | null) => void;
  onNavigateToCredentials?: () => void;
  // Error surfacing (fe-spec §2.7, A11Y-3). The hook catches the fetch
  // error and surfaces the message; the tab renders an alert-role banner
  // and offers a Retry button. Both are optional because the parent
  // (App.tsx + SettingsPage) does not strictly need to thread the error
  // through — the tab degrades silently if neither is provided.
  fetchError?: string | null;
  onRetry?: () => void;
}

// Toast/success message copy — per fe-spec §2.3 row "toast copy swap".
const TOAST = {
  added: 'Image-gen model added successfully',
  updated: 'Image-gen model updated successfully',
  deleted: 'Image-gen model deleted successfully',
  toggled: 'Image-gen model toggled successfully',
} as const;

// ─────────────────────────────────────────────────────────────────────────────
// Image-gen TEST button — per-row probe.
//
// The row's Test button fires ONE real image generation through the
// proxy's image-gen path (POST /fe/api/image_generation/test) and
// renders the outcome inline:
//
//   - success → the generated image (the upstream's CDN URL, rendered
//     directly — no second proxy hop, no base64) + the probe's
//     metadata (provider / model / image count / duration),
//   - failure → a CLASSIFIED message (the upstream's own error status
//     — 401 auth, 402 funds, 429 rate limited — or the provider's
//     business rejection) plus the upstream's short message. NEVER a
//     raw body dump.
//
// The classified status + code come from the BE (see
// pkg/ui/handlers_imggen.go); the labels are shared with the decision
// probe (probeErrorClasses.ts).
//
// The upstream's image URLs are CDN locators whose lifetime is NOT
// recorded anywhere in the repo — the FE renders them immediately and
// does not promise persistence.
// ─────────────────────────────────────────────────────────────────────────────

// IMGGEN_TEST_COST_HINT is the tooltip on the Test button.
//
// A probe is a REAL, provider-billed generation. The figure is an
// APPROXIMATE MiniMax rate (it is a hint, not a quote — no pricing is
// recorded in this repo).
const IMGGEN_TEST_COST_HINT = 'Generate one real image (~$0.0035 per test)';

// imageFormatFromUrl derives the image format from the CDN URL's
// extension (the upstream returns URLs, not bytes — the byte size is
// NOT available without a second fetch, which the proxy deliberately
// never does).
function imageFormatFromUrl(url: string): string {
  const withoutQuery = url.split('?')[0];
  const dot = withoutQuery.lastIndexOf('.');
  if (dot < 0 || dot === withoutQuery.length - 1) return '';
  const ext = withoutQuery.slice(dot + 1).toLowerCase();
  // Only the formats the upstream actually serves.
  if (ext === 'png' || ext === 'jpg' || ext === 'jpeg' || ext === 'webp') return ext;
  return '';
}

// isRenderableImageURL guards the <img src>: the URL is UPSTREAM-
// provided, so only http(s) URLs are rendered (a `javascript:` or
// `data:` URL must never reach the DOM).
function isRenderableImageURL(url: string): boolean {
  return url.startsWith('https://') || url.startsWith('http://');
}

export function ImgGenModelsTab({
  models,
  onAddModel,
  onUpdateModel,
  onDeleteModel,
  onToggleModel,
  setStatus,
  onNavigateToCredentials,
  fetchError,
  onRetry,
}: ImgGenModelsTabProps) {
  const [showForm, setShowForm] = useState(false);
  const [formMode, setFormMode] = useState<'add' | 'edit'>('add');
  const [modelToEdit, setModelToEdit] = useState<ImgGenModel | undefined>(undefined);
  const [modelToDelete, setModelToDelete] = useState<Model | null>(null);
  const [deleting, setDeleting] = useState(false);
  // Per-row TEST state: the ids of the rows whose probe is currently
  // in flight (SEVERAL rows may probe concurrently — each row owns
  // its own busy flag) + the last result per row id (the panel stays
  // until the next probe on that row replaces it).
  const [testingIds, setTestingIds] = useState<string[]>([]);
  const [testResults, setTestResults] = useState<Record<string, ImgGenTestResult>>({});

  const handleOpenAdd = () => {
    setModelToEdit(undefined);
    setFormMode('add');
    setShowForm(true);
    setStatus(null);
  };

  const handleOpenEdit = (model: Model) => {
    // Cast is safe — parent hook already filtered to image-gen; the form
    // never asks for chat-shaped fields.
    setModelToEdit(model as ImgGenModel);
    setFormMode('edit');
    setShowForm(true);
    setStatus(null);
  };

  const handleSave = async (payload: ImgGenModelPayload) => {
    try {
      if (formMode === 'add') {
        // Strip the local-only wrapper to a wire-shaped Model. The form
        // has already stamped kind/internal/internal_provider/
        // exclude_from_ultimate_switching (internal is now
        // unconditionally true repo-wide — no external upstream), so the
        // parent's addModel just forwards the body to POST
        // /fe/api/models. The Model type requires `fallback_chain:
        // string[]`; image-gen models never have a fallback chain, so we
        // send an empty array (the BE-D2 validator rejects any non-empty
        // value).
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
        const { id, name, internal_model, internal_base_url, credentials, enabled, exclude_from_ultimate_switching } = payload;
        await onUpdateModel(id, {
          name,
          enabled,
          internal_model,
          internal_base_url,
          credentials,
          exclude_from_ultimate_switching,
          // kind/internal/internal_provider are immutable in edit — the form
          // does not surface them and the BE rejects a kind flip anyway.
          // (internal is now unconditionally true repo-wide.)
        });
        setStatus({ type: 'success', message: TOAST.updated });
      }
      setShowForm(false);
      setModelToEdit(undefined);
    } catch (e) {
      setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to save image-gen model' });
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
      setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to delete image-gen model' });
    } finally {
      setDeleting(false);
    }
  };

  // handleTest fires ONE real image generation for the row and stores
  // the classified result under the row id (per-row state — a probe
  // on one row never touches another row's panel).
  //
  // The busy flag is PER ROW: two rows may probe concurrently and
  // each keeps its own "Testing..." state. A single global slot
  // would re-enable the first row's button while its probe was still
  // in flight (a second click = a duplicate billed call) and the
  // first row's finally{} would clear the second row's busy state.
  //
  // (The BE additionally runs ONE probe at a time process-wide — a
  // concurrent probe is rejected with the classified code "busy".)
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
      const result = await testImgGenModel(model.id);
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
            <h3 class="text-white font-medium">Image-Gen Models</h3>
            <button
              type="button"
              onClick={handleOpenAdd}
              class="bg-blue-600 hover:bg-blue-500 text-white text-sm font-medium py-1.5 px-3 rounded-md transition-colors flex items-center gap-1"
            >
              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 6v6m0 0v6m0-6h6m-6 0H6" />
              </svg>
              Add Image-Gen Model
            </button>
          </div>

          {/* Error banner (fe-spec §2.7 row "Network 5xx / 4xx on list fetch").
              A11Y-3: the banner carries role="alert" so screen readers
              announce it on appearance. The banner is conditionally
              mounted — it does not appear when there is no error. */}
          {fetchError && (
            <div
              class="bg-red-900/30 border border-red-800/50 rounded-md p-3 text-sm text-red-300 flex items-center justify-between"
              role="alert"
              data-testid="imggen-fetch-error"
            >
              <span>Failed to load image-gen models. {fetchError}</span>
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

          {/* Models list — mirrors ModelsTab chrome, with chat-only chips removed.
              Per fe-spec §2.3 row content: enabled toggle, name, internal_provider
              badge (🖼️ prefix), id chip, internal_model badge, credentials count
              line, internal_base_url line, edit/delete buttons. */}
          <div class="space-y-2" data-testid="imggen-models-list">
            {models.length === 0 ? (
              <div
                class="bg-gray-700/50 rounded-md p-6 border border-gray-700 border-dashed flex flex-col items-center justify-center"
                data-testid="imggen-empty-state"
              >
                <svg class="w-10 h-10 text-gray-500 mb-2" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z" />
                </svg>
                <p class="text-gray-400 text-sm">No image-gen models configured</p>
                <p class="text-gray-500 text-xs mt-1">Add your first image-gen model to enable image generation.</p>
              </div>
            ) : (
              models.map((model) => {
                const credCount = model.credentials?.length ?? 0;
                const testResult = testResults[model.id];
                const isTesting = testingIds.includes(model.id);
                const firstImageUrl = testResult?.image_urls?.find(isRenderableImageURL);
                const imageFormat = firstImageUrl ? imageFormatFromUrl(firstImageUrl) : '';
                return (
                  <div
                    key={model.id}
                    data-testid="imggen-model-row"
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
                              <span aria-hidden="true">🖼️</span>
                              {model.internal_provider || 'minimax'}
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
                            (per fe-spec §2.3 row content "If 0, render the
                            line in text-yellow-400 with copy 'No credentials
                            — model will not function'"). */}
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
                            generated image + the probe metadata; failure
                            renders the CLASSIFIED status + the upstream's
                            short message (never a raw body dump). */}
                        {testResult && (
                          <div
                            class={`mt-2 rounded-md border p-2 text-xs ${
                              testResult.success
                                ? 'border-green-700/50 bg-green-900/20'
                                : 'border-red-800/50 bg-red-900/20'
                            }`}
                            role={testResult.success ? 'status' : 'alert'}
                            data-testid={`imggen-test-result-${model.id}`}
                          >
                            {testResult.success ? (
                              <>
                                <p
                                  class="text-green-300 font-medium"
                                  data-testid="imggen-test-success"
                                >
                                  Image OK — {testResult.image_count ?? 0} image{(testResult.image_count ?? 0) === 1 ? '' : 's'} generated
                                </p>
                                {firstImageUrl && (
                                  <img
                                    src={firstImageUrl}
                                    alt={`Sample image generated by ${testResult.model || model.id}`}
                                    class="mt-1 h-24 w-24 object-contain rounded border border-gray-600/50 bg-gray-800/50"
                                    loading="lazy"
                                    data-testid="imggen-test-image"
                                  />
                                )}
                                <p
                                  class="text-gray-400 mt-1 break-words"
                                  data-testid="imggen-test-meta"
                                >
                                  {testResult.provider || 'unknown provider'} · {testResult.model || model.id} · {testResult.image_count ?? 0} image(s) · {testResult.duration_ms ?? 0} ms
                                  {imageFormat ? ` · ${imageFormat}` : ''}
                                </p>
                              </>
                            ) : (
                              <>
                                <p
                                  class="text-red-300 font-medium"
                                  data-testid="imggen-test-error"
                                >
                                  {classifyImgGenProbeError(testResult.status, testResult.code)} (HTTP {testResult.status})
                                </p>
                                {testResult.error && (
                                  <p
                                    class="text-gray-300 mt-1 break-words"
                                    data-testid="imggen-test-error-message"
                                  >
                                    {truncateProbeTestMessage(testResult.error)}
                                  </p>
                                )}
                              </>
                            )}
                          </div>
                        )}
                      </div>
                    </div>
                    <div class="flex items-center gap-1 flex-shrink-0 ml-4">
                      {/* Per-row TEST button — fires ONE real image
                          generation through the proxy's image-gen path
                          and renders the classified outcome in the
                          row. The probe is billable — the tooltip
                          says so. */}
                      <button
                        type="button"
                        onClick={() => handleTest(model)}
                        class={`text-xs font-medium px-2 py-1 rounded-md border transition-colors ${
                          isTesting
                            ? 'border-gray-600 bg-gray-700 text-gray-400 cursor-not-allowed'
                            : 'border-green-700/50 bg-green-900/40 text-green-300 hover:bg-green-800/50'
                        }`}
                        disabled={isTesting}
                        title={IMGGEN_TEST_COST_HINT}
                        aria-label={`Test ${model.name}`}
                        data-testid={`imggen-test-button-${model.id}`}
                      >
                        {isTesting ? 'Testing...' : 'Test'}
                      </button>
                      <button
                        type="button"
                        onClick={() => handleOpenEdit(model)}
                        class="text-gray-400 hover:text-blue-400 transition-colors p-1.5 rounded-md hover:bg-gray-600/50"
                        title="Edit image-gen model"
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
                        title="Delete image-gen model"
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
        <ImgGenModelForm
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

      {/* Delete Confirmation Dialog — mirrors ModelsTab.tsx:268-305 with the
          fe-spec §2.8 copy swaps: title "Delete Image-Gen Model", body
          interpolates model.name, identical chrome. */}
      {modelToDelete && (
        <div
          class="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-[60]"
          onClick={() => setModelToDelete(null)}
          data-testid="imggen-delete-modal"
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
            <h3 class="text-xl font-semibold text-white mb-2">Delete Image-Gen Model</h3>
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
                data-testid="imggen-delete-confirm"
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
