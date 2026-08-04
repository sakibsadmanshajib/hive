# Issue #717: tenant mapping for the demo box OWUI shim account

Date: 2026-08-04. Target: the demo box (`api-hive.scubed.co` = edge-api,
`chat-hive.scubed.co` = Open WebUI). All database work went through the
transaction-mode pooler on port 6543. No service on the box was restarted,
recreated, rebuilt, stopped or deployed, no file on the box was edited, and no
API key was revoked, disabled, deleted or rotated.

## Constraint found

`public.tenant_billing_accounts` is strictly one to one in both directions,
confirmed live from `pg_constraint`:

```
tenant_billing_accounts_pkey            PRIMARY KEY (tenant_id)
tenant_billing_accounts_account_id_key  UNIQUE (account_id)
tenant_billing_accounts_tenant_id_fkey  FOREIGN KEY (tenant_id)  REFERENCES tenants(id)  ON DELETE CASCADE
tenant_billing_accounts_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE RESTRICT
```

A tenant bills to exactly one account and an account funds exactly one tenant.

## Tenant decision: the shim account gets its own tenant

Account `hive-demo-owui-shim` (`feb1e8e2…b598`) and account `hive-demo-owner`
(`d18c9024…7d2f`) share one owner user. `hive-demo-owner` is already mapped to
tenant `hive-demo` (`7d9cec95…3afa`).

1. Sharing tenant `hive-demo` is structurally impossible. `tenant_id` is the
   primary key, so that tenant already holds its one row. "Sharing" could only
   mean re-pointing that row's `account_id` at the shim account, which would
   retroactively re-attribute tenant `hive-demo`'s usage and credit history to a
   throwaway shim credential and would simultaneously unmap the owner's real
   account, breaking the owner's own keys. `20260728_01` and `20260801_10` both
   reason explicitly against re-pointing an existing mapping because the ledger
   is append only.
2. A separate tenant moves no money. The ledger is keyed on `account_id`, and an
   API-key principal carries its own account (`metering.resolvePrecedence` rule
   4, `api_key_default`, never re-resolves a billing account). Shim-originated
   embeddings and text-to-speech already debit the shim account and continue to.
   Per-user chat attribution travels separately through the `hive_jwt_forward`
   filter and the OWUI unwrap middleware, so it is untouched.
3. Entitlement is identical either way today. `AliasVisibleToTenant` entitles
   `public` and `preview` aliases when a tenant has no
   `tenant_model_visibility` row. All six aliases in the catalog are public or
   preview, tenant `hive-demo` has zero visibility rows, and the whole table
   holds exactly one row (belonging to an unrelated tenant). A fresh tenant
   therefore resolves the same six models tenant `hive-demo` resolves.

`deployment` is `HIVE_CLOUD`, matching tenant `hive-demo`. It is not inert:
`metering.resolvePrecedence` short-circuits `ENTERPRISE_EDGE` to a
`not_billable` shadow verdict, so labelling the shim Enterprise would have
quietly marked its usage unbillable.

## SQL applied, verbatim

Two idempotent inserts. No schema change. The `tenants` insert is required
because `tenant_billing_accounts.tenant_id` is a foreign key, so a single row
cannot satisfy the mapping on its own.

```sql
INSERT INTO public.tenants (slug, name, deployment)
VALUES ('hive-demo-owui-shim', 'Hive Demo OWUI Shim', 'HIVE_CLOUD')
ON CONFLICT (slug) DO NOTHING;

INSERT INTO public.tenant_billing_accounts (tenant_id, account_id)
SELECT t.id, a.id
FROM public.tenants t
JOIN public.accounts a ON a.slug = 'hive-demo-owui-shim'
WHERE t.slug = 'hive-demo-owui-shim'
  AND NOT EXISTS (
    SELECT 1 FROM public.tenant_billing_accounts x WHERE x.account_id = a.id
  )
ON CONFLICT (tenant_id) DO NOTHING;
```

Both statements reported `rowcount=1`.

Before: `hive-demo-owui-shim | feb1e8e2…b598 | (no tenant) | (no mapping)`
After: `hive-demo-owui-shim | feb1e8e2…b598 | hive-demo-owui-shim | 452ee4a1-027e-4c75-80f4-d1987025d877 | 2026-08-04 20:42:13Z`

`apps/control-plane/cmd/backfill-tenants` was deliberately not used: its
confirm-mine check skips this account as
`tenant_already_billed_by_another_account` because its owner's sibling account
is already mapped.

## Scope after the fix

Exactly one account in the whole database still holds an active API key with no
tenant mapping: `owui-e2e-shim` (`c342cfaa…4074`), the CI nightly's own shim
account, minted 2026-08-04 19:48Z. Nothing belonging to the demo box remains
unmapped.

## Verification obtained

- Box edge-api is healthy and its catalog is populated.
  `GET https://api-hive.scubed.co/catalog/models` (public, no auth) returns
  HTTP 200 with six aliases: `hive-auto`, `hive-default`,
  `hive-embedding-default`, `hive-fast`, `hive-stt`, `hive-tts`.
- Box edge-api serves the tenant-filtered list on the JWT branch.
  `GET https://api-hive.scubed.co/v1/models` with a Supabase session token
  returns HTTP 200 with the same six model ids.
- `POST https://api-hive.scubed.co/v1/embeddings` with no credential returns
  HTTP 401 `missing bearer`, so the route exists and is reachable on the box.

## Verification NOT obtained, and why

`GET /v1/models` and `POST /v1/embeddings` on the box with the shim key itself
could not be executed, and the model picker is still empty in the screenshot.

- No credential on the shim account is available here. The account holds exactly
  one key, `…6_gDjQ`, whose value lives only in the box's `.env`, and
  `api_keys.token_hash` is a hash. The two shim-shaped keys in the local `.env`
  are stale: both return HTTP 401 from the box.
- No shell on the box. The LAN address is unroutable from this machine and the
  Cloudflare Access token cached for `ssh-hive.scubed.co` expired 2026-08-02, so
  `cloudflared access ssh` requires an interactive browser login that is not
  available. The only workflow that runs on the box's self-hosted runner
  recreates the stack, which is out of scope here.
- Minting a second key on the shim account would have produced a direct answer,
  but it would leave a permanent extra live credential that this task is not
  permitted to revoke, so it was not done.
- The only Open WebUI account with a known password is `qa-tester@hive.test`,
  which Open WebUI provisions with role `user`, not `admin`. On this box Open
  WebUI's own model registry is empty, and for a non-admin user Open WebUI
  filters its model list down to models present in that registry. That filter
  alone is sufficient to produce `count=0` no matter what edge-api returned, so
  the empty picker in the screenshot does not isolate edge-api. Reading Open
  WebUI's connection config, its retrieval config, or its per-connection model
  list all require an admin session.

## Separate defect found while probing

Open WebUI's document-RAG embedding path on the box fails with an edge-api
`model_not_found` shape rather than a provisioning error:

```
"error": "404, message='Not Found', url='http://edge-api:8080/v1/embeddings'"
```

`account_not_provisioned` is emitted by `Orchestrator.selectRoute` before any
model lookup, so a 404 here points at the embedding model name Open WebUI asks
for rather than at the tenant mapping. Worth its own issue.

## Pooler hazard worth recording

The first write attempt failed with `ReadOnlySqlTransaction: cannot execute
INSERT in a read-only transaction` even though the database accepts writes and
`default_transaction_read_only` is `off`. Cause: psycopg2's
`set_session(readonly=True)`, used by the read-only inspection scripts that ran
first, emits `SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY`. Port 6543
is a transaction-mode pooler, so that setting stays on the pooled server
connection and leaks to whichever client is handed it next. A temp table created
on one "fresh" connection was visible from the next, which demonstrates the same
leak directly. Writing `SET SESSION CHARACTERISTICS AS TRANSACTION READ WRITE`
before the insert cleared it. This is the same signature as the 2026-08-01
"reads succeed, writes refused" incident.

## Residue

One probe file was uploaded to Open WebUI to exercise the embedding path and was
deleted afterwards (`DELETE /api/v1/files/{id}` returned 200). Signing in
created an Open WebUI user row for `qa-tester@hive.test`, which was left in
place.
