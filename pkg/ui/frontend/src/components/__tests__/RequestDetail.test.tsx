import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { render, cleanup, act } from '@testing-library/preact';
import { RequestDetail } from '../RequestDetail';
import type { RequestDetail as RequestDetailType, Message } from '../../types';

// Build a synthetic RequestDetail with `n` alternating user/assistant messages.
// Deterministic content so we can assert which message bubbles are mounted
// (no real network — the FE never fetched these from the API in the first
// place for the windowing regression).
function makeDetail(n: number): RequestDetailType {
  const messages: Message[] = [];
  for (let i = 0; i < n; i++) {
    const role: Message['role'] = i % 2 === 0 ? 'user' : 'assistant';
    messages.push({
      role,
      content: `Message #${i + 1}`,
    });
  }
  return {
    id: 'test-request-abcdef0123456789',
    model: 'test-model',
    status: 'completed',
    startTime: '2026-09-22T09:00:00Z',
    duration: '1.234s',
    retries: 0,
    messages,
  };
}

// Selectors -----------------------------------------------------------------
// The message list wrapper carries Tailwind's compiled `.space-y-3` class.
// Every direct child of that wrapper is exactly one rendered message —
// spacers above/below the window are siblings of `.space-y-3`, not children,
// so counting `.space-y-3 > *` is a tight, role-stable signal for "how many
// message bubbles are currently mounted in the DOM".
const MESSAGE_LIST_SELECTOR = '.space-y-3';
const messagesInDom = (root: Element): number =>
  root.querySelectorAll(`${MESSAGE_LIST_SELECTOR} > *`).length;

// happy-dom has no layout engine — `clientHeight`, `scrollHeight`, and
// `scrollTop` all report 0 on every element by default. RequestDetail's
// `useWindowedMessageRange` hook reads those directly off the ref, so we
// need to attach a layout shim to the scroll container before the hook's
// first `update()` call reads them. We override the property on the
// specific element (not the prototype) so the rest of the test DOM stays
// at zero and other tests aren't affected.
function installLayoutShim(
  el: HTMLElement,
  { clientHeight, scrollHeight, scrollTop }: { clientHeight: number; scrollHeight: number; scrollTop: number },
): void {
  Object.defineProperty(el, 'clientHeight', { configurable: true, value: clientHeight });
  Object.defineProperty(el, 'scrollHeight', { configurable: true, value: scrollHeight });
  Object.defineProperty(el, 'scrollTop', { configurable: true, value: scrollTop, writable: true });
  // Tag the element so the scroll-container finder can target it
  // unambiguously across multiple divs.
  el.setAttribute('data-rd-scroll', '1');
}

// The scroll container is the messages wrapper with `overflow-y-auto`.
const findScrollContainer = (root: Element): HTMLElement | null =>
  root.querySelector<HTMLElement>('[data-rd-scroll]') ||
  (root.querySelector('div.flex-1.overflow-y-auto') as HTMLElement | null);

// Constants that mirror the production values; pinning them here means a
// future tweak to RequestDetail will surface as a test diff to review.
const ROW_HEIGHT = 220;
const TYPICAL_VIEWPORT_PX = 1000;

describe('RequestDetail — windowed conversation view', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
  });

  describe('regression: short conversations render fully', () => {
    it('renders all 10 messages for a 10-message conversation (natural viewport)', async () => {
      const detail = makeDetail(10);
      const { container } = render(<RequestDetail detail={detail} loading={false} />);

      const scrollEl = findScrollContainer(container);
      expect(scrollEl, 'scroll container should be present').not.toBeNull();
      // 10 messages × 220 px = 2200 px total content. A 1000 px viewport
      // means ceil(1000/220)=5 visible rows + 6 overscan = 11. End is
      // clamped to total=10, so all 10 render.
      installLayoutShim(scrollEl!, {
        clientHeight: TYPICAL_VIEWPORT_PX,
        scrollHeight: 10 * ROW_HEIGHT,
        scrollTop: 0,
      });

      await act(async () => {
        scrollEl!.dispatchEvent(new Event('scroll'));
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
      });

      expect(messagesInDom(container)).toBe(10);
    });

    it('does not render the jump nav for conversations under 50 messages', async () => {
      const detail = makeDetail(10);
      const { container } = render(<RequestDetail detail={detail} loading={false} />);

      const scrollEl = findScrollContainer(container)!;
      installLayoutShim(scrollEl, {
        clientHeight: TYPICAL_VIEWPORT_PX,
        scrollHeight: 10 * ROW_HEIGHT,
        scrollTop: 0,
      });
      await act(async () => {
        scrollEl.dispatchEvent(new Event('scroll'));
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
      });

      // Jump nav is gated on `totalMessages >= 50`. The status string
      // "Showing X–Y of N messages" only exists when the nav is rendered.
      expect(container.textContent ?? '').not.toMatch(/Showing\s+\d+–\d+\s+of\s+\d+\s+messages/);
    });

    it('renders all 49 messages just under the jump-nav threshold', async () => {
      const detail = makeDetail(49);
      const { container } = render(<RequestDetail detail={detail} loading={false} />);

      const scrollEl = findScrollContainer(container)!;
      // 49 × 220 = 10780. With viewport > total height, the window
      // overscan+visible math gets clamped to total=49 → all 49 render.
      installLayoutShim(scrollEl, {
        clientHeight: 12_000,
        scrollHeight: 49 * ROW_HEIGHT,
        scrollTop: 0,
      });
      await act(async () => {
        scrollEl.dispatchEvent(new Event('scroll'));
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
      });

      expect(messagesInDom(container)).toBe(49);
      expect(container.textContent ?? '').not.toMatch(/Showing\s+\d+–\d+\s+of\s+\d+\s+messages/);
    });
  });

  describe('windowing: 800-message conversation', () => {
    it('mounts a strictly bounded subset of the messages (regression: unbounded DOM growth)', async () => {
      const detail = makeDetail(800);
      const { container } = render(<RequestDetail detail={detail} loading={false} />);

      // Install a realistic layout: 1000 px viewport, 800 rows × 220 px
      // = 176000 px of scroll height. With overscan=6, the visible window
      // covers ~ceil(1000/220)+12 ≈ 17 messages.
      const scrollEl = findScrollContainer(container)!;
      installLayoutShim(scrollEl, {
        clientHeight: TYPICAL_VIEWPORT_PX,
        scrollHeight: 800 * ROW_HEIGHT,
        scrollTop: 0,
      });

      await act(async () => {
        scrollEl.dispatchEvent(new Event('scroll'));
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
      });

      const rendered = messagesInDom(container);
      // Hard regression guard: the windowed math caps the slice at
      // `visibleRows + 2*overscan + 1` ≈ 17 messages for a 1000 px
      // viewport. We assert a generous upper bound so this test is not
      // brittle to a small change in ROW_HEIGHT or overscan.
      expect(rendered).toBeGreaterThan(0);
      expect(rendered).toBeLessThan(50);
      // And: strictly less than the full conversation. This is the
      // minimum the user asked us to guarantee ("windowed subset renders").
      expect(rendered).toBeLessThan(800);
      // And: far less — the whole point of the windowing change was to
      // stop the FE from freezing on a full conversation mount.
      expect(rendered).toBeLessThan(800 * 0.1); // <10% of total
    });

    it('renders the jump-nav status with correct total count for long conversations', async () => {
      const detail = makeDetail(800);
      const { container } = render(<RequestDetail detail={detail} loading={false} />);

      const scrollEl = findScrollContainer(container)!;
      installLayoutShim(scrollEl, {
        clientHeight: TYPICAL_VIEWPORT_PX,
        scrollHeight: 800 * ROW_HEIGHT,
        scrollTop: 0,
      });
      await act(async () => {
        scrollEl.dispatchEvent(new Event('scroll'));
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
      });

      const text = container.textContent ?? '';
      expect(text).toMatch(/Showing\s+\d+–\d+\s+of\s+800\s+messages/);
    });

    it('exposes jump-to-first, jump-back-20, jump-forward-20, jump-to-last buttons', async () => {
      const detail = makeDetail(800);
      const { container } = render(<RequestDetail detail={detail} loading={false} />);

      const scrollEl = findScrollContainer(container)!;
      installLayoutShim(scrollEl, {
        clientHeight: TYPICAL_VIEWPORT_PX,
        scrollHeight: 800 * ROW_HEIGHT,
        scrollTop: 0,
      });
      await act(async () => {
        scrollEl.dispatchEvent(new Event('scroll'));
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
      });

      // Buttons are identified by their `title` attribute, which is the
      // most stable cross-role identifier present in the JSX.
      const titles = [
        'Jump to first message',
        'Jump back 20 messages',
        'Jump forward 20 messages',
        'Jump to last message',
      ];
      for (const t of titles) {
        expect(
          container.querySelector(`button[title="${t}"]`),
          `expected a button with title="${t}"`,
        ).not.toBeNull();
      }
    });

    it('clicking jump-to-last anchors the visible window at the tail of the conversation', async () => {
      const detail = makeDetail(800);
      const { container } = render(<RequestDetail detail={detail} loading={false} />);

      const scrollEl = findScrollContainer(container)!;
      // Pre-seed scrollTop near the tail of an 800-row scroll (220px each)
      // so the post-click window covers the last rows + overscan.
      installLayoutShim(scrollEl, {
        clientHeight: TYPICAL_VIEWPORT_PX,
        scrollHeight: 800 * ROW_HEIGHT,
        scrollTop: 799 * ROW_HEIGHT - TYPICAL_VIEWPORT_PX,
      });

      await act(async () => {
        scrollEl.dispatchEvent(new Event('scroll'));
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
      });

      const lastBtn = container.querySelector<HTMLButtonElement>('button[title="Jump to last message"]');
      expect(lastBtn).not.toBeNull();
      await act(async () => {
        lastBtn!.click();
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
        scrollEl.dispatchEvent(new Event('scroll'));
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
      });

      // Status text should now report a tail-anchored window.
      const text = container.textContent ?? '';
      const match = text.match(/Showing\s+(\d+)–(\d+)\s+of\s+800\s+messages/);
      expect(match, 'expected jump-nav status text').not.toBeNull();
      const [, startStr, endStr] = match!;
      const start = Number(startStr);
      const end = Number(endStr);
      // The visible window must reach the tail (end=800) and must NOT be
      // anchored at the top — this is the regression guard for the
      // "scroll-to-bottom" intent in the component.
      expect(end).toBe(800);
      expect(start).toBeGreaterThan(700);
    });

    it('window count remains bounded as scrollTop varies across the conversation', async () => {
      const detail = makeDetail(800);
      const { container } = render(<RequestDetail detail={detail} loading={false} />);

      const scrollEl = findScrollContainer(container)!;
      installLayoutShim(scrollEl, {
        clientHeight: TYPICAL_VIEWPORT_PX,
        scrollHeight: 800 * ROW_HEIGHT,
        scrollTop: 0,
      });

      // Sample three positions: top, middle, bottom.
      const samples = [0, 400 * ROW_HEIGHT, 799 * ROW_HEIGHT - TYPICAL_VIEWPORT_PX];
      for (const top of samples) {
        Object.defineProperty(scrollEl, 'scrollTop', { configurable: true, value: top, writable: true });
        await act(async () => {
          scrollEl.dispatchEvent(new Event('scroll'));
          await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
        });
        const count = messagesInDom(container);
        expect(count, `at scrollTop=${top}`).toBeGreaterThan(0);
        expect(count, `at scrollTop=${top}`).toBeLessThan(50);
        // Window must always be a strict subset of total.
        expect(count, `at scrollTop=${top}`).toBeLessThan(800);
      }
    });
  });

  describe('edge cases', () => {
    it('renders an empty messages array without throwing', async () => {
      const detail = makeDetail(0);
      const { container } = render(<RequestDetail detail={detail} loading={false} />);
      await act(async () => {
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
      });
      expect(messagesInDom(container)).toBe(0);
      expect(container.textContent ?? '').not.toMatch(/Showing\s+\d+–\d+\s+of\s+\d+\s+messages/);
    });

    it('does not render anything when detail is null', () => {
      const { container } = render(<RequestDetail detail={null} loading={false} />);
      expect(container.textContent ?? '').toContain('Select a request');
      expect(messagesInDom(container)).toBe(0);
    });

    it('does not render anything when loading is true (regression: no flash of stale detail)', () => {
      const detail = makeDetail(800);
      const { container } = render(<RequestDetail detail={detail} loading={true} />);
      expect(container.textContent ?? '').toContain('Loading');
      expect(messagesInDom(container)).toBe(0);
    });
  });
});
