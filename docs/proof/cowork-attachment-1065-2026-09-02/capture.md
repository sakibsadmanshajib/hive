# Issue 1065, Cowork half: a file attached in the composer reaches a run, captured 2026-09-02

## Substrate, stated plainly

The chat image built from this branch (`hive-owui:proof-1065`, built by
`deploy/docker/Dockerfile.open-webui`) running as a single container on
`http://127.0.0.1:13650`, with a fresh database and no prior state.

It is not the deployed box, because the deployed box serves `main` and this
change is not merged. It is not the full compose stack either. What the
container is missing is the Hive gateway and, behind it, the agent launcher, so
the responses to `/api/v1/hive/agent/tasks` are intercepted in the browser and
answered with a queued task. That interception is what makes the run turn
render at all in a container with nothing behind it.

Read that limitation exactly as written. **Nothing in these frames claims the
sandbox ran, and nothing claims the file exists inside it.** The intercepted
`files` response is deliberately an empty list rather than an invented working
folder. What the frames prove is the half that lives in the browser: the
composer accepts the attachment in Work mode, where it used to refuse it
outright, and the request that leaves the browser carries the document's text.
The other half, the launcher writing that text into the directory a sandbox
bind mounts as `/workspace`, is proven by the Go tests quoted at the end,
because Apptainer is `linux/amd64` only and cannot run on this development box
at all.

`WEBUI_AUTH=false`, so the run needs no identity. No shared test account was
used, and no password was set, reset or rotated. The one connected model is a
local stub that serves a models list and nothing else, because Open WebUI
refuses to send while no model is selected and Work mode never calls a model.

## Redaction

Nothing to redact. Every URL recorded below is a bare path on `127.0.0.1`, no
flow in this capture carries a credential in a query string, and the instance
runs with authentication disabled so no session token exists to leak. The one
`OPENAI_API_KEYS` value is the literal string `proof-key`, which authenticates
nothing.

## The state before this change

`submitHandler` refused the send with *"Attachments are not supported in Work
mode yet. Remove the file, or switch to Chat mode to send it."* The refusal was
accurate: `createTask` sent a pack and a prompt, `POST /v1/agent/tasks` decoded
a pack, a prompt and a project id, and the launch body carried no document. The
string is now absent from the component and its absence is pinned by
`coworkAttachments.test.ts`, which is why no before-frame is included: the
before state is a toast, and #1065 already records it.

## Frame 01, the composer accepts the attachment in Work mode

`01-work-mode-accepts-the-attachment.png`. The mode toggle reads **Work**, the
pack row reads **Knowledge work**, the chip reads `service-record.txt 85.0 B`,
and the prompt is typed. No refusal toast. On `main` this exact state is
unreachable: the attachment could be added and the send could not be made.

## Frame 02, the run carries it

`02-run-carries-the-attachment.png`. The user turn renders the same
`service-record.txt` chip a chat turn renders, and the run below it reads
*"Working on it."* The chip on the turn is deliberate: without it the file
would disappear from the composer on send and appear nowhere else, which reads
as the attachment having been dropped.

## What the browser actually sent

Recorded from the intercepted request, not inferred from the screenshot. The
document contains one string that exists nowhere else, `BRACKEN-1065-QX`.

```
intercepted POST /api/v1/hive/agent/tasks (258 bytes of body)
request carried 1 attachment(s)
  name=service-record.txt bytes=85
  content carries the unique code BRACKEN-1065-QX: true
  content: "Hive demo box service record.\n\nRack asset tag: BRACKEN-1065-QX\nLocation: lab shelf 2\n"
request pack: knowledge-work-pack
request instructions: "Read the attached service record and state the rack asset tag."
```

Full log: `capture.run.log`.

## The far end, proven where it can be

The launcher's half runs entirely inside `apps/agent-engine/internal/engine`
and is exercised without Apptainer, the same way the pack materialization tests
introduced by issue #1360 are. These read the bytes back out of the directory a
launch bind mounts as `/workspace`, rather than asserting the launch payload
carried them, which would have proven nothing.

```
$ go test ./apps/agent-engine/internal/engine/... -count=1 -v -run Attachment
--- PASS: TestSandboxEngine_Launch_WritesAttachmentsIntoTheAgentWorkingDir (0.07s)
--- PASS: TestSandboxEngine_Launch_AttachmentsAppearInTheWorkingFolderListing (0.01s)
--- PASS: TestSandboxEngine_Launch_AttachmentNeverReplacesAPackFile (0.01s)
--- PASS: TestSandboxEngine_Launch_RejectsAttachmentNamesThatAreNotFileNames (0.09s)
--- PASS: TestSandboxEngine_Launch_TellsTheAgentWhichFilesAreAttached (0.03s)
--- PASS: TestSandboxEngine_Launch_LeavesTheInitialMessageAloneWithoutAttachments (0.02s)
PASS
ok  	github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/engine	0.268s
```

The seam between them, the `/launch` body itself, is covered by
`TestRemoteLaunchCarriesAttachments` in
`apps/control-plane/internal/agentengine/remote_test.go`, which decodes what a
fake daemon received.

## Unit guard, in the image build

The browser-side module runs in `scripts/test-owui-hive-frontend.sh` and again
in place inside the image build, so an edit that drops the wiring fails the
build rather than leaving a green suite over a dead feature.

```
#54 [frontend 8/9] RUN npm run test:frontend -- --run
#54 20.35  ✓ src/lib/hive/coworkAttachments.test.ts  (17 tests) 278ms
#54 25.26  Test Files  27 passed (27)
```

## Reproduction

```bash
docker build -f deploy/docker/Dockerfile.open-webui -t hive-owui:proof-1065 .
python3 models-stub.py &   # serves one model at :13651/v1/models, nothing else
docker run -d --name owui1065 -p 13650:8080 \
  --add-host=host.docker.internal:host-gateway \
  -e WEBUI_AUTH=false -e ENABLE_OLLAMA_API=false -e ENABLE_OPENAI_API=true \
  -e OPENAI_API_BASE_URLS="http://host.docker.internal:13651/v1" \
  -e OPENAI_API_KEYS="proof-key" -e DEFAULT_MODELS="proof-model" \
  -e WEBUI_SECRET_KEY=proof1065 hive-owui:proof-1065
# then drive the capture: switch the composer to Work, attach a text file with
# a unique code in it, type a prompt, send, and read the intercepted body
```

## The sandbox half, proven in CI on a real Apptainer launch

Added after the frames above, because the limitation they were captured under
turned out to be a limitation of this development box and not of CI.
`.github/workflows/agent-visual-proof.yml` stands the real thing up per run,
checked out from `refs/pull/1735/merge`, and a scenario was added to its
harness and dispatched at this pull request. Run 33668985745, success:

```
[capture-live] amended the create with 1 attachment (66 bytes)
[capture-live] create answered HTTP 201 in 90ms, status=queued
[capture-live] the sandbox workspace holds service-record.txt carrying HIVE-1065-68985745
[capture-live] row "proof-33668985745 attachment: read servi..." reached Running
[capture-live] GET /v1/agent/tasks/{id}/files answered HTTP 200:
  {"files":[{"name":".git","size":4096,"mtime":"2026-09-02T18:52:40Z"},
            {"name":"service-record.txt","size":66,"mtime":"2026-09-02T18:52:29Z"}]}
[capture-live] scenario attachment-reaches-the-sandbox: ok
```

The file was read off the launcher's own workspace directory and asserted to
carry a string generated for that run and present nowhere else, and the
working folder was read through the customer route the panel itself calls.
Both halves of the issue's Cowork acceptance criterion, on a real launch.

Two false negatives came first and are recorded in the pull request discussion
because each looks exactly like the bug: an unauthenticated raw fetch reading
as a missing file, and a correct empty listing one second after create, before
the row leaves queued, reading as a missing file as well.

## What the demo box will show after merge

The path the frames stop short of. A run submitted from Work mode with a file
attached should land that file in `HIVE_AGENT_ENGINE_WORKSPACE_ROOT/<task-id>/`
on the box, list it in the run's Working folder panel, and have the agent read
it. The deploy workflow rebuilds and restarts the launcher on any
`apps/agent-engine/**` change, so both halves move together on that push.
