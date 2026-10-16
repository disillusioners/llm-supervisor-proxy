# Decision Log: `group` Model Kind — Load-Balance Groups

> **Companion plan:** [`plan.md`](./plan.md) — implementation-ready plan with 14 sections + Appendix A (Files Changed) + Appendix B (Architect Round 1 Amendment Log). §13 ("Open Questions for the User") is the authoritative source for the recommended-default column this file mirrors.
>
> **Purpose of this file:** for each open question in `plan.md` §13 (Q1–Q20), the plan's recommended default + what the user can approve or override + consequence of each choice. Read this file once and reply with item-by-item approvals (or overrides) before implementation begins.
>
> **Provenance:** planner-dispatched (PLAN-ONLY commission; `wt.claim.lb-groups-plan` mode=`plan-only`, `push=false`, `merge=false`). Source of truth = `plan.md` §13 "Recommended default" column (verified against plan body §3–§12 + `research-codebase.md` §9.2 citations + `architecture-recommendation.md` §10 amendments + §12 decisions-pending). This log mirrors the table — same numbering (Q1–Q20), same meaning, same defaults. Zero source-code changes from this task. Next gate after user-approval: approver polish.
>
> **Amendment history (per `plan.md` Appendix B):**
> - **Architect Round 1 (2026-10-16) — amendments 1–20 folded into `plan.md` in place** (per `architecture-recommendation.md` §10, endorsed-with-amendments; none rejected). Q17–Q20 appended from `architecture-recommendation.md` §12 (defaults = arch recommendations). Q1–Q16 numbering/titles STABLE; amended entries marked with **(per Architect Round 1, Am N — `plan.md` Appendix B)** pointers. USER-DECIDED (locked) marks intact (Q3/Q4 + flat-in-Q7).
> - **Review Round 1 (2026-10-16): anchor/label fixes F2/N14/N15 applied.** F2: `ultimatemodel/handler.go:696` → `pkg/proxy/handler.go:696` (companion `:746-768` citation stays; the ultimate-exclusion gate lives in `pkg/proxy`, not `pkg/ultimatemodel`). N14: closing reply-string example now references Q13's actual override label `Override (\`Internal=false\` per \`research-codebase.md §9.2 item 13\`)`. N15: stray trailing backtick removed from Q14 rationale's `plan.md §2.3` citation. **Approval posture (carry into the user-facing read):** Q17–Q20 are **newly appended (Architect Round 1)**; **ALL Q1–Q20 remain pending user approval except the USER-DECIDED items (Q3, Q4, Q7-flat membership)** — the architect's amendments, the review's anchor/label fixes, and the "recommended default" wording above are all proposals awaiting user approval. No Q is settled unless marked USER-DECIDED (locked).
>
> **USER-DECIDED handling note (locked mid-flight, 2026-10-16):** items Q3, Q4, and the flat-membership aspect of Q7 record directives already settled by the user during scoping (shared meta-KV `lb-groups-plan.scope-guidance`). They appear here with the full field treatment — Question / Recommended default / Rationale / What changes if overridden — so the override path stays open and auditable. They are marked **USER-DECIDED (locked)** in their headings, not "open." Approving them is a formality unless the user explicitly re-opens. Cut items live in `plan.md` §14 "v2 Candidates," not re-litigated here.

---

## Format

Each item is structured as:
- **Question** — the open question from `plan.md` §13
- **Recommended default** — what the plan will implement if the user does not override
- **Rationale** — why this default (cites `plan.md` §N + file:line anchors; `research-codebase.md §N` allowed)
- **What changes if overridden** — concrete impact on the plan + which sections/files it affects. **Approve** = adopt the default. **Override (…)** = the cost priced (≈ lines of change, tests affected, plan sections touched).

---

## Q1 — Shadowing mechanism (ID-space claim)

**Question:** The commission says "name shadowing," but resolution is ID-keyed everywhere (§2.2; ~25 ID-keyed sites inventory). Pick the claim design, resolution order, `/v1/models` hiding, deletion/disable behavior, and auth-allow-list semantics.

**Recommended default:** **Single-ID claim** via a `shadow_model_id` column on the group row. Claims are checked first in `ModelsManager.GetModel` (`store.go:854`) for **enabled** groups; admin strict twins (`store.go:1191/:1197/:1205`) bypass claims so operators can still edit/delete a shadowed model by ID; `/v1/models` (`handler.go:245-272`) gains a second-pass shadow-hide (claimed IDs filtered out while the claiming group is enabled); group deletion or disable drops the claim → the shadowed model reappears; **auth is unchanged** (`IsModelAllowed` `pkg/auth/token.go:133-143` evaluates the client-requested ID string — fail-closed exactly as today). Alias arrays (multiple shadowed IDs per group) → v2 candidate (§14). **(per Architect Round 1, Am4 — `plan.md` Appendix B)** the **`pkg/modelscache` claim-eviction hook** (`modelsByID` + `negByID`, evicting both old and new shadow IDs) is part of the claim design; the `OnModelChanged` extension runs the hook synchronously before any subsequent resolve can read a stale positive or 60s negative entry. **(per Architect Round 1, Am20 — `plan.md` Appendix B)** the **dual-claim edge** is documented: two *enabled* groups claiming one ID is blocked by G8's uniqueness scan at write time; two *disabled* claims are inert (no index entry — claims activate only on enabled groups); enable-ordering resolves through the same scan.

**Rationale:** ID-space is the only space the codebase resolves on (`plan.md` §2.2 — verified sites: `handler_functions.go:128`, `handler.go:338`, `handler_anthropic.go:158/:418/:209-219`, `race_executor.go:55/:121/:177/:275`, `race_coordinator.go:1409/:1521`, `/v1/models` `handler.go:245-272`, `ModelsManager.GetModel store.go:854` + `GetModelByName :936` + strict twins `:1191/:1197/:1205`, `ResolveInternalConfig(WithAffinity) store.go:2079-2097/:2170-2267`, JSON twin `config.go:1012-1064`, auth gates `handler.go:565/:647`, ultimate `ultimatemodel/handler.go:746-768`, modelscache `pkg/modelscache/models.go:400/:427/:454`). One column + one index + one G-rule set + zero array scanning matches the minimalism directive. Strict-twins bypass is mandatory — otherwise an operator could not edit/delete a shadowed model by ID. `research-codebase.md §9.2 item 1` proposed an `aliases` JSON array; the plan rejects that for v1 (single-ID is the minimal correct shape; aliases move to §14). **Am4 evidence** — `pkg/modelscache` writes **negative entries** under `ModelsNegTTL` (`models.go:375`, 60s default at `:55-56`); the existing `AddModel`/`UpdateModel`/`RemoveModel` (`models.go:643/:665/:687`) evict only by the row's own ID — so a stale positive cache hit or a 60s negative cache hit for a shadowed ID would bypass the manager's claim logic entirely. The hook is the only correct fix; it lives in the same `OnModelChanged` extension as the claim-index rebuild (P2.2). **Am20 evidence** — the single-valued `claims map[shadowedID]groupID` (`plan.md` §3.4) only carries entries for enabled groups, so the dual-claim edge reduces to "disabled+disabled = inert" (no index entry) and "enabled+enabled = blocked by G8 scan" (cannot be created via API); the scan is the safety net, the map is the runtime, both backends, one design.

**What changes if overridden:**
- **Approve**: implement as recommended. Migration 031 (`plan.md` §7.1) adds `shadow_model_id TEXT NULL`; `ModelsManager` gains `claims map[shadowedID]groupID` rebuilt via `OnModelChanged`; `GetModel` kind/claim branch; `/v1/models` second-pass filter; cache invalidation extended to old + new shadowed ID on claim change. ~60 LOC across 6 sites + tests in §9.3.
- **Override (alias arrays — `aliases TEXT NOT NULL DEFAULT '[]'` JSON)**: drops to `research-codebase.md §9.2 item 1`'s proposed shape. G8 uniqueness scan loops over the array; `GetModel` claim index becomes `map[string][]string` and the first claim wins (or first enabled, documented ordering); `/v1/models` second-pass hides every claimed ID; modelscache invalidation invalidates every ID in old + new alias set. ~120 LOC across 7 sites + 2 extra unit tests. No risk to the recommended path; pure additive. Use if operators have signaled "I want one virtual name to cover 3 deprecated chat models."
- **Override (sibling `model_aliases` table)**: migration 031 becomes a 2-table affair (group row + alias rows, FK on `group_id`). Hand-built integrity in a zero-FK schema; second JSON-backend implementation; cache invalidation grows a second trigger. **Rejected** — house-style violation (`plan.md` §3.3) and disproportionate cost for the v1 feature.

---

## Q2 — Membership shape (inline pairs)

**Question:** Three candidate shapes: (a) inline `(credential_ref, upstream_model, weight)` pairs, (b) references to existing chat model rows, (c) hybrid (pair plus optional per-member overrides).

**Recommended default:** **(a) inline pairs** — `[]GroupMember{CredentialID, UpstreamModel, Weight}` stored as a JSON column (§3.3, Q8). Members never appear in `/v1/models`; member-visibility dissolves. Member-key = `credentialID + "|" + upstreamModel` (`plan.md` §3.2 + Q14). (b) and (c) → v2 candidate (§14).

**Rationale:** (i) Matches the user's originally-suggested shape; (ii) dodges the resolver-seam blocker — `resolveWithCredential` (`store.go:2107-2126`, `actualModel = modelConfig.InternalModel` at `:2118`) hard-assumes one model row = one upstream model string, and member-rows would force nested resolution or a resolver fork; (iii) keeps ONE LB level (no group → member-model → credential hierarchy); (iv) the member key fixes the credentialID-only conflation (`§2.3` — known limitation: bindings/cooldowns key on credentialID; a 429 on (provider X, model A) currently cools the entire credential backing (X, B)) at the engine, not with special cases (`research-codebase.md §9.2 item 7` folded in). Members being rows is rejected because (b) would force visibility in `/v1/models` for every member model — a UX disaster for "I want a virtual name to fan out across 5 internal models without those 5 leaking into client listings."

**What changes if overridden:**
- **Approve**: implement as recommended. `pkg/models/group.go` (new) defines `GroupMember`; migration 031 adds `members_json TEXT NULL`; `validateGroupMembers` runs from both write paths (`config.go:971` JSON + `store.go:1719` DB) and replaces `validateModelAgainstCredentials` for group rows; G3/G4 enforce reference integrity + dup pair uniqueness.
- **Override (member = model row reference)** — (b): add `member_model_id` foreign-key-style field per row; `validateGroupMembers` checks each `member_model_id` exists, is chat-kind, and is enabled; the resolver kind-branch loops over member rows and re-enters `ResolveInternalConfigWithAffinity` per member (potential recursion if a member is itself a group — requires depth guard, see Q16). ~300 LOC across 8 sites + a recursion-depth test; forces `/v1/models` member visibility (or explicit `member_visibility=false` per row to suppress — extra surface). Use only if operators demand per-member overrides (timeout, retry, peak hours) — currently a §14 cut.
- **Override (hybrid — inline pairs + optional `member_model_id` override)** — (c): every member carries either a pair OR a row reference; `validateGroupMembers` accepts both shapes; the resolver branch checks `member_model_id` first and falls back to the inline pair. ~400 LOC; schema-validation complexity rises; defer to v2 unless product signals demand.

---

## Q3 — LB strategy — USER-DECIDED (locked)

> **USER-DECIDED (locked)**: directive already settled mid-flight (shared meta-KV `lb-groups-plan.scope-guidance`, 2026-10-16). "minimalism across the board: cut health-tracking sophistication, weighted strategies beyond what is trivial/free, exotic validation rules." Approving this row is a formality unless the user explicitly re-opens.

**Question:** What LB strategy does `pkg/grouplb` expose in v1 — clone `pkg/credentiallb` (weighted-random sticky + cooldowns free), or add weighted/round-robin/least-latency/health-tracking variants?

**Recommended default:** **Clone `pkg/credentiallb` verbatim.** The clone inherits weighted-random sticky + cooldowns + 24h sliding TTL + 5-min janitor + 60s default cooldown + the E-1 two-level locking pattern + the n==1 fast path (`engine.go:241-246`). **No new strategies in v1.** Round-robin / least-latency / health-tracking / per-strategy weight ceilings → v2 candidates (§14).

**Rationale:** The minimalism directive is binding; the v1 core is "group CRUD + multi-(provider,model) LB + name shadowing + sticky routing + FE Group tab + streaming pass-through" (shared meta-KV). The engine clone is ~80% mechanical (`plan.md` §3.5 mapping table — 7 known couplings with explicit resolutions); the free weighted-random selector (`selector.go:16-65`) is well-tested and already the proxy's existing credential-LB strategy. Adding a new strategy requires a comparable test surface + a strategy-switch plumbing on every binding/cooldown site + per-strategy metrics — costs that the directive explicitly cuts. `research-codebase.md §9.2 item 6` (per-group vs shared engine) is folded into the clone via the per-group `models map[modelID]*modelState` pattern from the source — no separate decision.

**What changes if overridden:**
- **Approve**: implement as recommended. `pkg/grouplb/{doc.go,engine.go,selector.go,binding.go,key.go}` (5 new files) cloned per `plan.md` §3.5; member-key `credentialID + "|" + upstreamModel` (`plan.md` §3.2 + Q14). ~700 LOC + ~600 LOC tests (`plan.md` §9.1).
- **Override (add round-robin + least-latency + weighted-strategy-config)**: ~300 extra LOC in `selector.go` + a `Strategy` enum threaded through `Engine.GetOrSelect`; per-strategy tests + a config quartet (mirrors `DecisionTimeout` precedent). Defers the v1 ship; risk concentrated in the engine's locking invariants.
- **Override (add active health-tracking / success-rate scoring / auto-ejection)**: ~500 extra LOC + a probe goroutine + per-strategy metrics; explicitly cut from the minimalism directive; would require its own commission.

---

## Q4 — Sticky affinity — USER-DECIDED (locked)

> **USER-DECIDED (locked)**: directive already settled mid-flight (shared meta-KV `lb-groups-plan.scope-guidance`, 2026-10-16). "AFFINITY/STICKY STORE = IN-MEMORY for v1 (message-hash memory NOT persisted; document restart-cold-start instead of engineering around it)." Approving this row is a formality unless the user explicitly re-opens.

**Question:** What is hashed for affinity, where state lives, TTL, restart behavior, sticky-target failure handling, member-removal handling.

**Recommended default:** **`ComputeConversationKey(groupID, tokenID, firstUserMessage)`** verbatim (`pkg/credentiallb/key.go:40-43`; multimodal via sorted-key canonical JSON `:57-157`). State lives **in-memory only** (`grouplb.Engine.bindings` map per group). **24h sliding TTL** + **5-minute janitor** + **60s default cooldown** — all inherited from the cloned engine. **Restart = cold start** documented as a single startup log line (`plan.md` §3.5 + R-related; §9.1). Sticky-target failure (rate-limit / cred-failover on the winner member) ⇒ **`ExcludeAndReselect(groupID, convKey, memberKey)`** with re-stick to the new pick (cloned from `engine.go:609-719`: cooldown from `Retry-After` else 60s; n==1 ⇒ reselect-none; rebind-only-if-current-matches; all-cooling ⇒ soonest-expiry). Member removal ⇒ drop that member's bindings via `OnGroupChanged` (next request re-sticks to a fresh pick — empty key ⇒ fresh pick per request, cloned semantics). DB persistence of sticky affinity → v2 candidate (§14).

**Rationale:** The directive is binding; the function already accepts the model ID as a parameter (`key.go:40-43` — verified), so the chat-side call site (`handler.go:534-548` for OpenAI; `handler_anthropic.go:296` for Anthropic) needs zero change — `rc.resolvedModel.ID` is the group ID by the time affinity is computed. In-memory matches `pkg/credentiallb`'s existing posture (no DB today; `RebindFromStore` seeds selectors, not bindings). The cold-start log line (`research-codebase.md §9.2 item 10` folded in) is the documented behavior — a config toggle was explicitly rejected. Empty-key semantics (no binding, fresh pick) match the source verbatim. **Verified unchanged in Architect Round 1 (2026-10-16, `plan.md` Appendix B)** — the round endorsed Q4's sticky-affinity design without amendment; the lazy per-group state (Am15 — no boot `RebindFromStore` walk) and the cloned `testhooks.go` seams (Am14) are the only nearby engine changes and they leave Q4's semantics as stated above.

**What changes if overridden:**
- **Approve**: implement as recommended. `pkg/grouplb/key.go` re-exports `ComputeConversationKey` (or imports `pkg/credentiallb/key.go` directly); engine binds snapshots in `bindings map[convKey]*binding` per group; `OnGroupChanged` drops removed-member bindings; `cmd/main.go` logs the cold-start line at boot (engine lifecycle owned by `ModelsManager`). Zero net code change at the affinity-hash site (chat call sites unchanged).
- **Override (DB-backed sticky memory for warm restart)**: ~150 extra LOC + migration 032 (`affinity_bindings` table keyed by `(group_id, conv_hash, member_key, created_at, expires_at)` + a janitor) + write-side serialization on every resolve. Defers v1 ship; the minimalism directive explicitly cuts this.
- **Override (per-request warning log line on cold-start)**: ~5 LOC at the `GetOrSelect` site. Spammy in production; documented posture is "one startup log line, no per-resolve warning."

---

## Q5 — OpenAI response `model` field (mask vs leak)

**Question:** Upstream echoes the `model` field verbatim on the OpenAI path (`handler.go:1172-1184` stream relay; `handler.go:1587` non-stream `finalBody`); a multi-target group would leak varying upstream model names. Anthropic already masks to the client name (`translator/response.go:38` non-stream; `incremental_stream.go:458` message_start). Mask or accept?

**Recommended default:** **Mask to the client-requested name** — non-stream (top-level `model` key of `finalBody` at `handler.go:1587`, group-kind-only via bounded top-level-key JSON patch `maskModelField`) and **stream (per complete SSE `data:` event, NOT per raw read-buffer chunk — per Architect Round 1, Am6 — `plan.md` Appendix B)**. Stream chunk-straddling ⇒ intermittent leak on a per-chunk patch (`handler.go:1172-1184` relays raw read-buffer chunks via `buffer.GetChunksFrom`); buffering per complete SSE event (reusing the existing per-event parsing machinery adjacent at `:1182`) eliminates the straddling case. Patch failure ⇒ verbatim write (log + continue), fail-open retained; never drops the stream. Anthropic: parity achieved with zero code change (already masks at `translator/response.go:38` non-stream; `incremental_stream.go:458` message_start). Group-kind-only (`if rc.resolvedModel.IsGroup()` gate at the call site) ⇒ zero cost and zero risk for existing traffic. Diagnostic verbatim toggle → v2 candidate (§14).

**Rationale:** `plan.md` §2.5 enumerates the asymmetry: the response `model` field on OpenAI leaks varying upstream names; `RequestMetadata{ClientModel, UpstreamModel}` exists (`adapter_openai.go:53-58`) but is populated **only by `ParseRequest`** (per Architect Round 1, Am7 — `plan.md` Appendix B — **zero production callers today**: the `RequestMetadata` seam is dead in main; adapter-seam masking priced out as out-of-scope), so the masking lives at the `handler.go` relay/parse sites, not in the adapter. `WriteStreamEvent` is pure passthrough (`adapter_openai.go:100-106`). The transparency goal (`plan.md` §1: "client names a model … and receives normal OpenAI-format chat responses — transparent virtualization") requires single-model appearance. Anthropic masking is the parity target. The bounded patch keeps the patch fail-open-to-verbatim (a chunk that fails to parse still reaches the client — never drops a stream) and costs nothing for non-group traffic. `research-codebase.md §9.2 item 5` recommended mask; **answered (recommended default)** — `plan.md` recommends the mask posture, with the per-SSE-event buffered patch from Am6. **Am6 (per `plan.md` Appendix B) drives the buffering shape**: chunk-straddling on the raw read-buffer path means a JSON object can split across two `buffer.GetChunksFrom` reads — patching per chunk falls open to verbatim on straddled events, leaking the upstream `model` field intermittently. Buffering per complete SSE event reuses the existing per-event parser (the parser already reconstructs the event boundary to extract JSON) and patch failures remain fail-open.

**What changes if overridden:**
- **Approve (per-SSE-event buffered patch — per Architect Round 1, Am6)**: implement as recommended. `pkg/proxy/handler.go` gains `maskModelField(body []byte, clientName string) []byte` (bounded top-level-key patch — string substitution for `"model":"<orig>"` → `"model":"<client>"`); called at `:1587` (non-stream, after body parse) and **inside the relay loop at `:1172-1184` per complete SSE `data:` event** (NOT per raw chunk); `handler_groups_test.go` asserts stream + non-stream carry the client-requested name AND non-group responses are byte-unchanged (regression assert) AND straddled-event fixtures pass. ~80 LOC + 2-3 tests.
- **Override (accept the leak — verbatim passthrough always)**: no code change to masking; clients see varying upstream names. Breaks transparency for any client that pins the `model` field; a "fallback" client gets confusing telemetry when a group rotates members. Save ~80 LOC + 2-3 tests; lose the transparency contract.
- **Override (per raw chunk patch — Am6 rejected shape)**: a JSON object straddling two `buffer.GetChunksFrom` chunks leaks the upstream `model` field on the straddled event. Intermittent and unreproducible in normal tests; rejected explicitly by Am6 (`plan.md` Appendix B). Defer to v2 only if a future product signal demands it.
- **Override (diagnostic verbatim toggle + operator env var)**: add a config quartet for `GroupModelFieldPassthrough` (default `false`); operators flip on for debug. ~30 extra LOC + 1 config surface + 1 test. Defer to v2 (the toggle is a nice-to-have, not a v1 requirement).

---

## Q6 — Ultimate switching posture for groups

**Question:** What is the group's posture vis-à-vis the ultimate-model switcher (`ultimatemodel/handler.go`)?

**Recommended default:** **Force `ExcludeFromUltimateSwitching == true`** via **G7** (D9 precedent `config.go:283-285`), enforced at write time in (a) the shared seam `models.ValidateKindRules`, (b) the UI POST mirror `pkg/ui/server.go:~1066` + PUT mirror `~:1333`, AND (c) the DB write path (`validateBeforeWrite` `store.go:1736-1742`). The runtime gate (`pkg/proxy/handler.go:696` checks only the flag) then never selects a group as the re-execution target; the kind-blind internal dispatch (`:746-768`, on `Internal`) never sees one. `X-Force-Ultimate-Model` bypass residual (`plan.md` R9): an operator forcing ultimate onto a group ID gets the external-passthrough path with an empty `UpstreamURL` — fails fast with the normal error envelope; documented, accepted.

**Rationale:** Decision commission precedent (`config.go:283-285`) is the working pattern. Image-gen has a doc/code drift on this same rule (D9 analog) — `research-codebase.md §4 surprise #4` — and the plan explicitly does NOT inherit that drift; group G7 is enforced at all three sites (parity test locked, §5.3). Groups have no meaningful "ultimate" semantic (the re-execution target would itself be a chat model — not a group; choosing one of a group's members as ultimate collapses the LB). Forcing the flag is the only safe posture.

**What changes if overridden:**
- **Approve**: implement as recommended. 3 enforcement sites mirror G7; `pkg/ui/kind_rules_parity_test.go` G-case matrix locks parity (`plan.md` §5.3). ~30 LOC + parity test row.
- **Override (no enforcement — allow either value, like image-gen)**: matches image-gen's posture (and its drift). A group with `ExcludeFromUltimateSwitching=false` would be selected as ultimate by the runtime gate; the kind-blind internal dispatch would then external-passthrough an empty `UpstreamURL` — fails fast with the normal error. Less safe; explicitly rejected.
- **Override (enforcement deferred until ultimate-model understands kinds)**: out-of-scope creep; the existing runtime gate is one line + the kind-blind dispatch needs only `Internal=true` to be safe (G1 enforces). The deferred path doesn't buy anything for v1.

---

## Q7 — Validation rule set G1–G8 (including flat membership — USER-DECIDED)

**Question:** Minimal G-rule set; member provider-span vs the credential provider-match invariant (`store.go:1631-1644`).

**Recommended default:** **G1–G8 per `plan.md` §5.1:**

| # | Rule |
|---|---|
| G1 | `Internal == true` — routes through internal-dispatch predicates (§2.4; Q13) |
| G2 | `len(Members) >= 1`; member count cap 16 (reuses `MaxCredentialRefs`, `config.go:84`) |
| G3 | Each member: `CredentialID` references an existing credential **AND contains no `|`** (member-key collision gate, defense-in-depth behind the `CredentialConfig.Validate` ban — **per Architect Round 1, Am2 — `plan.md` Appendix B**); `UpstreamModel` non-empty; `Weight > 0` (mirrors `store.go:1615-1617`) |
| G4 | No duplicate members (same `CredentialID + UpstreamModel` pair) — member-key uniqueness (§3.2) |
| G5 | Members **MAY span providers** — `validateModelAgainstCredentials`' provider-match invariant (`store.go:1631-1644`) does NOT apply to groups. **Flat membership (no group members) is USER-DECIDED (locked)** — see below. |
| G6 | `FallbackChain` empty; `SecondaryUpstreamModel == ""`; `PeakHourEnabled == false`; `ReleaseStreamChunkDeadline == 0` — makes group→group fallback impossible (§14, `research-codebase.md §9.2 item 14`) |
| G7 | `ExcludeFromUltimateSwitching == true` — Q6 + D9 precedent (`config.go:283-285`); explicit, not defaulted |
| G8 | If `ShadowModelID != ""`: target exists, is **chat-kind**, is not `KindGroup` (flat — USER-DECIDED), is not the group's own ID, and no other enabled group claims the same ID (uniqueness scan) |

> **USER-DECIDED (locked)** — the flat-membership aspect of G5: "NO group-of-groups — flat membership only" (shared meta-KV, 2026-10-16). G8's "claim target is not `KindGroup`" is the flat-membership G-rule, not a per-row check. Approving this row is a formality unless the user explicitly re-opens. Group→group fallback is impossible because G6 denies the chain (§14 + `research-codebase.md §9.2 item 14` deferred to v2).

**Rationale:** The minimalism directive ("cut exotic validation rules" — shared meta-KV) sets the budget; G1–G8 are the load-bearing rules, no more. G1's `Internal=true` rejection of `research-codebase.md §9.2 item 13`'s `false` proposal is on evidence (`§2.4` lists ~11 `.Internal` dispatch predicates + the resolver gate `store.go:2172` — every one would need an `|| m.Kind == KindGroup` extension with `false`). G5's deliberate divergence from the provider-match invariant is the feature itself: groups are *designed* to span providers (e.g., OpenAI primary + Anthropic secondary for cross-vendor load balancing). G2's ≥1 (not ≥2) keeps one-member groups valid as a staging/transition state; paternalism rejected (`config.go:84` cap 16 reused to avoid a new constant). G6 makes the group→group recursion question moot (Q16). G8 uniqueness scan is application-level (no DB unique index — `plan.md` §7.2 A11); sufficient for v1 member/claim volumes. **G3's `|` ban (per Architect Round 1, Am2 — `plan.md` Appendix B) is the member-key collision gate**: the member key `credentialID + "|" + upstreamModel` is ambiguous if a credential ID itself contains `|` — `("foo","bar|baz")` and `("foo|bar","baz")` both yield `"foo|bar|baz"` ⇒ silent wrong-binding. `CredentialConfig.Validate` (`pkg/models/credential.go:107`, re-verified: non-empty checks only) gains the `|` ban; G3 re-checks it defense-in-depth so the rule holds for both write paths (DB + JSON) and survives a hand-edited config file. The rule is cheap (one regex/contains check) and load-bearing — every member-key map read depends on it.

**What changes if overridden:**
- **Approve (G1–G8 flat, including Am2 `|` ban in G3)**: implement as recommended. `pkg/models/group.go` (new) defines `validateGroupRules` + `validateGroupMembers`; runs from `models.ValidateKindRules` (`config.go:971` JSON path + `store.go:1719` DB path); UI POST/PUT mirrors + G8 uniqueness scan + G7 write-gate; parity-test G-case matrix. **`pkg/models/credential.go` `CredentialConfig.Validate` gains a `|` ban** (re-verified `:107` currently only checks non-empty). ~270 LOC + ~160 LOC tests.
- **Override (allow group→group via nested membership)** — flat membership USER-DECIDED re-opened: G5 allows `KindGroup` rows as references; G6 is relaxed to permit `FallbackChain` to reference other groups; `resolveWithCredential` recursion becomes load-bearing (depth guard at, e.g., 3). ~150 extra LOC + a recursion-depth test + the v1 minimalism directive is broken. Defer to v2 unless product signals.
- **Override (G2 ≥2 instead of ≥1)**: rejects the one-member staging state. ~2 lines changed; breaks operators who set up a group, configure member 1, validate, then add member 2 (today's workflow).
- **Override (drop the Am2 `|` ban — accept silent wrong-binding)**: `("foo","bar|baz")` and `("foo|bar","baz")` both yield `"foo|bar|baz"`; a 429 on (provider X, model A) cools the wrong member's binding under the other member's identity. Operator-actionable confusion. Rejected.
- **Override (no G8 uniqueness scan — first-claim-wins)**: drops the cross-group uniqueness check. Two enabled groups claiming the same ID would race on first-write-wins; documented behavior shift; rejects the plan's "ambiguity-proof" commitment.

---

## Q8 — Storage (JSON membership column vs separate table)

**Question:** Storage profile: (a) JSON membership column + `shadow_model_id` column, or (b) separate `group_members` table?

**Recommended default:** **(a) JSON column** — migration 031 adds `members_json TEXT NULL` + `shadow_model_id TEXT NULL` to `models` in both dialects (`plan.md` §3.3 + §7.1). ~30 mechanical precedent-covered touch sites (SELECT/scan/write/`dbModelRow` +2 cols everywhere, `querybuilder_test.go` column pins 19→21, registry append at `migrate.go:64`). Keeps `OnModelChanged` and modelscache invalidation **untouched** (single-row storage; no second trigger).

**Rationale:** The table profile rejected on six counts (`plan.md` §3.3 + §7.2): (i) ~6+ new querybuilder strings × 2 dialects; (ii) new scan loop; (iii) manager CRUD + interface growth; (iv) hand-built integrity in a zero-FK schema; (v) a SECOND JSON implementation for the JSON backend; (vi) a new cache-invalidation trigger + house-style violation (`credentials_json` migration 028 is the in-file precedent for the column shape). Option (a) keeps the table profile flat and inherits every precedent. `research-codebase.md §9.2 item 4` recommended (a); **answered (recommended default)** — `plan.md` recommends the JSON-column shape, with ~30 mechanical touch-sites per §3.3.

**What changes if overridden:**
- **Approve**: implement as recommended. 4 SQL files (sqlite/postgres × up/down); registry append; `dbModelRow` +2 fields; INSERT/UPDATE 19→21 args; SELECT coalesce +2; `querybuilder_test.go` column-count pins 19→21; `schema_migrations`-contains-`"031"` test. ~80 LOC across 8 sites + 1 registry test.
- **Override (separate `group_members` table)**: new table + scan loop + manager CRUD + JSON two-handler interface + cache-invalidation trigger. **Rejected** — disproportionate cost for v1; explicit house-style violation.

---

## Q9 — Metering attribution (group + member rows)

**Question:** Per-target member vs group name vs both; timeout pattern at the 3 chat-metering sites.

**Recommended default:** **Both rows** at the **3 corrected** success-only sites — **`handler.go:908-934` ultimate branch** / **`handler.go:1446-1472` true stream** / **`handler.go:1656-1682` non-stream** (anchors corrected **per Architect Round 1, Am11 — `plan.md` Appendix B**; the original `:917-921/:1455-1475/:1665-1685` were within-block approximations, the corrected anchors are the actual metering call sites) — for `kind==group` requests: (1) **existing group-name row** — `IncrementModelUsage(clientRequestedName)` unchanged behavior (dashboard continuity; `rc.reqLog.Model` already records it), written at ALL three sites **including the ultimate re-execution branch**: (2) **new member row** — `IncrementModelUsage(servedMember.InternalModel)` the actually-served member target (the user's presumption), **EXCLUDED from the ultimate site (`:908-934`) per Am11** — the ultimate target was served, not a group member; group-name row only there, documented. Same-named upstream models across providers blend in `model_hourly_usage`; **cross-feature blend extended per Am19** — a member's `upstream_model` may collide with an operator model ID, accepted as cosmetic; provider-qualified usage keys → v2 candidate (§14). **Plumbing (per Architect Round 1, Am3 — `plan.md` Appendix B)**: winner-based gating via a **lock-protected `GetResolved()` getter on `upstreamRequest`** (house W4 discipline) **OR** an **rc-stamp of the winner's `resolved.InternalModel` + member key post-`WaitForWinner`** (the as-written site-scope `resolved` does NOT exist at any of the 3 metering sites — re-verified: blocks read only `rc.reqLog.Model` / `rc.tokenID` / `rc.reqLog.Usage`; `ResolvedCredential` is per-attempt in `race_executor.go:303` lock-free write `:310-311` and `winner.GetModelID()` returns the group row ID). The member row is gated on **winner-row-kind == group && resolvedOK** — which meters fallback-reached groups correctly for free (Q20). Both writers run under a **detached context with a 5s timeout**, adopting the decision pattern (`decision/handler.go:89-90`) on sites we already touch — chat's bare `context.Background()` (no timeout) is upgraded in the same edit (bounded behavior change, `plan.md` R10; touches ALL chat traffic, not just groups — A12). Token-level `Increment` unchanged. `writeMu` exclusivity holds (two `IncrementModelUsage` calls are serialized UPSERTs on distinct `model_id` rows). **`/v1/messages` meters nothing (per Architect Round 1, Am13 — `plan.md` Appendix B)** — pre-existing Anthropic-path gap, zero `counter.` refs in `handler_anthropic.go`, explicitly unchanged.

**Rationale:** Operators expect a group's dashboard row to show *something* (the group name keeps today's continuity); they also expect to see per-target usage for cost attribution (the member row). Skipping the per-target row would surprise operators on cost review. Skipping the group row would surprise operators on traffic review. The 5s detached timeout is the decision-commission's house pattern (replacing chat's bare `context.Background()` is bounded — `plan.md` R10 — and lands in the same edit). **`research-codebase.md §9.2 item 11` is folded separately into Q15 (events), not Q9.** **Am3 evidence (`plan.md` Appendix B)**: the first-draft `resolved.InternalModel` reference was a plumbing artifact — `ResolvedCredential` does not exist at the 3 metering sites; the lock-free per-attempt resolution at `race_executor.go:303` (write `:310-311`) and `winner.GetModelID()` returning the group row ID together mean the served-member identity must be propagated via either a `GetResolved()` getter on `upstreamRequest` (W4 discipline — `pkg/auth` precedent) or an `rc`-stamp post-`WaitForWinner`. Without that plumbing, the per-target row is unreachable. **Am11 evidence**: the ultimate re-execution branch (`:908-934`) was correctly identified as group-name-row-only — the ultimate target is served, not a member; mixing a member row at the ultimate site would mis-attribute ultimate-replay traffic to a group member that did not actually serve it. **Am13 evidence**: `/v1/messages` has zero metering refs in `handler_anthropic.go`; the group path on that route writes no usage rows. This is unchanged by the commission (pre-existing Anthropic-path gap). **Am19 evidence**: the cross-feature blend (member `upstream_model` colliding with an operator model ID in `model_hourly_usage`) is a cosmetic blend in one usage row; accepted as a v2 disambiguation candidate rather than a v1 schema split.

**What changes if overridden:**
- **Approve (both rows + 5s detached timeout + winner-based plumbing per Am3 + ultimate-site exclusion per Am11)**: implement as recommended. **~15-25 LOC** for the plumbing (`GetResolved()` on `upstreamRequest` OR `rc` stamp post-`WaitForWinner`, lock-protected) + 3 sites each gain the conditional `IncrementModelUsage(servedMember.InternalModel)` (group-kind-only, EXCLUDED at `:908-934`) and replace `context.Background()` with `context.WithTimeout(context.Background(), 5*time.Second)`. `model_member_selected` event at the W-1 publish site (Q15). P2.9 acceptance pins fallback-reached-group member metering (Q20). ~30 LOC + 2 unit tests (the writers are exercised at existing test sites).
- **Override (group-name only — skip per-target row)**: ~15 LOC; loses operator cost attribution. The plan rejects this — it's the surprise class that the decision commission deliberately avoided.
- **Override (per-target only — skip group-name row)**: ~15 LOC; loses dashboard continuity. Today's chat handler already metered the client name; matching that for groups is what makes the dashboard "feel the same."
- **Override (include the ultimate site `:908-934` in the member row — ignore Am11)**: the ultimate re-execution target is served, not a member; attributing ultimate traffic to a member mis-reports cost. Reject.
- **Override (provider-qualified usage keys — disambiguate same-named upstreams)**: schema change (`model_hourly_usage` partition by provider) + migration 032 + 4 surface changes. Defer to v2 (A7 — accepted blend for v1; Am19 cosmetic blend accepted for v1).
- **Override (don't adopt 5s timeout — leave bare `context.Background()`)**: ~5 fewer LOC; keeps the existing chat-metering posture. R10 documents the upgrade as in-scope; rejecting this override leaves the bounded-wait improvement on the table for no concrete benefit.

---

## Q10 — FE specifics (tab + member editor + cache discipline)

**Question:** Tab identity, member editor, cache key discipline.

**Recommended default:**
- **Tab** — 11th tab in `SettingsPage.tsx` (`TabType` extended at `:52` with `'group_models'`); label **"Group Models"**; glyph **🔀** (shuffle/branching = load balancing; mirrors 🖼️ / 🎯); `data-tab-id="group_models"`; `aria-label` per house pattern. Mirrors `DecisionModelForm.tsx` and `DecisionModelsTab.tsx`.
- **`GroupMemberEditor.tsx`** — new on the `MultiCredentialEditor.tsx` (225L) pattern: rows of `{credential dropdown, upstream-model text input, weight number input, remove}`, row add defaults, MIN 1, MAX 16. **Cross-provider**: `isOptionDisabled` lifts the same-provider constraint (`:83-93`) and **keeps only the duplicate-pair check** (`:76-81`). Provider badge per row sourced from the credential list (no BE round-trip). **Duplicate-`upstream_model` hint (per Architect Round 1, Am12-FE — `plan.md` Appendix B)**: a non-blocking inline hint when two rows share an `upstream_model` (~10 LOC) — that is the legal **N-members-per-target** pattern (`plan.md` §3.2; Q18), so warn, don't block. **N-members ops note (per Am12)**: a multi-credential target is expressed as N member rows (one per credential, same `upstream_model`) — legal and cooldown-correct (distinct member keys; weights compose arithmetically as credential weights; prefix-sum works). Documented so operators don't read near-identical rows as a validation accident; rotation note — credential rotation changes the member key, so `OnGroupChanged` drops the affected sticky bindings by design (runbook sentence, not a defect; `plan.md` §3.5). Rows-in-form layout (research-codebase.md §9.2 item 12 noted the pill alternative; rows chosen because members have 3 fields). Free-text `upstream_model` is v1-acceptable (DecisionModelForm precedent).
- **Shadow-target picker (per Architect Round 1, Am9 — `plan.md` Appendix B)** — `GroupModelForm.tsx` gains a **clearable dropdown for `shadow_model_id`** populated from `useModels()` **filtered to chat kind** (placeholder "None — no shadowing"); clearing it via group edit drops the claim so the shadowed model reappears (Q19). Without this picker, the field had no UI at all (Am9 surfaced a real gap).
- **Cache** — key `'group-models'`, URL `${API_BASE}/models?kind=group`; **quadruple** explicit delete at all **9** mutation sites in `useApi.ts` (3 per sibling hook: `useModels` `:300-302/:315-317/:325-327`, `useImgGenModels` `:402-404/:417-419/:427-429`, `useDecisionModels` `:503-505/:517-519/:526-528`): `'models'`, `'imggen-models'`, `'decision-models'`, `'group-models'`. **Never `deleteByPrefix`** (misses in-flight promises). **9 mutation sites confirmed** (**per Architect Round 1, Am18 — `plan.md` Appendix B** — research-codebase's "12" over-count; credential/token mutations correctly invalidate no model caches — no phantom sites).
- **Four lock-step chat-arm exclusions** (`plan.md` §6.4): (a) BE `?kind=chat` (`server.go:781` + default arm `:788`) excludes `kind=="group"`; (b) FE C-16 filter `ModelsTab.tsx:162` adds `&& model.kind !== 'group'`; (c) `/v1/models` keeps groups listed but hides claimed (Q1, §3.4 — odd arm out); (d) TokenForm pickers (`~:101` allowed-models, `~:157` ultimate-target) exclude `kind=="group"` from both.

**Rationale:** Tab count grows to 11 — manageable. Quadruple delete (not triple) is mandatory: any missing key from one of the 9 sites leaves a stale tab (decision commission's highest-severity finding was a chat-arm leak — `plan.md` R6 + `research-codebase.md` §6). The four-arm exclusion pattern mirrors the decision commission's lock-step (R6 named precedent). Cross-provider `isOptionDisabled` lift is the feature itself — without it, the dropdown enforces a same-provider invariant that group rows deliberately bypass (G5, Q7).

**What changes if overridden:**
- **Approve**: implement as recommended. New `GroupModelsTab.tsx`, `GroupModelForm.tsx` (incl. the Am9 clearable chat-kind shadow-target dropdown + G1/G7 stamping — `internal: true`, `exclude_from_ultimate_switching: true` read-only), `GroupMemberEditor.tsx` (3 new FE files, ~700 LOC per `plan.md` Appendix A); **9** mutation-site extensions to quadruple-delete (Am18 confirmed count); 4 lock-step arms; tsc ≤ 26 errors / 11 files (regression floor). The Am12 duplicate-`upstream_model` hint (~10 LOC) lands in `GroupMemberEditor.tsx`; the Am12 N-members ops notes land in `GroupModelForm.tsx` helper text + `models-template.json` example.
- **Override (use pill-list with edit-modal instead of rows in `GroupMemberEditor`)** — `research-codebase.md §9.2 item 12`'s alternative: ~50 extra LOC for the modal infrastructure; pill-with-edit pattern matches `DecisionModelForm.tsx`; trade-off is a heavier click-through for adding/removing members. Defer if operator UX signal suggests it.
- **Override (no shadow-target picker — keep the field text-only with no UI)**: this is the Am9 anti-pattern — the field had no UI, leaving operators no path to claim. Defer-to-v2 not viable; the picker is required to make Q19 (clearable dropdown) work.
- **Override (a different glyph — `⚖️` like decision or `🧩` or text-only)**: ~1 LOC. Pure preference; the recommended default 🔀 was chosen for "shuffle = LB" reading.
- **Override (skip the TokenForm picker exclusion — let groups appear in ultimate-target)**: G7 forces `ExcludeFromUltimateSwitching=true`, so the runtime gate never picks a group; the picker exclusion is defense-in-depth + UX clarity (the operator sees the kind discriminator). Reject this override — loses UX clarity for no gain.

---

## Q11 — Dangling member refs (block-at-delete vs runtime tolerance + heal)

**Question:** When a credential referenced by a group member is deleted, what happens? Block the delete (with a group-naming error) or tolerate at runtime (with a heal path)?

**Recommended default:** **Block-at-delete (v1)** — `ModelsManager.DeleteCredential` (DB + JSON paths) scans group members for the target credential; if any group references it, return an `ErrCredentialInUse`-family error **naming the group(s)**; operator removes the member first. Cheap scan, zero healing machinery. Runtime tolerance + heal → v2 candidate (§14).

**Rationale:** Block-at-delete is the strict posture; it guarantees the resolver kind-branch never sees an invalid member at runtime (defense-in-depth with the empty-members `ok=false` path in `plan.md` §3.6.1). Tolerance+heal would require: a per-resolve scan + a heal goroutine + a heal audit event + a state-machine ("credential deleted → members marked stale → heal finds replacement → unbinding") — disproportionate cost for v1. `plan.md` §3.8 already documents "empty group at runtime = impossible via API" — block-at-delete is the precondition that makes this true.

**What changes if overridden:**
- **Approve**: implement as recommended. `pkg/store/database/store.go` + `pkg/config/config.go` JSON twin: `DeleteCredential` gains a group-member scan before delete; new error type `ErrCredentialInUseByGroup{GroupIDs []string}` (or house envelope); ~50 LOC + 2 unit tests.
- **Override (runtime tolerance + heal)**: ~300 LOC + a heal goroutine + audit event + state machine. Defer to v2 (minimalism directive).
- **Override (warning, not block — log a warning, delete anyway)**: ~10 LOC. Renders the resolver's empty-members defense load-bearing at runtime; an operator who ignores the warning now has a group that 500s on every request. Reject this override — silent breakage is worse than loud blockage.

---

## Q12 — Semantics corners (enable/disable, empty, claim-target delete, ID immutability)

**Question:** Edge-case semantics: enable/disable a group, empty group at runtime, deleted claim target, group edits (ID, members, claim).

**Recommended default:**
- **Enable/disable**: `Enabled=false` group ⇒ claim **inactive** (shadowed model reappears in `/v1/models` and resolves normally) AND group absent from `/v1/models` (existing enabled-filter `handler.go:251`). Direct-named disabled group resolves exactly as a disabled chat model does today (no new gate — parity). No special tombstone machinery.
- **Empty group at runtime**: **Impossible via API** (G2 enforces ≥1 member at every write; Q11 blocks credential deletion that would orphan a member) and via config file (`ModelsConfig.Validate` runs the same G-rules). Defense-in-depth: the resolver kind-branch returns `ok=false` on zero valid members → the request fails with the normal resolve-failure envelope, never a panic.
- **Deleted claim target**: **Tolerated, harmless.** The claim keeps redirecting that (now nonexistent) ID to the group (a defensible "takeover" semantic); `/v1/models` has nothing to hide (the row is gone). No special machinery; documented behavior.
- **Group edits**: **ID is immutable identity**; member/weight/claim edits take effect immediately (claim index + `OnGroupChanged` + modelscache invalidation ride the write path; `plan.md` §3.5 + §3.8).

**Rationale:** Each corner is sized to the minimal surface that avoids surprise. Disable mirrors today's chat disable (parity). Empty is unreachable via normal flow but defense-in-depth keeps the resolver panic-free. Claim-target deletion is "ambiguity-proof by absence" — there is no row to hide, so the claim stands and does nothing harmful. ID immutability matches the PK + duplicate-ID hard block (`config.go:694-699`, `store.go:1358-1365`) — making IDs editable would re-introduce identity drift.

**What changes if overridden:**
- **Approve**: implement as recommended. No net new code beyond §3.4 (claim index) + §3.6.1 (resolver kind-branch empty-members `ok=false`) + §3.5 (`OnGroupChanged`); corner semantics fall out for free.
- **Override (tombstone machinery for disabled groups — keep the claim but mark inactive)**: ~30 LOC for the tombstone flag + a claim-index predicate. The recommended posture (claim inactive on disable) is equivalent without the flag — drop.
- **Override (reject request on deleted claim target — 410 Gone)**: ~10 LOC; breaks the takeover semantic. Reject.
- **Override (allow group ID edits with rename-claim cascades)**: ~150 LOC for the ID-rename machinery + G8 uniqueness scan re-runs + claim-index re-key + cache invalidation. ID immutability is the simpler posture; defer to v2 if product signals.
- **Override (panic on empty group at runtime instead of `ok=false`)**: ~3 LOC; regresses the resolver's panic-free posture. Reject.

---

## Q13 — `Internal` flag on the group row (`Internal=true`)

**Question:** `research-codebase.md §9.2 item 13` proposed `Internal=false` for groups. The plan rejects this on the §2.4 evidence (~11 `.Internal` dispatch predicates + the resolver gate `store.go:2172`). Confirm.

**Recommended default:** **`Internal=true` (G1 enforces).** The group row is a routing-layer construct, but it must be flagged internal so that:
- the ~11 `.Internal` dispatch predicates route it through the internal path: `race_executor.go:123/:178`, `handler.go:332/:339/:534`, `handler_anthropic.go:277/:419`, `handler_functions.go:174`, `internal_handler.go:190`, `race_coordinator.go:1410` (which additionally requires `len(Credentials) <= 1`), and the resolver gate `store.go:2172` (+ `:2081`, cached `:2293`).
- the resolver kind-branch intercepts **before** any `Credentials`/`InternalModel` read, so `resolveWithCredential` (`store.go:2107-2126`) never sees a group.
- G1 enforces it at write time (parity-locked at all 3 sites).

**`research-codebase.md §9.2 item 13` rejection rationale (must appear):** the proposed `Internal=false` would dispatch a group to the **external raw-passthrough path** (empty `UpstreamURL`); every one of the ~11 `.Internal` dispatch predicates would need an `|| m.Kind == KindGroup` extension. With `Internal=true`, **all sites route the group into the internal path for free**; the resolver kind-branch (§3.6.1) is the single integration point. `false` is therefore strictly more code with strictly more drift surface; `true` is the minimal correct posture. (The research-codebase proposal's underlying observation — "the member itself is internal" — is correct and is preserved: the resolver kind-branch returns `ResolvedCredential{InternalModel: member.UpstreamModel, ...}`, so the *served* member is internal even though the *group row* is also flagged internal.)

**Rationale:** Evidence-based rejection. The §2.4 predicate inventory is the proof. `false` would require 11+ kind extensions in stable code paths (chat pipeline, race coordinator, anthropic handler, internal_handler) — every one a future-drift hazard. `true` keeps those paths untouched.

**What changes if overridden:**
- **Approve (`Internal=true`)**: implement as recommended. G1 enforced at all 3 sites; parity-test G-case matrix locked (§5.3).
- **Override (`Internal=false` per `research-codebase.md §9.2 item 13`)**: ~80 LOC across ~11 sites (every `.Internal` predicate gains `|| m.IsGroup()`) + the resolver kind-branch remains necessary (it still needs to fabricate `ResolvedCredential` from the member). Strictly more code, strictly more drift surface. The research-codebase proposal's only win is "matches the chat model's posture for non-internal models" — a semantic nuance that does not justify the surface. **Rejected on evidence.**

---

## Q14 — Member cooldown precision (credentialID-only vs member-precise)

**Question:** `pkg/credentiallb` keys bindings/cooldowns by `credentialID` only — members sharing a credential would be conflated (a 429 on `(provider X, model A)` cools the entire credential backing `(X, B)`). `research-codebase.md §9.2 item 7` proposed `(credentialID, upstreamModel)` keying.

**Recommended default:** **Member-precise** — bindings + cooldowns keyed by `credentialID + "|" + upstreamModel` (`plan.md` §3.5 mapping table). A 429 cools exactly the failed (credential, model) pair. Chat-kind models retain `credentialID`-only keying (the source's existing posture — no change). Credential-wide cooling → v2 candidate (§14).

**Rationale:** The conflation is the credential-LB engine's documented limitation (`plan.md` §2.3); the clone would inherit it by default. Member-precise keying fixes the limitation at the engine and is the cost of one extra field in the bindings/cooldowns maps (the map type changes from `map[credentialID]time.Time` to `map[memberKey]time.Time`). Chat-kind models are unaffected because they live in `pkg/credentiallb`, not `pkg/grouplb`. The "credential-wide cooling" alternative is rejected as the feature class that motivates the clone in the first place. **The member key is safe only because of the `|` ban in credential IDs (per Architect Round 1, Am2 — `plan.md` Appendix B; see Q7 G3)**: without the ban, `("foo","bar|baz")` and `("foo|bar","baz")` both yield `"foo|bar|baz"` ⇒ silent wrong-binding (a 429 on one member cools the other under the same map key). The Q14 design and the Q7 G3 rule are a coupled pair — neither alone is correct; both are required for member-precise cooldowns to be meaningful.

**What changes if overridden:**
- **Approve**: implement as recommended. `pkg/grouplb/key.go` defines `MemberKey(credentialID, upstreamModel) string` (returns `credentialID + "|" + upstreamModel`); `engine.go` `cooldowns` map type changes to `map[MemberKey]time.Time`; `bindings` map type changes to `map[convKey]*binding` where `binding.MemberKey` is the new identity. P2.1 test pins per-member cooldown isolation (`plan.md` §9.1). ~20 LOC + 1 unit test.
- **Override (credential-wide cooling — inherit source's limitation)**: zero net code change in the cooldown keying; a 429 on `(X, A)` cools `(X, B)` even when `B` is healthy. Documented in §14; rejected for v1 because it is the engine's known limitation, not a feature.
- **Override (per-(credential, provider, upstreamModel) tuple — finer-grained)**: ~30 LOC; the extra provider dimension rarely matters (one credential = one provider in practice). Disproportionate cost for the typical case; defer to v2.

---

## Q15 — Member-resolution observability event (`model_member_selected`)

**Question:** `research-codebase.md §9.2 item 11` proposed emitting a `model_member_selected` event on member resolution. Adopt?

**Recommended default:** **Yes — `model_member_selected {group_id, member_key, upstream_provider, upstream_model}`** at the existing W-1 publish site (`race_executor.go`, W-1 — fire only on `newlyBound`). Non-group traffic keeps `model_credential_selected` unchanged. Folding the event into the existing publish site means the kind-switch is one new branch.

**Rationale:** Operators debugging "why did this conversation land on provider X?" want a single observable for member resolution. The existing `model_credential_selected` site is the parallel (and the doc-comment precedent at `store.go:2139` is named in `research-codebase.md §9.2 item 11`). Firing only on `newlyBound` matches the existing publish discipline (no event spam on every request). The slight overhead is bounded (one map lookup + one event publish per request, only when a new binding is created).

**What changes if overridden:**
- **Approve**: implement as recommended. `pkg/proxy/race_executor.go` kind-switch at the W-1 publish site: groups → `bus.Publish("model_member_selected", {group_id, member_key, upstream_provider, upstream_model})`; non-groups → existing path. ~20 LOC + 1 unit test (mock bus + assert event payload).
- **Override (no observability event — rely on logs)**: zero LOC; loses the structured-event discipline the codebase uses elsewhere. Reject — observability is cheap and the existing publish site is one branch.
- **Override (fire on every request, not only `newlyBound`)**: ~10 LOC; event spam. The codebase convention is `newlyBound` only; deviating is unjustified.
- **Override (also emit on `newlyBound=false` for debugging — operator env flag)**: ~30 LOC + 1 config surface. Defer to v2; no product signal.

---

## Q16 — Fallback interactions (group→group impossible; chat→group permitted)

**Question:** Can a chat model's `fallback_chain` reference a group ID? Can a group's `fallback_chain` reference another group ID? (`research-codebase.md §9.2 item 14`.)

**Recommended default:**
- **Group→group: impossible** — G6 forbids `FallbackChain` non-empty for groups (`plan.md` §5.1). `research-codebase.md §9.2 item 14`'s broader first-class semantics (recursion, debug depth guards) → v2 candidate (§14).
- **Chat→group: permitted** — a chat model's `fallback_chain` MAY reference a group ID; the fallback row re-resolves per attempt through the same resolver seam (`ResolveInternalConfigWithAffinity` kind-branch) and works. **Documented posture**: member-level exclusion does NOT fire for a group reached *via fallback* (the `race_coordinator.go:1410` `len(Credentials) <= 1` guard skips `Credentials`-empty rows) — such attempts just fail over to the next chain row. The primary group path (direct-named group request) has full member-level exclusion (`plan.md` §3.6.2). Documented, no extra code.

**Rationale:** The asymmetry is deliberate. Chat→group is a common ops pattern (chat primary, group as a heterogeneous fallback). Group→group would require recursion handling (depth guards, debug instrumentation) and is not part of v1 minimalism. The chat→group exclusion asymmetry is documented because operators who rely on per-attempt failover need to know it (the primary group path has exclusion; the fallback-reached group path does not).

**What changes if overridden:**
- **Approve (group→group impossible; chat→group permitted with documented posture)**: implement as recommended. G6 enforces; chat→group uses existing fallback chain machinery; documentation note in `plan.md` R-related + `pkg/proxy/handler.go` comments at the fallback resolve site. Zero net code change beyond the documentation.
- **Override (group→group legal — `research-codebase.md §9.2 item 14`)**: G6 is relaxed for groups whose `FallbackChain` entries are themselves group IDs; resolver kind-branch gains a depth guard (e.g., max 3); `pkg/grouplb` gains a `RecursionDepth` event. ~80 LOC + a depth-guard test. Defers v1 ship; minimalism directive cuts.
- **Override (chat→group forbidden — explicit G9 rule)**: ~5 LOC. Breaks a useful ops pattern; reject.
- **Override (chat→group permitted AND exclusion fires for fallback-reached groups — drop the `len(Credentials) <= 1` guard)**: ~10 LOC at `race_coordinator.go:1410`. The guard exists for a reason (raw-passthrough models); relaxing it for groups-only risks regression in the raw-passthrough path. Defer; the documented posture is acceptable for v1.

---

## Q17 — Ultimate-stops-while-shadowed

**Question:** **NEW (Architect Round 1, `architecture-recommendation.md` §12.1 — `plan.md` Appendix B, Am5)**. While X is claimed by an enabled group, claims-first `GetModel(X)` returns the group row (G7-excluded), so X's ultimate switching halts until the claim drops. Accept the semantic?

**Recommended default:** **Accept + document** (Am5; `plan.md` §3.6.4 + §11 R9). The group takes over X's identity wholesale; un-claiming (delete/disable/edit) restores X's ultimate eligibility. No code — pure documentation.

**Rationale:** Behavioral defensibility (`plan.md` §3.6.4, Am5): the group *is* X from a client's perspective — same ID, same wire format, same auth — so the same identity's ultimate switching transfers with it. The reverse (X retains ultimate eligibility while claimed) would create a client-visible surprise: a request for X sometimes returns the group, sometimes the ultimate target. Accepting the wholesale-takeover semantic keeps the surface uniform. Documentation-only means no P2.9-style code change; the architectural consequence (an operator who relies on X's ultimate switching must un-claim X first) is a discoverable docs note, not a runtime check. The residual `X-Force-Ultimate-Model` bypass (R9) is unchanged: an operator forcing ultimate onto a group ID gets the external-passthrough path with an empty `UpstreamURL` — fails fast with the normal error envelope.

**What changes if overridden:**
- **Approve (accept + document)**: implement as recommended. Add one sentence to `plan.md` §3.6.4 (already present post-Am5) and `pkg/ultimatemodel/handler.go` package doc (the runtime gate at `:696` already excludes `ExcludeFromUltimateSwitching=true`; the wholesale-takeover note belongs in the gate's package doc, not at the call site). Zero net code change beyond docs; §9.4 may add a one-line test asserting "claimed model resolves to group row, ultimate gate never selects the group row as ultimate target." ~5 LOC doc + 1 line test.
- **Override (re-open — keep X's ultimate switching alive while claimed)**: would require a kind-aware carve-out in the ultimate gate (`pkg/proxy/handler.go:696`) — the gate would need to consult the claims index to know whether the *request's* model is actually a group row, then bypass exclusion when the claim is in place. ~30 LOC + a kind-aware integration test. Behaviorally creates a client-visible inconsistency (the same ID sometimes routes to the group, sometimes to the ultimate target) — the cost of the override exceeds its benefit; reject unless product signal says otherwise.
- **Override (kill X's row outright — claim ≡ model deletion)**: stronger than the documented semantic; breaks the takeover semantic (clients who held tokens listing X would get 404 after the claim). Reject.

---

## Q18 — N-members-per-target pattern

**Question:** **NEW (Architect Round 1, `architecture-recommendation.md` §12.2 — `plan.md` Appendix B, Am12)**. A multi-credential target is expressed as N member rows (same `upstream_model`, one per credential) rather than per-target credential pooling. Accept for v1?

**Recommended default:** **Accept + document** (Am12; `plan.md` §3.2 ops notes + FE duplicate-`upstream_model` hint). Legal and cooldown-correct (distinct member keys, weights compose arithmetically as credential weights; prefix-sum works); rotation of a credential orphans that member's bindings by design (runbook sentence, not a defect). Per-target credential pooling → v2 candidate (§14).

**Rationale:** The N-members-per-target pattern is implicit in G4's "no duplicate members (same `CredentialID + UpstreamModel` pair)" — two members sharing an `upstream_model` but differing in `CredentialID` are distinct (different member keys, different weights, different cooldown buckets). The pattern is the natural way to express "fan out across multiple credentials for the same upstream model" in v1 without inventing a per-target credential-pooling sub-engine. The **duplicate-`upstream_model` hint (Am12-FE; see Q10)** in `GroupMemberEditor.tsx` warns when two rows share an `upstream_model` so operators don't mis-read the rows as a validation accident. The **rotation note (Am12 runbook)** documents that rotating a credential changes the member key and `OnGroupChanged` drops the affected sticky bindings — a desired property (stale-bindings cleanup), not a defect. Per-target credential pooling would be one member spanning multiple credentials with engine-level credential LB inside the member — a §14 candidate for v2 once per-member overrides become a real product signal.

**What changes if overridden:**
- **Approve (accept + document)**: implement as recommended. ~10 LOC for the FE duplicate-`upstream_model` hint (`GroupMemberEditor.tsx` inline non-blocking); the Am12 ops note lives in `GroupModelForm.tsx` helper text + `models-template.json` example + `plan.md` §3.2 ops notes (already present post-Am12). ~15 LOC + 1 hint test.
- **Override (re-open — defer N-members-per-target, require per-target credential pooling in v1)**: would require a per-member `MemberRef{Key, Weight, Credentials[]}` extension + an inner credential-LB sub-engine + G4 rewritten to permit multiple credentials per member row. ~200 LOC + a per-target selector; disproportionate cost for a v1 feature with no product signal. Reject; defer to v2 if pooling is ever required.
- **Override (forbid N-members-per-target — same `upstream_model` disallowed across rows)**: ~5 LOC at G4 (a duplicate-`upstream_model` check). Breaks the multi-credential-fan-out pattern; the only escape becomes a second credential row referenced as a separate member (which loses the weight-composition semantic). Reject.

---

## Q19 — Clearing `shadow_model_id` via group edit

**Question:** **NEW (Architect Round 1, `architecture-recommendation.md` §12.3 — `plan.md` Appendix B, Am9)**. Does an operator edit that empties the claim field drop the claim?

**Recommended default:** **Allow** (Am9; `GroupModelForm` clearable dropdown — see Q10). Claim drops → shadowed model reappears in `/v1/models` and resolves normally; cache/index invalidation rides the existing group-write hook (`plan.md` P2.2; the `OnModelChanged` extension already rebuilds the claim index + evicts `modelsByID[old shadow]` + `negByID[old shadow]` per Am4, so setting `shadow_model_id = ""` flows through the same hook with `old = cleared, new = empty`).

**Rationale:** Operator ergonomics (`plan.md` §6.2, Am9): the `shadow_model_id` field had **no UI** at all before Am9 surfaced the gap — a real defect, not a feature. Operators need a path to (a) initially set the claim (the chat-kind-filtered picker in `GroupModelForm`), (b) change the claim target (re-pick from the dropdown), and (c) clear the claim (set to "None — no shadowing"). Clearing is the simplest of the three but the most operator-discoverable: an operator who claims the wrong model needs to undo. The mechanics are free because the existing group-write hook already handles claim changes; clearing is a claim change with `new = ""`. G8 uniqueness scan (`plan.md` §5.1) only triggers when the new value is non-empty, so clearing is not gated by G8 — it just drops. The cleared model reappears in `/v1/models` (the second-pass shadow-hide no longer hides its ID), and `GetModel(shadowedID)` returns the real chat-model row again.

**What changes if overridden:**
- **Approve (allow + Am9 picker)**: implement as recommended. The `GroupModelForm.tsx` clearable chat-kind dropdown (Q10) covers (a) set, (b) change, (c) clear in one UI surface; the BE write path's `OnModelChanged` hook (P2.2) handles the claim-index + modelscache eviction (Am4) for all three cases. ~15 LOC (the picker UI; the BE invalidation is shared with claim changes). `GroupModelForm.test.tsx` green for set / clear / chat-kind-only options (P3.5).
- **Override (disallow clearing — once claimed, the claim is permanent until the group is deleted)**: ~5 LOC to reject the empty-string write at the UI mirror + DB path. Breaks an operator-undo path; operators who claimed the wrong model must delete and re-create the group. Reject — no operator benefit; UX cost is real.
- **Override (allow clearing but require a confirmation step — two-phase claim drop)**: ~20 LOC for a confirmation modal + a state-machine flag (`clearing_pending`) on the group row. Disproportionate cost for a v1 reversible action; the empty-string write is already auditable in `event_log`. Reject; defer to v2 if audit trails grow.

---

## Q20 — Fallback-reached groups meter the member row

**Question:** **NEW (Architect Round 1, `architecture-recommendation.md` §12.4 — `plan.md` Appendix B, Am3/Q9)**. Winner-based gating (Am3, Q9) meters chat→group fallback traffic to the served member as well as the group name — confirm intended.

**Recommended default:** **Yes — intended** (consistent with Q9's both-rows intent); falls out of the **winner-row-kind == group && resolvedOK** gate (Q9 plumbing per Am3) for free, no extra code (P2.9 acceptance pins it).

**Rationale:** A fallback-reached group IS a group for metering purposes — the request arrives at the group's resolver seam (`ResolveInternalConfigWithAffinity` kind-branch) and the engine picks a member; the winner's `resolved.InternalModel` propagates via the Am3 plumbing (`GetResolved()` on `upstreamRequest` OR `rc` stamp). Q9's gate keys on the winner row's kind, not on the request path: a direct-named group request and a fallback-reached group request both produce a winner-row-kind == group && resolvedOK gate hit. The two paths differ only in that fallback-reached groups bypass **member-level exclusion** (the `race_coordinator.go:1410` `len(Credentials) <= 1` guard skips the `:344-351` exclusion branch — Q16 documented posture) — they do NOT bypass metering. The metering symmetry is intentional: operators who rely on per-target cost attribution for groups want it everywhere a group is served, including the fallback-reach path. P2.9's acceptance criterion explicitly pins "fallback-reached group meters the member row" — no extra code, no override path needed.

**What changes if overridden:**
- **Approve (yes — meter the member row too)**: implement as recommended. The Q9 plumbing (Am3) is shared between the direct-named and fallback-reached paths; the gate keys on winner-row-kind; P2.9's acceptance test (`plan.md` §10) pins the behavior. Zero net code change beyond Q9.
- **Override (no — fallback-reached groups meter group-name only)**: would require a path discriminator in the metering gate (was this request direct-named or fallback-reached?) — either a per-attempt RC stamp or a per-attempt request-context flag. ~10-15 LOC + an integration test that asserts the asymmetric path. Asymmetry-for-cost-attribution is the surprise class this override would create; operators see member-metered traffic for direct-named groups and unmetered traffic for fallback-reached groups, both for the same model. Reject.
- **Override (meter member-row only — skip group-name row for fallback-reached)**: stronger asymmetry (Q9 already gates group-name row at the ultimate site; this would also skip it for fallback-reached). Loses dashboard continuity for the fallback path. Reject.

---

## How to approve / override

Reply with one of:

- **"Approve all"** — implement with all 20 defaults.
- **"Approve Q1-Q20 except: Q5 = override, Q9 = override, Q13 = `Internal=false` per `research-codebase.md §9.2 item 13`"** — implement with overrides named.
- **"Override: Q17 = re-open; Q19 = disallow clearing"** — implement with all other defaults.

Each override is treated as item-by-item. The implementer applies the approved defaults; overrides land as one-line to one-file changes per item (per the override pricing in each Q's "What changes if overridden" section).

If a question is unanswered, the default applies.

**USER-DECIDED (locked) items** — Q3 (LB strategy), Q4 (sticky affinity), and the flat-membership aspect of Q7 — record directives already settled mid-flight (shared meta-KV `lb-groups-plan.scope-guidance`, 2026-10-16). Approving them is a formality unless the user explicitly re-opens. The override path remains open in `What changes if overridden` for traceability; the override costs priced there are real (and would re-open the minimalism directive).