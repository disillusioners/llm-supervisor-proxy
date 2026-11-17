import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent } from '@testing-library/preact';
import { useState } from 'preact/hooks';
import {
  CredentialGroupMemberPicker,
  MAX_GROUP_MEMBERS,
} from '../CredentialGroupMemberPicker';
import type { Credential } from '../../../types';

// credential-groups commission (2026-11-16) — the ordered member
// picker: provider-filtered rows, add / remove / reorder, the
// MAX_GROUP_MEMBERS cap and the no-duplicate rule.

const mockCredentials: Credential[] = [
  { id: 'cred-oai-1', provider: 'openai' },
  { id: 'cred-oai-2', provider: 'openai' },
  { id: 'cred-oai-3', provider: 'openai' },
  { id: 'cred-mm-1', provider: 'minimax' },
];

// The picker is a CONTROLLED component — the harness owns the state so
// the test drives the real add/remove/reorder loop.
function Harness({
  provider = 'openai',
  initial = [] as string[],
}: {
  provider?: string;
  initial?: string[];
}) {
  const [members, setMembers] = useState<string[]>(initial);
  return (
    <CredentialGroupMemberPicker
      provider={provider}
      credentials={mockCredentials}
      members={members}
      onChange={setMembers}
    />
  );
}

function selectAt(container: Element, index: number): HTMLSelectElement {
  return container.querySelector(
    `[data-testid="credential-group-member-select-${index}"]`,
  ) as HTMLSelectElement;
}

function optionValues(select: HTMLSelectElement): string[] {
  return Array.from(select.querySelectorAll('option')).map((o) => o.value);
}

describe('CredentialGroupMemberPicker (credential-groups commission)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('D1 — renders only the credentials of the group provider', () => {
    const { container } = render(<Harness provider="openai" />);
    fireEvent.click(container.querySelector('[data-testid="credential-group-member-add"]')!);

    const values = optionValues(selectAt(container, 0));
    // The minimax credential is NOT offered to an openai group.
    expect(values).toContain('cred-oai-1');
    expect(values).not.toContain('cred-mm-1');
    // The placeholder is always first.
    expect(values[0]).toBe('');
  });

  it('D1 — switching the provider swaps the offered credentials', () => {
    const { container } = render(<Harness provider="minimax" />);
    fireEvent.click(container.querySelector('[data-testid="credential-group-member-add"]')!);

    const values = optionValues(selectAt(container, 0));
    expect(values).toContain('cred-mm-1');
    expect(values).not.toContain('cred-oai-1');
  });

  it('D2 — add, select, reorder and remove drive the ordered member list', () => {
    const { container } = render(<Harness />);
    const addBtn = () => container.querySelector('[data-testid="credential-group-member-add"]')!;

    fireEvent.click(addBtn());
    fireEvent.click(addBtn());
    expect(container.querySelectorAll('[data-testid^="credential-group-member-row-"]')).toHaveLength(2);

    fireEvent.change(selectAt(container, 0), { target: { value: 'cred-oai-1' } });
    fireEvent.change(selectAt(container, 1), { target: { value: 'cred-oai-2' } });

    // The resolution order mirrors the row order.
    expect(container.querySelector('[data-testid="credential-group-member-summary"]')!.textContent).toContain(
      'cred-oai-1 (openai) → cred-oai-2 (openai)',
    );

    // Reorder: move row 1 up.
    fireEvent.click(container.querySelector('[data-testid="credential-group-member-up-1"]')!);
    expect(selectAt(container, 0).value).toBe('cred-oai-2');
    expect(selectAt(container, 1).value).toBe('cred-oai-1');

    // Remove the first row.
    fireEvent.click(container.querySelector('[data-testid="credential-group-member-remove-0"]')!);
    expect(container.querySelectorAll('[data-testid^="credential-group-member-row-"]')).toHaveLength(1);
    expect(selectAt(container, 0).value).toBe('cred-oai-1');
  });

  it('D3 — the member list is capped at MAX_GROUP_MEMBERS', () => {
    const { container } = render(<Harness />);
    const addBtn = () => container.querySelector('[data-testid="credential-group-member-add"]') as HTMLButtonElement;

    for (let i = 0; i < MAX_GROUP_MEMBERS; i++) {
      fireEvent.click(addBtn());
    }
    expect(container.querySelectorAll('[data-testid^="credential-group-member-row-"]')).toHaveLength(
      MAX_GROUP_MEMBERS,
    );

    // At the cap the add button is disabled and the cap is surfaced.
    expect(addBtn().disabled).toBe(true);
    expect(container.querySelector('[data-testid="credential-group-member-cap"]')!.textContent).toContain(
      `Maximum ${MAX_GROUP_MEMBERS} members reached`,
    );

    // A click on the disabled button adds nothing.
    fireEvent.click(addBtn());
    expect(container.querySelectorAll('[data-testid^="credential-group-member-row-"]')).toHaveLength(
      MAX_GROUP_MEMBERS,
    );
  });

  it('D4 — a credential already selected on another row is not selectable', () => {
    const { container } = render(<Harness />);
    const addBtn = () => container.querySelector('[data-testid="credential-group-member-add"]')!;
    fireEvent.click(addBtn());
    fireEvent.click(addBtn());

    fireEvent.change(selectAt(container, 0), { target: { value: 'cred-oai-1' } });

    const row1 = selectAt(container, 1);
    const alreadyUsed = Array.from(row1.querySelectorAll('option')).find(
      (o) => o.value === 'cred-oai-1',
    ) as HTMLOptionElement;
    expect(alreadyUsed).toBeTruthy();
    expect(alreadyUsed.disabled).toBe(true);

    // The row's own value stays selectable (it is not a duplicate of itself).
    const row0 = selectAt(container, 0);
    const own = Array.from(row0.querySelectorAll('option')).find(
      (o) => o.value === 'cred-oai-1',
    ) as HTMLOptionElement;
    expect(own.disabled).toBe(false);
  });

  it('D2 — the member rows render the enriched member label', () => {
    const { container } = render(<Harness initial={['cred-oai-2']} />);
    expect(container.querySelector('[data-testid="credential-group-member-row-0"]')!.textContent).toContain(
      'cred-oai-2 (openai)',
    );
  });
});
