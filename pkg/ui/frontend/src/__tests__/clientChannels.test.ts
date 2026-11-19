// AR-60 census (internal-oauth2 phase 1, plan §7.4 / §9.3) — every
// client-initiated request channel must be routed through the guard
// (or be the identity probe itself), and the only stream channel is
// the single SSE constructor in useEvents.
//
// This is the in-process equivalent of the plan's acceptance grep —
// the regex is applied verbatim, per line, to every .ts/.tsx file
// under src/:
//
//   (^|[^A-Za-z])<wrapped-global-call>|<SSE-class>|<socket>|<xhr>|<beacon-ish>|<http-lib>|<service-worker>
//
// (The pattern's channel-name literals are assembled from split
// strings so THIS file stays clean under its own census — the plan's
// acceptance grep, run raw over src/, must return ONLY the guarded
// production modules.)
//
// @ts-nocheck — node:* builtins are outside the dashboard tsconfig's
// bundled deps (same posture as tsc-baseline.test.ts).
import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, existsSync } from 'node:fs';
import { resolve, dirname, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const FRONTEND_DIR = resolve(__dirname, '..', '..'); // src/__tests__ -> pkg/ui/frontend
const SRC_DIR = resolve(FRONTEND_DIR, 'src');

// Assembled from parts — see the file header.
const CENSUS_RX = new RegExp(
  '(^|[^A-Za-z])' + 'fet' + 'ch\\(' +
  '|' + 'Event' + 'Source' +
  '|Web' + 'Socket' +
  '|XMLHttp' + 'Request' +
  '|send' + 'Beacon' +
  '|ax' + 'ios' +
  '|navigator\\.service' + 'Worker',
);

const SSE_NAME = 'Event' + 'Source';

// The complete production allowlist: the guard's own delegation, the
// identity probe, and the single stream module.
const GUARD_FILE = 'src/utils/apiFetch.ts';
const PROBE_HOOK_FILE = 'src/hooks/useAuth.ts';
const STREAM_FILE = 'src/hooks/useEvents.ts';

interface Hit {
  file: string;
  line: number;
  text: string;
}

function walk(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = resolve(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...walk(full));
    } else if (/\.(ts|tsx)$/.test(entry.name)) {
      out.push(full);
    }
  }
  return out;
}

function census(): Hit[] {
  const hits: Hit[] = [];
  expect(existsSync(SRC_DIR)).toBe(true);
  for (const file of walk(SRC_DIR)) {
    const rel = relative(FRONTEND_DIR, file).split('\\').join('/');
    const lines = readFileSync(file, 'utf-8').split('\n');
    for (let i = 0; i < lines.length; i++) {
      if (CENSUS_RX.test(lines[i])) {
        hits.push({ file: rel, line: i + 1, text: lines[i].trim() });
      }
    }
  }
  return hits;
}

describe('AR-60 client-channel census', () => {
  it('every hit lives in the guarded production modules (no unguarded channel anywhere in src/)', () => {
    const hits = census();
    const allowed = new Set([GUARD_FILE, PROBE_HOOK_FILE, STREAM_FILE]);
    const strays = hits.filter((h) => !allowed.has(h.file));
    expect(strays).toEqual([]); // renders the offending file:line:text list on failure
  });

  it('the guard module delegates to the wrapped global exactly once (its own internals)', () => {
    const hits = census().filter((h) => h.file === GUARD_FILE);
    expect(hits.length).toBe(1);
    expect(hits[0].text).toContain('input, init'); // the delegation inside guardedFetch
    expect(hits[0].text).toContain('await');
  });

  it('the identity probe hook performs the one probe request (exempt, unguarded by design)', () => {
    const hits = census().filter((h) => h.file === PROBE_HOOK_FILE);
    expect(hits.length).toBe(1);
    expect(hits[0].text).toContain('AUTH_ME_PATH');
    expect(hits[0].text).toContain('await');
  });

  it('the stream module: exactly ONE constructor + exactly ONE probe request; every hit is one of those two classes', () => {
    const hits = census().filter((h) => h.file === STREAM_FILE);
    const ctorHits = hits.filter((h) => h.text.includes('new ' + SSE_NAME));
    const probeHits = hits.filter((h) => h.text.includes('/fe/api/auth/me'));
    expect(ctorHits.length).toBe(1);
    expect(probeHits.length).toBe(1);
    // Every remaining hit must be a bare class-name reference (state
    // constants / the shared instance type) — never a second channel.
    const other = hits.filter((h) => !h.text.includes(SSE_NAME) && !probeHits.includes(h));
    expect(other).toEqual([]);
  });
});
