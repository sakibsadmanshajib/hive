# Proof: the sign in page's three refusal states, captured from the branch bundle

PR #952. Text log only (`capture.log`); the screenshots go to the permanent
visual-proof release through `scripts/post-pr-visual-proof.sh`, per
`.wolf/decisions.md` D-042. The log is committed here because
`npm run lint:proof-tokens` scans this directory and nothing else.

Supersedes the captures in `docs/proof/owui-signin-suggestions-displayname-2026-08-17/`,
which were taken on 2026-08-17 against the hosted Supabase baseline. That
project has since been deleted in the self-hosted migration, so those captures
no longer describe a reachable system. The older directory's text log is kept
for its record of the display name work.

## What is worth proving, and what is not

The visible half of this change, arriving at the provider without an
intermediate page, is the easy half: with one provider configured and the
password form off, the page's only control was a choice between one option.
The half that can lock every user out is the refusal logic, the set of
conditions under which the page must **not** redirect. `ssoAutoRedirectDecision`
is a pure function with sixteen unit tests for exactly that reason, and this
capture is the visual counterpart: the three states a user can actually land in
and must not be bounced out of.

## Method

The bundle is the output of
`docker build -f deploy/docker/Dockerfile.open-webui --target frontend` on this
branch, rebased onto `main` at `c30882491`. A local static server serves it and
Playwright fulfils `/api/config` with the shape Open WebUI's own handler emits,
one provider, no password form, no LDAP, no trusted header, and
`oauth.auto_redirect` true, which is the condition `OAUTH_AUTO_REDIRECT` sets in
compose.

The live deployment was not used, and the reason is not a judgement call: the
development box's `.env` still points `SUPABASE_URL` at the hosted Supabase
project deleted in the self-hosted migration, and that host now returns
`ENOTFOUND`, so `live-auth.mjs` cannot mint a session. Setting or rotating a
password to get one is forbidden and was not attempted. `oauth.auto_redirect`
is also still false on the deployed instance, because the compose variable that
turns it on ships in this pull request.

## Result

Three URLs, three captures, each a state where redirecting would be wrong:

- `/auth?signed_out=true` renders the page. Signing out lands back here while
  the provider's own session is usually still live, so without this arm signing
  out would be impossible: the page would send the user straight back in.
- `/auth?error=access_denied` renders "Sign in did not complete. Your sign in
  provider reported a problem. Try again below, and tell your administrator if
  it keeps happening." with a Continue with Hive button under it. A broken
  provider is an explained page with a retry, not a bounce loop.
- `/auth?form=1` reaches the manual page, the deliberate escape hatch.

## Credential handling

No credential appears in any captured URL or in this log. The three query
strings are `signed_out=true`, `error=access_denied` and `form=1`; the origin is
loopback; no account was signed in and no token exists in the run. Headless
Playwright screenshots carry no browser chrome, so there is no URL overlay in
the pixels either.

## Known artefacts of the stub

The console shows WebSocket handshake failures: the static file server answers
socket.io's upgrade with a 200. Not reachable on a real deployment, listed here
rather than cropped out.
