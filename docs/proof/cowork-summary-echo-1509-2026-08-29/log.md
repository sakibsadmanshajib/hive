# A finished Cowork run's summary rendered once: capture log

Branch: `fix/1509-cowork-summary-echo`
Issue: #1509
PR: #1522
Date: 2026-08-29

## Substrate, stated plainly

Two standalone containers on the development box, each built from a real tree
with `docker build -f deploy/docker/Dockerfile.open-webui .`:

- `hive-owui:proof-1502` on port 3099, built from a tree WITHOUT this fix. It is
  the before frame. (That image carries the unrelated navigation change for
  issue #1502; nothing in it touches the Cowork projection, which is why it
  serves as the control.)
- `hive-owui:proof-1509` on port 3098, built from THIS branch. It is the after
  frame.

Not the demo box, and not a compose stack. Both containers were deleted after
the run.

This box holds no Supabase credentials (`SUPABASE_URL`, `SUPABASE_ANON_KEY`,
`SUPABASE_SERVICE_ROLE_KEY` and `SUPABASE_DB_URL` are all empty in `.env`), so
no capture here touches any deployed environment, and the live strings behind
the reported run could not be read out of the deployed database.

The account is local to the throwaway containers, created through
`POST /api/v1/auths/signup`. Its password was generated at run time and is
deliberately not recorded here. No URL in this capture carries a credential in
its query string.

## What is real in this capture and what is standing in

A genuinely live Cowork run cannot be produced on this box: it needs the control
plane, the unprivileged host launcher and an Apptainer sandbox, none of which a
standalone chat container has. So the run's two server-side reads are stubbed at
the browser, and everything downstream of them is the real shipped bundle.

Stubbed (Playwright request interception on `**/api/v1/hive/agent/tasks/**`):

- `GET /tasks/{id}` returns a succeeded task whose `result_summary_ref` is the
  agent's closing message.
- `GET /tasks/{id}/events?after_seq=N` returns five events, honouring the
  cursor: two `tool_call` and two `tool_result` pairs, then one assistant
  `message` whose `preview` is that same closing message with its content blocks
  joined by single spaces, which is what `normalizeEvent` in
  `apps/agent-engine/internal/controlclient/events.go` produces.

Real, and exercised end to end by the capture: `getTask`, `getTaskEvents`,
`decodeEvent`, `describeEvent`, `foldRunSteps`, `renderRun`, `runTurnIsDone`,
`settleRunSteps`, `dropSummaryEcho`, `resumeCoworkRun` and `applyCoworkRun`,
plus `ResponseMessage.svelte`, `StatusHistory.svelte` and `StatusItem.svelte`.

The stored conversation is a turn left mid-flight exactly as `submitCoworkRun`
writes one (content `Working on it.`, `done: false`, `hive_agent_task_id` set,
no steps yet), so opening it drives the resume path rather than rendering a
hand-written result. Nothing about the finished state is written into the
fixture; the transcript below is produced by the code under test.

## 1. Before, on the image without the fix (port 3099)

Read from the DOM after the turn settled:

```
steps: ["Created `sixcap.txt` with the text `HIVE-COWORK-OK` and displayed its contents: ``` HIVE-COWORK-OK ```"]
body : "Created `sixcap.txt` with the text `HIVE-COWORK-OK` and displayed its contents: ``` HIVE-COWORK-OK ```

        Created sixcap.txt with the text HIVE-COWORK-OK and displayed its contents:
        Collapse Copy 1 HIVE-COWORK-OK"
```

The collapsed step list shows exactly one line and that line is the summary,
because `StatusHistory.svelte` renders only `history.at(-1)` while collapsed and
the echo is by construction the last step. The body underneath repeats it.
Screenshot `i1509-02-before-livepath.png`.

An earlier frame, `i1509-01-before-double.png`, shows the same doubling on the
same image from a conversation whose steps were stored rather than fetched. It
is kept because it is the shape the issue's own capture has.

## 2. After, on the image built from this branch (port 3098)

Same fixture, same stubbed reads, same viewport:

```
steps: ["Used bash: HIVE-COWORK-OK"]
body : "Used bash: HIVE-COWORK-OK

        Created sixcap.txt with the text HIVE-COWORK-OK and displayed its contents:
        Collapse Copy 1 HIVE-COWORK-OK"
```

The sentence appears once. The collapsed step line is now the last real tool
step, which is what D-045 describes a muted line as being. The four tool steps
are untouched: only the echo went.
Screenshot `i1509-03-after-livepath.png`.

Both screenshots are posted on PR #1522 through
`scripts/post-pr-visual-proof.sh`.

The after container's sidebar still shows a `Knowledge` row, and that is
correct rather than a defect in this capture: this branch is cut from `main` and
carries none of PR #1516's navigation change. It is incidental evidence that the
two changes are independent.

## What is still owed

A capture of a genuinely live Cowork run on the deployed demo box, after this
merges and deploys. The two stubbed reads above are the only part of the chain
this box cannot exercise, and if the doubling survives there, the difference is
in those two strings, not in the projection this capture verified.

## Tests

`scripts/test-owui-hive-frontend.sh`: 20 files, 272 tests passed. The same
sources ran again inside both image builds (`npm run test:frontend -- --run`),
which printed `Test Files 20 passed (20)` and `Tests 272 passed (272)` on the
`proof-1509` build and would have failed it otherwise.

## Capture time versus branch head

Both frames were taken from images built at commit `0afa411ae`, before this
branch merged current `main` (which brought in #1518's composer pack selector,
among others) and before the follow-up comment-only commit answering a review
note. Neither changes the projection under test: the merge touches the composer
row, the stores and the chat CSS, and the follow-up edits a doc comment only.
The frontend unit suite was rerun on the merged tree and reports 20 files, 285
tests passed, up from 272 because the merged work brought its own tests. The
frames are kept rather than retaken, and their provenance is recorded here
rather than left implicit.
