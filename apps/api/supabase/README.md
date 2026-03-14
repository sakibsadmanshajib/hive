# Supabase migrations (Option A)

Source of truth for the Supabase CLI project: `supabase/migrations`

There are no app-scoped canonical SQL files in this folder anymore. Local bootstrap, CI smoke, and `npx supabase db reset --yes` execute the root `supabase/migrations/` directory.

Current CLI migration order:

1. `supabase/migrations/20260223000001_auth_user_tables.sql`
2. `supabase/migrations/20260223000002_api_keys.sql`
3. `supabase/migrations/20260223000003_billing_tables.sql`
4. `supabase/migrations/20260223000004_billing_rpcs.sql`
5. `supabase/migrations/20260313052000_refund_credits_rpc.sql`
6. `supabase/migrations/20260314000100_api_key_lifecycle.sql`
7. `supabase/migrations/20260314000200_guest_attribution.sql`
8. `supabase/migrations/20260314000300_usage_reporting_channels.sql`

## Operational flow

1. Apply the root `supabase/migrations/` files in sequence to the target Supabase project.
2. Confirm tables, indexes, RLS, and policies are present after each step.
3. Deploy API with matching Supabase feature flags for the domains you cut over.

## Notes

- Migrations are schema-only for auth/user, API key metadata, API key lifecycle audit trails, and billing persistence.
- Keep business formulas (for example, `1 BDT = 100 credits`) in API services, not SQL triggers.
- API key storage is hash-based (`key_hash`) for lookup; do not persist raw API keys.
