# Open WebUI surface subtraction, visual proof, 2026-08-17

Both halves are captured against locally built images, because this branch is
not deployed. The method is the one PR #909 used: two images built from the same
pinned upstream digest and the same `deploy/docker/Dockerfile.open-webui`,
differing only in the files this pull request touches, run with no compose file
at all.

```
docker build -f deploy/docker/Dockerfile.open-webui -t hive-owui-surfaces:before .   # main
docker build -f deploy/docker/Dockerfile.open-webui -t hive-owui-surfaces:after  .   # this branch
docker run -d --name owui-before -p 3031:8080 -e WEBUI_AUTH=false -e WEBUI_NAME=Hive hive-owui-surfaces:before
docker run -d --name owui-after  -p 3032:8080 -e WEBUI_AUTH=false -e WEBUI_NAME=Hive hive-owui-surfaces:after
node docs/proof/owui-surface-subtraction-2026-08-17/capture.mjs before http://localhost:3031 <this dir>
node docs/proof/owui-surface-subtraction-2026-08-17/capture.mjs after  http://localhost:3032 <this dir>
```

The compose-less run is the point, not a shortcut. Every surface removed here
was already off on the demo box and only there, because `docker-compose.yml`
names three environment variables and `hive_rag_env_config.py` reconciles them
onto its database. The image never carried that position, so this is what the
Hive image itself rendered, and it is the interface the owner described.

`capture.mjs` reads each list back out of the DOM, so the claim below is
asserted from state rather than from pixels. Its output is in `before.log` and
`after.log`.

## 1. The menus

| file | shows |
| --- | --- |
| `before-1-sidebar.png`, `after-1-sidebar.png` | the sidebar, Notes gone |
| `before-2-usermenu.png`, `after-2-usermenu.png` | the user menu, Notes, Calendar and Automations gone |
| `before-3-settings.png`, `after-3-settings.png` | the Settings dialog, Personalization gone, the "Admin Settings" link in the bottom-left corner gone, the vendor's "Help us translate Open WebUI!" link under the language picker gone, and no "A new version is now available" banner |

Read out of the DOM, verbatim from the two logs:

```
[before] sidebar entries: New Chat, Search, Notes, Workspace, Folders, Chats
[after]  sidebar entries: New Chat, Search, Workspace, Folders, Chats

[before] user menu: Update your status, Settings, Archived Chats, Workspace, Notes, Calendar, Automations, Sign Out
[after]  user menu: Update your status, Settings, Archived Chats, Workspace, Sign Out

[before] settings tabs: General, Interface, Personalization, Audio, Data Controls, Account, About
[after]  settings tabs: General, Interface, Audio, Data Controls, Account, About

[before] settings admin links: 1        [after] settings admin links: 0
[before] settings vendor links: [".../open-webui/blob/main/docs/CONTRIBUTING.md#-translations-and-internationalization"]
[after]  settings vendor links: []
```

The `before` user menu and sidebar are exactly the lists the owner named, which
is what identifies the image, rather than the deployment, as what he was looking
at.

## 2. The notes API, which no flag closes

`api-notes-block.log`. The two images are put behind a real Caddy on a shared
docker network (`--network-alias open-webui`, so the Caddyfile's own upstream
name resolves), one running the Caddyfile as it is on `main` and one running the
Caddyfile from this branch, and probed with a real session token.

```
notes.enable in /api/config:
  enable_notes = False  enable_calendar = False  enable_automations = False  enable_memories = False

authenticated, straight at the after image (no proxy):
  GET  /api/v1/notes/        -> 200
  POST /api/v1/notes/create  -> 200
  GET  /api/v1/calendars/    -> 403
  GET  /api/v1/memories/     -> 404

authenticated, through Caddy as configured on main:
  GET  /api/v1/notes/        -> 200
  POST /api/v1/notes/create  -> 200

authenticated, through Caddy as configured on this branch:
  GET  /api/v1/notes/        -> 404
  POST /api/v1/notes/create  -> 404
  GET  /api/config           -> 200
  GET  /api/v1/chats/        -> 200
  GET  /api/v1/knowledge/    -> 200
```

That is the whole argument for the Caddy line in one block. With
`notes.enable` false and the navigation entry gone, `POST /api/v1/notes/create`
still created a note, because `notes.py` checks the flag on none of its 9
routes. The same probe shows the other two answering 403 and 404 off their own
routers, which is why they get no proxy rule: their flag is already the
control. The kept APIs are unaffected.

## Notes

Sessions here are the throwaway `WEBUI_AUTH=false` local ones, not any real
account. No URL in these captures carries a credential, and no token appears in
any committed file. Nothing in this proof touched the demo box, any real
account, or any password.
