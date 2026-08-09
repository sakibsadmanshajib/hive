# Live demo box proof, 2026-08-09

Captured against the actually-running demo box at `main` `87eb22d6` (`chat-hive.scubed.co`,
`console-hive.scubed.co`). No stack was rebuilt for this capture; both hosts returned
their live production responses at capture time.

## Session method

Signed in via the audited magic-link mint (`apps/web-console/tests/e2e/support/live-auth.mjs`,
PR #810, not yet merged at capture time -- fetched from `origin/test/live-auth-helper` and run
standalone). No password was read, set, or rotated. The session was injected as cookies
directly into a fresh Playwright browser context (`context.addCookies`); no storage-state
file or token was ever written to disk.

## Account used: `demo@hive-demo.invalid`

Confirmed live by direct query against the production database immediately before capture
(not assumed):

| column | value |
| --- | --- |
| `accounts.is_platform_admin` | `f` (false) |
| `account_memberships.role` / `status` | `owner` / `active` |
| `tenant_users.role` / `status` | `OWNER` / `ACTIVE` |

This is a genuine workspace OWNER with no platform-admin bit, the exact shape PR #788 gates on.

## PR #788 -- workspace-scoped admin areas

`feature-gates-owner-live.png` -- `https://console-hive.scubed.co/console/feature-gates` as
this OWNER: the ADMIN nav group is reachable, the gate list renders (workspace-scoped
capability/RAG/audit-sink/SSO gates are live toggles; platform-only rows read "Managed by
your administrator" instead of a wall). No "Admin access required" state.

`marketplace-owner-live.png` -- `https://console-hive.scubed.co/console/marketplace` as the
same OWNER: renders the empty-catalog state, no wall, no curation form (curation stays
platform-admin only).

## PR #763 -- agent workspace task composer

`agent-workspace-composer-live.png` -- `https://chat-hive.scubed.co/agent-workspace` as the
same account: a real composer ("What should the agent do?" textarea, Coding / Knowledge work
pack chips, Start task button), plus the honest "agent runtime is not configured on this
deployment" state banner. Not just two pack buttons.

## Console root redirect investigation

`console-root-resolved.png` -- `https://console-hive.scubed.co/` follows its `307` to
`/auth/sign-in` and lands on a genuine, working sign-in form (email + password fields,
Continue button), HTTP 200. Confirmed identical for curl's default UA and a Chrome UA. The
`307` response body itself contains Next.js's generic `id="__next_error__"` RSC scaffolding,
which is used for every special server-side outcome (redirects included) and carries
`digest: "NEXT_REDIRECT;replace;/auth/sign-in;307;"` -- a deliberate redirect marker, not a
crash. A browser never renders that intermediate body; it follows the `Location` header
immediately. No defect found, no issue filed.
