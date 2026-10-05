import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup } from '@testing-library/preact';
import { ModelsTab } from '../ModelsTab';
import type { Model } from '../../../types';

function makeModel(overrides: Partial<Model> = {}): Model {
  return {
    id: 'm1',
    name: 'M1',
    enabled: true,
    fallback_chain: [],
    ...overrides,
  };
}

describe('ModelsTab — chat-tab filter for image-gen rows (C-16, T2.4.5, FE-15)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
  });

  it('C-16 — image-gen rows are filtered out; chat rows render', () => {
    const chat = makeModel({ id: 'chat', name: 'Chat A' });
    const img = makeModel({ id: 'image', name: 'Image B', kind: 'image-gen' });
    const { container } = render(
      <ModelsTab
        models={[chat, img]}
        onAddModel={vi.fn()}
        onUpdateModel={vi.fn()}
        onDeleteModel={vi.fn()}
        onToggleModel={vi.fn()}
        status={null}
        setStatus={vi.fn()}
      />,
    );
    // Chat row is rendered.
    expect(container.textContent).toContain('Chat A');
    // Image-gen row is filtered out (C-16 / T2.4.5).
    expect(container.textContent).not.toContain('Image B');
  });

  it('FE-15 (optional) — rows without a kind field are NOT filtered (degraded behavior)', () => {
    // Per spec §2.6 "R1 mitigation": if kind is undefined, the filter
    // returns true for the row (undefined !== 'image-gen' is true). This
    // is the correct degraded behavior when the BE doesn't yet expose
    // `kind` on the wire.
    const a = makeModel({ id: 'a', name: 'A' });
    const b = makeModel({ id: 'b', name: 'B' });
    const { container } = render(
      <ModelsTab
        models={[a, b]}
        onAddModel={vi.fn()}
        onUpdateModel={vi.fn()}
        onDeleteModel={vi.fn()}
        onToggleModel={vi.fn()}
        status={null}
        setStatus={vi.fn()}
      />,
    );
    expect(container.textContent).toContain('A');
    expect(container.textContent).toContain('B');
  });

  it('review finding #2 — config with ONLY image-gen rows renders the "No models configured" empty state', () => {
    // Regression for the leader-approved FE review finding #2: the
    // chat-tab filter (`kind !== 'image-gen'`) sat INSIDE the non-empty
    // branch of the empty-state ternary. A config containing only
    // image-gen rows (e.g. an operator who only uses image-gen, or a
    // fresh config before any chat models are added) would have
    // models.length > 0, take the non-empty branch, and render an
    // invisible filtered-to-empty list — never surfacing the
    // "No models configured" empty state to the user.
    //
    // The fix hoists the filter to `chatModels` BEFORE the ternary and
    // branches on chatModels.length, so the empty state renders whenever
    // the chat-tab-filtered list is empty (whether `models` itself is
    // empty OR all of its rows are image-gen).
    const imgOnly = makeModel({ id: 'image', name: 'Image B', kind: 'image-gen' });
    const { container } = render(
      <ModelsTab
        models={[imgOnly]}
        onAddModel={vi.fn()}
        onUpdateModel={vi.fn()}
        onDeleteModel={vi.fn()}
        onToggleModel={vi.fn()}
        status={null}
        setStatus={vi.fn()}
      />,
    );
    // The image-gen row is filtered out (C-16 / T2.4.5), AND the
    // empty-state now renders because chatModels.length === 0.
    expect(container.textContent).not.toContain('Image B');
    expect(container.textContent).toContain('No models configured');
  });
});
