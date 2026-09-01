# Live capture: web search query generation after PR #1614, and where the web search path stands (2026-09-01)

Captured against the deployed demo box (`chat-hive.scubed.co`, signed in
through `console-hive.scubed.co`). PR #1614 (issue #1600) merged and deployed
at 14:45 UTC on commit `cc22029a`, and its builder could not capture the
visible effect from a branch, because it needs the chat image rebuilt with the
patch plus a full authenticated stack. This is that owed capture, plus an
end to end measurement of the web search path.

Screenshot posted to PR #1614:
https://github.com/sakibsadmanshajib/hive/pull/1614#issuecomment-5499314382

## Identity and session

Two dedicated fixture identities, never a shared or demo facing account:

* `owui-e2e+proof298-1788132550@hive-e2e.invalid`, the same run scoped fixture
  the previous web search capture used (`docs/proof/websearch-searxng-live-298-2026-08-30`).
* `interaction-gate-20260808@scubed.com.bd`, a funded fixture identity, used
  once the first one turned out to carry a zero credit balance.

Both sessions were minted through the audited admin one time token flow
(`mintSession` in `apps/web-console/tests/e2e/support/live-auth.mjs`), never by
setting, resetting or rotating any account's password. The driver ran inside a
`mcr.microsoft.com/playwright:v1.62.1-noble` container attached to the stack's
compose network, which is the position `deploy-demo-box.yml`'s
`agent-workspace-coverage` job already uses: `SUPABASE_ADMIN_URL` points at the
in network listener (`http://caddy-supabase`), because the public listener
refuses `/auth/v1/admin/*` by design, while `SUPABASE_URL` stays on the public
console origin so the session cookie is named the way the deployed app reads
it.

The only URL in this run that carried a one time credential was the console
consent hop, and its `authorization_id` value is redacted in this document. The
screenshot shows `chat-hive.scubed.co/c/<chat-id>` only, so there was nothing
to mask in the pixels.

## Timeline, including a deploy that moved under the capture

| UTC | What |
| --- | --- |
| 14:45 | PR #1614 (issue #1600) deployed, commit `cc22029a` |
| 15:28 | Run A, on `cc22029a`: #1614 present, #1617 not yet merged |
| 15:35 | Run B, discarded: `hive-open-webui-1` was recreated at 15:35:58 mid run by the deploy of PR #1617 |
| 15:48 | PR #1617 (issue #1609) deployed, commit `bf97f2b8`, containers recreated |
| 15:56 | PR #1612 deployed, commit `296b9a16`, the build the final run measured |
| 19:30 | Run C, on `296b9a16`, stack stable for over three hours |

Run B is recorded and not used. It showed the raw user message as the search
query and five unrelated currency conversion pages as results, which is exactly
the #1600 symptom, but the backend it was talking to was being replaced while
it ran, so it is not evidence about either build. Naming it here rather than
dropping it, because a discarded sample that goes unmentioned is how a
contaminated result gets quoted later as a reproduction.

## Run A, on `cc22029a` (PR #1614 deployed, PR #1617 not yet merged)

Prompt: `Who won the most recent Formula 1 Grand Prix, and on what date was it held?`
Model: Deepseek V4 Flash. Chat: `https://chat-hive.scubed.co/c/130540d2-7f89-4693-8ff7-88ebd3aaa8f8`

Status history, read from the persisted chat rather than from the rendered page:

```
web_search                      "Searching the web"
web_search_queries_generated    queries = [
                                  "most recent Formula 1 Grand Prix winner 2026",
                                  "F1 2026 race results latest",
                                  "Formula 1 Grand Prix schedule September 2026" ]
web_search                      "Searched {{count}} sites", 11 urls, 13 items
queries_generated               queries = [
                                  "most recent Formula 1 Grand Prix winner 2026",
                                  "Formula 1 race results September 2026",
                                  "F1 latest race winner and date" ]
sources_retrieved               count = 3
```

Container log for the same request (`hive-open-webui-1`):

```
Fetching pages: 100%|##########| 11/11 [00:01<00:00, 10.50it/s]
save_docs_to_vector_db:1734 - generating embeddings for web-search-<collection>
save_docs_to_vector_db:1778 - embeddings generated 23 for 23 items
```

No `429` and no `Too Many Requests` anywhere in the window. The assistant then
produced no answer, for a billing reason unrelated to search: the fixture
tenant carries a zero credit balance and every alias visible to it is paid, so
the composer showed `You're out of credits`. Recorded as measured rather than
smoothed over, because the empty answer here looks like a search failure and is
not one.

Two honest limits on this run. It is a single sample, and its embedding burst
was 23 chunks, which is at the small end of what issue #1609 describes. It is
therefore not evidence that #1609 was absent on that build, only that this
particular request did not trip it.

## Run C, on `296b9a16` (PR #1614 and PR #1617 both deployed)

Same prompt and model, funded identity, stack stable since 15:48.
Chat: `https://chat-hive.scubed.co/c/a392b391-5add-4a8e-99ca-4bf95de14dd3`

```
web_search                      "Searching the web"
web_search_queries_generated    queries = [
                                  "most recent Formula 1 Grand Prix winner 2026",
                                  "F1 race results September 2026",
                                  "latest Formula 1 Grand Prix winner and date" ]
web_search                      "Searched {{count}} sites", 15 urls
queries_generated               queries = [
                                  "most recent Formula 1 Grand Prix winner 2026",
                                  "F1 race results September 2026",
                                  "latest F1 Grand Prix date and winner" ]
sources_retrieved               count = 3
```

Container log for the same request:

```
19:30:19 WARNING open_webui.routers.openai:_hive_collapse_sse_completion:1230 -
  hive: upstream answered a non-streaming request to deepseek-v4-flash with
  text/event-stream; collapsing it into a completion object. Further collapses
  in this process log at debug level.
Fetching pages: 100%|##########| 15/15 [00:05<00:00,  2.95it/s]
save_docs_to_vector_db:1734 - generating embeddings for web-search-<collection>
save_docs_to_vector_db:1778 - embeddings generated 64 for 64 items
pgvector:insert:345 - Inserted 64 items into collection 'web-search-<collection>'
```

That first line is PR #1614's own guard firing. The gateway still answers a
non streaming task request with `text/event-stream`, which is the true root
cause and now has its own issue (#1618), and the patch collapses the stream
into the completion object the caller declared, which is why query generation
returns a value at all instead of raising.

What the assistant actually said, quoted from the rendered answer with the
inline citation chips dropped and dash punctuation normalised:

> Lando Norris won the most recent Grand Prix, the 2026 Dutch Grand Prix at
> Zandvoort. Per the official 2026 calendar, the Netherlands round was held
> 21 to 23 August, with the race itself on Sunday, 23 August 2026.
>
> Note: as of this snapshot (early September 2026), the Italian GP was
> scheduled for 4 to 6 September but had not yet taken place when the source
> material was captured.

Citations rendered inline against `formula1.com` and `f1-gate.com`, with a
`3 Sources` chip under the answer. No denial of internet access, and no
`No sources found`.

## Where each of the three issues stands

| Issue | State on the build measured |
| --- | --- |
| #1600, query generation raised before its result was used | Fixed and visible. Both surviving runs show generated retrieval queries in the chips, not the raw typed question, and the collapse guard logs on every task completion. |
| #1609, sources fetched then discarded on an embedding 429 | Not reproduced, on a build that already carries PR #1617. 64 of 64 chunks embedded and inserted, three sources reached the answer, and no 429 appears in the window. Run A, on the pre #1617 build, also showed no 429, but with a 23 chunk burst, so it is a weak control rather than a counterexample. |
| #1621, search not invoked when the toggle is off | Still open, and untouched by either fix. Web search had to be enabled by hand from the integrations menu in every run here. Nothing in this capture bears on making it automatic. |

## A separate quality finding, not any of the three

Result quality from the search engine is noisy in a way the query generator is
not responsible for. Run C's queries all begin with the word "most", and five
of the fifteen URLs SearXNG returned were pages of `most.gov.bd`, the
Bangladesh Ministry of Science and Technology, which then failed to fetch on a
TLS certificate verification error. Run B, on its raw message query, came back
with Korean won to Vietnamese dong currency conversion pages. The answer in Run
C survived this because retrieval ranked the three formula1.com and f1-gate.com
chunks above the noise, but the engine mix is returning keyword matches on
single common words. That belongs with the SearXNG engine list work (issues
#1576 and #1585), not with #1600 or #1609.

## Reproduction

The driver, the container invocation and the raw driver log are not committed:
they carry the service role key path and are throwaway. What matters for a
rerun is the shape, which is the one `deploy-demo-box.yml`'s
`agent-workspace-coverage` job already documents: run Playwright in a container
on the stack network, mint with `SUPABASE_ADMIN_URL` on the internal listener
and `SUPABASE_URL` on the public console origin, enable web search from
`#integration-menu-button` then `button[aria-label='Enable Web Search']`, send,
and read the persisted chat's `statusHistory` rather than trusting the rendered
page.
