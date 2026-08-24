# Web search in chat, demo enablement: live capture log

Branch: feat/web-search-chat
Date: 2026-08-24
Capture environment: throwaway compose stack on the development box
(websearch-stub-llm + websearch-open-webui, project `websearch`), image built
from this branch's tree (`hive-open-webui:websearch-proof`).

## Enablement mechanism, resolved-config evidence

`docker compose config` against the exact demo flag set
(`-f docker-compose.yml -f docker-compose.override.yml -f docker-compose.enterprise.yml --profile local --profile chat --profile monitoring --profile selfhost`), run from deploy/docker of this worktree:

Without the workflow env values:

```
BYPASS_WEB_SEARCH_EMBEDDING_AND_RETRIEVAL: "false"
BYPASS_WEB_SEARCH_WEB_LOADER: "false"
ENABLE_WEB_SEARCH: "false"
WEB_SEARCH_ENGINE: ""
```

With the workflow env values exported:

```
BYPASS_WEB_SEARCH_EMBEDDING_AND_RETRIEVAL: "true"
BYPASS_WEB_SEARCH_WEB_LOADER: "true"
ENABLE_WEB_SEARCH: "true"
WEB_SEARCH_ENGINE: duckduckgo
```

## Live stack checks

All commands run against http://localhost:13104 after `docker compose up -d`.

### 1. Container env

```
$ docker exec websearch-open-webui printenv | grep WEB_SEARCH
ENABLE_WEB_SEARCH=true
WEB_SEARCH_ENGINE=duckduckgo
BYPASS_WEB_SEARCH_WEB_LOADER=true
BYPASS_WEB_SEARCH_EMBEDDING_AND_RETRIEVAL=true
```

### 2. Config API

```
$ curl -s localhost:13104/api/config | python3 -m json.tool | grep -A2 web_search
    "enable_web_search": true,
    "enable_web_search_confirmation": false,
```

### 3. Live DuckDuckGo retrieval

```
$ TOKEN=...  # admin session token minted via POST /api/v1/auths/signup
$ curl -s localhost:13104/api/v1/retrieval/process/web/search \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"queries":["latest AI news August 2026"],"collection_name":""}' | head -c 600

{"status":true,"collection_name":null,"filenames":[
 "https://kraviona.com/blog/latest-ai-news-august-2026",
 "https://imfounder.com/science-tech/ai/ai-updates-august-2026-openai-astra-deepmind/",
 "https://local-ai-zone.github.io/blog/ai-updates-august-2026.html"],
 "items":[{"link":"https://kraviona.com/blog/latest-ai-news-august-2026",
   "title":"AI News August 2026: OpenAI Astra, ChatGPT 1B Users & Price Cuts",
   "snippet":"Latest AI news August 2026: OpenAI Astra solves 10 math problems,
   ChatGPT crosses 1B users, GPT-5.6 Luna drops 80% in price. Updated August 14."},
 ...]}
```

Container log confirms the engine path:
```
| httpx._client:_send_single_request:1025 - HTTP Request:
POST https://html.duckduckgo.com/html/ "HTTP/2 200 OK"
```

### 4. In-chat end to end (native tool loop)

Headless Chromium (Playwright) drove the real UI: signed in, opened the
composer integrations menu, enabled the Web Search toggle, sent "What are the
latest AI news headlines this week?". The outgoing completion payload carried
`features.web_search: true` (captured by a request listener in the driver
script). The pinned backend then injected its builtin `search_web` tool, the
model called it, and the backend executed the DuckDuckGo search server side.

Container logs during the turn:
```
| ddgs.base:search:117 - response:
https://grokipedia.com/api/typeahead?query=What+latest+news+headlines+this+week&limit=1 200
| ddgs.base:search:117 - response:
https://en.wikipedia.org/w/api.php?action=opensearch&profile=fuzzy&limit=1&search=What%20latest%20news%20headlines%20this%20week 200
| ddgs.base:search:110 - response:
https://yandex.com/search/site/?text=What+latest+news+headlines+this+week&web=1 200
```

The reply rendered with a "View Result from search_web" citation row and a
"3 Sources" chip; the composer shows the active Web Search globe pill.
Screenshots: `shot-1-reply.png`, `shot-2-menu.png`, posted to the PR through
scripts/post-pr-visual-proof.sh.

Mechanism note discovered during verification: the pinned backend's forced
RAG-style search path only fires when `params.function_calling == 'legacy'`.
With the default native function-calling mode the backend instead injects the
builtin `search_web` + `fetch_url` tools and the model calls them. Both paths
are gated on the same `ENABLE_WEB_SEARCH` env this change sets, so pure-env
enablement covers both. Models served through the gateway that support tool
calling get the native path; the stub LLM used in this proof implements the
tool-call round trip to exercise it end to end.

No credentials appear anywhere on this page; the throwaway stack uses a
local-only signup and its token is not recorded.

## Rev 2: the persisted-config trap on the demo box

The pinned image's config model is DB-backed per-key storage whose
`Config.seed_defaults` inserts only absent keys, so "Existing DB values take
precedence over defaults": env flips after first boot are silent no-ops. The
demo box's chat database confirmed it read-only before any code was written:

```
$ ssh <box> docker exec hive-open-webui-1 python3 -c "<sqlite read-only>"
web.search.enable = "false"
web.search.engine = ""
web.search.bypass_web_loader = "false"
web.search.bypass_embedding_and_retrieval = "false"
(61 web.search rows total, all seeded at the box's first boot)
```

The rev 1 enablement (workflow env alone) would have been a silent no-op in
production. The fix rides the existing #722 reconcile
(owui-patches/hive_rag_env_config.py): the four keys follow the container
environment when it names them, so the demo's workflow env writes
true/duckduckgo/true/true over the stale rows on the next boot of the
reconcile-carrying image.

Reconcile unit check (overrides() against the four env values):

```
with env:    {'web.search.engine': 'duckduckgo', 'web.search.enable': True,
              'web.search.bypass_web_loader': True,
              'web.search.bypass_embedding_and_retrieval': True}
without env: {}
```

## Manual step on the demo box, 2026-08-24

The reconcile ships in this branch's image; the box runs main's image until
merge. To make the feature live for the demo without merging, the same effect
was applied by hand, with a DB backup taken first:

1. `docker compose stop open-webui` (deploy flag set, chat profile)
2. backup: `cp webui.db webui.db.bak-websearch-<ts>` on the volume
3. update the four rows to true / duckduckgo / true / true
4. `docker compose up -d open-webui` with the four env vars exported
   (identical to the workflow env the PR adds to deploy-demo-box.yml)

The next merged deploy rebuilds the image with the reconcile and re-asserts
the same four values from workflow env, and the manual state and the PR end
state agree.

### Reconcile proven against a seeded stale DB

A throwaway boot of the patched image against a hand-seeded webui.db whose
config table carried the box's exact stale rows (false / empty / false /
false), with the four env values exported, produced at boot:

```
| open_webui.config:seed_registered_defaults:52 - hive: reconciled Open WebUI
config from env: automations.enable=False, calendar.enable=False,
memories.enable=False, notes.enable=False,
rag.embedding_model=sentence-transformers/all-MiniLM-L6-v2,
web.search.bypass_embedding_and_retrieval=True, web.search.bypass_web_loader=True,
web.search.enable=True, web.search.engine=duckduckgo
```

and a read-back of the seeded rows showed all four flipped:

```
web.search.enable = true
web.search.engine = "duckduckgo"
web.search.bypass_web_loader = true
web.search.bypass_embedding_and_retrieval = true
```

The rate-limit patch is present in the built image (grep count 1 for the
patch comment in retrieval/web/duckduckgo.py, with `raise` replacing the
swallow).

## Live on the demo box, 2026-08-24 (through chat-hive.scubed.co)

After the manual step above, verified against the real deployment:

1. Authenticated `/api/config` through chat-hive.scubed.co reports
   `enable_web_search: True`.
2. `POST /api/v1/retrieval/process/web/search` from the box returned live
   DuckDuckGo results for a current-events query (Bangladesh technology news,
   August 2026: thedailystar.net, dailybanglapost.com, digibanglatech.news).
3. In the real Hive-branded chat UI on the box (deepseek-v4-flash selected):
   the composer shows the Web Search globe pill active, the turn ran, and the
   message shows "Retrieved 3 sources" from the live DuckDuckGo search
   (screenshot shot-3-box-reply.png, posted to the PR).

Honest limitation recorded: the completion TEXT on the box errored with the
fork's "chat session is not carrying a signed-in user token" message, because
the verification used a throwaway locally-created chat account, and such an
account has no stored OAuth session for hive_jwt_forward to forward to the
gateway. Every real user signs in through Hive SSO and carries that token, so
the search half (the feature this change enables) is fully exercised on the
box; the completion-text half is blocked by the throwaway account's missing
OAuth session, a pre-existing deployment auth property unrelated to web
search. The full cited-reply loop (toggle, search_web tool call, live DDG
results, cited answer text) is proven end to end on the throwaway stack
running the same branch image (shot-1-reply.png).

Throwaway user rows (auth + user for wsproof+box@hive-e2e.invalid) were
deleted from the box's chat database after capture.
