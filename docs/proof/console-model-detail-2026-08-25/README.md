# Visual proof: model detail page at `/console/catalog/[id]`

Scorecard row 6 ("Model detail page", verdict `missing`) in the vault note
`session-2026-08-25-openrouter-console-similarity-scorecard`. Captured against a
running console built from this branch, after the change.

## Where this ran

The console under test is `hive-web-console:modeldetail-proof`, built from this
worktree with `deploy/docker/Dockerfile.web-console` (which runs `next dev`, so
the served code is this branch's source, not a cached bundle).

It was attached to an already-running self-hosted stack on this machine
(compose project `hiveverify`: `supabase-db`, `supabase-auth` (GoTrue),
`control-plane`, `redis`), so the page's data came from a real control-plane
over a real Postgres, not from a fixture. Two throwaway containers were added
for the capture and removed after it:

- `modeldetail-console`: the console itself, on the stack's network.
- `modeldetail-proxy`: a `caddy:2-alpine` that gives the browser and the server
  one origin (`http://console.hive.test:13210`), routing `/auth/v1/*` to GoTrue
  and everything else to the console. It also carries the network alias
  `caddy-supabase`, because that stack's `control-plane` is configured with
  `SUPABASE_URL=http://caddy-supabase` and its own Caddy container had exited.

Nothing in the shared checkout, the shared `.env`, or any other agent's
containers was modified.

## Session

Minted with `apps/web-console/tests/e2e/support/live-auth.mjs`, the audited
one-time-token flow (`docs/live-test-auth.md`). No password was set, reset or
rotated. The account is `owner@hive-verify-952.invalid`, a local throwaway
account that already existed in that stack's database; it is not a shared
fixture account on any deployed environment.

The signed-in account address is visible in the console's sidebar footer, so it
is masked in pixels in every screenshot before upload. The linter reads text
only and cannot inspect a screenshot, so that masking is done at capture time,
not after.

## Data caveat, stated so no number here is read as production truth

This stack's database predates the 2026-08-24 credit rescale, so its catalog
rows carry pre-rescale credit values (for example `deepseek-v4-flash` input
`8,946` here against `89,460,000` in
`supabase/migrations/20260824_02_free_pool_router.sql`). The page renders
whatever the control plane returns; these are that database's real values, not
the deployed ones, and not invented ones.

The account has no `usage_events` rows, so the "Your usage" card renders its
genuine empty state. No usage figure was fabricated to make the card look
fuller.

## Captures

| File | URL | HTTP | What it shows |
| --- | --- | --- | --- |
| `md-01-catalog-rows-link.png` | `http://console.hive.test:13210/console/catalog` | 200 | The catalog list, with each model name now a link into its detail page. |
| `md-02-model-detail-deepseek-v4-flash.png` | `http://console.hive.test:13210/console/catalog/deepseek-v4-flash` | 200 | The new page. Header tiles (in/out price, cache read/write, context, pricing mode), the full pricing table including **cache read 1,790** and **cache write 0**, the named absences under Providers and uptime, and the usage card. |
| `md-03-model-detail-embedding-unknown-cache.png` | `http://console.hive.test:13210/console/catalog/hive-embedding-default` | 200 | The honesty case: this alias holds NULL cache prices on a `fixed` pricing mode, so both cache rows read **Unknown**, visibly different from the **0** on the previous capture. Zero means deliberately not charged; unknown means the catalog holds no rate. |
| `md-04-model-detail-hive-default.png` | `http://console.hive.test:13210/console/catalog/hive-default` | 200 | A second alias, cache read and cache write both a published `0`. |

## Assertions that a screenshot cannot make

A picture of a table row cannot show that the row navigates, so the capture
script clicked it:

```
catalog-row-click-navigates -> href=/console/catalog/deepseek-v4-flash
                               landed=http://console.hive.test:13210/console/catalog/deepseek-v4-flash
internal-alias-is-404       -> 404
```

The second line is `/console/catalog/openrouter-auto`, an alias whose
visibility is `internal`. The public catalog endpoint omits it, so the page is
a 404 rather than a detail page for a model this tenant cannot call.

## Browser console transcript

Full transcript for the run, unedited:

```
[info] %cDownload the React DevTools for a better development experience: https://react.dev/link/react-devtools font-weight:bold
[info] %cDownload the React DevTools for a better development experience: https://react.dev/link/react-devtools font-weight:bold
[info] %cDownload the React DevTools for a better development experience: https://react.dev/link/react-devtools font-weight:bold
[info] %cDownload the React DevTools for a better development experience: https://react.dev/link/react-devtools font-weight:bold
[info] %cDownload the React DevTools for a better development experience: https://react.dev/link/react-devtools font-weight:bold
[error] Failed to load resource: the server responded with a status of 404 (Not Found)
```

The single error is the deliberate `openrouter-auto` 404 above. No page error,
no hydration warning, no failed data fetch.

## Checks run on this branch

```
docker compose run --rm --build web-console npm run build      -> compiled, route ƒ /console/catalog/[id] present
docker compose run --rm --build web-console npm run test:unit  -> 679 passed
docker compose run --rm web-console npx vitest run lib/format/model-pricing.test.ts -> 6 passed
```

`tests/unit/ci-web-e2e-secret-free.test.ts` fails inside that image on both this
branch and `main`: it reads `.github/workflows/ci.yml`, which
`Dockerfile.web-console` does not copy into the image. Pre-existing and
unrelated to this change.
