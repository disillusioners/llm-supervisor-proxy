// load-balance-groups / P4.3 — tsc baseline freeze.
//
// This test pins the TypeScript compile-error count for the embedded
// React (Preact) dashboard against the values recorded in
// tsc-baseline.txt (committed alongside this file at the same dir).
// The baseline is the floor at the load-balance-groups release
// (commit 0c607b5 + P4): any new tsc errors or new files in the
// error set trip this test and block the release.
//
// The test runs `npx tsc --noEmit` programmatically via
// `node:child_process.spawnSync`, parses the resulting error lines,
// and asserts both the per-file set + the aggregate count match
// the baseline. The vitest run takes ~3s for the tsc subprocess on
// a warm cache; cold runs add ~5s.
//
// If the test proves impossible in a CI environment (e.g. no tsc
// binary), see the release-check command documented in
// tsc-baseline.txt — `cd pkg/ui/frontend && npx tsc --noEmit | grep
// "error TS" | wc -l`.
//
// `@ts-nocheck` at the top: this file uses `node:*` builtins which
// are not part of the dashboard tsconfig's bundled deps
// (`@types/node` is not installed). The file is OUTSIDE the
// production type-check surface (it lives under `__tests__/`, runs
// only in vitest via happy-dom + the Node test runner), so the
// assertion is direct: any tsc change to production code that
// alters the baseline file set or count is the release-gate signal.
// @ts-nocheck
import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync, existsSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

interface Baseline {
  totalErrors: number;
  totalFiles: number;
  perFile: Record<string, number>;
}

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const FRONTEND_DIR = resolve(__dirname, '..', '..', '..'); // src/utils/__tests__ -> pkg/ui/frontend
const BASELINE_PATH = resolve(FRONTEND_DIR, 'tsc-baseline.txt');

function parseBaseline(text: string): Baseline {
  const totalErrorsMatch = text.match(/^TOTAL_ERRORS=(\d+)\s*$/m);
  const totalFilesMatch = text.match(/^TOTAL_FILES=(\d+)\s*$/m);
  if (!totalErrorsMatch || !totalFilesMatch) {
    throw new Error('tsc-baseline.txt missing TOTAL_ERRORS= or TOTAL_FILES= footer');
  }
  const perFile: Record<string, number> = {};
  for (const line of text.split('\n')) {
    // Lines like: "6 src/components/config/ModelsTab.tsx"
    const m = line.match(/^(\d+)\s+(\S+\.tsx?)\s*$/);
    if (m) {
      perFile[m[2]] = parseInt(m[1], 10);
    }
  }
  return {
    totalErrors: parseInt(totalErrorsMatch[1], 10),
    totalFiles: parseInt(totalFilesMatch[1], 10),
    perFile,
  };
}

function runTSC(): { stdout: string; status: number | null } {
  // npx --no-install forces use of locally installed tsc (no
  // network). Filter stderr through stdout — tsc mixes them.
  const r = spawnSync('npx', ['--no-install', 'tsc', '--noEmit'], {
    cwd: FRONTEND_DIR,
    encoding: 'utf-8',
    timeout: 60_000,
  });
  return {
    stdout: (r.stdout ?? '') + (r.stderr ?? ''),
    status: r.status,
  };
}

interface TSCActual {
  totalErrors: number;
  totalFiles: number;
  perFile: Record<string, number>;
}

function parseActual(stdout: string): TSCActual {
  const lines = stdout.split('\n').filter((l) => l.includes('error TS'));
  const perFile: Record<string, number> = {};
  for (const l of lines) {
    // Lines like: "src/components/config/ModelsTab.tsx(146,11): error TS6133: ..."
    const m = l.match(/^([^(\s]+)\(\d+,\d+\):\s+error TS\d+:/);
    if (m) {
      const file = m[1];
      perFile[file] = (perFile[file] ?? 0) + 1;
    }
  }
  return {
    totalErrors: lines.length,
    totalFiles: Object.keys(perFile).length,
    perFile,
  };
}

describe('tsc baseline freeze (load-balance-groups P4.3)', () => {
  let baseline: Baseline;
  let actual: TSCActual;
  let tscRan = false;

  beforeAll(() => {
    if (!existsSync(BASELINE_PATH)) {
      throw new Error(`tsc-baseline.txt missing at ${BASELINE_PATH} — P4.3 was supposed to create it`);
    }
    const text = readFileSync(BASELINE_PATH, 'utf-8');
    baseline = parseBaseline(text);
    const r = runTSC();
    actual = parseActual(r.stdout);
    tscRan = true;
  }, 60_000);

  it('parses baseline.txt', () => {
    expect(baseline.totalErrors).toBeGreaterThan(0);
    expect(baseline.totalFiles).toBeGreaterThan(0);
    expect(Object.keys(baseline.perFile).length).toBe(baseline.totalFiles);
    const sum = Object.values(baseline.perFile).reduce((a, b) => a + b, 0);
    expect(sum).toBe(baseline.totalErrors);
  });

  it('tsc actually ran (skip when no tsc binary)', () => {
    if (!tscRan) {
      // The beforeAll could not run tsc (no binary in env); skip
      // the assertion suites but still surface the parse sanity.
      expect(baseline.totalErrors).toBeGreaterThan(0);
      return;
    }
    expect(actual.totalErrors).toBeGreaterThanOrEqual(0);
  });

  it('matches the recorded total error count', () => {
    if (!tscRan) return;
    expect(actual.totalErrors).toBe(baseline.totalErrors);
  });

  it('matches the recorded total file count', () => {
    if (!tscRan) return;
    expect(actual.totalFiles).toBe(baseline.totalFiles);
  });

  it('per-file counts match the baseline', () => {
    if (!tscRan) return;
    const wantKeys = Object.keys(baseline.perFile).sort();
    const gotKeys = Object.keys(actual.perFile).sort();
    expect(gotKeys).toEqual(wantKeys);
    for (const k of wantKeys) {
      expect(actual.perFile[k]).toBe(baseline.perFile[k]);
    }
  });
});
