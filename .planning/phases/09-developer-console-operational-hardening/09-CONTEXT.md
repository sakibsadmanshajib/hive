# Phase 9: Developer Console & Operational Hardening - Context

**Gathered:** 2026-04-10
**Status:** Ready for planning

<domain>
## Phase Boundary

Ship the customer-facing web console flows (billing, API keys, usage analytics, spend alerts) and operator-facing telemetry needed for launch. The console builds on the existing Next.js web-console app (auth, profile, settings already exist) and the Go control-plane APIs (accounting, apikeys, catalog, ledger, payments, usage handlers already wired). Operator monitoring is a separate internal tool, not customer-facing.

</domain>

<decisions>
## Implementation Decisions

### Billing & checkout console

- **Checkout flow**: In-page modal triggered by "Buy Credits" button. Modal shows rail selection, amount picker (1,000-credit increments), and final price. Redirects to provider for payment completion.
- **BDT pricing display**: Show final BDT price only. **Do NOT display FX rates, currency exchange language, USD equivalents, or conversion fee breakdowns.** This is a regulatory requirement for Bangladesh — no indication of currency exchange. USD customers see USD price directly.
- **Billing overview**: Current credit balance prominently displayed, last 5 transactions, "Buy Credits" CTA, links to full ledger and invoices.
- **Ledger history**: Paginated sortable table of all transactions (purchases, charges, refunds, adjustments) with type filters, date range picker, and CSV export.
- **Invoices**: Each completed payment generates a downloadable PDF invoice with tax and line items. Invoices listed in a dedicated invoices tab.
- **Tax profile editing**: Stays at /console/settings/billing (already exists from Phase 2). Billing page links to it when tax info is needed during checkout.

### Usage analytics & spend

- **Groupings**: Usage analytics support four dimensions — by model, by API key, by time window, and by endpoint.
- **Visualization**: Charts (line/bar) for trends at the top, detailed sortable table below. Click chart segment to filter table.
- **Time windows**: Presets (24h, 7d, 30d, 90d) plus custom date range picker.
- **Error inspection**: Tab within the analytics page. Analytics page has tabs: Overview, Usage, Spend, Errors. Errors tab shows error rates, status codes, and recent failures by model/key.
- **Model catalog**: Dedicated page showing each model's per-token credit pricing, capabilities, and availability status alongside model info.

### Budget alerts & notifications

- **Notification delivery**: Email notifications plus persistent in-console banner/alert when budget thresholds are approaching or exceeded.
- **Enforcement**: Notify only — thresholds trigger notifications but do not block API requests. The existing credit balance (running to zero) is the hard stop. Avoids accidental self-denial-of-service.

### Operator monitoring

- **Stack**: Prefer Supabase-native observability if available (researcher should investigate). If not, Prometheus metrics endpoints on Go services + Grafana dashboards running in Docker.
- **Signals**: All four categories required:
  1. API health & latency — request rates, error rates, p50/p95/p99 latency by endpoint
  2. Upstream provider status — per-provider success/failure rates, latency, circuit-breaker state
  3. Billing & payment events — payment success/failure rates, ledger posting volume, reservation timeouts
  4. Rate limit & auth events — rate-limit hits, auth failures, revoked key usage attempts
- **Access**: Separate internal tool (Grafana or equivalent), not exposed to customers. Customer console stays clean.
- **Alerting**: Basic Alertmanager (or equivalent) with critical alerts: API error rate spike, upstream provider down, payment webhook failures. Delivers to email or Slack.

### Claude's Discretion

- Spend threshold configuration UX (account-level budget form design, threshold granularity)
- Exact chart library and visualization implementation
- Loading states, skeletons, and empty states across console pages
- PDF invoice template design and generation approach
- Grafana dashboard layout and panel arrangement
- Exact Prometheus metric names and label cardinality

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Requirements
- `.planning/REQUIREMENTS.md` — BILL-05, BILL-06, CONS-01, CONS-02, CONS-03, OPS-01 define the acceptance criteria for this phase

### Phase dependencies
- `.planning/phases/02-identity-account-foundation/` — Console shell, auth flows, profile settings, workspace switcher (foundation for all new console pages)
- `.planning/phases/03-credits-ledger-usage-accounting/` — Ledger schema, balance calculations, usage events (data source for billing and analytics views)
- `.planning/phases/05-api-keys-hot-path-enforcement/` — API key CRUD and resolve (partially complete: 2/6 plans). Per-key policy, budgets, rate limits still pending.
- `.planning/phases/08-payments-fx-and-compliance-checkout/` — Payment rails, FX service, tax calculation, checkout API, webhook handlers

### Existing code
- `apps/web-console/app/console/` — Existing console pages (home, members, settings/profile, settings/billing, setup)
- `apps/control-plane/internal/platform/http/router.go` — Control-plane router with all registered API endpoints and auth middleware pattern
- `apps/control-plane/internal/payments/http.go` — Payments handler (checkout/rails, webhooks)
- `apps/control-plane/internal/apikeys/http.go` — API keys handler (CRUD, policy, rotate, revoke)
- `apps/control-plane/internal/accounting/http.go` — Accounting handler (reservations)
- `apps/control-plane/internal/ledger/` — Ledger service (balance, entries)
- `apps/control-plane/internal/usage/` — Usage handler (events, attempts)
- `apps/control-plane/internal/catalog/` — Model catalog handler (snapshot)
- `supabase/migrations/` — All existing DB migrations (identity, billing, credits, model catalog, API keys, payments, FX)

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- **Web console shell** (`apps/web-console/app/console/layout.tsx`): Console layout with auth, workspace switcher, navigation — all new pages nest inside this
- **Auth middleware** (`apps/control-plane/internal/auth/`): `AuthMiddleware.Require()` pattern for protecting API routes — used consistently across all control-plane handlers
- **Control-plane router** (`apps/control-plane/internal/platform/http/router.go`): Centralized route registration with nil-check pattern for optional handlers
- **Supabase auth client** (`apps/control-plane/internal/auth/client.go`): JWT verification and viewer context resolution
- **Payments HTTP handler**: Checkout options, initiate, and webhook endpoints already built — console just needs to call these APIs
- **API keys handler**: Full CRUD with policy, rotate, revoke, enable/disable — console needs UI wrapper
- **Ledger handler**: Balance and ledger entry endpoints exist — console needs to present them

### Established Patterns
- **Handler pattern**: Each domain has a Handler struct implementing `ServeHTTP` with path-based dispatch
- **ViewerContext**: Account resolution via `auth.Middleware` → `accounts.ViewerContext` for all authenticated endpoints
- **Protected routes**: `cfg.AuthMiddleware.Require(handler)` wraps handlers that need JWT auth
- **Next.js App Router**: Console uses app directory routing with server components, edge middleware for auth

### Integration Points
- New console pages add routes under `apps/web-console/app/console/`
- New control-plane APIs register in `apps/control-plane/internal/platform/http/router.go`
- New DB tables add migrations in `supabase/migrations/`
- Monitoring infrastructure adds to `docker-compose.yml`

</code_context>

<specifics>
## Specific Ideas

- **BDT regulatory constraint**: Never show FX rates, USD equivalents, or "currency exchange" language to Bangladeshi customers. Display final BDT price only. This is a legal/regulatory requirement, not a UX preference.
- Supabase may have built-in observability features (logging, metrics, dashboard) — researcher should investigate before adding Prometheus/Grafana infrastructure.

</specifics>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope.

</deferred>

---

*Phase: 09-developer-console-operational-hardening*
*Context gathered: 2026-04-10*
