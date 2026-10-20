// Shared error-classification vocabulary for the operator-facing TEST
// buttons (the decision probe + the image-generation probe).
//
// It lives in its own module (instead of inside DecisionModelsTab) so
// BOTH tabs classify a failed probe the same way — and so the
// image-gen lane can add a CODE-keyed overlay without touching a
// single decision-lane label.
//
// The resolution order for every probe outcome is:
//
//   1. the CODE label, when the code is known (the code is the more
//      specific signal — e.g. "the upstream refused the generation"),
//   2. the STATUS label (the upstream's own error status is the most
//      actionable class an operator can act on: 401 auth / 402 funds
//      / 429 rate limited / ...),
//   3. the default branch (>= 500 → "Upstream error", else
//      "Unexpected error").
//
// The image-gen lane consults its OWN status override
// (IMGGEN_PROBE_STATUS_CLASSES) between 1 and 2, so a lane-specific
// status never renders the other lane's wording.
//
// NOTE: the shared status map carries the DECISION lane's wording
// verbatim (the decision lane must not change). The image-gen lane
// overrides the labels that are genuinely lane-specific through its
// code overlay and its status override below.

/** PROBE_ERROR_CLASSES maps a classified status to a short,
 * operator-facing label. Anything unmapped falls back to
 * classifyProbeError's default branch. */
export const PROBE_ERROR_CLASSES: Record<number, string> = {
  0: 'Test request failed — the proxy did not answer',
  400: 'Request rejected by the decision adapter',
  401: 'Authentication failed — check the credential API key',
  402: 'Insufficient funds — top up the provider account',
  403: 'Access denied — the credential may not use this model',
  404: 'Not found — check the upstream base URL',
  408: 'Upstream timeout',
  422: 'Endpoint unavailable',
  429: 'Rate limited — retry later',
  500: 'Upstream error',
  502: 'Upstream error',
  503: 'Upstream unavailable — retry later',
  504: 'Upstream timed out',
};

/** classifyProbeError maps a classified status to its label. */
export function classifyProbeError(status: number): string {
  const mapped = PROBE_ERROR_CLASSES[status];
  if (mapped) return mapped;
  if (status >= 500) return 'Upstream error';
  return 'Unexpected error';
}

/** IMGGEN_PROBE_ERROR_CLASSES maps the image-gen probe's stable code
 * slug to its label. The codes are the BE's vocabulary (see
 * pkg/imggen/converter.go — the Code* constants).
 *
 * `upstream_http_error` is DELIBERATELY ABSENT: it is the generic
 * class, and the STATUS label is strictly more informative for it
 * (a 402 must render "Insufficient funds", not "Upstream error").
 * Every slug below is never LESS actionable than its status label,
 * so the overlay can never make the message less actionable. */
export const IMGGEN_PROBE_ERROR_CLASSES: Record<string, string> = {
  upstream_business_error: 'Upstream rejected the request — invalid parameters',
  empty_image: 'No image returned — the upstream accepted the request but produced no images',
  cooldown: 'Rate limited — retry later',
  response_too_large: 'Upstream response too large — the image payload exceeded the cap',
  busy: 'Another test probe is already running — wait for it to finish',
  timeout: 'Upstream timed out',
  upstream_transport_error: 'Could not reach the upstream',
  upstream_malformed: 'The upstream returned an unreadable response',
  upstream_read_failed: 'Could not read the upstream response',
  no_adapter: 'No image-generation adapter for this credential provider',
  preflight_rejected: 'Request rejected by the image-gen adapter',
};

/** IMGGEN_PROBE_STATUS_CLASSES is the image-gen lane's own STATUS
 * override, consulted BEFORE the shared status map
 * (classifyImgGenProbeError). It exists because the shared map
 * carries the DECISION lane's wording: an image-gen upstream 400
 * would otherwise render "Request rejected by the decision adapter"
 * on an image-gen row.
 *
 * The decision lane's wording and behavior are untouched. */
export const IMGGEN_PROBE_STATUS_CLASSES: Record<number, string> = {
  400: 'Request rejected by the image-gen adapter',
};

/** classifyImgGenProbeError maps a classified image-gen probe outcome
 * to its label: the code overlay first (it is the more specific
 * signal), then the lane's status override, then the shared status
 * label. */
export function classifyImgGenProbeError(status: number, code?: string): string {
  if (code) {
    const byCode = IMGGEN_PROBE_ERROR_CLASSES[code];
    if (byCode) return byCode;
  }
  const byStatus = IMGGEN_PROBE_STATUS_CLASSES[status];
  if (byStatus) return byStatus;
  return classifyProbeError(status);
}

/** PROBE_TEST_MAX_MESSAGE caps the upstream message rendered in a
 * probe row (the BE already caps it — this is the belt-and-
 * suspenders guard on the FE side). */
export const PROBE_TEST_MAX_MESSAGE = 300;

/** truncateProbeTestMessage caps the upstream message. */
export function truncateProbeTestMessage(message: string): string {
  if (message.length <= PROBE_TEST_MAX_MESSAGE) return message;
  return `${message.slice(0, PROBE_TEST_MAX_MESSAGE)}…`;
}
