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
});
