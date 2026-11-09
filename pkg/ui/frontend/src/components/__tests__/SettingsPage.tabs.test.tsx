import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, waitFor } from '@testing-library/preact';
import { SettingsPage } from '../SettingsPage';
import type { Model, AppConfig, ApiToken, ImgGenModel } from '../../types';

// Stub the credentials fetch the SettingsPage runs on mount, so the test
// doesn't hit a network. The image-gen list is also a fetch — we stub
// the global fetch and let the relevant tabs degrade gracefully (the
// models list mounts as empty for the chat tab; the ImgGenModelsTab
// sees an empty list and renders the empty-state copy).
const realFetch = globalThis.fetch;
beforeEach(() => {
  (globalThis as { fetch: typeof fetch }).fetch = vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : (input as Request).url;
    if (url.includes('/credentials')) {
      return new Response(JSON.stringify([]), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }
    if (url.includes('/models')) {
      return new Response(JSON.stringify([]), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }
    return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } });
  }) as unknown as typeof fetch;
});
afterEach(() => {
  (globalThis as { fetch: typeof fetch }).fetch = realFetch;
  cleanup();
});

// Minimal prop builder — every callback is a vi.fn() so a render is
// safe even though the form, list, and modal paths exist.
function makeProps(overrides: Partial<{
  models: Model[];
  imgGenModels: ImgGenModel[];
  decisionModels: Model[];
  groupModels: Model[];
  config: AppConfig | null;
}> = {}) {
  const baseConfig: AppConfig = {
    version: '0.0.0',
    port: 8089,
    idle_timeout: '',
    stream_deadline: '',
    max_generation_time: '',
    race_retry_enabled: false,
    race_parallel_on_idle: true,
    race_max_parallel: 3,
    race_max_buffer_bytes: 5242880,
    sticky_last_success_enabled: false,
    loop_detection: {
      enabled: false,
      shadow_mode: false,
      message_window: 0,
      action_window: 0,
      exact_match_count: 0,
      similarity_threshold: 0,
      min_tokens_for_simhash: 0,
      action_repeat_count: 0,
      oscillation_count: 0,
      min_tokens_for_analysis: 0,
      thinking_min_tokens: 0,
      trigram_threshold: 0,
      max_cycle_length: 0,
      reasoning_model_patterns: [],
      reasoning_trigram_threshold: 0,
    },
    tool_repair: {
      enabled: false,
      strategies: [],
      max_arguments_size: 0,
      max_tool_calls_per_response: 0,
      log_original: false,
      log_repaired: false,
      fixer_model: '',
      fixer_timeout: 0,
    },
    ultimate_model: { model_id: '', max_hash: 100 },
    updated_at: '',
  };
  return {
    config: baseConfig,
    onUpdateConfig: vi.fn().mockResolvedValue({ ...baseConfig, restart_required: false }),
    models: [] as Model[],
    onAddModel: vi.fn(),
    onUpdateModel: vi.fn(),
    onDeleteModel: vi.fn(),
    // copy-model commission — the chat tab's Copy action.
    onCopyModel: vi.fn(),
    imgGenModels: [] as ImgGenModel[],
    onAddImgGenModel: vi.fn(),
    onUpdateImgGenModel: vi.fn(),
    onDeleteImgGenModel: vi.fn(),
    decisionModels: [] as Model[],
    onAddDecisionModel: vi.fn(),
    onUpdateDecisionModel: vi.fn(),
    onDeleteDecisionModel: vi.fn(),
    // load-balance-groups (plan §6.1 / P3.6) — group tab props, same
    // shape as the decision trio.
    groupModels: [] as Model[],
    onAddGroupModel: vi.fn(),
    onUpdateGroupModel: vi.fn(),
    onDeleteGroupModel: vi.fn(),
    tokens: [] as ApiToken[],
    onCreateToken: vi.fn(),
    onDeleteToken: vi.fn(),
    onUpdateTokenPermission: vi.fn(),
    onRefetchTokens: vi.fn(),
    ...overrides,
  };
}

describe('SettingsPage — tab mechanism (ImgGen Commission C-17, T2.5.1; Decision Commission P3.5)', () => {
  it('renders the 9-tab bar with the 🖼️ ImgGen Models button at slot 3', () => {
    const { container } = render(<SettingsPage {...makeProps()} />);

    // Find the new tab button by its stable data-tab-id (added in
    // SettingsPage.tsx so the test has a hook).
    const imggenTab = container.querySelector('[data-tab-id="imggen_models"]') as HTMLButtonElement;
    expect(imggenTab).not.toBeNull();
    // Label is character-exact (fe-spec C-01).
    expect(imggenTab.textContent).toContain('🖼️');
    expect(imggenTab.textContent).toContain('ImgGen Models');

    // Position check (Decision Commission P3.5 / plan §6.1): the
    // ImgGen tab is now followed by the Decision Models tab (added
    // in P3.5 at slot 4 of 10), NOT by Credentials anymore. The tab
    // bar is a flex container; we walk the DOM.
    const tabs = Array.from(
      container.querySelectorAll('div.flex.overflow-x-auto > button'),
    ) as HTMLButtonElement[];
    const imggenIdx = tabs.findIndex((b) => b.getAttribute('data-tab-id') === 'imggen_models');
    expect(imggenIdx).toBeGreaterThan(0);
    expect(tabs[imggenIdx - 1].textContent).toContain('Models');
    // The button immediately following imggen is now the Decision Models
    // tab (P3.5 / plan §6.1 slot 4 of 10). The Credentials tab is one
    // further down.
    expect(tabs[imggenIdx + 1].textContent).toContain('Decision Models');
  });

  it('Decision Commission P3.5 — renders the 🎯 Decision Models button at slot 4 (Q19 default)', () => {
    // P3.5 acceptance / plan §6.1: the new Decision Models tab is
    // wired at slot 4 of 10, between ImgGen Models and Credentials,
    // with the Q19 default emoji 🎯 (target/bullseye) and the
    // recommended-default label "Decision Models".
    const { container } = render(<SettingsPage {...makeProps()} />);

    const decisionTab = container.querySelector('[data-tab-id="decision_models"]') as HTMLButtonElement;
    expect(decisionTab).not.toBeNull();
    expect(decisionTab.textContent).toContain('🎯');
    expect(decisionTab.textContent).toContain('Decision Models');

    // Position check (P3.5 / plan §6.1 slot 4 of 10): the button
    // immediately preceding the new Decision tab is the ImgGen tab,
    // and the button immediately following is the Credentials tab.
    const tabs = Array.from(
      container.querySelectorAll('div.flex.overflow-x-auto > button'),
    ) as HTMLButtonElement[];
    const decisionIdx = tabs.findIndex((b) => b.getAttribute('data-tab-id') === 'decision_models');
    expect(decisionIdx).toBeGreaterThan(0);
    expect(tabs[decisionIdx - 1].textContent).toContain('ImgGen Models');
    expect(tabs[decisionIdx + 1].textContent).toContain('Credentials');
  });

  it('clicking the 🎯 Decision Models tab mounts the DecisionModelsTab empty state (P3.5)', async () => {
    const { container } = render(<SettingsPage {...makeProps()} />);

    // Default active tab is 'proxy' — the DecisionModelsTab content
    // markers are absent. The tab BUTTON is always present (so the
    // assertion below uses the empty-state copy "No decision models
    // configured" and the "Add Decision Model" CTA — neither renders
    // until the user clicks the Decision tab).
    expect(container.textContent).not.toContain('No decision models configured');

    // Click the new tab.
    const decisionTab = container.querySelector('[data-tab-id="decision_models"]') as HTMLButtonElement;
    fireEvent.click(decisionTab);

    // The DecisionModelsTab mounts; the empty-state copy proves it.
    await waitFor(() => {
      expect(container.textContent).toContain('No decision models configured');
    });
    // The "Add Decision Model" button is the spec's binding label.
    expect(container.textContent).toContain('Add Decision Model');
  });

  it('clicking back to Models tab hides the DecisionModelsTab content', async () => {
    const { container } = render(<SettingsPage {...makeProps()} />);

    // Go to Decision first.
    fireEvent.click(container.querySelector('[data-tab-id="decision_models"]') as HTMLButtonElement);
    await waitFor(() => expect(container.textContent).toContain('No decision models configured'));

    // Now go back to Models.
    const modelsTab = Array.from(container.querySelectorAll('div.flex.overflow-x-auto > button'))
      .find((b) => b.textContent && b.textContent.trim() === 'Models') as HTMLButtonElement;
    expect(modelsTab).toBeDefined();
    fireEvent.click(modelsTab);

    await waitFor(() => {
      // The chat tab renders the spec's chrome header "Available Models".
      expect(container.textContent).toContain('Available Models');
      // The Decision tab's empty state is gone.
      expect(container.textContent).not.toContain('No decision models configured');
    });
  });

  it('clicking the 🖼️ ImgGen Models tab mounts the ImgGenModelsTab empty state (C-17)', async () => {
    const { container } = render(<SettingsPage {...makeProps()} />);

    // Default active tab is 'proxy' — neither Image-Gen Models nor
    // Available Models chrome is in the DOM yet.
    expect(container.textContent).not.toContain('Image-Gen Models');

    // Click the new tab.
    const imggenTab = container.querySelector('[data-tab-id="imggen_models"]') as HTMLButtonElement;
    fireEvent.click(imggenTab);

    // The ImgGenModelsTab mounts; the empty-state copy proves it.
    await waitFor(() => {
      expect(container.textContent).toContain('Image-Gen Models');
    });
    expect(container.textContent).toContain('No image-gen models configured');
    // The "Add Image-Gen Model" button is the spec's binding label.
    expect(container.textContent).toContain('Add Image-Gen Model');
  });

  it('clicking back to Models tab hides the ImgGenModelsTab content', async () => {
    const { container } = render(<SettingsPage {...makeProps()} />);

    // Go to ImgGen first.
    fireEvent.click(container.querySelector('[data-tab-id="imggen_models"]') as HTMLButtonElement);
    await waitFor(() => expect(container.textContent).toContain('Image-Gen Models'));

    // Now go back to Models.
    const modelsTab = Array.from(container.querySelectorAll('div.flex.overflow-x-auto > button'))
      .find((b) => b.textContent && b.textContent.trim() === 'Models') as HTMLButtonElement;
    expect(modelsTab).toBeDefined();
    fireEvent.click(modelsTab);

    await waitFor(() => {
      // The chat tab renders the spec's chrome header "Available Models".
      expect(container.textContent).toContain('Available Models');
      // The ImgGen tab's empty state is gone.
      expect(container.textContent).not.toContain('No image-gen models configured');
    });
  });

  it('A11Y-4 — emoji span carries aria-hidden="true"', () => {
    const { container } = render(<SettingsPage {...makeProps()} />);
    // Both the ImgGen and Decision Commission emojis carry aria-hidden.
    // ImgGen (🖼️) was the first place we put aria-hidden on an emoji;
    // Decision Commission 2026-10-15 (Q19) extends the pattern with 🎯.
    const imggenEmoji = container.querySelector('[data-tab-id="imggen_models"] span[aria-hidden="true"]');
    expect(imggenEmoji).not.toBeNull();
    const decisionEmoji = container.querySelector('[data-tab-id="decision_models"] span[aria-hidden="true"]');
    expect(decisionEmoji).not.toBeNull();
  });

  it('A11Y-5 — every tab button has type="button" (Preact best practice)', () => {
    const { container } = render(<SettingsPage {...makeProps()} />);
    const tabs = container.querySelectorAll('div.flex.overflow-x-auto > button');
    // Every tab in the bar must be type="button" so an accidental
    // Enter inside a form does not submit it.
    tabs.forEach((tab) => {
      expect((tab as HTMLButtonElement).type, 'tab button should be type="button"').toBe('button');
    });
  });

  it('load-balance-groups P3.6 — renders the 🔀 Group Models button as the 11th (last) tab', () => {
    // Plan §6.1 / Q10: label "Group Models", glyph 🔀, data-tab-id
    // "group_models". Positioned as the 11th tab (last, after MCP
    // Servers) so the Decision commission's pinned Decision→Credentials
    // adjacency test (above) is preserved.
    const { container } = render(<SettingsPage {...makeProps()} />);

    const groupTab = container.querySelector('[data-tab-id="group_models"]') as HTMLButtonElement;
    expect(groupTab).not.toBeNull();
    expect(groupTab.textContent).toContain('🔀');
    expect(groupTab.textContent).toContain('Group Models');
    expect(groupTab.getAttribute('aria-label')).toBe('Group Models');

    // Position: 11th of 11 — the button immediately preceding it is
    // MCP Servers; nothing follows it.
    const tabs = Array.from(
      container.querySelectorAll('div.flex.overflow-x-auto > button'),
    ) as HTMLButtonElement[];
    expect(tabs.length).toBe(11);
    const groupIdx = tabs.findIndex((b) => b.getAttribute('data-tab-id') === 'group_models');
    expect(groupIdx).toBe(10);
    expect(tabs[groupIdx - 1].textContent).toContain('MCP Servers');
    // The Decision→Credentials adjacency survives (P3.6 constraint).
    const decisionIdx = tabs.findIndex((b) => b.getAttribute('data-tab-id') === 'decision_models');
    expect(tabs[decisionIdx + 1].textContent).toContain('Credentials');
  });

  it('load-balance-groups P3.6 — clicking the 🔀 Group Models tab mounts the GroupModelsTab empty state', async () => {
    const { container } = render(<SettingsPage {...makeProps()} />);

    // Default active tab is 'proxy' — the GroupModelsTab content
    // markers are absent until the tab is clicked.
    expect(container.textContent).not.toContain('No group models configured');

    fireEvent.click(container.querySelector('[data-tab-id="group_models"]') as HTMLButtonElement);

    await waitFor(() => {
      expect(container.textContent).toContain('No group models configured');
    });
    // The "Add Group Model" button is the binding CTA label.
    expect(container.textContent).toContain('Add Group Model');
  });

  it('load-balance-groups P3.6 — clicking back to Models tab hides the GroupModelsTab content', async () => {
    const { container } = render(<SettingsPage {...makeProps()} />);

    fireEvent.click(container.querySelector('[data-tab-id="group_models"]') as HTMLButtonElement);
    await waitFor(() => expect(container.textContent).toContain('No group models configured'));

    const modelsTab = Array.from(container.querySelectorAll('div.flex.overflow-x-auto > button'))
      .find((b) => b.textContent && b.textContent.trim() === 'Models') as HTMLButtonElement;
    expect(modelsTab).toBeDefined();
    fireEvent.click(modelsTab);

    await waitFor(() => {
      expect(container.textContent).toContain('Available Models');
      expect(container.textContent).not.toContain('No group models configured');
    });
  });

  it('load-balance-groups P3.6 — A11Y: the 🔀 emoji span carries aria-hidden="true"', () => {
    const { container } = render(<SettingsPage {...makeProps()} />);
    const groupEmoji = container.querySelector('[data-tab-id="group_models"] span[aria-hidden="true"]');
    expect(groupEmoji).not.toBeNull();
    expect((groupEmoji as HTMLElement).textContent).toBe('🔀');
  });
});

// ─── sticky last-success ordering (FE delta, design §5.3) ───────────────────

function findStickyToggle(container: Element): HTMLButtonElement {
  const label = Array.from(container.querySelectorAll('label')).find(
    (l) => (l.textContent || '').trim() === 'Sticky Last-Success Ordering',
  );
  expect(label).toBeTruthy();
  const toggle = label!.parentElement!.querySelector('button') as HTMLButtonElement;
  expect(toggle).not.toBeNull();
  return toggle;
}

describe('SettingsPage — sticky last-success race ordering', () => {
  it('loads the flag from config (true ⇒ Enabled)', () => {
    const props = makeProps();
    props.config = { ...props.config!, sticky_last_success_enabled: true };
    const { container } = render(<SettingsPage {...props} />);

    const toggle = findStickyToggle(container);
    // The pill's sibling copy is the Enabled/Disabled label.
    expect(toggle.parentElement!.textContent).toContain('Enabled');
  });

  it('toggle round-trips into the proxy-Apply payload (and preserves the race siblings)', async () => {
    const props = makeProps();
    const { container } = render(<SettingsPage {...props} />);

    const toggle = findStickyToggle(container);
    expect(toggle.parentElement!.textContent).toContain('Disabled');
    fireEvent.click(toggle);
    expect(toggle.parentElement!.textContent).toContain('Enabled');

    const apply = Array.from(container.querySelectorAll('button')).find(
      (b) => (b.textContent || '').includes('Apply Changes'),
    ) as HTMLButtonElement;
    expect(apply).toBeDefined();
    fireEvent.click(apply);

    await waitFor(() => expect(props.onUpdateConfig).toHaveBeenCalled());
    const payload = (props.onUpdateConfig as unknown as { mock: { calls: unknown[][] } }).mock.calls[0][0] as Record<string, unknown>;
    // The flag rides the proxy-Apply payload — mandatory, because the BE
    // family merge is authoritative for set AND clear when the family is
    // present (omitting it would silently clear the flag).
    expect(payload.sticky_last_success_enabled).toBe(true);
    // Race siblings keep their loaded values (no zero-clobber).
    expect(payload.race_retry_enabled).toBe(false);
    expect(payload.race_max_parallel).toBe(3);
    expect(payload.race_max_buffer_bytes).toBe(5242880);
  });

  it('a family-bearing Apply sends sticky=false when the toggle is off (clear path)', async () => {
    const props = makeProps();
    props.config = { ...props.config!, sticky_last_success_enabled: true };
    const { container } = render(<SettingsPage {...props} />);

    const toggle = findStickyToggle(container);
    fireEvent.click(toggle); // true → false

    const apply = Array.from(container.querySelectorAll('button')).find(
      (b) => (b.textContent || '').includes('Apply Changes'),
    ) as HTMLButtonElement;
    fireEvent.click(apply);

    await waitFor(() => expect(props.onUpdateConfig).toHaveBeenCalled());
    const payload = (props.onUpdateConfig as unknown as { mock: { calls: unknown[][] } }).mock.calls[0][0] as Record<string, unknown>;
    expect(payload.sticky_last_success_enabled).toBe(false);
  });
  // copy-model commission — the end-to-end proof that the Copy action
  // surfaces through the HOUSE toast mechanism (the ToastContainer
  // rendered by SettingsPage), not a bespoke banner.
  it('the Models tab copy action raises the house success toast naming the new id + the DISABLED warning', async () => {
    const props = makeProps({
      models: [{ id: 'm1', name: 'M1', enabled: true, fallback_chain: [] }],
    });
    const { container } = render(<SettingsPage {...props} />);

    // Open the Models tab.
    const tabs = Array.from(
      container.querySelectorAll('div.flex.overflow-x-auto > button'),
    ) as HTMLButtonElement[];
    const modelsTab = tabs.find((b) => (b.textContent || '').trim() === 'Models');
    expect(modelsTab).toBeDefined();
    fireEvent.click(modelsTab!);

    await waitFor(() => {
      expect(container.querySelector('[data-testid="copy-model-button"]')).not.toBeNull();
    });

    // Open the copy dialog and submit the prefilled id.
    fireEvent.click(container.querySelector('[data-testid="copy-model-button"]')!);
    await waitFor(() => {
      expect(container.querySelector('[data-testid="copy-model-dialog"]')).not.toBeNull();
    });
    fireEvent.click(container.querySelector('[data-testid="copy-model-confirm"]')!);

    // The toast carries the new id + the DISABLED warning.
    await waitFor(() => {
      expect(container.textContent).toContain('Copied model "m1" to "m1-copy"');
    });
    expect(container.textContent).toContain(
      'The copy is DISABLED: it is not in /v1/models and not in automatic selection',
    );
    expect(props.onCopyModel).toHaveBeenCalledWith('m1', 'm1-copy');
  });
});
