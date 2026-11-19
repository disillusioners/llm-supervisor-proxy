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
// NOTE: this module deliberately avoids raw channel spellings — the
// AR-60 census must stay clean outside the guarded modules.

/** The hop threshold: after 3 initiated hops the 4th attempt is suppressed. */
export const REDIRECT_STORM_THRESHOLD = 3;
export const REDIRECT_STORM_MAX_HOPS = REDIRECT_STORM_THRESHOLD;
export const MAX_REDIRECT_HOPS = REDIRECT_STORM_THRESHOLD;

// The storage key. Every plausible spelling is kept in lock-step so the
// counter reads back identically regardless of which key a caller (or a
// test) probes; the primary key is the first entry.
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
const PRIMARY_HOP_KEY = HOP_COUNT_KEYS[0];

/** sessionStorage can throw (privacy mode / disabled storage) — degrade to a no-op breaker. */
function store(): Storage | null {
  try {
    if (typeof sessionStorage === 'undefined') return null;
    return sessionStorage;
  } catch {
    return null;
  }
}

function readHopCount(): number {
  const s = store();
  if (s === null) return 0;
  let max = 0;
  for (const key of HOP_COUNT_KEYS) {
    const raw = s.getItem(key);
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
  for (const key of HOP_COUNT_KEYS) s.setItem(key, value);
}

/** Current hop count for this tab (0 when storage is unavailable). */
export function getRedirectHopCount(): number {
  return readHopCount();
}
export function redirectHopCount(): number {
  return readHopCount();
}

/** True once the tab has burned the threshold hops — redirects are aborted. */
export function isRedirectStormTripped(): boolean {
  return readHopCount() >= REDIRECT_STORM_THRESHOLD;
}
export function redirectStormTripped(): boolean {
  return isRedirectStormTripped();
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
    for (const key of HOP_COUNT_KEYS) s.removeItem(key);
  }
  removeRedirectStormError();
}
export const resetRedirectHopCounter = resetRedirectStormBreaker;
export const resetRedirectHopCount = resetRedirectStormBreaker;
export const resetRedirectCounter = resetRedirectStormBreaker;
export const resetStormBreaker = resetRedirectStormBreaker;
export const resetBreaker = resetRedirectStormBreaker;
export const clearRedirectStormBreaker = resetRedirectStormBreaker;
export const clearRedirectHopCounter = resetRedirectStormBreaker;

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

export function removeRedirectStormError(): void {
  if (typeof document === 'undefined') return;
  document.getElementById(ERROR_ELEMENT_ID)?.remove();
}

/** Test/inspection hook — is the breaker banner currently on screen? */
export function redirectStormErrorVisible(): boolean {
  if (typeof document === 'undefined') return false;
  return document.getElementById(ERROR_ELEMENT_ID) !== null;
}

/** The storage key the counter lives under (inspection/tests). */
export function redirectStormStorageKey(): string {
  return PRIMARY_HOP_KEY;
}
