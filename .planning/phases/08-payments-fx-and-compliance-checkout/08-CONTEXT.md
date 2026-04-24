# Phase 8: Payments, FX, and Compliance Checkout - Context

**Gathered:** 2026-04-10
**Status:** Ready for planning

<domain>
## Phase Boundary

Let customers buy Hive Credits safely across global (Stripe) and Bangladesh-local (bKash, SSLCommerz) payment rails with reproducible FX and tax math. This phase builds the payment intent abstraction, provider integrations, webhook reconciliation, FX snapshots, and tax evidence capture. It does not include invoice rendering, spend dashboards, budget alerts, or subscription plans.

</domain>

<decisions>
## Implementation Decisions

### Payment rail selection
- Auto-detect available rails by customer's `country_code` from their account profile: BD → bKash + SSLCommerz + Stripe; all others → Stripe only.
- BD customers see all three rails with equal presentation — no preferred/default rail.
- Credit purchase options: predefined tiers (1K, 5K, 10K, 50K, 100K) plus a "Custom amount" field accepting any multiple of 1,000.
- Min/max purchase limits are rail-specific — Claude determines exact thresholds based on each provider's transaction limits.

### FX snapshot lifecycle
- Exchange rate source: multi-source with fallback. Primary: XE API. Fallback: cached last-known rate if XE is unavailable. Admin override available for emergencies.
- FX quote validity: session-based. Quote is valid for the duration of the checkout session. If the customer leaves and returns, a new quote with a fresh rate is generated.
- FX conversion fee: **5%** (not 3% as in the current REQUIREMENTS.md BILL-04 text — user decision to increase). Baked into the displayed BDT price, not shown as a separate line item.
- **NOTE:** REQUIREMENTS.md BILL-04 references "3% conversion fee" — this should be updated to 5% to match this decision.

### Checkout flow shape
- Server-side payment intent + redirect: control-plane creates a payment intent, returns a provider-hosted checkout URL, console redirects customer there, webhook confirms completion.
- Credits posted instantly on Stripe webhook success. BD rails (bKash, SSLCommerz) get a short confirmation delay (minutes, not days) before posting credits due to different webhook reliability characteristics.
- Checkout API lives inside the existing control-plane service as a new `payments` package — same DB, same deployment. Already has direct access to the ledger and profiles services.
- Failed/expired payment intents: auto-cancel with notification in the console. Customer sees an error with a "retry" button. No credits posted. Ledger stays clean.

### Tax and surcharge rules
- Full tax calculation at checkout — calculate and apply VAT/GST/sales tax based on customer's country and business type at purchase time.
- BD tax treatment: Claude's discretion — research current Bangladesh digital services tax rules and implement correctly.
- Provider fees (bKash ~1.5%, SSLCommerz ~2%, Stripe ~2.9%) absorbed by Hive. Customer sees the same credit price regardless of rail. No rail-specific surcharges.
- First purchase requires complete billing profile (name, country, business type, tax ID if applicable). Subsequent purchases use stored profile. This fulfills the Phase 2 decision to defer billing profile collection to checkout time.

### Claude's Discretion
- FX rate refresh cadence based on XE API limits and rate volatility patterns.
- Exact rail-specific min/max transaction limits based on provider documentation.
- Bangladesh digital services tax rules (VAT rate, business exemptions, BIN validation).
- Exact payment intent schema, webhook signature verification approach, and retry/reconciliation mechanics.
- Exact confirmation delay duration for BD rails before credit posting.
- International tax treatment beyond BD (whether to apply tax for non-BD customers at this stage).

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Phase scope and requirements
- `.planning/ROADMAP.md` § "Phase 8: Payments, FX, and Compliance Checkout" — Phase goal, success criteria, plan breakdown, and dependency on Phases 2-3.
- `.planning/REQUIREMENTS.md` § "Billing & Payments" — Defines BILL-03 (multi-rail credit purchase), BILL-04 (FX snapshot + conversion fee — **note: fee is 5% per user decision, not 3% as written**), BILL-07 (country/business/tax/surcharge compliance).
- `.planning/PROJECT.md` § "Context" — States prepaid Hive Credits commercial model, 100K credits per 1 USD, 1K increment top-ups, BDT pricing from XE data.

### Carry-forward identity and ledger model
- `.planning/phases/02-identity-account-foundation/02-CONTEXT.md` § "Account profile and billing identity capture" — Billing profile collection deferred to checkout time. Core profile has country_code and account_type.
- `.planning/phases/03-credits-ledger-usage-accounting/03-CONTEXT.md` § "Workspace wallet and attribution hierarchy" — Workspace-level wallet is the only real balance. `grant` entry type for credit purchases. Immutable ledger with idempotency.

### Existing schemas and services
- `supabase/migrations/20260328_02_billing_identity_profiles.sql` — `account_billing_profiles` table with legal entity, VAT, tax ID, country/state fields.
- `supabase/migrations/20260330_01_credits_ledger.sql` — `credit_ledger_entries` table with grant/adjustment/reservation types and idempotency.
- `apps/control-plane/internal/ledger/` — Ledger service with PostEntryInput, BalanceSummary, EntryTypeGrant for credit posting.
- `apps/control-plane/internal/profiles/` — Profile service with BillingProfile, AccountProfile types and validation.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `ledger.Service` / `ledger.PostEntryInput`: Use `EntryTypeGrant` with positive `credits_delta` to post purchased credits. Idempotency already enforced via `(account_id, entry_type, idempotency_key)`.
- `profiles.Service`: `GetBillingProfile` / `UpdateBillingProfile` for checkout profile gate. `GetAccountProfile` for country_code-based rail detection.
- `account_billing_profiles` table: Already has all fields needed for tax evidence (legal_entity_type, vat_number, tax_id_type, tax_id_value, country_code).

### Established Patterns
- Control-plane internal packages follow: types.go, service.go, repository.go, http.go pattern.
- Supabase Postgres is the authoritative transactional store. Redis for hot-path caches only.
- Router wiring in `apps/control-plane/internal/platform/http/router.go` — new `payments` package handler follows existing `RouterConfig` pattern.
- Docker-only development with all services running in containers.

### Integration Points
- New `payments` package in `apps/control-plane/internal/payments/` — depends on ledger (credit posting), profiles (billing profile gate, tax data), and new FX rate service.
- New Supabase migration for: payment_intents, payment_events/webhooks, fx_snapshots tables.
- Webhook endpoints need to be unauthenticated (provider callbacks) but signature-verified per provider.
- Web console will call control-plane checkout API to initiate purchase and get redirect URL.

</code_context>

<specifics>
## Specific Ideas

- User explicitly changed the FX conversion fee from 3% to 5% — this is a commercial decision that overrides the current REQUIREMENTS.md text.
- Fee transparency is intentionally hidden from customers for now — no line-item breakdown of FX fees or provider costs.
- Future tenant settings may expose fee transparency as an opt-in feature — not in this phase.
- BD rails get equal presentation because the user doesn't want to favor any specific provider.
- Session-based FX quotes rather than clock-based — aligns with a smoother checkout UX where the customer isn't racing a timer.

</specifics>

<deferred>
## Deferred Ideas

- **Tenant-level fee transparency toggle** — Allow accounts to opt in to seeing FX fee as a separate line item. Future phase or settings enhancement.
- **Invoice rendering and receipt generation** — Phase 9 (BILL-05).
- **Spend dashboards and budget alerts** — Phase 9 (BILL-06).
- **Subscription/recurring billing plans** — Explicitly out of scope for v1.
- **Update REQUIREMENTS.md BILL-04** — Change "3% conversion fee" to "5% conversion fee" to match user decision.

</deferred>

---

*Phase: 08-payments-fx-and-compliance-checkout*
*Context gathered: 2026-04-10*
