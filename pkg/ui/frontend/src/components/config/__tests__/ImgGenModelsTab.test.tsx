import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, act } from '@testing-library/preact';
import { ImgGenModelsTab } from '../ImgGenModelsTab';
import type { ImgGenModel } from '../../../types';

// Stable synthetic image-gen rows. Deterministic so a test diff is meaningful.
function makeImgGenRow(overrides: Partial<ImgGenModel> = {}): ImgGenModel {
  return {
    id: 'imggen-mini-v1',
    name: 'Image Gen MiniMax v1',
    enabled: true,
    fallback_chain: [],
    kind: 'image-gen',
    internal: true,
    internal_provider: 'minimax',
    internal_model: 'image-01',
    internal_base_url: 'https://api.minimax.io/v1',
    credentials: [{ credential_id: 'cred-mini-1', weight: 1, position: 0 }],
    exclude_from_ultimate_switching: true,
    ...overrides,
  };
}

describe('ImgGenModelsTab — list, empty state, delete modal', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
  });

  describe('empty state', () => {
    it('renders the empty-state markup with the exact copy from fe-spec §2.3', () => {
      const { container } = render(
        <ImgGenModelsTab
          models={[]}
          onAddModel={vi.fn()}
          onUpdateModel={vi.fn()}
          onDeleteModel={vi.fn()}
          onToggleModel={vi.fn()}
          setStatus={vi.fn()}
        />,
      );

      // Line 1 + line 2 of the empty state (per spec §2.3 + §5.3)
      expect(container.textContent).toContain('No image-gen models configured');
      expect(container.textContent).toContain('Add your first image-gen model to enable image generation.');

      // Add button carries the right label (per spec §2.3 "Add Image-Gen Model").
      const addBtn = container.querySelector('[data-testid="imggen-empty-state"]');
      expect(addBtn).not.toBeNull();
      // No rows rendered (empty).
      expect(container.querySelectorAll('[data-testid="imggen-model-row"]').length).toBe(0);

      // Header — "Image-Gen Models" is the spec's required title.
      expect(container.textContent).toContain('Image-Gen Models');
      // The Add button text must be present.
      expect(container.textContent).toContain('Add Image-Gen Model');
    });
  });

  describe('populated state', () => {
    it('renders a row per image-gen model with id, name, and the 🖼️ prefix badge', () => {
      const rows = [
        makeImgGenRow({ id: 'a-1', name: 'Alpha' }),
        makeImgGenRow({ id: 'a-2', name: 'Bravo' }),
      ];
      const { container } = render(
        <ImgGenModelsTab
          models={rows}
          onAddModel={vi.fn()}
          onUpdateModel={vi.fn()}
          onDeleteModel={vi.fn()}
          onToggleModel={vi.fn()}
          setStatus={vi.fn()}
        />,
      );

      const rendered = container.querySelectorAll('[data-testid="imggen-model-row"]');
      expect(rendered.length).toBe(2);
      expect(container.textContent).toContain('Alpha');
      expect(container.textContent).toContain('Bravo');
      // The 🖼️ glyph is the spec's prefix on the internal_provider badge.
      expect(container.textContent).toContain('🖼️');
      // Credentials count line is always rendered.
      expect(container.textContent).toContain('CREDENTIALS: 1');
    });

    it('renders the credentials warning copy when a row has zero credentials (defensive state, fe-spec §2.3)', () => {
      const rowNoCreds = makeImgGenRow({ id: 'broke', name: 'Broken', credentials: [] });
      const { container } = render(
        <ImgGenModelsTab
          models={[rowNoCreds]}
          onAddModel={vi.fn()}
          onUpdateModel={vi.fn()}
          onDeleteModel={vi.fn()}
          onToggleModel={vi.fn()}
          setStatus={vi.fn()}
        />,
      );
      expect(container.textContent).toContain('CREDENTIALS: 0');
      expect(container.textContent).toContain('No credentials — model will not function');
    });
  });

  describe('delete confirm modal', () => {
    it('opens with the spec copy "Delete Image-Gen Model" and the model name', () => {
      const target = makeImgGenRow({ name: 'Doomed Model' });
      const { container } = render(
        <ImgGenModelsTab
          models={[target]}
          onAddModel={vi.fn()}
          onUpdateModel={vi.fn()}
          onDeleteModel={vi.fn()}
          onToggleModel={vi.fn()}
          setStatus={vi.fn()}
        />,
      );

      // Click the row's delete button (last button on the row's icon group).
      const row = container.querySelector('[data-testid="imggen-model-row"]')!;
      const deleteBtn = row.querySelector('button[title="Delete image-gen model"]') as HTMLButtonElement;
      expect(deleteBtn).not.toBeNull();
      fireEvent.click(deleteBtn);

      // The modal mounts.
      const modal = container.querySelector('[data-testid="imggen-delete-modal"]');
      expect(modal).not.toBeNull();
      // Title is the spec's binding copy.
      expect(container.textContent).toContain('Delete Image-Gen Model');
      // Body interpolates the model name.
      expect(container.textContent).toContain('"Doomed Model"');
    });

    it('confirm calls onDeleteModel once; cancel does not', () => {
      const target = makeImgGenRow({ name: 'Doomed', id: 'doomed-1' });
      const onDelete = vi.fn().mockResolvedValue(undefined);
      const { container } = render(
        <ImgGenModelsTab
          models={[target]}
          onAddModel={vi.fn()}
          onUpdateModel={vi.fn()}
          onDeleteModel={onDelete}
          onToggleModel={vi.fn()}
          setStatus={vi.fn()}
        />,
      );
      const row = container.querySelector('[data-testid="imggen-model-row"]')!;
      fireEvent.click(row.querySelector('button[title="Delete image-gen model"]') as HTMLButtonElement);

      // Cancel path — modal Cancel button is the gray one next to the red Delete.
      const buttons = container.querySelectorAll('[data-testid="imggen-delete-modal"] button');
      // [Cancel, Delete]
      expect(buttons.length).toBe(2);
      fireEvent.click(buttons[0]);
      // Modal closed; onDelete NOT called.
      expect(onDelete).not.toHaveBeenCalled();
      expect(container.querySelector('[data-testid="imggen-delete-modal"]')).toBeNull();

      // Re-open and confirm.
      fireEvent.click(row.querySelector('button[title="Delete image-gen model"]') as HTMLButtonElement);
      const confirm = container.querySelector('[data-testid="imggen-delete-confirm"]') as HTMLButtonElement;
      expect(confirm).not.toBeNull();
      return act(async () => {
        fireEvent.click(confirm);
        // Wait for the async delete to resolve.
        await Promise.resolve();
      }).then(() => {
        expect(onDelete).toHaveBeenCalledTimes(1);
        expect(onDelete).toHaveBeenCalledWith('doomed-1');
      });
    });
  });

  describe('A11Y (fe-spec §4.8)', () => {
    it('A11Y-3 — the error banner carries role="alert" when fetchError is present', () => {
      // We render the tab with a non-null fetchError; the banner mounts
      // and is queryable. The role attribute is on the banner element
      // (per spec §2.7 and §4.7).
      const { container } = render(
        <ImgGenModelsTab
          models={[]}
          onAddModel={vi.fn()}
          onUpdateModel={vi.fn()}
          onDeleteModel={vi.fn()}
          onToggleModel={vi.fn()}
          setStatus={vi.fn()}
          fetchError="Network down"
          onRetry={vi.fn()}
        />,
      );
      const banner = container.querySelector('[data-testid="imggen-fetch-error"]');
      expect(banner).not.toBeNull();
      expect(banner!.getAttribute('role')).toBe('alert');
      expect(container.textContent).toContain('Failed to load image-gen models.');
      expect(container.textContent).toContain('Network down');
    });
  });

  describe('chat-tab filter integration (defensive — server should pre-filter)', () => {
    it('renders only the rows the parent gives it (no client-side filter; server is the source of truth)', () => {
      // Per spec §2.3 "Hook contract: models: Model[] is the image-gen
      // subset (BE filters server-side)." The tab does not filter itself —
      // we just render what arrives. (C-16 filter lives on ModelsTab.tsx,
      // not here.)
      const img = makeImgGenRow({ id: 'img', name: 'Image' });
      const { container } = render(
        <ImgGenModelsTab
          models={[img]} // pre-filtered to image-gen only by the parent
          onAddModel={vi.fn()}
          onUpdateModel={vi.fn()}
          onDeleteModel={vi.fn()}
          onToggleModel={vi.fn()}
          setStatus={vi.fn()}
        />,
      );
      const rendered = container.querySelectorAll('[data-testid="imggen-model-row"]');
      expect(rendered.length).toBe(1);
      expect(container.textContent).toContain('Image');
    });
  });
});
