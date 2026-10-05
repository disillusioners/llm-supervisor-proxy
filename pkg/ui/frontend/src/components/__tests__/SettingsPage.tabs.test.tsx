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
  config: AppConfig | null;
}> = {}) {
  const baseConfig: AppConfig = {
    version: '0.0.0',
    upstream_url: '',
    port: 8089,
    idle_timeout: '',
    stream_deadline: '',
    max_generation_time: '',
    race_retry_enabled: false,
    race_parallel_on_idle: true,
    race_max_parallel: 3,
    race_max_buffer_bytes: 5242880,
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
    imgGenModels: [] as ImgGenModel[],
    onAddImgGenModel: vi.fn(),
    onUpdateImgGenModel: vi.fn(),
    onDeleteImgGenModel: vi.fn(),
    tokens: [] as ApiToken[],
    onCreateToken: vi.fn(),
    onDeleteToken: vi.fn(),
    onUpdateTokenPermission: vi.fn(),
    onRefetchTokens: vi.fn(),
    ...overrides,
  };
}

describe('SettingsPage — tab mechanism (ImgGen Commission C-17, T2.5.1)', () => {
  it('renders the 9-tab bar with the 🖼️ ImgGen Models button at slot 3', () => {
    const { container } = render(<SettingsPage {...makeProps()} />);

    // Find the new tab button by its stable data-tab-id (added in
    // SettingsPage.tsx so the test has a hook).
    const imggenTab = container.querySelector('[data-tab-id="imggen_models"]') as HTMLButtonElement;
    expect(imggenTab).not.toBeNull();
    // Label is character-exact (fe-spec C-01).
    expect(imggenTab.textContent).toContain('🖼️');
    expect(imggenTab.textContent).toContain('ImgGen Models');

    // Position check (fe-spec C-02): the button immediately preceding the
    // new tab is "Models" and the button immediately following is
    // "Credentials". The tab bar is a flex container; we walk the DOM.
    const tabs = Array.from(
      container.querySelectorAll('div.flex.overflow-x-auto > button'),
    ) as HTMLButtonElement[];
    const imggenIdx = tabs.findIndex((b) => b.getAttribute('data-tab-id') === 'imggen_models');
    expect(imggenIdx).toBeGreaterThan(0);
    expect(tabs[imggenIdx - 1].textContent).toContain('Models');
    expect(tabs[imggenIdx + 1].textContent).toContain('Credentials');
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
    // The new tab is the first place we put aria-hidden on an emoji.
    const emojiSpan = container.querySelector('[data-tab-id="imggen_models"] span[aria-hidden="true"]');
    expect(emojiSpan).not.toBeNull();
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
});
