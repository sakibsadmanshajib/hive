---
phase: 20-provider-catalog
plan: 01
type: execute
wave: 1
depends_on: []
size: M
branch: b/phase-20-provider-catalog
milestone: v1.1
track: A
files_modified:
  - supabase/migrations/YYYYMMDD_01_phase20_provider_catalog_schema.sql
autonomous: true
---

# Plan 20-01 — Schema: custom_providers, tenant_model_visibility, provider_routes relaxation

## Objective

Create the two new tables that underpin the provider catalog feature and relax the existing `provider_routes` CHECK constraint so operators can register providers beyond the hardcoded `(openrouter, groq)` pair. All DDL follows the RLS pattern established in `supabase/migrations/20260529_01_*.sql`.

---

## Tasks

### Task 1: Relax provider_routes CHECK constraint

**File:** `supabase/migrations/YYYYMMDD_01_phase20_provider_catalog_schema.sql`

The current `provider_routes` table carries:

```sql
CONSTRAINT provider_routes_provider_check
  CHECK (provider IN ('openrouter', 'groq'))
```

defined in `supabase/migrations/20260331_02_routing_policy.sql`.

Drop and replace with a free-text non-empty constraint so any provider slug registered in `custom_providers` is valid:

```sql
ALTER TABLE public.provider_routes
  DROP CONSTRAINT IF EXISTS provider_routes_provider_check;

ALTER TABLE public.provider_routes
  ADD CONSTRAINT provider_routes_provider_nonempty
    CHECK (provider <> '');
```

**Acceptance:** `\d provider_routes` no longer shows `IN ('openrouter', 'groq')`.

---

### Task 2: Create custom_providers table

```sql
CREATE TABLE IF NOT EXISTS public.custom_providers (
  id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  slug          TEXT        NOT NULL UNIQUE,          -- e.g. "openrouter", "groq", "together"
  display_name  TEXT        NOT NULL,
  base_url      TEXT        NOT NULL,
  api_key_env   TEXT        NOT NULL,                 -- name of env var holding the key, e.g. "OPENROUTER_API_KEY"
  litellm_prefix TEXT       NOT NULL,                 -- LiteLLM provider prefix, e.g. "openrouter/"
  enabled       BOOLEAN     NOT NULL DEFAULT true,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS custom_providers_slug_idx ON public.custom_providers (slug);
CREATE INDEX IF NOT EXISTS custom_providers_enabled_idx ON public.custom_providers (enabled);
```

Seed the two known providers so existing `provider_routes` rows remain referentially consistent:

```sql
INSERT INTO public.custom_providers (slug, display_name, base_url, api_key_env, litellm_prefix, enabled)
VALUES
  ('openrouter', 'OpenRouter', 'https://openrouter.ai/api/v1', 'OPENROUTER_API_KEY', 'openrouter/', true),
  ('groq',       'Groq',       'https://api.groq.com/openai/v1', 'GROQ_API_KEY',     'groq/',       true)
ON CONFLICT (slug) DO NOTHING;
```

---

### Task 3: Create tenant_model_visibility table

```sql
CREATE TABLE IF NOT EXISTS public.tenant_model_visibility (
  id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    UUID        NOT NULL REFERENCES public.accounts(id) ON DELETE CASCADE,
  alias_id     UUID        NOT NULL REFERENCES public.model_aliases(id) ON DELETE CASCADE,
  visible      BOOLEAN     NOT NULL DEFAULT true,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, alias_id)
);

CREATE INDEX IF NOT EXISTS tmv_tenant_idx ON public.tenant_model_visibility (tenant_id);
CREATE INDEX IF NOT EXISTS tmv_alias_idx  ON public.tenant_model_visibility (alias_id);
```

---

### Task 4: RLS policies

Follow the pattern from `supabase/migrations/20260529_01_*.sql` exactly (enable + force, then idempotent service-role policy):

```sql
-- custom_providers
ALTER TABLE public.custom_providers ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.custom_providers FORCE ROW LEVEL SECURITY;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE tablename = 'custom_providers'
      AND policyname = 'custom_providers_service_role_all'
  ) THEN
    CREATE POLICY custom_providers_service_role_all
      ON public.custom_providers
      FOR ALL TO hive_app
      USING (true)
      WITH CHECK (true);
  END IF;
END $$;

-- tenant_model_visibility
ALTER TABLE public.tenant_model_visibility ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.tenant_model_visibility FORCE ROW LEVEL SECURITY;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE tablename = 'tenant_model_visibility'
      AND policyname = 'tenant_model_visibility_service_role_all'
  ) THEN
    CREATE POLICY tenant_model_visibility_service_role_all
      ON public.tenant_model_visibility
      FOR ALL TO hive_app
      USING (true)
      WITH CHECK (true);
  END IF;
END $$;
```

---

### Task 5: updated_at trigger (reuse or create)

Apply the standard `set_updated_at()` trigger (already present in the migration history) to both new tables:

```sql
CREATE TRIGGER set_custom_providers_updated_at
  BEFORE UPDATE ON public.custom_providers
  FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

CREATE TRIGGER set_tenant_model_visibility_updated_at
  BEFORE UPDATE ON public.tenant_model_visibility
  FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();
```

If `public.set_updated_at()` does not exist at time of execution, define it once in this migration (idempotent `CREATE OR REPLACE`).

---

### TDD Notes

Write a SQL migration test (same pattern as existing `*_test.sql` files in `/tmp/`):

1. Verify `custom_providers_provider_check` constraint is gone from `provider_routes`.
2. Insert a row into `custom_providers` with slug `"together"` and verify it saves.
3. Insert a row into `provider_routes` with `provider = 'together'` and verify it no longer rejects.
4. Insert a row into `tenant_model_visibility` referencing a known account and alias, verify uniqueness constraint on duplicate.

---

## Acceptance Criteria

- [ ] `provider_routes` CHECK constraint no longer enumerates `openrouter`/`groq`; any non-empty string is accepted.
- [ ] `custom_providers` table exists with `slug UNIQUE`, `enabled`, `litellm_prefix` columns.
- [ ] Seed rows for `openrouter` and `groq` present (idempotent).
- [ ] `tenant_model_visibility` table exists with `UNIQUE(tenant_id, alias_id)`.
- [ ] Both tables have RLS enabled + forced; `hive_app` service-role policy (idempotent DO $$) present.
- [ ] `updated_at` triggers fire on both tables.
- [ ] Migration file named `YYYYMMDD_01_phase20_provider_catalog_schema.sql` following project convention.
- [ ] `supabase db push` (or `apply_migration`) applies cleanly with no errors.
