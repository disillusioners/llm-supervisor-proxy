# Credential Groups — FE Static Zero-Modification Proof (RE-GATED)

> ### ⚠️ SUPERSEDES — this listing is the authoritative one
>
> This file **supersedes the first-pass listing** dated 2026-11-17, which was taken at gated
> tip `ad6996aceb5e2c5855dfb00252fa6b64db14e952`.
>
> That first-pass listing is **stale by design**: after it was written, the embedded SPA was
> rebuilt on-branch (`e187e94` "build(credential-groups): rebuild embedded SPA for F-1/T-1.5
> string qualification", then `7ae79c0` "fix(credential-groups): F-1 addendum — qualify the
> unknown-group_id error string", which re-emitted both JS chunks with new content-hash
> filenames). The `pkg/ui/static/assets/` filenames are content-derived, so the whole hash
> listing changed.
>
> **Do not use the prior 4-line listing as gate evidence.** The current, fresh, re-gated proof
> is below, at tip `d5178ebc58a53a704661e234d449471d2f6ba0be`. The prior listing is retained
> only in §8 (changed-vs-prior comparison) for provenance.

**Date:** 2026-11-17
**Worktree (the gated artifact):** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-cred-groups`
**Branch:** `feature/credential-groups`
**Re-gated tip SHA:** `d5178ebc58a53a704661e234d449471d2f6ba0be` (verified with `git rev-parse HEAD`, unchanged after the build)
**Supersedes tip:** `ad6996aceb5e2c5855dfb00252fa6b64db14e952` (first-pass listing — stale)
**Verdict:** ✅ **ZERO-MOD-PROVEN**

---

## 1. Verdict

The FE production build (`vite build` → `outDir: '../static'`) was executed against the
re-gated tip `d5178eb`. All four files under `pkg/ui/static/` were rewritten by the build and
are **byte-identical** before and after (sha256 match on every file). The post-build
`git status --porcelain -- pkg/ui/static/` is **EMPTY**.

**No file under `pkg/ui/static/` was modified by the build.** No revert was necessary because
no modification occurred. The build ran **twice** (second run for an explicit npm exit code);
both runs were byte-identical to the committed baseline.

---

## 2. Why the `find` form is authoritative (the glob form is FORBIDDEN)

With `globstar` OFF (bash default), the pattern `pkg/ui/static/**/*` collapses to
`pkg/ui/static/*/*`, which matches only the files **inside** `pkg/ui/static/assets/`.
It silently omits `pkg/ui/static/index.html` — the embed entry point
(`pkg/ui/server.go:88`, `pkg/ui/server.go:90`).

Hashing only 3 of 4 files would make the proof **VACUOUS**: the single file the browser
actually loads to bootstrap the SPA would never be compared.

| Form | Command | File count | Covers `index.html`? |
|------|---------|-----------:|----------------------|
| **AUTHORITATIVE** | `find pkg/ui/static -type f \| sort \| xargs -r sha256sum` | **4** | ✅ **YES** |
| FORBIDDEN (evidence only) | `ls -1 pkg/ui/static/**/* \| wc -l` | **3** | ❌ NO |

**Re-verified by execution at this tip:** glob = 3, find = 4, missing = `index.html`.

---

## 3. Exact commands executed (from the worktree root)

```bash
cd /home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-cred-groups
git rev-parse HEAD
mkdir -p /tmp/rg-gate
echo "=== find count (authoritative) ==="; find pkg/ui/static -type f | wc -l
echo "=== forbidden glob count (evidence only) ==="; ls -1 pkg/ui/static/**/* 2>/dev/null | wc -l
echo "=== build script ==="; sed -n '/"scripts"/,/^  }/p' pkg/ui/frontend/package.json
find pkg/ui/static -type f | sort | xargs -r sha256sum | tee /tmp/rg-gate/static-before.txt
cd pkg/ui/frontend && npm run build 2>&1 | tee /tmp/rg-gate/static-build.log
cd /home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-cred-groups
find pkg/ui/static -type f | sort | xargs -r sha256sum | tee /tmp/rg-gate/static-after.txt
echo "=== DIFF ==="; diff /tmp/rg-gate/static-before.txt /tmp/rg-gate/static-after.txt && echo "BYTE_IDENTICAL" || echo "DRIFT"
echo "=== post-build git status (must be EMPTY) ==="; git status --porcelain -- pkg/ui/static/
echo "=== full git status ==="; git status --porcelain
```

**Second run** (clean npm exit-code capture + reproducibility check):

```bash
bash -c 'cd /home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-cred-groups/pkg/ui/frontend && npm run build > /tmp/rg-gate/static-build2.log 2>&1; echo "NPM_EXIT_CODE=$?"'
find pkg/ui/static -type f | sort | xargs -r sha256sum > /tmp/rg-gate/static-after2.txt
diff /tmp/rg-gate/static-before.txt /tmp/rg-gate/static-after2.txt && echo "BYTE_IDENTICAL" || echo "DRIFT"
```

---

## 4. File-count evidence

### find-count (AUTHORITATIVE) = 4

```
=== find count (authoritative) ===
4
```

### glob-count (FORBIDDEN, evidence only) = 3

```
=== forbidden glob count (evidence only) ===
3
```

### The 4 files (sorted, as hashed)

```
pkg/ui/static/assets/SettingsPage-B3M0PhZL.js
pkg/ui/static/assets/index-Diz8jSFK.css
pkg/ui/static/assets/index-LFDB-8vZ.js
pkg/ui/static/index.html
```

**`pkg/ui/static/index.html` IS among the hashed files** — it is line 4 of both the
BEFORE and the AFTER listing, with sha256
`dffab86a2c6894cd13cfafbe5e8d5bd3243cbadc5adc55910f2743a5995e983f`.
The glob form returns only the three `assets/` files and misses this one.

---

## 5. BEFORE listing (verbatim, 4 lines)

```
659fd5899d298c8da3f2764622487a871eaa1f92932d8d3677f0d4ee03fabaa5  pkg/ui/static/assets/SettingsPage-B3M0PhZL.js
a14e8d1c97d05d693b2d14399b254824b77f1d92af9a7fefdd2f87e2b7f993b2  pkg/ui/static/assets/index-Diz8jSFK.css
0f369f80bb6ba0cb3e52e469b02f509dc97190a4412bdb13d9bad095a9d03cc0  pkg/ui/static/assets/index-LFDB-8vZ.js
dffab86a2c6894cd13cfafbe5e8d5bd3243cbadc5adc55910f2743a5995e983f  pkg/ui/static/index.html
```

---

## 6. AFTER listing (verbatim, 4 lines)

```
659fd5899d298c8da3f2764622487a871eaa1f92932d8d3677f0d4ee03fabaa5  pkg/ui/static/assets/SettingsPage-B3M0PhZL.js
a14e8d1c97d05d693b2d14399b254824b77f1d92af9a7fefdd2f87e2b7f993b2  pkg/ui/static/assets/index-Diz8jSFK.css
0f369f80bb6ba0cb3e52e469b02f509dc97190a4412bdb13d9bad095a9d03cc0  pkg/ui/static/assets/index-LFDB-8vZ.js
dffab86a2c6894cd13cfafbe5e8d5bd3243cbadc5adc55910f2743a5995e983f  pkg/ui/static/index.html
```

---

## 7. Diff verdict

```
=== DIFF ===
BYTE_IDENTICAL
```

**BYTE_IDENTICAL** — zero differing lines across all 4 hashed files.

Second run (reproducibility, `before` vs `after2`):

```
=== DIFF before vs after2 (build #2 reproducibility) ===
BYTE_IDENTICAL
```

---

## 8. Changed-vs-prior-gate hash comparison

Prior gate artifact listing (tip `ad6996ac`, now superseded) vs the current re-gated tree
(tip `d5178eb`):

| File | Prior (`ad6996ac`) | Prior sha256 | Current (`d5178eb`) | Current sha256 | State |
|------|--------------------|---------------|---------------------|----------------|-------|
| Settings chunk | `SettingsPage-DdTORRXZ.js` | `ca607d01fcf80a07e8f3bcc86f348817d6022a89ecf17c3c2d8fb14711fb8dde` | **`SettingsPage-B3M0PhZL.js`** | `659fd5899d298c8da3f2764622487a871eaa1f92932d8d3677f0d4ee03fabaa5` | ✅ **CHANGED** (name + hash) |
| Entry chunk | `index-CLCWY7h7.js` | `73eec8c1383440be383c0eba001b97047b1738a32df746d87716293e6e8a5872` | **`index-LFDB-8vZ.js`** | `0f369f80bb6ba0cb3e52e469b02f509dc97190a4412bdb13d9bad095a9d03cc0` | ✅ **CHANGED** (name + hash) |
| Stylesheet | `index-Diz8jSFK.css` | `a14e8d1c97d05d693b2d14399b254824b77f1d92af9a7fefdd2f87e2b7f993b2` | `index-Diz8jSFK.css` | `a14e8d1c97d05d693b2d14399b254824b77f1d92af9a7fefdd2f87e2b7f993b2` | ⬜ **STABLE** (byte-identical) |
| Embed entry | `index.html` | `44b10be7d2545f01d4e014dfa24f51932d90d1a6c30d56beb919a4a58cd5ab60` | `index.html` | `dffab86a2c6894cd13cfafbe5e8d5bd3243cbadc5adc55910f2743a5995e983f` | ✅ **CHANGED** (same name, new hash) |

**Confirmed exactly as expected:** the tree carries `SettingsPage-B3M0PhZL.js` and
`index-LFDB-8vZ.js` per the on-branch rebuild. 3 of 4 hashes changed; the CSS is the only
stable file.

### Why the hashes changed (git provenance)

Commits touching `pkg/ui/static/` between the superseded tip and the re-gated tip:

```
7ae79c0 fix(credential-groups): F-1 addendum — qualify the unknown-group_id error string
e187e94 build(credential-groups): rebuild embedded SPA for F-1/T-1.5 string qualification
```

Rename/modify map from `7ae79c0`:

```
R095  pkg/ui/static/assets/SettingsPage-Cnplp6lc.js → pkg/ui/static/assets/SettingsPage-B3M0PhZL.js
R099  pkg/ui/static/assets/index-DLNqg_z1.js     → pkg/ui/static/assets/index-LFDB-8vZ.js
M     pkg/ui/static/index.html
```

Static tree at each point on the branch:

| Tip | `pkg/ui/static/` contents |
|-----|---------------------------|
| `ad6996ac` (superseded) | `SettingsPage-DdTORRXZ.js`, `index-CLCWY7h7.js`, `index-Diz8jSFK.css`, `index.html` |
| `e187e94` (1st rebuild) | `SettingsPage-Cnplp6lc.js`, `index-DLNqg_z1.js`, `index-Diz8jSFK.css`, `index.html` |
| `7ae79c0` / `d5178eb` (current) | `SettingsPage-B3M0PhZL.js`, `index-Diz8jSFK.css`, `index-LFDB-8vZ.js`, `index.html` |

`index.html` changed only because it points at the re-emitted chunk:

```diff
-    <script type="module" crossorigin src="/ui/assets/index-CLCWY7h7.js"></script>
+    <script type="module" crossorigin src="/ui/assets/index-LFDB-8vZ.js"></script>
     <link rel="stylesheet" crossorigin href="/ui/assets/index-Diz8jSFK.css">
```

The `<link>` to the CSS is unchanged — consistent with the CSS blob being byte-identical.

---

## 9. Post-build `git status --porcelain -- pkg/ui/static/`

```
=== post-build git status (must be EMPTY) ===
[end-of-static-status]
```

**EMPTY** — the command produced no output at all. No tracked file under
`pkg/ui/static/` was modified, and no new file appeared there. Re-checked after the second
build: still EMPTY.

---

## 10. `npm run build` — script definition

`pkg/ui/frontend/package.json`:

```json
  "scripts": {
    "dev": "vite --port 5172",
    "build": "vite build",
    "preview": "vite preview",
    "test": "vitest run",
    "test:watch": "vitest"
  },
```

**`npm run build` invokes `vite build` ONLY** — it is **not** `build + tsc`. There is no
type-check step in the build script, so this proof covers the emitted bundle only, not
TypeScript type-checking (type-checking is covered separately by the sibling leg's
`npx vitest run` / `tsc` lane).

### Vite `outDir`

`pkg/ui/frontend/vite.config.ts`:

```
7:  build: {
8:    outDir: '../static',
9:    emptyOutDir: true,
```

Resolved relative to the Vite root (`pkg/ui/frontend`), `../static` → **`pkg/ui/static`**.
This is the directory hashed above, and the build output confirms it.

### Build output tail (verbatim)

```
vite v5.4.21 building for production...
transforming...
Browserslist: browsers data (caniuse-lite) is 9 months old. Please run:
  npx update-browserslist-db@latest
  Why you should do it regularly: https://github.com/browserslist/update-db#readme
✓ 60 modules transformed.
rendering chunks...
computing gzip size...
../static/index.html                         0.55 kB │ gzip:   0.35 kB
../static/assets/index-Diz8jSFK.css         52.83 kB │ gzip:   8.82 kB
../static/assets/SettingsPage-B3M0PhZL.js  205.74 kB │ gzip:  38.04 kB
../static/assets/index-LFDB-8vZ.js         313.51 kB │ gzip: 103.86 kB
✓ built in 4.87s
```

All four emitted paths are `../static/...` — i.e. the build **did** write into
`pkg/ui/static`. Second run: identical output, `✓ built in 4.46s`, **`NPM_EXIT_CODE=0`**
(the first invocation's non-zero shell status came from a `$PIPESTATUS` expansion that the
`/bin/sh` wrapper rejected — "Bad substitution" — not from npm; the second run captured npm's
own exit code cleanly as 0).

### Build-did-write corroboration (mtime after build)

BEFORE (as committed in the worktree):

```
2026-11-17+07:14:36.4844998320 313576 pkg/ui/static/assets/index-LFDB-8vZ.js
2026-11-17+07:14:36.4844998320  52837 pkg/ui/static/assets/index-Diz8jSFK.css
2026-11-17+07:14:36.4854998380 205904 pkg/ui/static/assets/SettingsPage-B3M0PhZL.js
2026-11-17+07:14:36.4854998380    548 pkg/ui/static/index.html
```

AFTER (post-build):

```
2026-11-17+07:42:23.7184823580 205904 pkg/ui/static/assets/SettingsPage-B3M0PhZL.js
2026-11-17+07:42:23.7184823580 313576 pkg/ui/static/assets/index-LFDB-8vZ.js
2026-11-17+07:42:23.7184823580  52837 pkg/ui/static/assets/index-Diz8jSFK.css
2026-11-17+07:42:23.7184823580    548 pkg/ui/static/index.html
```

All four files carry the post-build timestamp, so the zero-diff verdict is **not** a
"build never ran" artifact — the build genuinely rewrote every file, and the rewrite was
**byte-for-byte reproducible**. Identical content hashes plus identical filenames
(content-derived chunk names) confirm the bundler emitted the same chunk graph as the
committed baseline.

---

## 11. Full `git status --porcelain` (end of run)

```
?? .agents/approver/credential-groups-tracking.md
```

One entry only: a **pre-existing untracked** file under `.agents/approver/`
(not present in the gated tip, not created by this leg, and outside `pkg/ui/static/`).
It was already present before this leg started (captured in the pre-state check).
No other path in the worktree is modified, staged, or deleted. No commit, checkout,
stash, reset, clean, push, or ref move was performed by this leg.

---

## 12. Scope & constraint compliance

- No file in the worktree was modified except this artifact (the sanctioned single overwrite).
- No git commit / checkout / stash / reset / clean / push; no ref moves. `HEAD` verified
  unchanged at `d5178ebc58a53a704661e234d449471d2f6ba0be` after the builds.
- The main checkout was not touched.
- No process on port 8088 was signalled.
- No build quick-fixes applied.
- Scratch files confined to `/tmp/rg-gate/` (`static-before.txt`, `static-after.txt`,
  `static-after2.txt`, `static-build.log`, `static-build2.log`).
- `vitest` / `tsc` were NOT run by this leg (sibling leg owns that lane).
- This artifact is **not committed**.

---

## 13. Conclusion

**ZERO-MOD-PROVEN.** The FE build is content-stable at re-gated tip
`d5178ebc58a53a704661e234d449471d2f6ba0be`: rebuilding the SPA reproduces the committed
`pkg/ui/static/` tree byte-for-byte across all four files — **including
`pkg/ui/static/index.html`**, the embed entry point. The committed static bundle remains an
accurate build artifact of the gated source, and the credential-groups FE work introduces
**no** unreviewed change to the shipped/embedded frontend bundle.

Relative to the superseded `ad6996ac` listing, the bundle changed in 3 of 4 files — two
content-hash-renamed JS chunks (`SettingsPage-DdTORRXZ.js` → `SettingsPage-B3M0PhZL.js`,
`index-CLCWY7h7.js` → `index-LFDB-8vZ.js`) plus the `index.html` script reference — all
traceable to the committed on-branch rebuild (`e187e94`, `7ae79c0`). The stylesheet
`index-Diz8jSFK.css` is byte-identical across both gates.
