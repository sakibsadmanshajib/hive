# Catalog alias restructure, verification evidence

PR #1007. Captured 2026-08-22.

## Read this first: what kind of evidence this is

**This is database-level evidence, not a live UI capture.** It is not a screenshot of the chat model selector, and it should not be read as one.

The change is a data change with no front-end code in it. Its user-visible surfaces are `GET /v1/models` and the Open WebUI model picker, both of which render whatever the catalog contains. A live capture of either requires the migration applied against a running stack.

That was not possible when this PR was raised, for a reason outside this change:

* The demo box has been undeployable since 2026-08-22. `deploy` depends on `migrate`, migrate fails on #994's pg_cron migration, and the pg_cron image that migration needs is built only by the `deploy` job that therefore never runs (#1002). A separate agent is breaking that deadlock.
* Sign-in on the box is separately broken by an unrelated P0, so even a deployed build could not have been driven through the picker.

A live capture is still owed once #1002 lands and the box takes this migration. Until then this file is the strongest honest evidence available, and it deliberately does not claim to be more.

## What was actually done

`catalog-after-migration.txt` is the unedited transcript of:

1. A throwaway `pgvector/pgvector:pg17` Postgres, created empty.
2. The repo's own `scripts/ci-throwaway-db.sh`, which runs the Supabase role and schema bootstrap and then `scripts/apply-migrations.sh`, the same applier the demo box runs. Not an approximation of the chain, the chain.
3. `psql` queries against the result.

All 91 migrations executed, including `20260822_02_catalog_alias_restructure.sql`. The applier's own assertion confirms it: `throwaway database ready: 91 of 91 migrations executed`. The statement-level output for this migration is in the transcript, showing the row counts each statement touched.

CI runs the same thing independently as the required check `apply-migrations.sh against a throwaway Postgres`, which passed on this commit.

## What the transcript establishes

* Every alias price matches the derivation recorded in the migration header, applied to a real database rather than asserted against SQL text.
* Every alias except `hive-embedding-default` has exactly one enabled route. See the caveat below.
* `hive-fast` survives with `visibility = 'public'` and `lifecycle = 'hidden'`, at the same price and on the same route as before, so existing conversations and API clients keep resolving it. That is the back-compat requirement, demonstrated rather than asserted.
* `route-openrouter-default` and `route-openrouter-auto` are `disabled`, alongside the previously retired `route-openrouter-fast-fallback`.
* All four new aliases are members of both the `default` and `closed` policy groups, so a default-tier API key can actually reach them. This is the gate that has silently shipped inert aliases twice before, in `20260717_01` and `20260717_02`.
* `tools_supported` is true on all six new and moved routes, rather than falling to the column default of false.

## Pre-existing condition surfaced, not introduced

The one-enabled-route query returns `hive-embedding-default` with two enabled routes. **This predates this PR and is not changed by it.** `20260801_01_alias_pricing_correction.sql` found the same thing and explicitly left it alone, recording it as "Reported for a decision rather than changed unilaterally". It is reproduced here because the query is written to check the whole table rather than only the rows this change touches, which is the point of running it that way.

## Limits of this evidence

* It does not prove what LiteLLM sends upstream. That depends on the config sync running against a live gateway, which needs the box.
* It does not exercise `GET /v1/models` end to end, so it does not prove the HTTP response shape or the per-tenant visibility filter, only the catalog rows those read from.
* It proves nothing about whether the upstream models still exist at the provider. The migration's prices come from rates fetched live on 2026-08-22, and a model decommissioned after that date would not be visible here. That failure mode is issue #965 and only a live provider query catches it.

## Reproducing

```
bash scripts/ci-throwaway-db.sh   # with PGHOST/PGPORT/PGUSER/PGDATABASE exported
```

against any empty `pgvector/pgvector:pg17`, then the queries at the end of the transcript.
