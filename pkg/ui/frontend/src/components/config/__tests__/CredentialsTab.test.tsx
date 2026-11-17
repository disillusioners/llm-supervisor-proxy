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

// credential-groups commission (2026-11-16) — T-E: the group-membership
// badges in the credential list and the group line in the credential
// delete modal. The badge data comes from the container's UNCACHED group
// list (the `groupMemberships` map), never from the models cache.
describe('CredentialsTab group badges (credential-groups T-E)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('E1 — one badge is rendered per credential group the credential belongs to', async () => {
    const { container } = render(
      <CredentialsTab
        status={null}
        setStatus={vi.fn()}
        groupMemberships={{ 'cred-oai-1': ['OpenAI Pool', 'Backup Pool'] }}
      />,
    );

    await waitFor(() => {
      expect(container.querySelector('[data-testid="credential-group-badge"]')).not.toBeNull();
    });
    const badges = Array.from(container.querySelectorAll('[data-testid="credential-group-badge"]'));
    expect(badges).toHaveLength(2);
    expect(badges.map((b) => b.textContent)).toEqual(['🗂️ OpenAI Pool', '🗂️ Backup Pool']);
    // The badge sits on the credential's own row.
    const row = container.textContent?.includes('cred-oai-1') ? badges : [];
    expect(row.length).toBe(2);
  });

  it('E2 — no badges are rendered when the membership map is empty or undefined', async () => {
    const { container: withEmpty } = render(
      <CredentialsTab status={null} setStatus={vi.fn()} groupMemberships={{}} />,
    );
    await waitFor(() => {
      expect(withEmpty.textContent).toContain('cred-oai-1');
    });
    expect(withEmpty.querySelectorAll('[data-testid="credential-group-badge"]')).toHaveLength(0);
    cleanup();

    const { container: withUndefined } = render(
      <CredentialsTab status={null} setStatus={vi.fn()} />,
    );
    await waitFor(() => {
      expect(withUndefined.textContent).toContain('cred-oai-1');
    });
    expect(withUndefined.querySelectorAll('[data-testid="credential-group-badge"]')).toHaveLength(0);
  });

  it('E3 — the credential delete modal lists the credential groups', async () => {
    const { container } = render(
      <CredentialsTab
        status={null}
        setStatus={vi.fn()}
        groupMemberships={{ 'cred-oai-1': ['OpenAI Pool'] }}
      />,
    );

    await waitFor(() => {
      expect(container.textContent).toContain('cred-oai-1');
    });

    // Open the delete modal for the openai credential (the first row).
    const deleteButtons = Array.from(
      container.querySelectorAll('[title="Delete credential"]'),
    ) as HTMLButtonElement[];
    expect(deleteButtons.length).toBeGreaterThan(0);
    fireEvent.click(deleteButtons[0]);

    await waitFor(() => {
      expect(container.querySelector('[data-testid="credential-group-delete-impact"]')).not.toBeNull();
    });
    const impact = container.querySelector('[data-testid="credential-group-delete-impact"]')!;
    expect(impact.textContent).toContain('Member of credential group');
    expect(impact.textContent).toContain('OpenAI Pool');
  });
});
