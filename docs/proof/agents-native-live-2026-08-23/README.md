# Proof: the agent surface renders natively inside chat, live and signed in

Replaces the bundle-level captures on this PR. Those said so in their own text:
sign-in on the deployed instance was failing with an unsupported-scope error
from the authorization server, so no chat session could be minted through the
sanctioned flow and the panel had to be reached with the backend stubbed and
the origin on loopback. That blocker is gone (#1003), so this is a real signed
-in walk.

Text log only here. Images are posted as permanent GitHub Release assets via
`scripts/post-pr-visual-proof.sh`, because a `raw.githubusercontent.com` link
pinned to this branch would 404 the moment the branch is deleted at
squash-merge. `npm run lint:proof-tokens` scans this directory and nothing
else, which is why the log is committed here.

## Substrate

One complete local stack, nothing stubbed:

- self-hosted Supabase data plane (`selfhost` profile): Postgres, GoTrue
  v2.189.0, PostgREST, storage, `caddy-supabase`
- `control-plane`, `edge-api`, `litellm`, `redis`
- `web-console-prod` behind `caddy-console`, serving `/auth/v1`
- Open WebUI behind `caddy-owui`

Built from **`main` merged with #952, #956 and this branch**, which is the
state that will exist after all three land, not this branch in isolation. The
one merge conflict, in the `Makefile` `test-scripts` recipe, was resolved by
keeping both sides: #952's `test-owui-frontend` target and this branch's
`test_owui_agent_proxy.py` entry.

Signed in through a real OAuth 2.1 authorization-code round trip. No session
injected, no token minted by hand.

## What is shown

`01-chat-signed-in-agents-in-sidebar.png` — the signed-in chat surface with
`Agents` in the sidebar.

`02-agent-surface-native-no-credential-prompt.png` — where clicking that entry
lands. No URL was typed; the surface is reached from inside the chat UI. The
chat sidebar and the account chip are still there, which is the integration
the owner asked for rather than a second application bolted on.

Measured in the live DOM at both hops, not asserted:

| Measurement | Chat landing | Agent surface |
|---|---|---|
| Password inputs | 0 | 0 |
| `iframe` elements | 0 | 0 |
| Child browsing contexts | 0 | 0 |
| "sign in" / "log in" text nodes | 0 | 0 |
| Anchors to `/agent-workspace` | 0 | 0 |

`GET /api/v1/agent/tasks` answered **200** through the running proxy chain.
That is the part that makes this more than a rendering check: the request goes
Open WebUI → `hive_agent_proxy` → `edge-api`, carrying the signed-in user's own
token on the new `X-Hive-Upstream-Auth` header this PR adds, and it is accepted.

## What is deliberately not claimed

The task list reads "Nothing submitted yet". Submitting a task for real needs
the unprivileged host launcher and `HIVE_AGENT_ENGINE_SOCKET`; under docker
compose that arm cannot succeed at all, by design, per the agent-engine section
of `CLAUDE.md`. So the empty state is the honest ceiling on this substrate.
What is proven here is the claim this PR actually makes: the surface is native,
reached from inside chat, asks for no second credential, and its authenticated
data path works end to end.

The `/agent-workspace` path still 307s to agent-console's own form when typed
directly. That is deliberate and is not a regression: the Tauri desktop app
targets that base path.

## Security review of the auth change

The new header carrier in `apps/edge-api/internal/auth/owui_unwrap.go` is an
auth boundary, so it had a dedicated security review. Verdict: safe to merge,
with one LOW finding about a test guard that could not fail, now fixed on this
branch and verified by mutation. Details are in the PR thread.

## Credential handling

Credential-bearing query parameters are redacted in this log and in the URL
banner burned into each screenshot: `code`, `state`, `nonce`, `code_challenge`,
`client_id`, `authorization_id`, any token parameter, and any URL fragment. The
fixture address is a throwaway on a local database created for this run and
destroyed with it. No password was set, read, reset or rotated on any shared or
deployed account.
