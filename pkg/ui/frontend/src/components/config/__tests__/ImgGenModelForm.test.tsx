import { describe, it, expect, beforeEach, afterEach, vi, type Mock } from 'vitest';
import { render, cleanup, fireEvent, waitFor } from '@testing-library/preact';
import { ImgGenModelForm } from '../ImgGenModelForm';
import type { ImgGenModel, Credential } from '../../../types';

// Stub the credentials endpoint so the form can mount without a real
// network. The form calls getCredentials() on mount to populate the
// MultiCredentialEditor's dropdown.
const minimaxCreds: Credential[] = [
  { id: 'cred-mini-1', provider: 'minimax' },
  { id: 'cred-mini-2', provider: 'minimax' },
];
const openaiCreds: Credential[] = [
  { id: 'cred-oai-1', provider: 'openai' },
];

vi.mock('../../../hooks/useApi', async (importOriginal) => {
  // Import the real module so we don't break the rest of the surface;
  // we only stub the named export we care about.
  const real = await importOriginal<typeof import('../../../hooks/useApi')>();
  return {
    ...real,
    getCredentials: vi.fn(async () => [...minimaxCreds, ...openaiCreds]),
  };
});

// Wait for the form to finish its async credentials fetch so the
// MultiCredentialEditor's dropdown is populated. Without this, picking
// a credential on the dropdown would race the fetch and fail.
async function waitForCredsLoaded() {
  await waitFor(() => {
    // The MultiCredentialEditor renders a select for row 0 once creds arrive.
    // We probe by looking for the minimax options; they are absent in the
    // initial render (no creds yet) and present after the fetch resolves.
    const sel = document.querySelector('select');
    if (!sel) return;
    const opts = Array.from(sel.querySelectorAll('option'));
    if (!opts.some((o) => o.textContent && o.textContent.includes('cred-mini-1'))) {
      throw new Error('credentials not yet loaded');
    }
  });
}

describe('ImgGenModelForm — payload, validation, and edit-mode shape', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  describe('client-side validation (C-10..C-13)', () => {
    it('C-10 — empty name blocks submit and surfaces "Display Name is required"', async () => {
      const onSave = vi.fn();
      const { container } = render(
        <ImgGenModelForm
          mode="add"
          onSave={onSave}
          onCancel={vi.fn()}
          onStatus={vi.fn()}
        />,
      );
      await waitForCredsLoaded();

      // Click submit without filling any field.
      const submit = container.querySelector('[data-testid="imggen-form-submit"]') as HTMLButtonElement;
      fireEvent.click(submit);

      await waitFor(() => {
        expect(container.textContent).toContain('Display Name is required');
      });
      expect(onSave).not.toHaveBeenCalled();
    });

    it('C-11 — empty internal_model blocks submit and surfaces "Upstream Model is required"', async () => {
      const onSave = vi.fn();
      const { container } = render(
        <ImgGenModelForm
          mode="add"
          onSave={onSave}
          onCancel={vi.fn()}
          onStatus={vi.fn()}
        />,
      );
      await waitForCredsLoaded();

      // Fill name + id but leave internal_model empty.
      fireEvent.input(container.querySelector('[data-testid="imggen-form-id-input"]')!, {
        target: { value: 'test-id' },
      });
      fireEvent.input(container.querySelector('[data-testid="imggen-form-name-input"]')!, {
        target: { value: 'My Test Model' },
      });
      const submit = container.querySelector('[data-testid="imggen-form-submit"]') as HTMLButtonElement;
      fireEvent.click(submit);

      await waitFor(() => {
        expect(container.textContent).toContain('Upstream Model is required');
      });
      expect(onSave).not.toHaveBeenCalled();
    });

    it('C-12 — empty credentials array blocks submit and surfaces "At least one credential is required"', async () => {
      const onSave = vi.fn();
      const { container } = render(
        <ImgGenModelForm
          mode="add"
          onSave={onSave}
          onCancel={vi.fn()}
          onStatus={vi.fn()}
        />,
      );
      await waitForCredsLoaded();

      fireEvent.input(container.querySelector('[data-testid="imggen-form-id-input"]')!, {
        target: { value: 'test-id' },
      });
      fireEvent.input(container.querySelector('[data-testid="imggen-form-name-input"]')!, {
        target: { value: 'Test' },
      });
      fireEvent.input(container.querySelector('[data-testid="imggen-form-internal-model-input"]')!, {
        target: { value: 'image-01' },
      });
      const submit = container.querySelector('[data-testid="imggen-form-submit"]') as HTMLButtonElement;
      fireEvent.click(submit);

      await waitFor(() => {
        expect(container.textContent).toContain('At least one credential is required');
      });
      expect(onSave).not.toHaveBeenCalled();
    });

    it('C-13 — credential provider check is enforced even if a non-minimax id is somehow set (defensive)', async () => {
      // We test the validation by directly seeding fields state — the
      // MultiCredentialEditor's dropdown is filtered to minimax, so a
      // user cannot normally pick a non-minimax credential. The
      // "credential_provider" error path protects against a stale row
      // carrying a now-deleted credential id.
      const onSave = vi.fn();
      const initial: ImgGenModel = {
        id: 'seed-1',
        name: 'Seeded',
        enabled: true,
        fallback_chain: [],
        kind: 'image-gen',
        internal: true,
        internal_provider: 'minimax',
        internal_model: 'image-01',
        internal_base_url: 'https://api.minimax.io/v1',
        // Pretend a now-deleted non-minimax credential is still attached.
        credentials: [{ credential_id: 'cred-oai-1', weight: 1, position: 0 }],
        exclude_from_ultimate_switching: true,
      };
      const { container } = render(
        <ImgGenModelForm
          mode="edit"
          initialData={initial}
          onSave={onSave}
          onCancel={vi.fn()}
          onStatus={vi.fn()}
        />,
      );
      await waitForCredsLoaded();
      // The form runs the provider check on submit. Hit Submit and the
      // pre-flight resolver hits the cred lookup, sees
      // cred-oai-1.provider === 'openai' (not minimax), and rejects.
      const submit = container.querySelector('[data-testid="imggen-form-submit"]') as HTMLButtonElement;
      // Edit mode id input is disabled, so isValid is true; the gate is
      // the post-click validation. Click and observe.
      // Ensure fields are valid in the eyes of the gate (which checks
      // non-empty + credentials e-credential_id) before submit.
      // The button is only disabled if isValid is false; provider check
      // runs in handleSubmit, not in the gate.
      fireEvent.click(submit);
      await waitFor(() => {
        expect(container.textContent).toContain('All credentials must be MiniMax');
      });
      expect(onSave).not.toHaveBeenCalled();
    });
  });

  describe('valid submit (C-14)', () => {
    it('C-14 — calls onSave once with the expected payload shape and hard-coded fields', async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      const { container } = render(
        <ImgGenModelForm
          mode="add"
          onSave={onSave}
          onCancel={vi.fn()}
          onStatus={vi.fn()}
        />,
      );
      await waitForCredsLoaded();

      fireEvent.input(container.querySelector('[data-testid="imggen-form-id-input"]')!, {
        target: { value: 'imggen-test' },
      });
      fireEvent.input(container.querySelector('[data-testid="imggen-form-name-input"]')!, {
        target: { value: 'Test Model' },
      });
      fireEvent.input(container.querySelector('[data-testid="imggen-form-internal-model-input"]')!, {
        target: { value: 'image-01' },
      });

      // The form starts with credentials=[], so MultiCredentialEditor
      // renders no rows until the user clicks "+ Add credential" to
      // create the primary row. Add one, then pick a minimax credential.
      const addCredBtn = Array.from(container.querySelectorAll('button')).find(
        (b) => b.textContent && b.textContent.trim() === '+ Add credential',
      ) as HTMLButtonElement;
      expect(addCredBtn, '+ Add credential button should be present').not.toBeNull();
      fireEvent.click(addCredBtn);

      // Now the select for the first (primary) row is rendered.
      const select = container.querySelector('select') as HTMLSelectElement;
      expect(select, 'primary credential select should be present').not.toBeNull();
      fireEvent.change(select, { target: { value: 'cred-mini-1' } });

      const submit = container.querySelector('[data-testid="imggen-form-submit"]') as HTMLButtonElement;
      fireEvent.click(submit);

      await waitFor(() => {
        expect(onSave).toHaveBeenCalledTimes(1);
      });
      const arg = (onSave as Mock).mock.calls[0][0];
      expect(arg).toMatchObject({
        id: 'imggen-test',
        name: 'Test Model',
        kind: 'image-gen',
        internal: true,
        internal_provider: 'minimax',
        internal_model: 'image-01',
        enabled: true,
        exclude_from_ultimate_switching: true,
      });
      expect(arg.credentials).toEqual([
        { credential_id: 'cred-mini-1', weight: 1, position: 0 },
      ]);
      // The chat-only fields are NOT in the payload.
      expect(arg).not.toHaveProperty('fallback_chain');
      expect(arg).not.toHaveProperty('truncate_params');
      expect(arg).not.toHaveProperty('peak_hour_enabled');
      expect(arg).not.toHaveProperty('peak_hour_start');
      expect(arg).not.toHaveProperty('peak_hour_end');
      expect(arg).not.toHaveProperty('peak_hour_timezone');
      expect(arg).not.toHaveProperty('peak_hour_model');
      expect(arg).not.toHaveProperty('secondary_upstream_model');
      expect(arg).not.toHaveProperty('release_stream_chunk_deadline');
    });
  });

  describe('edit mode (C-15)', () => {
    it('C-15 — id input is NOT rendered in edit mode (the form has no editable id field when editing)', () => {
      const initial: ImgGenModel = {
        id: 'fixed-id',
        name: 'Existing',
        enabled: true,
        fallback_chain: [],
        kind: 'image-gen',
        internal: true,
        internal_provider: 'minimax',
        internal_model: 'image-01',
        internal_base_url: 'https://api.minimax.io/v1',
        credentials: [],
        exclude_from_ultimate_switching: true,
      };
      const { container } = render(
        <ImgGenModelForm
          mode="edit"
          initialData={initial}
          onSave={vi.fn()}
          onCancel={vi.fn()}
          onStatus={vi.fn()}
        />,
      );
      // The id field is conditionally rendered only in add mode
      // (fe-spec §2.4 row 2). In edit mode it is absent — the model id
      // is immutable, so a disabled input would be misleading UI.
      expect(container.querySelector('[data-testid="imggen-form-id-input"]')).toBeNull();
      // The "fixed-id" value is still visible in the form's name+id chip
      // is not rendered here either (chat ModelsTab shows the id chip
      // in the row, not the form), so just confirm the form mounted
      // with mode=edit title.
      expect(container.textContent).toContain('Edit Image-Gen Model');
    });
  });

  describe('A11Y (fe-spec §4.8)', () => {
    it('A11Y-1 — every form input has an associated <label>', () => {
      const { container } = render(
        <ImgGenModelForm
          mode="add"
          onSave={vi.fn()}
          onCancel={vi.fn()}
          onStatus={vi.fn()}
        />,
      );
      // Count labelled inputs/selects/buttons-with-aria-label.
      const inputs = container.querySelectorAll('input, select');
      inputs.forEach((inp) => {
        const id = (inp as HTMLInputElement).id;
        if (!id) {
          // The Provider field is a div (not an input) and the enabled
          // toggle is a button — both are exempt from label-association.
          return;
        }
        const label = container.querySelector(`label[for="${id}"]`);
        expect(label, `input id=${id} should have an associated label`).not.toBeNull();
      });
    });

    it('A11Y-2 — required fields carry aria-required="true" (name, id in add mode, internal_model)', () => {
      const { container } = render(
        <ImgGenModelForm
          mode="add"
          onSave={vi.fn()}
          onCancel={vi.fn()}
          onStatus={vi.fn()}
        />,
      );
      // Three required inputs per spec §4.8 A11Y-2.
      const required = container.querySelectorAll('[aria-required="true"]');
      expect(required.length).toBe(3);
    });

    it('A11Y-2 (edit mode) — id input is absent; remaining required fields still carry aria-required', () => {
      const initial: ImgGenModel = {
        id: 'fixed',
        name: 'X',
        enabled: true,
        fallback_chain: [],
        kind: 'image-gen',
        internal: true,
        internal_provider: 'minimax',
        internal_model: 'image-01',
        internal_base_url: '',
        credentials: [],
        exclude_from_ultimate_switching: true,
      };
      const { container } = render(
        <ImgGenModelForm
          mode="edit"
          initialData={initial}
          onSave={vi.fn()}
          onCancel={vi.fn()}
          onStatus={vi.fn()}
        />,
      );
      const required = container.querySelectorAll('[aria-required="true"]');
      // name + internal_model (id input is not rendered in edit mode).
      expect(required.length).toBe(2);
    });
  });
});
