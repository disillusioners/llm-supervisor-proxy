import { describe, it, expect, beforeEach, afterEach, vi, type Mock } from 'vitest';
import { render, cleanup, fireEvent, waitFor } from '@testing-library/preact';
import { DecisionModelForm } from '../DecisionModelForm';
import type { DecisionModel, Credential } from '../../../types';

// Stub the credentials endpoint so the form can mount without a real
// network. The form calls getCredentials() on mount to populate the
// MultiCredentialEditor's dropdown.
//
// We stub TWO providers here so the per-provider credentials filter
// (Decision Commission 2026-10-15 / plan §6.2) and the
// provider-mismatch validator (mirroring ImgGen C-13) have a chance
// to render the right state.
const openaiCreds: Credential[] = [
  { id: 'cred-oai-1', provider: 'openai' },
  { id: 'cred-oai-2', provider: 'openai' },
];
const typesafeCreds: Credential[] = [
  { id: 'cred-ts-1', provider: 'typesafe' },
];

vi.mock('../../../hooks/useApi', async (importOriginal) => {
  const real = await importOriginal<typeof import('../../../hooks/useApi')>();
  return {
    ...real,
    getCredentials: vi.fn(async () => [...openaiCreds, ...typesafeCreds]),
  };
});

// Wait for the form to finish its async credentials fetch so the
// MultiCredentialEditor's dropdown is populated. The MultiCredentialEditor
// renders "Loading credentials..." while credentials are still being
// fetched, then shows "+ Add credential" once they arrive. We probe on
// the add-credential button (cheap, doesn't conflict with the
// provider <select>) — that's the ImgGen test's implicit contract.
async function waitForCredsLoaded() {
  await waitFor(() => {
    const addCredBtn = Array.from(document.querySelectorAll('button')).find(
      (b) => b.textContent && b.textContent.trim() === '+ Add credential',
    );
    // The button only renders when the credentials fetch has
    // resolved (loading === false). Until then, `addCredBtn` is
    // undefined and we throw to keep waiting.
    if (!addCredBtn) {
      throw new Error('credentials not yet loaded');
    }
  }, { timeout: 5000 });
}

describe('DecisionModelForm — payload, validation, per-provider base URL (Decision Commission P3.5)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  describe('per-provider default base URL stamping (plan §6.2 / Q11 + Q17)', () => {
    // Plan §6.2 row "Stamp internal_base_url per the chosen provider":
    // - openai -> https://api.openai.com
    // - typesafe -> https://api.typesafe.ai
    //
    // This pair locks the contract — override A would adjust, but
    // identity B (recommended / approved) is the default.

    it('openai provider -> internal_base_url = https://api.openai.com on submit', async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      const { container } = render(
        <DecisionModelForm
          mode="add"
          onSave={onSave}
          onCancel={vi.fn()}
          onStatus={vi.fn()}
        />,
      );
      await waitForCredsLoaded();

      fireEvent.input(container.querySelector('[data-testid="decision-form-id-input"]')!, {
        target: { value: 'openai-router-1' },
      });
      fireEvent.input(container.querySelector('[data-testid="decision-form-name-input"]')!, {
        target: { value: 'OpenAI Decision Router' },
      });
      // Provider select defaults to 'openai'; leave it.
      const providerSelect = container.querySelector(
        '[data-testid="decision-form-provider-select"]',
      ) as HTMLSelectElement;
      expect(providerSelect.value).toBe('openai');
      fireEvent.input(container.querySelector('[data-testid="decision-form-internal-model-input"]')!, {
        target: { value: 'gpt-6-luna' },
      });

      // Add one credential (filtered to provider=openai at the form level).
      const addCredBtn = Array.from(container.querySelectorAll('button')).find(
        (b) => b.textContent && b.textContent.trim() === '+ Add credential',
      ) as HTMLButtonElement;
      expect(addCredBtn, '+ Add credential button should be present').not.toBeNull();
      fireEvent.click(addCredBtn);

      // Two selects are now rendered: provider (existing) + the primary
      // credential dropdown. The cred dropdown is the LAST <select>.
      const selects = Array.from(container.querySelectorAll('select')) as HTMLSelectElement[];
      const credSelect = selects[selects.length - 1];
      expect(credSelect).not.toBeNull();
      fireEvent.change(credSelect, { target: { value: 'cred-oai-1' } });

      const submit = container.querySelector('[data-testid="decision-form-submit"]') as HTMLButtonElement;
      fireEvent.click(submit);

      await waitFor(() => {
        expect(onSave).toHaveBeenCalledTimes(1);
      });
      const arg = (onSave as Mock).mock.calls[0][0];
      expect(arg).toMatchObject({
        id: 'openai-router-1',
        name: 'OpenAI Decision Router',
        kind: 'decision',
        internal: true,
        internal_provider: 'openai',
        internal_model: 'gpt-6-luna',
        internal_base_url: 'https://api.openai.com',
        enabled: true,
        exclude_from_ultimate_switching: true,
      });
      expect(arg.credentials).toEqual([
        { credential_id: 'cred-oai-1', weight: 1, position: 0 },
      ]);
    });

    it('typesafe provider -> internal_base_url = https://api.typesafe.ai on submit', async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      const { container } = render(
        <DecisionModelForm
          mode="add"
          onSave={onSave}
          onCancel={vi.fn()}
          onStatus={vi.fn()}
        />,
      );
      await waitForCredsLoaded();

      fireEvent.input(container.querySelector('[data-testid="decision-form-id-input"]')!, {
        target: { value: 'typesafe-router-1' },
      });
      fireEvent.input(container.querySelector('[data-testid="decision-form-name-input"]')!, {
        target: { value: 'TypeSafe Decision Router' },
      });

      // Switch provider to 'typesafe'.
      const providerSelect = container.querySelector(
        '[data-testid="decision-form-provider-select"]',
      ) as HTMLSelectElement;
      fireEvent.change(providerSelect, { target: { value: 'typesafe' } });

      // Base URL default should now reflect the selected provider.
      const baseUrlInput = container.querySelector(
        '[data-testid="decision-form-base-url-input"]',
      ) as HTMLInputElement;
      expect(baseUrlInput.value).toBe('https://api.typesafe.ai');

      fireEvent.input(container.querySelector('[data-testid="decision-form-internal-model-input"]')!, {
        target: { value: 'jev-1.13.0' },
      });

      // Add one typesafe credential.
      const addCredBtn = Array.from(container.querySelectorAll('button')).find(
        (b) => b.textContent && b.textContent.trim() === '+ Add credential',
      ) as HTMLButtonElement;
      fireEvent.click(addCredBtn);

      const selects = Array.from(container.querySelectorAll('select')) as HTMLSelectElement[];
      const credSelect = selects[selects.length - 1];
      fireEvent.change(credSelect, { target: { value: 'cred-ts-1' } });

      const submit = container.querySelector('[data-testid="decision-form-submit"]') as HTMLButtonElement;
      fireEvent.click(submit);

      await waitFor(() => {
        expect(onSave).toHaveBeenCalledTimes(1);
      });
      const arg = (onSave as Mock).mock.calls[0][0];
      expect(arg).toMatchObject({
        id: 'typesafe-router-1',
        name: 'TypeSafe Decision Router',
        kind: 'decision',
        internal: true,
        internal_provider: 'typesafe',
        internal_model: 'jev-1.13.0',
        internal_base_url: 'https://api.typesafe.ai',
        enabled: true,
        exclude_from_ultimate_switching: true,
      });
      expect(arg.credentials).toEqual([
        { credential_id: 'cred-ts-1', weight: 1, position: 0 },
      ]);
    });
  });

  describe('provider dropdown filters (Q11 identity B)', () => {
    it('provider dropdown lists exactly openai + typesafe (Q11 recommended identity)', () => {
      const { container } = render(
        <DecisionModelForm
          mode="add"
          onSave={vi.fn()}
          onCancel={vi.fn()}
          onStatus={vi.fn()}
        />,
      );
      const providerSelect = container.querySelector(
        '[data-testid="decision-form-provider-select"]',
      ) as HTMLSelectElement;
      expect(providerSelect).not.toBeNull();
      const opts = Array.from(providerSelect.querySelectorAll('option')).map((o) => o.value);
      expect(opts).toEqual(['openai', 'typesafe']);
    });
  });

  describe('client-side validation (mirroring ImgGen C-10..C-13)', () => {
    it('empty name blocks submit and surfaces "Display Name is required"', async () => {
      const onSave = vi.fn();
      const { container } = render(
        <DecisionModelForm
          mode="add"
          onSave={onSave}
          onCancel={vi.fn()}
          onStatus={vi.fn()}
        />,
      );
      await waitForCredsLoaded();

      const submit = container.querySelector('[data-testid="decision-form-submit"]') as HTMLButtonElement;
      fireEvent.click(submit);

      await waitFor(() => {
        expect(container.textContent).toContain('Display Name is required');
      });
      expect(onSave).not.toHaveBeenCalled();
    });

    it('empty internal_model blocks submit and surfaces "Upstream Model is required"', async () => {
      const onSave = vi.fn();
      const { container } = render(
        <DecisionModelForm
          mode="add"
          onSave={onSave}
          onCancel={vi.fn()}
          onStatus={vi.fn()}
        />,
      );
      await waitForCredsLoaded();

      fireEvent.input(container.querySelector('[data-testid="decision-form-id-input"]')!, {
        target: { value: 'test-id' },
      });
      fireEvent.input(container.querySelector('[data-testid="decision-form-name-input"]')!, {
        target: { value: 'My Test Model' },
      });
      const submit = container.querySelector('[data-testid="decision-form-submit"]') as HTMLButtonElement;
      fireEvent.click(submit);

      await waitFor(() => {
        expect(container.textContent).toContain('Upstream Model is required');
      });
      expect(onSave).not.toHaveBeenCalled();
    });

    it('empty credentials array blocks submit and surfaces "At least one credential is required"', async () => {
      const onSave = vi.fn();
      const { container } = render(
        <DecisionModelForm
          mode="add"
          onSave={onSave}
          onCancel={vi.fn()}
          onStatus={vi.fn()}
        />,
      );
      await waitForCredsLoaded();

      fireEvent.input(container.querySelector('[data-testid="decision-form-id-input"]')!, {
        target: { value: 'test-id' },
      });
      fireEvent.input(container.querySelector('[data-testid="decision-form-name-input"]')!, {
        target: { value: 'Test' },
      });
      fireEvent.input(container.querySelector('[data-testid="decision-form-internal-model-input"]')!, {
        target: { value: 'gpt-6-luna' },
      });
      const submit = container.querySelector('[data-testid="decision-form-submit"]') as HTMLButtonElement;
      fireEvent.click(submit);

      await waitFor(() => {
        expect(container.textContent).toContain('At least one credential is required');
      });
      expect(onSave).not.toHaveBeenCalled();
    });
  });

  describe('edit mode (mirrors ImgGen C-15)', () => {
    it('id input is NOT rendered in edit mode (the form has no editable id field when editing)', () => {
      const initial: DecisionModel = {
        id: 'fixed-id',
        name: 'Existing',
        enabled: true,
        fallback_chain: [],
        kind: 'decision',
        internal: true,
        internal_provider: 'openai',
        internal_model: 'gpt-6-luna',
        internal_base_url: 'https://api.openai.com',
        credentials: [],
        exclude_from_ultimate_switching: true,
      };
      const { container } = render(
        <DecisionModelForm
          mode="edit"
          initialData={initial}
          onSave={vi.fn()}
          onCancel={vi.fn()}
          onStatus={vi.fn()}
        />,
      );
      // id field is only rendered in add mode (FE-spec §2.4 row 2).
      expect(container.querySelector('[data-testid="decision-form-id-input"]')).toBeNull();
      expect(container.textContent).toContain('Edit Decision Model');
    });
  });

  describe('A11Y (fe-spec §4.8)', () => {
    it('A11Y-1 — every form input/select has an associated <label>', () => {
      const { container } = render(
        <DecisionModelForm
          mode="add"
          onSave={vi.fn()}
          onCancel={vi.fn()}
          onStatus={vi.fn()}
        />,
      );
      const inputs = container.querySelectorAll('input, select');
      inputs.forEach((inp) => {
        const id = (inp as HTMLInputElement).id;
        if (!id) {
          // The enabled toggle is a button — exempt.
          return;
        }
        const label = container.querySelector(`label[for="${id}"]`);
        expect(label, `input id=${id} should have an associated label`).not.toBeNull();
      });
    });
  });
});
