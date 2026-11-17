# Test Report: credential-groups FE phase2 static zero-mod proof

## Summary

Sanctioned command form used (run from repo root, globstar NOT used):
`find pkg/ui/static -type f | sort | xargs -r sha256sum`

Build performed twice with no source changes between runs; the before/after
4-line hash listings are byte-identical (deterministic build). All four
tracked files are present (3 under `pkg/ui/static/assets/` + `index.html`).

## Commands

```sh
# BEFORE
find pkg/ui/static -type f | sort | xargs -r sha256sum
# build
cd pkg/ui/frontend && npm run build
# AFTER
cd /home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-cred-groups && find pkg/ui/static -type f | sort | xargs -r sha256sum
```

## Before (4-line hash listing)

```
0cd784999899b140802ec00e0e2f33ad7b9cb7bd77b8edc08bf5ec3ab23e082e  pkg/ui/static/assets/SettingsPage-JML8-Hqt.js
0edfdd84bd067eb50be8035b19ed84c42ec367638b2df71c6feb747bdc34f0d3  pkg/ui/static/assets/index-Df2Aqyha.js
a14e8d1c97d05d693b2d14399b254824b77f1d92af9a7fefdd2f87e2b7f993b2  pkg/ui/static/assets/index-Diz8jSFK.css
5a770076f789352a5de3bb2e2cfd2fd5d1164a0a2115212256c13f582cb941f7  pkg/ui/static/index.html
```

## Build output

```
../static/index.html                         0.55 kB │ gzip:   0.35 kB
../static/assets/index-Diz8jSFK.css         52.83 kB │ gzip:   8.82 kB
../static/assets/SettingsPage-JML8-Hqt.js  205.58 kB │ gzip:  38.01 kB
../static/assets/index-Df2Aqyha.js         313.51 kB │ gzip: 103.86 kB
✓ built in 5.06s
```

## After (4-line hash listing)

```
0cd784999899b140802ec00e0e2f33ad7b9cb7bd77b8edc08bf5ec3ab23e082e  pkg/ui/static/assets/SettingsPage-JML8-Hqt.js
0edfdd84bd067eb50be8035b19ed84c42ec367638b2df71c6feb747bdc34f0d3  pkg/ui/static/assets/index-Df2Aqyha.js
a14e8d1c97d05d693b2d14399b254824b77f1d92af9a7fefdd2f87e2b7f993b2  pkg/ui/static/assets/index-Diz8jSFK.css
5a770076f789352a5de3bb2e2cfd2fd5d1164a0a2115212256c13f582cb941f7  pkg/ui/static/index.html
```

## Result

diff (before vs after) = empty → byte-identical across all 4 files.
