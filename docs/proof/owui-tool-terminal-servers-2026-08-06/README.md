# Open WebUI tool server and terminal server containment (#770, #771)

Captured 2026-08-06 against a local stack, not against the demo box. Nothing was
deployed and no running service was touched.

## Stack under test

| Component | What it was |
| --- | --- |
| Open WebUI | `Dockerfile.open-webui` built from this branch, whose last layer runs `deploy/docker/owui-patches/remove_integrations_tab.py`. The "before" captures use the same Dockerfile from `HEAD~`, so the only difference between them is that patch. |
| Blocking proxy | Real `caddy:2-alpine` with `deploy/docker/Caddyfile.owui` bind-mounted byte for byte from this branch. No local edits. |
| Logging proxy | A second `caddy:2-alpine` in front of the blocker, doing nothing but JSON access logging and proxying. It exists so the file under test never had to gain a `log` directive, and so every browser request is recorded with the status the blocker actually returned. |
| Upstream | A stub OpenAI-compatible server serving `/models`, `/chat/completions`, and `/embeddings`. No provider key, no network egress. |

Browser traffic went `browser -> logger -> blocker -> open-webui`. A second port
reached Open WebUI directly, bypassing both proxies, and is used only to create
the first admin and to prove that a blocked route really exists behind the block.

The account is `verify-admin@example.invalid` with a throwaway password, created
on a container-local SQLite database that was destroyed with the stack. It holds
the Open WebUI `admin` role, which is the role every tenant owner holds on the
real deployment (#748), so these captures exercise the worst case rather than a
plain member.

## Files

| File | What it shows |
| --- | --- |
| `01-both-directions-status-codes.txt` | Every target path requested twice with the same admin bearer token, once through the public origin and once straight at Open WebUI. Blocked paths answer 404 publicly while the direct request shows the route is live, which distinguishes "Caddy refused it" from "no such route". |
| `02-live-browser-path-capture.txt` | Every distinct method and path a real signed-in browser session issued, read out of the logger's access log, each marked against the Caddyfile's own `@blocked` regex. Static assets elided. |
| `03-settings-integrations-before.png` | Before. Settings > Integrations with "Manage Tool Servers" and "Open Terminal". |
| `04-add-tool-server-form-before.png` | Before. The Add Connection form the panel opens: a free-text URL and an API key with a Bearer/Session/OAuth selector. |
| `05-settings-integrations-gone-after.png` | After. The Integrations entry is absent from the settings navigation. |
| `06-settings-search-terminal-no-results-after.png` | After. Searching the settings modal for "terminal" returns no results, so the keyword route into the panel is gone too. |
| `07-chat-loads-after.png` | After. The chat surface loads through the blocking proxy. |
| `08-model-picker-after.png` | After. The model picker opens and lists the catalogue. |
| `09-chat-reply-and-document-attached-after.png` | After. A chat turn completes and a document attaches. |
| `10-rag-retrieved-1-source-after.png` | After. The follow-up answer reports "Retrieved 1 source", so upload, embedding, and retrieval all still work. |

## Reading

Blocked, with the route proven live behind the block: the whole
`/api/v1/terminals` router including the any-verb proxy and the websocket path,
and `/api/v1/configs/tool_servers` and `/api/v1/configs/terminal_servers` with
their `verify`, `policy`, `lifecycle`, and `refresh` children. Case variants and
`//`-prefixed variants are blocked too. The paths #774 already blocked are
re-asserted here so this change cannot quietly undo them.

Unchanged: `/api/v1/configs/banners`, `/api/v1/configs/models`,
`/api/v1/configs/connections`, `/api/v1/tools/`, `/openai/models`,
`/api/v1/models/*`, `/api/v1/files/*`, `/api/v1/knowledge/`, and the rest of the
open list, all identical through the proxy and direct.

One caveat, stated rather than hidden. Of the 206 distinct paths the browser
issued, exactly one matched the block: `GET /api/v1/terminals/`, which the app
layout calls on every page load. Its client returns an empty list on any non-2xx
instead of surfacing an error, so the terminal list is empty rather than broken,
and the message input's terminal menu renders only when that list is non-empty.
The cost is one line in the browser console, Chrome's own "Failed to load
resource" note for a non-2xx fetch, with no thrown error, no toast, and no
visible effect in captures 07 through 10.
