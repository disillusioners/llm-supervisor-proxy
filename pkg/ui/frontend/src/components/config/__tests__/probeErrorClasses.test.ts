import { describe, it, expect } from 'vitest';
import {
  PROBE_ERROR_CLASSES,
  PROBE_TEST_MAX_MESSAGE,
  IMGGEN_PROBE_ERROR_CLASSES,
  classifyProbeError,
  classifyImgGenProbeError,
  truncateProbeTestMessage,
} from '../probeErrorClasses';

// The shared probe-error vocabulary.
//
// The decision lane's classifier never had a direct unit test (it was
// only exercised through the tab) — this suite closes that gap for
// the shared module both lanes use.
//
// The contract pinned here:
//   - the STATUS map is the decision lane's map, verbatim (the
//     decision tab's labels must not move),
//   - the CODE overlay only carries labels that are STRICTLY more
//     specific than the status label (a code label must never make
//     the operator's message less actionable),
//   - the resolution order is code → status → default.

describe('probeErrorClasses — the shared probe vocabulary', () => {
  describe('the shared status map', () => {
    it('keeps the decision lane labels verbatim', () => {
      expect(PROBE_ERROR_CLASSES[0]).toBe('Test request failed — the proxy did not answer');
      expect(PROBE_ERROR_CLASSES[400]).toBe('Request rejected by the decision adapter');
      expect(PROBE_ERROR_CLASSES[401]).toBe('Authentication failed — check the credential API key');
      expect(PROBE_ERROR_CLASSES[402]).toBe('Insufficient funds — top up the provider account');
      expect(PROBE_ERROR_CLASSES[403]).toBe('Access denied — the credential may not use this model');
      expect(PROBE_ERROR_CLASSES[404]).toBe('Not found — check the upstream base URL');
      expect(PROBE_ERROR_CLASSES[408]).toBe('Upstream timeout');
      expect(PROBE_ERROR_CLASSES[422]).toBe('Endpoint unavailable');
      expect(PROBE_ERROR_CLASSES[429]).toBe('Rate limited — retry later');
      expect(PROBE_ERROR_CLASSES[500]).toBe('Upstream error');
      expect(PROBE_ERROR_CLASSES[502]).toBe('Upstream error');
      expect(PROBE_ERROR_CLASSES[503]).toBe('Upstream unavailable — retry later');
      expect(PROBE_ERROR_CLASSES[504]).toBe('Upstream timed out');
    });

    it('falls back to the default branch for an unmapped status', () => {
      expect(classifyProbeError(418)).toBe('Unexpected error');
      expect(classifyProbeError(599)).toBe('Upstream error');
      expect(classifyProbeError(402)).toBe(PROBE_ERROR_CLASSES[402]);
    });
  });

  describe('the image-gen code overlay', () => {
    it('carries a label for every code the BE can emit', () => {
      const expected = [
        'upstream_business_error',
        'empty_image',
        'cooldown',
        'response_too_large',
        'request_too_large',
        'busy',
        'timeout',
        'upstream_transport_error',
        'upstream_malformed',
        'no_adapter',
        'preflight_rejected',
      ];
      for (const code of expected) {
        expect(IMGGEN_PROBE_ERROR_CLASSES[code]).toBeTruthy();
      }
    });

    it('does NOT label the generic upstream_http_error — the status label is more specific', () => {
      // A 402 must render "Insufficient funds", not "Upstream error".
      expect(IMGGEN_PROBE_ERROR_CLASSES['upstream_http_error']).toBeUndefined();
      expect(classifyImgGenProbeError(402, 'upstream_http_error')).toBe(
        'Insufficient funds — top up the provider account',
      );
      expect(classifyImgGenProbeError(401, 'upstream_http_error')).toBe(
        'Authentication failed — check the credential API key',
      );
    });

    it('prefers the code label over the status label', () => {
      expect(classifyImgGenProbeError(502, 'upstream_business_error')).toBe(
        'Upstream rejected the request — invalid parameters',
      );
      expect(classifyImgGenProbeError(200, 'empty_image')).toBe(
        'No image returned — the upstream accepted the request but produced no images',
      );
      expect(classifyImgGenProbeError(429, 'busy')).toBe(
        'Another test probe is already running — wait for it to finish',
      );
    });

    it('falls back to the status label for an unknown code', () => {
      expect(classifyImgGenProbeError(402, 'some-future-code')).toBe(
        'Insufficient funds — top up the provider account',
      );
      expect(classifyImgGenProbeError(418, undefined)).toBe('Unexpected error');
    });
  });

  describe('the message cap', () => {
    it('caps the upstream message at the shared limit', () => {
      const long = 'x'.repeat(PROBE_TEST_MAX_MESSAGE + 50);
      const capped = truncateProbeTestMessage(long);
      expect(capped.length).toBe(PROBE_TEST_MAX_MESSAGE + 1);
      expect(capped.endsWith('…')).toBe(true);
    });

    it('leaves a short message untouched', () => {
      expect(truncateProbeTestMessage('short message')).toBe('short message');
    });
  });
});
