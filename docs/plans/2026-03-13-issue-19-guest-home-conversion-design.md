# Issue 19 Guest Home Conversion Upsell Design

## APPROVED

- Approver: repository maintainer (chat approval)
- Approval date: 2026-03-13
- Approval artifact: maintainer approved the design in chat before implementation

## Goal

Complete the remaining product scope for issue `#19` by turning the guest-first home into an explicit conversion surface: guests can keep chatting with free models, see paid models as locked, and authenticate through a dismissible modal without leaving `/`.

## Problem Statement

Issue `#19` is no longer blocked on guest access or analytics separation. Those pieces exist. The remaining gap is product behavior:

- guests can chat on `/`
- free models are available
- paid models should be visible, not invisible
- guests should understand that paid models require an account and credits
- authentication should be low-friction and non-disruptive

If paid models stay hidden or the auth handoff is too abrupt, the guest-first home does not actually create the intended upgrade path.

## Chosen Direction

Use a single shared chat workspace with inline locked-model upsell and a combined auth modal.

- Show all chat-capable models in the picker
- Mark non-free models as locked for guests
- Display a short reason: `Requires account and credits`
- Open a combined auth modal when a guest clicks a locked model
- Preserve the current guest conversation if the modal is dismissed
- After successful auth, close the modal and unlock the paid models in place

This keeps the conversion path visible without forcing guests off the chat surface.

## Section 1: UX Behavior

### Model picker

- Guests see both free and paid chat models.
- Free models behave normally.
- Paid models render as locked and are not selectable as the active model in guest mode, but still clickable to launch the combined auth modal.
- Locked entries show a small status label and a short explanation.

### Auth prompt

- Clicking a locked model opens a combined auth modal on top of `/`.
- The modal supports:
  - login
  - Google sign-in
  - account creation
- Closing the modal returns the user to the same guest chat state.

### Post-auth state

- On successful auth, the modal closes.
- The chat workspace remains on `/`.
- Model availability refreshes immediately.
- Previously locked paid models become selectable.

## Section 2: Implementation Shape

### Web state

The chat session hook should stop treating model options as bare strings. It needs enough metadata to drive both guest filtering and locked-state rendering:

- `id`
- `capability`
- `costType`
- derived `locked` state in guest mode

### UI composition

- Keep `HomePage` as the single product surface.
- Extend the composer model picker to render locked entries distinctly.
- Add a reusable auth modal component rather than forcing a page navigation to `/auth`.
- Reuse the current auth logic where possible so the modal does not create a second authentication system.

### Backend boundary

No backend policy should loosen:

- guest chat stays restricted to `free` models
- authenticated chat behavior stays as-is
- public API auth requirements stay unchanged
- issue `#57` runtime separation remains out of scope for this slice

## Section 3: Error Handling and UX Guardrails

- Selecting a locked model should not silently switch the active model.
- If auth fails, the modal stays open and surfaces the existing auth error handling.
- If the user dismisses the modal, they should still be able to continue sending to the currently selected free model.
- If model refresh after auth fails, keep the current model and surface a recoverable UI message rather than breaking the workspace.

## Section 4: Testing and Verification

Required verification should cover:

- guest rendering of locked paid models
- locked-model click opening the auth modal
- dismissing the modal without losing guest chat state
- successful auth closing the modal and unlocking paid models
- guest free-chat regression coverage
- production web build with the required public envs

If the smoke suite is extended in this slice, it should prove:

1. guest lands on `/`
2. guest can use a free model
3. guest sees a locked paid model
4. guest authenticates
5. paid model becomes available on the same chat surface

## Non-Goals

- no authenticated web runtime split away from the public API path in this issue
- no per-model pricing display in the picker yet
- no billing/top-up modal in this slice
- no issue `#57` implementation
