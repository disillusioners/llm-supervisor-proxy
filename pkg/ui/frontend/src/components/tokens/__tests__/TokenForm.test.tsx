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
//   2. The per-token ultimate-model <select> excludes both non-chat
//      kinds.
//
// The fixtures use a multi-kind model set so a single render covers
// every kind in one chrome — same as C-16 / ModelsTab.tsx:157.

interface ModelFixture {
  id: string;
  name: string;
  kind?: 'chat' | 'image-gen' | 'decision';
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

      const models: ModelFixture[] = [chat, chatNoKind, imgGen, decision];

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
    });
  });

  describe('per-token ultimate-model <select>', () => {
    it('P3.8 — per-token ultimate-model <select> excludes image-gen + decision rows', () => {
      // Multi-kind fixture again. The per-token ultimate-model
      // <select> is gated by `ultimateModelEnabled`; we must enable
      // the toggle first to mount the select.
      const chat = makeModel({ id: 'chat-1', name: 'Chat One' });
      const imgGen = makeModel({ id: 'img-1', name: 'Image Gen One', kind: 'image-gen' });
      const decision = makeModel({ id: 'dec-1', name: 'Decision One', kind: 'decision' });

      const models: ModelFixture[] = [chat, imgGen, decision];

      const { container } = render(
        <TokenForm
          models={models}
          onSubmit={vi.fn()}
          onCancel={vi.fn()}
          onStatus={vi.fn()}
        />,
      );

      // Enable the "Ultimate Model Access" toggle. The toggle is a
      // <button> with type="button" inside a flex row; we click the
      // first such button above the ultimate-model select area.
      // Simplest: find the toggle by its visible label text.
      const ultimateToggleLabel = Array.from(container.querySelectorAll('label, button'))
        .find((el) => el.textContent && el.textContent.includes('Ultimate Model Access'));
      expect(ultimateToggleLabel, 'Ultimate Model Access label should be present').toBeDefined();

      // Find the actual toggle button. The label wraps a button —
      // we click the button inside the row labelled "Ultimate Model
      // Access".
      let ultimateBtn: HTMLButtonElement | undefined;
      container.querySelectorAll('div').forEach((div) => {
        const lbl = div.querySelector('label, span');
        if (lbl && lbl.textContent && lbl.textContent.includes('Ultimate Model Access')) {
          ultimateBtn = div.querySelector('button') as HTMLButtonElement;
        }
      });
      expect(ultimateBtn, 'Ultimate Model Access toggle button should be present').toBeDefined();
      fireEvent.click(ultimateBtn!);

      // After enabling the toggle, the per-token ultimate-model
      // <select> mounts. Find ALL <select> elements (the form has no
      // <select> besides this one — the only other form control is
      // a date input + text inputs + buttons).
      const selects = container.querySelectorAll('select');
      expect(selects.length, 'exactly one <select> should render after enabling the toggle').toBe(1);
      const ultimateSelect = selects[0];

      // Verify the option labels: chat-only. The image-gen + decision
      // rows MUST NOT appear in the options.
      const optionTexts = Array.from(ultimateSelect.querySelectorAll('option')).map((o) => o.textContent || '');
      // First option is "Global Default" (empty value).
      expect(optionTexts[0]).toContain('Global Default');
      // Chat row IS present.
      expect(optionTexts.some((t) => t.includes('Chat One'))).toBe(true);
      // Image-gen row is NOT present.
      expect(optionTexts.some((t) => t.includes('Image Gen One'))).toBe(false);
      // Decision row is NOT present.
      expect(optionTexts.some((t) => t.includes('Decision One'))).toBe(false);
    });
  });
});
