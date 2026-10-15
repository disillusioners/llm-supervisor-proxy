import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, act } from '@testing-library/preact';
import { DecisionModelsTab } from '../DecisionModelsTab';
import type { DecisionModel } from '../../../types';

// Stable synthetic decision rows (Decision Commission 2026-10-15 /
// plan §6.5). Mirror the ImgGenModelsTab test pattern with the provider
// swapped to one of the Q11 identity-B options.
function makeDecisionRow(overrides: Partial<DecisionModel> = {}): DecisionModel {
  return {
    id: 'decision-router-v1',
    name: 'Decision Router v1',
    enabled: true,
    fallback_chain: [],
    kind: 'decision',
    internal: true,
    internal_provider: 'openai',
    internal_model: 'gpt-6-luna',
    internal_base_url: 'https://api.openai.com',
    credentials: [{ credential_id: 'cred-oai-1', weight: 1, position: 0 }],
    exclude_from_ultimate_switching: true,
    ...overrides,
  };
}

describe('DecisionModelsTab — list, empty state, delete modal (Decision Commission P3.5)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
  });

  describe('empty state', () => {
    it('renders the empty-state markup with the exact copy from plan §6.5', () => {
      const { container } = render(
        <DecisionModelsTab
          models={[]}
          onAddModel={vi.fn()}
          onUpdateModel={vi.fn()}
          onDeleteModel={vi.fn()}
          onToggleModel={vi.fn()}
          setStatus={vi.fn()}
        />,
      );

      expect(container.textContent).toContain('No decision models configured');
      expect(container.textContent).toContain('Add your first decision model to enable decision endpoints.');

      const emptyState = container.querySelector('[data-testid="decision-empty-state"]');
      expect(emptyState).not.toBeNull();
      // No rows rendered (empty).
      expect(container.querySelectorAll('[data-testid="decision-model-row"]').length).toBe(0);

      // Header — "Decision Models" is the spec's required title.
      expect(container.textContent).toContain('Decision Models');
      // The Add button text must be present.
      expect(container.textContent).toContain('Add Decision Model');
    });
  });

  describe('populated state', () => {
    it('renders a row per decision model with id, name, and the 🎯 prefix badge (Q19 default)', () => {
      const rows = [
        makeDecisionRow({ id: 'a-1', name: 'Alpha', internal_provider: 'openai' }),
        makeDecisionRow({ id: 'a-2', name: 'Bravo', internal_provider: 'typesafe' }),
      ];
      const { container } = render(
        <DecisionModelsTab
          models={rows}
          onAddModel={vi.fn()}
          onUpdateModel={vi.fn()}
          onDeleteModel={vi.fn()}
          onToggleModel={vi.fn()}
          setStatus={vi.fn()}
        />,
      );

      const rendered = container.querySelectorAll('[data-testid="decision-model-row"]');
      expect(rendered.length).toBe(2);
      expect(container.textContent).toContain('Alpha');
      expect(container.textContent).toContain('Bravo');
      // The 🎯 glyph is the Q19 default prefix on the
      // internal_provider badge.
      expect(container.textContent).toContain('🎯');
      // Both providers surface in the rendered rows.
      expect(container.textContent).toContain('openai');
      expect(container.textContent).toContain('typesafe');
      // Credentials count line is always rendered.
      expect(container.textContent).toContain('CREDENTIALS: 1');
    });

    it('renders the credentials warning copy when a row has zero credentials (defensive state, plan §6.5)', () => {
      const rowNoCreds = makeDecisionRow({ id: 'broke', name: 'Broken', credentials: [] });
      const { container } = render(
        <DecisionModelsTab
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
    it('opens with the spec copy "Delete Decision Model" and the model name', () => {
      const target = makeDecisionRow({ name: 'Doomed Model' });
      const { container } = render(
        <DecisionModelsTab
          models={[target]}
          onAddModel={vi.fn()}
          onUpdateModel={vi.fn()}
          onDeleteModel={vi.fn()}
          onToggleModel={vi.fn()}
          setStatus={vi.fn()}
        />,
      );

      // Click the row's delete button.
      const row = container.querySelector('[data-testid="decision-model-row"]')!;
      const deleteBtn = row.querySelector('button[title="Delete decision model"]') as HTMLButtonElement;
      expect(deleteBtn).not.toBeNull();
      fireEvent.click(deleteBtn);

      // The modal mounts.
      const modal = container.querySelector('[data-testid="decision-delete-modal"]');
      expect(modal).not.toBeNull();
      // Title is the spec's binding copy.
      expect(container.textContent).toContain('Delete Decision Model');
      // Body interpolates the model name.
      expect(container.textContent).toContain('"Doomed Model"');
    });

    it('confirm calls onDeleteModel once; cancel does not', () => {
      const target = makeDecisionRow({ name: 'Doomed', id: 'doomed-1' });
      const onDelete = vi.fn().mockResolvedValue(undefined);
      const { container } = render(
        <DecisionModelsTab
          models={[target]}
          onAddModel={vi.fn()}
          onUpdateModel={vi.fn()}
          onDeleteModel={onDelete}
          onToggleModel={vi.fn()}
          setStatus={vi.fn()}
        />,
      );
      const row = container.querySelector('[data-testid="decision-model-row"]')!;
      fireEvent.click(row.querySelector('button[title="Delete decision model"]') as HTMLButtonElement);

      // Cancel path — modal Cancel button is the gray one next to the red Delete.
      const buttons = container.querySelectorAll('[data-testid="decision-delete-modal"] button');
      expect(buttons.length).toBe(2);
      fireEvent.click(buttons[0]);
      // Modal closed; onDelete NOT called.
      expect(onDelete).not.toHaveBeenCalled();
      expect(container.querySelector('[data-testid="decision-delete-modal"]')).toBeNull();

      // Re-open and confirm.
      fireEvent.click(row.querySelector('button[title="Delete decision model"]') as HTMLButtonElement);
      const confirm = container.querySelector('[data-testid="decision-delete-confirm"]') as HTMLButtonElement;
      expect(confirm).not.toBeNull();
      return act(async () => {
        fireEvent.click(confirm);
        await Promise.resolve();
      }).then(() => {
        expect(onDelete).toHaveBeenCalledTimes(1);
        expect(onDelete).toHaveBeenCalledWith('doomed-1');
      });
    });
  });

  describe('A11Y (fe-spec §4.8)', () => {
    it('A11Y-3 — the error banner carries role="alert" when fetchError is present', () => {
      const { container } = render(
        <DecisionModelsTab
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
      const banner = container.querySelector('[data-testid="decision-fetch-error"]');
      expect(banner).not.toBeNull();
      expect(banner!.getAttribute('role')).toBe('alert');
      expect(container.textContent).toContain('Failed to load decision models.');
      expect(container.textContent).toContain('Network down');
    });
  });

  describe('server-side pre-filter trust (Decision Commission 2026-10-15 / plan §6.5)', () => {
    it('renders only the rows the parent gives it (no client-side filter; server is the source of truth)', () => {
      // Mirror the ImgGenModelsTab §2.3 contract: `models` is the
      // decision subset already pre-filtered by useDecisionModels.
      // The tab does not filter itself — it just renders what arrives.
      const decision = makeDecisionRow({ id: 'decision', name: 'Decision' });
      const { container } = render(
        <DecisionModelsTab
          models={[decision]}
          onAddModel={vi.fn()}
          onUpdateModel={vi.fn()}
          onDeleteModel={vi.fn()}
          onToggleModel={vi.fn()}
          setStatus={vi.fn()}
        />,
      );
      const rendered = container.querySelectorAll('[data-testid="decision-model-row"]');
      expect(rendered.length).toBe(1);
      expect(container.textContent).toContain('Decision');
    });
  });
});
