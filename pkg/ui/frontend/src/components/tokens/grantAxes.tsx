// ═══════════════════════════════════════════════════════════════════════════
// Shared per-axis grant rules for the token surfaces.
//
// ONE rule, TWO surfaces. The create form (TokenForm) and the LIVE inline
// editor (TokenList) must encode a grant identically: if they drift, the same
// operator action produces two different wire payloads — and one of them is
// the trap.
//
// ── WIRE CONTRACT (landed — pkg/ui/frontend/src/hooks/useApi.ts @ b0d4875,
//    pkg/ui/server.go:2583-2631) ────────────────────────────────────────────
//
//   POST  /fe/api/tokens        BOTH axes always on the wire:
//                                 null => ABSENT       (no allow-list: the axis
//                                        contributes NO grants of its own)
//                                 []   => PRESENT-EMPTY (grants NOTHING)
//                                 ids => PRESENT       (exactly those ids)
//   PATCH /fe/api/tokens/{id}   undefined => key OMITTED (KEEP)
//                                 null     => explicit null (ALSO KEEP)
//                                 []       => CLEAR (grants nothing; NEVER null)
//                                 ids      => REPLACE
//
// An ABSENT axis and an explicit `null` are the SAME state (the BE assigns a
// non-nil *[]string, so Go's omitempty never drops the key): read the DTO
// defensively (`?? null`) and treat a missing key and an explicit null
// identically.
//
// ── TOKEN-LEVEL SEMANTICS (pkg/authz/scope.go — LANDED, authoritative) ─────
//
//   Reach(T) = allowed_models ∪ allowed_groups ∪ ⋃ Members(allowed_groups)
//
//   The wildcard is CONJUNCTIVE: a token is unrestricted iff BOTH axes are
//   ABSENT. Once the other axis is PRESENT, an ABSENT axis contributes
//   NOTHING — "unrestricted on the model axis" is NOT "every model" in that
//   state. The per-axis copy below states the escalation the operator is
//   granting, and the surfaces ALSO state the effective combination
//   (effectiveGrantNote) so the two axes are never read in isolation.
//
//   A RESTRICTED axis can NEVER be widened back to unrestricted on PATCH:
//   omitted == null == KEEP, so promotion is inexpressible. The repair path
//   is to re-create the token — the edit surfaces offer NO promotion
//   affordance and SAY so (AXIS_COPY.createOnlyNote).
// ═══════════════════════════════════════════════════════════════════════════
import { ModelMultiSelect } from './ModelMultiSelect';

export type GrantAxis = 'models' | 'groups';
export type AxisMode = 'unrestricted' | 'restricted';
export type TokenModelKind = 'chat' | 'image-gen' | 'decision' | 'group';

// NOTE: a `type` (not an `interface`) on purpose — a type alias of an object
// literal type carries an implicit index signature, so a TokenModelRow is
// assignable to ModelMultiSelect's `{ [key: string]: any }` row shape.
export type TokenModelRow = {
  name: string;
  id: string;
  kind?: TokenModelKind;
};

// ── Copy (ONE place: both surfaces must say the same thing) ────────────────
export const AXIS_COPY = {
  // The escalation copy. The operator must SEE what "Unrestricted" grants.
  unrestrictedLabel: {
    models: 'Unrestricted — this token can reach EVERY model',
    groups: 'Unrestricted — this token can reach EVERY group and its members',
  },
  restrictedLabel: 'Restricted — only the selected items are reachable',
  unrestrictedHelp: {
    models:
      'Unrestricted — no allow-list is stored on this axis. This token can reach EVERY model only while the group axis is unrestricted too; once the group axis is restricted, an unrestricted model axis grants no models of its own.',
    groups:
      'Unrestricted — no allow-list is stored on this axis. This token can reach EVERY group and its members only while the model axis is unrestricted too; once the model axis is restricted, an unrestricted group axis grants no groups of its own.',
  },
  restrictedHelp:
    'Restricted — only the selected items are reachable. An empty selection grants NOTHING on this axis; it is NOT full access.',
  createOnlyNote:
    'Unrestricted cannot be selected on an existing token — a restricted axis cannot be widened back to full access; re-create the token to grant full access.',
  invalid: {
    models:
      "Deny-all: this axis's allow-list could not be read — the proxy denies all models on it.",
    groups:
      "Deny-all: this axis's allow-list could not be read — the proxy denies all groups on it.",
  },
  emptyLabel: {
    unrestricted: {
      models: 'Unrestricted — every model is reachable',
      groups: 'Unrestricted — every group is reachable',
    },
    restricted: 'Nothing selected — grants nothing on this axis',
  },
  itemNoun: { models: 'model', groups: 'group' },
} as const;

// ── The rule ────────────────────────────────────────────────────────────────

// kindOf — the row's kind, or undefined when the row is gone (deleted) or is
// a legacy chat row with no kind at all.
export function kindOf(id: string, models: TokenModelRow[]): TokenModelKind | undefined {
  return models.find((m) => m.id === id)?.kind;
}

// axisOfId — the AUTHORITATIVE axis of an id.
//
//   1. A stored grant NEVER moves between axes. Re-axising a grant is a
//      silent privilege change: a group id in allowed_models is a legal but
//      NARROW grant (ADR D4 — the group ENTRY only, never its members),
//      while the same id in allowed_groups grants the group AND its current
//      members. Moving it would escalate the token without the operator
//      asking.
//   2. Otherwise the ROW KIND decides: kind === 'group' => the group axis,
//      anything else (including a legacy row with no kind) => the model axis.
//
// A stale id (its row was deleted) stays on the axis it is ALREADY stored
// on, so a re-save never silently re-axes a grant.
export function axisOfId(
  id: string,
  models: TokenModelRow[],
  token?: { allowed_models?: string[] | null; allowed_groups?: string[] | null },
): GrantAxis {
  const row = models.find((m) => m.id === id);
  if (row) {
    // The row exists: its kind is the authority (no kind = legacy chat row
    // = the model axis).
    return row.kind === 'group' ? 'groups' : 'models';
  }
  // The row is GONE (stale grant): keep the id on the axis it is stored on.
  if (token?.allowed_groups?.includes(id)) return 'groups';
  return 'models';
}

// splitByAxis — split a selection into the two grant axes.
export function splitByAxis(
  ids: string[],
  models: TokenModelRow[],
  token?: { allowed_models?: string[] | null; allowed_groups?: string[] | null },
): { modelIds: string[]; groupIds: string[] } {
  const modelIds: string[] = [];
  const groupIds: string[] = [];
  for (const id of ids) {
    if (axisOfId(id, models, token) === 'groups') groupIds.push(id);
    else modelIds.push(id);
  }
  return { modelIds, groupIds };
}

// modeOfStored — the mode DERIVED from a stored axis.
//
//   ABSENT (null / missing key) => UNRESTRICTED (no allow-list on the axis)
//   a stored array (even [])   => RESTRICTED (an explicit [] grants NOTHING)
export function modeOfStored(stored: string[] | null | undefined): AxisMode {
  return stored === null || stored === undefined ? 'unrestricted' : 'restricted';
}

// wireValue — the value that axis goes on the wire with.
//
//   UNRESTRICTED => null  (POST: no allow-list = that axis is fully open;
//                         PATCH: null == KEEP, so it never widens anything)
//   RESTRICTED   => ids  (possibly [] — [] grants NOTHING, NEVER null)
export function wireValue(mode: AxisMode, ids: string[]): string[] | null {
  return mode === 'unrestricted' ? null : [...ids];
}

// effectiveGrantNote — what the token ACTUALLY reaches, given the two axis
// modes. The axes are ADDITIVE (Reach = models ∪ groups ∪ members), and the
// wildcard needs BOTH axes absent — so a per-axis reading alone misleads.
export function effectiveGrantNote(
  modelMode: AxisMode,
  groupMode: AxisMode,
  modelCount: number,
  groupCount: number,
): string {
  if (modelMode === 'unrestricted' && groupMode === 'unrestricted') {
    return 'Effective grant: UNRESTRICTED — this token can reach EVERY model (no allow-list is stored on either axis).';
  }
  if (modelMode === 'unrestricted') {
    return groupCount === 0
      ? 'Effective grant: RESTRICTED — the group axis grants NOTHING and the model axis has no allow-list of its own, so this token reaches NOTHING at all. An unrestricted axis is NOT full access while the other axis is restricted.'
      : 'Effective grant: RESTRICTED — this token reaches only the selected groups and their current members. The model axis has no allow-list of its own and grants NO models directly (an unrestricted axis is not full access while the other axis is restricted).';
  }
  if (groupMode === 'unrestricted') {
    return modelCount === 0
      ? 'Effective grant: RESTRICTED — the model axis grants NOTHING and the group axis has no allow-list of its own, so this token reaches NOTHING at all. An unrestricted axis is NOT full access while the other axis is restricted.'
      : 'Effective grant: RESTRICTED — this token reaches only the selected models. The group axis has no allow-list of its own and grants NO groups directly (an unrestricted axis is not full access while the other axis is restricted).';
  }
  if (modelCount === 0 && groupCount === 0) {
    return 'Effective grant: RESTRICTED — both axes have an empty selection, so this token reaches NOTHING at all.';
  }
  return 'Effective grant: RESTRICTED — this token reaches only the selected models, the selected groups and their current members.';
}

// ── Shared UI pieces ────────────────────────────────────────────────────────

interface AxisModeSelectProps {
  axis: GrantAxis;
  mode: AxisMode;
  onChange: (mode: AxisMode) => void;
  // True when the axis is (or was) RESTRICTED on an existing token: the
  // unrestricted option is then DISABLED — promotion is inexpressible on
  // PATCH, and a dead-end option would only confuse the operator.
  disableUnrestricted: boolean;
  disabled?: boolean;
}

export function AxisModeSelect({ axis, mode, onChange, disableUnrestricted, disabled }: AxisModeSelectProps) {
  const axisName = axis === 'models' ? 'Models' : 'Groups';
  return (
    <select
      aria-label={`${axisName} access mode`}
      value={mode}
      disabled={disabled}
      onChange={(e) => onChange((e.target as HTMLSelectElement).value as AxisMode)}
      class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
    >
      <option value="unrestricted" disabled={disableUnrestricted}>
        {AXIS_COPY.unrestrictedLabel[axis]}
      </option>
      <option value="restricted">{AXIS_COPY.restrictedLabel}</option>
    </select>
  );
}

interface AxisGrantControlsProps {
  axis: GrantAxis;
  mode: AxisMode;
  onModeChange: (mode: AxisMode) => void;
  rows: TokenModelRow[];
  selected: string[];
  onSelectedChange: (ids: string[]) => void;
  disableUnrestricted: boolean;
  invalid: boolean;
  // Show the create-only note (an existing token can never be widened back).
  showCreateOnlyNote: boolean;
}

export function AxisGrantControls({
  axis,
  mode,
  onModeChange,
  rows,
  selected,
  onSelectedChange,
  disableUnrestricted,
  invalid,
  showCreateOnlyNote,
}: AxisGrantControlsProps) {
  const axisName = axis === 'models' ? 'Models' : 'Groups';
  const unrestricted = mode === 'unrestricted';
  return (
    <div>
      <label class="block text-sm font-medium text-gray-300 mb-1">
        {axisName} access <span class="text-gray-500">(grant axis)</span>
      </label>
      <AxisModeSelect
        axis={axis}
        mode={mode}
        onChange={onModeChange}
        disableUnrestricted={disableUnrestricted}
      />
      <div class="mt-2">
        <ModelMultiSelect
          models={rows}
          selected={selected}
          onChange={onSelectedChange}
          disabled={unrestricted}
          emptyLabel={
            unrestricted ? AXIS_COPY.emptyLabel.unrestricted[axis] : AXIS_COPY.emptyLabel.restricted
          }
          itemNoun={AXIS_COPY.itemNoun[axis]}
          ariaLabel={`Allowed ${axisName.toLowerCase()}`}
        />
      </div>
      <p class="text-xs text-gray-400 mt-1">
        {unrestricted ? AXIS_COPY.unrestrictedHelp[axis] : AXIS_COPY.restrictedHelp}
      </p>
      {invalid && (
        <div role="alert" class="mt-2 text-xs text-red-300 bg-red-900/30 border border-red-800/50 rounded px-2 py-1.5">
          {AXIS_COPY.invalid[axis]}
        </div>
      )}
      {showCreateOnlyNote && mode === 'restricted' && (
        <p class="text-xs text-yellow-300/90 mt-1">{AXIS_COPY.createOnlyNote}</p>
      )}
    </div>
  );
}
