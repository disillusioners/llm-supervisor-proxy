// AUTH COMMISSION — Phase 3 FE (UI layer), SECOND surface.
//
// TokenList's inline editor is the LIVE edit path (SettingsPage renders
// TokenForm without a `token` prop — create-only), so the PATCH semantics and
// the per-axis grant rules reach production THROUGH this surface.
//
// The rules under test:
//   * the "all / wildcard" badge is shown ONLY when BOTH axes are absent
//     (null/null) — a groups-only token must never read as "all models";
//   * the model chips and the group chips are shown SEPARATELY;
//   * a save splits the selection into the two axes and writes both;
//   * an unreadable (INVALID) allow-list renders a DENY-ALL warning.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, waitFor } from '@testing-library/preact';
import { TokenList } from '../TokenList';
import type { ApiToken } from '../../../types';

const MODELS = [
  { id: 'chat-1', name: 'Chat One', kind: 'chat' as const },
  { id: 'chat-2', name: 'Chat Two (no kind)' },
  { id: 'img-1', name: 'Image Gen One', kind: 'image-gen' as const },
  { id: 'dec-1', name: 'Decision One', kind: 'decision' as const },
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

function modeSelect(container: Element, axis: 'models' | 'groups'): HTMLSelectElement {
  const label = axis === 'models' ? 'Models access mode' : 'Groups access mode';
  const el = container.querySelector(`select[aria-label="${label}"]`);
  if (!el) throw new Error(`mode select not found: ${label}`);
  return el as HTMLSelectElement;
}

function openPicker(container: Element, axis: 'models' | 'groups') {
  const label = axis === 'models' ? 'Allowed models' : 'Allowed groups';
  const toggle = container.querySelector(`button[aria-label="${label}"]`);
  if (!toggle) throw new Error(`picker toggle not found: ${label}`);
  fireEvent.click(toggle);
}

function pickRow(container: Element, name: string) {
  const span = Array.from(container.querySelectorAll('li span')).find((s) => s.textContent === name);
  if (!span) throw new Error(`picker row not found: ${name}`);
  const li = span.closest('li');
  const cb = li?.querySelector('input[type="checkbox"]') as HTMLInputElement;
  if (!cb) throw new Error(`picker checkbox not found: ${name}`);
  fireEvent.click(cb);
}

function clickEdit(container: Element) {
  const btn = container.querySelector('button[title="Edit token grants"]');
  if (!btn) throw new Error('edit button not found');
  fireEvent.click(btn);
}

function clickSave(container: Element) {
  const btn = Array.from(container.querySelectorAll('button')).find(
    (b) => b.textContent && b.textContent.trim() === 'Save',
  );
  if (!btn) throw new Error('save button not found');
  fireEvent.click(btn);
}

describe('TokenList — axis-aware grants (AUTH COMMISSION, phase 3 FE)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('TokenList_BadgeShowsWildcardOnlyWhenBothAxesAbsent', async () => {
    // 1. Both axes absent => the wildcard badge.
    const both = renderList([token()]);
    expect(both.container.querySelector('[data-testid="wildcard-badge"]')).not.toBeNull();
    expect(both.container.textContent).toContain('Unrestricted — all models and groups');

    // 2. Models only => NO wildcard badge; the model chips are shown and the
    //    group axis reads as "no allow-list".
    const modelsOnly = renderList([token({ allowed_models: ['chat-1'] })]);
    expect(modelsOnly.container.querySelector('[data-testid="wildcard-badge"]')).toBeNull();
    expect(modelsOnly.container.textContent).not.toContain('Unrestricted — all models and groups');
    expect(modelsOnly.container.textContent).toContain('Chat One');
    expect(modelsOnly.container.textContent).toContain('Allowed groups:');

    // 3. Groups only => NO wildcard badge; the group chips are shown and the
    //    model axis reads as "no allow-list".
    const groupsOnly = renderList([token({ allowed_groups: ['grp-1'] })]);
    expect(groupsOnly.container.querySelector('[data-testid="wildcard-badge"]')).toBeNull();
    expect(groupsOnly.container.textContent).not.toContain('Unrestricted — all models and groups');
    expect(groupsOnly.container.textContent).toContain('Group One');
    expect(groupsOnly.container.textContent).toContain('Allowed models:');
  });

  it('TokenList_SaveSplitsAxesAndRoundTripsBoth', async () => {
    const onUpdatePermission = vi.fn().mockResolvedValue(true);
    const { container } = renderList([token()], onUpdatePermission);

    clickEdit(container);

    // Narrow the model axis and pick a chat row.
    fireEvent.change(modeSelect(container, 'models'), { target: { value: 'restricted' } });
    openPicker(container, 'models');
    pickRow(container, 'Chat One');

    // Narrow the group axis and pick the group row.
    fireEvent.change(modeSelect(container, 'groups'), { target: { value: 'restricted' } });
    openPicker(container, 'groups');
    pickRow(container, 'Group One');

    clickSave(container);

    await waitFor(() => expect(onUpdatePermission).toHaveBeenCalled());
    const args = onUpdatePermission.mock.calls[0];
    expect(args[0]).toBe('tok-1');
    expect(args[1]).toEqual(['chat-1']);
    expect(args[2]).toEqual(['grp-1']);

    // The optimistic local state tracks BOTH axes: the wildcard badge is gone
    // and both chip sets are rendered.
    await waitFor(() =>
      expect(container.querySelector('[data-testid="wildcard-badge"]')).toBeNull(),
    );
    expect(container.textContent).toContain('Chat One');
    expect(container.textContent).toContain('Group One');
  });

  it('TokenList_InvalidAxisRendersDenyAllWarning', async () => {
    const { container } = renderList([
      token({ allowed_models: null, allowed_models_invalid: true }),
    ]);

    expect(container.textContent).toContain('could not be read');
    expect(container.textContent).toContain('denies all models');
    // A deny-all token is never a wildcard.
    expect(container.querySelector('[data-testid="wildcard-badge"]')).toBeNull();
  });

  it('TokenList_GroupChipsAreDistinctFromModelChips', async () => {
    const { container } = renderList([
      token({ allowed_models: ['chat-1'], allowed_groups: ['grp-1'] }),
    ]);

    const modelChips = Array.from(container.querySelectorAll('[data-axis="models"]'));
    const groupChips = Array.from(container.querySelectorAll('[data-axis="groups"]'));

    expect(modelChips.length).toBeGreaterThan(0);
    expect(groupChips.length).toBeGreaterThan(0);
    expect(modelChips.map((c) => c.textContent)).toContain('Chat One');
    expect(modelChips.map((c) => c.textContent)).not.toContain('Group One');
    expect(groupChips.map((c) => c.textContent)).toContain('Group One');
    expect(groupChips.map((c) => c.textContent)).not.toContain('Chat One');
  });
});
