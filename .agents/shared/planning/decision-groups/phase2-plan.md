# Phase 2: Frontend — Decision Models container, vertical child nav, decision-group UI

Date: 2026-11-16
Author: plan-creation worker (phase 2 — frontend) via plan-creation skill
Status: Draft — ready for approver review
Base: `feature/decision-groups` @ `2b987bf` (== `origin/latest`; **re-anchored 2026-11-16 from `24c51ef` by v194 `fix/cache-layer-separation` — ADR-16, `decisions.md:951-1001`**)
Worktree: `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-decision-groups` (NOT the main checkout)
Binding ruling: `.agents/shared/planning/decision-groups/decisions.md` (D-1..D-8; FE contracts 23–30). This plan implements the FE half of that ruling; it does not re-litigate it.
Base research: `.agents/shared/planning/decision-groups/research-fe-precedent.md` (FE inventory), `research-groups.md` §"Kind-widening blast radius" item 8 (FE chat-only membership sites).

**Scope of this artifact:** markdown plan only. NO code, NO tests written, NO config edits, NO commits, NO pushes, NO deploys. `vite build`, `tsc`, and `vitest` were **not run** (explicitly forbidden by the task) — every statement below is static reasoning from direct reads of the worktree at `2b987bf`. Each code claim carries `path:line`; unverified items are collected under `## Not verified`.

---

## Objective

Clicking the top-level **"Decision Models"** tab shows a container with a **vertical left-hand nav** offering two child tabs — `models` (the existing decision-models list, unchanged in behaviour) and `group` (a new decision-group management surface modelled on the existing text-group UI: members, weights, `backup_only`) — such that an operator can create, edit and delete a group whose members are decision-kind models from the Decision Models tab, without changing the top-level tab count (11), without changing any chat/text/image-gen behaviour, and without adding a provider.

**Completion test (one sentence):** a decision group created through the container's `group` child tab appears in that tab's list, its members are decision-kind models only, and the text lane, the img-gen tabs, the decision-models list and the provider vocabulary are byte-unchanged.

---

## Context — the FE surface being changed (verified inventory)

| Fact | Evidence |
|---|---|
| The Decision tab today is a flat list component with page-level chrome and an in-place form mount | `pkg/ui/frontend/src/components/config/DecisionModelsTab.tsx:8-25` (props), `:87-97` (7 `useState`s), `:217-222` (root `div.space-y-4` + `h3 "Decision Models"`), `:223-232` ("Add Decision Model" CTA), `:239-256` (`role="alert"` banner), `:260` (`data-testid="decision-models-list"`), `:302-310` (🎯 provider badge), `:342+` (per-row TEST panel) |
| It takes **no** router/context dependency → structurally nestable | `DecisionModelsTab.tsx:8-25` — every input is a prop; the only non-prop coupling is the module-level import at `:4` |
| The TEST button calls the API module directly (the one non-prop coupling) | `DecisionModelsTab.tsx:4` (`import { testDecisionModel } from '../../hooks/useApi'`), `:187-215` (`handleTest`), `:197` (`testDecisionModel(model.id)`) |
| The text-group UI to mirror | `pkg/ui/frontend/src/components/config/GroupModelsTab.tsx:6-30` (props, `chatModels` at `:14`, wrap signal at `:28-29`), `:35-40` (toast copy), `:55-65` (state), `:98-132` (save), `:115-124` (PUT body), `:151-167` (header + CTA), `:172-189` (banner), `:193` (list), `:194-204` (empty state), `:210-215` (rows), `:303` (`chatModels={chatModels}` pass-through), `:314` (`groups={models as GroupModel[]}`) |
| The group form | `pkg/ui/frontend/src/components/config/GroupModelForm.tsx:16-17` (`HARDCODED_KIND='group'`, `HARDCODED_INTERNAL=true`), `:24-34` (payload), `:36-60` (props; `chatModels` doc `:49-53`, prop `:53`; `groups` `:56`), `:77-114` (`makeInitialState`; `backup_only` round-trip `:85-89`), `:161-186` (client G-mirrors), `:195-207` (G8 shadow mirror), `:343-347` (editor mount), `:378-392` (shadow select + options) |
| The member editor | `pkg/ui/frontend/src/components/config/GroupMemberEditor.tsx:6` (`MAX_GROUP_MEMBERS = 16`), `:8-16` (props; `chatModels` `:14`), `:35` (signature), `:40` (hard chat-kind filter), `:43` (add-cap), `:61-74` (backup_only toggle; drops the key when false), `:81-85` + `:169-175` (dup warn, warn-only), `:91` (provider badge lookup), `:105` (placeholder "Select a chat model…"), `:180` (Add disabled at 16), `:190` (lane-dependent hint copy) |
| Top-level tab chrome | `pkg/ui/frontend/src/components/SettingsPage.tsx:66` (`TabType`, 11 values), `:103` (`useState<TabType>('proxy')`), `:381` (`bg-gray-800 border-b border-gray-700 px-6`), `:382` (`div.flex.overflow-x-auto`), `:383-385` (the `type="button"` rationale comment), `:388-391` (active/inactive class pair), `:426-437` (the Decision button, `data-tab-id="decision_models"` at `:433`), `:502-520` (the Group button, `data-tab-id="group_models"` at `:514`, `aria-label` at `:515`), `:523-526` (content branch pattern), `:607-619` (decision branch), `:631-645` (group branch) |
| **Zero** vertical-nav / ARIA-tab precedent in the whole FE | `grep` over `pkg/ui/frontend/src`: `role="tablist"`, `role="tab"`, `role="tabpanel"`, `aria-selected`, `aria-current` → **0 matches** (the container in this commission becomes the FE's first `aria-current` usage — CMO adjudication E1); `border-l-2` → **0 matches**; `border-l` → only `RequestDetail.tsx:322,:351` (1px rules); the only `border-r`+`flex-col` is `RequestList.tsx:51` (a grid column, not a nav) |
| Data layer — no separate hook files; all model hooks live in one module | `pkg/ui/frontend/src/hooks/useApi.ts:278-300` (`useModels`, key `'models'` `:285`, URL `:286`), `:532-613` (`useDecisionModels`, key `'decision-models'` `:544`, URL `?kind=decision` `:545`), `:636-713` (`useGroupModels`, key `'group-models'` `:648`, URL `?kind=group` `:649`), `:1267-1272` (`testDecisionModel` → `apiFetch('/decisions/test')`, **no AbortSignal**) |
| Quadruple cache-key invalidation already covers every list | `useApi.ts:572-575`, `:588-591`, `:599-602` (decision mutations) and `:674-677`, `:689-692`, `:699-702` (group mutations) — each deletes `'decision-models'`, `'models'`, `'imggen-models'`, `'group-models'` |
| Types | `pkg/ui/frontend/src/types.ts:107-154` (`Model`), `:122` (`kind?: 'chat' \| 'image-gen' \| 'decision' \| 'group'`), `:152` (`members?: GroupMember[]`), `:153` (`shadow_model_id?`), `:166` (`DecisionModel`), `:168-186` (GroupMember doc block), `:182-186` (`GroupMember {model_id, weight, backup_only?}`), `:192` (`GroupModel`) |
| The FE decision-provider constant (MUST NOT change) | `pkg/ui/frontend/src/components/config/DecisionModelForm.tsx:29` — `const PROVIDER_OPTIONS = ['openai', 'typesafe', 'openrouter'] as const;` |
| Server-side group listing has **no** lane filter | `pkg/ui/server.go:761-820` — `GET /fe/api/models?kind=` switch has arms for `""`/`image-gen`/`decision`/`group`/`chat` only; there is **no** lane parameter |
| Group rows carry no lane field | `pkg/models/group.go:42-46` (`GroupMember`/`GroupModel` shape), `migrations/sqlite/031_add_group_fields.up.sql:39-40` (`members_json` + `shadow_model_id` on `models`); `grep -rn "GroupLane" pkg/` → **0 matches** (the ruling's `GroupLane` seam is a phase-1 deliverable, not a shipped symbol) |
| **User ruling ADRs (2026-11-16)** | ADR-1 kind immutability for group members `decisions.md:426-460`; ADR-5 pick-is-policy / a mark suppresses an attempt `decisions.md:584-620`; ADR-6 a member fails only after its own resilience is consumed `decisions.md:622-654`; **ADR-7 chain-only models are supported, not a defect** `decisions.md:656-673`; ADR-8 single-member group / R5 `decisions.md:675-693`; ADR-9 text lane untouched by this release `decisions.md:695-720`; **ADR-10 the consolidated criteria AC-R1..AC-R10** `decisions.md:720-748`; **ADR-11 the OQ-1..OQ-4 closures** `decisions.md:750-778`; ADR-12 per-layer caches `decisions.md:780-809` |
| ADR-7's two-population rule (FE-visible) | "Chain targets and group members are two populations — do not conflate them in validation or in tests" — `decisions.md:775`; "The FE member picker and any server-side candidate list must exclude them" — `decisions.md:665-666` |
| The member wire shape carries **no** `enabled` (AC-R7) | `pkg/ui/frontend/src/types.ts:182-186` (`GroupMember = {model_id, weight, backup_only?}`); `pkg/ui/frontend/src/components/config/GroupModelForm.tsx:24-34` (`members: GroupMember[]`) — the FE structurally cannot flip a member's enable state |
| FE event rendering is **generic** (AC-R10) | `pkg/ui/frontend/src/components/EventLog.tsx:279-282` (`EVENT_MESSAGES[event.type]` → `` `Event: ${event.type}` ``), `:284` (`EVENT_COLORS[type] \|\| 'text-gray-400'`), `:286` (`EVENT_TYPE_LABELS[type] \|\| String(type).toUpperCase()`); the FE's `EventType` union (`pkg/ui/frontend/src/types.ts`) contains **no** decision token, while the BE publishes `eventTypeDecision = "decision"` (`pkg/decision/handler.go:92`) — every event renders through a generic fallback |
| The ruling forbids a persisted/DTO lane field | `decisions.md:137` (D-2) — "write-time homogeneity … runtime inference — NO schema change, no new column, no migration, no DTO field, no FE field" |

---

## 1. Component tree

### 1.1 New files

| File | Responsibility |
|---|---|
| `pkg/ui/frontend/src/components/config/DecisionModelsContainer.tsx` **(NEW)** | The container. Owns the vertical left nav, the child-tab state, and the conditional panel mount. **No page-level heading of its own** — each child panel owns its own heading (this is what keeps `DecisionModelsTab` byte-unchanged; see 1.3). Renders `DecisionModelsTab` (models panel) or `GroupModelsTab lane="decision"` (group panel). |
| `pkg/ui/frontend/src/components/config/groupLane.ts` **(NEW)** | Pure lane vocabulary + inference helpers, no React, no I/O: `GroupLane` re-export, `normalizeKind()`, `buildKindIndex()`, `inferGroupLane()`, `splitGroupsByLane()`. See §3.2 for the exact rule and §3.3 for the split. |

### 1.2 Changed files

| File | Change |
|---|---|
| `pkg/ui/frontend/src/components/SettingsPage.tsx` | (a) mount `DecisionModelsContainer` instead of `DecisionModelsTab` in the `decision_models` branch (`:607-619`); (b) compute the lane split and pass the lane-scoped group list + lane model lists to both group surfaces; (c) add `decisionKindModels`; (d) `chatKindModels` (`:293-295`) stays **byte-identical**. |
| `pkg/ui/frontend/src/components/config/GroupModelsTab.tsx` | Add required prop `lane: GroupLane`; rename `chatModels` → `laneModels`; lane-aware copy (heading / CTA / toast / empty state / member hint); the wrap pre-fill stays **text-lane only** (the decision lane passes no `wrapPreloadModel`). |
| `pkg/ui/frontend/src/components/config/GroupModelForm.tsx` | Add required prop `lane: GroupLane`; rename `chatModels` → `laneModels`; lane-aware G8 shadow mirror (`:195-207`) + copy; lane-aware editor mount (`:343-347`); lane-aware shadow options (`:381`); **new** lane-consistency guard for the edited group (see §5.2). |
| `pkg/ui/frontend/src/components/config/GroupMemberEditor.tsx` | Add required prop `lane: GroupLane`; rename `chatModels` → `laneModels`; the hard chat filter at `:40` becomes lane-driven; placeholder (`:105`) and hint copy (`:190`) become lane-aware. **AMENDED 2026-11-16 by Amendment 1 (ADR-7 / AC-R6):** the lane-driven filter gains a **lane-gated candidate-eligibility clause** — the `lane='decision'` branch also excludes **inactive** models (`m.kind === 'decision' && m.enabled`); the `lane='chat'` branch stays character-for-character today's predicate. |
| `pkg/ui/frontend/src/types.ts` | **Add** `export type GroupLane = 'chat' \| 'decision';` (new symbol, additive; no existing type changes). |

### 1.3 `DecisionModelsTab` is **wrapped, not refactored**

**Decision: `DecisionModelsTab.tsx` is NOT modified — byte-for-byte.** The container mounts it as-is inside the models panel.

Rationale (four independent reasons):

1. **The user's requirement is "moved inside unchanged in behaviour."** `DecisionModelsTab` owns 7 pieces of local state (`DecisionModelsTab.tsx:87-97`), an in-place form mount (`:217-232` — `showForm` swaps the whole body), a per-row probe state machine (`testingIds`/`testResults`) and a delete-confirm modal. Any refactor of it risks the decision lane's existing behaviour for zero functional gain.
2. **The duplicated-heading cosmetic concern dissolves if the container owns no heading.** The research flagged that the tab renders its own `h3 "Decision Models"` (`:221-222`) and that inside a container labelled "Decision Models" that is duplicated text (`research-fe-precedent.md:55`). The fix is **not** to strip the heading from the tab — it is to give the container **no page-level heading at all** and let each child panel own its heading (the models panel says "Decision Models", the group panel says "Decision Groups"). Nothing is duplicated, and `DecisionModelsTab` stays untouched.
3. **The existing decision-tab suites stay green with zero edits**: `pkg/ui/frontend/src/components/config/__tests__/DecisionModelsTab.test.tsx` (219 lines) and `DecisionModelsTab.decisionTest.test.tsx` are written against the component, not against its parent — the container is a pass-through for every prop.
4. **The ruling's FE contracts do not ask for a refactor.** `decisions.md:391-400` (contracts 23–30) require the container, the nav, a lane-parameterized member editor, a lane-aware G8 mirror, tsc parity and the embed discipline. A `DecisionModelsTab` refactor is none of them.

**Consequence to state explicitly:** the container must pass the models panel's props **verbatim** — including `onNavigateToCredentials` (the decision form's "navigate to credentials" affordance, `DecisionModelsTab.tsx:19`), `fetchError` and `onRetry` (`:23-24`). The container is a pass-through, not a filter.

### 1.4 The decision-group child panel

**Decision: the `group` child panel is the existing `GroupModelsTab` with `lane="decision"` — NOT a forked `DecisionGroupsTab`.**

- `GroupModelsTab` already *is* "list + form + delete-modal + enable-toggle + error banner" (`GroupModelsTab.tsx:151-…`). Forking it would duplicate ~360 lines including the delete-confirm modal, the toast plumbing and the PUT-body discipline (`:115-124` — the sparse-PUT trap).
- `GroupModelForm` is reused with `lane="decision"` (payload stamping `kind:'group'` + `internal:true` is **identical** for both lanes — `GroupModelForm.tsx:16-17`; a decision group is still a `kind='group'` row under the ruling D-1).
- `GroupMemberEditor` is reused with `lane="decision"` (the ruling's contract 26: "one component, two filtered feeds", not duplicated).

**Lane-aware copy inside the shared components** (the only text that differs):

| Surface | `lane='chat'` (today, unchanged) | `lane='decision'` (new) |
|---|---|---|
| Heading (`GroupModelsTab.tsx:222`) | "Group Models" | "Decision Groups" |
| CTA (`GroupModelsTab.tsx:228-231`) | "Add Group Model" | "Add Decision Group" |
| Empty state (`GroupModelsTab.tsx:194-204`, copy at `:203`) | "No group models configured" / "load-balance chat traffic across multiple member models" | "No decision groups configured" / "**members are picked by weight per request — there is no per-conversation pinning; resilience is per-credential attempts within a member; a fallback chain has no equivalent concept on this lane (deliberately not built)**" |
| Toast (`GroupModelsTab.tsx:35-40`) | "Group model added/updated/deleted/toggled successfully" | "Decision group added/updated/deleted/toggled successfully" |
| Member placeholder (`GroupMemberEditor.tsx:105`) | "Select a chat model…" | "Select a decision model…" |
| Member hint (`GroupMemberEditor.tsx:190`) | "Add up to 16 chat models. Higher weight = more requests routed to that member. Members run their own full pipeline (credentials, fallback, race). …" | "Add up to 16 **decision** models. Higher weight = more requests routed to that member. Each member runs its **own credentials**; resilience is **per-credential attempts within the member**. There is **no fallback chain** on this lane (deliberately not built) and **no racing** — 'racing consumed' means the member loop reached its end. …" |
| Shadow-target error copy (`GroupModelForm.tsx:200`) | "…must reference an existing chat-kind model" | "…must reference an existing decision-kind model" |
| Group-row badge glyph (`GroupModelsTab.tsx:210-215`) | "🔀" (shuffle = load balancing) | **"🎯"** (the app's established decision glyph, `DecisionModelsTab.tsx:302-310`) — **not** 🔀: the decision lane has **no racing / hedging** engine (ADR-8, ADR-11 OQ-3 option (a)), so the shuffle glyph would promise resilience that does not exist |
| **Fixed-constants line** (`GroupModelForm.tsx:263-265`, `data-testid="group-form-fixed-constants"`) — **AMENDED 2026-11-16 by Amendment 2 (review warning 12)** | "Virtual load-balancing group — kind: group · internal: true · backup-only members serve as last-resort fallback **in the race chain**." | "Decision group — kind: group · internal: true · backup-only members are **recourse-tier candidates** (served only after every default member has failed). **No fallback chain and no racing on this lane.**" — the chat-lane string promises a **race chain** that does not exist on the decision lane (ADR-11 OQ-3 option (a): structural loop contract, no race engine; OQ-2: no chain). Pinned by **D10** |

The decision group's empty state additionally states the no-pinning semantic (**Q6, recommended YES** — `decisions.md:413`): "Members are picked by weight per request — there is no per-conversation pinning on the decision lane." One extra sentence in the `lane='decision'` empty-state copy; the chat empty state is untouched.

---

## 2. Vertical nav spec

### 2.1 Markup shape

```
<div class="flex gap-4" data-testid="decision-models-container">
  <nav class="shrink-0 w-40 flex flex-col gap-1 border-r border-gray-700 pr-2"
       data-testid="decision-container-nav"
       aria-label="Decision Models sections">
    <button type="button"
            class="px-3 py-2 text-left font-medium transition-colors whitespace-nowrap border-l-2 text-blue-400 border-l-blue-400"
            data-testid="decision-container-tab-models"
            aria-label="Decision Models"
            aria-current="true"
            onClick={() => setChild('models')}>Models</button>
    <!-- aria-current="true" is rendered on the ACTIVE button only, and
         it FOLLOWS the click (it moves to the newly activated button).
         The INACTIVE button OMITS the attribute entirely — never
         aria-current="false". It adds no role, no roving tabindex and no
         keyboard-behaviour change (CMO adjudication E1). -->
    <button type="button"
            class="px-3 py-2 text-left font-medium transition-colors whitespace-nowrap border-l-2 text-gray-400 hover:text-white border-l-transparent"
            data-testid="decision-container-tab-group"
            aria-label="Decision Groups"
            onClick={() => setChild('group')}>Group</button>
  </nav>
  <div class="flex-1 min-w-0" data-testid="decision-container-panel">
    {child === 'models' ? (<DecisionModelsTab ...verbatim props... />)
                        : (<GroupModelsTab lane="decision" ... />)}
  </div>
</div>
```

### 2.2 Class derivation — what is mirrored and what is new

| Class | Source |
|---|---|
| `text-blue-400` (active) / `text-gray-400 hover:text-white` (inactive) | The top bar's active/inactive pair, verbatim (`SettingsPage.tsx:388-391`) |
| `border-l-2 border-blue-400` (active) / `border-l-2 border-transparent` (inactive) | **Translation** of the top bar's `border-b-2 border-blue-400` (`:390`) from the bottom edge to the left edge. `border-l-2` has **zero** occurrences in the FE today (verified by grep) — this is the one new utility class, and it is the deliberate vertical analogue of the horizontal one. `border-l-transparent` on the inactive item keeps the geometry constant, so activating a nav item does not shift the labels by 2px. |
| `font-medium transition-colors whitespace-nowrap` | The top bar's base typography (`:388`), condensed: `px-6 py-3` → `px-3 py-2` because the nav is a 10rem column, not a full-width bar. `whitespace-nowrap` is kept so the labels never wrap. |
| `flex flex-col gap-1` (the nav column) | The only `border-r`+`flex-col` column in the app is `RequestList.tsx:51` — `bg-gray-900 border-r border-gray-700 flex flex-col min-h-0`. The nav column borrows the `border-r border-gray-700` divider idiom (the top bar's `border-b border-gray-700`, `SettingsPage.tsx:381`, rotated). `gap-1` is within the house gap scale (`gap-1.5`/`gap-2`/`gap-3`/`gap-4` all appear). |
| `w-40` (nav width) | `w-40` already exists in the FE (`EventLog.tsx:174-178` etc.); `w-36` does not. |
| `flex gap-4` (the container row) + `shrink-0` + `flex-1 min-w-0` | `MCPServersTab.tsx:143-144` (`flex items-start justify-between gap-4` / `flex-1 min-w-0`), `SettingsPage.tsx:367` (`gap-4`), `Toast.tsx:97/:112` (`shrink-0` / `min-w-0`) |

**Deliberate deviation from the top bar, and why:** the top bar is a horizontal strip with a bottom rule under the active item. The nav is a left column with a left rule. Everything else (colour pair, weight, transition, hover, typography scale) is the same idiom, so the two surfaces read as one design language even though the geometry is new.

### 2.3 Nav labels

The user's specification is `[ models ] [ group ]`. The plan renders **`Models`** and **`Group`** — Title Case, matching every other label in the app (`Proxy Settings`, `Models`, `ImgGen Models`, `Decision Models`, `Group Models` — `SettingsPage.tsx:395`/`:437`/`:519`). This is the **only** case deviation from the literal spec text, and it is **RULED** (CMO adjudication E2 — see `## CMO adjudications`); the two nav buttons carry `aria-label="Decision Models"` / `aria-label="Decision Groups"` (the ruling's contract 25, `decisions.md:381`) so the accessible names are unambiguous against the top-level "Models" and "Group Models" tabs.

**Ruling (verbatim):** the commission's own FE requirement asks for styling "consistent with the app's existing design language", and every existing label in this surface is Title Case (`Decision Models`, `Add Decision Model`, group tab labels) — lowercase nav labels would be the only ones in the app. The spec's `[ models ] [ group ]` is read as schematic notation for *two child tabs, vertically stacked*, not literal copy.

**Revert path (one line, no structural change):** the two nav-button text literals in `pkg/ui/frontend/src/components/config/DecisionModelsContainer.tsx` — `Models` → `models`, `Group` → `group` — plus the two matching assertion strings in `pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx` (case A1). The CMO can veto without touching structure.

### 2.4 Mount semantics — conditional render, and the consequence for the in-flight TEST panel

**Decision: conditional render (mount/unmount), matching the house idiom.** The panel is `{child === 'models' ? <DecisionModelsTab/> : <GroupModelsTab/>}` — the same shape as every top-level tab branch (`SettingsPage.tsx:526`, `:607`, `:631`).

**Consequence for the models tab's TEST panel — stated explicitly, as required:**

- `DecisionModelsTab`'s probe state (`testingIds`, `testResults` — `DecisionModelsTab.tsx:92-97`) lives in the component's local state. Switching to the `group` child **unmounts** `DecisionModelsTab`, so that state is discarded: the busy flag ("Testing…") and any rendered result disappear.
- The probe **request itself is not aborted**: `testDecisionModel` → `apiFetch('/decisions/test', …)` with **no AbortSignal** (`useApi.ts:1267-1272`, `:25-33`). The BE keeps running the (real, billable) decision probe; only the FE's rendering of the outcome is lost (the state setter targets an unmounted component).
- Returning to the `models` child shows a fresh, idle list; the operator can re-run the probe. This is exactly the behaviour the operator already has one level up: switching away from the top-level Decision tab today already discards the probe panel (`SettingsPage.tsx:607-619` unmounts the whole branch).

**Why not CSS-hide (keep both panels mounted):**

1. It would keep a **half-filled "Add Decision Model" form alive** across a sibling switch (`DecisionModelsTab.tsx:217-232` mounts the form in place, replacing the whole body) — the exact hazard flagged at `research-fe-precedent.md:56` ("the form's local state must not survive a sibling switch by accident").
2. It would leave two panel subtrees in the DOM (one `display:none`), doubling the tab's DOM weight and creating a second focus-order trap for tooling.
3. There is **no** in-repo precedent for a hidden-but-mounted panel (no `hidden`/`display:none` panel pattern exists in the tab chrome).
4. The house idiom at the top level is already mount/unmount, so CSS-hide would be a *new* pattern introduced in the same commit as a new pattern (the vertical nav) — two first-of-kinds in one change.

**Alternative considered and rejected:** hoisting the probe state into the container so it survives the switch. Rejected — it would require changing `DecisionModelsTab`'s state ownership (i.e. refactoring the component the plan deliberately leaves byte-unchanged) to preserve a result the operator can trivially re-obtain.

### 2.5 Keyboard / click accessibility — what the chosen idiom actually gives the user

Per the ruling's Q5 (`decisions.md:412`, recommendation: stay class-only, no ARIA tab contract), the nav is **plain native buttons + a class-driven active state**, plus `aria-current="true"` on the **active** button only (CMO adjudication E1 — additive: no role, no roving tabindex, no keyboard-behaviour change). There is **no** `role="tablist"`, `role="tab"`, `role="tabpanel"`, `aria-selected`, roving `tabindex`, or arrow-key handling. Verified: the FE has **zero** occurrences of all five (`grep` over `pkg/ui/frontend/src`); the container in this commission becomes the FE's first `aria-current` usage.

**What the user actually gets:**

| Interaction | Behaviour | Why |
|---|---|---|
| Reach the nav | `Tab` key moves focus to **each** nav button in DOM order (they are native buttons, not `tabindex="-1"`). The nav is the first element inside the container, so the order is: nav "Models" → nav "Group" → the panel's own focusables. | Native button focusability. |
| Activate | `Enter` and `Space` activate the focused button — the browser's native button behaviour; **no JS key handler is needed or written**. | Native `<button>` semantics. |
| Arrow keys | **Do nothing.** There is no roving tabindex and no `ArrowLeft`/`ArrowRight`/`ArrowUp`/`ArrowDown` handler. | The ARIA-tablist pattern is out of scope per Q5. |
| Click | `onClick` switches the child panel. Identical to keyboard activation (same handler). | — |
| Form safety | Every nav button carries `type="button"` — `Enter` inside a form mounted in the panel can never submit through a nav button. This mirrors the house rationale at `SettingsPage.tsx:383-385`. | House discipline. |
| Active state | **Visual + programmatic** — `text-blue-400` + `border-l-2 border-blue-400` on the active item, `text-gray-400 hover:text-white` on the inactive; the active item additionally carries `aria-current="true"` and the inactive item **omits** the attribute entirely (never `aria-current="false"`), so the active state is perceivable to assistive tech. **Keyboard and mouse behaviour are unchanged by this attribute.** | Class-driven per Q5, plus the additive `aria-current` approved in the CMO adjudication E1 (no role, no roving tabindex, no keyboard change). |

**Remaining honest limitation:** with `aria-current="true"` on the **active** nav button (CMO adjudication E1 — see `## CMO adjudications`), the active item's state is perceivable to assistive tech. What the FE still does **not** have is **ARIA tab semantics** — no `role="tablist"` / `role="tab"` / `role="tabpanel"` / `aria-selected`, no roving tabindex, and no arrow-key navigation; the nav-to-panel relationship is conveyed by DOM order and the conditional mount, exactly as the top-level tab bar does today. **Keyboard and mouse behaviour are unchanged by E1:** `Tab` reaches both nav buttons, `Enter`/`Space` activate, and the arrow keys remain inert.

**Test-design consequence (honest):** happy-dom does **not** synthesise `Enter` → `click`. A test cannot assert "Enter activates the nav" by firing a keydown. The test therefore pins the **preconditions** that make native keyboard activation work — native `<button>` element, `type="button"`, not `disabled`, **no** `tabindex="-1"` — plus the observable consequence (a click/activation switches the panel), and separately pins that no ARIA tab roles were introduced. This is stated so the approver does not read a passing test as "we tested Enter".

### 2.6 Why vertical-left, and why it is a deliberate first-of-kind

The user asked for a **vertical** left nav; the ruling's contract 24 records the same shape (`decisions.md:380`). The justification recorded here:

1. **It is the FE's first nested-tab / vertical nav.** There is no precedent to mirror (zero `role="tablist"`, zero `subtab`/`sidebar`/`nav-item`, zero `border-r`+`flex-col` nav — only the `RequestList.tsx:51` grid column). So the plan **defines** the idiom rather than claiming consistency with an existing one.
2. **It is a deliberate, minimal first-of-kind:** two utilities are new (`border-l-2`, `border-l-transparent`) and one layout pattern is new (a left column of buttons). Everything else is lifted from the top bar's class pair and the app's `border-r` divider idiom.
3. **Why left (not right, not horizontal):** the two children are unequal in weight — the models list is the primary surface and the group surface is secondary. A left rail keeps the primary content on the left, matching the reading order of the top bar (the active marker travels **downward** as the user descends from the top bar into the child nav, and the child's active marker travels **rightward** across the column).
4. **Why not a horizontal sub-bar:** the top bar already is a horizontal tab strip; a second horizontal strip directly beneath it would be visually confusable with it and would break the `div.flex.overflow-x-auto > button` selector that `SettingsPage.tabs.test.tsx` uses to count top-level tabs (`:126-128`, `:282-284`) — the ruling's contract 24 makes avoiding that selector a requirement, not a preference.

---

## 3. Data layer changes

### 3.1 Reused as-is — no new hook, no new endpoint, no new cache key

| Concern | Decision |
|---|---|
| Decision models list | **Reuse `useDecisionModels()` unchanged** (`useApi.ts:532-613`; URL `?kind=decision` `:545`; key `'decision-models'` `:544`). The models panel is fed exactly as today. |
| Group list (both lanes) | **Reuse `useGroupModels()` unchanged** (`useApi.ts:636-713`; URL `?kind=group` `:649`; key `'group-models'` `:648`). One fetch feeds both group surfaces; the FE splits it by lane (see 3.2). |
| Decision member candidates | **Reuse the server-prefiltered `decisionModels` list** (`useDecisionModels` output) as the decision lane's member + shadow candidate list. No client-side filter over the unfiltered list is needed for the decision lane — the server already did it. |
| Text member candidates | **Unchanged**: `chatKindModels` (`SettingsPage.tsx:293-295`), a client filter over `models`. Byte-identical. |
| Mutations | **Unchanged**: `POST/PUT/DELETE /fe/api/models` via `useDecisionModels` / `useGroupModels`. No new route. |
| **Cache-key invalidation** | **NO extension.** The existing **quadruple** already covers every list this commission touches: `'decision-models'`, `'models'`, `'imggen-models'`, `'group-models'` are deleted by every model mutation in both hooks (`useApi.ts:572-575/:588-591/:599-602` and `:674-677/:689-692/:699-702`). Creating a decision group goes through `useGroupModels().addModel`, which deletes all four and refetches `'group-models'`; the decision-model list is invalidated in the same breath. **No fifth key is added** — adding one would be a gratuitous deviation from the house "quadruple in lock-step" invariant (the file-level comment at `useApi.ts:525-531` forbids prefix deletion and mandates an explicit list). |

### 3.2 How the group list is scoped to decision groups — **client-side, by the same rule the server uses**

**The problem:** there is no server-side lane filter and no lane field. `GET /fe/api/models?kind=group` returns **every** group row (`pkg/ui/server.go:788-795` — `kindFilter = func(m) { return m.IsGroup() }`), and a decision group *is* a `kind='group'` row (ruling D-1). `grep -rn "GroupLane" pkg/` → 0 matches: the ruling's lane seam is a phase-1 deliverable that does not exist in the tree this plan is written against.

**The decision:** the FE classifies each group **client-side, by the identical rule the server uses at request time** (ruling D-2, `decisions.md:137`): *the lane is inferred by unanimity over the CURRENT member rows; `""` normalises to chat and is accepted ONLY as chat; a mixed or unresolvable member set is unservable.*

`groupLane.ts` implements:

| Function | Rule |
|---|---|
| `normalizeKind(kind?: string)` | `undefined`/`''` → `'chat'`; `'chat'`/`'image-gen'`/`'decision'`/`'group'` → itself. (Mirrors the server's legacy-`''`-is-chat normalisation, `pkg/store/database/store_group_rewrite.go:291-301`.) |
| `buildKindIndex(lists)` | Builds `Map<model_id, kind>` from the union of the **server-prefiltered** lists, with the narrow list winning over the unfiltered one: `decisionModels` → `'decision'`, `groupModels` → `'group'`, `imgGenModels` → `'image-gen'`, `models` → `row.kind ?? 'chat'`. The narrow lists win because the unfiltered `models` array is **not** refreshed by another hook's mutation (each hook deletes the `'models'` cache key but only refetches *its own* list — `useApi.ts:572-575` vs `useModels`'s own `fetchModels`), so a newly created decision model can be missing from `models` while it is present in `decisionModels`. |
| `inferGroupLane(group, kindIndex)` | `members = group.members ?? []`. **0 members → `'chat'`** (legacy/pre-homogeneity row; it must never vanish from the tab where it has always lived). **All members resolve to chat-or-`''` → `'chat'`.** **All members resolve to `'decision'` → `'decision'`.** Anything else (a mixed set, or a member id that is not resolvable) → **`null` = undecidable**, i.e. fail-closed, mirroring the server's "unservable" outcome. |
| `splitGroupsByLane(groups, kindIndex)` | Returns `{ chat, decision, unknown }`: `chat` = lane `'chat'`; `decision` = lane `'decision'`; `unknown` = `null`. |

**Where the split is applied:** in `SettingsPage.tsx`, next to the existing `chatKindModels` (`:293-295`) — the parent already owns model-kind projection, so this is the idiomatic place. The **text** `GroupModelsTab` receives `chat + unknown`; the **decision** container's group panel receives `decision` only.

**Why "unknown" stays with the text tab (and is never hidden from both):**

- A group row must never become unreachable. The operator must be able to see and repair it.
- Under the ruling, every group that exists today is chat-lane (G3 admits only chat members; write-time homogeneity from phase 1 makes mixed groups unwritable), so at day-1 the text tab's visible set is **unchanged** — the split only starts moving rows once decision groups exist.
- An `unknown` row is already broken from the server's point of view (mixed member kinds ⇒ unservable; a missing member ⇒ G3 fails). It belongs where the operator will find it next to its historical home, and the text form is where the fix is cheapest (replace the member with a chat row).

**Why not a server-side filter:** adding `?kind=group&lane=decision` would require a phase-1 BE change that is **not** in the ruling's contract list (`decisions.md:362-400`), and the ruling explicitly refuses a persisted lane field (`decisions.md:137`). If phase 1 does add a `GroupLane` seam, this is the **cross-phase coupling to revisit** — see `## Coupling map` and `## Escalations`.

**Server-side validation remains authoritative in every case.** The FE split is a *display* filter; every write still goes through the server's widened G3 / homogeneity / G8 validators (`pkg/models/group.go:60-94`, `:115-214`), which are the authority on whether a group is legal.

> **AMENDED 2026-11-16 by Amendment 2 (review warning 14):** the original text stated only the 15 s cache TTL and **understated** the bound. BOTH bounds:

> **Bound 1 — the 15 s cache TTL (cache-reuse only).** `getOrFetch(key, fetcher, 15000)` — `useApi.ts:285-292` (`'models'`), `:544-551` (`'decision-models'`), `:648-655` (`'group-models'`). It bounds how long a triggered refetch may reuse a cached response; within 15 s of the last fetch, even the error-banner **Retry** button (`useApi.ts:608`/`:708` → `refetch`) returns the cached — possibly stale — response.
>
> **Bound 2 — unbounded for the live session (the real bound).** The model hooks are mounted **once**, in `App.tsx` (`useDecisionModels` `App.tsx:37`, `useGroupModels` `App.tsx:43`), and each fetches **once on its own mount** (`useApi.ts:388-392` models, `:605-610` decision, `:705-710` group). There is **no polling and no TTL-driven refetch of the hook state** — the 15 s TTL governs only the shared `defaultAPICache`, not the hooks' `useState`. A mounted surface therefore refreshes **only** on (a) a mutation issued through the **same** hook (`addModel`/`updateModel`/`deleteModel` → `fetchModels()`, `useApi.ts:572-575` decision / `:674-677` group), (b) a **Retry** press (bound 1 applies), or (c) a full app reload. Another surface's mutation deletes the cache key but does **not** refresh this hook's state. **So a mis-filed group stays mis-filed for as long as the session lives** — minutes or hours, not 15 s.
>
> **Consequence (unchanged in kind, corrected in magnitude):** a decision group can appear in the text tab (and vice versa) for the whole session. It cannot produce an illegal write — the server rejects a mixed-lane write — and the lane-flipping residual is now closed by the **repaired** lane-consistency guard (§5.2, Amendment 2 warning-11 fix). Optional mitigation **not** implemented (not ruled): call `refetchDecisionModels()`/`refetchGroupModels()` once on the container's mount, which would collapse bound 2 to bound 1; flagged in `## Amendment 2` for the leader.

### 3.3 The member picker feed

| Lane | Feed | Source |
|---|---|---|
| chat (today) | `chatKindModels` — a client filter over `models` (`SettingsPage.tsx:293-295`) | unchanged |
| decision (new) | `decisionModels` — the server-prefiltered `?kind=decision` list | new consumer of an existing list |

**Candidate eligibility (AMENDED 2026-11-16 by Amendment 1 — ADR-7 / AC-R6, ADR-11 OQ-4).** The decision lane's member picker must offer only **servable candidates**. At minimum: `enabled === false` models are excluded.

**Two populations — stated explicitly (ADR-11 OQ-4, `decisions.md:775`):** "Chain targets and group members are two populations — do not conflate them in validation or in tests."

1. An **inactive / chain-only** model is a **valid chain target** and is **not a defect**. The FE must never warn about it, never flag it as broken, never offer a cleanup affordance. (The decision-models list shows only its enable toggle — `DecisionModelsTab.tsx:281-291` — and says nothing else; unchanged.)
2. It is **not a group candidate**: the decision-lane member picker never lists it (AC-R6).
3. The **shadow-target picker** is a third population (an ID claim — neither a chain target nor a membership). ADR-7 rules on *group candidates* only, so the shadow list keeps the lane-kind filter and is **not** narrowed to enabled models. That narrowing is **not ruled** — flagged in `## Amendment 1` as an open, one-line question for the leader, **not** implemented here.

**The `laneModels` derivation is deliberately NOT changed** (the decision lane keeps the full server-prefiltered `?kind=decision` list). Reason: the same list also feeds the shadow-target picker (`GroupModelForm.tsx:381`), and narrowing it at the derivation site would silently narrow the shadow population too. The change lands in **one** place — the member picker filter (`GroupMemberEditor.tsx:40`).

`GroupMemberEditor`'s **own** defensive filter (`:40`) is the real guarantee and becomes lane-driven:

```
lane === 'chat'     → pickerModels = laneModels.filter(m => !m.kind || m.kind === 'chat')          // UNCHANGED (byte-for-byte)
lane === 'decision' → pickerModels = laneModels.filter(m => m.kind === 'decision' && m.enabled)    // NEW (ADR-7 / AC-R6)
```

Note the asymmetry, which is deliberate and matches the server (ruling D-2 contract 3, `decisions.md:370`): a kindless (`''`) row is chat and **never** decision.

---

## 4. Chat-only filter sites — per-site verdict

The ruling's blast radius lists six FE sites (`decisions.md:345-352`). Verdict per site, with the exact change and the text-lane consequence:

| # | Site | Rule today | **Verdict** | Exact change | Text-lane non-regression consequence |
|---|---|---|---|---|---|
| 1 | `pkg/ui/frontend/src/types.ts:168-186` (GroupMember doc block) | "an EXISTING **chat-kind** model" | **DOC-ONLY UPDATE** | Reword to "a model of the group's lane (chat-kind or decision-kind); `''` counts as chat". **No type change** — `GroupMember` stays `{model_id, weight, backup_only?}` (`:182-186`); a member carries no kind field, by design. | None — comment only. |
| 2 | `pkg/ui/frontend/src/components/config/GroupMemberEditor.tsx:40` | `chatModels.filter(m => !m.kind \|\| m.kind === 'chat')` | **PARAMETERIZE** | Add required `lane: GroupLane`; rename the prop `chatModels` → `laneModels`; filter becomes lane-driven (§3.3). Placeholder (`:105`) and hint (`:190`) become lane-aware. **AMENDED 2026-11-16 by Amendment 1 (ADR-7 / AC-R6):** the `lane='decision'` branch additionally excludes **inactive** models (`&& m.enabled`). | The `lane='chat'` branch is the **same predicate, character-for-character** (`!m.kind \|\| m.kind === 'chat'`) — **including no `enabled` filter**: the text lane's inactive-member behaviour is AC-R12 (ADR-9)'s business (`decisions.md:695-720`), not this release's. Existing fixtures (chat, kindless, image-gen, decision, group) keep their expectations. |
| 3a | `pkg/ui/frontend/src/components/config/GroupModelForm.tsx:49-53` (prop doc) + `:53` | "Chat-kind models only … G3/G8 only ever accept chat-kind targets" | **PARAMETERIZE** | Add required `lane: GroupLane`; rename `chatModels` → `laneModels`; doc reworded to "models of this group's lane (member + shadow candidates)". | The `lane='chat'` path is unchanged in behaviour; only the prop name and the doc change. |
| 3b | `pkg/ui/frontend/src/components/config/GroupModelForm.tsx:195-207` (G8 shadow mirror) | `!chatModels.some(m => m.id === shadow)` → "must reference an existing chat-kind model" | **PARAMETERIZE** | The existence check runs against `laneModels`; the error copy becomes lane-aware; the dual-claim scan (`:203-206`) uses the **lane's** group list. | With `lane='chat'` + `laneModels=chatKindModels`, the predicate and the message are identical to today. The dual-claim scope is unobservable for valid data (a chat id and a decision id cannot coincide — see §5.4). |
| 4 | `pkg/ui/frontend/src/components/SettingsPage.tsx:293-295` (`chatKindModels`) | client filter over `models` | **KEEP + ADD** (not replace) | `chatKindModels` stays **byte-identical**; a new `decisionKindModels` (or direct use of the server-prefiltered `decisionModels`) is added for the decision lane; the lane split is computed alongside. | Zero change to the chat filter predicate. |
| 5 | `pkg/ui/frontend/src/components/config/ModelsTab.tsx:363` | chat filter | **UNCHANGED** | None. | N/A — the chat tab's filter is not a lane site. |
| 6 | `pkg/ui/frontend/src/components/config/GroupModelsTab.tsx:303` (`chatModels={chatModels}`) | pass-through | **PARAMETERIZE** | Add required `lane: GroupLane`; rename the prop and the pass-through; lane-aware copy (§1.4). | The `lane='chat'` render path is unchanged; only the prop name/copy branches change. |

**Summary of the strategy:** **one component, two feeds** — `GroupMemberEditor`, `GroupModelForm` and `GroupModelsTab` are each **parameterized**, never forked. There is **no** `DecisionGroupMemberEditor`, no `DecisionGroupForm`, no `DecisionGroupsTab`. The only new FE source files are the container and the pure lane helper.

**Why a required (not defaulted) `lane`:** a defaulted `lane` would let a call site silently fall back to `'chat'` and render a chat-only picker for a decision group — a *visible* but confusing failure. A required prop makes every call site state its lane, and a missed call site is a **compile error** (caught by the tsc-baseline gate, §7). The cost is that the three existing test fixtures must add `lane="chat"` — mechanical, assertions unchanged.

**Prop rename (`chatModels` → `laneModels`) rationale:** keeping the name `chatModels` for a list that carries decision models is actively misleading, and the prop now feeds *both* the member picker and the shadow picker — neither of which is "chat" any more. `laneModels` names the binding (the lane) rather than the content. The rename touches exactly four source files and three test files, all listed in §1.2 and §6.

---

## 5. Validation mirrors (client-side) — the server stays authoritative

**Posture (house rule, from `group-backup-members/design.md` D2):** the client-side mirrors are *belt-and-suspenders* gates that surface a clear message before the request round-trips. **Server-side validation remains authoritative in every case** — the widened `pkg/models/group.go:60-94` / `:115-214` validators and the inline mirrors at `pkg/ui/server.go:941-944` / `:1443-1444` decide legality.

### 5.1 Rule-by-rule mirror table

| Server rule | Where it lives (BE) | FE mirror today | FE mirror for the decision lane | Status |
|---|---|---|---|---|
| G2 — ≥1 member | `pkg/models/group.go:126-128` | `GroupModelForm.tsx:161-162` | **Reused as-is** (lane-independent) | unchanged |
| G2 — cap 16 | `pkg/models/group.go:126-128`; `MAX_GROUP_MEMBERS` | `GroupModelForm.tsx:163-165`; `GroupMemberEditor.tsx:6`, `:43`, `:180` | **Reused as-is** (the constant stays 16; the Go constant `pkg/models/group.go:25` is the reference) | unchanged |
| G2 — every row picked | `pkg/models/group.go` (G3 arm) | `GroupModelForm.tsx:166-170` | **Reused as-is** | unchanged |
| G3 — weight > 0 | `pkg/models/group.go:180-184` arm | `GroupModelForm.tsx:171-175` | **Reused as-is** | unchanged |
| G3 (widened) — member kind == group's lane | ruling blast radius #1 | none (the picker filtered chat) | **NEW** — "Member X is not a decision-kind model" | **new mirror** |
| **Homogeneity (NEW rule)** | ruling blast radius #3 (a new BE rule, phase 1) | none | **NEW** — the picker makes a mixed list unconstructible; the submit-time check makes a *pre-existing* mixed list unsavable | **new mirror** |
| D2 (group-backup-members) — ≥1 non-backup member | `pkg/models/group.go` (≥1 default) | `GroupModelForm.tsx:182-186` | **Reused as-is** | unchanged |
| G4 — duplicate member id | `pkg/models/group.go` | warn-only in the editor (`GroupMemberEditor.tsx:81-85`, `:169-175`) | **Reused as-is** (warn-only, Q10) | unchanged |
| G5 — no group-as-member | `pkg/models/group.go:176-179` | structural (the picker offers only lane models, never group rows) | **Reused as-is** — structurally impossible on the decision lane too | unchanged |
| G8 (widened) — shadow target kind == lane | ruling blast radius #2 | `GroupModelForm.tsx:195-207` | **NEW** — the shadow must be a decision-kind model; copy lane-aware | **widened mirror** |
| G8 — no dual claim by another enabled group | `pkg/models/group.go` | `GroupModelForm.tsx:203-206` | **Reused**, scoped to the lane's group list (unobservable — §5.4) | unchanged logic |
| D5–D8 (decision model rules) | `pkg/models/config.go:277-320` | `DecisionModelForm.tsx:215-234` | **N/A** — the decision-model form is not touched | unchanged |
| Provider vocabulary | `pkg/models/config.go:293-300` + 4 BE sites | `DecisionModelForm.tsx:29` | **N/A** — unchanged, pinned | unchanged |
| **Candidate eligibility — a group member must be servable (`enabled`)** | ADR-7 / AC-R6 (`decisions.md:670-672`); the server-side candidate list excludes them too (`decisions.md:665-666`) | none (the picker filtered by kind only) | **NEW** — the decision-lane member picker excludes `enabled === false` models (`GroupMemberEditor.tsx:40`); the **text** picker is unchanged (ADR-9) | **new mirror** (AMENDED 2026-11-16) |
| **No silent auto-enable** | ADR-7 / AC-R7 (`decisions.md:673`); the BE save/validation path owns it (phase 1) | structurally satisfied — the member wire shape has no `enabled` key (`types.ts:182-186`) | **N/A — no FE change needed**; pinned by a payload-shape test (D8) | **confirmed** (AMENDED 2026-11-16) |
| **No ≥2-member rule** | ADR-8 / AC-R8 + ADR-11 OQ-3 (option (a), `decisions.md:765-769`) | the G2 mirror is `members.length < 1` (`GroupModelForm.tsx:161-162`) — i.e. **≥1**, not ≥2 | **Reused as-is — a single-member decision group is VALID and must not be blocked**; its resilience is consumed server-side (per-credential attempts within the member). Pinned by D9 | **confirmed** (AMENDED 2026-11-16) |

### 5.2 The lane-consistency guard (mandatory, new)

> **AMENDED 2026-11-16 by Amendment 1 (ADR-1, `decisions.md:426-460`):** ADR-1 makes a group member's **kind immutable** (the fence is the model-update path, not the group-save path) and closes the single-member re-lane hole. This guard therefore becomes **defence-in-depth** rather than the primary fence — keep it. The FE's model-edit surface is unchanged by this commission; a retyping attempt is rejected by the BE and the message surfaces verbatim through `apiFetch` (`useApi.ts:36-38`).

**Rule:** in **edit** mode, the form refuses to save a group whose **current** member set is not lane-consistent with the tab it was opened from.

Rationale: the FE split is a cached, client-side inference (§3.2). A decision group can be mis-filed into the text tab for up to one cache TTL. If the operator then edits it in the text tab, the text form's picker offers only chat models; saving would **rewrite the group's members to chat models**, silently **flipping the group's lane** — a legal write under the widened rules, and therefore invisible to the server. The guard closes that hole at the FE, cheaply, without a BE change.

Implementation: in `GroupModelForm.handleSubmit`, when `mode === 'edit'` and `initialData?.members` contains any member id that is **not** in `laneModels` (i.e. the stored group is not lane-consistent), block the submit with a lane-specific message — e.g. *"This is a decision group — edit it under Decision Models → Group."* (the chat-lane twin: *"This group is not a chat group — edit it under Decision Models → Group."*). The message names the other surface so the operator is routed to the right tab.

> **AMENDED 2026-11-16 by Amendment 2 (review warning 11 — the guard degenerated to delete+recreate).** As written, the guard fired on **any** member id outside `laneModels` — including when the stored group's lane is **unknown** (a mixed or unresolvable member set). An unknown-lane group is listed **only** in the text tab (§3.2) and is **not** listed in the decision tab, so the guard made it **uneditable in BOTH tabs** — the operator's only path was delete + recreate, losing the row, the id, the shadow claim and the enable state (and risking the 409 member-of-another-group rejection). **Encoded fix (minimal):**
>
> 1. `SettingsPage` passes the **kind index it already builds** for the lane split (§3.2) down to the form as one new optional prop — `kindIndex?: Map<string, string>` (`GroupModelForm.tsx`).
> 2. The guard computes the stored group's lane with the **existing** helper — `inferGroupLane(initialData, kindIndex)` (`groupLane.ts`) — instead of pattern-matching on `laneModels` membership.
> 3. It blocks **only when the stored lane is KNOWN and differs from the form's lane** (`storedLane !== null && storedLane !== lane`).
> 4. When `storedLane === null` — unknown, mixed, or an unresolvable member — the guard **does not block**; the submit proceeds and the **server's** validators decide (a mixed write is rejected by write-time homogeneity; a missing member by G3). An unknown-lane group is a legacy/hand-edited artifact (ADR-15 keeps `groupLaneOf` fail-open for exactly those, `decisions.md:913-916`) — it is the row an operator most needs to **repair**, so blocking its edit was the bug.
> 5. If `kindIndex` is not supplied, the guard falls back to the original conservative rule (block) — defence-in-depth default; the parent always supplies it.
>
> **Residual (stated, accepted):** with the fix, an operator **can** deliberately re-lane a broken (unknown-lane) group by rewriting its members in the text form — a legal write the server accepts. That is strictly better than the delete+recreate dead end: the group was already unservable, so nothing that worked is lost. ADR-1's kind-immutability fence (`decisions.md:426-460`) keeps the retyping path closed, so unknown-lane groups arise mainly from legacy/hand-edited rows — precisely the repair case.

This is a **belt-and-suspenders** guard like the rest: the server cannot detect it (both writes are legal), so the FE is the only place it can be caught.

### 5.3 The homogeneity mirror (new, decision lane)

Rule: every member of a decision group must be a decision-kind model. Two enforcement points:
1. **Constructive** — the member picker offers only decision models (§3.3), so a mixed list cannot be built through the UI.
2. **Submit-time** — the same check as §5.2 but for *staged* members: any staged member id that is not a decision-kind model (e.g. a stale row left over from an edit of a chat group, or a retyped member) blocks the submit with "Member X is not a decision-kind model".

### 5.4 The G8 dual-claim scope — why it is unobservable

The dual-claim scan for the decision lane runs over the **decision-lane** group list. Could a chat group's claim collide with a decision group's claim? **No**: the widened G8 requires the shadow target's kind to equal the group's lane (ruling blast radius #2), so a chat group can only claim a chat-kind id and a decision group only a decision-kind id. A model row has exactly one kind ⇒ the two id-spaces are disjoint ⇒ the scan's scope is unobservable for legal data. The server remains the authority for the (illegal) mixed case.

---

## 6. Tests

**House conventions honoured** (`research-fe-precedent.md:221-259`): every test imports `describe/it/expect/vi` from `'vitest'` (globals are **off**, `vitest.config.ts:11`); tests live at `src/**/__tests__/**/*.test.{ts,tsx}` (`vitest.config.ts:13`); `beforeEach(() => { document.body.innerHTML = ''; })` + `afterEach(() => { cleanup(); vi.clearAllMocks(); })`; assertions are on `data-testid` selectors; `fireEvent` from `@testing-library/preact`.

### 6.1 New test files

**A. `pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx`** (NEW)

| # | Case | Pins |
|---|---|---|
| A1 | renders exactly two nav items, labelled `Models` and `Group`, in that order | the `[models] [group]` requirement |
| A2 | the nav is the **first** element of the container and the panel follows it | vertical-LEFT ordering (DOM order) |
| A3 | the nav column is `flex-col` and does **not** carry `overflow-x-auto` | vertical, not horizontal |
| A4 | the nav buttons are **not** matched by `div.flex.overflow-x-auto > button` | the top-level-tab selector cannot see them (ruling contract 24) |
| A5 | the `models` child is the default: the models panel is mounted and the group panel is not | default child = models |
| A6 | clicking `Group` swaps the panel (group list present, models list absent) **and the `aria-current="true"` attribute moves to the newly activated button (the previously active button loses the attribute entirely)** | panel switching + E1's attribute-follows-the-click |
| A7 | the active nav item carries `text-blue-400` + `border-l-blue-400`; the inactive carries `text-gray-400` + `hover:text-white`; exactly one item is active; **the active item carries `aria-current="true"` and the inactive item has NO `aria-current` attribute at all** (never `aria-current="false"`) | the active/inactive class pair (translated from `SettingsPage.tsx:388-391`) + E1's active-only attribute |
| A8 | each nav button is a native `<button type="button">`, is not `disabled`, and has **no** `tabindex="-1"` | the preconditions for native Enter/Space activation (happy-dom does not synthesise Enter→click — see §2.5) |
| A9 | each nav button carries a `data-testid` and an `aria-label` | ruling contract 25 |
| A10 | **no** `role="tablist"` / `role="tab"` / `role="tabpanel"` / `aria-selected` appears in the container | ruling Q5 / contract 25 (class-only) |
| A11 | the models panel mounts the existing decision list unchanged (the "No decision models configured" empty state + the "Add Decision Model" CTA appear) | the wrap-not-refactor decision (§1.3) |
| A12 | switching to `group` **unmounts** the models panel (the in-flight probe panel is discarded; the probe request itself is not aborted) | the §2.4 consequence, pinned |
| A13 | the container passes the models panel's props through verbatim (e.g. `onNavigateToCredentials` is invoked) | §1.3 pass-through |

**B. `pkg/ui/frontend/src/components/config/__tests__/GroupModelsTab.decisionLane.test.tsx`** (NEW — house naming mirrors `ModelsTab.chatFilter.test.tsx` / `DecisionModelsTab.decisionTest.test.tsx`)

| # | Case | Pins |
|---|---|---|
| B1 | `lane='decision'` renders "Decision Groups", "Add Decision Group" and the "No decision groups configured" empty state | lane-aware copy |
| B2 | the decision-lane empty state states the **full** semantics: members picked by weight per request; no per-conversation pinning; resilience = per-credential attempts within a member; a fallback chain has "no equivalent concept on this lane; deliberately not built" | ruling Q6 + ADR-11 OQ-2/OQ-3 (AMENDED 2026-11-16) |
| B3 | the tab renders exactly the groups it is given (parent-scoped; no client-side re-filter) | the house "server-side pre-filter trust" idiom (`DecisionModelsTab.test.tsx:199-…`) |
| B4 | add → the payload is stamped `kind:'group'`, `internal:true`, empty `fallback_chain`, empty `credentials`, with the decision members | G1/G6/G7 stamping |
| B5 | edit → the PUT body carries `name, enabled, kind, internal, members, shadow_model_id, fallback_chain, credentials` (no sparse-PUT) | `GroupModelsTab.tsx:115-124` |
| B6 | the delete-confirm modal + the decision-lane toast copy | delete path |
| B7 | the decision lane offers **no** one-click-wrap pre-fill (the wrap signal is text-lane only) | the wrap feature stays chat-only |
| B9 | the decision-lane group row carries the **🎯** glyph, **not** the 🔀 load-balancing glyph (the lane has no racing/hedging engine, so 🔀 would promise resilience that does not exist) | ADR-8 / ADR-11 OQ-3 option (a) (AMENDED 2026-11-16) |

**C. `pkg/ui/frontend/src/components/config/__tests__/GroupMemberEditor.decisionLane.test.tsx`** (NEW)

| # | Case | Pins |
|---|---|---|
| C1 | the picker offers **only** decision-kind models — chat, kindless, image-gen and group rows are absent | the lane filter (mirrors `GroupMemberEditor.test.tsx:55-71`) |
| C2 | the placeholder is "Select a decision model…" | lane-aware copy |
| C3 | weight editing + `backup_only` toggling + remove behave identically to the chat lane (the `backup_only` key is **dropped** when false — Go `omitempty` parity, `GroupMemberEditor.tsx:61-74`) | parity |
| C4 | "Add Member" is disabled at 16 (`MAX_GROUP_MEMBERS`, `:6`) | cap |
| C5 | the duplicate-member hint is shown and is **warn-only** (no hard block) | Q10 |
| C6 | the picker offers only **enabled** decision-kind models — an **inactive** decision model is absent from the options | ADR-7 / AC-R6 (AMENDED 2026-11-16) |
| C7 | the editor is **silent** about the excluded inactive model (no warning, no "broken"/"disabled" defect copy — an inactive/chain-only model is not a defect) | ADR-7 R4 (`decisions.md:660`) (AMENDED 2026-11-16) |

**D. `pkg/ui/frontend/src/components/config/__tests__/GroupModelForm.decisionLane.test.tsx`** (NEW)

| # | Case | Pins |
|---|---|---|
| D1 | the submit mirrors fire: ≥1 member, >16 rejected, unpicked row rejected, `weight>0` required, ≥1 non-backup member required | §5.1 |
| D2 | **fixture shift (ADR-19):** a **genuinely mixed SURVIVING** set (not a vanished member — a vanished member is SKIPPED per E4) is rejected before the round-trip | the homogeneity mirror (§5.3) — server `pkg/models/group_lane.go:46-101` |
| D3 | the shadow target must be a decision-kind model (a chat-kind shadow id is rejected with the lane-aware copy) | the widened G8 mirror (§5.1) |
| D4 | the shadow dual-claim scan rejects a second **decision** group claiming the same id | G8 uniqueness |
| D5 | the payload is asserted as an **exact key set** (`toEqual` on the whole payload): `id, name, kind:'group', internal:true, members, shadow_model_id, enabled, fallback_chain:[], credentials:[]` — any extra key (e.g. a provider field) fails | G1/G6/G7 — **AMENDED 2026-11-16 by Amendment 2 (warning 13)**: exact-shape subsumes the removed B8/D7 intent |
| D6 | **edit mode** — **B2 (approver escalation): each sub-case is its own TOP-LEVEL test with the case id FIRST in the title** (`D6a — …`, `D6b — …`, `D6c — …`, `D6d — …`): **D6a** a **known-lane decision** group opened in the text form is blocked with the cross-tab message — **EXTENDED (ADR-19): a decision group with a VANISHED member is still known-lane** (the vanished member is skipped, the survivors decide ⇒ `decision`) and **still blocked**; **D6b** a known-lane chat group opened in the decision form is blocked; **D6c** an **unknown-lane** group — unknown = the surviving set is **mixed OR empty** (ADR-19 correction; a vanished member alone does NOT make it unknown) — opened in the text form is **NOT blocked** (the submit proceeds; the server's verdict is authoritative), **with a mixed-surviving variant added**; **D6d** with `kindIndex` absent the conservative fallback blocks | the lane-consistency guard (§5.2, repaired in Amendment 2 — warning 11; re-pinned to the server fold by ADR-19) |
| D8 | the submitted payload's `members[]` entries carry **only** `model_id`, `weight` (+ optional `backup_only`) — **no `enabled` key** on any member; the group's own `enabled` flag is unchanged by adding a member | ADR-7 / AC-R7 (AMENDED 2026-11-16) |
| D9 | a **single-member** decision group submits successfully — the FE imposes **no ≥2-members rule** | ADR-8 / AC-R8 + ADR-11 OQ-3 (option (a)) (AMENDED 2026-11-16) |
| D10 | the decision-lane member hint states per-credential resilience within a member and makes **no** chain / racing / hedging promise | ADR-11 OQ-2 + OQ-3 (AMENDED 2026-11-16) |

**E. `pkg/ui/frontend/src/components/config/__tests__/groupLane.test.ts`** (NEW — pure helper)

| # | Case | Pins |
|---|---|---|
| E1 | a group whose members are all chat/kindless → `'chat'` (the `''` normalisation) | D-2 mirror |
| E2 | a group whose members are all decision → `'decision'` | D-2 mirror |
| E3 | a mixed group → `null` (undecidable, fail-closed) | D-2 mirror |
| E4 | **CHANGED (ADR-19 — the server fold is authoritative):** an **unresolvable / vanished** member id is **SKIPPED** and the **survivors decide** — `[decision, vanished] ⇒ 'decision'`; **only-vanished ⇒ `null`** | D-2 mirror — server `pkg/models/group_lane.go:46-101`, `pkg/store/database/store_group_rewrite.go:369-384` |
| E5 | **CHANGED (ADR-19):** **zero surviving members ⇒ `null`** — and the row **stays visible in the text tab** (never silently hidden) | D-2 mirror — server `pkg/models/group_lane.go:137-154` (the collection-shaped twin the FE mirrors) |
| E6 | **fixture re-pinned (ADR-19):** the split puts `chat` + `unknown` in the text list and `decision` **only** in the decision list — no row is invisible in both tabs; exercised with a **mixed set** and an **all-vanished** set | §3.2 — server `pkg/models/group_lane.go:46-101` |
| E7 | the kind index prefers the narrow server-prefiltered lists over the unfiltered list | §3.2 (`buildKindIndex`) |
| **E8** | **NEW (ADR-19):** a member of **no lane** — image-gen / group / out-of-vocabulary provider — is **IGNORED** by the fold (it does not make the group undecidable) | server `pkg/models/group_lane.go:52-53` + `:67-71` |

### 6.2 Existing test files — mechanical updates (prop rename + required `lane`)

| File | Change | Assertions |
|---|---|---|
| `pkg/ui/frontend/src/components/config/__tests__/GroupModelsTab.test.tsx` | fixtures add `lane="chat"`; `chatModels` → `laneModels` | **unchanged** |
| `pkg/ui/frontend/src/components/config/__tests__/GroupModelForm.test.tsx` | fixtures add `lane="chat"`; `chatModels` → `laneModels` | **unchanged** |
| `pkg/ui/frontend/src/components/config/__tests__/GroupMemberEditor.test.tsx` | fixtures add `lane="chat"`; `chatModels` → `laneModels`; **+ T1** (AMENDED 2026-11-16) | existing assertions **unchanged** |
| `pkg/ui/frontend/src/components/__tests__/EventLog.test.tsx` | **+ F1** (AMENDED 2026-11-16) | existing assertions **unchanged** |

**New pins added to the updated files (AMENDED 2026-11-16):**

| ID | File | Case | Why |
|---|---|---|---|
| **T1** | `components/config/__tests__/GroupMemberEditor.test.tsx` | the **text**-lane picker still offers an **inactive** chat model — the decision-lane candidate-eligibility exclusion must **not** leak into the text lane | AC-R12 (ADR-9) (`decisions.md:695-720`): text-lane behaviour is byte-untouched by this release |
| **F1** | `components/__tests__/EventLog.test.tsx` | an event type **outside** the FE's `EventType` union renders through the generic fallbacks — the label is the uppercased token, the message is `Event: <token>`, the colour is the gray default. (The fixture needs a cast because the union lacks the token — that cast is itself the evidence for the deferred union extension.) | AC-R10 (`decisions.md:733`): the pick/attempt/skip trio becomes **visible** the moment the BE emits it, with **no** FE rendering change (`EventLog.tsx:279-282`, `:284`, `:286`) |

### 6.3 Existing test files — deliberately **LEFT ALONE**

| File | Verdict | Reason |
|---|---|---|
| `pkg/ui/frontend/src/components/__tests__/SettingsPage.tabs.test.tsx` | **NO EDIT** | The top-level tab bar is unchanged: the same `data-tab-id="decision_models"` button stays in place (`SettingsPage.tsx:426-437`), the bar still has 11 buttons, and the vertical nav is outside `div.flex.overflow-x-auto` (so the selector at `:126-128`/`:153-155`/`:282-284` cannot match it). Therefore `tabs.length === 11` (`:285`), `groupIdx === 10` (`:287`) and the Decision→Credentials adjacency (`:290-291`) all stay true. The "clicking the 🎯 Decision Models tab mounts the DecisionModelsTab empty state" case (`:162-183`) also stays true: the container's **default** child is `models`, so the empty-state copy "No decision models configured" and the "Add Decision Model" CTA still appear after the click. The "clicking back to Models tab hides the DecisionModelsTab content" case (`:185-203`) stays true: the whole `decision_models` branch unmounts. **No assertion is re-pinned and no count changes.** The new protection against a future regression lives in the container's own suite (A4), not in this file. |
| `pkg/ui/frontend/src/components/config/__tests__/DecisionModelsTab.test.tsx` | **NO EDIT** | `DecisionModelsTab.tsx` is not modified (§1.3). |
| `pkg/ui/frontend/src/components/config/__tests__/DecisionModelsTab.decisionTest.test.tsx` | **NO EDIT** | Same. This suite also demonstrates the module-level-import consequence (below). |
| `pkg/ui/frontend/src/components/config/__tests__/DecisionModelForm.test.tsx` | **UPDATED by Amendment 2 (warning 13)** — **+ V1**, the FE half of ADR-15's T5 set-equality pin: the rendered provider surface is **exactly the set** `{openai, typesafe, openrouter}` (order-insensitive) and **disjoint from the credential vocabulary** — no `minimax` / `9router` / `azure` / `zhipu` / `zai` / `grok` / `deepseek` / `opencode` / `opencode-go` / `lightllm` / `anthropic` / `gemini` option may appear. The existing order-sensitive pin at `:271-286` is unchanged and stays green. | ADR-15 T5 (`decisions.md:922-924`): "NO vocabulary change" is **pinned, not asserted** |
| `pkg/ui/frontend/src/hooks/__tests__/useDecisionModels.test.ts`, `useGroupModels.test.ts`, `useApiCredentials.test.ts` | **NO EDIT** | No hook, endpoint or cache key changes (§3.1). The existing quadruple-key assertion helper (`useDecisionModels.test.ts:41-54`) stays green. |
| `pkg/ui/frontend/src/utils/__tests__/tsc-baseline.test.ts` | **NO EDIT** | The baseline stays 19/9 (§7). |

### 6.4 The provider-vocabulary pin (explicit non-change)

**This commission adds NO provider.** The FE constant `pkg/ui/frontend/src/components/config/DecisionModelForm.tsx:29` — `const PROVIDER_OPTIONS = ['openai', 'typesafe', 'openrouter'] as const;` — is **byte-unchanged**, and `DecisionModelForm.tsx` is not touched at all. The guard is the **existing** assertion in `pkg/ui/frontend/src/components/config/__tests__/DecisionModelForm.test.tsx:271-286` (the provider dropdown contains exactly those three providers) — it must stay green, and it needs no edit. In addition, two **new** cases assert the commission's non-change from the other side: **B8/D7** (the decision-lane **group** form renders no provider field at all) and **A11** (the container's models panel mounts the unchanged decision form). The BE-side provider vocabulary (5 sites, `decisions.md:323-327`) is phase 1's concern.

### 6.5 The module-level `testDecisionModel` import — the test-mock consequence

`DecisionModelsTab.tsx:4` imports `testDecisionModel` from `'../../hooks/useApi'` at **module level**, and the TEST button calls it directly (`:197`) rather than through a prop.

Consequences for this commission's tests:

1. **Rendering the container does NOT require a mock.** The import binds the function at import time but performs no I/O; `useApi.ts` has no import-time network side effect. A container test that never fires a probe needs no `vi.mock` and no fetch stub.
2. **Firing the probe DOES require a fetch stub.** `DecisionModelsTab.decisionTest.test.tsx:19-28` stubs `globalThis.fetch` (it does **not** use `vi.mock`) — `testDecisionModel` → `apiFetch` → `fetch` (`useApi.ts:1267-1272`, `:25-33`). Any new test that clicks a decision TEST button must stub `globalThis.fetch` the same way; `vi.mock('../../hooks/useApi')` is the alternative but is **not** needed and would deviate from the house pattern.
3. **The container's A12 case (switching away discards the probe state) must not assert on the network.** It asserts the *mount* consequence (the models panel is gone), not that the request was aborted — the request is **not** aborted (no `AbortSignal`, `useApi.ts:1267-1272`).
4. **A container test that renders the group child does not import the probe path at all** — the group panel has no TEST button.

---

## 7. tsc baseline parity

**The exact expectation (re-stated after Amendment 1, 2026-11-16 — UNCHANGED): `pkg/ui/frontend/tsc-baseline.txt` is UNCHANGED — `TOTAL_ERRORS=19`, `TOTAL_FILES=9`, and the nine per-file counts unchanged.** Amendment 1 adds **no new source file** (the event-visibility case is an edit to an existing test file; the candidate-eligibility clause and the copy changes live in the already-planned `GroupMemberEditor` / `GroupModelsTab` files), so the file set and every per-file count are untouched: `src/components/SettingsPage.tsx` stays at 6, `src/components/config/ModelsTab.tsx` stays at 6, the seven 1-error files stay at 1, and every new file must still produce **zero** errors.

The gate (`pkg/ui/frontend/src/utils/__tests__/tsc-baseline.test.ts`) asserts, in order: the baseline's own internal consistency (per-file sum == total, `:121-127`), the **actual** total error count == the recorded total (`:139-142`), the actual file count == the recorded file count (`:144-147`), and the **per-file counts** and the **file set** match (`:149-…`). It runs `npx --no-install tsc --noEmit` in `pkg/ui/frontend` with a 60 s timeout (`:67-79`).

**Verdict rule (binding):**

- **Does this design add any new type error? NO — by construction, zero.** Every new source file is new (a new file that produced even one error would change the file set and fail the gate), and every changed file either (a) is already at zero (`GroupModelsTab.tsx`, `GroupModelForm.tsx`, `GroupMemberEditor.tsx`, `types.ts` are **not** in the baseline's nine files — `tsc-baseline.txt:19-27`), or (b) is already carrying frozen errors (`SettingsPage.tsx` = 6) and receives only **additive, type-checked** edits.
- **No baseline update is proposed.** The rule is: `TOTAL_ERRORS`/`TOTAL_FILES` stay **19/9**, the **file set** stays the same nine files, and **each touched baseline file keeps its exact per-file count** — `src/components/SettingsPage.tsx` stays at **6**, `src/components/config/ModelsTab.tsx` stays at **6**, and the seven 1-error files stay at 1. A new file must produce **zero** errors; a new file with an error is a **failure**, not a baseline bump.
- **No baseline-paper-over.** If the implementation produces a new error, the correct response is to fix the code — **not** to add a line to `tsc-baseline.txt`. The only legitimate baseline update would be a *deliberate, documented* change, and this plan proposes none.
- **The one file at risk** is `src/components/SettingsPage.tsx` (already at 6 frozen errors). Its diff must be strictly additive and type-correct: the new container's props are a typed interface, the lane helpers live in a **new** file (`groupLane.ts`, zero errors), and the split is computed with explicitly typed locals. If a new error appears there, the count goes 6 → 7 and the gate fails — which is the intended behaviour.
- **`src/components/config/ModelsTab.tsx` is not touched** (its chat filter at `:363` stays byte-identical) → its count stays 6.

**Not verified:** `tsc` was **not run** (forbidden by the task), so the six pre-existing `SettingsPage.tsx` errors were not enumerated and their exact locations are unknown. The mitigation above does not depend on knowing them.

---

## 8. Stages (dependency-ordered)

Each stage is independently completable and ends green.

### Stage 1 — Lane vocabulary + inference helper

| Seam | File:line | What changes |
|---|---|---|
| FE type home | `pkg/ui/frontend/src/types.ts:182-186` (adjacent) | **Add** `export type GroupLane = 'chat' \| 'decision';` (additive; nothing existing changes) |
| New pure helper | `pkg/ui/frontend/src/components/config/groupLane.ts` **(NEW)** | `normalizeKind`, `buildKindIndex`, `inferGroupLane`, `splitGroupsByLane` — pure, no React, no I/O (rules in §3.2) |
| New helper tests | `pkg/ui/frontend/src/components/config/__tests__/groupLane.test.ts` **(NEW)** | E1–E7 |

**Proves it:** the helper suite (E1–E7).
**Exit criterion:** `groupLane.test.ts` is green and `types.ts` exports `GroupLane`; no UI file references the helper yet.
**Risk:** LOW — a new file with zero deps; the rule is pinned by tests before any UI consumes it. The only judgement call is the `unknown` bucket (§3.2) — pinned by E3/E4/E6 and reversible in one function.

### Stage 2 — Parameterize the three group components by lane

| Seam | File:line | What changes |
|---|---|---|
| Member editor | `pkg/ui/frontend/src/components/config/GroupMemberEditor.tsx:8-16`, `:35`, `:40`, `:105`, `:190` | required `lane` prop; `chatModels` → `laneModels`; lane-driven picker filter; lane-aware placeholder + hint. **AMENDED 2026-11-16 (ADR-7 / AC-R6):** the `lane='decision'` branch of the `:40` filter also excludes inactive models (`&& m.enabled`); the `lane='chat'` branch is byte-identical |
| Group form | `pkg/ui/frontend/src/components/config/GroupModelForm.tsx:36-60`, `:120-126`, `:161-186`, `:195-207`, `:343-347`, `:381` | required `lane` prop; prop rename; lane-aware G8 mirror + copy; lane-aware editor mount + shadow options; **new** homogeneity + lane-consistency mirrors (§5.2, §5.3) |
| Group tab | `pkg/ui/frontend/src/components/config/GroupModelsTab.tsx:6-30`, `:35-40`, `:151-167`, `:194-204`, `:303`, `:314` | required `lane` prop; prop rename + pass-through; lane-aware copy table (§1.4); the wrap pre-fill stays text-lane-only |
| Existing fixtures | `GroupModelsTab.test.tsx`, `GroupModelForm.test.tsx`, `GroupMemberEditor.test.tsx` | add `lane="chat"`, rename the prop — **assertions unchanged** |
| New lane suites | `GroupMemberEditor.decisionLane.test.tsx`, `GroupModelForm.decisionLane.test.tsx`, `GroupModelsTab.decisionLane.test.tsx` **(NEW)** | C1–C5, D1–D7, B1–B8 |

**Proves it:** the three existing suites (unchanged assertions = the text-lane regression pin) + the three new decision-lane suites.
**Exit criterion:** the three existing group suites are green **with their assertions unchanged** and the three new decision-lane suites are green — the text lane is provably untouched and the three group components are lane-parameterized.
**Risk:** MEDIUM — the shared components carry the text lane. Mitigations: `lane` is **required** (a missed call site is a compile error, not a silent fallback); the text-lane branch of every conditional is the existing code path; the text-lane assertions are unchanged. A lane-parameterised copy map is the most likely defect surface — covered by B1/D1/D3.
**Blocked by:** Stage 1 (the lane type and the helper).

### Stage 3 — The container + vertical nav

| Seam | File:line | What changes |
|---|---|---|
| Container (new) | `pkg/ui/frontend/src/components/config/DecisionModelsContainer.tsx` **(NEW)** | vertical left nav + child-tab state + conditional panel mount (§2.1, §2.2); props: the models quartet verbatim + the group quartet + the lane lists |
| Container tests | `pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx` **(NEW)** | A1–A13 |

**Proves it:** the container suite.
**Exit criterion:** `DecisionModelsContainer.test.tsx` is green (A1–A13), including the no-ARIA-tab-roles pin and the "the nav is not matched by the top-level-tab selector" pin.
**Risk:** MEDIUM — first-of-kind styling and a11y. Mitigations: the class pair is lifted verbatim from `SettingsPage.tsx:388-391`; the a11y contract is pinned (no ARIA tab roles, native buttons, `type="button"`, `data-testid` + `aria-label`); the nav deliberately avoids `overflow-x-auto` so the top-level tab selector cannot see it (A4). The active state's programmatic signal is the additive `aria-current="true"` on the active button (CMO adjudication E1 — active only, omitted on the inactive); the absence of ARIA tab semantics is documented in §2.5, not silently accepted.
**Blocked by:** Stage 2 (the decision-lane group surface it mounts).

### Stage 4 — SettingsPage wiring

| Seam | File:line | What changes |
|---|---|---|
| Parent projection | `pkg/ui/frontend/src/components/SettingsPage.tsx:293-295` | keep `chatKindModels` byte-identical; add the decision lane's model list and the lane split (calls into `groupLane.ts`) |
| Decision branch | `pkg/ui/frontend/src/components/SettingsPage.tsx:607-619` | mount `DecisionModelsContainer` instead of `DecisionModelsTab`; pass the models props **verbatim** and the group props (decision-lane groups, decision lane models) |
| Group branch | `pkg/ui/frontend/src/components/SettingsPage.tsx:631-645` | pass `lane="chat"`, `laneModels={chatKindModels}`, and the text-lane group list (`chat` + `unknown`) |
| Top-level bar | `pkg/ui/frontend/src/components/SettingsPage.tsx:426-437` | **unchanged** — 11 tabs, same order, same `data-tab-id` |

**Proves it:** `SettingsPage.tabs.test.tsx` stays green **with no edit** (§6.3); `SettingsPage.tsx` stays at 6 tsc errors (§7).
**Exit criterion:** the whole FE suite is green, `SettingsPage.tabs.test.tsx` passes **without an edit**, and `tsc --noEmit` still reports 19 errors across the same 9 files.
**Risk:** MEDIUM — `SettingsPage.tsx` is the one file with frozen tsc errors, and the wiring is where a prop mismatch would surface. Mitigations: the container's props are a typed interface (a mismatch is a compile error, caught by the tsc gate); the top-level tab bar is not touched, so the count/order assertions cannot drift. The lane split must be computed from the **narrow server-prefiltered lists** (§3.2), not from the unfiltered `models` array — otherwise a just-created decision model would be unresolvable.
**Blocked by:** Stages 1–3.

### Stage 5 — FE gate + embed discipline

| Seam | File:line | What changes |
|---|---|---|
| FE test run | `pkg/ui/frontend` (`vitest run`, `package.json:10-11`) | the whole FE suite green (the 12 files under `components/config/__tests__/` + the 5 under `hooks/__tests__/` + `utils/__tests__/tsc-baseline.test.ts`) — **AMENDED 2026-11-16:** plus the 5 new decision-groups files and the F1 case in `components/__tests__/EventLog.test.tsx` (AC-R10) |
| Type gate | `pkg/ui/frontend/src/utils/__tests__/tsc-baseline.test.ts:67-79` | `tsc --noEmit` ⇒ 19 errors / 9 files, the same file set, the same per-file counts (§7) |
| Asset rebuild | `pkg/ui/frontend/vite.config.ts:7-10` (`outDir:'../static'`, `emptyOutDir:true`) | `npm run build` regenerates the **tracked** `pkg/ui/static/` (4 tracked paths: `index.html`, `assets/index-*.js`, `assets/index-*.css`, `assets/SettingsPage-*.js`) — the zero-mod proof is a `sha256sum` of `pkg/ui/static` before/after a no-source-change rebuild (byte-identical) |
| Commit discipline | — | **the regenerated `pkg/ui/static/` assets ride in the SAME commit as the FE source** (house rule; `research-fe-precedent.md:237`, ruling contract 29). **This plan does NOT propose committing `pkg/ui/static/` separately from the source, and performs no commit.** |

**Proves it:** the FE suite + the tsc gate + the zero-mod proof.
**Exit criterion:** the FE gate is green end-to-end (vitest + the tsc baseline gate + the sha256sum zero-mod proof) and `pkg/ui/static/` is regenerated and staged **together with** the FE source in one commit — not performed by this plan.
**Risk:** LOW mechanically, MEDIUM operationally — `vite build` writes into a tracked directory, so a build that runs against a dirty tree silently mixes assets. Mitigation: build once, after the source is final; the zero-mod proof (sha256sum before/after a no-op rebuild) isolates the mechanism.

---

## Coupling map

|  | Stage 1 (lane helper) | Stage 2 (lane-parameterized components) | Stage 3 (container) | Stage 4 (SettingsPage) | Stage 5 (gate) |
|---|---|---|---|---|---|
| **Stage 1** | — | **tight** — supplies the `GroupLane` type the components' props require | **loose** — the container consumes the lane-typed props only | **tight** — the split is computed with the helper | independent |
| **Stage 2** | tight | — | **tight** — the container mounts `GroupModelsTab lane="decision"`; the decision-lane copy/behaviour is the container's payload | **tight** — the parent supplies `laneModels` + the lane-scoped group list | loose — the gate consumes the suites |
| **Stage 3** | loose | tight | — | **tight** — the container is mounted in the `decision_models` branch | loose |
| **Stage 4** | tight | tight | tight | — | **tight** — the gate asserts the tsc count of the touched file |
| **Stage 5** | independent | loose | loose | tight | — |

**Cross-phase coupling to phase 1 (backend) — the one real external dependency:**

| Dependency | Status | If phase 1 diverges |
|---|---|---|
| The FE classifies groups **client-side** because the BE exposes no lane filter and no lane field (`pkg/ui/server.go:761-820`; `grep GroupLane pkg/` → 0) | **Open** — the ruling's `GroupLane` seam (blast radius #19, `decisions.md:319`) is a phase-1 deliverable that does not exist in this tree | If phase 1 adds a **server-computed lane** (a `?kind=group&lane=decision` filter or a lane field on the group DTO), the FE's split (§3.2) should be replaced by the server filter, and the helper's `inferGroupLane` becomes a display-only fallback. The **UI, the copy, the tests and the nav are unaffected** — only `splitGroupsByLane`'s input changes. The FE plan is therefore **not blocked** on phase 1, but it is **coupled** to it. |
| The FE's group **write** payload (`kind:'group'` + decision-kind members) must be accepted by the widened BE validators (ruling blast radius #1, #3, #5, #6) | **Open** — phase 1 | If phase 1 rejects the payload, the FE surfaces the server's 400 message verbatim (house `apiFetch` behaviour, `useApi.ts:36-38`) — no FE change is needed, only the error copy. |
| The provider vocabulary is unchanged on both sides | **Settled** | — |
| **ADR-1 kind immutability** (`decisions.md:426-460`) — the BE rejects retyping a group member on the **model-update** path | **Open** — phase 1 | No FE change: the FE's model-edit surface is untouched by this commission and the BE's 4xx message surfaces verbatim (`useApi.ts:36-38`). The FE's §5.2 lane-consistency guard becomes defence-in-depth. |
| **ADR-7's server-side candidate list** (`decisions.md:665-666`) — the BE must also exclude inactive models from any candidate list | **Open** — phase 1 | The FE's exclusion (§3.3) mirrors it; if the BE later serves a pre-filtered candidate list, the FE filter stays (belt-and-suspenders) and the derivation may then be narrowed. |
| **AC-R10 event tokens** (`decisions.md:733`) — the BE's pick/attempt/skip token names are not fixed | **Open** — phase 1 | The FE needs **no** change to render them (`EventLog.tsx:279-282`, `:284`, `:286` all fall back). The **deferred** follow-up — extending the FE's `EventType` union + adding curated `EVENT_TYPE_LABELS` / `EVENT_MESSAGES` / `EVENT_COLORS` entries — lands **with** the BE commit, once the tokens are named. **Do not invent token names now.** |
| **ADR-12 per-layer caches** (`decisions.md:780-809`; AC-R11) | **Backend-only for v1** | No FE change. The one FE-visible consequence is the **copy** requirement already folded in: the decision lane has no chain concept (OQ-2: "deliberately not built") and no hedge/race engine (OQ-3 option (a)), so the decision-lane copy must not imply layer-equivalent resilience. |

**Internal coupling that could break if a contract changes mid-flight:** the `lane` prop is **required** on three components and **four** call sites (the two `SettingsPage` branches + three test fixtures). If phase 1 or a review changes the lane vocabulary (e.g. adds `image-gen` as a third lane), the `lane` prop's type, the copy map, the picker filter and the split helper all move together — that is why the lane type lives in `types.ts` rather than being inlined in three components.

---

## Out of scope

1. **No provider additions.** The decision provider vocabulary (`['openai','typesafe','openrouter']`) is unchanged in the FE (`DecisionModelForm.tsx:29`) and unchanged in the BE (5 sites, `decisions.md:323-327`).
2. **No change to the chat lane's behaviour.** The text `Group Models` tab keeps its heading, CTA, toast copy, empty state, member picker, weight semantics, `backup_only` semantics, cap of 16, dup-warn posture, wrap pre-fill, shadow/claim semantics and its G-mirrors. The only text-lane deltas are the mechanical prop rename, the required `lane="chat"`, and the client-side lane split (which is a no-op at day-1 because no decision group exists).
3. **No change to the img-gen tabs** (`ImgGenModelsTab` / `ImgGenModelForm` are not touched).
4. **No change to the decision-models list surface** — `DecisionModelsTab.tsx` and `DecisionModelForm.tsx` are not touched.
5. **No new ARIA contract.** No `role="tablist"`, `role="tab"`, `role="tabpanel"`, `aria-selected`, roving tabindex or arrow-key navigation. The ruling's Q5 (`decisions.md:412`) keeps the FE class-only; the container introduces **no** ARIA role at all. `aria-label` + `data-testid` on the nav buttons are the ruling's contract 25 (`decisions.md:381`), not a tab contract. The only addition is the additive `aria-current="true"` on the **active** nav button (CMO adjudication E1) — no role, no roving tabindex, **no keyboard-behaviour change**.
6. **No backend change** — no endpoint, no query parameter, no DTO field, no migration. The FE consumes existing endpoints only.
7. **No change to the hooks layer** — no new hook, no new cache key, no change to the quadruple invalidation.
8. **No change to the top-level tab bar** — 11 tabs, same order, same `data-tab-id`s, same classes.
9. **No commit, no push, no deploy, no build** performed by this plan. The asset-regeneration discipline is *recorded* (Stage 5), not executed.
10. **No `pkg/ui/static/` commit separate from the source** — house rule: source + regenerated assets ride together.
11. **No ≥2-members rule** for decision groups (or text groups) — a single-member group is valid (ADR-8 / AC-R8, ADR-11 OQ-3 option (a)). The FE's existing G2 mirror is `≥1` and stays `≥1`.
12. **No auto-enable, and no member `enabled` field** anywhere in the FE (ADR-7 / AC-R7). The member wire shape has no such key (`types.ts:182-186`); the BE's save/validation path owns the server-side prohibition (phase 1).
13. **No servability annotation on member chips** — the FE does not label a member row as skipped/unservable (not ruled; the BE is authoritative, and the picker exclusion prevents new inactive members).
14. **No FE rendering of the pick/attempt/skip event trio** — the tokens do not exist yet (AC-R10); the FE renders them generically the moment they do (`EventLog.tsx:279-282`, `:284`, `:286`).

---

## Risks

| # | Risk | Impact | Likelihood | Mitigation |
|---|---|---|---|---|
| 1 | The lane split is a **client-side, cached** inference (§3.2). A retyped member can mis-file a group — **AMENDED 2026-11-16 (warning 14): the staleness bound is the whole live session, not the 15 s cache TTL** (the hooks mount once in `App.tsx:37`/`:43` and fetch once, `useApi.ts:605-610`/`:705-710`; no polling) — **DISPOSITION (leader, 2026-11-16): ACCEPTED-BY-LEADER this release; recorded as wishlist entry 404ad40b (project future list)** | Medium (cosmetic misfile) | Medium | The write path is server-validated; the split is display-only; the **lane-consistency guard** (§5.2, repaired in Amendment 2) blocks the only dangerous consequence (a lane-flipping edit) — and no longer dead-ends an unknown-lane group |
| 2 | Without the §5.2 guard, editing a mis-filed decision group in the text tab would **silently flip its lane** (a legal write the server cannot detect) | **High** | Low | The guard is **mandatory** in Stage 2 and pinned by D6 |
| 3 | The `SettingsPage.tsx` diff lands in a file with **6 frozen tsc errors**; one new error breaks the baseline gate | Medium (gate failure) | Medium | The diff is additive and type-checked; the new code lives in new zero-error files; the baseline is **not** to be bumped (§7) |
| 4 | Sharing three components across two lanes leaks a copy/branch defect into the **text** lane | High | Medium | `lane` is required (compile-time); the text lane's assertions are unchanged and must stay green; the lane copy map is table-driven (§1.4) so a branch is one line |
| 5 | The vertical nav is the FE's **first** nested-tab/vertical nav — the styling may read as foreign next to the top bar | Medium (design) | Medium | The active/inactive class pair is lifted verbatim; the divider idiom is the app's; two new utilities only; the first-of-kind status is stated in §2.6 rather than glossed |
| 6 | The active state would have been **colour-only**; a screen-reader user would get no programmatic "selected" signal (§2.5) | Medium (a11y) | Certain | **Closed by the CMO adjudication E1**: `aria-current="true"` on the **active** nav button (active only, omitted on the inactive — never `"false"`), additive — no role, no roving tabindex, no keyboard-behaviour change |
| 7 | Switching child tabs **discards an in-flight decision probe's result** (the request itself continues and is billable) (§2.4) | Low–Medium | Medium | Documented and pinned (A12); identical to the behaviour one level up today; the operator re-runs the probe |
| 8 | `vite build` writes into the **tracked** `pkg/ui/static/` — a build on a dirty tree mixes assets | Medium | Low | Build once after the source is final; the zero-mod sha256sum proof isolates the mechanism; source + assets ride in one commit (Stage 5) |
| 9 | The FE classifies the lane while the BE classifies it at request time — the two could disagree | Medium | Low | They implement the **same rule** over the **same rows**; the BE is authoritative for traffic; the FE's misclassification is display-only and write-neutralised by the §5.2 guard |

---

## Escalations (for the approver — not decided by this plan)

1. **`aria-current` on the active nav button?** The ruling's Q5 recommends class-only. The chosen idiom therefore conveys the active state **visually only** (§2.5). `aria-current="true"` on the active nav item is a **one-line** change that adds **no** tab role and no new ARIA contract, and it closes the "SR users get no selected signal" gap. **Recommendation: add it** — but it is a deviation from Q5's literal "class-only", so it is the approver's call.
2. **Nav label case.** The plan renders `Models` / `Group` (Title Case, house style); the user's spec text is `[ models ] [ group ]`. One-line revert if the literal lowercase is preferred (§2.3).
3. **The lane filter stays client-side until phase 1 provides a server-side lane.** If phase 1 adds a `?lane=` filter or a lane field, the FE's split should be replaced by it (§Coupling map). The UI is unaffected either way.
4. **Q6's empty-state copy** ("no per-conversation pinning") is included in the decision-lane empty state, per the ruling's recommendation (`decisions.md:413`).

---

## Not verified

- **Nothing was executed.** No `vite build`, no `tsc`, no `vitest`, no smoke, no commit, no push, no deploy (all forbidden by the task). Every breakage statement is static reasoning from direct reads at `2b987bf`.
- **The six pre-existing `tsc` errors in `src/components/SettingsPage.tsx` were not enumerated** (`tsc` not run). Their locations are unknown; the §7 mitigation does not depend on them.
- **The happy-dom Enter→click synthesis behaviour was not executed.** The test design therefore pins the *preconditions* for native keyboard activation rather than asserting a synthesised click (§2.5, A8). This is a deliberate, stated limitation of the test, not a claim that Enter was exercised.
- **The full bodies of `DecisionModelsTab.decisionTest.test.tsx` and `DecisionModelForm.test.tsx` were not read end-to-end** (the cited lines — `:271-286` for the provider dropdown, `:19-28` for the fetch stub — were verified by grep/read). The decision-model form's validation block (`DecisionModelForm.tsx:215-249`) was not read line-by-line.
- **The phase-1 (backend) plan does not exist yet** (`ls .agents/shared/planning/decision-groups/` → `decisions.md`, `research-decision.md`, `research-fe-precedent.md`, `research-groups.md` only). The FE plan's client-side lane split assumes no BE lane seam; the coupling table above states exactly what changes if phase 1 adds one.
- **`pkg/store/database/store_group_rewrite.go:291-301` and `pkg/models/group.go:60-214`** were consulted through the ruling's citations and targeted greps, not read end-to-end by this worker; the FE mirrors are written against the rule descriptions in `decisions.md`, which are themselves line-cited.
- **The user's live decision models** (ids, kinds, credentials, groups) live in the running DB and were not read; the day-1 "no decision group exists" premise in §3.2 is an inference from the ruling (G3 today admits only chat members), not an observation of the DB.
- **`pkg/ui/frontend/src/components/config/probeErrorClasses.ts`** was not read in full (only the two delegation call sites at `DecisionModelsTab.tsx:59-67`).

---

## CMO adjudications

Recorded 2026-11-16 by the CMO/planner ruling on the two escalations that were open when this plan was drafted. This section is the authoritative record and **supersedes** `## Escalations` items 1 and 2 above (that section is left verbatim, per the no-inline-edit instruction). No restructuring, no renumbering, no content reflow.

### E1 — `aria-current`: APPROVED as constrained

`aria-current="true"` is not a tab-role contract — it adds zero roles, zero roving tabindex, zero keyboard-behaviour change, and leaves the existing native `<button type="button">` idiom intact; it only makes the active item's state perceivable to assistive tech, which is strictly additive to Q5's class-only ruling rather than a deviation from it.

Constraints applied in this plan:

- **(a)** `aria-current="true"` is rendered on the **ACTIVE** button only; the inactive button **omits** the attribute entirely (never `aria-current="false"`). Spec: §2.1 markup + §2.5.
- **(b)** The top-level tab-bar selector surface is unchanged — the nav stays **outside** `div.flex.overflow-x-auto`, so `SettingsPage.tabs.test.tsx` (`:285` `tabs.length === 11`, `:287` `groupIdx === 10`) is untouched. Spec: §2.1 / §2.2; pinned by case A4.
- **(c)** Pinned in the container test (A-series): **A7** — the active button carries `aria-current="true"` and the inactive has **no** `aria-current` attribute; **A6** — the attribute **follows the click** (it moves to the newly activated button and is removed from the previous one).

**Behaviour is unchanged by E1:** `Tab` reaches both nav buttons, `Enter`/`Space` activate them, the arrow keys are inert, and the mouse path is identical. **No ARIA tab semantics are claimed** — the container still has no `role="tablist"` / `role="tab"` / `role="tabpanel"` / `aria-selected` (case A10 still pins that), and no roving tabindex.

### E2 — nav label case: RULED Title Case (`Models` / `Group`)

Rationale (verbatim): the commission's own FE requirement asks for styling "consistent with the app's existing design language", and every existing label in this surface is Title Case (`Decision Models`, `Add Decision Model`, group tab labels) — lowercase nav labels would be the only ones in the app. The spec's `[ models ] [ group ]` is read as schematic notation for *two child tabs, vertically stacked*, not literal copy.

**One-line revertible copy decision** (spec: §2.3): the two nav-button text literals in `pkg/ui/frontend/src/components/config/DecisionModelsContainer.tsx` — `Models` → `models`, `Group` → `group` — plus the two matching assertion strings in `pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx` (case A1). No structural change; the CMO can veto without touching structure.

### Escalation 3 — client-side lane filtering: ACKNOWLEDGED as correct (pending phase 1)

The FE classifies groups client-side because the BE exposes no lane filter and no lane field (`pkg/ui/server.go:761-820`; `grep -rn "GroupLane" pkg/` → 0 matches at `2b987bf`). That is **correct for v1**. Phase 1's `GroupLane(groupID)` makes **server-side lane filtering possible later**, but **the FE must not depend on it for v1**: the client-side split (`groupLane.ts` → `splitGroupsByLane`) remains the v1 mechanism, and the FE plan proceeds without waiting on the BE seam. If phase 1 later adds a lane filter or field, only the split's input changes — the UI, the copy, the tests and the nav are unaffected.

### Escalation 4 — Q6 no-pinning copy: ACCEPTED as-is

The decision-lane empty state states that members are picked by weight per request with no per-conversation pinning (ruling Q6, recommended YES). No change.

---

## Amendment 1 (2026-11-16) — user ruling consequences

**Source of truth:** the user ruling of 2026-11-16, recorded in `decisions.md` as **ADR-7** (chain-only models, `decisions.md:656-673`), **ADR-8** (single-member group / R5, `:675-693`), **ADR-9** (text lane untouched by this release, `:695-720`), **ADR-10** (the consolidated criteria AC-R1..AC-R10, `:722-744`), **ADR-11** (the OQ-1..OQ-4 closures, `:748-776`) and **ADR-12** (per-layer caches, `:778-807`), plus **ADR-1** (kind immutability, `:426-460`), **ADR-5** (pick is policy; a mark suppresses an attempt, `:584-620`) and **ADR-6** (a member fails only after its own resilience is consumed, `:622-654`).

**Shape of the amendment.** The plan above is **unchanged in architecture**: the container, the vertical left nav, the lane parameterization, the client-side lane split, the test-file layout and the tsc verdict all stand. This amendment (a) folds the ruling's FE-visible consequences into the affected sections — each with an `AMENDED 2026-11-16` supersession marker — and (b) records them here, once, with the ADR citation for every change.

| # | Ruling | FE consequence | Site |
|---|---|---|---|
| A-6 | **ADR-7 / AC-R6** (ADR-11 OQ-4) | the decision-lane member picker must exclude **inactive** models; the text picker does not | `GroupMemberEditor.tsx:40` (§3.3, §4, §5.1) |
| A-7 | **ADR-7 / AC-R7** | the FE must never flip a member's `enabled` — **structurally already impossible**; pinned by a test | `types.ts:182-186`, `GroupModelForm.tsx:24-34` (§5.1, §6.1) |
| A-8 | **ADR-8 / AC-R8** + **ADR-11 OQ-3** | no ≥2-members rule; the copy must not promise resilience that does not exist | `GroupModelForm.tsx:161-162` (§5.1), copy table (§1.4) |
| A-10 | **ADR-10 / AC-R10** (ADR-5 AC-R2) | the pick/attempt/skip event trio needs **no FE rendering change** | `EventLog.tsx:279-282`, `:284`, `:286` (§6.2) |
| A-12 | **ADR-12 / AC-R11** | backend-only for v1; the FE-visible residue is the copy rule above | §1.4, coupling map |

### A-6 / AC-R6 (ADR-7, ADR-11 OQ-4) — the member-picker gap, closed

**Ruling (ADR-7, `decisions.md:656-673`):** models may be deliberately created **INACTIVE**, exposed to no group, reachable only as a `fallback_chain` target. Validation **MUST allow** inactive/chain-only targets — **"Disabled-but-chain-referenced" is NOT a data defect to clean up.** Such models must **NOT** be surfaced as **group candidates**: "The FE member picker and any server-side candidate list must exclude them" (`decisions.md:665-666`). Nothing may **auto-enable** a model as a side effect of adding it to a group.

**ADR-11 OQ-4 (`decisions.md:771-775`):** disabled group members are **NOT attempted** — the request-time servability predicate stays exactly as today. R4 applies to **CHAIN TARGETS and VALIDATION only**. And the rule that this plan must state in its own words: **"Chain targets and group members are two populations — do not conflate them in validation or in tests."**

**The gap in the plan as written:** the decision-lane picker filter was kind-only (`m.kind === 'decision'`), so an **inactive** decision model would have been offered as a group candidate — a direct AC-R6 violation.

**The exact site that changes — ONE:** `pkg/ui/frontend/src/components/config/GroupMemberEditor.tsx:40`, the editor's defensive picker filter, now lane-gated:

```
lane === 'chat'     → pickerModels = laneModels.filter(m => !m.kind || m.kind === 'chat')          // UNCHANGED (byte-for-byte)
lane === 'decision' → pickerModels = laneModels.filter(m => m.kind === 'decision' && m.enabled)    // NEW (ADR-7 / AC-R6)
```

**The `laneModels` derivation is deliberately NOT changed.** The decision lane keeps the full server-prefiltered `?kind=decision` list, because the same list also feeds the **shadow-target picker** (`GroupModelForm.tsx:381`) — a third population (an ID claim, neither a chain target nor a membership). Narrowing at the derivation site would silently narrow the shadow population too, which ADR-7 does **not** rule. The member picker is the group-candidate population, and it is the only place that changes.

**The two-population rule, as the FE implements it:**

1. An **inactive / chain-only** model is a **valid chain target** and **not a defect** — the FE never warns about it, never labels it broken, never offers a cleanup affordance. The decision-models list shows only its enable toggle (`DecisionModelsTab.tsx:281-291`) and says nothing else; unchanged.
2. It is **not a group candidate** — the decision-lane member picker never lists it (AC-R6).
3. It is **not** a shadow-claim concept — the shadow list keeps the lane-kind filter only (see the open question below).

**The text lane is untouched — deliberately.** The `lane='chat'` predicate stays character-for-character today's, **including no `enabled` filter**: AC-R12 (ADR-9) (`decisions.md:695-720`) — "This release MUST NOT change text-lane behaviour. Any shared-code semantic change must be **lane-gated** and provably text-neutral." The parallel text-lane commission is bringing the text lane to these semantics; that is not this release's job. Pinned by **T1**.

### A-7 / AC-R7 (ADR-7) — no silent auto-enable: confirmed, no FE change, pinned by a test

The FE **structurally cannot** flip a member's enable state:

- the member wire shape carries no such key — `GroupMember = {model_id, weight, backup_only?}` (`pkg/ui/frontend/src/types.ts:182-186`);
- the group payload carries `members: GroupMember[]` (`GroupModelForm.tsx:24-34`);
- the PUT body's `enabled` key (`GroupModelsTab.tsx:115-124`) is the **group's own** enable flag, not a member's.

Therefore **no FE code change is required**. The server-side prohibition ("never silently auto-enabled by save/validation", `decisions.md:673`) is **phase 1's** fence (`pkg/models/group.go` validation + the `pkg/ui/server.go` POST/PUT mirrors); a rejection surfaces to the operator verbatim through `apiFetch` (`useApi.ts:36-38`). The picker exclusion (A-6) removes the other half of the hazard: an inactive model cannot be added in the first place. **D8** pins the payload shape.

### A-8 / AC-R8 + ADR-11 OQ-3 — single-member group: valid; the copy must not over-promise

**No ≥2-members rule exists and none is added.** The FE's G2 mirror is `members.length < 1` → "At least one member is required" (`GroupModelForm.tsx:161-162`) — i.e. **≥1**, not ≥2. A **single-member** decision group is fully valid.

**What its resilience actually is (ADR-11 OQ-3, option (a) — `decisions.md:765-769`):** the member loop treats a single-member list as fully valid and fully consumable; resilience is **per-credential attempts within the member** (ADR-6 / AC-R4); "racing consumed" means **the loop contract reached its end**, not a live race. There is **no** hedge/race engine on this lane. And per **ADR-11 OQ-2** (`decisions.md:757-761`) the chain clause is **vacuous** for decision members — `fallback_chain` stays forbidden for decision-kind models, recorded as **"no equivalent concept on this lane; deliberately not built"**.

**FE-visible consequence (copy):** the decision lane's copy must not imply resilience that does not exist. Two places carry that promise today and are rewritten for `lane='decision'` only (§1.4):

- the **empty state** — now states: members picked by weight per request; no per-conversation pinning (D-6 / Q6); resilience = per-credential attempts within a member; a fallback chain has "no equivalent concept on this lane; deliberately not built";
- the **member hint** (`GroupMemberEditor.tsx:190`) — the text-lane string *"Members run their own full pipeline (credentials, fallback, race)"* is **false on this lane** (no chain, no race), so the decision-lane variant says: each member runs its own credentials; resilience is per-credential attempts within the member; no fallback chain on this lane; no racing.

One further copy fix, same rule: the **group-row badge** on the decision lane is **🎯** (the app's established decision glyph, `DecisionModelsTab.tsx:302-310`), **not 🔀** (shuffle = load balancing) — the shuffle glyph would advertise a racing/hedging engine this lane does not have. Pinned by **B9**.

### A-10 / AC-R10 (ADR-10, ADR-5 AC-R2) — pick vs attempt vs skip: **no FE rendering change needed**

The backend's event vocabulary gains **pick vs attempt vs skip** as distinct outcomes (phase 1). The FE renders events **generically**, through three per-type lookups that **all** carry a fallback:

| FE site | Code | Fallback for an unknown token |
|---|---|---|
| `pkg/ui/frontend/src/components/EventLog.tsx:279-282` | `EVENT_MESSAGES[event.type]` | `` `Event: ${event.type}` `` |
| `pkg/ui/frontend/src/components/EventLog.tsx:284` | `EVENT_COLORS[type]` | `'text-gray-400'` |
| `pkg/ui/frontend/src/components/EventLog.tsx:286` | `EVENT_TYPE_LABELS[type]` | `String(type).toUpperCase()` |

Corroborating evidence that the FE is already generic here: the FE's `EventType` union (`pkg/ui/frontend/src/types.ts`) contains **no** decision token at all, while the BE publishes `eventTypeDecision = "decision"` (`pkg/decision/handler.go:92`) — that event already renders today through the fallbacks.

**Verdict: no FE change is needed** for the pick/attempt/skip trio to be visible — it becomes visible the moment the BE emits it, with no FE edit. **Do not invent UI (or token names) for telemetry that does not exist yet.**

**What is deferred, explicitly, to the phase-1 handoff:** once the BE fixes the token names, extend the FE's `EventType` union (`types.ts`) and add curated `EVENT_TYPE_LABELS` / `EVENT_MESSAGES` / `EVENT_COLORS` entries for the trio, **in the same commit as the BE change**. Until then the generic fallbacks carry it. The FE-side regression pin that proves the fallback path is **F1**.

### A-12 / ADR-12 (AC-R11) — per-layer caches: backend-only for v1

ADR-12 builds the decision lane with per-layer caches (GROUP = pick-only policy facts; MODEL = model-attributable failures, its **chain reader ABSENT** per OQ-2; UPSTREAM = credential failures). **No FE change.** The one FE-visible residue is the copy rule already folded into A-8: the decision lane has no chain concept and no hedge/race engine, so the FE copy must not imply layer-equivalent resilience.

### tsc verdict after Amendment 1 — **UNCHANGED**

`pkg/ui/frontend/tsc-baseline.txt` stays exactly as it is: **`TOTAL_ERRORS=19`, `TOTAL_FILES=9`**, the same nine-file set, and the same per-file counts (`src/components/SettingsPage.tsx` 6, `src/components/config/ModelsTab.tsx` 6, seven files at 1). Amendment 1 adds **no new source file** — the candidate-eligibility clause and the copy changes live in the already-planned `GroupMemberEditor` / `GroupModelsTab` files, and the event-visibility pin is an edit to an existing test file. Every new file must still produce **zero** errors, and a new error is a **failure**, never a baseline bump (§7).

> **SUPERSEDED 2026-11-16 by Amendment 2 (warnings 13 + 7):** B8 and D7 are removed as vacuous, **V1** is added, and the case total is **47** (44 new-file + T1/F1/V1). The authoritative FINAL list is in **`## Amendment 2` §5**; the tables below are the Amendment 1 state, left verbatim.

### FINAL FE test list (phase 3 consumes this) — superseded, see `## Amendment 2` §5

**NEW — 5 files**

| File | Cases |
|---|---|
| `pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx` | **A1–A13** — two nav items labelled `Models`/`Group`; nav is first in DOM order; vertical (`flex-col`, no `overflow-x-auto`); nav not matched by `div.flex.overflow-x-auto > button`; `models` is the default child; clicking `Group` swaps the panel **and moves `aria-current="true"`**; active/inactive class pair + active-only `aria-current`; native `<button type="button">`, not disabled, no `tabindex="-1"`; `data-testid` + `aria-label` on each; **no** ARIA tab roles / `aria-selected`; the models panel mounts the unchanged decision list; switching to `group` unmounts the models panel (probe state discarded, request not aborted); the models props pass through verbatim |
| `pkg/ui/frontend/src/components/config/__tests__/GroupModelsTab.decisionLane.test.tsx` | **B1–B9** — decision-lane chrome (B1); **full** empty-state semantics: weight per request, no pinning, per-credential resilience, "no equivalent concept on this lane; deliberately not built" (B2); parent-scoped list, no client re-filter (B3); add-payload stamping (B4); edit PUT body, no sparse PUT (B5); delete modal + toast copy (B6); no one-click-wrap pre-fill (B7); no provider field (B8); **🎯 badge, not 🔀** (B9) |
| `pkg/ui/frontend/src/components/config/__tests__/GroupMemberEditor.decisionLane.test.tsx` | **C1–C7** — picker offers only decision-kind models (C1); lane-aware placeholder (C2); weight / `backup_only` (key dropped when false) / remove parity (C3); Add disabled at 16 (C4); dup warn is warn-only (C5); **picker offers only ENABLED decision models — an inactive model is absent (C6)**; **the editor is silent about the excluded inactive model — no defect copy (C7)** |
| `pkg/ui/frontend/src/components/config/__tests__/GroupModelForm.decisionLane.test.tsx` | **D1–D10** — submit mirrors fire: ≥1 member, ≤16, every row picked, weight>0, ≥1 non-backup member (D1); a staged non-decision member is rejected — homogeneity mirror (D2); the shadow target must be decision-kind (D3); the dual-claim scan rejects a second decision group claiming the same id (D4); payload stamping `kind:'group'`/`internal:true`/empty `fallback_chain`/empty `credentials` (D5); edit-mode lane-consistency guard blocks a mis-laned group (D6); no provider select (D7); **members carry only `model_id`/`weight`/(`backup_only`) — NO `enabled` key (D8)**; **a single-member decision group submits successfully — no ≥2 rule (D9)**; **the member hint promises only per-credential resilience — no chain/racing (D10)** |
| `pkg/ui/frontend/src/components/config/__tests__/groupLane.test.ts` | **E1–E7** — chat lane inferred from chat/kindless members (E1); decision lane inferred from all-decision members (E2); mixed ⇒ undecidable (E3); unresolvable member ⇒ undecidable (E4); zero members ⇒ chat (legacy) (E5); the split puts chat+unknown in the text list and decision-only in the decision list — no row invisible in both (E6); the kind index prefers the narrow server-prefiltered lists (E7) |

**UPDATED — 4 files**

| File | Change | Existing assertions |
|---|---|---|
| `pkg/ui/frontend/src/components/config/__tests__/GroupModelsTab.test.tsx` | fixtures add `lane="chat"`; `chatModels` → `laneModels` | unchanged |
| `pkg/ui/frontend/src/components/config/__tests__/GroupModelForm.test.tsx` | fixtures add `lane="chat"`; `chatModels` → `laneModels` | unchanged |
| `pkg/ui/frontend/src/components/config/__tests__/GroupMemberEditor.test.tsx` | fixtures add `lane="chat"`; `chatModels` → `laneModels`; **+ T1** — the text picker still offers an **inactive** chat model (the decision-lane exclusion must not leak) | unchanged |
| `pkg/ui/frontend/src/components/__tests__/EventLog.test.tsx` | **+ F1** — an event type outside the FE's `EventType` union renders through the generic fallbacks: uppercased-token label, `Event: <token>` message, gray default colour (AC-R10 visibility without inventing UI) | unchanged |

**LEFT ALONE — 8 files** *(superseded by Amendment 2: now 7 files — `DecisionModelForm.test.tsx` moves to UPDATED with V1)* (`components/__tests__/SettingsPage.tabs.test.tsx`, `components/config/__tests__/DecisionModelsTab.test.tsx`, `components/config/__tests__/DecisionModelsTab.decisionTest.test.tsx`, `components/config/__tests__/DecisionModelForm.test.tsx`, `hooks/__tests__/useDecisionModels.test.ts`, `hooks/__tests__/useGroupModels.test.ts`, `hooks/__tests__/useApiCredentials.test.ts`, `utils/__tests__/tsc-baseline.test.ts`) — reasons in §6.3.

> **AMENDED 2026-11-16 by Amendment 2 (review warning 13 + warning 7):** B8 and D7 are **removed as vacuous** (they asserted the absence of a provider field the group form never had — they would pass even if the commission did nothing). Their intent is subsumed by **D5** (exact payload shape) and made real by **V1** (the FE half of ADR-15's T5 vocabulary set-equality pin). **Case totals: 5 new files (A1–A13 = 13, B1–B7 + B9 = 8, C1–C7 = 7, D1–D6 + D8–D10 = 9, E1–E7 = 7 → 44 cases) + 5 updated files (T1, F1, V1) = 47 new FE cases.**

### Open / not ruled by this amendment (for the leader)

1. **Should the shadow-target picker also exclude inactive models?** ADR-7 rules on **group candidates**; a shadow claim is a third population (an ID claim). The plan keeps the shadow list lane-kind-filtered only. If the leader wants it narrowed, it is a **one-line** change in `GroupModelForm.tsx:381` plus one test case — **not implemented here**.
2. **Should the FE annotate member chips with servability state?** Not ruled. The plan does **not** (out-of-scope #13): the picker exclusion prevents new inactive members, and the BE's request-time predicate is authoritative.
3. **AC-R10 token names** — not fixed by the ruling; the FE's union extension and curated labels are deferred to the phase-1 handoff (see the coupling map).

### Not verified (Amendment 1)

- **Nothing was executed** — no `vite build`, no `tsc`, no `vitest`, no smoke, no commit (all out of scope for this task). Every statement above is static reasoning from direct reads at `2b987bf` plus the ruling text in `decisions.md`.
- The BE-side counterparts of AC-R6/AC-R7 (the server-side candidate list and the no-auto-enable validation) were **not** read in `pkg/models/group.go` / `pkg/ui/server.go` — phase 1 owns them; this plan only mirrors them in the FE.
- `decisions.md` ADR-2 (`:462-544`) is marked as a **retracted framing** and ADR-1/5/6 were read only in the regions cited above; no other ADR was re-read for this amendment.
- The FE's `EventType` union was read in full at `pkg/ui/frontend/src/types.ts` (no decision token present) — but the BE's **complete** event-token registry was not enumerated; only `eventTypeDecision` (`pkg/decision/handler.go:92`) was confirmed.

---

## Amendment 2 (2026-11-16) — base re-anchor + review warnings

**Trigger:** Wave B cleared — the base was re-anchored to `2b987bf` (v194 `fix/cache-layer-separation`, 36 files +3507/−630, 15 commits; worktree HEAD verified `2b987bf94e39abfc9df515cbbadc2bb335dbb47c`). Governing rulings: **ADR-15** (`decisions.md:885-947` — review-cycle-1 leader decisions, the `member_suppressed` event rename, the T5 vocabulary set-equality pin) and **ADR-16** (`decisions.md:951-1001` — the re-anchor and its constraints). This amendment supersedes the base assertions and the case counts in `## Amendment 1` where they differ; everything else in Amendment 1 stands.

### 1. Base re-anchor: `24c51ef` → `2b987bf` (ADR-16)

Every base assertion in this plan now reads `2b987bf` (5 sites re-anchored; the one remaining `24c51ef` mention is the historical "re-anchored from" reference in this document's header). Per giter's delta map, the FE delta is exactly **two source files** — verified independently by `git diff --name-only 24c51ef..2b987bf -- pkg/ui/frontend` (2 files; and **zero** files under `components/config/`, `SettingsPage.tsx`, `hooks/`, `utils/`):

| File | v194 delta | Effect on this plan |
|---|---|---|
| `pkg/ui/frontend/src/components/EventLog.tsx` | +18 | the three per-type maps each gain 4 curated entries for the new text-lane MODEL-layer tokens; the fallback helpers move **`:261-263 → :279-282`** (fallback at `:281`), **`:266 → :284`**, **`:268 → :286`** — all 8 citations in this plan re-pinned |
| `pkg/ui/frontend/src/types.ts` | +5 | the `EventType` union gains 4 members at **`types.ts:326-330`** (`model_failed`, `model_fallback_attempted`, `model_fallback_advanced`, `model_fallback_exhausted`; `internal_error` now at `:331`) |
| `pkg/ui/static/` | rebuilt | `SettingsPage-BZbFjqam.js`, `index-CO0WMZaa.css`, `index-D10Xutfq.js`, `index.html` are the new tracked baseline (see §7 below) |

All other FE pins in this plan (`GroupModelsTab.tsx`, `GroupModelForm.tsx`, `GroupMemberEditor.tsx`, `SettingsPage.tsx`, `DecisionModelsTab.tsx`, `DecisionModelForm.tsx:29`, `useApi.ts`, `App.tsx:37/:43`) are **unchanged at the new base** — verified by the empty diff over those paths, not taken from the delta map on trust.

### 2. F1 re-derivation verdict — **F1 SURVIVES; its premise is narrowed and sharpened**

- **The premise, restated:** at `24c51ef` the FE's `EventType` union named **no** lane-outcome tokens at all. At `2b987bf` the union names **four** — but they are the **text-lane MODEL-layer chain-fallback** tokens (`model_failed`, `model_fallback_attempted/advanced/exhausted`, `types.ts:326-330`), landed by wi-4d2c8e17. **The decision-lane pick/attempt/suppress trio is still unnamed in the FE** — so F1's operative premise ("the tokens this plan cares about are not named") **still holds**, narrowed from "no outcome tokens exist" to "no *decision-lane* outcome tokens exist".
- **The mechanism is unchanged:** the three per-type lookups still fall back for unknown tokens (`EventLog.tsx:279-282`, `:284`, `:286`) — verified at the new base. An unnamed decision-lane token renders as `[TOKEN]` + `Event: <token>` in gray. F1 pins exactly that.
- **v194 strengthens the deferral, it does not invalidate it:** the v194 diff is now the **in-repo precedent** for the deferred AC-R10 follow-up — 4 union members + curated entries in all three maps (+18 lines in `EventLog.tsx`, +5 in `types.ts`). When phase 1 lands the decision-lane tokens, the FE follows the same shape, **in the same commit as the BE change**.
- **ADR-15 vocabulary rename (folded):** the suppression outcome's event name is **`member_suppressed`**, not `member_skipped` (`decisions.md:917-919`) — "lane-scoped, consistent with the AC-R10 pick/attempt/suppress vocabulary; avoids the 68-hit v193 `member_skipped` namespace and v194's `model_fallback_*`". The plan's "pick vs attempt vs skip" wording follows **ADR-10's AC-R10 text** (`decisions.md:733`, which still reads "skip"); the **token** the deferred FE extension will add is the ADR-15-vocabulary `member_suppressed`. F1's fixture therefore casts an arbitrary unnamed token — it does not hard-code `member_skipped`, so the rename does not touch it.

### 3. Warning 11 — lane-consistency guard degenerated to delete+recreate: **ENCODED (minimal fix)**

As written, the §5.2 guard blocked on **any** member id outside `laneModels` — including when the stored group's lane is **unknown**. An unknown-lane group is listed **only** in the text tab (§3.2) and never in the decision tab, so the guard made it **uneditable in BOTH tabs** — the only path left was delete + recreate, losing the row, the id, the shadow claim and the enable state (and risking the 409 member-of-another-group rejection).

**Encoded (the dispatcher's first suggested form):** the guard blocks only when the group's members are **KNOWN-lane inconsistent** — never when the lane is unknown/empty. Concretely (§5.2, marked in place):

1. `SettingsPage` passes the kind index it **already builds** for the lane split down as one new optional prop — `kindIndex?: Map<string, string>` on `GroupModelForm`.
2. The guard computes the stored lane with the existing helper: `inferGroupLane(initialData, kindIndex)`.
3. Block **only** if `storedLane !== null && storedLane !== lane`.
4. `storedLane === null` (unknown / mixed / unresolvable) → **do not block**; the submit proceeds and the server decides (write-time homogeneity rejects a mixed write; G3 rejects a missing member). ADR-15 keeps `groupLaneOf` fail-open for precisely these legacy/hand-edited shapes (`decisions.md:913-916`) — they are the rows an operator most needs to repair.
5. Absent `kindIndex`, the original conservative rule stands (defence-in-depth default); the parent always supplies it.

**Residual (accepted, stated):** an operator can now deliberately re-lane a broken unknown-lane group from the text form — a legal write. Strictly better than the dead end: the group was already unservable, so nothing that worked is lost. **Owner:** encoded in this plan (Stage 2, `GroupModelForm`); no deferred owner. Tests: **D6a–D6d**.

**ADR-19 — deviation 3, ACCEPTED RESIDUAL (documented in-code).** The server reads member rows
**directly** (`pkg/store/database/store_group_rewrite.go:369-384`); the FE holds **list snapshots**.
The FE therefore mirrors the **collection-shaped twin** at
`pkg/ui/frontend/src/components/config/group_lane.go:137-154` — **no new endpoint, no new hook**. The
deviation is documented **in-code** at
`pkg/ui/frontend/src/components/config/groupLane.ts:91-113`. **Accepted for this release**; the two
answers are one answer by construction, not by discipline.

### 4a. THE FE TEST NAMING CONTRACT (B2, approver escalation — HARD CONTRACT, the FE developer implements this exactly)

Phase 3's anti-vacuous gate greps the **verbose** vitest log for each promised case id with the
whole-word+separator pattern `[^A-Za-z0-9]<id>[^A-Za-z0-9]*[—:-]`. That grep can only fire if the
titles carry the ids. The contract:

1. **Every new or updated test title starts with its case id, then an em-dash, then the
   description** — exactly: `<id> — <description>` (e.g. `A1 — renders the container with two child
   tabs`, `T1 — the text picker still offers an inactive chat model`, `V1 — the provider surface is
   exactly the decision vocabulary`).
2. The separator is the **em-dash `—`** (U+2014) with single spaces either side — this is the exact
   token shape the phase-3 pattern expects; do not substitute a hyphen or colon.
3. **One case id per test, at the START of the title** — a nested `test()`/`it()` inside a describe
   block carries its own id; a describe title never carries a case id.
4. **No other text in the suite may match another case's token**: never write a bare `A1`/`B9`/… in
   a title, comment, or string literal except as that case's own leading id (a neighbouring `A1`
   mention could falsely satisfy `A10`'s or its own grep from the wrong line).
5. **All 48 tokens are matchable against real titles**: A1–A13, B1–B7, B9, C1–C7, D1–D6, D8–D10,
   E1–E8 (the 45 new-file cases) **plus T1, F1, V1** (the updated-file cases) — each is the leading
   id of exactly one test, so phase 3's per-token grep fires on a real pass line (`✓ … <id> — …`)
   and can never be satisfied by a file path or a passing neighbour.

6. **Forward note (label-namespace collision — NOT a defect in this contract):** upstream's
   credential-groups commission **reused the A1-A13 / B1-C7 / D1-D4 / E1-E3 label space** in 5 of
   **their** files, so a whole-log census can match 2–3 lines for 35 of our tokens. **Our contract
   holds** — each of our 48 tokens has exactly one line across **our 8 files** — so phase 3's census
   is **file-scoped** (each token is censused only within the file this contract assigns it), which
   is a **text-only** gate change. **Renaming our 48 titles into a `DG-` namespace would be a SOURCE
   change and belongs to a FUTURE TEST-ONLY COMMISSION** — it is explicitly out of scope here (no
   source change, no test-title change in this release).

### 4. Warning 12 — fixed-constants copy: **lane-gated**


`GroupModelForm.tsx:263-265` (`data-testid="group-form-fixed-constants"`) renders *"…backup-only members serve as last-resort fallback **in the race chain**"* for **both** lanes. On the decision lane that is false (ADR-11 OQ-3 option (a): structural loop contract, **no race engine**; OQ-2: no chain). The copy is now lane-gated (§1.4, marked in place): the `lane='decision'` string keeps the real backup-tier semantics and drops the race-chain promise — *"backup-only members are recourse-tier candidates (served only after every default member has failed). No fallback chain and no racing on this lane."* Pinned by **D10**.

### 5. Warning 13 — B8/D7 were vacuous: **removed; intent subsumed by D5; the real pin is V1**

**Disposition: REMOVED.** B8 ("the decision-lane group form renders no provider field") and D7 ("…no provider select") asserted the absence of a field the group form **never had** — `GroupModelPayload` (`GroupModelForm.tsx:24-34`) has no provider key and never did, so both tests would pass even if the commission did nothing.

- Their **intent** ("no provider added to the group surface") is now subsumed by **D5**, upgraded to an **exact key set** assertion (`toEqual` on the whole payload: `id, name, kind:'group', internal:true, members, shadow_model_id, enabled, fallback_chain:[], credentials:[]`) — any extra key, a provider field included, fails it.
- The **real** provider pin this commission owes is the FE half of **ADR-15's T5 set-equality pin** (`decisions.md:922-924`: "'NO vocabulary change' is pinned, not asserted"): new case **V1** in `components/config/__tests__/DecisionModelForm.test.tsx` — the rendered provider surface is **exactly the set** `{openai, typesafe, openrouter}` (order-insensitive; the existing order-sensitive pin at `:271-286` is unchanged and stays green) **and** is disjoint from the credential vocabulary (`minimax`, `9router`, `azure`, `zhipu`, `zai`, `grok`, `deepseek`, `opencode`, `opencode-go`, `lightllm`, `anthropic`, `gemini` must never appear as options).
- **Corrected counts (phase 3 consumes this):** new-file cases **44** (A1–A13 = 13, B1–B7 + B9 = 8, C1–C7 = 7, D1–D6 + D8–D10 = 9, E1–E7 = 7) + updated-file cases **3** (T1, F1, V1) = **47 new FE cases** (was 48). Updated files are now **5** (GroupModelsTab.test.tsx, GroupModelForm.test.tsx, GroupMemberEditor.test.tsx, EventLog.test.tsx, **DecisionModelForm.test.tsx**); LEFT ALONE is now **7** files. `DecisionModelForm.test.tsx:271-286` stays green with no edit to its existing assertions.

### 6. Warning 14 — staleness: **both bounds stated** (§3.2 and Risk #1, marked in place)

- **Bound 1 — 15 s cache TTL (cache-reuse only):** `getOrFetch(key, fetcher, 15000)` — `useApi.ts:285-292` (`'models'`), `:544-551` (`'decision-models'`), `:648-655` (`'group-models'`). It bounds how long a *triggered* refetch may reuse a cached response; a **Retry** pressed within the window returns the cached — possibly stale — response.
- **Bound 2 — unbounded for the live session (the real bound):** the hooks mount **once** in `App.tsx` (`:37` decision, `:43` group) and fetch **once** on their own mount (`useApi.ts:388-392` / `:605-610` / `:705-710`). No polling, no TTL-driven refetch of hook state. A mounted surface refreshes only on a same-hook mutation (`useApi.ts:572-575` / `:674-677`), a Retry (bound 1), or an app reload. **A mis-filed group stays mis-filed for the whole session.**
- Optional mitigation **not** implemented — **DISPOSITION (leader, 2026-11-16): ACCEPTED-BY-LEADER this release; recorded as wishlist entry 404ad40b (project future list).** The session-long bound stands for v1.

### 7. Warning 7 (FE part) — stale prose corrected

- **Case counts:** all "46 cases / 48 new FE cases" prose is superseded by **47** (§5 above; the Amendment 1 list below carries a supersession marker).
- **Contract numbers verified ACCURATE and unchanged:** the ruling's FE contracts are still **23–30** (`decisions.md:391-400`), so this plan's "contract 23/24/25/26/29" and "contracts 23–30" citations stand; no `FE #23-27`-style range reference exists in this document (grepped — zero hits).
- **decisions.md line re-pins (the ruling doc grew from 828 → 1022 lines when ADR-13/14/15/16 were appended):** `:131`→`:137` (D-2, ×3), `:352-386`→`:362-400` (contracts list), `:379-386`→`:391-400` (FE contracts), `:398`→`:412` (Q5 — **now RESOLVED** with the E1 ruling recorded), `:399`→`:413` (Q6), `:356`→`:370` (contract 3), `:331-338`→`:345-352` (blast-radius FE sites), `:722-744`→`:720-748` (ADR-10), `:735`→`:733` (AC-R10, ×2), `:748-776`→`:750-778` (ADR-11), `:763-767`→`:765-769` (OQ-3, ×2), `:769-773`→`:771-775` (OQ-4), `:773`→`:775` (two populations, ×2), `:778-807`→`:780-809` (ADR-12, ×2), `:396`→`:410` (Q3). The ADR-1..ADR-9 ranges were verified unchanged (`:426`, `:584`, `:622`, `:656`, `:675`, `:695`).

### 8. Statics (G11 premise, re-stated for phase 2's scope)

v194 rebuilt `pkg/ui/static/`; the tracked baseline at `2b987bf` is `index.html`, `assets/index-D10Xutfq.js`, `assets/index-CO0WMZaa.css`, `assets/SettingsPage-BZbFjqam.js`. The G11 premise for **this** commission is unchanged in rule and different in fact: **the FE source WILL change here**, so the regenerated assets **will differ** and ride the **same commit** as the source (ruling contract 29, `decisions.md:399`). The **zero-mod `sha256sum` proof applies only to a rebuild with no FE source change** — it is the mechanism check, run once, not a claim about this commission's build output.

### Not verified (Amendment 2)

- Nothing was executed — no `vite build`, no `tsc`, no `vitest`, no smoke, no commit. Static reasoning from direct reads at `2b987bf` plus the ruling text.
- The FE delta was verified by `git diff` over `pkg/ui/frontend` between the two bases (2 files), not by re-reading every FE file end-to-end; the unchanged-pin claim for `components/config/`, `SettingsPage.tsx`, `hooks/`, `utils/` rests on that empty diff.
- `decisions.md` was read in the ADR-15/ADR-16 regions and the re-pinned line targets were spot-verified by grep; the full document was not re-read end-to-end.
- Whether phase 1 will expose a server-side candidate list (which would let the FE narrow the `laneModels` derivation instead of filtering in the editor) remains open — unchanged from Amendment 1's coupling table.
