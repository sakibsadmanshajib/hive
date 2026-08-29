# Visual proof, console dead-end routes (PR #1394, issues #543 and #762)

Captured 2026-08-29 against a full stack built from `fix/console-dead-end-routes-543`.

## Substrate, stated plainly

Not the deployed demo box. The fix that unblocks these pages is in control-plane
(`apps/control-plane/internal/auth/client.go`), and `console-hive.scubed.co` runs
already-merged code, so proving it there before merge would be circular. The
stack was therefore stood up locally following `ci.yml`'s `Web E2E (full stack)`
job recipe step for step:

| Layer | What ran |
| --- | --- |
| Database | throwaway `pgvector/pgvector:pg17`, full `supabase/migrations` chain applied, including `20260829_02_feature_gate_enforcement_site.sql` |
| Auth | GoTrue and PostgREST behind one nginx origin, via `scripts/ci-supabase-stack.sh` |
| control-plane | built from this branch, `docker compose --profile local up --build control-plane`, reported healthy |
| web console | built from this branch, `npm run build` then `npm start` |
| Identity | the run-scoped fixture owner seeded by `tests/e2e/support/e2e-auth-fixtures.mjs` with a per-run random password, in a throwaway GoTrue destroyed with the stack |

Verified the migration actually applied before capturing, rather than assuming
it: `SELECT key, enforcement_site FROM public.feature_gate_keys` returned the
three enforcement sites and NULL for the other twenty two.

## What the capture shows

* `/console/feature-gates` renders 25 gates with 15 switches and **exactly 22**
  "Not enforced yet" notices. `ENABLE_RAG`, `ENABLE_VOICE` and `ENABLE_COWORK`
  carry no notice, which is the whole claim: the three that are mounted in
  edge-api are unmarked and every one of the other twenty two says so. Before
  this change the same page rendered "Could not load feature gates".
* `/console/marketplace` renders its honest empty state instead of "Could not
  load the marketplace catalog". The catalog is genuinely empty, since no
  migration seeds a row.
* `/console/api-keys` shows one Limits link per active key.
* The limits page was reached **by clicking that link**, not by typing a URL,
  and renders inside `ConsoleShell`: 11 navigation links and an "All API keys"
  back link where before there were none of either.
* `/console/billing` shows 14 console links, the 11 in the rail plus the three
  new Spend controls links to alerts, budget and invoices.

## Horizontal overflow

Measured with `window.scrollTo(9999, 0)` then `window.scrollX`, because
`documentElement.scrollWidth` lies. Every page read `0`, including
`/console/api-keys`, whose table gained a column in this change.

## Credentials

None appear here. The capture log was scanned for both fixture passwords, the
invitation token, the anon key and the service-role key by exact value, plus for
any `hk_` or JWT-shaped string; all absent. The fixture address is an address,
not a credential, and belongs to a throwaway account in a database that no
longer exists.
