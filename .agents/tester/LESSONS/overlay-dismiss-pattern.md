# Overlay Dismiss Pattern for Confirmation Dialogs

**Date:** 2026-05-08
**Context:** Delete model button fix (`fix/delete-model-button`)

## Lesson
When creating modal/confirmation dialogs, always add:
1. `onClick={dismissHandler}` on the overlay/background div
2. `onClick={(e) => e.stopPropagation()}` on the inner content div

Without these, clicking outside the dialog does nothing — users can't dismiss by clicking the overlay.

## Pattern
```tsx
<div className="fixed inset-0 bg-black/60 ..." onClick={() => setShowDialog(null)}>
  <div className="..." onClick={(e) => e.stopPropagation()}>
    {/* Dialog content */}
  </div>
</div>
```

## Files Affected
- `ModelsTab.tsx` — delete confirmation dialog
- `CredentialsTab.tsx` — same pattern for consistency
