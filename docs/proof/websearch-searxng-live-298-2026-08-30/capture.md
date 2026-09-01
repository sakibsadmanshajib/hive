# Live chat-surface capture: SearXNG web search (issue #298, PR #1570)

Captured 2026-08-30 against the deployed demo box (chat-hive.scubed.co /
console-hive.scubed.co), after PR #1570 merged and deploy run 33341365483
succeeded. This is the follow-up capture PR #1570's own description asked
for: a screenshot from the actual chat surface, not SearXNG's own component
UI, taken post-deploy against the running stack.

## Identity and session

A dedicated, run-scoped fixture identity was minted for this capture, never
a shared account: `owui-e2e+proof298-1788132550@hive-e2e.invalid`,
provisioned via the sanctioned `scripts/seed-owui-e2e-user.py` (its own
tenant/billing mapping, `OWUI_E2E_RUN_KEY=proof298-<timestamp>`). The session
itself was minted through the audited one-time-token flow
(`apps/web-console/tests/e2e/support/live-auth.mjs`'s `mintSession`), never
by setting, resetting, or rotating any account's password.

One real finding worth recording for future captures: `sessionCookies()`
names the session cookie from the `supabaseUrl` argument it is given
(`sb-<host-derived-ref>-auth-token`). Minting with the internal
`SUPABASE_URL=http://caddy-supabase` (required for the admin `generate_link`
call, which the public listener refuses by design) produces a cookie named
`sb-caddy-supabase-auth-token`. The deployed web-console middleware builds
its own `createServerClient` from `NEXT_PUBLIC_SUPABASE_URL`
(`https://console-hive.scubed.co`), which derives the different name
`sb-console-hive-auth-token`. A cookie minted and named against the internal
URL is therefore silently invisible to the deployed app's own auth check
(treated as signed out, redirected to `/auth/sign-in`), even though the
token itself is valid (confirmed directly against
`https://console-hive.scubed.co/auth/v1/user`, 200). The fix used here: call
`mintSession()` against the internal URL (for the network calls) and
`sessionCookies()` separately against the public URL (for the name/domain),
rather than the CLI's single-URL shortcut. Chat sign-in itself goes through
an OAuth-style consent redirect from `chat-hive.scubed.co` to
`console-hive.scubed.co/oauth/consent`, approved once per session.

No credential ever appeared in a navigated URL in this run (cookie-based
session injection, not a magic-link redirect), so there is nothing to redact
in the screenshots below.

## What was captured

1. Signed in on the real chat surface as the dedicated fixture identity
   (greeting read "Good evening, Owui E2e", confirmed also via
   `GET /api/v1/auths/` returning that account's own email, not any shared
   account).
2. Selected the `Hive Free` model (the fixture tenant carried a zero
   balance; `Hive Free` is a hive-free-priced alias that does not require
   paid credits) and enabled "Web Search" from the Integrations menu.
3. Sent: "What is today's date, and what is one current news headline from
   today?", a question that plainly requires current information.
4. Expanded the response's status history, which showed a real search
   attempt against SearXNG, and a real failure:
   `An error occurred while searching the web`.

Screenshots (posted to PR #1570 via `scripts/post-pr-visual-proof.sh`):
- Chat surface signed in as the fixture identity, web search enabled.
- The search-error response, with the page's live URL overlaid at the top
  (chat-hive.scubed.co/c/<chat-id>, no query string, nothing to redact).

## Root cause of the search failure (new finding, not #1567)

Two stacked failures appeared in `hive-open-webui-1`'s own log for this
request, read live via `docker logs` on the demo box:

1. `chat_web_search_handler:1359 - Query generation failed` — this is
   issue #1567, already known and out of scope for this capture: query
   generation fails and the surface falls back to sending the user's raw
   message as the search query (visible in the expanded status history:
   the query shown is this run's literal question, unshortened).
2. `retrieval.py:2211 - Exception: No SEARXNG_QUERY_URL found in
   environment variables` — **not** #1567, and not something PR #1570 got
   wrong in the compose file. The running `hive-open-webui-1` container's
   own process environment does carry the correct value
   (`docker exec hive-open-webui-1 env`: `SEARXNG_QUERY_URL=http://searxng:8080/search`,
   `WEB_SEARCH_ENGINE=searxng`), matching exactly what PR #1570 added to
   `deploy/docker/docker-compose.yml`. Open WebUI reads this setting from
   its own persisted config store rather than `os.environ` at request time,
   and that store appears not to have picked up the new value on this
   deploy: the same "seeded once on a volume's first boot, a later
   container recreate keeps the old value" class of gap already documented
   in this repo for `OWUI_SHIM_KEY` (`scripts/seed-owui-e2e-user.py`'s own
   module docstring, `sync_owui_config`). SearXNG itself
   (`hive-searxng-1`) was independently confirmed healthy and running on the
   box throughout this capture.

This capture therefore does **not** show the SearXNG engine swap working
end to end on the live chat surface today: it shows the toggle, the
composer, and a real (not simulated) search attempt reaching the point of
calling out to SearXNG, failing there for a persisted-config reason
distinct from the code in PR #1570. Flagged to the team for the config-sync
fix; not fixed here, since the fix belongs with whoever owns the live
config-sync work already in flight for this issue (out of scope for a
proof-capture pass, and this session had several other agents already
working the SearXNG/OWUI config surface concurrently).

Answer quality is therefore not assessed here at all (the search never
returned to the model), which is a strictly worse and more fundamental gap
than the "poor answer quality from a raw-message query" #1567 already
names.
