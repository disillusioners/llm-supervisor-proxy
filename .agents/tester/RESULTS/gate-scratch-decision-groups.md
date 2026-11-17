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

## ===== POST-MERGE RE-RUN (merged tree 5ffcbe3) =====

### MERGED Pack A (mg-packa) — PASS
exit 0, 140s wall; 9/9 pkgs ok; 1395 top-level PASS / 0 FAIL / 11 SKIP; proxy 130.867s, store/database
50.592s (up from 38.259s — upstream's added tests), decision 1.514s, ui 6.590s; 2422 top-level executions;
pkg/decision 249 top-level (160 decision-named) => the decision lane is exercised, not stubbed.
GOTCHA (institutionalise): a naive `grep -c FAIL` returns 775 — ALL of them the substring "FAILOVER"
inside test names. The anchored `^--- FAIL:` returns 0. Any future gate grepping failures naively will
read this suite as catastrophically broken. Hermeticity: real config sha identical before/after.

### MERGED Pack B (mg-packb) — exit 1, PASS-with-quarantine
34 pkgs (no new packages); 1151 PASS / 2 FAIL / 1 SKIP; exit 1, 61s; sole failures = the same 2 httpbin
tests at the SAME signature (:1864/:1904, 403) => identical to pre-merge + the 2026-11-14/2026-11-16 gates;
3/3 isolated re-runs fail identically; TOCTOU + usage-stress did NOT fire; real config hash unchanged.

### MERGED anti-vacuous (mg-antivac) — Go 74/74 GREEN; FE 48/48 PASS-with-collision
STEP 1: anti-vacuous OK 74/74, MISS 0; 0 SKIP; 0 FAIL; A_only=64 B_only=10 BOTH=0 NONE=0 not-exactly-once=0.
  Naive-grep trap quantified: naive `grep -c FAIL` = A 775 / B 2204 (FAILOVER = 2202 of B's hits);
  anchored `^--- FAIL:` = A 0 / B 2.
STEP 2: 5 withdrawn names ABSENT from the merged diff, from HEAD source, and from both logs.
STEP 3: expected=74, added vs dd8c585=74, removed=0, absent=0, unlisted=0 — EXACT.
STEP 4: upstream added 103 names; collision with our 74 = 0 (comm -12 empty); 0 upstream names in both logs.
  PASS-share accounting A+B: top-level PASS 2546 (2529 distinct) = our 74 + upstream-added 103 + baseline 2352.
  The 17 duplicate names across A+B are all upstream/baseline; 0 are ours.
STEP 5: per-file attribution IDENTICAL to the known-correct pre-merge split (0 deviation); the merge moved NONE
  of our 74 between files; all 13 defining files exist at MERGED.
STEP 6: 0 collisions at MERGED_BASE or OUR_ANCHOR; 5 prefix-pair families (incl. the pre-existing
  TestHandleModels_FilterDecision trap, which EXISTS in the merged tree at handle_models_imggen_test.go:155 and
  PASSes in A.log:1680) — all neutralised by the end-anchor (unanchored 2 -> anchored 1).
STEP 7 (FE): 48/48 tokens matched, 0 at n=0, 35 at n>1 (upstream reused A1-A13/B1-B7/B9/C1-C7/D1-D4/E1-E3 in
  5 upstream files, our-diff-added-lines = 0 for all 5); total x lines = 0; 416/416 tests pass.
  FILE ATTRIBUTION: all 48 of our tokens have EXACTLY ONE line in OUR 8 files (sum 48, 48 distinct lines,
  0 tokens credited solely by a foreign file). All 5 colliding files are 100% upstream's.
STEP 8: A PASS=1395/FAIL=0/SKIP=11; B PASS=1151/FAIL=2/SKIP=1; our 74 absent from the PASS set = 0.
  Merge integrity: all 103/103 upstream-added names still defined at MERGED and still have a PASS line.
  The 2 FAILs are defined at MERGED_BASE, pkg/mcp has 0 files in our diff, neither name is ours.

*** ADJUDICATION OF THE WORKER'S "RED-1" (it applied a criterion I set, stricter than the corpus's) ***
The corpus's own FE anti-vacuous rule (phase3 step 6) is a PRESENCE check — each token must yield an OK,
and "a MISS on any token => the gate fails". On the merged tree: 48/48 OK, 0 MISS, 0 x lines, 416/416 tests
pass, every case file-attributed to OUR files => THE CORPUS'S FE GATE IS SATISFIED.
The "exactly 1 over the whole log" property was the worker's/my stricter addition; it is a MEANS, not the
contract end. The FE anti-vacuous gate is therefore GREEN ON THE CONTRACT, with:
  - AMBER (gate mechanics, house): the standing whole-log "each token hits exactly 1" census (POK
    fe-gate-05-token-census-grep) is NO LONGER SATISFIABLE tree-wide and must become FILE-SCOPED
    (census within our 8 files) — it yields 48/48 at n=1 each with no source change.
  - RECOMMENDATION (future, test-only): namespace our labels (DG-A1 etc.) to restore tree-wide uniqueness.
  - No vacuity, no missing case, no double-crediting WITHIN our files.
Treated consistently with the Go-side prefix pairs, which were classified "found but safe" pre-merge.

### MERGED FE gates (mg-fe) — G9/G10/G11 ALL GREEN + 1 CROSS-FEATURE TOKEN COLLISION
G9 vitest: exit 0; "Test Files 38 passed (38)"; "Tests 416 passed (416)"; x lines = 0; v lines = 416.
  38 files ran (upstream's credential-groups FE files added 9 new test files vs the pre-merge 29).
  TOKEN CENSUS: all 48 tokens n>=1, but many n=2 (B6=3, C6=3, D1=3, D2=3).
  CAUSE: upstream v195's credential-groups FE tests REUSE the same A/B/C/D/E case-id letters in DIFFERENT
  files (CredentialsContainer A1-A13; CredentialGroupsTab B1-B7/B9; CredentialGroupForm C1-C7;
  CredentialGroupMemberPicker D1-D4; CredentialsTab E1-E3) — plus intra-file duplicates in upstream's files.
  FILE ATTRIBUTION proves OUR 48 are each satisfied by OUR own file:
    A1-A13 -> DecisionModelsContainer.test.tsx (ours) + CredentialsContainer.test.tsx (upstream)
    B1-B7,B9 -> GroupModelsTab.decisionLane.test.tsx (ours) + CredentialGroupsTab.test.tsx (upstream)
    C1-C7 -> GroupMemberEditor.decisionLane.test.tsx (ours) + CredentialGroupForm.test.tsx (upstream)
    D1-D4 -> GroupModelForm.decisionLane.test.tsx (ours) + CredentialGroupMemberPicker.test.tsx (upstream)
    D5-D10, E4-E8 -> ours only (GroupModelForm.decisionLane.test.tsx, groupLane.test.ts)
    E1-E3 -> groupLane.test.ts (ours) + CredentialsTab.test.tsx (upstream)
    T1 -> GroupMemberEditor.test.tsx (ours)   F1 -> EventLog.test.tsx (ours)   V1 -> DecisionModelForm.test.tsx (ours)
  => OUR 48 cases each exist in OUR files and PASS. The corpus's §4a rule #4 ("no other text in the suite may
  match another case's token") is now violated BY UPSTREAM's ids, not by ours. AMBER (namespacing DG-A1 is a
  separate future test-only commission), NOT a RED.
G10 tsc: exit 2; baseline sha 1bc60a57... IDENTICAL before/after; per-file counts identical on all 9 files;
  FILE-SET delta EMPTY both ways; TOTAL_ERRORS=18 (was 19 pre-merge) TOTAL_FILES=9, actual == baseline.
  ATTRIBUTION: OURS (dd8c585..HEAD) touched tsc-baseline.txt = EMPTY; UPSTREAM (a2952a9..dd8c585) changed it
  (1 file, +13/-3 — SettingsPage 6->5, TOTAL 19->18) — the credential-groups FE lane A.
G11 vite: "G11 OK: rebuild is byte-identical to the committed assets" (4 files, identical sha256);
  porcelain EMPTY (no restore needed). THE MERGE COMMIT 5ffcbe3 carried BOTH a FE-source change AND a
  pkg/ui/static change (pairing rule satisfied) => ITEM 4 PROVEN: the merge resolved build artifacts by
  REBUILDING from the merged source, not by picking a side.
  Assets at HEAD: SettingsPage-BDCXaBm2.js, index-DdLeb6Xb.js, index-Diz8jSFK.css, index.html.
  NOTE: node_modules was pre-existing; no fresh `npm ci` reproducibility check was run (separate scope).

B-1 G5: ./cmd = 26,700,265 B (26M) sha d563df8d...; root stub 3,730,294 B (3.6M); both exit 0.
  NOTED: local binary sha != the KV CI sha dc3f53c4... — cause is ONLY the `-ldflags -X main.Version` stamp
  (Makefile:34) vs a plain go build (version degrades to "dev"); CI injects APP_VERSION=run_number.
  TREE IDENTITY IDENTICAL: 5ffcbe3^{tree} == 3cf3cb8^{tree} == origin/master^{tree} == ea57ec7...
B-2 G6: build ./... exit 0, 0-byte diagnostics.  B-3 G7: vet exit 0, 0-byte diagnostics.
B-4 G8 layer 2: 3 greps EMPTY (exit 1); wired Makefile:54/:59/:64 + @go test ./... at :68; Makefile untouched by
  the merge (both diffs empty); NEGATIVE CONTROL FIRES on a comment-quoted banned path.
B-5 G8 layer 1: 4/4 literal PASS incl. TestPackage_ImportGuard_GroupPolicyFiles_NoCacheImports; exit 0.
B-6 marker: pkg/decision/group.go:57 (comment :23); binary=1, root-stub=0. Live negative control NOT claimed.
B-7 doctrine: pinned literal (154 B, 2 lines, em-dash) byte-identical in suppression.go:8 and policy.go:17
  (cmp exit 0 both); skipcache holds only re-wrapped paraphrases (:29-31, :144-146); the merged pinned test
  still reads suppression.go/policy.go and PASSES (both subtests); nothing written under pkg/.
B-8 B1 RE-VERIFICATION ON THE MERGED TREE:
  * The WORKFLOW did NOT change: docker-ci.yml byte-identical across a2952a9..HEAD (all three diffs empty).
    The pre-merge premise "the merge may have changed the workflow" is DISPROVED.
  * All 5 publish gates still `github.event_name == 'push'` (:110 Login, :117 Buildx, :121 Extract-metadata
    = the step that computes the tags, :136 build-and-push, :153 scan); push:true :141; the per-SHA tag is
    computed INSIDE the :121-gated step => a non-push event yields no tags and no per-SHA tag.
  * *** B1's SUBSTANCE IS FIXED IN THE MERGED CORPUS *** — rung 2 (:1269) is now the NON-ZERO-DELTA CONTENT
    RETRIGGER, states "NOT --allow-empty", and explicitly refutes the old claim ("a --allow-empty retrigger
    is therefore USELESS — it is the exact trap this rung previously encoded"). The §8/:338 and §10/:360 rows
    are now STRUCK THROUGH + marked SUPERSEDED (B1, tester gate RED).
  * *** BUT 3 RESIDUAL LIVE ROWS STILL PRESCRIBE THE FALSE ACTION ***
      - phase3-plan.md:1272  (the W4 summary row) — still says "empty-commit retrigger as the autonomous
        emergency fallback"
      - phase3-plan.md:1501  (risk register R6 — THE ROW AN OPERATOR ACTS ON UNDER STALL PRESSURE) — still
        says "the empty-commit retrigger to master"
      - architecture-recommendation.md:491 — un-marked, normative, asserts "a manual run is a first-class
        recovery path" (the opposite of the corrected truth)
    => B1 verdict moves RED -> AMBER (mechanism correct, residue live). NOT fully green.
  * NEW FACT: the corpus is now TRACKED (committed in 1f6fa2b "docs(decision-groups): the planning corpus,
    the gate artifacts and the tracking notes"), NOT untracked — so the pre-merge corpus text is NOT
    git-diff-recoverable (single commit) and the B1 correction cannot be diff-proven, only the merged state
    assessed. sha of phase3-plan.md = b2a5de2f77a7e67dd13475e252c0b5fe38dab12537cc3a4b411e5e45d6c46e8b
  * Minor: the corpus cites `docker-ci.yml:13-16` for the paths filter; the real block is :14-16 (:13 is a
    branch pattern). And "Do **not** ship an unbuilt sha" is BOLD-SPLIT => `grep -i 'do not ship'` returns 0;
    `grep -F 'ship an unbuilt sha'` hits. The guard survives but is not plainly greppable.

S0 topology: merge parents 1f6fa2b + dd8c585 confirmed; all 4 bases ancestors; dd8c585..HEAD=31 (our 30 +
merge) => diff dd8c585..HEAD IS our contribution; tree(promo 3cf3cb8)==tree(latest 5ffcbe3)=ea57ec7 YES.
S1 gofmt: base dd8c585 flagged 46, base a2952a9 flagged 47, head 46 => NEW drift 0 vs BOTH; touched _test.go 0.
  The 47->46 drop is upstream's fix to pkg/proxy/normalizers/registry_config_test.go (the merge took
  upstream's fixed side). Drift went DOWN, never up.
S2 G13: OURS (dd8c585..HEAD) = 0 changed files on k8s/ .github/ migrations/ migrate.go pkg/config/.
  UPSTREAM (a2952a9..dd8c585) brought pkg/config/config.go (+87), 2 pkg/config test files, migrate.go (+5),
  and migration 033 (4 SQL files: postgres+sqlite up/down). B4 re-check: bare `migrations/` = 0 files,
  real `pkg/store/database/migrations/` = 84 files.
S3 TIER-1: 10 files blob-IDENTICAL at all four revisions (a2952a9/dd8c585/1f6fa2b/HEAD).
  ITEM 2 VERDICT: our contribution to pkg/grouplb/engine_test.go = 0 (a2952a9..1f6fa2b EMPTY; blob
  27a910b30f at anchor AND our tip); merge TOOK UPSTREAM'S SIDE (HEAD blob c50a936937 == dd8c585's).
  *** PREMISE CORRECTION (anomaly ada0a10f): it is NOT "rename only" — the renamed test's assertion was
  INVERTED (!after.After(before) -> !after.Equal(before): "must REFRESH boundAt" -> "must NOT refresh
  createdAt (absolute lifetime)") + 2 message-text assertion changes. Upstream's sliding->absolute TTL
  semantics. Attribution and fence verdict unaffected. ***
S4 TIER-2 + ITEM 3: all 8 files — 0 removed test funcs, 0 assertion-guard hits, 0 removed lines.
  The single removed line in store_group_test.go is UPSTREAM'S COMMENT line; our delta vs dd8c585 removes 0.
  *** PREMISE CORRECTION (anomaly 3e788787): upstream grew the INTERFACE (models.ModelsConfigInterface +9
  credential-group methods); the stub is OUR file (absent at both a2952a9 and dd8c585) and THE MERGE
  COMMIT 5ffcbe3 added the 9 no-op adapters to it (35 insertions, 0 deletions). All 9 have 0 direct call
  sites => compile-time interface satisfiers. ***
S5 ITEM 1: added vs dd8c585 = 74; removed = 0; absent = 0; unlisted = 0; upstream added 103 names,
  collision with our 74 = 0 BOTH directions; all 74 new at dd8c585 and defined exactly once at HEAD;
  5 withdrawn names absent from diff AND HEAD.
S6 pkg/ui/static: the MERGE commit carried BOTH a FE-source change AND a pkg/ui/static change. HEAD's
  bundle is a REBUILD (new content hashes on 2 of 4 assets), NOT a byte-copy of either parent. Static proof
  HEAD carries BOTH feature sets: "Decision Model"=10, "Decision Group"=3 (ours) AND "Credential Groups"=3,
  "/fe/api/credential-groups"=1 (upstream's). CSS byte-identical to upstream's. index.html a fresh manifest
  pointing at both HEAD assets. Byte-identity rebuild proof = the FE worker's.
S7 B5 re-proof: positive 1 hit, negative 0, worktree untouched.
Deletion accounting: dd8c585..HEAD = +19796/-271; excluding pkg/ui/static = +19783/-258; NO test assertion
deleted (independently confirmed: 0 removed test funcs + 0 assertion-guard hits).
