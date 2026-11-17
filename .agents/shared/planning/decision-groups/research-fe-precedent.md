# Research — Decision Models container tab, decision groups, FE precedent

**Commission:** decision-groups (container tab with vertical child nav `models` | `group`).
**Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-decision-groups`
**Branch/base:** `feature/decision-groups` @ `24c51ef` (== `origin/latest`).
**Method:** read-only code + planning-doc reads. No builds, no tests, no FE run, no commits.

Citation convention: line numbers marked **[wt]** were read directly in the worktree at `24c51ef`.
When a line number came from a grep that resolved against the main checkout (`master`), it is marked
**[grep-main]** and must be re-verified before being quoted in a plan (two FE files already differ by
±1 line between `master` and `latest`, e.g. `DecisionModelForm.tsx` PROVIDER_OPTIONS is `:29` in the
worktree, `:30` in main).

---

## A. FE inventory

### A1. The Decision tab today

#### `pkg/ui/frontend/src/components/config/DecisionModelsTab.tsx` (541 lines) [wt]

**Props** (`:8-25`, interface `DecisionModelsTabProps`), all supplied by `SettingsPage`:

| Prop | Type | Line |
|---|---|---|
| `models` | `Model[]` | :13 — pre-filtered server-side to `kind:'decision'`; the tab never re-filters |
| `onAddModel` | `(Omit<Model,'id'> & {id:string}) => Promise<void>` | :14 |
| `onUpdateModel` | `(id, Partial<Model>) => Promise<void>` | :15 |
| `onDeleteModel` | `(id) => Promise<void>` | :16 |
| `onToggleModel` | `(model) => Promise<void>` | :17 |
| `setStatus` | toast sink `{type,message,restartRequired?}` | :18 |
| `onNavigateToCredentials?` | `() => void` | :19 |
| `fetchError?` / `onRetry?` | error banner plumbing | :23-24 |

**Hooks** — only local Preact state, no data fetching:
`useState` × 7 at `:87-97`: `showForm`, `formMode ('add'|'edit')`, `modelToEdit`, `modelToDelete`,
`deleting`, `testingIds: string[]`, `testResults: Record<string, DecisionTestResult>`.

**Module-level imports** (`:1-6`): `types` (Model, DecisionModel, DecisionTestResult), `escapeHtml`,
`testDecisionModel` from `hooks/useApi` — this is the one non-prop coupling: the per-row TEST button
calls the API module directly (`handleTest`, `:187-215`, `testDecisionModel(model.id)` at `:197`).

**Rendered structure** (`:217-541`):
- root `<div class="space-y-4">` (`:218`);
- `showForm === false` branch (`:219`):
  - header row `flex justify-between items-center mb-2` with `<h3 class="text-white font-medium">Decision Models</h3>` (`:221-222`) and "Add Decision Model" CTA (`:223-232`, `bg-blue-600 hover:bg-blue-500 …`);
  - `role="alert"` fetch-error banner, `data-testid="decision-fetch-error"`, conditional Retry (`:239-256`);
  - `data-testid="decision-models-list"` (`:260`) → empty state `data-testid="decision-empty-state"` (`:262+`) or one row per model: enable toggle, 🎯 provider badge (`:302-310`), id (`:312-314`), `internal_model` chip (`:315-322`), `CREDENTIALS: n` line with zero-credential warning (`:325-332`), `internal_base_url` (`:333-337`), per-row TEST result panel `role="status"|"alert"` + `data-testid="decision-test-result-<id>"` (`:342-…`);
  - delete-confirm modal + `<DecisionModelForm>` mount in the form branch.
- Toast copy constant `TOAST` at `:30-35` ("Decision model added/updated/deleted/toggled successfully").
- Exported test/probe helpers: `classifyDecisionTestError` (`:60-62`) and `formatProbability` (`:65-67`) delegating to the shared `probeErrorClasses.ts` module (`:6`, `:54-59`).

**Nestable?** Structurally yes, with caveats:
1. It takes **no router/route/context** dependency — every input is a prop; it can be mounted inside a container unchanged.
2. It renders **its own page-level chrome**: `h3 "Decision Models"` + "Add Decision Model" CTA at the top (`:221-232`). Inside a container tab whose label already says "Decision Models", that is duplicated heading text (cosmetic, not functional).
3. It mounts its own form in place (`showForm` toggles the whole tab body), so a container that also renders a sibling tab pane must mount/unmount, not hide — the form's local state must not survive a sibling switch by accident.
4. The API import (`testDecisionModel`) is module-level: any ancestor test of a container must stub `../../hooks/useApi` or `fetch`.

**Its tests:**
- `config/__tests__/DecisionModelsTab.test.tsx` (219 lines) — "list, empty state, delete modal"; sub-describes `empty state` (:34), `populated state` (:62), `delete confirm modal` (:110), `A11Y` (:177), `server-side pre-filter trust` (:199).
- `config/__tests__/DecisionModelsTab.decisionTest.test.tsx` — per-row Test button; sub-describes `wire shape` (:89, asserts `POST /fe/api/decisions/test` and the absence of `safety_identifier`), `success rendering` (:135), `error classification` (:196), `in-flight state` (:268).

#### `pkg/ui/frontend/src/components/config/DecisionModelForm.tsx` (528 lines) [wt]

- Hard-coded stamping constants: `HARDCODED_KIND='decision'` / `HARDCODED_INTERNAL=true` (`:21-22`).
- **The FE decision-provider constant** — `PROVIDER_OPTIONS` at **`:29`**:
  `const PROVIDER_OPTIONS = ['openai', 'typesafe', 'openrouter'] as const;`
  (`:24-28` explains it is deliberately hand-listed, not derived from the BE list; `:15-20` says an
  override to identity A would be a single edit here.)
- `DEFAULT_BASE_URLS` `:35-39` (openai `https://api.openai.com`, typesafe `https://api.typesafe.ai`, openrouter `https://openrouter.ai/api`).
- Wire payload `DecisionModelPayload` `:45-55`: `{id,name,kind:'decision',internal:true,internal_provider,internal_model,internal_base_url,credentials:CredentialRef[],enabled}`.
- Props `:57-68`: `mode`, `initialData?: DecisionModel`, `onSave`, `onCancel`, `onStatus`, `onNavigateToCredentials?`.
- `makeInitialState` `:89-118` (hydrate from `initialData`, per-provider base URL default at `:100`/`:114`).
- `filterCredentialsByProvider` `:124-129` (credentials dropdown shows only the selected provider's creds).
- Validation mirrors at `:215-…`: name required `:217-219`, `internal_model` required `:220-222`, id required in add `:223-225`, provider in `PROVIDER_OPTIONS` `:226-228`, ≥1 credential `:229-230`, primary row must have a credential id `:231-234`, provider-match resolution guarded by `loadingCredentials` `:242-249`.
- Test: `config/__tests__/DecisionModelForm.test.tsx` — base-URL stamping (:62), provider dropdown contents `['openai','typesafe','openrouter']` (:271-286), client validation (:290), edit mode (:370), A11Y (:400).

---

### A2. The text-group UI to mirror

#### `GroupModelsTab.tsx` (360 lines) [wt]

Props `:6-30`: `models: Model[]` (pre-filtered `kind:'group'`), **`chatModels: Model[]`** (`:14`, used for member + shadow pickers), the same add/update/delete/toggle quartet, `setStatus`, `fetchError?`/`onRetry?`, plus the one-click-wrap signal `wrapPreloadModel?: Model|null` + `onWrapConsumed?` (`:28-29`).
State `:55-65`: `showForm`, `formMode`, `modelToEdit`, `modelToDelete`, `deleting`, `wrapMember`.
Lifecycle: wrap pre-fill effect `:70-79`; add `:81-87`; edit `:89-96`; save `:98-132` (add → `onAddModel(payload)`; edit → `onUpdateModel(id, {name,enabled,kind,internal,members,shadow_model_id,fallback_chain,credentials})` at `:115-124`, with the comment that a sparse PUT would skip the BE group-validation arm); delete `:134-149` (409 = member of another group surfaces via the house error body).
Render `:151-…`: header `h3 "Group Models"` + "Add Group Model" (`:155-167`), `role="alert"` banner `data-testid="group-fetch-error"` (`:172-189`), `data-testid="group-models-list"` (`:193`), empty state `data-testid="group-empty-state"` (`:194-204`), rows `data-testid="group-model-row"` + `data-model-id` (`:210-215`) with 🔀 badge, member chips, shadow-claim line.
Toast copy `:35-40`.

#### `GroupModelForm.tsx` (450 lines) [wt]

- `HARDCODED_KIND='group'` / `HARDCODED_INTERNAL=true` at `:16-17`; the comment `:5-15` documents the stamping discipline (mirrors `DecisionModelForm`).
- `GroupModelPayload` `:24-34`: `{id,name,kind:'group',internal:true,members:GroupMember[],shadow_model_id:string,enabled:boolean,fallback_chain:[],credentials:[]}`.
- Props `:36-60`: `mode`, `initialData?: GroupModel`, `wrapTarget?: Model|null`, **`chatModels: Model[]`** (`:53`, "Chat-kind models only … Feeds BOTH the member picker and the shadow-target picker — G3/G8 only ever accept chat-kind targets"), `groups: GroupModel[]` (`:56`, needed for the client-side G8 dual-claim scan), `onSave`, `onCancel`, `onStatus`.
- `FormFields` `:64-70`: `{id,name,members,shadow_model_id,enabled}` — the hard-coded fields are constants, never state.
- `makeInitialState` `:77-114`: `backup_only` round-trips verbatim `:85-89`; wrap pre-fill shape `${id}[${id}]` + single member + self shadow claim `:99-105`.
- Re-hydrate effect on `initialData`/`wrapTarget` change `:139-…`; error state `:132-137` with `aria-describedby`/`aria-invalid` posture.
- Client-side G-rule mirrors live in `handleSubmit` (the design doc points at the error-collection block, cf. `group-backup-members/design.md:41-45`) — the authoritative rules are BE-side (`pkg/models/group.go`).

#### `GroupMemberEditor.tsx` (194 lines) [wt]

- `MAX_GROUP_MEMBERS = 16` at `:6` ("mirrors the Go `models.MaxGroupMembers` constant … Keep the two in lock-step").
- Props `:8-16`: `members: GroupMember[]`, **`chatModels: Model[]`**, `onChange`.
- **Defensive chat-kind filter at `:40`**: `const pickerModels = chatModels.filter((m) => !m.kind || m.kind === 'chat');` — i.e. the *editor itself* re-imposes the chat-only member rule.
- Row shape `:90-…`: `data-testid="group-member-row"`, `<select data-testid="group-member-model-select">` with placeholder "Select a chat model…" (`:105`), weight `<input>` (`setMemberWeight` `:51-54`, invalid → `0` so submit-time `weight>0` catches it), `Backup only` tick (`toggleMemberBackupOnly` `:61-74`, drops the key entirely when false to match Go `omitempty`), remove button.
- Duplicate model-id **warn only** (Q10) `:80-85`, rendering the aggregate hint.
- Test: `config/__tests__/GroupMemberEditor.test.tsx` (223 lines) — picker/weight/MIN-MAX/dup-warn; `:55-71` pins chat-only options (image-gen/decision/group excluded).

**Which parts are chat-specific today (must be parameterized or duplicated for decision groups):**
1. `GroupModelsTab.tsx:14` + `:634` in `SettingsPage` — `chatModels` prop; the parent computes it at `SettingsPage.tsx:293-295` as `models.filter(m => m.kind !== 'image-gen' && m.kind !== 'decision' && m.kind !== 'group')`.
2. `GroupModelForm.tsx:49-53` — comment + prop: members **and** shadow targets are chat-kind only.
3. `GroupMemberEditor.tsx:10-13` and `:40` — hard chat-kind filter inside the editor; placeholder text "Select a chat model…" (`:105`).
4. `GroupMemberEditor.tsx:115-…` provider badge reads `internal_provider` generically (not chat-specific).
5. Copy in `GroupModelsTab` empty state `:203`: "load-balance chat traffic across multiple member models".
6. `GroupModelsTab.tsx:32-40` / `GroupModelForm.tsx:5-17` comments reference chat/LB semantics only; there is no kind discriminator on a group row itself (`kind:'group'` is the group's own kind, membership kind is implicit chat).

---

### A3. Tab primitives / design language

#### Top-level tab menu — `pkg/ui/frontend/src/components/SettingsPage.tsx` (750 lines) [wt]

- `TabType` union at `:66`: `'proxy' | 'models' | 'imggen_models' | 'decision_models' | 'group_models' | 'credentials' | 'loop_detection' | 'tool_repair' | 'tokens' | 'usage' | 'mcp_servers'` (11 values).
- State: `const [activeTab, setActiveTab] = useState<TabType>('proxy');` `:103`. Pure `useState` — **no router involvement** for tabs.
- Markup `:380-521`:
  - outer `div.bg-gray-800.border-b.border-gray-700.px-6` (`:381`), inner `div.flex.overflow-x-auto` (`:382`);
  - each tab is a raw `<button type="button">` (`:386-395`, with `:383-385` explaining `type="button"` is deliberate so Enter in a form cannot submit);
  - class idiom, verbatim (`:388-391` / `:428-431`):
    `` `px-6 py-3 font-medium transition-colors flex items-center gap-1.5 whitespace-nowrap ${activeTab === 'decision_models' ? 'text-blue-400 border-b-2 border-blue-400' : 'text-gray-400 hover:text-white'}` ``
    (the `flex items-center gap-1.5` variant is used only when the tab has an emoji/glyph);
  - **no ARIA tab semantics**: no `role="tablist"`, no `role="tab"`, no `aria-selected`, no `role="tabpanel"` anywhere in the FE (grep across `src` → zero matches). Selection is conveyed only by class + the conditional content branch (`:526`, `:607`, `:631`, …).
  - `data-tab-id` attributes exist only on `imggen_models` (`:416`), `decision_models` (`:433`) and `group_models` (`:514`); the group tab additionally sets `aria-label="Group Models"` (`:515`) because its text content includes the 🔀 emoji; emoji spans are wrapped `aria-hidden="true"` (`:418`, `:435`, `:517`).
- Content branch pattern: `{activeTab === 'x' && (<Component … />)}` inside `div.flex-1.overflow-y-auto.p-6 > div.max-w-4xl.mx-auto` (`:523-525`). Decision branch `:607-619`, group branch `:631-645`.
- Handlers wired: `handleToggleDecisionModel`, `handleToggleGroupModel`, `setStatusWrapper` (`:128-132`, converts `setStatus` to a toast).

#### `components/__tests__/SettingsPage.tabs.test.tsx` (439 lines) [wt] — what it pins

- Fixture builder `makeProps()` `:31-108` (all callbacks `vi.fn()`); global `fetch` stub `:12-23` returning `[]` for `/credentials` and `/models`.
- Global selector used everywhere: **`container.querySelectorAll('div.flex.overflow-x-auto > button')`** (`:126-128`, `:153-155`, `:192-193`, `:233-234`, `:259`, `:282-284`, `:316`).
- Hard assertions that a container restructure will break:
  - `expect(tabs.length).toBe(11)` and `expect(groupIdx).toBe(10)` — `:285`, `:287`;
  - Decision→Credentials adjacency preserved — `:290-291` (also `:158-159`);
  - ImgGen→Decision adjacency — `:130-135`;
  - `type="button"` on every tab — `:257-265`;
  - emoji `span[aria-hidden="true"]` present — `:246-255`, `:327-332`;
  - `aria-label="Group Models"` — `:278`;
  - click → content mount / click-away → content unmount by empty-state copy (`:162-203`, `:294-325`).
- It also contains the sticky-toggle suite (`:335-…`) with a `label`-text based finder — unaffected by tab work.

**Consequence for the commission:** if the new "Decision Models" container keeps the SAME
`data-tab-id="decision_models"` button and the same 11-button bar, all of the above stays green.
Adding a 12th top-level tab, or moving the group tab into the container, changes `:285`/`:287` and must
be updated in the same commit.

#### Vertical nav / nested sub-tab precedent: **NO**

- `role="tablist" | "tab" | "tabpanel" | aria-selected` — **zero matches** in `pkg/ui/frontend/src` (`aria-current` also zero).
- `subtab|sub-tab|sidebar|vertical nav|nav-item` — zero matches except an unrelated comment in `RequestDetail.tsx:137` about "vertical footprint".
- The only `border-r` + `flex-col` layout in the app is the three-pane dashboard column: `RequestList.tsx:51` — `class="col-span-3 bg-gray-900 border-r border-gray-700 flex flex-col min-h-0"`. It is a grid column, **not** a nav.
- Therefore: the container + vertical child nav would be the **first** nested-tab / vertical nav in the FE. There is no precedent to mirror; the closest visual idiom is (a) the top tab bar's active/inactive class pair, and (b) the card/row chrome `bg-gray-700/80 rounded-md border border-gray-600/50` used by list rows (`GroupModelsTab.tsx:214`, `DecisionModelsTab` rows).

**Styling idiom to reuse for a vertical nav (all from the top bar):** base `px-6 py-3 font-medium transition-colors whitespace-nowrap`; active `text-blue-400` (+ `border-b-2 border-blue-400` horizontally — a vertical nav would translate this to a left border, e.g. `border-l-2 border-blue-400`, which has no in-repo precedent); inactive `text-gray-400 hover:text-white`. Panels sit in `bg-gray-800`/`bg-gray-900` with `border-gray-700` dividers; list rows use `bg-gray-700/80 border-gray-600/50`.

---

### A4. Data layer

#### Hooks — there are **no** `useDecisionModels.ts` / `useGroupModels.ts` files

`pkg/ui/frontend/src/hooks/` contains only `index.ts`, `useApi.ts`, `useEvents.ts` (+ `__tests__/`). All
model-list hooks are exported from the single `useApi.ts` (1291 lines) and re-exported by `hooks/index.ts`.

| Hook | Definition | Fetch URL | Cache key |
|---|---|---|---|
| `useDecisionModels()` | `useApi.ts:532-613` [wt] | `${API_BASE}/models?kind=decision` (`:545`) | `'decision-models'` (`:544`) |
| `useGroupModels()` | `useApi.ts:636-713` [wt] | `${API_BASE}/models?kind=group` (`:649`) | `'group-models'` (`:648`) |
| `useModels()` | `useApi.ts:~380-507` [grep-main] | `${API_BASE}/models` | `'models'` |
| `useImgGenModels()` | above | `?kind=image-gen` | `'imggen-models'` |

- Both decision/group hooks: `getOrFetch(..., 15000)` TTL, `AbortController` cleanup (`:606-610`, `:706-710`), `error: string|null` surfaced to the tab banner.
- Mutations: POST `/models`, PUT `/models/{id}` (merged body — hook-level client merge at `:579-585`/`:681-687`), DELETE `/models/{id}`; each mutation deletes **all four** keys explicitly — `'decision-models'`, `'models'`, `'imggen-models'`, `'group-models'` (`:572-575`, `:588-591`, `:599-602`; mirrored `:674-677`, `:689-692`, `:699-702`), then refetches. Comments state `deleteByPrefix` is forbidden (misses in-flight promise entries; none of the keys start with `models`).
- The test button: `export async function testDecisionModel(modelId)` at `useApi.ts:~1274` [grep-main] → `apiFetch('/decisions/test', …)`.
- Credentials/providers: `getCredentials()` used by forms; `useProviders()` at `useApi.ts:1023-1054` [grep-main] hits `/fe/api/providers` (BE chat provider catalog) — **the decision provider list does NOT come from there**.

#### `pkg/ui/frontend/src/types.ts` (646 lines) [wt]

- `Model` `:107-154`: `id, name, enabled, fallback_chain, truncate_params?, kind?: 'chat'|'image-gen'|'decision'|'group'` (`:122`), `internal_provider?: InternalProvider` (`:127`, union at `:105`), `internal?`, `credentials?: CredentialRef[]`, `internal_api_key?`, `internal_base_url?`, `internal_model?`, `secondary_upstream_model?`, `release_stream_chunk_deadline?`, `peak_hour_*?`, **`members?: GroupMember[]`** (`:152`), **`shadow_model_id?: string`** (`:153`).
- `DecisionModel = Model & { kind: 'decision' }` `:166`. `ImgGenModel` `:160`. `GroupModel = Model & { kind: 'group' }` `:192`.
- **`GroupMember`** `:182-186`:
  ```ts
  export interface GroupMember {
    model_id: string;
    weight: number;
    backup_only?: boolean;
  }
  ```
  Doc block `:168-181`: `model_id` references `models.id` of an existing **chat-kind** model (G3); no `enabled` and no per-member credential fields — the member is a full model row with its own credentials; `backup_only` absent ⇒ default tier (D7).
- `DecisionTestResult` `:591-603`: `{success, model?, provider?, credential_id?, answers?, usage?, status, relay_status?, upstream_status?, error?, reason?}`.
- `CredentialRef` `:21-25`: `{credential_id, weight, position}`.

#### The provider-vocabulary constants (POK `decision-provider-vocabulary`)

| Vocabulary | File:line | Verbatim |
|---|---|---|
| **FE decision-provider constant** | `pkg/ui/frontend/src/components/config/DecisionModelForm.tsx:29` [wt] | `const PROVIDER_OPTIONS = ['openai', 'typesafe', 'openrouter'] as const;` |
| FE image-gen provider constant (sibling pattern) | `pkg/ui/frontend/src/components/config/ImgGenModelForm.tsx:9` [wt] | `const PROVIDER_OPTIONS = ['minimax', '9router'] as const;` |
| BE decision gate #1 (JSON-file/kind rules) | `pkg/models/config.go:293-299` [wt] | `switch internalProvider { case "openai", "typesafe", "openrouter": … default: return fmt.Errorf("model %s: kind %q requires the primary credential's provider to be %q, %q or %q (got %q)", …) }` |
| BE decision gate #2 (POST `/fe/api/models` inline mirror) | `pkg/ui/server.go:1171-1178` [wt] | `if primaryProvider != "" && primaryProvider != "openai" && primaryProvider != "typesafe" && primaryProvider != "openrouter" { … 400 … }` |
| BE decision gate #3 (PUT `/fe/api/models/{id}` inline mirror) | `pkg/ui/server.go:~1654-1663` [grep-main] | same predicate / same message |
| **Credential vocabulary (separate map)** | `pkg/models/credential.go:15-60` [wt] | `var validProviders = map[string]bool{ "openai": true, "anthropic": true, "gemini": true, "zhipu": true, "azure": true, "zai": true, "minimax": true, "9router": true, "grok": true, "deepseek": true, "opencode": true, "opencode-go": true, "lightllm": true, "openrouter": true, "typesafe": true, }` |

Related BE rule sets the container/group work will touch:
- `validateDecisionRules` D1–D8 at `pkg/models/config.go:277-320` [wt] (D4 = provider gate `:287-300`; D5 fallback_chain `:301-304`; D6 secondary_upstream_model `:305-308`; D7 peak_hour `:309-312`; D8 release_stream_chunk_deadline `:313-316`; former D9 retired `:317-318`).
- Group rules: `validateGroupRules` (`G1`, `G6`, `G7`, self-half of `G8`) at `pkg/models/group.go:60` [grep-main]; collection rules `ValidateGroupMembers` (`G2` ≥1 + cap 16, `G3` member validity/weight, `G4` dup, `G5` no group-as-member, `G8` claim uniqueness) at `pkg/models/group.go:120` [grep-main]; doc block `:49-59`.
- `GET /fe/api/models?kind=` switch: `pkg/ui/server.go:~748-789` [grep-main]; chat arm must exclude every non-chat kind (`research-codebase.md:352` warns the leak precedent recurs).
- `/v1/models` exclusion predicate: `pkg/proxy/handler.go:~257-261` [grep-main].
- `kind` constants: `pkg/models/config.go:93-118` [grep-main] (`KindChat`, `KindImageGen`, `KindDecision`; `KindGroup` added).

---

### A5. Test + typecheck conventions

**Runner.** `pkg/ui/frontend/package.json:10-11` [wt]: `"test": "vitest run"`, `"test:watch": "vitest"`.
`vitest.config.ts:7-15` [wt]: `plugins: [preact()]`, `test.environment: 'happy-dom'`, `globals: false` (every test imports `describe/it/expect/vi` from `vitest`), `include: ['src/**/__tests__/**/*.test.{ts,tsx}']`, `css: false`.
Scripts: `dev` (vite :5172), `build` (`vite build`) — **no `tsc`/`typecheck` script**; the typecheck gate is the vitest test below.

**Component-test idiom (2 examples):**
1. Pure presentational, prop-driven, no module mocks — `config/__tests__/GroupMemberEditor.test.tsx:1-4` imports, `:6-14` `makeModel()` fixture factory, `:40-53` renders and asserts on `data-testid` selectors + `fireEvent.change` + `onChange` spy.
2. Module-level API usage → `vi.mock` of the hooks module — `config/__tests__/DecisionModelForm.test.tsx:18-29` (`getCredentials: vi.fn(async () => [...])`), and for the tab/test-button path `DecisionModelsTab.decisionTest.test.tsx:88-105` asserts the exact `fetch` URL `/fe/api/decisions/test`.
Common hygiene: `beforeEach(() => { document.body.innerHTML = ''; })`, `afterEach(() => { cleanup(); vi.clearAllMocks(); })` (`GroupMemberEditor.test.tsx:32-38`); `SettingsPage` tests stub `globalThis.fetch` globally (`SettingsPage.tabs.test.tsx:11-27`).

**tsc baseline.** `pkg/ui/frontend/tsc-baseline.txt` (31 lines) [wt]:
- Format: `#` comment preamble, then `# Per-file error count:` followed by lines `"<count> <relative path>"` (`:19-27`), then `# Aggregate (machine-readable footer):` with `TOTAL_ERRORS=19` and `TOTAL_FILES=9` (`:30-31`).
- Current content: `6 src/components/SettingsPage.tsx`, `6 src/components/config/ModelsTab.tsx`, `1` each for `EventLog.tsx`, `CredentialsTab.tsx`, `ModelForm.tsx`, `MCPServersTab.tsx`, `ModelUsageChart.tsx`, `UsageChart.tsx`, `helpers.ts`.
- `src/utils/__tests__/tsc-baseline.test.ts` [wt]: `@ts-nocheck` (`:28`) because `node:*` builtins are outside the app tsconfig; `parseBaseline` (`:46-65`) requires the `TOTAL_ERRORS=`/`TOTAL_FILES=` footer and parses `^(\\d+)\\s+(\\S+\\.tsx?)$`; `runTSC` (`:67-79`) runs `npx --no-install tsc --noEmit` with `cwd = pkg/ui/frontend`, 60 s timeout; asserts (a) baseline internal consistency (sum of per-file == total, `:121-127`), (b) actual total errors == baseline (`:139-142`), (c) actual total files == baseline (`:144-147`), (d) per-file counts match (`:149-…`). Because it is **per-file**, touching `SettingsPage.tsx` (already at 6) or `ModelsTab.tsx` (6) must not add a single new error there; new files must produce **zero** errors or the file set changes.

**vite → static (zero-mod proof).** `vite.config.ts:7-10` [wt]: `build.outDir: '../static'`, `emptyOutDir: true`; `base: '/ui/'`. So `npm run build` writes `pkg/ui/static/` — the **tracked embedded-UI area** (FE assets + `pkg/ui/embed_regression_test.go` reference them; only `frontend/dist/` is gitignored). Zero-source-change proof = `sha256sum` of `pkg/ui/static` before/after a rebuild must be byte-identical (content-hashed filenames change per build; the mechanism is the claim). Any FE source change carries its regenerated assets in the SAME commit.

**Every FE test file under `components/config/__tests__/` (12 files):**

| File | Subject (from its `describe`) |
|---|---|
| `CredentialsTab.test.tsx` | `CredentialsTab & ModelForm 9router integration (B1)` |
| `DecisionModelForm.test.tsx` | decision form: payload, per-provider base URL, provider dropdown, client validation, edit mode, A11Y |
| `DecisionModelsTab.decisionTest.test.tsx` | decision per-row Test button: wire shape, success rendering, error classification, in-flight state |
| `DecisionModelsTab.test.tsx` | decision list, empty state, delete modal, A11Y, server-side pre-filter trust |
| `GroupMemberEditor.test.tsx` | member picker (chat-kind only) + weight + MIN/MAX + dup-warn |
| `GroupModelForm.test.tsx` | group form payload stamping + client G-rule mirror; second block: `backup_only` round-trip + D2 guard (`:292`) |
| `GroupModelsTab.test.tsx` | group list/CRUD shell |
| `ImgGenModelForm.test.tsx` | image-gen form: validation C-10..C-13, edit-mode credential race, valid submit, A11Y, 9router |
| `ImgGenModelsTab.imgGenTest.test.tsx` | image-gen per-row Test button: wire shape, success, error classification, in-flight, cost hint |
| `ImgGenModelsTab.test.tsx` | image-gen list/empty/delete modal/A11Y/chat-filter integration |
| `ModelsTab.chatFilter.test.tsx` | chat-tab kind filter (C-16); second block: copy-model action (`:185`) |
| `probeErrorClasses.test.ts` | shared probe-error vocabulary (status map, image-gen overlay, message cap) |

Sibling FE suites that constrain this commission: `components/__tests__/SettingsPage.tabs.test.tsx`;
`hooks/__tests__/useDecisionModels.test.ts` (`:52-61` pins `kind=decision`), `useGroupModels.test.ts`
(same for `kind=group`), `hooks/__tests__/useApiCredentials.test.ts` (key-invalidation contract);
`utils/__tests__/tsc-baseline.test.ts`.

---

## B. House conventions extracted

### B1. Planning-dir document structure

Two generations, both live:

| Shape | Files | Examples |
|---|---|---|
| **Full commission** (kind introduction) | `plan.md` (13–14 sections, numbered §1..§14, + Appendices A "Files Changed", B "Amendment Log", C "Round 2 Pivot Log"), `decisions.md` (mirrors §13 open questions), `research-codebase.md` (file:line anchor inventory), `architecture-recommendation.md` (contractor review + §10 amendments), optionally `research-web.md` | `decision-model-type/`, `load-balance-groups/` |
| **Phased commission** (refactor/subsystem) | `plan-overview.md` (goal, phases table, risks), `phaseN-plan.md` (one per phase), `decisions.md` | `memory-traps-fix/`, `frontend-api-optimization/`, `token-ultimate-model/`, `secondary-upstream-model/`, `mcp-endpoint-split/` |
| **Design note** (single-scope change) | one `design.md` (decisions D1..Dn + "Seams touched" table + test plan + commit plan), or `design-note.md`/`technical-analysis.md`/`fe-spec.md` variants | `group-backup-members/design.md`, `opencode-go-provider/design-note.md`, `sticky-last-success-ordering/design.md` |
| **Research-only** | `report.md` / `findings.md` | `opencode-free-tier-research/report.md`, `openrouter-decision-provider/findings.md` |

Cross-doc conventions observed:
- Front matter blockquote: commission provenance, `Base:` SHA, "PLAN-ONLY / no source changes / no commits / no builds" statement, branch/lock policy reminder (`group-backup-members/design.md:3-7`, `load-balance-groups/plan.md:3-5`, `decision-model-type/plan.md:3-5`).
- `plan.md` opens with §1 Executive Summary split into **Goal / Goals (in) / Non-goals (out)**, then §2 "Grounding — How Existing Machinery Works" as a file:line anchor list, then design sections, then §13 "Open Questions for the User" (Q-table with a "Recommended default" column that is authoritative), then appendices.
- `decisions.md` mirrors §13 exactly, same numbering, with a fixed 4-field per-item format (`:18-24` in both logs): **Question / Recommended default / Rationale / What changes if overridden**, where override entries are priced ("≈ lines, tests, plan sections").
- `group-backup-members/design.md` adds a **"Seams touched" table** (`## Seams touched`, `:133-152`) with columns `Seam | File:line | What changes` — the most directly reusable artifact for this commission.
- Design docs also carry `## Test plan` (numbered, one-to-one mapped to named tests, `design.md:190-239`), `## Branching / commit plan` (conventional-commit order, docs-first, `:241-275`), `## Risks / known unknowns` (`:277-304`), `## Divergences from D1–D7` (`:306-315`).
- FE-asset discipline is written into the commit plan: "any FE source change carries its regenerated assets in ONE commit per embed discipline" (`design.md:272-275`).

### B2. Decision-ID conventions

- **`D1..Dn`** — *implementation decisions of one design note*, each with an explicit seam (`group-backup-members/design.md:9-131` D1–D7, then `:319-…` D8 added after the fact under a banner explaining *why it was added* and quoting the user requirement). Numbering is **never renumbered**; later additions append (D8) and superseded items are kept as history.
- **`G1..Gn`** — *field/collection rules of the group kind*, one letter per rule family, mirrored across three BE sites and locked by `pkg/ui/kind_rules_parity_test.go` (`load-balance-groups/plan.md:288-302`; rule text `pkg/models/group.go`).
- **`Q1..Qn`** — *open questions to the user*, stable numbering, never renumbered; superseded questions stay in the log under a `> **SUPERSEDED …**` banner (e.g. `load-balance-groups/decisions.md:267`, `:342`; `decision-model-type/decisions.md:9` Q9 marked superseded-by-Q14).
- **`Am1..Amn`** — *amendments* from a review round, folded in place in `plan.md` and pointed at from `decisions.md` ("per Architect Round 1, Am18 — `plan.md` Appendix B"); review rounds are labelled (Architect Round 1 / Review Round 1 / Approver Polish Round 3 / Round 2 Membership Pivot).
- **`USER-DECIDED (locked)`** — items settled by the user mid-flight; they keep the full field treatment but are not "open" (`load-balance-groups/decisions.md:14`).
- **Ruling style** — user-authoritative doctrine is quoted verbatim in ALL-CAPS tags: `(USER RULING D3, user-designed, FINAL)` (`pkg/skipcache/skipcache.go:1-2`), `(wi-7769a812 part 3 — P0 fix, USER-AUTHORITATIVE)` (`:13`).
- **`R1`/`R2`/`R6`** — *risks* in the plan's risk table with likelihood/impact columns (`load-balance-groups/plan.md:493`); **`AC`** — acceptance criteria; **`C-xx`/`FE-xx`/`BE-Dx`/`T2.4.5`** — per-commission finding/task IDs (`imggen-models/fe-spec.md`).

### B3. The v193 skip-cache doctrine — exact wording and location

Canonical form, verbatim, from `pkg/skipcache/skipcache.go:13-16` [wt]:

```go
// ORDERING-NOT-FILTERING (wi-7769a812 part 3 — P0 fix, USER-AUTHORITATIVE):
//
//	The cache is an optimization, not the truth: it may reorder
//	candidates, never exclude them.
```

The surrounding block (`:18-26`) is the operative restatement:

```go
// Selection consults the cache ONLY to ORDER: unmarked candidates
// first (normal pool/weight/sticky order), then marked ones —
// soonest-expiring first (the most likely-recovered upstream). A
// marked member is a DEMOTED candidate, never an invisible one: a
// request may only fail after every member×credential has genuinely
// failed THIS request, so execution proceeds through the demoted
// list when the unmarked head cannot serve. A WIN clears the
// winner's own live marks (Cache.Clear) — a proven-healthy identity
// must not stay benched.
```

Other places the same doctrine is recorded (for citation in a plan):
- Commit `718f636` message: `docs: T2 header — skip cache orders members, never filters` (quoted in `.agents/tester/RESULTS/2026-11-14-agentic-group-hidden-member-full-gates.md:127`).
- `.agents/tester/RESULTS/2026-11-14-agentic-group-hidden-member-repro-red.md:17`: "Skip cache is ORDERING, never FILTERING — candidate set is always the full servable member×credential set; unmarked first (pool/weight order), then marked soonest-expiring-first."
- T5 red assertion text: "provider-marked member reported unservable — marks must order, never filter" (same file `:30`).
- Implementation-side law ("Fallback-engine law (v193)": marks are ordering inputs, never candidacy filters; four-phase selection sweep in `pkg/store/database/store_group_rewrite.go`) — recorded in the project blueprint page "Credential Failure Classification & Cooldown Policy" and pinned by `pkg/proxy/group_fallback_hidden_member_test.go` + `pkg/store/database/store_group_skipcache_test.go`.

### B4. The modelscache "decorator forward trap" precedent

Canonical phrasing (POK entry `modelscache-decorator-forward-trap`, domain `verify`, audience `both`), verbatim:

> "CachedModelsConfig (default ON) forwards only some ModelsManager methods; new manager methods via interface assertion no-op in prod while raw-mgr tests pass green — add a decorator forward + decorator-through test."

The v193 gate record restates it as a fix bundle item, verbatim
(`.agents/tester/RESULTS/2026-11-14-agentic-group-hidden-member-full-gates.md:125`):

> "`7983dc2` (CRITICAL-FIX bundle, 9 files: modelscache forward at `pkg/modelscache/models.go:1079` per the decorator-forward trap POK, …)"

Why it matters for a new optional interface method (e.g. any group-cooldown call reached through `modelsMgr.Engine()`): production binds `CACHE_LAYER_ENABLED` default ON to the decorator, so an optional-interface assertion against the raw manager silently no-ops in prod while raw-manager unit tests stay green. Required artifact: decorator forward + a **decorator-through** test.

### B5. Prior discussion of groups for a non-chat kind — see §C (verbatim quotes there)

### B6. Gate checklist / acceptance-criteria idiom (what the approver expects)

Sources: `.agents/tester/PACKS.md` (236 lines) [wt], `.agents/tester/QUARANTINE.md` (24 lines) [wt], plus the blueprint page "Verification & Build Harness".

- **Pack A / Pack B release-gate slices** (`PACKS.md:14-16`): A = `./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...`; B = the anchored complement, produced with `go list ./... | grep -vE '<module>/(cmd|pkg/(proxy|store|decision|ui))(/|$)'` (module-prefix anchored; a repo-relative re-run silently runs everything). Internal `-timeout=240s` inside an outer `timeout 300` for release slices; unit packs keep 110s/120s (`:8-13`).
- **Exit-code discipline:** `0=PASS, 1=FAIL, 124=TIMEOUT`; timeout dual-layer (outer `timeout` + `go test -timeout`).
- **Anti-vacuous rule:** every ADDED `func Test…` must show a literal `--- PASS: <name>` in the gate log (`PACKS.md:15-16`: "Anti-vacuous: 25 added tests … each literal `--- PASS`", "anti-vacuous **10/10 literal `--- PASS`**"). `-run` splits on `/`, so the first element must be the TOP-LEVEL test name.
- **gofmt:** zero-new-drift, not repo-wide clean (`PACKS.md:15`: "gofmt zero-new (12 changed-set flags all pre-existing @base, per-file disambiguated)"); global rule in the harness blueprint: NEW drift = HEAD flagged-line-set minus base flagged set, and after aligned-fixture sweeps run `gofmt -l` on ALL touched `_test.go`.
- **Hermetic HOME:** every full-suite run needs a quarantined `HOME` (config.json is rewritten by `config.Load()` when `APPLY_ENV_OVERRIDES=1`, which `pkg/proxy` tests set), and a config.json hash guard (`PACKS.md:15`).
- **FE gate inside the release gate:** "FE 269/269 vitest, tsc 19-error/9-file exact baseline parity, vite build green" (`PACKS.md:15`), plus isolated-boot smoke (`healthz/version/models` 200s, zero panics, clean SIGTERM, config hash unchanged).
- **QUARANTINE registry:** a table of known-failing tests with columns `Test | Pack | Date Quarantined | Reason | Retry Budget | Attempts (P/F) | Status` (`QUARANTINE.md:5-11`). Known rows relevant here: `TestStoreEngine_CloseLifecycle` (tempdir cleanup race), `pkg/mcp` SSRF octal/decimal/hex (env-deterministic), `usage` SQLITE_BUSY stress pair, `TestEngine_GetOrSelect_TOCTOU_CoolingReverify` (load-context TOCTOU), `pkg/mcp` httpbin.org redirect pair (external 403). A single-suite failure of a quarantined test is adjudicated, not "fixed" in-flight.
- **Deploy/verification addenda** live in `RESULTS/<date>-<commission>-*.md`, with a one-line `**Verdict GO**` and the deploy scope named.

---

## C. Non-chat group precedent — verdict

**Verdict: NO prior doc designs, rejects, or defers "decision groups" or "image-gen groups" as a
feature. The only prior statements are SCOPE-EXCLUSION statements from the chat-only
load-balance-groups commission. There is no recorded ruling for or against a non-chat group kind;
the commission is precedent-setting and must write the ruling itself.**

Searched: every `.agents/shared/planning/**` doc (glob over 21 planning dirs), `docs/**`,
`pkg/**` comments. No file contains a "decision group", "decision-group", "image-gen group" or
"imggen group" design section. `decision-model-type` never mentions groups (its non-goal list is
about streaming/idempotency/`/v1/systemone`, `plan.md:25-32`). `group-backup-members` inherits
chat-only without discussing it (members are chat-kind models).

Verbatim statements that DO constrain the ruling:

1. `load-balance-groups/plan.md:28` — non-goal, chat-only scope:
   > "**Image-gen and decision paths** — untouched end-to-end (routes, guards, handlers). A group misrouted to `/v1/image_generation` or `/v1/decisions` keeps today's behavior of those guards (groups are neither `IsImageGen` nor `IsDecision`)."

2. `load-balance-groups/architecture-recommendation.md:25` — the enforced hard-requirements scorecard:
   > "**Hard-requirements scorecard (enforced by all three workers):** (1) client IO unchanged ✅ · (2) chat-only ✅ · (3) sticky routing fully designed ✅ · (4) shadowing semantics ✅ · (5) flat + minimal, cuts parked in §14 ✅ · (6) FE tab consistent ✅. **No violations found.**"

3. `load-balance-groups/research-codebase.md:536` — why no group misroute guard exists:
   > "For groups: **NO misroute guard on the chat path** — groups ARE chat. They belong on /v1/chat/completions."

4. `load-balance-groups/plan.md:290` (rule G6) — group rows are forbidden from the fallback machinery, which is also what makes group→group nesting impossible:
   > "| G6 | `FallbackChain` empty; `SecondaryUpstreamModel == ""`; `PeakHourEnabled == false`; `ReleaseStreamChunkDeadline == 0` | imggen/decision posture; makes group→group fallback impossible (research-codebase §9.2 item 14 denied for v1 → §14). Cut features are v2 candidates. |"

5. `pkg/ui/frontend/src/components/config/GroupMemberEditor.tsx:10-13` — the FE encodes the same chat-only membership rule:
   > "// Chat-kind models only — the parent (GroupModelForm) filters the
   > // useModels() list BEFORE handing it down (plan §6.2 / Am30: members
   > // are PROXY MODEL IDs of existing chat-kind models; image-gen,
   > // decision, and group rows are never valid members)."

6. `pkg/ui/frontend/src/components/config/GroupMemberEditor.tsx:40` — enforced defensively in code:
   > `const pickerModels = chatModels.filter((m) => !m.kind || m.kind === 'chat');`

Consequence for the A-vs-B ruling (stated as fact, not recommendation): today's membership rule is
chat-kind by construction at **four** layers — `GroupMember` doc (`types.ts:170-173`), the editor
filter (`GroupMemberEditor.tsx:40`), the form's `chatModels` prop contract (`GroupModelForm.tsx:49-53`),
and the parent filter (`SettingsPage.tsx:293-295`). Any decision-group membership reader must
either (a) widen all four, or (b) introduce a separate member vocabulary for decision groups.
The BE counterpart is `ValidateGroupMembers` (`G3` member-kind validity) at `pkg/models/group.go:120`
[grep-main].
