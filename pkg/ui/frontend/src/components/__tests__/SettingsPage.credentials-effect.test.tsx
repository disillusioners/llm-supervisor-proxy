import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, waitFor } from '@testing-library/preact';
import { SettingsPage } from '../SettingsPage';
import type { Model, AppConfig, ApiToken, ImgGenModel } from '../../types';

// Minimal prop builder (mirrors SettingsPage.tabs.test.tsx).
function makeProps() {
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
      enabled: false, shadow_mode: false, message_window: 0, action_window: 0,
      exact_match_count: 0, similarity_threshold: 0, min_tokens_for_simhash: 0,
      action_repeat_count: 0, oscillation_count: 0, min_tokens_for_analysis: 0,
      thinking_min_tokens: 0, trigram_threshold: 0, max_cycle_length: 0,
      reasoning_model_patterns: [], reasoning_trigram_threshold: 0,
    },
    tool_repair: { enabled: false, strategies: [], max_arguments_size: 0, max_tool_calls_per_response: 0, log_original: false, log_repaired: false, fixer_model: '', fixer_timeout: 0 },
    updated_at: '',
  } as any;
  return {
    config: baseConfig,
    onUpdateConfig: vi.fn().mockResolvedValue({ ...baseConfig, restart_required: false }),
    models: [] as Model[],
    onAddModel: vi.fn(), onUpdateModel: vi.fn(), onDeleteModel: vi.fn(),
    onCopyModel: vi.fn(),
    imgGenModels: [] as ImgGenModel[],
    onAddImgGenModel: vi.fn(), onUpdateImgGenModel: vi.fn(), onDeleteImgGenModel: vi.fn(),
    decisionModels: [] as Model[],
    onAddDecisionModel: vi.fn(), onUpdateDecisionModel: vi.fn(), onDeleteDecisionModel: vi.fn(),
    groupModels: [] as Model[],
    onAddGroupModel: vi.fn(), onUpdateGroupModel: vi.fn(), onDeleteGroupModel: vi.fn(),
    tokens: [] as ApiToken[],
    onCreateToken: vi.fn(), onDeleteToken: vi.fn(),
    onUpdateTokenPermission: vi.fn(), onRefetchTokens: vi.fn(),
  };
}

describe('SettingsPage — credentials fetch effect stability (C1)', () => {
  let credFetchCount = 0;
  let credShouldFail = true;
  const realFetch = globalThis.fetch;

  beforeEach(() => {
    credFetchCount = 0;
    credShouldFail = true;
    (globalThis as { fetch: typeof fetch }).fetch = vi.fn(async (input: RequestInfo | URL) => {
      const url = typeof input === 'string' ? input : (input as Request).url;
      if (url.includes('/credential-groups')) {
        return new Response(JSON.stringify([]), { status: 200, headers: { 'Content-Type': 'application/json' } });
      }
      if (url.includes('/fe/api/credentials')) {
        credFetchCount++;
        if (credShouldFail) {
          credShouldFail = false;
          return new Response(JSON.stringify({ error: 'boom' }), { status: 500, headers: { 'Content-Type': 'application/json' } });
        }
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

  it('does not refire the credentials fetch on the error-path re-render (count === 1)', async () => {
    const { container } = render(<SettingsPage {...makeProps()} />);
    const credentialsTab = container.querySelector('[data-tab-id="credentials"]') as HTMLButtonElement;
    expect(credentialsTab).not.toBeNull();
    fireEvent.click(credentialsTab);

    // Wait for the error to settle (toast rendered).
    //
    // R2 — the count alone is VACUOUS: a SWALLOWED error (the catch that
    // never calls setStatus) still yields credFetchCount === 1 and would
    // pass. The error message reaching the DOM (the SettingsPage toast
    // channel) is the discriminating assertion.
    await waitFor(() => {
      expect(credFetchCount).toBeGreaterThanOrEqual(1);
      expect(container.textContent).toContain('Failed to fetch credentials');
    });
    // give time for any refire
    await new Promise((r) => setTimeout(r, 50));
    expect(credFetchCount).toBe(1);
  });
});
