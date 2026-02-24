# Chat-First Frontend Information Architecture Design

Date: 2026-02-23
Status: Active

## Goals

- Make chat the default app landing route.
- Keep `Developer Panel` and `Settings` as peer actions in top-right header.
- Split responsibilities between developer tools and account/billing settings.
- Apply a cohesive editorial fintech visual language across web surfaces.

## Route Ownership

- `/` -> chat workspace (default)
- `/chat` -> compatibility route for chat workspace
- `/developer` -> API keys and usage-centric workflows
- `/settings` -> profile, billing/payment flows, account preferences
- `/billing` -> compatibility page linking to `/settings` and `/developer`
- `/auth` -> auth entry and redirect to `/` on success

## Navigation Model

- Left app sidebar focuses on chat-first navigation.
- Header actions are peer-level:
  - `Developer Panel`
  - `Settings` (avatar + label)
  - theme toggle

## Visual Direction (Option A)

- Warm neutral base with atmospheric gradients.
- Elevated cards with soft borders and gentle shadows.
- Clear typography hierarchy and stronger component contrast.
- Subtle motion and responsive layouts for desktop/mobile parity.

## Non-Goals

- No API contract changes.
- No billing formula changes.
- No provider routing behavior changes.
