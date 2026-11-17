import { useState } from 'preact/hooks';
import type { Model } from '../../types';
import { DecisionModelsTab } from './DecisionModelsTab';
import { GroupModelsTab } from './GroupModelsTab';

// The container owns the VERTICAL LEFT NAV and the child-tab state.
// It has NO page-level heading of its own: each child panel owns its
// own heading (the models panel says "Decision Models", the group
// panel says "Decision Groups") — that is what lets DecisionModelsTab
// stay BYTE-UNCHANGED (wrap, never refactor).
type ChildTab = 'models' | 'group';

interface DecisionModelsContainerProps {
  // ── The models panel — every prop is passed through VERBATIM ────────
  // The container is a pass-through, not a filter: onNavigateToCredentials,
  // fetchError, onRetry and the four mutations reach DecisionModelsTab
  // exactly as the parent handed them over.
  models: Model[];
  onAddModel: (model: Omit<Model, 'id'> & { id: string }) => Promise<void>;
  onUpdateModel: (id: string, updates: Partial<Model>) => Promise<void>;
  onDeleteModel: (id: string) => Promise<void>;
  onToggleModel: (model: Model) => Promise<void>;
  setStatus: (status: { type: 'success' | 'error'; message: string; restartRequired?: boolean } | null) => void;
  onNavigateToCredentials?: () => void;
  fetchError?: string | null;
  onRetry?: () => void;

  // ── The group panel — the DECISION lane's group surface ─────────────
  // `groups` is the parent-scoped, decision-lane group list (the
  // client-side lane split lives in the parent; the panel trusts what
  // it is given). `laneModels` is the decision lane's model feed — the
  // server-prefiltered ?kind=decision list.
  groups: Model[];
  laneModels: Model[];
  kindIndex?: Map<string, string>;
  onAddGroupModel: (model: Omit<Model, 'id'> & { id: string }) => Promise<void>;
  onUpdateGroupModel: (id: string, updates: Partial<Model>) => Promise<void>;
  onDeleteGroupModel: (id: string) => Promise<void>;
  onToggleGroupModel: (model: Model) => Promise<void>;
  groupFetchError?: string | null;
  groupOnRetry?: () => void;
}

// Nav labels — Title Case, matching every other label in the app
// (Proxy Settings / Models / ImgGen Models / Decision Models / Group
// Models). The accessible names are unambiguous against the top-level
// "Models" and "Group Models" tabs: aria-label="Decision Models" /
// aria-label="Decision Groups".
const NAV_ITEMS: { child: ChildTab; label: string; ariaLabel: string; testId: string }[] = [
  {
    child: 'models',
    label: 'Models',
    ariaLabel: 'Decision Models',
    testId: 'decision-container-tab-models',
  },
  {
    child: 'group',
    label: 'Group',
    ariaLabel: 'Decision Groups',
    testId: 'decision-container-tab-group',
  },
];

// The active/inactive class pair is the top bar's pair
// (SettingsPage.tsx) TRANSLATED from the bottom edge to the left edge:
// `border-b-2 border-blue-400` → `border-l-2 border-l-blue-400`.
// `border-l-transparent` on the inactive item keeps the geometry
// constant, so activating a nav item does not shift the labels.
//
// The nav is the FE's FIRST nested-tab / vertical nav — everything
// else (the colour pair, the weight, the transition, the hover, the
// typography scale, the border-r divider) is lifted from an existing
// site.
const ACTIVE_CLASS =
  'px-3 py-2 text-left font-medium transition-colors whitespace-nowrap border-l-2 text-blue-400 border-l-blue-400';
const INACTIVE_CLASS =
  'px-3 py-2 text-left font-medium transition-colors whitespace-nowrap border-l-2 text-gray-400 hover:text-white border-l-transparent';

export function DecisionModelsContainer({
  models,
  onAddModel,
  onUpdateModel,
  onDeleteModel,
  onToggleModel,
  setStatus,
  onNavigateToCredentials,
  fetchError,
  onRetry,
  groups,
  laneModels,
  kindIndex,
  onAddGroupModel,
  onUpdateGroupModel,
  onDeleteGroupModel,
  onToggleGroupModel,
  groupFetchError,
  groupOnRetry,
}: DecisionModelsContainerProps) {
  // The `models` child is the default — the panel the operator came
  // for is the decision-models list.
  const [child, setChild] = useState<ChildTab>('models');

  return (
    <div class="flex gap-4" data-testid="decision-models-container">
      {/* Vertical LEFT nav. Plain native buttons + a class-driven
          active state: NO role="tablist" / role="tab" / role="tabpanel",
          NO aria-selected, NO roving tabindex, NO arrow-key handling.
          `aria-current="true"` marks the ACTIVE button only — the
          inactive button OMITS the attribute entirely (never
          aria-current="false"). `type="button"` on every button so an
          Enter inside a form mounted in the panel can never submit
          through a nav button. */}
      <nav
        class="shrink-0 w-40 flex flex-col gap-1 border-r border-gray-700 pr-2"
        data-testid="decision-container-nav"
        aria-label="Decision Models sections"
      >
        {NAV_ITEMS.map((item) => {
          const active = child === item.child;
          return (
            <button
              key={item.child}
              type="button"
              class={active ? ACTIVE_CLASS : INACTIVE_CLASS}
              data-testid={item.testId}
              aria-label={item.ariaLabel}
              aria-current={active ? 'true' : undefined}
              onClick={() => setChild(item.child)}
            >
              {item.label}
            </button>
          );
        })}
      </nav>
      {/* Conditional render (mount/unmount) — the same shape as every
          top-level tab branch. Switching away DISCARDS the models
          panel's local state (an in-flight probe's rendered result
          goes with it; the probe REQUEST itself is not aborted — the
          FE never sends an AbortSignal). */}
      <div class="flex-1 min-w-0" data-testid="decision-container-panel">
        {child === 'models' ? (
          <DecisionModelsTab
            models={models}
            onAddModel={onAddModel}
            onUpdateModel={onUpdateModel}
            onDeleteModel={onDeleteModel}
            onToggleModel={onToggleModel}
            setStatus={setStatus}
            onNavigateToCredentials={onNavigateToCredentials}
            fetchError={fetchError}
            onRetry={onRetry}
          />
        ) : (
          <GroupModelsTab
            lane="decision"
            models={groups}
            laneModels={laneModels}
            kindIndex={kindIndex}
            onAddModel={onAddGroupModel}
            onUpdateModel={onUpdateGroupModel}
            onDeleteModel={onDeleteGroupModel}
            onToggleModel={onToggleGroupModel}
            setStatus={setStatus}
            fetchError={groupFetchError}
            onRetry={groupOnRetry}
          />
        )}
      </div>
    </div>
  );
}
