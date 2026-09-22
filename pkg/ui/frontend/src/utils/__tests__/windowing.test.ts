import { describe, it, expect } from 'vitest';
import { computeWindowedRange, buildOffsets, sumHeights, isPinnedToBottom } from '../windowing';

describe('computeWindowedRange', () => {
  // Constants mirror the values used by RequestDetail's virtualized list.
  // Pinning these in the test makes any future tweak to RequestDetail
  // (WINDOW_MESSAGE_ESTIMATED_HEIGHT / WINDOW_OVERSCAN) immediately
  // surface as a test diff to review.
  const ROW_HEIGHT = 220;
  const OVERSCAN = 6;
  const TOTAL = 800;

  describe('viewport math', () => {
    it('returns only overscan messages when the viewport is at the top with zero height', () => {
      // scrollTop=0, clientHeight=0 → first visible row is 0, last visible row is 0
      // → window = [0 - 6, 0 + 6] clamped to [0, 6]
      const r = computeWindowedRange(0, 0, TOTAL, ROW_HEIGHT, OVERSCAN);
      expect(r).toEqual({ start: 0, end: 6 });
    });

    it('renders a full single screen + overscan at scrollTop=0 with a typical viewport', () => {
      // 600px clientHeight / 220px row = ~3 rows visible
      // window = [0 - 6, 3 + 6] = [0, 9]
      const r = computeWindowedRange(0, 600, TOTAL, ROW_HEIGHT, OVERSCAN);
      expect(r).toEqual({ start: 0, end: 9 });
    });

    it('anchors to the tail with overscan when scrolled to the bottom', () => {
      // scrollTop = scrollHeight - clientHeight (e.g., last fully-visible row)
      const scrollTop = (TOTAL - 1) * ROW_HEIGHT;
      const r = computeWindowedRange(scrollTop, ROW_HEIGHT, TOTAL, ROW_HEIGHT, OVERSCAN);
      // first visible row ≈ TOTAL-1 → window starts at TOTAL-1-6=793
      // last visible row ≤ TOTAL → window ends at TOTAL
      expect(r).toEqual({ start: 793, end: 800 });
    });

    it('keeps the rendered window bounded as the user scrolls mid-conversation', () => {
      // The window size is `visibleRows + 2 * overscan`, where
      //   visibleRows ≈ ceil(clientHeight / ROW_HEIGHT).
      // For clientHeight=600, that's ceil(600/220)=3 visible rows + 12
      // overscan rows ≈ 15. Exact count varies by ±1 because of the
      // floor(scrollTop) / ceil(scrollTop+clientHeight) asymmetry. The
      // hard invariant we care about: window count is strictly bounded
      // regardless of scrollTop.
      const clientHeight = 600;
      const maxExpected = Math.ceil(clientHeight / ROW_HEIGHT) + 2 * OVERSCAN + 1;
      const samples = [
        0,
        220 * 100,           // mid-list
        220 * 400,           // mid-list
        220 * 797,           // near end
        220 * 799 - clientHeight, // bottom
      ];
      for (const scrollTop of samples) {
        const r = computeWindowedRange(scrollTop, clientHeight, TOTAL, ROW_HEIGHT, OVERSCAN);
        expect(r.end - r.start, `at scrollTop=${scrollTop}`).toBeGreaterThan(0);
        expect(r.end - r.start, `at scrollTop=${scrollTop}`).toBeLessThanOrEqual(maxExpected);
        // Window must always be a strict subset of total — this is the
        // regression guard for unbounded DOM growth on long conversations.
        expect(r.end - r.start).toBeLessThan(TOTAL);
      }
    });
  });

  describe('clamping', () => {
    it('clamps start to 0 when scrollTop would underflow', () => {
      const r = computeWindowedRange(-1000, 600, TOTAL, ROW_HEIGHT, OVERSCAN);
      expect(r.start).toBe(0);
      expect(r.end).toBeGreaterThan(0);
    });

    it('clamps end to total when scrollTop exceeds scrollHeight', () => {
      const r = computeWindowedRange(10_000_000, 600, TOTAL, ROW_HEIGHT, OVERSCAN);
      expect(r.end).toBe(TOTAL);
    });

    it('never returns start > end', () => {
      for (const scrollTop of [-100, 0, 100, 220, 100_000]) {
        const r = computeWindowedRange(scrollTop, 600, TOTAL, ROW_HEIGHT, OVERSCAN);
        expect(r.start).toBeLessThanOrEqual(r.end);
      }
    });

    it('returns {0, 0} when total is 0 (regression: empty conversation)', () => {
      const r = computeWindowedRange(0, 600, 0, ROW_HEIGHT, OVERSCAN);
      expect(r).toEqual({ start: 0, end: 0 });
    });

    it('returns the entire conversation when total fits inside the window', () => {
      // 10-message conversation, viewport covers everything → all 10 should be visible
      const r = computeWindowedRange(0, 10_000, 10, ROW_HEIGHT, OVERSCAN);
      expect(r).toEqual({ start: 0, end: 10 });
    });
  });

  describe('defensive inputs', () => {
    it.each([
      ['NaN scrollTop', NaN, 600, TOTAL],
      ['NaN clientHeight', 0, NaN, TOTAL],
      ['negative scrollTop', -500, 600, TOTAL],
      ['negative clientHeight', 0, -100, TOTAL],
      ['zero row height (treated as 1)', 0, 600, TOTAL],
    ])('does not throw on %s', (_label, scrollTop, clientHeight, total) => {
      expect(() =>
        computeWindowedRange(scrollTop, clientHeight, total, 0, OVERSCAN),
      ).not.toThrow();
    });

    it('treats a non-finite total as empty', () => {
      expect(computeWindowedRange(0, 600, Number.NaN, ROW_HEIGHT, OVERSCAN)).toEqual({
        start: 0,
        end: 0,
      });
      expect(computeWindowedRange(0, 600, Number.POSITIVE_INFINITY, ROW_HEIGHT, OVERSCAN)).toEqual({
        start: 0,
        end: 0,
      });
    });

    it('treats negative overscan as zero overscan (Math.floor makes it 0)', () => {
      // Overscan = -3 should not produce a window larger than the visible viewport.
      const r = computeWindowedRange(0, 600, TOTAL, ROW_HEIGHT, -3);
      // Without overscan, window = [0, ceil(600/220)] = [0, 3]
      expect(r.start).toBe(0);
      expect(r.end - r.start).toBeLessThanOrEqual(3);
    });
  });

  describe('regression: short conversations render fully', () => {
    it('renders all 10 messages when the viewport fits the conversation', () => {
      // clientHeight=5000 → ceil(5000/220)+6 = 29, clamped to total=10.
      const r = computeWindowedRange(0, 5000, 10, ROW_HEIGHT, OVERSCAN);
      expect(r).toEqual({ start: 0, end: 10 });
    });

    it('renders all 49 messages when the viewport fits the conversation', () => {
      // 49 rows × 220 px ≈ 10780 px. Use a viewport > that so the
      // overscan-driven end clamp lands on `total`, not on the visible
      // math alone.
      const r = computeWindowedRange(0, 12_000, 49, ROW_HEIGHT, OVERSCAN);
      expect(r).toEqual({ start: 0, end: 49 });
    });

    it('renders all 10 messages with a typical desktop viewport (~1000px)', () => {
      // 10 rows × 220 px = 2200 px. 1000 px viewport → ceil(1000/220)=5,
      // end = min(10, 5+6) = 10 (clamped by total).
      const r = computeWindowedRange(0, 1000, 10, ROW_HEIGHT, OVERSCAN);
      expect(r).toEqual({ start: 0, end: 10 });
    });
  });

  describe('regression: overshoot does not produce start > end (jump-back fix)', () => {
    // Symptom 1 fix: a very tall last message pushes the real content
    // height far past the estimate, so scrollTop can legitimately land
    // past the last estimated row. The old code only clamped `end`,
    // leaving `start` at an index > total and returning an empty slice —
    // which collapsed the DOM and triggered the jump-back loop.
    it('clamps start to total when scrollTop is far past the estimated extent', () => {
      const r = computeWindowedRange(10_000_000, 600, TOTAL, ROW_HEIGHT, OVERSCAN);
      expect(r.start).toBeLessThanOrEqual(TOTAL);
      expect(r.end).toBeLessThanOrEqual(TOTAL);
      expect(r.start).toBeLessThanOrEqual(r.end);
    });

    it('returns a non-empty tail-anchored window when scrollTop is far past the estimated extent', () => {
      // With the estimate path (no measurements yet), a huge scrollTop
      // clamps `start` to `total - overscan`, not past it. The window
      // covers the tail so the user sees the last message — the
      // jump-back loop (empty slice → DOM collapse → clamp → loop) is
      // gone.
      const r = computeWindowedRange(10_000_000, 600, TOTAL, ROW_HEIGHT, OVERSCAN);
      expect(r.end).toBe(TOTAL);
      expect(r.end - r.start).toBeGreaterThan(0);
    });

    it('preserves the documented invariant 0 <= start <= end <= total for all sane inputs', () => {
      const samples: Array<[number, number]> = [
        [0, 600],
        [100, 1000],
        [220 * 100, 600],
        [220 * 400, 800],
        [220 * 797, 600],
        [220 * 799, 1000],
        [220 * 1000, 600],
        [220 * 10_000, 1000],
        [Number.MAX_SAFE_INTEGER, 1000],
      ];
      for (const [scrollTop, clientHeight] of samples) {
        const r = computeWindowedRange(scrollTop, clientHeight, TOTAL, ROW_HEIGHT, OVERSCAN);
        expect(r.start, `at scrollTop=${scrollTop}`).toBeGreaterThanOrEqual(0);
        expect(r.start, `at scrollTop=${scrollTop}`).toBeLessThanOrEqual(TOTAL);
        expect(r.end, `at scrollTop=${scrollTop}`).toBeLessThanOrEqual(TOTAL);
        expect(r.start, `at scrollTop=${scrollTop}`).toBeLessThanOrEqual(r.end);
      }
    });
  });

  describe('buildOffsets: prefix-sum height math', () => {
    it('returns all-default offsets when no heights are measured', () => {
      const offsets = buildOffsets(TOTAL, new Map(), ROW_HEIGHT);
      expect(offsets.length).toBe(TOTAL + 1);
      expect(offsets[0]).toBe(0);
      expect(offsets[1]).toBe(ROW_HEIGHT);
      expect(offsets[TOTAL]).toBe(TOTAL * ROW_HEIGHT);
    });

    it('uses measured heights and falls back to default for unmeasured indices', () => {
      // Tall last message: index 799 measures 5000 px.
      const heights = new Map<number, number>([[TOTAL - 1, 5000]]);
      const offsets = buildOffsets(TOTAL, heights, ROW_HEIGHT);
      expect(offsets[0]).toBe(0);
      expect(offsets[TOTAL - 1]).toBe((TOTAL - 1) * ROW_HEIGHT);
      // offsets[total] = sum of all heights = 220*799 + 5000 = 180780
      expect(offsets[TOTAL]).toBe((TOTAL - 1) * ROW_HEIGHT + 5000);
    });

    it('treats non-positive measured heights as missing (falls back to default)', () => {
      // Index 2 has a valid measurement (500 px); indices 0 and 1 do not.
      const heights = new Map<number, number>([
        [0, 0],
        [1, -10],
        [2, 500],
      ]);
      const offsets = buildOffsets(3, heights, ROW_HEIGHT);
      // Index 0 → default 220, index 1 → default 220, index 2 → measured 500.
      expect(offsets).toEqual([0, ROW_HEIGHT, 2 * ROW_HEIGHT, 2 * ROW_HEIGHT + 500]);
    });

    it('sumHeights matches offsets[end] for any end index', () => {
      const heights = new Map<number, number>([
        [0, 100],
        [5, 400],
        [9, 250],
      ]);
      const offsets = buildOffsets(10, heights, ROW_HEIGHT);
      for (const end of [0, 1, 3, 6, 10]) {
        expect(sumHeights(end, heights, ROW_HEIGHT)).toBe(offsets[end]);
      }
    });
  });

  describe('computeWindowedRange with measuredOffsets (binary-search path)', () => {
    // When measured offsets are passed in, the function uses binary search
    // (O(log N)) and the real bubble positions — not the uniform estimate.
    it('finds the correct window for a tall-last-message scenario', () => {
      const heights = new Map<number, number>([[TOTAL - 1, 5000]]);
      const offsets = buildOffsets(TOTAL, heights, ROW_HEIGHT);
      // Total content height = 180780.
      expect(offsets[TOTAL]).toBe(180780);

      // Scrolled to the bottom: scrollTop = 180780 - 1000 = 179780.
      const r = computeWindowedRange(179780, 1000, TOTAL, ROW_HEIGHT, OVERSCAN, offsets);
      // The window must cover the tail (last message is in [start, end)).
      expect(r.end).toBe(TOTAL);
      // And must NOT be empty — we want the user to see the last message.
      expect(r.end - r.start).toBeGreaterThan(0);
      expect(r.start).toBeGreaterThan(700);
    });

    it('finds the start index accurately when real heights vary', () => {
      // Mixed heights: indices 0..99 measure 300 px, 100..799 measure 100 px.
      const heights = new Map<number, number>();
      for (let i = 0; i < 100; i++) heights.set(i, 300);
      for (let i = 100; i < TOTAL; i++) heights.set(i, 100);
      const offsets = buildOffsets(TOTAL, heights, ROW_HEIGHT);
      // offsets[100] = 100*300 = 30000; offsets[800] = 30000 + 700*100 = 100000.

      // scrollTop = 50000 → 50% through the second half.
      const r = computeWindowedRange(50000, 1000, TOTAL, ROW_HEIGHT, OVERSCAN, offsets);
      // upperBound(offsets, 50000) = 301 (offsets[300] = 50000 ≤ 50000 < offsets[301] = 50100).
      // start = 301 - 1 - 6 = 294.
      expect(r.start).toBeGreaterThanOrEqual(290);
      expect(r.start).toBeLessThanOrEqual(310);
      // lowerBound(offsets, 51000) = 310 (offsets[310] = 51000).
      // end = min(800, 310 + 6) = 316.
      expect(r.end).toBeGreaterThanOrEqual(310);
      expect(r.end).toBeLessThanOrEqual(330);
      // Sanity: the window covers ~22 rows (visibleRows + 2*overscan ≈ 12 + 10 + 2).
      expect(r.end - r.start).toBeGreaterThan(0);
      expect(r.end - r.start).toBeLessThan(50);
    });

    it('falls back to estimate math when measuredOffsets is missing entries', () => {
      // Pass an offsets array that's too short — function must NOT crash.
      const tooShort = [0, 220]; // length 2, but total = 800
      expect(() =>
        computeWindowedRange(100, 600, TOTAL, ROW_HEIGHT, OVERSCAN, tooShort),
      ).not.toThrow();
    });

    it('falls back to estimate math when measuredOffsets is undefined', () => {
      const a = computeWindowedRange(100, 600, TOTAL, ROW_HEIGHT, OVERSCAN, undefined);
      const b = computeWindowedRange(100, 600, TOTAL, ROW_HEIGHT, OVERSCAN);
      expect(a).toEqual(b);
    });

    it('clamps to a tail window when raw scrollTop overshoots far past the measured total', () => {
      // Regression guard for symptom 1 (overshoot → empty slice → DOM
      // collapse). When the user (or a buggy caller) hands us a
      // scrollTop that is far past any real content height, the
      // measured-path math (binary search over `offsets`) must STILL
      // produce a valid tail window — `end` clamps to `total`, and the
      // overscan pulls `start` back into the tail.
      const heights = new Map<number, number>([[TOTAL - 1, 5000]]);
      const offsets = buildOffsets(TOTAL, heights, ROW_HEIGHT);
      // Sanity: total measured content height = 180_780.
      expect(offsets[TOTAL]).toBe(180780);

      const r = computeWindowedRange(10_000_000, 600, TOTAL, ROW_HEIGHT, OVERSCAN, offsets);
      // Hard invariants: a valid, non-empty, in-bounds, tail-anchored slice.
      expect(r.end).toBeLessThanOrEqual(TOTAL);
      expect(r.start).toBeLessThanOrEqual(r.end);
      expect(r.end - r.start).toBeGreaterThan(0);
      // Tail anchor: the last messages must be in the visible window.
      expect(r.end).toBe(TOTAL);
      expect(r.start).toBeGreaterThan(700);
    });
  });

  describe('isPinnedToBottom predicate (re-anchor gate)', () => {
    it('returns true at the exact bottom (scrollTop + clientHeight === scrollHeight)', () => {
      expect(isPinnedToBottom(175000, 1000, 176000)).toBe(true);
    });

    it('returns true within the threshold of the bottom', () => {
      // 8 px from the bottom is inside the default 16 px threshold.
      expect(isPinnedToBottom(174992, 1000, 176000)).toBe(true);
    });

    it('returns false when more than the threshold above the bottom', () => {
      // 100 px from the bottom is outside the 16 px threshold.
      expect(isPinnedToBottom(174900, 1000, 176000)).toBe(false);
    });

    it('honors a custom threshold', () => {
      expect(isPinnedToBottom(174900, 1000, 176000, 200)).toBe(true);
      expect(isPinnedToBottom(174900, 1000, 176000, 50)).toBe(false);
    });

    it('returns false on degenerate / non-finite inputs (defensive)', () => {
      expect(isPinnedToBottom(NaN, 1000, 176000)).toBe(false);
      expect(isPinnedToBottom(-1, 1000, 176000)).toBe(false);
      expect(isPinnedToBottom(1000, -1, 176000)).toBe(false);
    });
  });
});
