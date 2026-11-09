import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, waitFor } from '@testing-library/preact';
import { CredentialsTab } from '../CredentialsTab';
import { ModelForm } from '../ModelForm';
import type { Provider, Credential } from '../../../types';

const mockProviders: Provider[] = [
  { type: 'openai', name: 'OpenAI', base_url: 'https://api.openai.com/v1', color: 'green' },
  { type: 'minimax', name: 'MiniMax', base_url: 'https://api.minimax.io/v1', color: 'yellow' },
  { type: '9router', name: '9Router', base_url: 'http://ninerouter.9router.svc.cluster.local:20128/v1', color: 'rose' },
];

const mockCredentials: Credential[] = [
  { id: 'cred-oai-1', provider: 'openai', base_url: 'https://api.openai.com/v1' },
  { id: 'cred-9r-1', provider: '9router', base_url: 'http://ninerouter.9router.svc.cluster.local:20128/v1' },
];

vi.mock('../../../hooks/useApi', async (importOriginal) => {
  const real = await importOriginal<typeof import('../../../hooks/useApi')>();
  return {
    ...real,
    useProviders: vi.fn(() => ({ providers: mockProviders, loading: false, error: null })),
    getCredentials: vi.fn(async () => mockCredentials),
    createCredential: vi.fn(async () => ({ success: true })),
  };
});

describe('CredentialsTab & ModelForm 9router integration (B1)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('9router is selectable in the credential-creation provider dropdown with default base URL', async () => {
    const { container } = render(<CredentialsTab status={null} setStatus={vi.fn()} />);

    // Click "+ Add Credential"
    await waitFor(() => {
      const addBtn = Array.from(container.querySelectorAll('button')).find(
        (b) => b.textContent && b.textContent.includes('Add Credential'),
      );
      expect(addBtn).not.toBeNull();
      fireEvent.click(addBtn!);
    });

    // Check provider select options
    const providerSelect = container.querySelector('select') as HTMLSelectElement;
    expect(providerSelect).not.toBeNull();
    const options = Array.from(providerSelect.querySelectorAll('option'));
    const optValues = options.map((o) => o.value);
    expect(optValues).toContain('9router');

    // Switch to 9router
    fireEvent.change(providerSelect, { target: { value: '9router' } });

    // Base URL input should be prefilled with 9router default base URL
    const baseUrlInput = container.querySelector('input[placeholder="http://ninerouter.9router.svc.cluster.local:20128/v1"]') as HTMLInputElement;
    expect(baseUrlInput).not.toBeNull();
    expect(baseUrlInput.value).toBe('http://ninerouter.9router.svc.cluster.local:20128/v1');
  });

  it('ModelForm excludes 9router credentials from the chat model credential selection dropdown', async () => {
    const { container } = render(
      <ModelForm
        mode="add"
        onSave={vi.fn()}
        onCancel={vi.fn()}
        onStatus={vi.fn()}
      />,
    );

    await waitFor(() => {
      const addCredBtn = Array.from(container.querySelectorAll('button')).find(
        (b) => b.textContent && b.textContent.includes('+ Add credential'),
      );
      expect(addCredBtn).not.toBeNull();
      fireEvent.click(addCredBtn!);
    });

    const selects = Array.from(container.querySelectorAll('select')) as HTMLSelectElement[];
    expect(selects.length).toBeGreaterThan(0);
    const credSelect = selects[0];
    const credOptions = Array.from(credSelect.querySelectorAll('option')).map((o) => o.textContent || '');

    // Chat model form must offer openai credential but EXCLUDE 9router credential
    expect(credOptions.some((t) => t.includes('cred-oai-1'))).toBe(true);
    expect(credOptions.some((t) => t.includes('cred-9r-1'))).toBe(false);
  });
});
