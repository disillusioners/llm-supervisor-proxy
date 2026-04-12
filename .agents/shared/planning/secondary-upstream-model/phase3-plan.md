# Phase 3: Frontend UI for Secondary Upstream Model

## Objective
Add UI for users to configure the secondary upstream model per model entry. Should be clear, optional, and visually distinct from both the primary upstream model and the fallback model.

## Coupling
- **Depends on**: Phase 1 (needs `secondary_upstream_model` field in API response)
- **Coupling type**: loose — only depends on API contract (field name and behavior)
- **Shared files with other phases**: None (frontend files are separate)
- **Shared APIs/interfaces**: Model CRUD API endpoints (`/fe/api/models`)
- **Why this coupling**: FE reads/writes the field that Phase 1 adds to the data model

## Context
Phase 1 delivered: The `/fe/api/models` API now returns `secondary_upstream_model` as part of the Model JSON and accepts it on create/update.

Current ModelForm layout:
```
Root container
├── Model ID (add-mode only)
├── Display Name
├── Fallback Chain
├── Strip Params
├── ☑ Internal Upstream
│   ├── Credential select
│   ├── API Key Override
│   ├── Base URL Override
│   ├── Upstream Model Name          ← Primary upstream model
│   ├── ── Peak Hour Auto-Switch ──
│   │   ├── ☑ Enable Peak Hour Switch
│   │   └── Peak Hour Model Name     ← Peak hour model
│   └── Test Connection button
└── [Cancel] [Save] buttons
```

## Design Decision: Where to Place the Field

**Chosen position:** Inside the Internal Upstream section, AFTER "Upstream Model Name" and BEFORE the Peak Hour section. This follows the natural hierarchy:

1. Upstream Model Name (primary)
2. Secondary Upstream Model (for retries) ← NEW
3. Peak Hour section (time-based override)

Visual theme: **Purple/indigo** focus ring to distinguish from:
- Blue (internal upstream section)
- Amber (peak hour section)
- Gray (base form)

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Update TypeScript Model interface | Add `secondary_upstream_model?: string` to `Model` interface | `pkg/ui/frontend/src/types.ts` |
| 2 | Add `race_secondary_model_used` to EventType union | Add the new event type to the `EventType` union in types.ts (used by event bus for tracking secondary model usage) | `pkg/ui/frontend/src/types.ts` |
| 3 | Update ModelForm state | Add `secondary_upstream_model: ''` to formData initial state | `pkg/ui/frontend/src/components/config/ModelForm.tsx` |
| 4 | Update ModelForm save handler | Include `secondary_upstream_model` in the onSave payload (only when internal=true and non-empty) | `pkg/ui/frontend/src/components/config/ModelForm.tsx` |
| 5 | Add form field JSX | Add text input after "Upstream Model Name" with label "Secondary Upstream Model" and purple-themed styling | `pkg/ui/frontend/src/components/config/ModelForm.tsx` |
| 6 | Add helper text | Explain the field: "Model to use for retries (e.g., faster/cheaper model). Leave empty to retry with same model." | `pkg/ui/frontend/src/components/config/ModelForm.tsx` |
| 7 | Clear field when internal toggled off | In `handleInputChange`, clear `secondary_upstream_model` when `internal` is set to false | `pkg/ui/frontend/src/components/config/ModelForm.tsx` |
| 8 | Update ModelsTab display | Show secondary upstream model badge AFTER the internal upstream model badge (same row, same pattern as existing model name display) | `pkg/ui/frontend/src/components/config/ModelsTab.tsx` |

## Key Files
- `pkg/ui/frontend/src/types.ts` — Model interface
- `pkg/ui/frontend/src/components/config/ModelForm.tsx` — Form UI
- `pkg/ui/frontend/src/components/config/ModelsTab.tsx` — List display

## Detailed Implementation Notes

### Task 1: TypeScript types
```typescript
// In Model interface, add after internal_model:
secondary_upstream_model?: string;  // Alternative upstream model for retries
```

### Task 2: EventType union
```typescript
// In EventType union, add after 'race_all_failed':
| 'race_secondary_model_used'
```

### Task 2: Form state
```typescript
// In useState formData:
secondary_upstream_model: '',

// In useEffect for edit mode:
secondary_upstream_model: initialData.secondary_upstream_model ?? '',
```

### Task 3: Save handler
```typescript
// In handleSubmit → onSave payload:
secondary_upstream_model: formData.internal && formData.secondary_upstream_model 
    ? formData.secondary_upstream_model 
    : undefined,
```

### Task 4: Form field JSX
```tsx
{/* Secondary Upstream Model */}
<div>
  <label class="block text-sm font-medium text-gray-300 mb-1">
    Secondary Upstream Model <span class="text-gray-500">(optional)</span>
  </label>
  <input
    type="text"
    value={formData.secondary_upstream_model}
    onInput={(e) => handleInputChange('secondary_upstream_model', (e.target as HTMLInputElement).value)}
    class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-500 
           focus:outline-none focus:ring-2 focus:ring-purple-500 focus:border-transparent transition-shadow"
    placeholder={selectedProvider === 'openai' ? 'gpt-4o-mini' : selectedProvider === 'zhipu' ? 'glm-4-flash' : ''}
  />
  <p class="text-xs text-gray-400 mt-1">
    Upstream model to use for retries when the primary fails (e.g., faster/cheaper model). 
    Leave empty to retry with the same model.
  </p>
</div>
```

### Task 6: Clear on internal toggle
In `handleInputChange`, inside the `field === 'internal' && value === false` block, add:
```typescript
updated.secondary_upstream_model = '';
```

### Task 8: ModelsTab badge
In the model list display, when a model has `secondary_upstream_model` configured, show a small badge AFTER the existing internal upstream model badge (same row, same visual pattern as the model name display):
```tsx
{/* Existing internal model badge */}
{model.internal && model.internal_model && (
  <span class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-blue-900/50 text-blue-300 border border-blue-700">
    {model.internal_model}
  </span>
)}
{/* NEW: Secondary upstream badge — placed right after the model name badge */}
{model.internal && model.secondary_upstream_model && (
  <span class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium 
               bg-purple-900/50 text-purple-300 border border-purple-700 ml-2">
    ↻ {model.secondary_upstream_model}
  </span>
)}
```

## UX Considerations

### Visual Hierarchy (nested levels with distinct colors)
| Level | Theme Color | Focus Ring | Section |
|-------|-----------|------------|---------|
| Base form | Gray | `ring-blue-500` | Model ID, Name, Fallback |
| Internal upstream | Dark gray bg | `ring-blue-500` | Credential, Upstream Model |
| Secondary upstream | Same as internal | `ring-purple-500` | Secondary Upstream Model |
| Peak hour | Darker bg | `ring-amber-500` | Time window, Peak Model |

### The form section for "Internal Upstream" will look like:
```
☑ Internal Upstream
┌── bg-gray-800/50 ──────────────────────────────────────────┐
│  Credential: [select ▼]                                      │
│  API Key Override: [password]                                │
│  Base URL Override: [text]                                   │
│                                                              │
│  Upstream Model Name *: [text]          ← Blue focus        │
│    "Actual model name at the provider"                       │
│                                                              │
│  Secondary Upstream Model: [text]       ← Purple focus      │
│    "Model to use for retries. Leave empty for same model."   │
│                                                              │
│  ── Peak Hour Auto-Switch ──────────────────────────────     │
│  │ ☑ Enable Peak Hour Switch                              │  │
│  │ ...                                                    │  │
│  └────────────────────────────────────────────────────────┘  │
│                                                              │
│  [Test Connection]                                           │
└──────────────────────────────────────────────────────────────┘
```

## Constraints
- Only shown when `internal=true`
- Optional field — empty means "use same model for retry"
- Must be visually distinguishable from primary upstream and fallback
- Must not affect fallback chain UI (completely separate concept)

## Deliverables
- [ ] TypeScript Model type has `secondary_upstream_model` field
- [ ] `race_secondary_model_used` added to EventType union
- [ ] ModelForm has input for secondary upstream model
- [ ] Field is properly saved and loaded
- [ ] Field is cleared when internal is toggled off
- [ ] ModelsTab shows badge after internal model badge for models with secondary configured
- [ ] Visual theme is distinct (purple)
