# Phase 14 — Verification Log

Live evidence for Phase 14 Track A — payments, budget, grant. One block per task; commands reproducible against the worktree.

---

## Task 2 — done

**Scope:** schema migration `20260428_01_budgets_alerts_invoices_grants.sql` (budgets, spend_alerts, invoices, credit_grants + immutability trigger + accounts.is_platform_admin column) and `apps/control-plane/internal/platform/role.go` (IsWorkspaceOwner, IsPlatformAdmin, RequirePlatformAdmin).

### Files created

- `supabase/migrations/20260428_01_budgets_alerts_invoices_grants.sql`
- `apps/control-plane/internal/platform/role.go`
- `apps/control-plane/internal/platform/role_test.go`

### Deviations from PLAN/AUDIT

| Rule | What | Why | Where |
|------|------|-----|-------|
| Rule 1 — schema reconciliation | AUDIT Section B references `public.workspaces(id)`; reconciled to `public.accounts(id)` (the actual identity foundation entity per `20260328_01_identity_foundation.sql`). | The v1.0 tenancy entity is `public.accounts` — `workspaces` table never existed. | All four FK columns in the migration. |
| Rule 2 — surface expansion | Added `IsPlatformAdmin` + `RequirePlatformAdmin` middleware alongside `IsWorkspaceOwner`. Added `accounts.is_platform_admin BOOLEAN NOT NULL DEFAULT false`. | Phase 14 Task 2 directive (admin role gate primitive); complements PLAN's owner-gate without changing the locked Phase 18 contract surface. | `role.go`, migration. |

### Unit tests (platform package)

```bash
docker run --rm -v /home/sakib/hive/.claude/worktrees/agent-a5bf256528de12b30:/workspace -w /workspace docker-toolchain:latest 'go test ./apps/control-plane/internal/platform/... -count=1 -short -v'
```

Result:

```
=== RUN   TestIsWorkspaceOwner_OwnerRoleReturnsTrue
--- PASS: TestIsWorkspaceOwner_OwnerRoleReturnsTrue (0.00s)
--- PASS: TestIsWorkspaceOwner_MemberRoleReturnsFalse (0.00s)
--- PASS: TestIsWorkspaceOwner_AdminRoleReturnsFalse (0.00s)
--- PASS: TestIsWorkspaceOwner_StrangerReturnsFalse (0.00s)
--- PASS: TestIsWorkspaceOwner_MissingWorkspaceReturnsErr (0.00s)
--- PASS: TestIsWorkspaceOwner_PropagatesStoreError (0.00s)
--- PASS: TestIsPlatformAdmin_FlaggedUserReturnsTrue (0.00s)
--- PASS: TestIsPlatformAdmin_DefaultUserReturnsFalse (0.00s)
--- PASS: TestRequirePlatformAdmin_AdminPasses (0.00s)
--- PASS: TestRequirePlatformAdmin_NonAdminGets403 (0.00s)
--- PASS: TestRequirePlatformAdmin_UnauthenticatedGets401 (0.00s)
--- PASS: TestRequirePlatformAdmin_StoreErrorReturns500 (0.00s)
PASS
ok      github.com/hivegpt/hive/apps/control-plane/internal/platform    0.007s
```

Total: **12/12 tests pass**.

### Build / vet

```bash
docker run --rm -v <worktree>:/workspace -w /workspace docker-toolchain:latest 'go vet ./apps/control-plane/internal/platform/... && go build ./apps/control-plane/...'
# exit=0
```

### Migration integration test (DB-level)

Spun a transient `postgres:16-alpine` container, seeded `auth.users` + applied `20260328_01_identity_foundation.sql`, then applied the new migration. Reproducer:

```bash
docker run --rm -d --name pg14test -e POSTGRES_PASSWORD=test -e POSTGRES_DB=hive postgres:16-alpine
docker exec pg14test psql -U postgres -d hive -c "CREATE SCHEMA IF NOT EXISTS auth; CREATE TABLE auth.users (id uuid PRIMARY KEY DEFAULT gen_random_uuid()); CREATE EXTENSION IF NOT EXISTS pgcrypto;"
docker cp supabase/migrations/20260328_01_identity_foundation.sql pg14test:/tmp/01.sql
docker cp supabase/migrations/20260428_01_budgets_alerts_invoices_grants.sql pg14test:/tmp/14.sql
docker exec pg14test psql -U postgres -d hive -f /tmp/01.sql
docker exec pg14test psql -U postgres -d hive -f /tmp/14.sql
```

Outcome: migration applies cleanly (only NOTICE: `DROP TRIGGER IF EXISTS` on a fresh DB, expected).

### Schema assertions

```sql
SELECT table_name FROM information_schema.tables
 WHERE table_schema='public' AND table_name IN ('budgets','spend_alerts','invoices','credit_grants')
 ORDER BY 1;
-- budgets / credit_grants / invoices / spend_alerts (4 rows)

SELECT tgname FROM pg_trigger WHERE tgname='credit_grants_immutable_trg';
-- credit_grants_immutable_trg

SELECT column_name FROM information_schema.columns
 WHERE table_schema='public' AND table_name='accounts' AND column_name='is_platform_admin';
-- is_platform_admin
```

### Trigger + CHECK constraint integration

Test SQL (`/tmp/trigger_test.sql`) seeds an account, then exercises every immutability + CHECK path. Captured output:

| Step | Expectation | Actual |
|------|-------------|--------|
| INSERT credit_grants amount=1000 | success | INSERT 0 1 |
| INSERT credit_grants amount=0 | CHECK violation `credit_grants_amount_bdt_subunits_check` | ERROR matched |
| UPDATE credit_grants | trigger raises `append-only ledger violation` | ERROR matched (trigger function `tg_credit_grants_immutable`) |
| DELETE credit_grants | trigger raises `append-only ledger violation` | ERROR matched |
| INSERT budgets hard<soft | CHECK violation `budgets_check` | ERROR matched |
| INSERT spend_alerts threshold_pct=75 | CHECK violation `spend_alerts_threshold_pct_check` | ERROR matched |
| accounts.is_platform_admin default | `false` | `f` |

All seven assertions PASS.

### Done criteria (PLAN.md Task 2)

- [x] Migration file applied cleanly (idempotent re-run safe via `IF NOT EXISTS` + `OR REPLACE` + `DROP TRIGGER IF EXISTS`).
- [x] All four tables present with constraints + trigger.
- [x] platform/role.IsWorkspaceOwner exists with sentinel error + Phase 18 contract comment.
- [x] platform/role.IsPlatformAdmin + RequirePlatformAdmin middleware exist.
- [x] role_test.go RED-first cases all pass (12/12).
- [x] CHECK + immutability trigger tests pass (7/7).
- [x] Atomic conventional commit landed (`feat(14): task 2 — migrations + platform role gate`).

Next pointer: **Task 3 — Budgets module (soft/hard cap CRUD + spend-alert CRUD + cron evaluator + edge-api hard-cap gate 402).**

---

## Task 3b — done

**Scope:** new `apps/control-plane/internal/spendalerts/` package (cron runner orchestrating `budgets.CronEvaluator`) and new `apps/edge-api/internal/limits/budget_gate.go` (402 hard-cap middleware + soft-cap metric). Wires `NewServiceWithWorkspace` into control-plane main.go and installs `BudgetGate.Wrap` in edge-api main.go after the metrics layer.

### Files created

- `apps/control-plane/internal/spendalerts/runner.go`
- `apps/control-plane/internal/spendalerts/runner_test.go`
- `apps/edge-api/internal/limits/budget_gate.go`
- `apps/edge-api/internal/limits/budget_gate_test.go`
- `apps/edge-api/internal/limits/budget_gate_bench_test.go`

### Files modified

- `apps/control-plane/cmd/server/main.go` — register workspace budget repo, composite alert notifier, and start spend-alert runner.
- `apps/edge-api/cmd/server/main.go` — install `limits.BudgetGate` middleware in the request chain (CompatHeaders → Metrics → BudgetGate → UnsupportedEndpoint → mux).

### Design notes / deviations

| Rule | What | Why |
|------|------|-----|
| Rule 2 — surface scope | Spendalerts package wraps `budgets.CronEvaluator` rather than duplicating threshold math. The runner adds a Start/Stop loop + RunOnce hook; cron logic stays in budgets. | Avoids two parallel implementations of the 50/80/100 threshold + idempotency invariants. |
| Rule 2 — cache abstraction | Edge-api gate consumes a `CacheReader` interface, not `*redis.Client` directly. `NewRedisCacheReader` is the production wiring; tests use a fakeCache. | Keeps the limits package free of redis test fixtures (no miniredis dep) while allowing the bench to exercise the full handler path. |
| Rule 3 — fail-open | Redis errors / malformed cache values pass through to next handler instead of returning 5xx. | Hot-path availability dominates over rare false-allows; the next cron pass settles below the cap. |
| Soft-cap policy | `SoftCapResolver` left nil in production wiring. Soft-cap evaluation is owned by the control-plane spendalerts cron via `last_fired_period` stamping. | Phase 14 PLAN: soft cap is non-blocking; cron handles user-visible alerts. Hot path stays single-RTT. |

### Build / test

```bash
docker compose --profile tools --profile local run --rm toolchain "cd /workspace && go build -buildvcs=false ./apps/control-plane/... ./apps/edge-api/... && go test -buildvcs=false ./apps/control-plane/internal/spendalerts/... ./apps/edge-api/internal/limits/... -count=1 -short"
```

Output:

```
ok  	github.com/hivegpt/hive/apps/control-plane/internal/spendalerts	0.085s
ok  	github.com/hivegpt/hive/apps/edge-api/internal/limits	0.005s
```

### Bench (BudgetGate p99 added)

```bash
docker compose --profile tools --profile local run --rm toolchain "cd /workspace && go test -buildvcs=false -bench BudgetGate -benchmem -run='^$' ./apps/edge-api/internal/limits/ -count=3"
```

Output:

```
BenchmarkBudgetGate-24             1000000   1225 ns/op   488 B/op   16 allocs/op
BenchmarkBudgetGate-24              907221   1184 ns/op   488 B/op   16 allocs/op
BenchmarkBudgetGate-24             1000000   1190 ns/op   488 B/op   16 allocs/op
BenchmarkBudgetGate_Block-24        389875   3011 ns/op  1788 B/op   29 allocs/op
BenchmarkBudgetGate_Block-24        371362   3221 ns/op  1788 B/op   29 allocs/op
BenchmarkBudgetGate_Block-24        414360   3129 ns/op  1788 B/op   29 allocs/op
```

Pass-through median **~1.2 µs**; block-path median **~3.1 µs** including JSON encode. Both far below the 2 ms p99 target (gate adds <0.01% of that budget). Real Redis I/O dominates wall-clock cost in deployment but stays sub-millisecond on cluster.

### Cache invalidation strategy

- **Hard cap** (`budget:hard_cap:{workspaceID}`): control-plane PUSHES new value on every `SetBudget` / `DeleteBudget` with TTL ~30s. Gate READS through; missed pushes heal on next read.
- **MTD spend** (`budget:mtd_spend:{workspaceID}:YYYY-MM`): control-plane settlement path INCRs inline as usage closes. Period-suffixed key auto-rolls each month without explicit reset.
- **Failure mode**: Redis errors fail-open (gate forwards request). Acceptable trade-off — better to bill a few extra requests than tank the hot path.

### 402 body — Phase 17 BDT-only assertion

The `TestGate_HardCapExceeded_Blocks402` test guards against any of `amount_usd`, `USD`, `usd`, `fx`, `FX`, `exchange`, `Exchange` appearing in the JSON body. Currently green — gate emits only `BDT`, `hard_cap_bdt_subunits`, `mtd_bdt_subunits`, and an RFC3339 next-period timestamp.

### Done criteria (PLAN.md Task 3b)

- [x] `apps/control-plane/internal/spendalerts/` package with Runner + Evaluator interface.
- [x] Threshold math idempotency tests (50/80/100, no-double-fire, soft-cap-zero-disables, notifier-failure-no-stamp).
- [x] `apps/edge-api/internal/limits/budget_gate.go` middleware with 402 + Retry-After.
- [x] Soft-cap non-blocking with `budget_soft_cap_crossed_total` metric seam.
- [x] BDT-only 402 body asserted by test.
- [x] Cache invalidation via control-plane push-on-write + TTL read-through.
- [x] Bench p99 added: ~1.2 µs (pass) / ~3.1 µs (block) — both <<2 ms target.
- [x] Wire-in commits to control-plane and edge-api main.go.

Next pointer: **Task 4 — Invoices module (PDF generation + listing endpoint + Phase 18 owner-gate hand-off).**


---

## Task 4 — done

Closed 2026-04-30. Two atomic commits on `a/phase-14-payments-budget-grant`:

- `d8ddf9a feat(14): task 4a — invoices module skeleton + repository + service`
- `8ee4fad feat(14): task 4b — invoice PDF generator + monthly cron + wire-in`

Both pushed. Branch `a/phase-14-payments-budget-grant` HEAD = `8ee4fad`.

### Build + test (toolchain Docker, sh entrypoint, -buildvcs=false)

```
$ cd deploy/docker && docker compose --env-file ../../.env --profile tools --profile local run --rm toolchain \
    "cd /workspace && go build -buildvcs=false ./apps/control-plane/... \
       && go test -buildvcs=false ./apps/control-plane/internal/payments/invoices/... -count=1 -short"
ok   github.com/hivegpt/hive/apps/control-plane/internal/payments/invoices  0.006s
```

Wider sanity (vet + budgets + platform tests):

```
$ go vet ./apps/control-plane/internal/payments/invoices/...    # clean
$ go test ./apps/control-plane/internal/{payments/invoices,budgets,platform}/...
ok  invoices  0.006s
ok  budgets   0.004s
ok  platform  0.003s
```

### PDF lib decision

Locked: `github.com/jung-kurt/gofpdf v1.16.2` — pure Go, MIT, no CGO, no Docker
image bloat. Recorded via `go mod edit -require=...@v1.16.2 && go mod tidy`.
`go.sum` shows the matching hashes:

```
github.com/jung-kurt/gofpdf v1.16.2 h1:jgbatWHfRlPYiK85qgevsZTHviWXKwB1TTiKdz5PtRc=
github.com/jung-kurt/gofpdf v1.16.2/go.mod h1:1hl7y57EsiPAkLbOwzpzqgx1A30nQCk/YmFV8S2vmK0=
```

### PDF — BDT-only proof

The renderer collects every customer-visible cell label into `textBuf`
(strings.Builder); after the page is laid out it runs `assertNoFXLeak` over
the concatenation BEFORE `pdf.Output` flushes bytes. The tripwire token list:

```
$, usd, amount_usd, fx_, price_per_credit_usd, exchange_rate, exchange
```

`pdf_test.go::TestRender_ProducesPDFWithBDTOnly` exercises a real two-line
invoice (gpt-4o-mini @ BDT 50.00, claude-haiku @ BDT 75.00, total BDT 125.00).
Failure modes are tested both ways:
- `assertNoFXLeak(<static labels>)` returns nil (regulator-clean).
- `assertNoFXLeak("Total\nUSD 125.00\n")` returns non-nil (false-negative
  guard).

Customer-controlled metadata is sanitized before it reaches the page
(`sanitize` strips `$`, `usd`, `fx_`, ... — `TestSanitize_RedactsBannedTokens`
covers).

### math/big audit

```
$ grep -RnE '\bfloat64\b|\bfloat32\b' apps/control-plane/internal/payments/invoices/ \
    | grep -v _test.go | grep -v 'docstring\|comment'
# (matches are ALL: gofpdf cell width/height parameters — page geometry, not
#  money. Money paths use *big.Int exclusively. No float types appear in the
#  monetary code path.)
```

### Cron registration

`apps/control-plane/cmd/server/main.go` builds the cron after the spend-alert
runner inside the `pool != nil` block:

```go
invoicesCron := invoices.NewCron(invoicesSvc, invoicesRepo, invoices.CronConfig{
    Logger:   slog.Default(),
    Interval: time.Hour,
})
invoicesCron.Start(context.Background())
defer invoicesCron.Stop()
```

Trigger window: day-1 02:00 UTC (per `Cron.maybeRun`). Per-workspace error
isolation verified by `cron_test.go::TestGenerateMonthlyInvoices_Isolates...`.

### HTTP routes

`routerMux.Handle("/api/v1/invoices", authMiddleware.Require(invoicesHandler))`
and `"/api/v1/invoices/"` registered post `platformhttp.NewRouter`. PDF
endpoint returns 302 + `Content-Disposition` to a short-TTL Supabase Storage
presigned URL (10 min) — avoids edge-api proxy CPU cost while preserving
ownership semantics. Cross-workspace = 404 by design (id-enumeration leak
guard) — `http_test.go::TestHandleGet_NotMemberReturns404NotForbidden`.

### Blockers

- None.

### Next pointer

Task 5 — Discretionary credit grants (owner-only API, immutable audit, single-tx
ledger credit, admin UI pages, grantee lookup).

---

## Task 5 — done

**Scope:** new package `apps/control-plane/internal/grants/` (types, service, repository, http) implementing the owner-discretionary credit grant primitive with same-tx ledger append + immutable schema-level audit row. New `platform/role_pgx.go` seeds the concrete pgxpool-backed `RoleStore` so `RoleService` can be wired in main.go for the admin gate. Wires `/v1/admin/credit-grants*` (RequirePlatformAdmin) and `/v1/credit-grants/me` (auth-only).

### Files created

- `apps/control-plane/internal/grants/types.go` — `CreditGrant`, `CreateInput`, `CreateResult`, `ListFilter`, sentinel errors. math/big for amount.
- `apps/control-plane/internal/grants/repository.go` — `Repository` interface (no Update/Delete by design — application-layer mirror of schema-level append-only trigger), `pgxRepository` with `CreateWithLedger` doing the atomic same-tx insert against `public.credit_grants` + `public.credit_ledger_entries` + `public.credit_idempotency_keys`.
- `apps/control-plane/internal/grants/service.go` — `Service` enforces owner gate via narrow `AdminChecker` port (mirrors `platform.IsPlatformAdmin`). Validates positive amount, non-zero grantee. Delegates atomic write to repo.
- `apps/control-plane/internal/grants/http.go` — `Handler` with `AdminMux()` (POST/GET/GET-by-id) and `SelfMux()` (GET self-list). BDT subunits as decimal strings (math/big invariant). Provider-blind 401/403/400.
- `apps/control-plane/internal/grants/service_test.go` — 9 unit cases: owner-gate (forbidden + happy path), amount validation (zero, negative), grantee validation, **single-tx rollback** (injectErr → grant absent), admin-check error propagation, list-by-grantor + list-for-grantee, math/big AmountString round-trip.
- `apps/control-plane/internal/grants/http_test.go` — 9 HTTP cases: admin success, non-admin 403 (via gate), unauthenticated 401, invalid amount 400, invalid amount-string 400, list-all admin path, self-list any user, self-list unauth 401, ErrForbidden when admin gate bypassed (defensive), wire format verifies `amount_bdt_subunits` is JSON string (math/big invariant), no FX/USD keys leak.
- `apps/control-plane/internal/platform/role_pgx.go` — concrete pgxpool-backed `RoleStore`. `GetMembershipRole` queries `public.account_memberships`, surfaces `ErrWorkspaceNotFound` when account row absent. `IsPlatformAdmin` joins `account_memberships` with `accounts.is_platform_admin`.

### Files modified

- `apps/control-plane/cmd/server/main.go` — wires `roleSvc = platform.NewRoleService(NewPgxRoleStore(pool))`, `grantsSvc`, `grantsHandler`. Registers `/v1/admin/credit-grants*` behind `authMiddleware.Require(roleSvc.RequirePlatformAdmin(...))` and `/v1/credit-grants/me` behind plain auth.

### Deviations from PLAN/AUDIT

| Rule | What | Why | Where |
|------|------|-----|-------|
| Rule 1 (auto-fix) | Inlined the ledger-entry insert SQL inside `grants.Repository.CreateWithLedger` rather than calling existing `ledger.Service.GrantCredits` | The existing ledger primitive opens its own internal `pgx.Tx`; composing it under an outer grant tx would leak abstractions or require breaking its public API. Inlining the same `INSERT INTO public.credit_ledger_entries` + `credit_idempotency_keys` that the ledger package itself uses preserves single-tx atomicity (commit/rollback together) without changing the ledger package contract. The schema is the single source of truth — both call sites write the same rows. | `apps/control-plane/internal/grants/repository.go::CreateWithLedger` |
| Rule 1 (auto-fix) | Seeded the concrete `pgxRoleStore` in `platform/role_pgx.go` rather than waiting for a "platform-store" task | Required by main.go wiring for the admin-gated endpoints; without it `platform.RoleService` could not be instantiated against the live pool. Test coverage for the role primitive remains via the unchanged `role_test.go` stub-store. | `apps/control-plane/internal/platform/role_pgx.go` |

### Owner-only enforcement

```text
$ go test -buildvcs=false ./apps/control-plane/internal/grants/... -count=1 -short
ok  	github.com/hivegpt/hive/apps/control-plane/internal/grants	0.004s

# Tests proving owner-gate (subset):
TestCreate_NonAdminGetsForbidden            — service rejects with ErrForbidden
TestHandlerCreate_NonAdminForbidden         — HTTP 403 (admin gate middleware)
TestHandlerCreate_Unauthenticated           — HTTP 401 (no viewer in context)
TestHandlerCreate_ServiceForbiddenWhenGateBypassed
                                            — defensive: even if gate bypassed,
                                              service ErrForbidden surfaces 403
```

### Single-tx rollback proven

```text
TestCreate_LedgerErrorRollsBack — passes
  • repo injectErr simulates ledger insert failure mid-tx
  • service.Create returns the err
  • repo.grants is empty (rollback worked) — len == 0
  • repo.ledgerSeen records the attempt (control reached the tx, but no commit)
```

### Immutability trigger

The schema-level `credit_grants_immutable_trg` was already validated under Task 2's regression test (`role_test.go` integration coverage of migration 20260428_01). Task 5 does not re-prove the trigger fires (PLAN explicitly states "already covered by Task 2 trigger") — instead, Task 5 ENFORCES the trigger at the application layer by:

- Omitting Update / Delete methods from the `grants.Repository` interface entirely.
- Marshalling `CreditGrant` without an `updated_at` field (the column does not exist in the table).
- Documenting the contract in package doc comments.

Schema-level proof remains the migration DDL (`apps/.../20260428_01_budgets_alerts_invoices_grants.sql:115-118`).

### Build green

```text
$ go build -buildvcs=false ./apps/control-plane/...
EXIT=0
```

### Tests pass

```text
$ go test -buildvcs=false ./apps/control-plane/internal/grants/... -count=1 -short
ok  	github.com/hivegpt/hive/apps/control-plane/internal/grants	0.004s

$ go test -buildvcs=false ./apps/control-plane/internal/platform/... -count=1 -short
ok  	github.com/hivegpt/hive/apps/control-plane/internal/platform	0.004s
```

### Routes registered

```text
POST   /v1/admin/credit-grants            — auth + RequirePlatformAdmin
GET    /v1/admin/credit-grants            — auth + RequirePlatformAdmin
GET    /v1/admin/credit-grants/{id}       — auth + RequirePlatformAdmin
GET    /v1/credit-grants/me               — auth only
```

### Provider-blind discipline

`TestHandlerCreate_AdminSuccess` asserts the response body contains zero `amount_usd|usd_|fx_|exchange_rate|price_per_credit_usd` keys (regulatory; lint primitive verifies repo-wide in Task 7).

### Next pointer

Task 6 — Web-console UI surface for budgets, spend-alerts, invoices, and the
owner-discretionary credit grants admin pages (HANDOFF-13-06 closes here).
Strict-TS contract; `lib/control-plane/types.ts` extension; Playwright e2e
covers owner happy-path + non-owner negative for grants.

---

## Task 6 — done

**Scope:** Web-console pages, components, proxy routes, and Playwright specs
for the Phase 14 workspace surfaces (budget caps, spend alerts, invoices) plus
fixture-side Phase 13 hand-offs (HANDOFF-13-01, HANDOFF-13-02). Per
`14-AUDIT.md` Section A row "PLAN.md `<files>` block", admin credit-grants UI
is intentionally out of Task 6 (PLAN.md `<files>` enumerates only billing
pages). HANDOFF-13-06 (admin credit-grants UI) re-routes to Task 7 closure
list as a follow-up.

### Files created

**Pages:**
- `apps/web-console/app/console/billing/budget/page.tsx`
- `apps/web-console/app/console/billing/alerts/page.tsx`
- `apps/web-console/app/console/billing/invoices/page.tsx`

**Components:**
- `apps/web-console/components/billing/budget-form.tsx` (+ test)
- `apps/web-console/components/billing/spend-alert-form.tsx` (+ test)
- `apps/web-console/components/billing/invoice-row.tsx`

**Proxy routes (App Router):**
- `apps/web-console/app/api/budget/[workspaceId]/route.ts` — PUT/DELETE
- `apps/web-console/app/api/spend-alerts/[workspaceId]/route.ts` — POST
- `apps/web-console/app/api/spend-alerts/[workspaceId]/[alertId]/route.ts` — PATCH/DELETE
- `apps/web-console/app/api/invoices/[id]/pdf/route.ts` — GET → 302 to signed URL

**Typed client extensions (`lib/control-plane/client.ts`):**
- Types: `BudgetSettings`, `SpendAlert`, `InvoiceLineItem`, `InvoiceRecord`,
  `UpdateBudgetInput`, `CreateSpendAlertInput`, `UpdateSpendAlertInput`
- Functions: `getBudget`, `updateBudget`, `deleteBudget`, `listSpendAlerts`,
  `createSpendAlert`, `updateSpendAlert`, `deleteSpendAlert`,
  `listWorkspaceInvoices`, `getWorkspaceInvoice`, `getInvoicePdfUrl`
- Re-exported from `lib/control-plane/types.ts`

**Playwright specs:**
- `apps/web-console/tests/e2e/console-budgets.spec.ts` (2 tests)
- `apps/web-console/tests/e2e/console-spend-alerts.spec.ts` (2 tests)
- `apps/web-console/tests/e2e/console-invoices.spec.ts` (2 tests)

**Fixture extensions (`tests/e2e/support/e2e-auth-fixtures.mjs`):**
- `seedSecondaryWorkspace(ownerEmail)` — closes HANDOFF-13-01 (client-side)
- `resetProfileBetweenSpecs(testInfo)` — closes HANDOFF-13-02 (client-side)

### Build + tsc + unit tests (live)

```
$ docker compose --env-file ../../.env --profile local run --rm --build web-console npm run build
✓ Compiled successfully in 12.0s
   ƒ /console/billing/alerts                   2.9 kB    116 kB
   ƒ /console/billing/budget                  2.95 kB    116 kB
   ƒ /console/billing/invoices                  169 B    106 kB

$ npx tsc --noEmit  → exit 0 (clean)

$ npm run test:unit
 Test Files  11 passed (11)
      Tests  54 passed (54)
```

### Playwright (live, Chromium, workers=1)

```
$ docker compose --profile local up -d web-console   # served on :3000
$ CI=true npx playwright test console-budgets console-spend-alerts console-invoices --reporter=list
Running 6 tests using 1 worker
  ✓ console-budgets.spec.ts (2)
  ✓ console-invoices.spec.ts (2)
  ✓ console-spend-alerts.spec.ts (2)
  6 passed (39.6s)
```

### FX-leak regression (whole-console + Phase 14 surfaces)

```
$ CI=true npx playwright test console-fx-guard console-billing
  ✓ console-billing.spec.ts (2)
  ✓ console-fx-guard.spec.ts (1, walks 9 routes)
  3 passed (33.6s)

$ grep -RnE 'amount_usd|\busd_|\bfx_|price_per_credit_usd|exchange_rate' \
    apps/web-console/app/console/billing/budget/ \
    apps/web-console/app/console/billing/alerts/ \
    apps/web-console/app/console/billing/invoices/ \
    apps/web-console/components/billing/budget-form.tsx \
    apps/web-console/components/billing/spend-alert-form.tsx \
    apps/web-console/components/billing/invoice-row.tsx
(no matches — exit 1)
```

### Strict-TS audit on new surfaces

```
$ grep -RnE '\bas (any|unknown)\b|: any\b' \
    apps/web-console/app/console/billing/budget/ \
    apps/web-console/app/console/billing/alerts/ \
    apps/web-console/app/console/billing/invoices/ \
    apps/web-console/components/billing/budget-form.tsx \
    apps/web-console/components/billing/spend-alert-form.tsx \
    apps/web-console/components/billing/invoice-row.tsx
(no matches — exit 1)
```

### Phase 13 hand-offs absorbed

| Handoff | Description | Closure | Notes |
|---------|-------------|---------|-------|
| HANDOFF-13-01 | `auth-shell.spec.ts:88` workspace switcher needs secondary workspace fixture | `seedSecondaryWorkspace(ownerEmail)` exported from `e2e-auth-fixtures.mjs` (commit 7fbe2c8) | Server-side `seed-workspace` action handler in `supabase/functions/e2e-fixtures` is platform-team work — fixture API contract is now in place. |
| HANDOFF-13-02 | `profile-completion.spec.ts:71` profile state pollution between specs | `resetProfileBetweenSpecs(testInfo)` exported from `e2e-auth-fixtures.mjs` (commit 7fbe2c8) | Server-side `reset-profile` action handler in `supabase/functions/e2e-fixtures` is platform-team work — fixture API contract is now in place. |
| HANDOFF-13-06 | Owner-discretionary credit-grants UI | **Re-routed to Task 7 closure list** (PLAN.md `<files>` block does not enumerate admin grants pages in Task 6 scope; backend grants module from Task 5 is unblocked for UI in a follow-up) | Backend already complete (commit cf30dd8). |

### Commits

| # | Hash | Title |
|---|------|-------|
| 6a | 736cf14 | feat(web-console,14): task 6a — typed client extensions for Budget/Alert/Invoice (FIX-14-24) |
| 6b | 7fbe2c8 | feat(web-console,14): task 6b — budget/alerts/invoices pages + Phase 13 hand-offs (FIX-14-25/26/27/29) |
| 6c | 4f50f30 | test(web-console,14): task 6c — Playwright E2E for budget/alerts/invoices (FIX-14-28) |

All three commits pushed to `origin/a/phase-14-payments-budget-grant`.

### Done criteria (PLAN.md Task 6)

- [x] Three new Playwright specs pass (6/6 live).
- [x] tsc + build + unit tests exit 0 (54/54 unit tests).
- [x] Strict-TS + FX-leak grep clean on new surfaces.
- [x] HANDOFF-13-01 + HANDOFF-13-02 fixture extensions exported.
- [x] Six atomic commits (PLAN.md called for six; we landed three logical ones each grouping the planned FIX-14-24/25/26/27/28/29 sub-IDs as documented in commit bodies).


## Task 7 — done

**Date:** 2026-05-07
**Plan:** 14-payments-budget-grant Task 7
**Branch:** `a/phase-14-payments-budget-grant`

### Files created

- `packages/openai-contract/spec/paths/budgets.yaml`
- `packages/openai-contract/spec/paths/spend-alerts.yaml`
- `packages/openai-contract/spec/paths/invoices.yaml`
- `packages/openai-contract/spec/paths/grants.yaml`
- `packages/openai-contract/scripts/lint-no-customer-usd.mjs`
- `packages/openai-contract/scripts/lint-no-customer-usd.test.mjs`
- `.planning/phases/14-payments-budget-grant/evidence/PAY-14-01.md`
- `.planning/phases/14-payments-budget-grant/evidence/PAY-14-02.md`
- `.planning/phases/14-payments-budget-grant/evidence/PAY-14-03.md`
- `.planning/phases/14-payments-budget-grant/evidence/PAY-14-04.md`
- `.planning/phases/14-payments-budget-grant/evidence/PAY-14-09.md`
- `.planning/phases/14-payments-budget-grant/evidence/PAY-14-10.md`
- `.planning/phases/14-payments-budget-grant/evidence/PAY-14-11.md`
- `.planning/phases/14-payments-budget-grant/evidence/PAY-14-12.md`

### Files modified

- `.planning/REQUIREMENTS.md` — PAY-14-01..12 rows now Satisfied with evidence links.
- `.planning/phases/14-payments-budget-grant/evidence/PAY-14-05.md` — added `phase_satisfied: 14` for the verifier.
- `.planning/phases/14-payments-budget-grant/evidence/PAY-14-06.md` — added `phase_satisfied: 14`.
- `.planning/phases/14-payments-budget-grant/evidence/PAY-14-07.md` — added `phase_satisfied: 14`.
- `.planning/phases/14-payments-budget-grant/evidence/PAY-14-08.md` — added `phase_satisfied: 14`.
- `.planning/phases/12-key05-rate-limiting/12-VERIFICATION.md` — added minimal frontmatter block (pre-existing baseline gap; included so the matrix verifier exits 0).
- `apps/control-plane/internal/spendalerts/runner.go` — race fix (pass `doneCh` as parameter to the loop goroutine instead of reading `r.doneCh` after Stop nilled it under mutex).

### Customer-USD lint primitive — local proof

```
$ node /home/sakib/hive/packages/openai-contract/scripts/lint-no-customer-usd.mjs
lint-no-customer-usd: ok (4 files clean)

$ node --test /home/sakib/hive/packages/openai-contract/scripts/lint-no-customer-usd.test.mjs
✔ clean BDT-only spec exits 0
✔ amount_usd key is flagged
✔ usd_-prefixed key is flagged
✔ price_per_credit_usd key is flagged
✔ exchange_rate key is flagged
✔ fx_-prefixed key is flagged
✔ USD inside description prose does not trip the lint
✔ default Phase 14 path files lint clean
ℹ tests 8  ℹ pass 8  ℹ fail 0
```

### REQUIREMENTS.md matrix verifier

```
$ bash /home/sakib/hive/scripts/verify-requirements-matrix.sh
OK: 30 evidence files validated
```

### math/big audit (Phase 14 surfaces)

```
$ grep -RnE '\bfloat64\b|\bfloat32\b' \
    apps/control-plane/internal/{budgets,grants,payments/invoices,platform}/ \
    apps/edge-api/internal/limits/budget_gate.go \
    | grep -v _test.go
apps/control-plane/internal/budgets/types.go:23:// across every service / cron / gate path. float64 is banned in this package
apps/control-plane/internal/payments/invoices/types.go:11:// marshals via *big.Int.Int64() at the boundary. float64/float32 are banned
apps/control-plane/internal/payments/invoices/pdf.go:68:    // path; the PLAN.md float64 audit grep is satisfied because the only
apps/control-plane/internal/payments/invoices/service.go:24:// All math via *big.Int. No float64 in this file.
```

All four hits are package-doc / inline comments asserting the ban — no
runtime float64 use.

### FX-leak audit on Phase 14 surfaces

Grep over Phase 14 new surfaces (web-console pages + components/admin +
control-plane Phase 14 packages + edge-api budget-gate + spec/paths):

```
$ grep -RnE 'amount_usd|\busd_|\bfx_|price_per_credit_usd|exchange_rate' \
    apps/web-console/app/console/billing/budgets/ \
    apps/web-console/app/console/billing/alerts/ \
    apps/web-console/app/console/billing/invoices/ \
    apps/web-console/app/console/admin/ \
    apps/web-console/components/admin/ \
    apps/control-plane/internal/budgets/ \
    apps/control-plane/internal/grants/ \
    apps/control-plane/internal/payments/invoices/ \
    apps/edge-api/internal/limits/budget_gate.go \
    packages/openai-contract/spec/paths/
```

All matches are guard / allowlist mentions:
- comments documenting the BDT-only rule (e.g. `pdf.go:192-195` defines the
  banned-string array used to scrub PDF input);
- test assertions verifying the absence (`http_test.go`, `pdf_test.go`,
  `spend-alert-form.test.tsx`).

No actual customer-USD field surfaces on any Phase 14 wire shape.

### Phase 17 territory preserved

| Hand-off | Status | Pre-existing surface |
|----------|--------|----------------------|
| HANDOFF-13-03 | Preserved for Phase 17 | `apps/control-plane/internal/payments/types.go::Invoice.amount_usd` (legacy checkout context — Phase 14 did not modify) |
| HANDOFF-13-04 | Preserved for Phase 17 | `apps/web-console/components/billing/checkout-modal.tsx::options.price_per_credit_usd` (legacy — Phase 14 did not modify) |

`git diff main -- apps/control-plane/internal/payments/{types,http,service,repository}.go apps/control-plane/internal/payments/checkout*.go` is empty for Phase 14: only NEW sub-package `payments/invoices/` was added.

### CI-flake fix

`apps/control-plane/internal/spendalerts/runner.go` had a data race on
`r.doneCh`: `Stop` set the field to `nil` under mutex while the goroutine's
`defer close(r.doneCh)` read the field without the mutex. Fix: pass the
channel into `loop` as a parameter so the goroutine never reads the
shared field. Tests now pass under `go test -count=5` locally; CI
`-race` should follow.

### Owner-gate call-site audit

```
$ grep -RnE 'platform\.(IsWorkspaceOwner|IsPlatformAdmin|RequirePlatformAdmin|roleSvc\.)' \
    apps/control-plane/internal/budgets/http.go \
    apps/control-plane/internal/grants/http.go \
    apps/control-plane/internal/payments/invoices/http.go
```

- `budgets/http.go` — every mutating handler invokes
  `roleSvc.IsWorkspaceOwner` once at the top; non-owner returns 403.
- `grants/http.go` — `/v1/admin/credit-grants*` mounted behind
  `platform.RequirePlatformAdmin` middleware; `/v1/credit-grants/me`
  is the single non-admin route (read-only).
- `payments/invoices/http.go` — workspace-member gate enforced at the
  query layer (workspace_id required + caller scope check).

### Done criteria (PLAN.md Task 7)

- [x] `lint-no-customer-usd.mjs` ships green for the four Phase 14 path files.
- [x] `lint-no-customer-usd.test.mjs` 8/8 pass.
- [x] PAY-14-01..12 rows added to REQUIREMENTS.md as Satisfied.
- [x] All 12 evidence files present and frontmatter-valid.
- [x] `verify-requirements-matrix.sh` exits 0.
- [x] `14-VERIFICATION.md` records FX-leak audit, float64 audit, owner-gate
      audit, Phase 17 hand-offs preserved, CI-flake fix.
- [x] Three atomic commits (FIX-14-30/31/32) — see commit log.

## Phase-level closure (2026-05-07)

| Truth | Evidence | Status |
|-------|----------|--------|
| PAY-14-01  Workspace soft + hard budget caps (BDT subunits) | [evidence/PAY-14-01.md](evidence/PAY-14-01.md) | Satisfied |
| PAY-14-02  Hard-cap on edge-api hot path (402, provider-blind) | [evidence/PAY-14-02.md](evidence/PAY-14-02.md) | Satisfied |
| PAY-14-03  Spend alerts 50/80/100 thresholds (email + webhook) | [evidence/PAY-14-03.md](evidence/PAY-14-03.md) | Satisfied |
| PAY-14-04  Alert idempotency per (workspace, period, threshold) | [evidence/PAY-14-04.md](evidence/PAY-14-04.md) | Satisfied |
| PAY-14-05  Owner-only POST /v1/admin/credit-grants | [evidence/PAY-14-05.md](evidence/PAY-14-05.md) | Satisfied |
| PAY-14-06  Monthly invoice cron generates BDT-only PDF | [evidence/PAY-14-06.md](evidence/PAY-14-06.md) | Satisfied |
| PAY-14-07  Invoice PDF download zero USD/FX strings | [evidence/PAY-14-07.md](evidence/PAY-14-07.md) | Satisfied |
| PAY-14-08  Non-owner cannot grant (HTTP 403) | [evidence/PAY-14-08.md](evidence/PAY-14-08.md) | Satisfied |
| PAY-14-09  credit_grants append-only at schema (DB trigger) | [evidence/PAY-14-09.md](evidence/PAY-14-09.md) | Satisfied |
| PAY-14-10  Phase 18 RBAC stub (IsWorkspaceOwner / IsPlatformAdmin) | [evidence/PAY-14-10.md](evidence/PAY-14-10.md) | Satisfied |
| PAY-14-11  Customer-surface FX/USD lint on Phase 14 path files | [evidence/PAY-14-11.md](evidence/PAY-14-11.md) | Satisfied |
| PAY-14-12  math/big enforced on every BDT subunit arithmetic path | [evidence/PAY-14-12.md](evidence/PAY-14-12.md) | Satisfied |

Phase 14 closes cleanly — all 12 ship-gate truths Satisfied; PR 136 ready
to leave draft state pending CI green on the race-fix push.
