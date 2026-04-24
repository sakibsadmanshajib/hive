---
phase: 09-developer-console-operational-hardening
plan: "01"
subsystem: control-plane
tags: [analytics, invoices, budgets, cursor-pagination, catalog, api]

requires:
  - phase: 08-payments-fx-and-compliance-checkout
    provides: payment_intents table referenced by payment_invoices FK

provides:
  - Analytics aggregation endpoints (usage, spend, errors) with GROUP BY model/api_key/endpoint
  - Invoice list and detail endpoints with cursor-based pagination
  - Budget threshold CRUD (GET/PUT/POST dismiss) with 24-hour notification cooldown
  - Public model catalog endpoint (unauthenticated)
  - Ledger cursor pagination via keyset (id < cursor ORDER BY id DESC)
  - Two database migrations (payment_invoices, account_budget_thresholds)

affects:
  - 09-02 (Console UI — consumes analytics, invoice, budget, catalog APIs)
  - 09-03 (Analytics UI — consumes analytics aggregation endpoints)

tech-stack:
  added: []
  patterns:
    - Keyset pagination avoids offset performance degradation
    - SQL COUNT FILTER for conditional aggregation in analytics
    - ON CONFLICT upsert resets alert_dismissed when threshold updated
    - LogNotifier provides mockable EmailNotifier interface for development
    - JSONB line_items for flexible invoice structure
    - Non-fatal notification pattern (failures logged, don't block caller)

key-files:
  created:
    - supabase/migrations/20260411_01_invoices.sql
    - supabase/migrations/20260411_02_budget_thresholds.sql
    - apps/control-plane/internal/budgets/types.go
    - apps/control-plane/internal/budgets/repository.go
    - apps/control-plane/internal/budgets/service.go
    - apps/control-plane/internal/budgets/http.go
    - apps/control-plane/internal/budgets/notifier.go
    - apps/control-plane/internal/budgets/service_test.go
  modified:
    - apps/control-plane/internal/ledger/http.go
    - apps/control-plane/internal/ledger/repository.go
    - apps/control-plane/internal/ledger/types.go
    - apps/control-plane/internal/usage/http.go
    - apps/control-plane/internal/usage/repository.go
    - apps/control-plane/internal/usage/types.go
    - apps/control-plane/internal/catalog/http.go
    - apps/control-plane/internal/platform/http/router.go
    - apps/control-plane/cmd/server/main.go

commits:
  - hash: 221d73a
    message: "feat(09-01): analytics aggregation, invoice endpoints, and ledger cursor pagination"
  - hash: b920aef
    message: "feat(09-01): budget threshold CRUD, public catalog endpoint, and route wiring"

## Self-Check: PASSED

All artifacts verified:
- [x] payment_invoices migration exists with correct schema
- [x] account_budget_thresholds migration exists with unique constraint
- [x] Budget HTTP handler exports NewHandler, Handler
- [x] Budget service_test.go contains TestCheckThresholds (5/5 pass)
- [x] Analytics handlers exist (handleAnalyticsUsage, handleAnalyticsSpend, handleAnalyticsErrors)
- [x] Public catalog endpoint handlePublicCatalog exists
- [x] Router registers all new routes (analytics, invoices, budgets, catalog)
- [x] main.go wires budgets service with LogNotifier
- [x] Build compiles successfully (go build ./...)

## Deviations

None. All plan tasks completed as specified.

## What This Enables

Console UI (09-02) can now consume:
- GET /api/v1/accounts/current/analytics/{usage,spend,errors} — grouped analytics
- GET /api/v1/accounts/current/invoices — paginated invoice list
- GET /api/v1/accounts/current/invoices/:id — invoice detail
- GET/PUT /api/v1/accounts/current/budget — threshold CRUD
- POST /api/v1/accounts/current/budget/dismiss — alert dismissal
- GET /api/v1/catalog/models — public model catalog
- GET /api/v1/accounts/current/credits/ledger?cursor=X&limit=N — cursor pagination
