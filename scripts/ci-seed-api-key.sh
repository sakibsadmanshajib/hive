#!/usr/bin/env bash
# ci-seed-api-key.sh -- mint one Hive API key, and the rows behind it, on a
# throwaway Postgres. Prints exactly one line to stdout:
#
#   HIVE_API_KEY=hk_...
#
# and nothing else; everything informational goes to stderr, so a caller can
# capture the value, mask it, and never have a diagnostic line land in the
# variable. The key is generated here per run and never committed: there is no
# fixed CI key to leak, and no shared account behind it.
#
# What it creates, and why each row is load bearing (the same set
# scripts/seed-owui-e2e-user.py provisions through PostgREST, written as SQL
# here because a throwaway database has no GoTrue and no PostgREST in front of
# it):
#
#   auth.users              accounts.owner_user_id and api_keys.created_by_user_id
#                           are both NOT NULL foreign keys into it.
#   public.tenants          the workspace the key's traffic is attributed to.
#   public.accounts         the billing account the key belongs to.
#   tenant_billing_accounts the mapping edge-api resolves on every call. Without
#                           it every request 403s account_not_provisioned, which
#                           is issue #717 and is what makes this row worth a
#                           comment rather than a line in a list.
#   public.api_keys         the key itself, stored as a sha256 hex digest of the
#                           raw secret, matching generateSecret() in
#                           apps/control-plane/internal/apikeys/service.go.
#   api_key_policies        allow_all_models, so the key is not narrowed to an
#                           alias allowlist the SDK suites do not know about.
#   credit_ledger_entries   a grant. The reservation guard in
#                           apps/edge-api/internal/inference/reservation_guard.go
#                           fails closed on a zero balance, so a key with no
#                           credit authenticates and then refuses every
#                           completion.
#
# Connection settings come from libpq environment variables the caller exports.

set -euo pipefail

: "${PGHOST:?PGHOST is required}"

# Same shape as generateSecret(): 32 random bytes, base64url without padding,
# hk_ prefix, sha256 hex digest stored.
raw_secret="hk_$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=\n')"
token_hash="$(printf '%s' "$raw_secret" | sha256sum | awk '{print $1}')"
redacted_suffix="${raw_secret: -6}"

# A grant large enough that no suite can exhaust it, and small enough to stay
# an obviously synthetic number. Credits are integer subunits.
grant_credits="${HIVE_CI_SEED_CREDITS:-100000000}"

slug="${HIVE_CI_SEED_SLUG:-ci-throwaway}"

psql --no-psqlrc -qX -v ON_ERROR_STOP=1 \
  -v token_hash="$token_hash" \
  -v redacted_suffix="$redacted_suffix" \
  -v slug="$slug" \
  -v grant_credits="$grant_credits" \
  >&2 <<'SQL'
BEGIN;

-- One synthetic identity.
--
-- The ON CONFLICT clauses below make a re-run against the same throwaway
-- database SURVIVE rather than fail on a duplicate slug. They do not make it a
-- no-op, and an earlier version of this comment claimed they did. A second run
-- inserts another auth.users row (that CTE has no conflict target), reuses the
-- existing tenant and account, and then mints a second active api_key with its
-- own policy and its own credit grant. The four assertions below still pass,
-- because each one is scoped to the token_hash this run just generated.
--
-- That is acceptable here and nowhere else: this script is invoked exactly once
-- per throwaway database, by a job that destroys it minutes later, so the
-- accumulation has nothing to accumulate into. It is written down rather than
-- left to be discovered because the same script pointed at a database that
-- outlives one job would quietly grow orphan identities, duplicate live
-- credentials and repeated grants, and the assertions would report green
-- through all of it.
WITH u AS (
  INSERT INTO auth.users (id, email)
  VALUES (gen_random_uuid(), format('%s@ci.invalid', :'slug'))
  RETURNING id
), t AS (
  INSERT INTO public.tenants (slug, name, deployment)
  VALUES (:'slug', 'CI throwaway workspace', 'HIVE_CLOUD')
  ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name
  RETURNING id
), a AS (
  INSERT INTO public.accounts (slug, display_name, account_type, owner_user_id)
  SELECT :'slug', 'CI throwaway account', 'business', u.id FROM u
  ON CONFLICT (slug) DO UPDATE SET display_name = EXCLUDED.display_name
  RETURNING id, owner_user_id
), m AS (
  INSERT INTO public.tenant_billing_accounts (tenant_id, account_id)
  SELECT t.id, a.id FROM t, a
  ON CONFLICT DO NOTHING
  RETURNING tenant_id
), k AS (
  INSERT INTO public.api_keys
    (account_id, nickname, token_hash, redacted_suffix, status, created_by_user_id)
  SELECT a.id, 'ci-throwaway', :'token_hash', :'redacted_suffix', 'active', a.owner_user_id
  FROM a
  RETURNING id, account_id
), p AS (
  INSERT INTO public.api_key_policies (api_key_id, allow_all_models)
  SELECT k.id, true FROM k
  RETURNING api_key_id
)
INSERT INTO public.credit_ledger_entries
  (account_id, entry_type, credits_delta, idempotency_key, metadata)
SELECT k.account_id, 'grant', :'grant_credits'::bigint,
       'ci-throwaway-grant-' || k.id::text,
       jsonb_build_object('source', 'scripts/ci-seed-api-key.sh')
FROM k;

COMMIT;
SQL

# Assertions, because every row above is silent when it is missing: the stack
# boots, the key authenticates, and the failure surfaces two layers away as a
# 403 or a refused completion.
assert() { # assert <description> <expected> <sql>
  local got
  got="$(psql --no-psqlrc -qtAX -v ON_ERROR_STOP=1 -c "$3")"
  if [ "$got" != "$2" ]; then
    echo "::error::seed check failed: $1 (expected '$2', got '$got')" >&2
    exit 1
  fi
}

assert "the key resolves to exactly one account" 1 \
  "SELECT count(*) FROM public.api_keys WHERE token_hash = '$token_hash' AND status = 'active'"
assert "the key's account maps to a tenant" 1 \
  "SELECT count(*) FROM public.api_keys k
     JOIN public.tenant_billing_accounts m ON m.account_id = k.account_id
    WHERE k.token_hash = '$token_hash'"
assert "the key may invoke every alias" t \
  "SELECT p.allow_all_models FROM public.api_key_policies p
     JOIN public.api_keys k ON k.id = p.api_key_id
    WHERE k.token_hash = '$token_hash'"
assert "the account has a positive credit balance" t \
  "SELECT coalesce(sum(l.credits_delta), 0) > 0 FROM public.credit_ledger_entries l
     JOIN public.api_keys k ON k.account_id = l.account_id
    WHERE k.token_hash = '$token_hash'"

echo "seeded tenant, account, api key, policy and credit grant" >&2
echo "HIVE_API_KEY=$raw_secret"
