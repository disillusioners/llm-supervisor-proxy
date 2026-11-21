import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent } from '@testing-library/preact';
import { GroupMemberEditor } from '../GroupMemberEditor';
import type { Model, GroupMember } from '../../../types';

// The IMAGE-GEN lane of the shared member editor (imggen-groups).
//
// ONE component, THREE filtered feeds: the image-gen lane's picker
// offers image-gen-kind AND ENABLED models only — the same
// candidate-eligibility clause the decision lane uses (IA-4).
//
// The clause is LANE-GATED: an inactive model is NOT a group candidate
// on this lane, while the text lane keeps offering inactive chat models
// (the chat arm is byte-frozen — ADR-9 / AC-R12).

function makeModel(overrides: Partial<Model> = {}): Model {
  return {
    id: 'm1',
    name: 'M1',
    enabled: true,
    fallback_chain: [],
    ...overrides,
  };
}

const imageGenLaneModels: Model[] = [
  makeModel({ id: 'img-1', name: 'Image One', kind: 'image-gen' }),
  makeModel({ id: 'img-2', name: 'Image Two', kind: 'image-gen' }),
  makeModel({ id: 'img-off', name: 'Image Off', kind: 'image-gen', enabled: false }),
  makeModel({ id: 'chat-1', name: 'Chat One' }),
  makeModel({ id: 'chat-kindless', name: 'Chat Kindless' }),
  makeModel({ id: 'dec-1', name: 'Decision One', kind: 'decision' }),
  makeModel({ id: 'grp-1', name: 'Group One', kind: 'group' }),
];

function makeMembers(overrides: GroupMember[] = [{ model_id: 'img-1', weight: 1 }]): GroupMember[] {
  return overrides;
}

function optionValues(container: Element): string[] {
  const select = container.querySelector('[data-testid="group-member-model-select"]') as HTMLSelectElement;
  return Array.from(select.options).map((o) => o.value);
}

describe('GroupMemberEditor — the image-gen lane (imggen-groups, phase 3)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('IE1 — the picker offers only image-gen-kind models (chat, kindless, decision and group rows are absent)', () => {
    const onChange = vi.fn();
    const { container } = render(
      <GroupMemberEditor
        members={makeMembers()}
        lane="image-gen"
        laneModels={imageGenLaneModels}
        onChange={onChange}
      />,
    );
    const values = optionValues(container);
    expect(values).toContain('img-1');
    expect(values).toContain('img-2');
    expect(values).not.toContain('chat-1');
    expect(values).not.toContain('chat-kindless');
    expect(values).not.toContain('dec-1');
    expect(values).not.toContain('grp-1');
  });

  it('IE2 — the picker placeholder is the image-gen-lane copy', () => {
    const onChange = vi.fn();
    const { container } = render(
      <GroupMemberEditor
        members={makeMembers()}
        lane="image-gen"
        laneModels={imageGenLaneModels}
        onChange={onChange}
      />,
    );
    const select = container.querySelector('[data-testid="group-member-model-select"]') as HTMLSelectElement;
    const placeholder = Array.from(select.options).find((o) => o.value === '');
    expect(placeholder?.textContent).toBe('Select an image-gen model…');
  });

  it('IE3 — the picker offers only enabled image-gen-kind models', () => {
    const onChange = vi.fn();
    const { container } = render(
      <GroupMemberEditor
        members={makeMembers()}
        lane="image-gen"
        laneModels={imageGenLaneModels}
        onChange={onChange}
      />,
    );
    const values = optionValues(container);
    expect(values).toContain('img-1');
    // The inactive image-gen model is NOT a group candidate.
    expect(values).not.toContain('img-off');
  });

  it('IE4 — weight editing, the backup toggle and remove behave exactly as on the sibling lanes', () => {
    const onChange = vi.fn();
    const { container } = render(
      <GroupMemberEditor
        members={makeMembers([{ model_id: 'img-1', weight: 2 }])}
        lane="image-gen"
        laneModels={imageGenLaneModels}
        onChange={onChange}
      />,
    );
    // Weight — a float parses.
    const weight = container.querySelector('[data-testid="group-member-weight-input"]') as HTMLInputElement;
    fireEvent.input(weight, { target: { value: '2.5' } });
    expect(onChange).toHaveBeenCalledWith([{ model_id: 'img-1', weight: 2.5 }]);

    // The backup-only tick rides the member row.
    fireEvent.click(container.querySelector('[data-testid="group-member-backup-only"]')!);
    expect(onChange).toHaveBeenLastCalledWith([
      { model_id: 'img-1', weight: 2, backup_only: true },
    ]);

    // Remove drops the row.
    fireEvent.click(container.querySelector('[data-testid="group-member-remove"]')!);
    expect(onChange).toHaveBeenLastCalledWith([]);
  });

  it('IE5 — the member hint promises only what the image-gen lane builds (no chain, no racing, no pinning)', () => {
    const onChange = vi.fn();
    const { container } = render(
      <GroupMemberEditor
        members={makeMembers()}
        lane="image-gen"
        laneModels={imageGenLaneModels}
        onChange={onChange}
      />,
    );
    const editor = container.querySelector('[data-testid="group-member-editor"]') as HTMLElement;
    const text = editor.textContent ?? '';
    // The lane's own feed name.
    expect(text).toContain('image-gen models');
    // Per-credential resilience within a member.
    expect(text).toContain('per-credential attempts within the member');
    // A member failure advances the walk.
    expect(text).toContain('advances the walk to the next member');
    // No chain, no racing.
    expect(text).toContain('no fallback chain on this lane');
    expect(text).toContain('no racing');
    // The backup-only recourse tier IS part of this lane.
    expect(text).toContain('Backup');
  });

  it('IE6 — the editor is silent about the excluded inactive model (it is not a defect)', () => {
    const onChange = vi.fn();
    const { container } = render(
      <GroupMemberEditor
        members={makeMembers()}
        lane="image-gen"
        laneModels={imageGenLaneModels}
        onChange={onChange}
      />,
    );
    const editor = container.querySelector('[data-testid="group-member-editor"]') as HTMLElement;
    const text = editor.textContent ?? '';
    expect(text).not.toContain('img-off');
    expect(text).not.toContain('Image Off');
    expect(text).not.toContain('disabled');
    expect(text).not.toContain('inactive');
    expect(text).not.toContain('broken');
  });

  it('IE7 — a duplicate member id warns but never blocks', () => {
    const onChange = vi.fn();
    const { container } = render(
      <GroupMemberEditor
        members={makeMembers([
          { model_id: 'img-1', weight: 1 },
          { model_id: 'img-1', weight: 1 },
        ])}
        lane="image-gen"
        laneModels={imageGenLaneModels}
        onChange={onChange}
      />,
    );
    expect(container.querySelectorAll('[data-testid="group-member-row"]').length).toBe(2);
    // The warn hint surfaces (the authoritative block is the BE's G4 400).
    expect(container.querySelector('[data-testid="group-member-dup-warn"]')).not.toBeNull();
    expect(onChange).not.toHaveBeenCalled();
  });
});
