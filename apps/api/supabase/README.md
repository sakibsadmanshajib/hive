# Supabase migrations (Option A)

Run these migrations in order:

1. `migrations/20260223_001_auth_user_tables.sql`
2. `migrations/20260223_002_api_keys.sql`
3. `migrations/20260223_003_billing_tables.sql`

Notes:

- These files are schema-only scaffolding for auth/user, API key metadata, and billing persistence.
- Keep business formulas (for example, `1 BDT = 100 credits`) in API services, not SQL triggers.
- API key storage is hash-based (`key_hash`) for lookup; do not persist raw API keys.
