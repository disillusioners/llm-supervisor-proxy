# Credential Groups — FE Static Zero-Modification Proof

**Date:** 2026-11-17
**Worktree (gated artifact):** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-cred-groups`
**Branch:** `feature/credential-groups`
**Gated tip SHA:** `ad6996aceb5e2c5855dfb00252fa6b64db14e952`
**Verdict:** ✅ **ZERO-MOD-PROVEN**

---

## 1. Verdict

The FE production build (`vite build` → `outDir: '../static'`) was executed against the
gated tip. All four files under `pkg/ui/static/` were rewritten by the build and are
**byte-identical** before and after (sha256 match on every file). The post-build
`git status --porcelain -- pkg/ui/static/` is **EMPTY**.

**No file under `pkg/ui/static/` was modified by the build.** No revert was necessary
because no modification occurred.

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

---

## 3. Exact commands executed (from the worktree root)

```bash
cd /home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-cred-groups
mkdir -p /tmp/credgroups-gate
echo "=== FILE COUNT via find (the authoritative form) ==="; find pkg/ui/static -type f | wc -l
echo "=== FILE COUNT via the FORBIDDEN glob (record it as evidence only) ==="; ls -1 pkg/ui/static/**/* 2>/dev/null | wc -l
echo "=== what does npm run build actually invoke? ==="; sed -n '/"scripts"/,/^  }/p' pkg/ui/frontend/package.json
echo "=== vite outDir ==="; grep -n "outDir" pkg/ui/frontend/vite.config.ts
find pkg/ui/static -type f | sort | xargs -r sha256sum | tee /tmp/credgroups-gate/static-before.txt
cd pkg/ui/frontend && npm run build 2>&1 | tee /tmp/credgroups-gate/static-build.log
cd /home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-cred-groups
find pkg/ui/static -type f | sort | xargs -r sha256sum | tee /tmp/credgroups-gate/static-after.txt
echo "=== DIFF before/after ==="; diff /tmp/credgroups-gate/static-before.txt /tmp/credgroups-gate/static-after.txt && echo "BYTE_IDENTICAL" || echo "DRIFT"
echo "=== POST-BUILD git status of the static area (must be EMPTY) ==="; git status --porcelain -- pkg/ui/static/
echo "=== FULL git status ==="; git status --porcelain
```

---

## 4. File-count evidence

### find-count (AUTHORITATIVE) = 4

```
=== FILE COUNT via find (the authoritative form) ===
4
```

### glob-count (FORBIDDEN, evidence only) = 3

```
=== FILE COUNT via the FORBIDDEN glob (record it as evidence only) ===
3
```

### The 4 files (sorted, as hashed)

```
pkg/ui/static/assets/SettingsPage-DdTORRXZ.js
pkg/ui/static/assets/index-CLCWY7h7.js
pkg/ui/static/assets/index-Diz8jSFK.css
pkg/ui/static/index.html
```

**`pkg/ui/static/index.html` IS among the hashed files** — it is line 4 of both the
BEFORE and the AFTER listing, with sha256
`44b10be7d2545f01d4e014dfa24f51932d90d1a6c30d56beb919a4a58cd5ab60`.
The glob form would have returned only the three `assets/` files and missed this one.

---

## 5. BEFORE listing (verbatim, 4 lines)

```
ca607d01fcf80a07e8f3bcc86f348817d6022a89ecf17c3c2d8fb14711fb8dde  pkg/ui/static/assets/SettingsPage-DdTORRXZ.js
73eec8c1383440be383c0eba001b97047b1738a32df746d87716293e6e8a5872  pkg/ui/static/assets/index-CLCWY7h7.js
a14e8d1c97d05d693b2d14399b254824b77f1d92af9a7fefdd2f87e2b7f993b2  pkg/ui/static/assets/index-Diz8jSFK.css
44b10be7d2545f01d4e014dfa24f51932d90d1a6c30d56beb919a4a58cd5ab60  pkg/ui/static/index.html
```

---

## 6. AFTER listing (verbatim, 4 lines)

```
ca607d01fcf80a07e8f3bcc86f348817d6022a89ecf17c3c2d8fb14711fb8dde  pkg/ui/static/assets/SettingsPage-DdTORRXZ.js
73eec8c1383440be383c0eba001b97047b1738a32df746d87716293e6e8a5872  pkg/ui/static/assets/index-CLCWY7h7.js
a14e8d1c97d05d693b2d14399b254824b77f1d92af9a7fefdd2f87e2b7f993b2  pkg/ui/static/assets/index-Diz8jSFK.css
44b10be7d2545f01d4e014dfa24f51932d90d1a6c30d56beb919a4a58cd5ab60  pkg/ui/static/index.html
```

---

## 7. Diff verdict

```
=== DIFF before/after ===
BYTE_IDENTICAL
```

**BYTE_IDENTICAL** — zero differing lines across all 4 hashed files.

---

## 8. Post-build `git status --porcelain -- pkg/ui/static/`

```
=== POST-BUILD git status of the static area (must be EMPTY) ===
[end-of-static-status]
```

**EMPTY** — the command produced no output at all. No tracked file under
`pkg/ui/static/` was modified, and no new file appeared there.

---

## 9. `npm run build` — script definition

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
8:    outDir: '../static',
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
../static/assets/SettingsPage-DdTORRXZ.js  205.65 kB │ gzip:  38.03 kB
../static/assets/index-CLCWY7h7.js         313.51 kB │ gzip: 103.86 kB
✓ built in 4.75s
```

All four emitted paths are `../static/...` — i.e. the build **did** write into
`pkg/ui/static`.

### Build-did-write corroboration (mtime after build)

```
2026-11-17+05:14:39.3485473020  205816 bytes  pkg/ui/static/assets/SettingsPage-DdTORRXZ.js
2026-11-17+05:14:39.3485473020  313576 bytes  pkg/ui/static/assets/index-CLCWY7h7.js
2026-11-17+05:14:39.3485473020   52837 bytes  pkg/ui/static/assets/index-Diz8jSFK.css
2026-11-17+05:14:39.3495473130     548 bytes  pkg/ui/static/index.html
```

All four files carry the build timestamp, so the zero-diff verdict is **not** a
"build never ran" artifact — the build genuinely rewrote every file, and the rewrite
was **byte-for-byte reproducible**. Identical content hashes plus identical filenames
(hash-based filenames such as `index-CLCWY7h7.js` are content-derived) confirm the
bundler emitted the same chunk graph as the committed baseline.

---

## 10. Full `git status --porcelain` (end of run)

```
?? .agents/approver/credential-groups-tracking.md
```

One entry only: a **pre-existing untracked** file under `.agents/approver/`
(not present in the gated tip, not created by this leg, and outside `pkg/ui/static/`).
No other path in the worktree is modified, staged, or deleted. No commit, checkout,
stash, reset, clean, push, or ref move was performed by this leg.

---

## 11. Scope & constraint compliance

- No file in the worktree was modified except this artifact.
- No git commit / checkout / stash / reset / clean / push; no ref moves.
- The main checkout was not touched.
- No process on port 8088 was signalled.
- No build quick-fixes applied.
- Scratch files confined to `/tmp/credgroups-gate/` (`static-before.txt`,
  `static-after.txt`, `static-build.log`).
- `vitest` / `tsc` were NOT run by this leg (sibling leg owns that lane).
- This artifact is **not committed**.

---

## 12. Conclusion

**ZERO-MOD-PROVEN.** The FE build is content-stable at gated tip
`ad6996aceb5e2c5855dfb00252fa6b64db14e952`: rebuilding the SPA reproduces the committed
`pkg/ui/static/` tree byte-for-byte across all four files — **including
`pkg/ui/static/index.html`**, the embed entry point. The committed static bundle
therefore remains an accurate build artifact of the gated source, and the credential-groups
FE work introduces **no** unreviewed change to the shipped/embedded frontend bundle.
