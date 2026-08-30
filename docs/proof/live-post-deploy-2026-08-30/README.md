# Live post-deploy capture: Cowork pack selector, summary echo, credential attribution, Knowledge row

Captured 2026-08-30 against the deployed demo box, not against a fixture and
not against a standalone container. The box was on `bbcf1118` (main tip) with
the stack recreated at 03:14:39 UTC, so every fix listed below was live at
capture time.

This is the after-state four separate pull requests said they owed and could
not take at the time:

* PR #1518 (issue #1500), whose own proof README states outright that "an
  `agent_tasks` row with `pack = coding-pack` created end to end through the
  deployed stack ... does not exist until this merges and `deploy-demo-box`
  runs".
* PR #1522 (issue #1509), which proved the render against a fixture and left
  the live payload explicitly open.
* PR #1519 (issue #1507), which stated "live ledger proof needs a deploy,
  which this PR does not perform".
* PR #1516 (issue #1502), the Knowledge navigation row.

## How the session was obtained

The admin one-time-token flow in
`apps/web-console/tests/e2e/support/live-auth.mjs`, as
`post-deploy-verify@hive-verify.invalid`. No password was set, reset or
rotated, and the shared demo account was not used.

The deployed Supabase admin API is not exposed on `console-hive.scubed.co`
(the console's Caddy answers 404 on `/auth/v1/admin/*`), so the mint ran
through an ssh port-forward to the box's `hive-caddy-supabase-1` container,
which routes on the `Host` header. Session cookies were minted against the
public origin `https://console-hive.scubed.co` so `@supabase/ssr` derives the
cookie name the apps actually read.

## What was observed

### Pack selector, issue #1500

In Cowork mode the composer's second row renders two segments,
`Knowledge work` and `Coding`. Issue #1500's own query, repeated verbatim
(`button:has-text(...)`, `[role=button]:has-text(...)`), returned 0 for both
labels before the fix. It now returns 1 for each. Clicking `Coding` sets
`aria-checked=true` on `[data-hive-pack="coding-pack"]` and moves the
radiogroup's `data-hive-composer-pack` to `coding-pack`.

Submitting from that composer created `agent_tasks` row
`cdf605fd-5c1e-471a-b425-e5f65740b626` with `pack = coding-pack`, which
reached `status = succeeded`. That is the row PR #1518 could not create.

### Summary echo, issue #1509

The settled turn renders its closing summary once. Collapsed, the single
visible step line is `Workspace file: hello.py`, not the summary. Expanded,
the step list carries four steps (two workspace-file lines, a `file_editor`
line and a `terminal` line) and no echo of the summary. A page-text search for
the summary's opening sentence returns 1 in both states.

Before `dropSummaryEcho` the collapsed step line was the summary itself, which
is the doubling issue #1509 reported.

### Credential attribution, issue #1507 / PR #1519

Read from the box's own database.

`api_keys` holds one row whose id is the task id, `kind = 'agent_task'`,
`account_id = a0634c83-4060-4724-9a80-ebaaa285953a` (the submitting tenant's
own billing account, not a Hive-owned one), `status = revoked`, minted
03:22:01.738 and revoked 03:22:54.731. The task's own `finished_at` is
03:22:54.727, so revocation follows the terminal transition by four
milliseconds.

`last_used_at` is 03:22:49.280, so the credential is NOT in the
`kind = 'agent_task' AND last_used_at IS NULL` set PR #1519 defines as
"settled nothing".

The ledger for that account over the run window shows the submit-time solvency
probe (a 100,000,000 credit hold released 11 ms later, `endpoint`
`agent_tasks`, reason `solvency_probe`) followed by four sandbox model turns,
each a hold, a `usage_charge` and a release on `endpoint = chat_completions`.
Charges: 248,162 + 718,901 + 54,839 + 125,506 = 1,147,408 credits.

Every one of those four turns is joined to the credential rather than
asserted: `request_attempts.api_key_id` equals the task id on all four, and
`request_attempts.account_id` is the tenant's account on all four.

### Knowledge navigation row, issue #1502

The sidebar renders New Chat, Search, Projects, Artifacts, Skills, Scheduled.
`a[href="/knowledge"]` count is 0 and no navigation entry matches
`/knowledge/i`.

## Where the images are

Posted as inline images on PR #1522, uploaded to the permanent
`visual-proof-assets` release by `scripts/post-pr-visual-proof.sh`. They are
not committed here: `docs/proof/` carries the text log, which is what
`npm run lint:proof-tokens` scans.

## Credential handling

Nothing captured here carries a credential. The flows driven are a chat
sign-in that arrives on cookies installed before navigation and an ordinary
conversation URL; no invitation, reset or magic-link URL was opened in the
browser, so no query-string token appears in any frame or in the log below.
