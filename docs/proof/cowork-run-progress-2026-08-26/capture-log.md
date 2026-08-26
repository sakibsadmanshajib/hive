# Cowork run progress: capture log

Branch: feat/cowork-run-progress
Date: 2026-08-26
Pull request: #1202 (follow-up to #1193)

## Substrate, stated plainly

The frontend under test is THIS BRANCH, built the way the deploy builds it:
`docker build -f deploy/docker/Dockerfile.open-webui -t hive-owui:runprogress .`
from the worktree, so the bundle in the running container is the production
`npm run build` output of the changed sources, not a dev server. That image's
own build stage ran the Hive frontend unit tests before building: `Test Files
14 passed (14) / Tests 176 passed (176)`.

What is NOT live, and why:

  * The demo box could not carry this change. `deploy-demo-box.yml` for
    4ece687a (#1193, this PR's parent) FAILED, so the deployed box does not
    even have the composer Cowork mode yet, let alone this follow-up.
  * A real agent run cannot be produced on this development box at all. The
    sandbox image is linux/amd64 Apptainer (deploy/apptainer/agent-engine.def)
    and the box is WSL2 with no `/dev/fuse` and no launcher socket, so
    `buildAgentEngine` resolves to `NotConfiguredEngine` and every submitted
    task fails at launch without ever producing an event.
  * A live session against the deployed environment could not be minted
    either: the cloud Supabase project in the checkout's `.env` no longer
    resolves (NXDOMAIN) since the self-hosted migration, and no service-role
    key for the self-hosted stack is available here. No password was set,
    reset or rotated on any account at any point; the standalone mint helper
    was attempted first and failed at DNS.

So the agent sandbox and the control-plane event syncer are stood in for by a
local process (`agent_stub.py`, capture-only, never committed and never
shipped) that serves the six `/api/v1/hive/agent/*` routes and proxies every
other path untouched to the real Open WebUI container. Its wire shapes are not
invented: each one is transcribed from the shipped Go.

  * task shape: apps/edge-api/internal/agenttask/types.go (`Task`)
  * event shape: same file (`Event`: seq, source_event_id, kind, payload,
    created_at), served as `{"events": [...]}` by handler.go
  * per-kind payloads: apps/control-plane/internal/agenttask/eventsync.go
    (`mapSandboxEvent`, `statusEvent`, `fileEvent`)
  * truncation marker: apps/control-plane/internal/agenttask/events.go
    (`capEventPayload` writes `{"truncated": true, "size": N}`)
  * preview cap: same file, `maxPreviewRunes = 2000`, with no marker left
    behind, which is why a preview sitting exactly on the cap is the only
    evidence there is that anything was cut
  * cursor semantics: repository.go `ListEvents` (`seq > $2 ORDER BY seq ASC
    LIMIT $4`), and the proxy's own integer-only validation of `after_seq`
    and `limit` in deploy/docker/owui-patches/hive_agent_proxy.py

Everything from that wire response to the pixels is the shipped read path:
`getTaskEvents` -> `foldRunSteps` -> the turn's `statusHistory` ->
StatusHistory.svelte -> StatusItem.svelte, none of which is stubbed.

## Stack

    docker run -d --name runprogress-owui \
      --add-host=host.docker.internal:host-gateway \
      -p 127.0.0.1:18082:8080 \
      -e ENABLE_OPENAI_API=true \
      -e OPENAI_API_BASE_URL=http://host.docker.internal:8080/v1 \
      hive-owui:runprogress

A throwaway local Open WebUI account was created through the ordinary signup
route on an ephemeral container whose database is discarded with it. No shared
account and no deployed environment was touched.

## What the run showed (screenshots on the pull request)

  * 04-in-flight-t28s: 28 seconds in. One muted line above the turn,
    `Using execute_bash: grep -rn "retry" ./src | head -40`, shimmering
    because that tool call has not returned yet, over `Working on it.`
    Before this change the same moment rendered `Working on it.` and nothing
    else, for the whole run.
  * 06-in-flight-expanded-t58s: 58 seconds in, step list expanded. Seven
    lines, including `Used str_replace_editor: File created.` (a tool call
    joined to its result on tool_call_id), `Workspace file: notes.md`, and
    `An update too large to show here (82134 bytes).` for the payload the
    backend replaced with its truncation marker.
  * 07-reloaded-mid-run-t65s: the page reloaded while the run was still
    going. Every line survived, the turn is still in flight, and the follower
    resumed from the stored cursor rather than re-reading from zero (see the
    request log below).
  * 08-settled-t93s: the run settled. Eight lines, none shimmering, and the
    run's own summary as the turn's content.

## The cursor, from the browser's own request log

Every request the page made to the agent surface, in order. Note that
`/agent/tasks` (the whole-list read this change removed from the follower)
appears exactly once, as the POST that creates the run.

```
2026-08-26T00:43:52.766Z POST /api/v1/hive/agent/tasks
2026-08-26T00:43:55.942Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:43:55.948Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=0&limit=200
2026-08-26T00:43:59.140Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:43:59.147Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=0&limit=200
2026-08-26T00:44:02.369Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:44:02.374Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=2&limit=200
2026-08-26T00:44:03.411Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:44:03.416Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=2&limit=200
2026-08-26T00:44:06.674Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:44:06.694Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=3&limit=200
2026-08-26T00:44:09.875Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:44:09.881Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=3&limit=200
2026-08-26T00:44:13.072Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:44:13.079Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=4&limit=200
2026-08-26T00:44:16.262Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:44:16.266Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=5&limit=200
2026-08-26T00:44:19.580Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:44:19.581Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=5&limit=200
2026-08-26T00:44:22.763Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:44:22.770Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=5&limit=200
2026-08-26T00:44:25.944Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:44:25.949Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=6&limit=200
2026-08-26T00:44:29.132Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:44:29.137Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=7&limit=200
2026-08-26T00:44:30.127Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:44:30.132Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=8&limit=200
2026-08-26T00:44:33.338Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:44:33.343Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=8&limit=200
2026-08-26T00:44:36.531Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:44:36.537Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=8&limit=200
2026-08-26T00:44:39.789Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:44:39.797Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=8&limit=200
2026-08-26T00:44:42.980Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:44:43.015Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=9&limit=200
2026-08-26T00:44:46.186Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:44:46.191Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=10&limit=200
2026-08-26T00:44:49.476Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:44:49.482Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=10&limit=200
2026-08-26T00:44:54.204Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:44:54.239Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=10&limit=200
2026-08-26T00:44:57.427Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:44:57.433Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=10&limit=200
2026-08-26T00:44:58.558Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:44:58.563Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=10&limit=200
2026-08-26T00:45:01.729Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
```

... 6 further polls, omitted here, cursor continuing to advance ...

The page was reloaded mid-run at 00:44:54, and the request either side of it
is the evidence for the resume: the cursor did NOT restart at 0. It continued
from 10, read off the lines already stored on the turn by `latestStepSeq`.

```
2026-08-26T00:45:14.478Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:45:14.484Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=11&limit=200
2026-08-26T00:45:17.662Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:45:17.666Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=12&limit=200
2026-08-26T00:45:20.849Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b
2026-08-26T00:45:20.855Z GET /api/v1/hive/agent/tasks/ea9fce9a-648f-4265-8fa3-d33dbac10d6b/events?after_seq=12&limit=200```

Polling stops there, at the first terminal reading, rather than continuing
against a settled task.

## Counts

    $ grep -c 'agent/tasks$' network.log        # whole-list reads: the create POST only
    1
    $ wc -l < network.log                       # every agent request the page made
    58
