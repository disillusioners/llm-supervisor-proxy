import type { Model } from '../../types';
import { DecisionModelsTab } from './DecisionModelsTab';
import { GroupModelsTab } from './GroupModelsTab';
import { SubTabContainer, type SubTabItem } from './SubTabContainer';

// The container owns the VERTICAL LEFT NAV and the child-tab state.
// It has NO page-level heading of its own: each child panel owns its
// own heading (the models panel says "Decision Models", the group
// panel says "Decision Groups") — that is what lets DecisionModelsTab
// stay BYTE-UNCHANGED (wrap, never refactor).
//
// The nav + the child state now live in the SHARED core
// (SubTabContainer.tsx) — this file is the decision lane's thin wrapper
// over it: the SAME nav items, the SAME testids, the SAME accessible
// names and the SAME panels as before the extraction. The wrapper stays
// UNCONTROLLED (the decision lane never needs an outside party to
// command the child).
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
const NAV_ITEMS: SubTabItem<ChildTab>[] = [
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
  return (
    // The shared core — the decision lane's nav, its child state and its
    // panel switch. The `models` child is the default — the panel the
    // operator came for is the decision-models list.
    <SubTabContainer<ChildTab>
      items={NAV_ITEMS}
      navAriaLabel="Decision Models sections"
      containerTestId="decision-models-container"
      navTestId="decision-container-nav"
      panelTestId="decision-container-panel"
      defaultChild="models"
      renderPanel={(child) =>
        child === 'models' ? (
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
        )
      }
    />
  );
}
