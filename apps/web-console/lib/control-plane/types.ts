// Source-of-truth types for control-plane request/response shapes.
//
// Phase 13 (CONSOLE-13-02): re-exports the canonical interface set from
// `./client`, where the runtime decoders + type guards already live (1500-line
// strict-TS module). This shim exists so consumers can `import { ... } from
// "@/lib/control-plane/types"` without forcing a 50-file refactor on every
// page that already imports from `@/lib/control-plane/client`.
//
// New consumers SHOULD prefer this module. Existing imports from
// `@/lib/control-plane/client` remain valid — both routes resolve the same
// interface objects.
//
// See `.planning/phases/13-console-integration-fixes/13-AUDIT.md` Section B
// for the type-sync gap analysis (zero gaps found at audit time).

export type {
  // Account / viewer surface
  Viewer,
  ViewerAccount,
  ViewerMembership,
  ViewerUser,
  ViewerGates,
  AccountProfile,
  UpdateAccountProfileInput,
  BillingProfile,
  UpdateBillingProfileInput,
  AccountMember,
  // Billing / payments surface
  BalanceSummary,
  LedgerEntry,
  LedgerPage,
  Invoice,
  CheckoutRail,
  CheckoutOptions,
  CheckoutInitiateResponse,
  // API keys surface
  ApiKey,
  // Catalog surface
  CatalogModel,
  // Analytics surface
  UsageSummaryRow,
  SpendSummaryRow,
  ErrorSummaryRow,
  // Budget surface
  BudgetThreshold,
  // Phase 14 — workspace budget / spend-alert / invoice surface
  BudgetSettings,
  SpendAlert,
  InvoiceLineItem,
  InvoiceRecord,
  UpdateBudgetInput,
  CreateSpendAlertInput,
  UpdateSpendAlertInput,
} from "./client";
