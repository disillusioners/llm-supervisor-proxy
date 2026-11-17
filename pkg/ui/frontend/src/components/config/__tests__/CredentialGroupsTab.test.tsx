import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, waitFor } from '@testing-library/preact';
import { CredentialGroupsTab } from '../CredentialGroupsTab';
import {
  ApiError,
  CREDENTIAL_GROUP_IN_USE_MESSAGE,
  createCredentialGroup,
  deleteCredentialGroup,
  getCredentials,
  updateCredentialGroup,
} from '../../../hooks/useApi';
import type { Credential, CredentialGroup, Model, Provider } from '../../../types';

// credential-groups commission (2026-11-16) — the credential-group
// management surface: list, create/edit/delete, the "used by N models"
// badge and the 409 delete-block flow with the inline-then-delete
// recovery action.
//
// The tab does NOT own the group list — the container owns the fetch and
// hands the list down; the tab re-reads it through `onRefreshGroups`.

const mockProviders: Provider[] = [
  { type: 'openai', name: 'OpenAI', base_url: 'https://api.openai.com/v1', color: 'green' },
  { type: 'minimax', name: 'MiniMax', base_url: 'https://api.minimax.io/v1', color: 'yellow' },
];

const mockCredentials: Credential[] = [
  { id: 'cred-oai-1', provider: 'openai' },
  { id: 'cred-oai-2', provider: 'openai' },
  { id: 'cred-mm-1', provider: 'minimax' },
];

const mockGroups: CredentialGroup[] = [
  { id: 'pool-1', name: 'OpenAI Pool', provider: 'openai', members: ['cred-oai-1'], used_by: ['model-a'] },
  { id: 'pool-2', name: 'MiniMax Pool', provider: 'minimax', members: ['cred-mm-1'] },
];

const mockModels: Model[] = [
  { id: 'model-a', name: 'Model A', enabled: true, fallback_chain: [] },
];

vi.mock('../../../hooks/useApi', async (importOriginal) => {
  const real = await importOriginal<typeof import('../../../hooks/useApi')>();
  return {
    ...real,
    useProviders: vi.fn(() => ({ providers: mockProviders, loading: false })),
    getCredentials: vi.fn(async () => mockCredentials),
    createCredentialGroup: vi.fn(async (group: CredentialGroup) => group),
    updateCredentialGroup: vi.fn(async (id: string, group: Partial<CredentialGroup>) => ({
      id,
      ...group,
    })),
    deleteCredentialGroup: vi.fn(async () => undefined),
  };
});

function renderTab(overrides: Partial<{
  groups: CredentialGroup[];
  models: Model[];
  usedBy: Record<string, string[]>;
  onInlineAndDelete: (group: CredentialGroup) => Promise<void>;
  onRefreshGroups: () => Promise<CredentialGroup[]>;
  status: { type: 'success' | 'error'; message: string } | null;
  setStatus: (status: { type: 'success' | 'error'; message: string } | null) => void;
}> = {}) {
  const props = {
    groups: mockGroups,
    models: mockModels,
    usedBy: { 'pool-1': ['model-a'] },
    onInlineAndDelete: vi.fn().mockResolvedValue(undefined),
    onRefreshGroups: vi.fn().mockResolvedValue(mockGroups),
    status: null,
    setStatus: vi.fn(),
    ...overrides,
  };
  const result = render(
    <CredentialGroupsTab
      groups={props.groups}
      models={props.models}
      usedBy={props.usedBy}
      onInlineAndDelete={props.onInlineAndDelete}
      onRefreshGroups={props.onRefreshGroups}
      status={props.status}
      setStatus={props.setStatus}
    />,
  );
  return { ...result, props };
}

describe('CredentialGroupsTab (credential-groups commission)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('B1 — renders the groups it is given (no client-side re-filter)', async () => {
    const { container } = renderTab();
    await waitFor(() => {
      expect(container.querySelector('[data-testid="credential-group-row-pool-1"]')).not.toBeNull();
    });
    expect(container.querySelector('[data-testid="credential-group-row-pool-2"]')).not.toBeNull();
    expect(container.textContent).toContain('OpenAI Pool');
    expect(container.textContent).toContain('MiniMax Pool');
    // The ordered member ids are rendered.
    expect(container.querySelector('[data-testid="credential-group-row-pool-1"]')!.textContent).toContain(
      'cred-oai-1',
    );
  });

  it('B2 — the "used by N models" badge counts the server-side used_by', async () => {
    const { container } = renderTab();
    await waitFor(() => {
      expect(container.querySelector('[data-testid="credential-group-used-by-pool-1"]')).not.toBeNull();
    });
    expect(container.querySelector('[data-testid="credential-group-used-by-pool-1"]')!.textContent).toContain(
      'Used by 1 models',
    );
    expect(container.querySelector('[data-testid="credential-group-used-by-pool-2"]')!.textContent).toContain(
      'Used by 0 models',
    );
  });

  it('B3 — create posts {id,name,provider,members}', async () => {
    const { container } = renderTab();
    await waitFor(() => {
      expect(container.querySelector('[data-testid="credential-groups-add"]')).not.toBeNull();
    });
    fireEvent.click(container.querySelector('[data-testid="credential-groups-add"]')!);

    const idInput = container.querySelector('[data-testid="credential-group-id-input"]') as HTMLInputElement;
    fireEvent.input(idInput, { target: { value: 'pool-new' } });
    const nameInput = container.querySelector(
      '[data-testid="credential-group-name-input"]',
    ) as HTMLInputElement;
    fireEvent.input(nameInput, { target: { value: 'New Pool' } });
    fireEvent.click(container.querySelector('[data-testid="credential-group-member-add"]')!);
    fireEvent.change(
      container.querySelector('[data-testid="credential-group-member-select-0"]') as HTMLSelectElement,
      { target: { value: 'cred-oai-1' } },
    );
    fireEvent.submit(container.querySelector('form')!);

    await waitFor(() => {
      expect(vi.mocked(createCredentialGroup).mock.calls.length).toBeGreaterThan(0);
    });
    expect(vi.mocked(createCredentialGroup).mock.calls[0][0]).toEqual({
      id: 'pool-new',
      name: 'New Pool',
      provider: 'openai',
      members: ['cred-oai-1'],
    });
  });

  it('B4 — edit PUTs {id,name,provider,members} for the edited group', async () => {
    const { container } = renderTab();
    await waitFor(() => {
      expect(container.querySelector('[data-testid="credential-group-edit-pool-1"]')).not.toBeNull();
    });
    fireEvent.click(container.querySelector('[data-testid="credential-group-edit-pool-1"]')!);

    const nameInput = container.querySelector(
      '[data-testid="credential-group-name-input"]',
    ) as HTMLInputElement;
    expect(nameInput.value).toBe('OpenAI Pool');
    fireEvent.input(nameInput, { target: { value: 'Renamed Pool' } });
    fireEvent.submit(container.querySelector('form')!);

    await waitFor(() => {
      expect(vi.mocked(updateCredentialGroup).mock.calls.length).toBeGreaterThan(0);
    });
    expect(vi.mocked(updateCredentialGroup).mock.calls[0][0]).toBe('pool-1');
    expect(vi.mocked(updateCredentialGroup).mock.calls[0][1]).toEqual({
      id: 'pool-1',
      name: 'Renamed Pool',
      provider: 'openai',
      members: ['cred-oai-1'],
    });
  });

  it('B5 — the delete happy path removes the group and refreshes the list', async () => {
    const { container, props } = renderTab();
    await waitFor(() => {
      expect(container.querySelector('[data-testid="credential-group-delete-pool-1"]')).not.toBeNull();
    });
    fireEvent.click(container.querySelector('[data-testid="credential-group-delete-pool-1"]')!);
    expect(container.textContent).toContain('Delete Credential Group');

    fireEvent.click(container.querySelector('[data-testid="credential-group-delete-confirm"]')!);

    await waitFor(() => {
      expect(vi.mocked(deleteCredentialGroup).mock.calls.length).toBeGreaterThan(0);
    });
    expect(vi.mocked(deleteCredentialGroup).mock.calls[0][0]).toBe('pool-1');
    await waitFor(() => {
      expect(props.onRefreshGroups).toHaveBeenCalled();
    });
    // The modal is closed again.
    await waitFor(() => {
      expect(container.querySelector('[data-testid="credential-group-delete-confirm"]')).toBeNull();
    });
  });

  it('B6 — a 409 delete is blocked: the named message + the referencing models are rendered', async () => {
    vi.mocked(deleteCredentialGroup).mockRejectedValueOnce(
      new ApiError('credential group "pool-1" is referenced by 1 models', 409),
    );
    const freshGroups: CredentialGroup[] = [
      { ...mockGroups[0], used_by: ['model-a'] },
      mockGroups[1],
    ];
    const onRefreshGroups = vi.fn().mockResolvedValue(freshGroups);
    const setStatus = vi.fn();
    const { container } = renderTab({ onRefreshGroups, setStatus });

    await waitFor(() => {
      expect(container.querySelector('[data-testid="credential-group-delete-pool-1"]')).not.toBeNull();
    });
    fireEvent.click(container.querySelector('[data-testid="credential-group-delete-pool-1"]')!);
    fireEvent.click(container.querySelector('[data-testid="credential-group-delete-confirm"]')!);

    await waitFor(() => {
      expect(container.querySelector('[data-testid="credential-group-blocked"]')).not.toBeNull();
    });
    // The message comes from the NAMED FE source, not from the server text.
    expect(
      container.querySelector('[data-testid="credential-group-blocked-message"]')!.textContent,
    ).toContain(CREDENTIAL_GROUP_IN_USE_MESSAGE);
    // The server's error string is NOT what the operator is shown.
    expect(container.textContent).not.toContain('is referenced by 1 models');
    // The referencing models are listed (resolved to display names).
    const blocked = container.querySelector('[data-testid="credential-group-blocked-models"]')!;
    expect(blocked.textContent).toContain('Model A');
    // The recovery action is offered.
    expect(container.querySelector('[data-testid="credential-group-inline-delete"]')).not.toBeNull();
    // The error is mirrored to the status channel.
    expect(setStatus).toHaveBeenCalledWith({ type: 'error', message: CREDENTIAL_GROUP_IN_USE_MESSAGE });
  });

  it('B6 — a non-409 failure surfaces the error message (not the block flow)', async () => {
    vi.mocked(deleteCredentialGroup).mockRejectedValueOnce(new ApiError('boom', 500));
    const setStatus = vi.fn();
    const { container } = renderTab({ setStatus });
    await waitFor(() => {
      expect(container.querySelector('[data-testid="credential-group-delete-pool-1"]')).not.toBeNull();
    });
    fireEvent.click(container.querySelector('[data-testid="credential-group-delete-pool-1"]')!);
    fireEvent.click(container.querySelector('[data-testid="credential-group-delete-confirm"]')!);

    await waitFor(() => {
      expect(setStatus).toHaveBeenCalledWith({ type: 'error', message: 'boom' });
    });
    expect(container.querySelector('[data-testid="credential-group-blocked"]')).toBeNull();
  });

  it('B7 — the inline-then-delete action hands the blocked group to the container', async () => {
    vi.mocked(deleteCredentialGroup).mockRejectedValueOnce(
      new ApiError('credential group "pool-1" is referenced by 1 models', 409),
    );
    const onInlineAndDelete = vi.fn().mockResolvedValue(undefined);
    const onRefreshGroups = vi.fn().mockResolvedValue([
      { ...mockGroups[0], used_by: ['model-a'] },
      mockGroups[1],
    ]);
    const { container } = renderTab({ onInlineAndDelete, onRefreshGroups });

    await waitFor(() => {
      expect(container.querySelector('[data-testid="credential-group-delete-pool-1"]')).not.toBeNull();
    });
    fireEvent.click(container.querySelector('[data-testid="credential-group-delete-pool-1"]')!);
    fireEvent.click(container.querySelector('[data-testid="credential-group-delete-confirm"]')!);

    await waitFor(() => {
      expect(container.querySelector('[data-testid="credential-group-inline-delete"]')).not.toBeNull();
    });
    fireEvent.click(container.querySelector('[data-testid="credential-group-inline-delete"]')!);

    await waitFor(() => {
      expect(onInlineAndDelete).toHaveBeenCalledTimes(1);
    });
    expect(onInlineAndDelete.mock.calls[0][0].id).toBe('pool-1');
  });

  it('B8 — the empty state states the management-only semantic', async () => {
    const { container } = renderTab({ groups: [] });
    await waitFor(() => {
      expect(container.querySelector('[data-testid="credential-groups-empty"]')).not.toBeNull();
    });
    expect(container.textContent).toContain('No credential groups configured');
    expect(container.textContent).toContain(
      'Members are flattened into the referencing models at resolution — grouping changes no routing behaviour.',
    );
  });

  it('B9 — the tab self-fetches the CREDENTIAL list for the member picker', async () => {
    const { container } = renderTab();
    await waitFor(() => {
      expect(vi.mocked(getCredentials).mock.calls.length).toBeGreaterThan(0);
    });
    // The credential list reaches the picker — NOT the group list.
    expect(vi.mocked(getCredentials).mock.calls.length).toBeGreaterThan(0);
    fireEvent.click(container.querySelector('[data-testid="credential-groups-add"]')!);
    fireEvent.click(container.querySelector('[data-testid="credential-group-member-add"]')!);
    const select = container.querySelector(
      '[data-testid="credential-group-member-select-0"]',
    ) as HTMLSelectElement;
    const values = Array.from(select.querySelectorAll('option')).map((o) => o.value);
    expect(values).toContain('cred-oai-1');
  });
});
