# Phase 3: Gate + Ship — credential-groups (release gate → land → CI → image verify → deploy → live verification → rollback)

**Commission:** credential groups — management-only bundling of same-provider credentials; flattened at model-resolution; credential selection never sees groups.
**Plan file:** `.agents/shared/planning/credential-groups/phase3-plan.md` (this file).
**Sibling plans (referenced BY FILENAME ONLY — do not wait for them):** `phase1-plan.md` (backend: migration 033, `pkg/models` types, manager CRUD, the flatten seam, decorator forwards, API surface, AST pin), `phase2-plan.md` (frontend: "Credential Groups" tab, group member editor, model-form picker, FE tests).
**Research inputs (this worktree, read-only, verified 2026-11-16):** `research-store-api.md`, `research-seam.md`, `research-fe.md`.
**Worktree (ALL work):** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-cred-groups` — git worktree, branch `feature/credential-groups`, base `2b987bf` (= `origin/latest` tip at plan time).
**Author lane:** plan-creation worker. **This file is the ONLY artifact written by this lane.** No code, no builds, no test runs, no git mutations, no commits, never a push.

---

## 1. Objective

Prove the credential-groups feature is ship-grade and land it on **both** production hosts, with the release gate executed as the **local** gate (CI does not gate Go tests or the FE), an image-verified artifact, a backup-first deploy, a live rotation proof that demonstrates the flat list draws from **both** a direct credential and a group-member credential, and a rollback path that is documented, rehearsed and — because of the group-ref persistence hazard proven in §3 R-1 — **executable without a data-repair step**.

**Single-sentence completion test:** the credential-groups feature is shipped when (a) the local release gate is green on the feature branch tip, (b) the same commit is on `origin/latest` and `origin/master`, (c) CI has published an image whose binary contains the `credential_groups` marker and whose digest matches the digest running on BOTH hosts, and (d) live traffic against a mixed direct+group model has been observed serving from both a direct credential and a group member — with the rollback path proven to restore service without data repair.

---

## 2. Scope

### 2.1 In Scope

| # | Item | Why it is in scope |
|---|------|--------------------|
| S-1 | **Local release gate** on the feature branch tip: Pack A + Pack B + `make test` + FE gate (tsc count-baseline, vitest, vite zero-mod proof) + `go vet`/`go build` + gofmt zero-new-drift + anti-vacuous `--- PASS` proof for every added `func Test` | CI gates **neither** Go tests nor the FE (`docker-ci.yml:104` swallows Go-test failures with `|| echo`; the FE is built but never type-checked). The local gate is the real gate. |
| S-2 | **Migration-033 pre-flight** — because this feature SHIPS a schema migration (unlike every train since 032) | `pkg/store/database/migrate.go:24-78` is an **ordered slice, not a directory scanner**: the registry append at `:78` is what ACTIVATES `033_add_credential_groups.up.sql`. The runner is **forward-only**; there is no down-runner (`migrate.go:81-94`). Prior trains all shipped "no schema migrations (latest remains 032)" (`values-pinned.yaml:27,33,41,47,54,65,75,85`); this train breaks that invariant and therefore needs its own pre-flight + dump gate. |
| S-3 | **Landing** (feature branch → `origin/latest` → `origin/master`) per the house recipe | `master` is the release line; a master push IS the release trigger (`docker-ci.yml:110,117,121` gate every publish step on `github.event_name == 'push'` + ref `master`). |
| S-4 | **CI watch + image verification** (registry-only, `~/llmproxy-verify/verify_image.sh`) with a **feature-specific marker** | A green CI run only proves the build did not fail; the marker proves the running binary is the one containing THIS feature. |
| S-5 | **Deploy** — BACKUP (llm.daoduc.org, k3s) FIRST, PRIMARY (llm.ensem.dev, RKE2) LAST | llm.ensem.dev carries the ensemble's own LLM traffic; daoduc is the standby where a bad build must be discovered first (blueprint "Live Deployment & Cluster Topology"; precedent `risks.md` R-4). |
| S-6 | **Live verification** — the commission-mandated scenario: create a group from 2 real same-provider credentials, configure a mixed-ref model, drive REAL traffic, prove flat rotation draws from BOTH sources, run the regression + management-flow checks | This is the only place the flattening is proven end-to-end through the real engine, not a unit fixture. |
| S-7 | **Rollback**: documented tier-1/tier-2 anchors per line, the **group-ref data-repair** procedure (R-1), and the pin-revert obligation | A binary rollback alone does NOT restore service when a model references a credential group (§3 R-1) — the rollback plan must say so and carry the repair. |
| S-8 | **GitOps pin moves** in `data-center-scripts` (both endpoints move together) | The pins are the source of truth for the running images; a deploy without the pin move re-ships the old tag on the next apply. |

### 2.2 Out of Scope

| # | Item | Why it is out of scope |
|---|------|------------------------|
| O-1 | **Implementing** any backend/frontend code | Phase 1 (`phase1-plan.md`) and phase 2 (`phase2-plan.md`) own implementation. This phase verifies and ships. |
| O-2 | **The `k8s/` chart** | This feature is **CONFIG-ONLY** — no new env var, no new port, no new probe, no new chart field. The chart tree is byte-identical across every pin since `6845344` (`aa0f4535…18c`, verified: `git rev-parse HEAD:k8s origin/master:k8s` → same object). Gate: `git diff <base>..HEAD -- k8s/` MUST be empty. |
| O-3 | **A new model `kind`** | A credential group is a **new top-level entity**, not a 5th model kind (`research-store-api.md` §c). `kind` stays untouched; the `group` kind remains owned by model load-balance groups. |
| O-4 | **CI hardening** (adding `timeout-minutes` to the build job; fixing the `|| echo` swallow at `docker-ci.yml:104`) | Known gaps, explicitly **noted, not fixed** in this lane. Changing the workflow would alter the release trigger for every future train — a separate commission. |
| O-5 | **Schema down-migration / data-restore tooling** | No down-runner exists (`migrate.go:81-94`); the house `.down.sql` files from 022+ are "MANUAL artifact, best-effort". Migration 033 is `CREATE TABLE IF NOT EXISTS` — additive, so the table may be left in place (see R-2). |
| O-6 | **Secret rotation / scrubbing / `INTERNAL_ENCRYPTION_KEY` rollout** | Out of scope. The feature stores credential **references**, not keys (`research-seam.md` §e.12). Posture follow-ups stay with the owner. |
| O-7 | **Any change to the credential-plaintext-at-rest posture** (`config.db` stores provider keys plaintext when `ENCRYPTION_KEY` is unset) | Pre-existing standing risk; this feature touches no new secret surface. |
| O-8 | **The `decision-groups` commission's FE surface** | Parallel commission in a separate worktree (`llm-supervisor-proxy-wt-decision-groups`); the FE conflict forecast is in `research-fe.md` §(d) and belongs to that lane's gate. |

---

## 3. Ship risk register

> Ranked by deploy-blocking severity. **R-1 and R-2 are the two this feature introduces** that no prior train carried; the rest are standing house risks that this train re-opens.

| # | Risk | Sev | Evidence | Mitigation (task ref) | Residual |
|---|------|-----|----------|----------------------|----------|
| **R-1** | **🔴 ROLLBACK DATA HAZARD — a binary rollback does NOT restore service for a model that references a credential group.** The group ref is persisted inside the model's existing `credentials_json` as `{"credential_id":"", "group_id":"g1", "weight":N, "position":M}` (`phase1-plan.md` §Locked representation). The **old** binary's `models.CredentialRef` (`pkg/models/config.go:331-335`) has **no `GroupID` field**, so `json.Unmarshal` silently drops `group_id` and yields `CredentialID: ""`. **Proven empirically** (scratch program using the exact pre-feature struct): the old binary then (a) fails its own write-path validator — `store.go:1942` → `"credentials[0].credential_id is empty"` → **the model can no longer be saved/edited**; (b) on the `len==1` fast path `store.go:2588` → `GetCredential("")` → nil → `ResolvedCredential{}, false` → **502 for that model**; (c) on the `len>=2` engine path the selector (`pkg/credentiallb/selector.go:44`) carries `""` in `credentialIDs` → `GetOrSelect` may return `""` → `store.go:2607` → `credID == ""` → resolution fails. **A rolled-back binary silently breaks every group-referencing model.** | 🔴 blocker | `pkg/models/config.go:331-335` (struct, no `GroupID`) · `pkg/store/database/store.go:1942` (empty-id validator) · `:2588` (`==1` fast path) · `:2607` (`==2` engine path, `credID==""` guard) · `pkg/credentiallb/selector.go:44` (empty id in `credentialIDs`) · `pkg/store/database/store.go:597` (`parseCredentialsJSON` — unparseable→empty, no error) · **empirically reproduced 2026-11-16 in `/tmp/rollback-probe` with the exact pre-feature struct** | **T-4.4 (inline-then-delete before the feature is ever relied upon)** + **T-8.4 (rollback runbook carries the data-repair step)** + **T-8.5 (rehearsal on the BACKUP line BEFORE the primary deploy)**. Rule: a rollback to a pre-feature binary is only safe when **zero models reference a credential group** — verified by the T-4.4 group-usage census. If the census is non-empty, the rollback is `helm rollback` **followed by** the T-8.5 repair, not `helm rollback` alone. | The repair is a **write** to `models.credentials_json` (inline the group's members as direct refs) — it is reversible (the group's `members_json` is unchanged) and idempotent. It is **NOT** a schema change; the `credential_groups` table may simply be left in place (additive `CREATE TABLE IF NOT EXISTS`). |
| **R-2** | **🔴 MIGRATION-033 ACTIVATION TRAP — the SQL files can be silently inert.** `migrate.go:24-78` is an ordered slice, NOT a directory scanner; the four in-code trap comments at `:53-57`/`:59-63`/`:64-69`/`:70-77` say so verbatim. A `033` pair without the registry append at `:78` = **no table, no error, feature dead on arrival**. | 🔴 blocker | `pkg/store/database/migrate.go:11-12` (dual `go:embed` trees) · `:24-78` (ordered registry slice, 32 entries, latest `032` at `:77`) · `:53-57`/`:59-63`/`:64-69`/`:70-77` (trap comments) · `research-store-api.md` §(a) | **T-3.5** — the migration gate asserts BOTH the file pair AND the registry entry, and asserts the table EXISTS on a live DB after boot (not just that files exist). Ship-gate T-21 (phase1) is the unit-level twin. | Low — the check is a single `grep` + a live `to_regclass`/`.tables` query. |
| **R-3** | **🟠 SCHEMA CHANGE IN A TRAIN THAT HAS NEVER SHIPPED ONE.** Every recorded pin since `6845344` asserts "No schema migrations (latest remains 032)". This train ships 033 → the standing pre-flight ("migration diff must be EMPTY") is **inverted**: here the diff MUST be non-empty and MUST contain exactly `033`. A reviewer who applies the old rule mechanically will either block a correct build or wave through a wrong one. | 🟠 high | `values-pinned.yaml:27,33,41,47,54,65,75,85,103,117,137,148,162` (12× "No schema migrations (latest remains 032)") · `migrate.go:81-94` (forward-only runner, no down-runner) · `risks.md` R-1 (prior plan: "a build that introduces a migration must NOT ship under this plan") · `risks.md` §5.3 (schema rollback NOT SUPPORTED) | **T-3.5** replaces the empty-diff gate with a **scoped** gate: the diff MUST contain exactly `033_add_credential_groups.*` + the `migrate.go` registry append, and nothing else. **T-3.6** takes a **pre-flight DB dump** on each line BEFORE the deploy (the prior plan's B6/TOUCHES_STORE analogue) so a row-level incident has a restore path. | The migration is **additive** (`CREATE TABLE IF NOT EXISTS` + `CREATE INDEX IF NOT EXISTS`, no backfill, no `models` column change — `phase1-plan.md` §(a)). Rolling the BINARY back leaves the extra table orphaned-but-harmless; the old binary never reads it. No down-runner is needed for correctness. |
| **R-4** | **🟠 DEPLOY-ORDER VIOLATION — deploying the primary first drops the ensemble's own LLM path.** llm.ensem.dev carries all provider traffic; daoduc is standby. | 🟠 high | blueprint "Live Deployment & Cluster Topology" (rollout order) · `risks.md` R-4 · precedent `risks.md` R-20 (agent-driven §4 loses its own reasoning lane) | **T-7.1–T-7.4** BACKUP FIRST, with a **binary** gate (§7.4) between the two lines. **T-7.5** — the PRIMARY step is executed from a human terminal or pre-staged commands; **agent-driven execution of the primary deploy must not be attempted**. | Process discipline only; nothing technically enforces the order. |
| **R-5** | **🟠 CI STALL / NO-EVENT.** The push trigger is paths-filtered (`docker-ci.yml:12-15` `'**'` minus `!.git/**`) → a **zero-delta push emits NO workflow event at all**; the Actions-UI retry rebuilds nothing. A `#190`-style 6h buildx hang is GitHub-side (identical content rebuilt clean as `#191`). No `timeout-minutes` on the build job (verified absent). | 🟠 high | `docker-ci.yml:4-7` (own warning comment) · `:12-15` (paths filter) · `:110,117,121` (`event_name=='push'` publish gates) · blueprint "Live Deployment & Cluster Topology" (CI release mechanics; band 9–13 min; `#190` hang) | **T-5.1** watch in the FIRST minutes (a run that has not started by ~3 min after the push = no event, not a slow runner). **T-5.4** the proven retrigger: one-line **null-content commit** on the branch → `--no-ff` latest → `--no-ff` master promotion (precedent `wi-7769a812`: `429a6ef→d9cdbea→487be74`). | A hang is recoverable; a NO-EVENT is invisible without the early watch. |
| **R-6** | **🟠 `|| echo` CI GAP — a red Go test in CI is reported green.** | 🟠 high | `docker-ci.yml:104` — `go test ./... -v 2>&1 \|\| echo "No tests found or tests completed"` | **Not fixed in this lane** (O-4). Mitigated by the fact that **the local gate (T-2) is the real gate** and by **T-5.1** reading the CI log's actual test output rather than the workflow's conclusion. Record explicitly in the ship record that CI's green is NOT evidence of test passage. | Standing; the local gate carries the load. |
| **R-7** | **🟠 IMAGE-VERIFY CACHE STALENESS — a moving tag can serve a STALE binary.** `~/llmproxy-verify/cache/<tag>/` is reused unless `--no-cache`; `latest`/`master` are moving. A cached `latest` from a previous train would "verify" while proving the OLD image. | 🟠 high | `~/llmproxy-verify/verify_image.sh` header (`--no-cache` semantics) · blueprint "Verification & Build Harness" (IMAGE VERIFICATION; moving tags go STALE) | **T-6.2** — verify the **per-SHA tag** (immutable → cache-safe), and **T-6.3** — re-run `latest`/`master` with `--no-cache` and assert the **index digest is IDENTICAL** to the per-SHA tag's digest (three tags must agree). | Low when the digest cross-check is done. |
| **R-8** | **🟠 MARKER FALSE-POSITIVE — `strings \| grep -ci sticky` matches Tailwind CSS strings.** A marker that also appears in the FE bundle proves nothing about the BE feature. | 🟠 high | blueprint "Verification & Build Harness" (IMAGE VERIFICATION: never `strings\|grep -ci sticky`) · verified: `strings -a <current binary> \| grep -c 'sticky'` is non-zero on a build with no sticky work | **T-6.1** pins the marker to `CREATE TABLE IF NOT EXISTS credential_groups` — a **BE-only** literal that can only exist if migration 033's SQL is embedded in the binary. **Verified negative control**: the current live binary (`fd1649c`) returns **0** for that marker while returning **2** for `CREATE TABLE IF NOT EXISTS credentials` (the 005 precedent) — so the literal survives `-s -w` stripping and the marker discriminates. A **positive control** (a marker that MUST be present on any build) is also counted. | Low — the marker is BE-only and empirically discriminating. |
| **R-9** | **🟠 STICKY PINNING OBSCURES THE ROTATION PROOF — the live scenario may show ONE credential only, and be misread as "the group did not flatten".** The engine binds per `(modelID, conversationKey)`; `conversationKey = sha256(modelID|tokenID|leadingUserRun)` (`pkg/credentiallb/key.go:48-51`) — so **the SAME first user message pins to the SAME credential**. | 🟠 high | `pkg/credentiallb/engine.go:255-297` (in-TTL binding hit → reuse) · `:346` (`newlyBound=true` log) · `pkg/credentiallb/key.go:48-51` (key = modelID+tokenID+leadingUserRun) · `pkg/proxy/handler.go:687-694` (key computed from the **leading user run**) | **T-9.4** — the scenario uses **distinct leading user messages** (distinct conversation keys → distinct bindings → the weighted selector re-picks), or **distinct tokens** (the key is token-salted). **T-9.5** names the evidence channel explicitly: the `[credentiallb] model=… conv=…→cred=… newlyBound=true` stdout line (`pkg/credentiallb/engine.go:346`) and the `[DEBUG] Race attempt N calling internal provider: … credential_id=…` line (`pkg/proxy/race_executor.go:576`) — both verified present in the live daoduc stdout. | Medium — a single-source observation is a **failed proof**, not a weak pass. T-9.6 makes it binary: BOTH sources must appear or the scenario is re-run with more turns. |
| **R-10** | **🟡 STALE IMAGE-VERIFY/DEPLOY EVIDENCE — a cached or pre-deploy artifact is presented as post-deploy proof.** | 🟡 med | house practice across `.agents/tester/RESULTS/` | Every evidence artifact is **named by its tag + digest + timestamp** and captured fresh in the run window. The **running** pod's `imageID` digest (not the tag) is the deploy-side identity proof (T-7.3). | Analyst error; prevented by naming discipline. |
| **R-11** | **🟡 SECRETS DISCIPLINE — credential values or tokens echoed into a log, a report, or a commit.** The live scenario necessarily touches real credentials and a real proxy token. | 🟡 med | `pkg/ui/server.go:2437` (`maskAPIKey` — first 8 chars + `***`); the FE already masks; the BE masks on the credentials endpoints (`server.go:2454,2521,2555`) | **T-9.3 / T-7.4**: credential **IDs and providers only** in every artifact; API keys never printed (the `/fe/api/credentials` response is already masked — take the IDs from it, not the keys). The proxy token is read via `read -rs` into a shell variable and never echoed, never committed (precedent `runbook.md` §3.3). The house rule for the DB-dump procedure (path-only reference; that file carries plaintext DB passwords) applies to T-4.3. | Discipline only. |
| **R-12** | **🟡 WRONG-CLUSTER EXECUTION — an unset kubeconfig is fail-OPEN to the PRIMARY.** `kubectl --kubeconfig ""` falls through to `~/.kube/config` = llm.ensem.dev. | 🟡 med | `risks.md` R-14 (fail-open, not fail-closed) · verified cluster servers: daoduc `https://46.62.137.105:6443`, ensem `https://192.168.1.250:6443` | The environment block is exported **first**; every `export KC=…` is preceded by `: "${KC_DAODUC:?environment block not loaded}"`; the **G0 server assertion** runs before any mutation and hard-exits on mismatch (T-7.1, T-7.2). | Only if BOTH the guard and G0 are skipped. |
| **R-13** | **🟡 PUSH WITHOUT USER INSTRUCTION — the repo's standing red line.** Pushes use the SSH pushurl; the `data-center-scripts` pin repo also pushes. | 🟡 med | project standing note (2026-10-19: "NEVER push w/o user instruction"); `git remote -v` on the proxy repo: fetch HTTPS, push `git@github.com:disillusioners/llm-supervisor-proxy.git` | **Every push in this plan is marked `⛔ USER-AUTHORIZED GATE`** and carries a **pause point** (T-3.7, T-5.2, T-7.6). The plan does not self-authorize a push. | Process discipline. |
| **R-14** | **🟡 PIN NOT MOVED / PIN MOVED WITHOUT THE DEPLOY — the next apply re-ships the old tag.** `helm rollback`/`set image` do not touch the repo. | 🟡 med | `risks.md` §5.5 (mandatory pin revert after rollback); precedent pin-commit trail `values-pinned.yaml:8…:162` (12+ repin blocks, each naming digest + CI run + rollback anchor) | **T-7.6** moves both pins in `data-center-scripts` (backup + primary, TOGETHER) with the full ledger comment block; **T-8.6** reverts both pins after any rollback. | Analyst error; prevented by the pin-move being an explicit task. |
| **R-15** | **🟡 IN-FLIGHT STREAM CUT AT ROLLOUT.** Both lines terminate the pod; the binary drains for only 5 s (`cmd/main.go:481-491`), then the 30 s grace cut. | 🟡 med | `risks.md` R-7 / §6.5 · `k8s/templates/deployment.yaml` (default 30 s grace) | Accepted (precedent, every train). Clients retry. Recorded in the ship note; the **primary** line's cut hits the ensemble's own traffic (R-4 amplification). | Inherent to a rolling restart. |
| **R-16** | **🟡 `helm rollback` restores the SAME image (image-level no-op).** Historical floating-`:latest` failure mode. | 🟡 med | `risks.md` R-6 | Every pin is a per-SHA tag and the T-8.1 pre-flight aborts a no-op deploy, so the premise cannot recur. If a rollback looks image-identical: **STOP and investigate** — do not patch around helm. | Deliberately no `kubectl set image` fallback on daoduc (it desyncs helm state). |
| **R-17** | **🟡 WRONG LINE / WRONG PORT in the live scenario.** The daoduc port-forward must target the **container** port 4321 (not 8080 — `14330:8080` is refused). | 🟡 med | blueprint (Live Deployment: port-forward targets container port 4321) | T-7.4 pins the exact port-forward form; the scenario's evidence is collected from the port-forwarded pod (pod-direct `/fe/api/*` bypasses OAuth) and from public ingress for the request path. | Low. |
| **R-18** | **🟡 FE BUNDLE / STATIC-AREA DRIFT — the committed `pkg/ui/static` no longer matches the FE source.** The static area is TRACKED and embedded (`pkg/ui/server.go:90` `//go:embed static/*`); a stale hashed asset 404s the UI. | 🟡 med | blueprint "Frontend Architecture" (embed discipline; `TestEmbedStatic_AllAssetReferencesResolve`) · `research-fe.md` §(e) | **T-2.5** — the FE gate's **zero-mod proof** (sha256 of `pkg/ui/static/` before/after `vite build`) is the mechanism; the verdict is count+file-set for tsc and byte-identity for the static area, never "looks the same". | Low — mechanically checked. |
| **R-19** | **🟡 GOVERNING-TREE DRIFT — `origin/latest` moves under the feature branch between gate and land.** | 🟡 med | tips move daily (verified this session: `origin/latest` = `2b987bf`, `origin/master` = `fd1649c`) | **T-3.2** re-fetches and re-verifies the tips immediately before the merge; **T-3.3** merges `--no-ff origin/latest` INTO the feature branch and **re-runs the gate** if the merge is non-trivial (any conflict or any file the feature touched). The landing uses an **explicit-SHA refspec** (`git push origin <merge-sha>:refs/heads/latest`) so local staleness cannot make the push non-FF. | Low — the recipe is re-proven. |
| **R-20** | **🟡 QUARANTINE MISREAD — an expected quarantined failure is mistaken for a feature regression (or vice versa: a real regression is dismissed as "quarantined").** | 🟡 med | `.agents/tester/QUARANTINE.md` (5 active rows: `TestStoreEngine_CloseLifecycle`; the MCP SSRF octal/decimal/hex trio; the usage-stress SQLITE_BUSY pair; `TestEngine_GetOrSelect_TOCTOU_CoolingReverify`; the httpbin redirect pair at `handlers_api_test.go:1864/:1904`) | **T-2.6** adjudicates every failure against the quarantine table **by signature** (file:line + test name), never by name alone; an UNQUARANTINED failure is a gate FAIL. The **httpbin pair is the ONLY expected Pack-B failure** (reconfirmed as the sole failure in 3 consecutive gates). | Low — signatures are stable. |
| **R-21** | **🟢 `kubectl diff` is unusable on the primary** (the nginx webhook rejects duplicate paths) — a reviewer may read its failure as drift. | 🟢 low | blueprint (Pre-render gate: `kubectl diff` unusable) | T-7.2 uses the **server-side dry-run + printed diff** on the primary and the **rendered-vs-live** diff on the backup; the verdict is "differs ONLY in the image tag". | Noise during triage. |
| **R-22** | **🟢 `/healthz` is not publicly exposed (public = 404)** — an outside-in check concludes "failure". | 🟢 low | blueprint (split ingress: `/healthz` has no ingress path) · `risks.md` R-16 | All health checks are **in-cluster via port-forward**; the external signal is `/v1/models` 200. | Wasted escalation. |
| **R-23** | **🟢 Helm rev ≠ Deployment rev misread as drift** (37 vs 88 on daoduc at plan time). | 🟢 low | verified live: daoduc helm rev **37**, Deployment gen **88**; ensem gen **34** | The helm revision is the rollback anchor on the backup; the Deployment generation is the primary's anchor. Never compare them across lines. | Confusion. |
| **R-24** | **🟢 Port-forward / scratch-process leak between gates.** | 🟢 low | house hygiene precedent (`runbook.md` §3.2b) | Every port-forward captures `PF_PID`, is killed and waited in the same block; scratch dirs are removed at session end. | Low. |
| **R-25** | **🟢 Model-count divergence misread as deploy failure.** The two lines have separate DBs and already differ (verified live: daoduc 28 model rows, ensem a separate DB). | 🟢 low | `risks.md` R-25 | The gate compares the **id-set against the SAME line's own pre-deploy baseline**, never the count and never the other line. | Analyst error. |

---

## 4. TEST-MATRIX → GATE mapping

> The feature test matrix is `phase1-plan.md` §(h) (T-1..T-23) and `phase2-plan.md` (FE matrix). **Every row below states WHERE it is proven.** A row with no home is a gap, not a pass.

### 4.1 Backend matrix (from `phase1-plan.md` §(h)) — all in Pack A or Pack B by package

| # | Test | Proved WHERE | Gate that must show it | Pass criterion |
|---|------|--------------|------------------------|----------------|
| T-1 | Flatten — **mixed refs** (`[{c1},{g1},{c2}]` → `[c1, g1.members…, c2]`) | **Pack A** — `pkg/store/database` (the flatten helper lives in `store.go` per `phase1-plan.md` T-19) | T-2.1 Pack A | literal `--- PASS` |
| T-2 | Flatten — **dedup keep-first** | **Pack A** — `pkg/store/database` | T-2.1 | literal `--- PASS` |
| T-3 | Flatten — **ordering** (expansion strictly at the group ref's position) | **Pack A** — `pkg/store/database` | T-2.1 | literal `--- PASS` |
| T-4 | Flatten — **empty group** contributes nothing | **Pack A** — `pkg/store/database` | T-2.1 | literal `--- PASS` |
| T-5 | **E-3 single-group-ref** resolves a member (not 502) | **Pack A** — `pkg/store/database` (both resolver variants) | T-2.1 | literal `--- PASS` |
| T-6 | `==0` legacy degrade (empty flattened list → `ok=false`) | **Pack A** — `pkg/store/database` | T-2.1 | literal `--- PASS` |
| T-7 | `==1` fast-path regression — plain single direct ref identical | **Pack A** — `pkg/store/database` | T-2.1 | literal `--- PASS` |
| T-8 | `>=2` engine-path regression — plain 2-ref model → `GetOrSelect` | **Pack A** — `pkg/store/database` | T-2.1 | literal `--- PASS` |
| T-9 | **Same-provider validation reject** (group write + member add) | **Pack B** — `pkg/models` (`ValidateCredentialGroup`, G-7) — `pkg/models` is in the anchored Pack-B complement (verified: 33 pkgs) | T-2.2 Pack B | literal `--- PASS` |
| T-10 | Member existence reject (unknown member ID) | **Pack B** — `pkg/models` (G-6) | T-2.2 | literal `--- PASS` |
| T-11 | Expanded-cap reject (>16 expanded; raw ≤16 accepted) | **Pack B** — `pkg/models` (R-4) | T-2.2 | literal `--- PASS` |
| T-12 | Non-chat kind forbid (decision/imggen/group + group ref rejected) | **Pack B** — `pkg/models` (R-5) | T-2.2 | literal `--- PASS` |
| T-13 | **Add/remove member propagation** → every referencing model's engine state fresh | **Pack A** — `pkg/store/database` (fan-out `OnModelChanged`) | T-2.1 | literal `--- PASS` |
| T-14 | Deep-copy preservation (`GroupID` survives `deepCopyModelConfig` + `BuildModelCopy`) | **Pack B** — `pkg/models` (`pkg/models/copy.go:102-130`) + `pkg/modelscache` | T-2.2 | literal `--- PASS` |
| T-15 | **Deletion-while-referenced block** (409 + message names models) + **inline-then-delete** succeeds | **Pack A** — `pkg/store/database` (manager) **and** `pkg/ui` (HTTP 409 mapping) | T-2.1 | literal `--- PASS` |
| T-16 | Credential-delete extended guard (group-only credential → 409 naming the group) | **Pack A** — `pkg/store/database` | T-2.1 | literal `--- PASS` |
| T-17 | **CRUD API** — POST/GET/PUT/DELETE + 400/404/409/405 mapping + 64 KB cap | **Pack A** — `pkg/ui` (route table `server.go:195-197` + handlers) | T-2.1 | literal `--- PASS` |
| T-18 | **Decorator-forward through-tests** — each new method delegates exactly once | **Pack B** — `pkg/modelscache` (`fakeStrictSource`, `models_test.go:236/327/334`, exactly-one-delegation pattern at `:1179`) | T-2.2 | literal `--- PASS` |
| T-19 | **AST/structural pin** — no group value constructed/flowed in selection packages; `GroupID` read-restricted | **Pack B** — the pin file's package (per `phase1-plan.md` §(g): walks `pkg/credentiallb`, `pkg/stickyorder`, `pkg/skipcache`, `pkg/proxy`) | T-2.2 | literal `--- PASS` **+ the pin's own non-vacuity probe** (T-2.7) |
| T-20 | Zero-DB cached resolver (no DB call when flattening) | **Pack A** — `pkg/store/database` | T-2.1 | literal `--- PASS` |
| T-21 | Migration — table + indexes exist; **idempotent re-run** | **Pack A** — `pkg/store/database` (`migrations_test.go`) | T-2.1 | literal `--- PASS` — **plus T-3.5** (the LIVE proof that the registry append actually activates it; the unit test alone can pass with inert SQL) |
| T-22 | JSON-twin reject (group refs rejected in `pkg/models` config path) | **Pack B** — `pkg/models` | T-2.2 | literal `--- PASS` |
| T-23 | **Plain-list regression pin** — resolver output for group-free models identical before/after | **Pack A** — `pkg/store/database` | T-2.1 | literal `--- PASS` |

### 4.2 FE matrix (from `phase2-plan.md`)

| # | Test | Proved WHERE | Gate | Pass criterion |
|---|------|--------------|------|----------------|
| F-1 | "Credential Groups" tab renders the group list; empty state; label is **"Credential Groups"** (never bare "group") | **FE vitest** — `pkg/ui/frontend` (invoke vitest FROM `pkg/ui/frontend`) | T-2.4 | vitest file+test count grows, 0 failed |
| F-2 | Group member editor — add/remove a member; provider shown | **FE vitest** | T-2.4 | 0 failed |
| F-3 | Model-form picker — a group can be added to a model's credential list alongside direct refs | **FE vitest** | T-2.4 | 0 failed |
| F-4 | Delete-while-referenced → 409 → FE renders the **inline-then-delete** recovery flow using `used_by` | **FE vitest** | T-2.4 | 0 failed |
| F-5 | `useApi` credential-group mutations invalidate the model-list caches (the quadruple-delete discipline, `useApi.ts:971-976`) | **FE vitest** — `hooks/__tests__/` (the `useApiCredentials.test.ts` pattern) | T-2.4 | 0 failed — **plus the non-vacuity probe** (T-2.7): remove one delete and the test MUST fail |
| F-6 | Tab-order / adjacency pin (the credentials tab keeps its position) | **FE vitest** — `components/__tests__/SettingsPage.tabs.test.tsx` | T-2.4 | 0 failed |
| F-7 | **tsc zero-NEW** — error count + file-SET parity vs the frozen baseline (19 errors / 9 files) | **FE tsc gate** + `utils/__tests__/tsc-baseline.test.ts` | T-2.4 | `TOTAL_ERRORS`/`TOTAL_FILES` **and** per-file counts + key-set identical; tsc exits 2 (expected shape) |
| F-8 | **Static-area zero-mod proof** — `pkg/ui/static/` byte-identical after `vite build` | **FE vite gate** | T-2.5 | sha256 of every file identical before/after; `git status` clean |

### 4.3 Live matrix (the commission-mandated scenario — the only end-to-end proof)

| # | Check | Proved WHERE | Gate | Pass criterion |
|---|------|--------------|------|----------------|
| L-1 | Create a credential group from **2 REAL same-provider credentials** (via `/fe/api/credential-groups`) | **Live** — pod-direct `/fe/api/*` (bypasses OAuth) | T-9.1 | 201; GET detail returns the group + `used_by` |
| L-2 | Configure a model with **MIXED refs** (≥1 direct + the group) | **Live** — `PUT /fe/api/models` | T-9.2 | 200; the persisted `credentials` array shows one direct ref and one group ref |
| L-3 | **Flat rotation draws from BOTH sources** — real traffic; the direct credential AND ≥1 group member each serve traffic | **Live** — stdout `[credentiallb] …→cred=… newlyBound=true` (`engine.go:346`) + `[DEBUG] Race attempt N … credential_id=…` (`race_executor.go:576`) | T-9.4, T-9.6 | **BOTH** source classes appear in the evidence window |
| L-4 | Regression: `/v1/models` catalog shows **NO credential-group artifacts** (groups are not models, not credentials) | **Live** — `GET /v1/models` id-set vs the pre-deploy baseline | T-9.7 | id-set diff EMPTY |
| L-5 | Regression: `/healthz` 200; a **plain-credential model** behaves exactly as before | **Live** — port-forward `/healthz`; a request to a group-free model | T-9.7 | `ok`; plain model serves 200 with the same winner as baseline |
| L-6 | Management flow: delete-while-referenced → **409** with a clear message naming the referencing models | **Live** — `DELETE /fe/api/credential-groups/{id}` | T-9.8 | 409 + bare `{"error":…}` naming the model(s) |
| L-7 | Management flow: removing a group member updates **every** referencing model's effective flat list | **Live** — remove a member, then observe the next request's flat list | T-9.8 | the removed member no longer appears in the flat list for ANY referencing model |
| L-8 | **Migration 033 is live on both hosts** (the table EXISTS — proof the registry append activated it) | **Live** — schema query per line | T-3.5, T-7.4 | `credential_groups` present in the live DB on BOTH lines |

### 4.4 Coverage closure check

| Test-matrix row | Home |
|---|---|
| T-1..T-8, T-13, T-15, T-16, T-17, T-20, T-21, T-23 | Pack A (`pkg/store/database`, `pkg/ui`) |
| T-9..T-12, T-14, T-18, T-19, T-22 | Pack B (`pkg/models`, `pkg/modelscache`, `pkg/credentiallb`, `pkg/stickyorder`, `pkg/skipcache`) |
| F-1..F-8 | FE gate (vitest + tsc baseline + vite zero-mod) |
| L-1..L-8 | Live scenario (post-deploy, per line) |

**Every row has exactly one home. No row is "covered by the suite" in the abstract.**

---

## 5. Runbook — ordered tasks

> Every push is marked `⛔ USER-AUTHORIZED GATE` and carries a **pause point**. This plan does not self-authorize any push.
> **Environment block — run FIRST, in the ONE shell that will deploy** (same-shell rule; an unset `KC_*` is fail-OPEN to the PRIMARY — R-12):

```bash
# ---- environment block (T-0) ----
export WT=/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-cred-groups
export KC_DAODUC=/home/nea/Code/data-center-scripts/hetzner/connection/kubeconfig.yaml
export KC_ENSEM=/home/nea/.kube/config
export KCTL=/home/nea/bin/kubectl
export HELM=/home/nea/bin/helm
export DCS=/home/nea/Code/data-center-scripts
export CHART=$WT/k8s
export GOBIN_CACHED=/home/nea/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.linux-amd64/bin/go
export GO=$GOBIN_CACHED
export GOTOOLCHAIN=local GOPATH=/home/nea/go GOMODCACHE=/home/nea/go/pkg/mod GOCACHE=/home/nea/.cache/go-build
# pin params are set by T-5.2 (TARGET_TAG / TARGET_DIGEST / TARGET_RUN) — see T-0 note below
: "${KC_DAODUC:?environment block not loaded}"; : "${KC_ENSEM:?environment block not loaded}"
```

> **T-0 note on parameters.** `TARGET_TAG` (the per-SHA image tag), `TARGET_DIGEST` (the registry index digest) and `TARGET_RUN` (the CI run number = what `/fe/api/version` will report) **do not exist until CI publishes** — they are resolved by T-5.1/T-6.1. Every task below that needs them is **downstream of T-5**. Tasks T-1..T-4 do not.

---

### 5.1 T-1 — Freeze the tip + prove the boundaries (read-only, no build)

| # | Task | Command | Acceptance |
|---|------|---------|------------|
| T-1.1 | Record the frozen tip | `cd $WT && git rev-parse HEAD && git status --short` | clean tree except `.agents/shared/planning/credential-groups/` |
| T-1.2 | Record the change inventory | `git diff --stat <base>..HEAD` (base = `2b987bf`) | full file list; **every** file accounted for |
| T-1.3 | **Chart untouched** (O-2) | `git diff <base>..HEAD -- k8s/` | **EMPTY** — the chart tree must still be `aa0f45355ab7f8fdb0128f966dfa2032fd83118c` (verified identical at `HEAD:k8s`, `origin/master:k8s` and the main checkout) |
| T-1.4 | **Secrets discipline scan** on the diff | `git diff <base>..HEAD \| grep -nEi 'sk-[A-Za-z0-9]{8,}\|api[_-]?key["'"'"']?\s*[:=]\s*["'"'"'][^"'"'"']{12,}'` | **no match** — no credential value in code, tests, fixtures, docs or the FE bundle |
| T-1.5 | Confirm the naming invariants hold in the diff | `grep -rn "credential_group\|credential-groups" <touched files>` + a check that **no** new file uses bare "group" in a user-facing string | every user-facing string carries the full "credential group" qualifier (the bare noun is owned by model load-balance groups — `research-store-api.md` §(c)) |

### 5.2 T-2 — LOCAL RELEASE GATE (the real gate — CI gates neither Go tests nor the FE)

> **Hermeticity is load-bearing.** `go test` without HOME isolation rewrites `~/.config/llm-supervisor-proxy/config.json` (PORT=9797 leaks from the daemon env; upstream = httptest ephemeral). Every Go command below runs with a **fresh `HOME=$(mktemp -d)`** and the ambient `DATABASE_URL` / `APPLY_ENV_OVERRIDES` / `PORT` / `POSTGRES_*` / `DAEMON_PORT` **unset**.

| # | Task | Command | Acceptance |
|---|------|---------|------------|
| T-2.0 | Fresh hermetic env | `export HOME=$(mktemp -d); unset DATABASE_URL APPLY_ENV_OVERRIDES PORT POSTGRES_PASSWORD POSTGRES_USER POSTGRES_HOST POSTGRES_PORT DAEMON_PORT` | real `~/.config` untouched (assert the config.json mtime is unchanged after the run) |
| **T-2.1** | **PACK A** — the release slice | `timeout 300 $GO test -count=1 -v -timeout=240s ./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...` | **EXIT=0**; 9 packages ok; wall < 300 s (proxy is the long pole: 109–132 s observed) |
| **T-2.2** | **PACK B** — the anchored complement | `timeout 300 $GO test -count=1 -v -timeout=240s $($GO list ./... \| grep -vE 'github.com/disillusioners/llm-supervisor-proxy/(cmd\|pkg/(proxy\|store\|decision\|ui))(/\|$)')` | **EXIT=0** except the quarantined httpbin pair (R-20); **33 packages** today (verified) — a changed count means a package was added/removed and the anchor must be re-derived. ⚠️ **the module-prefix anchor is load-bearing** — a repo-relative re-run executes ALL packages and silently changes the gate's meaning |
| **T-2.3** | **make test** — the house landing gate | `make test` (2nd run with `GOFLAGS=-count=1`) | import guards 3/3 PASS; `go test ./...` green except the same quarantined pair. ⚠️ `make test` covers **NEITHER** the FE **nor** `go vet` — T-2.6/T-2.4 close that hole |
| **T-2.4** | **FE GATE** | `cd $WT/pkg/ui/frontend && npx tsc --noEmit` → then `npx vitest run` → then `npx vitest run src/utils/__tests__/tsc-baseline.test.ts` | tsc exits **2** (expected shape) with **19 errors / 9 files** = EXACT parity (count **and** per-file key-set); the baseline re-assertion is green; vitest green with **every added FE test showing a literal pass**. ⚠️ invoke vitest **from `pkg/ui/frontend`** — from the repo root the config + happy-dom are skipped and you get false failures |
| **T-2.5** | **FE static zero-mod proof** | `sha256sum pkg/ui/static/**/* \| sort > /tmp/static-before.txt` → `npx vite build` → `sha256sum … > /tmp/static-after.txt` → `diff` | **byte-identical** (the MECHANISM is the claim; content-hashed filenames differ per build). `git status -- pkg/ui/static` clean |
| **T-2.6** | **Static Go gates** (ensure.md Critical #1/#2) | `bash test/packs/build_gate_test.sh` **and** `$GO vet ./...` **and** `$GO build ./...` | both **EXIT=0 with empty output**; `make test` covers NEITHER — the house pack is the required closure (42 pkgs incl. the root stub + the real `./cmd` entry; `go build .` = the 3.7 MB inert stub — the real entry is `go build ./cmd`) |
| **T-2.7** | **Anti-vacuity + pin non-vacuity** | For EVERY **added** `func Test`: `grep -c -- "--- PASS: <TestName>" <gate log>` and `grep -c -- "=== RUN   <TestName>"` | each added test shows a **literal** `--- PASS` **and** `=== RUN` — count > 0. A test that appears in the file but never in the log is a **vacuous pass** → GATE FAIL. Plus: **mutation-probe the AST pin** (T-19) in a scratch copy — inject a forbidden group reference into a selection package and prove the pin **FAILS**; inject a comment-only path and prove it stays **green** (comment-awareness). Report-only pin gaps (one-hop reachability, exact-string match) are recorded, not blockers |
| **T-2.8** | **gofmt zero-new-drift** | Per touched `.go` file: `git show <base>:<file> \| gofmt -d` → base flagged set; `gofmt -d <file>` → HEAD flagged set; NEW = HEAD − base | **∅** for every file. ⚠️ **diff-hunk overlap is UNRELIABLE** (±3 context lines distort) — use the flagged-LINE-SET method. ⚠️ run `gofmt -l` on **ALL** touched `_test.go` — an aligned-fixture sweep silently adds whole-file drift. Prove non-vacuity with a deliberate drift injection in a scratch copy |
| **T-2.9** | **Flake adjudication** | For every failure: match against `.agents/tester/QUARANTINE.md` **by signature** (test name + file:line) | an UNQUARANTINED failure = **GATE FAIL**. The httpbin pair (`handlers_api_test.go:1864/:1904`) is the ONLY expected failure; `TestStoreEngine_PeakHourParity` is only valid in the 23:59:00–23:59:59.999 UTC window — adjudicate by time, not by name |
| T-2.10 | **Gate artifact** | Write `.agents/tester/RESULTS/<date>-credential-groups-release-gate.md` (the house artifact convention) | every gate above has a receipt (command, exit code, wall, counts); the anti-vacuous table is included |

**Gate verdict:** **T-2 is GREEN ⟺ T-2.1 + T-2.2 + T-2.3 + T-2.4 + T-2.5 + T-2.6 + T-2.7 + T-2.8 + T-2.9 are all green.**

---

### 5.3 T-3 — LANDING (feature branch → origin/latest → origin/master)

| # | Task | Command | Acceptance |
|---|------|---------|------------|
| T-3.1 | Re-verify the tips (R-19) | `git fetch origin master latest && git ls-remote origin refs/heads/master refs/heads/latest` | record the tips; if `origin/latest` moved **past** the gate base, the gate must be **re-run** on the merged result (T-3.3) |
| T-3.2 | **Re-confirm the branch topology invariant** | `git merge-base --is-ancestor origin/latest origin/master && git rev-parse origin/master^{tree} origin/latest^{tree}` | `latest` IS an ancestor of `master`; the two trees are identical (verified at plan time: both `53c4163c0e10fee0f7df279191effad81e00be61`) |
| **T-3.3** | **Merge latest INTO the feature branch** | `git merge --no-ff origin/latest` (on `feature/credential-groups`) | no conflict. If the merge touched ANY file the feature touched → **re-run T-2 in full** on the merged tree before proceeding |
| T-3.4 | Record the merge SHA | `git rev-parse HEAD` | the exact SHA that will be pushed |
| **T-3.5** | **⛔ USER-AUTHORIZED GATE — publish `latest`** | `git push origin <merge-sha>:refs/heads/latest` | the **explicit-SHA refspec** guarantees FF regardless of local branch staleness. ⚠️ **PUSH = SSH pushurl** (`git@github.com:disillusioners/llm-supervisor-proxy.git`; fetch stays HTTPS). ⚠️ **NEVER push without explicit user instruction.** ⚠️ A **zero-delta push emits NO workflow event** — the CI step (T-5) watches for this |
| T-3.6 | **Migration-033 pre-flight (R-2, R-3)** — the SCOPED gate that INVERTS the standing rule | `git diff <base>..<merge-sha> -- pkg/store/database/migrations pkg/store/database/migrate.go` | the diff MUST contain **exactly** the `033_add_credential_groups.{up,down}.sql` pair (both dialect trees) + the `migrate.go` registry append with its trap comment — and **nothing else**. The standing "no migrations" rule does NOT apply here; the standing rule's *shape* does: the diff must be exactly the intended set, no more, no less. **Assert the table EXISTS on a live DB** (see T-7.4/L-8) — a unit test alone cannot prove the registry append activated the SQL |
| T-3.7 | **⛔ USER-AUTHORIZED GATE — promote to `master`** | in the SAME worktree: `git checkout --detach origin/master` → `git merge --no-ff origin/latest` → verify **parent1 = prior master tip**, **parent2 = latest tip**, **`tree(promo) == tree(latest tip)`** → `git push origin <promo-sha>:refs/heads/master` | the promotion merge is a **zero-content `--no-ff`** commit; the tree check proves master carries exactly latest's content. ⚠️ **master IS the release trigger** — this push publishes the image (T-5) |
| T-3.8 | **⛔ USER-AUTHORIZED GATE — publish the GitOps pins (data-center-scripts)** | `cd $DCS` → branch `feature/llmproxy-pin-<sha7>` → edit **both** pin files (backup `infranstructure/k8s/llmproxy/hetzner-daoduc/values-pinned.yaml` + primary `infranstructure/k8s/llmproxy/rancher-local-ensem-dev/deployment.yaml`) → `--no-ff` into `latest` → push | **both endpoints move together**; `main` untouched. ⚠️ The pins may be staged **before** the deploy (working tree only) and pushed **after** the deploy succeeds — but the repo state must never name a tag that is not live. House ledger format: digest + CI run number + bundle summary + "**Migration 033 shipped (schema 032 → 033; additive table, no backfill)**" + a refreshed `# Rollback known-good for THIS endpoint:` line |
| T-3.9 | Worktree cleanup | `git worktree remove` the temp path; `git branch -d` the temp branch | per the house landing recipe |

---

### 5.4 T-4 — PRE-DEPLOY BASELINE + PRE-FLIGHT DUMP (read-only + one write, before any cluster mutation)

| # | Task | Command | Acceptance |
|---|------|---------|------------|
| T-4.1 | Capture the **per-line** baselines | for each line: deployment gen, pod name + `imageID` + restartCount, `helm history` (backup only), and `curl -s https://<host>/v1/models \| jq -r '.data[].id' \| sort > $D/models-<line>.pre.txt` | **two separate baselines** (the lines have separate DBs and already differ — never compare across lines) |
| T-4.2 | Choose `SMOKE_MODEL` + the live scenario's models | from the captured id-set — a known-cheap, always-on chat model; plus a **plain-credential** model for the regression check (L-5) | recorded by name; **never** a blind `.data[0]` pick |
| T-4.3 | **Pre-flight DB dump (R-3)** — this train SHIPS a migration | dump the `llmproxy` DB per line **before** the deploy (the house procedure lives in `data-center-scripts/.agents/shared/planning/llm-daoduc-to-ensem/phase1-extract-from-hetzner.md` — **reference by PATH ONLY; that file carries plaintext DB passwords and must never be pasted, echoed or committed**) | a dated dump artifact exists per line; the restore path is known. ⚠️ `psql`/`pg_dump` are on the host (`/usr/bin/psql`, `/usr/bin/pg_dump`) — the **pod image has neither** (verified), so the dump is taken from the host against the cluster's DB endpoint, not from inside the pod |
| T-4.4 | **Group-usage census (R-1) — the rollback-safety census** | after the feature's live scenario (T-6) the DB holds group refs; the census answers: "does any model's `credentials_json` contain a `group_id`?" | recorded. **If the answer is non-empty at rollback time, the rollback is NOT `helm rollback` alone** — see T-8.4/T-8.5 |

---

### 5.5 T-5 — CI WATCH (the build is published by the master push)

| # | Task | Command / observation | Acceptance |
|---|------|----------------------|------------|
| T-5.1 | **Watch the run in the FIRST minutes** | the Actions UI is **not observable from this VM** — so the observation channel is the **registry**, not the Actions tab: poll the registry for the new per-SHA tag / re-read the `master` tag's digest. A **zero-delta push emits NO event** → the tag simply never appears | the new tag appears within the **9–13 min** band. If nothing appears after ~15 min: the push was **zero-delta** → go to T-5.4. If a run exists and hangs >20 min: the buildx-hang class (GitHub-side) → T-5.4 |
| T-5.2 | Resolve the pin parameters | `TARGET_TAG=<sha7>`, `TARGET_DIGEST=` the registry index digest for that tag, `TARGET_RUN=` the CI run number | all three non-empty; `TARGET_TAG != latest`; `TARGET_TAG !=` the currently-running tag (else the deploy is a **no-op** — abort) |
| **T-5.3** | **⛔ USER-AUTHORIZED GATE — no further pushes without instruction** | — | any additional push (a null-commit retrigger, a pin amend) is a **new user-authorized gate** |
| T-5.4 | **Retrigger** (only if no event fired or the run hung) | one-line **null-content commit** on the feature branch → `--no-ff` `latest` → `--no-ff` `master` promotion (the PROVEN chain; precedent `wi-7769a812`: `429a6ef→d9cdbea→487be74`) | a NEW CI run starts; the content is identical, so the gate result is unchanged (but re-verify: T-2.10's artifact still applies because the tree is identical) |
| T-5.5 | Read the CI log honestly | the CI conclusion is **NOT** the gate (R-6): `docker-ci.yml:104` swallows Go-test failures | record explicitly: "CI green ≠ tests passed; the local gate (T-2) is the gate" |

---

### 5.6 T-6 — IMAGE VERIFY (registry-only; prove the running binary is THIS feature)

| # | Task | Command | Acceptance |
|---|------|---------|------------|
| **T-6.1** | **Marker + negative/positive controls** | `bash ~/llmproxy-verify/verify_image.sh --tag $TARGET_TAG --json` → then `strings -a ~/llmproxy-verify/cache/$TARGET_TAG/llm-supervisor-proxy \| grep -c 'CREATE TABLE IF NOT EXISTS credential_groups'` | the marker count is **> 0**. **Why this marker:** it is a **BE-only literal** that can only exist if migration 033's SQL is embedded in the binary — it proves the registry append + the SQL pair actually shipped. **Negative control (verified this session):** the currently-live `fd1649c` binary returns **0** for it, while returning **2** for the 005 precedent `CREATE TABLE IF NOT EXISTS credentials` — so the literal survives `-s -w` stripping and the marker **discriminates**. ⚠️ **Never** `strings \| grep -ci sticky` (Tailwind false hits) — use a distinctive literal + a positive control |
| T-6.2 | **Per-SHA tag verification** (cache-safe) | `bash ~/llmproxy-verify/verify_image.sh --tag $TARGET_TAG` | index digest == `TARGET_DIGEST`; amd64 **and** arm64 slices present (daoduc is ARM64 — a single-arch image fails THERE first); the app-layer digest verifies; the extracted binary's `sha256` and `go version -m` are recorded |
| T-6.3 | **Moving-tag cross-check (R-7)** | `bash ~/llmproxy-verify/verify_image.sh --tag latest --no-cache` and `--tag master --no-cache` | the `latest`/`master` index digests are **IDENTICAL** to the per-SHA tag's digest — the three tags must agree. `--no-cache` is mandatory for moving tags |
| T-6.4 | **Image sanity** | the harness's `env KEYS` (keys only, **never values**), entrypoint, exposed ports | unchanged vs the previous build (this feature adds no env var, no port) |
| T-6.5 | **Budget + quirk notes** | Docker Hub anonymous = 100 req/hr; each verify run = 5 requests; blob fetch 307→Cloudflare → `curl -L`; `go version -m \| head -1` **SIGPIPEs** under `pipefail` → use `awk` | the run completes inside the budget; the digest-of-record is captured |

---

### 5.7 T-7 — DEPLOY (BACKUP FIRST, then PRIMARY — the order is mandatory)

#### 7A — BACKUP: llm.daoduc.org (k3s / Hetzner) — **deploy FIRST**

| # | Task | Command | Acceptance |
|---|------|---------|------------|
| T-7.1 | **G0 — wrong-cluster guard (R-12)** | `: "${KC_DAODUC:?environment block not loaded}"; export KC="$KC_DAODUC"; GOT=$($KCTL --kubeconfig "$KC" config view --minify -o jsonpath='{.clusters[0].cluster.server}'); [ "$GOT" = "https://46.62.137.105:6443" ] \|\| { echo "WRONG CLUSTER: $GOT — STOP"; exit 1; }` | prints the daoduc server, else **hard-exit** |
| T-7.2 | **Render/diff gate** | `$HELM lint "$CHART" -f $DCS/infranstructure/k8s/llmproxy/hetzner-daoduc/values-pinned.yaml`; `$HELM template llm-supervisor-proxy "$CHART" -n llmproxy -f <same values> > /tmp/render.new.yaml`; `$HELM --kubeconfig "$KC" get manifest llm-supervisor-proxy -n llmproxy > /tmp/live.man.yaml`; `diff` | the rendered-vs-live diff is **the image tag line ONLY**. ⚠️ a **values-file-only** diff reports phantom drift (cpu request, limits, strategy, ingress come from chart templates/defaults) — only the RENDERED diff is authoritative |
| T-7.3 | **Stage the pin + deploy** | `$HELM --kubeconfig "$KC" upgrade llm-supervisor-proxy "$CHART" -n llmproxy -f $DCS/.../hetzner-daoduc/values-pinned.yaml --wait --timeout 10m --atomic --cleanup-on-fail` | helm revision **mints +1**; `--wait` satisfied (precedent ~28 s); ⚠️ use **absolute paths** for both `--kubeconfig` and `-f` (the recorded precedent mixes relative paths that cannot both resolve); ⚠️ do **NOT** use `--reuse-values --set image.tag=…` (it abandons the pinned file as the source of truth) |
| T-7.4 | **Verify the BACKUP line — full gate** | G1 rollout status (300 s) · G2 exactly **one** non-Terminating pod, Ready, **0 restarts** (deletionTimestamp-filtered; a Terminating twin is tolerated only if gone on a 30 s re-run) · G3 `/healthz` == `ok` via port-forward **14330:4321** (container port 4321 — `14330:8080` is REFUSED) · G4 `/fe/api/version` == `{"version":"<TARGET_RUN>"}` · G5 `/v1/models` 200 **and the id-set diff vs THIS line's own baseline is EMPTY** (the count is recorded, never gated) · G6 generation caught up (`gen == obs == baseline+1`, `upd == rep == 1`, `unavail == 0`) · G7 the **running pod's `imageID`** ends with `sha256:${TARGET_DIGEST#sha256:}` · G8 a real public chat completion (token read via `read -rs`, never echoed) · G9 **10-minute soak** (re-run G2 + G5) · G10 **no crash signature** (grep exits 1 = zero matches, with **log retrieval proven non-empty**; `panic:` = FAIL, `panic recovered` = WARN — the binary logs that by design) · **L-8 migration-033 live check** (the `credential_groups` table EXISTS in the line's DB) | **ALL of G1..G10 + L-8 green.** The G5 id-set comparison is against the line's OWN baseline (R-25) |
| T-7.5 | **🛑 THE GATE — BACKUP VERIFIED** | any single failure ⇒ **T-8 rollback on the BACKUP line only**, revert the backup pin, and **STOP — do not touch llm.ensem.dev** | binary gate; no primary mutation until it is green |

#### 7B — PRIMARY: llm.ensem.dev (RKE2 / rancher-local) — **deploy SECOND**

> ⚠️ **Execution-context requirement (R-4).** This line carries the ensemble's own LLM traffic. Execute from a **human terminal or pre-staged commands** — an agent-driven execution loses its reasoning lane mid-procedure and **must not be attempted** (precedent `risks.md` R-20).

| # | Task | Command | Acceptance |
|---|------|---------|------------|
| T-7.6 | **G0 — wrong-cluster guard** | `: "${KC_ENSEM:?environment block not loaded}"; export KC="$KC_ENSEM"; GOT=$($KCTL --kubeconfig "$KC" config view --minify -o jsonpath='{.clusters[0].cluster.server}'); [ "$GOT" = "https://192.168.1.250:6443" ] \|\| { echo "WRONG CLUSTER: $GOT — STOP"; exit 1; }` | prints the ensem server, else **hard-exit**. ⚠️ the context here is named **`default`** — **not** `rancher-local-ensem-dev` |
| T-7.7 | **Stage the pin** | edit `$DCS/infranstructure/k8s/llmproxy/rancher-local-ensem-dev/deployment.yaml` — the `image:` line + the ledger comment block (digest, CI run, bundle summary, **"Migration 033 shipped (schema 032 → 033; additive table, no backfill)"**, refreshed rollback anchor) | only the image line + comments change |
| T-7.8 | **Deploy (manifest-only)** | `$KCTL --kubeconfig "$KC" -n llmproxy diff -f "$DEPLOY" \|\| true` (judge the PRINTED diff — `kubectl diff` exits non-zero whenever a diff exists; the nginx webhook rejects duplicate paths) → `$KCTL --kubeconfig "$KC" -n llmproxy apply --dry-run=server -f "$DEPLOY"` → `$KCTL --kubeconfig "$KC" -n llmproxy apply -f "$DEPLOY"` → `rollout status --timeout=300s` | the diff is **the image line only**; the rollout completes (precedent ~22–35 s). ⚠️ do **NOT** run `install.sh` — it applies 9 objects (incl. Secrets) and demands a context named `rancher-local` |
| T-7.9 | **Verify the PRIMARY line** | re-run the **full G1..G10 + L-8** with the ensem substitutions (port-forward binds **14331:4321**; G5 diffs the ensem baseline; G6 baseline is the ensem gen) | all green. ⚠️ **G9 is not optional here** — additionally confirm the ensemble's own traffic is **completing** (watch the `[RACE]` completion lines). If the soak cannot be observed cleanly, that is a **FAILED gate**, not a pass-by-default |
| T-7.10 | **Pin the digests (GitOps)** | commit both pin files in `data-center-scripts` on `feature/llmproxy-pin-<sha7>` → `--no-ff` `latest` → **⛔ USER-AUTHORIZED GATE** push; `main` untouched | both endpoints pinned to the SAME tag; the ledger comment records digest + run + migration note + rollback anchor |

---

### 5.8 T-8 — ROLLBACK (documented BEFORE the deploy; executed only on a gate failure)

| # | Task | Command | Acceptance |
|---|------|---------|------------|
| T-8.1 | **Re-derive the anchors** | backup: `$HELM --kubeconfig "$KC_DAODUC" history llm-supervisor-proxy -n llmproxy`; primary: `$KCTL --kubeconfig "$KC_ENSEM" -n llmproxy rollout history deployment/llm-supervisor-proxy` | tier-1 = the **immediately-prior verified revision**; tier-2 = the one before it. ⚠️ a rollback **mints** a new revision → literal numbers go stale; re-derive after ANY rollback (R-16, precedent `risks.md` R-18) |
| T-8.2 | **Backup-line rollback (tiered)** | `$HELM --kubeconfig "$KC" rollback llm-supervisor-proxy <tier-1-rev> -n llmproxy --wait --timeout 180s` (tier-2 only if tier-1 is itself implicated) | ~30 s recovery; surge-only ⇒ no capacity gap. ⚠️ **deliberately NO `kubectl set image` fallback on the backup line** — an out-of-band patch desyncs helm state |
| T-8.3 | **Primary-line rollback (tiered)** | `$KCTL --kubeconfig "$KC" -n llmproxy set image deployment/llm-supervisor-proxy llm-supervisor-proxy=docker.io/disillusioners/llm-supervisor-proxy@sha256:<tier-digest>` (digest form preferred — it removes tag mutability) | ~22–35 s recovery |
| **T-8.4** | **🔴 GROUP-REF DATA REPAIR (R-1) — the step that distinguishes this train** | After rolling the binary back, **any model whose `credentials_json` contains a `group_id` is BROKEN** in the old binary (proved in §3 R-1: the old struct drops the unknown field → `credential_id: ""` → the write-path validator rejects the model and the resolver returns 502). Repair = **inline the group's members as direct credential refs** on every affected model, via the same API the feature ships (`PUT /fe/api/models` with the group's members expanded, group ref removed) — **or**, if the old binary cannot accept the PUT, via a scoped `UPDATE models SET credentials_json = …` on the dumped DB | every group-referencing model serves again under the old binary. ⚠️ **This repair is REQUIRED whenever the group-usage census (T-4.4) is non-empty.** It is **reversible** (the group's `members_json` is untouched) and **idempotent** |
| **T-8.5** | **REHEARSE the repair on the BACKUP line BEFORE the primary deploy** | during the backup line's soak window, on the backup line only: create a throwaway group + a throwaway model referencing it → roll the backup binary back → observe the model 502 (the R-1 signature) → run the T-8.4 repair → observe the model serving again → roll FORWARD again | the repair is **proven executable** BEFORE the primary carries the ensemble's traffic. This converts R-1 from "a blocker" to "a documented, rehearsed procedure" |
| T-8.6 | **Revert the pins (mandatory after ANY rollback)** | both pin files → the tier's tag + a corrected ledger comment | the repo must never name a tag that is not live, or the next apply re-ships the bad build |
| T-8.7 | **Verify the rollback** | re-run G1..G8 with the **tier's** `EXPECTED_DIGEST` / `EXPECTED_RUN` — **including G8** (the real chat smoke): an unverified rollback is not a rollback. **G4 is decisive** (the running build number must match the tier) | all green on the rolled-back build |

---

### 5.9 T-9 — LIVE VERIFICATION (the commission-mandated scenario, per line)

> **Evidence channel (R-9).** The rotation proof is read from the **proxy stdout**, not from the UI:
> - `[credentiallb] model=<model> conv=<hash>→cred=<credentialID> newlyBound=true` — `pkg/credentiallb/engine.go:346` (emitted on a **new** binding, i.e. a fresh conversation key);
> - `[DEBUG] Race attempt <n> calling internal provider: <provider> (model=…, baseURL=…, credential_id=<credentialID>)` — `pkg/proxy/race_executor.go:576` (emitted on **every** attempt — the denser channel).
>
> Both are verified present in the live daoduc stdout (a real captured line: `[credentiallb] model=coding4 conv=3fab5b7b…→cred=minimax-company newlyBound=true`).
> ⚠️ **The request log carries NO credential attribution** (`pkg/store/memory.go` `RequestLog` has no credential field — verified) and **the usage counters are per-model/per-token, not per-credential** (`pkg/usage/counter.go:194/265` — `IncrementModelUsage(modelID, …)`) ⇒ **neither can prove rotation.** The stdout lines are the channel. `model_credential_selected` (the `/fe/api/events` SSE payload) also carries `credential_id` (`pkg/proxy/race_coordinator.go:1217`) but **fires only on a newly-bound pick** — it is a corroborating channel, not the primary one.

| # | Task | Action | Acceptance |
|---|------|--------|------------|
| **T-9.1 → L-1** | **Create a group from 2 REAL same-provider credentials** | `POST /fe/api/credential-groups` on the pod-direct port-forward (pod-direct `/fe/api/*` bypasses OAuth). Use two **real** same-provider credentials. **Observed live on daoduc (read-only census this session):** provider `minimax` has 2 → `minimax-company`, `minimax-mindspath`; provider `grok` has 2 → `grok-main`, `ha-grok`; provider `openai` has 5 → `external`, `ha-neuralwatt`, `neuralwatt-kha`, `openai-kha`, `tuyen`. **The `minimax` pair is the natural choice** (same provider, both real, and the models `coding` / `coding4` already rotate between exactly those two) | 201; the group detail returns the member IDs + `used_by` |
| **T-9.2 → L-2** | **Configure a MIXED-ref model** | `PUT /fe/api/models` — the model's `credentials` array = ≥1 **direct** ref + the **group** ref. ⚠️ the group ref and a direct ref are **mutually exclusive** (`phase1-plan.md` §Locked: exactly one of `credential_id` / `group_id` per ref) | 200; the persisted array shows both kinds |
| **T-9.3** | **Capture the credential census (R-11)** | `GET /fe/api/credentials` — take **IDs and providers only**; the API already masks the keys (`server.go:2454`) — never print a key | the artifact contains no key material |
| **T-9.4 → L-3** | **Drive REAL traffic, defeating the sticky pin (R-9)** | send **multiple requests with DISTINCT leading user messages** (the conversation key hashes the leading user run → distinct keys → distinct bindings → the weighted selector re-picks) or **distinct tokens** (the key is token-salted). A live liveness credential exists on daoduc (token id `c9c2c123-…`, name `smoke-15m`, allowed models `coding-group` / `quick-group`) — use it, or another authorised token; read it via `read -rs`, never echo | ≥2 distinct `credential_id` values observed |
| **T-9.5** | **Collect the rotation evidence** | capture the stdout window covering the drives (PRIMARY rotates under load — the stdout ring is ~7 min; establish the window FIRST; `--previous` only reaches a dead container) | a window containing **both** a direct-credential line and a group-member line |
| **T-9.6** | **Binary verdict** | **BOTH** source classes must appear. One class only = **the proof FAILED** (not a weak pass) → re-run with more turns / a different key strategy / a member-removal variant (T-9.8/L-7) | **PASS** = both `minimax-company` (or the chosen direct) **and** a group member appear in the window |
| **T-9.7 → L-4/L-5** | **Regressions** | `GET /v1/models` (public) → the id-set diff vs the **pre-deploy baseline of this same line** is **EMPTY** (no credential-group artifacts leak into the catalog); `/healthz` == `ok` (in-cluster port-forward); a **plain-credential** model (no group ref) serves 200 and resolves to the **same credential as before the deploy** | all three green |
| **T-9.8 → L-6/L-7** | **Management flows** (cheap, optional but recommended) | `DELETE /fe/api/credential-groups/{id}` while referenced → **409** with a bare `{"error":…}` naming the referencing models; then remove a member and confirm **every** referencing model's effective flat list changes (the removed member stops appearing) | both green |
| **T-9.9** | **Cleanup the scenario** (the R-1 discipline) | inline the group's members into the affected model's refs and delete the group — leaving a live model pointing at a group makes the **binary rollback** unsafe (R-1). ⚠️ **the group-usage census (T-4.4) MUST be re-run after cleanup** and MUST be **empty** | the census is empty; the model serves normally again |
| T-9.10 | **Record the live evidence** | the evidence block names: the group id, the member ids, the model id, the observed credential ids, the stdout window (timestamps + the exact lines), and the **id-set diff result** | the artifact contains **no secret values** (R-11) |

> **T-9.9 is not cosmetic.** The live scenario deliberately creates the R-1 condition. Cleaning it up is what keeps the rollback path (T-8.4) a contingency rather than a certainty.

---

## 6. Coupling

- **Tight with `phase1-plan.md`** — this phase CONSUMES phase 1's deliverables: the migration-033 file pair + registry append (T-3.6), the `CredentialRef.GroupID` representation (**T-8.4's repair semantics depend on it** — the group ref is persisted with an EMPTY `credential_id`), the 8 decorator forwards (T-18), the AST pin (T-19 + T-2.7), the API surface (L-1/L-2/L-6), and the test matrix (T-1..T-23). **A change to phase 1's locked representation invalidates T-8.4's repair procedure** — re-derive it.
- **Tight with `phase2-plan.md`** — the FE matrix (F-1..F-8) is proven by T-2.4/T-2.5; the FE **label** ("Credential Groups") is asserted by the naming scan (T-1.5); the FE's delete-while-referenced flow (F-4) mirrors the live check L-6. **Cross-reference by filename only.**
- **Tight with the store layer** — the flatten seam lives in `pkg/store/database/store.go`; the "flatten at resolve, raw at read" split (§Locked in phase 1) is what keeps the FE round-trip intact; the **live scenario (T-9.1) exercises that split against the real FE API**.
- **Loose with `pkg/credentiallb` / `pkg/stickyorder` / `pkg/skipcache` / `pkg/grouplb`** — **no code change**; the coupling is asserted **negatively** (the AST pin, T-19). At runtime the coupling is *semantic*: the group must be invisible to selection, which is exactly what L-3 proves negatively (the selected credential IDs are flat member IDs — never a group ID).
- **Independent of the JSON-file twin** — explicitly out of scope (reject-only); the live DB path is the only one exercised.
- **Coupled to the deployment substrate** — T-3 (git topology), T-5 (CI), T-6 (registry), T-7 (clusters), T-8 (rollback) form a single ordered chain: **no step may start before its predecessor is green**, and every step that mutates shared state (a push, a cluster, a DB) is a `⛔ USER-AUTHORIZED GATE`.

### 6.1 Coupling matrix

| | T-1 | T-2 | T-3 | T-4 | T-5 | T-6 | T-7 | T-8 | T-9 |
|---|---|---|---|---|---|---|---|---|---|
| **T-1** | — | tight (tip) | tight (tip) | loose | independent | independent | independent | loose | independent |
| **T-2** | tight | — | tight (gate → land) | independent | independent | independent | tight (gate → deploy) | independent | independent |
| **T-3** | tight | tight | — | loose | tight (push → CI) | independent | independent | loose | independent |
| **T-4** | independent | independent | independent | — | independent | independent | tight (baseline) | tight (anchors) | tight (baseline) |
| **T-5** | independent | independent | tight | independent | — | tight (tag → verify) | independent | independent | independent |
| **T-6** | independent | independent | independent | independent | tight | — | tight (digest → deploy) | loose | independent |
| **T-7** | independent | tight | independent | tight | independent | tight | — | tight (rollback) | tight (live) |
| **T-8** | loose | independent | loose | tight | independent | loose | tight | — | tight (R-1 rehearsal) |
| **T-9** | independent | independent | independent | tight | independent | independent | tight | tight (R-1) | — |

---

## 7. Success criteria

| # | Criterion | How to measure | Threshold |
|---|-----------|----------------|-----------|
| 1 | **Local release gate green** | T-2.1 + T-2.2 + T-2.3 + T-2.4 + T-2.5 + T-2.6 + T-2.7 + T-2.8 + T-2.9 | **all nine green**; Pack A EXIT=0; Pack B EXIT=0 except the single quarantined httpbin pair; `make test` green except the same pair; tsc **19 errors / 9 files** exact parity; vitest 0 failed; static area byte-identical; vet+build EXIT=0 empty |
| 2 | **Anti-vacuous** | for every added `func Test` (backend + FE) | a **literal** `--- PASS` **and** `=== RUN` line in the gate logs; count = the number of added tests; **zero** added tests missing from the logs |
| 3 | **Pin non-vacuity** | mutate the AST pin in a scratch copy | the forbidden-import/construct mutation is **caught** (FAIL) and the comment-only probe stays **green** |
| 4 | **gofmt zero-new-drift** | flagged-LINE-SET (HEAD − base) per touched file | **∅** for every touched `.go` incl. all `_test.go` |
| 5 | **Chart untouched** | `git diff <base>..HEAD -- k8s/` | **empty**; the `k8s/` tree hash still `aa0f45355ab7f8fdb0128f966dfa2032fd83118c` |
| 6 | **Migration scoped correctly** | the diff over `migrations/` + `migrate.go` | contains **exactly** the `033` pair (both dialects) + the registry append — and nothing else |
| 7 | **Migration LIVE on both hosts** | a schema query per line | the `credential_groups` table **exists** on both lines after deploy |
| 8 | **Landed on both lines** | `git ls-remote origin refs/heads/latest refs/heads/master` | `origin/master` contains the feature tip; `tree(master) == tree(latest)`; the master promotion has parent1 = prior master tip, parent2 = latest tip |
| 9 | **CI published** | the registry shows the per-SHA tag + `latest` + `master` | all three resolve; the run number is within the 9–13 min band (or the null-commit retrigger produced a second run) |
| 10 | **Image marker verified** | `strings` on the extracted binary | `CREATE TABLE IF NOT EXISTS credential_groups` count **> 0**; the negative control on the previous build is **0**; the positive control **> 0** |
| 11 | **Digest identity** | the registry digests + the running pods' `imageID` | the per-SHA, `latest` and `master` index digests are **identical**; the **running pod** on each line reports that digest |
| 12 | **Deploy order honored** | the ship record | backup deployed and gated **before** the primary was touched; the primary step executed from a human terminal |
| 13 | **Live rotation proven** | the stdout window during the scenario | **BOTH** a direct credential AND a group-member credential appear in the `credential_id` / `→cred=` evidence; a group **ID** never appears as a selected credential (group-blindness, live) |
| 14 | **Catalog clean** | `/v1/models` id-set vs this line's own baseline | **EMPTY diff** — no credential-group artifact in the model catalog |
| 15 | **Health + plain-model regression** | `/healthz` + a group-free model's request | `ok`; the plain model serves 200 and resolves to the **same credential** as the pre-deploy baseline |
| 16 | **Management flows** | the delete-while-referenced + member-removal checks | 409 with a message naming the referencing models; the removed member vanishes from every referencing model's flat list |
| 17 | **Rollback documented AND rehearsed** | T-8.1..T-8.7 + the backup-line rehearsal (T-8.5) | tier-1/tier-2 anchors recorded per line with digests + run numbers; **the group-ref repair (T-8.4) is proven executable on the backup line before the primary deploy**; the pin-revert path is written down |
| 18 | **Rollback unused** (the expected outcome) | the ship record | **no rollback was executed** — the deploy passed every gate on both lines. The rollback path is documented and rehearsed but not needed |
| 19 | **Pins moved together** | the `data-center-scripts` commit | both pin files name the SAME tag; the ledger comment carries digest + CI run + the migration-033 note + the refreshed rollback anchor; `main` untouched |
| 20 | **Secrets discipline** | a scan of every produced artifact | **zero** credential values, API keys or proxy tokens in any log, report, commit or plan artifact. Credential **IDs and providers only**; masked values only (as `/fe/api/credentials` already returns them) |
| 21 | **Push discipline** | the ship record | every push has an explicit **user-authorized** marker; the plan itself performed **zero** pushes and **zero** commits |
| 22 | **Artifact completeness** | the ship record | the local gate report (`.agents/tester/RESULTS/<date>-credential-groups-release-gate.md`), the live evidence block, the pin commits, and the rollout record all exist and are cross-referenced |

---

## 8. Assumptions & open items

**Assumptions (explicit, not buried):**

| # | Assumption | If false |
|---|------------|----------|
| A-1 | Phase 1 lands the migration-033 file pair **and** the `migrate.go` registry append, with the group ref persisted as `{"credential_id":"", "group_id":"…"}` (the locked representation) | **T-8.4's repair procedure is invalid** — re-derive it against the actual persisted shape before the deploy |
| A-2 | `phase2-plan.md` ships the "Credential Groups" tab and the model-form picker, and the FE label carries the full qualifier | T-1.5's naming scan fails → the label must be fixed in phase 2 before the gate |
| A-3 | The live hosts run **PostgreSQL** (both lines set `DATABASE_URL` from the `llm-proxy-db-secret` secret) — verified on daoduc (secret `llm-proxy-db-secret`, key `url`) | if a line runs SQLite (`~/.config/llm-supervisor-proxy/config.db`), the migration still applies (both dialect trees exist) but T-4.3's dump procedure changes to a file copy and the plaintext-at-rest posture note applies |
| A-4 | The registry image is reachable anonymously from this host (`~/llmproxy-verify/verify_image.sh` — verified working, 11 cached tags present) | T-6 needs a token-based variant; the marker logic is unchanged |
| A-5 | The `minimax` credential pair (`minimax-company`, `minimax-mindspath`) remains the natural live-scenario pair on daoduc | re-run the read-only credential census (T-9.3) at execution time and pick another same-provider pair — the scenario's shape is unchanged |
| A-6 | `origin/latest` does not move past `2b987bf` between the gate and the land | T-3.3 merges it and **re-runs the full gate** on the merged tree |
| A-7 | The proxy's own Bearer token store authorizes `/v1/chat/completions` on both lines (the split-ingress topology) | the live scenario's request path is replaced by the pod-direct port-forward for the request leg too |

**Open items for the planner/leader (not blocking the plan's structure):**

1. **The live scenario's blast radius on a production line.** Creating a group + re-pointing a real model on the **primary** changes production routing for that model. The plan proposes running the full scenario on the **backup first** (which is where the rotation proof is cheapest — daoduc is the standby, so a mistake there is contained), then a **reduced** confirmation on the primary. **Decision needed:** is the primary-side proof (L-3, full mixed-ref rotation) required, or is the backup-side proof + the primary's catalog/health/regression checks (L-4/L-5) sufficient? *Recommendation: full proof on backup; on primary, a mixed-ref model with the group pointed at a **non-ensemble** model (not `coding`/`coding-group`), so the ensemble's own traffic is untouched.* **If the answer is "no production model may be touched on the primary", the primary's L-3 leg is dropped and the rotation proof is backup-only — state that in the ship record.**
2. **Which credential pair to mutate on the primary.** Same question, narrower: the candidate must be a **same-provider** pair not currently serving ensemble traffic. The census (T-9.3) at execution time decides; the planner should pre-authorize the fallback pair list rather than deciding mid-deploy.
3. **The rollback rehearsal (T-8.5) requires a backup-line rollback.** That is a *second* rollout of the previous image on the backup line — visible churn on a standby. **Decision needed:** is the rehearsal authorized as part of this commission, or deferred to a separate drill? *Recommendation: rehearse — it is the only way R-1's repair is proven rather than asserted; the backup line is idle and the rollback is ~30 s.* **If declined, R-1's repair ships unproven and the ship record must say so explicitly.**
4. **The DB dump (T-4.3) touches a credential-bearing reference file.** The procedure is path-only (`data-center-scripts/.agents/shared/planning/llm-daoduc-to-ensem/phase1-extract-from-hetzner.md` carries plaintext DB passwords). Confirm the operator who executes it is comfortable with that file, and that the dump artifact lands **outside** any tracked repo.

---

## 9. Exit Criterion

Phase 3 is complete when **all** of the following hold, in order:

1. **The local gate is green and recorded** — T-2.1..T-2.9 all green; the anti-vacuous table shows a literal `--- PASS` + `=== RUN` for **every** added backend and FE test; the gofmt drift set is empty; the AST pin's mutation probe is proven non-vacuous; the FE static area is byte-identical; `go vet`/`go build` are EXIT=0 empty.
2. **The chart is provably untouched** — `git diff <base>..HEAD -- k8s/` empty; the `k8s/` tree hash unchanged.
3. **The migration is scoped and live** — the diff over `migrations/` + `migrate.go` contains exactly the `033` pair + the registry append; the `credential_groups` table **exists** in the live DB on **both** lines after deploy.
4. **The feature is landed on `latest` and `master`** — `origin/master` contains the feature tip; `tree(master) == tree(latest)`; the promotion merge has the exact expected parents; every push carried an explicit user-authorized marker.
5. **CI published and the image is verified** — the per-SHA tag, `latest` and `master` all resolve; the three index digests are **identical**; the marker `CREATE TABLE IF NOT EXISTS credential_groups` is present in the extracted binary (with its negative control at 0 and its positive control > 0); the amd64 **and** arm64 slices both exist.
6. **The deploy is backup-first and gated** — the backup line passed G1..G10 + L-8 before the primary was touched; the primary then passed the same gates; the primary's rollout was executed from a human terminal / pre-staged commands.
7. **The live scenario passed** — a real group was created from 2 real same-provider credentials; a mixed-ref model served traffic from **both** a direct credential and a group member (the stdout evidence shows both, and **no group ID ever appears as a selected credential**); `/v1/models` shows **no** credential-group artifact; `/healthz` is `ok`; a plain-credential model resolves exactly as before.
8. **The management flows passed** — delete-while-referenced → **409** naming the referencing models; member removal updates every referencing model's flat list.
9. **The rollback path is documented, rehearsed and unused** — tier-1/tier-2 anchors recorded per line with digests + run numbers; the **group-ref data repair (T-8.4) is proven executable on the backup line**; the pin-revert path is written; **no rollback was executed** on either line.
10. **The pins moved together** — both `data-center-scripts` pin files name the shipped tag, with the ledger comment (digest, CI run, **migration 032 → 033** note, refreshed rollback anchor); `main` untouched.
11. **The group-usage census is empty** — after the scenario cleanup, **no** model references a credential group, so the binary rollback is a clean single step.
12. **The artifacts exist and are secret-free** — the release-gate report, the live evidence block, the pin commits and the rollout record are all present, cross-referenced, and contain **no** credential value, API key or proxy token.

**Handoff:** a ship note in the project's operating knowledge recording the shipped tag, both digests, the migration-033 fact (schema 032 → 033, additive table, no backfill), the group-ref rollback hazard and its repair procedure, and the live proof's evidence channel — so the next commission inherits the hazard instead of rediscovering it.
