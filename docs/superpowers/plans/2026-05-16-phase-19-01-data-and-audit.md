# Phase 19 / Plan 01 — Data Foundation + Audit Primitive Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land all Postgres migrations and the two foundational Go packages (`internal/tenant/settings` and `internal/audit`) that every later Phase 19 plan and every future phase depends on.

**Architecture:** One migration set installs `tenants`, `tenant_settings`, `tenant_users`, `audit_log` (partitioned, hash-chained, append-only role-gated), `audit_outbox`, `audit_outbox_dlq`, `llm_traces` (partitioned), plus the `custom_access_token_hook` Postgres function. Two Go packages expose the only sanctioned APIs: the tenant-settings resolver (in-process cache with `LISTEN/NOTIFY` invalidation) and the audit logger (two-tier write policy — security-tier sync-block with SHA-256 chain, LLM-tier WAL-fallback). Lint scripts in `tools/lint-*.mjs` block direct DB access that bypasses the helpers.

**Tech stack:** Go 1.24, Postgres 15+ (Supabase-hosted), `pgx` driver, `jackc/pgxlisten`, `github.com/google/uuid`, `github.com/stretchr/testify`, `testcontainers-go`. Lint scripts are Node-based ESM (matches the Phase 17 `lint-no-customer-usd.mjs` pattern).

---

## File Structure (Plan 01)

**New files (created):**
- `supabase/migrations/20260516_01_phase19_tenants.sql` — `tenants` table + RLS prep.
- `supabase/migrations/20260516_02_phase19_tenant_settings.sql` — `tenant_setting_key` enum + `tenant_settings` table + RLS policy.
- `supabase/migrations/20260516_03_phase19_tenant_users.sql` — `tenant_users` table + RLS policy.
- `supabase/migrations/20260516_04_phase19_audit_log.sql` — `audit_log` partitioned table + monthly partitions for current + next month + role grants + revokes.
- `supabase/migrations/20260516_05_phase19_audit_outbox.sql` — `audit_outbox` + `audit_outbox_dlq` tables + indexes.
- `supabase/migrations/20260516_06_phase19_llm_traces.sql` — `llm_traces` partitioned table.
- `supabase/migrations/20260516_07_phase19_custom_access_token_hook.sql` — `public.custom_access_token_hook` function + grant.
- `apps/control-plane/internal/tenant/settings/keys.go` — `Key` constants (mirror Postgres enum).
- `apps/control-plane/internal/tenant/settings/resolver.go` — `Resolver` interface + cache impl.
- `apps/control-plane/internal/tenant/settings/resolver_test.go` — table-driven unit tests + Testcontainers integration.
- `apps/control-plane/internal/tenant/settings/listener.go` — `LISTEN/NOTIFY` invalidator.
- `apps/control-plane/internal/audit/actions.go` — action registry + `IsSecurityAction`.
- `apps/control-plane/internal/audit/event.go` — `Event`, `Severity`, `Actor` types.
- `apps/control-plane/internal/audit/log.go` — `Log` entry point + two-tier dispatch.
- `apps/control-plane/internal/audit/sync.go` — security-tier sync writer + hash-chain.
- `apps/control-plane/internal/audit/wal.go` — LLM-tier WAL writer + drainer.
- `apps/control-plane/internal/audit/canonical.go` — canonical JSON encoder for hash input.
- `apps/control-plane/internal/audit/log_test.go` — unit + integration tests.
- `apps/control-plane/internal/audit/wal_test.go` — WAL round-trip + drainer test.
- `tools/lint-no-direct-tenant-setting.mjs` — CI lint script.
- `tools/lint-no-direct-audit-write.mjs` — CI lint script.

**Existing files (modified):**
- `go.work` — no change expected; if package layout requires new module-relative path, add to workspace.
- `apps/control-plane/go.mod` — add `github.com/jackc/pgx/v5`, `github.com/jackc/pgxlisten` if not already pulled.
- `package.json` (root) — add `"lint:tenant-setting"` and `"lint:audit-write"` npm scripts that invoke the new lint scripts.

---

## Branching prerequisite

Before Task 1, confirm the working branch. Current head is `a/phase-18-rbac-matrix` with the Phase 19 spec commit `d7f59c0` already on it. Cut Phase 19 branch off that head so the spec travels with the implementation.

```
git fetch origin
git switch -c a/phase-19-foundation
git push -u origin a/phase-19-foundation
```

If Phase 18 PR merges into `main` while Phase 19 work is in progress, rebase Phase 19 onto `main` at that point (`git rebase origin/main`); do not cherry-pick.

---

## Task 1: Migrate `tenants` table

**Files:**
- Create: `supabase/migrations/20260516_01_phase19_tenants.sql`

- [ ] **Step 1: Write the migration**

```sql
-- supabase/migrations/20260516_01_phase19_tenants.sql
-- Phase 19 — tenants table. Root of every tenant-scoped relation.

BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS public.tenants (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  slug        text UNIQUE NOT NULL,
  name        text NOT NULL,
  deployment  text NOT NULL CHECK (deployment IN ('HIVE_CLOUD','ENTERPRISE_EDGE')),
  created_at  timestamptz NOT NULL DEFAULT now(),
  archived_at timestamptz
);

CREATE INDEX IF NOT EXISTS tenants_deployment_idx ON public.tenants(deployment);
CREATE INDEX IF NOT EXISTS tenants_active_idx ON public.tenants(id) WHERE archived_at IS NULL;

ALTER TABLE public.tenants ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenants_self_read ON public.tenants
  FOR SELECT
  TO authenticated
  USING (id = (auth.jwt() ->> 'tenant_id')::uuid);

GRANT SELECT ON public.tenants TO authenticated;

COMMIT;
```

- [ ] **Step 2: Apply migration locally and verify schema**

Run (from repo root, using the existing toolchain pattern documented in `CLAUDE.md`):
```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && psql \"$SUPABASE_DB_URL\" -f supabase/migrations/20260516_01_phase19_tenants.sql"
```

Expected output: `CREATE EXTENSION` (or `NOTICE: extension "pgcrypto" already exists, skipping`), `CREATE TABLE`, `CREATE INDEX`, `CREATE INDEX`, `ALTER TABLE`, `CREATE POLICY`, `GRANT`, `COMMIT`.

- [ ] **Step 3: Smoke-test RLS isolation in psql**

```sql
SET LOCAL ROLE authenticated;
SET LOCAL "request.jwt.claims" = '{"tenant_id":"00000000-0000-0000-0000-000000000000"}';
SELECT count(*) FROM public.tenants;  -- expect 0
RESET ROLE;
```

Expected: `count = 0` because no tenant row matches the fake JWT.

- [ ] **Step 4: Commit**

```
git add supabase/migrations/20260516_01_phase19_tenants.sql
git commit -m "feat(phase-19): add tenants table with RLS"
```

---

## Task 2: Migrate `tenant_setting_key` enum and `tenant_settings` table

**Files:**
- Create: `supabase/migrations/20260516_02_phase19_tenant_settings.sql`

- [ ] **Step 1: Write the migration**

```sql
-- supabase/migrations/20260516_02_phase19_tenant_settings.sql
-- Phase 19 — central enum of every gateable feature + per-tenant settings.

BEGIN;

CREATE TYPE public.tenant_setting_key AS ENUM (
  'ENABLE_PUBLIC_BILLING',
  'ENABLE_BKASH',
  'ENABLE_SSLCOMMERZ',
  'ENABLE_STRIPE',
  'ENABLE_CREDIT_POOL',
  'ENABLE_PER_USER_CAP',
  'ENABLE_EXTRA_USAGE',
  'ENABLE_RAG_PERSONAL',
  'ENABLE_RAG_SHARED_KB',
  'ENABLE_MULTI_TENANT',
  'ENABLE_SSO_GOOGLE',
  'ENABLE_SSO_MICROSOFT',
  'ENABLE_SSO_SAML',
  'ENABLE_AUDIT_SINK_ELK',
  'ENABLE_AUDIT_SINK_LOKI',
  'ENABLE_AUDIT_SINK_DATADOG',
  'ENABLE_AUDIT_SINK_SPLUNK',
  'ENABLE_AUDIT_SINK_LANGFUSE',
  'ENABLE_ADMIN_CONSOLE',
  'ENABLE_PROVIDER_CUSTOM'
);

CREATE TABLE public.tenant_settings (
  tenant_id   uuid NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
  key         public.tenant_setting_key NOT NULL,
  enabled     boolean NOT NULL,
  value_json  jsonb,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  updated_by  uuid REFERENCES auth.users(id),
  PRIMARY KEY (tenant_id, key)
);

CREATE INDEX tenant_settings_key_enabled_idx
  ON public.tenant_settings(key) WHERE enabled = true;

ALTER TABLE public.tenant_settings ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_settings_isolation ON public.tenant_settings
  FOR ALL
  TO authenticated
  USING (tenant_id = (auth.jwt() ->> 'tenant_id')::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON public.tenant_settings TO authenticated;

-- Notify channel for in-process cache invalidation.
CREATE OR REPLACE FUNCTION public.notify_tenant_settings_changed()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_notify('tenant_settings_changed',
    json_build_object('tenant_id', NEW.tenant_id, 'key', NEW.key)::text);
  RETURN NEW;
END;
$$;

CREATE TRIGGER tenant_settings_notify
AFTER INSERT OR UPDATE OR DELETE ON public.tenant_settings
FOR EACH ROW EXECUTE FUNCTION public.notify_tenant_settings_changed();

COMMIT;
```

- [ ] **Step 2: Apply migration**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && psql \"$SUPABASE_DB_URL\" -f supabase/migrations/20260516_02_phase19_tenant_settings.sql"
```

Expected output: `CREATE TYPE`, `CREATE TABLE`, `CREATE INDEX`, `ALTER TABLE`, `CREATE POLICY`, `GRANT`, `CREATE FUNCTION`, `CREATE TRIGGER`, `COMMIT`.

- [ ] **Step 3: Confirm NOTIFY fires**

In a psql session:
```sql
LISTEN tenant_settings_changed;
INSERT INTO public.tenants(slug, name, deployment) VALUES ('test-cloud','Test Cloud','HIVE_CLOUD');
INSERT INTO public.tenant_settings(tenant_id, key, enabled)
SELECT id, 'ENABLE_CREDIT_POOL', true FROM public.tenants WHERE slug='test-cloud';
```

Expected: an `Asynchronous notification "tenant_settings_changed"` line containing the JSON payload appears.

Cleanup:
```sql
DELETE FROM public.tenants WHERE slug='test-cloud';
```

- [ ] **Step 4: Commit**

```
git add supabase/migrations/20260516_02_phase19_tenant_settings.sql
git commit -m "feat(phase-19): add tenant_setting_key enum and tenant_settings table"
```

---

## Task 3: Migrate `tenant_users` table

**Files:**
- Create: `supabase/migrations/20260516_03_phase19_tenant_users.sql`

- [ ] **Step 1: Write the migration**

```sql
-- supabase/migrations/20260516_03_phase19_tenant_users.sql
-- Phase 19 — user-to-tenant membership with role and status.

BEGIN;

CREATE TABLE public.tenant_users (
  tenant_id   uuid NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
  user_id     uuid NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  role        text NOT NULL CHECK (role IN ('OWNER','ADMIN','MEMBER','VIEWER')),
  status      text NOT NULL CHECK (status IN ('ACTIVE','SUSPENDED','INVITED')),
  invited_by  uuid REFERENCES auth.users(id),
  joined_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, user_id)
);

CREATE INDEX tenant_users_user_idx ON public.tenant_users(user_id);
CREATE INDEX tenant_users_status_idx ON public.tenant_users(tenant_id, status);

ALTER TABLE public.tenant_users ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_users_isolation ON public.tenant_users
  FOR ALL
  TO authenticated
  USING (tenant_id = (auth.jwt() ->> 'tenant_id')::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON public.tenant_users TO authenticated;

COMMIT;
```

- [ ] **Step 2: Apply migration**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && psql \"$SUPABASE_DB_URL\" -f supabase/migrations/20260516_03_phase19_tenant_users.sql"
```

Expected output: `CREATE TABLE`, `CREATE INDEX` (x2), `ALTER TABLE`, `CREATE POLICY`, `GRANT`, `COMMIT`.

- [ ] **Step 3: Commit**

```
git add supabase/migrations/20260516_03_phase19_tenant_users.sql
git commit -m "feat(phase-19): add tenant_users membership table"
```

---

## Task 4: Migrate `audit_log` (partitioned, hash-chained, role-gated)

**Files:**
- Create: `supabase/migrations/20260516_04_phase19_audit_log.sql`

- [ ] **Step 1: Write the migration**

```sql
-- supabase/migrations/20260516_04_phase19_audit_log.sql
-- Phase 19 — append-only audit log, partitioned monthly, hash-chained.

BEGIN;

-- Application role used by control-plane and edge-api.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'hive_app') THEN
    CREATE ROLE hive_app NOLOGIN;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'auditor_ro') THEN
    CREATE ROLE auditor_ro NOLOGIN;
  END IF;
END
$$;

CREATE TABLE public.audit_log (
  id                bigserial,
  tenant_id         uuid,
  actor_id          uuid,
  actor_type        text NOT NULL CHECK (actor_type IN ('USER','SERVICE','SYSTEM','EXTERNAL')),
  action            text NOT NULL,
  resource_type     text,
  resource_id       text,
  severity          text NOT NULL CHECK (severity IN ('DEBUG','INFO','NOTICE','WARNING','ERROR','CRITICAL')),
  before_json       jsonb,
  after_json        jsonb,
  request_id        uuid,
  source_ip         inet,
  user_agent        text,
  jwt_claims_digest text,
  deploy_sha        text NOT NULL,
  env               text NOT NULL,
  ts                timestamptz NOT NULL DEFAULT clock_timestamp(),
  seq               bigint NOT NULL,
  prev_hash         bytea NOT NULL,
  row_hash          bytea NOT NULL,
  PRIMARY KEY (ts, id)
) PARTITION BY RANGE (ts);

CREATE INDEX audit_log_tenant_ts_idx ON public.audit_log (tenant_id, ts DESC);
CREATE INDEX audit_log_action_ts_idx ON public.audit_log (action, ts DESC);
CREATE INDEX audit_log_severity_ts_idx ON public.audit_log (severity, ts DESC)
  WHERE severity IN ('ERROR','CRITICAL');

-- Current month + next month partitions. The control-plane will create future
-- partitions on a daily cron in a later plan; bootstrap two months here.
CREATE TABLE public.audit_log_2026_05 PARTITION OF public.audit_log
  FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE public.audit_log_2026_06 PARTITION OF public.audit_log
  FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

-- Per-partition seq must be monotonic. The Go helper enforces this by
-- selecting MAX(seq) for the partition under SERIALIZABLE.
CREATE INDEX audit_log_2026_05_seq_idx ON public.audit_log_2026_05 (seq);
CREATE INDEX audit_log_2026_06_seq_idx ON public.audit_log_2026_06 (seq);

REVOKE ALL ON public.audit_log FROM PUBLIC;
GRANT INSERT, SELECT ON public.audit_log TO hive_app;
GRANT SELECT ON public.audit_log TO auditor_ro;
GRANT USAGE ON SCHEMA public TO auditor_ro;

-- Cold archive manifest used by the daily archive job.
CREATE TABLE public.audit_cold_archive_manifest (
  partition_name text PRIMARY KEY,
  archived_at    timestamptz NOT NULL,
  parquet_path   text NOT NULL,
  parquet_sha256 text NOT NULL,
  row_count      bigint NOT NULL,
  last_prev_hash bytea NOT NULL,
  last_row_hash  bytea NOT NULL
);

COMMIT;
```

- [ ] **Step 2: Apply migration**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && psql \"$SUPABASE_DB_URL\" -f supabase/migrations/20260516_04_phase19_audit_log.sql"
```

Expected output: `DO`, `CREATE TABLE`, `CREATE INDEX` (x3), `CREATE TABLE` (x2 partitions), `CREATE INDEX` (x2), `REVOKE`, `GRANT` (x3), `CREATE TABLE` (manifest), `COMMIT`.

- [ ] **Step 3: Verify UPDATE/DELETE are blocked for `hive_app`**

```sql
SET LOCAL ROLE hive_app;
UPDATE public.audit_log SET action='X' WHERE 1=0;  -- expect: permission denied
DELETE FROM public.audit_log WHERE 1=0;            -- expect: permission denied
RESET ROLE;
```

Expected: `ERROR: permission denied for table audit_log` on both statements.

- [ ] **Step 4: Commit**

```
git add supabase/migrations/20260516_04_phase19_audit_log.sql
git commit -m "feat(phase-19): add partitioned audit_log with hash-chain columns and role gates"
```

---

## Task 5: Migrate `audit_outbox` + DLQ

**Files:**
- Create: `supabase/migrations/20260516_05_phase19_audit_outbox.sql`

- [ ] **Step 1: Write the migration**

```sql
-- supabase/migrations/20260516_05_phase19_audit_outbox.sql
-- Phase 19 — async fanout outbox and dead-letter queue for audit sinks.

BEGIN;

CREATE TABLE public.audit_outbox (
  id           bigserial PRIMARY KEY,
  audit_id     bigint NOT NULL,
  audit_ts     timestamptz NOT NULL,
  sink         text NOT NULL,
  attempts     int NOT NULL DEFAULT 0,
  last_error   text,
  delivered_at timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_outbox_undelivered
  ON public.audit_outbox(sink, created_at)
  WHERE delivered_at IS NULL;

CREATE INDEX audit_outbox_audit_id_idx
  ON public.audit_outbox(audit_id);

CREATE TABLE public.audit_outbox_dlq (
  LIKE public.audit_outbox INCLUDING ALL
);

GRANT INSERT, SELECT, UPDATE ON public.audit_outbox TO hive_app;
GRANT INSERT, SELECT ON public.audit_outbox_dlq TO hive_app;
GRANT SELECT ON public.audit_outbox     TO auditor_ro;
GRANT SELECT ON public.audit_outbox_dlq TO auditor_ro;

COMMIT;
```

- [ ] **Step 2: Apply migration**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && psql \"$SUPABASE_DB_URL\" -f supabase/migrations/20260516_05_phase19_audit_outbox.sql"
```

Expected output: `CREATE TABLE` (x2), `CREATE INDEX` (x2), `GRANT` (x4), `COMMIT`.

- [ ] **Step 3: Commit**

```
git add supabase/migrations/20260516_05_phase19_audit_outbox.sql
git commit -m "feat(phase-19): add audit_outbox and audit_outbox_dlq"
```

---

## Task 6: Migrate `llm_traces` (partitioned)

**Files:**
- Create: `supabase/migrations/20260516_06_phase19_llm_traces.sql`

- [ ] **Step 1: Write the migration**

```sql
-- supabase/migrations/20260516_06_phase19_llm_traces.sql
-- Phase 19 — high-cardinality LLM-call detail, partitioned monthly.

BEGIN;

CREATE TABLE public.llm_traces (
  id                uuid NOT NULL DEFAULT gen_random_uuid(),
  tenant_id         uuid NOT NULL,
  user_id           uuid,
  request_id        uuid NOT NULL,
  model             text NOT NULL,
  provider          text NOT NULL,
  in_tokens         int  NOT NULL,
  out_tokens        int  NOT NULL,
  latency_ms        int  NOT NULL,
  cost_credits      bigint NOT NULL,
  finish_reason     text,
  prompt_hash       text NOT NULL,
  completion_hash   text NOT NULL,
  retrieval_doc_ids text[],
  langfuse_trace_id text,
  ts                timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (ts, id)
) PARTITION BY RANGE (ts);

CREATE INDEX llm_traces_tenant_ts_idx ON public.llm_traces (tenant_id, ts DESC);
CREATE INDEX llm_traces_request_idx   ON public.llm_traces (request_id);

CREATE TABLE public.llm_traces_2026_05 PARTITION OF public.llm_traces
  FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE public.llm_traces_2026_06 PARTITION OF public.llm_traces
  FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

GRANT INSERT, SELECT ON public.llm_traces TO hive_app;
GRANT SELECT ON public.llm_traces TO auditor_ro;

COMMIT;
```

- [ ] **Step 2: Apply migration**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && psql \"$SUPABASE_DB_URL\" -f supabase/migrations/20260516_06_phase19_llm_traces.sql"
```

Expected output: `CREATE TABLE`, `CREATE INDEX` (x2), `CREATE TABLE` (x2), `GRANT` (x2), `COMMIT`.

- [ ] **Step 3: Commit**

```
git add supabase/migrations/20260516_06_phase19_llm_traces.sql
git commit -m "feat(phase-19): add partitioned llm_traces table"
```

---

## Task 7: Install `custom_access_token_hook`

**Files:**
- Create: `supabase/migrations/20260516_07_phase19_custom_access_token_hook.sql`

- [ ] **Step 1: Write the migration**

```sql
-- supabase/migrations/20260516_07_phase19_custom_access_token_hook.sql
-- Phase 19 — Supabase Auth custom-access-token hook. Injects
-- tenant_id, tenants[], and role into every issued access token.

BEGIN;

CREATE OR REPLACE FUNCTION public.custom_access_token_hook(event jsonb)
RETURNS jsonb
LANGUAGE plpgsql STABLE
AS $$
DECLARE
  claims        jsonb;
  uid           uuid;
  tenant_list   jsonb;
  selected      uuid;
  user_role     text;
BEGIN
  uid := (event->>'user_id')::uuid;
  claims := event->'claims';

  SELECT jsonb_agg(jsonb_build_object('id', t.id, 'role', tu.role))
    INTO tenant_list
    FROM public.tenant_users tu
    JOIN public.tenants t ON t.id = tu.tenant_id
   WHERE tu.user_id = uid
     AND tu.status  = 'ACTIVE'
     AND t.archived_at IS NULL;

  SELECT (raw_user_meta_data->>'selected_tenant_id')::uuid
    INTO selected
    FROM auth.users
   WHERE id = uid;

  IF selected IS NULL AND tenant_list IS NOT NULL
     AND jsonb_array_length(tenant_list) > 0 THEN
    selected := (tenant_list->0->>'id')::uuid;
  END IF;

  SELECT role INTO user_role
    FROM public.tenant_users
   WHERE user_id = uid AND tenant_id = selected;

  claims := claims
    || jsonb_build_object('tenant_id', selected)
    || jsonb_build_object('tenants',   COALESCE(tenant_list, '[]'::jsonb))
    || jsonb_build_object('role',      user_role);

  RETURN jsonb_build_object('claims', claims);
END;
$$;

GRANT EXECUTE ON FUNCTION public.custom_access_token_hook TO supabase_auth_admin;

COMMIT;
```

- [ ] **Step 2: Apply migration**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && psql \"$SUPABASE_DB_URL\" -f supabase/migrations/20260516_07_phase19_custom_access_token_hook.sql"
```

Expected output: `CREATE FUNCTION`, `GRANT`, `COMMIT`.

- [ ] **Step 3: Manual: register the hook in the Supabase dashboard**

In Supabase dashboard → Auth → Hooks → "Custom Access Token" → select `public.custom_access_token_hook`. Save. (This step is required once per environment and is documented in the README update in Plan 04.)

- [ ] **Step 4: Smoke-test the function**

```sql
SELECT public.custom_access_token_hook(
  jsonb_build_object(
    'user_id', (SELECT id FROM auth.users LIMIT 1),
    'claims',  jsonb_build_object('sub', 'x', 'aud', 'authenticated')
  )
);
```

Expected: a JSON object with a `claims` key containing `tenant_id`, `tenants`, and `role` (values may be `null` if the test user has no membership — that's fine).

- [ ] **Step 5: Commit**

```
git add supabase/migrations/20260516_07_phase19_custom_access_token_hook.sql
git commit -m "feat(phase-19): add Supabase custom_access_token_hook function"
```

---

## Task 8: Implement `internal/tenant/settings` Go package

**Files:**
- Create: `apps/control-plane/internal/tenant/settings/keys.go`
- Create: `apps/control-plane/internal/tenant/settings/resolver.go`
- Create: `apps/control-plane/internal/tenant/settings/listener.go`
- Test: `apps/control-plane/internal/tenant/settings/resolver_test.go`

- [ ] **Step 1: Write the failing test**

```go
// apps/control-plane/internal/tenant/settings/resolver_test.go
package settings_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"hive/control-plane/internal/tenant/settings"
)

func TestResolver_IsEnabled_UnsetReturnsFalse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, teardown := newTestPool(t, ctx)
	defer teardown()

	r := settings.NewResolver(pool, 30*time.Second)
	tid := mustTenant(t, ctx, pool, "t1", "HIVE_CLOUD")

	require.False(t, r.IsEnabled(ctx, tid, settings.EnableCreditPool))
}

func TestResolver_IsEnabled_ReadsValue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, teardown := newTestPool(t, ctx)
	defer teardown()

	r := settings.NewResolver(pool, 30*time.Second)
	tid := mustTenant(t, ctx, pool, "t2", "HIVE_CLOUD")

	_, err := pool.Exec(ctx,
		`INSERT INTO public.tenant_settings(tenant_id, key, enabled) VALUES ($1, 'ENABLE_CREDIT_POOL', true)`,
		tid)
	require.NoError(t, err)

	require.True(t, r.IsEnabled(ctx, tid, settings.EnableCreditPool))
}

func TestResolver_CacheInvalidatesOnNotify(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, teardown := newTestPool(t, ctx)
	defer teardown()

	r := settings.NewResolver(pool, 30*time.Second)
	go r.StartListener(ctx)
	time.Sleep(200 * time.Millisecond) // listener warmup

	tid := mustTenant(t, ctx, pool, "t3", "HIVE_CLOUD")
	require.False(t, r.IsEnabled(ctx, tid, settings.EnableRAGPersonal))

	_, err := pool.Exec(ctx,
		`INSERT INTO public.tenant_settings(tenant_id, key, enabled) VALUES ($1, 'ENABLE_RAG_PERSONAL', true)`,
		tid)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return r.IsEnabled(ctx, tid, settings.EnableRAGPersonal)
	}, 3*time.Second, 50*time.Millisecond, "cache should pick up the NOTIFY within 3 s")
}

// Helpers
func mustTenant(t *testing.T, ctx context.Context, pool poolIface, slug, deployment string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO public.tenants(slug, name, deployment) VALUES ($1, $1, $2) RETURNING id`,
		slug, deployment).Scan(&id)
	require.NoError(t, err)
	return id
}
```

`newTestPool` and `poolIface` are defined in a shared helper file `testhelpers_test.go` introduced in Step 3 below.

- [ ] **Step 2: Run the test to verify it fails**

```
cd deploy/docker && docker compose --profile tools run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/tenant/settings/... -count=1 -short -buildvcs=false"
```

Expected: compile error — package `settings` not found.

- [ ] **Step 3: Write the test helpers**

```go
// apps/control-plane/internal/tenant/settings/testhelpers_test.go
package settings_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

type poolIface interface {
	Exec(ctx context.Context, sql string, args ...any) (pgx.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func newTestPool(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	teardown := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM public.tenants WHERE slug LIKE 't%'`)
		pool.Close()
	}
	return pool, teardown
}
```

- [ ] **Step 4: Write `keys.go`**

```go
// apps/control-plane/internal/tenant/settings/keys.go
package settings

// Key is a tenant-setting identifier. It must mirror the
// public.tenant_setting_key Postgres enum exactly.
type Key string

const (
	EnablePublicBilling     Key = "ENABLE_PUBLIC_BILLING"
	EnableBkash             Key = "ENABLE_BKASH"
	EnableSSLCommerz        Key = "ENABLE_SSLCOMMERZ"
	EnableStripe            Key = "ENABLE_STRIPE"
	EnableCreditPool        Key = "ENABLE_CREDIT_POOL"
	EnablePerUserCap        Key = "ENABLE_PER_USER_CAP"
	EnableExtraUsage        Key = "ENABLE_EXTRA_USAGE"
	EnableRAGPersonal       Key = "ENABLE_RAG_PERSONAL"
	EnableRAGSharedKB       Key = "ENABLE_RAG_SHARED_KB"
	EnableMultiTenant       Key = "ENABLE_MULTI_TENANT"
	EnableSSOGoogle         Key = "ENABLE_SSO_GOOGLE"
	EnableSSOMicrosoft      Key = "ENABLE_SSO_MICROSOFT"
	EnableSSOSaml           Key = "ENABLE_SSO_SAML"
	EnableAuditSinkELK      Key = "ENABLE_AUDIT_SINK_ELK"
	EnableAuditSinkLoki     Key = "ENABLE_AUDIT_SINK_LOKI"
	EnableAuditSinkDatadog  Key = "ENABLE_AUDIT_SINK_DATADOG"
	EnableAuditSinkSplunk   Key = "ENABLE_AUDIT_SINK_SPLUNK"
	EnableAuditSinkLangfuse Key = "ENABLE_AUDIT_SINK_LANGFUSE"
	EnableAdminConsole      Key = "ENABLE_ADMIN_CONSOLE"
	EnableProviderCustom    Key = "ENABLE_PROVIDER_CUSTOM"
)
```

- [ ] **Step 5: Write `resolver.go`**

```go
// apps/control-plane/internal/tenant/settings/resolver.go
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Resolver is the single sanctioned API for reading tenant settings.
// Direct queries against the table are blocked by lint.
type Resolver struct {
	pool *pgxpool.Pool
	ttl  time.Duration

	mu    sync.RWMutex
	cache map[uuid.UUID]map[Key]entry
}

type entry struct {
	enabled bool
	value   json.RawMessage
	loaded  time.Time
}

func NewResolver(pool *pgxpool.Pool, ttl time.Duration) *Resolver {
	return &Resolver{
		pool:  pool,
		ttl:   ttl,
		cache: make(map[uuid.UUID]map[Key]entry),
	}
}

// IsEnabled returns true only when the row exists and enabled = true.
// An unset key returns false; callers that need to distinguish "off" from
// "unset" should use ValueRaw.
func (r *Resolver) IsEnabled(ctx context.Context, tenantID uuid.UUID, key Key) bool {
	e, ok := r.lookup(ctx, tenantID, key)
	if !ok {
		return false
	}
	return e.enabled
}

// ValueRaw returns the value_json column if the row exists.
func (r *Resolver) ValueRaw(ctx context.Context, tenantID uuid.UUID, key Key) (json.RawMessage, bool) {
	e, ok := r.lookup(ctx, tenantID, key)
	if !ok {
		return nil, false
	}
	return e.value, true
}

func (r *Resolver) lookup(ctx context.Context, tenantID uuid.UUID, key Key) (entry, bool) {
	r.mu.RLock()
	if perTenant, ok := r.cache[tenantID]; ok {
		if e, ok := perTenant[key]; ok && time.Since(e.loaded) < r.ttl {
			r.mu.RUnlock()
			return e, true
		}
	}
	r.mu.RUnlock()
	return r.refresh(ctx, tenantID, key)
}

func (r *Resolver) refresh(ctx context.Context, tenantID uuid.UUID, key Key) (entry, bool) {
	var e entry
	err := r.pool.QueryRow(ctx,
		`SELECT enabled, COALESCE(value_json, 'null'::jsonb)
		   FROM public.tenant_settings
		  WHERE tenant_id = $1 AND key = $2::public.tenant_setting_key`,
		tenantID, string(key)).Scan(&e.enabled, &e.value)
	if errors.Is(err, pgx.ErrNoRows) {
		return entry{}, false
	}
	if err != nil {
		// Fail closed: treat unreachable DB as "unknown setting". Callers
		// should treat a false here as the safe default for ENABLE_* keys.
		return entry{}, false
	}
	e.loaded = time.Now()
	r.mu.Lock()
	per, ok := r.cache[tenantID]
	if !ok {
		per = make(map[Key]entry, 4)
		r.cache[tenantID] = per
	}
	per[key] = e
	r.mu.Unlock()
	return e, true
}

// Invalidate drops cached entries for a tenant + key. Used by the LISTEN
// callback (see listener.go).
func (r *Resolver) Invalidate(tenantID uuid.UUID, key Key) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if per, ok := r.cache[tenantID]; ok {
		delete(per, key)
	}
}
```

- [ ] **Step 6: Write `listener.go`**

```go
// apps/control-plane/internal/tenant/settings/listener.go
package settings

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// StartListener subscribes to the tenant_settings_changed Postgres NOTIFY
// channel and invalidates cache entries on receipt. Blocks until ctx is
// cancelled. Callers run it in a goroutine.
func (r *Resolver) StartListener(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		conn, err := r.pool.Acquire(ctx)
		if err != nil {
			slog.Warn("tenant_settings listener: acquire failed", "err", err)
			time.Sleep(time.Second)
			continue
		}
		if _, err := conn.Exec(ctx, "LISTEN tenant_settings_changed"); err != nil {
			conn.Release()
			slog.Warn("tenant_settings listener: LISTEN failed", "err", err)
			time.Sleep(time.Second)
			continue
		}
		for {
			n, err := conn.Conn().WaitForNotification(ctx)
			if err != nil {
				conn.Release()
				if ctx.Err() != nil {
					return
				}
				slog.Warn("tenant_settings listener: wait failed", "err", err)
				break
			}
			r.handle(n)
		}
	}
}

func (r *Resolver) handle(n *pgx.Notification) {
	var payload struct {
		TenantID uuid.UUID `json:"tenant_id"`
		Key      Key       `json:"key"`
	}
	if err := json.Unmarshal([]byte(n.Payload), &payload); err != nil {
		slog.Warn("tenant_settings listener: bad payload", "err", err, "payload", n.Payload)
		return
	}
	r.Invalidate(payload.TenantID, payload.Key)
}
```

- [ ] **Step 7: Run tests to verify they pass**

Set the test DSN once per shell:
```
export HIVE_TEST_DB_URL="$SUPABASE_DB_URL"
```

Then:
```
cd deploy/docker && docker compose --profile tools run --rm -e HIVE_TEST_DB_URL toolchain "cd /workspace && go test ./apps/control-plane/internal/tenant/settings/... -count=1 -short -race -buildvcs=false"
```

Expected: `PASS`, `ok hive/control-plane/internal/tenant/settings`.

- [ ] **Step 8: Commit**

```
git add apps/control-plane/internal/tenant/settings/
git commit -m "feat(phase-19): add internal/tenant/settings resolver with LISTEN cache"
```

---

## Task 9: Implement `internal/audit` — security-tier sync writer with hash chain

**Files:**
- Create: `apps/control-plane/internal/audit/event.go`
- Create: `apps/control-plane/internal/audit/actions.go`
- Create: `apps/control-plane/internal/audit/canonical.go`
- Create: `apps/control-plane/internal/audit/sync.go`
- Create: `apps/control-plane/internal/audit/log.go`
- Test: `apps/control-plane/internal/audit/log_test.go`

- [ ] **Step 1: Write the failing test**

```go
// apps/control-plane/internal/audit/log_test.go
package audit_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"hive/control-plane/internal/audit"
)

func TestLog_SecurityTierWritesAndChains(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	w := audit.NewSyncWriter(pool, audit.WriterConfig{
		DeploySHA: "test-sha", Env: "test",
	})

	tid := uuid.New()
	rid := uuid.New()

	// First event in the (currently empty) partition.
	err := w.Write(ctx, audit.Event{
		TenantID:  tid,
		Actor:     audit.Actor{ID: uuid.New(), Type: audit.ActorUser},
		Action:    "AUTH_SIGNIN_SUCCESS",
		Severity:  audit.SeverityInfo,
		RequestID: rid,
	})
	require.NoError(t, err)

	// Second event chains off the first.
	err = w.Write(ctx, audit.Event{
		TenantID:  tid,
		Actor:     audit.Actor{ID: uuid.New(), Type: audit.ActorUser},
		Action:    "RBAC_DENY",
		Severity:  audit.SeverityWarning,
		RequestID: rid,
	})
	require.NoError(t, err)

	var seq1, seq2 int64
	var prev1, prev2, row1, row2 []byte
	rows, err := pool.Query(ctx,
		`SELECT seq, prev_hash, row_hash FROM public.audit_log WHERE tenant_id=$1 ORDER BY seq`,
		tid)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	require.NoError(t, rows.Scan(&seq1, &prev1, &row1))
	require.True(t, rows.Next())
	require.NoError(t, rows.Scan(&seq2, &prev2, &row2))

	require.Equal(t, int64(seq1+1), seq2)
	require.Equal(t, hex.EncodeToString(row1), hex.EncodeToString(prev2),
		"second row's prev_hash must equal first row's row_hash")
	require.NotEqual(t, sha256.Size, 0)
}

func TestLog_DispatchByTierUsesSyncForCritical(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t, context.Background())
	t.Cleanup(func() { pool.Close() })

	logger := audit.NewLogger(audit.LoggerDeps{
		Sync: audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"}),
		WAL:  &countingWAL{},
	})

	err := logger.Log(ctx, audit.Event{
		Action:   "CROSS_TENANT_ATTEMPT",
		Severity: audit.SeverityCritical,
	})
	require.NoError(t, err)

	require.Equal(t, 0, logger.Deps().WAL.(*countingWAL).count,
		"CRITICAL must NOT touch the WAL path")
}

type countingWAL struct{ count int }

func (w *countingWAL) Write(ctx context.Context, e audit.Event) error { w.count++; return nil }

func newPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	return pool
}
```

- [ ] **Step 2: Run the test to verify it fails**

```
cd deploy/docker && docker compose --profile tools run --rm -e HIVE_TEST_DB_URL toolchain "cd /workspace && go test ./apps/control-plane/internal/audit/... -count=1 -short -buildvcs=false"
```

Expected: compile error — package `audit` not found.

- [ ] **Step 3: Write `event.go`**

```go
// apps/control-plane/internal/audit/event.go
package audit

import "github.com/google/uuid"

type Severity string

const (
	SeverityDebug    Severity = "DEBUG"
	SeverityInfo     Severity = "INFO"
	SeverityNotice   Severity = "NOTICE"
	SeverityWarning  Severity = "WARNING"
	SeverityError    Severity = "ERROR"
	SeverityCritical Severity = "CRITICAL"
)

type ActorType string

const (
	ActorUser     ActorType = "USER"
	ActorService  ActorType = "SERVICE"
	ActorSystem   ActorType = "SYSTEM"
	ActorExternal ActorType = "EXTERNAL"
)

type Actor struct {
	ID   uuid.UUID
	Type ActorType
}

type Event struct {
	TenantID         uuid.UUID
	Actor            Actor
	Action           string
	ResourceType     string
	ResourceID       string
	Severity         Severity
	Before           any
	After            any
	RequestID        uuid.UUID
	SourceIP         string
	UserAgent        string
	JWTClaimsDigest  string
}
```

- [ ] **Step 4: Write `actions.go` (security-tier registry)**

```go
// apps/control-plane/internal/audit/actions.go
package audit

// Security-tier actions fail closed on Postgres write errors. LLM-tier
// actions fall back to local WAL on Postgres failure. Membership in this
// set is the source of truth; do not gate on Severity alone.
var securityActions = map[string]struct{}{
	"AUTH_SIGNIN_SUCCESS":          {},
	"AUTH_SIGNIN_FAILURE":          {},
	"AUTH_SIGNUP_SUCCESS":          {},
	"AUTH_JWT_INVALID":             {},
	"AUTH_JWT_EXPIRED":             {},
	"AUTH_SESSION_REVOKED":         {},
	"AUTH_SIGNIN_FAILURE_NO_TENANT":{},
	"AUTH_ROLE_CHANGE":             {},
	"RBAC_GRANT":                   {},
	"RBAC_REVOKE":                  {},
	"RBAC_DENY":                    {},
	"CROSS_TENANT_ATTEMPT":         {},
	"TENANT_SETTING_UPDATE":        {},
	"TENANT_SWITCH":                {},
	"TENANT_USER_ADD":              {},
	"TENANT_USER_REMOVE":           {},
	"OWUI_GROUP_CREATE_SUCCESS":    {},
	"OWUI_GROUP_CREATE_FAILURE":    {},
	"OWUI_GROUP_ADD_SUCCESS":       {},
	"OWUI_GROUP_ADD_FAILURE":       {},
	"API_KEY_ISSUE":                {},
	"API_KEY_REVOKE":               {},
	"CRYPTO_KEY_ROTATE":            {},
	"TLS_CERT_ROTATE":              {},
	"JWKS_FETCH_FAILURE":           {},
	"MIGRATION_APPLY":              {},
	"DEPLOY_PUSH":                  {},
	"BACKUP_INTEGRITY_FAIL":        {},
	"AUDIT_CHAIN_VERIFY_FAIL":      {},
	"SERVER_PANIC":                 {},
	"WEBHOOK_SIGNATURE_FAIL":       {},
	"INCIDENT_DECLARE":             {},
	"INCIDENT_RESOLVE":             {},
}

// IsSecurityAction reports whether the action must use the sync-block path.
func IsSecurityAction(action string) bool {
	_, ok := securityActions[action]
	return ok
}
```

- [ ] **Step 5: Write `canonical.go`**

```go
// apps/control-plane/internal/audit/canonical.go
package audit

import (
	"bytes"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
)

// canonicalRow is the deterministic JSON shape over which row_hash is taken.
// It excludes hash-related columns (seq, prev_hash, row_hash) so the hash
// covers payload plus identity, not the chain link itself.
type canonicalRow struct {
	TenantID        string `json:"tenant_id,omitempty"`
	ActorID         string `json:"actor_id,omitempty"`
	ActorType       string `json:"actor_type"`
	Action          string `json:"action"`
	ResourceType    string `json:"resource_type,omitempty"`
	ResourceID      string `json:"resource_id,omitempty"`
	Severity        string `json:"severity"`
	BeforeJSON      json.RawMessage `json:"before_json,omitempty"`
	AfterJSON       json.RawMessage `json:"after_json,omitempty"`
	RequestID       string `json:"request_id,omitempty"`
	SourceIP        string `json:"source_ip,omitempty"`
	UserAgent       string `json:"user_agent,omitempty"`
	JWTClaimsDigest string `json:"jwt_claims_digest,omitempty"`
	DeploySHA       string `json:"deploy_sha"`
	Env             string `json:"env"`
	TS              string `json:"ts"`
}

func canonicalize(e Event, deploySHA, env string, ts time.Time, before, after json.RawMessage) ([]byte, error) {
	row := canonicalRow{
		ActorType:       string(e.Actor.Type),
		Action:          e.Action,
		ResourceType:    e.ResourceType,
		ResourceID:      e.ResourceID,
		Severity:        string(e.Severity),
		BeforeJSON:      before,
		AfterJSON:       after,
		SourceIP:        e.SourceIP,
		UserAgent:       e.UserAgent,
		JWTClaimsDigest: e.JWTClaimsDigest,
		DeploySHA:       deploySHA,
		Env:             env,
		TS:              ts.UTC().Format(time.RFC3339Nano),
	}
	if e.TenantID != uuid.Nil {
		row.TenantID = e.TenantID.String()
	}
	if e.Actor.ID != uuid.Nil {
		row.ActorID = e.Actor.ID.String()
	}
	if e.RequestID != uuid.Nil {
		row.RequestID = e.RequestID.String()
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(row); err != nil {
		return nil, err
	}
	out := bytes.TrimRight(buf.Bytes(), "\n")
	return out, nil
}

// stableJSON marshals an arbitrary value with sorted map keys for hash stability.
func stableJSON(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m any
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw, nil
	}
	return marshalSorted(m), nil
}

func marshalSorted(v any) json.RawMessage {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var buf bytes.Buffer
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			buf.Write(kb)
			buf.WriteByte(':')
			buf.Write(marshalSorted(x[k]))
		}
		buf.WriteByte('}')
		return buf.Bytes()
	case []any:
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.Write(marshalSorted(e))
		}
		buf.WriteByte(']')
		return buf.Bytes()
	default:
		raw, _ := json.Marshal(v)
		return raw
	}
}
```

- [ ] **Step 6: Write `sync.go`**

```go
// apps/control-plane/internal/audit/sync.go
package audit

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WriterConfig struct {
	DeploySHA string
	Env       string
}

type SyncWriter struct {
	pool *pgxpool.Pool
	cfg  WriterConfig
}

func NewSyncWriter(pool *pgxpool.Pool, cfg WriterConfig) *SyncWriter {
	return &SyncWriter{pool: pool, cfg: cfg}
}

// Write inserts a single row and chains it off the last row in the same
// partition under SERIALIZABLE isolation.
func (w *SyncWriter) Write(ctx context.Context, e Event) error {
	if e.Action == "" {
		return errors.New("audit: action required")
	}
	if e.Severity == "" {
		return errors.New("audit: severity required")
	}

	before, err := stableJSON(e.Before)
	if err != nil {
		return fmt.Errorf("audit: marshal before: %w", err)
	}
	after, err := stableJSON(e.After)
	if err != nil {
		return fmt.Errorf("audit: marshal after: %w", err)
	}

	tx, err := w.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("audit: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ts := time.Now().UTC()
	partitionName := fmt.Sprintf("public.audit_log_%04d_%02d", ts.Year(), int(ts.Month()))

	var maxSeq int64
	var prevHash []byte
	err = tx.QueryRow(ctx,
		fmt.Sprintf(`SELECT COALESCE(MAX(seq), 0), COALESCE(
			(SELECT row_hash FROM %s ORDER BY seq DESC LIMIT 1),
			decode(repeat('00', 32), 'hex')
		) FROM %s`, partitionName, partitionName)).Scan(&maxSeq, &prevHash)
	if err != nil {
		return fmt.Errorf("audit: read prev hash: %w", err)
	}

	canon, err := canonicalize(e, w.cfg.DeploySHA, w.cfg.Env, ts, before, after)
	if err != nil {
		return fmt.Errorf("audit: canonicalize: %w", err)
	}
	sum := sha256.New()
	sum.Write(prevHash)
	sum.Write(canon)
	rowHash := sum.Sum(nil)

	_, err = tx.Exec(ctx, `
		INSERT INTO public.audit_log
		  (tenant_id, actor_id, actor_type, action, resource_type, resource_id,
		   severity, before_json, after_json, request_id, source_ip, user_agent,
		   jwt_claims_digest, deploy_sha, env, ts, seq, prev_hash, row_hash)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
	`,
		nullableUUID(e.TenantID), nullableUUID(e.Actor.ID), string(e.Actor.Type),
		e.Action, nullableString(e.ResourceType), nullableString(e.ResourceID),
		string(e.Severity), before, after, nullableUUID(e.RequestID),
		nullableString(e.SourceIP), nullableString(e.UserAgent),
		nullableString(e.JWTClaimsDigest), w.cfg.DeploySHA, w.cfg.Env,
		ts, maxSeq+1, prevHash, rowHash,
	)
	if err != nil {
		return fmt.Errorf("audit: insert: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("audit: commit: %w", err)
	}
	return nil
}
```

`nullableUUID` and `nullableString` are helpers in `helpers.go`:

```go
// apps/control-plane/internal/audit/helpers.go
package audit

import "github.com/google/uuid"

func nullableUUID(u uuid.UUID) any {
	if u == uuid.Nil {
		return nil
	}
	return u
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
```

- [ ] **Step 7: Write `log.go`**

```go
// apps/control-plane/internal/audit/log.go
package audit

import (
	"context"
	"errors"
)

// WALWriter mirrors SyncWriter but persists to local disk first. See wal.go.
type WALWriter interface {
	Write(ctx context.Context, e Event) error
}

type LoggerDeps struct {
	Sync *SyncWriter
	WAL  WALWriter
}

type Logger struct {
	deps LoggerDeps
}

func NewLogger(deps LoggerDeps) *Logger {
	return &Logger{deps: deps}
}

func (l *Logger) Deps() LoggerDeps { return l.deps }

// Log dispatches to Sync (security tier) or WAL (LLM tier) by action.
func (l *Logger) Log(ctx context.Context, e Event) error {
	if e.Action == "" {
		return errors.New("audit: action required")
	}
	if IsSecurityAction(e.Action) || e.Severity == SeverityError || e.Severity == SeverityCritical {
		return l.deps.Sync.Write(ctx, e)
	}
	return l.deps.WAL.Write(ctx, e)
}
```

- [ ] **Step 8: Run the tests**

```
cd deploy/docker && docker compose --profile tools run --rm -e HIVE_TEST_DB_URL toolchain "cd /workspace && go test ./apps/control-plane/internal/audit/... -count=1 -short -race -buildvcs=false"
```

Expected: `PASS`. `TestLog_SecurityTierWritesAndChains` and `TestLog_DispatchByTierUsesSyncForCritical` both green.

- [ ] **Step 9: Commit**

```
git add apps/control-plane/internal/audit/event.go \
        apps/control-plane/internal/audit/actions.go \
        apps/control-plane/internal/audit/canonical.go \
        apps/control-plane/internal/audit/sync.go \
        apps/control-plane/internal/audit/helpers.go \
        apps/control-plane/internal/audit/log.go \
        apps/control-plane/internal/audit/log_test.go
git commit -m "feat(phase-19): add internal/audit sync writer with SHA-256 hash chain"
```

---

## Task 10: Implement LLM-tier WAL writer + drainer

**Files:**
- Create: `apps/control-plane/internal/audit/wal.go`
- Test: `apps/control-plane/internal/audit/wal_test.go`

- [ ] **Step 1: Write the failing test**

```go
// apps/control-plane/internal/audit/wal_test.go
package audit_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"hive/control-plane/internal/audit"
)

func TestWAL_WriteThenDrain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dir := t.TempDir()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	sync := audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "test", Env: "test"})
	wal, err := audit.NewWALWriter(audit.WALConfig{
		Dir: dir, Sync: sync,
	})
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		require.NoError(t, wal.Write(ctx, audit.Event{
			TenantID:  uuid.New(),
			Actor:     audit.Actor{ID: uuid.New(), Type: audit.ActorUser},
			Action:    "CHAT_REQUEST",
			Severity:  audit.SeverityInfo,
			RequestID: uuid.New(),
		}))
	}

	// WAL file present?
	entries, err := os.ReadDir(filepath.Join(dir, "events"))
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	// Drain.
	drained, err := wal.Drain(ctx)
	require.NoError(t, err)
	require.Equal(t, 5, drained)

	// Files cleaned up.
	entries, err = os.ReadDir(filepath.Join(dir, "events"))
	require.NoError(t, err)
	require.Empty(t, entries)
}
```

- [ ] **Step 2: Run test to verify it fails**

```
cd deploy/docker && docker compose --profile tools run --rm -e HIVE_TEST_DB_URL toolchain "cd /workspace && go test ./apps/control-plane/internal/audit/... -run TestWAL -count=1 -short -buildvcs=false"
```

Expected: compile error — `audit.NewWALWriter` undefined.

- [ ] **Step 3: Write `wal.go`**

```go
// apps/control-plane/internal/audit/wal.go
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

type WALConfig struct {
	Dir  string       // absolute path; e.g. /var/lib/hive/audit-wal
	Sync *SyncWriter  // used by Drain() to flush stored events to Postgres
}

type FileWALWriter struct {
	cfg WALConfig
	mu  sync.Mutex
}

// Ensure WALWriter interface is satisfied.
var _ WALWriter = (*FileWALWriter)(nil)

func NewWALWriter(cfg WALConfig) (*FileWALWriter, error) {
	if cfg.Dir == "" {
		return nil, errors.New("audit: WALConfig.Dir required")
	}
	if cfg.Sync == nil {
		return nil, errors.New("audit: WALConfig.Sync required")
	}
	if err := os.MkdirAll(filepath.Join(cfg.Dir, "events"), 0o750); err != nil {
		return nil, fmt.Errorf("audit: mkdir wal: %w", err)
	}
	return &FileWALWriter{cfg: cfg}, nil
}

type walEnvelope struct {
	WrittenAt time.Time `json:"written_at"`
	Event     Event     `json:"event"`
}

// Write attempts a synchronous Postgres insert via the embedded SyncWriter.
// On Postgres failure, it appends a JSON envelope to disk and returns nil so
// the calling request is unaffected.
func (w *FileWALWriter) Write(ctx context.Context, e Event) error {
	// Short-deadline attempt against Postgres first.
	deadline, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	if err := w.cfg.Sync.Write(deadline, e); err == nil {
		return nil
	}

	// Fall back to disk.
	env := walEnvelope{WrittenAt: time.Now().UTC(), Event: e}
	raw, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("audit: wal marshal: %w", err)
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	name := fmt.Sprintf("%d-%s.json", time.Now().UnixNano(), uuid.NewString())
	path := filepath.Join(w.cfg.Dir, "events", name)
	if err := os.WriteFile(path, raw, 0o640); err != nil {
		return fmt.Errorf("audit: wal write: %w", err)
	}
	return nil
}

// Drain attempts to flush every WAL file in order. Returns the count
// successfully drained. A file that fails to write to Postgres remains on
// disk and the drainer stops at the first failure to preserve order.
func (w *FileWALWriter) Drain(ctx context.Context) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	dir := filepath.Join(w.cfg.Dir, "events")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("audit: wal readdir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, en := range entries {
		if en.IsDir() {
			continue
		}
		names = append(names, en.Name())
	}
	sort.Strings(names)

	drained := 0
	for _, name := range names {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return drained, fmt.Errorf("audit: wal read %s: %w", name, err)
		}
		var env walEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return drained, fmt.Errorf("audit: wal unmarshal %s: %w", name, err)
		}
		if err := w.cfg.Sync.Write(ctx, env.Event); err != nil {
			return drained, fmt.Errorf("audit: wal flush %s: %w", name, err)
		}
		if err := os.Remove(path); err != nil {
			return drained, fmt.Errorf("audit: wal remove %s: %w", name, err)
		}
		drained++
	}
	return drained, nil
}
```

- [ ] **Step 4: Run tests**

```
cd deploy/docker && docker compose --profile tools run --rm -e HIVE_TEST_DB_URL toolchain "cd /workspace && go test ./apps/control-plane/internal/audit/... -count=1 -short -race -buildvcs=false"
```

Expected: `PASS` on all three tests (`TestLog_SecurityTierWritesAndChains`, `TestLog_DispatchByTierUsesSyncForCritical`, `TestWAL_WriteThenDrain`).

- [ ] **Step 5: Commit**

```
git add apps/control-plane/internal/audit/wal.go \
        apps/control-plane/internal/audit/wal_test.go
git commit -m "feat(phase-19): add LLM-tier WAL writer with disk fallback and drainer"
```

---

## Task 11: Lint script — no direct `tenant_settings` access

**Files:**
- Create: `tools/lint-no-direct-tenant-setting.mjs`
- Modify: `package.json` (root) — add npm script.

- [ ] **Step 1: Write the lint script**

```javascript
// tools/lint-no-direct-tenant-setting.mjs
// Block code paths that read or write public.tenant_settings without going
// through the internal/tenant/settings resolver. Mirrors the Phase 17
// lint-no-customer-usd.mjs pattern.

import { readFileSync } from 'node:fs';
import { execSync } from 'node:child_process';

const ALLOWLIST_DIRS = [
  'apps/control-plane/internal/tenant/settings/',
  'supabase/migrations/',
  'tools/lint-no-direct-tenant-setting.mjs',
];

const FORBIDDEN = [
  /tenant_settings\b/i,                       // any SQL identifier reference
  /public\.tenant_settings\b/i,
  /from\s+tenant_settings\b/i,
  /into\s+tenant_settings\b/i,
];

const FILE_GLOB = "{apps,packages,deploy,tools,supabase}/**/*.{go,ts,tsx,js,mjs,cjs,sql,yml,yaml}";

const files = execSync(`git ls-files -- ${FILE_GLOB}`, { encoding: 'utf8' })
  .split('\n')
  .filter(Boolean);

let violations = 0;
for (const file of files) {
  if (ALLOWLIST_DIRS.some(p => file.startsWith(p))) continue;
  const text = readFileSync(file, 'utf8');
  for (const re of FORBIDDEN) {
    if (re.test(text)) {
      const lines = text.split('\n');
      lines.forEach((line, i) => {
        if (re.test(line)) {
          console.error(`${file}:${i + 1}: forbidden direct access to tenant_settings — use internal/tenant/settings.Resolver`);
          violations++;
        }
      });
    }
  }
}

if (violations > 0) {
  console.error(`\n${violations} tenant-settings lint violation(s).`);
  process.exit(1);
}
console.log('lint-no-direct-tenant-setting: PASS');
```

- [ ] **Step 2: Add npm script to `package.json`**

Add to the `"scripts"` block:

```json
"lint:tenant-setting": "node tools/lint-no-direct-tenant-setting.mjs"
```

- [ ] **Step 3: Run the lint locally — expect PASS on a clean tree**

```
npm run lint:tenant-setting
```

Expected: `lint-no-direct-tenant-setting: PASS`.

- [ ] **Step 4: Write a regression test by introducing a temporary violation**

In a scratch file `tmp_violation_test.go`:

```go
package scratch
const x = "SELECT * FROM tenant_settings WHERE 1=0"
```

Run:
```
git add tmp_violation_test.go
npm run lint:tenant-setting
```

Expected: exit code 1, message identifying the offending line.

Remove the scratch file:
```
git rm -f tmp_violation_test.go
npm run lint:tenant-setting
```

Expected: PASS.

- [ ] **Step 5: Commit**

```
git add tools/lint-no-direct-tenant-setting.mjs package.json
git commit -m "ci(phase-19): add lint blocking direct tenant_settings access"
```

---

## Task 12: Lint script — no direct `audit_log` access

**Files:**
- Create: `tools/lint-no-direct-audit-write.mjs`
- Modify: `package.json` (root) — add npm script.

- [ ] **Step 1: Write the lint script**

```javascript
// tools/lint-no-direct-audit-write.mjs
// Block direct INSERT/UPDATE/DELETE against public.audit_log outside the
// internal/audit package. SELECTs are allowed (auditor queries).

import { readFileSync } from 'node:fs';
import { execSync } from 'node:child_process';

const ALLOWLIST_DIRS = [
  'apps/control-plane/internal/audit/',
  'supabase/migrations/',
  'tools/lint-no-direct-audit-write.mjs',
];

const FORBIDDEN = [
  /insert\s+into\s+public\.audit_log\b/i,
  /update\s+public\.audit_log\b/i,
  /delete\s+from\s+public\.audit_log\b/i,
  /\bINSERT\s+INTO\s+audit_log\b/i,
];

const FILE_GLOB = "{apps,packages,deploy,tools,supabase}/**/*.{go,ts,tsx,js,mjs,cjs,sql,yml,yaml}";

const files = execSync(`git ls-files -- ${FILE_GLOB}`, { encoding: 'utf8' })
  .split('\n')
  .filter(Boolean);

let violations = 0;
for (const file of files) {
  if (ALLOWLIST_DIRS.some(p => file.startsWith(p))) continue;
  const text = readFileSync(file, 'utf8');
  for (const re of FORBIDDEN) {
    if (re.test(text)) {
      const lines = text.split('\n');
      lines.forEach((line, i) => {
        if (re.test(line)) {
          console.error(`${file}:${i + 1}: forbidden direct write to audit_log — use internal/audit.Log`);
          violations++;
        }
      });
    }
  }
}

if (violations > 0) {
  console.error(`\n${violations} audit-write lint violation(s).`);
  process.exit(1);
}
console.log('lint-no-direct-audit-write: PASS');
```

- [ ] **Step 2: Add npm script**

```json
"lint:audit-write": "node tools/lint-no-direct-audit-write.mjs"
```

- [ ] **Step 3: Run lint locally**

```
npm run lint:audit-write
```

Expected: `lint-no-direct-audit-write: PASS`.

- [ ] **Step 4: Commit**

```
git add tools/lint-no-direct-audit-write.mjs package.json
git commit -m "ci(phase-19): add lint blocking direct audit_log writes outside internal/audit"
```

---

## Plan 01 Self-Review Checklist

Before declaring Plan 01 complete, walk through this checklist with fresh eyes against the spec (`docs/superpowers/specs/2026-05-16-phase-19-foundation-design.md`):

- [ ] Spec §4 (tenant-settings data model) — all three tables (`tenants`, `tenant_settings`, `tenant_users`) created with RLS. Enum matches the spec exactly. Trigger + NOTIFY channel installed.
- [ ] Spec §5 (audit log + SOC 2) — `audit_log` partitioned, hash columns present, `hive_app` and `auditor_ro` roles created, INSERT/SELECT-only grants verified. `audit_outbox`, `audit_outbox_dlq`, `llm_traces` all created. Cold-archive manifest table present.
- [ ] Spec §6 (identity bridge) — `custom_access_token_hook` function exists with the canonical body and is registered in the Supabase dashboard (Task 7 Step 3 manual step; tick after performing).
- [ ] Spec §10 (testing) — Go test coverage for the new packages is ≥ 80%. Run:
  ```
  cd deploy/docker && docker compose --profile tools run --rm -e HIVE_TEST_DB_URL toolchain "cd /workspace && go test ./apps/control-plane/internal/... -cover -count=1 -short -buildvcs=false"
  ```
  Expected: `coverage: ≥80% of statements` for `internal/tenant/settings` and `internal/audit`.
- [ ] Lint scripts both green on the new tree.

## Hand-off to Plan 02

Plan 02 (`2026-05-16-phase-19-02-identity-and-auth.md`) consumes the artefacts produced here:

* `internal/tenant/settings.Resolver` — used by the new edge-api middlewares for tenant-setting gates.
* `internal/audit.Logger` — used by JWT middleware, RBAC handlers, signup webhook, OWUI admin client, tenant CRUD handlers.
* `public.custom_access_token_hook` — Plan 02 verifies JWT claims it produces via integration test.
* `tenants` / `tenant_users` — Plan 02 writes through them on signup.
* `audit_log` / `audit_outbox` / `audit_outbox_dlq` — Plan 02 emits security-tier rows from the new handlers.

No code from Plan 02 is required to merge Plan 01. Plan 01 is independently testable and shippable.
