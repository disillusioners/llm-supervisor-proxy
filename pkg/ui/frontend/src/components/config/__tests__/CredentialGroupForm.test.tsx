import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent } from '@testing-library/preact';
import { CredentialGroupForm } from '../CredentialGroupForm';
import type { Credential, Provider } from '../../../types';

// credential-groups commission (2026-11-16) — the credential-group
// form: provider vocabulary, provider-filtered member picker, the
// submit-time guards (missing id/name, empty member list, duplicate
// member, missing member, cross-provider member) and the save payload.

const mockProviders: Provider[] = [
  { type: 'openai', name: 'OpenAI', base_url: 'https://api.openai.com/v1', color: 'green' },
  { type: 'minimax', name: 'MiniMax', base_url: 'https://api.minimax.io/v1', color: 'yellow' },
];

const mockCredentials: Credential[] = [
  { id: 'cred-oai-1', provider: 'openai' },
  { id: 'cred-oai-2', provider: 'openai' },
  { id: 'cred-mm-1', provider: 'minimax' },
];

vi.mock('../../../hooks/useApi', async (importOriginal) => {
  const real = await importOriginal<typeof import('../../../hooks/useApi')>();
  return {
    ...real,
    useProviders: vi.fn(() => ({ providers: mockProviders, loading: false })),
  };
});

function formEl(container: Element): HTMLFormElement {
  return container.querySelector('form') as HTMLFormElement;
}

function errorText(container: Element): string {
  const el = container.querySelector('[data-testid="credential-group-form-error"]');
  return el ? el.textContent || '' : '';
}

function setInput(container: Element, testId: string, value: string): void {
  const input = container.querySelector(`[data-testid="${testId}"]`) as HTMLInputElement;
  fireEvent.input(input, { target: { value } });
}

describe('CredentialGroupForm (credential-groups commission)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('C1 — the provider select is fed by useProviders()', () => {
    const { container } = render(
      <CredentialGroupForm
        mode="add"
        credentials={mockCredentials}
        onSave={vi.fn()}
        onCancel={vi.fn()}
        saving={false}
      />,
    );
    const select = container.querySelector(
      '[data-testid="credential-group-provider-select"]',
    ) as HTMLSelectElement;
    const values = Array.from(select.querySelectorAll('option')).map((o) => o.value);
    expect(values).toEqual(['openai', 'minimax']);
  });

  it('C2 — the member picker only offers credentials of the group provider', () => {
    const { container } = render(
      <CredentialGroupForm
        mode="add"
        credentials={mockCredentials}
        onSave={vi.fn()}
        onCancel={vi.fn()}
        saving={false}
      />,
    );
    fireEvent.click(container.querySelector('[data-testid="credential-group-member-add"]')!);
    let select = container.querySelector(
      '[data-testid="credential-group-member-select-0"]',
    ) as HTMLSelectElement;
    let values = Array.from(select.querySelectorAll('option')).map((o) => o.value);
    expect(values).toContain('cred-oai-1');
    expect(values).not.toContain('cred-mm-1');

    // Switch the group provider — the picker's options follow.
    fireEvent.change(
      container.querySelector('[data-testid="credential-group-provider-select"]') as HTMLSelectElement,
      { target: { value: 'minimax' } },
    );
    select = container.querySelector(
      '[data-testid="credential-group-member-select-0"]',
    ) as HTMLSelectElement;
    values = Array.from(select.querySelectorAll('option')).map((o) => o.value);
    expect(values).toContain('cred-mm-1');
    expect(values).not.toContain('cred-oai-1');
  });

  it('C3 — a cross-provider member is rejected at submit', () => {
    const onSave = vi.fn();
    const { container } = render(
      <CredentialGroupForm
        mode="edit"
        initialData={{
          id: 'pool-1',
          name: 'Pool 1',
          provider: 'openai',
          members: ['cred-mm-1'],
        }}
        credentials={mockCredentials}
        onSave={onSave}
        onCancel={vi.fn()}
        saving={false}
      />,
    );
    fireEvent.submit(formEl(container));
    expect(errorText(container)).toContain('Member cred-mm-1 is not a openai credential');
    expect(onSave).not.toHaveBeenCalled();
  });

  it('C4 — a member that does not exist is rejected at submit', () => {
    const onSave = vi.fn();
    const { container } = render(
      <CredentialGroupForm
        mode="edit"
        initialData={{ id: 'pool-1', name: 'Pool 1', provider: 'openai', members: ['ghost'] }}
        credentials={mockCredentials}
        onSave={onSave}
        onCancel={vi.fn()}
        saving={false}
      />,
    );
    fireEvent.submit(formEl(container));
    expect(errorText(container)).toContain('Member ghost does not exist');
    expect(onSave).not.toHaveBeenCalled();
  });

  it('C5 — a duplicate member is rejected at submit', () => {
    const onSave = vi.fn();
    const { container } = render(
      <CredentialGroupForm
        mode="edit"
        initialData={{
          id: 'pool-1',
          name: 'Pool 1',
          provider: 'openai',
          members: ['cred-oai-1', 'cred-oai-1'],
        }}
        credentials={mockCredentials}
        onSave={onSave}
        onCancel={vi.fn()}
        saving={false}
      />,
    );
    fireEvent.submit(formEl(container));
    expect(errorText(container)).toContain('Member cred-oai-1 is listed more than once');
    expect(onSave).not.toHaveBeenCalled();
  });

  it('C6 — an empty member list is rejected at submit', () => {
    const onSave = vi.fn();
    const { container } = render(
      <CredentialGroupForm
        mode="edit"
        initialData={{ id: 'pool-1', name: 'Pool 1', provider: 'openai', members: [] }}
        credentials={mockCredentials}
        onSave={onSave}
        onCancel={vi.fn()}
        saving={false}
      />,
    );
    fireEvent.submit(formEl(container));
    expect(errorText(container)).toContain('Add at least one member credential');
    expect(onSave).not.toHaveBeenCalled();
  });

  it('C7 — the group ID is disabled in edit mode', () => {
    const { container } = render(
      <CredentialGroupForm
        mode="edit"
        initialData={{ id: 'pool-1', name: 'Pool 1', provider: 'openai', members: ['cred-oai-1'] }}
        credentials={mockCredentials}
        onSave={vi.fn()}
        onCancel={vi.fn()}
        saving={false}
      />,
    );
    const idInput = container.querySelector(
      '[data-testid="credential-group-id-input"]',
    ) as HTMLInputElement;
    expect(idInput.disabled).toBe(true);
    expect(idInput.value).toBe('pool-1');
  });

  it('C8 — the save payload is {id,name,provider,members}', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    const { container } = render(
      <CredentialGroupForm
        mode="add"
        credentials={mockCredentials}
        onSave={onSave}
        onCancel={vi.fn()}
        saving={false}
      />,
    );
    setInput(container, 'credential-group-id-input', 'pool-1');
    setInput(container, 'credential-group-name-input', 'Pool One');
    fireEvent.click(container.querySelector('[data-testid="credential-group-member-add"]')!);
    fireEvent.change(
      container.querySelector('[data-testid="credential-group-member-select-0"]') as HTMLSelectElement,
      { target: { value: 'cred-oai-2' } },
    );
    fireEvent.submit(formEl(container));

    await vi.waitFor(() => {
      expect(onSave).toHaveBeenCalledTimes(1);
    });
    expect(onSave).toHaveBeenCalledWith({
      id: 'pool-1',
      name: 'Pool One',
      provider: 'openai',
      members: ['cred-oai-2'],
    });
  });

  it('R5 — a member whose provider differs only in case is accepted (case-insensitive provider compare)', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    const credsCase: Credential[] = [
      ...mockCredentials,
      { id: 'cred-oai-case', provider: 'OpenAI' },
    ];
    const { container } = render(
      <CredentialGroupForm
        mode="edit"
        initialData={{
          id: 'pool-1',
          name: 'Pool 1',
          provider: 'openai',
          members: ['cred-oai-case'],
        }}
        credentials={credsCase}
        onSave={onSave}
        onCancel={vi.fn()}
        saving={false}
      />,
    );
    fireEvent.submit(formEl(container));
    // The member's provider is 'OpenAI', the group's is 'openai' — the
    // SAME provider, different case ⇒ the cross-provider guard must NOT
    // fire ("Member cred-oai-case is not a openai credential").
    await vi.waitFor(() => {
      expect(onSave).toHaveBeenCalledTimes(1);
    });
    expect(errorText(container)).toBe('');
    expect(onSave).toHaveBeenCalledWith({
      id: 'pool-1',
      name: 'Pool 1',
      provider: 'openai',
      members: ['cred-oai-case'],
    });
  });

  it('C6 — a missing ID is rejected at submit', () => {
    const onSave = vi.fn();
    const { container } = render(
      <CredentialGroupForm
        mode="add"
        credentials={mockCredentials}
        onSave={onSave}
        onCancel={vi.fn()}
        saving={false}
      />,
    );
    setInput(container, 'credential-group-name-input', 'Pool One');
    fireEvent.click(container.querySelector('[data-testid="credential-group-member-add"]')!);
    fireEvent.change(
      container.querySelector('[data-testid="credential-group-member-select-0"]') as HTMLSelectElement,
      { target: { value: 'cred-oai-1' } },
    );
    fireEvent.submit(formEl(container));
    expect(errorText(container)).toContain('Credential group ID is required');
    expect(onSave).not.toHaveBeenCalled();
  });
});
