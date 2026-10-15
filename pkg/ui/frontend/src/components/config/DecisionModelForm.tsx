import { useState, useEffect } from 'preact/hooks';
import type { Credential, CredentialRef, DecisionModel } from '../../types';
import { getCredentials } from '../../hooks/useApi';
import { MultiCredentialEditor } from './MultiCredentialEditor';

// Hard-coded decision-only fields (Decision Commission 2026-10-15 /
// plan §6.2). These are NEVER form state — they are constants stamped
// on the submit payload. If BE ever adds another required field for
// decision models, it goes here, not in the JSX.
//
// `kind` and `internal: true` mirror ImgGen exactly (per plan §6.2).
// `internal_provider` is the ONLY divergence from ImgGen's hardcoded
// minimax — it's a dropdown over the BE-served provider list filtered
// to the recommended identity B (openai + typesafe). The Q11 default
// (FE form filter ['openai','typesafe']) is wired below; an override
// to identity A (['openai-decision','typesafe']) requires a single
// edit to PROVIDER_OPTIONS.
const HARDCODED_KIND = 'decision' as const;
const HARDCODED_INTERNAL = true as const;
const HARDCODED_EXCLUDE_FROM_ULTIMATE = true as const;

// Identity B (recommended) — Q11 approved. Override A would swap
// 'openai' -> 'openai-decision' here. The two provider options are
// explicitly listed (not derived from the BE list) so the form's
// contract is testable and the operator cannot accidentally pick a
// chat-only provider like minimax or azure.
const PROVIDER_OPTIONS = ['openai', 'typesafe'] as const;
type DecisionProvider = typeof PROVIDER_OPTIONS[number];

// Per-provider default base URL (plan §6.2 / Q11 + Q17). The form
// stamps this on submit when the operator leaves the base-url field
// empty. Operators can still override; this is the fallback only.
const DEFAULT_BASE_URLS: Record<DecisionProvider, string> = {
  openai: 'https://api.openai.com',
  typesafe: 'https://api.typesafe.ai',
};

// Wire payload emitted by the form on submit. The hook layer may stamp
// additional fields (e.g. `kind` here mirrors BE; the addModel wrapper
// re-emits everything as-is). Edit flow: id is required so the hook
// can route the PUT; the parent's updateModel merges by id.
export interface DecisionModelPayload {
  id: string;
  name: string;
  kind: 'decision';
  internal: true;
  internal_provider: DecisionProvider;
  internal_model: string;
  internal_base_url: string;
  credentials: CredentialRef[];
  enabled: boolean;
  exclude_from_ultimate_switching: true;
}

interface DecisionModelFormProps {
  mode: 'add' | 'edit';
  // initialData is pre-filtered to kind: 'decision' by the parent; the
  // type narrows the structural shape so a chat row can never
  // round-trip into the form (defense in depth on top of the
  // server-side filter).
  initialData?: DecisionModel;
  onSave: (data: DecisionModelPayload) => Promise<void>;
  onCancel: () => void;
  onStatus: (status: { type: 'success' | 'error'; message: string } | null) => void;
  onNavigateToCredentials?: () => void;
}

// Helper — pure data shape for the underlying form fields. The
// hard-coded fields are derived from constants above, not stored in
// state. `internal_provider` and the mirrored `internal_base_url`
// are user-editable (dropdown + input); the rest of the constants
// (kind/internal/exclude_from_ultimate_switching) are bound at submit
// time.
interface FormFields {
  id: string;
  name: string;
  internal_provider: DecisionProvider;
  internal_model: string;
  internal_base_url: string;
  credentials: CredentialRef[];
  enabled: boolean;
}

// Returns the form's initial state, hydrated from initialData in edit
// mode. `internal_base_url` defaults to the chosen provider's
// DEFAULT_BASE_URL (no value -> provider fallback at submit time).
function makeInitialState(initialData: DecisionModel | undefined): FormFields {
  if (initialData) {
    const provider = (initialData.internal_provider ?? 'openai') as DecisionProvider;
    const safeProvider: DecisionProvider = (PROVIDER_OPTIONS as readonly string[]).includes(provider)
      ? provider
      : 'openai';
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
    internal_provider: 'openai',
    internal_model: '',
    internal_base_url: DEFAULT_BASE_URLS.openai,
    credentials: [],
    enabled: true,
  };
}

// Filter credentials to the currently selected provider — mirrors the
// MultiCredentialEditor's same-provider invariant and C-13. When the
// user switches the provider dropdown, the credentials editor shows
// only that provider's credentials.
function filterCredentialsByProvider(
  all: Credential[],
  provider: DecisionProvider,
): Credential[] {
  return all.filter((c) => c.provider === provider);
}

export function DecisionModelForm({
  mode,
  initialData,
  onSave,
  onCancel,
  onStatus,
  onNavigateToCredentials,
}: DecisionModelFormProps) {
  const [fields, setFields] = useState<FormFields>(() => makeInitialState(initialData));
  const [credentials, setCredentials] = useState<Credential[]>([]);
  const [loadingCredentials, setLoadingCredentials] = useState(false);
  const [saving, setSaving] = useState(false);

  // Track per-field validation errors. Renders a red message below the
  // input and ties the input to the message via aria-describedby /
  // aria-invalid (A11Y-2, §4.4 of fe-spec).
  const [errors, setErrors] = useState<{
    name?: string;
    id?: string;
    internal_model?: string;
    credentials?: string;
    credential_provider?: string;
    internal_provider?: string;
  }>({});

  // Fetch credentials on mount. Per plan §6.2 the MultiCredentialEditor
  // is reused unchanged — we filter the `availableCredentials` to the
  // currently selected provider at the form level so the editor itself
  // stays provider-agnostic (and the existing same-provider invariant
  // in MultiCredentialEditor is preserved).
  //
  // AbortController mirrors the hook pattern in `useApi.ts` (see e.g.
  // useRequests at useApi.ts:66-103).
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

  // Re-hydrate when initialData changes (e.g. tab switches between
  // add/edit of different models). Also clear stale errors so a fresh
  // mount of the form does not show a prior submit's error message.
  useEffect(() => {
    setFields(makeInitialState(initialData));
    setErrors({});
  }, [mode, initialData]);

  const providerCredentials = filterCredentialsByProvider(credentials, fields.internal_provider);

  const handleProviderChange = (next: DecisionProvider) => {
    setFields((f) => {
      // When the provider changes, refresh the base-url default ONLY
      // if the operator hasn't overridden it (i.e. the current base-url
      // still equals the previous provider's default OR is empty).
      // This matches the ImgGen "default silently" posture.
      const previousDefault = DEFAULT_BASE_URLS[f.internal_provider];
      const isAtDefault = f.internal_base_url === '' || f.internal_base_url === previousDefault;
      const nextBaseUrl = isAtDefault ? DEFAULT_BASE_URLS[next] : f.internal_base_url;
      // Also clear credentials when the provider changes — a stale
      // credential_id from the previous provider cannot round-trip
      // (the next submit's invariant check would reject it).
      return { ...f, internal_provider: next, internal_base_url: nextBaseUrl, credentials: [] };
    });
  };

  const handleSubmit = async () => {
    // Client-side validation per plan §6.2 + BE. Each check populates
    // a structured error and short-circuits on the first failure so a
    // single submit shows the first offender; we still collect the
    // credentials list check in one pass because it's the cheapest.
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
      nextErrors.internal_provider = 'Provider must be openai or typesafe';
    }
    if (fields.credentials.length < 1) {
      nextErrors.credentials = 'At least one credential is required';
    } else {
      const firstMissing = fields.credentials.findIndex((c) => !c.credential_id);
      if (firstMissing !== -1) {
        nextErrors.credentials = 'Pick a credential for the primary row';
      } else {
        // Every credential must resolve to a credential whose
        // provider matches the chosen `internal_provider`. The
        // dropdown is filtered to that provider above, but a stale
        // row may carry a now-deleted credential id; we resolve the
        // provider through the full credentials list to catch this.
        //
        // Race guard: in edit mode, the form may mount BEFORE the
        // credentials fetch resolves. At that point the credentials
        // list is empty, so the resolver sees every credential_id as
        // unknown — same posture as ImgGenModelForm C-13. Skip the
        // check while credentials are still loading so a valid row
        // does not get a spurious error.
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
      // internal_base_url defaults silently — matches the ImgGen
      // posture: an empty value falls back to the chosen-provider
      // default.
      const baseUrl = fields.internal_base_url.trim() === '' ? DEFAULT_BASE_URLS[fields.internal_provider] : fields.internal_base_url.trim();
      const payload: DecisionModelPayload = {
        id: fields.id,
        name: fields.name,
        kind: HARDCODED_KIND,
        internal: HARDCODED_INTERNAL,
        internal_provider: fields.internal_provider,
        internal_model: fields.internal_model,
        internal_base_url: baseUrl,
        credentials: fields.credentials,
        enabled: fields.enabled,
        exclude_from_ultimate_switching: HARDCODED_EXCLUDE_FROM_ULTIMATE,
      };
      await onSave(payload);
    } catch (e) {
      onStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to save decision model' });
    } finally {
      setSaving(false);
    }
  };

  return (
    <div
      class="bg-gray-700/50 rounded-lg p-5 border border-gray-600"
      data-testid="decision-model-form"
    >
      <h3 class="text-lg font-medium text-white mb-4">
        {mode === 'add' ? 'Add Decision Model' : 'Edit Decision Model'}
      </h3>
      <div class="space-y-4">
        {/* Model ID — disabled in edit mode (id is immutable, primary key). */}
        {mode === 'add' && (
          <div>
            <label
              for="decision-form-id"
              class="block text-sm font-medium text-gray-300 mb-1"
            >
              Model ID <span class="text-red-400">*</span>
            </label>
            <input
              id="decision-form-id"
              type="text"
              value={fields.id}
              aria-required="true"
              aria-invalid={errors.id ? 'true' : 'false'}
              aria-describedby={errors.id ? 'decision-form-id-error' : undefined}
              onInput={(e) => setFields((f) => ({ ...f, id: (e.target as HTMLInputElement).value }))}
              class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
              placeholder="e.g., decision-router-v1"
              data-testid="decision-form-id-input"
            />
            {errors.id && (
              <p
                id="decision-form-id-error"
                class="text-xs text-red-400 mt-1"
                data-testid="decision-form-id-error"
              >
                {errors.id}
              </p>
            )}
          </div>
        )}

        {/* Display Name */}
        <div>
          <label
            for="decision-form-name"
            class="block text-sm font-medium text-gray-300 mb-1"
          >
            Display Name <span class="text-red-400">*</span>
          </label>
          <input
            id="decision-form-name"
            type="text"
            value={fields.name}
            aria-required="true"
            aria-invalid={errors.name ? 'true' : 'false'}
            aria-describedby={errors.name ? 'decision-form-name-error' : undefined}
            onInput={(e) => setFields((f) => ({ ...f, name: (e.target as HTMLInputElement).value }))}
            class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="e.g., Decision Router v1"
            data-testid="decision-form-name-input"
          />
          {errors.name && (
            <p
              id="decision-form-name-error"
              class="text-xs text-red-400 mt-1"
              data-testid="decision-form-name-error"
            >
              {errors.name}
            </p>
          )}
        </div>

        {/* Provider — DROPDOWN (Q11 identity B / plan §6.2 / §6.6). This
            is the ONE divergence from the ImgGen form, which hardcodes
            MiniMax. Decision has two providers (openai + typesafe); the
            operator picks one. Default = openai. */}
        <div>
          <label
            for="decision-form-provider"
            class="block text-sm font-medium text-gray-300 mb-1"
          >
            Provider <span class="text-red-400">*</span>
          </label>
          <select
            id="decision-form-provider"
            value={fields.internal_provider}
            aria-required="true"
            aria-invalid={errors.internal_provider ? 'true' : 'false'}
            aria-describedby={errors.internal_provider ? 'decision-form-provider-error' : undefined}
            onChange={(e) => handleProviderChange((e.target as HTMLSelectElement).value as DecisionProvider)}
            class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            data-testid="decision-form-provider-select"
          >
            {PROVIDER_OPTIONS.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </select>
          {errors.internal_provider && (
            <p
              id="decision-form-provider-error"
              class="text-xs text-red-400 mt-1"
              data-testid="decision-form-provider-error"
            >
              {errors.internal_provider}
            </p>
          )}
          <p class="text-xs text-gray-400 mt-1">
            OpenAI and TypeSafe (Jev) are the supported decision providers.
          </p>
        </div>

        {/* Upstream Model — required, the alias the upstream provider uses. */}
        <div>
          <label
            for="decision-form-internal-model"
            class="block text-sm font-medium text-gray-300 mb-1"
          >
            Upstream Model <span class="text-red-400">*</span>
          </label>
          <input
            id="decision-form-internal-model"
            type="text"
            value={fields.internal_model}
            aria-required="true"
            aria-invalid={errors.internal_model ? 'true' : 'false'}
            aria-describedby={errors.internal_model ? 'decision-form-internal-model-error' : undefined}
            onInput={(e) => setFields((f) => ({ ...f, internal_model: (e.target as HTMLInputElement).value }))}
            class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder='e.g., "gpt-6-luna" (the alias the upstream provider uses)'
            data-testid="decision-form-internal-model-input"
          />
          {errors.internal_model && (
            <p
              id="decision-form-internal-model-error"
              class="text-xs text-red-400 mt-1"
              data-testid="decision-form-internal-model-error"
            >
              {errors.internal_model}
            </p>
          )}
        </div>

        {/* Upstream Base URL — optional, defaults to the chosen
            provider's default (plan §6.2 / Q11 + Q17). */}
        <div>
          <label
            for="decision-form-base-url"
            class="block text-sm font-medium text-gray-300 mb-1"
          >
            Upstream Base URL
          </label>
          <input
            id="decision-form-base-url"
            type="text"
            value={fields.internal_base_url}
            onInput={(e) => setFields((f) => ({ ...f, internal_base_url: (e.target as HTMLInputElement).value }))}
            class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder={DEFAULT_BASE_URLS[fields.internal_provider]}
            data-testid="decision-form-base-url-input"
          />
          <p class="text-xs text-gray-400 mt-1">
            Default: {DEFAULT_BASE_URLS[fields.internal_provider]}
          </p>
        </div>

        {/* Credentials — MultiCredentialEditor reused unchanged, with
            `availableCredentials` filtered to the currently selected
            provider at the form level (plan §6.2 row 6). */}
        <div
          data-testid="decision-form-credentials-section"
          aria-invalid={errors.credentials || errors.credential_provider ? 'true' : 'false'}
          aria-describedby={errors.credentials || errors.credential_provider ? 'decision-form-credentials-error' : undefined}
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
              id="decision-form-credentials-error"
              class="text-xs text-red-400 mt-1"
              data-testid="decision-form-credentials-error"
            >
              {errors.credentials || errors.credential_provider}
            </p>
          )}
        </div>

        {/* Enabled toggle — same chrome as ImgGenModelForm. */}
        <div>
          <label class="flex items-center gap-2 cursor-pointer">
            <button
              type="button"
              onClick={() => setFields((f) => ({ ...f, enabled: !f.enabled }))}
              class={`w-10 h-6 rounded-full flex-shrink-0 relative transition-colors ${fields.enabled ? 'bg-green-500' : 'bg-gray-500'
                }`}
              title={fields.enabled ? 'Enabled' : 'Disabled'}
              aria-label="Toggle enabled"
              data-testid="decision-form-enabled-toggle"
            >
              <span class={`absolute top-1 w-4 h-4 bg-white rounded-full transition-all ${fields.enabled ? 'right-1' : 'left-1'
                }`}></span>
            </button>
            <span class="text-gray-300">Enabled (the model accepts requests)</span>
          </label>
        </div>

        {/* Footer — Cancel / Submit, same chrome as the chat form. */}
        <div class="flex justify-end gap-3 pt-2">
          <button
            type="button"
            onClick={onCancel}
            class="px-4 py-2 bg-gray-600 hover:bg-gray-500 text-white rounded-md transition-colors text-sm font-medium"
            data-testid="decision-form-cancel"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={handleSubmit}
            class="px-4 py-2 bg-blue-600 hover:bg-blue-500 text-white rounded-md transition-colors text-sm font-medium disabled:opacity-60 disabled:cursor-not-allowed"
            disabled={saving}
            data-testid="decision-form-submit"
          >
            {saving ? 'Saving...' : mode === 'add' ? 'Add Decision Model' : 'Save Changes'}
          </button>
        </div>
      </div>
    </div>
  );
}
