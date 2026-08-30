# Visual proof: self-hosted SearXNG backing chat web search (issue #298)

Captured 2026-08-30 against the searxng service and `deploy/searxng/settings.yml`
added by this PR, run through the real `docker compose` wiring (not a
hand-typed `docker run`), in this PR's own worktree.

## Scope of what this proves, and what it does not

This is component-level proof: the new SearXNG retrieval backend, live,
reachable exactly the way `deploy/docker/docker-compose.yml` and
`deploy/searxng/settings.yml` configure it in this PR, answering a real query
with real multi-engine results (`bing` and `brave` both visibly attributed
under individual results in the screenshot).

It is **not** a screenshot of the chat surface itself (Open WebUI showing a
search-backed answer). This builder worktree carries no Supabase, S3 or LLM
provider credentials (`.env` does not exist here; see `CLAUDE.md` "1.
Environment" for what a working stack needs), so no end-to-end chat session
with a real model could be brought up to search-and-answer against. Standing
that up needs either real credentials in a builder sandbox or capture against
the actually deployed demo box, which per `.claude/rules/orchestrator.md`
stage 10 happens after merge, as an orchestrator-owned step, not a
pre-merge builder one. This gap is called out explicitly in the PR body
rather than papered over with a fabricated chat screenshot.

## Where this ran

`hive-agent-a7ab50cd50799a3eb-2e1382eb-searxng-1`, the `searxng` service from
this branch's `deploy/docker/docker-compose.yml`, brought up with
`docker compose --profile local up -d searxng` from this worktree's own
isolated compose project (`scripts/set-compose-project-name.sh`, issue
#1242), against the image and settings.yml this PR adds. No other service,
no other worktree's containers, and no shared checkout were touched. The
container reported `healthy` on its own committed healthcheck
(`wget --spider http://127.0.0.1:8080/healthz`) before any query was run
against it.

## Captures

| File | URL | What it shows |
| --- | --- | --- |
| `searxng-01-html-search-live.png` | `http://127.0.0.1:8888/search?q=hive+api+gateway+bangladesh` | SearXNG's own HTML result page, real query, real results, `bing`/`brave` engine attribution visible per result, response time shown in-page. |

## Data and measurements

Full raw output (JSON-API query, the 5-query engine-availability probe, the
before/after DuckDuckGo-vs-SearXNG comparison, and the page-fetch latency
measurement backing `WEB_SEARCH_RESULT_COUNT=5`) is in `capture.log` in this
same directory, not summarized away here. No secrets, tokens or credentials
appear in it; the only non-public value referenced is the compose dev
fallback `SEARXNG_SECRET=hive-local-searxng-secret`, which is not a
production value (see the `SEARXNG_SECRET` comment in `.env.example`).

## Quality-comparison caveat (read before citing the before/after numbers)

Issue #1567 (filed after this work started, live on the demo box today) has
web search's query-generation step failing 100% of the time and silently
sending the user's entire raw message as the search query instead. The
before/after comparison in `capture.log` avoids that confound on purpose: all
ten queries (5 before, 5 after) were short, hand-typed strings sent directly
to each backend, not run through Open WebUI's query generation. That isolates
engine quality from #1567. It does **not** mean #1567 is fixed, and a
chat-surface screenshot captured before #1567 lands would very plausibly show
the raw-message symptom rather than a clean short query, regardless of which
search engine sits behind it.

## Checks run on this branch

```
docker compose --profile local --profile chat config --services   -> parses, searxng listed under local+chat
docker compose --profile local up -d searxng                      -> container reaches healthy
curl http://127.0.0.1:8888/search?...&format=json                 -> 200, JSON body with results
docker compose --profile local down searxng                       -> clean teardown, no leaked resources
```

No Go or frontend test suite touches this change (compose/YAML/settings.yml
only); see the PR body for the full list of what was and was not run.
