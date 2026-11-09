# LESSONS — 9router image-gen verification gate (2026-11-09)

## 1. Operator trap: imggen `base_url` must end in `/v1`
The 9router adapter (`pkg/imggen/ninerouter.go`) appends **only** `/images/generations` to the credential `base_url`. A credential with `base_url=http://127.0.0.1:20128` therefore hits the upstream's dashboard catch-all and returns an **HTML 404 in ~79 ms** — not a JSON error, not a 502 — so it looks like "provider broken" rather than "wrong URL". Correct value: `http://<host>:20128/v1`.
- Symptom signature: fast 404 (<100 ms), `content-type: text/html`, log line `[imggen] <model>: upstream HTTP 404`.
- The FE already ships the right default (`http://ninerouter.9router.svc.cluster.local:20128/v1`), so the trap only bites hand-written config / commissioning scripts.
- Generalization: every OpenAI-shaped provider in this repo expects `base_url` to be the API root (including `/v1`); the adapter appends only the resource path.

## 2. Mutation controls must run in a `git archive` scratch copy
`git -C <worktree> archive <sha> | tar -x -C $SCRATCH` — NEVER `cp -a` a linked worktree (it shares `.git` and mutating it corrupts the real repo state). Keep a second `tar`-copied pristine tree and restore with `cp`; verify restore with `diff -r`.
Validation criterion: the mutated build must still **compile**, and the expected tests must fail on an **assertion**, not a compile error. Record the exact edited line (`file:line`) and the failing assertion text — that is what proves the control is real rather than vacuous.

## 3. `--- PASS` matching must be exact-boundary
`grep -F -- "--- PASS: TestX"` matches `TestX` but also prefixes nothing — however the reverse hazard is real: `--- PASS: TestNineRouter_NormalizeErrorBody (` and `--- PASS: TestNineRouter_NormalizeErrorBody_VerbatimOnNoMessage (` are distinct lines and a naive `grep 'TestNineRouter_NormalizeErrorBody'` counts 2 for 1 test. Anchor on `--- PASS: <Name> (` and `=== RUN   <Name>$`. This gate's 37 added tests all passed exact-boundary checks.

## 4. Verbose `go test` stdout can be truncated by the tool view
A 335 KB log hit the 150 K display cap; the trailing `EXIT=` echo was lost. Write `-v` output to a file (`| tee /tmp/x.log`), then grep the file — and put exit-code markers on their own early line (or reconstruct from `ok`/`FAIL` counts). Never rely on the tail of a huge stdout.

## 5. Toolchain / kubectl are not on PATH in this environment
- system `go` is 1.22.2 and the 1.26 toolchain cannot be downloaded → prepend `/home/nea/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.linux-amd64/bin` to PATH in **every** shell invocation (each bash call is a fresh shell).
- `kubectl` lives at `/home/nea/bin/kubectl` (context `default`).
- Fence for a throwaway local boot: `HOME=$(mktemp -d) XDG_CONFIG_HOME=$HOME/.config APPLY_ENV_OVERRIDES=1 PORT=4321 DATABASE_URL= ENSEMBLE_SELF_ENV=dev` → SQLite inside the temp home; real `~/.config` untouched.

## 6. `pkill -f 'port-forward.*ninerouter'` self-matches the invoking shell
Use a bracket pattern (`pkill -f 'port-forward svc/nineroute[r]'`) or kill the recorded PID. The self-match killed the wrapper mid-teardown; recoverable, but avoidable.

## 7. Latency baseline drift
Real 9router image generation: 30.2 s this run (local instance, `n=1`) vs the 5-hour-old in-cluster p50 17.7 s / max 21.4 s. `ImageGenTimeout` default 120 s leaves 4× headroom, but the tail is moving — keep it in the watch list.
