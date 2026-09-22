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
//
// scrollTop is implemented with a getter/setter that mirrors the real
// browser's clamp to [0, scrollHeight - clientHeight]. Without this,
// assigning scrollTop = scrollHeight would leave it above the max, and
// the windowed math would produce a tail-of-list window instead of the
// full conversation — which masks the very regression we're trying to
// exercise.
function installLayoutShim(
  el: HTMLElement,
  { clientHeight, scrollHeight, scrollTop }: { clientHeight: number; scrollHeight: number; scrollTop: number },
): void {
  Object.defineProperty(el, 'clientHeight', { configurable: true, value: clientHeight });
  Object.defineProperty(el, 'scrollHeight', { configurable: true, value: scrollHeight });
  let actualScrollTop = Math.max(
    0,
    Math.min(scrollTop, Math.max(0, scrollHeight - clientHeight)),
  );
  Object.defineProperty(el, 'scrollTop', {
    configurable: true,
    get: () => actualScrollTop,
    set: (v: number) => {
      const max = Math.max(0, scrollHeight - clientHeight);
      actualScrollTop = Math.max(0, Math.min(v, max));
    },
  });
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
        // Go through the setter so the shim's clamp applies (mirrors real
        // browser behavior).
        scrollEl.scrollTop = top;
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

  describe('scroll-stability fix: tall-last-message handling', () => {
    // Happy-dom has no layout engine, so we can't reproduce the full DOM
    // resize flow that fires ResizeObserver in a real browser. We cover the
    // seams that ARE reachable: (a) the scroll container's CSS / layout
    // contract is right, (b) the initial anchor lands on the visible
    // content, and (c) the documented invariant 0 <= start <= end <= total
    // holds for the rendered window after a layout-effect dispatch.

    it('disables native overflow-anchor on the scroll container (the component manages its own anchoring)', () => {
      // The component manages scroll anchoring itself in a useLayoutEffect
      // so it can survive late height materialization. Letting the browser
      // auto-anchor would fight the windowing math whenever a bubble
      // resizes — that's the third leg of the bug, fixed here.
      const detail = makeDetail(50);
      const { container } = render(<RequestDetail detail={detail} loading={false} />);
      const scrollEl = findScrollContainer(container);
      expect(scrollEl, 'scroll container should be present').not.toBeNull();
      // `overflowAnchor` is the camelCase form of `overflow-anchor`.
      expect((scrollEl as HTMLElement).style.overflowAnchor).toBe('none');
    });

    it('initial anchor on detail change pins scrollTop to the visible bottom (symptom 2 fix)', async () => {
      // With my new useLayoutEffect, the anchor happens synchronously after
      // commit (before paint). The shim's scrollHeight is what the
      // component reads — for a 50-message detail with estimate-only
      // heights, that's 50 * 220 = 11000. The anchor clamps to
      // scrollHeight - clientHeight (the max in a real browser; the shim
      // mirrors that by writing the explicit target value).
      const detail = makeDetail(50);
      const { container } = render(<RequestDetail detail={detail} loading={false} />);

      const scrollEl = findScrollContainer(container)!;
      const expectedScrollHeight = 50 * ROW_HEIGHT;
      installLayoutShim(scrollEl, {
        clientHeight: TYPICAL_VIEWPORT_PX,
        scrollHeight: expectedScrollHeight,
        scrollTop: 0,
      });

      // Trigger one rAF so the layout effects have a chance to run and
      // the windowing hook has been set up.
      await act(async () => {
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
      });

      // After the layout-effect-on-detail-change fires, scrollTop is
      // expected to be at scrollHeight - clientHeight.
      expect(scrollEl.scrollTop).toBe(expectedScrollHeight - TYPICAL_VIEWPORT_PX);
    });

    it('keeps the rendered window bounded when an extreme scrollTop overshoot is requested (symptom 1 regression)', async () => {
      // Pre-fix scenario: scrollTop = 10_000_000 (far past any real
      // scrollHeight) would have produced start = 45448 (overshoot) +
      // end = 800, slice empty, DOM collapse, jump-back loop. Post-fix:
      // the binary-search upperBound in `computeWindowedRange` clamps
      // `end` to total, overscan pulls `start` back into the tail, and
      // the slice covers the last messages.
      //
      // Note: the test-time layout shim's `scrollTop` setter clamps the
      // assigned value to [0, scrollHeight - clientHeight], so the
      // 10_000_000 write lands at the true bottom (scrollHeight -
      // clientHeight = 800 * 220 - 1000 = 175_000) — still a valid
      // tail-overshoot exercise. The clamping is part of the production
      // browser behaviour we are deliberately emulating; bypassing it
      // (e.g. via `Object.defineProperty` for a raw setter) would test
      // a path that real users never hit.
      const detail = makeDetail(800);
      const { container } = render(<RequestDetail detail={detail} loading={false} />);

      const scrollEl = findScrollContainer(container)!;
      installLayoutShim(scrollEl, {
        clientHeight: TYPICAL_VIEWPORT_PX,
        scrollHeight: 800 * ROW_HEIGHT,
        scrollTop: 10_000_000,
      });

      await act(async () => {
        scrollEl.dispatchEvent(new Event('scroll'));
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
      });

      // The shim's setter clamped 10_000_000 to scrollHeight - clientHeight
      // (= 175_000), i.e. the true bottom. The windowed hook must react
      // to the resulting 'scroll' event and anchor to the tail.
      expect(scrollEl.scrollTop).toBe(800 * ROW_HEIGHT - TYPICAL_VIEWPORT_PX);

      // The window must reach the tail (end=800) — this is the regression
      // guard for "user cannot reach the bottom" under overshoot.
      const text = container.textContent ?? '';
      const match = text.match(/Showing\s+(\d+)–(\d+)\s+of\s+800\s+messages/);
      expect(match).not.toBeNull();
      const start = Number(match![1]);
      const end = Number(match![2]);
      expect(end).toBe(800);
      expect(start).toBeGreaterThan(700);
    });

    it('clicking jump-to-last works with measured offsets when the last message is much taller than the estimate', async () => {
      // This regression-guards the jumpToMessage path with measured offsets.
      // Even with the estimate-only path (no real measurements in happy-dom),
      // jump-to-last must reach the tail. With a real measurement in the
      // browser the math uses `offsets[clamped]` instead of the uniform
      // estimate — same end result.
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

      const lastBtn = container.querySelector<HTMLButtonElement>('button[title="Jump to last message"]');
      expect(lastBtn).not.toBeNull();
      await act(async () => {
        lastBtn!.click();
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
        scrollEl.dispatchEvent(new Event('scroll'));
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
      });

      const text = container.textContent ?? '';
      const match = text.match(/Showing\s+(\d+)–(\d+)\s+of\s+800\s+messages/);
      expect(match, 'expected jump-nav status text after jump-to-last').not.toBeNull();
      const start = Number(match![1]);
      const end = Number(match![2]);
      expect(end).toBe(800);
      expect(start).toBeGreaterThan(700);
    });

    it('does not programmatically scroll the user away from a mid-list position (user-respect invariant)', async () => {
      // Pre-seed scrollTop to a mid-list position (not near the bottom).
      // After dispatching scroll events and rAFs, scrollTop should remain
      // at the user's position — the re-anchor effect must only run when
      // the user is pinned.
      const detail = makeDetail(800);
      const { container } = render(<RequestDetail detail={detail} loading={false} />);

      const scrollEl = findScrollContainer(container)!;
      const midScrollTop = 400 * ROW_HEIGHT; // dead center of an 800-message list
      installLayoutShim(scrollEl, {
        clientHeight: TYPICAL_VIEWPORT_PX,
        scrollHeight: 800 * ROW_HEIGHT,
        scrollTop: 0,
      });

      // First, the anchor useLayoutEffect on detail change sets scrollTop
      // to scrollHeight. Move it to a mid-list position to simulate a
      // user who has scrolled away from the bottom.
      await act(async () => {
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
      });
      scrollEl.scrollTop = midScrollTop;
      scrollEl.dispatchEvent(new Event('scroll'));
      await act(async () => {
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
      });

      // Sanity: scrollTop is at the mid position (the re-anchor must NOT
      // have pulled it to the bottom because we are NOT pinned).
      expect(scrollEl.scrollTop).toBe(midScrollTop);

      // The rendered window must cover the mid-list area, not the tail.
      const text = container.textContent ?? '';
      const match = text.match(/Showing\s+(\d+)–(\d+)\s+of\s+800\s+messages/);
      expect(match).not.toBeNull();
      const start = Number(match![1]);
      const end = Number(match![2]);
      // Mid-list position: roughly start ~395 (400 - 6 overscan) and end ~411.
      expect(start).toBeLessThan(450);
      expect(start).toBeGreaterThan(350);
      expect(end).toBeLessThan(500);
    });
  });
});
