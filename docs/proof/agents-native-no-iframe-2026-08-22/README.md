# Proof: the Agents surface asks for no second credential

PR #951, issue #540. This directory carries the capture's text log only
(`capture.log`); the screenshots are posted as permanent GitHub Release assets
through `scripts/post-pr-visual-proof.sh`, per `.wolf/decisions.md` D-042. The
log lives here rather than only in a PR comment because
`npm run lint:proof-tokens` scans this directory and nothing else.

## What is being proved

Issue #540, re-confirmed live on the demo box on 2026-08-22, is the top demo
blocker: a user who signs into `chat-hive.scubed.co` normally and then clicks
**Agents** is shown a second email and password form, captioned that the
workspace is separate from chat. The mechanism was proved there by difference,
not guessed: chat's OAuth handshake mints an Open WebUI token and never writes
the `sb-...-auth-token` cookie that the embedded `apps/agent-console` reads, so
the embedded application falls back to its own sign in.

This branch removes the embedded application from that route. The claim under
test is therefore narrow and checkable: **on `/agents`, the built bundle renders
the workspace natively, with no iframe and no credential prompt.**

## Method, and what is real in it

- **The bundle is real.** It is the output of
  `docker build -f deploy/docker/Dockerfile.open-webui --target frontend` on
  this branch, rebased onto `main` at `c30882491`: byte for byte the artefact
  the image copies into `/app/build`. Nothing was hand-edited into it.
- **The backend is stubbed.** A local static server serves the bundle and
  Playwright fulfils `/api/*` with the shapes Open WebUI's own handlers emit,
  including the authenticated `/api/config` block and two agent tasks, one
  succeeded and one running.
- **The origin is `127.0.0.1`, not the deployed host.**

## Why the live deployment could not be used, stated plainly

Two independent blockers, neither of them a property of this branch:

1. **The surface depends on a backend patch, not only on the bundle.** The
   native panel calls `/api/v1/hive/agent/*`, a router this branch adds through
   `deploy/docker/owui-patches/hive_agent_proxy.py`. That router exists only in
   a rebuilt image. Intercepting the bundle against the deployed origin, the
   technique PR #956 used for its timings, cannot conjure it: those paths would
   404 on the currently deployed image.
2. **No live chat session can be minted from this machine.** The sanctioned
   one-time-token flow (`apps/web-console/tests/e2e/support/live-auth.mjs`)
   needs `SUPABASE_URL` to resolve, and the development box's `.env` still
   points it at the hosted Supabase project that was deleted in the self-hosted
   migration. That host now returns `ENOTFOUND`. Working around it by setting or
   rotating any password is forbidden and was not attempted.

So the faithful "signed in through chat, click Agents, real task runs" capture
is a post-deploy artefact, and this pull request is not being merged by the
agent that produced this directory. What is captured here is the part that can
be captured before merge, and the limits are stated rather than papered over.

## Result

From `capture.log`, measured in the page rather than asserted in prose:

```
password inputs on /agents: 0
iframes on /agents: 0
"Sign in" appears in body text: false
```

The screenshot shows the Hive shell with **Agents** selected in the sidebar, the
native composer captioned "Describe the task in your own words", the Knowledge
work and Coding pack selector, and the task list rendering one task as Done and
one as Running with a Cancel control. There is no second application in the
page and nothing asks for a password.

## Known artefacts of the stub, not of the branch

- The sidebar's Recents column reads "Loading...", and the console shows
  `fe.sort is not a function` and `a.map is not a function`. Those come from the
  catch-all stub answering `{}` where the chat list endpoints return arrays.
  They are not reachable on a real deployment and are called out here rather
  than cropped out.
- The WebSocket handshake errors are the static file server answering the
  socket.io upgrade with a 200. Same cause.

## Residual, and it is a real one

`/agent-workspace/*` is still proxied to `apps/agent-console` by
`deploy/docker/Caddyfile.owui`, and that application still renders its own email
and password form. This branch changes no Caddy route: its only edit to that
file is a comment. So a **typed or bookmarked** `https://chat-hive.scubed.co/agent-workspace`
still reaches a password prompt, even though no route inside the chat interface
leads there any more (`vendor/open-webui/src/lib/hive/nav.ts` points the sidebar
entry at `/agents`, and the only two remaining mentions of the old path in the
front end are historical comments).

Closing that route is deliberately not done here. The Tauri desktop app targets
`/agent-workspace` as its console base path (`apps/desktop/src/settings.ts`,
`apps/desktop/src-tauri/src/settings.rs`), and
`apps/web-console/tests/e2e/_probe/agent-workspace-flows.spec.ts` is a whole
coverage ledger over that surface. Answering it 404 would break both. Retiring
`apps/agent-console` or moving the desktop app onto the native surface is a
separate decision with a much larger blast radius than this pull request.

## Credential handling

No credential appears in any captured URL or in this log. The three URLs are
`/agents` on a loopback origin with no query string; the injected session value
is the literal string `stub-session-token`; the stubbed account is
`e2e-verified@scubed.com.bd`, which is an address, not a credential. Headless
Playwright screenshots carry no browser chrome, so there is no URL overlay in
the pixels to redact either. Nothing was read from, written to, or captured
about any real account.
