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
