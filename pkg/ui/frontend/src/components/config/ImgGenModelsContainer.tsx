import type { Model } from '../../types';
import { ImgGenModelsTab } from './ImgGenModelsTab';
import { GroupModelsTab } from './GroupModelsTab';
import { SubTabContainer, type SubTabItem } from './SubTabContainer';

// The IMAGE-GEN lane's container — the shared sub-tab shape (models |
// group) with the image-gen surfaces inside it.
//
//   nav items   : Models | Group
//   testids     : imggen-models-container / imggen-container-nav /
//                  imggen-container-tab-models / imggen-container-tab-group /
//                  imggen-container-panel
//   nav a11y    : "Image-Gen Models sections"
//
// The container has NO heading of its own: the models panel owns
// "Image-Gen Models" and the group panel owns "Image-Gen Groups".
//
// The container is UNCONTROLLED (like the decision lane's): nobody
// outside it needs to command its child. The group child carries the
// IMAGE-GEN lane's groups — the parent hands down the 'image-gen'
// bucket of the client-side lane split (a group whose members are
// image-gen models), and the lane's own model feed.
type ChildTab = 'models' | 'group';

export interface ImgGenModelsContainerProps {
  // ── The models panel — every prop is passed through VERBATIM ────────
  // The container is a pass-through, not a filter: the image-gen model
  // list, its mutations, its error state and its navigate-to-credentials
  // affordance reach ImgGenModelsTab exactly as the parent handed them
  // over.
  models: Model[];
  onAddModel: (model: Omit<Model, 'id'> & { id: string }) => Promise<void>;
  onUpdateModel: (id: string, updates: Partial<Model>) => Promise<void>;
  onDeleteModel: (id: string) => Promise<void>;
  onToggleModel: (model: Model) => Promise<void>;
  setStatus: (status: { type: 'success' | 'error'; message: string; restartRequired?: boolean } | null) => void;
  onNavigateToCredentials?: () => void;
  fetchError?: string | null;
  onRetry?: () => void;

  // ── The group panel — the IMAGE-GEN lane's group surface ────────────
  // `groups` is the parent-scoped, image-gen-lane group list (the
  // client-side lane split lives in the parent; the panel trusts what
  // it is given). `laneModels` is the image-gen lane's model feed — the
  // server-prefiltered ?kind=image-gen list.
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

// Nav items — the same shape as the decision lane's nav: the lane's
// surface name as the accessible name, so a screen reader never hears a
// bare "Models" next to the top-level "Models" tab.
const NAV_ITEMS: SubTabItem<ChildTab>[] = [
  {
    child: 'models',
    label: 'Models',
    ariaLabel: 'Image-Gen Models',
    testId: 'imggen-container-tab-models',
  },
  {
    child: 'group',
    label: 'Group',
    ariaLabel: 'Image-Gen Groups',
    testId: 'imggen-container-tab-group',
  },
];

export function ImgGenModelsContainer({
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
}: ImgGenModelsContainerProps) {
  return (
    <SubTabContainer<ChildTab>
      items={NAV_ITEMS}
      navAriaLabel="Image-Gen Models sections"
      containerTestId="imggen-models-container"
      navTestId="imggen-container-nav"
      panelTestId="imggen-container-panel"
      defaultChild="models"
      renderPanel={(child) =>
        child === 'models' ? (
          <ImgGenModelsTab
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
            lane="image-gen"
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
