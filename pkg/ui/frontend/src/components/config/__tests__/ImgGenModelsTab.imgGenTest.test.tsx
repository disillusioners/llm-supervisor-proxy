import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, act, waitFor } from '@testing-library/preact';
import { ImgGenModelsTab } from '../ImgGenModelsTab';
import type { ImgGenModel } from '../../../types';

// Image-generation TEST button (per-row probe).
//
// The suite pins the FE contract:
//   - a per-row Test button exists and fires ONE POST to
//     /fe/api/image_generation/test with the row id ONLY (the BE owns
//     the canned generation body),
//   - a success renders the generated image (the upstream's CDN URL,
//     rendered directly) + the probe metadata,
//   - every error class renders its CLASSIFIED label + the upstream's
//     short message (never a raw body dump),
//   - the per-row busy state is isolated (two rows may probe
//     concurrently) and the button shows "Testing..." while in flight.
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

function makeImgGenRow(overrides: Partial<ImgGenModel> = {}): ImgGenModel {
  return {
    id: 'imggen-mini-v1',
    name: 'Image Gen MiniMax v1',
    enabled: true,
    fallback_chain: [],
    kind: 'image-gen',
    internal: true,
    internal_provider: 'minimax',
    internal_model: 'image-01',
    internal_base_url: 'https://api.minimax.io/v1',
    credentials: [{ credential_id: 'cred-mini-1', weight: 1, position: 0 }],
    exclude_from_ultimate_switching: true,
    ...overrides,
  };
}

function renderTab(rows: ImgGenModel[]) {
  return render(
    <ImgGenModelsTab
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
  const btn = container.querySelector(`[data-testid="imggen-test-button-${modelId}"]`) as HTMLButtonElement | null;
  expect(btn).not.toBeNull();
  await act(async () => {
    fireEvent.click(btn as HTMLButtonElement);
  });
}

// successBody is the DTO the BE answers with on a successful
// generation (the upstream's CDN URLs, relayed verbatim).
const successDTO = {
  success: true,
  model: 'imggen-mini-v1',
  provider: 'minimax',
  credential_id: 'cred-mini-1',
  image_urls: ['https://cdn.example/a.png'],
  image_count: 1,
  success_count: 1,
  failed_count: 0,
  status: 200,
  code: 'ok',
  duration_ms: 1234,
};

describe('ImgGenModelsTab — per-row Test button (image-gen probe)', () => {
  describe('wire shape', () => {
    it('POSTs the row id to /fe/api/image_generation/test and sends no body fields', async () => {
      fetchMock.mockResolvedValue(jsonResponse(successDTO));

      const { container } = renderTab([makeImgGenRow()]);
      await clickTest(container, 'imggen-mini-v1');

      expect(fetchMock).toHaveBeenCalledTimes(1);
      const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
      expect(url).toBe('/fe/api/image_generation/test');
      expect(init.method).toBe('POST');

      const payload = JSON.parse(init.body as string) as Record<string, unknown>;
      expect(payload).toEqual({ model_id: 'imggen-mini-v1' });
      // The FE owns NO body fields — the BE owns the canned
      // generation body (model / prompt / aspect_ratio / n /
      // response_format).
      expect(Object.keys(payload)).toEqual(['model_id']);
    });

    it('renders one Test button per row and keeps the per-row state separate', async () => {
      fetchMock.mockResolvedValue(jsonResponse(successDTO));

      const { container } = renderTab([
        makeImgGenRow({ id: 'row-a', name: 'Alpha' }),
        makeImgGenRow({ id: 'row-b', name: 'Bravo' }),
      ]);

      const buttons = container.querySelectorAll('[data-testid^="imggen-test-button-"]');
      expect(buttons.length).toBe(2);

      await clickTest(container, 'row-a');
      await waitFor(() => {
        expect(container.querySelector('[data-testid="imggen-test-result-row-a"]')).not.toBeNull();
      });
      // The probe on row-a must NOT paint a panel on row-b.
      expect(container.querySelector('[data-testid="imggen-test-result-row-b"]')).toBeNull();
    });
  });

  describe('success rendering', () => {
    it('renders the generated image with the upstream URL + the metadata line', async () => {
      fetchMock.mockResolvedValue(jsonResponse(successDTO));

      const { container } = renderTab([makeImgGenRow()]);
      await clickTest(container, 'imggen-mini-v1');

      await waitFor(() => {
        expect(container.querySelector('[data-testid="imggen-test-success"]')).not.toBeNull();
      });

      // The image is rendered DIRECTLY from the upstream's CDN URL
      // (no second proxy hop, no base64).
      const img = container.querySelector('[data-testid="imggen-test-image"]') as HTMLImageElement | null;
      expect(img).not.toBeNull();
      expect(img?.getAttribute('src')).toBe('https://cdn.example/a.png');
      expect(img?.getAttribute('loading')).toBe('lazy');
      expect(img?.getAttribute('alt')).toContain('imggen-mini-v1');
      // The thumbnail is BOUNDED (a fixed box, never the raw image
      // size).
      expect(img?.className).toContain('h-24');
      expect(img?.className).toContain('w-24');

      // The metadata line carries the provider / model / count /
      // duration + the format derived from the URL.
      const meta = container.querySelector('[data-testid="imggen-test-meta"]');
      expect(meta?.textContent).toContain('minimax');
      expect(meta?.textContent).toContain('imggen-mini-v1');
      expect(meta?.textContent).toContain('1 image');
      // The count is pluralized like the headline (never "1 image(s)").
      expect(meta?.textContent).not.toContain('image(s)');
      expect(meta?.textContent).toContain('1234 ms');
      expect(meta?.textContent).toContain('png');

      // No error panel on a success.
      expect(container.querySelector('[data-testid="imggen-test-error"]')).toBeNull();
    });

    it('renders the success panel with role=status (announced, not an alert)', async () => {
      fetchMock.mockResolvedValue(jsonResponse(successDTO));

      const { container } = renderTab([makeImgGenRow()]);
      await clickTest(container, 'imggen-mini-v1');

      await waitFor(() => {
        expect(container.querySelector('[data-testid="imggen-test-result-imggen-mini-v1"]')).not.toBeNull();
      });
      const panel = container.querySelector('[data-testid="imggen-test-result-imggen-mini-v1"]');
      expect(panel?.getAttribute('role')).toBe('status');
    });
  });

  describe('error classification', () => {
    const statusCases: Array<{ status: number; label: string }> = [
      { status: 400, label: 'Request rejected by the image-gen adapter' },
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

    for (const { status, label } of statusCases) {
      it(`renders the ${status} class as "${label}"`, async () => {
        fetchMock.mockResolvedValue(
          jsonResponse({
            success: false,
            status,
            relay_status: 502,
            upstream_status: status,
            code: 'upstream_http_error',
            error: 'upstream says no',
          }),
        );

        const { container } = renderTab([makeImgGenRow()]);
        await clickTest(container, 'imggen-mini-v1');

        await waitFor(() => {
          expect(container.querySelector('[data-testid="imggen-test-error"]')).not.toBeNull();
        });
        const errorEl = container.querySelector('[data-testid="imggen-test-error"]');
        expect(errorEl?.textContent).toContain(label);
        expect(errorEl?.textContent).toContain(`HTTP ${status}`);
        // The upstream's own short message is shown next to the class.
        expect(container.querySelector('[data-testid="imggen-test-error-message"]')?.textContent).toContain(
          'upstream says no',
        );
        // No success panel on a failure.
        expect(container.querySelector('[data-testid="imggen-test-success"]')).toBeNull();
        // No image on a failure.
        expect(container.querySelector('[data-testid="imggen-test-image"]')).toBeNull();
      });
    }

    const codeCases: Array<{ code: string; status: number; label: string }> = [
      {
        code: 'upstream_business_error',
        status: 502,
        label: 'Upstream rejected the request — invalid parameters',
      },
      {
        code: 'empty_image',
        status: 200,
        label: 'No image returned — the upstream accepted the request but produced no images',
      },
      { code: 'cooldown', status: 429, label: 'Rate limited — retry later' },
      {
        code: 'response_too_large',
        status: 502,
        label: 'Upstream response too large — the image payload exceeded the cap',
      },
      {
        code: 'busy',
        status: 429,
        label: 'Another test probe is already running — wait for it to finish',
      },
    ];

    for (const { code, status, label } of codeCases) {
      it(`renders the ${code} code class as "${label}"`, async () => {
        fetchMock.mockResolvedValue(
          jsonResponse({
            success: false,
            status,
            code,
            error: 'the upstream said so',
          }),
        );

        const { container } = renderTab([makeImgGenRow()]);
        await clickTest(container, 'imggen-mini-v1');

        await waitFor(() => {
          expect(container.querySelector('[data-testid="imggen-test-error"]')).not.toBeNull();
        });
        const errorEl = container.querySelector('[data-testid="imggen-test-error"]');
        expect(errorEl?.textContent).toContain(label);
        expect(errorEl?.textContent).toContain(`HTTP ${status}`);
        // The failure panel is an ALERT (role), not a status.
        const panel = container.querySelector('[data-testid="imggen-test-result-imggen-mini-v1"]');
        expect(panel?.getAttribute('role')).toBe('alert');
      });
    }

    it('renders the class even when the upstream sent no message', async () => {
      fetchMock.mockResolvedValue(jsonResponse({ success: false, status: 402, code: 'upstream_http_error' }));

      const { container } = renderTab([makeImgGenRow()]);
      await clickTest(container, 'imggen-mini-v1');

      await waitFor(() => {
        expect(container.querySelector('[data-testid="imggen-test-error"]')).not.toBeNull();
      });
      expect(container.textContent).toContain('Insufficient funds');
      expect(container.querySelector('[data-testid="imggen-test-error-message"]')).toBeNull();
    });

    it('classifies a failed FE call (status 0) as a proxy-no-answer', async () => {
      fetchMock.mockRejectedValue(new Error('Network down'));

      const { container } = renderTab([makeImgGenRow()]);
      await clickTest(container, 'imggen-mini-v1');

      await waitFor(() => {
        expect(container.querySelector('[data-testid="imggen-test-error"]')).not.toBeNull();
      });
      expect(container.textContent).toContain('Test request failed');
      expect(container.textContent).toContain('Network down');
    });

    it('does not render a non-http(s) image URL (the URL is upstream-provided)', async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({
          ...successDTO,
          image_urls: ['javascript:alert(1)'],
        }),
      );

      const { container } = renderTab([makeImgGenRow()]);
      await clickTest(container, 'imggen-mini-v1');

      await waitFor(() => {
        expect(container.querySelector('[data-testid="imggen-test-success"]')).not.toBeNull();
      });
      // The upstream-provided URL never reaches the DOM as an <img src>.
      expect(container.querySelector('[data-testid="imggen-test-image"]')).toBeNull();
    });

    it('never renders a raw JSON body dump', async () => {
      fetchMock.mockResolvedValue(
        jsonResponse({
          success: false,
          status: 502,
          code: 'upstream_malformed',
          error: 'missing base_resp key',
          reason: 'missing base_resp key',
        }),
      );

      const { container } = renderTab([makeImgGenRow()]);
      await clickTest(container, 'imggen-mini-v1');

      await waitFor(() => {
        expect(container.querySelector('[data-testid="imggen-test-error"]')).not.toBeNull();
      });
      expect(container.textContent).not.toContain('"base_resp"');
      expect(container.textContent).not.toContain('{\\"');
    });
  });

  describe('in-flight state', () => {
    it('shows "Testing..." and disables the button while the probe is in flight', async () => {
      // A never-resolving promise keeps the probe in flight.
      fetchMock.mockReturnValue(new Promise(() => {}));

      const { container } = renderTab([makeImgGenRow()]);
      const btn = container.querySelector('[data-testid="imggen-test-button-imggen-mini-v1"]') as HTMLButtonElement;
      expect(btn.textContent).toBe('Test');
      expect(btn.disabled).toBe(false);

      await act(async () => {
        fireEvent.click(btn);
      });

      expect(btn.textContent).toBe('Testing...');
      expect(btn.disabled).toBe(true);
    });

    it('keeps the busy state PER ROW — two rows probe concurrently', async () => {
      // Each fetch gets its OWN pending promise (a Response body can
      // only be read once — a shared instance would break the second
      // row).
      const gates: Array<(r: Response) => void> = [];
      fetchMock.mockImplementation(
        () =>
          new Promise<Response>((res) => {
            gates.push(res);
          }),
      );

      const { container } = renderTab([
        makeImgGenRow({ id: 'row-a', name: 'Alpha' }),
        makeImgGenRow({ id: 'row-b', name: 'Bravo' }),
      ]);

      const btnA = container.querySelector('[data-testid="imggen-test-button-row-a"]') as HTMLButtonElement;
      const btnB = container.querySelector('[data-testid="imggen-test-button-row-b"]') as HTMLButtonElement;

      await act(async () => {
        fireEvent.click(btnA);
      });
      await act(async () => {
        fireEvent.click(btnB);
      });

      // BOTH rows are busy — row-b's probe must not clear row-a's
      // busy state (and vice versa).
      expect(btnA.textContent).toBe('Testing...');
      expect(btnB.textContent).toBe('Testing...');
      expect(btnA.disabled).toBe(true);
      expect(btnB.disabled).toBe(true);
      expect(fetchMock).toHaveBeenCalledTimes(2);

      // Resolving row-a clears ONLY row-a — row-b keeps its own busy
      // state while its probe is still in flight.
      await act(async () => {
        gates[0](jsonResponse(successDTO));
        await Promise.resolve();
      });
      await waitFor(() => {
        expect(container.querySelector('[data-testid="imggen-test-result-row-a"]')).not.toBeNull();
      });
      expect(btnA.textContent).toBe('Test');
      expect(btnB.textContent).toBe('Testing...');
      expect(container.querySelector('[data-testid="imggen-test-result-row-b"]')).toBeNull();

      // Resolving row-b finishes the second row.
      await act(async () => {
        gates[1](jsonResponse(successDTO));
        await Promise.resolve();
      });
      await waitFor(() => {
        expect(container.querySelector('[data-testid="imggen-test-result-row-b"]')).not.toBeNull();
      });
      expect(btnB.textContent).toBe('Test');
    });
  });

  describe('cost hint', () => {
    it('labels the button with the (approximate) cost hint — a probe is billable', async () => {
      const { container } = renderTab([makeImgGenRow()]);
      const btn = container.querySelector('[data-testid="imggen-test-button-imggen-mini-v1"]') as HTMLButtonElement;
      expect(btn.getAttribute('title')).toContain('Generate one real image');
    });
  });
});
