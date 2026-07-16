# Installing hive_agent_console_action.py

Open WebUI Functions have no file-mount or env-var auto-load (#269): a
Function is a database row created through the Functions REST API,
authenticated as an OWUI admin. CI installs `hive_jwt_forward.py` this way
in `apps/web-console/e2e/phase-19/owui/owui.setup.ts`, right after the e2e
user's first OIDC login (Open WebUI auto-promotes the very first signed-in
user to admin). Production and EnterpriseEdge deployments have no such CI
step, so an admin installs `hive_agent_console_action.py` once, manually,
after the first admin account exists.

## One-time install (after the first admin signs in)

Replace `$OWUI_URL` with the deployment's Open WebUI origin (e.g.
`http://localhost:3003` behind caddy-owui) and `$OWUI_ADMIN_COOKIE` with an
authenticated admin session cookie (copy the `token` cookie from a signed-in
admin browser session, or script an admin login first).

```bash
OWUI_URL=http://localhost:3003
FUNCTION_ID=hive_agent_console_action
CONTENT=$(cat hive_agent_console_action.py)

# 1. Create the function (skip if it already exists -- GET
#    "$OWUI_URL/api/v1/functions/id/$FUNCTION_ID" first to check).
curl -s -X POST "$OWUI_URL/api/v1/functions/create" \
  -H "Cookie: token=$OWUI_ADMIN_COOKIE" \
  -H "Content-Type: application/json" \
  -d "{\"id\": \"$FUNCTION_ID\", \"name\": \"Open Agent Workspace\", \"content\": $(python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))' < hive_agent_console_action.py), \"meta\": {\"description\": \"Opens the standalone agent-console sidecar, gated on ENABLE_COWORK (#311).\"}}"

# 2. Activate it.
curl -s -X POST "$OWUI_URL/api/v1/functions/id/$FUNCTION_ID/toggle" \
  -H "Cookie: token=$OWUI_ADMIN_COOKIE"

# 3. Make it global (visible to every user, not just the admin who created it).
curl -s -X POST "$OWUI_URL/api/v1/functions/id/$FUNCTION_ID/toggle/global" \
  -H "Cookie: token=$OWUI_ADMIN_COOKIE"
```

All three calls are idempotent to re-run: `create` on an existing id is
rejected harmlessly, and the two `toggle` calls just flip state (check
`GET $OWUI_URL/api/v1/functions/id/$FUNCTION_ID` first, as
`owui.setup.ts` does, to skip a toggle that's already in the desired
state).

## Verifying

Sign in as any user, open a chat, send a message, and confirm the "Open
Agent Workspace" action button appears under the assistant's response.
Clicking it appends either the workspace link or a gate/session error
message, depending on the signed-in user's `ENABLE_COWORK` state --
see `hive_agent_console_action.py`'s module docstring for the full
gating and event-emission behavior, and its "DISCOVERY NOTE FOR
REVIEWERS" section for what's confirmed vs. assumed against the pinned
OWUI image.

## Automating this later

Same open item as `hive_jwt_forward.py`: this manual step could be
automated for non-CI deployments (e.g. a first-boot script that waits for
the first admin, then runs the three calls above). Not built here --
tracked as a follow-up, not a blocker for this PR.
