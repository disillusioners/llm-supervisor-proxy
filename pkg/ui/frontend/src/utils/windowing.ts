// Pure windowing math for the RequestDetail virtualized message list.
//
// Extracted from RequestDetail.tsx so it can be unit-tested deterministically
// without DOM/layout mocking. Behavior is identical to the inline math that
// lives inside `useWindowedMessageRange`; the hook just owns the scroll
// listener / ResizeObserver wiring and delegates the actual computation here.

export interface WindowedRange {
  /** Inclusive start index into the messages array. 0 <= start. */
  start: number;
  /** Exclusive end index into the messages array. start <= end <= total. */
  end: number;
}

/**
 * Compute the slice `[start, end)` of messages to mount for the current
 * scroll position. The result covers the visible viewport plus
 * `overscan` messages on each side.
 *
 * The function is total and side-effect free — safe to call with any inputs
 * (including negatives, NaN, or total === 0).
 *
 * If `measuredOffsets` is provided, `measuredOffsets[i]` is the y-offset
 * (in px) at the top of message `i`, and `measuredOffsets[total]` is the
 * total scroll height of the content. Indices without a measurement are
 * estimated via `estimatedRowHeight * index`. The measured path uses
 * binary search so it stays O(log N) even for very tall conversations.
 *
 * Invariant: 0 <= start <= end <= total. When `scrollTop` exceeds the
 * (estimated or measured) extent, `start` is clamped to `total` so the
 * slice is empty rather than overshooting past the end — this is what
 * prevents the "very tall last message → jump-back" loop.
 */
export function computeWindowedRange(
  scrollTop: number,
  clientHeight: number,
  total: number,
  estimatedRowHeight: number,
  overscan: number,
  measuredOffsets?: ReadonlyArray<number>,
): WindowedRange {
  if (total <= 0 || !Number.isFinite(total)) {
    return { start: 0, end: 0 };
  }

  const safeScrollTop = Number.isFinite(scrollTop) ? Math.max(0, scrollTop) : 0;
  const safeClientHeight = Number.isFinite(clientHeight) ? Math.max(0, clientHeight) : 0;
  const safeRowHeight = estimatedRowHeight > 0 ? estimatedRowHeight : 1;
  const safeOverscan = overscan >= 0 ? Math.floor(overscan) : 0;

  const useMeasured =
    Array.isArray(measuredOffsets) && measuredOffsets.length >= total + 1;

  // startExclusive = smallest index in [0, total+1] with offset > scrollTop.
  // The first VISIBLE row is then `startExclusive - 1`.
  let startExclusive: number;
  if (useMeasured) {
    startExclusive = upperBound(measuredOffsets!, safeScrollTop);
  } else {
    startExclusive = Math.min(total, Math.floor(safeScrollTop / safeRowHeight) + 1);
  }
  // Subtract overscan, clamp to [0, total].
  let start = Math.max(0, Math.min(total, startExclusive - 1 - safeOverscan));

  // endInclusive = smallest index in [0, total] with offset >= scrollTop + clientHeight.
  let endInclusive: number;
  if (useMeasured) {
    endInclusive = lowerBound(measuredOffsets!, safeScrollTop + safeClientHeight);
  } else {
    endInclusive = Math.min(
      total,
      Math.ceil((safeScrollTop + safeClientHeight) / safeRowHeight),
    );
  }
  // Add overscan, clamp to [0, total].
  let end = Math.min(total, endInclusive + safeOverscan);

  // Defensive invariant: end must be >= start. If an aggressive overscan
  // or a degenerate viewport produced start > end, collapse to start
  // (empty slice at the pinned position — caller can render zero rows).
  if (start > end) {
    end = start;
  }

  return { start, end };
}

/**
 * Build a prefix-sum offsets array from a sparse measured-heights map.
 *
 * `offsets[i]` is the y-coordinate (in px) at the top of message `i`.
 * `offsets[total]` is the total content height (= sum of all heights).
 * Unmeasured indices fall back to `defaultRowHeight` so the array is
 * always monotonic and complete.
 *
 * Pure / O(total) — safe to call from a `useMemo`.
 */
export function buildOffsets(
  total: number,
  heights: ReadonlyMap<number, number>,
  defaultRowHeight: number,
): number[] {
  const safeDefault = defaultRowHeight > 0 ? defaultRowHeight : 1;
  const offsets: number[] = new Array(total + 1);
  offsets[0] = 0;
  for (let i = 1; i <= total; i++) {
    const h = heights.get(i - 1);
    offsets[i] = offsets[i - 1] + (h !== undefined && h > 0 ? h : safeDefault);
  }
  return offsets;
}

/** Sum heights for indices [0, end) using a sparse heights map. Unmeasured indices fall back to `defaultRowHeight`. */
export function sumHeights(
  end: number,
  heights: ReadonlyMap<number, number>,
  defaultRowHeight: number,
): number {
  const safeDefault = defaultRowHeight > 0 ? defaultRowHeight : 1;
  let sum = 0;
  for (let i = 0; i < end; i++) {
    const h = heights.get(i);
    sum += h !== undefined && h > 0 ? h : safeDefault;
  }
  return sum;
}

/** Smallest index `i` such that `arr[i] >= target`. Returns `arr.length` if all entries are < target. */
function lowerBound(arr: ReadonlyArray<number>, target: number): number {
  let lo = 0;
  let hi = arr.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if (arr[mid] < target) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

/** Smallest index `i` such that `arr[i] > target`. Returns `arr.length` if all entries are <= target. */
function upperBound(arr: ReadonlyArray<number>, target: number): number {
  let lo = 0;
  let hi = arr.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if (arr[mid] <= target) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

/**
 * "Pinned to bottom" predicate: returns true if the user's viewport
 * bottom is within `threshold` px of the scrollable content's bottom.
 *
 * Extracted as a pure function so the re-anchor decision is unit-testable
 * without a DOM. The hook uses this on each scroll/resize tick to decide
 * whether the next `useLayoutEffect` is allowed to programmatically move
 * `scrollTop` (the answer must be "yes" to keep the user at the bottom
 * across late height materialization, and "no" any time the user has
 * scrolled up).
 */
export function isPinnedToBottom(
  scrollTop: number,
  clientHeight: number,
  scrollHeight: number,
  threshold: number = 16,
): boolean {
  if (scrollTop < 0 || clientHeight < 0 || scrollHeight < 0) return false;
  if (!Number.isFinite(scrollTop) || !Number.isFinite(clientHeight) || !Number.isFinite(scrollHeight)) {
    return false;
  }
  return scrollTop + clientHeight >= scrollHeight - threshold;
}
