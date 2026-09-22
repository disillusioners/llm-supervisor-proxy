// Pure windowing math for the RequestDetail virtualized message list.
//
// Extracted from RequestDetail.tsx so it can be unit-tested deterministically
// without DOM/layout mocking. Behavior is identical to the inline math that
// lives inside `useWindowedMessageRange`; the hook just owns the scroll
// listener / ResizeObserver wiring and delegates the actual computation here.

export interface WindowedRange {
  /** Inclusive start index into the messages array. Always 0 <= start. */
  start: number;
  /** Exclusive end index into the messages array. Always start <= end <= total. */
  end: number;
}

/**
 * Compute the slice `[start, end)` of messages to mount for the current
 * scroll position. The result covers the visible viewport plus
 * `overscan` messages on each side.
 *
 * The function is total and side-effect free — safe to call with any inputs
 * (including negatives, NaN, or total === 0).
 */
export function computeWindowedRange(
  scrollTop: number,
  clientHeight: number,
  total: number,
  estimatedRowHeight: number,
  overscan: number,
): WindowedRange {
  if (total <= 0 || !Number.isFinite(total)) {
    return { start: 0, end: 0 };
  }

  const safeScrollTop = Number.isFinite(scrollTop) ? Math.max(0, scrollTop) : 0;
  const safeClientHeight = Number.isFinite(clientHeight) ? Math.max(0, clientHeight) : 0;
  const safeRowHeight = estimatedRowHeight > 0 ? estimatedRowHeight : 1;
  const safeOverscan = overscan >= 0 ? Math.floor(overscan) : 0;

  const start = Math.max(
    0,
    Math.floor(safeScrollTop / safeRowHeight) - safeOverscan,
  );
  const end = Math.min(
    total,
    Math.ceil((safeScrollTop + safeClientHeight) / safeRowHeight) + safeOverscan,
  );

  return { start, end };
}
