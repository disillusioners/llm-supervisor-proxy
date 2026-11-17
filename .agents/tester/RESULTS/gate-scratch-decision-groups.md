# Gate scratch notes — decision-groups full gate (2026-11-17)

## Static/git worker (c1db8ae4) — ALL OK
- HEAD e5f0816 verified, 28 commits a2952a9..HEAD, 33 commits 2b987bf..HEAD
- BASE = merge-base(feature, origin/latest) = a2952a9 (unchanged; origin/latest moved to dd8c585, origin/master 3891e9d)
- gofmt NEW drift = 0 vs BOTH 2b987bf and a2952a9 (base 47 flagged, head 47 flagged); touched _test.go drift 0
- Boundaries: k8s/ .github/ pkg/store/database/migrations/ migrate.go pkg/config/ = 0 changed files each
- B4 proven: wrong pathspec 'migrations/' = 0 files; real 'pkg/store/database/migrations/' = 80 files
- Tier-1: 11 files x 3 bases = 33/33 byte-identical, 0 fails; all 11 existed at 2b987bf
- Tier-2: 8 Go files — removed funcs EMPTY, assertion-guard hits 0; 5 FE updated files assertion-guard 0
- Net churn: base 2371 -> head 2451 funcs; 0 missing; 0 pre-existing headers touched; +80 added / 0 removed (vs 2b987bf)
- B5 synthetic: positive control 1 hit (t.Fatalf line), negative control 0 hits, worktree untouched

## Pack A worker (9cb35b41) — PASS
- 9 packages (corpus's "10" is its own miscount of a 9-item list; approver note N1)
- exit 0, wall 134s; pkg/proxy 131.416s (inner 240 / outer 300 — margin intact)
- --- PASS 1339, --- FAIL 0, --- SKIP 11 (intentional: SSE heartbeat/long-wait + PG-connection)
- Both known Pack A flakes PASSED on the full run (PeakHourParity, CloseLifecycle) — no adjudication triggered
- log: /tmp/dg-gate/logs/A.log (11343 lines)

## FE worker (e218bf9a) — G9/G10/G11 ALL PASS
- G9: vitest exit 0; Test Files 29 passed (29); Tests 328 passed (328); x lines 0; v lines 328
- G10: tsc exit 2; baseline sha fa0a0d63... IDENTICAL before/after (file unmodified);
  per-file counts match on all 9 files; TOTAL_ERRORS=19 TOTAL_FILES=9 exact; file-set delta EMPTY both ways
- G11: rebuild BYTE-IDENTICAL to committed assets (4 files, same sha256); porcelain EMPTY (no restore needed)
- G11 pairing: ee4644e/ae5554a/6aedf00 each carry FE src + static; 5a26a78 is src-only but
  provably asset-neutral (type annotation + comment hoist; git diff 6aedf00 HEAD -- pkg/ui/static empty)
- TOKEN CENSUS: 48/48, each token matched EXACTLY ONCE.
  E8 EXISTS (groupLane.test.ts) => phase3's 48-token list is CORRECT;
  phase2-plan.md §4a's "47 tokens (E1-E7)" is the STALE copy. Documentation defect, not a coverage gap.

## Build worker (e8f71275) — G5/G6/G7/G8 + marker + doctrine ALL OK
- G5 real entry: 26,477,333 B (~26 MB) sha 77b2c2a5...; root stub 3,730,246 B, exits 0, binds NO port
- G6 build ./... exit 0 (0 bytes diagnostics); G7 vet exit 0 (0 bytes diagnostics)
- G8 layer2: all 3 greps EMPTY (exit 1, no GUARD FAIL); wired Makefile:52-66, @go test ./... at :68
- G8 negative control FIRES on a comment-quoted banned path (ADR-14 raw-text trap)
- G8 layer1: FOUR guard tests, literal --- PASS each, incl TestPackage_ImportGuard_GroupPolicyFiles_NoCacheImports
- Marker: pkg/decision/group.go:57 const; in real binary grep -cF = 1; root-stub control = 0
  (live-host negative control = deploy-time, NOT claimed)
- Doctrine: suppression.go:8-9 == policy.go:17-18 => cmp BYTE-IDENTICAL; pinned test PASS
  NOTED: pkg/skipcache/skipcache.go holds a RE-WRAPPED paraphrase (:29-31, :144-146), not byte-exact

## Mech worker (cc0f4706) — B1 RED / B6 PASS-with-residue
### B1 — CI-STALL RECOVERY LADDER: FAIL (operative-text self-contradiction)
- Rung order CORRECT (:1253 admin re-run -> :1254 empty-commit fallback); "workflow_dispatch cannot
  publish" correctly stated at :1251/:1255/:1486.
- BUT rung 2 is FALSE: `git commit --allow-empty` = TRUE zero-delta => changed-file set EMPTY =>
  NO workflow event => NO build => NO publish. The corpus itself says so at :1251, and
  docker-ci.yml:4-5 says it outright ("a zero-delta push (empty commit / --no-ff merge with an
  identical tree) emits no workflow event at all" -- comment added by 955f5c7). So the
  "no-admin available" branch of the ladder CANNOT publish.
- RESIDUE: live §8 ledger row :338 still asserts the OPPOSITE ("workflow_dispatch IS first-class ...
  rebuilds and publishes with no null-content commit"), plus index row :360. Both UN-MARKED, though
  the corpus's own convention (R16) puts a SUPERSEDED marker at every corrected site (26 elsewhere).
- Corpus's own claim vs workflow: OK — every publish step gated event_name=='push' (:110 login,
  :117 buildx, :121 metadata, :136 build-and-push push:true :141). :153 scan pulls only, no push.
- Zero-delta claim vs workflow: OK (corroborated by docker-ci.yml:4-7).
- "Do not ship an unbuilt sha" present at :1486.
- HOUSE PRECEDENT (blueprint "Verification & Build Harness"): the PROVEN retrigger is a
  ONE-LINE NULL-CONTENT COMMIT (a content change -> non-empty changed-file set -> event fires),
  then --no-ff latest -> --no-ff master (wi-7769a812: 429a6ef->d9cdbea->487be74).
  `--allow-empty` is NOT that. Fix = rung 2 must be a NON-ZERO-DELTA retrigger.
- CORPUS IS UNTRACKED (`?? .agents/shared/planning/decision-groups/`) -> the defective runbook text
  would be COMMITTED at ship unless corrected first.

### B6 — PASS (fix landed), 3 residue items
- Order CORRECT: assign :763 -> before-hash :764-765 -> HOME export :766 -> after-hash :1142;
  after-hash uses "$REAL_HOME_CONFIG"; grep 'sha256sum "$HOME' EMPTY (no quarantined-home hash survives)
- EMPIRICAL: real config sha 68a06693... 1677 B mtime 2026-10-18 == pre-gate capture => UNCHANGED
- SENSITIVITY proven on a COPY (5381d78e... vs 68a06693...) => the guard CAN fire
- Residue 1: the smoke's own pair does NOT exist — :1127 writes config_smoke_before.sha,
  `grep -c config_smoke_after` = 0 => dangling write, never compared.
- Residue 2: evidence table :1464 over-demands "config.json before/after hashes (real + quarantined)";
  only the REAL pair is produced.
- Residue 3: gate matrix P0.5 at :457 still uses the TILDE form
  (`sha256sum ~/.config/llm-supervisor-proxy/config.json`) — after the :766 HOME export, ~ resolves
  to the quarantine => the B6'' trap in the MATRIX row (the Execution-order block :763 is correct).
- NOTE: residue 1 is CLOSED EMPIRICALLY by this gate run — the smoke worker was instructed to compute
  and compare the smoke-home pair explicitly.

## Pack B worker (de9d954d) — FAIL-as-executed (exit 1) = PASS-with-quarantine
- 34 packages (corpus said 32; +pkg/modelcache 2026-11-16, +pkg/credclass this branch) — LEGIT growth
- exit 1, 55s wall; 1107 top-level PASS / 2 FAIL / 1 SKIP (file-gated minimax log skip)
- The 2 FAILs = the pre-registered httpbin.org pair (QUARANTINE row 11), SAME signature :1864/:1904
- ADJUDICATION: DETERMINISTIC third-party failure, NOT a flake — 6/6 isolated attempts fail identically;
  curl proof: httpbin returns 403 text/plain server-side BEFORE the CheckRedirect classifier
- BRANCH-ORTHOGONAL: git rev-parse HEAD:pkg/mcp origin/master:pkg/mcp origin/latest:pkg/mcp = ALL 3d1d0c4b... (bit-identical)
- TOCTOU + usage stress did NOT fire; longest pkg = usage 53.131s (inside 240s)
- ANOMALY (worker-raised, minor): the repo-relative anchor variant ALSO yields 34 (identical set) — the
  corpus's "re-runs everything" claim is FALSE; the variant that actually leaks is the END-ANCHOR-ONLY
  `(cmd|pkg/(proxy|store|decision|ui))$` -> 38 (pulls in 4 Pack A packages). The `(/|$)` SUFFIX is the
  load-bearing part, not the module prefix. Documented normatively in PACKS.md "Release-Gate Slices".
- Hermeticity self-verified by worker: real config md5 5234b626... before AND after (mtime 2026-10-18)

## House docs updated by the tester this gate (in the worktree, uncommitted)
- PACKS.md: Summary 40 -> 43 Go packages (measured Pack A 9 + Pack B 34)
- PACKS.md: new "## Release-Gate Slices (normative)" section — Pack A 9 pkgs, Pack B 34-pkg anchored
  complement, the `(/|$)`-suffix finding, and the CORRECTED boundary pathspec
- PACKS.md: new 2026-11-17 gate note (full evidence summary + the RED-on-B1 verdict)
- PACKS.md: mcp_unit_test row — 2026-11-17 reconfirmation appended
- QUARANTINE.md row 11 — Attempts +8F (Pack B 2F + 6 isolated), Status: 2026-11-17 reconfirmation
  (deterministic 6/6, curl-proven 403, tree-hash bit-identical to origin/master AND origin/latest)
- NOT changed: tsc-baseline.txt (unmodified, proven by sha), no new quarantine rows (no new flake found)

## Smoke worker (964f8565) — G12 PASS
- binary 26,477,333 B (real entry; build info path .../cmd, go1.26.0), NOT the 3.7MB stub; port 14399 free pre-boot
- /healthz 200 ("ok") · /fe/api/version 200 ({"version":"dev"}) · /v1/models 200 ({"data":[],"object":"list"})
- 0 panics; clean SIGTERM (exit 0); port 14399 released; both PIDs confirmed dead; nothing touched on 8088
- B6 EMPIRICALLY CLOSED: real config sha 68a06693... + size 1677 + mtime 2026-10-18 IDENTICAL before/after,
  and identical to the 08:18 pre-gate capture ("REAL CONFIG UNTOUCHED")
- CORRECTION TO THE CORPUS'S B6 REMEDY: the smoke-home config.json is ABSENT both sides — the REAL ENTRY
  uses the DB-BACKED ConfigManager (cmd/main.go -> pkg/store/database/store.go), and migration
  017_config_json moved config from the file into the DB `configs` table. The binary writes config.db
  (126,976 B) + buffers/ under $SMOKE_HOME; it NEVER creates config.json.
  => the corpus's step-10 pair (hash $SMOKE_HOME/.config/.../config.json) is not merely DANGLING
     (residue #1) — it is the WRONG FILE and would compare "absent" vs "absent" forever.
     The correct quarantine evidence for the real binary = config.db + buffers/ under $SMOKE_HOME.
     (The file-based config.json rewrite still governs the GO-TEST path — pkg/proxy sets
      APPLY_ENV_OVERRIDES=1 — so the HOME quarantine remains the actual protection for the suites.)
- NOTED: anon /v1/messages returned 400 (no max_tokens) / 404 model_not_found (with max_tokens), NOT 401.
  Cause: the P1 boundary gate (resolvedModel == nil || !Internal -> 404) runs BEFORE the auth gate, and the
  isolated store has ZERO models, so 404 pre-empts 401. NOT a regression: 1c06b59 (auth+allow-list) and
  8113caf (the 401/403/200 contract pin) are ancestors of HEAD and that pin PASSED in Pack A.
  No anonymous request was SERVED (no 200, no upstream attempt). The 401 SHAPE is proven by the branch's
  own contract test, not by this smoke (would need a store with a resolvable internal model = out of scope).
- Also: the binary carries no vcs.* stamp (mod (devel)) — provenance confirmed by rodata fingerprint;
  no build manifest ties the binary to a SHA. NOTE for the ship step: the CI image carries the stamp.
- AMBER (house bookkeeping, no code impact): shared_meta_kv `wt.claim.decision-groups.branch_sha` still
  reads a2952a9 (the stage-0a re-anchor point / merge-base), 28 commits behind the tip e5f0816. Not a
  contradiction (a2952a9 IS an ancestor) — but the KV should be refreshed to e5f0816 at ship.

## SIDE-EFFECT FLAG for the leader (constraint violation, cannot self-correct)
- The packb worker created a POK entry `packb-anchor-count-34-at-e5f0816` — the dispatch said "no KB/POK writes".
  Content is accurate and harmless, but it is out of scope. pok_curate(archived=True) was REFUSED
  ("pok_curator_required" — tester is not a curator). Needs leader/project-manager cleanup.
- The SMOKE worker created 2 MORE POK entries (`gate-02-realentry-boot-smoke-home`,
  `gate-03-anon-messages-401-gate-order`, domain verify, audience both) + 1 KB record (DB-backed config
  architecture + gate-ordering fact). Also out of scope.
- DISPATCHER OWNERSHIP: the "no KB/POK writes" constraint was in MY brief but I did NOT propagate it into
  the worker briefs. That is my miss, not only the workers'. A constraint reminder was injected into the
  still-running anti-vacuous worker after this was observed.
- The mech worker minted a new skill `ci-workflow-claim-verification` (c57103f7-c6b4-47b6-9d91-abb232efcf13)
  from its B1 findings. Not a KB/POK write (so within the letter of the constraint), noted for transparency.
- No delete/curate path exists for me: pok_curate is curator-only (refused) and there is no KB delete tool.


## Anti-vacuous worker (c184b986) — G4 GREEN
- 74/74 Go names: literal end-anchored `--- PASS`, 64 A + 10 B, 0 MISS, 0 SKIP, each exactly once
- 48/48 FE tokens: each matched exactly once, 0 at n=0, 0 at n>1, x=0
- 5 withdrawn names: absent from diff (both bases), HEAD source, and both logs
- RECONCILIATION CLOSED: 74 expected = 74 added vs a2952a9 (gap 0, unlisted 0).
  The +6 vs 2b987bf = upstream 9router probe names from 13a8e4e (reachable from origin/latest, ALREADY at
  a2952a9, introduced by NO commit in a2952a9..HEAD) => zero commission attribution.
- LEDGER DEFECT RESOLVED: store_group_test.go = 16 (not 17). The 74th name
  TestDecision_Vocabulary_CredentialModelSplit_AnthropicSaves_ModelRejected lives at
  pkg/store/database/models_manager_kind_test.go:726 (added by f5ade55) — a file the phase-3 ledger omits.
  Grand total 74 is CORRECT; only the per-file split was wrong. Two independent methods agree.
- COLLISION GUARDS: (a) no expected name pre-exists at a2952a9 or 2b987bf (CLEAN)
  (b) 3 Go prefix pairs FOUND but SAFE under the end-anchored grep (unanchored would give 2 hits, anchored 1)
  (c) 48 FE tokens distinct; prefix relations A1<A10..A13 and D1<D10 EXIST but SAFE (each credits exactly 1 line)
- TALLY: A 1339P/0F/11S, B 1107P/2F/1S; 2446 top-level PASS; A∩B package overlap = 0; the 74 are a strict subset
- METHODOLOGICAL (tester-owned): my hand-built expected list was in LEDGER order, so the first `comm` run was
  garbage ("file 1 is not in sorted order"); the worker re-ran with LC_ALL=C sort -u. The CORPUS's own step-3 is
  safe (it writes expected_go_tests.clean through `sort -u`) — the defect was my input file, not the runbook.

## Gate closed: all 9 dispatch requirements answered
1 PACK A/B ✓  2 HERMETICITY ✓  3 ANTI-VACUOUS GO 74/74 ✓  4 ANTI-VACUOUS FE 48/48 ✓
5 FE GATES ✓  6 TEXT-LANE FENCE ✓ (Tier-1 33/33 x3 bases; Tier-2 0/0; 74 added / 0 removed)
7 MECHANISM B1-B6 ✓ (B1 RED, B2-B6 OK)  8 GUARDS ✓ (import 2 layers, gofmt NEW=0, build+vet, marker, doctrine)
9 FLAKES ✓ (classified honestly; 1 deterministic external row; 4 known flakes did not fire)

- FE token count settled: 48 (E8 exists) — phase2 §4a's 47 is stale.
- phase3 heredoc comment labels off by one in 2 blocks (store_group says 16 lists 17; group_rules
  says 5 lists 6) — totals still reconcile to 74 (64 A + 10 B). PENDING G4 resolution.
- B6 non-vacuous: real config exists 1677B mtime 2026-10-18, sha256 68a06693... captured BEFORE runs.
- origin/latest moved to dd8c585 (dispatch said >=0c4a7d7) — post-merge re-gate still required.
