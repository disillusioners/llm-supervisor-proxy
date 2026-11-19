// AUTH COMMISSION — Phase 3 FE (UI layer).
//
// The token grant model is PER-AXIS and the two axes are INDEPENDENT:
//   Axis 1 = MODEL  (allowed_models)
//   Axis 2 = GROUP  (allowed_groups)
//
// Each axis is tri-state on the wire:
//   null => Unrestricted (no allow-list on that axis)
//   []   => Restricted with an empty selection (grants NOTHING)
//   ids  => Restricted with exactly those ids
//
// The old contract ("chat-only picker", "an empty selection means all models
// are allowed") is RETIRED: the model picker now offers every non-group row
// (chat, image-gen, decision, legacy rows with no kind), group rows live in
// their OWN picker, and an empty selection on a restricted axis grants
// NOTHING. The SPLIT into the two axes happens at submit, by row kind.
//
// These cases assert the split on the onSubmit CALLBACK ARGUMENTS (the wire
// payload), never on rendered text, and they render the badge/warning
// behaviour on rendered text.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, waitFor } from '@testing-library/preact';
import { TokenForm } from '../TokenForm';
import type { ApiToken } from '../../../types';

interface ModelFixture {
  id: string;
  name: string;
  kind?: 'chat' | 'image-gen' | 'decision' | 'group';
}

// The multi-kind fixture every case renders: a chat row, a chat row with NO
// kind, an image-gen row, a decision row and a group row.
const CHAT = { id: 'chat-1', name: 'Chat One', kind: 'chat' as const };
const CHAT_NO_KIND = { id: 'chat-2', name: 'Chat Two (no kind)' };
const IMG_GEN = { id: 'img-1', name: 'Image Gen One', kind: 'image-gen' as const };
const DECISION = { id: 'dec-1', name: 'Decision One', kind: 'decision' as const };
const GROUP = { id: 'grp-1', name: 'Group One', kind: 'group' as const };

const MODELS: ModelFixture[] = [CHAT, CHAT_NO_KIND, IMG_GEN, DECISION, GROUP];

function baseToken(overrides: Partial<ApiToken> = {}): ApiToken {
  return {
    id: 'tok-1',
    name: 'Existing Token',
    prefix: 'sk-proxy-***',
    created_at: '2026-11-18T00:00:00Z',
    allowed_models: null,
    ...overrides,
  } as ApiToken;
}

function modeSelect(container: Element, axis: 'models' | 'groups'): HTMLSelectElement {
  const label = axis === 'models' ? 'Models access mode' : 'Groups access mode';
  const el = container.querySelector(`select[aria-label="${label}"]`);
  if (!el) throw new Error(`mode select not found: ${label}`);
  return el as HTMLSelectElement;
}

function setMode(container: Element, axis: 'models' | 'groups', mode: 'unrestricted' | 'restricted') {
  fireEvent.change(modeSelect(container, axis), { target: { value: mode } });
}

function pickerToggle(container: Element, axis: 'models' | 'groups'): HTMLElement {
  const label = axis === 'models' ? 'Allowed models' : 'Allowed groups';
  const el = container.querySelector(`button[aria-label="${label}"]`);
  if (!el) throw new Error(`picker toggle not found: ${label}`);
  return el as HTMLElement;
}

function openPicker(container: Element, axis: 'models' | 'groups') {
  fireEvent.click(pickerToggle(container, axis));
}

function pickRow(container: Element, name: string) {
  const span = Array.from(container.querySelectorAll('li span')).find((s) => s.textContent === name);
  if (!span) throw new Error(`picker row not found: ${name}`);
  const li = span.closest('li');
  if (!li) throw new Error(`picker row <li> not found: ${name}`);
  const cb = li.querySelector('input[type="checkbox"]') as HTMLInputElement;
  if (!cb) throw new Error(`picker checkbox not found: ${name}`);
  fireEvent.click(cb);
}

function fillName(container: Element, value: string) {
  const input = container.querySelector('input[type="text"]') as HTMLInputElement;
  fireEvent.input(input, { target: { value } });
}

function submitButton(container: Element, editing: boolean): HTMLElement {
  const wanted = editing ? 'Save Changes' : 'Create Token';
  const btn = Array.from(container.querySelectorAll('button')).find(
    (b) => b.textContent && b.textContent.includes(wanted),
  );
  if (!btn) throw new Error(`submit button not found: ${wanted}`);
  return btn as HTMLElement;
}

async function submitForm(container: Element, editing: boolean) {
  fireEvent.click(submitButton(container, editing));
  await waitFor(() => expect(container).toBeTruthy());
}

describe('TokenForm — per-axis grant axes (AUTH COMMISSION, phase 3 FE)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('TokenForm_PickerIncludesDecisionAndImageGenModels', async () => {
    const onSubmit = vi.fn();
    const { container } = render(
      <TokenForm models={MODELS} onSubmit={onSubmit} onCancel={vi.fn()} onStatus={vi.fn()} />,
    );

    // The model picker is disabled while the axis is Unrestricted (the
    // selection would be ignored), so the axis is narrowed first.
    setMode(container, 'models', 'restricted');
    openPicker(container, 'models');

    const modelList = container.querySelector('ul[role="listbox"]');
    expect(modelList).not.toBeNull();
    const text = modelList!.textContent ?? '';
    expect(text).toContain('Chat One');
    expect(text).toContain('Chat Two (no kind)');
    expect(text).toContain('Image Gen One');
    expect(text).toContain('Decision One');
  });

  it('TokenForm_PickerExcludesGroupModels', async () => {
    const onSubmit = vi.fn();
    const { container } = render(
      <TokenForm models={MODELS} onSubmit={onSubmit} onCancel={vi.fn()} onStatus={vi.fn()} />,
    );

    setMode(container, 'models', 'restricted');
    openPicker(container, 'models');
    const modelList = container.querySelector('ul[role="listbox"]');
    expect(modelList!.textContent).not.toContain('Group One');

    // Groups are offered in their OWN picker, and only there.
    setMode(container, 'groups', 'restricted');
    openPicker(container, 'groups');
    const lists = Array.from(container.querySelectorAll('ul[role="listbox"]'));
    const groupList = lists[lists.length - 1];
    expect(groupList.textContent).toContain('Group One');
    expect(groupList.textContent).not.toContain('Image Gen One');
    expect(groupList.textContent).not.toContain('Decision One');
  });

  it('TokenForm_GroupPickerEmitsAllowedGroups', async () => {
    const onSubmit = vi.fn();
    const { container } = render(
      <TokenForm models={MODELS} onSubmit={onSubmit} onCancel={vi.fn()} onStatus={vi.fn()} />,
    );

    setMode(container, 'groups', 'restricted');
    openPicker(container, 'groups');
    pickRow(container, 'Group One');
    fillName(container, 'Group Token');
    await submitForm(container, false);

    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    const args = onSubmit.mock.calls[0];
    expect(args[0]).toBe('Group Token');
    expect(args[3]).toEqual(['grp-1']);
  });

  it('TokenForm_GroupIdNeverEmittedUnderAllowedModels', async () => {
    const onSubmit = vi.fn();
    const { container } = render(
      <TokenForm models={MODELS} onSubmit={onSubmit} onCancel={vi.fn()} onStatus={vi.fn()} />,
    );

    setMode(container, 'groups', 'restricted');
    openPicker(container, 'groups');
    pickRow(container, 'Group One');
    fillName(container, 'Group Token');
    await submitForm(container, false);

    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    const args = onSubmit.mock.calls[0];
    expect(args[3]).toContain('grp-1');
    expect(args[2] ?? []).not.toContain('grp-1');
    expect(args[2] ?? []).toEqual([]);
  });

  it('TokenForm_DecisionModelEmitsUnderAllowedModels', async () => {
    const onSubmit = vi.fn();
    const { container } = render(
      <TokenForm models={MODELS} onSubmit={onSubmit} onCancel={vi.fn()} onStatus={vi.fn()} />,
    );

    setMode(container, 'models', 'restricted');
    openPicker(container, 'models');
    pickRow(container, 'Decision One');
    fillName(container, 'Decision Token');
    await submitForm(container, false);

    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    const args = onSubmit.mock.calls[0];
    expect(args[2]).toContain('dec-1');
    expect(args[3]).toBeNull();
  });

  it('TokenForm_UnrestrictedSendsNullAxis', async () => {
    const onSubmit = vi.fn();
    const { container } = render(
      <TokenForm models={MODELS} onSubmit={onSubmit} onCancel={vi.fn()} onStatus={vi.fn()} />,
    );

    setMode(container, 'models', 'unrestricted');
    setMode(container, 'groups', 'unrestricted');
    fillName(container, 'Open Token');
    await submitForm(container, false);

    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    const args = onSubmit.mock.calls[0];
    expect(args[2]).toBeNull();
    expect(args[3]).toBeNull();
  });

  it('TokenForm_RestrictedEmptySendsEmptyArray', async () => {
    const onSubmit = vi.fn();
    const { container } = render(
      <TokenForm models={MODELS} onSubmit={onSubmit} onCancel={vi.fn()} onStatus={vi.fn()} />,
    );

    setMode(container, 'models', 'restricted');
    setMode(container, 'groups', 'restricted');
    fillName(container, 'Locked Token');
    await submitForm(container, false);

    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    const args = onSubmit.mock.calls[0];
    expect(args[2]).toEqual([]);
    expect(args[3]).toEqual([]);
  });

  it('TokenForm_RestrictedSelectionSendsIdArray', async () => {
    const onSubmit = vi.fn();
    const { container } = render(
      <TokenForm models={MODELS} onSubmit={onSubmit} onCancel={vi.fn()} onStatus={vi.fn()} />,
    );

    setMode(container, 'models', 'restricted');
    openPicker(container, 'models');
    pickRow(container, 'Chat One');
    fillName(container, 'Narrow Token');
    await submitForm(container, false);

    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    const args = onSubmit.mock.calls[0];
    expect(args[2]).toEqual(['chat-1']);
    expect(args[3]).toBeNull();
  });

  it('TokenForm_ClearingGroupsNeverSendsNull', async () => {
    const onSubmit = vi.fn();
    const token = baseToken({ allowed_models: null, allowed_groups: ['grp-1'] });
    const { container } = render(
      <TokenForm
        models={MODELS}
        token={token}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
        onStatus={vi.fn()}
      />,
    );

    // The stored group axis is restricted (an array, not null).
    expect(modeSelect(container, 'groups').value).toBe('restricted');

    // The operator clears the group selection.
    openPicker(container, 'groups');
    pickRow(container, 'Group One');
    await submitForm(container, true);

    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    const args = onSubmit.mock.calls[0];
    expect(args[3]).toEqual([]);
    expect(args[3]).not.toBeNull();
    expect(args[2]).toBeNull();
  });

  it('TokenForm_EditModeCannotPromoteAxisToUnrestricted', async () => {
    const onSubmit = vi.fn();
    const token = baseToken({ allowed_models: ['chat-1'], allowed_groups: null });
    const { container } = render(
      <TokenForm
        models={MODELS}
        token={token}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
        onStatus={vi.fn()}
      />,
    );

    // The Unrestricted option is DISABLED on an existing restricted axis —
    // promotion is inexpressible on PATCH (omitted == null == KEEP).
    const unrestrictedOption = modeSelect(container, 'models').querySelector(
      'option[value="unrestricted"]',
    ) as HTMLOptionElement;
    expect(unrestrictedOption).not.toBeNull();
    expect(unrestrictedOption.disabled).toBe(true);
    expect(modeSelect(container, 'models').value).toBe('restricted');

    // The create-only note says so in plain words.
    expect(container.textContent).toContain(
      'Unrestricted cannot be selected on an existing token',
    );

    // Saving without touching the axis keeps it restricted.
    await submitForm(container, true);
    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    const args = onSubmit.mock.calls[0];
    expect(args[2]).toEqual(['chat-1']);
    expect(args[2]).not.toBeNull();
  });

  it('TokenForm_InvalidAxisRendersDenyAllWarning', async () => {
    const onSubmit = vi.fn();
    const token = baseToken({
      allowed_models: null,
      allowed_groups: null,
      allowed_models_invalid: true,
    });
    const { container } = render(
      <TokenForm
        models={MODELS}
        token={token}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
        onStatus={vi.fn()}
      />,
    );

    expect(container.textContent).toContain('could not be read');
    expect(container.textContent).toContain('denies all models');
    // The deny-all axis is NEVER rendered as unrestricted/wildcard.
    expect(modeSelect(container, 'models').value).toBe('restricted');
    const unrestrictedOption = modeSelect(container, 'models').querySelector(
      'option[value="unrestricted"]',
    ) as HTMLOptionElement;
    expect(unrestrictedOption.disabled).toBe(true);
  });

  it('TokenForm_AxisWithoutInvalidFlagIsNotReportedInvalid', async () => {
    const onSubmit = vi.fn();
    const token = baseToken({ allowed_models: ['chat-1'], allowed_groups: ['grp-1'] });
    const { container } = render(
      <TokenForm
        models={MODELS}
        token={token}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
        onStatus={vi.fn()}
      />,
    );

    // A missing flag means "no invalid state reported" — nothing is inferred.
    expect(container.textContent).not.toContain('could not be read');
    expect(container.textContent).not.toContain('Deny-all');
    expect(container.textContent).not.toContain('denies all models');
    expect(container.textContent).not.toContain('denies all groups');
  });

  it('TokenForm_EditModeKeepsStaleGroupIdOnGroupsAxis', async () => {
    const onSubmit = vi.fn();
    // 'grp-gone' was deleted: its row is gone from `models`, but the grant is
    // still stored on the GROUP axis and must stay there.
    const token = baseToken({ allowed_models: null, allowed_groups: ['grp-gone'] });
    const { container } = render(
      <TokenForm
        models={MODELS}
        token={token}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
        onStatus={vi.fn()}
      />,
    );

    await submitForm(container, true);
    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    const args = onSubmit.mock.calls[0];
    expect(args[3]).toEqual(['grp-gone']);
    expect(args[2] ?? []).not.toContain('grp-gone');
    expect(args[2] ?? []).toEqual([]);
  });
});
