# Visual and money-path proof: per-key spend display and enforced credit cap

Two things are proven separately here: the enforcement itself (edge-api,
against a real running binary and a real Postgres), and the console UI that
now exposes it (screenshot, against the actual compiled component code).

## Why the setup differs from a normal local run

The shared dev `.env`'s `SUPABASE_DB_URL`/`SUPABASE_URL` point at a Supabase
Cloud project that has since been deleted (self-hosted cutover, see
`.wolf/decisions.md`); the pooler answers `tenant/user ... not found` for the
DB and DNS does not resolve for the Auth REST API. Same blocker already
documented in `docs/proof/settings-declutter-p0-2026-08-23/README.md` for a
different PR. Worked around the same way: a throwaway local
`pgvector/pgvector:pg17` Postgres, migrated with the exact chain
`scripts/ci-throwaway-db.sh`/CI use (`.github/ci/test-db-bootstrap.sql` +
every file under `supabase/migrations/`), running in an isolated compose
project (`hive-a49158c`) with unique host ports so nothing touched the shared
checkout, `.env`, or any other agent's running stack. `SUPABASE_JWT_ISSUER`/
`AUDIENCE`/`JWKS_URL` were left empty for edge-api only (a documented,
sanctioned skip path in `apps/edge-api/cmd/server/main.go`'s
`loadJWTAuthEnv`, "intended... for single-tenant API-key-only deployments
where JWT validation is moot") since this capture never needed a Supabase
session, only the raw-API-key path the new cap actually gates.

The one thing this setup could not reach: the shared `litellm` container has
a fixed name so control-plane's Docker-socket restart API can find it after a
config sync, which meant a full successful completion (needing a sync that
restarts `litellm` by that fixed name) risked touching a peer agent's shared
LiteLLM. Not attempted. What was captured instead is a differential proof
that isolates the budget gate specifically, at the pre-dispatch stage where
it fires and no provider is ever reached, so this limitation costs nothing
material to the money-path claim.

## Live API-level proof (edge-api + control-plane, both built from this
## branch, against the throwaway Postgres)

Two seeded API keys, same account, same $10 real ledger balance (a `grant`
entry so the account-level balance check never confounds the per-key check
under test):

- `verify-capped`: `api_key_policies.budget_kind='lifetime'`,
  `budget_limit_credits=1` (set through the same code path this PR wires the
  console to, `POST .../api-keys/{id}/policy`).
- `verify-uncapped`: `budget_kind='none'`.

Same request against both, `model: hive-free` (the zero-budget-cost free
pool alias, D-048):

```
$ curl -o /tmp/resp-capped.json -w "HTTP_STATUS:%{http_code}\n" http://localhost:18092/v1/chat/completions \
    -H "Authorization: Bearer REDACTED_CAPPED_TEST_KEY" \
    -H "Content-Type: application/json" \
    -d '{"model":"hive-free","messages":[{"role":"user","content":"hi"}],"max_tokens":5}'
HTTP_STATUS:429
{"error":{"message":"You exceeded your current quota, please check your plan and billing details.","type":"insufficient_quota","param":null,"code":"insufficient_quota"}}

$ curl -o /tmp/resp-uncapped.json -w "HTTP_STATUS:%{http_code}\n" http://localhost:18092/v1/chat/completions \
    -H "Authorization: Bearer REDACTED_UNCAPPED_TEST_KEY" \
    -H "Content-Type: application/json" \
    -d '{"model":"hive-free","messages":[{"role":"user","content":"hi"}],"max_tokens":5}'
HTTP_STATUS:400
{"error":{"message":"/chat/completions: Invalid model name passed in model=hive-free. Call `/v1/models` to view available models for your key.","type":"api_error","param":null,"code":"upstream_error"}}
```

The capped key is rejected by `authz.CheckAccess`'s budget gate before any
reservation or dispatch. The uncapped key passes that same gate and fails
later, at LiteLLM dispatch (this throwaway litellm instance never received a
catalog sync, so it has no `hive-free` deployment registered) -- a different
failure mode at a later stage, which is exactly the differential the negative
control needs: the same request, only the key's budget policy differs, and
only the capped one is stopped by budget specifically.

Ledger state after both requests (`credit_ledger_entries`, real rows, this
branch's code, real Postgres):

```
              account_id              |     entry_type      | credits_delta
--------------------------------------+---------------------+---------------
 <account>                            | grant               |   10000000000
 <account>                            | reservation_hold    |    -100000000   <- verify-uncapped only
 <account>                            | reservation_release |     100000000   <- released, net zero
(3 rows)
```

Zero `reservation_hold`/`reservation_release` rows exist for `verify-capped`:
its request never reached `accounting.CreateReservation` at all, so it never
touched the ledger, positive or negative -- consistent with the pre-flight
rejection above. `verify-uncapped`'s hold and release net to zero, matching
the fail-closed rule (D-034): a request that never completed against a real
provider bills nothing, whether or not a cap applied.

`api_key_budget_windows` stayed empty for both keys through this whole run
(0 rows), confirming in real data the reason `GetLifetimeSpend` (this PR's
new query) reads `api_key_usage_rollups` instead: `ApplyReservationDelta`
returns early for `budget_kind='none'` and never writes that table, so it
cannot report spend for the common case of a key with no cap configured.
Inserted two synthetic `api_key_usage_rollups` rows for `verify-capped`
across two different `model_alias` values (700 + 300 credits) and ran the
exact SQL text `GetLifetimeSpend` executes:

```
SELECT COALESCE(SUM(consumed_credits), 0)
FROM public.api_key_usage_rollups
WHERE api_key_id = '<verify-capped>' AND window_kind = 'lifetime';
 coalesce
----------
     1000
```

Confirms the sum-across-model-aliases behavior the query exists for (a key's
lifetime spend is the total across every model it has ever used, not one
model's rollup row).

## Console screenshot

`hive-web-console:ci`, built from this branch, run standalone
(`npm run dev`, isolated container, no shared ports). Real Supabase Auth is
unreachable per the blocker above, so the authenticated `/console/api-keys`
route (which calls `getViewer()`) cannot render; captured a throwaway,
never-committed route (`app/verify-proof-scratch/page.tsx`, deleted before
push) that mounts the actual `ApiKeyCreateForm` and `ApiKeyList` components
this PR changes, with fixture rows mirroring the live run above
(`verify-capped` at 1000 credits spend / 1 credit lifetime cap,
`verify-uncapped` at 0 spend / no cap, plus a third `monthly` example not
covered by the API run). Same compiled component code the authenticated page
renders; only the account/session-fetching wrapper around it differs.

- `keyspend-console.png`: New Key modal showing the added "Credit limit
  (USD)" and "Reset limit every..." fields alongside the existing
  Nickname/Expires, and the keys table showing the new Spend and Credit
  limit columns (`$0.000001` / `$0.000000001 total`, `$0` / `Unlimited`,
  `$2.40` / `$10.00/mo`) -- every credit value rendered through
  `formatUsdFromCredits`, never a raw integer.

No credential-in-URL flow is present in this capture (a local API key value,
not a magic-link/OAuth/invite token), so the redaction requirement for those
flows does not apply here; the two test API key secrets are redacted in this
log regardless, on the same standing rule.
