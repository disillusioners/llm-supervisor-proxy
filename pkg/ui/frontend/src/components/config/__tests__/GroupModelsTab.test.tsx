import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, waitFor } from '@testing-library/preact';
import { GroupModelsTab } from '../GroupModelsTab';
import type { Model, GroupModel } from '../../../types';

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
    ...makeModel({ kind: 'group' as const, ...overrides }),
    members: overrides.members ?? [{ model_id: 'coding', weight: 1 }],
    shadow_model_id: overrides.shadow_model_id ?? '',
  } as GroupModel;
}

const chatModels = [
  makeModel({ id: 'coding', name: 'Coding' }),
  makeModel({ id: 'helper', name: 'Helper' }),
];

function makeProps(overrides: Record<string, unknown> = {}) {
  return {
    models: [] as Model[],
    chatModels,
    onAddModel: vi.fn(),
    onUpdateModel: vi.fn(),
    onDeleteModel: vi.fn(),
    onToggleModel: vi.fn(),
    setStatus: vi.fn(),
    ...overrides,
  };
}

describe('GroupModelsTab — list/CRUD shell (load-balance-groups P3.6)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('renders the empty state with the Add Group Model CTA', () => {
    const { container } = render(<GroupModelsTab {...makeProps()} />);
    expect(container.textContent).toContain('No group models configured');
    expect(container.textContent).toContain('Add Group Model');
    expect(container.textContent).toContain('Group Models');
  });

  it('renders one row per group with member chips + the shadow line', () => {
    const groups = [
      makeGroup({ id: 'g1', name: 'Pool One', members: [{ model_id: 'coding', weight: 2 }, { model_id: 'helper', weight: 3 }], shadow_model_id: 'coding' }),
      makeGroup({ id: 'g2', name: 'Pool Two', enabled: false }),
    ];
    const { container } = render(<GroupModelsTab {...makeProps({ models: groups })} />);

    const rows = container.querySelectorAll('[data-testid="group-model-row"]');
    expect(rows.length).toBe(2);
    expect((rows[0] as HTMLElement).getAttribute('data-model-id')).toBe('g1');
    expect(container.textContent).toContain('coding');
    expect(container.textContent).toContain('×2');
    expect(container.textContent).toContain('helper');
    expect(container.textContent).toContain('×3');
    expect(container.textContent).toContain('SHADOWS: coding');
    expect(container.textContent).toContain('No shadow claim');
    expect(container.textContent).toContain('group');
  });

  it('toggling a row calls onToggleModel with the row model', () => {
    const onToggleModel = vi.fn();
    const groups = [makeGroup({ id: 'g1', name: 'Pool One' })];
    const { container } = render(<GroupModelsTab {...makeProps({ models: groups, onToggleModel })} />);
    fireEvent.click(container.querySelector('[data-testid="group-model-row"] button[aria-label^="Toggle"]')!);
    expect(onToggleModel).toHaveBeenCalledTimes(1);
    expect(onToggleModel.mock.calls[0][0].id).toBe('g1');
  });

  it('Add Group Model opens the form; a valid submit routes the stamped payload to onAddModel', async () => {
    const onAddModel = vi.fn().mockResolvedValue(undefined);
    const setStatus = vi.fn();
    const { container } = render(<GroupModelsTab {...makeProps({ onAddModel, setStatus })} />);

    fireEvent.click(Array.from(container.querySelectorAll('button')).find((b) => b.textContent!.includes('Add Group Model'))!);
    await waitFor(() => expect(container.querySelector('[data-testid="group-model-form"]')).not.toBeNull());

    fireEvent.input(container.querySelector('[data-testid="group-form-id-input"]')!, { target: { value: 'new-group' } });
    fireEvent.input(container.querySelector('[data-testid="group-form-name-input"]')!, { target: { value: 'New Group' } });
    fireEvent.change(container.querySelector('[data-testid="group-member-model-select"]')!, { target: { value: 'coding' } });

    fireEvent.click(container.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() => expect(onAddModel).toHaveBeenCalledTimes(1));

    const payload = onAddModel.mock.calls[0][0];
    expect(payload.kind).toBe('group');
    expect(payload.internal).toBe(true);
    expect(payload.exclude_from_ultimate_switching).toBe(true);
    expect(setStatus).toHaveBeenCalledWith({ type: 'success', message: 'Group model added successfully' });
  });

  it('Edit opens the form hydrated with the row; submit routes the update to onUpdateModel', async () => {
    const onUpdateModel = vi.fn().mockResolvedValue(undefined);
    const groups = [makeGroup({ id: 'g1', name: 'Pool One', members: [{ model_id: 'coding', weight: 2 }], shadow_model_id: 'coding' })];
    const { container } = render(<GroupModelsTab {...makeProps({ models: groups, onUpdateModel })} />);

    fireEvent.click(container.querySelector('button[aria-label="Edit Pool One"]')!);
    await waitFor(() => expect(container.querySelector('[data-testid="group-model-form"]')).not.toBeNull());
    expect((container.querySelector('[data-testid="group-form-name-input"]') as HTMLInputElement).value).toBe('Pool One');

    fireEvent.click(container.querySelector('[data-testid="group-form-submit"]')!);
    await waitFor(() => expect(onUpdateModel).toHaveBeenCalledTimes(1));
    const [id, updates] = onUpdateModel.mock.calls[0];
    expect(id).toBe('g1');
    // The edit payload stamps kind/internal too (the BE PUT decodes
    // wholesale — a sparse PUT dropping kind would skip the group arm).
    expect(updates.kind).toBe('group');
    expect(updates.internal).toBe(true);
    expect(updates.members).toEqual([{ model_id: 'coding', weight: 2 }]);
    expect(updates.shadow_model_id).toBe('coding');
  });

  it('Delete opens the confirm modal; confirming calls onDeleteModel with the row id', async () => {
    const onDeleteModel = vi.fn().mockResolvedValue(undefined);
    const groups = [makeGroup({ id: 'g1', name: 'Pool One' })];
    const { container } = render(<GroupModelsTab {...makeProps({ models: groups, onDeleteModel })} />);

    fireEvent.click(container.querySelector('button[aria-label="Delete Pool One"]')!);
    await waitFor(() => expect(container.querySelector('[data-testid="group-delete-modal"]')).not.toBeNull());
    expect(container.textContent).toContain('Delete Group Model');

    fireEvent.click(container.querySelector('[data-testid="group-delete-confirm"]')!);
    await waitFor(() => expect(onDeleteModel).toHaveBeenCalledWith('g1'));
  });

  it('a failed delete (409 member-in-use) surfaces the BE error message via setStatus', async () => {
    const onDeleteModel = vi.fn().mockRejectedValue(new Error('model coding is a member of group g1'));
    const setStatus = vi.fn();
    const groups = [makeGroup({ id: 'g1', name: 'Pool One' })];
    const { container } = render(<GroupModelsTab {...makeProps({ models: groups, onDeleteModel, setStatus })} />);

    fireEvent.click(container.querySelector('button[aria-label="Delete Pool One"]')!);
    await waitFor(() => expect(container.querySelector('[data-testid="group-delete-modal"]')).not.toBeNull());
    fireEvent.click(container.querySelector('[data-testid="group-delete-confirm"]')!);

    await waitFor(() =>
      expect(setStatus).toHaveBeenCalledWith(
        expect.objectContaining({ type: 'error', message: expect.stringContaining('member of group') }),
      ),
    );
  });

  it('fetchError renders the alert-role banner with a Retry button', () => {
    const onRetry = vi.fn();
    const { container } = render(<GroupModelsTab {...makeProps({ fetchError: 'HTTP 500', onRetry })} />);
    const banner = container.querySelector('[data-testid="group-fetch-error"]') as HTMLElement;
    expect(banner).not.toBeNull();
    expect(banner.getAttribute('role')).toBe('alert');
    expect(banner.textContent).toContain('Failed to load group models');
    fireEvent.click(banner.querySelector('button')!);
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it('Am30 — a wrapPreloadModel opens the pre-filled add-form and consumes the signal once', async () => {
    const onWrapConsumed = vi.fn();
    const wrapTarget = makeModel({ id: 'coding', name: 'Coding' });
    const { container } = render(<GroupModelsTab {...makeProps({ wrapPreloadModel: wrapTarget, onWrapConsumed })} />);

    await waitFor(() => expect(container.querySelector('[data-testid="group-model-form"]')).not.toBeNull());
    // Pre-filled with the canonical coding[coding] shape.
    expect((container.querySelector('[data-testid="group-form-id-input"]') as HTMLInputElement).value).toBe('coding[coding]');
    expect((container.querySelector('[data-testid="group-form-shadow-select"]') as HTMLSelectElement).value).toBe('coding');
    expect(onWrapConsumed).toHaveBeenCalledTimes(1);
  });
});
