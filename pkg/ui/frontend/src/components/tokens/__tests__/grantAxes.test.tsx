import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, waitFor } from '@testing-library/preact';
import { axisOfId, splitByAxis } from '../grantAxes';
import { TokenList } from '../TokenList';
import { TokenForm } from '../TokenForm';
import type { ApiToken } from '../../../types';

const MODELS = [
  { id: 'chat-1', name: 'Chat One', kind: 'chat' as const },
  { id: 'grp-1', name: 'Group One', kind: 'group' as const },
];

function token(overrides: Partial<ApiToken> = {}): ApiToken {
  return {
    id: 'tok-1',
    name: 'Existing Token',
    prefix: 'sk-proxy-***',
    created_at: '2026-11-18T00:00:00Z',
    allowed_models: null,
    allowed_groups: null,
    ...overrides,
  } as ApiToken;
}

describe('grantAxes — axisOfId stored-membership-first (C3)', () => {
  it('axisOfId: target-only group id stored in allowed_models stays on models axis', () => {
    const t = { allowed_models: ['grp-1'], allowed_groups: null as string[] | null };
    expect(axisOfId('grp-1', MODELS, t)).toBe('models');
    const split = splitByAxis(['grp-1'], MODELS, t);
    expect(split.modelIds).toEqual(['grp-1']);
    expect(split.groupIds).toEqual([]);
  });

  it('axisOfId: stored group id stays on groups axis', () => {
    const t = { allowed_models: null as string[] | null, allowed_groups: ['grp-1'] };
    expect(axisOfId('grp-1', MODELS, t)).toBe('groups');
  });

  it('axisOfId: a NEW group id (not stored) classifies by kind onto groups axis', () => {
    const t = { allowed_models: null as string[] | null, allowed_groups: null as string[] | null };
    expect(axisOfId('grp-1', MODELS, t)).toBe('groups');
  });
});

describe('TokenList — target-only group id survives no-change save (C3 a/b)', () => {
  beforeEach(() => { document.body.innerHTML = ''; });
  afterEach(() => { cleanup(); vi.clearAllMocks(); });

  it('TokenList_TargetOnlyGroupId_SurvivesNoChangeSave', async () => {
    const onUpdatePermission = vi.fn().mockResolvedValue(true);
    const { container } = render(
      <TokenList
        tokens={[token({ allowed_models: ['grp-1'], allowed_groups: null })]}
        models={MODELS}
        onRevoke={vi.fn()}
        onStatus={vi.fn()}
        onCreateToken={vi.fn()}
        onUpdatePermission={onUpdatePermission}
        onRefetchTokens={vi.fn()}
      />,
    );

    const editBtn = container.querySelector('button[title="Edit token grants"]') as HTMLElement;
    fireEvent.click(editBtn);
    const saveBtn = Array.from(container.querySelectorAll('button')).find((b) => b.textContent?.trim() === 'Save') as HTMLElement;
    fireEvent.click(saveBtn);

    await waitFor(() => expect(onUpdatePermission).toHaveBeenCalled());
    const args = onUpdatePermission.mock.calls[0];
    expect(args[0]).toBe('tok-1');
    expect(args[1]).toEqual(['grp-1']);
    // groups axis unrestricted => null (KEEP), not revoked/escalated
    expect(args[2]).toBeNull();
  });
});

describe('TokenForm — new group selection lands on groups axis (C3 c)', () => {
  beforeEach(() => { document.body.innerHTML = ''; });
  afterEach(() => { cleanup(); vi.clearAllMocks(); });

  function modeSelect(container: Element, axis: 'models' | 'groups'): HTMLSelectElement {
    const label = axis === 'models' ? 'Models access mode' : 'Groups access mode';
    return container.querySelector(`select[aria-label="${label}"]`) as HTMLSelectElement;
  }
  function openPicker(container: Element, axis: 'models' | 'groups') {
    const label = axis === 'models' ? 'Allowed models' : 'Allowed groups';
    fireEvent.click(container.querySelector(`button[aria-label="${label}"]`) as HTMLElement);
  }
  function pickRow(container: Element, name: string) {
    const span = Array.from(container.querySelectorAll('li span')).find((s) => s.textContent === name);
    const li = span?.closest('li');
    const cb = li?.querySelector('input[type="checkbox"]') as HTMLInputElement;
    fireEvent.click(cb);
  }

  it('TokenForm_NewGroupSelection_LandsOnGroupsAxis', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const { container } = render(
      <TokenForm
        models={MODELS}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
        onStatus={vi.fn()}
      />,
    );

    // name required
    const nameInput = container.querySelector('input[type="text"]') as HTMLInputElement;
    fireEvent.input(nameInput, { target: { value: 't' } });

    // restrict groups axis and pick the group row
    fireEvent.change(modeSelect(container, 'groups'), { target: { value: 'restricted' } });
    openPicker(container, 'groups');
    pickRow(container, 'Group One');

    const saveBtn = Array.from(container.querySelectorAll('button')).find((b) => b.textContent?.trim() === 'Create Token') as HTMLElement;
    fireEvent.click(saveBtn);

    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    const args = onSubmit.mock.calls[0];
    // args: name, expiresAt, allowedModels, allowedGroups
    expect(args[2]).toBeNull(); // models unrestricted
    expect(args[3]).toEqual(['grp-1']); // new group lands on groups axis
  });
});

describe('TokenList — both-axes / cross-axis save payloads (F1)', () => {
  beforeEach(() => { document.body.innerHTML = ''; });
  afterEach(() => { cleanup(); vi.clearAllMocks(); });

  function renderList(tokens: ApiToken[], onUpdatePermission = vi.fn()) {
    return {
      onUpdatePermission,
      ...render(
        <TokenList
          tokens={tokens}
          models={MODELS}
          onRevoke={vi.fn()}
          onStatus={vi.fn()}
          onCreateToken={vi.fn()}
          onUpdatePermission={onUpdatePermission}
          onRefetchTokens={vi.fn()}
        />,
      ),
    };
  }

  function clickEdit(container: Element) {
    fireEvent.click(container.querySelector('button[title="Edit token grants"]') as HTMLElement);
  }

  function clickSave(container: Element) {
    const btn = Array.from(container.querySelectorAll('button')).find((b) => b.textContent?.trim() === 'Save') as HTMLElement;
    fireEvent.click(btn);
  }

  function modeSelect(container: Element, axis: 'models' | 'groups'): HTMLSelectElement {
    const label = axis === 'models' ? 'Models access mode' : 'Groups access mode';
    return container.querySelector(`select[aria-label="${label}"]`) as HTMLSelectElement;
  }

  function openPicker(container: Element, axis: 'models' | 'groups') {
    const label = axis === 'models' ? 'Allowed models' : 'Allowed groups';
    fireEvent.click(container.querySelector(`button[aria-label="${label}"]`) as HTMLElement);
  }

  function pickRow(container: Element, name: string) {
    const span = Array.from(container.querySelectorAll('li span')).find((s) => s.textContent === name);
    const li = span?.closest('li');
    const cb = li?.querySelector('input[type="checkbox"]') as HTMLInputElement;
    fireEvent.click(cb);
  }

  it('TokenList_BothAxes_NoChangeSave_KeepsIdOnBothAxes', async () => {
    const onUpdatePermission = vi.fn().mockResolvedValue(true);
    const { container } = renderList(
      [token({ allowed_models: ['grp-1'], allowed_groups: ['grp-1'] })],
      onUpdatePermission,
    );
    clickEdit(container);
    clickSave(container);
    await waitFor(() => expect(onUpdatePermission).toHaveBeenCalled());
    const [id, models, groups] = onUpdatePermission.mock.calls[0];
    expect(id).toBe('tok-1');
    expect(models).toEqual(['grp-1']);
    expect(groups).toEqual(['grp-1']);
    // (f) no duplicates on any axis
    expect(new Set(models).size).toBe(models.length);
    expect(new Set(groups).size).toBe(groups.length);
  });

  it('TokenList_CrossAxisAddition_IsPreserved', async () => {
    const onUpdatePermission = vi.fn().mockResolvedValue(true);
    const { container } = renderList(
      [token({ allowed_models: ['grp-1'], allowed_groups: null })],
      onUpdatePermission,
    );
    clickEdit(container);
    fireEvent.change(modeSelect(container, 'groups'), { target: { value: 'restricted' } });
    openPicker(container, 'groups');
    pickRow(container, 'Group One');
    clickSave(container);
    await waitFor(() => expect(onUpdatePermission).toHaveBeenCalled());
    const [, models, groups] = onUpdatePermission.mock.calls[0];
    expect(models).toEqual(['grp-1']); // models copy untouched
    expect(groups).toEqual(['grp-1']); // explicit groups-axis addition preserved
  });

  it('TokenList_DuplicateStoredIds_AreDedupedPerAxis', async () => {
    const onUpdatePermission = vi.fn().mockResolvedValue(true);
    const { container } = renderList(
      [token({ allowed_models: ['grp-1', 'grp-1'], allowed_groups: ['grp-1', 'grp-1'] })],
      onUpdatePermission,
    );
    clickEdit(container);
    clickSave(container);
    await waitFor(() => expect(onUpdatePermission).toHaveBeenCalled());
    const [, models, groups] = onUpdatePermission.mock.calls[0];
    expect(models).toEqual(['grp-1']);
    expect(groups).toEqual(['grp-1']);
  });
});
