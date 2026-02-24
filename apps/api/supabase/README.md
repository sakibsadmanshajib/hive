# Supabase migrations (Option A)

Location: `apps/api/supabase/migrations`

Apply these SQL files in order:

1. `apps/api/supabase/migrations/20260223_001_auth_user_tables.sql`
2. `apps/api/supabase/migrations/20260223_002_api_keys.sql`
3. `apps/api/supabase/migrations/20260223_003_billing_tables.sql`

## Operational flow

1. Apply each migration in sequence to the target Supabase project.
2. Confirm tables, indexes, RLS, and policies are present after each step.
3. Deploy API with matching Supabase feature flags for the domains you cut over.

## Notes

- Migrations are schema-only for auth/user, API key metadata, and billing persistence.
- Keep business formulas (for example, `1 BDT = 100 credits`) in API services, not SQL triggers.
- API key storage is hash-based (`key_hash`) for lookup; do not persist raw API keys.
