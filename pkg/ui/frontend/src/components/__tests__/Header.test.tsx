import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, cleanup, waitFor } from '@testing-library/preact';
import Header from '../Header';

// Stub global fetch by URL so the REAL useVersion hook runs (stronger
// guard than mocking the hook module): the badge must render `v<version>`
// from the actual /fe/api/version payload. Header also calls useRam
// (polls /fe/api/ram), so that endpoint is stubbed too. Same
// stub-globalThis.fetch style as SettingsPage.tabs.test.tsx.
const realFetch = globalThis.fetch;

function jsonResponse(payload: unknown): Response {
  return new Response(JSON.stringify(payload), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('Header version badge', () => {
  afterEach(() => {
    (globalThis as { fetch: typeof fetch }).fetch = realFetch;
    cleanup();
  });

  it('renders v<version> from the /fe/api/version payload', async () => {
    (globalThis as { fetch: typeof fetch }).fetch = vi.fn(async (input: RequestInfo | URL) => {
      const url = typeof input === 'string' ? input : (input as Request).url;
      if (url.includes('/fe/api/version')) {
        return jsonResponse({ version: '151' });
      }
      if (url.includes('/fe/api/ram')) {
        return jsonResponse({ alloc_bytes: 0, alloc_mb: 0 });
      }
      return jsonResponse({});
    }) as unknown as typeof fetch;

    const { container } = render(<Header />);
    await waitFor(() => {
      expect(container.textContent).toContain('v151');
    });
  });
});
