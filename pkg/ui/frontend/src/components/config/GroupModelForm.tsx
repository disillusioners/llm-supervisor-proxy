import { useState, useEffect } from 'preact/hooks';
import type { Model, GroupModel, GroupMember, GroupLane } from '../../types';
import { GroupMemberEditor } from './GroupMemberEditor';
import { inferGroupLane } from './groupLane';

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
const HARDCODED_KIND = 'group' as const;
const HARDCODED_INTERNAL = true as const;

// ── Lane-aware copy ────────────────────────────────────────────────────────
// ONE component, TWO lanes (the ruling's contract 26 — never a fork).
// The chat-lane strings are the historical text BYTE-FOR-BYTE
// (ADR-9 / AC-R12: this release must not change text-lane behaviour);
// the decision-lane strings drop the promises that are false on that
// lane — there is NO fallback chain and NO racing engine there
// (ADR-11 OQ-2 / OQ-3 option (a)), so a member's resilience is
// per-credential attempts WITHIN the member.
const SHADOW_TARGET_MISSING: Record<GroupLane, string> = {
  chat: 'does not exist (must reference an existing chat-kind model)',
  decision: 'does not exist (must reference an existing decision-kind model)',
  'image-gen': 'does not exist (must reference an existing image-gen-kind model)',
};

const FIXED_CONSTANTS_COPY: Record<GroupLane, string> = {
  chat: 'Virtual load-balancing group — kind: group · internal: true · backup-only members serve as last-resort fallback in the race chain.',
  decision: 'Decision group — kind: group · internal: true · backup-only members are recourse-tier candidates (served only after every default member has failed). No fallback chain and no racing on this lane.',
  // The image-gen lane (imggen-groups) states what the lane ACTUALLY
  // builds: the members fail over across the group and each member
  // rotates through its own credentials. No fallback chain and no
  // racing on this lane.
  'image-gen':
    'Image-gen group — kind: group · internal: true · backup-only members are recourse-tier candidates (served only after every default member has failed). Members fail over across the group, each rotating through its own credentials. No fallback chain and no racing on this lane.',
};

// The lane-consistency block message — it NAMES THE OTHER SURFACE so the
// operator is routed to the right tab (the FE's lane split is a cached,
// client-side inference, so a group can be mis-filed for a while).
const LANE_GUARD_MESSAGE: Record<GroupLane, string> = {
  chat: 'This is a decision group — edit it under Decision Models → Group.',
  // imggen-groups (phase 3): the top-level "Group Models" tab RETIRED
  // — the chat lane's group surface is now the Text container's Group
  // child (Models → Group). A navigation pointer to a tab that no
  // longer exists changes the operator's ability to act, so this ONE
  // decision-lane string is reworded to the post-consolidation home.
  // It mirrors the chat arm's established "Decision Models → Group"
  // shape. This is the ONLY user-facing string delta in the phase (the
  // chat-lane copy is frozen absolutely).
  decision: 'This is a chat group — edit it under Models → Group.',
  // The image-gen surface can host EITHER of the two older lanes (the
  // lane is inferred from the members), so the static string routes
  // BOTH homes.
  'image-gen':
    'This is a chat or decision group — edit it under Models → Group (chat) or Decision Models → Group (decision).',
};

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
  // Which lane this group belongs to. REQUIRED (not defaulted): a
  // defaulted lane would let a call site silently fall back to 'chat'
  // and render a chat-only picker for a decision group — a visible but
  // confusing failure. A missed call site is a compile error.
  lane: GroupLane;
  // The models OF THIS LANE (the parent picks the feed: chat-kind rows
  // for the chat lane, the server-prefiltered ?kind=decision list for
  // the decision lane). Feeds BOTH the member picker and the
  // shadow-target picker — G3/G8 only ever accept a target of the
  // group's own lane, so a single filtered list is the correct source
  // for both.
  laneModels: Model[];
  // model_id → kind index over the CURRENT model rows — the SAME
  // index the parent builds for the client-side lane split. The
  // lane-consistency guard uses it to infer the STORED group's lane
  // with the same helper the split uses (so the guard and the split
  // can never disagree). Optional: absent, the guard falls back to the
  // conservative member-membership rule.
  kindIndex?: Map<string, string>;
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
  lane,
  laneModels,
  kindIndex,
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
    lane?: string;
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
    // NEW — the lane-consistency guard (edit mode only).
    //
    // The FE's lane split is a CACHED, client-side inference, so a
    // group can be mis-filed into the wrong tab. Editing it there
    // would rewrite its members to the other lane's models — a LEGAL
    // write under the widened rules, so the server cannot detect it.
    // The guard closes that hole at the FE, cheaply, without a BE
    // change. It is defence-in-depth: the server's own lane checks
    // still decide every write.
    //
    // The stored group's lane is computed with the SAME helper the
    // parent uses for the split (inferGroupLane), so the guard and the
    // split can never disagree. The helper mirrors the server's fold
    // (pkg/models/group_lane.go :95-101 over
    // pkg/store/database/store_group_rewrite.go :369-385): a member
    // the FE cannot resolve (a vanished row) and a member whose kind
    // belongs to no lane (image-gen / group) carry NO lane evidence
    // and are skipped — the SURVIVING members decide.
    //   - kindIndex supplied → block ONLY when the stored lane is
    //     KNOWN and differs from this form's lane. An UNKNOWN lane
    //     (a MIXED surviving set or an EMPTY surviving set) is NOT
    //     blocked: it is the row the operator most needs to REPAIR,
    //     and blocking it would leave delete + recreate as the only
    //     path.
    //   - kindIndex absent → the conservative fallback: a stored
    //     member that is not one of this lane's models means the
    //     stored group belongs to the other lane.
    if (mode === 'edit' && initialData) {
      const storedMembers = initialData.members ?? [];
      let blocked = false;
      if (kindIndex) {
        const storedLane = inferGroupLane(initialData, kindIndex);
        blocked = storedLane !== null && storedLane !== lane;
      } else {
        blocked = storedMembers.some(
          (m) => m.model_id !== '' && !laneModels.some((lm) => lm.id === m.model_id),
        );
      }
      if (blocked) {
        nextErrors.lane = LANE_GUARD_MESSAGE[lane];
      }
    }

    // NEW — the homogeneity mirror (the DECISION lane only).
    //
    // The picker makes a mixed member list unconstructible, but a
    // PRE-EXISTING mixed list (a stale row left over from an edit of
    // a group on the other lane, or a retyped member) would still be
    // savable through this form. The server's write-time homogeneity
    // rule rejects it — this mirror surfaces the reason before the
    // round trip.
    //
    // LANE-GATED (ADR-9 / AC-R12): the text lane's behaviour is
    // byte-untouched by this release, so the mirror only fires on the
    // decision lane.
    if (!nextErrors.lane && lane === 'decision' && nextErrors.members === undefined) {
      // A member of the decision lane must be a DECISION-KIND model of
      // this lane's feed — the same rule the picker filters on (minus
      // the enabled clause: an inactive decision model is still
      // decision-KIND, so this mirror is about the member's KIND, not
      // its servability).
      const foreign = fields.members.find(
        (m) => m.model_id !== '' && !laneModels.some((lm) => lm.id === m.model_id && lm.kind === 'decision'),
      );
      if (foreign) {
        nextErrors.members = `Member ${foreign.model_id} is not a decision-kind model`;
      }
    }

    // G8 client-side mirror (target half): exists, of this lane's kind
    // (the picker only offers lane rows, but a stale edit value can
    // reference a deleted/never-lane model), not the group's own ID,
    // and not dual-claimed by another ENABLED group. Self-shadow —
    // claiming one of the group's OWN members — is LEGAL and the
    // canonical day-1 onboarding (Am22; not an error).
    const shadow = fields.shadow_model_id.trim();
    if (shadow !== '') {
      if (shadow === fields.id) {
        nextErrors.shadow_model_id = 'Shadow target cannot be the group\'s own ID';
      } else if (!laneModels.some((m) => m.id === shadow)) {
        nextErrors.shadow_model_id = `Shadow target ${shadow} ${SHADOW_TARGET_MISSING[lane]}`;
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
      {/* Read-only surfaced constants — the payload always stamps
          these; the operator cannot change them. LANE-AWARE: the chat
          lane's backup-only members are last-resort fallbacks IN THE
          RACE CHAIN, which does not exist on the decision lane. */}
      <p class="text-xs text-gray-400 mb-4" data-testid="group-form-fixed-constants">
        {FIXED_CONSTANTS_COPY[lane]}
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

        {/* Members — GroupMemberEditor (lane model picker + weight). */}
        <div
          data-testid="group-form-members-section"
          aria-invalid={errors.members || errors.lane ? 'true' : 'false'}
          aria-describedby={errors.members ? 'group-form-members-error' : errors.lane ? 'group-form-lane-error' : undefined}
        >
          <label class="block text-sm font-medium text-gray-300 mb-1">
            Members <span class="text-red-400">*</span>
          </label>
          <GroupMemberEditor
            members={fields.members}
            lane={lane}
            laneModels={laneModels}
            onChange={(next) => setFields((f) => ({ ...f, members: next }))}
          />
          {errors.lane && (
            <p
              id="group-form-lane-error"
              class="text-xs text-red-400 mt-1"
              data-testid="group-form-lane-error"
            >
              {errors.lane}
            </p>
          )}
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
            dropdown over this lane's models. The empty option IS the
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
            {laneModels.map((m) => (
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
