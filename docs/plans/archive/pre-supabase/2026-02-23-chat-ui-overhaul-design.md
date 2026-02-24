# Chat UI Overhaul Design

## Goal
- Rebuild the web frontend into a polished, mobile-first chat product that feels close to Claude/ChatGPT/Cursor while preserving current API flows and billing operations.

## Product Direction
- Keep the existing backend contract intact and focus on frontend UX quality.
- Use a hybrid visual language: clean whitespace and calm typography, strong conversational layout, and developer-focused message rendering.
- Prioritize phone and tablet behavior before desktop refinement.

## Scope
- In scope:
  - Full visual overhaul of `/`, `/chat`, and `/billing`.
  - shadcn/ui + Tailwind setup.
  - Responsive app shell with mobile drawer and desktop sidebar.
  - Advanced chat rendering (Markdown + code blocks + copy actions + typing indicator).
  - Theme system (light/dark, persisted preference, system fallback).
  - Better status/feedback states (toasts, loading, error, empty).
- Out of scope:
  - Backend API contract changes.
  - Auth/billing business rule changes.
  - File upload pipeline or multimodal backend features.

## UX and Layout Design
- App shell:
  - Mobile: top bar + slide-out conversation drawer + full-width chat composer.
  - Tablet/Desktop: persistent left sidebar + main chat panel.
- Chat page:
  - Conversation list with active state, last message preview, and quick actions.
  - Message stream with role-based bubbles, timestamps, and retry/copy controls.
  - Sticky composer with model selector, multiline input, send button, and keyboard behavior optimized for touch devices.
- Billing page:
  - Keep existing actions but present in cards and compact panels.
  - Strong hierarchy for credits, usage, and key management.

## Visual System
- Foundation:
  - Tailwind tokens + CSS variables for color, spacing, radius, and shadows.
  - Neutral-first palette with restrained accent colors.
  - Distinct surface levels for sidebar, panels, and message bubbles.
- Typography:
  - Use a modern sans stack with clear hierarchy and readable line-length.
- Motion:
  - Subtle transitions for hover/focus/active and drawer open-close.
  - Lightweight entrance animation for new messages.

## Component Architecture
- Base UI primitives from shadcn/ui:
  - `button`, `card`, `input`, `textarea`, `select`, `sheet`, `dropdown-menu`, `scroll-area`, `separator`, `avatar`, `badge`, `tooltip`, `toast`, `skeleton`.
- Domain components:
  - `ChatShell`, `ConversationList`, `MessageList`, `MessageBubble`, `MessageComposer`, `TypingIndicator`, `ApiKeyPanel`, `ModelSelector`, `UsageSummaryCard`.
- Utilities:
  - Markdown renderer with code block component and copy button.
  - Time formatting helper for relative timestamps.

## Data Flow and State
- Keep existing client-side state for MVP velocity.
- Split chat logic from UI:
  - `useChatSession` hook for API key, conversation state, send flow, pending/error state.
  - Presentation components stay stateless where possible.
- Preserve route-level behavior and existing API endpoints.

## Mobile/Tablet Requirements
- Primary breakpoints:
  - Mobile: `<768px`
  - Tablet: `768px-1023px`
  - Desktop: `>=1024px`
- Interaction requirements:
  - Minimum 44px touch targets.
  - Drawer and composer remain usable with soft keyboard open.
  - No horizontal scrolling in chat flow.

## Reliability and Error Handling
- Show actionable toasts and inline fallbacks for:
  - Auth failures.
  - Insufficient credits.
  - Provider/API errors.
- Maintain optimistic user message append and clear pending states.

## Testing and Verification Strategy
- Add focused frontend tests for:
  - Markdown/code rendering behavior.
  - Conversation reducer/hook state transitions.
  - Mobile drawer open/close behavior.
- Run and verify:
  - `pnpm --filter @hive/web test`
  - `pnpm --filter @hive/web build`

## Success Criteria
- UI feels materially more polished and modern across phone, tablet, and desktop.
- Chat workflow remains stable with existing backend API.
- Billing and usage surfaces remain functional and clearer.
- Groq/Ollama model selection and usage visibility remain obvious in the interface.
