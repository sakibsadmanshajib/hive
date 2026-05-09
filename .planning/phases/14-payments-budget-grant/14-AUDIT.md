# Phase 14 — Payments, Budget & Discretionary Credit Grant — AUDIT

> Audit-first artefact. NO source code under apps/, supabase/, packages/openai-contract/scripts/ is modified by Task 1.
> This document drives Tasks 2–7 (FIX-14-NN list, Section F).
>
> Phase: 14-payments-budget-grant
> Branch: `a/phase-14-payments-budget-grant`
> Date: 2026-04-27
> Milestone: v1.1 (Track A)

---

## Section A — Existing Surface Inventory

### A.1 `apps/control-plane/internal/budgets/`

Confirmed via `ls`:

```
http.go             (CRUD HTTP handlers — minimal surface)
http_test.go
notifier.go         (alert dispatch primitive)
repository.go       (pgx-backed budget rows)
service.go          (existing budget service — to be extended)
service_test.go
types.go            (39 lines — minimal: Budget, AlertConfig)
```

Phase 14 plan: ADD `cron.go`, `cron_test.go`, `notifier_test.go`. EXTEND `types.go` (soft + hard cap fields, *big.Int wrappers), `service.go` (eval pipeline + Redis invalidation publish), `http.go` (CRUD endpoints with owner-gate). EXTEND `notifier.go` (webhook dispatch + HMAC signature + retry).

Gap vs Phase 14 scope:
- No threshold-tuple alert idempotency (`spend_alerts.last_fired_period`).
- No cron evaluator entrypoint.
- No 50/80/100 fixed-threshold model — current types.go uses generic threshold list.
- No webhook dispatch — only email path exists in `notifier.go`.

### A.2 `apps/control-plane/internal/payments/`

```
bkash/                         payment rail
sslcommerz/                    payment rail
stripe/                        payment rail
fx.go, fx_test.go              FX conversion (math/big)
http.go (267 lines)            checkout HTTP handlers
http_test.go (505 lines)
rail.go                        rail interface
repository.go (241 lines)      payment row persistence
service.go (424 lines)         checkout service
service_test.go (632 lines)
tax.go, tax_test.go            tax calculation
types.go (169 lines)            Invoice, CheckoutResponse, etc.
```

USD-leak survey on existing surface (NOT touched by Phase 14):

- `payments/types.go:74`  — `AmountUSD int64 json:"amount_usd"` (Invoice struct).
- `payments/types.go:129` — `AmountUSD int64 json:"amount_usd"` (CheckoutResponse).
- `payments/http.go:119`  — `AmountUSD int64 json:"amount_usd"`.
- `payments/repository.go:40,62,72,128` — `amount_usd` in SQL projections.

Locked decision (PLAN §locked #9): Phase 14 MUST NOT modify these. Phase 17 (HANDOFF-13-03 / HANDOFF-13-04) owns the strip. Phase 14 invoice records are NEW rows in a NEW `invoices` table emitted by a NEW package `payments/invoices/` whose response shape contains BDT-only fields. Naming: existing `payments.Invoice` stays as-is; new package defines `invoices.Invoice` (different import path).

### A.3 `apps/control-plane/internal/ledger/`

```
http.go (206), http_test.go (183)
repository.go (350)        append-only credit ledger entries
service.go (128)           credit/debit service
service_test.go (277)
types.go (88)              LedgerEntry struct
```

Ledger primitive is append-only by design (existing). The new `credit_grants` table FK-references `ledger.entries.id` — every grant produces exactly one ledger append in the same DB transaction (Phase 14 scope: `grants.service.CreateGrant` uses `ledger.Repository.Append` inside `pgx.Tx`).

### A.4 `apps/control-plane/internal/accounting/`

```
http.go (420), http_test.go (418)
repository.go (366)        spend aggregations
service.go (493)           aggregator API
service_test.go (719)
types.go (101)             Aggregate types
```

Required by budgets cron + invoice cron: month-to-date spend aggregator returning `*big.Int` BDT subunits.

Gap vs Phase 14: Need to confirm whether `accounting.MonthToDateSpend(ctx, workspaceID, period)` exists with `*big.Int` return; if not, Phase 14 internal extension (NOT a hand-off) — adds the function in Task 3.

### A.5 `apps/control-plane/internal/platform/`

```
config/config.go
db/pool.go
http/router.go
metrics/middleware.go
metrics/metrics.go
metrics/metrics_test.go
redis/client.go
```

NO `role.go` exists. Phase 14 SEEDS `platform/role.go` + `role_test.go` here (Task 2).

### A.6 `apps/edge-api/internal/`

```
audio  authz  batches  catalog  errors  files
images  inference  matrix  middleware  proxy
```

NO `limits/` directory exists. PLAN refers to "Phase 12 KEY-05 lives in apps/edge-api/internal/limits/" — actual location is `apps/edge-api/internal/authz/ratelimit.go` (verified via `find apps/edge-api -name "*.go" | xargs grep -l "RateLimit"`).

**Naming deviation accepted:** Phase 14 still places `budget_gate.go` in a NEW package `apps/edge-api/internal/limits/` per PLAN frontmatter (separate concern from authz; rate-limit semantics differ from BDT-cap semantics). Recorded as FIX-14-08 + cross-link in Task 7 router wiring.

### A.7 `apps/web-console/lib/control-plane/`

`types.ts` already extended in Phase 13 (HANDOFF-13-03 / HANDOFF-13-04 carry-forwards). Phase 14 ADDS new typed surface for: BudgetSettings, SpendAlert, InvoiceRecord (NEW — separate from Phase 13's existing checkout-side Invoice mirror), CreditGrant, GranteeLookupRequest, GranteeLookupResponse. Strict TS — no `as`/`any`/`unknown` casts.

### A.8 `apps/web-console/app/console/`

Existing tree (confirmed): `billing/page.tsx`, `billing/budget-alert-form.tsx`. Phase 14 ADDS:
- `billing/budgets/page.tsx`
- `billing/alerts/page.tsx`
- `billing/invoices/page.tsx`
- `billing/invoices/[id]/route.ts` (PDF passthrough)
- `admin/grants/page.tsx`
- `admin/grants/new/page.tsx`
- `admin/grants/[id]/page.tsx`

Owner-gate UI primitive: `apps/web-console/lib/owner-gate.ts` (NEW) — server-side check + client-side surface (read-only render for non-owners).

### A.9 `supabase/migrations/`

Last applied: `20260427_01_batch_local_executor.sql` (Phase 15). Phase 14 ADDS `20260428_01_budgets_alerts_invoices_grants.sql`.

---

## Section B — Schema Design (Full DDL Preview)

```sql
-- supabase/migrations/20260428_01_budgets_alerts_invoices_grants.sql

-- ============================================================================
-- budgets — per-workspace soft + hard caps in BDT subunits
-- ============================================================================
CREATE TABLE IF NOT EXISTS public.budgets (
    workspace_id              uuid PRIMARY KEY REFERENCES public.workspaces(id) ON DELETE CASCADE,
    period_start              date NOT NULL,
    soft_cap_bdt_subunits     bigint NOT NULL CHECK (soft_cap_bdt_subunits >= 0),
    hard_cap_bdt_subunits     bigint NOT NULL CHECK (hard_cap_bdt_subunits >= soft_cap_bdt_subunits),
    currency                  char(3) NOT NULL DEFAULT 'BDT' CHECK (currency = 'BDT'),
    created_at                timestamptz NOT NULL DEFAULT now(),
    updated_at                timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_budgets_workspace_period
    ON public.budgets (workspace_id, period_start);

-- ============================================================================
-- spend_alerts — owner-managed thresholds + delivery channels
-- ============================================================================
CREATE TABLE IF NOT EXISTS public.spend_alerts (
    id                        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id              uuid NOT NULL REFERENCES public.workspaces(id) ON DELETE CASCADE,
    threshold_pct             smallint NOT NULL CHECK (threshold_pct IN (50, 80, 100)),
    email                     text,
    webhook_url               text,
    webhook_secret            text,
    last_fired_at             timestamptz,
    last_fired_period         date,
    created_at                timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, threshold_pct)
);

CREATE INDEX IF NOT EXISTS idx_spend_alerts_workspace
    ON public.spend_alerts (workspace_id);

-- ============================================================================
-- invoices — monthly BDT-only invoice rows (NEW table; distinct from
-- payments.Invoice checkout struct that Phase 17 owns).
-- ============================================================================
CREATE TABLE IF NOT EXISTS public.invoices (
    id                        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id              uuid NOT NULL REFERENCES public.workspaces(id) ON DELETE CASCADE,
    period_start              date NOT NULL,
    period_end                date NOT NULL,
    total_bdt_subunits        bigint NOT NULL CHECK (total_bdt_subunits >= 0),
    line_items                jsonb NOT NULL,
    pdf_storage_key           text,
    currency                  char(3) NOT NULL DEFAULT 'BDT' CHECK (currency = 'BDT'),
    generated_at              timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, period_start)
);

CREATE INDEX IF NOT EXISTS idx_invoices_workspace_period
    ON public.invoices (workspace_id, period_start);

-- ============================================================================
-- credit_grants — owner-discretionary credit issuance, append-only at schema
-- ============================================================================
CREATE TABLE IF NOT EXISTS public.credit_grants (
    id                        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    granted_by_user_id        uuid NOT NULL REFERENCES auth.users(id),
    granted_to_user_id        uuid NOT NULL REFERENCES auth.users(id),
    granted_to_workspace_id   uuid NOT NULL REFERENCES public.workspaces(id),
    amount_bdt_subunits       bigint NOT NULL CHECK (amount_bdt_subunits > 0),
    reason_note               text,
    ledger_entry_id           uuid NOT NULL,
    currency                  char(3) NOT NULL DEFAULT 'BDT' CHECK (currency = 'BDT'),
    created_at                timestamptz NOT NULL DEFAULT now()
    -- DELIBERATELY NO updated_at — schema-level immutability
);

CREATE INDEX IF NOT EXISTS idx_credit_grants_grantee
    ON public.credit_grants (granted_to_workspace_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_credit_grants_grantor
    ON public.credit_grants (granted_by_user_id, created_at DESC);

-- ----------------------------------------------------------------------------
-- credit_grants_immutable_trg — schema-level append-only enforcement
-- ----------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION public.tg_credit_grants_immutable()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'credit_grants is append-only — UPDATE/DELETE forbidden'
        USING ERRCODE = 'integrity_constraint_violation';
END;
$$;

DROP TRIGGER IF EXISTS credit_grants_immutable_trg ON public.credit_grants;
CREATE TRIGGER credit_grants_immutable_trg
    BEFORE UPDATE OR DELETE ON public.credit_grants
    FOR EACH ROW EXECUTE FUNCTION public.tg_credit_grants_immutable();
```

**Audit notes:**

- All money columns `BIGINT` (subunit precision). Application layer marshals to/from `*big.Int` even though int64 fits — discipline guard for math/big invariant.
- `workspaces` table assumed to exist in the `public` schema (consumed by Phase 13 already). FK-cascade on workspace deletion.
- `spend_alerts.webhook_secret` added to schema (referenced in PLAN action item Task 3 step 5 — HMAC signing).
- `credit_grants` deliberately omits `updated_at` (double-locked immutability — schema has no column, trigger blocks UPDATE/DELETE).

---

## Section C — Endpoint Design

| Method | Path                                    | Owner-gated | Request                                                                  | Response                                                          |
|--------|-----------------------------------------|-------------|--------------------------------------------------------------------------|-------------------------------------------------------------------|
| GET    | `/v1/budgets`                           | no (read)   | —                                                                        | `{workspace_id, period_start, soft_cap_bdt_subunits, hard_cap_bdt_subunits, currency}` |
| PUT    | `/v1/budgets`                           | yes         | `{soft_cap_bdt_subunits, hard_cap_bdt_subunits}`                          | same                                                              |
| GET    | `/v1/spend-alerts`                      | no (read)   | —                                                                        | `[SpendAlert]`                                                    |
| POST   | `/v1/spend-alerts`                      | yes         | `{threshold_pct, email?, webhook_url?, webhook_secret?}`                  | `SpendAlert` (201)                                                |
| PATCH  | `/v1/spend-alerts/{id}`                 | yes         | partial fields                                                            | `SpendAlert`                                                      |
| DELETE | `/v1/spend-alerts/{id}`                 | yes         | —                                                                        | 204                                                               |
| GET    | `/v1/invoices`                          | no (read)   | `?cursor`                                                                 | `{items: [InvoiceRecord], next_cursor?}`                          |
| GET    | `/v1/invoices/{id}`                     | no (read)   | —                                                                        | `InvoiceRecord`                                                   |
| GET    | `/v1/invoices/{id}/pdf`                 | no (read)   | —                                                                        | `application/pdf`                                                 |
| POST   | `/v1/admin/grantees:lookup`             | yes         | `{email?, phone?}`                                                        | `{user_id, workspace_id, display_name}` or 404                    |
| POST   | `/v1/admin/grants`                      | yes         | `{grantee_email | grantee_phone, amount_bdt_subunits, reason_note?}`     | `CreditGrant` (201)                                               |
| GET    | `/v1/admin/grants`                      | yes         | `?cursor`                                                                 | `{items: [CreditGrant], next_cursor?}`                            |
| GET    | `/v1/admin/grants/{id}`                 | yes         | —                                                                        | `CreditGrant`                                                     |

Every response shape: BDT subunits only. Zero `amount_usd|usd_|fx_|exchange_rate|price_per_credit_usd` keys. Lint enforced by Section I script.

Provider-blind 403 on non-owner (handler returns sanitized error JSON; no leak of "you must be owner" — just `{"error":{"message":"insufficient permissions","type":"permission_denied"}}`).

---

## Section D — Edge-API Budget Gate Design

### D.1 Wiring point

`apps/edge-api/internal/server/router.go` — gate runs **post-auth, post-rate-limit, pre-provider-dispatch** for `/v1/chat/completions`, `/v1/embeddings`, `/v1/responses`, `/v1/images/generations`, `/v1/audio/transcriptions`, etc. (every paid endpoint).

### D.2 Cache shape

- Redis key: `budget:hard_cap:{workspace_id}` → string-encoded BDT subunits (decimal, parseable by `*big.Int.SetString`).
- TTL: 60 seconds.
- On cache miss: edge-api reads control-plane GET `/v1/budgets` (internal-auth header), populates Redis with TTL.
- Invalidation race: control-plane's PUT `/v1/budgets` writes Redis with new value AFTER DB commit (write-through). Race window ≤ 60s in pathological case where Redis SET fails post-commit; covered by TTL self-healing. Documented as acceptable in PLAN locked-decision #4.

### D.3 Comparison

```go
mtdSpend := /* *big.Int from accounting MTD */
hardCap  := /* *big.Int from cache */
if mtdSpend.Cmp(hardCap) >= 0 {
    return ErrHardCapExceeded   // edge translates to HTTP 402
}
```

`>=` (NOT `>`) — at-cap is OVER-cap. Test asserts boundary.

### D.4 402 Response

```json
{
  "error": {
    "message": "workspace credit balance below required threshold",
    "type": "insufficient_quota",
    "code": "budget_hard_cap_exceeded"
  }
}
```

Same shape as existing low-balance error to keep customer error surface consistent (edge-api `internal/errors/openai.go` extension).

### D.5 Package placement deviation

PLAN says `apps/edge-api/internal/limits/budget_gate.go`. Inventory shows `apps/edge-api/internal/` has no `limits/` dir; rate-limit lives at `internal/authz/ratelimit.go`. Phase 14 creates NEW package `apps/edge-api/internal/limits/` for budget gate (separation of concern: authz = identity-bound, limits = workspace-bound). Recorded as FIX-14-08.

---

## Section E — Phase 13 Carry-Forward Absorption

| Hand-off       | Phase 13 file                                              | Phase 14 fix                                                                                                               | FIX id     |
|----------------|------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------|------------|
| HANDOFF-13-01  | `apps/web-console/tests/e2e/auth-shell.spec.ts:88`         | Extend `tests/e2e/support/e2e-auth-fixtures.mjs` with `seedSecondaryWorkspace()` helper consumed by switcher e2e fixture.  | FIX-14-13  |
| HANDOFF-13-02  | `apps/web-console/tests/e2e/profile-completion.spec.ts:71` | Add per-spec `test.beforeEach` profile-state cleanup hook in `e2e-auth-fixtures.mjs`; ordering-independent.                | FIX-14-14  |
| HANDOFF-13-06  | console route inventory                                    | Add `app/console/admin/grants/{,new,[id]}/page.tsx` + `components/admin/grant-{form,list,detail}.tsx` (Task 5).            | FIX-14-09  |

**NOT carried forward:**

- HANDOFF-13-03 — Phase 17 (control-plane Invoice.amount_usd strip at source). Phase 14 DOES NOT touch `payments.Invoice` (verified Section A.2).
- HANDOFF-13-04 — Phase 17 (CheckoutOptions.price_per_credit_usd split per-country).
- HANDOFF-13-05 — Phase 18 (tier-aware viewer-gates extension). Phase 14 ships owner/non-owner only via `platform.IsWorkspaceOwner`.

---

## Section F — FIX-14-NN List

| FIX id     | File(s)                                                                                                                   | Description                                                                                                            | Target test                                                                                  | Requirement |
|------------|---------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------|-------------|
| FIX-14-01  | `supabase/migrations/20260428_01_budgets_alerts_invoices_grants.sql`                                                      | Migration: 4 tables + immutability trigger.                                                                            | `platform/role_test.go::TestImmutabilityTrigger*`                                            | PAY-14-01   |
| FIX-14-02  | `apps/control-plane/internal/platform/role.go` + `role_test.go`                                                           | `IsWorkspaceOwner` Phase 18 RBAC contract stub.                                                                        | `role_test.go::TestIsWorkspaceOwner*`                                                        | PAY-14-08   |
| FIX-14-03  | `apps/control-plane/internal/budgets/{types,repository,service,http,notifier,cron}.go` + tests                             | Soft + hard cap CRUD; spend-alert CRUD; cron evaluator; webhook signing.                                               | `budgets/{service,http,cron,notifier}_test.go`                                                | PAY-14-02, PAY-14-03 |
| FIX-14-04  | `apps/control-plane/internal/payments/invoices/{types,repository,service,pdf,http,cron}.go` + tests                        | Monthly invoice gen; gofpdf renderer; PDF download endpoint.                                                           | `payments/invoices/{service,pdf,http,cron}_test.go`                                          | PAY-14-04   |
| FIX-14-05  | `apps/control-plane/internal/grants/{types,repository,service,http,audit}.go` + tests                                     | Discretionary grant create/list/get; owner-gated; single-tx ledger append.                                              | `grants/{service,http}_test.go`                                                               | PAY-14-05, PAY-14-06, PAY-14-07 |
| FIX-14-06  | `apps/edge-api/internal/limits/budget_gate.go` + `budget_gate_test.go` + `apps/edge-api/internal/server/router.go`         | Hot-path 402 hard-cap gate; Redis-cached; pre-provider-dispatch.                                                       | `limits/budget_gate_test.go` + `cmd/server/main_test.go::TestBudgetGate402`                  | PAY-14-02   |
| FIX-14-07  | `apps/web-console/lib/control-plane/{types,client}.ts` + `lib/owner-gate.ts`                                              | Strict-TS typed client extensions for budgets/alerts/invoices/grants + owner-gate utility.                              | `tsc --noEmit` + `npm run test:unit`                                                          | PAY-14-09   |
| FIX-14-08  | `apps/web-console/app/console/billing/{budgets,alerts,invoices}/**` + `components/billing/{budget,spend-alert,invoice-row}-form.tsx` + tests | Console pages: budget cap form, alert manager, invoice list + PDF download.                                            | `tests/e2e/console-{budgets,spend-alerts,invoices}.spec.ts`                                   | PAY-14-04   |
| FIX-14-09  | `apps/web-console/app/console/admin/grants/**` + `components/admin/grant-{form,list,detail}.tsx` + tests                  | Owner-gated discretionary grant UI (HANDOFF-13-06).                                                                     | `tests/e2e/admin-credit-grants.spec.ts`                                                       | PAY-14-05, PAY-14-11 |
| FIX-14-10  | `packages/openai-contract/spec/paths/{budgets,spend-alerts,invoices,grants}.yaml` + `generated/hive-openapi.yaml`          | OpenAPI path specs for new endpoints (BDT-only).                                                                        | `lint-no-customer-usd.test.mjs`                                                                | PAY-14-09   |
| FIX-14-11  | `packages/openai-contract/scripts/lint-no-customer-usd.{mjs,test.mjs}`                                                    | CI lint primitive (Phase 17 will extend repo-wide).                                                                     | `node lint-no-customer-usd.test.mjs`                                                          | PAY-14-09   |
| FIX-14-12  | `.planning/REQUIREMENTS.md`                                                                                               | PAY-14-01..12 rows added; evidence link to 14-VERIFICATION.md.                                                          | `bash scripts/verify-requirements-matrix.sh`                                                   | PAY-14-12   |
| FIX-14-13  | `apps/web-console/tests/e2e/support/e2e-auth-fixtures.mjs`                                                                | `seedSecondaryWorkspace()` helper (HANDOFF-13-01).                                                                      | `tests/e2e/auth-shell.spec.ts` switcher case                                                  | PAY-14-10   |
| FIX-14-14  | `apps/web-console/tests/e2e/support/e2e-auth-fixtures.mjs`                                                                | Per-spec profile cleanup hook (HANDOFF-13-02).                                                                          | `tests/e2e/profile-completion.spec.ts:71`                                                      | PAY-14-10   |
| FIX-14-15  | `deploy/docker/docker-compose.yml`                                                                                        | Wire budgets + invoices crons into control-plane container schedule (cron-ish goroutine; existing pattern).             | `apps/control-plane/cmd/server/main_test.go::TestCronWiring`                                  | PAY-14-03, PAY-14-04 |

---

## Section G — PDF Library Decision

**Choice: `github.com/jung-kurt/gofpdf` (locked decision PLAN §1).**

Reasoning:
- Pure Go, MIT, no CGO, no headless-browser dep.
- Lightweight: BDT-only invoice has minimal layout demand (header, line items, total).
- Already familiar pattern across small-volume invoicing systems.
- No Docker image bloat (alternative `wkhtmltopdf` would require a binary in the runtime image).

Alternatives considered:
- `pdfcpu` — modify-only, weaker authoring API.
- `unidoc/unipdf` — commercial license required for production.
- `wkhtmltopdf` (html-to-pdf) — adds binary dep, increases container image size, headless browser security surface.

`go.mod` check at audit time: `gofpdf` not yet present. Task 4 adds via `go get github.com/jung-kurt/gofpdf` (within the toolchain Docker shell to keep host clean).

Version pin: latest stable at audit time (Task 4 records exact version in summary).

---

## Section H — BD VAT Placeholder Format

**Decision: placeholder ships in v1.1.0; legal-reviewed format is a doc-only PR post-launch (locked decision PLAN §8).**

Rationale: BD VAT registration format requires legal counsel confirmation; current artefact is interim. Compliance flagged in `.planning/v1.1-DEFERRED-SCOPE.md` as a launch-readiness audit item, NOT a Phase 14 blocker.

### Placeholder block layout (rendered by gofpdf in Task 4)

```
+------------------------------------------------------------+
| HIVE  —  Tax Invoice                                       |
| Workspace: <workspace-name>                                |
| Period: <YYYY-MM-DD> — <YYYY-MM-DD>                        |
| Invoice ID: <uuid>                                         |
|                                                            |
| BIN: TBD (legal review)                                    |
| Mushok-9.4 reference: TBD (legal review)                   |
|                                                            |
| Line items:                                                |
|   <model-bucket>     <calls>     <BDT subunits>            |
|   ...                                                      |
| ---------------------------------------------------------- |
|   Total                          BDT <amount>              |
+------------------------------------------------------------+
```

`TBD (legal review)` strings are explicit in PDF body so post-launch refinement is a one-liner replace.

---

## Section I — FX/USD Lint Primitive Design

### I.1 Script `packages/openai-contract/scripts/lint-no-customer-usd.mjs`

```js
#!/usr/bin/env node
// CI guardrail: scan customer-facing OpenAPI path specs for FX/USD leak keys.
// Phase 14 scope: scans the four new path files. Phase 17 extends to repo-wide.

import { readFileSync, existsSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";

const FORBIDDEN = /^(amount_usd|usd_|fx_|price_per_credit_usd|exchange_rate)/;

const TARGETS = process.argv.slice(2);
if (TARGETS.length === 0) {
  console.error("usage: lint-no-customer-usd.mjs <yaml> [<yaml>...]");
  process.exit(2);
}

let leaks = 0;
for (const t of TARGETS) {
  const p = resolve(t);
  if (!existsSync(p)) { console.error(`MISSING: ${p}`); process.exit(2); }
  const doc = parse(readFileSync(p, "utf8"));
  walk(doc, [], (path, key) => {
    if (FORBIDDEN.test(key)) {
      console.error(`LEAK: ${t}: ${path.concat(key).join(".")}`);
      leaks += 1;
    }
  });
}
process.exit(leaks > 0 ? 1 : 0);

function walk(node, path, visit) {
  if (Array.isArray(node)) { node.forEach((v, i) => walk(v, path.concat(`[${i}]`), visit)); return; }
  if (node && typeof node === "object") {
    for (const [k, v] of Object.entries(node)) {
      visit(path, k);
      walk(v, path.concat(k), visit);
    }
  }
}
```

### I.2 Test `packages/openai-contract/scripts/lint-no-customer-usd.test.mjs`

- Synthetic spec with `amount_usd` property → script exits 1.
- Synthetic clean spec → exits 0.
- Synthetic spec with `fx_rate` → exits 1.
- Synthetic spec with `total_bdt_subunits` only → exits 0.

### I.3 CI invocation

`package.json` script (Phase 14 scope):

```json
"scripts": {
  "lint:fx": "node scripts/lint-no-customer-usd.mjs spec/paths/budgets.yaml spec/paths/spend-alerts.yaml spec/paths/invoices.yaml spec/paths/grants.yaml"
}
```

Phase 17 extends args to all paths under `spec/paths/`.

---

## Section J — REQUIREMENTS.md Mapping

| Req id     | Truth (PLAN §truths)                                                                                                                                | FIX id(s)             |
|------------|----------------------------------------------------------------------------------------------------------------------------------------------------|-----------------------|
| PAY-14-01  | Workspace owner sets monthly soft + hard caps in BDT subunits; round-trips typed.                                                                  | FIX-14-01, FIX-14-03, FIX-14-07 |
| PAY-14-02  | Hard-cap enforced on inference hot-path; 402 + provider-blind error before any provider call.                                                      | FIX-14-06             |
| PAY-14-03  | Soft-cap + threshold spend alerts (50/80/100) fire exactly once per (workspace, period, threshold); idempotency.                                   | FIX-14-03, FIX-14-15  |
| PAY-14-04  | Monthly invoice cron generates one row per workspace; PDF BDT-only with zero USD/FX strings.                                                       | FIX-14-04, FIX-14-08, FIX-14-15 |
| PAY-14-05  | Owner-only `POST /v1/admin/grants`; resolves grantee; immutable audit row + same-tx ledger append.                                                  | FIX-14-05, FIX-14-09  |
| PAY-14-06  | Non-owner `POST /v1/admin/grants` → 403 provider-blind; tested at handler + Playwright.                                                            | FIX-14-05, FIX-14-09  |
| PAY-14-07  | `credit_grants` UPDATE/DELETE raises Postgres exception via row trigger.                                                                            | FIX-14-01             |
| PAY-14-08  | Phase-18 RBAC contract stub: `platform.IsWorkspaceOwner` single source-of-truth.                                                                    | FIX-14-02, FIX-14-05  |
| PAY-14-09  | Customer-surface FX-leak grep across new path specs returns zero matches; CI lint script enforces.                                                  | FIX-14-10, FIX-14-11  |
| PAY-14-10  | Phase 13 carry-forward absorbed: HANDOFF-13-01/02/06 closed in this phase.                                                                         | FIX-14-09, FIX-14-13, FIX-14-14 |
| PAY-14-11  | Discretionary grant flow consumed end-to-end by Playwright (owner happy-path + non-owner negative).                                                | FIX-14-09             |
| PAY-14-12  | REQUIREMENTS.md PAY-14-01..12 rows added with evidence; `verify-requirements-matrix.sh` exits 0.                                                    | FIX-14-12             |

---

## Section K — Open Risks & Mitigations

| Risk                                                                                                                  | Mitigation                                                                                                  |
|-----------------------------------------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------|
| Edge-api budget cache stale after PUT /v1/budgets if Redis SET fails post-commit.                                     | TTL=60s self-heals; documented; consider control-plane retry queue if observed in production.              |
| `payments/types.go` USD fields (Invoice, CheckoutResponse) still on the wire.                                         | Phase 17 strips at source; Phase 14 explicitly does not touch (locked decision #9).                         |
| BD VAT placeholder may need legal-mandated fields not yet known.                                                      | Doc-only PR post-launch; placeholder text `TBD (legal review)` makes refinement trivial.                    |
| `spend_alerts.webhook_secret` in plaintext column (no envelope encryption).                                            | Documented limitation; v1.1 ships plaintext; Phase 18+ may introduce KMS envelope.                          |
| Naming deviation: PLAN says `apps/edge-api/internal/limits/`; inventory shows that path absent.                        | New package created at PLAN-prescribed path. Phase 12 KEY-05 lives at `internal/authz/ratelimit.go` (separate concern). |
| Existing `apps/control-plane/internal/budgets/` already partially populated.                                          | Task 3 EXTENDS rather than recreates; sentinel errors + cron + webhook are additive.                        |

---

## Section L — Execution Sequencing (Tasks 2–7 Order)

1. **Task 2** — migration + `platform/role.go` (foundation for all owner-gated endpoints).
2. **Task 3** — budgets module (control-plane CRUD, cron, notifier) + edge-api budget gate (depends on Task 2 owner-gate primitive).
3. **Task 4** — invoices sub-package + gofpdf renderer + monthly cron (depends on Task 2 schema).
4. **Task 5** — grants package + audit log (depends on Task 2 schema + Task 2 owner-gate primitive + ledger primitive).
5. **Task 6** — web-console pages + Playwright e2e (depends on Tasks 3/4/5 endpoints + types.ts mirror).
6. **Task 7** — OpenAPI path specs + lint primitive + REQUIREMENTS.md + 14-VERIFICATION.md (depends on all prior).

---

End of audit.
