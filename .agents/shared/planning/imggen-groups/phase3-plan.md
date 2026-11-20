# Phase 3: Frontend — ImgGen Groups container, Text-lane consolidation, the shared sub-tab container

Date: 2026-11-20
Author: plan-creation worker (phase 3 — frontend) via plan-creation skill
Status: Draft — pending approver review
Base: `feature/imggen-groups-text-subtabs` @ `5cf1ecdc` (== `origin/latest`; worktree prepared by giter, claim `wt.claim.imggen-groups`)
Worktree: `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-imggen-groups` (never the main checkout — it holds `master` + uncommitted `.agents/` session edits)
Commission: imggen-groups — (1) image-gen models get a container with two nested sub-tabs (`models` | `group`) mirroring the decision lane; (2) the Text lane's two top-level tabs (Text Models + Group Models) collapse into ONE container with left vertical sub-tabs `models` | `group`, behavior otherwise IDENTICAL; (3) all three kinds share the IDENTICAL sub-tab container.
Parallel phases: P1 (backend group machinery) and P2 (authz composition) are planned by a separate worker. This plan does NOT plan backend internals; every backend shape it leans on is recorded under §6 "Interface assumptions (reconcile in plan-overview)".
House format: `.agents/shared/planning/decision-groups/phase2-plan.md` / `phase3-plan.md` (matched).

**Scope of this artifact:** markdown plan only. NO code, NO tests written, NO commits, NO pushes, NO builds. `tsc`/`vitest`/`vite` were NOT run (plan-phase discipline) — every code claim below is static reasoning from direct reads of THIS worktree at `5cf1ecdc`, all anchors re-verified 2026-11-20 (the dispatcher's research notes warned ±1 line drift; drift found and corrected inline — e.g. chatLaneGroups const at `SettingsPage.tsx:345`, not :341; GroupMemberEditor hint literals at `:221`/`:225`). Each code claim carries `path:line`; unverified items are collected under §13 "Not verified".

---

## Objective

The admin UI's three model kinds — Text, Image Gen, Decision — each render as a top-level tab hosting the IDENTICAL two-child sub-tab container (vertical left nav, `models` | `group`): the Text lane's separate top-level "Group Models" tab retires (bar 11 → 10 tabs, `TabType` loses `'group_models'`) with its group surface moving inside the Text container's `group` child; the ImgGen lane gains a `group` child backed by an `'image-gen'` arm of the group-lane vocabulary (member picker, copy maps, lane fold) whose server twin lands in the SAME commit; chat-lane behavior and copy stay byte-identical, with exactly one DECISION-lane navigation string that names the retired tab reworded (§4.4).

**Completion test (one sentence):** with the phase landed, the settings bar shows exactly 10 top-level tabs; clicking Models / ImgGen Models / Decision Models each shows the same-shaped `[Models | Group]` vertical nav; a group whose members are image-gen models appears only in the ImgGen container's Group child; a chat model row's "wrap as group" affordance lands on the Text container's Group child with the add-form pre-filled; and `DecisionModelsContainer.test.tsx` is green with zero edits.

---

## Context — verified inventory (all anchors re-read at `5cf1ecdc`)

| Fact | Evidence |
|---|---|
| No router; plain state tabs; 11-value `TabType` incl. `'group_models'` | `pkg/ui/frontend/src/components/SettingsPage.tsx:79` (union), `:116` (`useState<TabType>('proxy')`) |
| Text Models tab = `'models'` | button `SettingsPage.tsx:453-462`; renders `ModelsTab` `:622-634` (`onWrapAsGroup={handleWrapAsGroup}` `:632`) |
| Group Models tab = separate top-level `'group_models'`, deliberately LAST | comment `SettingsPage.tsx:559-564` ("Positioned last (slot 11 of 11) so the pinned Decision→Credentials adjacency … is preserved"); button `:565-577` (`data-tab-id` `:572`, `aria-label="Group Models"` `:573`); renders `GroupModelsTab lane="chat"` `:709-725` (`models={chatLaneGroups}` `:712`, `laneModels={chatKindModels}` `:713`, `wrapPreloadModel={wrapTarget}` `:722`, `onWrapConsumed` `:723`) |
| ImgGen tab = flat `'imggen_models'` | button `:461-477` (`data-tab-id` `:473`); renders `ImgGenModelsTab` `:643-655` |
| Decision tab = the container precedent | button `:479-494`; renders `DecisionModelsContainer` `:673-696` (`groups={groupLaneSplit.decision}`, `laneModels={decisionModels}`, `kindIndex`, group mutations) |
| Kind index + client-side lane split live in SettingsPage | `buildKindIndex` call `:334-339`, `splitGroupsByLane` `:340`, `chatLaneGroups = [...chat, ...unknown]` `:345`, `chatKindModels` filter `:306` |
| Wrap flow navigates by setting the top-level tab | `handleWrapAsGroup` `SettingsPage.tsx:298-301`: `setWrapTarget(model); setActiveTab('group_models')`; `wrapTarget` state `:122` |
| The extraction source container | `pkg/ui/frontend/src/components/config/DecisionModelsContainer.tsx` (171 lines): `ChildTab` `:11`; pass-through props `:13-42`; `NAV_ITEMS {child,label,ariaLabel,testId}` `:49-62`; `ACTIVE_CLASS`/`INACTIVE_CLASS` `:74-77` (top-bar pair translated `border-b-2`→`border-l-2`, `border-l-transparent` keeps geometry); default `'models'` `:101`; no-tab-roles comment `:105-112`; nav rail `:113-133` (`w-40 shrink-0 flex flex-col gap-1 border-r border-gray-700 pr-2`, `aria-label="Decision Models sections"` `:116`); panel `:140-168` (`flex-1 min-w-0`, conditional mount/unmount — child state discarded on switch); **no container heading** (`:6-10` comment — panels own headings) |
| Second container precedent (fetch-owning — NOT the pattern to copy) | `pkg/ui/frontend/src/components/config/CredentialsContainer.tsx`: child union `'credentials'|'groups'` `:80`; owns fetch+status `:81-123`; same no-heading rule `:30-31`. The shared core must stay PASS-THROUGH; Credentials' ownership stays in its wrapper — out of scope |
| Group UI already lane-parameterized | `GroupModelsTab.tsx`: `lane` REQUIRED no default `:13-16`; copy maps `TOAST` `:49-61`, `HEADING` `:66-69`, `CTA` `:71-74`, `EMPTY_TITLE` `:76-79`, `EMPTY_BODY` `:81-91`, `BADGE_GLYPH` `:93-96` (🔀 chat / 🎯 decision); wrap pre-fill CHAT-ONLY (`:31-34` props doc, `:125-139` effect gates `lane === 'chat'`); save = POST via `onAddModel` / PUT re-stamps `kind/internal/members/shadow_model_id/fallback_chain/credentials` wholesale `:160-184` (BE PUT decodes the body wholesale — sparse PUT would skip the group validation arm) |
| Group form hardcoded stamps + copy | `GroupModelForm.tsx`: `HARDCODED_KIND='group'` `:17`, `HARDCODED_INTERNAL=true` `:18`; `SHADOW_TARGET_MISSING` `:29-32`, `FIXED_CONSTANTS_COPY` `:33-36`, `LANE_GUARD_MESSAGE` `:41-44` (doc `:38-40`) — **keyed by the SURFACE lane (the form's `lane` prop); each value names the misfiled group's ACTUAL (other) lane and its home surface** — chat arm `:42` points at `Decision Models → Group`, decision arm `:43` points at `Group Models`; payload interface `:50-61`; lane-consistency guard uses `inferGroupLane` `:264`, message `LANE_GUARD_MESSAGE[lane]` `:272` |
| Member editor lane seam | `GroupMemberEditor.tsx`: `MAX_GROUP_MEMBERS = 16` `:5` (mirrors Go `models.MaxGroupMembers`); `lane` REQUIRED `:15`; picker filter `:58-60` (chat: `!m.kind \|\| m.kind === 'chat'` — NO enabled filter, historical; decision: `m.kind === 'decision' && m.enabled` — ADR-7/AC-R6 eligibility clause); weight input `:145-152` (invalid parse → 0, caught at submit); `backup_only` checkbox `:159-170`; hint copy block `:216-225` (chat literal `:221`, decision literal `:225`) |
| FE lane vocabulary | `types.ts:211` `export type GroupLane = 'chat' \| 'decision'` (doc `:201-210`: additive-only, never persisted, never on the wire — ruling D-2); `groupLane.ts` (212 lines): drift-warning header `:3-16`; `ModelKind` `:27`; `memberLane` `:58-62` (image-gen → **null**, no lane evidence); `KindIndexSources` `:69-74`; `buildKindIndex` `:96-113` (already indexes `imgGenModels` as `'image-gen'` `:109`); `inferGroupLane` `:145-163` (empty-id skip `:153`, vanished member `:155`, no-lane member `:157`, unanimity `:161`); `GroupLaneSplit {chat,decision,unknown}` `:173-177`; `splitGroupsByLane` `:194-212` |
| Unknown-bucket policy today | unknown (mixed/empty surviving set) folds into the TEXT list: `groupLane.ts:181-184` ("the text lane receives chat + unknown … next to its historical home, where the operator can repair it") + `SettingsPage.tsx:345` |
| Server twin of the fold | `pkg/models/group_lane.go`: `GroupLaneChat` `:38`, `GroupLaneDecision` `:42`; `MemberLane` `:53-60` (doc `:46-52`; image-gen returns ok=false); `GroupLanesOf` `:72`; `UnanimousGroupLane` `:103` (doc `:95-102` — "the SINGLE-SOURCE-OF-TRUTH fold"). Request-time consumer: `pkg/store/database/store_group_rewrite.go` (fold cited at `groupLane.ts:122-125`). **P1 owns the server arm; FE mirrors it** |
| Server models API has NO lane parameter | `pkg/ui/server.go` `handleModels` GET switch `~:1004-1055`: kind arms `""` / `image-gen` / `decision` / `group` / `chat` — groups arrive as ONE list via `?kind=group`; lane classification is client-side (`buildKindIndex`+`splitGroupsByLane`). **image-gen groups need NO new endpoints** — a group is an ordinary `kind:'group'` row whose members are image-gen models, saved via existing POST/PUT `/fe/api/models` |
| API layer | `useApi.ts`: `API_BASE '/fe/api'` `:20`; `useModels` `:282`; `useImgGenModels` `:421` (GET `?kind=image-gen` `:438`); `useDecisionModels` `:536` (`:549`); `useGroupModels` `:640` (`?kind=group` `:653`); imggen probe `POST /fe/api/image_generation/test` `:1451`; decision probe `:1432`. Cache: `useGroupModels` mutations delete ALL FOUR cache keys (`'group-models'`,`'models'`,`'imggen-models'`,`'decision-models'`) — add `:678-681`, update `:693-696`, delete `:703-706` — so a group created in the imggen child refreshes every lane's list on next mount. No new hook work needed |
| ImgGen provider vocabulary (3-gate atomicity context) | FE constant `ImgGenModelForm.tsx:9` `PROVIDER_OPTIONS=['minimax','9router']`; `DEFAULT_BASE_URLS` `:24-27`; BE gate note (primary credential provider must be minimax\|9router) `:109-112`. Group members are image-gen MODELS, not providers — **no new provider vocabulary is introduced by this phase**; the group row carries no provider field (`GroupModelPayload` `GroupModelForm.tsx:50-61`). POK `decision-provider-vocabulary` atomicity rule applies only if vocabulary is touched — it is not (verified: no `PROVIDER_OPTIONS` change in scope) |
| tsc baseline discipline | `pkg/ui/frontend/tsc-baseline.txt`: frozen **18 errors / 9 files**, `SettingsPage.tsx = 5` (`:29`); header history `:3-26` (26/11 → 19/9 → 18/9 by credential-groups); machine footer `TOTAL_ERRORS=18` `:38`, `TOTAL_FILES=9` `:39`; verifier `src/utils/__tests__/tsc-baseline.test.ts` auto-runs in vitest. **Verdicts are per-file COUNT + exact file-SET, never line numbers** — moving code does not break the baseline; adding/dissolving errors does |
| Container test precedent (the mirror source) | `pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx`: A1 `:82` … A13 `:212`; A4 (nav buttons NOT matched by top-level tab-bar selector `div.flex.overflow-x-auto > button`) `:107-115`; exact-testid assertions `:173-175`; A10 no ARIA tab roles `:179`; A12 unmount-on-switch `:196-209`; A13 verbatim pass-through `:212` |
| Tabs test (must be updated) | `pkg/ui/frontend/src/components/__tests__/SettingsPage.tabs.test.tsx`: slot walks via `div.flex.overflow-x-auto > button`; the 11th-tab test `:270-296`; Group Models click-mount `:297-332` (incl. emoji check `:332`) |
| Lane-fold test (must be amended) | `groupLane.test.ts`: E1-E8; **E6** `:115` (split: chat+unknown → text, decision only → decision); **E8** `:176-…` uses an **image-gen member as the no-lane example** — its premise changes when image-gen becomes a lane vote; re-derive around kind `'group'` (still no-lane) and add image-gen lane cases |
| Chat-lane copy freeze | ADR-9 / AC-R12 markers: `GroupModelsTab.tsx:45-48`, `GroupModelForm.tsx:22-28` ("The chat-lane strings are the historical text BYTE-FOR-BYTE") |
| Stale-copy landmines (found by THIS plan's reads; arm attribution corrected by approver iteration 001) | `GroupModelForm.tsx:43` `LANE_GUARD_MESSAGE.decision = 'This is a chat group — edit it under Group Models.'` — names the retiring top-level tab. Keying: the map is keyed by the SURFACE lane, so this is the DECISION surface's pointer for a misfiled CHAT group (whose home after consolidation is the Text container's Group child) — the chat arm `:42` points at `Decision Models → Group` and stays valid; `ModelsTab.tsx:353` comment ("their own Group Models tab"); `SettingsPage.tsx:562` comment rides out with the deleted button |
| Test framework | vitest + @testing-library/preact + happy-dom; suites co-located in `__tests__/` (`pkg/ui/frontend/src/components/config/__tests__/` listing verified) |

---

## Scope

### In Scope
- Extract the decision lane's container into a shared, pass-through sub-tab container; rewire the decision lane onto it **with zero behavioral/test-visible change**.
- New Text container: Text Models tab hosts `[models | group]`; the top-level `'group_models'` tab retires (bar 11→10; `TabType` drops the value); the one-click wrap flow re-routes into the container's `group` child.
- New ImgGen container: ImgGen Models tab hosts `[models | group]`; the `group` child is `GroupModelsTab lane="image-gen"` fed by the new `'image-gen'` bucket of the client lane split.
- FE half of the imggen group-lane vocabulary: `GroupLane` grows an `'image-gen'` arm (lane VALUE ruling C-1, §6 IA-7); `memberLane`/`inferGroupLane`/`GroupLaneSplit`/`splitGroupsByLane` grow the arm; every `Record<GroupLane,…>` copy map gains the `'image-gen'` arm; member picker gains the `'image-gen'` eligibility arm — **with the server fold twin (`pkg/models/group_lane.go`) in the SAME commit** (content per P1; §6).
- The unknown-bucket policy for 3 lanes (decision taken, §3.3).
- All FE test work: new container suites per kind, image-gen-lane trio mirror, `groupLane.test.ts` re-derivation, `SettingsPage.tabs.test.tsx` updates, tsc-baseline verdict.
- FE build (`vite build` → tracked `pkg/ui/static/`) and its gate evidence.

### Out of Scope
- Backend group machinery for imggen serving (validation gates accepting image-gen members, pick/sweep behavior, metering) — **P1**. This plan records interface assumptions only (§6).
- Authz composition (allowed_groups interplay with image-gen groups) — **P2**.
- Any new `/fe/api` endpoint or hook — verified unnecessary (groups ride the existing models CRUD; lane derived, never on the wire).
- `CredentialsContainer` migration onto the shared core — it owns fetch+status (a different contract); the commission names Text/Image-Gen/Decision only.
- Any refactor of `ModelsTab`, `ImgGenModelsTab`, `DecisionModelsTab` — each is **wrapped, not refactored** (house decision, `decision-groups/phase2-plan.md` §1.3 rationale applies verbatim: pass-through container, panels own their headings).
- Provider vocabulary changes — none needed (verified above).
- Any chat-lane copy rewording — NONE (§4.4: the one stale navigation string is a DECISION-lane string; the chat-lane freeze holds absolutely).

---

## 1. Components

### 1.1 New files

| File | Responsibility | Effort |
|---|---|---|
| `pkg/ui/frontend/src/components/config/SubTabContainer.tsx` | The shared pass-through core: nav-rail JSX, `ACTIVE_CLASS`/`INACTIVE_CLASS`, a11y posture (native `type="button"` buttons, active-only `aria-current`, **no** tablist/tab/tabpanel roles, no aria-selected, no roving tabindex), conditional mount/unmount panel, no container heading. Parameterized: `items` (child/label/ariaLabel/testId), `navAriaLabel`, testid namespace (`containerTestId`/`navTestId`/`panelTestId`), `defaultChild`, optional controlled pair `activeChild`+`onChildChange` (§2.3), `renderPanel(activeChild)`. Every preserved behavior cites `DecisionModelsContainer.tsx:74-77,:101,:105-133,:140-168` | S |
| `pkg/ui/frontend/src/components/config/TextModelsContainer.tsx` | Thin wrapper over the core: nav `models`/`group`; models child mounts `ModelsTab` **verbatim** (incl. `onWrapAsGroup`, `onNavigateToCredentials`); group child mounts `GroupModelsTab lane="chat"` with `chatLaneGroups`, `chatKindModels`, `kindIndex`, wrap pre-fill pair. Exposes the controlled pair for the wrap flow | M |
| `pkg/ui/frontend/src/components/config/ImgGenModelsContainer.tsx` | Thin wrapper: models child mounts `ImgGenModelsTab` verbatim; group child mounts `GroupModelsTab lane="image-gen"` with `groupLaneSplit['image-gen']`, `imgGenModels` feed, `kindIndex`, group mutations (mirrors the decision container's prop shape, `SettingsPage.tsx:683-696`) | S |

Test files (new): `__tests__/TextModelsContainer.test.tsx`, `__tests__/ImgGenModelsContainer.test.tsx`, `__tests__/GroupModelsTab.imageGenLane.test.tsx`, `__tests__/GroupModelForm.imageGenLane.test.tsx`, `__tests__/GroupMemberEditor.imageGenLane.test.tsx` (§9). Suite names align to the lane VALUE per C-1 (the decision-lane precedent names suites after the lane: `*.decisionLane.test.tsx`).

### 1.2 Changed files

| File | Change | Effort |
|---|---|---|
| `types.ts` | `GroupLane` `:211` grows `'image-gen'` — the lane VALUE mirrors the server constant literally, so lane value == kind value (ruling C-1, §6 IA-7); additive; doc block `:201-210` updated to three lanes | S |
| `groupLane.ts` | `memberLane` `:58-62` gains `'image-gen' → 'image-gen'` (lane value EQUALS the kind value — C-1/IA-7); kind `'group'` + unknown stay null; `inferGroupLane` `:145-163` widens the lane set (`:150`) and the single-lane return (`:162`); `GroupLaneSplit` `:173-177` gains the `'image-gen'` bucket key (dashed key, quoted in TS); `splitGroupsByLane` `:194-212` routes the `'image-gen'` bucket; header comments `:3-16`, `:48-57`, `:132-135` updated (image-gen is now a lane vote). **Server twin in the same commit (§7)** | M |
| `GroupModelsTab.tsx` | Every `Record<GroupLane,…>` gains the `'image-gen'` arm: `TOAST` `:49-61`, `HEADING` `:66-69`, `CTA` `:71-74`, `EMPTY_TITLE` `:76-79`, `EMPTY_BODY` `:81-91`, `BADGE_GLYPH` `:93-96`. Chat + decision arms byte-unchanged | S |
| `GroupModelForm.tsx` | `SHADOW_TARGET_MISSING` `:29-32`, `FIXED_CONSTANTS_COPY` `:33-36`, `LANE_GUARD_MESSAGE` `:41-44` gain `'image-gen'` arms; **decision-arm guard reword** `:43` (§4.4 — the ONE sanctioned string delta; the chat arm `:42` is untouched and the chat freeze holds absolutely) | S |
| `GroupMemberEditor.tsx` | Picker `:58-60` gains the `'image-gen'` arm (`m.kind === 'image-gen' && m.enabled` — mirrors the decision eligibility clause; §6 IA-4); hint block `:216-225` gains the `'image-gen'` arm | S |
| `SettingsPage.tsx` | `TabType` `:79` drops `'group_models'`; delete the Group Models button `:559-577` (comment rides out) and branch `:709-725`; `'models'` branch `:622-634` mounts `TextModelsContainer` (models props verbatim + group props + controlled child pair); `'imggen_models'` branch `:643-655` mounts `ImgGenModelsContainer`; `handleWrapAsGroup` `:298-301` re-routes (`setActiveTab('models')` + set text child `'group'`); `chatLaneGroups` `:345` keeps chat+unknown (§3.3) | M |
| `pkg/models/group_lane.go` (+ the fold's request-time twin if P1 locates the arm there) | **The server half of the same-commit lane-fold coupling**: `GroupLaneImageGen` const + `MemberLane` `case KindImageGen` (content owned by P1; §6 IA-2). Go-side tests belong to P1's plan; this plan's gate covers the FE side + the same-commit file-list proof | S (FE commit) |
| `pkg/ui/frontend/tsc-baseline.txt` | Only if the restructure deliberately changes a frozen count (expected direction: a frozen SettingsPage error dissolving with the deleted branch → 18→17 or unchanged) — update counts + header note, precedent `:17-26` | S |
| `__tests__/SettingsPage.tabs.test.tsx` | Retire/rewrite the group_models slot + click tests `:270-332`; bar-count walks 11→10; new container-default assertions (§9 T12) | M |
| `__tests__/groupLane.test.ts` | Re-derive E8 around kind `'group'` (no-lane exemplar that stays no-lane); add `'image-gen'` lane cases E9-E12 (§9 T12) | M |
| `pkg/ui/static/**` | Rebuilt output of `vite build` (tracked embedded area; `vite.config.ts` `outDir '../static'`) — changes are EXPECTED for this phase | — |

### 1.3 Must stay green with ZERO edits (the freeze proofs)

| Suite | Guards |
|---|---|
| `DecisionModelsContainer.test.tsx` (A1-A13) | The extraction is behavior-invisible to the decision lane — **the strongest single gate in this phase** |
| `GroupModelsTab.test.tsx`, `GroupModelForm.test.tsx`, `GroupMemberEditor.test.tsx` | Chat-lane byte-freeze (ADR-9/AC-R12) |
| `GroupModelsTab.decisionLane.test.tsx`, `GroupModelForm.decisionLane.test.tsx`, `GroupMemberEditor.decisionLane.test.tsx` | Decision-lane arms untouched |
| `ImgGenModelsTab.test.tsx`, `ImgGenModelsTab.imgGenTest.test.tsx`, `DecisionModelsTab*.test.tsx`, `ModelsTab.chatFilter.test.tsx` | Wrapped-not-refactored panels (`ModelsTab.chatFilter.test.tsx:76` carries a stale comment — cosmetic, leave the file unedited) |

Note (verified 2026-11-20, approver iteration 001): neither form suite pins the guard literal — grep `edit it under` over `GroupModelForm.test.tsx` and `GroupModelForm.decisionLane.test.tsx` returns zero hits — so the §4.4 decision-arm reword (`GroupModelForm.tsx:43`) breaks NO existing suite, and the zero-edit claim for the decisionLane trio holds. The pin for the reworded literal is a NEW case in the T10 trio.

---

## 2. The shared container extraction

### 2.1 What is generic vs parameterized

Generic (moves into `SubTabContainer` verbatim, cited from `DecisionModelsContainer.tsx`): the class constants `:74-77`; the nav rail geometry `:113-117`; the button loop with active-only `aria-current` `:118-133`; the no-tab-roles posture `:105-112`; the `flex-1 min-w-0` conditional panel `:140-168`; mount/unmount semantics (child state discarded on switch — matches every top-level branch).

Parameterized: `items[]` (replaces `NAV_ITEMS` `:49-62`), `navAriaLabel` (replaces the literal at `:116`), the testid namespace (`decision-models-container`/`decision-container-nav`/`decision-container-panel`/`decision-container-tab-*` become props), `defaultChild`, `renderPanel`.

MUST preserve (load-bearing, test-pinned): (a) **no container heading** — panels own their headings (`:6-10`); (b) **pass-through props** — the core never fetches, never owns status (the Credentials precedent's fetch ownership stays OUT, `CredentialsContainer.tsx:81-123`); (c) `type="button"` on every nav button (an Enter inside a panel form can never submit through the nav, `:110-112`); (d) namespaced testids — per-kind suites assert exact ids AND non-matching by the top-bar selector (`DecisionModelsContainer.test.tsx:107-115,173-175`).

### 2.2 Wrapper strategy (decision)

`DecisionModelsContainer` becomes a thin wrapper: identical props interface (`:13-42`), identical NAV content, identical testids — its call site `SettingsPage.tsx:673-696` is untouched and its A-suite is green with zero edits. `TextModelsContainer` and `ImgGenModelsContainer` are the same shape for their kinds. Direct use of `SubTabContainer` from `SettingsPage` was considered and rejected: per-kind wrappers keep NAV copy, aria labels, and testid namespaces local, keep `SettingsPage` declarative (house pattern), and give each kind's suite a stable, named target.

**Testid namespaces (pinned for test stability):** Text = `text-models-container` / `text-container-nav` / `text-container-tab-models` / `text-container-tab-group` / `text-container-panel`; ImgGen = `imggen-models-container` / `imggen-container-nav` / `imggen-container-tab-models` / `imggen-container-tab-group` / `imggen-container-panel`. Nav aria-labels: `"Text Models sections"` / `"Image-Gen Models sections"` (pattern: `:116`).

### 2.3 The controlled-child seam (for the wrap flow)

Today `handleWrapAsGroup` (`SettingsPage.tsx:298-301`) switches the top-level tab to `'group_models'`, which MOUNTS `GroupModelsTab`, whose effect (`GroupModelsTab.tsx:125-139`) consumes `wrapPreloadModel` on mount. After consolidation the Text container stays mounted (the user is on its `models` child when clicking wrap) — a purely internal `useState` child (`DecisionModelsContainer.tsx:101`) cannot be commanded from outside.

**Decision: `SubTabContainer` gains an optional controlled pair — `activeChild?: Child` + `onChildChange?: (c: Child) => void` — both-or-neither (runtime guard + doc).** Uncontrolled (neither passed) = today's behavior, byte-identical — Decision and ImgGen containers stay uncontrolled. SettingsPage owns the Text child state; `handleWrapAsGroup` becomes: `setWrapTarget(model); setActiveTab('models'); setTextChild('group')`. The `GroupModelsTab` wrap effect then fires exactly as today (mount + consume-once via `onWrapConsumed`).

Rejected alternative: a mount-consumed signal prop (the wrap-signal pattern) — it only fires on MOUNT, and the container here is already mounted; faking a remount would discard the models child's local state. The controlled pair is the minimal, standard surface.

---

## 3. Lane vocabulary growth (the `'image-gen'` arm)

### 3.1 Type + fold

`types.ts:211` → `'chat' | 'decision' | 'image-gen'` — **the lane VALUE is `'image-gen'`, NOT a new `'imggen'` token** (leader ruling C-1, §6 IA-7): it mirrors the server constant (`pkg/models/group_lane.go`) literally, so after this ruling the lane value EQUALS the model-kind value. TypeScript's `Record<GroupLane,…>` exhaustiveness then FORCES every copy map to grow the arm in the same commit — a feature: the compiler enumerates the surface (maps at `GroupModelsTab.tsx:49-96`, `GroupModelForm.tsx:29-43`, picker + hint at `GroupMemberEditor.tsx:58-60,:216-225`). The dashed value makes arm keys quoted (`'image-gen': {…}`) and bucket access bracketed (`split['image-gen']`).

`groupLane.ts`: `memberLane('image-gen') → 'image-gen'`; `'group'` and unrecognized kinds stay `null` (no evidence) — mirroring the server twin (§6 IA-2). `inferGroupLane`'s set widens to `{'chat','decision','image-gen'}`; unanimity rule unchanged (`:161-162`): exactly one surviving lane → that lane; zero or ≥2 → unknown. `GroupLaneSplit` gains the `'image-gen'` bucket; `splitGroupsByLane` routes it.

### 3.2 Imggen copy (FE-authored; semantics reconcile with P1 — §6 IA-3)

| Map | `lane='image-gen'` (proposed; binding once P1 confirms semantics) |
|---|---|
| `HEADING` | `'Image-Gen Groups'` (Title Case per `ImgGenModelsTab.tsx:239` "Image-Gen Models") |
| `CTA` | `'Add Image-Gen Group'` |
| `TOAST` | `'Image-gen group added/updated/deleted/toggled successfully'` |
| `EMPTY_TITLE` | `'No image-gen groups configured'` (casing per `ImgGenModelsTab` empty state) |
| `EMPTY_BODY` | Placeholder direction: one ID load-balancing image generation across member image-gen models; **final wording derives from P1's serving semantics** (sticky? failover shape?) — must not promise a race engine or a chain the lane lacks (the exact discipline that made the decision lane's copy drop false promises, `GroupModelsTab.tsx:82-91`) |
| `BADGE_GLYPH` | `'🖼️'` (the established imggen glyph, `SettingsPage.tsx:472`) — not 🔀 unless P1 ships the full race chain |
| `SHADOW_TARGET_MISSING` | `'does not exist (must reference an existing image-gen-kind model)'` |
| `FIXED_CONSTANTS_COPY` | Placeholder per P1 semantics (chat arm promises "race chain", decision arm "recourse-tier … no chain no racing" — the `'image-gen'` arm states what P1 actually builds) |
| `LANE_GUARD_MESSAGE` | Keyed by the SURFACE lane; the value names the misfiled group's ACTUAL home (keying per `GroupModelForm.tsx:38-44,:272`). Key `'image-gen'` has TWO possible actual lanes (chat or decision) — the static string must route BOTH homes (placeholder: '…edit it under Models → Group (chat) or Decision Models → Group (decision).'; final wording rides IA-3 review) |
| Member hint | Placeholder per P1 semantics; picker arm `m.kind === 'image-gen' && m.enabled` (§6 IA-4) |

### 3.3 Unknown-bucket policy for 3 lanes (decision taken)

**Unknown (mixed or empty surviving set) stays with the TEXT group child ONLY.** Rationale: (1) the historical-home principle is already the codified rule (`groupLane.ts:181-184` — "next to its historical home, where the operator can repair it") and chat is the historical home of ALL groups; (2) single-home invariant preserved — a row is never invisible, never double-listed (double-listing would also make the lane guard's "edit it under X" ambiguous); (3) a mixed chat+image-gen group is precisely the mis-filed row the operator repairs, and the text child is where the guard message tells them what is wrong; (4) the FE mirrors the fold, not the pick-time consequence (`:186-189`) — the server still fails such groups closed at pick time. Consequence to pin in tests: `chatLaneGroups` (`SettingsPage.tsx:345`) remains `[...split.chat, ...split.unknown]`; the image-gen child receives `split['image-gen']` ONLY. Revisit trigger: if P1's fold semantics differ (§6 IA-2), this policy re-opens in plan-overview — the FE must mirror whatever the server fold says.

---

## 4. Text-lane consolidation

### 4.1 Topology delta (before → after)

BEFORE: 11 top-level tabs (`SettingsPage.tsx:79`); `'models'` renders `ModelsTab`; `'group_models'` (LAST, `:559-577`) renders `GroupModelsTab lane="chat"`. AFTER: 10 tabs — `'group_models'` value, button, and branch all retire; `'models'` renders `TextModelsContainer [models | group]`; `'imggen_models'` renders `ImgGenModelsContainer [models | group]`; `'decision_models'` unchanged (wrapper on the shared core). The Decision→Credentials adjacency is untouched (the retiring tab was deliberately LAST to protect it, `:559-564` — removing the last button cannot break adjacency).

### 4.2 `ModelsTab` and `ImgGenModelsTab` are wrapped, never refactored

House decision re-applied verbatim (`decision-groups/phase2-plan.md` §1.3): the panel components keep their headings, local state, probe state machines; the container adds NO heading; every prop passes through verbatim (incl. `onWrapAsGroup`, `onNavigateToCredentials`, `onCopyModel`, `fetchError`, `onRetry`). Their suites stay green unedited (§1.3).

### 4.3 Wrap flow re-route

`SettingsPage` owns `wrapTarget` (`:122`) and the Text container's child state (§2.3). `handleWrapAsGroup` `:298-301`: `setWrapTarget(model); setActiveTab('models'); setTextChild('group')`. The `GroupModelsTab` wrap effect stays CHAT-ONLY (`:125-139` gate `lane === 'chat'`) and consume-once — behavior identical to today from the operator's seat (click wrap on a chat model row → land in the group add-form pre-filled).

### 4.4 Copy freeze — the ONE sanctioned string delta (a DECISION-lane string)

ADR-9/AC-R12 freezes chat-lane copy — **after this ruling that freeze holds ABSOLUTELY: ZERO chat-string deltas anywhere in this phase.** The one sanctioned delta is a DECISION-lane string: `LANE_GUARD_MESSAGE.decision` at `GroupModelForm.tsx:43`. Keying semantics (source truth, approver-verified): the map is keyed by the SURFACE lane (the form's `lane` prop, consumed at `:272`); each value names the misfiled group's ACTUAL (other) lane and its home surface. The chat arm `:42` (`'This is a decision group — edit it under Decision Models → Group.'`) points at the Decision container's Group child and stays valid, untouched. The DECISION arm's value `:43` reads `'This is a chat group — edit it under Group Models.'` — it is the DECISION surface's pointer for a misfiled CHAT group, and it names the retiring top-level tab; after consolidation the chat home is the Text container's Group child. Leaving it is a defect, not fidelity: a navigation pointer to a nonexistent tab changes the operator's ability to act. The string is minimally reworded to `'This is a chat group — edit it under Models → Group.'` (mirroring the chat arm's established `Decision Models → Group` shape at `:42`). The diff must show EXACTLY this one DECISION-string change; the chat arm `:42` is untouched; a NEW test case (hosted in the T10 image-gen-lane form trio, because the existing decision-lane suite must stay unedited and — verified — never pinned this literal: grep `edit it under` over `GroupModelForm.test.tsx` / `GroupModelForm.decisionLane.test.tsx` returns zero hits) pins the reworded literal. Stale comments (not user-facing): `ModelsTab.tsx:353` and the retiring `SettingsPage.tsx:559-564` block are updated/removed with their code (comment edits do not violate the freeze; the freeze markers at `GroupModelsTab.tsx:45-48` / `GroupModelForm.tsx:22-28` stay in place and govern every chat string).

---

## 5. ImgGen container

`'imggen_models'` branch mounts `ImgGenModelsContainer`: models child = `ImgGenModelsTab` verbatim (props today at `SettingsPage.tsx:643-655`); group child = `GroupModelsTab lane="image-gen"` receiving `groups={groupLaneSplit['image-gen']}`, `laneModels={imgGenModels}`, `kindIndex`, the group mutation quartet + `groupError`/`onRetryGroupModels` — the exact prop shape the decision container receives (`:683-696`). No wrap props (wrap is a chat affordance, `GroupModelsTab.tsx:31-34`). Save path: existing POST/PUT `/fe/api/models` with `kind:'group'`/`internal:true` stamps (`GroupModelForm.tsx:17-18,:50-61`); membership legality is server-side (P1 gates, §6 IA-1) — a premature FE-only save of image-gen members before P1's gates land is an honest BE 400, not an FE defect; sequencing is an overview decision (§6 IA-6).

---

## 6. Interface assumptions (reconcile in plan-overview)

- **IA-1 — Server accepts image-gen members in `kind:'group'` rows** (P1 validation gates). FE saves via existing CRUD; until P1 lands, the image-gen group child still renders/lists (fold-derived) but saves of image-gen members fail with a BE 400. Reconcile: does the image-gen `group` child ship in the same release as P1's gates, or staged after?
- **IA-2 — The server fold grows `GroupLaneImageGen`**: `pkg/models/group_lane.go` `MemberLane` gains `case KindImageGen` (`:53-60`); `GroupLanesOf`/`UnanimousGroupLane` mechanics unchanged (`:72,:103`); the request-time fold consumer mirrors. The server constant's string value is `"image-gen"` — the FE arm mirrors it LITERALLY (IA-7). **The FE arm and this server arm land in the SAME commit** (§7). Whose task list authors that commit (P1 or P3) is an overview sequencing decision (IA-6).
- **IA-3 — Imggen serving semantics** (racing? failover? sticky pinning? backup_only meaning on this lane?) determine the final `EMPTY_BODY` / hint / `FIXED_CONSTANTS_COPY` wording (§3.2 placeholders). FE copy must not promise engines the lane lacks.
- **IA-4 — Member eligibility:** this plan mirrors the decision arm's enabled-filter (`m.kind === 'image-gen' && m.enabled`, `GroupMemberEditor.tsx:58-60` pattern). Revisit if P1 wants inactive image-gen members as candidates (chat keeps NO enabled filter — historical, untouched).
- **IA-5 — No new endpoints/hooks** (verified, not assumed: `server.go` kind switch has no lane param; quadruple cache invalidation already refreshes all lists, `useApi.ts:678-706`).
- **IA-6 — Joint-commit authorship:** the lane-fold commit contains BOTH `groupLane.ts`(+`types.ts`+copy maps) and `pkg/models/group_lane.go`. Go-side tests for the fold belong to P1's plan; this phase's gate proves the FE side + the same-commit file-list.
- **IA-7 — FE lane VALUE = `'image-gen'`** (leader ruling C-1, adjudicated 2026-11-20 — RECONCILED, do not re-open): the `GroupLane` arm value mirrors the server constant literally, so after this ruling the lane value EQUALS the model-kind value `'image-gen'`; the FE value never crosses the wire (ruling D-2 — the lane is inferred at display time, never persisted). Consequences: the `GroupLaneSplit` bucket key and every `Record<GroupLane,…>` arm are `'image-gen'` (dashed key ⇒ quoted keys / bracketed access); test-suite names and fixture lane values align (`*.imageGenLane.test.tsx`); COMPONENT identifiers are NOT lane values and stay as-is (`ImgGenModelsContainer`, `imggen-container-*` testid namespace, `'imggen_models'` tab id). Parity contract: the fold battery compares BUCKET MEMBERSHIP against the server fold, not literal string equality of internal constants (see §9 item 6) — the constants are deliberately identical, but the test contract is behavioral.

---

## 7. Dependencies & coupling

| Coupling | Rule |
|---|---|
| FE lane fold ⟷ server lane fold | **SAME COMMIT** — `groupLane.ts:3-16` drift warning is exactly about this class; the fold is one contract with two halves |
| `types.ts` `GroupLane` growth ⟷ every `Record<GroupLane,…>` map | Same commit (compile-enforced exhaustiveness); the build is red until every arm exists — intentional |
| `SubTabContainer` extraction ⟷ `DecisionModelsContainer` rewiring | Same commit; A-suite green-unedited is the proof |
| `'group_models'` retirement ⟷ `SettingsPage.tabs.test.tsx` updates | Same commit (the suite walks the real DOM; a stale suite fails loudly) |
| SettingsPage restructure ⟷ tsc-baseline verdict | Same commit; deliberate count delta = baseline update + header note in the same commit |
| Every shipping FE commit ⟷ rebuilt `pkg/ui/static/` | `static/` is the TRACKED embedded area; commits that change `src/` must carry the rebuilt output |
| Phase 3 FE ⟷ P1 gates | Loose (IA-1/IA-6): FE can land first with honest 400s on premature image-gen-member saves; overview decides sequencing |
| Phase 3 ⟷ P2 authz | None at FE level (tokens/allowed_groups are BE+FE-TokenForm surface; not touched here) |

---

## 8. Task breakdown (ordered for review)

| # | Task | Depends on | Acceptance |
|---|---|---|---|
| T1 | Create `SubTabContainer.tsx` (generic core per §2.1, incl. optional controlled pair §2.3) | — | Core compiles; no caller yet; class constants/a11y comments carried verbatim with citations |
| T2 | Rewire `DecisionModelsContainer` as thin wrapper (same props/testids/aria/default) | T1 | `DecisionModelsContainer.test.tsx` green with `git diff` EMPTY on the suite; call site `SettingsPage.tsx:673-696` untouched |
| T3 | Grow the fold: `types.ts` `'image-gen'` + `groupLane.ts` arm (§3.1) + **server twin `pkg/models/group_lane.go` in the same commit** (IA-2/IA-6/IA-7) | — | `groupLane.test.ts` amended suite (T12) green; commit file-list shows both halves |
| T4 | Copy-map `'image-gen'` arms: `GroupModelsTab` / `GroupModelForm` / `GroupMemberEditor` (§3.2; placeholders where IA-3 pending) + decision-arm guard reword at `:43` (§4.4) | T3 (type) | tsc clean of NEW errors; chat arms byte-identical (ZERO chat deltas; `:42` untouched); the ONE sanctioned delta = decision arm `:43`; `'image-gen'` arms present |
| T5 | Create `TextModelsContainer.tsx` (§2.2/§4; models child = `ModelsTab` verbatim; group child = `GroupModelsTab lane="chat"`; controlled pair) | T1, T3 | Wrapper compiles; passes every models prop verbatim incl. `onWrapAsGroup` |
| T6 | Create `ImgGenModelsContainer.tsx` (§5) | T1, T3, T4 | Wrapper compiles; prop shape mirrors decision container (`SettingsPage.tsx:683-696`) |
| T7 | SettingsPage restructure: retire `'group_models'` (union `:79`, button `:559-577`, branch `:709-725`); mount both containers; `handleWrapAsGroup` re-route (§4.3); keep `chatLaneGroups` chat+unknown (§3.3) | T5, T6 | Bar = 10 buttons; no `data-tab-id="group_models"`; wrap click lands on Text group child pre-filled (testid-level) |
| T8 | `TextModelsContainer.test.tsx` — full A1-A13 mirror in the `text-container-*` namespace + wrap-flow cases (W1 wrap switches child + consume-once; W2 manual child switch preserves controlled state; W3 unmount-on-switch discards panel state) | T5, T7 | Every case shows literal `--- PASS` (§9.8) |
| T9 | `ImgGenModelsContainer.test.tsx` — A1-A13 mirror in the `imggen-container-*` namespace; group child lists ONLY `split['image-gen']` (feed a mixed split fixture) | T6, T7 | Idem |
| T10 | Image-gen-lane trio mirror (decision-lane trio as template): `GroupModelsTab.imageGenLane.test.tsx` / `GroupModelForm.imageGenLane.test.tsx` / `GroupMemberEditor.imageGenLane.test.tsx` — copy arms, picker filter, guard message (incl. the NEW case pinning `LANE_GUARD_MESSAGE.decision`'s reworded literal at `:43`: render `lane="decision"` with a chat-inferred group; hosted here because the existing decisionLane suite stays unedited — it never pinned this literal, verified), fixed-constants copy | T4 | Idem |
| T11 | `SettingsPage.tabs.test.tsx` update: retire `:270-296` + `:297-332`; bar walk 11→10; new cases — Text tab default child = `Available Models`; ImgGen tab default child = imggen list; Decision adjacency unchanged | T7 | Suite green; deleted cases listed in the gate log (anti-vacuous accounting) |
| T12 | `groupLane.test.ts` re-derivation + extension: E8 re-derived on kind `'group'`; E9 image-gen member → `'image-gen'` lane; E10 mixed chat+image-gen → unknown → TEXT bucket; E11 split routes the `'image-gen'` bucket; E12 three-lane unknown stays text-only | T3 | Idem |
| T13 | Freeze-proof run: all §1.3 suites green with zero edits (`git status` clean on them) | T2-T12 | Gate log lists each suite green-unedited |
| T14 | tsc verdict + baseline: run `npx tsc --noEmit`; if frozen counts changed (expected possible direction: a frozen SettingsPage error dissolving with the deleted branch), update `tsc-baseline.txt` counts + header note in the SAME commit | T7 | Verdict = frozen set OR documented deliberate delta; `tsc-baseline.test.ts` green |
| T15 | Build + embed: `vite build` (writes `../static/`), commit rebuilt output; smoke the served UI (`make build-frontend` path) | T7-T14 | Build succeeds; `static/` delta committed; FE gate evidence in gate log |

Review order note: T1-T2 (extraction) and T3 (fold) are independent entry points; T7 is the integration point; everything after T7 is evidence. Stages can be reviewed commit-by-commit — each of T1+T2, T3+T4(+twin), T5-T7, T8-T15 forms a coherent review unit.

---

## 9. Test strategy (the 8 discipline items, mapped)

1. **tsc baseline (frozen 18/9; SettingsPage=5 at `tsc-baseline.txt:29`)** — verdicts are per-file COUNT + exact file-SET, never line numbers; code motion is safe, count deltas are not. Any deliberate delta (a frozen error dissolving in the deleted `'group_models'` branch is plausible) = update baseline + header history note in the same commit (precedent: credential-groups 19→18, `:17-26`). `tsc-baseline.test.ts` re-asserts automatically in vitest. (T14)
2. **Zero-mod proof — precisely scoped:** this phase CHANGES the FE, so `pkg/ui/static/` changes on build are EXPECTED and shipped; the sha256-before/after zero-mod proof does NOT apply here. It applies to any phase/PR claiming FE-untouched (e.g. P1/P2 backend phases): byte-identical `static/` before/after build as the mechanism proof. State exactly this in the gate log — never claim zero-mod for a phase that edits `src/`. (T15)
3. **Container suites per kind:** mirror `DecisionModelsContainer.test.tsx` A1-A13 (`:82-…:212`) for Text and ImgGen — nav order/labels (A1), nav-first geometry (A2), vertical flex-col no overflow-x (A3), **not matched by top-bar selector** (A4, `:107-115`), default child (A5), click swap + aria-current follows (A6), class pair (A7), `type="button"` everywhere (A8), exact testids (A9), NO tab roles/aria-selected/roving tabindex (A10), panel content (A11), unmount-on-switch (A12), verbatim pass-through (A13). The ORIGINAL A-suite stays green with ZERO edits — that is the extraction's byte-stability gate. (T8, T9, T13)
4. **`SettingsPage.tabs.test.tsx` slot assertions** — `:270-332` retire/rewrite; bar walk 11→10 via the established `div.flex.overflow-x-auto > button` walker; Decision→Credentials adjacency (`:559-564` rationale) re-asserted unchanged. (T11)
5. **Image-gen-lane trio mirror** — the decision-lane trio (`GroupModelsTab.decisionLane.test.tsx` / `GroupModelForm.decisionLane.test.tsx` / `GroupMemberEditor.decisionLane.test.tsx`) is the template for the image-gen trio: copy arms, picker filter (`'image-gen' && enabled`), shadow-target error copy, guard message, fixed-constants copy. (T10)
6. **Lane-fold purity tests** — `groupLane.test.ts` extended so the FE fold agrees with the server fold case-for-case: image-gen member → `'image-gen'` lane; mixed chat+image-gen → unknown → TEXT; `'image-gen'` bucket routing; unknown text-only under 3 lanes; E8 re-derived on kind `'group'` (the no-lane exemplar that stays no-lane). **The parity battery compares BUCKET MEMBERSHIP — which groups land in which lane bucket, case-for-case vs the server fold — NOT literal string equality of internal constants.** The FE and server constants are deliberately identical (IA-7: both `'image-gen'`), but the test contract is behavioral: a rename of either constant alone must NOT silently pass or fail the battery, while any change in WHICH bucket a group lands in MUST fail it. (T12)
7. **Chat-lane copy byte-frozen — ABSOLUTE** — existing chat suites green-unedited and ZERO chat-string deltas anywhere; the ONE sanctioned string change is DECISION-lane (`LANE_GUARD_MESSAGE.decision` reword at `GroupModelForm.tsx:43`, §4.4), pinned by a new literal assertion in the image-gen-lane form trio (T10; the existing decisionLane suite never pinned this literal — verified by grep). (T4, T13)
8. **Anti-vacuous** — every ADDED `it(` shows a literal `--- PASS` in the gate log; deleted cases (tabs-test retires) are LISTED, not silently dropped. Framework: vitest + @testing-library/preact + happy-dom, suites co-located in `__tests__/`. (all)

FE gate commands (run in `pkg/ui/frontend/`): `npx tsc --noEmit` (count/file-set verdict vs baseline) → `npx vitest run` (full FE suite) → `npm run build` (writes `../static/`). No Go tests are owed by this phase's own tasks except the same-commit fold twin's Go-side coverage, which belongs to P1's plan (IA-6) — if the joint commit is authored from this phase, P1's fold-test list must run in the same gate.

---

## 10. Risks

| # | Risk | Impact | Likelihood | Mitigation |
|---|---|---|---|---|
| R1 | tsc baseline drift from the SettingsPage restructure (frozen counts change when code is deleted/moved) | Medium | Medium | Count+file-set verdicts make drift LOUD, not silent; deliberate-delta protocol (update + header note, same commit — precedent `tsc-baseline.txt:17-26`); T14 gates it |
| R2 | Testid regressions (renamed/lost testids break suites or E2E hooks) | Medium | Low | Namespaces pinned in §2.2; A4-style non-match assertions per kind; the decision namespace is frozen by its unedited suite |
| R3 | Shared-container extraction regresses the decision lane (the live v200 surface) | High | Low | T2 gate: `DecisionModelsContainer.test.tsx` green with `git diff` EMPTY — the extraction must be invisible; any edit to that suite is a red flag, not a fix |
| R4 | Unknown-lane bucket policy wrong under 3 lanes (rows invisible or double-listed) | Medium | Low | Policy decided (§3.3: text-only, single-home) + pinned by E10-E12; revisit trigger recorded (IA-2 differs ⇒ policy re-opens in overview) |
| R5 | Wrap-flow regression (pre-fill lost / double-consumed / models-child state discarded) | Medium | Medium | Controlled pair both-or-neither (§2.3); W1-W3 cases (T8); consume-once effect untouched (`GroupModelsTab.tsx:125-139`) |
| R6 | Lane-fold twin drift (FE arm lands without the server arm, or semantics diverge) | High | Medium | SAME-COMMIT rule (§7) + commit file-list proof (T3); parity tests mirror the server fold case-for-case (T12); the `groupLane.ts:3-16` warning is cited in the commit message template |
| R7 | FE copy promises semantics P1 does not ship (race chain / chain / pinning on the image-gen lane) | Medium | Medium | Placeholders gated on IA-3; the decision-lane precedent discipline (drop false promises, `GroupModelsTab.tsx:82-91`) is the rule; final wording blocked on P1's semantics before ship |
| R8 | Sanctioned-delta scope creep (string deltas beyond the ONE decision-arm reword; any chat-string delta) | Medium | Low | Diff review gate: ZERO chat-string deltas; exactly one DECISION-string delta (`GroupModelForm.tsx:43`; chat arm `:42` untouched); chat suites green-unedited (T13); freeze markers stay in place |
| R9 | Cache staleness: image-gen group created in one container not visible in others | Low | Low | Verified mechanism, no new work: quadruple cache-key deletion (`useApi.ts:678-706`) already refreshes every lane list; behavior identical to the decision lane today |
| R10 | Stale deep references to the retired tab (comments, guard copy) mislead operators | Low | High | §4.4 enumerates the three sites; the decision-arm guard reword (`:43`) is tested (T10 pin); comment sites updated with their code |

---

## 11. Rollback thinking (FE-only revert scope)

- Every commit this phase produces is FE-only **except the joint lane-fold commit**, which carries `pkg/models/group_lane.go` — reverting THAT commit reverts both halves atomically (one commit, one revert; never revert one half alone).
- Post-revert safety property: image-gen-lane groups created while the feature was live become UNKNOWN rows under the old 2-lane fold (image-gen members = no lane evidence → empty surviving set → unknown → **text** child) — no row ever goes invisible; the operator can delete or repair from the text group surface. This is the same no-invisible-rows invariant the fold already guarantees (`groupLane.ts:181-184`).
- No DB migration, no config schema, no API shape, no credential/store writes — revert is `git revert` + rebuild `static/` + redeploy the UI-embedding image. The top-level tab bar returns to 11 tabs; wrap flow returns to `setActiveTab('group_models')`.
- Rollback anchor: the phase's commits sit on `feature/imggen-groups-text-subtabs` behind `latest`/`master` promotion gates — the pre-phase image is the standing rollback.

---

## 12. Success criteria (checkable)

| # | Criterion | Measure | Threshold |
|---|---|---|---|
| S1 | Bar topology | DOM walk in tabs suite | exactly 10 top-level buttons; `data-tab-id="group_models"` absent; `TabType` has no `'group_models'` |
| S2 | Text container | testid assertions | default child = models (`Available Models` visible); group child lists chat + unknown groups only; `text-container-*` namespace |
| S3 | Wrap flow | testid-level W1 case | wrap click on a chat model row → Text tab, group child mounted, add-form pre-filled, signal consumed exactly once |
| S4 | ImgGen container | testid + fixture | default child = imggen models list; group child lists ONLY `split['image-gen']`; mixed fixture's unknown row NOT in the image-gen child |
| S5 | Decision byte-stability | `git diff` on `DecisionModelsContainer.test.tsx` + suite run | diff EMPTY, suite green |
| S6 | New suites anti-vacuous | gate log | every added `it(` shows literal `--- PASS`; deleted tabs-test cases listed |
| S7 | tsc verdict | `npx tsc --noEmit` vs baseline | frozen 18/9 file-set OR documented deliberate delta with updated baseline + header, same commit |
| S8 | Copy freeze | string diff over chat AND decision arms + both suites | ZERO chat-string deltas (chat arms byte-identical, `:42` untouched); exactly ONE decision-string delta (guard reword `:43`); chat suites green-unedited |
| S9 | Same-commit fold twin | commit file-list | the commit touching `groupLane.ts`/`types.ts` `'image-gen'` arms also touches `pkg/models/group_lane.go` |
| S10 | Build + embed | `npm run build` + `make build-frontend` path | build succeeds; rebuilt `static/` committed; served UI shows the new topology |

---

## 13. Not verified (plan-phase honesty)

- `tsc`, `vitest`, `vite` were NOT run — S7's expected direction (a frozen SettingsPage error dissolving with the deleted branch) is a hypothesis from reading the baseline header, not a measurement.
- The five remaining frozen SettingsPage tsc errors are not individually located (the baseline records counts, not lines) — T14 measures, never assumes.
- P1's actual server-fold shape, validation-gate behavior, and imggen serving semantics (IA-1..IA-4) — placeholders stand until reconciled in plan-overview.
- Final image-gen lane copy literals (§3.2) are proposals pending IA-3.
- All line anchors verified at `5cf1ecdc` on 2026-11-20; later commits on this branch will drift them — re-verify before quoting in review docs.
