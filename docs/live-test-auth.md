# Getting a live session for an automated test run

**Read this before writing any script that signs a test run into a deployed
environment.** There is one supported way to do it:

```
apps/web-console/tests/e2e/support/live-auth.mjs   # the implementation, also a CLI
apps/web-console/tests/e2e/support/live-auth.ts    # the spec-side entry point
```

## Rotating the shared demo password is forbidden

Never set, reset or rotate the password of a shared test account to obtain a
session. Not as a fallback, not "just this once", not behind a flag.

Why: the account is shared mutable state. The control-plane resolves every
bearer against GoTrue on each request, so changing the password invalidates
every session that any other run currently holds. On 2026-08-08 a scratch
script did exactly that and broke three agents working concurrently; four
separate agents were blocked on credentials across that day and the one after.

The scratch helper `demo_login.py` (agent scratchpad, not in this repository)
is the specific script to avoid. Its only login path is:

1. `GET /auth/v1/admin/users` to find the account, then
2. `PUT /auth/v1/admin/users/{id}` to overwrite its password with a value it
   just generated, then
3. sign in with that value.

Step 1 has also been returning intermittent `500 Database error finding users`
(issue #791), and step 2 is the rotation described above. It is left in place
only because other agents still reference it; do not use it to log in, and do
not copy its shape into anything new.

`scripts/verify-control-plane.py` states the same rule in its docstring and
signs in with an existing password it is given. That is fine. Inventing a new
password is not.

## What the helper does instead

It mints a session the way a magic-link login does, which needs no knowledge of
the password:

1. `POST /auth/v1/admin/generate_link` with `{"type":"magiclink","email":...}`
   using the service-role key. The account is addressed **by email**, so the
   broken admin user-listing endpoint (#791) is never called. The account is
   not created, listed, or modified.
2. `POST /auth/v1/verify` with `{"type":"magiclink","token_hash":...}` using the
   anon key. This exchanges the one-shot token for a normal
   `access_token`/`refresh_token` pair and consumes it.

The `POST` form of `/auth/v1/verify` is used deliberately. The `GET` form
answers with a redirect that carries the session in the **URL fragment**, which
is far easier to leak into a log, a trace, or a screenshot.

If either call fails, the helper throws with the reason. There is no fallback
path that rotates anything, and adding one re-opens the incident above.

### Proof it mutates nothing

Recorded live against the demo account on 2026-08-08, before and after a full
mint (`docs/proof/live-auth-helper-2026-08-08/`):

| column | before | after |
| --- | --- | --- |
| `md5(encrypted_password)` | `cb8f7cb0b64bb37f0d2da3153b663ab1` | `cb8f7cb0b64bb37f0d2da3153b663ab1` |
| `md5(coalesce(confirmation_token,''))` | `d41d8cd9…` (empty) | `d41d8cd9…` (empty) |
| `md5(coalesce(recovery_token,''))` | `d41d8cd9…` (empty) | `d41d8cd9…` (empty) |

The password hash is byte-identical, and the one-time token columns are empty
on both sides because step 2 consumes what step 1 wrote. The only columns that
move are the ordinary sign-in timestamps that any login moves: `updated_at`,
`last_sign_in_at`, `recovery_sent_at`.

## Using it

As a Playwright storage-state producer, so specs start already signed in:

```ts
// some.setup.ts
import { writeStorageState } from "./support/live-auth";

writeStorageState(
  { email: process.env.HIVE_QA_AGENT_EMAIL!, targetUrl: "https://chat-hive.scubed.co/agent-workspace" },
  "tests/e2e/.auth/agent-workspace.json",
);
// playwright.config.ts -> use: { storageState: "tests/e2e/.auth/agent-workspace.json" }
```

Signing an already-open context in, which is also the re-authentication path:

```ts
import { reauthenticate } from "./support/live-auth";

await reauthenticate(page.context(), { email, targetUrl });
await page.reload();
```

Standalone, from a shell:

```bash
cd apps/web-console
node tests/e2e/support/live-auth.mjs demo@hive-demo.invalid \
  https://chat-hive.scubed.co/agent-workspace tests/e2e/.auth/agent-workspace.json
```

Requires `SUPABASE_URL`, `SUPABASE_SERVICE_ROLE_KEY`, `SUPABASE_ANON_KEY`.

### Sessions that die mid-run

Issue #782: a chat session dies roughly 55 minutes after sign-in because a
token refresh fails and destroys the OAuth session (fix in PR #787, unmerged).
A run longer than that must call `reauthenticate` and retry rather than record
a working control as a broken one. `withLiveSession` in `live-auth.mjs` wraps
that as run-once, re-auth, run-once-more.

### Why two files

`live-auth.mjs` is the single implementation and the CLI. `live-auth.ts` shells
out to it. Playwright compiles spec imports to CommonJS, so importing a `.mjs`
from a spec fails with `exports is not defined in ES module scope`; the child
process also keeps the service-role key out of the Playwright worker's module
graph, so it cannot reach a page, a trace, or a video. Same shape and same
reasons as `support/reset-profile.ts`.

## Logging is safe, including verbose

Everything the helper prints goes through `redactSecrets`
(`tests/e2e/support/e2e-fixture-seed.mjs`), which scrubs credential-bearing
parameters wherever they appear — **URL fragments as well as query strings** —
plus bare JWTs, plus the service-role key literal.

That distinction is not theoretical. On 2026-08-08 an agent's own redactor split
URLs on `?` and handled query parameters only, so it printed a live session
token to stdout: GoTrue returns the session after `#`, never after `?`. The
guards in `tests/unit/e2e-fixture-ids.test.ts` pin this down, and one of them
asserts that a faithful query-string-only redactor still leaks the fragment
token, so the fix cannot be quietly regressed into the version that leaked.

Related: `npm run lint:proof-tokens` catches credentials in committed proof
text, but cannot inspect screenshot pixels. Masking an image is still on the
agent capturing it.
