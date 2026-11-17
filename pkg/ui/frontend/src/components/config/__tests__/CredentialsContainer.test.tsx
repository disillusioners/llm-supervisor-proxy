import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, waitFor } from '@testing-library/preact';
import { createElement } from 'preact';
import { CredentialsContainer } from '../CredentialsContainer';
import type { Credential, CredentialGroup, Model } from '../../../types';

// credential-groups commission (2026-11-16) — the credentials-area
// container: the vertical sub-nav (Credentials | Credential Groups), the
// container-local status pass-through, the groups fetch (one owner of
// the `groups` state) and the 409 inline-then-delete recovery.
//
// The container's groups fetch is exercised through the REAL API layer
// (a stubbed global fetch) so the badge map / used-by map are wired to
// the uncached list, never to a models cache.

const mockProviders = [
  { type: 'openai', name: 'OpenAI', base_url: 'https://api.openai.com/v1', color: 'green' },
];

const mockCredentials: Credential[] = [
  { id: 'cred-oai-1', provider: 'openai' },
  { id: 'cred-oai-2', provider: 'openai' },
];

// The props CredentialsTab receives — captured so the container-local
// status pass-through can be asserted directly (the prop itself is not
// rendered by CredentialsTab).
interface CapturedCredentialsTabProps {
  status: { type: 'success' | 'error'; message: string } | null;
  setStatus: (status: { type: 'success' | 'error'; message: string } | null) => void;
  groupMemberships?: Record<string, string[]>;
}

const captured = {
  credentialsProps: null as CapturedCredentialsTabProps | null,
};

vi.mock('../CredentialsTab', async (importOriginal) => {
  const real = await importOriginal<typeof import('../CredentialsTab')>();
  return {
    ...real,
    CredentialsTab: (props: CapturedCredentialsTabProps) => {
      captured.credentialsProps = props;
      return createElement(real.CredentialsTab, props);
    },
  };
});

let groups: CredentialGroup[] = [];
let deleteCalls = 0;
let groupFetches = 0;
let deleteStatus = 204;
const realFetch = globalThis.fetch;

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

beforeEach(() => {
  document.body.innerHTML = '';
  groups = [];
  deleteCalls = 0;
  groupFetches = 0;
  deleteStatus = 204;
  captured.credentialsProps = null;
  (globalThis as { fetch: typeof fetch }).fetch = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : (input as Request).url;
      const method = (init?.method || 'GET').toUpperCase();
      if (url.includes('/fe/api/credential-groups')) {
        if (method === 'DELETE') {
          deleteCalls++;
          if (deleteStatus === 409) {
            return json({ error: 'credential group "pool-1" is referenced by 1 models' }, 409);
          }
          const id = url.split('/').pop() as string;
          groups = groups.filter((g) => g.id !== id);
          return new Response(null, { status: 204 });
        }
        if (method === 'POST' || method === 'PUT') {
          return json(groups[0] || { id: 'pool-1' });
        }
        groupFetches++;
        return json(groups);
      }
      if (url.includes('/fe/api/credentials')) {
        return json(mockCredentials);
      }
      if (url.includes('/fe/api/providers')) {
        return json(mockProviders);
      }
      return json({});
    },
  ) as unknown as typeof fetch;
});

afterEach(() => {
  (globalThis as { fetch: typeof fetch }).fetch = realFetch;
  cleanup();
  vi.clearAllMocks();
});

function renderContainer(overrides: Partial<{
  models: Model[];
  onUpdateModel: (id: string, updates: Partial<Model>) => Promise<void>;
  setStatusWrapper: (status: { type: 'success' | 'error'; message: string; restartRequired?: boolean } | null) => void;
}> = {}) {
  const props = {
    models: [] as Model[],
    onUpdateModel: vi.fn().mockResolvedValue(undefined),
    setStatusWrapper: vi.fn(),
    ...overrides,
  };
  const result = render(
    <CredentialsContainer
      models={props.models}
      onUpdateModel={props.onUpdateModel}
      setStatusWrapper={props.setStatusWrapper}
    />,
  );
  return { ...result, props };
}

function navButtons(container: Element): HTMLButtonElement[] {
  const nav = container.querySelector('[data-testid="credential-groups-nav"]') as HTMLElement;
  return Array.from(nav.querySelectorAll('button')) as HTMLButtonElement[];
}

function buttonWithExactText(container: Element, text: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll('button')).find(
    (b) => (b.textContent || '').trim() === text,
  ) as HTMLButtonElement | undefined;
}

describe('CredentialsContainer (credential-groups commission)', () => {
  it('A1 — the nav offers exactly "Credentials" and "Credential Groups", in that order', () => {
    const { container } = renderContainer();
    const buttons = navButtons(container);
    expect(buttons).toHaveLength(2);
    expect((buttons[0].textContent || '').trim()).toBe('Credentials');
    expect((buttons[1].textContent || '').trim()).toBe('Credential Groups');
  });

  it('A2 — the nav is FIRST in the DOM and the panel follows', () => {
    const { container } = renderContainer();
    const root = container.querySelector('[data-testid="credential-groups-container"]') as HTMLElement;
    const children = Array.from(root.children);
    expect(children[0].getAttribute('data-testid')).toBe('credential-groups-nav');
    expect(children[1].getAttribute('data-testid')).toBe('credential-groups-panel');
  });

  it('A3 — the nav is a vertical flex column with no horizontal overflow', () => {
    const { container } = renderContainer();
    const nav = container.querySelector('[data-testid="credential-groups-nav"]') as HTMLElement;
    expect(nav.className).toContain('flex-col');
    expect(nav.className).not.toContain('overflow-x-auto');
  });

  it('A4 — the nav buttons are NOT matched by div.flex.overflow-x-auto > button', () => {
    const { container } = renderContainer();
    expect(container.querySelectorAll('div.flex.overflow-x-auto > button')).toHaveLength(0);
  });

  it('A5 — the credentials panel is the default child', async () => {
    const { container } = renderContainer();
    await waitFor(() => {
      expect(buttonWithExactText(container, 'Add Credential')).toBeTruthy();
    });
    expect(container.querySelector('h3')!.textContent).toBe('Credentials');
  });

  it('A6 — clicking "Credential Groups" swaps the panel and moves aria-current', async () => {
    const { container } = renderContainer();
    await waitFor(() => {
      expect(buttonWithExactText(container, 'Add Credential')).toBeTruthy();
    });

    const [credentialsBtn, groupsBtn] = navButtons(container);
    expect(credentialsBtn.getAttribute('aria-current')).toBe('true');
    expect(groupsBtn.getAttribute('aria-current')).toBeNull();

    fireEvent.click(groupsBtn);

    await waitFor(() => {
      expect(container.querySelector('h3')!.textContent).toBe('Credential Groups');
    });
    expect(groupsBtn.getAttribute('aria-current')).toBe('true');
    // The inactive button OMITS aria-current (never "false").
    expect(credentialsBtn.getAttribute('aria-current')).toBeNull();
  });

  it('A7 — the active/inactive class pair is applied', () => {
    const { container } = renderContainer();
    const [credentialsBtn, groupsBtn] = navButtons(container);
    expect(credentialsBtn.className).toContain('text-blue-400');
    expect(credentialsBtn.className).toContain('border-l-blue-400');
    expect(groupsBtn.className).toContain('text-gray-400');
    expect(groupsBtn.className).toContain('border-l-transparent');
  });

  it('A8 — the nav buttons are native, enabled, type="button" and carry no roving tabindex', () => {
    const { container } = renderContainer();
    for (const button of navButtons(container)) {
      expect(button.tagName).toBe('BUTTON');
      expect(button.type).toBe('button');
      expect(button.disabled).toBe(false);
      expect(button.getAttribute('tabindex')).toBeNull();
    }
  });

  it('A9 — each nav button carries a data-testid and an aria-label', () => {
    const { container } = renderContainer();
    const [credentialsBtn, groupsBtn] = navButtons(container);
    expect(credentialsBtn.getAttribute('data-testid')).toBe('credential-groups-tab-credentials');
    expect(credentialsBtn.getAttribute('aria-label')).toBe('Credentials');
    expect(groupsBtn.getAttribute('data-testid')).toBe('credential-groups-tab-groups');
    expect(groupsBtn.getAttribute('aria-label')).toBe('Credential Groups');
  });

  it('A10 — no tablist/tab/tabpanel roles and no aria-selected are used', () => {
    const { container } = renderContainer();
    expect(container.querySelector('[role="tablist"]')).toBeNull();
    expect(container.querySelector('[role="tab"]')).toBeNull();
    expect(container.querySelector('[role="tabpanel"]')).toBeNull();
    expect(container.querySelector('[aria-selected]')).toBeNull();
  });

  it('A11 — the credentials panel mounts the unchanged credential list', async () => {
    groups = [{ id: 'pool-1', name: 'OpenAI Pool', provider: 'openai', members: ['cred-oai-1'] }];
    const { container } = renderContainer();
    await waitFor(() => {
      expect(buttonWithExactText(container, 'Add Credential')).toBeTruthy();
    });
    expect(container.textContent).toContain('cred-oai-1');
    // The badge map is derived from the UNCACHED group list.
    await waitFor(() => {
      expect(container.querySelector('[data-testid="credential-group-badge"]')).not.toBeNull();
    });
    expect(container.querySelector('[data-testid="credential-group-badge"]')!.textContent).toContain(
      'OpenAI Pool',
    );
  });

  it('A12 — switching to the groups panel unmounts the credentials panel', async () => {
    const { container } = renderContainer();
    await waitFor(() => {
      expect(buttonWithExactText(container, 'Add Credential')).toBeTruthy();
    });
    fireEvent.click(navButtons(container)[1]);
    await waitFor(() => {
      expect(container.querySelector('h3')!.textContent).toBe('Credential Groups');
    });
    expect(buttonWithExactText(container, 'Add Credential')).toBeUndefined();
    expect(container.querySelector('[data-testid="credential-groups-panel"]')).not.toBeNull();
  });

  it('A13 — the container-local status + setStatus reach CredentialsTab (no SettingsPage status prop)', async () => {
    const { props } = renderContainer();
    await waitFor(() => {
      expect(captured.credentialsProps).not.toBeNull();
    });
    const received = captured.credentialsProps as CapturedCredentialsTabProps;
    // The container supplies BOTH — CredentialsTab requires them.
    expect(received.status).toBeNull();
    expect(typeof received.setStatus).toBe('function');

    received.setStatus({ type: 'success', message: 'from the container' });

    // The wrapper is called AND the local state is updated (the next
    // render hands the new status back down).
    expect(props.setStatusWrapper).toHaveBeenCalledWith({
      type: 'success',
      message: 'from the container',
    });
    await waitFor(() => {
      expect(
        (captured.credentialsProps as { status: { type: string; message: string } | null }).status,
      ).toEqual({ type: 'success', message: 'from the container' });
    });
  });

  it('A14 — the 409 inline-then-delete inlines the members, re-attempts the delete and the group is gone', async () => {
    groups = [
      {
        id: 'pool-1',
        name: 'OpenAI Pool',
        provider: 'openai',
        members: ['cred-oai-1', 'cred-oai-2'],
        used_by: ['model-a'],
      },
    ];
    const models: Model[] = [
      {
        id: 'model-a',
        name: 'Model A',
        enabled: true,
        fallback_chain: [],
        credentials: [{ credential_id: '', group_id: 'pool-1', weight: 3, position: 0 }],
      },
    ];
    const { container, props } = renderContainer({ models });

    // Open the groups panel.
    fireEvent.click(navButtons(container)[1]);
    await waitFor(() => {
      expect(container.querySelector('[data-testid="credential-group-row-pool-1"]')).not.toBeNull();
    });

    // The first delete is blocked (409).
    deleteStatus = 409;
    fireEvent.click(container.querySelector('[data-testid="credential-group-delete-pool-1"]')!);
    fireEvent.click(container.querySelector('[data-testid="credential-group-delete-confirm"]')!);

    await waitFor(() => {
      expect(container.querySelector('[data-testid="credential-group-blocked"]')).not.toBeNull();
    });
    expect(container.querySelector('[data-testid="credential-group-blocked-models"]')!.textContent).toContain(
      'Model A',
    );
    expect(deleteCalls).toBe(1);

    // The inline-then-delete action: the group ref is replaced by the
    // members (the group ref's weight is inherited, positions reindexed)
    // and the delete is re-attempted.
    deleteStatus = 204;
    fireEvent.click(container.querySelector('[data-testid="credential-group-inline-delete"]')!);

    await waitFor(() => {
      expect(props.onUpdateModel).toHaveBeenCalled();
    });
    expect(props.onUpdateModel).toHaveBeenCalledWith('model-a', {
      credentials: [
        { credential_id: 'cred-oai-1', group_id: undefined, weight: 3, position: 0 },
        { credential_id: 'cred-oai-2', group_id: undefined, weight: 3, position: 1 },
      ],
    });
    await waitFor(() => {
      expect(deleteCalls).toBe(2);
    });
    // The group is gone from the (re-fetched) list.
    await waitFor(() => {
      expect(container.querySelector('[data-testid="credential-group-row-pool-1"]')).toBeNull();
    });
  });

  it('O6 — the 409 retry loop re-reads used_by and gives up after 3 attempts', async () => {
    groups = [
      {
        id: 'pool-1',
        name: 'OpenAI Pool',
        provider: 'openai',
        members: ['cred-oai-1'],
        used_by: ['model-a'],
      },
    ];
    const models: Model[] = [
      {
        id: 'model-a',
        name: 'Model A',
        enabled: true,
        fallback_chain: [],
        credentials: [{ credential_id: '', group_id: 'pool-1', weight: 1, position: 0 }],
      },
    ];
    const onUpdateModel = vi.fn().mockResolvedValue(undefined);
    const setStatusWrapper = vi.fn();
    const { container } = renderContainer({ models, onUpdateModel, setStatusWrapper });

    // Open the groups panel.
    fireEvent.click(navButtons(container)[1]);
    await waitFor(() => {
      expect(container.querySelector('[data-testid="credential-group-row-pool-1"]')).not.toBeNull();
    });

    // The delete is blocked (409) on EVERY attempt.
    deleteStatus = 409;
    fireEvent.click(container.querySelector('[data-testid="credential-group-delete-pool-1"]')!);
    fireEvent.click(container.querySelector('[data-testid="credential-group-delete-confirm"]')!);

    await waitFor(() => {
      expect(container.querySelector('[data-testid="credential-group-blocked"]')).not.toBeNull();
    });

    const beforeFetches = groupFetches;
    const beforeDeletes = deleteCalls;
    setStatusWrapper.mockClear();
    fireEvent.click(container.querySelector('[data-testid="credential-group-inline-delete"]')!);

    await waitFor(() => {
      // Terminal message after the 3rd attempt.
      expect(setStatusWrapper).toHaveBeenCalledWith(
        expect.objectContaining({ type: 'error', message: 'This credential group is still referenced by one or more models. Inline its members into those models, then delete the group.' }),
      );
    });

    // 3 delete attempts total (this run added 3).
    expect(deleteCalls - beforeDeletes).toBe(3);
    // used_by is re-read between attempts (at least 3 group fetches).
    expect(groupFetches - beforeFetches).toBeGreaterThanOrEqual(3);
  });
});
