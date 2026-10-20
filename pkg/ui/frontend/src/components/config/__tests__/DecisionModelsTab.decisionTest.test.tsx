import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, act, waitFor } from '@testing-library/preact';
import { DecisionModelsTab } from '../DecisionModelsTab';
import type { DecisionModel } from '../../../types';

// Decision-model TEST button (per-row probe).
//
// The suite pins the FE contract:
//   - a per-row Test button exists and fires ONE POST to
//     /fe/api/decisions/test with the row id (and NO
//     safety_identifier — the BE owns the canned body),
//   - a success renders the model + the typed answers (a predicate
//     answer with its probability),
//   - every error class renders its CLASSIFIED label + the upstream's
//     short message (never a raw body dump),
//   - the button shows "Testing..." and is disabled while in flight.
//
// The upstream itself is faked at the fetch boundary: the FE never
// talks to a provider (the BE owns that leg).
const fetchMock = vi.fn();
const realFetch = globalThis.fetch;

beforeEach(() => {
  (globalThis as { fetch: typeof fetch }).fetch = fetchMock as unknown as typeof fetch;
  fetchMock.mockReset();
});
afterEach(() => {
  (globalThis as { fetch: typeof fetch }).fetch = realFetch;
  cleanup();
});

function jsonResponse(body: unknown, init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
    ...init,
  });
}

function makeDecisionRow(overrides: Partial<DecisionModel> = {}): DecisionModel {
  return {
    id: 'decision-router-v1',
    name: 'Decision Router v1',
    enabled: true,
    fallback_chain: [],
    kind: 'decision',
    internal: true,
    internal_provider: 'openai',
    internal_model: 'gpt-6-luna',
    internal_base_url: 'https://api.openai.com',
    credentials: [{ credential_id: 'cred-oai-1', weight: 1, position: 0 }],
    exclude_from_ultimate_switching: true,
    ...overrides,
  };
}

function renderTab(rows: DecisionModel[]) {
  return render(
    <DecisionModelsTab
      models={rows}
      onAddModel={vi.fn()}
      onUpdateModel={vi.fn()}
      onDeleteModel={vi.fn()}
      onToggleModel={vi.fn()}
      setStatus={vi.fn()}
    />,
  );
}

async function clickTest(container: Element, modelId: string) {
  const btn = container.querySelector(`[data-testid="decision-test-button-${modelId}"]`) as HTMLButtonElement | null;
  expect(btn).not.toBeNull();
  await act(async () => {
    fireEvent.click(btn as HTMLButtonElement);
  });
}

describe('DecisionModelsTab — per-row Test button (decision test probe)', () => {
  describe('wire shape', () => {
    it('POSTs the row id to /fe/api/decisions/test and sends no safety_identifier', async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({
          success: true,
          model: 'gpt-6-luna',
          status: 200,
          answers: [{ type: 'predicate', name: 'is_refund_request', probability: 0.93 }],
        }),
      );

      const { container } = renderTab([makeDecisionRow()]);
      await clickTest(container, 'decision-router-v1');

      expect(fetchMock).toHaveBeenCalledTimes(1);
      const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
      expect(url).toBe('/fe/api/decisions/test');
      expect(init.method).toBe('POST');

      const payload = JSON.parse(init.body as string) as Record<string, unknown>;
      expect(payload).toEqual({ model_id: 'decision-router-v1' });
      // The FE owns no body fields — the BE owns the canned decision
      // body (which carries NO safety_identifier).
      expect(Object.keys(payload)).not.toContain('safety_identifier');
    });

    it('renders one Test button per row and keeps the per-row state separate', async () => {
      fetchMock.mockResolvedValue(jsonResponse({ success: true, model: 'm', status: 200, answers: [] }));

      const { container } = renderTab([
        makeDecisionRow({ id: 'row-a', name: 'Alpha' }),
        makeDecisionRow({ id: 'row-b', name: 'Bravo' }),
      ]);

      const buttons = container.querySelectorAll('[data-testid^="decision-test-button-"]');
      expect(buttons.length).toBe(2);

      await clickTest(container, 'row-a');
      await waitFor(() => {
        expect(container.querySelector('[data-testid="decision-test-result-row-a"]')).not.toBeNull();
      });
      // The probe on row-a must NOT paint a panel on row-b.
      expect(container.querySelector('[data-testid="decision-test-result-row-b"]')).toBeNull();
    });
  });

  describe('success rendering', () => {
    it('renders the model + the predicate answer with its probability', async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({
          success: true,
          model: 'gpt-6-luna',
          provider: 'openai',
          status: 200,
          usage: { input_tokens: 42, output_tokens: 7, total_tokens: 49 },
          answers: [{ type: 'predicate', name: 'is_refund_request', probability: 0.93 }],
        }),
      );

      const { container } = renderTab([makeDecisionRow()]);
      await clickTest(container, 'decision-router-v1');

      await waitFor(() => {
        expect(container.querySelector('[data-testid="decision-test-success"]')).not.toBeNull();
      });
      expect(container.textContent).toContain('Decision OK');
      expect(container.textContent).toContain('gpt-6-luna');
      expect(container.textContent).toContain('predicate');
      expect(container.textContent).toContain('is_refund_request');
      expect(container.textContent).toContain('93.0%');
      // No error panel on a success.
      expect(container.querySelector('[data-testid="decision-test-error"]')).toBeNull();
    });

    it('renders a choice answer with its probability distribution', async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({
          success: true,
          model: 'gpt-6-luna',
          status: 200,
          answers: [
            {
              type: 'choice',
              name: 'escalate',
              choice: 'human',
              probabilities: [
                { value: 'human', probability: 0.7 },
                { value: 'auto', probability: 0.3 },
              ],
            },
          ],
        }),
      );

      const { container } = renderTab([makeDecisionRow()]);
      await clickTest(container, 'decision-router-v1');

      await waitFor(() => {
        expect(container.querySelector('[data-testid="decision-test-answer"]')).not.toBeNull();
      });
      expect(container.textContent).toContain('choice');
      expect(container.textContent).toContain('human');
      expect(container.textContent).toContain('70.0%');
      expect(container.textContent).toContain('30.0%');
    });
  });

  describe('error classification', () => {
    const cases: Array<{ status: number; label: string }> = [
      { status: 401, label: 'Authentication failed — check the credential API key' },
      { status: 402, label: 'Insufficient funds — top up the provider account' },
      { status: 403, label: 'Access denied — the credential may not use this model' },
      { status: 404, label: 'Not found — check the upstream base URL' },
      { status: 422, label: 'Endpoint unavailable' },
      { status: 429, label: 'Rate limited — retry later' },
      { status: 500, label: 'Upstream error' },
      { status: 502, label: 'Upstream error' },
      { status: 503, label: 'Upstream unavailable — retry later' },
      { status: 504, label: 'Upstream timed out' },
      { status: 418, label: 'Unexpected error' },
    ];

    for (const { status, label } of cases) {
      it(`renders the ${status} class as "${label}"`, async () => {
        fetchMock.mockResolvedValue(
          jsonResponse({
            success: false,
            status,
            relay_status: 502,
            upstream_status: status,
            error: 'upstream says no',
          }),
        );

        const { container } = renderTab([makeDecisionRow()]);
        await clickTest(container, 'decision-router-v1');

        await waitFor(() => {
          expect(container.querySelector('[data-testid="decision-test-error"]')).not.toBeNull();
        });
        const errorEl = container.querySelector('[data-testid="decision-test-error"]');
        expect(errorEl?.textContent).toContain(label);
        expect(errorEl?.textContent).toContain(`HTTP ${status}`);
        // The upstream's own short message is shown next to the class.
        expect(container.querySelector('[data-testid="decision-test-error-message"]')?.textContent).toContain(
          'upstream says no',
        );
        // No success panel on a failure.
        expect(container.querySelector('[data-testid="decision-test-success"]')).toBeNull();
      });
    }

    it('renders the class even when the upstream sent no message', async () => {
      fetchMock.mockResolvedValue(jsonResponse({ success: false, status: 402 }));

      const { container } = renderTab([makeDecisionRow()]);
      await clickTest(container, 'decision-router-v1');

      await waitFor(() => {
        expect(container.querySelector('[data-testid="decision-test-error"]')).not.toBeNull();
      });
      expect(container.textContent).toContain('Insufficient funds');
      expect(container.querySelector('[data-testid="decision-test-error-message"]')).toBeNull();
    });

    it('classifies a failed FE call (status 0) as a proxy-no-answer', async () => {
      fetchMock.mockRejectedValue(new Error('Network down'));

      const { container } = renderTab([makeDecisionRow()]);
      await clickTest(container, 'decision-router-v1');

      await waitFor(() => {
        expect(container.querySelector('[data-testid="decision-test-error"]')).not.toBeNull();
      });
      expect(container.textContent).toContain('Test request failed');
      expect(container.textContent).toContain('Network down');
    });
  });

  describe('in-flight state', () => {
    it('shows "Testing..." and disables the button while the probe is in flight', async () => {
      // A never-resolving promise keeps the probe in flight.
      fetchMock.mockReturnValue(new Promise(() => {}));

      const { container } = renderTab([makeDecisionRow()]);
      const btn = container.querySelector(
        '[data-testid="decision-test-button-decision-router-v1"]',
      ) as HTMLButtonElement;
      expect(btn.textContent).toBe('Test');
      expect(btn.disabled).toBe(false);

      await act(async () => {
        fireEvent.click(btn);
      });

      const busy = container.querySelector(
        '[data-testid="decision-test-button-decision-router-v1"]',
      ) as HTMLButtonElement;
      expect(busy.textContent).toBe('Testing...');
      expect(busy.disabled).toBe(true);
    });
  });
});
