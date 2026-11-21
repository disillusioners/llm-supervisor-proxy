import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, waitFor } from '@testing-library/preact';
import { GroupModelsTab } from '../GroupModelsTab';
import type { Model, GroupModel } from '../../../types';

// The IMAGE-GEN lane of the shared group surface (imggen-groups).
//
// ONE component, THREE lanes: the same GroupModelsTab renders the text
// lane's group list, the decision lane's group list and the image-gen
// lane's group list. The lane changes the chrome (heading / CTA / empty
// state / toast / row badge) and the member + shadow feeds — never the
// component's shape.
//
// The image-gen lane's copy must NOT promise resilience that does not
// exist on this lane: there is no fallback chain, no racing engine and
// no per-conversation pinning there (IA-3 — the copy promises a
// weighted pick, a failover walk and per-credential rotation ONLY).

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
    ...makeModel({ id: 'g1', name: 'Image Pool', kind: 'group' as const, ...overrides }),
    members: overrides.members ?? [{ model_id: 'img-1', weight: 1 }],
    shadow_model_id: overrides.shadow_model_id ?? '',
  } as GroupModel;
}

// The image-gen lane's model feed — the server-prefiltered
// ?kind=image-gen list.
const imageGenModels: Model[] = [
  makeModel({ id: 'img-1', name: 'Image One', kind: 'image-gen' }),
  makeModel({ id: 'img-2', name: 'Image Two', kind: 'image-gen' }),
  makeModel({ id: 'chat-1', name: 'Chat One' }),
];

function makeProps(overrides: Record<string, unknown> = {}) {
  return {
    lane: 'image-gen' as const,
    models: [] as Model[],
    laneModels: imageGenModels,
    onAddModel: vi.fn(),
    onUpdateModel: vi.fn(),
    onDeleteModel: vi.fn(),
    onToggleModel: vi.fn(),
    setStatus: vi.fn(),
    ...overrides,
  };
}

describe('GroupModelsTab — the image-gen lane (imggen-groups, phase 3)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('IG1 — the image-gen lane renders its own heading, CTA and empty state', () => {
    const { container } = render(<GroupModelsTab {...makeProps()} />);
    expect(container.textContent).toContain('Image-Gen Groups');
    expect(container.textContent).toContain('Add Image-Gen Group');
    expect(container.textContent).toContain('No image-gen groups configured');
    // The chat lane's chrome must not leak into the image-gen lane.
    expect(container.textContent).not.toContain('No group models configured');
  });

  it('IG2 — the image-gen-lane empty state states the full semantics (weight per request, no pinning, a failover walk, per-credential resilience, no chain concept)', () => {
    const { container } = render(<GroupModelsTab {...makeProps()} />);
    const empty = container.querySelector('[data-testid="group-empty-state"]') as HTMLElement;
    const text = empty.textContent ?? '';
    // Members are picked by weight per request.
    expect(text).toContain('picked by weight per request');
    // No per-conversation pinning (the lane has no conversation key).
    expect(text).toContain('no per-conversation pinning');
    // A member failure advances the walk to the next member.
    expect(text).toContain('advances the walk to the next member');
    // Each member rotates through its own credentials first.
    expect(text).toContain('rotates through its own credentials');
    // A fallback chain has no equivalent concept on this lane.
    expect(text).toContain('no equivalent concept on this lane');
    expect(text).toContain('deliberately not built');
  });

  it('IG3 — the tab renders exactly the groups it is given (the parent owns the lane split)', () => {
    // A mis-filed group (chat members) is still rendered: the tab does
    // NOT re-filter client-side — the parent scoped the list.
    const groups = [
      makeGroup({ id: 'g1', name: 'Image Pool' }),
      makeGroup({ id: 'g2', name: 'Misfiled Pool', members: [{ model_id: 'chat-1', weight: 1 }] }),
    ];
    const { container } = render(<GroupModelsTab {...makeProps({ models: groups })} />);
    const rows = container.querySelectorAll('[data-testid="group-model-row"]');
    expect(rows.length).toBe(2);
    expect((rows[0] as HTMLElement).getAttribute('data-model-id')).toBe('g1');
    expect((rows[1] as HTMLElement).getAttribute('data-model-id')).toBe('g2');
  });

  it('IG4 — adding an image-gen group stamps the group payload (kind, internal, empty chain, empty credentials, image-gen members)', async () => {
    const onAddModel = vi.fn().mockResolvedValue(undefined);
    const setStatus = vi.fn();
    const { container } = render(
      <GroupModelsTab {...makeProps({ onAddModel, setStatus })} />,
    );

    fireEvent.click(
      Array.from(container.querySelectorAll('button')).find((b) =>
        (b.textContent ?? '').includes('Add Image-Gen Group'),
      ) as HTMLButtonElement,
    );
    await waitFor(() =>
      expect(container.querySelector('[data-testid="group-model-form"]')).not.toBeNull(),
    );

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
    await waitFor(() => expect(onAddModel).toHaveBeenCalledTimes(1));

    const payload = onAddModel.mock.calls[0][0];
    expect(payload.kind).toBe('group');
    expect(payload.internal).toBe(true);
    expect(payload.fallback_chain).toEqual([]);
    expect(payload.credentials).toEqual([]);
    expect(payload.members).toEqual([{ model_id: 'img-1', weight: 1 }]);
    // The image-gen-lane toast copy.
    expect(setStatus).toHaveBeenCalledWith({
      type: 'success',
      message: 'Image-gen group added successfully',
    });
  });

  it('IG5 — editing an image-gen group routes a complete PUT body (no sparse PUT)', async () => {
    const onUpdateModel = vi.fn().mockResolvedValue(undefined);
    const groups = [
      makeGroup({
        id: 'g1',
        name: 'Image Pool',
        members: [{ model_id: 'img-1', weight: 2 }],
        shadow_model_id: 'img-1',
      }),
    ];
    const { container } = render(
      <GroupModelsTab {...makeProps({ models: groups, onUpdateModel })} />,
    );

    fireEvent.click(container.querySelector('button[aria-label="Edit Image Pool"]')!);
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
    expect(updates.members).toEqual([{ model_id: 'img-1', weight: 2 }]);
    expect(updates.shadow_model_id).toBe('img-1');
  });

  it('IG6 — the delete-confirm modal routes the delete and the image-gen-lane toast copy fires', async () => {
    const onDeleteModel = vi.fn().mockResolvedValue(undefined);
    const setStatus = vi.fn();
    const groups = [makeGroup({ id: 'g1', name: 'Image Pool' })];
    const { container } = render(
      <GroupModelsTab {...makeProps({ models: groups, onDeleteModel, setStatus })} />,
    );

    fireEvent.click(container.querySelector('button[aria-label="Delete Image Pool"]')!);
    await waitFor(() =>
      expect(container.querySelector('[data-testid="group-delete-modal"]')).not.toBeNull(),
    );

    fireEvent.click(container.querySelector('[data-testid="group-delete-confirm"]')!);
    await waitFor(() => expect(onDeleteModel).toHaveBeenCalledWith('g1'));
    expect(setStatus).toHaveBeenCalledWith({
      type: 'success',
      message: 'Image-gen group deleted successfully',
    });
  });

  it('IG7 — the image-gen lane offers no one-click-wrap pre-fill (the wrap signal is text-lane only)', async () => {
    const wrapTarget = makeModel({ id: 'img-1', name: 'Image One', kind: 'image-gen' });
    const { container } = render(
      <GroupModelsTab {...makeProps({ wrapPreloadModel: wrapTarget })} />,
    );

    // Give the effect a chance to run before asserting.
    await waitFor(() => expect(container.querySelector('[data-testid="group-models-list"]')).not.toBeNull());
    expect(container.querySelector('[data-testid="group-model-form"]')).toBeNull();
    // The list chrome is still the list chrome.
    expect(container.textContent).toContain('Add Image-Gen Group');
  });

  it('IG8 — the image-gen-lane group row carries the image glyph, not the load-balancing glyph', () => {
    const groups = [makeGroup({ id: 'g1', name: 'Image Pool' })];
    const { container } = render(<GroupModelsTab {...makeProps({ models: groups })} />);
    const row = container.querySelector('[data-testid="group-model-row"]') as HTMLElement;
    const glyph = row.querySelector('span[aria-hidden="true"]') as HTMLElement;
    expect(glyph.textContent).toBe('🖼️');
    expect(row.textContent).not.toContain('🔀');
  });
});
