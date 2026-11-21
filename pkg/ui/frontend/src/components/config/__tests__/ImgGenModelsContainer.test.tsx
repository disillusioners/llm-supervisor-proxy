import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent } from '@testing-library/preact';
import { ImgGenModelsContainer } from '../ImgGenModelsContainer';
import { splitGroupsByLane } from '../groupLane';
import type { Model } from '../../../types';

// The IMAGE-GEN lane's container — the shared sub-tab core's third
// instance (the decision lane's is the first, the text lane's the
// second).
//
// It is the SAME contract as the decision container's A-suite, in the
// imggen namespace:
//
//   - native <button type="button">;
//   - NO role="tablist" / role="tab" / role="tabpanel", NO
//     aria-selected, NO roving tabindex;
//   - aria-current="true" on the ACTIVE button ONLY;
//   - the nav stays OUTSIDE div.flex.overflow-x-auto.
//
// The container is UNCONTROLLED (like the decision lane's): the
// image-gen lane has no cross-container navigation to command.
//
// The image-gen models panel probes the network, so the fetch boundary
// is stubbed (the house pattern — DecisionModelsTab.decisionTest).

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
    id: 'img-1',
    name: 'Image One',
    enabled: true,
    fallback_chain: [],
    kind: 'image-gen',
    ...overrides,
  };
}

// A group row — the members decide the lane (the fold).
function makeGroup(id: string, members: { model_id: string; weight: number }[]): Model {
  return {
    id,
    name: id,
    enabled: true,
    fallback_chain: [],
    kind: 'group',
    members,
    shadow_model_id: '',
  } as Model;
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
    `[data-testid="imggen-container-tab-${child}"]`,
  ) as HTMLButtonElement;
}

describe('ImgGenModelsContainer — vertical left nav + the two child panels', () => {
  it('IA1 — renders exactly two nav items labelled Models and Group, in that order', () => {
    const { container } = render(<ImgGenModelsContainer {...makeProps()} />);
    const nav = container.querySelector('[data-testid="imggen-container-nav"]') as HTMLElement;
    const buttons = Array.from(nav.querySelectorAll('button'));
    expect(buttons.length).toBe(2);
    expect(buttons[0].textContent).toBe('Models');
    expect(buttons[1].textContent).toBe('Group');
  });

  it('IA2 — the nav is the first element of the container and the panel follows it', () => {
    const { container } = render(<ImgGenModelsContainer {...makeProps()} />);
    const root = container.querySelector('[data-testid="imggen-models-container"]') as HTMLElement;
    const children = Array.from(root.children);
    expect(children.length).toBe(2);
    expect(children[0].getAttribute('data-testid')).toBe('imggen-container-nav');
    expect(children[1].getAttribute('data-testid')).toBe('imggen-container-panel');
  });

  it('IA3 — the nav column is a vertical flex-col rail and carries no overflow-x-auto', () => {
    const { container } = render(<ImgGenModelsContainer {...makeProps()} />);
    const nav = container.querySelector('[data-testid="imggen-container-nav"]') as HTMLElement;
    expect(nav.className).toContain('flex-col');
    expect(nav.className).not.toContain('overflow-x-auto');
  });

  it('IA4 — the nav buttons are not matched by the top-level tab-bar selector', () => {
    const { container } = render(<ImgGenModelsContainer {...makeProps()} />);
    const topLevelTabs = container.querySelectorAll('div.flex.overflow-x-auto > button');
    expect(topLevelTabs.length).toBe(0);
    const modelsBtn = navButton(container, 'models');
    const groupBtn = navButton(container, 'group');
    expect(Array.from(topLevelTabs)).not.toContain(modelsBtn);
    expect(Array.from(topLevelTabs)).not.toContain(groupBtn);
  });

  it('IA5 — the models child is the default: the image-gen models list is mounted and the group list is not', () => {
    const { container } = render(<ImgGenModelsContainer {...makeProps()} />);
    expect(container.querySelector('[data-testid="imggen-models-list"]')).not.toBeNull();
    expect(container.querySelector('[data-testid="group-models-list"]')).toBeNull();
  });

  it('IA6 — clicking Group swaps the panel and the aria-current marker follows the click', () => {
    const { container } = render(<ImgGenModelsContainer {...makeProps()} />);
    const modelsBtn = navButton(container, 'models');
    const groupBtn = navButton(container, 'group');
    expect(modelsBtn.getAttribute('aria-current')).toBe('true');
    expect(groupBtn.hasAttribute('aria-current')).toBe(false);

    fireEvent.click(groupBtn);

    expect(container.querySelector('[data-testid="group-models-list"]')).not.toBeNull();
    expect(container.querySelector('[data-testid="imggen-models-list"]')).toBeNull();
    expect(groupBtn.getAttribute('aria-current')).toBe('true');
    expect(modelsBtn.hasAttribute('aria-current')).toBe(false);
  });

  it('IA7 — the active item carries the blue pair and the active-only aria-current; the inactive carries the gray pair', () => {
    const { container } = render(<ImgGenModelsContainer {...makeProps()} />);
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

  it('IA8 — each nav button is a native button with type=button, not disabled and with no negative tabindex', () => {
    const { container } = render(<ImgGenModelsContainer {...makeProps()} />);
    for (const btn of [navButton(container, 'models'), navButton(container, 'group')]) {
      expect(btn.tagName).toBe('BUTTON');
      expect(btn.getAttribute('type')).toBe('button');
      expect(btn.disabled).toBe(false);
      expect(btn.getAttribute('tabindex')).not.toBe('-1');
    }
  });

  it('IA9 — each nav button carries a data-testid and an aria-label in the imggen namespace', () => {
    const { container } = render(<ImgGenModelsContainer {...makeProps()} />);
    const nav = container.querySelector('[data-testid="imggen-container-nav"]') as HTMLElement;
    expect(nav.getAttribute('aria-label')).toBe('Image-Gen Models sections');
    const modelsBtn = navButton(container, 'models');
    const groupBtn = navButton(container, 'group');
    expect(modelsBtn.getAttribute('data-testid')).toBe('imggen-container-tab-models');
    expect(modelsBtn.getAttribute('aria-label')).toBe('Image-Gen Models');
    expect(groupBtn.getAttribute('data-testid')).toBe('imggen-container-tab-group');
    expect(groupBtn.getAttribute('aria-label')).toBe('Image-Gen Groups');
  });

  it('IA10 — no ARIA tab roles, no aria-selected and no roving tabindex appear in the container', () => {
    const { container } = render(<ImgGenModelsContainer {...makeProps()} />);
    const root = container.querySelector('[data-testid="imggen-models-container"]') as HTMLElement;
    expect(root.querySelectorAll('[role="tablist"]').length).toBe(0);
    expect(root.querySelectorAll('[role="tab"]').length).toBe(0);
    expect(root.querySelectorAll('[role="tabpanel"]').length).toBe(0);
    expect(root.querySelectorAll('[aria-selected]').length).toBe(0);
    expect(root.querySelectorAll('[tabindex="-1"]').length).toBe(0);
  });

  it('IA11 — the models panel mounts the unchanged image-gen list (empty state + the add CTA)', () => {
    const { container } = render(<ImgGenModelsContainer {...makeProps()} />);
    expect(container.textContent).toContain('No image-gen models configured');
    expect(container.textContent).toContain('Add Image-Gen Model');
    expect(container.textContent).toContain('Image-Gen Models');
  });

  it('IA12 — switching to the group child unmounts the models panel (the panel\'s local state is discarded)', () => {
    const { container } = render(
      <ImgGenModelsContainer {...makeProps({ models: [makeModel()] })} />,
    );
    expect(container.querySelector('[data-testid="imggen-model-row"]')).not.toBeNull();

    fireEvent.click(navButton(container, 'group'));

    expect(container.querySelector('[data-testid="imggen-model-row"]')).toBeNull();
    expect(container.querySelector('[data-testid="imggen-models-list"]')).toBeNull();
    expect(container.querySelector('[data-testid="group-models-list"]')).not.toBeNull();
  });

  it('IA13 — the group child lists ONLY the image-gen bucket: a mixed split leaves the chat and unknown rows elsewhere', () => {
    // The MIXED fixture: a chat group, an image-gen group and a group
    // whose surviving member set is empty (unknown lane).
    const kindIndex = new Map<string, string>([
      ['chat-1', 'chat'],
      ['img-1', 'image-gen'],
    ]);
    const chatGroup = makeGroup('g-chat', [{ model_id: 'chat-1', weight: 1 }]);
    const imgGroup = makeGroup('g-img', [{ model_id: 'img-1', weight: 1 }]);
    const unknownGroup = makeGroup('g-unknown', [{ model_id: 'ghost', weight: 1 }]);
    const split = splitGroupsByLane([chatGroup, imgGroup, unknownGroup], kindIndex);

    const { container } = render(
      <ImgGenModelsContainer
        {...makeProps({
          groups: split['image-gen'],
          laneModels: [makeModel({ id: 'img-1' })],
          kindIndex,
        })}
      />,
    );

    fireEvent.click(navButton(container, 'group'));

    // The image-gen group is HERE...
    const rows = Array.from(container.querySelectorAll('[data-testid="group-model-row"]'));
    expect(rows.length).toBe(1);
    expect(rows[0].getAttribute('data-model-id')).toBe('g-img');
    // ...and the chat + unknown rows are NOT (they live in the text
    // container's group child — the single-home invariant).
    expect(container.textContent).not.toContain('g-chat');
    expect(container.textContent).not.toContain('g-unknown');
    // The lane's own surface (its heading + empty-state copy) is the
    // image-gen one.
    expect(container.textContent).toContain('Image-Gen Groups');
  });
});
