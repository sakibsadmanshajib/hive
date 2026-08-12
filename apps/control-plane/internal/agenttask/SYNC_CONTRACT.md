# Agent task sync contract (issue #311, blueprint Step 3.4)

Server-side backing store so a task started in one web session is visible
and resumable from another web session, tenant-scoped. This is the contract
the Wave 4 desktop consumer attaches to; nothing here is desktop-specific.

## State machine

```
queued ──> running ──> succeeded
  │           │
  │           └───────> failed
  │
  └────────────────────> cancelled
```

`queued` and `running` are the only states `cancel` accepts from. `succeeded`,
`failed`, and `cancelled` are terminal: no further transition is allowed
(`ErrTerminalState`).

## Wire shapes (JSON)

Task, as returned by every endpoint below:

```json
{
  "id": "uuid",
  "pack": "coding-pack | knowledge-work-pack",
  "instructions": "string, empty when none were given",
  "status": "queued | running | succeeded | failed | cancelled",
  "engine_session_ref": "string, empty until running",
  "result_summary_ref": "string, empty until succeeded (or failed with partial output)",
  "error_message": "string, empty unless failed",
  "created_at": "RFC3339",
  "updated_at": "RFC3339",
  "started_at": "RFC3339 | null",
  "finished_at": "RFC3339 | null"
}
```

`instructions` (issue #311 contract gap, closed alongside issue #305's Engine)
is the free-text prompt/goal the task's conversation starts from — passed as
the agent-server conversation's `initial_message` (see the Engine seam
section below). Optional: an empty string means the task carries no prompt.
Stored as a nullable column (`public.agent_tasks.instructions`); the read
path always returns `""` for `NULL`, never `null`, so every consumer of this
contract can treat it as a plain string. Skills (issue #300) route on a
`"Skill: X"` prefix inside this text; no separate skill field exists.

`tenant_id` and `user_id` never appear in a response body: both are implied
by the authenticated caller, never round-tripped.

## Edge-api surface (customer-facing, auth required, gated by feature
`ENABLE_COWORK`)

| Method | Path | Body | Notes |
|---|---|---|---|
| POST | `/v1/agent/tasks` | `{"pack": "...", "instructions": "..."}` | 201 with the created Task in `queued`; `instructions` is optional |
| GET | `/v1/agent/tasks` | — | `{"tasks": [Task, ...]}`, newest first, scoped to the caller |
| GET | `/v1/agent/tasks/{id}` | — | 404 if the task belongs to a different user or does not exist |
| POST | `/v1/agent/tasks/{id}/cancel` | — | 409 if the task already reached a terminal state |

Status is read by polling `GET /v1/agent/tasks/{id}`. No SSE/websocket
channel ships in this step — the existing SSE pattern in this repo (see
`apps/edge-api/internal/anthropic/stream.go`) is a provider-response
translator wired around one in-flight LLM call, not a general task-status
push channel, and building one is out of scope here. If a live-updating
panel needs push updates later, add a `GET /v1/agent/tasks/{id}/events` SSE
endpoint that polls the same store server-side and streams deltas; the Task
shape above does not change.

## Control-plane internal surface (shared-secret `X-Internal-Token`, not
customer-facing)

`tenant_id` and `user_id` travel as URL path segments, never body/query/
header, mirroring `apps/control-plane/internal/marketplace`'s internal read
surface — the process boundary means edge-api resolves both from the
caller's authenticated request context before building the path, never from
untrusted client input.

| Method | Path | Body |
|---|---|---|
| POST | `/internal/agent-tasks/{tenant_id}/{user_id}` | `{"pack": "...", "instructions": "..."}` |
| GET | `/internal/agent-tasks/{tenant_id}/{user_id}` | — |
| GET | `/internal/agent-tasks/{tenant_id}/{user_id}/{task_id}` | — |
| POST | `/internal/agent-tasks/{tenant_id}/{user_id}/{task_id}/cancel` | — |

## Create is asynchronous over the launch (issue #881)

`POST` returns the persisted `queued` task as soon as the row exists. The
sandbox launch runs on a background goroutine and moves the task to `running`
(or to `failed`, with a sanitized `error_message`) on its own; callers learn
the outcome from the same poll they already use for every later state change.

Create used to block on the launch, which is bounded at five minutes because a
cold sandbox mount routinely takes tens of seconds. edge-api's control-plane
client gives up at 15 seconds, so a create measured live on 2026-08-11 answered
`500` after 18.0 seconds for a task that reached `succeeded`. Aligning the two
timeouts was rejected: it would make an interactive request legitimately able
to hang for five minutes, and every intermediate proxy is free to cut it
anyway.

## Cancel stops the launcher session (issue #886)

`Service.Cancel` transitions the row first, because that UPDATE is the atomic
gate that decides which caller owns the cancellation, and only the winner then
calls `Engine.Cancel(ctx, engine_session_ref)`. Stopping the session is what
frees the launcher's per-tenant and per-user concurrency slot
(`apps/agent-engine/internal/quota`): the slot belongs to the live sandbox and
is released when that session ends, so a cancel that only wrote a row left the
slot held until the sandbox finished on its own, about sixteen minutes on the
demo box. Two cancels therefore exhausted `HIVE_QUOTA_USER_CONCURRENCY` and
every later create was refused.

Orderings, all three settled deliberately:

- **Double cancel.** The second loses the row's terminal guard, returns
  `ErrTerminalState` (HTTP 409) and never reaches the engine.
- **Cancel racing a completion.** Whichever transition commits first wins. If
  the poller already recorded a terminal status, the cancel is rejected and
  the engine is left alone, which is correct because a terminal status is
  exactly what makes the launcher reap that session itself.
- **Cancel before the launch finishes.** The row has no
  `engine_session_ref` yet, so there is nothing to stop at cancel time. The
  in-flight launch goroutine finds the task already terminal when it tries to
  record `running`, and stops the session it just started. The same path
  covers a launch whose `running` transition fails for any other reason: an
  unrecorded session can never be polled or cancelled by anything else, so it
  is torn down rather than left holding a slot.

An engine stop failure is logged for the operator and never returned to the
caller: the cancellation itself is already recorded, and a stuck launcher is
not something a customer can act on.

## Engine seam

`Service.CreateTask` hands the task to `Engine.Launch(ctx, task)` right after
persisting a `queued` row, on a background goroutine. Issue #305 closes the control-channel half of this gap:
`apps/agent-engine/internal/sandbox` bind-mounts a second Unix socket (the
control channel) alongside the existing egress-proxy one, so the host can
now reach the agent-server's REST API inside the sandbox
(`apps/agent-engine/internal/controlclient`), and
`apps/agent-engine/internal/engine.SandboxEngine` composes that into a full
Launch/Status/Cancel session lifecycle, mapped onto this package's
queued/running/succeeded/failed/cancelled vocabulary.
`apps/agent-engine/engineapi` re-exports that type for cross-module use (Go's
internal-package visibility does not cross module boundaries, the same
limitation `apps/agent-engine/internal/egressclient`'s doc comment already
covers), and `apps/control-plane/internal/agentengine.Engine` adapts it to
this package's `Engine` interface, translating `agenttask.Task` (including
`Instructions`, passed through as the conversation's `initial_message`) into
`engineapi.Task` and back.

That adapter is wired in `cmd/server/main.go` only when
`HIVE_AGENT_ENGINE_SIF_PATH` is set: the real `SandboxEngine` execs
Apptainer, which requires an Apptainer install and a built SIF on whatever
host runs this process — not true of every `control-plane` deployment today
(task tracked separately: "Live Apptainer validation of agent-engine on
x86-64 host"). Without that env var, `NotConfiguredEngine` is still wired,
and the background launch transitions the task to `StatusFailed` with a
sanitized generic customer message. Callers receive HTTP 201 with the queued
task and see the failure on their next read, rather than polling a task that
will never move. Startup logs a WARN naming each empty
`HIVE_AGENT_ENGINE_*` variable individually. Neither the `Service` nor the
HTTP surface changes in this scenario; the only difference is the `Engine`
implementation passed to `NewService`.

**Implemented** (issue #311 follow-up): `Poller` (`poller.go`) periodically
advances every active task past `running`. It lists queued/running tasks
with a non-empty `engine_session_ref` across every tenant
(`Repository.ListActive`, cross-tenant by design — see
`20260716_05_agent_tasks_service_scan.sql`'s `agent_tasks_service_scan`
policy), calls `StatusChecker.Status(ctx, sessionRef)` for each (a narrow
interface `apps/control-plane/internal/agentengine.Engine` already
satisfies structurally, no adapter code needed), and atomically
`Repository.Transition`s the terminal ones. A per-task engine error is
logged and left for the next pass (retried, not failed); a concurrent
`Cancel` winning the same task's `Transition` race returns
`ErrTerminalState`, silently swallowed. The loop backs off exponentially
(capped at 5 minutes) after a pass that had any error, resetting to the
configured interval on the next clean pass.

Wired in `cmd/server/main.go` behind the same `HIVE_AGENT_ENGINE_SIF_PATH`
gate as the `Engine` itself (the poller needs a real `StatusChecker`, which
only exists once the real `SandboxEngine` is configured) — interval via
`HIVE_AGENT_TASK_POLL_INTERVAL` (Go duration string, default 15s), bound to
the same process-lifetime context the other background workers use so it
stops cleanly on shutdown.
