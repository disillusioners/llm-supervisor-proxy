import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, waitFor } from '@testing-library/preact';
import { DecisionModelsContainer } from '../DecisionModelsContainer';
import type { Model } from '../../../types';

// The container owns a VERTICAL LEFT nav with two child tabs.
//
// The a11y contract is class-driven + one additive attribute:
//   - native <button type="button"> — Tab reaches each button in DOM
//     order, Enter/Space activate natively (happy-dom does NOT
//     synthesise Enter → click, so the test pins the PRECONDITIONS and
//     the observable consequence, not a synthesised keypress);
//   - NO role="tablist" / role="tab" / role="tabpanel", NO
//     aria-selected, NO roving tabindex, NO arrow-key handling;
//   - aria-current="true" on the ACTIVE button ONLY — the inactive
//     button OMITS the attribute entirely (never aria-current="false");
//   - the nav stays OUTSIDE div.flex.overflow-x-auto, so the top-level
//     tab selector cannot see it (the 11-tab bar is untouched).
//
// The decision-model probe hits the network, so the fetch boundary is
// stubbed (the house pattern — DecisionModelsTab.decisionTest).

const realFetch = globalThis.fetch;

beforeEach(() => {
  (globalThis as { fetch: typeof fetch }).fetch = vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : (input as Request).url;
    if (url.includes('/credentials')) {
      return new Response(JSON.stringify([]), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }
    return new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } });
  }) as unknown as typeof fetch;
  document.body.innerHTML = '';
});

afterEach(() => {
  (globalThis as { fetch: typeof fetch }).fetch = realFetch;
  cleanup();
  vi.clearAllMocks();
});

function makeDecisionRow(overrides: Partial<Model> = {}): Model {
  return {
    id: 'decision-router-v1',
    name: 'Decision Router v1',
    enabled: true,
    fallback_chain: [],
    kind: 'decision',
    internal: true,
    internal_provider: 'openai',
    internal_model: 'gpt-6-luna',
    credentials: [{ credential_id: 'cred-oai-1', weight: 1, position: 0 }],
    ...overrides,
  };
}

function makeProps(overrides: Record<string, unknown> = {}) {
  return {
    models: [] as Model[],
    onAddModel: vi.fn(),
    onUpdateModel: vi.fn(),
    onDeleteModel: vi.fn(),
    onToggleModel: vi.fn(),
    setStatus: vi.fn(),
    groups: [] as Model[],
    laneModels: [] as Model[],
    onAddGroupModel: vi.fn(),
    onUpdateGroupModel: vi.fn(),
    onDeleteGroupModel: vi.fn(),
    onToggleGroupModel: vi.fn(),
    ...overrides,
  };
}

function navButton(container: Element, child: 'models' | 'group'): HTMLButtonElement {
  return container.querySelector(
    `[data-testid="decision-container-tab-${child}"]`,
  ) as HTMLButtonElement;
}

describe('DecisionModelsContainer — vertical left nav + the two child panels', () => {
  it('A1 — renders exactly two nav items labelled Models and Group, in that order', () => {
    const { container } = render(<DecisionModelsContainer {...makeProps()} />);
    const nav = container.querySelector('[data-testid="decision-container-nav"]') as HTMLElement;
    const buttons = Array.from(nav.querySelectorAll('button'));
    expect(buttons.length).toBe(2);
    expect(buttons[0].textContent).toBe('Models');
    expect(buttons[1].textContent).toBe('Group');
  });

  it('A2 — the nav is the first element of the container and the panel follows it', () => {
    const { container } = render(<DecisionModelsContainer {...makeProps()} />);
    const root = container.querySelector('[data-testid="decision-models-container"]') as HTMLElement;
    const children = Array.from(root.children);
    expect(children.length).toBe(2);
    expect(children[0].getAttribute('data-testid')).toBe('decision-container-nav');
    expect(children[1].getAttribute('data-testid')).toBe('decision-container-panel');
  });

  it('A3 — the nav column is a vertical flex-col rail and carries no overflow-x-auto', () => {
    const { container } = render(<DecisionModelsContainer {...makeProps()} />);
    const nav = container.querySelector('[data-testid="decision-container-nav"]') as HTMLElement;
    expect(nav.className).toContain('flex-col');
    expect(nav.className).not.toContain('overflow-x-auto');
  });

  it('A4 — the nav buttons are not matched by the top-level tab-bar selector', () => {
    const { container } = render(<DecisionModelsContainer {...makeProps()} />);
    const topLevelTabs = container.querySelectorAll('div.flex.overflow-x-auto > button');
    expect(topLevelTabs.length).toBe(0);
    const modelsBtn = navButton(container, 'models');
    const groupBtn = navButton(container, 'group');
    expect(Array.from(topLevelTabs)).not.toContain(modelsBtn);
    expect(Array.from(topLevelTabs)).not.toContain(groupBtn);
  });

  it('A5 — the models child is the default: the models panel is mounted and the group panel is not', () => {
    const { container } = render(<DecisionModelsContainer {...makeProps()} />);
    expect(container.querySelector('[data-testid="decision-models-list"]')).not.toBeNull();
    expect(container.querySelector('[data-testid="group-models-list"]')).toBeNull();
  });

  it('A6 — clicking Group swaps the panel and the aria-current marker follows the click', () => {
    const { container } = render(<DecisionModelsContainer {...makeProps()} />);
    const modelsBtn = navButton(container, 'models');
    const groupBtn = navButton(container, 'group');
    expect(modelsBtn.getAttribute('aria-current')).toBe('true');
    expect(groupBtn.hasAttribute('aria-current')).toBe(false);

    fireEvent.click(groupBtn);

    expect(container.querySelector('[data-testid="group-models-list"]')).not.toBeNull();
    expect(container.querySelector('[data-testid="decision-models-list"]')).toBeNull();
    // The marker MOVES: the newly activated button carries it, the
    // previously active one loses the attribute entirely.
    expect(groupBtn.getAttribute('aria-current')).toBe('true');
    expect(modelsBtn.hasAttribute('aria-current')).toBe(false);
  });

  it('A7 — the active item carries the blue pair and the active-only aria-current; the inactive carries the gray pair', () => {
    const { container } = render(<DecisionModelsContainer {...makeProps()} />);
    const modelsBtn = navButton(container, 'models');
    const groupBtn = navButton(container, 'group');
    // Active — the top bar's pair translated to the LEFT edge.
    expect(modelsBtn.className).toContain('text-blue-400');
    expect(modelsBtn.className).toContain('border-l-blue-400');
    expect(modelsBtn.className).toContain('border-l-2');
    // Inactive.
    expect(groupBtn.className).toContain('text-gray-400');
    expect(groupBtn.className).toContain('hover:text-white');
    expect(groupBtn.className).toContain('border-l-transparent');
    // Exactly one item is active, and the inactive one has NO
    // aria-current attribute at all (never aria-current="false").
    expect(container.querySelectorAll('[aria-current="true"]').length).toBe(1);
    expect(groupBtn.hasAttribute('aria-current')).toBe(false);
    expect(groupBtn.getAttribute('aria-current')).not.toBe('false');
  });

  it('A8 — each nav button is a native button with type=button, not disabled and with no negative tabindex', () => {
    const { container } = render(<DecisionModelsContainer {...makeProps()} />);
    for (const btn of [navButton(container, 'models'), navButton(container, 'group')]) {
      expect(btn.tagName).toBe('BUTTON');
      expect(btn.getAttribute('type')).toBe('button');
      expect(btn.disabled).toBe(false);
      expect(btn.getAttribute('tabindex')).not.toBe('-1');
    }
  });

  it('A9 — each nav button carries a data-testid and an aria-label', () => {
    const { container } = render(<DecisionModelsContainer {...makeProps()} />);
    const modelsBtn = navButton(container, 'models');
    const groupBtn = navButton(container, 'group');
    expect(modelsBtn.getAttribute('data-testid')).toBe('decision-container-tab-models');
    expect(modelsBtn.getAttribute('aria-label')).toBe('Decision Models');
    expect(groupBtn.getAttribute('data-testid')).toBe('decision-container-tab-group');
    expect(groupBtn.getAttribute('aria-label')).toBe('Decision Groups');
  });

  it('A10 — no ARIA tab roles, no aria-selected and no roving tabindex appear in the container', () => {
    const { container } = render(<DecisionModelsContainer {...makeProps()} />);
    const root = container.querySelector('[data-testid="decision-models-container"]') as HTMLElement;
    expect(root.querySelectorAll('[role="tablist"]').length).toBe(0);
    expect(root.querySelectorAll('[role="tab"]').length).toBe(0);
    expect(root.querySelectorAll('[role="tabpanel"]').length).toBe(0);
    expect(root.querySelectorAll('[aria-selected]').length).toBe(0);
    expect(root.querySelectorAll('[tabindex="-1"]').length).toBe(0);
  });

  it('A11 — the models panel mounts the unchanged decision list (empty state + the add CTA)', () => {
    const { container } = render(<DecisionModelsContainer {...makeProps()} />);
    expect(container.textContent).toContain('No decision models configured');
    expect(container.textContent).toContain('Add Decision Model');
    expect(container.textContent).toContain('Decision Models');
  });

  it('A12 — switching to the group child unmounts the models panel (the in-flight probe panel is discarded)', () => {
    const { container } = render(
      <DecisionModelsContainer {...makeProps({ models: [makeDecisionRow()] })} />,
    );
    expect(container.querySelector('[data-testid="decision-model-row"]')).not.toBeNull();

    fireEvent.click(navButton(container, 'group'));

    // The models panel is GONE (mount/unmount — the same posture as the
    // top-level tab branch). The probe REQUEST is not aborted; only the
    // FE's rendering of its outcome is lost.
    expect(container.querySelector('[data-testid="decision-model-row"]')).toBeNull();
    expect(container.querySelector('[data-testid="decision-models-list"]')).toBeNull();
    expect(container.querySelector('[data-testid="group-models-list"]')).not.toBeNull();
  });

  it('A13 — the models panel props are passed through verbatim (the navigate-to-credentials affordance fires)', async () => {
    const onNavigateToCredentials = vi.fn();
    const { container } = render(
      <DecisionModelsContainer {...makeProps({ onNavigateToCredentials })} />,
    );

    // Open the decision-model form (the container is a pass-through,
    // not a filter — the form's own affordance reaches the callback).
    fireEvent.click(
      Array.from(container.querySelectorAll('button')).find((b) =>
        (b.textContent ?? '').includes('Add Decision Model'),
      ) as HTMLButtonElement,
    );
    await waitFor(() =>
      expect(container.querySelector('[data-testid="decision-model-form"]')).not.toBeNull(),
    );

    const manage = Array.from(container.querySelectorAll('button')).find((b) =>
      (b.textContent ?? '').includes('Manage Credentials'),
    );
    expect(manage).toBeDefined();
    fireEvent.click(manage as HTMLButtonElement);
    expect(onNavigateToCredentials).toHaveBeenCalledTimes(1);
  });
});
