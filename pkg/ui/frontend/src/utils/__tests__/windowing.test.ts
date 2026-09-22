import { describe, it, expect } from 'vitest';
import { computeWindowedRange } from '../windowing';

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
});
