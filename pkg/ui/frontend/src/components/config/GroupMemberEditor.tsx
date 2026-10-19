import type { Model, GroupMember } from '../../types';

// MAX_GROUP_MEMBERS mirrors the Go models.MaxGroupMembers constant
// (G2 cap — the same 16 ceiling as MaxCredentialRefs / the
// MultiCredentialEditor MAX). Keep the two in lock-step.
export const MAX_GROUP_MEMBERS = 16;

interface GroupMemberEditorProps {
  members: GroupMember[];
  // Chat-kind models only — the parent (GroupModelForm) filters the
  // useModels() list BEFORE handing it down (plan §6.2 / Am30: members
  // are PROXY MODEL IDs of existing chat-kind models; image-gen,
  // decision, and group rows are never valid members).
  chatModels: Model[];
  onChange: (next: GroupMember[]) => void;
}

// GroupMemberEditor — rows of {chat-kind model picker, weight number
// input, remove button} (load-balance-groups / plan §6.2, Round-2
// Am30 simplified editor: members are full model rows with their own
// credentials, so there are NO credential fields here — groups carry
// none by design).
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
export function GroupMemberEditor({ members, chatModels, onChange }: GroupMemberEditorProps) {
  // Defensive chat-kind filter (belt-and-suspenders on top of the
  // parent's filter — same shape as TokenForm's chatModels): members
  // are chat models only, so non-chat rows never render as options
  // even if a future caller hands the unfiltered list through.
  const pickerModels = chatModels.filter((m) => !m.kind || m.kind === 'chat');

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
          const selected = chatModels.find((m) => m.id === member.model_id);
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
                <option value="">Select a chat model…</option>
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
      <p class="text-xs text-gray-400 mt-1">
        Add up to {MAX_GROUP_MEMBERS} chat models. Higher weight = more requests routed to that member. Members run their own full pipeline (credentials, fallback, race).
      </p>
    </div>
  );
}
