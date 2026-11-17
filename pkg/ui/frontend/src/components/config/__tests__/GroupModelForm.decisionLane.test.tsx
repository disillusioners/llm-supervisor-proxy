import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, waitFor } from '@testing-library/preact';
import { GroupModelForm } from '../GroupModelForm';
import type { Model, GroupModel } from '../../../types';

// The DECISION lane of the shared group form.
//
// The form is ONE component with a `lane` prop — never a fork. The
// decision lane adds two client-side mirrors:
//
//   - the lane-consistency guard (edit mode): a stored group whose
//     lane is KNOWN and differs from this form's lane cannot be saved
//     here — the message routes the operator to the other tab;
//   - the homogeneity mirror: a staged member that is not a
//     decision-kind model blocks the submit.
//
// The server stays authoritative on every write — these are
// belt-and-suspenders gates that surface a clear message before the
// round trip.

function makeModel(overrides: Partial<Model> = {}): Model {
  return {
    id: 'm1',
    name: 'M1',
    enabled: true,
    fallback_chain: [],
    ...overrides,
  };
}

// The decision lane's model feed — the server-prefiltered
// ?kind=decision list. It carries DECISION rows only (the server
// filters them), which is why a chat-kind id is never in it.
const decisionModels: Model[] = [
  makeModel({ id: 'dec-1', name: 'Decision One', kind: 'decision' }),
  makeModel({ id: 'dec-2', name: 'Decision Two', kind: 'decision' }),
];

const chatModels: Model[] = [makeModel({ id: 'chat-1', name: 'Chat One' })];

function makeGroup(overrides: Partial<GroupModel> = {}): GroupModel {
  return {
    ...makeModel({ id: 'g1', name: 'Existing', kind: 'group' as const, ...overrides }),
    members: overrides.members ?? [{ model_id: 'dec-1', weight: 1 }],
    shadow_model_id: overrides.shadow_model_id ?? '',
  } as GroupModel;
}

// The kind index the parent builds for the lane split.
function makeKindIndex(entries: Record<string, string>): Map<string, string> {
  return new Map(Object.entries(entries));
}

const DECISION_KIND_INDEX = makeKindIndex({
  'dec-1': 'decision',
  'dec-2': 'decision',
  'chat-1': 'chat',
});

function makeProps(overrides: Record<string, unknown> = {}) {
  return {
    mode: 'add' as const,
    lane: 'decision' as const,
    laneModels: decisionModels,
    kindIndex: DECISION_KIND_INDEX,
    groups: [] as GroupModel[],
    onSave: vi.fn().mockResolvedValue(undefined),
    onCancel: vi.fn(),
    onStatus: vi.fn(),
    ...overrides,
  };
}

describe('GroupModelForm — the decision lane (decision-groups, phase 2)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('D1 — the submit mirrors fire (member count, cap, unpicked row, weight, default member)', async () => {
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
    const seventeen = Array.from({ length: 17 }, () => ({ model_id: 'dec-1', weight: 1 }));
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
      target: { value: 'dec-1' },
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
      target: { value: 'dec-1' },
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

  it('D2 — a staged member that is not decision-kind is rejected before the round trip', async () => {
    const onSave = vi.fn();
    // A MIXED stored group (one decision member + one chat member + one
    // vanished member): the vanished member carries no lane evidence
    // (server :378-381) and the SURVIVING set is mixed, so the lane is
    // UNKNOWN (server :96-99) — the lane-consistency guard does not
    // fire and the homogeneity mirror owns the rejection.
    const { container } = render(
      <GroupModelForm
        {...makeProps({
          mode: 'edit',
          onSave,
          initialData: makeGroup({
            members: [
              { model_id: 'dec-1', weight: 1 },
              { model_id: 'chat-1', weight: 1 },
              { model_id: 'ghost', weight: 1 },
            ],
          }),
          groups: [],
        })}
      />,
    );

    fireEvent.click(container.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() =>
      expect(container.querySelector('[data-testid="group-form-members-error"]')).not.toBeNull(),
    );
    expect(container.querySelector('[data-testid="group-form-members-error"]')!.textContent).toContain(
      'Member chat-1 is not a decision-kind model',
    );
    expect(onSave).not.toHaveBeenCalled();
  });

  it('D3 — the shadow target must be a decision-kind model', async () => {
    const onSave = vi.fn();
    // A stale chat-kind shadow id on a decision group: the lane-aware
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
      'Shadow target chat-1 does not exist (must reference an existing decision-kind model)',
    );
    expect(onSave).not.toHaveBeenCalled();
  });

  it('D4 — the shadow dual-claim scan rejects a second decision group claiming the same id', async () => {
    const onSave = vi.fn();
    const claimant = {
      ...makeModel({ id: 'other-group', name: 'Other', kind: 'group' as const }),
      enabled: true,
      members: [{ model_id: 'dec-2', weight: 1 }],
      shadow_model_id: 'dec-2',
    } as GroupModel;
    const { container } = render(
      <GroupModelForm
        {...makeProps({
          mode: 'edit',
          onSave,
          initialData: makeGroup({ shadow_model_id: 'dec-2' }),
          groups: [claimant],
        })}
      />,
    );

    fireEvent.click(container.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() =>
      expect(container.querySelector('[data-testid="group-form-shadow-error"]')).not.toBeNull(),
    );
    expect(container.querySelector('[data-testid="group-form-shadow-error"]')!.textContent).toContain(
      'already claimed by enabled group other-group',
    );
    expect(onSave).not.toHaveBeenCalled();
  });

  it('D5 — the submitted payload carries exactly the group key set', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    const { container } = render(<GroupModelForm {...makeProps({ onSave })} />);

    fireEvent.input(container.querySelector('[data-testid="group-form-id-input"]')!, {
      target: { value: 'dec-pool' },
    });
    fireEvent.input(container.querySelector('[data-testid="group-form-name-input"]')!, {
      target: { value: 'Decision Pool' },
    });
    fireEvent.change(container.querySelector('[data-testid="group-member-model-select"]')!, {
      target: { value: 'dec-1' },
    });

    fireEvent.click(container.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));

    // The EXACT key set — an extra key (a provider field included)
    // fails this assertion.
    expect(onSave.mock.calls[0][0]).toEqual({
      id: 'dec-pool',
      name: 'Decision Pool',
      kind: 'group',
      internal: true,
      members: [{ model_id: 'dec-1', weight: 1 }],
      shadow_model_id: '',
      enabled: true,
      fallback_chain: [],
      credentials: [],
    });
  });

  it('D6 — edit mode: the lane-consistency block message names the other surface so the operator is routed to the right tab', async () => {
    const onSave = vi.fn();
    // A known-lane DECISION group opened in the TEXT form.
    const { container } = render(
      <GroupModelForm
        {...makeProps({
          mode: 'edit',
          lane: 'chat',
          laneModels: chatModels,
          onSave,
          initialData: makeGroup({ members: [{ model_id: 'dec-1', weight: 1 }] }),
          groups: [],
        })}
      />,
    );

    fireEvent.click(container.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() =>
      expect(container.querySelector('[data-testid="group-form-lane-error"]')).not.toBeNull(),
    );
    const message = container.querySelector('[data-testid="group-form-lane-error"]')!.textContent ?? '';
    // The message ROUTES: it names the OTHER surface (the Decision
    // Models tab and its Group child tab), not the current one.
    expect(message).toContain('Decision Models');
    expect(message).toContain('Group');
    expect(message).not.toContain('Group Models');
    expect(onSave).not.toHaveBeenCalled();
  });

  it('D6a — a known-lane decision group opened in the text form is blocked with the cross-tab message', async () => {
    const onSave = vi.fn();
    const { container } = render(
      <GroupModelForm
        {...makeProps({
          mode: 'edit',
          lane: 'chat',
          laneModels: chatModels,
          onSave,
          initialData: makeGroup({ members: [{ model_id: 'dec-1', weight: 1 }] }),
          groups: [],
        })}
      />,
    );

    fireEvent.click(container.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() =>
      expect(container.querySelector('[data-testid="group-form-lane-error"]')).not.toBeNull(),
    );
    expect(container.querySelector('[data-testid="group-form-lane-error"]')!.textContent).toContain(
      'This is a decision group',
    );
    expect(onSave).not.toHaveBeenCalled();

    // The same holds when ONE member row VANISHED: the vanished row
    // carries no lane evidence (the server skips it — store_group_rewrite.go
    // :378-381) and the SURVIVING decision member still decides the
    // lane, so the group stays known-lane and is blocked too.
    const onSaveVanished = vi.fn();
    const { container: c2 } = render(
      <GroupModelForm
        {...makeProps({
          mode: 'edit',
          lane: 'chat',
          laneModels: chatModels,
          onSave: onSaveVanished,
          initialData: makeGroup({
            members: [
              { model_id: 'dec-1', weight: 1 },
              { model_id: 'ghost', weight: 1 },
            ],
          }),
          groups: [],
        })}
      />,
    );

    fireEvent.click(c2.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() =>
      expect(c2.querySelector('[data-testid="group-form-lane-error"]')).not.toBeNull(),
    );
    expect(c2.querySelector('[data-testid="group-form-lane-error"]')!.textContent).toContain(
      'This is a decision group',
    );
    expect(onSaveVanished).not.toHaveBeenCalled();
  });

  it('D6b — a known-lane chat group opened in the decision form is blocked', async () => {
    const onSave = vi.fn();
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
    expect(container.querySelector('[data-testid="group-form-lane-error"]')!.textContent).toContain(
      'This is a chat group',
    );
    expect(onSave).not.toHaveBeenCalled();
  });

  it('D6c — an unknown-lane group opened in the text form is not blocked', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    // An EMPTY surviving set ⇒ the stored lane is UNKNOWN ⇒ the guard
    // does not fire: every member row vanished, so no member votes and
    // the server's fold yields no lane at all (models.UnanimousGroupLane
    // :96-99). The submit proceeds and the server's validators decide
    // (the row is the one an operator needs to REPAIR — blocking it
    // would leave delete + recreate as the only path).
    const { container } = render(
      <GroupModelForm
        {...makeProps({
          mode: 'edit',
          lane: 'chat',
          laneModels: chatModels,
          kindIndex: makeKindIndex({ 'dec-1': 'decision' }),
          onSave,
          initialData: makeGroup({ members: [{ model_id: 'ghost', weight: 1 }] }),
          groups: [],
        })}
      />,
    );

    fireEvent.click(container.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(container.querySelector('[data-testid="group-form-lane-error"]')).toBeNull();

    // The other unknown shape: a MIXED surviving set (a chat member
    // next to a decision member) is unknown too — the server's fold
    // finds two valid lanes and no unanimity (server :96-99). It is
    // NOT blocked either.
    const onSaveMixed = vi.fn().mockResolvedValue(undefined);
    const { container: c2 } = render(
      <GroupModelForm
        {...makeProps({
          mode: 'edit',
          lane: 'chat',
          laneModels: chatModels,
          kindIndex: makeKindIndex({ 'dec-1': 'decision', 'chat-1': 'chat' }),
          onSave: onSaveMixed,
          initialData: makeGroup({
            members: [
              { model_id: 'dec-1', weight: 1 },
              { model_id: 'chat-1', weight: 1 },
            ],
          }),
          groups: [],
        })}
      />,
    );

    fireEvent.click(c2.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() => expect(onSaveMixed).toHaveBeenCalledTimes(1));
    expect(c2.querySelector('[data-testid="group-form-lane-error"]')).toBeNull();
  });

  it('D6d — with the kind index absent the conservative fallback blocks the submit', async () => {
    const onSave = vi.fn();
    // No kind index ⇒ the guard falls back to the conservative rule: a
    // stored member that is not one of this lane's models means the
    // stored group belongs to the other lane.
    const { container } = render(
      <GroupModelForm
        {...makeProps({
          mode: 'edit',
          lane: 'chat',
          laneModels: chatModels,
          kindIndex: undefined,
          onSave,
          initialData: makeGroup({ members: [{ model_id: 'dec-1', weight: 1 }] }),
          groups: [],
        })}
      />,
    );

    fireEvent.click(container.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() =>
      expect(container.querySelector('[data-testid="group-form-lane-error"]')).not.toBeNull(),
    );
    expect(onSave).not.toHaveBeenCalled();
  });

  it('D8 — the submitted members carry no enabled key and the group flag is untouched', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    const { container } = render(<GroupModelForm {...makeProps({ onSave })} />);

    fireEvent.input(container.querySelector('[data-testid="group-form-id-input"]')!, {
      target: { value: 'dec-pool' },
    });
    fireEvent.input(container.querySelector('[data-testid="group-form-name-input"]')!, {
      target: { value: 'Decision Pool' },
    });
    fireEvent.change(container.querySelector('[data-testid="group-member-model-select"]')!, {
      target: { value: 'dec-1' },
    });
    // Toggle the GROUP's own enable flag off — the members' state is
    // untouched by it.
    fireEvent.click(container.querySelector('[data-testid="group-form-enabled-toggle"]')!);

    fireEvent.click(container.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));

    const payload = onSave.mock.calls[0][0];
    expect(Object.keys(payload.members[0]).sort()).toEqual(['model_id', 'weight']);
    expect('enabled' in payload.members[0]).toBe(false);
    expect(payload.enabled).toBe(false);
  });

  it('D9 — a single-member decision group submits successfully (no two-member rule)', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    const { container } = render(<GroupModelForm {...makeProps({ onSave })} />);

    fireEvent.input(container.querySelector('[data-testid="group-form-id-input"]')!, {
      target: { value: 'dec-pool' },
    });
    fireEvent.input(container.querySelector('[data-testid="group-form-name-input"]')!, {
      target: { value: 'Decision Pool' },
    });
    fireEvent.change(container.querySelector('[data-testid="group-member-model-select"]')!, {
      target: { value: 'dec-1' },
    });

    fireEvent.click(container.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0][0].members).toEqual([{ model_id: 'dec-1', weight: 1 }]);
    expect(container.querySelector('[data-testid="group-form-members-error"]')).toBeNull();
  });

  it('D10 — the decision-lane member hint promises only per-credential resilience', () => {
    const { container } = render(<GroupModelForm {...makeProps()} />);
    const editor = container.querySelector('[data-testid="group-member-editor"]') as HTMLElement;
    const hint = editor.textContent ?? '';
    // The promise that IS made.
    expect(hint).toContain('per-credential attempts within the member');
    // The promises that are NOT made on this lane: no full pipeline,
    // no race chain, no "fallback, race".
    expect(hint).not.toContain('full pipeline');
    expect(hint).not.toContain('race chain');
    expect(hint).not.toContain('fallback, race');

    // The fixed-constants line is lane-gated too: the chat lane's
    // "in the race chain" promise does not exist here.
    const constants = container.querySelector('[data-testid="group-form-fixed-constants"]') as HTMLElement;
    expect(constants.textContent).toContain('recourse-tier candidates');
    expect(constants.textContent).toContain('No fallback chain and no racing on this lane');
    expect(constants.textContent).not.toContain('race chain');
  });
});
