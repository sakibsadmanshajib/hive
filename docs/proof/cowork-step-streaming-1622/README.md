# Cowork steps stream while the run is going, issues #1622 and #1504

Captured on 2026-09-02 against the forked chat front end running in Docker,
three times: once from an image built at `origin/main` with the wire behaving
as `origin/main`'s backend did, once from that same image with the wire
behaving as this branch's backend does, and once from an image built from this
branch. Same harness, same run, same account flow.

The middle run is what separates the two halves of the fix: it shows that
delivering the steps is not enough on its own, because the component that
renders them showed one at a time.

## The three runs

| | Steps visible while the run was going | Most step lines on screen at once | Lines left at the end |
| --- | --- | --- | --- |
| `origin/main` build, `origin/main` wire | 0 of 11 samples | 0 | none, just the summary |
| `origin/main` build, this branch's wire | 5 of 11 samples | 1 | 1 |
| This branch, both | 5 of 11 samples | 3 | 3 |

Three lines rather than six because the two `tool_call` steps collapse into
their results as those arrive, which is `foldRunSteps` closing each open call
in place rather than adding a second line for it, and because the run's closing
message arrives twice, once as a `message` event and once as the task's own
summary, so `dropSummaryEcho` removes the duplicate when the run settles
(#1509). The peak moves between three and four across runs depending on whether
a sample lands in the moment the summary echo is still on screen; the number in
the table is the one in the committed logs.

`capture-*.log` are the runs, with a per sample line naming what the transcript
showed and, at the end, every agent API call the front end made with its
timestamp. `timeline-*.json` is the same sampling as data.

The first row is issue #1504's live observation reproduced: one line reading
"Queued. Waiting for a sandbox.", then nothing at all for the whole run, then a
single assistant message carrying the summary and no step lines.

The third row is the fix: two tool steps and the workspace file rendered as a
chain, appearing one at a time as the run produced them, with the newest
shimmering while its tool call is open.

`timeline-*.json` carries two clocks per sample. `at` is measured with
`performance.now()` from the capture's start, because this runs on WSL2, whose
wall clock is periodically resynchronised against the Windows host and steps
backwards by a few hundred milliseconds when it is. An earlier version of these
artifacts stamped the samples with `Date.now()` and had one or two of them out
of order in consequence, at a different point each run. `since_submit` is the
same monotonic clock measured from the moment the composer's submission landed,
and is null for the samples taken before it did.

## What was real and what was scripted

Real: the built front end, the composer and its mode toggle, the submit path,
`agentTasks.ts`'s fetch and decode, `foldRunSteps`, `applyCoworkRun`, and the
transcript components that render the result. The chat is created and saved
through the front end's own backend, as any conversation is. Both images are
real builds of `deploy/docker/Dockerfile.open-webui`, one per side.

Scripted: the agent API, intercepted in the browser. There is no Apptainer
sandbox on the machine this was captured on. The SIF is linux/amd64 and cannot
be built or launched on WSL2, so a live run is impossible here and the wire is
played back instead.

The two wire shapes are not invented. They are the two behaviours the Go tests
in this change pin, one on each side of it:

* `WIRE=old` makes the step events readable only fifteen seconds after the task
  reports a terminal status. That is what `origin/main` does: the poller writes
  the terminal status, the row leaves the active set, and the event syncer's
  `finishVanished` pulls the tail on its next pass, one
  `HIVE_AGENT_TASK_POLL_INTERVAL` later. The transcript's follower stops at the
  terminal status, so it never asks again.
* `WIRE=new` makes them readable while the run is going, with the last of them
  readable strictly before the terminal status. That is what this branch does:
  the syncer runs on its own two second interval, and the poller flushes the
  session's events immediately before it records the terminal status.

What this capture therefore proves is the half a unit test cannot: that the
front end renders that wire as a growing chain of steps. What it does not prove
is that the backend produces that wire, which is what
`TestPoller_StoresEveryStepBeforeItPublishesATerminalStatus` and its neighbours
are for, and what the live Apptainer launch in the `agent visual proof`
workflow exercises on this pull request.

## Reproducing

`capture.mjs` and `stub.py` are committed here. The rest:

```bash
docker build -f deploy/docker/Dockerfile.open-webui -t hive-owui:proof-1622 .
docker network create proof1622
docker run -d --name proof1622-stub --network proof1622 -e PORT=8000 \
  -v "$PWD/docs/proof/cowork-step-streaming-1622:/work" \
  -w /work python:3.12-alpine python stub.py
# Both of these are generated rather than written down. Neither authenticates
# anything (the upstream is the stub beside this file, which ignores the key),
# but a literal in a documented command reads like a credential and this
# directory is the one the proof-token linter scans, so nothing key-shaped is
# committed here at all.
docker run -d --name proof1622-owui --network proof1622 -p 127.0.0.1:3422:8080 \
  -e "WEBUI_SECRET_KEY=$(openssl rand -hex 16)" -e ENABLE_SIGNUP=true \
  -e OPENAI_API_BASE_URL=http://proof1622-stub:8000/v1 \
  -e "OPENAI_API_KEY=$(openssl rand -hex 16)" -e DEFAULT_MODELS=hive-default \
  hive-owui:proof-1622

# The fork's sign-in page offers no sign-up form, so the account is created
# through the front end's own signup API first. The password is generated here
# and never leaves this throwaway container.
PW="$(openssl rand -hex 12)"
curl -s -X POST http://127.0.0.1:3422/api/v1/auths/signup \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"Proof Runner\",\"email\":\"cowork-proof@hive.invalid\",\"password\":\"$PW\"}"

OWUI_URL=http://127.0.0.1:3422 OUT_DIR=/tmp/proof1622 LABEL=after WIRE=new \
  PROOF_PASSWORD="$PW" node docs/proof/cowork-step-streaming-1622/capture.mjs
```

`@playwright/test` has to resolve for that last line; the capture was run from
`apps/web-console`, which declares it.

The screenshots and the screen recording are attached to the pull request
through `scripts/post-pr-visual-proof.sh` and the release it uploads to. The
logs are here because `npm run lint:proof-tokens` scans this directory and
nothing else.
