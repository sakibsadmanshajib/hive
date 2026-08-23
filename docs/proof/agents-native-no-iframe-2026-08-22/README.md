# Proof: the Agents surface asks for no second credential

PR #951, issue #540. This directory carries the capture's text log only
(`capture.log`); the screenshots are posted as permanent GitHub Release assets
through `scripts/post-pr-visual-proof.sh`, per `.wolf/decisions.md` D-042. The
log lives here rather than only in a PR comment because
`npm run lint:proof-tokens` scans this directory and nothing else.

Recaptured on 2026-08-22 after the branch was rebased onto `main` at
`851003b7b`, which is the merge of PR #787. The rebase conflicted in one place
only, a single line in the Makefile's `test-scripts` list, and both sides were
kept.

## What is being proved

Issue #540, re-confirmed live on the demo box on 2026-08-22, is the top demo
blocker: a user who signs into `chat-hive.scubed.co` normally and then clicks
**Agents** is shown a second email and password form, captioned that the
workspace is separate from chat. The mechanism was proved there by difference,
not guessed: chat's OAuth handshake mints an Open WebUI token and never writes
the `sb-...-auth-token` cookie that the embedded `apps/agent-console` reads, so
the embedded application falls back to its own sign in.

This branch removes the embedded application from that route. The claim under
test is therefore narrow and checkable: **starting from the chat landing page,
clicking the sidebar's Agents entry renders the workspace natively, with no
iframe and no credential prompt anywhere on the way.**

## Method, and what is real in it

- **The bundle is real.** It is the output of
  `docker build -f deploy/docker/Dockerfile.open-webui --target frontend` on
  this branch as rebased: byte for byte the artefact the image copies into
  `/app/build`. Nothing was hand-edited into it. That build also runs the
  fork's own unit tests, which this branch adds to the frontend stage, and they
  passed there: three files, thirty-one tests, including this branch's
  `agentTasks.test.ts`.
- **The navigation is real.** The capture loads `/`, screenshots the chat
  landing, then clicks the sidebar entry and screenshots where it lands. It
  does not type `/agents` into the address bar, because a typed URL would not
  answer the question the issue asks.
- **The backend is stubbed.** A local static server serves the bundle and
  Playwright fulfils `/api/*` with the shapes Open WebUI's own handlers emit,
  including the authenticated `/api/config` block and two agent tasks, one
  succeeded and one running.
- **The origin is `127.0.0.1`, not the deployed host.**

## Why the live deployment could not be used, stated plainly

Three independent blockers. None of them is a property of this branch, and the
first one is new since the previous capture:

1. **Sign in on the deployed instance is currently broken outright, and it
   broke on `main`, not here.** `GET https://chat-hive.scubed.co/oauth/oidc/login`
   redirects to the authorization server with
   `scope=openid+email+profile+offline_access`, and that request comes straight
   back to the callback with `error=invalid_request` and
   `error_description=unsupported scope: offline_access`. The same URL with the
   scope reduced to `openid email profile` reaches the consent page normally.
   The self-hosted GoTrue the box now runs advertises
   `scopes_supported = ["openid","profile","email","phone"]` in its own
   discovery document, with no `offline_access` in it. So nobody can sign into
   chat through the sanctioned flow at the moment. A separate change owns that
   fix; this pull request must not touch it.
2. **The surface depends on a backend patch, not only on the bundle.** The
   native panel calls `/api/v1/hive/agent/*`, a router this branch adds through
   `deploy/docker/owui-patches/hive_agent_proxy.py`. That router exists only in
   a rebuilt image. Intercepting the bundle against the deployed origin, the
   technique PR #956 used for its timings, cannot conjure it: those paths would
   404 on the currently deployed image.
3. **No live chat session can be minted from this machine either.** The
   sanctioned one-time-token flow
   (`apps/web-console/tests/e2e/support/live-auth.mjs`) needs `SUPABASE_URL` to
   resolve, and the development box's `.env` still points it at the hosted
   Supabase project that was deleted in the self-hosted migration. That host
   now returns `ENOTFOUND`. Working around it by setting or rotating any
   password is forbidden and was not attempted.

So the faithful "signed in through chat, click Agents, real task runs" capture
is a post-deploy artefact and is still owed. It is scheduled: once the sign in
fix lands and deploys, this surface gets recaptured live before anyone treats
the demo blocker as closed. What is captured here is the part that can be
captured before merge, and the limits are stated rather than papered over.

## Result

From `capture.log`, measured in the page rather than asserted in prose:

```
chat landing:                                            password inputs=0, iframes=0
sidebar entries pointing at /agents:                     1
links anywhere in the signed-in chat UI to /agent-workspace: 0
url after clicking Agents:                               .../agents
after clicking Agents:                                   password inputs=0, iframes=0
```

The first screenshot is the chat shell signed in, with **Agents** in the
sidebar. The second is where clicking that entry lands: the same shell with
Agents selected, the native composer captioned "Describe the task in your own
words", the Knowledge work and Coding pack selector, and the task list
rendering one task as Done and one as Running with a Cancel control. There is
no second application in the page and nothing asks for a password at either
hop.

## The residual, re-confirmed on the rebased head rather than carried over

`/agent-workspace/*` is still proxied to `apps/agent-console` by
`deploy/docker/Caddyfile.owui`, and that application still renders its own
email and password form. This branch changes no Caddy route: its only edit to
that file is a comment. So a **typed or bookmarked**
`https://chat-hive.scubed.co/agent-workspace` still reaches a password prompt.

What is confirmed is that no route inside the chat interface leads there.
Checked three ways on the rebased tree, not assumed:

- `vendor/open-webui/src/lib/hive/nav.ts` points the sidebar entry at
  `/agents`, and its `activePaths` list names only `/agents`.
- The only two occurrences of the string `agent-workspace` anywhere under
  `vendor/open-webui/src/` are historical comments, one in `app.html` and one
  in the `/agents` route file explaining what that route used to hold.
- In the running bundle, the capture counted every anchor in the signed-in
  chat UI pointing at `/agent-workspace`, and there are zero. The only
  occurrences in the built output are the same `app.html` comment and a source
  map.

Closing that route is deliberately not done here. The Tauri desktop app targets
`/agent-workspace` as its console base path (`apps/desktop/src/settings.ts`,
`apps/desktop/src-tauri/src/settings.rs`), and
`apps/web-console/tests/e2e/_probe/agent-workspace-flows.spec.ts` is a whole
coverage ledger over that surface. Answering it 404 would break both. Retiring
`apps/agent-console` or moving the desktop app onto the native surface is a
separate decision with a much larger blast radius than this pull request.

## Known artefacts of the stub, not of the branch

- The console shows `Pe is not iterable` and
  `(l(...) ?? []).some is not a function`. Those come from the catch-all stub
  answering `{}` where some chat endpoints return a differently shaped object.
  They are not reachable on a real deployment and are called out here rather
  than cropped out.
- The WebSocket handshake errors are the static file server answering the
  socket.io upgrade with a 200. Same cause.

## Credential handling

No credential appears in any captured URL or in this log. The two URLs are `/`
and `/agents` on a loopback origin, neither with a query string; the injected
session value is the literal string `stub-session-token`; the stubbed account
is `e2e-verified@scubed.com.bd`, which is an address, not a credential.
Headless Playwright screenshots carry no browser chrome, so there is no URL
overlay in the pixels to redact either. Nothing was read from, written to, or
captured about any real account.
