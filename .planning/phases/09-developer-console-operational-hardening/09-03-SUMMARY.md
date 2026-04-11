---
phase: 09-developer-console-operational-hardening
plan: 03
subsystem: web-console
tags: [analytics, recharts, budget-alerts, billing, charts]
dependency_graph:
  requires: [09-01, 09-02]
  provides: [analytics-page, budget-alerts, spend-notifications]
  affects: [console-layout, billing-page]
tech_stack:
  added: [recharts@3.8.1]
  patterns: [server-component-data-fetch, client-chart-components, api-route-proxy, explicit-json-decoders]
key_files:
  created:
    - apps/web-console/app/console/analytics/page.tsx
    - apps/web-console/components/analytics/usage-chart.tsx
    - apps/web-console/components/analytics/spend-chart.tsx
    - apps/web-console/components/analytics/error-chart.tsx
    - apps/web-console/components/analytics/analytics-table.tsx
    - apps/web-console/components/analytics/time-window-picker.tsx
    - apps/web-console/components/analytics/analytics-controls.tsx
    - apps/web-console/components/billing/budget-alert-form.tsx
    - apps/web-console/components/billing/budget-alert-banner.tsx
    - apps/web-console/app/api/budget/route.ts
  modified:
    - apps/web-console/lib/control-plane/client.ts
    - apps/web-console/app/console/layout.tsx
    - apps/web-console/app/console/billing/page.tsx
    - apps/web-console/package.json
decisions:
  - "AnalyticsControls extracted to separate use-client file — keeps analytics page a pure server component while enabling client-side tab/window navigation via useRouter"
  - "/api/budget route handler bridges client BudgetAlertForm/Banner to server-only client.ts functions — avoids exposing CONTROL_PLANE_BASE_URL or session tokens to browser"
  - "BudgetAlertBanner uses local dismissed state with best-effort DELETE /api/budget — hides banner immediately even if network dismiss fails"
  - "Promise.allSettled for balance/budget in layout — prevents layout render failure if either fetch errors; falls back to zero balance and null threshold"
metrics:
  duration: 20min
  completed: "2026-04-11T06:52:29Z"
  tasks_completed: 1
  files_changed: 14
  checkpoint_task: 1
requirements_satisfied: [CONS-03, BILL-06]
---

# Phase 09 Plan 03: Analytics Page and Budget Alerts Summary

**One-liner:** Recharts-powered analytics page with four tabs, time window/group-by controls, spend threshold form, and persistent alert banner wired into console layout.

## What Was Built

### Analytics Page (/console/analytics)

Server component page with four tabs (Overview, Usage, Spend, Errors). Fetches data from the control plane analytics endpoints based on active tab, group-by, and time window. Overview tab shows four summary cards (total requests, input tokens, output tokens, credits spent) plus a usage line chart. Each tab shows the appropriate chart plus a detail table.

### Chart Components

All charts are `"use client"` components using Recharts:
- `UsageChart` — LineChart with two lines: input tokens (#6366f1) and output tokens (#8b5cf6)
- `SpendChart` — BarChart with credits spent bars (#10b981)
- `ErrorChart` — BarChart with error count (#ef4444) and total requests (#d1d5db)

### AnalyticsControls

Client component wrapping TimeWindowPicker + group-by select, using `useRouter` for navigation. Extracted so the analytics page itself remains a server component.

### TimeWindowPicker

Client component with preset buttons (24h, 7d, 30d, 90d) and expandable custom date range with from/to date inputs and an Apply button.

### AnalyticsTable

Pure server component rendering any data array with named columns. Shows "No usage data / Usage data will appear here after your first API request." when empty.

### Budget Alert Form (billing overview tab)

`"use client"` component with number input (Hive Credits unit label), enforcement note ("Alerts are notifications only..."), and Save button. Calls PUT `/api/budget` route handler.

### Budget Alert Banner (console layout)

`"use client"` component rendered in console layout after VerificationBanner. Shows warning surface (#fef9c3/#fde047) when balance is approaching (≤ threshold × 1.1) or crossed (≤ threshold). "Dismiss alert" button calls DELETE `/api/budget`. Uses local state for immediate hide on dismiss.

### /api/budget Route Handler

Next.js route handler bridging client components to server-only `upsertBudgetThreshold` and `dismissBudgetAlert` from `client.ts`. PUT upserts threshold, DELETE dismisses.

### Client Functions (client.ts additions)

Six new exports with full explicit decoder pattern (no `as` casts):
- `getAnalyticsUsage`, `getAnalyticsSpend`, `getAnalyticsErrors` — analytics fetch with group_by/window/from/to params
- `getBudgetThreshold` — returns `BudgetThreshold | null`
- `upsertBudgetThreshold` — PUT with threshold_credits body
- `dismissBudgetAlert` — POST to dismiss endpoint

## Deviations from Plan

### Auto-added: AnalyticsControls wrapper component

**Found during:** Task 1 implementation

**Issue:** The plan specified TimeWindowPicker and group-by select inside the analytics page, but the analytics page is a server component — client hooks (`useRouter`) can't run in server components.

**Fix:** Extracted `AnalyticsControls` as a separate `"use client"` wrapper component that receives current state as props and uses `useRouter` for navigation. The analytics page remains a pure server component.

**Files modified:** `apps/web-console/components/analytics/analytics-controls.tsx` (new file)

**Commit:** 2dca2d7

### Auto-added: /api/budget route handler

**Found during:** Task 1 implementation

**Issue:** `BudgetAlertForm` and `BudgetAlertBanner` are client components that need to call `upsertBudgetThreshold` and `dismissBudgetAlert`. These server-only functions use `cookies()` and `createClient()` which only work in server contexts. Client components cannot call them directly.

**Fix:** Created `/app/api/budget/route.ts` (PUT + DELETE) that calls the server-side client functions, providing a browser-safe endpoint for the client components.

**Files modified:** `apps/web-console/app/api/budget/route.ts` (new file)

**Commit:** 2dca2d7

## Self-Check: PASSED

All created files verified present on disk. Commit 2dca2d7 confirmed in git log. TypeScript check (`npx tsc --noEmit`) exited 0 with no errors.
