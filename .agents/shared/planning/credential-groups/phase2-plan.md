# Phase 2: Frontend — Credential Groups container, sub-nav, group management UI, mixed model picker

Date: 2026-11-16
Author: plan-creation worker (phase 2 — frontend) via plan-creation skill
Status: Draft — ready for approver review
Base: `feature/credential-groups` @ `2b987bf` (== `origin/latest` at branch time; zero commits)
Worktree: `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-cred-groups` (NOT the main checkout)
Sibling plans: `phase1-plan.md` (backend) and `phase3-plan.md` (gate+ship) — written in parallel, cross-referenced by filename only.
Precedent spec: `.agents/shared/planning/decision-groups/phase2-plan.md` (read-only, in `../llm-supervisor-proxy-wt-decision-groups/`) — the vertical sub-nav idiom is mirrored here with `CredentialGroup*` naming.
Base research: `.agents/shared/planning/credential-groups/research-fe.md` (FE inventory, verified at `2b987bf`).

**Scope of this artifact:** markdown plan only. NO code, NO tests written, NO config edits, NO commits, NO pushes, NO deploys. `vite build`, `tsc`, and `vitest` were **not run** (forbidden by the task) — every statement below is static reasoning from direct `read_file` of the worktree at `2b987bf`. Each code claim carries `path:line`; unverified items are collected under `## Not verified`.

---

## Objective

Clicking the top-level **"Credentials"** tab shows a container with a **vertical left-hand nav** offering two child tabs — `credentials` (the existing credential list, behaviour unchanged apart from group-membership badges) and `groups` (a new credential-group management surface: named, same-provider bundles of credentials, ordered members, "used by N models", 409 delete-block with an inline-then-delete action) — and the chat model credential picker accepts **both** direct credential refs and credential-group refs in one ordered, weighted list, so that adding an account to a group once makes every referencing model gain it.

**Completion test (one sentence):** a credential group created through the `groups` child tab appears in that tab's list with its ordered same-provider members, a chat model can reference that group alongside direct credentials (visually distinct, same-provider rule intact, weight on the group ref), deleting a referenced group is blocked with the referencing models named and an inline-then-delete action that succeeds, and a model with a plain credential list round-trips byte-identically.

---

## Scope

### In Scope
- The credentials-area container + vertical left sub-nav (`Credentials` / `Credential Groups`), mirroring the decision-lane idiom.
- `CredentialGroupsTab` (list + create/edit/delete + ordered member picker filtered to the group's provider + "used by N models" + 409 delete-block flow with inline-then-delete).
- `CredentialGroupForm` + `CredentialGroupMemberPicker`.
- `CredentialsTab` group-membership badges (additive, minimal).
- `types.ts`: `CredentialGroup`, `CredentialGroupMember`, and an additive `group_id?` discriminator on `CredentialRef`.
- `useApi.ts`: credential-group CRUD with server-side `used_by` on list+detail, each mutation calling `invalidateModelCaches()`.
- `MultiCredentialEditor` + `ModelForm`: mixed credential/group picker, visually distinct group options, extended same-provider + no-duplicate rules, weight on the group ref, group-aware primary display, Test-button contract.
- FE tests for all of the above + the gate procedures (tsc baseline parity, static zero-mod proof).

### Out of Scope
- **Backend** (`/fe/api/credential-groups` handlers, flatten helper, `handleTestModel` group expansion, DB/migrations) — phase1-plan.md. This plan only consumes the contract in §4 and flags integration deltas.
- **Gate + ship** (CI, embed, deploy, GitOps) — phase3-plan.md.
- **Decision-lane surfaces** (`DecisionModelsContainer`, `groupLane.ts`, `GroupModelsTab/Form/MemberEditor` lane parameterization) — the sibling lane's plan; we do not touch them.
- **Decision/ImgGen model forms** — untouched; they structurally cannot produce group refs (§1.7).
- **Runtime flatten semantics** (dedup keep-first, member weight inheritance) — backend; the FE only renders and submits refs.
- **Model-kind group UI** (`Group Models` top tab) — different domain; the `Group*` namespace stays reserved for it.

### Adjacent features deliberately excluded
- Group-level weights / backup-only tiers / racing on the credential-group lane (the commission is management-only bundling; no runtime policy change).
- A `Credential Group` picker on the Decision/ImgGen lanes (groups are chat-kind only).
- URL/route changes for the sub-nav (house rule: no router below `SettingsPage`).

---

## Context — the FE surface being changed (verified inventory)

| Fact | Evidence |
|---|---|
| SettingsPage owns all 11 admin tabs in local state; no tab touches the URL | `pkg/ui/frontend/src/components/SettingsPage.tsx:66` (`TabType`), `:103` (`useState<TabType>('proxy')`), `:381-520` (top bar), `:526-699` (conditional branches) |
| The Credentials tab is mounted with `setStatus` only | `SettingsPage.tsx:647-651` — `<CredentialsTab setStatus={setStatusWrapper} />` (the `status` prop is omitted; this is one of the 6 frozen `SettingsPage.tsx` tsc errors) |
| "Manage Credentials" links navigate to the credentials tab | `SettingsPage.tsx:573` (models), `:593` (imggen), `:615` (decision) — `onNavigateToCredentials={() => setActiveTab('credentials')}` |
| CredentialsTab is self-fetching, 434 L, own heading, inline form, fixed-overlay delete modal | `CredentialsTab.tsx:28-55` (fetch), `:71-128` (CRUD), `:139-233` (list), `:250-286` (delete modal), `:291-434` (`CredentialForm`), `:12-26` (`PROVIDER_COLORS`) |
| ModelForm fetches credentials itself and hydrates `credentials: CredentialRef[]` with a legacy `credential_id` shim | `ModelForm.tsx:156-169` (fetch), `:186-221` (hydration + shim) |
| MultiCredentialEditor is ordered `<select>` rows, one ref per row + weight, `MAX_CREDENTIALS=16`, row 0 = "Primary", same-provider + no-duplicate enforced in `isOptionDisabled` | `MultiCredentialEditor.tsx:11`, `:70-95`, `:128`, `:130-155`, `:154` |
| The editor is reused unchanged by the Decision and ImgGen forms, which filter `availableCredentials` to the selected provider at the form level | `DecisionModelForm.tsx:4`, `:156-175`, `:468-471`; `ImgGenModelForm.tsx:4`, `:135-150`, `:382-385` |
| ModelForm submit validation mirrors server 400 rules; the payload maps `credential_id/weight/position` | `ModelForm.tsx:314-345` |
| Test Connection posts `credential_id` = primary ref only | `ModelForm.tsx:384-425`, `:391` (`primaryCredId`), `:428-429` (`canTestConnection`) |
| Credential API is uncached direct fetch; every mutation calls `invalidateModelCaches()` (4 keys) | `useApi.ts:951-1013`, `:962-966`, `:971-976` |
| `useProviders()` is the provider vocabulary, cached 60 s | `useApi.ts:1016-1048` |
| types.ts mirrors Go wire structs verbatim, snake_case, doc comments citing commissions; the `Group*` cluster is the model-group namespace | `types.ts:11-16` (`Credential`), `:21-25` (`CredentialRef`), `:107-154` (`Model`), `:133` (`credentials?`), `:182-192` (`GroupMember`/`GroupModel`) |
| Decision-lane sub-tab idiom (spec only, zero commits) | `../llm-supervisor-proxy-wt-decision-groups/.agents/shared/planning/decision-groups/phase2-plan.md:106-157` (markup), `:139-148` (class derivation), `:352` (test conventions) |
| tsc frozen baseline: 19 errors / 9 files; `SettingsPage.tsx` 6, `ModelsTab.tsx` 6, `CredentialsTab.tsx` 1, `ModelForm.tsx` 1, + 5 single-error files | `pkg/ui/frontend/tsc-baseline.txt:14-27` |
| vitest: happy-dom, globals off, tests at `src/**/__tests__/**/*.test.{ts,tsx}` | `pkg/ui/frontend/vitest.config.ts:11`, `:13` |
| Static build writes the TRACKED `pkg/ui/static/` | `pkg/ui/frontend/vite.config.ts:7-9` |

---

## 1. Component tree

### 1.1 New files

| File | Responsibility |
|---|---|
| `pkg/ui/frontend/src/components/config/CredentialsContainer.tsx` **(NEW)** | The container. Owns the vertical left nav, the child-tab state, the groups fetch, the membership map, and the conditional panel mount. **No page-level heading of its own** — each child panel owns its heading (this is what keeps `CredentialsTab` as close to byte-unchanged as the badge requirement allows). |
| `pkg/ui/frontend/src/components/config/CredentialGroupsTab.tsx` **(NEW)** | Group list + create/edit/delete + "used by N models" + 409 delete-block flow with inline-then-delete. Mirrors `CredentialsTab.tsx`'s structure (list ↔ inline form ↔ fixed-overlay modal). |
| `pkg/ui/frontend/src/components/config/CredentialGroupForm.tsx` **(NEW)** | Group form mirroring `CredentialForm` (`CredentialsTab.tsx:291-434`): ID / Name / Provider (via `useProviders()`) + the ordered member picker. |
| `pkg/ui/frontend/src/components/config/CredentialGroupMemberPicker.tsx` **(NEW)** | Ordered member rows (provider-filtered `<select>` + add/remove/reorder), mirroring `MultiCredentialEditor.tsx`'s row idiom minus weight (a group member carries no weight; weight lives on the model's group ref). |

### 1.2 Changed files

| File | Change |
|---|---|
| `pkg/ui/frontend/src/components/SettingsPage.tsx` | (a) import `CredentialsContainer`; (b) mount it in the `credentials` branch (`:647-651`) passing `models` + `onUpdateModel` (both already SettingsPage props) + `setStatusWrapper`. Top-level tab bar **unchanged** (11 tabs, same order, same `data-tab-id`). |
| `pkg/ui/frontend/src/components/config/CredentialsTab.tsx` | **Additive only**: new optional prop `groupMemberships?: Record<string, string[]>`; render one badge per group name in the Provider cell. Everything else byte-identical. |
| `pkg/ui/frontend/src/types.ts` | Add `CredentialGroup`, `CredentialGroupMember`; add `group_id?: string` to `CredentialRef` (§2). |
| `pkg/ui/frontend/src/hooks/useApi.ts` | Add `getCredentialGroups`, `createCredentialGroup`, `updateCredentialGroup`, `deleteCredentialGroup`; each mutation calls `invalidateModelCaches()` (§3). `CredentialGroup` carries server-side `used_by?: string[]` on list+detail (A-10); no `/{id}/used-by` route. |
| `pkg/ui/frontend/src/components/config/MultiCredentialEditor.tsx` | Accept `availableGroups?: CredentialGroup[]`; render group options distinctly; extend `isOptionDisabled`; weight applies to the ref (§1.7). |
| `pkg/ui/frontend/src/components/config/ModelForm.tsx` | Fetch groups; pass `availableGroups`; hydrate group refs; extend submit validation + primary display + `canTestConnection` (§1.7). |

### 1.3 Container markup spec (mirrors the decision-lane idiom)

```
<div class="flex gap-4" data-testid="credential-groups-container">
  <nav class="shrink-0 w-40 flex flex-col gap-1 border-r border-gray-700 pr-2"
       data-testid="credential-groups-nav"
       aria-label="Credentials sections">
    <button type="button"
            class="px-3 py-2 text-left font-medium transition-colors whitespace-nowrap border-l-2 text-blue-400 border-l-blue-400"
            data-testid="credential-groups-tab-credentials"
            aria-label="Credentials"
            aria-current="true"
            onClick={() => setChild('credentials')}>Credentials</button>
    <!-- aria-current="true" on the ACTIVE button only; the INACTIVE button
         OMITS it (never "false"). No role=tablist/tab/tabpanel, no
         aria-selected, no roving tabindex, no arrow-key handlers. -->
    <button type="button"
            class="px-3 py-2 text-left font-medium transition-colors whitespace-nowrap border-l-2 text-gray-400 hover:text-white border-l-transparent"
            data-testid="credential-groups-tab-groups"
            aria-label="Credential Groups"
            onClick={() => setChild('groups')}>Credential Groups</button>
  </nav>
  <div class="flex-1 min-w-0" data-testid="credential-groups-panel">
    {child === 'credentials'
      ? (<CredentialsTab status={status} setStatus={setStatus} groupMemberships={groupMemberships} />)
      : (<CredentialGroupsTab groups={groups} models={models} usedBy={usedBy}
                              onInlineAndDelete={handleInlineAndDelete}
                              status={status} setStatus={setStatus} />)}
  </div>
</div>
```

**Class derivation** (same reasoning as the decision lane, `phase2-plan.md:139-148`):

| Class | Source |
|---|---|
| `text-blue-400` / `text-gray-400 hover:text-white` | Top bar active/inactive pair verbatim (`SettingsPage.tsx:388-391`) |
| `border-l-2 border-blue-400` / `border-l-2 border-transparent` | Translation of the top bar's `border-b-2 border-blue-400` (`:390`) to the left edge; `border-l-transparent` on the inactive item keeps geometry constant (no 2px shift on activation) |
| `font-medium transition-colors whitespace-nowrap` | Top bar base typography (`:388`); `px-6 py-3` → `px-3 py-2` for a 10rem column |
| `flex flex-col gap-1` + `border-r border-gray-700` | Divider idiom borrowed from `RequestList.tsx:51`; `gap-1` is in the house scale |
| `w-40` | Use the class; the prior citation (`EventLog.tsx:174-178`) is a colour map, not a width precedent — drop the citation (A-12.v). |
| `flex gap-4` + `shrink-0` + `flex-1 min-w-0` | `MCPServersTab.tsx:143-144`, `SettingsPage.tsx:367`, `Toast.tsx:97/:112` |

**Labels (locked):** visible labels are Title-Case `Credentials` / `Credential Groups` (matching every other label in the surface). The user-visible feature name is exactly **"Credential Groups"** — never bare "Groups"/"group" in labels or docs (the `Group Models` top tab owns that word). `aria-label` mirrors the visible label.

**State / routing:** local `useState<'credentials' | 'groups'>('credentials')`; **conditional mount/unmount** (CSS-hide rejected — half-filled form survival, DOM weight, no precedent); **no URL change** (house rule: no router below `SettingsPage`).

**Container data flow:**
- Props include `status` **and** `setStatus` (A-12.i) — both are forwarded to `CredentialsTab`; the mount move must not drop `status` (SettingsPage.tsx:649 passes only `setStatus` today; CredentialsTab.tsx:7-8/:28 takes both).
- `useEffect` on mount → `getCredentialGroups()` → `groups` (uncached).
- `groupMemberships: Record<credentialId, string[]>` derived from `groups` (credential id → group names) — passed to `CredentialsTab`.
- `usedBy: Record<groupId, string[]>` derived from the (uncached) groups list's server-side `used_by` (A-10) — model ids resolved to names via the chat `models` prop for display only; **not** derived from the 15s-TTL models cache. Passed to `CredentialGroupsTab`.
- `handleInlineAndDelete(group)` — the 409 recovery action (§1.6).

### 1.4 CredentialsTab wrap (byte-unchanged where possible + badges)

`CredentialsTab` keeps its own heading, self-fetch, CRUD, and delete modal. The **only** change is:

```tsx
interface CredentialsTabProps {
  status: { type: 'success' | 'error'; message: string } | null;
  setStatus: (status: { type: 'success' | 'error'; message: string } | null) => void;
  /** credential-groups commission — credential id → group names. */
  groupMemberships?: Record<string, string[]>;
}
```

In the Provider cell (`CredentialsTab.tsx:186-190`), after the provider badge, render:

```tsx
{groupMemberships?.[cred.id]?.map((name) => (
  <span key={name} data-testid="credential-group-badge"
        class="inline-flex items-center text-xs px-2 py-1 rounded border bg-purple-900/50 text-purple-300 border-purple-800/40 mt-1">
    🗂️ {escapeHtml(name)}
  </span>
))}
```

**Membership source — CHOSEN: FE-side computation from the (uncached) fetched groups list (A-10).** The badge data flows from the groups list's server-side `members`/`used_by` — **not** from the 15s-TTL `models` prop. Justification: the groups list is already fetched (uncached, mirroring the credentials functions) by the container for the `groups` tab, so the badge and the group list share one source of truth (no divergence); it adds no backend wire field and no extra request; the group list is a small management surface. The backend `GroupsContainingCredential` field (contract §4) is therefore **not required** for badges — it remains available for the delete-impact message. **Rejected alternative:** a per-credential backend membership field — more wire surface, and it can disagree with the rendered group list.

**Credential delete impact:** the existing confirm modal (`:250-286`) gains a line listing the groups the credential belongs to (from `groupMemberships?.[cred.id]`), so the operator sees the group surface before deleting. If the backend still returns 409 (credential is a group member or model-referenced), the error message is surfaced verbatim via `setStatus` (`:123-127`). No new fetch.

### 1.5 CredentialGroupsTab spec

Mirrors `CredentialsTab.tsx`'s three-state shape (list ↔ inline form ↔ modal).

- **Props:** `{ groups: CredentialGroup[]; models: Model[]; usedBy: Record<string, Model[]>; onInlineAndDelete: (group: CredentialGroup) => Promise<void>; status; setStatus }`.
- **Self-fetch:** `getCredentials()` on mount (mirrors `CredentialsTab.tsx:42-55`) for the member picker.
- **List** (`data-testid="credential-groups-list"`): table ID / Name / Provider / Members / Used by / Actions; row `data-testid="credential-group-row-<id>"`; "Used by N models" badge `data-testid="credential-group-used-by-<id>"` (from `usedBy[id]?.length`).
- **CTA** "Add Credential Group" → inline `CredentialGroupForm` (mirrors `showForm`/`formMode` state at `:31-33`).
- **Edit** → form prefilled (`initialData`).
- **Delete** → fixed-overlay confirm modal (mirrors `:250-286`) → `deleteCredentialGroup(id)`.
  - **409 delete-block flow:** on `!res.ok` with 409, branch on the **status code only** (A-9) — never parse the error message text, never assume the body is JSON beyond the bare `{"error"}` envelope (a `text/plain` body throws `SyntaxError`). The FE then re-fetches the groups list (uncached) and reads the blocked group's server-side `used_by` to render the message + the referencing model list (`data-testid="credential-group-blocked-models"`) + an **"Inline members then delete"** button (`data-testid="credential-group-inline-delete"`).
  - The modal stays open so the operator can cancel; the error is also mirrored to `setStatus`.
- **Empty state** copy states the management-only semantic: "Members are flattened into the referencing models at resolution — grouping changes no routing behaviour."

### 1.6 CredentialGroupForm + CredentialGroupMemberPicker spec

`CredentialGroupForm` mirrors `CredentialForm` (`CredentialsTab.tsx:291-434`):

- Fields: **Group ID** (disabled in edit, like `:353-360`), **Name**, **Provider** (`<select>` fed by `useProviders()`, required).
- **No base_url field** — a group is a bundle of existing credentials; each member carries its own `base_url`.
- Mounts `CredentialGroupMemberPicker` with `provider={provider}` + `members` + `onChange`.
- **Submit-time guards (mirror the backend, defense in depth):**
  - ID + Name required.
  - At least one member.
  - No duplicate member id.
  - Every member exists in the fetched credentials.
  - Every member's provider === the group provider → `"Member <id> is not a <provider> credential"` (cross-provider reject; the picker is filtered so this is a stale-row / retyped-id guard).
- **Save payload:** `createCredentialGroup({ id, name, provider, members })` / `updateCredentialGroup(id, { id, name, provider, members })`; on success re-fetch groups (the container owns the refetch via a passed `onGroupsChanged` callback, or the tab re-fetches — see Open Questions OQ-1).

`CredentialGroupMemberPicker` mirrors `MultiCredentialEditor.tsx`'s row idiom:

- Ordered rows: `<select>` of credentials **filtered to `provider`**, + remove button + up/down reorder buttons.
- `MAX_GROUP_MEMBERS = 16` (mirrors `GroupMemberEditor.tsx:6`; a FE guard — the backend 64 KB cap is the authority).
- Placeholder "Select a credential"; the picker shows `{cred.id} ({cred.provider})` (same label grammar as `MultiCredentialEditor.tsx:154`).
- No weight input (weight lives on the model's group ref).

### 1.7 MultiCredentialEditor + ModelForm extension (mixed picker)

**Wire discriminator (see §4/OQ-2):** `CredentialRef` gains `group_id?: string`. A group ref has `credential_id: ""` and `group_id: "<groupId>"`; a plain ref has `group_id` absent/undefined. Plain lists round-trip byte-identically (`group_id` omitted when undefined).

**`MultiCredentialEditor.tsx`:**
- New prop `availableGroups?: CredentialGroup[]` (default `[]`). `DecisionModelForm.tsx:468` / `ImgGenModelForm.tsx:382` do **not** pass it → zero group options → those forms are structurally unchanged.
- Build `groupIds = new Set(availableGroups.map(g => g.id))`; on change, `groupIds.has(value)` ⇒ set `{ group_id: value, credential_id: '' }`, else `{ credential_id: value, group_id: undefined }`.
- Row value = `row.credential_id || row.group_id`.
- **Group option rendering (visually distinct):** label `🗂️ {group.name} ({group.provider})`; when a row's value is a group id, render a `🗂️ Group` badge beside the row label (`data-testid="credential-group-ref-badge"`).
- **`isOptionDisabled` extension (precise):**
  - `primaryRef = credentials[0]`; `primaryProvider` = `primaryRef.group_id ? availableGroups.find(g => g.id === primaryRef.group_id)?.provider : availableCredentials.find(c => c.id === primaryRef.credential_id)?.provider`. **LOAD-BEARING (A-12.ii):** when row 0 is a group ref, the same-provider lock MUST re-key on the group's provider — otherwise the lock silently disappears for group-primary models. Cite `MultiCredentialEditor.tsx:65-68` (`primaryProvider` from `credentials[0]?.credential_id`) and `:70-95` (`isOptionDisabled`).
  - **Group option `g` on row `rowIndex`:** disabled if any other row has `group_id === g.id` ("Already selected in another row"); disabled if `rowIndex > 0 && primaryProvider && g.provider !== primaryProvider` ("All credentials must share the same provider"); else enabled.
  - **Credential option `c` on row `rowIndex`:** existing rules unchanged, except `primaryProvider` may now derive from a group.
  - **Warn-only (not disabled):** a credential that is a member of a group already selected on another row renders a hint below the editor ("Credential X is already included via group Y") — mirrors the warn-only dup idiom at `GroupMemberEditor.tsx:81-85`. **This is a backend invariant (R-6 dedup keep-first), not an FE gap** (A-12.iii): the FE does not dedup; the backend flattens with dedup keep-first. Optional; may be deferred.
- **Weight:** unchanged per-row input; for a group row the weight applies to the group ref (members inherit it at flatten — backend).

**`ModelForm.tsx`:**
- Fetch groups on mount (`getCredentialGroups()`) → `groups`.
- Pass `availableGroups={groups.filter(g => g.provider !== '9router')}` (mirrors the credential 9router filter at `:543`).
- **Hydration (`:186-221`):** map `credentials` → `{ credential_id: c.credential_id ?? '', group_id: c.group_id, weight, position }`; the legacy `credential_id` shim is unchanged.
- **Submit validation (`:314-345`):**
  - `if (!row.credential_id && !row.group_id) throw "Credential row N: please select a credential or group"`.
  - Resolve provider: credential → `credentials.find(...)`; group → `groups.find(...)`.
  - 9router guard for credentials (unchanged) + for groups (`group.provider === '9router'` → same message).
  - Duplicate key = `row.credential_id || row.group_id` (so a credential and a group cannot share a row slot, and identical refs are rejected).
  - Weight check unchanged.
  - `validatedCredentials` maps `{ credential_id: c.credential_id || undefined, group_id: c.group_id, weight, position: i }` — `group_id` omitted when undefined ⇒ plain lists byte-identical.
- **Primary display (`:438-444`):** if row 0 is a group, `selectedProvider = group.provider` and render a `🗂️ Group: {name}` badge; the base-URL line (`:556-560`) is hidden for a group primary (members carry their own).
- **Test button (`:384-425`):** see §4 integration contract. `canTestConnection` (`:428-429`) gains `|| formData.credentials[0]?.group_id`.

---

## 2. types.ts additions

```ts
// credential-groups commission (2026-11-16): a named bundle of
// same-provider credentials. Management-only — flattened at model
// resolution (groups expand in place, dedup keep-first). Wire mirror of
// the Go CredentialGroup struct (snake_case verbatim). `members` is the
// ORDERED list of credential ids.
export interface CredentialGroup {
  id: string;
  name: string;
  provider: string;
  members: string[];
  // A-10: server-side on list+detail (cheap at N-group scale). Model ids
  // referencing this group. FE never derives this from the models cache.
  used_by?: string[];
  created_at: string;
  updated_at: string;
}

// FE view model enriching a member id with its credential metadata for
// rendering (derived client-side from the credentials list; NOT a wire
// type). Exists so the picker/list never re-derives labels inline.
export interface CredentialGroupMember {
  credential_id: string;
  provider: string;
  label: string;
}
```

`CredentialRef` gains one additive field (doc comment updated):

```ts
export interface CredentialRef {
  credential_id: string;   // empty when this ref is a group
  group_id?: string;       // credential-groups commission: set ⇒ group ref
  weight: number;
  position: number;
}
```

**Naming discipline:** every new symbol is `CredentialGroup*`; the bare `Group*` namespace (`GroupMember`, `GroupModel`) stays reserved for model load-balance groups. No existing type is modified except the additive `group_id?`.

---

## 3. useApi.ts additions

```ts
// credential-groups commission — every group mutation invalidates the
// SAME four model-list caches as the credential mutations (:971-976),
// because a group create/update/delete changes the flat credential lists
// the model tabs render.
export async function getCredentialGroups(): Promise<CredentialGroup[]>
export async function createCredentialGroup(group: CredentialGroup): Promise<CredentialGroup>
export async function updateCredentialGroup(id: string, group: Partial<CredentialGroup>): Promise<CredentialGroup>
export async function deleteCredentialGroup(id: string): Promise<void>
```

- URLs: `GET/POST /fe/api/credential-groups`, `GET/PUT/DELETE /fe/api/credential-groups/{id}/` (trailing slash — ServeMux prefix-subtree, precedent `server.go:197`; A-12.vi). No `/{id}/used-by` route — `used_by` is server-side on list+detail (A-10).
- Bare `{"error":"<string>"}` envelope on failure; `throw new Error(err.error || '...')` (mirrors `:984-987`). **FE branches on STATUS CODES only** (A-9): never parse error message text; never assume the body is JSON beyond the bare `{"error"}` envelope (a `text/plain` body throws `SyntaxError`). Groups are fetched **uncached**, mirroring the credentials functions (`getCredentials` is a direct fetch, `:962-966`).
- **Every mutation calls `invalidateModelCaches()`** — the quadruple delete at `:971-976` is the canonical contract; a group rename/delete changes the flat lists.
- Fetch-stub test coverage: `useApiCredentialGroups.test.ts` mirrors `useApiCredentials.test.ts:56` (`MODEL_KEYS`) and asserts all four keys are cleared after each group mutation.

---

## 4. Backend contract + integration notes (cross-phase — planner reconciles)

Plan FE against the phase1 shape: `CredentialGroup {id, name, provider, members: [credential_id...] (ordered), used_by?: string[] (server-side on list+detail), timestamps}`; `POST` validates same-provider + member existence; 64 KB cap; bare error envelope at **every** error site incl. 405/missing-id (A-9 — text/plain breaks `res.json()`); 201/204 conventions as credentials; `DELETE` → 409 naming referencing models; `{id}` routes registered with the literal trailing slash (A-12.vi); **no `/{id}/used-by` route** (A-10); credential `DELETE` → 409 when a group member or model-referenced.

**Integration note 1 — `CredentialRef` discriminator (OQ-2).** The FE needs a discriminator to render group refs distinctly and to submit them. This plan assumes the additive `group_id?: string` (§2). If phase1 chooses a different discriminator (e.g. `kind: 'credential' | 'credential_group'`, or the group id in `credential_id` with a separate marker), the FE adapter is a one-line change in `MultiCredentialEditor` + `ModelForm`; the component tree is unchanged. **Flagged for the planner.**

**Integration note 2 — Test button with a group primary (RECOMMENDED).** `handleTestModel` (`pkg/ui/server.go:2053`) receives `req.CredentialID`. When the primary ref is a group, a raw group id → `GetCredential` → 404.
- **RECOMMENDED (chosen):** the backend expands group ids through the same flatten helper in `handleTestModel` — if the id names a credential, today's path; if it names a group, flatten (dedup keep-first) and use the first member. FE delta: `primaryRefId = credentials[0]?.credential_id || credentials[0]?.group_id` (one line, `:391`), `canTestConnection` accepts a group primary (`:428-429`), and label rendering shows the group. Payload shape unchanged.
- **Justification:** one flatten source of truth (backend), the FE carries no flatten logic, and the payload contract is unchanged. The ALTERNATIVE (FE sends the first flattened member id) duplicates flatten logic in the FE and can diverge from the saved model shape — rejected.
- **Recorded as an explicit cross-phase integration requirement.**

**Integration note 3 — `used_by` surface (A-10).** `used_by` is server-side on the **list** and **detail** responses; the `/{id}/used-by` route is dropped. The list badge is FE-computed from the (uncached) groups list's `members` (not the 15s-TTL `models` prop); the 409 flow reads the blocked group's `used_by` from a re-fetched (uncached) groups list — never from the error message text (A-9). Groups are fetched uncached, mirroring the credentials functions.

**Integration note 4 — inline-then-delete is FE-driven.** The FE replaces each referencing model's group ref with the group's member list via the existing model-save flow (`onUpdateModel`), then re-attempts the delete. Weight mapping: each inlined member gets the group ref's `weight` (preserving effective routing weight); positions are reindexed. No backend endpoint required. **Flagged so phase1 does not build one.**

---

## 5. Test matrix (mapped to tasks)

House conventions (`vitest.config.ts:11`, `:13`; `phase2-plan.md:352`): `describe/it/expect/vi` imported from `'vitest'`; tests at `src/**/__tests__/**/*.test.{ts,tsx}`; `beforeEach(() => { document.body.innerHTML = ''; })` + `afterEach(() => { cleanup(); vi.clearAllMocks(); })`; assert on `data-testid`; `fireEvent` from `@testing-library/preact`. happy-dom traps: `keyDown` on `window` never reaches listeners (fire on the focused element); Enter→click is **not** synthesized (assert preconditions + click).

| # | File | Cases | Task |
|---|---|---|---|
| T-A | `config/__tests__/CredentialsContainer.test.tsx` **(NEW)** | A1 two nav items `Credentials`/`Credential Groups` in order; A2 nav first in DOM, panel follows; A3 nav is `flex-col`, no `overflow-x-auto`; A4 nav buttons **not** matched by `div.flex.overflow-x-auto > button`; A5 `credentials` default child; A6 clicking `Credential Groups` swaps the panel **and moves `aria-current="true"`** (inactive omits it); A7 active/inactive class pair + active-only `aria-current`; A8 native `<button type="button">`, not disabled, no `tabindex="-1"`; A9 `data-testid` + `aria-label` on each; A10 **no** `role="tablist"/"tab"/"tabpanel"/"aria-selected`; A11 credentials panel mounts the unchanged list (empty state + "Add Credential"); A12 switching to `groups` unmounts the credentials panel; A13 props pass through | 3 |
| T-B | `config/__tests__/CredentialGroupsTab.test.tsx` **(NEW)** | B1 list renders the groups it is given (no client re-filter); B2 "used by N models" badge count; B3 add → payload `{id,name,provider,members}`; B4 edit → PUT body; B5 delete happy path; B6 **409 delete-block**: error message + referencing-models list rendered; B7 **inline-then-delete**: models updated with inlined members, delete re-attempted, group gone; B8 empty state; B9 self-fetch credentials for the picker | 6, 7 |
| T-C | `config/__tests__/CredentialGroupForm.test.tsx` **(NEW)** | C1 provider select fed by `useProviders()`; C2 member picker filtered to the group's provider; C3 cross-provider member rejected at submit; C4 missing member rejected; C5 duplicate member rejected; C6 empty member list rejected; C7 ID disabled in edit; C8 save payload shape | 5, 6 |
| T-D | `config/__tests__/CredentialGroupMemberPicker.test.tsx` **(NEW)** | D1 renders only provider-matching credentials; D2 add/remove/reorder; D3 cap at `MAX_GROUP_MEMBERS`; D4 no duplicate member selectable | 5 |
| T-E | `config/__tests__/CredentialsTab.test.tsx` **(EXTEND)** | E1 group badges render per credential row from `groupMemberships`; E2 no badges when the map is empty/undefined; E3 credential delete modal lists the credential's groups; E4 existing 9router + ModelForm tests **unchanged and green** | 4 |
| T-F | `config/__tests__/MultiCredentialEditor.mixed.test.tsx` **(NEW)** | F1 group option renders distinctly (`🗂️` + provider); F2 group selectable on row 0; F3 group disabled on row >0 when providers differ; F4 group disabled when already selected on another row; F5 credential disabled when row-0 is a group of another provider; F6 weight input applies to the group row; F7 selecting a group writes `group_id` + empty `credential_id`; F8 **regression:** a plain credential list renders and submits byte-identically (no `group_id` in the payload) | 8 |
| T-G | `config/__tests__/ModelForm.mixed.test.tsx` **(NEW)** | G1 hydration of a group ref; G2 submit validation: empty ref, duplicate ref, 9router group; G3 Test button sends the group id as `credential_id` when the primary is a group; G4 `canTestConnection` true for a group primary; G5 primary display shows the group badge + provider; G6 legacy `credential_id` shim still works | 8 |
| T-H | `hooks/__tests__/useApiCredentialGroups.test.ts` **(NEW)** | H1 each group mutation clears the four model-list cache keys; H2 error envelope parsed (status-code branching, no message-text parsing); H3 list carries server-side `used_by`; H4 uncached fetch parity | 2 |
| T-I | `components/__tests__/SettingsPage.tabs.test.tsx` **(NO EDIT)** | stays green unchanged — the top-level bar still has 11 tabs in order; the credentials branch mounts the container | 3 |
| T-J | `config/__tests__/DecisionModelForm.test.tsx`, `ImgGenModelForm.test.tsx` **(NO EDIT)** | stay green unchanged — the shared editor's new prop is optional and unset | 8 |

**Folded addenda coverage (A-10, A-12.i–vi) — each has a testable row:**

| Addendum | Testable row | Where |
|---|---|---|
| A-10 (server-side `used_by` on list+detail; no `/{id}/used-by` route) | T-B (list carries `used_by`; 409 flow re-fetches groups and reads `used_by`) + T-H (H3/H4) | §1.5, §3 |
| A-12.i (container forwards `status` AND `setStatus`) | T-A (A13 props pass-through includes both) | §1.3 |
| A-12.ii (primaryProvider re-derivation for group-primary row 0) | T-F (F5) | §1.7 |
| A-12.iii (member-overlap warn-only = backend invariant R-6) | T-F (warn-only hint) — stated as backend invariant, not FE gap | §1.7 |
| A-12.iv (new test files type-clean; pre/post per-file tsc delta on 9 baseline files) | §6.1 procedure + gate task 9 | §6.1 |
| A-12.v (drop false `w-40` precedent citation) | doc-only; no test | §1.3 |
| A-12.vi (trailing-slash route registration) | §4 contract text + §3 URLs | §3, §4 |

**Regression pin (locked):** a model with a plain credential list behaves byte-identically — covered by T-F8 + T-G6 + the unchanged `CredentialsTab.test.tsx` / `SettingsPage.tabs.test.tsx` / Decision+ImgGen form suites.

---

## 6. Gate mechanics

### 6.1 tsc baseline parity
- `tsc-baseline.txt` freezes 19 errors / 9 files; the verifier asserts total + file-set + per-file counts (`src/utils/__tests__/tsc-baseline.test.ts:139-157`).
- **New clean files change nothing** (no `error TS` lines → never enter `perFile`).
- **Expected verdict:** unchanged 19/9. The edits to `SettingsPage.tsx` (6), `CredentialsTab.tsx` (1), `ModelForm.tsx` (1) are additive; `MultiCredentialEditor.tsx` is currently clean and must stay clean.
- **Pre/post per-file delta check (A-12.iv):** capture `npx tsc --noEmit` output **before** FE edits and **after**, then diff per-file counts for the 9 baseline files. An **accidental fix** of a frozen error in `CredentialsTab.tsx`(1)/`ModelForm.tsx`(1)/`SettingsPage.tsx`(6) is a gate failure with a confusing verdict — the count drops and the verifier fails, but the "fix" looks like progress. Treat any per-file count **decrease** as a failure, not a win, unless the baseline is re-frozen with an amendment note in the same commit.
- **Procedure:** `cd pkg/ui/frontend && npx tsc --noEmit | grep "error TS" | wc -l` after FE edits. **If the count or file-set moves**, update the per-file lines + footer in the same commit with an amendment note (`tsc-baseline.txt:3-9` precedent). Do **not** opportunistically fix the 19 frozen errors.

### 6.2 Static zero-mod proof
- `vite build` writes the TRACKED `pkg/ui/static/` (`vite.config.ts:7-9`). Zero-mod proof = `find pkg/ui/static -type f | sort | xargs sha256sum` before vs after a build with **no FE source change**; byte-identity is the mechanism claim (content-hashed names legitimately differ on real builds). The glob `sha256sum pkg/ui/static/**/*` is **broken** under default bash (globstar off) — it provably hashes 3 of 4 files, silently omitting `index.html`, the embed entry point (`server.go:88`) — verified by execution (A-13). This belongs to phase3's gate; phase2 only requires that the FE changes are real (so the static tree *will* change).

### 6.3 Test runner
- `cd pkg/ui/frontend && npx vitest run` (or `npm test`). All suites in §5 green; `-run` splits on `/` (bare subtest names select zero tests).

---

## 7. Merge-conflict disclosure (credential-groups × decision-groups) — SEMANTIC merge

Their lane lands in `latest` later; **our lane ships first**. Conflicts are resolved when the decision lane lands. **Upgrade (claim-6 correction 7):** both lanes add a container + left vertical nav to the same `SettingsPage` area and new types to the same `types.ts` cluster — the merge is **semantic**, not merely textual. The decision lane's new files are `DecisionModelsContainer.tsx`, `groupLane.ts`, and `__tests__/DecisionModelsContainer.test.tsx` (their phase2-plan §1.1); ours are `CredentialsContainer.tsx`, `CredentialGroupsTab.tsx`, `CredentialGroupForm.tsx`, `CredentialGroupMemberPicker.tsx`. Both containers use the `border-l-2`/`aria-current` idiom (our §1.3 / their §2.1) — at merge time this must be reconciled as a **shared convention** (one Tailwind class set, one aria-current rule), not two parallel implementations. Disclosed:

| File | Risk | Why | Resolution strategy at merge time |
|---|---|---|---|
| `components/SettingsPage.tsx` | **HIGH** | Both edit the imports block (`:4-16`) and adjacent content branches (decision `:607-619`; credentials `:647-651`) | Textual conflict, semantic independence. Resolve by keeping **both** branch edits: the decision branch mounts `DecisionModelsContainer`, the credentials branch mounts `CredentialsContainer`. Re-run `SettingsPage.tabs.test.tsx` + the tsc gate; the top-level bar is untouched by either lane so the 11-tab assertions hold. |
| `types.ts` | MEDIUM | Both additive near the `Group*` cluster (`:182-192`) | Vocabulary collision is the risk. Ours reserves `Group*` for model groups and adds `CredentialGroup*` + `CredentialRef.group_id?`; theirs adds `GroupLane`. Resolve by union — no symbol overlaps. |
| `components/__tests__/SettingsPage.tabs.test.tsx` | MEDIUM | Shared fetch-stub (`:12-23`) + `makeProps` (`:31-108`) + tab assertions both extend | Merge the stub extensions (add `/credential-groups` to the URL router) and keep both `makeProps` builders; assertions are additive. |
| `hooks/useApi.ts` | LOW | Decision plan lists no `useApi` changes; ours adds near `:951-1054` | Clean union. |
| `GroupModelsTab/Form/MemberEditor.tsx` | LOW | Decision renames props (`chatModels`→`laneModels`); we don't touch them | No conflict unless their rename shifts line numbers — rebase is trivial. |
| `MultiCredentialEditor/ModelForm/CredentialsTab/DecisionModelsTab` | NONE | Disjoint | — |

**Discipline:** our lane must not rename or refactor any `Group*` symbol, and must not touch `DecisionModelsTab.tsx` / `DecisionModelForm.tsx` / `ImgGenModelForm.tsx` beyond the optional `availableGroups` prop (which they don't pass).

---

## 8. Risk register

| # | Risk | Impact | Likelihood | Mitigation |
|---|---|---|---|---|
| R1 | `SettingsPage.tsx` double-lane textual conflict | Medium | High | §7 resolution strategy; semantic independence; gate re-run after merge |
| R2 | Vocabulary collision with the model-group `Group*` namespace | Medium | Medium | `CredentialGroup*` prefix everywhere; no bare `Group`/`group` label; §2 naming discipline; pinned by tests asserting labels |
| R3 | `MultiCredentialEditor` is shared by Decision/ImgGen forms — a regression there | High | Medium | `availableGroups` is optional and unset for those forms → zero group options; their suites run unchanged (T-J); the editor's plain-credential path is byte-identical (T-F8) |
| R4 | happy-dom traps (keyDown on window, Enter→click) produce false greens/reds | Medium | Low | Tests assert preconditions + `fireEvent.click`; no keyboard-activation assertions |
| R5 | Test-button-with-group-primary 404 if the backend does not expand | High | Medium | §4 integration note 2 (RECOMMENDED backend expansion) recorded as a cross-phase requirement; FE sends the group id in `credential_id`; T-G3 pins the payload |
| R6 | tsc frozen baseline moves when editing `SettingsPage.tsx`/`CredentialsTab.tsx`/`ModelForm.tsx` | Medium | Medium | Additive edits; run the §6.1 procedure; re-freeze only if counts move, with an amendment note |
| R7 | `CredentialsTab` "byte-unchanged" is broken by the badge requirement | Low | High | Accepted and disclosed: one optional prop + one badge block; the rest byte-identical; the frozen per-file count (1) must stay 1 |
| R8 | Group deletion 409 flow shows stale referencing models | Medium | Low | 409 → re-fetch the groups list (uncached) and read the blocked group's server-side `used_by`; branch on status code only (A-9); never parse error-message text |
| R9 | Inline-then-delete maps weights/positions wrong | Medium | Low | Spec'd: each inlined member gets the group ref's weight, positions reindexed; T-B7 pins the payload |
| R10 | Group refs leak into Decision/ImgGen submit payloads | High | Very low | Those forms never receive `availableGroups`; their `credential_id`-only validation rejects empty refs; T-J |
| R11 | Sub-nav labels collide with the top-level "Credentials" tab | Low | Medium | `aria-label` disambiguates; Title-Case grammar matches the decision lane; A9 pins labels |
| R12 | `CredentialGroup` wire shape diverges from phase1 | Medium | Medium | §4 integration notes 1–4 flagged for the planner; the FE adapter is one line if the discriminator changes |

---

## 9. Success criteria

| # | Criterion | How to measure | Threshold |
|---|---|---|---|
| SC1 | Container renders two child tabs and switches panels | `CredentialsContainer.test.tsx` A1–A13 | all green |
| SC2 | Group CRUD round-trips | `CredentialGroupsTab.test.tsx` B1–B9 | all green |
| SC3 | Member picker is provider-filtered, ordered, capped | `CredentialGroupMemberPicker.test.tsx` D1–D4 | all green |
| SC4 | 409 delete-block + inline-then-delete | T-B6, T-B7 | block message names models; inline action succeeds |
| SC5 | Mixed picker: group option distinct, same-provider + no-duplicate parity, weight on the ref | T-F1–F7 | all green |
| SC6 | Plain credential list is byte-identical | T-F8, T-G6 + unchanged Decision/ImgGen/Credentials suites | all green |
| SC7 | Group mutations invalidate the four model-list caches | `useApiCredentialGroups.test.ts` H1 | all four keys cleared |
| SC8 | tsc baseline parity | `npx tsc --noEmit | grep "error TS" \| wc -l` + `tsc-baseline.test.ts` | 19 errors / same 9 files (or a documented re-freeze) |
| SC9 | FE suite green | `npx vitest run` | 0 failures |
| SC10 | Top-level tab bar unchanged | `SettingsPage.tabs.test.tsx` without edit | green |
| SC11 | No `Group*` symbol renamed/added by this lane | `git diff` symbol audit | only `CredentialGroup*` added |
| SC12 | Backend contract deltas surfaced | §4 notes 1–4 delivered to the planner | planner reconciles before phase1 commit |

---

## 10. Tasks

| # | Task | Depends On | Acceptance |
|---|---|---|---|
| 1 | `types.ts`: add `CredentialGroup`, `CredentialGroupMember`, `CredentialRef.group_id?` + doc comments | none | tsc baseline unchanged; new symbols are `CredentialGroup*` |
| 2 | `useApi.ts`: group CRUD + used-by, each mutation calls `invalidateModelCaches()` | 1 | `useApiCredentialGroups.test.ts` green (4 keys cleared) |
| 3 | `CredentialsContainer.tsx` + mount in `SettingsPage.tsx` `:647-651` | 1, 2 | T-A green; `SettingsPage.tabs.test.tsx` green with no edit |
| 4 | `CredentialsTab.tsx` additive `groupMemberships` prop + badges | 3 | T-E green; per-file tsc count stays 1 |
| 5 | `CredentialGroupMemberPicker.tsx` | 1 | T-D green |
| 6 | `CredentialGroupForm.tsx` | 2, 5 | T-C green |
| 7 | `CredentialGroupsTab.tsx` (list, CRUD, used-by, 409 + inline-then-delete) | 3, 6 | T-B green |
| 8 | `MultiCredentialEditor.tsx` + `ModelForm.tsx` mixed picker | 1, 2 | T-F, T-G green; T-J (Decision/ImgGen) green unchanged |
| 9 | Gate: tsc parity, static zero-mod handoff, full `vitest run` | 1–8 | SC8–SC10 |

**Exit criterion:** tasks 1–9 green, §4 integration notes delivered to the planner, and the merge-conflict disclosure (§7) recorded for the decision-lane landing.

---

## Coupling

- **Tight with phase1 (backend):** the `CredentialGroup` wire shape, the `CredentialRef` discriminator, the `handleTestModel` group expansion, the server-side `used_by` + trailing-slash contract (A-9/A-10/A-12.vi). §4 notes 1–4 are the seam.
- **Tight with phase3 (gate+ship):** tsc parity, static zero-mod, the full vitest gate.
- **Tight internally:** `CredentialsContainer` ↔ `CredentialsTab` (badge map) ↔ `CredentialGroupsTab` (groups + usedBy) ↔ `CredentialGroupForm` ↔ `CredentialGroupMemberPicker`; `ModelForm` ↔ `MultiCredentialEditor` (shared option model).
- **Loose with the decision lane:** `SettingsPage.tsx`, `types.ts`, `SettingsPage.tabs.test.tsx` (§7).
- **Independent of:** `GroupModelsTab/Form/MemberEditor`, `DecisionModelsTab`, runtime flatten logic.

---

## Open questions (for the planner)

- **OQ-1 — group-list refetch ownership.** Should `CredentialGroupsTab` own the groups fetch (self-fetch, mirroring `CredentialsTab`) or should the container own it and pass `groups` + a `refetchGroups` callback? This plan assumes the **container owns the fetch** (it needs `groups` for the badge map and `usedBy`). If phase1/phase3 prefer self-fetch, the container drops to a pure nav and `CredentialsTab`/`CredentialGroupsTab` each fetch — a one-file change. **Recommendation: container owns it** (one fetch, one source of truth).
- **OQ-2 — `CredentialRef` discriminator.** Confirm `group_id?: string` vs a `kind` field vs group-id-in-`credential_id`. FE adapter is one line either way; the component tree is unchanged. **Recommendation: `group_id?: string`** (additive, mirrors `credential_id` style, omitted ⇒ plain ref).
- **OQ-3 — group refetch after save.** Does the backend return the created/updated group (201/204 conventions as credentials, `:2462-2530`/`:2630-2651`)? If 204, the FE re-fetches the list. Confirm in phase1.
- **OQ-4 — member cap.** `MAX_GROUP_MEMBERS = 16` is a FE guard; confirm the backend 64 KB cap is the only authority (no separate count limit).

---

## Not verified

- `vite build`, `tsc --noEmit`, and `vitest` were **not run** (forbidden by the task). The tsc verdict (19/9 unchanged) is a static expectation, not a measurement.
- The exact frozen error text in `CredentialsTab.tsx` / `ModelForm.tsx` / `SettingsPage.tsx` was not enumerated; the per-file counts are from `tsc-baseline.txt`.
- The backend contract in §4 is the phase1 plan's shape, not a shipped API; the `/fe/api/credential-groups` routes do not exist at `2b987bf`.
- The decision lane's phase2-plan was read for the idiom only; its committed state (zero commits) is per `research-fe.md:27-43`.
