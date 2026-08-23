# Proof: sign in with no intermediate page, live and signed in

Fixes the sign-in half of PR #952. This directory carries the capture's text
log only. The images are posted as permanent GitHub Release assets through
`scripts/post-pr-visual-proof.sh`, not committed here, because a
`raw.githubusercontent.com` link pinned to this branch's name would 404 the
moment the branch is deleted at squash-merge (PR #867, `.wolf/decisions.md`
D-042). `npm run lint:proof-tokens` scans this directory and nothing else,
which is why the log lives here rather than only in a PR comment.

## What this replaces

The 2026-08-22 captures on this PR were **bundle level** and said so: sign-in
on the deployed instance was failing with an unsupported-scope error from the
authorization server, so no chat session could be minted through the
sanctioned flow at all and the three states captured were the refusal paths.

That blocker is gone (#1003), so this is the capture that was owed: a real
OAuth 2.1 authorization-code round trip ending on a signed-in chat surface.

## Substrate

**BEFORE** is `https://chat-hive.scubed.co`, the deployed demo box running
`main`, loaded signed out. Nothing on that box was changed, and no account,
conversation or other data on it was opened.

**AFTER** is a complete local stack, not a bundle and not a stub:

- self-hosted Supabase data plane from `docker-compose.enterprise.yml`
  (`selfhost` profile): Postgres, GoTrue v2.189.0, PostgREST, storage, and the
  `caddy-supabase` gateway
- `control-plane`, `edge-api`, `litellm`, `redis`
- `web-console-prod` behind `caddy-console`, serving `/auth/v1` on the console
  origin exactly as the demo box does
- Open WebUI built from **`main` merged with this branch**, so what is
  exercised is the post-merge state rather than the branch in isolation

The repository's own migration chain was applied to the fresh database with
`scripts/apply-migrations.sh` (probe reported `baseline_state=fresh`, 91
migrations applied), and the Open WebUI OAuth client was registered with
`scripts/register-owui-oauth-client.py`. No hosted Supabase project is
involved anywhere: the old one (`yimgflllgdsbcibnaxqe.supabase.co`) no longer
resolves.

No session was injected and no token was minted by hand. The only way this
capture reaches the chat surface is a genuine authorization-code exchange
against a real authorization server.

## What is shown

`01-before-deployed-intermediate-page.png` — the deployed box today.
`/api/config` reports `oauth.auto_redirect = false`, and the page renders
exactly one action: a "Continue with Hive" button on a page whose only purpose
is to lead to another sign-in page. This is the state the owner objected to.

`02-after-signed-in-zero-clicks.png` — the local branch stack. `/api/config`
reports `oauth.auto_redirect = true`. Starting from the chat root with no Open
WebUI session and **zero clicks**, the browser lands signed in on the chat
surface. Measured in the page rather than asserted:

| Measurement | Value |
|---|---|
| Clicks performed on the hop | 0 |
| Password inputs on the landed page | 0 |
| Iframes on the landed page | 0 |
| "Continue with Hive" buttons on the landed page | 0 |
| "Suggested" stock prompt blocks | 0 |

The full main-frame navigation chain is in `capture.log`. `/auth` appears in
it as a pass-through redirect and is never rested on, which is the difference
between traversing a route and making a person look at a page.

## The one hop that is a click, and why that is correct

On a brand new instance with no users at all, Open WebUI reports
`onboarding: true`, and this branch deliberately **refuses** to auto-redirect
in that state (`ssoAutoRedirectDecision`, reason `onboarding`). The first
sign-in on a virgin instance is therefore a click by design. That hop is
labelled as setup in the log and is not the claim under test; the claim is the
second hop, which performs zero clicks.

This was observed rather than assumed: the first run of this capture recorded
`onboarding: true` throughout and correctly did **not** auto-redirect, which
is what surfaced the environment fault described below.

## Credential handling

Every credential-bearing query parameter is redacted in this log and in the
URL banner burned into each screenshot: `code`, `state`, `nonce`,
`code_challenge`, `client_id`, `authorization_id`, and any token parameter,
plus any URL fragment. The fixture address `owner@hive-verify-952.invalid` is
a throwaway on a local database created for this run and destroyed with it; it
is an address, not a credential. No password was set, read, reset or rotated
on any shared or deployed account.

## Environment fault found on the way, reported separately

`deploy/supabase/init/00-extensions.sql` sets `ALTER DEFAULT PRIVILEGES` for
schema `storage` but never for `public`, so on a genuinely fresh self-hosted
install every table the migration chain creates in `public` lands with no
grant to `service_role`, and PostgREST answers `permission denied for table
tenants` (SQLSTATE 42501) to the provisioning scripts. Hosted Supabase ships
those grants, and the demo box is a restore of the hosted database, so it
inherited them and the gap has never been hit in anger. It is unrelated to
this PR and is being raised on its own; this capture granted them on the local
throwaway database only, to bring it to the state a hosted project would
already have been in.
