# Phase 4: UI + API Surface

## Objective
Replace the single `credential_id` field on the model editor with a weighted, ordered list of same-provider credentials, and propagate that change through the JSON API surface, the validate endpoint, and the frontend form. After this phase, an operator can attach N credentials to one model, set per-credential integer weights, and save; the API round-trips the new shape; the validation endpoint rejects mixed-provider entries and non-positive weights with HTTP 400; the legacy `credential_id` field is gone from the API wire (the DB column is retained as a derived shadow per M-1 — see Amendment Changelog).

## Context
Phase 1 delivered the persistence: `models.credentials_json TEXT NOT NULL DEFAULT '[]'`, `ModelConfig.Credentials []models.CredentialRef` (`{credential_id, weight, position}`), `ModelsManager.Validate()` enforcing `weight > 0` and same-provider invariant. Phase 2 delivered the engine. Phase 3 wired the engine into the request path; it does NOT touch the UI. Phase 4 is the operator-facing surface that lets a user attach multiple credentials to one model.

Per `decisions.md §B` (as amended by M-1): the API payload uses `credentials: [{credential_id, weight, position}, ...]` as the single application source of truth; the DB `models.credential_id` column is retained only as a derived shadow (`= credentials[0].credential_id`) for legacy/external readers until migration 029 removes it. Per `decisions.md §D`: the UI rollout keeps a "Primary credential" affordance that maps to `credentials[0]`, but the wire shape is the array. The frontend is Preact + Vite + TypeScript + Tailwind (per the research facts and existing `pkg/ui/frontend/` structure).

The relevant precedent is `exclude_from_ultimate_switching` — a single boolean field added to `ModelConfig` with one matching checkbox in `ModelForm.tsx` (lines 709-725) and one matching field in `types.ts` (line 125). The multi-credential editor is the same shape, scaled: one new array field, N rows in the form, a credential picker per row, weight per row, add/remove buttons, default weight 1.

## Scope

### In Scope
1. **Modify** `pkg/ui/server.go`:
   - `Model` DTO (lines 27-50): drop `CredentialID string`; add `Credentials []CredentialRef` (mirror `models.CredentialRef`).
   - `handleModels` GET (lines 380-405): serialize `mc.Credentials` into the DTO.
   - `handleModels` POST (lines 411-471): accept and validate the new array; reject empty array when `internal=true`; reject entries with `credential_id == ""` or `weight <= 0`; reject mixed-provider; remap to `models.ModelConfig`.
   - `handleModelDetail` PUT (lines 511-588): same accept/validate pipeline as POST.
   - `handleModelDetail` DELETE (lines 589-605): unchanged (Phase 1's `RemoveModel` invalidates the engine).
   - `handleValidateModel` POST (lines 612-669): add per-entry validation (each `credential_id` exists, weight > 0, all providers match), returning the same `{valid: false, errors: [...]}` shape.
   - `handleTestModel` POST (lines 687-…): unchanged — the test endpoint is single-credential (the operator types a credential_id and api_key to test against). If the contract demands multi-cred test, leave as out-of-scope below.
2. **Modify** `pkg/ui/frontend/src/types.ts`:
   - Add `CredentialRef` interface (`{credential_id: string; weight: number; position: number}`).
   - Add `Credentials: CredentialRef[]` to `Model` interface; remove `credential_id?: string`.
3. **Modify** `pkg/ui/frontend/src/components/config/ModelForm.tsx`:
   - `formData`: replace `credential_id: string` with `credentials: CredentialRef[]` (initialize to `[]`).
   - `initialData` load (`useEffect` at lines 162-208): hydrate `credentials` from `initialData.credentials ?? []`. Backward-compat shim: if `initialData.credential_id` is set on a server that hasn't migrated yet, surface a banner (`Warning: legacy single-credential field detected; save to migrate`).
   - Submit payload (`handleSubmit` lines 282-311): remap `formData.credentials` to the API; drop the legacy `credential_id` field from the payload; validate weight > 0 client-side before sending.
   - Add a `<MultiCredentialEditor>` subcomponent (in the same file, or a new file under `components/config/`) — credential picker (Select), weight input (number, min 1, default 1), add/remove row buttons, same-provider invariant enforcement (disable adding a credential whose `provider` differs from `credentials[0]`'s provider once at least one row exists; show a tooltip explaining why).
   - Reuse the existing `getCredentials()` fetch (line 142) to populate the credential picker; filter by provider match when the user has already picked one.
   - Keep the "Test Connection" button working — when at least one credential row exists, test against `credentials[0]` (the primary) with the same payload shape as today.
4. **Modify** `pkg/ui/frontend/src/components/config/ModelForm.tsx` (rendering):
   - Place the `<MultiCredentialEditor>` inside the existing `Internal Upstream` collapsible (after the `credential_id` dropdown is removed). Visual layout: header "Credentials", sub-text "Add up to 16 same-provider credentials. Higher weight = more requests routed to that credential.", list of rows, "+ Add credential" button (disabled at 16 rows).
5. **Modify** `pkg/ui/handlers_models_test.go` (and any other UI handler test):
   - Tests that send `credential_id` in the payload must send `credentials: [{credential_id: "X", weight: 1, position: 0}]` instead.
   - Add coverage for: (a) empty `credentials` with `internal=true` → 400; (b) mixed-provider → 400; (c) weight=0 → 400; (d) legacy `credential_id` field present → ignored (no error, but the new shape is authoritative).
6. **Update** documentation:
   - `pkg/ui/frontend/README.md` (if it exists; per house pattern) — note the new multi-credential shape.
   - `README.md` at the repo root if it has a "Models" or "Configuration" section — short paragraph + a code block showing the new JSON shape.
   - **Skip** if neither README exists (house pattern is "document only if a doc page is in place" — do not create new docs).

### Out of Scope
- **NOT modifying** `pkg/ui/handlers_credentials*` — credentials CRUD is unchanged.
- **NOT modifying** the `handleTestModel` endpoint — single-credential testing only (the test payload is `{credential_id, api_key, internal_base_url, internal_model}` and is independent of `credentials[]`). Documenting this limitation is in-scope for the README update.
- **NOT adding** a new "duplicate credentials" UI affordance — operators can add the same credential twice with different weights? Per `decisions.md`, validation rejects duplicate `credential_id` entries (the validator in Phase 1's `ModelsManager.Validate` returns an error for that case); Phase 4 surfaces that error to the form via the toast.
- **NOT implementing** "Test Connection" against every credential in the list — only `credentials[0]`.
- **NOT modifying** the `pkg/ui/frontend/src/components/config/ModelsTab.tsx` list view — it does not render credential details today; Phase 5 may add a "credentials: N" badge but that's a Phase 5 task (tests/E2E).
- **NOT exposing** `position` as an editable UI control — `position` is set server-side (0-based on slice index) and the form sends whatever order the rows are in. The frontend just orders rows visually (drag-to-reorder is NOT in scope for v1).
- **NOT translating** the validation error messages — keep them in English (matches existing house pattern).

## Files

### Create
- **None required for the minimum implementation.** Optionally, if `MultiCredentialEditor` grows beyond ~120 lines, factor it into `pkg/ui/frontend/src/components/config/MultiCredentialEditor.tsx` (a new file) — the implementation may defer this decision to the implementer based on file size.

### Modify
| File | Lines | What changes |
|---|---|---|
| `pkg/ui/server.go` | 27-50 | `Model` DTO: drop `CredentialID`, add `Credentials []models.CredentialRef`. |
| `pkg/ui/server.go` | 380-405 (`handleModels` GET) | Hydrate `Credentials` from `mc.Credentials`. |
| `pkg/ui/server.go` | 411-471 (`handleModels` POST) | Accept and validate `Credentials` array; reject empty when internal, mixed-provider, weight<=0; remap to `models.ModelConfig{Credentials: ...}`. |
| `pkg/ui/server.go` | 511-588 (`handleModelDetail` PUT) | Same as POST. |
| `pkg/ui/server.go` | 612-669 (`handleValidateModel`) | Add per-entry credential validation; return error strings in the `errors` array. |
| `pkg/ui/frontend/src/types.ts` | 11-16, 103-126 | Add `CredentialRef` interface; add `Credentials: CredentialRef[]` to `Model`; remove `credential_id?: string`. |
| `pkg/ui/frontend/src/components/config/ModelForm.tsx` | 112-130 (`formData` initial state) | Replace `credential_id: string` with `credentials: CredentialRef[]`. |
| `pkg/ui/frontend/src/components/config/ModelForm.tsx` | 162-208 (`useEffect` hydrating from initialData) | Hydrate `credentials` from `initialData.credentials ?? []`; add legacy shim banner. |
| `pkg/ui/frontend/src/components/config/ModelForm.tsx` | 211-279 (`handleInputChange`) | Add a `handleCredentialRowChange(index, ref)` and `handleAddCredentialRow()` and `handleRemoveCredentialRow(index)` helpers. |
| `pkg/ui/frontend/src/components/config/ModelForm.tsx` | 282-311 (`handleSubmit`) | Build the payload from `formData.credentials`; drop legacy `credential_id`. Client-side validation: weight>0, no empty credential_id, length<=16. |
| `pkg/ui/frontend/src/components/config/ModelForm.tsx` | 319-358 (`handleTestConnection`) | When `credentials.length > 0`, send `credentials[0].credential_id` as the test target. |
| `pkg/ui/frontend/src/components/config/ModelForm.tsx` | 360-366 (`canTestConnection`) | Test against `credentials[0]` if present, else legacy form fields. |
| `pkg/ui/frontend/src/components/config/ModelForm.tsx` | (render block, near credential_id dropdown) | Replace the existing single-credential dropdown with `<MultiCredentialEditor>` (inline subcomponent or new file). |
| `pkg/ui/handlers_models_test.go` | (multiple) | Replace `credential_id: "X"` payloads with `credentials: [{credential_id: "X", weight: 1, position: 0}]`. |
| `README.md` (optional, only if a Models/Configuration section exists) | (locate) | Add a "Model credentials" subsection. |
| `pkg/ui/frontend/README.md` (optional, only if it exists) | (locate) | Note the multi-credential shape. |

## Tasks

| # | Task | Depends On | Acceptance |
|---|---|---|---|
| 1 | Update `pkg/ui/server.go`'s `Model` DTO: drop `CredentialID`, add `Credentials []models.CredentialRef` (with explicit `json:"credentials,omitempty"` tag and `bson:"-"` if any). | Phase 1 (`CredentialRef` exists in `pkg/models`) | `go build ./pkg/ui/...` clean; `json.Marshal(Model{Credentials: nil})` produces `{}` (omitempty honored). |
| 2 | In `handleModels` GET, hydrate `Credentials` from `mc.Credentials` (preserving order). | Task 1 | A `GET /fe/api/models` against a model with one credential returns `credentials: [{credential_id, weight, position}]` and no `credential_id` field. |
| 3 | Add server-side credential validation helper `validateCredentials(creds []CredentialRef, internal bool, existingCreds []CredentialConfig) []string` in `pkg/ui/server.go`. Rules: (a) if `internal && len(creds) == 0` → error; (b) each entry's `credential_id` must exist in `existingCreds`; (c) each entry's `weight > 0`; (d) all entries' providers must match the first entry's provider; (e) no duplicate `credential_id`. | Phase 1 (provider-match invariant lives in `ModelsManager.Validate` already) | Unit-tested in `pkg/ui/handlers_models_test.go` (Task 8). |
| 4 | Wire `validateCredentials` into `handleModels` POST, `handleModelDetail` PUT, and `handleValidateModel`. On error: HTTP 400 with `{error: "..."}` (POST/PUT) or `{valid: false, errors: [...]}` (validate). | Task 3 | All three endpoints reject the same set of invalid shapes; consistent error shape per endpoint. |
| 5 | Add `CredentialRef` interface to `pkg/ui/frontend/src/types.ts`; add `Credentials: CredentialRef[]` to `Model`; remove `credential_id?: string`. | Task 1 (DTO shape) | `tsc --noEmit` (or `pnpm tsc --noEmit`) clean. |
| 6 | Build the `<MultiCredentialEditor>` subcomponent in `ModelForm.tsx`: list of rows with `{credential_id, weight}` controlled inputs; + Add row button (disabled at 16); × remove button per row; provider-mismatch disabled state on the picker once `credentials[0]` is set. | Task 5 | The component renders inside the existing `Internal Upstream` panel; the form reflects row order (first row == primary). |
| 7 | Update `formData` initial state, hydration `useEffect`, `handleInputChange` / submit, and Test Connection button to use `credentials` instead of `credential_id`. Client-side weight/duplicate validation in `handleSubmit` mirrors server validation. | Task 6 | Submitting a form with `weight=0` shows a toast error before the network request; submitting with `credentials=[]` and `internal=true` shows the same. |
| 8 | Add coverage in `pkg/ui/handlers_models_test.go` for: empty + internal → 400; mixed-provider → 400; weight=0 → 400; legacy `credential_id` field silently ignored on POST/PUT (no error); valid POST with `credentials: [A, B]` returns 201 with the array in the body. | Tasks 1, 4 | `go test ./pkg/ui/...` passes; new test names cover each branch. |
| 9 | Update any UI snapshot/Playwright tests that pin the `credential_id` field to use `credentials` instead. | Task 7 | `pnpm test` (or equivalent) passes; no test references `credential_id` anymore. |
| 10 | Optional: update `README.md` and/or `pkg/ui/frontend/README.md` with the new shape. Skip if no existing docs to extend. | Task 1 | New JSON example visible in docs (if docs exist). |

## Coupling

- **Tight with Phase 1 (DB/store)**: The wire shape `[{credential_id, weight, position}]` is dictated by `models.CredentialRef`. If Phase 1 changes the field names, Phase 4 must follow.
- **Tight with Phase 3 (handler wiring)**: Phase 3 calls `ResolveInternalConfigWithAffinity(modelID, conversationKey)`; the API consumer (the proxy) reads from `models.Credentials`. UI changes that affect what the engine sees (e.g., accidentally posting a `credential_id` field that the backend silently drops) would break the feature. Phase 4 must NOT keep the `credential_id` field on the wire — it is dropped per the contract.
- **Loose with Phase 5 (tests)**: Phase 5 will add E2E tests that POST to `/fe/api/models` with multi-credential payloads and verify backend persistence. Phase 4 must leave the API surface stable enough for those tests to be written.
- **Independent** of the engine internals — UI does not import `pkg/credentiallb`.

### Coupling Matrix

| | Phase 1 | Phase 2 | Phase 3 | Phase 4 | Phase 5 |
|---|---|---|---|---|---|
| Phase 1 | — | tight | tight | tight (DTO shape) | tight (backfill) |
| Phase 2 | tight | — | tight | independent | tight |
| Phase 3 | tight | tight | — | loose (wire shape) | loose |
| Phase 4 | tight | independent | loose | — | tight (UI/E2E tests) |
| Phase 5 | tight | tight | loose | tight | — |

## Risks

| # | Risk | Impact | Likelihood | Mitigation |
|---|---|---|---|---|
| 1 | Mixed-provider credentials save via the UI and silently route to the wrong upstream at request time | High — wrong API key per credential, all requests fail | Low (server-side validator in Phase 1 already rejects this; Phase 4 calls it via `validateCredentials`) | `validateCredentials` enforces same-provider; client-side picker also disables non-matching providers once `credentials[0]` is set. Unit test in Task 8. |
| 2 | Operator adds the same credential twice (duplicate `credential_id`) — the engine would build a selector with that entry twice and weighted-random would pick it more often than intended | Medium — silent weighting bug | Low (Phase 1's `ModelsManager.Validate` rejects duplicates; Phase 4 surfaces the error) | `validateCredentials` includes the duplicate check; client-side form prevents adding the same `credential_id` twice (greys out already-selected credentials in the picker). Unit test in Task 8. |
| 3 | The frontend sends stale `credential_id` (legacy field) on POST and the backend silently drops it; the user thinks their save "didn't take" | Medium — UX confusion, no data loss (Phase 1 migration guarantees one entry) | Low (Task 7 explicitly drops the field; legacy shim banner warns the user) | Banner: "Detected legacy `credential_id` field — saving will migrate to the new `credentials` shape." Documented in commit message. |
| 4 | The provider picker in the form is not filtered by provider when `credentials[0]` is set, allowing the user to pick a different provider and only learn on save | Low — caught by server-side validator with a clear error | Medium | Task 6 disables non-matching providers once `credentials[0]` is set, with a tooltip. Saves a round trip. |
| 5 | The `MultiCredentialEditor` grows past 200 lines and bloats `ModelForm.tsx` (already 757 lines) | Low — maintainability | Medium | Optional Task 6 sub-decision: factor into `MultiCredentialEditor.tsx` if it exceeds ~120 lines. The plan leaves this to the implementer; not a blocking risk. |
| 6 | The "Test Connection" button continues to test only `credentials[0]` and the user wonders if all credentials were tested | Low — UX clarity | Low | Tooltip on the button: "Tests against the primary credential only." No multi-cred test in v1. |
| 7 | The position field is server-managed but the frontend doesn't send it, so the server assigns position=slice index — if a user reorders rows, positions shift on save | Low — positions are an internal tiebreaker | Low | Server-side: in `validateCredentials`, after accepting the array, walk it and stamp `position` as the slice index. Documented in Phase 1 plan-overview. |
| 8 | Adding the multi-credential editor to `ModelForm.tsx` blows up its render block, requiring careful hook ordering to keep `useProviders`, `useCredentials`, and the new `useState` arrays stable across renders | Low — UI jank | Low | Use a single `useState<CredentialRef[]>` for the credentials list; mutate via immutable updates (matches existing `handleInputChange` style). Phase 5 visual smoke test verifies no re-render thrash. |

## Exit Criterion
Phase 4 is complete when:
1. `go build ./...` and `go vet ./...` pass with zero warnings.
2. `pnpm tsc --noEmit` (or the equivalent type-check command in `pkg/ui/frontend/`) passes with zero errors.
3. `go test ./pkg/ui/...` passes — including the new coverage in `pkg/ui/handlers_models_test.go` (Task 8).
4. Manual smoke: an operator can attach 2 credentials to a model via the UI, save, reload, and see both rows preserved in the correct order. Saving with weight=0 or a mixed-provider credential is rejected with a clear error toast.
5. **#9 — REVISED 2026-08-21 (reviewer pass):** The legacy `credential_id` field is gone from the **Model DTO + model CRUD response handlers only**. The exact grep + expected result (scoped per the reviewer punch-list):
   ```bash
   # Scoped grep: matches the Model DTO and model CRUD response handlers, NOT TestModelRequest.
   grep -n '"credential_id"' pkg/ui/server.go \
       | grep -v 'TestModelRequest' \
       | grep -v 'credential_id is required' \
       | grep -v 'look up the credential'
   ```
   Expected: **ZERO** matches. The only remaining `"credential_id"` string-literal occurrences in `pkg/ui/server.go` are inside the `TestModelRequest` struct (`pkg/ui/server.go:673` — legitimately carries `credential_id` for the operator-driven connection test) and the test-endpoint error/lookup strings (`"credential_id is required"`, `"look up the credential"` — both in `handleTestModel`, also legitimately test-only). The DTO at line ~35 (`Model` struct's `CredentialID string \`json:"credential_id,omitempty"\``) MUST be removed. The model CRUD response handlers (`handleModels` GET, `handleModels` POST, `handleModelDetail` PUT, `handleValidateModel`) MUST emit `credentials: [{credential_id, weight, position}]` and never emit a top-level `credential_id` field. The field appears only inside `credentials[i].credential_id`.
6. The backend validator returns 400 for: empty `credentials` with `internal=true`; mixed-provider entries; weight=0; duplicate `credential_id`.
7. `pnpm test` (or the frontend test suite) passes; no tests reference the dropped top-level `credential_id` field on the Model DTO.

## Amendment Changelog

> **AMENDED 2026-08-21 (leader rulings, aggregator-applied):** The leader's amendment cycle
> (A-1, A-2, M-1, W-1/2/3, E-1..4, PG-test mandate) required NO task/file/exit-criterion changes
> in Phase 4 — verified by grep (no `CredEngine`, no engine consumption, no key computation, no
> migration content). Two narrative inaccuracies were corrected in place:

| # | Ruling | What changed in this file |
|---|--------|---------------------------|
| M-1 | Derived-shadow retention | Objective (line 4): "gone from the API (the migration drops it in Phase 1)" → "gone from the API wire (the DB column is retained as a derived shadow per M-1)". The DB column survives until migration 029; only the JSON wire shape drops it. |
| M-1 | Single source of truth restated | Context (line 9): "the `credential_id` field is DROPPED — no dual-source-of-truth" → decisions.md §B as amended: `credentials[]` is the single application source of truth; `models.credential_id` is a derived shadow (`= credentials[0].credential_id`) for legacy/external readers until 029. |

No other rulings touched Phase 4's scope. UI tasks, files table, validation rules, and exit
criteria are unchanged.

---

## Round 2 — Reviewer Punch-List (2026-08-21)

> **REVISED 2026-08-21 (reviewer pass):** The single reviewer-punch-list
> item that touches Phase 4 (#9) is the scope of the legacy-`credential_id`
> removal grep in the Exit Criterion. All other items are upstream
> (Phase 1 / Phase 3) — they propagate to Phase 4 only by sharing the
> wire shape (`credentials: [...]` array).

| # | Item | Section(s) updated | One-line summary |
|---|------|---------------------|------------------|
| **#9** | Scope the legacy-`credential_id` grep to the Model DTO + model CRUD response handlers; exempt `TestModelRequest` (and the test-endpoint error/lookup strings) | Exit Criterion #5 (REVISED — was over-broad; now scoped + exact grep stated) | The previous grep `grep -rn '"credential_id"' pkg/ui/server.go` was over-broad — it returned `TestModelRequest.CredentialID` (`pkg/ui/server.go:673`) which legitimately carries `credential_id` for the operator-driven connection test, plus the test-endpoint error/lookup strings (`"credential_id is required"`, `"look up the credential"`) in `handleTestModel`. The revised grep excludes those exact patterns; expected result is ZERO matches. The Model DTO (`pkg/ui/server.go` ~line 35 — `Model` struct's `CredentialID string \`json:"credential_id,omitempty"\``) MUST be removed; the model CRUD response handlers MUST emit `credentials: [...]` only. The grep is the Phase 4 merge gate. |
