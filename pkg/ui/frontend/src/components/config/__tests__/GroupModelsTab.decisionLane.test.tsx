import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, waitFor } from '@testing-library/preact';
import { GroupModelsTab } from '../GroupModelsTab';
import type { Model, GroupModel } from '../../../types';

// The DECISION lane of the shared group surface.
//
// ONE component, TWO lanes: the same GroupModelsTab renders the text
// lane's group list and the decision lane's group list. The lane
// changes the chrome (heading / CTA / empty state / toast / row badge)
// and the member + shadow feeds — never the component's shape.
//
// The decision lane's copy must NOT promise resilience that does not
// exist on this lane: there is no fallback chain and no racing engine
// there (ADR-11 OQ-2 / OQ-3 option (a)).

function makeModel(overrides: Partial<Model> = {}): Model {
  return {
    id: 'm1',
    name: 'M1',
    enabled: true,
    fallback_chain: [],
    ...overrides,
  };
}

function makeGroup(overrides: Partial<GroupModel> = {}): GroupModel {
  return {
    ...makeModel({ id: 'g1', name: 'Decision Pool', kind: 'group' as const, ...overrides }),
    members: overrides.members ?? [{ model_id: 'dec-1', weight: 1 }],
    shadow_model_id: overrides.shadow_model_id ?? '',
  } as GroupModel;
}

const decisionModels: Model[] = [
  makeModel({ id: 'dec-1', name: 'Decision One', kind: 'decision' }),
  makeModel({ id: 'dec-2', name: 'Decision Two', kind: 'decision' }),
  makeModel({ id: 'chat-1', name: 'Chat One' }),
];

function makeProps(overrides: Record<string, unknown> = {}) {
  return {
    lane: 'decision' as const,
    models: [] as Model[],
    laneModels: decisionModels,
    onAddModel: vi.fn(),
    onUpdateModel: vi.fn(),
    onDeleteModel: vi.fn(),
    onToggleModel: vi.fn(),
    setStatus: vi.fn(),
    ...overrides,
  };
}

describe('GroupModelsTab — the decision lane (decision-groups, phase 2)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('B1 — the decision lane renders its own heading, CTA and empty state', () => {
    const { container } = render(<GroupModelsTab {...makeProps()} />);
    expect(container.textContent).toContain('Decision Groups');
    expect(container.textContent).toContain('Add Decision Group');
    expect(container.textContent).toContain('No decision groups configured');
    // The text lane's chrome must not leak into the decision lane.
    expect(container.textContent).not.toContain('No group models configured');
  });

  it('B2 — the decision-lane empty state states the full semantics (weight per request, no pinning, per-credential resilience, no chain concept)', () => {
    const { container } = render(<GroupModelsTab {...makeProps()} />);
    const empty = container.querySelector('[data-testid="group-empty-state"]') as HTMLElement;
    const text = empty.textContent ?? '';
    // Members are picked by weight per request.
    expect(text).toContain('picked by weight per request');
    // No per-conversation pinning (Q6).
    expect(text).toContain('no per-conversation pinning');
    // Resilience is per-credential attempts WITHIN a member.
    expect(text).toContain('per-credential attempts within a member');
    // A fallback chain has no equivalent concept on this lane.
    expect(text).toContain('no equivalent concept on this lane');
    expect(text).toContain('deliberately not built');
  });

  it('B3 — the tab renders exactly the groups it is given (the parent owns the lane split)', () => {
    // A mis-filed group (chat members) is still rendered: the tab does
    // NOT re-filter client-side — the parent scoped the list.
    const groups = [
      makeGroup({ id: 'g1', name: 'Decision Pool' }),
      makeGroup({ id: 'g2', name: 'Misfiled Pool', members: [{ model_id: 'chat-1', weight: 1 }] }),
    ];
    const { container } = render(<GroupModelsTab {...makeProps({ models: groups })} />);
    const rows = container.querySelectorAll('[data-testid="group-model-row"]');
    expect(rows.length).toBe(2);
    expect((rows[0] as HTMLElement).getAttribute('data-model-id')).toBe('g1');
    expect((rows[1] as HTMLElement).getAttribute('data-model-id')).toBe('g2');
  });

  it('B4 — adding a decision group stamps the group payload (kind, internal, empty chain, empty credentials, decision members)', async () => {
    const onAddModel = vi.fn().mockResolvedValue(undefined);
    const setStatus = vi.fn();
    const { container } = render(
      <GroupModelsTab {...makeProps({ onAddModel, setStatus })} />,
    );

    fireEvent.click(
      Array.from(container.querySelectorAll('button')).find((b) =>
        (b.textContent ?? '').includes('Add Decision Group'),
      ) as HTMLButtonElement,
    );
    await waitFor(() =>
      expect(container.querySelector('[data-testid="group-model-form"]')).not.toBeNull(),
    );

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
    await waitFor(() => expect(onAddModel).toHaveBeenCalledTimes(1));

    const payload = onAddModel.mock.calls[0][0];
    expect(payload.kind).toBe('group');
    expect(payload.internal).toBe(true);
    expect(payload.fallback_chain).toEqual([]);
    expect(payload.credentials).toEqual([]);
    expect(payload.members).toEqual([{ model_id: 'dec-1', weight: 1 }]);
    // The decision-lane toast copy.
    expect(setStatus).toHaveBeenCalledWith({
      type: 'success',
      message: 'Decision group added successfully',
    });
  });

  it('B5 — editing a decision group routes a complete PUT body (no sparse PUT)', async () => {
    const onUpdateModel = vi.fn().mockResolvedValue(undefined);
    const groups = [
      makeGroup({
        id: 'g1',
        name: 'Decision Pool',
        members: [{ model_id: 'dec-1', weight: 2 }],
        shadow_model_id: 'dec-1',
      }),
    ];
    const { container } = render(
      <GroupModelsTab {...makeProps({ models: groups, onUpdateModel })} />,
    );

    fireEvent.click(container.querySelector('button[aria-label="Edit Decision Pool"]')!);
    await waitFor(() =>
      expect(container.querySelector('[data-testid="group-model-form"]')).not.toBeNull(),
    );
    fireEvent.click(container.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() => expect(onUpdateModel).toHaveBeenCalledTimes(1));

    const [id, updates] = onUpdateModel.mock.calls[0];
    expect(id).toBe('g1');
    expect(Object.keys(updates).sort()).toEqual(
      ['credentials', 'enabled', 'fallback_chain', 'internal', 'kind', 'members', 'name', 'shadow_model_id'],
    );
    expect(updates.kind).toBe('group');
    expect(updates.internal).toBe(true);
    expect(updates.members).toEqual([{ model_id: 'dec-1', weight: 2 }]);
    expect(updates.shadow_model_id).toBe('dec-1');
  });

  it('B6 — the delete-confirm modal routes the delete and the decision-lane toast copy fires', async () => {
    const onDeleteModel = vi.fn().mockResolvedValue(undefined);
    const setStatus = vi.fn();
    const groups = [makeGroup({ id: 'g1', name: 'Decision Pool' })];
    const { container } = render(
      <GroupModelsTab {...makeProps({ models: groups, onDeleteModel, setStatus })} />,
    );

    fireEvent.click(container.querySelector('button[aria-label="Delete Decision Pool"]')!);
    await waitFor(() =>
      expect(container.querySelector('[data-testid="group-delete-modal"]')).not.toBeNull(),
    );

    fireEvent.click(container.querySelector('[data-testid="group-delete-confirm"]')!);
    await waitFor(() => expect(onDeleteModel).toHaveBeenCalledWith('g1'));
    expect(setStatus).toHaveBeenCalledWith({
      type: 'success',
      message: 'Decision group deleted successfully',
    });
  });

  it('B7 — the decision lane offers no one-click-wrap pre-fill (the wrap signal is text-lane only)', async () => {
    const wrapTarget = makeModel({ id: 'dec-1', name: 'Decision One', kind: 'decision' });
    const { container } = render(
      <GroupModelsTab {...makeProps({ wrapPreloadModel: wrapTarget })} />,
    );

    // Give the effect a chance to run before asserting.
    await waitFor(() => expect(container.querySelector('[data-testid="group-models-list"]')).not.toBeNull());
    expect(container.querySelector('[data-testid="group-model-form"]')).toBeNull();
    // The list chrome is still the list chrome.
    expect(container.textContent).toContain('Add Decision Group');
  });

  it('B9 — the decision-lane group row carries the decision glyph, not the load-balancing glyph', () => {
    const groups = [makeGroup({ id: 'g1', name: 'Decision Pool' })];
    const { container } = render(<GroupModelsTab {...makeProps({ models: groups })} />);
    const row = container.querySelector('[data-testid="group-model-row"]') as HTMLElement;
    const glyph = row.querySelector('span[aria-hidden="true"]') as HTMLElement;
    expect(glyph.textContent).toBe('🎯');
    expect(row.textContent).not.toContain('🔀');
  });
});
