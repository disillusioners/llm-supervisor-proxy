# Tidier Review 001 — decision-groups (maintainability / craftsmanship pass)

- **Diff:** `a2952a9..fb1199e` (25 commits), 57 changed paths, +8565/−253. `pkg/ui/static/*` (generated vite bundles) excluded — ~52 reviewable source files.
- **Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-decision-groups`, branch `feature/decision-groups` @ `fb1199e`, nothing pushed.
- **Iteration:** 001 of 3 (first tidier pass on this commission; code-review gate already APPROVED).
- **Dispatch:** 3 parallel skill-per-worker reviews — `tidier-readable-code`, `tidier-static-hygiene`, `tidier-robustness`. All three reported; no gaps, no timeouts, no re-dispatches needed.
- **Method:** workers inspected the diff via `git diff`/`git show`/file reads only; findings formed from code alone (planning corpus deliberately not read). Hygiene worker additionally ran read-only gates: `tsc --noEmit` (baseline parity measured), `gofmt -l` (0 drift on changed files), `go build ./...`, `go vet` incl. test files (clean), and the 4 `pkg/decision` import-guard tests (pass).

## VERDICT

**Concrete maintainability defects present — not a clean approve. 0 High / 16 Medium / 17 Low (Tidier scale).**

All 16 Medium are **mechanical** fixes (message helpers, constant extraction, comment/doc adjacency, one cast removal, one guard reorder) — none re-litigates architecture or correctness, and none blocks the tester gate. Recommendation: **one Developer fix pass before ship** for the Mediums (prioritized list below); the 17 Lows are a defer list. In the caller's three-way vocabulary: this lands in the **HIGH ISSUES bucket only in the sense of "concrete defects a maintainer will pay for"** — severity-calibrated, none is 🔴.

Gate-level health confirmed by measurement: **tsc baseline 19 errors / 9 files — zero new errors or files from this diff**; gofmt zero new drift; vet/build clean; FE id-first naming contract fully adhered (49 new cases, exact em-dash separator, no token collisions); no unused imports, no `any` misuse, no TODO residue.

---

## Answers to the six examination questions

**1. Naming & vocabulary** — mostly one vocabulary; three defects: the shadow-target "does not exist" message still hardcodes `chat-kind` at all three sites while its two sibling messages were lane-worded by this very diff (**M2**); `decision_misroute` payload shapes diverge at the diff's own emit sites plus one silent arm (**M5**); `hasSuppressedOnly` names the wrong predicate (**M9**); `client_attributable` breaks the outcome-vocabulary shape (**L-m**). Event/accounting key spelling itself is single-sourced via `withDecisionAccounting` — good.

**2. Dead / ambiguous code** — `normalizeKind` verdict: **acceptable executable guard, not a trap** (see **L-f**): removal is loud (its only import is the E1 test — deletion breaks the suite compile), misuse inside the fold is loud (E8 reds twice), so it cannot be silently "cleaned up" into a breakage. Residual gap: a *future new module* importing it for lane decisions is only defended by a comment — move the invariant to the file header (and optionally rename to `legacyKindNormaliser`). Other leftovers found: dead `memberAttempt.err` field (**M7**), dead loop in the vocabulary pin test (**M11**), zero-importer `GroupLane` re-export + duplicate `ModelKind` union (**L-f**), exported-but-untested `GroupLaneFromCollection` (**L-g**). `doc.go` amendment is **truthful** (group branch described; classifier seam lists all six Outcome fields matching converter.go; stale import list replaced by a drift-proof rule — the rule-vs-list trade-off is intentional, noted in Deferred). Deleted-then-restored test file (`models_manager_kind_test.go`, commit 9150400) verified **coherent**: pure 70-line append, nothing lost, no orphaned helpers; the test *move* (5dd9d9e) also clean.

**3. Structure** — the member loop is **legible from code alone**: the loop-contract block at `pkg/decision/group.go:9-35` carries the operation order, and `memberFullyUnavailable` sits directly below its only caller `attemptSuppressed` (:396→:461). The readability tax is elsewhere: the OQ-1 honest-attempt block re-implements the in-loop attempt tail with undocumented event asymmetry (**M10**); `attempted` ≡ `members_attempted` — two counters, one number (**M6**); counter-trio threading lengthens signatures (**L-l**). FE container / groupLane.ts / component split **matches the repo's own convention** (twin of `probeErrorClasses.ts`); `lane` is a required prop everywhere (compile error beats silent chat fallback). Test organization is acceptable but the 1914-line handler_group_test.go has 4 banners and one 383-line function (**L-q**).

**4. Duplication** — the FE fold **accurately mirrors** the server fold member-for-member (verified: empty id / vanished row / no-lane kind / unanimity, `groupLane.ts:144-152` vs `store_group_rewrite.go:371-384` + `models/group_lane.go:95-101`) — but the mirror is **documented on the FE end only**; no Go comment mentions the FE twin and no cross-seam pin exists (**M3**; the parity test pins only the server-side POST≡PUT≡canonical trio). The lane-validation fold + rejection messages live **three times** (canonical + POST + PUT inline mirrors), already drifted in prefix wording (**M1**). The suppression doctrine is quoted verbatim in a **second production file** (`policy.go:15-18`) that the verbatim pin test does not cover (**M8**). `decision_misroute` machine code duplicated as ~10 raw literals while its sibling got a named const (**M4**).

**5. Conventions** — measured green: gofmt (0 new drift), vet/build, tsc baseline (0 new), FE naming contract (id-first, exact separator, no collisions), no `any`/unsafe casts in production FE except one (**M15**). Violations of house idiom found: hand-rolled nested scan where neighbors build a `byID` map (**L-a**); TTL clamp duplicated in `cmd/main.go` vs the seam's own clamp (**L-e**); doc-adjacency damage from inserted compile-time assertions in two hot files (**M12**, **M13**); mangled godoc on `UnanimousGroupLane` (**M14**).

**6. Pin-battery maintainability** — the mutation-control tests are **self-explanatory at the point of use**: `cooling_only_latch`, `per_member_latch`, `per_request_cap_zero_cost_skip` each open with a plain-language intent comment, and test names are self-explanatory. Two gaps: the corpus shorthand tokens (RC-1, R-2(a)/(b), Q1/Q2, B1/B2, D-C, AM-4, AC-R10, E1-E8, D1-D10) resolve only from the planning corpus — add a short code→meaning block (**L-n**, also fixes the unexplained "(count stays at 74)" arithmetic comment); the B8 and D7 numbering holes are unexplained, and D7 intentionally avoids the legacy "D7 backward-compat" token — one header line each, do NOT renumber (**L-o**).

---

## Findings

### 🔴 High

None. No resource leaks (all bodies/contexts `defer`-closed), no swallowed exceptions in critical paths, no file >3000 lines, no unused/duplicate/side-effect imports, no `any`/`interface{}` misuse, no new tsc errors.

### 🟡 Medium (16 — recommended for the Developer fix pass)

**[Medium] Code Smells: lane-validation fold + rejection messages live three times, already drifting — pkg/models/group.go:205-247; pkg/ui/server.go:949-974 (POST), :1510-1535 (PUT)**
- Problem: derive-lane → `GroupLanesOf > 1` mixed check → invalid-kind rejection is written out three times (canonical validator + POST/PUT inline mirrors, ~26 new lines each), and the rejection messages are copy-pasted `fmt.Sprintf` literals with differing prefixes (`"model X: kind Y member …"` vs `` `kind "group" member …` ``). The parity test pins substrings only, so bytes can drift without a red. *(Found independently by all three workers.)*
- Impact: every future lane rule must be typed three times; wording drift is already present; three hand-synced copies in a 2796-line file.
- Fix: extract models-level helpers — e.g. `DeriveGroupLane(kinds []string) (GroupLane, error)` plus shared message constructors (the `KindChangeGroupMemberMessage` precedent) — and have `ValidateGroupMembers`, POST and PUT call them. Keep POST/PUT envelope plumbing inline (house mirror discipline preserved); the parity test then locks single-sourced behavior.

**[Medium] Coding Style: shadow-target "does not exist" message hardcodes `chat-kind` at all three sites — pkg/models/group.go:254; pkg/ui/server.go:982, :1543**
- Problem: this diff lane-worded two of the three shadow-target messages to `"%s-kind"` via `groupLane`, but the not-exists arm kept the literal "must reference an existing chat-kind model" — on a decision-lane group the operator is told to reference a **chat**-kind model. The FE twin already carries the lane-worded pair (`SHADOW_TARGET_MISSING`, GroupModelForm.tsx). *(Three workers; merged up from Low×2 + Medium×1 with justification: operator-facing misleading message, and the diff's own generalization pass missed this arm.)*
- Impact: misleading validation output on the new lane; server lags the FE.
- Fix: interpolate the already-in-scope `groupLane` in that message too. Chat-lane output stays byte-identical ("chat" == the historical literal), so the frozen wording contract is preserved.

**[Medium] Readability: FE↔server lane-fold mirror documented on ONE end only, no cross-seam pin — pkg/ui/frontend/src/components/config/groupLane.ts:106-135 vs pkg/store/database/store_group_rewrite.go:354-384 (+ pkg/models/group_lane.go:95-101)**
- Problem: the FE side documents the mirror exhaustively (verified accurate member-for-member), but grep finds no mention of the FE twin anywhere in the Go fold comments, no mechanical cross-seam pin exists, and the FE's line-number citations (`:369-385`, `:375-377`, `:96-99`, `:67-71`) rot silently.
- Impact: a future server change to the fold has no signal that `groupLane.ts` must move with it — the exact two-answers-must-not-ship hazard the branch rulings warned about.
- Fix: one sentence in the Go fold comments ("mirrored in the FE at pkg/ui/frontend/src/components/config/groupLane.ts — update both"), and/or extend the parity pin to read the FE source (the source-literal style already used in `pkg/decision/vocabulary_pin_test.go`).

**[Medium] Code Smells: `decision_misroute` machine code duplicated as ~10 raw literals while its sibling got a named const — pkg/proxy/handler.go:297-298, 658, 680, 687; pkg/proxy/handler_anthropic.go:309, 311, 321, 323; pkg/decision/handler.go:322, 329; pkg/decision/group.go:105, 112**
- Problem: the same diff that named `decisionAllMembersFailedCode` (pkg/decision/group.go:57) added ~10 bare `"decision_misroute"` strings across two packages.
- Impact: the greppable marker vocabulary can be split by one typo at any site; one diff teaching two conventions.
- Fix: name it once per package (`decisionMisrouteCode` in pkg/decision; in pkg/proxy import it if the import direction allows, else a const + cross-referencing comment) and route publishes through one helper.

**[Medium] Coding Style: `decision_misroute` payload shapes diverge at the diff's own emit sites + one silent arm — pkg/proxy/handler.go:294-299 vs :676-681; pkg/proxy/handler_anthropic.go:311-313, 323-325**
- Problem: the chat helper publishes `{outcome, model}`; both new Anthropic arms hand-roll `{id, model}` (twice each); and the chat decision-lane arm publishes **no** event while its sibling mixed arm (:682-688) publishes via the helper.
- Impact: one event type, two payload shapes for consumers; the silent arm reads as an omission, not a decision.
- Fix: use `publishDecisionMisrouteEvent` at all sites (add `id` to the helper if the Anthropic lane needs it), and either emit on the decision-lane arm or add one line stating the silence is deliberate.

**[Medium] Readability: `attempted` ≡ `members_attempted` — two counters, one number — pkg/decision/group.go:129, 194-195, 255-256**
- Problem: both counters increment at exactly the same two sites, so they are identical for every walk, yet `members_attempted` implies distinct-member semantics. Both travel as event keys via `withDecisionAccounting` (:359-364).
- Impact: readers hunt for a divergence that does not exist; telemetry consumers misread exhaustion/fail-fast events; a future edit incrementing one silently breaks an undocumented equality.
- Fix: drop one counter (publish `attempted` only) — or make `membersAttempted` genuinely per-distinct-member — and state the chosen semantics at the declarations.

**[Medium] Error Handling: `memberAttempt.err` never populated — attempt-path errors reduced to outcome strings — pkg/decision/group.go:89 (field), :583-637 (emit path)**
- Problem: errors from `provider.BuildUpstream`, `http.NewRequestWithContext`, `sharedHTTPClient.Do`, `io.ReadAll`, `provider.InspectResponse` are all discarded — only an outcome string (`"build_failed"`, `"request_build_failed"`, `"upstream_transport_error"`, `"upstream_read_failed"`, `"inspect_error"`) reaches `res.failure`; `memberAttempt.err` is a dead field (never assigned, never read).
- Impact: upstream failure causes are lost — hard to diagnose without reproduction; the struct advertises a channel that carries nothing.
- Fix: set `res.err = err` in each failure branch (and/or debug-log) so the cause survives into telemetry; if ever propagated, wrap with `fmt.Errorf("…: %w", err)`. (Alternative: delete the field — but preserving the cause is the better fix.)

**[Medium] Code Smells: suppression doctrine quoted verbatim in a second production file the pin does not cover — pkg/decision/policy.go:15-18 (pin: handler_group_test.go:1834-1846 covers suppression.go only)**
- Problem: two "verbatim" copies of the doctrine in one package; the pin test reads `suppression.go` only.
- Impact: the policy.go copy can drift silently — two sources of truth for a one-source doctrine.
- Fix: reduce policy.go to a paraphrase + pointer to suppression.go, or extend the pin test to assert both files byte-equal.

**[Medium] Readability: `hasSuppressedOnly` names the wrong thing — pkg/decision/group.go:134, 178, 280**
- Problem: the flag is set when ANY attempt was suppressed (even alongside genuine attempts) and gates B1's "relay the last genuine failure instead of claiming exhaustion"; the name reads "the walk was suppression-only".
- Impact: `hasSuppressedOnly && lastFailure != nil` invites the wrong mental model and a plausible-but-wrong "simplification".
- Fix: rename to `anySuppressed` (or `sawSuppression`) with one line at :280: "a suppression means 'all members failed' would overclaim".

**[Medium] Readability: OQ-1 honest-attempt block re-implements the in-loop attempt tail, already diverging in event emission — pkg/decision/group.go:246-272 vs :205-238**
- Problem: two parallel attempt→classify→mark→fail-fast shapes in one function; in-loop attempts publish `member_attempted`/outcome events (:196-203, :215-222), the honest attempt publishes none — undocumented.
- Impact: a maintainer editing one copy (adding an event, a mark) misses the twin; the asymmetry looks accidental.
- Fix: extract a shared `attemptOne(memberID, cred, …)` for both sites, or at minimum a comment at :257 ("no per-attempt events here by design — only the terminal event").

**[Medium] Code Smells: dead loop in the vocabulary pin test — pkg/decision/vocabulary_pin_test.go:95-98**
- Problem: `for _, entry := range providerRegistry { _ = entry }` iterates and discards everything; the real collection is the second loop (:99-101).
- Impact: a reader hunts for a side effect that does not exist — noise in a test whose entire point is precision.
- Fix: delete the first loop.

**[Medium] File Hygiene: exported `Config` loses its doc comment to the inserted assertion — pkg/proxy/handler.go:76-80**
- Problem: the diff inserted `var _ groupLaneReader = …` directly under `// Config holds runtime configuration…`, so the comment block now attaches to the `var _`; `type Config struct` has no doc comment (gofmt-invisible).
- Impact: `pkg/proxy.Config` is a public type — its godoc disappears; regression introduced by this diff.
- Fix: move the assertion above the doc comment and separate with a blank line.

**[Medium] File Hygiene: duplicate package-level assertion + broken section banner — pkg/proxy/handler_anthropic.go:83-86 (handler.go:76-80 already covers the package)**
- Problem: `var _ groupLaneReader = (*modelscache.CachedModelsConfig)(nil)` declared a second time in the same package (legal via blank identifier — the compiler will never flag it), spliced *between* the top and bottom rules of the file's section banner.
- Impact: the section header no longer wraps its own title; the assertion reads as if it belongs to the Anthropic handler; a future reader cannot tell which of the two declarations is load-bearing.
- Fix: delete the `:83-86` declaration and restore the banner to an uninterrupted pair.

**[Medium] Readability: mangled godoc on `UnanimousGroupLane` — pkg/models/group_lane.go:89**
- Problem: missing newline splices two sentences: "…into the single lane// they share — the SINGLE-SOURCE-OF-TRUTH fold…". Only mangled comment in the diff; gofmt does not repair comment content.
- Impact: the godoc of the canonical fold function — the one the whole lane-derivation story rests on — renders as broken prose.
- Fix: insert the newline after `single lane`. (One-line fix.)

**[Medium] Type Cleanliness: unsound and unnecessary `as GroupModel` cast on the split buckets — pkg/ui/frontend/src/components/config/groupLane.ts:157-161, 185**
- Problem: `GroupModel = Model & { kind: 'group' }` with `Model.kind` optional; `const row = group as GroupModel` asserts `kind: 'group'` for rows of any kind. The cast is unnecessary: `inferGroupLane` accepts `GroupModel | Model`, and `Model` itself declares `members?`.
- Impact: the only type assertion in production FE code from this diff; it widens the bucket types beyond what input guarantees, and later code reading `kind` would trust it.
- Fix: type the buckets as the actual input (`chat: Model[]; decision: Model[]; unknown: Model[]`) and drop the cast — both consumers already accept `Model[]`.

**[Medium] Code Smells: nil guard placed after the dereference (dead branch) — pkg/decision/policy.go:58-62**
- Problem: `policy := groupPolicy{GroupID: group.ID, …}` dereferences `group`, then `if group == nil { return policy }` follows — unreachable as a guard; the intended "nil group ⇒ empty policy" contract is not implemented.
- Impact: dead branch that misdocuments the function's nil-safety; the nil path actually panics at the dereference (correctness consequence **deferred to Reviewer**, below).
- Fix: move the guard first: `if group == nil { return groupPolicy{Lane: lane} }` before constructing from `group.ID`.

### 🟢 Low (17 — defer list)

- **[Low] Coding Style** — hand-rolled nested lane scan vs repo's byID-map idiom — `pkg/proxy/handler_helpers.go:941-959` (`groupHasDecisionMember`; neighbors `policy.go:63-66`, `models/group_lane.go:141-144` build a map). Consider: build `byID` once, look up per member. *(O(groups×members×rows) noted as perf → deferred.)*
- **[Low] Readability** — stale file pointer in FE comment — `groupLane.ts:16` cites "pkg/models/config.go EffectiveKind"; it lives at `pkg/models/group_lane.go:111`. Fix the pointer.
- **[Low] Readability** — double lane gate with no "deliberate" marker — `pkg/decision/handler.go:835-841` + `pkg/decision/group.go:99-114`. Consider: one sentence at the second site ("re-checked: defence in depth at the branch entry") so nobody "deduplicates" it.
- **[Low] Readability** — inconsistent `ok=false` contract in `attemptSuppressed` — `pkg/decision/group.go:426` returns `(false, "all_credentials_unavailable")` while :432/:437 return `(false, "")`. Consider: `(false, "")` on the not-suppressed path, or a comment that the reason is diagnostic-only.
- **[Low] Code Smells** — mark-TTL clamp duplicated — `cmd/main.go:358-364` re-implements the `<=0 → skipcache.DefaultTTL` fallback `suppressionSeam.effectiveTTL` (`suppression.go:54-63`) already guarantees. Consider: return the raw config value; keep the clamp in the seam.
- **[Low] File Hygiene** — `groupLane.ts` export-surface cluster (**caller Q2 answer**): `normalizeKind` (:31-37) is an **acceptable executable guard, not a trap** — removal is loud (only importer is `__tests__/groupLane.test.ts:3` → suite breaks), in-fold misuse is loud (E8 reds twice) — but the invariant lives above the function, not in the file header where the next maintainer looks, and a future *new module* importing it is comment-defended only. Consider: state the invariant in the header ("exported for the E1 pin ONLY — zero production callers; deleting it requires deleting the two E1 assertions in the same commit; it must never decide a lane"), optionally rename to `legacyKindNormaliser`. Same cluster: `export type { GroupLane }` (:7) has zero importers (all three consumers import from `../../types`) — the header's "ONE module" claim is unfulfilled — delete the re-export or switch the consumers, not both; `ModelKind` (:12) re-declares the closed union already in `types.ts:122` — `NonNullable<Model['kind']>` or delete with `normalizeKind`.
- **[Low] Type Cleanliness** — `GroupLaneFromCollection` exported with an in-package-only caller and no direct test — `pkg/models/group_lane.go:137` (only caller `ValidateFallbackChainLanes` :176). Unexport it, or give it the direct mixed/vanished-member table test it deserves as the collection-shaped twin.
- **[Low] Error Handling** — unreachable mixed-lane fallback worded differently from the primary branch — `pkg/models/group.go:234`. Align the wording so the two can never diverge.
- **[Low] Error Handling** — silent `("", false)` on the GroupLane miss paths — `pkg/modelscache/models.go` (decorator forward) + `pkg/proxy/handler_helpers.go` (`groupLaneOf`): a dropped forward is invisible at runtime (compile-time assertion mitigates). Consider: one debug log on the miss path.
- **[Low] Error Handling** — unknown lane falls through to the chat arm silently — `pkg/store/database/store_group_rewrite.go:132-186` (`isServableGroupMemberBase` default case). Consider: an explicit unknown-lane branch that fails closed, or a comment that chat-default is intended.
- **[Low] Code Smells** — twin kind-change guards duplicate the membership scan — `pkg/ui/server.go:2766-2796` vs `pkg/store/database/store_kind_guard.go:25-53`. Mitigated (shared `KindChangeGroupMemberMessage`, parity-pinned, documented mirrors). Consider a shared models-level predicate only if a third site appears.
- **[Low] Coding Style** — counter-trio threading lengthens signatures — `pkg/decision/group.go:342 (6 params), :368 (7), :96 (7)`. Consider a small `walkAccounting{attempted, skipped, membersAttempted}` struct (folds naturally into the M6 fix).
- **[Low] Readability** — `client_attributable` breaks the outcome-vocabulary shape — `pkg/decision/group.go:695`: every sibling outcome is a what-happened phrase; this is a classifier-class adjective. Consider `request_rejected`, or a comment that the class name is deliberately the outcome.
- **[Low] Readability** — corpus shorthand tokens have no in-code glossary — `pkg/decision/handler_group_test.go` + FE decisionLane tests (RC-1, R-2(a)/(b), Q1/Q2, B1/B2, D-C, AM-4, AC-R10, E1-E8, D1-D10 resolve only from the planning corpus); plus the unexplained "(count stays at 74)" arithmetic at `handler_group_test.go:1304-1305` (34 top-level funcs in-file). Consider: a short code→meaning block at the top of each battery; reword the 74 comment to name the pack's test-name contract without the number.
- **[Low] File Hygiene** — unexplained B8 / D7 numbering holes — `GroupModelsTab.decisionLane.test.tsx` (B1-B7 then B9), `GroupModelForm.decisionLane.test.tsx` (D6+D6a-d then D8). D7 was evidently skipped because the legacy "D7 backward-compat" token (group-backup-members: `GroupModelForm.tsx:117`, `GroupMemberEditor.tsx:77`, + 2 test files) already owns that name. Do NOT renumber (corpus frozen) — add one header line per file naming the reserved slot.
- **[Low] File Hygiene** — six pre-existing 1000-3000-line files grown by this diff (+338 lines) carry no top-level "why this large" header — `pkg/ui/server.go` (2796, +160), `pkg/proxy/handler.go` (2018, +65), `pkg/proxy/handler_anthropic.go` (2826, +44), `pkg/store/database/store.go` (2794, +18), `pkg/models/config.go` (1429, +17), `pkg/modelscache/models.go` (1202, +34). Pre-existing debt (new test files correctly carry headers). Consider: a 3-line layout header on `pkg/ui/server.go` at minimum, or codify a threshold override in `.agents/tidier/rules/`.
- **[Low] File Hygiene** — 1914-line new test file with 4 banners and one 383-line function — `pkg/decision/handler_group_test.go` (34 top-level funcs + subtests; `TestDecision_Group_AllCredentialsCooling_PolicyFirstCredentialHonestAttempt` :1290-1673 spans 3 seed families). Discoverable by name prefix, density in line with siblings. Consider: split the suppression/cooling family into `handler_group_suppression_test.go` + add 5-7 banners.

*(Dropped as noise: robustness worker's fire-and-forget toggle observation in GroupModelsTab.tsx:280 — explicitly consistent with sibling components, no change recommended.)*

---

## Recommendations (prioritized for the Developer pass)

1. **Single-source the lane-validation messages + fold** (M1) and **lane-word the shadow-target not-exists arm** (M2) — one `pkg/models` helper pass fixes both and removes the already-present wording drift. Trivial, byte-compatible for the chat lane.
2. **Close the cross-seam mirror gap** (M3): one Go-side comment + (optional) source-literal FE pin — cheap insurance against the two-answers-must-not-ship hazard.
3. **Vocabulary hygiene trio** (M4 + M5): name `decisionMisrouteCode`, route all publishes through the helper, unify payload shape, resolve the silent arm. (M8 doctrine pin belongs here too — extend the pin to policy.go or paraphrase.)
4. **pkg/decision internals** (M6, M7, M9, M10, M11): counter semantics, populate `memberAttempt.err`, rename `hasSuppressedOnly`, extract/comment the honest-attempt twin, delete the dead loop. All local to group.go + two test files.
5. **Doc/mechanical repairs** (M12, M13, M14, M16): assertion placement ×2, one newline in group_lane.go:89, guard reorder in policy.go:58-62.
6. **FE type fix** (M15): drop the `as GroupModel` cast, retype the buckets — small, compile-checked.
7. Lows: defer or batch opportunistically; L-f (normalizeKind header invariant), L-n (glossary), L-o (B8/D7 headers) are the three with real future-reader payoff.

Estimated effort for 1–6: a single focused Developer session (all mechanical, no behavior redesign; chat-lane bytes preserved by construction where contracts pin them). Re-run `tsc`/`gofmt`/vet + the pin batteries after.

## Deferred to Reviewer

- **policy.go:58-62 nil-guard**: the craftsmanship fix (reorder) is in-scope above, but the *consequence* — nil group panics at `group.ID` rather than yielding an empty policy — is a correctness question (reachability of a nil group at the call sites) that belongs to the code-review lane; flagged here since the gate artifact predates this worker finding.
- **Chat decision-lane arm emits no `decision_misroute` event** (M5's silent arm): whether that is a telemetry *gap* (vs a deliberate omission) is a product/observability call.
- **`groupHasDecisionMember` O(groups×members×rows)** per request — perf, not craftsmanship.
- **doc.go rule-vs-list trade-off**: the amended doc names the banned subtrees instead of an exhaustive allowed list — intentional ("cannot go stale"), but a reader can no longer tell what *is* allowed; noted for the planner's corpus amendment, not a defect.
- **Double lane gate** (handler.go:835-841 + group.go:99-114) and the **twin optional-seam interfaces** (`groupLaneSeam` vs `groupLaneReader`): architecture observations, documented as deliberate.

## Process notes

- **Worker worktree-mutation incident (resolved, no residue):** the hygiene worker's early `go build` under `GOFLAGS=-mod=mod` reclassified two `// indirect` deps in `go.mod`; the worker reverted via `git checkout -- go.mod` and disclosed it. Dispatcher verification post-review: `git status --porcelain` shows only the pre-existing `.agents/approver/active.md` modification and pre-existing untracked `.agents/` paths; `go.mod`/`go.sum` diff is **empty**. Tree byte-identical to pre-review state. Flagged for awareness: workers should pin read-only GOFLAGS on report-only passes.
- **skill_feedback soft-fails ("no usage record")** on context-injected skills — the known repeat-dispatch attribution gap recorded in prior reviews (003/004); workers otherwise followed the contract.
- **Worker-3 Low-count discrepancy:** summary line said 7 Low, table carried 5 concrete rows; dispatcher reconciled to actual rows (one was a no-change-recommended observation, folded out).
- **KB accuracy note (leader's domain, no writes per mandate):** the operating-knowledge entry stating the frozen tsc baseline is "18 errors / 9 files (19 is stale)" is **inverted** vs the repository — `origin/latest`, `origin/master`, base `a2952a9`, and a measured run all say **TOTAL_ERRORS=19 / TOTAL_FILES=9**. Surfaced here for the leader to correct through the KB channel.
- `.agents/tidier/notes.md` (worktree) intentionally NOT updated this pass — the caller's DO-NOT-EDIT mandate outranks the tracking convention; this artifact is the record of review 006.

*Tidier dispatch, iteration 001/3. Aggregated from three worker reports; deduplicated by file:line:category; severity re-ranks (2 of them) carry stated justification inline.*
