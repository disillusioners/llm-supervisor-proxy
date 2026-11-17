# Test Report: credential-groups FE phase2 — EARLY EXTRA PASS (static zero-mod observation)

## Status of this document

**EARLY EXTRA PASS — NOT the release gate.**

The AUTHORITATIVE phase3 static zero-mod proof is produced by the
**TESTER GATE**, assigned there by `phase2-plan.md` §6.2 ("This belongs to
phase3's gate; phase2 only requires that the FE changes are real (so the
static tree *will* change)"). This document is a **pre-gate extra data
point** collected during the phase2 FE lane — it does NOT replace, weaken
or pre-empt the tester-gate proof.

What this document DOES establish: the tracked embed tree
(`pkg/ui/static/`, written in place by `vite build`) is byte-reproducible
from the committed FE sources, and a build does not dirty the tracked
embed — i.e. the mechanism the phase3 gate relies on is sound.

## Summary

Sanctioned command form used (run from the repo root, the globstar form
was NOT used):

```
find pkg/ui/static -type f | sort | xargs -r sha256sum
```

Two genuine build cycles were run with **no FE source change between
them**. All three hash listings (BEFORE = committed tree, AFTER-1 =
after build #1, AFTER-2 = after build #2) are byte-identical: the same
4 files, the same sha256 digests. The post-build
`git status --porcelain -- pkg/ui/static/` line is **EMPTY** — the build
reproduces the tracked embed exactly and dirties nothing.

All four tracked files are present (3 under `pkg/ui/static/assets/` +
`pkg/ui/static/index.html`, the embed entry point).

## Commands (run from the repo root unless noted)

```sh
# BEFORE (committed tree)
find pkg/ui/static -type f | sort | xargs -r sha256sum

# build cycle #1
cd pkg/ui/frontend && npm run build
cd /home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-cred-groups
find pkg/ui/static -type f | sort | xargs -r sha256sum

# build cycle #2 (no source change in between)
cd pkg/ui/frontend && npm run build
cd /home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-cred-groups
find pkg/ui/static -type f | sort | xargs -r sha256sum

# post-build worktree check
git status --porcelain -- pkg/ui/static/
```

## BEFORE (4-line hash listing — committed tree)

```
0cd784999899b140802ec00e0e2f33ad7b9cb7bd77b8edc08bf5ec3ab23e082e  pkg/ui/static/assets/SettingsPage-JML8-Hqt.js
0edfdd84bd067eb50be8035b19ed84c42ec367638b2df71c6feb747bdc34f0d3  pkg/ui/static/assets/index-Df2Aqyha.js
a14e8d1c97d05d693b2d14399b254824b77f1d92af9a7fefdd2f87e2b7f993b2  pkg/ui/static/assets/index-Diz8jSFK.css
5a770076f789352a5de3bb2e2cfd2fd5d1164a0a2115212256c13f582cb941f7  pkg/ui/static/index.html
```

## Build output #1

```
✓ 60 modules transformed.
rendering chunks...
computing gzip size...
../static/index.html                         0.55 kB │ gzip:   0.35 kB
../static/assets/index-Diz8jSFK.css         52.83 kB │ gzip:   8.82 kB
../static/assets/SettingsPage-JML8-Hqt.js  205.58 kB │ gzip:  38.01 kB
../static/assets/index-Df2Aqyha.js         313.51 kB │ gzip: 103.86 kB
✓ built in 4.77s
```

## AFTER-1 (4-line hash listing — after build #1)

```
0cd784999899b140802ec00e0e2f33ad7b9cb7bd77b8edc08bf5ec3ab23e082e  pkg/ui/static/assets/SettingsPage-JML8-Hqt.js
0edfdd84bd067eb50be8035b19ed84c42ec367638b2df71c6feb747bdc34f0d3  pkg/ui/static/assets/index-Df2Aqyha.js
a14e8d1c97d05d693b2d14399b254824b77f1d92af9a7fefdd2f87e2b7f993b2  pkg/ui/static/assets/index-Diz8jSFK.css
5a770076f789352a5de3bb2e2cfd2fd5d1164a0a2115212256c13f582cb941f7  pkg/ui/static/index.html
```

## Build output #2

```
✓ 60 modules transformed.
rendering chunks...
computing gzip size...
../static/index.html                         0.55 kB │ gzip:   0.35 kB
../static/assets/index-Diz8jSFK.css         52.83 kB │ gzip:   8.82 kB
../static/assets/SettingsPage-JML8-Hqt.js  205.58 kB │ gzip:  38.01 kB
../static/assets/index-Df2Aqyha.js         313.51 kB │ gzip: 103.86 kB
✓ built in 4.69s
```

## AFTER-2 (4-line hash listing — after build #2)

```
0cd784999899b140802ec00e0e2f33ad7b9cb7bd77b8edc08bf5ec3ab23e082e  pkg/ui/static/assets/SettingsPage-JML8-Hqt.js
0edfdd84bd067eb50be8035b19ed84c42ec367638b2df71c6feb747bdc34f0d3  pkg/ui/static/assets/index-Df2Aqyha.js
a14e8d1c97d05d693b2d14399b254824b77f1d92af9a7fefdd2f87e2b7f993b2  pkg/ui/static/assets/index-Diz8jSFK.css
5a770076f789352a5de3bb2e2cfd2fd5d1164a0a2115212256c13f582cb941f7  pkg/ui/static/index.html
```

## Post-build worktree check (recorded verbatim)

Command (repo root):

```
git status --porcelain -- pkg/ui/static/
```

Output:

```
```

(EMPTY — the command printed nothing. The build rewrote the tracked embed
in place and left the tree byte-identical to the committed state; no
modified, no added and no untracked file under `pkg/ui/static/`.)

Cross-check: `diff -r` of the post-build tree against a pre-build copy of
the same directory reported no difference, and the file count is 4.

## Result

- diff (BEFORE vs AFTER-1) = empty → byte-identical.
- diff (AFTER-1 vs AFTER-2) = empty → byte-identical across two genuine
  build cycles with no source change in between.
- The tracked embed is reproducible from the committed FE sources, and a
  build does not modify the worktree (`git status --porcelain -- pkg/ui/static/`
  = EMPTY).
- All four tracked files are listed (3 under `pkg/ui/static/assets/` +
  `pkg/ui/static/index.html`).

## Correction log (2026-11-17, FE hardening session)

This document was corrected in the FE test-hardening session:

1. The earlier version claimed "Build performed twice with no source
   changes between runs" while recording only ONE build cycle. The claim
   is now EVIDENCED: two genuine build cycles (before → build → hash →
   build → hash) are recorded above with both build outputs and all
   three hash listings.
2. The post-build `git status --porcelain -- pkg/ui/static/` line was
   missing; it is now recorded verbatim above (EMPTY).
3. The document was RELABELLED as an **EARLY EXTRA PASS**: the
   authoritative phase3 zero-mod proof belongs to the TESTER GATE
   (`phase2-plan.md` §6.2). This file is a pre-gate data point only.
