# e2e-fixtures Supabase Edge Function

Server-side seed + reset of E2E test users, accounts, memberships, profiles,
and a single pending invitation. Callers POST once per test; the function
handles all Supabase admin-API work internally.

## Deploy

Install the Supabase CLI (host, not in the repo):

```bash
npm install -g supabase
```

Link the repo to the project (only needed once; password prompt uses
`SUPABASE_DB_PASSWORD`):

```bash
cd /home/sakib/hive
supabase link --project-ref yimgflllgdsbcibnaxqe
```

Deploy the function and configure its secret:

```bash
supabase functions deploy e2e-fixtures --no-verify-jwt

# Generate + set the shared secret (random 48+ chars)
SECRET=$(openssl rand -hex 32)
supabase secrets set E2E_FIXTURE_SECRET="$SECRET"
echo "put the same value into GH secrets E2E_FIXTURE_SECRET"
```

`--no-verify-jwt` is required: the fixture CI runner has no Supabase JWT — it
authenticates with the `X-E2E-Secret` header instead.

Add two GitHub Actions secrets in the repo settings:

| Secret | Value |
|--------|-------|
| `E2E_FIXTURE_URL` | `https://yimgflllgdsbcibnaxqe.functions.supabase.co/e2e-fixtures` |
| `E2E_FIXTURE_SECRET` | same value you set via `supabase secrets set` |

## Contract

```
POST /functions/v1/e2e-fixtures
Headers:
  X-E2E-Secret: <E2E_FIXTURE_SECRET>
  Content-Type: application/json
Body:
  { "action": "reset" }

200:
  {
    "verifiedEmail": "e2e-verified@scubed.com.bd",
    "unverifiedEmail": "e2e-unverified@scubed.com.bd",
    "verifiedPassword": "E2eFixture-Verified#2026",
    "unverifiedPassword": "E2eFixture-Unverified#2026",
    "invitationToken": "e2e-invitation-token-2026-fixture",
    "verifiedUserId": "...",
    "unverifiedUserId": "...",
    "inviterUserId": "...",
    "verifiedPrimaryAccountId": "...",
    "verifiedSecondaryAccountId": "...",
    "invitedAccountId": "...",
    "unverifiedAccountId": "..."
  }
```

Every call lands the database in the same deterministic state: three test
users exist with verified/unverified email status, one invitation is pending
for the verified user, and all prior profile/billing mutations are cleared.

## Fallback behaviour

`apps/web-console/tests/e2e/support/e2e-auth-fixtures.mjs` prefers the edge
function when both `E2E_FIXTURE_URL` and `E2E_FIXTURE_SECRET` are set.
Otherwise it falls back to the in-process admin-API path it ships with, so
local dev without a deployed function still works unchanged.

## Local function dev

```bash
supabase functions serve e2e-fixtures --env-file ../../.env
# in another shell:
curl -X POST http://localhost:54321/functions/v1/e2e-fixtures \
  -H "X-E2E-Secret: $E2E_FIXTURE_SECRET" \
  -H "Content-Type: application/json" \
  -d '{"action":"reset"}'
```
