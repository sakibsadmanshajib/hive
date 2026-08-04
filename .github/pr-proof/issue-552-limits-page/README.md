# Issue 552 visual proof: /console/api-keys/{id}/limits

Captured against a running `next dev` server built from this branch (and, for the
before shot, from `origin/main`), with Playwright driving a real Chromium.

| File | What it shows |
| --- | --- |
| `limits-before.png` | Unfixed code (`origin/main`): the page renders the error boundary. HTTP 500. |
| `limits-after.png` | This branch: the page renders, the form is populated from the limits response, and a save round-trip reports "Saved." |
| `limits-before.log` | Playwright log for the before run, including the HTTP status and the rendered body text. |
| `limits-after.log` | Playwright log for the after run, including the read values and the save result. |
| `server-error-before.log` | The server-side cause on unfixed code: `TypeError: Failed to parse URL from /api/v1/accounts/current/api-keys/{id}/limits`. |

Substrate notes, stated plainly:

- The Next.js server, its middleware, the Supabase SSR cookie plumbing and the
  React Server Component render are all real.
- Supabase auth and the control-plane were local stand-ins on this machine. The
  defect is entirely in how the Server Component resolved its own URL, so a
  local upstream exercises the same code path, and no shared infrastructure was
  touched: no live Supabase project, and no database connection, so the shared
  session-mode pooler was never involved.
- No credential appears in any captured URL. The path carries an API key
  identifier, never key material, and the identifier here is a locally invented
  UUID rather than a real key.

## Container-substrate verification (added during #683 review)

`limits-before.png` / `limits-after.png` above were captured against `next
dev`, not the container image the demo box actually runs. A different PR
(#696) shipped proof captured against the stale `dev`-profile
`hive-web-console-1` container, so this PR's review asked for confirmation
against the correct pair: `hive-web-console-prod-1` (the real
`next build && next start` image) behind `hive-caddy-console-1` (the same
Caddy origin the demo box uses).

`prod-substrate-signin.png` was the first pass at that confirmation: it
reached the real pair but stopped at the sign-in page, short of a signed-in
limits-page screenshot, because the control-plane container needs the shared
session-mode Supabase DB pooler to start, which this project's proof captures
deliberately stay off (see above).

`limits-after-prod-container.png` / `limits-after-prod-container.log` close
that gap by pointing the same *class* of local stand-in used for
`limits-after.png` (a fake auth server, a fake control-plane) at the real
container pair instead of `next dev`:

- `web-console-prod` was rebuilt from this exact commit with
  `NEXT_PUBLIC_SUPABASE_URL` baked to a local stand-in GoTrue server
  (`fake-auth-server.js`, stdlib `http` only) instead of live Supabase Cloud,
  and `CONTROL_PLANE_BASE_URL` pointed at a local stand-in control-plane
  (`fake-control-plane-server.js`) instead of the real service. Neither
  stand-in opens a database connection of any kind, so the shared
  session-mode pooler is never touched, and the real `control-plane` /
  `redis` services were never started (`--no-deps`, only `web-console-prod`
  and `caddy-console` were brought up).
- Both stand-ins ran as plain Node processes on the host, reachable from the
  container via Docker Desktop's `host.docker.internal`. The image tag was
  overridden to `hive-web-console-prod:proof552` for the build so it never
  clobbers the shared `hive-web-console-prod:ci` tag another concurrent
  worktree on the same box might be using.
- Confirmed against the real substrate, not the stale `dev`-profile
  container PR #696 shipped: `docker top` on the running container shows
  `next-server (v15.5.15)` under a `next start` command line (the `next dev`
  image never produces a `.next/BUILD_ID`; this container's
  `.next/BUILD_ID` and its route manifest's
  `/console/api-keys/[id]/limits/page` entry both exist), the container was
  created seconds before the capture, and `git rev-parse HEAD` at capture
  time was this commit.
- The Playwright browser itself ran inside a short-lived
  `mcr.microsoft.com/playwright:v1.51.1-jammy` container attached to the
  same compose network, because a bare-host Chromium process cannot reach
  `host.docker.internal` the way a container can on this box (proven the
  hard way: the first attempt hung with the sign-in button stuck on
  "Signing in..." because the browser-side `fetch` to the auth stand-in
  never completed). Running the browser as a container gives it the same
  `host.docker.internal` route the app container uses, so both sides resolve
  the stand-ins identically.
- Result: signed in, `GET /console/api-keys/{id}/limits -> HTTP 200`, the
  rate-limit form renders populated (rpm 60, tpm 4000), and a save round-trip
  reports "Saved.", the same shape of proof as `limits-after.png`, now
  against the real prod-container substrate.

The demo box itself (the actual `hive-web-console-prod-1` /
`hive-caddy-console-1` pair running in production) was never reached by any
of this and still isn't: there is no SSH access to it from this
environment. This capture closes the "wrong substrate class" gap (`next dev`
vs. the real production build), not the separate "not the literal demo box"
gap, which stays open for the same reason it always has been.

The two stand-in server scripts and the Playwright driver
(`fake-auth-server.js`, `fake-control-plane-server.js`, `capture-prod.js`)
are committed here so the next run of this capture does not have to
reconstruct them from scratch.
