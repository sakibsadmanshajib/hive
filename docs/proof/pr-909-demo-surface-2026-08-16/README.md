# PR #909 visual proof, 2026-08-16

Two different things are captured here, and they are not interchangeable.

## 1. The #846 menu change, proven on images, not on the deployment

The branch is not deployed. `main` runs the demo box, so the deployed chat still
renders the Admin Panel entry. To show the branch's actual effect, both images
were built from the same pinned upstream digest and the same
`deploy/docker/Dockerfile.open-webui`, differing only in the three patch files
this pull request touches (`hive_ui_surfaces.py`,
`apply_ui_surfaces_patch.py`, `pinned-bundle-excerpts.json`), and run locally
with `WEBUI_AUTH=false`, which is the same method used for the #833 and #772
proofs.

```
docker build -f deploy/docker/Dockerfile.open-webui -t hive-owui909:before .   # main's patch files
docker build -f deploy/docker/Dockerfile.open-webui -t hive-owui909:after  .   # this branch's patch files
docker run -d -p 3021:8080 -e WEBUI_AUTH=false -e WEBUI_NAME=Hive hive-owui909:before
docker run -d -p 3022:8080 -e WEBUI_AUTH=false -e WEBUI_NAME=Hive hive-owui909:after
```

| file | shows |
| --- | --- |
| `usermenu-main-image.png` | user menu built from `main`: Settings, **Admin Panel**, Archived Chats, Workspace, Notes, Calendar, Automations, Sign Out |
| `usermenu-pr909-image.png` | user menu built from this branch: the same list with **Admin Panel gone**, nothing else changed |
| `usermenu-*.log` | the same menus read out of the DOM, so the difference is machine-checkable and not a matter of reading pixels |

This proves the entry leaves the client bundle. It does not prove anything
about the `/admin` route, which `Caddyfile.owui` already 404s independently and
which was therefore never reachable.

## 2. The demo account after the #848 litter was removed

Captured against the live deployment (`chat-hive.scubed.co`,
`console-hive.scubed.co`) on 2026-08-16, after the enumerated litter was
deleted under owner authorization.

| file | shows |
| --- | --- |
| `live-1-chat-sidebar-empty.png` | the chat sidebar with no chat rows, `GET /api/v1/chats/?page=1` returning 0 |
| `live-2-chat-usermenu-deployed-main.png` | the deployed user menu, which still has Admin Panel, because this branch is not deployed. Included so the difference above is not mistaken for a deployment claim |
| `live-3-console-api-keys.png` | every API key on the account revoked, including the six `interaction gate probe` rows whose expiry reads 1 Jan 2026 |
| `live-4-console-spend-alerts.png` | "No spend alerts configured yet" |

None of these prove the branch's code. They show the state of the account the
branch is meant to stop littering.

## Notes

Sessions came from `apps/web-console/tests/e2e/support/live-auth.mjs`, which
mints a magic link with the service-role key and touches no password. No URL in
these journeys carries a credential; the capture script scrubs
token-like query and fragment parameters before writing any URL to a log
anyway. The key column in `live-3` is the product's own redacted suffix
display, on keys that are all revoked.
