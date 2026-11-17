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


---

## Appendix (2026-11-17, O-10 — ModelForm credential-group kind gate) — post-change static re-record

**EARLY EXTRA PASS — NOT the release gate.** This appendix is an ADDITIONAL
pre-gate data point; it does NOT replace, weaken or pre-empt the
authoritative phase3 static zero-mod proof, which remains the **TESTER
GATE**'s (`phase2-plan.md` §6.2). The framing and the correction log above
stand unchanged.

The O-10 FE change (the chat model form now offers credential-group options
to CHAT-kind models only) alters the built SPA, so the tracked embed tree
was rebuilt and committed (`14db293 build(credential-groups): rebuild the
embedded SPA for the ModelForm kind gate`), and a FRESH zero-mod cycle was
recorded below.

### Commands (run from the repo root unless noted)

```sh
# BEFORE (committed tree)
find pkg/ui/static -type f | sort | xargs -r sha256sum

# build cycle
cd pkg/ui/frontend && npm run build
cd /home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-cred-groups

# AFTER
find pkg/ui/static -type f | sort | xargs -r sha256sum

# post-build worktree check
git status --porcelain -- pkg/ui/static/
```

### BEFORE (4-line hash listing — committed tree after the O-10 rebuild)

```
ca607d01fcf80a07e8f3bcc86f348817d6022a89ecf17c3c2d8fb14711fb8dde  pkg/ui/static/assets/SettingsPage-DdTORRXZ.js
73eec8c1383440be383c0eba001b97047b1738a32df746d87716293e6e8a5872  pkg/ui/static/assets/index-CLCWY7h7.js
a14e8d1c97d05d693b2d14399b254824b77f1d92af9a7fefdd2f87e2b7f993b2  pkg/ui/static/assets/index-Diz8jSFK.css
44b10be7d2545f01d4e014dfa24f51932d90d1a6c30d56beb919a4a58cd5ab60  pkg/ui/static/index.html
```

### Build output (fresh cycle)

```
✓ 60 modules transformed.
rendering chunks...
computing gzip size...
../static/index.html                         0.55 kB │ gzip:   0.35 kB
../static/assets/index-Diz8jSFK.css         52.83 kB │ gzip:   8.82 kB
../static/assets/SettingsPage-DdTORRXZ.js  205.65 kB │ gzip:  38.03 kB
../static/assets/index-CLCWY7h7.js         313.51 kB │ gzip: 103.86 kB
✓ built in 4.06s
```

### AFTER (4-line hash listing — after the fresh build)

```
ca607d01fcf80a07e8f3bcc86f348817d6022a89ecf17c3c2d8fb14711fb8dde  pkg/ui/static/assets/SettingsPage-DdTORRXZ.js
73eec8c1383440be383c0eba001b97047b1738a32df746d87716293e6e8a5872  pkg/ui/static/assets/index-CLCWY7h7.js
a14e8d1c97d05d693b2d14399b254824b77f1d92af9a7fefdd2f87e2b7f993b2  pkg/ui/static/assets/index-Diz8jSFK.css
44b10be7d2545f01d4e014dfa24f51932d90d1a6c30d56beb919a4a58cd5ab60  pkg/ui/static/index.html
```

### Post-build worktree check (recorded verbatim)

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

### Result

- diff (BEFORE vs AFTER) = empty → byte-identical.
- The tracked embed is exactly **4** files (3 under `pkg/ui/static/assets/`
  + `pkg/ui/static/index.html`) — `find pkg/ui/static -type f | wc -l` = 4.
- The content-hashed asset names changed vs the phase-2 section above
  (`SettingsPage-JML8-Hqt.js` → `SettingsPage-DdTORRXZ.js`,
  `index-Df2Aqyha.js` → `index-CLCWY7h7.js`) because the FE sources
  changed; the CSS asset (`index-Diz8jSFK.css`, sha256 `a14e8d1c…`) and
  the file COUNT (4) are unchanged.
- `git status --porcelain -- pkg/ui/static/` = EMPTY after the build.
- All four tracked files are listed by the sanctioned command form; the
  globstar form is not used anywhere in this document.
