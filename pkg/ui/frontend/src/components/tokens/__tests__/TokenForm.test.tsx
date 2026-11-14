import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent } from '@testing-library/preact';
import { TokenForm } from '../TokenForm';

// Decision Commission 2026-10-15 / plan §6.7.a / P3.8 (amendment 7):
// Both pickers in TokenForm must list chat-only models. The filter
// shape (!m.kind || m.kind === 'chat') excludes both image-gen AND
// decision models.
//
// These tests assert that:
//   1. The allowed-models picker (ModelMultiSelect) excludes both
//      image-gen + decision rows.
//
// The fixtures use a multi-kind model set so a single render covers
// every kind in one chrome — same as C-16 / ModelsTab.tsx:157.

interface ModelFixture {
  id: string;
  name: string;
  kind?: 'chat' | 'image-gen' | 'decision' | 'group';
}

function makeModel(overrides: Partial<ModelFixture> = {}): ModelFixture {
  return {
    id: 'm1',
    name: 'M1',
    ...overrides,
  };
}

describe('TokenForm — kind filter for both pickers (Decision Commission 2026-10-15 / plan §6.7.a / P3.8)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  describe('allowed-models picker (ModelMultiSelect)', () => {
    it('P3.8 — allowed-models picker excludes image-gen + decision rows; chat rows surface', () => {
      // Multi-kind fixture to assert both exclusions in one render.
      const chat = makeModel({ id: 'chat-1', name: 'Chat One' });
      const chatNoKind = makeModel({ id: 'chat-2', name: 'Chat Two (no kind)' });
      const imgGen = makeModel({ id: 'img-1', name: 'Image Gen One', kind: 'image-gen' });
      const decision = makeModel({ id: 'dec-1', name: 'Decision One', kind: 'decision' });
      // load-balance-groups (plan §6.4 arm 4 of 4 / P3.7): groups join
      // the exclusion set in BOTH pickers.
      const group = makeModel({ id: 'grp-1', name: 'Group One', kind: 'group' });

      const models: ModelFixture[] = [chat, chatNoKind, imgGen, decision, group];

      const { container } = render(
        <TokenForm
          models={models}
          onSubmit={vi.fn()}
          onCancel={vi.fn()}
          onStatus={vi.fn()}
        />,
      );

      // Open the allowed-models dropdown so the filtered list is
      // visible. Without opening, ModelMultiSelect keeps the items
      // hidden in a closed dropdown — we cannot probe them via the
      // DOM without opening the panel.
      const allowedSelectToggle = container.querySelector('button[aria-haspopup="listbox"], [data-testid="model-multiselect-toggle"]')
        || Array.from(container.querySelectorAll('button, [role="combobox"]'))
          .find((b) => b.textContent && b.textContent.includes('Select models')) as HTMLElement;
      if (allowedSelectToggle) {
        fireEvent.click(allowedSelectToggle);
      }

      // After opening, the chat rows should be present in the
      // dropdown. The image-gen and decision rows must be filtered
      // out per the kind filter (amendment 7 / plan §6.7.a).
      //
      // ModelMultiSelect renders the model name as the option label;
      // the simplest assertion is "name appears in container text"
      // once the dropdown is open.
      expect(container.textContent).toContain('Chat One');
      expect(container.textContent).toContain('Chat Two (no kind)');
      expect(container.textContent).not.toContain('Image Gen One');
      expect(container.textContent).not.toContain('Decision One');
      expect(container.textContent).not.toContain('Group One');
    });
  });

});
