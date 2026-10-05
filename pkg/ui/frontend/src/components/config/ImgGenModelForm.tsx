import { useState, useEffect } from 'preact/hooks';
import type { Credential, CredentialRef, ImgGenModel } from '../../types';
import { getCredentials } from '../../hooks/useApi';
import { MultiCredentialEditor } from './MultiCredentialEditor';

// Hard-coded image-gen-only fields (fe-spec §2.4 "hard-coded in the submit
// handler, single source of truth"). These are NEVER form state — they are
// constants stamped on the submit payload. If BE-D2 ever adds another
// required field for image-gen, it goes here, not in the JSX.
const HARDCODED_KIND = 'image-gen' as const;
const HARDCODED_INTERNAL = true as const;
const HARDCODED_INTERNAL_PROVIDER = 'minimax' as const;
const HARDCODED_EXCLUDE_FROM_ULTIMATE = true as const;
const DEFAULT_BASE_URL = 'https://api.minimax.io/v1';

// Wire payload emitted by the form on submit. The hook layer may stamp
// additional fields (e.g. `kind` here mirrors BE-D2; the addModel wrapper
// re-emits everything as-is). Edit flow: id is required so the hook can
// route the PUT; the parent's updateModel merges by id.
export interface ImgGenModelPayload {
  id: string;
  name: string;
  kind: 'image-gen';
  internal: true;
  internal_provider: 'minimax';
  internal_model: string;
  internal_base_url: string;
  credentials: CredentialRef[];
  enabled: boolean;
  exclude_from_ultimate_switching: true;
}

interface ImgGenModelFormProps {
  mode: 'add' | 'edit';
  // initialData is pre-filtered to kind: 'image-gen' by the parent; the
  // type narrows the structural shape so a chat row can never round-trip
  // into the form (defense in depth on top of the server-side filter).
  initialData?: ImgGenModel;
  onSave: (data: ImgGenModelPayload) => Promise<void>;
  onCancel: () => void;
  onStatus: (status: { type: 'success' | 'error'; message: string } | null) => void;
  onNavigateToCredentials?: () => void;
}

// Helper — pure data shape for the underlying form fields. The hard-coded
// fields are derived from constants above, not stored in state.
interface FormFields {
  id: string;
  name: string;
  internal_model: string;
  internal_base_url: string;
  credentials: CredentialRef[];
  enabled: boolean;
}

// Returns the form's initial state, hydrated from initialData in edit mode.
function makeInitialState(initialData: ImgGenModel | undefined): FormFields {
  if (initialData) {
    return {
      id: initialData.id,
      name: initialData.name,
      internal_model: initialData.internal_model ?? '',
      internal_base_url: initialData.internal_base_url ?? DEFAULT_BASE_URL,
      credentials: (initialData.credentials ?? []).map((c) => ({
        credential_id: c.credential_id,
        weight: c.weight,
        position: c.position,
      })),
      enabled: initialData.enabled,
    };
  }
  return {
    id: '',
    name: '',
    internal_model: '',
    internal_base_url: DEFAULT_BASE_URL,
    credentials: [],
    enabled: true,
  };
}

export function ImgGenModelForm({
  mode,
  initialData,
  onSave,
  onCancel,
  onStatus,
  onNavigateToCredentials,
}: ImgGenModelFormProps) {
  const [fields, setFields] = useState<FormFields>(() => makeInitialState(initialData));
  const [credentials, setCredentials] = useState<Credential[]>([]);
  const [loadingCredentials, setLoadingCredentials] = useState(false);
  const [saving, setSaving] = useState(false);

  // Track per-field validation errors. Renders a red message below the
  // input and ties the input to the message via aria-describedby/aria-invalid
  // (A11Y-2, §4.4 of fe-spec).
  const [errors, setErrors] = useState<{
    name?: string;
    id?: string;
    internal_model?: string;
    credentials?: string;
    credential_provider?: string;
  }>({});

  // Fetch credentials on mount. Per fe-spec §2.4 row "credentials", the
  // MultiCredentialEditor is reused unchanged — we filter the
  // `availableCredentials` to provider === 'minimax' at the form level so
  // the editor itself stays provider-agnostic (and the existing
  // same-provider invariant in MultiCredentialEditor is preserved).
  useEffect(() => {
    const fetchCreds = async () => {
      setLoadingCredentials(true);
      try {
        const data = await getCredentials();
        setCredentials(data || []);
      } catch (e) {
        console.error('Failed to fetch credentials:', e);
      } finally {
        setLoadingCredentials(false);
      }
    };
    fetchCreds();
  }, []);

  // Re-hydrate when initialData changes (e.g. tab switches between
  // add/edit of different models). Also clear stale errors so a fresh
  // mount of the form does not show a prior submit's error message.
  useEffect(() => {
    setFields(makeInitialState(initialData));
    setErrors({});
  }, [mode, initialData]);

  const minimaxCredentials = credentials.filter((c) => c.provider === 'minimax');

  const handleSubmit = async () => {
    // Client-side validation per fe-spec §2.4 row "Client-side validation"
    // and BE-D2. Each check populates a structured error and short-
    // circuits on the first failure so a single submit shows the first
    // offender; we still collect the credentials list check in one
    // pass because it's the cheapest.
    const nextErrors: typeof errors = {};

    if (fields.name.trim() === '') {
      nextErrors.name = 'Display Name is required';
    }
    if (fields.internal_model.trim() === '') {
      nextErrors.internal_model = 'Upstream Model is required';
    }
    if (mode === 'add' && fields.id.trim() === '') {
      nextErrors.id = 'Model ID is required';
    }
    if (fields.credentials.length < 1) {
      nextErrors.credentials = 'At least one credential is required';
    } else {
      const firstMissing = fields.credentials.findIndex((c) => !c.credential_id);
      if (firstMissing !== -1) {
        nextErrors.credentials = 'Pick a credential for the primary row';
      } else {
        // Every credential must resolve to a minimax credential (C-13).
        // The dropdown is filtered to minimax above, but a stale row may
        // carry a now-deleted credential; we resolve the provider through
        // the full credentials list to catch this.
        const nonMinimax = fields.credentials.find((c) => {
          const found = credentials.find((cd) => cd.id === c.credential_id);
          return !found || found.provider !== 'minimax';
        });
        if (nonMinimax) {
          nextErrors.credential_provider = 'All credentials must be MiniMax';
        }
      }
    }

    if (Object.keys(nextErrors).length > 0) {
      setErrors(nextErrors);
      return;
    }

    setErrors({});
    setSaving(true);
    try {
      // internal_base_url defaults silently — fe-spec §2.4 row "internal_base_url"
      // says an empty value falls back to the MiniMax default; no error.
      const baseUrl = fields.internal_base_url.trim() === '' ? DEFAULT_BASE_URL : fields.internal_base_url.trim();
      const payload: ImgGenModelPayload = {
        id: fields.id,
        name: fields.name,
        kind: HARDCODED_KIND,
        internal: HARDCODED_INTERNAL,
        internal_provider: HARDCODED_INTERNAL_PROVIDER,
        internal_model: fields.internal_model,
        internal_base_url: baseUrl,
        credentials: fields.credentials,
        enabled: fields.enabled,
        exclude_from_ultimate_switching: HARDCODED_EXCLUDE_FROM_ULTIMATE,
      };
      await onSave(payload);
    } catch (e) {
      onStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to save image-gen model' });
    } finally {
      setSaving(false);
    }
  };

  return (
    <div
      class="bg-gray-700/50 rounded-lg p-5 border border-gray-600"
      data-testid="imggen-model-form"
    >
      <h3 class="text-lg font-medium text-white mb-4">
        {mode === 'add' ? 'Add Image-Gen Model' : 'Edit Image-Gen Model'}
      </h3>
      <div class="space-y-4">
        {/* Model ID — disabled in edit mode (id is immutable, primary key). */}
        {mode === 'add' && (
          <div>
            <label
              for="imggen-form-id"
              class="block text-sm font-medium text-gray-300 mb-1"
            >
              Model ID <span class="text-red-400">*</span>
            </label>
            <input
              id="imggen-form-id"
              type="text"
              value={fields.id}
              aria-required="true"
              aria-invalid={errors.id ? 'true' : 'false'}
              aria-describedby={errors.id ? 'imggen-form-id-error' : undefined}
              onInput={(e) => setFields((f) => ({ ...f, id: (e.target as HTMLInputElement).value }))}
              class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
              placeholder="e.g., imggen-mini-v1"
              data-testid="imggen-form-id-input"
            />
            {errors.id && (
              <p
                id="imggen-form-id-error"
                class="text-xs text-red-400 mt-1"
                data-testid="imggen-form-id-error"
              >
                {errors.id}
              </p>
            )}
          </div>
        )}

        {/* Display Name */}
        <div>
          <label
            for="imggen-form-name"
            class="block text-sm font-medium text-gray-300 mb-1"
          >
            Display Name <span class="text-red-400">*</span>
          </label>
          <input
            id="imggen-form-name"
            type="text"
            value={fields.name}
            aria-required="true"
            aria-invalid={errors.name ? 'true' : 'false'}
            aria-describedby={errors.name ? 'imggen-form-name-error' : undefined}
            onInput={(e) => setFields((f) => ({ ...f, name: (e.target as HTMLInputElement).value }))}
            class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="e.g., Image Gen MiniMax v1"
            data-testid="imggen-form-name-input"
          />
          {errors.name && (
            <p
              id="imggen-form-name-error"
              class="text-xs text-red-400 mt-1"
              data-testid="imggen-form-name-error"
            >
              {errors.name}
            </p>
          )}
        </div>

        {/* Provider — read-only display (fe-spec §2.4 row 3, decision D9). */}
        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">Provider</label>
          <div
            class="px-3 py-2 bg-gray-800/50 border border-gray-700 rounded-md text-gray-300"
            data-testid="imggen-form-provider"
          >
            MiniMax
          </div>
          <p class="text-xs text-gray-500 mt-1">Image generation is currently supported only for MiniMax.</p>
        </div>

        {/* Upstream Model — required, the alias the upstream provider uses. */}
        <div>
          <label
            for="imggen-form-internal-model"
            class="block text-sm font-medium text-gray-300 mb-1"
          >
            Upstream Model <span class="text-red-400">*</span>
          </label>
          <input
            id="imggen-form-internal-model"
            type="text"
            value={fields.internal_model}
            aria-required="true"
            aria-invalid={errors.internal_model ? 'true' : 'false'}
            aria-describedby={errors.internal_model ? 'imggen-form-internal-model-error' : undefined}
            onInput={(e) => setFields((f) => ({ ...f, internal_model: (e.target as HTMLInputElement).value }))}
            class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder='e.g., "image-01" (the alias the upstream provider uses)'
            data-testid="imggen-form-internal-model-input"
          />
          {errors.internal_model && (
            <p
              id="imggen-form-internal-model-error"
              class="text-xs text-red-400 mt-1"
              data-testid="imggen-form-internal-model-error"
            >
              {errors.internal_model}
            </p>
          )}
        </div>

        {/* Upstream Base URL — optional, defaults to MiniMax default. */}
        <div>
          <label
            for="imggen-form-base-url"
            class="block text-sm font-medium text-gray-300 mb-1"
          >
            Upstream Base URL
          </label>
          <input
            id="imggen-form-base-url"
            type="text"
            value={fields.internal_base_url}
            onInput={(e) => setFields((f) => ({ ...f, internal_base_url: (e.target as HTMLInputElement).value }))}
            class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder={DEFAULT_BASE_URL}
            data-testid="imggen-form-base-url-input"
          />
          <p class="text-xs text-gray-400 mt-1">
            Default: {DEFAULT_BASE_URL} — override to https://api.minimax.cn/v1 for CN
          </p>
        </div>

        {/* Credentials — MultiCredentialEditor reused unchanged, with
            `availableCredentials` filtered to provider === 'minimax' at the
            form level (fe-spec §2.4 row 6). */}
        <div
          data-testid="imggen-form-credentials-section"
          aria-invalid={errors.credentials || errors.credential_provider ? 'true' : 'false'}
          aria-describedby={errors.credentials || errors.credential_provider ? 'imggen-form-credentials-error' : undefined}
        >
          <MultiCredentialEditor
            credentials={fields.credentials}
            availableCredentials={minimaxCredentials}
            loading={loadingCredentials}
            onChange={(next) => setFields((f) => ({ ...f, credentials: next }))}
            onNavigateToCredentials={onNavigateToCredentials}
          />
          {(errors.credentials || errors.credential_provider) && (
            <p
              id="imggen-form-credentials-error"
              class="text-xs text-red-400 mt-1"
              data-testid="imggen-form-credentials-error"
            >
              {errors.credentials || errors.credential_provider}
            </p>
          )}
        </div>

        {/* Enabled toggle — same chrome as ModelsTab.tsx:159-166. */}
        <div>
          <label class="flex items-center gap-2 cursor-pointer">
            <button
              type="button"
              onClick={() => setFields((f) => ({ ...f, enabled: !f.enabled }))}
              class={`w-10 h-6 rounded-full flex-shrink-0 relative transition-colors ${fields.enabled ? 'bg-green-500' : 'bg-gray-500'
                }`}
              title={fields.enabled ? 'Enabled' : 'Disabled'}
              aria-label="Toggle enabled"
              data-testid="imggen-form-enabled-toggle"
            >
              <span class={`absolute top-1 w-4 h-4 bg-white rounded-full transition-all ${fields.enabled ? 'right-1' : 'left-1'
                }`}></span>
            </button>
            <span class="text-gray-300">Enabled (the model accepts requests)</span>
          </label>
        </div>

        {/* Footer — Cancel / Submit, same chrome as ModelForm.tsx:797-811.
            The submit button stays enabled even when the form is invalid:
            a hard `disabled` would prevent happy-dom / browsers from firing
            click events, so the inline validation message could never
            appear. Validation lives in handleSubmit — that is the gate
            (the chat form's chat-sh equivalent also gates in handleSubmit;
            see ModelForm.tsx — we follow the same pattern). The `saving`
            branch is the only true disable, to prevent double-submit. */}
        <div class="flex justify-end gap-3 pt-2">
          <button
            type="button"
            onClick={onCancel}
            class="px-4 py-2 bg-gray-600 hover:bg-gray-500 text-white rounded-md transition-colors text-sm font-medium"
            data-testid="imggen-form-cancel"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={handleSubmit}
            class="px-4 py-2 bg-blue-600 hover:bg-blue-500 text-white rounded-md transition-colors text-sm font-medium disabled:opacity-60 disabled:cursor-not-allowed"
            disabled={saving}
            data-testid="imggen-form-submit"
          >
            {saving ? 'Saving...' : mode === 'add' ? 'Add Image-Gen Model' : 'Save Changes'}
          </button>
        </div>
      </div>
    </div>
  );
}
