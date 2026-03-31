# Phase 3: Frontend Visualization

## Objective
Add a new "Usage" tab to the Settings page that displays token-level hourly request usage with a table/chart, token selector, and date range picker.

## Context
- **Previous Phase:** Phase 2 completed — API endpoints available at `/fe/api/usage/*`, Server has `dbStore`
- **Key Decisions:**
  - Reuse existing component patterns (Tailwind dark theme, tab-based layout)
  - Use HTML table with CSS bar visualization instead of a heavy chart library
  - Add to existing Settings tabs alongside Proxy, Models, Credentials, etc.

## Tasks

### 1 — Add TypeScript types for usage data
**Why:** Type safety for API responses.

| Detail | Value |
|--------|-------|
| File | `pkg/ui/frontend/src/types.ts` |

**New types:**
```typescript
export interface HourlyUsageRow {
  hour_bucket: string;
  request_count: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
}

export interface UsageResponse {
  token_id?: string;
  from: string;
  to: string;
  view: 'hourly' | 'daily';
  data: HourlyUsageRow[];
  totals: HourlyUsageRow;
}

export interface UsageToken {
  token_id: string;
  name: string;
}

export interface UsageSummary {
  from: string;
  to: string;
  tokens: {
    token_id: string;
    name: string;
    total_requests: number;
    total_prompt_tokens: number;
    total_completion_tokens: number;
    total_tokens: number;
  }[];
  grand_total: {
    total_requests: number;
    total_prompt_tokens: number;
    total_completion_tokens: number;
    total_tokens: number;
  };
}
```

### 2 — Add API hook for usage data
**Why:** Follow existing `useApi.ts` pattern for data fetching.

| Detail | Value |
|--------|-------|
| File | `pkg/ui/frontend/src/hooks/useApi.ts` |

**New function `useUsage()`:**
```typescript
export function useUsage() {
  const [usageData, setUsageData] = useState<UsageResponse | null>(null);
  const [usageTokens, setUsageTokens] = useState<UsageToken[]>([]);
  const [summary, setSummary] = useState<UsageSummary | null>(null);
  const [loading, setLoading] = useState(false);

  const fetchUsage = useCallback(async (params: {
    token_id?: string;
    from?: string;
    to?: string;
    view?: 'hourly' | 'daily';
  }) => {
    setLoading(true);
    const qs = new URLSearchParams();
    if (params.token_id) qs.set('token_id', params.token_id);
    if (params.from) qs.set('from', params.from);
    if (params.to) qs.set('to', params.to);
    if (params.view) qs.set('view', params.view);
    const data = await apiFetch<UsageResponse>(`/usage?${qs.toString()}`);
    setUsageData(data);
    setLoading(false);
    return data;
  }, []);

  const fetchTokens = useCallback(async () => {
    const data = await apiFetch<{ tokens: UsageToken[] }>('/usage/tokens');
    setUsageTokens(data.tokens || []);
  }, []);

  const fetchSummary = useCallback(async (from?: string, to?: string) => {
    const qs = new URLSearchParams();
    if (from) qs.set('from', from);
    if (to) qs.set('to', to);
    const data = await apiFetch<UsageSummary>(`/usage/summary?${qs.toString()}`);
    setSummary(data);
    return data;
  }, []);

  useEffect(() => { fetchTokens(); }, [fetchTokens]);

  return { usageData, usageTokens, summary, loading, fetchUsage, fetchTokens, fetchSummary };
}
```

### 3 — Create UsageTab component
**Why:** Main container component for the usage view.

| Detail | Value |
|--------|-------|
| File | `pkg/ui/frontend/src/components/usage/UsageTab.tsx` (new file) |

**Layout:**
```
┌──────────────────────────────────────────────────────────┐
│  [Token Selector ▾]  [From: date]  [To: date]  [Search] │
├──────────────────────────────────────────────────────────┤
│                                                          │
│  Summary Cards                                           │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐   │
│  │ Requests │ │ Prompt   │ │Complete  │ │ Total    │   │
│  │   1,234  │ │ 500K tok │ │ 200K tok │ │ 700K tok │   │
│  └──────────┘ └──────────┘ └──────────┘ └──────────┘   │
│                                                          │
│  Hourly Breakdown Table                                  │
│  ┌─────────────┬─────────┬────────┬──────────┬───────┐  │
│  │ Hour        │ Requests│ Prompt │ Complete │ Total │  │
│  ├─────────────┼─────────┼────────┼──────────┼───────┤  │
│  │ 2026-03-30  │ 42      │ 15,000 │ 8,000    │23,000 │  │
│  │   14:00     │ ██████  │        │          │       │  │
│  ├─────────────┼─────────┼────────┼──────────┼───────┤  │
│  │ 2026-03-30  │ 38      │ 12,000 │ 6,000    │18,000 │  │
│  │   15:00     │ █████   │        │          │       │  │
│  └─────────────┴─────────┴────────┴──────────┴───────┘  │
│                                                          │
│  [View: Hourly ▾] [Export CSV]                          │
└──────────────────────────────────────────────────────────┘
```

### 4 — Create UsageTable sub-component
**Why:** Reusable table for hourly/daily breakdown with inline bar charts.

| Detail | Value |
|--------|-------|
| File | `pkg/ui/frontend/src/components/usage/UsageTable.tsx` (new file) |

**Features:**
- Sortable columns (by hour, request count, tokens)
- Inline CSS bar charts showing relative magnitude
- Responsive design (scrollable on small screens)
- Toggle between hourly and daily view

### 5 — Create UsageSummaryCards sub-component
**Why:** Quick-glance summary metrics.

| Detail | Value |
|--------|-------|
| File | `pkg/ui/frontend/src/components/usage/UsageSummaryCards.tsx` (new file) |

**Cards:**
- Total Requests (number)
- Prompt Tokens (formatted: 1.5K, 2.3M)
- Completion Tokens (formatted)
- Total Tokens (formatted)

### 6 — Add "Usage" tab to SettingsPage
**Why:** Integrate into existing settings navigation.

| Detail | Value |
|--------|-------|
| File | `pkg/ui/frontend/src/components/SettingsPage.tsx` |

**Changes:**
1. Update `TabType` union: add `'usage'`
2. Add tab button with icon (📊 or similar)
3. Add conditional render block for UsageTab component

```tsx
// Update type
type TabType = 'proxy' | 'models' | 'credentials' | 'loop_detection' | 'tool_repair' | 'tokens' | 'usage';

// Add tab button (after tokens)
<button
  onClick={() => setActiveTab('usage')}
  className={activeTab === 'usage' ? 'active' : ''}
>
  📊 Usage
</button>

// Add content block
{activeTab === 'usage' && <UsageTab />}
```

### 7 — Add number formatting utilities
**Why:** Display large token counts in human-readable format.

| Detail | Value |
|--------|-------|
| File | `pkg/ui/frontend/src/utils/helpers.ts` |

```typescript
export function formatTokenCount(n: number): string {
  if (n >= 1_000_000_000) return (n / 1_000_000_000).toFixed(1) + 'B';
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M';
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K';
  return n.toString();
}

export function formatHourBucket(bucket: string): string {
  // "2026-03-30T14" → "Mar 30, 14:00"
  return bucket.replace(/(\d{4})-(\d{2})-(\d{2})T(\d{2})/, (_, y, m, d, h) => {
    const months = ['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];
    return `${months[parseInt(m)-1]} ${parseInt(d)}, ${h}:00`;
  });
}
```

## Key Files
- `pkg/ui/frontend/src/types.ts` — New TypeScript interfaces
- `pkg/ui/frontend/src/hooks/useApi.ts` — New `useUsage()` hook
- `pkg/ui/frontend/src/components/usage/UsageTab.tsx` — Main usage container (new)
- `pkg/ui/frontend/src/components/usage/UsageTable.tsx` — Hourly table (new)
- `pkg/ui/frontend/src/components/usage/UsageSummaryCards.tsx` — Summary cards (new)
- `pkg/ui/frontend/src/components/SettingsPage.tsx` — Tab integration
- `pkg/ui/frontend/src/utils/helpers.ts` — Formatting utilities

## Constraints
- **No new dependencies** — Use existing Preact + Tailwind stack
- **Dark theme** — Match existing dashboard styling
- **Responsive** — Must work on smaller screens
- **Default view** — Show last 24 hours for all tokens when tab opens
- **Performance** — Don't refetch on every render; use caching in the hook

## Deliverables
- [ ] "Usage" tab visible in Settings page
- [ ] Token selector populated from API
- [ ] Date range picker with reasonable defaults (last 24h)
- [ ] Summary cards show aggregated totals
- [ ] Table shows hourly/daily breakdown with visual bars
- [ ] Toggle between hourly and daily view
- [ ] Loading states and error handling
- [ ] Mobile-responsive layout
