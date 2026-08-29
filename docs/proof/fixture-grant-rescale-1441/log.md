# Issue #1441 / PR #1447 visual proof

A fixture account funded through the sanctioned `e2e-fixture-seed.mjs` grant
path can sign in to the chat surface, send a message, and is billed for it.

Captured against the deployed demo box on 2026-08-29, after the corrected
`FIXTURE_GRANT_CREDITS`. The OAuth `authorization_id` in the sign-in redirect
is a single-use credential and is redacted below as `[REDACTED]`; it does not
appear in the screenshots, which are headless captures with no URL bar.

## Why the conversation is read back after a reload

This deployment dispatches a chat turn asynchronously, so the send returns
before any accept-or-refuse decision exists and a fast response proves
nothing. The proof below reloads the page and reads what actually persisted.

## Transcript

```
== navigate ==
url: https://console-hive.scubed.co/auth/sign-in?next=%2Foauth%2Fconsent%3Fauthorization_id%3D[REDACTED]%26retried%3D1
title: Hive Console
== sign in form present ==
post-signin url: https://console-hive.scubed.co/oauth/consent?authorization_id=[REDACTED]&retried=1
== consent screen == https://console-hive.scubed.co/oauth/consent?authorization_id=[REDACTED]&retried=1
clicking: Approve
after consent url: https://chat-hive.scubed.co/
banner: You've used $0 today · $10.00 remaining
== sending ==
conversation url: https://chat-hive.scubed.co/c/b7de847c-2bb0-4dd4-b81c-aba50645a094
== reload and re-read persisted conversation ==
--- persisted conversation text ---
Reply with exactly the word: FUNDED
E2e Verified
Thought for less than a second
FUNDED
You've used < $0.01 today · $9.99 remaining
Chat
Cowork
Deepseek V4 Flash
--- end ---
REFUSAL PRESENT AFTER RELOAD: NO
done
EXIT=0
```

## Ledger for the same account

`public.credit_ledger_entries` for account
`21968d5f-8bfa-5f4a-f550-b892c4705402`
(`e2e-verified-workspace-8c035cb8`):

| entry_type | credits_delta | idempotency_key | created_at |
|---|---|---|---|
| grant | 10,000,000,000 | `e2e-fixture-grant:21968d5f-...` | 17:35:13 |
| reservation_hold | -100,000,000 | `reservation:61ec2753-...:reserve` | 17:43:01 |
| usage_charge | -6,799 | `reservation:61ec2753-...:charge-6799` | 17:43:02 |
| reservation_release | +100,000,000 | `reservation:61ec2753-...:release` | 17:43:02 |
| grant | 10,000,000,000 | `e2e-fixture-grant:21968d5f-...:10000000000` | 17:53:56 |

Reading down: the corrected grant lands at 10,000,000,000 credits, which is
$10.00 at 1 USD = 1e9 credits. The chat turn takes the flat $0.10
authorization hold, settles at an actual charge of 6,799 credits, and releases
the hold in full, so nothing is stranded. The final row is the top-up under
the amount-versioned idempotency key; a second re-seed immediately after it
added no further row, which is the idempotency half of that change.

The actual charge is 6,799 credits against a 100,000,000 credit hold, roughly
one part in fifteen thousand. That gap is the subject of #1450 and #699 and is
not changed by this PR.

## Before

The same seeder before this change granted 1,000,000 credits, which is $0.001,
one hundredth of the hold. The banner then read
`You've used $0 today · < $0.01 remaining` and every send was refused with
"Your available credit does not cover this request".
