import type { ComponentChildren } from 'preact';
import type { Model } from '../../types';
import { GroupModelsTab } from './GroupModelsTab';
import { SubTabContainer } from './SubTabContainer';

// The TEXT lane's container — the shared sub-tab shape (models | group)
// with the chat lane's surfaces inside it.
//
// It is the decision lane's container (DecisionModelsContainer) turned
// inside out: the SAME shared core (SubTabContainer), the SAME nav
// geometry, the SAME a11y posture, a DIFFERENT testid namespace.
//
//   nav items   : Models | Group
//   testids     : text-models-container / text-container-nav /
//                  text-container-tab-models / text-container-tab-group /
//                  text-container-panel
//   nav a11y    : "Text Models sections"
//
// The container has NO heading of its own: the models panel owns
// "Available Models" and the group panel owns "Group Models".
//
// ── WHY THE MODELS PANEL ARRIVES AS A NODE ─────────────────────────────────
// The models panel is MOUNTED BY THE PARENT (SettingsPage) and handed
// over as a node. This is a deliberate deviation from the "the wrapper
// mounts its panels" shape, forced by the frozen tsc baseline:
//
//   The <ModelsTab …> mount in SettingsPage.tsx carries a FROZEN
//   TS2322 error (ModelsTab's `status` prop is required; the call site
//   never passes it — the count is frozen at 5 errors in
//   SettingsPage.tsx, see pkg/ui/frontend/tsc-baseline.txt). The
//   baseline is a COUNT + FILE-SET freeze: moving the mount into this
//   new file would move that error into a NEW file, which is baseline
//   drift — and the baseline must not be edited. Keeping the mount in
//   SettingsPage.tsx keeps the error in its frozen file, so the
//   baseline stays EXACTLY as frozen.
//
//   The props still reach ModelsTab verbatim (the parent builds the
//   node with exactly the props it always passed) — the container stays
//   a pass-through: it never fetches, never owns status, never filters.
//
// The GROUP panel is mounted HERE (the chat lane's group surface): its
// props are the parent-scoped lane data, and the wrap pre-fill pair.
type ChildTab = 'models' | 'group';

// The controlled pair is BOTH-OR-NEITHER, exactly as in the shared
// core: the TYPE forbids one without the other and the core's runtime
// guard makes a broken contract loud.
type ControlledPair =
  | { activeChild?: undefined; onChildChange?: undefined }
  | { activeChild: ChildTab; onChildChange: (child: ChildTab) => void };

export type TextModelsContainerProps = {
  // ── The models panel — the parent's node (see the header note) ─────
  modelsPanel: ComponentChildren;

  // ── The group panel — the CHAT lane's group surface ────────────────
  // `groups` is the parent-scoped chat-lane group list (the client-side
  // lane split lives in the parent; the panel trusts what it is given).
  // `laneModels` is the chat lane's model feed (the chat-kind rows).
  groups: Model[];
  laneModels: Model[];
  kindIndex?: Map<string, string>;
  setStatus: (status: { type: 'success' | 'error'; message: string; restartRequired?: boolean } | null) => void;
  onAddGroupModel: (model: Omit<Model, 'id'> & { id: string }) => Promise<void>;
  onUpdateGroupModel: (id: string, updates: Partial<Model>) => Promise<void>;
  onDeleteGroupModel: (id: string) => Promise<void>;
  onToggleGroupModel: (model: Model) => Promise<void>;
  groupFetchError?: string | null;
  groupOnRetry?: () => void;
  // One-click wrap pre-fill (Am30): the chat model being wrapped. The
  // panel consumes the signal once and opens the pre-filled add-form.
  // TEXT LANE ONLY — the wrap is a chat-model affordance.
  wrapPreloadModel?: Model | null;
  onWrapConsumed?: () => void;

  // ── The controlled-child seam ───────────────────────────────────────
  // The parent owns the child state: it hands the ACTIVE child down and
  // receives the child's changes back. The one-click wrap flow needs it
  // — the container stays mounted while the parent moves it onto the
  // group child.
} & ControlledPair;

// Nav items — the same labels and the same accessible-name shape as
// the decision lane's nav ("Decision Models" / "Decision Groups"): the
// lane name plus the surface name, so a screen reader never hears a
// bare "Models" next to the top-level "Models" tab.
const NAV_ITEMS: { child: ChildTab; label: string; ariaLabel: string; testId: string }[] = [
  {
    child: 'models',
    label: 'Models',
    ariaLabel: 'Text Models',
    testId: 'text-container-tab-models',
  },
  {
    child: 'group',
    label: 'Group',
    ariaLabel: 'Group Models',
    testId: 'text-container-tab-group',
  },
];

export function TextModelsContainer(props: TextModelsContainerProps) {
  const {
    modelsPanel,
    groups,
    laneModels,
    kindIndex,
    setStatus,
    onAddGroupModel,
    onUpdateGroupModel,
    onDeleteGroupModel,
    onToggleGroupModel,
    groupFetchError,
    groupOnRetry,
    wrapPreloadModel,
    onWrapConsumed,
    activeChild,
    onChildChange,
  } = props;

  // The control pair is forwarded verbatim — but the core's props are a
  // BOTH-OR-NEITHER union, so the pair travels as a union-typed spread
  // (each arm is checked against the core's arms: the controlled pair
  // matches the controlled arm, the empty spread the uncontrolled one).
  const controlProps =
    activeChild !== undefined && onChildChange !== undefined
      ? { activeChild, onChildChange }
      : {};

  return (
    <SubTabContainer<ChildTab>
      items={NAV_ITEMS}
      navAriaLabel="Text Models sections"
      containerTestId="text-models-container"
      navTestId="text-container-nav"
      panelTestId="text-container-panel"
      defaultChild="models"
      {...controlProps}
      renderPanel={(child) =>
        child === 'models' ? (
          modelsPanel
        ) : (
          <GroupModelsTab
            lane="chat"
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
            wrapPreloadModel={wrapPreloadModel}
            onWrapConsumed={onWrapConsumed}
          />
        )
      }
    />
  );
}
