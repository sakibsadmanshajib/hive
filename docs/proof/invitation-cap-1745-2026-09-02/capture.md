# Issue #1745 visual proof, 2026-09-02

The invitation endpoint had no cap, so one authenticated account could drive
unbounded Brevo sends to arbitrary addresses. The cap that fixes it refuses
server-side; these captures are about the other half, which is what a person
sees when it refuses. A refusal that reads "please try again" is the one
instruction guaranteed to fail while the window is open, and that is what the
console said before this branch.

## How the captures were taken

Every stack is the real `web-console` production server (`next build`, then
`next start`) running in the repo's own `deploy/docker` `web-console` service,
each built with `--build` so the image carries that tree's source rather than a
stale one. `proof-stub.mjs` stands in for Supabase Auth and the control plane so
a status can be produced on demand without touching a shared environment and
without sending a single email; everything above the HTTP boundary (middleware,
Server Components, the route handler this PR changes, the invite panel) is
product code.

The stub answers the invitation POST with a fixed sequence, one status per call:
`201`, then `429`, then `503`. The two refusal bodies are byte-identical to what
`apps/control-plane/internal/accounts/http.go` produces, which is asserted
separately by `TestInvitationHandler_CapAnswers429WithRetryAfterAndNoDimension`
and `TestInvitationHandler_CounterOutageAnswers503`, so what the console renders
here is what it renders against the real server.

| capture | tree | upstream answered | console answered |
| --- | --- | --- | --- |
| 00 | `origin/main` @ 216c0824b | `429` | `400` |
| 01 | `fix/1745-invitation-cap` @ HEAD | `201` | `201` |
| 02 | `fix/1745-invitation-cap` @ HEAD | `429` | `429` |
| 03 | `fix/1745-invitation-cap` @ HEAD | `503` | `503` |

01, 02 and 03 are one browser session on one page, in that order, with no
restart between them.

URLs carry no credentials, so nothing is redacted. The session is a fabricated
token for a fabricated account (`proof@example.com`, "Northwind Analytics"); no
real user, workspace or address appears. The invitation link in 01 carries
`PROOF-FIXTURE-NOT-A-REAL-INVITATION-TOKEN`, which the stub emits in place of a
token precisely because that value lands in a screenshot.

## 00 before, unmodified main, upstream 429

The console answers **HTTP 400** and renders `Could not create the invitation.
Please try again.`

Two things are wrong with that frame. The advice is false: the cause is a cap
with a wait attached, and trying again immediately fails again. And the status
is wrong: a refusal that the caller can retry later, and a counter outage, both
arrive as "bad request", which is neither.

## 01 after, this branch, an invitation inside the cap

The console answers **HTTP 201** and renders the success notice naming the
address, with the invitation link. This is the control: it is what makes 02
evidence that the refusal is caused by the upstream status rather than by this
change having broken the form.

## 02 after, this branch, the cap refuses

The console answers **HTTP 429** and renders `invitation limit reached, try
again in 5 minutes` in the failure alert. The wait is the control-plane's own
text, passed through rather than replaced with a generic message, and the
success notice from 01 is gone (asserted: zero `role="status"` elements).

The message names no dimension and no address. Which cap tripped is not
disclosed, because a per-address refusal would otherwise tell the caller that
somebody recently invited that address.

## 03 after, this branch, the counter is unreachable

The console answers **HTTP 503** and renders `Invitations are temporarily
unavailable. Please try again shortly.` The limiter fails closed on a backend
error (#51), and a backend outage is not the caller's quota running out, so it
is not reported as one.

## Assertions

Every line in `capture-after.log` and `capture-before.log` is a checked claim,
not a description: `proof-shots.mjs` throws and takes no screenshot when one is
false, so a run that produced these images is the evidence. The checked claims
are the console's HTTP status on each POST, the exact rendered alert text, the
presence or absence of the success notice, and that the page loaded at all.

## Commands

```
# after (worktree at fix/1745-invitation-cap)
cd deploy/docker
docker compose run --build --rm --no-deps --name proof1745after \
  -p 3311:3000 -p 9311:9311 \
  -v <repo>/docs/proof/invitation-cap-1745-2026-09-02/proof-stub.mjs:/stub.mjs \
  -e PROOF_STUB_PORT=9311 \
  -e NEXT_PUBLIC_SUPABASE_URL=http://localhost:9311 \
  -e NEXT_PUBLIC_SUPABASE_ANON_KEY=proof-anon-key \
  -e NEXT_PUBLIC_APP_URL=http://localhost:3311 \
  -e CONTROL_PLANE_BASE_URL=http://localhost:9311 \
  web-console sh -c "node /stub.mjs & npm run build && npm run start -- --hostname 0.0.0.0 --port 3000"

PROOF_APP_URL=http://localhost:3311 PROOF_STUB_URL=http://localhost:9311 \
PROOF_ANON_KEY=proof-anon-key PROOF_OUT_DIR=$PWD \
PROOF_APP_DIR=<repo>/apps/web-console PROOF_MODE=after \
  node proof-shots.mjs

# before: the same two commands in a worktree detached at origin/main,
# on ports 3312 and 9312, with PROOF_MODE=before.
```
