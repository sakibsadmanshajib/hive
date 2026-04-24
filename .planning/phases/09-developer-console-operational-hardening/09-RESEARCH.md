# Phase 9: Developer Console & Operational Hardening - Research

**Researched:** 2026-04-10
**Domain:** Next.js 15 console UI, Go Prometheus instrumentation, PDF generation, Grafana/Alertmanager
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Billing & checkout console**
- Checkout flow: In-page modal triggered by "Buy Credits" button. Modal shows rail selection, amount picker (1,000-credit increments), and final price. Redirects to provider for payment completion.
- BDT pricing display: Show final BDT price only. **Do NOT display FX rates, currency exchange language, USD equivalents, or conversion fee breakdowns.** This is a regulatory requirement for Bangladesh. USD customers see USD price directly.
- Billing overview: Current credit balance prominently displayed, last 5 transactions, "Buy Credits" CTA, links to full ledger and invoices.
- Ledger history: Paginated sortable table of all transactions (purchases, charges, refunds, adjustments) with type filters, date range picker, and CSV export.
- Invoices: Each completed payment generates a downloadable PDF invoice with tax and line items. Invoices listed in a dedicated invoices tab.
- Tax profile editing: Stays at /console/settings/billing (already exists from Phase 2). Billing page links to it when tax info is needed during checkout.

**Usage analytics & spend**
- Groupings: Usage analytics support four dimensions — by model, by API key, by time window, and by endpoint.
- Visualization: Charts (line/bar) for trends at the top, detailed sortable table below. Click chart segment to filter table.
- Time windows: Presets (24h, 7d, 30d, 90d) plus custom date range picker.
- Error inspection: Tab within the analytics page. Analytics page has tabs: Overview, Usage, Spend, Errors.
- Model catalog: Dedicated page showing each model's per-token credit pricing, capabilities, and availability status.

**Budget alerts & notifications**
- Notification delivery: Email notifications plus persistent in-console banner/alert when budget thresholds are approaching or exceeded.
- Enforcement: Notify only — thresholds trigger notifications but do not block API requests.

**Operator monitoring**
- Stack: Prefer Supabase-native observability if available. If not, Prometheus metrics endpoints on Go services + Grafana dashboards running in Docker.
- Signals: API health & latency, upstream provider status, billing & payment events, rate limit & auth events.
- Access: Separate internal tool (Grafana or equivalent), not exposed to customers.
- Alerting: Basic Alertmanager with critical alerts delivering to email or Slack.

### Claude's Discretion
- Spend threshold configuration UX (account-level budget form design, threshold granularity)
- Exact chart library and visualization implementation
- Loading states, skeletons, and empty states across console pages
- PDF invoice template design and generation approach
- Grafana dashboard layout and panel arrangement
- Exact Prometheus metric names and label cardinality

### Deferred Ideas (OUT OF SCOPE)
None — discussion stayed within phase scope.
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| BILL-05 | Customer can view invoices, receipts, and itemized spend by model, API key, and time window | Ledger API exists (`/api/v1/accounts/current/credits/ledger`); usage events API exists (`/api/v1/accounts/current/usage-events`); need invoice list/download endpoints + PDF generation library |
| BILL-06 | Customer can set account-level budgets and spend-threshold notifications in Hive Credits | Needs new DB migration for `account_budget_thresholds` table + new control-plane API + background checker or trigger-based notification dispatch |
| CONS-01 | Customer can manage balance, top-ups, ledger entries, invoices, and tax profile from web console | Console pages needed: /console/billing (overview+ledger+invoices), /console/billing/checkout modal; existing checkout APIs at `/api/v1/accounts/current/checkout/*` are ready |
| CONS-02 | Customer can manage API keys, model allowlists, model catalog visibility, and pricing from web console | API keys CRUD + policy update APIs are complete in control-plane; public catalog endpoint needed for customer-facing view; console pages needed at /console/api-keys and /console/catalog |
| CONS-03 | Customer can inspect privacy-safe usage analytics, error history, and spend trends by account, key, model, and time window | Existing usage events and ledger entries APIs need aggregation layer for analytics; new aggregated query endpoints needed for grouped/time-windowed data |
| OPS-01 | Hive operators can monitor health, latency, upstream failures, payment workflows, rate-limit events, and billing events | Prometheus instrumentation in Go services + Grafana + Alertmanager in docker-compose; Supabase-native observability covers DB signals but not application-level metrics |
</phase_requirements>

---

## Summary

Phase 9 is a UI-plus-instrumentation phase. The control-plane APIs for billing, ledger, checkout, and API-key management are already wired — the main backend work is adding aggregation query endpoints (analytics by model/key/time/endpoint), an invoice storage-and-PDF system, a budget threshold table with notification dispatch, and Prometheus metrics instrumentation across the Go services. The frontend work is building ~8 new Next.js console pages on top of the existing shell.

The operator monitoring question is resolved: Supabase's native observability (Logflare-backed logs dashboard, Prometheus Metrics API) covers Postgres and auth signals but does NOT cover application-level custom signals like upstream provider success rates, payment webhook events, rate-limit hits, or circuit-breaker state. Therefore the plan must include adding `prometheus/client_golang` to the Go control-plane and edge-api services and deploying Grafana + Alertmanager as Docker Compose services.

The chart library decision is resolved: Recharts v3.8.1 is current, React 19 compatible, and well-suited to the line/bar chart patterns required. No additional chart framework is needed — inline Recharts components via server-component data fetching works cleanly with Next.js 15 App Router.

**Primary recommendation:** Build analytics aggregation endpoints first (they unblock all four analytics console tabs), then wire console pages in plan order, and add Prometheus instrumentation as a dedicated plan that also sets up docker-compose monitoring services.

---

## Standard Stack

### Core (already in project)
| Library | Version | Purpose | Status |
|---------|---------|---------|--------|
| Next.js | 15.1.0 | Console app framework (App Router, server actions) | Installed |
| React | 19.0.0 | UI rendering | Installed |
| @supabase/ssr | ^0.6.1 | Session/cookie auth for server components | Installed |
| pgx/v5 | v5.7.2 | Postgres queries in Go control-plane | Installed |
| stripe-go | v84.4.1 | Already wired for checkout | Installed |

### New additions needed

| Library | Version | Purpose | Why |
|---------|---------|---------|-----|
| recharts | 3.8.1 | Line/bar charts for usage analytics | React 19 compatible, composable, widely used |
| @react-pdf/renderer | 4.4.1 | Server-side PDF invoice generation | Pure JS/TS, generates PDF from React component tree, no headless browser needed |
| prometheus/client_golang | v1.23.2 | Prometheus metrics for Go services | Official library, promhttp.Handler() exposes /metrics endpoint |

### Supporting (optional, discretionary)
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| Grafana (Docker image) | latest | Operator dashboard | Added to docker-compose monitoring profile |
| Alertmanager (Docker image) | latest | Alert routing to email/Slack | Added alongside Grafana |
| Prometheus (Docker image) | latest | Metric scraping and storage | Scrapes /metrics from control-plane + edge-api |

**Installation (console):**
```bash
cd apps/web-console
npm install recharts @react-pdf/renderer
```

**Installation (control-plane, in go.mod):**
```bash
cd apps/control-plane
go get github.com/prometheus/client_golang@v1.23.2
```

**Version verification (confirmed 2026-04-10):**
- `recharts`: 3.8.1 (npm verified)
- `@react-pdf/renderer`: 4.4.1 (npm verified)
- `prometheus/client_golang`: v1.23.2 (Go proxy verified)

---

## Architecture Patterns

### Recommended Project Structure (additions only)

```
apps/web-console/app/console/
├── billing/                    # NEW — billing overview, ledger, invoices
│   ├── page.tsx                # Balance + last-5 + Buy Credits + tabs
│   └── [invoiceId]/
│       └── download/route.ts   # PDF download route
├── analytics/                  # NEW — usage/spend/error tabs
│   └── page.tsx
├── api-keys/                   # NEW — list, create, policy, rotate, revoke
│   └── page.tsx
└── catalog/                    # NEW — model catalog view
    └── page.tsx

apps/control-plane/internal/
├── ledger/
│   └── http.go                 # ADD invoice list + invoice detail endpoints
├── usage/
│   └── http.go                 # ADD aggregation endpoints (by model, key, time, endpoint)
├── budgets/                    # NEW package — budget thresholds + notification dispatch
│   ├── types.go
│   ├── service.go
│   ├── repository.go
│   └── http.go
└── platform/
    └── metrics/                # NEW package — Prometheus registry + metric definitions
        └── metrics.go

deploy/docker/
├── prometheus/
│   └── prometheus.yml          # Scrape config for control-plane:8081, edge-api:8080
├── grafana/
│   └── dashboards/             # Pre-provisioned dashboard JSON
└── alertmanager/
    └── alertmanager.yml        # Email/Slack routing

supabase/migrations/
└── 20260410_03_budget_thresholds.sql   # NEW — account_budget_thresholds table
└── 20260410_04_invoices.sql            # NEW — payment_invoices table
```

### Pattern 1: New control-plane API endpoints (analytics aggregation)

**What:** The existing `/api/v1/accounts/current/usage-events` returns raw events with simple limit/offset. Analytics tabs need grouped, time-windowed aggregates.

**New endpoints to add:**
```
GET /api/v1/accounts/current/analytics/usage
  ?group_by=model|api_key|endpoint
  &window=24h|7d|30d|90d
  &from=<ISO8601>&to=<ISO8601>

GET /api/v1/accounts/current/analytics/spend
  ?group_by=model|api_key|endpoint
  &window=24h|7d|30d|90d
  &from=<ISO8601>&to=<ISO8601>

GET /api/v1/accounts/current/analytics/errors
  ?group_by=model|api_key
  &window=24h|7d|30d|90d
```

**Pattern:** SQL GROUP BY aggregation in repository layer, handler follows existing usage.Handler struct pattern (ServeHTTP with path dispatch, auth via ViewerFromContext).

### Pattern 2: Next.js server component data fetching

**What:** Console pages use server components for data fetching (no client-side API calls except for modals/interactive elements).

```typescript
// apps/web-console/app/console/billing/page.tsx
export default async function BillingPage() {
  const [balance, entries, invoices] = await Promise.all([
    getBalance(),       // /api/v1/accounts/current/credits/balance
    getLedgerEntries({ limit: 5 }),
    getInvoices(),      // /api/v1/accounts/current/invoices
  ]);

  return <BillingOverview balance={balance} recentEntries={entries} invoices={invoices} />;
}
```

**Client components** are used only for interactive elements: the checkout modal, date range picker, chart click-to-filter, CSV export button.

### Pattern 3: Checkout modal (client component)

The "Buy Credits" modal must be a client component that:
1. Fetches rails from `/api/v1/accounts/current/checkout/rails` (already built)
2. Calls `/api/v1/accounts/current/checkout/initiate` with chosen rail + credit amount
3. Receives `redirect_url` in response and navigates to it
4. **Never shows FX rate, USD equivalent, or "currency exchange" language to BDT accounts**

```typescript
// apps/web-console/components/billing/checkout-modal.tsx
"use client";

interface CheckoutModalProps {
  accountCountryCode: string; // 'BD' triggers BDT-only display
}

// Display logic: if accountCountryCode === 'BD', show only final BDT price.
// The backend already returns the correct currency price per account context.
// No math or conversion display needed client-side.
```

### Pattern 4: Recharts in Next.js 15 App Router

Recharts renders on the client side (requires `"use client"` directive). Server components fetch data, pass as props to chart client components.

```typescript
// apps/web-console/components/analytics/usage-chart.tsx
"use client";
import { LineChart, Line, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer } from "recharts";

interface UsageDataPoint {
  timestamp: string;
  inputTokens: number;
  outputTokens: number;
}

interface UsageChartProps {
  data: UsageDataPoint[];
}

export function UsageLineChart({ data }: UsageChartProps) {
  return (
    <ResponsiveContainer width="100%" height={300}>
      <LineChart data={data}>
        <CartesianGrid strokeDasharray="3 3" />
        <XAxis dataKey="timestamp" />
        <YAxis />
        <Tooltip />
        <Line type="monotone" dataKey="inputTokens" stroke="#6366f1" />
        <Line type="monotone" dataKey="outputTokens" stroke="#8b5cf6" />
      </LineChart>
    </ResponsiveContainer>
  );
}
```

### Pattern 5: PDF invoice generation (server route)

`@react-pdf/renderer` generates PDFs server-side in a Next.js Route Handler. No headless browser needed.

```typescript
// apps/web-console/app/console/billing/[invoiceId]/download/route.ts
import { renderToBuffer } from "@react-pdf/renderer";
import { InvoiceDocument } from "@/components/invoices/invoice-document";
import { getInvoice } from "@/lib/control-plane/client";

export async function GET(_req: Request, { params }: { params: { invoiceId: string } }) {
  const invoice = await getInvoice(params.invoiceId);
  const buffer = await renderToBuffer(<InvoiceDocument invoice={invoice} />);
  return new Response(buffer, {
    headers: {
      "Content-Type": "application/pdf",
      "Content-Disposition": `attachment; filename="invoice-${params.invoiceId}.pdf"`,
    },
  });
}
```

### Pattern 6: Prometheus instrumentation in Go

Add a `/metrics` endpoint to the control-plane alongside the existing `/health`. Each domain registers its own counters/histograms against a shared registry.

```go
// apps/control-plane/internal/platform/metrics/metrics.go
package metrics

import "github.com/prometheus/client_golang/prometheus"

type Registry struct {
    HTTPRequestsTotal    *prometheus.CounterVec
    HTTPRequestDuration  *prometheus.HistogramVec
    UpstreamRequestsTotal *prometheus.CounterVec
    PaymentEventsTotal   *prometheus.CounterVec
    RateLimitHitsTotal   *prometheus.CounterVec
    AuthFailuresTotal    *prometheus.CounterVec
}

func NewRegistry() (*Registry, *prometheus.Registry) {
    reg := prometheus.NewRegistry()
    r := &Registry{
        HTTPRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
            Name: "hive_http_requests_total",
            Help: "Total HTTP requests by endpoint and status code",
        }, []string{"endpoint", "method", "status_class"}),
        // ... other metrics
    }
    reg.MustRegister(r.HTTPRequestsTotal, r.HTTPRequestDuration, ...)
    return r, reg
}
```

```go
// In router.go — add /metrics endpoint
import "github.com/prometheus/client_golang/prometheus/promhttp"

mux.Handle("/metrics", promhttp.HandlerFor(promReg, promhttp.HandlerOpts{}))
```

### Pattern 7: Budget threshold table + notification check

```sql
-- supabase/migrations/20260410_03_budget_thresholds.sql
CREATE TABLE account_budget_thresholds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    threshold_credits BIGINT NOT NULL,         -- in Hive Credits
    threshold_pct INT,                          -- optional: 80%, 90%, 100%
    last_notified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
```

Notification dispatch approach: background goroutine in control-plane (or Supabase Edge Function via pg_cron) checks balance vs threshold after each ledger entry. Sends email via SMTP or Supabase Auth email; posts in-console banner via stored flag read at console load.

### Anti-Patterns to Avoid

- **Do NOT fetch raw usage_events client-side and aggregate in JS** — the event volume will be too large. All aggregation must happen in SQL GROUP BY queries in the control-plane repository layer.
- **Do NOT embed Prometheus metrics exposition inside the customer-facing /api/* routes** — keep `/metrics` as a separate internal endpoint not reachable by customer JWT auth.
- **Do NOT use `"use client"` on billing/analytics page.tsx** — data fetching stays server-side; only chart and modal subcomponents are client.
- **Do NOT show BDT customers FX rates, USD equivalents, or currency conversion language** — this is a regulatory requirement, not a UX preference.
- **Do NOT use Recharts 2.x** — the project uses React 19.0.0; only Recharts 3.x (current: 3.8.1) is compatible.
- **Do NOT use Puppeteer for PDF generation** — adds 100+ MB binary, does not work cleanly in Docker without additional config; `@react-pdf/renderer` is the correct choice.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Chart rendering | Custom SVG chart components | recharts 3.8.1 | Handles axes, tooltips, responsive containers, animation |
| PDF binary generation | Custom PDF byte-builder | @react-pdf/renderer 4.4.1 | Handles font embedding, page layout, tables |
| Prometheus metrics exposition | Custom text format writer | promhttp.HandlerFor() | Handles content negotiation, registry scraping, compression |
| Prometheus metric types | Custom counters/gauges | prometheus.NewCounterVec, NewHistogramVec | Thread-safe, label validation, standard naming |
| CSV export | Manual string concat | Array.map + join with quoted fields | Handle commas and quotes in values correctly |

---

## API Gap Analysis

### What already exists (callable from console today)

| API | Route | Status |
|-----|-------|--------|
| Credit balance | GET /api/v1/accounts/current/credits/balance | Ready |
| Ledger entries | GET /api/v1/accounts/current/credits/ledger?limit=N | Ready — needs pagination cursor |
| Usage events (raw) | GET /api/v1/accounts/current/usage-events | Ready — needs aggregation layer |
| Request attempts (raw) | GET /api/v1/accounts/current/request-attempts | Ready |
| API key list | GET /api/v1/accounts/current/api-keys | Ready |
| API key create/rotate/revoke | POST /api/v1/accounts/current/api-keys/* | Ready |
| API key policy update | POST /api/v1/accounts/current/api-keys/{id}/policy | Ready |
| Checkout rails | GET /api/v1/accounts/current/checkout/rails | Ready |
| Checkout initiate | POST /api/v1/accounts/current/checkout/initiate | Ready |
| Catalog snapshot (internal) | GET /internal/catalog/snapshot | Internal only — need customer-facing route |
| Billing profile | GET/PUT /api/v1/accounts/current/billing-profile | Ready |

### What needs to be built (control-plane gaps)

| API | Route | Notes |
|-----|-------|-------|
| Analytics — usage aggregated | GET /api/v1/accounts/current/analytics/usage | New — SQL GROUP BY on usage_events |
| Analytics — spend aggregated | GET /api/v1/accounts/current/analytics/spend | New — SQL GROUP BY on ledger entries |
| Analytics — error aggregated | GET /api/v1/accounts/current/analytics/errors | New — filter on error_code/error_type |
| Invoice list | GET /api/v1/accounts/current/invoices | New — needs invoice table + migration |
| Invoice detail | GET /api/v1/accounts/current/invoices/{id} | New |
| Budget threshold CRUD | GET/PUT /api/v1/accounts/current/budget | New — needs budget table + migration |
| Public model catalog | GET /api/v1/catalog/models | New — customer-safe view of catalog snapshot |
| Prometheus metrics | GET /metrics | New — internal endpoint, not auth-gated |

---

## Common Pitfalls

### Pitfall 1: BDT Regulatory Compliance Breakage
**What goes wrong:** A developer adds "equivalent" or "exchange rate" text to the checkout modal UI without realizing it's a regulatory prohibition, not a style preference.
**Why it happens:** The requirement looks like UX copy guidance, not a legal constraint.
**How to avoid:** The checkout modal component must receive `accountCountryCode` as a prop and suppress all conversion language when `countryCode === 'BD'`. Add a comment in the component file referencing the regulatory requirement. Test both BD and non-BD paths.
**Warning signs:** Any string containing "USD", "FX", "rate", "exchange", or "conversion" in BDT checkout code paths.

### Pitfall 2: Recharts Requires "use client"
**What goes wrong:** Recharts components placed inside server components throw runtime errors because Recharts relies on browser APIs (ResizeObserver, DOM refs).
**Why it happens:** Next.js App Router defaults to server components.
**How to avoid:** All Recharts chart components must have `"use client"` directive. Server components fetch data and pass it as serializable props (plain objects/arrays, not class instances) to the chart client component.
**Warning signs:** `Error: createContext only works in Client Components` or `ReferenceError: document is not defined`.

### Pitfall 3: Analytics Aggregation at Wrong Layer
**What goes wrong:** The console fetches all raw usage events and aggregates them in JavaScript, causing slow load times and memory pressure as event volume grows.
**Why it happens:** Existing raw event endpoints are already available; aggregation in JS looks simpler.
**How to avoid:** Aggregation MUST happen in SQL. Build new repository methods with GROUP BY queries in the control-plane.
**Warning signs:** Any Array.reduce() call over usage event lists with more than ~100 items.

### Pitfall 4: Prometheus Cardinality Explosion
**What goes wrong:** Labels like `{model_alias="gpt-4o-mini-2024-07-18"}` or `{api_key_id="uuid"}` make every unique value a new time-series, causing memory exhaustion in Prometheus.
**Why it happens:** Using high-cardinality identifiers (full model names, UUIDs) as label values.
**How to avoid:** Use low-cardinality labels only: `endpoint` (group as `/v1/responses`, `/v1/chat/completions`, etc.), `method` (GET/POST), `status_class` (2xx/4xx/5xx), `provider` (openrouter/groq), `rail` (stripe/bkash/sslcommerz). Never use API key UUIDs or user IDs as Prometheus labels.
**Warning signs:** Prometheus memory > 1GB, metric cardinality > 10k series.

### Pitfall 5: Invoice PDF Generation Blocks Request Thread
**What goes wrong:** `renderToBuffer()` is synchronous-heavy CPU work. If invoked on the Next.js edge runtime or inside a server action during page load, it blocks other requests.
**Why it happens:** PDF generation is triggered inline rather than in a dedicated route.
**How to avoid:** PDF generation belongs in a dedicated `/console/billing/[invoiceId]/download/route.ts` Route Handler that is only invoked when the user explicitly clicks "Download PDF". Never generate PDFs during page render.
**Warning signs:** `/console/billing` page load times > 1s correlated with invoice list length.

### Pitfall 6: Missing Ledger Pagination Cursor
**What goes wrong:** The ledger list endpoint only supports `?limit=N`. The UI needs paginated navigation through potentially thousands of entries.
**Why it happens:** Existing endpoint was built for "last N" use case, not full pagination.
**How to avoid:** Add `?cursor=<last_entry_id>&limit=N` support to the ledger handler. Use `WHERE id < $cursor ORDER BY id DESC LIMIT $limit` pattern for keyset pagination (no offset drift on inserts).
**Warning signs:** Users report "stuck" on first page of ledger, or using limit=9999 workarounds.

---

## Code Examples

### Analytics aggregation SQL (Go repository)

```go
// Source: project pattern — pgx/v5 with positional params
func (r *Repository) GetUsageSummaryByModel(ctx context.Context, accountID uuid.UUID, from, to time.Time) ([]UsageSummaryRow, error) {
    rows, err := r.db.Query(ctx, `
        SELECT
            model_alias,
            SUM(input_tokens)  AS total_input_tokens,
            SUM(output_tokens) AS total_output_tokens,
            SUM(CASE WHEN hive_credit_delta < 0 THEN ABS(hive_credit_delta) ELSE 0 END) AS total_credits_spent,
            COUNT(*)           AS request_count
        FROM usage_events
        WHERE account_id = $1
          AND created_at >= $2
          AND created_at <  $3
        GROUP BY model_alias
        ORDER BY total_credits_spent DESC
    `, accountID, from, to)
    // ... scan rows
}
```

### Checkout modal (BDT compliance)

```typescript
// apps/web-console/components/billing/checkout-modal.tsx
"use client";

function formatPrice(amount: number, currency: string): string {
  // Never show 'USD equivalent' or FX context for any currency
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency,
    minimumFractionDigits: 2,
  }).format(amount);
}

// The backend returns `price` and `currency` based on account country.
// BD accounts receive BDT price only. No conditional FX display needed.
```

### Prometheus counter increment (Go handler middleware)

```go
// Source: prometheus/client_golang promhttp pattern
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    start := time.Now()
    wrapped := &statusRecorder{ResponseWriter: w, status: 200}

    // ... dispatch to sub-handler ...

    statusClass := fmt.Sprintf("%dxx", wrapped.status/100)
    h.metrics.HTTPRequestsTotal.WithLabelValues(r.URL.Path, r.Method, statusClass).Inc()
    h.metrics.HTTPRequestDuration.WithLabelValues(r.URL.Path).Observe(time.Since(start).Seconds())
}
```

### Budget threshold check (post-ledger-entry hook)

```go
// Check after any credit deduction: if balance < threshold, dispatch notification
func (s *BudgetService) CheckThresholds(ctx context.Context, accountID uuid.UUID, currentBalance int64) error {
    thresholds, err := s.repo.GetActiveThresholds(ctx, accountID)
    if err != nil {
        return err
    }
    for _, t := range thresholds {
        if currentBalance <= t.ThresholdCredits && !t.AlreadyNotifiedThisPeriod() {
            if err := s.notify(ctx, accountID, t, currentBalance); err != nil {
                // log and continue — notification failure must not block the request
                s.logger.Error("budget threshold notification failed", "error", err)
            }
        }
    }
    return nil
}
```

---

## Operator Monitoring Architecture

### Decision: Supabase-native is insufficient for application signals

Supabase provides:
- Postgres performance metrics (connections, cache hit rate, query latency) via Metrics API (Prometheus-compatible endpoint)
- Log analytics via Logflare/Supabase Studio (SQL queryable logs)
- OpenTelemetry export capability (beta, 2025)

Supabase does NOT provide:
- Custom application-level counters (upstream provider success/failure rates)
- Circuit-breaker state metrics
- Payment webhook event counts
- Rate-limit hit counts
- API key auth failure counts
- Per-endpoint p50/p95/p99 latency for Go HTTP handlers

**Verdict (MEDIUM confidence — cross-verified):** Add `prometheus/client_golang` to control-plane and edge-api. Deploy Prometheus + Grafana + Alertmanager as a `monitoring` Docker Compose profile (does not run in production by default, operators opt-in).

### Prometheus scrape targets

```yaml
# deploy/prometheus/prometheus.yml
scrape_configs:
  - job_name: control-plane
    static_configs:
      - targets: ['control-plane:8081']
    metrics_path: /metrics

  - job_name: edge-api
    static_configs:
      - targets: ['edge-api:8080']
    metrics_path: /metrics
```

### Grafana provisioning

Dashboard panels needed (by category from CONTEXT.md):
1. **API health & latency**: `hive_http_requests_total` by status_class + `hive_http_request_duration_seconds` histogram (p50/p95/p99)
2. **Upstream provider status**: `hive_upstream_requests_total{provider, status}` + `hive_circuit_breaker_state{provider}`
3. **Billing & payment events**: `hive_payment_events_total{rail, status}` + `hive_ledger_postings_total`
4. **Rate limit & auth events**: `hive_rate_limit_hits_total{tier}` + `hive_auth_failures_total{reason}`

### Alertmanager rules

```yaml
# Critical alerts only (per CONTEXT.md):
# 1. API error rate spike (4xx/5xx > threshold for 5 min)
# 2. Upstream provider down (provider success rate = 0 for 2 min)
# 3. Payment webhook failures (consecutive failures > 3)
```

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Vitest 3.1.1 (web-console) + `go test` (control-plane) |
| Config file | apps/web-console/vitest.config.ts (existing) |
| Quick run command | `cd apps/web-console && npm run test:unit` |
| Full suite command | `cd apps/web-console && npm run test:unit && cd ../../apps/control-plane && go test ./...` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| BILL-05 | Invoice list + PDF download route return correct data | unit | `go test ./internal/ledger/... -run TestInvoice` | ❌ Wave 0 |
| BILL-05 | Analytics aggregation SQL groups by model/key/endpoint | unit | `go test ./internal/usage/... -run TestAnalytics` | ❌ Wave 0 |
| BILL-06 | Budget threshold table insert + notification check | unit | `go test ./internal/budgets/... -run TestThreshold` | ❌ Wave 0 |
| CONS-01 | Billing page server component renders balance + entries | unit | `npm run test:unit -- billing` | ❌ Wave 0 |
| CONS-01 | Checkout modal displays BDT price only for BD accounts | unit | `npm run test:unit -- checkout-modal` | ❌ Wave 0 |
| CONS-02 | API key list/create/revoke pages call correct endpoints | unit | `npm run test:unit -- api-keys` | ❌ Wave 0 |
| CONS-03 | Analytics page aggregation endpoint called with correct params | unit | `npm run test:unit -- analytics` | ❌ Wave 0 |
| OPS-01 | Prometheus metrics endpoint returns valid text/plain | unit | `go test ./internal/platform/metrics/... -run TestMetrics` | ❌ Wave 0 |

### Wave 0 Gaps
- [ ] `apps/control-plane/internal/budgets/` — new package, no tests yet; covers BILL-06
- [ ] `apps/control-plane/internal/platform/metrics/` — new package, no tests yet; covers OPS-01
- [ ] `apps/web-console/lib/control-plane/client.ts` — new functions need unit tests for analytics, invoice, budget endpoints
- [ ] `apps/web-console/components/billing/checkout-modal.tsx` — BDT compliance test is critical, must be written first (TDD)

---

## State of the Art

| Old Approach | Current Approach | Impact |
|--------------|------------------|--------|
| Recharts 2.x (React 18 era) | Recharts 3.x (React 19 compatible) | Breaking API: internal state hooks changed; must use 3.x |
| Puppeteer for PDF | @react-pdf/renderer | No headless browser dependency; works in any Node.js env |
| Grafana native alerting only | Alertmanager for routing | Alertmanager handles grouping, silence, multi-receiver routing better |
| Prometheus default registry | Custom prometheus.NewRegistry() | Avoid including Go runtime metrics in application-specific /metrics |

**Deprecated/outdated:**
- Recharts 2.x: Not compatible with React 19.0.0 without patches. Do not use.
- `Customized` component in Recharts: Removed in v3. Use custom components directly inside chart composition.

---

## Open Questions

1. **Invoice storage: where do we store PDF invoices?**
   - What we know: Payments handler creates PaymentIntents. Each successful payment should trigger invoice creation.
   - What's unclear: Is the PDF pre-generated and stored in legacy local object-store emulator, or generated on-demand at download time? legacy local object-store emulator is already available in docker-compose.
   - Recommendation: Generate on-demand at download time (no storage needed, no stale PDFs). Store invoice metadata (intent ID, amount, date, line items) in a `payment_invoices` Postgres table. PDF is assembled from that row at download time.

2. **Budget notification delivery: email via Supabase Auth or SMTP?**
   - What we know: Supabase Auth handles transactional emails (verify, reset). Control-plane has no email client today.
   - What's unclear: Does the project have SMTP credentials available, or should we use Supabase's existing email infrastructure?
   - Recommendation: Use Supabase Auth's email template system (trigger via admin API) for external email notifications. Store an `in_console_alert_pending` boolean in the budget threshold row for the banner, cleared when user acknowledges.

3. **Public catalog endpoint: new route or expose existing internal snapshot?**
   - What we know: `/internal/catalog/snapshot` returns the full catalog (internal only). Customer-facing view should hide upstream provider details.
   - What's unclear: Does the existing CatalogHandler.Service.GetSnapshot() already sanitize provider info?
   - Recommendation: Add a new `/api/v1/catalog/models` customer-facing route that serializes only: model alias, capabilities summary, per-token credit pricing, and availability status. Reuses the same service but with a customer-safe serializer.

---

## Sources

### Primary (HIGH confidence)
- Go proxy verified: `github.com/prometheus/client_golang@v1.23.2` — current version as of 2026-04-10
- npm verified: `recharts@3.8.1` — current version as of 2026-04-10
- npm verified: `@react-pdf/renderer@4.4.1` — current version as of 2026-04-10
- Codebase read: `apps/web-console/package.json` — confirmed React 19.0.0, Next.js 15.1.0, Vitest 3.1.1
- Codebase read: `apps/control-plane/internal/*/http.go` — confirmed all existing API routes and response shapes
- Codebase read: `deploy/docker/docker-compose.yml` — confirmed no existing Prometheus/Grafana services

### Secondary (MEDIUM confidence)
- [Supabase Metrics API](https://supabase.com/docs/guides/telemetry/metrics) — confirmed Prometheus-compatible /metrics for Postgres signals; confirmed NOT covering custom application metrics
- [Supabase New Observability Features](https://supabase.com/blog/new-observability-features-in-supabase) — OpenTelemetry support is beta/in-progress as of 2025; not production-ready for this use case
- [prometheus/client_golang promhttp docs](https://pkg.go.dev/github.com/prometheus/client_golang/prometheus/promhttp) — HandlerFor() pattern for custom registry
- [Recharts 3.0 migration guide](https://github.com/recharts/recharts/wiki/3.0-migration-guide) — confirmed breaking changes from 2.x; internal state no longer available in custom components
- [Recharts React 19 support issue](https://github.com/recharts/recharts/issues/4558) — resolved in 3.x series

### Tertiary (LOW confidence)
- WebSearch: Alertmanager docker-compose patterns — multiple sources agree on standard docker image + YAML config approach; specific config values need validation at implementation time
- WebSearch: BDT regulatory requirement for no FX display — confirmed by CONTEXT.md as locked decision (not independently verified from regulatory source)

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — npm and go proxy verified versions, React 19 compatibility confirmed
- API gap analysis: HIGH — based on direct codebase read of all existing handlers
- Architecture: HIGH — follows established project patterns (Handler struct, ServeHTTP dispatch, server components)
- Supabase observability scope: MEDIUM — verified from Supabase docs, but OpenTelemetry beta status may change
- Prometheus label cardinality guidance: MEDIUM — standard community wisdom, needs validation against actual metric volume
- Alertmanager config: LOW — pattern confirmed, specific YAML values need implementation-time verification

**Research date:** 2026-04-10
**Valid until:** 2026-05-10 (recharts and prometheus versions may update; core architecture is stable)
