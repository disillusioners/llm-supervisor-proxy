import type { Model, GroupMember, GroupLane } from '../../types';

// MAX_GROUP_MEMBERS mirrors the Go models.MaxGroupMembers constant
// (G2 cap — the same 16 ceiling as MaxCredentialRefs / the
// MultiCredentialEditor MAX). Keep the two in lock-step.
export const MAX_GROUP_MEMBERS = 16;

interface GroupMemberEditorProps {
  members: GroupMember[];
  // Which lane this group belongs to. REQUIRED (not defaulted): a
  // defaulted lane would let a call site silently fall back to 'chat'
  // and render a chat-only picker for a decision group — a visible but
  // confusing failure. A required prop makes every call site state
  // its lane, and a missed call site is a compile error.
  lane: GroupLane;
  // The models OF THIS LANE (the parent picks the feed: chat-kind rows
  // for the chat lane, the server-prefiltered ?kind=decision list for
  // the decision lane). Feeds the member picker.
  laneModels: Model[];
  onChange: (next: GroupMember[]) => void;
}

// GroupMemberEditor — rows of {lane model picker, weight number
// input, remove button} (load-balance-groups / plan §6.2, Round-2
// Am30 simplified editor: members are full model rows with their own
// credentials, so there are NO credential fields here — groups carry
// none by design).
//
// ONE component, TWO filtered feeds (the ruling's contract 26): the
// `lane` prop picks which feed the picker offers — the chat lane's
// chat-kind rows, or the decision lane's decision-kind AND enabled
// rows. There is no separate decision-lane editor.
//
// Discipline mirrors MultiCredentialEditor:
//   - Add is disabled at MAX_GROUP_MEMBERS (16); the form's submit
//     validation is the authoritative MIN-1 gate (a staging empty
//     list surfaces "At least one member is required" on submit —
//     same posture as the decision form's credentials check).
//   - Weight parses as a positive float; invalid input stores 0 so
//     the submit-time weight>0 check (G3 mirror) catches it.
//   - Duplicate model IDs WARN but never block (Q10: a deliberate
//     duplicate is rejected server-side by G4 with a house 400 —
//     the warn-hint keeps the operator informed without inventing a
//     client-side hard block the BE does not have).
export function GroupMemberEditor({ members, lane, laneModels, onChange }: GroupMemberEditorProps) {
  // Defensive lane filter (belt-and-suspenders on top of the parent's
  // filter — same shape as TokenForm's chatModels): members are lane
  // models only, so foreign rows never render as options even if a
  // future caller hands the unfiltered list through.
  //
  // LANE-GATED (ADR-9 / AC-R12): the chat arm is the historical
  // predicate, CHARACTER-FOR-CHARACTER (no enabled filter — the text
  // lane still offers inactive chat models). The decision arm adds the
  // candidate-eligibility clause (ADR-7 / AC-R6): an inactive model is
  // a valid CHAIN target but NOT a group candidate, so it never
  // appears here.
  const pickerModels =
    lane === 'chat'
      ? laneModels.filter((m) => !m.kind || m.kind === 'chat')
      : laneModels.filter((m) => m.kind === 'decision' && m.enabled);

  const addMember = () => {
    if (members.length >= MAX_GROUP_MEMBERS) return;
    onChange([...members, { model_id: '', weight: 1 }]);
  };

  const setMemberModel = (index: number, modelId: string) => {
    onChange(members.map((m, i) => (i === index ? { ...m, model_id: modelId } : m)));
  };

  const setMemberWeight = (index: number, raw: string) => {
    const parsed = parseFloat(raw);
    onChange(members.map((m, i) => (i === index ? { ...m, weight: Number.isFinite(parsed) ? parsed : 0 } : m)));
  };

  // group-backup-members: per-member "Backup only" tick. Default is
  // false (D7 backward compat); the server-side D2 ≥1-default
  // validation is the authoritative gate, but exposing the tick in
  // the editor lets the operator mark a member as a recourse-tier
  // pool member without ever touching the JSON column by hand.
  const toggleMemberBackupOnly = (index: number, value: boolean) => {
    onChange(
      members.map((m, i) => {
        if (i !== index) return m;
        // drop the key entirely when false so the JSON form is
        // "absent ⇒ default" (matches Go omitempty semantics; the
        // round-trip in GroupModelForm preserves an existing
        // true, never invents one on load).
        if (value) return { ...m, backup_only: true };
        const { backup_only: _drop, ...rest } = m;
        return rest;
      }),
    );
  };

  const removeMember = (index: number) => {
    onChange(members.filter((_, i) => i !== index));
  };

  // Duplicate model-ID detection (Q10 warn-only). A model_id that
  // appears more than once triggers the aggregate hint below.
  const counts = new Map<string, number>();
  for (const m of members) {
    if (m.model_id !== '') counts.set(m.model_id, (counts.get(m.model_id) ?? 0) + 1);
  }

  return (
    <div data-testid="group-member-editor">
      <div class="space-y-2">
        {members.map((member, index) => {
          const selected = laneModels.find((m) => m.id === member.model_id);
          return (
            <div
              key={index}
              class="flex items-center gap-2 bg-gray-800/60 rounded-md p-2 border border-gray-600/50"
              data-testid="group-member-row"
            >
              <select
                aria-label={`Member ${index + 1} model`}
                value={member.model_id}
                onChange={(e) => setMemberModel(index, (e.target as HTMLSelectElement).value)}
                class="flex-1 px-2 py-1.5 bg-gray-800 border border-gray-600 rounded-md text-white text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                data-testid="group-member-model-select"
              >
                <option value="">
                  {lane === 'chat' ? 'Select a chat model…' : 'Select a decision model…'}
                </option>
                {pickerModels.map((m) => (
                  <option key={m.id} value={m.id}>
                    {m.name} ({m.id})
                  </option>
                ))}
              </select>
              {/* Provider badge for the selected member (Q10 — sourced
                  from the model row itself, no BE round-trip). */}
              {selected?.internal_provider && (
                <span
                  class="hidden sm:inline-flex items-center text-xs bg-purple-900/50 text-purple-300 border border-purple-700/50 px-1.5 py-0.5 rounded whitespace-nowrap"
                  title={`Internal upstream: ${selected.internal_provider}`}
                >
                  {selected.internal_provider}
                </span>
              )}
              <input
                type="number"
                min="0"
                step="any"
                aria-label={`Member ${index + 1} weight`}
                value={Number.isFinite(member.weight) ? member.weight : ''}
                onInput={(e) => setMemberWeight(index, (e.target as HTMLInputElement).value)}
                class="w-20 px-2 py-1.5 bg-gray-800 border border-gray-600 rounded-md text-white text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
                data-testid="group-member-weight-input"
              />
              {/* group-backup-members: per-member "Backup only" tick
                  (recourse-tier marker). Compact label-only chrome so
                  the row width does not balloon. Tooltip + aria-label
                  make the semantics legible to A11Y tools. */}
              <label
                class="flex items-center gap-1 text-xs text-gray-300 cursor-pointer select-none"
                title="Backup only — this member is a recourse-tier pool entry, picked only after every default member has failed"
              >
                <input
                  type="checkbox"
                  checked={member.backup_only === true}
                  onChange={(e) =>
                    toggleMemberBackupOnly(index, (e.target as HTMLInputElement).checked)
                  }
                  class="w-4 h-4 bg-gray-800 border border-gray-600 rounded focus:ring-2 focus:ring-blue-500 focus:ring-offset-0 cursor-pointer"
                  aria-label={`Member ${index + 1} backup only`}
                  data-testid="group-member-backup-only"
                />
                <span class="whitespace-nowrap">Backup</span>
              </label>
              <button
                type="button"
                onClick={() => removeMember(index)}
                class="text-gray-400 hover:text-red-400 transition-colors p-1.5 rounded-md hover:bg-gray-600/50 flex-shrink-0"
                title={`Remove member ${index + 1}`}
                aria-label={`Remove member ${index + 1}`}
                data-testid="group-member-remove"
              >
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                </svg>
              </button>
            </div>
          );
        })}
      </div>

      {/* Duplicate-model warn hint (Q10 — warn, never block). Rendered
          once under the rows whenever ANY model_id is duplicated. */}
      {Array.from(counts.values()).some((c) => c > 1) && (
        <p class="text-xs text-yellow-400 mt-1" data-testid="group-member-dup-warn">
          Duplicate member model — the server rejects duplicate model IDs (G4); pick a different model or remove the row.
        </p>
      )}

      <button
        type="button"
        onClick={addMember}
        disabled={members.length >= MAX_GROUP_MEMBERS}
        class="mt-2 px-3 py-1.5 bg-gray-600 hover:bg-gray-500 text-white text-sm font-medium rounded-md transition-colors disabled:opacity-60 disabled:cursor-not-allowed flex items-center gap-1"
        data-testid="group-member-add"
      >
        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 6v6m0 0v6m0-6h6m-6 0H6" />
        </svg>
        Add Member
      </button>
      {/* Member hint — LANE-AWARE. The chat arm is the historical text
          byte-for-byte (ADR-9 / AC-R12: this release must not change
          text-lane behaviour). The decision arm drops the promises that
          are false on this lane: there is NO fallback chain and NO
          racing engine here (ADR-11 OQ-2 / OQ-3 option (a)) — a
          member's resilience is per-credential attempts WITHIN the
          member. */}
      <p class="text-xs text-gray-400 mt-1">
        {lane === 'chat' ? (
          <>
            Add up to {MAX_GROUP_MEMBERS} chat models. Higher weight = more requests routed to that member. Members run their own full pipeline (credentials, fallback, race). Tick <em>Backup</em> on a member to keep it as a recourse-tier candidate: it is served only after every non-backup (default) member in the request's exclusion set has failed — same weighted-random + sticky rules apply among backups.
          </>
        ) : (
          <>
            Add up to {MAX_GROUP_MEMBERS} decision models. Higher weight = more requests routed to that member. Each member runs its own credentials; resilience is per-credential attempts within the member. There is no fallback chain on this lane (deliberately not built) and no racing — 'racing consumed' means the member loop reached its end. Tick <em>Backup</em> on a member to keep it as a recourse-tier candidate: it is served only after every non-backup (default) member in the request's exclusion set has failed — same weighted-random + sticky rules apply among backups.
          </>
        )}
      </p>
    </div>
  );
}
