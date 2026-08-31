# Visual proof: model picker overflow fix (issue #1601)

The chat model picker fork of Open WebUI clipped its dropdown to 256px with
no cue that more models existed below the fold, and sorted by alias_id
ascending, a database default. This captures the fix: a persistent count and
continuation footer plus alphabetical, pinned-first ordering, against the
actual changed component, vendor/open-webui/src/lib/components/chat/ModelSelector/Selector.svelte.

## Where this ran

The demo box has no live nine-model catalog reachable from this WSL2 dev
checkout, and standing up a full local control-plane requires Supabase Storage
S3 credentials this box does not hold, a documented gap at the top of .env:
there is no self-hosted data plane for this WSL2 box to point at. Rather than
fake S3 or database credentials to satisfy an unrelated boot gate, this
capture runs the real, unmodified Selector.svelte, the actual changed file,
inside the real, pinned Vite and Svelte toolchain from
vendor/open-webui/package-lock.json, built via the frontend build stage of
deploy/docker/Dockerfile.open-webui, then served with vite dev from inside
that image, so the real Tailwind pipeline and the real component compile,
not a hand rewrite.

A throwaway SvelteKit route, once at
vendor/open-webui/src/routes/proof-1601/plus-page.svelte, mounted Selector.svelte
with a fixture items array of nine model names taken from real hive dash
prefixed alias ids in supabase/migrations/20260331_01_model_catalog.sql and
its follow-on migrations. The live catalog holds nine models per the issue;
this stack has no database to read the exact nine from, so the fixture reuses
real alias ids rather than invented ones. That route file was deleted before
this diff was committed; it is not part of the shipped fix. The root layouts
own network calls, api/config, session, sockets, were mocked with Playwright
page.route, the standard mechanism, so the app boots without a backend; the
model list and the picker itself are real.

## Captures

| File | What it shows |
| --- | --- |
| proof-1601-01-closed.png | The collapsed picker button, unopened. |
| proof-1601-02-open-top.png | Opened, scrolled to top: 7 of 9 models visible, Hive Auto through Hive Medium, alphabetical, and the new footer reading 9 models, scroll for more below the clipped list, the fix core claim, a persistent, accurate cue that the list continues. |
| proof-1601-03-scrolled-bottom.png | Scrolled to the bottom of the same list: the last three models, Hive Ops and Hive Small, are reachable, confirming all nine fixture models are actually reachable, not just counted. |

## Assertions a screenshot cannot make

The capture script asserted these directly against the DOM, not just visually:

    DOM_OPTION_COUNT_AT_TOP 9        all 9 items are in the DOM, virtualized list, not just 7 rendered
    OVERFLOW_FOOTER_VISIBLE true     the count and continuation footer is actually visible, not just present
    LAST_OPTION_AFTER_SCROLL Hive Small   the 9th, alphabetically last, item is reachable by scrolling
    ALL_OPTION_TEXTS Hive Auto, Hive Default, Hive Embedding Default, Hive Fast,
      Hive Free, Hive Free (Tools), Hive Medium, Hive Ops, Hive Small
      alphabetical order end to end, replacing the old alias_id ascending default

## Browser console transcript

Full transcript for the run, unedited. The errors are the mocked out
background calls, websocket connect, Ollama version probe, timezone update,
that the real app makes on every load and that a backend would normally
answer; none of them are page errors and none affected rendering.

    [console:debug] [vite] connecting...
    [console:debug] [vite] connected.
    [console:log] Backend config: name Hive proof harness, version 0.10.2, default_locale en-US
    [console:error] Failed to load resource: net::ERR_CONNECTION_REFUSED
    [console:log] connect_error TransportError: xhr poll error, socket.io, mocked backend has no websocket
    [console:error] Failed to load resource: net::ERR_CONNECTION_REFUSED
    [console:error] Failed to update timezone: TypeError: Failed to fetch, mocked backend has no timezone endpoint
    [console:error] Failed to load resource: net::ERR_CONNECTION_REFUSED
    [console:log] connect_error TransportError: xhr poll error
    [console:error] Failed to load resource: net::ERR_CONNECTION_REFUSED
    [console:error] TypeError: Failed to fetch, Selector.svelte own Ollama version probe, mocked backend has no ollama endpoint

No page error, no hydration warning, no failed render of the component under test.

## Checks run on this branch

    sh scripts/test-owui-hive-frontend.sh
      -> 22 test files passed, 311 tests, including the new model-sort.test.ts, 100 percent stmt/branch/func coverage
      -> 14/14 lib/hive components compiled

    docker build -f deploy/docker/Dockerfile.open-webui --target frontend .
      -> real npm run test:frontend, vitest, in-place, passed
      -> real npm run build, vite build, Svelte plus Tailwind, succeeded, including
         the edited Selector.svelte, confirming it compiles in the actual product build

    docker compose --profile local --profile chat up --build
      -> full open-webui image builds clean, hive: shell present, removed surfaces absent
      -> full stack boot blocked by this WSL2 box documented missing S3/DB
         credentials, unrelated to this change, see Where this ran above
