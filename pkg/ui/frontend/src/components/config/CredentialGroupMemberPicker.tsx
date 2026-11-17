import type { Credential, CredentialGroupMember } from '../../types';
import { escapeHtml } from '../../utils/helpers';

// credential-groups commission (2026-11-16) — the ordered member picker.
//
// A group's members are ORDERED credential ids of ONE provider. The
// picker mirrors MultiCredentialEditor's row idiom (a <select> per row
// + reorder + remove) minus the weight input — a member carries no
// weight; the weight lives on the model's GROUP ref and is inherited by
// the members when the group is flattened at model resolution.
//
// The member cap mirrors the backend authority
// (MaxCredentialGroupMembers = 16). The 64 KB envelope cap is
// transport-only and is NOT the member-count authority.

export const MAX_GROUP_MEMBERS = 16;

interface CredentialGroupMemberPickerProps {
  /** The group's provider — only matching credentials are selectable. */
  provider: string;
  /** Every known credential (the tab self-fetches the credential list). */
  credentials: Credential[];
  /** The ordered member ids. An empty string is an unfilled row. */
  members: string[];
  onChange: (members: string[]) => void;
}

export function CredentialGroupMemberPicker({
  provider,
  credentials,
  members,
  onChange,
}: CredentialGroupMemberPickerProps) {
  // FE view model — the selectable options enriched with credential
  // metadata (the picker never re-derives labels inline).
  const options: CredentialGroupMember[] = credentials
    .filter((c) => c.provider === provider)
    .map((c) => ({
      credential_id: c.id,
      provider: c.provider,
      label: `${c.id} (${c.provider})`,
    }));

  // FE view model — the currently selected members, enriched the same
  // way (used for the resolution-order summary).
  const details: CredentialGroupMember[] = members
    .filter((id) => id !== '')
    .map((id) => {
      const cred = credentials.find((c) => c.id === id);
      return {
        credential_id: id,
        provider: cred?.provider ?? '',
        label: cred ? `${cred.id} (${cred.provider})` : `${id} (unknown)`,
      };
    });

  const atCap = members.length >= MAX_GROUP_MEMBERS;

  const handleAdd = () => {
    if (atCap) return;
    onChange([...members, '']);
  };

  const handleSelect = (index: number, credentialId: string) => {
    onChange(members.map((m, i) => (i === index ? credentialId : m)));
  };

  const handleRemove = (index: number) => {
    onChange(members.filter((_, i) => i !== index));
  };

  const handleMove = (index: number, delta: number) => {
    const target = index + delta;
    if (target < 0 || target >= members.length) return;
    const next = [...members];
    const moved = next.splice(index, 1)[0];
    next.splice(target, 0, moved);
    onChange(next);
  };

  // A credential already selected on ANOTHER row is not selectable here
  // (duplicate prevention).
  const isOptionDisabled = (rowIndex: number, credentialId: string): boolean =>
    members.some((m, i) => i !== rowIndex && m === credentialId);

  return (
    <div data-testid="credential-group-member-picker">
      <label class="block text-sm font-medium text-gray-300 mb-1">Members</label>
      <p class="text-xs text-gray-400 mb-2">
        Ordered {provider} credentials. Members are flattened into the referencing models at
        resolution. Up to {MAX_GROUP_MEMBERS} members.
      </p>

      {members.length === 0 ? (
        <p class="text-gray-500 text-xs mb-2" data-testid="credential-group-member-empty">
          No members yet
        </p>
      ) : (
        <ul class="space-y-1 mb-2">
          {members.map((memberId, index) => (
            <li
              key={index}
              class="flex items-center gap-2 bg-gray-900/40 border border-gray-700 rounded-md p-2"
              data-testid={`credential-group-member-row-${index}`}
            >
              <select
                value={memberId}
                onChange={(e) => handleSelect(index, (e.target as HTMLSelectElement).value)}
                data-testid={`credential-group-member-select-${index}`}
                class="flex-1 px-2 py-1.5 bg-gray-700 border border-gray-600 rounded-md text-white text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              >
                <option value="">Select a credential</option>
                {options.map((option) => (
                  <option
                    key={option.credential_id}
                    value={option.credential_id}
                    disabled={isOptionDisabled(index, option.credential_id)}
                  >
                    {option.label}
                  </option>
                ))}
              </select>
              <button
                type="button"
                onClick={() => handleMove(index, -1)}
                disabled={index === 0}
                aria-label={`Move member ${index + 1} up`}
                data-testid={`credential-group-member-up-${index}`}
                class="text-gray-400 hover:text-blue-400 transition-colors p-1 rounded disabled:opacity-30"
              >
                ↑
              </button>
              <button
                type="button"
                onClick={() => handleMove(index, 1)}
                disabled={index === members.length - 1}
                aria-label={`Move member ${index + 1} down`}
                data-testid={`credential-group-member-down-${index}`}
                class="text-gray-400 hover:text-blue-400 transition-colors p-1 rounded disabled:opacity-30"
              >
                ↓
              </button>
              <button
                type="button"
                onClick={() => handleRemove(index)}
                aria-label={`Remove member ${index + 1}`}
                data-testid={`credential-group-member-remove-${index}`}
                class="text-gray-400 hover:text-red-400 transition-colors p-1 rounded"
              >
                ✕
              </button>
            </li>
          ))}
        </ul>
      )}

      {details.length > 0 && (
        <p class="text-xs text-gray-400 mb-2" data-testid="credential-group-member-summary">
          Resolution order: {details.map((d) => escapeHtml(d.label)).join(' → ')}
        </p>
      )}

      <div class="flex items-center gap-2">
        <button
          type="button"
          onClick={handleAdd}
          disabled={atCap}
          data-testid="credential-group-member-add"
          class="text-xs text-blue-400 hover:text-blue-300 transition-colors disabled:opacity-40"
        >
          + Add member
        </button>
        {atCap && (
          <span class="text-xs text-gray-500" data-testid="credential-group-member-cap">
            Maximum {MAX_GROUP_MEMBERS} members reached
          </span>
        )}
      </div>
    </div>
  );
}
