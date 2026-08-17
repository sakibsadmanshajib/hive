# Open WebUI sidebar toggle accessible name (#833)

Captured 2026-08-10 against the Hive-built Open WebUI image, before and after
the six accessibility rewrites in `deploy/docker/owui-patches/hive_ui_surfaces.py`.

## How this was captured

Both images are `deploy/docker/Dockerfile.open-webui` built from this
repository, so both carry every Hive bundle rewrite. The only difference
between them is this branch's change.

```bash
# before: main at f7d9293f            # after: this branch
docker build -f deploy/docker/Dockerfile.open-webui -t hive-owui-833:before .
docker build -f deploy/docker/Dockerfile.open-webui -t hive-owui-833:after .

docker run -d --name owui833before -p 3010:8080 -e WEBUI_AUTH=false ... hive-owui-833:before
docker run -d --name owui833after  -p 3009:8080 -e WEBUI_AUTH=false ... hive-owui-833:after

node probe.mjs before ./out      # OWUI_BASE=http://localhost:3010
node probe.mjs after  ./out
OWUI_BASE=http://localhost:3010 node sweep.mjs before ./out
node sweep.mjs after ./out
```

The container runs standalone on its own sqlite database with authentication
disabled, because the question here is what the compiled bundle renders, and
that needs no gateway, no tenant and no account. Consequences visible in the
screenshots, none of them caused by this change: the vendor name and logo show
through (Hive's branding arrives through compose environment variables, not
through the image), and Notes appears in the sidebar (`ENABLE_NOTES` is a
compose value too). The two chats in the sidebar were seeded through Open
WebUI's own `POST /api/v1/chats/new` from inside the page, using the session
the page already held.

No credential appears in any artifact here. The captured URLs are
`http://localhost:3009/` and `http://localhost:3010/` with no query string and
no fragment, the screenshots are page captures with no address bar in frame,
and the session token was read and used inside the browser only. `max_tokens`
in the aria snapshots is a settings label, not a token.

## What the artifacts show

| file | shows |
| --- | --- |
| `before-desktop-collapsed.png`, `after-desktop-collapsed.png` | the collapsed rail, wide viewport |
| `before-mobile.png`, `after-mobile.png` | the navbar toggle, narrow viewport |
| `after-sidebar-open.png` | sidebar pinned open by clicking the control found by accessible name |
| `after-chat-item-menu.png` | the per-chat menu, reached from that sidebar |
| `after-message-actions.png` | copy, edit, regenerate and the rest on an opened chat |
| `after-composer-controls.png` | the Controls panel |
| `probe-*.txt` | every button in the page with its `aria-label` and `aria-expanded` |
| `aria-*.txt` | Playwright accessibility snapshots, which is where the name is actually visible |
| `sweep-*.txt` | the pin-by-name query and the four surfaces, same script against both images |

The screenshots cannot show an accessible name, which is what the `aria-*.txt`
and `probe-*.txt` files are for.

### The name, before and after

Narrow viewport, `aria-before-mobile.txt` then `aria-after-mobile.txt`:

```
- navigation:
    - button                          <- nothing else; no name at all
- navigation:
    - button "Open Sidebar"
```

Wide viewport, `aria-before-desktop.txt`: the control had no author-supplied
name, so the browser computed one from everything inside it.

```
- button "Open Sidebar New Chat Search Notes Workspace":
    - button "Open Sidebar"           <- a nested decorative button, no handler
```

After (`aria-after-desktop.txt`), and the open state from `aria-after-sweep.txt`:

```
- button "Open Sidebar":
    - button "Open Sidebar"
- button "Close Sidebar" [expanded]
```
