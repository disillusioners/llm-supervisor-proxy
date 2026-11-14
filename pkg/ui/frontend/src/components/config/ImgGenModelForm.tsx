import { useState, useEffect } from 'preact/hooks';
import type { Credential, CredentialRef, ImgGenModel } from '../../types';
import { getCredentials } from '../../hooks/useApi';
import { MultiCredentialEditor } from './MultiCredentialEditor';

// Image-gen providers — the BE imggen lane ships MiniMax and 9router.
// This allowlist mirrors the BE kind gate (pkg/models/config.go +
// pkg/ui/server.go) so the FE cannot stamp a provider the BE rejects.
const PROVIDER_OPTIONS = ['minimax', '9router'] as const;
type ImgGenProvider = typeof PROVIDER_OPTIONS[number];

// Hard-coded image-gen-only fields (fe-spec §2.4 "hard-coded in the submit
// handler, single source of truth"). These are NEVER form state — they are
// constants stamped on the submit payload. If BE-D2 ever adds another
// required field for image-gen, it goes here, not in the JSX.
// `internal: true` is now the only mode repo-wide — the
// remove-external-upstream commission retired the external tick.
const HARDCODED_KIND = 'image-gen' as const;
const HARDCODED_INTERNAL = true as const;

// Per-provider default base URL (mirrors the BE credential default for
// minimax and the in-cluster 9router endpoint). The form stamps this on
// submit when the operator leaves the base-url field empty.
const DEFAULT_BASE_URLS: Record<ImgGenProvider, string> = {
  minimax: 'https://api.minimax.io/v1',
  '9router': 'http://ninerouter.9router.svc.cluster.local:20128/v1',
};

// Wire payload emitted by the form on submit. The hook layer may stamp
// additional fields (e.g. `kind` here mirrors BE-D2; the addModel wrapper
// re-emits everything as-is). Edit flow: id is required so the hook can
// route the PUT; the parent's updateModel merges by id.
export interface ImgGenModelPayload {
  id: string;
  name: string;
  kind: 'image-gen';
  internal: true;
  internal_provider: ImgGenProvider;
  internal_model: string;
  internal_base_url: string;
  credentials: CredentialRef[];
  enabled: boolean;
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
  internal_provider: ImgGenProvider;
  internal_model: string;
  internal_base_url: string;
  credentials: CredentialRef[];
  enabled: boolean;
}

// Returns the form's initial state, hydrated from initialData in edit mode.
function makeInitialState(initialData: ImgGenModel | undefined): FormFields {
  if (initialData) {
    const provider = (initialData.internal_provider ?? 'minimax') as ImgGenProvider;
    const safeProvider: ImgGenProvider = (PROVIDER_OPTIONS as readonly string[]).includes(provider)
      ? provider
      : 'minimax';
    return {
      id: initialData.id,
      name: initialData.name,
      internal_provider: safeProvider,
      internal_model: initialData.internal_model ?? '',
      internal_base_url: initialData.internal_base_url ?? DEFAULT_BASE_URLS[safeProvider],
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
    internal_provider: 'minimax',
    internal_model: '',
    internal_base_url: DEFAULT_BASE_URLS.minimax,
    credentials: [],
    enabled: true,
  };
}

// Filter credentials to the currently selected provider — mirrors the
// MultiCredentialEditor's same-provider invariant and C-13.
function filterCredentialsByProvider(all: Credential[], provider: ImgGenProvider): Credential[] {
  return all.filter((c) => c.provider === provider);
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
    internal_provider?: string;
  }>({});

  // Fetch credentials on mount.
  useEffect(() => {
    const controller = new AbortController();
    const fetchCreds = async () => {
      setLoadingCredentials(true);
      try {
        const data = await getCredentials();
        if (!controller.signal.aborted) {
          setCredentials(data || []);
        }
      } catch (e) {
        if (controller.signal.aborted) return;
        console.error('Failed to fetch credentials:', e);
      } finally {
        if (!controller.signal.aborted) setLoadingCredentials(false);
      }
    };
    fetchCreds();
    return () => controller.abort();
  }, []);

  // Re-hydrate when initialData changes.
  useEffect(() => {
    setFields(makeInitialState(initialData));
    setErrors({});
  }, [mode, initialData]);

  const providerCredentials = filterCredentialsByProvider(credentials, fields.internal_provider);

  const handleProviderChange = (next: ImgGenProvider) => {
    setFields((f) => {
      const previousDefault = DEFAULT_BASE_URLS[f.internal_provider];
      const isAtDefault = f.internal_base_url === '' || f.internal_base_url === previousDefault;
      const nextBaseUrl = isAtDefault ? DEFAULT_BASE_URLS[next] : f.internal_base_url;
      return { ...f, internal_provider: next, internal_base_url: nextBaseUrl, credentials: [] };
    });
  };

  const handleSubmit = async () => {
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
    if (!(PROVIDER_OPTIONS as readonly string[]).includes(fields.internal_provider)) {
      nextErrors.internal_provider = 'Provider must be minimax or 9router';
    }
    if (fields.credentials.length < 1) {
      nextErrors.credentials = 'At least one credential is required';
    } else {
      const firstMissing = fields.credentials.findIndex((c) => !c.credential_id);
      if (firstMissing !== -1) {
        nextErrors.credentials = 'Pick a credential for the primary row';
      } else {
        if (!loadingCredentials) {
          const expected = fields.internal_provider;
          const nonMatching = fields.credentials.find((c) => {
            const found = credentials.find((cd) => cd.id === c.credential_id);
            return !found || found.provider !== expected;
          });
          if (nonMatching) {
            nextErrors.credential_provider = `All credentials must match the selected provider (${expected})`;
          }
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
      const baseUrl = fields.internal_base_url.trim() === '' ? DEFAULT_BASE_URLS[fields.internal_provider] : fields.internal_base_url.trim();
      const payload: ImgGenModelPayload = {
        id: fields.id,
        name: fields.name,
        kind: HARDCODED_KIND,
        internal: HARDCODED_INTERNAL,
        internal_provider: fields.internal_provider,
        internal_model: fields.internal_model,
        internal_base_url: baseUrl,
        credentials: fields.credentials,
        enabled: fields.enabled,
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

        {/* Provider — dropdown (minimax or 9router). */}
        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">Provider</label>
          <select
            value={fields.internal_provider}
            onInput={(e) => handleProviderChange((e.target as HTMLSelectElement).value as ImgGenProvider)}
            class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            data-testid="imggen-form-provider"
          >
            {PROVIDER_OPTIONS.map((p) => (
              <option key={p} value={p}>{p}</option>
            ))}
          </select>
          <p class="text-xs text-gray-500 mt-1">Image generation is supported for MiniMax and 9router.</p>
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

        {/* Upstream Base URL — optional, defaults to the selected provider's default. */}
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
            placeholder={DEFAULT_BASE_URLS[fields.internal_provider]}
            data-testid="imggen-form-base-url-input"
          />
          <p class="text-xs text-gray-400 mt-1">
            Default: {DEFAULT_BASE_URLS[fields.internal_provider]} — override for CN / alternate host
          </p>
        </div>

        {/* Credentials — MultiCredentialEditor reused unchanged, with
            `availableCredentials` filtered to the selected provider. */}
        <div
          data-testid="imggen-form-credentials-section"
          aria-invalid={errors.credentials || errors.credential_provider ? 'true' : 'false'}
          aria-describedby={errors.credentials || errors.credential_provider ? 'imggen-form-credentials-error' : undefined}
        >
          <MultiCredentialEditor
            credentials={fields.credentials}
            availableCredentials={providerCredentials}
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
              class={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${fields.enabled ? 'bg-blue-600' : 'bg-gray-600'}`}
              data-testid="imggen-form-enabled-toggle"
            >
              <span class={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${fields.enabled ? 'translate-x-6' : 'translate-x-1'}`} />
            </button>
            <span class="text-sm text-gray-300">Enabled</span>
          </label>
        </div>

        <div class="flex gap-3 pt-2">
          <button
            type="button"
            onClick={handleSubmit}
            disabled={saving}
            class="px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 text-white rounded-md text-sm font-medium transition-colors"
            data-testid="imggen-form-submit"
          >
            {saving ? 'Saving...' : (mode === 'add' ? 'Create Model' : 'Save Changes')}
          </button>
          <button
            type="button"
            onClick={onCancel}
            class="px-4 py-2 bg-gray-600 hover:bg-gray-700 text-white rounded-md text-sm font-medium transition-colors"
            data-testid="imggen-form-cancel"
          >
            Cancel
          </button>
        </div>
      </div>
    </div>
  );
}
