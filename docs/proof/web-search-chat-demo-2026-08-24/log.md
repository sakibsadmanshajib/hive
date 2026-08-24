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
