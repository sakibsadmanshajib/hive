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

`prod-substrate-signin.png` is that confirmation: freshly captured against
`docker compose --profile local --profile chat up --build control-plane
web-console-prod caddy-console` on this exact commit (containers created
seconds before the capture, confirmed against `git rev-parse HEAD`). It shows
the real production Next.js build serving `/auth/sign-in` through Caddy, and
`next build`'s route manifest for this run includes
`/console/api-keys/[id]/limits`, so the fixed route is present in the image
being served.

It stops at the sign-in page rather than a signed-in limits-page screenshot.
Getting a real session on `web-console-prod` needs either a live Supabase
Cloud sign-in (this project's stated convention, restated above, is to keep
proof captures off the shared session-mode pooler) or a full local Supabase
emulator, which was out of proportion to stand up for this fix. The
authenticated functional proof above (local stand-ins) is unchanged code, so
it still accurately shows this branch's behavior; only the container that
served it during capture differs from what the demo box runs. Flagging this
gap plainly rather than fabricating a signed-in screenshot against the prod
container.
