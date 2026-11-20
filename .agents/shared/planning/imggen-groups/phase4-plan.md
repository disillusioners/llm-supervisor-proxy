# Phase 4: Verification Gate + Ship/Deploy Runbook + Rollback — imggen-groups

**Lane:** `feature/imggen-groups-text-subtabs` · **Base:** `origin/latest` @ `5cf1ecdc` (giter-fenced worktree `../llm-supervisor-proxy-wt-imggen-groups`; re-anchor at kickoff per P0.1) · **Date:** 2026-11-20 · **Status:** REVISED 2026-11-20 — reviewer **APPROVED-CONDITIONAL** folded (doc-only): G8 pre-declares the walk-budget knob + the decision-lane zero-edit rule (A-5); G9 authority phrasing (W-6); G10(a)/§6.6 the two EMPTY id-set diff gates disambiguated (S-1); §6.6/§6.9 the dual misroute strings (D-15); §8.2 all-paths rollback wording (A-2). Rulings D-14..D-18 + adopted warnings: `decisions.md`.
**Read first:** `decisions.md` (this directory — rulings D-1..D-13, drift + not-verified ledgers), then `phase1-plan.md` / `phase2-plan.md` (backend) and `phase3-plan.md` (FE) when they land. House precedent: `.agents/shared/planning/decision-groups/phase3-plan.md` (gate + runbook shape), `.agents/shared/planning/token-authz-allowed-groups/phase3-plan.md` (register + deploy-table shape).
**Phase verdict:** TIGHTLY COUPLED and strictly LAST — this phase consumes phases 1-3; it owns no source file, only gate/ship/rollback artifacts and this plan.
**Scope note:** this plan IS the execution checklist for the ship operator. Nothing in it executes at planning time. llmproxy is the CRITICAL substrate (the whole ensemble's LLM traffic, including the dispatcher's own turns, flows through it) — quality beats speed on every step here, however small the change looks.

---

## 0. Preconditions (clear ALL before the gate starts)

| # | Precondition | How to check | Blocking |
|---|--------------|--------------|----------|
| P0.1 | **Base re-anchor + pin re-verification.** `origin/latest` moves daily; the branch was cut at `5cf1ecdc`. At kickoff, giter re-anchors to the fresh remote tip; every base-relative pin (test ledgers, byte-identity corpora) is re-checked against the NEW merge-base and drift is adjudicated BEFORE gate work starts. The anchor-of-record SHA goes into the KV row. The ship-time full-gate re-run is UNCONDITIONAL regardless of merge conflicts. | `git fetch origin && git log --oneline origin/latest -1`; re-run the ledger set-arithmetic vs the new base | YES |
| P0.2 | Phases 1-3 implemented, reviewed, committed on the branch; working tree clean | `git status --porcelain` empty; `git log --oneline $(git merge-base HEAD origin/latest)..HEAD` non-empty | YES |
| P0.3 | **Added-test ledger declared** by phases 1-3 (exact top-level `func Test*` names + FE case tokens, per owning phase) | handoff docs → §3 G7 set arithmetic | YES |
| P0.4 | **Migration boundary PRE-FILED** (both branches of D-1): expected = NO migration (`git diff --stat $BASE..HEAD -- pkg/store/database/migrations/ pkg/store/database/migrate.go` → 0 bytes). IF phase 1 lands a knob/persisted column, the pre-file names the exact `035_*` files (sqlite+postgres, up+down) + the single registry append after `migrate.go:95`, and §3 G9 activates. | boundary declaration in the gate artifact, BEFORE the gate | YES |
| P0.5 | **Boundary fence:** no `k8s/`, no `.github/`, no `pkg/config/` interface changes beyond the pre-declared shape | `git diff --stat $BASE..HEAD -- k8s/ .github/ pkg/config/` → 0 bytes unless pre-declared with bounded shape | YES |
| P0.6 | Toolchain pinned: go1.26 cached toolchain; offline pins | `PATH=/home/nea/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.linux-amd64/bin:$PATH GOTOOLCHAIN=local GOPATH=/home/nea/go GOMODCACHE=/home/nea/go/pkg/mod GOCACHE=/home/nea/.cache/go-build go version` → `go1.26.0` (system go 1.22.2 fails `go.mod requires go >= 1.26` offline) | YES |
| P0.7 | **HOME quarantine prepared** — the config.json rewrite trap: `pkg/proxy` tests set `APPLY_ENV_OVERRIDES=1` ⇒ `config.Load()` create-if-absent + env overrides rewrite `$HOME/.config/llm-supervisor-proxy/config.json`; `PORT=9797` leaks from the daemon env. Resolve the ABSOLUTE real-home config path BEFORE `export HOME=…` (a `~` form resolves to the quarantine after the export). | `REAL_HOME_CONFIG="$(getent passwd "$(id -u)" | cut -d: -f6)/.config/llm-supervisor-proxy"`; hash `config.db` + `buffers/` pre-gate | YES |
| P0.8 | FE toolchain: `pkg/ui/frontend/node_modules` is absent in a fresh worktree → `npm ci` (needs network); without it the FE gates are skipped AND THE GATE IS INCOMPLETE | `ls pkg/ui/frontend/node_modules` | YES |
| P0.9 | **Import-guard awareness:** the `pkg/imggen` / `pkg/decision` / `pkg/oauth2` / `pkg/identity` grep gates + the census anti-drift gate live in `make test` (`Makefile:51-113`), NOT in plain `go test` — the gate MUST run `make test` (or the greps explicitly) in addition to the pack slices | run `make test` once; guards print `>> import guard: …` lines | YES |
| P0.10 | ADR O1 supersession note exists (decisions.md D-11) | grep the imggen-groups corpus for the dated supersession | YES (docs) |

---

## 1. Objective

Prove the commission green on every code/test/FE/boundary gate, land it through the house branch flow, deploy it backup-first to both hosts under the standing auto-deploy grant, verify the image registry-side, pin dcs, and leave a measured rollback anchor + a data-compat verdict for the imggen-group rows — with the live-store configuration step (creating the first real imggen group rows) explicitly held for caller confirmation.

## 2. Files owned / boundaries

**This phase owns (writes):** the gate artifact under `.agents/tester/RESULTS/`, the `.agents/tester/PACKS.md` append-only lane note, the ship record, and (rider R2 only) a one-line digest-gate edit on branch `docs/phase4-ensem-execution-plan`. **No source file.** If the gate finds a defect it routes BACK to the owning phase — this phase never patches Go/FE behaviour implicitly.
**Must remain untouched:** `k8s/` (this commission must not touch it — `git diff <old> <new> -- k8s/` EMPTY is itself a gate), `.github/`, `pkg/config/` beyond the pre-declared knob shape, `migrations/` + `migrate.go` unless P0.4 pre-filed the `035` set.

---

## 3. Verification gates (pre-merge; local `make test` is the REAL gate)

> **CI gates NEITHER Go tests nor FE.** `docker-ci.yml:104` runs `go test ./... -v 2>&1 || echo "No tests found or tests completed"` — the `|| echo` swallows every Go-test failure. The FE is never type-checked in CI. The real gate is this section, run locally.

### 3.0 Hermetic environment (copy-pasteable, run FIRST)

```bash
export PATH=/home/nea/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.linux-amd64/bin:$PATH
export GOTOOLCHAIN=local GOPATH=/home/nea/go GOMODCACHE=/home/nea/go/pkg/mod GOCACHE=/home/nea/.cache/go-build
BASE=$(git merge-base HEAD origin/latest)   # P0.1 anchor-of-record, NOT a hard-coded SHA
REAL_HOME_CONFIG="$(getent passwd "$(id -u)" | cut -d: -f6)/.config/llm-supervisor-proxy"
# hash REAL_HOME_CONFIG/config.db + buffers/ BEFORE quarantine; re-verify AFTER the gate
```

### 3.1 Gate matrix

| # | Gate | Command / scope | Pass condition |
|---|------|-----------------|----------------|
| G1 | build + vet + gofmt | `go build ./...` + `go vet ./...` + `gofmt -l` on the changed set | exit 0 both; **ZERO NEW gofmt drift**: NEW drift = HEAD flagged-LINE-SET minus base flagged set, computed per-file via `git show $BASE:<file> \| gofmt -d` vs `gofmt -d <file>` at HEAD (diff-hunk overlap is UNRELIABLE — ±3 context lines distort). **After any key-removal sweep: `gofmt -l` on ALL touched `_test.go`** — sweeps silently add whole-file drift (20-file precedent, wi-7769a812). |
| G2 | **Pack A** (9 pkgs @ base — re-measure) | `timeout 300 env HOME=$(mktemp -d) -u DATABASE_URL go test ./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/... -count=1 -timeout=240s` (slice budget: `-timeout=240s` inside `timeout 300`; pkg/proxy alone has measured 109-145s — a 110s cap fails spuriously) | exit 0; only §4-registered flakes adjudicated. Anti-vacuous input: phase ledgers. |
| G3 | **Pack B** (module-anchored complement; **37 pkgs @ base — re-measure, never copy a count**) | `go list ./... \| grep -vE 'github\.com/disillusioners/llm-supervisor-proxy/(cmd\|pkg/(proxy\|store\|decision\|ui))(/\|$)'` → `go test` that set (same HOME quarantine) | sole expected failure = the quarantined `pkg/mcp` httpbin redirect pair (`handlers_api_test.go:1864/:1904`, deterministic third-party 403 — adjudicate, never chase). **THE `(/\|$)` ANCHOR IS LOAD-BEARING** — the module prefix alone is fine, but an end-anchor-only variant leaks Pack A back in (38-pkgs precedent). |
| G4 | `make test` (import guards + census) | `make test` in the worktree | the `>> import guard:` greps EMPTY (incl. **pkg/imggen must not import pkg/proxy** — D-2), census gate OK, `go test ./...` matches G2/G3 adjudications. Plain `go test` does NOT run these (P0.9). |
| G5 | **FE vitest** | `cd pkg/ui/frontend && timeout 300 npx vitest run` | all green, `×`=0. The tsc-baseline comparator auto-runs inside the suite. FE case tokens from the phase-3 ledger each appear EXACTLY once (file-scoped census if any label collides with upstream). |
| G6 | **tsc freeze** | `npx tsc --noEmit` (exit 2 is EXPECTED, not 1) | verdict = **per-file COUNT + exact file-SET** vs the committed baseline — NEVER line numbers. Baseline at base: `TOTAL_ERRORS=18 / TOTAL_FILES=9`, `SettingsPage.tsx` frozen at **5** (`pkg/ui/frontend/tsc-baseline.txt`). The FE restructure (D-12) touching SettingsPage.tsx makes this gate LIVE: zero new error locations; a DECREASE requires a same-commit baseline update with its own justification. |
| G7 | **Anti-vacuous ledger** | set arithmetic vs `$BASE`: added top-level `func Test*` names + FE tokens | every ADDED Go name shows a literal end-anchored `--- PASS` in the gate's OWN logs, matched EXACTLY ONCE, 0 MISS / 0 SKIP; `added == expected`, `unlisted == 0`, `absent == 0`; a MISS on any token FAILS the gate, no exceptions. The D-3 flipped pin (`group_rules_test.go:555-556` region) is listed as MODIFIED with its justification, not silently absorbed. |
| G8 | **Boundary check (pre-declared shapes, revised 2026-11-20)** | `git diff --stat $BASE..HEAD -- k8s/ .github/ pkg/config/ pkg/decision/ pkg/store/database/migrations/ pkg/store/database/migrate.go` | **Two PRE-DECLARED bounded shapes + silence elsewhere.** (1) **The walk-budget knob** (D-17): `image_gen_group_walk_timeout` with the FIVE implementer touchpoints of P1 §4.7 (`pkg/config/config.go` knob block `:159-167`, `ManagerInterface :207`, `Defaults :308`, clamp block `:336-346`, + the handler's per-request getter read) — anything larger under `pkg/config/` is a finding, not noise. (2) **Decision-lane zero-edit (A-5):** `pkg/decision/` EMPTY **(zero exceptions)**; the one declared exception is `pkg/proxy/authz_guard_test.go`, OUTSIDE that path — any decision-side diff is a RED gate (no phase plan declares a `pkg/decision` edit; all references are citations/mirrors). `k8s/`, `.github/`, migrations + `migrate.go` stay EMPTY per P0.4/P0.5. |
| G9 | **Migration reconciliation — ONLY IF a migration lands** (P0.4 branch 2) | count `.sql` files (88 at base — verify the delta is exactly the 035 set: 2 files × 2 dialects) AND derive the registry↔disk pairing **FRESH at gate time** — the authority phrasing is **"88 .sql verified + FRESH pairing at gate time"** (W-6): the historical 91-grep figure is dead (decisions.md §Drift #2), the pairing is re-derived and recorded in the artifact, never copied | files = registry + 88; every new file registered; sqlite+postgres twins present; the FRESH pairing recorded |
| G10 | **Commission pins** (the discriminating assertions) | targeted `-run` runs | (a) **the catalog-exclusion regression gate** — the `/v1/models` EMPTY id-set diff on a no-imggen-group store (D-9), asserted as GO TEST PINS on the SAME store comparing base-vs-HEAD binary behaviour, + the exclusion pin with one imggen group. **This is NOT the §6.6 live-dormancy proof** — the two EMPTY id-set diff gates are DISTINCT (S-1): this one diffs old-vs-new BEHAVIOUR on one store, pre-merge; §6.6 diffs the LIVE catalog pre-vs-post DEPLOY on one host, ship-tail; neither can silently stand in for the other; (b) the misroute battery: `image_misroute` 400 lane-gate pins (D-7.4) **+ the chat-family `image_gen_misroute` arms in BOTH chat routes (D-15)** — string-pinned per W-1 (assert the code string in body+event; any numeric arm-floor states the FINAL arm count); (c) 403-vs-502 discriminator pins (scope-empty vs all-disabled) **+ the A-6 zero-engine-state assertion after the 403 arm**; (d) decorator-through test IF D-13 landed a forward (0-1 expected; the 7-step surface). Note `-run` splits on `/`: bare subtest names select ZERO tests ("no tests to run", exit 0 — the vacuity trap). |
| G11 | `-race` spot | `go test -race ./pkg/store/... ./pkg/proxy` (+ light: `./pkg/authz ./pkg/auth ./pkg/modelscache ./pkg/decision ./pkg/imggen`) | zero data races. Budget honestly: the heavy slice measured 317s at the token-authz gate (the 1200-1500s figure was a ≈4× sum-projection); give it ≥600s and do not read a short timeout as a failure. GOPATH/GOMODCACHE pins are REQUIRED for the pkg/authz guard to run, not skip. |
| G12 | **static/ rebuild — THIS COMMISSION CHANGES FE** | `cd pkg/ui/frontend && npm run build` (vite `outDir ../static` — a TRACKED embedded area) | The FE-untouched **sha256 zero-mod proof does NOT apply** to this commission's FE work — phases 1-2 (backend-only) may claim it for THEIR runs; phase 3 (FE) and the merged tree may NOT. Instead assert: (a) the **pairing rule** — the commit that changes `pkg/ui/frontend/src` also lists the `pkg/ui/static` change (split across two commits = FAILURE); (b) rebuild determinism — build 2× , `pkg/ui/static` sha256-identical both times; (c) the bundle carries the new imggen-group strings and the frozen chat-lane copy verbatim. |
| G13 | Boot smoke in isolation | `env -u DATABASE_URL APPLY_ENV_OVERRIDES=1 PORT=<p> HOME=$(mktemp -d) go build -o /tmp/lsptest ./cmd && /tmp/lsptest` (bare PORT is INERT without `APPLY_ENV_OVERRIDES=1`; default bind 0.0.0.0:4321) | `/healthz` 200, `/fe/api/version` = dev-build, `/fe/api/models` 200, `/v1/models` 200; zero panics; clean SIGTERM; real-home config hash unchanged. |
| G14 | FE freeze spot-checks | grep the built bundle + sources | chat-lane copy BYTE-FROZEN (D-12d): the chat ModelsTab/GroupModelsTab user-facing strings byte-identical to base (only mount point moves); `data-tab-id="group_models"` retired exactly once; new container test-ids present. |
| G15 | FE↔BE contract sweep | compare the FE lane fold vs the widened Go fold | `groupLane.ts` mirrors the 3-lane vocabulary (ADR-19, same commit as D-3 — verify the Go change and the FE twin are in ONE commit, not adjacent ones). |

---

## 4. Pre-registered flakes (adjudicate, never chase)

| Test | Signature | Handling |
|---|---|---|
| `TestEngine_GetOrSelect_TOCTOU_CoolingReverify` | `pkg/credentiallb` `cooldown_test.go:481`/`:517`, ~1/10 plain (deterministic under `-race`); full-package load widens the window | re-run isolated; isolated PASS ⇒ adjudicated flake |
| `TestStoreEngine_PeakHourParity` | fires ONLY 23:59:00–23:59:59.999 UTC (half-open `[start,end)` minute seeding drops minute 1439) | check wall clock; isolated PASS ⇒ adjudicated |
| `TestStoreEngine_CloseLifecycle` | `t.TempDir()` RemoveAll "directory not empty" cleanup-hook race; assertions never fail | re-run; adjudicated |
| usage stress pair | SQLITE_BUSY under `pkg/usage` concurrent stress | re-run; adjudicated |
| `pkg/mcp` httpbin redirect pair | `handlers_api_test.go:1864/:1904` — DETERMINISTIC third-party 403 (httpbin policy change); `pkg/mcp` untouched by this commission ⇒ branch-orthogonal | do NOT re-run to "confirm" — a retry cannot change a third-party response; record the signature |

---

## 5. Landing recipe (house flow, verified — the main checkout HOLDS `master`; never merge there)

1. **Publish `latest`:** in the sibling worktree (off REMOTE tips): `git merge --no-ff origin/latest`; `git push origin <merge-sha>:refs/heads/latest` — the explicit-SHA refspec guarantees fast-forward regardless of local branch staleness.
2. **Promote `master`:** in the SAME worktree: `git checkout --detach origin/master`; `git merge --no-ff origin/latest`; **gate on exact parents** (parent1 = prior master tip, parent2 = latest tip) **+ tree identity** (`tree(promo) == tree(latest tip)`); `git push origin <promo-sha>:refs/heads/master`. A promotion IS the release trigger — CI publishes images ONLY on a master push.
3. **Cleanup:** remove the worktree; `git branch -d` the temp branch. Base every merge on `origin` refs. A worktree `.git` is a POINTER file — never `cp -a` to /tmp and run git there (the 2026-11-09 detached-HEAD incident).

**Invariant:** `origin/master^{tree}` == `origin/latest^{tree}`.

---

## 6. Ship tail (execution checklist — capture everything, assume nothing)

### 6.1 CI build

- **WAIT for the CI build** after the master push (a queued build is not a built image). Normal band **9-13 min**; the 6-hour buildx hang precedent (#190) was GitHub-side — an identical-content retry rebuilt clean; there is still no `timeout-minutes` on the build job.
- **A zero-delta push emits NO workflow event** (`on.push.paths` self-documentation) and the Actions-UI retry rebuilds NOTHING. Proven retrigger: a one-line null-content commit → `--no-ff latest` → `--no-ff master` promotion.
- **Version expectation:** CI counter at **#204 (v204)** as of 2026-11-20 — expect this release near **v205+; capture the run number at ship, do not hardcode.** `/fe/api/version` must return the new `v<run_number>` on both hosts.

### 6.2 Image digest verification (registry-side, NOT from the CI log)

`~/llmproxy-verify/verify_image.sh <tag>`: anon token → manifest → OCI index → amd64 → config blob (**Env KEYS only — never values**) → largest layer → digest; extract the binary; `strings` for the commission marker.
- **Marker:** the new lane constant / machine-code strings — `GroupLaneImageGen`'s string value alone is too generic; prefer the **exhaustion machine-code constant** (the imggen analog of `decision_all_members_failed`, `pkg/decision/group.go:57`) and **`image_misroute`** — pin the exact literals from phase-1's ledger at gate time. Avoid `sticky*` marker classes (Tailwind false hits).
- **Cache rules:** results cached per-tag under `~/llmproxy-verify/cache/<tag>/`; MOVING tags (`latest`/`master`) go stale after new pushes until `--no-cache`; per-SHA tags are immutable ⇒ safe. Rate budgets: Docker Hub anon 100/hr.

### 6.3 Capture observed live state — BOTH hosts, BEFORE deploy (the anchor-of-record)

**Never assume.** Context-of-record (2026-11-20) SAYS daoduc helm rev 51 (post Phase-3 oauth2 cutover) / primary gen 44 @ v204 `abd61d7` digest `cfc34862` — the plan treats these as HINTS, and §8's rollback ladder resolves its placeholders ONLY from the measurements below, taken at ship time:

```bash
export HELM=/home/nea/bin/helm KUBECTL=/home/nea/bin/kubectl   # not on PATH on this host
# BACKUP (daoduc, k3s): kubeconfig = data-center-scripts hetzner/connection/kubeconfig.yaml, context hetzner, ns llmproxy
$HELM --kubeconfig <dcs>/hetzner/connection/kubeconfig.yaml --kube-context hetzner -n llmproxy history llm-supervisor-proxy   # ← rev N (record)
# PRIMARY (ensem, RKE2): ~/.kube/config, context default — NOT rancher-local-ensem-dev
$KUBECTL --context default -n llmproxy get deploy llm-supervisor-proxy -o jsonpath='{.spec.template.spec.containers[0].image}' # ← tag+gen (record)
# dcs pins-of-record: read BOTH pin files (paths in 6.7); record current pinned sha7
```

### 6.4 Deploy BACKUP FIRST — daoduc (`llm.daoduc.org`)

1. **Pre-render gate:** rendered-vs-live must differ ONLY in the image tag (`kubectl diff` is UNUSABLE — the nginx webhook rejects duplicate paths). **`git diff <old> <new> -- k8s/` must be EMPTY** (this commission does not touch the chart; a non-empty diff is a STOP).
2. `helm upgrade` with the verified tag (kubeconfig `hetzner/connection/kubeconfig.yaml` from the **data-center-scripts** repo — the designated secret store; never echo secret VALUES, paths only; context `hetzner`, ns `llmproxy`). Take the pre-deploy DB backup the house flow mandates (the tier-2 anchor).
3. Verify: rollout complete, **`restartCount == 0`** (a crash-loop is a STOP, but see rider R3 before attributing), `/healthz` 200, `/fe/api/version` = new build, `/v1/models` 200.
4. **Quick probe llm.daoduc.org:** `/healthz` 200; edge `/` 302; `/v1/models` 200. Pod-direct admin if needed: `kubectl -n llmproxy port-forward pod/<pod> 14330:4321` — container port **4321, NOT 8080**; pod-direct `/fe/api/*` bypasses OAuth (unauthenticated inside the binary by design).

### 6.5 Deploy PRIMARY LAST — ensem (`llm.ensem.dev`)

1. `kubectl apply` (`~/.kube/config`, context **`default`** — NOT `rancher-local-ensem-dev`; binaries `/home/nea/bin`). **Confirm the apply source:** the live spec must match the pinned GitOps manifest before applying, so an apply cannot silently revert fields.
2. Verify: `restartCount == 0`; generation bumped (from 6.3's capture); `/healthz` 200; `/fe/api/version` = new build; `/v1/models` 200; **the text lane still answers** (one authenticated `/v1/chat/completions` 200 — the widening must not have disturbed the text lane); one `/v1/decisions` 200 (the decision lane composes through the same widened fold — D-3).
3. Primary stdout rotates ~7 min under load; the requests ring holds 100 — establish the observation window before grepping. Stdout never carries FE event names — grep `[RACE]`/`[LB-FAILOVER]`/`[grouplb]`.

### 6.6 Post-deploy smoke of the commission itself

- **The live-dormancy proof** (S-1 — the OTHER of the two EMPTY id-set diff gates, distinct from the pre-merge catalog-exclusion regression gate at G10(a)): the LIVE `/v1/models` id-set vs the 6.3 pre-deploy capture, same host, pre-vs-post deploy — diff must be EMPTY until imggen-group rows exist (they cannot exist yet — see 6.8). This gate diffs the live catalog across the deploy; G10(a) diffed old-vs-new binary behaviour on one store before the merge; neither substitutes for the other.
- After 6.8 (rows created): one imggen-group `/v1/image_generation` request ⇒ 200 with the winner's member metered; one wrong-lane request (imggen group id posted to `/v1/chat/completions`) ⇒ 400 **`image_gen_misroute`** (the CHAT-family code, D-15 — the imggen lane's own code `image_misroute` fires only on `/v1/image_generation`); a scoped token granted the group id ⇒ 200; a token WITHOUT the grant ⇒ 403 `access_denied` (never 502) — the D-7 composition proof, live.

### 6.7 dcs pins — two-step, daoduc-first (pins move together)

1. backup `infranstructure/k8s/llmproxy/hetzner-daoduc/values-pinned.yaml` FIRST, then primary `infranstructure/k8s/llmproxy/rancher-local-ensem-dev/deployment.yaml` (`docker.io/...:<sha7>` + digest/push-time/rollback comment).
2. Branch `feature/llmproxy-pin-<sha7>` → `--no-ff` → `latest` → push; `main` untouched. Re-fetch/rebase/retry on drift, NEVER `--force`. Pins record the highest deployed version across hosts.

### 6.8 ⚠️ LIVE-STORE CONFIG STEP — confirm with caller BEFORE writing rows

**Standing grant (Emperor, 2026-11-17): feature/bug-fix work AUTO-DEPLOYS without asking** — "don't need to ask me except critical thing happen". **The never-auto-ask-back list STANDS**, and it includes: *"live-store CONFIG mutations (models/credentials/tokens rows) — code fixes fine, store writes not."* Creating the imggen group rows (+ any scoped-token grants) in the LIVE store is exactly that. Therefore:

> **STOP-GATE: after both hosts serve the new image, present the caller the ready-to-write row set (group id, members, weights, any token grants) and CONFIRM before writing.** Code deploys needed no ask; the store write does. Until confirmed, the commission is deployed-but-dormant on both hosts (no imggen-group rows exist; all lanes byte-behave as before — that dormancy is itself a safety property, and 6.6's first bullet proves it).

### 6.9 Ship record

Record in the artifact: merge-sha + promo-sha + CI run number + digest, both hosts' pre/post state (6.3), probe logs, the rollback anchors as MEASURED, the 6.8 confirmation status, and the two release-note conditions: (1) **the misroute 400 family is NEW, with BOTH machine-code strings recorded** — `'image_misroute'` on `/v1/image_generation` (D-7.4) AND `'image_gen_misroute'` on the chat family `/v1/chat/completions` + `/v1/messages` (D-15; wrong-kind/wrong-lane posts move from confusing 502s to clean 400s — a fix, not a regression; smoke NOT amended to expect 502); (2) the ADR O1 supersession (D-11).

---

## 7. Sequencing riders (checklist items, not afterthoughts)

- **R1 — Phase-4 ensem OAuth2 cutover digest gate.** The cutover plan pins digest `cfc34862` (v204). **If imggen-groups ships BEFORE the Phase-4 cutover window**, refresh that plan's digest gate as a **one-line doc update on ITS branch** — `docs/phase4-ensem-execution-plan` @ `cd445c8` (verified via ls-remote; the file is NOT on `latest` — do not go looking for it in this worktree). The line cites the NEW v-shipped digest; nothing else on that plan changes.
- **R2 — OAuth2 config untouched.** This commission ships a normal image deploy — **no oauth2 knobs, no oauth2-related env or config writes**, on either host. If any step appears to need one, STOP: that is a different commission.
- **R3 — Pre-existing v204 defect: `sendHeartbeat` nil-pointer panic** (`pkg/proxy/heartbeat.go:114`, the SSE `f.Flush()` post-disconnect; SEPARATE commission, known before this ship). **Do not conflate any post-deploy crash with this commission — READ THE STACK before any rollback decision.** A panic whose top frames are `heartbeat.go`/SSE-flush is the known defect (triage: note + continue); anything with `imggen`/`grouplb`/`group_lane` frames is THIS commission (→ §8).

---

## 8. Rollback

### 8.1 The ladder (placeholders resolved ONLY from 6.3's measurements — NEVER from this document)

| Tier | Action | When / ordering |
|---|---|---|
| **Tier-1** | daoduc: `$HELM --kube-context hetzner -n llmproxy rollback llm-supervisor-proxy <rev-before-deploy>` (the 6.3-captured rev). primary: `$KUBECTL --context default -n llmproxy set image deploy/llm-supervisor-proxy llm-supervisor-proxy=docker.io/disillusioners/llm-supervisor-proxy:<prior-tag>` (the 6.3-captured tag) | the release degrades the live service; tier-1 is the FIRST move and restores the last known-good release. **The incident path never strips a newer release than the incident requires** — read the stack first (R3). |
| **Tier-2** | one revision further back (same command shape, next-preceding helm rev / image tag) | tier-1 proved insufficient — the symptom survives |
| **dcs** | rollback does NOT revert the dcs pin. A host rollback changes the LIVE workload only; if the pin has drifted from the rolled-back live state, MEASURE, compare, act on the delta deliberately — never push dcs as part of an incident rollback. | always |

### 8.2 DATA-COMPAT ANALYSIS — the position, argued

**Branch 1 — NO new migration lands (the research expectation, D-1).** Imggen-group rows are ordinary `kind='group'` rows with image-gen members; nothing schema-level distinguishes them. Under an OLD image (rollback target):

- `MemberLane(kind)` has no image-gen arm ⇒ image-gen members carry **no lane evidence**; a group of only image-gen members folds to `("", false)` — **unknown lane**.
- **Text-lane request naming the group:** the sweep's servability predicate is chat-only (`isServableGroupMemberBase`, chat or legacy `""`) ⇒ zero servable members ⇒ the group **fails closed somewhere on the credential/provider/upstream path** — **ALL paths, not one envelope** (adopted wording A-2): the chat sweep refuses the group; the imggen route's resolution fails later at credential/provider lookup — the exact envelope is path-dependent; the invariant is **never a wrong-lane serve**, never a specific 502 shape.
- **`/v1/models`:** the old fold cannot classify the group as an imggen group ⇒ the group id is LISTED as an ordinary model (unknown lane is not decision-lane, so the `:435`-class exclusion does not fire). Cosmetic-only surface noise.
- **FE:** the old FE ships in the old binary (embedded) — 11 tabs, `group_models` tab, unknown-lane groups folded into the chat tab. Read-only display fine; an EDIT attempt fails write validation LOUDLY (the old G3 rejects image-gen members: "members must reference existing chat-kind models").
- **Verdict: BENIGN-INERT.** No cleanup is REQUIRED. The rows sit unroutable-and-invisible-to-the-new-surfaces until the new image is re-deployed (the normal repair; re-deploy is preferred over cleanup). OPTIONAL operator cleanup (disable/delete the rows via the admin API under the OLD image) exists but changes nothing the old binary serves.
- **Authz:** a token granting the imggen group id keeps the grant (group ids are lane-agnostic, D-7.1); requests naming it under the old image get the fail-closed refusal above (A-2 — path-dependent envelope, never a wrong-lane serve). No privilege widens or narrows.

**Branch 2 — a migration DOES land (035+; P0.4 branch 2).** Migrations run **in-process at boot, pre-cache-priming** (`migrate.go`; no migrate Job) and the registry is an append-only slice — an old-image rollback simply does not know `035` exists and never runs it down; the old binary ignores the extra columns/rows. Rollback stays benign; **the row forward-compat of Branch 1 applies unchanged** (the rows are still `kind='group'` rows). If the migration rewrote EXISTING rows (it should not — pre-file it if it does), then and only then does the token-authz-style reverse-first ordering rule activate; the pre-file must say so explicitly.

### 8.3 Health/smoke after any rollback

`/healthz` 200 · edge `/` 302 · `/v1/models` 200 (id-set back to the 6.3 capture, modulo Branch-1's listed-group cosmetic) · `restartCount == 0` after settle · one chat 200 + one decision 200 (the two lanes that must never regress). Port-forward: container port **4321** (`kubectl -n llmproxy port-forward pod/<pod> 14330:4321`), never 8080.

---

## 9. Risks & abort criteria

| # | Risk | Impact | Mitigation / abort |
|---|------|--------|--------------------|
| R1 | The lane-vocabulary widening (D-3) drifts between BE and FE (ADR-19 violation shipped) | High | G15 same-commit proof; abort on split commits |
| R2 | A group-lane fold regression disturbs the TEXT lane (the substrate every ensemble turn rides on) | Critical | G2 + the text-lane regression pins + 6.5's live text-lane probe; ANY text-lane regression ⇒ tier-1, no deliberation |
| R3 | Post-deploy crash mis-attributed (the known heartbeat defect vs this commission) | High | R3 stack-first rule before any rollback |
| R4 | Live-store rows written without caller confirmation | Medium | 6.8 STOP-GATE is absolute; deployed-dormant is a safe state |
| R5 | Pack B count copied stale (37 moves with every new package) | Low | re-measure at gate (G3); the `(/\|$)` anchor is the invariant, not the count |
| R6 | tsc freeze misread (exit 2 is expected; line-number verdicts are wrong) | Low | G6 count+file-set verdicts only; SettingsPage frozen at 5 |
| R7 | Zero-delta CI non-event mistaken for a green build | Medium | 6.1: wait for the run number; retrigger recipe if no event fires |
| R8 | The 6.3 anchors captured AFTER deploy (anchor contaminated by the new release) | High | capture block runs BEFORE the first helm upgrade, recorded in the artifact timestamped |

## 10. Exit criterion

All gates G1-G15 green (flakes adjudicated per §4) → landed (`latest` + `master`, tree-identity invariant holds) → CI run number captured → digest verified registry-side with the commission marker → both hosts serving the new `/fe/api/version` with `restartCount == 0` and the text + decision lanes probed 200 → dcs pinned two-step → live-state anchors + rollback ladder recorded → the 6.8 caller confirmation either granted (rows written + commission smoke 6.6 complete) or explicitly parked with the dormancy proof attached → ship record filed.
