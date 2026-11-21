import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, waitFor } from '@testing-library/preact';
import { GroupModelForm } from '../GroupModelForm';
import type { Model, GroupModel } from '../../../types';

// The IMAGE-GEN lane of the shared group form (imggen-groups).
//
// The form is ONE component with a `lane` prop — never a fork. The
// image-gen lane adds the same two client-side mirrors the decision
// lane has:
//
//   - the lane-consistency guard (edit mode): a stored group whose lane
//     is KNOWN and differs from this form's lane cannot be saved here —
//     the message routes the operator to the other surface;
//   - the shadow-target mirror: the shadow target must be an
//     image-gen-kind model.
//
// This suite ALSO hosts the ONE sanctioned string delta of the phase
// (imggen-groups §4.4): the DECISION surface's lane-guard message used
// to name the retired top-level "Group Models" tab. It is pinned here
// because the existing decision-lane suite must stay UNEDITED (it never
// pinned that literal).

function makeModel(overrides: Partial<Model> = {}): Model {
  return {
    id: 'm1',
    name: 'M1',
    enabled: true,
    fallback_chain: [],
    ...overrides,
  };
}

// The image-gen lane's model feed — the server-prefiltered
// ?kind=image-gen list.
const imageGenModels: Model[] = [
  makeModel({ id: 'img-1', name: 'Image One', kind: 'image-gen' }),
  makeModel({ id: 'img-2', name: 'Image Two', kind: 'image-gen' }),
];

const decisionModels: Model[] = [makeModel({ id: 'dec-1', name: 'Decision One', kind: 'decision' })];

function makeGroup(overrides: Partial<GroupModel> = {}): GroupModel {
  return {
    ...makeModel({ id: 'g1', name: 'Existing', kind: 'group' as const, ...overrides }),
    members: overrides.members ?? [{ model_id: 'img-1', weight: 1 }],
    shadow_model_id: overrides.shadow_model_id ?? '',
  } as GroupModel;
}

// The kind index the parent builds for the lane split.
function makeKindIndex(entries: Record<string, string>): Map<string, string> {
  return new Map(Object.entries(entries));
}

const IMAGE_GEN_KIND_INDEX = makeKindIndex({
  'img-1': 'image-gen',
  'img-2': 'image-gen',
  'chat-1': 'chat',
  'dec-1': 'decision',
});

function makeProps(overrides: Record<string, unknown> = {}) {
  return {
    mode: 'add' as const,
    lane: 'image-gen' as const,
    laneModels: imageGenModels,
    kindIndex: IMAGE_GEN_KIND_INDEX,
    groups: [] as GroupModel[],
    onSave: vi.fn().mockResolvedValue(undefined),
    onCancel: vi.fn(),
    onStatus: vi.fn(),
    ...overrides,
  };
}

describe('GroupModelForm — the image-gen lane (imggen-groups, phase 3)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('IF1 — the submit mirrors fire (member count, cap, unpicked row, weight, default member)', async () => {
    // ≥1 member.
    const minOne = vi.fn();
    const { container: c1 } = render(<GroupModelForm {...makeProps({ onSave: minOne })} />);
    fireEvent.click(c1.querySelector('[data-testid="group-member-remove"]')!);
    fireEvent.input(c1.querySelector('[data-testid="group-form-id-input"]')!, { target: { value: 'g' } });
    fireEvent.input(c1.querySelector('[data-testid="group-form-name-input"]')!, { target: { value: 'G' } });
    fireEvent.click(c1.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() =>
      expect(c1.querySelector('[data-testid="group-form-members-error"]')).not.toBeNull(),
    );
    expect(c1.querySelector('[data-testid="group-form-members-error"]')!.textContent).toContain(
      'At least one member is required',
    );
    expect(minOne).not.toHaveBeenCalled();

    // Cap of 16.
    const cap = vi.fn();
    const seventeen = Array.from({ length: 17 }, () => ({ model_id: 'img-1', weight: 1 }));
    const { container: c2 } = render(
      <GroupModelForm
        {...makeProps({
          mode: 'edit',
          onSave: cap,
          initialData: makeGroup({ members: seventeen }),
          groups: [],
        })}
      />,
    );
    fireEvent.click(c2.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() =>
      expect(c2.querySelector('[data-testid="group-form-members-error"]')).not.toBeNull(),
    );
    expect(c2.querySelector('[data-testid="group-form-members-error"]')!.textContent).toContain(
      'exceeds max of 16',
    );
    expect(cap).not.toHaveBeenCalled();

    // Every row picked.
    const unpicked = vi.fn();
    const { container: c3 } = render(<GroupModelForm {...makeProps({ onSave: unpicked })} />);
    fireEvent.input(c3.querySelector('[data-testid="group-form-id-input"]')!, { target: { value: 'g' } });
    fireEvent.input(c3.querySelector('[data-testid="group-form-name-input"]')!, { target: { value: 'G' } });
    fireEvent.click(c3.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() =>
      expect(c3.querySelector('[data-testid="group-form-members-error"]')).not.toBeNull(),
    );
    expect(c3.querySelector('[data-testid="group-form-members-error"]')!.textContent).toContain(
      'Pick a model for every member row',
    );
    expect(unpicked).not.toHaveBeenCalled();

    // Weight > 0.
    const zeroWeight = vi.fn();
    const { container: c4 } = render(<GroupModelForm {...makeProps({ onSave: zeroWeight })} />);
    fireEvent.input(c4.querySelector('[data-testid="group-form-id-input"]')!, { target: { value: 'g' } });
    fireEvent.input(c4.querySelector('[data-testid="group-form-name-input"]')!, { target: { value: 'G' } });
    fireEvent.change(c4.querySelector('[data-testid="group-member-model-select"]')!, {
      target: { value: 'img-1' },
    });
    fireEvent.input(c4.querySelector('[data-testid="group-member-weight-input"]')!, {
      target: { value: '0' },
    });
    fireEvent.click(c4.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() =>
      expect(c4.querySelector('[data-testid="group-form-members-error"]')).not.toBeNull(),
    );
    expect(c4.querySelector('[data-testid="group-form-members-error"]')!.textContent).toContain(
      'must be > 0',
    );
    expect(zeroWeight).not.toHaveBeenCalled();

    // ≥1 non-backup (default) member.
    const allBackup = vi.fn();
    const { container: c5 } = render(<GroupModelForm {...makeProps({ onSave: allBackup })} />);
    fireEvent.input(c5.querySelector('[data-testid="group-form-id-input"]')!, { target: { value: 'g' } });
    fireEvent.input(c5.querySelector('[data-testid="group-form-name-input"]')!, { target: { value: 'G' } });
    fireEvent.change(c5.querySelector('[data-testid="group-member-model-select"]')!, {
      target: { value: 'img-1' },
    });
    fireEvent.click(c5.querySelector('[data-testid="group-member-backup-only"]')!);
    fireEvent.click(c5.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() =>
      expect(c5.querySelector('[data-testid="group-form-members-error"]')).not.toBeNull(),
    );
    expect(c5.querySelector('[data-testid="group-form-members-error"]')!.textContent).toContain(
      'non-backup (default) member',
    );
    expect(allBackup).not.toHaveBeenCalled();
  });

  it('IF2 — the shadow target must be an image-gen-kind model', async () => {
    const onSave = vi.fn();
    // A stale chat-kind shadow id on an image-gen group: the lane-aware
    // G8 mirror rejects it before the round trip.
    const { container } = render(
      <GroupModelForm
        {...makeProps({
          mode: 'edit',
          onSave,
          initialData: makeGroup({ shadow_model_id: 'chat-1' }),
          groups: [],
        })}
      />,
    );

    fireEvent.click(container.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() =>
      expect(container.querySelector('[data-testid="group-form-shadow-error"]')).not.toBeNull(),
    );
    expect(container.querySelector('[data-testid="group-form-shadow-error"]')!.textContent).toContain(
      'Shadow target chat-1 does not exist (must reference an existing image-gen-kind model)',
    );
    expect(onSave).not.toHaveBeenCalled();
  });

  it('IF3 — the submitted payload carries exactly the group key set', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    const { container } = render(<GroupModelForm {...makeProps({ onSave })} />);

    fireEvent.input(container.querySelector('[data-testid="group-form-id-input"]')!, {
      target: { value: 'img-pool' },
    });
    fireEvent.input(container.querySelector('[data-testid="group-form-name-input"]')!, {
      target: { value: 'Image Pool' },
    });
    fireEvent.change(container.querySelector('[data-testid="group-member-model-select"]')!, {
      target: { value: 'img-1' },
    });

    fireEvent.click(container.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));

    // The EXACT key set — an extra key (a provider field included)
    // fails this assertion.
    expect(onSave.mock.calls[0][0]).toEqual({
      id: 'img-pool',
      name: 'Image Pool',
      kind: 'group',
      internal: true,
      members: [{ model_id: 'img-1', weight: 1 }],
      shadow_model_id: '',
      enabled: true,
      fallback_chain: [],
      credentials: [],
    });
  });

  it('IF4 — the image-gen surface\'s lane-consistency guard routes BOTH other homes (chat and decision)', async () => {
    const onSave = vi.fn();
    // A known-lane CHAT group opened in the IMAGE-GEN form.
    const { container } = render(
      <GroupModelForm
        {...makeProps({
          mode: 'edit',
          onSave,
          initialData: makeGroup({ members: [{ model_id: 'chat-1', weight: 1 }] }),
          groups: [],
        })}
      />,
    );

    fireEvent.click(container.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() =>
      expect(container.querySelector('[data-testid="group-form-lane-error"]')).not.toBeNull(),
    );
    const message = container.querySelector('[data-testid="group-form-lane-error"]')!.textContent ?? '';
    // The static string routes BOTH homes (the image-gen surface can
    // host either of the two older lanes — the lane is inferred).
    expect(message).toContain('This is a chat or decision group');
    expect(message).toContain('Models → Group (chat)');
    expect(message).toContain('Decision Models → Group (decision)');
    expect(onSave).not.toHaveBeenCalled();
  });

  it('IF5 — the DECISION surface\'s guard names the post-consolidation chat home (the phase\'s ONE sanctioned string delta)', async () => {
    const onSave = vi.fn();
    // A known-lane CHAT group opened in the DECISION form. The message
    // used to name the retired top-level "Group Models" tab; it now
    // names the Text container's Group child ("Models → Group").
    const { container } = render(
      <GroupModelForm
        {...makeProps({
          mode: 'edit',
          lane: 'decision',
          laneModels: decisionModels,
          onSave,
          initialData: makeGroup({ members: [{ model_id: 'chat-1', weight: 1 }] }),
          groups: [],
        })}
      />,
    );

    fireEvent.click(container.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() =>
      expect(container.querySelector('[data-testid="group-form-lane-error"]')).not.toBeNull(),
    );
    const message = container.querySelector('[data-testid="group-form-lane-error"]')!.textContent ?? '';
    expect(message).toContain('This is a chat group — edit it under Models → Group.');
    // The retired tab is NOT named any more.
    expect(message).not.toContain('Group Models');
    expect(onSave).not.toHaveBeenCalled();
  });

  it('IF6 — the image-gen lane\'s fixed-constants copy states what the lane builds (no chain, no racing)', () => {
    const { container } = render(<GroupModelForm {...makeProps()} />);
    const text = container.textContent ?? '';
    expect(text).toContain('Image-gen group — kind: group · internal: true');
    // Backup-only members are a recourse tier on this lane.
    expect(text).toContain('backup-only members are recourse-tier candidates');
    // The members fail over across the group.
    expect(text).toContain('Members fail over across the group');
    // No chain, no racing — the copy never promises what the lane lacks.
    expect(text).toContain('No fallback chain and no racing on this lane.');
  });

  it('IF7 — a known-lane IMAGE-GEN group opened in the decision form is blocked', async () => {
    const onSave = vi.fn();
    const { container } = render(
      <GroupModelForm
        {...makeProps({
          mode: 'edit',
          lane: 'decision',
          laneModels: decisionModels,
          onSave,
          initialData: makeGroup({ members: [{ model_id: 'img-1', weight: 1 }] }),
          groups: [],
        })}
      />,
    );

    fireEvent.click(container.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() =>
      expect(container.querySelector('[data-testid="group-form-lane-error"]')).not.toBeNull(),
    );
    expect(container.querySelector('[data-testid="group-form-lane-error"]')!.textContent).toContain(
      'This is a chat group',
    );
    expect(onSave).not.toHaveBeenCalled();
  });

  it('IF8 — an unknown-lane group opened in the image-gen form is not blocked (the repair path stays open)', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    // Every member row vanished: no member votes, so the stored lane is
    // UNKNOWN and the guard does not fire (the row the operator needs
    // to REPAIR must stay editable).
    const { container } = render(
      <GroupModelForm
        {...makeProps({
          mode: 'edit',
          onSave,
          initialData: makeGroup({ members: [{ model_id: 'ghost', weight: 1 }] }),
          groups: [],
        })}
      />,
    );

    fireEvent.click(container.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(container.querySelector('[data-testid="group-form-lane-error"]')).toBeNull();
  });
});
