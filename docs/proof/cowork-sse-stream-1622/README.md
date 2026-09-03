# A Cowork run's steps arrive over SSE, as the run takes them (issue #1622)

Captured on 2026-09-02 against the forked chat front end running in Docker,
twice, from the same image built from this branch. The only difference between
the two runs is whether the agent API serves the subscription this change adds
or answers it 404, which is what a deployment without this change does.

## What the two runs measured

Every step is timed twice: when the run took it, and when it first appeared in
the transcript. The clock is the wire's own, read from it after the submission
landed rather than taken from the keypress, because the composer creates the
chat through the front end's backend first and on a cold container that gap is
six seconds, which would be added to every step in both runs equally.

| | Worst a step was late | Mean | Steps that never appeared as their own line | Steps sharing an arrival |
| --- | --- | --- | --- | --- |
| Subscription (`MODE=stream`) | 3.0s | 0.8s | none | none |
| Cursor read, the fallback (`MODE=poll`) | 6.4s | 4.3s | two of seven | two pairs |

Six of the subscription's seven steps were under 0.7s late, which is about the
floor this harness can measure: the wire writes on a 500ms tick and the capture
samples the DOM on a 500ms tick. The single 3.0s outlier is the run's last step,
which lands next to the terminal transition and a screenshot pause.

The last two columns are the part that is visible rather than merely faster.
Under the cursor read the transcript moves in lumps at the poll boundary: one
read returned three steps at once, and they appeared in the same frame. Worse,
two of the seven steps never appeared at all. A tool call and its result that
arrive in the same read fold into one line before anything renders, because
`foldRunSteps` closes an open call in place rather than adding a second line for
it, so the call itself is never seen. Under the subscription each step arrives
on its own and each one is shown.

`wire-poll.log` is that as data, one cursor read every three and a half seconds
with two of them returning three steps each. `wire-stream.log` is the same run
as a single connection opened at `after_seq=0` and written frame by frame, with
one cursor read at the very end: the follower's settle read after the stream
closed.

## The screenshots

The pair to compare is `stream-03-run-t6p5s.png` against
`poll-03-run-t6p5s.png`, both taken 6.5 seconds into the same run. The
subscription shows the steps the run had taken by then. The cursor read shows
one, still open, with more already taken and not yet on screen.

`03-run-t*.png` is a sequence of stills at increasing run times for each mode,
which is what makes incremental appearance visible in stills rather than only
in a recording. The recordings are in `video-stream/` and `video-poll/` in the
capture output directory and are linked from the pull request rather than
committed.

## What was real and what was scripted

Real: the built front end, the composer and its mode toggle, the submit path,
`agentTasks.ts`'s fetch, its SSE parser, `foldRunSteps`, `applyCoworkRun`, the
transcript components, and the HTTP stream itself, which is a genuine chunked
response the browser reads incrementally. The chat is created and saved through
the front end's own backend, as any conversation is. The image is a real build
of `deploy/docker/Dockerfile.open-webui` from this branch.

Scripted: what the sandbox did. There is no Apptainer on the machine this was
captured on. The SIF is linux/amd64 and cannot be built or launched on WSL2, so
a live run is impossible here and the run is played back by `agent-wire.mjs`.

`agent-wire.mjs` is a real HTTP server in front of the real chat container
rather than a browser interception, and that is the difference from the sibling
capture for PR #1709 (`docs/proof/cowork-step-streaming-1622`). That one uses
Playwright's `route.fulfill`, which sends a body it has already finished
writing. The claim here is that the body arrives in pieces, so a capture built
on `fulfill` would be measuring the thing this change stops doing.

The frame vocabulary is not invented. It is what
`apps/control-plane/internal/agenttask/stream.go` writes, including the
ordering: the status frame first, then the steps, then one end frame, and the
pass that observes a terminal status still drains the steps before it sends
that end frame. `TestHandler_EventStream_DrainsTheFinalStepsBeforeItEnds` is
the server-side pin for that, and it goes red when the drain is moved after the
end.

What this capture proves is the half a unit test cannot: that the front end
renders a live stream as a chain that grows while the run works, and that it
falls back rather than stranding a run when the stream is unavailable. What it
does not prove is that control-plane produces that wire from a live sandbox.
That is what the Go tests in this change are for, and what the live Apptainer
launch in the `agent visual proof` workflow exercises on this pull request.

## Reproducing

```bash
docker build -f deploy/docker/Dockerfile.open-webui -t hive-owui:proof-1622-sse .

# The upstream model list. The chat front end needs a non-empty one before its
# composer will send anything. stub.py is the sibling capture's, reused.
PORT=8000 python3 docs/proof/cowork-step-streaming-1622/stub.py &

# Both generated rather than written down. Neither authenticates anything (the
# upstream is the stub above, which ignores the key), but a literal in a
# documented command reads like a credential and this directory is the one the
# proof-token linter scans.
docker run -d --name proof1622sse-owui --add-host=host.docker.internal:host-gateway \
  -p 127.0.0.1:3422:8080 \
  -e "WEBUI_SECRET_KEY=$(openssl rand -hex 16)" -e ENABLE_SIGNUP=true \
  -e OPENAI_API_BASE_URL=http://host.docker.internal:8000/v1 \
  -e "OPENAI_API_KEY=$(openssl rand -hex 16)" -e DEFAULT_MODELS=hive-default \
  hive-owui:proof-1622-sse

# The fork's sign-in page offers no sign-up form, so the account is created
# through the front end's own signup API. The password is generated here and
# never leaves this throwaway container.
PW="$(openssl rand -hex 12)"
curl -s -X POST http://127.0.0.1:3422/api/v1/auths/signup \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"Proof Runner\",\"email\":\"cowork-proof@hive.invalid\",\"password\":\"$PW\"}"

# One run per mode, restarting the wire in between.
MODE=stream PORT=3423 OWUI_URL=http://127.0.0.1:3422 \
  node docs/proof/cowork-sse-stream-1622/agent-wire.mjs &
APP_URL=http://127.0.0.1:3423 OUT_DIR=/tmp/proof-1622-sse LABEL=stream \
  PROOF_PASSWORD="$PW" node docs/proof/cowork-sse-stream-1622/capture.mjs

# The control: the same run with the subscription answered 404, which is what a
# deployment without this change does. Stop the wire above first. Both modes
# listen on the same port, and a stale one still serving the stream would make
# the control silently a second copy of the first run.
MODE=poll PORT=3423 OWUI_URL=http://127.0.0.1:3422 \
  node docs/proof/cowork-sse-stream-1622/agent-wire.mjs &
APP_URL=http://127.0.0.1:3423 OUT_DIR=/tmp/proof-1622-sse LABEL=poll \
  PROOF_PASSWORD="$PW" node docs/proof/cowork-sse-stream-1622/capture.mjs
```

`@playwright/test` has to resolve for that last line. It is declared by
`apps/web-console`, and Node resolves a bare specifier from the importing
file's directory upwards, so running from that directory is not enough: the
package has to be reachable from `docs/proof/` upwards, which on this machine
meant a link into the repository root's `node_modules`.
