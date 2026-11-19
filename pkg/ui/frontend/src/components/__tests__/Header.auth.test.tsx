// internal-oauth2 phase 1 (plan §7.6 / §9.3) — the header badge/logout.
//
// Pins: the identity badge + Sign out render ONLY when the internal
// mode is active AND authenticated; absent when dormant; absent when
// anonymous; and logout issues a POST form submission to /oauth2/logout
// (AR-36 — a GET would be 405).
//
// NOTE: channel-name spellings are deliberately avoided in this file —
// the AR-60 census must stay clean outside the guarded modules.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, cleanup, fireEvent, waitFor } from '@testing-library/preact';
import Header from '../Header';
import { authState } from '../../hooks/useAuth';

const realFetch = globalThis.fetch;

function jsonResponse(payload: unknown): Response {
  return new Response(JSON.stringify(payload), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

function installWire(probePayload: unknown) {
  const wire = vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : (input as Request).url;
    if (url.includes('/fe/api/auth/me')) return jsonResponse(probePayload);
    if (url.includes('/fe/api/version')) return jsonResponse({ version: '203' });
    if (url.includes('/fe/api/ram')) return jsonResponse({ alloc_bytes: 0, alloc_mb: 0 });
    return jsonResponse({});
  });
  (globalThis as { fetch: typeof fetch }).fetch = wire as unknown as typeof fetch;
  return wire;
}

async function renderHeader() {
  const screen = render(<Header />);
  // The version badge proves the header finished its data probes.
  await waitFor(() => expect(screen.container.textContent).toContain('v203'));
  return screen;
}

describe('Header auth badge + logout (internal-oauth2 phase 1)', () => {
  beforeEach(() => {
    // The module probe cache persists across renders/tests — reset it so
    // every case drives its own probe payload.
    authState.resetForTests();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    (globalThis as { fetch: typeof fetch }).fetch = realFetch;
    document.body.innerHTML = '';
    cleanup();
  });

  it('renders the badge + Sign out when enabled && authenticated', async () => {
    installWire({ enabled: true, authenticated: true, user: { sub: 's1', email: 'op@example.com', name: 'Operator' } });
    const { container } = await renderHeader();

    expect(container.textContent).toContain('Operator');
    expect(container.textContent).toContain('Sign out');
    const badge = container.querySelector('span[title="op@example.com"]');
    expect(badge).not.toBeNull();
  });

  it('the badge falls back name -> email -> sub (NO preferred_username)', async () => {
    installWire({ enabled: true, authenticated: true, user: { sub: 'sub-42', email: 'op@example.com' } });
    const { container } = await renderHeader();
    expect(container.textContent).toContain('op@example.com');
    cleanup();
    authState.resetForTests(); // fresh probe for the second shape

    installWire({ enabled: true, authenticated: true, user: { sub: 'sub-42' } });
    const second = await renderHeader();
    expect(second.container.textContent).toContain('sub-42');
  });

  it('absent when dormant (enabled:false) — header renders exactly as today', async () => {
    installWire({ enabled: false, authenticated: false, state_store_count: 0 });
    const { container } = await renderHeader();

    expect(container.textContent).not.toContain('Sign out');
    expect(container.querySelectorAll('span[title="op@example.com"]')).toHaveLength(0);
    // The other chips still render.
    expect(container.textContent).toContain('System Active');
  });

  it('absent when enabled but unauthenticated (anonymous)', async () => {
    installWire({ enabled: true, authenticated: false, state_store_count: 1 });
    const { container } = await renderHeader();

    expect(container.textContent).not.toContain('Sign out');
    expect(container.textContent).not.toContain('op@example.com');
  });

  it('logout issues a POST form submission to /oauth2/logout (AR-36)', async () => {
    installWire({ enabled: true, authenticated: true, user: { sub: 's1', email: 'op@example.com', name: 'Operator' } });
    const { container } = await renderHeader();

    const submitSpy = vi.spyOn(HTMLFormElement.prototype, 'submit').mockImplementation(() => {});
    const button = Array.from(container.querySelectorAll('button')).find(
      (b) => b.textContent === 'Sign out',
    );
    expect(button).not.toBeNull();
    fireEvent.click(button as HTMLButtonElement);

    expect(submitSpy).toHaveBeenCalledTimes(1);
    const form = document.body.querySelector('form[action="/oauth2/logout"]');
    expect(form).not.toBeNull();
    expect((form as HTMLFormElement).getAttribute('method')?.toLowerCase()).toBe('post');
  });
});
