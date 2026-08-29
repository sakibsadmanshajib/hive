# Getting a live session for an automated test run

**Read this before writing any script that signs a test run into a deployed
environment.** There is one supported way to do it:

```
apps/web-console/tests/e2e/support/live-auth.mjs   # the implementation, also a CLI
apps/web-console/tests/e2e/support/live-auth.ts    # the spec-side entry point
```

## No credential is ever committed

This repository is public. A test credential in a tracked file is a published
credential, and a fixture password is not inert: the seeder writes it back onto
the live account on every run, so the committed value is the account's actual
password.

The sanctioned pattern is an environment variable with **no fallback**. A
missing one fails loudly and names itself. It never falls back to a default and
never turns into a silent skip, because a silent skip is how a committed value
survives unnoticed:

```js
// apps/web-console/tests/e2e/support/e2e-auth-creds.ts
export const E2E_VERIFIED_PASSWORD = requiredSecretEnv(
  "E2E_VERIFIED_PASSWORD",
  DEFAULTS.minPasswordLength
);
```

`e2e-auth-defaults.json` carries addresses and length limits only. Two unit
guards in `apps/web-console/tests/unit/e2e-auth-creds.test.ts` fail if a
credential-shaped field reappears there. Addresses may stay: an address is not
a credential.

Local runs therefore need this before `npx playwright test`:

```bash
export E2E_RUN_KEY="$(whoami)-$(date +%s)"
export E2E_VERIFIED_PASSWORD=...
export E2E_UNVERIFIED_PASSWORD=...
export E2E_INVITATION_TOKEN=...
```

CI passes the two passwords from repository secrets, generates the invitation
token randomly per job (it used to be a committed literal joined to the public
run id, and the seeder stores its sha256 as a live pending invitation, so
anyone reading the run page could compute it), and sets the run key to
`run_id-run_attempt`.

`E2E_RUN_KEY` is not optional, and the next section is why.

## Every fixture address belongs to the run

Seeding writes a password to whatever address it is handed. So the address it
is handed must never be a shared one:

* `runScopedEmail` (`e2e-fixture-seed.mjs`, mirrored in `e2e-auth-creds.ts`)
  throws without a run key, and otherwise turns the shared base address into
  this run's own (`e2e-verified+<key>@...`). It is idempotent, because CI
  passes an already-namespaced address alongside the same key.
* There is no default address anywhere in the seeding path. The `DEFAULT_EMAILS`
  constant that used to supply the two shared live accounts is gone.
* Stale run-scoped rows are swept after three hours by `sweepStaleFixtureRuns`,
  so this costs no permanent rows.

This closes a real hole rather than a theoretical one. `ensureUser` sends
`password:` on both of its update paths, so before the guard, every
credential-less local run overwrote the password of two shared live accounts
that hold a tenant OWNER role, and revoked every session other runs held on
them. A committed default made that write idempotent and therefore invisible;
removing the default without this guard would have made each operator write a
different value.

## Rotating a shared account's password is forbidden

Never set, reset or rotate the password of a shared test account to obtain a
session. Not as a fallback, not "just this once", not behind a flag.

Why: the account is shared mutable state. The control-plane resolves every
bearer against GoTrue on each request, so changing the password invalidates
every session that any other run currently holds. On 2026-08-08 a scratch
script did exactly that and broke three agents working concurrently; four
separate agents were blocked on credentials across that day and the one after.

## Never point a write-capable suite at the demo account

The password is not the only shared state on `demo@hive-demo.invalid`. It is
also the account the owner demos to prospects, and every chat this account
sends, every agent task it submits, and every API key it mints is real,
visible in that account's own sidebar, task list and keys page, and today
undeletable: there is no chat-delete, task-delete or account-delete route
wired up yet (issues #828, #848). A suite that authenticates as this account
and drives a real composer, a real task submit, or a real key mint leaves a
permanent mark on the surface the owner is about to show someone.

This happened. `docs/proof/chat-interaction-coverage-2026-08-10/coverage.run.json`
records `"demo@hive-demo.invalid -> hive-coverage-77811 survived a reload"`,
and the 2026-08-11 demo-readiness walk (issue #858) found the sidebar full of
empty "New Chat" rows and the agent task list "a wall of 'cancel-guard proof'
and 'interaction-coverage proof' entries," on that same account. Issue #848
tracks the cleanup and the standing fix.

So: a suite that only signs in and reads (a redirect check, a rendered
heading, an unclicked button) is fine against the demo account. A suite that
sends a message, submits a task, or mints a key must run as a dedicated,
non-demo identity instead, the same way the plain E2E suite already does with
its `E2E_RUN_KEY`-scoped fixture accounts (see above), or the way
`seed-owui-e2e-user.py --account-slug` stands up a billing account of its
own rather than sharing one. Do not add a new literal fallback to
`demo@hive-demo.invalid` anywhere in this codebase; the existing ones are
being removed, not extended.

`scripts/verify-control-plane.py` is NOT an example of the read-only case,
despite never rotating a password: its "api-key lifecycle" check mints a real
key and sends a real `POST /v1/chat/completions` through it, which is a write
and a real spend, not a read. It now requires `HIVE_VERIFY_EMAIL` with no
default rather than falling back to the demo account (issue #848); point it
at a dedicated verification identity, same as any other write-capable suite.

### What this repository does about it

Three scripts here used to rotate unconditionally. They no longer do, and the
shape they now share is the one to copy:

| script | account | how it behaves now |
| --- | --- | --- |
| `scripts/seed-demo-owner.py` | `demo@hive-demo.invalid` | `password_to_set` leaves an existing account alone unless `HIVE_DEMO_PASSWORD` is set. Creation still generates one. |
| `scripts/seed-owui-e2e-user.py` | `owui-e2e@…`, `owui-e2e-bootstrap@…` | Same helper. Prints a `PASSWORD` line only for a password it actually set. `OWUI_E2E_RUN_KEY` namespaces both addresses per run, which is how the nightly gets a usable credential without touching a shared one, and `sweep_stale_fixture_users` clears what earlier runs left. |
| `scripts/verify-rag-roundtrip.py` | `rag-verify-e2e@hive-e2e.invalid` | Never had a password to begin with: signs in through the admin one-time-token mint (same protocol as `live-auth.mjs`, reimplemented in Python since the script has no other reason to depend on a browser or `@supabase/ssr`). No `RAG_VERIFY_PASSWORD` and nothing to save. |

The shim API key that `seed-owui-e2e-user.py` mints is a separate hazard with
the same shape. Its billing account stays shared (a tenant bills to exactly one
account, so a per-run account needs a per-run tenant and leaves a permanent row
behind), so the revocation of previously minted keys is bounded by age rather
than by "every key except mine". An identity-bounded delete had two overlapping
runs revoking each other's key mid-flight.

`scripts/seed-owui-e2e-user.py` matters most of the three: it is the only one
invoked automatically. `.github/workflows/owui-nightly.yml` runs it on a
schedule, on `workflow_dispatch`, and on any pull request labelled
`run-owui-e2e`. That workflow's `concurrency.group` is keyed on the ref, so a
labelled pull request run and the scheduled run are in different groups and can
overlap. Before the run key, they rotated the same two accounts out from under
each other.

The web-console fixture seeder
(`apps/web-console/tests/e2e/support/e2e-fixture-seed.mjs`) writes the resolved
passwords onto its fixture accounts too. In CI the addresses are run-scoped, so
it only ever touches its own run's users. Run locally with no `E2E_RUN_KEY`, it
targets the shared `e2e-verified@scubed.com.bd` and
`e2e-unverified@scubed.com.bd` accounts, which is why the credentials it seeds
with must come from your environment and why that path must never silently
default.

`scripts/verify-control-plane.py` states the same rule in its docstring and
signs in with an existing password it is given. That is fine on the password
question specifically. Inventing a new password is not. It is not otherwise a
read-only script; see the note above about its identity requirement.

### The scratch script to avoid

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

## Playwright artifacts are public

A workflow artifact on this repository is downloadable by anyone holding the
run URL, for as long as it is retained. A Playwright trace is not a log: it
records every request header (`Authorization: Bearer`), every cookie
(`sb-*-auth-token`), and every navigation URL, including an OAuth callback
whose `code=` and `access_token=` sit in the query string and the fragment. No
text linter can see inside it, because it is a zip of binary resources.

`index.html` is no better. The HTML reporter base64-inlines the entire report
payload into it, stdout, stderr and error text included, and a `waitForURL`
timeout enumerates every URL it navigated, fragment and all. Same material,
same binary wrapper, same blindness in any text linter.

So both report uploads exclude `*.zip`, `*.webm` and `index.html`, and set
`retention-days: 5`:

* `playwright-report-api` in `.github/workflows/ci.yml`
* `owui-playwright-report` in `.github/workflows/owui-nightly.yml`

Screenshots still upload, and the failing test names and error text are already
in the job log. Renaming an artifact is not a fix.

Container log dumps are the same problem in plain text: Open WebUI's uvicorn
access lines print `GET /oauth/oidc/callback?code=...` verbatim, and edge-api
logs carry the shim key. The three log artifacts
(`compose-logs`, `compose-logs-web-e2e-api`, `owui-compose-logs`) pipe through
`scripts/redact-log-credentials.py`, which mirrors `redactSecrets`, handles
fragments as well as query strings, and carries its own self-check
(`python3 scripts/redact-log-credentials.py --selfcheck`, also in
`make test-scripts`).

If you add a job that uploads Playwright output or container logs, apply the
same exclusions, pipe the logs through the redactor, and set a short retention.
