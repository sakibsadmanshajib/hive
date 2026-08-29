# Visual proof: Hive Auto answers on a sub-dollar balance (issue #1372, PR #1378)

Captured 2026-08-29 against the live demo box, `https://chat-hive.scubed.co`,
signed in as `qa-tester@hive.test` through the admin one-time-token mint
(`apps/web-console/tests/e2e/support/live-auth.mjs`). No password was set, reset
or rotated. No credential appears in this log or in either screenshot: the only
values shown are the account's own displayed balance and the model name.

The account is the one the defect was reported on. Its billing account is
`qa-tester`, holding 455,466,709 credits at capture time, which the composer
footer renders as `$0.455 remaining`. That is well under the 2,000,000,000
credit (2.00 USD) hold `hive-auto` used to take, which is the whole defect.

## Before, the live box on `main`

URL: `https://chat-hive.scubed.co/`
Model selected in the picker: Hive Auto
Prompt: `Say hello in one sentence.`

Result: no answer. The transcript renders

    You exceeded your current quota, please check your plan and billing details.

directly above the composer's own

    You've used $0.00287 today · $0.455 remaining

Screenshot: `before-03-answer.png` (posted to the PR through
`scripts/post-pr-visual-proof.sh`).

## After, the same box running this branch

`hive-edge-api:ci` was rebuilt on the box from this branch
(`6a35740`), the `edge-api` service recreated with the deploy workflow's own
compose flag set, and the original image restored immediately afterwards. The
box is back on `main`'s image; `docker inspect hive-edge-api-1` reports
`sha256:ca7a721983cc…`, the pre-change build, and the container is healthy.
Nothing else on the box was changed: the LiteLLM config half of this PR reaches
the box through the compose entrypoint's seed reconciliation on deploy, and was
verified by reading the running container's config rather than by editing it.

URL: `https://chat-hive.scubed.co/`
Model selected in the picker: Hive Auto
Prompt: `Say hello in one sentence, then say the words hive auto works.`

Result:

    Thought for less than a second
    Hello. Hive auto works.

Same account, same `$0.455 remaining` in the composer footer, same model.
Network: `200 https://chat-hive.scubed.co/api/chat/completions`.

Screenshot: `after-03-answer.png`.

## Ledger evidence for the same turn

`public.credit_reservations` joined to `public.request_attempts`, on account
`3104dbfc-2c29-4098-828b-6fa07b52c254`:

```
created_at                    | model_alias | reserved_credits | consumed_credits | released_credits | status    | terminal_usage_confirmed
2026-08-29 07:25:48.632339+00 | hive-auto   |        344853600 |            43526 |        344810074 | finalized | t
```

Three things this shows, in order of how much they matter.

1. The hold was 344,853,600 credits, 0.3449 USD, not the 2,000,000,000 the
   catalog envelope carries. That is what let a 0.455 USD account through.
2. Hold and release balance exactly: 43,526 consumed plus 344,810,074 released
   is 344,853,600 reserved, to the credit. Nothing stranded.
3. The turn actually cost 43,526 credits, 0.0000435 USD. The old hold demanded
   an authorization roughly 46,000 times the real charge before serving it.

`terminal_usage_confirmed` is true, so the charge came from a cost the upstream
really reported, not from the fail-closed path.
