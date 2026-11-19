import { useState } from 'preact/hooks';
import type { ApiToken } from '../../types';
import {
  AxisGrantControls,
  AxisMode,
  TokenModelRow,
  effectiveGrantNote,
  modeOfStored,
  wireValue,
} from './grantAxes';

interface TokenFormProps {
  // kind flows through from SettingsPage's models.map, so the axis split can
  // be made by ROW KIND at submit (a group id must never land in
  // allowed_models, and a model id must never land in allowed_groups).
  models: TokenModelRow[];
  token?: ApiToken;  // For editing existing tokens
  // The two axes are INDEPENDENT and TRI-STATE:
  //   null => Unrestricted (no allow-list on that axis — the axis is open)
  //   []   => Restricted with an empty selection (grants NOTHING)
  //   ids  => Restricted with exactly those ids
  onSubmit: (
    name: string,
    expiresAt: string | null,
    allowedModels: string[] | null,
    allowedGroups: string[] | null,
  ) => Promise<void>;
  onCancel: () => void;
  onStatus: (status: { type: 'success' | 'error'; message: string } | null) => void;
}

export function TokenForm({ models, token, onSubmit, onCancel, onStatus }: TokenFormProps) {
  const [name, setName] = useState(token?.name || '');
  const [expiresAt, setExpiresAt] = useState(() => {
    if (token?.expires_at) {
      return token.expires_at.split('T')[0]; // Extract date part only
    }
    return '';
  });

  // ── Per-axis grant state ──────────────────────────────────────────────────
  // CREATE: the operator picks a mode per axis. The default is Unrestricted
  // (null = no allow-list on that axis), which is also today's outcome for a
  // token whose pickers are left alone — and the escalation is spelled out
  // in the mode control itself.
  // EDIT: the mode is DERIVED from the stored value and may only ever
  // NARROW (a restricted axis can never be widened back — the PATCH
  // contract maps an omitted key AND an explicit null to KEEP).
  const [modelMode, setModelMode] = useState<AxisMode>(() => modeOfStored(token?.allowed_models));
  const [selectedModels, setSelectedModels] = useState<string[]>(() => token?.allowed_models ?? []);
  const [groupMode, setGroupMode] = useState<AxisMode>(() => modeOfStored(token?.allowed_groups));
  const [selectedGroups, setSelectedGroups] = useState<string[]>(() => token?.allowed_groups ?? []);
  const [saving, setSaving] = useState(false);

  const isEditing = !!token;

  // A DENY-ALL axis (an allow-list the proxy could not read) is NEVER
  // rendered as unrestricted/wildcard — the fail-closed state is shown and the
  // axis can only be repaired by narrowing it.
  const modelsInvalid = token?.allowed_models_invalid === true;
  const groupsInvalid = token?.allowed_groups_invalid === true;

  const effectiveModelMode: AxisMode = modelsInvalid ? 'restricted' : modelMode;
  const effectiveGroupMode: AxisMode = groupsInvalid ? 'restricted' : groupMode;

  // Unrestricted cannot be selected on an existing RESTRICTED axis: the BE
  // maps both an omitted key and an explicit null to KEEP, so promotion is
  // inexpressible on PATCH. The option is disabled rather than offered as a
  // dead end.
  const modelPromotionBlocked =
    isEditing && (modelsInvalid || modeOfStored(token?.allowed_models) === 'restricted');
  const groupPromotionBlocked =
    isEditing && (groupsInvalid || modeOfStored(token?.allowed_groups) === 'restricted');

  // All kinds are selectable. The picker emits ids; the SPLIT into
  // allowed_models vs allowed_groups happens at submit, by row kind
  // (plan §12.2). A kind filter here would be a trap: it would exclude
  // nothing after the widening and mislead the next reader. The pickers are
  // split by SURFACE only — group rows live in the group picker, every other
  // row (chat, image-gen, decision, legacy rows with no kind) in the model
  // picker — and the submit-time split is applied to BOTH selections, so a
  // group id can never land in allowed_models whichever control it was
  // picked in.
  const modelRows = models.filter((m) => m.kind !== 'group');
  const groupRows = models.filter((m) => m.kind === 'group');

  const handleSubmit = async () => {
    if (!name.trim()) {
      onStatus({ type: 'error', message: 'Token name is required' });
      return;
    }

    try {
      setSaving(true);
      onStatus(null);
      const expires = expiresAt ? new Date(expiresAt).toISOString() : null;
      // F1 — each axis's OWN picker selection is authoritative. The
      // pickers are split by surface (group rows live only in the group
      // picker), so re-partitioning the union would move a stored
      // target-only group id between axes and drop an id legally stored on
      // BOTH axes. De-duplicate per axis instead.
      const modelIds = [...new Set(selectedModels)];
      const groupIds = [...new Set(selectedGroups)];
      // Unrestricted => null (no allow-list on that axis). Restricted => the
      // ids, or [] when nothing is selected — [] grants NOTHING and is NEVER
      // null.
      const allowedModels = wireValue(effectiveModelMode, modelIds);
      const allowedGroups = wireValue(effectiveGroupMode, groupIds);
      await onSubmit(name.trim(), expires, allowedModels, allowedGroups);
    } catch (e) {
      onStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to save token' });
    } finally {
      setSaving(false);
    }
  };

  // Calculate min date (today) for the date input
  const today = new Date().toISOString().split('T')[0];

  return (
    <div class="bg-gray-700/50 rounded-lg p-5 border border-gray-600">
      <h3 class="text-lg font-medium text-white mb-4">
        {isEditing ? 'Edit API Token' : 'Create New API Token'}
      </h3>
      <div class="space-y-4">
        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">
            Token Name <span class="text-red-400">*</span>
          </label>
          <input
            type="text"
            value={name}
            onInput={(e) => setName((e.target as HTMLInputElement).value)}
            class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
            placeholder="e.g., Development Token"
          />
          <p class="text-xs text-gray-400 mt-1">A descriptive name to identify this token</p>
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">
            Expires At <span class="text-gray-500">(optional)</span>
          </label>
          <input
            type="date"
            value={expiresAt}
            min={today}
            onInput={(e) => setExpiresAt((e.target as HTMLInputElement).value)}
            class="w-full px-3 py-2 bg-gray-800 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
          />
          <p class="text-xs text-gray-400 mt-1">Leave empty for no expiration</p>
        </div>

        {/* ── The two grant axes ───────────────────────────────────────────
            The model axis and the group axis are INDEPENDENT. Each one is
            either Unrestricted (no allow-list on that axis) or Restricted
            (an allow-list — which may be EMPTY, and an empty allow-list
            grants NOTHING). */}
        <AxisGrantControls
          axis="models"
          mode={effectiveModelMode}
          onModeChange={setModelMode}
          rows={modelRows}
          selected={selectedModels}
          onSelectedChange={setSelectedModels}
          disableUnrestricted={modelPromotionBlocked}
          invalid={modelsInvalid}
          showCreateOnlyNote={isEditing}
        />

        <AxisGrantControls
          axis="groups"
          mode={effectiveGroupMode}
          onModeChange={setGroupMode}
          rows={groupRows}
          selected={selectedGroups}
          onSelectedChange={setSelectedGroups}
          disableUnrestricted={groupPromotionBlocked}
          invalid={groupsInvalid}
          showCreateOnlyNote={isEditing}
        />

        <p class="text-xs text-gray-300 bg-gray-800/60 border border-gray-700 rounded px-2 py-1.5">
          {effectiveGrantNote(
            effectiveModelMode,
            effectiveGroupMode,
            selectedModels.length,
            selectedGroups.length,
          )}
        </p>

        <div class="flex justify-end gap-3 pt-2">
          <button
            onClick={onCancel}
            class="px-4 py-2 bg-gray-600 hover:bg-gray-500 text-white rounded-md transition-colors text-sm font-medium"
          >
            Cancel
          </button>
          <button
            onClick={handleSubmit}
            class="px-4 py-2 bg-blue-600 hover:bg-blue-500 text-white rounded-md transition-colors text-sm font-medium"
            disabled={!name.trim() || saving}
          >
            {saving ? (isEditing ? 'Saving...' : 'Creating...') : (isEditing ? 'Save Changes' : 'Create Token')}
          </button>
        </div>
      </div>
    </div>
  );
}
