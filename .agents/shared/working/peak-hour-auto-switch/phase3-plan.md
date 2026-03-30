# Phase 3: Frontend Peak Hour UI

## Objective
Add a peak hour configuration section to the model form in the Preact frontend, with time inputs, timezone selector, toggle switch, and a live status indicator showing whether the model is currently in peak hour mode (computed UTC-based).

## Context
- Phase 2 delivered: API endpoints accept/return peak hour fields, proxy resolves peak-hour models
- Frontend uses Preact + Vite + Tailwind CSS
- `ModelForm.tsx` has conditional rendering pattern: `{formData.internal && (...)}` for internal fields
- Form uses `handleInputChange(field, value)` for state updates
- `onSave` handler conditionally includes fields following the existing pattern (see S1)
- TypeScript interfaces in `types.ts` define the `Model` type
- API hooks in `useApi.ts` use full-object merge pattern — no changes needed

## UTC Offset Format Convention
- **Stored/sent**: `"+7"`, `"-5"`, `"+5.5"` (signed decimal string)
- **Displayed**: `UTC+7`, `UTC-5`, `UTC+5:30` (label in dropdown)
- **Dropdown value**: `"+7"`, **Dropdown label**: `UTC+7`
- **Conversion**: `label = "UTC" + value.replace(".5", ":30").replace(".25", ":15").replace(".75", ":45")`

## Timezone Selector Options (~30 options)
Hourly offsets: UTC-12 through UTC+14 (25 options)
Half-hour offsets: UTC+5:30, UTC+5:45, UTC+6:30, UTC+9:30, UTC-9:30 (5 options)

## UI Design

### Form Section Layout
The peak hour section appears **inside** the internal upstream section (only when `internal: true`), after the "Upstream Model Name" field:

```
┌─ Internal Upstream ──────────────────────────────┐
│ ☑ Internal Upstream                               │
│                                                    │
│  [Credential: ...▼]  [API Key: ...]               │
│  [Base URL: ...]                                   │
│  [Upstream Model Name: gpt-4o]                    │
│                                                    │
│  ┌─ Peak Hour Auto-Switch ─────────────────────┐  │
│  │ ☑ Enable Peak Hour Switch                    │  │
│  │                                              │  │
│  │  Peak Hour Window                            │  │
│  │  [Start: 13:00] — [End: 18:00]              │  │
│  │                                              │  │
│  │  Timezone: [UTC+7 ▼]                        │  │
│  │                                              │  │
│  │  Peak Hour Model Name                        │  │
│  │  [gpt-4o-mini]                               │  │
│  │                                              │  │
│  │  ● Currently using: gpt-4o (off-peak)       │  │
│  └──────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────┘
```

### Live Status Indicator (UTC-based)
To avoid client clock issues, compute status using UTC:
```typescript
function isPeakHourActive(start: string, end: string, offset: string): boolean {
    const now = new Date();
    const offsetHours = parseFloat(offset) || 0;
    // Convert current UTC time + offset to get local time in configured timezone
    const localMinutes = (now.getUTCHours() * 60 + now.getUTCMinutes()) + offsetHours * 60;
    const currentMinutes = ((localMinutes % 1440) + 1440) % 1440; // Normalize to 0-1439

    const [startH, startM] = start.split(':').map(Number);
    const [endH, endM] = end.split(':').map(Number);
    const startMinutes = startH * 60 + startM;
    const endMinutes = endH * 60 + endM;

    if (startMinutes < endMinutes) {
        return currentMinutes >= startMinutes && currentMinutes < endMinutes;
    }
    return currentMinutes >= startMinutes || currentMinutes < endMinutes;
}
```

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Update TypeScript `Model` interface | Add 5 peak hour fields: `peak_hour_enabled?: boolean`, `peak_hour_start?: string`, `peak_hour_end?: string`, `peak_hour_timezone?: string`, `peak_hour_model?: string` | `pkg/ui/frontend/src/types.ts` |
| 2 | Add peak hour form state & defaults | Extend `formData` initial values in both add and edit modes. Follow existing pattern for clearing dependent fields when toggle changes (see S1): when `peak_hour_enabled` is toggled OFF, clear all peak-hour fields | `pkg/ui/frontend/src/components/config/ModelForm.tsx` |
| 3 | Build peak hour toggle section | Checkbox "Enable Peak Hour Switch" — only visible when `internal: true`. Toggles `peak_hour_enabled`. When toggled off, clear `peak_hour_start`, `peak_hour_end`, `peak_hour_timezone`, `peak_hour_model` | `pkg/ui/frontend/src/components/config/ModelForm.tsx` |
| 4 | Build time window inputs | Two `<input type="time">` for start/end. Native browser time picker handles HH:MM format. Only shown when `peak_hour_enabled: true` | `pkg/ui/frontend/src/components/config/ModelForm.tsx` |
| 5 | Build timezone selector | `<select>` dropdown with ~30 UTC offset options. Value is stored format (`"+7"`), label is display format (`UTC+7`). Default: `"+0"` | `pkg/ui/frontend/src/components/config/ModelForm.tsx` |
| 6 | Build peak hour model name input | Text input for the alternative upstream model name. Required when peak hour enabled. Only shown when `peak_hour_enabled: true` | `pkg/ui/frontend/src/components/config/ModelForm.tsx` |
| 7 | Build live status indicator | Compute current status using UTC-based calculation. Show: "Currently using: {model} (peak hour 🔴)" or "Currently using: {model} (off-peak ⚫)". Only when peak hour enabled and fully configured | `pkg/ui/frontend/src/components/config/ModelForm.tsx` |
| 8 | Update `onSave` payload | Include peak hour fields in save handler (conditional on `internal: true`), clear fields when peak_hour_enabled is false. Follow existing pattern: `peak_hour_start: formData.internal && formData.peak_hour_enabled ? formData.peak_hour_start : undefined` | `pkg/ui/frontend/src/components/config/ModelForm.tsx` |
| 9 | Add form validation | Peak hour enabled → require start, end, timezone, model name. Show validation errors inline. Prevent save when invalid | `pkg/ui/frontend/src/components/config/ModelForm.tsx` |

## Key Files
- `pkg/ui/frontend/src/types.ts` — TypeScript Model interface
- `pkg/ui/frontend/src/components/config/ModelForm.tsx` — Main form component (all tasks)
- `pkg/ui/frontend/src/hooks/useApi.ts` — No changes needed (full-object merge handles new fields)

## Styling Patterns (from existing code)
- Container: `class="bg-gray-800/50 rounded-md p-4 space-y-3 border border-gray-600/50"`
- Labels: `class="block text-sm text-gray-400 mb-1"`
- Inputs: `class="w-full bg-gray-700 border border-gray-600 rounded px-3 py-2 text-white"`
- Section title: `class="text-gray-300 font-medium"`
- Toggle: `<input type="checkbox">` with label

## Clearing Dependent Fields Pattern (S1)
Follow the existing pattern from `ModelForm.tsx` (line 126-130) where toggling "Internal Upstream" off clears dependent fields:
```typescript
// When peak_hour_enabled is toggled OFF, clear all peak-hour fields:
handleInputChange('internal', checked);
if (!checked) {
    // Clear internal fields (existing pattern)
    handleInputChange('credential_id', '');
    handleInputChange('internal_model', '');
    // Clear peak hour fields (new)
    handleInputChange('peak_hour_enabled', false);
    handleInputChange('peak_hour_start', '');
    handleInputChange('peak_hour_end', '');
    handleInputChange('peak_hour_timezone', '');
    handleInputChange('peak_hour_model', '');
}
```

## Constraints
- Peak hour section only renders when `formData.internal === true`
- All peak hour fields cleared/omitted when `peak_hour_enabled` is false (S1 pattern)
- Form must pre-populate correctly when editing an existing model with peak hour config
- Live status indicator uses **UTC-based** calculation, not local client time (W2)
- No changes needed to `useApi.ts` — full-object merge pattern handles new fields automatically
- No changes needed to `ModelsTab.tsx` — list view doesn't need peak hour display
- Timezone dropdown limited to ~30 standard offsets (W5), not all 53 half-hour increments

## Deliverables
- [ ] TypeScript `Model` interface extended with 5 peak hour fields
- [ ] Peak hour toggle checkbox in model form (conditional on internal)
- [ ] Time window inputs (start/end) using native `<input type="time">`
- [ ] Timezone selector with ~30 UTC offset options
- [ ] Peak hour model name text input
- [ ] Live UTC-based status indicator showing current active model
- [ ] Form validation for required peak hour fields
- [ ] Save payload correctly includes/omits peak hour fields
- [ ] Edit mode correctly pre-populates existing peak hour config
- [ ] Dependent fields cleared when toggle changes (following existing pattern)
