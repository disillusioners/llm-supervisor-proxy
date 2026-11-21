import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, waitFor } from '@testing-library/preact';
import { TextModelsContainer } from '../TextModelsContainer';
import type { Model } from '../../../types';

// The TEXT lane's container — the shared sub-tab core's second
// instance (the decision lane's is the first).
//
// It is the SAME contract as the decision container's A-suite, in the
// text namespace:
//
//   - native <button type="button"> — Tab reaches each button in DOM
//     order, Enter/Space activate natively (happy-dom does NOT
//     synthesise Enter → click, so the test pins the PRECONDITIONS and
//     the observable consequence, not a synthesised keypress);
//   - NO role="tablist" / role="tab" / role="tabpanel", NO
//     aria-selected, NO roving tabindex, NO arrow-key handling;
//   - aria-current="true" on the ACTIVE button ONLY — the inactive
//     button OMITS the attribute entirely (never aria-current="false");
//   - the nav stays OUTSIDE div.flex.overflow-x-auto, so the top-level
//     tab selector cannot see it.
//
// The text container is the CONTROLLED one: the parent owns the child
// state (the one-click wrap flow must be able to COMMAND the container
// onto its group child while the container stays mounted). The W cases
// pin that seam; the TA cases pin the shared shape.

const realFetch = globalThis.fetch;

beforeEach(() => {
  (globalThis as { fetch: typeof fetch }).fetch = vi.fn(async () =>
    new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } }),
  ) as unknown as typeof fetch;
  document.body.innerHTML = '';
});

afterEach(() => {
  (globalThis as { fetch: typeof fetch }).fetch = realFetch;
  cleanup();
  vi.clearAllMocks();
});

function makeModel(overrides: Partial<Model> = {}): Model {
  return {
    id: 'chat-1',
    name: 'Chat One',
    enabled: true,
    fallback_chain: [],
    ...overrides,
  };
}

// The models panel arrives as a NODE (the parent mounts it — see
// TextModelsContainer's header note). The stub carries a testid so the
// suite can prove the container renders the parent's node verbatim.
function modelsPanelNode() {
  return <div data-testid="stub-models-panel">Available Models</div>;
}

function makeProps(overrides: Record<string, unknown> = {}) {
  return {
    modelsPanel: modelsPanelNode(),
    groups: [] as Model[],
    laneModels: [] as Model[],
    setStatus: vi.fn(),
    onAddGroupModel: vi.fn(),
    onUpdateGroupModel: vi.fn(),
    onDeleteGroupModel: vi.fn(),
    onToggleGroupModel: vi.fn(),
    ...overrides,
  };
}

function navButton(container: Element, child: 'models' | 'group'): HTMLButtonElement {
  return container.querySelector(
    `[data-testid="text-container-tab-${child}"]`,
  ) as HTMLButtonElement;
}

describe('TextModelsContainer — vertical left nav + the two child panels', () => {
  it('TA1 — renders exactly two nav items labelled Models and Group, in that order', () => {
    const { container } = render(<TextModelsContainer {...makeProps()} />);
    const nav = container.querySelector('[data-testid="text-container-nav"]') as HTMLElement;
    const buttons = Array.from(nav.querySelectorAll('button'));
    expect(buttons.length).toBe(2);
    expect(buttons[0].textContent).toBe('Models');
    expect(buttons[1].textContent).toBe('Group');
  });

  it('TA2 — the nav is the first element of the container and the panel follows it', () => {
    const { container } = render(<TextModelsContainer {...makeProps()} />);
    const root = container.querySelector('[data-testid="text-models-container"]') as HTMLElement;
    const children = Array.from(root.children);
    expect(children.length).toBe(2);
    expect(children[0].getAttribute('data-testid')).toBe('text-container-nav');
    expect(children[1].getAttribute('data-testid')).toBe('text-container-panel');
  });

  it('TA3 — the nav column is a vertical flex-col rail and carries no overflow-x-auto', () => {
    const { container } = render(<TextModelsContainer {...makeProps()} />);
    const nav = container.querySelector('[data-testid="text-container-nav"]') as HTMLElement;
    expect(nav.className).toContain('flex-col');
    expect(nav.className).not.toContain('overflow-x-auto');
  });

  it('TA4 — the nav buttons are not matched by the top-level tab-bar selector', () => {
    const { container } = render(<TextModelsContainer {...makeProps()} />);
    const topLevelTabs = container.querySelectorAll('div.flex.overflow-x-auto > button');
    expect(topLevelTabs.length).toBe(0);
    const modelsBtn = navButton(container, 'models');
    const groupBtn = navButton(container, 'group');
    expect(Array.from(topLevelTabs)).not.toContain(modelsBtn);
    expect(Array.from(topLevelTabs)).not.toContain(groupBtn);
  });

  it('TA5 — the models child is the default: the models panel is mounted and the group panel is not', () => {
    const { container } = render(<TextModelsContainer {...makeProps()} />);
    expect(container.querySelector('[data-testid="stub-models-panel"]')).not.toBeNull();
    expect(container.querySelector('[data-testid="group-models-list"]')).toBeNull();
  });

  it('TA6 — clicking Group swaps the panel and the aria-current marker follows the click', () => {
    const { container } = render(<TextModelsContainer {...makeProps()} />);
    const modelsBtn = navButton(container, 'models');
    const groupBtn = navButton(container, 'group');
    expect(modelsBtn.getAttribute('aria-current')).toBe('true');
    expect(groupBtn.hasAttribute('aria-current')).toBe(false);

    fireEvent.click(groupBtn);

    // The group child mounts the CHAT lane's group surface.
    expect(container.querySelector('[data-testid="group-models-list"]')).not.toBeNull();
    expect(container.querySelector('[data-testid="stub-models-panel"]')).toBeNull();
    // The marker MOVES: the newly activated button carries it, the
    // previously active one loses the attribute entirely.
    expect(groupBtn.getAttribute('aria-current')).toBe('true');
    expect(modelsBtn.hasAttribute('aria-current')).toBe(false);
  });

  it('TA7 — the active item carries the blue pair and the active-only aria-current; the inactive carries the gray pair', () => {
    const { container } = render(<TextModelsContainer {...makeProps()} />);
    const modelsBtn = navButton(container, 'models');
    const groupBtn = navButton(container, 'group');
    expect(modelsBtn.className).toContain('text-blue-400');
    expect(modelsBtn.className).toContain('border-l-blue-400');
    expect(modelsBtn.className).toContain('border-l-2');
    expect(groupBtn.className).toContain('text-gray-400');
    expect(groupBtn.className).toContain('hover:text-white');
    expect(groupBtn.className).toContain('border-l-transparent');
    expect(container.querySelectorAll('[aria-current="true"]').length).toBe(1);
    expect(groupBtn.hasAttribute('aria-current')).toBe(false);
    expect(groupBtn.getAttribute('aria-current')).not.toBe('false');
  });

  it('TA8 — each nav button is a native button with type=button, not disabled and with no negative tabindex', () => {
    const { container } = render(<TextModelsContainer {...makeProps()} />);
    for (const btn of [navButton(container, 'models'), navButton(container, 'group')]) {
      expect(btn.tagName).toBe('BUTTON');
      expect(btn.getAttribute('type')).toBe('button');
      expect(btn.disabled).toBe(false);
      expect(btn.getAttribute('tabindex')).not.toBe('-1');
    }
  });

  it('TA9 — each nav button carries a data-testid and an aria-label in the text namespace', () => {
    const { container } = render(<TextModelsContainer {...makeProps()} />);
    const nav = container.querySelector('[data-testid="text-container-nav"]') as HTMLElement;
    expect(nav.getAttribute('aria-label')).toBe('Text Models sections');
    const modelsBtn = navButton(container, 'models');
    const groupBtn = navButton(container, 'group');
    expect(modelsBtn.getAttribute('data-testid')).toBe('text-container-tab-models');
    expect(modelsBtn.getAttribute('aria-label')).toBe('Text Models');
    expect(groupBtn.getAttribute('data-testid')).toBe('text-container-tab-group');
    expect(groupBtn.getAttribute('aria-label')).toBe('Group Models');
  });

  it('TA10 — no ARIA tab roles, no aria-selected and no roving tabindex appear in the container', () => {
    const { container } = render(<TextModelsContainer {...makeProps()} />);
    const root = container.querySelector('[data-testid="text-models-container"]') as HTMLElement;
    expect(root.querySelectorAll('[role="tablist"]').length).toBe(0);
    expect(root.querySelectorAll('[role="tab"]').length).toBe(0);
    expect(root.querySelectorAll('[role="tabpanel"]').length).toBe(0);
    expect(root.querySelectorAll('[aria-selected]').length).toBe(0);
    expect(root.querySelectorAll('[tabindex="-1"]').length).toBe(0);
  });

  it('TA11 — the models panel renders the parent\'s node verbatim', () => {
    const { container } = render(
      <TextModelsContainer {...makeProps({ modelsPanel: modelsPanelNode() })} />,
    );
    const panel = container.querySelector('[data-testid="stub-models-panel"]');
    expect(panel).not.toBeNull();
    expect(panel?.textContent).toBe('Available Models');
    // The node is rendered INSIDE the container's panel slot.
    const panelSlot = container.querySelector('[data-testid="text-container-panel"]');
    expect(panelSlot?.contains(panel as Node)).toBe(true);
  });

  it('TA12 — switching to the group child unmounts the models panel (the panel\'s local state is discarded)', () => {
    const { container } = render(<TextModelsContainer {...makeProps()} />);
    expect(container.querySelector('[data-testid="stub-models-panel"]')).not.toBeNull();

    fireEvent.click(navButton(container, 'group'));

    expect(container.querySelector('[data-testid="stub-models-panel"]')).toBeNull();
    expect(container.querySelector('[data-testid="group-models-list"]')).not.toBeNull();
  });

  it('TA13 — the group child mounts the CHAT lane\'s group surface with the parent\'s groups', () => {
    const group = makeModel({ id: 'g-chat', name: 'Chat Pool', kind: 'group' });
    const { container } = render(
      <TextModelsContainer {...makeProps({ groups: [group] })} />,
    );

    fireEvent.click(navButton(container, 'group'));

    // The chat lane's OWN surface (its heading + the group's row) — the
    // container did not fork the panel, it mounted the lane's panel.
    expect(container.textContent).toContain('Group Models');
    expect(container.querySelector('[data-testid="group-model-row"]')).not.toBeNull();
  });

  it('W1 — the controlled child: the parent commands the group child and the wrap pre-fill fires exactly once', async () => {
    const onChildChange = vi.fn();
    const onWrapConsumed = vi.fn();
    const wrapTarget = makeModel({ id: 'chat-wrap', name: 'Chat Wrap' });

    const { container } = render(
      <TextModelsContainer
        {...makeProps({
          activeChild: 'group',
          onChildChange,
          wrapPreloadModel: wrapTarget,
          onWrapConsumed,
        })}
      />,
    );

    // The parent commanded the group child: the models panel is GONE
    // and the group surface is mounted (the wrap pre-fill opens the
    // add-form, so the group's list is replaced by the form).
    expect(container.querySelector('[data-testid="stub-models-panel"]')).toBeNull();
    expect(navButton(container, 'group').getAttribute('aria-current')).toBe('true');

    // The wrap pre-fill fires EXACTLY ONCE: the panel consumes the
    // signal when it mounts and never again.
    await waitFor(() => expect(onWrapConsumed).toHaveBeenCalledTimes(1));
    // ...and the pre-filled add-form is open for the wrapped model.
    await waitFor(() => {
      expect(container.querySelector('[data-testid="group-form-id-input"]')).not.toBeNull();
    });
    expect(
      (container.querySelector('[data-testid="group-form-id-input"]') as HTMLInputElement).value,
    ).toBe('chat-wrap[chat-wrap]');
    expect(onWrapConsumed).toHaveBeenCalledTimes(1);
  });

  it('W2 — the controlled child follows the PARENT: a nav click reports through onChildChange and the container does not move on its own', () => {
    const onChildChange = vi.fn();
    const { container } = render(
      <TextModelsContainer {...makeProps({ activeChild: 'models', onChildChange })} />,
    );

    fireEvent.click(navButton(container, 'group'));

    // The click is REPORTED ...
    expect(onChildChange).toHaveBeenCalledTimes(1);
    expect(onChildChange).toHaveBeenCalledWith('group');
    // ... but the container did NOT move on its own: the parent's
    // activeChild is the authority (a controlled container never
    // self-moves).
    expect(navButton(container, 'models').getAttribute('aria-current')).toBe('true');
    expect(container.querySelector('[data-testid="stub-models-panel"]')).not.toBeNull();
  });

  it('W3 — switching the controlled child unmounts the models panel (its local state goes with it)', () => {
    const { container, rerender } = render(
      <TextModelsContainer {...makeProps({ activeChild: 'models', onChildChange: vi.fn() })} />,
    );
    expect(container.querySelector('[data-testid="stub-models-panel"]')).not.toBeNull();

    rerender(
      <TextModelsContainer {...makeProps({ activeChild: 'group', onChildChange: vi.fn() })} />,
    );

    expect(container.querySelector('[data-testid="stub-models-panel"]')).toBeNull();
    expect(container.querySelector('[data-testid="group-models-list"]')).not.toBeNull();
  });
});
