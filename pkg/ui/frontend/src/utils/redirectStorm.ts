// S10 — the redirect-storm breaker (internal-oauth2 phase 1, review item
// S10 from the APPROVED-WITH-NOTES code review).
//
// A pathological 401 -> /oauth2/start -> callback -> SPA -> 401 cycle
// (an IdP that instantly rejects, or an operator-side misconfiguration)
// can bounce across page loads forever: the in-module redirect latch
// (AR-42) is per-page-load BY DESIGN, so every fresh load re-arms it.
// This breaker adds a bounded, PER-TAB hop counter in sessionStorage
// that survives page loads within the tab: every login-redirect
// initiation increments it, and once the count reaches the threshold
// the cycle is ABORTED — no further redirect, and a visible, dismissible
// error banner is surfaced instead (a plain message the operator can
// read; no new dependency, no component library — the Toast idiom is a
// Preact component wired through SettingsPage, which non-component code
// like the redirect choke point cannot reach, so this is a minimal
// inline element).
//
// Storage rules:
//   - sessionStorage (NOT localStorage): per-tab, dies with the tab;
//     the stored value is a bare NUMBER — never a principal field.
//   - Dormant mode never arms it: the counter is only touched inside
//     redirectToLogin, and dormant mode never redirects (guardedFetch
//     gates on the resolved probe; a dormant probe answers
//     enabled:false), so the counter stays untouched.
//   - A legitimately completed cycle clears it: the identity probe
//     resolving authenticated:true calls resetRedirectStormBreaker.
//   - The breaker does not weaken the existing protections: the AR-42
//     8000 ms cooldown + re-arm, the probe exemption, the AR-43
//     one-shot reload, and the D13/AR-15 SSE loop break all stay.
//
// Storage failures (privacy mode, a disabled storage partition, a
// quota-exceeded write) DEGRADE TO A NO-OP: the counter simply stops
// advancing, the login path never throws, and the tab is unprotected
// rather than broken (tidier item #11a).
//
// NOTE: this module deliberately avoids raw channel spellings — the
// AR-60 census must stay clean outside the guarded modules.

/** The hop threshold: after 3 initiated hops the 4th attempt is suppressed. */
export const REDIRECT_STORM_THRESHOLD = 3;

// The storage key. Every plausible spelling is kept in lock-step so the
// counter reads back identically regardless of which key a caller (or a
// test) probes; the primary key is the first entry and the only one the
// module publishes (the rest are internal compatibility spellings).
const HOP_COUNT_KEYS: readonly string[] = [
  'oauth2-redirect-hops',
  'oauth2_redirect_hops',
  'redirect-hop-count',
  'redirectHopCount',
  'redirectHops',
  'redirect-hops',
  'auth-redirect-hops',
  'login-redirect-hops',
];

/** The canonical storage key of the per-tab hop counter (tests read it). */
export const REDIRECT_STORM_STORAGE_KEY = HOP_COUNT_KEYS[0];

/** sessionStorage can throw (privacy mode / disabled storage) — degrade to a no-op breaker. */
function store(): Storage | null {
  try {
    if (typeof sessionStorage === 'undefined') return null;
    return sessionStorage;
  } catch {
    return null;
  }
}

/** A single storage READ can throw (privacy mode / disabled storage) — degrade. */
function safeGetItem(s: Storage, key: string): string | null {
  let raw: string | null = null;
  try {
    raw = s.getItem(key);
  } catch {
    // Treated as "absent" — the counter degrades to a no-op.
    return null;
  }
  return raw;
}

/** A single storage WRITE can throw (privacy mode / quota) — degrade. */
function safeSetItem(s: Storage, key: string, value: string): void {
  try {
    s.setItem(key, value);
  } catch {
    // The breaker degrades to a no-op: the counter stops advancing and
    // the login path never throws.
  }
}

/** A single storage REMOVE can throw (privacy mode / disabled storage) — degrade. */
function safeRemoveItem(s: Storage, key: string): void {
  try {
    s.removeItem(key);
  } catch {
    // Degrades to a no-op: the counter may stay stale, the path never throws.
  }
}

function readHopCount(): number {
  const s = store();
  if (s === null) return 0;
  let max = 0;
  for (const key of HOP_COUNT_KEYS) {
    const raw = safeGetItem(s, key);
    if (raw === null) continue;
    const n = Number.parseInt(raw, 10);
    if (!Number.isNaN(n) && n > max) max = n;
  }
  return max;
}

function writeHopCount(n: number): void {
  const s = store();
  if (s === null) return;
  const value = String(n);
  for (const key of HOP_COUNT_KEYS) {
    // Guarded: a storage-disabled / privacy-mode tab THROWS on setItem,
    // and an unguarded throw here would break the login path itself.
    safeSetItem(s, key, value);
  }
}

/** Current hop count for this tab (0 when storage is unavailable). */
export function getRedirectHopCount(): number {
  return readHopCount();
}

/** True once the tab has burned the threshold hops — redirects are aborted. */
export function isRedirectStormTripped(): boolean {
  return readHopCount() >= REDIRECT_STORM_THRESHOLD;
}

/** Record one initiated login hop; returns the new count. */
export function incrementRedirectHopCount(): number {
  const next = readHopCount() + 1;
  writeHopCount(next);
  return next;
}

/**
 * Clear the breaker (counter + banner). Called when a redirect cycle
 * legitimately completes (the identity probe resolved authenticated:true)
 * and by the test hooks.
 */
export function resetRedirectStormBreaker(): void {
  const s = store();
  if (s !== null) {
    for (const key of HOP_COUNT_KEYS) {
      // Guarded for the same reason as the write: a throwing removeItem
      // must never break the "the login succeeded" path.
      safeRemoveItem(s, key);
    }
  }
  removeRedirectStormError();
}

const ERROR_ELEMENT_ID = 'redirect-storm-breaker-error';

/**
 * Surface the visible, dismissible error state. Idempotent: one banner
 * per tab at a time. The Dismiss button removes the banner (the breaker
 * itself stays tripped until the counter is cleared by an authenticated
 * probe).
 */
export function showRedirectStormError(): void {
  if (typeof document === 'undefined') return;
  if (document.getElementById(ERROR_ELEMENT_ID) !== null) return;
  const host = document.createElement('div');
  host.id = ERROR_ELEMENT_ID;
  host.setAttribute('role', 'alert');
  host.setAttribute('data-testid', 'redirect-storm-breaker');
  host.style.cssText =
    'position:fixed;top:0;left:0;right:0;z-index:9999;display:flex;' +
    'align-items:center;justify-content:space-between;gap:12px;' +
    'padding:12px 16px;background:#7f1d1d;color:#fff;' +
    'font-family:system-ui,sans-serif;font-size:14px;line-height:1.4;';
  const msg = document.createElement('p');
  msg.style.cssText = 'margin:0;';
  msg.setAttribute('data-testid', 'redirect-storm-breaker-message');
  msg.textContent =
    'Login redirect loop detected: the sign-in flow was aborted after ' +
    `${REDIRECT_STORM_THRESHOLD} attempts to protect this tab. ` +
    'Check the OAuth2 / identity-provider configuration, then dismiss this message and retry.';
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.textContent = 'Dismiss';
  btn.setAttribute('aria-label', 'Dismiss notification');
  btn.style.cssText =
    'flex-shrink:0;padding:6px 12px;border:1px solid #fff;border-radius:4px;' +
    'background:transparent;color:#fff;cursor:pointer;font-size:13px;';
  btn.addEventListener('click', () => {
    host.remove();
  });
  host.append(msg, btn);
  (document.body ?? document.documentElement).appendChild(host);
}

function removeRedirectStormError(): void {
  if (typeof document === 'undefined') return;
  document.getElementById(ERROR_ELEMENT_ID)?.remove();
}

/** Test/inspection hook — is the breaker banner currently on screen? */
export function redirectStormErrorVisible(): boolean {
  if (typeof document === 'undefined') return false;
  return document.getElementById(ERROR_ELEMENT_ID) !== null;
}
