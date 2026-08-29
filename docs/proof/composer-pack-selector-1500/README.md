# Visual proof: composer pack selector (issue #1500, PR #1518)

## What was captured, and against what

The Hive chat image built from this pull request's branch
(`deploy/docker/Dockerfile.open-webui`, tag `hive-owui:pr1500`), run standalone
with `docker run`. That is the proof harness `Dockerfile.open-webui` documents
for itself: "it is how this repo builds its own before/after proof
screenshots". The frontend bundle under test is the real one the deploy ships,
built by the same `npm run build` the deploy runs.

## What is real here and what is stubbed, stated plainly

Real: the whole frontend. The container, the built bundle, the composer, the
new radiogroup, the click and keyboard handling, the store, and the request
body the composer puts on the wire.

Stubbed: two responses, and only for step 6. The model list, because
`submitHandler` in `Chat.svelte` refuses a submission before it reaches the
Cowork branch when no model is selected, and a standalone container has no
provider configured. And the response to `POST .../hive/agent/tasks`, because
there is no edge-api behind this container to answer it. Neither stub touches
the code this pull request changed: what step 6 records is the body the real
frontend built and sent, which is the half that was broken.

Not captured here, and deliberately not faked: an `agent_tasks` row with
`pack = coding-pack` created end to end through the deployed stack. That
after-state does not exist until this merges and `deploy-demo-box` runs, so it
is captured after the deploy rather than substituted with a stale or unrelated
frame.

## The measurement that matters

Issue #1500 recorded, against the live box, that a query for clickable
elements matching "Knowledge work" returned a count of 0, so no pack could be
chosen and every composer submission ran as `knowledge-work-pack`. The same
query re-run against this build returns 1, alongside a count of 1 for
"Coding", and the request body carries `"pack":"coding-pack"` after the Coding
segment is clicked.

## Credentials

None. No URL in this capture carries a credential in a query string or
fragment, so there is nothing to redact in either the log text or the
screenshot pixels. The throwaway local admin account exists only inside a
container with an empty SQLite database that is discarded after the run; its
password is generated per run inside the capture script, is never written to
this log, and the signup form is never screenshotted. No shared or fixture
account was touched and no password anywhere was set, reset or rotated.

## Files

- `capture.log` — the full transcript of the run, including every counted
  query and the request body.

The screenshots are attached to PR #1518 as inline images through
`scripts/post-pr-visual-proof.sh`, which uploads them to the permanent
`visual-proof-assets` release rather than to a branch that merge will delete.
