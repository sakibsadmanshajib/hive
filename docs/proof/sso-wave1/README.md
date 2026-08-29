# Proof: SSO wave 1, silent server-completed consent landing

This directory carries the capture's text log only (capture.log). The frames
are posted as permanent GitHub Release assets through
scripts/post-pr-visual-proof.sh, not committed here, because a
raw.githubusercontent.com link pinned to this branch's name would 404 the
moment the branch is deleted at squash-merge (PR #867, D-042).

capture.log holds two captures: the 2026-08-29 one taken after the branch was
rebased onto main at 5e08d641, and the original 2026-08-23 one preserved
verbatim underneath it.

## What wave 1 changed

apps/web-console/app/oauth/consent/page.tsx is a Server Component that reads
the @supabase/ssr session from cookies and calls
GET /auth/v1/oauth/authorizations/{id} with the access token. When GoTrue's
auto-approve applies (an active consent row covers the requested scopes) the
page answers with one bare redirect: zero paint, zero client JS. The
interactive Approve/Deny panel renders only when there is no session yet,
when consent is genuinely required, or as a painted error state.

The decision function refuses any auto-approve target whose path is the
consent landing itself or whose protocol is not http(s): at most one redirect
leaves the page per request and it can never point back at itself.

## Acceptance numbers

Before (tracer baseline): repeat sign-in = 3 HTML loads + 3 redirects with a
painted consent stop. After: authorize -> 302 landing -> 302 callback -> chat
home; zero painted consent documents; at most one redirect beyond the
authorize round trip. Full chain in capture.log.
