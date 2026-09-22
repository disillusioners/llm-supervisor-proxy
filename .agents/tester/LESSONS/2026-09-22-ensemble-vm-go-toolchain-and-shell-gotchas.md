# Ensemble-VM Go toolchain + shell gotchas (llm-supervisor-proxy testing)

Date: 2026-09-22 · Context: FE API payload RAM incident verification on ensemble-vm (Linux, user nea)

## 1. GOTOOLCHAIN=go1.26.0 is mandatory for every go command
- System go is **1.22.2**; go.mod requires **go 1.26**.
- `GOTOOLCHAIN=auto` FAILS on this VM: `go: download go1.26 … toolchain not available` (no network/toolchain download), even though go1.26.0 objects exist in the module cache.
- Fix: prefix every go command with `GOTOOLCHAIN=go1.26.0` (env-only — never edit GOENV or repo files).
- Suspected side-effect worth upstream eyes: pkg/mcp `TestValidateUpstreamURL` SSRF octal/decimal/hex subtests fail **deterministically** under this toolchain on Linux (ValidateUpstreamURL returns nil for `http://0177.0.0.01/`, `http://2130706433/`, `http://0x7f000001/`), byte-identical on base b832ea3f → likely stdlib parse-behavior delta, NOT a regression. Documented in QUARANTINE.md.

## 2. Worker shells may be dash — PIPESTATUS is bash-only
- `cmd 2>&1 | tee log; echo ${PIPESTATUS[0]}` → "Bad substitution" under /bin/sh (dash), and `tee` otherwise masks go's exit code.
- Fix: wrap in `bash -c '…'` or capture exit code without tee.

## 3. PORT env is gated behind APPLY_ENV_OVERRIDES
- `pkg/config` `applyEnvOverrides` only applies env overrides when `APPLY_ENV_OVERRIDES` is set; a bare `PORT=17891 ./binary` silently binds the default 4321.
- Fix for isolated test runs: `env APPLY_ENV_OVERRIDES=1 PORT=… …`.

## 4. vite build writes INTO the repo by default
- `pkg/ui/frontend/vite.config.ts` sets `build.outDir: '../static'`. For read-only verification, copy the frontend tree to /tmp and run `npx vite build --outDir /tmp/…/dist --emptyOutDir`. Symlink node_modules from the repo (read-only reuse) to skip npm ci.

## 5. gzip smoke-testing: MinResponseSize=1024
- `pkg/middleware/gzipmw/response.go` compresses only bodies ≥ 1 KiB (`Vary: Accept-Encoding` is still emitted below the threshold). A gzip smoke check against an EMPTY store (`[]` = 3 bytes) will show NO Content-Encoding **by design** — populate the store first (e.g., 5 failing proxy POSTs against a dead upstream, ~0.1s each, no external calls) to cross the threshold.

## 6. Reproducible FE builds are byte-stable on this VM
- vite build from the post-fix source reproduced the committed hashed bundles **byte-identically (sha256)** — safe to use "fresh build == committed static" as a verification gate.
