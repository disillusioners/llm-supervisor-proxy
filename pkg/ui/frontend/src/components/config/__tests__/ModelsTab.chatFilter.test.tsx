import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent, act } from '@testing-library/preact';
import { ModelsTab } from '../ModelsTab';
import type { Model } from '../../../types';

function makeModel(overrides: Partial<Model> = {}): Model {
  return {
    id: 'm1',
    name: 'M1',
    enabled: true,
    fallback_chain: [],
    ...overrides,
  };
}

describe('ModelsTab — chat-tab filter for image-gen rows (C-16, T2.4.5, FE-15)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
  });

  it('C-16 — image-gen rows are filtered out; chat rows render', () => {
    const chat = makeModel({ id: 'chat', name: 'Chat A' });
    const img = makeModel({ id: 'image', name: 'Image B', kind: 'image-gen' });
    const { container } = render(
      <ModelsTab
        models={[chat, img]}
        onAddModel={vi.fn()}
        onUpdateModel={vi.fn()}
        onDeleteModel={vi.fn()}
        onToggleModel={vi.fn()}
        onCopyModel={vi.fn()}
        status={null}
        setStatus={vi.fn()}
      />,
    );
    // Chat row is rendered.
    expect(container.textContent).toContain('Chat A');
    // Image-gen row is filtered out (C-16 / T2.4.5).
    expect(container.textContent).not.toContain('Image B');
  });

  it('Decision Commission P3.2 — decision rows are filtered out alongside image-gen rows', () => {
    // Locked-by-test (Decision Commission 2026-10-15 / plan §6.5):
    // a model with kind: 'decision' must be filtered OUT of `chatModels`
    // alongside image-gen rows, so a chat-tab render never shows
    // non-chat models. Mirrors the C-16 image-gen exclusion above.
    const chat = makeModel({ id: 'chat', name: 'Chat A' });
    const decision = makeModel({ id: 'decision-1', name: 'Decision B', kind: 'decision' });
    const img = makeModel({ id: 'image', name: 'Image C', kind: 'image-gen' });
    const { container } = render(
      <ModelsTab
        models={[chat, decision, img]}
        onAddModel={vi.fn()}
        onUpdateModel={vi.fn()}
        onDeleteModel={vi.fn()}
        onToggleModel={vi.fn()}
        onCopyModel={vi.fn()}
        status={null}
        setStatus={vi.fn()}
      />,
    );
    // Chat row is rendered.
    expect(container.textContent).toContain('Chat A');
    // Decision + image-gen rows are filtered out.
    expect(container.textContent).not.toContain('Decision B');
    expect(container.textContent).not.toContain('Image C');
  });

  it('load-balance-groups P3.7 — group rows are filtered out of the chat tab (§6.4 arm 2 of 4)', () => {
    // Locked-by-test (load-balance-groups 2026-10-16 / plan §6.4):
    // a model with kind: 'group' must be filtered OUT of `chatModels`
    // alongside image-gen + decision rows — virtual groups get their
    // own Group Models tab; the chat tab stays pure. The chat hook
    // fetches UNFILTERED (bare /fe/api/models), so this client filter
    // is load-bearing.
    const chat = makeModel({ id: 'chat', name: 'Chat A' });
    const group = makeModel({ id: 'grp', name: 'Group B', kind: 'group' });
    const { container } = render(
      <ModelsTab
        models={[chat, group]}
        onAddModel={vi.fn()}
        onUpdateModel={vi.fn()}
        onDeleteModel={vi.fn()}
        onToggleModel={vi.fn()}
        onCopyModel={vi.fn()}
        status={null}
        setStatus={vi.fn()}
      />,
    );
    // Chat row is rendered.
    expect(container.textContent).toContain('Chat A');
    // Group row is filtered out (§6.4 arm 2).
    expect(container.textContent).not.toContain('Group B');
  });

  it('load-balance-groups P3.7 — a config with ONLY group rows renders the "No models configured" empty state', () => {
    // Same hoisted-filter regression class as the image-gen-only
    // config below: the filter runs BEFORE the empty-state ternary, so
    // a group-only config surfaces the chat empty state instead of an
    // invisible filtered-to-empty list.
    const groupOnly = makeModel({ id: 'grp', name: 'Group B', kind: 'group' });
    const { container } = render(
      <ModelsTab
        models={[groupOnly]}
        onAddModel={vi.fn()}
        onUpdateModel={vi.fn()}
        onDeleteModel={vi.fn()}
        onToggleModel={vi.fn()}
        onCopyModel={vi.fn()}
        status={null}
        setStatus={vi.fn()}
      />,
    );
    expect(container.textContent).not.toContain('Group B');
    expect(container.textContent).toContain('No models configured');
  });

  it('FE-15 (optional) — rows without a kind field are NOT filtered (degraded behavior)', () => {
    // Per spec §2.6 "R1 mitigation": if kind is undefined, the filter
    // returns true for the row (undefined !== 'image-gen' is true). This
    // is the correct degraded behavior when the BE doesn't yet expose
    // `kind` on the wire.
    const a = makeModel({ id: 'a', name: 'A' });
    const b = makeModel({ id: 'b', name: 'B' });
    const { container } = render(
      <ModelsTab
        models={[a, b]}
        onAddModel={vi.fn()}
        onUpdateModel={vi.fn()}
        onDeleteModel={vi.fn()}
        onToggleModel={vi.fn()}
        onCopyModel={vi.fn()}
        status={null}
        setStatus={vi.fn()}
      />,
    );
    expect(container.textContent).toContain('A');
    expect(container.textContent).toContain('B');
  });

  it('review finding #2 — config with ONLY image-gen rows renders the "No models configured" empty state', () => {
    // Regression for the leader-approved FE review finding #2: the
    // chat-tab filter (`kind !== 'image-gen'`) sat INSIDE the non-empty
    // branch of the empty-state ternary. A config containing only
    // image-gen rows (e.g. an operator who only uses image-gen, or a
    // fresh config before any chat models are added) would have
    // models.length > 0, take the non-empty branch, and render an
    // invisible filtered-to-empty list — never surfacing the
    // "No models configured" empty state to the user.
    //
    // The fix hoists the filter to `chatModels` BEFORE the ternary and
    // branches on chatModels.length, so the empty state renders whenever
    // the chat-tab-filtered list is empty (whether `models` itself is
    // empty OR all of its rows are image-gen).
    const imgOnly = makeModel({ id: 'image', name: 'Image B', kind: 'image-gen' });
    const { container } = render(
      <ModelsTab
        models={[imgOnly]}
        onAddModel={vi.fn()}
        onUpdateModel={vi.fn()}
        onDeleteModel={vi.fn()}
        onToggleModel={vi.fn()}
        onCopyModel={vi.fn()}
        status={null}
        setStatus={vi.fn()}
      />,
    );
    // The image-gen row is filtered out (C-16 / T2.4.5), AND the
    // empty-state now renders because chatModels.length === 0.
    expect(container.textContent).not.toContain('Image B');
    expect(container.textContent).toContain('No models configured');
  });
});

// copy-model commission — the chat tab's Copy action.
//
// The BE route (POST /fe/api/models/{id}/copy) owns the clone
// semantics; this suite pins the FE contract around it: prefill,
// inline validation, the error path (dialog stays open, typed id
// preserved, BE error shown inline), the success path (dialog closes,
// success toast raised) and the a11y attributes.
describe('ModelsTab — copy model action (copy-model commission)', () => {
  const chat = makeModel({ id: 'm1', name: 'M1' });

  beforeEach(() => {
    document.body.innerHTML = '';
  });
  afterEach(() => {
    cleanup();
  });

  // Render the tab with a single chat model and return the pieces the
  // copy flow needs.
  function renderTab(onCopyModel: (sourceId: string, newId: string) => Promise<void>) {
    const setStatus = vi.fn();
    const { container } = render(
      <ModelsTab
        models={[chat]}
        onAddModel={vi.fn()}
        onUpdateModel={vi.fn()}
        onDeleteModel={vi.fn()}
        onToggleModel={vi.fn()}
        onCopyModel={onCopyModel}
        status={null}
        setStatus={setStatus}
      />,
    );
    const copyBtn = container.querySelector('[data-testid="copy-model-button"]') as HTMLButtonElement;
    const dialog = () => container.querySelector('[data-testid="copy-model-dialog"]');
    const input = () => container.querySelector('[data-testid="copy-model-new-id-input"]') as HTMLInputElement;
    const confirm = () => container.querySelector('[data-testid="copy-model-confirm"]') as HTMLButtonElement;
    const alert = () => container.querySelector('[role="alert"]');
    return { container, setStatus, copyBtn, dialog, input, confirm, alert };
  }

  it('renders a Copy row action with the house attributes (type/title/aria-label/testid)', () => {
    const { container, copyBtn } = renderTab(vi.fn());
    expect(copyBtn).not.toBeNull();
    expect(copyBtn.getAttribute('type')).toBe('button');
    expect(copyBtn.getAttribute('title')).toBe('Copy M1');
    expect(copyBtn.getAttribute('aria-label')).toBe('Copy M1');
    expect(container.querySelectorAll('[data-testid="copy-model-button"]').length).toBe(1);
  });

  it('opens the dialog prefilled with "<source-id>-copy" and focuses the new-id input', () => {
    const { copyBtn, dialog, input } = renderTab(vi.fn());
    expect(dialog()).toBeNull();

    fireEvent.click(copyBtn);

    expect(dialog()).not.toBeNull();
    expect(input().value).toBe('m1-copy');
    // Initial focus lands on the new-id input.
    expect(document.activeElement).toBe(input());
  });

  it('inline validation blocks the submit when the new id is empty', async () => {
    const onCopy = vi.fn().mockResolvedValue(undefined);
    const { copyBtn, input, confirm, alert } = renderTab(onCopy);

    fireEvent.click(copyBtn);
    fireEvent.input(input(), { target: { value: '' } });
    await act(async () => {
      fireEvent.click(confirm());
      await Promise.resolve();
    });

    // The BE is never called.
    expect(onCopy).not.toHaveBeenCalled();
    // The message is inline + announced (role="alert").
    expect(alert()!.textContent).toBe('new_id is required');
  });

  it('inline validation blocks the submit when the new id contains "/" or whitespace', async () => {
    const onCopy = vi.fn().mockResolvedValue(undefined);
    const { copyBtn, input, confirm, alert } = renderTab(onCopy);

    fireEvent.click(copyBtn);
    fireEvent.input(input(), { target: { value: 'bad/id' } });
    await act(async () => {
      fireEvent.click(confirm());
      await Promise.resolve();
    });
    expect(onCopy).not.toHaveBeenCalled();
    expect(alert()!.textContent).toBe('new_id must not contain "/" or whitespace');

    // Whitespace is refused with the same message (the BE's rule).
    fireEvent.input(input(), { target: { value: 'bad id' } });
    await act(async () => {
      fireEvent.click(confirm());
      await Promise.resolve();
    });
    expect(onCopy).not.toHaveBeenCalled();
    expect(alert()!.textContent).toBe('new_id must not contain "/" or whitespace');
  });

  it('submit calls onCopyModel with the source id and the typed new id', async () => {
    const onCopy = vi.fn().mockResolvedValue(undefined);
    const { copyBtn, input, confirm } = renderTab(onCopy);

    fireEvent.click(copyBtn);
    fireEvent.input(input(), { target: { value: 'm1-variant' } });
    await act(async () => {
      fireEvent.click(confirm());
      await Promise.resolve();
    });

    expect(onCopy).toHaveBeenCalledTimes(1);
    expect(onCopy).toHaveBeenCalledWith('m1', 'm1-variant');
  });

  it('success closes the dialog and raises the success toast naming the new id + the DISABLED warning', async () => {
    const onCopy = vi.fn().mockResolvedValue(undefined);
    const { copyBtn, dialog, setStatus, confirm } = renderTab(onCopy);

    fireEvent.click(copyBtn);
    await act(async () => {
      fireEvent.click(confirm());
      await Promise.resolve();
    });

    // The dialog is gone.
    expect(dialog()).toBeNull();
    // The house toast is raised through setStatus (the container's
    // toast API — SettingsPage maps setStatus onto its ToastContainer).
    expect(setStatus).toHaveBeenCalledTimes(1);
    expect(setStatus).toHaveBeenCalledWith({
      type: 'success',
      message: 'Copied model "m1" to "m1-copy". The copy is DISABLED: it is not in /v1/models and not in automatic selection — a client that names "m1-copy" explicitly is still served. Enable it to add it to /v1/models and auto-selection.',
    });
  });

  it('a backend failure keeps the dialog open, preserves the typed id and shows the BE error inline', async () => {
    const onCopy = vi.fn().mockRejectedValue(new Error('duplicate model ID'));
    const { copyBtn, dialog, input, confirm, alert, setStatus } = renderTab(onCopy);

    fireEvent.click(copyBtn);
    // A DISTINCT id, NOT the prefill ("m1-copy"): with the prefill the
    // test could not tell "the typed id was preserved" apart from "the
    // field was reset to the prefill on failure" — a defect this test
    // exists to catch.
    fireEvent.input(input(), { target: { value: 'm1-typed-copy' } });
    await act(async () => {
      fireEvent.click(confirm());
      await Promise.resolve();
    });

    // The dialog STAYS OPEN and the typed id is PRESERVED.
    expect(dialog()).not.toBeNull();
    expect(input().value).toBe('m1-typed-copy');
    // The BE's {"error":"..."} text is shown inline, announced by role.
    expect(alert()!.textContent).toBe('duplicate model ID');
    // No success toast on a failure.
    expect(setStatus).not.toHaveBeenCalled();
  });

  it('a11y — role="dialog", aria-modal, aria-labelledby → the dialog title id', () => {
    const { copyBtn, dialog } = renderTab(vi.fn());
    fireEvent.click(copyBtn);

    const panel = dialog()!;
    expect(panel.getAttribute('role')).toBe('dialog');
    expect(panel.getAttribute('aria-modal')).toBe('true');
    const labelledBy = panel.getAttribute('aria-labelledby')!;
    expect(labelledBy).toBeTruthy();
    // The title element carries that id.
    const title = panel.querySelector(`#${labelledBy}`);
    expect(title).not.toBeNull();
    expect(title!.textContent).toContain('Copy Model');
  });

  it('a11y — Esc closes the dialog and focus is restored to the Copy button', async () => {
    const { copyBtn, dialog, input } = renderTab(vi.fn());

    fireEvent.click(copyBtn);
    expect(dialog()).not.toBeNull();
    expect(document.activeElement).toBe(input());

    await act(async () => {
      // The keydown is fired on the focused input — it bubbles up to
      // the window listener, exactly as it does in a browser.
      fireEvent.keyDown(input(), { key: 'Escape' });
      await Promise.resolve();
    });

    expect(dialog()).toBeNull();
    // Focus is RESTORED to the Copy button that opened the dialog.
    expect(document.activeElement).toBe(copyBtn);
  });

  it('Esc is IGNORED while the copy request is in flight — the dialog stays open and a late BE failure surfaces inline', async () => {
    // A DEFERRED promise so the request is genuinely in flight: the
    // rejection is triggered by the test, not by the click.
    let rejectCopy: (reason?: unknown) => void = () => {};
    const onCopy = vi.fn(
      () =>
        new Promise<void>((_resolve, reject) => {
          rejectCopy = reject;
        }),
    );
    const { copyBtn, dialog, input, confirm, alert, setStatus } = renderTab(onCopy);

    fireEvent.click(copyBtn);
    fireEvent.input(input(), { target: { value: 'm1-typed-copy' } });

    // Start the copy — the request is now pending (`copying` = true,
    // which also disables the Cancel button).
    await act(async () => {
      fireEvent.click(confirm());
      await Promise.resolve();
    });
    expect(onCopy).toHaveBeenCalledTimes(1);
    expect(onCopy).toHaveBeenCalledWith('m1', 'm1-typed-copy');

    // Esc mid-flight must NOT close the dialog — it is not a bypass of
    // the disabled-Cancel guard.
    await act(async () => {
      fireEvent.keyDown(input(), { key: 'Escape' });
      await Promise.resolve();
    });
    expect(dialog()).not.toBeNull();
    expect(input().value).toBe('m1-typed-copy');
    // Still in flight: no error and no toast yet.
    expect(alert()).toBeNull();
    expect(setStatus).not.toHaveBeenCalled();

    // The BE failure arrives AFTER the Esc press: it is displayed
    // inline in the still-open dialog, the typed id is preserved and NO
    // toast is raised.
    await act(async () => {
      rejectCopy(new Error('duplicate model ID'));
      await Promise.resolve();
    });
    expect(dialog()).not.toBeNull();
    expect(input().value).toBe('m1-typed-copy');
    expect(alert()!.textContent).toBe('duplicate model ID');
    expect(setStatus).not.toHaveBeenCalled();
  });

  it('the BACKDROP closes the copy dialog when idle (guards the selector + the idle close path)', async () => {
    const { copyBtn, dialog, container } = renderTab(vi.fn());

    fireEvent.click(copyBtn);
    expect(dialog()).not.toBeNull();

    // The backdrop is the overlay behind the dialog panel. Selecting it
    // by testid (not by structure) is what makes this test meaningful:
    // a wrong selector would silently click a harmless element and the
    // "does not close" assertion would pass VACUOUSLY.
    const backdrop = container.querySelector('[data-testid="copy-model-backdrop"]') as HTMLElement;
    expect(backdrop).not.toBeNull();

    await act(async () => {
      fireEvent.click(backdrop);
      await Promise.resolve();
    });

    // Idle → the backdrop click DOES close the dialog.
    expect(dialog()).toBeNull();
  });

  it('clicking the BACKDROP while the copy request is in flight does NOT close the dialog — a late BE failure surfaces inline', async () => {
    // A DEFERRED promise so the request is genuinely in flight: the
    // rejection is triggered by the test, not by the click.
    let rejectCopy: (reason?: unknown) => void = () => {};
    const onCopy = vi.fn(
      () =>
        new Promise<void>((_resolve, reject) => {
          rejectCopy = reject;
        }),
    );
    const { copyBtn, dialog, input, confirm, alert, setStatus, container } = renderTab(onCopy);

    fireEvent.click(copyBtn);
    fireEvent.input(input(), { target: { value: 'm1-typed-copy' } });

    // Start the copy — the request is now pending.
    await act(async () => {
      fireEvent.click(confirm());
      await Promise.resolve();
    });
    expect(onCopy).toHaveBeenCalledTimes(1);
    expect(onCopy).toHaveBeenCalledWith('m1', 'm1-typed-copy');

    const backdrop = container.querySelector('[data-testid="copy-model-backdrop"]') as HTMLElement;
    expect(backdrop).not.toBeNull();

    // A backdrop click mid-flight must NOT close the dialog — the
    // contract requires the dialog to stay open so the outcome is
    // always observable.
    await act(async () => {
      fireEvent.click(backdrop);
      await Promise.resolve();
    });
    expect(dialog()).not.toBeNull();
    expect(input().value).toBe('m1-typed-copy');
    // Still in flight: no error and no toast yet.
    expect(alert()).toBeNull();
    expect(setStatus).not.toHaveBeenCalled();

    // The BE failure arrives AFTER the backdrop click: it is displayed
    // inline in the still-open dialog, the typed id is preserved and NO
    // toast is raised.
    await act(async () => {
      rejectCopy(new Error('duplicate model ID'));
      await Promise.resolve();
    });
    expect(dialog()).not.toBeNull();
    expect(input().value).toBe('m1-typed-copy');
    expect(alert()!.textContent).toBe('duplicate model ID');
    expect(setStatus).not.toHaveBeenCalled();
  });

  it('the SUCCESS path still closes the dialog and raises the toast (the in-flight guard must not block it)', async () => {
    // A DEFERRED promise so the request is still in flight when we
    // assert — that is what makes this test discriminate against the
    // "success close blocked by the guard" regression.
    let resolveCopy: () => void = () => {};
    const onCopy = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolveCopy = resolve;
        }),
    );
    const { copyBtn, dialog, setStatus, confirm } = renderTab(onCopy);

    fireEvent.click(copyBtn);
    await act(async () => {
      fireEvent.click(confirm());
      await Promise.resolve();
    });

    // The request IS in flight here (the button is in its "Copying..."
    // state), so the in-flight guard is ARMED at this moment.
    expect(confirm().textContent).toBe('Copying...');
    expect(dialog()).not.toBeNull();

    // The request settles successfully.
    await act(async () => {
      resolveCopy();
      await Promise.resolve();
    });

    // The success close must still go through — the programmatic close
    // clears the in-flight flag first. If the guard blocked it, the
    // dialog would stay open and the operator would see no toast.
    expect(dialog()).toBeNull();
    expect(setStatus).toHaveBeenCalledTimes(1);
    expect(setStatus).toHaveBeenCalledWith({
      type: 'success',
      message: 'Copied model "m1" to "m1-copy". The copy is DISABLED: it is not in /v1/models and not in automatic selection — a client that names "m1-copy" explicitly is still served. Enable it to add it to /v1/models and auto-selection.',
    });
  });

  it('two same-tick submits start only ONE request (re-entrancy guard) — one success toast, dialog closed', async () => {
    const onCopy = vi.fn().mockResolvedValue(undefined);
    const { copyBtn, dialog, setStatus, confirm } = renderTab(onCopy);

    fireEvent.click(copyBtn);
    // Two submits dispatched in the SAME tick (the re-entrancy probe):
    // without the guard BOTH start a request. The in-flight flag is a
    // BOOLEAN, not a counter, so the first request settling would clear
    // it while the second is still pending — re-opening the
    // mid-flight-close window that rounds 1 and 2 closed.
    await act(async () => {
      fireEvent.click(confirm());
      fireEvent.click(confirm());
      await Promise.resolve();
    });

    // EXACTLY ONE request.
    expect(onCopy).toHaveBeenCalledTimes(1);
    expect(onCopy).toHaveBeenCalledWith('m1', 'm1-copy');

    // The single request still produces the normal outcome: the dialog
    // closes and exactly ONE success toast is raised.
    expect(dialog()).toBeNull();
    expect(setStatus).toHaveBeenCalledTimes(1);
    expect(setStatus).toHaveBeenCalledWith({
      type: 'success',
      message: 'Copied model "m1" to "m1-copy". The copy is DISABLED: it is not in /v1/models and not in automatic selection — a client that names "m1-copy" explicitly is still served. Enable it to add it to /v1/models and auto-selection.',
    });
  });

  it('W9a — the dialog warning paragraph renders the EXACT truthful text (not-listed + explicit-id still served + the C2 enable clause)', () => {
    const { copyBtn, container } = renderTab(vi.fn());

    fireEvent.click(copyBtn);

    const warning = container.querySelector('[data-testid="copy-model-warning"]');
    expect(warning).not.toBeNull();

    // The EXACT RENDERED text of the paragraph (queried from the DOM,
    // not compared against a constant), INCLUDING the C2 trailing
    // sentence. A regression to "unreachable" / "cannot be reached"
    // wording changes this string and fails here — the paragraph
    // previously had NO test reading it, which is exactly how the
    // untruthful toast (C1) and the contradictory trailing sentence (C2)
    // slipped through.
    expect(warning!.textContent).toBe(
      'The copy is created DISABLED: it is excluded from /v1/models and from race chains and groups, but a client that names its id explicitly can still reach it. Enable it to add it to /v1/models and to automatic selection.',
    );

    // The two required facts, asserted independently so a partial
    // rewording fails loudly rather than only on the exact-match above.
    expect(warning!.textContent).toContain('excluded from /v1/models');
    expect(warning!.textContent).toContain('race chains and groups');
    expect(warning!.textContent).toContain('names its id explicitly can still reach it');
    expect(warning!.textContent).toContain('Enable it to add it to /v1/models and to automatic selection.');

    // Truthfulness tripwire: the copy IS served on the explicit-id path,
    // so no "unreachable" claim may appear anywhere in the paragraph.
    expect(warning!.textContent).not.toMatch(/unreachable|cannot be reached|can not be reached/i);
  });
});
