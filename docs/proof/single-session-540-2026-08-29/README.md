# One sign in for chat and the agent surface (issue #540)

Captured 2026-08-29 for pull request #1460. The images are on the permanent
`visual-proof-assets` release and linked from that pull request; only the text
lives here, because `npm run lint:proof-tokens` scans this directory and
nothing else.

The claim being proved is not "the agent surface renders". It is "a user meets
one credential prompt and nothing after it asks again", and separately "the
path this pull request removes was still presenting a second, independent
prompt". A screenshot of the agent surface alone cannot tell those apart, which
is why this is a sequence.

## `capture.log` and images 01 to 05, live demo box before the change

`journey.mjs` produced them. No password was typed and none was changed: part A
walks to the single credential prompt and stops without submitting it, and part
B starts from that prompt already satisfied, carrying the account's existing
Supabase session on the console origin, minted through the admin one-time-token
flow (`docs/live-test-auth.md`). The account is a per-run seeded fixture, not
the shared demo account.

What it establishes, in order:

1. **01** A browser with no session anywhere reaches chat and is redirected
   straight into the identity provider on the console origin. That is the one
   credential prompt, and there is only one.
2. **03** With that prompt satisfied, chat signs the user in with no further
   prompt. `chat asked for credentials again: false`.
3. The cookie dump is the root cause of the whole issue. What the browser holds
   on the chat origin is `token`, `oauth_id_token` and `oauth_session_id`, all
   Open WebUI's own, and **zero** `sb-*` cookies. `apps/agent-console` reads a
   Supabase cookie, and on this origin there was never one to read, so it
   redirected every visitor to a sign in of its own regardless of the chat
   session. "Same origin, same cookie" was not merely expensive, it was
   unavailable.
4. **04** The agent surface at `/agents` renders from that same one session,
   with no prompt and no iframe (`iframes on the agent surface: 0`).
5. **05** `/agent-workspace/tasks` redirects to a second sign in page whose own
   copy reads "This workspace is separate from chat, so signing in here does
   not sign you out of it." The product was advertising two sessions.

The `authorization_id` in the part A URL is redacted. It is a short-lived
identifier for a pending authorization request, and the rule on credentials in
query strings does not have an exception for short-lived ones. No screenshot
carries a URL overlay, so there is no pixel half to mask here.

## `caddy-before.log`, `caddy-after.log` and image 06, the change itself

`caddy-ab.sh` runs the real `Caddyfile.owui` from `origin/main` and from this
branch against one stub upstream that answers 200 on every service name the
file proxies to, so what is measured is the shipped matcher set rather than a
paraphrase of it. Adapted from
`docs/proof/chat-origin-admin-lockdown-2026-08-23/caddy-ab.sh`.

| path | before | after |
| --- | --- | --- |
| `/agent-workspace` | 200 | 404 |
| `/agent-workspace/tasks` | 200 | 404 |
| `/agent-workspace/auth/sign-in` | 200 | 404 |
| `//agent-workspace` | 200 | 404 |
| `/Agent-Workspace/tasks` | 200 | 404 |
| `/` | 200 | 200 |
| `/agents` | 200 | 200 |
| `/api/v1/hive/agent/tasks` | 200 | 200 |
| `/api/v1/hive/credits/balance` | 200 | 200 |
| `/v1/agent/tasks` | 200 | 200 |
| `/v1/featuregate` | 200 | 200 |
| `/_app/immutable/nodes/7.js` | 200 | 200 |
| `/agent-workspaces` | 200 | 200 |

Image **06** is the same after-state in a browser: `/agent-workspace/tasks`
answers 404 where image 05 showed a sign in form, while `/agents` on the same
listener still reaches the backend.

This half is measured locally rather than on the box because the box runs
`main` until this merges. The deployed result is confirmed after the merge,
per the deploy step of the pipeline.
