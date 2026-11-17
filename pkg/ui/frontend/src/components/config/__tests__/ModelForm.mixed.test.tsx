import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, waitFor } from '@testing-library/preact';
import { ModelForm } from '../ModelForm';
import type { Model, Credential, CredentialGroup } from '../../../types';

const mockProviders: any[] = [
  { type: 'openai', name: 'OpenAI', base_url: 'https://api.openai.com/v1', color: 'green' },
];

const mockCredentials: Credential[] = [
  { id: 'cred-oai-1', provider: 'openai', base_url: 'https://api.openai.com/v1' },
  { id: 'cred-9r-1', provider: '9router', base_url: 'http://ninerouter.9router.svc.cluster.local:20128/v1' },
];

const mockGroups: CredentialGroup[] = [
  { id: 'group-oai', name: 'OpenAI Pool', provider: 'openai', members: ['cred-oai-1'] },
  { id: 'group-9r', name: '9Router Pool', provider: '9router', members: ['cred-9r-1'] },
];

vi.mock('../../../hooks/useApi', async (importOriginal) => {
  const real = await importOriginal<typeof import('../../../hooks/useApi')>();
  return {
    ...real,
    useProviders: vi.fn(() => ({ providers: mockProviders, loading: false, error: null })),
    getCredentials: vi.fn(async () => mockCredentials),
    getCredentialGroups: vi.fn(async () => mockGroups),
  };
});

async function waitForCredsLoaded(container: any) {
  await waitFor(() => {
    const addCredBtn = Array.from(container.querySelectorAll('button')).find(
      (b: any) => b.textContent && b.textContent.includes("+ Add credential"),
    );
    if (!addCredBtn) throw new Error('credentials not yet loaded');
  }, { timeout: 5000 });
}

describe('ModelForm — mixed credential/group picker (T-G)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('G1: hydration of a group ref', async () => {
    const initial: Model = {
      id: 'm1',
      name: 'Model 1',
      fallback_chain: [],
      truncate_params: [],
      credentials: [{ credential_id: '', group_id: 'group-oai', weight: 2, position: 0 }],
    } as any;
    const { container } = render(
      <ModelForm mode="edit" initialData={initial} onSave={vi.fn()} onCancel={vi.fn()} onStatus={vi.fn()} />,
    );
    await waitFor(() => {
      const badge = container.querySelector('[data-testid="model-form-primary-group-badge"]');
      expect(badge).not.toBeNull();
    });
    const badge = container.querySelector('[data-testid="model-form-primary-group-badge"]');
    expect(badge!.textContent).toContain('🗂️ Group: OpenAI Pool');
  });

  it('G6: legacy credential_id shim still works', async () => {
    const initial: Model = {
      id: 'm2',
      name: 'Model 2',
      fallback_chain: [],
      truncate_params: [],
      credential_id: 'cred-oai-1',
    } as any;
    const { container } = render(
      <ModelForm mode="edit" initialData={initial} onSave={vi.fn()} onCancel={vi.fn()} onStatus={vi.fn()} />,
    );
    await waitFor(() => {
      const select = container.querySelector('select');
      expect(select).not.toBeNull();
    });
    const select = container.querySelector('select') as HTMLSelectElement;
    expect(select.value).toBe('cred-oai-1');
  });

  it('G5: primary display shows group badge + provider, base URL hidden for group primary', async () => {
    const initial: Model = {
      id: 'm3',
      name: 'Model 3',
      fallback_chain: [],
      truncate_params: [],
      credentials: [{ credential_id: '', group_id: 'group-oai', weight: 1, position: 0 }],
    } as any;
    const { container } = render(
      <ModelForm mode="edit" initialData={initial} onSave={vi.fn()} onCancel={vi.fn()} onStatus={vi.fn()} />,
    );
    await waitFor(() => {
      expect(container.textContent).toContain('OpenAI Pool');
    });
    expect(container.textContent).not.toContain('Credential base URL');
  });

  it('G2: submit validation rejects empty row', async () => {
    const onSave = vi.fn();
    const onStatus = vi.fn();
    const { container } = render(
      <ModelForm mode="add" onSave={onSave} onCancel={vi.fn()} onStatus={onStatus} />,
    );
    await waitForCredsLoaded(container);
    // add a credential row
    const addCredBtn = Array.from(container.querySelectorAll('button')).find(
      (b: any) => b.textContent && b.textContent.includes("+ Add credential"),
    )!;
    fireEvent.click(addCredBtn as HTMLElement);
    // fill required fields
    const idInput = container.querySelector('input[placeholder="e.g., gpt-4"]') as HTMLInputElement;
    fireEvent.input(idInput, { target: { value: 'm-new' } });
    const nameInput = container.querySelector('input[placeholder="e.g., GPT-4"]') as HTMLInputElement;
    fireEvent.input(nameInput, { target: { value: 'New Model' } });
    // submit
    const saveBtn = container.querySelector('[data-testid="model-form-submit"]') as HTMLButtonElement;
    expect(saveBtn).not.toBeNull();
    expect(saveBtn.disabled).toBe(false);
    fireEvent.click(saveBtn);
    await waitFor(() => {
      expect(onStatus).toHaveBeenCalledWith({ type: 'error', message: expect.stringContaining('please select a credential or group') });
    });
    expect(onSave).not.toHaveBeenCalled();
  });

  it('G2: submit validation rejects duplicate ref', async () => {
    const onSave = vi.fn();
    const onStatus = vi.fn();
    const { container } = render(
      <ModelForm mode="add" onSave={onSave} onCancel={vi.fn()} onStatus={onStatus} />,
    );
    await waitForCredsLoaded(container);
    const addCredBtn = Array.from(container.querySelectorAll('button')).find(
      (b: any) => b.textContent && b.textContent.includes("+ Add credential"),
    )!;
    fireEvent.click(addCredBtn as HTMLElement);
    fireEvent.click(addCredBtn as HTMLElement);
    const selects = container.querySelectorAll('select');
    fireEvent.change(selects[0] as HTMLSelectElement, { target: { value: "cred-oai-1" } });
    fireEvent.change(selects[1] as HTMLSelectElement, { target: { value: 'cred-oai-1' } });
    const idInput = container.querySelector('input[placeholder="e.g., gpt-4"]') as HTMLInputElement;
    fireEvent.input(idInput, { target: { value: 'm-new' } });
    const nameInput = container.querySelector('input[placeholder="e.g., GPT-4"]') as HTMLInputElement;
    fireEvent.input(nameInput, { target: { value: 'New Model' } });
    // set weights
    const weightInputs = container.querySelectorAll('input[type="number"]');
    fireEvent.input(weightInputs[0], { target: { value: '1' } });
    fireEvent.input(weightInputs[1], { target: { value: '1' } });
    const saveBtn = container.querySelector('[data-testid="model-form-submit"]') as HTMLButtonElement;
    expect(saveBtn).not.toBeNull();
    expect(saveBtn.disabled).toBe(false);
    fireEvent.click(saveBtn);
    await waitFor(() => {
      expect(onStatus).toHaveBeenCalledWith({ type: 'error', message: expect.stringContaining('duplicate credential') });
    });
    expect(onSave).not.toHaveBeenCalled();
  });

  it('G2: submit validation rejects weight < 1', async () => {
    const onSave = vi.fn();
    const onStatus = vi.fn();
    const { container } = render(
      <ModelForm mode="add" onSave={onSave} onCancel={vi.fn()} onStatus={onStatus} />,
    );
    await waitForCredsLoaded(container);
    const addCredBtn = Array.from(container.querySelectorAll('button')).find(
      (b: any) => b.textContent && b.textContent.includes("+ Add credential"),
    )!;
    fireEvent.click(addCredBtn as HTMLElement);
    const select = container.querySelector('select') as HTMLSelectElement;
    fireEvent.change(select as HTMLSelectElement, { target: { value: 'cred-oai-1' } });
    const weightInput = container.querySelector('input[type="number"]') as HTMLInputElement;
    fireEvent.input(weightInput, { target: { value: '0' } });
    const idInput = container.querySelector('input[placeholder="e.g., gpt-4"]') as HTMLInputElement;
    fireEvent.input(idInput, { target: { value: 'm-new' } });
    const nameInput = container.querySelector('input[placeholder="e.g., GPT-4"]') as HTMLInputElement;
    fireEvent.input(nameInput, { target: { value: 'New Model' } });
    const saveBtn = container.querySelector('[data-testid="model-form-submit"]') as HTMLButtonElement;
    expect(saveBtn).not.toBeNull();
    expect(saveBtn.disabled).toBe(false);
    fireEvent.click(saveBtn);
    await waitFor(() => {
      expect(onStatus).toHaveBeenCalledWith({ type: 'error', message: expect.stringContaining('weight must be at least 1') });
    });
    expect(onSave).not.toHaveBeenCalled();
  });

  it('G3: Test button sends group id as credential_id when primary is group', async () => {
    const initial: Model = {
      id: 'm4',
      name: 'Model 4',
      fallback_chain: [],
      truncate_params: [],
      internal_model: 'gpt-4',
      credentials: [{ credential_id: '', group_id: 'group-oai', weight: 1, position: 0 }],
    } as any;
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    );
    const { container } = render(
      <ModelForm mode="edit" initialData={initial} onSave={vi.fn()} onCancel={vi.fn()} onStatus={vi.fn()} />,
    );
    const testBtn = Array.from(container.querySelectorAll('button')).find(
      (b) => b.textContent && b.textContent.toLowerCase().includes('test'),
    );
    expect(testBtn).not.toBeNull();
    // canTestConnection should be true (button not disabled)
    expect((testBtn as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(testBtn as HTMLElement);
    await waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });
    const call = fetchSpy.mock.calls.find((c) => String(c[0]).includes('/fe/api/models/test'));
    expect(call).toBeDefined();
    const body = JSON.parse((call![1] as any).body);
    expect(body.credential_id).toBe('group-oai');
    fetchSpy.mockRestore();
  });

  it('G4: canTestConnection true for a group primary', () => {
    const initial: Model = {
      id: 'm5',
      name: 'Model 5',
      fallback_chain: [],
      truncate_params: [],
      internal_model: 'gpt-4',
      credentials: [{ credential_id: '', group_id: 'group-oai', weight: 1, position: 0 }],
    } as any;
    const { container } = render(
      <ModelForm mode="edit" initialData={initial} onSave={vi.fn()} onCancel={vi.fn()} onStatus={vi.fn()} />,
    );
    const testBtn = Array.from(container.querySelectorAll('button')).find(
      (b) => b.textContent && b.textContent.toLowerCase().includes('test'),
    ) as HTMLButtonElement;
    expect(testBtn.disabled).toBe(false);
  });

  it('C3: 9router-group guard reachable via edit-mode hydration', async () => {
    const onStatus = vi.fn();
    const onSave = vi.fn();
    const initial: Model = {
      id: 'm9r',
      name: 'Nine Router Model',
      fallback_chain: [],
      truncate_params: [],
      credentials: [{ credential_id: '', group_id: 'group-9r', weight: 1, position: 0 }],
    } as any;
    const { container } = render(
      <ModelForm mode="edit" initialData={initial} onSave={onSave} onCancel={vi.fn()} onStatus={onStatus} />,
    );
    await waitForCredsLoaded(container);
    const saveBtn = container.querySelector('[data-testid="model-form-submit"]') as HTMLButtonElement;
    expect(saveBtn).not.toBeNull();
    expect(saveBtn.disabled).toBe(false);
    fireEvent.click(saveBtn);
    await waitFor(() => {
      expect(onStatus).toHaveBeenCalledWith({ type: 'error', message: expect.stringContaining('9router') });
    });
    expect(onSave).not.toHaveBeenCalled();
  });

  it('O8: unknown group_id is a submit-time error', async () => {
    const onStatus = vi.fn();
    const onSave = vi.fn();
    const initial: Model = {
      id: 'm-unk',
      name: 'Unknown Group Model',
      fallback_chain: [],
      truncate_params: [],
      credentials: [{ credential_id: '', group_id: 'group-unknown', weight: 1, position: 0 }],
    } as any;
    const { container } = render(
      <ModelForm mode="edit" initialData={initial} onSave={onSave} onCancel={vi.fn()} onStatus={onStatus} />,
    );
    await waitForCredsLoaded(container);
    const saveBtn = container.querySelector('[data-testid="model-form-submit"]') as HTMLButtonElement;
    expect(saveBtn).not.toBeNull();
    expect(saveBtn.disabled).toBe(false);
    fireEvent.click(saveBtn);
    await waitFor(() => {
      expect(onStatus).toHaveBeenCalledWith({ type: 'error', message: expect.stringContaining('not found') });
    });
    expect(onSave).not.toHaveBeenCalled();
  });

  it('O11: expanded cap rejects a group with 16 members plus one more row', async () => {
    const { getCredentialGroups } = await import('../../../hooks/useApi');
    (getCredentialGroups as any).mockResolvedValueOnce([
      { id: 'group-big', name: 'Big Pool', provider: 'openai', members: Array.from({ length: 16 }, (_, i) => `cred-${i}`) },
    ]);
    const onSave = vi.fn();
    const onStatus = vi.fn();
    const { container } = render(
      <ModelForm mode="add" onSave={onSave} onCancel={vi.fn()} onStatus={onStatus} />,
    );
    await waitForCredsLoaded(container);
    const addCredBtn = Array.from(container.querySelectorAll('button')).find(
      (b: any) => b.textContent && b.textContent.includes("+ Add credential"),
    )!;
    // row 1: pick the big group
    fireEvent.click(addCredBtn as HTMLElement);
    const firstSelect = container.querySelector('select') as HTMLSelectElement;
    fireEvent.change(firstSelect, { target: { value: 'group-big' } });
    // row 2: one more direct/empty row
    fireEvent.click(addCredBtn as HTMLElement);
    const idInput = container.querySelector('input[placeholder="e.g., gpt-4"]') as HTMLInputElement;
    fireEvent.input(idInput, { target: { value: 'm-new' } });
    const nameInput = container.querySelector('input[placeholder="e.g., GPT-4"]') as HTMLInputElement;
    fireEvent.input(nameInput, { target: { value: 'New Model' } });
    const saveBtn = container.querySelector('[data-testid="model-form-submit"]') as HTMLButtonElement;
    expect(saveBtn).not.toBeNull();
    expect(saveBtn.disabled).toBe(false);
    fireEvent.click(saveBtn);
    await waitFor(() => {
      expect(onStatus).toHaveBeenCalledWith({ type: 'error', message: expect.stringContaining('At most 16 credentials allowed') });
    });
    expect(onSave).not.toHaveBeenCalled();
  });

  it('validatedCredentials maps credential_id with ?? "" (plain list byte-identical)', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    const initial: Model = {
      id: 'm6',
      name: 'Model 6',
      fallback_chain: [],
      truncate_params: [],
      credentials: [{ credential_id: 'cred-oai-1', weight: 2, position: 0 }],
    } as any;
    const { container } = render(
      <ModelForm mode="edit" initialData={initial} onSave={onSave} onCancel={vi.fn()} onStatus={vi.fn()} />,
    );
    // R1 — the submit button is looked up by its STABLE testid, never by
    // its label text. The text lookup was the round-1 vacuity: the lookup
    // missed the button and the `if (saveBtn)` guard swallowed the miss,
    // so the test passed only while the edit-mode label happened to
    // contain 'save'. The click is now UNCONDITIONAL.
    const saveBtn = container.querySelector('[data-testid="model-form-submit"]') as HTMLButtonElement;
    expect(saveBtn).not.toBeNull();
    fireEvent.click(saveBtn!);
    await waitFor(() => {
      expect(onSave).toHaveBeenCalled();
    });
    const payload = onSave.mock.calls[0][0];
    expect(payload.credentials).toEqual([{ credential_id: 'cred-oai-1', weight: 2, position: 0 }]);
    // no group_id key
    expect(Object.prototype.hasOwnProperty.call(payload.credentials[0], 'group_id')).toBe(false);
  });
});
