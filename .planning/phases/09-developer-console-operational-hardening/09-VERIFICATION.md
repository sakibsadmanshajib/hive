---
phase: 09-developer-console-operational-hardening
verified: 2026-04-11T07:00:00Z
status: human_needed
score: 15/15 must-haves verified
re_verification: false
human_verification:
  - test: "Navigate to /console/billing and verify balance displays with Overview/Ledger/Invoices tabs"
    expected: "Balance card shows available Hive Credits, three tabs render, recent transactions visible"
    why_human: "Server component data fetch requires live Supabase connection and seeded credits"
  - test: "Click 'Buy Credits' and verify checkout modal with BDT compliance"
    expected: "Modal opens, rail selection and amount picker shown, BDT account sees only BDT price — no USD equivalent, no FX rate, no conversion language"
    why_human: "Modal is client-rendered and regulatory compliance requires visual inspection of a BD-country account session"
  - test: "Navigate to /console/api-keys and create a key"
    expected: "Key list displays with status badges, create form shows secret exactly once with copy prompt"
    why_human: "One-time secret display requires live POST to control-plane API and session auth"
  - test: "Navigate to /console/analytics and verify charts"
    expected: "Four tabs (Overview/Usage/Spend/Errors) render with Recharts line/bar charts, time-window picker functional"
    why_human: "Recharts canvas renders client-side and requires live analytics data from control-plane"
  - test: "Set budget alert threshold and verify banner appears"
    expected: "Budget alert form saves threshold, banner appears in layout when balance drops below it"
    why_human: "Banner requires balance/threshold comparison which is live account state"
  - test: "Download invoice PDF"
    expected: "PDF file downloads with invoice number, line items, totals, and payment rail"
    why_human: "@react-pdf/renderer buffer generation requires route handler execution"
  - test: "Prometheus/Grafana stack via monitoring profile"
    expected: "docker compose --profile monitoring up starts Prometheus, Grafana, Alertmanager; dashboards load"
    why_human: "Multi-container stack integration cannot be verified without running docker compose"
---

# Phase 9: Developer Console & Operational Hardening Verification Report

**Phase Goal:** Ship the customer-facing control plane and the operator-facing telemetry needed for launch.
**Verified:** 2026-04-11T07:00:00Z
**Status:** human_needed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Analytics aggregation endpoints return grouped usage, spend, and error data by model/key/endpoint/time | VERIFIED | `handleAnalyticsUsage` exists in `usage/http.go` (2 matches); `GetUsageSummary` in `usage/repository.go` (2 matches); analytics page fetches `getAnalyticsUsage|getAnalyticsSpend|getAnalyticsErrors` (9 matches) |
| 2 | Invoice list and detail endpoints return payment invoice metadata for an account | VERIFIED | `20260411_01_invoices.sql` contains `CREATE TABLE payment_invoices`; `getInvoices` exported from `client.ts`; billing page fetches invoices |
| 3 | Budget threshold CRUD endpoints allow creating, reading, and updating spend alert thresholds | VERIFIED | `budgets/http.go` exists with Handler; `budgets/service.go` NewService; router wires `BudgetsHandler` (4 matches); `20260411_02_budget_thresholds.sql` contains `CREATE TABLE account_budget_thresholds` |
| 4 | Budget threshold check dispatches email notification when balance drops below threshold | VERIFIED | `service.go` calls `s.notifier.SendBudgetAlert`; `EmailNotifier` interface pattern; 5/5 unit tests pass including `TestCheckThresholds_SendsEmailWhenBalanceBelowThreshold` |
| 5 | Ledger list endpoint supports cursor-based pagination for browsing full history | VERIFIED | `getLedgerEntries` exported from `client.ts`; billing page fetches with cursor params (6 total balance/ledger/invoice fetch matches) |
| 6 | Public catalog endpoint returns customer-safe model list with pricing and capabilities | VERIFIED | `handlePublicCatalog` in `catalog/http.go` (2 matches); `getCatalogModels` in `client.ts`; `ModelCatalogTable` in catalog page (2 matches) |
| 7 | Customer can see credit balance and recent transactions on the billing page | VERIFIED | `BillingOverview` component in `billing/page.tsx` (2 matches); `getBalance` exported from `client.ts` |
| 8 | Customer can open checkout modal, select a payment rail, and be redirected to provider | VERIFIED | `checkout-modal.tsx` has `"use client"`, `checkout/initiate` fetch (1 match), `export function formatPrice` |
| 9 | BDT customers see only BDT price — no FX rates, USD equivalents, or conversion language | VERIFIED | Only occurrences of `exchange`/`conversion` in checkout-modal.tsx are inside the REGULATORY comment block (not in display logic); BDT compliance unit test passes 3/3 |
| 10 | Customer can list, create, rotate, and revoke API keys from the console | VERIFIED | `ApiKeyList`, `ApiKeyCreateForm` in api-keys page; `revoke-confirm-panel.tsx` contains "Revoking this key immediately"; `api-key-create-form.tsx` contains "This is the only time"; all 9 API key client functions exported |
| 11 | Customer can browse model catalog with pricing and capability badges | VERIFIED | `ModelCatalogTable` in catalog page; component contains "per 1M tokens" (2 matches) |
| 12 | Customer can view usage analytics with charts and time-window filtering | VERIFIED | `UsageChart` referenced in analytics page (3 matches); `usage-chart.tsx` imports from `"recharts"` (1 match); time-window-picker component exists |
| 13 | Customer can set a spend threshold alert and see a banner when balance is low | VERIFIED | `budget-alert-form.tsx` contains `budget` (1 match); `budget-alert-banner.tsx` contains `threshold` (8 matches); `BudgetAlertBanner` in `layout.tsx` (2 matches) |
| 14 | Control-plane and edge-api expose /metrics with Prometheus counters and histograms | VERIFIED | `metrics.go` contains `hive_http_requests_total`; `TestNewRegistry` in `metrics_test.go`; `promhttp.HandlerFor` registered in `router.go` |
| 15 | Prometheus/Grafana/Alertmanager stack configured with dashboards and alert rules | VERIFIED | `prometheus.yml` targets `control-plane` (2 matches) and `control-plane:8081` (1 match); `hive-overview.json` contains `hive_http_requests_total` (2 matches); `alertmanager.yml` contains `receiver` (2 matches); `docker-compose.yml` has `prometheus`/`monitoring` profile references (11 matches) |

**Score:** 15/15 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `supabase/migrations/20260411_01_invoices.sql` | payment_invoices table | VERIFIED | Contains `CREATE TABLE payment_invoices` |
| `supabase/migrations/20260411_02_budget_thresholds.sql` | account_budget_thresholds table | VERIFIED | Contains `CREATE TABLE account_budget_thresholds` |
| `apps/control-plane/internal/budgets/http.go` | Budget HTTP handler | VERIFIED | Exists, wired in router |
| `apps/control-plane/internal/budgets/service_test.go` | Budget unit tests | VERIFIED | 5 tests, all PASS |
| `apps/control-plane/internal/usage/http.go` | Analytics aggregation endpoints | VERIFIED | Contains `handleAnalyticsUsage` |
| `apps/control-plane/internal/catalog/http.go` | Public catalog endpoint | VERIFIED | Contains `handlePublicCatalog` |
| `apps/control-plane/internal/platform/metrics/metrics.go` | Prometheus registry | VERIFIED | Contains `hive_http_requests_total` |
| `apps/control-plane/internal/platform/metrics/metrics_test.go` | Metrics unit tests | VERIFIED | Contains `TestNewRegistry` |
| `deploy/prometheus/prometheus.yml` | Prometheus scrape config | VERIFIED | Targets control-plane:8081 |
| `deploy/grafana/dashboards/hive-overview.json` | Grafana dashboard | VERIFIED | Contains `hive_http_requests_total` |
| `deploy/alertmanager/alertmanager.yml` | Alert routing config | VERIFIED | Contains `receiver` |
| `apps/web-console/app/console/billing/page.tsx` | Billing page with tabs | VERIFIED | Contains `BillingOverview` |
| `apps/web-console/components/billing/checkout-modal.tsx` | Checkout modal with BDT compliance | VERIFIED | `"use client"`, `REGULATORY` comment, `export function formatPrice`, no display-side FX language |
| `apps/web-console/components/billing/checkout-modal.test.tsx` | BDT compliance unit test | VERIFIED | 3/3 tests pass |
| `apps/web-console/app/console/billing/[invoiceId]/download/route.ts` | PDF invoice download route | VERIFIED | Contains `renderToBuffer` (2 matches) |
| `apps/web-console/app/console/api-keys/page.tsx` | API key management page | VERIFIED | Contains `ApiKeyList`, `ApiKeyCreateForm` |
| `apps/web-console/app/console/catalog/page.tsx` | Model catalog page | VERIFIED | Contains `ModelCatalogTable` |
| `apps/web-console/app/console/analytics/page.tsx` | Analytics page with charts | VERIFIED | Contains `UsageChart`, fetches all 3 analytics endpoints |
| `apps/web-console/components/billing/budget-alert-form.tsx` | Budget threshold form | VERIFIED | `"use client"`, fetches budget endpoint |
| `apps/web-console/components/billing/budget-alert-banner.tsx` | Budget alert banner | VERIFIED | Contains `threshold` |
| `apps/web-console/lib/control-plane/client.ts` | All 9+ new client functions | VERIFIED | 9 exported async functions; zero `as` type assertions |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `platform/http/router.go` | `budgets/http.go` | route registration | WIRED | `BudgetsHandler` found (4 matches) |
| `usage/http.go` | `usage/repository.go` | SQL GROUP BY aggregation | WIRED | `GetUsageSummary` found (2 matches) |
| `cmd/server/main.go` | `internal/budgets` | service wiring | WIRED | `budgets.NewService` found (1 match) |
| `budgets/service.go` | email notification | `EmailNotifier.SendBudgetAlert` | WIRED | Interface + `s.notifier.SendBudgetAlert` call found |
| `billing/page.tsx` | `client.ts` | server fetch | WIRED | `getBalance`, `getLedgerEntries`, `getInvoices` all present (6 total matches) |
| `checkout-modal.tsx` | `/api/v1/accounts/current/checkout/initiate` | client-side fetch | WIRED | `checkout/initiate` found (1 match) |
| `api-keys/page.tsx` | `client.ts` | server fetch | WIRED | `getApiKeys` found (2 matches) |
| `billing-overview.tsx` | `/console/settings/billing` | tax profile link | WIRED | `settings/billing` + `Tax profile` both found |
| `analytics/page.tsx` | `client.ts` | server fetch | WIRED | `getAnalyticsUsage|getAnalyticsSpend|getAnalyticsErrors` found (9 matches) |
| `usage-chart.tsx` | recharts | LineChart import | WIRED | `from "recharts"` found (1 match) |
| `budget-alert-form.tsx` | `/api/v1/accounts/current/budget` | client fetch | WIRED | `budget` keyword found (1 match) |
| `layout.tsx` | `budget-alert-banner.tsx` | layout-level render | WIRED | `BudgetAlertBanner` found (2 matches) |
| `platform/http/router.go` | `metrics/metrics.go` | /metrics endpoint | WIRED | `promhttp.HandlerFor` found (1 match) |
| `prometheus.yml` | control-plane | scrape target | WIRED | `control-plane:8081` found (1 match) |
| `docker-compose.yml` | `prometheus.yml` | volume mount in monitoring profile | WIRED | `prometheus`/`monitoring` found (11 matches) |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| BILL-05 | 09-01, 09-02 | Customer can view invoices, receipts, and itemized spend by model, API key, and time window | SATISFIED | Invoice migrations, invoice list/detail endpoints, billing page with invoices tab, analytics spend grouping |
| BILL-06 | 09-01, 09-03 | Customer can set account-level budgets and spend-threshold notifications in Hive Credits | SATISFIED | Budget threshold CRUD, CheckThresholds with email dispatch (5/5 tests pass), budget-alert-form, budget-alert-banner in layout |
| CONS-01 | 09-02 | Customer can manage balance, top-ups, ledger entries, invoices, and tax profile from web console | SATISFIED | Billing page with balance, ledger, invoices tabs; tax profile settings link wired |
| CONS-02 | 09-02 | Customer can manage API keys, model allowlists, model catalog visibility from web console | SATISFIED | API keys page (list/create/rotate/revoke), model catalog page with pricing |
| CONS-03 | 09-01, 09-03 | Customer can inspect privacy-safe usage analytics, error history, and spend trends | SATISFIED | Analytics aggregation endpoints, analytics page with 4 tabs and Recharts charts, time-window picker |
| OPS-01 | 09-04 | Operators can monitor health, latency, upstream failures, payment, rate-limit, billing events | SATISFIED | Prometheus metrics on both services, Grafana dashboards, Alertmanager rules, Docker Compose monitoring profile |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `components/billing/budget-alert-form.tsx` | 86 | HTML input `placeholder` attribute | Info | Not a code stub — standard HTML form input placeholder, no impact |

No blockers found. No TODO/FIXME/HACK markers. No empty `return null` or stub implementations. TypeScript compiles clean. No `as` casts in `client.ts`.

### Human Verification Required

The following items require a running environment or live session to verify:

#### 1. Billing Page — Balance and Transactions

**Test:** Sign in as a test account, navigate to `/console/billing`
**Expected:** Balance card shows Hive Credits amount, recent transactions table renders, three tabs (Overview/Ledger/Invoices) switch correctly
**Why human:** Server component data fetch requires live Supabase + seeded credits ledger

#### 2. BDT Checkout Modal — Regulatory Visual Check

**Test:** Sign in with an account whose country is BD, open the checkout modal
**Expected:** Only BDT price shown (e.g. "BDT 1,200.00"), zero FX rates, zero USD equivalents, zero conversion language anywhere on screen
**Why human:** Client-rendered modal with live country data; the automated test covers `formatPrice` but not the live modal render path

#### 3. API Key — Secret Shown Once

**Test:** Navigate to `/console/api-keys`, create a key
**Expected:** Secret is displayed exactly once in a highlighted box with "This is the only time" message and Copy button; navigating away hides it permanently
**Why human:** Requires live POST to control-plane API with session auth

#### 4. Analytics Charts — Recharts Renders Correctly

**Test:** Navigate to `/console/analytics`, switch time windows and tabs
**Expected:** Line/bar charts render with data, time-window picker changes data range, grouping selector (model/key/endpoint) works
**Why human:** Recharts renders to canvas client-side; requires live analytics data

#### 5. Budget Alert Banner — End-to-End Flow

**Test:** Set a budget threshold above current balance, verify banner appears in console layout
**Expected:** "Budget Alert" banner visible at top of all console pages; "Dismiss" button removes it
**Why human:** Requires live balance/threshold comparison from control-plane

#### 6. PDF Invoice Download

**Test:** Complete a purchase, navigate to `/console/billing?tab=invoices`, click "Download PDF"
**Expected:** PDF file downloads with Hive header, invoice number, date, line items, totals, tax treatment, payment rail
**Why human:** `@react-pdf/renderer` buffer generation requires the route handler to execute with real invoice data

#### 7. Monitoring Stack — Docker Compose Profile

**Test:** `docker compose -f deploy/docker/docker-compose.yml --profile monitoring up prometheus grafana alertmanager`
**Expected:** Prometheus UI at :9090 shows both targets (control-plane, edge-api) as UP; Grafana at :3000 shows hive-overview dashboard; Alertmanager at :9093 has rules loaded
**Why human:** Multi-container orchestration integration; requires Docker networking between services

---

_Verified: 2026-04-11T07:00:00Z_
_Verifier: Claude (gsd-verifier)_
