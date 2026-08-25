-- supabase/migrations/20260824_03_tenant_provider_keys.sql
--
-- Tenant BYOK (bring your own key): tenants register their own provider
-- credentials and Hive routes their traffic through them. This migration is
-- the storage half; the register/list/revoke API lives in
-- apps/control-plane/internal/byok, and the routing preference seam is
-- documented in the follow-up issue filed with this PR.
--
-- Credential material is encrypted at the application layer with AES-256-GCM
-- before it ever reaches this table (HIVE_BYOK_ENC_KEY); the database only
-- ever sees ciphertext in encrypted_api_key. key_last4 is the only plaintext
-- remnant of the secret, stored so lists render a customer-safe mask without
-- decrypting anything.
--
-- Scoping: one row per account (workspace), same principal that owns
-- public.api_keys. Exactly one target per row: either a reference to a
-- platform-registered custom_providers slug, or a freeform OpenAI-compatible
-- base_url plus an optional model_map of requested alias -> upstream model.
--
-- RLS mirrors public.custom_providers (20260611_01): FORCE RLS + a permissive
-- policy for hive_app only. Isolation between accounts is enforced at the
-- application layer (every query filters account_id; see
-- internal/byok/repository.go), the same split custom_providers uses for its
-- platform-level rows. anon and authenticated roles have no policy at all,
-- which is default-deny under enabled+forced RLS.

BEGIN;

CREATE TABLE public.tenant_provider_keys (
  id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id         UUID        NOT NULL REFERENCES public.accounts(id) ON DELETE CASCADE,
  label              TEXT        NOT NULL CHECK (char_length(label) BETWEEN 1 AND 100),
  provider_slug      TEXT        NULL REFERENCES public.custom_providers(slug) ON DELETE RESTRICT,
  base_url           TEXT        NULL,
  model_map          JSONB       NOT NULL DEFAULT '{}'::jsonb,
  encrypted_api_key  BYTEA       NOT NULL,
  key_last4          TEXT        NOT NULL CHECK (char_length(key_last4) = 4),
  status             TEXT        NOT NULL DEFAULT 'active'
                                 CHECK (status IN ('active', 'revoked')),
  created_by_user_id UUID        NOT NULL REFERENCES auth.users(id),
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  revoked_at         TIMESTAMPTZ NULL,
  CONSTRAINT tenant_provider_keys_target_check CHECK (
    (provider_slug IS NOT NULL AND provider_slug <> '' AND base_url IS NULL)
    OR
    (provider_slug IS NULL AND base_url IS NOT NULL AND base_url <> '')
  )
);

-- One active credential per provider per account: re-registering replaces
-- nothing silently, the caller must revoke first.
CREATE UNIQUE INDEX tenant_provider_keys_account_slug_active_idx
  ON public.tenant_provider_keys (account_id, provider_slug)
  WHERE status = 'active' AND provider_slug IS NOT NULL;

CREATE INDEX tenant_provider_keys_account_created_idx
  ON public.tenant_provider_keys (account_id, created_at DESC);

COMMENT ON TABLE public.tenant_provider_keys IS
  'Tenant BYOK credentials (bring your own key). encrypted_api_key holds AES-256-GCM ciphertext produced by the control plane with HIVE_BYOK_ENC_KEY; plaintext never reaches the database. key_last4 is the only secret remnant, for display masks. Exactly one target per row via tenant_provider_keys_target_check: a custom_providers slug reference or a freeform https(s) base_url, plus an optional model_map of requested alias to upstream model name. Account isolation is enforced in internal/byok/repository.go (every query filters account_id); RLS keeps every role except hive_app fully locked out.';

ALTER TABLE public.tenant_provider_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.tenant_provider_keys FORCE ROW LEVEL SECURITY;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE schemaname = 'public'
      AND tablename  = 'tenant_provider_keys'
      AND policyname = 'tenant_provider_keys_hive_app_all'
  ) THEN
    CREATE POLICY tenant_provider_keys_hive_app_all
      ON public.tenant_provider_keys
      AS PERMISSIVE FOR ALL TO hive_app
      USING (true)
      WITH CHECK (true);
  END IF;
END$$;

GRANT SELECT, INSERT, UPDATE ON public.tenant_provider_keys TO hive_app;
GRANT SELECT ON public.tenant_provider_keys TO auditor_ro;

COMMIT;
