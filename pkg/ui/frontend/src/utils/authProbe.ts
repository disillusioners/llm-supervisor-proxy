// authProbe — the ONE shared identity-probe contract (internal-oauth2
// phase 1; tidier item #6 — the probe-parse dedup cluster).
//
// Two call sites consume the identity probe:
//   * hooks/useAuth.ts   — the one-shot probe behind the authState store
//   * hooks/useEvents.ts — the bounded SSE auth probe (D13/AR-15)
// Both used to hand-roll the SAME 401-body interpretation (and each
// carried its own copy of the 3000 ms timeout constant), so the two
// probes could silently drift apart. This module is the single source of
// truth: the endpoint path, the timeout budget, the runtime type guard
// for the untrusted body, and the outcome classification.
//
// Backend contract (plan §5.8 — pinned, do NOT change here):
//   dormant              {"enabled":false,"authenticated":false,"state_store_count":0}
//   enabled + anonymous  {"enabled":true,"authenticated":false,"state_store_count":N}  (HTTP 401)
//   enabled + signed in  {"enabled":true,"authenticated":true,"user":{...},"state_store_count":N}
// `user.preferred_username` DOES NOT EXIST — the FE renders
// `user.name || user.email || user.sub` (AR-33/AR-62).
//
// AR-16: only an EXPLICIT answer is cached. A missing / unparseable body
// or a body without a boolean `enabled` is UNKNOWN — never `{enabled:false}`
// and never cached as such.

/**
 * The identity-probe endpoint (the gate-exempt read-only route).
 *
 * THE single source of truth for the probe path: both probe call sites
 * (hooks/useAuth.ts, hooks/useEvents.ts) request it, and the guard's
 * probe-exemption matcher (utils/apiFetch.isAuthProbe) compares the
 * PARSED PATHNAME of an incoming request against this constant. The
 * path is never spelled out as a literal anywhere else.
 */
export const AUTH_ME_PATH = '/fe/api/auth/me';

/** The probe budget: the request is aborted after this many ms. */
export const AUTH_PROBE_TIMEOUT_MS = 3000;

export interface AuthProbeUser {
  sub: string;
  email?: string;
  name?: string;
}

export interface AuthProbeBody {
  enabled: boolean;
  authenticated: boolean;
  user?: AuthProbeUser;
  state_store_count: number;
}

export type AuthProbeOutcome =
  /** An EXPLICIT answer — the body carried a boolean `enabled`. */
  | { kind: 'answer'; body: AuthProbeBody }
  /** UNKNOWN — the body was missing, unparseable, or carried no boolean
   *  `enabled` (e.g. an EXTERNAL ingress 401 with an HTML body). Never
   *  cached as an answer (AR-16). */
  | { kind: 'unknown' }
  /** The internal mode is demonstrably NOT in charge — the status was
   *  neither 200 nor 401 (a 404 from an older binary, a 5xx). */
  | { kind: 'not-in-charge' };

/**
 * Runtime type guard for the untrusted probe body. The body arrives from
 * the network, so it is NEVER cast with a bare `as` — the shape is
 * checked field by field and a body without a boolean `enabled` is
 * rejected outright (it is not an explicit answer — AR-16).
 */
export function toAuthProbeBody(raw: unknown): AuthProbeBody | null {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return null;
  const rec = raw as Record<string, unknown>;
  if (typeof rec.enabled !== 'boolean') return null;
  const body: AuthProbeBody = {
    enabled: rec.enabled,
    authenticated: rec.authenticated === true,
    state_store_count: typeof rec.state_store_count === 'number' ? rec.state_store_count : 0,
  };
  const user = toAuthProbeUser(rec.user);
  if (user !== null) body.user = user;
  return body;
}

/** The user sub-shape guard — `sub`/`email`/`name` only, never
 *  `preferred_username` (AR-33/AR-62). */
function toAuthProbeUser(raw: unknown): AuthProbeUser | null {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return null;
  const rec = raw as Record<string, unknown>;
  const user: AuthProbeUser = { sub: typeof rec.sub === 'string' ? rec.sub : '' };
  if (typeof rec.email === 'string') user.email = rec.email;
  if (typeof rec.name === 'string') user.name = rec.name;
  return user;
}

/**
 * Parse a probe response into the shared outcome classification.
 *
 *  - 200 → the dormant / signed-in shape.
 *  - 401 → the enabled+anonymous shape (the body carries `enabled:true`).
 *  - any other status → `not-in-charge` (the internal mode is not in
 *    charge — e.g. a 404 from an older binary, or a 5xx).
 *  - an unparseable body, or one without a boolean `enabled` → UNKNOWN.
 */
export async function parseAuthProbeResponse(res: Response): Promise<AuthProbeOutcome> {
  if (res.status !== 200 && res.status !== 401) {
    return { kind: 'not-in-charge' };
  }
  let raw: unknown;
  try {
    raw = await res.json();
  } catch {
    // Unparseable body — UNKNOWN, never a cached `enabled:false`.
    return { kind: 'unknown' };
  }
  const body = toAuthProbeBody(raw);
  return body === null ? { kind: 'unknown' } : { kind: 'answer', body };
}
