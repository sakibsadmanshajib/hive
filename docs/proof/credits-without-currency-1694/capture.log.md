# Capture log: credit balances render as Hive credits, currency only on invoices (issue #1694)

Captured 2026-09-02 against containers built from `fix/1694-credits-without-currency`
at commit `ff63e8067`. Two stacks, because the change spans two separate front
end builds that share no module.

No credential appears in any URL in this capture. The chat account is created
through the front end's own signup API with a per-run generated password; the
session token is set as a cookie by the capture script and never rides in a
URL, so nothing here needs redaction on that count. The password itself is
never printed.

## Console (Next.js, `Dockerfile.web-console` built from this branch)

```
$ docker build -f deploy/docker/Dockerfile.web-console -t hive-proof-1694-console:local .
$ docker run -d --name proof1694-console -p 127.0.0.1:3694:3000 \
    -e NEXT_PUBLIC_SUPABASE_URL=http://127.0.0.1:9 \
    -e NEXT_PUBLIC_SUPABASE_ANON_KEY=proof-anon-not-a-real-key \
    -e CONTROL_PLANE_BASE_URL=http://127.0.0.1:9 \
    hive-proof-1694-console:local
$ docker run --rm --network container:proof1694-console ... \
    -e TARGET_URL=http://localhost:3000/proof-1694 \
    mcr.microsoft.com/playwright:v1.55.0-noble node capture-console.mjs
```

Real Supabase Auth is unreachable from this dev box (the self-hosted data
plane on the demo box publishes no public port and the shared `.env` names
in-stack hostnames), so the authenticated `/console/*` routes cannot render:
their layout calls `getViewer()`. Captured instead through a throwaway route,
`app/proof-1694/page.tsx`, never committed and deleted after the capture, that
mounts the actual `CreditBalance`, `ApiKeyList`, `AnalyticsOverviewSection` and
`ModelCatalogTable` components this change edits, with fixture rows. Same
compiled component code the authenticated pages render; only the
session-fetching wrapper around it differs. Same approach and same reason as
`docs/proof/api-key-credit-cap-2026-08-28/README.md`.

Script output:

```
console:info: %cDownload the React DevTools for a better development experience: https://react.dev/link/react-devtools font-weight:bold
console:log: [HMR] connected
console:log: [Fast Refresh] rebuilding
console:log: [Fast Refresh] done in 250ms
captured 1694-01-credit-balance.png for section Credit balance
captured 1694-02-api-keys.png for section API keys
captured 1694-03-analytics.png for section Analytics
captured 1694-04-model-catalog.png for section Model catalog
currency marks found in rendered main: none
credit figures rendered: 99,996,364,207 credits | 100,000,000,000 credits | 3,635,793 credits | 5,000,000,000 credits | 5,000,000,000 credits/mo | 12,000,000,000 credits | 360,000,000 credits | 10,000,000,000 credits/mo | 3,000,000,000 credits | 136,363,636 credits | 2,400,000,000 credits | 600,000,000 credits
url: http://localhost:3000/proof-1694
```

`currency marks found in rendered main: none` is the assertion that matters:
the script reads the rendered text of the whole harness and matches it against
`/[$৳€£¥]|USD|BDT/`.

What each image shows:

- `1694-01-credit-balance.png`: the balance card. "99,996,364,207 credits" as
  the headline, "Posted 100,000,000,000 credits, Reserved 3,635,793 credits"
  beneath it, and no third line. The deleted third line used to read
  "99,996,364,207 credits, at 1,000,000,000 credits per $1.00".
- `1694-02-api-keys.png`: the budget column across five keys, in one table.
  `prod-server` is the partial case, "1,000,000,000 of 5,000,000,000 credits
  total, 20.0%", with the bar a fifth full. `batch-worker` is the over limit
  case, "7,500,000,000 of 5,000,000,000 credits/mo, Limit reached, 150.0%",
  with the fill clamped to the track and the true percentage stated beside it,
  plus "12,000,000,000 credits lifetime" underneath. `zero-cap` is the
  non-positive cap, exhausted at 100.0%. `older-control-plane` is a cap with no
  enforced counter: two figures, no bar, no "of" between them.
  `uncapped` reads "360,000,000 credits of Unlimited".
- `1694-03-analytics.png`: the overview tiles. Total spend "3,000,000,000
  credits", Blended price / 1M "136,363,636 credits", and the Top API keys card
  ranking "2,400,000,000 credits" and "600,000,000 credits".
- `1694-04-model-catalog.png`: the catalog price columns, headed "Input credits
  / 1M", "Output credits / 1M", "Cache read credits / 1M", "Cache write credits
  / 1M", carrying 89,460,000 / 178,920,000 / 2,982,000 / 0.

## Chat front end (`Dockerfile.open-webui` built from this branch)

```
$ docker build -f deploy/docker/Dockerfile.open-webui -t hive-owui:proof-1694 .
$ docker network create proof1694
$ docker run -d --name proof1694-stub --network proof1694 \
    -v "$PWD/docs/proof/credits-without-currency-1694:/work" -w /work \
    python:3.12-alpine python stub.py 8000
$ docker run -d --name proof1694-owui --network proof1694 -p 127.0.0.1:3695:8080 \
    -e WEBUI_SECRET_KEY=proof1694-local-secret -e ENABLE_SIGNUP=true \
    -e HIVE_CONTROL_PLANE_URL=http://proof1694-stub:8000 \
    -e CONTROL_PLANE_INTERNAL_TOKEN=proof1694-internal-token \
    -e HIVE_CONSOLE_BILLING_URL=https://console-hive.scubed.co/console/billing \
    hive-owui:proof-1694
$ docker run --rm --network container:proof1694-owui ... \
    -e OWUI_URL=http://localhost:8080 -e PROOF_PASSWORD="$(openssl rand -hex 12)" \
    mcr.microsoft.com/playwright:v1.55.0-noble node capture-chat.mjs
```

`stub.py` stands in for control-plane's internal chat balance route, which is
what `deploy/docker/owui-patches/hive_credits.py` calls behind an internal
token. It returns one fixed balance, so the capture exercises the real Svelte
components over a real HTTP round trip without standing up a ledger. The two
containers share a network; the browser shares the chat container's network
namespace so the front end is reached on `http://localhost:8080`, a secure
origin as far as the browser is concerned.

Script output:

```
signup status 200, session token received (redacted)
banner text: You've used 340,000,000 credits today · 9,996,364,207 credits remaining
usage pane text: Usage | Organization credit balance | 9,996,364,207 credits | Organization usage today | 340,000,000 credits | Top up | Last updated: 2:34:04 PM | Refresh
currency marks in rendered chat surfaces: none
url: http://localhost:8080
```

What each image shows:

- `1694-05-chat-credits-banner.png`: the composer banner, reading "You've used
  340,000,000 credits today, 9,996,364,207 credits remaining". It used to read
  "$0.34" and "$9.99".
- `1694-06-chat-settings-usage.png`: Settings, Usage tab. "Organization credit
  balance 9,996,364,207 credits" and "Organization usage today 340,000,000
  credits", with the Top up link the deployment configures.

## Reproducing

`capture-console.mjs`, `capture-chat.mjs` and `stub.py` are committed here. The
console harness route is not: it is a throwaway that mounts committed
components with fixture props, and leaving it in the tree would ship an
unauthenticated page that renders a balance.
