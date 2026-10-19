import { useState, useEffect } from 'preact/hooks';
import type { Model, GroupModel, GroupMember } from '../../types';
import { GroupMemberEditor } from './GroupMemberEditor';

// Hard-coded group-only fields (load-balance-groups / plan §6.2, G1 +
// G7). These are NEVER form state — they are constants stamped on the
// submit payload, mirroring the DecisionModelForm discipline. If BE
// ever adds another required field for group rows, it goes here, not
// in the JSX.
//
//   kind: 'group'                      — the kind discriminator (G-arm)
//   internal: true                     — G1: routes the row through the
//                                        ~11 internal-dispatch predicates.
//                                        Now the only mode repo-wide
//                                        (remove-external-upstream).
//   exclude_from_ultimate_switching:
//   true                               — G7: explicit enforcement; ultimate
//                                        switching is evaluated on the group
//                                        row pre-rewrite, so the flag shields
//                                        group traffic
const HARDCODED_KIND = 'group' as const;
const HARDCODED_INTERNAL = true as const;
const HARDCODED_EXCLUDE_FROM_ULTIMATE = true as const;

// Wire payload emitted by the form on submit (mirrors the BE Model DTO
// group fields — pkg/ui/server.go Model struct). The hook layer stamps
// nothing further; the parent's addModel/updateModel route it to
// POST/PUT /fe/api/models. Groups carry no credentials by design, so
// the payload never includes credential fields.
export interface GroupModelPayload {
  id: string;
  name: string;
  kind: 'group';
  internal: true;
  members: GroupMember[];
  shadow_model_id: string;
  enabled: boolean;
  exclude_from_ultimate_switching: true;
  fallback_chain: [];
  credentials: [];
}

interface GroupModelFormProps {
  mode: 'add' | 'edit';
  // initialData is pre-filtered to kind: 'group' by the parent; the
  // type narrows the structural shape so a chat row can never
  // round-trip into the form (defense in depth on top of the
  // server-side filter).
  initialData?: GroupModel;
  // One-click wrap pre-fill (Am30): the chat model to wrap. Populates
  // a NEW group form (add mode only) with ID `${id}[${id}]`, a single
  // member of that model, and a shadow claim on that model's ID —
  // the canonical `coding[coding]` day-1 onboarding shape. Form
  // pre-fill convenience ONLY: the operator still presses Add.
  wrapTarget?: Model | null;
  // Chat-kind models only (the parent filters the useModels() list).
  // Feeds BOTH the member picker and the shadow-target picker — G3/G8
  // only ever accept chat-kind targets, so a single filtered list is
  // the correct source for both.
  chatModels: Model[];
  // The current group list — needed for the client-side G8 dual-claim
  // scan (no OTHER enabled group may claim the same shadow_model_id).
  groups: GroupModel[];
  onSave: (data: GroupModelPayload) => Promise<void>;
  onCancel: () => void;
  onStatus: (status: { type: 'success' | 'error'; message: string } | null) => void;
}

// Pure data shape for the editable form fields. The hard-coded fields
// are derived from the constants above, not stored in state.
interface FormFields {
  id: string;
  name: string;
  members: GroupMember[];
  shadow_model_id: string;
  enabled: boolean;
}

// Returns the form's initial state: hydrated from initialData in edit
// mode, pre-filled from wrapTarget in add mode (Am30), empty otherwise.
// `backup_only` round-trips verbatim — a saved group with backup
// members restores them on edit; load-and-save round-trip must not
// drop the flag (D7, group-backup-members).
function makeInitialState(
  initialData: GroupModel | undefined,
  wrapTarget: Model | null | undefined,
): FormFields {
  if (initialData) {
    return {
      id: initialData.id,
      name: initialData.name,
      members: (initialData.members ?? []).map((m) => ({
        model_id: m.model_id,
        weight: m.weight,
        ...(m.backup_only ? { backup_only: true as const } : {}),
      })),
      shadow_model_id: initialData.shadow_model_id ?? '',
      enabled: initialData.enabled,
    };
  }
  if (wrapTarget) {
    // Am30 wrap pre-fill — the canonical `coding[coding]` shape: group
    // ID `${id}[${id}]` (NOT the bare member ID — a duplicate-PK hard
    // block and a G8 self-ID violation), single member, shadow claim
    // on the wrapped model so its traffic routes to the group.
    return {
      id: `${wrapTarget.id}[${wrapTarget.id}]`,
      name: `${wrapTarget.name} group`,
      members: [{ model_id: wrapTarget.id, weight: 1 }],
      shadow_model_id: wrapTarget.id,
      enabled: true,
    };
  }
  return {
    id: '',
    name: '',
    members: [{ model_id: '', weight: 1 }],
    shadow_model_id: '',
    enabled: true,
  };
}

export function GroupModelForm({
  mode,
  initialData,
  wrapTarget,
  chatModels,
  groups,
  onSave,
  onCancel,
  onStatus,
}: GroupModelFormProps) {
  const [fields, setFields] = useState<FormFields>(() => makeInitialState(initialData, wrapTarget));
  const [saving, setSaving] = useState(false);

  // Track per-field validation errors. Renders a red message below the
  // input and ties the input to the message via aria-describedby /
  // aria-invalid (A11Y-2 posture, mirroring DecisionModelForm).
  const [errors, setErrors] = useState<{
    name?: string;
    id?: string;
    members?: string;
    shadow_model_id?: string;
  }>({});

  // Re-hydrate when initialData / wrapTarget change (tab switches
  // between add/edit of different models, or a one-click wrap lands
  // while the form is open). Also clear stale errors so a fresh mount
  // of the form does not show a prior submit's error message.
  useEffect(() => {
    setFields(makeInitialState(initialData, wrapTarget));
    setErrors({});
  }, [mode, initialData, wrapTarget]);

  const handleSubmit = async () => {
    // Client-side mirror of the G-rule set (plan §6.2 / §5.1): each
    // check populates a structured error; the first-offender pattern
    // mirrors DecisionModelForm (collect in one pass, submit blocked).
    const nextErrors: typeof errors = {};

    if (fields.name.trim() === '') {
      nextErrors.name = 'Display Name is required';
    }
    if (mode === 'add' && fields.id.trim() === '') {
      nextErrors.id = 'Group ID is required';
    }
    // G2 — at least one member keeps the group live at every write.
    if (fields.members.length < 1) {
      nextErrors.members = 'At least one member is required';
    } else if (fields.members.length > 16) {
      nextErrors.members = `Members list exceeds max of 16 entries (got ${fields.members.length})`;
    } else {
      const firstUnpicked = fields.members.findIndex((m) => !m.model_id);
      if (firstUnpicked !== -1) {
        nextErrors.members = 'Pick a model for every member row';
      } else {
        // G3 — every member weight must be > 0.
        const badWeight = fields.members.find((m) => !Number.isFinite(m.weight) || m.weight <= 0);
        if (badWeight) {
          nextErrors.members = `Weight for member ${badWeight.model_id} must be > 0`;
        } else {
          // group-backup-members / D2 client-side mirror:
          // at least one member must NOT be marked backup_only.
          // The server validator at
          // pkg/models/group.go ValidateGroupMembers is
          // authoritative — this is the cheap belt-and-suspenders
          // gate that surfaces a clear message before the request
          // round-trips.
          const hasDefault = fields.members.some((m) => m.backup_only !== true);
          if (!hasDefault) {
            nextErrors.members = `At least one non-backup (default) member is required`;
          }
        }
      }
    }
    // G8 client-side mirror (target half): exists, chat-kind (the
    // picker only offers chat rows, but a stale edit value can
    // reference a deleted/never-chat model), not the group's own ID,
    // and not dual-claimed by another ENABLED group. Self-shadow —
    // claiming one of the group's OWN members — is LEGAL and the
    // canonical day-1 onboarding (Am22; not an error).
    const shadow = fields.shadow_model_id.trim();
    if (shadow !== '') {
      if (shadow === fields.id) {
        nextErrors.shadow_model_id = 'Shadow target cannot be the group\'s own ID';
      } else if (!chatModels.some((m) => m.id === shadow)) {
        nextErrors.shadow_model_id = `Shadow target ${shadow} does not exist (must reference an existing chat-kind model)`;
      } else {
        const claimant = groups.find(
          (g) => g.id !== fields.id && g.enabled && (g.shadow_model_id ?? '') === shadow,
        );
        if (claimant) {
          nextErrors.shadow_model_id = `Shadow target ${shadow} is already claimed by enabled group ${claimant.id}`;
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
      const payload: GroupModelPayload = {
        id: fields.id,
        name: fields.name,
        kind: HARDCODED_KIND,
        internal: HARDCODED_INTERNAL,
        members: fields.members.map((m) => ({
          model_id: m.model_id,
          weight: m.weight,
          // Drop the key entirely when false so the JSON form is
          // "absent ⇒ default" — matches the Go omitempty
          // semantics and keeps the D7 backward-compat shape
          // (existing groups without backup_only round-trip with no
          // field present).
          ...(m.backup_only ? { backup_only: true as const } : {}),
        })),
        shadow_model_id: shadow,
        enabled: fields.enabled,
        exclude_from_ultimate_switching: HARDCODED_EXCLUDE_FROM_ULTIMATE,
        fallback_chain: [],
        credentials: [],
      };
      await onSave(payload);
    } catch (e) {
      onStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to save group model' });
    } finally {
      setSaving(false);
    }
  };

  // Self-shadow hint (informational, never an error): the chosen
  // shadow target is one of this group's own members.
  const selfShadow =
    fields.shadow_model_id !== '' &&
    fields.members.some((m) => m.model_id === fields.shadow_model_id);

  return (
    <div
      class="bg-gray-700/50 rounded-lg p-5 border border-gray-600"
      data-testid="group-model-form"
    >
      <h3 class="text-lg font-medium text-white mb-1">
        {mode === 'add' ? 'Add Group Model' : 'Edit Group Model'}
      </h3>
      {/* G1/G7 read-only surfaced constants — the payload always stamps
          these; the operator cannot change them. */}
      <p class="text-xs text-gray-400 mb-4" data-testid="group-form-fixed-constants">
        Virtual load-balancing group — kind: group · internal: true · excluded from ultimate switching (fixed by G1/G7).
      </p>
      <div class="space-y-4">
        {/* Group ID — disabled in edit mode (id is immutable, primary key). */}
        {mode === 'add' && (
          <div>
            <label
              for="group-form-id"
              class="block text-sm font-medium text-gray-300 mb-1"
            >
              Group ID <span class="text-red-400">*</span>
            </label>
            <input
              id="group-form-id"
              type="text"
              value={fields.id}
              aria-required="true"
              aria-invalid={errors.id ? 'true' : 'false'}
              aria-describedby={errors.id ? 'group-form-id-error' : undefined}
              onInput={(e) => setFields((f) => ({ ...f, id: (e.target as HTMLInputElement).value }))}
              class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
              placeholder="e.g., coding[coding]"
              data-testid="group-form-id-input"
            />
            <p class="text-xs text-gray-400 mt-1">
              Clients request this ID (or the shadowed model's ID) to hit the group.
            </p>
            {errors.id && (
              <p
                id="group-form-id-error"
                class="text-xs text-red-400 mt-1"
                data-testid="group-form-id-error"
              >
                {errors.id}
              </p>
            )}
          </div>
        )}

        {/* Display Name */}
        <div>
          <label
            for="group-form-name"
            class="block text-sm font-medium text-gray-300 mb-1"
          >
            Display Name <span class="text-red-400">*</span>
          </label>
          <input
            id="group-form-name"
            type="text"
            value={fields.name}
            aria-required="true"
            aria-invalid={errors.name ? 'true' : 'false'}
            aria-describedby={errors.name ? 'group-form-name-error' : undefined}
            onInput={(e) => setFields((f) => ({ ...f, name: (e.target as HTMLInputElement).value }))}
            class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="e.g., Coding Pool"
            data-testid="group-form-name-input"
          />
          {errors.name && (
            <p
              id="group-form-name-error"
              class="text-xs text-red-400 mt-1"
              data-testid="group-form-name-error"
            >
              {errors.name}
            </p>
          )}
        </div>

        {/* Members — GroupMemberEditor (chat-kind model picker + weight). */}
        <div
          data-testid="group-form-members-section"
          aria-invalid={errors.members ? 'true' : 'false'}
          aria-describedby={errors.members ? 'group-form-members-error' : undefined}
        >
          <label class="block text-sm font-medium text-gray-300 mb-1">
            Members <span class="text-red-400">*</span>
          </label>
          <GroupMemberEditor
            members={fields.members}
            chatModels={chatModels}
            onChange={(next) => setFields((f) => ({ ...f, members: next }))}
          />
          {errors.members && (
            <p
              id="group-form-members-error"
              class="text-xs text-red-400 mt-1"
              data-testid="group-form-members-error"
            >
              {errors.members}
            </p>
          )}
        </div>

        {/* Shadow-target picker (arch R1 Am9 / Q10 / Q19) — clearable
            dropdown over chat-kind models. The empty option IS the
            clear affordance: selecting "None — no shadowing" sends
            shadow_model_id: "" which drops the claim server-side, so
            the shadowed model reappears in /v1/models. */}
        <div>
          <label
            for="group-form-shadow"
            class="block text-sm font-medium text-gray-300 mb-1"
          >
            Shadow Target <span class="text-gray-500">(optional)</span>
          </label>
          <select
            id="group-form-shadow"
            value={fields.shadow_model_id}
            aria-invalid={errors.shadow_model_id ? 'true' : 'false'}
            aria-describedby={errors.shadow_model_id ? 'group-form-shadow-error' : undefined}
            onChange={(e) => setFields((f) => ({ ...f, shadow_model_id: (e.target as HTMLSelectElement).value }))}
            class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            data-testid="group-form-shadow-select"
          >
            <option value="">None — no shadowing</option>
            {chatModels.map((m) => (
              <option key={m.id} value={m.id}>
                {m.name} ({m.id})
              </option>
            ))}
          </select>
          <p class="text-xs text-gray-400 mt-1">
            Claiming a model's ID routes requests for that ID to this group (the claimed model is hidden from /v1/models while the claim is active).
          </p>
          {selfShadow && (
            <p class="text-xs text-blue-300 mt-1" data-testid="group-form-self-shadow-hint">
              Self-shadow: the target is one of this group's members — legal and the canonical day-1 onboarding (e.g., coding[coding]).
            </p>
          )}
          {errors.shadow_model_id && (
            <p
              id="group-form-shadow-error"
              class="text-xs text-red-400 mt-1"
              data-testid="group-form-shadow-error"
            >
              {errors.shadow_model_id}
            </p>
          )}
        </div>

        {/* Enabled toggle — same chrome as the sibling forms. */}
        <div>
          <label class="flex items-center gap-2 cursor-pointer">
            <button
              type="button"
              onClick={() => setFields((f) => ({ ...f, enabled: !f.enabled }))}
              class={`w-10 h-6 rounded-full flex-shrink-0 relative transition-colors ${fields.enabled ? 'bg-green-500' : 'bg-gray-500'
                }`}
              title={fields.enabled ? 'Enabled' : 'Disabled'}
              aria-label="Toggle enabled"
              data-testid="group-form-enabled-toggle"
            >
              <span class={`absolute top-1 w-4 h-4 bg-white rounded-full transition-all ${fields.enabled ? 'right-1' : 'left-1'
                }`}></span>
            </button>
            <span class="text-gray-300">Enabled (the group accepts requests)</span>
          </label>
        </div>

        {/* Footer — Cancel / Submit, same chrome as the sibling forms.
            `saving` is the submit race guard: exactly one in-flight
            submit; the button disables until the promise settles. */}
        <div class="flex justify-end gap-3 pt-2">
          <button
            type="button"
            onClick={onCancel}
            class="px-4 py-2 bg-gray-600 hover:bg-gray-500 text-white rounded-md transition-colors text-sm font-medium"
            data-testid="group-form-cancel"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={handleSubmit}
            class="px-4 py-2 bg-blue-600 hover:bg-blue-500 text-white rounded-md transition-colors text-sm font-medium disabled:opacity-60 disabled:cursor-not-allowed"
            disabled={saving}
            data-testid="group-form-submit"
          >
            {saving ? 'Saving...' : mode === 'add' ? 'Add Group Model' : 'Save Changes'}
          </button>
        </div>
      </div>
    </div>
  );
}
