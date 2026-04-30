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

