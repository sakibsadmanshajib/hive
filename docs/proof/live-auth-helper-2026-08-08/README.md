# Live-session helper: evidence, 2026-08-08

Evidence for `apps/web-console/tests/e2e/support/live-auth.mjs`. Protocol and
the forbidden-shortcut list live in `docs/live-test-auth.md`.

## 1. Minting a session mutates no credential

Run against the demo Supabase project, `auth.users` for `demo@hive-demo.invalid`,
immediately before and immediately after one full mint
(`generate_link` -> `verify`).

```sql
select id,
       md5(encrypted_password)                as pw_md5,
       updated_at, last_sign_in_at, recovery_sent_at,
       md5(coalesce(confirmation_token,''))   as conf_tok_md5,
       md5(coalesce(recovery_token,''))       as rec_tok_md5
from auth.users
where email = 'demo@hive-demo.invalid';
```

| column | before (17:53:03Z) | after (17:59:28Z) | verdict |
| --- | --- | --- | --- |
| `pw_md5` | `cb8f7cb0b64bb37f0d2da3153b663ab1` | `cb8f7cb0b64bb37f0d2da3153b663ab1` | unchanged |
| `conf_tok_md5` | `d41d8cd98f00b204e9800998ecf8427e` | `d41d8cd98f00b204e9800998ecf8427e` | unchanged (md5 of empty string) |
| `rec_tok_md5` | `d41d8cd98f00b204e9800998ecf8427e` | `d41d8cd98f00b204e9800998ecf8427e` | unchanged (md5 of empty string) |
| `updated_at` | 17:53:03.267546+00 | 17:59:28.194316+00 | moved (any login moves it) |
| `last_sign_in_at` | 17:53:03.259156+00 | 17:59:28.165828+00 | moved (any login moves it) |
| `recovery_sent_at` | 17:52:48.175418+00 | 17:59:27.090894+00 | moved (the one-time token was issued) |

The password hash is byte-identical. The one-time token columns are empty on
both sides: `generate_link` writes one and `verify` consumes it inside the same
mint, so nothing is left behind. Only sign-in timestamps move.

The helper never calls `PUT /auth/v1/admin/users` and never calls
`GET /auth/v1/admin/users` (the endpoint returning intermittent 500s, #791). It
addresses the account by email and throws if either call fails; there is no
credential-rotating fallback in it.

## 2. Redaction: fragment as well as query string

`redactSecrets` (`apps/web-console/tests/e2e/support/e2e-fixture-seed.mjs`) is
the single redactor every message from the fixture CLI and the live-auth helper
passes through. It previously scrubbed only literal known secrets.

RED — the four new guards run against the pre-fix redactor
(`git show origin/main:...e2e-fixture-seed.mjs` restored in place):

```
 x redactSecrets: URL-borne credentials > scrubs a session token carried in a URL fragment
   -> expected '...' not to contain 'eyJhbGciOi...'
 x redactSecrets: URL-borne credentials > scrubs a one-time OTP carried in a query string
   -> expected '...' not to contain 'pkce_9f3c1'
 x redactSecrets: URL-borne credentials > scrubs a bare JWT with no parameter name around it
 x redactSecrets: URL-borne credentials > does not mistake a longer credential name for a shorter one
      Tests  4 failed | 8 passed (12)
```

GREEN — same guards against the shipped redactor:

```
 Test Files  1 passed (1)
      Tests  12 passed (12)
```

One of those guards asserts that a faithful query-string-only redactor (split on
`?`, scrub the parameters) still leaks the fragment token. It is kept in the
test file permanently, so the fix cannot be regressed back into the shape that
leaked a live session token to stdout on 2026-08-08.

`tools/lint-no-token-in-proof-captures.mjs` was extended in the same class:
`token_hash`, `hashed_token`, `email_otp` and bare JWTs are now caught, with a
fragment fixture in its self-test (12 assertions, passing).

## 3. Agent-workspace coverage gate, re-run with the helper

See `coverage.md` in this directory.
