// credential-groups commission (O-10): the credential-GROUP options of the
// chat model form must be gated on the MODEL KIND, exactly like the backend
// rule `kindAllowsCredentialGroups` (pkg/ui/server.go) which rejects group
// refs on the decision / image-gen / group kinds.
//
// Before the fix the group options were filtered by PROVIDER only, so a
// non-chat model could be offered a credential group and the backend would
// reject the save with a 400.

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

// --- the expectation table -------------------------------------------------
//
// The FE kind vocabulary is `Model['kind']` (src/types.ts):
//   'chat' | 'image-gen' | 'decision' | 'group'
//
// KEYED BY THE UNION ON PURPOSE: if a kind is ever added to `Model['kind']`
// this literal stops type-checking (missing key) => the tsc gate fails =>
// a new kind can never be silently skipped by this table. This is the FE
// counterpart of the backend drift where the R-5 gate compared against
// "imggen" while the real constant was `KindImageGen == "image-gen"` — a
// literal matching no kind silently DISABLES the gate instead of failing.
//
// `true`  => the credential-group option MUST be offered
// `false` => the credential-group option MUST NOT be offered
const GROUP_OFFERED_BY_KIND: Record<NonNullable<Model['kind']>, boolean> = {
  'chat': true,
  'image-gen': false,
  'decision': false,
  'group': false,
};

// The iterated set IS the union (the Record above is exhaustive by type),
// so the table can never drift away from the FE kind vocabulary.
const ALL_FE_KINDS = Object.keys(GROUP_OFFERED_BY_KIND) as NonNullable<Model['kind']>[];

function optionByValue(container: Element, value: string): HTMLOptionElement | undefined {
  return Array.from(container.querySelectorAll('option')).find((o) => o.value === value);
}

async function waitForCredsLoaded(container: Element) {
  await waitFor(
    () => {
      const addCredBtn = Array.from(container.querySelectorAll('button')).find(
        (b) => b.textContent && b.textContent.includes('+ Add credential'),
      );
      if (!addCredBtn) throw new Error('credentials not yet loaded');
    },
    { timeout: 5000 },
  );
}

function modelOfKind(kind: NonNullable<Model['kind']>): Model {
  return {
    id: `m-${kind}`,
    name: `Model ${kind}`,
    enabled: true,
    fallback_chain: [],
    truncate_params: [],
    kind,
    credentials: [{ credential_id: 'cred-oai-1', weight: 1, position: 0 }],
  };
}

describe('ModelForm — credential-group options are gated on the model kind (O-10)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  // TABLE-DRIVEN over EVERY kind of the FE union — not a sample. A new kind
  // added to `Model['kind']` breaks the Record above at COMPILE time (tsc
  // gate) so it can never land here untested.
  for (const kind of ALL_FE_KINDS) {
    const offered = GROUP_OFFERED_BY_KIND[kind];

    it(`edit mode — kind '${kind}': the credential-group option is ${offered ? 'OFFERED' : 'NOT offered'}`, async () => {
      const { container } = render(
        <ModelForm
          mode="edit"
          initialData={modelOfKind(kind)}
          onSave={vi.fn()}
          onCancel={vi.fn()}
          onStatus={vi.fn()}
        />,
      );
      await waitForCredsLoaded(container);

      // ANTI-VACUITY: the credential row's select MUST be rendered and MUST
      // carry its plain-credential options. Without this assertion a
      // "group option absent" verdict could pass on an empty DOM (i.e. the
      // test would be vacuous, not the gate).
      const credOption = optionByValue(container, 'cred-oai-1');
      expect(credOption).toBeDefined();

      const groupOption = optionByValue(container, 'group-oai');
      if (offered) {
        expect(groupOption).toBeDefined();
        expect(groupOption!.textContent).toContain('🗂️');
        expect(groupOption!.textContent).toContain('OpenAI Pool');
        expect(groupOption!.textContent).toContain('openai');
      } else {
        expect(groupOption).toBeUndefined();
      }
    });
  }

  it('add mode (no initialData ⇒ the chat form) still OFFERS the credential-group option', async () => {
    const { container } = render(
      <ModelForm mode="add" onSave={vi.fn()} onCancel={vi.fn()} onStatus={vi.fn()} />,
    );
    await waitForCredsLoaded(container);

    // add a credential row so the picker renders its select
    const addCredBtn = Array.from(container.querySelectorAll('button')).find(
      (b) => b.textContent && b.textContent.includes('+ Add credential'),
    )!;
    fireEvent.click(addCredBtn);

    await waitFor(() => {
      expect(optionByValue(container, 'cred-oai-1')).toBeDefined();
    });

    const groupOption = optionByValue(container, 'group-oai');
    expect(groupOption).toBeDefined();
    expect(groupOption!.textContent).toContain('🗂️');
    expect(groupOption!.textContent).toContain('OpenAI Pool');
  });

  it('chat kind: the pre-existing 9router provider filter is retained (a 9router group/credential is never offered)', async () => {
    const { container } = render(
      <ModelForm
        mode="edit"
        initialData={modelOfKind('chat')}
        onSave={vi.fn()}
        onCancel={vi.fn()}
        onStatus={vi.fn()}
      />,
    );
    await waitForCredsLoaded(container);

    // the openai group IS offered (chat kind)
    expect(optionByValue(container, 'group-oai')).toBeDefined();
    // the 9router group is NOT (provider filter kept)
    expect(optionByValue(container, 'group-9r')).toBeUndefined();
    // the 9router credential is NOT (provider filter kept)
    expect(optionByValue(container, 'cred-9r-1')).toBeUndefined();
  });
});
