import { useState } from 'preact/hooks';
import type { ComponentChildren } from 'preact';

// ─── The shared sub-tab container core ───────────────────────────────────────
//
// This component is the GENERIC CORE of the vertical-left-nav container.
// It was extracted from DecisionModelsContainer.tsx (the decision lane's
// container) so the Text lane and the Image-Gen lane can host the SAME
// shape: a vertical left nav with two child panels (models | group).
//
// The core is a PASS-THROUGH: it never fetches, never owns status, never
// filters the data it renders. It owns exactly three things:
//
//   1. the vertical left nav rail (geometry + the active/inactive class
//      pair + the active-only aria-current marker),
//   2. the child-tab selection (uncontrolled by default; optionally
//      CONTROLLED by the parent — see the controlled-child seam below),
//   3. the conditional panel (mount/unmount — switching children
//      DISCARDS the panel's local state).
//
// It has NO page-level heading of its own: each child panel owns its own
// heading (the models panel says "Available Models", the group panel says
// "Group Models"). That is what lets the wrapped panels stay
// BYTE-UNCHANGED (wrap, never refactor).
//
// The a11y posture is deliberate and copied verbatim from the decision
// lane's container:
//
//   - native <button type="button"> — Tab reaches each button in DOM
//     order, Enter/Space activate natively;
//   - NO role="tablist" / role="tab" / role="tabpanel", NO aria-selected,
//     NO roving tabindex, NO arrow-key handling;
//   - aria-current="true" on the ACTIVE button ONLY — the inactive
//     button OMITS the attribute entirely (never aria-current="false");
//   - `type="button"` on every button so an Enter inside a form mounted
//     in the panel can never submit through a nav button.
//
// The testid NAMESPACE is a prop (containerTestId / navTestId /
// panelTestId + the per-item testId) so each kind's suite can assert
// exact ids AND assert that the nav is NOT matched by the top-level
// tab-bar selector (`div.flex.overflow-x-auto > button`).

// One nav item — the child it selects, the visible label, the accessible
// name and the testid. The testid is namespaced per kind by the caller.
export interface SubTabItem<C extends string> {
  child: C;
  label: string;
  ariaLabel: string;
  testId: string;
}

// The parts every container has, whatever its control mode.
interface SubTabContainerCommon<C extends string> {
  // The nav items, in render order.
  items: readonly SubTabItem<C>[];
  // The nav's accessible name — the per-kind "… sections" label.
  navAriaLabel: string;
  // The testid namespace of this container (container / nav / panel).
  containerTestId: string;
  navTestId: string;
  panelTestId: string;
  // The panel renderer. It receives the ACTIVE child and returns the
  // panel for it — the container never decides what a panel contains.
  renderPanel: (activeChild: C) => ComponentChildren;
}

// UNCONTROLLED — the container owns the child state. This is the
// decision lane's and the image-gen lane's posture (the container is
// self-contained; the parent never needs to command a child).
interface UncontrolledArm<C extends string> {
  activeChild?: undefined;
  onChildChange?: undefined;
  // The child shown on mount.
  defaultChild: C;
}

// CONTROLLED — the parent owns the child state. The container renders
// `activeChild` and reports a click through `onChildChange`; it never
// moves the child on its own. This is the seam the one-click wrap flow
// uses: the parent must be able to COMMAND the container onto the group
// child while the container stays mounted.
interface ControlledArm<C extends string> {
  activeChild: C;
  onChildChange: (child: C) => void;
  // Ignored in controlled mode (the parent is the authority); kept
  // optional so a controlled call site does not have to invent one.
  defaultChild?: C;
}

// The controlled pair is BOTH-OR-NEITHER: the TYPE forbids one without
// the other, and the runtime guard below makes the same promise loud if
// the contract is ever broken from plain JavaScript.
export type SubTabContainerProps<C extends string> =
  (UncontrolledArm<C> | ControlledArm<C>) & SubTabContainerCommon<C>;

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

export function SubTabContainer<C extends string>(props: SubTabContainerProps<C>) {
  const {
    items,
    navAriaLabel,
    containerTestId,
    navTestId,
    panelTestId,
    renderPanel,
    defaultChild,
    activeChild,
    onChildChange,
  } = props;

  // ── The controlled-pair guard (runtime half of the both-or-neither
  // contract). A container that renders a CHILD it does not own while
  // nobody can move it is a dead nav; a container that owns a child the
  // parent also owns is a nav that fights its parent. Both are
  // programming errors, and both are silent without this guard.
  if ((activeChild === undefined) !== (onChildChange === undefined)) {
    throw new Error(
      'SubTabContainer: activeChild and onChildChange are BOTH-OR-NEITHER — ' +
        'a CONTROLLED container needs both, an UNCONTROLLED one needs neither.',
    );
  }
  if (items.length === 0) {
    throw new Error('SubTabContainer: items must not be empty — a nav needs at least one child.');
  }

  // The UNCONTROLLED child state. In controlled mode this value is
  // ignored — the parent's `activeChild` is the authority (the
  // container never moves a child the parent owns).
  const [uncontrolledChild, setUncontrolledChild] = useState<C>(
    (defaultChild ?? items[0].child) as C,
  );
  const child = (activeChild ?? uncontrolledChild) as C;

  const selectChild = (next: C) => {
    if (onChildChange) {
      // CONTROLLED — report the intent; the parent moves the child.
      onChildChange(next);
      return;
    }
    setUncontrolledChild(next);
  };

  return (
    <div class="flex gap-4" data-testid={containerTestId}>
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
        data-testid={navTestId}
        aria-label={navAriaLabel}
      >
        {items.map((item) => {
          const active = child === item.child;
          return (
            <button
              key={item.child}
              type="button"
              class={active ? ACTIVE_CLASS : INACTIVE_CLASS}
              data-testid={item.testId}
              aria-label={item.ariaLabel}
              aria-current={active ? 'true' : undefined}
              onClick={() => selectChild(item.child)}
            >
              {item.label}
            </button>
          );
        })}
      </nav>
      {/* Conditional render (mount/unmount) — the same shape as every
          top-level tab branch. Switching away DISCARDS the panel's
          local state (an in-flight probe's rendered result goes with
          it; the probe REQUEST itself is not aborted — the FE never
          sends an AbortSignal). */}
      <div class="flex-1 min-w-0" data-testid={panelTestId}>
        {renderPanel(child)}
      </div>
    </div>
  );
}
