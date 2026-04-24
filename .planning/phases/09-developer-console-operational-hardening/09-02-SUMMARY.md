---
phase: 09-developer-console-operational-hardening
plan: "02"
subsystem: web-console
tags: [billing, api-keys, model-catalog, bdt-compliance, pdf-invoices, next-js]
dependency_graph:
  requires: ["09-01"]
  provides: [billing-ui, api-key-management-ui, model-catalog-ui, invoice-pdf-download]
  affects: [apps/web-console]
tech_stack:
  added: ["@react-pdf/renderer@4.4.1"]
  patterns: [server-components, client-components, cursor-pagination, explicit-json-decoders, bdt-regulatory-compliance]
key_files:
  created:
    - apps/web-console/components/billing/billing-overview.tsx
    - apps/web-console/components/billing/checkout-modal.tsx
    - apps/web-console/components/billing/checkout-modal.test.tsx
    - apps/web-console/components/billing/ledger-table.tsx
    - apps/web-console/components/billing/ledger-csv-export.tsx
    - apps/web-console/components/billing/invoice-list.tsx
    - apps/web-console/components/api-keys/api-key-list.tsx
    - apps/web-console/components/api-keys/api-key-create-form.tsx
    - apps/web-console/components/api-keys/revoke-confirm-panel.tsx
    - apps/web-console/components/catalog/model-catalog-table.tsx
    - apps/web-console/app/console/billing/page.tsx
    - apps/web-console/app/console/billing/[invoiceId]/download/route.ts
    - apps/web-console/app/console/api-keys/page.tsx
    - apps/web-console/app/console/catalog/page.tsx
  modified:
    - apps/web-console/lib/control-plane/client.ts
    - apps/web-console/app/console/layout.tsx
    - apps/web-console/package.json
decisions:
  - "[09-02] renderToBuffer typed via React.ComponentProps<typeof Document> — avoids importing ReactPDF namespace from CommonJS export = module while satisfying renderToBuffer's DocumentProps constraint"
  - "[09-02] BillingOverview Buy Credits uses anchor link (?action=buy) not inline modal — overview tab is a server component; checkout modal client state handled on client re-render"
  - "[09-02] LedgerCsvExport extracted to separate use client file — keeps LedgerTable a pure server component while enabling browser Blob/URL CSV download"
metrics:
  duration: "~35min"
  completed_date: "2026-04-11"
  tasks_completed: 2
  tasks_total: 3
  files_created: 14
  files_modified: 3
---

# Phase 09 Plan 02: Billing, API Keys, and Model Catalog Console Pages Summary

Billing console pages with balance/ledger/invoices tabs, checkout modal with BDT regulatory compliance, API key CRUD UI with inline revoke confirmation, and model catalog with pricing — all using @react-pdf/renderer for PDF invoice downloads and Next.js 15 server components throughout.

## Tasks Completed

| Task | Name | Commit | Key Files |
|------|------|--------|-----------|
| 1 | Control-plane client extensions, nav update, billing pages | 843c0e1 | client.ts, layout.tsx, billing/page.tsx, checkout-modal.tsx, checkout-modal.test.tsx, ledger-table.tsx, invoice-list.tsx, [invoiceId]/download/route.ts |
| 2 | API key management page and model catalog page | baf8091 | api-key-list.tsx, api-key-create-form.tsx, revoke-confirm-panel.tsx, model-catalog-table.tsx, api-keys/page.tsx, catalog/page.tsx |
| 3 | Verify billing, API key, and catalog console pages | auto-approved | checkpoint:human-verify auto-approved per execution context |

## What Was Built

### Control-plane client extensions (`lib/control-plane/client.ts`)
Added 8 new exported interfaces (`BalanceSummary`, `LedgerEntry`, `LedgerPage`, `Invoice`, `CheckoutOptions`, `CheckoutRail`, `CheckoutInitiateResponse`, `ApiKey`, `CatalogModel`) and 11 new exported async functions following the existing explicit JSON decoder pattern — no `as` casts, no `any`. Helper decoders: `decodeLedgerEntry`, `decodeInvoice`, `decodeCheckoutRail`, `decodeApiKey`, `decodeCatalogModel`, `readNumberField`.

### Billing page (`/console/billing`)
Three-tab page (Overview / Ledger / Invoices) implemented as Next.js 15 server component. Tab switching via `?tab=` search param. Overview shows balance card with `available_credits`, tax profile settings link to `/console/settings/billing`, and recent 5 transactions table. Ledger tab shows `LedgerTable` with type filter links and cursor pagination. Invoices tab shows `InvoiceList`.

### Checkout modal (`components/billing/checkout-modal.tsx`)
Client component fetching rails on mount, radio-button rail selection, credit amount picker with +/- buttons. **BDT Regulatory Compliance:** `formatPrice()` uses `Intl.NumberFormat` with the account's local currency only — no USD equivalent, no FX rate, no conversion language for any customer. Exported `formatPrice` helper for independent unit testing.

### BDT compliance test (3/3 passing)
Verifies: BDT price contains "1,200", no "USD"/"exchange"/"conversion"/"rate" in output. USD formats as "$10.00". All 4 currencies (BDT/USD/EUR/GBP) produce no FX language.

### Ledger table with CSV export
`LedgerTable` server component with type filter anchors, column: Type/Credits/Description/Date, cursor-based Previous/Next pagination. `LedgerCsvExport` client component handles browser Blob + `URL.createObjectURL` download.

### Invoice PDF download (`/console/billing/[invoiceId]/download`)
Route handler fetches invoice via `getInvoice()`, builds PDF with `@react-pdf/renderer` `renderToBuffer`. Invoice doc includes header, metadata rows, line items table with totals, footer. Returns `Uint8Array` as `application/pdf` response.

### API key management
`ApiKeyList` server component: status badges (Active=#10b981, Revoked=#dc2626, Expired=#6b7280), monospace redacted suffix, Rotate key link, inline Revoke trigger. `RevokeConfirmPanel` client component: inline panel (not browser confirm()), "Revoking this key immediately blocks all requests using it. This cannot be undone.", calls `router.refresh()` after successful revoke. `ApiKeyCreateForm` client component: shows secret once in highlighted box with "This is the only time your key secret will be shown. Copy it now." + clipboard copy button.

### Model catalog
`ModelCatalogTable` server component: columns Model/Capabilities/Input (per 1M tokens)/Output (per 1M tokens)/Status. Pricing as `toLocaleString()` integers. Lifecycle "active" → "Available" in green, else "Unavailable" in muted.

### Navigation
Console layout updated with 4 new nav links below Members: Billing, API Keys, Analytics, Model Catalog.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] TypeScript error in PDF download route — `renderToBuffer` type mismatch**
- **Found during:** Task 1 TypeScript check
- **Issue:** `React.createElement(Document, null, ...)` returned `ReactElement<unknown>` which is not assignable to `renderToBuffer`'s expected `ReactElement<DocumentProps>`. Also `Buffer` not assignable to `BodyInit`.
- **Fix 1:** Changed `null` props to `{} as React.ComponentProps<typeof Document>` to satisfy type constraint. Annotated return type as `React.ReactElement<React.ComponentProps<typeof Document>>`. `DocumentProps` is inside the `export = ReactPDF` namespace so cannot be imported as named export.
- **Fix 2:** Converted `Buffer` to `new Uint8Array(buffer)` for `Response` body.
- **Files modified:** `apps/web-console/app/console/billing/[invoiceId]/download/route.ts`
- **Commit:** 843c0e1 (inline fix before commit)

**2. [Rule 2 - Missing functionality] Extracted LedgerCsvExport to separate file**
- **Found during:** Task 1 — LedgerTable is a server component but CSV export needs browser APIs
- **Issue:** Cannot mix `"use client"` inside a server component file
- **Fix:** Created `ledger-csv-export.tsx` as a separate `"use client"` component imported by `LedgerTable`
- **Files created:** `apps/web-console/components/billing/ledger-csv-export.tsx`
- **Commit:** 843c0e1

## Verification Results

- `npx vitest run components/billing/checkout-modal.test.tsx`: 3/3 tests passed
- `npx tsc --noEmit`: 0 errors

## Self-Check: PASSED

All created files exist on disk. Both commits verified in git log.
