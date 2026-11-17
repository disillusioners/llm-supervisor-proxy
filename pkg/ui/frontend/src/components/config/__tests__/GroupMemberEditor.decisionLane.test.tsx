import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent } from '@testing-library/preact';
import { GroupMemberEditor } from '../GroupMemberEditor';
import type { Model, GroupMember } from '../../../types';

// The DECISION lane of the shared member editor.
//
// ONE component, TWO filtered feeds (the ruling's contract 26): the
// decision lane's picker offers decision-kind AND ENABLED models only.
//
// The eligibility clause is LANE-GATED (ADR-7 / AC-R6 vs ADR-9 /
// AC-R12): an inactive model is a valid CHAIN target but NOT a group
// candidate, so the decision lane never lists it — while the text lane
// keeps offering inactive chat models (pinned in the text-lane suite).

function makeModel(overrides: Partial<Model> = {}): Model {
  return {
    id: 'm1',
    name: 'M1',
    enabled: true,
    fallback_chain: [],
    ...overrides,
  };
}

const decisionLaneModels: Model[] = [
  makeModel({ id: 'dec-1', name: 'Decision One', kind: 'decision' }),
  makeModel({ id: 'dec-2', name: 'Decision Two', kind: 'decision' }),
  makeModel({ id: 'dec-off', name: 'Decision Off', kind: 'decision', enabled: false }),
  makeModel({ id: 'chat-1', name: 'Chat One' }),
  makeModel({ id: 'chat-kindless', name: 'Chat Kindless' }),
  makeModel({ id: 'img-1', name: 'Image One', kind: 'image-gen' }),
  makeModel({ id: 'grp-1', name: 'Group One', kind: 'group' }),
];

function makeMembers(overrides: GroupMember[] = [{ model_id: 'dec-1', weight: 1 }]): GroupMember[] {
  return overrides;
}

function optionValues(container: Element): string[] {
  const select = container.querySelector('[data-testid="group-member-model-select"]') as HTMLSelectElement;
  return Array.from(select.options).map((o) => o.value);
}

describe('GroupMemberEditor — the decision lane (decision-groups, phase 2)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('C1 — the picker offers only decision-kind models (chat, kindless, image-gen and group rows are absent)', () => {
    const onChange = vi.fn();
    const { container } = render(
      <GroupMemberEditor
        members={makeMembers()}
        lane="decision"
        laneModels={decisionLaneModels}
        onChange={onChange}
      />,
    );
    const values = optionValues(container);
    expect(values).toContain('dec-1');
    expect(values).toContain('dec-2');
    expect(values).not.toContain('chat-1');
    expect(values).not.toContain('chat-kindless');
    expect(values).not.toContain('img-1');
    expect(values).not.toContain('grp-1');
  });

  it('C2 — the picker placeholder is the decision-lane copy', () => {
    const onChange = vi.fn();
    const { container } = render(
      <GroupMemberEditor
        members={makeMembers()}
        lane="decision"
        laneModels={decisionLaneModels}
        onChange={onChange}
      />,
    );
    const select = container.querySelector('[data-testid="group-member-model-select"]') as HTMLSelectElement;
    const placeholder = Array.from(select.options).find((o) => o.value === '');
    expect(placeholder?.textContent).toBe('Select a decision model…');
  });

  it('C3 — weight editing, the backup toggle and remove behave exactly as on the text lane', () => {
    const onChange = vi.fn();
    const { container } = render(
      <GroupMemberEditor
        members={makeMembers([{ model_id: 'dec-1', weight: 2 }])}
        lane="decision"
        laneModels={decisionLaneModels}
        onChange={onChange}
      />,
    );
    // Weight — a float parses.
    const weight = container.querySelector('[data-testid="group-member-weight-input"]') as HTMLInputElement;
    fireEvent.input(weight, { target: { value: '2.5' } });
    expect(onChange).toHaveBeenLastCalledWith([{ model_id: 'dec-1', weight: 2.5 }]);

    // backup_only ON — the key is stored.
    const tick = container.querySelector('[data-testid="group-member-backup-only"]') as HTMLInputElement;
    fireEvent.click(tick);
    expect(onChange).toHaveBeenLastCalledWith([
      { model_id: 'dec-1', weight: 2, backup_only: true },
    ]);

    // backup_only OFF — the key is DROPPED entirely (Go omitempty parity).
    const { container: off } = render(
      <GroupMemberEditor
        members={makeMembers([{ model_id: 'dec-1', weight: 2, backup_only: true }])}
        lane="decision"
        laneModels={decisionLaneModels}
        onChange={onChange}
      />,
    );
    const offTick = off.querySelector('[data-testid="group-member-backup-only"]') as HTMLInputElement;
    expect(offTick.checked).toBe(true);
    fireEvent.click(offTick);
    expect(onChange).toHaveBeenLastCalledWith([{ model_id: 'dec-1', weight: 2 }]);

    // Remove — the row drops.
    const { container: rm } = render(
      <GroupMemberEditor
        members={makeMembers([
          { model_id: 'dec-1', weight: 1 },
          { model_id: 'dec-2', weight: 1 },
        ])}
        lane="decision"
        laneModels={decisionLaneModels}
        onChange={onChange}
      />,
    );
    fireEvent.click(rm.querySelectorAll('[data-testid="group-member-remove"]')[0]);
    expect(onChange).toHaveBeenLastCalledWith([{ model_id: 'dec-2', weight: 1 }]);
  });

  it('C4 — the add button is disabled at the member cap', () => {
    const onChange = vi.fn();
    const sixteen = Array.from({ length: 16 }, (_, i) => ({ model_id: `dec-${i + 1}`, weight: 1 }));
    const { container } = render(
      <GroupMemberEditor
        members={sixteen}
        lane="decision"
        laneModels={decisionLaneModels}
        onChange={onChange}
      />,
    );
    const add = container.querySelector('[data-testid="group-member-add"]') as HTMLButtonElement;
    expect(add.disabled).toBe(true);
    expect(container.querySelectorAll('[data-testid="group-member-row"]').length).toBe(16);
  });

  it('C5 — a duplicate member id warns but never blocks', () => {
    const onChange = vi.fn();
    const { container } = render(
      <GroupMemberEditor
        members={makeMembers([
          { model_id: 'dec-1', weight: 1 },
          { model_id: 'dec-1', weight: 2 },
        ])}
        lane="decision"
        laneModels={decisionLaneModels}
        onChange={onChange}
      />,
    );
    // Both rows still render — nothing is blocked or removed.
    expect(container.querySelectorAll('[data-testid="group-member-row"]').length).toBe(2);
    // The warn hint surfaces (the authoritative block is the BE's G4 400).
    expect(container.querySelector('[data-testid="group-member-dup-warn"]')).not.toBeNull();
    expect(onChange).not.toHaveBeenCalled();
  });

  it('C6 — the picker offers only enabled decision-kind models', () => {
    const onChange = vi.fn();
    const { container } = render(
      <GroupMemberEditor
        members={makeMembers()}
        lane="decision"
        laneModels={decisionLaneModels}
        onChange={onChange}
      />,
    );
    const values = optionValues(container);
    expect(values).toContain('dec-1');
    // The inactive decision model is NOT a group candidate.
    expect(values).not.toContain('dec-off');
  });

  it('C7 — the editor is silent about the excluded inactive model (it is not a defect)', () => {
    const onChange = vi.fn();
    const { container } = render(
      <GroupMemberEditor
        members={makeMembers()}
        lane="decision"
        laneModels={decisionLaneModels}
        onChange={onChange}
      />,
    );
    const editor = container.querySelector('[data-testid="group-member-editor"]') as HTMLElement;
    const text = editor.textContent ?? '';
    // No mention of the excluded model, and no "broken / disabled"
    // defect copy — an inactive model is a valid chain target, not a
    // data defect to clean up.
    expect(text).not.toContain('dec-off');
    expect(text).not.toContain('Decision Off');
    expect(text).not.toContain('disabled');
    expect(text).not.toContain('inactive');
    expect(text).not.toContain('broken');
  });
});
