import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent } from '@testing-library/preact';
import { MultiCredentialEditor } from '../MultiCredentialEditor';
import type { Credential } from '../../../types';

const creds: Credential[] = [
  { id: 'cred-oai-1', provider: 'openai' },
  { id: 'cred-oai-2', provider: 'openai' },
  { id: 'cred-anth-1', provider: 'anthropic' },
];

const groups = [
  { id: 'group-oai', name: 'OpenAI Pool', provider: 'openai', members: ['cred-oai-1', 'cred-oai-2'] },
  { id: 'group-anth', name: 'Anthropic Pool', provider: 'anthropic', members: ['cred-anth-1'] },
];

describe('MultiCredentialEditor — mixed credential/group picker (T-F)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
  });

  it('F1: group option renders distinctly with 🗂️ + name + provider', () => {
    const onChange = vi.fn();
    const { container } = render(
      <MultiCredentialEditor
        credentials={[{ credential_id: '', weight: 1, position: 0 }]}
        availableCredentials={creds}
        availableGroups={groups as any}
        loading={false}
        onChange={onChange}
      />,
    );
    const selects = container.querySelectorAll('select');
    expect(selects.length).toBeGreaterThan(0);
    const opts = Array.from(selects[0].querySelectorAll('option')).map((o) => o.textContent || '');
    expect(opts.some((t) => t.includes('🗂️ OpenAI Pool (openai)'))).toBe(true);
  });

  it('F2: group selectable on row 0', () => {
    const onChange = vi.fn();
    const { container } = render(
      <MultiCredentialEditor
        credentials={[{ credential_id: '', weight: 1, position: 0 }]}
        availableCredentials={creds}
        availableGroups={groups as any}
        loading={false}
        onChange={onChange}
      />,
    );
    const select = container.querySelector('select') as HTMLSelectElement;
    fireEvent.change(select, { target: { value: 'group-oai' } });
    expect(onChange).toHaveBeenCalled();
    const last = onChange.mock.calls[onChange.mock.calls.length - 1][0];
    expect(last[0].group_id).toBe('group-oai');
    expect(last[0].credential_id).toBe('');
  });

  it('F7: selecting a group writes group_id + empty credential_id', () => {
    const onChange = vi.fn();
    const { container } = render(
      <MultiCredentialEditor
        credentials={[{ credential_id: '', weight: 1, position: 0 }]}
        availableCredentials={creds}
        availableGroups={groups as any}
        loading={false}
        onChange={onChange}
      />,
    );
    const select = container.querySelector('select') as HTMLSelectElement;
    fireEvent.change(select, { target: { value: 'group-oai' } });
    const last = onChange.mock.calls[onChange.mock.calls.length - 1][0];
    expect(last[0]).toMatchObject({ group_id: 'group-oai', credential_id: '' });
  });

  it('F6: weight input applies to a group row', () => {
    const onChange = vi.fn();
    const { container } = render(
      <MultiCredentialEditor
        credentials={[{ credential_id: '', group_id: 'group-oai', weight: 1, position: 0 }]}
        availableCredentials={creds}
        availableGroups={groups as any}
        loading={false}
        onChange={onChange}
      />,
    );
    const weightInput = container.querySelector('input[type="number"]') as HTMLInputElement;
    expect(weightInput).not.toBeNull();
    fireEvent.input(weightInput, { target: { value: '3' } });
    const last = onChange.mock.calls[onChange.mock.calls.length - 1][0];
    expect(last[0].weight).toBe(3);
    expect(last[0].group_id).toBe('group-oai');
  });

  it('F3: group disabled on row > 0 when providers differ', () => {
    const onChange = vi.fn();
    const { container } = render(
      <MultiCredentialEditor
        credentials={[
          { credential_id: '', group_id: 'group-oai', weight: 1, position: 0 },
          { credential_id: '', weight: 1, position: 1 },
        ]}
        availableCredentials={creds}
        availableGroups={groups as any}
        loading={false}
        onChange={onChange}
      />,
    );
    const selects = container.querySelectorAll('select');
    const secondSelect = selects[1];
    const groupOption = Array.from(secondSelect.querySelectorAll('option')).find(
      (o) => o.getAttribute('value') === 'group-anth',
    ) as HTMLOptionElement;
    expect(groupOption).not.toBeNull();
    expect(groupOption.disabled).toBe(true);
    expect(groupOption.title).toBe('All credentials must share the same provider');
  });

  it('F4: group disabled when already selected on another row', () => {
    const onChange = vi.fn();
    const { container } = render(
      <MultiCredentialEditor
        credentials={[
          { credential_id: '', group_id: 'group-oai', weight: 1, position: 0 },
          { credential_id: '', weight: 1, position: 1 },
        ]}
        availableCredentials={creds}
        availableGroups={groups as any}
        loading={false}
        onChange={onChange}
      />,
    );
    const selects = container.querySelectorAll('select');
    const secondSelect = selects[1];
    const groupOption = Array.from(secondSelect.querySelectorAll('option')).find(
      (o) => o.getAttribute('value') === 'group-oai',
    ) as HTMLOptionElement;
    expect(groupOption).not.toBeNull();
    expect(groupOption.disabled).toBe(true);
    expect(groupOption.title).toBe('Already selected in another row');
  });

  it('F5: credential disabled when row-0 is a group of another provider (A-12.ii)', () => {
    const onChange = vi.fn();
    const { container } = render(
      <MultiCredentialEditor
        credentials={[
          { credential_id: '', group_id: 'group-anth', weight: 1, position: 0 },
          { credential_id: '', weight: 1, position: 1 },
        ]}
        availableCredentials={creds}
        availableGroups={groups as any}
        loading={false}
        onChange={onChange}
      />,
    );
    const selects = container.querySelectorAll('select');
    const secondSelect = selects[1];
    const credOption = Array.from(secondSelect.querySelectorAll('option')).find(
      (o) => o.getAttribute('value') === 'cred-oai-1',
    ) as HTMLOptionElement;
    expect(credOption).not.toBeNull();
    expect(credOption.disabled).toBe(true);
    expect(credOption.title).toBe('All credentials must share the same provider');
  });

  it('F8: group badge renders when row value is a group id', () => {
    const onChange = vi.fn();
    const { container } = render(
      <MultiCredentialEditor
        credentials={[{ credential_id: '', group_id: 'group-oai', weight: 1, position: 0 }]}
        availableCredentials={creds}
        availableGroups={groups as any}
        loading={false}
        onChange={onChange}
      />,
    );
    const badge = container.querySelector('[data-testid="credential-group-ref-badge"]');
    expect(badge).not.toBeNull();
    expect(badge!.textContent).toContain('🗂️ Group');
  });

  it('F8 regression: plain credential list renders byte-identically (no group_id in payload)', () => {
    const onChange = vi.fn();
    const { container } = render(
      <MultiCredentialEditor
        credentials={[{ credential_id: 'cred-oai-1', weight: 2, position: 0 }]}
        availableCredentials={creds}
        loading={false}
        onChange={onChange}
      />,
    );
    // no availableGroups → no group options
    const opts = Array.from(container.querySelectorAll('option')).map((o) => o.textContent || '');
    expect(opts.some((t) => t.includes('🗂️'))).toBe(false);
    // select value shows credential id
    const select = container.querySelector('select') as HTMLSelectElement;
    expect(select.value).toBe('cred-oai-1');
    // Fire a real change so the emitted payload is inspected (discriminating):
    // a plain credential row must NOT carry a group_id key.
    const weightInput = container.querySelector('input[type="number"]') as HTMLInputElement;
    fireEvent.input(weightInput, { target: { value: '3' } });
    expect(onChange).toHaveBeenCalled();
    const last = onChange.mock.calls[onChange.mock.calls.length - 1][0];
    expect(Object.prototype.hasOwnProperty.call(last[0], 'group_id')).toBe(false);
  });

  it('O10: group whose provider differs only in case is NOT disabled', () => {
    const onChange = vi.fn();
    const groupsCase = [
      { id: 'group-oai-case', name: 'OpenAI Pool (mixed case)', provider: 'OpenAI', members: ['cred-oai-1'] },
    ];
    const { container } = render(
      <MultiCredentialEditor
        credentials={[
          { credential_id: 'cred-oai-1', weight: 1, position: 0 },
          { credential_id: '', weight: 1, position: 1 },
        ]}
        availableCredentials={creds}
        availableGroups={groupsCase as any}
        loading={false}
        onChange={onChange}
      />,
    );
    const selects = container.querySelectorAll('select');
    const secondSelect = selects[1];
    const groupOption = Array.from(secondSelect.querySelectorAll('option')).find(
      (o) => o.getAttribute('value') === 'group-oai-case',
    ) as HTMLOptionElement;
    expect(groupOption).not.toBeNull();
    expect(groupOption.disabled).toBe(false);
  });


  it('warn-only: member-overlap hint renders', () => {
    const onChange = vi.fn();
    const { container } = render(
      <MultiCredentialEditor
        credentials={[
          { credential_id: 'cred-oai-1', weight: 1, position: 0 },
          { credential_id: '', group_id: 'group-oai', weight: 1, position: 1 },
        ]}
        availableCredentials={creds}
        availableGroups={groups as any}
        loading={false}
        onChange={onChange}
      />,
    );
    // cred-oai-1 is a member of group-oai selected on another row → hint
    const hint = container.querySelector('[data-testid="credential-group-member-overlap-warn"]');
    expect(hint).not.toBeNull();
    expect(hint!.textContent).toContain('Credential cred-oai-1 is already included via group OpenAI Pool');
  });
});
