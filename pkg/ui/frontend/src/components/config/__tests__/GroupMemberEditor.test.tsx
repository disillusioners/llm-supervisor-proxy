import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent } from '@testing-library/preact';
import { GroupMemberEditor } from '../GroupMemberEditor';
import type { Model, GroupMember } from '../../../types';

function makeModel(overrides: Partial<Model> = {}): Model {
  return {
    id: 'm1',
    name: 'M1',
    enabled: true,
    fallback_chain: [],
    ...overrides,
  };
}

// Chat-kind fixture set — one row per kind so every test asserts the
// picker filter in one render (the same multi-kind chrome as
// ModelsTab.chatFilter / TokenForm tests).
const chatModels: Model[] = [
  makeModel({ id: 'chat-1', name: 'Chat One' }),
  makeModel({ id: 'chat-2', name: 'Chat Two (no kind)' }),
  makeModel({ id: 'img-1', name: 'Image One', kind: 'image-gen' }),
  makeModel({ id: 'dec-1', name: 'Decision One', kind: 'decision' }),
  makeModel({ id: 'grp-1', name: 'Group One', kind: 'group' }),
];

function makeMembers(overrides: GroupMember[] = [{ model_id: 'chat-1', weight: 1 }]): GroupMember[] {
  return overrides;
}

describe('GroupMemberEditor — chat-kind picker + weight + MIN/MAX + dup-warn (load-balance-groups P3.4 / Am30)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('renders one row per member with picker + weight input + remove', () => {
    const onChange = vi.fn();
    const { container } = render(
      <GroupMemberEditor members={makeMembers([{ model_id: 'chat-1', weight: 2 }, { model_id: 'chat-2', weight: 3 }])} chatModels={chatModels} onChange={onChange} />,
    );
    const rows = container.querySelectorAll('[data-testid="group-member-row"]');
    expect(rows.length).toBe(2);
    expect(container.querySelectorAll('[data-testid="group-member-model-select"]').length).toBe(2);
    const weights = container.querySelectorAll('[data-testid="group-member-weight-input"]') as NodeListOf<HTMLInputElement>;
    expect(weights.length).toBe(2);
    expect((weights[0] as HTMLInputElement).value).toBe('2');
    expect((weights[1] as HTMLInputElement).value).toBe('3');
    expect(container.querySelectorAll('[data-testid="group-member-remove"]').length).toBe(2);
  });

  it('Am30 — the model picker offers ONLY chat-kind models (image-gen, decision, and group rows are absent)', () => {
    const onChange = vi.fn();
    const { container } = render(
      <GroupMemberEditor members={makeMembers()} chatModels={chatModels} onChange={onChange} />,
    );
    const select = container.querySelector('[data-testid="group-member-model-select"]') as HTMLSelectElement;
    const optionValues = Array.from(select.options).map((o) => o.value);
    // Chat rows present (kindless rows ARE chat — the legacy baseline).
    expect(optionValues).toContain('chat-1');
    expect(optionValues).toContain('chat-2');
    // Non-chat kinds filtered out of the picker source (the parent
    // filters the useModels() list; the editor receives only chat
    // rows — asserted here so the contract is test-pinned).
    expect(optionValues).not.toContain('img-1');
    expect(optionValues).not.toContain('dec-1');
    expect(optionValues).not.toContain('grp-1');
  });

  it('picking a model updates the row via onChange', () => {
    const onChange = vi.fn();
    const { container } = render(
      <GroupMemberEditor members={makeMembers([{ model_id: '', weight: 1 }])} chatModels={chatModels} onChange={onChange} />,
    );
    const select = container.querySelector('[data-testid="group-member-model-select"]') as HTMLSelectElement;
    fireEvent.change(select, { target: { value: 'chat-2' } });
    expect(onChange).toHaveBeenCalledWith([{ model_id: 'chat-2', weight: 1 }]);
  });

  it('editing a weight updates the row via onChange (float weights parse)', () => {
    const onChange = vi.fn();
    const { container } = render(
      <GroupMemberEditor members={makeMembers()} chatModels={chatModels} onChange={onChange} />,
    );
    const input = container.querySelector('[data-testid="group-member-weight-input"]') as HTMLInputElement;
    fireEvent.input(input, { target: { value: '2.5' } });
    expect(onChange).toHaveBeenCalledWith([{ model_id: 'chat-1', weight: 2.5 }]);
  });

  it('remove drops the row via onChange', () => {
    const onChange = vi.fn();
    const { container } = render(
      <GroupMemberEditor members={makeMembers([{ model_id: 'chat-1', weight: 1 }, { model_id: 'chat-2', weight: 1 }])} chatModels={chatModels} onChange={onChange} />,
    );
    const removes = container.querySelectorAll('[data-testid="group-member-remove"]');
    fireEvent.click(removes[0]);
    expect(onChange).toHaveBeenCalledWith([{ model_id: 'chat-2', weight: 1 }]);
  });

  it('add appends a fresh {model_id: "", weight: 1} row', () => {
    const onChange = vi.fn();
    const { container } = render(
      <GroupMemberEditor members={makeMembers()} chatModels={chatModels} onChange={onChange} />,
    );
    fireEvent.click(container.querySelector('[data-testid="group-member-add"]') as HTMLButtonElement);
    expect(onChange).toHaveBeenCalledWith([
      { model_id: 'chat-1', weight: 1 },
      { model_id: '', weight: 1 },
    ]);
  });

  it('MAX 16 — the add button disables at 16 members (G2 cap)', () => {
    const onChange = vi.fn();
    const sixteen = Array.from({ length: 16 }, (_, i) => ({ model_id: `chat-${i + 1}`, weight: 1 }));
    const { container } = render(
      <GroupMemberEditor members={sixteen} chatModels={chatModels} onChange={onChange} />,
    );
    const add = container.querySelector('[data-testid="group-member-add"]') as HTMLButtonElement;
    expect(add.disabled).toBe(true);
    expect(container.querySelectorAll('[data-testid="group-member-row"]').length).toBe(16);
  });

  it('Q10 — duplicate model IDs WARN but do not block (no onChange interference, hint rendered)', () => {
    const onChange = vi.fn();
    const { container } = render(
      <GroupMemberEditor members={makeMembers([{ model_id: 'chat-1', weight: 1 }, { model_id: 'chat-1', weight: 2 }])} chatModels={chatModels} onChange={onChange} />,
    );
    // Both duplicate rows still render (nothing is blocked/removed).
    expect(container.querySelectorAll('[data-testid="group-member-row"]').length).toBe(2);
    // The warn hint surfaces (warn-only per Q10; the authoritative
    // block is the BE G4 400).
    expect(container.querySelector('[data-testid="group-member-dup-warn"]')).not.toBeNull();
    // No dup ⇒ no hint.
    const { container: clean } = render(
      <GroupMemberEditor members={makeMembers([{ model_id: 'chat-1', weight: 1 }, { model_id: 'chat-2', weight: 1 }])} chatModels={chatModels} onChange={onChange} />,
    );
    expect(clean.querySelector('[data-testid="group-member-dup-warn"]')).toBeNull();
  });

  it('provider badge renders for a selected internal model (Q10 — no BE round-trip)', () => {
    const onChange = vi.fn();
    const internal = [makeModel({ id: 'int-1', name: 'Internal One', internal: true, internal_provider: 'deepseek' })];
    const { container } = render(
      <GroupMemberEditor members={makeMembers([{ model_id: 'int-1', weight: 1 }])} chatModels={internal} onChange={onChange} />,
    );
    expect(container.textContent).toContain('deepseek');
  });
});
