# Credential-Groups Release Gate — 2026-11-17

**VERDICT: GATE-PASS**

- **Gated tip:** `ad6996aceb5e2c5855dfb00252fa6b64db14e952` (branch `feature/credential-groups`, verified `git rev-parse HEAD`)
- **Base:** `2b987bf94e39abfc9df515cbbadc2bb335dbb47c` (= origin/latest at claim)
- **Plan region gated:** phase3-plan T-1 / T-2 (T-2.1–T-2.9) + read-only T-3.6 scope proof. T-3 landing is NOT the tester's (giter lands; tester gates).
- **Scope decision:** no reduction — release gate, full suite warranted.
- **Method:** 13 parallel/sequenced worker legs, one skill each; every Go command HOME-isolated (`HOME=$(mktemp -d)`, `unset DATABASE_URL APPLY_ENV_OVERRIDES PORT POSTGRES_* DAEMON_PORT`), real-home `config.json` stat+md5 proven unchanged on every leg that ran Go. All mutation probes confined to `.git`-free `git archive HEAD | tar -x` scratch copies. No push, no ref moves, main checkout untouched.
- **Toolchain:** go1.26.0 linux/amd64 via `/home/nea/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.linux-amd64/bin`, `GOTOOLCHAIN=local`, `GOPATH=/home/nea/go`, `GOMODCACHE=/home/nea/go/pkg/mod`, `GOCACHE=/home/nea/.cache/go-build`.

---

## Evidence table

| # | Gate item | Command (authoritative form) | Exit / wall | Counts | Verdict |
|---|---|---|---|---|---|
| 1 | PACK A | `timeout 300 $GO test -count=1 -v -timeout=240s ./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...` | 0 / 135.5 s | 2321 `--- PASS` · **0 `--- FAIL`** · 2334 `=== RUN` · 13 `--- SKIP` | **PASS** (9/9 pkgs ok; arithmetic closes exactly: 2321+13=2334=RUN) |
| 2 | PACK B | `timeout 300 $GO test -count=1 -v -timeout=240s $($GO list ./... \| grep -vE 'github.com/disillusioners/llm-supervisor-proxy/(cmd\|pkg/(proxy\|store\|decision\ui))(/\|$)')` | 1 / ~60 s | 1116 `--- PASS` · 2 `--- FAIL` (both EXPECTED-QUARANTINED) · 2242 `=== RUN` · 1 SKIP | **PASS** — 33 pkgs selected (matches plan), `ANCHOR_OK` |
| 3 | pkg/modelscache direct | `timeout 300 $GO test -count=1 -v -timeout=240s ./pkg/modelscache/...` | 0 / `ok 7.600s` | 107 `--- PASS` = 107 `=== RUN` (89 top-level + 18 subtests), 0 FAIL / 0 SKIP, `-list` 89 names all RUN+PASS | **PASS** |
| 4 | FE vitest | `cd pkg/ui/frontend && timeout 300 npx vitest run` | 0 / 12.36 s | **Test Files 33 passed (33) · Tests 357 passed (357)** | **PASS** — vacuity reconciliation clean (33 src files ↔ 33 executed, set-diff empty both ways) |
| 5 | tsc parity | `cd pkg/ui/frontend && timeout 240 npx tsc --noEmit` | **2** (required shape) | 18 errors / 9 files — per-file counts MATCH ×9, file-set diff EMPTY, `TOTAL_ERRORS=18`/`TOTAL_FILES=9` MATCH | **EXACT-PARITY** |
| 6 | Static zero-mod | `find pkg/ui/static -type f \| sort \| xargs -r sha256sum` before/after `npm run build` | build `✓ built in 4.75s` | 4/4 files hashed incl. `index.html`; before/after **byte-identical**; post-build `git status --porcelain -- pkg/ui/static/` EMPTY | **ZERO-MOD-PROVEN** — forbidden glob executed as evidence: **3 files vs find's 4** |
| 7 | gofmt zero-new-drift | per touched file: `gofmt -d` HEAD-set − `git show 2b987bf:<f> \| gofmt -d` base-set | — | 32 touched `.go` (18 `_test.go`, 13 new) · **TOTAL NEW = 0** · `gofmt -l` over all 18 `_test.go` = empty | **PASS** — non-vacuity proven BOTH directions (detection: NEW set [37..43]; subtraction: inherited drift → NEW ∅) |
| 8a | AST structural pin | `TestCredentialGroups_Structural_NoCredentialGroupTypesInSelectionPkgs` (`pkg/proxy/credential_groups_ast_pin_test.go:143`) | baseline PASS 0.08 s | forbidden-path probe → **RED** naming `pkg/credentiallb/zz_probe_forbidden.go:5` (composite+selector+ident); comment-only probe → **GREEN** | **NON-VACUOUS** — multi-value `:=` exemption DOCUMENTED (`:407-413`, `:455-458`, phase1-plan:559), hole open-but-accepted |
| 8b | Rebuild no-block | `TestCredentialGroup_Rebuild_DoesNotBlockRequestPath` (`credential_group_reconcile_concurrency_test.go:29`) | baseline PASS 0.28 s | pre-C1 mutation (write-lock across DB read) → **RED @ :83** "m.mu.RLock() did NOT complete within 500ms…" | **NON-VACUOUS** — failure cause = CONTENTION (RLock acquisition bound), not setup |
| 8c | Provider-edit DirectSQL | `TestCredentialGroup_Rebuild_ProviderEdit_DirectSQL` (`:256`) | baseline PASS 0.31 s | group-rows-only comparison mutation → **RED @ :295** "the member-provider half is missing" | **NON-VACUOUS** |
| 8d | ModelForm.mixed submit | `ModelForm.mixed.test.tsx` (12 tests) | baseline 12/12 | testid lookup + `not.toBeNull()` + UNCONDITIONAL click + `onStatus` `stringContaining('<fragment>')` + `onSave` not called; validation-branch mutation → **RED** (onStatus got success-path null) | **NON-VACUOUS** — anti-pattern ABSENT (file documents the killed round-1 vacuity at `:337-341`) |
| 8e | SettingsPage.tabs mount | `SettingsPage.tabs.test.tsx` (17 tests) | baseline 17/17 | `:345` pre-click `toBeNull()` + `:352` post-click `not.toBeNull()`; never-mount → RED (presence); always-mount → RED in 25 ms **before any click** (absence) | **NON-VACUOUS** — both halves have teeth |
| 8f | FE 409 delete-block | `CredentialGroupsTab.test.tsx` B6 (`:206-240`) | baseline 10/10 | named constant `CREDENTIAL_GROUP_IN_USE_MESSAGE` (`useApi.ts:1056`) + `not.toContain('is referenced by 1 models')`; leak-append probe → **RED exactly on the absence assertion @ :232** | **NON-VACUOUS** |
| 9 | O-2 plain-model pin | `TestCredentialGroup_PersistedShape_PlainModel` / `_GroupRef` (`credential_group_persisted_shape_test.go:14/:44`) | 0 (isolated) + `ok pkg/store/database 46.231s` | **byte-for-byte** `raw != string(json.Marshal(...))` both cases + substring guards; locked shape `{"credential_id":"","group_id":"…"}` confirmed | **PASS — STRONG form** |
| 10 | BE↔FE drift | read-only 8-point audit, commit-by-commit `af0db05..ad6996ac` | — | 7/8 CONFIRMED with file:line both sides; 1 literal-wording finding (below) | **CONTRACT-INTACT** |
| 11 | Whole-repo build/vet | `go build ./...` · `go build -o /tmp/cmd-check ./cmd` · `go vet ./...` · `bash test/packs/build_gate_test.sh` | **0 / 0 / 0 / 0**; all outputs EMPTY | binary 26,554,063 B; `go build ./cmd` (no -o) → exit 1 `build output "cmd" already exists and is a directory` = **KNOWN DESIGN TRAP, not a defect** | **BUILD-VET-GREEN** |
| 12 | Pack hygiene | PACKS.md + QUARANTINE.md updated; artifacts committed (house convention) | — | this file + static-zero-mod artifact | **DONE** (this commit) |
| T-2.3 | make test ×2 | `timeout 300 make test` then `GOFLAGS=-count=1 timeout 300 make test` (toolchain prepended to PATH) | **2 / 137 s** · **2 / 136 s** | 38 ok + 3 no-test-files + 1 FAIL (`pkg/mcp`) both runs; the ONLY `--- FAIL:`s = the quarantined pair (`handlers_api_test.go:1864`/`:1904`); import guards **3/3 PASS** | **PASS** (expected exit-2 quarantine shape; plan-mandated, beyond the enumerated 12) |

### T-1 boundary proofs (read-only)

| Check | Result |
|---|---|
| T-1.1 tree cleanliness | Only the 2 expected untracked entries (approver tracking file + this gate's static artifact) |
| T-1.2 change inventory | 72 files, +14,316/−182, all in-feature, zero stray artifacts |
| T-1.3 **chart untouched (O-2)** | `git diff 2b987bf..HEAD -- k8s/` = **0 bytes**; tree `aa0f45355ab7f8fdb0128f966dfa2032fd83118c` identical across expected / `HEAD:k8s` / `origin/master:k8s` / main-checkout / base — **5-way proven** |
| T-1.4 secrets discipline | 2 regex hits = benign `"username:password":"API key"` MCP label in minified bundles; **zero credential values** in diff/tests/fixtures/bundle |
| T-1.5 naming invariants | **FINDING — see below** (10 bare-`group` user-facing strings) |
| T-3.6 migration scope | **exactly** `033_add_credential_groups.{up,down}.sql` × 2 dialect trees (4 files) + the `migrate.go` registry append (its entire diff) — nothing else |
| Commit ancestry | 22/22 implementation SHAs (11 BE + 11 FE) present **and ancestors of HEAD**; both lanes FINAL |

---

## Known flakes — adjudication record

| Test | Fired? | Adjudication |
|---|---|---|
| `TestStoreEngine_PeakHourParity` (store_test.go) | **No** — passed outright in Pack A | Current wall clock ~05:17 UTC, outside the 23:59:00–23:59:59.999 UTC trigger window. No adjudication needed. |
| `TestEngine_GetOrSelect_TOCTOU_CoolingReverify` (credentiallb/cooldown_test.go:481/:517) | **No** — passed outright in Pack B (`pkg/credentiallb ok 3.832s`) | Load-context flake did not fire. No adjudication needed. |
| `pkg/mcp` httpbin redirect pair (`handlers_api_test.go:1864`/`:1904`) | **Yes — 2×, in Pack B and BOTH make-test runs** | DETERMINISTIC third-party (httpbin.org server-side 403 before the classifier); QUARANTINE.md row reconfirmed 4th consecutive gate. Identical signature all 3 observations this gate. |
| `TestStoreEngine_CloseLifecycle` | **No** — passed in Pack A | No adjudication needed. |

**Zero unquarantined failures across all 13 legs.** No flake re-runs were needed except none — no non-quarantined failure occurred anywhere.

---

## Findings (non-blocking, adjudicated)

### F-1 🟠 Naming invariant deviation — 10 bare-`group` user-facing strings (T-1.5)
Plan T-1.5 acceptance: every user-facing string carries the full "credential group" qualifier. Extracted from 2,765 added lines:
- **Strongest (wire-facing 400 envelopes, internally inconsistent):** `store.go:2151`/`:2192` "group refs are not allowed for kind %q"; `models/credential_group.go:138` "group refs not allowed for kind %q"; `:142` "group %q not found" — while the parallel `server.go:801` message IS qualified ("references an unknown credential group").
- **FE form-scoped (waivable):** `CredentialGroupForm.tsx:58/:62/:109`, `ModelForm.tsx:363/:630`, `MultiCredentialEditor.tsx:171/:285`.
**Ruling:** strings-only, zero behavioral defect, BE/FE mutually consistent; not in the phase-3 §7 success criteria nor §9 exit criterion nor the 12 enumerated gate items → does NOT flip the verdict. **Action for leader: strings-only follow-up commit to qualify F8–F10 (ideally F4: the `🗂️ Group:` badge sits in the chat-model form where bare "Group" collides with model-LB vocabulary), or explicit waiver.**

### F-2 🟢 DTO wire shape vs gate-item wording (drift-check point 1)
Wire DTO carries **5** keys (`id,name,provider,members,used_by`); `created_at`/`updated_at` exist on the models-layer struct but are dropped by `newCredentialGroupDTO` (`server.go:2946-2962`). **Pre-existing at af0db05** (no commit in this branch touched it), **plan-conformant** (`phase1-plan.md:394` prescribes the 5-key wire DTO), FE declares them optional-and-never-rendered. Not a defect; wording of the gate item mirrored the models-layer struct. **Action for leader: re-spec the gate item to 5 wire keys, or raise a spec-change if wire timestamps are wanted.**

### F-3 🟢 Premise correction — C1 edits are NOT both in pkg/modelscache
`git show 9f475ae -- pkg/modelscache/` = **EMPTY**. The C1 commit's production edit (+439/−72) lives in `pkg/store/database/credential_group.go`; only `ad6996ac` touched `pkg/modelscache/models.go` (12 lines: boot-rebuild error logging `:163`; empty-ID guards `:749`/`:799`). Every changed pkg/modelscache symbol is nonetheless exercised by a literal-PASS test (C1 coverage table in the modelscache leg report).

### F-4 🟢 ensure.md improvement notice (user-owned file — NOT modified)
`rules/ensure.md:7` requires "zero NEW error **locations**"; the machine-readable baseline records counts only, and the verifier compares counts + file-set. Validated the designated way, then strengthened into a de-facto location proof (freeze-commit replay `git archive 3f94a69`: (file,TScode,message) multiset IDENTICAL, 17/18 errors on the exact same line). Suggested rewrite: "`tsc --noEmit` reports zero NEW type errors vs the documented baseline: per-file error counts, the error file-set, and the `TOTAL_ERRORS`/`TOTAL_FILES` footer must all match `pkg/ui/frontend/tsc-baseline.txt`."

### F-5 🟢 Environment notes (not defects)
- Pack scripts + Makefile resolve `go` from PATH: system go 1.22.2 fails (`go.mod requires go >= 1.26`). Prepend the toolchain bin dir — proven by build_gate pack and make test legs.
- `go build ./cmd` (no `-o`) fails by design (cmd/ directory-collision trap) — evidence captured, not a defect.
- The `fe-ship-gate-tsc-vitest-vite` skill carries a stale hard-coded `TOTAL_ERRORS=19` (now 18) and lacks the error-set-swap recipe; `skill_fix` recorded but the evolution lane is disabled — needs manual action.
- Gate-leg scratch dir `/tmp/credgroups-gate/` was shared across legs (distinct filenames, no observed clobber; the gofmt leg re-derived + re-ran after the window, identical result). Future gates: per-leg scratch dirs.

---

## Hermeticity ledger

Every Go-running leg captured the real `~/.config/llm-supervisor-proxy/config.json` stat+md5 before/after. All identical: `1792303696 1677` / `5234b6264278a9cd91a37a8c7afbdd81`. The real home was never rewritten.

## Worktree integrity

`git status --porcelain` at every leg boundary showed only:
```
?? .agents/approver/credential-groups-tracking.md
?? .agents/tester/RESULTS/2026-11-17-credential-groups-static-zero-mod.md
```
No tracked file modified by any leg; no commit/checkout/stash/reset/clean/push/ref-move executed by any leg; main checkout untouched; no process on port 8088 touched. The artifact commit that carries this file is the ONLY write, and touches `.agents/tester/` exclusively (not compiled — the gated tree is unchanged by it).

## Worker instances

13 legs dispatched to `agent=worker` (one skill each): cg-gate-packA (a377d37b), packB (eccc715f), modelscache (1de7e33e), fevitest (88f6c502), tsc (486f021e), static (c989596b), gofmt (745c119f), drift (847ccfcc), o2pin (46c040e0), boundaries (f37ceb1a), feantivac (e51eb906), buildvet (73201ef4), beantivac (1132b5b8), maketest (8d863fb0). All reported with concrete artifacts; zero re-dispatches needed.

**Gate verdict: GATE-PASS.** All 12 enumerated items green; T-2.3 (make test) green with the expected quarantine shape; T-1 boundaries green except the adjudicated naming finding (F-1, 🟠, leader decision). Anti-vacuity proven for every pin the commission flagged as highest-value.
