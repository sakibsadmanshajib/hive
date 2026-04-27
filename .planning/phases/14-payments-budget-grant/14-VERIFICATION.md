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
